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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"goto/pkg/global"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Every test uses its own port, and its own server names and URIs, since the registries of the package (servers,
// routes, components, statuses, trackers) are global. isolate puts the registries back when the test is over.
const testPortBase = 41000

var (
	stubOnce sync.Once
	portSeq  atomic.Int32
	apiOnce  sync.Once
)

// stubGlobalFuncs fills the global.Funcs hooks that the server package normally wires up at startup. util.WithRequestStore
// and the MCP tools dereference some of them.
func stubGlobalFuncs() {
	no := func(*http.Request) bool { return false }
	if global.Funcs.IsReadinessProbe == nil {
		global.Funcs.IsReadinessProbe = no
	}
	if global.Funcs.IsLivenessProbe == nil {
		global.Funcs.IsLivenessProbe = no
	}
	if global.Funcs.IsListenerPresent == nil {
		global.Funcs.IsListenerPresent = func(int) bool { return false }
	}
	if global.Funcs.IsListenerOpen == nil {
		global.Funcs.IsListenerOpen = func(int) bool { return false }
	}
	if global.Funcs.IsListenerTLS == nil {
		global.Funcs.IsListenerTLS = func(int) bool { return false }
	}
	if global.Funcs.GetListenerID == nil {
		global.Funcs.GetListenerID = func(int) string { return "" }
	}
	if global.Funcs.GetListenerLabel == nil {
		global.Funcs.GetListenerLabel = func(*http.Request) string { return "" }
	}
	if global.Funcs.GetListenerLabelForPort == nil {
		global.Funcs.GetListenerLabelForPort = func(int) string { return "" }
	}
	if util.WillTunnel == nil {
		util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
	}
	if util.HTTPHandler == nil {
		// What serves the requests that the MCP handler leaves alone.
		util.HTTPHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		})
	}
}

func cloneAllComponents(src map[string]map[string]map[string]IMCPComponent) map[string]map[string]map[string]IMCPComponent {
	out := map[string]map[string]map[string]IMCPComponent{}
	for kind, byName := range src {
		out[kind] = map[string]map[string]IMCPComponent{}
		for name, byServer := range byName {
			out[kind][name] = maps.Clone(byServer)
		}
	}
	return out
}

func clearTrackers(port int) {
	trackLock.Lock()
	defer trackLock.Unlock()
	delete(InitCallsByPortServer, port)
	delete(ToolCallsByPortServer, port)
	delete(ToolCallsByPortServerSession, port)
	delete(ToolCallSuccessByPortServer, port)
	delete(ToolCallFailureByPortServer, port)
	delete(ToolCallDurationByPortServer, port)
}

// isolate returns a port that no other test uses, and restores the global registries when the test is over.
func isolate(t *testing.T) int {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
	port := testPortBase + int(portSeq.Add(1))
	lock.Lock()
	savedServers := maps.Clone(PortsServers)
	savedRoutes := maps.Clone(ServerRoutes)
	savedAll := cloneAllComponents(AllComponents)
	savedStateless, savedStateful := DefaultStatelessServer, DefaultStatefulServer
	lock.Unlock()
	t.Cleanup(func() {
		lock.Lock()
		PortsServers = savedServers
		ServerRoutes = savedRoutes
		AllComponents = savedAll
		DefaultStatelessServer, DefaultStatefulServer = savedStateless, savedStateful
		lock.Unlock()
		for _, p := range []int{port, port + 500} {
			StatusManager.Clear(p, "", "")
			clearTrackers(p)
		}
		for id := range MCPRequestStoreBySession {
			delete(MCPRequestStoreBySession, id)
		}
	})
	return port
}

// mustNotPanic fails the test, instead of crashing it, when f panics.
func mustNotPanic(t *testing.T, what string, f func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s panicked: %v", what, r)
		}
	}()
	f()
}

// adminRouter is the router of the admin API. Its routes are registered once, since they live on a router that is cached
// by path.
func adminRouter() http.Handler {
	apiOnce.Do(func() { Middleware.SetRoutes(nil) })
	return middleware.RootPath("/mcpapi")
}

// newRequest creates a request that arrives on the given listener port and carries a RequestStore, as the server does
// before routing.
func newRequest(method, target string, port int, body string) *http.Request {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, rd)
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, port))
	_, req, _ = util.WithRequestStore(req, nil)
	return req
}

