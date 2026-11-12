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
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"goto/pkg/constants"
	"goto/pkg/util"
)

// failingWriter is a response writer that can't write.
type failingWriter struct {
	http.ResponseWriter
}

func (f *failingWriter) Write(p []byte) (int, error) { return 0, errors.New("broken pipe") }

// failingFlushWriter is a failingWriter that can flush, so that it can be streamed to.
type failingFlushWriter struct {
	failingWriter
}

func (f *failingFlushWriter) Flush() {}

// plainWriter is a response writer that can't flush.
type plainWriter struct {
	http.ResponseWriter
}

func logMessages(req *http.Request) string {
	return strings.Join(util.GetRequestStore(req).LogMessages, "\n")
}

func TestMiddlewareLeavesRequestsAloneWhenNoPayloadIsSet(t *testing.T) {
	e := newTestEnv(t)
	rr := e.get(e.uri("/anything"))
	if rr.Body.String() != "next" || e.nextCalls.Load() != 1 {
		t.Errorf("response = %q, next calls = %d, want the next handler to answer once", rr.Body.String(), e.nextCalls.Load())
	}
}

func TestMiddlewareSkipsRequestsThatAreNotTraffic(t *testing.T) {
	e := newTestEnv(t)
	e.set(t, "/set/default", "default payload")
	for _, target := range []string{"/server/anything", "/health", "/metrics", "/version", "/tunnels/x"} {
		before := e.nextCalls.Load()
		rr := e.get(target)
		if rr.Body.String() != "next" || e.nextCalls.Load() != before+1 {
			t.Errorf("GET %s = %q, want the next handler's answer", target, rr.Body.String())
		}
	}
}

func TestMiddlewareSkipsRequestsForPayloadsAndStreams(t *testing.T) {
	e := newTestEnv(t)
	e.set(t, "/set/default", "default payload")
	for _, target := range []string{"/stream/count=3", "/some/payload/10", "/x/stream"} {
		if got := e.get(target).Body.String(); got != "next" {
			t.Errorf("GET %s = %q, want the next handler's answer, since the request asks for a payload of its own", target, got)
		}
	}
}

func TestMiddlewareWithoutNextHandler(t *testing.T) {
	e := newTestEnv(t)
	handler := Middleware.MiddlewareHandler(nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, e.newRequest("GET", "/x", ""))
	if rr.Code != http.StatusOK || rr.Body.Len() != 0 {
		t.Errorf("with nothing set: %d %q, want an empty 200", rr.Code, rr.Body.String())
	}
	e.set(t, "/set/default", "default payload")
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, e.newRequest("GET", "/x", ""))
	if rr.Body.String() != "default payload" {
		t.Errorf("with a payload: %q, want the payload", rr.Body.String())
	}
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, e.newRequest("GET", "/server/x", ""))
	if rr.Code != http.StatusOK || rr.Body.Len() != 0 {
		t.Errorf("for a request that is not traffic: %d %q, want an empty 200", rr.Code, rr.Body.String())
	}
}

func TestMiddlewareCallsNextAfterThePayloadForStatusAndDelayRequests(t *testing.T) {
	e := newTestEnv(t)
	e.set(t, "/set/default", "payload;")
	for _, target := range []string{"/status/418", "/delay/1ms"} {
		before := e.nextCalls.Load()
		rr := e.get(target)
		if rr.Body.String() != "payload;next" || e.nextCalls.Load() != before+1 {
			t.Errorf("GET %s = %q, want the payload and then the answer of the next handler", target, rr.Body.String())
		}
	}
}

func TestMiddlewareHandsThePayloadAndCapturesToTheNextHandler(t *testing.T) {
	e := newTestEnv(t)
	e.set(t, "/set/body/paths/a={.id}?uri=/delay", `{"id":"{a}"}`)
	var gotPayload any
	var gotCaptures any
	handler := Middleware.MiddlewareHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPayload = r.Context().Value(payloadKey)
		gotCaptures = r.Context().Value(captureKey)
	}))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, e.newRequest("POST", "/delay/1ms", `{"id":"abc"}`))
	rp, ok := gotPayload.(*ResponsePayload)
	if !ok || string(rp.Payload) != `{"id":"{a}"}` {
		t.Fatalf("payload in the context = %v", gotPayload)
	}
	if captures, ok := gotCaptures.(map[string]string); !ok || captures["a"] != "abc" {
		t.Errorf("captures in the context = %v, want a=abc", gotCaptures)
	}
	if rr.Body.String() != `{"id":"abc"}` {
		t.Errorf("response = %q, want the payload with the capture filled in", rr.Body.String())
	}
}

