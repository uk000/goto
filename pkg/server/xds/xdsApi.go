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

package xds

import (
	"fmt"
	grpcserver "goto/pkg/rpc/grpc/server"
	"goto/pkg/server/listeners"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net/http"

	"github.com/gorilla/mux"
	"google.golang.org/grpc"
)

var (
	Middleware = middleware.NewMiddleware("xds", setRoutes, nil)
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	xdsRouter := util.PathPrefix(r, "/?server?/xds")
	util.AddRouteWithPort(xdsRouter, "/start", startXDS, "POST")
	util.AddRouteWithPort(xdsRouter, "/add/{type:clusters|routes|listeners|secrets}", addXDSResources, "POST")
	util.AddRouteWithPort(xdsRouter, "/remove/{type:cluster|route|listener|secret}/{name}", removeXDSResource, "POST")
	util.AddRouteWithPort(xdsRouter, "/get/{type:clusters|routes|listeners|secrets}", getXDSResources, "GET")
}

func startXDS(w http.ResponseWriter, r *http.Request) {
	port := util.GetIntParamValue(r, "port")
	msg := ""
	status := http.StatusOK
	if port <= 0 || port > 65535 {
		status = http.StatusBadRequest
		msg = fmt.Sprintf("Invalid port [%d]", port)
	} else if l, err := listeners.AddGRPCListener(port, false); err == nil {
		grpcserver.StartWithCallback(l, func(gs *grpc.Server) {
			GetXDSServer(port).registerServer(gs)
		})
		msg = fmt.Sprintf("XDS Server started on port [%d]", port)
	} else {
		status = http.StatusInternalServerError
		msg = fmt.Sprintf("Failed to start XDS server on port [%d] with error: %s", port, err.Error())
	}
	w.WriteHeader(status)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func addXDSResources(w http.ResponseWriter, r *http.Request) {
	port := util.GetIntParamValue(r, "port")
	t := util.GetStringParamValue(r, "type")
	msg := ""
	x := GetXDSServer(port)

	if count, err := x.Store.LoadResourcesFromBody(t, r.Body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Failed to parse resource of type [%s] with error: %s", t, err.Error())
	} else {
		msg = fmt.Sprintf("Added [%d] resources of type [%s] to XDS Server on port [%d]", count, t, port)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func removeXDSResource(w http.ResponseWriter, r *http.Request) {
	port := util.GetIntParamValue(r, "port")
	t := util.GetStringParamValue(r, "type")
	name := util.GetStringParamValue(r, "name")
	msg := ""
	x := GetXDSServer(port)

	if x.Store.RemoveResources(t, name) {
		msg = fmt.Sprintf("Removed resource of type [%s] name [%s] from XDS Server on port [%d]", t, name, port)
	} else {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Resource Type [%s] or Name [%s] not found", t, name)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func getXDSResources(w http.ResponseWriter, r *http.Request) {
	port := util.GetIntParamValue(r, "port")
	t := util.GetStringParamValue(r, "type")
	msg := ""
	x := GetXDSServer(port)
	util.WriteJsonPayload(w, x.Store.GetResources(t))
	msg = fmt.Sprintf("Sent resources of type [%s] from XDS Server on port [%d]", t, port)
	util.AddLogMessage(msg, r)
}
