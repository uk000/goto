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
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	a2aclient "goto/pkg/ai/a2a/client"
	"goto/pkg/ai/a2a/model"
	"goto/pkg/types"
	"goto/pkg/util"
	"goto/pkg/util/timeline"

	a2aproto "trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
)

// stubTaskHandler is the handler of a task that has no task manager behind it. The calls that the tests don't make
// are not implemented.
type stubTaskHandler struct {
	taskmanager.TaskHandler
	updateErr error
	updates   atomic.Int32
	artifacts atomic.Int32
	canceled  atomic.Int32
	cleaned   atomic.Int32
	lock      sync.Mutex
	messages  []*a2aproto.Message
	sent      []a2aproto.Artifact
}

// sentText tells everything that was handed over to the task manager, as the task manager marshals it.
func (h *stubTaskHandler) sentText() string {
	h.lock.Lock()
	defer h.lock.Unlock()
	return util.ToJSONText(h.messages) + util.ToJSONText(h.sent)
}

func (h *stubTaskHandler) UpdateTaskState(taskID *string, state a2aproto.TaskState, message *a2aproto.Message) error {
	h.updates.Add(1)
	h.lock.Lock()
	h.messages = append(h.messages, message)
	h.lock.Unlock()
	if state == a2aproto.TaskStateCanceled {
		h.canceled.Add(1)
	}
	return h.updateErr
}

func (h *stubTaskHandler) CleanTask(taskID *string) error {
	h.cleaned.Add(1)
	return nil
}

func (h *stubTaskHandler) AddArtifact(taskID *string, artifact a2aproto.Artifact, isFinal bool, needMoreData bool) error {
	h.artifacts.Add(1)
	h.lock.Lock()
	h.sent = append(h.sent, artifact)
	h.lock.Unlock()
	return h.updateErr
}

// newUnitContext makes the context of a call to the agent, for tests that run a part of the agent without a request.
func newUnitContext(ctx context.Context, agent *model.Agent, handler taskmanager.TaskHandler) *AgentContext {
	ac := newAgentContext(agent.Port, "unit-server", "unit-listener", agent, http.Header{}, &util.RequestStore{})
	ac.task = &AgentTask{agent: agent, taskID: "unit-task", handler: handler}
	ac.ctx = ctx
	ac.timeline = &timeline.Timeline{}
	if federate, ok := agent.Behavior.Impl.(*AgentBehaviorFederate); ok {
		ac.triggers = federate.triggers
	}
	return ac
}

// callWithin calls the agent, and gives up at the deadline.
func callWithin(t *testing.T, d time.Duration, agentURL, name, message string) (r *reply, took time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	call := &a2aclient.AgentCall{Name: name, CardURL: agentURL, Message: message}
	call.Prepare()
	var mu sync.Mutex
	r = &reply{}
	start := time.Now()
	result, err := a2aclient.CallAgent(ctx, 1, call, func(id, out string, data any) {
		mu.Lock()
		defer mu.Unlock()
		if out != "" {
			r.Texts = append(r.Texts, out)
		}
	}, nil)
	took = time.Since(start)
	r.Result, r.Err = result, err
	return r, took
}

// addStreamAgent adds an agent that streams the given number of results, quickly.
func (ts *testServer) addStreamAgent(prefix string, count int) (string, *model.Agent) {
	ts.t.Helper()
	name, agent := ts.add(prefix, true, &model.AgentBehavior{Stream: true}, nil)
	payload := fmt.Sprintf(`{"textStream":["tick"],"streamCount":%d,"delay":{"min":"1ms","max":"1ms"}}`, count)
	if err := agent.SetPayload([]byte(payload)); err != nil {
		ts.t.Fatalf("SetPayload failed: %v", err)
	}
	return name, agent
}

