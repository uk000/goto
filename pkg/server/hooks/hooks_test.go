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

package hooks

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"goto/pkg/global"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
	stubOnce sync.Once
	// Hooks are registered per port and can't be removed, so every test uses a port of its own.
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
	HeaderTrackingFunc = func(int, string, string, map[string][]string) {}
}

// newServerRouting builds the routers the way the server does: the root router with the hooks middleware, which is
// also where the URI matchers of hooks get registered, and a chain router behind it with the handler that really
// serves traffic. It answers "real:<path>".
func newServerRouting(t *testing.T) (http.Handler, int) {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
	core := mux.NewRouter()
	core.SkipClean(true)
	root := util.CreateRouters(core)
	root.Use(Middleware.MiddlewareHandler)
	chain := root.PathPrefix("").Subrouter()
	chain.PathPrefix("/").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "real:%s", r.URL.Path)
	})
	return core, int(30000 + portSeq.Add(1))
}

func serve(server http.Handler, port int, path string, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Add(headers[i], headers[i+1])
	}
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, port))
	_, req, _ = util.WithRequestStore(req, nil)
	rr := httptest.NewRecorder()
	server.ServeHTTP(rr, req)
	return rr
}

func expectServed(t *testing.T, server http.Handler, port int, path string, headers ...string) {
	t.Helper()
	rr := serve(server, port, path, headers...)
	if rr.Code != http.StatusOK || rr.Body.String() != "real:"+path {
		t.Errorf("GET %s = %d %q, want 200 %q: the request was not served by its handler", path, rr.Code, rr.Body.String(), "real:"+path)
	}
}

// listener returns a listener that counts its calls, and lets the request continue.
func listener() (HTTPListener, *atomic.Int32) {
	var calls atomic.Int32
	return func(int, string, map[string][]string, io.Reader) bool {
		calls.Add(1)
		return true
	}, &calls
}

// A hook that only observes requests must not take them over. A route registered for its URI would answer the
// requests that match with an empty response, instead of the handlers that serve them.
func TestHookWithListenerDoesNotSwallowRequests(t *testing.T) {
	server, port := newServerRouting(t)
	l, calls := listener()
	if err := GetPortHooks(port).AddHTTPHookWithListener("k", "id", "/foo", nil, false, l); err != nil {
		t.Fatal(err)
	}
	// Under the URI of the hook, and the URI itself, requests are served and reported to the listener.
	for _, path := range []string{"/foo", "/foo/bar", "/foobar"} {
		expectServed(t, server, port, path)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("listener calls = %d, want 3", got)
	}
	// Other requests are served, and not reported.
	for _, path := range []string{"/other", "/"} {
		expectServed(t, server, port, path)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("listener calls = %d, want requests of other URIs to be left alone", got)
	}
}

// The empty URI matches every request, so a route registered for it would take over all the traffic of the server.
func TestHeaderOnlyHookDoesNotSwallowRequests(t *testing.T) {
	server, port := newServerRouting(t)
	l, calls := listener()
	if err := GetPortHooks(port).AddHTTPHookWithListener("k", "id", "", Headers{{"h1", "v1"}}, false, l); err != nil {
		t.Fatal(err)
	}
	paths := []string{"/plain", "/foo", "/foo/bar", "/"}
	for _, path := range paths {
		expectServed(t, server, port, path)
		expectServed(t, server, port, path, "H1", "v1")
	}
	if got := calls.Load(); got != int32(2*len(paths)) {
		t.Errorf("listener calls = %d, want %d: the hook matches every request", got, 2*len(paths))
	}
}

func TestHookWithURIVariablesDoesNotSwallowRequests(t *testing.T) {
	server, port := newServerRouting(t)
	l, calls := listener()
	if err := GetPortHooks(port).AddHTTPHookWithListener("k", "id", "/a/{x}/b", nil, false, l); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/a/1/b", "/a/1/b/c", "/a/1", "/a", "/b"} {
		expectServed(t, server, port, path)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("listener calls = %d, want 2 (/a/1/b and /a/1/b/c)", got)
	}
}

func TestRegisterURIBuildsTheSameMatcherWithOrWithoutHandler(t *testing.T) {
	_, port := newServerRouting(t)
	h := GetPortHooks(port)
	_, _, reWithout, err := h.registerURI("/Foo/{x}", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, reWith, err := h.registerURI("/Foo/{x}", func(http.ResponseWriter, *http.Request) {})
	if err != nil {
		t.Fatal(err)
	}
	if reWithout.String() != reWith.String() {
		t.Errorf("matcher without a handler = %s, want the same as with one: %s", reWithout, reWith)
	}
}

// A hook that has a handler serves its URI, so it needs its route.
func TestHookWithHandlerKeepsItsRoute(t *testing.T) {
	server, port := newServerRouting(t)
	_, _, _, err := GetPortHooks(port).registerURI("/handled", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("handled"))
	})
	if err != nil {
		t.Fatal(err)
	}
	if rr := serve(server, port, "/handled/x"); rr.Body.String() != "handled" {
		t.Errorf("GET /handled/x = %q, want the handler of the hook to answer", rr.Body.String())
	}
	expectServed(t, server, port, "/other")
}
