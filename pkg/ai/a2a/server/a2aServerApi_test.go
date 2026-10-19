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
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	a2aclient "goto/pkg/ai/a2a/client"
	"goto/pkg/ai/a2a/model"
	"goto/pkg/ai/registry"
	"goto/pkg/constants"
	"goto/pkg/server/intercept"
	"goto/pkg/util"
)

// agentsBody is the payload that adds the agents through the admin API.
func agentsBody(t *testing.T, agents ...*model.Agent) string {
	t.Helper()
	b, err := json.Marshal(agents)
	if err != nil {
		t.Fatalf("marshal of agents failed: %v", err)
	}
	return string(b)
}

// decodeMap decodes the JSON response of the admin API.
func decodeMap(t *testing.T, resp *rawResponse) map[string]any {
	t.Helper()
	m := map[string]any{}
	if err := json.Unmarshal([]byte(resp.Body), &m); err != nil {
		t.Fatalf("response is not a JSON object (%v): %s", err, resp.Body)
	}
	return m
}

func (ts *testServer) agentPath(name, suffix string) string {
	return "/a2a/agents/" + name + suffix
}

// post sends a message to the endpoint of the agent, which is how the clients call it.
func (ts *testServer) post(name string, headers ...string) *rawResponse {
	ts.t.Helper()
	return ts.postMessage("/agent/"+name+"/", "hello", headers...)
}

// setStatus sets a forced status for the agent. The spec is what follows /status/set/ in the path.
func (ts *testServer) setStatus(name, spec string) {
	ts.t.Helper()
	resp := ts.request("POST", ts.agentPath(name, "/status/set/"+spec), "")
	if resp.Code != http.StatusOK {
		ts.t.Fatalf("setting status [%s] for agent [%s] = %d: %s", spec, name, resp.Code, resp.Body)
	}
}

// countCalls makes the agent count the requests that reach it, which a forced status must keep from happening.
func countCalls(agent *model.Agent) *atomic.Int32 {
	calls := &atomic.Int32{}
	served := agent.Handler
	agent.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		served.ServeHTTP(w, r)
	})
	return calls
}

func requireForced(t *testing.T, resp *rawResponse, code int, remaining string) {
	t.Helper()
	if resp.Code != code {
		t.Errorf("status = %d, want the forced %d. Body: %s", resp.Code, code, resp.Body)
	}
	if got := resp.Header.Get(constants.HeaderGotoForcedStatus); got != strconv.Itoa(code) {
		t.Errorf("%s = %q, want %d", constants.HeaderGotoForcedStatus, got, code)
	}
	if got := resp.Header.Get(constants.HeaderGotoForcedStatusRemaining); got != remaining {
		t.Errorf("%s = %q, want %q", constants.HeaderGotoForcedStatusRemaining, got, remaining)
	}
	if strings.Contains(resp.Body, "jsonrpc") {
		t.Errorf("body = %s, want no response of the agent for a forced status", resp.Body)
	}
}

func requireServed(t *testing.T, resp *rawResponse) {
	t.Helper()
	if resp.Code != http.StatusOK {
		t.Errorf("status = %d, want 200. Body: %s", resp.Code, resp.Body)
	}
	if got := resp.Header.Get(constants.HeaderGotoForcedStatus); got != "" {
		t.Errorf("%s = %q, want none for a request that is served", constants.HeaderGotoForcedStatus, got)
	}
	if !strings.Contains(resp.Body, "jsonrpc") {
		t.Errorf("body = %s, want the response of the agent", resp.Body)
	}
}

