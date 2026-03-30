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
	aicommon "goto/pkg/ai/common"
	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/metrics"
	"goto/pkg/transport"
	"goto/pkg/types"
	"goto/pkg/util"
	"log"
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
	Tool         string                 `json:"tool"`
	URL          string                 `json:"url"`
	SSEURL       string                 `json:"sseURL"`
	Server       string                 `json:"server,omitempty"`
	Authority    string                 `json:"authority,omitempty"`
	H2           bool                   `json:"h2,omitempty"`
	TLS          bool                   `json:"tls,omitempty"`
	ForceSSE     bool                   `json:"forceSSE,omitempty"`
	Raw          bool                   `json:"neat,omitempty"`
	Args         *aicommon.ToolCallArgs `json:"args,omitempty"`
	Headers      *types.Headers         `json:"headers,omitempty"`
	Delay        string                 `json:"delay,omitempty"`
	RequestCount int                    `json:"requestCount"`
	Concurrent   int                    `json:"concurrent"`
	InitialDelay string                 `json:"initialDelay"`
	delayD       *types.Delay           `json:"-"`
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
	currentArgs     *aicommon.ToolCallArgs
	ongoingCalls    map[string]*MCPResult
	inHeaders       http.Header
	outHeaders      *types.Headers
	ResponseHeaders http.Header
	requestContext  context.Context
	transport       gomcp.Transport
}

type MCPClient struct {
	ID                   string
	Name                 string
	CallerId             string
	Listener             string
	SSE                  bool
	TLS                  bool
	Stop                 bool
	ActiveSessions       map[string]*MCPSession
	httpClient           *http.Client
	ht                   transport.IHTTPTransportIntercept
	mcpTransport         *MCPClientInterceptTransport
	localProgressChan    chan *types.Pair[string, any]
	upstreamProgressChan chan *types.Pair[string, any]
	client               *gomcp.Client
	clientPayload        *MCPNamedClientPayload
	sessionCounter       atomic.Int32
	lock                 sync.RWMutex
}

type MCPNamedClientPayload struct {
	Name          string
	ElicitPayload *MCPClientPayload
	SamplePayload *MCPClientPayload
	Roots         []*gomcp.Root
}

type MCPClientInterceptTransport struct {
	sseTransport    *gomcp.SSEClientTransport
	streamTransport *gomcp.StreamableClientTransport
	gomcp.Transport
}

type MCPCallEvent struct {
	LocalUpdate string
	RemoteData  *types.Pair[string, any]
}

type MCPCallResult struct {
	ClientInfo   map[string]map[string]any
	LocalUpdates []string
	RemoteData   map[string]any
	parent       *MCPResult
}

type MCPResult struct {
	URL                  string
	CallResults          []*MCPCallResult
	OrderedEvents        []*MCPCallEvent
	localProgressChan    chan *types.Pair[string, any]
	upstreamProgressChan chan *types.Pair[string, any]
}

const (
	GotoMCPSessionID = "goto-mcp-session-id"
	GotoMCPRequestID = "goto-mcp-request-id"
	GotoMCPCallerID  = "goto-mcp-caller-id"
)

