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

package proxy

import (
	"fmt"
	"net/http"
	"strings"

	"goto/pkg/events"
	"goto/pkg/server/listeners"
	"goto/pkg/server/middleware"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("proxy", setRoutes, middlewareFunc)
	rootRouter *mux.Router
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	rootRouter = root
	proxyRouter := util.PathRouter(r, "/proxy")
	proxyTargetsRouter := util.PathRouter(r, "/proxy/targets")
	httpTargetsRouter := util.PathRouter(r, "/proxy/http/targets")
	tcpTargetsRouter := util.PathRouter(r, "/proxy/tcp/targets")
	tcpProxyRouter := util.PathRouter(r, "/proxy/tcp")
	udpProxyRouter := util.PathRouter(r, "/proxy/udp")

	util.AddRouteWithPort(proxyTargetsRouter, "/clear", clearProxyTargets, "POST")
	util.AddRouteWithPort(proxyTargetsRouter, "/add", addProxyTarget, "POST", "PUT")
	util.AddRouteWithPort(proxyTargetsRouter, "/{target}/remove", removeProxyTarget, "POST")
	util.AddRouteWithPort(proxyTargetsRouter, "/{target}/enable", enableProxyTarget, "POST")
	util.AddRouteWithPort(proxyTargetsRouter, "/{target}/disable", disableProxyTarget, "POST")

	util.AddRouteMultiQWithPort(httpTargetsRouter, "/add/{target}", addHTTPProxyTarget, []string{"url", "proto", "from", "to", "sni"}, "POST", "PUT")
	util.AddRouteMultiQWithPort(httpTargetsRouter, "/{target}/route", addTargetRoute, []string{"from", "to"}, "PUT", "POST")
	util.AddRouteWithPort(httpTargetsRouter, "/{target}/match/header/{key}={value}", addHeaderMatch, "PUT", "POST")
	util.AddRouteWithPort(httpTargetsRouter, "/{target}/match/header/{key}", addHeaderMatch, "PUT", "POST")
	util.AddRouteWithPort(httpTargetsRouter, "/{target}/match/query/{key}={value}", addQueryMatch, "PUT", "POST")
	util.AddRouteWithPort(httpTargetsRouter, "/{target}/match/query/{key}", addQueryMatch, "PUT", "POST")
	util.AddRouteWithPort(httpTargetsRouter, "/{target}/headers/add/{key}={value}", addTargetHeader, "PUT", "POST")
	util.AddRouteWithPort(httpTargetsRouter, "/{target}/headers/remove/{key}", removeTargetHeader, "PUT", "POST")
	util.AddRouteWithPort(httpTargetsRouter, "/{target}/query/add/{key}={value}", addTargetQuery, "PUT", "POST")
	util.AddRouteWithPort(httpTargetsRouter, "/{target}/query/remove/{key}", removeTargetQuery, "PUT", "POST")

	util.AddRouteMultiQWithPort(tcpTargetsRouter, "/add/{target}", addTCPProxyTarget, []string{"address", "sni", "retries"}, "POST", "PUT")
	util.AddRouteQWithPort(tcpProxyRouter, "/{port}/{endpoint}", proxyTCPOrUDP, "sni", "POST")
	util.AddRouteQWithPort(tcpProxyRouter, "/{port}/{endpoint}/retries/{retries}", proxyTCPOrUDP, "sni", "POST")
	util.AddRouteWithPort(tcpProxyRouter, "/{port}/{endpoint}/retries/{retries}", proxyTCPOrUDP, "POST")
	util.AddRouteWithPort(tcpProxyRouter, "/{port}/{endpoint}", proxyTCPOrUDP, "POST")

	util.AddRouteWithPort(udpProxyRouter, "/{port}/{endpoint}", proxyTCPOrUDP, "POST")
	util.AddRouteWithPort(udpProxyRouter, "/{port}/{endpoint}/delay/{delay}", proxyTCPOrUDP, "POST")
	util.AddRouteWithPort(udpProxyRouter, "/{port}/delay/{delay}", setUDPDelay, "POST")

	util.AddRouteWithPort(proxyTargetsRouter, "/{target}/delay={delay}", setProxyTargetDelay, "PUT", "POST")
	util.AddRouteWithPort(proxyTargetsRouter, "/{target}/delay/clear", clearProxyTargetDelay, "POST")
	util.AddRouteWithPort(proxyTargetsRouter, "/{target}/drop={drop}", setProxyTargetDrops, "PUT", "POST")
	util.AddRouteWithPort(proxyTargetsRouter, "/{target}/drop/clear", clearProxyTargetDrops, "POST")

	util.AddRouteWithPort(proxyTargetsRouter, "", getProxyTargets, "GET")
	util.AddRouteWithPort(proxyTargetsRouter, "/{target}/report", getProxyTargetReport, "GET")
	util.AddRouteWithPort(proxyRouter, "/report/http", getProxyReport, "GET")
	util.AddRouteWithPort(proxyRouter, "/report/tcp", getProxyReport, "GET")
	util.AddRouteWithPort(proxyRouter, "/report/grpc", getProxyReport, "GET")
	util.AddRouteWithPort(proxyRouter, "/report", getProxyReport, "GET")
	util.AddRouteWithPort(proxyRouter, "/report/clear", clearProxyReport, "POST")
	util.AddRouteWithPort(proxyRouter, "/all/report", getAllProxiesReports, "GET")
	util.AddRouteWithPort(proxyRouter, "/all/report/clear", clearAllProxiesReports, "POST")

	util.AddRouteWithPort(proxyRouter, "/enable", enableProxy, "POST")
	util.AddRouteWithPort(proxyRouter, "/disable", disableProxy, "POST")
}

