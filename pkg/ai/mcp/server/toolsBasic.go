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
	"goto/pkg/global"
	"goto/pkg/util"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func (t *MCPTool) echo(tctx *ToolCallContext) (*gomcp.CallToolResult, error) {
	content := []gomcp.Content{}
	input := ""
	if tctx.args != nil {
		if tctx.args.Text != "" {
			input = tctx.args.Text
		}
		if tctx.args.Metadata != nil {
			input = fmt.Sprintf("%s %s", input, util.ToJSONText(tctx.args.Metadata))
		}
	}
	if input == "" {
		input = "<No input to echo>"
	}
	msg := fmt.Sprintf("Echo Server: %s[%s]. Input: %s, Time: %s", tctx.Label, global.Funcs.GetListenerLabelForPort(tctx.Server.GetPort()), input, time.Now().Format(time.RFC3339))
	content = append(content, &gomcp.TextContent{Text: msg})
	// content = append(content, &gomcp.TextContent{Text: util.ToJSONText(echo.GetEchoResponseWithAddendum(t.rs, map[string]any{"Goto-MCP-Server": t.Server.ID, "Goto-MCP-Tool": t.Name}))})
	tctx.applyDelay()
	tctx.notifyClient(tctx.Log("Server %s Tool %s echoing back", tctx.Server.Host, tctx.Label), 0)
	return &gomcp.CallToolResult{Content: content}, nil
}

func (t *MCPTool) ping(tctx *ToolCallContext) (*gomcp.CallToolResult, error) {
	if err := tctx.req.Session.Ping(tctx.ctx, &gomcp.PingParams{}); err != nil {
		return nil, fmt.Errorf("ping failed with error: %s", err.Error())
	}
	tctx.Log("Server [%s] pinged client", tctx.Server.GetName())
	tctx.applyDelay()
	return &gomcp.CallToolResult{
		Content: []gomcp.Content{
			&gomcp.TextContent{Text: fmt.Sprintf("%s Ping from Goto MCP successful", tctx.Label)},
		},
	}, nil
}

func (t *MCPTool) sendTime(tctx *ToolCallContext) (*gomcp.CallToolResult, error) {
	content := []gomcp.Content{&gomcp.TextContent{Text: fmt.Sprintf("Time: %s", time.Now().Format(time.RFC3339))}}
	content = append(content, &gomcp.TextContent{Text: fmt.Sprintf("Client Data: %s", tctx.req.Params.Arguments)})
	tctx.notifyClient(tctx.Log("Server [%s] sent time back", tctx.Server.GetName()), 0)
	tctx.applyDelay()
	tctx.hops.Add(tctx.Flush(true))
	return &gomcp.CallToolResult{Content: content}, nil
}

func (t *MCPTool) listRoots(tctx *ToolCallContext) (*gomcp.CallToolResult, error) {
	res, err := tctx.req.Session.ListRoots(tctx.ctx, &gomcp.ListRootsParams{})
	if err != nil {
		tctx.Log("Server [%s] failed to get roots from client", tctx.Server.GetName())
		return nil, fmt.Errorf("listing roots failed: %s", err.Error())
	}
	var roots []string
	if len(res.Roots) > 0 {
		for _, r := range res.Roots {
			roots = append(roots, fmt.Sprintf("%s [uri: %s]", r.Name, r.URI))
		}
	} else {
		roots = []string{"no roots"}
	}
	tctx.notifyClient(tctx.Log("Server [%s] Reporting client's roots", tctx.Server.GetName()), 0)
	tctx.applyDelay()
	result := &gomcp.CallToolResult{}
	for _, r := range roots {
		result.Content = append(result.Content, &gomcp.TextContent{Text: r})
	}
	return result, nil
}

func (t *MCPTool) sendServerDetails(tctx *ToolCallContext) (*gomcp.CallToolResult, error) {
	result := &gomcp.CallToolResult{}
	result.Content = append(result.Content, &gomcp.TextContent{Text: util.ToJSONText(tctx.MCPTool.Server)})
	tctx.Log(fmt.Sprintf("%s sent Server [%s] details", tctx.Label, tctx.Server.GetName()))
	return result, nil
}

func (t *MCPTool) sendAllServers(tctx *ToolCallContext) (*gomcp.CallToolResult, error) {
	result := &gomcp.CallToolResult{}
	result.Content = append(result.Content, &gomcp.TextContent{Text: util.ToJSONText(tctx.MCPTool.Server.ps.AllServers())})
	tctx.Log(fmt.Sprintf("%s sent All Servers", tctx.Label))
	return result, nil
}

func (t *MCPTool) sendServerPaths(tctx *ToolCallContext) (*gomcp.CallToolResult, error) {
	result := &gomcp.CallToolResult{}
	result.Content = append(result.Content, &gomcp.TextContent{Text: util.ToJSONText(ServerRoutes)})
	tctx.Log(fmt.Sprintf("%s sent Server Routes", tctx.Label))
	return result, nil
}

func (t *MCPTool) sendAllComponents(tctx *ToolCallContext) (*gomcp.CallToolResult, error) {
	result := &gomcp.CallToolResult{}
	result.Content = append(result.Content, &gomcp.TextContent{Text: util.ToJSONText(AllComponents)})
	tctx.Log(fmt.Sprintf("%s sent all components", tctx.Label))
	return result, nil
}
