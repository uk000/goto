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

package a2aclient

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"weak"

	"goto/pkg/util"
	"goto/pkg/util/timeline"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func newResult(t *testing.T, call *AgentCall) *A2AResult {
	t.Helper()
	setup(t)
	if call == nil {
		call = &AgentCall{Name: "agent-x"}
	}
	return NewA2AResult("http://server", call, timeline.NewTimeline(1, call.Name, nil, nil, nil, nil, nil, nil))
}

func TestNewA2AResult(t *testing.T) {
	r := newResult(t, &AgentCall{Name: "agent-x"})
	if r.ID != "[agent-x]@http://server" || r.Server != "http://server" || r.Agent != "agent-x" {
		t.Errorf("result = %+v", r)
	}
	if r.CallResults == nil || r.UpstreamStatuses == nil || r.RemoteGotos == nil || r.Timeline == nil || r.ServerInfo == nil {
		t.Errorf("result has nil collections: %+v", r)
	}
}

func TestGetOrAddCall(t *testing.T) {
	r := newResult(t, nil)
	c1 := r.getOrAddCall("req-1")
	if c1 == nil || c1.RequestID != "req-1" || c1.Data == nil || c1.parent != r {
		t.Fatalf("call = %+v", c1)
	}
	if r.getOrAddCall("req-1") != c1 {
		t.Error("getOrAddCall must return the call that exists")
	}
	if r.getOrAddCall("req-2") == c1 || len(r.CallResults) != 2 {
		t.Errorf("call results = %v, want 2 distinct calls", keys(r.CallResults))
	}
}

func TestAddOrUpdateCallAndStore(t *testing.T) {
	r := newResult(t, nil)
	info := &timeline.GotoClientInfo{}
	if got := r.addOrUpdateCall("a", nil, info); got == nil || got.ClientInfo != info || r.CallResults["a"] != got {
		t.Errorf("addOrUpdateCall(nil) = %+v", got)
	}
	fresh := &A2ACallResult{RequestID: "b", Content: []string{"x"}}
	if got := r.addOrUpdateCall("b", fresh, info); got != fresh || r.CallResults["b"] != fresh {
		t.Errorf("addOrUpdateCall(new) must store the given call")
	}
	merged := r.addOrUpdateCall("b", &A2ACallResult{Content: []string{"y"}, Data: map[string]any{"k": 1}}, info)
	if merged != fresh || len(merged.Content) != 2 || merged.Data["k"] != 1 {
		t.Errorf("merged = %+v, want the given call to be merged into the one that exists", merged)
	}
	r.storeA2ACallResult("c", nil, info)
	if r.CallResults["c"] != nil {
		t.Error("storing a nil call result must not add a call")
	}
	r.storeA2ACallResult("c", &A2ACallResult{RequestID: "c"}, info)
	if r.CallResults["c"] == nil || r.CallResults["c"].ClientInfo != info {
		t.Errorf("stored call = %+v", r.CallResults["c"])
	}
}

func TestStoreHeaders(t *testing.T) {
	r := newResult(t, &AgentCall{Name: "agent-x"})
	req := http.Header{"X-Req": []string{"1"}}
	resp := http.Header{"X-Resp": []string{"2"}}
	r.storeHeaders("req-1", req, resp, 202)
	cr := r.CallResults["req-1"]
	if cr == nil || cr.ResponseStatus != 202 || cr.RequestHeaders.Get("X-Req") != "1" || cr.ResponseHeaders.Get("X-Resp") != "2" {
		t.Fatalf("call = %+v", cr)
	}
	if r.LastResponseStatus != 202 || r.LastRequestHeaders.Get("X-Req") != "1" || r.LastResponseHeaders.Get("X-Resp") != "2" {
		t.Errorf("last = %d %v %v", r.LastResponseStatus, r.LastRequestHeaders, r.LastResponseHeaders)
	}
	if r.UpstreamStatuses["agent-x"] != 202 {
		t.Errorf("UpstreamStatuses = %v, want the status of the agent", r.UpstreamStatuses)
	}
}

