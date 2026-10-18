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

package tracking

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/mux"
)

const (
	workers = 8
	rounds  = 300
)

// The concurrent tests below follow a few rules, so that they can actually see data races:
//   - requests are created up front. Creating them inside goroutines is not what is under test.
//   - no *testing.T methods are used inside goroutines, since their internal locking would order the goroutines and
//     hide races.
//   - totals are asserted exactly, so that lost updates fail the test even when run without -race.

func prebuild(n int, method, target string, port int, headers ...string) []*http.Request {
	reqs := make([]*http.Request, n)
	for i := range reqs {
		reqs[i] = newRequest(method, target, port, headers...)
	}
	return reqs
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

type counts struct {
	byURI, byHeader, byHeaderValue, byURIAndHeader, byURIAndHeaderValue int
}

func countsFor(rtd *TrackingData, uri, header, value string) counts {
	rtd.lock.RLock()
	defer rtd.lock.RUnlock()
	c := counts{byURI: rtd.ByURI[uri]}
	if hd := rtd.ByHeader[header]; hd != nil {
		hd.lock.RLock()
		c.byHeader, c.byHeaderValue = hd.RequestCountsByHeader, hd.RequestCountsByHeaderValue[value]
		hd.lock.RUnlock()
	}
	if hd := rtd.ByURIAndHeader[uri][header]; hd != nil {
		hd.lock.RLock()
		c.byURIAndHeader, c.byURIAndHeaderValue = hd.RequestCountsByHeader, hd.RequestCountsByHeaderValue[value]
		hd.lock.RUnlock()
	}
	return c
}

func expectAllCounts(t *testing.T, got counts, want int) {
	t.Helper()
	if got != (counts{want, want, want, want, want}) {
		t.Errorf("counts = %+v, want every count to be %d", got, want)
	}
}

func setupTracked(t *testing.T) (*mux.Router, int, *TrackingData) {
	t.Helper()
	router, port := setup(t)
	Tracker.Init(port, "", "/foo", []string{"X"})
	Tracker.initHeaders(port, "", []string{"X"})
	return router, port, dataFor(port)
}

func TestConcurrentTrackCountsEveryRequest(t *testing.T) {
	_, _, rtd := setupTracked(t)
	headers := http.Header{"X": {"v"}}
	runWorkers(workers, func(int) {
		for i := 0; i < rounds; i++ {
			track("1", headers, "/foo", rtd)
		}
	})
	// The middleware counts the tracked headers. The URIs are counted by their hooks.
	n := workers * rounds
	if got, want := countsFor(rtd, "/foo", "X", "v"), (counts{byHeader: n, byHeaderValue: n}); got != want {
		t.Errorf("counts = %+v, want %+v", got, want)
	}
}

func TestConcurrentExportedTrackCountsEveryCall(t *testing.T) {
	_, port, rtd := setupTracked(t)
	matched := map[string][]string{"X": {"v"}}
	runWorkers(workers, func(int) {
		for i := 0; i < rounds; i++ {
			Track(port, "", "/foo", matched)
		}
	})
	expectAllCounts(t, countsFor(rtd, "/foo", "X", "v"), workers*rounds)
}

func TestConcurrentTrackCallCountsEveryCall(t *testing.T) {
	_, port, rtd := setupTracked(t)
	headers := map[string][]string{"X": {"v"}}
	runWorkers(workers, func(int) {
		for i := 0; i < rounds; i++ {
			Tracker.TrackCall(port, "", "/foo", headers)
		}
	})
	// The hooks count the tracked URIs and the headers tracked for them. The tracked headers are left to the middleware.
	n := workers * rounds
	if got, want := countsFor(rtd, "/foo", "X", "v"), (counts{byURI: n, byURIAndHeader: n, byURIAndHeaderValue: n}); got != want {
		t.Errorf("counts = %+v, want %+v", got, want)
	}
}

func TestConcurrentMiddlewareCountsEveryRequest(t *testing.T) {
	_, port, rtd := setupTracked(t)
	reqs := make([][]*http.Request, workers)
	for w := range reqs {
		reqs[w] = prebuild(rounds, "GET", "/foo", port, "X", "v")
	}
	handler := Middleware.MiddlewareHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	runWorkers(workers, func(w int) {
		for _, req := range reqs[w] {
			handler.ServeHTTP(httptest.NewRecorder(), req)
		}
	})

	// The middleware counts in goroutines that may still be running. It counts the tracked headers only.
	n := workers * rounds
	waitFor(t, "all header counts", func() bool {
		return countsFor(rtd, "/foo", "X", "v") == counts{byHeader: n, byHeaderValue: n}
	})
}

func TestConcurrentTrackingWhileConfigChanges(t *testing.T) {
	router, port, rtd := setupTracked(t)
	ops := []struct{ method, target string }{
		{"POST", "/track/headers/add/a,b,c"},
		{"GET", "/track/counts"},
		{"GET", "/track/headers/counts"},
		{"POST", "/track/headers/remove/a,b"},
		{"GET", "/track/headers"},
		{"POST", "/track/headers/counts/clear"},
		{"GET", "/track"},
		{"POST", "/track/headers/counts/clear/X"},
		{"POST", "/track/headers/add/X"},
		{"POST", "/track/headers/clear"},
		{"POST", "/track/headers/add/X"},
	}
	adminReqs := make([][]*http.Request, len(ops))
	for i, op := range ops {
		adminReqs[i] = prebuild(rounds, op.method, op.target, port)
	}
	headers := http.Header{"X": {"v"}, "A": {"a"}}
	matched := map[string][]string{"X": {"v"}, "a": {"a"}}

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				switch (w + i) % 3 {
				case 0:
					track("1", headers, "/foo", rtd)
				case 1:
					Track(port, "", "/foo", matched)
				default:
					Tracker.TrackCall(port, "", "/foo", matched)
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
}

func TestClearHeaderCountsClearsNamedHeaders(t *testing.T) {
	router, port := setup(t)
	expectStatus(t, call(router, "POST", "/track/headers/add/h1,h2", port), http.StatusOK)
	rtd := dataFor(port)
	for _, h := range []string{"h1", "h2"} {
		rtd.getHeaderData(h).trackRequest("v")
		rtd.getHeaderData(h).trackRequest("v")
	}

	expectStatus(t, call(router, "POST", "/track/headers/counts/clear/h1", port), http.StatusOK)
	if hd := rtd.getHeaderData("h1"); hd == nil || hd.RequestCountsByHeader != 0 || len(hd.RequestCountsByHeaderValue) != 0 {
		t.Errorf("h1 data = %+v, want its counts cleared", hd)
	}
	if hd := rtd.getHeaderData("h2"); hd.RequestCountsByHeader != 2 || hd.RequestCountsByHeaderValue["v"] != 2 {
		t.Errorf("h2 data = %+v, want its counts kept", hd)
	}
}

func TestClearHeaderCountsResetsTotals(t *testing.T) {
	router, port := setup(t)
	expectStatus(t, call(router, "POST", "/track/headers/add/h1", port), http.StatusOK)
	rtd := dataFor(port)
	rtd.getHeaderData("h1").trackRequest("v")

	expectStatus(t, call(router, "POST", "/track/headers/counts/clear", port), http.StatusOK)
	if hd := rtd.getHeaderData("h1"); hd.RequestCountsByHeader != 0 || len(hd.RequestCountsByHeaderValue) != 0 {
		t.Errorf("h1 data = %+v, want both the total and the value counts cleared", hd)
	}
}

func TestTrackURIAndHeadersRoute(t *testing.T) {
	router, port := setup(t)
	rr := call(router, "POST", "/track/headers/x,y?uri=/foo", port)
	expectStatus(t, rr, http.StatusOK)
	expectBodyContains(t, rr, "Port ["+portText(port)+"] will track URI [/foo] and Headers [[x y]]")

	rtd := dataFor(port)
	if count, present := rtd.ByURI["/foo"]; !present || count != 0 {
		t.Errorf("byURI = %v, want /foo tracked with 0 calls", rtd.ByURI)
	}
	for _, h := range []string{"x", "y"} {
		if _, present := rtd.ByURIAndHeader["/foo"][h]; !present {
			t.Errorf("byURIAndHeader = %v, want header %s tracked for /foo", rtd.ByURIAndHeader, h)
		}
	}
	if !hookMatches(port, "/foo", map[string][]string{"x": {"v"}}) {
		t.Error("a hook for the URI and headers must be registered on the port")
	}
}

// Routes that were registered after /headers/{headers}?uri= must keep matching the same handlers as before.
func TestTrackHeadersRoutesKeepTheirHandlers(t *testing.T) {
	root := mux.NewRouter()
	setRoutes(root)
	for _, tc := range []struct {
		method, target, handler string
	}{
		{"POST", "/track/headers/x,y", "trackHeaders"},
		{"POST", "/track/headers/x,y?uri=/foo", "trackURIAndHeaders"},
		{"POST", "/track/headers/clear", "clearHeaders"},
		{"POST", "/track/headers/add/x", "addHeaders"},
		{"POST", "/track/headers/remove/x", "removeHeaders"},
		{"POST", "/track/headers/counts/clear", "clearHeaderCounts"},
		{"POST", "/track/headers/counts/clear/x", "clearHeaderCounts"},
		{"GET", "/track/headers/counts", "getCounts"},
		{"GET", "/track/headers", "getHeaders"},
		{"POST", "/track?uri=/foo", "trackURI"},
		{"POST", "/track/foo=bar?uri=/x", "trackURIAndHeaderValue"},
		{"GET", "/track/counts", "getCounts"},
		{"GET", "/track", "getCounts"},
	} {
		t.Run(tc.method+" "+tc.target, func(t *testing.T) {
			var match mux.RouteMatch
			if !root.Match(httptest.NewRequest(tc.method, tc.target, nil), &match) {
				t.Fatalf("no route matched (err: %v), want %s", match.MatchErr, tc.handler)
			}
			name := runtime.FuncForPC(reflect.ValueOf(match.Route.GetHandler()).Pointer()).Name()
			if got := name[strings.LastIndex(name, ".")+1:]; got != tc.handler {
				t.Errorf("handler = %s, want %s", got, tc.handler)
			}
		})
	}
}

func TestTrackHandlersReportWhatTheyTrack(t *testing.T) {
	router, port := setup(t)
	for _, tc := range []struct{ target, want string }{
		{"/track/headers/h1,h2", "Port [" + portText(port) + "] will track Headers [[h1 h2]]"},
		{"/track/foo=bar?uri=/x", "Port [" + portText(port) + "] will track URI [/x] with Header:Value [foo:bar]"},
		{"/track?uri=/foo", "Port [" + portText(port) + "] will track URI [/foo]"},
	} {
		rr := call(router, "POST", tc.target, port)
		expectStatus(t, rr, http.StatusOK)
		expectBodyContains(t, rr, tc.want)
	}
}

func TestInvalidTrackRequestsReportTheProblem(t *testing.T) {
	_, port := setup(t)
	for name, h := range map[string]http.HandlerFunc{
		"Invalid Headers":          trackHeaders,
		"Invalid URI/Headers":      trackURIAndHeaders,
		"Invalid URI/Header/Value": trackURIAndHeaderValue,
	} {
		rr := callHandler(h, port, nil)
		expectStatus(t, rr, http.StatusBadRequest)
		expectBodyContains(t, rr, name)
	}
}

func TestCountsReportIsConsistentJSON(t *testing.T) {
	router, port, _ := setupTracked(t)
	Tracker.TrackCall(port, "", "/foo", map[string][]string{"X": {"v"}})
	rr := call(router, "GET", "/track/counts", port)
	expectStatus(t, rr, http.StatusOK)
	expectBodyContains(t, rr, `"byPort"`)
	expectBodyContains(t, rr, `"requestCountsByHeaderValue"`)
	expectBodyContains(t, rr, `"requestCounts": 1`)
}
