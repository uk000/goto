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
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"goto/pkg/util"
)

// The tests here are regressions of bugs that were fixed in the proxy. They only use what the proxy had before the fixes,
// so that they can be run against the code before the fixes.

// adminSafely sends an admin request, and reports a panic of the handler as a failure of the test.
func (e *env) adminSafely(method, target, body string) (rr *httptest.ResponseRecorder) {
	e.t.Helper()
	defer func() {
		if p := recover(); p != nil {
			e.t.Errorf("%s %s panicked: %v", method, target, p)
			rr = httptest.NewRecorder()
			rr.WriteHeader(http.StatusInternalServerError)
		}
	}()
	return e.admin(method, target, body)
}

func TestRegressionSetResponsesStoresValidPayloadAndRejectsInvalidOne(t *testing.T) {
	e := newEnv(t)
	rr := e.adminSafely("POST", "/proxy/http/responses/set", `[{"upResponseRange":[500,599],"proxyResponse":502}]`)
	if rr.Code != http.StatusOK {
		t.Fatalf("setting responses answered %d: %s", rr.Code, rr.Body.String())
	}
	if got := e.proxy.ProxyResponses; len(got) != 1 || got[0].ProxyResponse != 502 || len(got[0].UpResponseRange) != 2 {
		t.Fatalf("proxy responses = %+v, want the ones that were set", got)
	}
	rr = e.adminSafely("GET", "/proxy/http/responses", "")
	if !strings.Contains(rr.Body.String(), `"proxyResponse": 502`) && !strings.Contains(rr.Body.String(), `"proxyResponse":502`) {
		t.Errorf("reported responses = %q, want the ones that were set", rr.Body.String())
	}
	rr = e.adminSafely("POST", "/proxy/http/responses/set", `not json`)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("setting responses to an invalid payload answered %d, want 400", rr.Code)
	}
	if got := e.proxy.ProxyResponses; len(got) != 1 {
		t.Errorf("an invalid payload changed the responses to %+v", got)
	}
}

func TestRegressionSetResponsesRemapsUpstreamStatus(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 503, "down")
	e.addTarget("t", "/", up.URL)
	if rr := e.adminSafely("POST", "/proxy/http/responses/set", `[{"upResponseRange":[500,599],"proxyResponse":502}]`); rr.Code != http.StatusOK {
		t.Fatalf("setting responses answered %d: %s", rr.Code, rr.Body.String())
	}
	expectProxied(t, e.get("/x"), 502)
}

func TestRegressionTargetTrackerOfUnknownTargetIsBadRequest(t *testing.T) {
	e := newEnv(t)
	rr := e.adminSafely("GET", "/proxy/http/targets/nosuch/tracker", "")
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Invalid target") {
		t.Errorf("tracker of an unknown target answered %d %q, want 400 Invalid target", rr.Code, rr.Body.String())
	}
}

func TestRegressionHeaderWithRemoveInItsNameIsAdded(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTarget("remover", "/", up.URL)
	rr := e.adminSafely("POST", "/proxy/http/targets/remover/headers/add/x-remove-id=abc", "")
	if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), "Failed") {
		t.Fatalf("adding the header answered %d %q", rr.Code, rr.Body.String())
	}
	expectProxied(t, e.get("/x"), 200)
	got := up.only(t).Headers
	if got.Get("x-remove-id") != "abc" {
		t.Errorf("upstream headers = %v, want x-remove-id: abc", got)
	}
}

func TestProxyRemovedHeaderIsNotSent(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTarget("t", "/", up.URL)
	if rr := e.adminSafely("POST", "/proxy/http/targets/t/headers/remove/x-secret", ""); rr.Code != http.StatusOK {
		t.Fatalf("removing the header answered %d %q", rr.Code, rr.Body.String())
	}
	expectProxied(t, e.get("/x", "x-secret", "s", "x-keep", "k"), 200)
	got := up.only(t).Headers
	if got.Get("x-secret") != "" || got.Get("x-keep") != "k" {
		t.Errorf("upstream headers = %v, want x-keep without x-secret", got)
	}
}

