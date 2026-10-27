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

package tunnel

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	. "goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/server/intercept"
	"goto/pkg/server/listeners"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

const ioTimeout = 5 * time.Second

var (
	stubOnce sync.Once
	// Tunnels are kept per port, so every test uses a port of its own.
	portSeq atomic.Int32

	routerOnce sync.Once
	testRouter *mux.Router
)

func stubGlobalFuncs() {
	global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsListenerPresent = func(int) bool { return false }
	global.Funcs.IsListenerOpen = func(int) bool { return false }
	global.Funcs.GetListenerID = func(int) string { return "" }
	global.Funcs.GetListenerLabel = func(*http.Request) string { return "" }
	global.Funcs.GetListenerLabelForPort = func(int) string { return "" }
	// util.WillTunnel is what the package installs, and the tests need it
}

// newPort returns a port number that the tunnels of the test are kept under, which are dropped when the test ends.
func newPort(t *testing.T) int {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
	port := int(43000 + portSeq.Add(1))
	trackPort(t, port)
	return port
}

func trackPort(t *testing.T, port int) {
	t.Cleanup(func() {
		tunnelLock.Lock()
		delete(tunnels, port)
		tunnelLock.Unlock()
	})
}

func newRequest(port int, method, target string, body io.Reader, headers ...string) *http.Request {
	req := httptest.NewRequest(method, target, body)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Add(headers[i], headers[i+1])
	}
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, port))
	_, req, _ = util.WithRequestStore(req, nil)
	return req
}

// serve sends the request through the handler the way the server does, with the response intercepted.
func serve(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	w, irw := intercept.WithIntercept(req, rr)
	h.ServeHTTP(w, req)
	irw.Proceed()
	return rr
}

func local() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprintf(w, "local:%s", r.RequestURI) })
}

// chain is the tunnel middlewares as the server uses them, in front of a handler that answers on its own.
func chain() http.Handler {
	return TunnelCountMiddleware.MiddlewareHandler(Middleware.MiddlewareHandler(local()))
}

// router has the route of the tunnel= URIs, with the tunnel middlewares in front of it.
func router() *mux.Router {
	routerOnce.Do(func() {
		testRouter = mux.NewRouter()
		Middleware.SetRoutes(testRouter)
		testRouter.Use(TunnelCountMiddleware.MiddlewareHandler, Middleware.MiddlewareHandler)
	})
	return testRouter
}

type seenRequest struct {
	method, uri, host string
	headers           http.Header
	body              string
}

type upstream struct {
	srv  *httptest.Server
	name string
	mu   sync.Mutex
	reqs []seenRequest
}

