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
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goto/pkg/types"
	"goto/pkg/util"
	"goto/pkg/util/timeline"

	a2aproto "trpc.group/trpc-go/trpc-a2a-go/protocol"
	goa2aserver "trpc.group/trpc-go/trpc-a2a-go/server"
)

// A unary call made without a channel for the progress (every call of the client API and of the MCP tool is) used
// to close the nil channel when it was done.
func TestRegressionUnaryCallWithoutProgressChannel(t *testing.T) {
	u := newUpstream(t, "reg-unary", false, echoProcessor)
	result, err := CallAgent(ctxWithTimeout(t), 1, u.call("reg-unary", "hello"), nil, nil)
	if err != nil || result == nil || result.LastResponseStatus != http.StatusOK {
		t.Errorf("CallAgent = %v, %v", result, err)
	}
}

// The error of a call that went fine is nil, not a nil *AgentError inside an error.
func TestRegressionSuccessfulCallHasNilError(t *testing.T) {
	u := newUpstream(t, "reg-nil-err", true, streamProcessor("x"))
	_, err := CallAgent(ctxWithTimeout(t), 1, u.call("reg-nil-err", "hello"), nil, nil)
	if err != nil {
		t.Errorf("error = %#v, want nil", err)
	}
}

// A status update of a task does not have to have a message.
func TestRegressionStatusUpdateWithoutMessage(t *testing.T) {
	u := newUpstream(t, "reg-no-msg", true, streamProcessorWithFinalState(a2aproto.TaskStateCompleted))
	var c collector
	if _, err := CallAgent(ctxWithTimeout(t), 1, u.call("reg-no-msg", "hello"), c.callback(), nil); err != nil {
		t.Errorf("CallAgent failed: %v", err)
	}
	if !c.hasText("Task completed successfully") {
		t.Errorf("texts = %q, want the final status", c.texts())
	}
}

func TestRegressionFetchAgentCardWithoutCall(t *testing.T) {
	u := newUpstream(t, "reg-card", false, echoProcessor)
	if _, err := FetchAgentCard(ctxWithTimeout(t), u.URL, "", nil, nil); err != nil {
		t.Errorf("FetchAgentCard failed: %v", err)
	}
}

func TestRegressionCallAgentWithoutCall(t *testing.T) {
	setup(t)
	result, err := CallAgent(ctxWithTimeout(t), 1, nil, nil, nil)
	if err == nil || result != nil {
		t.Errorf("CallAgent = %v, %v, want an error", result, err)
	}
}

// A request that fails before there is a response (the agent can't be reached) leaves the result without response
// headers, which must not make the report of the call crash.
func TestRegressionCallToUnreachableAgent(t *testing.T) {
	card := newUpstream(t, "reg-card-ok", true, streamProcessor("x"))
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL + "/agent/dead/"
	dead.Close()
	call := card.call("reg-card-ok", "hello")
	call.AgentURL = deadURL
	result, err := CallAgent(ctxWithTimeout(t), 1, call, nil, nil)
	if err == nil {
		t.Fatal("CallAgent to an agent that can't be reached must fail")
	}
	if result == nil || result.LastResponseStatus != 0 {
		t.Errorf("result = %+v, want no response status", result)
	}
}

// One request with a concurrency of two is one request.
func TestRegressionFewerRequestsThanConcurrent(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		u := newUpstream(t, "reg-few", streaming, streamOrEcho(streaming))
		call := u.call("reg-few", "hello")
		call.RequestCount, call.Concurrent = 1, 2
		if _, err := CallAgent(ctxWithTimeout(t), 1, call, nil, nil); err != nil {
			t.Errorf("CallAgent (streaming %v) failed: %v", streaming, err)
		}
		if got := len(u.calls()); got != 1 {
			t.Errorf("streaming %v: agent got %d calls, want 1", streaming, got)
		}
	}
}

