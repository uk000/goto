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
	"log"
	"net/http"
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
	Tool      string              `json:"tool"`
	URL       string              `json:"url"`
	SSEURL    string              `json:"sseURL"`
	Server    string              `json:"server,omitempty"`
	Authority string              `json:"authority,omitempty"`
	ForceSSE  bool                `json:"forceSSE,omitempty"`
	Neat      bool                `json:"neat,omitempty"`
	Delay     string              `json:"delay,omitempty"`
	Args      map[string]any      `json:"args,omitempty"`
	Headers   map[string][]string `json:"headers,omitempty"`
	delayD    time.Duration       `json:"-"`
}

type MCPSession struct {
	ID              string
	Name            string
	CallerId        string
	Authority       string
	SSE             bool
	Operation       string
	Hops            *util.Hops
	session         *gomcp.ClientSession
	FirstActivityAt time.Time
	LasatActivityAt time.Time
	parentClient    *MCPClient
}

type MCPClient struct {
	name           string
	callerId       string
	sse            bool
	httpClient     *http.Client
	ht             transport.ClientTransport
	mcpTransport   *MCPClientInterceptTransport
	client         *gomcp.Client
	activeSessions map[string]*MCPSession
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

func NewClient(port int, sse bool, callerId string) *MCPClient {
	name := fmt.Sprintf("GotoMCP-%d[%s][%s]", Counter.Add(1), global.Funcs.GetHostLabelForPort(port), callerId)
	if sse {
		name += "[sse]"
	}
	return newMCPClient(sse, name, callerId)
}

func newMCPClient(sse bool, name, callerId string) *MCPClient {
	//httpClient := transport.CreateSimpleHTTPClient()
	ht := transport.CreateHTTPClient(name, false, true, false, "", 0,
		10*time.Minute, 10*time.Minute, 10*time.Minute, metrics.ConnTracker)
	m := &MCPClient{
		name:           name,
		callerId:       callerId,
		sse:            sse,
		httpClient:     ht.HTTP(),
		ht:             ht,
		activeSessions: map[string]*MCPSession{},
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
	if c.sse {
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
			log.Println("NO")
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
	t := c.newMCPTransport(c.name, url)
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
			tc.delayD = util.ParseDuration(tc.Delay)
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
		ID:           session.ID(),
		Name:         c.name,
		CallerId:     c.callerId,
		SSE:          c.sse,
		session:      session,
		parentClient: c,
	}
	if hops != nil {
		mpcSession.Hops = hops
	} else {
		mpcSession.Hops = util.NewHops(c.callerId, operLabel)
	}
	c.lock.Lock()
	c.activeSessions[mpcSession.ID] = mpcSession
	c.lock.Unlock()
	return mpcSession
}

func (c *MCPClient) GetSession(sessionID string) *MCPSession {
	c.lock.RLock()
	defer c.lock.RLock()
	return c.activeSessions[sessionID]
}

func (c *MCPClient) RemoveSession(sessionID string) {
	c.lock.RLock()
	defer c.lock.RLock()
	delete(c.activeSessions, sessionID)
}

func (c *MCPClient) OnConnClose() {
	log.Println("Received connection close notification")
}

func (s *MCPSession) CallTool(tc *ToolCall, args map[string]any) (map[string]any, error) {
	if args == nil {
		args = tc.Args
	}
	args["Goto-Client"] = s.Name
	if tc.Delay != "" {
		if delay := util.ParseDuration(tc.Delay); delay > 0 {
			s.Hops.Add(fmt.Sprintf("%s [%s] Applying delay of [%s] before calling tool [%s]", s.CallerId, s.Operation, tc.Delay, tc.Tool))
		}
	}
	s.Hops.Add(fmt.Sprintf("%s [%s] calling tool [%s] with sse[%t] on url [%s]", s.CallerId, s.Operation, tc.Tool, s.SSE, tc.URL))
	ctx := context.Background()
	if tc.Headers != nil {
		tc.Headers["Host"] = []string{tc.Authority}
		ctx = util.WithContextHeaders(ctx, tc.Headers)
	}
	msg := ""
	result, err := s.session.CallTool(ctx, &gomcp.CallToolParams{Name: tc.Tool, Arguments: tc.Args})
	if err != nil {
		msg = fmt.Sprintf("%s --> Failed to call tool [%s]/sse[%t] on url [%s] with error [%s]", msg, tc.Tool, s.SSE, tc.URL, err.Error())
		log.Println(msg)
		s.Hops.Add(msg)
		return nil, errors.New(msg)
	}
	output := map[string]any{}
	output["Goto-Client-Info"] = map[string]any{
		"Goto-MCP-Client":      s.Name,
		"Goto-MCP-Server-URL":  tc.URL,
		"Goto-MCP-SSE":         s.SSE,
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
			serverOutput = s.Hops.MergeRemoteHops(serverOutput)
			if tc.Neat {
				for k, v := range serverOutput {
					output[k] = v
				}
			} else {
				output["structuredContent"] = serverOutput
			}
		}
	}
	msg = fmt.Sprintf("%s --> Tool [%s](sse=%t) successful", msg, tc.Tool, s.SSE)
	s.Hops.Add(fmt.Sprintf("%s %s", s.CallerId, msg))
	log.Println(msg)
	return output, nil
}

func (s *MCPSession) SetAuthority(authority string) {
	s.Authority = authority
}

func (s *MCPSession) Close() {
	s.session.Close()
	s.session = nil
	s.parentClient.RemoveSession(s.ID)
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
	label := fmt.Sprintf("%s[Elicitation]", c.callerId)
	msg := ""
	var hops *util.Hops
	s := c.GetSession(req.Session.ID())
	if s == nil {
		msg = fmt.Sprintf("Session missing for ID [%s]", req.Session.ID())
		hops = util.NewHops(c.callerId, label)
	} else {
		hops = s.Hops
	}
	responseContent := map[string]any{}
	if req.Params != nil {
		responseContent["requestParams"] = req.Params
	}
	if ElicitPayload.Delay != nil {
		delay := ElicitPayload.Delay.Apply()
		responseContent["delay"] = delay.String()
		msg = fmt.Sprintf("%s --> Delaying for %s", msg, delay.String())
	}
	msg = fmt.Sprintf("%s %s --> %s", label, msg, ElicitPayload.Contents[util.Random(len(ElicitPayload.Contents))])
	log.Println(msg)
	responseContent["hops"] = hops.Add(msg).Steps
	result = &gomcp.ElicitResult{
		Action:  ElicitPayload.Actions[util.Random(len(ElicitPayload.Actions))],
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
	label := fmt.Sprintf("%s[%s]", c.callerId, task)
	msg := ""
	var hops *util.Hops
	s := c.GetSession(req.Session.ID())
	if s == nil {
		msg = fmt.Sprintf("Session missing for ID [%s]", req.Session.ID())
		hops = util.NewHops(c.callerId, label)
	} else {
		hops = s.Hops
	}
	result := &gomcp.CreateMessageResult{}
	var content, model, role string
	if payload != nil {
		if payload.Delay != nil {
			delay := payload.Delay.Apply()
			msg = fmt.Sprintf("%s --> Delaying for %s", msg, delay.String())
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
		msg = fmt.Sprintf("%s %s --> Responding to [%s] request with no defined payload", label, msg, task)
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
	msg := ""
	var hops *util.Hops
	var label string
	s := c.GetSession(req.Session.ID())
	if s == nil {
		msg = fmt.Sprintf("Session missing for ID [%s]", req.Session.ID())
		label = fmt.Sprintf("%s[ProgressNotification]", c.callerId)
	} else {
		label = fmt.Sprintf("%s[%s][ProgressNotification]", c.callerId, s.Operation)
		hops = s.Hops
	}
	msg = fmt.Sprintf("%s %s --> Received Progress Notification [%s][Total: %f][Progress: %f]", label, msg, req.Params.Message, req.Params.Total, req.Params.Progress)
	if hops != nil {
		hops.Add(msg)
	}
	log.Println(msg)
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
