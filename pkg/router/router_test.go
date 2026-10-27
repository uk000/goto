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

package router

import (
	"context"
	"encoding/json"
	"fmt"
	"goto/pkg/global"
	"goto/pkg/server/intercept"
	"goto/pkg/util"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gorilla/mux"
)

var (
	stubOnce sync.Once
	portSeq  atomic.Int32
)

func stubGlobalFuncs() {
	global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsListenerPresent = func(int) bool { return false }
	global.Funcs.IsListenerOpen = func(int) bool { return false }
	global.Funcs.GetListenerID = func(int) string { return "" }
	global.Funcs.GetListenerLabel = func(*http.Request) string { return "" }
	global.Funcs.GetListenerLabelForPort = func(int) string { return "" }
}

// productionPrefixRegexp is what the server sets for the URI matchers: an optional /port=N in front of the URI.
func productionPrefixRegexp() string {
	portRouteRegex, _ := mux.NewRouter().PathPrefix("/port={port}").GetPathRegexp()
	return "(?i)(" + portRouteRegex + ")?"
}

// newPort returns a port number that routes of the test are kept under. The routes are dropped when the test ends, and the
// URI matchers are built the way the server builds them.
func newPort(t *testing.T) int {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
	prev, prevWillTunnel := util.RoutePrefixRegexp, util.WillTunnel
	util.RoutePrefixRegexp = productionPrefixRegexp()
	// the tunnel package is what installs this, and it is not part of what is tested here
	util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
	port := int(44000 + portSeq.Add(1))
	t.Cleanup(func() {
		util.RoutePrefixRegexp = prev
		util.WillTunnel = prevWillTunnel
		lock.Lock()
		delete(PortRouters, port)
		lock.Unlock()
	})
	return port
}

// newRequest creates an inbound request for the port, with the request store that the server gives it.
func newRequest(port int, method, target string, body io.Reader, headers ...string) *http.Request {
	req := httptest.NewRequest(method, target, body)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Add(headers[i], headers[i+1])
	}
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, port))
	_, req, _ = util.WithRequestStore(req, nil)
	return req
}

// serveRouted sends the request to the handler that the router has for it, the way the server does.
func serveRouted(t *testing.T, port int, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	h := WillRoute(port, req)
	if h == nil {
		t.Fatalf("no route for %s %s on port %d", req.Method, req.RequestURI, port)
	}
	rr := httptest.NewRecorder()
	w, irw := intercept.WithIntercept(req, rr)
	h.ServeHTTP(w, req)
	irw.Proceed()
	return rr
}

type seenRequest struct {
	method, uri, host string
	headers           http.Header
	body              string
}

type upstream struct {
	srv  *httptest.Server
	mu   sync.Mutex
	reqs []seenRequest
}

// newUpstream starts a server that remembers the requests it gets. What it answers with is decided by the handler.
func newUpstream(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *upstream {
	t.Helper()
	u := &upstream{}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		u.mu.Lock()
		u.reqs = append(u.reqs, seenRequest{r.Method, r.RequestURI, r.Host, r.Header.Clone(), string(b)})
		u.mu.Unlock()
		if handler != nil {
			handler(w, r)
		} else {
			w.Header().Set("X-Upstream", "yes")
			fmt.Fprint(w, "upstream says hi")
		}
	}))
	t.Cleanup(u.srv.Close)
	return u
}

func (u *upstream) addr() string { return u.srv.Listener.Addr().String() }

func (u *upstream) seen() []seenRequest {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]seenRequest(nil), u.reqs...)
}

func newRoute(port int, label, from, to string) *Route {
	return &Route{Label: label, From: RouteFrom{Port: port, URIPrefix: from}, To: RouteTo{URL: to}}
}

// registerRoute registers the route on the router of the port.
func registerRoute(port int, r *Route) *PortRouter {
	pr := GetPortRouter(port)
	pr.AddRoute(r)
	return pr
}

func routeLabels(pr *PortRouter) []string {
	pr.lock.RLock()
	defer pr.lock.RUnlock()
	labels := []string{}
	for l := range pr.Routes {
		labels = append(labels, l)
	}
	return labels
}

func trafficOf(pr *PortRouter) map[string]*RouteTraffic {
	pr.lock.RLock()
	defer pr.lock.RUnlock()
	out := map[string]*RouteTraffic{}
	for k, v := range pr.RouteTraffic {
		cp := *v
		out[k] = &cp
	}
	return out
}

