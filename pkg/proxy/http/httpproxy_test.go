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

package httpproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/server/middleware"
	"goto/pkg/server/response/status"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
	setupOnce sync.Once
	// Proxies, statuses and invocations are kept per port, so every test uses a port of its own.
	portSeq atomic.Int32
)

// setupGlobals stubs the hooks that the server fills in at startup, creates the routers that the server creates at
// startup (the proxy builds URIs on util.MatchRouter), and registers the admin routes, which live on the router of
// /proxy.
func setupGlobals() {
	setupOnce.Do(func() {
		global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
		global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
		global.Funcs.IsListenerPresent = func(int) bool { return false }
		global.Funcs.IsListenerOpen = func(int) bool { return false }
		global.Funcs.GetListenerID = func(int) string { return "" }
		global.Funcs.GetListenerLabel = func(*http.Request) string { return "test" }
		global.Funcs.GetListenerLabelForPort = func(int) string { return "test" }
		util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
		if util.MatchRouter == nil {
			util.CreateRouters(mux.NewRouter())
		}
		Middleware.SetRoutes(mux.NewRouter())
	})
}

// upstream is an upstream endpoint that records the requests that it receives.
type upstream struct {
	*httptest.Server
	count atomic.Int32
	lock  sync.Mutex
	reqs  []seenRequest
}

type seenRequest struct {
	Method  string
	URI     string
	Path    string
	Query   map[string][]string
	Headers http.Header
	Host    string
	Body    string
}

// newUpstream starts an upstream that answers with the given status and body.
func newUpstream(t *testing.T, code int, body string) *upstream {
	t.Helper()
	return newUpstreamFunc(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream-Header", "up")
		w.WriteHeader(code)
		io.WriteString(w, body)
	})
}

func newUpstreamFunc(t *testing.T, h http.HandlerFunc) *upstream {
	t.Helper()
	u := &upstream{}
	u.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		u.lock.Lock()
		u.reqs = append(u.reqs, seenRequest{Method: r.Method, URI: r.RequestURI, Path: r.URL.Path, Query: r.URL.Query(),
			Headers: r.Header.Clone(), Host: r.Host, Body: string(b)})
		u.lock.Unlock()
		u.count.Add(1)
		h(w, r)
	}))
	t.Cleanup(u.Close)
	return u
}

func (u *upstream) hits() int { return int(u.count.Load()) }

func (u *upstream) requests() []seenRequest {
	u.lock.Lock()
	defer u.lock.Unlock()
	return append([]seenRequest{}, u.reqs...)
}

// only returns the one request that the upstream is expected to have received.
func (u *upstream) only(t *testing.T) seenRequest {
	t.Helper()
	reqs := u.requests()
	if len(reqs) != 1 {
		t.Fatalf("upstream received %d requests %+v, want 1", len(reqs), reqs)
	}
	return reqs[0]
}

// env is a proxy on a port of its own, with helpers to configure it the way the admin API does, and to send requests
// through it the way the server does.
type env struct {
	t     *testing.T
	port  int
	proxy *Proxy
}

func newEnv(t *testing.T) *env {
	t.Helper()
	setupGlobals()
	port := int(41000 + portSeq.Add(1))
	e := &env{t: t, port: port, proxy: GetPortProxy(port)}
	t.Cleanup(func() {
		proxyLock.Lock()
		delete(portProxy, port)
		delete(proxyRouters, port)
		proxyLock.Unlock()
		status.TheStatusManager.Clear(port, "", "")
	})
	return e
}

// addTarget adds an enabled target with the given endpoints (ep1, ep2, ...) and one trigger "t1" that sends requests
// with the given URI prefix to all of the endpoints.
func (e *env) addTarget(name string, uriPrefix string, endpointURLs ...string) *Target {
	e.t.Helper()
	return e.addTargetSpec(targetSpec(name, uriPrefix, "", endpointURLs...))
}

// targetSpec builds the JSON of a target. The extra is JSON members that are added to the trigger, like the transform.
func targetSpec(name, uriPrefix, triggerExtra string, endpointURLs ...string) string {
	eps := make([]string, len(endpointURLs))
	names := make([]string, len(endpointURLs))
	for i, u := range endpointURLs {
		eps[i] = fmt.Sprintf(`"ep%d":{"url":%q}`, i+1, u)
		names[i] = fmt.Sprintf(`"ep%d"`, i+1)
	}
	if triggerExtra != "" {
		triggerExtra = "," + triggerExtra
	}
	return fmt.Sprintf(`{"name":%q,"enabled":true,"endpoints":{%s},"triggers":{"t1":{"matchAny":[{"uriPrefix":%q}],"endpoints":[%s]%s}}}`,
		name, strings.Join(eps, ","), uriPrefix, strings.Join(names, ","), triggerExtra)
}

func (e *env) addTargetSpec(spec string) *Target {
	e.t.Helper()
	target, err := parseTarget(strings.NewReader(spec))
	if err != nil {
		e.t.Fatalf("parseTarget(%s): %v", spec, err)
	}
	if err := e.proxy.AddTarget(target); err != nil {
		e.t.Fatalf("AddTarget(%s): %v", spec, err)
	}
	return target
}

