/**
 * Copyright 2025 uk
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

	mcpclient "goto/pkg/ai/mcp/client"
	mcpserver "goto/pkg/ai/mcp/server"
	mcpserverapi "goto/pkg/ai/mcp/server/api"
	"goto/pkg/client"
	"goto/pkg/events"
	"goto/pkg/global"
	"goto/pkg/job"
	k8sApi "goto/pkg/k8s/api"
	k8sYaml "goto/pkg/k8s/yaml"
	"goto/pkg/log"
	"goto/pkg/metrics"
	"goto/pkg/pipe"
	"goto/pkg/proxy"
	"goto/pkg/registry"
	"goto/pkg/routing"
	"goto/pkg/rpc"
	grpcclient "goto/pkg/rpc/grpc/client"
	"goto/pkg/rpc/grpc/protos"
	grpcapi "goto/pkg/rpc/grpc/server"
	"goto/pkg/rpc/jsonrpc"
	"goto/pkg/scripts"
	"goto/pkg/server/catchall"
	"goto/pkg/server/conn"
	"goto/pkg/server/echo"
	"goto/pkg/server/hooks"
	"goto/pkg/server/listeners"
	"goto/pkg/server/listeners/label"
	"goto/pkg/server/middleware"
	"goto/pkg/server/probes"
	"goto/pkg/server/request"
	"goto/pkg/server/request/body"
	"goto/pkg/server/response"
	"goto/pkg/server/tcp"
	"goto/pkg/server/udp"
	"goto/pkg/server/ui"
	"goto/pkg/server/xds"
	"goto/pkg/tls"
	"goto/pkg/tunnel"
	"goto/pkg/util"
)

func init() {
	middleware.BaseMiddlewares = []*middleware.Middleware{
		label.Middleware, conn.Middleware, mcpserver.Middleware,
		tunnel.TunnelCountMiddleware, body.Middleware, hooks.Middleware, routing.Middleware,
	}
	middleware.Middlewares = []*middleware.Middleware{
		tunnel.Middleware, events.Middleware, metrics.Middleware,
		listeners.Middleware, probes.Middleware, registry.Middleware, client.Middleware,
		k8sYaml.Middleware, k8sApi.Middleware, pipe.Middleware, request.Middleware,
		proxy.Middleware, response.Middleware, tcp.Middleware, udp.Middleware,
		rpc.Middleware, grpcapi.Middleware, grpcclient.Middleware, protos.Middleware,
		jsonrpc.Middleware, xds.Middleware, mcpclient.Middleware, mcpserverapi.Middleware,
		scripts.Middleware, job.Middleware, tls.Middleware, log.Middleware, ui.Middleware,
		echo.Middleware, catchall.Middleware,
	}
}

func Run() {
	global.Self.Address = util.GetPodIP() + ":" + strconv.Itoa(global.Self.ServerPort)
	RunHttpServer()
	global.Shutdown()
	os.Exit(0)
}