// onlyTraffic returns the only traffic record of the port router.
func onlyTraffic(t *testing.T, pr *PortRouter) (string, *RouteTraffic) {
	t.Helper()
	all := trafficOf(pr)
	if len(all) != 1 {
		t.Fatalf("traffic records = %d, want 1", len(all))
	}
	for id, rt := range all {
		return id, rt
	}
	return "", nil
}

func TestRouteIsValid(t *testing.T) {
	cases := []struct {
		name  string
		route Route
		valid bool
	}{
		{"complete", Route{From: RouteFrom{Port: 8080, URIPrefix: "/a"}, To: RouteTo{URL: "x:80"}}, true},
		{"no port", Route{To: RouteTo{URL: "x:80"}}, false},
		{"negative port", Route{From: RouteFrom{Port: -1}, To: RouteTo{URL: "x:80"}}, false},
		{"no upstream", Route{From: RouteFrom{Port: 8080}}, false},
		{"no URI is a root route", Route{From: RouteFrom{Port: 8080}, To: RouteTo{URL: "x:80"}}, true},
	}
	for _, c := range cases {
		r := c.route
		if got := r.IsValid(); got != c.valid {
			t.Errorf("%s: IsValid() = %v, want %v", c.name, got, c.valid)
		}
	}
	r := Route{From: RouteFrom{Port: 1, URIPrefix: "/from"}, To: RouteTo{URL: "x:80"}}
	r.IsValid()
	if r.To.URIPrefix != "/from" {
		t.Errorf("upstream URI prefix defaults to the one of the request, got %q", r.To.URIPrefix)
	}
	r = Route{From: RouteFrom{Port: 1, URIPrefix: "/from"}, To: RouteTo{URL: "x:80", URIPrefix: "/to"}}
	r.IsValid()
	if r.To.URIPrefix != "/to" {
		t.Errorf("upstream URI prefix is kept, got %q", r.To.URIPrefix)
	}
}

func TestRouteSetup(t *testing.T) {
	newPort(t)
	cases := []struct {
		name       string
		route      Route
		url        string
		basePrefix string
		rootMatch  bool
	}{
		{"host and port", Route{From: RouteFrom{URIPrefix: "/api"}, To: RouteTo{URL: "upstream:8080"}}, "http://upstream:8080", "/api", false},
		{"tls", Route{From: RouteFrom{URIPrefix: "/api"}, To: RouteTo{URL: "upstream:8443", IsTLS: true}}, "https://upstream:8443", "/api", false},
		{"http URL is kept", Route{From: RouteFrom{URIPrefix: "/api"}, To: RouteTo{URL: "http://upstream:8080"}}, "http://upstream:8080", "/api", false},
		{"https URL is kept", Route{From: RouteFrom{URIPrefix: "/api"}, To: RouteTo{URL: "https://upstream:8443"}}, "https://upstream:8443", "/api", false},
		{"no URI is root", Route{To: RouteTo{URL: "upstream:80"}}, "http://upstream:80", "/", true},
		{"slash is root", Route{From: RouteFrom{URIPrefix: "/"}, To: RouteTo{URL: "upstream:80"}}, "http://upstream:80", "/", true},
		{"prefix is lowercased", Route{From: RouteFrom{URIPrefix: "/API/V1"}, To: RouteTo{URL: "upstream:80"}}, "http://upstream:80", "/api/v1", false},
		{"glob", Route{From: RouteFrom{URIPrefix: "/api/*"}, To: RouteTo{URL: "upstream:80"}}, "http://upstream:80", "/api/", false},
		{"path variable", Route{From: RouteFrom{URIPrefix: "/api/{id}/items"}, To: RouteTo{URL: "upstream:80"}}, "http://upstream:80", "/api/", false},
	}
	for _, c := range cases {
		r := c.route
		if err := r.Setup(); err != nil {
			t.Errorf("%s: Setup() failed with %v", c.name, err)
			continue
		}
		if r.To.URL != c.url {
			t.Errorf("%s: URL = %q, want %q", c.name, r.To.URL, c.url)
		}
		if r.basePrefix != c.basePrefix {
			t.Errorf("%s: basePrefix = %q, want %q", c.name, r.basePrefix, c.basePrefix)
		}
		if r.rootMatch != c.rootMatch {
			t.Errorf("%s: rootMatch = %v, want %v", c.name, r.rootMatch, c.rootMatch)
		}
		if r.re == nil || r.client == nil {
			t.Errorf("%s: matcher %v and client %v are set up", c.name, r.re, r.client)
		}
	}
}