func (e *env) newRequest(method, target string, body io.Reader, headers ...string) *http.Request {
	req := httptest.NewRequest(method, target, body)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Add(headers[i], headers[i+1])
	}
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, e.port))
	_, req, _ = util.WithRequestStore(req, nil)
	return req
}

// served is the outcome of a request sent through the proxy: whether the proxy took the request, and the response.
type served struct {
	proxied bool
	rr      *httptest.ResponseRecorder
}

// serve routes the request the way the server does: the proxy takes the request if it matches a target, and the
// request is handled locally (answered with 299 "local") otherwise.
func (e *env) serve(req *http.Request) served {
	rr := httptest.NewRecorder()
	if ok, router := WillProxyHTTP(req); ok && router != nil {
		util.GetRequestStore(req).IsProxy = true
		router.ServeHTTP(rr, req)
		return served{true, rr}
	}
	rr.WriteHeader(299)
	io.WriteString(rr, "local")
	return served{false, rr}
}

func (e *env) get(target string, headers ...string) served {
	return e.serve(e.newRequest("GET", target, nil, headers...))
}

// admin sends a request to the admin API of the proxy, which is on the router of /proxy.
func (e *env) admin(method, target string, body string) *httptest.ResponseRecorder {
	setupGlobals()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	rr := httptest.NewRecorder()
	middleware.RootRouters["/proxy"].ServeHTTP(rr, e.newRequest(method, target, r))
	return rr
}

type upstreamResult struct {
	Status     string              `json:"status"`
	StatusCode int                 `json:"statusCode"`
	Headers    map[string][]string `json:"headers"`
	Payload    string              `json:"payload"`
}

// results parses the default proxy response, which is the upstream responses keyed by target and endpoint.
func results(t *testing.T, rr *httptest.ResponseRecorder) map[string]map[string][]upstreamResult {
	t.Helper()
	out := map[string]map[string][]upstreamResult{}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("proxy response is not the upstream results: %v: %q", err, rr.Body.String())
	}
	return out
}

func expectProxied(t *testing.T, s served, wantCode int) {
	t.Helper()
	if !s.proxied {
		t.Fatalf("request was not proxied (answered %d %q)", s.rr.Code, s.rr.Body.String())
	}
	if s.rr.Code != wantCode {
		t.Fatalf("proxy status = %d, want %d: %s", s.rr.Code, wantCode, s.rr.Body.String())
	}
}

func expectLocal(t *testing.T, s served) {
	t.Helper()
	if s.proxied {
		t.Fatalf("request was proxied (answered %d %q), want it handled locally", s.rr.Code, s.rr.Body.String())
	}
}

// ---------------------------------------------------------------------------------------------------------------
// Forwarding

func TestProxyForwardsRequestToUpstream(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 201, "hello")
	e.addTarget("t", "/", up.URL)
	s := e.get("/foo/bar?x=1", "X-Req", "a")
	expectProxied(t, s, 201)
	got := up.only(t)
	if got.Method != "GET" || got.Path != "/foo/bar" || got.Query["x"][0] != "1" || got.Headers.Get("X-Req") != "a" {
		t.Errorf("upstream saw %+v", got)
	}
	if got.Headers.Get("Goto-Target-Id") == "" {
		t.Errorf("upstream request has no %s header: %v", "Goto-Target-Id", got.Headers)
	}
}

func TestProxyForwardsMethodAndBody(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTarget("t", "/", up.URL)
	expectProxied(t, e.serve(e.newRequest("POST", "/post/here", strings.NewReader(`{"a":1}`), "Content-Type", "application/json")), 200)
	got := up.only(t)
	if got.Method != "POST" || got.Body != `{"a":1}` || got.Headers.Get("Content-Type") != "application/json" {
		t.Errorf("upstream saw %+v", got)
	}
}

func TestProxyEndpointMethodOverridesRequestMethod(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTargetSpec(fmt.Sprintf(`{"name":"t","enabled":true,"endpoints":{"ep":{"url":%q,"method":"PUT"}},"triggers":{"t1":{"matchAny":[{"uriPrefix":"/"}],"endpoints":["ep"]}}}`, up.URL))
	expectProxied(t, e.get("/x"), 200)
	if got := up.only(t); got.Method != "PUT" {
		t.Errorf("upstream method = %s, want PUT", got.Method)
	}
}

func TestProxyEndpointAuthorityBecomesHost(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTargetSpec(fmt.Sprintf(`{"name":"t","enabled":true,"endpoints":{"ep":{"url":%q,"authority":"foo.example.com"}},"triggers":{"t1":{"matchAny":[{"uriPrefix":"/"}],"endpoints":["ep"]}}}`, up.URL))
	expectProxied(t, e.get("/x"), 200)
	if got := up.only(t); got.Host != "foo.example.com" {
		t.Errorf("upstream host = %s, want foo.example.com", got.Host)
	}
}

