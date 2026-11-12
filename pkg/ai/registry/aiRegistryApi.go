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

package registry

import (
	"fmt"
	"goto/pkg/ai/a2a/model"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("airegistry", setRoutes, nil)
)

// isAgentsRequest tells if the request is for the API of the agents: the other requests under /ai have no API to serve
// them, and are handled as any other request.
func isAgentsRequest(r *http.Request) bool {
	uri := r.RequestURI
	if i := strings.IndexAny(uri, "?#"); i >= 0 {
		uri = uri[:i]
	}
	return uri == "/ai/agents" || strings.HasPrefix(uri, "/ai/agents/")
}

// setRoutes registers the GET /agents, POST /agents/add and POST /agents/clear routes of the agent registry API.
func setRoutes(r *mux.Router) {
	// The server hands a request to the router of the first segment of its path, which is /ai: a router of a root path
	// of /ai/agents is never given a request, so the routes are under the /agents of the router of /ai, which is given
	// only the requests for them.
	agentRouter := util.PathRouter(middleware.RootPathIf("/ai", isAgentsRequest), "/agents")
	util.AddRoute(agentRouter, "", getAgents, "GET")
	util.AddRoute(agentRouter, "/add", addAgents, "POST")
	util.AddRoute(agentRouter, "/clear", clearAgents, "POST")
}

// getAgents reports the registered agents, by name and port, as JSON or YAML.
func getAgents(w http.ResponseWriter, r *http.Request) {
	agents := TheAgentRegistry.snapshot()
	util.WriteJsonOrYAMLPayload(w, agents, util.IsAcceptYAML(r))
	util.AddLogMessage(fmt.Sprintf("Reported [%d] agents from registry", len(agents)), r)
}

// addAgents registers the agents in the request payload on the request's port, rejecting payloads with unnamed agent cards.
func addAgents(w http.ResponseWriter, r *http.Request) {
	msg := ""
	agents := []*model.Agent{}
	err := util.ReadJsonPayload(r, &agents)
	invalid := -1
	for i, a := range agents {
		if !isRegistrable(a) {
			invalid = i
			break
		}
	}
	if err != nil || len(agents) == 0 || invalid >= 0 {
		w.WriteHeader(http.StatusBadRequest)
		if err != nil {
			msg = fmt.Sprintf("Failed to parse payload with error [%s]", err.Error())
		} else if len(agents) == 0 {
			msg = "Failed to add agents, no agent cards in the payload"
		} else {
			msg = fmt.Sprintf("Failed to add agents, agent #%d in the payload has no card with a name", invalid+1)
		}
	} else {
		port := util.GetRequestOrListenerPortNum(r)
		TheAgentRegistry.AddAgents(agents, port)
		names := []string{}
		for _, a := range agents {
			names = append(names, a.Card.Name)
		}
		msg = fmt.Sprintf("Added Agents: %+v", names)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

// clearAgents removes all agents from the registry and reports how many names were removed.
func clearAgents(w http.ResponseWriter, r *http.Request) {
	count := TheAgentRegistry.clearAll()
	msg := fmt.Sprintf("%d Agents removed from registry", count)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}
