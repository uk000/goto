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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	aicommon "goto/pkg/ai/common"
	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/server/middleware"
	"goto/pkg/types"
	"goto/pkg/util"

	"github.com/gorilla/mux"
	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	stubOnce   sync.Once
	routesOnce sync.Once
	portSeq    atomic.Int32
)

// stubGlobalFuncs fills the global.Funcs hooks that the server wires up at startup, and that the MCP client, the
// timeline and the request store call on every request.
func stubGlobalFuncs() {
	global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsListenerPresent = func(int) bool { return false }
	global.Funcs.IsListenerOpen = func(int) bool { return false }
	global.Funcs.GetListenerID = func(int) string { return "" }
	global.Funcs.GetListenerLabel = func(*http.Request) string { return "" }
	global.Funcs.GetListenerLabelForPort = func(port int) string { return fmt.Sprintf("L%d", port) }
	util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
	global.Self.HostLabel = "test-host"
}

// setup stubs the global hooks, and restores the process wide client payload state when the test ends, since payloads
// are kept in package level maps.
func setup(t *testing.T) {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
	lock.Lock()
	savedPayloads := NamedClientPayloads
	savedDefault := DefaultClientPayload
	NamedClientPayloads = map[string]*MCPNamedClientPayload{}
	DefaultClientPayload = nil
	lock.Unlock()
	savedVerbose := global.Flags.VerboseMCP
	t.Cleanup(func() {
		lock.Lock()
		NamedClientPayloads = savedPayloads
		DefaultClientPayload = savedDefault
		lock.Unlock()
		global.Flags.VerboseMCP = savedVerbose
	})
}

func nextPort() int { return int(41000 + portSeq.Add(1)) }

// apiRouter returns the router that serves the admin APIs. The routes are mounted on the process wide root router of
// /mcpapi, so they are registered only once.
func apiRouter() *mux.Router {
	routesOnce.Do(func() {
		stubOnce.Do(stubGlobalFuncs)
		setRoutes(nil)
	})
	return middleware.RootRouters["/mcpapi"]
}

// apiCall serves a request through the router the way the server does, with a request store attached to the request.
func apiCall(t *testing.T, method, target, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, nextPort()))
	_, req, _ = util.WithRequestStore(req, nil)
	rr := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		apiRouter().ServeHTTP(rr, req)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatalf("%s %s did not complete in time", method, target)
	}
	return rr
}

// ----------------------------------------------------------------------------------------------------------------
// MCP test server
// ----------------------------------------------------------------------------------------------------------------

// toolCallRecord is what the test MCP server saw of one tool call.
type toolCallRecord struct {
	Tool   string
	Args   map[string]any
	Header http.Header
}

const observedHostHeader = "X-Observed-Host"

// testMCPServer is an MCP server on a local listener that serves both the streamable HTTP (/mcp) and the SSE (/sse)
// transports, and records what it receives.
type testMCPServer struct {
	srv      *httptest.Server
	mcp      *gomcp.Server
	mu       sync.Mutex
	hosts    []string
	requests []*http.Request
	calls    []toolCallRecord
	// postHeaders has the headers of the POST that carried a tool call, by the request ID of the call. The SSE
	// transport doesn't tell tools what the headers of their requests were, as the streamable transport does.
	postHeaders map[string]http.Header
	toolHits    atomic.Int32
	// The sampling and elicitation tools put what the client answered here.
	sampled   chan *gomcp.CreateMessageResult
	elicited  chan *gomcp.ElicitResult
	rootsSeen chan []string
}

func (s *testMCPServer) streamURL() string { return s.srv.URL + "/mcp" }
func (s *testMCPServer) sseURL() string    { return s.srv.URL + "/sse" }
func (s *testMCPServer) hostPort() string  { return s.srv.Listener.Addr().String() }

func (s *testMCPServer) recordedHosts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.hosts...)
}

func (s *testMCPServer) recordedCalls() []toolCallRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]toolCallRecord{}, s.calls...)
}

func (s *testMCPServer) recordedRequests() []*http.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*http.Request{}, s.requests...)
}

func textResult(text string) *gomcp.CallToolResult {
	return &gomcp.CallToolResult{Content: []gomcp.Content{&gomcp.TextContent{Text: text}}}
}

func newTestMCPServer(t *testing.T) *testMCPServer { return newTestMCPServerWith(t, false, false) }

// newTestMCPServerWith creates the test server, optionally with TLS and with HTTP/2 (which needs TLS).
func newTestMCPServerWith(t *testing.T, useTLS, useH2 bool) *testMCPServer {
	t.Helper()
	s := &testMCPServer{
		postHeaders: map[string]http.Header{},
		sampled:     make(chan *gomcp.CreateMessageResult, 16),
		elicited:    make(chan *gomcp.ElicitResult, 16),
		rootsSeen:   make(chan []string, 16),
	}
	s.mcp = gomcp.NewServer(&gomcp.Implementation{Name: "test-mcp-server", Version: "1.0"}, nil)
	schema := map[string]any{"type": "object"}
	s.mcp.AddTool(&gomcp.Tool{Name: "echo", Description: "echoes the text argument", InputSchema: schema},
		func(ctx context.Context, req *gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
			args := s.record(req, "echo")
			text, _ := args["text"].(string)
			res := textResult("echo:" + text)
			res.StructuredContent = map[string]any{"echoed": text, "metadata": args["metadata"]}
			return res, nil
		})
	s.mcp.AddTool(&gomcp.Tool{Name: "plain", Description: "answers with text only", InputSchema: schema},
		func(ctx context.Context, req *gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
			s.record(req, "plain")
			return textResult("plain-answer"), nil
		})
	s.mcp.AddTool(&gomcp.Tool{Name: "fail", Description: "fails with a protocol error", InputSchema: schema},
		func(ctx context.Context, req *gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
			s.record(req, "fail")
			return nil, errors.New("boom")
		})
	s.mcp.AddTool(&gomcp.Tool{Name: "slow", Description: "blocks until the caller gives up", InputSchema: schema},
		func(ctx context.Context, req *gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
			s.record(req, "slow")
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(10 * time.Second):
				return textResult("late"), nil
			}
		})
	s.mcp.AddTool(&gomcp.Tool{Name: "sample", Description: "asks the client to sample a message", InputSchema: schema},
		func(ctx context.Context, req *gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
			args := s.record(req, "sample")
			system, _ := args["text"].(string)
			res, err := req.Session.CreateMessage(ctx, &gomcp.CreateMessageParams{
				SystemPrompt: system,
				MaxTokens:    10,
				Messages:     []*gomcp.SamplingMessage{{Role: "user", Content: &gomcp.TextContent{Text: "hello"}}},
			})
			if err != nil {
				return nil, err
			}
			s.sampled <- res
			return textResult("sampled"), nil
		})
	s.mcp.AddTool(&gomcp.Tool{Name: "elicit", Description: "asks the client to elicit input", InputSchema: schema},
		func(ctx context.Context, req *gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
			s.record(req, "elicit")
			res, err := req.Session.Elicit(ctx, &gomcp.ElicitParams{
				Message:         "need input",
				RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			})
			if err != nil {
				return nil, err
			}
			s.elicited <- res
			return textResult("elicited:" + res.Action), nil
		})
	s.mcp.AddTool(&gomcp.Tool{Name: "roots", Description: "asks the client for its roots", InputSchema: schema},
		func(ctx context.Context, req *gomcp.CallToolRequest) (*gomcp.CallToolResult, error) {
			s.record(req, "roots")
			res, err := req.Session.ListRoots(ctx, nil)
			if err != nil {
				return nil, err
			}
			uris := []string{}
			for _, r := range res.Roots {
				uris = append(uris, r.URI)
			}
			s.rootsSeen <- uris
			return textResult("roots"), nil
		})
	s.mcp.AddPrompt(&gomcp.Prompt{Name: "greet", Description: "greets"},
		func(ctx context.Context, req *gomcp.GetPromptRequest) (*gomcp.GetPromptResult, error) {
			return &gomcp.GetPromptResult{Messages: []*gomcp.PromptMessage{{Role: "user", Content: &gomcp.TextContent{Text: "hi"}}}}, nil
		})
	s.mcp.AddResource(&gomcp.Resource{Name: "readme", URI: "file:///readme.txt", MIMEType: "text/plain"},
		func(ctx context.Context, req *gomcp.ReadResourceRequest) (*gomcp.ReadResourceResult, error) {
			return &gomcp.ReadResourceResult{Contents: []*gomcp.ResourceContents{{URI: "file:///readme.txt", Text: "readme"}}}, nil
		})
	getServer := func(*http.Request) *gomcp.Server { return s.mcp }
	mux := http.NewServeMux()
	mux.Handle("/mcp", s.recording(gomcp.NewStreamableHTTPHandler(getServer, &gomcp.StreamableHTTPOptions{DisableLocalhostProtection: true})))
	mux.Handle("/sse", s.recording(gomcp.NewSSEHandler(getServer, nil)))
	s.srv = httptest.NewUnstartedServer(mux)
	if useTLS {
		s.srv.EnableHTTP2 = useH2
		s.srv.StartTLS()
	} else {
		s.srv.Start()
	}
	t.Cleanup(func() {
		// Open SSE streams would make Close wait for them.
		s.srv.CloseClientConnections()
		s.srv.Close()
	})
	return s
}