func TestProxyResponseReportsEachUpstreamResponse(t *testing.T) {
	e := newEnv(t)
	up1 := newUpstream(t, 201, "one")
	up2 := newUpstream(t, 202, "two")
	e.addTarget("t", "/", up1.URL, up2.URL)
	s := e.get("/foo")
	if !s.proxied {
		t.Fatal("request was not proxied")
	}
	res := results(t, s.rr)
	if len(res["t"]) != 2 {
		t.Fatalf("results = %+v, want responses of both endpoints under target t", res)
	}
	if r := res["t"]["ep1"]; len(r) != 1 || r[0].StatusCode != 201 || r[0].Headers["X-Upstream-Header"][0] != "up" {
		t.Errorf("ep1 result = %+v", r)
	}
	if r := res["t"]["ep2"]; len(r) != 1 || r[0].StatusCode != 202 {
		t.Errorf("ep2 result = %+v", r)
	}
	if up1.hits() != 1 || up2.hits() != 1 {
		t.Errorf("upstream hits = %d, %d, want 1 each", up1.hits(), up2.hits())
	}
	if got := s.rr.Header().Get(constants.HeaderViaGoto); !strings.Contains(got, "(Proxy)") {
		t.Errorf("%s = %q, want it to tell that the request was proxied", constants.HeaderViaGoto, got)
	}
	if got := s.rr.Header().Get("Proxy-" + constants.HeaderGotoResponseStatus); got == "" {
		t.Errorf("response has no Proxy-%s header: %v", constants.HeaderGotoResponseStatus, s.rr.Header())
	}
	var statuses map[string]map[string]int
	if err := json.Unmarshal([]byte(s.rr.Header().Get(constants.HeaderGotoProxyUpstreamStatus)), &statuses); err != nil {
		t.Fatalf("%s = %q is not JSON: %v", constants.HeaderGotoProxyUpstreamStatus, s.rr.Header().Get(constants.HeaderGotoProxyUpstreamStatus), err)
	}
	if statuses["t"]["ep1"] != 201 || statuses["t"]["ep2"] != 202 {
		t.Errorf("%s = %v, want the status of each endpoint", constants.HeaderGotoProxyUpstreamStatus, statuses)
	}
}

func TestProxyResponseStatusIsStatusOfTheUpstreamResponse(t *testing.T) {
	for _, code := range []int{200, 201, 404, 500, 503} {
		e := newEnv(t)
		up := newUpstream(t, code, "x")
		e.addTarget("t", "/", up.URL)
		expectProxied(t, e.get("/foo"), code)
	}
}

func TestProxyResponseMapsUpstreamStatus(t *testing.T) {
	e := newEnv(t)
	e.proxy.ProxyResponses = []*ProxyResponse{
		{UpResponseRange: []int{500, 599}, ProxyResponse: 502},
		{UpResponseRange: []int{404}, ProxyResponse: 410},
		{UpResponseRange: nil, ProxyResponse: 418},
	}
	for upCode, want := range map[int]int{200: 200, 404: 410, 500: 502, 503: 502, 499: 499, 600: 600} {
		up := newUpstream(t, upCode, "x")
		name := fmt.Sprintf("t%d", upCode)
		e.addTarget(name, "/"+name, up.URL)
		expectProxied(t, e.get("/"+name), want)
	}
}

func TestProxyTransparentReturnsTheUpstreamResponse(t *testing.T) {
	e := newEnv(t)
	up := newUpstreamFunc(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream-Header", "from-up")
		w.WriteHeader(203)
		io.WriteString(w, "raw upstream body")
	})
	e.addTargetSpec(targetSpec("t", "/", `"trafficConfig":{"transparent":true,"payload":true}`, up.URL))
	s := e.get("/foo")
	expectProxied(t, s, 203)
	if got := strings.TrimSpace(s.rr.Body.String()); got != "raw upstream body" {
		t.Errorf("body = %q, want the upstream body", got)
	}
	if got := s.rr.Header().Get("X-Upstream-Header"); got != "from-up" {
		t.Errorf("X-Upstream-Header = %q, want the upstream header", got)
	}
}

func TestProxyJSONPayloadParsesUpstreamBody(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, `{"a":{"b":1}}`)
	e.addTargetSpec(targetSpec("t", "/", `"trafficConfig":{"jsonPayload":true}`, up.URL))
	s := e.get("/foo")
	expectProxied(t, s, 200)
	var got map[string]map[string]int
	if err := json.Unmarshal(s.rr.Body.Bytes(), &got); err != nil || got["a"]["b"] != 1 {
		t.Errorf("body = %q (%v), want the upstream JSON", s.rr.Body.String(), err)
	}
}

func TestProxyPayloadFlagAddsUpstreamPayloadToResults(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "payload-text")
	e.addTargetSpec(targetSpec("t", "/", `"trafficConfig":{"payload":true}`, up.URL))
	s := e.get("/foo")
	expectProxied(t, s, 200)
	if got := results(t, s.rr)["t"]["ep1"]; len(got) != 1 || got[0].Payload != "payload-text" {
		t.Errorf("results = %+v, want the payload of the upstream response", got)
	}
}

