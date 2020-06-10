package runner

import (
	"context"
	"fmt"
	"goto/pkg/global"
	"goto/pkg/http/client/target"
	"goto/pkg/http/registry"
	"goto/pkg/http/server/conn"
	"goto/pkg/job"
	"goto/pkg/util"
	"io"
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
  server = &http.Server{
    Addr:         fmt.Sprintf("0.0.0.0:%d", global.ServerPort),
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

func setupStartupTasks(payload io.ReadCloser) {
  data := map[string]interface{}{}
  if err := util.ReadJsonPayloadFromBody(payload, &data); err == nil {
    targets := registry.PeerTargets{}
    if data["targets"] != nil {
      targetsData := util.ToJSON(data["targets"])
      if err := util.ReadJson(targetsData, &targets); err != nil {
        log.Println(err.Error())
        return
      }
    }
    jobs := registry.PeerJobs{}
    if data["jobs"] != nil {
      for _, jobData := range data["jobs"].(map[string]interface {}) {
        if job, err := job.ParseJobFromPayload(util.ToJSON(jobData)); err != nil {
          log.Println(err.Error())
          return
        } else {
          jobs[job.ID] = &registry.PeerJob{*job}
        }
      }
    }
    log.Printf("Got %d targets and %d jobs from registry:\n", len(targets), len(jobs))
    port := strconv.Itoa(global.ServerPort)
    pc := target.GetClientForPort(port)
    pj := job.GetPortJobs(port)

    for _, job := range jobs {
      log.Printf("%+v\n", job)
      pj.AddJob(&job.Job)
    }

    for _, t := range targets {
      log.Printf("%+v\n", t)
      pc.AddTarget(&target.Target{t.InvocationSpec})
    }
  } else {
    log.Printf("Failed to read peer targets with error: %s\n", err.Error())
  }
}

func registerPeer() {
  if global.RegistryURL != "" {
    registered := false
    retries := 0
    for !registered && retries < 3 {
      peer := registry.Peer{
        Name: global.PeerName, 
        Address: util.GetHostIP()+":"+strconv.Itoa(global.ServerPort),
        Pod: util.GetPodName(),
        Namespace: util.GetNamespace(),
      }
      if resp, err := http.Post(global.RegistryURL+"/registry/peers/add", "application/json", 
                            strings.NewReader(util.ToJSON(peer))); err == nil {
        defer resp.Body.Close()
        log.Printf("Registered as peer [%s] with registry [%s]\n", global.PeerName, global.RegistryURL)
        registered = true
        setupStartupTasks(resp.Body)
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
  if global.RegistryURL != "" {
    url := global.RegistryURL+"/registry/peers/"+global.PeerName+"/remove/"+util.GetHostIP()+":"+strconv.Itoa(global.ServerPort)
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
  defer cancel()
  deregisterPeer()
  server.Shutdown(ctx)
  log.Printf("HTTP Server %s shutting down", server.Addr)
}
