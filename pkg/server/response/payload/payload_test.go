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

package payload

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goto/pkg/global"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
	stubOnce  sync.Once
	routeOnce sync.Once
	// api serves the admin routes, mounted where the server mounts them: under /server/response.
	api *mux.Router
	// Payloads are kept per port, so every test uses a port of its own.
	portSeq atomic.Int32
)

func stubGlobalFuncs() {
	global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsListenerPresent = func(int) bool { return false }
	global.Funcs.IsListenerOpen = func(int) bool { return false }
	global.Funcs.GetListenerID = func(int) string { return "" }
	global.Funcs.GetListenerLabel = func(*http.Request) string { return "" }
	global.Funcs.GetListenerLabelForPort = func(int) string { return "" }
	util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
}

// Routes are registered on global routers, so they are registered only once, and tests don't run in parallel.
func setupRoutes() {
	stubOnce.Do(stubGlobalFuncs)
	routeOnce.Do(func() {
		// Payloads for URIs are registered on util.MatchRouter, which has to exist.
		util.CreateRouters(mux.NewRouter())
		api = mux.NewRouter()
		Middleware.SetRoutes(api.PathPrefix("/server/response").Subrouter())
	})
}

type testEnv struct {
	port int
	// next is the handler behind the middleware. It counts its calls and answers 200 "next".
	nextCalls atomic.Int32
	// direct is the payload middleware over the next handler.
	direct http.Handler
	// routed is the router that the server uses for traffic, with the payload middleware on it. It is only there when
	// the test asked for it with useRouter.
	routed http.Handler
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	setupRoutes()
	e := &testEnv{port: int(46000 + portSeq.Add(1))}
	e.direct = Middleware.MiddlewareHandler(e.next())
	t.Cleanup(func() {
		PayloadManager.lock.Lock()
		delete(PayloadManager.payloads, e.port)
		PayloadManager.lock.Unlock()
	})
	return e
}

func (e *testEnv) next() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.nextCalls.Add(1)
		fmt.Fprint(w, "next")
	})
}

// useRouter makes the router of the server for this test, so that the URIs of payloads that are set after this are served
// the way the server serves them: a route is registered on util.MatchRouter for each of them, and the payload middleware
// runs on every request that the router serves. The router answers with the next handler when no payload applies and
// no route is registered for the URI.
func (e *testEnv) useRouter(t *testing.T) {
	t.Helper()
	core := mux.NewRouter()
	util.CreateRouters(core)
	core.PathPrefix("/").Handler(e.next())
	core.Use(Middleware.MiddlewareHandler)
	e.routed = core
}

func (e *testEnv) newRequest(method, target, body string, headers ...string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, e.port))
	_, req, _ = util.WithRequestStore(req, nil)
	return req
}

// call calls the admin API.
func (e *testEnv) call(method, target, body string, headers ...string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	api.ServeHTTP(rr, e.newRequest(method, target, body, headers...))
	return rr
}

// configure calls the admin API and expects it to succeed.
func (e *testEnv) configure(t *testing.T, method, target, body string, headers ...string) string {
	t.Helper()
	rr := e.call(method, target, body, headers...)
	if rr.Code != http.StatusOK {
		t.Fatalf("%s %s = %d %q, want 200", method, target, rr.Code, rr.Body.String())
	}
	return rr.Body.String()
}

// set calls the admin API to set a payload, and expects the answer to tell that it was set.
func (e *testEnv) set(t *testing.T, target, body string, headers ...string) string {
	t.Helper()
	msg := e.configure(t, "POST", "/server/response/payload"+target, body, headers...)
	if strings.Contains(msg, "Failed") || strings.Contains(msg, "Invalid") {
		t.Fatalf("POST %s: %q reports a failure", target, msg)
	}
	return msg
}

// traffic sends a request through the payload middleware alone.
func (e *testEnv) traffic(method, target, body string, headers ...string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	e.direct.ServeHTTP(rr, e.newRequest(method, target, body, headers...))
	return rr
}

// get sends a GET request through the payload middleware alone.
func (e *testEnv) get(target string, headers ...string) *httptest.ResponseRecorder {
	return e.traffic("GET", target, "", headers...)
}

