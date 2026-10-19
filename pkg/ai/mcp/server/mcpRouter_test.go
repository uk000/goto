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

package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/global"
	mcpproxy "goto/pkg/proxy/mcp"
	"goto/pkg/util"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// endpoint serves the MCP handler of the package over a real HTTP server, the way the server of goto does, for the
// requests that arrive on the port.
type endpoint struct {
	t    *testing.T
	port int
	ts   *httptest.Server
	hc   *http.Client
}

func newEndpoint(t *testing.T, port int) *endpoint {
	t.Helper()
	return newEndpointWith(t, port, nil)
}

// newEndpointWith is newEndpoint for requests that are prepared, as the middleware of the server does, before they are
// handled.
func newEndpointWith(t *testing.T, port int, prepare func(*http.Request, *util.RequestStore)) *endpoint {
	t.Helper()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(context.WithValue(r.Context(), util.CurrentPortKey, port))
		_, r, _ = util.WithRequestStore(r, w)
		if prepare != nil {
			prepare(r, util.GetRequestStore(r))
		}
		MCPHandler().ServeHTTP(w, r)
	})
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return &endpoint{t: t, port: port, ts: ts, hc: &http.Client{Timeout: 20 * time.Second}}
}

type rawResponse struct {
	code   int
	header http.Header
	body   string
}

func (e *endpoint) do(method, path, body string, headers map[string]string) rawResponse {
	e.t.Helper()
	req, err := http.NewRequest(method, e.ts.URL+path, strings.NewReader(body))
	if err != nil {
		e.t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := e.hc.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return rawResponse{code: resp.StatusCode, header: resp.Header, body: string(b)}
}

func (e *endpoint) post(path, body string, headers map[string]string) rawResponse {
	e.t.Helper()
	return e.do("POST", path, body, headers)
}

func toolsCallBody(id int, tool, args string) string {
	if args == "" {
		args = "{}"
	}
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, id, tool, args)
}

// callTool posts a tools/call request. Headers are sent as given, so that it is the session that a stateful server
// requires.
func (e *endpoint) callTool(path, tool, args string, headers map[string]string) rawResponse {
	e.t.Helper()
	return e.post(path, toolsCallBody(1, tool, args), headers)
}

