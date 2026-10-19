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
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAddServersAPIAddsServersOnThePortOfTheRequest(t *testing.T) {
	port := isolate(t)
	body := mustAdmin(t, port, "POST", "/mcpapi/servers/add",
		fmt.Sprintf(`[{"name":"api-a","version":"1","stateless":true,"enabled":true},{"name":"api-b","version":"2","port":%d}]`, port+500))
	expectBodyContains(t, body, fmt.Sprintf("api-a (port: %d)", port))
	expectBodyContains(t, body, fmt.Sprintf("api-b (port: %d)", port+500))
	if s := GetMCPServer(port, "api-a"); s == nil || !s.Stateless || !s.Enabled || s.Version != "1" {
		t.Errorf("api-a = %+v", s)
	}
	if s := GetMCPServer(port+500, "api-b"); s == nil || s.Enabled || s.Version != "2" {
		t.Errorf("api-b = %+v", s)
	}
}

func TestAddServersAPIRejectsBadPayloads(t *testing.T) {
	port := isolate(t)
	for name, body := range map[string]string{
		"not json":    `[{`,
		"empty array": `[]`,
		"empty body":  ``,
	} {
		rr := admin(port, "POST", "/mcpapi/servers/add", body)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d (%q), want 400", name, rr.Code, rr.Body.String())
		}
	}
	if len(GetPortMCPServers(port).Servers) != 0 {
		t.Error("a rejected payload added servers")
	}
}

func TestGetServersAPI(t *testing.T) {
	port := isolate(t)
	addServer(t, port, "get-a", true)
	addServer(t, port+500, "get-b", false)

	t.Run("servers of the port", func(t *testing.T) {
		body := mustAdmin(t, port, "GET", "/mcpapi/servers", "")
		ps := &struct {
			Port    int                        `json:"port"`
			Servers map[string]json.RawMessage `json:"servers"`
		}{}
		if err := json.Unmarshal([]byte(body), ps); err != nil {
			t.Fatalf("body %q: %v", body, err)
		}
		if ps.Port != port || len(ps.Servers) != 1 || ps.Servers["get-a"] == nil {
			t.Errorf("servers = %+v", ps)
		}
	})
	t.Run("all ports", func(t *testing.T) {
		body := mustAdmin(t, port, "GET", "/mcpapi/servers/all", "")
		all := map[string]json.RawMessage{}
		if err := json.Unmarshal([]byte(body), &all); err != nil {
			t.Fatalf("body %q: %v", body, err)
		}
		if all[fmt.Sprint(port)] == nil || all[fmt.Sprint(port+500)] == nil {
			t.Errorf("ports in the output: %v", mapKeys(all))
		}
	})
	t.Run("names", func(t *testing.T) {
		body := mustAdmin(t, port, "GET", "/mcpapi/servers/names", "")
		names := map[string]map[string][]string{}
		if err := json.Unmarshal([]byte(body), &names); err != nil {
			t.Fatalf("body %q: %v", body, err)
		}
		if len(names[fmt.Sprint(port)]["get-a"]) == 0 {
			t.Errorf("names = %v", names)
		}
	})
	t.Run("one server by name, from any port, any case", func(t *testing.T) {
		body := mustAdmin(t, port, "GET", "/mcpapi/server/GET-B", "")
		expectJSONContains(t, body, `"name":"get-b"`)
	})
	t.Run("unknown server", func(t *testing.T) {
		rr := admin(port, "GET", "/mcpapi/server/missing", "")
		expectCode(t, rr, http.StatusBadRequest)
		expectBodyContains(t, rr.Body.String(), "doesn't exist")
	})
	t.Run("yaml", func(t *testing.T) {
		req := newRequest("GET", "/mcpapi/servers", port, "")
		req.Header.Set("Accept", "application/yaml")
		rr := httptest.NewRecorder()
		adminRouter().ServeHTTP(rr, req)
		expectCode(t, rr, http.StatusOK)
		if strings.HasPrefix(strings.TrimSpace(rr.Body.String()), "{") {
			t.Errorf("body is JSON, not YAML: %q", rr.Body.String())
		}
		expectBodyContains(t, rr.Body.String(), "get-a")
	})
}