func TestMiddlewareServesPayloadsOfTheProtocolOfTheRequest(t *testing.T) {
	e := newTestEnv(t)
	if err := PayloadManager.SetRPCResponsePayload(e.port, false, []byte("rpc payload"), "text/plain", "", "", "", "", "", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	rpcRequest := func() *http.Request {
		req := e.newRequest("POST", "/svc/method", "")
		util.SetIsJSONRPC(req, true)
		return req
	}
	rr := httptest.NewRecorder()
	e.direct.ServeHTTP(rr, rpcRequest())
	if rr.Body.String() != "rpc payload" {
		t.Errorf("an RPC request got %q, want the payload for RPC", rr.Body.String())
	}
	if got := e.traffic("POST", "/svc/method", "").Body.String(); got != "next" {
		t.Errorf("an HTTP request got %q, want it to ignore the payload for RPC", got)
	}
	e.set(t, "/set/default", "http payload")
	rr = httptest.NewRecorder()
	e.direct.ServeHTTP(rr, rpcRequest())
	if rr.Body.String() != "rpc payload" {
		t.Errorf("an RPC request got %q, want the payload for RPC and not the one for HTTP", rr.Body.String())
	}
}

func TestMiddlewareFillsInCapturesOfThePayload(t *testing.T) {
	e := newTestEnv(t)
	e.set(t, "/set/header/x-id={id}", "id={id}")
	if got := e.get("/x", "X-Id", "42").Body.String(); got != "id=42" {
		t.Errorf("response = %q", got)
	}
}

func TestMiddlewareServesJSONPayloadsFromMatches(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/json")
	e.set(t, "/set/matches", `{"payload":{"b":2,"a":[1,2]},"matches":[{"uriPrefix":"`+uri+`"}]}`)
	rr := e.get(uri)
	got := decodeJSON(t, rr.Body.Bytes())
	if got["b"].(float64) != 2 || len(got["a"].([]any)) != 2 {
		t.Errorf("response = %q, want the payload", rr.Body.String())
	}
	if h := header(rr, constants.HeaderContentType); !strings.HasPrefix(h, constants.ContentTypeJSON) {
		t.Errorf("content type = %q, want JSON", h)
	}
	if h := header(rr, constants.HeaderGotoPayloadContentType); h != constants.ContentTypeJSON {
		t.Errorf("%s = %q, want JSON", constants.HeaderGotoPayloadContentType, h)
	}
}

func TestMiddlewareServesEscapedJSONPayloadsAsText(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/escaped")
	e.set(t, "/set/matches", `{"escapeJSON":true,"payload":{"a":1},"matches":[{"uriPrefix":"`+uri+`"}]}`)
	rr := e.get(uri)
	if rr.Body.String() != `{"a":1}` {
		t.Errorf("response = %q, want the payload as it was written", rr.Body.String())
	}
	if h := header(rr, constants.HeaderGotoPayloadLength); h != "7" {
		t.Errorf("%s = %q, want 7", constants.HeaderGotoPayloadLength, h)
	}
}

func TestMiddlewareServesTextPayloadsOfJSONContentTypeAsTheyAre(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/not-json")
	e.set(t, "/set/matches", `{"payload":"this is not json","matches":[{"uriPrefix":"`+uri+`"}]}`)
	rr := e.get(uri)
	if rr.Body.String() != "this is not json" {
		t.Errorf("response = %q, want the payload as it is", rr.Body.String())
	}
	if h := header(rr, constants.HeaderGotoPayloadLength); h != "16" {
		t.Errorf("%s = %q, want 16", constants.HeaderGotoPayloadLength, h)
	}
}

func TestMiddlewareServesStreamPayloadsFromMatches(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/chunks")
	e.set(t, "/set/matches", `{"streamPayload":["one","two","three"],"contentType":"text/plain","matches":[{"uriPrefix":"`+uri+`"}]}`)
	rr := e.get(uri)
	if rr.Body.String() != "onetwothree" {
		t.Errorf("response = %q, want the chunks", rr.Body.String())
	}
	if h := header(rr, constants.HeaderGotoPayloadCount); h != "3" {
		t.Errorf("%s = %q, want 3", constants.HeaderGotoPayloadCount, h)
	}
	if !rr.Flushed {
		t.Errorf("the chunks were not flushed")
	}
}

func TestMiddlewareStreamsToAWriterThatCanNotFlush(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/chunks-plain")
	e.set(t, "/set/matches", `{"streamPayload":["one","two"],"contentType":"text/plain","matches":[{"uriPrefix":"`+uri+`"}]}`)
	rr := httptest.NewRecorder()
	req := e.newRequest("GET", uri, "")
	e.direct.ServeHTTP(&plainWriter{rr}, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(logMessages(req), "Responding with configured payload") {
		t.Errorf("log = %q", logMessages(req))
	}
}

func TestMiddlewareReportsWritesThatFail(t *testing.T) {
	e := newTestEnv(t)
	e.set(t, "/set/default", "twelve bytes")
	req := e.newRequest("GET", "/x", "")
	e.direct.ServeHTTP(&failingWriter{httptest.NewRecorder()}, req)
	log := logMessages(req)
	if !strings.Contains(log, "Failed to write payload of length [12] with error: broken pipe") {
		t.Errorf("log = %q, want the failure to be reported with the length of the payload", log)
	}
	if strings.Contains(log, "%!") {
		t.Errorf("log = %q, has a message that was not formatted", log)
	}
}

func TestMiddlewareReportsJSONWritesThatFail(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/json-fails")
	e.set(t, "/set/matches", `{"payload":{"a":1},"matches":[{"uriPrefix":"`+uri+`"}]}`)
	req := e.newRequest("GET", uri, "")
	e.direct.ServeHTTP(&failingWriter{httptest.NewRecorder()}, req)
	log := logMessages(req)
	if !strings.Contains(log, "Failed to write JSON payload of length") || !strings.Contains(log, "Will send as non-JSON") {
		t.Errorf("log = %q, want the failure to write JSON to be reported", log)
	}
	if !strings.Contains(log, "Failed to write payload of length") {
		t.Errorf("log = %q, want the failure of the retry to be reported", log)
	}
	if strings.Contains(log, "%!") {
		t.Errorf("log = %q, has a message that was not formatted", log)
	}
}

func TestMiddlewareReportsStreamWritesThatFail(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/chunks-fail")
	e.set(t, "/set/matches", `{"streamPayload":["one","two"],"contentType":"text/plain","matches":[{"uriPrefix":"`+uri+`"}]}`)
	req := e.newRequest("GET", uri, "")
	e.direct.ServeHTTP(&failingFlushWriter{failingWriter{httptest.NewRecorder()}}, req)
	if log := logMessages(req); !strings.Contains(log, "Failed to write stream payload of length [0] with error: broken pipe") {
		t.Errorf("log = %q, want the failure to be reported", log)
	}
}

func TestMiddlewareReportsWhatItWrote(t *testing.T) {
	e := newTestEnv(t)
	e.set(t, "/set/default", "twelve bytes")
	req := e.newRequest("GET", "/x", "")
	e.direct.ServeHTTP(httptest.NewRecorder(), req)
	log := logMessages(req)
	if !strings.Contains(log, "Written payload of length [12] compared to configured size [12]") {
		t.Errorf("log = %q", log)
	}
	if strings.Contains(log, "%!") {
		t.Errorf("log = %q, has a message that was not formatted", log)
	}
}

func TestMiddlewareMatchesTheURIWithItsQueryAndAnyCase(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/Mixed/Case")
	e.set(t, "/set?uri="+uri, "mixed")
	for _, target := range []string{uri, strings.ToLower(uri), strings.ToUpper(uri), uri + "?x=1&y=2"} {
		if got := e.get(target).Body.String(); got != "mixed" {
			t.Errorf("GET %s = %q, want the payload", target, got)
		}
	}
}

func TestMiddlewareServesThePayloadOfATransform(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/transformed")
	e.set(t, "/transform?uri="+uri, `[{"mappings":[{"source":"a","target":"b","mode":"replace"}],"payload":{"b":"","kept":"yes"}}]`)
	rr := e.traffic("POST", uri, `{"a":"copied"}`)
	if rr.Body.String() == "next" || rr.Body.Len() == 0 {
		t.Fatalf("response = %q, want the transform to answer", rr.Body.String())
	}
	// What the transform does to the request payload is done by util.TransformPayload, which returns a request payload
	// that is valid JSON without applying the mappings (pkg/util/payloadUtil.go:167), so only the wiring is checked here.
	decodeJSON(t, rr.Body.Bytes())
	if got := e.traffic("POST", e.uri("/not-transformed"), `{"a":"x"}`).Body.String(); got != "next" {
		t.Errorf("response for another URI = %q, want the next handler's answer", got)
	}
}

func TestHandleURIServesThePayloadOfTheRequest(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/direct")
	e.set(t, "/set?uri="+uri, "direct payload")
	rr := httptest.NewRecorder()
	handleURI(rr, e.newRequest("GET", uri, ""))
	if rr.Body.String() != "direct payload" {
		t.Errorf("response = %q, want the payload", rr.Body.String())
	}
}

func TestHandleURIEchoesTheRequestWhenNoPayloadMatches(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/direct-header")
	e.set(t, "/set/header/x-env=prod?uri="+uri, "only for prod")
	rr := httptest.NewRecorder()
	handleURI(rr, e.newRequest("GET", uri, "", "X-Env", "dev"))
	if !strings.Contains(rr.Body.String(), uri) || rr.Body.String() == "only for prod" {
		t.Errorf("response = %q, want an echo of the request", rr.Body.String())
	}
	decodeJSON(t, rr.Body.Bytes())
}

func TestHandleURIDoesNothingWithoutPayloads(t *testing.T) {
	e := newTestEnv(t)
	rr := httptest.NewRecorder()
	handleURI(rr, e.newRequest("GET", "/x", ""))
	if rr.Code != http.StatusOK || rr.Body.Len() != 0 {
		t.Errorf("response = %d %q, want an empty 200", rr.Code, rr.Body.String())
	}
}

func TestHandleURIDoesNothingForPayloadRequests(t *testing.T) {
	e := newTestEnv(t)
	e.set(t, "/set/default", "default")
	rr := httptest.NewRecorder()
	handleURI(rr, e.newRequest("GET", "/some/payload/5", ""))
	if rr.Body.Len() != 0 {
		t.Errorf("response = %q, want nothing for a request that asks for a payload of its own", rr.Body.String())
	}
}

func TestRouterServesPayloadsAndFallsThroughToTheNextHandler(t *testing.T) {
	e := newTestEnv(t)
	e.useRouter(t)
	uri := e.uri("/routed")
	e.set(t, "/set/header/x-env=prod?uri="+uri, "only for prod")
	if got := e.routedGet(t, uri, "X-Env", "prod").Body.String(); got != "only for prod" {
		t.Errorf("response = %q, want the payload", got)
	}
	// The route of the URI is there, so the request is answered by the handler of the URI, which echoes it.
	rr := e.routedGet(t, uri, "X-Env", "dev")
	if !strings.Contains(rr.Body.String(), uri) {
		t.Errorf("response = %q, want an echo of the request", rr.Body.String())
	}
	if got := e.routedGet(t, e.uri("/unknown")).Body.String(); got != "next" {
		t.Errorf("response for an unknown URI = %q, want the next handler's answer", got)
	}
}

func TestRequestBodyIsStillReadableByTheNextHandler(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/body-kept")
	e.set(t, "/set/body~alpha?uri="+uri, "matched")
	e.set(t, "/set/header/x-env=prod?uri="+uri, "header payload")
	var seen string
	var kept bool
	handler := Middleware.MiddlewareHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, kept = r.Body.(*util.ReReader)
		seen = readAll(t, r.Body)
	}))
	for _, body := range []string{"nothing to match", "some body with  spaces "} {
		seen, kept = "", false
		handler.ServeHTTP(httptest.NewRecorder(), e.newRequest("POST", uri, body))
		if seen != body {
			t.Errorf("the next handler read %q, want the body of the request, %q", seen, body)
		}
		if !kept {
			t.Errorf("the body of the request is not a re-reader any more, which the handlers after this one look for")
		}
	}
}