func TestRouteSetupMatcher(t *testing.T) {
	newPort(t)
	r := newRoute(1, "r", "/api/v1", "upstream:80")
	if err := r.Setup(); err != nil {
		t.Fatal(err)
	}
	for uri, want := range map[string]bool{
		"/api/v1":             true,
		"/api/v1/":            true,
		"/api/v1/x/y":         true,
		"/api/v1?a=b":         true,
		"/api/v1/x?a=b&c=d":   true,
		"/API/V1/x":           true,
		"/port=8080/api/v1/x": true,
		"/api/v2":             false,
		"/other":              false,
		"/":                   false,
		"":                    false,
	} {
		if got := r.re.MatchString(uri); got != want {
			t.Errorf("matcher of /api/v1 on %q = %v, want %v", uri, got, want)
		}
	}
	root := newRoute(1, "root", "", "upstream:80")
	if err := root.Setup(); err != nil {
		t.Fatal(err)
	}
	for _, uri := range []string{"/", "/a", "/a/b?c=d", "/port=1/a"} {
		if !root.re.MatchString(uri) {
			t.Errorf("root route doesn't match %q", uri)
		}
	}
}

func TestRouteWillReroute(t *testing.T) {
	newPort(t)
	r := newRoute(1, "r", "/api", "upstream:80")
	if err := r.Setup(); err != nil {
		t.Fatal(err)
	}
	for uri, want := range map[string]bool{
		"/api":        true,
		"/api/x":      true,
		"/api?x=1":    true,
		"/apiary":     true,
		"/x/api":      false,
		"/ap":         false,
		"/":           false,
		"":            false,
		"/port=1/api": false,
	} {
		req := &http.Request{RequestURI: uri}
		if got := r.WillReroute(req); got != want {
			t.Errorf("WillReroute(%q) = %v, want %v", uri, got, want)
		}
	}
}

// The prefix of a route is matched in any case, as the regexp of the route is, so a request that is matched to a route
// is also rerouted by it.
func TestRouteWillRerouteInAnyCase(t *testing.T) {
	newPort(t)
	for _, from := range []string{"/api", "/API", "/Api"} {
		r := newRoute(1, "r", from, "upstream:80")
		if err := r.Setup(); err != nil {
			t.Fatal(err)
		}
		for uri, want := range map[string]bool{
			"/api/x": true,
			"/API/x": true,
			"/aPi":   true,
			"/ap":    false,
			"/x/API": false,
		} {
			if got := r.WillReroute(&http.Request{RequestURI: uri}); got != want {
				t.Errorf("route from %q: WillReroute(%q) = %v, want %v", from, uri, got, want)
			}
			// The regexp is not anchored, which is why a route is also asked if it will reroute: it has to match
			// what is rerouted.
			if want && !r.re.MatchString(uri) {
				t.Errorf("route from %q: the regexp doesn't match %q, which is rerouted", from, uri)
			}
		}
	}
}

// Routes are kept by label, which Go iterates in no order, and the request goes to the one with the longest prefix, and
// to the first label of those with the same one, in every run.
func TestGetMatchingRouteIsTheOneWithTheLongestPrefix(t *testing.T) {
	port := newPort(t)
	pr := GetPortRouter(port)
	pr.AddRoute(newRoute(port, "short", "/api", "a:80"))
	pr.AddRoute(newRoute(port, "long", "/api/v1", "b:80"))
	pr.AddRoute(newRoute(port, "longest", "/api/v1/admin", "c:80"))
	pr.AddRoute(newRoute(port, "same-b", "/same", "d:80"))
	pr.AddRoute(newRoute(port, "same-a", "/SAME", "e:80"))
	cases := map[string]string{
		"/api":                "short",
		"/api/x":              "short",
		"/api/v1":             "long",
		"/api/v1/x?a=1":       "long",
		"/api/v1/admin/users": "longest",
		"/API/V1/ADMIN":       "longest",
		"/same/x":             "same-a",
		"/SAME":               "same-a",
	}
	for i := 0; i < 50; i++ {
		for uri, want := range cases {
			route := pr.GetMatchingRoute(&http.Request{RequestURI: uri})
			if route == nil || route.Label != want {
				got := "<none>"
				if route != nil {
					got = route.Label
				}
				t.Fatalf("round %d: route for %q = %s, want %s", i, uri, got, want)
			}
		}
	}
}

