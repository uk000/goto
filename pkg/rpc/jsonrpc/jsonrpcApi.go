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
	"goto/pkg/rpc"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net/http"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("jsonrpc", SetRoutes, nil)
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	rpcRouter := util.PathRouter(r, "/jsonrpc")
	servicesRouter := util.PathRouter(rpcRouter, "/services")
	util.AddRouteQWithPort(servicesRouter, "/add/{service}", addService, "fromGRPC", "POST")
	util.AddRouteWithPort(servicesRouter, "/add/{service}", addService, "POST")
	util.AddRouteWithPort(servicesRouter, "/remove/{service}", removeService, "POST")
	util.AddRouteWithPort(servicesRouter, "/clear", removeAllServices, "POST")
	util.AddRouteWithPort(servicesRouter, "", listServices, "GET")
	util.AddRouteWithPort(servicesRouter, "/{service}", getService, "GET")

	util.AddRouteWithPort(servicesRouter, "/{service}/{method}/payload/clear", clearServiceResponsePayload, "POST")
	util.AddRouteWithPort(servicesRouter, "/{service}/{method}/payload", setServiceResponsePayload, "POST")
	util.AddRouteWithPort(servicesRouter, "/{service}/{method}/payload/header/{header}={value}", setServiceResponsePayload, "POST")
	util.AddRouteWithPort(servicesRouter, "/{service}/{method}/payload/header/{header}", setServiceResponsePayload, "POST")
	util.AddRouteWithPort(servicesRouter, "/{service}/{method}/payload/body~{regexes}", setServiceResponsePayload, "POST")
	util.AddRouteWithPort(servicesRouter, "/{service}/{method}/payload/body/paths/{paths}", setServiceResponsePayload, "POST")
	util.AddRouteWithPort(servicesRouter, "/{service}/{method}/payload/transform", setServicePayloadTransform, "POST")
	util.AddRouteWithPort(servicesRouter, "/{service}/{method}/payload/stream/count={count}/delay={delay}", setServiceResponsePayload, "POST")
	util.AddRouteWithPort(servicesRouter, "/{service}/{method}/payload/stream/count={count}/delay={delay}/header/{header}={value}", setServiceResponsePayload, "POST")
	util.AddRouteWithPort(servicesRouter, "/{service}/{method}/payload/stream/count={count}/delay={delay}/header/{header}", setServiceResponsePayload, "POST")
}

func addService(w http.ResponseWriter, r *http.Request) {
	msg := ""
	name := util.GetStringParamValue(r, "service")
	fromGRPC := util.GetBoolParamValue(r, "fromGRPC")
	var service *JSONRPCService
	var err error
	if name == "" {
		w.WriteHeader(http.StatusBadRequest)
		msg = "No name"
	} else if fromGRPC {
		service, err = JSONRPCRegistry.FromGRPCService(name)
	} else {
		service, err = JSONRPCRegistry.NewJSONRPCService(r.Body)
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
		JSONRPCRegistry.RemoveService(name)
		w.WriteHeader(http.StatusOK)
		msg = fmt.Sprintf("JSONRPCService [%s] removed", name)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func removeAllServices(w http.ResponseWriter, r *http.Request) {
	msg := "All services removed"
	JSONRPCRegistry.Init()
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func listServices(w http.ResponseWriter, r *http.Request) {
	util.WriteJsonPayload(w, JSONRPCRegistry.Services)
	msg := fmt.Sprintf("Listed %d services", len(JSONRPCRegistry.Services))
	util.AddLogMessage(msg, r)
}

func getService(w http.ResponseWriter, r *http.Request) {
	msg := ""
	name := util.GetStringParamValue(r, "service")
	if name == "" {
		w.WriteHeader(http.StatusBadRequest)
		msg = "No name"
	} else {
		if service := JSONRPCRegistry.GetService(name); service != nil {
			util.WriteJsonPayload(w, service)
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusNotFound)
			msg = fmt.Sprintf("JSONRPCService [%s] not found", name)
		}
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func clearServiceResponsePayload(w http.ResponseWriter, r *http.Request) {
	rpc.ClearServiceResponsePayload(w, r, JSONRPCRegistry)
}

func setServiceResponsePayload(w http.ResponseWriter, r *http.Request) {
	rpc.SetServiceResponsePayload(w, r, JSONRPCRegistry)
}

func setServicePayloadTransform(w http.ResponseWriter, r *http.Request) {
	rpc.SetServicePayloadTransform(w, r, JSONRPCRegistry)
}