// admin calls the admin API of the MCP servers on the port.
func admin(port int, method, target, body string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	adminRouter().ServeHTTP(rr, newRequest(method, target, port, body))
	return rr
}

func mustAdmin(t *testing.T, port int, method, target, body string) string {
	t.Helper()
	rr := admin(port, method, target, body)
	if rr.Code != http.StatusOK {
		t.Fatalf("%s %s = %d %q, want 200", method, target, rr.Code, rr.Body.String())
	}
	return rr.Body.String()
}

func expectCode(t *testing.T, rr *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rr.Code != want {
		t.Fatalf("status = %d, want %d (body: %q)", rr.Code, want, rr.Body.String())
	}
}

func expectBodyContains(t *testing.T, body, want string) {
	t.Helper()
	if !strings.Contains(body, want) {
		t.Errorf("body = %q, want it to contain %q", body, want)
	}
}

// expectJSONContains compares without the whitespace of the indented JSON that the API writes.
func expectJSONContains(t *testing.T, body, want string) {
	t.Helper()
	buf := &bytes.Buffer{}
	if err := json.Compact(buf, []byte(body)); err != nil {
		t.Errorf("body %q is not JSON: %v", body, err)
		return
	}
	if !strings.Contains(buf.String(), want) {
		t.Errorf("body = %q, want it to contain %q", buf.String(), want)
	}
}

// serverURI is the URI of a server, which is also how the server is found by its name when its URI is not a route.
func serverURI(port int, name string) string {
	return "/mcp/" + name
}

// addServer adds a server through the admin API, as a user does, and returns it.
func addServer(t *testing.T, port int, name string, stateless bool) *MCPServer {
	t.Helper()
	payload := fmt.Sprintf(`[{"name":%q,"version":"1.0","uri":%q,"stateless":%t,"enabled":true}]`,
		name, serverURI(port, name), stateless)
	mustAdmin(t, port, "POST", "/mcpapi/servers/add", payload)
	s := GetMCPServer(port, name)
	if s == nil {
		t.Fatalf("server %s was not added on port %d", name, port)
	}
	return s
}

// addTools adds tools to the server through the admin API.
func addTools(t *testing.T, port int, server, tools string) {
	t.Helper()
	mustAdmin(t, port, "POST", "/mcpapi/server/"+server+"/tools/add", tools)
}

const (
	echoToolJSON   = `{"tool":{"name":"echo","description":"echo tool","inputSchema":{"type":"object"}},"behavior":{"echo":true}}`
	statusToolJSON = `{"tool":{"name":"status","description":"status tool","inputSchema":{"type":"object"}},"behavior":{"status":true}}`
)

func newPayload(port int, name string) *MCPServerPayload {
	return &MCPServerPayload{Port: port, Name: name, Version: "1.0", Enabled: true, URI: serverURI(port, name)}
}

// connectInMemory connects a client to the server without any HTTP in between, and makes the session store that the
// tools look the request up from. The store is what the HTTP handler makes for the requests of a stateless server.
func connectInMemory(t *testing.T, s *MCPServer, opts *gomcp.ClientOptions) *gomcp.ClientSession {
	t.Helper()
	s.SetMCPSessionStore(newRequest("POST", s.URI, s.Port, ""))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	clientTransport, serverTransport := gomcp.NewInMemoryTransports()
	ss, err := s.server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test-client", Version: "1.0"}, opts)
	cs, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() {
		cs.Close()
		ss.Close()
	})
	return cs
}

func invokeTool(t *testing.T, cs *gomcp.ClientSession, name string, args any) *gomcp.CallToolResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := cs.CallTool(ctx, &gomcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s) = %v", name, err)
	}
	return res
}

