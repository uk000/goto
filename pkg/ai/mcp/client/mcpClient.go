package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"goto/pkg/global"
	"goto/pkg/metrics"
	"goto/pkg/transport"
	"goto/pkg/types"
	"goto/pkg/util"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type MCPClientPayload struct {
	Contents []string     `json:"contents"`
	Roles    []string     `json:"roles,omitempty"`
	Models   []string     `json:"models,omitempty"`
	Actions  []string     `json:"actions,omitempty"`
	Delay    *types.Delay `json:"delay,omitempty"`
}

type ToolCall struct {
	Tool           string              `json:"tool"`
	URL            string              `json:"url"`
	SSEURL         string              `json:"sseURL"`
	Server         string              `json:"server,omitempty"`
	Authority      string              `json:"authority,omitempty"`
	ForceSSE       bool                `json:"forceSSE,omitempty"`
	Raw            bool                `json:"neat,omitempty"`
	Delay          string              `json:"delay,omitempty"`
	Args           map[string]any      `json:"args,omitempty"`
	Headers        map[string][]string `json:"headers,omitempty"`
	ForwardHeaders []string            `json:"forwardHeaders,omitempty"`
	delayD         *types.Delay        `json:"-"`
}

type MCPSession struct {
	ID              string
	Name            string
	CallerId        string
	Authority       string
	SSE             bool
	Operation       string
	ForwardHeaders  map[string]bool
	Hops            *util.Hops
	session         *gomcp.ClientSession
	FirstActivityAt time.Time
	LasatActivityAt time.Time
	mcpClient       *MCPClient
}

type MCPClient struct {
	Name           string
	CallerId       string
	SSE            bool
	ActiveSessions map[string]*MCPSession
	httpClient     *http.Client
	ht             transport.ClientTransport
	mcpTransport   *MCPClientInterceptTransport
	progressChan   chan string
	client         *gomcp.Client
	lock           sync.RWMutex
}

type MCPClientInterceptTransport struct {
	*transport.HTTPTransportIntercept
	gomcp.Transport
	SessionHeaders map[string]map[string]string
}

var (
	Counter       = atomic.Int32{}
	ElicitPayload *MCPClientPayload
	SamplePayload *MCPClientPayload
	Roots         = []*gomcp.Root{}
	lock          sync.RWMutex
)

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

func NewClient(port int, sse bool, callerId string, progressChan chan string) *MCPClient {
	name := fmt.Sprintf("GotoMCP-%d[%s][%s]", Counter.Add(1), global.Funcs.GetListenerLabelForPort(port), callerId)
	if sse {
		name += "[sse]"
	}
	return newMCPClient(sse, name, callerId, progressChan)
}

func newMCPClient(sse bool, name, callerId string, progressChan chan string) *MCPClient {
	//httpClient := transport.CreateSimpleHTTPClient()
	ht := transport.CreateHTTPClient(name, false, true, false, "", 0,
		10*time.Minute, 10*time.Minute, 10*time.Minute, metrics.ConnTracker)
	m := &MCPClient{
		Name:           name,
		CallerId:       callerId,
		SSE:            sse,
		httpClient:     ht.HTTP(),
		ht:             ht,
		ActiveSessions: map[string]*MCPSession{},
		progressChan:   progressChan,
	}
	m.client = gomcp.NewClient(&gomcp.Implementation{Name: name, Version: "2.0"}, &gomcp.ClientOptions{
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

	m.client.AddRoots(Roots...)
	m.client.AddSendingMiddleware(m.SendingMiddleware)
	m.client.AddReceivingMiddleware(m.ReceivingMiddleware)
	if t, ok := ht.Transport().(*transport.HTTPTransportIntercept); ok {
		t.SetHeadersIntercept(m)
	}
	return m
}

func (c *MCPClient) Connect(url, operLabel string) (session *MCPSession, err error) {
	return c.ConnectWithHops(url, operLabel, nil)
}

func (c *MCPClient) newMCPTransport(label, url string) gomcp.Transport {
	var mcpTransport gomcp.Transport
	if c.SSE {
		mcpTransport = &gomcp.SSEClientTransport{Endpoint: url, HTTPClient: c.httpClient}
	} else {
		mcpTransport = &gomcp.StreamableClientTransport{Endpoint: url, MaxRetries: -1, HTTPClient: c.httpClient}
	}
	var ht *http.Transport
	var ok bool
	if c.httpClient.Transport != nil {
		ht, ok = c.httpClient.Transport.(*http.Transport)
		if !ok {
			if ht2, ok := c.httpClient.Transport.(*transport.HTTPTransportIntercept); ok {
				ht = ht2.Transport
			}
		}
	}
	return &MCPClientInterceptTransport{
		HTTPTransportIntercept: transport.NewHTTPTransportInterceptWithWatch(ht, label, metrics.ConnTracker, c),
		Transport:              mcpTransport,
		SessionHeaders:         map[string]map[string]string{},
	}
}

func (c *MCPClient) ConnectWithHops(url, operLabel string, hops *util.Hops) (session *MCPSession, err error) {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "http://" + url
	}
	t := c.newMCPTransport(c.Name, url)
	s, err := c.client.Connect(context.Background(), t, &gomcp.ClientSessionOptions{})
	if err == nil {
		session = c.newMCPSession(s, operLabel, hops)
	}
	return
}

func ParseToolCall(b []byte) (*ToolCall, error) {
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
		}
		if tc.Delay != "" {
			tc.delayD = types.ParseDelay(tc.Delay)
		}
	}
	return tc, err
}

