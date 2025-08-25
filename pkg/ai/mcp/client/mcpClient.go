package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"goto/pkg/global"
	"goto/pkg/util"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type MCPClientPayload struct {
	Content string      `json:"content"`
	Role    string      `json:"role,omitempty"`
	Model   string      `json:"model,omitempty"`
	Delay   *util.Delay `json:"delay,omitempty"`
}

type MCPClient struct {
	name           string
	listenerLabel  string
	operationLabel string
	role           string
	serverURL      string
	sse            bool
	client         *gomcp.Client
	session        *gomcp.ClientSession
	hops           *util.Hops
}

var (
	Counter       = atomic.Int32{}
	ElicitPayload *MCPClientPayload
	SamplePayload *MCPClientPayload
	Roots         = []*gomcp.Root{}
	lock          sync.RWMutex
)

func NewClient(port int, sse bool, role, operationLabel string) *MCPClient {
	name := ""
	if sse {
		name = fmt.Sprintf("GotoMCPClient-%d[mcp/sse]", Counter.Add(1))
		role += "[sse]"
	} else {
		name = fmt.Sprintf("GotoMCPClient-%d[mcp]", Counter.Add(1))
	}
	return newMCPClient(sse, name, role, global.Funcs.GetHostLabelForPort(port), operationLabel)
}

func NewClientWithHops(port int, sse bool, role, operationLabel string, hops *util.Hops) *MCPClient {
	client := NewClient(port, sse, role, operationLabel)
	if hops != nil {
		client.hops = hops
	}
	return client
}

func newMCPClient(sse bool, name, role, listenerLabel, operationLabel string) *MCPClient {
	m := &MCPClient{
		name:           name,
		role:           role,
		listenerLabel:  listenerLabel,
		operationLabel: operationLabel,
		sse:            sse,
		hops:           util.NewHops(listenerLabel, name, operationLabel),
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
	c.serverURL = url
	if c.sse {
		c.session, err = c.client.Connect(context.Background(), &gomcp.SSEClientTransport{Endpoint: url}, &gomcp.ClientSessionOptions{})
	} else {
		c.session, err = c.client.Connect(context.Background(), &gomcp.StreamableClientTransport{Endpoint: url}, &gomcp.ClientSessionOptions{})
	}
	return err
}

func (c *MCPClient) Close() {
	c.session.Close()
	c.session = nil
}

func (c *MCPClient) CallTool(port int, tool string, args map[string]any) (map[string]any, error) {
	c.hops.Add(fmt.Sprintf("%s [%s][%s] calling tool [%s]/sse[%t] on url [%s]", c.role, c.listenerLabel, c.operationLabel, tool, c.sse, c.serverURL))
	result, err := c.session.CallTool(context.Background(), &gomcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		msg := fmt.Sprintf("%s [%s][%s] Failed to call tool [%s]/sse[%t] on url [%s] with error [%s]", c.role, c.listenerLabel, c.operationLabel, tool, c.sse, c.serverURL, err.Error())
		c.hops.Add(msg)
		return nil, errors.New(msg)
	}
	output := map[string]any{}
	if result.Content != nil {
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
	}
	if result.StructuredContent != nil {
		output["Goto-Client-Info"] = map[string]any{
			"Goto-MCP-Client":      global.Funcs.GetListenerLabelForPort(port),
			"Goto-MCP-Server-URL":  c.serverURL,
			"Goto-MCP-SSE":         c.sse,
			"Goto-MCP-Server-Tool": tool,
		}
		if serverOutput, ok := result.StructuredContent.(map[string]any); ok {
			serverOutput = c.hops.MergeRemoteHops(serverOutput)
			for k, v := range serverOutput {
				output[k] = v
			}
		}
	}
	c.hops.Add(fmt.Sprintf("%s [%s][%s] Tool [%s] successful", c.role, c.listenerLabel, c.operationLabel, tool))
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

func (c *MCPClient) ElicitationHandler(context.Context, *gomcp.ElicitRequest) (result *gomcp.ElicitResult, err error) {
	label := fmt.Sprintf("%s [%s][%s][elicit]", c.role, c.listenerLabel, c.operationLabel)
	hops := util.NewHops(c.listenerLabel, c.name, label)
	if ElicitPayload.Delay != nil {
		delay := ElicitPayload.Delay.Apply()
		hops.Add(fmt.Sprintf("Delaying for %s", delay.String()))
	}
	result = &gomcp.ElicitResult{
		Action:  "accept",
		Content: hops.Add(ElicitPayload.Content).AsOutput(),
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
	content := "Responding to Sample request with no defined payload"
	lock.RUnlock()
	result := &gomcp.CreateMessageResult{}
	if payload != nil {
		if payload.Delay != nil {
			payload.Delay.Apply()
		}
		result.Model = payload.Model
		result.Role = gomcp.Role(payload.Role)
		content = payload.Content
	} else {
		result.Model = "GotoModel"
		result.Role = gomcp.Role("none")
	}
	result.Content = &gomcp.TextContent{Text: util.NewHops(c.listenerLabel, c.name, label).Add(content).AsJSONText()}
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
