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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	aicommon "goto/pkg/ai/common"
	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/types"
	"goto/pkg/util"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// waitWithin waits for the goroutines of the group, and fails the test when they don't finish in time, which is how a
// deadlock shows up.
func waitWithin(t *testing.T, wg *sync.WaitGroup, d time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("the goroutines did not finish within %s, they are deadlocked", d)
	}
}

// noPanic runs fn and reports a panic as a failure of the test, instead of letting it end the whole run.
func noPanic(t *testing.T, what string, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s panicked: %v", what, r)
		}
	}()
	fn()
}

// The success message of the payload API used to be built with a missing argument: the kind took the place of the
// name, and the kind itself was reported as %!s(MISSING).
func TestRegressionAddClientPayloadMessageNamesTheClient(t *testing.T) {
	setup(t)
	rr := apiCall(t, "POST", "/mcpapi/client/regr-name/payload/elicit", `{"contents":["x"]}`)
	expectStatus(t, rr, http.StatusOK)
	if got, want := rr.Body.String(), "Client payload added for MCP [regr-name] kind [elicit]\n"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	rr = apiCall(t, "POST", "/mcpapi/client/payload/sample", `{"contents":["x"]}`)
	expectStatus(t, rr, http.StatusOK)
	if got, want := rr.Body.String(), "Client payload added for MCP [] kind [sample]\n"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

// GetSession and RemoveSession took a read lock, and never released it, so that the first session that was removed
// or looked up made all the later sessions of the client wait forever.
func TestRegressionGetSessionReleasesItsLock(t *testing.T) {
	setup(t)
	client := newTestClient(false)
	tc := newToolCall("echo", "http://127.0.0.1:1/mcp")
	var created *MCPSession
	finishesWithin(t, 10*time.Second, "a session created after a lookup", func() {
		first := client.CreateSession(context.Background(), "", "op", tc, nil)
		client.GetSession(first.ID)
		created = client.CreateSession(context.Background(), "", "op", tc, nil)
	})
	if created == nil {
		t.Error("no session was created")
	}
}

func TestRegressionRemoveSessionReleasesItsLock(t *testing.T) {
	setup(t)
	client := newTestClient(false)
	tc := newToolCall("echo", "http://127.0.0.1:1/mcp")
	finishesWithin(t, 10*time.Second, "a session created after one was removed", func() {
		first := client.CreateSession(context.Background(), "", "op", tc, nil)
		client.RemoveSession(first.ID)
		client.CreateSession(context.Background(), "", "op", tc, nil)
	})
}

func TestRegressionSessionsOfOneClientRunOneAfterTheOther(t *testing.T) {
	setup(t)
	srv := newTestMCPServer(t)
	client := newTestClient(false)
	finishesWithin(t, 20*time.Second, "two calls through the same client", func() {
		for i := 0; i < 2; i++ {
			tc := newToolCall("plain", srv.streamURL())
			session := client.CreateSession(context.Background(), "", "op", tc, nil)
			session.CallTool(nil)
		}
	})
	if got := srv.toolHits.Load(); got != 2 {
		t.Errorf("server saw %d calls, want 2", got)
	}
}

// The client of an HTTP/2 transport was not hooked up, and creating it panicked.
func TestRegressionH2ClientIsCreated(t *testing.T) {
	setup(t)
	for _, tls := range []bool{false, true} {
		client := safeNewClient(t, nextPort(), false, true, tls, "caller", "listener", "", time.Second)
		if client.ht == nil {
			t.Errorf("h2 client (tls=%t) has no transport hooks", tls)
		}
	}
}

// Concurrent requests of a tool call used to share their request ID, which is derived from the round only, and the
// args that were meant to be a copy per request. A request count that can't be divided by the concurrency lost the
// remainder, and a concurrency above the request count made no calls at all.
func TestRegressionRequestCountAndConcurrency(t *testing.T) {
	setup(t)
	cases := []struct{ count, concurrent, want int }{
		{4, 2, 4},
		{6, 3, 6},
		{5, 2, 5},
		{2, 4, 2},
		{1, 3, 1},
		{3, 1, 3},
		{0, 0, 1},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%d requests, %d at a time", c.count, c.concurrent), func(t *testing.T) {
			srv := newTestMCPServer(t)
			tc := newToolCall("echo", srv.streamURL())
			tc.RequestCount, tc.Concurrent = c.count, c.concurrent
			tc.Args.Text = "multi"
			res, err := invokeTool(t, false, tc)
			if err != nil {
				t.Fatal(err)
			}
			if got := srv.toolHits.Load(); got != int32(c.want) {
				t.Fatalf("server saw %d calls, want %d", got, c.want)
			}
			if len(res.CallResults) != c.want {
				t.Fatalf("call results = %d, want %d: each request has to have its own", len(res.CallResults), c.want)
			}
			seen := map[string]bool{}
			for _, call := range srv.recordedCalls() {
				md, _ := call.Args["metadata"].(map[string]any)
				rid, _ := md[constants.HeaderGotoMCPRequestID].(string)
				if rid == "" || seen[rid] {
					t.Errorf("request ID %q of a call is missing or not unique (metadata: %v)", rid, md)
				}
				seen[rid] = true
				if _, found := res.CallResults[rid]; !found {
					t.Errorf("no call result for the request ID %q the server saw", rid)
				}
			}
		})
	}
}

