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

package request

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/server/middleware"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
	setupOnce sync.Once
	portSeq   atomic.Int32
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

func setup() {
	setupOnce.Do(func() {
		stubGlobalFuncs()
		Middleware.SetRoutes(nil)
	})
}

func newRequest(method, target string, port int) *http.Request {
	setup()
	req := httptest.NewRequest(method, target, nil)
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, port))
	_, req, _ = util.WithRequestStore(req, nil)
	return req
}

func nextPort() int {
	return int(44000 + portSeq.Add(1))
}

func api(root, method, target string, port int) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	middleware.RootRouters[root].ServeHTTP(rr, newRequest(method, target, port))
	return rr
}

// routeTemplates lists the paths that are routed by the router.
func routeTemplates(t *testing.T, r *mux.Router) []string {
	t.Helper()
	var templates []string
	if err := r.Walk(func(route *mux.Route, _ *mux.Router, _ []*mux.Route) error {
		if tpl, err := route.GetPathTemplate(); err == nil {
			templates = append(templates, tpl)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(templates)
	return templates
}

func TestMiddlewareIsNamedAndHasRoutesAndAHandler(t *testing.T) {
	if Middleware.Name != "request" || Middleware.SetRoutes == nil || Middleware.MiddlewareHandler == nil {
		t.Errorf("middleware = %+v, want one named request with routes and a handler", Middleware)
	}
	var core, filters []string
	for _, m := range CoreMiddlewares {
		core = append(core, m.Name)
	}
	for _, m := range requestMiddlewares {
		filters = append(filters, m.Name)
	}
	if fmt.Sprint(core) != "[tracking uri]" || fmt.Sprint(filters) != "[timeout filter]" {
		t.Errorf("core middlewares = %v, request middlewares = %v, want [tracking uri] and [timeout filter]", core, filters)
	}
}

func TestRoutesOfTheMiddlewaresAreUnderBothRoots(t *testing.T) {
	setup()
	for _, root := range []string{"/server", "/request"} {
		router := middleware.RootRouters[root]
		if router == nil {
			t.Fatalf("no router for %s", root)
		}
		templates := routeTemplates(t, router)
		for _, name := range []string{"/track", "/uri", "/timeout", "/ignore", "/bypass"} {
			want := "/request" + name
			if root == "/server" {
				want = "/server" + want
			}
			found := false
			for _, tpl := range templates {
				found = found || tpl == want || strings.HasPrefix(tpl, want+"/")
			}
			if !found {
				t.Errorf("%s router has no routes under %s: %v", root, want, templates)
			}
		}
	}
}

func TestRoutesOfTheMiddlewaresAreServedUnderBothRoots(t *testing.T) {
	for _, tt := range []struct {
		root, prefix string
	}{
		{"/server", "/server/request"},
		{"/request", "/request"},
	} {
		for _, path := range []string{"/timeout", "/timeout/status", "/uri", "/uri/counts", "/ignore", "/ignore/count", "/ignore/status", "/bypass", "/bypass/count"} {
			port := nextPort()
			if rr := api(tt.root, "GET", tt.prefix+path, port); rr.Code != http.StatusOK {
				t.Errorf("GET %s = %d %q, want 200", tt.prefix+path, rr.Code, rr.Body.String())
			}
		}
	}
}

func TestRoutesRefuseWhatIsNotTheirs(t *testing.T) {
	port := nextPort()
	for _, tt := range []struct{ method, target string }{
		{"GET", "/request/nothing-here"},
		{"GET", "/server/request/nothing-here"},
		// The routes are by method too.
		{"GET", "/request/timeout/track/all"},
		{"POST", "/request/timeout/status"},
		{"POST", "/request/uri/counts"},
		{"GET", "/request/ignore/clear"},
	} {
		root := "/" + strings.SplitN(tt.target, "/", 3)[1]
		if rr := api(root, tt.method, tt.target, port); rr.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d %q, want 404", tt.method, tt.target, rr.Code, rr.Body.String())
		}
	}
}

// chain runs the request through the middleware of the package, and tells if the next handler was reached.
func chain(req *http.Request) (rr *httptest.ResponseRecorder, reached bool) {
	rr = httptest.NewRecorder()
	Middleware.MiddlewareHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, "next")
	})).ServeHTTP(rr, req)
	return
}

func TestMiddlewareReachesTheNextHandlerWhenNothingIsFiltered(t *testing.T) {
	port := nextPort()
	rr, reached := chain(newRequest("GET", "/ordinary", port))
	if !reached || rr.Code != http.StatusAccepted || rr.Body.String() != "next" {
		t.Errorf("reached = %t, response = %d %q, want the response of the next handler", reached, rr.Code, rr.Body.String())
	}
}

