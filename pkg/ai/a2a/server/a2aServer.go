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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"goto/pkg/ai/a2a/model"
	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/server/intercept"
	"goto/pkg/types"
	"goto/pkg/util"
	"net/http"
	"net/url"
	"sort"
	"sync"

	trpcserver "trpc.group/trpc-go/trpc-a2a-go/server"
	trpctask "trpc.group/trpc-go/trpc-a2a-go/taskmanager"
)

type A2AServer struct {
	ID      string
	Port    int
	Agents  map[string]*model.Agent `json:"agents"`
	Enabled bool                    `json:"enabled"`
	lock    sync.RWMutex
}

var (
	PortServers = map[int]*A2AServer{}
	lock        sync.RWMutex
)

// GetOrAddServer returns the server of the port, creating and registering one if the port has none.
func GetOrAddServer(port int) *A2AServer {
	lock.Lock()
	defer lock.Unlock()
	s := PortServers[port]
	if s == nil {
		s = newA2AServer(port)
		PortServers[port] = s
	}
	return s
}

// GetAgent returns the named agent of the port's server (creating the server if needed), or nil if there is none.
func GetAgent(port int, name string) *model.Agent {
	s := GetOrAddServer(port)
	return s.GetAgent(name)
}

// GetAgentNames lists the delegates of the agents, by port and agent. It lists the ports of the given one, or all of
// them if the port is not positive. A delegate is listed by the name of its tool, agent or URL, unless it's disabled.
func GetAgentNames(port int) map[int]map[string]map[string][]string {
	names := map[int]map[string]map[string][]string{}
	buildNames := func(port int, s *A2AServer) {
		names[port] = map[string]map[string][]string{}
		for name, a := range s.agentsSnapshot() {
			delegateNames := map[string][]string{}
			names[port][name] = delegateNames
			if a.Config == nil || a.Config.Delegates == nil {
				continue
			}
			d := a.Config.Delegates
			if d.Agents != nil {
				delegateNames["agents"] = []string{}
				for _, c := range d.Agents {
					if c != nil && !c.Disabled && c.AgentCall != nil {
						delegateNames["agents"] = append(delegateNames["agents"], c.AgentCall.Name)
					}
				}
			}
			if d.Tools != nil {
				delegateNames["tools"] = []string{}
				for _, c := range d.Tools {
					if c != nil && !c.Disabled && c.ToolCall != nil {
						delegateNames["tools"] = append(delegateNames["tools"], c.ToolCall.Tool)
					}
				}
			}
			if d.HTTP != nil {
				delegateNames["http"] = []string{}
				for _, c := range d.HTTP {
					if c != nil && !c.Disabled && c.HTTPCall != nil {
						delegateNames["http"] = append(delegateNames["http"], c.HTTPCall.URL)
					}
				}
			}
			for _, list := range delegateNames {
				sort.Strings(list)
			}
		}
	}
	if port > 0 {
		buildNames(port, GetOrAddServer(port))
	} else {
		for port, s := range snapshotServers() {
			buildNames(port, s)
		}
	}
	return names
}

// snapshotServers returns the servers of all the ports. The map is the caller's to read, the servers are not.
func snapshotServers() map[int]*A2AServer {
	lock.RLock()
	defer lock.RUnlock()
	servers := make(map[int]*A2AServer, len(PortServers))
	for port, s := range PortServers {
		servers[port] = s
	}
	return servers
}

// agentsSnapshot returns the agents of the server. The map is the caller's to read, the agents are not.
func (a *A2AServer) agentsSnapshot() map[string]*model.Agent {
	a.lock.RLock()
	defer a.lock.RUnlock()
	agents := make(map[string]*model.Agent, len(a.Agents))
	for name, agent := range a.Agents {
		agents[name] = agent
	}
	return agents
}

// MarshalJSON reports the server with the agents that it has at the moment, which change while it is reported.
func (a *A2AServer) MarshalJSON() ([]byte, error) {
	a.lock.RLock()
	defer a.lock.RUnlock()
	return json.Marshal(&struct {
		ID      string
		Port    int
		Agents  map[string]*model.Agent `json:"agents"`
		Enabled bool                    `json:"enabled"`
	}{a.ID, a.Port, a.Agents, a.Enabled})
}

// RemoveAgent deletes the named agent from the server of the port.
func RemoveAgent(port int, name string) {
	server := GetOrAddServer(port)
	server.RemoveAgent(name)
}

// ClearServer replaces the server of the port with a fresh, empty one.
func ClearServer(port int) {
	lock.Lock()
	defer lock.Unlock()
	PortServers[port] = newA2AServer(port)
}

// ClearAllServers drops the servers of all ports.
func ClearAllServers() {
	lock.Lock()
	defer lock.Unlock()
	PortServers = map[int]*A2AServer{}
}

// newA2AServer creates an enabled, empty server for the port, labelled with the port's listener label.
func newA2AServer(port int) *A2AServer {
	return &A2AServer{
		ID:      global.Funcs.GetListenerLabelForPort(port),
		Port:    port,
		Agents:  map[string]*model.Agent{},
		Enabled: true,
	}
}

// Enable marks the server as enabled.
func (a *A2AServer) Enable() {
	a.lock.Lock()
	defer a.lock.Unlock()
	a.Enabled = true
}

