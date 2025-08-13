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
	"goto/pkg/proxy"
	"goto/pkg/rpc"
	"goto/pkg/rpc/grpc"
	gotogrpc "goto/pkg/rpc/grpc"
	grpcclient "goto/pkg/rpc/grpc/client"
	grpcserver "goto/pkg/rpc/grpc/server"
	"goto/pkg/server/listeners"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/mux"
)

var (
	Middleware     = middleware.NewMiddleware("grpc", setRoutes, nil)
	ActiveServices = map[int]map[string]*grpc.GRPCService{}
	GRPCFactory    = grpcserver.GRPCManager
	lock           = sync.RWMutex{}
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	grpcRouter := util.PathRouter(r, "/grpc")
	util.AddRouteWithPort(grpcRouter, "/services/active", getActiveServices, "GET")
	util.AddRouteWithPort(grpcRouter, "/open", openGRPCPort, "POST")
	util.AddRouteWithPort(grpcRouter, "/services", getActiveServices, "GET")
	util.AddRouteWithPort(grpcRouter, "/serve/{service}", serveService, "POST")
	util.AddRouteWithPort(grpcRouter, "/stop/{service}", stopService, "POST")
	util.AddRouteWithPort(grpcRouter, "/services/{service}/serve", serveService, "POST")
	util.AddRouteWithPort(grpcRouter, "/services/{service}/stop", stopService, "POST")

	util.AddRouteWithPort(grpcRouter, "/services/reflect/{upstream}", loadReflectedServices, "POST")

	util.AddRouteWithPort(grpcRouter, "/proxy/status", getGRPCProxyDetails, "GET")
	util.AddRouteWithPort(grpcRouter, "/proxy/clear", clearGRPCProxies, "POST")
	util.AddRouteWithPort(grpcRouter, "/proxy/{service}/{upstream}/tee/{teeport}", proxyGRPCService, "POST")
	util.AddRouteWithPort(grpcRouter, "/proxy/{service}/{upstream}/{targetService}/tee/{teeport}", proxyGRPCService, "POST")
	util.AddRouteWithPort(grpcRouter, "/proxy/{service}/{upstream}/{targetService}", proxyGRPCService, "POST")
	util.AddRouteWithPort(grpcRouter, "/proxy/{service}/{upstream}", proxyGRPCService, "POST")
	util.AddRouteWithPort(grpcRouter, "/proxy/{service}/{upstream}/{targetService}/delay/{delay}", proxyGRPCService, "POST")
	util.AddRouteWithPort(grpcRouter, "/proxy/{service}/{upstream}/delay/{delay}", proxyGRPCService, "POST")
	util.AddRouteWithPort(grpcRouter, "/proxy/all/{upstream}", proxyGRPCService, "POST")
}

func openGRPCPort(w http.ResponseWriter, r *http.Request) {
	port := util.GetIntParamValue(r, "port")
	msg := ""
	status := http.StatusOK
	if port <= 0 || port > 65535 {
		status = http.StatusBadRequest
		msg = fmt.Sprintf("Invalid port [%d]", port)
	} else if l, err := listeners.AddGRPCListener(port, true); err == nil {
		GRPCFactory.ServeListener(l)
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
	rs, _, _, msg, ok := rpc.CheckService(w, r, grpc.ServiceRegistry)
	if ok {
		service := rs.(*grpc.GRPCService)
		port := util.GetRequestOrListenerPortNum(r)
		GRPCFactory.Serve(port, service)
		lock.Lock()
		if ActiveServices[port] == nil {
			ActiveServices[port] = map[string]*grpc.GRPCService{}
		}
		ActiveServices[port][service.Name] = service
		lock.Unlock()
		msg = fmt.Sprintf("Service [%s] registered for serving on port [%d]", service.Name, port)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func stopService(w http.ResponseWriter, r *http.Request) {
	rs, _, _, msg, ok := rpc.CheckService(w, r, grpc.ServiceRegistry)
	if ok {
		service := rs.(*grpc.GRPCService)
		port := util.GetRequestOrListenerPortNum(r)
		GRPCFactory.StopService(port, service)
		lock.Lock()
		delete(ActiveServices[port], service.Name)
		if len(ActiveServices[port]) == 0 {
			delete(ActiveServices, port)
		}
		lock.Unlock()
		msg = fmt.Sprintf("Service [%s] registered for serving on port [%d]", service.Name, port)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func getActiveServices(w http.ResponseWriter, r *http.Request) {
	port := util.GetIntParamValue(r, "port")
	active := strings.Contains(r.RequestURI, "active")
	if active {
		if port > 0 {
			util.WriteJsonPayload(w, ActiveServices[port])
		} else {
			util.WriteJsonPayload(w, ActiveServices)
		}
	} else {
		util.WriteJsonPayload(w, gotogrpc.ServiceRegistry.Services)
	}
}

func proxyGRPCService(w http.ResponseWriter, r *http.Request) {
	rs, _, _, msg, ok := rpc.CheckService(w, r, grpc.ServiceRegistry)
	upstream := util.GetStringParamValue(r, "upstream")
	if ok {
		targetService := util.GetStringParamValue(r, "targetService")
		teeport := util.GetIntParamValue(r, "teeport")
		delayMin, delayMax, delayCount, ok := util.GetDurationParam(r, "delay")
		if !ok {
			delayMin = 0
			delayMax = 0
			delayCount = 0
		}
		port := util.GetRequestOrListenerPortNum(r)
		proxy := proxy.GetGRPCProxyForPort(port)
		proxy.SetupGRPCProxy(rs.GetName(), targetService, nil, upstream, "", teeport, delayMin, delayMax, delayCount)
		msg = fmt.Sprintf("Service [%s] will be proxied on port [%d] to upstream [%s] target service [%s]", rs.GetName(), port, upstream, targetService)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func getGRPCProxyDetails(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	proxy := proxy.GetGRPCProxyForPort(port)
	util.WriteJsonPayload(w, proxy)
	util.AddLogMessage("GRPC Proxy Details Returned", r)
}

func clearGRPCProxies(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	proxy := proxy.GetGRPCProxyForPort(port)
	proxy.Init()
	util.AddLogMessage("GRPC Proxy cleared", r)
}

func removeGRPCProxy(w http.ResponseWriter, r *http.Request) {
	service := util.GetStringParamValue(r, "service")
	port := util.GetRequestOrListenerPortNum(r)
	proxy := proxy.GetGRPCProxyForPort(port)
	proxy.RemoveProxy(service)
	util.AddLogMessage(fmt.Sprintf("GRPC Proxy [%s] removed", service), r)
}

func loadReflectedServices(w http.ResponseWriter, r *http.Request) {
	msg := ""
	defer func() {
		if msg != "" {
			fmt.Fprintln(w, msg)
			util.AddLogMessage(msg, r)
		}
	}()
	upstream := util.GetStringParamValue(r, "upstream")
	if upstream == "" {
		w.WriteHeader(http.StatusBadRequest)
		msg = "Missing upstream"
		return
	}
	err := grpcclient.LoadRemoteReflectedServices(upstream)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = err.Error()
		return
	}
	util.WriteJsonPayload(w, gotogrpc.ServiceRegistry.Services)
	util.AddLogMessage("Remote services loaded", r)
}
