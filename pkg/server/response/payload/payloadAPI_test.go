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
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"goto/pkg/constants"
)

func header(rr *httptest.ResponseRecorder, name string) string {
	return rr.Result().Header.Get(name)
}

func TestSetDefaultPayload(t *testing.T) {
	e := newTestEnv(t)
	msg := e.set(t, "/set/default", `{"hello":"world"}`)
	if !strings.Contains(msg, "Default Payload set") {
		t.Errorf("message = %q", msg)
	}
	for _, method := range []string{"GET", "POST", "PUT"} {
		rr := e.traffic(method, e.uri("/anything/1"), "")
		if rr.Code != http.StatusOK || rr.Body.String() != `{"hello":"world"}` {
			t.Errorf("%s: response = %d %q, want the default payload", method, rr.Code, rr.Body.String())
		}
	}
	rr := e.get("/another/path")
	if got := header(rr, constants.HeaderContentType); got != "plain/text" {
		t.Errorf("content type = %q, want plain/text, which is the default of the API", got)
	}
	if got := header(rr, constants.HeaderGotoPayloadContentType); got != "plain/text" {
		t.Errorf("%s = %q, want plain/text", constants.HeaderGotoPayloadContentType, got)
	}
	if got := header(rr, constants.HeaderGotoPayloadLength); got != strconv.Itoa(len(`{"hello":"world"}`)) {
		t.Errorf("%s = %q, want the length of the payload", constants.HeaderGotoPayloadLength, got)
	}
}

func TestSetDefaultPayloadWithContentType(t *testing.T) {
	e := newTestEnv(t)
	e.set(t, "/set/default", `<a/>`, constants.HeaderResponseContentType, "application/xml")
	rr := e.get("/x")
	if got := header(rr, constants.HeaderContentType); got != "application/xml" || rr.Body.String() != "<a/>" {
		t.Errorf("response = %q %q, want application/xml with the payload", got, rr.Body.String())
	}
}

func TestSetDefaultPayloadReplacesTheDefault(t *testing.T) {
	e := newTestEnv(t)
	e.set(t, "/set/default", "first")
	e.set(t, "/set/default", "second")
	if got := e.get("/x").Body.String(); got != "second" {
		t.Errorf("response = %q, want the payload that was set last", got)
	}
}

func TestSetDefaultPayloadOfSize(t *testing.T) {
	tests := []struct {
		name, size, body string
		wantLen          int
		wantPrefix       string
	}{
		{"generated", "10", "", 10, ""},
		{"kilobytes", "1K", "", 1000, ""},
		{"kilobytes with B", "2KB", "", 2000, ""},
		{"megabytes", "1M", "", 1000000, ""},
		{"trimmed", "5", "abcdefghijkl", 5, "abcde"},
		{"extended", "12", "abc", 12, "abc"},
		{"as long as the size", "3", "abc", 3, "abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newTestEnv(t)
			msg := e.set(t, "/set/default/"+tt.size, tt.body)
			if !strings.Contains(msg, "size: "+strconv.Itoa(tt.wantLen)) {
				t.Errorf("message = %q, want it to tell the size %d", msg, tt.wantLen)
			}
			rr := e.get("/x")
			got := rr.Body.String()
			if len(got) != tt.wantLen || !strings.HasPrefix(got, tt.wantPrefix) {
				t.Errorf("payload = %q (%d bytes), want %d bytes starting with %q", got, len(got), tt.wantLen, tt.wantPrefix)
			}
			if h := header(rr, constants.HeaderGotoPayloadLength); h != strconv.Itoa(tt.wantLen) {
				t.Errorf("%s = %q, want %d", constants.HeaderGotoPayloadLength, h, tt.wantLen)
			}
		})
	}
}

func TestSetDefaultBinaryPayload(t *testing.T) {
	e := newTestEnv(t)
	// Binary payloads are served as they are: the markers in them are not filled in.
	body := string([]byte{0x00, 0xff, 0x10, '{', 'x', '}', 0x80})
	e.set(t, "/set/default/binary", body)
	rr := e.get("/x?x=1")
	if rr.Body.String() != body {
		t.Errorf("payload = %q, want %q", rr.Body.String(), body)
	}
	if got := header(rr, constants.HeaderContentType); got != "application/octet-stream" {
		t.Errorf("content type = %q, want application/octet-stream", got)
	}
}

func TestSetDefaultBinaryPayloadWithContentType(t *testing.T) {
	e := newTestEnv(t)
	e.set(t, "/set/default/binary", "PNG", constants.HeaderResponseContentType, "image/png")
	rr := e.get("/x")
	if header(rr, constants.HeaderContentType) != "image/png" || rr.Body.String() != "PNG" {
		t.Errorf("response = %q %q", header(rr, constants.HeaderContentType), rr.Body.String())
	}
}

