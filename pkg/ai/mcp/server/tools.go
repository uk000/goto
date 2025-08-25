package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"goto/pkg/ai/mcp"
	"goto/pkg/global"
	"goto/pkg/server/echo"
	"goto/pkg/util"
	"log"
	"net/http"
	"sync"
	"time"

	mcpclient "goto/pkg/ai/mcp/client"

	"github.com/google/jsonschema-go/jsonschema"
	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type MCPTool struct {
	mcp.MCPComponent
	Tool *gomcp.Tool `json:"tool"`
}

func NewMCPTool(name, desc string) *MCPTool {
	return &MCPTool{
		Tool: &gomcp.Tool{
			Meta:         map[string]any{},
			Annotations:  &gomcp.ToolAnnotations{},
			Title:        name,
			Name:         name,
			Description:  desc,
			InputSchema:  &jsonschema.Schema{},
			OutputSchema: &jsonschema.Schema{},
		},
	}
}

func ParseTool(payload []byte) (*MCPTool, error) {
	tool := &MCPTool{}
	if err := util.ReadJsonFromBytes(payload, tool); err != nil {
		return nil, err
	}
	tool.Kind = KindTools
	return tool, nil
}

func (t *MCPTool) Handle(ctx context.Context, req *gomcp.CallToolRequest) (result *gomcp.CallToolResult, err error) {
	req.Params.GetProgressToken()
	protocol := "mcp"
	if util.IsSSE(ctx) {
		protocol = "mcp/sse"
	}
	serverID := fmt.Sprintf("[%s][%s]", t.Server.GetName(), protocol)
	hops := util.NewHops(t.Server.GetHost(), serverID, t.Label)
	hops.Add(fmt.Sprintf("Server %s received call for tool [%s]", t.Label, t.Tool.Name))
	if t.Behavior.Echo {
		result, err = t.echo(ctx, req, hops)
	} else if t.Behavior.Ping {
		result, err = t.ping(ctx, req, hops)
	} else if t.Behavior.Time {
		result, err = t.sendTime(ctx, req, hops)
	} else if t.Behavior.ListRoots {
		result, err = t.listRoots(ctx, req, hops)
	} else if t.Behavior.Sample {
		result, err = t.sample(ctx, req, hops)
	} else if t.Behavior.Elicit {
		result, err = t.elicit(ctx, req, hops)
	} else if t.IsFetch {
		result, err = t.fetch(ctx, req, hops)
	} else if t.IsRemote {
		result, err = t.remoteToolCall(ctx, req, hops)
	} else {
		result, err = t.sendPayload(ctx, req, hops)
	}
	if result == nil {
		result = &gomcp.CallToolResult{}
	}
	output := map[string]any{}
	sc := result.StructuredContent
	if sc == nil {
		_, rs := util.GetRequestStoreForContext(ctx)
		sc = echo.GetEchoResponseFromRS(rs)
	}
	hops.Add(fmt.Sprintf("Server %s finished call for tool [%s]", t.Label, t.Tool.Name))
	output["hops"] = hops.Steps
	output["Goto-Server-Info"] = sc
	result.StructuredContent = output
	return
}

func argsFromRaw(a any) map[string]any {
	if raw, ok := a.(json.RawMessage); ok {
		data := map[string]any{}
		json.Unmarshal([]byte(raw), &data)
		return data
	}
	return nil
}

func (t *MCPTool) echo(ctx context.Context, req *gomcp.CallToolRequest, hops *util.Hops) (*gomcp.CallToolResult, error) {
	content := []gomcp.Content{}
	data := argsFromRaw(req.Params.Arguments)
	content = append(content, &gomcp.TextContent{Text: fmt.Sprintf("Echo Server: %s[%s]", t.Label, global.Funcs.GetListenerLabelForPort(t.Server.GetPort()))})
	if len(data) > 0 {
		for k, v := range data {
			content = append(content, &gomcp.TextContent{Text: fmt.Sprintf("%s Received [%s: %s]", t.Label, k, v)})
		}
	} else {
		content = append(content, &gomcp.TextContent{Text: fmt.Sprintf("%s Tool.echo: Received no input, still echoing...", t.Label)})
	}
	hops.Add(fmt.Sprintf("%s Server [%s] echoed back", t.Label, t.Server.GetName()))
	return &gomcp.CallToolResult{Content: content}, nil
}

func (t *MCPTool) ping(ctx context.Context, req *gomcp.CallToolRequest, hops *util.Hops) (*gomcp.CallToolResult, error) {
	if err := req.Session.Ping(ctx, &gomcp.PingParams{}); err != nil {
		return nil, fmt.Errorf("ping failed")
	}
	hops.Add(fmt.Sprintf("Server [%s] pinged client", t.Server.GetName()))
	return &gomcp.CallToolResult{
		Content: []gomcp.Content{
			&gomcp.TextContent{Text: fmt.Sprintf("%s Ping from Goto MCP successful", t.Label)},
		},
	}, nil
}

