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
	"errors"
	"fmt"
	a2aclient "goto/pkg/ai/a2a/client"
	"goto/pkg/ai/a2a/model"
	mcpclient "goto/pkg/ai/mcp/client"
	"goto/pkg/constants"
	"goto/pkg/types"
	"goto/pkg/util"
	"goto/pkg/util/timeline"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	a2aproto "trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
)

type AgentTask struct {
	agent      *model.Agent
	behavior   model.IAgentBehavior
	taskID     string
	input      *a2aproto.Message
	options    *taskmanager.ProcessOptions
	handler    taskmanager.TaskHandler
	subscriber taskmanager.TaskSubscriber
}

type AgentContext struct {
	port           int
	label          string
	serverID       string
	listener       string
	agent          *model.Agent
	behavior       model.IAgentBehavior
	ctx            context.Context
	rs             *util.RequestStore
	requestHeaders http.Header
	delay          *types.Delay
	triggers       DelegateTriggers
	tools          map[string]map[string]*model.DelegateToolCall
	agents         map[string]map[string]*model.DelegateAgentCall
	input          *a2aproto.Message
	inputText      string
	options        *taskmanager.ProcessOptions
	handler        taskmanager.TaskHandler
	task           *AgentTask
	logs           []string
	localProgress  chan *types.Pair[string, any]
	toolResults    map[string]any
	agentResults   map[string]any
	timeline       *timeline.Timeline
	err            error
}

type DelegateCallContext struct {
	agentCall        *a2aclient.AgentCall
	toolCall         *mcpclient.ToolCall
	httpCall         *model.HTTPCall
	name             string
	url              string
	upstreamProgress chan *types.Pair[string, any]
	tracker          *model.DelegateTracker
}

type agentOverrides struct {
	tool        string
	agent       string
	url         string
	remoteInput string
	count       int
	delay       time.Duration
	args        map[string]any
}

func newAgentContext(port int, serverID, listenerLabel string, agent *model.Agent, headers http.Header, rs *util.RequestStore) *AgentContext {
	ac := &AgentContext{
		port:           port,
		label:          agent.ID,
		serverID:       serverID,
		listener:       listenerLabel,
		agent:          agent,
		requestHeaders: headers,
		rs:             rs,
	}
	return ac
}

func newDelegateCallContext(tc *mcpclient.ToolCall, ac *a2aclient.AgentCall, tracker *model.DelegateTracker) *DelegateCallContext {
	dc := &DelegateCallContext{
		toolCall:         tc,
		agentCall:        ac,
		tracker:          tracker,
		upstreamProgress: make(chan *types.Pair[string, any], 10),
	}
	if tc != nil {
		dc.name = tc.Tool
		dc.url = tc.URL
	} else if ac != nil {
		dc.name = ac.Name
		dc.url = ac.AgentURL
	}
	return dc
}

func (ac *AgentContext) setContext(ctx context.Context, b *AgentBehaviorImpl, task *AgentTask, input *a2aproto.Message, options *taskmanager.ProcessOptions, handler taskmanager.TaskHandler) {
	ac.ctx = ctx
	ac.behavior = b
	ac.task = task
	ac.input = input
	ac.inputText = getMessageText(input)
	ac.options = options
	ac.handler = handler
	ac.delay = b.delay
	if abd, ok := ac.behavior.(*AgentBehaviorFederate); ok {
		ac.triggers = abd.triggers
	}
	ac.timeline = timeline.NewTimeline(ac.port, ac.label, map[string]any{
		constants.HeaderGotoA2AServer: ac.serverID,
		constants.HeaderGotoA2AAgent:  ac.agent.Card.Name,
	}, nil, ac.requestHeaders, nil, ac.notifyUpdate, ac.notifyEndSession)
	ac.timeline.StartTimeline(ac.agent.ID, fmt.Sprintf("Received Agent Call [%s]", ac.agent.Card.Name), ac.timeline.Server)
}

