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
	"fmt"
	aicommon "goto/pkg/ai/common"
	"goto/pkg/global"
	"goto/pkg/server/echo"
	"goto/pkg/util"
	"time"

	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
)

type AgentBehaviorEcho struct {
	*AgentBehaviorImpl
}

func (ab *AgentBehaviorEcho) DoUnary(aCtx *AgentContext) (*taskmanager.MessageProcessingResult, error) {
	msg := createDataMessage(util.ToJSONText(echo.GetEchoResponseFromRS(aCtx.rs)))
	return &taskmanager.MessageProcessingResult{
		Result: &msg,
	}, nil
}

func (ab *AgentBehaviorEcho) DoStream(aCtx *AgentContext) (string, error) {
	msg := fmt.Sprintf("Agent: %s[%s]. Received Input: %s at Time: %s", ab.agent.ID, global.Funcs.GetListenerLabelForPort(ab.agent.Port),
		aicommon.GetInputTextFromMessage(aCtx.input), time.Now().Format(time.RFC3339))
	aCtx.AddEvent(msg, nil, false)
	return "Echo Response Sent", nil
}
