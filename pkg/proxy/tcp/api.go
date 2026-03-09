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

package tcpproxy

import (
	"fmt"
	"goto/pkg/events"
	"goto/pkg/server/listeners"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net/http"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("tcpproxy", setRoutes, nil)
)

func setRoutes(r *mux.Router, root *mux.Router) {
	proxyRouter := middleware.RootPath("/proxy")
	tcpProxyRouter := util.PathPrefix(proxyRouter, "/tcp")
	tcpTargetsRouter := util.PathPrefix(tcpProxyRouter, "/targets")
	util.AddRouteWithMultiQ(tcpProxyRouter, "/{port}", proxyTCP, [][]string{{"address"}, {"retries", "delay"}, {"sni"}}, "POST")
	util.AddRouteQO(tcpProxyRouter, "/{port}/{endpoint}", proxyTCP, "sni", "POST")
	util.AddRouteQO(tcpProxyRouter, "/{port}/{endpoint}/retries/{retries}", proxyTCP, "sni", "POST")
	util.AddRouteQO(tcpProxyRouter, "/{port}/{endpoint}/delay/{delay}", proxyTCP, "sni", "POST")
	util.AddRouteWithMultiQ(tcpTargetsRouter, "/add/{target}", addTCPProxyTarget, [][]string{{"address"}, {"retries", "delay"}, {"sni"}}, "POST", "PUT")
}

func addTCPProxyTarget(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	target := util.GetStringParamValue(r, "target")
	address := util.GetStringParamValue(r, "address")
	sni := util.GetStringParamValue(r, "sni")
	retries := util.GetIntParamValue(r, "retries")
	delayMin, delayMax, _, _ := util.GetDurationParam(r, "delay")
	getTCPProxyForPort(port).addNewUpstream(target, address, sni, retries, delayMin, delayMax)
	msg := fmt.Sprintf("Port [%d]: Added TCP proxy target [%s] with upstream address [%s], SNI [%s]", port, target, address, sni)
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
	events.SendRequestEventJSON("Proxy Target Added", target, address, r)
}

func proxyTCP(w http.ResponseWriter, r *http.Request) {
	port := util.GetIntParamValue(r, "port")
	endpoint := util.GetStringParamValue(r, "endpoint")
	retries := util.GetIntParamValue(r, "retries")
	delayMin, delayMax, _, _ := util.GetDurationParam(r, "delay")
	msg := ""
	status := http.StatusOK
	if port <= 0 || endpoint == "" {
		status = http.StatusBadRequest
		msg = fmt.Sprintf("Invalid port [%d] or upstream address [%s]", port, endpoint)
	} else if err := listeners.AddListener(port, true, false, ""); err == nil {
		proxy := getTCPProxyForPort(port)
		proxy.addNewUpstream(endpoint, endpoint, "", retries, delayMin, delayMax)
		msg = fmt.Sprintf("Proxying TCP on port [%d] to upstream [%s] with retries [%d]", port, endpoint, retries)
	} else {
		status = http.StatusBadRequest
		msg = fmt.Sprintf("Failed to open listener on port [%d] with error: %s", port, err.Error())
	}
	w.WriteHeader(status)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}
