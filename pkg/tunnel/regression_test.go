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
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	. "goto/pkg/constants"
	"goto/pkg/server/listeners"
	"goto/pkg/util"
)

// A host that starts with the letters of a scheme (httpbin.org, h2o.example.com) was taken for a scheme, which gave
// a URL like httpbin.org://80.
func TestRegressionHostsThatStartLikeASchemeAreNotSchemes(t *testing.T) {
	for _, c := range []struct {
		address, id, url, host string
		port                   int
	}{
		{"httpbin.org:80", "http:httpbin.org:80", "http://httpbin.org:80", "httpbin.org", 80},
		{"h2o.example.com:8080", "http:h2o.example.com:8080", "http://h2o.example.com:8080", "h2o.example.com", 8080},
		{"http-svc:9000", "http:http-svc:9000", "http://http-svc:9000", "http-svc", 9000},
		{"https-proxy", "http:https-proxy", "http://https-proxy", "https-proxy", 80},
	} {
		ep := newEndpoint(c.address, false, false)
		if ep.ID != c.id || ep.URL != c.url || ep.Host != c.host || ep.Port != c.port || ep.Address != c.address || !ep.UseRequestProto || ep.IsTLS {
			t.Errorf("newEndpoint(%q) = %+v", c.address, ep)
		}
	}
	if ep := newEndpoint("https-proxy:443", true, false); ep.ID != "https:https-proxy:443" || ep.URL != "https://https-proxy:443" {
		t.Errorf("newEndpoint(https-proxy:443) = %+v", ep)
	}
}

// A tunnel that was added more than once was removed only partly, and so went on being used.
func TestRegressionRemovingTunnelThatWasAddedSeveralTimes(t *testing.T) {
	pt := newPortTunnel(newPort(t))
	for i := 0; i < 3; i++ {
		pt.addTunnel("a:80", false, false, "", "", "")
		pt.addTunnel("a:80", false, false, "/u", "", "")
		pt.addTunnel("a:80", false, false, "", "H", "v")
		pt.addTunnel("a:80", false, false, "/u", "H", "v")
	}
	id := "http:a:80"
	for _, r := range []struct {
		name         string
		uri, h, v    string
		leftOver     func() int
		leftOverList func() []string
	}{
		{"broad", "", "", "", func() int { return len(pt.BroadTunnels) }, func() []string { return pt.BroadTunnels }},
		{"uri", "/u", "", "", func() int { return len(pt.URITunnels) }, func() []string { return pt.URITunnels["/u"] }},
		{"header", "", "H", "v", func() int { return len(pt.HeaderTunnels) }, func() []string { return pt.HeaderTunnels["H"]["v"] }},
		{"uri and header", "/u", "H", "v", func() int { return len(pt.URIHeaderTunnels) }, func() []string { return pt.URIHeaderTunnels["/u"]["H"]["v"] }},
	} {
		if !pt.removeTunnel(id, r.uri, r.h, r.v) {
			t.Errorf("%s: the tunnel should have been found", r.name)
		}
		if left := r.leftOverList(); len(left) != 0 {
			t.Errorf("%s: tunnels left after removing %s: %v", r.name, id, left)
		}
	}
}

// A request is given the endpoints of the tunnel that matched it, and removing a tunnel used to change them.
func TestRegressionEndpointsOfRequestAreNotChangedByRemovingTunnels(t *testing.T) {
	pt := newPortTunnel(newPort(t))
	pt.addTunnel("a:80", false, false, "", "", "")
	pt.addTunnel("b:80", false, false, "", "", "")
	pt.addTunnel("c:80", false, false, "/u", "", "")
	pt.addTunnel("d:80", false, false, "/u", "", "")
	broad := func() []string {
		_, eps := pt.checkTunnelsForRequest(httptest.NewRequest("GET", "/x", nil))
		return eps
	}()
	byURI := func() []string {
		_, eps := pt.checkTunnelsForRequest(httptest.NewRequest("GET", "/u", nil))
		return eps
	}()
	pt.removeTunnel("http:a:80", "", "", "")
	pt.removeTunnel("http:c:80", "/u", "", "")
	if strings.Join(broad, ",") != "http:a:80,http:b:80" {
		t.Errorf("endpoints of a request that matched a broad tunnel changed to %v", broad)
	}
	if strings.Join(byURI, ",") != "http:c:80,http:d:80" {
		t.Errorf("endpoints of a request that matched a URI tunnel changed to %v", byURI)
	}
}

