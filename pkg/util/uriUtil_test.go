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
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
)

var buildURIRegexpURIs = []string{"", "/", "/foo", "/Foo/Bar", "/foo*", "/Foo*", "/a/{x}", "/a/{x}/b/{y}*", "/a/{x}?"}

// newRouters creates the routers the way the server does, and returns the core router that serves requests, with a
// handler on a chain router behind the root router, like the handlers that serve traffic.
func newRouters(t *testing.T) *mux.Router {
	t.Helper()
	core := mux.NewRouter()
	core.SkipClean(true)
	root := CreateRouters(core)
	chain := root.PathPrefix("").Subrouter()
	chain.PathPrefix("/").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("real"))
	})
	return core
}

func serveBody(router http.Handler, path string) string {
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest("GET", path, nil))
	return rr.Body.String()
}

func TestBuildURIRegexpMatchesWhatBuildURIMatcherBuilds(t *testing.T) {
	newRouters(t)
	for _, uri := range buildURIRegexpURIs {
		for _, prefix := range []bool{true, false} {
			wantPrefix, want, _, err := BuildURIMatcher(uri, prefix, func(http.ResponseWriter, *http.Request) {})
			if err != nil {
				t.Fatalf("BuildURIMatcher(%q): %v", uri, err)
			}
			gotPrefix, got, err := BuildURIRegexp(uri, prefix)
			if err != nil {
				t.Fatalf("BuildURIRegexp(%q): %v", uri, err)
			}
			if got.String() != want.String() {
				t.Errorf("BuildURIRegexp(%q, %v) regexp = %s, want %s", uri, prefix, got, want)
			}
			if gotPrefix != wantPrefix {
				t.Errorf("BuildURIRegexp(%q, %v) prefix = %q, want %q", uri, prefix, gotPrefix, wantPrefix)
			}
		}
	}
}

func TestBuildURIRegexpMatchesURIs(t *testing.T) {
	tests := []struct {
		uri     string
		matches []string
		others  []string
	}{
		{"", []string{"/", "/foo", "/foo/bar?x=y"}, nil},
		{"/foo", []string{"/foo", "/FOO", "/foo/bar", "/foobar", "/foo?x=y"}, []string{"/", "/fo", "/bar/foo"}},
		{"/foo*", []string{"/foo", "/foo/bar"}, []string{"/bar"}},
		{"/a/{x}/b", []string{"/a/1/b", "/a/1/b/c"}, []string{"/a/b", "/b"}},
	}
	for _, tt := range tests {
		_, re, err := BuildURIRegexp(tt.uri, true)
		if err != nil {
			t.Fatalf("BuildURIRegexp(%q): %v", tt.uri, err)
		}
		for _, uri := range tt.matches {
			if !re.MatchString(uri) {
				t.Errorf("regexp of %q = %s, want it to match %q", tt.uri, re, uri)
			}
		}
		for _, uri := range tt.others {
			if re.MatchString(uri) {
				t.Errorf("regexp of %q = %s, want it not to match %q", tt.uri, re, uri)
			}
		}
	}
}

func TestBuildURIRegexpRegistersNoRoute(t *testing.T) {
	core := newRouters(t)
	for _, uri := range buildURIRegexpURIs {
		if _, _, err := BuildURIRegexp(uri, true); err != nil {
			t.Fatalf("BuildURIRegexp(%q): %v", uri, err)
		}
	}
	for _, path := range []string{"/", "/foo", "/Foo/Bar", "/foo/bar", "/foobar", "/a/1", "/a/1/b/2", "/other"} {
		if got := serveBody(core, path); got != "real" {
			t.Errorf("GET %s = %q, want %q: the request was taken over by a route of a matcher", path, got, "real")
		}
	}
}

func TestBuildURIMatcherRegistersLiveRoute(t *testing.T) {
	// Callers that pass a handler rely on the route to serve their URI.
	core := newRouters(t)
	if _, _, _, err := BuildURIMatcher("/handled", true, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("handled"))
	}); err != nil {
		t.Fatal(err)
	}
	if got := serveBody(core, "/handled/x"); got != "handled" {
		t.Errorf("GET /handled/x = %q, want %q", got, "handled")
	}
	if got := serveBody(core, "/other"); got != "real" {
		t.Errorf("GET /other = %q, want %q", got, "real")
	}
}

func TestBuildURIRegexpRejectsInvalidURI(t *testing.T) {
	newRouters(t)
	if _, re, err := BuildURIRegexp("/a/{x", true); err == nil || re != nil {
		t.Errorf("BuildURIRegexp of an unterminated variable = (%v, %v), want an error", re, err)
	}
}
