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

package label

import (
	"fmt"
	"net/http"

	"goto/pkg/events"
	"goto/pkg/server/listeners"
	"goto/pkg/server/middleware"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("label", setRoutes, nil)
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	labelRouter := util.PathRouter(r, "/server?/label")
	util.AddRouteWithPort(labelRouter, "/set/{label}", setLabel, "PUT", "POST")
	util.AddRouteWithPort(labelRouter, "/clear", setLabel, "POST")
	util.AddRouteWithPort(labelRouter, "", getLabel)
}

func setLabel(w http.ResponseWriter, r *http.Request) {
	msg := ""
	if label := listeners.SetListenerLabel(r); label == "" {
		msg := fmt.Sprintf("Port [%s] Label Cleared", util.GetRequestOrListenerPort(r))
		events.SendRequestEvent("Label Cleared", msg, r)
	} else {
		msg = fmt.Sprintf("Will use label %s for all responses on port %s", label, util.GetRequestOrListenerPort(r))
		events.SendRequestEvent("Label Set", msg, r)
	}
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func getLabel(w http.ResponseWriter, r *http.Request) {
	label := listeners.GetListenerLabel(r)
	msg := fmt.Sprintf("Port [%s] Label [%s]", util.GetRequestOrListenerPort(r), label)
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, "Server Label: "+label)
}
