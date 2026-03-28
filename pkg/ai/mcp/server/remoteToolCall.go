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

package mcpserver

import (
	"fmt"
	mcpclient "goto/pkg/ai/mcp/client"
	"goto/pkg/constants"
	"strings"
	"sync"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

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

	//t.addForwardHeaders(tc.Headers.Request.Add, tc.Headers.Request.Forward, tc.Args)

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
	progressChan := make(chan string, 10)
	go func(pc chan string) {
		for msg := range pc {
			t.notifyClient(msg)
		}
	}(progressChan)
	go func() {
		client := mcpclient.NewClient(t.Server.GetPort(), false, t.Config.RemoteTool.H2, t.Config.RemoteTool.TLS, t.Server.ID, t.rs.ListenerLabel, t.Config.RemoteTool.Authority, progressChan)
		session := client.CreateSessionWithHops(url, t.Label, t.hops)
		session.SetCallContext(tc, t.requestHeaders)
		err = session.Connect()
		if err == nil {
			defer session.Close()
			remoteResult, err = session.CallTool(tc, tc.Args, t.requestHeaders)
		}
		client.Stop = true
		wg.Done()
	}()
	wg.Wait()
	close(progressChan)
	if err != nil {
		msg := fmt.Sprintf("Server [%s] Failed to invoke Remote tool [%s] at URL [%s] with error: %s",
			t.Server.GetName(), tc.Tool, tc.URL, err.Error())
		t.Log(msg)
		result = &gomcp.CallToolResult{Content: []gomcp.Content{&gomcp.TextContent{Text: msg}}, IsError: true}
	} else {
		output := map[string]map[string]any{}
		msg := fmt.Sprintf("Remote operation [%s] successful on [%s]. Sending response...", operLabel, tc.URL)
		t.notifyClient(msg, 0)
		t.applyDelay()
		output["toolResult"] = map[string]any{"": msg}
		output[constants.HeaderGotoClientInfo] = map[string]any{}
		if m, ok := remoteResult[constants.HeaderGotoClientInfo].(map[string]any); ok {
			for k, v := range m {
				output[constants.HeaderGotoClientInfo][k] = v
			}
		} else {
			output[constants.HeaderGotoClientInfo][""] = remoteResult[constants.HeaderGotoClientInfo]
		}
		delete(remoteResult, constants.HeaderGotoClientInfo)
		i := 0
		calls := map[string]map[string]any{}
		for url, a := range remoteResult {
			calls[url] = map[string]any{}
			callResults := a.([]any)
			callData := map[string]map[string]any{}
			for _, data := range callResults {
				i++
				key := fmt.Sprintf("Request %d", i)
				switch val := data.(type) {
				case map[string]any:
					callData[key] = map[string]any{}
					if m, ok := val["structuredContent"].(map[string]any); ok {
						if m[constants.HeaderGotoServerInfo] != nil {
							callData[key][constants.HeaderGotoServerInfo] = m[constants.HeaderGotoServerInfo]
						}
						delete(m, constants.HeaderGotoServerInfo)
						if len(m) > 0 {
							callData[key]["upstreamInfo"] = m
						}
					} else {
						callData[key]["upstreamInfo"] = val["structuredContent"]
					}
					delete(val, "structuredContent")
					for k, v := range val {
						callData[key][k] = v
						result.Content = append(result.Content, &gomcp.TextContent{Text: fmt.Sprintf("[%s][%s] %s: %+v", url, key, k, v)})
					}
				default:
					result.Content = append(result.Content, &gomcp.TextContent{Text: fmt.Sprintf("[%s][%s]: %+v", url, key, data)})
				}
			}
			for k, v := range callData {
				calls[url][k] = v
			}
			output["Calls"] = map[string]any{}
			for k, v := range calls {
				output["Calls"][k] = v
			}
			result.StructuredContent = output
		}
	}
	return result, err
}
