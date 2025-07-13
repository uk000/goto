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
