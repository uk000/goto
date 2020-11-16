package runner

import (
	"context"
	"fmt"
	"goto/pkg/global"
	"goto/pkg/http/registry/peer"
	"goto/pkg/http/server/conn"
	"goto/pkg/util"
	"log"
	"net"
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
  server *http.Server
)

func RunHttpServer(root string, handlers ...util.ServerHandler) {
  r := mux.NewRouter()
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
  http.Handle(root, r)
  h2s := &http2.Server{}
  server = &http.Server{
    Addr:         fmt.Sprintf("0.0.0.0:%d", global.ServerPort),
    WriteTimeout: 60 * time.Minute,
    ReadTimeout:  60 * time.Minute,
    IdleTimeout:  60 * time.Minute,
    ConnContext:  conn.SaveConnInContext,
    Handler:      h2c.NewHandler(r, h2s),
  }
  StartHttpServer(server)
  peer.RegisterPeer(global.PeerName, global.PeerAddress)
  WaitForHttpServer(server)
}

func StartHttpServer(server *http.Server) {
  if global.StartupDelay > 0 {
    log.Printf("Sleeping %s before starting", global.StartupDelay)
    time.Sleep(global.StartupDelay)
  }
  go func() {
    log.Printf("Server %s ready", server.Addr)
    if err := server.ListenAndServe(); err != nil {
      log.Println(err)
    }
  }()
}

func ServeListener(l net.Listener) {
  go func() {
    log.Printf("Starting listener %s\n", l.Addr())
    if err := server.Serve(l); err != nil {
      log.Println(err)
    }
  }()
}

func WaitForHttpServer(server *http.Server) {
  c := make(chan os.Signal, 1)
  signal.Notify(c, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
  <-c
  global.Stopping = true
  log.Printf("Received stop signal. Deregistering peer [%s : %s] from registry", global.PeerName, global.PeerAddress)
  peer.DeregisterPeer(global.PeerName, global.PeerAddress)
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
  log.Printf("HTTP Server %s started shutting down", server.Addr)
  ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
  defer cancel()
  server.Shutdown(ctx)
  log.Printf("HTTP Server %s finished shutting down", server.Addr)
}
