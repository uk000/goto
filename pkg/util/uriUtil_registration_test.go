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

package util

import (
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gorilla/mux"
)

func countRoutes(r *mux.Router, withHandler bool) int {
	n := 0
	r.Walk(func(route *mux.Route, _ *mux.Router, _ []*mux.Route) error {
		if !withHandler || route.GetHandler() != nil {
			n++
		}
		return nil
	})
	return n
}

func writer(body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }
}

// The payload of every URI that is set registers a route for the URI, and payloads are set by concurrent calls of the admin
// API, so registering routes has to be safe to do from many goroutines, and not lose any.
func TestBuildURIMatcherConcurrentRegistration(t *testing.T) {
	core := newRouters(t)
	const workers, rounds = 8, 25
	errs := make([]error, workers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			for i := 0; i < rounds; i++ {
				uri := fmt.Sprintf("/w%d/r%d/", w, i)
				if _, _, _, err := BuildURIMatcher(uri, true, writer("handled "+uri)); err != nil {
					errs[w] = err
					return
				}
			}
		}(w)
	}
	close(start)
	wg.Wait()
	for w, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", w, err)
		}
	}
	if got := countRoutes(MatchRouter, true); got != workers*rounds {
		t.Errorf("routes with a handler = %d, want %d, one for every URI", got, workers*rounds)
	}
	for w := 0; w < workers; w++ {
		for i := 0; i < rounds; i++ {
			uri := fmt.Sprintf("/w%d/r%d/", w, i)
			if got := serveBody(core, uri+"x"); got != "handled "+uri {
				t.Fatalf("GET %sx = %q, want %q", uri, got, "handled "+uri)
			}
		}
	}
}

func TestBuildURIMatcherForRouterConcurrentRegistration(t *testing.T) {
	router := mux.NewRouter()
	const workers, rounds = 6, 20
	errs := make([]error, workers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			for i := 0; i < rounds; i++ {
				if _, err := BuildURIMatcherForRouter(fmt.Sprintf("/w%d/r%d", w, i), false, writer("ok"), router); err != nil {
					errs[w] = err
					return
				}
			}
		}(w)
	}
	close(start)
	wg.Wait()
	for w, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", w, err)
		}
	}
	if got := countRoutes(router, true); got != workers*rounds {
		t.Errorf("routes with a handler = %d, want %d", got, workers*rounds)
	}
}