func resultText(res *gomcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*gomcp.TextContent); ok {
			sb.WriteString(tc.Text)
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

func toJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// ---- server model ----

func TestNewMCPServerSetsUpTheServer(t *testing.T) {
	port := isolate(t)
	p := newPayload(port, "model-a")
	p.Description = "a server"
	s := NewMCPServer(p)

	if s.Name != "model-a" || s.Version != "1.0" || s.Port != port || s.Description != "a server" || !s.Enabled {
		t.Errorf("server = %+v", s)
	}
	if !strings.HasPrefix(s.ID, fmt.Sprintf("[model-a:%d]", port)) {
		t.Errorf("ID = %q", s.ID)
	}
	if s.GetID() != s.ID || s.GetName() != "model-a" || s.GetPort() != port || s.GetHost() != s.Host {
		t.Errorf("getters return %q %q %d %q", s.GetID(), s.GetName(), s.GetPort(), s.GetHost())
	}
	if s.server == nil || s.handler == nil || s.sseHandler == nil || s.streamHTTPHandler == nil {
		t.Error("handlers are not set up")
	}
	if s.uriRegexp == nil || !s.uriRegexp.MatchString(p.URI) || !s.uriRegexp.MatchString(p.URI+"/echo") || s.uriRegexp.MatchString("/other") {
		t.Errorf("URI regexp = %v", s.uriRegexp)
	}
	for _, name := range []string{"ServerDetails", "ServerPaths", "ListServers", "ListComponents", "AddTool"} {
		tool := s.GetTool(name)
		if tool == nil {
			t.Errorf("default tool %s is missing", name)
			continue
		}
		if tool.Server != s || tool.ServerURI != p.URI+strings.ToLower("/"+name) || tool.Kind != KindTools || tool.Label == "" {
			t.Errorf("tool %s = %+v", name, tool.MCPComponent)
		}
		if tool.Behavior.run == nil {
			t.Errorf("tool %s has nothing to run", name)
		}
	}
}

func TestNewMCPServerWithoutURIHasNoRegexp(t *testing.T) {
	port := isolate(t)
	s := NewMCPServer(&MCPServerPayload{Port: port, Name: "no-uri", Version: "1"})
	if s.uriRegexp != nil || s.Enabled {
		t.Errorf("regexp = %v, enabled = %t", s.uriRegexp, s.Enabled)
	}
}

func TestNewMCPServerWithURIRegex(t *testing.T) {
	port := isolate(t)
	s := NewMCPServer(&MCPServerPayload{Port: port, Name: "re", Version: "1", URIRegex: `^/mcp/re-\d+$`})
	if s.uriRegexp == nil || !s.uriRegexp.MatchString("/mcp/re-12") || s.uriRegexp.MatchString("/mcp/re-x") {
		t.Errorf("regexp = %v", s.uriRegexp)
	}
}

func TestPortServersAddGetAndRemove(t *testing.T) {
	port := isolate(t)
	ps := GetPortMCPServers(port)
	if ps.Port != port || len(ps.Servers) != 0 {
		t.Fatalf("new port servers = %+v", ps)
	}
	if GetPortMCPServers(port) != ps {
		t.Error("the servers of a port are not kept")
	}
	a := ps.AddMCPServer(newPayload(port, "Alpha"))
	b := ps.AddMCPServer(newPayload(port, "beta"))
	if a.ps != ps || b.ps != ps {
		t.Error("server is not linked to its port")
	}
	if GetMCPServer(port, "alpha") != a || GetMCPServer(port, "ALPHA") != a || ps.GetMCPServer("Alpha") != a {
		t.Error("server lookup is not case insensitive")
	}
	if GetMCPServer(port, "missing") != nil || GetMCPServer(port+1, "alpha") != nil {
		t.Error("found a server that doesn't exist on the port")
	}
	if ps.DefaultServer != "beta" || ps.defaultServer != b {
		t.Errorf("default server = %q, want the last added", ps.DefaultServer)
	}

	RemoveMCPServer(port, "beta")
	if GetMCPServer(port, "beta") != nil {
		t.Error("server was not removed")
	}
	if ps.defaultServer != a || ps.DefaultServer != "Alpha" {
		t.Errorf("default server after removal = %q", ps.DefaultServer)
	}
	if got := ps.GetComponents(KindTools); got == nil {
		t.Error("components of the port are missing")
	}
}

func TestAddMCPServersUsesTheGivenPortForPayloadsWithoutOne(t *testing.T) {
	port := isolate(t)
	other := port + 500
	AddMCPServers(port, []*MCPServerPayload{
		{Name: "no-port", Version: "1"},
		{Port: other, Name: "with-port", Version: "1"},
	})
	if GetMCPServer(port, "no-port") == nil {
		t.Error("server without a port is not on the given port")
	}
	if GetMCPServer(other, "with-port") == nil || GetMCPServer(port, "with-port") != nil {
		t.Error("server with a port is not on its own port")
	}
}

func TestGetMCPServerNamesListsToolsByPortAndServer(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "names-a", true)
	addTools(t, port, "names-a", "["+echoToolJSON+"]")
	names := GetMCPServerNames()
	tools := names[port]["names-a"]
	if len(tools) != len(s.Tools) {
		t.Fatalf("tools = %v, want %d of them", tools, len(s.Tools))
	}
	found := false
	for _, n := range tools {
		found = found || n == "echo"
	}
	if !found {
		t.Errorf("tools = %v, want echo among them", tools)
	}
}

