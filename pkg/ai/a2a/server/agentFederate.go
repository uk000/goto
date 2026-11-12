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

// DoUnary runs the tools and agents that the input triggers and responds with a message of all their results.
func (ab *AgentBehaviorFederate) DoUnary(aCtx *AgentContext) (*taskmanager.MessageProcessingResult, error) {
	aCtx.triggers = ab.triggers
	aCtx.detectRemoteCalls()
	aCtx.toolResults = map[string]any{}
	aCtx.agentResults = map[string]any{}
	if len(ab.triggers) == 0 {
		aCtx.AddEvent("Agent update: No tools available")
		aCtx.toolResults["No Tools"] = "Agent update: No tools available"
	} else if len(aCtx.tools) == 0 {
		aCtx.AddEvent("Agent update: No tools were triggered")
		aCtx.toolResults["No Tools"] = "Agent update: No tools were triggered"
	} else {
		ab.runTools(aCtx, nil, nil)
		if len(aCtx.toolResults) == 0 {
			aCtx.AddEvent("Agent update: No tool produced any results")
			aCtx.toolResults["No Tools"] = "Agent update: No tool produced any results"
		}
	}
	if len(ab.triggers) == 0 {
		aCtx.AddEvent("Agent update: No agents available")
		aCtx.agentResults["No Agents"] = "Agent update: No agents available"
	} else if len(aCtx.agents) == 0 {
		aCtx.AddEvent("Agent update: No agents were triggered")
		aCtx.agentResults["No Agents"] = "Agent update: No agents were triggered"
	} else {
		ab.runAgents(aCtx, nil, nil)
		if len(aCtx.agentResults) == 0 {
			aCtx.AddEvent("Agent update: No agent produced any results")
			aCtx.agentResults["No Agents"] = "No agent produced any results"
		}
	}
	result := createHybridMessage(aCtx.agent.ID, aCtx.toolResults, aCtx.agentResults, aCtx.upstreamHeaders)
	return &taskmanager.MessageProcessingResult{
		Result: &result,
	}, nil
}

// DoStream runs the tools and agents that the input triggers, and relays their progress to the client until they are done.
func (ab *AgentBehaviorFederate) DoStream(aCtx *AgentContext) (string, error) {
	aCtx.triggers = ab.triggers
	aCtx.detectRemoteCalls()
	aCtx.localProgress = make(chan *types.Pair[string, any], 10)
	aCtx.timeline.SetStreamPreferred(aCtx.localProgress)

	runWG := &sync.WaitGroup{}
	resultsWG := &sync.WaitGroup{}
	if len(ab.triggers) == 0 {
		aCtx.AddEvent("Agent update: Nothing triggered")
	} else {
		resultsWG.Add(1)
		go ab.processLocalUpdates(aCtx, resultsWG)
		if len(aCtx.tools) == 0 {
			aCtx.AddEvent("Agent update: No tools were triggered")
		} else {
			runWG.Add(1)
			go ab.runTools(aCtx, runWG, resultsWG)
		}
		if len(aCtx.agents) == 0 {
			aCtx.AddEvent("Agent update: No agents were triggered")
		} else {
			runWG.Add(1)
			go ab.runAgents(aCtx, runWG, resultsWG)
		}
		runWG.Wait()
		aCtx.localProgress <- types.NewStringAnyPair("UpstreamHeaders", aCtx.upstreamHeaders)
		close(aCtx.localProgress)
		resultsWG.Wait()
	}
	if err := aCtx.getErr(); !util.IsNil(err) {
		return "Agent federation done with error \u274C", err
	}
	return "Agent federation done \U000026F3", nil
}

