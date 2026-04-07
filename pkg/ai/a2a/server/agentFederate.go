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
	"errors"
	"fmt"
	a2aclient "goto/pkg/ai/a2a/client"
	"goto/pkg/ai/a2a/model"
	aicommon "goto/pkg/ai/common"
	mcpclient "goto/pkg/ai/mcp/client"
	"goto/pkg/types"
	"goto/pkg/util"
	"log"
	"net/http"
	"regexp"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	a2aproto "trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
)

type AgentBehaviorFederate struct {
	*AgentBehaviorImpl
	triggers DelegateTriggers
}

func (ab *AgentBehaviorFederate) DoUnary(aCtx *AgentContext) (*taskmanager.MessageProcessingResult, error) {
	aCtx.triggers = ab.triggers
	aCtx.detectRemoteCalls()
	aCtx.toolResults = map[string]any{}
	aCtx.agentResults = map[string]any{}
	if len(ab.triggers) == 0 {
		aCtx.AddEvent("Agent update: No tools available", nil, false)
		aCtx.toolResults[""] = []any{"Agent update: No tools available"}
	} else if len(aCtx.tools) == 0 {
		aCtx.AddEvent("Agent update: No tools were triggered", nil, false)
		aCtx.toolResults[""] = []any{"Agent update: No tools were triggered"}
	} else {
		ab.runTools(aCtx, nil, nil)
		if len(aCtx.toolResults) == 0 {
			aCtx.AddEvent("Agent update: No tool produced any results", nil, false)
			aCtx.toolResults[""] = []any{"Agent update: No tool produced any results"}
		}
	}
	if len(ab.triggers) == 0 {
		aCtx.AddEvent("Agent update: No agents available", nil, false)
		aCtx.agentResults[""] = []any{"Agent update: No agents available"}
	} else if len(aCtx.agents) == 0 {
		aCtx.AddEvent("Agent update: No agents were triggered", nil, false)
		aCtx.agentResults[""] = []any{"Agent update: No agents were triggered"}
	} else {
		ab.runAgents(aCtx, nil, nil)
		if len(aCtx.agentResults) == 0 {
			aCtx.AddEvent("Agent update: No agent produced any results", nil, false)
			aCtx.agentResults[""] = []any{"No agent produced any results"}
		}
	}
	result := createHybridMessage(aCtx.agent.ID, aCtx.toolResults, aCtx.agentResults)
	return &taskmanager.MessageProcessingResult{
		Result: &result,
	}, nil
}

func (ab *AgentBehaviorFederate) DoStream(aCtx *AgentContext) (string, error) {
	aCtx.triggers = ab.triggers
	aCtx.detectRemoteCalls()
	aCtx.localProgress = make(chan *types.Pair[string, any], 10)
	aCtx.timeline.SetStreamPreferred(aCtx.localProgress)

	runWG := &sync.WaitGroup{}
	resultsWG := &sync.WaitGroup{}
	resultsWG.Add(1)
	go ab.processLocalUpdates(aCtx, resultsWG)
	if len(ab.triggers) == 0 {
		aCtx.AddEvent("Agent update: No tools available", nil, false)
	} else if len(aCtx.tools) == 0 {
		aCtx.AddEvent("Agent update: No tools were triggered", nil, false)
	} else {
		runWG.Add(1)
		go ab.runTools(aCtx, runWG, resultsWG)
	}
	if len(ab.triggers) == 0 {
		aCtx.AddEvent("Agent update: No agents available", nil, false)
	} else if len(aCtx.agents) == 0 {
		aCtx.AddEvent("Agent update: No agents were triggered", nil, false)
	} else {
		runWG.Add(1)
		go ab.runAgents(aCtx, runWG, resultsWG)
	}
	runWG.Wait()
	close(aCtx.localProgress)
	resultsWG.Wait()
	if aCtx.err != nil {
		return "Agent federation done with error \u274C", aCtx.err
	}
	return "Agent federation done \U000026F3", nil
}