func TestStartAndStopServers(t *testing.T) {
	port := isolate(t)
	ps := GetPortMCPServers(port)
	a := ps.AddMCPServer(newPayload(port, "ss-a"))
	b := ps.AddMCPServer(newPayload(port, "ss-b"))

	ps.Stop("SS-A")
	if a.Enabled || !b.Enabled {
		t.Errorf("after stopping a: a=%t b=%t", a.Enabled, b.Enabled)
	}
	ps.Stop("")
	if a.Enabled || b.Enabled {
		t.Errorf("after stopping all: a=%t b=%t", a.Enabled, b.Enabled)
	}
	ps.Start("ss-b")
	if a.Enabled || !b.Enabled {
		t.Errorf("after starting b: a=%t b=%t", a.Enabled, b.Enabled)
	}
	ps.Start("")
	if !a.Enabled || !b.Enabled {
		t.Errorf("after starting all: a=%t b=%t", a.Enabled, b.Enabled)
	}
	ps.Stop("missing")
	if !a.Enabled || !b.Enabled {
		t.Error("stopping a server that doesn't exist changed others")
	}
}

func TestClearAllMCPServers(t *testing.T) {
	port := isolate(t)
	addServer(t, port, "clr-a", true)
	ClearAllMCPServers()
	if GetMCPServer(port, "clr-a") != nil {
		t.Error("server survived clearing all servers")
	}
	if len(GetAllComponents(KindTools)) != 0 {
		t.Errorf("components survived: %v", GetAllComponents(KindTools))
	}
	for _, kind := range Kinds {
		if AllComponents[kind] == nil {
			t.Errorf("kind %s is missing after clearing", kind)
		}
	}
}

func TestMCPServerClearEmptiesTheComponents(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "clr-b", true)
	s.AddCompletionPayload("ref/prompt", []byte(`["x"]`), 0, 0, -1)
	s.getOrSetSessionContext("sess")
	s.Clear()
	if len(s.Tools)+len(s.Prompts)+len(s.Resources)+len(s.ResourceTemplates)+len(s.CompletionPayload)+len(s.sessionContexts) != 0 {
		t.Errorf("server was not cleared: %d tools", len(s.Tools))
	}
}

func TestInitDefaultServerAddsTheStatelessAndStatefulServers(t *testing.T) {
	port := isolate(t)
	saved := global.Self.JSONRPCPort
	global.Self.JSONRPCPort = port
	t.Cleanup(func() { global.Self.JSONRPCPort = saved })

	InitDefaultServer()

	if DefaultStatelessServer == nil || DefaultStatefulServer == nil {
		t.Fatal("default servers are not set")
	}
	if !DefaultStatelessServer.Stateless || DefaultStatefulServer.Stateless {
		t.Error("stateless flags of the default servers are wrong")
	}
	if GetMCPServer(port, "default-stateless") != DefaultStatelessServer || GetMCPServer(port, "default-stateful") != DefaultStatefulServer {
		t.Error("default servers are not registered on the port")
	}
	if ServerRoutes["/mcp/default/stateless"] == nil || ServerRoutes["/mcp/default/stateful"] == nil {
		t.Errorf("routes of default servers are missing: %v", ServerRoutes)
	}
	if got := GetPortDefaultMCPServer(port); got != DefaultStatefulServer {
		t.Errorf("default of the port = %v", got)
	}
}

func TestGetPortDefaultMCPServerFallsBackToTheStatelessServer(t *testing.T) {
	port := isolate(t)
	saved := global.Self.JSONRPCPort
	global.Self.JSONRPCPort = port + 500
	t.Cleanup(func() { global.Self.JSONRPCPort = saved })
	DefaultStatelessServer = nil
	// No server on this port, so the stateless default server is what it gets, and it is created when it is missing.
	got := GetPortMCPServers(port)
	if len(got.Servers) != 0 {
		t.Fatal("port is not empty")
	}
	def := GetPortDefaultMCPServer(port)
	if def == nil || def != DefaultStatelessServer || def.Name != "default-stateless" {
		t.Errorf("default = %v", def)
	}
}