func TestStartAndStopAPI(t *testing.T) {
	port := isolate(t)
	a := addServer(t, port, "ss-api-a", true)
	b := addServer(t, port, "ss-api-b", true)

	expectBodyContains(t, mustAdmin(t, port, "POST", "/mcpapi/server/ss-api-a/stop", ""), "Stopped")
	if a.Enabled || !b.Enabled {
		t.Errorf("after stopping a: a=%t b=%t", a.Enabled, b.Enabled)
	}
	expectBodyContains(t, mustAdmin(t, port, "POST", "/mcpapi/servers/stop", ""), "Stopped")
	if a.Enabled || b.Enabled {
		t.Errorf("after stopping all: a=%t b=%t", a.Enabled, b.Enabled)
	}
	expectBodyContains(t, mustAdmin(t, port, "POST", "/mcpapi/server/SS-API-B/start", ""), "Started")
	if a.Enabled || !b.Enabled {
		t.Errorf("after starting b: a=%t b=%t", a.Enabled, b.Enabled)
	}
	expectBodyContains(t, mustAdmin(t, port, "POST", "/mcpapi/servers/start", ""), "Started")
	if !a.Enabled || !b.Enabled {
		t.Errorf("after starting all: a=%t b=%t", a.Enabled, b.Enabled)
	}
}

func TestSetServerRouteAPI(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "route-api", true)
	alias := fmt.Sprintf("/mcp/alias-%d", port)

	expectBodyContains(t, mustAdmin(t, port, "POST", "/mcpapi/server/route-api/route?uri="+alias, ""), alias)
	if _, found := findServerForURI(port, alias); found != s {
		t.Errorf("server of the alias = %v", found)
	}
	if _, found := findServerForURI(port, alias+"/echo"); found != nil {
		// Only the tools that exist get a route.
		t.Errorf("tool route of a tool that doesn't exist found %v", found)
	}
	if _, found := findServerForURI(port, alias+"/serverdetails"); found != s {
		t.Errorf("route of a default tool = %v", found)
	}

	rr := admin(port, "POST", "/mcpapi/server/missing/route?uri=/mcp/x", "")
	expectCode(t, rr, http.StatusBadRequest)
	expectBodyContains(t, rr.Body.String(), "not configured")
}

func TestComponentsAPI(t *testing.T) {
	port := isolate(t)
	addServer(t, port, "comp-api", true)
	addServer(t, port, "comp-api-2", true)

	mustAdmin(t, port, "POST", "/mcpapi/server/comp-api/tools/add", "["+echoToolJSON+"]")
	body := mustAdmin(t, port, "POST", "/mcpapi/server/comp-api/prompts/add", `[{"prompt":{"name":"p1","description":"d"}}]`)
	expectBodyContains(t, body, "p1")
	mustAdmin(t, port, "POST", "/mcpapi/server/comp-api/resources/add", `[{"resource":{"name":"r1","uri":"file:///r1.txt","text":"x"}}]`)
	mustAdmin(t, port, "POST", "/mcpapi/server/comp-api/templates/add", `[{"template":{"name":"t1","uriTemplate":"file:///{n}.txt"}}]`)

	t.Run("components of a server", func(t *testing.T) {
		for kind, want := range map[string]string{"tools": "echo", "prompts": "p1", "resources": "r1", "templates": "t1"} {
			body := mustAdmin(t, port, "GET", "/mcpapi/server/comp-api/"+kind, "")
			expectBodyContains(t, body, want)
		}
	})
	t.Run("one component of a server", func(t *testing.T) {
		body := mustAdmin(t, port, "GET", "/mcpapi/server/comp-api/tools/echo", "")
		expectJSONContains(t, body, `"name":"echo"`)
	})
	t.Run("components of all servers on the port", func(t *testing.T) {
		body := mustAdmin(t, port, "GET", "/mcpapi/servers/tools?q=servers", "")
		expectBodyContains(t, body, "echo")
	})
	t.Run("components of all servers on all ports", func(t *testing.T) {
		body := mustAdmin(t, port, "GET", "/mcpapi/servers/prompts", "")
		expectBodyContains(t, body, "p1")
	})
	t.Run("unknown server", func(t *testing.T) {
		expectCode(t, admin(port, "GET", "/mcpapi/server/missing/tools", ""), http.StatusBadRequest)
		expectCode(t, admin(port, "POST", "/mcpapi/server/missing/tools/add", "[]"), http.StatusBadRequest)
	})
	t.Run("invalid component", func(t *testing.T) {
		rr := admin(port, "POST", "/mcpapi/server/comp-api/tools/add", `[{"tool":{"name":"no-schema"}}]`)
		expectCode(t, rr, http.StatusBadRequest)
		expectBodyContains(t, rr.Body.String(), "Failed to add")
		rr = admin(port, "POST", "/mcpapi/server/comp-api/prompts/add", `not json`)
		expectCode(t, rr, http.StatusBadRequest)
	})
	t.Run("kind that is not known is not routed", func(t *testing.T) {
		rr := admin(port, "GET", "/mcpapi/server/comp-api/bogus", "")
		if rr.Code != http.StatusNotFound && rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("status = %d", rr.Code)
		}
	})
}

