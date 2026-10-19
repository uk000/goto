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

package mcpclient

import (
	"net/http"
	"reflect"
	"sort"
	"testing"

	"goto/pkg/util/timeline"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func newTestResult(t *testing.T, tc *ToolCall) *MCPResult {
	t.Helper()
	setup(t)
	if tc == nil {
		tc = newToolCall("echo", "http://srv.test/mcp")
	}
	return NewMCPResult("http://srv.test/mcp", tc, timeline.NewTimeline(nextPort(), "result-test", nil, nil, nil, nil, nil, nil))
}

func toolResult(text string, structured any) *gomcp.CallToolResult {
	res := textResult(text)
	res.StructuredContent = structured
	return res
}

func sortedKeys(m map[string]bool) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestNewMCPResult(t *testing.T) {
	r := newTestResult(t, nil)
	if r.ID != "[echo]@http://srv.test/mcp" || r.Server != "http://srv.test/mcp" || r.Tool != "echo" {
		t.Errorf("result = id %q server %q tool %q", r.ID, r.Server, r.Tool)
	}
	if r.CallResults == nil || r.RemoteGotos == nil || r.UpstreamStatuses == nil || r.Timeline == nil || r.ServerInfo != r.Timeline.Server || r.ToolCall == nil {
		t.Errorf("result is not fully initialized: %+v", r)
	}
}

func TestStoreCallResult(t *testing.T) {
	info := &timeline.GotoClientInfo{}
	t.Run("data", func(t *testing.T) {
		r := newTestResult(t, nil)
		r.storeCallResult("r1", toolResult("hello", map[string]any{"a": 1}), info)
		cr := r.CallResults["r1"]
		if cr == nil || cr.RequestID != "r1" || cr.parent != r || cr.ClientInfo != info {
			t.Fatalf("call result = %+v", cr)
		}
		if contentText(t, cr) != "hello" || !reflect.DeepEqual(cr.Data, map[string]any{"a": 1}) {
			t.Errorf("call result content/data = %+v/%+v", cr.Content, cr.Data)
		}
		if cr.RemoteTimeline != nil || cr.RemoteResult != nil || cr.RemoteHeaders != nil {
			t.Errorf("call result has remote parts: %+v", cr)
		}
	})
	t.Run("result only has no content", func(t *testing.T) {
		tc := newToolCall("echo", "http://srv.test/mcp")
		tc.ResultOnly = true
		r := newTestResult(t, tc)
		r.storeCallResult("r1", toolResult("hello", "data"), info)
		if cr := r.CallResults["r1"]; cr == nil || len(cr.Content) != 0 || cr.Data != "data" {
			t.Errorf("call result = %+v", cr)
		}
	})
	t.Run("nil result", func(t *testing.T) {
		r := newTestResult(t, nil)
		r.storeCallResult("r1", nil, info)
		if len(r.CallResults) != 0 {
			t.Errorf("call results = %+v, want none", r.CallResults)
		}
	})
	t.Run("remote timeline", func(t *testing.T) {
		tc := newToolCall("echo", "http://srv.test/mcp")
		tc.NoEvents = false
		r := newTestResult(t, tc)
		structured := map[string]any{
			"TYPE":   "TIMELINE",
			"Label":  "remote-timeline",
			"Events": []any{map[string]any{"Label": "e1", "Text": "an event"}},
			"UpstreamHeaders": map[string]any{
				"hop (MCP)": map[string]any{"ResponseHeaders": map[string]any{"Via-Goto": []any{"peer-a", "peer-b"}}},
			},
		}
		r.storeCallResult("r1", toolResult("hello", structured), info)
		cr := r.CallResults["r1"]
		if cr.RemoteTimeline == nil || cr.RemoteTimeline.Label != "remote-timeline" || len(cr.RemoteTimeline.Events) != 1 {
			t.Fatalf("remote timeline = %+v", cr.RemoteTimeline)
		}
		if cr.Data != nil {
			t.Errorf("data = %+v, want none for a timeline", cr.Data)
		}
		if got := sortedKeys(r.RemoteGotos); !reflect.DeepEqual(got, []string{"peer-a", "peer-b"}) {
			t.Errorf("remote gotos = %v", got)
		}
		if _, ok := r.LastRemoteHeaders["hop (MCP)"]; !ok {
			t.Errorf("last remote headers = %+v", r.LastRemoteHeaders)
		}
	})
	t.Run("remote timeline without events", func(t *testing.T) {
		tc := newToolCall("echo", "http://srv.test/mcp")
		tc.NoEvents = true
		r := newTestResult(t, tc)
		structured := map[string]any{"TYPE": "TIMELINE", "Events": []any{map[string]any{"Label": "e1"}}}
		r.storeCallResult("r1", toolResult("hello", structured), info)
		if cr := r.CallResults["r1"]; cr.RemoteTimeline == nil || cr.RemoteTimeline.Events != nil {
			t.Errorf("remote timeline = %+v, want it without events", cr.RemoteTimeline)
		}
	})
	t.Run("remote result", func(t *testing.T) {
		r := newTestResult(t, nil)
		structured := map[string]any{"Result": map[string]any{"x": 1}}
		r.storeCallResult("r1", toolResult("hello", structured), info)
		cr := r.CallResults["r1"]
		if !reflect.DeepEqual(cr.RemoteResult, structured) || cr.Data != nil {
			t.Errorf("remote result/data = %+v/%+v", cr.RemoteResult, cr.Data)
		}
	})
	t.Run("remote headers", func(t *testing.T) {
		r := newTestResult(t, nil)
		structured := map[string]any{"Headers": map[string]any{
			"hop (MCP)": map[string]any{"ResponseHeaders": map[string]any{"Via-Goto": []any{"peer-c"}}},
		}}
		r.storeCallResult("r1", toolResult("hello", structured), info)
		cr := r.CallResults["r1"]
		if cr.RemoteHeaders == nil || cr.Data != nil {
			t.Errorf("remote headers/data = %+v/%+v", cr.RemoteHeaders, cr.Data)
		}
		if got := sortedKeys(r.RemoteGotos); !reflect.DeepEqual(got, []string{"peer-c"}) {
			t.Errorf("remote gotos = %v", got)
		}
	})
	t.Run("same request twice is one call", func(t *testing.T) {
		r := newTestResult(t, nil)
		r.storeCallResult("r1", toolResult("first", nil), info)
		r.storeCallResult("r1", toolResult("second", nil), info)
		if len(r.CallResults) != 1 || contentText(t, r.CallResults["r1"]) != "second" {
			t.Errorf("call results = %+v", r.CallResults)
		}
	})
}

func TestStoreHeaders(t *testing.T) {
	r := newTestResult(t, nil)
	req := http.Header{"X-Req": {"1"}}
	resp := http.Header{"X-Resp": {"2"}}
	r.storeHeaders("r1", req, resp, 503)
	cr := r.CallResults["r1"]
	if cr == nil || cr.ResponseStatus != 503 || cr.RequestHeaders.Get("X-Req") != "1" || cr.ResponseHeaders.Get("X-Resp") != "2" {
		t.Errorf("call result = %+v", cr)
	}
	if r.LastResponseStatus != 503 || r.LastRequestHeaders.Get("X-Req") != "1" || r.LastResponseHeaders.Get("X-Resp") != "2" {
		t.Errorf("last request/response = %+v/%+v/%d", r.LastRequestHeaders, r.LastResponseHeaders, r.LastResponseStatus)
	}
	if r.UpstreamStatuses["echo"] != 503 {
		t.Errorf("upstream statuses = %+v", r.UpstreamStatuses)
	}
	// The result of a call and its headers are the same call.
	r.storeCallResult("r1", toolResult("late", nil), nil)
	if len(r.CallResults) != 1 || r.CallResults["r1"].ResponseStatus != 503 || contentText(t, r.CallResults["r1"]) != "late" {
		t.Errorf("call results = %+v", r.CallResults)
	}
}

func filledResult(t *testing.T, tc *ToolCall) *MCPResult {
	t.Helper()
	r := newTestResult(t, tc)
	r.storeHeaders("r1", http.Header{"X-Req": {"1"}}, http.Header{"X-Resp": {"2"}}, 200)
	r.storeCallResult("r1", toolResult("one", map[string]any{"n": 1}), nil)
	r.storeHeaders("r2", http.Header{"X-Req": {"3"}}, http.Header{"X-Resp": {"4"}}, 200)
	r.storeCallResult("r2", toolResult("two", map[string]any{"n": 2}), nil)
	return r
}

func TestResultToMCP(t *testing.T) {
	t.Run("with details", func(t *testing.T) {
		r := filledResult(t, nil)
		res := r.ToMCP()
		texts := []string{}
		for _, c := range res.Content {
			texts = append(texts, c.(*gomcp.TextContent).Text)
		}
		sort.Strings(texts)
		if !reflect.DeepEqual(texts, []string{"one", "two"}) {
			t.Errorf("content = %v, want the content of both calls", texts)
		}
		tl, ok := res.StructuredContent.(*timeline.Timeline)
		if !ok || tl != r.Timeline {
			t.Fatalf("structured content = %T, want the timeline of the result", res.StructuredContent)
		}
		calls, ok := tl.Data["MCPCalls"].(map[string]map[string]any)
		if !ok || len(calls[r.ID]) != 2 {
			t.Errorf("MCPCalls = %+v, want the 2 calls under the result ID", tl.Data["MCPCalls"])
		}
		hop, _ := tl.UpstreamHeaders["echo (MCP)"].(map[string]any)
		if hop == nil || hop["RequestHeaders"] == nil || hop["ResponseHeaders"] == nil {
			t.Errorf("upstream headers = %+v", tl.UpstreamHeaders)
		}
		if tl.Status != 200 || tl.UpstreamStatuses["echo"] != 200 {
			t.Errorf("timeline status = %d, upstream statuses = %+v", tl.Status, tl.UpstreamStatuses)
		}
	})
	t.Run("remote headers are carried over", func(t *testing.T) {
		r := filledResult(t, nil)
		r.LastRemoteHeaders = map[string]any{"remote (MCP)": "remote-headers"}
		res := r.ToMCP()
		if tl := res.StructuredContent.(*timeline.Timeline); tl.UpstreamHeaders["remote (MCP)"] != "remote-headers" {
			t.Errorf("upstream headers = %+v", tl.UpstreamHeaders)
		}
	})
	t.Run("result only", func(t *testing.T) {
		tc := newToolCall("echo", "http://srv.test/mcp")
		tc.ResultOnly = true
		res := filledResult(t, tc).ToMCP()
		if len(res.Content) != 0 {
			t.Errorf("content = %+v, want none", res.Content)
		}
	})
	t.Run("no call details", func(t *testing.T) {
		tc := newToolCall("echo", "http://srv.test/mcp")
		tc.NoCallDetails = true
		r := filledResult(t, tc)
		res := r.ToMCP()
		if len(res.Content) != 0 || r.Timeline.Data["MCPCalls"] != nil {
			t.Errorf("content = %+v, MCPCalls = %+v, want none", res.Content, r.Timeline.Data["MCPCalls"])
		}
	})
	t.Run("no events", func(t *testing.T) {
		tc := newToolCall("echo", "http://srv.test/mcp")
		tc.NoEvents = true
		r := newTestResult(t, tc)
		r.Timeline.NoEvents = false
		r.Timeline.AddEvent("l", "an event")
		if len(r.Timeline.Events) != 1 {
			t.Fatalf("events = %d, want 1", len(r.Timeline.Events))
		}
		r.ToMCP()
		if r.Timeline.Events != nil {
			t.Errorf("events = %+v, want none", r.Timeline.Events)
		}
	})
	t.Run("without calls", func(t *testing.T) {
		r := newTestResult(t, nil)
		res := r.ToMCP()
		if len(res.Content) != 0 || r.Timeline.Data["MCPCalls"] != nil {
			t.Errorf("content = %+v, MCPCalls = %+v, want none", res.Content, r.Timeline.Data["MCPCalls"])
		}
	})
}

func TestResultToObject(t *testing.T) {
	t.Run("with content", func(t *testing.T) {
		r := filledResult(t, nil)
		obj := r.ToObject()
		content, _ := obj["Content"].([]string)
		sort.Strings(content)
		if !reflect.DeepEqual(content, []string{"one", "two"}) {
			t.Errorf("content = %v", content)
		}
		if obj["Timeline"] != r.Timeline {
			t.Errorf("timeline = %+v", obj["Timeline"])
		}
	})
	t.Run("result only", func(t *testing.T) {
		tc := newToolCall("echo", "http://srv.test/mcp")
		tc.ResultOnly = true
		obj := filledResult(t, tc).ToObject()
		if _, ok := obj["Content"]; ok {
			t.Errorf("object = %+v, want no content", obj)
		}
	})
	t.Run("without calls", func(t *testing.T) {
		obj := newTestResult(t, nil).ToObject()
		if _, ok := obj["Content"]; ok || obj["Timeline"] == nil {
			t.Errorf("object = %+v", obj)
		}
	})
}

func TestBuildCallsDataClearsContentOfResultOnlyCalls(t *testing.T) {
	tc := newToolCall("echo", "http://srv.test/mcp")
	tc.ResultOnly = true
	r := newTestResult(t, tc)
	r.storeHeaders("r1", nil, nil, 200)
	r.CallResults["r1"].Content = []gomcp.Content{&gomcp.TextContent{Text: "x"}}
	r.CallResults["r1"].RemoteTimeline = &timeline.Timeline{Events: []*timeline.Event{{Label: "e"}}}
	data := r.buildCallsData()
	if len(data[r.ID]) != 1 || data[r.ID]["r1"] != r.CallResults["r1"] {
		t.Errorf("calls data = %+v", data)
	}
	if cr := r.CallResults["r1"]; cr.Content != nil || cr.RemoteTimeline.Events != nil {
		t.Errorf("call result = %+v, want the content and the remote events dropped", cr)
	}
}

func TestExtractDataAndTimeline(t *testing.T) {
	data, tl := extractDataAndTimeline(map[string]any{"TYPE": "TIMELINE", "Timeline": 1, "other": 2})
	if tl == nil || data["other"] != 2 || data["Timeline"] != nil {
		t.Errorf("data, timeline = %+v, %+v", data, tl)
	}
	data, tl = extractDataAndTimeline(map[string]any{"other": 2})
	if tl != nil || data["other"] != 2 {
		t.Errorf("data, timeline = %+v, %+v", data, tl)
	}
	if data, tl = extractDataAndTimeline("not a map"); data != nil || tl != nil {
		t.Errorf("data, timeline = %+v, %+v", data, tl)
	}
	if data, tl = extractDataAndTimeline(nil); data != nil || tl != nil {
		t.Errorf("data, timeline = %+v, %+v", data, tl)
	}
}
