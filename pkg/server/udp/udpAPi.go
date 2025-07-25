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

package udp

import (
	"fmt"
	"goto/pkg/proxy"
	"goto/pkg/server/listeners"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net/http"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("udp", setRoutes, nil)
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	udpRouter := util.PathPrefix(r, "/?server?/udp")
	util.AddRoute(udpRouter, "/{port}/stop/{upstream}", stopUDPProxy, "POST")
	util.AddRoute(udpRouter, "/{port}/proxy/{upstream}", proxyUDP, "POST")
	util.AddRoute(udpRouter, "/{port}/proxy/{upstream}/delay/{delay}", proxyUDP, "POST")
	util.AddRoute(udpRouter, "/{port}/delay/{upstream}/{delay}", setDelay, "POST")
}

func setDelay(w http.ResponseWriter, r *http.Request) {
	if !listeners.ValidateUDPListener(w, r) {
		return
	}
	port := util.GetIntParamValue(r, "port")
	upstream := util.GetStringParamValue(r, "upstream")
	msg := ""
	if delayMin, delayMax, _, ok := util.GetDurationParam(r, "delay"); ok {
		proxy.SetUDPDelay(port, upstream, delayMin, delayMax)
		msg = fmt.Sprintf("Delay configured for UDP port [%d]", port)
	} else {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Invalid delay value [%s]", util.GetStringParamValue(r, "delay"))
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func proxyUDP(w http.ResponseWriter, r *http.Request) {
	port := util.GetIntParamValue(r, "port")
	upstream := util.GetStringParamValue(r, "upstream")
	delayMin, delayMax, _, _ := util.GetDurationParam(r, "delay")
	msg := ""
	status := http.StatusOK
	if port <= 0 || upstream == "" {
		status = http.StatusBadRequest
		msg = fmt.Sprintf("Invalid port [%d] or upstream address [%s]", port, upstream)
	} else if err := listeners.AddUDPListener(port); err == nil {
		proxy.ProxyUDPUpstream(port, upstream, delayMin, delayMax)
		msg = fmt.Sprintf("Proxying UDP on port [%d] to upstream [%s] with delay [%s-%s]", port, upstream, delayMin, delayMax)
	} else {
		status = http.StatusBadRequest
		msg = fmt.Sprintf("Failed to open UDP listener on port [%d] with error: %s", port, err.Error())
	}
	w.WriteHeader(status)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func stopUDPProxy(w http.ResponseWriter, r *http.Request) {
	if !listeners.ValidateUDPListener(w, r) {
		return
	}
	port := util.GetIntParamValue(r, "port")
	upstream := util.GetStringParamValue(r, "upstream")
	status := http.StatusOK
	msg := ""
	if port <= 0 || upstream == "" {
		status = http.StatusBadRequest
		msg = fmt.Sprintf("Invalid port [%d] or upstream address [%s]", port, upstream)
	} else {
		proxy.StopUDPUpstream(port, upstream)
		msg = fmt.Sprintf("Stopped Proxying UDP on port [%d] to upstream [%s]", port, upstream)
	}
	w.WriteHeader(status)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}
