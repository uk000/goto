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
	"context"
	"fmt"
	"goto/pkg/ai/a2a/model"
	"goto/pkg/server/echo"
	"goto/pkg/types"
	"goto/pkg/util"
	"regexp"
	"strings"
	"time"

	a2aproto "trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
)

type DelegateTriggers map[string]*types.Triple[*regexp.Regexp, *model.DelegateToolCall, *model.DelegateAgentCall]
type UnaryHandler func(aCtx *AgentContext) (*taskmanager.MessageProcessingResult, error)
type StreamHandler func(aCtx *AgentContext) error

type AgentBehaviorImpl struct {
	self     model.IAgentBehavior
	agent    *model.Agent
	delay    *types.Delay
	doUnary  UnaryHandler
	doStream StreamHandler
}

func newAgentBehavior(agent *model.Agent) *AgentBehaviorImpl {
	if agent.Behavior == nil {
		agent.Behavior = &model.AgentBehavior{}
	}
	impl := &AgentBehaviorImpl{
		agent: agent,
	}
	if agent.Behavior.Echo {
		abe := &AgentBehaviorEcho{AgentBehaviorImpl: impl}
		impl.self = abe
		impl.doUnary = abe.DoUnary
		impl.doStream = abe.DoStream
		agent.Behavior.Impl = abe
	} else if agent.Behavior.Stream {
		abs := &AgentBehaviorStream{AgentBehaviorImpl: impl}
		impl.self = abs
		impl.doStream = abs.DoStream
		agent.Behavior.Impl = abs
	} else if agent.Behavior.Federate {
		abd := &AgentBehaviorFederate{
			AgentBehaviorImpl: impl,
			triggers:          map[string]*types.Triple[*regexp.Regexp, *model.DelegateToolCall, *model.DelegateAgentCall]{},
		}
		impl.self = abd
		impl.doUnary = abd.DoUnary
		impl.doStream = abd.DoStream
		agent.Behavior.Impl = abd
	} else if agent.Behavior.HTTPProxy {
		abrh := &AgentBehaviorRemoteHttp{AgentBehaviorImpl: impl}
		impl.self = abrh
		impl.doUnary = abrh.DoUnary
		impl.doStream = abrh.DoStream
		agent.Behavior.Impl = abrh
	}
	return impl
}

func (bd *AgentBehaviorImpl) prepareDelay() {
	if bd.agent.Config == nil || bd.agent.Config.Delay == nil {
		return
	}
	bd.agent.Config.Delay.Prepare()
	bd.delay = bd.agent.Config.Delay
}

func (b *AgentBehaviorImpl) prepareDelegates() error {
	bd, ok := b.self.(*AgentBehaviorFederate)
	if !ok {
		return nil
	}
	return bd.prepareDelegates()
}

func (bd *AgentBehaviorImpl) newAgentTask(aCtx *AgentContext, input *a2aproto.Message, options *taskmanager.ProcessOptions,
	handler taskmanager.TaskHandler) (*AgentTask, error) {
	taskID, err := handler.BuildTask(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build task: %w", err)
	}
	subscriber, err := handler.SubscribeTask(&taskID)
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to task: %w", err)
	}
	return &AgentTask{
		agent:      bd.agent,
		behavior:   bd.self,
		taskID:     taskID,
		input:      input,
		options:    options,
		handler:    handler,
		subscriber: subscriber,
	}, nil
}

func (b *AgentBehaviorImpl) ProcessMessage(ctx context.Context, input a2aproto.Message, options taskmanager.ProcessOptions,
	handler taskmanager.TaskHandler) (*taskmanager.MessageProcessingResult, error) {
	var aCtx *AgentContext
	if val := ctx.Value(util.AgentContextKey); val != nil {
		aCtx = val.(*AgentContext)
	}
	if aCtx == nil {
		return nil, fmt.Errorf("Received agent [%s] call without context", b.agent.ID)
	}
	task, err := b.newAgentTask(aCtx, &input, &options, handler)
	if err != nil {
		return nil, fmt.Errorf("failed to build task: %w", err)
	}
	aCtx.setContext(ctx, b, task, &input, &options, handler)
	if options.Streaming {
		return b.handleStream(aCtx)
	} else {
		return b.handleUnary(aCtx)
	}
}

func (b *AgentBehaviorImpl) handleUnary(aCtx *AgentContext) (result *taskmanager.MessageProcessingResult, err error) {
	if b.doUnary == nil {
		return nil, fmt.Errorf("Agent [%s] doesn't support Unary behavior.", b.agent.ID)
	}
	result, err = b.doUnary(aCtx)
	b.addOrSendServerInfo(aCtx, result)
	return
}

