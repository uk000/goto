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

package transport

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeProxy is a proxy that tunnels what it is asked to (CONNECT), and answers on its own to the requests that are made to it
// as a proxy (the ones that have a full URL as their target), keeping track of both.
type fakeProxy struct {
	ln       net.Listener
	lock     sync.Mutex
	connects []string
	headers  []http.Header
	plain    []string
	conns    []net.Conn
}

// startFakeProxy starts a proxy on the loopback address, which is reached over TLS if the config for it is given.
func startFakeProxy(t *testing.T, tlsConfig *tls.Config) *fakeProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if tlsConfig != nil {
		ln = tls.NewListener(ln, tlsConfig)
	}
	p := &fakeProxy{ln: ln}
	go p.accept()
	t.Cleanup(p.close)
	return p
}

// proxyTLSConfig is a TLS config for a proxy, with the certificate that the test servers have.
func proxyTLSConfig(t *testing.T, onHello func(*tls.ClientHelloInfo)) *tls.Config {
	t.Helper()
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	cert := srv.TLS.Certificates[0]
	srv.Close()
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		GetConfigForClient: func(chi *tls.ClientHelloInfo) (*tls.Config, error) {
			if onHello != nil {
				onHello(chi)
			}
			return nil, nil
		},
	}
}

func (p *fakeProxy) accept() {
	for {
		conn, err := p.ln.Accept()
		if err != nil {
			return
		}
		p.lock.Lock()
		p.conns = append(p.conns, conn)
		p.lock.Unlock()
		go p.serve(conn)
	}
}

func (p *fakeProxy) close() {
	p.ln.Close()
	p.lock.Lock()
	defer p.lock.Unlock()
	for _, c := range p.conns {
		c.Close()
	}
}