// Five requests, two at a time, are five requests (not four).
func TestRegressionRequestCountNotMultipleOfConcurrent(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		u := newUpstream(t, "reg-uneven", streaming, streamOrEcho(streaming))
		call := u.call("reg-uneven", "hello")
		call.RequestCount, call.Concurrent = 5, 2
		if _, err := CallAgent(ctxWithTimeout(t), 1, call, nil, nil); err != nil {
			t.Errorf("CallAgent (streaming %v) failed: %v", streaming, err)
		}
		if got := len(u.calls()); got != 5 {
			t.Errorf("streaming %v: agent got %d calls, want 5", streaming, got)
		}
	}
}

// The headers and status of a unary request belong to the call of the request, and there is no call without a
// request ID.
func TestRegressionUnaryRequestsAreTrackedByID(t *testing.T) {
	u := newUpstream(t, "reg-ids", false, echoProcessor)
	call := u.call("reg-ids", "hello")
	call.RequestCount, call.Concurrent = 2, 1
	result, err := CallAgent(ctxWithTimeout(t), 1, call, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.CallResults) != 2 {
		t.Fatalf("call results = %v, want one per request", keys(result.CallResults))
	}
	for id, cr := range result.CallResults {
		if id == "" || cr.ResponseStatus != http.StatusOK || cr.RequestHeaders == nil || cr.ResponseHeaders == nil {
			t.Errorf("call %q = %+v, want its own status and headers", id, cr)
		}
	}
	for _, c := range u.calls() {
		if c.Headers.Get("Goto-A2A-Request-ID") == "" {
			t.Errorf("a call has no request ID header: %v", c.Headers)
		}
	}
}

// The input data of a call is the one of the caller. Delegates that run concurrently are given the same data.
func TestRegressionInputDataIsNotChangedByCalls(t *testing.T) {
	u := newUpstream(t, "reg-input", false, echoProcessor)
	shared := map[string]any{"k": "v"}
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			call := u.call("reg-input", "hello")
			_, errs[i] = callWithInput(t, call, shared)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("call %d failed: %v", i, err)
		}
	}
	if len(shared) != 1 {
		t.Errorf("input data = %v, want it unchanged", shared)
	}
}

func callWithInput(t *testing.T, call *AgentCall, input map[string]any) (*A2AResult, error) {
	t.Helper()
	client := NewA2AClient(1, "caller", false, false, "", 0)
	session, err := client.ConnectWithAgentCard(ctxWithTimeout(t), call, call.CardURL, "", nil, timeline.NewTimeline(1, "caller", nil, nil, nil, nil, nil, nil))
	if err != nil {
		return nil, err
	}
	if aerr := session.CallAgent("hello", input, nil, nil, nil); aerr != nil {
		return session.Result, aerr
	}
	return session.Result, nil
}

// The requests of a call run concurrently, and report to the same result. Run with -race.
func TestRegressionConcurrentRequestsShareResultSafely(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		name := "unary"
		if streaming {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			u := newUpstream(t, "reg-conc", streaming, streamOrEcho(streaming))
			u.setResponseHeader("Via-Goto", "remote-goto")
			call := u.call("reg-conc", "hello")
			call.RequestCount, call.Concurrent = 12, 6
			call.Headers = newHeaders(map[string]string{"X-Added": "a"})
			var c collector
			result, err := CallAgent(ctxWithTimeout(t), 1, call, c.callback(), nil)
			if err != nil {
				t.Fatalf("CallAgent failed: %v", err)
			}
			if got := len(u.calls()); got != 12 {
				t.Errorf("agent got %d calls, want 12", got)
			}
			if len(result.CallResults) != 12 {
				t.Errorf("call results = %d, want 12", len(result.CallResults))
			}
			if !result.RemoteGotos["remote-goto"] {
				t.Errorf("RemoteGotos = %v", result.RemoteGotos)
			}
			if c.count() < 12 {
				t.Errorf("callback calls = %d, want at least one per request", c.count())
			}
		})
	}
}

