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

package mcpserver

import (
	"errors"
	"fmt"
	a2aclient "goto/pkg/ai/a2a/client"
	mcpclient "goto/pkg/ai/mcp/client"
	"goto/pkg/types"
	"goto/pkg/util"
	"net/http"
	"strings"
	"sync"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func (t *ToolCallContext) fetch() (*gomcp.CallToolResult, error) {
	result := &gomcp.CallToolResult{}
	url := t.Config.RemoteTool.URL
	authority := t.Config.RemoteTool.Authority
	if t.remoteArgs == nil {
		t.remoteArgs = &RemoteCallArgs{}
	}
	if t.remoteArgs.URL != "" {
		url = t.remoteArgs.URL
	}
	if t.remoteArgs.Authority != "" {
		authority = t.remoteArgs.Authority
	}
	forwardHeaders := map[string]bool{}
	for _, h := range t.remoteArgs.ForwardHeaders {
		forwardHeaders[h] = true
	}
	for _, h := range t.Config.RemoteTool.ForwardHeaders {
		forwardHeaders[h] = true
	}
	if !strings.HasPrefix(url, "http") {
		url = "http://" + url
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	for h, v := range t.remoteArgs.Headers {
		req.Header.Add(h, v)
	}
	if t.requestHeaders != nil {
		for h := range forwardHeaders {
			if t.requestHeaders[h] != nil {
				req.Header[h] = t.requestHeaders[h]
			}
		}
	}
	req.Header["User-Agent"] = []string{t.Label}
	if authority != "" {
		req.Host = t.remoteArgs.Authority
	}
	if req.Host == "" {
		req.Host = req.URL.Host
	}
	util.PrintRequest("Tool Remote HTTP Call Request Details", req)
	resp, err := t.client.HTTP().Do(req)
	msg := ""
	if err != nil {
		msg = fmt.Sprintf("Server [%s] Failed to invoke Remote URL [%s] with error: %s", t.Server.GetName(), url, err.Error())
		t.Log(msg)
		result.IsError = true
		result.Content = append(result.Content, &gomcp.TextContent{Text: msg})
	} else {
		t.notifyClient(t.Log(fmt.Sprintf("Server [%s] fetched response from remote URL [%s]", t.Server.GetName(), url)), 0)
		output := util.Read(resp.Body)
		result.Content = append(result.Content, &gomcp.TextContent{Text: output})
		result.StructuredContent = util.BuildGotoClientInfo(nil, t.Server.Port, t.Label, t.Name, req.Host, url, req.Host, t.remoteArgs, nil, t.requestHeaders, req.Header,
			map[string]any{"ForwardHeaders": forwardHeaders})
	}
	t.applyDelay()
	return result, err
}

func (t *ToolCallContext) remoteToolCall() (*gomcp.CallToolResult, error) {
	result := &gomcp.CallToolResult{}
	if t.remoteArgs == nil {
		t.remoteArgs = &RemoteCallArgs{}
	}
	if t.remoteArgs.ToolArgs == nil {
		t.remoteArgs.ToolArgs = map[string]any{}
	}
	tc := t.Config.RemoteTool.UpdateAndClone(t.remoteArgs.ToolName, t.remoteArgs.URL, "", t.remoteArgs.Authority,
		t.remoteArgs.Delay, t.remoteArgs.Headers, t.remoteArgs.ToolArgs)

	t.addForwardHeaders(tc.Headers, tc.ForwardHeaders, tc.Args)

	isSSE := t.sse
	if t.remoteArgs.SSE || tc.ForceSSE {
		isSSE = true
	}
	url := tc.URL
	argHasURL := !strings.EqualFold(t.Config.RemoteTool.URL, t.remoteArgs.URL)
	if isSSE && !argHasURL {
		// url = tc.SSEURL
	}
	operLabel := fmt.Sprintf("%s->%s@%s", t.Label, tc.Tool, tc.Server)
	var remoteResult map[string]any
	var err error
	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func() {
		client := mcpclient.NewClient(t.Server.GetPort(), false, t.Server.ID, tc.Headers, nil)
		var session *mcpclient.MCPSession
		session, err = client.ConnectWithHops(url, t.Label, tc.Headers, t.hops)
		if err == nil {
			defer session.Close()
			remoteResult, err = session.CallTool(tc, tc.Args)
		}
		wg.Done()
	}()
	wg.Wait()
	if err != nil {
		msg := fmt.Sprintf("Server [%s] Failed to invoke Remote tool [%s] at URL [%s] with error: %s",
			t.Server.GetName(), tc.Tool, tc.URL, err.Error())
		t.Log(msg)
		result = &gomcp.CallToolResult{Content: []gomcp.Content{&gomcp.TextContent{Text: msg}}, IsError: true}
	} else {
		msg := fmt.Sprintf("Remote operation [%s] successful on [%s]. Sending response...", operLabel, tc.URL)
		t.notifyClient(msg, 0)
		t.applyDelay()
		if remoteResult["content"] != nil {
			content := remoteResult["content"]
			if c, ok := content.([]gomcp.Content); ok {
				result.Content = c
			} else if arr, ok := content.([]any); ok {
				for _, item := range arr {
					result.Content = append(result.Content, &gomcp.TextContent{Text: fmt.Sprintf("%+v", item)})
				}
			} else if s, ok := content.(string); ok {
				result.Content = []gomcp.Content{&gomcp.TextContent{Text: s}}
			} else if m, ok := content.(map[string]any); ok {
				result.Content = []gomcp.Content{&gomcp.TextContent{Text: util.ToJSONText(m)}}
			} else {
				result.Content = []gomcp.Content{&gomcp.TextContent{Text: fmt.Sprintf("%+v", content)}}
			}
		}
		delete(remoteResult, "content")
		output := map[string]any{}
		output["Goto-Client-Info"] = remoteResult["Goto-Client-Info"]
		delete(remoteResult, "Goto-Client-Info")
		output["upstreamContent"] = remoteResult["structuredContent"]
		output["toolResult"] = msg
		result.StructuredContent = output
	}
	return result, err
}

func (t *ToolCallContext) remoteAgentCall() (*gomcp.CallToolResult, error) {
	result := &gomcp.CallToolResult{
		Content: []gomcp.Content{},
	}
	if t.remoteArgs == nil {
		t.remoteArgs = &RemoteCallArgs{}
	}
	ac := t.Config.Agent.CloneWithUpdate(t.remoteArgs.AgentName, t.remoteArgs.URL, t.remoteArgs.Authority, t.remoteArgs.AgentMessage, t.remoteArgs.AgentData)
	t.addForwardHeaders(ac.Headers, ac.ForwardHeaders, ac.Data)
	msg := fmt.Sprintf("Invoking Agent [%s] at URL [%s]", ac.Name, ac.AgentURL)
	t.notifyClient(msg, 0)
	client := a2aclient.NewA2AClient(t.Server.Port)
	if client == nil {
		return nil, errors.New("failed to create A2A client")
	}
	session, err := client.ConnectWithAgentCard(t.ctx, ac, t.remoteArgs.URL)
	if err != nil {
		return nil, fmt.Errorf("Failed to load agent card for Agent [%s] URL [%s] with error: %s", ac.Name, ac.AgentURL, err.Error())
	} else {
		msg = fmt.Sprintf("Loaded agent card for Agent [%s] URL [%s], Streaming [%d]", ac.Name, ac.AgentURL, session.Card.Capabilities.Streaming)
		t.notifyClient(msg, 0)
	}
	resultsChan := make(chan *types.Pair[string, any], 10)
	progressChan := make(chan string, 10)
	wg := sync.WaitGroup{}
	wg.Add(1)
	go t.processResults(ac.Name, progressChan, resultsChan, result, &wg)
	err = session.CallAgent(nil, resultsChan, progressChan)
	close(resultsChan)
	close(progressChan)
	wg.Wait()
	if err != nil {
		return nil, fmt.Errorf("Failed to call Agent [%s] URL [%s] with error: %s", ac.Name, ac.AgentURL, err.Error())
	} else {
		msg = fmt.Sprintf("Finished Call to Agent [%s] URL [%s], Streaming [%d]", ac.Name, ac.AgentURL, session.Card.Capabilities.Streaming)
		t.notifyClient(msg, 0)
	}
	return result, nil
}

func (t *ToolCallContext) processResults(name string, progressChan chan string, resultsChan chan *types.Pair[string, any], result *gomcp.CallToolResult, wg *sync.WaitGroup) {
	structuredContent := map[string]any{}
	structuredCount := 1
outer:
	for {
		select {
		case <-t.ctx.Done():
			t.notifyClient("Stream was cancelled", 0)
			break outer
		case update, ok := <-progressChan:
			if !ok {
				break outer
			}
			if update != "" {
				t.notifyClient(fmt.Sprintf("Upstream[%s] update: %s", name, update), 0)
			}
		case pair, ok := <-resultsChan:
			if !ok {
				break outer
			}
			if pair != nil && pair.Right != nil {
				t.notifyClient(fmt.Sprintf("Upstream[%s] Result for %s: %s", name, pair.Left, pair.RightS()), 0)
				// content, data := createContent(name, pair.Right)
				// result.Content = append(result.Content, content)
				structuredContent[fmt.Sprintf("%s-%d", pair.Left, structuredCount)] = pair.Right
				structuredCount++
			}
		}
	}
	result.StructuredContent = structuredContent
	wg.Done()
}

func (t *ToolCallContext) addForwardHeaders(headers map[string][]string, forwardHeaders []string, args map[string]any) {
	finalForwardHeaders := map[string]bool{}
	if t.remoteArgs.ToolArgs == nil {
		t.remoteArgs.ToolArgs = map[string]any{}
	}
	if t.remoteArgs.ToolArgs["forwardHeaders"] != nil {
		if arr, ok := t.remoteArgs.ToolArgs["forwardHeaders"].([]string); ok {
			for _, h := range arr {
				finalForwardHeaders[h] = true
			}
		}
	}
	if t.remoteArgs.ForwardHeaders == nil {
		t.remoteArgs.ForwardHeaders = []string{}
	}
	t.remoteArgs.ForwardHeaders = append(t.remoteArgs.ForwardHeaders, forwardHeaders...)
	for _, h := range t.remoteArgs.ForwardHeaders {
		finalForwardHeaders[h] = true
	}
	toolForwardHeaders := []string{}
	for h := range finalForwardHeaders {
		toolForwardHeaders = append(toolForwardHeaders, h)
	}
	t.remoteArgs.ToolArgs["forwardHeaders"] = toolForwardHeaders
	args["forwardHeaders"] = toolForwardHeaders
	if t.requestHeaders != nil {
		for _, h := range t.remoteArgs.ForwardHeaders {
			for h2, v2 := range t.requestHeaders {
				if strings.EqualFold(h, h2) {
					headers[h] = v2
					break
				}
			}
		}
	}
}
