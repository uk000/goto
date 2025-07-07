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

package grpcserver

import (
	"fmt"
	"goto/pkg/rpc"
	"goto/pkg/rpc/grpc"
	"goto/pkg/server/listeners"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net/http"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("grpc", setRoutes, nil)
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	grpcRouter := util.PathRouter(r, "/grpc/services")
	// util.AddRouteWithPort(grpcRouter, "", getServices, "GET")
	util.AddRouteWithPort(grpcRouter, "/{service}/serve", serveService, "POST")

}

func serveService(w http.ResponseWriter, r *http.Request) {
	msg := ""
	if TheGRPCServer == nil {
		w.WriteHeader(http.StatusInternalServerError)
		msg = "GRPC Server not started"
	} else {
		var rs rpc.RPCService
		rs, _, _, msg = rpc.CheckService(w, r, grpc.ServiceRegistry)
		if rs != nil {
			service := rs.(*grpc.GRPCService)
			serve(service, listeners.GetCurrentListener(r))
			msg = fmt.Sprintf("Service [%s] registered for serving", service.Name)
		}
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}