func (s *testMCPServer) recording(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The Host header is not part of the headers of a request, so it's passed to the tools as a header.
		r.Header.Set(observedHostHeader, r.Host)
		var postedRequestID string
		var body []byte
		if r.Method == http.MethodPost && r.Body != nil {
			body, _ = io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(body))
			postedRequestID = toolCallRequestID(body)
		}
		s.mu.Lock()
		s.hosts = append(s.hosts, r.Host)
		s.requests = append(s.requests, r.Clone(context.Background()))
		if postedRequestID != "" {
			s.postHeaders[postedRequestID] = r.Header.Clone()
		}
		s.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

// toolCallRequestID returns the request ID that a call of a tool carries in its metadata, if the body is a JSON-RPC
// request for a tool call.
func toolCallRequestID(body []byte) string {
	msg := struct {
		Method string
		Params struct {
			Arguments struct {
				Metadata map[string]any
			}
		}
	}{}
	if json.Unmarshal(body, &msg) != nil || msg.Method != "tools/call" {
		return ""
	}
	id, _ := msg.Params.Arguments.Metadata[constants.HeaderGotoMCPRequestID].(string)
	return id
}

func (s *testMCPServer) record(req *gomcp.CallToolRequest, tool string) map[string]any {
	s.toolHits.Add(1)
	args := map[string]any{}
	if req.Params != nil && len(req.Params.Arguments) > 0 {
		_ = json.Unmarshal(req.Params.Arguments, &args)
	}
	var header http.Header
	if req.Extra != nil && req.Extra.Header != nil {
		header = req.Extra.Header.Clone()
	}
	s.mu.Lock()
	if md, ok := args["metadata"].(map[string]any); ok && len(header) == 0 {
		if rid, _ := md[constants.HeaderGotoMCPRequestID].(string); rid != "" && s.postHeaders[rid] != nil {
			header = s.postHeaders[rid].Clone()
		}
	}
	s.calls = append(s.calls, toolCallRecord{Tool: tool, Args: args, Header: header})
	s.mu.Unlock()
	return args
}

// ----------------------------------------------------------------------------------------------------------------
// Client helpers
// ----------------------------------------------------------------------------------------------------------------

// newTestClient creates a client the way the admin APIs do, with a request timeout so that no test can hang on a call.
func newTestClient(sse bool) *MCPClient {
	return NewClient(nextPort(), sse, false, false, "TestCaller", "test-listener", "", 20*time.Second, nil, nil, nil)
}

// newToolCall builds a prepared tool call that has no events, which keeps the timelines small.
func newToolCall(tool, url string) *ToolCall {
	tc := &ToolCall{Tool: tool, URL: url, NoEvents: true, RequestTimeout: "20s"}
	tc.Prepare()
	return tc
}

func invokeTool(t *testing.T, sse bool, tc *ToolCall) (*MCPResult, error) {
	t.Helper()
	client := newTestClient(sse)
	session := client.CreateSession(context.Background(), "", "test/call", tc, http.Header{})
	return session.CallTool(nil)
}

func newPayload(contents ...string) *MCPClientPayload {
	return &MCPClientPayload{Contents: contents}
}

func mustDelay(t *testing.T, min, max time.Duration) *types.Delay {
	t.Helper()
	d := types.NewDelay(min, max, 1)
	if d == nil {
		t.Fatalf("NewDelay(%s, %s) = nil", min, max)
	}
	return d
}

// ----------------------------------------------------------------------------------------------------------------
// Client payload registry
// ----------------------------------------------------------------------------------------------------------------

func TestAddPayloadKinds(t *testing.T) {
	setup(t)
	for _, c := range []struct{ kind, content string }{{"elicit", "e"}, {"sample", "s"}, {"other-kind", "o"}} {
		if err := AddPayload("kinds", c.kind, []byte(`{"contents":["`+c.content+`"]}`)); err != nil {
			t.Fatalf("AddPayload(%s) failed: %v", c.kind, err)
		}
	}
	np := getNamedClientPayload("kinds")
	if np == nil || np.ElicitPayload == nil || np.SamplePayload == nil {
		t.Fatalf("payload = %+v, want both kinds", np)
	}
	if got := np.ElicitPayload.Contents; len(got) != 1 || got[0] != "e" {
		t.Errorf("elicit contents = %v, want [e]", got)
	}
	// Every kind but elicit is a sample payload, and a later one replaces the earlier.
	if got := np.SamplePayload.Contents; len(got) != 1 || got[0] != "o" {
		t.Errorf("sample contents = %v, want [o]", got)
	}
}

func TestAddPayloadInvalid(t *testing.T) {
	setup(t)
	for _, body := range []string{`{`, `{"contents":"not a list"}`, `[1,2]`, ``} {
		if err := AddPayload("invalid", "sample", []byte(body)); err == nil {
			t.Errorf("AddPayload(%q) = nil, want an error", body)
		}
	}
	if np := getNamedClientPayload("invalid"); np != nil {
		t.Errorf("a payload was registered by the failed calls: %+v", np)
	}
	if len(NamedClientPayloads) != 0 {
		t.Errorf("registry = %v, want it empty", NamedClientPayloads)
	}
}

