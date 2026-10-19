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

package model

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"sigs.k8s.io/yaml"
)

const agentJSON = `{
  "card": {
    "name": "agent-1",
    "description": "an agent",
    "url": "http://localhost:3000/agent/agent-1/",
    "version": "1.0",
    "capabilities": {"streaming": true},
    "defaultInputModes": ["text"],
    "defaultOutputModes": ["text", "task-status"],
    "skills": [{"id": "echo", "name": "echo", "tags": ["echo"]}]
  },
  "behavior": {"federate": true},
  "config": {
    "delay": {"min": "10ms", "max": "20ms", "count": 3},
    "response": {"streamCount": 4, "textStream": ["a", "b"]},
    "delegates": {
      "maxCalls": 2,
      "parallel": true,
      "tools": {
        "echo": {
          "triggers": ["echo", "feedback"],
          "toolCall": {"tool": "echo", "url": "localhost:3000/mcp/mcp-1/echo"},
          "substitutes": {"alt": {"url": "localhost:4000/mcp/mcp-2/echo", "authority": "alt.example"}}
        }
      },
      "agents": {
        "remote": {
          "triggers": ["remote"],
          "agentCall": {"name": "remote-agent", "agentURL": "localhost:4000/agent/remote/", "cardURL": "localhost:4000/agent/remote/"}
        }
      },
      "http": {
        "web": {"triggers": ["web"], "httpCall": {"url": "localhost:5000/x", "authority": "web.example"}}
      }
    }
  }
}`

func TestAgentParsedFromJSON(t *testing.T) {
	agent := &Agent{}
	if err := json.Unmarshal([]byte(agentJSON), agent); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if agent.GetCard() == nil || agent.GetCard().Name != "agent-1" {
		t.Fatalf("card = %+v, want name agent-1", agent.GetCard())
	}
	if agent.Card.URL != "http://localhost:3000/agent/agent-1/" || agent.Card.Version != "1.0" {
		t.Errorf("card url/version = %q/%q", agent.Card.URL, agent.Card.Version)
	}
	if s := agent.Card.Capabilities.Streaming; s == nil || !*s {
		t.Errorf("capabilities.streaming = %v, want true", s)
	}
	if len(agent.Card.Skills) != 1 || agent.Card.Skills[0].ID != "echo" {
		t.Errorf("skills = %+v", agent.Card.Skills)
	}
	if agent.Behavior == nil || !agent.Behavior.Federate || agent.Behavior.Echo || agent.Behavior.Stream || agent.Behavior.HTTPProxy {
		t.Errorf("behavior = %+v, want only federate", agent.Behavior)
	}
	cfg := agent.Config
	if cfg == nil || cfg.Delay == nil || cfg.Delay.Count != 3 {
		t.Fatalf("config.delay = %+v", cfg)
	}
	if cfg.ResponsePayload == nil || cfg.ResponsePayload.StreamCount != 4 || len(cfg.ResponsePayload.TextStream) != 2 {
		t.Errorf("config.response = %+v", cfg.ResponsePayload)
	}
	d := cfg.Delegates
	if d == nil || d.MaxCalls != 2 || !d.Parallel {
		t.Fatalf("delegates = %+v", d)
	}
	tool := d.Tools["echo"]
	if tool == nil || len(tool.Triggers) != 2 || tool.ToolCall == nil || tool.ToolCall.Tool != "echo" {
		t.Fatalf("tool delegate = %+v", tool)
	}
	if alt := tool.Substitutes["alt"]; alt == nil || alt.Authority != "alt.example" || alt.URL != "localhost:4000/mcp/mcp-2/echo" {
		t.Errorf("tool substitutes = %+v", tool.Substitutes)
	}
	ag := d.Agents["remote"]
	if ag == nil || ag.AgentCall == nil || ag.AgentCall.Name != "remote-agent" || ag.AgentCall.AgentURL != "localhost:4000/agent/remote/" {
		t.Fatalf("agent delegate = %+v", ag)
	}
	web := d.HTTP["web"]
	if web == nil || web.HTTPCall == nil || web.HTTPCall.URL != "localhost:5000/x" || web.HTTPCall.Authority != "web.example" {
		t.Fatalf("http delegate = %+v", web)
	}
}

