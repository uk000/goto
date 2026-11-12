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

// setRoutes registers the /a2a admin routes and the /agent/{agent} route that serves agent calls.
func setRoutes(r *mux.Router) {
	a2a := middleware.RootPath("/a2a")
	agentsRouter := util.PathRouter(a2a, "/agents")
	util.AddRoute(agentsRouter, "", getRegistryAgents, "GET")
	util.AddRoute(agentsRouter, "/all", getAllServerAgents, "GET")
	util.AddRoute(agentsRouter, "/names", getAgentNames, "GET")
	util.AddRoute(agentsRouter, "/names/all", getAllAgentNames, "GET")
	util.AddRoute(agentsRouter, "/add", addAgents, "POST")
	util.AddRoute(agentsRouter, "/clear", clearAgents, "POST")

	agentRouter := util.PathRouter(agentsRouter, "/{agent}")
	util.AddRoute(agentRouter, "", getAgent, "GET")
	util.AddRoute(agentRouter, "/delegates", getAgentDelegates(delegatesAll), "GET")
	util.AddRoute(agentRouter, "/delegates/tools", getAgentDelegates(delegatesTools), "GET")
	util.AddRoute(agentRouter, "/delegates/tools/{delegate}", getAgentDelegates(delegatesTools), "GET")
	util.AddRoute(agentRouter, "/delegates/agents", getAgentDelegates(delegatesAgents), "GET")
	util.AddRoute(agentRouter, "/delegates/agents/{delegate}", getAgentDelegates(delegatesAgents), "GET")
	util.AddRoute(agentRouter, "/payload", setAgentPayload, "POST")

	a2aServersRouter := util.PathRouter(a2a, "/servers")
	util.AddRoute(a2aServersRouter, "", getServers, "GET")
	util.AddRoute(a2aServersRouter, "/clear", clearServers, "POST")

	a2aStatusRouter := util.PathRouter(agentRouter, "/status")
	util.AddRouteQO(a2aStatusRouter, "/set/{status}", setStatus, "methods", "POST")
	util.AddRouteQO(a2aStatusRouter, "/set/{status}/header/{header}={value}", setStatus, "methods", "POST")
	util.AddRouteQO(a2aStatusRouter, "/set/{status}/header/{header}", setStatus, "methods", "POST")
	util.AddRouteQO(a2aStatusRouter, "/set/{status}/header/not/{header}", setStatusWithoutHeader, "methods", "POST")

	util.AddRoute(a2aStatusRouter, "/configure", configureStatus, "POST")
	util.AddRoute(a2aStatusRouter, "/clear", clearStatus, "POST")
	util.AddRoute(a2aStatusRouter, "", getStatuses, "GET")

	agentCallRouter := middleware.RootPath("/agent")
	util.AddRoute(agentCallRouter, "/{agent}", serveAgent, "GET", "POST", "DELETE")
}

// getRegistryAgents reports the agents of the registry, which has the agents by name and port.
func getRegistryAgents(w http.ResponseWriter, r *http.Request) {
	util.WriteJsonOrYAMLPayload(w, registry.TheAgentRegistry.Agents, util.IsAcceptYAML(r))
	util.AddLogMessage("All agents sent", r)
}

// getAllServerAgents reports the servers of all ports, with their agents.
func getAllServerAgents(w http.ResponseWriter, r *http.Request) {
	util.WriteJsonOrYAMLPayload(w, snapshotServers(), util.IsAcceptYAML(r))
	util.AddLogMessage("Details sent for all agents", r)
}

// getAgentNames reports the names of the agents and their delegates on the port of the request.
func getAgentNames(w http.ResponseWriter, r *http.Request) {
	util.WriteJsonOrYAMLPayload(w, GetAgentNames(util.GetRequestOrListenerPortNum(r)), util.IsAcceptYAML(r))
	util.AddLogMessage("Names sent for all agents and delegates", r)
}

// getAllAgentNames reports the names of the agents and their delegates on all ports.
func getAllAgentNames(w http.ResponseWriter, r *http.Request) {
	util.WriteJsonOrYAMLPayload(w, GetAgentNames(0), util.IsAcceptYAML(r))
	util.AddLogMessage("Names sent for all agents and delegates", r)
}

// getAgent reports the named agent of the port, with a 400 if there is no such agent.
func getAgent(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	name := util.GetStringParamValue(r, "agent")
	msg := ""
	agent := GetAgent(port, name)
	if agent == nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Bad agent [%s]", name)
		fmt.Fprintln(w, msg)
	} else {
		util.WriteJsonOrYAMLPayload(w, agent, util.IsAcceptYAML(r))
		msg = fmt.Sprintf("Details sent for agent [%s]", name)
	}
	util.AddLogMessage(msg, r)
}

// delegatesKind tells which delegates of an agent are reported.
type delegatesKind int

const (
	delegatesAll delegatesKind = iota
	delegatesTools
	delegatesAgents
)

