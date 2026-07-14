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

func (t *MCPTool) callRemoteTool(tctx *ToolContext) (*gomcp.CallToolResult, error) {
	var result *gomcp.CallToolResult
	if tctx.args == nil {
		tctx.args = aicommon.NewCallArgs()
	}
	tctx.args.NonNil()
	tc := tctx.Config.RemoteTool.UpdateAndClone(tctx.args.RemoteArgs.ToolName, tctx.args.RemoteArgs.URL, "", tctx.args.RemoteArgs.Authority,
		tctx.args.DelayText, tctx.args.RemoteArgs.Headers, tctx.args.RemoteArgs)
	if tctx.args.ResultOnly {
		tc.ResultOnly = true
	}
	if tctx.args.NoEvents {
		tc.NoEvents = true
	}
	//t.addForwardHeaders(tc.Headers.Request.Add, tc.Headers.Request.Forward, tc.Args)
	isSSE := tctx.sse
	if tctx.args.RemoteArgs.SSE || tc.ForceSSE {
		isSSE = true
	}
	url := tc.URL
	argHasURL := tctx.args.RemoteArgs.URL != ""
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
		tctx.Server.ID, tctx.rs.ListenerLabel, tctx.Config.RemoteTool.Authority, tc.RequestTimeoutD, nil, tctx.notifyClientWithError, tctx.notifyClient)
	session := client.CreateSessionWithTimeline(tctx.ctx, url, tctx.Label, tc, tctx.requestHeaders, tctx.timeline)
	remoteResult, err = session.CallTool(tc.Args)
	session.Stop = true
	tctx.applyDelay()
	if remoteResult != nil {
		result = remoteResult.ToMCP()
		tctx.timeline.RemoteGotos = remoteResult.RemoteGotos
		tctx.ms.ForcedStatus = remoteResult.LastResponseStatus
	} else {
		result = &gomcp.CallToolResult{}
	}
	if err != nil {
		msg := fmt.Sprintf("MCP Server [%s]: Failed to invoke Remote tool [%s] at URL [%s] with error: %s",
			tctx.Server.ID, tc.Tool, tc.URL, err.Error())
		tctx.Log(msg)
		result.Content = append(result.Content, &gomcp.TextContent{Text: msg})
		result.IsError = true
	} else if remoteResult == nil {
		msg := fmt.Sprintf("Server [%s] Remote tool [%s] at URL [%s] produced no result", tctx.Server.GetName(), tc.Tool, tc.URL)
		tctx.Log(msg)
		result.Content = append(result.Content, &gomcp.TextContent{Text: msg})
	} else {
		msg := fmt.Sprintf("MCP Server [%s]: Remote operation [%s] successful on [%s]. Sending response...", tctx.Server.ID, operLabel, tc.URL)
		tctx.Log(msg)
	}
	return result, err
}
