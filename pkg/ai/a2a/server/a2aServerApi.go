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

package a2aserver

import (
	"fmt"
	"goto/pkg/ai/a2a/model"
	"goto/pkg/ai/registry"
	"goto/pkg/constants"
	"goto/pkg/server/middleware"
	"goto/pkg/server/response/status"
	"goto/pkg/util"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
)

var (
	Middleware    = middleware.NewMiddleware("a2a", setRoutes, nil)
	statusManager = status.NewStatusManager()
)

func setRoutes(r *mux.Router) {
	a2a := middleware.RootPath("/a2a")
	agentsRouter := util.PathRouter(a2a, "/agents")
	util.AddRoute(agentsRouter, "", getAgents, "GET")
	util.AddRoute(agentsRouter, "/all", getAgents, "GET")
	util.AddRoute(agentsRouter, "/names", getAgents, "GET")
	util.AddRoute(agentsRouter, "/names/all", getAgents, "GET")
	util.AddRoute(agentsRouter, "/{agent}", getAgents, "GET")
	util.AddRoute(agentsRouter, "/{agent}/delegates", getAgentDelegates, "GET")
	util.AddRoute(agentsRouter, "/{agent}/delegates/tools", getAgentDelegates, "GET")
	util.AddRoute(agentsRouter, "/{agent}/delegates/tools/{delegate}", getAgentDelegates, "GET")
	util.AddRoute(agentsRouter, "/{agent}/delegates/agents", getAgentDelegates, "GET")
	util.AddRoute(agentsRouter, "/{agent}/delegates/agents/{delegate}", getAgentDelegates, "GET")
	util.AddRoute(agentsRouter, "/add", addAgents, "POST")
	util.AddRoute(agentsRouter, "/{agent}/payload", setAgentPayload, "POST")
	util.AddRoute(agentsRouter, "/clear", clearAgents, "POST")

	a2aServersRouter := util.PathRouter(a2a, "/servers")
	util.AddRoute(a2aServersRouter, "", getServers, "GET")
	util.AddRoute(a2aServersRouter, "/clear", clearServers, "POST")

	a2aStatusRouter := util.PathRouter(a2a, "/status")
	util.AddRouteQO(a2aStatusRouter, "/set/{status}", setStatus, "uri", "POST")
	util.AddRouteQO(a2aStatusRouter, "/set/{status}/header/{header}={value}", setStatus, "uri", "POST")
	util.AddRouteQO(a2aStatusRouter, "/set/{status}/header/{header}", setStatus, "uri", "POST")
	util.AddRouteQO(a2aStatusRouter, "/set/{status}/header/not/{header}", setStatus, "uri", "POST")

	util.AddRoute(a2aStatusRouter, "/configure", configureStatus, "POST")
	util.AddRoute(a2aStatusRouter, "/clear", clearStatus, "POST")
	util.AddRoute(a2aStatusRouter, "", getStatuses, "GET")

	agentRouter := middleware.RootPath("/agent")
	util.AddRoute(agentRouter, "/{agent}", serveAgent, "GET", "POST", "DELETE")
}

func getAgents(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	name := util.GetStringParamValue(r, "agent")
	all := strings.Contains(r.RequestURI, "all")
	names := strings.Contains(r.RequestURI, "names")
	yaml := strings.EqualFold(r.Header.Get("Accept"), "application/yaml")
	msg := ""
	if names {
		if all {
			util.WriteJsonOrYAMLPayload(w, GetAgentNames(0), yaml)
		} else {
			util.WriteJsonOrYAMLPayload(w, GetAgentNames(port), yaml)
		}
		msg = "Names sent for all agents and delegates"
	} else if all {
		util.WriteJsonOrYAMLPayload(w, PortServers, yaml)
		msg = "Details sent for all agents"
	} else if name != "" {
		agent := GetAgent(port, name)
		if agent == nil {
			w.WriteHeader(http.StatusBadRequest)
			msg = fmt.Sprintf("Bad agent [%s]", name)
		} else {
			util.WriteJsonOrYAMLPayload(w, agent, yaml)
			msg = fmt.Sprintf("Details sent for agent [%s]", name)
		}
	} else {
		util.WriteJsonOrYAMLPayload(w, registry.TheAgentRegistry.Agents, yaml)
		msg = "All agents sent"
	}
	util.AddLogMessage(msg, r)
}

