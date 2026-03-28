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

func (ab *AgentBehaviorFederate) DoUnary(aCtx *AgentContext) (*taskmanager.MessageProcessingResult, error) {
	aCtx.triggers = ab.triggers
	aCtx.detectRemoteCalls()
	aCtx.toolResults = map[string]any{}
	aCtx.agentResults = map[string]any{}
	if len(ab.triggers) == 0 {
		aCtx.toolResults[""] = []any{"Agent update: No tools available"}
	} else if len(aCtx.tools) == 0 {
		aCtx.toolResults[""] = []any{"Agent update: No tools were triggered"}
	} else {
		ab.runTools(aCtx, nil, nil)
		if len(aCtx.toolResults) == 0 {
			aCtx.toolResults[""] = []any{"Agent update: No tool produced any results."}
		}
	}
	if len(ab.triggers) == 0 {
		aCtx.agentResults[""] = []any{"Agent update: No agents available"}
	} else if len(aCtx.agents) == 0 {
		aCtx.agentResults[""] = []any{"Agent update: No agents were triggered"}
	} else {
		ab.runAgents(aCtx, nil, nil)
		if len(aCtx.agentResults) == 0 {
			aCtx.agentResults[""] = []any{"No agent produced any results."}
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
	aCtx.resultsChan = make(chan *types.Pair[string, any], 10)
	aCtx.localProgress = make(chan *types.Pair[string, any], 10)

	runWG := &sync.WaitGroup{}
	resultsWG := &sync.WaitGroup{}
	if len(ab.triggers) == 0 {
		aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, "Agent update: No tools available", nil)
	} else if len(aCtx.tools) == 0 {
		aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, "Agent update: No tools were triggered", nil)
	} else {
		runWG.Add(1)
		resultsWG.Add(1)
		go ab.runTools(aCtx, runWG, resultsWG)
	}
	if len(ab.triggers) == 0 {
		aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, "Agent update: No agents available", nil)
	} else if len(aCtx.agents) == 0 {
		aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, "Agent update: No agents were triggered", nil)
	} else {
		runWG.Add(1)
		resultsWG.Add(1)
		go ab.runAgents(aCtx, runWG, resultsWG)
	}
	runWG.Wait()
	close(aCtx.resultsChan)
	close(aCtx.localProgress)
	resultsWG.Wait()
	if aCtx.err != nil {
		return "Agent federation done with error \u274C", aCtx.err
	}
	return "Agent federation done \u2705", nil
}

func (ab *AgentBehaviorFederate) processResults(aCtx *AgentContext, dCtx *DelegateCallContext, resultsWG *sync.WaitGroup) {
	channelsWG := sync.WaitGroup{}
	channelsWG.Add(1)
	processResult := func(pair *types.Pair[string, any], sendArtifact bool) error {
		if pair != nil && pair.Right != nil {
			callId := fmt.Sprintf("Upstream [%s] Result:", pair.Left)
			parts := createAnyParts(callId, pair.Right)
			textParts := []a2aproto.Part{}
			dataParts := []a2aproto.Part{}
			for _, part := range parts {
				if t, ok := part.(a2aproto.TextPart); ok {
					textParts = append(textParts, t)
				} else if t, ok := part.(a2aproto.DataPart); ok {
					dataParts = append(dataParts, t)
				}
			}
			var err error
			if len(textParts) > 0 {
				if sendArtifact {
					if err = aCtx.sendTextArtifactFromParts(dCtx.name, dCtx.url, textParts, false, false); err != nil {
						return err
					}
				} else {
					if err = aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, "", textParts); err != nil {
						return err
					}
				}
			}
			if len(dataParts) > 0 {
				if err = aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, "", dataParts); err != nil {
					return err
				}
			}
		}
		return nil
	}
	go func() {
	outer:
		for {
			select {
			case <-aCtx.ctx.Done():
				aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, "Stream was cancelled", nil)
				break outer
			case update, ok := <-dCtx.upstreamProgress:
				if !ok {
					break outer
				}
				if update != "" {
					if err := aCtx.sendTextArtifact(dCtx.name, dCtx.url, []string{update}, false, false); err != nil {
						break outer
					} else {
						dCtx.tracker.IncrementResponse()
					}
				}
			}
		}
		channelsWG.Done()
	}()
	channelsWG.Add(1)
	go func() {
	outer:
		for {
			select {
			case <-aCtx.ctx.Done():
				aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, "Stream was cancelled", nil)
				break outer
			case pair, ok := <-aCtx.localProgress:
				if !ok {
					break outer
				}
				if err := processResult(pair, false); err != nil {
					break outer
				}
			}
		}
		channelsWG.Done()
	}()
	channelsWG.Add(1)
	go func() {
	outer:
		for {
			select {
			case <-aCtx.ctx.Done():
				aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, "Stream was cancelled", nil)
				break outer
			case pair, ok := <-aCtx.resultsChan:
				if !ok {
					break outer
				}
				if err := processResult(pair, true); err != nil {
					break outer
				}
			}
		}
		channelsWG.Done()
	}()
	channelsWG.Wait()
	if resultsWG != nil {
		resultsWG.Done()
	}
}