func (ab *AgentBehaviorFederate) runTools(aCtx *AgentContext, runWG, resultsWG *sync.WaitGroup) {
	parallel := ab.agent.Config.Delegates.Parallel
	toolsWG := &sync.WaitGroup{}
	delegateContexts := []*DelegateCallContext{}
	for _, tcalls := range aCtx.tools {
		for _, tc := range tcalls {
			if tc.ToolCall.Headers == nil {
				tc.ToolCall.Headers = types.NewHeaders()
			}
			tc.ToolCall.Headers.NonNil()
			dCtx := newDelegateCallContext(tc.ToolCall, nil, tc.Tracker)
			dCtx.tracker.IncrementCall()
			delegateContexts = append(delegateContexts, dCtx)
			if resultsWG != nil {
				resultsWG.Add(1)
				go ab.processUpstreamUpdates(aCtx, dCtx, resultsWG)
			}
			toolsWG.Add(1)
			if parallel {
				go func(dCtx *DelegateCallContext) {
					ab.callTool(aCtx, dCtx, toolsWG)
				}(dCtx)
			} else {
				ab.callTool(aCtx, dCtx, toolsWG)
			}
		}
	}
	toolsWG.Wait()
	for _, dCtx := range delegateContexts {
		dCtx.tracker.Finish()
		close(dCtx.upstreamProgress)
	}
	if runWG != nil {
		runWG.Done()
	}
}

func (ab *AgentBehaviorFederate) runAgents(aCtx *AgentContext, runWG, resultsWG *sync.WaitGroup) {
	parallel := ab.agent.Config.Delegates.Parallel
	agentsWG := &sync.WaitGroup{}
	delegateContexts := []*DelegateCallContext{}
	for _, acalls := range aCtx.agents {
		for _, a := range acalls {
			a.AgentCall.NonNil()
			dCtx := newDelegateCallContext(nil, a.AgentCall, a.Tracker)
			dCtx.tracker.IncrementCall()
			delegateContexts = append(delegateContexts, dCtx)
			if resultsWG != nil {
				resultsWG.Add(1)
				go ab.processUpstreamUpdates(aCtx, dCtx, resultsWG)
			}
			agentsWG.Add(1)
			if parallel {
				go func(dCtx *DelegateCallContext) {
					ab.callAgent(aCtx, dCtx, agentsWG)
				}(dCtx)
			} else {
				ab.callAgent(aCtx, dCtx, agentsWG)
			}
		}
	}
	agentsWG.Wait()
	for _, dCtx := range delegateContexts {
		dCtx.tracker.Finish()
		close(dCtx.upstreamProgress)
	}
	if runWG != nil {
		runWG.Done()
	}
}

func (ab *AgentBehaviorFederate) callAgent(aCtx *AgentContext, dCtx *DelegateCallContext, agentsWG *sync.WaitGroup) {
	defer agentsWG.Done()
	result, err := ab.invokeAgent(aCtx, dCtx)
	if err != nil {
		aCtx.err = err
		msg := fmt.Sprintf("Failed to invoke Agent [%s] at URL [%s] with error: %s", dCtx.agentCall.Name, dCtx.agentCall.AgentURL, err.Error())
		aCtx.AddEvent(msg, nil, false)
	} else {
		msg := fmt.Sprintf("Successfully invoked Agent [%s] at URL [%s]. Call Count [%d], Response Count [%d]",
			dCtx.agentCall.Name, dCtx.agentCall.AgentURL, dCtx.tracker.CallCount.Load(), dCtx.tracker.ResponseCount.Load())
		aCtx.AddEvent(msg, nil, false)
	}
	// if respHeaders != nil {
	// 	msg := fmt.Sprintf("Response headers from Agent [%s][%s]", dCtx.agentCall.Name, dCtx.agentCall.AgentURL)
	// 	aCtx.AddEvent(msg, map[string]any{"responseHeaders": respHeaders}, true)
	// }
	aCtx.sendData("Result", result.ToObject())
}