func TestRequestBodyIsStillReadableAfterAPayloadWasFoundForIt(t *testing.T) {
	e := newTestEnv(t)
	e.set(t, "/set/default", "payload;")
	var seen string
	handler := Middleware.MiddlewareHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = readAll(t, r.Body)
	}))
	// A status request is also handed to the next handler after the payload is written.
	handler.ServeHTTP(httptest.NewRecorder(), e.newRequest("POST", "/status/200", "the body"))
	if seen != "the body" {
		t.Errorf("the next handler read %q, want the body of the request", seen)
	}
}

func TestPayloadsAreServedConcurrently(t *testing.T) {
	e := newTestEnv(t)
	e.set(t, "/set/default", "default;")
	e.set(t, "/set/header/x-env=prod", "prod;")
	e.set(t, "/set/query/mode=fast", "fast;")
	e.set(t, "/set/body~needle?uri="+e.uri("/search"), "found;")
	const workers, perWorker = 8, 25
	type probe struct {
		req  *http.Request
		want string
	}
	probes := make([][]probe, workers)
	for w := range probes {
		for i := 0; i < perWorker; i++ {
			switch i % 4 {
			case 0:
				probes[w] = append(probes[w], probe{e.newRequest("GET", fmt.Sprintf("/c/%d/%d", w, i), ""), "default;"})
			case 1:
				probes[w] = append(probes[w], probe{e.newRequest("GET", fmt.Sprintf("/c/%d/%d", w, i), "", "X-Env", "prod"), "prod;"})
			case 2:
				probes[w] = append(probes[w], probe{e.newRequest("GET", fmt.Sprintf("/c/%d/%d?mode=fast", w, i), ""), "fast;"})
			default:
				probes[w] = append(probes[w], probe{e.newRequest("POST", e.uri("/search"), "a needle in it"), "found;"})
			}
		}
	}
	got := make([][]string, workers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for w := range probes {
		got[w] = make([]string, perWorker)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i, p := range probes[w] {
				rr := httptest.NewRecorder()
				e.direct.ServeHTTP(rr, p.req)
				got[w][i] = rr.Body.String()
			}
		}()
	}
	close(start)
	wg.Wait()
	for w := range probes {
		for i, p := range probes[w] {
			if got[w][i] != p.want {
				t.Errorf("worker %d, request %d: response = %q, want %q", w, i, got[w][i], p.want)
			}
		}
	}
	if e.nextCalls.Load() != 0 {
		t.Errorf("the next handler was called %d times, want none", e.nextCalls.Load())
	}
}

