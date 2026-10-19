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

package tcpproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"goto/pkg/server/listeners"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"sigs.k8s.io/yaml"
)

var routesOnce sync.Once

// router returns the router that serves the admin APIs of the proxy. The routes are mounted on the router that the
// middleware package keeps for the /proxy path, once for the process.
func router() *mux.Router {
	routesOnce.Do(func() {
		setup()
		Middleware.SetRoutes(nil)
	})
	return middleware.RootRouters["/proxy"]
}

// apiPort takes a port for a test to configure through the APIs, and cleans up what the test leaves behind.
func apiPort(t *testing.T) int {
	t.Helper()
	port := int(52000 + portSeq.Add(1))
	t.Cleanup(func() {
		proxyLock.Lock()
		delete(portProxy, port)
		proxyLock.Unlock()
	})
	return port
}

// call serves a request the way the server does: the request has a store, and arrives at a listener, which is the
// port that the APIs act on unless the request names one.
func call(t *testing.T, listenerPort int, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	return callWith(t, listenerPort, 0, method, target, body)
}

func callWith(t *testing.T, listenerPort, requestPort int, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := router()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, listenerPort))
	_, req, rs := util.WithRequestStore(req, nil)
	if requestPort > 0 {
		// what the /port={port} prefix of a URI does
		rs.RequestPort = strconv.Itoa(requestPort)
		rs.RequestPortNum = requestPort
		rs.RequestPortChecked = true
	}
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	return rr
}

func decode(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("response is not json: %v\n%s", err, rr.Body.String())
	}
	return got
}

const goodUpstreams = `{"web": {"endpoints": {"ep1": {"address": "localhost:8081"}, "ep2": {"address": "localhost:8082"}},
	"match": {"sni": "a.example.com"}, "retries": 1, "dropPct": 10, "delay": {"min": "1ms", "max": "2ms"}}}`

func TestMiddlewareDefinition(t *testing.T) {
	if Middleware.Name != "tcpproxy" || Middleware.SetRoutes == nil {
		t.Errorf("middleware = %+v", Middleware)
	}
	if Middleware.MiddlewareHandler != nil {
		t.Error("the proxy only has admin APIs, and no middleware for the traffic")
	}
}

func TestAddUpstreamsAPI(t *testing.T) {
	port := apiPort(t)
	for _, method := range []string{"POST", "PUT"} {
		rr := call(t, port, method, "/proxy/tcp/upstreams/add", goodUpstreams)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, body %s", method, rr.Code, rr.Body.String())
		}
		if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "yaml") {
			t.Errorf("%s: content type = %q", method, ct)
		}
		// The response is the upstreams as added, as YAML
		var echoed map[string]map[string]any
		if err := yaml.Unmarshal(rr.Body.Bytes(), &echoed); err != nil {
			t.Fatalf("%s: response is not yaml: %v\n%s", method, err, rr.Body.String())
		}
		if echoed["web"]["name"] != "web" || echoed["web"]["retries"] != 1.0 {
			t.Errorf("%s: response = %v", method, echoed)
		}
		p := GetPortProxy(port)
		ups := p.getUpstreams()
		if len(ups) != 1 || ups["web"] == nil || ups["web"].Name != "web" || len(ups["web"].Endpoints) != 2 || ups["web"].proxyPort != port {
			t.Errorf("%s: upstreams = %+v", method, ups)
		}
		if !p.hasSNITargets() {
			t.Errorf("%s: the sni match must have been set up", method)
		}
	}
}