// A payload that is set again for a URI would add one more route for it on every call, and a route that is behind an
// earlier one of the same URI never gets a request.
func TestBuildURIMatcherRegistersAURIOnce(t *testing.T) {
	core := newRouters(t)
	prefix1, re1, router1, err := BuildURIMatcher("/same/{id}", true, writer("first"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		prefix2, re2, router2, err := BuildURIMatcher("/same/{id}", true, writer("later"))
		if err != nil {
			t.Fatal(err)
		}
		if prefix2 != prefix1 || re2.String() != re1.String() || router2 != router1 {
			t.Fatalf("call %d returned (%q, %s, %p), want what the first one did: (%q, %s, %p)", i, prefix2, re2, router2, prefix1, re1, router1)
		}
	}
	if got := countRoutes(MatchRouter, true); got != 1 {
		t.Errorf("routes with a handler after setting the same URI 6 times = %d, want 1", got)
	}
	if got := serveBody(core, "/same/7/x"); got != "first" {
		t.Errorf("GET /same/7/x = %q, want the handler that was registered first", got)
	}
	// Another match of the URI (exact instead of prefix) is another route, but the case of the URI is no difference.
	for _, call := range []struct {
		uri    string
		prefix bool
		routes int
	}{{"/same/{id}", false, 2}, {"/SAME/{id}", true, 2}, {"/SAME/{id}", false, 2}, {"/other", true, 3}} {
		if _, _, _, err := BuildURIMatcher(call.uri, call.prefix, writer("x")); err != nil {
			t.Fatal(err)
		}
		if got := countRoutes(MatchRouter, true); got != call.routes {
			t.Errorf("routes with a handler after BuildURIMatcher(%q, %v) = %d, want %d", call.uri, call.prefix, got, call.routes)
		}
	}
}

func TestBuildURIMatcherForRouterRegistersAURIOncePerRouter(t *testing.T) {
	r1, r2 := mux.NewRouter(), mux.NewRouter()
	for i := 0; i < 3; i++ {
		for _, r := range []*mux.Router{r1, r2} {
			if _, err := BuildURIMatcherForRouter("/x", false, writer("x"), r); err != nil {
				t.Fatal(err)
			}
		}
	}
	if a, b := countRoutes(r1, true), countRoutes(r2, true); a != 1 || b != 1 {
		t.Errorf("routes with a handler = %d and %d, want 1 on each router", a, b)
	}
}

func TestBuildURIMatcherWithoutRouters(t *testing.T) {
	saved := MatchRouter
	MatchRouter = nil
	defer func() { MatchRouter = saved }()
	if _, re, _, err := BuildURIMatcher("/x", true, writer("x")); err == nil || re != nil {
		t.Errorf("BuildURIMatcher without a router = (%v, %v), want an error", re, err)
	}
}

// Working out the target of a proxied request must not leave anything in the router that serves requests.
func TestTransposeURIAddsNoRoutes(t *testing.T) {
	newRouters(t)
	before := countRoutes(MatchRouter, false)
	for i := 0; i < 20; i++ {
		got := TransposeURI("/a/1", "/a/{id}", "/b/{id}", map[string]string{"id": "1"}, nil, nil, nil, nil)
		if got != "/b/1" {
			t.Fatalf("TransposeURI = %q, want /b/1", got)
		}
	}
	if after := countRoutes(MatchRouter, false); after != before {
		t.Errorf("routes of the router that serves requests = %d after transposing URIs, want the %d it had", after, before)
	}
}

func TestTransposeURIWithoutRouters(t *testing.T) {
	saved := MatchRouter
	MatchRouter = nil
	defer func() { MatchRouter = saved }()
	if got := TransposeURI("/a/1", "/a/{id}", "/b/{id}", map[string]string{"id": "1"}, nil, nil, nil, nil); got != "/b/1" {
		t.Errorf("TransposeURI = %q, want /b/1", got)
	}
}

func TestConcurrentTransposeURI(t *testing.T) {
	newRouters(t)
	const workers = 8
	results := make([]string, workers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			for i := 0; i < 30; i++ {
				results[w] = TransposeURI("/a/1/rest", "/a/{id}/*", "/b/{id}/*", map[string]string{"id": "1"}, nil, nil, nil, nil)
			}
		}(w)
	}
	close(start)
	wg.Wait()
	for w, got := range results {
		if got != "/b/1/rest" {
			t.Errorf("worker %d: TransposeURI = %q, want /b/1/rest", w, got)
		}
	}
}

// A URI that is not a prefix matches the paths that are the URI, with or without the query that the request has.
func TestExactURIRegexpMatchesRequestsWithAQuery(t *testing.T) {
	newRouters(t)
	tests := []struct {
		uri     string
		matches []string
		others  []string
	}{
		{"/foo", []string{"/foo", "/FOO", "/foo?x=y", "/Foo?x=y&z=1", "/foo?"}, []string{"/foo/bar", "/foo/bar?x=y", "/foobar", "/foobar?x=y", "/", "/fo", "/bar/foo?x=y"}},
		{"/a/{x}", []string{"/a/1", "/a/1?q=2"}, []string{"/a/1/2", "/a/1/2?q=3", "/a"}},
		{"/a/{x}/b", []string{"/a/1/b", "/a/1/b?q=2"}, []string{"/a/1/b/c", "/a/b", "/a/1/b/c?q=2"}},
	}
	for _, tt := range tests {
		_, fromRegexp, err := BuildURIRegexp(tt.uri, false)
		if err != nil {
			t.Fatalf("BuildURIRegexp(%q): %v", tt.uri, err)
		}
		_, fromMatcher, _, err := BuildURIMatcher(tt.uri, false, writer("x"))
		if err != nil {
			t.Fatalf("BuildURIMatcher(%q): %v", tt.uri, err)
		}
		for name, re := range map[string]interface{ MatchString(string) bool }{"BuildURIRegexp": fromRegexp, "BuildURIMatcher": fromMatcher} {
			for _, uri := range tt.matches {
				if !re.MatchString(uri) {
					t.Errorf("%s(%q, exact) = %s, want it to match %q", name, tt.uri, fromRegexp, uri)
				}
			}
			for _, uri := range tt.others {
				if re.MatchString(uri) {
					t.Errorf("%s(%q, exact) = %s, want it not to match %q", name, tt.uri, fromRegexp, uri)
				}
			}
		}
	}
}