func TestAddAgentsAPI(t *testing.T) {
	ts := newTestServer(t)
	first, second := uniqueName("api-add"), uniqueName("api-add")
	t.Cleanup(func() {
		registry.TheAgentRegistry.RemoveAgent(first)
		registry.TheAgentRegistry.RemoveAgent(second)
	})
	body := agentsBody(t, ts.newAgent(first, false, nil, nil), ts.newAgent(second, true, &model.AgentBehavior{Stream: true}, nil))
	resp := ts.request("POST", "/a2a/agents/add", body)
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body, first) || !strings.Contains(resp.Body, second) {
		t.Fatalf("add = %d: %s", resp.Code, resp.Body)
	}

	for _, name := range []string{first, second} {
		if GetAgent(ts.port, name) == nil {
			t.Errorf("agent [%s] is not on the server of the port", name)
		}
		if got := registry.TheAgentRegistry.GetAgents(name); got[ts.port] == nil {
			t.Errorf("agent [%s] is not in the registry for port %d: %v", name, ts.port, got)
		}
	}

	agent := decodeMap(t, ts.request("GET", ts.agentPath(first, ""), ""))
	if card, _ := agent["card"].(map[string]any); card["name"] != first {
		t.Errorf("agent = %v, want the card of [%s]", agent, first)
	}

	names := decodeMap(t, ts.request("GET", "/a2a/agents/names", ""))
	if forPort, _ := names[strconv.Itoa(ts.port)].(map[string]any); forPort[first] == nil || forPort[second] == nil {
		t.Errorf("names = %v, want both agents of the port", names)
	}
	allNames := decodeMap(t, ts.request("GET", "/a2a/agents/names/all", ""))
	if forPort, _ := allNames[strconv.Itoa(ts.port)].(map[string]any); forPort[first] == nil {
		t.Errorf("names of all ports = %v, want the port of the test", allNames)
	}

	servers := decodeMap(t, ts.request("GET", "/a2a/agents/all", ""))
	server, _ := servers[strconv.Itoa(ts.port)].(map[string]any)
	if agents, _ := server["agents"].(map[string]any); agents[first] == nil || agents[second] == nil {
		t.Errorf("servers = %v, want both agents on the port", servers)
	}

	inRegistry := decodeMap(t, ts.request("GET", "/a2a/agents", ""))
	if byPort, _ := inRegistry[first].(map[string]any); byPort[strconv.Itoa(ts.port)] == nil {
		t.Errorf("registry agents = %v, want [%s] on port %d", inRegistry, first, ts.port)
	}

	if card := ts.getCard(first); card.Code != http.StatusOK || !strings.Contains(card.Body, first) {
		t.Errorf("card of an added agent = %d: %s", card.Code, card.Body)
	}
	if r := ts.call(first, "hello", nil); r.Err != nil || r.Result == nil {
		t.Errorf("call of an added agent = %+v, %v, want a response", r.Result, r.Err)
	}
}

func TestAddAgentsAPIYAML(t *testing.T) {
	ts := newTestServer(t)
	name := uniqueName("api-yaml")
	t.Cleanup(func() { registry.TheAgentRegistry.RemoveAgent(name) })
	doc := fmt.Sprintf("- card:\n    name: %s\n    url: %s\n    version: \"1.0\"\n  behavior:\n    echo: true\n", name, ts.agentURL(name))
	resp := ts.request("POST", "/a2a/agents/add", doc, "Content-Type", "application/yaml")
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body, name) {
		t.Fatalf("add with yaml = %d: %s", resp.Code, resp.Body)
	}
	agent := GetAgent(ts.port, name)
	if agent == nil || !agent.Behavior.Echo {
		t.Fatalf("agent = %+v, want the echo agent of the document", agent)
	}
	got := ts.request("GET", ts.agentPath(name, ""), "", "Accept", "application/yaml")
	if got.Code != http.StatusOK || !strings.Contains(got.Body, "name: "+name) {
		t.Errorf("yaml report = %d: %s", got.Code, got.Body)
	}
}

func TestAddAgentsAPIFailures(t *testing.T) {
	ts := newTestServer(t)
	valid := uniqueName("api-partial")
	t.Cleanup(func() { registry.TheAgentRegistry.RemoveAgent(valid) })
	validJSON := agentsBody(t, ts.newAgent(valid, false, nil, nil))
	tests := []struct {
		name string
		body string
		want string
	}{
		{"not json", "not json", "Failed to parse payload"},
		{"empty list", "[]", "no agent cards"},
		{"null agent", "[null]", "has no card"},
		{"no card", `[{"id":"no-card"}]`, "has no card"},
		{"no name", `[{"card":{"url":"http://127.0.0.1:1/agent/x/"}}]`, "no name"},
		{"bad url", `[{"card":{"name":"bad-url","url":"://bad"}}]`, "bad-url"},
		{"valid and invalid", `[` + strings.Trim(validJSON, "[]") + `,{"id":"no-card"}]`, "Failed Agents"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts.t = t
			resp := ts.request("POST", "/a2a/agents/add", tt.body)
			if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body, tt.want) {
				t.Errorf("add = %d: %s, want 400 with [%s]", resp.Code, resp.Body, tt.want)
			}
		})
	}
	ts.t = t
	if GetAgent(ts.port, valid) == nil {
		t.Errorf("the valid agent of a mixed payload was not added")
	}
	if got := ts.call(valid, "still works", nil); got.Err != nil {
		t.Errorf("call of the agent added with a failing one = %v", got.Err)
	}
}

func TestGetAgentUnknown(t *testing.T) {
	ts := newTestServer(t)
	resp := ts.request("GET", ts.agentPath("nobody", ""), "")
	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body, "Bad agent [nobody]") {
		t.Errorf("unknown agent = %d: %s, want 400", resp.Code, resp.Body)
	}
}

