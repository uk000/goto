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
	"regexp"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	a2aproto "trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
)

type AgentBehaviorFederate struct {
	*AgentBehaviorImpl
	triggers DelegateTriggers
}

func (bd *AgentBehaviorFederate) prepareDelegates() error {
	if bd.agent.Config == nil || bd.agent.Config.Delegates == nil {
		return nil
	}
	if len(bd.agent.Config.Delegates.Tools) == 0 && len(bd.agent.Config.Delegates.Agents) == 0 {
		return nil
	}
	d := bd.agent.Config.Delegates
	var nilToolCall *model.DelegateToolCall
	var nilAgentCall *model.DelegateAgentCall
	for name, a := range d.Agents {
		if a == nil {
			return fmt.Errorf("Missing agent call spec for [%s]", name)
		}
		if len(a.Triggers) == 0 {
			log.Printf("Agent [%s] has no triggers, will never trigger", name)
		}
		for _, trigger := range a.Triggers {
			if triple := bd.triggers[trigger]; triple != nil {
				triple.Third = a
			} else {
				bd.triggers[trigger] = types.NewTriple(regexp.MustCompile(fmt.Sprintf("(?i)%s%s%s", util.BeforeRegex, trigger, util.AfterRegex)), nilToolCall, a)
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
		for _, trigger := range t.Triggers {
			if triple := bd.triggers[trigger]; triple != nil {
				triple.Second = t
			} else {
				bd.triggers[trigger] = types.NewTriple(regexp.MustCompile(fmt.Sprintf("(?i)%s%s%s", util.BeforeRegex, trigger, util.AfterRegex)), t, nilAgentCall)
			}
		}
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
		ab.runTools(aCtx, nil)
		if len(aCtx.toolResults) == 0 {
			aCtx.toolResults[""] = []any{"Agent update: No tool produced any results."}
		}
	}
	if len(ab.triggers) == 0 {
		aCtx.agentResults[""] = []any{"Agent update: No agents available"}
	} else if len(aCtx.agents) == 0 {
		aCtx.agentResults[""] = []any{"Agent update: No agents were triggered"}
	} else {
		ab.runAgents(aCtx, nil)
		if len(aCtx.agentResults) == 0 {
			aCtx.agentResults[""] = []any{"No agent produced any results."}
		}
	}
	result := createHybridMessage(aCtx.toolResults, aCtx.agentResults)
	return &taskmanager.MessageProcessingResult{
		Result: &result,
	}, nil
}

func (ab *AgentBehaviorFederate) DoStream(aCtx *AgentContext) error {
	aCtx.triggers = ab.triggers
	aCtx.detectRemoteCalls()
	aCtx.resultsChan = make(chan *types.Pair[string, any], 10)
	aCtx.upstreamProgress = make(chan string, 10)
	aCtx.localProgress = make(chan *types.Pair[string, any], 10)

	runWG := sync.WaitGroup{}
	resultsWG := sync.WaitGroup{}
	if len(ab.triggers) == 0 {
		aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, "Agent update: No tools available", nil)
	} else if len(aCtx.tools) == 0 {
		aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, "Agent update: No tools were triggered", nil)
	} else {
		runWG.Add(1)
		resultsWG.Add(1)
		go ab.processResults(aCtx, "tool", &resultsWG)
		go ab.runTools(aCtx, &runWG)
	}
	if len(ab.triggers) == 0 {
		aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, "Agent update: No agents available", nil)
	} else if len(aCtx.agents) == 0 {
		aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, "Agent update: No agents were triggered", nil)
	} else {
		runWG.Add(1)
		resultsWG.Add(1)
		go ab.processResults(aCtx, "agent", &resultsWG)
		go ab.runAgents(aCtx, &runWG)
	}
	runWG.Wait()
	close(aCtx.resultsChan)
	close(aCtx.localProgress)
	close(aCtx.upstreamProgress)
	resultsWG.Wait()

	return nil
}

func (ab *AgentBehaviorFederate) processResults(aCtx *AgentContext, dType string, wg *sync.WaitGroup) {
	channelsWG := sync.WaitGroup{}
	channelsWG.Add(1)
	go func() {
	outer:
		for {
			select {
			case <-aCtx.ctx.Done():
				aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, "Stream was cancelled", nil)
				break outer
			case update, ok := <-aCtx.upstreamProgress:
				if !ok {
					break outer
				}
				if update != "" {
					callId := fmt.Sprintf("Upstream %s update", dType)
					aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, fmt.Sprintf("%s: %s", callId, update), nil)
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
				if pair != nil && pair.Right != nil {
					callId := fmt.Sprintf("Agent update [%s]", ab.agent.ID)
					aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, fmt.Sprintf("%s: %s", callId, pair.Right), nil)
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
				if pair != nil && pair.Right != nil {
					callId := fmt.Sprintf("Upstream [%s] Result:", pair.Left)
					aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, "", createAnyParts(callId, pair.Right))
				}
			}
		}
		channelsWG.Done()
	}()
	channelsWG.Wait()
	wg.Done()
}

