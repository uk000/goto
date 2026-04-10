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
	aicommon "goto/pkg/ai/common"
	mcpclient "goto/pkg/ai/mcp/client"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func (t *MCPTool) callRemoteTool(tctx *ToolCallContext) (*gomcp.CallToolResult, error) {
	var result *gomcp.CallToolResult
	if tctx.args == nil {
		tctx.args = aicommon.NewCallArgs()
	}
	tc := tctx.Config.RemoteTool.UpdateAndClone(tctx.args.Remote.ToolName, tctx.args.Remote.URL, "", tctx.args.Remote.Authority,
		tctx.args.DelayText, tctx.args.Remote.Headers, tctx.args, tctx.args.Remote.Args)
	if tctx.args.ResultOnly {
		tc.ResultOnly = true
	}
	if tctx.args.NoEvents {
		tc.NoEvents = true
	}
	//t.addForwardHeaders(tc.Headers.Request.Add, tc.Headers.Request.Forward, tc.Args)
	isSSE := tctx.sse
	if tctx.args.Remote.SSE || tc.ForceSSE {
		isSSE = true
	}
	url := tc.URL
	argHasURL := tctx.args.Remote.URL != ""
	if isSSE && !argHasURL {
		url = tc.SSEURL
	}
	operLabel := fmt.Sprintf("%s->%s@%s", tctx.Label, tc.Tool, tc.Server)
	count := tctx.args.Count
	if count > 0 {
		tc.RequestCount = count
	}
	var remoteResult *mcpclient.MCPResult
	var err error
	client := mcpclient.NewClient(tctx.Server.GetPort(), false, tctx.Config.RemoteTool.H2, tctx.Config.RemoteTool.TLS,
		tctx.Server.ID, tctx.rs.ListenerLabel, tctx.Config.RemoteTool.Authority, nil, tctx.notifyClientWithError, tctx.notifyClient)
	session := client.CreateSessionWithTimeline(tctx.ctx, url, tctx.Label, tc, tctx.requestHeaders, tctx.timeline)
	remoteResult, err = session.CallTool(tc.Args)
	session.Stop = true
	if err != nil {
		msg := fmt.Sprintf("Server [%s] Failed to invoke Remote tool [%s] at URL [%s] with error: %s",
			tctx.Server.GetName(), tc.Tool, tc.URL, err.Error())
		tctx.Log(msg)
		result = &gomcp.CallToolResult{Content: []gomcp.Content{&gomcp.TextContent{Text: msg}}, IsError: true}
	} else if remoteResult == nil {
		msg := fmt.Sprintf("Server [%s] Remote tool [%s] at URL [%s] produced no result", tctx.Server.GetName(), tc.Tool, tc.URL)
		tctx.Log(msg)
		result = &gomcp.CallToolResult{Content: []gomcp.Content{&gomcp.TextContent{Text: msg}}, IsError: false}
	} else {
		msg := fmt.Sprintf("Remote operation [%s] successful on [%s]. Sending response...", operLabel, tc.URL)
		tctx.Log(msg)
		result = remoteResult.ToMCP()
		tctx.applyDelay()
	}
	return result, err
}