// routedGetFor sends a GET request for the port of the other test environment through the router of this one.
func (e *testEnv) routedGetFor(t *testing.T, other *testEnv, target string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	e.routed.ServeHTTP(rr, other.newRequest("GET", target, "", headers...))
	return rr
}

// The payloads of no URI (the default one, and the ones for a header or a query) are served by the payload middleware. They
// used to be given a route for the empty URI, which matches every request of every port, and which can't be taken back from the
// router: after one of them was set, and cleared, every request answered an empty 200 instead of reaching the handlers that
// serve it.
func TestPayloadsOfNoURIDoNotTakeOverTheRouter(t *testing.T) {
	e, other := newTestEnv(t), newTestEnv(t)
	e.useRouter(t)
	e.set(t, "/set/default", "dflt")
	e.set(t, "/set/header/x-env=prod", "hdr")
	e.set(t, "/set/query/mode=fast", "qry")
	for _, tt := range []struct {
		target  string
		headers []string
		want    string
	}{
		{"/foo", nil, "dflt"},
		{"/foo", []string{"X-Env", "prod"}, "hdr"},
		{"/foo?mode=fast", nil, "qry"},
	} {
		if got := e.routedGet(t, tt.target, tt.headers...).Body.String(); got != tt.want {
			t.Errorf("GET %s %v = %q, want the payload %q", tt.target, tt.headers, got, tt.want)
		}
	}
	// the port of another environment has no payloads, and is served by the handlers that are behind the router
	if got := e.routedGetFor(t, other, "/foo").Body.String(); got != "next" {
		t.Errorf("GET /foo on a port without payloads = %q, want the answer of the next handler", got)
	}
	e.configure(t, "POST", "/server/response/payload/clear", "")
	if got := e.routedGet(t, "/foo").Body.String(); got != "next" {
		t.Errorf("GET /foo after the payloads were cleared = %q, want the answer of the next handler", got)
	}
}

