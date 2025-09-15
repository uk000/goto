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
	a2amodel "goto/pkg/ai/a2a/model"
	mcpserver "goto/pkg/ai/mcp/server"
	"sync"
)

type SkillsRegistry struct {
	Skill         *a2amodel.AgentSkill
	AgentsBySkill map[string][]*a2amodel.Agent
	ToolsBySkill  map[string][]*mcpserver.MCPTool
	lock          sync.RWMutex
}

type AgentRegistry struct {
	Agents map[string]*a2amodel.Agent
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

func newAgentRegistry() *AgentRegistry {
	r := &AgentRegistry{}
	r.init()
	return r
}

func newMCPRegistry() *MCPRegistry {
	r := &MCPRegistry{}
	r.init()
	return r
}

func newSkillsRegistry() *SkillsRegistry {
	r := &SkillsRegistry{}
	r.init()
	return r
}

func (r *AgentRegistry) init() {
	r.Agents = map[string]*a2amodel.Agent{}
}

func (r *AgentRegistry) AddAgents(agents []*a2amodel.Agent) {
	for _, agent := range agents {
		r.AddAgent(agent)
	}
}

func (r *AgentRegistry) AddAgent(agent *a2amodel.Agent) {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.Agents[agent.Card.Name] = agent
}

func (ar *AgentRegistry) GetAgent(name string) *a2amodel.Agent {
	ar.lock.RLock()
	defer ar.lock.RUnlock()
	return ar.Agents[name]
}

func (mr *MCPRegistry) init() {
	mr.Servers = map[string]*mcpserver.MCPServer{}
}

func (sr *SkillsRegistry) init() {
	sr.AgentsBySkill = map[string][]*a2amodel.Agent{}
	sr.ToolsBySkill = map[string][]*mcpserver.MCPTool{}
}