func TestAddPayloadWithDelay(t *testing.T) {
	setup(t)
	if err := AddPayload("delayed", "sample", []byte(`{"contents":["x"],"delay":{"min":"1ms","max":"2ms","count":2}}`)); err != nil {
		t.Fatal(err)
	}
	d := getNamedClientPayload("delayed").SamplePayload.Delay
	if d == nil || d.Min == nil || d.Max == nil || d.Min.Duration != time.Millisecond || d.Max.Duration != 2*time.Millisecond || d.Count != 2 {
		t.Errorf("delay = %+v", d)
	}
}

func TestDefaultClientPayload(t *testing.T) {
	setup(t)
	if np := getNamedClientPayload("anything"); np != nil {
		t.Fatalf("payload of an empty registry = %+v, want none", np)
	}
	if err := AddPayload("first", "sample", []byte(`{"contents":["one"]}`)); err != nil {
		t.Fatal(err)
	}
	// With no payload of the requested name, the default one is used. It's the payload that was created last.
	if np := getNamedClientPayload("unknown"); np == nil || np.SamplePayload.Contents[0] != "one" {
		t.Errorf("default payload = %+v, want the first one", np)
	}
	if err := AddPayload("second", "sample", []byte(`{"contents":["two"]}`)); err != nil {
		t.Fatal(err)
	}
	if np := getNamedClientPayload("unknown"); np == nil || np.SamplePayload.Contents[0] != "two" {
		t.Errorf("default payload = %+v, want the second one", np)
	}
	if np := getNamedClientPayload("first"); np == nil || np.SamplePayload.Contents[0] != "one" {
		t.Errorf("payload of first = %+v", np)
	}
	// Updating the payload that is not the default one leaves the default alone, and the default follows the updates of
	// its own payload.
	if err := AddPayload("first", "elicit", []byte(`{"contents":["one-e"]}`)); err != nil {
		t.Fatal(err)
	}
	if np := getNamedClientPayload("unknown"); np == nil || np.SamplePayload.Contents[0] != "two" {
		t.Errorf("default payload = %+v, want it to stay the second one", np)
	}
	if err := AddPayload("second", "elicit", []byte(`{"contents":["two-e"]}`)); err != nil {
		t.Fatal(err)
	}
	np := getNamedClientPayload("unknown")
	if np == nil || np.SamplePayload == nil || np.ElicitPayload == nil || np.ElicitPayload.Contents[0] != "two-e" {
		t.Errorf("default payload = %+v, want the second one with its elicit payload", np)
	}
}

func TestSetRoots(t *testing.T) {
	setup(t)
	if err := SetRoots("roots", []byte(`[{"uri":"file:///a","name":"A"},{"uri":"file:///b"}]`)); err != nil {
		t.Fatal(err)
	}
	roots := getNamedClientPayload("roots").Roots
	if len(roots) != 2 || roots[0].URI != "file:///a" || roots[0].Name != "A" || roots[1].URI != "file:///b" {
		t.Errorf("roots = %+v", roots)
	}
	for _, body := range []string{`{`, `{"uri":"file:///a"}`, `"x"`} {
		if err := SetRoots("roots", []byte(body)); err == nil {
			t.Errorf("SetRoots(%q) = nil, want an error", body)
		}
	}
	if got := getNamedClientPayload("roots").Roots; len(got) != 2 {
		t.Errorf("roots changed by the failed calls: %+v", got)
	}
	if err := SetRoots("roots", []byte(`[{"uri":"file:///c"}]`)); err != nil {
		t.Fatal(err)
	}
	if got := getNamedClientPayload("roots").Roots; len(got) != 1 || got[0].URI != "file:///c" {
		t.Errorf("roots = %+v, want them replaced", got)
	}
	if err := SetRoots("roots", []byte(`[]`)); err != nil {
		t.Fatal(err)
	}
	if got := getNamedClientPayload("roots").Roots; len(got) != 0 {
		t.Errorf("roots = %+v, want them cleared", got)
	}
}

// ----------------------------------------------------------------------------------------------------------------
// ToolCall
// ----------------------------------------------------------------------------------------------------------------

func TestParseToolCall(t *testing.T) {
	t.Run("json", func(t *testing.T) {
		tc, err := ParseToolCall(io.NopCloser(strings.NewReader(
			`{"tool":"\"quoted\"","url":"http://h:1/mcp","authority":"a.test","delay":"10ms-20ms","requestTimeout":"3s","args":{"text":"t","count":2}}`)))
		if err != nil {
			t.Fatal(err)
		}
		if tc.Tool != "quoted" {
			t.Errorf("tool = %q, want the quotes trimmed", tc.Tool)
		}
		if tc.Server != "a.test" {
			t.Errorf("server = %q, want it to default to the authority", tc.Server)
		}
		if tc.RequestTimeoutD != 3*time.Second {
			t.Errorf("request timeout = %s, want 3s", tc.RequestTimeoutD)
		}
		if tc.delayD == nil || tc.delayD.Min.Duration != 10*time.Millisecond || tc.delayD.Max.Duration != 20*time.Millisecond {
			t.Errorf("delay = %+v", tc.delayD)
		}
		if tc.Args == nil || tc.Args.Text != "t" || tc.Args.Count != 2 || tc.Args.Metadata == nil || tc.Args.Headers == nil {
			t.Errorf("args = %+v, want the given ones, with the unset parts initialized", tc.Args)
		}
	})
	t.Run("yaml", func(t *testing.T) {
		tc, err := ParseToolCall(io.NopCloser(strings.NewReader("tool: 'single'\nurl: http://h:1/mcp\nserver: s1\nforceSSE: true\nargs:\n  text: yaml-text\n")))
		if err != nil {
			t.Fatal(err)
		}
		if tc.Tool != "single" || tc.Server != "s1" || !tc.ForceSSE || tc.Args.Text != "yaml-text" {
			t.Errorf("tool call = %+v", tc)
		}
	})
	t.Run("defaults", func(t *testing.T) {
		tc, err := ParseToolCall(io.NopCloser(strings.NewReader(`{"tool":"t","url":"u"}`)))
		if err != nil {
			t.Fatal(err)
		}
		if tc.RequestTimeoutD != 10*time.Hour || tc.Server != "" || tc.delayD != nil {
			t.Errorf("tool call = %+v", tc)
		}
	})
	t.Run("invalid", func(t *testing.T) {
		for _, body := range []string{`{"url":"u"}`, `{"tool":"t"}`, `{}`, `{"tool":"","url":""}`} {
			tc, err := ParseToolCall(io.NopCloser(strings.NewReader(body)))
			if err == nil || tc != nil {
				t.Errorf("ParseToolCall(%s) = %+v, %v, want an error and no tool call", body, tc, err)
			}
		}
		if _, err := ParseToolCall(io.NopCloser(strings.NewReader(`{`))); err == nil {
			t.Error("a broken payload was accepted")
		}
	})
}

