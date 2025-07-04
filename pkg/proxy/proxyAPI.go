// Copyright 2025 uk
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package proxy

import (
	"fmt"
	"net/http"
	"strings"

	"goto/pkg/server/middleware"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("proxy", SetRoutes, MiddlewareHandler)
	rootRouter *mux.Router
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	rootRouter = root
	proxyRouter := util.PathRouter(r, "/proxy")
	proxyTargetsRouter := util.PathRouter(r, "/proxy/targets")
	httpTargetsRouter := util.PathRouter(r, "/proxy/http/targets")
	tcpTargetsRouter := util.PathRouter(r, "/proxy/tcp/targets")

	util.AddRouteWithPort(proxyTargetsRouter, "/clear", clearProxyTargets, "POST")
	util.AddRouteWithPort(proxyTargetsRouter, "/add", addProxyTarget, "POST", "PUT")
	util.AddRouteWithPort(proxyTargetsRouter, "/{target}/remove", removeProxyTarget, "POST")
	util.AddRouteWithPort(proxyTargetsRouter, "/{target}/enable", enableProxyTarget, "POST")
	util.AddRouteWithPort(proxyTargetsRouter, "/{target}/disable", disableProxyTarget, "POST")

	util.AddRouteMultiQWithPort(httpTargetsRouter, "/add/{target}", addHTTPProxyTarget, []string{"url", "proto", "from", "to"}, "POST", "PUT")
	util.AddRouteMultiQWithPort(httpTargetsRouter, "/{target}/route", addTargetRoute, []string{"from", "to"}, "PUT", "POST")
	util.AddRouteWithPort(httpTargetsRouter, "/{target}/match/header/{key}={value}", addHeaderMatch, "PUT", "POST")
	util.AddRouteWithPort(httpTargetsRouter, "/{target}/match/header/{key}", addHeaderMatch, "PUT", "POST")
	util.AddRouteWithPort(httpTargetsRouter, "/{target}/match/query/{key}={value}", addQueryMatch, "PUT", "POST")
	util.AddRouteWithPort(httpTargetsRouter, "/{target}/match/query/{key}", addQueryMatch, "PUT", "POST")
	util.AddRouteWithPort(httpTargetsRouter, "/{target}/headers/add/{key}={value}", addTargetHeader, "PUT", "POST")
	util.AddRouteWithPort(httpTargetsRouter, "/{target}/headers/remove/{key}", removeTargetHeader, "PUT", "POST")
	util.AddRouteWithPort(httpTargetsRouter, "/{target}/query/add/{key}={value}", addTargetQuery, "PUT", "POST")
	util.AddRouteWithPort(httpTargetsRouter, "/{target}/query/remove/{key}", removeTargetQuery, "PUT", "POST")

	util.AddRouteMultiQWithPort(tcpTargetsRouter, "/add/{target}", addTCPProxyTarget, []string{"address", "sni"}, "POST", "PUT")

	util.AddRouteWithPort(proxyTargetsRouter, "/{target}/delay={delay}", setProxyTargetDelay, "PUT", "POST")
	util.AddRouteWithPort(proxyTargetsRouter, "/{target}/delay/clear", clearProxyTargetDelay, "POST")
	util.AddRouteWithPort(proxyTargetsRouter, "/{target}/drop={drop}", setProxyTargetDrops, "PUT", "POST")
	util.AddRouteWithPort(proxyTargetsRouter, "/{target}/drop/clear", clearProxyTargetDrops, "POST")

	util.AddRouteWithPort(proxyTargetsRouter, "", getProxyTargets, "GET")
	util.AddRouteWithPort(proxyTargetsRouter, "/{target}/report", getProxyTargetReport, "GET")
	util.AddRouteWithPort(proxyRouter, "/report/http", getProxyReport, "GET")
	util.AddRouteWithPort(proxyRouter, "/report/tcp", getProxyReport, "GET")
	util.AddRouteWithPort(proxyRouter, "/report", getProxyReport, "GET")
	util.AddRouteWithPort(proxyRouter, "/report/clear", clearProxyReport, "POST")
	util.AddRouteWithPort(proxyRouter, "/all/report", getAllProxiesReports, "GET")
	util.AddRouteWithPort(proxyRouter, "/all/report/clear", clearAllProxiesReports, "POST")

	util.AddRouteWithPort(proxyRouter, "/enable", enableProxy, "POST")
	util.AddRouteWithPort(proxyRouter, "/disable", disableProxy, "POST")
}

func addProxyTarget(w http.ResponseWriter, r *http.Request) {
	getPortProxy(r).addProxyTarget(w, r)
}

func addHTTPProxyTarget(w http.ResponseWriter, r *http.Request) {
	getPortProxy(r).addNewProxyTarget(w, r, false)
}

func addTCPProxyTarget(w http.ResponseWriter, r *http.Request) {
	getPortProxy(r).addNewProxyTarget(w, r, true)
}

func addTargetRoute(w http.ResponseWriter, r *http.Request) {
	getPortProxy(r).addTargetRoute(w, r)
}

func addHeaderMatch(w http.ResponseWriter, r *http.Request) {
	getPortProxy(r).addHeaderOrQueryMatch(w, r, true)
}

func addQueryMatch(w http.ResponseWriter, r *http.Request) {
	getPortProxy(r).addHeaderOrQueryMatch(w, r, false)
}

func addTargetHeader(w http.ResponseWriter, r *http.Request) {
	getPortProxy(r).addTargetHeaderOrQuery(w, r, true)
}

