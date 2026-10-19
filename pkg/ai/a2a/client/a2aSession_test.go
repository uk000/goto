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
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"goto/pkg/constants"
	"goto/pkg/types"
	"goto/pkg/util/timeline"

	a2aproto "trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
)

func TestCallAgentUnary(t *testing.T) {
	u := newUpstream(t, "unary-echo", false, echoProcessor)
	call := u.call("unary-echo", "hello agent")
	call.Args = map[string]any{"k": "v"}
	var c collector
	result, err := CallAgent(ctxWithTimeout(t), 1, call, c.callback(), nil)
	if err != nil {
		t.Fatalf("CallAgent failed: %v", err)
	}
	if result == nil {
		t.Fatal("CallAgent returned no result")
	}
	if result.LastResponseStatus != http.StatusOK {
		t.Errorf("LastResponseStatus = %d, want 200", result.LastResponseStatus)
	}
	if !c.hasText("echo: hello agent") {
		t.Errorf("callback texts = %q, want the echoed message", c.texts())
	}
	if call.AgentURL != u.URL {
		t.Errorf("call.AgentURL = %q, want the URL of the card %q", call.AgentURL, u.URL)
	}
	calls := u.calls()
	if len(calls) != 1 {
		t.Fatalf("agent got %d calls, want 1", len(calls))
	}
	if !strings.Contains(calls[0].Body, "hello agent") || !strings.Contains(calls[0].Body, `"k":"v"`) {
		t.Errorf("agent call body = %s, want the message and the data", calls[0].Body)
	}
	if got := calls[0].Headers.Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Errorf("agent call content type = %q", got)
	}
}

func TestCallAgentUsesAgentURLOverCardURL(t *testing.T) {
	card := newUpstream(t, "card-only", false, echoProcessor)
	target := newUpstream(t, "target", false, echoProcessor)
	call := card.call("target", "route me")
	call.AgentURL = target.URL
	if _, err := CallAgent(ctxWithTimeout(t), 1, call, nil, nil); err != nil {
		t.Fatalf("CallAgent failed: %v", err)
	}
	if len(target.calls()) != 1 || len(card.calls()) != 0 {
		t.Errorf("calls: target %d, card %d, want the call at the agent URL only", len(target.calls()), len(card.calls()))
	}
	if card.cardHits.Load() != 1 {
		t.Errorf("card fetches = %d, want 1", card.cardHits.Load())
	}
}

func TestCallAgentStreaming(t *testing.T) {
	u := newUpstream(t, "streamer", true, streamProcessor("update one", "update two", "update three"))
	var c collector
	result, err := CallAgent(ctxWithTimeout(t), 1, u.call("streamer", "stream please"), c.callback(), nil)
	if err != nil {
		t.Fatalf("CallAgent failed: %v", err)
	}
	if result.LastResponseStatus != http.StatusOK {
		t.Errorf("LastResponseStatus = %d, want 200", result.LastResponseStatus)
	}
	texts := c.texts()
	last := -1
	for _, want := range []string{"update one", "update two", "update three", "stream done"} {
		found := -1
		for i, s := range texts {
			if i > last && strings.Contains(s, want) {
				found = i
				break
			}
		}
		if found < 0 {
			t.Fatalf("texts = %q, want %q after position %d", texts, want, last)
		}
		last = found
	}
	if !c.hasText("Task completed successfully") {
		t.Errorf("texts = %q, want the final status to be reported", texts)
	}
	for _, cr := range result.CallResults {
		if cr.ResponseStatus != http.StatusOK {
			t.Errorf("call result %q status = %d, want 200", cr.RequestID, cr.ResponseStatus)
		}
	}
}

func TestCallAgentStreamingFailedTask(t *testing.T) {
	failing := processorFunc(streamProcessorWithFinalState(a2aproto.TaskStateFailed))
	u := newUpstream(t, "fails", true, failing)
	var c collector
	_, err := CallAgent(ctxWithTimeout(t), 1, u.call("fails", "go"), c.callback(), nil)
	// A failed task is reported to the callback.
	if !c.hasText("Task failed") {
		t.Errorf("texts = %q, want the failure to be reported", c.texts())
	}
	_ = err
}

func TestCallAgentStreamingStatusUpdateWithoutMessage(t *testing.T) {
	u := newUpstream(t, "no-msg", true, streamProcessorWithFinalState(a2aproto.TaskStateCompleted))
	var c collector
	if _, err := CallAgent(ctxWithTimeout(t), 1, u.call("no-msg", "go"), c.callback(), nil); err != nil {
		t.Fatalf("CallAgent failed: %v", err)
	}
}

