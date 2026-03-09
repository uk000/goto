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

	a2aclient "goto/pkg/ai/a2a/client"
	a2aserver "goto/pkg/ai/a2a/server"
	mcpclient "goto/pkg/ai/mcp/client"
	mcpserverapi "goto/pkg/ai/mcp/server/api"
	"goto/pkg/client"
	"goto/pkg/events"
	"goto/pkg/global"
	"goto/pkg/job"
	k8sApi "goto/pkg/k8s/api"
	k8sYaml "goto/pkg/k8s/yaml"
	"goto/pkg/log"
	"goto/pkg/memory"
	"goto/pkg/metrics"
	"goto/pkg/pipe"
	grpcproxy "goto/pkg/proxy/grpc"
	httpproxy "goto/pkg/proxy/http"
	mcpproxy "goto/pkg/proxy/mcp"
	tcpproxy "goto/pkg/proxy/tcp"
	udpproxy "goto/pkg/proxy/udp"
	"goto/pkg/registry"
	"goto/pkg/router"
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
	"goto/pkg/server/info"
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
	"goto/pkg/tls"
	"goto/pkg/tunnel"
)

func init() {
	middleware.Core = []*middleware.Middleware{conn.Middleware, hooks.Middleware}

	middleware.InterceptedCore = append(middleware.InterceptedCore, request.CoreMiddlewares...)
	middleware.InterceptedCore = append(middleware.InterceptedCore, response.CoreMiddlewares...)

	middleware.Unintercepted = []*middleware.Middleware{
		tunnel.TunnelCountMiddleware, tunnel.Middleware, probes.Middleware,
		memory.Middleware, events.Middleware, metrics.Middleware, ui.Middleware,
		body.Middleware,
	}
	middleware.Intercepted = []*middleware.Middleware{
		request.Middleware, response.Middleware, catchall.Middleware,
	}
	middleware.RoutesOnly = []*middleware.Middleware{
		router.Middleware, httpproxy.Middleware, grpcproxy.Middleware, mcpproxy.Middleware,
		tcpproxy.Middleware, udpproxy.Middleware,
		a2aserver.Middleware, a2aclient.Middleware, mcpclient.Middleware, mcpserverapi.Middleware,
		tcp.Middleware, udp.Middleware, rpc.Middleware, jsonrpc.Middleware,
		client.Middleware, listeners.Middleware, registry.Middleware,
		grpcapi.Middleware, grpcclient.Middleware, protos.Middleware,
		scripts.Middleware, job.Middleware, tls.Middleware, log.Middleware,
		label.Middleware, info.Middleware, echo.Middleware,
		pipe.Middleware, k8sYaml.Middleware, k8sApi.Middleware,
	}
}

func Run() {
	RunHttpServer()
	global.Shutdown()
	os.Exit(0)
}