func TestProxyRequestCountAndConcurrentMultiplyUpstreamCalls(t *testing.T) {
	tests := []struct {
		count, concurrent, want int
	}{{1, 1, 1}, {3, 1, 3}, {4, 2, 8}}
	for _, tc := range tests {
		e := newEnv(t)
		up := newUpstream(t, 200, "ok")
		e.addTargetSpec(fmt.Sprintf(`{"name":"t","enabled":true,"endpoints":{"ep":{"url":%q,"requestCount":%d,"concurrent":%d}},"triggers":{"t1":{"matchAny":[{"uriPrefix":"/"}],"endpoints":["ep"]}}}`,
			up.URL, tc.count, tc.concurrent))
		s := e.get("/foo")
		expectProxied(t, s, 200)
		if up.hits() != tc.want {
			t.Errorf("requestCount=%d concurrent=%d: upstream hits = %d, want %d", tc.count, tc.concurrent, up.hits(), tc.want)
		}
		if got := results(t, s.rr)["t"]["ep"]; len(got) != tc.want {
			t.Errorf("requestCount=%d concurrent=%d: %d results, want %d", tc.count, tc.concurrent, len(got), tc.want)
		}
	}
}

func TestProxyRetriesOnConfiguredStatuses(t *testing.T) {
	e := newEnv(t)
	var calls atomic.Int32
	up := newUpstreamFunc(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
	})
	e.addTargetSpec(targetSpec("t", "/", `"trafficConfig":{"retries":3,"retryOn":[503],"retryDelay":{"min":"1ms","max":"1ms"}}`, up.URL))
	expectProxied(t, e.get("/foo"), 200)
	if up.hits() != 3 {
		t.Errorf("upstream hits = %d, want 3 (2 failures, and the retry that succeeded)", up.hits())
	}
}

func TestProxyDoesNotRetryOnOtherStatuses(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 500, "x")
	e.addTargetSpec(targetSpec("t", "/", `"trafficConfig":{"retries":3,"retryOn":[503],"retryDelay":{"min":"1ms","max":"1ms"}}`, up.URL))
	expectProxied(t, e.get("/foo"), 500)
	if up.hits() != 1 {
		t.Errorf("upstream hits = %d, want 1", up.hits())
	}
}

func TestProxyDelayDelaysUpstreamCall(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTargetSpec(targetSpec("t", "/", `"trafficConfig":{"delay":{"min":"150ms","max":"150ms"}}`, up.URL))
	start := time.Now()
	expectProxied(t, e.get("/foo"), 200)
	if took := time.Since(start); took < 150*time.Millisecond {
		t.Errorf("request took %s, want at least the 150ms delay", took)
	}
}

func TestProxyStreamsUpstreamResponse(t *testing.T) {
	e := newEnv(t)
	up := newUpstreamFunc(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		for _, chunk := range []string{"chunk1 ", "chunk2 ", "chunk3"} {
			io.WriteString(w, chunk)
			w.(http.Flusher).Flush()
			time.Sleep(5 * time.Millisecond)
		}
	})
	e.addTargetSpec(fmt.Sprintf(`{"name":"t","enabled":true,"endpoints":{"ep":{"url":%q,"stream":true}},"triggers":{"t1":{"matchAny":[{"uriPrefix":"/"}],"endpoints":["ep"]}}}`, up.URL))
	s := e.get("/stream")
	if !s.proxied {
		t.Fatal("request was not proxied")
	}
	body := s.rr.Body.String()
	for _, chunk := range []string{"chunk1 ", "chunk2 ", "chunk3"} {
		if !strings.Contains(body, chunk) {
			t.Errorf("body = %q, want it to have the streamed %q", body, chunk)
		}
	}
}

// ---------------------------------------------------------------------------------------------------------------
// Transformations

func TestProxyURIMapRewritesURI(t *testing.T) {
	tests := []struct {
		name     string
		uriMap   string
		trigger  string
		request  string
		wantPath string
	}{
		{"exact map", `{"/abc":"/api/foo"}`, `{"uriPrefix":"/abc"}`, "/abc", "/api/foo"},
		{"variable is carried over", `{"/users/{id}":"/v2/u/{id}"}`, `{"uriPrefix":"/users/{id}"}`, "/users/42", "/v2/u/42"},
		{"wildcard keeps the rest of the URI", `{"/old":"/new/*"}`, `{"uriPrefix":"/old"}`, "/old/a/b", "/new/a/b"},
		{"star key matches prefix", `{"/ab*":"/api/foobar"}`, `{"uriPrefix":"/ab"}`, "/abcd", "/api/foobar"},
		{"no entry leaves URI as is", `{"/zzz":"/api/foo"}`, `{"uriPrefix":"/abc"}`, "/abc/def", "/abc/def"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			up := newUpstream(t, 200, "ok")
			e.addTargetSpec(fmt.Sprintf(`{"name":"t","enabled":true,"endpoints":{"ep":{"url":%q}},"triggers":{"t1":{"matchAny":[%s],"endpoints":["ep"],"transform":{"uriMap":%s}}}}`,
				up.URL, tc.trigger, tc.uriMap))
			expectProxied(t, e.get(tc.request), 200)
			if got := up.only(t).Path; got != tc.wantPath {
				t.Errorf("upstream path = %q, want %q", got, tc.wantPath)
			}
		})
	}
}