func (c *MCPClient) ParseAndCallTool(b []byte, operLabel string) (map[string]any, error) {
	tc, err := ParseToolCall(b)
	if err != nil || tc == nil {
		return nil, err
	}
	session, err := c.Connect(tc.URL, operLabel)
	if err != nil {
		return nil, err
	}
	return session.CallTool(tc, nil)
}

func (c *MCPClient) newMCPSession(session *gomcp.ClientSession, operLabel string, hops *util.Hops) *MCPSession {
	mpcSession := &MCPSession{
		ID:        session.ID(),
		Name:      c.Name,
		CallerId:  c.CallerId,
		SSE:       c.SSE,
		session:   session,
		mcpClient: c,
	}
	if hops != nil {
		mpcSession.Hops = hops
	} else {
		mpcSession.Hops = util.NewHops(c.CallerId, operLabel)
	}
	c.lock.Lock()
	c.ActiveSessions[mpcSession.ID] = mpcSession
	c.lock.Unlock()
	return mpcSession
}

func (c *MCPClient) GetSession(sessionID string) *MCPSession {
	c.lock.RLock()
	defer c.lock.RLock()
	return c.ActiveSessions[sessionID]
}

func (c *MCPClient) RemoveSession(sessionID string) {
	c.lock.RLock()
	defer c.lock.RLock()
	delete(c.ActiveSessions, sessionID)
}

func (c *MCPClient) OnConnClose() {
	log.Println("Received connection close notification")
}

