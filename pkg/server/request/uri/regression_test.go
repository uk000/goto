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

package uri

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"goto/pkg/constants"
	"goto/pkg/server/intercept"
	"goto/pkg/util"
)

const (
	workers = 8
	rounds  = 100
)

// The concurrent tests below follow a few rules, so that they can actually see data races:
//   - requests are created up front. Creating them inside goroutines is not what is under test.
//   - no *testing.T methods are used inside goroutines, since their internal locking would order the goroutines and
//     hide races. Problems are counted with atomics and reported by the test goroutine.
//   - totals are asserted exactly, so that lost updates fail the test even when run without -race.

// preparedServe is a request that is ready to go through the URI middleware, as serve does it.
type preparedServe struct {
	req *http.Request
	w   http.ResponseWriter
	rr  *httptest.ResponseRecorder
	irw *intercept.InterceptResponseWriter
}

func prepareServe(path string, port int) *preparedServe {
	p := &preparedServe{rr: httptest.NewRecorder(), req: newRequest("GET", path, port)}
	p.w, p.irw = intercept.WithIntercept(p.req, p.rr)
	return p
}

func (p *preparedServe) run() *preparedServe {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusAccepted) })
	Middleware.MiddlewareHandler(next).ServeHTTP(p.w, p.req)
	p.irw.Proceed()
	return p
}

func prepareAll(workers, rounds int, path func(worker, round int) string, port func(worker, round int) int) [][]*preparedServe {
	all := make([][]*preparedServe, workers)
	for w := range all {
		all[w] = make([]*preparedServe, rounds)
		for i := range all[w] {
			all[w][i] = prepareServe(path(w, i), port(w, i))
		}
	}
	return all
}

func runWorkers(n int, f func(worker int)) {
	var wg sync.WaitGroup
	for w := 0; w < n; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			f(w)
		}(w)
	}
	wg.Wait()
}

func samePath(path string) func(int, int) string { return func(int, int) string { return path } }
func samePort(port int) func(int, int) int       { return func(int, int) int { return port } }

func TestConcurrentLimitedStatusIsAppliedExactly(t *testing.T) {
	router := setup(t)
	const limit = 500 // fewer than the 800 requests that are sent
	expectStatus(t, call(router, "POST", "/uri/set/status=418:500?uri=/foo", port1), http.StatusOK)
	served := prepareAll(workers, rounds, samePath("/foo"), samePort(port1))

	var forced atomic.Int64
	runWorkers(workers, func(w int) {
		for _, p := range served[w] {
			if p.run().rr.Code == 418 {
				forced.Add(1)
			}
		}
	})

	if forced.Load() != limit {
		t.Errorf("%d requests got the forced status, want exactly %d", forced.Load(), limit)
	}
	if statusConfig(t, port1, "/foo") != nil {
		t.Error("the config must be removed once its calls are used up")
	}
}

func TestConcurrentLimitedDelayIsAppliedExactly(t *testing.T) {
	router := setup(t)
	const limit = 100 // fewer than the 800 requests that are sent
	expectStatus(t, call(router, "POST", "/uri/set/delay=1ms:100?uri=/foo", port1), http.StatusOK)
	served := prepareAll(workers, rounds, samePath("/foo"), samePort(port1))

	var delayed atomic.Int64
	runWorkers(workers, func(w int) {
		for _, p := range served[w] {
			if p.run().rr.Header().Get(constants.HeaderGotoResponseDelay) != "" {
				delayed.Add(1)
			}
		}
	})

	if delayed.Load() != limit {
		t.Errorf("%d requests were delayed, want exactly %d", delayed.Load(), limit)
	}
	if delayConfig(t, port1, "/foo") != nil {
		t.Error("the config must be removed once its calls are used up")
	}
}

func TestConcurrentCountingCountsEveryRequest(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(router, "POST", "/uri/set/status=418?uri=/configured", port1), http.StatusOK)
	expectStatus(t, call(router, "POST", "/uri/counts/enable", port1), http.StatusOK)
	served := prepareAll(workers, rounds, func(w, _ int) string {
		if w%2 == 0 {
			return "/configured"
		}
		return "/plain"
	}, samePort(port1))

	runWorkers(workers, func(w int) {
		for _, p := range served[w] {
			p.run()
		}
	})

	want := workers / 2 * rounds
	counts := uriCountsByPort[portText(port1)]
	if counts["/configured"] != want || counts["/plain"] != want {
		t.Errorf("counts = %v, want %d for both /configured and /plain", counts, want)
	}
}