// routedTraffic sends a request through the router of the server.
func (e *testEnv) routedTraffic(t *testing.T, method, target, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	if e.routed == nil {
		t.Fatal("the test did not ask for the router")
	}
	rr := httptest.NewRecorder()
	e.routed.ServeHTTP(rr, e.newRequest(method, target, body, headers...))
	return rr
}

// routedGet sends a GET request through the router of the server.
func (e *testEnv) routedGet(t *testing.T, target string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	return e.routedTraffic(t, "GET", target, "", headers...)
}

// servedByPayload tells whether the response came from a payload rather than from the next handler.
func servedByPayload(rr *httptest.ResponseRecorder) bool {
	return rr.Body.String() != "next"
}

// uri makes a URI for traffic that no other test uses. The routes of URIs stay registered for the rest of the run, so
// tests don't share them.
func (e *testEnv) uri(path string) string {
	return fmt.Sprintf("/t%d%s", e.port, path)
}

func decodeJSON(t *testing.T, data []byte) map[string]any {
	t.Helper()
	m := map[string]any{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("%q is not a JSON object: %v", string(data), err)
	}
	return m
}

func readAll(t *testing.T, r io.Reader) string {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	return string(b)
}

// Tests of the manager of payloads, which is what the RPC servers use.

func TestGetPortResponseIsCreatedOncePerPort(t *testing.T) {
	e := newTestEnv(t)
	pr := PayloadManager.GetPortResponse(e.port)
	if pr == nil || pr.Port != e.port {
		t.Fatalf("GetPortResponse(%d) = %+v", e.port, pr)
	}
	if pr.HTTPPayloads == nil || pr.RPCPayloads == nil {
		t.Fatalf("the payloads of a new port are not initialized: %+v", pr)
	}
	if again := PayloadManager.GetPortResponse(e.port); again != pr {
		t.Errorf("GetPortResponse returned a different value the second time")
	}
	other := PayloadManager.GetPortResponse(e.port + 5000)
	t.Cleanup(func() {
		PayloadManager.lock.Lock()
		delete(PayloadManager.payloads, e.port+5000)
		PayloadManager.lock.Unlock()
	})
	if other == pr || other.Port != e.port+5000 {
		t.Errorf("ports share payloads: %+v and %+v", pr, other)
	}
}

func TestGetPortResponseConcurrentlyCreatesOne(t *testing.T) {
	e := newTestEnv(t)
	const workers = 32
	results := make([]*PortResponsePayloads, workers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i] = PayloadManager.GetPortResponse(e.port)
		}()
	}
	close(start)
	wg.Wait()
	for i, r := range results {
		if r == nil || r != results[0] {
			t.Fatalf("goroutine %d got %p, goroutine 0 got %p: the port got more than one set of payloads", i, r, results[0])
		}
	}
}

func TestGetGRPCPayloadsIsTheRPCSet(t *testing.T) {
	e := newTestEnv(t)
	pr := PayloadManager.GetPortResponse(e.port)
	if pr.GetGRPCPayloads() != pr.RPCPayloads || pr.GetGRPCPayloads() == pr.HTTPPayloads {
		t.Errorf("GetGRPCPayloads() is not the set of RPC payloads")
	}
	if pr.protoPayload(true) != pr.RPCPayloads || pr.protoPayload(false) != pr.HTTPPayloads {
		t.Errorf("protoPayload does not pick the set by the protocol")
	}
}

func TestSetRPCResponsePayloadRequiresPayload(t *testing.T) {
	e := newTestEnv(t)
	err := PayloadManager.SetRPCResponsePayload(e.port, false, nil, "", "/rpc/m", "", "", "", "", 0, 0, 0)
	if err == nil || !strings.Contains(err.Error(), "No payload") {
		t.Errorf("err = %v, want a complaint about the missing payload", err)
	}
	if PayloadManager.GetPortResponse(e.port).RPCPayloads.HasAnyPayload() {
		t.Errorf("a payload was set though none was given")
	}
}

