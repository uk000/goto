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

package listeners

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	gototls "goto/pkg/tls"
	"goto/pkg/util"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// These tests check that an mTLS listener serves the connections that negotiated an HTTP/1.x ALPN protocol (e.g. Istio's
// "istio-http/1.1") with HTTP/1.x, without changing how HTTP/1.1, HTTP/2 and h2c requests are served.

type probeReq struct {
	Proto, Negotiated, ConnType string
	TLS                         bool
	Port                        int
	RemoteAddr                  string
}

// alpnProbe records what the handler sees of each request, and the states of the connections that the shared server tracks.
type alpnProbe struct {
	mu     sync.Mutex
	seen   []probeReq
	states map[string][]http.ConnState
}

func (p *alpnProbe) connState(c net.Conn, s http.ConnState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	k := c.RemoteAddr().String()
	p.states[k] = append(p.states[k], s)
}

func (p *alpnProbe) hasState(addr string, s http.ConnState) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, st := range p.states[addr] {
		if st == s {
			return true
		}
	}
	return false
}

func (p *alpnProbe) waitState(t *testing.T, addr string, s http.ConnState) {
	t.Helper()
	deadline := time.Now().Add(ioTimeout)
	for !p.hasState(addr, s) {
		if time.Now().After(deadline) {
			p.mu.Lock()
			defer p.mu.Unlock()
			t.Fatalf("connection %s never reached %s, states = %v", addr, s, p.states[addr])
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (p *alpnProbe) last() probeReq {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.seen) == 0 {
		return probeReq{}
	}
	return p.seen[len(p.seen)-1]
}

// swapProbeServer makes a shared HTTP server like the one that the server runs (an h2c handler, HTTP/2 configured, connection
// tracking), with a handler that records the requests, until the test is over.
func swapProbeServer(t *testing.T, idle time.Duration) *alpnProbe {
	t.Helper()
	p := &alpnProbe{states: map[string][]http.ConnState{}}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pr := probeReq{Proto: r.Proto, TLS: r.TLS != nil, ConnType: fmt.Sprintf("%T", util.GetConn(r)), RemoteAddr: r.RemoteAddr}
		if r.TLS != nil {
			pr.Negotiated = r.TLS.NegotiatedProtocol
		}
		pr.Port, _ = r.Context().Value(util.CurrentPortKey).(int)
		p.mu.Lock()
		p.seen = append(p.seen, pr)
		p.mu.Unlock()
		fmt.Fprintf(w, "%s|%s", r.Proto, pr.Negotiated)
	})
	h2s := &http2.Server{}
	server := &http.Server{
		Handler:      h2c.NewHandler(handler, h2s),
		IdleTimeout:  idle,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		ConnState:    p.connState,
	}
	if err := http2.ConfigureServer(server, h2s); err != nil {
		t.Fatal(err)
	}
	swapHTTPServer(t, server)
	return p
}

func openALPNListener(t *testing.T, protos ...string) *Listener {
	t.Helper()
	l := newListener(freePort(t), "https", "l.test", true)
	l.MTLS, l.AutoCert = true, true
	l.ALPN = &gototls.ALPN{Protos: protos}
	forget(t, l.Port)
	if err, msg := AddOrUpdateListener(l); err != nil {
		t.Fatalf("AddOrUpdateListener failed: %s", msg)
	}
	return l
}