func TestProxyStripURIRemovesPartOfURI(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTargetSpec(targetSpec("t", "/svc", `"transform":{"stripURI":"/svc"}`, up.URL))
	expectProxied(t, e.get("/svc/users/1?a=b"), 200)
	got := up.only(t)
	if got.Path != "/users/1" || got.Query["a"][0] != "b" {
		t.Errorf("upstream saw %s?%v, want /users/1?a=b", got.Path, got.Query)
	}
}

func TestProxyHeaderTransform(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTargetSpec(targetSpec("t", "/users/{id}", `"transform":{"headers":{"add":{"X-Added":"v1","X-From-URI":"id-{id}"},"remove":["x-remove-me"],"map":{"x-old":"X-New"}}}`, up.URL))
	expectProxied(t, e.get("/users/7", "X-Keep", "k", "X-Remove-Me", "r", "X-Old", "o"), 200)
	got := up.only(t).Headers
	if got.Get("X-Added") != "v1" {
		t.Errorf("X-Added = %q, want v1", got.Get("X-Added"))
	}
	if got.Get("X-From-URI") != "id-7" {
		t.Errorf("X-From-URI = %q, want the URI variable filled in (id-7)", got.Get("X-From-URI"))
	}
	if got.Get("X-Keep") != "k" {
		t.Errorf("X-Keep = %q, want it forwarded", got.Get("X-Keep"))
	}
	if _, present := got["X-Remove-Me"]; present {
		t.Errorf("X-Remove-Me was forwarded: %v", got["X-Remove-Me"])
	}
	if got.Get("X-New") != "o" || got.Get("X-Old") != "" {
		t.Errorf("X-New = %q X-Old = %q, want the header renamed", got.Get("X-New"), got.Get("X-Old"))
	}
}

func TestProxyQueryTransform(t *testing.T) {
	tests := []struct {
		name      string
		queries   string
		request   string
		wantQuery map[string]string
	}{
		{"add", `{"add":{"added":"1"}}`, "/q?keep=k", map[string]string{"keep": "k", "added": "1"}},
		{"remove", `{"remove":["drop"]}`, "/q?keep=k&drop=d", map[string]string{"keep": "k"}},
		{"remove all", `{"remove":["*"]}`, "/q?keep=k&drop=d", map[string]string{}},
		{"remove all then add", `{"remove":["*"],"add":{"only":"o"}}`, "/q?keep=k", map[string]string{"only": "o"}},
		{"add overrides", `{"add":{"keep":"new"}}`, "/q?keep=k", map[string]string{"keep": "new"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			up := newUpstream(t, 200, "ok")
			e.addTargetSpec(targetSpec("t", "/q", `"transform":{"queries":`+tc.queries+`}`, up.URL))
			expectProxied(t, e.get(tc.request), 200)
			got := map[string]string{}
			for k, v := range up.only(t).Query {
				got[k] = v[0]
			}
			if !reflect.DeepEqual(got, tc.wantQuery) {
				t.Errorf("upstream query = %v, want %v", got, tc.wantQuery)
			}
		})
	}
}

func TestProxyPayloadTransformReplacesRequestBody(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTargetSpec(fmt.Sprintf(`{"name":"t","enabled":true,"endpoints":{"ep":{"url":%q,"method":"POST"}},"triggers":{"t1":{"matchAny":[{"uriPrefix":"/users/{id}"}],"endpoints":["ep"],"transform":{"payload":"{\"user\":\"{id}\"}"}}}}`, up.URL))
	expectProxied(t, e.serve(e.newRequest("POST", "/users/9", strings.NewReader("original body, longer than the replacement"))), 200)
	if got := up.only(t); got.Body != `{"user":"9"}` || got.Method != "POST" {
		t.Errorf("upstream saw %s %q, want the payload of the transform with the URI variable filled in", got.Method, got.Body)
	}
}

func TestProxyRequestIDTransformSendsRequestID(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTargetSpec(targetSpec("t", "/", `"transform":{"requestId":{"send":true,"header":"X-Req-Id","query":"rid"}}`, up.URL))
	expectProxied(t, e.get("/x"), 200)
	got := up.only(t)
	if got.Headers.Get("X-Req-Id") == "" || got.Query["rid"] == nil {
		t.Errorf("upstream saw header %q query %v, want a request ID in both", got.Headers.Get("X-Req-Id"), got.Query)
	}
}

