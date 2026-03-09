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

package udpproxy

import (
	"fmt"
	"goto/pkg/server/listeners"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net/http"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("udpproxy", setRoutes, nil)
)

func setRoutes(r *mux.Router, root *mux.Router) {
	proxyRouter := middleware.RootPath("/proxy")
	udpProxyRouter := util.PathPrefix(proxyRouter, "/udp")
	util.AddRoute(udpProxyRouter, "/{port}/{endpoint}", proxyUDP, "POST")
	util.AddRoute(udpProxyRouter, "/{port}/{endpoint}/delay/{delay}", proxyUDP, "POST")
	util.AddRoute(udpProxyRouter, "/{port}/delay/{delay}", setUDPDelay, "POST")
}

func proxyUDP(w http.ResponseWriter, r *http.Request) {
	port := util.GetIntParamValue(r, "port")
	endpoint := util.GetStringParamValue(r, "endpoint")
	delayMin, delayMax, _, _ := util.GetDurationParam(r, "delay")
	msg := ""
	status := http.StatusOK
	if port <= 0 || endpoint == "" {
		status = http.StatusBadRequest
		msg = fmt.Sprintf("Invalid port [%d] or upstream address [%s]", port, endpoint)
	} else if err := listeners.AddListener(port, false, true, ""); err == nil {
		ProxyUDPUpstream(port, endpoint, delayMin, delayMax)
		msg = fmt.Sprintf("Proxying UDP on port [%d] to upstream [%s]", port, endpoint)
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
