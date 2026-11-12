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
	"errors"
	"fmt"
	"goto/pkg/global"
	"goto/pkg/util"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// echo gives back the text and the metadata that it was called with.
func (t *MCPTool) echo(tctx *ToolContext) (*gomcp.CallToolResult, error) {
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
	msg := fmt.Sprintf("Echo Server: %s[%s]. Received Input: %s at Time: %s", tctx.Label, global.Funcs.GetListenerLabelForPort(tctx.Server.GetPort()), input, time.Now().Format(time.RFC3339))
	content = append(content, &gomcp.TextContent{Text: msg})
	tctx.applyDelay()
	tctx.Log("Server %s Tool %s echoing back", tctx.Server.Host, tctx.Label)
	msg = tctx.Flush(false, false)
	content = append(content, &gomcp.TextContent{Text: msg})
	return &gomcp.CallToolResult{Content: content}, nil
}

// status reports the status that was asked for in the args (200 if none), and forces it on the response.
func (t *MCPTool) status(tctx *ToolContext) (*gomcp.CallToolResult, error) {
	content := []gomcp.Content{}
	status := 200
	if tctx.args != nil {
		if tctx.args.Status != 0 {
			status = tctx.args.Status
			tctx.ms.ForcedStatus = status
		}
	}
	msg := ""
	if status == 200 {
		msg = fmt.Sprintf("%s[%s] Status: Success [200] at Time: %s", tctx.Label, global.Funcs.GetListenerLabelForPort(tctx.Server.GetPort()), time.Now().Format(time.RFC3339))
	} else {
		msg = fmt.Sprintf("%s[%s] Status: Requested [%d] at Time: %s", tctx.Label, global.Funcs.GetListenerLabelForPort(tctx.Server.GetPort()), status, time.Now().Format(time.RFC3339))
	}
	content = append(content, &gomcp.TextContent{Text: msg})
	tctx.applyDelay()
	tctx.Log("Server %s Tool %s reporting status %d", tctx.Server.Host, tctx.Label, status)
	msg = tctx.Flush(false, false)
	content = append(content, &gomcp.TextContent{Text: msg})
	return &gomcp.CallToolResult{Content: content}, nil
}

// ping pings the client over the session.
func (t *MCPTool) ping(tctx *ToolContext) (*gomcp.CallToolResult, error) {
	session, err := tctx.session()
	if err != nil {
		return nil, fmt.Errorf("ping failed with error: %s", err.Error())
	}
	if err := session.Ping(tctx.ctx, &gomcp.PingParams{}); err != nil {
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

// sendTime gives the current time along with the data that the client sent.
func (t *MCPTool) sendTime(tctx *ToolContext) (*gomcp.CallToolResult, error) {
	content := []gomcp.Content{&gomcp.TextContent{Text: fmt.Sprintf("Time: %s", time.Now().Format(time.RFC3339))}}
	content = append(content, &gomcp.TextContent{Text: fmt.Sprintf("Client Data: %s", tctx.req.Params.Arguments)})
	tctx.Log("Server [%s] sent time back", tctx.Server.GetName())
	tctx.applyDelay()
	return &gomcp.CallToolResult{Content: content}, nil
}

// listRoots asks the client for its roots, and gives them as the result.
func (t *MCPTool) listRoots(tctx *ToolContext) (*gomcp.CallToolResult, error) {
	session, err := tctx.session()
	if err != nil {
		return nil, fmt.Errorf("listing roots failed: %s", err.Error())
	}
	res, err := session.ListRoots(tctx.ctx, &gomcp.ListRootsParams{})
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
	tctx.Log("Server [%s] Reporting client's roots", tctx.Server.GetName())
	tctx.applyDelay()
	result := &gomcp.CallToolResult{}
	for _, r := range roots {
		result.Content = append(result.Content, &gomcp.TextContent{Text: r})
	}
	return result, nil
}

// serverDetails gives the details of the server that has the tool.
func (t *MCPTool) serverDetails(tctx *ToolContext) (*gomcp.CallToolResult, error) {
	result := &gomcp.CallToolResult{}
	result.Content = append(result.Content, &gomcp.TextContent{Text: util.ToJSONText(tctx.MCPTool.Server)})
	tctx.Log(fmt.Sprintf("%s sent Server [%s] details", tctx.Label, tctx.Server.GetName()))
	return result, nil
}

// listServers gives all the servers of the port of the tool.
func (t *MCPTool) listServers(tctx *ToolContext) (*gomcp.CallToolResult, error) {
	result := &gomcp.CallToolResult{}
	result.Content = append(result.Content, &gomcp.TextContent{Text: util.ToJSONText(tctx.MCPTool.Server.ps.AllServers())})
	tctx.Log(fmt.Sprintf("%s sent All Servers", tctx.Label))
	return result, nil
}

// serverPaths gives the URI routes of the servers.
func (t *MCPTool) serverPaths(tctx *ToolContext) (*gomcp.CallToolResult, error) {
	result := &gomcp.CallToolResult{}
	result.Content = append(result.Content, &gomcp.TextContent{Text: util.ToJSONText(snapshotServerRoutes())})
	tctx.Log(fmt.Sprintf("%s sent Server Routes", tctx.Label))
	return result, nil
}

// listComponents gives all the components of the servers.
func (t *MCPTool) listComponents(tctx *ToolContext) (*gomcp.CallToolResult, error) {
	result := &gomcp.CallToolResult{}
	result.Content = append(result.Content, &gomcp.TextContent{Text: util.ToJSONText(currentComponents())})
	tctx.Log(fmt.Sprintf("%s sent all components", tctx.Label))
	return result, nil
}

// addTool adds the tool that is defined in the args to the server that has this tool.
func (t *MCPTool) addTool(tctx *ToolContext) (result *gomcp.CallToolResult, err error) {
	content := []gomcp.Content{}
	var tool *MCPTool
	name := ""
	if tctx.args != nil && tctx.args.ToolDef != nil {
		name = fmt.Sprint(tctx.args.ToolDef["name"])
		tool = &MCPTool{
			Tool: &gomcp.Tool{
				Name:        name,
				Description: fmt.Sprint(tctx.args.ToolDef["description"]),
			},
			Schema: fmt.Sprint(tctx.args.ToolDef["schema"]),
		}
		util.ReadJsonFromAny(tctx.args.ToolDef["behavior"], &tool.Behavior)
		tool.Server = t.Server
		tool.SetName(name)
		err = processTool(tool)
		if err == nil {
			err = t.Server.addTool(tool)
		}
	} else {
		err = errors.New("no tool definition in the args")
	}
	msg := ""
	listener := global.Funcs.GetListenerLabelForPort(tctx.Server.GetPort())
	if err == nil {
		msg = fmt.Sprintf("%s[%s] Added Tool [%s] to Server at Time: %s", tctx.Label, listener, tool.Name, time.Now().Format(time.RFC3339))
	} else {
		msg = fmt.Sprintf("%s[%s] Failed to add Tool [%s] to Server at Time: %s, Error: %v", tctx.Label, listener, name, time.Now().Format(time.RFC3339), err)
	}
	content = append(content, &gomcp.TextContent{Text: msg})
	tctx.applyDelay()
	tctx.Log("Server %s Tool %s reporting status %d", tctx.Server.Host, tctx.Label, 200)
	msg = tctx.Flush(false, false)
	content = append(content, &gomcp.TextContent{Text: msg})
	return &gomcp.CallToolResult{Content: content, IsError: err != nil}, nil
}