// addFederate adds an agent that calls the remote agents when it is told "go".
func (ts *testServer) addFederate(prefix string, streaming, parallel bool, remoteCardURLs map[string]string) (string, *model.Agent) {
	ts.t.Helper()
	agents := map[string]*model.DelegateAgentCall{}
	for name, cardURL := range remoteCardURLs {
		agents[name] = &model.DelegateAgentCall{
			AgentCall: &a2aclient.AgentCall{Name: name, CardURL: cardURL, AgentURL: cardURL},
			Triggers:  []string{"go"},
		}
	}
	cfg := &model.AgentConfig{Delegates: &model.DelegateConfig{Agents: agents, MaxCalls: len(agents), Parallel: parallel}}
	return ts.add(prefix, streaming, &model.AgentBehavior{Federate: true}, cfg)
}

// The stream agent sends a message for each result. Each message was made of the one before it, so that the n-th was
// the text of the request with n prefixes.
func TestRegressionStreamMessagesDoNotAccumulate(t *testing.T) {
	ts := newTestServer(t)
	name, _ := ts.addStreamAgent("stream-text", 3)
	r := ts.call(name, "go {current}/{total}", nil)
	if r.Err != nil {
		t.Fatalf("call failed: %v", r.Err)
	}
	for i := 1; i <= 3; i++ {
		want := fmt.Sprintf("Result# %d (of 3): go %d/3", i, i)
		if !r.has(want) {
			t.Errorf("texts = %v, want one with [%s]", r.Texts, want)
		}
	}
	for _, text := range r.Texts {
		if strings.Count(text, "Result#") > 1 {
			t.Errorf("message [%s] has the results of the messages before it", text)
		}
	}
}

// A stop that is asked for when nothing is streaming is not a stop of the next stream.
func TestRegressionStopWithoutStreamDoesNotStopNextStream(t *testing.T) {
	ts := newTestServer(t)
	name, _ := ts.addStreamAgent("stream-stale", 3)
	if r := ts.call(name, "stop", nil); r.Err != nil {
		t.Fatalf("stop failed: %v", r.Err)
	}
	r := ts.call(name, "go", nil)
	if r.Err != nil || !r.has("Result# 1 (of 3)") || !r.has("Result# 3 (of 3)") {
		t.Errorf("texts = %v, err = %v, want the 3 results of a stream that was not stopped", r.Texts, r.Err)
	}
	if r.has("stop requested") {
		t.Errorf("texts = %v, want no stop for the stream", r.Texts)
	}
}

// A stream reads the payload of the agent and whether it was asked to stop, while other requests ask it to stop and
// replace the payload. (One stream is sent at a time, as the delays that streams wait for are shared.)
func TestRegressionStreamWhileStopsAndPayloadsAreSet(t *testing.T) {
	ts := newTestServer(t)
	name, agent := ts.addStreamAgent("stream-race", 4)
	const streams, stoppers, stops, payloads = 3, 3, 4, 20
	payload := `{"textStream":["tick"],"streamCount":4,"delay":{"min":"1ms","max":"1ms"}}`
	var wg sync.WaitGroup
	streamReplies := make([]*reply, streams)
	stopReplies := make([]*reply, stoppers*stops)
	var payloadFailures atomic.Int32
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < streams; i++ {
			streamReplies[i] = callAgent(t, ts.agentURL(name), name, "go", nil)
		}
	}()
	for s := 0; s < stoppers; s++ {
		wg.Add(1)
		go func(s int) {
			defer wg.Done()
			for i := 0; i < stops; i++ {
				stopReplies[s*stops+i] = callAgent(t, ts.agentURL(name), name, "stop", nil)
			}
		}(s)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < payloads; i++ {
			if err := agent.SetPayload([]byte(payload)); err != nil {
				payloadFailures.Add(1)
			}
		}
	}()
	wg.Wait()
	if payloadFailures.Load() != 0 {
		t.Errorf("%d payloads failed to be set", payloadFailures.Load())
	}
	for i, r := range append(streamReplies, stopReplies...) {
		if r == nil || r.Err != nil {
			t.Errorf("call %d = %+v, want no error", i, r)
		}
	}
}

