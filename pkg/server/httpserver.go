package server

import (
  "context"
  "fmt"
  . "goto/pkg/constants"
  "goto/pkg/events"
  "goto/pkg/global"
  "goto/pkg/metrics"
  "goto/pkg/registry/peer"
  "goto/pkg/server/intercept"
  "goto/pkg/server/listeners"
  "goto/pkg/util"
  "io/ioutil"
  "log"
  "net"
  "net/http"
  "os"
  "os/exec"
  "os/signal"
  "strconv"
  "strings"
  "syscall"
  "time"

  "github.com/gorilla/mux"
  "golang.org/x/net/http2"
  "golang.org/x/net/http2/h2c"
)

var (
  httpServer *http.Server
  h2s        = &http2.Server{}
)

func RunHttpServer(handlers ...util.ServerHandler) {
  r := mux.NewRouter()
  util.InitListenerRouter(r)
  r.Use(ContextMiddleware)
  r.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
    util.WriteJsonPayload(w, map[string]string{"version": global.Version, "commit": global.Commit})
  })
  for _, h := range handlers {
    if h.SetRoutes != nil {
      h.SetRoutes(r, nil, r)
    }
    if h.Middleware != nil {
      r.Use(h.Middleware)
    }
  }
  h2 := HTTPHandler(r, h2c.NewHandler(GRPCHandler(r), h2s))
  httpServer = &http.Server{
    Addr:         fmt.Sprintf("0.0.0.0:%d", global.ServerPort),
    WriteTimeout: 1 * time.Minute,
    ReadTimeout:  1 * time.Minute,
    IdleTimeout:  1 * time.Minute,
    ConnContext:  withConnContext,
    Handler:      h2,
    ErrorLog:     log.New(ioutil.Discard, "discard", 0),
  }
  StartHttpServer(httpServer)
  listeners.StartInitialListeners()
  RunStartupScript()
  peer.RegisterPeer(global.PeerName, global.PeerAddress)
  events.SendEventJSONDirect("Server Started", global.HostLabel, listeners.GetListeners())
  WaitForHttpServer(httpServer)
}

func HTTPHandler(httpHandler, h2cHandler http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    ctx, rs := util.WithRequestStore(r)
    r = r.WithContext(ctx)
    if util.IsH2Upgrade(r) {
      if util.IsPutOrPost(r) {
        httpHandler.ServeHTTP(w, r)
      } else {
        r.ProtoMajor = 2
        rs.IsH2C = true
        h2cHandler.ServeHTTP(w, r)
      }
    } else if r.ProtoMajor == 2 {
      rs.IsH2C = true
      h2cHandler.ServeHTTP(w, r)
    } else {
      httpHandler.ServeHTTP(w, r)
    }
  })
}

func ContextMiddleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    if global.Stopping && global.IsReadinessProbe(r) {
      util.CopyHeaders(HeaderStoppingReadinessRequest, r, w, r.Header, true, true, false)
      w.WriteHeader(http.StatusNotFound)
    } else if next != nil {
      var rs *util.RequestStore
      startTime := time.Now()
      ctx := r.Context()
      if v := ctx.Value(util.RequestStoreKey); v != nil {
        rs = v.(*util.RequestStore)
      } else {
        ctx, rs = util.WithRequestStore(r)
        rs.IsH2C = r.ProtoMajor == 2
      }
      r = r.WithContext(withPort(ctx, util.GetListenerPortNum(r)))
      var irw *intercept.InterceptResponseWriter
      w, irw = withIntercept(r, w)
      next.ServeHTTP(w, r)
      statusCode := http.StatusOK
      bodyLength := 0
      if !util.IsKnownNonTraffic(r) && irw != nil {
        statusCode = irw.StatusCode
        bodyLength = irw.BodyLength
      }
      endTime := time.Now()
      statusCodeText := strconv.Itoa(statusCode)
      if !rs.IsTunnelRequest {
        w.Header().Add(HeaderGotoResponseStatus, statusCodeText)
        w.Header().Add(HeaderGotoInAt, startTime.UTC().String())
        w.Header().Add(HeaderGotoOutAt, endTime.UTC().String())
        w.Header().Add(HeaderGotoTook, endTime.Sub(startTime).String())
      } else {
        w.Header().Add(fmt.Sprintf("%s|%d", HeaderGotoInAt, rs.TunnelCount), startTime.UTC().String())
        w.Header().Add(fmt.Sprintf("%s|%d", HeaderGotoOutAt, rs.TunnelCount), endTime.UTC().String())
        w.Header().Add(fmt.Sprintf("%s|%d", HeaderGotoTook, rs.TunnelCount), endTime.Sub(startTime).String())
        w.Header()[HeaderGotoTunnel] = r.Header[HeaderGotoRequestedTunnel]
      }
      if !rs.IsAdminRequest {
        metrics.UpdateURIRequestCount(r.RequestURI, statusCodeText)
        metrics.UpdatePortRequestCount(util.GetListenerPort(r), r.RequestURI)
      }
      if irw != nil {
        irw.Proceed()
      }
      var data []byte
      if irw != nil {
        data = irw.Data
      }
      go PrintLogMessages(statusCode, bodyLength, data, w.Header(), r.Context().Value(util.RequestStoreKey).(*util.RequestStore))
    }
  })
}