func (t *MCPTool) sendTime(ctx context.Context, req *gomcp.CallToolRequest, hops *util.Hops) (*gomcp.CallToolResult, error) {
	content := []gomcp.Content{&gomcp.TextContent{Text: fmt.Sprintf("Time: %s", time.Now().Format(time.RFC3339))}}
	content = append(content, &gomcp.TextContent{Text: fmt.Sprintf("Client Data: %s", req.Params.Arguments)})
	// if args, ok := req.Params.Arguments.(map[string]any); ok {
	// 	for k, v := range args {
	// 		content = append(content, &gomcp.TextContent{Text: fmt.Sprintf("%s: %s", k, v)})
	// 	}
	// } else {
	// }
	hops.Add(fmt.Sprintf("%s Server [%s] sent time back", t.Label, t.Server.GetName()))
	return &gomcp.CallToolResult{Content: content}, nil
}

func (t *MCPTool) listRoots(ctx context.Context, req *gomcp.CallToolRequest, hops *util.Hops) (*gomcp.CallToolResult, error) {
	res, err := req.Session.ListRoots(ctx, &gomcp.ListRootsParams{})
	if err != nil {
		hops.Add(fmt.Sprintf("%s Server [%s] failed to get roots from client", t.Label, t.Server.GetName()))
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
	hops.Add(fmt.Sprintf("%s Server [%s] Reporting client's roots", t.Label, t.Server.GetName()))
	result := &gomcp.CallToolResult{}
	for _, r := range roots {
		result.Content = append(result.Content, &gomcp.TextContent{Text: r})
	}
	return result, nil
}

func (t *MCPTool) sample(ctx context.Context, req *gomcp.CallToolRequest, hops *util.Hops) (*gomcp.CallToolResult, error) {
	res, err := req.Session.CreateMessage(ctx, &gomcp.CreateMessageParams{
		Messages: []*gomcp.SamplingMessage{{
			Role:    "user",
			Content: &gomcp.TextContent{Text: util.ToJSONText(t.Payload)},
		}},
		IncludeContext: "allServers",
		SystemPrompt:   t.Tool.Description,
		MaxTokens:      10,
	})
	if err != nil {
		hops.Add(fmt.Sprintf("%s Server [%s] failed to get sample from client", t.Label, t.Server.GetName()))
		return nil, fmt.Errorf("sampling failed: %v", err)
	}
	hops.Add(fmt.Sprintf("%s Server [%s] got sample from client", t.Label, t.Server.GetName()))
	if res.Content == nil {
		res.Content = &gomcp.TextContent{Text: "No content"}
	} else {
		if tc, ok := res.Content.(*gomcp.TextContent); ok {
			text, _ := t.assignClientHops(tc.Text, nil, hops)
			if text != "" {
				res.Content = &gomcp.TextContent{Text: text}
			} else {
				res.Content = &gomcp.TextContent{Text: "No content"}
			}
		}
	}
	return &gomcp.CallToolResult{
		Content: []gomcp.Content{
			&gomcp.TextContent{Text: "Sampling successful"},
			&gomcp.TextContent{Text: "Model: " + res.Model},
			&gomcp.TextContent{Text: "Role: " + string(res.Role)},
			&gomcp.TextContent{Text: "StopReason: " + res.StopReason},
			&gomcp.TextContent{Text: "Content: " + res.Content.(*gomcp.TextContent).Text},
		},
	}, nil
}

func (t *MCPTool) assignClientHops(text string, data map[string]any, hops *util.Hops) (string, map[string]any) {
	toText := false
	if data == nil {
		json := util.JSONFromJSONText(text)
		if json != nil {
			data = json.Object()
			toText = true
		}
	}
	if s := data["hops"]; s != nil {
		if steps, ok := s.([]any); ok {
			for _, step := range steps {
				hops.AddRemote(step)
			}
		}
		delete(data, "hops")
	}
	if len(data) == 0 {
		data = nil
	}
	if toText {
		if data != nil {
			text = util.ToJSONText(data)
		} else {
			text = ""
		}
	}
	return text, data
}

func (t *MCPTool) elicit(ctx context.Context, req *gomcp.CallToolRequest, hops *util.Hops) (*gomcp.CallToolResult, error) {
	hops.Add(fmt.Sprintf("%s Server [%s] sent elicit request to client", t.Label, t.Server.GetName()))
	params := &gomcp.ElicitParams{}
	if t.Payload != nil && t.Payload.JSON != nil {
		params.Message = t.Payload.JSON.GetText("message")
		schema := t.Payload.JSON.Get("requestedSchema")
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
	res, err := req.Session.Elicit(ctx, params)
	if err != nil {
		msg := fmt.Sprintf("%s Server [%s] failed to get elicit response from client with error [%s]", t.Label, t.Server.GetName(), err.Error())
		hops.Add(msg)
		return nil, errors.New(msg)
	}
	if res.Content == nil {
		res.Content = map[string]any{"Elicit Result": "No content"}
	} else {
		_, data := t.assignClientHops("", res.Content, hops)
		res.Content = data
	}
	hops.Add(fmt.Sprintf("%s Server [%s] Received elicit response from client", t.Label, t.Server.GetName()))
	result := &gomcp.CallToolResult{
		Content: []gomcp.Content{
			&gomcp.TextContent{Text: "Elicit successful"},
		},
	}
	if res.Content != nil {
		result.Content = append(result.Content, &gomcp.TextContent{Text: util.ToJSONText(res.Content)})
	}
	return result, nil
}

func (t *MCPTool) fetch(ctx context.Context, req *gomcp.CallToolRequest, hops *util.Hops) (*gomcp.CallToolResult, error) {
	data := argsFromRaw(req.Params.Arguments)
	result := &gomcp.CallToolResult{}
	url := t.RemoteURL
	if len(data) > 0 && data["url"] != nil {
		url = data["url"].(string)
	}
	resp, err := http.Get(url)
	if err != nil || resp.StatusCode != 200 {
		msg := fmt.Sprintf("%s Server [%s] Failed to invoke Remote URL [%s] with error: %s", t.Label, t.Server.GetName(), t.RemoteURL, err.Error())
		hops.Add(msg)
		log.Println(msg)
		result.IsError = true
		result.Content = append(result.Content, &gomcp.TextContent{Text: msg})
	} else {
		hops.Add(fmt.Sprintf("%s Server [%s] fetched response from remote URL [%s]", t.Label, t.Server.GetName(), t.RemoteURL))
		output := util.Read(resp.Body)
		result.Content = append(result.Content, &gomcp.TextContent{Text: output})
	}
	return result, err
}

func (t *MCPTool) remoteToolCall(ctx context.Context, req *gomcp.CallToolRequest, hops *util.Hops) (*gomcp.CallToolResult, error) {
	result := &gomcp.CallToolResult{}
	var remoteResult map[string]any
	var err error
	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func() {
		sse := util.IsSSE(ctx)
		client := mcpclient.NewClientWithHops(t.Server.GetPort(), sse, t.Server.GetName(), t.Label, hops)
		if sse {
			err = client.Connect(t.RemoteSSEURL)
		} else {
			err = client.Connect(t.RemoteURL)
		}
		if err == nil {
			defer client.Close()
			args, _ := req.Params.Arguments.(map[string]any)
			remoteResult, err = client.CallTool(t.Server.GetPort(), t.RemoteTool, args)
		}
		wg.Done()
	}()
	wg.Wait()
	if err != nil {
		msg := fmt.Sprintf("%s Server [%s] Failed to invoke Remote tool [%s] at URL [%s] with error: %s", t.Label, t.Server.GetName(), t.RemoteTool, t.RemoteURL, err.Error())
		hops.Add(msg)
		log.Println(msg)
		result = &gomcp.CallToolResult{Content: []gomcp.Content{&gomcp.TextContent{Text: msg}}, IsError: true}
	} else {
		c := remoteResult["content"]
		if c != nil {
			if remoteContent, ok := c.([]any); ok {
				for _, v := range remoteContent {
					if text, ok := v.(string); ok {
						result.Content = append(result.Content, &gomcp.TextContent{Text: text})
					}
				}
			}
			delete(remoteResult, "content")
		}
		result.StructuredContent = remoteResult
		result.Content = append(result.Content, &gomcp.TextContent{Text: fmt.Sprintf("%s Server [%s] Remote tool [%s] invoked successfully on [%s]", t.Label, global.Funcs.GetListenerLabelForPort(t.Server.GetPort()), t.RemoteTool, t.RemoteURL)})
	}
	return result, err
}

func (t *MCPTool) sendPayload(ctx context.Context, req *gomcp.CallToolRequest, hops *util.Hops) (*gomcp.CallToolResult, error) {
	result := &gomcp.CallToolResult{}
	var delay time.Duration
	if t.Payload != nil {
		if t.Payload.Delay != nil {
			delay = t.Payload.Delay.Apply()
		}
		responseCount := 0
		t.Payload.RangeText(func(text string) {
			result.Content = append(result.Content, &gomcp.TextContent{Text: text})
			responseCount++
		})
		hops.Add(fmt.Sprintf("%s Server [%s] sent response: count [%d] after delay [%s]", t.Label, t.Server.GetName(), responseCount, delay))
	} else {
		result.Content = append(result.Content, &gomcp.TextContent{Text: "<No payload>"})
		hops.Add(fmt.Sprintf("%s Server [%s] sent default response after delay [%s]", t.Label, t.Server.GetName(), delay))
	}
	return result, nil
}
