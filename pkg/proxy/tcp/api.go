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

package tcpproxy

import (
	"fmt"
	"goto/pkg/global"
	"goto/pkg/server/listeners"
	"goto/pkg/server/middleware"
	"goto/pkg/types"
	"goto/pkg/util"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("tcpproxy", setRoutes, nil)
)

func setRoutes(r *mux.Router) {
	proxyRouter := middleware.RootPath("/proxy")
	tcpProxyRouter := util.PathPrefix(proxyRouter, "/tcp")
	util.AddRouteWithMultiQ(tcpProxyRouter, "/{port}", proxyTCP, [][]string{{"address"}}, "POST")
	util.AddRoute(tcpProxyRouter, "", getProxy, "GET")
	util.AddRoute(tcpProxyRouter, "/all", getProxy, "GET")

	upRouter := util.PathPrefix(tcpProxyRouter, "/upstreams")
	util.AddRoute(upRouter, "/add", addTCPProxyUpstreams, "POST", "PUT")
	util.AddRoute(upRouter, "", getProxyUpstreams, "GET")
	util.AddRoute(upRouter, "/all", getProxyUpstreams, "GET")

	util.AddRoute(tcpProxyRouter, "/http/connect/{o:enable|disable}", enableConnect, "POST", "PUT")
	util.AddRoute(tcpProxyRouter, "/http/connect/delay/{delay}", setConnectDelay, "POST", "PUT")
}

// enableConnect turns the HTTP CONNECT proxy mode of a port on or off. A port in this mode is a TCP port, as the tunnels
// of CONNECT are streams of bytes that the HTTP server has no part in: enabling the mode makes the port a TCP listener
// (the TCP listener of the port is kept if it has one, which is how a port is made a TLS one: add it as a tls listener
// first), and the tunneling is done by the proxy. See proxyconnect.go.
func enableConnect(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	o := util.GetStringParamValue(r, "o")
	enable := strings.EqualFold(o, "enable")
	msg := ""
	if !enable {
		EnableConnect(port, false)
		msg = fmt.Sprintf("Port [%d]: CONNECT proxy mode disabled", port)
	} else if port == util.GetConnectionPortNum(r) || port == global.Self.ServerPort {
		// The port would stop serving HTTP, which is what this very request came over, or the main port of the server.
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Port [%d] can't be a CONNECT proxy port, as it has to serve HTTP requests. Use another port, which can be configured via this one with the prefix /port={port}", port)
	} else if err := EnableConnectProxy(port); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Port [%d] failed to enable CONNECT proxy mode: %s", port, err.Error())
	} else {
		msg = fmt.Sprintf("Port [%d]: CONNECT proxy mode enabled, and the port is a TCP port", port)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

// setConnectDelay sets the delay that the CONNECT proxy of a port applies to the connection of a client, and to every
// transfer of its tunnels. The delay is a duration, or a range of durations to pick from (e.g. 100ms-1s), and a count after
// a colon (e.g. 1s:5) makes it apply to the next so many of these events of the port, which is the connections and the
// transfers altogether. 0s, or clear, takes the delay away. See proxyconnect.go.
func setConnectDelay(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	val := util.GetStringParamValue(r, "delay")
	msg := ""
	if strings.EqualFold(val, "clear") {
		SetConnectDelay(port, nil)
		msg = fmt.Sprintf("Port [%d]: CONNECT proxy delay cleared", port)
	} else if low, high, count, ok := util.GetDurationParam(r, "delay"); !ok {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Invalid delay value [%s], expected a duration or a range of durations, with an optional count (e.g. 1s, 100ms-1s, 1s:5)", val)
	} else if delay := types.NewDelay(low, high, count); delay == nil {
		SetConnectDelay(port, nil)
		msg = fmt.Sprintf("Port [%d]: CONNECT proxy delay cleared", port)
	} else {
		SetConnectDelay(port, delay)
		times := "unlimited"
		if delay.Count > 0 {
			times = strconv.Itoa(delay.Count)
		}
		msg = fmt.Sprintf("Port [%d]: CONNECT proxy delay set to [%s], count [%s]", port, delay.String(), times)
		if !WillProxyConnect(port) {
			msg += ", which applies once CONNECT proxy mode is enabled on the port"
		}
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func addTCPProxyUpstreams(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	msg := ""
	if upstreams, err := parseUpstreams(r.Body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Failed to parse proxy target with error: %s", err.Error())
		fmt.Fprintln(w, msg)
	} else {
		proxy := GetPortProxy(port)
		proxy.AddUpstreams(upstreams)
		msg = fmt.Sprintf("Port [%d]: Added [%d] TCP proxy upstreams", port, len(upstreams))
		util.WriteJsonOrYAMLPayload(w, upstreams, true)
	}
	util.AddLogMessage(msg, r)
}

func proxyTCP(w http.ResponseWriter, r *http.Request) {
	port := util.GetIntParamValue(r, "port")
	address := util.GetStringParamValue(r, "address")
	msg := ""
	status := http.StatusOK
	if port <= 0 || address == "" {
		status = http.StatusBadRequest
		msg = fmt.Sprintf("Invalid port [%d] or upstream address [%s]", port, address)
	} else if err := validateAddress(address); err != nil {
		status = http.StatusBadRequest
		msg = fmt.Sprintf("Invalid upstream address: %s", err.Error())
	} else if err := listeners.AddListener(port, true, false, ""); err == nil {
		proxy := GetPortProxy(port)
		proxy.addNewUpstream("default", address)
		msg = fmt.Sprintf("Proxying TCP on port [%d] to upstream [%s]", port, address)
	} else {
		status = http.StatusBadRequest
		msg = fmt.Sprintf("Failed to open listener on port [%d] with error: %s", port, err.Error())
	}
	w.WriteHeader(status)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

// isAllRequest tells whether the request is for the data of all the ports, i.e. the URI ends with /all.
func isAllRequest(r *http.Request) bool {
	return strings.HasSuffix(strings.TrimRight(r.URL.Path, "/"), "/all")
}

func getProxy(w http.ResponseWriter, r *http.Request) {
	result := map[string]any{}
	if isAllRequest(r) {
		for port, proxy := range snapshotProxies() {
			result[strconv.Itoa(port)] = proxy
		}
	} else {
		port := util.GetRequestOrListenerPortNum(r)
		proxy := GetPortProxy(port)
		result["port"] = port
		result["tcp"] = proxy
	}
	util.WriteJsonPayload(w, result)
	util.AddLogMessage("Reported proxy targets", r)
}

func getProxyUpstreams(w http.ResponseWriter, r *http.Request) {
	result := map[string]any{}
	if isAllRequest(r) {
		for port, proxy := range snapshotProxies() {
			result[strconv.Itoa(port)] = proxy.getUpstreams()
		}
	} else {
		port := util.GetRequestOrListenerPortNum(r)
		proxy := GetPortProxy(port)
		result["port"] = port
		result["tcp"] = proxy.getUpstreams()
	}
	util.WriteJsonPayload(w, result)
	util.AddLogMessage("Reported proxy upstreams", r)
}
