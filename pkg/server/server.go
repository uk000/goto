package server

import (
  "os"
  "strconv"

  "goto/pkg/client"
  "goto/pkg/client/results"
  "goto/pkg/global"
  "goto/pkg/invocation"
  "goto/pkg/job"
  "goto/pkg/registry"
  "goto/pkg/server/catchall"
  "goto/pkg/server/conn"
  "goto/pkg/server/echo"
  "goto/pkg/server/listeners"
  "goto/pkg/server/listeners/label"
  "goto/pkg/server/probe"
  "goto/pkg/server/request"
  "goto/pkg/server/response"
  "goto/pkg/server/runner"
  "goto/pkg/server/tcp"
  "goto/pkg/util"
)

func Run() {
  global.PeerAddress = util.GetHostIP() + ":" + strconv.Itoa(global.ServerPort)
  global.GetPeers = registry.GetPeers
  listeners.SetHTTPServer(runner.ServeHTTPListener)
  listeners.SetTCPServer(runner.StartTCPServer)
  invocation.Startup()
  runner.RunHttpServer("/", label.Handler, conn.Handler, listeners.Handler, probe.Handler, job.Handler, request.Handler,
    response.Handler, registry.Handler, client.Handler, tcp.Handler, echo.Handler, catchall.Handler)
  invocation.Shutdown()
  results.StopRegistrySender()
  os.Exit(0)
}
