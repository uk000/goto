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
	"context"
	"fmt"
	"goto/pkg/ai/a2a/model"
	"goto/pkg/constants"
	"goto/pkg/types"
	"goto/pkg/util"
	"reflect"
	"regexp"
	"strings"
	"time"

	a2aproto "trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
)

type DelegateTriggerArr []*types.Triple[*types.Pair[string, *regexp.Regexp], *model.DelegateToolCall, *model.DelegateAgentCall]
type DelegateTriggers map[string]DelegateTriggerArr
type UnaryHandler func(aCtx *AgentContext) (*taskmanager.MessageProcessingResult, error)
type StreamHandler func(aCtx *AgentContext) (string, error)

type AgentBehaviorImpl struct {
	self     model.IAgentBehavior
	agent    *model.Agent
	delay    *types.Delay
	doUnary  UnaryHandler
	doStream StreamHandler
}

// newAgentBehavior creates the agent's behavior (echo, stream, federate or HTTP proxy) from its config.
func newAgentBehavior(agent *model.Agent) *AgentBehaviorImpl {
	if agent.Behavior == nil {
		agent.Behavior = &model.AgentBehavior{}
	}
	// An agent that is not told how to behave echoes.
	b := agent.Behavior
	useDefault := !b.Echo && !b.Stream && !b.Federate && !b.HTTPProxy
	impl := &AgentBehaviorImpl{
		agent: agent,
	}
	if agent.Behavior.Echo || useDefault {
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
			triggers:          map[string]DelegateTriggerArr{},
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

// prepareDelay prepares the delay of the agent's config, if it has one, for the behavior to use.
func (bd *AgentBehaviorImpl) prepareDelay() {
	if bd.agent.Config == nil || bd.agent.Config.Delay == nil {
		return
	}
	bd.agent.Config.Delay.Prepare()
	bd.delay = bd.agent.Config.Delay
}

// prepareDelegates prepares the delegates if the behavior is federate, and does nothing otherwise.
func (b *AgentBehaviorImpl) prepareDelegates() error {
	bd, ok := b.self.(*AgentBehaviorFederate)
	if !ok {
		return nil
	}
	return bd.prepareDelegates()
}

// newAgentTask builds the task for the message with the handler, and subscribes to its events.
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

// ProcessMessage handles an incoming message by building its task and context and running the stream or unary behavior.
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

// handleUnary runs the unary behavior, and adds the timeline to its result message if the timeline is to be reported.
func (b *AgentBehaviorImpl) handleUnary(aCtx *AgentContext) (result *taskmanager.MessageProcessingResult, err error) {
	if b.doUnary == nil {
		return nil, fmt.Errorf("Agent [%s] doesn't support Unary behavior.", b.agent.ID)
	}
	result, err = b.doUnary(aCtx)
	if util.IsNil(err) && result != nil {
		if result.Result == nil {
			result.Result = &a2aproto.Message{}
		}
		if aCtx.reportTimeline {
			if msg, ok := result.Result.(*a2aproto.Message); ok {
				msg.Parts = append(msg.Parts, a2aproto.NewDataPart(map[string]any{constants.HeaderGotoServerTimeline: aCtx.timeline}))
			}
		}
	}
	return
}

// handleStream starts the stream behavior in a goroutine and returns the subscriber that its events are read from.
func (b *AgentBehaviorImpl) handleStream(aCtx *AgentContext) (*taskmanager.MessageProcessingResult, error) {
	if b.doStream == nil {
		return nil, fmt.Errorf("Agent [%s] doesn't support Streaming behavior.", b.agent.ID)
	}
	go b.stream(aCtx)
	return &taskmanager.MessageProcessingResult{
		StreamingEvents: aCtx.task.subscriber,
	}, nil
}

// stream runs the stream behavior and ends the task with its status, as a failure if it returns an error.
func (b *AgentBehaviorImpl) stream(aCtx *AgentContext) {
	status, err := b.doStream(aCtx)
	if !util.IsNil(err) {
		aCtx.endTask(false, fmt.Sprintf("%s | Error: %s", status, err.Error()))
	} else {
		aCtx.endTask(true, status)
	}
}

// createTextMessage makes an agent message of one text part.
func createTextMessage(data string) a2aproto.Message {
	return a2aproto.NewMessage(
		a2aproto.MessageRoleAgent,
		[]a2aproto.Part{a2aproto.NewTextPart(data)},
	)
}

// createDataMessage makes an agent message of one data part.
func createDataMessage(data any) a2aproto.Message {
	return a2aproto.NewMessage(
		a2aproto.MessageRoleAgent,
		[]a2aproto.Part{a2aproto.NewDataPart(data)},
	)
}

// createTextPartsFromArrayOrString adds a "key: text" part for each string in the value, and reports whether any was found.
func createTextPartsFromArrayOrString(key string, val any, parts *[]a2aproto.Part, builder *strings.Builder) bool {
	hasText := false
	if arr, ok := val.([]any); ok {
		for _, data := range arr {
			if s, ok := data.(string); ok {
				hasText = true
				msg := fmt.Sprintf("%s: %s\n", key, s)
				*parts = append(*parts, a2aproto.NewTextPart(msg))
				builder.WriteString(msg)
			} else if arr2, ok := data.([]any); ok {
				createTextPartsFromArrayOrString(key, arr2, parts, builder)
			}
		}
	} else if arr, ok := val.([]string); ok {
		for _, s := range arr {
			hasText = true
			msg := fmt.Sprintf("%s: %s\n", key, s)
			*parts = append(*parts, a2aproto.NewTextPart(msg))
			builder.WriteString(msg)
		}
	} else if s, ok := val.(string); ok {
		hasText = true
		msg := fmt.Sprintf("%s: %s\n", key, s)
		*parts = append(*parts, a2aproto.NewTextPart(msg))
		builder.WriteString(msg)
	}
	return hasText
}

// createPartsFromMap adds the text parts of the values of the map, going into the values that are maps or arrays.
func createPartsFromMap(key string, m map[string]any, parts *[]a2aproto.Part, builder *strings.Builder, deep bool) {
	for k, any := range m {
		v := reflect.ValueOf(any)
		// Check if the Kind of that type is a Map
		if v.Kind() == reflect.Map {
			createPartsFromMapIter(fmt.Sprintf("%s.%s", key, k), v, parts, builder, deep)
			*parts = append(*parts, a2aproto.NewDataPart(m))
		} else if v.Kind() == reflect.Array {
			createPartsFromArrayIter(fmt.Sprintf("%s.%s", key, k), v, parts, builder, deep)
			*parts = append(*parts, a2aproto.NewDataPart(m))
		} else {
			createTextPartsFromArrayOrString(fmt.Sprintf("%s: [%s]", key, k), any, parts, builder)
		}
	}
}

// createPartsFromMapIter walks a map by reflection and adds text parts of its values, recursing into maps and arrays.
func createPartsFromMapIter(key string, m reflect.Value, parts *[]a2aproto.Part, builder *strings.Builder, deep bool) {
	iter := m.MapRange()
	for iter.Next() {
		k := iter.Key()
		any := iter.Value().Interface()
		v := reflect.ValueOf(any)
		if v.Kind() == reflect.Map {
			createPartsFromMapIter(fmt.Sprintf("%s.%s", key, k), v, parts, builder, deep)
		} else if v.Kind() == reflect.Array || v.Kind() == reflect.Slice {
			createPartsFromArrayIter(fmt.Sprintf("%s.%s", key, k), v, parts, builder, deep)
		} else {
			createTextPartsFromArrayOrString(fmt.Sprintf("%s.%s", key, k), any, parts, builder)
		}
	}
}

// createPartsFromArrayIter walks an array by reflection and adds text parts of its elements, recursing into maps and arrays.
func createPartsFromArrayIter(key string, arr reflect.Value, parts *[]a2aproto.Part, builder *strings.Builder, deep bool) {
	for i := 0; i < arr.Len(); i++ {
		any := arr.Index(i)
		v := reflect.ValueOf(any)
		if v.Kind() == reflect.Map {
			createPartsFromMapIter(fmt.Sprintf("%s[%d]", key, i), v, parts, builder, deep)
		} else if v.Kind() == reflect.Array || v.Kind() == reflect.Slice {
			createPartsFromArrayIter(fmt.Sprintf("%s[%d]", key, i), v, parts, builder, deep)
		} else {
			createTextPartsFromArrayOrString(fmt.Sprintf("%s[%d]", key, i), any, parts, builder)
		}
	}
}

// createHybridMessage makes a message of the text of the tool and agent results, followed by their data and headers.
func createHybridMessage(id string, toolResults, agentResults, upstreamHeaders map[string]any) a2aproto.Message {
	parts := []a2aproto.Part{}
	builder := &strings.Builder{}
	createPartsFromMap(fmt.Sprintf("[%s] Reporting Result:", id), toolResults, &parts, builder, false)
	createPartsFromMap(fmt.Sprintf("[%s] Reporting Result:", id), agentResults, &parts, builder, false)
	parts = append(parts, a2aproto.NewDataPart(toolResults))
	parts = append(parts, a2aproto.NewDataPart(agentResults))
	parts = append(parts, a2aproto.NewDataPart(upstreamHeaders))
	finalParts := []a2aproto.Part{a2aproto.NewTextPart(builder.String())}
	finalParts = append(finalParts, parts...)
	return a2aproto.NewMessage(a2aproto.MessageRoleAgent, finalParts)
}

// createAnyPart makes a part of the result: timestamped text for a string, the part itself, or data keyed by the message.
func createAnyPart(msg string, result any) a2aproto.Part {
	if s, ok := result.(string); ok {
		return a2aproto.NewTextPart(fmt.Sprintf("[%s] %s: %s", time.Now().Format(time.RFC3339Nano), msg, s))
	} else if a, ok := result.([]any); ok {
		return a2aproto.NewDataPart(map[string]any{msg: a})
	} else if m, ok := result.(map[string]any); ok {
		return a2aproto.NewDataPart(map[string]any{msg: m})
	} else if t, ok := result.(a2aproto.TextPart); ok {
		return t
	} else if d, ok := result.(a2aproto.DataPart); ok {
		return d
	} else if p, ok := result.(a2aproto.Part); ok {
		return p
	} else {
		return a2aproto.NewDataPart(map[string]any{msg: result})
		//parts = append(parts, a2aproto.NewTextPart(fmt.Sprintf("[%s] %s: %s", time.Now().Format(time.RFC3339Nano), msg, util.ToJSONText(result))))
	}
}
