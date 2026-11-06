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

package router

import (
	"fmt"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("routing", setRoutes, nil)
)

// setRoutes registers the /routing routes to add, clear and list routes.
func setRoutes(r *mux.Router) {
	routingRouter := middleware.RootPath("/routing")
	util.AddRoute(routingRouter, "/add", addRoute, "POST")
	util.AddRoute(routingRouter, "/clear/all", clearRoutes, "POST")
	util.AddRoute(routingRouter, "/clear", clearRoutes, "POST")
	util.AddRoute(routingRouter, "/all", getRoutes, "GET")
	util.AddRoute(routingRouter, "", getRoutes, "GET")
}

// addRoute parses a route from the payload and, if valid, adds it to the router of its source port.
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

// clearRoutes clears the routes of the request's port, or of all ports for the "all" URI.
func clearRoutes(w http.ResponseWriter, r *http.Request) {
	all := strings.Contains(r.RequestURI, "all")
	port := util.GetRequestOrListenerPortNum(r)
	if all {
		Init()
	} else {
		pr := GetPortRouter(port)
		pr.Clear()
	}
	msg := fmt.Sprintf("Routes cleared on port [%d]", port)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

// getRoutes reports the routes and traffic of the request's port, or of all ports for the "all" URI.
func getRoutes(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	all := strings.Contains(r.RequestURI, "all")
	var data any
	msg := ""
	if all {
		data = PortRouters
		msg = "All Routes reported"
	} else {
		data = GetPortRouter(port)
		msg = fmt.Sprintf("Routes reported for port [%d]", port)
	}
	util.WriteJsonPayload(w, data)
	util.AddLogMessage(msg, r)
}