// The route of a payload URI stays in the router when the payload goes away, and when the URI has payloads on another port only,
// and it must leave the requests that it has nothing to answer with to the handlers that come after it.
func TestRouteOfAPayloadURILeavesRequestsItHasNothingToAnswerWith(t *testing.T) {
	e, other := newTestEnv(t), newTestEnv(t)
	e.useRouter(t)
	uri := e.uri("/lingering")
	e.set(t, "/set?uri="+uri, "uri payload")
	if got := e.routedGet(t, uri).Body.String(); got != "uri payload" {
		t.Fatalf("GET %s = %q, want the payload", uri, got)
	}
	if got := e.routedGetFor(t, other, uri).Body.String(); got != "next" {
		t.Errorf("GET %s on a port without payloads = %q, want the answer of the next handler", uri, got)
	}
	e.configure(t, "POST", "/server/response/payload/clear", "")
	if got := e.routedGet(t, uri).Body.String(); got != "next" {
		t.Errorf("GET %s after the payload was cleared = %q, want the answer of the next handler", uri, got)
	}
	// set again for the same URI, which is registered once: it is served again
	e.set(t, "/set?uri="+uri, "again")
	if got := e.routedGet(t, uri).Body.String(); got != "again" {
		t.Errorf("GET %s after the payload was set again = %q, want the payload", uri, got)
	}
}

