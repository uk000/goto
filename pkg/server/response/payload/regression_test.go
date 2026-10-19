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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"goto/pkg/constants"
	"goto/pkg/types"
	"goto/pkg/util"

	"gopkg.in/yaml.v3"
)

// panics runs f and returns what it panicked with, or nil.
func panics(f func()) (recovered any) {
	defer func() { recovered = recover() }()
	f()
	return nil
}

// Setting a payload for a URI and a query kept it with the payloads for a URI and a header, so it was never found by
// the query, and was found by a header of the name of the query.
func TestRegressionPayloadForURIAndQueryIsKeptByQuery(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/regression-query")
	e.set(t, "/set/query/mode=fast?uri="+uri, "by query")
	if got := e.get(uri + "?mode=fast").Body.String(); got != "by query" {
		t.Errorf("a request with the query got %q, want the payload", got)
	}
	if got := e.get(uri, "Mode", "fast").Body.String(); got != "next" {
		t.Errorf("a request with a header of the name of the query got %q, want it to get no payload", got)
	}
	pp := PayloadManager.GetPortResponse(e.port).protoPayload(false)
	if pp.PayloadByURIAndQuery[uri] == nil || pp.PayloadByURIAndHeaders[uri] != nil {
		t.Errorf("by URI and query: %v, by URI and header: %v", pp.PayloadByURIAndQuery[uri], pp.PayloadByURIAndHeaders[uri])
	}
}

// The headers of the response were set after the payload was written, which is too late for them to be sent, and the
// length that they had was the size that was asked for, which has units (2KB is 2000 bytes).
func TestRegressionPayloadOfRequestedSizeHasHeaders(t *testing.T) {
	e := newTestEnv(t)
	rr := e.call("GET", "/server/response/payload/2KB", "")
	res := rr.Result()
	for _, name := range []string{constants.HeaderContentLength, constants.HeaderGotoPayloadLength} {
		if got := res.Header.Get(name); got != strconv.Itoa(rr.Body.Len()) || rr.Body.Len() != 2000 {
			t.Errorf("%s = %q for a body of %d bytes, want 2000", name, got, rr.Body.Len())
		}
	}
	if got := res.Header.Get(constants.HeaderContentType); got == "" {
		t.Errorf("the response has no content type")
	}
}

// The messages about the payload that was written had a number for a string.
func TestRegressionLogMessagesAreFormatted(t *testing.T) {
	e := newTestEnv(t)
	e.set(t, "/set/default", "twelve bytes")
	req := e.newRequest("GET", "/x", "")
	e.direct.ServeHTTP(httptest.NewRecorder(), req)
	if log := logMessages(req); strings.Contains(log, "%!") || !strings.Contains(log, "compared to configured size [12]") {
		t.Errorf("log = %q", log)
	}
	req = e.newRequest("GET", "/x", "")
	e.direct.ServeHTTP(&failingWriter{httptest.NewRecorder()}, req)
	if log := logMessages(req); strings.Contains(log, "%!") || !strings.Contains(log, "Failed to write payload of length [12]") {
		t.Errorf("log = %q", log)
	}
}

// A body match that is not a regular expression made the server panic, while the payload was being added or while it was
// being prepared, and it left a route behind.
func TestRegressionInvalidBodyMatchDoesNotPanic(t *testing.T) {
	setupRoutes()
	before := routeCount()
	var err error
	if p := panics(func() {
		_, err = newResponsePayload([]byte("x"), false, false, "text/plain", "/regression-regex", "", "", "", []string{"(unclosed"}, nil, nil, 0, 0)
	}); p != nil {
		t.Errorf("adding a payload with an invalid body match panicked: %v", p)
	} else if err == nil {
		t.Errorf("a payload with an invalid body match was accepted")
	}
	rp := NewResponsePayload([]byte("x"), []*RequestMatch{NewRequestMatch("/regression-regex-matches")}, nil, "text/plain", false, false, false, false)
	rp.BodyMatch = []string{"[unclosed"}
	if p := panics(func() { err = rp.Process() }); p != nil {
		t.Errorf("preparing a payload with an invalid body match panicked: %v", p)
	} else if err == nil {
		t.Errorf("a payload with an invalid body match was prepared")
	}
	if after := routeCount(); after != before {
		t.Errorf("%d routes were left behind by payloads that were refused", after-before)
	}
}

