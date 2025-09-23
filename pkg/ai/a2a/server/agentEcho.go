package a2aserver

import (
	"goto/pkg/util"

	a2aproto "trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
)

type AgentBehaviorEcho struct {
	*AgentBehaviorImpl
}

func (ab *AgentBehaviorEcho) DoUnary(aCtx *AgentCallContext) (*taskmanager.MessageProcessingResult, error) {
	_, msg := ab.getEchoMessage(aCtx.input)
	return &taskmanager.MessageProcessingResult{
		Result: &msg,
	}, nil
}

func (ab *AgentBehaviorEcho) DoStream(aCtx *AgentCallContext) error {
	output, _ := ab.getEchoMessage(aCtx.task.input)
	aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, output, nil)
	return nil
}

func (ab *AgentBehaviorEcho) getEchoMessage(input a2aproto.Message) (output string, message a2aproto.Message) {
	output = util.ToJSONText(input)
	message = createDataMessage(input)
	return
}