func addProxyTarget(w http.ResponseWriter, r *http.Request) {
	getHTTPProxyForRequestPort(r).addProxyTarget(w, r)
}

func addHTTPProxyTarget(w http.ResponseWriter, r *http.Request) {
	getHTTPProxyForRequestPort(r).addNewHTTPTarget(w, r)
}

func addTCPProxyTarget(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	target := util.GetStringParamValue(r, "target")
	address := util.GetStringParamValue(r, "address")
	sni := util.GetStringParamValue(r, "sni")
	retries := util.GetIntParamValue(r, "retries")
	getTCPProxyForPort(port).addNewProxyTarget(target, address, sni, retries)
	msg := fmt.Sprintf("Port [%d]: Added TCP proxy target [%s] with upstream address [%s], SNI [%s]", port, target, address, sni)
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
	events.SendRequestEventJSON("Proxy Target Added", target, address, r)
}

func proxyTCPOrUDP(w http.ResponseWriter, r *http.Request) {
	port := util.GetIntParamValue(r, "port")
	isTCP := strings.Contains(r.URL.Path, "tcp")
	isUDP := strings.Contains(r.URL.Path, "udp")
	endpoint := util.GetStringParamValue(r, "endpoint")
	retries := util.GetIntParamValue(r, "retries")
	delayMin, delayMax, _, _ := util.GetDurationParam(r, "delay")
	msg := ""
	status := http.StatusOK
	if port <= 0 || endpoint == "" {
		status = http.StatusBadRequest
		msg = fmt.Sprintf("Invalid port [%d] or upstream address [%s]", port, endpoint)
	} else if err := listeners.AddListener(port, isTCP, isUDP, ""); err == nil {
		if isTCP {
			proxy := getTCPProxyForPort(port)
			proxy.addNewProxyTarget(endpoint, endpoint, "", retries)
			msg = fmt.Sprintf("Proxying TCP on port [%d] to upstream [%s] with retries [%d]", port, endpoint, retries)
		} else {
			ProxyUDPUpstream(port, endpoint, delayMin, delayMax)
			msg = fmt.Sprintf("Proxying UDP on port [%d] to upstream [%s]", port, endpoint)
		}
	} else {
		status = http.StatusBadRequest
		msg = fmt.Sprintf("Failed to open listener on port [%d] with error: %s", port, err.Error())
	}
	w.WriteHeader(status)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func setUDPDelay(w http.ResponseWriter, r *http.Request) {
	if !listeners.ValidateUDPListener(w, r) {
		return
	}
	port := util.GetIntParamValue(r, "port")
	upstream := util.GetStringParamValue(r, "upstream")
	msg := ""
	if delayMin, delayMax, _, ok := util.GetDurationParam(r, "delay"); ok {
		SetUDPDelay(port, upstream, delayMin, delayMax)
		msg = fmt.Sprintf("Delay configured for UDP port [%d]", port)
	} else {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Invalid delay value [%s]", util.GetStringParamValue(r, "delay"))
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func addTargetRoute(w http.ResponseWriter, r *http.Request) {
	getHTTPProxyForRequestPort(r).addTargetRoute(w, r)
}

func addHeaderMatch(w http.ResponseWriter, r *http.Request) {
	getHTTPProxyForRequestPort(r).addHeaderOrQueryMatch(w, r, true)
}

func addQueryMatch(w http.ResponseWriter, r *http.Request) {
	getHTTPProxyForRequestPort(r).addHeaderOrQueryMatch(w, r, false)
}

func addTargetHeader(w http.ResponseWriter, r *http.Request) {
	getHTTPProxyForRequestPort(r).addTargetHeaderOrQuery(w, r, true)
}

func removeTargetHeader(w http.ResponseWriter, r *http.Request) {
	getHTTPProxyForRequestPort(r).removeTargetHeaderOrQuery(w, r, true)
}

func addTargetQuery(w http.ResponseWriter, r *http.Request) {
	getHTTPProxyForRequestPort(r).addTargetHeaderOrQuery(w, r, false)
}

func removeTargetQuery(w http.ResponseWriter, r *http.Request) {
	getHTTPProxyForRequestPort(r).removeTargetHeaderOrQuery(w, r, false)
}

func setProxyTargetDelay(w http.ResponseWriter, r *http.Request) {
	getHTTPProxyForRequestPort(r).setProxyTargetDelay(w, r)
}

func clearProxyTargetDelay(w http.ResponseWriter, r *http.Request) {
	getHTTPProxyForRequestPort(r).clearProxyTargetDelay(w, r)
}

func setProxyTargetDrops(w http.ResponseWriter, r *http.Request) {
	getHTTPProxyForRequestPort(r).setProxyTargetDrops(w, r)
}

func clearProxyTargetDrops(w http.ResponseWriter, r *http.Request) {
	getHTTPProxyForRequestPort(r).clearProxyTargetDrops(w, r)
}

func removeProxyTarget(w http.ResponseWriter, r *http.Request) {
	getHTTPProxyForRequestPort(r).removeProxyTarget(w, r)
}

func enableProxyTarget(w http.ResponseWriter, r *http.Request) {
	getHTTPProxyForRequestPort(r).enableProxyTarget(w, r)
}

func disableProxyTarget(w http.ResponseWriter, r *http.Request) {
	getHTTPProxyForRequestPort(r).disableProxyTarget(w, r)
}

func clearProxyTargets(w http.ResponseWriter, r *http.Request) {
	listenerPort := util.GetRequestOrListenerPortNum(r)
	proxyLock.Lock()
	defer proxyLock.Unlock()
	if httpProxyByPort[listenerPort] != nil {
		httpProxyByPort[listenerPort] = newHTTPProxy(listenerPort)
	} else if tcpProxyByPort[listenerPort] != nil {
		tcpProxyByPort[listenerPort] = newTCPProxy(listenerPort)
	}
	w.WriteHeader(http.StatusOK)
	util.AddLogMessage("Proxy targets cleared", r)
	fmt.Fprintln(w, "Proxy targets cleared")
}

func getProxyTargets(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	hp := getHTTPProxyForPort(port)
	tp := getTCPProxyForPort(port)
	util.AddLogMessage("Reporting proxy targets", r)
	result := map[string]interface{}{}
	result["Port"] = port
	result["HTTP"] = hp.Targets
	result["TCP"] = tp.Targets
	util.WriteJsonPayload(w, result)
}

func getProxyTargetReport(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	hp := getHTTPProxyForPort(port)
	tp := getTCPProxyForPort(port)
	target := hp.checkAndGetTarget(w, r)
	if target == nil {
		target = tp.checkAndGetTarget(w, r)
	}
	if target == nil {
		return
	}
	result := map[string]interface{}{}
	result["Port"] = port
	result["Target"] = target.GetName()
	if t := hp.HTTPTracker.TargetTrackers[target.GetName()]; t != nil {
		result["HTTP"] = t
	}
	if t := tp.Tracker.TargetTrackers[target.GetName()]; t != nil {
		result["TCP"] = t
	}
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, util.ToJSONText(result))
	util.AddLogMessage(fmt.Sprintf("Proxy[%d]: TCP Target [%s] Reported", port, target.GetName()), r)
}

func getProxyReport(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	tcpOnly := strings.Contains(r.RequestURI, "tcp")
	httpOnly := strings.Contains(r.RequestURI, "http")
	grpcOnly := strings.Contains(r.RequestURI, "grpc")
	all := !(tcpOnly || httpOnly || grpcOnly)
	result := map[string]interface{}{}
	result["Port"] = port
	if all || httpOnly {
		hp := getHTTPProxyForPort(port)
		result["HTTP"] = hp.HTTPTracker
		result["Enabled"] = hp.Enabled
	}
	if all || tcpOnly {
		tp := getTCPProxyForPort(port)
		result["TCP"] = tp.Tracker
	}
	if all || grpcOnly {
		gp := GetGRPCProxyForPort(port)
		result["GRPC"] = gp.Tracker
	}
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, util.ToJSONText(result))
	util.AddLogMessage(fmt.Sprintf("Proxy[%d]: Reported", port), r)
}

func clearProxyReport(w http.ResponseWriter, r *http.Request) {
	p := getHTTPProxyForRequestPort(r)
	p.initTracker()
	w.WriteHeader(http.StatusOK)
	msg := fmt.Sprintf("Proxy[%d]: Tracking Info Cleared", p.Port)
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func getAllProxiesReports(w http.ResponseWriter, r *http.Request) {
	result := map[int]map[string]interface{}{}
	for port, p := range httpProxyByPort {
		result[port] = map[string]interface{}{}
		result[port]["Enabled"] = p.Enabled
		result[port]["HTTP"] = p.HTTPTracker
	}
	for port, p := range tcpProxyByPort {
		result[port] = map[string]interface{}{}
		result[port]["Enabled"] = p.Enabled
		result[port]["TCP"] = p.Tracker
	}
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, util.ToJSONText(result))
	util.AddLogMessage("All Proxies Reported", r)
}

func clearAllProxiesReports(w http.ResponseWriter, r *http.Request) {
	for _, p := range httpProxyByPort {
		p.initTracker()
	}
	for _, p := range tcpProxyByPort {
		p.initTracker()
	}
	w.WriteHeader(http.StatusOK)
	msg := "All Proxies Tracking Info Cleared"
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func enableProxy(w http.ResponseWriter, r *http.Request) {
	p := getHTTPProxyForRequestPort(r)
	p.enable(true)
	w.WriteHeader(http.StatusOK)
	msg := fmt.Sprintf("Proxy enabled on port [%d]", p.Port)
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func disableProxy(w http.ResponseWriter, r *http.Request) {
	p := getHTTPProxyForRequestPort(r)
	p.enable(false)
	w.WriteHeader(http.StatusOK)
	msg := fmt.Sprintf("Proxy disabled on port [%d]", p.Port)
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}