func TestCallAgentErrorStatuses(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusNotFound, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		for _, streaming := range []bool{false, true} {
			name := "err-" + http.StatusText(status)
			if streaming {
				name += "-stream"
			}
			t.Run(name, func(t *testing.T) {
				u := newUpstream(t, "err-agent", streaming, echoProcessor)
				u.forcedStatus.Store(int32(status))
				result, err := CallAgent(ctxWithTimeout(t), 1, u.call("err-agent", "hi"), nil, nil)
				if err == nil {
					t.Fatal("CallAgent must fail when the agent answers with an error status")
				}
				aerr, ok := err.(*AgentError)
				if !ok {
					t.Fatalf("error = %T %v, want *AgentError", err, err)
				}
				if aerr.StatusCode != status {
					t.Errorf("AgentError.StatusCode = %d, want %d (error: %v)", aerr.StatusCode, status, err)
				}
				if result == nil || result.LastResponseStatus != status {
					t.Errorf("result = %+v, want LastResponseStatus %d", result, status)
				}
			})
		}
	}
}

func TestCallAgentCardErrors(t *testing.T) {
	u := newUpstream(t, "card-err", false, echoProcessor)
	u.cardStatus.Store(http.StatusNotFound)
	call := u.call("card-err", "hi")
	result, err := CallAgent(ctxWithTimeout(t), 1, call, nil, nil)
	if err == nil || result != nil {
		t.Fatalf("CallAgent = %v, %v, want an error and no result when the card can't be fetched", result, err)
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error = %q, want the status of the card request", err)
	}
	if len(u.calls()) != 0 {
		t.Errorf("agent got %d calls, want none", len(u.calls()))
	}
}

func TestCallAgentRequiresSpec(t *testing.T) {
	for name, call := range map[string]*AgentCall{"nil": nil, "no card url": {Name: "x", AgentURL: "http://127.0.0.1:1/"}} {
		t.Run(name, func(t *testing.T) {
			setup(t)
			result, err := CallAgent(ctxWithTimeout(t), 1, call, nil, nil)
			if err == nil || result != nil {
				t.Errorf("CallAgent = %v, %v, want an error", result, err)
			}
		})
	}
}

func TestCallAgentSendsConfiguredAndForwardedHeaders(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		name := "unary"
		if streaming {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			u := newUpstream(t, "hdr-agent", streaming, streamOrEcho(streaming))
			call := u.call("hdr-agent", "hi")
			call.Headers = newHeaders(map[string]string{"X-Added": "added-value"}, "X-Forward-Me", "X-Missing")
			inHeaders := http.Header{"X-Forward-Me": []string{"fwd-value"}, "X-Dont-Forward": []string{"nope"}}
			if _, err := CallAgent(ctxWithTimeout(t), 1, call, nil, inHeaders); err != nil {
				t.Fatalf("CallAgent failed: %v", err)
			}
			calls := u.calls()
			if len(calls) != 1 {
				t.Fatalf("agent got %d calls, want 1", len(calls))
			}
			h := calls[0].Headers
			if h.Get("X-Added") != "added-value" || h.Get("X-Forward-Me") != "fwd-value" {
				t.Errorf("headers = %v, want the added and the forwarded ones", h)
			}
			if h.Get("X-Dont-Forward") != "" || h.Get("X-Missing") != "" {
				t.Errorf("headers = %v, want only the configured headers to be forwarded", h)
			}
			if h.Get("User-Agent") != "hdr-agent" {
				t.Errorf("user agent = %q, want the id of the caller (the agent name)", h.Get("User-Agent"))
			}
		})
	}
}