var (
	ClientCounter        = atomic.Int32{}
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

func NewClient(port int, sse, h2, tls bool, callerId, listener, authority string, localProgressChan, upstreamProgressChan chan *types.Pair[string, any]) *MCPClient {
	name := fmt.Sprintf("GotoMCP[%s][%s]", global.Funcs.GetListenerLabelForPort(port), callerId)
	if sse {
		name += "[sse]"
	}
	return newMCPClient(sse, h2, tls, name, callerId, listener, authority, localProgressChan, upstreamProgressChan)
}

func newMCPClient(sse, h2, tls bool, name, callerId, listener, authority string, localProgressChan, upstreamProgressChan chan *types.Pair[string, any]) *MCPClient {
	//httpClient := transport.CreateSimpleHTTPClient()
	ht := transport.CreateHTTPClient(name, h2, true, tls, authority, 0,
		10*time.Minute, 10*time.Minute, 10*time.Minute, metrics.ConnTracker)
	mcpClient := &MCPClient{
		ID:                   fmt.Sprint(ClientCounter.Add(1)),
		Name:                 name,
		CallerId:             callerId,
		Listener:             listener,
		SSE:                  sse,
		TLS:                  tls,
		httpClient:           ht.HTTP(),
		ActiveSessions:       map[string]*MCPSession{},
		localProgressChan:    localProgressChan,
		upstreamProgressChan: upstreamProgressChan,
		sessionCounter:       atomic.Int32{},
	}
	mcpClient.client = gomcp.NewClient(&gomcp.Implementation{Name: name, Version: "2.0"}, &gomcp.ClientOptions{
		KeepAlive:                   10 * time.Second,
		CreateMessageHandler:        mcpClient.CreateMessageHandler,
		ElicitationHandler:          mcpClient.ElicitationHandler,
		ToolListChangedHandler:      mcpClient.ToolListChangedHandler,
		PromptListChangedHandler:    mcpClient.PromptListChangedHandler,
		ResourceListChangedHandler:  mcpClient.ResourceListChangedHandler,
		ResourceUpdatedHandler:      mcpClient.ResourceUpdatedHandler,
		LoggingMessageHandler:       mcpClient.LoggingMessageHandler,
		ProgressNotificationHandler: mcpClient.ProgressNotificationHandler,
	})
	mcpClient.clientPayload = getNamedClientPayload(name)
	if mcpClient.clientPayload != nil {
		mcpClient.client.AddRoots(mcpClient.clientPayload.Roots...)
	}
	if t, ok := ht.Transport().(*transport.HTTPTransportIntercept); ok {
		mcpClient.ht = t
	} else if t, ok := ht.Transport().(*transport.HTTP2TransportIntercept); ok {
		mcpClient.ht = t
	}
	mcpClient.ht.SetRequestIntercept(mcpClient)
	mcpClient.ht.SetResponseIntercept(mcpClient)
	return mcpClient
}

func (c *MCPClient) newMCPTransport(label, url string) gomcp.Transport {
	mcpTransport := &MCPClientInterceptTransport{}
	if c.SSE {
		mcpTransport.sseTransport = &gomcp.SSEClientTransport{Endpoint: url, HTTPClient: c.httpClient}
		mcpTransport.Transport = mcpTransport.sseTransport
	} else {
		mcpTransport.streamTransport = &gomcp.StreamableClientTransport{Endpoint: url, MaxRetries: -1, HTTPClient: c.httpClient}
		mcpTransport.Transport = mcpTransport.streamTransport
	}
	return mcpTransport
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
			tc.Args = aicommon.NewCallArgs()
		}
		if tc.Delay != "" {
			tc.delayD = types.ParseDelay(tc.Delay)
		}
	}
	return tc, err
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
	c.ActiveSessions = map[string]*MCPSession{}
}

func (c *MCPClient) CreateSession(url, operLabel string, tc *ToolCall, inHeaders http.Header) *MCPSession {
	return c.CreateSessionWithHops(url, operLabel, tc, inHeaders, nil)
}

func (c *MCPClient) CreateSessionWithHops(url, operLabel string, tc *ToolCall, inHeaders http.Header, hops *util.Hops) *MCPSession {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		if c.TLS {
			url = "https://" + url
		} else {
			url = "http://" + url
		}
	}
	return c.newMCPSession(operLabel, url, tc, inHeaders, hops)
}