// runTools calls the triggered tools, in parallel if configured, and tracks their calls; it signals runWG when done.
func (ab *AgentBehaviorFederate) runTools(aCtx *AgentContext, runWG, resultsWG *sync.WaitGroup) {
	parallel := ab.agent.Config.Delegates.Parallel
	toolsWG := &sync.WaitGroup{}
	delegateContexts := []*DelegateCallContext{}
	for _, tcalls := range aCtx.tools {
		for _, tc := range tcalls {
			if tc.ToolCall.Stream {
				aCtx.irw.HoldChunked = false
			}
			if tc.ToolCall.Headers == nil {
				tc.ToolCall.Headers = types.NewHeaders()
			}
			tc.ToolCall.Headers.NonNil()
			dCtx := newDelegateCallContext(tc.ToolCall, nil, tc.Tracker)
			dCtx.tracker.IncrementCall()
			delegateContexts = append(delegateContexts, dCtx)
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
	}
	if runWG != nil {
		runWG.Done()
	}
}

// runAgents calls the triggered agents, in parallel if configured, and signals runWG when done.
func (ab *AgentBehaviorFederate) runAgents(aCtx *AgentContext, runWG, resultsWG *sync.WaitGroup) {
	parallel := ab.agent.Config.Delegates.Parallel
	agentsWG := &sync.WaitGroup{}
	delegateContexts := []*DelegateCallContext{}
	for _, acalls := range aCtx.agents {
		for _, a := range acalls {
			a.AgentCall.NonNil()
			if a.AgentCall.Stream {
				aCtx.irw.HoldChunked = false
			}
			dCtx := newDelegateCallContext(nil, a.AgentCall, a.Tracker)
			dCtx.tracker.IncrementCall()
			delegateContexts = append(delegateContexts, dCtx)
			if resultsWG != nil {
				resultsWG.Add(1)
				go ab.processUpstreamUpdates(aCtx, dCtx, resultsWG)
			} else {
				// Nobody reads the progress of a unary call, which would stop the call after the first few updates.
				// Its results are collected by the callback instead.
				dCtx.upstreamProgress = nil
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
	}
	if runWG != nil {
		runWG.Done()
	}
}

// callAgent invokes an agent delegate, records its error and events, and sends its result and what the upstream reported.
func (ab *AgentBehaviorFederate) callAgent(aCtx *AgentContext, dCtx *DelegateCallContext, agentsWG *sync.WaitGroup) {
	defer agentsWG.Done()
	result, err := ab.invokeAgent(aCtx, dCtx)
	if !util.IsNil(err) {
		aCtx.setErr(err)
		msg := fmt.Sprintf("Failed to invoke Agent [%s] at URL [%s] with error: %s", dCtx.agentCall.Name, dCtx.agentCall.AgentURL, err.Error())
		aCtx.AddEvent(msg)
		log.Println(msg)
	} else {
		msg := fmt.Sprintf("Successfully invoked Agent [%s] at URL [%s]. (Tracker: Call#[%d], Response#[%d])",
			dCtx.agentCall.Name, dCtx.agentCall.AgentURL, dCtx.tracker.CallCount.Load(), dCtx.tracker.ResponseCount.Load())
		aCtx.AddEvent(msg)
		log.Println(msg)
	}
	if result != nil {
		aCtx.sendData("Result", result.Snapshot())
		aCtx.recordUpstream("A2A", dCtx.name, result.RemoteGotos, result.LastRemoteHeaders,
			result.LastRequestHeaders, result.LastResponseHeaders, result.UpstreamStatuses, result.LastResponseStatus)
	}
}

// callTool invokes an MCP tool delegate, records its error and events, and sends its result and what the upstream reported.
func (ab *AgentBehaviorFederate) callTool(aCtx *AgentContext, dCtx *DelegateCallContext, toolsWG *sync.WaitGroup) {
	defer toolsWG.Done()
	result, respHeaders, err := ab.invokeMCP(aCtx, dCtx)
	if !util.IsNil(err) {
		aCtx.setErr(err)
		msg := fmt.Sprintf("Failed to invoke MCP tool [%s] at URL [%s] with error: %s", dCtx.toolCall.Tool, dCtx.toolCall.URL, err.Error())
		aCtx.AddEvent(msg)
		log.Println(msg)
	}
	if respHeaders != nil {
		msg := fmt.Sprintf("MCP tool [%s] sent response headers", dCtx.toolCall.Tool)
		aCtx.AddEvent(msg)
	}
	if result != nil {
		msg := fmt.Sprintf("Successfully invoked MCP tool [%s] at URL [%s]. Call Count [%d], Response Count [%d]",
			dCtx.toolCall.Tool, dCtx.toolCall.URL, dCtx.tracker.CallCount.Load(), dCtx.tracker.ResponseCount.Load())
		aCtx.AddEvent(msg)
		log.Println(msg)
		aCtx.sendData("Result", result.ToObject())
		aCtx.recordUpstream("MCP", dCtx.name, result.RemoteGotos, result.LastRemoteHeaders,
			result.LastRequestHeaders, result.LastResponseHeaders, result.UpstreamStatuses, result.LastResponseStatus)
	}
}

// prepareArgs makes the args of a tool call from its configured args and the input args.
func (ab *AgentBehaviorFederate) prepareArgs(args map[string]any, tcArgs *aicommon.ToolCallArgs, forwardHeaders []string) *aicommon.ToolCallArgs {
	newArgs := aicommon.NewCallArgs()
	newArgs.UpdateFrom(tcArgs)
	newArgs.UpdateFromInputArgs(args)
	newArgs.RemoteArgs.ForwardHeaders = forwardHeaders
	return newArgs
}

// invokeAgent connects to the agent delegate by its card and calls it with the message.
func (ab *AgentBehaviorFederate) invokeAgent(aCtx *AgentContext, dCtx *DelegateCallContext) (result *a2aclient.A2AResult, err error) {
	msg := fmt.Sprintf("Agent [%s] Invoking Agent [%s] at URL [%s] with input [%s]", aCtx.agent.ID, dCtx.agentCall.Name, dCtx.agentCall.AgentURL, dCtx.agentCall.Message)
	aCtx.AddEvent(msg)
	if aCtx.timeline.ResultOnly {
		dCtx.agentCall.ResultOnly = true
	}
	if aCtx.timeline.NoEvents {
		dCtx.agentCall.NoEvents = true
	}
	client := a2aclient.NewA2AClient(ab.agent.Port, ab.agent.ID, dCtx.agentCall.H2, dCtx.agentCall.TLS, dCtx.agentCall.Authority, dCtx.agentCall.RequestTimeoutD)
	if client == nil {
		dCtx.closeUpstreamProgress()
		return nil, errors.New("failed to create A2A client")
	}
	session, err := client.ConnectWithAgentCard(aCtx.ctx, dCtx.agentCall, dCtx.agentCall.CardURL, dCtx.agentCall.Authority, aCtx.requestHeaders, aCtx.timeline)
	if !util.IsNil(err) {
		// The call that closes the progress is not made, and what reads the progress waits for that.
		dCtx.closeUpstreamProgress()
		return nil, fmt.Errorf("Failed to load agent card for Agent [%s] URL [%s] with error: %s", dCtx.agentCall.Name, dCtx.agentCall.CardURL, err.Error())
	}
	agentResults := map[string][]any{}
	var unaryCallback a2aclient.AgentResultsCallback
	if dCtx.upstreamProgress == nil {
		var callbackLock sync.Mutex
		unaryCallback = func(id, aOutput string, data any) {
			callbackLock.Lock()
			defer callbackLock.Unlock()
			agentResults[id] = append(agentResults[id], aOutput)
			agentResults[id] = append(agentResults[id], data)
		}
	}
	agentMessage := dCtx.agentCall.Message
	if agentMessage == "" {
		agentMessage = aCtx.inputText
	}
	err = session.CallAgent(agentMessage, aCtx.inputData, unaryCallback, aCtx.localProgress, dCtx.upstreamProgress)
	if aCtx.agentResults != nil && len(agentResults) > 0 {
		aCtx.setAgentResults(dCtx.agentCall.Name, agentResults)
	}
	if session.Result != nil {
		result = session.Result
	}
	if !util.IsNil(err) {
		msg := fmt.Sprintf("Failed to call Agent [%s] URL [%s] with error: %s", dCtx.agentCall.Name, dCtx.agentCall.AgentURL, err.Error())
		aCtx.AddEvent(msg)
	}
	return
}

// invokeMCP calls the MCP tool delegate with the args from the input, and returns its result and response headers.
func (ab *AgentBehaviorFederate) invokeMCP(aCtx *AgentContext, dCtx *DelegateCallContext) (mcpResult *mcpclient.MCPResult, respHeaders http.Header, err error) {
	if aCtx.timeline.ResultOnly {
		dCtx.toolCall.ResultOnly = true
	}
	if aCtx.timeline.NoEvents {
		dCtx.toolCall.NoEvents = true
	}
	args := ab.prepareArgs(aCtx.inputData, dCtx.toolCall.Args, dCtx.toolCall.Headers.Request.Forward)
	msg := fmt.Sprintf("Agent [%s] Invoking MCP tool [%s] at URL [%s]", aCtx.agent.ID, dCtx.toolCall.Tool, dCtx.toolCall.URL)
	aCtx.AddEvent(msg)
	log.Println(msg)
	client := mcpclient.NewClient(ab.agent.Port, false, dCtx.toolCall.H2, dCtx.toolCall.TLS, ab.agent.ID,
		aCtx.rs.ListenerLabel, dCtx.toolCall.Authority, dCtx.toolCall.RequestTimeoutD, aCtx.localProgress, aCtx.notifyUpdate, aCtx.notifyEndSession)
	session := client.CreateSessionWithTimeline(aCtx.ctx, dCtx.toolCall.URL, dCtx.toolCall.Tool, dCtx.toolCall, aCtx.requestHeaders, aCtx.timeline)
	mcpResult, err = session.CallTool(args)
	if mcpResult != nil {
		respHeaders = mcpResult.LastResponseHeaders
	}
	return
}

// processLocalUpdates relays the local progress to the client until it ends, the task is cancelled or sending fails.
func (ab *AgentBehaviorFederate) processLocalUpdates(aCtx *AgentContext, resultsWG *sync.WaitGroup) {
	processResult := func(pair *types.Pair[string, any]) error {
		if pair != nil && pair.Right != nil {
			if s, ok := pair.Right.(string); ok {
				if err := aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, s); err != nil {
					return err
				}
			} else {
				if err := aCtx.sendData(pair.Left, pair.Right); err != nil {
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
			// The ones that send the progress wait for it to be read until they are done.
			for range aCtx.localProgress {
			}
			break outer
		case pair, ok := <-aCtx.localProgress:
			if !ok {
				break outer
			}
			if err := processResult(pair); err != nil {
				aCtx.setErr(err)
				// The ones that send the progress wait for it to be read until they are done.
				for range aCtx.localProgress {
				}
				break outer
			}
		}
	}
	if resultsWG != nil {
		resultsWG.Done()
	}
}

// processUpstreamUpdates relays an agent call's progress to the client until it ends, the task is cancelled or sending fails.
func (ab *AgentBehaviorFederate) processUpstreamUpdates(aCtx *AgentContext, dCtx *DelegateCallContext, resultsWG *sync.WaitGroup) {
outer:
	for {
		select {
		case <-aCtx.ctx.Done():
			aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, "Stream was cancelled")
			// The call that sends the progress waits for it to be read until it is done.
			for range dCtx.upstreamProgress {
			}
			break outer
		case p, ok := <-dCtx.upstreamProgress:
			if !ok {
				break outer
			}
			if p != nil {
				var err error
				if p.Right != nil {
					err = aCtx.notifyUpdate(p.Left, p.Right, true)
				} else {
					err = aCtx.notifyUpdate(fmt.Sprintf("%s[%s]: %s", dCtx.name, dCtx.url, p.Left), nil, false)
				}
				if err != nil {
					aCtx.setErr(err)
					// The call that sends the progress waits for it to be read until it is done.
					for range dCtx.upstreamProgress {
					}
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

// processMCPCallResults puts the timeline and call results of an MCP result in the output, or the channel if streamed.
func processMCPCallResults(key string, mcpResult *mcpclient.MCPResult, output map[string]any, resultsChan chan *types.Pair[string, any], isStreamed bool) {
	if mcpResult != nil {
		if isStreamed {
			resultsChan <- types.NewPair[string, any]("Timeline", mcpResult.Timeline)
		} else {
			output["Timeline"] = mcpResult.Timeline
		}
		for i, callResult := range mcpResult.CallResults {
			if !isStreamed {
				callId := fmt.Sprintf("%s.%s", key, i)
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

// processRemoteContent turns remote result content into text or data, sent to the channel if there is one, else the output.
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

// processRemoteOtherData sends each entry of the remote result to the channel if there is one, else adds it to the output.
func processRemoteOtherData(callId string, remoteResult map[string]any, output map[string]any, resultsChan chan *types.Pair[string, any]) {
	for k, v := range remoteResult {
		if resultsChan != nil {
			resultsChan <- types.NewPair[string, any](callId, map[string]any{k: v})
		} else {
			output[k] = v
		}
	}
}

// prepareDelegates validates the delegates, gives them trackers, and indexes the tools and agents by their triggers.
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
		if a == nil || a.AgentCall == nil {
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
		if t == nil || t.ToolCall == nil {
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
	for name, h := range d.HTTP {
		if h == nil {
			return fmt.Errorf("Missing http call spec for [%s]", name)
		}
		h.Tracker = model.NewDelegateTracker()
	}
	if d.MaxCalls <= 0 {
		d.MaxCalls = 1
	}
	return nil
}