// Every request reports to the request store of the request that is being served, which they share.
func TestRegressionConcurrentRequestsShareRequestStoreSafely(t *testing.T) {
	u := newUpstream(t, "reg-rs", false, echoProcessor)
	call := u.call("reg-rs", "hello")
	call.RequestCount, call.Concurrent = 8, 8
	req := httptest.NewRequest("POST", "/agent/x/", nil)
	_, req, rs := util.WithRequestStore(req, nil)
	ctx := req.Context()
	if _, err := CallAgent(ctx, 1, call, nil, nil); err != nil {
		t.Fatal(err)
	}
	if rs.UpstreamStatuses == nil || rs.UpstreamStatuses["-"] == nil {
		t.Errorf("request store statuses = %v, want the upstream statuses to be recorded", rs.UpstreamStatuses)
	}
}

// The status of a call that got no response from the agent is not a status to answer with.
func TestRegressionAPICallWithoutAgentResponseGetsValidStatus(t *testing.T) {
	card := newUpstream(t, "reg-api-card", true, streamProcessor("x"))
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL + "/agent/dead/"
	dead.Close()
	body := callBody(map[string]any{"name": "reg-api-card", "cardURL": card.URL, "agentURL": deadURL, "message": "hi"})
	rr := serveAPI(t, "POST", "/a2a/client/call", body)
	if rr.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rr.Code)
	}
}

// The name of the agent is not what tells that a call is a streaming one.
func TestRegressionAPIAgentNamedStreamIsNotStreamed(t *testing.T) {
	u := newUpstream(t, "stream-bot", false, echoProcessor)
	body := callBody(map[string]any{"cardURL": u.URL, "message": "hi"})
	rr := serveAPI(t, "POST", "/a2a/client/agent/stream-bot/call", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	if out := decodeObject(t, rr); out["headers"] == nil {
		t.Errorf("response = %v, want the report of a call that is not streamed", out)
	}
}

func TestRegressionAPIFetchAgentCard(t *testing.T) {
	u := newUpstream(t, "reg-api-fetch", false, echoProcessor)
	rr := serveAPI(t, "GET", "/a2a/client/agent/card?url="+url.QueryEscape(u.URL), "")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "reg-api-fetch") {
		t.Errorf("response = %d %q", rr.Code, rr.Body.String())
	}
}