const (
	initializeBody  = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"raw","version":"1"}}}`
	initializedBody = `{"jsonrpc":"2.0","method":"notifications/initialized"}`
)

// openSession initializes a session of a stateful server, and returns the headers to use for the calls of it.
func (e *endpoint) openSession(path string) map[string]string {
	e.t.Helper()
	resp := e.post(path, initializeBody, nil)
	if resp.code != http.StatusOK {
		e.t.Fatalf("initialize = %d %q", resp.code, resp.body)
	}
	id := resp.header.Get(HeaderMCPSessionID)
	if id == "" {
		e.t.Fatalf("initialize gave no session ID, headers: %v", resp.header)
	}
	headers := map[string]string{HeaderMCPSessionID: id, "Mcp-Protocol-Version": "2025-06-18"}
	if r := e.post(path, initializedBody, headers); r.code/100 != 2 {
		e.t.Fatalf("initialized = %d %q", r.code, r.body)
	}
	return headers
}

// rpcResult returns the JSON-RPC message of a response, which is either JSON, or the data of the events of a stream.
func rpcResult(t *testing.T, body string) map[string]any {
	t.Helper()
	text := strings.TrimSpace(body)
	if !strings.HasPrefix(text, "{") {
		sc := bufio.NewScanner(strings.NewReader(body))
		text = ""
		for sc.Scan() {
			if line := sc.Text(); strings.HasPrefix(line, "data:") {
				text = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			}
		}
	}
	m := map[string]any{}
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatalf("response %q is not a JSON-RPC message: %v", body, err)
	}
	return m
}

func expectHeader(t *testing.T, h http.Header, name, want string) {
	t.Helper()
	if got := h.Get(name); got != want {
		t.Errorf("header %s = %q, want %q", name, got, want)
	}
}

// ---- finding the server of a request ----

func TestGetServerAndToolFindsTheServerByURIAndName(t *testing.T) {
	port := isolate(t)
	a := addServer(t, port, "find-a", true)
	b := addServer(t, port, "find-b", true)
	addTools(t, port, "find-b", "["+echoToolJSON+"]")

	for _, tt := range []struct {
		name, uri string
		server    *MCPServer
		tool      string
	}{
		{"exact URI", a.URI, a, ""},
		{"URI of a server", b.URI, b, ""},
		{"tool in the URI", b.URI + "/echo", b, "echo"},
		{"tool that doesn't exist", b.URI + "/missing", b, ""},
		{"URI with a prefix of the port", "/port=" + fmt.Sprint(port) + a.URI, a, ""},
	} {
		server, tool := getServerAndTool(newRequest("POST", tt.uri, port, ""))
		if server != tt.server {
			t.Errorf("%s: server = %v, want %v", tt.name, server, tt.server)
		}
		if (tt.tool == "") != (tool == nil) || tool != nil && tool.Name != tt.tool {
			t.Errorf("%s: tool = %v, want %q", tt.name, tool, tt.tool)
		}
	}
}

func TestGetServerAndToolFallsBackToTheDefaultServerOfThePort(t *testing.T) {
	port := isolate(t)
	addServer(t, port, "dflt-a", true)
	b := addServer(t, port, "dflt-b", true)
	// Not a URI of any server, and not the name of one, so it is served by the server that was added last.
	if server, _ := getServerAndTool(newRequest("POST", "/mcp/unknown", port, "")); server != b {
		t.Errorf("server = %v, want the default server of the port", server)
	}
	// A request that is not for MCP is not served by any server.
	if server, _ := getServerAndTool(newRequest("POST", "/not-mcp", port, "")); server != nil {
		t.Errorf("server of a request that is not for MCP = %v", server)
	}
}

func TestGetServerAndToolFallsBackToTheStatelessDefaultServerWhenThePortHasNoServers(t *testing.T) {
	port := isolate(t)
	saved := global.Self.JSONRPCPort
	global.Self.JSONRPCPort = port + 500
	t.Cleanup(func() { global.Self.JSONRPCPort = saved })
	InitDefaultServer()
	if server, _ := getServerAndTool(newRequest("POST", "/mcp/anything", port, "")); server != DefaultStatelessServer {
		t.Errorf("server = %v, want the stateless default server", server)
	}
}

func TestGetServerAndToolRoutesADisabledServerToTheStatelessDefaultServer(t *testing.T) {
	port := isolate(t)
	saved := global.Self.JSONRPCPort
	global.Self.JSONRPCPort = port + 500
	t.Cleanup(func() { global.Self.JSONRPCPort = saved })
	InitDefaultServer()
	s := addServer(t, port, "off-a", true)
	GetPortMCPServers(port).Stop("off-a")
	if server, _ := getServerAndTool(newRequest("POST", s.URI, port, "")); server != DefaultStatelessServer {
		t.Errorf("server = %v, want the stateless default server", server)
	}
	GetPortMCPServers(port).Start("off-a")
	if server, _ := getServerAndTool(newRequest("POST", s.URI, port, "")); server != s {
		t.Errorf("server after starting it = %v", server)
	}
}

func TestGetServerAndToolFindsServerByRegexURI(t *testing.T) {
	port := isolate(t)
	GetPortMCPServers(port).AddMCPServer(&MCPServerPayload{Port: port, Name: "re-a", Version: "1", Enabled: true,
		URIRegex: `^/mcp/re-a-\d+$`})
	server, _ := getServerAndTool(newRequest("POST", "/mcp/re-a-77", port, ""))
	if server == nil || server.Name != "re-a" {
		t.Errorf("server = %v", server)
	}
}

// ---- the handlers ----

func TestMCPHandlerLeavesRequestsThatAreNotForMCPToTheHTTPHandler(t *testing.T) {
	port := isolate(t)
	addServer(t, port, "plain-a", true)
	e := newEndpoint(t, port)
	resp := e.do("GET", "/some/path", "", nil)
	if resp.code != http.StatusTeapot {
		t.Errorf("status = %d, want the status of the HTTP handler", resp.code)
	}
}

func TestMCPHandlerServesToolCallsOfAStatelessServer(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "stateless-a", true)
	addTools(t, port, "stateless-a", "["+echoToolJSON+"]")
	e := newEndpoint(t, port)

	resp := e.callTool(s.URI, "echo", `{"text":"hello stateless"}`, nil)
	if resp.code != http.StatusOK {
		t.Fatalf("status = %d, body %q", resp.code, resp.body)
	}
	expectHeader(t, resp.header, constants.HeaderGotoMCPServer, s.ID)
	if !strings.Contains(resp.body, "hello stateless") {
		t.Errorf("body = %q, want the echo", resp.body)
	}
	if msg := rpcResult(t, resp.body); msg["error"] != nil || msg["result"] == nil {
		t.Errorf("message = %v", msg)
	}
}

func TestMCPHandlerAddsTheToolOfTheURIToTheResponse(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "tooluri-a", true)
	addTools(t, port, "tooluri-a", "["+echoToolJSON+"]")
	e := newEndpoint(t, port)
	resp := e.callTool(s.URI+"/echo", "echo", `{"text":"routed"}`, nil)
	if resp.code != http.StatusOK {
		t.Fatalf("status = %d, body %q", resp.code, resp.body)
	}
	expectHeader(t, resp.header, constants.HeaderGotoMCPServer, s.ID)
	if !strings.Contains(resp.body, "routed") {
		t.Errorf("body = %q", resp.body)
	}
}

// A request for the URI of a tool can only call that tool.
func TestMCPHandlerRejectsACallOfAnotherToolThanTheOneOfTheURI(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "mismatch-a", true)
	addTools(t, port, "mismatch-a", "["+echoToolJSON+","+statusToolJSON+"]")
	e := newEndpoint(t, port)
	resp := e.callTool(s.URI+"/echo", "status", `{}`, nil)
	if !strings.Contains(resp.body, "doesn't match tool") {
		t.Errorf("status = %d, body = %q, want the tool to be rejected", resp.code, resp.body)
	}
}

func TestMCPHandlerServesAStatefulServerThroughASession(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "stateful-a", false)
	addTools(t, port, "stateful-a", "["+echoToolJSON+"]")
	e := newEndpoint(t, port)
	session := e.openSession(s.URI)

	resp := e.callTool(s.URI, "echo", `{"text":"hello stateful"}`, session)
	if resp.code != http.StatusOK || !strings.Contains(resp.body, "hello stateful") {
		t.Fatalf("status = %d, body %q", resp.code, resp.body)
	}
	if s.getSessionContext(session[HeaderMCPSessionID]) == nil {
		t.Error("the server doesn't know the session")
	}
	// A stateful server needs the session.
	if resp := e.callTool(s.URI, "echo", `{}`, nil); resp.code == http.StatusOK && !strings.Contains(resp.body, `"error"`) {
		t.Errorf("call without a session = %d %q, want it to fail", resp.code, resp.body)
	}

	del := e.do("DELETE", s.URI, "", session)
	if del.code/100 != 2 {
		t.Errorf("delete = %d %q", del.code, del.body)
	}
	if s.getSessionContext(session[HeaderMCPSessionID]) != nil {
		t.Error("session of the server survived the delete")
	}
	if MCPRequestStoreBySession[session[HeaderMCPSessionID]] != nil {
		t.Error("request store of the session survived the delete")
	}
}

func TestMCPHandlerDeleteWithoutASessionIsNotAnError(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "del-a", false)
	e := newEndpoint(t, port)
	if resp := e.do("DELETE", s.URI, "", nil); resp.code >= 500 {
		t.Errorf("status = %d, body %q", resp.code, resp.body)
	}
}

func TestMCPHandlerStreamsEventsOfASessionOverGET(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "get-stream-a", false)
	e := newEndpoint(t, port)
	session := e.openSession(s.URI)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", e.ts.URL+s.URI, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range session {
		req.Header.Set(k, v)
	}
	type result struct {
		code        int
		contentType string
		err         error
	}
	got := make(chan result, 1)
	go func() {
		resp, err := e.hc.Do(req)
		if err != nil {
			got <- result{err: err}
			return
		}
		got <- result{code: resp.StatusCode, contentType: resp.Header.Get("Content-Type")}
		// Reading to the end waits for the stream to end.
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()
	select {
	case r := <-got:
		if r.err != nil {
			t.Fatalf("GET: %v", r.err)
		}
		if r.code != http.StatusOK || !strings.HasPrefix(r.contentType, "text/event-stream") {
			t.Errorf("GET = %d %q, want an event stream", r.code, r.contentType)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no response to GET")
	}
	// The delete of the session ends the stream, and its request.
	if del := e.do("DELETE", s.URI, "", session); del.code/100 != 2 {
		t.Errorf("delete = %d", del.code)
	}
}

func TestMCPHandlerServesMCPClients(t *testing.T) {
	for _, tt := range []struct {
		name      string
		stateless bool
		sse       bool
	}{
		{"streamable stateless", true, false},
		{"streamable stateful", false, false},
		{"sse", false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			port := isolate(t)
			name := "client-a"
			s := addServer(t, port, name, tt.stateless)
			addTools(t, port, name, "["+echoToolJSON+"]")
			e := newEndpoint(t, port)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			t.Cleanup(cancel)
			client := gomcp.NewClient(&gomcp.Implementation{Name: "test-client", Version: "1.0"}, nil)
			var transport gomcp.Transport = &gomcp.StreamableClientTransport{Endpoint: e.ts.URL + s.URI}
			if tt.sse {
				transport = &gomcp.SSEClientTransport{Endpoint: e.ts.URL + s.SSEURI}
			}
			cs, err := client.Connect(ctx, transport, nil)
			if err != nil {
				t.Fatalf("connect: %v", err)
			}
			t.Cleanup(func() { cs.Close() })

			if info := cs.InitializeResult(); info == nil || info.ServerInfo == nil || info.ServerInfo.Name != name {
				t.Errorf("initialize result = %+v", info)
			}
			tools, err := cs.ListTools(ctx, nil)
			if err != nil {
				t.Fatalf("list tools: %v", err)
			}
			found := map[string]bool{}
			for _, tool := range tools.Tools {
				found[tool.Name] = true
			}
			for _, want := range []string{"echo", "ServerDetails", "ServerPaths", "ListServers", "ListComponents", "AddTool"} {
				if !found[want] {
					t.Errorf("tool %s is not listed, tools: %v", want, found)
				}
			}
			res := invokeTool(t, cs, "echo", map[string]any{"text": "through " + tt.name})
			if res.IsError || !strings.Contains(resultText(res), "through "+tt.name) {
				t.Errorf("result = %+v", res)
			}
		})
	}
}

func TestMCPHandlerServesPromptsResourcesAndCompletions(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "all-kinds", true)
	mustAdmin(t, port, "POST", "/mcpapi/server/all-kinds/prompts/add", `[{"prompt":{"name":"greet","description":"say hi","arguments":[{"name":"who"}]}}]`)
	mustAdmin(t, port, "POST", "/mcpapi/server/all-kinds/resources/add", `[{"resource":{"name":"doc","uri":"file:///doc.txt","mimeType":"text/plain","text":"resource text"}}]`)
	mustAdmin(t, port, "POST", "/mcpapi/server/all-kinds/templates/add", `[{"template":{"name":"tpl","uriTemplate":"file:///{name}.txt","text":"template text"}}]`)
	mustAdmin(t, port, "POST", "/mcpapi/server/all-kinds/payload/prompts/greet", `{"say":"hello"}`)
	mustAdmin(t, port, "POST", "/mcpapi/server/all-kinds/payload/completion?type=ref/prompt", "suggestion-1")
	e := newEndpoint(t, port)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test-client", Version: "1.0"}, nil)
	cs, err := client.Connect(ctx, &gomcp.StreamableClientTransport{Endpoint: e.ts.URL + s.URI}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })

	prompts, err := cs.ListPrompts(ctx, nil)
	if err != nil || len(prompts.Prompts) != 1 || prompts.Prompts[0].Name != "greet" {
		t.Fatalf("prompts = %+v, err = %v", prompts, err)
	}
	prompt, err := cs.GetPrompt(ctx, &gomcp.GetPromptParams{Name: "greet", Arguments: map[string]string{"who": "me"}})
	if err != nil || len(prompt.Messages) != 2 {
		t.Fatalf("prompt = %+v, err = %v", prompt, err)
	}
	if text := prompt.Messages[0].Content.(*gomcp.TextContent).Text; !strings.Contains(text, `"who"`) {
		t.Errorf("first message = %q, want the arguments", text)
	}
	if text := prompt.Messages[1].Content.(*gomcp.TextContent).Text; !strings.Contains(text, "hello") {
		t.Errorf("second message = %q, want the payload", text)
	}

	resources, err := cs.ListResources(ctx, nil)
	if err != nil || len(resources.Resources) != 1 {
		t.Fatalf("resources = %+v, err = %v", resources, err)
	}
	read, err := cs.ReadResource(ctx, &gomcp.ReadResourceParams{URI: "file:///doc.txt"})
	if err != nil || len(read.Contents) != 1 || read.Contents[0].Text != "resource text" {
		t.Errorf("resource = %+v, err = %v", read, err)
	}
	templates, err := cs.ListResourceTemplates(ctx, nil)
	if err != nil || len(templates.ResourceTemplates) != 1 {
		t.Fatalf("templates = %+v, err = %v", templates, err)
	}
	read, err = cs.ReadResource(ctx, &gomcp.ReadResourceParams{URI: "file:///anything.txt"})
	if err != nil || len(read.Contents) != 1 || read.Contents[0].Text != "template text" {
		t.Errorf("template = %+v, err = %v", read, err)
	}

	completion, err := cs.Complete(ctx, &gomcp.CompleteParams{
		Ref:      &gomcp.CompleteReference{Type: "ref/prompt", Name: "greet"},
		Argument: gomcp.CompleteParamsArgument{Name: "who", Value: "m"},
	})
	if err != nil || len(completion.Completion.Values) != 1 || completion.Completion.Values[0] != "suggestion-1" {
		t.Errorf("completion = %+v, err = %v", completion, err)
	}
	completion, err = cs.Complete(ctx, &gomcp.CompleteParams{
		Ref:      &gomcp.CompleteReference{Type: "ref/resource", URI: "file:///x"},
		Argument: gomcp.CompleteParamsArgument{Name: "n", Value: "m"},
	})
	if err != nil || len(completion.Completion.Values) != 1 || completion.Completion.Values[0] != "<No Completion>" {
		t.Errorf("completion without a payload = %+v, err = %v", completion, err)
	}
}

func TestMCPHandlerTracksToolCalls(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "track-a", true)
	addTools(t, port, "track-a", "["+echoToolJSON+"]")
	e := newEndpoint(t, port)
	for i := 0; i < 3; i++ {
		if resp := e.callTool(s.URI, "echo", `{}`, nil); resp.code != http.StatusOK {
			t.Fatalf("call %d = %d %q", i, resp.code, resp.body)
		}
	}
	if got := getPortServerCount(port, "track-a", "echo", ToolCallsByPortServer); got != 3 {
		t.Errorf("tool calls = %d, want 3", got)
	}
	if got := getPortServerCount(port, "track-a", "echo", ToolCallSuccessByPortServer); got != 3 {
		t.Errorf("successful tool calls = %d, want 3", got)
	}
	if got := getPortServerCount(port, "track-a", "echo", ToolCallFailureByPortServer); got != 0 {
		t.Errorf("failed tool calls = %d, want 0", got)
	}
}

// ---- forced status ----

// forcedServer is a stateless server with an echo tool and a second tool, on an endpoint.
type forcedServer struct {
	*endpoint
	server *MCPServer
	name   string
}

func newForcedServer(t *testing.T) *forcedServer {
	t.Helper()
	port := isolate(t)
	name := fmt.Sprintf("forced%d", port)
	s := addServer(t, port, name, true)
	addTools(t, port, name, "["+echoToolJSON+","+statusToolJSON+"]")
	return &forcedServer{endpoint: newEndpoint(t, port), server: s, name: name}
}

func (f *forcedServer) admin(method, target string) {
	f.t.Helper()
	mustAdmin(f.t, f.port, method, "/mcpapi/server/"+f.name+target, "")
}

// call calls the echo tool, and returns the status.
func (f *forcedServer) call(headers map[string]string) rawResponse {
	f.t.Helper()
	return f.callTool(f.server.URI, "echo", `{"text":"forced"}`, headers)
}

func (f *forcedServer) callOther() rawResponse {
	f.t.Helper()
	return f.callTool(f.server.URI, "status", `{}`, nil)
}

func codesOf(rs ...rawResponse) []int {
	codes := make([]int, len(rs))
	for i, r := range rs {
		codes[i] = r.code
	}
	return codes
}

func expectCodes(t *testing.T, got []int, want ...int) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("status codes = %v, want %v", got, want)
	}
}

func TestForcedStatusAppliesToPOSTToolCalls(t *testing.T) {
	f := newForcedServer(t)
	f.admin("POST", "/status/set/503")
	resp := f.call(nil)
	if resp.code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body %q, want 503", resp.code, resp.body)
	}
	expectHeader(t, resp.header, constants.HeaderGotoForcedStatus, "503")
	expectHeader(t, resp.header, constants.HeaderGotoForcedStatusRemaining, "-1")
	if sc := StatusManager.Snapshot()[f.port]; len(sc) != 1 || sc[0].Times != -1 {
		t.Errorf("configs = %s", statusesOf(f.port))
	}
}

func TestForcedStatusIsNotAppliedToRequestsThatAreNotToolCalls(t *testing.T) {
	f := newForcedServer(t)
	f.admin("POST", "/status/set/503")
	resp := f.post(f.server.URI, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, nil)
	if resp.code != http.StatusOK {
		t.Errorf("tools/list = %d %q, want it not to be forced", resp.code, resp.body)
	}
	resp = f.post(f.server.URI, `{"jsonrpc":"2.0","id":2,"method":"ping"}`, nil)
	if resp.code != http.StatusOK {
		t.Errorf("ping = %d %q, want it not to be forced", resp.code, resp.body)
	}
}

// The status is set for POST requests. The ones that are not, such as the requests that open a stream of events, are
// never forced a status.
func TestForcedStatusIsNotAppliedToGETRequests(t *testing.T) {
	f := newForcedServer(t)
	f.admin("POST", "/status/set/503")
	for _, method := range []string{"GET", "PUT"} {
		resp := f.do(method, f.server.URI, toolsCallBody(1, "echo", ""), nil)
		if resp.code == http.StatusServiceUnavailable {
			t.Errorf("%s was forced a status, body %q", method, resp.body)
		}
		if resp.header.Get(constants.HeaderGotoForcedStatus) != "" {
			t.Errorf("%s response has the forced status header %q", method, resp.header)
		}
	}
	// The status is still there for the requests that it is for, and the others didn't use it.
	if resp := f.call(nil); resp.code != http.StatusServiceUnavailable {
		t.Errorf("POST = %d, want 503", resp.code)
	}
}

func TestForcedStatusIsNotAppliedToAGETStreamOfASession(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "get-forced", false)
	e := newEndpoint(t, port)
	session := e.openSession(s.URI)
	mustAdmin(t, port, "POST", "/mcpapi/server/get-forced/status/set/503", "")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", e.ts.URL+s.URI, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range session {
		req.Header.Set(k, v)
	}
	resp, err := e.hc.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if resp.StatusCode != http.StatusOK || resp.Header.Get(constants.HeaderGotoForcedStatus) != "" {
		t.Errorf("GET = %d %v, want the stream to open", resp.StatusCode, resp.Header)
	}
	cancel()
	resp.Body.Close()
	if status, _ := StatusManager.PeekStatusFor(port, s.URI, nil, "POST"); status != 503 {
		t.Errorf("status after the GET = %d, want it kept", status)
	}
}

func TestForcedStatusForALimitedNumberOfCallsThenNormal(t *testing.T) {
	f := newForcedServer(t)
	f.admin("POST", "/status/set/503:2")
	r1, r2, r3, r4 := f.call(nil), f.call(nil), f.call(nil), f.call(nil)
	expectCodes(t, codesOf(r1, r2, r3, r4), 503, 503, 200, 200)
	expectHeader(t, r1.header, constants.HeaderGotoForcedStatusRemaining, "1")
	expectHeader(t, r2.header, constants.HeaderGotoForcedStatusRemaining, "0")
	if r3.header.Get(constants.HeaderGotoForcedStatus) != "" {
		t.Errorf("a call that is not forced has the forced status header: %v", r3.header)
	}
	if !strings.Contains(r3.body, "forced") {
		t.Errorf("the call after the status was used up was not served: %q", r3.body)
	}
}

func TestForcedStatusForUnlimitedCalls(t *testing.T) {
	f := newForcedServer(t)
	f.admin("POST", "/status/set/502")
	var codes []int
	for i := 0; i < 5; i++ {
		codes = append(codes, f.call(nil).code)
	}
	expectCodes(t, codes, 502, 502, 502, 502, 502)
}

func TestForcedStatusByHeader(t *testing.T) {
	t.Run("header present", func(t *testing.T) {
		f := newForcedServer(t)
		f.admin("POST", "/status/set/503/header/x-force")
		with := f.call(map[string]string{"X-Force": "yes"})
		without := f.call(nil)
		expectCodes(t, codesOf(with, without), 503, 200)
	})
	t.Run("header with a value", func(t *testing.T) {
		f := newForcedServer(t)
		f.admin("POST", "/status/set/504/header/x-force=on")
		expectCodes(t, codesOf(
			f.call(map[string]string{"X-Force": "on"}),
			f.call(map[string]string{"X-Force": "off"}),
			f.call(nil)), 504, 200, 200)
	})
	t.Run("header absent", func(t *testing.T) {
		f := newForcedServer(t)
		f.admin("POST", "/status/set/503/header/not/x-skip")
		expectCodes(t, codesOf(
			f.call(map[string]string{"X-Skip": "1"}),
			f.call(nil)), 200, 503)
	})
	t.Run("a header with not in its name", func(t *testing.T) {
		f := newForcedServer(t)
		f.admin("POST", "/status/set/503/header/x-note")
		expectCodes(t, codesOf(
			f.call(map[string]string{"X-Note": "1"}),
			f.call(nil)), 503, 200)
	})
	t.Run("limited by header", func(t *testing.T) {
		f := newForcedServer(t)
		f.admin("POST", "/status/set/503:1/header/x-force")
		h := map[string]string{"X-Force": "1"}
		expectCodes(t, codesOf(f.call(nil), f.call(h), f.call(h), f.call(nil)), 200, 503, 200, 200)
	})
}

func TestForcedStatusPerTool(t *testing.T) {
	f := newForcedServer(t)
	f.admin("POST", "/tool/echo/status/set/502")
	expectCodes(t, codesOf(f.call(nil), f.callOther(), f.call(nil)), 502, 200, 502)
}

func TestForcedStatusOfTheServerBeatsTheOneOfTheTool(t *testing.T) {
	f := newForcedServer(t)
	f.admin("POST", "/status/set/503")
	f.admin("POST", "/tool/echo/status/set/502")
	// Both are for the call, the status of the server is the one it gets. The other tool only has the one of the server.
	expectCodes(t, codesOf(f.call(nil), f.callOther()), 503, 503)
}

// One call uses one count. The status of the tool is looked up only when the server has none for the call, so the
// counts of the tool are not used by the calls that the status of the server takes.
func TestForcedStatusOfAToolCallUsesOnlyOneCount(t *testing.T) {
	f := newForcedServer(t)
	f.admin("POST", "/status/set/503:1")
	f.admin("POST", "/tool/echo/status/set/504:1")
	r1, r2, r3 := f.call(nil), f.call(nil), f.call(nil)
	expectCodes(t, codesOf(r1, r2, r3), 503, 504, 200)
	expectHeader(t, r1.header, constants.HeaderGotoForcedStatusRemaining, "0")
	expectHeader(t, r2.header, constants.HeaderGotoForcedStatusRemaining, "0")

	// The same for a status of the tool that is used up first.
	f2 := newForcedServer(t)
	f2.admin("POST", "/tool/echo/status/set/504:1")
	f2.admin("POST", "/status/set/503:2")
	expectCodes(t, codesOf(f2.call(nil), f2.call(nil), f2.call(nil), f2.call(nil)), 503, 503, 504, 200)
}

func TestForcedStatusCountsAreConsumedOnlyByTheCallsThatAreForced(t *testing.T) {
	f := newForcedServer(t)
	f.admin("POST", "/status/set/503:1/header/x-force")
	// Calls that don't match don't take the count.
	for i := 0; i < 3; i++ {
		if resp := f.call(nil); resp.code != http.StatusOK {
			t.Fatalf("call %d = %d", i, resp.code)
		}
	}
	if status, left := StatusManager.PeekStatusFor(f.port, f.server.URI, map[string][]string{"X-Force": {"1"}}, "POST"); status != 503 || left != 1 {
		t.Errorf("status = (%d, %d), want (503, 1)", status, left)
	}
}

func TestForcedStatusIsClearedByClearStatus(t *testing.T) {
	f := newForcedServer(t)
	f.admin("POST", "/status/set/503")
	if resp := f.call(nil); resp.code != 503 {
		t.Fatalf("status = %d, want 503", resp.code)
	}
	f.admin("POST", "/status/clear")
	expectCodes(t, codesOf(f.call(nil), f.call(nil)), 200, 200)
}

func TestForcedStatusOfAToolIsClearedWithTheServer(t *testing.T) {
	f := newForcedServer(t)
	f.admin("POST", "/tool/echo/status/set/502")
	f.admin("POST", "/status/clear")
	expectCodes(t, codesOf(f.call(nil)), 200)
}

func TestForcedStatusIsClearedForAllServersOfThePort(t *testing.T) {
	f := newForcedServer(t)
	f.admin("POST", "/status/set/503")
	mustAdmin(t, f.port, "POST", "/mcpapi/servers/status/clear", "")
	expectCodes(t, codesOf(f.call(nil)), 200)
}

func TestClearingTheStatusOfAServerLeavesTheStatusOfOthers(t *testing.T) {
	f := newForcedServer(t)
	other := addServer(t, f.port, "other-"+f.name, true)
	f.admin("POST", "/status/set/503")
	mustAdmin(t, f.port, "POST", "/mcpapi/server/other-"+f.name+"/status/set/502", "")
	f.admin("POST", "/status/clear")
	if resp := f.call(nil); resp.code != 200 {
		t.Errorf("cleared server = %d, want 200", resp.code)
	}
	if resp := f.callTool(other.URI, "echo", `{}`, nil); resp.code != 502 {
		t.Errorf("other server = %d, want it to keep its status 502", resp.code)
	}
}

func TestConfiguredStatusIsAppliedToToolCalls(t *testing.T) {
	f := newForcedServer(t)
	mustAdmin(t, f.port, "POST", "/mcpapi/server/"+f.name+"/status/configure",
		`{"statuses":[429],"times":2,"match":{"methods":["POST"]}}`)
	expectCodes(t, codesOf(f.call(nil), f.call(nil), f.call(nil)), 429, 429, 200)
}

// Looking at the status of a server is not a use of it.
func TestReportingTheStatusDoesNotConsumeACount(t *testing.T) {
	f := newForcedServer(t)
	f.admin("POST", "/status/set/503:1")
	for i := 0; i < 3; i++ {
		expectBodyContains(t, mustAdmin(f.t, f.port, "GET", "/mcpapi/server/"+f.name+"/status", ""), "[503] for [1] times")
		mustAdmin(f.t, f.port, "GET", "/mcpapi/servers/status", "")
	}
	expectCodes(t, codesOf(f.call(nil), f.call(nil)), 503, 200)
}

// The tool of the status tool, that is told the status to send, forces it for its call only.
func TestStatusToolForcesTheStatusOfItsCallOnly(t *testing.T) {
	f := newForcedServer(t)
	forced := f.callTool(f.server.URI, "status", `{"status":418}`, nil)
	normal := f.callTool(f.server.URI, "status", `{}`, nil)
	again := f.call(nil)
	expectCodes(t, codesOf(forced, normal, again), 418, 200, 200)
	expectHeader(t, forced.header, constants.HeaderGotoForcedStatus, "418")
}

func TestForcedStatusOfAStatefulServer(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "forced-stateful", false)
	addTools(t, port, "forced-stateful", "["+echoToolJSON+"]")
	e := newEndpoint(t, port)
	session := e.openSession(s.URI)
	mustAdmin(t, port, "POST", "/mcpapi/server/forced-stateful/status/set/503:2", "")
	expectCodes(t, codesOf(
		e.callTool(s.URI, "echo", `{}`, session),
		e.callTool(s.URI, "echo", `{}`, session),
		e.callTool(s.URI, "echo", `{}`, session)), 503, 503, 200)
}

// The calls of many sessions at once use exactly the number of counts that were set.
func TestForcedStatusCountsAreExactUnderConcurrentCalls(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "forced-conc", false)
	addTools(t, port, "forced-conc", "["+echoToolJSON+"]")
	e := newEndpoint(t, port)
	const sessions = 8
	const callsPerSession = 5
	const limit = 12
	sessionHeaders := make([]map[string]string, sessions)
	for i := range sessionHeaders {
		sessionHeaders[i] = e.openSession(s.URI)
	}
	mustAdmin(t, port, "POST", "/mcpapi/server/forced-conc/status/set/503:"+fmt.Sprint(limit), "")

	var reqs []*http.Request
	for _, h := range sessionHeaders {
		for c := 0; c < callsPerSession; c++ {
			req, err := http.NewRequest("POST", e.ts.URL+s.URI, strings.NewReader(toolsCallBody(c+1, "echo", `{}`)))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			for k, v := range h {
				req.Header.Set(k, v)
			}
			reqs = append(reqs, req)
		}
	}
	codes := make(chan int, len(reqs))
	var wg sync.WaitGroup
	for i := 0; i < sessions; i++ {
		wg.Add(1)
		go func(mine []*http.Request) {
			defer wg.Done()
			// The calls of a session are in order.
			for _, req := range mine {
				resp, err := e.hc.Do(req)
				if err != nil {
					codes <- -1
					continue
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				codes <- resp.StatusCode
			}
		}(reqs[i*callsPerSession : (i+1)*callsPerSession])
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("calls did not finish")
	}
	close(codes)
	counts := map[int]int{}
	for c := range codes {
		counts[c]++
	}
	if counts[503] != limit || counts[200] != sessions*callsPerSession-limit || len(counts) != 2 {
		t.Errorf("status codes = %v, want %d of 503 and %d of 200", counts, limit, sessions*callsPerSession-limit)
	}
}

// ---- Serve and the helpers of the handlers ----

func TestSendStatusWritesTheStatusHeadersAndLogsTheRequest(t *testing.T) {
	port := isolate(t)
	req := newRequest("POST", "/mcp/x", port, `{"jsonrpc":"2.0","method":"tools/call"}`)
	rr := httptest.NewRecorder()
	sendStatus("server-id", 503, 4, rr, req)
	if rr.Code != 503 {
		t.Errorf("status = %d", rr.Code)
	}
	expectHeader(t, rr.Header(), constants.HeaderGotoForcedStatus, "503")
	expectHeader(t, rr.Header(), constants.HeaderGotoForcedStatusRemaining, "4")
	rs := util.GetRequestStore(req)
	if len(rs.LogMessages) == 0 || !strings.Contains(rs.LogMessages[len(rs.LogMessages)-1], "server-id Reporting status [503], Remaining status count [4]") {
		t.Errorf("log messages = %v", rs.LogMessages)
	}
	if !strings.Contains(rs.LogMessages[len(rs.LogMessages)-1], "tools/call") {
		t.Errorf("the log message doesn't have the payload: %v", rs.LogMessages)
	}
}

func TestServeSetsTheStatusCodeOfTheRequestStoreToTheForcedStatus(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "serve-a", true)
	StatusManager.SetStatusFor(port, s.URI, "", "", "", []string{"POST"}, []int{507}, 1, true)
	req := newRequest("POST", s.URI, port, toolsCallBody(1, "ServerDetails", ""))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "handled") })
	Serve(s, rr, req, handler)
	if rr.Code != 507 {
		t.Errorf("status = %d, want 507", rr.Code)
	}
	if got := util.GetRequestStore(req).StatusCode; got != 507 {
		t.Errorf("status of the request store = %d, want 507", got)
	}
	if rr.Body.String() != "handled" {
		t.Errorf("body = %q, want the response of the handler to be sent with the status", rr.Body.String())
	}
}

// ---- proxying ----

func TestProxyToolIsSetUpAsAProxyTargetOfThePort(t *testing.T) {
	port := isolate(t)
	addServer(t, port, "proxy-a", true)
	t.Cleanup(func() { delete(mcpproxy.GetMCPProxyForPort(port).Targets, "proxied") })
	addTools(t, port, "proxy-a", `[{"tool":{"name":"proxied","inputSchema":{"type":"object"}},"proxy":true,"config":{"remote":{"url":"http://127.0.0.1:1/mcp/upstream"}}}]`)
	target := mcpproxy.GetMCPProxyForPort(port).Targets["proxied"]
	if target == nil {
		t.Fatal("the tool is not a target of the proxy of the port")
	}
	if target.Endpoint != "http://127.0.0.1:1/mcp/upstream" || target.Name != "proxy-a" {
		t.Errorf("target = %+v", target)
	}
	// A tool that isn't a proxy one is not.
	addTools(t, port, "proxy-a", "["+echoToolJSON+"]")
	if mcpproxy.GetMCPProxyForPort(port).Targets["echo"] != nil {
		t.Error("a tool that is not a proxy was made a target of the proxy")
	}
}

// The calls of the tools that the proxy of the port doesn't have are served by the server, which can only read them if
// the proxy has left their body as it was.
func TestCallsOfOtherToolsAreStillServedWhenThePortHasAProxyTarget(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "proxy-b", true)
	t.Cleanup(func() { delete(mcpproxy.GetMCPProxyForPort(port).Targets, "proxied") })
	addTools(t, port, "proxy-b", `[{"tool":{"name":"proxied","inputSchema":{"type":"object"}},"proxy":true,"config":{"remote":{"url":"http://127.0.0.1:1/mcp/upstream"}}},`+echoToolJSON+`]`)
	// The middleware of the server marks the requests for MCP, and keeps their body for being read again.
	e := newEndpointWith(t, port, func(r *http.Request, rs *util.RequestStore) {
		rs.IsMCP = true
		r.Body = util.CreateOrGetReReader(r.Body)
	})
	resp := e.callTool(s.URI, "echo", `{"text":"not proxied"}`, nil)
	if resp.code != http.StatusOK {
		t.Fatalf("status of the call of echo = %d: %s", resp.code, resp.body)
	}
	expectContains(t, resp.body, "not proxied")
	expectHeader(t, resp.header, constants.HeaderGotoMCPServer, s.ID)
}