// A shallow copy of the args of a tool call shared its maps with the original, so that the update of a clone wrote
// into the original, and into the clones of the others.
func TestRegressionUpdateAndCloneDoesNotShareArgs(t *testing.T) {
	tc := &ToolCall{Tool: "t", URL: "u", Args: aicommon.NewCallArgs()}
	tc.Args.AddMetadata("orig", "1")
	tc.Args.AgentArgs = map[string]any{"a": "1"}
	tc.Args.ForwardHeaders = []string{"X-One"}
	tc.Args.RemoteArgs = &aicommon.ToolCallArgs{Metadata: map[string]string{"r": "1"}}
	update := aicommon.NewCallArgs()
	update.AddMetadata("added", "2")
	update.RemoteArgs = &aicommon.ToolCallArgs{Metadata: map[string]string{"r2": "2"}}
	clone := tc.UpdateAndClone("", "", "", "", "", nil, update)
	if clone.Args.Metadata["added"] != "2" || clone.Args.Metadata["orig"] != "1" {
		t.Errorf("clone metadata = %v", clone.Args.Metadata)
	}
	if _, found := tc.Args.Metadata["added"]; found {
		t.Errorf("original metadata = %v, want it left alone by the update of the clone", tc.Args.Metadata)
	}
	if _, found := tc.Args.RemoteArgs.Metadata["r2"]; found {
		t.Errorf("original remote args metadata = %v, want it left alone", tc.Args.RemoteArgs.Metadata)
	}
	clone.Args.AgentArgs["b"] = "2"
	clone.Args.ForwardHeaders[0] = "X-Changed"
	if _, found := tc.Args.AgentArgs["b"]; found || tc.Args.ForwardHeaders[0] != "X-One" {
		t.Errorf("original agent args/forward headers = %v/%v, want them left alone", tc.Args.AgentArgs, tc.Args.ForwardHeaders)
	}
}

func TestRegressionUpdateAndCloneConcurrently(t *testing.T) {
	tc := &ToolCall{Tool: "t", URL: "u", Args: aicommon.NewCallArgs()}
	tc.Args.AddMetadata("orig", "1")
	const workers = 16
	var wg sync.WaitGroup
	clones := make([]*ToolCall, workers)
	for i := 0; i < workers; i++ {
		update := aicommon.NewCallArgs()
		update.AddMetadata(fmt.Sprintf("w%d", i), "v")
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			clones[i] = tc.UpdateAndClone("", "", "", "", "", nil, update)
		}(i)
	}
	waitWithin(t, &wg, 30*time.Second)
	for i, c := range clones {
		if len(c.Args.Metadata) != 2 || c.Args.Metadata[fmt.Sprintf("w%d", i)] != "v" {
			t.Errorf("clone %d metadata = %v, want only the original and its own entry", i, c.Args.Metadata)
		}
	}
	if len(tc.Args.Metadata) != 1 {
		t.Errorf("original metadata = %v", tc.Args.Metadata)
	}
}