func (c *MCPClient) newMCPSession(operLabel, url string, tc *ToolCall, inHeaders http.Header, hops *util.Hops) *MCPSession {
	id := fmt.Sprintf("%s.%d", c.ID, c.sessionCounter.Add(1))
	if !strings.Contains(url, "?") {
		url = fmt.Sprintf("%s?%s=%s", url, GotoMCPSessionID, id)
	} else {
		url = fmt.Sprintf("%s&%s=%s", url, GotoMCPSessionID, id)
	}
	t := c.newMCPTransport(c.Name, url)
	s := &MCPSession{
		ID:           id,
		Name:         c.Name,
		CallerId:     c.CallerId,
		Listener:     c.Listener,
		Operation:    operLabel,
		URL:          url,
		SSE:          c.SSE,
		mcpClient:    c,
		transport:    t,
		ongoingCalls: map[string]*MCPResult{},
	}

	if hops != nil {
		s.Hops = hops
	} else {
		s.Hops = util.NewHops(c.CallerId, c.Listener, operLabel)
	}
	c.lock.Lock()
	c.ActiveSessions[s.ID] = s
	c.lock.Unlock()
	c.client.AddSendingMiddleware(s.SendingMiddleware)
	c.client.AddReceivingMiddleware(s.ReceivingMiddleware)
	s.currentToolCall = tc
	s.inHeaders = inHeaders
	s.outHeaders = tc.Headers
	return s
}

func (s *MCPSession) connect() error {
	cs, err := s.mcpClient.client.Connect(context.Background(), s.transport, &gomcp.ClientSessionOptions{})
	if err == nil {
		s.session = cs
		return nil
	}
	return err
}

func (s *MCPSession) Call(args *aicommon.ToolCallArgs, inHeaders http.Header) (*MCPResult, error) {
	return s.CallTool(s.currentToolCall, args, inHeaders)
}

func (s *MCPSession) CallTool(tc *ToolCall, args *aicommon.ToolCallArgs, inHeaders http.Header) (*MCPResult, error) {
	if err := s.connect(); err != nil {
		return nil, err
	}
	defer s.close()
	msg := ""
	result := NewMCPResult(tc.URL, s.mcpClient.localProgressChan, s.mcpClient.upstreamProgressChan)
	s.ongoingCalls[tc.URL] = result
	cr := result.addResult()
	if tc == nil {
		tc = s.currentToolCall
	}
	if tc == nil {
		return nil, errors.New("No tool given")
	}
	if args == nil {
		args = tc.Args
	}
	if args.DelayText == "" {
		args.DelayText = tc.Delay
	}
	msg = fmt.Sprintf("%s [%s] Initiating Tool Call [%s] with sse[%t] on url [%s]. Request Count [%d], Concurrent [%d]", s.CallerId, s.Operation, tc.Tool, s.SSE, tc.URL, tc.RequestCount, tc.Concurrent)
	s.Hops.Add(msg)
	cr.addLocalUpdate(tc.Tool, msg)
	ctx := context.Background()
	if args.Remote != nil {
		if len(args.Remote.ForwardHeaders) == 0 && tc.Headers != nil && tc.Headers.Request != nil {
			args.Remote.ForwardHeaders = tc.Headers.Request.Forward
		}
	}
	s.currentArgs = args
	outHeaders := s.outHeaders
	if outHeaders == nil {
		outHeaders = tc.Headers
	}
	if outHeaders == nil {
		outHeaders = types.NewHeaders()
	}
	if !tc.Raw {
		outHeaders.Request.Add["Host"] = tc.Authority
		outHeaders.Request.Add["User-Agent"] = s.CallerId
	}
	ctx = util.WithContextHeaders(ctx, outHeaders)
	s.requestContext = ctx
	if inHeaders != nil {
		s.inHeaders = inHeaders
	}
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
	cr.addLocalUpdate(tc.Tool, msg)
	for i := 1; i <= rounds; i++ {
		cr := result.addResult()
		requestID := fmt.Sprintf("%s.%d", s.ID, i)
		msg = fmt.Sprintf("[%s] Calling tool [%s] on url [%s]. Request #%d/%d", s.CallerId, tc.Tool, tc.URL, i, requestCount)
		s.Hops.Add(msg)
		cr.addLocalUpdate(tc.Tool, msg)
		args.AddMetadata(GotoMCPRequestID, requestID)
		args.AddMetadata(GotoMCPCallerID, s.CallerId)
		toolResult, err := s.session.CallTool(ctx, &gomcp.CallToolParams{Name: tc.Tool, Arguments: args})
		if err != nil {
			msg = fmt.Sprintf("%s --> Request #%d/%d: Failed to call tool [%s]/sse[%t] on url [%s] with error [%s]", s.CallerId, i, requestCount, tc.Tool, s.SSE, tc.URL, err.Error())
			s.Hops.Add(msg)
			log.Println(s.Hops.AsJSONText())
			cr.addLocalUpdate(tc.Tool, msg)
		} else {
			cr.storeResult(tc, toolResult, s.Hops)
			msg = fmt.Sprintf("%s --> Request #%d/%d: Tool [%s](sse=%t) successful on URL [%s]", s.CallerId, i, requestCount, tc.Tool, s.SSE, tc.URL)
			s.Hops.Add(fmt.Sprintf("%s %s", s.CallerId, msg))
			log.Println(msg)
		}
	}
	return result, nil
}