// The data that a streaming agent sends (as data parts) has to reach the caller of the streaming API as well as its
// text does.
func TestRegressionStreamedDataIsNotDropped(t *testing.T) {
	u := newUpstream(t, "reg-stream-data", true, streamDataProcessor(map[string]any{"temperature": 21}))
	body := callBody(map[string]any{"name": "reg-stream-data", "cardURL": u.URL, "message": "go"})
	rr := serveAPI(t, "POST", "/a2a/client/call/stream", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"temperature": 21`) {
		t.Errorf("response = %s, want the data of the agent", rr.Body.String())
	}
}

// The results of the calls that an agent makes share the timeline of the agent. The calls end together when the agent
// calls its delegates in parallel, and each result reports what it made to the timeline.
func TestRegressionResultsOfParallelCallsReportToTheSharedTimeline(t *testing.T) {
	setup(t)
	shared := timeline.NewTimeline(1, "shared", nil, nil, nil, nil, nil, nil)
	const results, reports = 8, 20
	all := make([]*A2AResult, results)
	for i := range all {
		r := NewA2AResult("http://server", &AgentCall{Name: fmt.Sprintf("agent-%d", i)}, shared)
		r.getOrAddCall("r1").Content = []string{"hello"}
		all[i] = r
	}
	start := make(chan struct{})
	done := make(chan struct{})
	var wg sync.WaitGroup
	for _, r := range all {
		wg.Add(1)
		go func(r *A2AResult) {
			defer wg.Done()
			<-start
			for j := 0; j < reports; j++ {
				r.ToObject()
				r.ToMCP(nil)
			}
		}(r)
	}
	close(start)
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatalf("the results were not reported in time")
	}
	if _, ok := shared.Data["A2ACalls"]; !ok {
		t.Errorf("data = %v, want the calls reported to the timeline", shared.Data)
	}
	for i := range all {
		if _, ok := shared.UpstreamHeaders[fmt.Sprintf("agent-%d (A2A)", i)]; !ok {
			t.Errorf("upstream headers = %v, want what agent-%d reported", shared.UpstreamHeaders, i)
		}
	}
}

// The sessions of the calls that an agent makes in parallel report to the timeline of the agent, while the results of the
// calls that are done are reported from it.
func TestRegressionSnapshotWhileSessionsReportToTheSharedTimeline(t *testing.T) {
	setup(t)
	shared := timeline.NewTimeline(1, "shared", nil, nil, nil, nil, nil, nil)
	const sessions, reports = 4, 25
	progress := make(chan *types.Pair[string, any], sessions*reports*2)
	var snapshots atomic.Int32
	reported := NewA2AResult("http://server", &AgentCall{Name: "reported"}, shared)
	reported.getOrAddCall("r1").Content = []string{"hello"}
	start := make(chan struct{})
	done := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < sessions; i++ {
		name := fmt.Sprintf("agent-%d", i)
		call := &AgentCall{Name: name, AgentURL: "http://127.0.0.1:1/"}
		session := NewA2ASessionWithTimeline(ctxWithTimeout(t), 7, &goa2aserver.AgentCard{Name: name}, call, nil, shared)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < reports; j++ {
				session.configure(nil, progress, nil, nil)
				session.reportInitiateCall()
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for j := 0; j < reports; j++ {
			if snapshot := reported.Snapshot(); snapshot["Timeline"] == nil {
				snapshots.Add(1)
				return
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
		t.Fatalf("the sessions and the snapshots were not done in time")
	}
	if n := snapshots.Load(); n != 0 {
		t.Errorf("snapshots without the timeline = %d, want 0", n)
	}
	if want := sessions * reports; len(shared.Events) != want {
		t.Errorf("events = %d, want the %d that the sessions reported", len(shared.Events), want)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// The requests of a call store their headers in the client info of the call, which is in the timeline that the results of
// other calls report.
func TestRegressionClientInfoUpdatedWhileSnapshotsAreTaken(t *testing.T) {
	setup(t)
	shared := timeline.NewTimeline(1, "shared", nil, nil, nil, nil, nil, nil)
	const sessions, requests = 4, 25
	var failed atomic.Int32
	reported := NewA2AResult("http://server", &AgentCall{Name: "reported"}, shared)
	reported.getOrAddCall("r1").Content = []string{"hello"}
	httpClient := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{}")), Request: r}, nil
	})}
	start := make(chan struct{})
	done := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < sessions; i++ {
		name := fmt.Sprintf("agent-%d", i)
		call := &AgentCall{Name: name, AgentURL: "http://upstream.invalid/"}
		session := NewA2ASessionWithTimeline(ctxWithTimeout(t), 7, &goa2aserver.AgentCard{Name: name}, call, nil, shared)
		session.reportInitiateCall()
		reqs := make([]*http.Request, requests)
		for j := range reqs {
			req, err := http.NewRequest(http.MethodPost, "http://upstream.invalid/", strings.NewReader("{}"))
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			reqs[j] = req
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for _, req := range reqs {
				resp, err := session.Handle(session.ctx, httpClient, req)
				if err != nil {
					failed.Add(1)
					return
				}
				resp.Body.Close()
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for j := 0; j < requests; j++ {
			reported.Snapshot()
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
		t.Fatalf("the requests and the snapshots were not done in time")
	}
	if n := failed.Load(); n != 0 {
		t.Errorf("sessions whose request failed = %d, want 0", n)
	}
}