func (ac *AgentContext) detectRemoteCalls() {
	inputText := ac.inputText
	inputText, jsons := util.ExtractEmbeddedJSONs(inputText)
	inputText, targetHint := util.ExtractTargetHint(inputText)
	inputText, inputs := util.ExtractInputHint(inputText)
	inputText, portHint := util.ExtractPortHint(inputText)
	ac.matchDelegates(inputText, portHint, targetHint, inputs)
	ac.sendDelegatesMatchUpdate()
	// count, inputText := util.ExtractNumberHint(inputText)
	// overrideDelay, inputText := util.ExtractDurationHint(inputText)
	ac.setOverrideParamsFromInput(jsons, inputs)
	ac.inputText = inputText
}

func (ac *AgentContext) sendDelegatesMatchUpdate() {
	toolNames := []string{}
	agentNames := []string{}
	for name, tservers := range ac.tools {
		for server := range tservers {
			toolNames = append(toolNames, fmt.Sprintf("%s@%s", name, server))
		}
	}
	for name, aservers := range ac.agents {
		for server, _ := range aservers {
			agentNames = append(agentNames, fmt.Sprintf("%s@%s", name, server))
		}
	}
	msg := fmt.Sprintf("Matched Tools: %+v, Agents: %+v", toolNames, agentNames)
	log.Println(msg)
	ac.sendTaskStatusUpdate(a2aproto.TaskStateWorking, msg)
}

func (ac *AgentContext) matchDelegates(input string, portHint, delegateHint string, inputs map[string]string) {
	ac.tools = map[string]map[string]*model.DelegateToolCall{}
	ac.agents = map[string]map[string]*model.DelegateAgentCall{}
	addTool := func(d *model.DelegateToolCall) bool {
		tool := *d
		add := false
		if portHint != "" {
			if strings.Contains(tool.ToolCall.URL, portHint) {
				add = true
			}
		} else {
			add = true
		}
		if delegateHint != "" && !strings.EqualFold(delegateHint, tool.ToolCall.Tool) {
			altDelegate := tool.Substitutes[delegateHint]
			if altDelegate != nil {
				msg := fmt.Sprintf("Using alternate server [%s] with URL [%s] Authority [%s] instead of default Server [%s] URL [%s]",
					delegateHint, altDelegate.URL, altDelegate.Authority, tool.ToolCall.Server, tool.ToolCall.URL)
				ac.sendTaskStatusUpdate(a2aproto.TaskStateWorking, msg)
				tool.ToolCall.URL = altDelegate.URL
				tool.ToolCall.Authority = altDelegate.Authority
			}
		}
		if add {
			if ac.tools[d.GivenName] == nil {
				ac.tools[d.GivenName] = map[string]*model.DelegateToolCall{}
			}
			ac.tools[d.GivenName][tool.ToolCall.URL] = &tool
		}
		return add
	}
	addAgent := func(d *model.DelegateAgentCall) bool {
		agent := *d
		add := false
		if portHint != "" {
			if strings.Contains(agent.AgentCall.AgentURL, portHint) {
				add = true
			}
		} else {
			add = true
		}
		if delegateHint != "" {
			altDelegate := agent.Substitutes[delegateHint]
			if altDelegate != nil {
				agent.AgentCall.AgentURL = altDelegate.URL
				agent.AgentCall.Authority = altDelegate.Authority
			}
			agentURL := strings.Split(agent.AgentCall.AgentURL, agent.AgentCall.Name)[0]
			agent.AgentCall.AgentURL = agentURL + "/" + delegateHint
			agent.AgentCall.Name = delegateHint
		}
		if add {
			if ac.agents[d.GivenName] == nil {
				ac.agents[d.GivenName] = map[string]*model.DelegateAgentCall{}
			}
			ac.agents[d.GivenName][agent.AgentCall.AgentURL] = &agent
		}
		return add
	}
	haveExactMatches := false
	for name := range inputs {
		if ac.agent.Config.Delegates.Agents != nil && ac.agent.Config.Delegates.Agents[name] != nil {
			if addAgent(ac.agent.Config.Delegates.Agents[name]) {
				haveExactMatches = true
			}
		}
		if ac.agent.Config.Delegates.Tools != nil && ac.agent.Config.Delegates.Tools[name] != nil {
			if addTool(ac.agent.Config.Delegates.Tools[name]) {
				haveExactMatches = true
			}
		}
		if len(ac.tools)+len(ac.agents) >= ac.agent.Config.Delegates.MaxCalls {
			break
		}
	}
	for _, dInfos := range ac.triggers {
		for _, delegateTriple := range dInfos {
			if len(ac.tools)+len(ac.agents) >= ac.agent.Config.Delegates.MaxCalls {
				break
			}
			triggerPair := delegateTriple.First
			if strings.EqualFold(triggerPair.Left, input) {
				if delegateTriple.Second != nil {
					if addTool(delegateTriple.Second) {
						haveExactMatches = true
					}
				}
				if delegateTriple.Third != nil {
					if addAgent(delegateTriple.Third) {
						haveExactMatches = true
					}
				}
			}
		}
	}
	if !haveExactMatches {
		for _, dInfos := range ac.triggers {
			for _, delegateTriple := range dInfos {
				if len(ac.tools)+len(ac.agents) >= ac.agent.Config.Delegates.MaxCalls {
					break
				}
				triggerPair := delegateTriple.First
				if triggerPair.Right.MatchString(input) || strings.Contains(triggerPair.Left, input) {
					if delegateTriple.Second != nil {
						addTool(delegateTriple.Second)
					}
					if delegateTriple.Third != nil {
						addAgent(delegateTriple.Third)
					}
				}
			}
		}
	}
}