// newUpstream starts a server that answers every request with the status and body, and remembers the requests.
func newUpstream(t *testing.T, name string, status int, body string) *upstream {
	t.Helper()
	u := &upstream{name: name}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		u.mu.Lock()
		u.reqs = append(u.reqs, seenRequest{r.Method, r.RequestURI, r.Host, r.Header.Clone(), string(b)})
		u.mu.Unlock()
		w.Header().Set("X-Upstream", name)
		w.WriteHeader(status)
		fmt.Fprint(w, body)
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

func TestNewEndpoint(t *testing.T) {
	cases := []struct {
		address         string
		tls, transp     bool
		id, url, addr   string
		host            string
		port            int
		isTLS, isH2     bool
		useRequestProto bool
	}{
		{"svc:8080", false, false, "http:svc:8080", "http://svc:8080", "svc:8080", "svc", 8080, false, false, true},
		{"svc:8443", true, true, "https:svc:8443", "https://svc:8443", "svc:8443", "svc", 8443, true, false, true},
		{"svc", false, false, "http:svc", "http://svc", "svc", "svc", 80, false, false, true},
		{"http:svc:8080", true, false, "http:svc:8080", "http://svc:8080", "svc:8080", "svc", 8080, false, false, false},
		{"https:svc:8443", false, false, "https:svc:8443", "https://svc:8443", "svc:8443", "svc", 8443, true, false, false},
		{"h2:svc:8443", false, true, "h2:svc:8443", "https://svc:8443", "svc:8443", "svc", 8443, true, true, false},
		{"h2c:svc:8080", true, false, "h2c:svc:8080", "http://svc:8080", "svc:8080", "svc", 8080, false, true, false},
		{"127.0.0.1:9000", false, false, "http:127.0.0.1:9000", "http://127.0.0.1:9000", "127.0.0.1:9000", "127.0.0.1", 9000, false, false, true},
	}
	for _, c := range cases {
		ep := newEndpoint(c.address, c.tls, c.transp)
		if ep.ID != c.id || ep.URL != c.url || ep.Address != c.addr || ep.Host != c.host || ep.Port != c.port ||
			ep.IsTLS != c.isTLS || ep.IsH2 != c.isH2 || ep.UseRequestProto != c.useRequestProto || ep.Transparent != c.transp {
			t.Errorf("newEndpoint(%q, %v, %v) = %+v", c.address, c.tls, c.transp, ep)
		}
	}
}

func TestGetTunnelID(t *testing.T) {
	for _, c := range []struct {
		h2, h2c, tls bool
		want         string
	}{
		{false, false, false, "http:host:80"},
		{false, false, true, "https:host:80"},
		{true, false, true, "h2:host:80"},
		{false, true, false, "h2c:host:80"},
		{true, true, true, "h2:host:80"},
	} {
		if got := GetTunnelID(c.h2, c.h2c, c.tls, "host:80"); got != c.want {
			t.Errorf("GetTunnelID(%v, %v, %v) = %q, want %q", c.h2, c.h2c, c.tls, got, c.want)
		}
	}
}

func TestGetOrCreatePortTunnel(t *testing.T) {
	port := newPort(t)
	pt := GetOrCreatePortTunnel(port)
	if pt.Port != port || pt.Tunnels == nil || pt.ProxyTunnels == nil || pt.URITunnels == nil || pt.HeaderTunnels == nil ||
		pt.URIHeaderTunnels == nil || pt.Traffic == nil || len(pt.BroadTunnels) != 0 || pt.TunnelTrackingHeaders == nil {
		t.Errorf("new port tunnel = %+v", pt)
	}
	if GetOrCreatePortTunnel(port) != pt {
		t.Error("the tunnel of a port is created once")
	}
	trackPort(t, port+1000)
	if GetOrCreatePortTunnel(port+1000) == pt {
		t.Error("ports have tunnels of their own")
	}
}

func TestAddAndRemoveTunnels(t *testing.T) {
	pt := newPortTunnel(newPort(t))
	a, b := "http:a:80", "http:b:80"
	pt.addTunnel("a:80", false, false, "", "", "")
	pt.addTunnel("b:80", false, false, "", "", "")
	pt.addTunnel("a:80", false, false, "/uri", "", "")
	pt.addTunnel("b:80", false, false, "", "X-H", "v1")
	pt.addTunnel("a:80", false, false, "", "X-H", "")
	pt.addTunnel("b:80", false, false, "/uri", "X-U", "v2")
	if len(pt.Tunnels) != 2 || pt.Tunnels[a] == nil || pt.Tunnels[b] == nil {
		t.Errorf("tunnels = %v", pt.Tunnels)
	}
	if got := strings.Join(pt.BroadTunnels, ","); got != a+","+b {
		t.Errorf("broad = %q", got)
	}
	if got := pt.URITunnels["/uri"]; len(got) != 1 || got[0] != a {
		t.Errorf("by URI = %v", pt.URITunnels)
	}
	if pt.HeaderTunnels["X-H"]["v1"][0] != b || pt.HeaderTunnels["X-H"][""][0] != a {
		t.Errorf("by header = %v", pt.HeaderTunnels)
	}
	if pt.URIHeaderTunnels["/uri"]["X-U"]["v2"][0] != b {
		t.Errorf("by URI and header = %v", pt.URIHeaderTunnels)
	}

	if !pt.removeTunnel(a, "", "", "") {
		t.Error("removing a broad tunnel should find it")
	}
	if got := strings.Join(pt.BroadTunnels, ","); got != b {
		t.Errorf("broad after removal = %q", got)
	}
	if pt.removeTunnel(a, "", "", "") {
		t.Error("removing a tunnel that is gone reports that nothing was removed")
	}
	if !pt.removeTunnel(a, "/uri", "", "") || len(pt.URITunnels) != 0 {
		t.Errorf("by URI after removal = %v", pt.URITunnels)
	}
	if !pt.removeTunnel(b, "", "X-H", "v1") || pt.HeaderTunnels["X-H"]["v1"] != nil || pt.HeaderTunnels["X-H"][""] == nil {
		t.Errorf("by header after removal = %v", pt.HeaderTunnels)
	}
	if !pt.removeTunnel("", "", "X-H", "") || len(pt.HeaderTunnels) != 0 {
		t.Errorf("a removal without an endpoint removes all of the value: %v", pt.HeaderTunnels)
	}
	if !pt.removeTunnel(b, "/uri", "X-U", "v2") || len(pt.URIHeaderTunnels) != 0 {
		t.Errorf("by URI and header after removal = %v", pt.URIHeaderTunnels)
	}
	pt.addTunnel("a:80", false, false, "", "", "")
	if !pt.removeTunnel("", "", "", "") || len(pt.BroadTunnels) != 0 {
		t.Errorf("a removal without anything removes all the broad tunnels: %v", pt.BroadTunnels)
	}
	pt.addTunnel("a:80", false, false, "/uri", "", "")
	pt.addTunnel("b:80", false, false, "/uri", "", "")
	if !pt.removeTunnel("", "/uri", "", "") || len(pt.URITunnels) != 0 {
		t.Errorf("a removal of a URI without an endpoint removes all of them: %v", pt.URITunnels)
	}
}

func TestRemoveTunnelOfOneEndpointKeepsTheOthers(t *testing.T) {
	pt := newPortTunnel(newPort(t))
	for _, e := range []string{"a:80", "b:80", "c:80"} {
		pt.addTunnel(e, false, false, "/u", "H", "v")
		pt.addTunnel(e, false, false, "/u", "", "")
		pt.addTunnel(e, false, false, "", "H", "v")
		pt.addTunnel(e, false, false, "", "", "")
	}
	pt.removeTunnel("http:b:80", "/u", "H", "v")
	pt.removeTunnel("http:b:80", "/u", "", "")
	pt.removeTunnel("http:b:80", "", "H", "v")
	pt.removeTunnel("http:b:80", "", "", "")
	want := "http:a:80,http:c:80"
	for name, got := range map[string][]string{
		"uri+header": pt.URIHeaderTunnels["/u"]["H"]["v"], "uri": pt.URITunnels["/u"],
		"header": pt.HeaderTunnels["H"]["v"], "broad": pt.BroadTunnels,
	} {
		if strings.Join(got, ",") != want {
			t.Errorf("%s tunnels = %v, want %s", name, got, want)
		}
	}
}

func TestCheckTunnelsForRequest(t *testing.T) {
	port := newPort(t)
	pt := newPortTunnel(port)
	get := func(uri string, headers ...string) (bool, []string) {
		r := httptest.NewRequest("GET", uri, nil)
		for i := 0; i+1 < len(headers); i += 2 {
			r.Header.Add(headers[i], headers[i+1])
		}
		return pt.checkTunnelsForRequest(r)
	}
	if ok, eps := get("/x"); ok || eps != nil {
		t.Errorf("no tunnels = %v %v", ok, eps)
	}
	if ok, eps := get("/tunnel=a:80/x"); !ok || eps != nil {
		t.Errorf("tunnel= URI always tunnels, to the addresses it carries: %v %v", ok, eps)
	}
	if ok, eps := get("/x", HeaderGotoTunnel, "a:80"); !ok || eps != nil {
		t.Errorf("Goto-Tunnel header always tunnels: %v %v", ok, eps)
	}
	if ok, eps := get("/x", HeaderProxyConnection, "keep-alive"); !ok || eps != nil {
		t.Errorf("Proxy-Connection header always tunnels: %v %v", ok, eps)
	}

	pt.addTunnel("broad:80", false, false, "", "", "")
	pt.addTunnel("header:80", false, false, "", "X-Route", "blue")
	pt.addTunnel("anyvalue:80", false, false, "", "X-Any", "")
	pt.addTunnel("uri:80", false, false, "/api", "", "")
	pt.addTunnel("uriheader:80", false, false, "/api", "X-Route", "red")

	cases := []struct {
		name    string
		uri     string
		headers []string
		want    string
	}{
		{"URI and header", "/api", []string{"X-Route", "red"}, "http:uriheader:80"},
		{"URI when the header doesn't match", "/api", []string{"X-Route", "other"}, "http:uri:80"},
		{"URI", "/api", nil, "http:uri:80"},
		{"header value", "/other", []string{"X-Route", "blue"}, "http:header:80"},
		{"any value of a header", "/other", []string{"X-Any", "whatever"}, "http:anyvalue:80"},
		{"header without a value", "/other", []string{"X-Any", ""}, "http:anyvalue:80"},
		{"broad when nothing else matches", "/other", []string{"X-Route", "green"}, "http:broad:80"},
		{"broad", "/other", nil, "http:broad:80"},
		{"URI of another case isn't the URI", "/API", nil, "http:broad:80"},
	}
	for _, c := range cases {
		ok, eps := get(c.uri, c.headers...)
		if !ok || strings.Join(eps, ",") != c.want {
			t.Errorf("%s: = %v %v, want %s", c.name, ok, eps, c.want)
		}
	}
	pt.removeTunnel("", "", "", "")
	if ok, eps := get("/other"); ok || len(eps) != 0 {
		t.Errorf("without the broad tunnel = %v %v", ok, eps)
	}
	if ok, eps := get("/other", "X-Route", "green"); ok || len(eps) != 0 {
		t.Errorf("a header value that has no tunnel = %v %v", ok, eps)
	}
}

func TestWillTunnel(t *testing.T) {
	port := newPort(t)
	r := newRequest(port, "GET", "/some/uri", nil)
	rs := util.GetRequestStore(r)
	if WillTunnel(r, rs) || rs.IsTunnelRequest {
		t.Fatal("a port without tunnels doesn't tunnel")
	}
	pt := GetOrCreatePortTunnel(port)
	pt.addTunnel("a:80", false, false, "/some/uri", "", "")
	r = newRequest(port, "GET", "/some/uri", nil)
	rs = util.GetRequestStore(r)
	if !rs.IsTunnelRequest {
		t.Error("the request store should know that the request is tunneled")
	}
	if eps, ok := rs.TunnelEndpoints.([]string); !ok || len(eps) != 1 || eps[0] != "http:a:80" {
		t.Errorf("tunnel endpoints = %#v", rs.TunnelEndpoints)
	}
	r = newRequest(port, "GET", "/not/tunneled", nil)
	if util.IsTunnelRequest(r) {
		t.Error("another URI isn't tunneled")
	}
	if !util.IsTunnelRequest(newRequest(port, "GET", "/tunnel=a:80/x", nil)) {
		t.Error("a tunnel= URI is tunneled")
	}
	if util.IsTunnelRequest(newRequest(port, "GET", "/tunnels", nil)) {
		t.Error("the API for tunnels is not tunneled")
	}
	if util.IsTunnelRequest(newRequest(port+1, "GET", "/some/uri", nil)) {
		t.Error("tunnels are per port")
	}
}

func TestCheckTunnelRequest(t *testing.T) {
	port := newPort(t)
	t.Run("connect", func(t *testing.T) {
		r := newRequest(port, "CONNECT", "example.com:443", nil)
		CheckTunnelRequest(r)
		if rs := util.GetRequestStore(r); !rs.IsTunnelConnectRequest || rs.IsTunnelRequest {
			t.Errorf("CONNECT: %+v", rs)
		}
	})
	t.Run("goto tunnel header", func(t *testing.T) {
		r := newRequest(port, "GET", "/x", nil, HeaderGotoTunnel, "a:80,b:80", HeaderGotoTunnel, "c:80")
		CheckTunnelRequest(r)
		rs := util.GetRequestStore(r)
		if !rs.IsTunnelRequest || strings.Join(rs.RequestedTunnels, "|") != "a:80,b:80|c:80" {
			t.Errorf("requested tunnels = %v, tunnel request %v", rs.RequestedTunnels, rs.IsTunnelRequest)
		}
		if r.RequestURI != "/x" {
			t.Errorf("RequestURI = %q", r.RequestURI)
		}
	})
	t.Run("proxy connection", func(t *testing.T) {
		r := newRequest(port, "GET", "http://example.com/a/b", nil, HeaderProxyConnection, "keep-alive")
		r.Host = "example.com"
		CheckTunnelRequest(r)
		rs := util.GetRequestStore(r)
		if !rs.IsTunnelRequest || strings.Join(rs.RequestedTunnels, "|") != "http:example.com" {
			t.Errorf("requested tunnels = %v, tunnel request %v", rs.RequestedTunnels, rs.IsTunnelRequest)
		}
		if r.RequestURI != "/a/b" {
			t.Errorf("an absolute URI becomes a path: %q", r.RequestURI)
		}
	})
	t.Run("proxy connection to all", func(t *testing.T) {
		r := newRequest(port, "OPTIONS", "*", nil, HeaderProxyConnection, "keep-alive")
		CheckTunnelRequest(r)
		if r.RequestURI != "" {
			t.Errorf("* becomes an empty URI: %q", r.RequestURI)
		}
	})
	t.Run("not a tunnel", func(t *testing.T) {
		r := newRequest(port, "GET", "/x?y=1", nil)
		CheckTunnelRequest(r)
		if rs := util.GetRequestStore(r); rs.IsTunnelRequest || len(rs.RequestedTunnels) != 0 || r.RequestURI != "/x?y=1" {
			t.Errorf("a plain request: %+v, URI %q", rs, r.RequestURI)
		}
	})
	t.Run("request of an open proxy tunnel", func(t *testing.T) {
		pt := GetOrCreatePortTunnel(port)
		pt.lock.Lock()
		pt.ProxyTunnels["10.1.1.1:5555"] = &ProxyTunnel{FromAddress: "10.1.1.1:5000", ToAddress: "dest.example:443", IsTLS: true}
		pt.lock.Unlock()
		r := newRequest(port, "GET", "/in/the/tunnel", nil)
		r.RemoteAddr = "10.1.1.1:5555"
		CheckTunnelRequest(r)
		rs := util.GetRequestStore(r)
		if !rs.IsTunnelRequest || strings.Join(rs.RequestedTunnels, "|") != "https:dest.example:443" {
			t.Errorf("requested tunnels = %v, tunnel request %v", rs.RequestedTunnels, rs.IsTunnelRequest)
		}
	})
}

func TestTunnelCountMiddleware(t *testing.T) {
	port := newPort(t)
	var got int
	h := TunnelCountMiddleware.MiddlewareHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = util.GetTunnelCount(r) }))
	h.ServeHTTP(httptest.NewRecorder(), newRequest(port, "GET", "/tunnel=a:80/x", nil, HeaderGotoViaTunnelCount, "2"))
	if got != 3 {
		t.Errorf("tunnel count of a tunneled request = %d, want one more than the count it carries", got)
	}
	h.ServeHTTP(httptest.NewRecorder(), newRequest(port, "GET", "/plain", nil, HeaderGotoViaTunnelCount, "2"))
	if got != 0 {
		t.Errorf("tunnel count of a request that isn't tunneled = %d", got)
	}
	if TunnelCountHandler(nil) == nil {
		t.Error("a handler without a next one is still a handler")
	}
	TunnelCountHandler(nil).ServeHTTP(httptest.NewRecorder(), newRequest(port, "GET", "/tunnel=a:80/x", nil))
}

