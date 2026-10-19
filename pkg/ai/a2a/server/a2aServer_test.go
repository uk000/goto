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

package a2aserver

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	a2aclient "goto/pkg/ai/a2a/client"
	"goto/pkg/ai/a2a/model"
	mcpclient "goto/pkg/ai/mcp/client"
	"goto/pkg/ai/registry"
	"goto/pkg/global"
	"goto/pkg/server/intercept"
	"goto/pkg/server/middleware"
	"goto/pkg/util"

	"github.com/gorilla/mux"
	trpcserver "trpc.group/trpc-go/trpc-a2a-go/server"
)

// mcpToolCall is the call of a tool, which is the spec of a tool delegate.
type mcpToolCall = mcpclient.ToolCall

// callTimeout is the deadline of every call to an agent in these tests.
const callTimeout = 20 * time.Second

var (
	stubOnce  sync.Once
	routeOnce sync.Once
	nameSeq   atomic.Int32
)

// stubGlobalFuncs fills the hooks that the server wires up at startup. The request store of every request uses them.
func stubGlobalFuncs() {
	global.Funcs.GetPeers = func(string, *http.Request) map[string]string { return nil }
	global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsListenerPresent = func(int) bool { return false }
	global.Funcs.IsListenerOpen = func(int) bool { return false }
	global.Funcs.GetListenerID = func(int) string { return "" }
	global.Funcs.GetListenerLabel = func(r *http.Request) string { return fmt.Sprintf("goto-%d", util.GetListenerPortNum(r)) }
	global.Funcs.GetListenerLabelForPort = func(port int) string { return fmt.Sprintf("goto-%d", port) }
	global.Funcs.IsListenerTLS = func(int) bool { return false }
	global.Funcs.GetCertInfo = func(int, string) (string, string) { return "", "" }
	global.Funcs.CloseConnectionsForPort = func(int) {}
	global.Funcs.StoreEventInCurrentLocker = func(interface{}) {}
	util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
}

func setup(t *testing.T) {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
}

func uniqueName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, nameSeq.Add(1))
}

func ptr[T any](v T) *T { return &v }

func ctxWithTimeout(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	t.Cleanup(cancel)
	return ctx
}

// adminRouter returns the router that serves /a2a, the admin APIs of the agents.
func adminRouter(t *testing.T) *mux.Router {
	t.Helper()
	setup(t)
	routeOnce.Do(func() { Middleware.SetRoutes(nil) })
	return middleware.RootRouters["/a2a"]
}

// callRouter returns the router that serves /agent/{agent}.
func callRouter(t *testing.T) *mux.Router {
	t.Helper()
	setup(t)
	routeOnce.Do(func() { Middleware.SetRoutes(nil) })
	return middleware.RootRouters["/agent"]
}

// testServer is a listener of the server, which serves the admin APIs and the agents the way the server does.
type testServer struct {
	t    *testing.T
	srv  *httptest.Server
	port int
	URL  string
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	setup(t)
	// An agent stores the result of every session in the temp dir.
	t.Setenv("TMPDIR", t.TempDir())
	adminRouter(t)
	ts := &testServer{t: t}
	ts.srv = httptest.NewUnstartedServer(nil)
	ts.port = ts.srv.Listener.Addr().(*net.TCPAddr).Port
	ts.URL = fmt.Sprintf("http://%s", ts.srv.Listener.Addr().String())
	agents := intercept.IntereceptMiddleware(nil, nil)(AgentsHandler())
	ts.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, r, _ = util.WithRequestStore(r, w)
		if strings.HasPrefix(r.RequestURI, "/a2a") {
			middleware.RootRouters["/a2a"].ServeHTTP(w, r)
		} else {
			agents.ServeHTTP(w, r)
		}
	})
	ts.srv.Start()
	t.Cleanup(func() {
		// An open stream keeps Close waiting, so the connections are closed first.
		ts.srv.CloseClientConnections()
		ts.srv.Close()
		resetPort(ts.port)
	})
	return ts
}

// resetPort forgets what the tests made on the port.
func resetPort(port int) {
	lock.Lock()
	delete(PortServers, port)
	lock.Unlock()
	statusManager.Clear(port, "", "")
}

