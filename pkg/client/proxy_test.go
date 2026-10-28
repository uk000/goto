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

package client

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"goto/pkg/constants"
	tcpproxy "goto/pkg/proxy/tcp"
	gototls "goto/pkg/tls"
)

// connectProxy is the CONNECT proxy of goto, served on a loopback port the way the server serves a port in that mode.
type connectProxy struct {
	port  int
	conns atomic.Int32
	done  atomic.Int32
}

// startConnectProxy serves a port in CONNECT mode, over TLS if the config for it is given.
func startConnectProxy(t *testing.T, tlsConfig *tls.Config) *connectProxy {
	t.Helper()
	stubGlobalFuncs()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &connectProxy{port: ln.Addr().(*net.TCPAddr).Port}
	if tlsConfig != nil {
		ln = tls.NewListener(ln, tlsConfig)
	}
	tcpproxy.ClearPortProxy(p.port)
	tcpproxy.EnableConnect(p.port, true)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			p.conns.Add(1)
			go func() {
				tcpproxy.ProxyConnectConnection(p.port, conn)
				p.done.Add(1)
			}()
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		tcpproxy.ClearPortProxy(p.port)
	})
	return p
}

// expectTunnelsClosed waits for the tunnels that the proxy got to be over, which is what the end of a call is to bring.
func (p *connectProxy) expectTunnelsClosed(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for p.done.Load() != p.conns.Load() {
		if time.Now().After(deadline) {
			t.Fatalf("the proxy got %d tunnels, and %d of them are over: the call left tunnels open", p.conns.Load(), p.done.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (p *connectProxy) url(scheme, host string) string {
	return fmt.Sprintf("%s://%s:%d", scheme, host, p.port)
}

// proxyServerTLSConfig is a TLS config for a proxy, with the certificate that the test servers have.
func proxyServerTLSConfig() *tls.Config {
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	cert := srv.TLS.Certificates[0]
	srv.Close()
	return &tls.Config{Certificates: []tls.Certificate{cert}}
}

func newTLSEchoServer(t *testing.T) (*httptest.Server, *recorder) {
	t.Helper()
	rc := &recorder{}
	s := httptest.NewTLSServer(echoHandler(rc))
	t.Cleanup(s.Close)
	return s, rc
}

func TestInvokeViaProxyTunnelsTLSCalls(t *testing.T) {
	useHostLabel(t, "client-host-proxy")
	s, rc := newTLSEchoServer(t)
	proxy := startConnectProxy(t, nil)
	r, _ := newCallRequest(t, "POST", "/client/http/invoke", "")
	spec := &CallSpec{
		URL:     s.URL + "/hello?x=1",
		Method:  "POST",
		Count:   3,
		Payload: []string{"ping"},
		Proxy:   proxy.url("http", "127.0.0.1"),
	}
	results, err := spec.Invoke(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(results.Results) != 3 || len(results.Statuses) != 3 {
		t.Fatalf("results = %+v", results)
	}
	for i := 1; i <= 3; i++ {
		key := fmt.Sprintf("%s/hello?x=1[%d]", s.URL, i)
		if res := results.Results[key]; res == nil || res.Status != 200 || res.Payload != "echo:ping" || res.Headers.Get("X-Echo") != "yes" {
			t.Errorf("result for %q = %+v", key, res)
		}
	}
	got := rc.all()
	if len(got) != 3 {
		t.Fatalf("server saw %d requests", len(got))
	}
	// the server was reached over TLS, with what the client sent along
	if g := got[0]; g.TLSVersion == 0 || g.Path != "/hello" || g.RawQuery != "x=1" || g.Body != "ping" ||
		g.Header.Get(constants.HeaderGotoClientTLS) != "true" || g.Header.Get("User-Agent") != "client-host-proxy" {
		t.Errorf("server saw %+v", g)
	}
	// the certificate of the server is found out through the tunnel
	if len(results.ServerCerts) == 0 {
		t.Error("no server certs for the calls that were tunneled")
	}
	// and the three calls shared the tunnel that was set up for the first, which is closed with the end of the calls
	if n := proxy.conns.Load(); n != 1 {
		t.Errorf("the proxy got %d connections, want 1", n)
	}
	proxy.expectTunnelsClosed(t)
}

func TestInvokeViaProxyOverTLS(t *testing.T) {
	s, rc := newTLSEchoServer(t)
	proxy := startConnectProxy(t, proxyServerTLSConfig())
	r, _ := newCallRequest(t, "GET", "/client/http/invoke", "")
	spec := &CallSpec{URL: s.URL + "/tls", Proxy: proxy.url("https", "localhost")}
	results, err := spec.Invoke(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(results.Statuses) != 1 || results.Statuses[0] != 200 {
		t.Fatalf("results = %+v", results)
	}
	if got := rc.all(); len(got) != 1 || got[0].Path != "/tls" || got[0].TLSVersion == 0 {
		t.Errorf("server saw %+v", got)
	}
	if n := proxy.conns.Load(); n != 1 {
		t.Errorf("the proxy got %d connections, want 1", n)
	}
}

func TestInvokeViaProxyAppliesTLSSettingsOfTheCall(t *testing.T) {
	s, rc := newTLSEchoServer(t)
	proxy := startConnectProxy(t, nil)
	r, _ := newCallRequest(t, "GET", "/client/http/invoke", "")
	// the certificate of the server is not trusted, so a call that verifies it is refused...
	spec := &CallSpec{URL: s.URL, Proxy: proxy.url("http", "127.0.0.1"), VerifyTLS: true}
	if _, err := spec.Invoke(r); err == nil {
		t.Error("the certificate of the server was verified by none")
	}
	// ... and a call that has a TLS version has it for the tunneled connection, which the server then sees
	spec = &CallSpec{URL: s.URL, Proxy: proxy.url("http", "127.0.0.1"), TLSVersion: tls.VersionTLS12}
	if _, err := spec.Invoke(r); err != nil {
		t.Fatal(err)
	}
	if got := rc.all(); len(got) != 1 || got[0].TLSVersion != tls.VersionTLS12 {
		t.Errorf("server saw %+v, want one request over TLS 1.2", got)
	}
}

// proxyVias are the Via-Goto values of a result that tell that the call went through the proxy of a port in CONNECT mode.
func proxyVias(res *CallResult) []string {
	var found []string
	for _, v := range res.Headers[constants.HeaderViaGoto] {
		if strings.HasSuffix(v, "(Proxy)(HTTP)") {
			found = append(found, v)
		}
	}
	return found
}

func TestInvokeViaProxyTunnelsPlainHTTPCalls(t *testing.T) {
	// a call to an http target goes through a tunnel too, in which the proxy adds its identity to the responses
	useHostLabel(t, "client-host-plain")
	s, rc := newEchoServer(t)
	proxy := startConnectProxy(t, nil)
	r, _ := newCallRequest(t, "POST", "/client/http/invoke", "")
	spec := &CallSpec{URL: s.URL + "/plain?x=1", Method: "POST", Count: 3, Payload: []string{"ping"}, Proxy: proxy.url("http", "127.0.0.1")}
	results, err := spec.Invoke(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(results.Results) != 3 || len(results.Statuses) != 3 {
		t.Fatalf("results = %+v", results)
	}
	for i := 1; i <= 3; i++ {
		key := fmt.Sprintf("%s/plain?x=1[%d]", s.URL, i)
		res := results.Results[key]
		if res == nil || res.Status != 200 || res.Payload != "echo:ping" {
			t.Fatalf("result for %q = %+v", key, res)
		}
		// what the destination says about the gotos that the call went through, and the proxy on top of it
		vias := res.Headers[constants.HeaderViaGoto]
		if len(vias) != 2 || vias[0] != "upstream-goto" || len(proxyVias(res)) != 1 {
			t.Errorf("Via-Goto of %q = %q, want the destination, and then the proxy", key, vias)
		}
	}
	got := rc.all()
	if len(got) != 3 {
		t.Fatalf("server saw %d requests", len(got))
	}
	// the request is what the server gets from any client, and not what a proxy is sent
	if g := got[0]; g.Method != "POST" || g.Path != "/plain" || g.RawQuery != "x=1" || g.Body != "ping" || g.TLSVersion != 0 ||
		g.Header.Get("User-Agent") != "client-host-plain" || g.Header.Get(constants.HeaderGotoClientTLS) != "false" {
		t.Errorf("server saw %+v", g)
	}
	// the three calls shared the tunnel that was set up for the first, which is closed with the end of the calls
	if n := proxy.conns.Load(); n != 1 {
		t.Errorf("the proxy got %d connections, want 1", n)
	}
	proxy.expectTunnelsClosed(t)
}

func TestInvokeViaProxyOverTLSTunnelsPlainHTTPCalls(t *testing.T) {
	s, rc := newEchoServer(t)
	proxy := startConnectProxy(t, proxyServerTLSConfig())
	r, _ := newCallRequest(t, "GET", "/client/http/invoke", "")
	spec := &CallSpec{URL: s.URL + "/plain-over-tls", Proxy: proxy.url("https", "localhost")}
	results, err := spec.Invoke(r)
	if err != nil {
		t.Fatal(err)
	}
	res := results.Results[s.URL+"/plain-over-tls[1]"]
	if res == nil || res.Status != 200 || len(proxyVias(res)) != 1 {
		t.Fatalf("result = %+v", res)
	}
	if got := rc.all(); len(got) != 1 || got[0].Path != "/plain-over-tls" || got[0].TLSVersion != 0 {
		t.Errorf("server saw %+v", got)
	}
	if n := proxy.conns.Load(); n != 1 {
		t.Errorf("the proxy got %d connections, want 1", n)
	}
}

func TestInvokeViaProxyForPlainHTTPCallsThatTheProxyRefuses(t *testing.T) {
	// a proxy that doesn't tunnel gives no answer to the call, which is not sent to the proxy instead
	s, rc := newEchoServer(t)
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	t.Cleanup(refusing.Close)
	r, _ := newCallRequest(t, "GET", "/client/http/invoke", "")
	spec := &CallSpec{URL: s.URL, Proxy: refusing.URL}
	if results, err := spec.Invoke(r); err == nil || !strings.Contains(err.Error(), "refused a tunnel") {
		t.Errorf("results = %+v, err = %v, want an error about the tunnel that was refused", results, err)
	}
	if got := rc.all(); len(got) != 0 {
		t.Errorf("the server was reached: %+v", got)
	}
}

func TestInvokeViaProxyThatCantBeReached(t *testing.T) {
	s, rc := newTLSEchoServer(t)
	r, _ := newCallRequest(t, "GET", "/client/http/invoke", "")
	spec := &CallSpec{URL: s.URL, Proxy: "http://" + closedAddress(t)}
	if _, err := spec.Invoke(r); err == nil {
		t.Error("the call succeeded without the proxy")
	}
	if got := rc.all(); len(got) != 0 {
		t.Errorf("the server was reached: %+v", got)
	}
}

func TestInvokeViaProxyThatCantBeReachedForPlainHTTPCalls(t *testing.T) {
	s, rc := newEchoServer(t)
	r, _ := newCallRequest(t, "GET", "/client/http/invoke", "")
	spec := &CallSpec{URL: s.URL, Proxy: "http://" + closedAddress(t)}
	if _, err := spec.Invoke(r); err == nil {
		t.Error("the call succeeded without the proxy")
	}
	if got := rc.all(); len(got) != 0 {
		t.Errorf("the server was reached: %+v", got)
	}
}

func TestInvokeViaProxyRejectsWhatCantBeDone(t *testing.T) {
	s, rc := newTLSEchoServer(t)
	proxy := startConnectProxy(t, nil)
	good := proxy.url("http", "127.0.0.1")
	tests := []struct {
		name   string
		spec   CallSpec
		errHas string
	}{
		{"h2", CallSpec{URL: s.URL, Proxy: good, IsH2: true}, "h2"},
		{"noSNI", CallSpec{URL: s.URL, Proxy: good, NoSNI: true}, "noSNI"},
		{"alpn handshake", CallSpec{URL: s.URL, Proxy: good, ALPN: &gototls.ALPN{Handshake: &gototls.TCPHandshake{}}}, "ALPN handshake"},
		{"scheme", CallSpec{URL: s.URL, Proxy: "socks5://127.0.0.1:1080"}, "not supported"},
		{"no host", CallSpec{URL: s.URL, Proxy: "http://"}, "no host"},
		{"bad port", CallSpec{URL: s.URL, Proxy: "http://127.0.0.1:port"}, "invalid proxy URL"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := newCallRequest(t, "GET", "/client/http/invoke", "")
			spec := tc.spec
			_, err := spec.Invoke(r)
			if err == nil || !strings.Contains(err.Error(), tc.errHas) {
				t.Errorf("err = %v, want one with %q", err, tc.errHas)
			}
		})
	}
	if got := rc.all(); len(got) != 0 {
		t.Errorf("the server was reached by calls that were to be rejected: %+v", got)
	}
	if n := proxy.conns.Load(); n != 0 {
		t.Errorf("the proxy got %d connections from calls that were to be rejected", n)
	}
}

func TestInvokeViaProxyAllowsNoSNIForCallsWithoutTLS(t *testing.T) {
	// noSNI is of no concern to a call that has no TLS
	s, _ := newEchoServer(t)
	proxy := startConnectProxy(t, nil)
	r, _ := newCallRequest(t, "GET", "/client/http/invoke", "")
	spec := &CallSpec{URL: s.URL, Proxy: proxy.url("http", "127.0.0.1"), NoSNI: true}
	results, err := spec.Invoke(r)
	if err != nil || len(results.Statuses) != 1 || results.Statuses[0] != 200 {
		t.Fatalf("results = %+v, err = %v", results, err)
	}
	if n := proxy.conns.Load(); n != 1 {
		t.Errorf("the proxy got %d connections, want 1", n)
	}
}

func TestInvokeHTTPAPIViaProxy(t *testing.T) {
	s, rc := newTLSEchoServer(t)
	proxy := startConnectProxy(t, nil)
	spec := fmt.Sprintf(`{"url":%q,"method":"POST","payload":["api-body"],"proxy":%q}`, s.URL+"/api", proxy.url("http", "127.0.0.1"))
	r, _ := newCallRequest(t, "POST", "/client/http/invoke", spec, "Accept", "application/json")
	rr := serve(t, r)
	if rr.Code != 200 {
		t.Fatalf("status = %d, body: %s", rr.Code, rr.Body.String())
	}
	var out struct {
		Results  map[string]*CallResult
		Statuses []int
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("body %q: %v", rr.Body.String(), err)
	}
	if len(out.Statuses) != 1 || out.Statuses[0] != 200 || out.Results[s.URL+"/api[1]"].Payload != "echo:api-body" {
		t.Errorf("out = %+v", out)
	}
	if got := rc.all(); len(got) != 1 || got[0].TLSVersion == 0 {
		t.Errorf("server saw %+v", got)
	}
	if n := proxy.conns.Load(); n != 1 {
		t.Errorf("the proxy got %d connections, want 1", n)
	}
	// a proxy that can't be used is a bad request
	r, _ = newCallRequest(t, "POST", "/client/http/invoke", fmt.Sprintf(`{"url":%q,"proxy":"ftp://proxy.example"}`, s.URL))
	rr = serve(t, r)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "not supported") {
		t.Errorf("bad proxy: status = %d, body: %s", rr.Code, rr.Body.String())
	}
}

func TestInvokeViaProxyWithClientCertificate(t *testing.T) {
	name := fmt.Sprintf("proxy-client-cert-%d", time.Now().UnixNano())
	certPEM, keyPEM := selfSignedPEM(t, "goto-proxied-client")
	gototls.AddCert(name, certPEM)
	gototls.AddKey(name, keyPEM)
	t.Cleanup(func() { gototls.RemoveCert(name); gototls.RemoveKey(name) })
	rc := &recorder{}
	s := newTLSServer(t, rc, tls.RequestClientCert)
	proxy := startConnectProxy(t, nil)
	r, _ := newCallRequest(t, "GET", "/client/http/invoke", "")
	spec := &CallSpec{URL: s.URL + "/mtls", ClientCert: name, Proxy: proxy.url("http", "127.0.0.1")}
	results, err := spec.Invoke(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(results.ClientCert, "goto-proxied-client") {
		t.Errorf("the client certificate that was used is reported as %q", results.ClientCert)
	}
	// the server sees the client certificate on the connection that went through the tunnel
	got := rc.all()
	if len(got) != 1 || got[0].PeerCN != "goto-proxied-client" || got[0].Header.Get(constants.HeaderGotoClientMTLS) != "true" {
		t.Errorf("server saw %+v", got)
	}
	if n := proxy.conns.Load(); n != 1 {
		t.Errorf("the proxy got %d connections, want 1", n)
	}
}

func TestInvokeViaProxyWithALPN(t *testing.T) {
	rc := &recorder{}
	s := newTLSServer(t, rc, tls.NoClientCert)
	proxy := startConnectProxy(t, nil)
	r, _ := newCallRequest(t, "GET", "/client/http/invoke", "")
	spec := &CallSpec{URL: s.URL + "/alpn", ALPN: &gototls.ALPN{Protos: []string{"http/1.1"}}, Proxy: proxy.url("http", "127.0.0.1")}
	results, err := spec.Invoke(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(results.Statuses) != 1 || results.Statuses[0] != 200 {
		t.Fatalf("results = %+v", results)
	}
	if got := rc.all(); len(got) != 1 || got[0].ALPN != "http/1.1" {
		t.Errorf("server saw %+v, want one request that negotiated ALPN http/1.1", got)
	}
}

func TestInvokeViaProxyToHTTP2Server(t *testing.T) {
	// a server that has HTTP/2 gets it through the tunnel, as with a call that has no proxy
	rc := &recorder{}
	s := httptest.NewUnstartedServer(echoHandler(rc))
	s.EnableHTTP2 = true
	s.StartTLS()
	t.Cleanup(s.Close)
	proxy := startConnectProxy(t, nil)
	r, _ := newCallRequest(t, "GET", "/client/http/invoke", "")
	for _, proxyURL := range []string{"", proxy.url("http", "127.0.0.1")} {
		spec := &CallSpec{URL: s.URL + "/h2", Proxy: proxyURL}
		results, err := spec.Invoke(r)
		if err != nil || len(results.Statuses) != 1 || results.Statuses[0] != 200 {
			t.Fatalf("proxy [%s]: results = %+v, err = %v", proxyURL, results, err)
		}
	}
	got := rc.all()
	if len(got) != 2 || got[0].Proto != got[1].Proto {
		t.Errorf("server saw %+v, want the same protocol with and without the proxy", got)
	}
}

func TestInvokeHTTPAPIViaProxyForPlainHTTPCalls(t *testing.T) {
	s, _ := newEchoServer(t)
	proxy := startConnectProxy(t, nil)
	spec := fmt.Sprintf(`{"url":%q,"proxy":%q}`, s.URL+"/api", proxy.url("http", "127.0.0.1"))
	r, rs := newCallRequest(t, "POST", "/client/http/invoke", spec, "Accept", "application/json")
	rr := serve(t, r)
	if rr.Code != 200 {
		t.Fatalf("status = %d, body: %s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get(constants.HeaderGotoUpstreamStatus); got != "[200]" {
		t.Errorf("upstream status header = %q", got)
	}
	// the call is reported to have gone through the destination and the proxy, which the response of the server says
	if len(rs.ViaGotos) != 2 || rs.ViaGotos[0] != "upstream-goto" || !strings.HasSuffix(rs.ViaGotos[1], "(Proxy)(HTTP)") {
		t.Errorf("via gotos = %q", rs.ViaGotos)
	}
}
