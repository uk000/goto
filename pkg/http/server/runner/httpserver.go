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
    WriteTimeout: 60 * time.Second,
    ReadTimeout:  60 * time.Second,
    IdleTimeout:  60 * time.Second,
    ConnContext:  conn.SaveConnInContext,
    Handler:      h2c.NewHandler(r, h2s),
  }
  StartHttpServer(server)
  peer.RegisterPeer(global.PeerName, global.PeerAddress)
  WaitForHttpServer(server)
}

func StartHttpServer(server *http.Server) {
  go func() {
    if err := server.ListenAndServe(); err != nil {
      log.Println(err)
    }
  }()
}

func ServeListener(l net.Listener) {
  go func() {
    log.Printf("starting listener %s\n", l.Addr())
    if err := server.Serve(l); err != nil {
      log.Println(err)
    }
  }()
}

func WaitForHttpServer(server *http.Server) {
  c := make(chan os.Signal, 1)
  signal.Notify(c, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
  <-c
  StopHttpServer(server)
}

func StopHttpServer(server *http.Server) {
  global.Stopping = true
  log.Printf("HTTP Server %s started shutting down", server.Addr)
  time.Sleep(30*time.Second)
  ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
  defer cancel()
  peer.DeregisterPeer(global.PeerName, global.PeerAddress)
  server.Shutdown(ctx)
  log.Printf("HTTP Server %s finished shutting down", server.Addr)
}