func (ab *AgentBehaviorFederate) runTools(aCtx *AgentContext, wg *sync.WaitGroup) {
	parallel := ab.agent.Config.Delegates.Parallel
	wg2 := sync.WaitGroup{}
	for _, tc := range aCtx.tools {
		dCtx := &DelegateCallContext{
			toolCall:       &tc.ToolCall,
			configHeaders:  tc.ToolCall.Headers,
			forwardHeaders: tc.ToolCall.ForwardHeaders,
			removeHeaders:  tc.ToolCall.RemoveHeaders,
		}
		if parallel {
			wg2.Add(1)
			go func() {
				ab.callTool(aCtx, dCtx)
				wg2.Done()
			}()
		} else {
			ab.callTool(aCtx, dCtx)
		}
	}
	if parallel {
		wg2.Wait()
	}
	if wg != nil {
		wg.Done()
	}
}

func (ab *AgentBehaviorFederate) runAgents(aCtx *AgentContext, wg *sync.WaitGroup) {
	parallel := ab.agent.Config.Delegates.Parallel
	wg2 := sync.WaitGroup{}
	for _, a := range aCtx.agents {
		dCtx := &DelegateCallContext{
			agentCall:      &a.AgentCall,
			configHeaders:  a.AgentCall.Headers,
			forwardHeaders: a.AgentCall.ForwardHeaders,
			removeHeaders:  a.AgentCall.RemoveHeaders,
		}
		if parallel {
			wg2.Add(1)
			go func() {
				ab.callAgent(aCtx, dCtx)
				wg2.Done()
			}()
		} else {
			ab.callAgent(aCtx, dCtx)
		}
	}
	if parallel {
		wg2.Wait()
	}
	if wg != nil {
		wg.Done()
	}
}

func (ab *AgentBehaviorFederate) callAgent(aCtx *AgentContext, dCtx *DelegateCallContext) {
	err := ab.invokeAgent(aCtx, dCtx)
	if err != nil {
		aCtx.Log(err.Error())
		aCtx.ReportProgress(dCtx.agentCall.Name, err.Error())
	}
}

func (ab *AgentBehaviorFederate) callTool(aCtx *AgentContext, dCtx *DelegateCallContext) {
	remoteResult, err := ab.invokeMCP(aCtx, dCtx)
	output := map[string]any{}
	if err != nil {
		msg := fmt.Sprintf("Failed to invoke MCP tool [%s] at URL [%s] with error: %s", dCtx.toolCall.Tool, dCtx.toolCall.URL, err.Error())
		aCtx.Log(msg)
		if remoteResult == nil {
			remoteResult = map[string]any{}
		}
		util.BuildGotoClientInfo(remoteResult, aCtx.agent.Port, aCtx.agent.ID, "", dCtx.toolCall.Tool, dCtx.toolCall.URL, dCtx.toolCall.Server, aCtx.input, dCtx.toolCall.Args, aCtx.requestHeaders, dCtx.callHeaders,
			map[string]any{
				"Goto-MCP-Tool": dCtx.toolCall.Tool,
				"Tool-Call":     dCtx.toolCall,
			})
		if aCtx.localProgress != nil {
			aCtx.ReportProgress(dCtx.toolCall.Tool, msg)
			aCtx.ReportProgress(dCtx.toolCall.Tool, remoteResult)
		} else if aCtx.toolResults != nil {
			aCtx.toolResults[dCtx.toolCall.Tool] = msg
		}
	} else {
		msg := fmt.Sprintf("Successfully invoked MCP tool [%s] at URL [%s]", dCtx.toolCall.Tool, dCtx.toolCall.URL)
		aCtx.Log(msg)

		if !aCtx.ReportProgress(dCtx.toolCall.Tool, msg) {
			output["toolResult"] = msg
		}
	}
	if remoteResult["content"] != nil {
		processMCPContent(dCtx.toolCall.Tool, remoteResult, output, aCtx.resultsChan)
		delete(remoteResult, "content")
	}
	if remoteResult["structuredContent"] != nil {
		if aCtx.resultsChan != nil {
			aCtx.resultsChan <- types.NewPair(dCtx.toolCall.Tool, remoteResult["structuredContent"])
		} else {
			output["upstreamContent"] = remoteResult["structuredContent"]
		}
		delete(remoteResult, "structuredContent")
	}
	for k, v := range remoteResult {
		count := 0
		if arr, ok := v.([]any); ok {
			count = len(arr)
		} else if m, ok := v.(map[string]any); ok {
			count = len(m)
		}
		if aCtx.resultsChan != nil {
			aCtx.resultsChan <- types.NewPair[string, any](dCtx.toolCall.Tool, fmt.Sprintf("Sent %s with %d items.", k, count))
			aCtx.resultsChan <- types.NewPair[string, any](dCtx.toolCall.Tool, map[string]any{k: v})
		} else {
			output[k] = v
		}
	}
	if aCtx.toolResults != nil {
		aCtx.toolResults[dCtx.toolCall.Tool] = output
	}
}

