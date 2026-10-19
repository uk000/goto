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
	"io"
	"log"
	"maps"
	"net/http"
	"slices"
	"sort"
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
	Tool                 string                 `json:"tool"`
	URL                  string                 `json:"url"`
	SSEURL               string                 `json:"sseURL"`
	Server               string                 `json:"server,omitempty"`
	Authority            string                 `json:"authority,omitempty"`
	H2                   bool                   `json:"h2,omitempty"`
	TLS                  bool                   `json:"tls,omitempty"`
	ForceSSE             bool                   `json:"forceSSE,omitempty"`
	Raw                  bool                   `json:"neat,omitempty"`
	Args                 *aicommon.ToolCallArgs `json:"args,omitempty"`
	Headers              *types.Headers         `json:"headers,omitempty"`
	Delay                string                 `json:"delay,omitempty"`
	RequestCount         int                    `json:"requestCount"`
	Concurrent           int                    `json:"concurrent"`
	RequestDelay         string                 `json:"requestDelay"`
	InitialDelay         string                 `json:"initialDelay"`
	RetryDelay           string                 `json:"retryDelay"`
	RetriableStatusCodes []int                  `json:"retriableStatusCodes"`
	RequestTimeout       string                 `json:"requestTimeout"`
	RequestId            *types.RequestId       `json:"requestId"`
	ForcedStatus         int                    `json:"forcedStatus"`
	ResultOnly           bool                   `json:"resultOnly,omitempty"`
	NoEvents             bool                   `json:"noEvents,omitempty"`
	NoCallDetails        bool                   `json:"noCallDetails,omitempty"`
	Stream               bool                   `json:"stream,omitempty"`
	delayD               *types.Delay           `json:"-"`
	RequestTimeoutD      time.Duration          `json:"-"`
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
	concurrent int
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
	// mu guards currentRequest, ongoingCalls, outHeaders and the response tracking below, which the callbacks and the
	// HTTP transport use from their own goroutines.
	mu sync.Mutex
	// pending counts the responses whose bodies are still to be done (their trailers are processed then), and
	// pendingChanged is closed (and replaced) whenever one of them is done.
	pending        int
	pendingChanged chan struct{}
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

// defaultRequestTimeout is the time that a request is given when no (valid) timeout is configured.
const defaultRequestTimeout = 10 * time.Hour

// responseWait is how long a call waits, after its requests are done, for the bodies of their responses to be done,
// which is when the trailers of the responses are stored in the result.
const responseWait = 2 * time.Second

