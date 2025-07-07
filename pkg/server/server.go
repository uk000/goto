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
	"goto/pkg/rpc"
	"goto/pkg/rpc/grpc/protos"
	grpcserver "goto/pkg/rpc/grpc/server"
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
	"goto/pkg/server/response"
	"goto/pkg/server/tcp"
	"goto/pkg/server/ui"
	"goto/pkg/tls"
	"goto/pkg/tunnel"
	"goto/pkg/util"
)

func init() {
	middleware.Middlewares = []*middleware.Middleware{
		ui.Middleware, tunnel.TunnelCountMiddleware, label.Middleware, conn.Middleware, hooks.Middleware, tunnel.Middleware,
		events.Middleware, metrics.Middleware, listeners.Middleware, probes.Middleware, registry.Middleware, client.Middleware,
		k8sYaml.Middleware, k8sApi.Middleware, pipe.Middleware, request.Middleware, proxy.Middleware, response.Middleware,
		tcp.Middleware, scripts.Middleware, job.Middleware, rpc.Middleware, grpcserver.Middleware, protos.Middleware, jsonrpc.Middleware,
		tls.Middleware, log.Middleware, echo.Middleware, catchall.Middleware,
	}
}

func Run() {
	global.Self.Address = util.GetPodIP() + ":" + strconv.Itoa(global.Self.ServerPort)
	RunHttpServer()
	global.Shutdown()
	os.Exit(0)
}