func (ab *AgentBehaviorFederate) callTool(aCtx *AgentContext, dCtx *DelegateCallContext, toolsWG *sync.WaitGroup) {
	defer toolsWG.Done()
	remoteResult, respHeaders, err := ab.invokeMCP(aCtx, dCtx)
	if err != nil {
		aCtx.err = err
		msg := fmt.Sprintf("Failed to invoke MCP tool [%s] at URL [%s] with error: %s", dCtx.toolCall.Tool, dCtx.toolCall.URL, err.Error())
		aCtx.AddEvent(msg, nil, false)
	}
	if respHeaders != nil {
		msg := fmt.Sprintf("MCP tool [%s] sent response headers", dCtx.toolCall.Tool)
		aCtx.AddEvent(msg, map[string]any{"responseHeaders": respHeaders}, true)
	}
	if remoteResult != nil {
		msg := fmt.Sprintf("Successfully invoked MCP tool [%s] at URL [%s]. Call Count [%d], Response Count [%d]",
			dCtx.toolCall.Tool, dCtx.toolCall.URL, dCtx.tracker.CallCount.Load(), dCtx.tracker.ResponseCount.Load())
		aCtx.AddEvent(msg, nil, false)
	}
	aCtx.sendData("Result", remoteResult.ToObject())
	//processMCPCallResults(dCtx.toolCall.Tool, remoteResult, dCtx.results, dCtx.upstreamProgress, ab.agent.Streaming)
}

func (ab *AgentBehaviorFederate) prepareArgs(args *aicommon.ToolCallArgs, forwardHeaders []string) *aicommon.ToolCallArgs {
	newArgs := aicommon.NewCallArgs()
	newArgs.UpdateFrom(args)
	newArgs.Remote.ForwardHeaders = forwardHeaders
	return newArgs
}

func (ab *AgentBehaviorFederate) invokeAgent(aCtx *AgentContext, dCtx *DelegateCallContext) (result *a2aclient.A2AResult, err error) {
	msg := fmt.Sprintf("Agent [%s] Invoking Agent [%s] at URL [%s] with input [%s]", aCtx.agent.ID, dCtx.agentCall.Name, dCtx.agentCall.AgentURL, dCtx.agentCall.Message)
	aCtx.AddEvent(msg, nil, false)
	client := a2aclient.NewA2AClient(ab.agent.Port, ab.agent.ID, dCtx.agentCall.H2, dCtx.agentCall.TLS, dCtx.agentCall.Authority)
	if client == nil {
		return nil, errors.New("failed to create A2A client")
	}
	session, err := client.ConnectWithAgentCard(aCtx.ctx, dCtx.agentCall, dCtx.agentCall.CardURL, dCtx.agentCall.Authority, aCtx.requestHeaders, aCtx.timeline)
	if err != nil {
		return nil, fmt.Errorf("Failed to load agent card for Agent [%s] URL [%s] with error: %s", dCtx.agentCall.Name, dCtx.agentCall.CardURL, err.Error())
	}
	agentResults := map[string][]any{}
	var unaryCallback a2aclient.AgentResultsCallback
	if dCtx.upstreamProgress == nil {
		unaryCallback = func(id, aOutput string, data any) {
			agentResults[id] = append(agentResults[id], aOutput)
			agentResults[id] = append(agentResults[id], data)
		}
	}
	err = session.CallAgent(unaryCallback, aCtx.localProgress, dCtx.upstreamProgress)
	if err != nil {
		msg := fmt.Sprintf("Failed to call Agent [%s] URL [%s] with error: %s", dCtx.agentCall.Name, dCtx.agentCall.AgentURL, err.Error())
		aCtx.AddEvent(msg, nil, false)
		return nil, err
	}
	if aCtx.agentResults != nil && len(agentResults) > 0 {
		aCtx.agentResults[dCtx.agentCall.Name] = agentResults
	}
	return session.Result, nil
}

func (ab *AgentBehaviorFederate) invokeMCP(aCtx *AgentContext, dCtx *DelegateCallContext) (mcpResult *mcpclient.MCPResult, respHeaders http.Header, err error) {
	args := ab.prepareArgs(dCtx.toolCall.Args, dCtx.toolCall.Headers.Request.Forward)
	msg := fmt.Sprintf("Agent [%s] Invoking MCP tool [%s] at URL [%s]", aCtx.agent.ID, dCtx.toolCall.Tool, dCtx.toolCall.URL)
	aCtx.AddEvent(msg, nil, false)
	client := mcpclient.NewClient(ab.agent.Port, false, dCtx.toolCall.H2, dCtx.toolCall.TLS, ab.agent.ID,
		aCtx.rs.ListenerLabel, dCtx.toolCall.Authority, aCtx.localProgress, aCtx.notifyUpdate, aCtx.notifyEndSession)
	session := client.CreateSessionWithTimeline(aCtx.ctx, dCtx.toolCall.URL, dCtx.toolCall.Tool, dCtx.toolCall, aCtx.requestHeaders, aCtx.timeline)
	mcpResult, err = session.CallTool(args)
	respHeaders = mcpResult.LastResponseHeaders
	return
}