// updateNamedClientPayload changes the payload of the named client, which is created when it doesn't exist. Payloads
// are never changed after they are published: the update is made to a copy that replaces the payload, so that the
// sessions and the API that use a payload don't race with the updates.
func updateNamedClientPayload(name string, update func(np *MCPNamedClientPayload)) {
	lock.Lock()
	defer lock.Unlock()
	if NamedClientPayloads == nil {
		NamedClientPayloads = map[string]*MCPNamedClientPayload{}
	}
	old := NamedClientPayloads[name]
	np := &MCPNamedClientPayload{}
	if old != nil {
		*np = *old
	}
	np.Name = name
	update(np)
	NamedClientPayloads[name] = np
	if old == nil || DefaultClientPayload == old {
		DefaultClientPayload = np
	}
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

// namedClientPayloadsSnapshot returns a copy of the registry of client payloads.
func namedClientPayloadsSnapshot() map[string]*MCPNamedClientPayload {
	lock.RLock()
	defer lock.RUnlock()
	return maps.Clone(NamedClientPayloads)
}

func AddPayload(name, kind string, b []byte) error {
	payload := &MCPClientPayload{}
	if err := json.Unmarshal(b, &payload); err != nil {
		return err
	}
	updateNamedClientPayload(name, func(np *MCPNamedClientPayload) {
		if kind == "elicit" {
			np.ElicitPayload = payload
		} else {
			np.SamplePayload = payload
		}
	})
	return nil
}

func SetRoots(name string, payload []byte) error {
	var roots []*gomcp.Root
	if err := util.ReadJsonFromBytes(payload, &roots); err != nil {
		return err
	}
	updateNamedClientPayload(name, func(np *MCPNamedClientPayload) {
		np.Roots = roots
	})
	return nil
}

func NewClient(port int, sse, h2, tls bool, callerId, listener, authority string, requestTimeout time.Duration, stream chan *types.Pair[string, any], updateCallback timeline.TimelineUpdateNotifierFunc, endCallback timeline.TimelineEndNotifierFunc) *MCPClient {
	name := fmt.Sprintf("GotoMCP[%s]", global.Funcs.GetListenerLabelForPort(port))
	if sse {
		name += "[sse]"
	}
	return newMCPClient(port, sse, h2, tls, name, callerId, listener, authority, requestTimeout, stream, updateCallback, endCallback)
}

func newMCPClient(port int, sse, h2, tls bool, name, callerId, listener, authority string, requestTimeout time.Duration, stream chan *types.Pair[string, any], updateCallback timeline.TimelineUpdateNotifierFunc, endCallback timeline.TimelineEndNotifierFunc) *MCPClient {
	//httpClient := transport.CreateSimpleHTTPClient()
	if requestTimeout <= 0 {
		requestTimeout = 1 * time.Hour
	}
	ht := transport.CreateHTTPClient(port, name, h2, true, tls, false, authority,
		requestTimeout, 10*time.Minute, 10*time.Minute, metrics.ConnTracker)
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
	// The transport is an HTTP/1 or an HTTP/2 one, which both can be intercepted.
	if t, ok := ht.Transport().(transport.IHTTPTransportIntercept); ok {
		mcpClient.ht = t
		mcpClient.ht.SetRequestIntercept(mcpClient)
		mcpClient.ht.SetResponseIntercept(mcpClient)
	}
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

func ParseToolCall(r io.ReadCloser) (*ToolCall, error) {
	tc := &ToolCall{}
	err := util.ReadJsonOrYamlPayloadFromBody(r, tc)
	if err == nil {
		if tc.Tool == "" || tc.URL == "" {
			return nil, errors.New("invalid tool call payload")
		}
		tc.Prepare()
	}
	return tc, err
}

func (c *MCPClient) GetSession(sessionID string) *MCPSession {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.ActiveSessions[sessionID]
}

func (c *MCPClient) addSession(s *MCPSession) {
	c.lock.Lock()
	defer c.lock.Unlock()
	if c.ActiveSessions == nil {
		c.ActiveSessions = map[string]*MCPSession{}
	}
	c.ActiveSessions[s.ID] = s
}

func (c *MCPClient) RemoveSession(sessionID string) {
	c.lock.Lock()
	defer c.lock.Unlock()
	delete(c.ActiveSessions, sessionID)
}

func (c *MCPClient) OnConnClose() {
	log.Println("Received connection close notification")
	c.lock.Lock()
	defer c.lock.Unlock()
	c.ActiveSessions = map[string]*MCPSession{}
}

// sessionOf finds the session that a request of the client belongs to. The URL of the session carries its ID, but the
// requests that the SSE transport posts go to an endpoint that the server gives, which doesn't have it, so the ID that
// the requests of a session carry in their context is the fallback.
func (c *MCPClient) sessionOf(r *http.Request) *MCPSession {
	if r == nil {
		return nil
	}
	if r.URL != nil {
		if ids := r.URL.Query()[constants.HeaderGotoMCPSessionID]; len(ids) > 0 && len(ids[0]) > 0 {
			if s := c.GetSession(ids[0]); s != nil {
				return s
			}
		}
	}
	if id, ok := r.Context().Value(constants.HeaderGotoMCPSessionID).(string); ok && id != "" {
		return c.GetSession(id)
	}
	return nil
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
	var outHeaders *types.Headers
	if tc != nil {
		if tc.Headers == nil {
			tc.Headers = types.NewHeaders()
		}
		tc.Headers.NonNil()
		outHeaders = aicommon.CloneHeaders(tc.Headers)
		t.NoEvents = tc.NoEvents
		t.ResultOnly = tc.ResultOnly
	}
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
		outHeaders:   outHeaders,
		Timeline:     t,
		transport:    transport,
		ongoingCalls: map[string]*ToolCallContext{},
	}
	c.addSession(s)
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

// sessionContext adds the ID of the session to the context, so that the requests that are made with it can be matched
// to the session even when their URL doesn't have the ID.
func (s *MCPSession) sessionContext(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, constants.HeaderGotoMCPSessionID, s.ID)
}

func (s *MCPSession) connect() error {
	// A session that was closed by an earlier operation (listing closes it) has to be known to the client again, since
	// that's how its requests are matched to it.
	if s.mcpClient != nil {
		s.mcpClient.addSession(s)
	}
	cs, err := s.client.Connect(s.sessionContext(context.Background()), s.transport, &gomcp.ClientSessionOptions{})
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
	if tctx.args == nil {
		tctx.args = aicommon.NewCallArgs()
	}
	// The call works on its own copy, which it fills in, instead of changing the args it was given.
	tctx.args = tctx.args.Clone()
	tctx.args.NonNil()
	if tctx.args.DelayText == "" {
		tctx.args.DelayText = s.tc.Delay
	}
	return tctx
}

func (s *MCPSession) CallTool(args *aicommon.ToolCallArgs) (*MCPResult, error) {
	if s.tc == nil {
		return nil, errors.New("no tool call is configured for the session")
	}
	if err := s.connect(); err != nil {
		return nil, err
	}
	tctx := s.newToolCallContext(args)
	return tctx.call()
}

func (s *MCPSession) addOngoingCall(requestID string, tctx *ToolCallContext) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ongoingCalls == nil {
		s.ongoingCalls = map[string]*ToolCallContext{}
	}
	s.ongoingCalls[requestID] = tctx
}