// The routes are looked up while they are added and cleared by the admin calls.
func TestGetMatchingRouteWhileRoutesChange(t *testing.T) {
	port := newPort(t)
	pr := GetPortRouter(port)
	pr.AddRoute(newRoute(port, "base", "/base", "a:80"))
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				pr.AddRoute(newRoute(port, fmt.Sprintf("r%d-%d", i, j), fmt.Sprintf("/r%d/%d", i, j), "b:80"))
			}
		}(i)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				pr.GetMatchingRoute(&http.Request{RequestURI: "/base/x"})
			}
		}()
	}
	wg.Wait()
}

func TestGetPortRouter(t *testing.T) {
	port := newPort(t)
	pr := GetPortRouter(port)
	if pr == nil || pr.Port != port || pr.ID != fmt.Sprintf("PortRouter-%d", port) {
		t.Fatalf("port router = %+v", pr)
	}
	if pr.Routes == nil || pr.RouteTraffic == nil || pr.proxy == nil || pr.intercept == nil ||
		pr.requestHandler == nil || pr.responseHandler == nil || pr.requestResponseHandler == nil {
		t.Errorf("port router isn't completely set up: %+v", pr)
	}
	if GetPortRouter(port) != pr {
		t.Error("the router of a port is created once")
	}
	if GetPortRouter(port+1000) == pr {
		t.Error("ports have routers of their own")
	}
	lock.Lock()
	delete(PortRouters, port+1000)
	lock.Unlock()
}

func TestAddRouteAndClear(t *testing.T) {
	port := newPort(t)
	pr := GetPortRouter(port)
	r1 := newRoute(port, "one", "/one", "upstream:80")
	r2 := newRoute(port, "two", "/two", "upstream:80")
	r2.To.RequestHeaders = &Headers{Remove: map[string]string{"X-Remove-Me": "", "Authorization": "Bearer x"}}
	r2.To.ResponseHeaders = &Headers{Remove: map[string]string{"Set-Cookie": ""}}
	pr.AddRoute(r1)
	pr.AddRoute(r2)
	if len(pr.Routes) != 2 || pr.Routes["one"] != r1 || pr.Routes["two"] != r2 {
		t.Fatalf("routes = %v", routeLabels(pr))
	}
	if r1.re == nil || r1.handler == nil {
		t.Errorf("route isn't ready to be used: %+v", r1)
	}
	if _, ok := r2.To.RequestHeaders.Remove["x-remove-me"]; !ok || len(r2.To.RequestHeaders.Remove) != 2 {
		t.Errorf("request headers to remove are lowercased: %v", r2.To.RequestHeaders.Remove)
	}
	if _, ok := r2.To.ResponseHeaders.Remove["set-cookie"]; !ok {
		t.Errorf("response headers to remove are lowercased: %v", r2.To.ResponseHeaders.Remove)
	}

	// a route that is added under a label that is taken replaces the route
	r3 := newRoute(port, "one", "/uno", "other:80")
	pr.AddRoute(r3)
	if len(pr.Routes) != 2 || pr.Routes["one"] != r3 {
		t.Errorf("route that was added again didn't replace the first one: %v", routeLabels(pr))
	}

	if h := http.Handler(pr.proxy); r1.handler != h {
		t.Error("a route that needs no processing is served by the proxy itself")
	}
	pr.AddRoute(&Route{Label: "req", From: RouteFrom{Port: port, URIPrefix: "/req"}, To: RouteTo{URL: "u:80"}, ProcessReq: true})
	pr.AddRoute(&Route{Label: "resp", From: RouteFrom{Port: port, URIPrefix: "/resp"}, To: RouteTo{URL: "u:80"}, ProcessResp: true})
	pr.AddRoute(&Route{Label: "both", From: RouteFrom{Port: port, URIPrefix: "/both"}, To: RouteTo{URL: "u:80"}, ProcessReq: true, ProcessResp: true})
	for _, label := range []string{"req", "resp", "both"} {
		if h := pr.Routes[label].handler; h == nil || h == http.Handler(pr.proxy) {
			t.Errorf("route %s with processing is served by %v, want a handler that wraps the proxy", label, h)
		}
	}

	pr.AddRouteTraffic("1", r1, newRequest(port, "GET", "/one", nil))
	if pr.GetRouteTraffic("1") == nil {
		t.Fatal("traffic isn't recorded")
	}
	pr.Clear()
	if len(pr.Routes) != 0 || len(pr.RouteTraffic) != 0 {
		t.Errorf("after Clear: routes %v, traffic %v", routeLabels(pr), pr.RouteTraffic)
	}
	if pr.GetRouteTraffic("1") != nil {
		t.Error("traffic is cleared too")
	}
	pr.AddRoute(r1)
	if len(pr.Routes) != 1 {
		t.Error("routes can be added after Clear")
	}
}