func (ab *AgentBehaviorFederate) runTools(aCtx *AgentContext, runWG, resultsWG *sync.WaitGroup) {
	parallel := ab.agent.Config.Delegates.Parallel
	toolsWG := &sync.WaitGroup{}
	delegateContexts := []*DelegateCallContext{}
	for _, tcalls := range aCtx.tools {
		for _, tc := range tcalls {
			log.Printf("Processing tool call [%s] at URL [%s]", tc.ToolCall.Tool, tc.ToolCall.URL)
			if tc.ToolCall.Headers == nil {
				tc.ToolCall.Headers = types.NewHeaders()
			}
			tc.ToolCall.Headers.NonNil()
			dCtx := newDelegateCallContext(tc.ToolCall, nil, tc.Tracker)
			dCtx.tracker.IncrementCall()
			delegateContexts = append(delegateContexts, dCtx)
			if resultsWG != nil {
				go ab.processResults(aCtx, dCtx, resultsWG)
			}
			log.Printf("Calling tool [%s] at URL [%s]", dCtx.toolCall.Tool, dCtx.toolCall.URL)
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
				go ab.processResults(aCtx, dCtx, resultsWG)
			}
			log.Printf("Calling agent [%s] at URL [%s]", dCtx.agentCall.Name, dCtx.agentCall.AgentURL)
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
	respHeaders, err := ab.invokeAgent(aCtx, dCtx)
	if err != nil {
		aCtx.err = err
		msg := fmt.Sprintf("Failed to invoke Agent [%s] at URL [%s] with error: %s", dCtx.agentCall.Name, dCtx.agentCall.AgentURL, err.Error())
		aCtx.Log(err.Error())
		if !aCtx.ReportProgress(dCtx.agentCall.Name, msg) {
			dCtx.results["RemoteCallError"] = msg
		}
	} else {
		msg := fmt.Sprintf("Successfully invoked Agent [%s] at URL [%s]. Call Count [%d], Response Count [%d]",
			dCtx.agentCall.Name, dCtx.agentCall.AgentURL, dCtx.tracker.CallCount.Load(), dCtx.tracker.ResponseCount.Load())
		aCtx.Log(msg)
		if !aCtx.ReportProgress(dCtx.agentCall.Name, msg) {
			dCtx.results["RemoteCallResult"] = msg
		}
	}
	if respHeaders != nil {
		msg := fmt.Sprintf("Agent [%s][%s] sent response headers: %s", dCtx.agentCall.Name, dCtx.agentCall.AgentURL, util.ToPrettyJSONText(respHeaders))
		aCtx.Log(msg)
		if !aCtx.ReportProgress(dCtx.agentCall.Name, msg) {
			dCtx.results["RemoteResponseHeaders"] = msg
		}
	}
	if aCtx.agentResults != nil {
		aCtx.agentResults[dCtx.agentCall.Name] = dCtx.results
	}
}

func (ab *AgentBehaviorFederate) callTool(aCtx *AgentContext, dCtx *DelegateCallContext, toolsWG *sync.WaitGroup) {
	defer toolsWG.Done()
	remoteResult, respHeaders, err := ab.invokeMCP(aCtx, dCtx)
	if err != nil {
		aCtx.err = err
		msg := fmt.Sprintf("Failed to invoke MCP tool [%s] at URL [%s] with error: %s", dCtx.toolCall.Tool, dCtx.toolCall.URL, err.Error())
		aCtx.Log(msg)
		if remoteResult == nil {
			remoteResult = map[string]any{}
		}
		if !aCtx.ReportProgress(dCtx.toolCall.Tool, msg) {
			dCtx.results["RemoteCallError"] = msg
		}
	}
	if respHeaders != nil {
		msg := fmt.Sprintf("MCP tool [%s] sent response headers: %s", dCtx.toolCall.Tool, util.ToPrettyJSONText(respHeaders))
		aCtx.Log(msg)
		if !aCtx.ReportProgress(dCtx.toolCall.Tool, msg) {
			dCtx.results["RemoteResponseHeaders"] = msg
		}
	}
	if remoteResult != nil {
		msg := fmt.Sprintf("Successfully invoked MCP tool [%s] at URL [%s]. Call Count [%d], Response Count [%d]",
			dCtx.toolCall.Tool, dCtx.toolCall.URL, dCtx.tracker.CallCount.Load(), dCtx.tracker.ResponseCount.Load())
		aCtx.Log(msg)
		if !aCtx.ReportProgress(dCtx.toolCall.Tool, msg) {
			dCtx.results["RemoteCallResult"] = msg
		}
	}
	if remoteResult["content"] != nil {
		processMCPContent(dCtx.toolCall.Tool, remoteResult, dCtx.results, aCtx.resultsChan)
		delete(remoteResult, "content")
	}
	if remoteResult["structuredContent"] != nil {
		if aCtx.resultsChan != nil {
			aCtx.resultsChan <- types.NewPair(dCtx.toolCall.Tool, remoteResult["structuredContent"])
		} else {
			dCtx.results["upstreamContent"] = remoteResult["structuredContent"]
		}
		delete(remoteResult, "structuredContent")
	}
	for k, v := range remoteResult {
		if aCtx.resultsChan != nil {
			aCtx.resultsChan <- types.NewPair[string, any](dCtx.toolCall.Tool, map[string]any{k: v})
		} else {
			dCtx.results[k] = v
		}
	}
	if aCtx.toolResults != nil {
		aCtx.toolResults[dCtx.toolCall.Tool] = dCtx.results
	}
}

func (ab *AgentBehaviorFederate) prepareArgs(args map[string]any, forwardHeaders []string) map[string]any {
	newArgs := map[string]any{
		"forwardHeaders": forwardHeaders,
	}
	for k, v := range args {
		newArgs[k] = v
	}
	return newArgs
}

func (ab *AgentBehaviorFederate) invokeAgent(aCtx *AgentContext, dCtx *DelegateCallContext) (respHeaders http.Header, err error) {
	msg := fmt.Sprintf("Agent [%s] Invoking Agent [%s] at URL [%s] with input [%s]", aCtx.agent.ID, dCtx.agentCall.Name, dCtx.agentCall.AgentURL, dCtx.agentCall.Message)
	aCtx.Log(msg)
	if !aCtx.ReportProgress(dCtx.agentCall.Name, msg) {
		aCtx.agentResults[dCtx.agentCall.Name] = msg
	}
	client := a2aclient.NewA2AClient(ab.agent.Port, ab.agent.ID, dCtx.agentCall.H2, dCtx.agentCall.TLS, dCtx.agentCall.Authority)
	if client == nil {
		return nil, errors.New("failed to create A2A client")
	}
	session, err := client.ConnectWithAgentCard(aCtx.ctx, dCtx.agentCall, dCtx.agentCall.CardURL, dCtx.agentCall.Authority, aCtx.requestHeaders)
	if err != nil {
		return nil, fmt.Errorf("Failed to load agent card for Agent [%s] URL [%s] with error: %s", dCtx.agentCall.Name, dCtx.agentCall.CardURL, err.Error())
	}
	agentResults := map[string][]string{}
	var unaryCallback a2aclient.AgentResultsCallback
	if aCtx.resultsChan == nil {
		unaryCallback = func(id, aOutput string) {
			agentResults[id] = append(agentResults[id], aOutput)
		}
	}
	err = session.CallAgent(unaryCallback, aCtx.resultsChan, aCtx.localProgress, dCtx.upstreamProgress)
	if err != nil {
		if !aCtx.ReportProgress(dCtx.agentCall.Name, err.Error()) {
			agentResults[""] = []string{err.Error()}
		}
		return session.ResponseHeaders, fmt.Errorf("Failed to call Agent [%s] URL [%s]", dCtx.agentCall.Name, dCtx.agentCall.AgentURL)
	}
	if aCtx.agentResults != nil && len(agentResults) > 0 {
		aCtx.agentResults[dCtx.agentCall.Name] = agentResults
	}
	return session.ResponseHeaders, nil
}

func (ab *AgentBehaviorFederate) invokeMCP(aCtx *AgentContext, dCtx *DelegateCallContext) (remoteResult map[string]any, respHeaders http.Header, err error) {
	args := ab.prepareArgs(dCtx.toolCall.Args, dCtx.toolCall.Headers.Request.Forward)
	msg := fmt.Sprintf("Agent [%s] Invoking MCP tool [%s] at URL [%s]", aCtx.agent.ID, dCtx.toolCall.Tool, dCtx.toolCall.URL)
	aCtx.Log(msg)
	log.Println("AgentBehaviorFederate: " + msg)
	aCtx.ReportProgress(dCtx.toolCall.Tool, msg)
	client := mcpclient.NewClient(ab.agent.Port, false, dCtx.toolCall.H2, dCtx.toolCall.TLS, ab.agent.ID, aCtx.rs.ListenerLabel, dCtx.toolCall.Authority, dCtx.upstreamProgress)
	session := client.CreateSessionWithHops(dCtx.toolCall.URL, dCtx.toolCall.Tool, aCtx.hops)
	session.SetCallContext(dCtx.toolCall, aCtx.requestHeaders)
	err = session.Connect()
	if err == nil {
		defer session.Close()
		remoteResult, err = session.Call(args, aCtx.requestHeaders)
		respHeaders = session.ResponseHeaders
	}
	return
}

func processMCPContent(key string, remoteResult map[string]any, output map[string]any, resultsChan chan *types.Pair[string, any]) {
	if s, ok := remoteResult["content"].(string); ok {
		if resultsChan != nil {
			resultsChan <- types.NewPair[string, any](key, s)
		} else {
			output["content"] = s
		}
	} else if arr, ok := remoteResult["content"].([]any); ok {
		textResult := []any{}
		for _, val := range arr {
			text := fmt.Sprintf("%+v", val)
			if resultsChan != nil {
				resultsChan <- types.NewPair[string, any](key, text)
			} else {
				textResult = append(textResult, text)
			}
		}
		output["content"] = textResult
	} else if contents, ok := remoteResult["content"].([]mcp.Content); ok {
		textResult := []any{}
		handled := false
		for _, content := range contents {
			if textContent, ok := content.(*mcp.TextContent); ok {
				if textContent.Text != "" {
					handled = true
					if resultsChan != nil {
						resultsChan <- types.NewPair[string, any](key, textContent.Text)
					} else {
						textResult = append(textResult, textContent.Text)
					}
				}
			} else {
				handled = true
				if resultsChan != nil {
					resultsChan <- types.NewPair[string, any](key, content)
				} else {
					b, _ := content.MarshalJSON()
					textResult = append(textResult, string(b))
				}
			}
		}
		if len(textResult) > 0 {
			output["content"] = textResult
		} else if !handled {
			if resultsChan != nil {
				resultsChan <- types.NewPair(key, remoteResult["content"])
			} else {
				output["content"] = remoteResult["content"]
			}
		}
	} else {
		if resultsChan != nil {
			resultsChan <- types.NewPair(key, remoteResult["content"])
		} else {
			output["content"] = remoteResult["content"]
		}
	}
}
