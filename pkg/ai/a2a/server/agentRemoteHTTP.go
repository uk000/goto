package a2aserver

import (
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
)

type AgentBehaviorRemoteHttp struct {
	*AgentBehaviorImpl
}

func (ab *AgentBehaviorRemoteHttp) DoUnary(aCtx *AgentContext) (*taskmanager.MessageProcessingResult, error) {
	return nil, nil
}

func (ab *AgentBehaviorRemoteHttp) DoStream(aCtx *AgentContext) error {
	return nil
}
