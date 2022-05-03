/**
 * Copyright 2022 uk
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package server

import (
  "os"
  "strconv"

  "goto/pkg/client"
  "goto/pkg/client/results"
  "goto/pkg/events"
  "goto/pkg/global"
  "goto/pkg/grpc"
  "goto/pkg/invocation"
  "goto/pkg/job"
  "goto/pkg/k8s"
  "goto/pkg/log"
  "goto/pkg/metrics"
  "goto/pkg/pipe"
  "goto/pkg/proxy"
  "goto/pkg/registry"
  "goto/pkg/script"
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
  util.WillTunnel = tunnel.WillTunnel
  util.WillProxyHTTP = proxy.WillProxyHTTP
  global.StoreEventInCurrentLocker = registry.StoreEventInCurrentLocker
  listeners.Configure(ServeHTTPListener, ServeGRPCListener, StartTCPServer)
  metrics.Startup()
  invocation.Startup()
  RunHttpServer(tunnel.TunnelCountHandler, label.Handler, conn.Handler, tunnel.Handler, events.Handler, metrics.Handler,
    listeners.Handler, probes.Handler, registry.Handler, client.Handler, k8s.Handler, pipe.Handler,
    request.Handler, proxy.Handler, response.Handler, tcp.Handler, script.Handler, job.Handler,
    grpc.Handler, log.Handler, echo.Handler, catchall.Handler)
  invocation.Shutdown()
  job.Manager.StopJobWatch()
  metrics.Shutdown()
  results.StopRegistrySender()
  os.Exit(0)
}