func TestTunnelByURIPrefix(t *testing.T) {
	port := newPort(t)
	up := newUpstream(t, "one", http.StatusCreated, "upstream-body")
	req := newRequest(port, "GET", "/tunnel="+up.addr()+"/some/path?x=1", nil, "X-Custom", "abc")
	rr := serve(router(), req)
	if rr.Code != http.StatusCreated || rr.Body.String() != "upstream-body" || rr.Header().Get("X-Upstream") != "one" {
		t.Fatalf("answered %d %q, headers %v", rr.Code, rr.Body.String(), rr.Header())
	}
	seen := up.seen()
	if len(seen) != 1 || seen[0].uri != "/some/path?x=1" || seen[0].method != "GET" || seen[0].headers.Get("X-Custom") != "abc" {
		t.Fatalf("upstream saw %+v", seen)
	}
	if seen[0].headers.Get(HeaderGotoViaTunnelCount) == "" {
		t.Errorf("the tunnel count is passed on: %v", seen[0].headers)
	}
	if seen[0].headers.Get(HeaderGotoTunnelHost) != "" || seen[0].headers.Get(HeaderViaGotoTunnel) != "" {
		t.Errorf("a transparent tunnel doesn't add its own headers: %v", seen[0].headers)
	}
}

func TestTunnelPostWithBody(t *testing.T) {
	port := newPort(t)
	up := newUpstream(t, "one", http.StatusOK, "ok")
	rr := serve(router(), newRequest(port, "POST", "/tunnel="+up.addr()+"/post", strings.NewReader("payload"), "Content-Type", "text/plain"))
	if rr.Code != http.StatusOK {
		t.Fatalf("answered %d %q", rr.Code, rr.Body.String())
	}
	if seen := up.seen(); len(seen) != 1 || seen[0].method != "POST" || seen[0].body != "payload" {
		t.Errorf("upstream saw %+v", seen)
	}
}