// Disable marks the server as disabled.
func (a *A2AServer) Disable() {
	a.lock.Lock()
	defer a.lock.Unlock()
	a.Enabled = false
}

// AddAgent prepares the agent and, if that succeeds, registers it on the server by its card name.
func (a *A2AServer) AddAgent(agent *model.Agent) error {
	err := a.PrepareAgent(agent)
	if err == nil {
		a.lock.Lock()
		a.Agents[agent.Card.Name] = agent
		a.lock.Unlock()
	}
	return err
}

// PrepareAgent validates the agent's card and wires up its behavior, task manager, handler and delegates.
func (a *A2AServer) PrepareAgent(agent *model.Agent) error {
	if agent == nil || agent.Card == nil {
		return errors.New("agent has no card")
	}
	if agent.Card.Name == "" {
		return errors.New("agent card has no name")
	}
	cardURL, err := url.Parse(agent.Card.URL)
	if err != nil {
		return err
	}
	// A prepared agent always has a config. The admin API sets the payload of the agent while it serves, and must not
	// be the one that creates the config that the requests read.
	if agent.Config == nil {
		agent.Config = &model.AgentConfig{}
	}
	if err := PrepareAgentBehavior(agent); err != nil {
		return err
	}
	if agent.ID == "" {
		agent.ID = fmt.Sprintf("%s(%s)", agent.Card.Name, a.ID)
	}
	agent.Port = a.Port
	// The agent doesn't stream unless its card says that it does.
	streaming := agent.Card.Capabilities.Streaming
	agent.Streaming = streaming != nil && *streaming
	agent.URI = cardURL.Path
	tm, err := trpctask.NewMemoryTaskManager(agent.Behavior.Impl)
	if err != nil {
		return err
	}
	srv, err := trpcserver.NewA2AServer(*agent.Card, tm)
	if err != nil {
		return err
	}
	agent.Server = srv
	agent.Handler = srv.Handler()
	if agent.Config != nil && agent.Config.Delegates != nil {
		for dName, d := range agent.Config.Delegates.Agents {
			d.GivenName = dName
			if d.AgentCall != nil {
				d.AgentCall.Prepare()
				d.AgentCall.NonNil()
			}
		}
		for dName, d := range agent.Config.Delegates.Tools {
			d.GivenName = dName
			if d.ToolCall != nil {
				d.ToolCall.Prepare()
				// The requests share the headers of the call, so they are completed once, here.
				if d.ToolCall.Headers == nil {
					d.ToolCall.Headers = types.NewHeaders()
				}
				d.ToolCall.Headers.NonNil()
			}
		}
		for dName, d := range agent.Config.Delegates.HTTP {
			d.GivenName = dName
		}
	}
	return nil
}

// PrepareAgentBehavior sets the behavior of the agent up, and what the behavior needs of the config of the agent.
func PrepareAgentBehavior(agent *model.Agent) error {
	b := newAgentBehavior(agent)
	b.prepareDelay()
	return b.prepareDelegates()
}

// GetAgent returns the agent with the given name, or nil if the server has none.
func (a *A2AServer) GetAgent(name string) *model.Agent {
	a.lock.RLock()
	defer a.lock.RUnlock()
	return a.Agents[name]
}

// RemoveAgent deletes the named agent from the server.
func (a *A2AServer) RemoveAgent(name string) {
	a.lock.Lock()
	defer a.lock.Unlock()
	delete(a.Agents, name)
}

// Serve serves the request with the agent. A status that is forced for the agent is given to the response, after the
// agent has served the request.
func (a *A2AServer) Serve(name string, w http.ResponseWriter, r *http.Request) error {
	agent := a.GetAgent(name)
	if agent == nil {
		return errAgentNotFound(name, a)
	}
	status, rem := forcedStatusFor(a.Port, agent, r)
	a.serve(agent, status, rem, w, r)
	return nil
}

// serve serves the request with the agent, and gives the response the status if there is one. The status is the one
// that is forced for the request, which is counted by the caller.
func (a *A2AServer) serve(agent *model.Agent, status, rem int, w http.ResponseWriter, r *http.Request) {
	w.Header().Add(constants.HeaderGotoA2AServer, a.ID)
	w.Header().Add(constants.HeaderGotoA2AAgent, agent.ID)
	ctx := r.Context()
	_, rs := util.GetRequestStoreFromContext(ctx)
	rs.RequestHeaders = r.Header
	aCtx := newAgentContext(a.Port, a.ID, rs.ListenerLabel, agent, r.Header, rs)
	r = r.WithContext(context.WithValue(ctx, util.AgentContextKey, aCtx))
	aCtx.ctx = r.Context()
	w, irw := intercept.WithInterceptAndStatus(r, w, status, !agent.Behavior.Stream)
	aCtx.irw = irw
	if status > 0 {
		aCtx.forcedStatus = status
		rs.StatusCode = status
	}
	agent.Serve(w, r)
	for v := range aCtx.remoteGotos {
		rs.ViaGotos = append(rs.ViaGotos, v)
	}
	if aCtx.forcedStatus > 0 {
		status = aCtx.forcedStatus
		rs.StatusCode = aCtx.forcedStatus
	}
	if status > 0 && status != 200 {
		sendStatus(a.ID, status, rem, irw.ResponseWriter, r)
	}
	rs.StatusCode = irw.StatusCode
	irw.Proceed()
}