// A tunnel for a header, added without a value, is for the requests that have the header. It used to apply to all
// the requests.
func TestRegressionTunnelForAHeaderIsNotForRequestsWithoutIt(t *testing.T) {
	pt := newPortTunnel(newPort(t))
	pt.addTunnel("a:80", false, false, "", "X-Any", "")
	pt.addTunnel("b:80", false, false, "/u", "X-Any", "")
	pt.addTunnel("c:80", false, false, "", "X-Value", "v")
	for _, c := range []struct {
		uri     string
		headers []string
		want    string
	}{
		{"/x", nil, ""},
		{"/x", []string{"X-Value", "other"}, ""},
		{"/x", []string{"X-Other", "v"}, ""},
		{"/u", nil, ""},
		{"/x", []string{"X-Any", "1"}, "http:a:80"},
		{"/u", []string{"X-Any", "1"}, "http:b:80"},
		{"/x", []string{"X-Value", "v"}, "http:c:80"},
	} {
		r := httptest.NewRequest("GET", c.uri, nil)
		for i := 0; i+1 < len(c.headers); i += 2 {
			r.Header.Add(c.headers[i], c.headers[i+1])
		}
		ok, eps := pt.checkTunnelsForRequest(r)
		if ok != (c.want != "") || strings.Join(eps, ",") != c.want {
			t.Errorf("%s with headers %v tunneled to %v (%v), want %q", c.uri, c.headers, eps, ok, c.want)
		}
	}
}

// clear() took the lock of the tunnel and then the same lock again to reset the tracking.
func TestRegressionClearingAPortTunnelDoesNotDeadlock(t *testing.T) {
	pt := newPortTunnel(newPort(t))
	pt.addTunnel("a:80", false, false, "", "", "")
	pt.addTracking([]string{"X-H"}, []string{"q"})
	done := make(chan struct{})
	go func() {
		defer close(done)
		pt.clear()
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("clear() didn't return")
	}
	if len(pt.Tunnels) != 0 || len(pt.BroadTunnels) != 0 || len(pt.TunnelTrackingHeaders) != 0 || len(pt.Traffic.ByEndpoints) != 0 {
		t.Errorf("port tunnel after clear = %+v", pt)
	}
}

// The wait for the end of a tunnel could start before the copying that it waits for, as that added itself to the
// wait group when it started, and a tunnel was then taken for finished at once.
func TestRegressionProxyTunnelStaysRegisteredWhileItIsOpen(t *testing.T) {
	self := newSelfServer(t)
	pt := GetOrCreatePortTunnel(self.port())
	for i := 0; i < 20; i++ {
		clientSide, remote := pipeConns(t)
		if !pt.openProxyTunnel(fmt.Sprintf("10.0.0.1:%d", 1000+i), "dest:443", false, false, false, clientSide) {
			t.Fatal("tunnel didn't open")
		}
		server := self.accept(t)
		time.Sleep(5 * time.Millisecond)
		pt.lock.RLock()
		registered := pt.ProxyTunnels[server.RemoteAddr().String()] != nil
		pt.lock.RUnlock()
		if !registered {
			t.Fatalf("tunnel %d isn't registered while its connections are open", i)
		}
		remote.Close()
		server.Close()
	}
	waitFor(t, "all the tunnels to be unregistered", func() bool { return pt.proxyTunnelCount() == 0 })
}

