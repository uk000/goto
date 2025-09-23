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

func (ab *AgentBehaviorFederate) DoUnary(aCtx *AgentCallContext) (*taskmanager.MessageProcessingResult, error) {
	aCtx.triggers = ab.triggers
	aCtx.detectRemoteCalls()
	results := map[string]map[string]any{}
	results["tools"] = map[string]any{}
	results["agents"] = map[string]any{}
	if len(ab.triggers) == 0 {
		results["tools"][""] = []any{"Agent update: No tools available"}
	} else if len(aCtx.tools) == 0 {
		results["tools"][""] = []any{"Agent update: No tools were triggered"}
	} else {
		ab.runTools(aCtx, results["tools"], nil, nil, nil)
		if len(results["tools"]) == 0 {
			results["tools"][""] = []any{"Agent update: No tool produced any results."}
		}
	}
	if len(ab.triggers) == 0 {
		results["agents"][""] = []any{"Agent update: No agents available"}
	} else if len(aCtx.agents) == 0 {
		results["agents"][""] = []any{"Agent update: No agents were triggered"}
	} else {
		ab.runAgents(aCtx, results["agents"], nil, nil, nil)
		if len(results["agents"]) == 0 {
			results["agents"][""] = []any{"No agent produced any results."}
		}
	}
	result := createHybridMessage(results)
	return &taskmanager.MessageProcessingResult{
		Result: &result,
	}, nil
}

func (ab *AgentBehaviorFederate) DoStream(aCtx *AgentCallContext) error {
	aCtx.triggers = ab.triggers
	aCtx.detectRemoteCalls()
	toolsResultsChan := make(chan *types.Pair[string, any], 10)
	agentsResultsChan := make(chan *types.Pair[string, any], 10)
	upstreamProgress := make(chan string, 10)
	localProgress := make(chan *types.Pair[string, any], 10)

	wg := sync.WaitGroup{}
	if len(ab.triggers) == 0 {
		aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, "Agent update: No tools available", nil)
	} else if len(aCtx.tools) == 0 {
		aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, "Agent update: No tools were triggered", nil)
	} else {
		wg.Add(1)
		go ab.processResults(aCtx, "tool", upstreamProgress, localProgress, toolsResultsChan, &wg)
		ab.runTools(aCtx, nil, upstreamProgress, localProgress, toolsResultsChan)
	}
	if len(ab.triggers) == 0 {
		aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, "Agent update: No agents available", nil)
	} else if len(aCtx.agents) == 0 {
		aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, "Agent update: No agents were triggered", nil)
	} else {
		wg.Add(1)
		go ab.processResults(aCtx, "agent", upstreamProgress, localProgress, agentsResultsChan, &wg)
		ab.runAgents(aCtx, nil, upstreamProgress, localProgress, agentsResultsChan)
	}
	wg.Wait()
	return nil
}

func (ab *AgentBehaviorFederate) processResults(aCtx *AgentCallContext, dType string, upstreamProgress chan string, localProgress, resultsChan chan *types.Pair[string, any], wg *sync.WaitGroup) {
outer:
	for {
		select {
		case <-aCtx.ctx.Done():
			aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, "Stream was cancelled", nil)
			break outer
		case update, ok := <-upstreamProgress:
			if !ok {
				break outer
			}
			if update != "" {
				callId := fmt.Sprintf("Upstream %s update", dType)
				aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, fmt.Sprintf("%s: %s", callId, update), nil)
			}
		case pair, ok := <-localProgress:
			if !ok {
				break outer
			}
			if pair != nil && pair.Right != nil {
				callId := fmt.Sprintf("Agent update [%s]", pair.Left)
				aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, fmt.Sprintf("%s: %s", callId, pair.Right), nil)
			}
		case pair, ok := <-resultsChan:
			if !ok {
				break outer
			}
			if pair != nil && pair.Right != nil {
				log.Println(util.ToJSONText(pair))
				callId := fmt.Sprintf("Upstream [%s] Result:", pair.Left)
				aCtx.sendTaskStatusUpdate(a2aproto.TaskStateWorking, "", createAnyParts(callId, pair.Right))
			}
		}
	}
	wg.Done()
}