// The route of a URI stays when its payload goes away, and a request for that URI is for the handlers behind the router, unless
// a payload of the port can answer it. A payload of another URI can't.
func TestRouteOfAPayloadURILeavesRequestsToTheNextHandlerWhenOnlyPayloadsOfOtherURIsRemain(t *testing.T) {
	e, other := newTestEnv(t), newTestEnv(t)
	e.useRouter(t)
	gone, kept := e.uri("/gone"), e.uri("/kept")
	e.set(t, "/set?uri="+gone, "gone payload")
	if got := e.routedGet(t, gone).Body.String(); got != "gone payload" {
		t.Fatalf("GET %s = %q, want the payload", gone, got)
	}
	e.configure(t, "POST", "/server/response/payload/clear", "")
	e.set(t, "/set?uri="+kept, "kept payload")
	other.set(t, "/set?uri="+gone, "payload of another port")
	if got := e.routedGet(t, kept).Body.String(); got != "kept payload" {
		t.Errorf("GET %s = %q, want its payload", kept, got)
	}
	if got := e.routedGet(t, gone).Body.String(); got != "next" {
		t.Errorf("GET %s with the payload of %s only = %q, want the answer of the next handler", gone, kept, got)
	}
	// the other port has a payload for the URI, and this one has none for it
	if got := e.routedGetFor(t, other, gone).Body.String(); got != "payload of another port" {
		t.Errorf("GET %s on the port that has a payload for it = %q, want the payload", gone, got)
	}
	// a payload that is for any request answers it, and so does one of a header that the request has
	e.set(t, "/set/header/x-env=prod", "hdr")
	if got := e.routedGet(t, gone).Body.String(); got != "next" {
		t.Errorf("GET %s with a header payload that its request doesn't have = %q, want the answer of the next handler", gone, got)
	}
	if got := e.routedGet(t, gone, "X-Env", "prod").Body.String(); got != "hdr" {
		t.Errorf("GET %s with the header of a header payload = %q, want that payload", gone, got)
	}
	e.set(t, "/set/default", "dflt")
	if got := e.routedGet(t, gone).Body.String(); got != "dflt" {
		t.Errorf("GET %s with a default payload = %q, want that payload", gone, got)
	}
}

func TestHasPayloadsForCreatesNothingForPortsWithoutPayloads(t *testing.T) {
	e := newTestEnv(t)
	if PayloadManager.hasPayloadsFor(e.newRequest("GET", "/x", "")) {
		t.Error("a port without payloads has payloads")
	}
	PayloadManager.lock.RLock()
	_, created := PayloadManager.payloads[e.port]
	PayloadManager.lock.RUnlock()
	if created {
		t.Error("asking whether a port has payloads created the payloads of the port")
	}
	e.set(t, "/set/default", "x")
	if !PayloadManager.hasPayloadsFor(e.newRequest("GET", "/x", "")) {
		t.Error("a port with a payload has none")
	}
}
