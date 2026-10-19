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

package header

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"goto/pkg/global"
	"goto/pkg/server/intercept"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
	stubOnce  sync.Once
	routeOnce sync.Once
	// api serves the admin routes, mounted where the server mounts them: under /server/response.
	api *mux.Router
	// Headers are kept per port, so every test uses a port of its own.
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
		api = mux.NewRouter()
		Middleware.SetRoutes(api.PathPrefix("/server/response").Subrouter())
	})
}

type testEnv struct {
	port int
	// handler is the middleware, in front of a handler that answers 200 "next".
	handler http.Handler
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	setupRoutes()
	e := &testEnv{port: int(42000 + portSeq.Add(1))}
	e.handler = Middleware.MiddlewareHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "next")
	}))
	t.Cleanup(func() {
		port := strconv.Itoa(e.port)
		headersLock.Lock()
		// The headers that are removed are also excluded for the whole server, which the test must not leave behind.
		for h := range responseHeadersToRemoveByPort[port] {
			delete(util.ExcludedHeaders, h)
		}
		delete(responseHeadersToRemoveByPort, port)
		delete(responseHeadersToAddByPort, port)
		headersLock.Unlock()
	})
	return e
}

func (e *testEnv) newRequest(method, target string, headers ...string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Add(headers[i], headers[i+1])
	}
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, e.port))
	_, req, _ = util.WithRequestStore(req, nil)
	return req
}

func (e *testEnv) serveAPI(method, target string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	api.ServeHTTP(rr, e.newRequest(method, target))
	return rr
}

// call expects the admin API to answer with the given status.
func (e *testEnv) call(t *testing.T, method, target string, status int) string {
	t.Helper()
	rr := e.serveAPI(method, target)
	if rr.Code != status {
		t.Fatalf("%s %s = %d %q, want %d", method, target, rr.Code, rr.Body.String(), status)
	}
	return rr.Body.String()
}

// serveTraffic serves a request through the middleware, the way the server does: with the writer that holds the
// response until the middlewares are done, since the middleware adds its headers after the handler wrote.
func (e *testEnv) serveTraffic(target string, headers ...string) *http.Response {
	return e.serveRequest(e.newRequest("GET", target, headers...))
}

func (e *testEnv) serveRequest(req *http.Request) *http.Response {
	rr := httptest.NewRecorder()
	w, irw := intercept.WithIntercept(req, rr)
	e.handler.ServeHTTP(w, req)
	irw.Proceed()
	return rr.Result()
}