func (ab *AgentBehaviorFederate) prepareHeaders(aCtx *AgentContext, dCtx *DelegateCallContext) {
	headers := map[string][]string{}
	for h, v := range dCtx.configHeaders {
		headers[h] = v
	}
	for _, h := range dCtx.forwardHeaders {
		for h2, v2 := range aCtx.requestHeaders {
			if strings.EqualFold(h, h2) {
				headers[h] = v2
				break
			}
		}
	}
	for _, h := range dCtx.removeHeaders {
		delete(headers, h)
	}
	dCtx.callHeaders = headers
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

func (ab *AgentBehaviorFederate) invokeAgent(aCtx *AgentContext, dCtx *DelegateCallContext) error {
	ab.prepareHeaders(aCtx, dCtx)
	dCtx.agentCall.Headers = dCtx.callHeaders
	msg := fmt.Sprintf("Invoking Agent [%s] at URL [%s] with input [%s]", dCtx.agentCall.Name, dCtx.agentCall.URL, dCtx.agentCall.Message)
	aCtx.Log(msg)
	aCtx.ReportProgress(dCtx.agentCall.Name, msg)
	client := a2aclient.NewA2AClient(ab.agent.Port)
	if client == nil {
		return errors.New("failed to create A2A client")
	}
	session, err := client.ConnectWithAgentCard(aCtx.ctx, dCtx.agentCall, dCtx.url)
	if err != nil {
		return fmt.Errorf("Failed to load agent card for Agent [%s] URL [%s] with error: %s", dCtx.agentCall.Name, dCtx.agentCall.URL, err.Error())
	} else {
		msg = fmt.Sprintf("Loaded agent card for Agent [%s] URL [%s], Streaming [%d]", dCtx.agentCall.Name, dCtx.agentCall.URL, session.Card.Capabilities.Streaming)
		aCtx.ReportProgress(dCtx.agentCall.Name, msg)
	}
	err = session.CallAgent(nil, aCtx.resultsChan, aCtx.upstreamProgress)
	if err != nil {
		return fmt.Errorf("Failed to call Agent [%s] URL [%s] with error: %s", dCtx.agentCall.Name, dCtx.agentCall.URL, err.Error())
	} else {
		msg = fmt.Sprintf("Finished Call to Agent [%s] URL [%s], Streaming [%d]", dCtx.agentCall.Name, dCtx.agentCall.URL, session.Card.Capabilities.Streaming)
		aCtx.ReportProgress(dCtx.agentCall.Name, msg)
	}
	return nil
}

func (ab *AgentBehaviorFederate) invokeMCP(aCtx *AgentContext, dCtx *DelegateCallContext) (remoteResult map[string]any, err error) {
	ab.prepareHeaders(aCtx, dCtx)
	dCtx.toolCall.Headers = dCtx.callHeaders
	args := ab.prepareArgs(dCtx.toolCall.Args, dCtx.forwardHeaders)
	msg := fmt.Sprintf("Invoking MCP tool [%s] at URL [%s]", dCtx.toolCall.Tool, dCtx.toolCall.URL)
	aCtx.Log(msg)
	aCtx.ReportProgress(dCtx.toolCall.Tool, msg)
	client := mcpclient.NewClient(ab.agent.Port, false, ab.agent.ID, dCtx.callHeaders, aCtx.upstreamProgress)
	session, err := client.ConnectWithHops(dCtx.toolCall.URL, dCtx.toolCall.Tool, dCtx.callHeaders, aCtx.hops)
	if err == nil {
		defer session.Close()
		remoteResult, err = session.CallTool(dCtx.toolCall, args)
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