func TestToolCallUpdateAndClone(t *testing.T) {
	headers := types.NewHeaders()
	headers.Request.Add["X-Base"] = "base"
	tc := &ToolCall{Tool: "t", URL: "u", Server: "s", Authority: "a", Delay: "1ms-2ms", Headers: headers}
	tc.Args = aicommon.NewCallArgs()
	tc.Args.Text = "orig"
	tc.Args.AddMetadata("k", "v")

	extra := types.NewHeaders()
	extra.Request.Add["X-Extra"] = "extra"
	newArgs := aicommon.NewCallArgs()
	newArgs.Text = "new"
	newArgs.Count = 3
	clone := tc.UpdateAndClone("t2", "u2", "s2", "a2", "5ms-6ms", extra, newArgs)
	if clone.Tool != "t2" || clone.URL != "u2" || clone.Server != "s2" || clone.Authority != "a2" || clone.Delay != "5ms-6ms" {
		t.Errorf("clone = %+v, want the updated fields", clone)
	}
	if clone.Args.Text != "new" || clone.Args.Count != 3 {
		t.Errorf("clone args = %+v", clone.Args)
	}
	if clone.Headers.Request.Add["X-Base"] != "base" || clone.Headers.Request.Add["X-Extra"] != "extra" {
		t.Errorf("clone headers = %+v, want the union of both", clone.Headers.Request.Add)
	}
	// The original is left alone.
	if tc.Tool != "t" || tc.URL != "u" || tc.Args.Text != "orig" || tc.Args.Count != 0 || len(tc.Headers.Request.Add) != 1 {
		t.Errorf("original changed: %+v, args %+v", tc, tc.Args)
	}
	// Empty values keep the original ones.
	same := tc.UpdateAndClone("", "", "", "", "", nil, nil)
	if same.Tool != "t" || same.URL != "u" || same.Server != "s" || same.Authority != "a" || same.Delay != "1ms-2ms" || same.Args.Text != "orig" {
		t.Errorf("clone without updates = %+v, args %+v", same, same.Args)
	}
	if same == tc || same.Args == tc.Args {
		t.Error("the clone shares its tool call or args with the original")
	}
	// A tool call without args gets some when the update has them.
	bare := &ToolCall{Tool: "t", URL: "u"}
	if got := bare.UpdateAndClone("", "", "", "", "", nil, newArgs); got.Args == nil || got.Args.Text != "new" {
		t.Errorf("clone of a tool call without args = %+v", got.Args)
	}
}

// ----------------------------------------------------------------------------------------------------------------
// Client and sessions
// ----------------------------------------------------------------------------------------------------------------

// safeNewClient creates a client, and fails the test instead of crashing the run when the construction panics.
func safeNewClient(t *testing.T, port int, sse, h2, tls bool, callerId, listener, authority string, timeout time.Duration) (client *MCPClient) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("NewClient(sse=%t, h2=%t, tls=%t) panicked: %v", sse, h2, tls, r)
		}
	}()
	return NewClient(port, sse, h2, tls, callerId, listener, authority, timeout, nil, nil, nil)
}

func TestNewClient(t *testing.T) {
	setup(t)
	for _, c := range []struct {
		name         string
		sse, h2, tls bool
		wantName     string
	}{
		{"http", false, false, false, "GotoMCP[L%d]"},
		{"sse", true, false, false, "GotoMCP[L%d][sse]"},
		{"tls", false, false, true, "GotoMCP[L%d]"},
		{"h2c", false, true, false, "GotoMCP[L%d]"},
		{"h2 over tls", false, true, true, "GotoMCP[L%d]"},
	} {
		t.Run(c.name, func(t *testing.T) {
			port := nextPort()
			client := safeNewClient(t, port, c.sse, c.h2, c.tls, "caller-1", "listener-1", "auth.test", 7*time.Second)
			if client.Port != port || client.SSE != c.sse || client.TLS != c.tls || client.CallerId != "caller-1" || client.Listener != "listener-1" {
				t.Errorf("client = %+v", client)
			}
			if want := fmt.Sprintf(c.wantName, port); client.Name != want {
				t.Errorf("name = %q, want %q", client.Name, want)
			}
			if client.httpClient == nil || client.httpClient.Timeout != 7*time.Second {
				t.Errorf("http client = %+v, want a client with the request timeout", client.httpClient)
			}
			if client.ActiveSessions == nil || len(client.ActiveSessions) != 0 {
				t.Errorf("active sessions = %v", client.ActiveSessions)
			}
		})
	}
}

func TestNewClientDefaults(t *testing.T) {
	setup(t)
	a := NewClient(nextPort(), false, false, false, "c", "l", "", 0, nil, nil, nil)
	b := NewClient(nextPort(), false, false, false, "c", "l", "", 0, nil, nil, nil)
	if a.httpClient.Timeout != time.Hour {
		t.Errorf("default request timeout = %s, want 1h", a.httpClient.Timeout)
	}
	if a.ID == "" || b.ID == "" || a.ID == b.ID {
		t.Errorf("client IDs = %q and %q, want distinct ones", a.ID, b.ID)
	}
}

func TestCreateSessionURL(t *testing.T) {
	setup(t)
	sessionParam := constants.HeaderGotoMCPSessionID
	cases := []struct {
		name    string
		tls     bool
		url     string
		tcURL   string
		wantFmt string
	}{
		{"scheme added", false, "127.0.0.1:9/mcp", "", "http://127.0.0.1:9/mcp?" + sessionParam + "=%s"},
		{"https when client is TLS", true, "127.0.0.1:9/mcp", "", "https://127.0.0.1:9/mcp?" + sessionParam + "=%s"},
		{"http kept", true, "http://h:9/mcp", "", "http://h:9/mcp?" + sessionParam + "=%s"},
		{"https kept", false, "https://h:9/mcp", "", "https://h:9/mcp?" + sessionParam + "=%s"},
		{"existing query", false, "http://h:9/mcp?a=b", "", "http://h:9/mcp?a=b&" + sessionParam + "=%s"},
		{"url of the tool call", false, "", "h:9/from-tc", "http://h:9/from-tc?" + sessionParam + "=%s"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			client := NewClient(nextPort(), false, false, c.tls, "caller", "listener", "", time.Second, nil, nil, nil)
			tc := newToolCall("echo", c.tcURL)
			s := client.CreateSession(context.Background(), c.url, "op", tc, nil)
			if want := fmt.Sprintf(c.wantFmt, s.ID); s.URL != want {
				t.Errorf("session URL = %q, want %q", s.URL, want)
			}
			if s.Operation != "op" || s.CallerId != "caller" || s.Listener != "listener" || s.Name != client.Name || s.SSE {
				t.Errorf("session = %+v", s)
			}
		})
	}
}

// finishesWithin runs fn in a goroutine, and fails the test when it doesn't finish in time, which is how a deadlock
// shows up. fn must not use t.
func finishesWithin(t *testing.T, d time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s did not finish within %s, it is deadlocked", what, d)
	}
}