func (ab *AgentBehaviorFederate) processLocalUpdates(aCtx *AgentContext, resultsWG *sync.WaitGroup) {
	processResult := func(pair *types.Pair[string, any]) error {
		if pair != nil && pair.Right != nil {
			part := createAnyPart(pair.Left, pair.Right)
			if t, ok := part.(a2aproto.TextPart); ok {
				if err := aCtx.sendDataUpdate(a2aproto.TaskStateWorking, nil, t); err != nil {
					return err
				}
			} else if d, ok := part.(a2aproto.DataPart); ok {
				if err := aCtx.sendArtifact(aCtx.agent.ID, "", "", d.Data, false, false); err != nil {
					return err
				}
			}
		}
		return nil
	}
outer:
	for {
		select {
		case <-aCtx.ctx.Done():
			aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, "Stream was cancelled")
			break outer
		case pair, ok := <-aCtx.localProgress:
			if !ok {
				break outer
			}
			if err := processResult(pair); err != nil {
				break outer
			}
		}
	}
	if resultsWG != nil {
		resultsWG.Done()
	}
}

func (ab *AgentBehaviorFederate) processUpstreamUpdates(aCtx *AgentContext, dCtx *DelegateCallContext, resultsWG *sync.WaitGroup) {
outer:
	for {
		select {
		case <-aCtx.ctx.Done():
			aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, "Stream was cancelled")
			break outer
		case p, ok := <-dCtx.upstreamProgress:
			if !ok {
				break outer
			}
			if p != nil {
				var err error
				if p.Right != nil {
					err = aCtx.notifyUpdate(fmt.Sprintf("%s[%s]", dCtx.name, dCtx.url), p.Right, true)
				} else {
					err = aCtx.notifyUpdate(fmt.Sprintf("%s[%s]: %s", dCtx.name, dCtx.url, p.Left), nil, false)
				}
				if err != nil {
					break outer
				} else {
					dCtx.tracker.IncrementResponse()
				}
			}
		}
	}
	if resultsWG != nil {
		resultsWG.Done()
	}
}

func processMCPCallResults(key string, mcpResult *mcpclient.MCPResult, output map[string]any, resultsChan chan *types.Pair[string, any], isStreamed bool) {
	if mcpResult != nil {
		if isStreamed {
			resultsChan <- types.NewPair[string, any]("Timeline", mcpResult.Timeline)
		} else {
			output["Timeline"] = mcpResult.Timeline
		}
		for i, callResult := range mcpResult.CallResults {
			if !isStreamed {
				callId := fmt.Sprintf("%s.%d", key, i)
				processRemoteContent(callId, callResult.Content, output, resultsChan)
				processRemoteContent(callId, callResult.Data, output, resultsChan)
			}
			if isStreamed {
				if callResult.Data != nil {
					resultsChan <- types.NewPair[string, any](callResult.RequestID+".Data", callResult.Data)
				}
				resultsChan <- types.NewPair[string, any](callResult.RequestID+".Timeline", callResult.RemoteTimeline)
			} else {
				if callResult.Data != nil {
					output[callResult.RequestID+".Data"] = callResult.Data
				}
				output[callResult.RequestID+".Timeline"] = callResult.RemoteTimeline
			}
		}
	}
}

