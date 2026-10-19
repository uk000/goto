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

package udpproxy

import (
	"context"
	"fmt"
	"goto/pkg/server/listeners"
	"goto/pkg/util"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"
)

// router returns the router that serves the admin APIs of the proxy. The routes are mounted on the router that the
// middleware package keeps for the /proxy path, once for the process, by the helper that the regression tests use too.
func router() *mux.Router {
	setup()
	return regRouter()
}

// call serves a request the way the server does: the request has a store, and arrives at a listener.
func call(t *testing.T, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := router()
	req := httptest.NewRequest(method, target, strings.NewReader(""))
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, 8080))
	_, req, _ = util.WithRequestStore(req, nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	return rr
}

// freeUDPPort finds a port that nothing uses at the moment, and that the proxy can open a listener on.
func freeUDPPort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}

// udpPort takes a port for a test to configure through the APIs, and cleans up what the test leaves behind: the
// listener that the APIs open on it, the proxy of the port and the goroutine that reads packets of the proxy.
func udpPort(t *testing.T) int {
	t.Helper()
	router()
	port := freeUDPPort(t)
	t.Cleanup(func() {
		var conn net.PacketConn
		if l := listeners.GetListenerForPort(port); l != nil {
			if l.UDPConn != nil {
				conn = l.UDPConn
			}
			l.Close()
			listeners.RemoveListener(l)
		}
		if p := findUDPProxy(port); p != nil && conn != nil {
			waitFor(t, "the proxy to stop reading from the closed listener", func() bool { return !p.isServing(conn) })
			deadline := time.After(waitTimeout)
			for i := 0; i < cap(p.inFlight); i++ {
				select {
				case p.inFlight <- struct{}{}:
				case <-deadline:
					t.Errorf("timed out waiting for the packets in flight to be over")
					return
				}
			}
		}
		proxyLock.Lock()
		delete(udpProxyByPort, port)
		proxyLock.Unlock()
	})
	return port
}

func listenerOpen(port int) bool {
	l := listeners.GetListenerForPort(port)
	return l != nil && l.IsUDP && l.UDPConn != nil
}

func TestMiddlewareDefinition(t *testing.T) {
	if Middleware.Name != "udpproxy" || Middleware.SetRoutes == nil {
		t.Errorf("middleware = %+v", Middleware)
	}
	if Middleware.MiddlewareHandler != nil {
		t.Error("the proxy only has admin APIs, and no middleware for the traffic")
	}
}

func TestProxyUDPAPIRejectsInvalidRequests(t *testing.T) {
	tests := []struct {
		name   string
		target string // the %d is the port
		port   func(t *testing.T) int
		want   string
	}{
		{"port is not a number", "/proxy/udp/abc/127.0.0.1:53", nil, "Invalid port"},
		{"port is zero", "/proxy/udp/0/127.0.0.1:53", nil, "Invalid port"},
		{"port is negative", "/proxy/udp/-5/127.0.0.1:53", nil, "Invalid port"},
		{"address has no port", "/proxy/udp/%d/localhost", udpPort, "invalid upstream address"},
		{"address has no host and port", "/proxy/udp/%d/nonsense", udpPort, "invalid upstream address"},
		{"address has port zero", "/proxy/udp/%d/127.0.0.1:0", udpPort, "no port"},
		{"address has port out of range", "/proxy/udp/%d/127.0.0.1:99999", udpPort, "invalid upstream address"},
		{"address has a port that isn't one", "/proxy/udp/%d/127.0.0.1:port", udpPort, "invalid upstream address"},
		{"delay is not a duration", "/proxy/udp/%d/127.0.0.1:53/delay/abc", udpPort, "Invalid delay"},
		{"delay is a number", "/proxy/udp/%d/127.0.0.1:53/delay/100", udpPort, "Invalid delay"},
		{"delay is negative", "/proxy/udp/%d/127.0.0.1:53/delay/-1s", udpPort, "Invalid delay"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target, port := tt.target, 0
			if tt.port != nil {
				port = tt.port(t)
				target = fmt.Sprintf(tt.target, port)
			}
			rr := call(t, "POST", target)
			if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), tt.want) {
				t.Errorf("status = %d, body %q, want 400 and %q", rr.Code, rr.Body.String(), tt.want)
			}
			if port > 0 {
				// Nothing is set up for a request that is refused
				if listeners.IsListenerPresent(port) {
					t.Errorf("a listener was opened on port %d for a refused request", port)
				}
				if findUDPProxy(port) != nil {
					t.Errorf("a proxy was set up for port %d for a refused request", port)
				}
			}
		})
	}
}