func (ab *AgentBehaviorFederate) runTools(aCtx *AgentCallContext, results map[string]any, upstreamProgress chan string, localProgress, resultsChan chan *types.Pair[string, any]) {
	parallel := ab.agent.Config.Delegates.Parallel
	wg := sync.WaitGroup{}
	for _, tc := range aCtx.tools {
		if parallel {
			wg.Add(1)
			go func() {
				ab.callTool(aCtx, &tc.ToolCall, results, upstreamProgress, localProgress, resultsChan)
				wg.Done()
			}()
		} else {
			ab.callTool(aCtx, &tc.ToolCall, results, upstreamProgress, localProgress, resultsChan)
		}
	}
	if parallel {
		wg.Wait()
	}
	if resultsChan != nil {
		close(resultsChan)
	}
}

func (ab *AgentBehaviorFederate) runAgents(aCtx *AgentCallContext, results map[string]any, upstreamProgress chan string, localProgress, resultsChan chan *types.Pair[string, any]) {
	parallel := ab.agent.Config.Delegates.Parallel
	wg := sync.WaitGroup{}
	for _, a := range aCtx.agents {
		if parallel {
			wg.Add(1)
			go func() {
				ab.callAgent(aCtx, &a.AgentCall, results, upstreamProgress, localProgress, resultsChan)
				wg.Done()
			}()
		} else {
			ab.callAgent(aCtx, &a.AgentCall, results, upstreamProgress, localProgress, resultsChan)
		}
	}
	if parallel {
		wg.Wait()
	}
	close(resultsChan)
}

func (ab *AgentBehaviorFederate) callAgent(aCtx *AgentCallContext, ac *a2aclient.AgentCall, results map[string]any, upstreamProgress chan string, localProgress, resultsChan chan *types.Pair[string, any]) {
	err := ab.invokeAgent(aCtx, ac, upstreamProgress, localProgress, resultsChan)
	if err != nil {
		aCtx.Log(err.Error())
		if localProgress != nil {
			localProgress <- types.NewPair[string, any](ac.Name, err.Error())
		} else if results != nil {
			results[ac.Name] = err.Error()
		}
	}
}

