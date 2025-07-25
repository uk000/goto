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
	a2amodel "goto/pkg/ai/agents/a2a/model"
	"sync"
)

type SkillRoute struct {
	Skill   *a2amodel.AgentSkill
	Servers []*MCPServer
	Agents  []*Agent
}

type AgentRegistry struct {
	Agents        map[string]*Agent
	AgentsBySkill map[string][]*Agent
}

type ToolRegistry struct {
	Tools        map[string]*Tool
	ToolsBySkill map[string][]*Tool
}

type MCPRegistry struct {
	Servers        map[string]*MCPServer
	ServersBySkill map[string][]*MCPServer
}

var (
	PortAgentRegistry = map[int]*AgentRegistry{}
	PortToolRegistry  = map[int]*ToolRegistry{}
	PortMCPRegistry   = map[int]*MCPRegistry{}
	lock              sync.RWMutex
)

func GetAgentRegistry(port int) *AgentRegistry {
	lock.Lock()
	defer lock.Unlock()
	if PortAgentRegistry[port] == nil {
		PortAgentRegistry[port] = &AgentRegistry{}
		PortAgentRegistry[port].init()
	}
	return PortAgentRegistry[port]
}

func GetToolRegistry(port int) *ToolRegistry {
	lock.Lock()
	defer lock.Unlock()
	if PortToolRegistry[port] == nil {
		PortToolRegistry[port] = &ToolRegistry{}
		PortToolRegistry[port].init()
	}
	return PortToolRegistry[port]
}

func GetMCPRegistry(port int) *MCPRegistry {
	lock.Lock()
	defer lock.Unlock()
	if PortMCPRegistry[port] == nil {
		PortMCPRegistry[port] = &MCPRegistry{}
		PortMCPRegistry[port].init()
	}
	return PortMCPRegistry[port]
}

func (ar *AgentRegistry) init() {
	ar.Agents = map[string]*Agent{}
	ar.AgentsBySkill = map[string][]*Agent{}
}

func (tr *ToolRegistry) init() {
	tr.Tools = map[string]*Tool{}
	tr.ToolsBySkill = map[string][]*Tool{}
}

func (mr *MCPRegistry) init() {
	mr.Servers = map[string]*MCPServer{}
	mr.ServersBySkill = map[string][]*MCPServer{}
}
