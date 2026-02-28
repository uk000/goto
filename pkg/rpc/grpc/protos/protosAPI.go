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

package protos

import (
	"fmt"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("protos", setRoutes, nil)
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	protosRouter := util.PathRouter(r, "/grpc/protos")
	util.AddRouteQ(protosRouter, "/store/{name}", addProto, "path", "POST", "PUT")
	util.AddRoute(protosRouter, "/store/{name}", addProto, "POST", "PUT")
	util.AddRouteQ(protosRouter, "/add/{name}", addProto, "path", "POST", "PUT")
	util.AddRoute(protosRouter, "/add/{name}", addProto, "POST", "PUT")
	util.AddRoute(protosRouter, "/remove/{name}", removeProto, "POST", "PUT")
	util.AddRoute(protosRouter, "/clear", clearProtos, "POST")
	util.AddRoute(protosRouter, "/{proto}/list/services", listServices, "GET")
	util.AddRoute(protosRouter, "/{proto}/list/{service}/methods", listMethods, "GET")
	util.AddRoute(protosRouter, "", getProtos, "GET")
}

func addProto(w http.ResponseWriter, r *http.Request) {
	msg := ""
	name := util.GetStringParamValue(r, "name")
	path := util.GetStringParamValue(r, "path")
	uploadOnly := strings.Contains(r.RequestURI, "store")
	if name == "" {
		w.WriteHeader(http.StatusBadRequest)
		msg = "No name"
	} else if err := ProtosRegistry.AddProto(name, path, util.ReadBytes(r.Body), uploadOnly); err == nil {
		msg = fmt.Sprintf("Proto [%s] stored", name)
	} else {
		w.WriteHeader(http.StatusInternalServerError)
		msg = fmt.Sprintf("Failed to store proto [%s] with error: %s", name, err.Error())
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func removeProto(w http.ResponseWriter, r *http.Request) {
	msg := ""
	name := util.GetStringParamValue(r, "name")
	if name == "" {
		w.WriteHeader(http.StatusBadRequest)
		msg = "No name"
	} else {
		ProtosRegistry.RemoveProto(name)
		msg = fmt.Sprintf("Proto [%s] removed", name)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func listServices(w http.ResponseWriter, r *http.Request) {
	msg := ""
	proto := util.GetStringParamValue(r, "proto")
	if proto == "" {
		w.WriteHeader(http.StatusBadRequest)
		msg = "No proto"
		fmt.Fprintln(w, msg)
	} else if list := ProtosRegistry.GetService(proto); list != nil {
		util.WriteJsonPayload(w, list)
	} else {
		msg = fmt.Sprintf("No services in proto [%s]", proto)
		fmt.Fprintln(w, msg)
	}
	util.AddLogMessage(msg, r)
}

func listMethods(w http.ResponseWriter, r *http.Request) {
	msg := ""
	proto := util.GetStringParamValue(r, "proto")
	service := util.GetStringParamValue(r, "service")
	if proto == "" {
		w.WriteHeader(http.StatusBadRequest)
		msg = "No proto"
		fmt.Fprintln(w, msg)
	} else if serviceMethods := ProtosRegistry.ListMethods(service); serviceMethods != nil {
		util.WriteJsonPayload(w, serviceMethods)
	}
	util.AddLogMessage(msg, r)
}

func clearProtos(w http.ResponseWriter, r *http.Request) {
	msg := "Protos cleared"
	ProtosRegistry.ClearProtos()
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func getProtos(w http.ResponseWriter, r *http.Request) {
	util.WriteJsonPayload(w, ProtosRegistry.servicesByProto)
	util.AddLogMessage("Protos reported", r)
}
