package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"goto/pkg/global"
	"goto/pkg/metrics"
	"goto/pkg/transport"
	"goto/pkg/util"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type MCPClientPayload struct {
	Contents []string    `json:"contents"`
	Roles    []string    `json:"roles,omitempty"`
	Models   []string    `json:"models,omitempty"`
	Actions  []string    `json:"actions,omitempty"`
	Delay    *util.Delay `json:"delay,omitempty"`
}

type ToolCall struct {
	Tool      string         `json:"tool"`
	URL       string         `json:"url"`
	Server    string         `json:"server,omitempty"`
	Authority string         `json:"authority,omitempty"`
	SSE       bool           `json:"sse,omitempty"`
	Neat      bool           `json:"neat,omitempty"`
	Args      map[string]any `json:"args,omitempty"`
}

type MCPClient struct {
	name            string
	listenerLabel   string
	operationLabel  string
	role            string
	host            string
	sse             bool
	transportClient transport.TransportClient
	client          *gomcp.Client
	session         *gomcp.ClientSession
	hops            *util.Hops
}

var (
	Counter       = atomic.Int32{}
	ElicitPayload *MCPClientPayload
	SamplePayload *MCPClientPayload
	Roots         = []*gomcp.Root{}
	lock          sync.RWMutex
)

func NewClient(port int, sse bool, role, operationLabel, host string) *MCPClient {
	name := ""
	if sse {
		name = fmt.Sprintf("GotoMCPClient-%d[mcp/sse]", Counter.Add(1))
		role += "[sse]"
	} else {
		name = fmt.Sprintf("GotoMCPClient-%d[mcp]", Counter.Add(1))
	}
	return newMCPClient(sse, name, role, global.Funcs.GetHostLabelForPort(port), operationLabel, host)
}

func NewClientWithHops(port int, sse bool, role, operationLabel, host string, hops *util.Hops) *MCPClient {
	client := NewClient(port, sse, role, operationLabel, host)
	if hops != nil {
		client.hops = hops
	}
	return client
}

func newMCPClient(sse bool, name, role, listenerLabel, operationLabel, host string) *MCPClient {
	m := &MCPClient{
		name:            name,
		role:            role,
		listenerLabel:   listenerLabel,
		operationLabel:  operationLabel,
		sse:             sse,
		host:            host,
		hops:            util.NewHops(listenerLabel, name, operationLabel),
		transportClient: transport.CreateDefaultHTTPClient(name, false, false, metrics.ConnTracker),
	}
	m.client = gomcp.NewClient(&gomcp.Implementation{Name: name}, &gomcp.ClientOptions{
		KeepAlive:                   10 * time.Second,
		CreateMessageHandler:        m.CreateMessageHandler,
		ElicitationHandler:          m.ElicitationHandler,
		ToolListChangedHandler:      m.ToolListChangedHandler,
		PromptListChangedHandler:    m.PromptListChangedHandler,
		ResourceListChangedHandler:  m.ResourceListChangedHandler,
		ResourceUpdatedHandler:      m.ResourceUpdatedHandler,
		LoggingMessageHandler:       m.LoggingMessageHandler,
		ProgressNotificationHandler: m.ProgressNotificationHandler,
	})
	lock.RLock()
	m.client.AddRoots(Roots...)
	lock.RUnlock()
	m.client.AddSendingMiddleware(m.SendingMiddleware)
	m.client.AddReceivingMiddleware(m.ReceivingMiddleware)
	return m
}

func AddPayload(kind string, b []byte) error {
	lock.Lock()
	defer lock.Unlock()
	payload := &MCPClientPayload{}
	if err := json.Unmarshal(b, &payload); err != nil {
		return err
	}
	if kind == "elicit" {
		ElicitPayload = payload
	} else {
		SamplePayload = payload
	}
	return nil
}

func SetRoots(roots []*gomcp.Root) {
	Roots = roots
}

func (c *MCPClient) Connect(url string) (err error) {
	if c.session != nil {
		c.session.Close()
		c.session = nil
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "http://" + url
	}
	if c.sse {
		c.session, err = c.client.Connect(context.Background(), &gomcp.SSEClientTransport{Endpoint: url, HTTPClient: c.transportClient.HTTP()}, &gomcp.ClientSessionOptions{})
	} else {
		c.session, err = c.client.Connect(context.Background(), &gomcp.StreamableClientTransport{Endpoint: url, MaxRetries: -1, HTTPClient: c.transportClient.HTTP()}, &gomcp.ClientSessionOptions{})
	}
	return err
}