func (ac *AgentContext) setOverrideParamsFromInput(jsons []map[string]any, inputs map[string]string) {
	overrides := extractJSONValues(jsons)
	for name, override := range overrides {
		if tcalls := ac.tools[name]; tcalls != nil {
			for _, t := range tcalls {
				if override.url != "" {
					msg := fmt.Sprintf("Will use URL [%s] instead of [%s] for Tool [%s]", override.url, t.ToolCall.URL, t.ToolCall.Tool)
					log.Println(msg)
					ac.sendTaskStatusUpdate(a2aproto.TaskStateWorking, msg)
					t.ToolCall.URL = override.url
				}
				if override.args != nil {
					msg := fmt.Sprintf("Will use Args %+v instead of %+v for Tool [%s]", override.args, t.ToolCall.Args, t.ToolCall.Tool)
					log.Println(msg)
					ac.sendTaskStatusUpdate(a2aproto.TaskStateWorking, msg)
					t.ToolCall.Args.UpdateFromInputArgs(override.args)
				}
			}
		} else if acalls := ac.agents[name]; acalls != nil {
			for _, a := range acalls {
				if override.url != "" {
					msg := fmt.Sprintf("Will use URL [%s] instead of [%s] for Agent [%s]", override.url, a.AgentCall.AgentURL, a.AgentCall.Name)
					log.Println(msg)
					ac.sendTaskStatusUpdate(a2aproto.TaskStateWorking, msg)
					a.AgentCall.AgentURL = override.url
				}
				if override.args != nil {
					msg := fmt.Sprintf("Will use Data %+v instead of %+v for Agent [%s]", override.args, a.AgentCall.Data, a.AgentCall.Name)
					log.Println(msg)
					ac.sendTaskStatusUpdate(a2aproto.TaskStateWorking, msg)
					a.AgentCall.Data = override.args
				}
				if override.remoteInput != "" {
					msg := fmt.Sprintf("Will use Message %s instead of %s for Agent [%s]", override.remoteInput, a.AgentCall.Message, a.AgentCall.Name)
					log.Println(msg)
					ac.sendTaskStatusUpdate(a2aproto.TaskStateWorking, msg)
					a.AgentCall.Message = override.remoteInput
				}
			}
		}
	}
	for name, input := range inputs {
		agent := ac.agent.Config.Delegates.Agents[name]
		tool := ac.agent.Config.Delegates.Tools[name]
		if tool != nil {
			if tcalls := ac.tools[name]; tcalls != nil {
				for _, t := range tcalls {
					json, ok := util.JSONFromJSONText(input)
					if ok && !json.IsEmpty() {
						args := json.Object()
						msg := fmt.Sprintf("Will use Args %+v instead of %+v for Tool [%s]", args, t.ToolCall.Args, t.ToolCall.Tool)
						log.Println(msg)
						ac.sendTaskStatusUpdate(a2aproto.TaskStateWorking, msg)
						t.ToolCall.Args.UpdateFromInputArgs(args)
					}
				}
			}
		}
		if agent != nil {
			if acalls := ac.agents[name]; acalls != nil {
				for _, a := range acalls {
					msg := fmt.Sprintf("Will use Message %s instead of %s for Agent [%s]", input, a.AgentCall.Message, a.AgentCall.Name)
					log.Println(msg)
					ac.sendTaskStatusUpdate(a2aproto.TaskStateWorking, msg)
					a.AgentCall.Message = input
				}
			}
		}
	}
}

