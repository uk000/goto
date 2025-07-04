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

package tunnel

import (
	"fmt"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("tunnel", SetRoutes, MiddlewareHandler)
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	root.PathPrefix("/tunnel={address}").Subrouter().MatcherFunc(func(*http.Request, *mux.RouteMatch) bool { return true }).HandlerFunc(tunnel)
	tunnelRouter := util.PathRouter(r, "/tunnels")
	util.AddRouteQWithPort(tunnelRouter, "/add/{endpoint}/header/{header}={value}", addTunnel, "uri", "POST", "PUT")
	util.AddRouteWithPort(tunnelRouter, "/add/{endpoint}/header/{header}={value}", addTunnel, "POST", "PUT")
	util.AddRouteQWithPort(tunnelRouter, "/add/{endpoint}/header/{header}", addTunnel, "uri", "POST", "PUT")
	util.AddRouteWithPort(tunnelRouter, "/add/{endpoint}/header/{header}", addTunnel, "POST", "PUT")
	util.AddRouteQWithPort(tunnelRouter, "/add/{endpoint}", addTunnel, "uri", "POST", "PUT")
	util.AddRouteWithPort(tunnelRouter, "/add/{endpoint}/transparent", addTunnel, "POST", "PUT")
	util.AddRouteWithPort(tunnelRouter, "/add/{endpoint}", addTunnel, "POST", "PUT")
	util.AddRouteQWithPort(tunnelRouter, "/remove/{endpoint}/header/{header}={value}", clearTunnel, "uri", "POST", "PUT")
	util.AddRouteWithPort(tunnelRouter, "/remove/{endpoint}/header/{header}={value}", clearTunnel, "POST", "PUT")
	util.AddRouteQWithPort(tunnelRouter, "/remove/{endpoint}/header/{header}", clearTunnel, "uri", "POST", "PUT")
	util.AddRouteWithPort(tunnelRouter, "/remove/{endpoint}/header/{header}", clearTunnel, "POST", "PUT")
	util.AddRouteQWithPort(tunnelRouter, "/remove/{endpoint}", clearTunnel, "uri", "POST", "PUT")
	util.AddRouteWithPort(tunnelRouter, "/remove/{endpoint}", clearTunnel, "POST", "PUT")
	util.AddRouteWithPort(tunnelRouter, "/clear", clearTunnel, "POST", "PUT")

	util.AddRouteWithPort(tunnelRouter, "/track/header/{headers}", addTunnelTracking, "POST", "PUT")
	util.AddRouteWithPort(tunnelRouter, "/track/query/{params}", addTunnelTracking, "POST", "PUT")
	util.AddRouteWithPort(tunnelRouter, "/track/clear", clearTunnelTracking, "POST", "PUT")
	util.AddRouteWithPort(tunnelRouter, "/track", getTunnelTracking, "GET")

	util.AddRouteWithPort(tunnelRouter, "/traffic/capture={yn}", captureTunnelTraffic, "POST", "PUT")
	util.AddRouteWithPort(tunnelRouter, "/traffic", getTunnelTrafficLog, "GET")

	util.AddRouteWithPort(tunnelRouter, "/active", getActiveTunnels, "GET")
	util.AddRouteWithPort(tunnelRouter, "", getTunnels, "GET")
}

func addTunnel(w http.ResponseWriter, r *http.Request) {
	endpoint := util.GetStringParamValue(r, "endpoint")
	uri := util.GetStringParamValue(r, "uri")
	header := util.GetStringParamValue(r, "header")
	value := util.GetStringParamValue(r, "value")
	transparent := strings.HasSuffix(r.RequestURI, "transparent")
	port := util.GetRequestOrListenerPortNum(r)
	GetOrCreatePortTunnel(port).addTunnel(endpoint, r.TLS != nil, transparent, uri, header, value)
	msg := fmt.Sprintf("Tunnel added on port [%d] to endpoint [%s] for URI [%s], Header [%s:%s]", port, endpoint, uri, header, value)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func clearTunnel(w http.ResponseWriter, r *http.Request) {
	endpoint := util.GetStringParamValue(r, "endpoint")
	uri := util.GetStringParamValue(r, "uri")
	header := util.GetStringParamValue(r, "header")
	value := util.GetStringParamValue(r, "value")
	port := util.GetRequestOrListenerPortNum(r)
	pt := GetOrCreatePortTunnel(port)
	msg := ""
	if exists := pt.removeTunnel(endpoint, uri, header, value); exists {
		msg = fmt.Sprintf("Tunnel removed on port [%d] to endpoint [%s] for URI [%s], Header [%s:%s]", port, endpoint, uri, header, value)
	} else {
		msg = fmt.Sprintf("No tunnel to remove on port [%d] to endpoint [%s] for URI [%s], Header [%s:%s]", port, endpoint, uri, header, value)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func addTunnelTracking(w http.ResponseWriter, r *http.Request) {
	headers, _ := util.GetListParam(r, "headers")
	queryParams, _ := util.GetListParam(r, "params")
	port := util.GetRequestOrListenerPortNum(r)
	pt := GetOrCreatePortTunnel(port)
	pt.addTracking(headers, queryParams)
	msg := fmt.Sprintf("Tracking headers %+v and query params %+v added for tunnels on port [%d]", headers, queryParams, port)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func clearTunnelTracking(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	pt := GetOrCreatePortTunnel(port)
	pt.initTracking()
	msg := fmt.Sprintf("Tracking cleared for tunnels on port [%d]", port)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func getTunnelTracking(w http.ResponseWriter, r *http.Request) {
	counts := map[int]*TunnelTraffic{}
	for port, pt := range tunnels {
		counts[port] = pt.Traffic
	}
	util.WriteJsonPayload(w, counts)
	util.AddLogMessage("Reported Tunnel Tracking", r)
}

func captureTunnelTraffic(w http.ResponseWriter, r *http.Request) {
	yn := util.GetBoolParamValue(r, "yn")
	port := util.GetRequestOrListenerPortNum(r)
	GetOrCreatePortTunnel(port).captureTunnelTraffic(yn)
	msg := ""
	if yn {
		msg = fmt.Sprintf("Traffic capture enabled for tunnels on port [%d]", port)
	} else {
		msg = fmt.Sprintf("Traffic capture disabled for tunnels on port [%d]", port)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func getTunnelTrafficLog(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	pt := GetOrCreatePortTunnel(port)
	pt.lock.RLock()
	defer pt.lock.RUnlock()
	util.WriteJsonPayload(w, pt.Traffic)
	util.AddLogMessage("Reported Tunnel Tracking", r)
}

func getActiveTunnels(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	pt := GetOrCreatePortTunnel(port)
	pt.lock.RLock()
	defer pt.lock.RUnlock()
	util.WriteJsonPayload(w, pt.ProxyTunnels)
	util.AddLogMessage("Reported Active Tunnels", r)
}

func getTunnels(w http.ResponseWriter, r *http.Request) {
	util.WriteJsonPayload(w, tunnels)
	util.AddLogMessage("Reported Tunnels", r)
}