func withIntercept(r *http.Request, w http.ResponseWriter) (http.ResponseWriter, *intercept.InterceptResponseWriter) {
  var irw *intercept.InterceptResponseWriter
  if !util.IsKnownNonTraffic(r) {
    irw = intercept.NewInterceptResponseWriter(r, w, true)
    r.Context().Value(util.RequestStoreKey).(*util.RequestStore).InterceptResponseWriter = irw
    w = irw
  }
  return w, irw
}

func withConnContext(ctx context.Context, conn net.Conn) context.Context {
  return context.WithValue(ctx, util.ConnectionKey, conn)
}

func withPort(ctx context.Context, port int) context.Context {
  return context.WithValue(ctx, util.CurrentPortKey, port)
}

func RunStartupScript() {
  if len(global.StartupScript) > 0 {
    command := "sh"
    args := []string{"-c", strings.Join(global.StartupScript, "; ")}
    realCmd := command + " " + strings.Join(args, " ")
    cmd := exec.Command(command, args...)
    cmd.Stdout = os.Stdout
    cmd.Stderr = os.Stderr
    if err := cmd.Run(); err != nil {
      log.Printf("Failed to run startup command [%s]. Error: [%s]\n", realCmd, err.Error())
    } else {
      log.Printf("Startup command [%s] ran successfully.\n", realCmd)
    }
  }
}

func StartHttpServer(server *http.Server) {
  if global.StartupDelay > 0 {
    log.Printf("Sleeping %s before starting", global.StartupDelay)
    time.Sleep(global.StartupDelay)
  }
  events.StartSender()
  go func() {
    log.Printf("Server %s ready", server.Addr)
    if err := server.ListenAndServe(); err != nil {
      log.Println(err)
    }
  }()
}

func ServeHTTPListener(l *listeners.Listener) {
  go func() {
    msg := fmt.Sprintf("Starting HTTP Listener [%s]", l.ListenerID)
    if l.TLS {
      msg += fmt.Sprintf(" With TLS [CN: %s]", l.CommonName)
    }
    log.Println(msg)
    if err := httpServer.Serve(l.Listener); err != nil {
      log.Printf("Listener [%d]: %s", l.Port, err.Error())
    }
  }()
}

func WaitForHttpServer(server *http.Server) {
  c := make(chan os.Signal, 1)
  signal.Notify(c, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
  <-c
  global.Stopping = true
  log.Println("Received stop signal.")
  if global.ShutdownDelay > 0 {
    log.Printf("Sleeping %s before stopping", global.ShutdownDelay)
    signal.Notify(c, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
    select {
    case <-c:
      log.Printf("Received 2nd Interrupt. Really stopping now.")
      break
    case <-time.After(global.ShutdownDelay):
      log.Printf("Slept long enough. Stopping now.")
      break
    }
  }
  StopHttpServer(server)
}

func StopHttpServer(server *http.Server) {
  StopGRPCServer()
  log.Printf("HTTP Server %s started shutting down", server.Addr)
  ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
  defer cancel()
  events.StopSender()
  time.Sleep(time.Second)
  log.Printf("Deregistering peer [%s : %s] from registry", global.PeerName, global.PeerAddress)
  peer.DeregisterPeer(global.PeerName, global.PeerAddress)
  events.SendEventJSONDirect("Server Stopped", global.HostLabel, listeners.GetListeners())
  server.Shutdown(ctx)
  log.Printf("HTTP Server %s finished shutting down", server.Addr)
}

func PrintLogMessages(statusCode, bodyLength int, payload []byte, headers http.Header, rs *util.RequestStore) {
  if (!rs.IsLockerRequest || global.EnableRegistryLockerLogs) &&
    (!rs.IsPeerEventsRequest || global.EnableRegistryEventsLogs) &&
    (!rs.IsAdminRequest || global.EnableAdminLogs) &&
    (!rs.IsReminderRequest || global.EnableRegistryReminderLogs) &&
    (!rs.IsProbeRequest || global.EnableProbeLogs) &&
    (!rs.IsHealthRequest || global.EnablePeerHealthLogs) &&
    (!rs.IsMetricsRequest || global.EnableMetricsLogs) &&
    (!rs.IsFilteredRequest && global.EnableServerLogs) {
    if global.LogResponseHeaders {
      rs.LogMessages = append(rs.LogMessages, util.GetResponseHeadersLog(headers))
    }
    if statusCode == 0 {
      statusCode = 200
    }
    rs.LogMessages = append(rs.LogMessages, fmt.Sprintf("Response Status Code: [%d]", statusCode))
    rs.LogMessages = append(rs.LogMessages, fmt.Sprintf("Response Body Length: [%d]", bodyLength))
    bodyLog := ""
    logLabel := ""
    if payload != nil && !rs.IsAdminRequest {
      if global.LogResponseMiniBody {
        logLabel = "Mini Body"
        if len(payload) > 50 {
          bodyLog = fmt.Sprintf("%s...", payload[:50])
          bodyLog += fmt.Sprintf("%s", payload[len(payload)-50:])
        } else {
          bodyLog = fmt.Sprintf("%s", payload)
        }
      } else if global.LogResponseBody {
        logLabel = "Body"
        bodyLog = fmt.Sprintf("%s", payload)
      }
      if bodyLog != "" {
        rs.LogMessages = append(rs.LogMessages, fmt.Sprintf("Response %s: [%s]", logLabel, bodyLog))
      }
    }
    log.Println(strings.Join(rs.LogMessages, " --> "))
    if flusher, ok := log.Writer().(http.Flusher); ok {
      flusher.Flush()
    }
  }
  rs.LogMessages = rs.LogMessages[:0]
}