func TestAddUpstreamsAPIReplacesUpstreams(t *testing.T) {
	port := apiPort(t)
	call(t, port, "POST", "/proxy/tcp/upstreams/add", goodUpstreams)
	rr := call(t, port, "POST", "/proxy/tcp/upstreams/add", `{"db": {"endpoints": {"e": {"address": "localhost:5432"}}}}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	ups := GetPortProxy(port).getUpstreams()
	if len(ups) != 1 || ups["db"] == nil {
		t.Errorf("upstreams = %+v, want only db", ups)
	}
	if GetPortProxy(port).hasSNITargets() {
		t.Error("the SNI targets of the earlier upstreams are gone")
	}
}

func TestAddUpstreamsAPIActsOnTheRequestedPort(t *testing.T) {
	listenerPort, requestedPort := apiPort(t), apiPort(t)
	rr := callWith(t, listenerPort, requestedPort, "POST", "/proxy/tcp/upstreams/add", goodUpstreams)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	if !WillProxyTCP(requestedPort) || WillProxyTCP(listenerPort) {
		t.Errorf("proxied: requested port %v, listener port %v; want the requested one only", WillProxyTCP(requestedPort), WillProxyTCP(listenerPort))
	}
}

func TestAddUpstreamsAPIRejectsBadPayloads(t *testing.T) {
	tests := map[string]struct{ body, msg string }{
		"not json":            {`{{`, "Failed to parse proxy target"},
		"empty":               {``, "Failed to parse proxy target"},
		"no endpoints":        {`{"a": {}}`, "upstream endpoints missing"},
		"null upstream":       {`{"a": null}`, "upstream [a] is empty"},
		"null endpoint":       {`{"a": {"endpoints": {"e": null}}}`, "missing address"},
		"bad address":         {`{"a": {"endpoints": {"e": {"address": "nope"}}}}`, "invalid address"},
		"bad sni":             {`{"a": {"endpoints": {"e": {"address": "h:1"}}, "match": {"sni": "*.example.com"}}}`, "invalid SNI match"},
		"delay without a max": {`{"a": {"endpoints": {"e": {"address": "h:1"}}, "delay": {"min": "1s"}}}`, "has a delay without min or max"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			port := apiPort(t)
			call(t, port, "POST", "/proxy/tcp/upstreams/add", `{"keep": {"endpoints": {"e": {"address": "h:1"}}}}`)
			rr := call(t, port, "POST", "/proxy/tcp/upstreams/add", tt.body)
			if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), tt.msg) {
				t.Errorf("status = %d, body %q; want a 400 with %q", rr.Code, rr.Body.String(), tt.msg)
			}
			if ups := GetPortProxy(port).getUpstreams(); len(ups) != 1 || ups["keep"] == nil {
				t.Errorf("a rejected payload must leave the upstreams alone, got %+v", ups)
			}
		})
	}
}

func TestAPIMethods(t *testing.T) {
	port := apiPort(t)
	// The routes only take the methods they were registered for. (The routers answer 404 or 405 for the others,
	// depending on how deep the route is nested, either way the request is refused.)
	for _, tt := range []struct{ method, uri string }{
		{"GET", "/proxy/tcp/upstreams/add"},
		{"DELETE", "/proxy/tcp/upstreams/add"},
		{"POST", "/proxy/tcp"},
		{"POST", "/proxy/tcp/all"},
		{"POST", "/proxy/tcp/upstreams"},
		{"POST", "/proxy/tcp/upstreams/all"},
		{"GET", fmt.Sprintf("/proxy/tcp/%d?address=h:1", port)},
		{"GET", "/proxy/tcp/nothing/here"},
	} {
		if rr := call(t, port, tt.method, tt.uri, ""); rr.Code != http.StatusNotFound && rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d, want it refused", tt.method, tt.uri, rr.Code)
		}
	}
	if WillProxyTCP(port) {
		t.Error("none of these may have configured anything")
	}
}

func TestGetProxyAPI(t *testing.T) {
	port := apiPort(t)
	call(t, port, "POST", "/proxy/tcp/upstreams/add", goodUpstreams)
	for _, uri := range []string{"/proxy/tcp", "/proxy/tcp/"} {
		rr := call(t, port, "GET", uri, "")
		if rr.Code != http.StatusOK || !strings.Contains(rr.Header().Get("Content-Type"), "json") {
			t.Fatalf("GET %s = %d %q", uri, rr.Code, rr.Header().Get("Content-Type"))
		}
		got := decode(t, rr)
		if got["port"] != float64(port) {
			t.Errorf("GET %s: port = %v, want %d", uri, got["port"], port)
		}
		tcp, _ := got["tcp"].(map[string]any)
		if tcp["port"] != float64(port) || tcp["enabled"] != true {
			t.Fatalf("GET %s: tcp = %v", uri, got["tcp"])
		}
		web := tcp["upstreams"].(map[string]any)["web"].(map[string]any)
		if web["name"] != "web" || len(web["endpoints"].(map[string]any)) != 2 || web["match"].(map[string]any)["sni"] != "a.example.com" || web["dropPct"] != 10.0 {
			t.Errorf("GET %s: upstream = %v", uri, web)
		}
		if tcp["tracker"].(map[string]any)["proxyPort"] != float64(port) {
			t.Errorf("GET %s: tracker = %v", uri, tcp["tracker"])
		}
	}
	// A port that has nothing set up still gets an answer
	other := apiPort(t)
	got := decode(t, call(t, other, "GET", "/proxy/tcp", ""))
	if got["port"] != float64(other) || len(got["tcp"].(map[string]any)["upstreams"].(map[string]any)) != 0 {
		t.Errorf("answer for a port without a proxy = %v", got)
	}
}

func TestGetProxyAPIOfRequestedPort(t *testing.T) {
	listenerPort, requestedPort := apiPort(t), apiPort(t)
	call(t, requestedPort, "POST", "/proxy/tcp/upstreams/add", goodUpstreams)
	got := decode(t, callWith(t, listenerPort, requestedPort, "GET", "/proxy/tcp", ""))
	if got["port"] != float64(requestedPort) || len(got["tcp"].(map[string]any)["upstreams"].(map[string]any)) != 1 {
		t.Errorf("answer = %v, want the one for the requested port", got)
	}
}

func TestGetProxyAPIAll(t *testing.T) {
	p1, p2 := apiPort(t), apiPort(t)
	call(t, p1, "POST", "/proxy/tcp/upstreams/add", goodUpstreams)
	GetPortProxy(p2)
	for _, uri := range []string{"/proxy/tcp/all", "/proxy/tcp/all/"} {
		rr := call(t, p1, "GET", uri, "")
		if rr.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", uri, rr.Code)
		}
		got := decode(t, rr)
		one, two := got[strconv.Itoa(p1)], got[strconv.Itoa(p2)]
		if one == nil || two == nil {
			t.Fatalf("GET %s = %v, want both ports", uri, got)
		}
		if len(one.(map[string]any)["upstreams"].(map[string]any)) != 1 || len(two.(map[string]any)["upstreams"].(map[string]any)) != 0 {
			t.Errorf("GET %s: upstreams of the ports = %v, %v", uri, one, two)
		}
		if _, found := got["port"]; found {
			t.Errorf("GET %s answered for a single port", uri)
		}
	}
}

// The URI is what tells a request for all ports from one for a port, not any text in it.
func TestGetAPIsOnlyReportAllForAllURIs(t *testing.T) {
	port, other := apiPort(t), apiPort(t)
	GetPortProxy(other)
	for _, uri := range []string{"/proxy/tcp?note=all", "/proxy/tcp/upstreams?tall=1&all=true", "/proxy/tcp/?x=all"} {
		got := decode(t, call(t, port, "GET", uri, ""))
		if got["port"] != float64(port) {
			t.Errorf("GET %s = %v, want the answer for a single port", uri, got)
		}
		if _, found := got[strconv.Itoa(other)]; found {
			t.Errorf("GET %s listed the other ports", uri)
		}
	}
}

func TestGetUpstreamsAPI(t *testing.T) {
	port := apiPort(t)
	call(t, port, "POST", "/proxy/tcp/upstreams/add", goodUpstreams)
	for _, uri := range []string{"/proxy/tcp/upstreams", "/proxy/tcp/upstreams/"} {
		rr := call(t, port, "GET", uri, "")
		if rr.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", uri, rr.Code)
		}
		got := decode(t, rr)
		ups := got["tcp"].(map[string]any)
		if got["port"] != float64(port) || ups["web"] == nil || len(ups) != 1 {
			t.Errorf("GET %s = %v", uri, got)
		}
	}
}

func TestGetUpstreamsAPIAll(t *testing.T) {
	p1, p2 := apiPort(t), apiPort(t)
	call(t, p1, "POST", "/proxy/tcp/upstreams/add", goodUpstreams)
	GetPortProxy(p2)
	got := decode(t, call(t, p1, "GET", "/proxy/tcp/upstreams/all", ""))
	one, two := got[strconv.Itoa(p1)].(map[string]any), got[strconv.Itoa(p2)].(map[string]any)
	if one["web"] == nil || len(two) != 0 {
		t.Errorf("answer = %v", got)
	}
}

func TestReportedUpstreamsCanBeAddedBack(t *testing.T) {
	p1, p2 := apiPort(t), apiPort(t)
	call(t, p1, "POST", "/proxy/tcp/upstreams/add", goodUpstreams)
	reported := decode(t, call(t, p1, "GET", "/proxy/tcp/upstreams", ""))["tcp"]
	b, _ := json.Marshal(reported)
	if rr := call(t, p2, "POST", "/proxy/tcp/upstreams/add", string(b)); rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	if !GetPortProxy(p2).hasSNITargets() || len(GetPortProxy(p2).getUpstreams()["web"].Endpoints) != 2 {
		t.Errorf("upstreams = %+v", GetPortProxy(p2).getUpstreams())
	}
}

func TestProxyTCPAPIRejectsInvalidRequests(t *testing.T) {
	port := apiPort(t)
	for name, tt := range map[string]struct{ uri, msg string }{
		"port is not a number":    {"/proxy/tcp/abc?address=h:1", "Invalid port [0]"},
		"port is zero":            {"/proxy/tcp/0?address=h:1", "Invalid port [0]"},
		"address without a port":  {fmt.Sprintf("/proxy/tcp/%d?address=localhost", port), "invalid address [localhost]"},
		"address with a bad port": {fmt.Sprintf("/proxy/tcp/%d?address=localhost:x", port), "invalid port in address"},
		"empty address":           {fmt.Sprintf("/proxy/tcp/%d?address=", port), "Invalid port"},
	} {
		t.Run(name, func(t *testing.T) {
			rr := call(t, port, "POST", tt.uri, "")
			if rr.Code != http.StatusBadRequest && rr.Code != http.StatusNotFound {
				t.Fatalf("status = %d, body %q", rr.Code, rr.Body.String())
			}
			if rr.Code == http.StatusBadRequest && !strings.Contains(rr.Body.String(), tt.msg) {
				t.Errorf("body = %q, want it to contain %q", rr.Body.String(), tt.msg)
			}
			if WillProxyTCP(port) {
				t.Error("nothing may be proxied after a rejected request")
			}
			if l := listeners.GetListenerForPort(port); l != nil {
				t.Errorf("a rejected request must not open a listener, got %+v", l)
			}
		})
	}
}

func TestProxyTCPAPIRequiresAddress(t *testing.T) {
	port := apiPort(t)
	rr := call(t, port, "POST", fmt.Sprintf("/proxy/tcp/%d", port), "")
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want the route for the port to need an address", rr.Code)
	}
	if listeners.GetListenerForPort(port) != nil || WillProxyTCP(port) {
		t.Error("nothing may be set up without an address")
	}
}

// ---------------------------------------------------------------------------------------------------------------
// The APIs with the listeners of the server behind them. The listeners package opens real ports on all the interfaces
// of the host.

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestProxyTCPAPIOpensListenerAndProxies(t *testing.T) {
	regServeListeners()
	echo := startEchoServer(t)
	port := freePort(t)
	done := make(chan struct{})
	regAccepted.Store(port, done)
	t.Cleanup(func() {
		proxyLock.Lock()
		delete(portProxy, port)
		proxyLock.Unlock()
	})
	rr := call(t, port, "POST", fmt.Sprintf("/proxy/tcp/%d?address=%s", port, echo.Addr()), "")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), fmt.Sprintf("Proxying TCP on port [%d] to upstream [%s]", port, echo.Addr())) {
		t.Fatalf("status = %d, body %q", rr.Code, rr.Body.String())
	}
	l := listeners.GetListenerForPort(port)
	if l == nil {
		t.Fatal("the listener of the port was not opened")
	}
	t.Cleanup(func() {
		// Runs after the connections of the proxy are over
		l.Close()
		listeners.RemoveListener(l)
		select {
		case <-done:
		case <-time.After(waitTimeout):
			t.Error("the listener did not stop serving")
		}
	})
	waitFor(t, "the listener to serve", func() bool { _, ok := regServed.Load(port); return ok })
	if !WillProxyTCP(port) {
		t.Fatal("the port must be proxied")
	}
	ups := GetPortProxy(port).getUpstreams()
	if len(ups) != 1 || ups[echo.Addr()] == nil || ups[echo.Addr()].Endpoints["default"].Address != echo.Addr() {
		t.Fatalf("upstreams = %+v", ups)
	}

	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), waitTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if got := roundTrip(t, c, "through the listener"); got != "through the listener" {
		t.Errorf("echo = %q", got)
	}
	c.Close()
	t.Cleanup(func() { waitGroup(t, &regConns, "proxied connections") })

	// The APIs report what went through
	waitFor(t, "the connection to be counted", func() bool {
		tracker := decode(t, call(t, port, "GET", "/proxy/tcp", ""))["tcp"].(map[string]any)["tracker"].(map[string]any)
		return tracker["connCount"] == 1.0
	})
}

func TestProxyTCPAPIFailsWhenPortCantBeOpened(t *testing.T) {
	regServeListeners()
	port := apiPort(t)
	t.Cleanup(func() {
		// the listeners package keeps the listener that it failed to open
		if l := listeners.GetListenerForPort(70000); l != nil {
			listeners.RemoveListener(l)
		}
	})
	rr := call(t, port, "POST", fmt.Sprintf("/proxy/tcp/%d?address=localhost:9", 70000), "")
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Failed to open listener on port [70000]") {
		t.Errorf("status = %d, body %q", rr.Code, rr.Body.String())
	}
	if WillProxyTCP(70000) {
		t.Error("nothing may be proxied on a port that couldn't be opened")
	}
}

func TestProxyTCPAPIReusesTheListenerOfThePort(t *testing.T) {
	regServeListeners()
	echo := startEchoServer(t)
	port := freePort(t)
	done := make(chan struct{})
	regAccepted.Store(port, done)
	t.Cleanup(func() {
		proxyLock.Lock()
		delete(portProxy, port)
		proxyLock.Unlock()
	})
	// Asking again for the same port reuses its listener, and adds the upstream
	for i := 0; i < 2; i++ {
		rr := call(t, port, "POST", fmt.Sprintf("/proxy/tcp/%d?address=%s", port, echo.Addr()), "")
		if rr.Code != http.StatusOK {
			t.Fatalf("round %d: status = %d, body %q", i, rr.Code, rr.Body.String())
		}
	}
	l := listeners.GetListenerForPort(port)
	t.Cleanup(func() {
		l.Close()
		listeners.RemoveListener(l)
		select {
		case <-done:
		case <-time.After(waitTimeout):
			t.Error("the listener did not stop serving")
		}
	})
	waitFor(t, "the listener to serve", func() bool { _, ok := regServed.Load(port); return ok })
	if n := len(GetPortProxy(port).getUpstreams()); n != 1 {
		t.Errorf("upstreams = %d, want the same one", n)
	}
	if l2 := listeners.GetListenerForPort(port); l2 != l || !l.IsTCP {
		t.Error("the listener of the port must be reused")
	}
}