// The values that tell an agent where to call come from the text of the request, which can be anything.
func TestRegressionExtractJSONValuesIgnoresWrongTypes(t *testing.T) {
	overrides := extractJSONValues([]map[string]any{
		{"tool": 5, "agent": true, "url": 7, "input": 1, "args": "text"},
		{"tool": "t1", "url": "http://a/", "args": map[string]any{"count": 2}},
		{"agent": "a1", "input": "hello"},
	})
	if len(overrides) != 2 {
		t.Fatalf("overrides = %v, want only the ones of t1 and a1", overrides)
	}
	if o := overrides["t1"]; o == nil || o.url != "http://a/" || o.args["count"] != 2 {
		t.Errorf("override of t1 = %+v", o)
	}
	if o := overrides["a1"]; o == nil || o.remoteInput != "hello" {
		t.Errorf("override of a1 = %+v", o)
	}
}

// A feature that is hinted as off, or is given as false, is off.
func TestRegressionFeatureFlagsKeepTheirValue(t *testing.T) {
	ts := newTestServer(t)
	_, agent := ts.add("features", false, nil, nil)
	ac := newUnitContext(ctxWithTimeout(t), agent, &stubTaskHandler{})
	tests := []struct {
		key        string
		value      any
		resultOnly bool
		noEvents   bool
	}{
		{"resultOnly", true, true, false},
		{"resultOnly", false, false, false},
		{"result", true, false, false},
		{"result", false, true, false},
		{"noEvents", true, false, true},
		{"noEvents", false, false, false},
		{"events", true, false, false},
		{"events", false, false, true},
	}
	for _, tt := range tests {
		ac.timeline.ResultOnly, ac.timeline.NoEvents = false, false
		ac.detectAndConfigureFeature(tt.key, tt.value)
		if ac.timeline.ResultOnly != tt.resultOnly || ac.timeline.NoEvents != tt.noEvents {
			t.Errorf("feature %s=%v: resultOnly = %v, noEvents = %v, want %v, %v",
				tt.key, tt.value, ac.timeline.ResultOnly, ac.timeline.NoEvents, tt.resultOnly, tt.noEvents)
		}
	}
}

func TestRegressionTaskUpdateWithoutTask(t *testing.T) {
	ts := newTestServer(t)
	_, agent := ts.add("notask", false, nil, nil)
	ac := newAgentContext(ts.port, "srv", "listener", agent, http.Header{}, &util.RequestStore{})
	if err := ac.sendTaskStatusUpdate(a2aproto.TaskStateWorking, "update"); err == nil {
		t.Errorf("sendTaskStatusUpdate without a task = nil, want an error")
	}
}

// An agent that has no delegates can still be told things that look like inputs for delegates.
func TestRegressionInputHintsForAgentWithoutDelegates(t *testing.T) {
	ts := newTestServer(t)
	_, agent := ts.add("nodelegates", false, &model.AgentBehavior{Federate: true}, nil)
	ac := newUnitContext(ctxWithTimeout(t), agent, &stubTaskHandler{})
	ac.inputText = `go t1:[{"count":2}] a1:[hello] {"tool":"t1","args":{"count":3}}`
	ac.detectRemoteCalls()
	if len(ac.tools)+len(ac.agents) != 0 {
		t.Errorf("matched = %v %v, want nothing", ac.tools, ac.agents)
	}
}