// URIs without a query have the same regexps that they always had, and prefix matches keep matching the paths below them.
func TestURIRegexpsOfURIsWithoutQueries(t *testing.T) {
	newRouters(t)
	tests := []struct {
		uri    string
		prefix bool
		want   string
	}{
		{"/foo", true, `(?i)^/(?P<v0>(?i)foo)(.*)?(\?.*)?$`},
		{"/foo", false, `(?i)^/(?P<v0>(?i)foo)(\?.*)?$`},
		{"/a/{x}", true, `(?i)^/a/(?P<v0>[^/&\?]*)(.*)?(\?.*)?$`},
		{"/a/{x}", false, `(?i)^/a/(?P<v0>[^/&\?]*)(\?.*)?$`},
		{"/foo*", true, `(?i)^/(?P<v0>(?i)foo)(.*)?(.*)?(\?.*)?$`},
	}
	for _, tt := range tests {
		_, re, err := BuildURIRegexp(tt.uri, tt.prefix)
		if err != nil {
			t.Fatalf("BuildURIRegexp(%q, %v): %v", tt.uri, tt.prefix, err)
		}
		if re.String() != tt.want {
			t.Errorf("BuildURIRegexp(%q, %v) = %s, want %s", tt.uri, tt.prefix, re, tt.want)
		}
	}
	for _, uri := range []string{"/foo", "/foo/bar", "/foo/bar?x=y", "/foobar", "/foo?x=y"} {
		_, re, err := BuildURIRegexp("/foo", true)
		if err != nil || !re.MatchString(uri) {
			t.Errorf("prefix match of /foo for %q = (%v, %v), want a match", uri, re, err)
		}
	}
}

// The empty URI matches every request of every port, so a route for it would take over all the traffic, and a route can't be
// taken back from the router.
func TestBuildURIMatcherForTheEmptyURIRegistersNoRoute(t *testing.T) {
	core := newRouters(t)
	before := countRoutes(MatchRouter, true)
	prefix, re, router, err := BuildURIMatcher("", true, writer("taken"))
	if err != nil {
		t.Fatal(err)
	}
	if router != nil || prefix != "" || re == nil {
		t.Errorf("BuildURIMatcher of the empty URI = (%q, %v, %v), want its regexp only", prefix, re, router)
	}
	for _, path := range []string{"/", "/foo", "/foo/bar?x=y"} {
		if !re.MatchString(path) {
			t.Errorf("regexp %s doesn't match %q", re, path)
		}
		if got := serveBody(core, path); got != "real" {
			t.Errorf("GET %s = %q, want %q: the request was taken over", path, got, "real")
		}
	}
	if got := countRoutes(MatchRouter, true); got != before {
		t.Errorf("routes with a handler = %d, want the %d there were", got, before)
	}
}

func TestBuildURIMatcherWhenTakesOverTheRequestsThatMeetTheCondition(t *testing.T) {
	core := newRouters(t)
	var on atomic.Bool
	if _, _, _, err := BuildURIMatcherWhen("/cond", true, writer("handled"), func(*http.Request) bool { return on.Load() }); err != nil {
		t.Fatal(err)
	}
	for i, tt := range []struct {
		on   bool
		path string
		want string
	}{{false, "/cond/x", "real"}, {true, "/cond/x", "handled"}, {true, "/other", "real"}, {false, "/cond/x", "real"}, {true, "/cond", "handled"}} {
		on.Store(tt.on)
		if got := serveBody(core, tt.path); got != tt.want {
			t.Errorf("case %d: GET %s with the condition %v = %q, want %q", i, tt.path, tt.on, got, tt.want)
		}
	}
	// the URI is registered once, with the condition of the first registration
	if _, _, _, err := BuildURIMatcherWhen("/cond", true, writer("later"), nil); err != nil {
		t.Fatal(err)
	}
	on.Store(false)
	if got := serveBody(core, "/cond/x"); got != "real" {
		t.Errorf("GET /cond/x = %q, want %q: the condition of the first registration stays", got, "real")
	}
}
