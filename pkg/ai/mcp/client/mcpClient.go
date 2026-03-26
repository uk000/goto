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

package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/metrics"
	"goto/pkg/transport"
	"goto/pkg/types"
	"goto/pkg/util"
	"log"
	"maps"
	"net/http"
	"slices"
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
	Tool         string         `json:"tool"`
	URL          string         `json:"url"`
	SSEURL       string         `json:"sseURL"`
	Server       string         `json:"server,omitempty"`
	Authority    string         `json:"authority,omitempty"`
	H2           bool           `json:"h2,omitempty"`
	TLS          bool           `json:"tls,omitempty"`
	ForceSSE     bool           `json:"forceSSE,omitempty"`
	Raw          bool           `json:"neat,omitempty"`
	Args         map[string]any `json:"args,omitempty"`
	Headers      *types.Headers `json:"headers,omitempty"`
	Delay        string         `json:"delay,omitempty"`
	RequestCount int            `json:"requestCount"`
	Concurrent   int            `json:"concurrent"`
	InitialDelay string         `json:"initialDelay"`
	delayD       *types.Delay   `json:"-"`
}

type MCPSession struct {
	ID              string
	Name            string
	CallerId        string
	Listener        string
	URL             string
	Authority       string
	SSE             bool
	Operation       string
	Hops            *util.Hops
	FirstActivityAt time.Time
	LasatActivityAt time.Time
	session         *gomcp.ClientSession
	mcpClient       *MCPClient
	currentRequest  string
	currentToolCall *ToolCall
	currentArgs     map[string]any
	currentOutput   map[string]any
	inHeaders       http.Header
	outHeaders      *types.Headers
	respHeaders     http.Header
}

