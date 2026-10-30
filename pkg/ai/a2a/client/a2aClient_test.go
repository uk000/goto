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

package a2aclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goto/pkg/global"
	"goto/pkg/types"
	"goto/pkg/util"

	a2aproto "trpc.group/trpc-go/trpc-a2a-go/protocol"
	goa2aserver "trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
)

// callTimeout is the deadline of every call to an upstream agent in these tests.
const callTimeout = 20 * time.Second

var stubOnce sync.Once

// stubGlobalFuncs fills the hooks that the server wires up at startup. The request store of every request
// (the client creates one on demand) uses them.
func stubGlobalFuncs() {
	global.Funcs.GetPeers = func(string, *http.Request) map[string]string { return nil }
	global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsListenerPresent = func(int) bool { return false }
	global.Funcs.IsListenerOpen = func(int) bool { return false }
	global.Funcs.GetListenerID = func(int) string { return "" }
	global.Funcs.GetListenerLabel = func(*http.Request) string { return "" }
	global.Funcs.GetListenerLabelForPort = func(int) string { return "" }
	global.Funcs.IsListenerTLS = func(int) bool { return false }
	global.Funcs.GetCertInfo = func(int, string) (string, string) { return "", "" }
	global.Funcs.CloseConnectionsForPort = func(int) {}
	util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
}

func setup(t *testing.T) {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
}

func ctxWithTimeout(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	t.Cleanup(cancel)
	return ctx
}

// seenRequest is what an upstream agent saw of a request.
type seenRequest struct {
	Method  string
	Path    string
	Host    string
	Headers http.Header
	Body    string
}

// processorFunc is the behavior of an upstream agent.
type processorFunc func(ctx context.Context, msg a2aproto.Message, opts taskmanager.ProcessOptions, h taskmanager.TaskHandler) (*taskmanager.MessageProcessingResult, error)

func (f processorFunc) ProcessMessage(ctx context.Context, msg a2aproto.Message, opts taskmanager.ProcessOptions, h taskmanager.TaskHandler) (*taskmanager.MessageProcessingResult, error) {
	return f(ctx, msg, opts, h)
}

// upstream is an A2A agent served by the trpc server library, as a remote agent is.
type upstream struct {
	srv *httptest.Server
	// URL is the URL of the agent, which is also the URL in its card.
	URL string
	// forcedStatus is the HTTP status that POSTs (the agent calls) get instead of being served, when it is not 0.
	forcedStatus atomic.Int32
	// cardStatus is the HTTP status that the card fetches get instead of the card, when it is not 0.
	cardStatus atomic.Int32
	cardHits   atomic.Int32
	callHits   atomic.Int32
	mu         sync.Mutex
	seen       []seenRequest
	respHdrs   http.Header
}

func text(msg a2aproto.Message) string {
	var sb strings.Builder
	for _, p := range msg.Parts {
		switch tp := p.(type) {
		case *a2aproto.TextPart:
			sb.WriteString(tp.Text)
		case a2aproto.TextPart:
			sb.WriteString(tp.Text)
		}
	}
	return sb.String()
}

func dataOf(msg a2aproto.Message) map[string]any {
	for _, p := range msg.Parts {
		switch dp := p.(type) {
		case *a2aproto.DataPart:
			if m, ok := dp.Data.(map[string]any); ok {
				return m
			}
		case a2aproto.DataPart:
			if m, ok := dp.Data.(map[string]any); ok {
				return m
			}
		}
	}
	return nil
}

// echoProcessor answers with a text part ("echo: <input>") and a data part with the input data.
func echoProcessor(ctx context.Context, msg a2aproto.Message, opts taskmanager.ProcessOptions, h taskmanager.TaskHandler) (*taskmanager.MessageProcessingResult, error) {
	reply := a2aproto.NewMessage(a2aproto.MessageRoleAgent, []a2aproto.Part{
		a2aproto.NewTextPart("echo: " + text(msg)),
		a2aproto.NewDataPart(map[string]any{"input": dataOf(msg)}),
	})
	return &taskmanager.MessageProcessingResult{Result: &reply}, nil
}