func TestProxyTargetTransformAppliesToTriggersWithoutOwn(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTargetSpec(fmt.Sprintf(`{"name":"t","enabled":true,"endpoints":{"ep":{"url":%q}},
		"transform":{"headers":{"add":{"X-Target":"target-level"}}},
		"triggers":{
			"own":{"matchAny":[{"uriPrefix":"/own"}],"endpoints":["ep"],"transform":{"headers":{"add":{"X-Trigger":"trigger-level"}}}},
			"plain":{"matchAny":[{"uriPrefix":"/plain"}],"endpoints":["ep"]}}}`, up.URL))
	expectProxied(t, e.get("/own"), 200)
	expectProxied(t, e.get("/plain"), 200)
	reqs := up.requests()
	if len(reqs) != 2 {
		t.Fatalf("upstream received %d requests, want 2", len(reqs))
	}
	byPath := map[string]http.Header{reqs[0].Path: reqs[0].Headers, reqs[1].Path: reqs[1].Headers}
	if byPath["/own"].Get("X-Trigger") != "trigger-level" || byPath["/own"].Get("X-Target") != "" {
		t.Errorf("/own headers = %v, want the trigger transform to replace the target transform", byPath["/own"])
	}
	if byPath["/plain"].Get("X-Target") != "target-level" || byPath["/plain"].Get("X-Trigger") != "" {
		t.Errorf("/plain headers = %v, want the target transform", byPath["/plain"])
	}
}

// ---------------------------------------------------------------------------------------------------------------
// Target selection

func TestProxyHandlesRequestsLocallyWithoutMatch(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTarget("t", "/api", up.URL)
	expectLocal(t, e.get("/other"))
	expectLocal(t, e.get("/ap"))
	expectProxied(t, e.get("/api/x"), 200)
	if up.hits() != 1 {
		t.Errorf("upstream hits = %d, want 1", up.hits())
	}
}

func TestProxyIsDisabled(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTarget("t", "/", up.URL)
	e.proxy.enable(false)
	expectLocal(t, e.get("/x"))
	e.proxy.enable(true)
	expectProxied(t, e.get("/x"), 200)
	if up.hits() != 1 {
		t.Errorf("upstream hits = %d, want 1", up.hits())
	}
}

func TestProxyWithoutTargetsHandlesRequestsLocally(t *testing.T) {
	e := newEnv(t)
	expectLocal(t, e.get("/x"))
}

func TestProxyDisabledTargetIsNotUsed(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTarget("t", "/", up.URL)
	if !e.proxy.enableTarget("t", false) {
		t.Fatal("enableTarget(t, false) = false")
	}
	expectLocal(t, e.get("/x"))
	e.proxy.enableTarget("t", true)
	expectProxied(t, e.get("/x"), 200)
	if up.hits() != 1 {
		t.Errorf("upstream hits = %d, want 1", up.hits())
	}
}

func TestProxyTargetAddedWithoutEnabledIsNotUsedUntilEnabled(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	spec := strings.Replace(targetSpec("t", "/", "", up.URL), `"enabled":true,`, "", 1)
	e.addTargetSpec(spec)
	expectLocal(t, e.get("/x"))
	e.proxy.enableTarget("t", true)
	expectProxied(t, e.get("/x"), 200)
}

func TestProxyRequestMatchingSeveralTargetsReachesAllOfThem(t *testing.T) {
	e := newEnv(t)
	up1 := newUpstream(t, 200, "one")
	up2 := newUpstream(t, 200, "two")
	up3 := newUpstream(t, 200, "three")
	e.addTarget("a", "/shared", up1.URL)
	e.addTarget("b", "/shared", up2.URL)
	e.addTarget("c", "/other", up3.URL)
	s := e.get("/shared/x")
	expectProxied(t, s, 200)
	res := results(t, s.rr)
	if len(res) != 2 || res["a"] == nil || res["b"] == nil {
		t.Errorf("results = %+v, want the responses of targets a and b", res)
	}
	if up1.hits() != 1 || up2.hits() != 1 || up3.hits() != 0 {
		t.Errorf("upstream hits = %d %d %d, want 1 1 0", up1.hits(), up2.hits(), up3.hits())
	}
}

func TestProxyRequestWithHeaderMatch(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTargetSpec(fmt.Sprintf(`{"name":"t","enabled":true,"endpoints":{"ep":{"url":%q}},"triggers":{"t1":{"matchAny":[{"uriPrefix":"/h","headers":{"x-match":"yes","x-present":""}}],"endpoints":["ep"]}}}`, up.URL))
	expectLocal(t, e.get("/h"))
	expectLocal(t, e.get("/h", "X-Match", "yes"))
	expectLocal(t, e.get("/h", "X-Match", "no", "X-Present", "1"))
	expectProxied(t, e.get("/h", "X-Match", "yes", "X-Present", "1"), 200)
	expectProxied(t, e.get("/h/more", "x-match", "yes", "x-present", "anything"), 200)
	if up.hits() != 2 {
		t.Errorf("upstream hits = %d, want 2", up.hits())
	}
}

// ---------------------------------------------------------------------------------------------------------------
// A status that is forced for requests: the proxy leaves the request to the status middleware instead of calling the
// upstream. The status is applied (counted) by the status middleware, which the tests emulate by getting the status.

func applyForcedStatus(e *env, req *http.Request) {
	status.TheStatusManager.GetStatusFor(e.port, req.RequestURI, req.Header, req.Method)
}

