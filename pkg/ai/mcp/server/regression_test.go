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
	"context"
	"encoding/json"
	"fmt"
	aicommon "goto/pkg/ai/common"
	registryclient "goto/pkg/registry/client"
	"goto/pkg/util"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// A route of a server of another port is not a reason to fail to find the server of the port.
func TestRegressionFindServerForURISkipsRoutesOfServersOnOtherPorts(t *testing.T) {
	port := isolate(t)
	addServer(t, port+500, "regr-other-port", true)
	mustNotPanic(t, "finding the server of a URI", func() {
		if _, server := findServerForURI(port, "/mcp/something-else"); server != nil {
			t.Errorf("server = %v, want none", server)
		}
		// The server of the other port is found on its own port by its regexp, not by the port that doesn't have it.
		if _, server := findServerForURI(port+500, "/mcp/regr-other-port"); server == nil {
			t.Error("server of the port was not found")
		}
	})
}

func TestRegressionGetServerWithoutAServerReturnsNil(t *testing.T) {
	port := isolate(t)
	mustNotPanic(t, "getServer", func() {
		if s := getServer(newRequest("POST", "/not-mcp", port, "")); s != nil {
			t.Errorf("server = %v, want none", s)
		}
	})
}

func TestRegressionToolsCallWithoutAToolNameIsNotAPanic(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "regr-noname", true)
	for name, body := range map[string]string{
		"no name":      `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`,
		"no params":    `{"jsonrpc":"2.0","id":1,"method":"tools/call"}`,
		"name numeric": `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":5}}`,
	} {
		req := newRequest("POST", s.URI, port, body)
		rr := httptest.NewRecorder()
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "handled") })
		mustNotPanic(t, "serving a call with "+name, func() { Serve(s, rr, req, handler) })
		if rr.Body.String() != "handled" {
			t.Errorf("%s: body = %q, want the request to be served", name, rr.Body.String())
		}
	}
}

func TestRegressionDisabledServerWithoutDefaultServerIsNotAPanic(t *testing.T) {
	port := isolate(t)
	DefaultStatelessServer = nil
	s := addServer(t, port, "regr-off", true)
	GetPortMCPServers(port).Stop("regr-off")
	mustNotPanic(t, "finding the server of a disabled one", func() {
		if server, _ := getServerAndTool(newRequest("POST", s.URI, port, "")); server != nil {
			t.Errorf("server = %v, want none", server)
		}
	})
	// Not served by the MCP handler either, so the HTTP handler serves it.
	e := newEndpoint(t, port)
	if resp := e.callTool(s.URI, "echo", `{}`, nil); resp.code != http.StatusTeapot {
		t.Errorf("status = %d, want the status of the HTTP handler", resp.code)
	}
}

func TestRegressionGetPortMCPServersIsSafeForConcurrentUse(t *testing.T) {
	port := isolate(t)
	var wg sync.WaitGroup
	got := make([]*PortServers, 16)
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Some of them for ports of their own, and all of them for one that they share.
			GetPortMCPServers(port + 20000 + i)
			got[i] = GetPortMCPServers(port + 19999)
			GetMCPServer(port+20000+i, "x")
		}(i)
	}
	wg.Wait()
	for i := range got {
		if got[i] == nil || got[i] != got[0] {
			t.Fatalf("worker %d got a different PortServers for the shared port", i)
		}
	}
}

// The result of a call is counted for its tool, not for none, and only the calls of tools have results.
func TestRegressionToolCallsAreTrackedByToolName(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "regr-track", true)
	addTools(t, port, "regr-track", "["+echoToolJSON+"]")
	e := newEndpoint(t, port)
	if resp := e.post(s.URI, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, nil); resp.code != http.StatusOK {
		t.Fatalf("tools/list = %d: %s", resp.code, resp.body)
	}
	if resp := e.callTool(s.URI, "echo", `{"text":"x"}`, nil); resp.code != http.StatusOK {
		t.Fatalf("tools/call = %d: %s", resp.code, resp.body)
	}
	if got := getPortServerCount(port, "regr-track", "echo", ToolCallSuccessByPortServer); got != 1 {
		t.Errorf("successful calls of echo = %d, want 1", got)
	}
	if got := getPortServerCount(port, "regr-track", "", ToolCallSuccessByPortServer); got != 0 {
		t.Errorf("successful calls of no tool = %d, want 0", got)
	}
}

func TestRegressionConfigureStatusOfAnUnknownServerAppliesNothing(t *testing.T) {
	port := isolate(t)
	addServer(t, port, "regr-cfg", true)
	rr := admin(port, "POST", "/mcpapi/server/no-such-server/status/configure", `{"statuses":[503]}`)
	expectCode(t, rr, http.StatusBadRequest)
	if configs := StatusManager.Snapshot()[port]; len(configs) != 0 {
		t.Errorf("a status that is for no server was applied to the port: %s", statusesOf(port))
	}
}