// takeOngoingCall returns the call that the request ID belongs to, which is no longer ongoing after that.
func (s *MCPSession) takeOngoingCall(requestID string) *ToolCallContext {
	s.mu.Lock()
	defer s.mu.Unlock()
	tctx := s.ongoingCalls[requestID]
	delete(s.ongoingCalls, requestID)
	return tctx
}

func (s *MCPSession) setCurrentRequest(method string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentRequest = method
}

func (s *MCPSession) getCurrentRequest() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentRequest
}

// headers returns the headers that the session's requests get and its responses are updated with. The headers are
// never changed once they are set (setRequestHeaders replaces them), so that they are read without a lock.
func (s *MCPSession) headers() *types.Headers {
	s.mu.Lock()
	outHeaders := s.outHeaders
	s.mu.Unlock()
	if outHeaders == nil && s.Ctx != nil {
		if h, ok := s.Ctx.Value(util.HeadersKey).(*types.Headers); ok {
			outHeaders = h
		}
	}
	return outHeaders
}

// setRequestHeaders adds the headers to the ones that the requests of the session get. The headers of the session are
// replaced by a copy that has them, because requests are already using the headers (the connection is made before the
// call is prepared).
func (s *MCPSession) setRequestHeaders(add map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := aicommon.CloneHeaders(s.outHeaders)
	if h == nil {
		h = types.NewHeaders()
	}
	h.NonNil()
	maps.Copy(h.Request.Add, add)
	s.outHeaders = h
}

// trackResponse registers a response whose body is not done yet.
func (s *MCPSession) trackResponse() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending++
}

// responseDone is called when the body of a tracked response is done, and its trailers are stored.
func (s *MCPSession) responseDone() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending--
	if s.pendingChanged != nil {
		close(s.pendingChanged)
		s.pendingChanged = nil
	}
}

// waitForResponses waits for the bodies of the tracked responses to be done, so that what they add to the result is
// there when the call returns. It gives up after the timeout, as bodies that are never read to the end are never done.
func (s *MCPSession) waitForResponses(timeout time.Duration) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		s.mu.Lock()
		if s.pending <= 0 {
			s.mu.Unlock()
			return
		}
		if s.pendingChanged == nil {
			s.pendingChanged = make(chan struct{})
		}
		changed := s.pendingChanged
		s.mu.Unlock()
		select {
		case <-changed:
		case <-timer.C:
			return
		}
	}
}