func TestProxyUDPAPIOnlyPost(t *testing.T) {
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		for _, target := range []string{"/proxy/udp/9999/127.0.0.1:53", "/proxy/udp/9999/delay/1s", "/proxy/udp/9999/127.0.0.1:53/delay/1s"} {
			rr := call(t, method, target)
			if rr.Code != http.StatusNotFound && rr.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s: status = %d", method, target, rr.Code)
			}
		}
	}
	if listeners.IsListenerPresent(9999) || findUDPProxy(9999) != nil {
		t.Error("requests that were refused must not set anything up")
	}
}

func TestProxyUDPAPIProxiesPackets(t *testing.T) {
	srv := startUDPServer(t, "up:")
	port := udpPort(t)
	rr := call(t, "POST", fmt.Sprintf("/proxy/udp/%d/%s", port, srv.Addr()))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Proxying UDP on port") {
		t.Fatalf("status = %d, body %q", rr.Code, rr.Body.String())
	}
	if !listenerOpen(port) {
		t.Fatal("the API must have opened a UDP listener on the port")
	}
	p := findUDPProxy(port)
	if p == nil || p.Upstreams[srv.Addr()] == nil || !p.Upstreams[srv.Addr()].running() || p.Upstreams[srv.Addr()].Delay != nil {
		t.Fatalf("proxy = %+v", p)
	}
	c, err := net.DialTimeout("udp", fmt.Sprintf("127.0.0.1:%d", port), waitTimeout)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, msg := range []string{"first", "second"} {
		if got := ask(t, c.(*net.UDPConn), []byte(msg)); string(got) != "up:"+msg {
			t.Errorf("answer = %q, want %q", got, "up:"+msg)
		}
	}
	// Asking again for the same thing is fine, and keeps one upstream
	if rr := call(t, "POST", fmt.Sprintf("/proxy/udp/%d/%s", port, srv.Addr())); rr.Code != http.StatusOK {
		t.Errorf("status = %d, body %q", rr.Code, rr.Body.String())
	}
	if got := ask(t, c.(*net.UDPConn), []byte("third")); string(got) != "up:third" {
		t.Errorf("answer = %q", got)
	}
	if len(p.Upstreams) != 1 || srv.received.Load() != 3 {
		t.Errorf("upstreams = %v, packets received by the upstream = %d, want one upstream and 3 packets", p.Upstreams, srv.received.Load())
	}
	tr := p.tracker()
	tr.lock.RLock()
	defer tr.lock.RUnlock()
	if tr.PacketCount != 3 || tr.ConnCount != 1 || tr.PacketCountByUpstream[srv.Addr()] != 3 {
		t.Errorf("tracker = %+v", tr)
	}
}

