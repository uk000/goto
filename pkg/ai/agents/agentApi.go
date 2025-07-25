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

package agents

import (
	"fmt"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net/http"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("rpc", setRoutes, nil)
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	agentRouter := util.PathRouter(r, "/agents")
	util.AddRouteWithPort(agentRouter, "", getAgents, "GET")
	util.AddRouteQWithPort(agentRouter, "/add/{agent}", addAgent, "fromRPC", "POST")
	util.AddRouteWithPort(agentRouter, "/add/{agent}", addAgent, "POST")
}

func getAgents(w http.ResponseWriter, r *http.Request) {
}

func addAgent(w http.ResponseWriter, r *http.Request) {
	msg := ""
	name := util.GetStringParamValue(r, "service")
	fromGRPC := util.GetBoolParamValue(r, "fromRPC")
	var err error
	if name == "" {
		w.WriteHeader(http.StatusBadRequest)
		msg = "No name"
	} else if fromGRPC {
		// service, err = reg.FromGRPCService(name)
	} else {
		// service, err = reg.NewJSONRPCService(r.Body)
	}
	if err == nil {
		w.WriteHeader(http.StatusOK)
		// msg = fmt.Sprintf("Registered JSONRPCService: %s with %d methods", service.Name, len(service.Methods))
	} else {
		w.WriteHeader(http.StatusInternalServerError)
		msg = fmt.Sprintf("Failed to register service [%s] with error: %s", name, err.Error())
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}