// streamProcessor streams the given updates as working status messages, and then completes the task.
func streamProcessor(updates ...string) processorFunc {
	return func(ctx context.Context, msg a2aproto.Message, opts taskmanager.ProcessOptions, h taskmanager.TaskHandler) (*taskmanager.MessageProcessingResult, error) {
		taskID, err := h.BuildTask(nil, nil)
		if err != nil {
			return nil, err
		}
		sub, err := h.SubscribeTask(&taskID)
		if err != nil {
			return nil, err
		}
		go func() {
			for _, u := range updates {
				m := a2aproto.NewMessage(a2aproto.MessageRoleAgent, []a2aproto.Part{a2aproto.NewTextPart(u)})
				if err := h.UpdateTaskState(&taskID, a2aproto.TaskStateWorking, &m); err != nil {
					break
				}
			}
			m := a2aproto.NewMessage(a2aproto.MessageRoleAgent, []a2aproto.Part{a2aproto.NewTextPart("stream done")})
			_ = h.UpdateTaskState(&taskID, a2aproto.TaskStateCompleted, &m)
			sub.Close()
			_ = h.CleanTask(&taskID)
		}()
		return &taskmanager.MessageProcessingResult{StreamingEvents: sub}, nil
	}
}

func newUpstream(t *testing.T, name string, streaming bool, p processorFunc) *upstream {
	t.Helper()
	setup(t)
	u := &upstream{respHdrs: http.Header{}}
	u.srv = httptest.NewUnstartedServer(nil)
	u.URL = fmt.Sprintf("http://%s/agent/%s/", u.srv.Listener.Addr().String(), name)
	card := goa2aserver.AgentCard{
		Name:               name,
		Description:        "test agent " + name,
		URL:                u.URL,
		Version:            "1.0",
		Capabilities:       goa2aserver.AgentCapabilities{Streaming: &streaming},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
		Skills:             []goa2aserver.AgentSkill{{ID: "echo", Name: "echo", Tags: []string{"echo"}}},
	}
	tm, err := taskmanager.NewMemoryTaskManager(p)
	if err != nil {
		t.Fatal(err)
	}
	a2a, err := goa2aserver.NewA2AServer(card, tm)
	if err != nil {
		t.Fatal(err)
	}
	handler := a2a.Handler()
	u.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		u.mu.Lock()
		u.seen = append(u.seen, seenRequest{Method: r.Method, Path: r.URL.Path, Host: r.Host, Headers: r.Header.Clone(), Body: string(body)})
		for k, v := range u.respHdrs {
			w.Header()[k] = v
		}
		u.mu.Unlock()
		if r.Method == http.MethodGet {
			u.cardHits.Add(1)
			if s := int(u.cardStatus.Load()); s > 0 {
				http.Error(w, "forced card status", s)
				return
			}
		} else {
			u.callHits.Add(1)
			if s := int(u.forcedStatus.Load()); s > 0 {
				http.Error(w, "forced status", s)
				return
			}
		}
		handler.ServeHTTP(w, r)
	})
	u.srv.Start()
	t.Cleanup(func() {
		// An open stream keeps Close waiting, so the connections are closed first.
		u.srv.CloseClientConnections()
		u.srv.Close()
	})
	return u
}

func (u *upstream) requests() []seenRequest {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]seenRequest(nil), u.seen...)
}

func (u *upstream) calls() []seenRequest {
	var out []seenRequest
	for _, r := range u.requests() {
		if r.Method == http.MethodPost {
			out = append(out, r)
		}
	}
	return out
}

func (u *upstream) setResponseHeader(k, v string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.respHdrs.Add(k, v)
}

func (u *upstream) call(name, message string) *AgentCall {
	c := &AgentCall{Name: name, CardURL: u.URL, Message: message}
	c.Prepare()
	return c
}

// collector collects what the client reports to the callback of a call, which is called by the session.
type collector struct {
	mu    sync.Mutex
	items []collected
}

type collected struct {
	ID   string
	Text string
	Data any
}

func (c *collector) callback() AgentResultsCallback {
	return func(id, out string, data any) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.items = append(c.items, collected{ID: id, Text: out, Data: data})
	}
}

func (c *collector) texts() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, i := range c.items {
		if i.Text != "" {
			out = append(out, i.Text)
		}
	}
	return out
}