func TestCallResultMerge(t *testing.T) {
	cr := &A2ACallResult{Content: []string{"a"}, RequestHeaders: http.Header{"A": {"1"}}, ResponseHeaders: http.Header{"B": {"2"}}}
	tl := timeline.NewTimeline(1, "remote", nil, nil, nil, nil, nil, nil)
	cr.merge(&A2ACallResult{Content: []string{"b", "c"}, RemoteTimeline: tl})
	if len(cr.Content) != 3 || cr.Data != nil || cr.RemoteTimeline != tl {
		t.Errorf("merged = %+v", cr)
	}
	if cr.RequestHeaders.Get("A") != "1" || cr.ResponseHeaders.Get("B") != "2" {
		t.Errorf("headers = %v / %v, want the ones that the other call doesn't have to be kept", cr.RequestHeaders, cr.ResponseHeaders)
	}
	cr.merge(&A2ACallResult{RequestHeaders: http.Header{"C": {"3"}}, ResponseHeaders: http.Header{"D": {"4"}}, Data: map[string]any{"k": "v"}})
	if cr.RequestHeaders.Get("C") != "3" || cr.ResponseHeaders.Get("D") != "4" || cr.Data["k"] != "v" {
		t.Errorf("merged = %+v, want the headers and data of the other call", cr)
	}
}

func TestStoreRemoteTimeline(t *testing.T) {
	r := newResult(t, &AgentCall{Name: "agent-x", ResultOnly: true})
	cr := r.getOrAddCall("r1")
	tl := timeline.NewTimeline(2, "remote", nil, nil, nil, nil, nil, nil)
	tl.AddEvent("remote", "something happened")
	cr.storeRemoteTimeline(tl)
	if cr.RemoteTimeline != tl || tl.Events != nil {
		t.Errorf("RemoteTimeline = %+v, want it stored without events for a result only call", cr.RemoteTimeline)
	}
	cr.storeRemoteTimeline("not a timeline")
	if cr.RemoteTimeline != tl {
		t.Error("data that is not a timeline must be ignored")
	}

	r2 := newResult(t, &AgentCall{Name: "agent-y"})
	cr2 := r2.getOrAddCall("r1")
	tl2 := timeline.NewTimeline(2, "remote", nil, nil, nil, nil, nil, nil)
	tl2.AddEvent("remote", "kept")
	cr2.storeRemoteTimeline(tl2)
	if len(tl2.Events) != 1 {
		t.Errorf("events = %v, want them kept when the call is not result only", tl2.Events)
	}
}

func TestToObject(t *testing.T) {
	r := newResult(t, &AgentCall{Name: "agent-x"})
	if got := r.ToObject(); len(got) != 1 || got["Timeline"] != r.Timeline {
		t.Errorf("ToObject of a result without calls = %v, want only the timeline", got)
	}
	c1 := r.getOrAddCall("r1")
	c1.Content = []string{"hello", "world"}
	c1.ResponseStatus = 200
	got := r.ToObject()
	content, _ := got["Content"].([]string)
	if len(content) != 2 {
		t.Errorf("Content = %v, want the content of the calls", got["Content"])
	}
	calls, ok := r.Timeline.Data["A2ACalls"].(map[string]map[string]any)
	if !ok || calls[r.ID]["r1"] != c1 {
		t.Errorf("A2ACalls = %v, want the calls of the result", r.Timeline.Data["A2ACalls"])
	}
	if _, err := json.Marshal(got); err != nil {
		t.Errorf("the object can't be marshaled: %v", err)
	}
}