func TestInitDropsAllRouters(t *testing.T) {
	port := newPort(t)
	registerRoute(port, newRoute(port, "x", "/x", "upstream:80"))
	RequestCorrelationID.Store(41)
	Init()
	lock.RLock()
	n := len(PortRouters)
	lock.RUnlock()
	if n != 0 {
		t.Errorf("port routers after Init = %d", n)
	}
	if RequestCorrelationID.Load() != 0 {
		t.Errorf("correlation ID after Init = %d", RequestCorrelationID.Load())
	}
	if pr := GetPortRouter(port); len(pr.Routes) != 0 {
		t.Errorf("the router that is created after Init has routes %v", routeLabels(pr))
	}
}

func TestGetMatchingRoute(t *testing.T) {
	port := newPort(t)
	pr := GetPortRouter(port)
	pr.AddRoute(newRoute(port, "api", "/api", "a:80"))
	pr.AddRoute(newRoute(port, "other", "/other/", "b:80"))
	for uri, want := range map[string]string{
		"/api":               "api",
		"/api/v1/things?a=1": "api",
		"/other/x":           "other",
		"/port=1/api":        "",
		"/nothing":           "",
		"/":                  "",
		"/oth":               "",
	} {
		route := pr.GetMatchingRoute(&http.Request{RequestURI: uri})
		got := ""
		if route != nil {
			got = route.Label
		}
		if got != want {
			t.Errorf("route for %q = %q, want %q", uri, got, want)
		}
	}
	pr.Clear()
	if pr.GetMatchingRoute(&http.Request{RequestURI: "/api"}) != nil {
		t.Error("no route is found after Clear")
	}
}

func TestGetMatchingRouteForRootRoute(t *testing.T) {
	port := newPort(t)
	pr := GetPortRouter(port)
	pr.AddRoute(newRoute(port, "root", "/", "a:80"))
	for _, uri := range []string{"/", "/anything", "/a/b/c?d=e"} {
		if route := pr.GetMatchingRoute(&http.Request{RequestURI: uri}); route == nil || route.Label != "root" {
			t.Errorf("route for %q = %v, want the root route", uri, route)
		}
	}
}

func TestWillRoute(t *testing.T) {
	port := newPort(t)
	other := newPort(t)
	registerRoute(port, newRoute(port, "api", "/api", "a:80"))
	if h := WillRoute(port, newRequest(port, "GET", "/api/x", nil)); h == nil {
		t.Error("no handler for a URI that has a route")
	}
	if h := WillRoute(port, newRequest(port, "GET", "/web", nil)); h != nil {
		t.Errorf("handler for a URI that has no route: %v", h)
	}
	if h := WillRoute(other, newRequest(other, "GET", "/api/x", nil)); h != nil {
		t.Errorf("a route of a port is used on another port: %v", h)
	}
}

func TestPrepareRequestRewritesURI(t *testing.T) {
	port := newPort(t)
	cases := []struct {
		name         string
		from, to     string
		uri, wantURI string
	}{
		{"same prefix", "/api", "", "/api/x?y=1", "/api/x?y=1"},
		{"other prefix", "/api", "/v2", "/api/x?y=1", "/v2/x?y=1"},
		{"prefix alone", "/api", "/v2", "/api", "/v2"},
		{"to the root of upstream", "/api/v1", "/", "/api/v1/x", "/x"},
		{"to the root of upstream with query", "/api/v1", "/", "/api/v1?a=b", "/?a=b"},
		{"root to a prefix", "/", "/v2", "/api", "/v2/api"},
		{"no from prefix to a prefix", "", "/v2", "/api/x", "/v2/api/x"},
		{"prefix with slash", "/api/", "/v2", "/api/x", "/v2/x"},
		{"both with slash", "/api/", "/v2/", "/api/x", "/v2/x"},
		{"longer upstream prefix", "/a", "/internal/a/b", "/a/c", "/internal/a/b/c"},
		{"prefix appears again", "/a", "/b", "/a/a/a", "/b/a/a"},
		{"upper case request", "/api", "/v2", "/API/x", "/v2/x"},
		{"upper case route", "/API", "/v2", "/api/x", "/v2/x"},
	}
	for _, c := range cases {
		r := newRoute(port, "r", c.from, "upstream:80")
		r.To.URIPrefix = c.to
		if err := r.Setup(); err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		req, _, err := r.prepareRequest(newRequest(port, "GET", c.uri, nil), "7")
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got := req.URL.RequestURI(); got != c.wantURI {
			t.Errorf("%s: from %q to %q, %q went upstream as %q, want %q", c.name, c.from, c.to, c.uri, got, c.wantURI)
		}
		if req.URL.Host != "upstream:80" || req.URL.Scheme != "http" {
			t.Errorf("%s: upstream URL = %s", c.name, req.URL)
		}
	}
}