func removeTargetHeader(w http.ResponseWriter, r *http.Request) {
	getPortProxy(r).removeTargetHeaderOrQuery(w, r, true)
}

func addTargetQuery(w http.ResponseWriter, r *http.Request) {
	getPortProxy(r).addTargetHeaderOrQuery(w, r, false)
}

func removeTargetQuery(w http.ResponseWriter, r *http.Request) {
	getPortProxy(r).removeTargetHeaderOrQuery(w, r, false)
}

func setProxyTargetDelay(w http.ResponseWriter, r *http.Request) {
	getPortProxy(r).setProxyTargetDelay(w, r)
}

func clearProxyTargetDelay(w http.ResponseWriter, r *http.Request) {
	getPortProxy(r).clearProxyTargetDelay(w, r)
}

func setProxyTargetDrops(w http.ResponseWriter, r *http.Request) {
	getPortProxy(r).setProxyTargetDrops(w, r)
}

func clearProxyTargetDrops(w http.ResponseWriter, r *http.Request) {
	getPortProxy(r).clearProxyTargetDrops(w, r)
}

func removeProxyTarget(w http.ResponseWriter, r *http.Request) {
	getPortProxy(r).removeProxyTarget(w, r)
}

func enableProxyTarget(w http.ResponseWriter, r *http.Request) {
	getPortProxy(r).enableProxyTarget(w, r)
}

func disableProxyTarget(w http.ResponseWriter, r *http.Request) {
	getPortProxy(r).disableProxyTarget(w, r)
}

func clearProxyTargets(w http.ResponseWriter, r *http.Request) {
	listenerPort := util.GetRequestOrListenerPortNum(r)
	proxyLock.Lock()
	defer proxyLock.Unlock()
	proxyByPort[listenerPort] = newProxy(listenerPort)
	w.WriteHeader(http.StatusOK)
	util.AddLogMessage("Proxy targets cleared", r)
	fmt.Fprintln(w, "Proxy targets cleared")
}

func getProxyTargets(w http.ResponseWriter, r *http.Request) {
	p := getPortProxy(r)
	util.AddLogMessage("Reporting proxy targets", r)
	result := map[string]interface{}{}
	result["Port"] = p.Port
	result["Enabled"] = p.Enabled
	result["HTTP"] = p.HTTPTargets
	result["TCP"] = p.TCPTargets
	util.WriteJsonPayload(w, result)
}

func getProxyTargetReport(w http.ResponseWriter, r *http.Request) {
	p := getPortProxy(r)
	target := p.checkAndGetTarget(w, r)
	if target == nil {
		return
	}
	result := map[string]interface{}{}
	result["Port"] = p.Port
	result["Target"] = target.Name
	if http := p.HTTPTracker.TargetTrackers[target.Name]; http != nil {
		result["HTTP"] = p.HTTPTracker.TargetTrackers[target.Name]
	}
	if tcp := p.TCPTracker.TargetTrackers[target.Name]; tcp != nil {
		result["TCP"] = tcp
	}
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, util.ToJSONText(result))
	util.AddLogMessage(fmt.Sprintf("Proxy[%d]: TCP Target [%s] Reported", p.Port, target.Name), r)
}

func getProxyReport(w http.ResponseWriter, r *http.Request) {
	p := getPortProxy(r)
	tcpOnly := strings.Contains(r.RequestURI, "tcp")
	httpOnly := strings.Contains(r.RequestURI, "http")
	result := map[string]interface{}{}
	result["Port"] = p.Port
	result["Enabled"] = p.Enabled
	if !tcpOnly {
		result["HTTP"] = p.HTTPTracker
	}
	if !httpOnly {
		result["TCP"] = p.TCPTracker
	}
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, util.ToJSONText(result))
	util.AddLogMessage(fmt.Sprintf("Proxy[%d]: Reported", p.Port), r)
}

func clearProxyReport(w http.ResponseWriter, r *http.Request) {
	p := getPortProxy(r)
	p.initTracker()
	w.WriteHeader(http.StatusOK)
	msg := fmt.Sprintf("Proxy[%d]: Tracking Info Cleared", p.Port)
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func getAllProxiesReports(w http.ResponseWriter, r *http.Request) {
	result := map[int]map[string]interface{}{}
	for port, p := range proxyByPort {
		result[port] = map[string]interface{}{}
		result[port]["Enabled"] = p.Enabled
		result[port]["HTTP"] = p.HTTPTracker
		result[port]["TCP"] = p.TCPTracker
	}
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, util.ToJSONText(result))
	util.AddLogMessage("All Proxies Reported", r)
}

func clearAllProxiesReports(w http.ResponseWriter, r *http.Request) {
	for _, p := range proxyByPort {
		p.initTracker()
	}
	w.WriteHeader(http.StatusOK)
	msg := "All Proxies Tracking Info Cleared"
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func enableProxy(w http.ResponseWriter, r *http.Request) {
	p := getPortProxy(r)
	p.enable(true)
	w.WriteHeader(http.StatusOK)
	msg := fmt.Sprintf("Proxy enabled on port [%d]", p.Port)
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func disableProxy(w http.ResponseWriter, r *http.Request) {
	p := getPortProxy(r)
	p.enable(false)
	w.WriteHeader(http.StatusOK)
	msg := fmt.Sprintf("Proxy disabled on port [%d]", p.Port)
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}
