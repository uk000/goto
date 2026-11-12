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

package label

import (
	"fmt"
	"net/http"

	"goto/pkg/server/listeners"
	"goto/pkg/server/middleware"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("label", setRoutes, nil)
)

// setRoutes registers the /server/label routes to set, clear and get the label of a listener.
func setRoutes(r *mux.Router) {
	server := middleware.RootPath("/server")
	labelRouter := util.PathRouter(server, "/label")
	util.AddRoute(labelRouter, "/set/{label}", setLabel, "PUT", "POST")
	util.AddRoute(labelRouter, "/clear", setLabel, "POST")
	util.AddRoute(labelRouter, "", getLabel)
}

// setLabel sets or clears the label of the request's listener and reports it.
func setLabel(w http.ResponseWriter, r *http.Request) {
	msg := ""
	if label := listeners.SetListenerLabel(r); label == "" {
		msg = fmt.Sprintf("Port [%s] Label Cleared", util.GetRequestOrListenerPort(r))
	} else {
		msg = fmt.Sprintf("Will use label %s for all responses on port %s", label, util.GetRequestOrListenerPort(r))
	}
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

// getLabel responds with the label of the request's listener.
func getLabel(w http.ResponseWriter, r *http.Request) {
	label := listeners.GetListenerLabel(r)
	msg := fmt.Sprintf("Port [%s] Label [%s]", util.GetRequestOrListenerPort(r), label)
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, "Server Label: "+label)
}