func TestAddToolSchemasAPI(t *testing.T) {
	port := isolate(t)
	name := fmt.Sprintf("api-schema-%d", port)
	t.Cleanup(func() {
		lock.Lock()
		delete(ToolSchemas, name)
		lock.Unlock()
	})
	body := mustAdmin(t, port, "POST", "/mcpapi/schemas/add",
		fmt.Sprintf(`[{"name":%q,"inputSchema":{"type":"object"}}]`, name))
	expectBodyContains(t, body, name)
	if snapshotToolSchemas()[name] == nil {
		t.Error("schema was not added")
	}
	rr := admin(port, "POST", "/mcpapi/schemas/add", `{{`)
	expectBodyContains(t, rr.Body.String(), "Failed to add tool schemas")
}

func TestComponentPayloadAPI(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "pl-api", true)
	addTools(t, port, "pl-api", "["+echoToolJSON+"]")

	expectBodyContains(t, mustAdmin(t, port, "POST", "/mcpapi/server/pl-api/payload/tools/echo", `{"hello":"world"}`), "Set payload")
	if s.Tools["echo"].Response == nil {
		t.Fatal("payload was not set")
	}
	mustAdmin(t, port, "POST", "/mcpapi/server/pl-api/payload/tools/echo/stream/count=3/delay=10ms", `{"hello":"world"}`)
	if r := s.Tools["echo"].Response; r == nil || r.StreamCount != 3 {
		t.Errorf("stream payload = %+v", r)
	}

	rr := admin(port, "POST", "/mcpapi/server/pl-api/payload/tools/missing", `{}`)
	expectCode(t, rr, http.StatusBadRequest)
	expectBodyContains(t, rr.Body.String(), "Failed to set payload")
	expectCode(t, admin(port, "POST", "/mcpapi/server/nope/payload/tools/echo", `{}`), http.StatusBadRequest)
}

func TestCompletionPayloadAPI(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "compl-api", true)
	body := mustAdmin(t, port, "POST", "/mcpapi/server/compl-api/payload/completion?type=ref/prompt", "one")
	expectBodyContains(t, body, "Set completion payload")
	if s.CompletionPayload["ref/prompt"] == nil {
		t.Error("completion payload was not set")
	}
	mustAdmin(t, port, "POST", "/mcpapi/server/compl-api/payload/completion/delay=10ms?type=ref/resource", "two")
	if s.CompletionPayload["ref/resource"] == nil {
		t.Error("completion payload with a delay was not set")
	}
	expectCode(t, admin(port, "POST", "/mcpapi/server/missing/payload/completion?type=x", "a"), http.StatusBadRequest)
}

func TestClearServersAPI(t *testing.T) {
	port := isolate(t)
	a := addServer(t, port, "clr-api-a", true)
	addServer(t, port+500, "clr-api-b", true)

	t.Run("one port", func(t *testing.T) {
		expectBodyContains(t, mustAdmin(t, port, "POST", "/mcpapi/servers/clear", ""), "Cleared all MCP servers on port")
		if len(a.Tools) != 0 {
			t.Errorf("server of the port was not cleared: %d tools", len(a.Tools))
		}
		if GetMCPServer(port+500, "clr-api-b") == nil || len(GetMCPServer(port+500, "clr-api-b").Tools) == 0 {
			t.Error("server of another port was cleared")
		}
	})
	t.Run("unknown server", func(t *testing.T) {
		expectCode(t, admin(port, "POST", "/mcpapi/server/missing/clear", ""), http.StatusBadRequest)
	})
	t.Run("all servers on all ports", func(t *testing.T) {
		expectBodyContains(t, mustAdmin(t, port, "POST", "/mcpapi/servers/clear/all", ""), "Cleared all MCP servers")
		if GetMCPServer(port, "clr-api-a") != nil || GetMCPServer(port+500, "clr-api-b") != nil {
			t.Error("servers survived")
		}
	})
}

func TestCallToolAPI(t *testing.T) {
	port := isolate(t)
	addServer(t, port, "call-api", true)
	addTools(t, port, "call-api", "["+echoToolJSON+"]")

	var body string
	mustNotPanic(t, "calling a tool through the admin API", func() {
		body = mustAdmin(t, port, "POST", "/mcpapi/server/call-api/tool/echo/call", `{"text":"hello there"}`)
	})
	expectBodyContains(t, body, "hello there")
	expectJSONContains(t, body, `"structuredContent"`)
}