// A request timeout that doesn't parse, or is not positive, made every request time out as soon as it was sent.
func TestRegressionInvalidRequestTimeoutFallsBackToTheDefault(t *testing.T) {
	for _, timeout := range []string{"bogus", "0s", "-5s", "10"} {
		tc := &ToolCall{Tool: "t", URL: "u", RequestTimeout: timeout}
		tc.Prepare()
		if tc.RequestTimeoutD != 10*time.Hour {
			t.Errorf("request timeout of %q = %s, want the default of 10h", timeout, tc.RequestTimeoutD)
		}
	}
	setup(t)
	srv := newTestMCPServer(t)
	tc := newToolCall("plain", srv.streamURL())
	tc.RequestTimeout = "bogus"
	tc.Prepare()
	if _, err := invokeTool(t, false, tc); err != nil {
		t.Errorf("call with an invalid request timeout failed: %v", err)
	}
}

// Sampling and elicitation requests used to panic when the client had no payload, or one without what it needs.
func TestRegressionCreateMessageHandlerWithoutPayload(t *testing.T) {
	for name, np := range map[string]*MCPNamedClientPayload{
		"no client payload":   nil,
		"no sample payload":   {},
		"only elicit payload": {ElicitPayload: newPayload("e")},
	} {
		t.Run(name, func(t *testing.T) {
			s := newCallbackSession(t, np)
			noPanic(t, "CreateMessageHandler", func() {
				res, err := s.CreateMessageHandler(context.Background(), createMessageRequest("sys"))
				if err != nil || res == nil {
					t.Fatalf("result, error = %+v, %v", res, err)
				}
				if res.Model != "GotoModel" || res.Role != "none" || res.StopReason != "sys" {
					t.Errorf("model/role/stop reason = %q/%q/%q, want the defaults", res.Model, res.Role, res.StopReason)
				}
			})
		})
	}
}

func TestRegressionCreateMessageHandlerWithoutParams(t *testing.T) {
	s := newCallbackSession(t, &MCPNamedClientPayload{SamplePayload: newPayload("c")})
	noPanic(t, "CreateMessageHandler", func() {
		res, err := s.CreateMessageHandler(context.Background(), &gomcp.CreateMessageRequest{})
		if err != nil || res == nil || res.Model != "GotoModel" {
			t.Errorf("result, error = %+v, %v", res, err)
		}
	})
}

func TestRegressionElicitationHandlerWithAnIncompletePayload(t *testing.T) {
	for name, p := range map[string]*MCPClientPayload{
		"empty payload":        {},
		"contents only":        newPayload("c"),
		"actions only":         {Actions: []string{"decline"}},
		"empty but not absent": {Contents: []string{}, Actions: []string{}},
	} {
		t.Run(name, func(t *testing.T) {
			s := newCallbackSession(t, &MCPNamedClientPayload{ElicitPayload: p})
			noPanic(t, "ElicitationHandler", func() {
				res, err := s.ElicitationHandler(context.Background(), &gomcp.ElicitRequest{})
				if err != nil || res == nil {
					t.Fatalf("result, error = %+v, %v", res, err)
				}
				want := "approve"
				if len(p.Actions) > 0 {
					want = p.Actions[0]
				}
				if res.Action != want {
					t.Errorf("action = %q, want %q", res.Action, want)
				}
			})
		})
	}
}

// The same requests, as the server makes them. A panic in a callback of the client ends the process.
func TestRegressionRequestsWithoutPayloads(t *testing.T) {
	setup(t)
	srv := newTestMCPServer(t)
	if _, err := invokeTool(t, false, newToolCall("sample", srv.streamURL())); err != nil {
		t.Fatalf("sampling without a payload failed: %v", err)
	}
	if res := receive(t, srv.sampled, "the sampling result"); res.Model != "GotoModel" || res.Role != "none" {
		t.Errorf("sampling result = model %q role %q, want the defaults", res.Model, res.Role)
	}
	if _, err := invokeTool(t, false, newToolCall("elicit", srv.streamURL())); err != nil {
		t.Fatalf("elicitation without a payload failed: %v", err)
	}
	if res := receive(t, srv.elicited, "the elicitation result"); res.Action != "approve" {
		t.Errorf("elicitation action = %q, want approve", res.Action)
	}
}