func TestRegressionDelegateWithoutCallIsRejected(t *testing.T) {
	ts := newTestServer(t)
	valid := &model.DelegateToolCall{ToolCall: &mcpToolCall{Tool: "t", URL: "localhost:1/mcp"}}
	tests := []struct {
		name string
		cfg  *model.DelegateConfig
	}{
		{"nil tool", &model.DelegateConfig{Tools: map[string]*model.DelegateToolCall{"t1": nil}}},
		{"tool without call", &model.DelegateConfig{Tools: map[string]*model.DelegateToolCall{"t1": {}}}},
		{"nil agent", &model.DelegateConfig{Agents: map[string]*model.DelegateAgentCall{"a1": nil}}},
		{"agent without call", &model.DelegateConfig{Agents: map[string]*model.DelegateAgentCall{"a1": {}}}},
		{"nil http", &model.DelegateConfig{Tools: map[string]*model.DelegateToolCall{"t1": valid}, HTTP: map[string]*model.DelegateHTTPCall{"h1": nil}}},
	}
	for _, tt := range tests {
		agent := ts.newAgent(uniqueName("badcall"), false, &model.AgentBehavior{Federate: true}, &model.AgentConfig{Delegates: tt.cfg})
		err := GetOrAddServer(ts.port).AddAgent(agent)
		if err == nil || !strings.Contains(err.Error(), "Missing") {
			t.Errorf("%s: AddAgent = %v, want an error about the missing call", tt.name, err)
		}
		if GetAgent(ts.port, agent.Card.Name) != nil {
			t.Errorf("%s: the agent was added", tt.name)
		}
	}
}

