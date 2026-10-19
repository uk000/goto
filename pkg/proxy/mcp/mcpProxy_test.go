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

package mcpproxy

import (
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

	"goto/pkg/global"
	"goto/pkg/server/response/status"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

// The MCP proxy package decides whether a request is to be proxied and keeps the proxy targets and their trackers. It
// has no forwarding of its own: the tests check the decision and what it leaves in the request store for the server.

var (
	setupOnce sync.Once
	// Proxies and statuses are kept per port, so every test uses a port of its own.
	portSeq atomic.Int32
)

func setupGlobals() {
	setupOnce.Do(func() {
		global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
		global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
		global.Funcs.IsListenerPresent = func(int) bool { return false }
		global.Funcs.IsListenerOpen = func(int) bool { return false }
		global.Funcs.GetListenerID = func(int) string { return "" }
		global.Funcs.GetListenerLabel = func(*http.Request) string { return "test" }
		global.Funcs.GetListenerLabelForPort = func(int) string { return "test" }
		util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
		Middleware.SetRoutes(mux.NewRouter())
	})
}

type env struct {
	t     *testing.T
	port  int
	proxy *MCPProxy
}

func newEnv(t *testing.T) *env {
	t.Helper()
	setupGlobals()
	port := int(43000 + portSeq.Add(1))
	e := &env{t: t, port: port, proxy: GetMCPProxyForPort(port)}
	t.Cleanup(func() {
		mcpProxyLock.Lock()
		delete(mcpProxyByPort, port)
		mcpProxyLock.Unlock()
		status.TheStatusManager.Clear(port, "", "")
	})
	return e
}

func callBody(tool string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":{"a":1}}}`, tool)
}

const listBody = `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`

// request builds an MCP request the way the middleware of the server does: marked as MCP, with a body that can be read again.
func (e *env) request(body string) *http.Request {
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, e.port))
	_, req, rs := util.WithRequestStore(req, nil)
	rs.IsMCP = true
	req.Body = util.CreateOrGetReReader(req.Body)
	return req
}

// target and targetCount look at the targets of the proxy.
func (e *env) target(tool string) *MCPTarget {
	e.proxy.lock.RLock()
	defer e.proxy.lock.RUnlock()
	return e.proxy.Targets[tool]
}

func (e *env) targetCount() int {
	e.proxy.lock.RLock()
	defer e.proxy.lock.RUnlock()
	return len(e.proxy.Targets)
}

// headersOf gives the headers of a target as they are reported.
func headersOf(target *MCPTarget) map[string]string {
	b, err := json.Marshal(target)
	if err != nil {
		return nil
	}
	var got struct {
		Headers map[string]string `json:"headers"`
	}
	json.Unmarshal(b, &got)
	return got.Headers
}

func (e *env) setup(server, endpoint, fromTool, toTool string) {
	e.t.Helper()
	if err := e.proxy.SetupMCPProxy(server, endpoint, fromTool, toTool, nil); err != nil {
		e.t.Fatalf("SetupMCPProxy(%s, %s, %s, %s): %v", server, endpoint, fromTool, toTool, err)
	}
}