func (tctx *ToolCallContext) prepare() {
	if tctx.ctx == nil {
		tctx.ctx = context.Background()
	}
	if tctx.args.RemoteArgs != nil && len(tctx.args.RemoteArgs.ForwardHeaders) == 0 {
		if outHeaders := tctx.session.headers(); outHeaders != nil && outHeaders.Request != nil {
			tctx.args.RemoteArgs.ForwardHeaders = slices.Clone(outHeaders.Request.Forward)
		}
	}
	if !tctx.tc.Raw {
		add := map[string]string{}
		// A host or a user agent that is configured is kept, unless there is another to replace it.
		if tctx.tc.Authority != "" {
			add["Host"] = tctx.tc.Authority
		}
		if tctx.callerId != "" {
			add["User-Agent"] = tctx.callerId
		}
		tctx.session.setRequestHeaders(add)
	}
	if tctx.tc.ResultOnly {
		tctx.args.ResultOnly = true
	}
	if tctx.tc.NoEvents {
		tctx.args.NoEvents = true
	}
	tctx.ctx = util.WithContextHeaders(tctx.ctx, tctx.session.headers())
	if tctx.tc.RequestCount <= 0 {
		tctx.tc.RequestCount = 1
	}
	if tctx.tc.Concurrent <= 0 {
		tctx.tc.Concurrent = 1
	}
	// The requests are made in rounds of concurrent requests. The last round has the requests that are left, when the
	// request count can't be divided by the concurrency, and there are no more concurrent requests than requests.
	tctx.concurrent = min(tctx.tc.Concurrent, tctx.tc.RequestCount)
	tctx.rounds = (tctx.tc.RequestCount + tctx.concurrent - 1) / tctx.concurrent
}

func (tctx *ToolCallContext) reportInitiateToolCall() {
	msg := fmt.Sprintf("%s [%s] Initiating Tool Call [%s] on URL [%s], Request Count [%d], Concurrent [%d]",
		tctx.callerId, tctx.session.Operation, tctx.tc.Tool, tctx.tc.URL, tctx.tc.RequestCount, tctx.tc.Concurrent)
	tctx.clientInfo = timeline.BuildGotoClientInfo(tctx.client.Port, tctx.callerId, tctx.tc.Tool, tctx.tc.URL, tctx.tc.Server, tctx.session.inHeaders, nil,
		tctx.args, nil, tctx.tc.RequestCount, tctx.tc.Concurrent, map[string]any{
			"Tool-Call": tctx.tc,
		})
	tctx.session.Timeline.AddEventWithClient(tctx.callerId, msg, tctx.clientInfo)
}

func (tctx *ToolCallContext) reportToolCallRequest(index int, args *aicommon.ToolCallArgs) {
	msg := fmt.Sprintf("%s [%s] Calling Tool [%s] on URL [%s], Request# [%d/%d], Args: %+v",
		tctx.callerId, tctx.session.Operation, tctx.tc.Tool, tctx.tc.URL, index, tctx.tc.RequestCount, args)
	tctx.AddEvent(msg)
}

func (tctx *ToolCallContext) reportToolCallFailure(index int, err string) {
	msg := fmt.Sprintf("%s [%s] Request# [%d/%d], Failed to call tool [%s] on URL [%s] with error [%s]",
		tctx.callerId, tctx.session.Operation, index, tctx.tc.RequestCount, tctx.tc.Tool, tctx.tc.URL, err)
	tctx.AddEvent(msg)
}

func (tctx *ToolCallContext) reportToolCallSuccess(index int) {
	msg := fmt.Sprintf("%s [%s] Request# [%d/%d], Tool [%s] called successfully on URL [%s]",
		tctx.callerId, tctx.session.Operation, index, tctx.tc.RequestCount, tctx.tc.Tool, tctx.tc.URL)
	tctx.AddEvent(msg)
}

