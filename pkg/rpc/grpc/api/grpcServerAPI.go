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

package grpcapi

import (
	"fmt"
	"goto/pkg/rpc"
	"goto/pkg/rpc/grpc"
	grpcserver "goto/pkg/rpc/grpc/server"
	"goto/pkg/server/listeners"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net/http"
	"sync"

	"github.com/gorilla/mux"
)

var (
	Middleware     = middleware.NewMiddleware("grpc", setRoutes, nil)
	ActiveServices = map[int]map[string]*grpc.GRPCService{}
	lock           = sync.RWMutex{}
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	grpcRouter := util.PathRouter(r, "/grpc")
	util.AddRouteWithPort(grpcRouter, "/services/active", getActiveServices, "GET")
	util.AddRouteWithPort(grpcRouter, "/port/{port}/open", openGRPCPort, "POST")
	util.AddRouteWithPort(grpcRouter, "/port/{port}/services", getActiveServices, "GET")
	util.AddRouteWithPort(grpcRouter, "/port/{port}/serve/{service}", serveService, "POST")
	util.AddRouteWithPort(grpcRouter, "/port/{port}/stop/{service}", stopService, "POST")
	util.AddRouteWithPort(grpcRouter, "/services/{service}/serve", serveService, "POST")
	util.AddRouteWithPort(grpcRouter, "/services/{service}/stop", stopService, "POST")
}

func openGRPCPort(w http.ResponseWriter, r *http.Request) {
	port := util.GetIntParamValue(r, "port")
	msg := ""
	status := http.StatusOK
	if port <= 0 || port > 65535 {
		status = http.StatusBadRequest
		msg = fmt.Sprintf("Invalid port [%d]", port)
	} else if l, err := listeners.AddGRPCListener(port, true); err == nil {
		grpcserver.Start(l)
		msg = fmt.Sprintf("Opened GRPC listener on port [%d]", port)
	} else {
		status = http.StatusInternalServerError
		msg = fmt.Sprintf("Failed to open GRPC listener on port [%d] with error: %s", port, err.Error())
	}
	w.WriteHeader(status)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func serveService(w http.ResponseWriter, r *http.Request) {
	msg := ""
	var rs rpc.RPCService
	rs, _, _, msg = rpc.CheckService(w, r, grpc.ServiceRegistry)
	if rs != nil {
		service := rs.(*grpc.GRPCService)
		l := listeners.GetRequestedListener(r)
		grpcserver.Serve(service, l)
		lock.Lock()
		if ActiveServices[l.Port] == nil {
			ActiveServices[l.Port] = map[string]*grpc.GRPCService{}
		}
		ActiveServices[l.Port][service.Name] = service
		lock.Unlock()
		msg = fmt.Sprintf("Service [%s] registered for serving on port [%d]", service.Name, l.Port)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func stopService(w http.ResponseWriter, r *http.Request) {
	msg := ""
	var rs rpc.RPCService
	rs, _, _, msg = rpc.CheckService(w, r, grpc.ServiceRegistry)
	if rs != nil {
		service := rs.(*grpc.GRPCService)
		l := listeners.GetRequestedListener(r)
		grpcserver.StopService(service, l)
		lock.Lock()
		delete(ActiveServices[l.Port], service.Name)
		if len(ActiveServices[l.Port]) == 0 {
			delete(ActiveServices, l.Port)
		}
		lock.Unlock()
		msg = fmt.Sprintf("Service [%s] registered for serving on port [%d]", service.Name, l.Port)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func getActiveServices(w http.ResponseWriter, r *http.Request) {
	port := util.GetIntParamValue(r, "port")
	if port > 0 {
		util.WriteJsonPayload(w, ActiveServices[port])
	} else {
		util.WriteJsonPayload(w, ActiveServices)
	}
}