func TestProxySkipsUpstreamWhileUnlimitedStatusIsForced(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTarget("t", "/", up.URL)
	status.TheStatusManager.SetStatusFor(e.port, "", "", "", "", nil, []int{503}, -1, true)
	for i := 0; i < 5; i++ {
		req := e.newRequest("GET", "/forced", nil)
		expectLocal(t, e.serve(req))
		if util.GetRequestStore(req).ProxiedRequest {
			t.Fatalf("request %d is marked as proxied", i)
		}
		applyForcedStatus(e, req)
	}
	if up.hits() != 0 {
		t.Fatalf("upstream received %d requests while a status was forced, want 0", up.hits())
	}
	// The status is still forced after the requests, since it has no limit.
	expectLocal(t, e.get("/forced"))
	status.TheStatusManager.Clear(e.port, "", "")
	expectProxied(t, e.get("/forced"), 200)
	if up.hits() != 1 {
		t.Errorf("upstream hits = %d after the status was cleared, want 1", up.hits())
	}
}

func TestProxySkipsUpstreamWhileLimitedStatusIsForced(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTarget("t", "/", up.URL)
	status.TheStatusManager.SetStatusFor(e.port, "", "", "", "", nil, []int{503}, 2, true)
	for i := 0; i < 2; i++ {
		req := e.newRequest("GET", "/forced", nil)
		expectLocal(t, e.serve(req))
		if up.hits() != 0 {
			t.Fatalf("upstream received %d requests while a status was forced, want 0", up.hits())
		}
		applyForcedStatus(e, req)
	}
	// The status is used up now, so the request is proxied.
	expectProxied(t, e.get("/forced"), 200)
	if up.hits() != 1 {
		t.Fatalf("upstream hits = %d after the limited status was used up, want 1", up.hits())
	}
	expectProxied(t, e.get("/forced"), 200)
	if up.hits() != 2 {
		t.Errorf("upstream hits = %d, want 2", up.hits())
	}
}

func TestProxyCheckingForcedStatusDoesNotUseItUp(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTarget("t", "/", up.URL)
	status.TheStatusManager.SetStatusFor(e.port, "", "", "", "", nil, []int{503}, 1, true)
	for i := 0; i < 10; i++ {
		expectLocal(t, e.get("/forced"))
	}
	if up.hits() != 0 {
		t.Fatalf("upstream hits = %d, want 0: nothing applied the status yet", up.hits())
	}
	if st, remaining := status.TheStatusManager.PeekStatusFor(e.port, "/forced", nil, "GET"); st != 503 || remaining != 1 {
		t.Errorf("status = %d remaining = %d, want 503 with the one count left", st, remaining)
	}
}

func TestProxyForcedStatusForOneURIDoesNotStopOtherURIs(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTarget("t", "/", up.URL)
	status.TheStatusManager.SetStatusFor(e.port, "/only", "", "", "", nil, []int{503}, -1, true)
	expectLocal(t, e.get("/only"))
	expectProxied(t, e.get("/other"), 200)
	if up.hits() != 1 {
		t.Errorf("upstream hits = %d, want 1", up.hits())
	}
}

// ---------------------------------------------------------------------------------------------------------------
// Tracking

func TestProxyCountsRequests(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTarget("t", "/api", up.URL)
	for i := 0; i < 3; i++ {
		expectProxied(t, e.get("/api/x"), 200)
	}
	expectProxied(t, e.get("/api/y"), 200)
	tr := e.proxy.HTTPTracker
	if tr.DownstreamRequestCount != 4 || tr.DownstreamRequestCountsByURI["/api/x"] != 3 || tr.DownstreamRequestCountsByURI["/api/y"] != 1 {
		t.Errorf("downstream counts = %d %v", tr.DownstreamRequestCount, tr.DownstreamRequestCountsByURI)
	}
	if tr.UpstreamRequestCount != 4 || tr.UpstreamRequestCountsByURI["/api/x"] != 3 {
		t.Errorf("upstream counts = %d %v", tr.UpstreamRequestCount, tr.UpstreamRequestCountsByURI)
	}
	if tr.UpstreamRequestCountByStatus["200"] != 4 {
		t.Errorf("upstream counts by status = %v, want 4 of 200", tr.UpstreamRequestCountByStatus)
	}
	tt := tr.TargetTrackers["t"]
	if tt == nil || tt.DownstreamRequestCount != 4 || tt.UpstreamRequestCount != 4 || tt.UpstreamRequestCountsByEndpoint["ep1"] != 4 {
		t.Fatalf("target tracker = %+v", tt)
	}
	if tt.UpstreamRequestCountsByEndpointStatus["ep1"]["200"] != 4 {
		t.Errorf("upstream counts by endpoint status = %v", tt.UpstreamRequestCountsByEndpointStatus)
	}
	if tr.URIMatchCounts["/api"] != 4 {
		t.Errorf("URI match counts = %v, want 4 for /api", tr.URIMatchCounts)
	}
	target := e.proxy.getTarget("t")
	if target.CallCount != 4 || target.Endpoints["ep1"].CallCount != 4 || target.Triggers["t1"].CallCount != 4 {
		t.Errorf("call counts target=%d endpoint=%d trigger=%d, want 4 each", target.CallCount, target.Endpoints["ep1"].CallCount, target.Triggers["t1"].CallCount)
	}
}