func TestSetDefaultBinaryPayloadOfSize(t *testing.T) {
	e := newTestEnv(t)
	e.set(t, "/set/default/binary/16", "")
	rr := e.get("/x")
	if rr.Body.Len() != 16 {
		t.Errorf("payload has %d bytes, want 16", rr.Body.Len())
	}
	if got := header(rr, constants.HeaderContentType); got != "application/octet-stream" {
		t.Errorf("content type = %q, want application/octet-stream", got)
	}
}

func TestSetPayloadForURI(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/orders")
	msg := e.set(t, "/set?uri="+uri, "orders payload")
	if !strings.Contains(msg, "Payload set for URI ["+uri+"]") {
		t.Errorf("message = %q", msg)
	}
	for _, target := range []string{uri, uri + "/17", uri + "?a=b", strings.ToUpper(uri)} {
		if got := e.get(target).Body.String(); got != "orders payload" {
			t.Errorf("GET %s = %q, want the payload of the URI", target, got)
		}
	}
	for _, target := range []string{e.uri("/other"), "/orders"} {
		if got := e.get(target).Body.String(); got != "next" {
			t.Errorf("GET %s = %q, want the answer of the next handler", target, got)
		}
	}
}

func TestPayloadForURIIsRemovedByAnEmptyPayload(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/removable")
	e.set(t, "/set?uri="+uri, "here")
	if got := e.get(uri).Body.String(); got != "here" {
		t.Fatalf("GET = %q, want the payload", got)
	}
	e.set(t, "/set?uri="+uri, "")
	if got := e.get(uri).Body.String(); got != "next" {
		t.Errorf("GET after removal = %q, want the answer of the next handler", got)
	}
}

func TestPayloadForURIPrefersTheMoreSpecificURIOverTheDefault(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/special")
	e.set(t, "/set/default", "default")
	e.set(t, "/set?uri="+uri, "special")
	if got := e.get(uri).Body.String(); got != "special" {
		t.Errorf("GET %s = %q, want the payload of the URI", uri, got)
	}
	if got := e.get("/ordinary").Body.String(); got != "default" {
		t.Errorf("GET /ordinary = %q, want the default payload", got)
	}
}

func TestURIAndPathCapturesAreFilledIn(t *testing.T) {
	e := newTestEnv(t)
	e.useRouter(t)
	e.set(t, "/set?uri="+e.uri("/orders/{id}/items/{item}"), `{"id":"{id}","item":"{item}"}`)
	rr := e.routedGet(t, e.uri("/orders/7/items/abc"))
	if rr.Body.String() != `{"id":"7","item":"abc"}` {
		t.Errorf("response = %q, want the captures filled in", rr.Body.String())
	}
	rr = e.routedGet(t, e.uri("/orders/8/items/xyz?q=1"))
	if rr.Body.String() != `{"id":"8","item":"xyz"}` {
		t.Errorf("response with a query = %q, want the captures filled in", rr.Body.String())
	}
}

