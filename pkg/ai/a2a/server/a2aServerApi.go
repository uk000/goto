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

package a2aserver

import (
	"fmt"
	"goto/pkg/ai/a2a/model"
	"goto/pkg/ai/registry"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("a2a", setRoutes, nil)
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	a2aRouter := util.PathRouter(r, "/a2a")
	util.AddRouteWithPort(a2aRouter, "/agents", getAgents, "GET")
	util.AddRouteWithPort(a2aRouter, "/servers", getServers, "GET")
	util.AddRouteWithPort(a2aRouter, "/agents/add", addAgents, "POST")
	util.AddRouteWithPort(a2aRouter, "/agent/{agent}/payload", setAgentPayload, "POST")
	util.AddRouteWithPort(a2aRouter, "/clear", clearServers, "POST")

	agentRouter := util.PathRouter(r, "/agent")
	util.AddRouteWithPort(agentRouter, "/{agent}", serveAgent, "GET", "POST", "DELETE")
}

func getAgents(w http.ResponseWriter, r *http.Request) {
	util.WriteJsonPayload(w, registry.TheAgentRegistry.Agents)
}

func getServers(w http.ResponseWriter, r *http.Request) {
	text := util.ToJSONText(PortServers)
	log.Println(text)
	util.WriteJsonPayload(w, PortServers)
}

func addAgents(w http.ResponseWriter, r *http.Request) {
	msg := ""
	port := util.GetRequestOrListenerPortNum(r)
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
		names := []string{}
		server := GetOrAddServer(port)
		for _, agent := range agents {
			server.AddAgent(agent)
			registry.TheAgentRegistry.AddAgent(agent)
			names = append(names, agent.Card.Name)
		}
		msg = fmt.Sprintf("Added Agents: %+v", names)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func setAgentPayload(w http.ResponseWriter, r *http.Request) {
	msg := ""
	port := util.GetRequestOrListenerPortNum(r)
	name := util.GetStringParamValue(r, "agent")
	payload, _ := io.ReadAll(r.Body)
	if name == "" {
		w.WriteHeader(http.StatusBadRequest)
		msg = "No agent name given"
	} else {
		agent := GetAgent(port, name)
		if agent == nil {
			w.WriteHeader(http.StatusBadRequest)
			msg = fmt.Sprintf("Bad agent [%s]", name)
		} else {
			if err := agent.SetPayload(payload); err != nil {
				msg = fmt.Sprintf("Failed to set payload for agent [%s] with error: %s", name, err.Error())
			} else {
				msg = fmt.Sprintf("Payload set successfully for agent [%s]", name)
			}
		}
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func clearServers(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	ClearServer(port)
	msg := fmt.Sprintf("Server cleared on port: %d", port)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func serveAgent(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	agent := util.GetStringParamValue(r, "agent")
	if agent == "" {
		agent = getAgentNameFromURI(r.RequestURI)
	}
	msg := ""
	if agent == "" {
		w.WriteHeader(http.StatusBadRequest)
		msg = "Agent name needed"
		fmt.Fprintln(w, msg)
	} else {
		GetOrAddServer(port).Serve(agent, w, r)
		msg = fmt.Sprintf("Handled agent [%s] on port: %d", agent, port)
	}
	util.AddLogMessage(msg, r)
}

func AgentsHandler() http.Handler {
	return http.HandlerFunc(serveAgent)
}

func getAgentNameFromURI(uri string) string {
	parts := strings.Split(uri, "/agent/")
	if len(parts) >= 2 {
		parts2 := strings.Split(parts[1], "/")
		return parts2[0]
	}
	return ""
}