// listed returns the configured headers that GET /headers reports: the added ones as "name:value", and the removed
// ones as "-name". The report is made of tokens like +[name:value] and -[name], whose order is not defined.
func (e *testEnv) listed(t *testing.T) (added, removed []string) {
	t.Helper()
	body := e.call(t, "GET", "/server/response/headers", http.StatusOK)
	for _, tok := range strings.Fields(body) {
		switch {
		case strings.HasPrefix(tok, "+[") && strings.HasSuffix(tok, "]"):
			added = append(added, tok[2:len(tok)-1])
		case strings.HasPrefix(tok, "-[") && strings.HasSuffix(tok, "]"):
			removed = append(removed, tok[2:len(tok)-1])
		default:
			t.Errorf("GET /headers reported %q, which is neither +[name:value] nor -[name]", tok)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return
}

// headerValues looks a header up by name, whatever the case of the key is. The server writes the headers that echo the
// request with the keys that it builds, e.g. Request-URI, and a lookup through http.Header would not find those.
func headerValues(h http.Header, name string) []string {
	var values []string
	for k, v := range h {
		if strings.EqualFold(k, name) {
			values = append(values, v...)
		}
	}
	return values
}

func headerValue(h http.Header, name string) string {
	if values := headerValues(h, name); len(values) > 0 {
		return values[0]
	}
	return ""
}

func expectList(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	sort.Strings(want)
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func TestAddResponseHeader(t *testing.T) {
	e := newTestEnv(t)
	msg := e.call(t, "POST", "/server/response/headers/add/x-test-a=1", http.StatusOK)
	if !strings.Contains(msg, "x-test-a : 1") || !strings.Contains(msg, "add header") {
		t.Errorf("message = %q, want it to name the header and its value", msg)
	}
	added, removed := e.listed(t)
	expectList(t, "added headers", added, "x-test-a:1")
	expectList(t, "removed headers", removed)
}

func TestAddResponseHeaderAcceptsPutAndKeepsAllValues(t *testing.T) {
	e := newTestEnv(t)
	e.call(t, "PUT", "/server/response/headers/add/x-test-b=1", http.StatusOK)
	e.call(t, "POST", "/server/response/headers/add/x-test-b=2", http.StatusOK)
	e.call(t, "POST", "/server/response/headers/add/x-test-c=3", http.StatusOK)
	added, _ := e.listed(t)
	expectList(t, "added headers", added, "x-test-b:1", "x-test-b:2", "x-test-c:3")
}

func TestHeaderRoutesRefuseOtherMethods(t *testing.T) {
	e := newTestEnv(t)
	for _, tc := range []struct{ method, target string }{
		{"GET", "/server/response/headers/add/x-test-d=1"},
		{"DELETE", "/server/response/headers/add/x-test-d=1"},
		{"GET", "/server/response/headers/remove/x-test-d"},
		{"GET", "/server/response/headers/clear"},
		{"POST", "/server/response/headers"},
		{"PUT", "/server/response/headers"},
	} {
		if rr := e.serveAPI(tc.method, tc.target); rr.Code != http.StatusMethodNotAllowed && rr.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want it to be refused", tc.method, tc.target, rr.Code)
		}
	}
	added, removed := e.listed(t)
	expectList(t, "added headers", added)
	expectList(t, "removed headers", removed)
}

func TestAddResponseHeaderWithoutValueIsNotRouted(t *testing.T) {
	e := newTestEnv(t)
	for _, target := range []string{"/server/response/headers/add/x-test-e", "/server/response/headers/add/x-test-e=", "/server/response/headers/add/=1"} {
		if rr := e.serveAPI("POST", target); rr.Code == http.StatusOK {
			t.Errorf("POST %s = 200, want it to be refused", target)
		}
	}
	added, _ := e.listed(t)
	expectList(t, "added headers", added)
}

func TestHeaderHandlersRefuseMissingHeaderName(t *testing.T) {
	e := newTestEnv(t)
	for name, handler := range map[string]http.HandlerFunc{"add": addResponseHeader, "remove": removeResponseHeader} {
		rr := httptest.NewRecorder()
		handler(rr, e.newRequest("POST", "/headers/"+name))
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Invalid header") {
			t.Errorf("%s without a header = %d %q, want 400 Invalid header", name, rr.Code, rr.Body.String())
		}
	}
	added, removed := e.listed(t)
	expectList(t, "added headers", added)
	expectList(t, "removed headers", removed)
}

func TestRemoveResponseHeader(t *testing.T) {
	e := newTestEnv(t)
	msg := e.call(t, "POST", "/server/response/headers/remove/X-Test-F", http.StatusOK)
	if !strings.Contains(msg, "remove header [X-Test-F]") {
		t.Errorf("message = %q, want it to name the header", msg)
	}
	e.call(t, "PUT", "/server/response/headers/remove/x-test-g", http.StatusOK)
	_, removed := e.listed(t)
	// Removed headers are kept in lower case, since request headers are matched case-insensitively.
	expectList(t, "removed headers", removed, "x-test-f", "x-test-g")
	if !util.ExcludedHeaders["x-test-f"] || !util.ExcludedHeaders["x-test-g"] {
		t.Errorf("removed headers are not excluded for the server: %v", util.ExcludedHeaders)
	}
}

func TestClearResponseHeaders(t *testing.T) {
	e := newTestEnv(t)
	e.call(t, "POST", "/server/response/headers/add/x-test-h=1", http.StatusOK)
	e.call(t, "POST", "/server/response/headers/remove/x-test-i", http.StatusOK)
	msg := e.call(t, "POST", "/server/response/headers/clear", http.StatusOK)
	if !strings.Contains(msg, "cleared") {
		t.Errorf("message = %q, want it to say that headers are cleared", msg)
	}
	added, removed := e.listed(t)
	expectList(t, "added headers", added)
	expectList(t, "removed headers", removed)
	resp := e.serveTraffic("/traffic", "X-Test-I", "i")
	if got := resp.Header.Get("X-Test-H"); got != "" {
		t.Errorf("X-Test-H = %q after the headers were cleared, want none", got)
	}
	if got := headerValue(resp.Header, "Request-X-Test-I"); got != "i" {
		t.Errorf("Request-X-Test-I = %q after the headers were cleared, want the request header to be echoed again", got)
	}
	// Clearing again, and clearing a port that was never configured, work too.
	e.call(t, "PUT", "/server/response/headers/clear", http.StatusOK)
	newTestEnv(t).call(t, "POST", "/server/response/headers/clear", http.StatusOK)
}

func TestGetResponseHeadersWithoutHeaders(t *testing.T) {
	e := newTestEnv(t)
	rr := e.serveAPI("GET", "/server/response/headers")
	if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != "" {
		t.Errorf("GET /headers = %d %q, want 200 with an empty report", rr.Code, rr.Body.String())
	}
	if rr := e.serveAPI("GET", "/server/response/headers/"); rr.Code != http.StatusOK {
		t.Errorf("GET /headers/ = %d, want the trailing slash to be accepted", rr.Code)
	}
}

func TestGetResponseHeadersAddsConfiguredHeadersToItsOwnResponse(t *testing.T) {
	e := newTestEnv(t)
	e.call(t, "POST", "/server/response/headers/add/x-test-j=1", http.StatusOK)
	rr := e.serveAPI("GET", "/server/response/headers")
	if got := rr.Header().Get("X-Test-J"); got != "1" {
		t.Errorf("X-Test-J on the report = %q, want 1", got)
	}
}

func TestHeadersAreKeptPerPort(t *testing.T) {
	a, b := newTestEnv(t), newTestEnv(t)
	a.call(t, "POST", "/server/response/headers/add/x-test-k=a", http.StatusOK)
	a.call(t, "POST", "/server/response/headers/remove/x-test-l", http.StatusOK)
	b.call(t, "POST", "/server/response/headers/add/x-test-k=b", http.StatusOK)
	addedA, removedA := a.listed(t)
	addedB, removedB := b.listed(t)
	expectList(t, "added headers of A", addedA, "x-test-k:a")
	expectList(t, "removed headers of A", removedA, "x-test-l")
	expectList(t, "added headers of B", addedB, "x-test-k:b")
	expectList(t, "removed headers of B", removedB)
	if got := a.serveTraffic("/traffic").Header.Values("X-Test-K"); !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("X-Test-K for A = %v, want [a]", got)
	}
	if got := headerValue(b.serveTraffic("/traffic", "X-Test-L", "l").Header, "Request-X-Test-L"); got != "l" {
		t.Errorf("Request-X-Test-L for B = %q, want the header that A removed to be echoed for B", got)
	}
	b.call(t, "POST", "/server/response/headers/clear", http.StatusOK)
	if added, _ := a.listed(t); len(added) != 1 {
		t.Errorf("added headers of A = %v after B was cleared, want them to stay", added)
	}
}