func TestProxyUDPAPIWithDelay(t *testing.T) {
	srv := startUDPServer(t, "")
	port := udpPort(t)
	rr := call(t, "POST", fmt.Sprintf("/proxy/udp/%d/%s/delay/100ms", port, srv.Addr()))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rr.Code, rr.Body.String())
	}
	up := findUDPProxy(port).Upstreams[srv.Addr()]
	if d := up.Delay; d == nil || d.Min.Duration != 100*time.Millisecond || d.Max.Duration != 100*time.Millisecond {
		t.Fatalf("delay = %+v", d)
	}
	c, err := net.DialTimeout("udp", fmt.Sprintf("127.0.0.1:%d", port), waitTimeout)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	start := time.Now()
	ask(t, c.(*net.UDPConn), []byte("slow"))
	if took := time.Since(start); took < 200*time.Millisecond {
		t.Errorf("the round trip took %s, want the packet and the answer delayed by 100ms each", took)
	}
	// a range, and a count that the delay of the upstream doesn't have
	if rr := call(t, "POST", fmt.Sprintf("/proxy/udp/%d/%s/delay/20ms-40ms", port, srv.Addr())); rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rr.Code, rr.Body.String())
	}
	if d := up.Delay; d == nil || d.Min.Duration != 20*time.Millisecond || d.Max.Duration != 40*time.Millisecond || d.Count != -1 {
		t.Errorf("delay = %+v", d)
	}
	// asking for the upstream again without a delay takes the delay off
	if rr := call(t, "POST", fmt.Sprintf("/proxy/udp/%d/%s", port, srv.Addr())); rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rr.Code, rr.Body.String())
	}
	if up.Delay != nil {
		t.Errorf("delay = %+v, want none", up.Delay)
	}
}

func TestSetUDPDelayAPI(t *testing.T) {
	a := startUDPServer(t, "")
	b := startUDPServer(t, "")
	port := udpPort(t)
	for _, srv := range []*udpServer{a, b} {
		if rr := call(t, "POST", fmt.Sprintf("/proxy/udp/%d/%s", port, srv.Addr())); rr.Code != http.StatusOK {
			t.Fatalf("status = %d, body %q", rr.Code, rr.Body.String())
		}
	}
	p := findUDPProxy(port)
	rr := call(t, "POST", fmt.Sprintf("/proxy/udp/%d/delay/30ms-60ms", port))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Delay configured") {
		t.Fatalf("status = %d, body %q", rr.Code, rr.Body.String())
	}
	// the URI of the delay has no upstream in it, so the delay is for all the upstreams of the port
	for ep, up := range p.Upstreams {
		if d := up.Delay; d == nil || d.Min.Duration != 30*time.Millisecond || d.Max.Duration != 60*time.Millisecond {
			t.Errorf("delay of upstream %s = %+v", ep, d)
		}
	}
	if rr := call(t, "POST", fmt.Sprintf("/proxy/udp/%d/delay/0s", port)); rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rr.Code, rr.Body.String())
	}
	for ep, up := range p.Upstreams {
		if up.Delay != nil {
			t.Errorf("delay of upstream %s = %+v, want it cleared", ep, up.Delay)
		}
	}
	for _, bad := range []string{"abc", "100", "-1s"} {
		rr := call(t, "POST", fmt.Sprintf("/proxy/udp/%d/delay/%s", port, bad))
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Invalid delay") {
			t.Errorf("delay %q: status = %d, body %q", bad, rr.Code, rr.Body.String())
		}
	}
}

func TestSetUDPDelayAPIRejectsPortWithoutListener(t *testing.T) {
	port := freeUDPPort(t)
	rr := call(t, "POST", fmt.Sprintf("/proxy/udp/%d/delay/10ms", port))
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "No listener") {
		t.Errorf("status = %d, body %q", rr.Code, rr.Body.String())
	}
	if findUDPProxy(port) != nil {
		t.Error("a proxy must not be set up for a port without a listener")
	}
}

func TestSetUDPDelayAPIRejectsPortWithoutUpstreams(t *testing.T) {
	port := udpPort(t)
	if err := listeners.AddListener(port, false, true, ""); err != nil {
		t.Fatal(err)
	}
	rr := call(t, "POST", fmt.Sprintf("/proxy/udp/%d/delay/10ms", port))
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "No upstream") {
		t.Errorf("status = %d, body %q, want 400 for a port that proxies to nothing", rr.Code, rr.Body.String())
	}
	if findUDPProxy(port) != nil {
		t.Error("setting a delay must not set a proxy up")
	}
}