func TestSnapshotIsDetached(t *testing.T) {
	r := newResult(t, &AgentCall{Name: "agent-x"})
	r.getOrAddCall("r1").Content = []string{"hello"}
	r.Timeline.AddEvent("x", "before the snapshot")
	snapshot := r.Snapshot()
	if _, live := snapshot["Timeline"].(*timeline.Timeline); live || snapshot["Timeline"] == nil {
		t.Fatalf("Timeline = %T, want what has none of the live timeline", snapshot["Timeline"])
	}
	if content, _ := snapshot["Content"].([]any); len(content) != 1 || content[0] != "hello" {
		t.Errorf("Content = %v, want the content of the calls", snapshot["Content"])
	}
	before, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("the snapshot can't be marshaled: %v", err)
	}
	// What the timeline goes on to do doesn't change the snapshot.
	r.Timeline.AddEvent("x", "after the snapshot")
	r.Timeline.Data["late"] = 1
	r.Timeline.Status = 503
	after, _ := json.Marshal(snapshot)
	if string(before) != string(after) {
		t.Errorf("snapshot changed with the timeline:\nbefore: %s\nafter:  %s", before, after)
	}
	if !strings.Contains(string(before), "before the snapshot") || !strings.Contains(string(before), "A2ACalls") {
		t.Errorf("snapshot = %s, want the events and the calls of the timeline", before)
	}
	if strings.Contains(string(before), "after the snapshot") {
		t.Errorf("snapshot = %s, want it as of when it was taken", before)
	}
	// What is reported is what ToObject reports.
	if got := r.ToObject(); got["Timeline"] != r.Timeline {
		t.Errorf("ToObject Timeline = %v, want the live timeline", got["Timeline"])
	}
}

// What is done to a timeline waits for what is done to the same timeline, and not for what is done to the others.
func TestUpdateTimelineLockIsOfTheTimeline(t *testing.T) {
	t1 := timeline.NewTimeline(1, "one", nil, nil, nil, nil, nil, nil)
	t2 := timeline.NewTimeline(2, "two", nil, nil, nil, nil, nil, nil)
	holding := make(chan struct{})
	release := make(chan struct{})
	held := make(chan struct{})
	go func() {
		defer close(held)
		UpdateTimeline(t1, func() {
			close(holding)
			<-release
		})
	}()
	defer func() {
		close(release)
		<-held
	}()
	select {
	case <-holding:
	case <-time.After(10 * time.Second):
		t.Fatalf("the update of the first timeline did not start")
	}
	other := make(chan struct{})
	go func() {
		defer close(other)
		UpdateTimeline(t2, func() {})
	}()
	select {
	case <-other:
	case <-time.After(10 * time.Second):
		t.Fatalf("the update of another timeline waited for the update of the first")
	}
	same := make(chan struct{})
	go func() {
		defer close(same)
		UpdateTimeline(t1, func() {})
	}()
	select {
	case <-same:
		t.Errorf("the update of the timeline did not wait for the update that was in progress")
	case <-time.After(200 * time.Millisecond):
	}
	// A call without a timeline has nothing to wait for.
	ran := false
	UpdateTimeline(nil, func() { ran = true })
	if !ran {
		t.Errorf("the update without a timeline did not run")
	}
}