func TestRegressionStatusByHeaderIsForTheCallsThatHaveTheHeader(t *testing.T) {
	f := newForcedServer(t)
	f.admin("POST", "/status/set/503/header/x-force")
	with := f.call(map[string]string{"X-Force": "yes"})
	without := f.call(nil)
	expectCodes(t, codesOf(with, without), 503, 200)
}

func TestRegressionStatusByHeaderNotIsForTheCallsThatDontHaveTheHeader(t *testing.T) {
	f := newForcedServer(t)
	f.admin("POST", "/status/set/503/header/not/x-skip")
	with := f.call(map[string]string{"X-Skip": "1"})
	without := f.call(nil)
	expectCodes(t, codesOf(with, without), 200, 503)
}

func TestRegressionClearStatusOfAServerClearsItsStatuses(t *testing.T) {
	f := newForcedServer(t)
	f.admin("POST", "/status/set/503")
	f.admin("POST", "/tool/echo/status/set/502")
	f.admin("POST", "/status/clear")
	expectCodes(t, codesOf(f.call(nil), f.callOther()), 200, 200)
	if configs := StatusManager.Snapshot()[f.port]; len(configs) != 0 {
		t.Errorf("configs after clearing = %s", statusesOf(f.port))
	}
}

func TestRegressionClearStatusOfAnUnknownServerLeavesTheStatusesOfThePort(t *testing.T) {
	f := newForcedServer(t)
	f.admin("POST", "/status/set/503")
	rr := admin(f.port, "POST", "/mcpapi/server/no-such-server/status/clear", "")
	expectCode(t, rr, http.StatusBadRequest)
	expectCodes(t, codesOf(f.call(nil)), 503)
}

func TestRegressionReportingStatusOfAnUnknownServerIsABadRequest(t *testing.T) {
	port := isolate(t)
	expectCode(t, admin(port, "GET", "/mcpapi/server/no-such-server/status", ""), http.StatusBadRequest)
}

func TestRegressionForcedStatusIsNotKeptForTheNextCall(t *testing.T) {
	f := newForcedServer(t)
	f.admin("POST", "/status/set/503:1")
	expectCodes(t, codesOf(f.call(nil), f.call(nil), f.call(nil)), 503, 200, 200)
}

func TestRegressionStatusOfAToolIsNotKeptForTheNextCall(t *testing.T) {
	f := newForcedServer(t)
	forced := f.callTool(f.server.URI, "status", `{"status":418}`, nil)
	expectCodes(t, codesOf(forced, f.call(nil), f.callOther()), 418, 200, 200)
}

func TestRegressionCallToolAPIWorksWithoutAClientSession(t *testing.T) {
	port := isolate(t)
	addServer(t, port, "regr-call", true)
	addTools(t, port, "regr-call", "["+echoToolJSON+"]")
	mustNotPanic(t, "calling a tool through the admin API", func() {
		body := mustAdmin(t, port, "POST", "/mcpapi/server/regr-call/tool/echo/call", `{"text":"hello there"}`)
		expectBodyContains(t, body, "hello there")
	})
	// And without a body, which is a call with no args.
	mustNotPanic(t, "calling a tool through the admin API without args", func() {
		body := mustAdmin(t, port, "POST", "/mcpapi/server/regr-call/tool/echo/call", "")
		expectBodyContains(t, body, "Received Input:")
	})
	mustNotPanic(t, "calling a tool that needs a client through the admin API", func() {
		addTools(t, port, "regr-call", `[{"tool":{"name":"pinger","inputSchema":{"type":"object"}},"behavior":{"ping":true}}]`)
		rr := admin(port, "POST", "/mcpapi/server/regr-call/tool/pinger/call", "{}")
		expectBodyContains(t, rr.Body.String(), "ping failed")
	})
}

func TestRegressionAddToolToolWithoutADefinitionReportsTheFailure(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "regr-addtool", true)
	tool := s.GetTool("AddTool")
	tctx := &ToolContext{MCPTool: tool, args: aicommon.NewCallArgs()}
	var res *gomcp.CallToolResult
	mustNotPanic(t, "the AddTool tool without a tool definition", func() {
		var err error
		if res, err = tool.addTool(tctx); err != nil {
			t.Errorf("err = %v", err)
		}
	})
	if res == nil || len(res.Content) == 0 {
		t.Fatal("no result")
	}
	if text := res.Content[0].(*gomcp.TextContent).Text; !strings.Contains(text, "Failed to add Tool") {
		t.Errorf("text = %q, want it to say that adding the tool failed", text)
	}
}