// The words that the paths of the API have in them are also in the names of agents.
func TestAgentRoutesWithReservedWordsInName(t *testing.T) {
	ts := newTestServer(t)
	cfg := &model.AgentConfig{Delegates: &model.DelegateConfig{
		Tools: map[string]*model.DelegateToolCall{"t1": {ToolCall: &mcpToolCall{Tool: "echo-tool", URL: "localhost:3/mcp"}}},
	}}
	for _, prefix := range []string{"tall", "notebook", "names", "agents", "delegates-tools", "rename"} {
		name, _ := ts.add(prefix, false, &model.AgentBehavior{Federate: true}, cfg)
		agent := decodeMap(t, ts.request("GET", ts.agentPath(name, ""), ""))
		if card, _ := agent["card"].(map[string]any); card["name"] != name {
			t.Errorf("GET agent [%s] = %v, want the agent and not a report of all agents", name, agent)
		}
		delegates := decodeMap(t, ts.request("GET", ts.agentPath(name, "/delegates"), ""))
		if delegates["tools"] == nil {
			t.Errorf("GET delegates of [%s] = %v, want the tools", name, delegates)
		}
	}
}

func TestAgentDelegatesAPI(t *testing.T) {
	ts := newTestServer(t)
	cfg := &model.AgentConfig{Delegates: &model.DelegateConfig{
		MaxCalls: 3,
		Agents: map[string]*model.DelegateAgentCall{
			"a1": {AgentCall: &a2aclient.AgentCall{Name: "remote-1", CardURL: "localhost:1/"}, Triggers: []string{"a"}},
		},
		Tools: map[string]*model.DelegateToolCall{
			"t1": {ToolCall: &mcpToolCall{Tool: "echo-tool", URL: "localhost:3/mcp"}, Triggers: []string{"t"}},
		},
		HTTP: map[string]*model.DelegateHTTPCall{
			"h1": {HTTPCall: &model.HTTPCall{URL: "localhost:5/x"}},
		},
	}}
	name, _ := ts.add("deleg", false, &model.AgentBehavior{Federate: true}, cfg)
	toolsOnly, _ := ts.add("deleg-tools", false, &model.AgentBehavior{Federate: true}, &model.AgentConfig{Delegates: &model.DelegateConfig{
		Tools: map[string]*model.DelegateToolCall{"t1": {ToolCall: &mcpToolCall{Tool: "echo-tool", URL: "localhost:3/mcp"}}},
	}})
	noConfig, _ := ts.add("deleg-none", false, nil, nil)

	all := decodeMap(t, ts.request("GET", ts.agentPath(name, "/delegates"), ""))
	for _, k := range []string{"tools", "agents", "http"} {
		if all[k] == nil {
			t.Errorf("delegates = %v, want [%s] in them", all, k)
		}
	}
	if all["maxCalls"] != float64(3) {
		t.Errorf("delegates = %v, want maxCalls 3", all)
	}

	tools := decodeMap(t, ts.request("GET", ts.agentPath(name, "/delegates/tools"), ""))
	if tools["t1"] == nil || len(tools) != 1 {
		t.Errorf("tools = %v, want only t1", tools)
	}
	tool := ts.request("GET", ts.agentPath(name, "/delegates/tools/t1"), "")
	if tool.Code != http.StatusOK || !strings.Contains(tool.Body, "echo-tool") {
		t.Errorf("tool t1 = %d: %s", tool.Code, tool.Body)
	}
	agents := decodeMap(t, ts.request("GET", ts.agentPath(name, "/delegates/agents"), ""))
	if agents["a1"] == nil || len(agents) != 1 {
		t.Errorf("agents = %v, want only a1", agents)
	}
	agent := ts.request("GET", ts.agentPath(name, "/delegates/agents/a1"), "")
	if agent.Code != http.StatusOK || !strings.Contains(agent.Body, "remote-1") {
		t.Errorf("agent a1 = %d: %s", agent.Code, agent.Body)
	}

	failures := []struct {
		name string
		path string
		want string
	}{
		{"unknown agent", ts.agentPath("nobody", "/delegates"), "Bad agent [nobody]"},
		{"unknown tool", ts.agentPath(name, "/delegates/tools/none"), "No Tool delegate [none]"},
		{"unknown agent delegate", ts.agentPath(name, "/delegates/agents/none"), "No Agents delegate [none]"},
		{"no agent delegates", ts.agentPath(toolsOnly, "/delegates/agents"), "No Agent delegates"},
		{"no config", ts.agentPath(noConfig, "/delegates"), "No delegates"},
		{"no config tools", ts.agentPath(noConfig, "/delegates/tools"), "No delegates"},
	}
	for _, tt := range failures {
		resp := ts.request("GET", tt.path, "")
		if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body, tt.want) {
			t.Errorf("%s: %s = %d: %s, want 400 with [%s]", tt.name, tt.path, resp.Code, resp.Body, tt.want)
		}
	}
}