// writerWithoutFlush is a response writer that can't flush.
type writerWithoutFlush struct {
	header http.Header
	body   strings.Builder
}

func (w *writerWithoutFlush) Header() http.Header         { return w.header }
func (w *writerWithoutFlush) Write(b []byte) (int, error) { return w.body.Write(b) }
func (w *writerWithoutFlush) WriteHeader(int)             {}

// The streaming of events flushed unconditionally, which panicked for a writer that can't flush.
func TestRegressionStreamingToAWriterWithoutFlush(t *testing.T) {
	setup(t)
	w := &writerWithoutFlush{header: http.Header{}}
	r := httptest.NewRequest("POST", "/mcpapi/client/call/tool", nil)
	noPanic(t, "streaming an event", func() {
		streamMCPResponse("tool-1", w, r)("an event", nil, false)
		endMCPResponse("tool-1", w, r)("the end", map[string]int{"k": 1}, true)
	})
	if got := w.body.String(); !strings.Contains(got, "an event") || !strings.Contains(got, "the end : map[k:1]") {
		t.Errorf("streamed body = %q", got)
	}
}

// With verbose logging, the response of a request that doesn't belong to a session dereferenced the missing session.
func TestRegressionVerboseResponseOutsideOfASession(t *testing.T) {
	setup(t)
	global.Flags.VerboseMCP = true
	client := newTestClient(false)
	req := httptest.NewRequest("POST", "http://example.test/mcp", nil)
	noPanic(t, "InterceptResponse", func() {
		client.InterceptResponse(&http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: req})
	})
	noPanic(t, "InterceptRequest", func() { client.InterceptRequest(req) })
}

// The tools/prompts/resources flags were read from the request URI, query included, so that a URL like
// ...?url=http://host/tools turned the listing of everything into the one of tools.
func TestRegressionListFlagsComeFromThePath(t *testing.T) {
	setup(t)
	srv := newTestMCPServer(t)
	for _, note := range []string{"tools", "names-of-tools"} {
		rr := apiCall(t, "GET", listURL("all", srv.streamURL(), "note", note), "")
		expectStatus(t, rr, http.StatusOK)
		got := map[string]json.RawMessage{}
		decodeJSON(t, rr, &got)
		if !reflectDeepEqualKeys(got, "prompts", "resources", "tools") {
			t.Errorf("listing of all with note=%s has the keys %v, want tools, prompts and resources", note, keys(got))
		}
	}
}

func reflectDeepEqualKeys[V any](m map[string]V, want ...string) bool {
	got := keys(m)
	sort.Strings(want)
	return strings.Join(got, ",") == strings.Join(want, ",")
}

// The sse, tls and authority params were only read when they were all given, as they are one group of query params in
// the routes, so ?url=...&sse=true on its own listed over the streamable transport.
func TestRegressionListQueryParamsWorkOnTheirOwn(t *testing.T) {
	setup(t)
	srv := newTestMCPServer(t)
	t.Run("sse alone", func(t *testing.T) {
		rr := apiCall(t, "GET", listURL("tools/names", srv.sseURL(), "sse", "true"), "")
		expectStatus(t, rr, http.StatusOK)
		got := map[string][]string{}
		decodeJSON(t, rr, &got)
		if len(got["tools"]) != 7 {
			t.Errorf("tools = %v", got)
		}
	})
	t.Run("authority alone", func(t *testing.T) {
		before := len(srv.recordedHosts())
		rr := apiCall(t, "GET", listURL("tools", srv.streamURL(), "authority", "alone.example.test"), "")
		expectStatus(t, rr, http.StatusOK)
		hosts := srv.recordedHosts()[before:]
		if len(hosts) == 0 {
			t.Fatal("the server saw no request")
		}
		for _, h := range hosts {
			if h != "alone.example.test" {
				t.Errorf("server saw Host %q, want the authority alone.example.test", h)
			}
		}
	})
}