// getAgentDelegates returns a handler that reports an agent's delegates, all, by kind, or one by name.
func getAgentDelegates(kind delegatesKind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		port := util.GetRequestOrListenerPortNum(r)
		name := util.GetStringParamValue(r, "agent")
		delegate := util.GetStringParamValue(r, "delegate")
		yaml := util.IsAcceptYAML(r)
		msg := ""
		agent := GetAgent(port, name)
		if name == "" || agent == nil {
			w.WriteHeader(http.StatusBadRequest)
			msg = fmt.Sprintf("Bad agent [%s]", name)
		} else if agent.Config == nil || agent.Config.Delegates == nil {
			w.WriteHeader(http.StatusBadRequest)
			msg = fmt.Sprintf("No delegates for agent [%s]", name)
		} else if kind == delegatesTools {
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
		} else if kind == delegatesAgents {
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
		if w.Header().Get(constants.HeaderContentType) == "" {
			fmt.Fprintln(w, msg)
		}
		util.AddLogMessage(msg, r)
	}
}

// getServers reports the A2A servers of all ports.
func getServers(w http.ResponseWriter, r *http.Request) {
	util.WriteJsonOrYAMLPayload(w, snapshotServers(), util.IsAcceptYAML(r))
	util.AddLogMessage("All A2A servers reported", r)
}

// addAgents adds the agents in the request body to the port's server and the registry.
func addAgents(w http.ResponseWriter, r *http.Request) {
	msg := ""
	port := util.GetRequestOrListenerPortNum(r)
	agents := []*model.Agent{}
	err := util.ReadJsonOrYamlPayloadFromBody(r.Body, &agents)
	if err != nil || len(agents) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		if err != nil {
			msg = fmt.Sprintf("Failed to parse payload with error [%s]", err.Error())
		} else {
			msg = "Failed to add agents, no agent cards in the payload"
		}
	} else {
		names := []string{}
		failures := []string{}
		server := GetOrAddServer(port)
		for i, agent := range agents {
			if agent == nil || agent.Card == nil {
				failures = append(failures, fmt.Sprintf("#%d: agent has no card", i+1))
				continue
			}
			if err := server.AddAgent(agent); err != nil {
				failures = append(failures, fmt.Sprintf("%s: %s", agent.Card.Name, err.Error()))
				continue
			}
			registry.TheAgentRegistry.AddAgent(agent, port)
			names = append(names, agent.Card.Name)
		}
		msg = fmt.Sprintf("Port [%d] Added Agents: %+v", port, names)
		if len(failures) > 0 {
			w.WriteHeader(http.StatusBadRequest)
			msg = fmt.Sprintf("%s. Failed Agents: %+v", msg, failures)
		}
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

// setAgentPayload sets the payload that the named agent responds or streams with, from the request body.
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
				w.WriteHeader(http.StatusBadRequest)
				msg = fmt.Sprintf("Failed to set payload for agent [%s] with error: %s", name, err.Error())
			} else {
				msg = fmt.Sprintf("Payload set successfully for agent [%s]", name)
			}
		}
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

