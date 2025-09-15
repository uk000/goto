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

package registry

import (
	"fmt"
	"goto/pkg/ai/a2a/model"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net/http"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("airegistry", setRoutes, nil)
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	agentRouter := util.PathRouter(r, "/ai/agents")
	util.AddRoute(agentRouter, "", getAgents, "GET")
	util.AddRoute(agentRouter, "/add", addAgents, "POST")
	util.AddRoute(agentRouter, "/clear", clearAgents, "POST")
}

func getAgents(w http.ResponseWriter, r *http.Request) {
}

func addAgents(w http.ResponseWriter, r *http.Request) {
	msg := ""
	agents := []*model.Agent{}
	err := util.ReadJsonPayload(r, &agents)
	if err != nil || len(agents) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		if err != nil {
			msg = fmt.Sprintf("Failed to parse payload with error [%s]", err.Error())
		} else {
			msg = "Failed to add agents, no agent cards in the payload"
		}
	} else {
		TheAgentRegistry.AddAgents(agents)
		names := []string{}
		for _, a := range agents {
			names = append(names, a.Card.Name)
		}
		msg = fmt.Sprintf("Added Agents: %+v", names)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func clearAgents(w http.ResponseWriter, r *http.Request) {
	count := len(TheAgentRegistry.Agents)
	TheAgentRegistry.init()
	msg := fmt.Sprintf("%d Agents removed from registry", count)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}
