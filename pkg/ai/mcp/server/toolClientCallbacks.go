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
	"goto/pkg/util"

	"github.com/google/jsonschema-go/jsonschema"
	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func (t *MCPTool) elicit(tctx *ToolCallContext) (*gomcp.CallToolResult, error) {
	tctx.AddEvent(fmt.Sprintf("Server [%s] sent elicit request to client", tctx.Server.GetName()))
	params := &gomcp.ElicitParams{}
	if tctx.Response != nil && tctx.Response.JSON != nil {
		params.Message = tctx.Response.JSON.GetText("message")
		schema := tctx.Response.JSON.Get("requestedSchema")
		properties := map[string]*jsonschema.Schema{}
		for k, v := range schema.Get("properties").JSON().Object() {
			vj := util.JSONFromJSON(v)
			properties[k] = &jsonschema.Schema{Type: vj.GetText("type")}
		}
		params.RequestedSchema = &jsonschema.Schema{
			Type:       schema.GetText("type"),
			Properties: properties,
		}
	}
	res, err := tctx.req.Session.Elicit(tctx.ctx, params)
	var msg string
	if err != nil {
		msg = fmt.Sprintf("Server [%s] failed to get elicit response from client with error [%s]", tctx.Server.GetName(), err.Error())
		tctx.Log(msg)
		return nil, errors.New(msg)
	}
	if res.Action == "decline" {
		tctx.Log("%s Client declined Elicitation", tctx.Label)
	}
	var data map[string]any
	if res.Content == nil {
		msg = tctx.Log("Server [%s] Empty elicit response from client", tctx.Server.GetName())
	} else {
		msg = fmt.Sprintf("Server [%s] Received elicit response from client", tctx.Server.GetName())
		data = extractClientData(res.Content)
		tctx.AddRemoteEvent(msg, "", data, false)
	}
	tctx.applyDelay()
	result := &gomcp.CallToolResult{
		Content: []gomcp.Content{
			&gomcp.TextContent{Text: msg},
		},
	}
	return result, nil
}

func (t *MCPTool) sample(tctx *ToolCallContext) (*gomcp.CallToolResult, error) {
	res, err := tctx.req.Session.CreateMessage(tctx.ctx, &gomcp.CreateMessageParams{
		Messages: []*gomcp.SamplingMessage{{
			Role:    "user",
			Content: &gomcp.TextContent{Text: util.ToJSONText(tctx.Response)},
		}},
		IncludeContext: "allServers",
		SystemPrompt:   tctx.Tool.Description,
		MaxTokens:      10,
	})
	var msg string
	if err != nil || res == nil {
		msg = tctx.Log("Server [%s] failed to get sample from client", tctx.Server.GetName())
		return nil, fmt.Errorf("sampling failed: %v", err)
	}
	msg = fmt.Sprintf("Server [%s] got sample from client", tctx.Server.GetName())
	data := extractClientData(res.Content)
	tctx.AddRemoteEvent(msg, "", data, false)
	tctx.applyDelay()
	result := &gomcp.CallToolResult{}
	result.Content = []gomcp.Content{
		&gomcp.TextContent{Text: "Sampling successful"},
		&gomcp.TextContent{Text: "Model: " + res.Model},
		&gomcp.TextContent{Text: "Role: " + string(res.Role)},
		&gomcp.TextContent{Text: "StopReason: " + res.StopReason},
		res.Content,
	}
	return result, nil
}

func extractClientData(content any) map[string]any {
	if m, ok := content.(map[string]any); ok {
		return m
	}
	if text, ok := content.(gomcp.TextContent); ok {
		json, ok := util.JSONFromJSONText(text.Text)
		if ok && !json.IsEmpty() {
			return json.Object()
		}
		return map[string]any{"content": text.Text}
	}
	if text, ok := content.(*gomcp.TextContent); ok {
		json, ok := util.JSONFromJSONText(text.Text)
		if ok && !json.IsEmpty() {
			return json.Object()
		}
		return map[string]any{"content": text.Text}
	}
	return nil
}