// A query name that is not a regular expression made the server panic.
func TestRegressionInvalidQueryNameIsReported(t *testing.T) {
	e := newTestEnv(t)
	var rr *httptest.ResponseRecorder
	if p := panics(func() { rr = e.call("POST", "/server/response/payload/set/query/(unclosed", "payload") }); p != nil {
		t.Fatalf("setting a payload for an invalid query name panicked: %v", p)
	}
	if !strings.Contains(rr.Body.String(), "Failed to set payload for query") {
		t.Errorf("answer = %q, want the failure to be reported", rr.Body.String())
	}
	if PayloadManager.GetPortResponse(e.port).HTTPPayloads.HasAnyPayload() {
		t.Errorf("a payload was set for an invalid query name")
	}
}

// The payload to use for a body was whichever of the candidates came first in a map, and a body that is not JSON ended the
// search, so a payload that matches keywords could be missed.
func TestRegressionBodyMatchDoesNotDependOnMapOrder(t *testing.T) {
	byKeywords := rp([]string{"needle"}, nil)
	byPath := rp(nil, []string{"{.id}"})
	payloads := map[string]*ResponsePayload{"a": byPath, "b": byPath, "c": byPath, "d": byPath, "z": byKeywords}
	for i := 0; i < 64; i++ {
		if _, got, _, matched := getPayloadForBodyMatch(nopBody("a needle in plain text"), payloads); !matched || got != byKeywords {
			t.Fatalf("round %d: matched %v with %p, want the payload that matches keywords", i, matched, got)
		}
		if _, got, _, matched := getPayloadForBodyMatch(nopBody(`{"id":1,"text":"a needle"}`), payloads); !matched || got != byKeywords {
			t.Fatalf("round %d: for JSON, matched %v with %p, want the payload that matches keywords", i, matched, got)
		}
	}
}

// The value of a header to match was looked up by the name of the header as it was written, while it is kept by the name in
// lower case, so a header written with capitals matched any value.
func TestRegressionHeaderValueIsMatchedForNamesWithCapitals(t *testing.T) {
	setupRoutes()
	e := newTestEnv(t)
	match := NewRequestMatch(e.uri("/regression-header"))
	match.Headers = map[string]string{"X-Env": "prod"}
	match.Queries = map[string]string{"Mode": "fast"}
	rp := NewResponsePayload([]byte("x"), []*RequestMatch{match}, nil, "text/plain", false, false, false, false)
	if err := rp.Process(); err != nil {
		t.Fatal(err)
	}
	uri := e.uri("/regression-header")
	if !match.match(uri, http.Header{"X-Env": {"prod"}}, map[string][]string{"Mode": {"fast"}}) {
		t.Errorf("the match does not match the values that it asks for")
	}
	if match.match(uri, http.Header{"X-Env": {"dev"}}, map[string][]string{"Mode": {"fast"}}) {
		t.Errorf("the match matched another value of the header")
	}
	if match.match(uri, http.Header{"X-Env": {"prod"}}, map[string][]string{"Mode": {"slow"}}) {
		t.Errorf("the match matched another value of the query")
	}
}