func TestTunnelConfiguredOnPort(t *testing.T) {
	port := newPort(t)
	up := newUpstream(t, "configured", http.StatusAccepted, "configured-body")
	GetOrCreatePortTunnel(port).addTunnel(up.addr(), false, false, "", "", "")

	rr := serve(chain(), newRequest(port, "GET", "/anything?q=2", nil))
	if rr.Code != http.StatusAccepted || rr.Body.String() != "configured-body" {
		t.Fatalf("answered %d %q", rr.Code, rr.Body.String())
	}
	seen := up.seen()
	if len(seen) != 1 || seen[0].uri != "/anything?q=2" {
		t.Fatalf("upstream saw %+v", seen)
	}
	l := listeners.GetCurrentListener(newRequest(port, "GET", "/", nil))
	// The count of a tunneled request is the count that it came with plus one
	if got := seen[0].headers.Get(HeaderGotoTunnelHost); got != fmt.Sprintf("%s|1", l.HostLabel) {
		t.Errorf("%s = %q", HeaderGotoTunnelHost, got)
	}
	if got := seen[0].headers.Get(HeaderViaGotoTunnel); got != fmt.Sprintf("%s|1", l.Label) {
		t.Errorf("%s = %q", HeaderViaGotoTunnel, got)
	}
}

