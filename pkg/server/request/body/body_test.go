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

package body

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"goto/pkg/global"
	"goto/pkg/util"
)

const testPort = 8092

var stubOnce sync.Once

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

// setLogFlags sets what is logged of the bodies, and puts back what was there when the test is done.
func setLogFlags(t *testing.T, mini, full, rpc bool) {
	t.Helper()
	m, f, r := global.Flags.LogRequestMiniBody, global.Flags.LogRequestBody, global.Flags.LogRPCRequestBody
	global.Flags.LogRequestMiniBody, global.Flags.LogRequestBody, global.Flags.LogRPCRequestBody = mini, full, rpc
	t.Cleanup(func() {
		global.Flags.LogRequestMiniBody, global.Flags.LogRequestBody, global.Flags.LogRPCRequestBody = m, f, r
	})
}

// newRequest makes a request with a request store, which is what puts a body that can be rewound in the request.
func newRequest(target, body string, mods ...func(*http.Request)) *http.Request {
	stubOnce.Do(stubGlobalFuncs)
	req := httptest.NewRequest("POST", target, strings.NewReader(body))
	for _, mod := range mods {
		mod(req)
	}
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, testPort))
	_, req, _ = util.WithRequestStore(req, nil)
	return req
}

// serve runs the request through the middleware, and gives what the next handler read of the body.
func serve(req *http.Request) (seenByNext string, nextCalled bool) {
	h := Middleware.MiddlewareHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		b, _ := io.ReadAll(r.Body)
		seenByNext = string(b)
	}))
	h.ServeHTTP(httptest.NewRecorder(), req)
	return
}

func TestBodyIsKeptInTheRequestStoreAndGivenToTheNextHandler(t *testing.T) {
	setLogFlags(t, false, false, false)
	for _, body := range []string{"", "x", "hello world", "line 1\nline 2\n", `{"json": [1, 2, 3]}`, strings.Repeat("0123456789", 100000)} {
		req := newRequest("/some/path", body)
		seen, called := serve(req)
		rs := util.GetRequestStore(req)
		if !called || seen != body {
			t.Errorf("body of %d bytes: next called = %t, saw %d bytes, want it all", len(body), called, len(seen))
		}
		if rs.RequestPayload != body || rs.RequestPayloadSize != len(body) {
			t.Errorf("body of %d bytes: payload of %d bytes, size %d, want the body in the store", len(body), len(rs.RequestPayload), rs.RequestPayloadSize)
		}
		if want := []string{fmt.Sprintf("Request Body Length: [%d]", len(body))}; !reflect.DeepEqual(rs.LogMessages, want) {
			t.Errorf("body of %d bytes: log = %v, want %v", len(body), rs.LogMessages, want)
		}
	}
}

func TestSpacesAroundTheBodyAreNotInThePayload(t *testing.T) {
	setLogFlags(t, false, false, false)
	req := newRequest("/some/path", "  spaced out \n")
	seen, _ := serve(req)
	rs := util.GetRequestStore(req)
	if seen != "  spaced out \n" {
		t.Errorf("next saw %q, want the body as it was sent", seen)
	}
	if rs.RequestPayload != "spaced out \n" {
		t.Errorf("payload = %q, want it without the leading and trailing spaces", rs.RequestPayload)
	}
}

func TestAdminRequestsKeepTheirBodyForTheNextHandlerAndNotInTheStore(t *testing.T) {
	setLogFlags(t, true, true, true)
	req := newRequest("/server/something", "admin body")
	seen, called := serve(req)
	rs := util.GetRequestStore(req)
	if !rs.IsAdminRequest {
		t.Fatal("the request is expected to be an admin request")
	}
	if !called || seen != "admin body" {
		t.Errorf("next called = %t, saw %q, want the body", called, seen)
	}
	if rs.RequestPayload != "" || rs.RequestPayloadSize != 0 || len(rs.LogMessages) != 0 {
		t.Errorf("payload = %q, size = %d, log = %v, want nothing of an admin request", rs.RequestPayload, rs.RequestPayloadSize, rs.LogMessages)
	}
}

func TestHTTP2RequestsAreNotRead(t *testing.T) {
	setLogFlags(t, true, true, true)
	req := newRequest("/some/path", "h2 body", func(r *http.Request) {
		r.ProtoMajor, r.ProtoMinor, r.Proto = 2, 0, "HTTP/2.0"
	})
	seen, called := serve(req)
	rs := util.GetRequestStore(req)
	if !called || seen != "h2 body" {
		t.Errorf("next called = %t, saw %q, want the body", called, seen)
	}
	if rs.RequestPayload != "" || rs.RequestPayloadSize != 0 || len(rs.LogMessages) != 0 {
		t.Errorf("payload = %q, size = %d, log = %v, want nothing of an HTTP/2 request", rs.RequestPayload, rs.RequestPayloadSize, rs.LogMessages)
	}
}