func (p *fakeProxy) serve(conn net.Conn) {
	defer conn.Close()
	br := bufio.NewReader(conn)
	for {
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		if req.Method != http.MethodConnect {
			p.lock.Lock()
			p.plain = append(p.plain, req.Method+" "+req.RequestURI)
			p.lock.Unlock()
			io.Copy(io.Discard, req.Body)
			fmt.Fprint(conn, "HTTP/1.1 200 OK\r\nContent-Length: 7\r\n\r\nproxied")
			continue
		}
		p.lock.Lock()
		p.connects = append(p.connects, req.Host)
		p.headers = append(p.headers, req.Header.Clone())
		p.lock.Unlock()
		dst, err := net.Dial("tcp", req.Host)
		if err != nil {
			fmt.Fprint(conn, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
			return
		}
		defer dst.Close()
		fmt.Fprint(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
		done := make(chan struct{}, 2)
		go func() { io.Copy(dst, br); done <- struct{}{} }()
		go func() { io.Copy(conn, dst); done <- struct{}{} }()
		<-done
		return
	}
}

func (p *fakeProxy) seenConnects() []string {
	p.lock.Lock()
	defer p.lock.Unlock()
	return append([]string(nil), p.connects...)
}

func (p *fakeProxy) seenConnectHeaders() []http.Header {
	p.lock.Lock()
	defer p.lock.Unlock()
	return append([]http.Header(nil), p.headers...)
}

func (p *fakeProxy) seenPlain() []string {
	p.lock.Lock()
	defer p.lock.Unlock()
	return append([]string(nil), p.plain...)
}

// url is the URL of the proxy, for the scheme and the host name given.
func (p *fakeProxy) url(scheme, host string) *url.URL {
	_, port, _ := net.SplitHostPort(p.ln.Addr().String())
	return &url.URL{Scheme: scheme, Host: net.JoinHostPort(host, port)}
}

func hostPort(t *testing.T, serverURL string) string {
	t.Helper()
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

func TestProxyTunnelsRequestsForHTTPSTargets(t *testing.T) {
	var lock sync.Mutex
	var snis []string
	srv := newTLSServer(t, false, func(sni string) {
		lock.Lock()
		defer lock.Unlock()
		snis = append(snis, sni)
	})
	proxy := startFakeProxy(t, nil)
	ct := newClient(false, true)
	defer ct.Close()
	if err := ct.(ProxyClient).SetProxy(proxy.url("http", "127.0.0.1")); err != nil {
		t.Fatal(err)
	}
	ct.UpdateTLSConfig("sni.example", 0, false, nil)
	got, err := doGet(ct, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if got.body != "HTTP/1.1" {
		t.Errorf("body = %q", got.body)
	}
	if c := proxy.seenConnects(); len(c) != 1 || c[0] != hostPort(t, srv.URL) {
		t.Errorf("proxy was asked to connect to %v, want [%s]", c, hostPort(t, srv.URL))
	}
	if p := proxy.seenPlain(); len(p) != 0 {
		t.Errorf("proxy got requests that are not tunnels: %v", p)
	}
	// the TLS handshake with the target takes the settings of the transport, even though net/http does it on its own
	lock.Lock()
	defer lock.Unlock()
	if len(snis) != 1 || snis[0] != "sni.example" {
		t.Errorf("the target got SNI %v, want [sni.example]", snis)
	}
}

func TestProxyTunnelKeepsPeerCertInfo(t *testing.T) {
	srv := newTLSServer(t, false, nil)
	proxy := startFakeProxy(t, nil)
	ct := newClient(false, true)
	defer ct.Close()
	if err := ct.(ProxyClient).SetProxy(proxy.url("http", "127.0.0.1")); err != nil {
		t.Fatal(err)
	}
	ct.UpdateTLSConfig("sni.example", 0, false, nil)
	if _, err := doGet(ct, srv.URL); err != nil {
		t.Fatal(err)
	}
	pci := ct.GetPeerCertInfo()
	if pci == nil || pci.SNI != "sni.example" || pci.Subject == "" {
		t.Errorf("peer cert info = %+v", pci)
	}
}

func TestProxyTunnelAppliesTLSVersion(t *testing.T) {
	srv := newTLSServer(t, false, nil)
	proxy := startFakeProxy(t, nil)
	ct := newClient(false, true)
	defer ct.Close()
	if err := ct.(ProxyClient).SetProxy(proxy.url("http", "127.0.0.1")); err != nil {
		t.Fatal(err)
	}
	ct.UpdateTLSConfig("sni.example", tls.VersionTLS12, false, nil)
	if _, err := doGet(ct, srv.URL); err != nil {
		t.Fatal(err)
	}
	if pci := ct.GetPeerCertInfo(); pci == nil {
		t.Fatal("no peer cert info")
	}
	// a version that the server doesn't have fails the handshake that net/http does through the tunnel
	other := newClient(false, true)
	defer other.Close()
	if err := other.(ProxyClient).SetProxy(proxy.url("http", "127.0.0.1")); err != nil {
		t.Fatal(err)
	}
	other.UpdateTLSConfig("sni.example", tls.VersionSSL30, false, nil)
	if _, err := doGet(other, srv.URL); err == nil {
		t.Error("a TLS version that the server doesn't support was applied by none")
	}
}

func TestProxyVerifiesTheTargetWithTheSettingsOfTheTransport(t *testing.T) {
	srv := newTLSServer(t, false, nil)
	proxy := startFakeProxy(t, nil)
	ct := newClient(false, true)
	defer ct.Close()
	if err := ct.(ProxyClient).SetProxy(proxy.url("http", "127.0.0.1")); err != nil {
		t.Fatal(err)
	}
	// the certificate of the server is not trusted, so a call that verifies it fails
	ct.UpdateTLSConfig("sni.example", 0, true, nil)
	if _, err := doGet(ct, srv.URL); err == nil {
		t.Fatal("the certificate was verified by none")
	}
	// and a call that doesn't succeeds
	ct.UpdateTLSConfig("sni.example", 0, false, nil)
	if _, err := doGet(ct, srv.URL); err != nil {
		t.Fatal(err)
	}
}

// newDirectServer is a plain http server that says it was reached.
func newDirectServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "direct")
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestProxyTunnelsRequestsForHTTPTargets(t *testing.T) {
	target := newDirectServer(t)
	proxy := startFakeProxy(t, nil)
	ct := newClient(false, true)
	defer ct.Close()
	if err := ct.(ProxyClient).SetProxy(proxy.url("http", "127.0.0.1")); err != nil {
		t.Fatal(err)
	}
	// the proxy is asked for a tunnel, and not sent the request, which goes through the tunnel to the target; and the
	// requests share the tunnel
	for i := 0; i < 3; i++ {
		got, err := doGet(ct, target.URL+"/some/path?q=1")
		if err != nil {
			t.Fatal(err)
		}
		if got.body != "direct" {
			t.Errorf("body = %q, the request didn't reach the target", got.body)
		}
	}
	if c := proxy.seenConnects(); len(c) != 1 || c[0] != hostPort(t, target.URL) {
		t.Errorf("proxy was asked to connect to %v, want one tunnel to [%s]", c, hostPort(t, target.URL))
	}
	if p := proxy.seenPlain(); len(p) != 0 {
		t.Errorf("proxy got requests that are not tunnels: %v", p)
	}
}

func TestProxyForLoopbackTargets(t *testing.T) {
	// the proxy of the environment is not used for loopback addresses, but the one that was set is
	target := newDirectServer(t)
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("http_proxy", "")
	proxy := startFakeProxy(t, nil)
	ct := newClient(false, true)
	defer ct.Close()
	got, err := doGet(ct, target.URL)
	if err != nil || got.body != "direct" {
		t.Fatalf("without a proxy: body = %q, err = %v", got.body, err)
	}
	if c := proxy.seenConnects(); len(c) != 0 {
		t.Fatalf("the proxy was used before it was set: %v", c)
	}
	if err := ct.(ProxyClient).SetProxy(proxy.url("http", "127.0.0.1")); err != nil {
		t.Fatal(err)
	}
	got, err = doGet(ct, target.URL)
	if err != nil || got.body != "direct" {
		t.Fatalf("with a proxy: body = %q, err = %v", got.body, err)
	}
	if c := proxy.seenConnects(); len(c) != 1 {
		t.Fatalf("with a proxy, it was asked for %v, want one tunnel", c)
	}
	// and the requests go direct again when the proxy is taken away
	if err := ct.(ProxyClient).SetProxy(nil); err != nil {
		t.Fatal(err)
	}
	got, err = doGet(ct, target.URL)
	if err != nil || got.body != "direct" {
		t.Fatalf("after the proxy was taken away: body = %q, err = %v", got.body, err)
	}
	if c := proxy.seenConnects(); len(c) != 1 {
		t.Errorf("the proxy was used after it was taken away: %v", c)
	}
}

func TestProxyCredentialsAreSentWithTheRequestForATunnel(t *testing.T) {
	httpTarget := newDirectServer(t)
	httpsTarget := newTLSServer(t, false, nil)
	proxy := startFakeProxy(t, nil)
	ct := newClient(false, true)
	defer ct.Close()
	u := proxy.url("http", "127.0.0.1")
	u.User = url.UserPassword("user", "secret")
	if err := ct.(ProxyClient).SetProxy(u); err != nil {
		t.Fatal(err)
	}
	ct.UpdateTLSConfig("sni.example", 0, false, nil)
	for _, target := range []string{httpTarget.URL, httpsTarget.URL} {
		if _, err := doGet(ct, target); err != nil {
			t.Fatal(err)
		}
	}
	headers := proxy.seenConnectHeaders()
	if len(headers) != 2 {
		t.Fatalf("proxy was asked for %d tunnels, want 2", len(headers))
	}
	for i, h := range headers {
		if got := h.Get("Proxy-Authorization"); got != "Basic dXNlcjpzZWNyZXQ=" {
			t.Errorf("tunnel %d: Proxy-Authorization = %q", i+1, got)
		}
	}
}

func TestProxyRefusesTunnelForHTTPTargets(t *testing.T) {
	target := newDirectServer(t)
	refuse := startRefusingProxy(t)
	ct := newClient(false, true)
	defer ct.Close()
	if err := ct.(ProxyClient).SetProxy(&url.URL{Scheme: "http", Host: refuse}); err != nil {
		t.Fatal(err)
	}
	_, err := doGet(ct, target.URL)
	if err == nil || !strings.Contains(err.Error(), "refused a tunnel") || !strings.Contains(err.Error(), "405 Method Not Allowed") {
		t.Errorf("err = %v, want one about the 405 of the proxy", err)
	}
}

func TestProxyUnreachableForHTTPTargets(t *testing.T) {
	target := newDirectServer(t)
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := closed.Addr().String()
	closed.Close()
	ct := newClient(false, true)
	defer ct.Close()
	if err := ct.(ProxyClient).SetProxy(&url.URL{Scheme: "http", Host: addr}); err != nil {
		t.Fatal(err)
	}
	if _, err := doGet(ct, target.URL); err == nil {
		t.Error("the target was reached without the proxy")
	}
}

func TestProxyTunnelForHTTPTargetEndsWithTheRequest(t *testing.T) {
	// a proxy that takes the request for a tunnel and never answers it
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			go io.Copy(io.Discard, conn)
		}
	}()
	target := newDirectServer(t)
	ct := newClient(false, true)
	defer ct.Close()
	if err := ct.(ProxyClient).SetProxy(&url.URL{Scheme: "http", Host: ln.Addr().String()}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", target.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	resp, err := ct.HTTP().Do(req)
	if err == nil {
		resp.Body.Close()
		t.Fatal("the request got an answer from a proxy that doesn't answer")
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("the request took %v to end, it was to end with its context", took)
	}
}

func TestOpenTunnelKeepsWhatComesWithTheAnswer(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	go func() {
		br := bufio.NewReader(server)
		req, err := http.ReadRequest(br)
		if err != nil || req.Method != http.MethodConnect || req.Host != "target.example:80" || req.RequestURI != "target.example:80" {
			server.Close()
			return
		}
		// what the destination says first comes in the same write as the answer
		io.WriteString(server, "HTTP/1.1 200 Connection Established\r\nVia-Goto: p\r\n\r\nHELLO")
	}()
	conn, err := openTunnel(context.Background(), client, &url.URL{Scheme: "http", Host: "proxy.example"}, "target.example:80")
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "HELLO" {
		t.Errorf("read %q, %v, want what the destination said first", buf, err)
	}
}

// startRefusingProxy is a proxy that answers every request with 405, which is what a port that doesn't serve the request
// does. It gives its address.
func startRefusingProxy(t *testing.T) string {
	t.Helper()
	refuse, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { refuse.Close() })
	go func() {
		for {
			conn, err := refuse.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				if _, err := http.ReadRequest(bufio.NewReader(conn)); err == nil {
					fmt.Fprint(conn, "HTTP/1.1 405 Method Not Allowed\r\nAllow: GET\r\nContent-Length: 0\r\n\r\n")
				}
			}()
		}
	}()
	return refuse.Addr().String()
}