func TestSetRPCResponsePayloadDefault(t *testing.T) {
	e := newTestEnv(t)
	if err := PayloadManager.SetRPCResponsePayload(e.port, false, []byte("default"), "text/plain", "", "", "", "", "", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	_, rp, _, found := PayloadManager.GetResponsePayload(e.port, true, "/any/thing", nil, nil, nil)
	if !found || string(rp.Payload) != "default" || rp.ContentType != "text/plain" {
		t.Fatalf("default payload = %+v, found %v", rp, found)
	}
	if _, _, _, found := PayloadManager.GetResponsePayload(e.port, false, "/any/thing", nil, nil, nil); found {
		t.Errorf("the default payload of RPC is served for HTTP")
	}
}

func TestSetRPCResponsePayloadForURI(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/pkg.Service/Method")
	if err := PayloadManager.SetRPCResponsePayload(e.port, false, []byte(`{"ok":true}`), "", uri, "", "", "", "", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	_, rp, _, found := PayloadManager.GetResponsePayload(e.port, true, uri, nil, nil, nil)
	if !found || string(rp.Payload) != `{"ok":true}` {
		t.Fatalf("payload for the URI = %+v, found %v", rp, found)
	}
	// The content type is JSON when none is given.
	if rp.ContentType != "application/json" {
		t.Errorf("content type = %q, want application/json", rp.ContentType)
	}
	if _, _, _, found := PayloadManager.GetResponsePayload(e.port, true, e.uri("/pkg.Service/Other"), nil, nil, nil); found {
		t.Errorf("a payload is found for a URI that was not set")
	}
	// An empty payload for the URI removes it.
	if err := PayloadManager.GetPortResponse(e.port).setURIResponsePayload(true, false, nil, false, uri, "", nil, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, _, found := PayloadManager.GetResponsePayload(e.port, true, uri, nil, nil, nil); found {
		t.Errorf("the payload is still found after it was removed")
	}
}

func TestSetRPCResponsePayloadForHeader(t *testing.T) {
	e := newTestEnv(t)
	if err := PayloadManager.SetRPCResponsePayload(e.port, false, []byte("by header"), "text/plain", "", "X-Mode", "fast", "", "", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, rp, _, found := PayloadManager.GetResponsePayload(e.port, true, "/x", http.Header{"X-Mode": {"fast"}}, nil, nil); !found || string(rp.Payload) != "by header" {
		t.Errorf("payload for the header = %+v, found %v", rp, found)
	}
	if _, _, _, found := PayloadManager.GetResponsePayload(e.port, true, "/x", http.Header{"X-Mode": {"slow"}}, nil, nil); found {
		t.Errorf("a payload is found for another value of the header")
	}
	if _, _, _, found := PayloadManager.GetResponsePayload(e.port, true, "/x", nil, nil, nil); found {
		t.Errorf("a payload is found without the header")
	}
}

func TestSetRPCResponsePayloadForURIAndHeader(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/svc/call")
	if err := PayloadManager.SetRPCResponsePayload(e.port, false, []byte("uri and header"), "text/plain", uri, "X-Mode", "fast", "", "", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, rp, _, found := PayloadManager.GetResponsePayload(e.port, true, uri, http.Header{"X-Mode": {"fast"}}, nil, nil); !found || string(rp.Payload) != "uri and header" {
		t.Errorf("payload = %+v, found %v", rp, found)
	}
	if _, _, _, found := PayloadManager.GetResponsePayload(e.port, true, uri, nil, nil, nil); found {
		t.Errorf("the payload is found for the URI without the header")
	}
}

func TestSetRPCResponsePayloadForURIAndBodyMatch(t *testing.T) {
	e := newTestEnv(t)
	uriRegexes := e.uri("/svc/regexes")
	uriPaths := e.uri("/svc/paths")
	if err := PayloadManager.SetRPCResponsePayload(e.port, false, []byte("keywords"), "text/plain", uriRegexes, "", "", "alpha,beta", "", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := PayloadManager.SetRPCResponsePayload(e.port, false, []byte("paths {a}"), "text/plain", uriPaths, "", "", "", ".k.a={a}", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	body := func(s string) io.ReadCloser { return io.NopCloser(strings.NewReader(s)) }
	if _, rp, _, found := PayloadManager.GetResponsePayload(e.port, true, uriRegexes, nil, nil, body("ALPHA and then BETA")); !found || string(rp.Payload) != "keywords" {
		t.Errorf("payload for the keywords = %+v, found %v", rp, found)
	}
	if _, _, _, found := PayloadManager.GetResponsePayload(e.port, true, uriRegexes, nil, nil, body("beta and then alpha")); found {
		t.Errorf("keywords match in another order than the one that was set")
	}
	_, rp, captures, found := PayloadManager.GetResponsePayload(e.port, true, uriPaths, nil, nil, body(`{"k":{"a":"value-a"}}`))
	if !found || string(rp.Payload) != "paths {a}" || captures["a"] != "value-a" {
		t.Errorf("payload for the JSON paths = %+v, captures %v, found %v", rp, captures, found)
	}
	if _, _, _, found := PayloadManager.GetResponsePayload(e.port, true, uriPaths, nil, nil, body(`{"k":{"b":"x"}}`)); found {
		t.Errorf("a payload is found for a body without the JSON path")
	}
}

func TestSetRPCResponsePayloadStream(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/svc/stream-out")
	if err := PayloadManager.SetRPCResponsePayload(e.port, true, []byte(`[{"n":1},{"n":2}]`), "", uri, "", "", "", "", 5, 0, 0); err != nil {
		t.Fatal(err)
	}
	_, rp, _, found := PayloadManager.GetResponsePayload(e.port, true, uri, nil, nil, nil)
	if !found || !rp.IsStream || rp.StreamCount != 5 {
		t.Fatalf("stream payload = %+v, found %v", rp, found)
	}
	got := []string{}
	for _, b := range rp.StreamPayload {
		got = append(got, string(b))
	}
	if strings.Join(got, ",") != `{"n":1},{"n":2},{"n":1},{"n":2},{"n":1}` {
		t.Errorf("stream = %v, want the array repeated until there are 5 items", got)
	}
}

func TestSetRPCResponsePayloadTransform(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/svc/transform")
	transforms := []*util.Transform{{Mappings: []*util.JSONTransform{{Source: ".a", Target: ".b"}}}}
	if err := PayloadManager.SetRPCResponsePayloadTransform(e.port, false, "", uri, transforms, 0, 0); err != nil {
		t.Fatal(err)
	}
	_, rp, _, found := PayloadManager.GetResponsePayload(e.port, true, uri, nil, nil, nil)
	if !found || len(rp.Transforms) != 1 || len(rp.Payload) != 0 {
		t.Fatalf("transform payload = %+v, found %v", rp, found)
	}
	// With no transforms and no payload the URI is cleared.
	if err := PayloadManager.SetRPCResponsePayloadTransform(e.port, false, "", uri, nil, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, _, found := PayloadManager.GetResponsePayload(e.port, true, uri, nil, nil, nil); found {
		t.Errorf("the transform is still found after it was removed")
	}
}

func TestClearRPCResponsePayloads(t *testing.T) {
	e := newTestEnv(t)
	if err := PayloadManager.SetRPCResponsePayload(e.port, false, []byte("default"), "text/plain", "", "", "", "", "", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	// A payload for HTTP is set apart from the one for RPC, and clearing the RPC payloads clears them all.
	pr := PayloadManager.GetPortResponse(e.port)
	pr.HTTPPayloads.setDefaultResponsePayload(&ResponsePayload{Payload: []byte("http default")})
	PayloadManager.ClearRPCResponsePayloads(e.port)
	if pr.HTTPPayloads.HasAnyPayload() || pr.RPCPayloads.HasAnyPayload() {
		t.Errorf("payloads remain after they were cleared: %+v", pr)
	}
	if _, _, _, found := PayloadManager.GetResponsePayload(e.port, true, "/x", nil, nil, nil); found {
		t.Errorf("a payload is found after the payloads were cleared")
	}
}

func TestSetURIResponsePayloadWithMatches(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/matched/{id}")
	rp := NewResponsePayload([]byte(`{"id":"{id}"}`), []*RequestMatch{NewRequestMatch(strings.ToUpper(uri))}, nil, "", false, false, false, false)
	if err := PayloadManager.SetURIResponsePayloadWithMatches(e.port, rp, false); err != nil {
		t.Fatal(err)
	}
	if !rp.IsJSON || rp.ContentType != "application/json" || rp.IsStream {
		t.Errorf("payload was not prepared: %+v", rp)
	}
	if rp.RequestMatches[0].URIPrefix != strings.ToLower(uri) {
		t.Errorf("URI prefix = %q, want it in lower case", rp.RequestMatches[0].URIPrefix)
	}
	_, found, _, ok := PayloadManager.GetResponsePayload(e.port, false, e.uri("/matched/17"), nil, nil, nil)
	if !ok || found != rp {
		t.Errorf("payload = %+v, found %v, want the payload that was set", found, ok)
	}
	if _, _, _, ok := PayloadManager.GetResponsePayload(e.port, true, e.uri("/matched/17"), nil, nil, nil); ok {
		t.Errorf("a payload that was set for HTTP is served for RPC")
	}
}

func TestSetURIResponsePayloadWithMatchesForRPC(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/rpc.Service/Call")
	rp := NewResponsePayload([]byte(`{"ok":1}`), []*RequestMatch{NewRequestMatch(uri)}, nil, "", false, false, false, false)
	if err := PayloadManager.SetURIResponsePayloadWithMatches(e.port, rp, true); err != nil {
		t.Fatal(err)
	}
	if _, found, _, ok := PayloadManager.GetResponsePayload(e.port, true, uri, nil, nil, nil); !ok || found != rp {
		t.Errorf("payload = %+v, found %v", found, ok)
	}
}

func TestSetURIResponsePayloadWithMatchesIsRefusedWhenIncomplete(t *testing.T) {
	e := newTestEnv(t)
	match := []*RequestMatch{NewRequestMatch(e.uri("/m"))}
	tests := []struct {
		name string
		rp   *ResponsePayload
		want string
	}{
		{"nil payload", nil, "missing payload"},
		{"no matches", NewResponsePayload([]byte("x"), nil, nil, "", false, false, false, false), "Matches required"},
		{"no payload", NewResponsePayload(nil, match, nil, "", false, false, false, false), "Payload required"},
		{"match without URI", NewResponsePayload([]byte("x"), []*RequestMatch{NewRequestMatch("")}, nil, "", false, false, false, false), "URI match is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := PayloadManager.SetURIResponsePayloadWithMatches(e.port, tt.rp, false)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
	if PayloadManager.GetPortResponse(e.port).HTTPPayloads.HasAnyPayload() {
		t.Errorf("a payload that was refused is served")
	}
}

func TestSetURIResponsePayloadWithMatchesStream(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/matched-feed")
	rp := NewResponsePayload([]byte(`[{"n":1},{"n":2}]`), []*RequestMatch{NewRequestMatch(uri)}, nil, "", false, false, false, false)
	rp.StreamCount = 3
	if err := PayloadManager.SetURIResponsePayloadWithMatches(e.port, rp, false); err != nil {
		t.Fatal(err)
	}
	if !rp.IsStream || len(rp.StreamPayload) != 3 {
		t.Errorf("stream = %v (stream %v), want 3 items", rp.StreamPayload, rp.IsStream)
	}
}

func TestPortResponsePayloadsAreListedAsJSON(t *testing.T) {
	e := newTestEnv(t)
	if err := PayloadManager.SetRPCResponsePayload(e.port, false, []byte("listed"), "text/plain", "", "", "", "", "", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(PayloadManager.GetPortResponse(e.port))
	if err != nil {
		t.Fatal(err)
	}
	listed := decodeJSON(t, b)
	if int(listed["Port"].(float64)) != e.port {
		t.Errorf("port = %v, want %d", listed["Port"], e.port)
	}
	httpPayloads, rpcPayloads := listed["http"].(map[string]any), listed["rpc"].(map[string]any)
	if httpPayloads["defaultPayload"] != nil {
		t.Errorf("HTTP default payload = %v, want none", httpPayloads["defaultPayload"])
	}
	if d, ok := rpcPayloads["defaultPayload"].(map[string]any); !ok || d["payload"] != "listed" {
		t.Errorf("RPC default payload = %v, want the one that was set", rpcPayloads["defaultPayload"])
	}
}

func TestPayloadsCanBeChangedAndReadConcurrently(t *testing.T) {
	e := newTestEnv(t)
	pr := PayloadManager.GetPortResponse(e.port)
	// The payloads are prepared before the goroutines start: preparing one registers a route on the router of the server,
	// which is not safe to do while the server is serving.
	const writers, rounds = 4, 40
	prepared := make([][]*ResponsePayload, writers)
	for w := range prepared {
		for i := 0; i < rounds; i++ {
			rp, err := newResponsePayload([]byte(fmt.Sprintf("w%d-%d", w, i)), false, false, "text/plain", "", fmt.Sprintf("x-w%d", w), "", "", nil, nil, nil, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			prepared[w] = append(prepared[w], rp)
		}
	}
	var wg sync.WaitGroup
	var listed, matched atomic.Int32
	failures := make(chan string, 64)
	fail := func(format string, args ...any) {
		select {
		case failures <- fmt.Sprintf(format, args...):
		default:
		}
	}
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pp := pr.protoPayload(false)
			for i, rp := range prepared[w] {
				header := fmt.Sprintf("x-w%d", w)
				pp.setHeaderResponsePayload(header, rp)
				if i%3 == 0 {
					pp.removeHeaderResponsePayload(header, "")
				}
			}
		}()
	}
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				b, err := json.Marshal(pr)
				if err != nil {
					fail("listing the payloads failed: %v", err)
					return
				}
				if !json.Valid(b) {
					fail("the listing is not JSON: %q", string(b))
				}
				listed.Add(1)
				_, _, _, found := pr.protoPayload(false).GetMatchingResponsePayload("/any", http.Header{"X-W0": {"1"}, "X-W1": {"1"}}, nil, nil)
				if found {
					matched.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	close(failures)
	for f := range failures {
		t.Error(f)
	}
	if listed.Load() != 4*rounds {
		t.Errorf("listed %d times, want %d", listed.Load(), 4*rounds)
	}
	// The last payload that each writer set for its header is the one with the last index that is not removed.
	pp := pr.protoPayload(false)
	for w := 0; w < writers; w++ {
		_, rp, _, found := pp.GetMatchingResponsePayload("/any", http.Header{fmt.Sprintf("X-W%d", w): {"v"}}, nil, nil)
		wantLast := rounds - 1
		if wantLast%3 == 0 {
			if found {
				t.Errorf("writer %d: payload %q is found though it was removed last", w, rp.Payload)
			}
			continue
		}
		if !found || string(rp.Payload) != fmt.Sprintf("w%d-%d", w, wantLast) {
			t.Errorf("writer %d: payload = %v, found %v, want w%d-%d", w, rp, found, w, wantLast)
		}
	}
}

func TestClearingWhileServingDoesNotBreakRequests(t *testing.T) {
	e := newTestEnv(t)
	e.set(t, "/set/default", "default payload", "ResponseContent-Type", "text/plain")
	const clears, requests = 20, 60
	var wg sync.WaitGroup
	results := make([]string, requests)
	prepared := make([]*http.Request, requests)
	for i := range prepared {
		prepared[i] = e.newRequest("GET", fmt.Sprintf("/serving/%d", i), "")
	}
	clearReqs := make([]*http.Request, clears)
	for i := range clearReqs {
		clearReqs[i] = e.newRequest("POST", "/server/response/payload/clear", "")
	}
	start := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		for _, req := range clearReqs {
			api.ServeHTTP(httptest.NewRecorder(), req)
			time.Sleep(time.Millisecond)
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for i, req := range prepared {
			rr := httptest.NewRecorder()
			e.direct.ServeHTTP(rr, req)
			results[i] = rr.Body.String()
		}
	}()
	close(start)
	wg.Wait()
	for i, got := range results {
		if got != "default payload" && got != "next" {
			t.Errorf("request %d got %q, want either the payload or the next handler's answer", i, got)
		}
	}
	// Whatever the interleaving was, the payloads are cleared in the end.
	if rr := e.get("/serving/after"); rr.Body.String() != "next" {
		t.Errorf("after clearing, the answer is %q, want the next handler's", rr.Body.String())
	}
}