func TestConcurrentConfigChanges(t *testing.T) {
	router := setup(t)
	ops := []struct{ method, target string }{
		{"POST", "/uri/set/status=418:20?uri=/foo"},
		{"POST", "/uri/set/status=500?uri=/foo*"},
		{"POST", "/uri/set/delay=1ms:5?uri=/foo"},
		{"POST", "/uri/counts/enable"},
		{"GET", "/uri"},
		{"GET", "/uri/counts"},
		{"POST", "/uri/set/status=0?uri=/foo"},
		{"POST", "/uri/set/delay=0s?uri=/foo"},
		{"POST", "/uri/counts/clear"},
		{"POST", "/uri/counts/disable"},
		{"GET", "/uri"},
	}
	adminReqs := make([][]*http.Request, len(ops))
	for i, op := range ops {
		adminReqs[i] = make([]*http.Request, rounds)
		for j := range adminReqs[i] {
			adminReqs[i][j] = newRequest(op.method, op.target, port1)
		}
	}
	// Traffic on a port of its own for every request makes the per port maps grow while they are being read.
	served := prepareAll(workers, rounds, samePath("/foo/bar"), func(w, i int) int {
		if i%2 == 0 {
			return port1
		}
		return port2 + w*1000 + i
	})

	var unexpected atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for _, p := range served[w] {
				if code := p.run().rr.Code; code != http.StatusAccepted && code != 418 && code != 500 {
					unexpected.Add(1)
				}
			}
		}(w)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			for op := range ops {
				router.ServeHTTP(httptest.NewRecorder(), adminReqs[op][i])
			}
		}
	}()
	wg.Wait()

	if unexpected.Load() != 0 {
		t.Errorf("%d requests got an unexpected status", unexpected.Load())
	}
}

func TestSetStatusRejectsInvalidStatus(t *testing.T) {
	for _, status := range []string{"abc", ",", ":3", "99", "1000"} {
		t.Run(status, func(t *testing.T) {
			router := setup(t)
			expectStatus(t, call(router, "POST", "/uri/set/status=418?uri=/foo", port1), http.StatusOK)

			rr := call(router, "POST", "/uri/set/status="+status+"?uri=/foo", port1)
			expectStatus(t, rr, http.StatusBadRequest)
			expectBodyContains(t, rr, "Invalid status")
			if cfg := statusConfig(t, port1, "/foo"); cfg == nil || cfg.Statuses[0] != 418 {
				t.Errorf("config = %+v, want 418 to be kept after the rejected request", cfg)
			}
		})
	}
}

func TestMiddlewareCountsAllURIs(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(router, "POST", "/uri/counts/enable", port1), http.StatusOK)

	for _, path := range []string{"/plain", "/plain?x=1", "/PLAIN", "/other"} {
		prepareServe(path, port1).run()
	}
	prepareServe("/plain", port2).run()
	prepareServe("/server/request/uri", port1).run() // admin requests are not traffic

	counts := uriCountsByPort[portText(port1)]
	if counts["/plain"] != 3 || counts["/other"] != 1 || len(counts) != 2 {
		t.Errorf("counts = %v, want /plain=3 and /other=1 although no URI is configured", counts)
	}
	if got := uriCountsByPort[portText(port2)]["/plain"]; got != 1 {
		t.Errorf("counts of the other port = %v, want /plain=1", uriCountsByPort[portText(port2)])
	}
}

func TestPrefixMatchUsesLongestGlobConfig(t *testing.T) {
	setup(t)
	initPort(newRequest("GET", "/", port1))
	exact := &URIStatusConfig{URI: "/foo", Statuses: []int{418}, Times: -1}
	short := &URIStatusConfig{URI: "/fo", Glob: true, Statuses: []int{500}, Times: -1}
	longer := &URIStatusConfig{URI: "/foo/b", Glob: true, Statuses: []int{503}, Times: -1}
	shortest := &URIStatusConfig{URI: "/f", Glob: true, Statuses: []int{502}, Times: -1}
	for _, cfg := range []*URIStatusConfig{exact, short, longer, shortest} {
		uriStatusByPort[portText(port1)][cfg.URI] = cfg
	}

	// Map iteration order is random, so the answer must not depend on it.
	for i := 0; i < 100; i++ {
		if got := GetURIStatus(newRequest("GET", "/foo", port1)); got != exact {
			t.Fatalf("GetURIStatus(/foo) = %+v, want the exact config", got)
		}
		if got := GetURIStatus(newRequest("GET", "/foo/x", port1)); got != short {
			t.Fatalf("GetURIStatus(/foo/x) = %+v, want the longest glob config that matches, /fo*", got)
		}
		if got := GetURIStatus(newRequest("GET", "/foo/bar", port1)); got != longer {
			t.Fatalf("GetURIStatus(/foo/bar) = %+v, want the longest glob config that matches, /foo/b*", got)
		}
		if got := GetURIStatus(newRequest("GET", "/fx", port1)); got != shortest {
			t.Fatalf("GetURIStatus(/fx) = %+v, want /f*", got)
		}
	}
}