// ---- routes ----

func TestSetServerRouteRegistersTheServerAndItsTools(t *testing.T) {
	port := isolate(t)
	s := NewMCPServer(newPayload(port, "route-a"))
	uri := fmt.Sprintf("/mcp/route-a-%d", port)
	SetServerRoute(strings.ToUpper(uri), s)

	wantSSE := fmt.Sprintf("/mcp/sse/route-a-%d", port)
	if s.SSEURI != wantSSE {
		t.Errorf("SSE URI = %q, want %q", s.SSEURI, wantSSE)
	}
	for _, key := range []string{uri, s.SSEURI} {
		if pair := ServerRoutes[key]; pair == nil || pair.LeftS() != "route-a" || pair.RightS() != "" {
			t.Errorf("route %s = %v", key, pair)
		}
	}
	for _, key := range []string{uri + "/serverdetails", s.SSEURI + "/serverdetails"} {
		if pair := ServerRoutes[key]; pair == nil || pair.LeftS() != "route-a" || pair.RightS() != "serverdetails" {
			t.Errorf("route of a tool %s = %v", key, pair)
		}
	}
}

func TestFindServerForURIByExactAndRegexRoutes(t *testing.T) {
	port := isolate(t)
	ps := GetPortMCPServers(port)
	exact := ps.AddMCPServer(newPayload(port, "find-exact"))
	re := ps.AddMCPServer(&MCPServerPayload{Port: port, Name: "find-re", Version: "1", Enabled: true,
		URI: fmt.Sprintf("/mcp/find-re-%d", port), URIRegex: fmt.Sprintf(`^/mcp/find-re-%d(/.*)?$`, port)})

	if uri, s := findServerForURI(port, exact.URI); s != exact || uri != exact.URI {
		t.Errorf("exact match = %q %v", uri, s)
	}
	if uri, s := findServerForURI(port, re.URI+"/something/deeper"); s != re || uri == "" {
		t.Errorf("regex match = %q %v", uri, s)
	}
	if _, s := findServerForURI(port, "/mcp/unknown"); s != nil {
		t.Errorf("unknown URI matched %v", s)
	}
}

func TestGetPortServerToolFromURI(t *testing.T) {
	for _, tt := range []struct {
		name, uri    string
		port         int
		server, tool string
	}{
		{"server only", "/mcp/srv", 0, "srv", ""},
		{"server and tool", "/mcp/srv/tool1", 0, "srv", "tool1"},
		{"trailing slash", "/mcp/srv/", 0, "srv", ""},
		{"port in URI", "/port=9000/mcp/srv/tool1", 9000, "srv", "tool1"},
		{"sse and mcp", "/mcp/sse/srv/tool1", 0, "srv", "tool1"},
		{"sse only", "/sse/srv", 0, "", ""},
		{"not MCP", "/other/path", 0, "", ""},
		{"bare", "/mcp", 0, "", ""},
	} {
		port, server, tool := getPortServerToolFromURI(tt.uri)
		if port != tt.port || server != tt.server || tool != tt.tool {
			t.Errorf("%s: %q -> (%d, %q, %q), want (%d, %q, %q)", tt.name, tt.uri, port, server, tool, tt.port, tt.server, tt.tool)
		}
	}
}

// ---- components ----