func TestSessionsOfAClient(t *testing.T) {
	setup(t)
	client := newTestClient(false)
	tc := newToolCall("echo", "http://127.0.0.1:1/mcp")
	var s1, s2, s3 *MCPSession
	var got1, got2, gotMissing, afterRemove1, afterRemove2, afterClose, got3 *MCPSession
	finishesWithin(t, 10*time.Second, "the lifecycle of sessions", func() {
		s1 = client.CreateSession(context.Background(), "", "op", tc, nil)
		s2 = client.CreateSession(context.Background(), "", "op", tc, nil)
		got1, got2, gotMissing = client.GetSession(s1.ID), client.GetSession(s2.ID), client.GetSession("missing")
		client.RemoveSession(s1.ID)
		afterRemove1, afterRemove2 = client.GetSession(s1.ID), client.GetSession(s2.ID)
		client.OnConnClose()
		afterClose = client.GetSession(s2.ID)
		// The client is still usable after that.
		s3 = client.CreateSession(context.Background(), "", "op", tc, nil)
		got3 = client.GetSession(s3.ID)
	})
	if s1.ID == s2.ID || !strings.HasPrefix(s1.ID, client.ID+".") || !strings.HasPrefix(s2.ID, client.ID+".") {
		t.Errorf("session IDs = %q and %q for client %q", s1.ID, s2.ID, client.ID)
	}
	if got1 != s1 || got2 != s2 || gotMissing != nil {
		t.Error("sessions are not found by their IDs")
	}
	if afterRemove1 != nil || afterRemove2 != s2 {
		t.Error("RemoveSession didn't remove just the given session")
	}
	if afterClose != nil {
		t.Error("OnConnClose left a session behind")
	}
	if got3 != s3 {
		t.Error("a session created after OnConnClose is not found")
	}
}

func TestNewSessionTakesAToolCallsHeaders(t *testing.T) {
	setup(t)
	client := newTestClient(false)
	tc := newToolCall("echo", "http://127.0.0.1:1/mcp")
	tc.NoEvents, tc.ResultOnly = true, true
	tc.Headers = types.NewHeaders()
	tc.Headers.Request.Add["X-A"] = "a"
	s := client.CreateSession(context.Background(), "", "op", tc, nil)
	if s.outHeaders == nil || s.outHeaders.Request.Add["X-A"] != "a" {
		t.Fatalf("session headers = %+v", s.outHeaders)
	}
	// The session works with its own copy of the headers.
	s.outHeaders.Request.Add["X-B"] = "b"
	if _, ok := tc.Headers.Request.Add["X-B"]; ok {
		t.Error("the session shares its headers with the tool call")
	}
	if !s.Timeline.NoEvents || !s.Timeline.ResultOnly {
		t.Errorf("timeline flags = noEvents %t resultOnly %t, want both from the tool call", s.Timeline.NoEvents, s.Timeline.ResultOnly)
	}
	// A session without a tool call (listing) has no headers.
	s2 := client.CreateSession(context.Background(), "http://127.0.0.1:1/mcp", "list", nil, nil)
	if s2.outHeaders != nil {
		t.Errorf("headers of a session without a tool call = %+v", s2.outHeaders)
	}
}

// ----------------------------------------------------------------------------------------------------------------
// Outbound and inbound interception
// ----------------------------------------------------------------------------------------------------------------

func newInterceptedRequest(t *testing.T, s *MCPSession, requestID string) *http.Request {
	t.Helper()
	req := httptest.NewRequest("POST", s.URL, nil)
	if requestID != "" {
		req = req.WithContext(context.WithValue(req.Context(), constants.HeaderGotoMCPRequestID, requestID))
	}
	return req
}

func TestInterceptRequest(t *testing.T) {
	setup(t)
	tc := newToolCall("echo", "http://example.test/mcp")
	tc.Server = "backend-1"
	tc.Headers = types.NewHeaders()
	tc.Headers.Request.Add["X-Added"] = "yes"
	tc.Headers.Request.Add["Host"] = "override.test"
	tc.Headers.Request.Remove = []string{"X-Removed"}
	tc.Headers.Request.Forward = []string{"X-Forwarded", "X-Not-Given"}
	client := newTestClient(false)
	s := client.CreateSession(context.Background(), "", "op", tc, http.Header{"X-Forwarded": {"fwd-value"}})
	req := newInterceptedRequest(t, s, "rid-1")
	req.Header.Set("X-Removed", "1")
	client.InterceptRequest(req)
	if got := req.Header.Get("X-Added"); got != "yes" {
		t.Errorf("X-Added = %q", got)
	}
	if got := req.Header.Get("X-Removed"); got != "" {
		t.Errorf("X-Removed = %q, want it removed", got)
	}
	if got := req.Header.Get("X-Forwarded"); got != "fwd-value" {
		t.Errorf("X-Forwarded = %q, want the value of the inbound request", got)
	}
	if got := req.Header.Get("X-Not-Given"); got != "" {
		t.Errorf("X-Not-Given = %q, want none", got)
	}
	for h, want := range map[string]string{
		constants.HeaderGotoMCPServer:    "backend-1",
		constants.HeaderGotoMCPTool:      "echo",
		constants.HeaderGotoMCPRequestID: "rid-1",
	} {
		if got := req.Header.Get(h); got != want {
			t.Errorf("%s = %q, want %q", h, got, want)
		}
	}
	if req.Host != "override.test" {
		t.Errorf("request host = %q, want the one of the Host header", req.Host)
	}
}

func TestInterceptRequestOutsideOfASession(t *testing.T) {
	setup(t)
	client := newTestClient(false)
	for _, target := range []string{"http://example.test/mcp", "http://example.test/mcp?" + constants.HeaderGotoMCPSessionID + "=", "http://example.test/mcp?" + constants.HeaderGotoMCPSessionID + "=nope"} {
		req := httptest.NewRequest("POST", target, nil)
		origHost := req.Host
		client.InterceptRequest(req)
		if len(req.Header) != 0 || req.Host != origHost {
			t.Errorf("request to %s was changed: host %q headers %v", target, req.Host, req.Header)
		}
	}
	global.Flags.VerboseMCP = true
	client.InterceptRequest(httptest.NewRequest("POST", "http://example.test/mcp", nil))
}

func TestInterceptResponse(t *testing.T) {
	setup(t)
	tc := newToolCall("echo", "http://example.test/mcp")
	client := newTestClient(false)
	s := client.CreateSession(context.Background(), "", "op", tc, nil)
	tctx := s.newToolCallContext(nil)
	tctx.prepare()
	tctx.reportInitiateToolCall()
	s.ongoingCalls["rid-1"] = tctx
	req := newInterceptedRequest(t, s, "")
	req.Header.Set(constants.HeaderGotoMCPRequestID, "rid-1")
	resp := &http.Response{
		StatusCode: 202,
		Header: http.Header{
			constants.HeaderViaGoto:            {"peer-1", "peer-2(A2A)"},
			constants.HeaderGotoUpstreamStatus: {`{"down":503}`},
			"X-Plain":                          {"p"},
		},
		Body:    io.NopCloser(strings.NewReader("body")),
		Request: req,
	}
	client.InterceptResponse(resp)
	if _, found := s.ongoingCalls["rid-1"]; found {
		t.Error("the call is still ongoing after its response")
	}
	if got := tctx.result.LastResponseStatus; got != 202 {
		t.Errorf("last response status = %d, want 202", got)
	}
	call := tctx.result.CallResults["rid-1"]
	if call == nil || call.ResponseStatus != 202 || call.RequestHeaders.Get(constants.HeaderGotoMCPRequestID) != "rid-1" {
		t.Fatalf("call result = %+v", call)
	}
	if tctx.result.RemoteGotos["peer-1"] != true || tctx.result.RemoteGotos["peer-2(A2A)"] != true {
		t.Errorf("remote gotos = %v", tctx.result.RemoteGotos)
	}
	// The trailers are processed when the body is done.
	body, err := io.ReadAll(resp.Body)
	if err != nil || string(body) != "body" {
		t.Fatalf("body = %q, %v", body, err)
	}
	via := append([]string{}, tctx.result.LastResponseHeaders[constants.HeaderViaGoto]...)
	sort.Strings(via)
	if want := []string{"peer-1(MCP)", "peer-2(A2A)"}; !reflect.DeepEqual(via, want) {
		t.Errorf("Via-Goto = %v, want %v", via, want)
	}
	if got := tctx.result.UpstreamStatuses["echo"]; got != 202 {
		t.Errorf("upstream status of the tool = %v, want 202", got)
	}
	if got, ok := tctx.result.UpstreamStatuses["-"].(map[string]any); !ok || got["down"] != float64(503) {
		t.Errorf("upstream statuses = %+v, want the ones of the response under -", tctx.result.UpstreamStatuses)
	}
}