func TestCallAgentRemoveHeaders(t *testing.T) {
	u := newUpstream(t, "rm-agent", false, echoProcessor)
	call := u.call("rm-agent", "hi")
	call.Headers = types.NewHeaders()
	call.Headers.Request.Remove = []string{"Accept-Encoding"}
	if _, err := CallAgent(ctxWithTimeout(t), 1, call, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := u.calls()[0].Headers.Get("Accept-Encoding"); got != "" {
		t.Errorf("Accept-Encoding = %q, want it to be removed", got)
	}
}

func TestCallAgentRequestIDHeaderOfStreamCalls(t *testing.T) {
	u := newUpstream(t, "rid-agent", true, streamProcessor("a"))
	call := u.call("rid-agent", "hi")
	if _, err := CallAgent(ctxWithTimeout(t), 1, call, nil, nil); err != nil {
		t.Fatal(err)
	}
	h := u.calls()[0].Headers
	if got := h.Get(constants.HeaderGotoA2ARequestID); !strings.Contains(got, "rid-agent") || !strings.Contains(got, "Request# 1.1") {
		t.Errorf("%s = %q, want the id of the request", constants.HeaderGotoA2ARequestID, got)
	}
	if h.Get(constants.HeaderGotoA2AAgent) != "rid-agent" {
		t.Errorf("%s = %q, want the agent name", constants.HeaderGotoA2AAgent, h.Get(constants.HeaderGotoA2AAgent))
	}
}

func TestCallAgentRequestCountAndConcurrency(t *testing.T) {
	tests := []struct {
		name              string
		count, concurrent int
		wantCalls         int
	}{
		{"default is one", 0, 0, 1},
		{"sequential", 3, 1, 3},
		{"concurrent rounds", 6, 2, 6},
		{"all at once", 4, 4, 4},
	}
	for _, streaming := range []bool{false, true} {
		for _, tt := range tests {
			name := tt.name + "/unary"
			if streaming {
				name = tt.name + "/stream"
			}
			t.Run(name, func(t *testing.T) {
				u := newUpstream(t, "count-agent", streaming, streamOrEcho(streaming))
				call := u.call("count-agent", "hi")
				call.RequestCount = tt.count
				call.Concurrent = tt.concurrent
				result, err := CallAgent(ctxWithTimeout(t), 1, call, nil, nil)
				if err != nil {
					t.Fatalf("CallAgent failed: %v", err)
				}
				if got := len(u.calls()); got != tt.wantCalls {
					t.Errorf("agent got %d calls, want %d", got, tt.wantCalls)
				}
				if got := len(result.CallResults); got != tt.wantCalls {
					t.Errorf("call results = %d, want %d: %v", got, tt.wantCalls, keys(result.CallResults))
				}
			})
		}
	}
}

func TestCallAgentWithTimelineReportsEvents(t *testing.T) {
	u := newUpstream(t, "tl-agent", false, echoProcessor)
	tl := timeline.NewTimeline(1, "tl-agent", nil, nil, nil, nil, nil, nil)
	result, err := CallAgentWithTimeline(ctxWithTimeout(t), 1, u.call("tl-agent", "hi"), nil, nil, tl)
	if err != nil {
		t.Fatal(err)
	}
	if result.Timeline != tl {
		t.Error("the result must carry the timeline that was given")
	}
	found := false
	for _, e := range tl.Events {
		if strings.Contains(e.Text, "Initiating Agent Call [tl-agent]") {
			found = true
		}
	}
	if !found {
		t.Errorf("timeline events = %+v, want the call to be recorded", tl.Events)
	}
}

func TestCallAgentRemoteGotosFromResponseHeaders(t *testing.T) {
	u := newUpstream(t, "via-agent", false, echoProcessor)
	u.setResponseHeader(constants.HeaderViaGoto, "remote-goto-1")
	u.setResponseHeader(constants.HeaderViaGoto, "remote-goto-2(MCP)")
	result, err := CallAgent(ctxWithTimeout(t), 1, u.call("via-agent", "hi"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"remote-goto-1", "remote-goto-2(MCP)"} {
		if !result.RemoteGotos[want] {
			t.Errorf("RemoteGotos = %v, want %q", result.RemoteGotos, want)
		}
	}
	via := result.LastResponseHeaders[constants.HeaderViaGoto]
	joined := strings.Join(via, ",")
	if !strings.Contains(joined, "remote-goto-1(A2A)") || !strings.Contains(joined, "remote-goto-2(MCP)") || strings.Contains(joined, "remote-goto-2(MCP)(A2A)") {
		t.Errorf("Via-Goto = %v, want every remote marked once", via)
	}
}

func TestUpstreamStatusesAreRecorded(t *testing.T) {
	u := newUpstream(t, "status-agent", false, echoProcessor)
	u.setResponseHeader(constants.HeaderGotoUpstreamStatus, `{"remote":200}`)
	result, err := CallAgent(ctxWithTimeout(t), 1, u.call("status-agent", "hi"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.UpstreamStatuses["status-agent"] != http.StatusOK {
		t.Errorf("UpstreamStatuses = %v, want the status of the agent", result.UpstreamStatuses)
	}
}

func TestNewA2ASession(t *testing.T) {
	setup(t)
	u := newUpstream(t, "sess-agent", false, echoProcessor)
	call := u.call("sess-agent", "hi")
	card, err := FetchAgentCard(ctxWithTimeout(t), u.URL, "", call, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := NewA2ASession(ctxWithTimeout(t), 7, card, call, nil)
	if s.Card != card || s.Result == nil || s.Result.Agent != "sess-agent" {
		t.Fatalf("session = %+v", s)
	}
	if err := s.Connect(); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	var c collector
	if aerr := s.CallAgent("session hello", nil, c.callback(), nil, nil); aerr != nil {
		t.Fatalf("CallAgent failed: %v", aerr)
	}
	if !c.hasText("echo: session hello") {
		t.Errorf("texts = %q", c.texts())
	}
}

func TestConnectWithAgentCard(t *testing.T) {
	u := newUpstream(t, "cwac-agent", false, echoProcessor)
	call := u.call("cwac-agent", "hi")
	client := NewA2AClient(1, "caller", false, false, "", 0)
	session, err := client.ConnectWithAgentCard(ctxWithTimeout(t), call, u.URL, "", nil, timeline.NewTimeline(1, "caller", nil, nil, nil, nil, nil, nil))
	if err != nil {
		t.Fatalf("ConnectWithAgentCard failed: %v", err)
	}
	if session.Card == nil || session.Card.Name != "cwac-agent" {
		t.Fatalf("card = %+v", session.Card)
	}
	var c collector
	if aerr := session.CallAgent("via client", nil, c.callback(), nil, nil); aerr != nil {
		t.Fatalf("CallAgent failed: %v", aerr)
	}
	if !c.hasText("echo: via client") {
		t.Errorf("texts = %q", c.texts())
	}
	calls := u.calls()
	if len(calls) != 1 || calls[0].Headers.Get("User-Agent") != "caller" {
		t.Errorf("calls = %+v, want one call by the client id", calls)
	}
}

func TestConnectWithAgentCardFailure(t *testing.T) {
	u := newUpstream(t, "cwac-fail", false, echoProcessor)
	u.cardStatus.Store(http.StatusInternalServerError)
	client := NewA2AClient(1, "caller", false, false, "", 0)
	session, err := client.ConnectWithAgentCard(ctxWithTimeout(t), u.call("cwac-fail", ""), u.URL, "", nil, timeline.NewTimeline(1, "caller", nil, nil, nil, nil, nil, nil))
	if err == nil || session != nil {
		t.Errorf("ConnectWithAgentCard = %v, %v, want an error and no session", session, err)
	}
}

func TestCallAgentUsesCallMessageAndArgsAsDefaults(t *testing.T) {
	u := newUpstream(t, "default-agent", false, echoProcessor)
	call := u.call("default-agent", "default message")
	call.Args = map[string]any{"from": "call"}
	client := NewA2AClient(1, "caller", false, false, "", 0)
	session, err := client.ConnectWithAgentCard(ctxWithTimeout(t), call, u.URL, "", nil, timeline.NewTimeline(1, "caller", nil, nil, nil, nil, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if aerr := session.CallAgent("", nil, nil, nil, nil); aerr != nil {
		t.Fatalf("CallAgent failed: %v", aerr)
	}
	body := u.calls()[0].Body
	if !strings.Contains(body, "default message") || !strings.Contains(body, `"from":"call"`) {
		t.Errorf("body = %s, want the message and args of the call", body)
	}
}

func TestBuildInputParts(t *testing.T) {
	if parts := buildInputParts("", nil); len(parts) != 0 {
		t.Errorf("parts = %v, want none", parts)
	}
	parts := buildInputParts("hello", map[string]any{"a": 1})
	if len(parts) != 2 || parts[0].GetKind() != a2aproto.KindText || parts[1].GetKind() != a2aproto.KindData {
		t.Errorf("parts = %v, want text then data", parts)
	}
	if parts := buildInputParts("only text", nil); len(parts) != 1 || parts[0].GetKind() != a2aproto.KindText {
		t.Errorf("parts = %v, want a text part", parts)
	}
}

func TestSessionProcessPartsKinds(t *testing.T) {
	setup(t)
	var c collector
	s := &A2ASession{callback: c.callback(), Result: &A2AResult{Agent: "a", AgentCall: &AgentCall{}, CallResults: map[string]*A2ACallResult{}, RemoteGotos: map[string]bool{}, UpstreamStatuses: map[string]any{}}}
	cr := s.Result.getOrAddCall("r1")
	s.processParts("r1", []a2aproto.Part{
		a2aproto.NewTextPart("value text"),
		&a2aproto.TextPart{Kind: a2aproto.KindText, Text: "pointer text"},
		a2aproto.NewDataPart(map[string]any{"key": "val"}),
		&a2aproto.DataPart{Kind: a2aproto.KindData, Data: map[string]any{"ptrkey": 1}},
	}, cr)
	if len(cr.Content) != 2 || cr.Content[0] != "value text" || cr.Content[1] != "pointer text" {
		t.Errorf("content = %q", cr.Content)
	}
	if len(cr.Data) != 2 {
		t.Errorf("data = %v, want two entries", cr.Data)
	}
	if c.count() != 4 {
		t.Errorf("callback calls = %d, want 4", c.count())
	}
}

func TestSessionSendResponseRecognizesResultAndHeaders(t *testing.T) {
	setup(t)
	res := &A2AResult{Agent: "a", AgentCall: &AgentCall{}, CallResults: map[string]*A2ACallResult{}, RemoteGotos: map[string]bool{}, UpstreamStatuses: map[string]any{}, LastResponseHeaders: http.Header{}}
	s := &A2ASession{Result: res}
	cr := res.getOrAddCall("r1")
	s.sendResponse("r1", "", map[string]any{"Result": map[string]any{"answer": 42}}, cr)
	if m, ok := cr.RemoteResult.(map[string]any); !ok || m["Result"] == nil && m["answer"] == nil {
		t.Errorf("RemoteResult = %v", cr.RemoteResult)
	}
	s.sendResponse("r1", "", map[string]any{"UpstreamHeaders": map[string]any{"x (A2A)": map[string]any{"ResponseHeaders": map[string]any{constants.HeaderViaGoto: []any{"hop-1"}}}}}, cr)
	if cr.RemoteHeaders == nil || res.LastRemoteHeaders == nil {
		t.Errorf("RemoteHeaders = %v, LastRemoteHeaders = %v, want the headers to be kept", cr.RemoteHeaders, res.LastRemoteHeaders)
	}
}

func TestCallAgentStreamCancelledByContext(t *testing.T) {
	hang := processorFunc(func(ctx context.Context, msg a2aproto.Message, opts taskmanager.ProcessOptions, h taskmanager.TaskHandler) (*taskmanager.MessageProcessingResult, error) {
		taskID, err := h.BuildTask(nil, nil)
		if err != nil {
			return nil, err
		}
		sub, err := h.SubscribeTask(&taskID)
		if err != nil {
			return nil, err
		}
		// The task is never finished.
		return &taskmanager.MessageProcessingResult{StreamingEvents: sub}, nil
	})
	u := newUpstream(t, "hang-agent", true, hang)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := CallAgent(ctx, 1, u.call("hang-agent", "hi"), nil, nil)
	if err == nil {
		t.Fatal("CallAgent must fail when the context ends before the stream does")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("CallAgent took %v to give up", elapsed)
	}
}

func TestCallAgentInitialDelayBeforeStreaming(t *testing.T) {
	u := newUpstream(t, "delay-agent", true, streamProcessor("x"))
	call := u.call("delay-agent", "hi")
	call.InitialDelay = "150ms"
	start := time.Now()
	if _, err := CallAgent(ctxWithTimeout(t), 1, call, nil, nil); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Errorf("call took %v, want it to wait for the initial delay of 150ms", elapsed)
	}
}

func TestCallAgentRequestDelayBetweenRounds(t *testing.T) {
	u := newUpstream(t, "rdelay-agent", true, streamProcessor("x"))
	call := u.call("rdelay-agent", "hi")
	call.RequestCount, call.Concurrent = 3, 1
	call.RequestDelay = "100ms"
	start := time.Now()
	if _, err := CallAgent(ctxWithTimeout(t), 1, call, nil, nil); err != nil {
		t.Fatal(err)
	}
	// The delay is applied between the rounds, so twice for three rounds.
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond {
		t.Errorf("call took %v, want it to wait for the delays between the 3 rounds (200ms)", elapsed)
	}
	if got := len(u.calls()); got != 3 {
		t.Errorf("agent got %d calls, want 3", got)
	}
}