func processRemoteContent(callId string, remoteContent any, output map[string]any, resultsChan chan *types.Pair[string, any]) {
	if remoteContent == nil {
		return
	}
	var content any
	if s, ok := remoteContent.(string); ok {
		if resultsChan != nil {
			resultsChan <- types.NewPair[string, any](callId, s)
		} else {
			content = s
		}
	} else if arr, ok := remoteContent.([]any); ok {
		textResult := []any{}
		for _, val := range arr {
			text := fmt.Sprintf("%+v", val)
			if resultsChan != nil {
				resultsChan <- types.NewPair[string, any](callId, text)
			} else {
				textResult = append(textResult, text)
			}
		}
		content = textResult
	} else if contents, ok := remoteContent.([]mcp.Content); ok {
		textResult := []any{}
		handled := false
		for _, content := range contents {
			if textContent, ok := content.(*mcp.TextContent); ok {
				if textContent.Text != "" {
					handled = true
					if resultsChan != nil {
						resultsChan <- types.NewPair[string, any](callId, textContent.Text)
					} else {
						textResult = append(textResult, textContent.Text)
					}
				}
			} else {
				handled = true
				if resultsChan != nil {
					resultsChan <- types.NewPair[string, any](callId, content)
				} else {
					b, _ := content.MarshalJSON()
					textResult = append(textResult, string(b))
				}
			}
		}
		if len(textResult) > 0 {
			content = textResult
		} else if !handled {
			if resultsChan != nil {
				resultsChan <- types.NewPair(callId, remoteContent)
			} else {
				content = remoteContent
			}
		}
	} else {
		if resultsChan != nil {
			resultsChan <- types.NewPair(callId, remoteContent)
		} else {
			content = remoteContent
		}
	}
	if content != nil {
		output[callId] = map[string]any{"content": content}
	}
}

func processRemoteOtherData(callId string, remoteResult map[string]any, output map[string]any, resultsChan chan *types.Pair[string, any]) {
	for k, v := range remoteResult {
		if resultsChan != nil {
			resultsChan <- types.NewPair[string, any](callId, map[string]any{k: v})
		} else {
			output[k] = v
		}
	}
}

func (ab *AgentBehaviorFederate) prepareDelegates() error {
	if ab.agent.Config == nil || ab.agent.Config.Delegates == nil {
		return nil
	}
	if len(ab.agent.Config.Delegates.Tools) == 0 && len(ab.agent.Config.Delegates.Agents) == 0 {
		return nil
	}
	d := ab.agent.Config.Delegates
	var nilToolCall *model.DelegateToolCall
	var nilAgentCall *model.DelegateAgentCall
	for name, a := range d.Agents {
		if a == nil {
			return fmt.Errorf("Missing agent call spec for [%s]", name)
		}
		a.Tracker = model.NewDelegateTracker()
		if len(a.Triggers) == 0 {
			log.Printf("Agent [%s] has no triggers, will never trigger", name)
		}
		for _, trigger := range a.Triggers {
			triple := types.NewTriple(types.NewPair(trigger, regexp.MustCompile(fmt.Sprintf("(?i)%s%s%s", util.BeforeRegex, trigger, util.AfterRegex))), nilToolCall, a)
			if dInfos := ab.triggers[trigger]; dInfos != nil {
				ab.triggers[trigger] = append(dInfos, triple)
			} else {
				ab.triggers[trigger] = DelegateTriggerArr{triple}
			}
		}
	}
	for name, t := range d.Tools {
		if d == nil {
			return fmt.Errorf("Missing tool call spec for [%s]", name)
		}
		if len(t.Triggers) == 0 {
			log.Printf("Tool [%s] has no triggers, will never trigger", name)
		}
		t.Tracker = model.NewDelegateTracker()
		for _, trigger := range t.Triggers {
			triple := types.NewTriple(types.NewPair(trigger, regexp.MustCompile(fmt.Sprintf("(?i)%s%s%s", util.BeforeRegex, trigger, util.AfterRegex))), t, nilAgentCall)
			if dInfos := ab.triggers[trigger]; dInfos != nil {
				ab.triggers[trigger] = append(dInfos, triple)
			} else {
				ab.triggers[trigger] = DelegateTriggerArr{triple}
			}
		}
	}
	for _, h := range d.HTTP {
		h.Tracker = model.NewDelegateTracker()
	}
	if d.MaxCalls <= 0 {
		d.MaxCalls = 1
	}
	return nil
}