func TestInterceptResponseWithResponseHeaderConfig(t *testing.T) {
	setup(t)
	tc := newToolCall("echo", "http://example.test/mcp")
	tc.Headers = types.NewHeaders()
	tc.Headers.Response.Add["X-Added-To-Response"] = "yes"
	tc.Headers.Response.Remove = []string{"X-Drop"}
	client := newTestClient(false)
	s := client.CreateSession(context.Background(), "", "op", tc, nil)
	req := newInterceptedRequest(t, s, "")
	resp := &http.Response{Header: http.Header{"X-Drop": {"1"}}, Body: io.NopCloser(strings.NewReader("")), Request: req}
	client.InterceptResponse(resp)
	if got := resp.Header.Get("X-Added-To-Response"); got != "yes" {
		t.Errorf("X-Added-To-Response = %q, want the header of the response config", got)
	}
	if got := resp.Header.Get("X-Drop"); got != "" {
		t.Errorf("X-Drop = %q, want it removed by the response config", got)
	}
}

func TestInterceptResponseOutsideOfASession(t *testing.T) {
	setup(t)
	client := newTestClient(false)
	req := httptest.NewRequest("POST", "http://example.test/mcp", nil)
	resp := &http.Response{Header: http.Header{"X-A": {"1"}}, Body: io.NopCloser(strings.NewReader("b")), Request: req}
	client.InterceptResponse(resp)
	if len(resp.Header) != 1 {
		t.Errorf("response headers = %v, want them untouched", resp.Header)
	}
}

func TestSendingMiddlewareAddsClientMetadata(t *testing.T) {
	setup(t)
	client := newTestClient(false)
	s := client.CreateSession(context.Background(), "", "op", newToolCall("echo", "http://127.0.0.1:1/mcp"), nil)
	args := aicommon.NewCallArgs()
	req := &gomcp.ClientRequest[*gomcp.CallToolParams]{Params: &gomcp.CallToolParams{Name: "echo", Arguments: args}}
	called := false
	next := func(ctx context.Context, method string, r gomcp.Request) (gomcp.Result, error) {
		called = true
		return nil, nil
	}
	if _, err := s.SendingMiddleware(next)(context.Background(), "tools/call", req); err != nil || !called {
		t.Fatalf("middleware did not call the next handler: called %t, err %v", called, err)
	}
	if got := args.Metadata["goto-client"]; got != "test-host" {
		t.Errorf("goto-client metadata = %q", got)
	}
	if s.currentRequest != "tools/call" {
		t.Errorf("current request = %q", s.currentRequest)
	}
	// Other methods are passed through.
	called = false
	if _, err := s.ReceivingMiddleware(next)(context.Background(), "tools/list", &gomcp.ClientRequest[*gomcp.ListToolsParams]{Params: &gomcp.ListToolsParams{}}); err != nil || !called {
		t.Fatalf("receiving middleware did not call the next handler: called %t, err %v", called, err)
	}
	if s.currentRequest != "tools/list" {
		t.Errorf("current request = %q", s.currentRequest)
	}
}

// ----------------------------------------------------------------------------------------------------------------
// Calling tools of MCP servers
// ----------------------------------------------------------------------------------------------------------------

type transportCase struct {
	name string
	sse  bool
	url  func(*testMCPServer) string
}

var transportCases = []transportCase{
	{"streamable", false, func(s *testMCPServer) string { return s.streamURL() }},
	{"sse", true, func(s *testMCPServer) string { return s.sseURL() }},
}

// closedServerURL returns the URL of a server that was just closed, to see how calls fail.
func closedServerURL(t *testing.T) (streamable, sse string) {
	t.Helper()
	srv := newTestMCPServer(t)
	streamable, sse = srv.streamURL(), srv.sseURL()
	srv.srv.CloseClientConnections()
	srv.srv.Close()
	return
}

// onlyCall returns the one result that a call has, and its request ID.
func onlyCall(t *testing.T, res *MCPResult) (string, *MCPCallResult) {
	t.Helper()
	if len(res.CallResults) != 1 {
		t.Fatalf("call results = %d, want 1: %+v", len(res.CallResults), res.CallResults)
	}
	for id, cr := range res.CallResults {
		return id, cr
	}
	return "", nil
}

func contentText(t *testing.T, cr *MCPCallResult) string {
	t.Helper()
	if len(cr.Content) != 1 {
		t.Fatalf("content = %+v, want one item", cr.Content)
	}
	tc, ok := cr.Content[0].(*gomcp.TextContent)
	if !ok {
		t.Fatalf("content = %T, want text", cr.Content[0])
	}
	return tc.Text
}

func TestCallToolOverTransports(t *testing.T) {
	setup(t)
	for _, tr := range transportCases {
		t.Run(tr.name, func(t *testing.T) {
			srv := newTestMCPServer(t)
			tc := newToolCall("echo", tr.url(srv))
			tc.Args.Text = "hello"
			tc.Args.AddMetadata("k", "v")
			res, err := invokeTool(t, tr.sse, tc)
			if err != nil {
				t.Fatalf("CallTool failed: %v", err)
			}
			if res.Tool != "echo" || !strings.HasPrefix(res.ID, "[echo]@"+tr.url(srv)) || !strings.HasPrefix(res.Server, tr.url(srv)) {
				t.Errorf("result id/tool/server = %q/%q/%q", res.ID, res.Tool, res.Server)
			}
			rid, cr := onlyCall(t, res)
			if cr.RequestID != rid {
				t.Errorf("call request ID = %q, want %q", cr.RequestID, rid)
			}
			if got := contentText(t, cr); got != "echo:hello" {
				t.Errorf("content = %q", got)
			}
			data, _ := cr.Data.(map[string]any)
			if data["echoed"] != "hello" {
				t.Errorf("structured data = %+v", cr.Data)
			}
			if res.LastResponseStatus != http.StatusOK && res.LastResponseStatus != http.StatusAccepted {
				t.Errorf("last response status = %d, want 200 or 202", res.LastResponseStatus)
			}
			calls := srv.recordedCalls()
			if len(calls) != 1 || calls[0].Tool != "echo" || calls[0].Args["text"] != "hello" {
				t.Fatalf("server saw %+v", calls)
			}
			md, _ := calls[0].Args["metadata"].(map[string]any)
			for k, want := range map[string]string{
				"k":                              "v",
				constants.HeaderGotoMCPRequestID: rid,
				constants.HeaderGotoMCPCallerID:  "TestCaller",
				"goto-client":                    "test-host",
			} {
				if md[k] != want {
					t.Errorf("metadata[%s] = %v, want %q (all: %v)", k, md[k], want, md)
				}
			}
			if sid, _ := md[constants.HeaderGotoMCPSessionID].(string); sid == "" || !strings.HasPrefix(rid, sid+".") {
				t.Errorf("metadata session ID = %q, request ID = %q, want the request ID to be derived from the session ID", sid, rid)
			}
			if got := calls[0].Header.Get(constants.HeaderGotoMCPTool); got != "echo" {
				t.Errorf("%s header = %q, want echo", constants.HeaderGotoMCPTool, got)
			}
			if got := calls[0].Header.Get(constants.HeaderGotoMCPRequestID); got != rid {
				t.Errorf("%s header = %q, want %q", constants.HeaderGotoMCPRequestID, got, rid)
			}
		})
	}
}

