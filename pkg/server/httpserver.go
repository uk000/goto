package server

import (
  "context"
  "fmt"
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
  h2c := h2c.NewHandler(GRPCHandler(r), h2s)
  httpServer = &http.Server{
    Addr:         fmt.Sprintf("0.0.0.0:%d", global.ServerPort),
    WriteTimeout: 1 * time.Minute,
    ReadTimeout:  1 * time.Minute,
    IdleTimeout:  1 * time.Minute,
    ConnContext:  withConnContext,
    Handler:      HTTPHandler(r, h2c),
    ErrorLog:     log.New(ioutil.Discard, "discard", 0),
  }
  StartHttpServer(httpServer)
  go listeners.StartInitialListeners()
  peer.RegisterPeer(global.PeerName, global.PeerAddress)
  events.SendEventJSONDirect("Server Started", global.HostLabel, listeners.GetListeners())
  WaitForHttpServer(httpServer)
}

func HTTPHandler(httpHandler, h2cHandler http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    ctx, rs := withRequestStore(r)
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
      util.CopyHeaders("Stopping-Readiness-Request", w, r.Header, r.Host, r.RequestURI)
      w.WriteHeader(http.StatusNotFound)
    } else if next != nil {
      var rs *util.RequestStore
      startTime := time.Now()
      ctx := r.Context()
      if v := ctx.Value(util.RequestStoreKey); v != nil {
        rs = v.(*util.RequestStore)
      } else {
        ctx, rs = withRequestStore(r)
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
      if !rs.IsTunnelRequest {
        statusCode := strconv.Itoa(statusCode)
        w.Header().Add("Goto-Response-Status", statusCode)
        w.Header().Add("Goto-In-At", startTime.UTC().String())
        w.Header().Add("Goto-Out-At", endTime.UTC().String())
        w.Header().Add("Goto-Took", endTime.Sub(startTime).String())
        if !rs.IsAdminRequest {
          metrics.UpdateURIRequestCount(r.RequestURI, statusCode)
          metrics.UpdatePortRequestCount(util.GetListenerPort(r), r.RequestURI)
        }
      }
      if irw != nil {
        irw.Proceed()
      }
      go PrintLogMessages(statusCode, bodyLength, w.Header(), r.Context().Value(util.RequestStoreKey).(*util.RequestStore))
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

func withRequestStore(r *http.Request) (context.Context, *util.RequestStore) {
  isAdminRequest := util.CheckAdminRequest(r)
  rs := &util.RequestStore{
    IsAdminRequest:      isAdminRequest,
    IsVersionRequest:    strings.HasPrefix(r.RequestURI, "/version"),
    IsLockerRequest:     strings.HasPrefix(r.RequestURI, "/registry") && strings.Contains(r.RequestURI, "/locker"),
    IsPeerEventsRequest: strings.HasPrefix(r.RequestURI, "/registry") && strings.Contains(r.RequestURI, "/events"),
    IsMetricsRequest:    strings.HasPrefix(r.RequestURI, "/metrics") || strings.HasPrefix(r.RequestURI, "/stats"),
    IsReminderRequest:   strings.Contains(r.RequestURI, "/remember"),
    IsProbeRequest:      global.IsReadinessProbe(r) || global.IsLivenessProbe(r),
    IsHealthRequest:     !isAdminRequest && strings.HasPrefix(r.RequestURI, "/health"),
    IsStatusRequest:     !isAdminRequest && strings.HasPrefix(r.RequestURI, "/status"),
    IsDelayRequest:      !isAdminRequest && strings.Contains(r.RequestURI, "/delay"),
    IsPayloadRequest:    !isAdminRequest && (strings.Contains(r.RequestURI, "/stream") || strings.Contains(r.RequestURI, "/payload")),
    IsTunnelRequest:     strings.HasPrefix(r.RequestURI, "/tunnel"),
  }
  return context.WithValue(r.Context(), util.RequestStoreKey, rs), rs
}

func withPort(ctx context.Context, port int) context.Context {
  return context.WithValue(ctx, util.CurrentPortKey, port)
}

func StartHttpServer(server *http.Server) {
  if global.StartupDelay > 0 {
    log.Printf("Sleeping %s before starting", global.StartupDelay)
    time.Sleep(global.StartupDelay)
  }
  go func() {
    log.Printf("Server %s ready", server.Addr)
    events.StartSender()
    if err := server.ListenAndServe(); err != nil {
      log.Println(err)
    }
  }()
}

func ServeHTTPListener(l *listeners.Listener) {
  go func() {
    log.Printf("Starting HTTP Listener %s\n", l.ListenerID)
    if err := httpServer.Serve(l.Listener); err != nil {
      log.Println(err)
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

func PrintLogMessages(statusCode, bodyLength int, headers http.Header, rs *util.RequestStore) {
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
    log.Println(strings.Join(rs.LogMessages, " --> "))
    if flusher, ok := log.Writer().(http.Flusher); ok {
      flusher.Flush()
    }
  }
  rs.LogMessages = rs.LogMessages[:0]
}