func extractJSONValues(jsons []map[string]any) map[string]*agentOverrides {
	overrides := map[string]*agentOverrides{}
	for _, json := range jsons {
		override := &agentOverrides{}
		if json["tool"] != nil {
			override.tool = json["tool"].(string)
			overrides[override.tool] = override
		}
		if json["agent"] != nil {
			override.agent = json["agent"].(string)
			overrides[override.agent] = override
		}
		if json["url"] != nil {
			override.url = json["url"].(string)
		}
		if json["input"] != nil {
			if s, ok := json["input"].(string); ok {
				override.remoteInput = s
			}
		}
		if json["args"] != nil {
			if m, ok := json["args"].(map[string]any); ok {
				override.args = m
			}
		}
	}
	return overrides
}

func (ac *AgentContext) sendData(key string, data any) error {
	data = map[string]any{key: data}
	a := a2aproto.Artifact{
		ArtifactID:  uuid.New().String(),
		Name:        util.Ptr(key),
		Description: util.Ptr(""),
		Parts:       []a2aproto.Part{a2aproto.NewDataPart(data)},
	}
	if err := ac.task.handler.AddArtifact(&ac.task.taskID, a, false, false); err != nil {
		return err
	}
	return nil
}

func (ac *AgentContext) prepareAndSendUpdate(msg string, data any, json bool, prefix, suffix string, finish bool) error {
	if data != nil {
		return ac.sendData(msg, data)
	} else {
		msg = fmt.Sprintf("%s[%s]: Agent [%s]: %s", prefix, time.Now().Format(time.RFC3339Nano), ac.agent.ID, msg)
		if suffix != "" {
			msg = fmt.Sprintf("%s%s", msg, suffix)
		}
		m := a2aproto.NewMessage(a2aproto.MessageRoleAgent, []a2aproto.Part{a2aproto.NewTextPart(msg)})
		state := a2aproto.TaskStateWorking
		if finish {
			state = a2aproto.TaskStateCompleted
		}
		if err := ac.task.handler.UpdateTaskState(&ac.task.taskID, state, &m); err != nil {
			log.Printf("Failed to notify client about [%s] with error: %s", msg, err.Error())
			return err
		}
	}
	return nil
}

func (ac *AgentContext) notifyUpdate(msg string, data any, json bool) error {
	if ac.task == nil || ac.task.handler == nil {
		return errors.New("Agent not initialized")
	}
	return ac.prepareAndSendUpdate(msg, data, json, "", "", false)
}

func (ac *AgentContext) notifyEndSession(msg string, data any, success bool) {
	if ac.task == nil || ac.task.handler == nil {
		return
	}
	prefix := "\u2705 "
	suffix := " \U0001F4AF "
	if !success {
		prefix = "\u274C "
		suffix = " \u274C "
	}
	if err := ac.prepareAndSendUpdate(msg, data, true, prefix, suffix, true); err != nil {
		log.Printf("Failed to notify client about [%s] with error: %s", msg, err.Error())
	}
	ac.timeline.TYPE = ""
	if err := ac.sendData("Timeline", ac.timeline); err != nil {
		log.Printf("Failed to send timeline to client with error: %s", err.Error())
	}
}