func bodyOf(t *testing.T, r *http.Request) string {
	t.Helper()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func matchedTargets(t *testing.T, r *http.Request) map[string]*MatchedTarget {
	t.Helper()
	m, ok := util.GetRequestStore(r).ProxyTargets.(map[string]*MatchedTarget)
	if !ok {
		t.Fatalf("proxy targets in the request store = %T %v", util.GetRequestStore(r).ProxyTargets, util.GetRequestStore(r).ProxyTargets)
	}
	return m
}

// ---------------------------------------------------------------------------------------------------------------
// The decision to proxy

func TestWillProxyMCPForCallOfToolWithTarget(t *testing.T) {
	e := newEnv(t)
	e.setup("srv", "http://upstream:9000/mcp", "calc", "remote-calc")
	req := e.request(callBody("calc"))
	if !WillProxyMCP(e.port, req) {
		t.Fatal("a call of a tool that has a proxy target is not proxied")
	}
	rs := util.GetRequestStore(req)
	if !rs.ProxiedRequest {
		t.Error("the request is not marked as proxied")
	}
	m := matchedTargets(t, req)
	if len(m) != 1 || m["calc"] == nil || m["calc"].matchedTool != "calc" || m["calc"].target.Name != "srv" || m["calc"].target.Endpoint != "http://upstream:9000/mcp" {
		t.Errorf("matched targets = %+v", m)
	}
	if got := m["calc"].target.Tools["calc"]; got != "remote-calc" {
		t.Errorf("tool mapping = %q, want remote-calc", got)
	}
}

func TestWillProxyMCPLeavesBodyReadable(t *testing.T) {
	e := newEnv(t)
	e.setup("srv", "http://upstream:9000/mcp", "calc", "calc")
	for name, tc := range map[string]struct {
		body string
		want bool
	}{
		"call of a proxied tool":      {callBody("calc"), true},
		"call of another tool":        {callBody("other"), false},
		"call without a tool name":    {`{"method":"tools/call","params":{}}`, false},
		"list of tools":               {listBody, false},
		"not json but a tools/call":   {`tools/call`, false},
		"empty body":                  {``, false},
		"initialize":                  {`{"jsonrpc":"2.0","id":0,"method":"initialize"}`, false},
		"tool call as a notification": {`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"calc"}}`, true},
	} {
		t.Run(name, func(t *testing.T) {
			req := e.request(tc.body)
			if got := WillProxyMCP(e.port, req); got != tc.want {
				t.Errorf("WillProxyMCP = %v, want %v", got, tc.want)
			}
			// Whoever serves the request next reads the whole body, and can read it again.
			for i := 0; i < 2; i++ {
				if got := bodyOf(t, req); got != tc.body {
					t.Errorf("body read %d = %q, want %q", i+1, got, tc.body)
				}
				req.Body.(*util.ReReader).Rewind()
			}
			if got := util.GetRequestStore(req).ProxiedRequest; got != tc.want {
				t.Errorf("ProxiedRequest = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWillProxyMCPTargetForAllTools(t *testing.T) {
	e := newEnv(t)
	e.setup("any", "http://upstream/mcp", "*", "")
	e.setup("calc-srv", "http://calc/mcp", "calc", "calc")
	req := e.request(callBody("whatever"))
	if !WillProxyMCP(e.port, req) {
		t.Fatal("a call of a tool without a target of its own is not proxied to the target for all tools")
	}
	if m := matchedTargets(t, req); m["whatever"] == nil || m["whatever"].target.Name != "any" {
		t.Errorf("matched targets = %+v", m)
	}
	// The target of the tool is preferred to the one for all tools.
	req = e.request(callBody("calc"))
	if !WillProxyMCP(e.port, req) {
		t.Fatal("a call of calc is not proxied")
	}
	if m := matchedTargets(t, req); m["calc"].target.Name != "calc-srv" {
		t.Errorf("matched target = %s, want calc-srv", m["calc"].target.Name)
	}
}

func TestWillProxyMCPNeedsAnEnabledProxyWithTargets(t *testing.T) {
	e := newEnv(t)
	if WillProxyMCP(e.port, e.request(callBody("calc"))) {
		t.Error("a proxy without targets proxies")
	}
	e.setup("srv", "http://upstream/mcp", "calc", "calc")
	e.proxy.lock.Lock()
	e.proxy.Enabled = false
	e.proxy.lock.Unlock()
	if WillProxyMCP(e.port, e.request(callBody("calc"))) {
		t.Error("a disabled proxy proxies")
	}
	e.proxy.lock.Lock()
	e.proxy.Enabled = true
	e.proxy.lock.Unlock()
	if !WillProxyMCP(e.port, e.request(callBody("calc"))) {
		t.Error("an enabled proxy with a target does not proxy")
	}
	e.proxy.Init()
	if WillProxyMCP(e.port, e.request(callBody("calc"))) {
		t.Error("a proxy proxies after its targets were cleared")
	}
}

func TestWillProxyMCPOnlyForMCPRequestsThatAreNotAdminRequests(t *testing.T) {
	e := newEnv(t)
	e.setup("srv", "http://upstream/mcp", "calc", "calc")
	req := e.request(callBody("calc"))
	util.GetRequestStore(req).IsMCP = false
	if WillProxyMCP(e.port, req) {
		t.Error("a request that is not an MCP request is proxied")
	}
	req = e.request(callBody("calc"))
	util.GetRequestStore(req).IsAdminRequest = true
	if WillProxyMCP(e.port, req) {
		t.Error("an admin request is proxied")
	}
	// The body can only be looked into if it is kept.
	req = e.request(callBody("calc"))
	req.Body = io.NopCloser(strings.NewReader(callBody("calc")))
	if WillProxyMCP(e.port, req) {
		t.Error("a request with a body that can't be read again is proxied")
	}
	if got := bodyOf(t, req); got != callBody("calc") {
		t.Errorf("body = %q, want it left alone", got)
	}
}

func TestWillProxyMCPIsPerPort(t *testing.T) {
	e1, e2 := newEnv(t), newEnv(t)
	e1.setup("srv", "http://upstream/mcp", "calc", "calc")
	if !WillProxyMCP(e1.port, e1.request(callBody("calc"))) {
		t.Error("the port of the target does not proxy")
	}
	if WillProxyMCP(e2.port, e2.request(callBody("calc"))) {
		t.Error("another port proxies")
	}
}

// ---------------------------------------------------------------------------------------------------------------
// Forced statuses: the request is answered with the status instead of being proxied

// applyForcedStatus counts the status the way the status middleware does when it applies it to a response.
func applyForcedStatus(e *env, r *http.Request) {
	status.TheStatusManager.GetStatusFor(e.port, r.RequestURI, r.Header, r.Method)
}

func TestWillProxyMCPNotWhileUnlimitedStatusIsForced(t *testing.T) {
	e := newEnv(t)
	e.setup("srv", "http://upstream/mcp", "calc", "calc")
	status.TheStatusManager.SetStatusFor(e.port, "", "", "", "", nil, []int{503}, -1, true)
	for i := 0; i < 5; i++ {
		req := e.request(callBody("calc"))
		if WillProxyMCP(e.port, req) {
			t.Fatalf("request %d is proxied while a status is forced", i)
		}
		if util.GetRequestStore(req).ProxiedRequest || util.GetRequestStore(req).ProxyTargets != nil {
			t.Fatalf("request %d has proxy state in its store", i)
		}
		if got := bodyOf(t, req); got != callBody("calc") {
			t.Fatalf("body = %q", got)
		}
		applyForcedStatus(e, req)
	}
	// An unlimited status is never used up.
	if WillProxyMCP(e.port, e.request(callBody("calc"))) {
		t.Fatal("request is proxied after an unlimited status was applied")
	}
	status.TheStatusManager.Clear(e.port, "", "")
	if !WillProxyMCP(e.port, e.request(callBody("calc"))) {
		t.Error("request is not proxied after the status was cleared")
	}
}

func TestWillProxyMCPNotWhileLimitedStatusIsForcedAndAgainWhenUsedUp(t *testing.T) {
	e := newEnv(t)
	e.setup("srv", "http://upstream/mcp", "calc", "calc")
	status.TheStatusManager.SetStatusFor(e.port, "", "", "", "", nil, []int{503}, 2, true)
	for i := 0; i < 2; i++ {
		req := e.request(callBody("calc"))
		if WillProxyMCP(e.port, req) {
			t.Fatalf("request %d is proxied while a status is forced", i)
		}
		applyForcedStatus(e, req)
	}
	if !WillProxyMCP(e.port, e.request(callBody("calc"))) {
		t.Fatal("request is not proxied after the limited status was used up")
	}
	if !WillProxyMCP(e.port, e.request(callBody("calc"))) {
		t.Fatal("request is not proxied again")
	}
}

func TestWillProxyMCPDecisionDoesNotUseUpLimitedStatus(t *testing.T) {
	e := newEnv(t)
	e.setup("srv", "http://upstream/mcp", "calc", "calc")
	status.TheStatusManager.SetStatusFor(e.port, "", "", "", "", nil, []int{503}, 1, true)
	for i := 0; i < 10; i++ {
		if WillProxyMCP(e.port, e.request(callBody("calc"))) {
			t.Fatalf("request %d is proxied", i)
		}
	}
	if st, remaining := status.TheStatusManager.PeekStatusFor(e.port, "/mcp", nil, "POST"); st != 503 || remaining != 1 {
		t.Errorf("status = %d remaining = %d, want 503 with the one count left", st, remaining)
	}
}

func TestWillProxyMCPForcedStatusOfOtherURIDoesNotStopProxying(t *testing.T) {
	e := newEnv(t)
	e.setup("srv", "http://upstream/mcp", "calc", "calc")
	status.TheStatusManager.SetStatusFor(e.port, "/other", "", "", "", nil, []int{503}, -1, true)
	if !WillProxyMCP(e.port, e.request(callBody("calc"))) {
		t.Error("a status that is forced for another URI stops the proxy")
	}
}

// ---------------------------------------------------------------------------------------------------------------
// Proxy targets

func TestSetupMCPProxyValidatesAndStoresTargets(t *testing.T) {
	e := newEnv(t)
	for _, tc := range []struct{ server, endpoint string }{{"", "http://e"}, {"s", ""}, {"", ""}} {
		if err := e.proxy.SetupMCPProxy(tc.server, tc.endpoint, "calc", "calc", nil); err == nil {
			t.Errorf("SetupMCPProxy(%q, %q) = nil, want an error", tc.server, tc.endpoint)
		}
	}
	if e.targetCount() > 0 {
		t.Fatalf("targets = %v after invalid setups", e.proxy.Targets)
	}
	headers := map[string]string{"X-A": "1"}
	if err := e.proxy.SetupMCPProxy("s", "http://e/mcp", "calc", "remote", headers); err != nil {
		t.Fatal(err)
	}
	headers["X-A"] = "changed"
	target := e.target("calc")
	if target == nil || target.Name != "s" || target.Endpoint != "http://e/mcp" || target.Tools["calc"] != "remote" ||
		target.ActiveSessions == nil || target.PastSessions == nil || headersOf(target)["X-A"] != "1" {
		t.Errorf("target = %+v", target)
	}
	// A setup for the same tool replaces the target of the tool.
	e.setup("s2", "http://e2/mcp", "calc", "calc")
	if got := e.target("calc"); got.Name != "s2" || len(e.proxy.Targets) != 1 {
		t.Errorf("target = %+v among %v, want the new one only", got, e.proxy.Targets)
	}
}

func TestSetupMCPProxyWithoutToolIsForAllTools(t *testing.T) {
	e := newEnv(t)
	e.setup("s", "http://e/mcp", "", "")
	if e.proxy.Targets["*"] == nil || e.proxy.Targets[""] != nil {
		t.Fatalf("targets = %v, want the target to be the one for all tools", e.proxy.Targets)
	}
	if !WillProxyMCP(e.port, e.request(callBody("anything"))) {
		t.Error("a call of any tool is not proxied")
	}
}

func TestRemoveProxyRemovesTargetsOfToolAndServer(t *testing.T) {
	e := newEnv(t)
	e.setup("srv-a", "http://e/mcp", "t1", "t1")
	e.setup("srv-a", "http://e/mcp", "t2", "t2")
	e.setup("srv-b", "http://e/mcp", "t3", "t3")
	e.proxy.RemoveProxy("t3")
	if e.target("t3") != nil || len(e.proxy.Targets) != 2 {
		t.Errorf("targets = %v after removing the tool t3", e.proxy.Targets)
	}
	e.proxy.RemoveProxy("srv-a")
	if len(e.proxy.Targets) != 0 {
		t.Errorf("targets = %v after removing the server srv-a", e.proxy.Targets)
	}
	e.proxy.RemoveProxy("unknown")
}

func TestInitClearsTargetsAndTracker(t *testing.T) {
	e := newEnv(t)
	e.setup("s", "http://e/mcp", "t", "t")
	e.proxy.MCPTracker.IncrementConnCounts("up")
	old := e.proxy.MCPTracker
	e.proxy.Init()
	if (e.targetCount() > 0) || e.proxy.MCPTracker == old || e.proxy.MCPTracker.ConnCount != 0 {
		t.Errorf("proxy after Init = %+v", e.proxy)
	}
}

func TestNewProxyIsEnabledAndEmpty(t *testing.T) {
	p := newMCPProxy(7)
	if p.Port != 7 || !p.Enabled || len(p.Targets) != 0 || p.MCPTracker == nil || p.MCPTracker.HTTPProxyTracker == nil {
		t.Errorf("new proxy = %+v", p)
	}
}

func TestGetMCPProxyForPortReturnsOneProxyPerPort(t *testing.T) {
	setupGlobals()
	const ports, callers = 100, 16
	base := int(52000 + portSeq.Add(1)*ports)
	t.Cleanup(func() {
		mcpProxyLock.Lock()
		for p := 0; p < ports; p++ {
			delete(mcpProxyByPort, base+p)
		}
		mcpProxyLock.Unlock()
	})
	got := make([][callers]*MCPProxy, ports)
	for p := 0; p < ports; p++ {
		start := make(chan struct{})
		var wg sync.WaitGroup
		for c := 0; c < callers; c++ {
			wg.Add(1)
			go func(p, c int) {
				defer wg.Done()
				<-start
				got[p][c] = GetMCPProxyForPort(base + p)
			}(p, c)
		}
		close(start)
		wg.Wait()
	}
	for p := 0; p < ports; p++ {
		for c := 1; c < callers; c++ {
			if got[p][c] != got[p][0] {
				t.Fatalf("port %d: callers got different proxies", base+p)
			}
		}
	}
}

func TestProxyIsReportedAsJSON(t *testing.T) {
	e := newEnv(t)
	if err := e.proxy.SetupMCPProxy("s", "http://e/mcp", "calc", "calc", map[string]string{"X-A": "1"}); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(e.proxy)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Port    int  `json:"port"`
		Enabled bool `json:"enabled"`
		Targets map[string]struct {
			Name     string            `json:"name"`
			Endpoint string            `json:"endpoint"`
			Headers  map[string]string `json:"headers"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	if got.Port != e.port || !got.Enabled || got.Targets["calc"].Name != "s" || got.Targets["calc"].Endpoint != "http://e/mcp" || got.Targets["calc"].Headers["X-A"] != "1" {
		t.Errorf("reported proxy = %s", b)
	}
}

// Decisions are made on requests while targets are set up and removed.
func TestWillProxyMCPConcurrentWithTargetChanges(t *testing.T) {
	e := newEnv(t)
	e.setup("stable", "http://e/mcp", "stable", "stable")
	const rounds = 60
	reqs := make([]*http.Request, rounds)
	for i := range reqs {
		reqs[i] = e.request(callBody("stable"))
	}
	var wg sync.WaitGroup
	for w := 0; w < 3; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			tool := fmt.Sprintf("dyn%d", w)
			for i := 0; i < rounds; i++ {
				e.proxy.SetupMCPProxy(tool, "http://e/mcp", tool, tool, nil)
				e.proxy.RemoveProxy(tool)
			}
		}(w)
	}
	proxied := 0
	for _, req := range reqs {
		if WillProxyMCP(e.port, req) {
			proxied++
		}
	}
	wg.Wait()
	if proxied != rounds {
		t.Errorf("proxied %d requests, want %d", proxied, rounds)
	}
}