func TestProxyCountsHeaderMatches(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTargetSpec(fmt.Sprintf(`{"name":"t","enabled":true,"endpoints":{"ep":{"url":%q}},"triggers":{"t1":{"matchAny":[{"uriPrefix":"/h","headers":{"x-match":"yes"}}],"endpoints":["ep"]}}}`, up.URL))
	expectProxied(t, e.get("/h", "X-Match", "yes"), 200)
	expectLocal(t, e.get("/h"))
	tr := e.proxy.HTTPTracker
	if tr.HeaderValueMatchCounts["x-match"]["yes"] != 1 {
		t.Errorf("header value match counts = %v, want 1 for x-match=yes", tr.HeaderValueMatchCounts)
	}
}

// ---------------------------------------------------------------------------------------------------------------
// Concurrency

// Requests of one endpoint share the client transport, and invocation updates the TLS config of that shared transport for
// every request, which pkg/transport does without any race between the requests that run concurrently.
func TestProxyConcurrentRequestsAreAllProxied(t *testing.T) {
	const workers, rounds = 8, 6
	e := newEnv(t)
	up1 := newUpstream(t, 200, "one")
	up2 := newUpstream(t, 201, "two")
	e.addTarget("t", "/", up1.URL, up2.URL)
	reqs := make([]*http.Request, workers*rounds)
	rrs := make([]*httptest.ResponseRecorder, len(reqs))
	for i := range reqs {
		reqs[i] = e.newRequest("GET", fmt.Sprintf("/c/%d", i), nil)
		rrs[i] = httptest.NewRecorder()
	}
	proxied := make([]bool, len(reqs))
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := w * rounds; i < (w+1)*rounds; i++ {
				if ok, router := WillProxyHTTP(reqs[i]); ok && router != nil {
					router.ServeHTTP(rrs[i], reqs[i])
					proxied[i] = true
				}
			}
		}(w)
	}
	wg.Wait()
	for i := range reqs {
		if !proxied[i] {
			t.Errorf("request %d was not proxied", i)
			continue
		}
		res := results(t, rrs[i])
		if len(res["t"]["ep1"]) != 1 || len(res["t"]["ep2"]) != 1 {
			t.Errorf("request %d results = %+v", i, res)
		}
	}
	total := workers * rounds
	if up1.hits() != total || up2.hits() != total {
		t.Errorf("upstream hits = %d %d, want %d each", up1.hits(), up2.hits(), total)
	}
	tr := e.proxy.HTTPTracker
	if tr.DownstreamRequestCount != total || tr.UpstreamRequestCount != 2*total {
		t.Errorf("tracker downstream=%d upstream=%d, want %d and %d", tr.DownstreamRequestCount, tr.UpstreamRequestCount, total, 2*total)
	}
	// The call count of a target counts the calls to its endpoints, which are two for every request.
	if got := e.proxy.getTarget("t").CallCount; got != 2*total {
		t.Errorf("target call count = %d, want %d", got, 2*total)
	}
}

func TestProxyConcurrentRequestsToDifferentTargets(t *testing.T) {
	const targets, perTarget = 6, 5
	e := newEnv(t)
	ups := make([]*upstream, targets)
	for i := range ups {
		ups[i] = newUpstream(t, 200, "ok")
		e.addTarget(fmt.Sprintf("t%d", i), fmt.Sprintf("/svc%d", i), ups[i].URL)
	}
	reqs := make([][]*http.Request, targets)
	rrs := make([][]*httptest.ResponseRecorder, targets)
	for i := range reqs {
		for j := 0; j < perTarget; j++ {
			reqs[i] = append(reqs[i], e.newRequest("GET", fmt.Sprintf("/svc%d/x%d", i, j), nil))
			rrs[i] = append(rrs[i], httptest.NewRecorder())
		}
	}
	proxied := make([]int, targets)
	var wg sync.WaitGroup
	for i := 0; i < targets; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j, r := range reqs[i] {
				if ok, router := WillProxyHTTP(r); ok && router != nil {
					router.ServeHTTP(rrs[i][j], r)
					proxied[i]++
				}
			}
		}(i)
	}
	wg.Wait()
	for i, up := range ups {
		if proxied[i] != perTarget || up.hits() != perTarget {
			t.Errorf("target %d: proxied=%d upstream hits=%d, want %d", i, proxied[i], up.hits(), perTarget)
		}
		if got := e.proxy.getTarget(fmt.Sprintf("t%d", i)).CallCount; got != perTarget {
			t.Errorf("target %d call count = %d, want %d", i, got, perTarget)
		}
	}
	tr := e.proxy.HTTPTracker
	if total := targets * perTarget; tr.DownstreamRequestCount != total || tr.UpstreamRequestCount != total {
		t.Errorf("tracker downstream=%d upstream=%d, want %d each", tr.DownstreamRequestCount, tr.UpstreamRequestCount, total)
	}
}