func TestTunnelConfiguredTransparent(t *testing.T) {
	port := newPort(t)
	up := newUpstream(t, "transparent", http.StatusOK, "ok")
	GetOrCreatePortTunnel(port).addTunnel(up.addr(), false, true, "", "", "")
	rr := serve(chain(), newRequest(port, "GET", "/t", nil, "Goto-Something-Tunnel-Related", "x", "X-Keep", "y"))
	if rr.Code != http.StatusOK {
		t.Fatalf("answered %d", rr.Code)
	}
	h := up.seen()[0].headers
	if h.Get(HeaderGotoTunnelHost) != "" || h.Get(HeaderViaGotoTunnel) != "" || h.Get("Goto-Something-Tunnel-Related") != "" || h.Get("X-Keep") != "y" {
		t.Errorf("a transparent tunnel hides itself from the endpoint: %v", h)
	}
}

func TestTunnelSelectsEndpointByHeaderAndURI(t *testing.T) {
	port := newPort(t)
	blue := newUpstream(t, "blue", http.StatusOK, "blue-body")
	green := newUpstream(t, "green", http.StatusOK, "green-body")
	api := newUpstream(t, "api", http.StatusOK, "api-body")
	pt := GetOrCreatePortTunnel(port)
	pt.addTunnel(blue.addr(), false, false, "", "X-Route", "blue")
	pt.addTunnel(green.addr(), false, false, "", "X-Route", "green")
	pt.addTunnel(api.addr(), false, false, "/api", "", "")
	h := chain()

	for header, want := range map[string]string{"blue": "blue-body", "green": "green-body"} {
		if rr := serve(h, newRequest(port, "GET", "/x", nil, "X-Route", header)); rr.Body.String() != want {
			t.Errorf("X-Route %s answered %q, want %q", header, rr.Body.String(), want)
		}
	}
	if rr := serve(h, newRequest(port, "GET", "/api", nil)); rr.Body.String() != "api-body" {
		t.Errorf("/api answered %q", rr.Body.String())
	}
	if rr := serve(h, newRequest(port, "GET", "/x", nil)); rr.Body.String() != "local:/x" {
		t.Errorf("a request without a tunnel is served locally, got %q", rr.Body.String())
	}
	if rr := serve(h, newRequest(port, "GET", "/x", nil, "X-Route", "red")); rr.Body.String() != "local:/x" {
		t.Errorf("a header value without a tunnel is served locally, got %q", rr.Body.String())
	}
}