// Listing used to ignore the authority altogether, since a session without a tool call had no headers to put it in.
func TestRegressionListingSessionsApplyTheAuthority(t *testing.T) {
	setup(t)
	for _, tr := range transportCases {
		t.Run(tr.name, func(t *testing.T) {
			srv := newTestMCPServer(t)
			session := newTestClient(tr.sse).CreateSession(context.Background(), tr.url(srv), "list", nil, nil)
			session.SetAuthority("listing.example.test")
			if _, err := session.ListTools(); err != nil {
				t.Fatal(err)
			}
			hosts := srv.recordedHosts()
			if len(hosts) == 0 {
				t.Fatal("the server saw no request")
			}
			for _, h := range hosts {
				if h != "listing.example.test" {
					t.Errorf("server saw Host %q, want the authority (all hosts: %v)", h, hosts)
					break
				}
			}
		})
	}
}

// The requests that the SSE transport posts don't carry the query of the SSE URL, so they were not matched to their
// session: no headers were added, and the responses were not tracked.
func TestRegressionSSERequestsBelongToTheirSession(t *testing.T) {
	setup(t)
	srv := newTestMCPServer(t)
	tc := newToolCall("echo", srv.sseURL())
	tc.Authority = "sse.example.test"
	tc.Headers = types.NewHeaders()
	tc.Headers.Request.Add["X-Custom"] = "custom"
	res, err := invokeTool(t, true, tc)
	if err != nil {
		t.Fatal(err)
	}
	rid, cr := onlyCall(t, res)
	if res.LastResponseStatus == 0 || cr.ResponseStatus == 0 {
		t.Errorf("response status = %d/%d, want the status of the post of the call to be tracked", res.LastResponseStatus, cr.ResponseStatus)
	}
	calls := srv.recordedCalls()
	if len(calls) != 1 {
		t.Fatalf("server saw %d calls, want 1", len(calls))
	}
	h := calls[0].Header
	if h.Get("X-Custom") != "custom" || h.Get(constants.HeaderGotoMCPRequestID) != rid || h.Get(observedHostHeader) != "sse.example.test" {
		t.Errorf("headers of the call = %v, want the configured ones, the request ID %q and the authority", h, rid)
	}
}

// The details of a client didn't have its name, as the payload was created without it.
func TestRegressionNamedPayloadHasItsName(t *testing.T) {
	setup(t)
	if err := AddPayload("named-payload", "sample", []byte(`{"contents":["x"]}`)); err != nil {
		t.Fatal(err)
	}
	if err := SetRoots("named-roots", []byte(`[{"uri":"file:///r"}]`)); err != nil {
		t.Fatal(err)
	}
	if np := getNamedClientPayload("named-payload"); np == nil || np.Name != "named-payload" {
		t.Errorf("payload = %+v, want it to have its name", np)
	}
	if np := getNamedClientPayload("named-roots"); np == nil || np.Name != "named-roots" {
		t.Errorf("roots payload = %+v, want it to have its name", np)
	}
	rr := apiCall(t, "GET", "/mcpapi/client/named-payload/details", "")
	expectStatus(t, rr, http.StatusOK)
	if got := rr.Body.String(); !strings.Contains(got, `"Name": "named-payload"`) {
		t.Errorf("details = %s, want the name", got)
	}
}

// Response headers of a tool call config were dropped when the headers were copied for the session.
func TestRegressionResponseHeaderConfigReachesTheResult(t *testing.T) {
	setup(t)
	srv := newTestMCPServer(t)
	tc := newToolCall("plain", srv.streamURL())
	tc.Headers = types.NewHeaders()
	tc.Headers.Response.Add["X-Added-To-Response"] = "yes"
	res, err := invokeTool(t, false, tc)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.LastResponseHeaders.Get("X-Added-To-Response"); got != "yes" {
		t.Errorf("X-Added-To-Response = %q in the response headers %v", got, res.LastResponseHeaders)
	}
}

