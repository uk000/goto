package runner

import (
  "context"
  "fmt"
  "goto/pkg/events"
  "goto/pkg/global"
  "goto/pkg/registry/peer"
  "goto/pkg/server/conn"
  "goto/pkg/server/listeners"
  "goto/pkg/util"
  "io/ioutil"
  "log"
  "net/http"
  "os"
  "os/signal"
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
  r.Use(util.ContextMiddleware)
  r.Use(util.LoggingMiddleware)
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
