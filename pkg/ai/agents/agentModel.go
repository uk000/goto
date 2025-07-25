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

import a2amodel "goto/pkg/ai/agents/a2a/model"

type Agent interface {
	GetCard() *a2amodel.AgentCard
	Invoke()
}

type LocalAgent struct {
	Agent
	card *a2amodel.AgentCard
}

type RemoteAgent struct {
	Agent
	card *a2amodel.AgentCard
}

type Tool interface {
}

type LocalTool struct {
	Tool
}

type RemoteTool struct {
	Tool
}

type MCPServer interface {
}

type LocalMCPServer struct {
	MCPServer
	Routes map[string]*SkillRoute
}

type RemoteMCPServer struct {
	MCPServer
}

func (la *LocalAgent) GetCard() *a2amodel.AgentCard {
	return nil
}