// A session that has no tool call (the ones that list) made a call that dereferenced the missing tool call.
func TestRegressionCallToolWithoutAToolCall(t *testing.T) {
	setup(t)
	srv := newTestMCPServer(t)
	session := newTestClient(false).CreateSession(context.Background(), srv.streamURL(), "no-tool-call", nil, nil)
	noPanic(t, "CallTool of a session without a tool call", func() {
		if res, err := session.CallTool(nil); err == nil || res != nil {
			t.Errorf("result, error = %+v, %v, want only an error", res, err)
		}
	})
}

func TestRegressionProgressNotificationWithoutParams(t *testing.T) {
	s := newCallbackSession(t, nil)
	noPanic(t, "ProgressNotificationHandler without params", func() {
		s.ProgressNotificationHandler(context.Background(), &gomcp.ProgressNotificationClientRequest{})
	})
	noPanic(t, "ProgressNotificationHandler without a request", func() {
		s.ProgressNotificationHandler(context.Background(), nil)
	})
}

// Listing closes the session, and the next listing of the session connected again without being known to the client,
// so that its requests were not matched to it.
func TestRegressionAllListingsApplyTheAuthority(t *testing.T) {
	setup(t)
	for _, tr := range transportCases {
		t.Run(tr.name, func(t *testing.T) {
			srv := newTestMCPServer(t)
			session := newTestClient(tr.sse).CreateSession(context.Background(), tr.url(srv), "list", nil, nil)
			session.SetAuthority("all.example.test")
			if _, err := session.ListTools(); err != nil {
				t.Fatal(err)
			}
			if _, err := session.ListPrompts(); err != nil {
				t.Fatal(err)
			}
			if _, err := session.ListResources(); err != nil {
				t.Fatal(err)
			}
			hosts := srv.recordedHosts()
			if len(hosts) < 3 {
				t.Fatalf("the server saw %d requests, want at least 3", len(hosts))
			}
			for _, h := range hosts {
				if h != "all.example.test" {
					t.Errorf("server saw Host %q, want the authority (all hosts: %v)", h, hosts)
					break
				}
			}
		})
	}
}

// Without an authority, the Host header of a tool call was replaced by an empty one, so that the configured one was lost.
func TestRegressionConfiguredHostIsKeptWithoutAuthority(t *testing.T) {
	setup(t)
	srv := newTestMCPServer(t)
	tc := newToolCall("echo", srv.streamURL())
	tc.Headers = types.NewHeaders()
	tc.Headers.Request.Add["Host"] = "configured.example.test"
	if _, err := invokeTool(t, false, tc); err != nil {
		t.Fatal(err)
	}
	calls := srv.recordedCalls()
	if len(calls) != 1 {
		t.Fatalf("server saw %d calls, want 1", len(calls))
	}
	if got := calls[0].Header.Get(observedHostHeader); got != "configured.example.test" {
		t.Errorf("host of the call = %q, want the configured one", got)
	}
}

// A timeout that is not positive was handed to the HTTP client as it was.
func TestRegressionNonPositiveClientTimeoutFallsBackToTheDefault(t *testing.T) {
	setup(t)
	for _, timeout := range []time.Duration{-time.Second, 0} {
		client := NewClient(nextPort(), false, false, false, "c", "l", "", timeout, nil, nil, nil)
		if client.httpClient.Timeout != time.Hour {
			t.Errorf("timeout of the client for %s = %s, want the default of 1h", timeout, client.httpClient.Timeout)
		}
	}
}

// The call filled in the args it was given, and its requests added their IDs to the metadata that the caller owns.
func TestRegressionCallToolDoesNotChangeTheArgsItIsGiven(t *testing.T) {
	setup(t)
	srv := newTestMCPServer(t)
	tc := newToolCall("echo", srv.streamURL())
	tc.Delay = "1ms-2ms"
	args := &aicommon.ToolCallArgs{Text: "given", Metadata: map[string]string{"k": "v"}}
	session := newTestClient(false).CreateSession(context.Background(), "", "op", tc, nil)
	if _, err := session.CallTool(args); err != nil {
		t.Fatal(err)
	}
	want := &aicommon.ToolCallArgs{Text: "given", Metadata: map[string]string{"k": "v"}}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("args = %+v, want them as they were given", args)
	}
	if calls := srv.recordedCalls(); len(calls) != 1 || calls[0].Args["text"] != "given" {
		t.Errorf("server saw %+v, want the call with the given text", calls)
	}
}

