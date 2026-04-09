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
	"goto/pkg/util/timeline"
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
	ForcedStatus int                    `json:"forcedStatus"`
	ResultOnly   bool                   `json:"resultOnly,omitempty"`
	delayD       *types.Delay           `json:"-"`
}

type ToolCallContext struct {
	ctx        context.Context
	client     *MCPClient
	session    *MCPSession
	clientInfo *timeline.GotoClientInfo
	tc         *ToolCall
	callerId   string
	args       *aicommon.ToolCallArgs
	rounds     int
	result     *MCPResult
}

type MCPRequestContext struct {
	requestID string
	sessionID string
	tctx      *ToolCallContext
}

type MCPSession struct {
	Ctx               context.Context
	ID                string
	Name              string
	CallerId          string
	Listener          string
	URL               string
	Authority         string
	SSE               bool
	Operation         string
	tc                *ToolCall
	Timeline          *timeline.Timeline
	FirstActivityAt   time.Time
	LasatActivityAt   time.Time
	Stop              bool
	session           *gomcp.ClientSession
	mcpClient         *MCPClient
	client            *gomcp.Client
	clientPayload     *MCPNamedClientPayload
	currentRequest    string
	ongoingCalls      map[string]*ToolCallContext
	inHeaders         http.Header
	outHeaders        *types.Headers
	currentClientInfo *timeline.GotoClientInfo
	transport         gomcp.Transport
}