func TestMiddlewareAddsConfiguredHeaders(t *testing.T) {
	e := newTestEnv(t)
	e.call(t, "POST", "/server/response/headers/add/x-Test-Mixed=m1", http.StatusOK)
	e.call(t, "POST", "/server/response/headers/add/x-test-mixed=m2", http.StatusOK)
	e.call(t, "POST", "/server/response/headers/add/x-test-m=1", http.StatusOK)
	resp := e.serveTraffic("/traffic")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Values("X-Test-M"); !reflect.DeepEqual(got, []string{"1"}) {
		t.Errorf("X-Test-M = %v, want [1]", got)
	}
	got := resp.Header.Values("X-Test-Mixed")
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"m1", "m2"}) {
		t.Errorf("X-Test-Mixed = %v, want [m1 m2]: header names are not case-sensitive", got)
	}
}

func TestMiddlewareEchoesRequestWithPrefix(t *testing.T) {
	e := newTestEnv(t)
	resp := e.serveTraffic("/traffic?q=1", "X-Test-N", "n1")
	if got := headerValue(resp.Header, "Request-X-Test-N"); got != "n1" {
		t.Errorf("Request-X-Test-N = %q, want n1", got)
	}
	if got := headerValue(resp.Header, "Request-Method"); got != "GET" {
		t.Errorf("Request-Method = %q, want GET", got)
	}
	if got := headerValue(resp.Header, "Request-URI"); got != "/traffic?q=1" {
		t.Errorf("Request-URI = %q, want /traffic?q=1", got)
	}
	if got := headerValue(resp.Header, "Request-Host"); got == "" {
		t.Errorf("Request-Host is empty, want the host of the request")
	}
}