func TestSetAgentPayloadAPI(t *testing.T) {
	ts := newTestServer(t)
	// The payload is what a stream agent sends.
	name, agent := ts.add("payload", true, &model.AgentBehavior{Stream: true}, nil)
	if r := ts.call(name, "go", nil); r.Err != nil || r.has("Result#") {
		t.Errorf("call before a payload is set = %v, %v, want no stream", r.Texts, r.Err)
	}

	payload := `{"textStream":["alpha","beta"],"streamCount":2,"delay":{"min":"1ms","max":"1ms"}}`
	resp := ts.request("POST", ts.agentPath(name, "/payload"), payload)
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body, "Payload set successfully") {
		t.Fatalf("set payload = %d: %s", resp.Code, resp.Body)
	}
	if p := agent.GetPayload(); p == nil || p.StreamCount != 2 {
		t.Errorf("payload = %+v, want the one that was set", p)
	}
	if got := decodeMap(t, ts.request("GET", ts.agentPath(name, ""), "")); !strings.Contains(fmt.Sprint(got["config"]), "alpha") {
		t.Errorf("agent report = %v, want the payload in the config", got)
	}
	r := ts.call(name, "go {current}/{total}", nil)
	if r.Err != nil || !r.has("Result# 1 (of 2): go 1/2") || !r.has("Result# 2 (of 2): go 2/2") {
		t.Errorf("call = %v, %v, want the 2 results of the payload that was set", r.Texts, r.Err)
	}

	for _, tt := range []struct {
		name string
		path string
		body string
		want string
	}{
		{"not json", ts.agentPath(name, "/payload"), "not json", "Failed to set payload"},
		{"unknown agent", ts.agentPath("nobody", "/payload"), `{"text":"x"}`, "Bad agent [nobody]"},
	} {
		resp := ts.request("POST", tt.path, tt.body)
		if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body, tt.want) {
			t.Errorf("%s = %d: %s, want 400 with [%s]", tt.name, resp.Code, resp.Body, tt.want)
		}
	}
	if p := agent.GetPayload(); p == nil || p.StreamCount != 2 {
		t.Errorf("payload = %+v, want the previous one to be kept after a failure", p)
	}
}

func TestServersAndClearAPI(t *testing.T) {
	ts := newTestServer(t)
	other := newTestServer(t)
	name, _ := ts.add("srv", false, nil, nil)
	otherName, _ := other.add("srv-other", false, nil, nil)

	servers := decodeMap(t, ts.request("GET", "/a2a/servers", ""))
	server, _ := servers[strconv.Itoa(ts.port)].(map[string]any)
	if agents, _ := server["agents"].(map[string]any); agents[name] == nil {
		t.Errorf("servers = %v, want [%s] on port %d", servers, name, ts.port)
	}
	if server["enabled"] != true {
		t.Errorf("server = %v, want it enabled", server)
	}

	resp := ts.request("POST", "/a2a/servers/clear", "")
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body, fmt.Sprintf("Server cleared on port: %d", ts.port)) {
		t.Errorf("clear server = %d: %s", resp.Code, resp.Body)
	}
	if GetAgent(ts.port, name) != nil {
		t.Errorf("agent [%s] is still on the cleared server", name)
	}
	if GetAgent(other.port, otherName) == nil {
		t.Errorf("agent [%s] of another port was cleared", otherName)
	}

	resp = ts.request("POST", "/a2a/agents/clear", "")
	if resp.Code != http.StatusOK {
		t.Errorf("clear agents = %d: %s", resp.Code, resp.Body)
	}
	if GetAgent(other.port, otherName) != nil {
		t.Errorf("agent [%s] survived the clear of all agents", otherName)
	}
	if got := registry.TheAgentRegistry.GetAgents(otherName); len(got) != 0 {
		t.Errorf("registry has %v for [%s] after clearing all agents", got, otherName)
	}
}

func TestForcedStatusPostNotGet(t *testing.T) {
	ts := newTestServer(t)
	name, agent := ts.add("forced", false, nil, nil)
	calls := countCalls(agent)
	ts.setStatus(name, "503:1")

	// The card is fetched with GET, which is not given the status and does not use it up.
	if card := ts.getCard(name); card.Code != http.StatusOK || card.Header.Get(constants.HeaderGotoForcedStatus) != "" {
		t.Errorf("card = %d, %v, want it served", card.Code, card.Header)
	}
	requireForced(t, ts.post(name), 503, "0")
	if n := calls.Load(); n != 1 {
		t.Errorf("agent served %d requests, want only the card", n)
	}
	requireServed(t, ts.post(name))
	if n := calls.Load(); n != 2 {
		t.Errorf("agent served %d requests, want 2 once the status is used up", n)
	}
}

func TestForcedStatusLimited(t *testing.T) {
	ts := newTestServer(t)
	name, _ := ts.add("limited", false, nil, nil)
	ts.setStatus(name, "503:2")
	requireForced(t, ts.post(name), 503, "1")
	requireForced(t, ts.post(name), 503, "0")
	requireServed(t, ts.post(name))
	requireServed(t, ts.post(name))
}

func TestForcedStatusUnlimited(t *testing.T) {
	ts := newTestServer(t)
	name, _ := ts.add("unlimited", false, nil, nil)
	ts.setStatus(name, "502")
	for i := 0; i < 5; i++ {
		requireForced(t, ts.post(name), 502, "-1")
	}
}

