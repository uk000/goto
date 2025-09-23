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
	"goto/pkg/types"
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
	Delay       *types.Delay            `json:"delay,omitempty"`
}

type RemoteCallArgs struct {
	ToolName       string            `json:"tool,omitempty"`
	URL            string            `json:"url,omitempty"`
	Authority      string            `json:"authority,omitempty"`
	SSE            bool              `json:"sse,omitempty"`
	Delay          string            `json:"delay,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	ForwardHeaders []string          `json:"forwardHeaders,omitempty"`
	ToolArgs       map[string]any    `json:"args,omitempty"`
}

type ToolCallContext struct {
	*MCPTool
	rs         *util.RequestStore
	sse        bool
	ctx        context.Context
	headers    map[string][]string
	req        *gomcp.CallToolRequest
	args       map[string]any  //for all tools except remote tools
	remoteArgs *RemoteCallArgs //for remote tools
	delay      *types.Delay
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
	return
}

func (t *MCPTool) Handle(ctx context.Context, req *gomcp.CallToolRequest) (result *gomcp.CallToolResult, err error) {
	_, rs := util.GetRequestStoreFromContext(ctx)
	isSSE := false
	if rs != nil {
		if rs.RequestedMCPTool != "" && !strings.EqualFold(rs.RequestedMCPTool, t.Name) && !strings.Contains(t.Name, "toolcall") {
			return nil, fmt.Errorf("URI [%s] doesn't match tool [%s] requested in RPC", rs.RequestedMCPTool, t.Name)
		}
		isSSE = rs.IsSSE
	}
	if !isSSE {
		isSSE = util.IsSSE(ctx)
	}
	headers := req.Extra.Header
	if rs != nil {
		if headers != nil {
			rs.RequestHeaders = headers
		} else {
			headers = rs.RequestHeaders
		}
	}
	if headers == nil {
		headers = util.GetContextHeaders(ctx)
	}
	if headers != nil && rs != nil {
		headers["RequestURI"] = []string{rs.RequestURI}
		headers["RequestHost"] = []string{rs.RequestHost}
	}
	var remoteArgs *RemoteCallArgs
	var args map[string]any
	if req.Params != nil && req.Params.Arguments != nil {
		if t.Config.Remote != nil {
			remoteArgs, err = parseRemoteCallArgs(req.Params.Arguments)
		} else {
			args, err = parseArgs(req.Params.Arguments)
		}
	}
	delay := t.Config.Delay
	if args["delay"] != nil {
		if d, ok := args["delay"].(string); ok {
			if delay2 := types.ParseDelay(d); delay2 != nil {
				delay = delay2
			}
		}
	}
	tctx := &ToolCallContext{MCPTool: t, rs: rs, sse: isSSE, ctx: ctx, headers: headers, req: req, args: args, remoteArgs: remoteArgs, delay: delay}
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
	if msg != "" {
		t.hops.Add(msg)
	}
}

func (t *ToolCallContext) applyDelay() {
	if t.delay != nil {
		d := t.delay.Compute()
		t.notifyClient(t.Log("Server %s Tool %s: sleeping for [%s]", t.Label, t.Tool.Name, d), 0)
		t.delay.Apply()
	}
}

func (t *ToolCallContext) RunTool() (result *gomcp.CallToolResult, err error) {
	t.hops = util.NewHops(t.Server.ID, t.Label)
	t.notifyClient(t.Log("%s: Received request with Args [%+v] Remote Args [%+v] Headers [%+v]", t.Label, t.args, t.remoteArgs, t.headers), 0)
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
		output = result.StructuredContent.(map[string]any)
	}
	t.Hop(t.Flush(true))
	t.rs.GotoProtocol = "MCP"
	t.rs.IsJSONRPC = true
	t.rs.RequestPortNum = t.MCPTool.Server.Port
	if t.rs.RequestHeaders == nil {
		t.rs.RequestHeaders = t.headers
	}
	output["Goto-Server-Info"] = echo.GetEchoResponseFromRS(t.rs)
	output["hops"] = t.hops.Steps
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
	return
}

func (t *ToolCallContext) echo() (*gomcp.CallToolResult, error) {
	content := []gomcp.Content{}
	input := ""
	if t.args != nil && t.args["text"] != nil {
		input = t.args["text"].(string)
	} else {
		input = "<No input to echo>"
	}
	msg := fmt.Sprintf("Echo Server: %s[%s]. Input: %s", t.Label, global.Funcs.GetListenerLabelForPort(t.Server.GetPort()), input)
	content = append(content, &gomcp.TextContent{Text: msg})
	t.applyDelay()
	t.notifyClient(t.Log("Server [%s] echoed back", t.Server.GetName()), 0)
	return &gomcp.CallToolResult{Content: content}, nil
}

func (t *ToolCallContext) ping() (*gomcp.CallToolResult, error) {
	if err := t.req.Session.Ping(t.ctx, &gomcp.PingParams{}); err != nil {
		return nil, fmt.Errorf("ping failed")
	}
	t.Log("Server [%s] pinged client", t.Server.GetName())
	t.applyDelay()
	return &gomcp.CallToolResult{
		Content: []gomcp.Content{
			&gomcp.TextContent{Text: fmt.Sprintf("%s Ping from Goto MCP successful", t.Label)},
		},
	}, nil
}

func (t *ToolCallContext) sendTime() (*gomcp.CallToolResult, error) {
	content := []gomcp.Content{&gomcp.TextContent{Text: fmt.Sprintf("Time: %s", time.Now().Format(time.RFC3339))}}
	content = append(content, &gomcp.TextContent{Text: fmt.Sprintf("Client Data: %s", t.req.Params.Arguments)})
	t.notifyClient(t.Log("Server [%s] sent time back", t.Server.GetName()), 0)
	t.applyDelay()
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
	t.notifyClient(t.Log("Server [%s] Reporting client's roots", t.Server.GetName()), 0)
	t.applyDelay()
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
	t.notifyClient(t.Log("Server [%s] got sample from client", t.Server.GetName()), 0)
	t.applyDelay()
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
		t.notifyClient(t.Log("Server [%s] Empty elicit response from client", t.Server.GetName()), 0)
	} else {
		t.notifyClient(t.Log("Server [%s] Received elicit response from client", t.Server.GetName()), 0)
		data := t.assignClientHops("", res.Content)
		res.Content = map[string]any{"clientResponse": data}
	}
	t.applyDelay()
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
	authority := t.Config.Remote.Authority
	if t.remoteArgs == nil {
		t.remoteArgs = &RemoteCallArgs{}
	}
	if t.remoteArgs.URL != "" {
		url = t.remoteArgs.URL
	}
	if t.remoteArgs.Authority != "" {
		authority = t.remoteArgs.Authority
	}
	forwardHeaders := map[string]bool{}
	for _, h := range t.remoteArgs.ForwardHeaders {
		forwardHeaders[h] = true
	}
	for _, h := range t.Config.Remote.ForwardHeaders {
		forwardHeaders[h] = true
	}
	if !strings.HasPrefix(url, "http") {
		url = "http://" + url
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	for h, v := range t.remoteArgs.Headers {
		req.Header.Add(h, v)
	}
	if t.headers != nil {
		for h := range forwardHeaders {
			if t.headers[h] != nil {
				req.Header[h] = t.headers[h]
			}
		}
	}
	if authority != "" {
		req.Host = t.remoteArgs.Authority
	}
	if req.Host == "" {
		req.Host = req.URL.Host
	}
	util.PrintRequest("Tool Remote HTTP Call Request Details", req)
	resp, err := t.client.HTTP().Do(req)
	msg := ""
	if err != nil {
		msg = fmt.Sprintf("Server [%s] Failed to invoke Remote URL [%s] with error: %s", t.Server.GetName(), url, err.Error())
		t.Log(msg)
		result.IsError = true
		result.Content = append(result.Content, &gomcp.TextContent{Text: msg})
	} else {
		t.notifyClient(t.Log(fmt.Sprintf("Server [%s] fetched response from remote URL [%s]", t.Server.GetName(), url)), 0)
		output := util.Read(resp.Body)
		result.Content = append(result.Content, &gomcp.TextContent{Text: output})
	}
	t.applyDelay()
	return result, err
}

func (t *ToolCallContext) addForwardHeaders(tc *mcpclient.ToolCall) {
	forwardHeaders := map[string]bool{}
	if t.remoteArgs.ToolArgs["forwardHeaders"] != nil {
		if arr, ok := t.remoteArgs.ToolArgs["forwardHeaders"].([]string); ok {
			for _, h := range arr {
				forwardHeaders[h] = true
			}
		}
	}
	if t.remoteArgs.ForwardHeaders == nil {
		t.remoteArgs.ForwardHeaders = []string{}
	}
	t.remoteArgs.ForwardHeaders = append(t.remoteArgs.ForwardHeaders, t.Config.Remote.ForwardHeaders...)
	for _, h := range t.remoteArgs.ForwardHeaders {
		forwardHeaders[h] = true
	}
	toolForwardHeaders := []string{}
	for h := range forwardHeaders {
		toolForwardHeaders = append(toolForwardHeaders, h)
	}
	t.remoteArgs.ToolArgs["forwardHeaders"] = toolForwardHeaders
	tc.Args["forwardHeaders"] = toolForwardHeaders
	if t.headers != nil {
		if tc.Headers == nil {
			tc.Headers = map[string][]string{}
		}
		for _, h := range t.remoteArgs.ForwardHeaders {
			for h2, v2 := range t.headers {
				if strings.EqualFold(h, h2) {
					tc.Headers[h] = v2
					break
				}
			}
		}
	}
}

func (t *ToolCallContext) remoteToolCall() (*gomcp.CallToolResult, error) {
	result := &gomcp.CallToolResult{}
	if t.remoteArgs == nil {
		t.remoteArgs = &RemoteCallArgs{}
	}
	if t.remoteArgs.ToolArgs == nil {
		t.remoteArgs.ToolArgs = map[string]any{}
	}
	tc := t.Config.Remote.UpdateAndClone(t.remoteArgs.ToolName, t.remoteArgs.URL, "", t.remoteArgs.Authority,
		t.remoteArgs.Delay, t.remoteArgs.Headers, t.remoteArgs.ToolArgs)
	t.addForwardHeaders(tc)
	isSSE := t.sse
	if t.remoteArgs.SSE || tc.ForceSSE {
		isSSE = true
	}
	url := tc.URL
	argHasURL := !strings.EqualFold(t.Config.Remote.URL, t.remoteArgs.URL)
	if isSSE && !argHasURL {
		// url = tc.SSEURL
	}
	operLabel := fmt.Sprintf("%s->%s@%s", t.Label, tc.Tool, tc.Server)
	var remoteResult map[string]any
	var err error
	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func() {
		client := mcpclient.NewClient(t.Server.GetPort(), false, t.Server.ID, nil)
		var session *mcpclient.MCPSession
		session, err = client.ConnectWithHops(url, t.Label, t.hops)
		if err == nil {
			defer session.Close()
			remoteResult, err = session.CallTool(tc, tc.Args)
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

func (t *ToolCallContext) sendPayload() (*gomcp.CallToolResult, error) {
	result := &gomcp.CallToolResult{}
	var delay time.Duration
	d := t.delay
	if t.Response != nil && t.Response.Delay != nil {
		if t.Response.Delay.IsLargerThan(t.delay) {
			d = t.Response.Delay
		}
	}
	if t.Response != nil {
		responseCount := 0
		total := t.Response.Count()
		t.Response.RangeText(func(text string) {
			responseCount++
			if t.Behavior.Stream {
				progress := float64(total) / float64(responseCount)
				msg := fmt.Sprintf("%s Progress: [%d] done, only [%d] more to go", t.Label, responseCount, total-responseCount)
				t.notifyClient(msg, progress)
			}
			if d != nil {
				delay = d.ComputeAndApply()
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

func (t *ToolCallContext) notifyClient(msg string, progress float64) {
	total := 0
	if progress > 0 {
		total = t.Response.Count()
	}
	params := &gomcp.ProgressNotificationParams{
		ProgressToken: t.req.Params.Meta.GetMeta()["progressToken"],
		Total:         float64(total),
		Progress:      progress,
		Message:       msg,
	}
	t.req.Session.NotifyProgress(t.ctx, params)
}

func (t *ToolCallContext) sendServerDetails() (*gomcp.CallToolResult, error) {
	result := &gomcp.CallToolResult{}
	result.Content = append(result.Content, &gomcp.TextContent{Text: util.ToJSONText(t.MCPTool.Server)})
	t.Log(fmt.Sprintf("%s sent Server [%s] details", t.Label, t.Server.GetName()))
	return result, nil
}

func (t *ToolCallContext) sendAllServers() (*gomcp.CallToolResult, error) {
	result := &gomcp.CallToolResult{}
	result.Content = append(result.Content, &gomcp.TextContent{Text: util.ToJSONText(t.MCPTool.Server.ps.AllServers())})
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