func (c *MCPClient) InterceptRequest(r *http.Request) {
	qp := r.URL.Query()
	var s *MCPSession
	var tool, callerId string
	if sessionID := qp[GotoMCPSessionID]; len(sessionID) > 0 && len(sessionID[0]) > 0 {
		s = c.ActiveSessions[sessionID[0]]
	}
	if s != nil {
		callerId = s.CallerId
		outHeaders := s.outHeaders
		if outHeaders == nil && s.requestContext != nil {
			outHeaders = util.GetContextHeaders(s.requestContext)
		}
		if outHeaders != nil && outHeaders.Request != nil {
			label := fmt.Sprintf("MCP client request for %s", s.mcpClient.CallerId)
			outHeaders.Request.UpdateHeaders(r.Header, label)
			types.ForwardHeaders(s.inHeaders, r.Header, slices.Values(outHeaders.Request.Forward), label)
		}
		if s.currentToolCall != nil {
			tool = s.currentToolCall.Tool
		}
		result := s.ongoingCalls[s.currentToolCall.URL]
		if result != nil {
			clientInfo := util.BuildGotoClientInfo(nil, 0, s.Name, "", s.currentToolCall.Tool, s.currentToolCall.URL, s.currentToolCall.Server, nil, s.currentArgs,
				s.inHeaders, r.Header, outHeaders.Request.Forward, outHeaders.Request.Add, outHeaders.Request.Remove,
				map[string]any{
					"Tool-Call": s.currentToolCall,
				})
			result.addClientInfo(s.ID, clientInfo)
		}
	}
	if len(r.Header["Host"]) > 0 {
		r.Host = r.Header["Host"][0]
	}
	log.Printf("---------- Outbound request headers from MCP client {%s} for [tool: %s] to {%s} ------------\n", callerId, tool, r.URL.String())
	log.Println(util.ToJSONText(r.Header))
}

