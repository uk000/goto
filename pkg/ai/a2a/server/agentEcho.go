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
	"goto/pkg/server/echo"

	a2aproto "trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
)

type AgentBehaviorEcho struct {
	*AgentBehaviorImpl
}

func (ab *AgentBehaviorEcho) DoUnary(aCtx *AgentContext) (*taskmanager.MessageProcessingResult, error) {
	msg := createDataMessage(ab.getEchoMessage(aCtx, aCtx.input))
	return &taskmanager.MessageProcessingResult{
		Result: &msg,
	}, nil
}

func (ab *AgentBehaviorEcho) DoStream(aCtx *AgentContext) error {
	aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, "", ab.getEchoMessage(aCtx, aCtx.input))
	aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, "Echo response sent", nil)
	return nil
}

func (ab *AgentBehaviorEcho) getEchoMessage(aCtx *AgentContext, input *a2aproto.Message) (parts []a2aproto.Part) {
	parts = append(parts, input.Parts...)
	parts = append(parts, a2aproto.NewDataPart(echo.GetEchoResponseFromRS(aCtx.rs)))
	return
}