func clientCert(t *testing.T) tls.Certificate {
	t.Helper()
	c, err := gototls.CreateCertificate([]string{"client.test"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	return *c
}

func dialALPN(t *testing.T, port int, cert tls.Certificate, offered ...string) *tls.Conn {
	t.Helper()
	c, err := tls.DialWithDialer(&net.Dialer{Timeout: dialTimeout}, "tcp", fmt.Sprintf("127.0.0.1:%d", port),
		&tls.Config{InsecureSkipVerify: true, Certificates: []tls.Certificate{cert}, NextProtos: offered})
	if err != nil {
		t.Fatalf("TLS dial offering %v: %v", offered, err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func rawGet(t *testing.T, c net.Conn, br *bufio.Reader, path string) (*http.Response, string) {
	t.Helper()
	c.SetDeadline(time.Now().Add(ioTimeout))
	if _, err := fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: goto.test\r\n\r\n", path); err != nil {
		t.Fatalf("writing request: %v", err)
	}
	req, _ := http.NewRequest("GET", path, nil)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		t.Fatalf("reading the HTTP/1.1 response failed (server spoke something else?): %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(body)
}

// h2Client makes HTTP/2 requests over TLS connections that offer the ALPN protocols, whatever they negotiate.
func h2Client(cert tls.Certificate, offered ...string) *http.Client {
	return &http.Client{Timeout: ioTimeout, Transport: &http2.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return tls.Dial(network, addr, &tls.Config{InsecureSkipVerify: true, Certificates: []tls.Certificate{cert}, NextProtos: offered})
		},
	}}
}

// The ALPN protocols that the listener serves, as the real config of goto in Istio has them plus the HTTP/1.x ones.
var istioProtos = []string{"istio-http/1.1", "istio-http/1.0", "istio-h2", "istio", "istio-peer-exchange"}

func TestHTTP1ALPNIsServedWithHTTP1(t *testing.T) {
	setup(t)
	p := swapProbeServer(t, 0)
	l := openALPNListener(t, istioProtos...)
	cert := clientCert(t)

	for _, alpn := range []string{"istio-http/1.1", "istio-http/1.0"} {
		t.Run(alpn, func(t *testing.T) {
			c := dialALPN(t, l.Port, cert, alpn)
			if got := c.ConnectionState().NegotiatedProtocol; got != alpn {
				t.Fatalf("negotiated %q, want %q", got, alpn)
			}
			br := bufio.NewReader(c)
			// several requests on the one connection: keep-alive works through the wrapped connection
			for i := 0; i < 3; i++ {
				resp, body := rawGet(t, c, br, fmt.Sprintf("/r%d", i))
				if resp.StatusCode != 200 || resp.ProtoMajor != 1 || body != "HTTP/1.1|"+alpn {
					t.Fatalf("request %d: status %d, HTTP/%d, body %q, want 200 HTTP/1 %q", i, resp.StatusCode, resp.ProtoMajor, body, "HTTP/1.1|"+alpn)
				}
				if resp.Close {
					t.Fatalf("request %d: connection is closed by the server, want keep-alive", i)
				}
			}
			seen := p.last()
			if !seen.TLS || seen.Negotiated != alpn || seen.ConnType != "*tls.Conn" || seen.Port != l.Port || seen.RemoteAddr == "" {
				t.Errorf("request as the handler sees it = %+v, want TLS state of %q, *tls.Conn in context, port %d, remote addr", seen, alpn, l.Port)
			}
			addr := c.LocalAddr().String()
			c.Close()
			// the server finishes with the connection: the handler of the ALPN returns, so the shared server sees it closed
			p.waitState(t, addr, http.StateClosed)
		})
	}
}

func TestHTTP1ALPNDoesNotChangeHTTP1HTTP2AndH2C(t *testing.T) {
	setup(t)
	p := swapProbeServer(t, 0)
	l := openALPNListener(t, istioProtos...)
	cert := clientCert(t)
	base := fmt.Sprintf("127.0.0.1:%d", l.Port)

	t.Run("http/1.1 ALPN", func(t *testing.T) {
		c := dialALPN(t, l.Port, cert, "http/1.1")
		br := bufio.NewReader(c)
		for i := 0; i < 2; i++ {
			resp, body := rawGet(t, c, br, "/")
			if resp.ProtoMajor != 1 || body != "HTTP/1.1|" {
				t.Fatalf("status %d, HTTP/%d, body %q, want HTTP/1.1 served by the HTTP/1.1 server with no ALPN protocol", resp.StatusCode, resp.ProtoMajor, body)
			}
		}
	})

	t.Run("no ALPN at all", func(t *testing.T) {
		c := dialALPN(t, l.Port, cert)
		br := bufio.NewReader(c)
		if resp, body := rawGet(t, c, br, "/"); resp.ProtoMajor != 1 || body != "HTTP/1.1|" {
			t.Fatalf("status %d, HTTP/%d, body %q", resp.StatusCode, resp.ProtoMajor, body)
		}
	})

	t.Run("h2", func(t *testing.T) {
		client := h2Client(cert, "h2")
		for i := 0; i < 3; i++ {
			resp, err := client.Get("https://" + base + "/")
			if err != nil {
				t.Fatalf("GET over h2: %v", err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.ProtoMajor != 2 || string(body) != "HTTP/2.0|h2" {
				t.Fatalf("response = HTTP/%d %q, want HTTP/2.0|h2", resp.ProtoMajor, body)
			}
		}
		if seen := p.last(); seen.ConnType != "*tls.Conn" || seen.Port != l.Port {
			t.Errorf("h2 request as the handler sees it = %+v", seen)
		}
	})

	t.Run("istio-h2 ALPN is HTTP/2", func(t *testing.T) {
		client := h2Client(cert, "istio-h2")
		resp, err := client.Get("https://" + base + "/")
		if err != nil {
			t.Fatalf("GET over HTTP/2 with ALPN istio-h2: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.ProtoMajor != 2 || string(body) != "HTTP/2.0|istio-h2" {
			t.Fatalf("response = HTTP/%d %q, want HTTP/2.0|istio-h2", resp.ProtoMajor, body)
		}
	})

	t.Run("cleartext HTTP/1.1 on the TLS port", func(t *testing.T) {
		client := &http.Client{Timeout: ioTimeout}
		resp, err := client.Get("http://" + base + "/")
		if err != nil {
			t.Fatalf("cleartext GET: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.ProtoMajor != 1 || string(body) != "HTTP/1.1|" {
			t.Fatalf("response = HTTP/%d %q, want HTTP/1.1 with no TLS", resp.ProtoMajor, body)
		}
		if seen := p.last(); seen.TLS {
			t.Errorf("cleartext request has TLS state: %+v", seen)
		}
	})

	t.Run("h2c with prior knowledge on the TLS port", func(t *testing.T) {
		client := &http.Client{Timeout: ioTimeout, Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				return net.Dial(network, addr)
			},
		}}
		resp, err := client.Get("http://" + base + "/")
		if err != nil {
			t.Fatalf("h2c GET: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.ProtoMajor != 2 || string(body) != "HTTP/2.0|" {
			t.Fatalf("response = HTTP/%d %q, want h2c HTTP/2.0", resp.ProtoMajor, body)
		}
	})

	t.Run("h2c upgrade on the TLS port", func(t *testing.T) {
		c, err := net.DialTimeout("tcp", base, dialTimeout)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(ioTimeout))
		fmt.Fprint(c, "GET / HTTP/1.1\r\nHost: goto.test\r\nConnection: Upgrade, HTTP2-Settings\r\nUpgrade: h2c\r\nHTTP2-Settings: AAMAAABkAARAAAAAAAIAAAAA\r\n\r\n")
		line, err := bufio.NewReader(c).ReadString('\n')
		if err != nil || !strings.HasPrefix(line, "HTTP/1.1 101") {
			t.Fatalf("h2c upgrade response = %q, %v, want 101 Switching Protocols", line, err)
		}
	})
}

// h2c on a plain HTTP listener, which the shared server serves as before, with no handler of ALPN involved.
func TestH2CAndHTTP1OnAPlainListenerAreUnchanged(t *testing.T) {
	setup(t)
	swapProbeServer(t, 0)
	l := addAndOpen(t, "http")
	base := fmt.Sprintf("127.0.0.1:%d", l.Port)
	h1 := &http.Client{Timeout: ioTimeout}
	resp, err := h1.Get("http://" + base + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.ProtoMajor != 1 || string(body) != "HTTP/1.1|" {
		t.Errorf("HTTP/1.1 response = HTTP/%d %q", resp.ProtoMajor, body)
	}
	h2 := &http.Client{Timeout: ioTimeout, Transport: &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return net.Dial(network, addr)
		},
	}}
	resp, err = h2.Get("http://" + base + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.ProtoMajor != 2 || string(body) != "HTTP/2.0|" {
		t.Errorf("h2c response = HTTP/%d %q", resp.ProtoMajor, body)
	}
}

// The timeouts of the server apply to the HTTP/1.x connections of the ALPN protocols: an idle connection is closed.
func TestHTTP1ALPNConnectionHonoursTheIdleTimeout(t *testing.T) {
	setup(t)
	p := swapProbeServer(t, 300*time.Millisecond)
	l := openALPNListener(t, istioProtos...)
	c := dialALPN(t, l.Port, clientCert(t), "istio-http/1.1")
	br := bufio.NewReader(c)
	rawGet(t, c, br, "/")
	start := time.Now()
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := br.ReadByte(); err != io.EOF {
		t.Fatalf("idle connection read = %v, want EOF from the server closing it", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("idle connection was closed after %s, want about the idle timeout (300ms)", took)
	}
	p.waitState(t, c.LocalAddr().String(), http.StateClosed)
}

// What clients that offer several ALPN protocols (as Envoy does) get is decided by the order of the protocols of the
// listener: the first of them that the client offers. A protocol that is listed ahead of the HTTP/1.x one (e.g. "istio") wins
// and is served with HTTP/2: that is how the listener is configured, and not something that the HTTP/1.x ALPN handler can
// tell, so the HTTP/1.x protocols have to be listed first.
func TestHTTP1ALPNNegotiationOrder(t *testing.T) {
	setup(t)
	swapProbeServer(t, 0)
	cert := clientCert(t)
	for _, tt := range []struct {
		name     string
		protos   []string
		offered  []string
		wantAlpn string
		wantH1   bool
	}{
		{"http first in listener", []string{"istio-http/1.1", "istio"}, []string{"istio", "istio-http/1.1"}, "istio-http/1.1", true},
		{"istio first in listener (known limitation: served with HTTP/2)", []string{"istio", "istio-http/1.1"}, []string{"istio-http/1.1", "istio"}, "istio", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			l := openALPNListener(t, tt.protos...)
			c := dialALPN(t, l.Port, cert, tt.offered...)
			got := c.ConnectionState().NegotiatedProtocol
			t.Logf("listener protos %v, client offers %v: negotiated %q", tt.protos, tt.offered, got)
			if got != tt.wantAlpn {
				t.Fatalf("negotiated %q, want %q", got, tt.wantAlpn)
			}
			c.SetDeadline(time.Now().Add(ioTimeout))
			fmt.Fprint(c, "GET / HTTP/1.1\r\nHost: goto.test\r\n\r\n")
			buf := make([]byte, 5)
			io.ReadFull(c, buf)
			isH1 := string(buf) == "HTTP/"
			t.Logf("first bytes of the answer to an HTTP/1.1 request: %q (HTTP/1.x: %t)", buf, isH1)
			if isH1 != tt.wantH1 {
				t.Errorf("answered with HTTP/1.x = %t, want %t", isH1, tt.wantH1)
			}
		})
	}
}