// clearServers clears the server of the port of the request.
func clearServers(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	ClearServer(port)
	msg := fmt.Sprintf("Server cleared on port: %d", port)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

// clearAgents clears the servers of all ports and the agent registry.
func clearAgents(w http.ResponseWriter, r *http.Request) {
	ClearAllServers()
	registry.TheAgentRegistry.Clear()
	msg := "Agents cleared on all ports and registry"
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

// configureStatus parses a status config from the request body and applies it for the port.
func configureStatus(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	sc, err := statusManager.ParseAndApplyStatusConfig(port, r.Body)
	msg := ""
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Failed to parse status config with error: %s", err.Error())
		fmt.Fprintln(w, msg)
	} else {
		msg = fmt.Sprintf("Parsed status config: %s", sc.Log("A2A", port))
		util.WriteJsonPayload(w, sc)
	}
	util.AddLogMessage(msg, r)
}

// setStatus sets a forced status for an agent, for the requests that have the header if the route gives one.
func setStatus(w http.ResponseWriter, r *http.Request) {
	setAgentStatus(w, r, true)
}

// setStatusWithoutHeader sets a status for the requests that don't have the header.
func setStatusWithoutHeader(w http.ResponseWriter, r *http.Request) {
	setAgentStatus(w, r, false)
}

// setAgentStatus sets a forced status for the agent's URI, by header and methods (POST by default).
func setAgentStatus(w http.ResponseWriter, r *http.Request, headerPresent bool) {
	port := util.GetRequestOrListenerPortNum(r)
	name := util.GetStringParamValue(r, "agent")
	header := util.GetStringParamValue(r, "header")
	value := util.GetStringParamValue(r, "value")
	statusCodes, times, ok := util.GetStatusParam(r)
	if !ok {
		util.AddLogMessage("Invalid status", r)
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintln(w, "Invalid Status")
		return
	}
	agent := GetAgent(port, name)
	if agent == nil {
		w.WriteHeader(http.StatusBadRequest)
		msg := fmt.Sprintf("Bad agent [%s]", name)
		util.AddLogMessage(msg, r)
		fmt.Fprintln(w, msg)
		return
	}
	// Agents are called with POST, so that is what a status is for unless the methods are given (methods=GET,POST, or
	// methods=* for all of them): the requests that fetch the card of an agent, or that stream from it, are not given
	// the status otherwise.
	methods, ok := status.ParseMethods(util.GetStringParamValue(r, "methods"), http.MethodPost)
	if !ok {
		msg := fmt.Sprintf("Invalid methods [%s]", util.GetStringParamValue(r, "methods"))
		util.AddLogMessage(msg, r)
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintln(w, msg)
		return
	}
	sc := statusManager.SetStatusFor(port, agent.URI, agent.URI, header, value, methods, statusCodes, times, headerPresent)
	msg := sc.Log("A2A", port)
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

// clearStatus clears the statuses of the port of the request.
func clearStatus(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	statusManager.Clear(port, "", "")
	msg := fmt.Sprintf("Status cleared on port [%d]", port)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

// getStatuses reports the statuses that are set.
func getStatuses(w http.ResponseWriter, r *http.Request) {
	util.WriteJsonPayload(w, statusManager.Snapshot())
	util.AddLogMessage("Delivered statuses", r)
}

// sendStatus responds with the forced status, and with headers for the status and the count of it that remains.
func sendStatus(id string, status, rem int, w http.ResponseWriter, r *http.Request) {
	w.Header().Add(constants.HeaderGotoForcedStatus, strconv.Itoa(status))
	w.Header().Add(constants.HeaderGotoForcedStatusRemaining, strconv.Itoa(rem))
	w.WriteHeader(status)
	rs := util.GetRequestStore(r)
	rs.StatusCode = status
	b, _ := io.ReadAll(r.Body)
	msg := fmt.Sprintf("%s Reporting status [%d], Remaining status count [%d]. A2A Request Headers [%s], Payload: %s", id, status, rem, util.ToJSONText(r.Header), string(b))
	util.AddLogMessage(msg, r)
}

// serveAgent serves a call to an agent of the port, or responds with its forced status if it has one.
func serveAgent(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	name := util.GetStringParamValue(r, "agent")
	if name == "" {
		name = getAgentNameFromURI(r.RequestURI)
	}
	msg := ""
	if name == "" {
		w.WriteHeader(http.StatusBadRequest)
		msg = "Agent name needed"
		fmt.Fprintln(w, msg)
	} else {
		server := GetOrAddServer(port)
		agent := server.GetAgent(name)
		if agent == nil {
			w.WriteHeader(http.StatusNotFound)
			msg = fmt.Sprintf("Failed to serve agent [%s] on port [%d]: %s", name, port, errAgentNotFound(name, server).Error())
			fmt.Fprintln(w, msg)
		} else {
			// A forced status is counted once per request, here, and a request that gets one is not served by the agent.
			status, rem := forcedStatusFor(port, agent, r)
			if status > 0 && status != http.StatusOK {
				sendStatus(name, status, rem, w, r)
			} else {
				server.serve(agent, status, rem, w, r)
			}
			msg = fmt.Sprintf("Handled agent [%s] on port: %d", name, port)
		}
	}
	util.AddLogMessage(msg, r)
}

// statusURI is the URI that the statuses are matched with for a request to the agent. It is the path of the request,
// without the query. The endpoint of the agent is always the URI of the agent, with or without the trailing slash.
func statusURI(agent *model.Agent, r *http.Request) string {
	path := r.URL.Path
	if strings.TrimSuffix(path, "/") == strings.TrimSuffix(agent.URI, "/") {
		return agent.URI
	}
	return path
}

// forcedStatusFor finds the status that is forced for the request to the agent, and counts the use of it. A status is
// found by the path of the request, and then, for the requests that are not POST and are for a path under the agent,
// such as the one that fetches its card, by the URI of the agent, which is what a status that is set for the agent
// with methods=GET is for. A status is used up by one of them at most.
func forcedStatusFor(port int, agent *model.Agent, r *http.Request) (status, rem int) {
	uri := statusURI(agent, r)
	status, rem = statusManager.GetStatusFor(port, uri, r.Header, r.Method)
	if status == 0 && r.Method != http.MethodPost && uri != agent.URI && strings.HasPrefix(uri, strings.TrimSuffix(agent.URI, "/")+"/") {
		status, rem = statusManager.GetStatusFor(port, agent.URI, r.Header, r.Method)
	}
	return status, rem
}

// errAgentNotFound makes the error for an agent that the server doesn't have.
func errAgentNotFound(name string, s *A2AServer) error {
	return fmt.Errorf("agent [%s] not found on server [%s] port [%d]", name, s.ID, s.Port)
}

// AgentsHandler returns the HTTP handler that serves the calls to agents.
func AgentsHandler() http.Handler {
	return http.HandlerFunc(serveAgent)
}

// getAgentNameFromURI finds the name of the agent in the URI of a request to /agent/{agent}, which can have more
// path, and a query.
func getAgentNameFromURI(uri string) string {
	if i := strings.IndexAny(uri, "?#"); i >= 0 {
		uri = uri[:i]
	}
	parts := strings.Split(uri, "/agent/")
	if len(parts) >= 2 {
		parts2 := strings.Split(parts[1], "/")
		return parts2[0]
	}
	return ""
}