func (ab *AgentBehaviorFederate) callTool(aCtx *AgentCallContext, tc *mcpclient.ToolCall, results map[string]any, upstreamProgress chan string, localProgress, resultsChan chan *types.Pair[string, any]) {
	remoteResult, err := ab.invokeMCP(aCtx, tc, upstreamProgress, localProgress)
	if err != nil {
		msg := fmt.Sprintf("Failed to invoke MCP tool [%s] at URL [%s] with error: %s", tc.Tool, tc.URL, err.Error())
		aCtx.Log(msg)
		if localProgress != nil {
			localProgress <- types.NewPair[string, any](tc.Tool, msg)
		} else if results != nil {
			results[tc.Tool] = msg
		}
	} else {
		output := map[string]any{}
		if remoteResult["content"] != nil {
			processMCPContent(tc.Tool, remoteResult, output, resultsChan)
			delete(remoteResult, "content")
		}
		if remoteResult["structuredContent"] != nil {
			if resultsChan != nil {
				resultsChan <- types.NewPair(tc.Tool, remoteResult["structuredContent"])
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
			if resultsChan != nil {
				resultsChan <- types.NewPair[string, any](tc.Tool, fmt.Sprintf("Sent %s with %d items.", k, count))
				resultsChan <- types.NewPair[string, any](tc.Tool, map[string]any{k: v})
			} else {
				output[k] = v
			}
		}
		if results != nil {
			results[tc.Tool] = output
		}
		msg := fmt.Sprintf("Successfully invoked MCP tool [%s] at URL [%s]", tc.Tool, tc.URL)
		aCtx.Log(msg)
		if localProgress != nil {
			localProgress <- types.NewPair[string, any](tc.Tool, msg)
		} else {
			output["toolResult"] = msg
		}
	}
}

func (ab *AgentBehaviorFederate) prepareHeaders(aCtx *AgentCallContext, forwardHeaders []string, headers map[string][]string) {
	for _, h := range forwardHeaders {
		for h2, v2 := range aCtx.headers {
			if strings.EqualFold(h, h2) {
				headers[h] = v2
				break
			}
		}
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

func (ab *AgentBehaviorFederate) invokeAgent(aCtx *AgentCallContext, ac *a2aclient.AgentCall, upstreamProgress chan string, localProgress, resultsChan chan *types.Pair[string, any]) error {
	ab.prepareHeaders(aCtx, ac.ForwardHeaders, ac.Headers)
	msg := fmt.Sprintf("Invoking Agent [%s] at URL [%s]", ac.Name, ac.URL)
	aCtx.Log(msg)
	if localProgress != nil {
		localProgress <- types.NewPair[string, any](ac.Name, msg)
	}
	client := a2aclient.NewA2AClient(ab.agent.Port)
	if client == nil {
		return errors.New("failed to create A2A client")
	}
	session, err := client.ConnectWithAgentCard(aCtx.ctx, ac)
	if err != nil {
		return fmt.Errorf("Failed to load agent card for Agent [%s] URL [%s] with error: %s", ac.Name, ac.URL, err.Error())
	} else {
		msg = fmt.Sprintf("Loaded agent card for Agent [%s] URL [%s], Streaming [%d]", ac.Name, ac.URL, session.Card.Capabilities.Streaming)
		localProgress <- types.NewPair[string, any](ac.Name, msg)
	}
	err = session.CallAgent(nil, resultsChan, upstreamProgress)
	if err != nil {
		return fmt.Errorf("Failed to call Agent [%s] URL [%s] with error: %s", ac.Name, ac.URL, err.Error())
	} else {
		msg = fmt.Sprintf("Finished Call to Agent [%s] URL [%s], Streaming [%d]", ac.Name, ac.URL, session.Card.Capabilities.Streaming)
		localProgress <- types.NewPair[string, any](ac.Name, msg)
	}
	return nil
}

func (ab *AgentBehaviorFederate) invokeMCP(aCtx *AgentCallContext, tc *mcpclient.ToolCall, upstreamProgress chan string, localProgress chan *types.Pair[string, any]) (remoteResult map[string]any, err error) {
	ab.prepareHeaders(aCtx, tc.ForwardHeaders, tc.Headers)
	args := ab.prepareArgs(tc.Args, tc.ForwardHeaders)
	msg := fmt.Sprintf("Invoking MCP tool [%s] at URL [%s]", tc.Tool, tc.URL)
	aCtx.Log(msg)
	if localProgress != nil {
		localProgress <- types.NewPair[string, any](tc.Tool, msg)
	}
	client := mcpclient.NewClient(ab.agent.Port, false, ab.agent.ID, upstreamProgress)
	session, err := client.ConnectWithHops(tc.URL, tc.Tool, aCtx.hops)
	if err == nil {
		defer session.Close()
		remoteResult, err = session.CallTool(tc, args)
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
		hasText := false
		for _, content := range contents {
			if textContent, ok := content.(*mcp.TextContent); ok {
				if textContent.Text != "" {
					hasText = true
					if resultsChan != nil {
						resultsChan <- types.NewPair[string, any](key, textContent.Text)
					} else {
						textResult = append(textResult, textContent.Text)
					}
				}
			} else {
				hasText = true
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
		} else if !hasText {
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
	if resultsChan != nil {
		resultsChan <- types.NewPair[string, any](key, fmt.Sprintf("Sent result content for [%s]", key))
	}
}
