package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"goto/pkg/global"
	"goto/pkg/metrics"
	"goto/pkg/server/echo"
	"goto/pkg/transport"
	"goto/pkg/util"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	mcpclient "goto/pkg/ai/mcp/client"

	"github.com/google/jsonschema-go/jsonschema"
	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type MCPTool struct {
	MCPComponent
	Tool     *gomcp.Tool  `json:"tool"`
	Behavior ToolBehavior `json:"behavior,omitempty"`
	Config   ToolConfig   `json:"config"`
	client   transport.ClientTransport
}

type ToolBehavior struct {
	Ping      bool `json:"ping,omitempty"`
	Echo      bool `json:"echo,omitempty"`
	Time      bool `json:"time,omitempty"`
	Stream    bool `json:"stream,omitempty"`
	Elicit    bool `json:"elicit,omitempty"`
	Sample    bool `json:"sample,omitempty"`
	ListRoots bool `json:"listRoots,omitempty"`
	Fetch     bool `json:"fetch,omitempty"`
	Remote    bool `json:"remote,omitempty"`
}

type ToolConfig struct {
	Remote *mcpclient.ToolCall `json:"remote,omitempty"`
	Delay  *util.Delay         `json:"delay,omitempty"`
}

type ToolCallContext struct {
	*MCPTool
	ctx     context.Context
	headers http.Header
	req     *gomcp.CallToolRequest
	hops    *util.Hops
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

func ParseTool(payload []byte) (tool *MCPTool, err error) {
	tool = &MCPTool{}
	if err = json.Unmarshal(payload, tool); err != nil {
		return
	}
	tool.Kind = KindTools
	if (tool.Behavior.Remote || tool.Behavior.Fetch) && (tool.Config.Remote == nil || tool.Config.Remote.URL == "") {
		err = errors.New("remote config required")
	}
	tool.Name = strings.ReplaceAll(tool.Name, "\"", "")
	if tool.Behavior.Fetch {
		isTLS := strings.HasPrefix(tool.Config.Remote.URL, "https:")
		client, _ := transport.CreateDefaultHTTPClient(tool.Name, false, isTLS, metrics.ConnTracker)
		tool.client = client
	}
	return
}

func (t *MCPTool) Handle(ctx context.Context, req *gomcp.CallToolRequest) (result *gomcp.CallToolResult, err error) {
	var headers http.Header
	var w http.ResponseWriter
	sCtx := t.Server.GetAndClearSessionContext(req.Session.ID())
	if sCtx != nil {
		headers = sCtx.Request.Header
		w = sCtx.Writer
	}
	tctx := &ToolCallContext{MCPTool: t, ctx: ctx, headers: headers, req: req}
	if w == nil {
		log.Println("no http request/response available in context")
	} else {
		w.Header().Add("Goto-Server", t.Label)
	}
	result, err = tctx.RunTool()
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

func (t *ToolCallContext) RunTool() (result *gomcp.CallToolResult, err error) {
	protocol := "mcp"
	if util.IsSSE(t.ctx) {
		protocol = "mcp/sse"
	}
	serverID := fmt.Sprintf("[%s][%s]", t.Server.GetName(), protocol)
	t.hops = util.NewHops(t.Server.GetHost(), serverID, t.Label)
	t.hops.Add(fmt.Sprintf("Server %s received call for tool [%s]", t.Label, t.Tool.Name))

	if t.Behavior.Echo {
		result, err = t.echo()
	} else if t.Behavior.Ping {
		result, err = t.ping()
	} else if t.Behavior.Time {
		result, err = t.sendTime()
	} else if t.Behavior.ListRoots {
		result, err = t.listRoots()
	} else if t.Behavior.Sample {
		result, err = t.sample()
	} else if t.Behavior.Elicit {
		result, err = t.elicit()
	} else if t.Behavior.Fetch {
		result, err = t.fetch()
	} else if t.Behavior.Remote {
		result, err = t.remoteToolCall()
	} else {
		result, err = t.sendPayload()
	}
	if result == nil {
		result = &gomcp.CallToolResult{}
	}
	output := map[string]any{}
	if result.StructuredContent != nil {
		toolOutput := result.StructuredContent.(map[string]any)
		if toolOutput["upstreamContent"] != nil {
			output["upstreamContent"] = toolOutput["upstreamContent"]
		}
		if toolOutput["toolResult"] != nil {
			output[fmt.Sprintf("result/%s/%s", t.Server.GetName(), t.Name)] = toolOutput["toolResult"]
		}
	}
	_, rs := util.GetRequestStoreForContext(t.ctx)
	output["Goto-Server-Info"] = echo.GetEchoResponseFromRS(rs)
	t.hops.Add(fmt.Sprintf("Server %s finished call for tool [%s]", t.Label, t.Tool.Name))
	output["hops"] = t.hops.Steps
	result.StructuredContent = output
	return
}

func (t *ToolCallContext) echo() (*gomcp.CallToolResult, error) {
	content := []gomcp.Content{}
	data := argsFromRaw(t.req.Params.Arguments)
	content = append(content, &gomcp.TextContent{Text: fmt.Sprintf("Echo Server: %s[%s]", t.Label, global.Funcs.GetListenerLabelForPort(t.Server.GetPort()))})
	if len(data) > 0 {
		for k, v := range data {
			content = append(content, &gomcp.TextContent{Text: fmt.Sprintf("%s Received [%s: %s]", t.Label, k, v)})
		}
	} else {
		content = append(content, &gomcp.TextContent{Text: fmt.Sprintf("%s Tool.echo: Received no input, still echoing...", t.Label)})
	}
	t.hops.Add(fmt.Sprintf("%s Server [%s] echoed back", t.Label, t.Server.GetName()))
	return &gomcp.CallToolResult{Content: content}, nil
}

func (t *ToolCallContext) ping() (*gomcp.CallToolResult, error) {
	if err := t.req.Session.Ping(t.ctx, &gomcp.PingParams{}); err != nil {
		return nil, fmt.Errorf("ping failed")
	}
	t.hops.Add(fmt.Sprintf("Server [%s] pinged client", t.Server.GetName()))
	return &gomcp.CallToolResult{
		Content: []gomcp.Content{
			&gomcp.TextContent{Text: fmt.Sprintf("%s Ping from Goto MCP successful", t.Label)},
		},
	}, nil
}

func (t *ToolCallContext) sendTime() (*gomcp.CallToolResult, error) {
	content := []gomcp.Content{&gomcp.TextContent{Text: fmt.Sprintf("Time: %s", time.Now().Format(time.RFC3339))}}
	content = append(content, &gomcp.TextContent{Text: fmt.Sprintf("Client Data: %s", t.req.Params.Arguments)})
	// if args, ok := t.req.Params.Arguments.(map[string]any); ok {
	// 	for k, v := range args {
	// 		content = append(content, &gomcp.TextContent{Text: fmt.Sprintf("%s: %s", k, v)})
	// 	}
	// } else {
	// }
	t.hops.Add(fmt.Sprintf("%s Server [%s] sent time back", t.Label, t.Server.GetName()))
	return &gomcp.CallToolResult{Content: content}, nil
}

func (t *ToolCallContext) listRoots() (*gomcp.CallToolResult, error) {
	res, err := t.req.Session.ListRoots(t.ctx, &gomcp.ListRootsParams{})
	if err != nil {
		t.hops.Add(fmt.Sprintf("%s Server [%s] failed to get roots from client", t.Label, t.Server.GetName()))
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
	t.hops.Add(fmt.Sprintf("%s Server [%s] Reporting client's roots", t.Label, t.Server.GetName()))
	result := &gomcp.CallToolResult{}
	for _, r := range roots {
		result.Content = append(result.Content, &gomcp.TextContent{Text: r})
	}
	return result, nil
}

func (t *ToolCallContext) sample() (*gomcp.CallToolResult, error) {
	res, err := t.req.Session.CreateMessage(t.ctx, &gomcp.CreateMessageParams{
		Messages: []*gomcp.SamplingMessage{{
			Role:    "user",
			Content: &gomcp.TextContent{Text: util.ToJSONText(t.Response)},
		}},
		IncludeContext: "allServers",
		SystemPrompt:   t.Tool.Description,
		MaxTokens:      10,
	})
	if err != nil {
		t.hops.Add(fmt.Sprintf("%s Server [%s] failed to get sample from client", t.Label, t.Server.GetName()))
		return nil, fmt.Errorf("sampling failed: %v", err)
	}
	t.hops.Add(fmt.Sprintf("%s Server [%s] got sample from client", t.Label, t.Server.GetName()))
	var data map[string]any
	if res.Content == nil {
		res.Content = &gomcp.TextContent{Text: "No content"}
	} else {
		if tc, ok := res.Content.(*gomcp.TextContent); ok {
			data = t.assignClientHops(tc.Text, nil, t.hops)
		}
	}
	result := &gomcp.CallToolResult{}
	result.Content = []gomcp.Content{
		&gomcp.TextContent{Text: "Sampling successful"},
		&gomcp.TextContent{Text: "Model: " + res.Model},
		&gomcp.TextContent{Text: "Role: " + string(res.Role)},
		&gomcp.TextContent{Text: "StopReason: " + res.StopReason},
	}
	if len(data) > 0 {
		for k, v := range data {
			result.Content = append(result.Content, &gomcp.TextContent{Text: fmt.Sprintf("%+v: %+v", k, v)})
		}
	} else {
		res.Content = &gomcp.TextContent{Text: "No content"}
	}
	return result, nil
}

func (t *ToolCallContext) assignClientHops(text string, data map[string]any, hops *util.Hops) map[string]any {
	if data == nil {
		json := util.JSONFromJSONText(text)
		if json != nil {
			data = json.Object()
		}
	}
	if s := data["hops"]; s != nil {
		if steps, ok := s.([]any); ok {
			for _, step := range steps {
				t.hops.AddRemote(step)
			}
		}
		delete(data, "hops")
	}
	if len(data) == 0 {
		data = nil
	}
	return data
}

func (t *ToolCallContext) elicit() (*gomcp.CallToolResult, error) {
	t.hops.Add(fmt.Sprintf("%s Server [%s] sent elicit request to client", t.Label, t.Server.GetName()))
	params := &gomcp.ElicitParams{}
	if t.Response != nil && t.Response.JSON != nil {
		params.Message = t.Response.JSON.GetText("message")
		schema := t.Response.JSON.Get("requestedSchema")
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
	res, err := t.req.Session.Elicit(t.ctx, params)
	var msg string
	if err != nil {
		msg = fmt.Sprintf("%s Server [%s] failed to get elicit response from client with error [%s]", t.Label, t.Server.GetName(), err.Error())
		t.hops.Add(msg)
		return nil, errors.New(msg)
	}
	if res.Content == nil {
		msg = fmt.Sprintf("%s Server [%s] Empty elicit response from client", t.Label, t.Server.GetName())
	} else {
		msg = fmt.Sprintf("%s Server [%s] Received elicit response from client", t.Label, t.Server.GetName())
		data := t.assignClientHops("", res.Content, t.hops)
		res.Content = map[string]any{"clientResponse": data}
	}
	t.hops.Add(msg)
	result := &gomcp.CallToolResult{
		Content: []gomcp.Content{
			&gomcp.TextContent{Text: msg},
		},
	}
	if res.Content != nil {
		result.Content = append(result.Content, &gomcp.TextContent{Text: util.ToJSONText(res.Content)})
	}
	return result, nil
}

func (t *ToolCallContext) fetch() (*gomcp.CallToolResult, error) {
	data := argsFromRaw(t.req.Params.Arguments)
	result := &gomcp.CallToolResult{}
	url := t.Config.Remote.URL
	var headers map[string]any
	var authority string
	if len(data) > 0 {
		if data["url"] != nil {
			url = data["url"].(string)
		}
		if data["headers"] != nil {
			headers = data["headers"].(map[string]any)
		}
		if data["authority"] != nil {
			authority = data["authority"].(string)
		}
	}
	if !strings.HasPrefix(url, "http") {
		url = "http://" + url
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	for h, v := range headers {
		req.Header.Add(h, v.(string))
	}
	if authority != "" {
		req.Host = authority
	}
	if req.Host == "" {
		req.Host = req.URL.Host
	}
	resp, err := t.client.HTTP().Do(req)
	msg := ""
	if err != nil {
		msg = fmt.Sprintf("%s Server [%s] Failed to invoke Remote URL [%s] with error: %s", t.Label, t.Server.GetName(), url, err.Error())
		t.hops.Add(msg)
		log.Println(msg)
		result.IsError = true
		result.Content = append(result.Content, &gomcp.TextContent{Text: msg})
	} else {
		t.hops.Add(fmt.Sprintf("%s Server [%s] fetched response from remote URL [%s]", t.Label, t.Server.GetName(), url))
		output := util.Read(resp.Body)
		result.Content = append(result.Content, &gomcp.TextContent{Text: output})
	}
	return result, err
}

func (t *ToolCallContext) remoteToolCall() (*gomcp.CallToolResult, error) {
	result := &gomcp.CallToolResult{}
	tc := *t.Config.Remote
	args := argsFromRaw(t.req.Params.Arguments)
	if args["tool"] != nil {
		tc.Tool = args["tool"].(string)
	}
	if args["url"] != nil {
		tc.URL = args["url"].(string)
		tc.Server = tc.URL
	}
	if args["authority"] != nil {
		tc.Authority = args["authority"].(string)
		tc.Server = tc.Authority
	}
	if args["sse"] != nil {
		tc.SSE = args["sse"].(bool)
	}
	if args["headers"] != nil {
		headers := args["headers"].(map[string]any)
		for h, v := range headers {
			tc.Headers[h] = v.(string)
		}
	}
	if args["args"] != nil {
		tc.Args = args["args"].(map[string]any)
	}
	operLabel := fmt.Sprintf("%s->%s@%s", t.Label, tc.Tool, tc.Server)
	var remoteResult map[string]any
	var err error
	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func() {
		client := mcpclient.NewClientWithHops(t.Server.GetPort(), tc.SSE, t.Server.GetName(), operLabel, tc.Authority, t.hops)
		defer client.Close()
		remoteResult, err = client.CallTool(t.Server.GetPort(), &tc, tc.Args)
		wg.Done()
	}()
	wg.Wait()
	if err != nil {
		msg := fmt.Sprintf("%s Server [%s] Failed to invoke Remote tool [%s] at URL [%s] with error: %s",
			operLabel, t.Server.GetName(), tc.Tool, tc.URL, err.Error())
		t.hops.Add(msg)
		log.Println(msg)
		result = &gomcp.CallToolResult{Content: []gomcp.Content{&gomcp.TextContent{Text: msg}}, IsError: true}
	} else {
		result.Content = remoteResult["content"].([]gomcp.Content)
		output := map[string]any{}
		output["upstreamContent"] = remoteResult["structuredContent"]
		output["toolResult"] = fmt.Sprintf("%s Remote operation [%s] successful on [%s]", t.Label, operLabel, tc.URL)
		result.StructuredContent = output
	}
	return result, err
}

func (t *ToolCallContext) sendPayload() (*gomcp.CallToolResult, error) {
	result := &gomcp.CallToolResult{}
	var delay time.Duration
	d := t.Config.Delay
	if t.Response != nil && t.Response.Delay != nil {
		d = t.Response.Delay
	}
	if d != nil {
		delay = d.Apply()
	}
	if t.Response != nil {
		responseCount := 0
		total := t.Response.Count()
		params := &gomcp.ProgressNotificationParams{
			ProgressToken: t.req.Params.Meta.GetMeta()["progressToken"],
			Total:         float64(total),
		}
		t.Response.RangeText(func(text string) {
			responseCount++
			if t.Behavior.Stream {
				params.Progress = float64(total) / float64(responseCount)
				params.Message = fmt.Sprintf("[%d] done, only [%d] more to go", responseCount, total-responseCount)
				t.req.Session.NotifyProgress(t.ctx, params)
			}
			result.Content = append(result.Content, &gomcp.TextContent{Text: text})
		})
		t.hops.Add(fmt.Sprintf("%s Server [%s] sent response: count [%d] after delay [%s]", t.Label, t.Server.GetName(), responseCount, delay))
	} else {
		result.Content = append(result.Content, &gomcp.TextContent{Text: "<No payload>"})
		t.hops.Add(fmt.Sprintf("%s Server [%s] sent default response after delay [%s]", t.Label, t.Server.GetName(), delay))
	}
	return result, nil
}