// A connection that was taken over from the server and couldn't be tunneled was left open.
func TestRegressionHijackedConnectionIsClosedWhenTunnelCantOpen(t *testing.T) {
	stubOnce.Do(stubGlobalFuncs)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	trackPort(t, port)
	setPodIP(t, "127.0.0.1")
	clientSide, remote := pipeConns(t)
	w := &hijackable{ResponseRecorder: httptest.NewRecorder(), conn: clientSide}
	r := newRequest(port, "CONNECT", "dest.example:443", nil)
	if HijackConnect(r, w) {
		t.Fatal("there is no server to tunnel through")
	}
	if _, err := remote.Read(make([]byte, 1)); err == nil || strings.Contains(err.Error(), "timeout") {
		t.Errorf("the connection should be closed, got %v", err)
	}
}

// The query of a request that is proxied was dropped.
func TestRegressionProxiedRequestsKeepTheirQuery(t *testing.T) {
	port := newPort(t)
	r := newRequest(port, "GET", "http://example.com/a/b?x=1&y=2", nil, HeaderProxyConnection, "keep-alive")
	r.Host = "example.com"
	CheckTunnelRequest(r)
	if r.RequestURI != "/a/b?x=1&y=2" {
		t.Errorf("proxied URI = %q, want the path and the query", r.RequestURI)
	}

	pt := GetOrCreatePortTunnel(port)
	pt.lock.Lock()
	pt.ProxyTunnels["10.1.1.1:5555"] = &ProxyTunnel{ToAddress: "dest.example:443"}
	pt.lock.Unlock()
	r = newRequest(port, "GET", "/in/the/tunnel?token=abc", nil)
	r.RemoteAddr = "10.1.1.1:5555"
	CheckTunnelRequest(r)
	if r.RequestURI != "/in/the/tunnel?token=abc" {
		t.Errorf("URI in a tunnel = %q, want the path and the query", r.RequestURI)
	}
}

// The tunnel host header has the label of the host and the tunnel count at the host, which is not what was
// compared with the label of this host, so a loop was never detected.
func TestRegressionTunnelLoopIsDetected(t *testing.T) {
	port := newPort(t)
	up := newUpstream(t, "one", http.StatusOK, "should not be reached")
	l := listeners.DefaultListener
	old := l.HostLabel
	l.HostLabel = "loop-test-host"
	t.Cleanup(func() { l.HostLabel = old })
	req := newRequest(port, "GET", "/tunnel="+up.addr()+"/x", nil, HeaderGotoTunnelHost, "other-host|0", HeaderGotoTunnelHost, "LOOP-TEST-HOST|1")
	rr := serve(router(), req)
	if !strings.Contains(rr.Body.String(), "Tunnel loop detected") {
		t.Errorf("answered %d %q", rr.Code, rr.Body.String())
	}
	if len(up.seen()) != 0 {
		t.Error("a request that came back to its host must not be tunneled again")
	}
	if !strings.Contains(rr.Header().Get(HeaderViaGoto), "[tunnel loop]") {
		t.Errorf("headers = %v", rr.Header())
	}
	// A request that went through other hosts only is tunneled on
	req = newRequest(port, "GET", "/tunnel="+up.addr()+"/x", nil, HeaderGotoTunnelHost, "other-host|0", HeaderGotoTunnelHost, "loop-test-host-2|1")
	if rr := serve(router(), req); rr.Body.String() != "should not be reached" {
		t.Errorf("a request that didn't loop answered %d %q", rr.Code, rr.Body.String())
	}
}