func TestMiddlewareWithoutANextHandlerDoesNotPanic(t *testing.T) {
	port := nextPort()
	rr := httptest.NewRecorder()
	Middleware.MiddlewareHandler(nil).ServeHTTP(rr, newRequest("GET", "/ordinary", port))
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}

// filterConfig sets a filter up for a test, and has it cleared as the test is done.
func filterConfig(t *testing.T, kind, status string) {
	t.Helper()
	port := nextPort()
	t.Cleanup(func() {
		api("/request", "POST", "/request/"+kind+"/clear", port)
		api("/request", "POST", "/request/"+kind+"/set/status=200", port)
	})
	if rr := api("/request", "POST", "/request/"+kind+"/set/status="+status, port); rr.Code != http.StatusOK {
		t.Fatalf("cannot set the status of the %s filter: %d %q", kind, rr.Code, rr.Body.String())
	}
}

func TestMiddlewareStopsRequestsThatAreIgnored(t *testing.T) {
	filterConfig(t, "ignore", "204")
	port := nextPort()
	if rr := api("/request", "POST", "/request/ignore/add?uri=/ignored.*", port); rr.Code != http.StatusOK {
		t.Fatalf("cannot add the URI: %d %q", rr.Code, rr.Body.String())
	}
	rr, reached := chain(newRequest("GET", "/ignored/path", port))
	if reached || rr.Code != http.StatusNoContent || rr.Header().Get(constants.HeaderGotoFilteredRequest) != "true" {
		t.Errorf("reached = %t, response = %d %v, want it stopped with the status of the filter and its header", reached, rr.Code, rr.Header())
	}
	rr, reached = chain(newRequest("GET", "/not-ignored", port))
	if !reached || rr.Code != http.StatusAccepted {
		t.Errorf("reached = %t, response = %d, want a request that is not ignored to reach the next handler", reached, rr.Code)
	}
}

func TestMiddlewareStopsRequestsThatAreBypassedToo(t *testing.T) {
	filterConfig(t, "bypass", "202")
	port := nextPort()
	if rr := api("/server", "POST", "/server/request/bypass/add?uri=/bypassed", port); rr.Code != http.StatusOK {
		t.Fatalf("cannot add the URI: %d %q", rr.Code, rr.Body.String())
	}
	rr, reached := chain(newRequest("GET", "/bypassed", port))
	if reached || rr.Header().Get(constants.HeaderGotoFilteredRequest) != "true" {
		t.Errorf("reached = %t, response = %d %v, want it stopped by the filter", reached, rr.Code, rr.Header())
	}
}

func TestMiddlewareLeavesNonTrafficAloneWhateverIsFiltered(t *testing.T) {
	filterConfig(t, "ignore", "204")
	port := nextPort()
	if rr := api("/request", "POST", "/request/ignore/add?uri=.*", port); rr.Code != http.StatusOK {
		t.Fatalf("cannot add the URI: %d %q", rr.Code, rr.Body.String())
	}
	for _, target := range []string{"/health", "/metrics", "/remember/x"} {
		req := newRequest("GET", target, port)
		if !util.GetRequestStore(req).IsKnownNonTraffic {
			t.Fatalf("%s is expected to be known as not being traffic", target)
		}
		if rr, reached := chain(req); !reached || rr.Code != http.StatusAccepted {
			t.Errorf("%s: reached = %t, response = %d, want the next handler to be reached", target, reached, rr.Code)
		}
	}
	if _, reached := chain(newRequest("GET", "/anything", port)); reached {
		t.Error("a request that is traffic must be filtered by the filter that matches all")
	}
}

func TestConcurrentRequestsThroughTheMiddleware(t *testing.T) {
	filterConfig(t, "ignore", "204")
	port := nextPort()
	if rr := api("/request", "POST", "/request/ignore/add?uri=/ignored/.*", port); rr.Code != http.StatusOK {
		t.Fatalf("cannot add the URI: %d %q", rr.Code, rr.Body.String())
	}
	const requests = 40
	reached := make([]bool, requests)
	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			target := fmt.Sprintf("/ignored/%d", i)
			if i%2 == 0 {
				target = fmt.Sprintf("/passed/%d", i)
			}
			_, reached[i] = chain(newRequest("GET", target, port))
		}()
	}
	wg.Wait()
	for i, got := range reached {
		if want := i%2 == 0; got != want {
			t.Errorf("request %d: reached = %t, want %t", i, got, want)
		}
	}
}