func TestTunnelByGotoTunnelHeader(t *testing.T) {
	port := newPort(t)
	up := newUpstream(t, "one", http.StatusOK, "via-header")
	req := newRequest(port, "GET", "/h?z=1", nil, HeaderGotoTunnel, up.addr())
	CheckTunnelRequest(req)
	rr := serve(chain(), req)
	if rr.Body.String() != "via-header" {
		t.Fatalf("answered %d %q", rr.Code, rr.Body.String())
	}
	seen := up.seen()
	if len(seen) != 1 || seen[0].uri != "/h?z=1" {
		t.Fatalf("upstream saw %+v", seen)
	}
	if seen[0].headers.Get(HeaderGotoTunnel) != "" {
		t.Errorf("the tunnel header that was acted upon isn't passed on: %v", seen[0].headers)
	}
}

func TestTunnelChainPassesTheRestOfTheTunnelsOn(t *testing.T) {
	port := newPort(t)
	first := newUpstream(t, "first", http.StatusOK, "first-body")
	req := newRequest(port, "GET", "/h", nil, HeaderGotoTunnel, first.addr()+",next:80,last:80")
	CheckTunnelRequest(req)
	serve(chain(), req)
	seen := first.seen()
	if len(seen) != 1 {
		t.Fatalf("first endpoint saw %+v", seen)
	}
	if got := seen[0].headers.Get(HeaderGotoTunnel); got != "next:80,last:80" {
		t.Errorf("the endpoint is given the remaining tunnels: %q", got)
	}
}