func TestRegressionComponentsWithoutADefinitionAreRejected(t *testing.T) {
	port := isolate(t)
	addServer(t, port, "regr-nodef", true)
	for _, kind := range []string{"prompts", "resources", "templates"} {
		rr := admin(port, "POST", "/mcpapi/server/regr-nodef/"+kind+"/add", `[{}]`)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d (%q), want 400", kind, rr.Code, rr.Body.String())
		}
	}
	if s := GetMCPServer(port, "regr-nodef"); len(s.Prompts)+len(s.Resources)+len(s.ResourceTemplates) != 0 {
		t.Error("components were added without a definition")
	}
}

func TestRegressionParseResourceDoesNotPanicOnWrongTypes(t *testing.T) {
	for _, payload := range []string{
		`{"resource":"text"}`,
		`{"resource":{"name":"a","uri":"file:///a","text":5}}`,
		`{"resource":[1]}`,
	} {
		mustNotPanic(t, "ParseResource "+payload, func() {
			if r, err := ParseResource([]byte(payload)); err == nil {
				t.Errorf("ParseResource(%s) = %v, want an error", payload, r)
			}
		})
	}
	for _, payload := range []string{
		`{"template":"text"}`,
		`{"template":{"name":"a","uriTemplate":"file:///{a}","text":5}}`,
	} {
		mustNotPanic(t, "ParseResourceTemplate "+payload, func() {
			if r, err := ParseResourceTemplate([]byte(payload)); err == nil {
				t.Errorf("ParseResourceTemplate(%s) = %v, want an error", payload, r)
			}
		})
	}
}

func TestRegressionComponentsThatTheSDKRefusesAreRejectedNotPanics(t *testing.T) {
	port := isolate(t)
	addServer(t, port, "regr-sdk", true)
	for name, tt := range map[string]struct{ kind, payload string }{
		"resource with an invalid URI":        {"resources", `[{"resource":{"name":"r","uri":"%zz","text":"x"}}]`},
		"template with an invalid URI":        {"templates", `[{"template":{"name":"t","uriTemplate":"file:///{unclosed","text":"x"}}]`},
		"tool with a schema of a type":        {"tools", `[{"tool":{"name":"t1","inputSchema":{"type":"string"}},"behavior":{"echo":true}}]`},
		"tool with a schema that is a string": {"tools", `[{"tool":{"name":"t2","inputSchema":"abc"},"behavior":{"echo":true}}]`},
		"proxy tool without a remote tool":    {"tools", `[{"tool":{"name":"t3","inputSchema":{"type":"object"}},"proxy":true}]`},
		"tool without a name":                 {"tools", `[{"tool":{"inputSchema":{"type":"object"}},"behavior":{"echo":true}}]`},
	} {
		var rr *httptest.ResponseRecorder
		mustNotPanic(t, name, func() { rr = admin(port, "POST", "/mcpapi/server/regr-sdk/"+tt.kind+"/add", tt.payload) })
		if rr == nil || rr.Code != http.StatusBadRequest {
			t.Errorf("%s: response = %v, want 400", name, rr)
		}
	}
	// The server still takes components that are good, which it wouldn't if the SDK kept its lock.
	addTools(t, port, "regr-sdk", "["+echoToolJSON+"]")
	mustAdmin(t, port, "POST", "/mcpapi/server/regr-sdk/resources/add", `[{"resource":{"name":"ok","uri":"file:///ok.txt","text":"x"}}]`)
}

func TestRegressionToolWithAnInvalidBehaviorIsNotAdded(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "regr-badtool", true)
	tool := &MCPTool{
		Tool:     &gomcp.Tool{Name: "bad", InputSchema: &jsonschema.Schema{Type: "object"}},
		Behavior: ToolBehavior{Fetch: true}, // without where to fetch from
	}
	mustNotPanic(t, "adding a tool with an invalid behavior", func() { s.AddTool(tool) })
	if s.GetTool("bad") != nil {
		t.Error("a tool that has nothing to run was added")
	}
}

func TestRegressionToolNamesLoseTheirQuotes(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "regr-quote", true)
	names, err := s.AddTools([]byte(`[{"tool":{"name":"\"quoted\"","inputSchema":{"type":"object"}},"behavior":{"echo":true}}]`))
	if err != nil || len(names) != 1 || names[0] != "quoted" {
		t.Fatalf("names = %v, err = %v", names, err)
	}
	if s.GetTool("quoted") == nil || s.GetTool("quoted").Tool.Name != "quoted" {
		t.Error("tool is not kept under its name without the quotes")
	}
}