func TestCallToolAPIRejectsUnknownServerAndTool(t *testing.T) {
	port := isolate(t)
	addServer(t, port, "call-api-2", true)
	rr := admin(port, "POST", "/mcpapi/server/missing/tool/echo/call", `{}`)
	expectCode(t, rr, http.StatusBadRequest)
	expectBodyContains(t, rr.Body.String(), "not configured")
	rr = admin(port, "POST", "/mcpapi/server/call-api-2/tool/missing/call", `{}`)
	expectCode(t, rr, http.StatusBadRequest)
	expectBodyContains(t, rr.Body.String(), "Tool [missing] not configured")
}

// ---- status API ----

func statusesOf(port int) string {
	b, _ := json.Marshal(StatusManager.Snapshot()[port])
	return string(b)
}

func TestSetStatusAPIConfiguresTheStatusOfTheServerURI(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "st-api", true)
	body := mustAdmin(t, port, "POST", "/mcpapi/server/st-api/status/set/503:2", "")
	expectBodyContains(t, body, "503")

	configs := StatusManager.Snapshot()[port]
	if len(configs) != 1 {
		t.Fatalf("configs = %s", statusesOf(port))
	}
	sc := configs[0]
	if len(sc.Statuses) != 1 || sc.Statuses[0] != 503 || sc.Times != 2 {
		t.Errorf("config = %+v", sc)
	}
	if sc.Match.URIMatch.Exact != strings.ToLower(s.URI) {
		t.Errorf("exact URI = %q, want the URI of the server %q", sc.Match.URIMatch.Exact, s.URI)
	}
	if len(sc.Match.MethodMatch) != 1 || sc.Match.MethodMatch[0] != "POST" {
		t.Errorf("methods = %v, want only POST", sc.Match.MethodMatch)
	}
}

func TestSetStatusAPIForATool(t *testing.T) {
	port := isolate(t)
	addServer(t, port, "st-tool", true)
	addTools(t, port, "st-tool", "["+echoToolJSON+"]")
	mustAdmin(t, port, "POST", "/mcpapi/server/st-tool/tool/echo/status/set/502", "")

	configs := StatusManager.Snapshot()[port]
	if len(configs) != 1 || configs[0].Match.URIMatch.Exact != strings.ToLower(serverURI(port, "st-tool")+"/echo") {
		t.Fatalf("configs = %s", statusesOf(port))
	}
	if configs[0].Times != -1 {
		t.Errorf("times = %d, want unlimited", configs[0].Times)
	}
}

func TestSetStatusAPIRejectsBadRequests(t *testing.T) {
	port := isolate(t)
	addServer(t, port, "st-bad", true)
	for name, target := range map[string]string{
		"invalid status": "/mcpapi/server/st-bad/status/set/abc",
		"unknown server": "/mcpapi/server/missing/status/set/503",
		"unknown tool":   "/mcpapi/server/st-bad/tool/missing/status/set/503",
	} {
		if rr := admin(port, "POST", target, ""); rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d (%q), want 400", name, rr.Code, rr.Body.String())
		}
	}
	if configs := StatusManager.Snapshot()[port]; len(configs) != 0 {
		t.Errorf("a rejected request configured %s", statusesOf(port))
	}
}

func TestSetStatusAPIAgainChangesTheConfigOfTheServer(t *testing.T) {
	port := isolate(t)
	addServer(t, port, "st-again", true)
	mustAdmin(t, port, "POST", "/mcpapi/server/st-again/status/set/503:5", "")
	mustAdmin(t, port, "POST", "/mcpapi/server/st-again/status/set/504", "")
	configs := StatusManager.Snapshot()[port]
	if len(configs) != 1 || configs[0].Statuses[0] != 504 || configs[0].Times != -1 {
		t.Errorf("configs = %s", statusesOf(port))
	}
}

func TestConfigureStatusAPI(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "cfg-api", true)
	body := mustAdmin(t, port, "POST", "/mcpapi/server/cfg-api/status/configure",
		`{"statuses":[502],"times":2,"match":{"headers":[{"header":"x-a","value":"b"}],"methods":["POST"]}}`)
	expectBodyContains(t, body, "502")
	configs := StatusManager.Snapshot()[port]
	if len(configs) != 1 {
		t.Fatalf("configs = %s", statusesOf(port))
	}
	sc := configs[0]
	if sc.Match.URIMatch.Exact != s.URI || sc.Times != 2 || len(sc.Match.HeaderMatches) != 1 {
		t.Errorf("config = %s", statusesOf(port))
	}
}

