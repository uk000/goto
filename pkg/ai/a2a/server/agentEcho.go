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

package a2aserver

import (
	"goto/pkg/util"

	a2aproto "trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
)

type AgentBehaviorEcho struct {
	*AgentBehaviorImpl
}

func (ab *AgentBehaviorEcho) DoUnary(aCtx *AgentContext) (*taskmanager.MessageProcessingResult, error) {
	_, msg := ab.getEchoMessage(aCtx.input)
	return &taskmanager.MessageProcessingResult{
		Result: &msg,
	}, nil
}

func (ab *AgentBehaviorEcho) DoStream(aCtx *AgentContext) error {
	output, _ := ab.getEchoMessage(aCtx.task.input)
	aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, output, nil)
	return nil
}

func (ab *AgentBehaviorEcho) getEchoMessage(input *a2aproto.Message) (output string, message a2aproto.Message) {
	output = util.ToJSONText(input)
	message = createDataMessage(input)
	return
}