type MCPClient struct {
	Port           int
	ID             string
	Name           string
	CallerId       string
	Listener       string
	SSE            bool
	TLS            bool
	ActiveSessions map[string]*MCPSession
	httpClient     *http.Client
	ht             transport.IHTTPTransportIntercept
	mcpTransport   *MCPClientInterceptTransport
	stream         chan *types.Pair[string, any]
	updateCallback timeline.TimelineUpdateNotifierFunc
	endCallback    timeline.TimelineEndNotifierFunc
	sessionCounter atomic.Int32
	lock           sync.RWMutex
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

func NewClient(port int, sse, h2, tls bool, callerId, listener, authority string, stream chan *types.Pair[string, any], updateCallback timeline.TimelineUpdateNotifierFunc, endCallback timeline.TimelineEndNotifierFunc) *MCPClient {
	name := fmt.Sprintf("GotoMCP[%s][%s]", global.Funcs.GetListenerLabelForPort(port), callerId)
	if sse {
		name += "[sse]"
	}
	return newMCPClient(port, sse, h2, tls, name, callerId, listener, authority, stream, updateCallback, endCallback)
}

func newMCPClient(port int, sse, h2, tls bool, name, callerId, listener, authority string, stream chan *types.Pair[string, any], updateCallback timeline.TimelineUpdateNotifierFunc, endCallback timeline.TimelineEndNotifierFunc) *MCPClient {
	//httpClient := transport.CreateSimpleHTTPClient()
	ht := transport.CreateHTTPClient(name, h2, true, tls, authority, 0,
		10*time.Minute, 10*time.Minute, 10*time.Minute, metrics.ConnTracker)
	mcpClient := &MCPClient{
		Port:           port,
		ID:             fmt.Sprint(ClientCounter.Add(1)),
		Name:           name,
		CallerId:       callerId,
		Listener:       listener,
		SSE:            sse,
		TLS:            tls,
		httpClient:     ht.HTTP(),
		ActiveSessions: map[string]*MCPSession{},
		stream:         stream,
		updateCallback: updateCallback,
		endCallback:    endCallback,
		sessionCounter: atomic.Int32{},
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

func (c *MCPClient) CreateSession(ctx context.Context, url, operLabel string, tc *ToolCall, inHeaders http.Header) *MCPSession {
	return c.CreateSessionWithTimeline(ctx, url, operLabel, tc, inHeaders, timeline.NewTimeline(c.Port, operLabel, nil, nil, inHeaders, c.stream, c.updateCallback, c.endCallback))
}

func (c *MCPClient) CreateSessionWithTimeline(ctx context.Context, url, operLabel string, tc *ToolCall, inHeaders http.Header, timeline *timeline.Timeline) *MCPSession {
	if url == "" {
		url = tc.URL
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		if c.TLS {
			url = "https://" + url
		} else {
			url = "http://" + url
		}
	}
	return c.newMCPSession(ctx, operLabel, url, tc, inHeaders, timeline)
}

func addSessionIDQuery(url, id string) string {
	if !strings.Contains(url, "?") {
		url = fmt.Sprintf("%s?%s=%s", url, constants.HeaderGotoMCPSessionID, id)
	} else {
		url = fmt.Sprintf("%s&%s=%s", url, constants.HeaderGotoMCPSessionID, id)
	}
	return url
}

func (c *MCPClient) newMCPSession(ctx context.Context, operLabel, url string, tc *ToolCall, inHeaders http.Header, t *timeline.Timeline) *MCPSession {
	id := fmt.Sprintf("%s.%d", c.ID, c.sessionCounter.Add(1))
	url = addSessionIDQuery(url, id)
	transport := c.newMCPTransport(c.Name, url)
	if tc.Headers == nil {
		tc.Headers = types.NewHeaders()
	}
	tc.Headers.NonNil()
	s := &MCPSession{
		Ctx:          ctx,
		ID:           id,
		Name:         c.Name,
		CallerId:     c.CallerId,
		Listener:     c.Listener,
		Operation:    operLabel,
		URL:          url,
		SSE:          c.SSE,
		tc:           tc,
		mcpClient:    c,
		inHeaders:    inHeaders,
		outHeaders:   tc.Headers.Clone(),
		Timeline:     t,
		transport:    transport,
		ongoingCalls: map[string]*ToolCallContext{},
	}
	c.lock.Lock()
	c.ActiveSessions[s.ID] = s
	c.lock.Unlock()
	s.prepareClient()
	return s
}

func (s *MCPSession) prepareClient() {
	s.client = gomcp.NewClient(&gomcp.Implementation{Name: s.Name, Version: "2.0"}, &gomcp.ClientOptions{
		KeepAlive:                   10 * time.Second,
		CreateMessageHandler:        s.CreateMessageHandler,
		ElicitationHandler:          s.ElicitationHandler,
		ToolListChangedHandler:      s.ToolListChangedHandler,
		PromptListChangedHandler:    s.PromptListChangedHandler,
		ResourceListChangedHandler:  s.ResourceListChangedHandler,
		ResourceUpdatedHandler:      s.ResourceUpdatedHandler,
		LoggingMessageHandler:       s.LoggingMessageHandler,
		ProgressNotificationHandler: s.ProgressNotificationHandler,
	})
	s.client.AddSendingMiddleware(s.SendingMiddleware)
	s.client.AddReceivingMiddleware(s.ReceivingMiddleware)
	s.clientPayload = getNamedClientPayload(s.Name)
	if s.clientPayload != nil {
		s.client.AddRoots(s.clientPayload.Roots...)
	}
}

func (s *MCPSession) connect() error {
	cs, err := s.client.Connect(context.Background(), s.transport, &gomcp.ClientSessionOptions{})
	if err == nil {
		s.session = cs
		return nil
	}
	return err
}

func (s *MCPSession) newToolCallContext(args *aicommon.ToolCallArgs) *ToolCallContext {
	tctx := &ToolCallContext{
		ctx:      s.Ctx,
		client:   s.mcpClient,
		session:  s,
		tc:       s.tc,
		callerId: s.CallerId,
		args:     args,
		result:   NewMCPResult(s.URL, s.tc, s.Timeline),
	}
	if tctx.args == nil {
		tctx.args = s.tc.Args
	}
	if tctx.args.DelayText == "" {
		tctx.args.DelayText = s.tc.Delay
	}
	return tctx
}

func (s *MCPSession) CallTool(args *aicommon.ToolCallArgs) (*MCPResult, error) {
	if err := s.connect(); err != nil {
		return nil, err
	}
	tctx := s.newToolCallContext(args)
	return tctx.call()
}

func (tctx *ToolCallContext) prepare() {
	if tctx.args.Remote != nil {
		if len(tctx.args.Remote.ForwardHeaders) == 0 && tctx.session.outHeaders != nil && tctx.session.outHeaders.Request != nil {
			tctx.args.Remote.ForwardHeaders = tctx.session.outHeaders.Request.Forward
		}
	}
	if !tctx.tc.Raw {
		tctx.session.outHeaders.Request.Add["Host"] = tctx.tc.Authority
		tctx.session.outHeaders.Request.Add["User-Agent"] = tctx.callerId
	}
	tctx.ctx = util.WithContextHeaders(tctx.ctx, tctx.session.outHeaders)
	if tctx.tc.RequestCount == 0 {
		tctx.tc.RequestCount = 1
	}
	if tctx.tc.Concurrent == 0 {
		tctx.tc.Concurrent = 1
	}
	tctx.rounds = tctx.tc.RequestCount / tctx.tc.Concurrent
}

func (tctx *ToolCallContext) reportInitiateToolCall() {
	msg := fmt.Sprintf("%s [%s] Initiating Tool Call [%s] on URL [%s], Request Count [%d], Concurrent [%d]",
		tctx.callerId, tctx.session.Operation, tctx.tc.Tool, tctx.tc.URL, tctx.tc.RequestCount, tctx.tc.Concurrent)
	tctx.clientInfo = timeline.BuildGotoClientInfo(tctx.client.Port, tctx.callerId, tctx.tc.Tool, tctx.tc.URL, tctx.tc.Server, tctx.session.inHeaders, nil,
		tctx.args, nil, tctx.tc.RequestCount, tctx.tc.Concurrent, map[string]any{
			"Tool-Call": tctx.tc,
		})
	tctx.session.Timeline.AddEvent(tctx.callerId, msg, tctx.clientInfo, nil, true)
}

func (tctx *ToolCallContext) reportToolCallRequest(index int, args *aicommon.ToolCallArgs) {
	msg := fmt.Sprintf("%s [%s] Calling Tool [%s] on URL [%s], Request# [%d/%d], Args: %+v",
		tctx.callerId, tctx.session.Operation, tctx.tc.Tool, tctx.tc.URL, index, tctx.tc.RequestCount, args)
	tctx.session.Timeline.AddEvent(tctx.callerId, msg, nil, nil, true)
}

func (tctx *ToolCallContext) reportToolCallFailure(index int, err string) {
	msg := fmt.Sprintf("%s [%s] Request# [%d/%d], Failed to call tool [%s] on URL [%s] with error [%s]",
		tctx.callerId, tctx.session.Operation, index, tctx.tc.RequestCount, tctx.tc.Tool, tctx.tc.URL, err)
	tctx.session.Timeline.AddEvent(tctx.callerId, msg, nil, nil, true)
}

func (tctx *ToolCallContext) reportToolCallSuccess(index int) {
	msg := fmt.Sprintf("%s [%s] Request# [%d/%d], Tool [%s] called successfully on URL [%s]",
		tctx.callerId, tctx.session.Operation, index, tctx.tc.RequestCount, tctx.tc.Tool, tctx.tc.URL)
	tctx.session.Timeline.AddEvent(tctx.callerId, msg, nil, nil, true)
}

func (tctx *ToolCallContext) reportToolCallResult(toolResult *gomcp.CallToolResult) {
	tctx.session.Timeline.AddData(fmt.Sprintf("%s->%s", tctx.callerId, tctx.tc.Tool), toolResult, true)
}

func (tctx *ToolCallContext) call() (*MCPResult, error) {
	defer tctx.session.close()
	tctx.prepare()
	tctx.reportInitiateToolCall()
	for i := 1; i <= tctx.rounds; i++ {
		requestID := fmt.Sprintf("%s.%d", tctx.session.ID, i)
		tctx.session.ongoingCalls[requestID] = tctx
		args := tctx.args.Clone()
		args.AddMetadata(constants.HeaderGotoMCPRequestID, requestID)
		args.AddMetadata(constants.HeaderGotoMCPSessionID, tctx.session.ID)
		args.AddMetadata(constants.HeaderGotoMCPCallerID, tctx.callerId)
		tctx.reportToolCallRequest(i, args)
		ctx := context.WithValue(tctx.ctx, constants.HeaderGotoMCPRequestID, requestID)
		toolResult, err := tctx.session.session.CallTool(ctx, &gomcp.CallToolParams{Name: tctx.tc.Tool, Arguments: args})
		if err != nil {
			tctx.reportToolCallFailure(i, err.Error())
			tctx.result.LastError = err
		} else if toolResult != nil {
			tctx.reportToolCallSuccess(i)
			tctx.result.storeCallResult(requestID, toolResult, tctx.clientInfo)
		} else {
			tctx.reportToolCallFailure(i, "No Error, No Result")
		}
	}
	return tctx.result, tctx.result.LastError
}

func (c *MCPClient) InterceptRequest(r *http.Request) {
	qp := r.URL.Query()
	var s *MCPSession
	var tool, callerId string
	if sessionID := qp[constants.HeaderGotoMCPSessionID]; len(sessionID) > 0 && len(sessionID[0]) > 0 {
		s = c.ActiveSessions[sessionID[0]]
	}
	if s != nil {
		callerId = s.CallerId
		outHeaders := s.outHeaders
		if outHeaders == nil && s.Ctx != nil {
			outHeaders = util.GetContextHeaders(s.Ctx)
		}
		if outHeaders != nil && outHeaders.Request != nil {
			label := fmt.Sprintf("MCP client request for %s", s.mcpClient.CallerId)
			outHeaders.Request.UpdateHeaders(r.Header, label)
			types.ForwardHeaders(s.inHeaders, r.Header, slices.Values(outHeaders.Request.Forward), label)
		}
		if s.tc != nil {
			tool = s.tc.Tool
		}
		if v := r.Context().Value(constants.HeaderGotoMCPRequestID); v != nil {
			if requestID, ok := v.(string); ok {
				r.Header.Add(constants.HeaderGotoMCPRequestID, requestID)
			}
		}
		r.Header.Add(constants.HeaderGotoMCPServer, s.tc.Server)
		r.Header.Add(constants.HeaderGotoMCPTool, s.tc.Tool)
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
	if sessionID := qp[constants.HeaderGotoMCPSessionID]; len(sessionID) > 0 && len(sessionID[0]) > 0 {
		s = c.ActiveSessions[sessionID[0]]
	}
	if s != nil {
		callerId = s.CallerId
		outHeaders := s.outHeaders
		if outHeaders == nil && s.Ctx != nil {
			outHeaders = util.GetContextHeaders(s.Ctx)
		}
		if outHeaders != nil && outHeaders.Response != nil {
			outHeaders.Response.UpdateHeaders(r.Header, fmt.Sprintf("MCP client response for %s", s.mcpClient.CallerId))
		}
		if s.tc != nil {
			tool = s.tc.Tool
		}
		requestID := r.Request.Header.Get(constants.HeaderGotoMCPRequestID)
		if requestID != "" {
			tctx := s.ongoingCalls[requestID]
			if tctx != nil {
				tctx.result.storeHeaders(requestID, r.Request.Header, r.Header, r.StatusCode)
				tctx.clientInfo.StoreHeaders(r.Request.Header, r.Header)
			}
			delete(s.ongoingCalls, requestID)
		}
	}
	log.Printf("---------- Response headers from MCP client {%s} for {%s}[tool: %s] to {%s} ------------\n", callerId, s.currentRequest, tool, r.Request.URL.String())
	log.Println(util.ToJSONText(r.Header))
}

func (s *MCPSession) SendingMiddleware(next gomcp.MethodHandler) gomcp.MethodHandler {
	return func(ctx context.Context, method string, req gomcp.Request) (result gomcp.Result, err error) {
		s.currentRequest = method
		tool := ""
		if s.tc != nil {
			tool = s.tc.Tool
		}
		log.Printf("---------- Outbound request from MCP client {%s} for {%s}[tool: %s] to {%s} ------------\n", s.CallerId, method, tool, s.URL)
		if ctp, ok := req.GetParams().(*gomcp.CallToolParams); ok {
			if args, ok := ctp.Arguments.(*aicommon.ToolCallArgs); ok && args != nil {
				args.AddMetadata("goto-client", global.Self.HostLabel)
			}
		}
		result, err = next(ctx, method, req)
		return
	}
}

func (s *MCPSession) ReceivingMiddleware(next gomcp.MethodHandler) gomcp.MethodHandler {
	return func(ctx context.Context, method string, req gomcp.Request) (result gomcp.Result, err error) {
		s.currentRequest = method
		tool := ""
		if s.tc != nil {
			tool = s.tc.Tool
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

func (tctx *ToolCallContext) addEvent(label, text string) {

}