func TestHasAnyURIStatusOrDelayIgnoresEmptyConfigs(t *testing.T) {
	router := setup(t)
	initPort(newRequest("GET", "/", port1)) // creates empty config maps for the port
	if HasAnyURIStatusOrDelay() {
		t.Error("a port without any status or delay config must not count")
	}

	expectStatus(t, call(router, "POST", "/uri/set/status=418?uri=/foo", port1), http.StatusOK)
	if !HasAnyURIStatusOrDelay() {
		t.Error("a configured status must count")
	}
	expectStatus(t, call(router, "POST", "/uri/set/status=0?uri=/foo", port1), http.StatusOK)
	if HasAnyURIStatusOrDelay() {
		t.Error("nothing is configured once the status is cleared")
	}

	expectStatus(t, call(router, "POST", "/uri/set/delay=1ms?uri=/foo", port2), http.StatusOK)
	if !HasAnyURIStatusOrDelay() {
		t.Error("a configured delay must count")
	}
}

func TestExhaustedStatusIsNotApplied(t *testing.T) {
	setup(t)
	initPort(newRequest("GET", "/", port1))
	cfg := &URIStatusConfig{URI: "/foo", Statuses: []int{418}, Times: 0}
	rr := httptest.NewRecorder()
	if got := computeURIStatus("/foo", portText(port1), cfg, newRequest("GET", "/foo", port1), rr); got != 0 {
		t.Errorf("computeURIStatus = %d, want 0 for a config with no calls left", got)
	}
}

func TestConfigRemovalKeepsReplacementConfig(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(router, "POST", "/uri/set/status=418:1?uri=/foo", port1), http.StatusOK)
	old := statusConfig(t, port1, "/foo")

	// The old config is replaced, then used up by a request that still holds it: the replacement must survive.
	expectStatus(t, call(router, "POST", "/uri/set/status=500?uri=/foo", port1), http.StatusOK)
	replacement := statusConfig(t, port1, "/foo")
	rr := httptest.NewRecorder()
	computeURIStatus("/foo", portText(port1), old, newRequest("GET", "/foo", port1), rr)
	if got := statusConfig(t, port1, "/foo"); got != replacement {
		t.Errorf("config = %+v, want the replacement %+v to survive the removal of the old one", got, replacement)
	}
}