func (b *AgentBehaviorImpl) handleStream(aCtx *AgentContext) (*taskmanager.MessageProcessingResult, error) {
	if b.doStream == nil {
		return nil, fmt.Errorf("Agent [%s] doesn't support Streaming behavior.", b.agent.ID)
	}
	go b.stream(aCtx)
	return &taskmanager.MessageProcessingResult{
		StreamingEvents: aCtx.task.subscriber,
	}, nil
}

func (b *AgentBehaviorImpl) stream(aCtx *AgentContext) (err error) {
	b.addOrSendServerInfo(aCtx, nil)
	err = b.doStream(aCtx)
	if err != nil {
		aCtx.endTask(false, err.Error())
	} else {
		aCtx.endTask(true, b.agent.ID+": Task Done")
	}
	return
}

func (b *AgentBehaviorImpl) addOrSendServerInfo(aCtx *AgentContext, result *taskmanager.MessageProcessingResult) {
	serverInfo := a2aproto.NewDataPart(map[string]any{"Goto-Server-Info": echo.GetEchoResponseFromRS(aCtx.rs)})
	if result != nil && result.Result != nil {
		if msg, ok := result.Result.(*a2aproto.Message); ok {
			msg.Parts = append(msg.Parts, serverInfo)
		}
	}
}

func getMessageText(message *a2aproto.Message) string {
	s := strings.Builder{}
	for _, part := range message.Parts {
		if p, ok := part.(*a2aproto.TextPart); ok {
			s.WriteString(p.Text)
		} else if p, ok := part.(*a2aproto.DataPart); ok {
			s.WriteString(util.ToJSONText(p.Data))
		}
	}
	return s.String()
}

func createDataMessage(data any) a2aproto.Message {
	return a2aproto.NewMessage(
		a2aproto.MessageRoleAgent,
		[]a2aproto.Part{a2aproto.NewDataPart(data)},
	)
}

func createTextPartsFromArrayOrString(key string, val any, parts *[]a2aproto.Part) bool {
	hasText := false
	if arr, ok := val.([]any); ok {
		for _, data := range arr {
			if s, ok := data.(string); ok {
				hasText = true
				*parts = append(*parts, a2aproto.NewTextPart(fmt.Sprintf("%s: %s", key, s)))
			} else if arr2, ok := val.([]any); ok {
				createTextPartsFromArrayOrString(key, arr2, parts)
			}
		}
	} else if arr, ok := val.([]string); ok {
		for _, s := range arr {
			hasText = true
			*parts = append(*parts, a2aproto.NewTextPart(fmt.Sprintf("%s: %s", key, s)))
		}
	}
	return hasText
}

func createPartsFromMap(key string, m map[string]any, parts *[]a2aproto.Part, deep bool) {
	for k2, val := range m {
		if m2, ok := val.(map[string]any); ok {
			if deep {
				createPartsFromMap(fmt.Sprintf("%s: [%s]", key, k2), m2, parts, false)
			}
		} else {
			createTextPartsFromArrayOrString(fmt.Sprintf("%s: [%s]", key, k2), val, parts)
		}
	}
}

func createHybridMessage(toolResults, agentResults map[string]any) a2aproto.Message {
	parts := []a2aproto.Part{}
	createPartsFromMap("tools", toolResults, &parts, true)
	createPartsFromMap("agents", agentResults, &parts, true)
	parts = append(parts, a2aproto.NewDataPart(toolResults))
	parts = append(parts, a2aproto.NewDataPart(agentResults))
	return a2aproto.NewMessage(a2aproto.MessageRoleAgent, parts)
}

func createAnyParts(msg string, result any) []a2aproto.Part {
	parts := []a2aproto.Part{}
	if s, ok := result.(string); ok {
		parts = append(parts, a2aproto.NewTextPart(fmt.Sprintf("[%s] %s: %s", time.Now().Format(time.RFC3339Nano), msg, s)))
	} else if a, ok := result.([]any); ok {
		parts = append(parts, a2aproto.NewDataPart(a))
	} else if m, ok := result.(map[string]any); ok {
		parts = append(parts, a2aproto.NewDataPart(m))
	} else if t, ok := result.(a2aproto.TextPart); ok {
		parts = append(parts, t)
	} else if d, ok := result.(a2aproto.DataPart); ok {
		parts = append(parts, d)
	} else if p, ok := result.(a2aproto.Part); ok {
		parts = append(parts, p)
	} else {
		parts = append(parts, a2aproto.NewDataPart(result))
		//parts = append(parts, a2aproto.NewTextPart(fmt.Sprintf("[%s] %s: %s", time.Now().Format(time.RFC3339Nano), msg, util.ToJSONText(result))))
	}
	return parts
}