func TestRegressionStatusConfigIsLoggedAsA2A(t *testing.T) {
	ts := newTestServer(t)
	var logs []string
	rr := recordRequest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		configureStatus(w, r)
		logs = append(logs, util.GetRequestStore(r).LogMessages...)
	}), ts.port, "POST", "/a2a/agents/x/status/configure", `{"statuses":[502],"times":1,"match":{"uri":{"exact":"/agent/none/"}}}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("configure = %d: %s", rr.Code, rr.Body.String())
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "A2A Port") || strings.Contains(logs[0], "MCP") {
		t.Errorf("logs = %v, want the config to be logged for A2A", logs)
	}
}

// The agents of the server are read by the admin API while others are added.
func TestRegressionServersReportedWhileAgentsAreAdded(t *testing.T) {
	ts := newTestServer(t)
	s := GetOrAddServer(ts.port)
	var wg sync.WaitGroup
	var failures atomic.Int32
	const adders, perAdder, readers, reads = 3, 8, 3, 15
	for a := 0; a < adders; a++ {
		wg.Add(1)
		go func(a int) {
			defer wg.Done()
			for i := 0; i < perAdder; i++ {
				if err := s.AddAgent(ts.newAgent(fmt.Sprintf("srvread-%d-%d-%d", ts.port, a, i), false, nil, nil)); err != nil {
					failures.Add(1)
				}
			}
		}(a)
	}
	for _, path := range []string{"/a2a/servers", "/a2a/agents/all", "/a2a/agents/names"} {
		for i := 0; i < readers/3; i++ {
			wg.Add(1)
			go func(path string) {
				defer wg.Done()
				for j := 0; j < reads; j++ {
					if resp, err := ts.try("GET", path, ""); err != nil || resp.Code != http.StatusOK {
						failures.Add(1)
					}
				}
			}(path)
		}
	}
	wg.Wait()
	if n := failures.Load(); n != 0 {
		t.Errorf("%d calls failed, want 0", n)
	}
	if got := len(GetAgentNames(ts.port)[ts.port]); got != adders*perAdder {
		t.Errorf("agents on the port = %d, want %d", got, adders*perAdder)
	}
}

// The payload of an agent that has no config is set by creating the config, while the names of the agents are read.
func TestRegressionPayloadSetOnAgentWithoutConfigWhileNamesAreListed(t *testing.T) {
	ts := newTestServer(t)
	const rounds = 30
	agents := make([]*model.Agent, rounds)
	for i := range agents {
		agents[i] = ts.addAgent(ts.newAgent(uniqueName("nocfg"), false, nil, nil))
	}
	var wg sync.WaitGroup
	var failures atomic.Int32
	wg.Add(2)
	go func() {
		defer wg.Done()
		for _, agent := range agents {
			if err := agent.SetPayload([]byte(`{"text":"x"}`)); err != nil {
				failures.Add(1)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < rounds*3; i++ {
			GetAgentNames(ts.port)
		}
	}()
	wg.Wait()
	if failures.Load() != 0 {
		t.Errorf("%d payloads failed to be set", failures.Load())
	}
}

// What every delegate reported about its upstream is reported, and not only what the last one did.
func TestRegressionUpstreamHeadersOfAllDelegatesAreReported(t *testing.T) {
	for _, parallel := range []bool{false, true} {
		t.Run(fmt.Sprintf("parallel=%v", parallel), func(t *testing.T) {
			ts := newTestServer(t)
			remotes := map[string]string{}
			for i := 0; i < 4; i++ {
				name, _ := ts.add("upstream", false, nil, nil)
				remotes[name] = ts.agentURL(name)
			}
			fed, _ := ts.addFederate("federate", false, parallel, remotes)
			resp := ts.postMessage("/agent/"+fed+"/", "go")
			if resp.Code != http.StatusOK {
				t.Fatalf("call = %d: %s", resp.Code, resp.Body)
			}
			for name := range remotes {
				if !strings.Contains(resp.Body, name+" (A2A)") {
					t.Errorf("response has no upstream headers of [%s]: %s", name, resp.Body)
				}
			}
		})
	}
}

// A call that is not streamed has nobody to read the progress of the agents that it calls, and an agent that streams
// stopped it after a few updates.
func TestRegressionUnaryFederateCallsStreamingAgent(t *testing.T) {
	ts := newTestServer(t)
	remote, _ := ts.addStreamAgent("remote-stream", 10)
	fed, _ := ts.addFederate("federate-unary", false, false, map[string]string{remote: ts.agentURL(remote)})
	r, took := callWithin(t, 8*time.Second, ts.agentURL(fed), fed, "go")
	if r.Err != nil {
		t.Fatalf("call failed after %s: %v", took, r.Err)
	}
	if r.Result == nil {
		t.Errorf("call has no result")
	}
}

// A streamed call waits for the progress of the agents that it calls to end, which a call that never started doesn't.
func TestRegressionStreamingFederateWithUnreachableAgent(t *testing.T) {
	ts := newTestServer(t)
	fed, _ := ts.addFederate("federate-dead", true, false, map[string]string{"dead": "http://127.0.0.1:1/agent/dead/"})
	r, took := callWithin(t, 8*time.Second, ts.agentURL(fed), fed, "go")
	if took > 5*time.Second {
		t.Errorf("call took %s (err: %v), want it to end when the agent can't be reached", took, r.Err)
	}
}

// What reads the progress of a delegate stops when it can't send updates or the call is cancelled. What sends the
// progress goes on, and waits for it to be read.
func TestRegressionConsumersDrainProgressWhenTheyStop(t *testing.T) {
	ts := newTestServer(t)
	_, agent := ts.add("drain", true, &model.AgentBehavior{Federate: true}, nil)
	federate := agent.Behavior.Impl.(*AgentBehaviorFederate)
	const sends = 40

	produce := func(t *testing.T, send func(i int), closeIt func()) {
		done := make(chan struct{})
		go func() {
			defer close(done)
			for i := 0; i < sends; i++ {
				send(i)
			}
			closeIt()
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("what sends the progress is blocked, as nothing reads it any more")
		}
	}
	waitFor := func(t *testing.T, wg *sync.WaitGroup) {
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("what reads the progress did not end")
		}
	}
	cancelled := func() context.Context {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return ctx
	}

	for _, tt := range []struct {
		name string
		ctx  context.Context
		err  error
	}{
		{"update fails", ctxWithTimeout(t), errors.New("task is gone")},
		{"cancelled", cancelled(), nil},
	} {
		t.Run("upstream/"+tt.name, func(t *testing.T) {
			ac := newUnitContext(tt.ctx, agent, &stubTaskHandler{updateErr: tt.err})
			dCtx := &DelegateCallContext{name: "remote", url: "u", tracker: model.NewDelegateTracker(), upstreamProgress: make(chan *types.Pair[string, any], 10)}
			wg := &sync.WaitGroup{}
			wg.Add(1)
			go federate.processUpstreamUpdates(ac, dCtx, wg)
			produce(t, func(i int) { dCtx.upstreamProgress <- types.NewPair[string, any](fmt.Sprintf("update %d", i), nil) },
				func() { close(dCtx.upstreamProgress) })
			waitFor(t, wg)
		})
		t.Run("local/"+tt.name, func(t *testing.T) {
			ac := newUnitContext(tt.ctx, agent, &stubTaskHandler{updateErr: tt.err})
			ac.localProgress = make(chan *types.Pair[string, any], 10)
			wg := &sync.WaitGroup{}
			wg.Add(1)
			go federate.processLocalUpdates(ac, wg)
			produce(t, func(i int) { ac.localProgress <- types.NewPair[string, any]("update", fmt.Sprintf("update %d", i)) },
				func() { close(ac.localProgress) })
			waitFor(t, wg)
		})
	}
}

// What is sent to the client is marshaled later by the task manager, on the goroutine that serves the client. It can't
// be the data that the agent goes on to change.
func TestRegressionSendDataHandsOverACopy(t *testing.T) {
	for _, surface := range []bool{false, true} {
		t.Run(fmt.Sprintf("surface=%v", surface), func(t *testing.T) {
			ts := newTestServer(t)
			_, agent := ts.add("senddata", false, nil, nil)
			handler := &stubTaskHandler{}
			ac := newUnitContext(ctxWithTimeout(t), agent, handler)
			ac.surfaceData = surface
			live := map[string]any{"status": "before", "list": []any{"first"}, "nested": map[string]any{"n": 1}}
			if err := ac.sendData("Result", live); err != nil {
				t.Fatalf("sendData failed: %v", err)
			}
			live["status"] = "after"
			live["list"].([]any)[0] = "changed"
			live["nested"].(map[string]any)["n"] = 2
			live["extra"] = true
			sent := handler.sentText()
			for _, want := range []string{`"status":"before"`, `"first"`, `"n":1`} {
				if !strings.Contains(sent, want) {
					t.Errorf("sent = %s, want it to have %s as it was when it was sent", sent, want)
				}
			}
			for _, unwanted := range []string{`after`, `changed`, `"n":2`, `extra`} {
				if strings.Contains(sent, unwanted) {
					t.Errorf("sent = %s, want it to be without %s that came after", sent, unwanted)
				}
			}
		})
	}
}

// The agent adds events and data to its timeline from the goroutines of its delegates, while the results of the calls that
// are done are reported from the same timeline.
func TestRegressionTimelineChangesWhileResultsAreReported(t *testing.T) {
	ts := newTestServer(t)
	_, agent := ts.add("timeline-agent", false, nil, nil)
	ac := newUnitContext(ctxWithTimeout(t), agent, &stubTaskHandler{})
	ac.timeline = timeline.NewTimeline(agent.Port, "unit", nil, nil, nil, nil, nil, nil)
	reported := a2aclient.NewA2AResult("http://server", &a2aclient.AgentCall{Name: "reported"}, ac.timeline)
	const writers, changes = 4, 50
	start := make(chan struct{})
	done := make(chan struct{})
	var wg sync.WaitGroup
	var missing atomic.Int32
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			for j := 0; j < changes; j++ {
				ac.AddEvent(fmt.Sprintf("event %d.%d", i, j))
				ac.AddData(fmt.Sprintf("data %d.%d", i, j), j, false)
			}
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for j := 0; j < changes; j++ {
			if reported.Snapshot()["Timeline"] == nil {
				missing.Add(1)
			}
		}
	}()
	close(start)
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatalf("the timeline was not changed and reported in time")
	}
	if n := missing.Load(); n != 0 {
		t.Errorf("snapshots without the timeline = %d, want 0", n)
	}
	if len(ac.timeline.Events) != writers*changes || len(ac.timeline.Data) != writers*changes {
		t.Errorf("events = %d, data = %d, want %d of each", len(ac.timeline.Events), len(ac.timeline.Data), writers*changes)
	}
}
