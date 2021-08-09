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
  "goto/pkg/log"
  "goto/pkg/metrics"
  "goto/pkg/proxy"
  "goto/pkg/registry"
  "goto/pkg/server/catchall"
  "goto/pkg/server/conn"
  "goto/pkg/server/echo"
  "goto/pkg/server/listeners"
  "goto/pkg/server/listeners/label"
  "goto/pkg/server/probes"
  "goto/pkg/server/request"
  "goto/pkg/server/response"
  "goto/pkg/server/tcp"
  "goto/pkg/tunnel"
  "goto/pkg/util"
)

func Run() {
  global.PeerAddress = util.GetHostIP() + ":" + strconv.Itoa(global.ServerPort)
  global.GetPeers = registry.GetPeers
  global.HasTunnel = tunnel.HasTunnel
  global.StoreEventInCurrentLocker = registry.StoreEventInCurrentLocker
  listeners.Configure(ServeHTTPListener, ServeGRPCListener, StartTCPServer)
  metrics.Startup()
  invocation.Startup()
  RunHttpServer(tunnel.TunnelCountHandler, label.Handler, conn.Handler, tunnel.Handler, events.Handler, metrics.Handler,
    listeners.Handler, probes.Handler, proxy.Handler, registry.Handler, client.Handler,
    request.Handler, response.Handler, tcp.Handler, job.Handler, log.Handler,echo.Handler, catchall.Handler)
  invocation.Shutdown()
  metrics.Shutdown()
  results.StopRegistrySender()
  os.Exit(0)
}