func TestForcedStatusOneOfSeveral(t *testing.T) {
	ts := newTestServer(t)
	name, _ := ts.add("several", false, nil, nil)
	ts.setStatus(name, "502,503:6")
	seen := map[int]int{}
	for i := 0; i < 6; i++ {
		resp := ts.post(name)
		seen[resp.Code]++
		if resp.Code != 502 && resp.Code != 503 {
			t.Errorf("request %d = %d, want 502 or 503", i, resp.Code)
		}
	}
	if seen[502]+seen[503] != 6 {
		t.Errorf("statuses = %v, want 6 forced ones", seen)
	}
	requireServed(t, ts.post(name))
}

func TestForcedStatusCountedOncePerRequestUnderConcurrency(t *testing.T) {
	ts := newTestServer(t)
	name, agent := ts.add("counted", false, nil, nil)
	calls := countCalls(agent)
	const total, forced = 20, 7
	ts.setStatus(name, fmt.Sprintf("503:%d", forced))

	codes := make([]int, total)
	errs := make([]error, total)
	var wg sync.WaitGroup
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp, err := ts.try("POST", "/agent/"+name+"/", sendMessageBody("hello"))
			if err != nil {
				errs[i] = err
				return
			}
			codes[i] = resp.Code
		}(i)
	}
	wg.Wait()
	counts := map[int]int{}
	for i, code := range codes {
		if errs[i] != nil {
			t.Errorf("request %d failed: %v", i, errs[i])
		}
		counts[code]++
	}
	if counts[503] != forced || counts[200] != total-forced {
		t.Errorf("statuses = %v, want exactly %d forced and %d served", counts, forced, total-forced)
	}
	if n := calls.Load(); n != total-forced {
		t.Errorf("agent served %d requests, want %d", n, total-forced)
	}
}

func TestForcedStatusWithHeaderValue(t *testing.T) {
	ts := newTestServer(t)
	name, _ := ts.add("hdrval", false, nil, nil)
	ts.setStatus(name, "503:2/header/x-fail=yes")

	requireServed(t, ts.post(name))
	requireServed(t, ts.post(name, "X-Fail", "no"))
	requireForced(t, ts.post(name, "X-Fail", "yes"), 503, "1")
	// Requests that don't match didn't use the status up.
	requireServed(t, ts.post(name, "X-Other", "yes"))
	requireForced(t, ts.post(name, "x-fail", "yes"), 503, "0")
	requireServed(t, ts.post(name, "X-Fail", "yes"))
}

func TestForcedStatusWithHeaderPresent(t *testing.T) {
	ts := newTestServer(t)
	name, _ := ts.add("hdrpres", false, nil, nil)
	ts.setStatus(name, "504/header/x-fail")
	requireServed(t, ts.post(name))
	requireForced(t, ts.post(name, "X-Fail", "anything"), 504, "-1")
	requireForced(t, ts.post(name, "X-Fail", ""), 504, "-1")
}

func TestForcedStatusWithHeaderAbsent(t *testing.T) {
	ts := newTestServer(t)
	name, _ := ts.add("hdrabs", false, nil, nil)
	ts.setStatus(name, "503/header/not/x-pass")
	requireForced(t, ts.post(name), 503, "-1")
	requireForced(t, ts.post(name, "X-Other", "1"), 503, "-1")
	requireServed(t, ts.post(name, "X-Pass", "1"))
}

// The path of the API for a status with a header has the word "not" only for a header that is absent. The name of an
// agent has it too.
func TestForcedStatusAgentNameWithNot(t *testing.T) {
	ts := newTestServer(t)
	name, _ := ts.add("notebook", false, nil, nil)
	ts.setStatus(name, "503/header/x-fail=yes")
	requireForced(t, ts.post(name, "X-Fail", "yes"), 503, "-1")
	requireServed(t, ts.post(name))
	requireServed(t, ts.post(name, "X-Fail", "no"))
}

func TestForcedStatusHeaderNameWithNot(t *testing.T) {
	ts := newTestServer(t)
	name, _ := ts.add("hdrnot", false, nil, nil)
	ts.setStatus(name, "503/header/x-notify=on")
	requireForced(t, ts.post(name, "X-Notify", "on"), 503, "-1")
	requireServed(t, ts.post(name))
}

func TestForcedStatusAgentBeatsAllRequests(t *testing.T) {
	for _, order := range []string{"all first", "agent first"} {
		t.Run(order, func(t *testing.T) {
			ts := newTestServer(t)
			name, _ := ts.add("beats", false, nil, nil)
			setAll := func() { statusManager.SetStatus(ts.port, []int{500}, -1, true) }
			if order == "all first" {
				setAll()
				ts.setStatus(name, "503")
			} else {
				ts.setStatus(name, "503")
				setAll()
			}
			requireForced(t, ts.post(name), 503, "-1")
		})
	}
}