func TestCallToolSendsConfiguredHeaders(t *testing.T) {
	setup(t)
	for _, tr := range transportCases {
		t.Run(tr.name, func(t *testing.T) {
			srv := newTestMCPServer(t)
			tc := newToolCall("echo", tr.url(srv))
			tc.Authority = "svc.example.test"
			tc.Headers = types.NewHeaders()
			tc.Headers.Request.Add["X-Custom"] = "custom"
			tc.Headers.Request.Forward = []string{"X-Fwd"}
			client := newTestClient(tr.sse)
			session := client.CreateSession(context.Background(), "", "test/call", tc, http.Header{"X-Fwd": {"fwd-value"}})
			if _, err := session.CallTool(nil); err != nil {
				t.Fatal(err)
			}
			calls := srv.recordedCalls()
			if len(calls) != 1 {
				t.Fatalf("server saw %d calls, want 1", len(calls))
			}
			h := calls[0].Header
			if got := h.Get("X-Custom"); got != "custom" {
				t.Errorf("X-Custom = %q, want custom", got)
			}
			if got := h.Get("X-Fwd"); got != "fwd-value" {
				t.Errorf("X-Fwd = %q, want the value of the inbound request", got)
			}
			if got := h.Get(observedHostHeader); got != "svc.example.test" {
				t.Errorf("host of the tool call = %q, want the authority", got)
			}
			if got := h.Get("User-Agent"); got != "TestCaller" {
				t.Errorf("User-Agent = %q, want the caller ID", got)
			}
		})
	}
}

func TestCallToolRawKeepsTheDefaultHeaders(t *testing.T) {
	setup(t)
	srv := newTestMCPServer(t)
	tc := newToolCall("plain", srv.streamURL())
	tc.Raw = true
	tc.Authority = "svc.example.test"
	if _, err := invokeTool(t, false, tc); err != nil {
		t.Fatal(err)
	}
	calls := srv.recordedCalls()
	if len(calls) != 1 {
		t.Fatalf("server saw %d calls, want 1", len(calls))
	}
	if got := calls[0].Header.Get(observedHostHeader); got != srv.hostPort() {
		t.Errorf("host = %q, want %q, the authority is not applied to raw calls", got, srv.hostPort())
	}
	if got := calls[0].Header.Get("User-Agent"); got == "TestCaller" {
		t.Errorf("User-Agent = %q, the caller ID is not applied to raw calls", got)
	}
}

func TestCallToolArgs(t *testing.T) {
	setup(t)
	srv := newTestMCPServer(t)
	tc := newToolCall("echo", srv.streamURL())
	tc.Args.Text = "from-tool-call"
	tc.Delay = "1ms-2ms"
	session := newTestClient(false).CreateSession(context.Background(), "", "test/call", tc, nil)
	if _, err := session.CallTool(nil); err != nil {
		t.Fatal(err)
	}
	explicit := aicommon.NewCallArgs()
	explicit.Text = "explicit"
	explicit.Count = 7
	session = newTestClient(false).CreateSession(context.Background(), "", "test/call", tc, nil)
	if _, err := session.CallTool(explicit); err != nil {
		t.Fatal(err)
	}
	calls := srv.recordedCalls()
	if len(calls) != 2 {
		t.Fatalf("server saw %d calls, want 2", len(calls))
	}
	if calls[0].Args["text"] != "from-tool-call" || calls[0].Args["delay"] != "1ms-2ms" {
		t.Errorf("args of the call without explicit args = %+v, want the ones of the tool call, with its delay", calls[0].Args)
	}
	if calls[1].Args["text"] != "explicit" || calls[1].Args["count"] != float64(7) || calls[1].Args["delay"] != "1ms-2ms" {
		t.Errorf("args of the call with explicit args = %+v", calls[1].Args)
	}
}

func TestCallToolResultFlags(t *testing.T) {
	setup(t)
	srv := newTestMCPServer(t)
	t.Run("result only has no content", func(t *testing.T) {
		tc := newToolCall("echo", srv.streamURL())
		tc.ResultOnly = true
		res, err := invokeTool(t, false, tc)
		if err != nil {
			t.Fatal(err)
		}
		_, cr := onlyCall(t, res)
		if len(cr.Content) != 0 {
			t.Errorf("content = %+v, want none for a result only call", cr.Content)
		}
	})
	t.Run("no call details", func(t *testing.T) {
		tc := newToolCall("echo", srv.streamURL())
		tc.NoCallDetails = true
		res, err := invokeTool(t, false, tc)
		if err != nil {
			t.Fatal(err)
		}
		if res.CallResults != nil {
			t.Errorf("call results = %+v, want none", res.CallResults)
		}
	})
	t.Run("events", func(t *testing.T) {
		tc := newToolCall("echo", srv.streamURL())
		tc.NoEvents = false
		client := newTestClient(false)
		session := client.CreateSession(context.Background(), "", "test/call", tc, nil)
		res, err := session.CallTool(nil)
		if err != nil {
			t.Fatal(err)
		}
		texts := []string{}
		for _, e := range res.Timeline.Events {
			texts = append(texts, e.Text)
		}
		all := strings.Join(texts, "\n")
		for _, want := range []string{"Initiating Tool Call [echo]", "Calling Tool [echo]", "Tool [echo] called successfully"} {
			if !strings.Contains(all, want) {
				t.Errorf("events do not include %q:\n%s", want, all)
			}
		}
	})
}

func TestCallToolErrors(t *testing.T) {
	setup(t)
	closedStream, closedSSE := closedServerURL(t)
	for _, tr := range transportCases {
		t.Run(tr.name, func(t *testing.T) {
			srv := newTestMCPServer(t)
			t.Run("tool failure", func(t *testing.T) {
				res, err := invokeTool(t, tr.sse, newToolCall("fail", tr.url(srv)))
				if err == nil || !strings.Contains(err.Error(), "boom") {
					t.Errorf("error = %v, want the failure of the tool", err)
				}
				if res == nil {
					t.Error("result = nil, want the result of the failed call")
				}
			})
			t.Run("unknown tool", func(t *testing.T) {
				if _, err := invokeTool(t, tr.sse, newToolCall("no-such-tool", tr.url(srv))); err == nil {
					t.Error("error = nil, want an error for an unknown tool")
				}
			})
			t.Run("request timeout", func(t *testing.T) {
				tc := newToolCall("slow", tr.url(srv))
				tc.RequestTimeout = "200ms"
				tc.Prepare()
				start := time.Now()
				_, err := invokeTool(t, tr.sse, tc)
				if err == nil {
					t.Fatal("error = nil, want the call to time out")
				}
				if took := time.Since(start); took > 8*time.Second {
					t.Errorf("call took %s, want it to give up after its 200ms timeout", took)
				}
			})
			t.Run("server down", func(t *testing.T) {
				url := closedStream
				if tr.sse {
					url = closedSSE
				}
				res, err := invokeTool(t, tr.sse, newToolCall("echo", url))
				if err == nil || res != nil {
					t.Errorf("result, error = %+v, %v, want only an error", res, err)
				}
			})
		})
	}
}