func TestBodyLogging(t *testing.T) {
	long := strings.Repeat("a", 60) + strings.Repeat("b", 60)
	for _, tt := range []struct {
		name             string
		mini, full, rpc  bool
		body             string
		mark             func(rs *util.RequestStore)
		wantBodyLogLines []string
	}{
		{"nothing is logged by default", false, false, false, "payload", nil, nil},
		{"full body", false, true, false, "payload", nil, []string{"Request Body: [payload]"}},
		{"newlines are escaped", false, true, false, "a\nb\n", nil, []string{`Request Body: [a\nb\n]`}},
		{"mini body that is short is whole", true, false, false, "short body", nil, []string{"Request Mini Body: [short body]"}},
		{"mini body of exactly 50 is whole", true, false, false, strings.Repeat("c", 50), nil, []string{"Request Mini Body: [" + strings.Repeat("c", 50) + "]"}},
		{"mini body that is long is its ends", true, false, false, long, nil,
			[]string{"Request Mini Body: [" + strings.Repeat("a", 50) + "..." + strings.Repeat("b", 50) + "]"}},
		{"mini wins over full", true, true, false, "payload", nil, []string{"Request Mini Body: [payload]"}},
		{"mini wins over full for long bodies", true, true, false, long, nil,
			[]string{"Request Mini Body: [" + strings.Repeat("a", 50) + "..." + strings.Repeat("b", 50) + "]"}},
		{"rpc flag is for rpc requests only", false, false, true, "payload", nil, nil},
		{"rpc flag with grpc", false, false, true, "payload", func(rs *util.RequestStore) { rs.IsGRPC = true }, []string{"Request Body: [payload]"}},
		{"rpc flag with json rpc", false, false, true, "payload", func(rs *util.RequestStore) { rs.IsJSONRPC = true }, []string{"Request Body: [payload]"}},
		{"rpc flag with mcp", false, false, true, "payload", func(rs *util.RequestStore) { rs.IsMCP = true }, []string{"Request Body: [payload]"}},
		{"rpc kinds without the rpc flag", false, false, false, "payload", func(rs *util.RequestStore) { rs.IsJSONRPC, rs.IsMCP = true, true }, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			setLogFlags(t, tt.mini, tt.full, tt.rpc)
			req := newRequest("/some/path", tt.body)
			if tt.mark != nil {
				tt.mark(util.GetRequestStore(req))
			}
			serve(req)
			want := append([]string{fmt.Sprintf("Request Body Length: [%d]", len(tt.body))}, tt.wantBodyLogLines...)
			if got := util.GetRequestStore(req).LogMessages; !reflect.DeepEqual(got, want) {
				t.Errorf("log = %q, want %q", got, want)
			}
		})
	}
}

func TestMiddlewareWithoutANextHandlerStillReadsTheBody(t *testing.T) {
	setLogFlags(t, false, false, false)
	req := newRequest("/some/path", "no next")
	Middleware.MiddlewareHandler(nil).ServeHTTP(httptest.NewRecorder(), req)
	rs := util.GetRequestStore(req)
	if rs.RequestPayload != "no next" || rs.RequestPayloadSize != 7 {
		t.Errorf("payload = %q, size = %d, want the body", rs.RequestPayload, rs.RequestPayloadSize)
	}
}

func TestWhatTheNextHandlerLeavesOfTheBodyIsDiscarded(t *testing.T) {
	setLogFlags(t, false, false, false)
	req := newRequest("/some/path", "0123456789")
	var first []byte
	h := Middleware.MiddlewareHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		first = make([]byte, 3)
		io.ReadFull(r.Body, first)
	}))
	h.ServeHTTP(httptest.NewRecorder(), req)
	if string(first) != "012" {
		t.Fatalf("next read %q, want the start of the body", first)
	}
	if rest, _ := io.ReadAll(req.Body); len(rest) != 0 {
		t.Errorf("body has %q left, want it to be read to the end", rest)
	}
}

func TestMiddlewareCallsTheNextHandlerOnce(t *testing.T) {
	setLogFlags(t, false, false, false)
	calls := 0
	h := Middleware.MiddlewareHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	h.ServeHTTP(httptest.NewRecorder(), newRequest("/some/path", "x"))
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

func TestMiddlewareHasNoRoutes(t *testing.T) {
	if Middleware.Name != "body" || Middleware.SetRoutes != nil || Middleware.MiddlewareHandler == nil {
		t.Errorf("middleware = %+v, want a middleware named body that has no routes", Middleware)
	}
}

func TestConcurrentRequestsKeepTheirOwnBodies(t *testing.T) {
	setLogFlags(t, false, false, false)
	const requests = 32
	type result struct {
		seen, payload string
		size          int
	}
	results := make([]result, requests)
	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := strings.Repeat(fmt.Sprint(i%10), 100*(i+1))
			req := newRequest("/some/path", body)
			seen, _ := serve(req)
			rs := util.GetRequestStore(req)
			results[i] = result{seen, rs.RequestPayload, rs.RequestPayloadSize}
		}()
	}
	wg.Wait()
	for i, got := range results {
		want := strings.Repeat(fmt.Sprint(i%10), 100*(i+1))
		if got.seen != want || got.payload != want || got.size != len(want) {
			t.Errorf("request %d: next saw %d bytes, payload %d bytes, size %d, want %d bytes of its own", i, len(got.seen), len(got.payload), got.size, len(want))
		}
	}
}