// ----------------------------------------------------------------------------------------------------------------
// Data races. These are found by running the tests with -race.
// ----------------------------------------------------------------------------------------------------------------

// The headers and trailers of a response were stored in the result by the goroutine that reads the body of the
// response, which may be done after the call returned, while the caller was reading the result.
func TestRegressionResultsAreCompleteWhenCallsReturn(t *testing.T) {
	setup(t)
	srv := newTestMCPServer(t)
	for i := 0; i < 15; i++ {
		tc := newToolCall("echo", srv.streamURL())
		tc.RequestCount, tc.Concurrent = 2, 2
		res, err := invokeTool(t, false, tc)
		if err != nil {
			t.Fatal(err)
		}
		if res.LastResponseStatus != http.StatusOK {
			t.Fatalf("round %d: last response status = %d, want 200", i, res.LastResponseStatus)
		}
		if res.UpstreamStatuses["echo"] != http.StatusOK {
			t.Fatalf("round %d: upstream statuses = %+v", i, res.UpstreamStatuses)
		}
		if _, found := res.UpstreamStatuses["-"]; !found {
			t.Fatalf("round %d: upstream statuses = %+v, want the processed trailers", i, res.UpstreamStatuses)
		}
		if res.LastResponseHeaders == nil {
			t.Fatalf("round %d: no last response headers", i)
		}
		for id, cr := range res.CallResults {
			if cr.ResponseStatus != http.StatusOK || cr.ResponseHeaders == nil {
				t.Fatalf("round %d: call %s = status %d headers %v", i, id, cr.ResponseStatus, cr.ResponseHeaders)
			}
		}
	}
}

func TestRegressionResultsOfTheToolCallAPIAreComplete(t *testing.T) {
	setup(t)
	srv := newTestMCPServer(t)
	body := toolCallBody(t, map[string]any{"tool": "echo", "url": srv.streamURL(), "noEvents": true, "requestCount": 3, "concurrent": 3})
	for i := 0; i < 10; i++ {
		rr := apiCall(t, "POST", "/mcpapi/client/call/tool", body)
		expectStatus(t, rr, http.StatusOK)
		out := struct {
			CallResults map[string]struct{ ResponseStatus int }
		}{}
		decodeJSON(t, rr, &out)
		if len(out.CallResults) != 3 {
			t.Fatalf("round %d: call results = %d, want 3", i, len(out.CallResults))
		}
	}
}

func TestRegressionConcurrentResultUpdates(t *testing.T) {
	r := newTestResult(t, nil)
	const workers, perWorker = 8, 25
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				id := fmt.Sprintf("w%d.%d", w, i)
				r.storeHeaders(id, http.Header{"X": {id}}, http.Header{"Y": {id}}, 200)
				r.storeCallResult(id, toolResult(id, map[string]any{"id": id}), nil)
			}
		}(w)
	}
	waitWithin(t, &wg, 30*time.Second)
	if len(r.CallResults) != workers*perWorker {
		t.Errorf("call results = %d, want %d", len(r.CallResults), workers*perWorker)
	}
	for id, cr := range r.CallResults {
		if cr.RequestID != id || cr.ResponseStatus != 200 || len(cr.Content) != 1 {
			t.Errorf("call %s = %+v", id, cr)
			break
		}
	}
}

func TestRegressionConcurrentSessionsOfAClient(t *testing.T) {
	setup(t)
	client := newTestClient(false)
	const workers, perWorker = 8, 20
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tc := newToolCall("echo", "http://example.test/mcp")
			for i := 0; i < perWorker; i++ {
				s := client.CreateSession(context.Background(), "", "op", tc, nil)
				req := httptest.NewRequest("POST", s.URL, nil)
				client.InterceptRequest(req)
				client.GetSession(s.ID)
				client.InterceptResponse(&http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: req})
				client.RemoveSession(s.ID)
			}
		}()
	}
	waitWithin(t, &wg, 30*time.Second)
	client.lock.RLock()
	defer client.lock.RUnlock()
	if len(client.ActiveSessions) != 0 {
		t.Errorf("active sessions = %d, want none", len(client.ActiveSessions))
	}
}