func TestAgentParsedFromYAML(t *testing.T) {
	doc := `
card:
  name: yaml-agent
  url: http://localhost:3000/agent/yaml-agent/
  capabilities:
    streaming: false
behavior:
  echo: true
config:
  delegates:
    maxCalls: 3
    agents:
      a:
        triggers: [x, y]
        agentCall:
          name: a
          cardURL: localhost:1/
`
	agent := &Agent{}
	if err := yaml.Unmarshal([]byte(doc), agent); err != nil {
		t.Fatalf("yaml unmarshal failed: %v", err)
	}
	if agent.Card.Name != "yaml-agent" || !agent.Behavior.Echo {
		t.Errorf("agent = card %+v behavior %+v", agent.Card, agent.Behavior)
	}
	if s := agent.Card.Capabilities.Streaming; s == nil || *s {
		t.Errorf("capabilities.streaming = %v, want false (explicit)", s)
	}
	a := agent.Config.Delegates.Agents["a"]
	if a == nil || len(a.Triggers) != 2 || a.AgentCall.CardURL != "localhost:1/" || agent.Config.Delegates.MaxCalls != 3 {
		t.Errorf("delegates = %+v", agent.Config.Delegates)
	}
}

func TestAgentJSONIgnoresRuntimeFields(t *testing.T) {
	agent := &Agent{ID: "id-1", URI: "/x", Port: 99, Streaming: true}
	b, err := json.Marshal(agent)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["id"] != "id-1" {
		t.Errorf("id = %v, want id-1", m["id"])
	}
	for _, k := range []string{"URI", "uri", "Server", "server", "Port", "port", "Streaming", "streaming"} {
		if _, found := m[k]; found {
			t.Errorf("runtime field %q is reported in the agent JSON: %s", k, b)
		}
	}
}

func TestAgentConfigOmittedWhenEmpty(t *testing.T) {
	b, err := json.Marshal(&Agent{})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"behavior", "config"} {
		if strings.Contains(string(b), `"`+k+`"`) {
			t.Errorf("%q is reported for an agent without it: %s", k, b)
		}
	}
}

func TestAgentGetCard(t *testing.T) {
	if (&Agent{}).GetCard() != nil {
		t.Error("GetCard of an agent without card must be nil")
	}
	agent := &Agent{}
	if err := json.Unmarshal([]byte(agentJSON), agent); err != nil {
		t.Fatal(err)
	}
	if agent.GetCard() != agent.Card {
		t.Error("GetCard must return the card of the agent")
	}
}

func TestAgentSetPayload(t *testing.T) {
	agent := &Agent{Config: &AgentConfig{}}
	if err := agent.SetPayload([]byte(`{"text":"hi"}`)); err != nil {
		t.Fatalf("SetPayload failed: %v", err)
	}
	p := agent.Config.ResponsePayload
	if p == nil || p.Text != "hi" || p.IsStream {
		t.Errorf("payload = %+v", p)
	}
	// A payload that is not valid keeps the one that is set.
	if err := agent.SetPayload([]byte(`{not json`)); err == nil {
		t.Error("SetPayload of invalid JSON must fail")
	}
	if agent.Config.ResponsePayload != p {
		t.Error("a failed SetPayload must keep the payload that was set")
	}
	// A later payload replaces the earlier one.
	if err := agent.SetPayload([]byte(`{"textStream":["a","b"],"streamCount":3}`)); err != nil {
		t.Fatal(err)
	}
	if got := agent.Config.ResponsePayload; got == p || len(got.TextStream) != 2 || got.StreamCount != 3 || !got.IsStream {
		t.Errorf("payload = %+v, want the new text stream", got)
	}
}

func TestAgentServe(t *testing.T) {
	agent := &Agent{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Seen", r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusTeapot)
	})}
	rr := httptest.NewRecorder()
	agent.Serve(rr, httptest.NewRequest("POST", "/agent/x/", nil))
	if rr.Code != http.StatusTeapot || rr.Header().Get("X-Seen") != "POST /agent/x/" {
		t.Errorf("Serve = %d %v, want the handler to serve the request", rr.Code, rr.Header())
	}
}