// HasURIStatus is how the status middleware finds out that the URI middleware, which is outside of it, has taken the
// status of the response. It must agree with GetURIStatus about which configs have a status to report. A status
// that is not limited has Times -1, which is what a status gets when no count is given.
func TestHasURIStatusAgreesWithGetURIStatus(t *testing.T) {
	for _, tt := range []struct {
		name   string
		config *URIStatusConfig
		path   string
		want   bool
	}{
		{"not limited", &URIStatusConfig{URI: "/foo", Statuses: []int{418}, Times: -1}, "/foo", true},
		{"calls left", &URIStatusConfig{URI: "/foo", Statuses: []int{418}, Times: 3}, "/foo", true},
		{"last call", &URIStatusConfig{URI: "/foo", Statuses: []int{418}, Times: 1}, "/foo", true},
		{"used up", &URIStatusConfig{URI: "/foo", Statuses: []int{418}, Times: 0}, "/foo", false},
		{"no status", &URIStatusConfig{URI: "/foo", Statuses: []int{0}, Times: -1}, "/foo", false},
		{"no statuses", &URIStatusConfig{URI: "/foo", Times: -1}, "/foo", false},
		{"glob, not limited", &URIStatusConfig{URI: "/foo", Glob: true, Statuses: []int{418}, Times: -1}, "/foo/bar", true},
		{"not a glob", &URIStatusConfig{URI: "/foo", Statuses: []int{418}, Times: -1}, "/foo/bar", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			setup(t)
			initPort(newRequest("GET", "/", port1))
			uriStatusByPort[portText(port1)][tt.config.URI] = tt.config

			req := newRequest("GET", tt.path, port1)
			if got := HasURIStatus(req); got != tt.want {
				t.Errorf("HasURIStatus(%s) = %v, want %v", tt.path, got, tt.want)
			}
			if got := GetURIStatus(req) != nil; got != tt.want {
				t.Errorf("GetURIStatus(%s) != nil is %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

// The status middleware asks inside the chain of the URI middleware, after the URI middleware has used up the count
// of the status. The last call of a status is then no longer configured, and must still be reported as a request
// that has a URI status, like the calls before it, or else the status middleware would also change its status.
func TestHasURIStatusInsideTheChainOfTheURIMiddleware(t *testing.T) {
	for _, tt := range []struct {
		name  string
		times int
		calls int
	}{
		{"not limited", -1, 3},
		{"limited", 3, 3},
		{"one call", 1, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			setup(t)
			initPort(newRequest("GET", "/", port1))
			uriStatusByPort[portText(port1)]["/foo"] = &URIStatusConfig{URI: "/foo", Statuses: []int{418}, Times: tt.times}

			for call := 1; call <= tt.calls; call++ {
				if !hasURIStatusInsideChain("/foo", port1) {
					t.Errorf("call %d of %d: HasURIStatus = false inside the chain, want true", call, tt.calls)
				}
			}
			// Once the calls are used up, requests are not given a status, and are not reported as having one.
			if tt.times > 0 {
				if hasURIStatusInsideChain("/foo", port1) {
					t.Error("HasURIStatus = true after the status was used up, want false")
				}
			}
			if hasURIStatusInsideChain("/bar", port1) {
				t.Error("HasURIStatus = true for a URI that has no status, want false")
			}
		})
	}
}

func TestHasURIStatusIsFalseWhenAnotherRequestUsedUpTheStatus(t *testing.T) {
	setup(t)
	initPort(newRequest("GET", "/", port1))
	config := &URIStatusConfig{URI: "/foo", Statuses: []int{418}, Times: 1}
	uriStatusByPort[portText(port1)]["/foo"] = config

	// A request that found the config just before another one used it up gets no status from the middleware.
	if got := computeURIStatus("/foo", portText(port1), config, newRequest("GET", "/foo", port1), httptest.NewRecorder()); got != 418 {
		t.Fatalf("first use = %d, want 418", got)
	}
	p := prepareServe("/foo", port1)
	if got := computeURIStatus("/foo", portText(port1), config, p.req, p.rr); got != 0 {
		t.Fatalf("second use of a used up status = %d, want 0", got)
	}
	if HasURIStatus(p.req) {
		t.Error("HasURIStatus = true for a request that was not given a status")
	}
}

// hasURIStatusInsideChain serves a request through the URI middleware, and returns what HasURIStatus reports from
// inside its chain, which is where the status middleware asks.
func hasURIStatusInsideChain(path string, port int) bool {
	p := prepareServe(path, port)
	seen := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = HasURIStatus(r)
		w.WriteHeader(http.StatusAccepted)
	})
	Middleware.MiddlewareHandler(next).ServeHTTP(p.w, p.req)
	p.irw.Proceed()
	return seen
}

// The status middleware counts the response under the status that the URI middleware applied, and reads it from the
// request store while the rest of the chain runs.
func TestAppliedURIStatusIsInTheRequestStoreInsideTheChain(t *testing.T) {
	setup(t)
	initPort(newRequest("GET", "/", port1))
	uriStatusByPort[portText(port1)]["/foo"] = &URIStatusConfig{URI: "/foo", Statuses: []int{418}, Times: 1}
	uriStatusByPort[portText(port1)]["/glob"] = &URIStatusConfig{URI: "/glob", Glob: true, Statuses: []int{503}, Times: -1}

	appliedInsideChain := func(path string) int {
		p := prepareServe(path, port1)
		applied := -1
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			applied = util.GetRequestStore(r).AppliedURIStatus
		})
		Middleware.MiddlewareHandler(next).ServeHTTP(p.w, p.req)
		p.irw.Proceed()
		return applied
	}
	for _, tt := range []struct {
		path string
		want int
	}{
		{"/foo", 418},    // the call that uses the status up
		{"/foo", 0},      // used up
		{"/glob/x", 503}, // a glob that is not limited
		{"/glob/y", 503},
		{"/other", 0},
	} {
		if got := appliedInsideChain(tt.path); got != tt.want {
			t.Errorf("AppliedURIStatus of %s inside the chain = %d, want %d", tt.path, got, tt.want)
		}
	}
}