func (c *collector) hasText(sub string) bool {
	for _, s := range c.texts() {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func (c *collector) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

func newHeaders(add map[string]string, forward ...string) *types.Headers {
	h := types.NewHeaders()
	for k, v := range add {
		h.Request.Add[k] = v
	}
	h.Request.Forward = forward
	return h
}

// streamOrEcho is the processor to use for an agent that streams or not, with a reply for either.
func streamOrEcho(streaming bool) processorFunc {
	if streaming {
		return streamProcessor("working on it")
	}
	return echoProcessor
}

// streamProcessorWithFinalState sends a status update without a message, and then ends the task in the given state,
// also without a message.
func streamProcessorWithFinalState(state a2aproto.TaskState) processorFunc {
	return func(ctx context.Context, msg a2aproto.Message, opts taskmanager.ProcessOptions, h taskmanager.TaskHandler) (*taskmanager.MessageProcessingResult, error) {
		taskID, err := h.BuildTask(nil, nil)
		if err != nil {
			return nil, err
		}
		sub, err := h.SubscribeTask(&taskID)
		if err != nil {
			return nil, err
		}
		go func() {
			_ = h.UpdateTaskState(&taskID, a2aproto.TaskStateWorking, nil)
			_ = h.UpdateTaskState(&taskID, state, nil)
			sub.Close()
			_ = h.CleanTask(&taskID)
		}()
		return &taskmanager.MessageProcessingResult{StreamingEvents: sub}, nil
	}
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestFetchAgentCard(t *testing.T) {
	u := newUpstream(t, "card-agent", true, echoProcessor)
	card, err := FetchAgentCard(ctxWithTimeout(t), u.URL, "", nil, nil)
	if err != nil {
		t.Fatalf("FetchAgentCard failed: %v", err)
	}
	if card.Name != "card-agent" || card.URL != u.URL || card.Version != "1.0" || len(card.Skills) != 1 {
		t.Errorf("card = %+v", card)
	}
	if s := card.Capabilities.Streaming; s == nil || !*s {
		t.Errorf("capabilities = %+v, want streaming", card.Capabilities)
	}
	reqs := u.requests()
	if len(reqs) != 1 || reqs[0].Method != http.MethodGet || !strings.HasSuffix(reqs[0].Path, "/agent/card-agent/.well-known/agent.json") {
		t.Errorf("requests = %+v, want one GET of the card", reqs)
	}
}

func TestFetchAgentCardURLForms(t *testing.T) {
	u := newUpstream(t, "url-agent", false, echoProcessor)
	for name, url := range map[string]string{
		"without scheme":         strings.TrimPrefix(u.URL, "http://"),
		"with trailing slash":    u.URL,
		"without trailing slash": strings.TrimSuffix(u.URL, "/"),
		"full card path":         strings.TrimSuffix(u.URL, "/") + "/.well-known/agent.json",
	} {
		t.Run(name, func(t *testing.T) {
			card, err := FetchAgentCard(ctxWithTimeout(t), url, "", nil, nil)
			if err != nil {
				t.Fatalf("FetchAgentCard(%q) failed: %v", url, err)
			}
			if card.Name != "url-agent" {
				t.Errorf("card = %+v", card)
			}
		})
	}
}

func TestFetchAgentCardFallsBackToCallURLs(t *testing.T) {
	u := newUpstream(t, "fallback-agent", false, echoProcessor)
	for name, call := range map[string]*AgentCall{
		"card url":  {CardURL: u.URL},
		"agent url": {AgentURL: u.URL},
	} {
		t.Run(name, func(t *testing.T) {
			card, err := FetchAgentCard(ctxWithTimeout(t), "", "", call, nil)
			if err != nil {
				t.Fatalf("FetchAgentCard failed: %v", err)
			}
			if card.Name != "fallback-agent" {
				t.Errorf("card = %+v", card)
			}
		})
	}
	if _, err := FetchAgentCard(ctxWithTimeout(t), "", "", &AgentCall{}, nil); err == nil {
		t.Error("FetchAgentCard without any URL must fail")
	}
}

func TestFetchAgentCardAuthority(t *testing.T) {
	u := newUpstream(t, "auth-agent", false, echoProcessor)
	if _, err := FetchAgentCard(ctxWithTimeout(t), u.URL, "agents.example.com", nil, nil); err != nil {
		t.Fatalf("FetchAgentCard failed: %v", err)
	}
	if got := u.requests()[0].Host; got != "agents.example.com" {
		t.Errorf("host of the card request = %q, want the authority", got)
	}
	// The authority of the call is the one to use when none is given.
	if _, err := FetchAgentCard(ctxWithTimeout(t), u.URL, "", &AgentCall{Authority: "call.example.com"}, nil); err != nil {
		t.Fatalf("FetchAgentCard failed: %v", err)
	}
	if got := u.requests()[1].Host; got != "call.example.com" {
		t.Errorf("host of the card request = %q, want the authority of the call", got)
	}
}

func TestFetchAgentCardForwardsHeaders(t *testing.T) {
	u := newUpstream(t, "fwd-agent", false, echoProcessor)
	call := &AgentCall{Headers: newHeaders(nil, "X-Trace", "X-Absent")}
	in := http.Header{"X-Trace": {"trace-1"}, "X-Other": {"other"}}
	if _, err := FetchAgentCard(ctxWithTimeout(t), u.URL, "", call, in); err != nil {
		t.Fatal(err)
	}
	h := u.requests()[0].Headers
	if h.Get("X-Trace") != "trace-1" || h.Get("X-Other") != "" {
		t.Errorf("headers = %v, want only the forward header", h)
	}
	// Nothing is forwarded for a call without forward headers.
	if _, err := FetchAgentCard(ctxWithTimeout(t), u.URL, "", &AgentCall{}, in); err != nil {
		t.Fatal(err)
	}
	if got := u.requests()[1].Headers.Get("X-Trace"); got != "" {
		t.Errorf("X-Trace = %q, want nothing forwarded", got)
	}
}

func TestFetchAgentCardFailures(t *testing.T) {
	t.Run("status", func(t *testing.T) {
		u := newUpstream(t, "fail-agent", false, echoProcessor)
		u.cardStatus.Store(http.StatusBadGateway)
		_, err := FetchAgentCard(ctxWithTimeout(t), u.URL, "", nil, nil)
		if err == nil || !strings.Contains(err.Error(), "502") {
			t.Errorf("error = %v, want the status", err)
		}
	})
	t.Run("not a card", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "this is not json") }))
		t.Cleanup(srv.Close)
		setup(t)
		_, err := FetchAgentCard(ctxWithTimeout(t), srv.URL, "", nil, nil)
		if err == nil || !strings.Contains(err.Error(), "failed to parse agent card") || !strings.Contains(err.Error(), "this is not json") {
			t.Errorf("error = %v, want the parse failure with the body", err)
		}
	})
	t.Run("unreachable", func(t *testing.T) {
		setup(t)
		srv := httptest.NewServer(http.NotFoundHandler())
		url := srv.URL
		srv.Close()
		_, err := FetchAgentCard(ctxWithTimeout(t), url, "", nil, nil)
		if err == nil || !strings.Contains(err.Error(), "failed to fetch agent card") {
			t.Errorf("error = %v, want a failure to fetch", err)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		setup(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		u := newUpstream(t, "cancel-agent", false, echoProcessor)
		if _, err := FetchAgentCard(ctx, u.URL, "", nil, nil); err == nil {
			t.Error("FetchAgentCard with a cancelled context must fail")
		}
	})
}

func TestNewA2AClient(t *testing.T) {
	setup(t)
	c := NewA2AClient(9, "client-1", false, false, "", 0)
	if c.ID != "client-1" || c.port != 9 || c.httpClient == nil {
		t.Fatalf("client = %+v", c)
	}
	if c.httpClient.Timeout != time.Hour {
		t.Errorf("default request timeout = %v, want 1h", c.httpClient.Timeout)
	}
	if c := NewA2AClient(9, "client-2", false, false, "", 3*time.Second); c.httpClient.Timeout != 3*time.Second {
		t.Errorf("request timeout = %v, want 3s", c.httpClient.Timeout)
	}
}

func TestAgentCallPrepare(t *testing.T) {
	c := &AgentCall{}
	c.Prepare()
	if c.RequestTimeoutD != 10*time.Hour {
		t.Errorf("default timeout = %v, want 10h", c.RequestTimeoutD)
	}
	c = &AgentCall{RequestTimeout: "250ms"}
	c.Prepare()
	if c.RequestTimeoutD != 250*time.Millisecond {
		t.Errorf("timeout = %v, want 250ms", c.RequestTimeoutD)
	}
	c = &AgentCall{RequestTimeout: "garbage"}
	c.Prepare()
	if c.RequestTimeoutD != 0 {
		t.Errorf("timeout = %v, want 0 for one that doesn't parse", c.RequestTimeoutD)
	}
}

func TestAgentCallNonNil(t *testing.T) {
	c := &AgentCall{}
	c.NonNil()
	if c.Args == nil || c.Headers == nil || c.Headers.Request == nil || c.Headers.Response == nil || c.Headers.Request.Add == nil {
		t.Errorf("call = %+v, want everything to be set", c)
	}
	args := map[string]any{"k": 1}
	c = &AgentCall{Args: args, Headers: &types.Headers{}}
	c.NonNil()
	if c.Args["k"] != 1 || c.Headers.Request == nil {
		t.Errorf("call = %+v, want existing values kept and the missing ones set", c)
	}
}

func TestAgentCallCloneWithUpdate(t *testing.T) {
	orig := &AgentCall{Name: "orig", AgentURL: "u1", Authority: "a1", Message: "m1", Args: map[string]any{"a": 1}, ForcedStatus: 5}
	same := orig.CloneWithUpdate("", "", "", "", nil)
	if same == orig || same.Name != "orig" || same.AgentURL != "u1" || same.Authority != "a1" || same.Message != "m1" || same.Args["a"] != 1 || same.ForcedStatus != 5 {
		t.Errorf("clone = %+v, want a copy", same)
	}
	updated := orig.CloneWithUpdate("n2", "u2", "a2", "m2", map[string]any{"b": 2})
	if updated.Name != "n2" || updated.AgentURL != "u2" || updated.Authority != "a2" || updated.Message != "m2" || updated.Args["b"] != 2 {
		t.Errorf("clone = %+v, want the updates", updated)
	}
	if orig.Name != "orig" || orig.AgentURL != "u1" || orig.Args["a"] != 1 {
		t.Errorf("original = %+v, want it unchanged", orig)
	}
}

func TestAgentCallJSON(t *testing.T) {
	c := &AgentCall{}
	body := `{"name":"n","agentURL":"u","cardURL":"c","message":"m","args":{"k":"v"},"requestCount":3,"concurrent":2,"stream":true,
	  "headers":{"request":{"add":{"x":"y"},"forward":["a"]}},"retriableStatusCodes":[502,503],"resultOnly":true}`
	if err := json.Unmarshal([]byte(body), c); err != nil {
		t.Fatal(err)
	}
	if c.Name != "n" || c.AgentURL != "u" || c.CardURL != "c" || c.RequestCount != 3 || c.Concurrent != 2 || !c.Stream || !c.ResultOnly ||
		c.Headers.Request.Add["x"] != "y" || len(c.RetriableStatusCodes) != 2 || c.Args["k"] != "v" {
		t.Errorf("call = %+v", c)
	}
	b, _ := json.Marshal(&AgentCall{RequestTimeoutD: time.Second})
	if strings.Contains(string(b), "RequestTimeoutD") {
		t.Errorf("the prepared timeout is reported: %s", b)
	}
}

// streamDataProcessor streams a status update with a data part, and then completes the task.
func streamDataProcessor(data map[string]any) processorFunc {
	return func(ctx context.Context, msg a2aproto.Message, opts taskmanager.ProcessOptions, h taskmanager.TaskHandler) (*taskmanager.MessageProcessingResult, error) {
		taskID, err := h.BuildTask(nil, nil)
		if err != nil {
			return nil, err
		}
		sub, err := h.SubscribeTask(&taskID)
		if err != nil {
			return nil, err
		}
		go func() {
			m := a2aproto.NewMessage(a2aproto.MessageRoleAgent, []a2aproto.Part{a2aproto.NewDataPart(data)})
			_ = h.UpdateTaskState(&taskID, a2aproto.TaskStateWorking, &m)
			done := a2aproto.NewMessage(a2aproto.MessageRoleAgent, []a2aproto.Part{a2aproto.NewTextPart("data sent")})
			_ = h.UpdateTaskState(&taskID, a2aproto.TaskStateCompleted, &done)
			sub.Close()
			_ = h.CleanTask(&taskID)
		}()
		return &taskmanager.MessageProcessingResult{StreamingEvents: sub}, nil
	}
}
