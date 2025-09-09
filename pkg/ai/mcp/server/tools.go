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
	Ping          bool `json:"ping,omitempty"`
	Echo          bool `json:"echo,omitempty"`
	Time          bool `json:"time,omitempty"`
	Stream        bool `json:"stream,omitempty"`
	Elicit        bool `json:"elicit,omitempty"`
	Sample        bool `json:"sample,omitempty"`
	ListRoots     bool `json:"listRoots,omitempty"`
	Fetch         bool `json:"fetch,omitempty"`
	Remote        bool `json:"remote,omitempty"`
	MultiRemote   bool `json:"multiRemote,omitempty"`
	ServerDetails bool `json:"serverDetails,omitempty"`
	ServerPaths   bool `json:"serverPaths,omitempty"`
	AllServers    bool `json:"allServers,omitempty"`
	AllComponents bool `json:"allComponents,omitempty"`
}

type ToolConfig struct {
	Remote      *mcpclient.ToolCall     `json:"remote,omitempty"`
	MultiRemote [][]*mcpclient.ToolCall `json:"multiRemote,omitempty"`
	Delay       *util.Delay             `json:"delay,omitempty"`
}

type RemoteCallArgs struct {
	ToolName  string            `json:"tool,omitempty"`
	URL       string            `json:"url,omitempty"`
	Authority string            `json:"authority,omitempty"`
	SSE       bool              `json:"sse,omitempty"`
	Delay     string            `json:"delay,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	ToolArgs  map[string]any    `json:"args,omitempty"`
}

type ToolCallContext struct {
	*MCPTool
	sse        bool
	ctx        context.Context
	headers    map[string][]string
	req        *gomcp.CallToolRequest
	remoteArgs *RemoteCallArgs
	args       map[string]any
	hops       *util.Hops
	log        []string
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
	if tool.Name == "" {
		tool.Name = tool.Tool.Name
	}
	tool.Name = strings.ReplaceAll(tool.Name, "\"", "")
	if tool.Behavior.Fetch {
		isTLS := strings.HasPrefix(tool.Config.Remote.URL, "https:")
		client := transport.CreateDefaultHTTPClient(tool.Name, false, isTLS, metrics.ConnTracker)
		tool.client = client
	}
	if tool.Config.Delay != nil {
	}
	return
}

func (t *MCPTool) Handle(ctx context.Context, req *gomcp.CallToolRequest) (result *gomcp.CallToolResult, err error) {
	_, rs := util.GetRequestStoreFromContext(ctx)
	if rs != nil {
		if rs.RequestedMCPTool != "" && !strings.EqualFold(rs.RequestedMCPTool, t.Name) {
			return nil, fmt.Errorf("URI [%s] doesn't match tool [%s] requested in RPC", rs.RequestedMCPTool, t.Name)
		}
	}
	sCtx := t.Server.GetAndClearSessionContext(req.Session.ID())
	isSSE := false
	headers := util.GetContextHeaders(ctx)
	if sCtx != nil {
		isSSE = sCtx.RS.IsSSE
	}
	if !isSSE {
		isSSE = util.IsSSE(ctx)
	}
	var ra *RemoteCallArgs
	var args map[string]any
	if req.Params != nil && req.Params.Arguments != nil {
		if raw, ok := req.Params.Arguments.(json.RawMessage); ok {
			if t.Config.Remote != nil {
				ra, err = parseRemoteCallArgs(raw)
			} else {
				args, err = parseArgs(raw)
			}
		}
	}
	tctx := &ToolCallContext{MCPTool: t, sse: isSSE, ctx: ctx, headers: headers, req: req, args: args, remoteArgs: ra}
	result, err = tctx.RunTool()
	return
}

func (t *ToolCallContext) Log(msg string, args ...any) string {
	msg = fmt.Sprintf(msg, args...)
	t.log = append(t.log, msg)
	return msg
}

func (t *ToolCallContext) Flush(print bool) string {
	msg := strings.Join(t.log, " --> ")
	t.log = []string{}
	if print {
		log.Println(msg)
	}
	return msg
}

func (t *ToolCallContext) Hop(msg string) {
	t.hops.Add(msg)
}

func (t *ToolCallContext) RunTool() (result *gomcp.CallToolResult, err error) {
	t.Log("Args: [%+v], Remote args: [%+v]", t.args, t.remoteArgs)
	protocol := "mcp"
	if util.IsSSE(t.ctx) {
		protocol = "mcp/sse"
	}
	serverID := fmt.Sprintf("[%s][%s]", t.Server.GetName(), protocol)
	t.hops = util.NewHops(t.Server.GetHost(), serverID, t.Label)
	t.Log("%s: received request with args [%+v]", t.Label, t.args)
	if t.args["delay"] != nil {
		if delay, ok := t.args["delay"].(string); ok {
			if d, err := time.ParseDuration(delay); err == nil {
				t.Log("Server %s Tool %s: sleeping for [%s]", t.Label, t.Tool.Name, delay)
				time.Sleep(d)
			} else {
				t.Log("Server %s Tool %s: invalid delay param, not sleeping for [%s]", t.Label, t.Tool.Name, delay)
			}
		}
	}
	t.Hop(t.Flush(true))
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
	} else if t.Behavior.ServerDetails {
		result, err = t.sendServerDetails()
	} else if t.Behavior.ServerPaths {
		result, err = t.sendServerPaths()
	} else if t.Behavior.AllServers {
		result, err = t.sendAllServers()
	} else if t.Behavior.AllComponents {
		result, err = t.sendAllComponents()
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
	t.Hop(t.Flush(true))
	_, rs := util.GetRequestStoreFromContext(t.ctx)
	rs.GotoProtocol = "MCP"
	rs.IsJSONRPC = true
	rs.IsMCP = true
	rs.RequestPortNum = t.MCPTool.Server.Port
	output["Goto-Server-Info"] = echo.GetEchoResponseFromRS(rs)
	output["hops"] = t.hops.Steps
	if t.headers != nil {
		outHeaders := map[string][]string{}
		util.CopyHeadersWithPrefix("Request", t.headers, outHeaders)
	}

	result.StructuredContent = output
	return
}

func parseArgs(raw json.RawMessage) (args map[string]any, err error) {
	if len(raw) > 0 {
		args = map[string]any{}
		err = json.Unmarshal([]byte(raw), &args)
	}
	return
}

func parseRemoteCallArgs(raw json.RawMessage) (ra *RemoteCallArgs, err error) {
	if len(raw) > 0 {
		ra = &RemoteCallArgs{}
		err = json.Unmarshal([]byte(raw), ra)
	}
	// if t.args["sse"] != nil {
	// 	if v, ok := t.args["sse"].(bool); ok {
	// 		isSSE = v
	// 	}
	// }
	// if t.args["url"] != nil {
	// 	if v, ok := t.args["url"].(string); ok {
	// 		url = v
	// 		argURL = true
	// 	}
	// }
	// if t.args["tool"] != nil {
	// 	if v, ok := t.args["tool"].(string); ok {
	// 		tc.Tool = v
	// 	}
	// }
	// if t.args["authority"] != nil {
	// 	if v, ok := t.args["authority"].(string); ok {
	// 		tc.Authority = v
	// 		tc.Server = tc.Authority
	// 	}
	// }
	// if t.args["delay"] != nil {
	// 	if v, ok := t.args["delay"].(string); ok {
	// 		tc.Delay = v
	// 	}
	// }
	// if t.args["headers"] != nil {
	// 	if v, ok := t.args["headers"].(map[string]any); ok {
	// 		headers := v
	// 		for h, v := range headers {
	// 			if v2, ok := v.(string); ok {
	// 				tc.Headers[h] = []string{v2}
	// 			}
	// 		}
	// 	}
	// }
	// if t.args["args"] != nil {
	// 	tc.Args = t.args["args"].(map[string]any)
	// }
	return
}

func (t *ToolCallContext) echo() (*gomcp.CallToolResult, error) {
	content := []gomcp.Content{}
	content = append(content, &gomcp.TextContent{Text: fmt.Sprintf("Echo Server: %s[%s]", t.Label, global.Funcs.GetListenerLabelForPort(t.Server.GetPort()))})
	if len(t.args) > 0 {
		for k, v := range t.args {
			msg := fmt.Sprintf("Received [%s: %s]", k, v)
			t.Log(msg)
		}
		content = append(content, &gomcp.TextContent{Text: t.Flush(false)})
	} else {
		msg := fmt.Sprintf("Server [%s] Tool.echo: Received no input, still echoing...", t.Server.GetName())
		t.Log(msg)
		content = append(content, &gomcp.TextContent{Text: msg})
	}
	t.Log("Server [%s] echoed back", t.Server.GetName())
	return &gomcp.CallToolResult{Content: content}, nil
}

func (t *ToolCallContext) ping() (*gomcp.CallToolResult, error) {
	if err := t.req.Session.Ping(t.ctx, &gomcp.PingParams{}); err != nil {
		return nil, fmt.Errorf("ping failed")
	}
	t.Log("Server [%s] pinged client", t.Server.GetName())
	return &gomcp.CallToolResult{
		Content: []gomcp.Content{
			&gomcp.TextContent{Text: fmt.Sprintf("%s Ping from Goto MCP successful", t.Label)},
		},
	}, nil
}

func (t *ToolCallContext) sendTime() (*gomcp.CallToolResult, error) {
	content := []gomcp.Content{&gomcp.TextContent{Text: fmt.Sprintf("Time: %s", time.Now().Format(time.RFC3339))}}
	content = append(content, &gomcp.TextContent{Text: fmt.Sprintf("Client Data: %s", t.req.Params.Arguments)})
	t.Log("Server [%s] sent time back", t.Server.GetName())
	t.hops.Add(t.Flush(true))
	return &gomcp.CallToolResult{Content: content}, nil
}

func (t *ToolCallContext) listRoots() (*gomcp.CallToolResult, error) {
	res, err := t.req.Session.ListRoots(t.ctx, &gomcp.ListRootsParams{})
	if err != nil {
		t.Log("Server [%s] failed to get roots from client", t.Server.GetName())
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
	t.Log("Server [%s] Reporting client's roots", t.Server.GetName())
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
		t.Log("Server [%s] failed to get sample from client", t.Server.GetName())
		return nil, fmt.Errorf("sampling failed: %v", err)
	}
	t.Log("Server [%s] got sample from client", t.Server.GetName())
	var data map[string]any
	if res.Content == nil {
		res.Content = &gomcp.TextContent{Text: "No content"}
	} else {
		if tc, ok := res.Content.(*gomcp.TextContent); ok {
			data = t.assignClientHops(tc.Text, nil)
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

func (t *ToolCallContext) assignClientHops(text string, data map[string]any) map[string]any {
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
	t.Log(fmt.Sprintf("Server [%s] sent elicit request to client", t.Server.GetName()))
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
		msg = fmt.Sprintf("Server [%s] failed to get elicit response from client with error [%s]", t.Server.GetName(), err.Error())
		t.Log(msg)
		return nil, errors.New(msg)
	}
	if res.Action == "decline" {
		t.Log("%s Client declined Elicitation", t.Label)
	}
	if res.Content == nil {
		t.Log("Server [%s] Empty elicit response from client", t.Server.GetName())
	} else {
		t.Log("Server [%s] Received elicit response from client", t.Server.GetName())
		data := t.assignClientHops("", res.Content)
		res.Content = map[string]any{"clientResponse": data}
	}
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
	result := &gomcp.CallToolResult{}
	url := t.Config.Remote.URL
	var headers map[string]any
	var authority string
	if len(t.args) > 0 {
		t.Log("Received args: [%+v]", t.args)
		if t.args["url"] != nil {
			url = t.args["url"].(string)
		}
		if t.args["headers"] != nil {
			headers = t.args["headers"].(map[string]any)
		}
		if t.args["authority"] != nil {
			authority = t.args["authority"].(string)
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
		msg = fmt.Sprintf("Server [%s] Failed to invoke Remote URL [%s] with error: %s", t.Server.GetName(), url, err.Error())
		t.Log(msg)
		result.IsError = true
		result.Content = append(result.Content, &gomcp.TextContent{Text: msg})
	} else {
		t.Log(fmt.Sprintf("Server [%s] fetched response from remote URL [%s]", t.Server.GetName(), url))
		output := util.Read(resp.Body)
		result.Content = append(result.Content, &gomcp.TextContent{Text: output})
	}
	return result, err
}

func (t *ToolCallContext) remoteToolCall() (*gomcp.CallToolResult, error) {
	result := &gomcp.CallToolResult{}
	tc := *t.Config.Remote
	isSSE := t.sse
	url := tc.URL
	argURL := false
	if t.remoteArgs != nil {
		t.Log("Received args: [%+v]", t.remoteArgs)
		if t.remoteArgs.SSE {
			isSSE = true
		}
		if t.remoteArgs.URL != "" {
			url = t.remoteArgs.URL
			argURL = true
		}
		if t.remoteArgs.ToolName != "" {
			tc.Tool = t.remoteArgs.ToolName
		}
		if t.remoteArgs.Authority != "" {
			tc.Authority = t.remoteArgs.Authority
			tc.Server = tc.Authority
		}
		if t.remoteArgs.Delay != "" {
			tc.Delay = t.remoteArgs.Delay
		}
		if t.remoteArgs.Headers != nil {
			for h, v := range t.remoteArgs.Headers {
				tc.Headers[h] = []string{v}
			}
		}
		if t.remoteArgs.ToolArgs != nil {
			tc.Args = t.remoteArgs.ToolArgs
		}
	}
	if tc.ForceSSE {
		isSSE = true
	}
	if isSSE && !argURL {
		// url = tc.SSEURL
	}
	operLabel := fmt.Sprintf("%s->%s@%s", t.Label, tc.Tool, tc.Server)
	var remoteResult map[string]any
	var err error
	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func() {
		client := mcpclient.NewClient(t.Server.GetPort(), false, t.Label)
		var session *mcpclient.MCPSession
		session, err = client.ConnectWithHops(url, t.Name, t.hops)
		if err == nil {
			defer session.Close()
			remoteResult, err = session.CallTool(&tc, tc.Args)
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
		msg := fmt.Sprintf("Remote operation [%s] successful on [%s]", operLabel, tc.URL)
		result.Content = remoteResult["content"].([]gomcp.Content)
		output := map[string]any{}
		output["upstreamContent"] = remoteResult["structuredContent"]
		output["toolResult"] = msg
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
		t.Log(fmt.Sprintf("%s Server [%s] sent response: count [%d] after delay [%s]", t.Label, t.Server.GetName(), responseCount, delay))
	} else {
		result.Content = append(result.Content, &gomcp.TextContent{Text: "<No payload>"})
		t.Log(fmt.Sprintf("%s Server [%s] sent default response after delay [%s]", t.Label, t.Server.GetName(), delay))
	}
	return result, nil
}

func (t *ToolCallContext) sendServerDetails() (*gomcp.CallToolResult, error) {
	result := &gomcp.CallToolResult{}
	result.Content = append(result.Content, &gomcp.TextContent{Text: util.ToJSONText(t.MCPTool.Server)})
	t.Log(fmt.Sprintf("%s sent Server [%s] details", t.Label, t.Server.GetName()))
	return result, nil
}

func (t *ToolCallContext) sendAllServers() (*gomcp.CallToolResult, error) {
	result := &gomcp.CallToolResult{}
	result.Content = append(result.Content, &gomcp.TextContent{Text: util.ToJSONText(t.MCPTool.Server.ps)})
	t.Log(fmt.Sprintf("%s sent All Servers", t.Label))
	return result, nil
}

func (t *ToolCallContext) sendServerPaths() (*gomcp.CallToolResult, error) {
	result := &gomcp.CallToolResult{}
	result.Content = append(result.Content, &gomcp.TextContent{Text: util.ToJSONText(ServerRoutes)})
	t.Log(fmt.Sprintf("%s sent Server Routes", t.Label))
	return result, nil
}

func (t *ToolCallContext) sendAllComponents() (*gomcp.CallToolResult, error) {
	result := &gomcp.CallToolResult{}
	result.Content = append(result.Content, &gomcp.TextContent{Text: util.ToJSONText(AllComponents)})
	t.Log(fmt.Sprintf("%s sent all components", t.Label))
	return result, nil
}