func TestPrepareRequest(t *testing.T) {
	port := newPort(t)
	r := newRoute(port, "r", "/api", "upstream:80")
	r.To.RequestHeaders = &Headers{
		Add:    map[string]string{"X-Added": "yes", "X-Replaced": "new"},
		Remove: util.ToLowerHeader(map[string]string{"Authorization": "", "X-Only-With-Value": "secret"}),
	}
	if err := r.Setup(); err != nil {
		t.Fatal(err)
	}
	in := newRequest(port, "POST", "/api/things", strings.NewReader("the payload"),
		"Authorization", "Bearer abc", "X-Only-With-Value", "secret", "X-Kept", "k1", "X-Kept", "k2", "X-Replaced", "old", "Content-Type", "text/plain")
	in.Host = "downstream.example"
	req, rs, err := r.prepareRequest(in, "99")
	if err != nil {
		t.Fatal(err)
	}
	if rs == nil || rs != util.GetRequestStore(in) {
		t.Errorf("request store of the inbound request is returned, got %v", rs)
	}
	if req.Method != "POST" || req.ContentLength != int64(len("the payload")) {
		t.Errorf("method %s, content length %d", req.Method, req.ContentLength)
	}
	if b, _ := io.ReadAll(req.Body); string(b) != "the payload" {
		t.Errorf("body = %q", b)
	}
	if got := req.Header.Get("X-Correlation-ID"); got != "99" {
		t.Errorf("correlation ID = %q", got)
	}
	if req.Header.Get("Authorization") != "" || req.Header.Get("X-Only-With-Value") != "" {
		t.Errorf("headers that are to be removed went upstream: %v", req.Header)
	}
	if got := req.Header.Values("X-Kept"); len(got) != 2 || got[0] != "k1" || got[1] != "k2" {
		t.Errorf("X-Kept = %v", got)
	}
	if req.Header.Get("X-Added") != "yes" || req.Header.Get("X-Replaced") != "new" {
		t.Errorf("headers to add: %v", req.Header)
	}
	if req.Header.Get("Content-Type") != "text/plain" {
		t.Errorf("content type = %q", req.Header.Get("Content-Type"))
	}
	if req.Host != "upstream:80" {
		t.Errorf("host = %q, want the one of the upstream URL", req.Host)
	}
	if in.Header.Get("Authorization") != "Bearer abc" || in.Header.Get("X-Replaced") != "old" {
		t.Errorf("inbound request was changed: %v", in.Header)
	}

	r.To.Authority = "virtual.host:1234"
	req, _, err = r.prepareRequest(newRequest(port, "GET", "/api", nil), "100")
	if err != nil {
		t.Fatal(err)
	}
	if req.Host != "virtual.host:1234" {
		t.Errorf("host with an authority = %q", req.Host)
	}
	if req.URL.Host != "upstream:80" {
		t.Errorf("authority doesn't change where the request goes: %s", req.URL)
	}

	// a header that is to be removed only for a value stays for other values
	req, _, _ = r.prepareRequest(newRequest(port, "GET", "/api", nil, "X-Only-With-Value", "public", "authorization", "x"), "101")
	if req.Header.Get("X-Only-With-Value") != "public" || req.Header.Get("Authorization") != "" {
		t.Errorf("headers = %v", req.Header)
	}
}