// A URI with "stream" or "binary" in it was taken as the URI of a stream or of a binary payload, as was a header with
// such a name.
func TestRegressionNamesWithStreamOrBinaryDoNotChangeTheKindOfPayload(t *testing.T) {
	e := newTestEnv(t)
	streamURI, binaryURI := e.uri("/mystream/x"), e.uri("/binary/x")
	e.set(t, "/set?uri="+streamURI, "text for the stream uri")
	e.set(t, "/set?uri="+binaryURI, "text for the binary uri")
	e.set(t, "/set/header/stream=on", "text for the stream header")
	e.set(t, "/set/header/binary=on", "text for the binary header")
	if got := e.get(streamURI).Body.String(); got != "text for the stream uri" {
		t.Errorf("response for the URI with stream in it = %q", got)
	}
	rr := e.get(binaryURI)
	if rr.Body.String() != "text for the binary uri" || header(rr, constants.HeaderContentType) != "plain/text" {
		t.Errorf("response for the URI with binary in it = %q of type %q", rr.Body.String(), header(rr, constants.HeaderContentType))
	}
	if got := e.get("/x", "Stream", "on").Body.String(); got != "text for the stream header" {
		t.Errorf("response for the header with stream in its name = %q", got)
	}
	rr = e.get("/x", "Binary", "on")
	if rr.Body.String() != "text for the binary header" || header(rr, constants.HeaderContentType) != "plain/text" {
		t.Errorf("response for the header with binary in its name = %q of type %q", rr.Body.String(), header(rr, constants.HeaderContentType))
	}
}

// A payload without a delay made ranging over it with a delay panic.
func TestRegressionRangeTextWithDelayWithoutDelayDoesNotPanic(t *testing.T) {
	p := &Payload{TextStream: []string{"a", "b"}, StreamCount: 2}
	calls := 0
	if r := panics(func() {
		p.RangeTextWithDelay(0, nil, func(string, int, bool) (bool, error) { calls++; return true, nil })
		p.RangeTextWithDelay(0, &types.Duration{Duration: time.Millisecond}, func(string, int, bool) (bool, error) { calls++; return true, nil })
	}); r != nil {
		t.Fatalf("ranging with a delay over a payload that has none panicked: %v", r)
	}
	if calls != 4 {
		t.Errorf("%d items were given, want 4", calls)
	}
}

// A stream of nothing that was to be repeated a number of times never ended.
func TestRegressionStreamOfNothingEnds(t *testing.T) {
	p := &Payload{TextStream: []string{}, StreamCount: 3}
	done := make(chan struct{})
	go func() {
		p.RangeText(0, func(string, int, bool) (bool, error) { return true, nil })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("ranging over a stream of nothing does not end")
	}
}

// Marshaling a payload to YAML marshaled it again, over and over, until the stack was exhausted.
func TestRegressionPayloadCanBeMarshaledToYAML(t *testing.T) {
	out, err := yaml.Marshal(&Payload{Text: "hello"})
	if err != nil || !strings.Contains(string(out), "text: hello") {
		t.Errorf("YAML = %q, err %v", string(out), err)
	}
}