func TestForcedStatusForAllRequestsOfPort(t *testing.T) {
	ts := newTestServer(t)
	name, _ := ts.add("all-reqs", false, nil, nil)
	statusManager.SetStatus(ts.port, []int{500}, 1, true)
	requireForced(t, ts.post(name), 500, "0")
	requireServed(t, ts.post(name))

	// A status of 0 for all the requests has nothing to say.
	statusManager.SetStatus(ts.port, []int{0}, -1, true)
	requireServed(t, ts.post(name))
}

func TestForcedStatusQueryAndTrailingSlash(t *testing.T) {
	ts := newTestServer(t)
	name, _ := ts.add("query", false, nil, nil)
	ts.setStatus(name, "503")
	for _, path := range []string{"/agent/" + name + "/?trace=1", "/agent/" + name + "?trace=1&x=/agent/", "/agent/" + name} {
		requireForced(t, ts.request("POST", path, sendMessageBody("hello")), 503, "-1")
	}
	// The query doesn't make the request one for another path.
	other, _ := ts.add("query-other", false, nil, nil)
	requireServed(t, ts.request("POST", "/agent/"+other+"/?x=/agent/"+name+"/", sendMessageBody("hello")))
}

func TestForcedStatusIsPerAgent(t *testing.T) {
	ts := newTestServer(t)
	name, _ := ts.add("per-agent", false, nil, nil)
	other, _ := ts.add("per-agent-other", false, nil, nil)
	ts.setStatus(name, "503")
	requireForced(t, ts.post(name), 503, "-1")
	requireServed(t, ts.post(other))
}

func TestSetStatusFailures(t *testing.T) {
	ts := newTestServer(t)
	name, _ := ts.add("setfail", false, nil, nil)
	for _, tt := range []struct {
		name string
		path string
		want string
	}{
		{"invalid status", ts.agentPath(name, "/status/set/abc"), "Invalid Status"},
		{"unknown agent", ts.agentPath("nobody", "/status/set/503"), "Bad agent [nobody]"},
		{"unknown agent with header", ts.agentPath("nobody", "/status/set/503/header/x"), "Bad agent [nobody]"},
	} {
		resp := ts.request("POST", tt.path, "")
		if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body, tt.want) {
			t.Errorf("%s = %d: %s, want 400 with [%s]", tt.name, resp.Code, resp.Body, tt.want)
		}
	}
	requireServed(t, ts.post(name))
}

func TestStatusGetAndClear(t *testing.T) {
	ts := newTestServer(t)
	name, _ := ts.add("getclear", false, nil, nil)
	ts.setStatus(name, "503:5/header/x-fail=yes")

	statuses := decodeMap(t, ts.request("GET", ts.agentPath(name, "/status"), ""))
	configs, _ := statuses[strconv.Itoa(ts.port)].([]any)
	if len(configs) != 1 {
		t.Fatalf("statuses = %v, want one for port %d", statuses, ts.port)
	}
	sc, _ := configs[0].(map[string]any)
	if fmt.Sprint(sc["statuses"]) != "[503]" || sc["times"] != float64(5) {
		t.Errorf("status = %v, want 503 for 5 times", sc)
	}
	match, _ := sc["match"].(map[string]any)
	uri, _ := match["uri"].(map[string]any)
	if uri["exact"] != "/agent/"+name+"/" || uri["prefix"] != "/agent/"+name+"/" {
		t.Errorf("uri match = %v, want the URI of the agent", uri)
	}
	if fmt.Sprint(match["methods"]) != "[POST]" {
		t.Errorf("methods = %v, want only POST", match["methods"])
	}

	// The report is of a copy, so using the status doesn't change what was reported.
	requireForced(t, ts.post(name, "X-Fail", "yes"), 503, "4")
	if sc["times"] != float64(5) {
		t.Errorf("reported status changed to %v", sc["times"])
	}

	resp := ts.request("POST", ts.agentPath(name, "/status/clear"), "")
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body, fmt.Sprintf("Status cleared on port [%d]", ts.port)) {
		t.Errorf("clear = %d: %s", resp.Code, resp.Body)
	}
	requireServed(t, ts.post(name, "X-Fail", "yes"))
	after := decodeMap(t, ts.request("GET", ts.agentPath(name, "/status"), ""))
	if left, _ := after[strconv.Itoa(ts.port)].([]any); len(left) != 0 {
		t.Errorf("statuses after clear = %v, want none for the port", left)
	}
}