func TestAddComponentsToServer(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "comp-a", true)

	names, err := s.AddComponents(KindTools, []byte("["+echoToolJSON+"]"))
	if err != nil || len(names) != 1 || names[0] != "echo" {
		t.Fatalf("tools: %v %v", names, err)
	}
	names, err = s.AddComponents(KindPrompts, []byte(`[{"prompt":{"name":"greet","description":"say hi"}}]`))
	if err != nil || len(names) != 1 || names[0] != "greet" {
		t.Fatalf("prompts: %v %v", names, err)
	}
	names, err = s.AddComponents(KindResources, []byte(`[{"resource":{"name":"doc","uri":"file:///doc.txt","text":"hello"}}]`))
	if err != nil || len(names) != 1 || names[0] != "doc" {
		t.Fatalf("resources: %v %v", names, err)
	}
	names, err = s.AddComponents(KindTemplates, []byte(`[{"template":{"name":"tpl","uriTemplate":"file:///{name}.txt","text":"t"}}]`))
	if err != nil || len(names) != 1 || names[0] != "tpl" {
		t.Fatalf("templates: %v %v", names, err)
	}
	for kind, name := range map[string]string{KindTools: "echo", KindPrompts: "greet", KindResources: "doc", KindTemplates: "tpl"} {
		c := s.GetComponent(name, kind)
		if c == nil || c.GetName() != name || c.GetKind() != kind {
			t.Errorf("component %s/%s = %v", kind, name, c)
		}
		if GetAllComponents(kind)[name]["comp-a"] == nil {
			t.Errorf("component %s/%s is not registered globally", kind, name)
		}
		if GetPortMCPServers(port).GetComponents(kind) == nil {
			t.Errorf("component %s/%s is not registered on the port", kind, name)
		}
	}
	if s.GetComponents(KindTools) == nil || s.GetComponents("bogus") != nil {
		t.Error("GetComponents returned the wrong thing")
	}
	if s.GetComponent("missing", KindTools) != nil || s.GetComponent("echo", "bogus") != nil {
		t.Error("found a component that doesn't exist")
	}
	if s.Tools["echo"].ServerURI != serverURI(port, "comp-a")+"/echo" {
		t.Errorf("tool server URI = %q", s.Tools["echo"].ServerURI)
	}
	if n, err := s.AddComponents("bogus", nil); err != nil || n != nil {
		t.Errorf("unknown kind: %v %v", n, err)
	}
}

func TestAddComponentsReportsParseErrors(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "comp-err", true)
	for kind, payload := range map[string]string{
		KindTools:     `[{"tool":{"name":"x"}}]`, // no schema
		KindPrompts:   `[not json`,
		KindResources: `[not json`,
		KindTemplates: `[not json`,
	} {
		if names, err := s.AddComponents(kind, []byte(payload)); err == nil || names != nil {
			t.Errorf("%s: names = %v, err = %v, want an error", kind, names, err)
		}
	}
}

func TestAddPayloadSetsTheResponseOfAComponent(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "payload-a", true)
	addTools(t, port, "payload-a", "["+echoToolJSON+"]")

	if err := s.AddPayload("echo", KindTools, []byte(`{"a":1}`), true, false, 0, 0, 0, -1); err != nil {
		t.Fatal(err)
	}
	if s.Tools["echo"].Response == nil {
		t.Error("payload was not set")
	}
	if err := s.AddPayload("nope", KindTools, nil, true, false, 0, 0, 0, -1); err == nil {
		t.Error("payload for a missing component was accepted")
	}
}

func TestAddCompletionPayloadKeepsThePayloadByType(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "compl-a", true)
	n := s.AddCompletionPayload("ref/prompt", []byte("suggestion"), 0, 0, -1)
	if n != 1 {
		t.Errorf("count = %d, want 1", n)
	}
	if s.CompletionPayload["ref/prompt"] == nil {
		t.Error("payload is not kept")
	}
}

func TestAddToolSchemasMakesTheSchemaAvailableToTools(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "schema-a", true)
	name := fmt.Sprintf("custom-%d", port)
	t.Cleanup(func() {
		lock.Lock()
		delete(ToolSchemas, name)
		lock.Unlock()
	})
	names, err := AddToolSchemas([]byte(fmt.Sprintf(`[{"name":%q,"inputSchema":{"type":"object","properties":{"x":{"type":"string"}}}}]`, name)))
	if err != nil || len(names) != 1 || names[0] != name {
		t.Fatalf("names = %v, err = %v", names, err)
	}
	if _, err := s.AddTools([]byte(fmt.Sprintf(`[{"tool":{"name":"uses-schema"},"schema":%q,"behavior":{"echo":true}}]`, name))); err != nil {
		t.Fatal(err)
	}
	if s.Tools["uses-schema"].Tool.InputSchema == nil {
		t.Error("tool didn't get the schema")
	}
	if _, err := AddToolSchemas([]byte(`{{{`)); err == nil {
		t.Error("invalid schemas were accepted")
	}
}