func TestProxyUDPAPIConcurrently(t *testing.T) {
	srv := startUDPServer(t, "")
	port := udpPort(t)
	if rr := call(t, "POST", fmt.Sprintf("/proxy/udp/%d/%s", port, srv.Addr())); rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rr.Code, rr.Body.String())
	}
	const workers, rounds = 6, 15
	codes := make(chan int, workers*rounds*2)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				codes <- call(t, "POST", fmt.Sprintf("/proxy/udp/%d/delay/%dms", port, i%3)).Code
				codes <- call(t, "POST", fmt.Sprintf("/proxy/udp/%d/%s/delay/%dms", port, srv.Addr(), i%3)).Code
			}
		}()
	}
	c, err := net.DialTimeout("udp", fmt.Sprintf("127.0.0.1:%d", port), waitTimeout)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// traffic goes on while the admin requests are served
	for i := 0; i < 5; i++ {
		if got := ask(t, c.(*net.UDPConn), []byte("q")); string(got) != "q" {
			t.Errorf("answer = %q", got)
		}
	}
	waitGroup(t, &wg, "admin requests")
	close(codes)
	n := 0
	for code := range codes {
		n++
		if code != http.StatusOK {
			t.Errorf("status = %d, want 200", code)
		}
	}
	if n != workers*rounds*2 {
		t.Errorf("%d requests were served, want %d", n, workers*rounds*2)
	}
}

func TestExportedFunctionsWithoutListener(t *testing.T) {
	// none of these may panic or set a proxy up for a port that has no UDP listener
	port := freeUDPPort(t)
	t.Cleanup(func() {
		proxyLock.Lock()
		delete(udpProxyByPort, port)
		proxyLock.Unlock()
	})
	ProxyUDPUpstream(port, "127.0.0.1:53", 0, 0)
	if p := findUDPProxy(port); p != nil && len(p.Upstreams) != 0 {
		t.Errorf("upstreams = %v, want none for a port without a listener", p.Upstreams)
	}
	SetUDPDelay(port, "", time.Second, time.Second)
	StopUDPUpstream(port, "127.0.0.1:53")
	ProxyUDPUpstream(port, "not an address", 0, 0)
}

func TestExportedFunctionsWithListener(t *testing.T) {
	a := startUDPServer(t, "A:")
	b := startUDPServer(t, "B:")
	port := udpPort(t)
	if err := listeners.AddListener(port, false, true, ""); err != nil {
		t.Fatal(err)
	}
	ProxyUDPUpstream(port, a.Addr(), 0, 0)
	ProxyUDPUpstream(port, "not an address", 0, 0)
	ProxyUDPUpstream(port, "127.0.0.1:0", 0, 0)
	p := findUDPProxy(port)
	if p == nil || len(p.Upstreams) != 1 || p.Upstreams[a.Addr()] == nil || p.Upstreams[a.Addr()].Name != fmt.Sprintf("%d-%s", port, a.Addr()) {
		t.Fatalf("proxy = %+v, want one upstream, and none for the addresses that aren't usable", p)
	}
	ProxyUDPUpstream(port, b.Addr(), 0, 0)
	c, err := net.DialTimeout("udp", fmt.Sprintf("127.0.0.1:%d", port), waitTimeout)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	uc := c.(*net.UDPConn)
	// the packets go to the upstreams in turn
	got := []string{string(ask(t, uc, []byte("1"))), string(ask(t, uc, []byte("2"))), string(ask(t, uc, []byte("3")))}
	first, second := "A:", "B:"
	if b.Addr() < a.Addr() {
		first, second = "B:", "A:"
	}
	if want := []string{first + "1", second + "2", first + "3"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("answers = %v, want %v", got, want)
	}
	StopUDPUpstream(port, a.Addr())
	for i := 0; i < 3; i++ {
		if got := string(ask(t, uc, []byte("x"))); got != "B:x" {
			t.Errorf("answer = %q, want the one of the upstream that wasn't stopped", got)
		}
	}
	SetUDPDelay(port, b.Addr(), 50*time.Millisecond, 50*time.Millisecond)
	if d := p.Upstreams[b.Addr()].Delay; d == nil || d.Min.Duration != 50*time.Millisecond {
		t.Errorf("delay = %+v", d)
	}
	if p.Upstreams[a.Addr()].Delay != nil {
		t.Error("the delay of one upstream must not be set on the others")
	}
}
