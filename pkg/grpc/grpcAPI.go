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

package grpc

import (
	"fmt"
	"goto/pkg/util"
	"net/http"

	"github.com/gorilla/mux"
)

var (
	Handler = util.ServerHandler{Name: "grpc", SetRoutes: SetRoutes}
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	grpcRouter := util.PathRouter(r, "/grpc")
	util.AddRouteQWithPort(grpcRouter, "/protos/add/{name}", addProto, "path", "POST", "PUT")
	util.AddRouteWithPort(grpcRouter, "/protos/add/{name}", addProto, "POST", "PUT")
	util.AddRouteWithPort(grpcRouter, "/protos/{proto}/list/services", listServices, "GET")
	util.AddRouteWithPort(grpcRouter, "/protos/{proto}/list/{service}/methods", listMethods, "GET")
	util.AddRouteWithPort(grpcRouter, "/protos/clear", clearProtos, "POST", "PUT")
	util.AddRouteWithPort(grpcRouter, "/protos", getProtos, "GET")
}

func addProto(w http.ResponseWriter, r *http.Request) {
	msg := ""
	name := util.GetStringParamValue(r, "name")
	path := util.GetStringParamValue(r, "path")
	if name == "" {
		w.WriteHeader(http.StatusInternalServerError)
		msg = "No name"
	} else if err := grpcParser.AddProto(name, path, util.ReadBytes(r.Body)); err == nil {
		msg = fmt.Sprintf("Proto [%s] stored", name)
	} else {
		w.WriteHeader(http.StatusInternalServerError)
		msg = fmt.Sprintf("Failed to store proto [%s] with error: %s", name, err.Error())
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func listServices(w http.ResponseWriter, r *http.Request) {
	msg := ""
	proto := util.GetStringParamValue(r, "proto")
	if proto == "" {
		w.WriteHeader(http.StatusInternalServerError)
		msg = "No proto"
		fmt.Fprintln(w, msg)
	} else if list := grpcParser.GetService(proto); list != nil {
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
		w.WriteHeader(http.StatusInternalServerError)
		msg = "No proto"
		fmt.Fprintln(w, msg)
	} else if serviceMethods := grpcParser.ListMethods(proto, service); serviceMethods != nil {
		util.WriteJsonPayload(w, serviceMethods)
	}
	util.AddLogMessage(msg, r)
}

func clearProtos(w http.ResponseWriter, r *http.Request) {
	msg := "Protos cleared"
	grpcParser.ClearProtos()
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func getProtos(w http.ResponseWriter, r *http.Request) {
	util.WriteJsonPayload(w, grpcParser.fileSources)
	util.AddLogMessage("Protos reported", r)
}