func TestTunnelToUnreachableEndpoint(t *testing.T) {
	port := newPort(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	rr := serve(router(), newRequest(port, "GET", "/tunnel="+addr+"/x", nil))
	if !strings.Contains(rr.Body.String(), "Error invoking tunnel to [http://"+addr+"/x]") {
		t.Errorf("answered %d %q", rr.Code, rr.Body.String())
	}
}

func TestTunnelWithoutEndpoints(t *testing.T) {
	port := newPort(t)
	pt := GetOrCreatePortTunnel(port)
	r := newRequest(port, "GET", "/x", nil)
	rr := httptest.NewRecorder()
	pt.tunnel(nil, "/x", r, rr)
	if rr.Body.Len() != 0 {
		t.Errorf("answered %q", rr.Body.String())
	}
}

func TestTunnelTracking(t *testing.T) {
	port := newPort(t)
	up := newUpstream(t, "one", http.StatusTeapot, "tea")
	pt := GetOrCreatePortTunnel(port)
	pt.addTunnel(up.addr(), false, false, "", "", "")
	pt.addTracking([]string{"X-Track"}, []string{"q"})
	pt.captureTunnelTraffic(true)
	for i := 0; i < 2; i++ {
		serve(chain(), newRequest(port, "GET", "/Tracked?q=v&other=1", nil, "X-Track", "t1"))
	}
	id := "http:" + up.addr()
	pt.lock.RLock()
	defer pt.lock.RUnlock()
	c := pt.Traffic
	if c.ByEndpoints[id] != 2 || c.ByURIs["/tracked?q=v&other=1"] != 2 || c.ByURIEndpoints["/tracked?q=v&other=1"][id] != 2 {
		t.Errorf("counts by endpoint and URI = %v %v %v", c.ByEndpoints, c.ByURIs, c.ByURIEndpoints)
	}
	if c.ByRequestHeaders["X-Track"] != 2 || c.ByRequestHeaderValues["X-Track"]["t1"] != 2 || c.ByRequestHeaderEndpoints["X-Track"][id] != 2 {
		t.Errorf("counts by request header = %v %v", c.ByRequestHeaders, c.ByRequestHeaderValues)
	}
	if c.ByRequestQueries["q"] != 2 || c.ByRequestQueryValues["q"]["v"] != 2 {
		t.Errorf("counts by request query = %v %v", c.ByRequestQueries, c.ByRequestQueryValues)
	}
	if c.ByResponseStatus[http.StatusTeapot] != 2 {
		t.Errorf("counts by response status = %v", c.ByResponseStatus)
	}
	if len(c.Logs) != 2 || c.Logs[0].RequestURI != "/Tracked?q=v&other=1" || len(c.Logs[0].ResponseHeaders) != 1 ||
		c.Logs[0].ResponseHeaders[0].Get("X-Upstream") != "one" || c.Logs[0].ResponsePayloadLengths[0] != 3 {
		t.Errorf("traffic log = %+v", c.Logs)
	}
}

func TestTunnelTrafficIsNotLoggedUnlessCaptured(t *testing.T) {
	port := newPort(t)
	up := newUpstream(t, "one", http.StatusOK, "ok")
	pt := GetOrCreatePortTunnel(port)
	pt.addTunnel(up.addr(), false, false, "", "", "")
	serve(chain(), newRequest(port, "GET", "/x", nil))
	pt.lock.RLock()
	defer pt.lock.RUnlock()
	if len(pt.Traffic.Logs) != 0 || pt.Traffic.ByEndpoints["http:"+up.addr()] != 1 {
		t.Errorf("logs = %v, by endpoints = %v", pt.Traffic.Logs, pt.Traffic.ByEndpoints)
	}
}

func TestPipeCallbacks(t *testing.T) {
	port := newPort(t)
	up := newUpstream(t, "one", http.StatusCreated, "piped")
	id := "http:" + up.addr()
	type call struct {
		ep, source string
		port, code int
		body       string
	}
	calls := make(chan call, 2)
	RegisterPipeCallback(id, "pipe1", func(epID, source string, p int, r *http.Request, status int, h http.Header, body io.ReadCloser) {
		b, _ := io.ReadAll(body)
		calls <- call{epID, source, p, status, string(b)}
	})
	t.Cleanup(func() {
		tunnelLock.Lock()
		delete(pipeCallbacksByTunnels, id)
		tunnelLock.Unlock()
	})
	serve(router(), newRequest(port, "GET", "/tunnel="+up.addr()+"/x", nil))
	select {
	case c := <-calls:
		if c.ep != id || c.source != "pipe1" || c.port != port || c.code != http.StatusCreated {
			t.Errorf("callback = %+v", c)
		}
	case <-time.After(ioTimeout):
		t.Fatal("callback wasn't invoked")
	}
}

func TestRegisterPipeCallbackReplacesPerPipe(t *testing.T) {
	var n1, n2 atomic.Int32
	t.Cleanup(func() {
		tunnelLock.Lock()
		delete(pipeCallbacksByTunnels, "t1")
		tunnelLock.Unlock()
	})
	RegisterPipeCallback("t1", "p", func(string, string, int, *http.Request, int, http.Header, io.ReadCloser) { n1.Add(1) })
	RegisterPipeCallback("t1", "p", func(string, string, int, *http.Request, int, http.Header, io.ReadCloser) { n2.Add(1) })
	RegisterPipeCallback("t1", "q", func(string, string, int, *http.Request, int, http.Header, io.ReadCloser) {})
	tunnelLock.RLock()
	defer tunnelLock.RUnlock()
	if len(pipeCallbacksByTunnels["t1"]) != 2 {
		t.Errorf("callbacks = %v", pipeCallbacksByTunnels["t1"])
	}
	pipeCallbacksByTunnels["t1"]["p"]("", "", 0, nil, 0, nil, nil)
	if n1.Load() != 0 || n2.Load() != 1 {
		t.Errorf("registering a callback for the same tunnel and pipe replaces the previous one: %d %d", n1.Load(), n2.Load())
	}
}

// selfServer stands for the server of the port: it takes the connections that a proxy tunnel opens to the server itself.
type selfServer struct {
	l     net.Listener
	conns chan net.Conn
}

func newSelfServer(t *testing.T) *selfServer {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	setPodIP(t, "127.0.0.1")
	s := &selfServer{l: l, conns: make(chan net.Conn, 8)}
	trackPort(t, s.port())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			s.conns <- c
		}
	}()
	t.Cleanup(func() {
		l.Close()
		wg.Wait()
		close(s.conns)
		for c := range s.conns {
			c.Close()
		}
	})
	return s
}

// setPodIP sets the address that the tunnels connect to for the server itself, until the test ends.
func setPodIP(t *testing.T, ip string) {
	t.Helper()
	old := global.Self.PodIP
	global.Self.PodIP = ip
	t.Cleanup(func() { global.Self.PodIP = old })
}

func (s *selfServer) port() int { return s.l.Addr().(*net.TCPAddr).Port }

func (s *selfServer) accept(t *testing.T) net.Conn {
	t.Helper()
	select {
	case c := <-s.conns:
		_ = c.SetDeadline(time.Now().Add(ioTimeout))
		return c
	case <-time.After(ioTimeout):
		t.Fatal("the tunnel didn't connect to the server")
		return nil
	}
}

func pipeConns(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() })
	_ = a.SetDeadline(time.Now().Add(ioTimeout))
	_ = b.SetDeadline(time.Now().Add(ioTimeout))
	return a, b
}

