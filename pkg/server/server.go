package server

import (
  "os"
  "strconv"

  "goto/pkg/client"
  "goto/pkg/client/results"
  "goto/pkg/events"
  "goto/pkg/global"
  "goto/pkg/invocation"
  "goto/pkg/job"
  "goto/pkg/metrics"
  "goto/pkg/registry"
  "goto/pkg/server/catchall"
  "goto/pkg/server/conn"
  "goto/pkg/server/echo"
  "goto/pkg/server/listeners"
  "goto/pkg/server/listeners/label"
  "goto/pkg/server/probes"
  "goto/pkg/server/proxy"
  "goto/pkg/server/request"
  "goto/pkg/server/response"
  "goto/pkg/server/runner"
  "goto/pkg/server/tcp"
  "goto/pkg/util"
)

func Run() {
  global.PeerAddress = util.GetHostIP() + ":" + strconv.Itoa(global.ServerPort)
  global.GetPeers = registry.GetPeers
  listeners.Configure(runner.ServeHTTPListener, runner.ServeGRPCListener, runner.StartTCPServer)
  invocation.Startup()
  runner.RunHttpServer(events.Handler, label.Handler, conn.Handler, metrics.Handler,
    listeners.Handler, probes.Handler, registry.Handler, job.Handler, client.Handler,
    tcp.Handler, proxy.Handler, request.Handler, response.Handler, echo.Handler, catchall.Handler)
  invocation.Shutdown()
  results.StopRegistrySender()
  os.Exit(0)
}