func (tctx *ToolCallContext) reportToolCallResult(toolResult *gomcp.CallToolResult) {
	tctx.AddRemoteEvent(fmt.Sprintf("%s->%s", tctx.callerId, tctx.tc.Tool), "Result", toolResult, true)
}

func (tctx *ToolCallContext) call() (*MCPResult, error) {
	defer tctx.session.close()
	tctx.prepare()
	tctx.reportInitiateToolCall()
	initialDelay := types.ParseDelay(tctx.tc.InitialDelay)
	delay := types.ParseDelay(tctx.tc.RequestDelay)
	if initialDelay != nil && initialDelay.IsNonZero() {
		initialDelay.ComputeAndApply(func(d time.Duration) {
			log.Printf("MCP Client [%s]: Applying initial delay of  %s before proceesing with MCP requests\n", tctx.callerId, d)
		})
	}
	requestTimeout := tctx.tc.RequestTimeoutD
	if requestTimeout <= 0 {
		requestTimeout = defaultRequestTimeout
	}
	for i := 1; i <= tctx.rounds; i++ {
		requestsInRound := tctx.concurrent
		if i == tctx.rounds {
			requestsInRound = tctx.tc.RequestCount - (tctx.rounds-1)*tctx.concurrent
		}
		wg := &sync.WaitGroup{}
		for j := 1; j <= requestsInRound; j++ {
			// Every request has an ID of its own, that its results are stored under.
			index := (i-1)*tctx.concurrent + j
			requestID := fmt.Sprintf("%s.%d", tctx.session.ID, index)
			tctx.session.addOngoingCall(requestID, tctx)
			args := tctx.args.Clone()
			args.AddMetadata(constants.HeaderGotoMCPRequestID, requestID)
			args.AddMetadata(constants.HeaderGotoMCPSessionID, tctx.session.ID)
			args.AddMetadata(constants.HeaderGotoMCPCallerID, tctx.callerId)
			tctx.reportToolCallRequest(index, args)
			ctx := context.WithValue(tctx.session.sessionContext(tctx.ctx), constants.HeaderGotoMCPRequestID, requestID)
			wg.Add(1)
			go func(ctx context.Context, index int, requestID string, args *aicommon.ToolCallArgs) {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(ctx, requestTimeout)
				defer cancel()
				toolResult, err := tctx.session.session.CallTool(ctx, &gomcp.CallToolParams{Name: tctx.tc.Tool, Arguments: args})
				if toolResult != nil {
					tctx.result.storeCallResult(requestID, toolResult, tctx.clientInfo)
				}
				if err != nil {
					tctx.reportToolCallFailure(index, err.Error())
					tctx.result.setLastError(err)
				} else if toolResult != nil {
					tctx.reportToolCallSuccess(index)
				} else {
					tctx.reportToolCallFailure(index, "No Error, No Result")
				}
			}(ctx, index, requestID, args)
		}
		wg.Wait()
		tctx.session.waitForResponses(responseWait)
		if (tctx.rounds-i) > 0 && delay != nil && delay.IsNonZero() {
			delay.ComputeAndApply(func(d time.Duration) {
				log.Printf("MCP Client [%s]: Delaying by %s before proceesing with next round of MCP requests\n", tctx.callerId, d)
			})
		}
	}
	if tctx.tc.NoCallDetails {
		tctx.result.clearCallResults()
	}
	return tctx.result, tctx.result.lastError()
}

func (c *MCPClient) InterceptRequest(r *http.Request) {
	var tool, callerId, currentRequest string
	s := c.sessionOf(r)
	if s != nil {
		callerId = s.CallerId
		currentRequest = s.getCurrentRequest()
		outHeaders := s.headers()
		if outHeaders != nil && outHeaders.Request != nil {
			outHeaders.Request.UpdateHeaders(r.Header)
			types.ForwardHeaders(s.inHeaders, r.Header, slices.Values(outHeaders.Request.Forward))
		}
		if s.tc != nil {
			tool = s.tc.Tool
			r.Header.Add(constants.HeaderGotoMCPServer, s.tc.Server)
			r.Header.Add(constants.HeaderGotoMCPTool, s.tc.Tool)
		}
		if v := r.Context().Value(constants.HeaderGotoMCPRequestID); v != nil {
			if requestID, ok := v.(string); ok {
				r.Header.Add(constants.HeaderGotoMCPRequestID, requestID)
			}
		}
	}
	if len(r.Header["Host"]) > 0 {
		r.Host = r.Header["Host"][0]
	}
	if global.Flags.VerboseMCP {
		log.Printf("---------- Outbound request headers from MCP client {%s} for {%s}[tool: %s] to {%s} ------------\n", callerId, currentRequest, tool, r.URL.String())
		log.Println(util.ToJSONText(r.Header))
	}
}