func readExactly(t *testing.T, c net.Conn, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := io.ReadFull(c, b); err != nil {
		t.Fatalf("reading %d bytes: %v", n, err)
	}
	return string(b)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(ioTimeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (pt *PortTunnel) proxyTunnelCount() int {
	pt.lock.RLock()
	defer pt.lock.RUnlock()
	return len(pt.ProxyTunnels)
}

func TestOpenProxyTunnelPipesDataAndEnds(t *testing.T) {
	self := newSelfServer(t)
	pt := GetOrCreatePortTunnel(self.port())
	clientSide, remote := pipeConns(t)
	if !pt.openProxyTunnel("10.0.0.9:4000", "dest.example:443", false, false, true, clientSide) {
		t.Fatal("tunnel didn't open")
	}
	server := self.accept(t)
	defer server.Close()
	waitFor(t, "the tunnel to be registered", func() bool { return pt.proxyTunnelCount() == 1 })

	pt.lock.RLock()
	registered := pt.ProxyTunnels[server.RemoteAddr().String()]
	pt.lock.RUnlock()
	if registered == nil || registered.FromAddress != "10.0.0.9:4000" || registered.ToAddress != "dest.example:443" || !registered.IsTLS || registered.IsH2 {
		t.Fatalf("proxy tunnels = %+v", pt.ProxyTunnels)
	}

	go remote.Write([]byte("ping"))
	if got := readExactly(t, server, 4); got != "ping" {
		t.Errorf("the server received %q", got)
	}
	go server.Write([]byte("pong"))
	if got := readExactly(t, remote, 4); got != "pong" {
		t.Errorf("the client received %q", got)
	}

	// A request that arrives from the tunnel's own connection is for the destination of the tunnel
	r := newRequest(self.port(), "GET", "/inside", nil)
	r.RemoteAddr = server.RemoteAddr().String()
	CheckTunnelRequest(r)
	if rs := util.GetRequestStore(r); !rs.IsTunnelRequest || strings.Join(rs.RequestedTunnels, "|") != "https:dest.example:443" {
		t.Errorf("requested tunnels = %v", rs.RequestedTunnels)
	}

	remote.Close()
	waitFor(t, "the tunnel to be unregistered once the client is gone", func() bool { return pt.proxyTunnelCount() == 0 })
	if _, err := server.Read(make([]byte, 1)); err == nil {
		t.Error("the connection to the server must have been closed")
	}
}

func TestOpenProxyTunnelWithoutServer(t *testing.T) {
	stubOnce.Do(stubGlobalFuncs)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	setPodIP(t, "127.0.0.1")
	trackPort(t, port)
	clientSide, _ := pipeConns(t)
	if newPortTunnel(port).openProxyTunnel("a:1", "b:2", false, false, false, clientSide) {
		t.Error("a tunnel can't open without the server to connect to")
	}
}

// hijackable is a response writer that lets the connection be taken over, as the one of the server does.
type hijackable struct {
	*httptest.ResponseRecorder
	conn    net.Conn
	flushed bool
}

func (h *hijackable) Hijack() (net.Conn, *bufio.ReadWriter, error) { return h.conn, nil, nil }
func (h *hijackable) Flush()                                       { h.flushed = true }

func TestHijackConnect(t *testing.T) {
	self := newSelfServer(t)
	clientSide, remote := pipeConns(t)
	w := &hijackable{ResponseRecorder: httptest.NewRecorder(), conn: clientSide}
	r := newRequest(self.port(), "CONNECT", "dest.example:443", nil)
	r.Host = "dest.example:443"
	CheckTunnelRequest(r)
	if !HijackConnect(r, w) {
		t.Fatal("HijackConnect failed")
	}
	if !w.flushed {
		t.Error("the response is flushed before the connection is taken over")
	}
	server := self.accept(t)
	defer server.Close()
	go remote.Write([]byte("hi"))
	if got := readExactly(t, server, 2); got != "hi" {
		t.Errorf("the server received %q", got)
	}
	pt := GetOrCreatePortTunnel(self.port())
	waitFor(t, "the tunnel to be registered", func() bool { return pt.proxyTunnelCount() == 1 })
	pt.lock.RLock()
	for _, p := range pt.ProxyTunnels {
		if p.ToAddress != "dest.example:443" {
			t.Errorf("tunnel to %q", p.ToAddress)
		}
	}
	pt.lock.RUnlock()
}

func TestHijackConnectOfWriterThatCantHijack(t *testing.T) {
	r := newRequest(newPort(t), "CONNECT", "dest.example:443", nil)
	if HijackConnect(r, httptest.NewRecorder()) {
		t.Error("a writer that can't be hijacked can't be tunneled")
	}
}

// errList collects the failures of the goroutines of a test, which report them from the goroutine of the test.
type errList struct {
	mu   sync.Mutex
	msgs []string
}

func (e *errList) addf(format string, args ...any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.msgs = append(e.msgs, fmt.Sprintf(format, args...))
}

func (e *errList) report(t *testing.T) {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, m := range e.msgs {
		t.Error(m)
	}
}