func (c *MCPClient) Close() {
	c.session.Close()
	c.session = nil
}

func ParseToolCall(port int, b []byte) (*ToolCall, error) {
	tc := &ToolCall{}
	err := json.Unmarshal(b, tc)
	if err == nil {
		if tc.Tool == "" || tc.URL == "" {
			return nil, errors.New("invalid tool call payload")
		}
		tc.Tool = strings.Trim(strings.Trim(tc.Tool, "\""), "'")
		if tc.Server == "" {
			tc.Server = tc.Authority
		}
		if tc.Args == nil {
			tc.Args = map[string]any{}
			tc.Args["Goto-Client"] = global.Funcs.GetListenerLabelForPort(port)
		}
	}
	return tc, err
}

func (c *MCPClient) ParseAndCallTool(port int, b []byte) (map[string]any, error) {
	tc, err := ParseToolCall(port, b)
	if err != nil || tc == nil {
		return nil, err
	}
	return c.CallTool(port, tc, nil)
}

func (c *MCPClient) CallTool(port int, tc *ToolCall, args map[string]any) (map[string]any, error) {
	if args == nil {
		args = tc.Args
	}
	c.hops.Add(fmt.Sprintf("%s [%s][%s] calling tool [%s]/sse[%t] on url [%s]", c.role, c.listenerLabel, c.operationLabel, tc.Tool, c.sse, tc.URL))
	err := c.Connect(tc.URL)
	if err != nil {
		return nil, err
	}

	result, err := c.session.CallTool(context.Background(), &gomcp.CallToolParams{Name: tc.Tool, Arguments: tc.Args})
	if err != nil {
		msg := fmt.Sprintf("%s [%s][%s] Failed to call tool [%s]/sse[%t] on url [%s] with error [%s]", c.role, c.listenerLabel, c.operationLabel, tc.Tool, c.sse, tc.URL, err.Error())
		c.hops.Add(msg)
		return nil, errors.New(msg)
	}
	output := map[string]any{}
	output["Goto-Client-Info"] = map[string]any{
		"Goto-MCP-Client":      global.Funcs.GetListenerLabelForPort(port),
		"Goto-MCP-Server-URL":  tc.URL,
		"Goto-MCP-SSE":         c.sse,
		"Goto-MCP-Server-Tool": tc.Tool,
	}
	if result.Content != nil {
		if tc.Neat {
			content := []any{}
			for _, c := range result.Content {
				if tc, ok := c.(*gomcp.TextContent); ok {
					json := util.JSONFromJSONText(tc.Text)
					if json != nil && !json.IsEmpty() {
						content = append(content, json.Value())
					} else {
						content = append(content, tc.Text)
					}
				}
			}
			if len(content) == 1 {
				output["content"] = content[0]
			} else {
				output["content"] = content
			}
		} else {
			output["content"] = result.Content
		}
	}
	if result.StructuredContent != nil {
		if serverOutput, ok := result.StructuredContent.(map[string]any); ok {
			serverOutput = c.hops.MergeRemoteHops(serverOutput)
			if tc.Neat {
				for k, v := range serverOutput {
					output[k] = v
				}
			} else {
				output["structuredContent"] = serverOutput
			}
		}
	}
	c.hops.Add(fmt.Sprintf("%s [%s][%s] Tool [%s] successful", c.role, c.listenerLabel, c.operationLabel, tc.Tool))
	return output, nil
}

func (c *MCPClient) ListTools() (*gomcp.ListToolsResult, error) {
	return c.session.ListTools(context.Background(), &gomcp.ListToolsParams{})
}

func (c *MCPClient) ListPrompts() (*gomcp.ListPromptsResult, error) {
	return c.session.ListPrompts(context.Background(), &gomcp.ListPromptsParams{})
}

func (c *MCPClient) ListResources() (*gomcp.ListResourcesResult, error) {
	return c.session.ListResources(context.Background(), &gomcp.ListResourcesParams{})
}