func TestConfigureStatus(t *testing.T) {
	ts := newTestServer(t)
	name, _ := ts.add("configure", false, nil, nil)
	configure := func(body string) *rawResponse {
		ts.t.Helper()
		return ts.request("POST", ts.agentPath(name, "/status/configure"), body)
	}
	uri := "/agent/" + name + "/"

	t.Run("exact", func(t *testing.T) {
		ts.t = t
		resp := configure(fmt.Sprintf(`{"statuses":[502],"times":2,"match":{"uri":{"exact":%q},"methods":["POST"]}}`, uri))
		if resp.Code != http.StatusOK {
			t.Fatalf("configure = %d: %s", resp.Code, resp.Body)
		}
		requireForced(t, ts.post(name), 502, "1")
		if card := ts.getCard(name); card.Code != http.StatusOK {
			t.Errorf("card = %d, want it served as the status is for POST", card.Code)
		}
		requireForced(t, ts.post(name), 502, "0")
		requireServed(t, ts.post(name))
	})

	t.Run("prefix applies to the card", func(t *testing.T) {
		ts.t = t
		statusManager.Clear(ts.port, "", "")
		resp := configure(fmt.Sprintf(`{"statuses":[503],"times":1,"match":{"uri":{"prefix":%q},"methods":["GET"]}}`, uri+".well-known"))
		if resp.Code != http.StatusOK {
			t.Fatalf("configure = %d: %s", resp.Code, resp.Body)
		}
		requireServed(t, ts.post(name))
		if card := ts.getCard(name); card.Code != 503 || card.Header.Get(constants.HeaderGotoForcedStatus) != "503" {
			t.Errorf("card = %d, %v, want the forced 503", card.Code, card.Header)
		}
		if card := ts.getCard(name); card.Code != http.StatusOK {
			t.Errorf("card = %d, want it served once the status is used up", card.Code)
		}
	})

	t.Run("regex", func(t *testing.T) {
		ts.t = t
		statusManager.Clear(ts.port, "", "")
		resp := configure(fmt.Sprintf(`{"statuses":[429],"match":{"uri":{"regex":%q},"headers":[{"header":"x-tenant","value":"t42"}]}}`, "/agent/"+name+"/?$"))
		if resp.Code != http.StatusOK {
			t.Fatalf("configure = %d: %s", resp.Code, resp.Body)
		}
		requireServed(t, ts.post(name))
		requireForced(t, ts.post(name, "X-Tenant", "T42"), 429, "-1")
		requireServed(t, ts.post(name, "X-Tenant", "other"))
	})

	t.Run("failures", func(t *testing.T) {
		ts.t = t
		for _, tt := range []struct{ name, body string }{
			{"not json", "not json"},
			{"no status", `{"times":1}`},
			{"negative status", `{"statuses":[-1]}`},
			{"bad times", `{"statuses":[500],"times":-5}`},
			{"bad regex", `{"statuses":[500],"match":{"uri":{"regex":"("}}}`},
		} {
			resp := configure(tt.body)
			if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body, "Failed to parse status config") {
				t.Errorf("%s = %d: %s, want 400", tt.name, resp.Code, resp.Body)
			}
		}
	})
}

func TestSendStatus(t *testing.T) {
	setup(t)
	port := 41101
	var forced int
	rr := recordRequest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sendStatus("agent-1", 418, 3, w, r)
		forced = util.GetRequestStore(r).StatusCode
	}), port, "POST", "/agent/x/", `{"payload":"is read"}`, "X-Test", "1")
	if rr.Code != 418 {
		t.Errorf("status = %d, want 418", rr.Code)
	}
	if got := rr.Header().Get(constants.HeaderGotoForcedStatus); got != "418" {
		t.Errorf("%s = %q, want 418", constants.HeaderGotoForcedStatus, got)
	}
	if got := rr.Header().Get(constants.HeaderGotoForcedStatusRemaining); got != "3" {
		t.Errorf("%s = %q, want 3", constants.HeaderGotoForcedStatusRemaining, got)
	}
	if forced != 418 {
		t.Errorf("status of the request store = %d, want 418", forced)
	}
}

func TestServeAgentUnknownAndMissing(t *testing.T) {
	ts := newTestServer(t)
	resp := ts.request("POST", "/agent/nobody/", sendMessageBody("hello"))
	if resp.Code != http.StatusNotFound || !strings.Contains(resp.Body, "agent [nobody] not found") {
		t.Errorf("unknown agent = %d: %s, want 404", resp.Code, resp.Body)
	}
	if resp.Header.Get("Goto-A2A-Agent") != "" {
		t.Errorf("headers = %v, want no agent headers for an unknown agent", resp.Header)
	}
	for _, path := range []string{"/agent/", "/agent", "/x"} {
		resp := ts.request("GET", path, "")
		if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body, "Agent name needed") {
			t.Errorf("GET %s = %d: %s, want 400", path, resp.Code, resp.Body)
		}
	}
}