// The endpoints of a request are tunneled to at the same time, and shared the payload of the request.
func TestRegressionTunnelToSeveralEndpoints(t *testing.T) {
	port := newPort(t)
	one := newUpstream(t, "one", http.StatusOK, "from-one")
	two := newUpstream(t, "two", http.StatusOK, "from-two")
	three := newUpstream(t, "three", http.StatusOK, "from-three")
	for i := 0; i < 5; i++ {
		rr := serve(router(), newRequest(port, "POST", "/tunnel="+one.addr()+","+two.addr()+","+three.addr()+"/all", strings.NewReader("the-payload")))
		if body := rr.Body.String(); body != "from-one" && body != "from-two" && body != "from-three" {
			t.Errorf("only the first response is sent: %q", body)
		}
	}
	for _, u := range []*upstream{one, two, three} {
		seen := u.seen()
		if len(seen) != 5 {
			t.Errorf("%s saw %d requests, want 5", u.name, len(seen))
		}
		for _, s := range seen {
			if s.body != "the-payload" {
				t.Errorf("%s saw the payload %q", u.name, s.body)
			}
		}
	}
}

// A tunnel that was removed after the request was matched to it was a nil endpoint.
func TestRegressionTunnelToRemovedEndpointIsSkipped(t *testing.T) {
	port := newPort(t)
	pt := GetOrCreatePortTunnel(port)
	up := newUpstream(t, "one", http.StatusOK, "ok")
	pt.addTunnel(up.addr(), false, false, "", "", "")
	r := newRequest(port, "GET", "/x", nil)
	util.GetRequestStore(r).TunnelEndpoints = []string{"http:" + up.addr(), "http:gone:80"}
	rr := httptest.NewRecorder()
	func() {
		defer func() {
			if p := recover(); p != nil {
				t.Errorf("tunneling to an endpoint that was removed panicked: %v", p)
			}
		}()
		pt.tunnel(nil, "/x", r, rr)
	}()
	if len(up.seen()) != 1 {
		t.Errorf("the endpoint that is still there should have been tunneled to, it saw %d requests", len(up.seen()))
	}
}

// The requests that are tunneled at the same time to an endpoint share its client, and the traffic of the port.
func TestRegressionConcurrentTunnelConfigurationAndRequests(t *testing.T) {
	port := newPort(t)
	up := newUpstream(t, "one", http.StatusOK, "ok")
	pt := GetOrCreatePortTunnel(port)
	pt.addTunnel(up.addr(), false, false, "", "", "")
	pt.addTracking([]string{"X-Track"}, []string{"q"})
	pt.captureTunnelTraffic(true)
	errs := &errList{}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				rr := serve(chain(), newRequest(port, "GET", fmt.Sprintf("/r%d?q=%d", j, j), nil, "X-Track", "v"))
				if rr.Code != http.StatusOK {
					errs.addf("request answered %d %q", rr.Code, rr.Body.String())
				}
			}
		}()
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				ep := fmt.Sprintf("other%d:80", i)
				pt.addTunnel(ep, false, false, fmt.Sprintf("/u%d", j), "X-H", "v")
				pt.addTunnel(ep, false, false, "", "X-H", "v")
				pt.removeTunnel("http:"+ep, fmt.Sprintf("/u%d", j), "X-H", "v")
				pt.removeTunnel("http:"+ep, "", "X-H", "v")
				pt.addTracking([]string{"X-More"}, nil)
			}
		}(i)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				pt.checkTunnelsForRequest(httptest.NewRequest("GET", "/u1", nil))
				for _, target := range []string{"/tunnels", "/tunnels/track", "/tunnels/traffic", "/tunnels/active"} {
					if rr := admin(t, port, "GET", target); rr.Code != http.StatusOK {
						errs.addf("%s answered %d", target, rr.Code)
					}
				}
			}
		}()
	}
	wg.Wait()
	errs.report(t)
	pt.lock.RLock()
	defer pt.lock.RUnlock()
	if got := pt.Traffic.ByEndpoints["http:"+up.addr()]; got != 20 {
		t.Errorf("requests tracked for the endpoint = %d, want 20", got)
	}
	if len(pt.Traffic.Logs) != 20 {
		t.Errorf("traffic logged = %d, want 20", len(pt.Traffic.Logs))
	}
}
