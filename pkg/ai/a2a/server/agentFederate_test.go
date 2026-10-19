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
	"net/http"
	"strings"
	"testing"
	"time"

	a2aclient "goto/pkg/ai/a2a/client"
	"goto/pkg/ai/a2a/model"
)

func agentDelegate(name, cardURL string, disabled bool, triggers ...string) *model.DelegateAgentCall {
	return &model.DelegateAgentCall{
		AgentCall: &a2aclient.AgentCall{Name: name, CardURL: cardURL, AgentURL: cardURL},
		Triggers:  triggers,
		Disabled:  disabled,
	}
}

func TestFederatePreparesDelegates(t *testing.T) {
	ts := newTestServer(t)
	cfg := &model.AgentConfig{Delegates: &model.DelegateConfig{
		Agents: map[string]*model.DelegateAgentCall{"a1": agentDelegate("remote-1", "localhost:1/", false, "go", "run")},
		Tools: map[string]*model.DelegateToolCall{
			"t1": {ToolCall: &mcpToolCall{Tool: "tool-1", URL: "localhost:2/mcp"}, Triggers: []string{"go"}},
			"t2": {ToolCall: &mcpToolCall{Tool: "tool-2", URL: "localhost:3/mcp"}},
		},
	}}
	_, agent := ts.add("prepared", false, &model.AgentBehavior{Federate: true}, cfg)
	federate := agent.Behavior.Impl.(*AgentBehaviorFederate)
	if len(federate.triggers["go"]) != 2 || len(federate.triggers["run"]) != 1 || len(federate.triggers) != 2 {
		t.Errorf("triggers = %v, want 2 for [go] and 1 for [run]", federate.triggers)
	}
	d := agent.Config.Delegates
	if d.MaxCalls != 1 {
		t.Errorf("max calls = %d, want the default of 1", d.MaxCalls)
	}
	if d.Agents["a1"].GivenName != "a1" || d.Tools["t1"].GivenName != "t1" || d.Tools["t2"].GivenName != "t2" {
		t.Errorf("given names = %q %q %q, want the keys", d.Agents["a1"].GivenName, d.Tools["t1"].GivenName, d.Tools["t2"].GivenName)
	}
	if d.Agents["a1"].Tracker == nil || d.Tools["t1"].Tracker == nil || d.Tools["t2"].Tracker == nil {
		t.Errorf("trackers are missing")
	}
	if d.Tools["t1"].ToolCall.Headers == nil || d.Tools["t1"].ToolCall.Headers.Request == nil {
		t.Errorf("headers of the tool call = %+v, want them prepared once for the requests that share them", d.Tools["t1"].ToolCall.Headers)
	}
}

func TestFederateWithoutDelegates(t *testing.T) {
	ts := newTestServer(t)
	name, _ := ts.add("fed-none", false, &model.AgentBehavior{Federate: true}, nil)
	resp := ts.postMessage("/agent/"+name+"/", "go")
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body, "No tools available") || !strings.Contains(resp.Body, "No agents available") {
		t.Errorf("call = %d: %s, want it to say that nothing is available", resp.Code, resp.Body)
	}
}

func TestFederateNothingTriggered(t *testing.T) {
	ts := newTestServer(t)
	remote, _ := ts.add("remote-idle", false, nil, nil)
	fed, agent := ts.addFederate("fed-idle", false, false, map[string]string{remote: ts.agentURL(remote)})
	resp := ts.postMessage("/agent/"+fed+"/", "unrelated words")
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body, "No agents were triggered") {
		t.Errorf("call = %d: %s, want it to say that no agent was triggered", resp.Code, resp.Body)
	}
	if n := agent.Config.Delegates.Agents[remote].Tracker.CallCount.Load(); n != 0 {
		t.Errorf("calls of the delegate = %d, want 0", n)
	}
}

func TestFederateCallsTriggeredAgent(t *testing.T) {
	ts := newTestServer(t)
	remote, _ := ts.add("remote-echo", false, nil, nil)
	fed, agent := ts.addFederate("fed-unary", false, false, map[string]string{remote: ts.agentURL(remote)})
	resp := ts.postMessage("/agent/"+fed+"/", "go")
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body, remote) {
		t.Fatalf("call = %d: %s, want the result of the delegate", resp.Code, resp.Body)
	}
	tracker := agent.Config.Delegates.Agents[remote].Tracker
	if tracker.CallCount.Load() != 1 || !tracker.IsDone() {
		t.Errorf("tracker = %d calls, done %v, want one call that is done", tracker.CallCount.Load(), tracker.IsDone())
	}
	// The delegate is told who is calling it, and the federate reports the headers that it exchanged with the delegate.
	for _, want := range []string{"Request-Goto-A2a-Agent", "UpstreamHeaders", remote + " (A2A)"} {
		if !strings.Contains(resp.Body, want) {
			t.Errorf("response = %s, want it to have %q", resp.Body, want)
		}
	}
}