func TestRemoveMCPServerRemovesItsComponentsFromTheRegistries(t *testing.T) {
	port := isolate(t)
	addServer(t, port, "rm-a", true)
	addServer(t, port, "rm-b", true)
	addTools(t, port, "rm-a", "["+echoToolJSON+"]")
	if GetAllComponents(KindTools)["echo"]["rm-a"] == nil {
		t.Fatal("tool is not registered")
	}
	RemoveMCPServer(port, "rm-a")
	if GetAllComponents(KindTools)["echo"]["rm-a"] != nil {
		t.Error("tool of the removed server is still registered")
	}
	if GetMCPServer(port, "rm-b") == nil {
		t.Error("removing a server removed another one")
	}
}

func TestMCPServerRegistryIsSafeForConcurrentUse(t *testing.T) {
	port := isolate(t)
	const workers = 8
	const rounds = 20
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				// A port of its own for every round, so that ports are created concurrently.
				p := port + 10000 + w*rounds + i
				name := fmt.Sprintf("conc-%d-%d", w, i)
				ps := GetPortMCPServers(p)
				s := ps.AddMCPServer(&MCPServerPayload{Port: p, Name: name, Version: "1", Enabled: true, URI: serverURI(p, name)})
				GetMCPServer(p, name)
				GetMCPServerNames()
				s.GetComponents(KindTools)
				ps.Stop("")
				ps.Start(name)
				GetAllComponents(KindTools)
				snapshotToolSchemas()
			}
		}(w)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("workers did not finish")
	}
	for w := 0; w < workers; w++ {
		for i := 0; i < rounds; i++ {
			p := port + 10000 + w*rounds + i
			if GetMCPServer(p, fmt.Sprintf("conc-%d-%d", w, i)) == nil {
				t.Errorf("server %d/%d is missing", w, i)
			}
		}
	}
}

// snapshotToolSchemas copies the registered tool schemas under the lock, as the tools that add schemas on the fly do.
func snapshotToolSchemas() map[string]any {
	lock.RLock()
	defer lock.RUnlock()
	return maps.Clone(ToolSchemas)
}

// ---- notifications and subscriptions of the clients ----

func TestServerTakesTheNotificationsOfTheClient(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "notify-a", true)
	var client *gomcp.Client
	cs := connectConfigured(t, s, nil, func(c *gomcp.Client) { client = c })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// The roots of the client change, and the client tells progress of its own.
	client.AddRoots(&gomcp.Root{Name: "r1", URI: "file:///r1"})
	if err := cs.NotifyProgress(ctx, &gomcp.ProgressNotificationParams{ProgressToken: "tok", Progress: 1, Total: 2, Message: "half"}); err != nil {
		t.Errorf("NotifyProgress = %v", err)
	}
	client.RemoveRoots("file:///r1")
	// A round trip tells that the server is done with what came before.
	if err := cs.Ping(ctx, nil); err != nil {
		t.Errorf("Ping = %v", err)
	}
}

func TestServerTakesSubscriptionsOfTheClient(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "subscribe-a", true)
	mustAdmin(t, port, "POST", "/mcpapi/server/subscribe-a/resources/add", `[{"resource":{"name":"doc","uri":"file:///doc.txt","text":"x"}}]`)
	cs := connectInMemory(t, s, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := cs.Subscribe(ctx, &gomcp.SubscribeParams{URI: "file:///doc.txt"}); err != nil {
		t.Errorf("Subscribe = %v", err)
	}
	if err := cs.Unsubscribe(ctx, &gomcp.UnsubscribeParams{URI: "file:///doc.txt"}); err != nil {
		t.Errorf("Unsubscribe = %v", err)
	}
}

func TestRequestHeadersOfAnExtraThatIsNotThere(t *testing.T) {
	if got := requestHeaders(nil); got != nil {
		t.Errorf("headers = %v, want none", got)
	}
	h := http.Header{"X-A": {"1"}}
	if got := requestHeaders(&gomcp.RequestExtra{Header: h}); got.Get("X-A") != "1" {
		t.Errorf("headers = %v", got)
	}
	// What the handlers of the notifications do with a request that has no extra, and no session to tell.
	s := NewMCPServer(newPayload(isolate(t), "extra-a"))
	mustNotPanic(t, "handling notifications that have no extra", func() {
		s.onRootsListChanged(context.Background(), &gomcp.RootsListChangedRequest{Params: &gomcp.RootsListChangedParams{}})
		s.onProgressNotification(context.Background(), &gomcp.ProgressNotificationServerRequest{Params: &gomcp.ProgressNotificationParams{}})
	})
	if got := clientInfoOf(nil); got != nil {
		t.Errorf("client info of no session = %v", got)
	}
}
