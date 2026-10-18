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
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"goto/pkg/server/hooks"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

// newServerRouting builds the routers the way the server does: the admin routes first, then the root router with
// the hooks middleware, which is also where the URI matchers of hooks get registered, and then a chain router with
// the tracking middleware and the handler that really serves traffic. It answers "real:<path>".
func newServerRouting(t *testing.T) http.Handler {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
	resetTracker()
	t.Cleanup(resetTracker)

	core := mux.NewRouter()
	core.SkipClean(true)
	admin := core.PathPrefix("").Subrouter()
	setRoutes(admin)
	root := util.CreateRouters(core)
	root.Use(hooks.Middleware.MiddlewareHandler)
	chain := root.PathPrefix("").Subrouter()
	chain.Use(Middleware.MiddlewareHandler)
	chain.PathPrefix("/").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "real:%s", r.URL.Path)
	})
	return core
}

func serve(server http.Handler, method, target string, port int, headers ...string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	server.ServeHTTP(rr, newRequest(method, target, port, headers...))
	return rr
}

// expectServed verifies that the request reached the handler that serves traffic, and was not answered by anything
// else, e.g. by a route that was registered while setting up tracking.
func expectServed(t *testing.T, server http.Handler, port int, path string, headers ...string) {
	t.Helper()
	rr := serve(server, "GET", path, port, headers...)
	if rr.Code != http.StatusOK || rr.Body.String() != "real:"+path {
		t.Errorf("GET %s = %d %q, want 200 %q: the request was not served by its handler", path, rr.Code, rr.Body.String(), "real:"+path)
	}
}

// expectCounts waits for the counts to be the wanted ones, and then checks that they stay so. The middleware counts
// in goroutines, and a request that is counted twice would only show up late.
func expectCounts(t *testing.T, rtd *TrackingData, uri, header, value string, want counts) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for countsFor(rtd, uri, header, value) != want {
		if time.Now().After(deadline) {
			t.Fatalf("counts of %s %s = %+v, want %+v", uri, header, countsFor(rtd, uri, header, value), want)
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if got := countsFor(rtd, uri, header, value); got != want {
		t.Errorf("counts of %s %s = %+v, want %+v: requests were counted more than once", uri, header, got, want)
	}
}

func trackedURIs(rtd *TrackingData) int {
	rtd.lock.RLock()
	defer rtd.lock.RUnlock()
	return len(rtd.ByURI) + len(rtd.ByURIAndHeader)
}

func TestHeaderOnlyTrackingDoesNotSwallowRequests(t *testing.T) {
	server := newServerRouting(t)
	port := int(20000 + portSeq.Add(1))
	expectStatus(t, serve(server, "POST", "/track/headers/h1", port), http.StatusOK)

	paths := []string{"/plain", "/foo", "/foo/bar", "/"}
	for _, path := range paths {
		expectServed(t, server, port, path)
		expectServed(t, server, port, path, "H1", "v1")
	}

	// The requests that carry the header are counted, once each. The URIs that are requested are not tracked.
	rtd := dataFor(port)
	n := len(paths)
	expectCounts(t, rtd, "/plain", "h1", "v1", counts{byHeader: n, byHeaderValue: n})
	if got := trackedURIs(rtd); got != 0 {
		t.Errorf("%d URIs are tracked, want none: tracking headers must not track the URIs of the requests", got)
	}
}

func TestHeaderOnlyTrackingFindsHeadersWhateverTheCaseOfTheirNames(t *testing.T) {
	server := newServerRouting(t)
	port := int(20000 + portSeq.Add(1))
	expectStatus(t, serve(server, "POST", "/track/headers/x-request-id", port), http.StatusOK)
	for i := 0; i < 3; i++ {
		expectServed(t, server, port, "/plain", "X-Request-Id", "abc")
	}
	expectCounts(t, dataFor(port), "/plain", "x-request-id", "abc", counts{byHeader: 3, byHeaderValue: 3})
}

func TestURITrackingDoesNotSwallowRequests(t *testing.T) {
	server := newServerRouting(t)
	port := int(20000 + portSeq.Add(1))
	expectStatus(t, serve(server, "POST", "/track?uri=/foo", port), http.StatusOK)

	// Everything under the tracked URI, and the URI itself, is still served.
	for _, path := range []string{"/foo", "/foo/bar", "/foobar", "/other", "/"} {
		expectServed(t, server, port, path)
	}
	// What is tracked is counted, once.
	rtd := dataFor(port)
	for path, want := range map[string]int{"/foo": 1, "/foo/bar": 1, "/foobar": 1} {
		expectCounts(t, rtd, path, "", "", counts{byURI: want})
	}
	for _, path := range []string{"/other", "/"} {
		if got := countsFor(rtd, path, "", "").byURI; got != 0 {
			t.Errorf("count of URI %s = %d, want 0: it is not tracked", path, got)
		}
	}
}

func TestURIAndHeaderTrackingDoNotSwallowRequests(t *testing.T) {
	server := newServerRouting(t)
	port := int(20000 + portSeq.Add(1))
	expectStatus(t, serve(server, "POST", "/track/headers/h1,h2?uri=/foo", port), http.StatusOK)
	expectStatus(t, serve(server, "POST", "/track/h3=v3?uri=/bar", port), http.StatusOK)

	for _, path := range []string{"/foo", "/foo/x", "/bar", "/bar/x", "/other"} {
		expectServed(t, server, port, path)
		expectServed(t, server, port, path, "H1", "v1", "H3", "v3")
	}

	// The header names were added in lower case, and the requests carry them in canonical case.
	rtd := dataFor(port)
	expectCounts(t, rtd, "/foo", "h1", "v1", counts{byURI: 2, byURIAndHeader: 1, byURIAndHeaderValue: 1})
	expectCounts(t, rtd, "/bar", "h3", "v3", counts{byURI: 2, byURIAndHeader: 1, byURIAndHeaderValue: 1})
}

func TestTrackedRequestsAreCountedOnceWhateverTheyAreTrackedBy(t *testing.T) {
	// A URI and a header that are tracked on their own, and requests that match both: the URI is counted by its
	// hook and the header by the middleware, and the handler that serves the URI is the one that answers.
	server := newServerRouting(t)
	port := int(20000 + portSeq.Add(1))
	expectStatus(t, serve(server, "POST", "/track?uri=/plain", port), http.StatusOK)
	expectStatus(t, serve(server, "POST", "/track/headers/h9", port), http.StatusOK)
	for i := 0; i < 3; i++ {
		expectServed(t, server, port, "/plain", "H9", "x")
	}
	expectCounts(t, dataFor(port), "/plain", "h9", "x", counts{byURI: 3, byHeader: 3, byHeaderValue: 3})
}
