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

package info

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gorilla/mux"
)

func printRecovering(level int, query, baseURL string, refresh, text bool) (rr *httptest.ResponseRecorder, panicked any) {
	rr = httptest.NewRecorder()
	func() {
		defer func() { panicked = recover() }()
		PrintRoutes(rr, level, query, baseURL, refresh, text, false)
	}()
	return rr, panicked
}

// A query that matched a sub path, and none of the roots and sub roots above it, was added to maps of the output that
// were not made.
func TestQueryThatMatchesOnlyASubPathDoesNotPanic(t *testing.T) {
	seedSample(t)
	rr, panicked := printRecovering(0, "add", "", false, false)
	if panicked != nil {
		t.Fatalf("PrintRoutes panicked: %v", panicked)
	}
	var got shaped
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q is not JSON routes: %v", rr.Body.String(), err)
	}
	want := APIRoutes{"/server": {"/server/listeners": {"/server/listeners/add": {{Left: "/server/listeners/add", Right: []string{"POST", "PUT"}}}}}}
	if !reflect.DeepEqual(got, shape(want)) {
		t.Errorf("routes = %v, want %v", got, shape(want))
	}
}

// The curls were made from the methods of the routes as if they were the format of the curl, which gave "POST%!(EXTRA
// string=...)", and were added to the routes instead of taking their place.
func TestCurlsAreMadeFromTheCurlsOfTheRoutes(t *testing.T) {
	seedSample(t)
	rr, panicked := printRecovering(0, "probes", "http://h:8080", false, false)
	if panicked != nil {
		t.Fatalf("PrintRoutes panicked: %v", panicked)
	}
	if strings.Contains(rr.Body.String(), "%!") {
		t.Errorf("body %q has a broken format", rr.Body.String())
	}
	var got shaped
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q is not JSON routes: %v", rr.Body.String(), err)
	}
	pairs := got["/server"]["/server/probes"]["/server/probes/set"]
	if len(pairs) != 1 {
		t.Fatalf("routes = %v, want one entry for the route, which has the curl", got)
	}
	if want := (map[string]string{"/server/probes/set?uri={uri}": `[curl -v -XPOST http://h:8080/server/probes/set\?uri={uri}]`}); !reflect.DeepEqual(pairs[0], want) {
		t.Errorf("curls = %q, want %q", pairs[0], want)
	}
}

// Each refresh added the routes to the ones that were cached, so every route was listed once more after each of them.
func TestRefreshDoesNotListRoutesAgain(t *testing.T) {
	seed(t, APIRoutes{}, APIRoutes{})
	core := mux.NewRouter()
	core.HandleFunc("/dupcheck/x/y", func(http.ResponseWriter, *http.Request) {}).Methods("GET")
	useCoreRouter(t, core)
	for i := 0; i < 3; i++ {
		if _, panicked := printRecovering(0, "dupcheck", "", true, false); panicked != nil {
			t.Fatalf("PrintRoutes panicked: %v", panicked)
		}
	}
	if got := routes["/dupcheck"]["/dupcheck/x"]["/dupcheck/x/y"]; len(got) != 1 {
		t.Errorf("route is listed %d times after 3 refreshes, want once: %v", len(got), got)
	}
	if got := routesWithCurl["/dupcheck"]["/dupcheck/x"]["/dupcheck/x/y"]; len(got) != 1 {
		t.Errorf("curl is listed %d times after 3 refreshes, want once: %v", len(got), got)
	}
}

// The routes are loaded from the core router, which is not there until the server is set up.
func TestLoadingRoutesWithoutACoreRouterDoesNotPanic(t *testing.T) {
	seed(t, APIRoutes{}, APIRoutes{})
	useCoreRouter(t, nil)
	if _, panicked := printRecovering(0, "", "", true, false); panicked != nil {
		t.Errorf("PrintRoutes panicked: %v", panicked)
	}
}

// The cached routes were loaded and read, and the output made of them changed, without any lock.
func TestConcurrentRequestsDoNotRaceOnTheRoutes(t *testing.T) {
	seedSample(t)
	core := mux.NewRouter()
	for _, p := range []string{"/c1/a/b", "/c1/a/c", "/c2/a/b", "/c3/x"} {
		core.HandleFunc(p, func(http.ResponseWriter, *http.Request) {}).Methods("GET", "POST")
	}
	useCoreRouter(t, core)
	queries := []string{"", "c1", "a", "add", "nothing"}
	urls := []string{"", "http://h:8080"}
	var wg sync.WaitGroup
	var panics atomic.Int32
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if recover() != nil {
					panics.Add(1)
				}
			}()
			for i := 0; i < 15; i++ {
				rr := httptest.NewRecorder()
				PrintRoutes(rr, (g+i)%4, queries[(g+i)%len(queries)], urls[i%2], (g+i)%3 == 0, i%5 == 0, false)
			}
		}()
	}
	wg.Wait()
	if n := panics.Load(); n != 0 {
		t.Errorf("%d of the requests panicked", n)
	}
}