func TestRegressionTemplateNamesAreReportedForAServerWithoutAPort(t *testing.T) {
	port := isolate(t)
	s := NewMCPServer(newPayload(port, "regr-tpl"))
	names, err := s.AddResourceTemplates([]byte(`[{"template":{"name":"tpl","uriTemplate":"file:///{n}.txt"}}]`))
	if err != nil || len(names) != 1 || names[0] != "tpl" {
		t.Errorf("names = %v, err = %v", names, err)
	}
}

func TestRegressionRemoveMCPServerFindsTheServerByAnyCaseOfItsName(t *testing.T) {
	port := isolate(t)
	GetPortMCPServers(port).AddMCPServer(newPayload(port, "RegrMixed"))
	GetPortMCPServers(port).AddMCPServer(newPayload(port, "regr-keep"))
	RemoveMCPServer(port, "RegrMixed")
	if GetMCPServer(port, "regrmixed") != nil {
		t.Error("server was not removed")
	}
}

func TestRegressionRemoveTheLastMCPServerWithoutDefaultServers(t *testing.T) {
	port := isolate(t)
	DefaultStatefulServer = nil
	GetPortMCPServers(port).AddMCPServer(newPayload(port, "regr-last"))
	mustNotPanic(t, "removing the last server", func() { RemoveMCPServer(port, "regr-last") })
	if GetMCPServer(port, "regr-last") != nil {
		t.Error("server was not removed")
	}
}

func TestRegressionServerRoutesAreOnlyOfTheUsedURIs(t *testing.T) {
	port := isolate(t)
	// A URI that is not under /mcp has no SSE URI to route the SSE requests of the server under.
	uri := fmt.Sprintf("/custom-%d", port)
	GetPortMCPServers(port).AddMCPServer(&MCPServerPayload{Port: port, Name: "regr-routes", Version: "1", Enabled: true, URI: uri})
	addTools(t, port, "regr-routes", "["+echoToolJSON+"]")
	for _, junk := range []string{"", "/echo", "/serverdetails", "/echo/", "/"} {
		if pair := ServerRoutes[junk]; pair != nil {
			t.Errorf("there is a route for %q: %v", junk, pair)
		}
	}
	if pair := ServerRoutes[uri+"/echo"]; pair == nil || pair.LeftS() != "regr-routes" {
		t.Errorf("route of the tool = %v", pair)
	}
}

func TestRegressionToolsOfAServerAreKnownAsTools(t *testing.T) {
	port := isolate(t)
	s := NewMCPServer(newPayload(port, "regr-kind"))
	tool := s.GetTool("ServerDetails")
	if tool.Kind != KindTools || !strings.Contains(tool.Label, "/tools/ServerDetails") {
		t.Errorf("kind = %q, label = %q", tool.Kind, tool.Label)
	}
}

func TestRegressionLogMessagesAreNotFormats(t *testing.T) {
	tctx := &ToolContext{}
	msg := "fetched http://host/a%2Fb at 100% of the rate %s"
	if got := tctx.Log(msg); got != msg {
		t.Errorf("Log(%q) = %q", msg, got)
	}
	if got := tctx.Flush(false, false); got != msg {
		t.Errorf("Flush = %q", got)
	}
	if got := tctx.Log("count %d", 5); got != "count 5" {
		t.Errorf("Log with args = %q", got)
	}
}

func TestRegressionParseArgsOfAnEmptyAndANullPayload(t *testing.T) {
	for name, raw := range map[string]json.RawMessage{"nil": nil, "empty": {}, "null": json.RawMessage("null")} {
		mustNotPanic(t, "parseArgs of "+name, func() {
			args, err := parseArgs(raw)
			if err != nil || args == nil {
				t.Errorf("parseArgs(%s) = %v, %v, want the default args", name, args, err)
			}
		})
	}
}

func TestRegressionServeWaitsForTheStreamHandlerOfAFinishedSession(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "regr-get", false)
	var handlerDone sync.WaitGroup
	handlerDone.Add(1)
	stopped := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer handlerDone.Done()
		// A stream that takes a while to stop once its request is canceled.
		<-r.Context().Done()
		time.Sleep(100 * time.Millisecond)
		close(stopped)
	})
	req := newRequest("GET", s.URI, port, "")
	req.Header.Set(HeaderMCPSessionID, "regr-get-session")
	served := make(chan struct{})
	go func() {
		defer close(served)
		Serve(s, httptest.NewRecorder(), req, handler)
	}()
	var session *SessionContext
	deadline := time.Now().Add(5 * time.Second)
	for session == nil && time.Now().Before(deadline) {
		session = s.getSessionContext("regr-get-session")
		time.Sleep(time.Millisecond)
	}
	if session == nil {
		t.Fatal("session was not created")
	}
	session.finished <- true
	select {
	case <-served:
	case <-time.After(10 * time.Second):
		t.Fatal("Serve didn't return after the session finished")
	}
	select {
	case <-stopped:
	default:
		t.Error("Serve returned while the handler of the stream was still running")
	}
	handlerDone.Wait()
}

