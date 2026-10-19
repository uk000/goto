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
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"goto/pkg/tls"
	"goto/pkg/types"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

// routeCount is the number of routes that are registered for the URIs of payloads.
func routeCount() int {
	n := 0
	util.MatchRouter.Walk(func(*mux.Route, *mux.Router, []*mux.Route) error {
		n++
		return nil
	})
	return n
}

func nopBody(s string) io.ReadCloser { return io.NopCloser(strings.NewReader(s)) }

func TestNewResponsePayload(t *testing.T) {
	setupRoutes()
	t.Run("defaults", func(t *testing.T) {
		r, err := newResponsePayload([]byte("hello {name}"), false, false, "", "", "", "", "", nil, nil, nil, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if r.ContentType != "application/json" || r.IsBinary || r.IsStream || r.Delay != nil {
			t.Errorf("payload = %+v", r)
		}
		if len(r.fillers) != 1 || r.fillers[0] != "name" {
			t.Errorf("fillers = %v, want the one in the payload", r.fillers)
		}
	})
	t.Run("binary payloads have no fillers", func(t *testing.T) {
		r, err := newResponsePayload([]byte("hello {name}"), false, true, "application/octet-stream", "", "", "", "", nil, nil, nil, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if !r.IsBinary || len(r.fillers) != 0 {
			t.Errorf("payload = %+v", r)
		}
	})
	t.Run("header value", func(t *testing.T) {
		r, err := newResponsePayload([]byte("x"), false, false, "text/plain", "", "x-env", "", "prod", nil, nil, nil, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if r.HeaderMatch != "x-env" || r.HeaderValueMatch != "prod" || r.HeaderCaptureKey != "" {
			t.Errorf("payload = %+v", r)
		}
	})
	t.Run("header capture", func(t *testing.T) {
		r, err := newResponsePayload([]byte("x"), false, false, "text/plain", "", "x-env", "", "{env}", nil, nil, nil, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if r.HeaderValueMatch != "" || r.HeaderCaptureKey != "{env}" {
			t.Errorf("payload = %+v", r)
		}
	})
	t.Run("query value and capture", func(t *testing.T) {
		r, err := newResponsePayload([]byte("x"), false, false, "text/plain", "", "", "mode", "fast", nil, nil, nil, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if r.QueryMatch != "mode" || r.QueryValueMatch != "fast" || r.QueryCaptureKey != "" {
			t.Errorf("payload = %+v", r)
		}
		r, err = newResponsePayload([]byte("x"), false, false, "text/plain", "", "", "mode", "{m}", nil, nil, nil, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if r.QueryValueMatch != "" || r.QueryCaptureKey != "{m}" {
			t.Errorf("payload = %+v", r)
		}
	})
	t.Run("delay", func(t *testing.T) {
		r, err := newResponsePayload([]byte("x"), false, false, "text/plain", "", "", "", "", nil, nil, nil, 2, 4)
		if err != nil {
			t.Fatal(err)
		}
		if r.Delay == nil || r.Delay.Min.Duration != 2 || r.Delay.Max.Duration != 4 || r.Delay.Count != -1 {
			t.Errorf("delay = %+v", r.Delay)
		}
	})
	t.Run("URI captures", func(t *testing.T) {
		r, err := newResponsePayload([]byte("x"), false, false, "text/plain", "/newpayload-a/{a}/b/{b}", "", "", "", nil, nil, nil, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(r.URICaptureKeys, ",") != "a,b" || r.uriRegexp == nil || r.router == nil {
			t.Errorf("payload = %+v", r)
		}
		if !r.uriRegexp.MatchString("/newpayload-a/1/b/2/more?x=y") || r.uriRegexp.MatchString("/other/1/b/2") {
			t.Errorf("the regexp of the URI is %v", r.uriRegexp)
		}
	})
	t.Run("body matches", func(t *testing.T) {
		r, err := newResponsePayload([]byte("x"), false, false, "text/plain", "", "", "", "", []string{"a", "b"}, nil, nil, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if r.bodyMatchRegexp == nil || !r.bodyMatchRegexp.MatchString("xx a yy b") || r.bodyMatchRegexp.MatchString("b a") {
			t.Errorf("the body regexp is %v", r.bodyMatchRegexp)
		}
		r, err = newResponsePayload([]byte("x"), false, false, "text/plain", "", "", "", "", nil, []string{"a={.x}"}, nil, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if r.bodyJsonPath == nil || r.bodyJsonPath.IsEmpty() || r.BodyPaths["a"] != "{.x}" {
			t.Errorf("the body paths are %v", r.BodyPaths)
		}
	})
	t.Run("transforms are prepared", func(t *testing.T) {
		tr := &util.Transform{Mappings: []*util.JSONTransform{{Source: "a", Target: "b"}}}
		r, err := newResponsePayload(nil, false, false, "text/plain", "", "", "", "", nil, nil, []*util.Transform{tr}, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Transforms) != 1 {
			t.Errorf("transforms = %v", r.Transforms)
		}
	})
}

func TestNewResponsePayloadRefusesMatchesThatAreNotRegularExpressions(t *testing.T) {
	setupRoutes()
	before := routeCount()
	if _, err := newResponsePayload([]byte("x"), false, false, "text/plain", "/refused-body", "", "", "", []string{"(unclosed"}, nil, nil, 0, 0); err == nil ||
		!strings.Contains(err.Error(), "invalid body match") {
		t.Errorf("err = %v, want a complaint about the body match", err)
	}
	if _, err := newResponsePayload([]byte("x"), false, false, "text/plain", "/refused-query", "", "(unclosed", "", nil, nil, nil, 0, 0); err == nil ||
		!strings.Contains(err.Error(), "invalid query match") {
		t.Errorf("err = %v, want a complaint about the query match", err)
	}
	if after := routeCount(); after != before {
		t.Errorf("%d routes were registered for payloads that were refused", after-before)
	}
}

func TestProcessPreparesThePayload(t *testing.T) {
	setupRoutes()
	tests := []struct {
		name       string
		rp         *ResponsePayload
		wantType   string
		wantBinary bool
		wantStream bool
		wantJSON   bool
	}{
		{"defaults to JSON", NewResponsePayload([]byte(`{"a":1}`), []*RequestMatch{NewRequestMatch("/process-a")}, nil, "", false, false, false, false), "application/json", false, false, true},
		{"text", NewResponsePayload([]byte("hi"), []*RequestMatch{NewRequestMatch("/process-b")}, nil, "text/plain", false, false, false, false), "text/plain", false, false, false},
		{"binary", NewResponsePayload([]byte("bin"), []*RequestMatch{NewRequestMatch("/process-c")}, nil, "application/octet-stream", false, false, false, false), "application/octet-stream", true, false, false},
		{"stream", &ResponsePayload{StreamPayload: []types.RawBytes{[]byte("a"), []byte("b")}, ContentType: "text/plain", RequestMatches: []*RequestMatch{NewRequestMatch("/process-d")}}, "text/plain", false, true, false},
	}
	for _, tt := range tests {
		if err := tt.rp.Process(); err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if tt.rp.ContentType != tt.wantType || tt.rp.IsBinary != tt.wantBinary || tt.rp.IsStream != tt.wantStream || tt.rp.IsJSON != tt.wantJSON {
			t.Errorf("%s: payload = type %q, binary %v, stream %v, JSON %v", tt.name, tt.rp.ContentType, tt.rp.IsBinary, tt.rp.IsStream, tt.rp.IsJSON)
		}
		if tt.rp.RequestCapture == nil || tt.rp.RequestCapture.headerCaptureKeys == nil || tt.rp.RequestCapture.queryCaptureKeys == nil {
			t.Errorf("%s: the capture is not prepared: %+v", tt.name, tt.rp.RequestCapture)
		}
	}
}

func TestProcessCleansTheJSONOfThePayload(t *testing.T) {
	setupRoutes()
	rp := NewResponsePayload([]byte("{\"a\":\n1,\n\"b\":\"\\\"x\\\"\"}"), []*RequestMatch{NewRequestMatch("/process-clean")}, nil, "", false, false, false, false)
	if err := rp.Process(); err != nil {
		t.Fatal(err)
	}
	if string(rp.Payload) != `{"a":1,"b":""x""}` {
		t.Errorf("payload = %q", rp.Payload)
	}
}

func TestProcessPreparesMatchesAndCaptures(t *testing.T) {
	setupRoutes()
	match := NewRequestMatch("/Process-Match/{id}")
	match.Headers = map[string]string{"X-Env": "prod", "X-User": "{user}", "X-Any": "{}"}
	match.Queries = map[string]string{"Mode": "fast", "Who": "{who}"}
	rp := NewResponsePayload([]byte("x"), []*RequestMatch{match}, &RequestCapture{
		Headers: map[string]string{"x-ref": "ref-{ref}"},
		Queries: map[string]string{"page": "p{page}"},
	}, "text/plain", false, false, false, false)
	if err := rp.Process(); err != nil {
		t.Fatal(err)
	}
	if match.URIPrefix != "/process-match/{id}" || match.uriRegexp == nil || match.router == nil || strings.Join(match.uriCaptureKeys, ",") != "id" {
		t.Errorf("the URI of the match is not prepared: %+v", match)
	}
	if len(match.headerValueMatches) != 1 || match.headerValueMatches["x-env"] != "prod" {
		t.Errorf("header value matches = %v, want only x-env", match.headerValueMatches)
	}
	if len(match.queryValueMatches) != 1 || match.queryValueMatches["mode"] != "fast" {
		t.Errorf("query value matches = %v, want only mode", match.queryValueMatches)
	}
	for _, k := range []string{"x-ref", "x-user"} {
		if rp.RequestCapture.headerCaptureKeys[k] == nil {
			t.Errorf("header capture keys = %v, want one for %s", rp.RequestCapture.headerCaptureKeys, k)
		}
	}
	for _, k := range []string{"page", "who"} {
		if rp.RequestCapture.queryCaptureKeys[k] == nil {
			t.Errorf("query capture keys = %v, want one for %s", rp.RequestCapture.queryCaptureKeys, k)
		}
	}
}

func TestProcessRefusesBodyMatchesThatAreNotRegularExpressions(t *testing.T) {
	setupRoutes()
	before := routeCount()
	rp := NewResponsePayload([]byte("x"), []*RequestMatch{NewRequestMatch("/process-bad-body")}, nil, "text/plain", false, false, false, false)
	rp.BodyMatch = []string{"(unclosed"}
	if err := rp.Process(); err == nil || !strings.Contains(err.Error(), "invalid body match") {
		t.Errorf("err = %v, want a complaint about the body match", err)
	}
	if after := routeCount(); after != before {
		t.Errorf("%d routes were registered for a payload that was refused", after-before)
	}
}

func TestProcessUsesTheBodyMatchOfThePayload(t *testing.T) {
	setupRoutes()
	rp := NewResponsePayload([]byte("x"), []*RequestMatch{NewRequestMatch("/process-body")}, nil, "text/plain", false, false, false, false)
	rp.BodyMatch = []string{"first", "second"}
	if err := rp.Process(); err != nil {
		t.Fatal(err)
	}
	if rp.bodyMatchRegexp == nil || !rp.bodyMatchRegexp.MatchString("a first and a second") || rp.bodyMatchRegexp.MatchString("second first") {
		t.Errorf("the body regexp is %v", rp.bodyMatchRegexp)
	}
}

func TestRequestMatchMatchesURIHeadersAndQueries(t *testing.T) {
	setupRoutes()
	e := newTestEnv(t)
	match := NewRequestMatch(e.uri("/match"))
	match.Headers = map[string]string{"X-Env": "prod", "X-Id": "{id}"}
	match.Queries = map[string]string{"Mode": "fast", "Page": "{page}"}
	rp := NewResponsePayload([]byte("x"), []*RequestMatch{match}, nil, "text/plain", false, false, false, false)
	if err := rp.Process(); err != nil {
		t.Fatal(err)
	}
	uri := e.uri("/match")
	full := func() (string, http.Header, url.Values) {
		return uri + "/sub?Mode=fast&Page=3", http.Header{"X-Env": {"prod"}, "X-Id": {"7"}}, url.Values{"Mode": {"fast"}, "Page": {"3"}}
	}
	if u, h, q := full(); !match.match(u, h, q) {
		t.Errorf("the match does not match a request that has everything")
	}
	tests := []struct {
		name   string
		change func(uri *string, h http.Header, q url.Values)
	}{
		{"another URI", func(uri *string, h http.Header, q url.Values) { *uri = e.uri("/other") }},
		{"header with another value", func(uri *string, h http.Header, q url.Values) { h.Set("X-Env", "dev") }},
		{"header with the value in another case", nil},
		{"missing header with a value to match", func(uri *string, h http.Header, q url.Values) { h.Del("X-Env") }},
		{"missing header to capture", func(uri *string, h http.Header, q url.Values) { h.Del("X-Id") }},
		{"query with another value", func(uri *string, h http.Header, q url.Values) { q.Set("Mode", "slow") }},
		{"missing query with a value to match", func(uri *string, h http.Header, q url.Values) { q.Del("Mode") }},
		{"missing query to capture", func(uri *string, h http.Header, q url.Values) { q.Del("Page") }},
	}
	for _, tt := range tests {
		u, h, q := full()
		if tt.change == nil {
			h.Set("X-Env", "PROD")
			if !match.match(u, h, q) {
				t.Errorf("%s: the value of a header is matched in any case", tt.name)
			}
			continue
		}
		tt.change(&u, h, q)
		if match.match(u, h, q) {
			t.Errorf("%s: matched", tt.name)
		}
	}
}

func TestRequestMatchWithoutHeadersOrQueriesMatchesTheURIOnly(t *testing.T) {
	setupRoutes()
	e := newTestEnv(t)
	match := NewRequestMatch(e.uri("/uri-only"))
	rp := NewResponsePayload([]byte("x"), []*RequestMatch{match}, nil, "text/plain", false, false, false, false)
	if err := rp.Process(); err != nil {
		t.Fatal(err)
	}
	if !match.match(e.uri("/uri-only/x?a=b"), nil, nil) || match.match(e.uri("/elsewhere"), nil, nil) {
		t.Errorf("a match with a URI only does not match by the URI")
	}
	if !(&RequestMatch{}).match("/anything", nil, nil) {
		t.Errorf("a match with nothing in it does not match every request")
	}
}

func TestMatchKV(t *testing.T) {
	values := map[string]string{"x-env": "prod"}
	tests := []struct {
		name  string
		key   string
		value string
		want  bool
	}{
		{"value", "x-env", "prod", true},
		{"value in another case", "x-env", "PROD", true},
		{"another value", "x-env", "dev", false},
		{"key in another case", "X-Env", "prod", true},
		{"key in another case with another value", "X-Env", "dev", false},
		{"no value", "x-env", "", false},
		{"key without a value to match", "x-other", "anything", true},
		{"key without a value to match and no value", "x-other", "", false},
	}
	for _, tt := range tests {
		if got := matchKV(tt.key, values, tt.value); got != tt.want {
			t.Errorf("%s: matchKV(%q, %q) = %v, want %v", tt.name, tt.key, tt.value, got, tt.want)
		}
	}
	if !matchKV("k", nil, "v") || matchKV("k", nil, "") {
		t.Errorf("matchKV without value matches does not check that there is a value")
	}
}

// precedenceSetup sets a payload of every kind for a URI, and returns what is needed to make a request that all of them
// match, and to take the payloads away one at a time, from the one with the highest precedence down.
type precedenceSetup struct {
	pp      *ProtoPayloads
	uri     string
	header  http.Header
	query   url.Values
	body    string
	removes []func()
	names   []string
}

func newPrecedenceSetup(t *testing.T, e *testEnv) *precedenceSetup {
	t.Helper()
	pr := PayloadManager.GetPortResponse(e.port)
	pp := pr.protoPayload(false)
	uri := e.uri("/precedence")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	matched := NewResponsePayload([]byte("matches"), []*RequestMatch{{URIPrefix: uri, Headers: map[string]string{"X-A": "1"}}}, nil, "text/plain", false, false, false, false)
	must(PayloadManager.SetURIResponsePayloadWithMatches(e.port, matched, false))
	must(pr.setResponsePayloadForURIWithHeader(false, []byte("uri+header"), false, uri, "x-h", "vh", "text/plain", 0, 0))
	must(pr.setResponsePayloadForURIWithQuery(false, []byte("uri+query"), false, uri, "q", "vq", "text/plain", 0, 0))
	must(pr.setResponsePayloadForURIWithBodyMatch(false, []byte("uri+keywords"), false, uri, "kw", "text/plain", false, 0, 0))
	must(pr.setResponsePayloadForURIWithBodyMatch(false, []byte("uri+paths"), false, uri, "{.p}", "text/plain", true, 0, 0))
	must(pr.setURIResponsePayload(false, false, []byte("uri"), false, uri, "text/plain", nil, 0, 0))
	must(pr.setHeaderResponsePayload(false, []byte("header"), false, "x-g", "", "text/plain", 0, 0))
	must(pr.setQueryResponsePayload(false, []byte("query"), false, "g", "", "text/plain", 0, 0))
	must(pr.setDefaultResponsePayload(false, []byte("default"), "text/plain", 0, 0, 0))
	s := &precedenceSetup{
		pp:     pp,
		uri:    uri,
		header: http.Header{"X-A": {"1"}, "X-H": {"vh"}, "X-G": {"1"}},
		query:  url.Values{"q": {"vq"}, "g": {"1"}},
		body:   `{"p":1,"t":"kw"}`,
	}
	add := func(name string, remove func()) {
		s.names = append(s.names, name)
		s.removes = append(s.removes, remove)
	}
	locked := func(f func()) func() {
		return func() {
			pp.lock.Lock()
			defer pp.lock.Unlock()
			f()
		}
	}
	add("matches", locked(func() { delete(pp.PayloadByURIWithMatches, uri) }))
	add("uri+header", func() { pp.removeURIWithHeaderResponsePayload(uri, "x-h", "vh") })
	add("uri+query", func() { pp.removeURIWithQueryResponsePayload(uri, "q", "vq") })
	add("uri+keywords", func() { pp.removeURIWithBodyMatchResponsePayload(uri, "kw") })
	add("uri+paths", func() { pp.removeURIWithBodyMatchResponsePayload(uri, "{.p}") })
	add("uri", func() { pp.removeURIResponsePayload(uri) })
	add("header", func() { pp.removeHeaderResponsePayload("x-g", "") })
	add("query", func() { pp.removeQueryResponsePayload("g", "") })
	add("default", func() { pp.setDefaultResponsePayload(nil) })
	return s
}

func (s *precedenceSetup) winner() string {
	_, rp, _, found := s.pp.GetMatchingResponsePayload(s.uri+"/x?q=vq&g=1", s.header, s.query, nopBody(s.body))
	if !found {
		return ""
	}
	return string(rp.Payload)
}

func TestMatchingPayloadsArePreferredInTheDocumentedOrder(t *testing.T) {
	e := newTestEnv(t)
	s := newPrecedenceSetup(t, e)
	for i, name := range s.names {
		want := strings.ReplaceAll(name, "+", "+")
		for round := 0; round < 16; round++ {
			if got := s.winner(); got != want {
				t.Fatalf("with the payloads from %q down, round %d: the payload is %q, want %q", name, round, got, want)
			}
		}
		s.removes[i]()
	}
	if got := s.winner(); got != "" {
		t.Errorf("with everything removed, the payload is %q, want none", got)
	}
	if s.pp.HasAnyPayload() {
		t.Errorf("payloads remain after everything was removed: %+v", s.pp)
	}
}

func TestMatchingPayloadsOfOverlappingURIs(t *testing.T) {
	e := newTestEnv(t)
	pr := PayloadManager.GetPortResponse(e.port)
	pp := pr.protoPayload(false)
	general, specific := e.uri("/overlap/{x}"), e.uri("/overlap/special")
	for _, err := range []error{
		pr.setURIResponsePayload(false, false, []byte("general"), false, general, "text/plain", nil, 0, 0),
		pr.setResponsePayloadForURIWithHeader(false, []byte("specific+header"), false, specific, "x-h", "v", "text/plain", 0, 0),
		pr.setURIResponsePayload(false, false, []byte("specific"), false, specific, "text/plain", nil, 0, 0),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	lookup := func(uri string, h http.Header) string {
		_, rp, _, found := pp.GetMatchingResponsePayload(uri, h, nil, nil)
		if !found {
			return ""
		}
		return string(rp.Payload)
	}
	for i := 0; i < 32; i++ {
		if got := lookup(e.uri("/overlap/special"), http.Header{"X-H": {"v"}}); got != "specific+header" {
			t.Fatalf("round %d: with the header, the payload is %q, want the one for the URI and the header", i, got)
		}
		if got := lookup(e.uri("/overlap/special"), nil); got != "specific" {
			t.Fatalf("round %d: without the header, the payload is %q, want the one for the more specific URI", i, got)
		}
		if got := lookup(e.uri("/overlap/other"), http.Header{"X-H": {"v"}}); got != "general" {
			t.Fatalf("round %d: for another URI, the payload is %q, want the one for the general URI", i, got)
		}
	}
}

func TestMatchingPayloadsGivesTheBodyBackWhenItWasRead(t *testing.T) {
	e := newTestEnv(t)
	pr := PayloadManager.GetPortResponse(e.port)
	pp := pr.protoPayload(false)
	uri := e.uri("/body-back")
	if err := pr.setResponsePayloadForURIWithBodyMatch(false, []byte("by body"), false, uri, "needle", "text/plain", false, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := pr.setURIResponsePayload(false, false, []byte("by uri"), false, uri, "text/plain", nil, 0, 0); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"has a needle", "has none"} {
		reader, rp, _, found := pp.GetMatchingResponsePayload(uri, nil, nil, nopBody(body))
		if !found || rp == nil {
			t.Fatalf("body %q: nothing was found", body)
		}
		if reader == nil {
			t.Fatalf("body %q: no reader was returned, so the body that was read is lost", body)
		}
		if again, _ := io.ReadAll(reader); string(again) != body {
			t.Errorf("body %q: the reader gives %q", body, again)
		}
	}
	// The body is not read when no payload depends on it.
	reader, _, _, _ := pp.GetMatchingResponsePayload(e.uri("/elsewhere"), nil, nil, nopBody("untouched"))
	if reader != nil {
		t.Errorf("a reader was returned for a body that was not read")
	}
}

func TestMatchingPayloadsIgnoresTheBodyWhenThereIsNone(t *testing.T) {
	e := newTestEnv(t)
	pr := PayloadManager.GetPortResponse(e.port)
	uri := e.uri("/no-body")
	if err := pr.setResponsePayloadForURIWithBodyMatch(false, []byte("by body"), false, uri, "needle", "text/plain", false, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, _, found := pr.protoPayload(false).GetMatchingResponsePayload(uri, nil, nil, nil); found {
		t.Errorf("a payload that depends on the body is found for a request without one")
	}
}

func TestHasAnyPayload(t *testing.T) {
	e := newTestEnv(t)
	pr := PayloadManager.GetPortResponse(e.port)
	pp := pr.protoPayload(false)
	if pp.HasAnyPayload() {
		t.Fatalf("a new set of payloads has payloads")
	}
	steps := []struct {
		name string
		set  func() error
		undo func()
	}{
		{"default", func() error { return pr.setDefaultResponsePayload(false, []byte("d"), "text/plain", 0, 0, 0) }, func() { pp.setDefaultResponsePayload(nil) }},
		{"URI", func() error {
			return pr.setURIResponsePayload(false, false, []byte("u"), false, e.uri("/has"), "text/plain", nil, 0, 0)
		}, func() { pp.removeURIResponsePayload(e.uri("/has")) }},
		{"header", func() error {
			return pr.setHeaderResponsePayload(false, []byte("h"), false, "x-has", "", "text/plain", 0, 0)
		}, func() { pp.removeHeaderResponsePayload("x-has", "") }},
		{"query", func() error {
			return pr.setQueryResponsePayload(false, []byte("q"), false, "has", "", "text/plain", 0, 0)
		}, func() { pp.removeQueryResponsePayload("has", "") }},
	}
	for _, s := range steps {
		if err := s.set(); err != nil {
			t.Fatal(err)
		}
		if !pp.HasAnyPayload() {
			t.Errorf("%s: a payload that was set is not there", s.name)
		}
		s.undo()
		if pp.HasAnyPayload() {
			t.Errorf("%s: a payload that was removed is still there", s.name)
		}
	}
}

func TestRemovingAPayloadOfAURIKeepsTheURIWhileOtherPayloadsUseIt(t *testing.T) {
	e := newTestEnv(t)
	pr := PayloadManager.GetPortResponse(e.port)
	pp := pr.protoPayload(false)
	uri := e.uri("/shared")
	if err := pr.setURIResponsePayload(false, false, []byte("u"), false, uri, "text/plain", nil, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := pr.setResponsePayloadForURIWithHeader(false, []byte("uh"), false, uri, "x-h", "v", "text/plain", 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := pr.setResponsePayloadForURIWithQuery(false, []byte("uq"), false, uri, "q", "v", "text/plain", 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := pr.setResponsePayloadForURIWithBodyMatch(false, []byte("ub"), false, uri, "kw", "text/plain", false, 0, 0); err != nil {
		t.Fatal(err)
	}
	pp.removeURIWithHeaderResponsePayload(uri, "x-h", "v")
	pp.removeURIWithQueryResponsePayload(uri, "q", "v")
	pp.removeURIWithBodyMatchResponsePayload(uri, "kw")
	if _, _, _, found := pp.GetMatchingResponsePayload(uri, nil, nil, nil); !found {
		t.Errorf("the payload of the URI is gone after the others were removed")
	}
	pp.removeURIResponsePayload(uri)
	if pp.HasAnyPayload() {
		t.Errorf("the URI is tracked after all its payloads were removed: %v", pp.allURIResponsePayloads)
	}
}

func TestRemovingPayloadsThatAreNotThereIsHarmless(t *testing.T) {
	e := newTestEnv(t)
	pp := PayloadManager.GetPortResponse(e.port).protoPayload(false)
	pp.removeURIResponsePayload("/nothing")
	pp.removeHeaderResponsePayload("nothing", "x")
	pp.removeQueryResponsePayload("nothing", "x")
	pp.removeURIWithHeaderResponsePayload("/nothing", "h", "v")
	pp.removeURIWithQueryResponsePayload("/nothing", "q", "v")
	pp.removeURIWithBodyMatchResponsePayload("/nothing", "m")
	if pp.HasAnyPayload() {
		t.Errorf("a payload appeared from removing nothing")
	}
}

func TestSettingANilPayloadChangesNothing(t *testing.T) {
	e := newTestEnv(t)
	pp := PayloadManager.GetPortResponse(e.port).protoPayload(false)
	pp.setURIResponsePayload("/u", nil)
	pp.setURIResponsePayloadWithMatches("/u", nil)
	pp.setHeaderResponsePayload("h", nil)
	pp.setURIWithHeaderResponsePayload("/u", "h", nil)
	pp.setQueryResponsePayload("q", nil)
	pp.setURIWithQueryResponsePayload("/u", "q", nil)
	pp.setURIWithBodyMatchResponsePayload("/u", "m", nil)
	if pp.HasAnyPayload() {
		t.Errorf("a payload appeared from setting nil")
	}
}

func TestRemovingHeaderAndQueryPayloadsWithACaptureValueRemovesThePayloadForAnyValue(t *testing.T) {
	e := newTestEnv(t)
	pr := PayloadManager.GetPortResponse(e.port)
	pp := pr.protoPayload(false)
	uri := e.uri("/capture-removal")
	if err := pr.setHeaderResponsePayload(false, []byte("h"), false, "x-c", "{v}", "text/plain", 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := pr.setQueryResponsePayload(false, []byte("q"), false, "c", "{v}", "text/plain", 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := pr.setResponsePayloadForURIWithHeader(false, []byte("uh"), false, uri, "x-c", "{v}", "text/plain", 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := pr.setResponsePayloadForURIWithQuery(false, []byte("uq"), false, uri, "c", "{v}", "text/plain", 0, 0); err != nil {
		t.Fatal(err)
	}
	for _, empty := range []func() error{
		func() error { return pr.setHeaderResponsePayload(false, nil, false, "x-c", "{v}", "", 0, 0) },
		func() error { return pr.setQueryResponsePayload(false, nil, false, "c", "{v}", "", 0, 0) },
		func() error {
			return pr.setResponsePayloadForURIWithHeader(false, nil, false, uri, "x-c", "{v}", "", 0, 0)
		},
		func() error {
			return pr.setResponsePayloadForURIWithQuery(false, nil, false, uri, "c", "{v}", "", 0, 0)
		},
	} {
		if err := empty(); err != nil {
			t.Fatal(err)
		}
	}
	if pp.HasAnyPayload() {
		t.Errorf("payloads remain after they were removed: %+v", pp)
	}
}

func TestPrepareJSONStreamPayload(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		count   int
		want    string
	}{
		{"array repeated", `[{"a":1},{"a":2}]`, 5, `{"a":1},{"a":2},{"a":1},{"a":2},{"a":1}`},
		{"array cut", `[{"a":1},{"a":2},{"a":3}]`, 2, `{"a":1},{"a":2}`},
		{"object", `{"a":1}`, 3, `{"a":1},{"a":1},{"a":1}`},
		{"no count", `[{"a":1}]`, 0, ``},
		{"not JSON", `plain text`, 3, ``},
		{"empty", ``, 3, ``},
	}
	for _, tt := range tests {
		rp := &ResponsePayload{Payload: []byte(tt.payload)}
		rp.PrepareJSONStreamPayload(tt.count)
		got := []string{}
		for _, b := range rp.StreamPayload {
			got = append(got, string(b))
		}
		if strings.Join(got, ",") != tt.want || rp.StreamCount != tt.count {
			t.Errorf("%s: stream = %v (count %d), want %q", tt.name, got, rp.StreamCount, tt.want)
		}
	}
}

func filledPayloadRequest(target string, vars map[string]string, headers ...string) *http.Request {
	req := httptest.NewRequest("POST", target, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	return mux.SetURLVars(req, vars)
}

func TestFilledPayloadHasTheValuesOfTheRequest(t *testing.T) {
	setupRoutes()
	e := newTestEnv(t)
	r, err := newResponsePayload([]byte("id={id} item={item} none={none}"), false, false, "text/plain", e.uri("/f/{id}/{item}"), "", "", "", nil, nil, nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := string(r.getFilledPayload(filledPayloadRequest(e.uri("/f/7/abc"), map[string]string{"id": "7", "item": "abc"}), nil))
	if got != "id=7 item=abc none={none}" {
		t.Errorf("payload = %q", got)
	}
}

func TestFilledPayloadHasTheHeaderAndQueryThatWereCaptured(t *testing.T) {
	setupRoutes()
	byHeader, err := newResponsePayload([]byte("user={u} {u}"), false, false, "text/plain", "", "x-user", "", "{u}", nil, nil, nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(byHeader.getFilledPayload(filledPayloadRequest("/x", nil, "X-User", "bob"), nil)); got != "user=bob bob" {
		t.Errorf("by header: payload = %q", got)
	}
	if got := string(byHeader.getFilledPayload(filledPayloadRequest("/x", nil), nil)); got != "user={u} {u}" {
		t.Errorf("by header without the header: payload = %q, want the markers to stay", got)
	}
	byQuery, err := newResponsePayload([]byte("who={w}"), false, false, "text/plain", "", "", "who", "{w}", nil, nil, nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(byQuery.getFilledPayload(filledPayloadRequest("/x?who=alice&other=1", nil), nil)); got != "who=alice" {
		t.Errorf("by query: payload = %q", got)
	}
}

func TestFilledPayloadHasTheValuesThatWereCapturedByPatterns(t *testing.T) {
	setupRoutes()
	rp := NewResponsePayload([]byte("ref={ref} page={page}"), []*RequestMatch{NewRequestMatch("/filled-patterns")},
		&RequestCapture{Headers: map[string]string{"x-ref": "ref-{ref}"}, Queries: map[string]string{"page": "p{page}"}}, "text/plain", false, false, false, false)
	if err := rp.Process(); err != nil {
		t.Fatal(err)
	}
	req := filledPayloadRequest("/filled-patterns?page=p12", nil, "X-Ref", "ref-abc")
	if got := string(rp.getFilledPayload(req, nil)); got != "ref=abc page=12" {
		t.Errorf("payload = %q", got)
	}
	req = filledPayloadRequest("/filled-patterns?page=zzz", nil, "X-Ref", "other")
	if got := string(rp.getFilledPayload(req, nil)); got != "ref={ref} page={page}" {
		t.Errorf("payload for values that don't fit the patterns = %q, want the markers to stay", got)
	}
}

func TestFilledPayloadHasTheCapturesGivenToIt(t *testing.T) {
	setupRoutes()
	r, err := newResponsePayload([]byte("a={a} b={b}"), false, false, "text/plain", "", "", "", "", nil, nil, nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := string(r.getFilledPayload(filledPayloadRequest("/x", nil), map[string]string{"a": "1", "b": "2"}))
	if got != "a=1 b=2" {
		t.Errorf("payload = %q", got)
	}
}

func TestFilledPayloadReplacesText(t *testing.T) {
	setupRoutes()
	r, err := newResponsePayload([]byte("a=b=c"), false, false, "text/plain", "", "", "", "", nil, nil, nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	r.Replace = map[string]string{"=": ":"}
	if got := string(r.getFilledPayload(filledPayloadRequest("/x", nil), nil)); got != "a:b:c" {
		t.Errorf("payload = %q", got)
	}
}

func TestFilledPayloadHasJSONThatWasCapturedFromTheRequest(t *testing.T) {
	setupRoutes()
	build := func(detect, escape bool) *ResponsePayload {
		rp := NewResponsePayload([]byte(`{"v":"{v}"}`), []*RequestMatch{NewRequestMatch("/filled-json")},
			&RequestCapture{Headers: map[string]string{"x-v": "{v}"}}, "text/plain", false, false, detect, escape)
		if err := rp.Process(); err != nil {
			t.Fatal(err)
		}
		return rp
	}
	req := func() *http.Request { return filledPayloadRequest("/filled-json", nil, "X-V", `{"a":1}`) }
	if got := string(build(false, false).getFilledPayload(req(), nil)); got != `{"v":"{"a":1}"}` {
		t.Errorf("plain: payload = %q", got)
	}
	if got := string(build(true, false).getFilledPayload(req(), nil)); got != `{"v":{"a":1}}` {
		t.Errorf("detect JSON: payload = %q, want the JSON embedded", got)
	}
	if got := string(build(false, true).getFilledPayload(req(), nil)); got != `{"v":"{\"a\":1}"}` {
		t.Errorf("escape JSON: payload = %q, want the JSON escaped", got)
	}
}

func TestFilledPayloadWithParts(t *testing.T) {
	setupRoutes()
	build := func(parts *PayloadParts, payload string) *ResponsePayload {
		r, err := newResponsePayload([]byte(payload), false, false, "text/plain", "", "", "", "", nil, nil, nil, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		r.Parts = parts
		return r
	}
	fill := func(r *ResponsePayload) string {
		return string(r.getFilledPayload(filledPayloadRequest("/x", nil), nil))
	}
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	t.Run("encode a part", func(t *testing.T) {
		r := build(&PayloadParts{Pre: &PayloadPartProcess{SplitWith: ".", Base64Encode: []bool{false, true}}, Post: &PayloadPartProcess{JoinWith: "."}}, "head.body")
		if got := fill(r); got != "head."+enc("body") {
			t.Errorf("payload = %q", got)
		}
	})
	t.Run("decode a part", func(t *testing.T) {
		r := build(&PayloadParts{Pre: &PayloadPartProcess{SplitWith: ".", Base64Decode: []bool{false, true}}, Post: &PayloadPartProcess{JoinWith: "."}}, "head."+enc("body"))
		if got := fill(r); got != "head.body" {
			t.Errorf("payload = %q", got)
		}
	})
	t.Run("keep parts", func(t *testing.T) {
		r := build(&PayloadParts{Pre: &PayloadPartProcess{SplitWith: "."}, Post: &PayloadPartProcess{JoinWith: ".", Keep: []bool{true, false, true}}}, "a.b.c")
		if got := fill(r); got != "a.c" {
			t.Errorf("payload = %q", got)
		}
	})
	t.Run("encode after the parts are joined and split again", func(t *testing.T) {
		r := build(&PayloadParts{Post: &PayloadPartProcess{SplitWith: ".", JoinWith: "~", Base64Encode: []bool{false, true}}}, "a.b")
		if got := fill(r); got != "a~"+enc("b") {
			t.Errorf("payload = %q", got)
		}
	})
	t.Run("parts without post-processing", func(t *testing.T) {
		r := build(&PayloadParts{Pre: &PayloadPartProcess{SplitWith: "."}}, "a.b")
		// Parts are joined with the separator of the post-processing, and there is none here.
		if got := fill(r); got != "ab" {
			t.Errorf("payload = %q, want the parts to be put back together without a separator", got)
		}
	})
	t.Run("a part that is not base64 is kept", func(t *testing.T) {
		r := build(&PayloadParts{Pre: &PayloadPartProcess{Base64Decode: []bool{true}}}, "not base64!")
		if got := fill(r); got != "not base64!" {
			t.Errorf("payload = %q", got)
		}
	})
}

func TestFilledPayloadWithDecodedJSONPart(t *testing.T) {
	setupRoutes()
	r, err := newResponsePayload([]byte(base64.RawURLEncoding.EncodeToString([]byte("{ \"a\" : 1 }"))), false, false, "application/json", "", "", "", "", nil, nil, nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	r.IsJSON = true
	r.Parts = &PayloadParts{Pre: &PayloadPartProcess{Base64Decode: []bool{true}}}
	if got := string(r.getFilledPayload(filledPayloadRequest("/x", nil), nil)); got != `{"a":1}` {
		t.Errorf("payload = %q, want the JSON that was encoded, in its compact form", got)
	}
}

func TestFilledPayloadIsSigned(t *testing.T) {
	setupRoutes()
	if tls.DefaultCAKey == nil {
		t.Skip("the default CA key was not created")
	}
	r, err := newResponsePayload([]byte("head.body.[SIGNATURE]"), false, false, "text/plain", "", "", "", "", nil, nil, nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	r.Parts = &PayloadParts{
		Pre:  &PayloadPartProcess{SplitWith: "."},
		Post: &PayloadPartProcess{JoinWith: ".", Sign: []bool{true, true, false}},
	}
	got := string(r.getFilledPayload(filledPayloadRequest("/x", nil), nil))
	pieces := strings.Split(got, ".")
	if len(pieces) != 3 || pieces[0] != "head" || pieces[1] != "body" {
		t.Fatalf("payload = %q, want head.body.<signature>", got)
	}
	signature, err := base64.RawURLEncoding.DecodeString(pieces[2])
	if err != nil {
		t.Fatalf("the signature %q is not base64: %v", pieces[2], err)
	}
	hash := sha256.Sum256([]byte("head.body"))
	if err := rsa.VerifyPKCS1v15(&tls.DefaultCAKey.PublicKey, crypto.SHA256, hash[:], signature); err != nil {
		t.Errorf("the signature of the signed parts is not valid: %v", err)
	}
}

func TestPayloadsAreEncodedAndDecodedAsAWhole(t *testing.T) {
	setupRoutes()
	encoded := base64.RawURLEncoding.EncodeToString([]byte("hello"))
	r, err := newResponsePayload([]byte("hello"), false, false, "text/plain", "", "", "", "", nil, nil, nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	r.Base64Encode = true
	if got := string(r.getFilledPayload(filledPayloadRequest("/x", nil), nil)); got != encoded {
		t.Errorf("encoded payload = %q, want %q", got, encoded)
	}
	r, err = newResponsePayload([]byte(encoded), false, false, "text/plain", "", "", "", "", nil, nil, nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	r.Base64Decode = true
	if got := string(r.getFilledPayload(filledPayloadRequest("/x", nil), nil)); got != "hello" {
		t.Errorf("decoded payload = %q, want hello", got)
	}
}

func TestEncodeAndDecodePayloadRoundTrip(t *testing.T) {
	r := &ResponsePayload{}
	for _, s := range []string{"a", "ab", "abc", "some longer text with spaces", ""} {
		encoded := r.encodePayload([]byte(s))
		if strings.ContainsAny(string(encoded), "=+/") {
			t.Errorf("%q encodes to %q, which has characters that are not URL safe or has padding", s, encoded)
		}
		if got := string(r.decodePayload(encoded)); got != s {
			t.Errorf("%q round trips to %q", s, got)
		}
	}
}

func TestBodyPathsCanBeGivenInBothOrders(t *testing.T) {
	setupRoutes()
	tests := []struct {
		in   string
		want map[string]string
	}{
		{".foo.bar={a}", map[string]string{"a": "{.foo.bar}"}},
		{".one.three[0]={three.1}", map[string]string{"three.1": "{.one.three[0]}"}},
		{"a={.foo.bar}", map[string]string{"a": "{.foo.bar}"}},
		{"{.foo.bar}", map[string]string{"1": "{.foo.bar}"}},
		{".foo.bar={.baz}", map[string]string{".foo.bar": "{.baz}"}},
		{".foo.bar=plain", map[string]string{".foo.bar": "plain"}},
		{".foo.bar={a}{b}", map[string]string{".foo.bar": "{a}{b}"}},
		{"x.y={a}", map[string]string{"x.y": "{a}"}},
		{"", map[string]string{}},
	}
	for _, tt := range tests {
		r, err := newResponsePayload([]byte("x"), false, false, "text/plain", "", "", "", "", nil, []string{tt.in}, nil, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(r.BodyPaths) != len(tt.want) {
			t.Errorf("%q: paths = %v, want %v", tt.in, r.BodyPaths, tt.want)
			continue
		}
		for k, v := range tt.want {
			if r.BodyPaths[k] != v {
				t.Errorf("%q: paths = %v, want %v", tt.in, r.BodyPaths, tt.want)
			}
		}
	}
}

func TestRequestCaptureNonNil(t *testing.T) {
	rc := &RequestCapture{}
	rc.NonNil()
	if rc.headerCaptureKeys == nil || rc.queryCaptureKeys == nil {
		t.Errorf("capture keys are not created: %+v", rc)
	}
	rc.headerCaptureKeys["h"] = nil
	rc.NonNil()
	if _, ok := rc.headerCaptureKeys["h"]; !ok {
		t.Errorf("NonNil replaced the keys that exist")
	}
}

func TestNewResponsePayloadFromTheSpec(t *testing.T) {
	m := []*RequestMatch{NewRequestMatch("/x")}
	rp := NewResponsePayload([]byte("p"), m, &RequestCapture{}, "text/plain", true, false, true, false)
	if string(rp.Payload) != "p" || rp.ContentType != "text/plain" || !rp.Base64Encode || rp.Base64Decode || !rp.DetectJSON || rp.EscapeJSON || len(rp.RequestMatches) != 1 || rp.RequestCapture == nil {
		t.Errorf("payload = %+v", rp)
	}
}