func (ts *testServer) agentURL(name string) string {
	return fmt.Sprintf("%s/agent/%s/", ts.URL, name)
}

// card builds the card of an agent that is served by the test server.
func (ts *testServer) card(name string, streaming *bool) *trpcserver.AgentCard {
	return &trpcserver.AgentCard{
		Name:               name,
		Description:        "test agent " + name,
		URL:                ts.agentURL(name),
		Version:            "1.0",
		Capabilities:       trpcserver.AgentCapabilities{Streaming: streaming},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
		Skills:             []trpcserver.AgentSkill{{ID: "echo", Name: "echo", Tags: []string{"echo"}}},
	}
}

func (ts *testServer) newAgent(name string, streaming bool, behavior *model.AgentBehavior, config *model.AgentConfig) *model.Agent {
	return &model.Agent{Card: ts.card(name, ptr(streaming)), Behavior: behavior, Config: config}
}

// addAgent adds an agent to the server of the port, as the config of the server does.
func (ts *testServer) addAgent(agent *model.Agent) *model.Agent {
	ts.t.Helper()
	if err := GetOrAddServer(ts.port).AddAgent(agent); err != nil {
		ts.t.Fatalf("AddAgent(%s) failed: %v", agent.Card.Name, err)
	}
	name := agent.Card.Name
	ts.t.Cleanup(func() { registry.TheAgentRegistry.RemoveAgent(name) })
	return agent
}

// add adds an agent with the behavior, and returns its name and URL.
func (ts *testServer) add(prefix string, streaming bool, behavior *model.AgentBehavior, config *model.AgentConfig) (string, *model.Agent) {
	ts.t.Helper()
	name := uniqueName(prefix)
	return name, ts.addAgent(ts.newAgent(name, streaming, behavior, config))
}

// reply is the result of a call to an agent.
type reply struct {
	Result *a2aclient.A2AResult
	Texts  []string
	Err    error
}

