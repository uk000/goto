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

package grpcproxy

import (
	"fmt"
	"goto/pkg/rpc"
	"goto/pkg/rpc/grpc"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net/http"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("grpc", setRoutes, nil)
)

func setRoutes(r *mux.Router) {
	proxyRouter := middleware.RootPath("/proxy")
	grpcRouter := util.PathPrefix(proxyRouter, "/grpc")
	util.AddRoute(grpcRouter, "/status", getGRPCProxyDetails, "GET")
	util.AddRoute(grpcRouter, "/clear", clearGRPCProxies, "POST")
	util.AddRoute(grpcRouter, "/{service}/{upstream}/tee/{teeport}", proxyGRPCService, "POST")
	util.AddRoute(grpcRouter, "/{service}/{upstream}/{targetService}/tee/{teeport}", proxyGRPCService, "POST")
	util.AddRoute(grpcRouter, "/{service}/{upstream}/{targetService}", proxyGRPCService, "POST")
	util.AddRoute(grpcRouter, "/{service}/{upstream}", proxyGRPCService, "POST")
	util.AddRoute(grpcRouter, "/{service}/{upstream}/{targetService}/delay/{delay}", proxyGRPCService, "POST")
	util.AddRoute(grpcRouter, "/{service}/{upstream}/delay/{delay}", proxyGRPCService, "POST")
}

func proxyGRPCService(w http.ResponseWriter, r *http.Request) {
	rs, _, _, msg, ok := rpc.CheckService(w, r, grpc.ServiceRegistry)
	upstream := util.GetStringParamValue(r, "upstream")
	if ok {
		targetService := util.GetStringParamValue(r, "targetService")
		if targetService == "" {
			targetService = rs.GetName()
		}
		teeport := util.GetIntParamValue(r, "teeport")
		delayMin, delayMax, delayCount, ok := util.GetDurationParam(r, "delay")
		if !ok {
			delayMin = 0
			delayMax = 0
			delayCount = 0
		}
		port := util.GetRequestOrListenerPortNum(r)
		proxy := GetPortProxy(port)
		if err := proxy.SetupGRPCProxy(rs.GetName(), targetService, nil, upstream, "", teeport, delayMin, delayMax, delayCount); err != nil {
			msg = fmt.Sprintf("Failed to setup gRPC proxy for Service [%s]on port [%d] to upstream [%s] target service [%s] with error: %s", rs.GetName(), port, upstream, targetService, err.Error())
		} else {
			msg = fmt.Sprintf("Service [%s] will be proxied on port [%d] to upstream [%s] target service [%s]", rs.GetName(), port, upstream, targetService)
		}
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func getGRPCProxyDetails(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	proxy := GetPortProxy(port)
	util.WriteJsonPayload(w, proxy)
	util.AddLogMessage("GRPC Proxy Details Returned", r)
}

func clearGRPCProxies(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	proxy := GetPortProxy(port)
	proxy.Init()
	util.AddLogMessage("GRPC Proxy cleared", r)
}

func removeGRPCProxy(w http.ResponseWriter, r *http.Request) {
	service := util.GetStringParamValue(r, "service")
	port := util.GetRequestOrListenerPortNum(r)
	proxy := GetPortProxy(port)
	proxy.RemoveProxy(service)
	util.AddLogMessage(fmt.Sprintf("GRPC Proxy [%s] removed", service), r)
}