func (c *MCPClient) InterceptResponse(r *http.Response) {
	var tool, callerId, currentRequest string
	var s *MCPSession
	if r.Request != nil {
		s = c.sessionOf(r.Request)
	}
	if s != nil {
		callerId = s.CallerId
		currentRequest = s.getCurrentRequest()
		outHeaders := s.headers()
		if outHeaders != nil && outHeaders.Response != nil {
			outHeaders.Response.UpdateHeaders(r.Header)
		}
		if s.tc != nil {
			tool = s.tc.Tool
			requestID := r.Request.Header.Get(constants.HeaderGotoMCPRequestID)
			if requestID != "" {
				if tctx := s.takeOngoingCall(requestID); tctx != nil {
					c.trackResponse(s, tctx, requestID, r)
				}
			}
		}
	}
	if global.Flags.VerboseMCP {
		target := ""
		if r.Request != nil && r.Request.URL != nil {
			target = r.Request.URL.String()
		}
		log.Printf("---------- Response headers from MCP client {%s} for {%s}[tool: %s] to {%s} ------------\n", callerId, currentRequest, tool, target)
		log.Println(util.ToJSONText(r.Header))
	}
}

// trackResponse stores what the response of a request of a tool call tells, and arranges for the trailers of the
// response to be stored too, when its body is done. The session knows of the response until then, so that the call
// that is waiting for the results can wait for it.
func (c *MCPClient) trackResponse(s *MCPSession, tctx *ToolCallContext, requestID string, r *http.Response) {
	tctx.result.storeHeaders(requestID, r.Request.Header.Clone(), r.Header.Clone(), r.StatusCode)
	tctx.result.lock.Lock()
	if tctx.clientInfo != nil {
		tctx.clientInfo.StoreHeaders(r.Request.Header.Clone())
	}
	tctx.result.lock.Unlock()
	tctx.result.storeRemoteGotos(util.GetViaGotosFromHeaders(r.Header))
	rs := util.GetRequestStore(r.Request)
	s.trackResponse()
	r.Body = util.NewResponseTracker(r, func(h http.Header) {
		defer s.responseDone()
		tctx.processHeaders(requestID, h, rs)
	})
}

// processHeaders is called when the body of a response is done, and the trailers of the response are among its headers.
// The header map is owned by the response, so the result gets a copy of it.
func (tctx *ToolCallContext) processHeaders(requestID string, h http.Header, rs *util.RequestStore) {
	h = h.Clone()
	if h == nil {
		h = http.Header{}
	}
	remoteGotos := util.GetViaGotosFromHeaders(h)
	viaGotos := []string{}
	for v := range remoteGotos {
		if !strings.Contains(v, "(A2A)") && !strings.Contains(v, "(MCP)") {
			v = v + "(MCP)"
		}
		viaGotos = append(viaGotos, v)
	}
	sort.Strings(viaGotos)
	h[constants.HeaderViaGoto] = viaGotos
	tctx.result.storeResponseDone(requestID, h, remoteGotos, util.GetUpstreamStatuses(h), rs)
}