func TestRegressionConcurrentPayloadUpdatesAndReads(t *testing.T) {
	setup(t)
	const workers, perWorker = 6, 30
	detailsReqs := make([]*http.Request, 0, workers*perWorker)
	for i := 0; i < workers*perWorker; i++ {
		req := httptest.NewRequest("GET", "/mcpapi/client/details", nil)
		_, req, _ = withStore(req)
		detailsReqs = append(detailsReqs, req)
	}
	var wg sync.WaitGroup
	var detailFailures sync.Map
	for w := 0; w < workers; w++ {
		wg.Add(3)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				AddPayload(fmt.Sprintf("c%d", w), "sample", []byte(fmt.Sprintf(`{"contents":["w%d-%d"]}`, w, i)))
				AddPayload(fmt.Sprintf("c%d", w), "elicit", []byte(`{"contents":["e"]}`))
			}
		}(w)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				SetRoots(fmt.Sprintf("c%d", w), []byte(fmt.Sprintf(`[{"uri":"file:///w%d-%d"}]`, w, i)))
			}
		}(w)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				if np := getNamedClientPayload(fmt.Sprintf("c%d", w)); np != nil {
					_ = np.Roots
					_ = np.SamplePayload
				}
				rr := httptest.NewRecorder()
				apiRouter().ServeHTTP(rr, detailsReqs[w*perWorker+i])
				if rr.Code != http.StatusOK || !json.Valid(rr.Body.Bytes()) {
					detailFailures.Store(fmt.Sprintf("%d.%d", w, i), rr.Code)
				}
			}
		}(w)
	}
	waitWithin(t, &wg, 30*time.Second)
	detailFailures.Range(func(k, v any) bool {
		t.Errorf("details request %v = %v, want 200 with JSON", k, v)
		return true
	})
	if len(NamedClientPayloads) != workers {
		t.Errorf("clients = %d, want %d", len(NamedClientPayloads), workers)
	}
	for w := 0; w < workers; w++ {
		np := getNamedClientPayload(fmt.Sprintf("c%d", w))
		if np == nil || np.SamplePayload == nil || np.ElicitPayload == nil || len(np.Roots) != 1 ||
			np.SamplePayload.Contents[0] != fmt.Sprintf("w%d-%d", w, perWorker-1) || np.Roots[0].URI != fmt.Sprintf("file:///w%d-%d", w, perWorker-1) {
			t.Errorf("payload of c%d = %+v: updates of its different parts were lost", w, np)
		}
	}
}

// Callbacks that wait for a delay of a payload that sessions share used to change the state of the delay concurrently.
func TestRegressionConcurrentCallbacksWithADelayPayload(t *testing.T) {
	setup(t)
	delay := types.NewDelay(2*time.Millisecond, 3*time.Millisecond, -1)
	p := newPayload("c")
	p.Delay = delay
	np := &MCPNamedClientPayload{ElicitPayload: p, SamplePayload: p}
	const workers = 8
	var wg sync.WaitGroup
	results := make([]*gomcp.ElicitResult, workers)
	sessions := make([]*MCPSession, workers)
	for i := range sessions {
		sessions[i] = newCallbackSession(t, np)
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], _ = sessions[i].ElicitationHandler(context.Background(), &gomcp.ElicitRequest{})
			sessions[i].CreateMessageHandler(context.Background(), createMessageRequest("s"))
		}(i)
	}
	waitWithin(t, &wg, 30*time.Second)
	for i, r := range results {
		if r == nil || r.Action != "approve" {
			t.Errorf("result %d = %+v", i, r)
		}
	}
}

// withStore attaches a request store to a request the way the server does, so that requests can be prepared in advance.
func withStore(req *http.Request) (context.Context, *http.Request, *util.RequestStore) {
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, nextPort()))
	return util.WithRequestStore(req, nil)
}