func TestProxyOverTLS(t *testing.T) {
	var lock sync.Mutex
	var targetSNIs []string
	var proxyHellos []*tls.ClientHelloInfo
	srv := newTLSServer(t, false, func(sni string) {
		lock.Lock()
		defer lock.Unlock()
		targetSNIs = append(targetSNIs, sni)
	})
	proxy := startFakeProxy(t, proxyTLSConfig(t, func(chi *tls.ClientHelloInfo) {
		lock.Lock()
		defer lock.Unlock()
		proxyHellos = append(proxyHellos, chi)
	}))
	ct := newClient(false, true)
	defer ct.Close()
	// the proxy is reached by a name of its own, which the connection to it is for, and not the name of the target
	if err := ct.(ProxyClient).SetProxy(proxy.url("https", "localhost")); err != nil {
		t.Fatal(err)
	}
	ct.UpdateTLSConfig("sni.example", tls.VersionTLS12, false, nil)
	got, err := doGet(ct, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if got.body != "HTTP/1.1" {
		t.Errorf("body = %q", got.body)
	}
	if c := proxy.seenConnects(); len(c) != 1 || c[0] != hostPort(t, srv.URL) {
		t.Errorf("proxy was asked to connect to %v", c)
	}
	lock.Lock()
	defer lock.Unlock()
	if len(targetSNIs) != 1 || targetSNIs[0] != "sni.example" {
		t.Errorf("the target got SNI %v, want [sni.example]", targetSNIs)
	}
	if len(proxyHellos) != 1 {
		t.Fatalf("the proxy got %d TLS connections", len(proxyHellos))
	}
	if h := proxyHellos[0]; h.ServerName != "localhost" || len(h.SupportedProtos) != 0 {
		t.Errorf("the proxy got SNI [%s] and ALPN %v, want [localhost] and none", h.ServerName, h.SupportedProtos)
	}
}

func TestProxyOverTLSForHTTPTargets(t *testing.T) {
	target := newDirectServer(t)
	var lock sync.Mutex
	var proxyHellos []*tls.ClientHelloInfo
	proxy := startFakeProxy(t, proxyTLSConfig(t, func(chi *tls.ClientHelloInfo) {
		lock.Lock()
		defer lock.Unlock()
		proxyHellos = append(proxyHellos, chi)
	}))
	ct := newClient(false, true)
	defer ct.Close()
	if err := ct.(ProxyClient).SetProxy(proxy.url("https", "localhost")); err != nil {
		t.Fatal(err)
	}
	got, err := doGet(ct, target.URL)
	if err != nil || got.body != "direct" {
		t.Fatalf("body = %q, err = %v", got.body, err)
	}
	// the request goes through a tunnel that is asked for over TLS
	if c := proxy.seenConnects(); len(c) != 1 || c[0] != hostPort(t, target.URL) {
		t.Errorf("proxy was asked to connect to %v, want one tunnel to [%s]", c, hostPort(t, target.URL))
	}
	lock.Lock()
	defer lock.Unlock()
	if len(proxyHellos) != 1 || proxyHellos[0].ServerName != "localhost" || len(proxyHellos[0].SupportedProtos) != 0 {
		t.Errorf("the proxy got TLS connections %+v, want one with SNI [localhost] and no ALPN", proxyHellos)
	}
}

func TestProxyUnreachable(t *testing.T) {
	srv := newTLSServer(t, false, nil)
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := closed.Addr().String()
	closed.Close()
	ct := newClient(false, true)
	defer ct.Close()
	if err := ct.(ProxyClient).SetProxy(&url.URL{Scheme: "http", Host: addr}); err != nil {
		t.Fatal(err)
	}
	if _, err := doGet(ct, srv.URL); err == nil {
		t.Error("the target was reached without the proxy")
	}
}

func TestProxyRefusesTunnel(t *testing.T) {
	srv := newTLSServer(t, false, nil)
	refuse := startRefusingProxy(t)
	ct := newClient(false, true)
	defer ct.Close()
	if err := ct.(ProxyClient).SetProxy(&url.URL{Scheme: "http", Host: refuse}); err != nil {
		t.Fatal(err)
	}
	_, err := doGet(ct, srv.URL)
	// net/http reports the status text of the proxy, with no code
	if err == nil || !strings.Contains(err.Error(), "Method Not Allowed") {
		t.Errorf("err = %v, want one about the 405 of the proxy", err)
	}
}

func TestProxyIsNotSupportedByTheH2Transport(t *testing.T) {
	proxy := startFakeProxy(t, nil)
	ct := newClient(true, false)
	defer ct.Close()
	if err := ct.(ProxyClient).SetProxy(proxy.url("http", "127.0.0.1")); err == nil {
		t.Error("an HTTP/2 transport was given a proxy, which it would have ignored")
	}
	if err := ct.(ProxyClient).SetProxy(nil); err != nil {
		t.Errorf("taking the proxy away from an HTTP/2 transport: %v", err)
	}
}

func TestParseProxyURL(t *testing.T) {
	tests := []struct {
		in     string
		want   string
		errHas string
	}{
		{in: "http://proxy.example:3128", want: "http://proxy.example:3128"},
		{in: "https://proxy.example", want: "https://proxy.example"},
		{in: "  http://proxy.example:3128  ", want: "http://proxy.example:3128"},
		{in: "HTTP://proxy.example:3128", want: "http://proxy.example:3128"},
		{in: "proxy.example:3128", want: "http://proxy.example:3128"},
		{in: "localhost:9000", want: "http://localhost:9000"},
		{in: "http://user:secret@proxy.example:3128", want: "http://user:secret@proxy.example:3128"},
		{in: "http://[::1]:3128", want: "http://[::1]:3128"},
		{in: "", errHas: "empty"},
		{in: "   ", errHas: "empty"},
		{in: "socks5://proxy.example:1080", errHas: "not supported"},
		{in: "ftp://proxy.example", errHas: "not supported"},
		{in: "http://", errHas: "no host"},
		{in: "http://:3128", errHas: "no host"},
		{in: "http://proxy.example:port", errHas: "invalid"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			u, err := ParseProxyURL(tc.in)
			if tc.errHas != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errHas) {
					t.Fatalf("ParseProxyURL(%q) = %v, %v; want an error with %q", tc.in, u, err, tc.errHas)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if u.String() != tc.want {
				t.Errorf("ParseProxyURL(%q) = %s, want %s", tc.in, u, tc.want)
			}
		})
	}
}