func TestDelegateTracker(t *testing.T) {
	tr := NewDelegateTracker()
	if tr.CallCount.Load() != 0 || tr.ResponseCount.Load() != 0 || tr.IsDone() {
		t.Fatalf("new tracker = %+v, want zeroes", tr)
	}
	tr.IncrementCall()
	tr.IncrementCall()
	tr.IncrementResponse()
	if tr.CallCount.Load() != 2 || tr.ResponseCount.Load() != 1 {
		t.Errorf("counts = %d/%d, want 2/1", tr.CallCount.Load(), tr.ResponseCount.Load())
	}
	if tr.IsDone() {
		t.Error("tracker must not be done before Finish")
	}
	tr.Finish()
	if !tr.IsDone() {
		t.Error("tracker must be done after Finish")
	}
}

func TestDelegateTrackerConcurrent(t *testing.T) {
	tr := NewDelegateTracker()
	const workers, each = 16, 200
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < each; j++ {
				tr.IncrementCall()
				tr.IncrementResponse()
				_ = tr.IsDone()
				if _, err := json.Marshal(tr); err != nil {
					// Marshal never fails for a tracker; keep the loop free of t calls from goroutines.
					return
				}
			}
			tr.Finish()
		}()
	}
	wg.Wait()
	if got := tr.CallCount.Load(); got != workers*each {
		t.Errorf("call count = %d, want %d", got, workers*each)
	}
	if got := tr.ResponseCount.Load(); got != workers*each {
		t.Errorf("response count = %d, want %d", got, workers*each)
	}
	if !tr.IsDone() {
		t.Error("tracker must be done")
	}
}

func TestDelegateTrackerJSON(t *testing.T) {
	tr := NewDelegateTracker()
	tr.IncrementCall()
	tr.IncrementCall()
	tr.IncrementCall()
	tr.IncrementResponse()
	tr.Finish()
	b, err := json.Marshal(tr)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["callCount"] != float64(3) || m["responseCount"] != float64(1) || m["done"] != true {
		t.Errorf("tracker JSON = %s, want callCount 3, responseCount 1, done true", b)
	}
	back := NewDelegateTracker()
	if err := json.Unmarshal(b, back); err != nil {
		t.Fatalf("unmarshal of the reported tracker failed: %v", err)
	}
	if back.CallCount.Load() != 3 || back.ResponseCount.Load() != 1 || !back.IsDone() {
		t.Errorf("round trip tracker = %d/%d/%v", back.CallCount.Load(), back.ResponseCount.Load(), back.IsDone())
	}
}

// What the admin API reports for an agent must be accepted back by the API that adds agents.
func TestAgentJSONRoundTrip(t *testing.T) {
	agent := &Agent{}
	if err := json.Unmarshal([]byte(agentJSON), agent); err != nil {
		t.Fatal(err)
	}
	agent.Handler = http.NewServeMux()
	agent.Config.Delegates.Tools["echo"].Tracker = NewDelegateTracker()
	agent.Config.Delegates.Tools["echo"].Tracker.IncrementCall()
	b, err := json.Marshal(agent)
	if err != nil {
		t.Fatal(err)
	}
	back := &Agent{}
	if err := json.Unmarshal(b, back); err != nil {
		t.Fatalf("unmarshal of the reported agent failed: %v\n%s", err, b)
	}
	if back.Card.Name != "agent-1" || back.Config.Delegates.Tools["echo"].Tracker.CallCount.Load() != 1 {
		t.Errorf("round trip agent = %+v", back)
	}
}

func TestDelegateConfigWithNullEntriesParses(t *testing.T) {
	d := &DelegateConfig{}
	if err := json.Unmarshal([]byte(`{"tools":{"t":null},"agents":{"a":null},"http":{"h":null}}`), d); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if v, found := d.Tools["t"]; !found || v != nil {
		t.Errorf("tools = %+v, want key t with a nil value", d.Tools)
	}
	if v, found := d.Agents["a"]; !found || v != nil {
		t.Errorf("agents = %+v, want key a with a nil value", d.Agents)
	}
}