func TestSetPayloadForHeader(t *testing.T) {
	e := newTestEnv(t)
	msg := e.set(t, "/set/header/x-env=prod", "for prod")
	if !strings.Contains(msg, "Payload set for header [x-env : prod]") {
		t.Errorf("message = %q", msg)
	}
	tests := []struct {
		name    string
		headers []string
		want    string
	}{
		{"value matches", []string{"X-Env", "prod"}, "for prod"},
		{"value matches in another case", []string{"x-env", "PROD"}, "for prod"},
		{"another value", []string{"X-Env", "dev"}, "next"},
		{"no header", nil, "next"},
		{"another header", []string{"X-Other", "prod"}, "next"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := e.get(e.uri("/x"), tt.headers...).Body.String(); got != tt.want {
				t.Errorf("response = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSetPayloadForHeaderWithAnyValue(t *testing.T) {
	e := newTestEnv(t)
	e.set(t, "/set/header/x-any", "any value")
	if got := e.get("/x", "X-Any", "whatever").Body.String(); got != "any value" {
		t.Errorf("response = %q, want the payload", got)
	}
	if got := e.get("/x").Body.String(); got != "next" {
		t.Errorf("response without the header = %q, want the answer of the next handler", got)
	}
}

func TestSetPayloadForHeaderPrefersExactValueOverAnyValue(t *testing.T) {
	e := newTestEnv(t)
	e.set(t, "/set/header/x-env", "any")
	e.set(t, "/set/header/x-env=prod", "prod")
	if got := e.get("/x", "X-Env", "prod").Body.String(); got != "prod" {
		t.Errorf("response = %q, want the payload for the value", got)
	}
	if got := e.get("/x", "X-Env", "dev").Body.String(); got != "any" {
		t.Errorf("response = %q, want the payload for any value", got)
	}
}

func TestHeaderValueIsCapturedIntoPayload(t *testing.T) {
	e := newTestEnv(t)
	e.set(t, "/set/header/x-user={u}", "hello {u}")
	if got := e.get("/x", "X-User", "bob").Body.String(); got != "hello bob" {
		t.Errorf("response = %q, want the value of the header filled in", got)
	}
}

func TestPayloadForHeaderIsRemovedByAnEmptyPayload(t *testing.T) {
	e := newTestEnv(t)
	e.set(t, "/set/header/x-env=prod", "for prod")
	e.set(t, "/set/header/x-env=prod", "")
	if got := e.get("/x", "X-Env", "prod").Body.String(); got != "next" {
		t.Errorf("response = %q, want the answer of the next handler", got)
	}
}

func TestSetPayloadForQuery(t *testing.T) {
	e := newTestEnv(t)
	msg := e.set(t, "/set/query/mode=fast", "fast mode")
	if !strings.Contains(msg, "Payload set for query [mode : fast]") {
		t.Errorf("message = %q", msg)
	}
	tests := []struct{ target, want string }{
		{"/x?mode=fast", "fast mode"},
		{"/x?mode=FAST", "fast mode"},
		{"/x?a=b&mode=fast", "fast mode"},
		{"/x?mode=slow", "next"},
		{"/x", "next"},
	}
	for _, tt := range tests {
		if got := e.get(tt.target).Body.String(); got != tt.want {
			t.Errorf("GET %s = %q, want %q", tt.target, got, tt.want)
		}
	}
}

func TestSetPayloadForQueryWithAnyValue(t *testing.T) {
	e := newTestEnv(t)
	e.set(t, "/set/query/debug", "debugging")
	if got := e.get("/x?debug=1").Body.String(); got != "debugging" {
		t.Errorf("response = %q, want the payload", got)
	}
	if got := e.get("/x").Body.String(); got != "next" {
		t.Errorf("response without the query = %q, want the answer of the next handler", got)
	}
}

func TestQueryValueIsCapturedIntoPayload(t *testing.T) {
	e := newTestEnv(t)
	e.set(t, "/set/query/who={w}", "hello {w}")
	if got := e.get("/x?who=alice").Body.String(); got != "hello alice" {
		t.Errorf("response = %q, want the value of the query param filled in", got)
	}
}

func TestSetPayloadForURIAndHeader(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/both")
	msg := e.set(t, "/set/header/x-env=prod?uri="+uri, "uri and header")
	if !strings.Contains(msg, "header [x-env : prod]") {
		t.Errorf("message = %q", msg)
	}
	if got := e.get(uri, "X-Env", "prod").Body.String(); got != "uri and header" {
		t.Errorf("response = %q, want the payload", got)
	}
	if got := e.get(uri).Body.String(); got != "next" {
		t.Errorf("response without the header = %q, want the answer of the next handler", got)
	}
	if got := e.get(e.uri("/elsewhere"), "X-Env", "prod").Body.String(); got != "next" {
		t.Errorf("response for another URI = %q, want the answer of the next handler", got)
	}
}

func TestSetPayloadForURIAndHeaderIsRemovedByAnEmptyPayload(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/both-removed")
	e.set(t, "/set/header/x-env=prod?uri="+uri, "uri and header")
	e.set(t, "/set/header/x-env=prod?uri="+uri, "")
	if got := e.get(uri, "X-Env", "prod").Body.String(); got != "next" {
		t.Errorf("response = %q, want the answer of the next handler", got)
	}
}

func TestSetPayloadForURIAndQuery(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/uq")
	msg := e.set(t, "/set/query/mode=fast?uri="+uri, "uri and query")
	if !strings.Contains(msg, "query [mode : fast]") {
		t.Errorf("message = %q", msg)
	}
	if got := e.get(uri + "?mode=fast").Body.String(); got != "uri and query" {
		t.Errorf("response = %q, want the payload", got)
	}
	if got := e.get(uri + "?mode=slow").Body.String(); got != "next" {
		t.Errorf("response for another value = %q, want the answer of the next handler", got)
	}
	if got := e.get(e.uri("/elsewhere") + "?mode=fast").Body.String(); got != "next" {
		t.Errorf("response for another URI = %q, want the answer of the next handler", got)
	}
	e.set(t, "/set/query/mode=fast?uri="+uri, "")
	if got := e.get(uri + "?mode=fast").Body.String(); got != "next" {
		t.Errorf("response after removal = %q, want the answer of the next handler", got)
	}
}

func TestSetPayloadForURIAndBodyKeywords(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/keywords")
	msg := e.set(t, "/set/body~alpha,beta?uri="+uri, "keywords matched")
	if !strings.Contains(msg, "match [alpha,beta]") {
		t.Errorf("message = %q", msg)
	}
	tests := []struct{ name, body, want string }{
		{"in order", "an alpha and a beta", "keywords matched"},
		{"in another case", "ALPHA, Beta", "keywords matched"},
		{"out of order", "beta then alpha", "next"},
		{"one missing", "only alpha", "next"},
		{"empty", "", "next"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := e.traffic("POST", uri, tt.body).Body.String(); got != tt.want {
				t.Errorf("response = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBodyIsStillReadableByTheNextHandlerAfterMatching(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/keep-body")
	e.set(t, "/set/body~alpha?uri="+uri, "matched")
	var seen string
	handler := Middleware.MiddlewareHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = readAll(t, r.Body)
	}))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, e.newRequest("POST", uri, "nothing here"))
	if seen != "nothing here" {
		t.Errorf("the next handler read %q, want the body of the request", seen)
	}
}

func TestSetPayloadForURIAndBodyPaths(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/paths")
	msg := e.set(t, "/set/body/paths/.foo.bar={a},.foo.baz={b}?uri="+uri,
		`{"bar":"{a}","baz":"{b}","message":"{a} is {b}"}`)
	if !strings.Contains(msg, "match [") {
		t.Errorf("message = %q", msg)
	}
	rr := e.traffic("POST", uri, `{"foo":{"bar":"hi","baz":"hello"}}`)
	if rr.Body.String() != `{"bar":"hi","baz":"hello","message":"hi is hello"}` {
		t.Errorf("response = %q, want the values of the paths filled in", rr.Body.String())
	}
	for _, body := range []string{`{"foo":{"bar":"hi"}}`, `{"foo":1}`, `not json`, ``} {
		if got := e.traffic("POST", uri, body).Body.String(); got != "next" {
			t.Errorf("body %q: response = %q, want the answer of the next handler", body, got)
		}
	}
}

func TestSetPayloadForURIAndBodyPathsWithoutNames(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/paths-plain")
	e.set(t, "/set/body/paths/{.user.id}?uri="+uri, "has a user id")
	if got := e.traffic("POST", uri, `{"user":{"id":7}}`).Body.String(); got != "has a user id" {
		t.Errorf("response = %q, want the payload", got)
	}
	if got := e.traffic("POST", uri, `{"user":{}}`).Body.String(); got != "next" {
		t.Errorf("response without the path = %q, want the answer of the next handler", got)
	}
}

func TestBodyKeywordsArePreferredToBodyPaths(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/both-kinds")
	e.set(t, "/set/body/paths/{.id}?uri="+uri, "by path")
	e.set(t, "/set/body~widget?uri="+uri, "by keyword")
	body := `{"id":1,"kind":"widget"}`
	for i := 0; i < 32; i++ {
		if got := e.traffic("POST", uri, body).Body.String(); got != "by keyword" {
			t.Fatalf("request %d: response = %q, want the payload that is matched by keywords", i, got)
		}
	}
}

func TestStreamPayloadForURI(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/feed")
	msg := e.set(t, "/set/stream/count=3/delay=1ms?uri="+uri, `[{"n":1},{"n":2}]`)
	if !strings.Contains(msg, "Stream Payload set") || !strings.Contains(msg, "count: [3]") {
		t.Errorf("message = %q", msg)
	}
	rr := e.get(uri)
	if rr.Body.String() != `{"n":1}{"n":2}{"n":1}` {
		t.Errorf("stream = %q, want the array repeated to make 3 items", rr.Body.String())
	}
	if got := header(rr, constants.HeaderGotoPayloadCount); got != "3" {
		t.Errorf("%s = %q, want 3", constants.HeaderGotoPayloadCount, got)
	}
	if !rr.Flushed {
		t.Errorf("the stream was not flushed")
	}
}

func TestStreamPayloadAsDefault(t *testing.T) {
	e := newTestEnv(t)
	e.set(t, "/set/stream/count=2/delay=1ms", `[{"a":1}]`)
	if got := e.get("/anything").Body.String(); got != `{"a":1}{"a":1}` {
		t.Errorf("stream = %q, want the item twice", got)
	}
}

func TestStreamPayloadForHeader(t *testing.T) {
	e := newTestEnv(t)
	e.set(t, "/set/stream/count=2/delay=1ms/header/x-feed", `[{"a":1},{"a":2}]`)
	if got := e.get("/anything", "X-Feed", "on").Body.String(); got != `{"a":1}{"a":2}` {
		t.Errorf("stream = %q, want both items", got)
	}
	if got := e.get("/anything").Body.String(); got != "next" {
		t.Errorf("response without the header = %q, want the answer of the next handler", got)
	}
}

func TestStreamPayloadForURIAndHeader(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/feed-header")
	e.set(t, "/set/stream/count=2/delay=1ms/header/x-feed?uri="+uri, `[{"a":1}]`)
	if got := e.get(uri, "X-Feed", "on").Body.String(); got != `{"a":1}{"a":1}` {
		t.Errorf("stream = %q, want the item twice", got)
	}
	if got := e.get(uri).Body.String(); got != "next" {
		t.Errorf("response without the header = %q, want the answer of the next handler", got)
	}
}

func TestStreamPayloadIsRemovedByAnEmptyPayload(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/feed-removed")
	e.set(t, "/set/stream/count=2/delay=1ms?uri="+uri, `[{"a":1}]`)
	e.set(t, "/set/stream/count=2/delay=1ms?uri="+uri, "")
	if got := e.get(uri).Body.String(); got != "next" {
		t.Errorf("response = %q, want the answer of the next handler", got)
	}
}

func TestPayloadForGRPCIsKeptApartFromHTTP(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/pkg.Service/Method")
	e.set(t, "/set/grpc?uri="+uri, "for grpc")
	e.set(t, "/set/grpc/default", "grpc default")
	e.set(t, "/set/grpc/header/x-env=prod", "grpc header")
	e.set(t, "/set/grpc/query/mode=fast", "grpc query")
	if got := e.get(uri).Body.String(); got != "next" {
		t.Errorf("an HTTP request got %q, want it to ignore payloads for gRPC", got)
	}
	port := PayloadManager.GetPortResponse(e.port)
	if !port.GetGRPCPayloads().HasAnyPayload() || port.HTTPPayloads.HasAnyPayload() {
		t.Fatalf("gRPC payloads: %v, HTTP payloads: %v", port.GetGRPCPayloads().HasAnyPayload(), port.HTTPPayloads.HasAnyPayload())
	}
	if _, rp, _, found := PayloadManager.GetResponsePayload(e.port, true, uri, nil, nil, nil); !found || string(rp.Payload) != "for grpc" {
		t.Errorf("payload for the URI = %v, found %v", rp, found)
	}
	if _, rp, _, found := PayloadManager.GetResponsePayload(e.port, true, "/other", nil, nil, nil); !found || string(rp.Payload) != "grpc default" {
		t.Errorf("payload for another URI = %v, found %v", rp, found)
	}
	if _, rp, _, found := PayloadManager.GetResponsePayload(e.port, true, "/other", http.Header{"X-Env": {"prod"}}, nil, nil); !found || string(rp.Payload) != "grpc header" {
		t.Errorf("payload for the header = %v, found %v", rp, found)
	}
	if _, rp, _, found := PayloadManager.GetResponsePayload(e.port, true, "/other", nil, map[string][]string{"mode": {"fast"}}, nil); !found || string(rp.Payload) != "grpc query" {
		t.Errorf("payload for the query = %v, found %v", rp, found)
	}
}

func TestSetPayloadWithMatches(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/matches")
	body := `{"payload":{"answer":"matched"},"matches":[{"uriPrefix":"` + uri + `","headers":{"X-Env":"prod"}}]}`
	msg := e.set(t, "/set/matches", body)
	if !strings.Contains(msg, "Response Payload set") || !strings.Contains(msg, "matched") {
		t.Errorf("message = %q, want it to show the payload that was set", msg)
	}
	rr := e.get(uri, "X-Env", "prod")
	if got := decodeJSON(t, rr.Body.Bytes()); got["answer"] != "matched" {
		t.Errorf("response = %q, want the payload", rr.Body.String())
	}
	if got := header(rr, constants.HeaderContentType); got != constants.ContentTypeJSON {
		t.Errorf("content type = %q, want JSON", got)
	}
	if got := e.get(uri, "X-Env", "dev").Body.String(); got != "next" {
		t.Errorf("response for another value = %q, want the answer of the next handler", got)
	}
	if got := e.get(uri).Body.String(); got != "next" {
		t.Errorf("response without the header = %q, want the answer of the next handler", got)
	}
}

func TestSetPayloadWithMatchesAcceptsAList(t *testing.T) {
	e := newTestEnv(t)
	uriA, uriB := e.uri("/list-a"), e.uri("/list-b")
	body := `[{"payload":{"n":"a"},"matches":[{"uriPrefix":"` + uriA + `"}]},{"payload":{"n":"b"},"matches":[{"uriPrefix":"` + uriB + `"}]}]`
	msg := e.set(t, "/set/matches", body)
	if !strings.Contains(msg, uriA) || !strings.Contains(msg, uriB) {
		t.Errorf("message = %q, want it to show every payload that was set", msg)
	}
	if got := decodeJSON(t, e.get(uriA).Body.Bytes()); got["n"] != "a" {
		t.Errorf("response for A = %v", got)
	}
	if got := decodeJSON(t, e.get(uriB).Body.Bytes()); got["n"] != "b" {
		t.Errorf("response for B = %v", got)
	}
}

func TestSetPayloadWithMatchesReportsErrors(t *testing.T) {
	e := newTestEnv(t)
	tests := []struct{ name, body, want string }{
		{"not JSON", "not json", "Failed to parse Response Payload"},
		{"empty body", "", "Failed to parse Response Payload"},
		{"empty list", "[]", "Failed to parse Response Payload"},
		{"no matches", `{"payload":{"a":1}}`, "Invalid Response Payload: Matches required"},
		{"no payload", `{"matches":[{"uriPrefix":"/x"}]}`, "Invalid Response Payload: Payload required"},
		{"match without a URI", `{"payload":{"a":1},"matches":[{"headers":{"a":"b"}}]}`, "Invalid Response Payload: URI match is required"},
		{"list with an invalid item", `[{"payload":{"a":1},"matches":[{"uriPrefix":"/x"}]},{"matches":[]}]`, "Invalid Response Payload"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := e.call("POST", "/server/response/payload/set/matches", tt.body)
			if !strings.Contains(rr.Body.String(), tt.want) {
				t.Errorf("answer = %q, want it to contain %q", rr.Body.String(), tt.want)
			}
		})
	}
}

func TestSetPayloadWithMatchesForGRPC(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/rpc.Svc/Call")
	e.set(t, "/set/grpc/matches", `{"payload":{"ok":true},"matches":[{"uriPrefix":"`+uri+`"}]}`)
	if _, rp, _, found := PayloadManager.GetResponsePayload(e.port, true, uri, nil, nil, nil); !found || rp == nil {
		t.Errorf("the payload is not found for RPC")
	}
	if _, _, _, found := PayloadManager.GetResponsePayload(e.port, false, uri, nil, nil, nil); found {
		t.Errorf("the payload for RPC is found for HTTP")
	}
}

func TestSetPayloadTransform(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/transform")
	transforms := `[{"mappings":[{"source":"a","target":"b","mode":"replace"}],"payload":{"b":"","c":"fixed"}}]`
	msg := e.set(t, "/transform?uri="+uri, transforms)
	if !strings.Contains(msg, "transform paths set for URI ["+uri+"]") {
		t.Errorf("message = %q", msg)
	}
	port := PayloadManager.GetPortResponse(e.port)
	_, rp, _, found := port.HTTPPayloads.GetMatchingResponsePayload(uri, nil, nil, nil)
	if !found || len(rp.Transforms) != 1 || rp.ContentType != constants.ContentTypeJSON {
		t.Fatalf("transform payload = %+v, found %v", rp, found)
	}
	if rp.Delay != nil {
		t.Errorf("delay = %v, want none", rp.Delay)
	}
	// With a delay and a content type
	e.set(t, "/transform/delay=5ms?uri="+uri, transforms, constants.HeaderResponseContentType, "application/yaml")
	_, rp, _, _ = port.HTTPPayloads.GetMatchingResponsePayload(uri, nil, nil, nil)
	if rp.ContentType != "application/yaml" || rp.Delay == nil || rp.Delay.Max.Duration.String() != "5ms" {
		t.Errorf("transform payload = %+v, want the content type and the delay that were given", rp)
	}
}

func TestSetPayloadTransformForGRPC(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/rpc.Svc/Transform")
	e.set(t, "/grpc/transform?uri="+uri, `[{"mappings":[{"source":"a"}]}]`)
	if _, rp, _, found := PayloadManager.GetResponsePayload(e.port, true, uri, nil, nil, nil); !found || len(rp.Transforms) != 1 {
		t.Errorf("transform for RPC = %+v, found %v", rp, found)
	}
	if _, _, _, found := PayloadManager.GetResponsePayload(e.port, false, uri, nil, nil, nil); found {
		t.Errorf("a transform for RPC is found for HTTP")
	}
}

func TestSetPayloadTransformReportsErrors(t *testing.T) {
	e := newTestEnv(t)
	tests := []struct{ name, target, body, want string }{
		{"not JSON", "/transform?uri=/x", "not json", "Invalid transformations"},
		{"null", "/transform?uri=/x", "null", "Invalid transformation. Missing URI or payload."},
		{"no URI", "/transform?uri=", `[{"mappings":[]}]`, "Invalid transformation. Missing URI or payload."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := e.call("POST", "/server/response/payload"+tt.target, tt.body)
			if !strings.Contains(rr.Body.String(), tt.want) {
				t.Errorf("answer = %q, want it to contain %q", rr.Body.String(), tt.want)
			}
		})
	}
	if PayloadManager.GetPortResponse(e.port).HTTPPayloads.HasAnyPayload() {
		t.Errorf("a payload was set though the transform was refused")
	}
}

func TestClearResponsePayload(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/to-clear")
	e.set(t, "/set/default", "default")
	e.set(t, "/set?uri="+uri, "uri")
	e.set(t, "/set/header/x-env=prod", "header")
	e.set(t, "/set/query/mode=fast", "query")
	e.set(t, "/set/grpc/default", "grpc default")
	msg := e.configure(t, "POST", "/server/response/payload/clear", "")
	if !strings.Contains(msg, "Response Payload Cleared") || !strings.Contains(msg, strconv.Itoa(e.port)) {
		t.Errorf("message = %q", msg)
	}
	for _, target := range []string{uri, "/x?mode=fast", "/x"} {
		if got := e.get(target, "X-Env", "prod").Body.String(); got != "next" {
			t.Errorf("GET %s after clearing = %q, want the answer of the next handler", target, got)
		}
	}
	if PayloadManager.GetPortResponse(e.port).GetGRPCPayloads().HasAnyPayload() {
		t.Errorf("payloads for gRPC remain after clearing")
	}
}

func TestClearResponsePayloadOnlyClearsThePortOfTheRequest(t *testing.T) {
	e1, e2 := newTestEnv(t), newTestEnv(t)
	e1.set(t, "/set/default", "one")
	e2.set(t, "/set/default", "two")
	e1.configure(t, "POST", "/server/response/payload/clear", "")
	if got := e1.get("/x").Body.String(); got != "next" {
		t.Errorf("port 1 answers %q after it was cleared", got)
	}
	if got := e2.get("/x").Body.String(); got != "two" {
		t.Errorf("port 2 answers %q, want its payload to be kept", got)
	}
}

func TestPayloadsAreKeptPerPort(t *testing.T) {
	e1, e2 := newTestEnv(t), newTestEnv(t)
	e1.set(t, "/set/default", "one")
	if got := e2.get("/x").Body.String(); got != "next" {
		t.Errorf("port 2 answers %q for a payload of port 1", got)
	}
	if got := e1.get("/x").Body.String(); got != "one" {
		t.Errorf("port 1 answers %q, want its payload", got)
	}
}

func TestGetResponsePayloads(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/listed")
	e.set(t, "/set/default", "the default", constants.HeaderResponseContentType, "text/plain")
	e.set(t, "/set?uri="+uri, "for the uri")
	e.set(t, "/set/header/x-env=prod", "for the header")
	e.set(t, "/set/grpc/default", "for grpc")
	rr := e.call("GET", "/server/response/payload", "")
	if rr.Code != http.StatusOK || !strings.HasPrefix(header(rr, constants.HeaderContentType), constants.ContentTypeJSON) {
		t.Fatalf("response = %d, content type %q", rr.Code, header(rr, constants.HeaderContentType))
	}
	listed := decodeJSON(t, rr.Body.Bytes())
	if int(listed["Port"].(float64)) != e.port {
		t.Errorf("port = %v, want %d", listed["Port"], e.port)
	}
	httpPayloads := listed["http"].(map[string]any)
	if d := httpPayloads["defaultPayload"].(map[string]any); d["payload"] != "the default" || d["contentType"] != "text/plain" {
		t.Errorf("default payload = %v", d)
	}
	if byURI := httpPayloads["payloadByURIs"].(map[string]any); byURI[uri] == nil {
		t.Errorf("payloads by URI = %v, want one for %s", byURI, uri)
	}
	if byHeader := httpPayloads["responsePayloadByHeaders"].(map[string]any); byHeader["x-env"] == nil {
		t.Errorf("payloads by header = %v, want one for x-env", byHeader)
	}
	rpcPayloads := listed["rpc"].(map[string]any)
	if d := rpcPayloads["defaultPayload"].(map[string]any); d["payload"] != "for grpc" {
		t.Errorf("rpc default payload = %v", d)
	}
}

func TestGetResponsePayloadsOfAFreshPort(t *testing.T) {
	e := newTestEnv(t)
	listed := decodeJSON(t, e.call("GET", "/server/response/payload", "").Body.Bytes())
	httpPayloads := listed["http"].(map[string]any)
	if httpPayloads["defaultPayload"] != nil || len(httpPayloads["payloadByURIs"].(map[string]any)) != 0 {
		t.Errorf("a port without payloads lists %v", httpPayloads)
	}
}

func TestRespondWithPayloadOfTheRequestedSize(t *testing.T) {
	tests := []struct {
		size string
		want int
	}{
		{"1", 1},
		{"10", 10},
		{"100", 100},
		{"1K", 1000},
		{"2KB", 2000},
		{"0", 100},
		{"junk", 100},
	}
	e := newTestEnv(t)
	for _, tt := range tests {
		for _, method := range []string{"GET", "PUT", "POST"} {
			rr := e.call(method, "/server/response/payload/"+tt.size, "")
			if rr.Code != http.StatusOK || rr.Body.Len() != tt.want {
				t.Errorf("%s /payload/%s = %d with %d bytes, want 200 with %d bytes", method, tt.size, rr.Code, rr.Body.Len(), tt.want)
				continue
			}
			res := rr.Result()
			if got := res.Header.Get(constants.HeaderGotoPayloadLength); got != strconv.Itoa(tt.want) {
				t.Errorf("%s /payload/%s: %s = %q, want %d", method, tt.size, constants.HeaderGotoPayloadLength, got, tt.want)
			}
			if got := res.Header.Get(constants.HeaderContentLength); got != strconv.Itoa(tt.want) {
				t.Errorf("%s /payload/%s: %s = %q, want %d", method, tt.size, constants.HeaderContentLength, got, tt.want)
			}
			if got := res.Header.Get(constants.HeaderContentType); got != "plain/text" {
				t.Errorf("%s /payload/%s: content type = %q", method, tt.size, got)
			}
		}
	}
}

func TestRespondWithPayloadUsesRandomCharactersOnly(t *testing.T) {
	e := newTestEnv(t)
	body := e.call("GET", "/server/response/payload/500", "").Body.String()
	for i, c := range body {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			t.Fatalf("character %d is %q, want a letter or a digit", i, c)
		}
	}
	other := e.call("GET", "/server/response/payload/500", "").Body.String()
	if body == other {
		t.Errorf("two responses of 500 random characters are the same")
	}
}

