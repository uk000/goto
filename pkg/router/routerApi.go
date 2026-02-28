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

package router

import (
	"fmt"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net/http"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("routing", setRoutes, nil)
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	routingRouter := util.PathRouter(r, "/routing")
	util.AddRoute(routingRouter, "/add", addRoute, "POST")
	util.AddRoute(routingRouter, "/clear", clearRoutes, "POST")
	util.AddRoute(routingRouter, "", getRoutes, "GET")
}

func addRoute(w http.ResponseWriter, r *http.Request) {
	route := &Route{}
	err := util.ReadJsonPayload(r, route)
	msg := ""
	if err != nil {
		msg = fmt.Sprintf("Failed to parse routing payload with error [%s]", err.Error())
	} else if !route.IsValid() {
		msg = fmt.Sprintf("Invalid route: [%+v]", route)
	} else {
		pr := GetPortRouter(route.From.Port)
		pr.AddRoute(route)
		msg = fmt.Sprintf("Route added [%s]", route.Label)
		fmt.Fprintln(w, util.ToJSONText(route))
	}
	util.AddLogMessage(msg, r)
}

func clearRoutes(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	pr := GetPortRouter(port)
	pr.Clear()
	msg := fmt.Sprintf("Routes cleared on port [%d]", port)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func getRoutes(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	pr := GetPortRouter(port)
	util.WriteJsonPayload(w, pr)
	msg := fmt.Sprintf("Routes reported on port [%d]", port)
	util.AddLogMessage(msg, r)
}
