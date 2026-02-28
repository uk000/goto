/**
 * Copyright 2025 uk
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
	RemoveHeaders  []string            `json:"removeHeaders,omitempty"`
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
	OutHeaders      map[string][]string
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
	ht             *transport.HTTPTransportIntercept
	mcpTransport   *MCPClientInterceptTransport
	progressChan   chan string
	client         *gomcp.Client
	clientPayload  *MCPNamedClientPayload
	lock           sync.RWMutex
}

type MCPNamedClientPayload struct {
	Name          string
	ElicitPayload *MCPClientPayload
	SamplePayload *MCPClientPayload
	Roots         []*gomcp.Root
}

type MCPClientInterceptTransport struct {
	*transport.HTTPTransportIntercept
	gomcp.Transport
	SessionHeaders map[string]map[string]string
}

var (
	Counter              = atomic.Int32{}
	NamedClientPayloads  = map[string]*MCPNamedClientPayload{}
	DefaultClientPayload *MCPNamedClientPayload
	lock                 sync.RWMutex
)

func getOrCreateNamedClientPayload(name string) *MCPNamedClientPayload {
	lock.Lock()
	defer lock.Unlock()
	namedClientPayload := NamedClientPayloads[name]
	if namedClientPayload == nil {
		namedClientPayload = &MCPNamedClientPayload{}
		NamedClientPayloads[name] = namedClientPayload
		DefaultClientPayload = namedClientPayload
	}
	return namedClientPayload
}

func getNamedClientPayload(name string) *MCPNamedClientPayload {
	lock.RLock()
	defer lock.RUnlock()
	namedClientPayload := NamedClientPayloads[name]
	if namedClientPayload == nil {
		namedClientPayload = DefaultClientPayload
	}
	return namedClientPayload
}

func AddPayload(name, kind string, b []byte) error {
	payload := &MCPClientPayload{}
	if err := json.Unmarshal(b, &payload); err != nil {
		return err
	}
	namedClientPayload := getOrCreateNamedClientPayload(name)
	if kind == "elicit" {
		namedClientPayload.ElicitPayload = payload
	} else {
		namedClientPayload.SamplePayload = payload
	}
	return nil
}

func SetRoots(name string, payload []byte) error {
	var roots []*gomcp.Root
	if err := util.ReadJsonFromBytes(payload, &roots); err != nil {
		return err
	}
	namedClientPayload := getOrCreateNamedClientPayload(name)
	namedClientPayload.Roots = roots
	return nil
}

func NewClient(port int, sse bool, callerId string, headers http.Header, progressChan chan string) *MCPClient {
	name := fmt.Sprintf("GotoMCP-%d[%s][%s]", Counter.Add(1), global.Funcs.GetListenerLabelForPort(port), callerId)
	if sse {
		name += "[sse]"
	}
	return newMCPClient(sse, name, callerId, headers, progressChan)
}

func newMCPClient(sse bool, name, callerId string, headers http.Header, progressChan chan string) *MCPClient {
	//httpClient := transport.CreateSimpleHTTPClient()
	ht := transport.CreateHTTPClient(name, false, true, false, "", 0,
		10*time.Minute, 10*time.Minute, 10*time.Minute, metrics.ConnTracker)
	m := &MCPClient{
		Name:           name,
		CallerId:       callerId,
		SSE:            sse,
		httpClient:     ht.HTTP(),
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
	m.clientPayload = getNamedClientPayload(name)
	if m.clientPayload != nil {
		m.client.AddRoots(m.clientPayload.Roots...)
	}
	m.client.AddSendingMiddleware(m.SendingMiddleware)
	m.client.AddReceivingMiddleware(m.ReceivingMiddleware)
	if t, ok := ht.Transport().(*transport.HTTPTransportIntercept); ok {
		m.ht = t
	}
	return m
}

func (c *MCPClient) newMCPTransport(label, url string) gomcp.Transport {
	var mcpTransport gomcp.Transport
	if c.SSE {
		mcpTransport = &gomcp.SSEClientTransport{Endpoint: url, HTTPClient: c.httpClient}
	} else {
		mcpTransport = &gomcp.StreamableClientTransport{Endpoint: url, MaxRetries: -1, HTTPClient: c.httpClient}
	}
	return &MCPClientInterceptTransport{
		HTTPTransportIntercept: transport.NewHTTPTransportInterceptWithWatch(c.ht.Transport, label, metrics.ConnTracker, c),
		Transport:              mcpTransport,
		SessionHeaders:         map[string]map[string]string{},
	}
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

func (c *MCPClient) Connect(url, operLabel string, headers http.Header) (session *MCPSession, err error) {
	return c.ConnectWithHops(url, operLabel, headers, nil)
}

func (c *MCPClient) ConnectWithHops(url, operLabel string, headers http.Header, hops *util.Hops) (*MCPSession, error) {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "http://" + url
	}
	t := c.newMCPTransport(c.Name, url)
	session := c.newMCPSession(operLabel, headers, hops)
	s, err := c.client.Connect(context.Background(), t, &gomcp.ClientSessionOptions{})
	if err == nil {
		session.ID = s.ID()
		session.session = s
		return session, nil
	}
	return nil, err
}

func (c *MCPClient) newMCPSession(operLabel string, headers http.Header, hops *util.Hops) *MCPSession {
	outHeaders := http.Header{}
	for h, v := range headers {
		outHeaders[h] = v
	}
	mpcSession := &MCPSession{
		Name:       c.Name,
		CallerId:   c.CallerId,
		SSE:        c.SSE,
		OutHeaders: outHeaders,
		mcpClient:  c,
	}
	if hops != nil {
		mpcSession.Hops = hops
	} else {
		mpcSession.Hops = util.NewHops(c.CallerId, operLabel)
	}
	c.ht.SetRequestIntercept(mpcSession)
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
		for h, v := range tc.Headers {
			s.OutHeaders[h] = v
		}
	}
	output := util.BuildGotoClientInfo(nil, 0, s.Name, "", tc.Tool, tc.URL, tc.Server, nil, args, nil, tc.Headers,
		map[string]any{
			"Goto-MCP-SSE":  s.SSE,
			"Goto-MCP-Tool": tc.Tool,
			"Tool-Call":     tc,
		})
	result, err := s.session.CallTool(ctx, &gomcp.CallToolParams{Name: tc.Tool, Arguments: args})
	if err != nil {
		msg = fmt.Sprintf("%s --> Failed to call tool [%s]/sse[%t] on url [%s] with error [%s]", msg, tc.Tool, s.SSE, tc.URL, err.Error())
		s.Hops.Add(msg)
		log.Println(s.Hops.AsJSONText())
		log.Println(util.ToJSONText(output))
		return output, errors.New(msg)
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
	var elicitPayload *MCPClientPayload
	if c.clientPayload != nil {
		elicitPayload = c.clientPayload.ElicitPayload
	}
	if elicitPayload != nil {
		msg = fmt.Sprintf("%s %s --> %s", label, msg, elicitPayload.Contents[types.Random(len(elicitPayload.Contents))])
		if elicitPayload.Delay != nil {
			msg = fmt.Sprintf("%s --> Will delay", msg)
		}
		action = elicitPayload.Actions[types.Random(len(elicitPayload.Actions))]
	}
	if s.mcpClient.progressChan != nil {
		s.mcpClient.progressChan <- msg
	}
	if elicitPayload != nil && elicitPayload.Delay != nil {
		delay := elicitPayload.Delay.ComputeAndApply()
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
	var payload *MCPClientPayload
	if c.clientPayload != nil {
		payload = c.clientPayload.SamplePayload
	}
	if isElicit {
		task = "Elicitation"
		if c.clientPayload != nil {
			payload = c.clientPayload.ElicitPayload
		}
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

func (s *MCPSession) Intercept(r *http.Request) {
	headers := util.GetContextHeaders(r.Context())
	if headers == nil {
		headers = map[string][]string{}
	}
	if s.OutHeaders != nil {
		for h, v := range s.OutHeaders {
			headers[h] = v
		}
	}
	if len(headers["Host"]) > 0 {
		r.Host = headers["Host"][0]
	}
	for k, v := range headers {
		r.Header.Add(k, v[0])
	}
	if len(headers) == 0 {
		log.Printf("---------- Not sending any outbound headers for MCP client %s ------------\n", s.mcpClient.CallerId)
	} else {
		log.Printf("---------- Outbound request headers from MCP client %s ------------\n", s.mcpClient.CallerId)
		log.Println(util.ToJSONText(r.Header))
	}
}

func (c *MCPClient) SendingMiddleware(next gomcp.MethodHandler) gomcp.MethodHandler {
	return func(ctx context.Context, method string, req gomcp.Request) (result gomcp.Result, err error) {
		log.Printf("---------- Outbound request from MCP client %s ------------\n", c.CallerId)
		log.Println(util.ToJSONText(req))
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
	clone.Headers = map[string][]string{}
	for h, v := range tc.Headers {
		clone.Headers[h] = v
	}
	for h, v := range headers {
		clone.Headers[h] = []string{v}
	}
	clone.Args = map[string]any{}
	for k, v := range tc.Args {
		clone.Args[k] = v
	}
	for k, v := range args {
		clone.Args[k] = v
	}
	return &clone
}