func (s *MCPSession) SendingMiddleware(next gomcp.MethodHandler) gomcp.MethodHandler {
	return func(ctx context.Context, method string, req gomcp.Request) (result gomcp.Result, err error) {
		s.setCurrentRequest(method)
		tool := ""
		if s.tc != nil {
			tool = s.tc.Tool
		}
		if global.Flags.VerboseMCP {
			log.Printf("---------- Outbound request from MCP client {%s} for {%s}[tool: %s] to {%s} ------------\n", s.CallerId, method, tool, s.URL)
		}
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
		s.setCurrentRequest(method)
		tool := ""
		if s.tc != nil {
			tool = s.tc.Tool
		}
		if global.Flags.VerboseMCP {
			log.Printf("---------- Response received by MCP client {%s} for {%s}[tool: %s] from {%s} ------------\n", s.CallerId, method, tool, s.URL)
		}
		return next(ctx, method, req)
	}
}

func (t *MCPClientInterceptTransport) Connect(ctx context.Context) (gomcp.Connection, error) {
	return t.Transport.Connect(ctx)
}

// SetAuthority sets the authority that the requests of the session are made to. A session that has no tool call (one
// that lists tools, prompts and resources) has no headers to put it in, so it gets them.
func (s *MCPSession) SetAuthority(authority string) {
	s.mu.Lock()
	s.Authority = authority
	s.mu.Unlock()
	if s.tc == nil && authority != "" {
		s.setRequestHeaders(map[string]string{"Host": authority})
	}
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

func (s *MCPSession) listContext() context.Context {
	s.mu.Lock()
	authority := s.Authority
	s.mu.Unlock()
	return s.sessionContext(util.WithRequestHeaders(context.Background(), map[string][]string{"Host": []string{authority}}))
}

func (s *MCPSession) ListTools() (*gomcp.ListToolsResult, error) {
	if err := s.connect(); err != nil {
		return nil, err
	}
	defer s.close()
	return s.session.ListTools(s.listContext(), &gomcp.ListToolsParams{})
}

func (s *MCPSession) ListPrompts() (*gomcp.ListPromptsResult, error) {
	if err := s.connect(); err != nil {
		return nil, err
	}
	defer s.close()
	return s.session.ListPrompts(s.listContext(), &gomcp.ListPromptsParams{})
}

func (s *MCPSession) ListResources() (*gomcp.ListResourcesResult, error) {
	if err := s.connect(); err != nil {
		return nil, err
	}
	defer s.close()
	return s.session.ListResources(s.listContext(), &gomcp.ListResourcesParams{})
}

func (tc *ToolCall) Prepare() {
	tc.Tool = strings.Trim(strings.Trim(tc.Tool, "\""), "'")
	if tc.Server == "" {
		tc.Server = tc.Authority
	}
	if tc.Args == nil {
		tc.Args = aicommon.NewCallArgs()
	}
	tc.Args.NonNil()
	if tc.Delay != "" {
		tc.delayD = types.ParseDelay(tc.Delay)
	}
	if tc.RequestTimeout != "" {
		tc.RequestTimeoutD = util.ParseDuration(tc.RequestTimeout)
	}
	// A timeout that is not there, doesn't parse or is not positive would make every request time out as soon as it's
	// sent, so the default is used instead.
	if tc.RequestTimeoutD <= 0 {
		tc.RequestTimeoutD = defaultRequestTimeout
	}
}

func (tc *ToolCall) UpdateAndClone(tool, url, server, authority, delay string, headers *types.Headers, args *aicommon.ToolCallArgs) *ToolCall {
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
	if tc.Args != nil {
		clone.Args = tc.Args.Clone()
	}
	if args != nil {
		if clone.Args == nil {
			clone.Args = aicommon.NewCallArgs()
		}
		clone.Args.NonNil()
		clone.Args.UpdateFrom(args)
	}
	return &clone
}

func (tctx *ToolCallContext) AddEvent(msg string) {
	if msg != "" && (tctx.args == nil || !tctx.args.NoEvents) {
		tctx.session.Timeline.AddEvent(tctx.callerId, msg)
	}
}

func (tctx *ToolCallContext) AddRemoteEvent(msg string, remoteText string, remoteData any, json bool) {
	if msg != "" || remoteData != nil {
		tctx.session.Timeline.AddEventWithRemote(tctx.callerId, msg, remoteText, nil, nil, remoteData, json)
	}
}