func (ac *AgentContext) sendTaskStatusUpdate(state a2aproto.TaskState, msg string) (err error) {
	if msg != "" {
		msg = fmt.Sprintf("[%s]: Agent [%s]: %s", time.Now().Format(time.RFC3339Nano), ac.agent.ID, msg)
		m := a2aproto.NewMessage(a2aproto.MessageRoleAgent, []a2aproto.Part{a2aproto.NewTextPart(msg)})
		err = ac.task.handler.UpdateTaskState(&ac.task.taskID, state, &m)
		if err != nil {
			return
		}
	}
	return
}

func (ac *AgentContext) sendDataUpdate(state a2aproto.TaskState, data any, part a2aproto.Part) (err error) {
	if data != nil {
		err = ac.sendArtifact(ac.agent.ID, "", "", data, false, false)
	}
	if part != nil {
		if tp, ok := part.(a2aproto.TextPart); ok {
			m := a2aproto.NewMessage(a2aproto.MessageRoleAgent, []a2aproto.Part{tp})
			err = ac.task.handler.UpdateTaskState(&ac.task.taskID, state, &m)
			if err != nil {
				return
			}
		} else if dp, ok := part.(a2aproto.DataPart); ok {
			err = ac.sendArtifact(ac.agent.ID, "", "", dp.Data, false, false)
		}
	}
	return
}

func (ac *AgentContext) sendArtifact(title, description string, text string, data any, isFinal, isQuestion bool) (err error) {
	if data != nil {
		a := a2aproto.Artifact{
			ArtifactID:  uuid.New().String(),
			Name:        util.Ptr(title),
			Description: util.Ptr(description),
			Parts:       []a2aproto.Part{a2aproto.NewDataPart(data)},
		}
		if err := ac.task.handler.AddArtifact(&ac.task.taskID, a, isFinal, isQuestion); err != nil {
			return err
		}
	} else {
		message := ""
		if title != "" {
			message = fmt.Sprintf("%s", title)
			message = fmt.Sprintf("%s\n%s", message, strings.Repeat("-", len(message)))
		}
		if description != "" {
			message = fmt.Sprintf("%s\n%s", message, description)
		}
		if text != "" {
			message = fmt.Sprintf("%s\n* %s", message, text)
			a := a2aproto.Artifact{
				ArtifactID:  uuid.New().String(),
				Name:        util.Ptr(title),
				Description: util.Ptr(description),
				Parts:       []a2aproto.Part{a2aproto.NewTextPart(message)},
			}
			if err := ac.task.handler.AddArtifact(&ac.task.taskID, a, isFinal, isQuestion); err != nil {
				return err
			}
		}
	}
	return nil
}

func (ac *AgentContext) sendTextArtifactFromParts(title, description string, parts []a2aproto.Part, isFinal, isQuestion bool) (err error) {
	artifact := a2aproto.Artifact{
		ArtifactID:  uuid.New().String(),
		Name:        util.Ptr(title),
		Description: &description,
		Parts:       parts,
	}
	return ac.task.handler.AddArtifact(&ac.task.taskID, artifact, isFinal, isQuestion)
}

func (ac *AgentContext) endTask(success bool, msg string) {
	ac.timeline.EndTimeline(ac.label, msg, nil, success)
	if ac.task.subscriber != nil {
		ac.task.subscriber.Close()
	}
	ac.task.handler.CleanTask(&ac.task.taskID)
}

func (ac *AgentContext) waitBeforeNextStep() (time.Duration, error) {
	select {
	case <-ac.ctx.Done():
		log.Printf("Task %s cancelled during delay: %v", ac.task.taskID, ac.ctx.Err())
		ac.task.handler.UpdateTaskState(&ac.task.taskID, a2aproto.TaskStateCanceled, nil)
		return 0, ac.ctx.Err()
	case delay := <-ac.delay.Block():
		return delay, nil
	}
}

func (ac *AgentContext) AddEvent(msg string, remoteData any, json bool) {
	if msg != "" || remoteData != nil {
		ac.timeline.AddEvent(ac.label, msg, nil, remoteData, json)
	}
}

func (ac *AgentContext) AddData(data any, json bool) {
	if data != nil {
		ac.timeline.AddData(ac.label, data, json)
	}
}