type MCPClient struct {
	Name           string
	CallerId       string
	Listener       string
	SSE            bool
	TLS            bool
	Stop           bool
	ActiveSessions map[string]*MCPSession
	httpClient     *http.Client
	ht             transport.IHTTPTransportIntercept
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
	ht transport.IHTTPTransportIntercept
	gomcp.Transport
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

func NewClient(port int, sse, tls bool, callerId, listener, authority string, progressChan chan string) *MCPClient {
	name := fmt.Sprintf("GotoMCP-%d[%s][%s]", Counter.Add(1), global.Funcs.GetListenerLabelForPort(port), callerId)
	if sse {
		name += "[sse]"
	}
	return newMCPClient(sse, tls, name, callerId, listener, authority, progressChan)
}

func newMCPClient(sse, tls bool, name, callerId, listener, authority string, progressChan chan string) *MCPClient {
	//httpClient := transport.CreateSimpleHTTPClient()
	ht := transport.CreateHTTPClient(name, true, true, tls, authority, 0,
		10*time.Minute, 10*time.Minute, 10*time.Minute, metrics.ConnTracker)
	m := &MCPClient{
		Name:           name,
		CallerId:       callerId,
		Listener:       listener,
		SSE:            sse,
		TLS:            tls,
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
	if t, ok := ht.Transport().(*transport.HTTPTransportIntercept); ok {
		m.ht = t
	} else if t, ok := ht.Transport().(*transport.HTTP2TransportIntercept); ok {
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
		ht:        c.ht,
		Transport: mcpTransport,
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

func (c *MCPClient) Connect(url, operLabel string) (session *MCPSession, err error) {
	return c.ConnectWithHops(url, operLabel, nil)
}

func (c *MCPClient) ConnectWithHops(url, operLabel string, hops *util.Hops) (*MCPSession, error) {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		if c.TLS {
			url = "https://" + url
		} else {
			url = "http://" + url
		}
	}
	t := c.newMCPTransport(c.Name, url)
	session := c.newMCPSession(operLabel, url, hops)
	s, err := c.client.Connect(context.Background(), t, &gomcp.ClientSessionOptions{})
	if err == nil {
		session.ID = s.ID()
		session.session = s
		return session, nil
	}
	return nil, err
}

func (c *MCPClient) newMCPSession(operLabel, url string, hops *util.Hops) *MCPSession {
	s := &MCPSession{
		Name:      c.Name,
		CallerId:  c.CallerId,
		Listener:  c.Listener,
		Operation: operLabel,
		URL:       url,
		SSE:       c.SSE,
		mcpClient: c,
	}
	if hops != nil {
		s.Hops = hops
	} else {
		s.Hops = util.NewHops(c.CallerId, c.Listener, operLabel)
	}
	c.ht.SetRequestIntercept(s)
	c.ht.SetResponseIntercept(s)
	c.lock.Lock()
	c.ActiveSessions[s.ID] = s
	c.lock.Unlock()
	c.client.AddSendingMiddleware(s.SendingMiddleware)
	c.client.AddReceivingMiddleware(s.ReceivingMiddleware)
	return s
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

func (s *MCPSession) CallTool(tc *ToolCall, args map[string]any, inHeaders http.Header) (map[string]any, error) {
	msg := ""
	results := []any{}
	s.currentToolCall = tc
	if args == nil {
		args = tc.Args
	}
	if args["delay"] == nil {
		args["delay"] = tc.Delay
	}
	msg = fmt.Sprintf("%s [%s] Tool Call [%s] with sse[%t] on url [%s]. Request Count [%d], Concurrent [%d]", s.CallerId, s.Operation, tc.Tool, s.SSE, tc.URL, tc.RequestCount, tc.Concurrent)
	s.Hops.Add(msg)
	results = append(results, msg)
	ctx := context.Background()
	if args["forwardHeaders"] == nil && tc.Headers != nil && tc.Headers.Request != nil {
		args["forwardHeaders"] = tc.Headers.Request.Forward
	}
	s.outHeaders = tc.Headers
	s.currentArgs = args
	if s.outHeaders == nil {
		s.outHeaders = types.NewHeaders()
	}
	if !tc.Raw {
		s.outHeaders.Request.Add["Host"] = tc.Authority
		s.outHeaders.Request.Add["User-Agent"] = s.CallerId
	}
	// ctx = util.WithContextHeaders(ctx, s.AddRequestHeaders)
	s.inHeaders = inHeaders
	s.currentOutput = map[string]any{}
	requestCount := tc.RequestCount
	if requestCount == 0 {
		requestCount = 1
	}
	concurrent := tc.Concurrent
	if concurrent == 0 {
		concurrent = 1
	}
	rounds := requestCount / concurrent
	msg = fmt.Sprintf("[%s] Will send %d requests in %d rounds with concurrency %d to agent %s\n", s.CallerId, requestCount, rounds, concurrent, tc.URL)
	s.Hops.Add(msg)
	results = append(results, msg)
	for i := 1; i <= rounds; i++ {
		msg = fmt.Sprintf("[%s] Calling tool [%s] on url [%s]. Request #%d/%d", s.CallerId, tc.Tool, tc.URL, i, requestCount)
		s.Hops.Add(msg)
		results = append(results, msg)
		toolResult, err := s.session.CallTool(ctx, &gomcp.CallToolParams{Name: tc.Tool, Arguments: args})
		if err != nil {
			msg = fmt.Sprintf("%s --> Request #%d/%d: Failed to call tool [%s]/sse[%t] on url [%s] with error [%s]", s.CallerId, i, requestCount, tc.Tool, s.SSE, tc.URL, err.Error())
			s.Hops.Add(msg)
			log.Println(s.Hops.AsJSONText())
			results = append(results, msg)
		} else {
			results = append(results, s.resultToOutput(tc, toolResult))
			msg = fmt.Sprintf("%s --> Request #%d/%d: Tool [%s](sse=%t) successful on URL [%s]", s.CallerId, i, requestCount, tc.Tool, s.SSE, tc.URL)
			s.Hops.Add(fmt.Sprintf("%s %s", s.CallerId, msg))
			log.Println(msg)
		}
	}
	s.currentOutput[tc.URL] = results
	return s.currentOutput, nil
}

func (s *MCPSession) resultToOutput(tc *ToolCall, result *gomcp.CallToolResult) (output map[string]any) {
	output = map[string]any{}
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
	return
}

func (s *MCPSession) InterceptRequest(r *http.Request) {
	if s.outHeaders != nil && s.outHeaders.Request != nil {
		label := fmt.Sprintf("MCP client request for %s", s.mcpClient.CallerId)
		s.outHeaders.Request.UpdateHeaders(r.Header, label)
		types.ForwardHeaders(s.inHeaders, r.Header, slices.Values(s.outHeaders.Request.Forward), label)
	}
	if len(r.Header["Host"]) > 0 {
		r.Host = r.Header["Host"][0]
	}
	tool := ""
	if s.currentToolCall != nil {
		tool = s.currentToolCall.Tool
		clientInfo := util.BuildGotoClientInfo(nil, 0, s.Name, "", s.currentToolCall.Tool, s.currentToolCall.URL, s.currentToolCall.Server, nil, s.currentArgs,
			s.inHeaders, r.Header, s.outHeaders.Request.Forward, s.outHeaders.Request.Add, s.outHeaders.Request.Remove,
			map[string]any{
				"Tool-Call": s.currentToolCall,
			})
		s.currentOutput[constants.HeaderGotoClientInfo] = clientInfo[constants.HeaderGotoClientInfo]
	}
	log.Printf("---------- Outbound request headers from MCP client {%s} for {%s}[tool: %s] to {%s} ------------\n", s.CallerId, s.currentRequest, tool, s.URL)
	log.Println(util.ToJSONText(r.Header))
}

func (s *MCPSession) InterceptResponse(r *http.Response) {
	if s.outHeaders != nil && s.outHeaders.Response != nil {
		s.outHeaders.Response.UpdateHeaders(r.Header, fmt.Sprintf("MCP client response for %s", s.mcpClient.CallerId))
	}
	s.respHeaders = r.Header
	tool := ""
	if s.currentToolCall != nil {
		tool = s.currentToolCall.Tool
	}
	log.Printf("---------- Response headers from MCP client {%s} for {%s}[tool: %s] to {%s} ------------\n", s.CallerId, s.currentRequest, tool, s.URL)
	log.Println(util.ToJSONText(r.Header))
}

func (s *MCPSession) SendingMiddleware(next gomcp.MethodHandler) gomcp.MethodHandler {
	return func(ctx context.Context, method string, req gomcp.Request) (result gomcp.Result, err error) {
		s.currentRequest = method
		tool := ""
		if s.currentToolCall != nil {
			tool = s.currentToolCall.Tool
		}
		log.Printf("---------- Outbound request from MCP client {%s} for {%s}[tool: %s] to {%s} ------------\n", s.CallerId, method, tool, s.URL)
		if ctp, ok := req.GetParams().(*gomcp.CallToolParams); ok {
			if args, ok := ctp.Arguments.(map[string]any); ok && args != nil {
				args["goto-client"] = global.Self.HostLabel
			}
		}
		return next(ctx, method, req)
	}
}

func (s *MCPSession) ReceivingMiddleware(next gomcp.MethodHandler) gomcp.MethodHandler {
	return func(ctx context.Context, method string, req gomcp.Request) (result gomcp.Result, err error) {
		s.currentRequest = method
		tool := ""
		if s.currentToolCall != nil {
			tool = s.currentToolCall.Tool
		}
		log.Printf("---------- Response received by MCP client {%s} for {%s}[tool: %s] from {%s} ------------\n", s.CallerId, method, tool, s.URL)
		return next(ctx, method, req)
	}
}

func (t *MCPClientInterceptTransport) Connect(ctx context.Context) (gomcp.Connection, error) {
	return t.Transport.Connect(ctx)
}

func (s *MCPSession) SetAuthority(authority string) {
	s.Authority = authority
}

func (s *MCPSession) Close() {
	s.session.Close()
	s.session = nil
	s.mcpClient.RemoveSession(s.ID)
}

func (s *MCPSession) ResponseHeaders() http.Header {
	return s.respHeaders
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
	if c.Stop {
		log.Println("Received elicitation after client stopped. Ignoring.")
		return
	}
	label := fmt.Sprintf("%s[Elicitation]", c.CallerId)
	msg := ""
	var hops *util.Hops
	s := c.GetSession(req.Session.ID())
	if s == nil {
		msg = fmt.Sprintf("Session missing for ID [%s]", req.Session.ID())
		hops = util.NewHops(c.CallerId, c.Listener, label)
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
	if c.progressChan != nil {
		c.progressChan <- msg
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
	if c.Stop {
		log.Println("Received message after client stopped. Ignoring.")
		return nil, nil
	}
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
		hops = util.NewHops(c.CallerId, c.Listener, label)
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
	if c.progressChan != nil {
		c.progressChan <- msg
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
	if c.Stop {
		log.Println("Received progress notification after client stopped. Ignoring.")
		return
	}
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

func (tc *ToolCall) UpdateAndClone(tool, url, server, authority, delay string, headers *types.Headers, args map[string]any) *ToolCall {
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
	clone.Headers = types.Union(tc.Headers, headers)
	clone.Args = maps.Clone(tc.Args)
	for k, v := range args {
		clone.Args[k] = v
	}
	return &clone
}