func TestConfigureStatusAPIRejectsBadConfigs(t *testing.T) {
	port := isolate(t)
	addServer(t, port, "cfg-bad", true)
	for name, body := range map[string]string{
		"not json":    `{`,
		"no statuses": `{"times":2}`,
		"bad regex":   `{"statuses":[500],"match":{"uri":{"regex":"("}}}`,
	} {
		if rr := admin(port, "POST", "/mcpapi/server/cfg-bad/status/configure", body); rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d (%q), want 400", name, rr.Code, rr.Body.String())
		}
	}
	if configs := StatusManager.Snapshot()[port]; len(configs) != 0 {
		t.Errorf("a rejected config was applied: %s", statusesOf(port))
	}
}

func TestGetStatusesAPI(t *testing.T) {
	port := isolate(t)
	addServer(t, port, "get-st", true)
	t.Run("server without a status", func(t *testing.T) {
		body := mustAdmin(t, port, "GET", "/mcpapi/server/get-st/status", "")
		expectBodyContains(t, body, "Reporting Status [0]")
	})
	mustAdmin(t, port, "POST", "/mcpapi/server/get-st/status/set/503:2", "")
	t.Run("server with a status", func(t *testing.T) {
		body := mustAdmin(t, port, "GET", "/mcpapi/server/get-st/status", "")
		expectBodyContains(t, body, "Reporting Status [503] for [2] times")
	})
	t.Run("all statuses of all ports", func(t *testing.T) {
		body := mustAdmin(t, port, "GET", "/mcpapi/servers/status", "")
		all := map[string][]map[string]any{}
		if err := json.Unmarshal([]byte(body), &all); err != nil {
			t.Fatalf("body %q: %v", body, err)
		}
		if len(all[fmt.Sprint(port)]) != 1 {
			t.Errorf("statuses of the port = %v", all)
		}
	})
}

// Reporting a status is not using it, so no number of reports takes the count of the status down.
func TestGetStatusesAPIDoesNotConsumeACount(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "peek-st", true)
	mustAdmin(t, port, "POST", "/mcpapi/server/peek-st/status/set/503:2", "")
	for i := 0; i < 5; i++ {
		expectBodyContains(t, mustAdmin(t, port, "GET", "/mcpapi/server/peek-st/status", ""), "Reporting Status [503] for [2] times")
		mustAdmin(t, port, "GET", "/mcpapi/servers/status", "")
	}
	if status, left := StatusManager.GetStatusFor(port, s.URI, nil, "POST"); status != 503 || left != 1 {
		t.Errorf("first use after the reports = (%d, %d), want (503, 1)", status, left)
	}
	if status, left := StatusManager.GetStatusFor(port, s.URI, nil, "POST"); status != 503 || left != 0 {
		t.Errorf("second use = (%d, %d), want (503, 0)", status, left)
	}
	if status, _ := StatusManager.GetStatusFor(port, s.URI, nil, "POST"); status != 0 {
		t.Errorf("third use = %d, want the status to be used up", status)
	}
}

func TestClearStatusAPIClearsAllStatusesOfThePort(t *testing.T) {
	port := isolate(t)
	addServer(t, port, "clr-st-a", true)
	addServer(t, port, "clr-st-b", true)
	mustAdmin(t, port, "POST", "/mcpapi/server/clr-st-a/status/set/503", "")
	mustAdmin(t, port, "POST", "/mcpapi/server/clr-st-b/status/set/504", "")
	expectBodyContains(t, mustAdmin(t, port, "POST", "/mcpapi/servers/status/clear", ""), "Status cleared on port")
	if configs := StatusManager.Snapshot()[port]; len(configs) != 0 {
		t.Errorf("configs after clearing = %s", statusesOf(port))
	}
}

func TestSetStatusAPIIsSafeForConcurrentUse(t *testing.T) {
	port := isolate(t)
	addServer(t, port, "conc-st", true)
	addTools(t, port, "conc-st", "["+echoToolJSON+"]")
	targets := []string{
		"/mcpapi/server/conc-st/status/set/503:3",
		"/mcpapi/server/conc-st/tool/echo/status/set/502",
		"/mcpapi/server/conc-st/status/set/500/header/x-conc",
	}
	var wg sync.WaitGroup
	codes := make(chan int, 120)
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				codes <- admin(port, "POST", targets[(w+i)%len(targets)], "").Code
				codes <- admin(port, "GET", "/mcpapi/server/conc-st/status", "").Code
				codes <- admin(port, "GET", "/mcpapi/servers/status", "").Code
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
	close(codes)
	n := 0
	for code := range codes {
		n++
		if code != http.StatusOK {
			t.Errorf("status = %d, want 200", code)
		}
	}
	if n != 120 {
		t.Errorf("%d responses, want 120", n)
	}
}

func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