func (c *MCPClient) ElicitationHandler(ctx context.Context, req *gomcp.ElicitRequest) (result *gomcp.ElicitResult, err error) {
	label := fmt.Sprintf("%s [%s][%s][elicit]", c.role, c.listenerLabel, c.operationLabel)
	hops := util.NewHops(c.listenerLabel, c.name, label)
	responseContent := map[string]any{}
	if req.Params != nil {
		responseContent["requestParams"] = req.Params
	}
	if ElicitPayload.Delay != nil {
		delay := ElicitPayload.Delay.Apply()
		responseContent["delay"] = delay.String()
		hops.Add(fmt.Sprintf("Delaying for %s", delay.String()))
	}
	responseContent["hops"] = hops.Add(ElicitPayload.Contents[util.Random(len(ElicitPayload.Contents))]).Steps
	result = &gomcp.ElicitResult{
		Action:  ElicitPayload.Actions[util.Random(len(ElicitPayload.Actions))],
		Content: responseContent,
	}
	return
}

func (c *MCPClient) CreateMessageHandler(ctx context.Context, req *gomcp.CreateMessageRequest) (*gomcp.CreateMessageResult, error) {
	isElicit := strings.Contains(req.Params.SystemPrompt, "elicit")
	lock.RLock()
	task := "sample"
	payload := SamplePayload
	if isElicit {
		task = "elicit"
		payload = ElicitPayload
	}
	label := fmt.Sprintf("%s [%s][%s][%s]", c.role, c.listenerLabel, c.operationLabel, task)
	lock.RUnlock()
	result := &gomcp.CreateMessageResult{}
	var content, model, role string
	if payload != nil {
		if payload.Delay != nil {
			payload.Delay.Apply()
		}
		if len(payload.Models) > 0 {
			model = payload.Models[util.Random(len(payload.Models))]
		}
		if len(payload.Roles) > 0 {
			role = payload.Roles[util.Random(len(payload.Roles))]
		}
		if len(payload.Contents) > 0 {
			content = payload.Contents[util.Random(len(payload.Contents))]
		}
	}
	if model == "" {
		model = "GotoModel"
	}
	if role == "" {
		role = "none"
	}
	if content == "" {
		content = fmt.Sprintf("Responding to [%s] request with no defined payload", task)
	}
	output := map[string]any{}
	output["Content"] = content
	util.NewHops(c.listenerLabel, c.name, label).Add(content).AddToOutput(output)
	result.Model = model
	result.Role = gomcp.Role(role)
	result.Content = &gomcp.TextContent{Text: util.ToJSONText(output)}
	result.StopReason = req.Params.SystemPrompt
	return result, nil
}

func (c *MCPClient) ToolListChangedHandler(ctx context.Context, req *gomcp.ToolListChangedRequest) {

}

func (c *MCPClient) PromptListChangedHandler(ctx context.Context, req *gomcp.PromptListChangedRequest) {

}

func (c *MCPClient) ResourceListChangedHandler(ctx context.Context, req *gomcp.ResourceListChangedRequest) {

}

func (c *MCPClient) ResourceUpdatedHandler(ctx context.Context, req *gomcp.ResourceUpdatedNotificationRequest) {

}

func (c *MCPClient) LoggingMessageHandler(ctx context.Context, req *gomcp.LoggingMessageRequest) {

}

func (c *MCPClient) ProgressNotificationHandler(ctx context.Context, req *gomcp.ProgressNotificationClientRequest) {
	if c.hops != nil {
		label := fmt.Sprintf("%s [%s][%s]", c.role, c.listenerLabel, c.operationLabel)
		c.hops.Add(fmt.Sprintf("%s: Received Progress Notification [%s][Total: %f][Progress: %f]", label, req.Params.Message, req.Params.Total, req.Params.Progress))
	}
}

func (c *MCPClient) SendingMiddleware(next gomcp.MethodHandler) gomcp.MethodHandler {
	return func(ctx context.Context, method string, req gomcp.Request) (result gomcp.Result, err error) {
		if ctp, ok := req.GetParams().(*gomcp.CallToolParams); ok {
			if args, ok := ctp.Arguments.(map[string]any); ok && args != nil {
				args["goto-client"] = global.Self.HostLabel
			}
		}
		return next(ctx, method, req)
	}
}

func (c *MCPClient) ReceivingMiddleware(next gomcp.MethodHandler) gomcp.MethodHandler {
	return func(ctx context.Context, method string, req gomcp.Request) (result gomcp.Result, err error) {
		return next(ctx, method, req)
	}
}