func TestPrepareRequestFailsForAnInvalidUpstream(t *testing.T) {
	port := newPort(t)
	r := newRoute(port, "r", "/api", "http://bad host:80")
	if err := r.Setup(); err != nil {
		t.Fatal(err)
	}
	if req, rs, err := r.prepareRequest(newRequest(port, "GET", "/api", nil), "1"); err == nil || req != nil || rs != nil {
		t.Errorf("request to an invalid URL: %v %v %v", req, rs, err)
	}
}

func TestPrepareHeaders(t *testing.T) {
	cases := []struct {
		name      string
		in        http.Header
		out       http.Header
		overrides *Headers
		want      http.Header
	}{
		{"copy", http.Header{"A": {"1"}, "B": {"2", "3"}}, http.Header{}, nil, http.Header{"A": {"1"}, "B": {"2", "3"}}},
		{"existing header of out wins", http.Header{"A": {"1"}}, http.Header{"A": {"keep"}}, nil, http.Header{"A": {"keep"}}},
		{"remove any value", http.Header{"A": {"1"}, "B": {"2"}}, http.Header{}, &Headers{Remove: map[string]string{"a": ""}}, http.Header{"B": {"2"}}},
		{"remove matching value", http.Header{"A": {"Secret"}, "B": {"2"}}, http.Header{}, &Headers{Remove: map[string]string{"a": "secret"}}, http.Header{"B": {"2"}}},
		{"keep other value", http.Header{"A": {"public"}}, http.Header{}, &Headers{Remove: map[string]string{"a": "secret"}}, http.Header{"A": {"public"}}},
		{"add", http.Header{"A": {"1"}}, http.Header{}, &Headers{Add: map[string]string{"C": "3"}}, http.Header{"A": {"1"}, "C": {"3"}}},
		{"add replaces", http.Header{"A": {"1"}}, http.Header{}, &Headers{Add: map[string]string{"A": "new"}}, http.Header{"A": {"new"}}},
		{"add after remove", http.Header{"A": {"1"}}, http.Header{}, &Headers{Remove: map[string]string{"a": ""}, Add: map[string]string{"A": "new"}}, http.Header{"A": {"new"}}},
		{"nothing", http.Header{}, http.Header{}, &Headers{}, http.Header{}},
	}
	for _, c := range cases {
		prepareHeaders(c.in, c.out, c.overrides)
		if got, want := util.ToJSONText(c.out), util.ToJSONText(c.want); got != want {
			t.Errorf("%s: headers = %s, want %s", c.name, got, want)
		}
	}
	// the headers of a response are edited in place
	h := http.Header{"A": {"1"}, "B": {"2"}}
	prepareHeaders(h, h, &Headers{Remove: map[string]string{"a": ""}, Add: map[string]string{"C": "3"}})
	if util.ToJSONText(h) != util.ToJSONText(http.Header{"B": {"2"}, "C": {"3"}}) {
		t.Errorf("headers edited in place = %v", h)
	}
}

func TestRouteJSON(t *testing.T) {
	in := `{"label":"l","from":{"port":7000,"uriPrefix":"/api/v1"},"to":{"url":"up:8080","uriPrefix":"/","authority":"a:1",
		"requestHeaders":{"add":{"X-A":"1"},"remove":{"Authorization":""}},"responseHeaders":{"add":{"X-B":"2"},"remove":{"Set-Cookie":""}},
		"http2":true,"tls":true,"noSNI":true},"logHops":true,"logBody":true,"processReq":true,"processResp":true}`
	var r Route
	if err := json.Unmarshal([]byte(in), &r); err != nil {
		t.Fatal(err)
	}
	if r.Label != "l" || r.From.Port != 7000 || r.From.URIPrefix != "/api/v1" || r.To.URL != "up:8080" || r.To.URIPrefix != "/" ||
		r.To.Authority != "a:1" || r.To.RequestHeaders.Add["X-A"] != "1" || r.To.ResponseHeaders.Remove["Set-Cookie"] != "" ||
		!r.To.IsH2 || !r.To.IsTLS || !r.To.NoSNI || !r.LogHops || !r.LogBody || !r.ProcessReq || !r.ProcessResp {
		t.Errorf("route = %+v", r)
	}
	out := util.ToJSONText(&r)
	for _, part := range []string{`"label":"l"`, `"uriPrefix":"/api/v1"`, `"url":"up:8080"`, `"noSNI":true`, `"processResp":true`} {
		if !strings.Contains(out, part) {
			t.Errorf("JSON of the route lacks %s: %s", part, out)
		}
	}
}
