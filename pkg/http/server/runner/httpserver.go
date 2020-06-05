package runner

import (
	"context"
	"fmt"
	"goto/pkg/http/client/target"
	"goto/pkg/http/registry"
	"goto/pkg/http/server/conn"
	"goto/pkg/util"
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
)

var (
  server *http.Server
  serverPort int
)

func RunHttpServer(port int, root string, handlers ...util.ServerHandler) {
  serverPort = port
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
  server = &http.Server{
    Addr:         fmt.Sprintf("0.0.0.0:%d", port),
    WriteTimeout: 60 * time.Second,
    ReadTimeout:  60 * time.Second,
    IdleTimeout:  60 * time.Second,
    ConnContext:  conn.SaveConnInContext,
    Handler:      r,
  }
  StartHttpServer(server)
  registerPeer()
  WaitForHttpServer(server)
}

func StartHttpServer(server *http.Server) {
  go func() {
    if err := server.ListenAndServe(); err != nil {
      log.Println("http server start failed")
      log.Println(err)
    }
  }()
}

func ServeListener(l net.Listener) {
  go func() {
    log.Printf("starting listener %s\n", l.Addr())
    if err := server.Serve(l); err != nil {
      log.Println("listener start failed")
      log.Println(err)
    }
  }()
}

func registerPeer() {
  if registry.RegistryURL != "" {
    registered := false
    retries := 0
    for !registered && retries < 3 {
      peer := registry.Peer{registry.PeerName, util.GetHostIP()+":"+strconv.Itoa(serverPort)}
      if resp, err := http.Post(registry.RegistryURL+"/registry/peers/add", "application/json", 
                            strings.NewReader(util.ToJSON(peer))); err == nil {
        defer resp.Body.Close()
        log.Printf("Register as peer [%s] with registry [%s]\n", registry.PeerName, registry.RegistryURL)
        targets := registry.PeerTargets{}
        if err := util.ReadJsonPayloadFromBody(resp.Body, &targets); err == nil {
          log.Printf("Got %d targets from registry:\n", len(targets))
          pc := target.GetClientForPort(strconv.Itoa(serverPort))
          autoInvoke := false
          for _, t := range targets {
            log.Printf("%+v\n", t)
            if t.AutoInvoke {
              log.Printf("Target %s marked for auto invoke\n", t.Name)
              pc.AddTarget(&target.Target{t.InvocationSpec})
              autoInvoke = true
            }
          }
          if autoInvoke {
            go target.InvokeTargets(pc)
          }
          for _, t := range targets {
            if !t.AutoInvoke {
              log.Printf("Target %s added without auto invoke\n", t.Name)
              pc.AddTarget(&target.Target{t.InvocationSpec})
            }
          }
        } else {
          log.Printf("Failed to read peer targets with error: %s\n", err.Error())
        }
        registered = true
      } else {
        retries++
        log.Printf("Failed to register as peer to registry, retries: %d, error: %s\n", retries, err.Error())
        if retries < 3 {
          time.Sleep(10*time.Second)
        }
      }
    }
    if !registered {
      log.Printf("Failed to register as peer to registry after %d retries\n", retries)
    }
  }
}

func deregisterPeer() {
  if registry.RegistryURL != "" {
    url := registry.RegistryURL+"/registry/peers/"+registry.PeerName+"/remove/"+util.GetHostIP()
    if resp, err := http.Post(url, "plain/text", nil); err == nil {
      defer resp.Body.Close()
      log.Println(util.Read(resp.Body))
    } else {
      log.Println(err.Error())
    }
  }
}

func WaitForHttpServer(server *http.Server) {
  c := make(chan os.Signal, 1)
  signal.Notify(c, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
  <-c
  StopHttpServer(server)
}

func StopHttpServer(server *http.Server) {
  ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
  deregisterPeer()
  defer cancel()
  server.Shutdown(ctx)
  log.Printf("HTTP Server %s shutting down", server.Addr)
}