func (s *MCPSession) CallTool(tc *ToolCall, args map[string]any) (map[string]any, error) {
	if args == nil {
		args = tc.Args
	}
	msg := ""
	if args["delay"] == nil {
		args["delay"] = tc.Delay
	}
	s.Hops.Add(fmt.Sprintf("%s [%s] calling tool [%s] with sse[%t] on url [%s]", s.CallerId, s.Operation, tc.Tool, s.SSE, tc.URL))
	ctx := context.Background()
	if tc.Headers != nil {
		if !tc.Raw {
			tc.Headers["Host"] = []string{tc.Authority}
			tc.Headers["User-Agent"] = []string{s.CallerId}
		}
		ctx = util.WithContextHeaders(ctx, tc.Headers)
	}
	result, err := s.session.CallTool(ctx, &gomcp.CallToolParams{Name: tc.Tool, Arguments: args})
	if err != nil {
		msg = fmt.Sprintf("%s --> Failed to call tool [%s]/sse[%t] on url [%s] with error [%s]", msg, tc.Tool, s.SSE, tc.URL, err.Error())
		log.Println(msg)
		s.Hops.Add(msg)
		return nil, errors.New(msg)
	}
	output := map[string]any{}
	if !tc.Raw {
		output["Goto-Client-Info"] = map[string]any{
			"Goto-MCP-Client":      s.Name,
			"Goto-MCP-Server-URL":  tc.URL,
			"Goto-MCP-SSE":         s.SSE,
			"Goto-MCP-Server-Tool": tc.Tool,
			"Tool-Call":            tc,
			"Tool-Args":            args,
			"Tool-Request-Headers": tc.Headers,
		}
	}
	if result.Content != nil {
		if tc.Raw {
			output["content"] = result.Content
		} else {
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
	}
	if result.StructuredContent != nil {
		if serverOutput, ok := result.StructuredContent.(map[string]any); ok {
			serverOutput = s.Hops.MergeRemoteHops(serverOutput)
			if tc.Raw {
				for k, v := range serverOutput {
					output[k] = v
				}
			} else {
				output["structuredContent"] = serverOutput
			}
		}
	}
	msg = fmt.Sprintf("%s --> Tool [%s](sse=%t) successful on URL [%s]", msg, tc.Tool, s.SSE, tc.URL)
	s.Hops.Add(fmt.Sprintf("%s %s", s.CallerId, msg))
	log.Println(msg)
	return output, nil
}

func (s *MCPSession) AddForwardHeaders(forwardHeaders []string) {
	for _, h := range forwardHeaders {
		s.ForwardHeaders[h] = true
	}
}

func (s *MCPSession) SetAuthority(authority string) {
	s.Authority = authority
}

func (s *MCPSession) Close() {
	s.session.Close()
	s.session = nil
	s.mcpClient.RemoveSession(s.ID)
}

func (s *MCPSession) ListTools() (*gomcp.ListToolsResult, error) {
	return s.session.ListTools(util.WithContextHeaders(context.Background(), map[string][]string{"Host": []string{s.Authority}}), &gomcp.ListToolsParams{})
}

func (s *MCPSession) ListPrompts() (*gomcp.ListPromptsResult, error) {
	return s.session.ListPrompts(util.WithContextHeaders(context.Background(), map[string][]string{"Host": []string{s.Authority}}), &gomcp.ListPromptsParams{})
}

func (s *MCPSession) ListResources() (*gomcp.ListResourcesResult, error) {
	return s.session.ListResources(util.WithContextHeaders(context.Background(), map[string][]string{"Host": []string{s.Authority}}), &gomcp.ListResourcesParams{})
}

func (c *MCPClient) ElicitationHandler(ctx context.Context, req *gomcp.ElicitRequest) (result *gomcp.ElicitResult, err error) {
	label := fmt.Sprintf("%s[Elicitation]", c.CallerId)
	msg := ""
	var hops *util.Hops
	s := c.GetSession(req.Session.ID())
	if s == nil {
		msg = fmt.Sprintf("Session missing for ID [%s]", req.Session.ID())
		hops = util.NewHops(c.CallerId, label)
	} else {
		hops = s.Hops
	}
	responseContent := map[string]any{}
	if req.Params != nil {
		responseContent["requestParams"] = req.Params
	}
	action := "approve"
	if ElicitPayload != nil {
		msg = fmt.Sprintf("%s %s --> %s", label, msg, ElicitPayload.Contents[types.Random(len(ElicitPayload.Contents))])
		if ElicitPayload.Delay != nil {
			msg = fmt.Sprintf("%s --> Will delay", msg)
		}
		action = ElicitPayload.Actions[types.Random(len(ElicitPayload.Actions))]
	}
	if s.mcpClient.progressChan != nil {
		s.mcpClient.progressChan <- msg
	}
	if ElicitPayload != nil && ElicitPayload.Delay != nil {
		delay := ElicitPayload.Delay.ComputeAndApply()
		msg = fmt.Sprintf("%s --> Delaying for %s", msg, delay.String())
		responseContent["delay"] = delay.String()
	}
	log.Println(msg)
	responseContent["hops"] = hops.Add(msg).Steps
	result = &gomcp.ElicitResult{
		Action:  action,
		Content: responseContent,
	}
	return
}

func (c *MCPClient) CreateMessageHandler(ctx context.Context, req *gomcp.CreateMessageRequest) (*gomcp.CreateMessageResult, error) {
	isElicit := strings.Contains(req.Params.SystemPrompt, "elicit")
	task := "Sampling/Message"
	payload := SamplePayload
	if isElicit {
		task = "Elicitation"
		payload = ElicitPayload
	}
	label := fmt.Sprintf("%s[%s]", c.CallerId, task)
	msg := ""
	var hops *util.Hops
	s := c.GetSession(req.Session.ID())
	if s == nil {
		msg = fmt.Sprintf("Session missing for ID [%s]", req.Session.ID())
		hops = util.NewHops(c.CallerId, label)
	} else {
		hops = s.Hops
	}
	result := &gomcp.CreateMessageResult{}
	var content, model, role string
	if payload != nil {
		if payload.Delay != nil {
			msg = fmt.Sprintf("%s --> Will delay", msg)
		}
		if len(payload.Models) > 0 {
			model = payload.Models[types.Random(len(payload.Models))]
		}
		if len(payload.Roles) > 0 {
			role = payload.Roles[types.Random(len(payload.Roles))]
		}
		if len(payload.Contents) > 0 {
			content = payload.Contents[types.Random(len(payload.Contents))]
		}
	}
	if model == "" {
		model = "GotoModel"
	}
	if role == "" {
		role = "none"
	}
	if content == "" {
		msg = fmt.Sprintf("%s %s --> Responding to [%s] request with no defined payload", label, msg, task)
	}
	if s.mcpClient.progressChan != nil {
		s.mcpClient.progressChan <- msg
	}
	if payload.Delay != nil {
		delay := payload.Delay.ComputeAndApply()
		msg = fmt.Sprintf("%s --> Delaying for %s", msg, delay.String())
	}
	log.Println(msg)
	output := map[string]any{}
	output["Content"] = content
	hops.Add(msg).AddToOutput(output)
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
	if req.Params.Message != "" && c.progressChan != nil {
		c.progressChan <- req.Params.Message
	}
	msg := ""
	var hops *util.Hops
	s := c.GetSession(req.Session.ID())
	if s == nil {
		msg = fmt.Sprintf("%s[ProgressNotification]. Session missing for ID [%s]. Upstream Message: [%s]", c.CallerId, req.Session.ID(), req.Params.Message)
	} else {
		msg = fmt.Sprintf("%s[%s: ProgressNotification]. Upstream Message: [%s]", c.CallerId, s.Operation, req.Params.Message)
		hops = s.Hops
	}
	if req.Params.Progress > 0 {
		msg = fmt.Sprintf("%s --> [Total: %f][Progress: %f]", msg, req.Params.Total, req.Params.Progress)
	}
	if hops != nil {
		hops.Add(msg)
	}
	log.Println(msg)
	time.Sleep(200 * time.Millisecond)
}

func (c *MCPClient) Intercept(r *http.Request) {
	if headers := util.GetContextHeaders(r.Context()); headers != nil {
		if len(headers["Host"]) > 0 {
			r.Host = headers["Host"][0]
		}
		for k, v := range headers {
			r.Header.Add(k, v[0])
		}
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

func (t *MCPClientInterceptTransport) SetSessionHeaders(sessionID string, headers map[string]string) {
	t.SessionHeaders[sessionID] = headers
}

func (t *MCPClientInterceptTransport) GetSessionHeaders(sessionID string) map[string]string {
	return t.SessionHeaders[sessionID]
}

func (t *MCPClientInterceptTransport) RemoveSessionHeaders(sessionID string) {
	delete(t.SessionHeaders, sessionID)
}

func (t *MCPClientInterceptTransport) Connect(ctx context.Context) (gomcp.Connection, error) {
	return t.Transport.Connect(ctx)
}

func (tc *ToolCall) UpdateAndClone(tool, url, server, authority, delay string, headers map[string]string, args map[string]any) *ToolCall {
	clone := *tc
	if tool != "" {
		clone.Tool = tool
	}
	if url != "" {
		clone.URL = url
	}
	if server != "" {
		clone.Server = server
	}
	if authority != "" {
		clone.Authority = authority
	}
	if delay != "" {
		clone.Delay = delay
	}
	for h, v := range headers {
		clone.Headers[h] = []string{v}
	}
	for k, v := range args {
		clone.Args[k] = v
	}
	return &clone
}

func (tc *ToolCall) GetName() string {
	return tc.Tool
}
