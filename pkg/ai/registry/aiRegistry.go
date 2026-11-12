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
	a2amodel "goto/pkg/ai/a2a/model"
	mcpserver "goto/pkg/ai/mcp/server"
	"maps"
	"sync"
)

type SkillsRegistry struct {
	Skill         *a2amodel.AgentSkill
	AgentsBySkill map[string][]*a2amodel.Agent
	ToolsBySkill  map[string][]*mcpserver.MCPTool
	lock          sync.RWMutex
}

type AgentRegistry struct {
	Agents map[string]map[int]*a2amodel.Agent
	lock   sync.RWMutex
}

type MCPRegistry struct {
	Servers map[string]*mcpserver.MCPServer
	lock    sync.RWMutex
}

var (
	TheAgentRegistry  = newAgentRegistry()
	TheMCPRegistry    = newMCPRegistry()
	TheSkillsRegistry = newSkillsRegistry()
	lock              sync.RWMutex
)

// newAgentRegistry creates an initialized, empty agent registry.
func newAgentRegistry() *AgentRegistry {
	r := &AgentRegistry{}
	r.init()
	return r
}

// newMCPRegistry creates an initialized, empty MCP server registry.
func newMCPRegistry() *MCPRegistry {
	r := &MCPRegistry{}
	r.init()
	return r
}

// newSkillsRegistry creates an initialized, empty skills registry.
func newSkillsRegistry() *SkillsRegistry {
	r := &SkillsRegistry{}
	r.init()
	return r
}

// init resets the registry to an empty agents map.
func (r *AgentRegistry) init() {
	r.Agents = map[string]map[int]*a2amodel.Agent{}
}

// AddAgents registers each of the agents under the given port.
func (r *AgentRegistry) AddAgents(agents []*a2amodel.Agent, port int) {
	for _, agent := range agents {
		r.AddAgent(agent, port)
	}
}

// isRegistrable tells whether the agent has a card with a name, which is what the registry keys agents by.
func isRegistrable(agent *a2amodel.Agent) bool {
	return agent != nil && agent.Card != nil && agent.Card.Name != ""
}

// AddAgent registers the agent under its card name and the port. An agent without a named card is ignored.
func (r *AgentRegistry) AddAgent(agent *a2amodel.Agent, port int) {
	if !isRegistrable(agent) {
		return
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	if r.Agents[agent.Card.Name] == nil {
		r.Agents[agent.Card.Name] = map[int]*a2amodel.Agent{}
	}
	r.Agents[agent.Card.Name][port] = agent
}

// GetAgents returns the agents of the given name by port. The map is a copy that the caller owns, so that it doesn't
// change (or race) with the registry.
func (r *AgentRegistry) GetAgents(name string) map[int]*a2amodel.Agent {
	r.lock.RLock()
	defer r.lock.RUnlock()
	return maps.Clone(r.Agents[name])
}

// snapshot returns a copy of the registry: agents by name, and by port.
func (r *AgentRegistry) snapshot() map[string]map[int]*a2amodel.Agent {
	r.lock.RLock()
	defer r.lock.RUnlock()
	agents := make(map[string]map[int]*a2amodel.Agent, len(r.Agents))
	for name, byPort := range r.Agents {
		agents[name] = maps.Clone(byPort)
	}
	return agents
}

// clearAll empties the registry, and returns the number of agent names that were registered.
func (r *AgentRegistry) clearAll() int {
	r.lock.Lock()
	defer r.lock.Unlock()
	count := len(r.Agents)
	r.Agents = map[string]map[int]*a2amodel.Agent{}
	return count
}

// Clear removes all agents from the registry.
func (r *AgentRegistry) Clear() {
	r.clearAll()
}

// RemoveAgent removes the agents of the given name, on all ports, from the registry.
func (r *AgentRegistry) RemoveAgent(name string) {
	r.lock.Lock()
	defer r.lock.Unlock()
	delete(r.Agents, name)
}

// init resets the registry to an empty servers map.
func (mr *MCPRegistry) init() {
	mr.Servers = map[string]*mcpserver.MCPServer{}
}

// init resets the registry to empty agents-by-skill and tools-by-skill maps.
func (sr *SkillsRegistry) init() {
	sr.AgentsBySkill = map[string][]*a2amodel.Agent{}
	sr.ToolsBySkill = map[string][]*mcpserver.MCPTool{}
}