func TestRegressionGetProxyReportsTheProxyAsJSON(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTarget("t", "/", up.URL)
	rr := e.adminSafely("GET", "/proxy/http", "")
	var got struct {
		Port int `json:"port"`
		HTTP struct {
			Port    int                        `json:"port"`
			Enabled bool                       `json:"enabled"`
			Targets map[string]json.RawMessage `json:"targets"`
		} `json:"http"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("the proxy is not reported as JSON: %v: %q", err, rr.Body.String())
	}
	if got.Port != e.port || got.HTTP.Port != e.port || !got.HTTP.Enabled || got.HTTP.Targets["t"] == nil {
		t.Errorf("reported proxy = %+v (%s)", got, rr.Body.String())
	}
}

// Requests on a port that has no proxy yet all create it, and they must all end up with the same one.
func TestRegressionGetPortProxyReturnsOneProxyPerPort(t *testing.T) {
	setupGlobals()
	const ports, callers = 120, 16
	base := int(60000 + portSeq.Add(1)*ports)
	t.Cleanup(func() {
		proxyLock.Lock()
		for p := 0; p < ports; p++ {
			delete(portProxy, base+p)
		}
		proxyLock.Unlock()
	})
	got := make([][callers]*Proxy, ports)
	for p := 0; p < ports; p++ {
		start := make(chan struct{})
		var wg sync.WaitGroup
		for c := 0; c < callers; c++ {
			wg.Add(1)
			go func(p, c int) {
				defer wg.Done()
				<-start
				got[p][c] = GetPortProxy(base + p)
			}(p, c)
		}
		close(start)
		wg.Wait()
	}
	for p := 0; p < ports; p++ {
		for c := 1; c < callers; c++ {
			if got[p][c] != got[p][0] {
				t.Fatalf("port %d: callers got different proxies", base+p)
			}
		}
	}
}

// The endpoints of a request answer concurrently, and report to the same response headers.
func TestRegressionEndpointsOfARequestReportTheirHeadersSafely(t *testing.T) {
	e := newEnv(t)
	up1 := newUpstream(t, 200, "one")
	up2 := newUpstream(t, 200, "two")
	e.addTarget("t", "/", up1.URL, up2.URL)
	for i := 0; i < 60; i++ {
		s := e.get(fmt.Sprintf("/r/%d", i))
		expectProxied(t, s, 200)
	}
	if up1.hits() != 60 || up2.hits() != 60 {
		t.Errorf("upstream hits = %d, %d, want 60 each", up1.hits(), up2.hits())
	}
}

// A request body can be read once, but every endpoint of the request must send it.
func TestRegressionBodyReachesEveryEndpoint(t *testing.T) {
	e := newEnv(t)
	up1 := newUpstream(t, 200, "one")
	up2 := newUpstream(t, 200, "two")
	e.addTarget("t", "/", up1.URL, up2.URL)
	expectProxied(t, e.serve(e.newRequest("POST", "/x", strings.NewReader("the body"), "Content-Type", "text/plain")), 200)
	for i, up := range []*upstream{up1, up2} {
		if got := up.only(t); got.Method != "POST" || got.Body != "the body" {
			t.Errorf("upstream %d saw %s with body %q, want POST with the body", i+1, got.Method, got.Body)
		}
	}
}

// An endpoint that can't be called must not leave the request waiting for its response.
func TestRegressionEndpointWithUnsupportedProtocolDoesNotHang(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTargetSpec(fmt.Sprintf(`{"name":"t","enabled":true,"endpoints":{"ep1":{"url":%q,"protocol":"tcp"}},`+
		`"triggers":{"t1":{"matchAny":[{"uriPrefix":"/"}],"endpoints":["ep1"]}}}`, up.URL))
	done := make(chan served, 1)
	req := e.newRequest("GET", "/x", nil)
	go func() { done <- e.serve(req) }()
	select {
	case s := <-done:
		if !s.proxied || !strings.Contains(s.rr.Body.String(), "No Response") {
			t.Errorf("request answered %d %q (proxied=%v), want the proxy to report that there was no response", s.rr.Code, s.rr.Body.String(), s.proxied)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the request to an endpoint that can't be called did not complete")
	}
	if up.hits() != 0 {
		t.Errorf("upstream got %d requests through an unsupported protocol", up.hits())
	}
}

func collectorGoroutines() int {
	buf := make([]byte, 1<<22)
	buf = buf[:runtime.Stack(buf, true)]
	return strings.Count(string(buf), "asyncCollectResponses")
}

// The goroutine that collects the upstream responses of a request must end with the request.
func TestRegressionRequestsDoNotLeakResponseCollectors(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTarget("t", "/", up.URL)
	before := collectorGoroutines()
	for i := 0; i < 20; i++ {
		expectProxied(t, e.get(fmt.Sprintf("/r/%d", i)), 200)
	}
	deadline := time.Now().Add(3 * time.Second)
	var after int
	for after = collectorGoroutines(); after > before && time.Now().Before(deadline); after = collectorGoroutines() {
		time.Sleep(20 * time.Millisecond)
	}
	if after > before {
		t.Errorf("%d response collectors are left running after 20 requests (%d before)", after-before, before)
	}
}

func TestRegressionHeaderRemovedTwiceIsListedOnceAndClearedCompletely(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTarget("t", "/", up.URL)
	for i := 0; i < 2; i++ {
		if rr := e.adminSafely("POST", "/proxy/http/targets/t/headers/remove/x-a", ""); rr.Code != http.StatusOK {
			t.Fatalf("removing the header answered %d %q", rr.Code, rr.Body.String())
		}
	}
	remove := func() []string { return e.proxy.getTarget("t").Transform.Headers.Remove }
	if got := remove(); len(got) != 1 || got[0] != "x-a" {
		t.Fatalf("removed headers = %v, want x-a once", got)
	}
	if rr := e.adminSafely("POST", "/proxy/http/targets/t/headers/clear/x-a", ""); rr.Code != http.StatusOK {
		t.Fatalf("clearing the header answered %d %q", rr.Code, rr.Body.String())
	}
	if got := remove(); len(got) != 0 {
		t.Errorf("removed headers after clearing x-a = %v, want none", got)
	}
}

func TestRegressionExactURIMatchesRequestWithQuery(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTargetSpec(fmt.Sprintf(`{"name":"t","enabled":true,"endpoints":{"ep1":{"url":%q}},"triggers":{`+
		`"t1":{"matchAny":[{"uri":"/svc/exact"}],"endpoints":["ep1"]}}}`, up.URL))
	expectProxied(t, e.get("/svc/exact?x=1&y=2"), 200)
	if got := up.only(t); got.Path != "/svc/exact" || got.Query["x"][0] != "1" || got.Query["y"][0] != "2" {
		t.Errorf("upstream saw %+v", got)
	}
}

func TestRegressionTriggerWithVariableFirstSegmentIsServed(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTargetSpec(targetSpec("t", "/{svc}/bar", "", up.URL))
	expectProxied(t, e.get("/foo/bar"), 200)
	if got := up.only(t); got.Path != "/foo/bar" {
		t.Errorf("upstream saw %+v", got)
	}
}

func TestRegressionTriggersOfOneRootURIAreServedByTheirOwnRoutes(t *testing.T) {
	e := newEnv(t)
	up1 := newUpstream(t, 200, "one")
	up2 := newUpstream(t, 200, "two")
	// Both triggers are under /svc, but one has a variable in the first segment of its URI, which has a router of its own.
	e.addTarget("a", "/svc/only", up1.URL)
	e.addTargetSpec(targetSpec("b", "/{x}/other", "", up2.URL))
	expectProxied(t, e.get("/svc/only/1"), 200)
	expectProxied(t, e.get("/svc/other"), 200)
	if up1.hits() != 1 || up2.hits() != 1 {
		t.Errorf("upstream hits = %d, %d, want 1 each", up1.hits(), up2.hits())
	}
}

func TestRegressionProxyRequestIgnoresTargetsOfAnotherProxy(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTarget("t", "/", up.URL)
	req := e.newRequest("GET", "/x", nil)
	// Another kind of proxy (MCP) leaves its own matches in the request store.
	util.GetRequestStore(req).ProxyTargets = map[string]string{"other": "kind"}
	rr := httptest.NewRecorder()
	func() {
		defer func() {
			if p := recover(); p != nil {
				t.Errorf("ProxyRequest panicked: %v", p)
			}
		}()
		ProxyRequest(rr, req)
	}()
	if up.hits() != 1 {
		t.Errorf("upstream hits = %d, want the request to be matched again and proxied", up.hits())
	}
}

func TestRegressionTrackersOfDifferentTargetsAreCreatedConcurrently(t *testing.T) {
	tr := NewHTTPTracker()
	const targets = 64
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < targets; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("target-%d", i)
			<-start
			for j := 0; j < 20; j++ {
				tr.IncrementTargetMatchCounts(name, "/uri", "h", "v", "", "")
				tr.IncrementTargetDropCount(name, "/uri", j%2 == 0)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	if len(tr.TargetTrackers) != targets {
		t.Fatalf("trackers of %d targets, want %d", len(tr.TargetTrackers), targets)
	}
	if tr.URIMatchCounts["/uri"] != targets*20 {
		t.Errorf("uri match count = %d, want %d", tr.URIMatchCounts["/uri"], targets*20)
	}
}

// The transform of a target or trigger is created when the first header is added to it, which requests that are matched
// at the same time read.
func TestRegressionTransformIsReadSafelyWhileFirstHeaderIsAdded(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	const rounds = 30
	for i := 0; i < rounds; i++ {
		// The names have the same length, as a URI prefix is a prefix of other URIs that begin with it.
		name := fmt.Sprintf("t%03d", i)
		e.addTarget(name, "/"+name, up.URL)
		req := e.newRequest("GET", "/"+name+"/x", nil)
		done := make(chan bool, 1)
		go func() { done <- e.proxy.addRemoveHeader(name, "", "x-h", "v", false) }()
		s := e.serve(req)
		if !<-done {
			t.Fatalf("round %d: the header was not added", i)
		}
		expectProxied(t, s, 200)
	}
	if up.hits() != rounds {
		t.Errorf("upstream hits = %d, want %d", up.hits(), rounds)
	}
}

// Enabling and disabling a target while the targets are reported: the flag is written under the lock of the proxy,
// and the target is reported under its own lock, so both must be held to write it.
func TestRegressionEnablingATargetWhileTargetsAreReported(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTarget("flip", "/flip", up.URL)
	const rounds = 400
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			e.proxy.enableTarget("flip", i%2 == 0)
		}
	}()
	errs := make(chan error, rounds)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			// What GET /proxy/http/targets reports, without the lock of the proxy.
			if _, err := json.Marshal(e.proxy.getTargets()); err != nil {
				errs <- err
			}
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("reporting targets: %v", err)
	}
}