func (r *reply) has(sub string) bool {
	for _, s := range r.Texts {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// call calls the agent with the a2a client, the way another agent or the client API does.
func (ts *testServer) call(name, message string, args map[string]any) *reply {
	ts.t.Helper()
	return callAgent(ts.t, ts.agentURL(name), name, message, args)
}

func callAgent(t *testing.T, agentURL, name, message string, args map[string]any) *reply {
	t.Helper()
	call := &a2aclient.AgentCall{Name: name, CardURL: agentURL, Message: message, Args: args}
	call.Prepare()
	var mu sync.Mutex
	r := &reply{}
	result, err := a2aclient.CallAgent(ctxWithTimeout(t), 1, call, func(id, out string, data any) {
		mu.Lock()
		defer mu.Unlock()
		if out != "" {
			r.Texts = append(r.Texts, out)
		}
	}, nil)
	r.Result, r.Err = result, err
	return r
}

// rawResponse is a response of the server to a request made without the a2a client.
type rawResponse struct {
	Code   int
	Header http.Header
	Body   string
}

// try makes a request to the server. It can be called from any goroutine.
func (ts *testServer) try(method, path, body string, headers ...string) (*rawResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, ts.URL+path, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Add(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return &rawResponse{Code: resp.StatusCode, Header: resp.Header, Body: string(b)}, err
}

// request makes a request to the server, and fails the test if it can't. It must be called by the test goroutine.
func (ts *testServer) request(method, path, body string, headers ...string) *rawResponse {
	ts.t.Helper()
	resp, err := ts.try(method, path, body, headers...)
	if err != nil {
		ts.t.Fatalf("%s %s failed: %v", method, path, err)
	}
	return resp
}

// sendMessageBody is the JSON-RPC request that sends a message to an agent.
func sendMessageBody(text string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"message/send","params":{"message":{"kind":"message","messageId":"m-1","role":"user","parts":[{"kind":"text","text":%q}]}}}`, text)
}

// postMessage sends a message to the agent with a plain POST.
func (ts *testServer) postMessage(agentPath, text string, headers ...string) *rawResponse {
	ts.t.Helper()
	return ts.request("POST", agentPath, sendMessageBody(text), headers...)
}

// getCard fetches the card of the agent with a plain GET.
func (ts *testServer) getCard(name string) *rawResponse {
	ts.t.Helper()
	return ts.request("GET", fmt.Sprintf("/agent/%s/.well-known/agent.json", name), "")
}

// recordRequest serves a request of the given method to the handler, with a request store, as the server does.
func recordRequest(t *testing.T, h http.Handler, port int, method, target, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	setup(t)
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, port))
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Add(headers[i], headers[i+1])
	}
	_, req, _ = util.WithRequestStore(req, nil)
	ctx, cancel := context.WithTimeout(req.Context(), callTimeout)
	defer cancel()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req.WithContext(ctx))
	return rr
}

func TestGetOrAddServer(t *testing.T) {
	setup(t)
	port := 41001
	t.Cleanup(func() { resetPort(port) })
	s := GetOrAddServer(port)
	if s == nil || s.Port != port || !s.Enabled || s.Agents == nil || s.ID != "goto-41001" {
		t.Fatalf("server = %+v", s)
	}
	if GetOrAddServer(port) != s {
		t.Error("GetOrAddServer must return the server of the port that exists")
	}
	if GetOrAddServer(port+1) == s {
		t.Error("a port must have a server of its own")
	}
	t.Cleanup(func() { resetPort(port + 1) })
}

func TestServerEnableDisable(t *testing.T) {
	setup(t)
	s := newA2AServer(41002)
	s.Disable()
	if s.Enabled {
		t.Error("Disable must disable the server")
	}
	s.Enable()
	if !s.Enabled {
		t.Error("Enable must enable the server")
	}
}

func TestAddAgentPreparesAgent(t *testing.T) {
	ts := newTestServer(t)
	name := uniqueName("prep")
	agent := ts.newAgent(name, true, nil, nil)
	if err := GetOrAddServer(ts.port).AddAgent(agent); err != nil {
		t.Fatalf("AddAgent failed: %v", err)
	}
	if agent.ID != fmt.Sprintf("%s(goto-%d)", name, ts.port) {
		t.Errorf("ID = %q, want name(server)", agent.ID)
	}
	if agent.Port != ts.port || !agent.Streaming || agent.URI != "/agent/"+name+"/" {
		t.Errorf("agent = port %d, streaming %v, URI %q", agent.Port, agent.Streaming, agent.URI)
	}
	if agent.Handler == nil || agent.Server == nil || agent.Behavior == nil || agent.Behavior.Impl == nil {
		t.Errorf("agent = %+v, want handler, server and behavior to be set", agent)
	}
	if got := GetAgent(ts.port, name); got != agent {
		t.Errorf("GetAgent = %v, want the agent that was added", got)
	}
	if got := GetAgent(ts.port, "nope"); got != nil {
		t.Errorf("GetAgent of an unknown agent = %v, want nil", got)
	}
}

func TestAddAgentKeepsGivenID(t *testing.T) {
	ts := newTestServer(t)
	agent := ts.newAgent(uniqueName("given-id"), false, nil, nil)
	agent.ID = "my-own-id"
	if err := GetOrAddServer(ts.port).AddAgent(agent); err != nil {
		t.Fatal(err)
	}
	if agent.ID != "my-own-id" {
		t.Errorf("ID = %q, want the one that was given", agent.ID)
	}
}

func TestAddAgentBehaviorSelection(t *testing.T) {
	ts := newTestServer(t)
	tests := []struct {
		name     string
		behavior *model.AgentBehavior
		want     string
	}{
		{"default", nil, "echo"},
		{"empty", &model.AgentBehavior{}, "echo"},
		{"echo", &model.AgentBehavior{Echo: true}, "echo"},
		{"stream", &model.AgentBehavior{Stream: true}, "stream"},
		{"federate", &model.AgentBehavior{Federate: true}, "federate"},
		{"http", &model.AgentBehavior{HTTPProxy: true}, "http"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := ts.newAgent(uniqueName("beh-"+tt.name), false, tt.behavior, nil)
			if err := GetOrAddServer(ts.port).AddAgent(agent); err != nil {
				t.Fatal(err)
			}
			var got string
			switch agent.Behavior.Impl.(type) {
			case *AgentBehaviorEcho:
				got = "echo"
			case *AgentBehaviorStream:
				got = "stream"
			case *AgentBehaviorFederate:
				got = "federate"
			case *AgentBehaviorRemoteHttp:
				got = "http"
			}
			if got != tt.want {
				t.Errorf("behavior = %q (%T), want %q", got, agent.Behavior.Impl, tt.want)
			}
		})
	}
}

func TestAddAgentFailures(t *testing.T) {
	ts := newTestServer(t)
	s := GetOrAddServer(ts.port)
	t.Run("invalid url", func(t *testing.T) {
		agent := ts.newAgent(uniqueName("bad-url"), false, nil, nil)
		agent.Card.URL = "http://[::1"
		if err := s.AddAgent(agent); err == nil {
			t.Error("AddAgent with an invalid URL must fail")
		}
		if s.GetAgent(agent.Card.Name) != nil {
			t.Error("an agent that failed to be added must not be on the server")
		}
	})
}

func TestRemoveAgentAndClear(t *testing.T) {
	ts := newTestServer(t)
	n1, _ := ts.add("rm-1", false, nil, nil)
	n2, _ := ts.add("rm-2", false, nil, nil)
	RemoveAgent(ts.port, n1)
	if GetAgent(ts.port, n1) != nil || GetAgent(ts.port, n2) == nil {
		t.Error("RemoveAgent must remove only the agent")
	}
	RemoveAgent(ts.port, "never-added")
	ClearServer(ts.port)
	if GetAgent(ts.port, n2) != nil {
		t.Error("ClearServer must remove the agents of the port")
	}
	if s := GetOrAddServer(ts.port); s == nil || s.Port != ts.port || !s.Enabled {
		t.Errorf("server after clear = %+v, want an empty server of the port", s)
	}
}

func TestClearAllServers(t *testing.T) {
	ts := newTestServer(t)
	name, _ := ts.add("clear-all", false, nil, nil)
	other := 41011
	t.Cleanup(func() { resetPort(other) })
	GetOrAddServer(other)
	ClearAllServers()
	lock.RLock()
	n := len(PortServers)
	lock.RUnlock()
	if n != 0 || GetAgent(ts.port, name) != nil {
		t.Errorf("servers = %d, agent = %v, want everything cleared", n, GetAgent(ts.port, name))
	}
}

func TestGetAgentNames(t *testing.T) {
	ts := newTestServer(t)
	cfg := &model.AgentConfig{Delegates: &model.DelegateConfig{
		Agents: map[string]*model.DelegateAgentCall{
			"a1": {AgentCall: &a2aclient.AgentCall{Name: "remote-1", CardURL: "localhost:1/"}, Triggers: []string{"a"}},
			"a2": {AgentCall: &a2aclient.AgentCall{Name: "remote-off", CardURL: "localhost:2/"}, Disabled: true},
		},
		Tools: map[string]*model.DelegateToolCall{
			"t1": {ToolCall: &mcpToolCall{Tool: "echo-tool", URL: "localhost:3/mcp"}, Triggers: []string{"t"}},
			"t2": {ToolCall: &mcpToolCall{Tool: "tool-off", URL: "localhost:4/mcp"}, Disabled: true},
		},
		HTTP: map[string]*model.DelegateHTTPCall{
			"h1": {HTTPCall: &model.HTTPCall{URL: "localhost:5/x"}},
			"h2": {HTTPCall: &model.HTTPCall{URL: "localhost:6/x"}, Disabled: true},
		},
	}}
	name, _ := ts.add("names", false, &model.AgentBehavior{Federate: true}, cfg)
	plain, _ := ts.add("names-plain", false, nil, nil)

	one := GetAgentNames(ts.port)
	if len(one) != 1 || one[ts.port] == nil {
		t.Fatalf("names of the port = %v", one)
	}
	got := one[ts.port][name]
	if len(got["agents"]) != 1 || got["agents"][0] != "remote-1" {
		t.Errorf("agents = %v, want the one that is enabled", got["agents"])
	}
	if len(got["tools"]) != 1 || got["tools"][0] != "echo-tool" {
		t.Errorf("tools = %v, want the one that is enabled", got["tools"])
	}
	if len(got["http"]) != 1 || got["http"][0] != "localhost:5/x" {
		t.Errorf("http = %v, want the one that is enabled", got["http"])
	}
	if v, found := one[ts.port][plain]; !found || len(v) != 0 {
		t.Errorf("names of an agent without delegates = %v, found %v, want it listed with nothing", v, found)
	}
	all := GetAgentNames(0)
	if all[ts.port] == nil || all[ts.port][name] == nil {
		t.Errorf("names of all ports = %v, want the port of the test", all)
	}
}

func TestGetAgentNamesDelegatesWithoutAgents(t *testing.T) {
	ts := newTestServer(t)
	cfg := &model.AgentConfig{Delegates: &model.DelegateConfig{
		Tools: map[string]*model.DelegateToolCall{"t1": {ToolCall: &mcpToolCall{Tool: "only-tool", URL: "localhost:3/mcp"}}},
	}}
	name, _ := ts.add("names-tools", false, &model.AgentBehavior{Federate: true}, cfg)
	got := GetAgentNames(ts.port)[ts.port][name]
	if len(got["tools"]) != 1 || got["tools"][0] != "only-tool" {
		t.Errorf("names = %v, want the tools of an agent that has no agent delegates", got)
	}
}

func TestServeUnknownAgent(t *testing.T) {
	ts := newTestServer(t)
	rr := recordRequest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := GetOrAddServer(ts.port).Serve("nobody", w, r)
		if err == nil || !strings.Contains(err.Error(), "agent [nobody] not found") {
			t.Errorf("Serve error = %v", err)
		}
	}), ts.port, "POST", "/agent/nobody/", "")
	if rr.Header().Get("Goto-A2A-Agent") != "" {
		t.Errorf("headers = %v, want none for an unknown agent", rr.Header())
	}
}

func TestServeSetsAgentHeaders(t *testing.T) {
	ts := newTestServer(t)
	name, agent := ts.add("hdrs", false, nil, nil)
	resp := ts.getCard(name)
	if resp.Code != http.StatusOK {
		t.Fatalf("card status = %d: %s", resp.Code, resp.Body)
	}
	if resp.Header.Get("Goto-A2A-Server") != fmt.Sprintf("goto-%d", ts.port) || resp.Header.Get("Goto-A2A-Agent") != agent.ID {
		t.Errorf("headers = %v, want the server and the agent", resp.Header)
	}
	if !strings.Contains(resp.Body, `"name":"`+name+`"`) {
		t.Errorf("card = %s", resp.Body)
	}
}

func TestConcurrentServerStateAccess(t *testing.T) {
	ts := newTestServer(t)
	s := GetOrAddServer(ts.port)
	var wg sync.WaitGroup
	var addFailures atomic.Int32
	const workers = 6
	const perWorker = 25
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				agent := ts.newAgent(fmt.Sprintf("conc-%d-%d-%d", ts.port, w, i), false, nil, nil)
				if err := s.AddAgent(agent); err != nil {
					addFailures.Add(1)
					continue
				}
				s.GetAgent(agent.Card.Name)
				GetAgentNames(ts.port)
				GetAgentNames(0)
				GetAgent(ts.port, agent.Card.Name)
				if i%2 == 0 {
					s.RemoveAgent(agent.Card.Name)
				}
			}
		}(w)
	}
	wg.Wait()
	if n := addFailures.Load(); n != 0 {
		t.Errorf("AddAgent failed %d times, want 0", n)
	}
	s.lock.RLock()
	n := len(s.Agents)
	s.lock.RUnlock()
	// i = 0, 2, ..., 24 are removed, which is 13 of the 25 agents of each worker.
	want := workers * (perWorker - (perWorker+1)/2)
	if n != want {
		t.Errorf("agents on the server = %d, want %d", n, want)
	}
}