func TestCallToolDelays(t *testing.T) {
	setup(t)
	srv := newTestMCPServer(t)
	t.Run("initial delay", func(t *testing.T) {
		tc := newToolCall("plain", srv.streamURL())
		tc.InitialDelay = "150ms-160ms"
		start := time.Now()
		if _, err := invokeTool(t, false, tc); err != nil {
			t.Fatal(err)
		}
		if took := time.Since(start); took < 150*time.Millisecond {
			t.Errorf("call took %s, want it to wait for the initial delay of 150ms", took)
		}
	})
	t.Run("delay between rounds", func(t *testing.T) {
		before := srv.toolHits.Load()
		tc := newToolCall("plain", srv.streamURL())
		tc.RequestCount = 3
		tc.RequestDelay = "100ms-110ms"
		start := time.Now()
		if _, err := invokeTool(t, false, tc); err != nil {
			t.Fatal(err)
		}
		if took := time.Since(start); took < 200*time.Millisecond {
			t.Errorf("call took %s, want it to wait twice for the delay of 100ms between the 3 requests", took)
		}
		if got := srv.toolHits.Load() - before; got != 3 {
			t.Errorf("server saw %d calls, want 3", got)
		}
	})
}

func TestSessionListing(t *testing.T) {
	setup(t)
	for _, tr := range transportCases {
		t.Run(tr.name, func(t *testing.T) {
			srv := newTestMCPServer(t)
			client := newTestClient(tr.sse)
			session := client.CreateSession(context.Background(), tr.url(srv), "list", nil, nil)
			tools, err := session.ListTools()
			if err != nil || len(tools.Tools) != 7 {
				t.Fatalf("ListTools = %d tools, %v, want 7", len(tools.Tools), err)
			}
			prompts, err := session.ListPrompts()
			if err != nil || len(prompts.Prompts) != 1 || prompts.Prompts[0].Name != "greet" {
				t.Fatalf("ListPrompts = %+v, %v", prompts, err)
			}
			resources, err := session.ListResources()
			if err != nil || len(resources.Resources) != 1 || resources.Resources[0].URI != "file:///readme.txt" {
				t.Fatalf("ListResources = %+v, %v", resources, err)
			}
			if client.GetSession(session.ID) != nil {
				t.Error("the session is still active after its operations are done")
			}
		})
	}
	t.Run("server down", func(t *testing.T) {
		closedStream, _ := closedServerURL(t)
		session := newTestClient(false).CreateSession(context.Background(), closedStream, "list", nil, nil)
		if _, err := session.ListTools(); err == nil {
			t.Error("ListTools of a server that is down = nil error")
		}
		if _, err := session.ListPrompts(); err == nil {
			t.Error("ListPrompts of a server that is down = nil error")
		}
		if _, err := session.ListResources(); err == nil {
			t.Error("ListResources of a server that is down = nil error")
		}
	})
}

func TestCallToolOverTLS(t *testing.T) {
	setup(t)
	for _, c := range []struct {
		name string
		h2   bool
	}{{"https", false}, {"h2", true}} {
		t.Run(c.name, func(t *testing.T) {
			srv := newTestMCPServerWith(t, true, c.h2)
			tc := newToolCall("echo", srv.streamURL())
			tc.Args.Text = "secure"
			client := safeNewClient(t, nextPort(), false, c.h2, true, "TestCaller", "test-listener", "", 20*time.Second)
			session := client.CreateSession(context.Background(), "", "test/call", tc, http.Header{})
			res, err := session.CallTool(nil)
			if err != nil {
				t.Fatalf("CallTool over TLS failed: %v", err)
			}
			if _, cr := onlyCall(t, res); contentText(t, cr) != "echo:secure" {
				t.Errorf("content = %+v", cr.Content)
			}
			if c.h2 {
				for _, r := range srv.recordedRequests() {
					if r.Proto != "HTTP/2.0" {
						t.Errorf("request protocol = %s, want HTTP/2.0", r.Proto)
						break
					}
				}
			}
		})
	}
}

// ----------------------------------------------------------------------------------------------------------------
// Server initiated requests: sampling, elicitation and roots
// ----------------------------------------------------------------------------------------------------------------

func receive[T any](t *testing.T, ch chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}

func TestSamplingRequestsAreAnsweredFromThePayload(t *testing.T) {
	for _, tr := range transportCases {
		t.Run(tr.name, func(t *testing.T) {
			setup(t)
			if err := AddPayload("sampler", "sample", []byte(`{"contents":["the-answer"],"models":["m-1"],"roles":["assistant"]}`)); err != nil {
				t.Fatal(err)
			}
			srv := newTestMCPServer(t)
			tc := newToolCall("sample", tr.url(srv))
			tc.Args.Text = "please sample"
			if _, err := invokeTool(t, tr.sse, tc); err != nil {
				t.Fatal(err)
			}
			res := receive(t, srv.sampled, "the sampling result")
			if res.Model != "m-1" || res.Role != "assistant" || res.StopReason != "please sample" {
				t.Errorf("sampling result = model %q role %q stop %q", res.Model, res.Role, res.StopReason)
			}
			if text, ok := res.Content.(*gomcp.TextContent); !ok || !strings.Contains(text.Text, "the-answer") {
				t.Errorf("sampling content = %+v, want the content of the payload", res.Content)
			}
		})
	}
}

func TestElicitationRequestsAreAnsweredFromThePayload(t *testing.T) {
	for _, tr := range transportCases {
		t.Run(tr.name, func(t *testing.T) {
			setup(t)
			if err := AddPayload("eliciter", "elicit", []byte(`{"contents":["why"],"actions":["decline"]}`)); err != nil {
				t.Fatal(err)
			}
			srv := newTestMCPServer(t)
			if _, err := invokeTool(t, tr.sse, newToolCall("elicit", tr.url(srv))); err != nil {
				t.Fatal(err)
			}
			if res := receive(t, srv.elicited, "the elicitation result"); res.Action != "decline" {
				t.Errorf("elicitation action = %q, want decline", res.Action)
			}
		})
	}
}

func TestRootsAreProvidedToTheServer(t *testing.T) {
	for _, tr := range transportCases {
		t.Run(tr.name, func(t *testing.T) {
			setup(t)
			if err := SetRoots("rooter", []byte(`[{"uri":"file:///one"},{"uri":"file:///two","name":"Two"}]`)); err != nil {
				t.Fatal(err)
			}
			srv := newTestMCPServer(t)
			if _, err := invokeTool(t, tr.sse, newToolCall("roots", tr.url(srv))); err != nil {
				t.Fatal(err)
			}
			if got := receive(t, srv.rootsSeen, "the roots"); !reflect.DeepEqual(got, []string{"file:///one", "file:///two"}) {
				t.Errorf("roots seen by the server = %v", got)
			}
		})
	}
}