func TestParseProxyURLKeepsThePasswordOutOfErrors(t *testing.T) {
	_, err := ParseProxyURL("http://user:s3cret@proxy.example:port")
	if err == nil {
		t.Fatal("no error")
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Errorf("the error has the password: %v", err)
	}
}

func TestProxyAddress(t *testing.T) {
	tests := map[string]string{
		"http://proxy.example":        "proxy.example:80",
		"https://proxy.example":       "proxy.example:443",
		"http://proxy.example:3128":   "proxy.example:3128",
		"https://proxy.example:8443":  "proxy.example:8443",
		"http://[::1]:3128":           "[::1]:3128",
		"https://u:p@proxy.example/x": "proxy.example:443",
	}
	for in, want := range tests {
		u, err := url.Parse(in)
		if err != nil {
			t.Fatal(err)
		}
		if got := proxyAddress(u); got != want {
			t.Errorf("proxyAddress(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestProxyTunnelsHTTP2Targets(t *testing.T) {
	srv := newTLSServer(t, true, nil)
	proxy := startFakeProxy(t, nil)
	direct := newClient(false, true)
	defer direct.Close()
	want, err := doGet(direct, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if want.major != 2 {
		t.Fatalf("a call without a proxy got HTTP/%d, the server is expected to have HTTP/2", want.major)
	}
	for _, scheme := range []string{"http", "https"} {
		t.Run(scheme+" proxy", func(t *testing.T) {
			p := proxy
			host := "127.0.0.1"
			if scheme == "https" {
				p = startFakeProxy(t, proxyTLSConfig(t, nil))
				host = "localhost"
			}
			ct := newClient(false, true)
			defer ct.Close()
			if err := ct.(ProxyClient).SetProxy(p.url(scheme, host)); err != nil {
				t.Fatal(err)
			}
			ct.UpdateTLSConfig("sni.example", 0, false, nil)
			// two calls, which share the tunnel as the HTTP/2 connection is shared
			for i := 0; i < 2; i++ {
				got, err := doGet(ct, srv.URL)
				if err != nil {
					t.Fatal(err)
				}
				if got != want {
					t.Errorf("call %d via the proxy got %+v, want what a call without it gets: %+v", i+1, got, want)
				}
			}
			if c := p.seenConnects(); len(c) != 1 || c[0] != hostPort(t, srv.URL) {
				t.Errorf("proxy was asked to connect to %v, want one tunnel to [%s]", c, hostPort(t, srv.URL))
			}
		})
	}
}
