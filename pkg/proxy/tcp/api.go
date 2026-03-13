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
	"goto/pkg/server/listeners"
	"goto/pkg/server/middleware"
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

func getProxy(w http.ResponseWriter, r *http.Request) {
	all := strings.Contains(r.RequestURI, "all")
	result := map[string]any{}
	if all {
		for port, proxy := range portProxy {
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
	all := strings.Contains(r.RequestURI, "all")
	result := map[string]any{}
	if all {
		for port, proxy := range portProxy {
			result[strconv.Itoa(port)] = proxy.Upstreams
		}
	} else {
		port := util.GetRequestOrListenerPortNum(r)
		proxy := GetPortProxy(port)
		result["port"] = port
		result["tcp"] = proxy.Upstreams
	}
	util.WriteJsonPayload(w, result)
	util.AddLogMessage("Reported proxy upstreams", r)
}