func TestRegressionSessionStoresAreSafeForConcurrentSessions(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "regr-sessions", false)
	const sessions = 16
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })
	reqs := make([]*http.Request, sessions)
	recorders := make([]*httptest.ResponseRecorder, sessions)
	for i := range reqs {
		reqs[i] = newRequest("POST", s.URI, port, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
		reqs[i].Header.Set(HeaderMCPSessionID, fmt.Sprintf("regr-session-%d", i))
		recorders[i] = httptest.NewRecorder()
	}
	var wg sync.WaitGroup
	for i := 0; i < sessions; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for round := 0; round < 5; round++ {
				Serve(s, recorders[i], reqs[i], handler)
			}
		}(i)
	}
	waitFor(t, &wg, 30*time.Second)
	for i, rr := range recorders {
		if rr.Code != 200 || !strings.HasPrefix(rr.Body.String(), "ok") {
			t.Errorf("session %d: %d %q", i, rr.Code, rr.Body.String())
		}
	}
}

func waitFor(t *testing.T, wg *sync.WaitGroup, timeout time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatal("workers did not finish")
	}
}

// Deleting a session twice at once is deleting it, not a crash.
func TestRegressionSessionCanBeDeletedTwiceAtOnce(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "regr-del", false)
	var inside sync.WaitGroup
	inside.Add(2)
	release := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Both requests have the session before either of them is done with it.
		inside.Done()
		<-release
	})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		req := newRequest("DELETE", s.URI, port, "")
		req.Header.Set(HeaderMCPSessionID, "regr-del-session")
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					errs <- fmt.Errorf("panic: %v", r)
				}
			}()
			Serve(s, httptest.NewRecorder(), req, handler)
		}()
	}
	entered := make(chan struct{})
	go func() { inside.Wait(); close(entered) }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the requests didn't get to the handler")
	}
	close(release)
	waitFor(t, &wg, 10*time.Second)
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestRegressionGetOrSetSessionContextGivesOneSessionPerID(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "regr-ctx", false)
	var wg sync.WaitGroup
	got := make([]*SessionContext, 32)
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i] = s.getOrSetSessionContext("regr-ctx-session")
		}(i)
	}
	waitFor(t, &wg, 10*time.Second)
	for i := range got {
		if got[i] == nil || got[i] != got[0] {
			t.Fatalf("worker %d got another session than worker 0", i)
		}
	}
}

func TestRegressionClearServerByNameClearsOnlyThatServer(t *testing.T) {
	port := isolate(t)
	// The name of this one has "all" in it, which is not a reason to clear all servers.
	small := addServer(t, port, "regr-small", true)
	other := addServer(t, port, "regr-other", true)
	addTools(t, port, "regr-small", "["+echoToolJSON+"]")
	addTools(t, port, "regr-other", "["+echoToolJSON+"]")
	expectBodyContains(t, mustAdmin(t, port, "POST", "/mcpapi/server/regr-small/clear", ""), "Cleared MCP server [regr-small]")
	if len(small.Tools) != 0 {
		t.Errorf("server was not cleared, it has %d tools", len(small.Tools))
	}
	if GetMCPServer(port, "regr-other") != other || len(other.Tools) == 0 {
		t.Error("another server was cleared")
	}
}

func TestRegressionServerNamesThatHaveAllOrNamesAreServers(t *testing.T) {
	port := isolate(t)
	addServer(t, port, "tall-server", true)
	addServer(t, port, "names-server", true)
	for _, name := range []string{"tall-server", "names-server"} {
		body := mustAdmin(t, port, "GET", "/mcpapi/server/"+name, "")
		expectJSONContains(t, body, fmt.Sprintf(`"name":%q`, name))
		if strings.Contains(body, `"servers"`) {
			t.Errorf("%s: got the servers of the ports instead of the server", name)
		}
	}
}

func TestRegressionPayloadOfAComponentWithStreamInItsNameIsNotAStream(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "regr-streamer", true)
	addTools(t, port, "regr-streamer", `[{"tool":{"name":"streamer","inputSchema":{"type":"object"}},"behavior":{"echo":true}}]`)
	mustAdmin(t, port, "POST", "/mcpapi/server/regr-streamer/payload/tools/streamer", `{"a":1}`)
	if r := s.Tools["streamer"].Response; r == nil || r.IsStream {
		t.Errorf("payload = %+v, want one that is not a stream", r)
	}
}