func TestUpdateTimelineLockIsForgottenWithTheTimeline(t *testing.T) {
	key := func() weak.Pointer[timeline.Timeline] {
		tl := timeline.NewTimeline(1, "gone", nil, nil, nil, nil, nil, nil)
		UpdateTimeline(tl, func() {})
		key := weak.Make(tl)
		if _, ok := timelineLocks.Load(key); !ok {
			t.Fatalf("the lock of the timeline was not kept while the timeline is in use")
		}
		return key
	}()
	deadline := time.Now().Add(20 * time.Second)
	for {
		runtime.GC()
		if _, ok := timelineLocks.Load(key); !ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the lock of the timeline is kept after the timeline is gone")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestToObjectResultOnly(t *testing.T) {
	r := newResult(t, &AgentCall{Name: "agent-x", ResultOnly: true})
	r.Timeline.AddEvent("x", "an event")
	c1 := r.getOrAddCall("r1")
	c1.Content = []string{"hello"}
	got := r.ToObject()
	if _, found := got["Content"]; found {
		t.Errorf("object = %v, want no content for a result only call", got)
	}
	if r.Timeline.Events != nil {
		t.Errorf("events = %v, want none for a result only call", r.Timeline.Events)
	}
	if c1.Content != nil {
		t.Errorf("call content = %v, want it dropped from the reported calls", c1.Content)
	}
}

func TestToMCP(t *testing.T) {
	r := newResult(t, &AgentCall{Name: "agent-x"})
	r.LastResponseStatus = 201
	r.LastRequestHeaders = http.Header{"Req": {"1"}}
	r.LastResponseHeaders = http.Header{"Resp": {"2"}}
	r.UpstreamStatuses["agent-x"] = 201
	r.LastRemoteHeaders = map[string]any{"RemoteHeader": "x"}
	c1 := r.getOrAddCall("r1")
	c1.Content = []string{"one", "two"}
	res := r.ToMCP(map[string]any{"extra": "yes"})
	if len(res.Content) != 2 {
		t.Fatalf("content = %v, want the content of the calls", res.Content)
	}
	for i, want := range []string{"one", "two"} {
		if tc, ok := res.Content[i].(*gomcp.TextContent); !ok || tc.Text != want {
			t.Errorf("content[%d] = %v, want %q", i, res.Content[i], want)
		}
	}
	if res.StructuredContent != r.Timeline {
		t.Errorf("structured content = %v, want the timeline", res.StructuredContent)
	}
	if r.Timeline.Status != 201 || r.Timeline.UpstreamStatuses["agent-x"] != 201 || r.Timeline.Data["extra"] != "yes" {
		t.Errorf("timeline = status %d, upstream statuses %v, data %v", r.Timeline.Status, r.Timeline.UpstreamStatuses, r.Timeline.Data)
	}
	if r.Timeline.UpstreamHeaders["RemoteHeader"] != "x" || r.Timeline.UpstreamHeaders["agent-x (A2A)"] == nil {
		t.Errorf("upstream headers = %v", r.Timeline.UpstreamHeaders)
	}
}

func TestToMCPOptions(t *testing.T) {
	r := newResult(t, &AgentCall{Name: "agent-x", NoCallDetails: true, NoEvents: true})
	r.Timeline.AddEvent("x", "an event")
	r.getOrAddCall("r1").Content = []string{"hidden"}
	res := r.ToMCP(nil)
	if len(res.Content) != 0 {
		t.Errorf("content = %v, want none without call details", res.Content)
	}
	if r.Timeline.Events != nil {
		t.Errorf("events = %v, want none with no events", r.Timeline.Events)
	}
	if _, found := r.Timeline.Data["A2ACalls"]; found {
		t.Errorf("data = %v, want no calls without call details", r.Timeline.Data)
	}

	r2 := newResult(t, &AgentCall{Name: "agent-y", ResultOnly: true})
	r2.getOrAddCall("r1").Content = []string{"hidden"}
	if res := r2.ToMCP(nil); len(res.Content) != 0 {
		t.Errorf("content = %v, want none for a result only call", res.Content)
	}
}

func TestResultJSONHidesStatusFields(t *testing.T) {
	r := newResult(t, nil)
	r.LastResponseStatus = 500
	r.LastRequestHeaders = http.Header{"A": {"b"}}
	r.LastResponseHeaders = http.Header{"C": {"d"}}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"LastRequestHeaders", "LastResponseHeaders", "LastResponseStatus"} {
		if _, found := m[k]; found {
			t.Errorf("%s is reported in %s", k, b)
		}
	}
}

func TestResultHelpersAreSafeForConcurrentUse(t *testing.T) {
	r := newResult(t, nil)
	rs := &util.RequestStore{}
	var wg sync.WaitGroup
	const workers, each = 8, 50
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < each; j++ {
				id := fmt.Sprintf("req-%d-%d", i, j)
				r.getOrAddCall(id)
				r.storeHeaders(id, http.Header{"A": {"b"}}, http.Header{"C": {"d"}}, 200)
				r.addRemoteGotos("hop-1", "hop-2")
				r.setLastRemoteHeaders(map[string]any{"k": j})
				r.recordUpstreamStatuses(rs, map[string]any{"up": 200})
				r.addOrUpdateCall(id, &A2ACallResult{Content: []string{"x"}}, nil)
			}
		}(i)
	}
	wg.Wait()
	if len(r.CallResults) != workers*each {
		t.Errorf("call results = %d, want %d", len(r.CallResults), workers*each)
	}
	if !r.RemoteGotos["hop-1"] || !r.RemoteGotos["hop-2"] {
		t.Errorf("RemoteGotos = %v", r.RemoteGotos)
	}
	if r.UpstreamStatuses["-"] == nil || rs.UpstreamStatuses["-"] == nil {
		t.Errorf("upstream statuses = %v / %v, want the reported ones to be recorded", r.UpstreamStatuses, rs.UpstreamStatuses)
	}
}
