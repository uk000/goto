package server

import (
  "context"
  "fmt"
  "goto/pkg/events"
  "goto/pkg/global"
  "goto/pkg/registry/peer"
  "goto/pkg/server/conn"
  "goto/pkg/server/intercept"
  "goto/pkg/server/listeners"
  "goto/pkg/util"
  "io/ioutil"
  "log"
  "net/http"
  "os"
  "os/signal"
  "strings"
  "syscall"
  "time"

  "github.com/gorilla/mux"
  "golang.org/x/net/http2"
  "golang.org/x/net/http2/h2c"
)

var (
  httpServer *http.Server
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
  h2s := &http2.Server{}
  httpServer = &http.Server{
    Addr:         fmt.Sprintf("0.0.0.0:%d", global.ServerPort),
    WriteTimeout: 1 * time.Minute,
    ReadTimeout:  1 * time.Minute,
    IdleTimeout:  1 * time.Minute,
    ConnContext:  conn.SaveConnInContext,
    Handler:      h2c.NewHandler(GRPCHandler(r), h2s),
    ErrorLog:     log.New(ioutil.Discard, "_logger", 0),
  }
  StartHttpServer(httpServer)
  listeners.StartInitialListeners()
  peer.RegisterPeer(global.PeerName, global.PeerAddress)
  events.SendEventJSONDirect("Server Started", listeners.GetListeners())
  WaitForHttpServer(httpServer)
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
  events.SendEventJSONDirect("Server Stopped", listeners.GetListeners())
  server.Shutdown(ctx)
  log.Printf("HTTP Server %s finished shutting down", server.Addr)
}

func ContextMiddleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    if global.Stopping && global.IsReadinessProbe(r) {
      util.CopyHeaders("Stopping-Readiness-Request", w, r.Header, r.Host, r.RequestURI)
      w.WriteHeader(http.StatusNotFound)
    } else if next != nil {
      crw := intercept.NewInterceptResponseWriter(w, true)
      startTime := time.Now().UnixNano()
      w.Header().Add("Goto-In-Nanos", fmt.Sprint(startTime))
      r = r.WithContext(WithRequestStore(WithPort(r.Context(), util.GetListenerPortNum(r)), r))
      next.ServeHTTP(crw, r)
      endTime := time.Now().UnixNano()
      w.Header().Add("Goto-Out-Nanos", fmt.Sprint(endTime))
      w.Header().Add("Goto-Took-Nanos", fmt.Sprint(endTime-startTime))
      go PrintLogMessages(crw.StatusCode, crw.BodyLength, w.Header(), r.Context().Value(util.RequestStoreKey).(*util.RequestStore))
      crw.Proceed()
    }
  })
}

func WithRequestStore(ctx context.Context, r *http.Request) context.Context {
  isAdminRequest := util.CheckAdminRequest(r)
  return context.WithValue(ctx, util.RequestStoreKey, &util.RequestStore{
    IsAdminRequest:      isAdminRequest,
    IsLockerRequest:     strings.HasPrefix(r.RequestURI, "/registry") && strings.Contains(r.RequestURI, "/locker"),
    IsPeerEventsRequest: strings.HasPrefix(r.RequestURI, "/registry") && strings.Contains(r.RequestURI, "/events"),
    IsMetricsRequest:    strings.Contains(r.RequestURI, "/metrics"),
    IsReminderRequest:   strings.Contains(r.RequestURI, "/remember"),
    IsProbeRequest:      global.IsReadinessProbe(r) || global.IsLivenessProbe(r),
    IsHealthRequest:     !isAdminRequest && strings.Contains(r.RequestURI, "/health"),
    IsStatusRequest:     !isAdminRequest && strings.Contains(r.RequestURI, "/status"),
    IsDelayRequest:      !isAdminRequest && strings.Contains(r.RequestURI, "/delay"),
    IsPayloadRequest:    !isAdminRequest && (strings.Contains(r.RequestURI, "/stream") || strings.Contains(r.RequestURI, "/payload")),
  })
}

func WithPort(ctx context.Context, port int) context.Context {
  return context.WithValue(ctx, util.CurrentPortKey, port)
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
    rs.LogMessages = append(rs.LogMessages, util.GetResponseHeadersLog(headers))
    rs.LogMessages = append(rs.LogMessages, fmt.Sprintf("Response Status Code: [%d]", statusCode))
    rs.LogMessages = append(rs.LogMessages, fmt.Sprintf("Response Body Length: [%d]", bodyLength))
    log.Println(strings.Join(rs.LogMessages, " --> "))
    if flusher, ok := log.Writer().(http.Flusher); ok {
      flusher.Flush()
    }
  }
  rs.LogMessages = rs.LogMessages[:0]
}