func TestRegressionStartAndStopOfAnUnknownServerAreBadRequests(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "regr-startstop", true)
	expectCode(t, admin(port, "POST", "/mcpapi/server/no-such-server/stop", ""), http.StatusBadRequest)
	expectCode(t, admin(port, "POST", "/mcpapi/server/no-such-server/start", ""), http.StatusBadRequest)
	if !s.Enabled {
		t.Error("a server was stopped by a request for another one")
	}
}

func TestRegressionInvalidURIRegexIsRejected(t *testing.T) {
	port := isolate(t)
	var rr *httptest.ResponseRecorder
	mustNotPanic(t, "adding a server with an invalid regex", func() {
		rr = admin(port, "POST", "/mcpapi/servers/add", `[{"name":"regr-regex","version":"1","uriRegex":"("}]`)
	})
	if rr == nil || rr.Code != http.StatusBadRequest {
		t.Errorf("response = %v, want 400", rr)
	}
	mustNotPanic(t, "creating a server with an invalid regex", func() {
		s := NewMCPServer(&MCPServerPayload{Port: port, Name: "regr-regex2", Version: "1", URI: "/mcp/regr-regex2", URIRegex: "("})
		if s.uriRegexp == nil || !s.uriRegexp.MatchString("/mcp/regr-regex2") {
			t.Errorf("a server with an invalid regex should be matched by its URI, regexp = %v", s.uriRegexp)
		}
	})
}

func TestRegressionServerWithAURIThatIsNotARegexpIsStillRouted(t *testing.T) {
	port := isolate(t)
	mustNotPanic(t, "creating a server with a URI that has regexp characters", func() {
		s := NewMCPServer(&MCPServerPayload{Port: port, Name: "regr-uri", Version: "1", URI: "/mcp/a(b"})
		if s.uriRegexp == nil || !s.uriRegexp.MatchString("/mcp/a(b/tool") {
			t.Errorf("regexp = %v", s.uriRegexp)
		}
	})
}

func TestRegressionServerIsSafeToReportWhileItIsChanged(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "regr-marshal", true)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for i := 0; i < 3; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					json.Marshal(GetPortMCPServers(port))
					json.Marshal((*PortServers)(nil).AllServers())
					GetMCPServerNames()
					s.GetComponents(KindTools)
					GetAllComponents(KindTools)
					getServerAndTool(newRequest("POST", s.URI, port, ""))
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 30; i++ {
			s.AddTools([]byte(fmt.Sprintf(`[{"tool":{"name":"t%d","inputSchema":{"type":"object"}},"behavior":{"echo":true}}]`, i)))
			if i%10 == 0 {
				GetPortMCPServers(port).Stop("regr-marshal")
				GetPortMCPServers(port).Start("regr-marshal")
			}
		}
	}()
	waitFor(t, &wg, 60*time.Second)
	close(stop)
	readers.Wait()
	if n := len(s.GetComponents(KindTools).(map[string]*MCPTool)); n != 35 {
		t.Errorf("tools = %d, want 30 and the 5 default ones", n)
	}
}

func TestRegressionFetchToolSendsTheConfiguredAuthority(t *testing.T) {
	port := isolate(t)
	hosts := make(chan string, 4)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hosts <- r.Host
		io.WriteString(w, "upstream says hi")
	}))
	t.Cleanup(upstream.Close)
	s := addServer(t, port, "regr-fetch", true)
	addTools(t, port, "regr-fetch", fmt.Sprintf(
		`[{"tool":{"name":"fetcher","inputSchema":{"type":"object"}},"behavior":{"fetch":true},"config":{"remote":{"url":%q,"authority":"configured.example.com"}}}]`, upstream.URL))
	e := newEndpoint(t, port)
	resp := e.callTool(s.URI, "fetcher", `{}`, nil)
	if resp.code != http.StatusOK || !strings.Contains(resp.body, "upstream says hi") {
		t.Fatalf("response = %d %s", resp.code, resp.body)
	}
	select {
	case host := <-hosts:
		if host != "configured.example.com" {
			t.Errorf("host of the request = %q, want the authority of the tool", host)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the upstream was not called")
	}
}