func TestPayloadAPIRoutesAcceptOnlyTheirMethods(t *testing.T) {
	e := newTestEnv(t)
	tests := []struct{ method, target string }{
		{"GET", "/server/response/payload/set/default"},
		{"PUT", "/server/response/payload/set/default"},
		{"DELETE", "/server/response/payload/clear"},
		{"POST", "/server/response/payload"},
		{"GET", "/server/response/payload/set/matches"},
		{"GET", "/server/response/payload/transform/delay=1s?uri=/x"},
		{"DELETE", "/server/response/payload/10"},
	}
	for _, tt := range tests {
		rr := e.call(tt.method, tt.target, "")
		if rr.Code != http.StatusMethodNotAllowed && rr.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 405 or 404", tt.method, tt.target, rr.Code)
		}
	}
	if PayloadManager.GetPortResponse(e.port).HTTPPayloads.HasAnyPayload() {
		t.Errorf("a payload was set by a request that is not allowed")
	}
}

func TestGetOfAWriteRouteIsTakenAsAPayloadSize(t *testing.T) {
	// /payload/{size} takes any one segment, so a GET of /clear is a request for a payload that nobody can size.
	e := newTestEnv(t)
	e.set(t, "/set/default", "default")
	rr := e.call("GET", "/server/response/payload/clear", "")
	if rr.Code != http.StatusOK || rr.Body.Len() != 100 {
		t.Errorf("GET /clear = %d with %d bytes, want 200 with the 100 bytes of a payload of no size", rr.Code, rr.Body.Len())
	}
	if got := e.get("/x").Body.String(); got != "default" {
		t.Errorf("response = %q, want the payloads to be kept by a GET", got)
	}
}

func TestSetPayloadReportsInvalidBodyMatches(t *testing.T) {
	e := newTestEnv(t)
	uri := e.uri("/bad-regex")
	rr := e.call("POST", "/server/response/payload/set/body~(unclosed?uri="+uri, "never served")
	if !strings.Contains(rr.Body.String(), "Failed to set payload") {
		t.Errorf("answer = %q, want it to report the failure", rr.Body.String())
	}
	if got := e.traffic("POST", uri, "(unclosed").Body.String(); got != "next" {
		t.Errorf("response = %q, want the invalid match to leave no payload", got)
	}
}
