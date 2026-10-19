/**
 * Copyright 2026 uk
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

package rpc

import (
	"fmt"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net/http"

	"github.com/gorilla/mux"
)

var (
	Middleware         = middleware.NewMiddleware("rpc", setRoutes, nil)
	GetServiceRegistry = map[string]func(int) RPCServiceRegistry{}
)

func setRoutes(r *mux.Router) {
	rpcRouter := middleware.RootPath("/{rpc:grpc|jsonrpc}")
	rpcServiceRouter := util.PathRouter(rpcRouter, "/server")
	util.AddRoute(rpcServiceRouter, "", getServices, "GET")
	util.AddRoute(rpcServiceRouter, "/{service}", getService, "GET")
	util.AddRoute(rpcServiceRouter, "/{service}/track", trackService, "POST")
	util.AddRoute(rpcServiceRouter, "/{service}/track/headers/{headers}", trackService, "POST")
	util.AddRoute(rpcServiceRouter, "/{service}/track/{header}={value}", trackService, "POST")
	util.AddRoute(rpcServiceRouter, "/{service}/tracking", getServiceTracking, "GET")
	util.AddRoute(rpcServiceRouter, "/{service}/{method}/payload/clear", clearServiceResponsePayload, "POST")
	util.AddRoute(rpcServiceRouter, "/{service}/{method}/payload", setServiceResponsePayload, "POST")
	util.AddRoute(rpcServiceRouter, "/{service}/{method}/payload/header/{header}={value}", setServiceResponsePayload, "POST")
	util.AddRoute(rpcServiceRouter, "/{service}/{method}/payload/header/{header}", setServiceResponsePayload, "POST")
	util.AddRoute(rpcServiceRouter, "/{service}/{method}/payload/body~{regexes}", setServiceResponsePayload, "POST")
	util.AddRoute(rpcServiceRouter, "/{service}/{method}/payload/body/paths/{paths}", setServiceResponsePayload, "POST")
	util.AddRoute(rpcServiceRouter, "/{service}/{method}/payload/transform", setServicePayloadTransform, "POST")
	util.AddRoute(rpcServiceRouter, "/{service}/{method}/payload/stream/count={count}/delay={delay}", setServiceResponsePayload, "POST")
	util.AddRoute(rpcServiceRouter, "/{service}/{method}/payload/stream/count={count}/delay={delay}/header/{header}={value}", setServiceResponsePayload, "POST")
	util.AddRoute(rpcServiceRouter, "/{service}/{method}/payload/stream/count={count}/delay={delay}/header/{header}", setServiceResponsePayload, "POST")
}

// serviceRegistry returns the registry of services of the given type (grpc, jsonrpc) for the port, or nil if there
// isn't one: the registries are provided by the packages of the types, which register themselves.
func serviceRegistry(rpcType string, port int) RPCServiceRegistry {
	if getRegistry := GetServiceRegistry[rpcType]; getRegistry != nil {
		if reg := getRegistry(port); !isNil(reg) {
			return reg
		}
	}
	return nil
}

func getServices(w http.ResponseWriter, r *http.Request) {
	rpcType := util.GetStringParamValue(r, "rpc")
	util.WriteYaml(w, serviceRegistry(rpcType, util.GetRequestOrListenerPortNum(r)))
	util.AddLogMessage(fmt.Sprintf("All %s services listed", rpcType), r)
}

func getService(w http.ResponseWriter, r *http.Request) {
	rpcType := util.GetStringParamValue(r, "rpc")
	rs, _, _, msg, ok := CheckService(w, r, serviceRegistry(rpcType, util.GetRequestOrListenerPortNum(r)))
	if ok {
		util.WriteYaml(w, rs)
		msg = fmt.Sprintf("Service [%s] details served", rs.GetName())
	} else {
		fmt.Fprintln(w, msg)
	}
	util.AddLogMessage(msg, r)
}

func trackService(w http.ResponseWriter, r *http.Request) {
	rpcType := util.GetStringParamValue(r, "rpc")
	port := util.GetRequestOrListenerPortNum(r)
	service, _, _, msg, ok := CheckService(w, r, serviceRegistry(rpcType, port))
	if ok {
		headers, _ := util.GetListParam(r, "headers")
		header := util.GetStringParamValue(r, "header")
		value := util.GetStringParamValue(r, "value")
		GetRPCTracker(port).TrackService(port, service, headers, header, value)
		w.WriteHeader(http.StatusOK)
		msg = fmt.Sprintf("Tracking %s Service [%s] with headers [%+v]", rpcType, service.GetName(), []any{headers, header, value})
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func getServiceTracking(w http.ResponseWriter, r *http.Request) {
	rpcType := util.GetStringParamValue(r, "rpc")
	port := util.GetRequestOrListenerPortNum(r)
	service, _, _, msg, ok := CheckService(w, r, serviceRegistry(rpcType, port))
	if ok {
		if tracker := GetRPCTracker(port).GetServiceTrackerJSON(service); tracker != nil {
			tracker.PortTracking = portTrackingOf(service.GetName())
			util.WriteJsonPayload(w, tracker)
			util.AddLogMessage(fmt.Sprintf("Tracking of %s Service [%s] served", rpcType, service.GetName()), r)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		msg = fmt.Sprintf("%s Service [%s] is not tracked", rpcType, service.GetName())
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func clearServiceResponsePayload(w http.ResponseWriter, r *http.Request) {
	rpcType := util.GetStringParamValue(r, "rpc")
	ClearServiceResponsePayload(w, r, serviceRegistry(rpcType, util.GetRequestOrListenerPortNum(r)))
}

func setServiceResponsePayload(w http.ResponseWriter, r *http.Request) {
	rpcType := util.GetStringParamValue(r, "rpc")
	SetServiceResponsePayload(w, r, serviceRegistry(rpcType, util.GetRequestOrListenerPortNum(r)))
}

func setServicePayloadTransform(w http.ResponseWriter, r *http.Request) {
	rpcType := util.GetStringParamValue(r, "rpc")
	SetServicePayloadTransform(w, r, serviceRegistry(rpcType, util.GetRequestOrListenerPortNum(r)))
}
