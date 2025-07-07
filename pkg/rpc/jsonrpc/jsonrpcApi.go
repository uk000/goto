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

package jsonrpc

import (
	"fmt"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net/http"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("jsonrpc", setRoutes, nil)
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	jsonrpc := util.PathRouter(r, "/jsonrpc/services")
	util.AddRouteQWithPort(jsonrpc, "/add/{service}", addService, "fromGRPC", "POST")
	util.AddRouteWithPort(jsonrpc, "/add/{service}", addService, "POST")
	util.AddRouteWithPort(jsonrpc, "/remove/{service}", removeService, "POST")
	util.AddRouteWithPort(jsonrpc, "/{service}/remove", removeService, "POST")
	util.AddRouteWithPort(jsonrpc, "/clear", removeAllServices, "POST")
}

func addService(w http.ResponseWriter, r *http.Request) {
	msg := ""
	name := util.GetStringParamValue(r, "service")
	fromGRPC := util.GetBoolParamValue(r, "fromGRPC")
	reg := GetJSONRPCRegistry(r)
	var service *JSONRPCService
	var err error
	if name == "" {
		w.WriteHeader(http.StatusBadRequest)
		msg = "No name"
	} else if fromGRPC {
		service, err = reg.FromGRPCService(name)
	} else {
		service, err = reg.NewJSONRPCService(r.Body)
	}
	if err == nil {
		w.WriteHeader(http.StatusOK)
		msg = fmt.Sprintf("Registered JSONRPCService: %s with %d methods", service.Name, len(service.Methods))
	} else {
		w.WriteHeader(http.StatusInternalServerError)
		msg = fmt.Sprintf("Failed to register service [%s] with error: %s", name, err.Error())
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func removeService(w http.ResponseWriter, r *http.Request) {
	msg := ""
	name := util.GetStringParamValue(r, "service")
	if name == "" {
		w.WriteHeader(http.StatusBadRequest)
		msg = "No name"
	} else {
		GetJSONRPCRegistry(r).RemoveService(name)
		w.WriteHeader(http.StatusOK)
		msg = fmt.Sprintf("JSONRPCService [%s] removed", name)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func removeAllServices(w http.ResponseWriter, r *http.Request) {
	msg := "All services removed"
	GetJSONRPCRegistry(r).Init()
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}
