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
	"goto/pkg/util"
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

	t.addForwardHeaders(tc.Headers.Request.Add, tc.Headers.Request.Forward, tc.Args)

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
		client := mcpclient.NewClient(t.Server.GetPort(), false, t.Server.ID, t.rs.ListenerLabel, progressChan)
		var session *mcpclient.MCPSession
		session, err = client.ConnectWithHops(url, t.Label, t.hops)
		if err == nil {
			defer session.Close()
			remoteResult, err = session.CallTool(tc, tc.Args, t.requestHeaders)
		}
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