func TestFederateDisabledDelegateIsNotCalled(t *testing.T) {
	ts := newTestServer(t)
	remote, _ := ts.add("remote-off", false, nil, nil)
	cfg := &model.AgentConfig{Delegates: &model.DelegateConfig{
		MaxCalls: 1,
		Agents:   map[string]*model.DelegateAgentCall{remote: agentDelegate(remote, ts.agentURL(remote), true, "go")},
	}}
	fed, agent := ts.add("fed-off", false, &model.AgentBehavior{Federate: true}, cfg)
	resp := ts.postMessage("/agent/"+fed+"/", "go")
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body, "No agents were triggered") {
		t.Errorf("call = %d: %s", resp.Code, resp.Body)
	}
	if n := agent.Config.Delegates.Agents[remote].Tracker.CallCount.Load(); n != 0 {
		t.Errorf("calls of the disabled delegate = %d, want 0", n)
	}
}

func TestFederateMaxCalls(t *testing.T) {
	ts := newTestServer(t)
	agents := map[string]*model.DelegateAgentCall{}
	for i := 0; i < 3; i++ {
		remote, _ := ts.add("remote-max", false, nil, nil)
		agents[remote] = agentDelegate(remote, ts.agentURL(remote), false, "go")
	}
	cfg := &model.AgentConfig{Delegates: &model.DelegateConfig{MaxCalls: 2, Agents: agents}}
	fed, agent := ts.add("fed-max", false, &model.AgentBehavior{Federate: true}, cfg)
	if resp := ts.postMessage("/agent/"+fed+"/", "go"); resp.Code != http.StatusOK {
		t.Fatalf("call = %d: %s", resp.Code, resp.Body)
	}
	calls := int32(0)
	for _, d := range agent.Config.Delegates.Agents {
		calls += d.Tracker.CallCount.Load()
	}
	if calls != 2 {
		t.Errorf("calls = %d, want the 2 that the agent is allowed", calls)
	}
}

func TestFederateRelaysProgressOfStreamingDelegate(t *testing.T) {
	ts := newTestServer(t)
	remote, _ := ts.addStreamAgent("remote-relay", 3)
	fed, agent := ts.addFederate("fed-stream", true, false, map[string]string{remote: ts.agentURL(remote)})
	r, took := callWithin(t, 15*time.Second, ts.agentURL(fed), fed, "go")
	if r.Err != nil || !r.has("Result# 3 (of 3)") {
		t.Fatalf("texts = %v, err = %v (after %s), want the results of the delegate", r.Texts, r.Err, took)
	}
	tracker := agent.Config.Delegates.Agents[remote].Tracker
	if tracker.CallCount.Load() != 1 || tracker.ResponseCount.Load() < 3 || !tracker.IsDone() {
		t.Errorf("tracker = %d calls, %d responses, done %v, want one call with the progress that it made",
			tracker.CallCount.Load(), tracker.ResponseCount.Load(), tracker.IsDone())
	}
}

func TestFederateDelegateStatusIsPropagated(t *testing.T) {
	ts := newTestServer(t)
	remote, _ := ts.add("remote-fail", false, nil, nil)
	ts.setStatus(remote, "503")
	fed, _ := ts.addFederate("fed-status", false, false, map[string]string{remote: ts.agentURL(remote)})
	resp := ts.postMessage("/agent/"+fed+"/", "go")
	if resp.Code != 503 || resp.Header.Get("Goto-Forced-Status") != "503" {
		t.Errorf("call = %d, %v, want the status of the delegate", resp.Code, resp.Header)
	}
}

func TestFederateUnreachableToolDelegate(t *testing.T) {
	ts := newTestServer(t)
	cfg := &model.AgentConfig{Delegates: &model.DelegateConfig{
		MaxCalls: 1,
		Tools: map[string]*model.DelegateToolCall{"t1": {
			ToolCall: &mcpToolCall{Tool: "echo-tool", URL: "http://127.0.0.1:1/mcp", RequestTimeout: "3s"},
			Triggers: []string{"go"},
		}},
	}}
	fed, agent := ts.add("fed-tool", false, &model.AgentBehavior{Federate: true}, cfg)
	start := time.Now()
	resp := ts.postMessage("/agent/"+fed+"/", "go")
	if took := time.Since(start); took > 15*time.Second {
		t.Errorf("call took %s, want the tool that can't be reached to fail soon", took)
	}
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body, "No tool produced any results") {
		t.Errorf("call = %d: %s, want it to say that the tool gave nothing", resp.Code, resp.Body)
	}
	tracker := agent.Config.Delegates.Tools["t1"].Tracker
	if tracker.CallCount.Load() != 1 || !tracker.IsDone() {
		t.Errorf("tracker = %d calls, done %v, want one call that is done", tracker.CallCount.Load(), tracker.IsDone())
	}
}