func TestMiddlewareDoesNotEchoRemovedHeaders(t *testing.T) {
	e := newTestEnv(t)
	e.call(t, "POST", "/server/response/headers/remove/X-Test-O", http.StatusOK)
	resp := e.serveTraffic("/traffic", "x-test-o", "o", "X-Test-P", "p")
	if got := headerValue(resp.Header, "Request-X-Test-O"); got != "" {
		t.Errorf("Request-X-Test-O = %q, want the removed header not to be echoed", got)
	}
	if got := headerValue(resp.Header, "Request-X-Test-P"); got != "p" {
		t.Errorf("Request-X-Test-P = %q, want the other headers to be echoed", got)
	}
}

func TestMiddlewareKeepsBodyOfNextHandler(t *testing.T) {
	e := newTestEnv(t)
	e.call(t, "POST", "/server/response/headers/add/x-test-q=1", http.StatusOK)
	resp := e.serveTraffic("/traffic")
	body, err := io.ReadAll(resp.Body)
	if err != nil || string(body) != "next" {
		t.Errorf("body = %q (%v), want the response of the next handler", body, err)
	}
}

func TestMiddlewareSkipsRequestsThatTakeNoResponseHeaders(t *testing.T) {
	e := newTestEnv(t)
	e.call(t, "POST", "/server/response/headers/add/x-test-r=1", http.StatusOK)
	cases := map[string]func(*http.Request){
		"admin request": func(r *http.Request) { util.GetRequestStore(r).IsAdminRequest = true },
		"tunnel":        func(r *http.Request) { util.GetRequestStore(r).IsTunnelRequest = true },
		"gRPC":          func(r *http.Request) { util.GetRequestStore(r).IsGRPC = true },
		"headers sent":  func(r *http.Request) { util.GetRequestStore(r).IsHeadersSent = true },
	}
	for name, mark := range cases {
		req := e.newRequest("GET", "/traffic")
		mark(req)
		resp := e.serveRequest(req)
		if got := resp.Header.Get("X-Test-R"); got != "" {
			t.Errorf("%s: X-Test-R = %q, want no headers added", name, got)
		}
		if got := headerValue(resp.Header, "Request-Method"); got != "" {
			t.Errorf("%s: Request-Method = %q, want the request not to be echoed", name, got)
		}
	}
	// A request that is marked as none of them gets the headers.
	if got := e.serveTraffic("/traffic").Header.Get("X-Test-R"); got != "1" {
		t.Errorf("X-Test-R = %q for a plain request, want 1", got)
	}
}

func TestMiddlewareWithoutNextHandler(t *testing.T) {
	e := newTestEnv(t)
	e.call(t, "POST", "/server/response/headers/add/x-test-s=1", http.StatusOK)
	h := Middleware.MiddlewareHandler(nil)
	req := e.newRequest("GET", "/traffic")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if got := rr.Header().Get("X-Test-S"); got != "1" {
		t.Errorf("X-Test-S = %q, want 1", got)
	}
}

func TestSetResponseHeadersDirectly(t *testing.T) {
	e := newTestEnv(t)
	e.call(t, "POST", "/server/response/headers/add/x-test-t=1", http.StatusOK)
	e.call(t, "POST", "/server/response/headers/remove/x-test-u", http.StatusOK)
	rr := httptest.NewRecorder()
	SetResponseHeaders(rr, e.newRequest("POST", "/traffic", "X-Test-U", "u", "X-Test-V", "v"))
	h := rr.Header()
	if h.Get("X-Test-T") != "1" || headerValue(h, "Request-X-Test-V") != "v" || headerValue(h, "Request-X-Test-U") != "" || headerValue(h, "Request-Method") != "POST" {
		t.Errorf("headers = %v, want X-Test-T and the echoed request without the removed header", h)
	}
}

