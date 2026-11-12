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
	"strings"

	"github.com/gorilla/mux"
)

var (
	Middleware         = middleware.NewMiddleware("rpc", setRoutes, nil)
	GetServiceRegistry = map[string]func(int) RPCServiceRegistry{}
)

const (
	grpcRoot    = "/grpc"
	jsonrpcRoot = "/jsonrpc"
)

// RootRouter gives the router of the root path that the APIs of the type of rpc (grpc, jsonrpc) are served from. The
// server hands a request to the router of the first segment of its path, which is the root path of the type, so the
// routes of the APIs have to be on that router to be reached: a router of a pattern that spans the types, which no
// root path is equal to, is never given a request.
func RootRouter(rpcType string) *mux.Router {
	if rpcType == "grpc" {
		return middleware.RootPath(grpcRoot)
	}
	// The root path of jsonrpc is also one that other requests can have, and those are not for the APIs: only the paths
	// of the APIs are given to the router.
	return middleware.RootPathIf(jsonrpcRoot, IsJSONRPCAdminRequest)
}

// IsJSONRPCAdminRequest tells if the request is for one of the APIs of jsonrpc, as opposed to a request that has no API
// to serve it, and is handled as any other request.
func IsJSONRPCAdminRequest(r *http.Request) bool {
	uri := r.RequestURI
	if i := strings.IndexAny(uri, "?#"); i >= 0 {
		uri = uri[:i]
	}
	return uri == jsonrpcRoot+"/server" || strings.HasPrefix(uri, jsonrpcRoot+"/server/") ||
		uri == jsonrpcRoot+"/services" || strings.HasPrefix(uri, jsonrpcRoot+"/services/")
}

// forRPC gives the handler the type of rpc that the routes of the router are for, as the param that it takes it from.
func forRPC(rpcType string, f func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := map[string]string{}
		for k, v := range mux.Vars(r) {
			vars[k] = v
		}
		vars["rpc"] = rpcType
		f(w, mux.SetURLVars(r, vars))
	}
}

// setRoutes registers the service admin routes for both the grpc and jsonrpc types on their root routers.
func setRoutes(r *mux.Router) {
	for _, rpcType := range []string{"grpc", "jsonrpc"} {
		setRPCRoutes(rpcType, RootRouter(rpcType))
	}
}

// setRPCRoutes adds the /server routes of one rpc type for listing services, tracking calls, and configuring method response payloads.
func setRPCRoutes(rpcType string, rpcRouter *mux.Router) {
	rpcServiceRouter := util.PathRouter(rpcRouter, "/server")
	add := func(path string, f func(http.ResponseWriter, *http.Request), methods ...string) {
		util.AddRoute(rpcServiceRouter, path, forRPC(rpcType, f), methods...)
	}
	add("", getServices, "GET")
	add("/{service}", getService, "GET")
	add("/{service}/track", trackService, "POST")
	add("/{service}/track/headers/{headers}", trackService, "POST")
	add("/{service}/track/{header}={value}", trackService, "POST")
	add("/{service}/tracking", getServiceTracking, "GET")
	add("/{service}/{method}/payload/clear", clearServiceResponsePayload, "POST")
	add("/{service}/{method}/payload", setServiceResponsePayload, "POST")
	add("/{service}/{method}/payload/header/{header}={value}", setServiceResponsePayload, "POST")
	add("/{service}/{method}/payload/header/{header}", setServiceResponsePayload, "POST")
	add("/{service}/{method}/payload/body~{regexes}", setServiceResponsePayload, "POST")
	add("/{service}/{method}/payload/body/paths/{paths}", setServiceResponsePayload, "POST")
	add("/{service}/{method}/payload/transform", setServicePayloadTransform, "POST")
	add("/{service}/{method}/payload/stream/count={count}/delay={delay}", setServiceResponsePayload, "POST")
	add("/{service}/{method}/payload/stream/count={count}/delay={delay}/header/{header}={value}", setServiceResponsePayload, "POST")
	add("/{service}/{method}/payload/stream/count={count}/delay={delay}/header/{header}", setServiceResponsePayload, "POST")
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

// getServices lists all services registered for the rpc type of the request as YAML.
func getServices(w http.ResponseWriter, r *http.Request) {
	rpcType := util.GetStringParamValue(r, "rpc")
	util.WriteYaml(w, serviceRegistry(rpcType, util.GetRequestOrListenerPortNum(r)))
	util.AddLogMessage(fmt.Sprintf("All %s services listed", rpcType), r)
}

// getService serves the details of the requested service as YAML, or an error message if the service isn't found.
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

// trackService starts tracking calls to the requested service, optionally by the given request headers.
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

// getServiceTracking serves the call counts and port tracking of the requested service, or 404 if it isn't tracked.
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

// clearServiceResponsePayload clears the response payloads configured for a service method on the port.
func clearServiceResponsePayload(w http.ResponseWriter, r *http.Request) {
	rpcType := util.GetStringParamValue(r, "rpc")
	ClearServiceResponsePayload(w, r, serviceRegistry(rpcType, util.GetRequestOrListenerPortNum(r)))
}

// setServiceResponsePayload configures the response payload (unary or stream, optionally header or body matched) of a service method.
func setServiceResponsePayload(w http.ResponseWriter, r *http.Request) {
	rpcType := util.GetStringParamValue(r, "rpc")
	SetServiceResponsePayload(w, r, serviceRegistry(rpcType, util.GetRequestOrListenerPortNum(r)))
}

// setServicePayloadTransform configures payload transformations for the responses of a service method.
func setServicePayloadTransform(w http.ResponseWriter, r *http.Request) {
	rpcType := util.GetStringParamValue(r, "rpc")
	SetServicePayloadTransform(w, r, serviceRegistry(rpcType, util.GetRequestOrListenerPortNum(r)))
}