// Listing the payloads read what the requests that set them write, without a lock.
func TestRegressionListingPayloadsWhileTheyChange(t *testing.T) {
	e := newTestEnv(t)
	pr := PayloadManager.GetPortResponse(e.port)
	const rounds = 60
	prepared := make([]*ResponsePayload, rounds)
	for i := range prepared {
		r, err := newResponsePayload([]byte("p"), false, false, "text/plain", "", "x-regression", fmt.Sprint(i), "", nil, nil, nil, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		prepared[i] = r
	}
	listings := make([]*http.Request, rounds)
	for i := range listings {
		listings[i] = e.newRequest("GET", "/server/response/payload", "")
	}
	var wg sync.WaitGroup
	codes := make([]int, rounds)
	wg.Add(2)
	go func() {
		defer wg.Done()
		pp := pr.protoPayload(false)
		for i, r := range prepared {
			pp.setHeaderResponsePayload("x-regression", r)
			if i%2 == 1 {
				pp.removeHeaderResponsePayload("x-regression", "")
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i, req := range listings {
			rr := httptest.NewRecorder()
			api.ServeHTTP(rr, req)
			codes[i] = rr.Code
			if !strings.HasPrefix(rr.Body.String(), "{") {
				codes[i] = -1
			}
		}
	}()
	wg.Wait()
	for i, c := range codes {
		if c != http.StatusOK {
			t.Errorf("listing %d answered %d", i, c)
		}
	}
}

// The message about the payloads that were set showed a payload with nothing in it when a list of payloads was given.
func TestRegressionMessageShowsTheListOfPayloadsThatWasSet(t *testing.T) {
	e := newTestEnv(t)
	uriA, uriB := e.uri("/regression-list-a"), e.uri("/regression-list-b")
	body := `[{"payload":{"n":"a"},"matches":[{"uriPrefix":"` + uriA + `"}]},{"payload":{"n":"b"},"matches":[{"uriPrefix":"` + uriB + `"}]}]`
	msg := e.configure(t, "POST", "/server/response/payload/set/matches", body)
	if !strings.Contains(msg, uriA) || !strings.Contains(msg, uriB) {
		t.Errorf("message = %q, want it to show the payloads that were set", msg)
	}
}

// The payload that was found for a request could be the one of a URI that is less specific than another that matches the
// request, and that has a payload for a header that the request has, since the URIs were visited in the order of a map.
func TestRegressionOverlappingURIsDoNotDependOnMapOrder(t *testing.T) {
	e := newTestEnv(t)
	pr := PayloadManager.GetPortResponse(e.port)
	pp := pr.protoPayload(false)
	general, specific := e.uri("/regression-overlap/{x}"), e.uri("/regression-overlap/special")
	if err := pr.setURIResponsePayload(false, false, []byte("general"), false, general, "text/plain", nil, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := pr.setResponsePayloadForURIWithHeader(false, []byte("specific+header"), false, specific, "x-h", "v", "text/plain", 0, 0); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 64; i++ {
		_, rp, _, found := pp.GetMatchingResponsePayload(e.uri("/regression-overlap/special"), http.Header{"X-H": {"v"}}, nil, nil)
		if !found || string(rp.Payload) != "specific+header" {
			t.Fatalf("round %d: found %v with %v, want the payload for the URI and the header", i, found, rp)
		}
	}
}

// A payload of a default size was trimmed to the size, but it was not extended to it, as the documentation says, and a
// size that is not positive made trimming panic.
func TestRegressionPayloadIsExtendedToTheSize(t *testing.T) {
	e := newTestEnv(t)
	e.set(t, "/set/default/12", "abc")
	got := e.get("/x").Body.String()
	if len(got) != 12 || !strings.HasPrefix(got, "abc") {
		t.Errorf("payload = %q, want 12 bytes starting with abc", got)
	}
	var fixed []byte
	if p := panics(func() { fixed = FixPayload([]byte("abc"), -1) }); p != nil {
		t.Errorf("FixPayload with a negative size panicked: %v", p)
	} else if string(fixed) != "abc" {
		t.Errorf("FixPayload with a negative size = %q, want the payload as it was", fixed)
	}
}

// The route to set a payload for a URI and a body match was taken by the route to set a payload for a URI, which takes any
// segment after /set as the protocol.
func TestRegressionBodyKeywordRouteIsReachable(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/regression-keywords")
	msg := e.set(t, "/set/body~alpha,beta?uri="+uri, "keywords matched")
	if !strings.Contains(msg, "match [alpha,beta]") {
		t.Errorf("message = %q, want it to tell the match that was set", msg)
	}
	if got := e.traffic("POST", uri, "alpha then beta").Body.String(); got != "keywords matched" {
		t.Errorf("a body with the keywords got %q", got)
	}
	if got := e.traffic("POST", uri, "something else").Body.String(); got != "next" {
		t.Errorf("a body without the keywords got %q, want the payload to depend on the body", got)
	}
	if PayloadManager.GetPortResponse(e.port).GetGRPCPayloads().HasAnyPayload() {
		t.Errorf("the payload was set for gRPC, for a protocol that is named after the match")
	}
}

// The body of a request that no payload matched was read for the match and not given back, so the handlers after this one
// found it empty, and the body that was given back when a payload matched had lost its type.
func TestRegressionRequestBodySurvivesTheLookupForPayloads(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/regression-body")
	e.set(t, "/set/header/x-env=prod?uri="+uri, "header payload")
	var seen string
	var kept bool
	handler := Middleware.MiddlewareHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, kept = r.Body.(*util.ReReader)
		seen = readAll(t, r.Body)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), e.newRequest("POST", uri, "the body of the request"))
	if seen != "the body of the request" {
		t.Errorf("the next handler read %q", seen)
	}
	if !kept {
		t.Errorf("the body is not a re-reader any more")
	}
}

// The base64Encode and base64Decode of a payload had an effect only when the payload had parts.
func TestRegressionBase64OfAPayloadWithoutParts(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/regression-base64")
	e.set(t, "/set/matches", `{"base64Encode":true,"contentType":"text/plain","payload":"hello","matches":[{"uriPrefix":"`+uri+`"}]}`)
	if got := e.get(uri).Body.String(); got != "aGVsbG8" {
		t.Errorf("payload = %q, want hello as base64", got)
	}
}

// The JSON paths of a body match could not be given the way the documentation gives them: the path first, and then the
// name of what it captures.
func TestRegressionBodyPathsAsDocumented(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/regression-paths")
	e.set(t, "/set/body/paths/.foo.bar={a},.foo.baz={b}?uri="+uri, `{"bar":"{a}","baz":"{b}","message":"{a} is {b}"}`)
	rr := e.traffic("POST", uri, `{"foo":{"bar":"hi","baz":"hello"}}`)
	if rr.Body.String() != `{"bar":"hi","baz":"hello","message":"hi is hello"}` {
		t.Errorf("response = %q, want the values that were captured", rr.Body.String())
	}
}

// A request that has several headers or queries that have payloads got any one of them, whichever the map of the request
// gave first.
func TestRegressionLookupByHeaderOrQueryIsRepeatable(t *testing.T) {
	first, second := rp(nil, nil), rp(nil, nil)
	payloads := map[string]map[string]*ResponsePayload{"a": {"": first}, "b": {"": second}}
	kv := map[string][]string{"A": {"1"}, "B": {"1"}}
	for i := 0; i < 64; i++ {
		if got, _ := getPayloadForKV(kv, payloads); got != first {
			t.Fatalf("round %d: the payload differs from one lookup to the next", i)
		}
	}
}

// A request that matches the payloads of several URIs that have request matches got any one of them, whichever the map of
// the URIs gave first.
func TestRegressionPayloadsWithMatchesAreChosenTheSameWayEveryTime(t *testing.T) {
	e := newTestEnv(t)
	for _, suffix := range []string{"/a", "/a/b", "/a/b/c", "/a/b/c/d"} {
		rp := NewResponsePayload([]byte(suffix), []*RequestMatch{NewRequestMatch(e.uri("/same" + suffix))}, nil, "text/plain", false, false, false, false)
		if err := PayloadManager.SetURIResponsePayloadWithMatches(e.port, rp, false); err != nil {
			t.Fatal(err)
		}
	}
	pp := PayloadManager.GetPortResponse(e.port).protoPayload(false)
	var first string
	for i := 0; i < 64; i++ {
		_, rp, _, found := pp.GetMatchingResponsePayload(e.uri("/same/a/b/c/d"), nil, nil, nil)
		if !found {
			t.Fatalf("round %d: nothing was found", i)
		}
		if i == 0 {
			first = string(rp.Payload)
		} else if string(rp.Payload) != first {
			t.Fatalf("round %d: the payload is %q, and it was %q in round 0", i, rp.Payload, first)
		}
	}
}

// Preparing a set of payloads for use wrote to it while holding only the lock for reading, so it could run at the same
// time as the lookups.
func TestRegressionInitializingPayloadsTakesTheWriteLock(t *testing.T) {
	pp := &ProtoPayloads{}
	pp.init()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			pp.init()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			pp.HasAnyPayload()
		}
	}()
	wg.Wait()
}
