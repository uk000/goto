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

package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"goto/pkg/global"
	"goto/pkg/server/middleware"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
	stubOnce   sync.Once
	routesOnce sync.Once
	portSeq    atomic.Int32
)

func stubGlobalFuncs() {
	global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsListenerPresent = func(int) bool { return false }
	global.Funcs.IsListenerOpen = func(int) bool { return false }
	global.Funcs.GetListenerID = func(int) string { return "" }
	global.Funcs.GetListenerLabel = func(*http.Request) string { return "" }
	global.Funcs.GetListenerLabelForPort = func(int) string { return "" }
	util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
}

// router returns the router that serves the registry APIs, which are mounted once on the root router of /ai/agents.
func router() *mux.Router {
	routesOnce.Do(func() {
		stubOnce.Do(stubGlobalFuncs)
		setRoutes(nil)
	})
	return middleware.RootRouters["/ai"]
}

// resetRegistry empties the process wide registry, now and when the test is done.
func resetRegistry(t *testing.T) {
	t.Helper()
	TheAgentRegistry.Clear()
	t.Cleanup(TheAgentRegistry.Clear)
}

func newRequest(method, target, body string, port int) *http.Request {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, port))
	_, req, _ = util.WithRequestStore(req, nil)
	return req
}

func call(t *testing.T, method, target, body string, port int, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
	req := newRequest(method, target, body, port)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rr := httptest.NewRecorder()
	router().ServeHTTP(rr, req)
	return rr
}

func nextPort() int { return int(42000 + portSeq.Add(1)) }

func agentsJSON(names ...string) string {
	items := []string{}
	for _, n := range names {
		items = append(items, fmt.Sprintf(`{"card":{"name":%q,"description":"d","url":"http://127.0.0.1:1","version":"1"}}`, n))
	}
	return "[" + strings.Join(items, ",") + "]"
}

func TestAddAgentsAPI(t *testing.T) {
	resetRegistry(t)
	port := nextPort()
	rr := call(t, "POST", "/ai/agents/add", agentsJSON("api-one", "api-two"), port)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rr.Code, rr.Body.String())
	}
	if got, want := rr.Body.String(), "Added Agents: [api-one api-two]\n"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	for _, name := range []string{"api-one", "api-two"} {
		agents := TheAgentRegistry.GetAgents(name)
		if len(agents) != 1 || agents[port] == nil || agents[port].Card.Name != name {
			t.Errorf("agents of %s = %+v, want the one of the port %d", name, agents, port)
		}
	}
	// The same agents on another port are added next to the first ones.
	other := nextPort()
	rr = call(t, "POST", "/ai/agents/add", agentsJSON("api-one"), other)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rr.Code, rr.Body.String())
	}
	if agents := TheAgentRegistry.GetAgents("api-one"); len(agents) != 2 || agents[port] == nil || agents[other] == nil {
		t.Errorf("agents of api-one = %+v, want the ones of both ports", agents)
	}
}

func TestAddAgentsAPIInvalidPayloads(t *testing.T) {
	resetRegistry(t)
	cases := []struct{ name, body, wantPrefix string }{
		{"not json", `[{`, "Failed to parse payload with error ["},
		{"not a list", `{"card":{"name":"x"}}`, "Failed to parse payload with error ["},
		{"empty list", `[]`, "Failed to add agents, no agent cards in the payload"},
		{"empty body", ``, "Failed to parse payload with error ["},
		{"null", `null`, "Failed to add agents, no agent cards in the payload"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := call(t, "POST", "/ai/agents/add", c.body, nextPort())
			if rr.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rr.Code)
			}
			if got := rr.Body.String(); !strings.HasPrefix(got, c.wantPrefix) {
				t.Errorf("body = %q, want prefix %q", got, c.wantPrefix)
			}
		})
	}
	if got := len(agentNames(TheAgentRegistry)); got != 0 {
		t.Errorf("registry = %v, want nothing registered by the failed requests", agentNames(TheAgentRegistry))
	}
}

func TestClearAgentsAPI(t *testing.T) {
	resetRegistry(t)
	call(t, "POST", "/ai/agents/add", agentsJSON("clear-a", "clear-b", "clear-c"), nextPort())
	rr := call(t, "POST", "/ai/agents/clear", "", nextPort())
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if got, want := rr.Body.String(), "3 Agents removed from registry\n"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if got := agentNames(TheAgentRegistry); len(got) != 0 {
		t.Errorf("registry = %v, want it empty", got)
	}
	rr = call(t, "POST", "/ai/agents/clear", "", nextPort())
	if got, want := rr.Body.String(), "0 Agents removed from registry\n"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestAgentsAPIMethods(t *testing.T) {
	resetRegistry(t)
	for _, c := range []struct{ method, path string }{
		{"GET", "/ai/agents/add"},
		{"GET", "/ai/agents/clear"},
		{"POST", "/ai/agents"},
		{"DELETE", "/ai/agents"},
	} {
		rr := call(t, c.method, c.path, "", nextPort())
		if rr.Code != http.StatusMethodNotAllowed && rr.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 405 or 404", c.method, c.path, rr.Code)
		}
	}
}

func TestGetAgentsAPI(t *testing.T) {
	resetRegistry(t)
	port := nextPort()
	rr := call(t, "GET", "/ai/agents", "", port)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if got := strings.TrimSpace(rr.Body.String()); got != "{}" {
		t.Errorf("body of an empty registry = %q, want {}", got)
	}
	call(t, "POST", "/ai/agents/add", agentsJSON("list-a", "list-b"), port)
	rr = call(t, "GET", "/ai/agents", "", port)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	got := map[string]map[string]struct{ Card struct{ Name string } }{}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not JSON: %v\n%s", err, rr.Body.String())
	}
	if len(got) != 2 || got["list-a"][fmt.Sprint(port)].Card.Name != "list-a" || got["list-b"][fmt.Sprint(port)].Card.Name != "list-b" {
		t.Errorf("agents = %+v, want the two agents by name and port", got)
	}
	rr = call(t, "GET", "/ai/agents", "", port, "Accept", "application/yaml")
	if body := rr.Body.String(); rr.Code != http.StatusOK || !strings.Contains(body, "list-a") || json.Valid(rr.Body.Bytes()) {
		t.Errorf("YAML response = %d %q", rr.Code, body)
	}
}