// A call that is slow is not forced the status that another call forced and was done with in the meantime: the calls of a
// stateless server, which have no session to tell them apart, don't share a store.
func TestRegressionStatusForcedByACallDoesNotLeakToAnotherCallInFlight(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "regr-leak", true)
	addTools(t, port, "regr-leak", "["+echoToolJSON+","+statusToolJSON+"]")
	e := newEndpoint(t, port)
	slowReq, err := http.NewRequest("POST", e.ts.URL+s.URI, strings.NewReader(toolsCallBody(1, "echo", `{"text":"slow","delay":"500ms"}`)))
	if err != nil {
		t.Fatal(err)
	}
	slowReq.Header.Set("Content-Type", "application/json")
	slowReq.Header.Set("Accept", "application/json, text/event-stream")
	slow := make(chan int, 1)
	go func() {
		resp, err := e.hc.Do(slowReq)
		if err != nil {
			slow <- -1
			return
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		slow <- resp.StatusCode
	}()
	// The slow call is under way before the other one starts, and it is still when that one is done.
	time.Sleep(150 * time.Millisecond)
	if resp := e.callTool(s.URI, "status", `{"status":452}`, nil); resp.code != 452 {
		t.Errorf("status of the call that forces one = %d, want 452", resp.code)
	}
	select {
	case code := <-slow:
		if code != http.StatusOK {
			t.Errorf("status of the slow call = %d, want 200, since it forced none", code)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the slow call didn't finish")
	}
}

func TestRegressionHandleMCPDefaultKeepsTheSessionOnTheServerThatServesIt(t *testing.T) {
	port := isolate(t)
	DefaultStatelessServer = addServer(t, port, "regr-dflt-stateless", true)
	DefaultStatefulServer = addServer(t, port, "regr-dflt-stateful", false)
	req := newRequest("POST", "/mcp/anything", port, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set(HeaderMCPSessionID, "regr-dflt-session")
	rr := httptest.NewRecorder()
	util.GetRequestStore(req).ResponseWriter = rr
	mustNotPanic(t, "handling the request with the default server", func() { HandleMCPDefault(rr, req) })
	if DefaultStatelessServer.getSessionContext("regr-dflt-session") == nil {
		t.Error("the stateless default server, which served the request, doesn't have its session")
	}
	if DefaultStatefulServer.getSessionContext("regr-dflt-session") != nil {
		t.Error("the stateful default server has the session of a request that it didn't serve")
	}
}

func TestRegressionHandleMCPDefaultWithoutADefaultServerIsNotAPanic(t *testing.T) {
	port := isolate(t)
	DefaultStatelessServer, DefaultStatefulServer = nil, nil
	for _, uri := range []string{"/mcp/a", "/mcp/stateful/a", "/mcp/sse/a", "/mcp/stateful/sse/a"} {
		rr := httptest.NewRecorder()
		mustNotPanic(t, "handling "+uri, func() { HandleMCPDefault(rr, newRequest("POST", uri, port, "{}")) })
		if rr.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: status = %d, want 503", uri, rr.Code)
		}
	}
}

func TestRegressionComponentThatIsNotOnAServerCanBeNamed(t *testing.T) {
	mustNotPanic(t, "naming a prompt that has no server", func() {
		p := NewMCPPrompt("loose", "a prompt")
		p.Kind = KindPrompts
		p.SetName("loose")
		if p.GetName() != "loose" || p.Label != "/prompts/loose" {
			t.Errorf("name = %q, label = %q", p.GetName(), p.Label)
		}
	})
}

func TestRegressionHeadersOfACallAreACopyOfTheOnesOfTheRequest(t *testing.T) {
	live := http.Header{"X-Client": {"yes"}}
	rs := &util.RequestStore{RequestURI: "/mcp/x", RequestHost: "host.example.com"}
	req := &gomcp.CallToolRequest{Extra: &gomcp.RequestExtra{Header: live}}
	got := getRequestHeaders(context.Background(), req, rs)
	// The pseudo-headers of the request are not canonical keys, so they are read as they are.
	if got.Get("X-Client") != "yes" || got["RequestURI"][0] != "/mcp/x" || got["RequestHost"][0] != "host.example.com" {
		t.Errorf("headers of the call = %v", got)
	}
	if len(live) != 1 {
		t.Errorf("the headers of the request were changed by the call: %v", live)
	}
}

// A payload that is posted as JSON, which is what it is unless the content type says otherwise, but isn't, is still what
// the component answers with, instead of a "null".
func TestRegressionPayloadThatIsNotJSONIsNotLost(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "regr-payload", true)
	mustAdmin(t, port, "POST", "/mcpapi/server/regr-payload/resources/add", `[{"resource":{"name":"doc","uri":"file:///doc.txt","text":"original"}}]`)
	mustAdmin(t, port, "POST", "/mcpapi/server/regr-payload/payload/resources/doc", `just some plain words`)
	cs := connectInMemory(t, s, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	read, err := cs.ReadResource(ctx, &gomcp.ReadResourceParams{URI: "file:///doc.txt"})
	if err != nil || len(read.Contents) != 1 {
		t.Fatalf("read = %+v, err = %v", read, err)
	}
	if !strings.Contains(read.Contents[0].Text, "just some plain words") {
		t.Errorf("contents = %q, want the payload that was posted", read.Contents[0].Text)
	}
}

func TestRegressionRegistryClientIsMadeOnceForConcurrentCallers(t *testing.T) {
	registryClientLock.Lock()
	saved := MCPRegistryClient
	MCPRegistryClient = nil
	registryClientLock.Unlock()
	t.Cleanup(func() {
		registryClientLock.Lock()
		MCPRegistryClient = saved
		registryClientLock.Unlock()
	})
	const callers = 16
	got := make([]*registryclient.RegistryClient, callers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			got[i] = getRegistryClient()
		}(i)
	}
	close(start)
	waitFor(t, &wg, 30*time.Second)
	for i := range got {
		if got[i] == nil || got[i] != got[0] {
			t.Fatalf("caller %d got another client than caller 0", i)
		}
	}
}