func TestServeAgentThroughRouter(t *testing.T) {
	ts := newTestServer(t)
	name, _ := ts.add("routed", false, nil, nil)
	router := intercept.IntereceptMiddleware(nil, nil)(callRouter(t))

	rr := recordRequest(t, router, ts.port, "POST", "/agent/nobody", sendMessageBody("hello"))
	if rr.Code != http.StatusNotFound {
		t.Errorf("unknown agent through the router = %d: %s, want 404", rr.Code, rr.Body.String())
	}

	ts.setStatus(name, "503:1")
	rr = recordRequest(t, router, ts.port, "POST", "/agent/"+name, sendMessageBody("hello"))
	if rr.Code != 503 || rr.Header().Get(constants.HeaderGotoForcedStatus) != "503" {
		t.Errorf("forced status through the router = %d, %v, want 503", rr.Code, rr.Header())
	}
	rr = recordRequest(t, router, ts.port, "POST", "/agent/"+name, sendMessageBody("hello"))
	if rr.Code == 503 {
		t.Errorf("status = %d, want the status to be used up", rr.Code)
	}
}

func TestGetAgentNameFromURI(t *testing.T) {
	for uri, want := range map[string]string{
		"/agent/foo":                            "foo",
		"/agent/foo/":                           "foo",
		"/agent/foo?trace=1":                    "foo",
		"/agent/foo/?trace=1":                   "foo",
		"/agent/foo#frag":                       "foo",
		"/agent/foo/.well-known/agent.json":     "foo",
		"/agent/foo/.well-known/agent.json?x=1": "foo",
		"/a2a/agent/foo/bar":                    "foo",
		"/agent/":                               "",
		"/agent":                                "",
		"/other/foo":                            "",
		"":                                      "",
		"/agent/?x=/agent/foo/":                 "",
	} {
		if got := getAgentNameFromURI(uri); got != want {
			t.Errorf("getAgentNameFromURI(%q) = %q, want %q", uri, got, want)
		}
	}
}

func TestAdminAPIConcurrent(t *testing.T) {
	ts := newTestServer(t)
	base, _ := ts.add("conc-api", false, nil, nil)
	const adders, perAdder = 4, 6

	type addPlan struct{ name, body string }
	plans := make([][]addPlan, adders)
	var added []string
	for a := range plans {
		for i := 0; i < perAdder; i++ {
			name := uniqueName("conc-add")
			plans[a] = append(plans[a], addPlan{name, agentsBody(t, ts.newAgent(name, false, nil, nil))})
			added = append(added, name)
		}
	}
	t.Cleanup(func() {
		for _, name := range added {
			registry.TheAgentRegistry.RemoveAgent(name)
		}
	})

	var failures sync.Map
	var failureCount atomic.Int32
	fail := func(format string, args ...any) {
		failureCount.Add(1)
		failures.Store(fmt.Sprintf(format, args...), true)
	}
	check := func(method, path, body string, accept ...int) {
		resp, err := ts.try(method, path, body, "X-Fail", "yes")
		if err != nil {
			fail("%s %s failed: %v", method, path, err)
			return
		}
		ok := false
		for _, code := range accept {
			ok = ok || resp.Code == code
		}
		if !ok {
			fail("%s %s = %d: %s", method, path, resp.Code, resp.Body)
		}
	}

	var wg sync.WaitGroup
	for a := range plans {
		wg.Add(1)
		go func(a int) {
			defer wg.Done()
			for _, p := range plans[a] {
				check("POST", "/a2a/agents/add", p.body, 200)
			}
		}(a)
	}
	for _, path := range []string{"/a2a/servers", "/a2a/agents/all", "/a2a/agents/names", "/a2a/agents/names/all", "/a2a/agents/" + base, "/a2a/agents/" + base + "/status"} {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				resp, err := ts.try("GET", path, "")
				if err != nil {
					fail("GET %s failed: %v", path, err)
				} else if !json.Valid([]byte(resp.Body)) || resp.Code != 200 {
					fail("GET %s = %d: %s", path, resp.Code, resp.Body)
				}
			}
		}(path)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 15; i++ {
			check("POST", "/a2a/agents/"+base+"/payload", fmt.Sprintf(`{"text":"payload %d"}`, i), 200)
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 15; i++ {
			check("POST", "/a2a/agents/"+base+"/status/set/503:1/header/x-fail=yes", "", 200)
			check("POST", "/a2a/agents/"+base+"/status/configure", `{"statuses":[502],"times":1,"match":{"uri":{"prefix":"/agent/none/"}}}`, 200)
			check("POST", "/a2a/agents/"+base+"/status/clear", "", 200)
		}
	}()
	for c := 0; c < 3; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				check("POST", "/agent/"+base+"/", sendMessageBody("hello"), 200, 503)
			}
		}()
	}
	wg.Wait()

	failures.Range(func(k, _ any) bool {
		t.Errorf("%v", k)
		return true
	})
	if n := failureCount.Load(); n != 0 {
		t.Errorf("%d requests failed, want 0", n)
	}
	names := GetAgentNames(ts.port)[ts.port]
	if len(names) != adders*perAdder+1 {
		t.Errorf("agents on the port = %d, want %d", len(names), adders*perAdder+1)
	}
	for _, name := range added {
		if GetAgent(ts.port, name) == nil {
			t.Errorf("agent [%s] was not added", name)
		}
	}
}