func TestSetResponseHeadersLeavesHeadersOfTheHandler(t *testing.T) {
	e := newTestEnv(t)
	e.call(t, "POST", "/server/response/headers/add/x-test-w=added", http.StatusOK)
	rr := httptest.NewRecorder()
	rr.Header().Set("X-Test-X", "from-handler")
	SetResponseHeaders(rr, e.newRequest("GET", "/traffic", "X-Test-X", "from-request"))
	if got := rr.Header().Values("X-Test-X"); !reflect.DeepEqual(got, []string{"from-handler"}) {
		t.Errorf("X-Test-X = %v, want the header of the handler to stay as it is", got)
	}
	if got := headerValue(rr.Header(), "Request-X-Test-X"); got != "" {
		t.Errorf("Request-X-Test-X = %q, want no echo of a header that the response has", got)
	}
}

func TestConcurrentAddsKeepEveryHeaderAndValue(t *testing.T) {
	e := newTestEnv(t)
	const writers, sameHeaderWriters = 40, 40
	var reqs []*http.Request
	var targets []string
	for i := 0; i < writers; i++ {
		targets = append(targets, fmt.Sprintf("/server/response/headers/add/x-test-conc-%d=v", i))
	}
	for i := 0; i < sameHeaderWriters; i++ {
		targets = append(targets, fmt.Sprintf("/server/response/headers/add/x-test-same=v%d", i))
	}
	for _, target := range targets {
		reqs = append(reqs, e.newRequest("POST", target))
	}
	traffic := make([]*http.Request, 20)
	for i := range traffic {
		traffic[i] = e.newRequest("GET", "/traffic")
	}
	codes := make([]int, len(reqs))
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range reqs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			rr := httptest.NewRecorder()
			api.ServeHTTP(rr, reqs[i])
			codes[i] = rr.Code
		}(i)
	}
	// Traffic and reports run along with the changes.
	for i := range traffic {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			rr := httptest.NewRecorder()
			e.handler.ServeHTTP(rr, traffic[i])
		}(i)
	}
	close(start)
	wg.Wait()
	for i, code := range codes {
		if code != http.StatusOK {
			t.Errorf("%s = %d, want 200", targets[i], code)
		}
	}
	added, _ := e.listed(t)
	if want := writers + sameHeaderWriters; len(added) != want {
		t.Errorf("reported %d header values, want %d", len(added), want)
	}
	same := e.serveTraffic("/traffic").Header.Values("X-Test-Same")
	if len(same) != sameHeaderWriters {
		t.Errorf("X-Test-Same has %d values, want %d", len(same), sameHeaderWriters)
	}
	seen := map[string]bool{}
	for _, v := range same {
		seen[v] = true
	}
	for i := 0; i < sameHeaderWriters; i++ {
		if !seen[fmt.Sprintf("v%d", i)] {
			t.Errorf("X-Test-Same is missing the value v%d", i)
		}
	}
}

func TestConcurrentRemoves(t *testing.T) {
	e := newTestEnv(t)
	const writers = 30
	var reqs []*http.Request
	for i := 0; i < writers; i++ {
		reqs = append(reqs, e.newRequest("POST", fmt.Sprintf("/server/response/headers/remove/x-test-rm-%d", i)))
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	codes := make([]int, len(reqs))
	for i := range reqs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			rr := httptest.NewRecorder()
			api.ServeHTTP(rr, reqs[i])
			codes[i] = rr.Code
		}(i)
	}
	close(start)
	wg.Wait()
	for i, code := range codes {
		if code != http.StatusOK {
			t.Errorf("remove %d = %d, want 200", i, code)
		}
	}
	_, removed := e.listed(t)
	if len(removed) != writers {
		t.Errorf("reported %d removed headers, want %d", len(removed), writers)
	}
}

func TestMiddlewareIsRegistered(t *testing.T) {
	if Middleware == nil || Middleware.Name != "response.header" || Middleware.SetRoutes == nil || Middleware.MiddlewareHandler == nil {
		t.Errorf("Middleware = %+v, want the response header middleware with routes and a handler", Middleware)
	}
}