func getAgentDelegates(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	name := util.GetStringParamValue(r, "agent")
	delegate := util.GetStringParamValue(r, "delegate")
	yaml := strings.EqualFold(r.Header.Get("Accept"), "application/yaml")
	msg := ""
	agent := GetAgent(port, name)
	if name == "" || agent == nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Bad agent [%s]", name)
	} else if agent.Config.Delegates == nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("No delegates for agent [%s]", name)
	} else if strings.Contains(r.RequestURI, "/tools") {
		if agent.Config.Delegates.Tools == nil {
			w.WriteHeader(http.StatusBadRequest)
			msg = fmt.Sprintf("No Tool delegates for agent [%s]", name)
		} else if delegate != "" {
			d := agent.Config.Delegates.Tools[delegate]
			if d == nil {
				w.WriteHeader(http.StatusBadRequest)
				msg = fmt.Sprintf("No Tool delegate [%s] for agent [%s]", delegate, name)
			} else {
				util.WriteJsonOrYAMLPayload(w, d, yaml)
				msg = fmt.Sprintf("Delegate Tool [%s] sent for agent [%s]", delegate, name)
			}
		} else {
			util.WriteJsonOrYAMLPayload(w, agent.Config.Delegates.Tools, yaml)
			msg = fmt.Sprintf("Delegate Tools sent for agent [%s]", name)
		}
	} else if strings.Contains(r.RequestURI, "/agents") {
		if agent.Config.Delegates.Agents == nil {
			w.WriteHeader(http.StatusBadRequest)
			msg = fmt.Sprintf("No Agent delegates for agent [%s]", name)
		} else if delegate != "" {
			d := agent.Config.Delegates.Agents[delegate]
			if d == nil {
				w.WriteHeader(http.StatusBadRequest)
				msg = fmt.Sprintf("No Agents delegate [%s] for agent [%s]", delegate, name)
			} else {
				util.WriteJsonOrYAMLPayload(w, d, yaml)
				msg = fmt.Sprintf("Delegate Agents [%s] sent for agent [%s]", delegate, name)
			}
		} else {
			util.WriteJsonOrYAMLPayload(w, agent.Config.Delegates.Agents, yaml)
			msg = fmt.Sprintf("Delegate Agents sent for agent [%s]", name)
		}
	} else {
		util.WriteJsonOrYAMLPayload(w, agent.Config.Delegates, yaml)
		msg = fmt.Sprintf("Delegates sent for agent [%s]", name)
	}
	util.AddLogMessage(msg, r)
}

func getServers(w http.ResponseWriter, r *http.Request) {
	yaml := strings.EqualFold(r.Header.Get("Accept"), "application/yaml")
	util.WriteJsonOrYAMLPayload(w, PortServers, yaml)
	util.AddLogMessage("All A2A servers reported", r)
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
			registry.TheAgentRegistry.AddAgent(agent, port)
			names = append(names, agent.Card.Name)
		}
		msg = fmt.Sprintf("Port [%d] Added Agents: %+v", port, names)
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

func clearAgents(w http.ResponseWriter, r *http.Request) {
	ClearAllServers()
	registry.TheAgentRegistry.Clear()
	msg := "Agents cleared on all ports and registry"
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func configureStatus(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	sc, err := statusManager.ParseStatusConfig(port, r.Body)
	msg := ""
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Failed to parse status config with error: %s", err.Error())
		fmt.Fprintln(w, msg)
	} else {
		msg = fmt.Sprintf("Parsed status config: %s", sc.Log("MCP", port))
		util.WriteJsonPayload(w, sc)
	}
	util.AddLogMessage(msg, r)
}

func setStatus(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	uri := util.GetStringParamValue(r, "uri")
	header := util.GetStringParamValue(r, "header")
	value := util.GetStringParamValue(r, "value")
	notPresent := strings.Contains(r.RequestURI, "not")
	statusCodes, times, ok := util.GetStatusParam(r)
	if !ok {
		util.AddLogMessage("Invalid status", r)
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintln(w, "Invalid Status")
		return
	}
	status := statusManager.SetStatusFor(port, uri, header, value, statusCodes, times, !notPresent)
	msg := status.Log("A2A", port)
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func clearStatus(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	statusManager.Clear(port)
	msg := fmt.Sprintf("Status cleared on port [%d]", port)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func getStatuses(w http.ResponseWriter, r *http.Request) {
	util.WriteJsonPayload(w, statusManager.PortStatus)
	util.AddLogMessage("Delivered statuses", r)
}

func sendStatus(id string, status, rem int, w http.ResponseWriter, r *http.Request) {
	w.Header().Add(constants.HeaderGotoForcedStatus, strconv.Itoa(status))
	w.Header().Add(constants.HeaderGotoForcedStatusRemaining, strconv.Itoa(rem))
	w.WriteHeader(status)
	b, _ := io.ReadAll(r.Body)
	msg := fmt.Sprintf("%s Reporting status [%d], Remaining status count [%d]. A2A Request Headers [%s], Payload: %s", id, status, rem, util.ToJSONText(r.Header), string(b))
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
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
		if status, rem := statusManager.GetStatusFor(port, r.RequestURI, r.Header); status > 0 {
			sendStatus(agent, status, rem, w, r)
		} else {
			err := GetOrAddServer(port).Serve(agent, w, r)
			if err != nil {
				msg = fmt.Sprintf("Failed to serve agent [%s] on port [%d]: %s", agent, port, err.Error())
				fmt.Fprintln(w, err.Error())
			} else {
				msg = fmt.Sprintf("Handled agent [%s] on port: %d", agent, port)
			}
		}
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