// The status that a call forces is the one of its own response, whatever other calls are in flight on the server.
func TestRegressionConcurrentStatelessCallsAreEachForcedTheirOwnStatus(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "conc-status", true)
	addTools(t, port, "conc-status", "["+statusToolJSON+","+echoToolJSON+"]")
	e := newEndpoint(t, port)
	const n = 24
	reqs := make([]*http.Request, n)
	for i := range reqs {
		tool, args := "status", fmt.Sprintf(`{"status":%d}`, 450+i)
		if i%4 == 3 {
			tool, args = "echo", `{"text":"plain"}`
		}
		req, err := http.NewRequest("POST", e.ts.URL+s.URI, strings.NewReader(toolsCallBody(i+1, tool, args)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		reqs[i] = req
	}
	codes := make([]int, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range reqs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			resp, err := e.hc.Do(reqs[i])
			if err != nil {
				codes[i] = -1
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			codes[i] = resp.StatusCode
		}(i)
	}
	close(start)
	waitFor(t, &wg, 60*time.Second)
	for i, code := range codes {
		want := 450 + i
		if i%4 == 3 {
			want = http.StatusOK
		}
		if code != want {
			t.Errorf("call %d: status = %d, want %d", i, code, want)
		}
	}
}

// A tool that calls the client back, over the transports that allow it. A call over SSE comes with no extra of the
// request, which the handler of the tool took for granted.
func TestRegressionToolsCanCallTheClientBack(t *testing.T) {
	for _, tt := range []struct {
		name string
		sse  bool
	}{{"streamable stateful", false}, {"sse", true}} {
		t.Run(tt.name, func(t *testing.T) {
			port := isolate(t)
			s := addServer(t, port, "regr-callback", false)
			addTools(t, port, "regr-callback", "["+toolJSON("pinger", `{"ping":true}`, "")+","+toolJSON("roots", `{"listRoots":true}`, "")+"]")
			e := newEndpoint(t, port)
			client := gomcp.NewClient(&gomcp.Implementation{Name: "test-client", Version: "1.0"}, nil)
			client.AddRoots(&gomcp.Root{Name: "r1", URI: "file:///r1"})
			var transport gomcp.Transport = &gomcp.StreamableClientTransport{Endpoint: e.ts.URL + s.URI}
			if tt.sse {
				transport = &gomcp.SSEClientTransport{Endpoint: e.ts.URL + s.SSEURI}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cs, err := client.Connect(ctx, transport, nil)
			if err != nil {
				t.Fatalf("connect: %v", err)
			}
			defer cs.Close()
			for tool, want := range map[string]string{"pinger": "Ping from Goto MCP successful", "roots": "r1 [uri: file:///r1]"} {
				res, err := cs.CallTool(ctx, &gomcp.CallToolParams{Name: tool, Arguments: map[string]any{}})
				if err != nil || res.IsError || !strings.Contains(resultText(res), want) {
					t.Errorf("%s: result = %+v, err = %v, want %q", tool, res, err, want)
				}
			}
		})
	}
}

// A call that the status of the server exempts (0) is not forced the status of its tool either, and doesn't use a count
// of it: one call uses one count, whatever the status of the server is.
func TestRegressionExemptStatusOfTheServerDoesNotUseTheCountOfTheTool(t *testing.T) {
	f := newForcedServer(t)
	StatusManager.SetStatusFor(f.port, f.server.URI, "", "", "", []string{"POST"}, []int{0}, 1, true)
	StatusManager.SetStatusFor(f.port, f.server.GetTool("echo").ServerURI, "", "", "", []string{"POST"}, []int{504}, 1, true)
	expectCodes(t, codesOf(f.call(nil), f.call(nil), f.call(nil)), 200, 504, 200)
}