func (c *MCPClient) InterceptResponse(r *http.Response) {
	var s *MCPSession
	var tool, callerId string
	qp := r.Request.URL.Query()
	if sessionID := qp[GotoMCPSessionID]; len(sessionID) > 0 && len(sessionID[0]) > 0 {
		s = c.ActiveSessions[sessionID[0]]
	}
	if s != nil {
		callerId = s.CallerId
		outHeaders := s.outHeaders
		if outHeaders == nil && s.requestContext != nil {
			outHeaders = util.GetContextHeaders(s.requestContext)
		}
		if outHeaders != nil && outHeaders.Response != nil {
			outHeaders.Response.UpdateHeaders(r.Header, fmt.Sprintf("MCP client response for %s", s.mcpClient.CallerId))
		}
		if s.currentToolCall != nil {
			tool = s.currentToolCall.Tool
		}
		s.ResponseHeaders = r.Header
	}
	log.Printf("---------- Response headers from MCP client {%s} for [tool: %s] to {%s} ------------\n", callerId, tool, r.Request.URL.String())
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

func (s *MCPSession) close() {
	if s.session != nil {
		s.session.Close()
		s.session = nil
	}
	if s.mcpClient != nil {
		s.mcpClient.RemoveSession(s.ID)
	}
}

func (s *MCPSession) ListTools() (*gomcp.ListToolsResult, error) {
	if err := s.connect(); err != nil {
		return nil, err
	}
	defer s.close()
	return s.session.ListTools(util.WithRequestHeaders(context.Background(), map[string][]string{"Host": []string{s.Authority}}), &gomcp.ListToolsParams{})
}

func (s *MCPSession) ListPrompts() (*gomcp.ListPromptsResult, error) {
	if err := s.connect(); err != nil {
		return nil, err
	}
	defer s.close()
	return s.session.ListPrompts(util.WithRequestHeaders(context.Background(), map[string][]string{"Host": []string{s.Authority}}), &gomcp.ListPromptsParams{})
}

func (s *MCPSession) ListResources() (*gomcp.ListResourcesResult, error) {
	if err := s.connect(); err != nil {
		return nil, err
	}
	defer s.close()
	return s.session.ListResources(util.WithRequestHeaders(context.Background(), map[string][]string{"Host": []string{s.Authority}}), &gomcp.ListResourcesParams{})
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
	if c.localProgressChan != nil {
		c.localProgressChan <- types.NewPair[string, any](c.CallerId, msg)
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
	if c.localProgressChan != nil {
		c.localProgressChan <- types.NewPair[string, any](c.CallerId, msg)
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
	if req.Params.Message != "" && c.upstreamProgressChan != nil {
		c.upstreamProgressChan <- types.NewPair[string, any](c.CallerId, req.Params.Message)
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

func (tc *ToolCall) UpdateAndClone(tool, url, server, authority, delay string, headers *types.Headers, args ...*aicommon.ToolCallArgs) *ToolCall {
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
	newArgs := *tc.Args
	clone.Args = &newArgs
	if args != nil {
		clone.Args.UpdateFrom(args...)
	}
	return &clone
}

func NewMCPResult(url string, localProgressChan, upstreamProgressChan chan *types.Pair[string, any]) *MCPResult {
	return &MCPResult{
		URL:                  url,
		CallResults:          []*MCPCallResult{},
		OrderedEvents:        []*MCPCallEvent{},
		localProgressChan:    localProgressChan,
		upstreamProgressChan: upstreamProgressChan,
	}
}

func (r *MCPResult) NewMCPCallResult() *MCPCallResult {
	return &MCPCallResult{
		RemoteData: map[string]any{},
		ClientInfo: map[string]map[string]any{},
		parent:     r,
	}
}

func (r *MCPResult) addResult() *MCPCallResult {
	cr := r.NewMCPCallResult()
	r.CallResults = append(r.CallResults, cr)
	return cr
}

func (r *MCPResult) addClientInfo(k string, v map[string]any) {
	if len(r.CallResults) > 0 {
		r.CallResults[0].addClientInfo(k, v)
	}
}

func (r *MCPResult) addEvent(tool string, localMsg string, remoteK string, removeV any) {
	e := &MCPCallEvent{}
	if localMsg != "" {
		e.LocalUpdate = localMsg
		if r.localProgressChan != nil {
			r.localProgressChan <- types.NewPair[string, any](tool, localMsg)
		}
	}
	if remoteK != "" {
		e.RemoteData = types.NewPair(remoteK, removeV)
		if r.upstreamProgressChan != nil {
			r.upstreamProgressChan <- types.NewPair(remoteK, removeV)
		}
	}
	r.OrderedEvents = append(r.OrderedEvents, e)
}

func (r *MCPResult) storeResult(index int, tc *ToolCall, result *gomcp.CallToolResult, hops *util.Hops) {
	if index < len(r.CallResults) {
		r.CallResults[index].storeResult(tc, result, hops)
	}
}

func (r *MCPResult) ToToolResult(msg string) *gomcp.CallToolResult {
	result := &gomcp.CallToolResult{}
	output := map[string]map[string]any{}
	output["toolResult"] = map[string]any{"": msg}
	upstreamClientInfoFound := false
	if len(r.CallResults) > 0 && len(r.CallResults[0].RemoteData) > 0 {
		clientInfo := r.CallResults[0].RemoteData[constants.HeaderGotoClientInfo]
		if clientInfo != nil {
			if m, ok := clientInfo.(map[string]any); ok {
				output[constants.HeaderGotoClientInfo] = m
				upstreamClientInfoFound = true
			} else {
				output[constants.HeaderGotoClientInfo] = map[string]any{"": clientInfo}
				upstreamClientInfoFound = true
			}
			delete(r.CallResults[0].RemoteData, constants.HeaderGotoClientInfo)
		}
	}
	if !upstreamClientInfoFound {

	}
	calls := map[string]map[int]map[string]any{}
	calls[r.URL] = map[int]map[string]any{}
	for i := 1; i < len(r.CallResults); i++ {
		callOutput := map[string]any{}
		calls[r.URL][i] = callOutput
		callData := r.CallResults[i]
		if m, ok := callData.RemoteData["structuredContent"].(map[string]any); ok {
			if m[constants.HeaderGotoServerInfo] != nil {
				callOutput[constants.HeaderGotoServerInfo] = m[constants.HeaderGotoServerInfo]
			}
			delete(m, constants.HeaderGotoServerInfo)
			if len(m) > 0 {
				callOutput["upstreamInfo"] = m
			}
		} else {
			callOutput["upstreamInfo"] = callData.RemoteData["structuredContent"]
		}
		delete(callData.RemoteData, "structuredContent")
		for k, v := range callData.RemoteData {
			callOutput[k] = v
		}
	}
	for i, event := range r.OrderedEvents {
		if event.LocalUpdate != "" {
			result.Content = append(result.Content, &gomcp.TextContent{Text: fmt.Sprintf("[%s][%d]: %s", r.URL, i, event.LocalUpdate)})
		} else {
			result.Content = append(result.Content, &gomcp.TextContent{Text: fmt.Sprintf("[%s][%d] %s: %+v", r.URL, i, event.RemoteData.Left, event.RemoteData.Right)})
		}
	}
	output["Calls"] = map[string]any{}
	for k, v := range calls {
		output["Calls"][k] = v
	}
	result.StructuredContent = output
	return result
}

func (r *MCPCallResult) addLocalUpdate(tool string, msg string) {
	r.LocalUpdates = append(r.LocalUpdates, msg)
	r.parent.addEvent(tool, msg, "", "")
}

func (r *MCPCallResult) addRemoteData(tool string, k string, v any) {
	r.RemoteData[k] = v
	r.parent.addEvent(tool, "", k, v)
}

func (r *MCPCallResult) addClientInfo(k string, v map[string]any) {
	r.ClientInfo[k] = v
}

func (r *MCPCallResult) storeResult(tc *ToolCall, result *gomcp.CallToolResult, hops *util.Hops) {
	if result.Content != nil {
		//var anyContent any
		if tc.Raw {
			r.RemoteData["content"] = result.Content
			//anyContent = result.Content
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
				r.RemoteData["content"] = content[0]
			} else {
				r.RemoteData["content"] = content
			}
			//anyContent = r.RemoteData["content"]
		}
		//r.parent.addEvent(tc.Tool, "", "content", map[string]any{"content": anyContent})
	}
	if result.StructuredContent != nil {
		if serverOutput, ok := result.StructuredContent.(map[string]any); ok {
			serverOutput = hops.MergeRemoteHops(serverOutput)
			if tc.Raw {
				for k, v := range serverOutput {
					r.RemoteData[k] = v
					r.parent.addEvent(tc.Tool, "", k, v)
				}
			} else {
				r.RemoteData["structuredContent"] = serverOutput
				r.parent.addEvent(tc.Tool, "", "structuredContent", serverOutput)
			}
		}
	}
}
