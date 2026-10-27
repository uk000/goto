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

package conn

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/server/listeners"
	"goto/pkg/server/tcp"
	"goto/pkg/util"
)

var (
	stubOnce sync.Once
	portSeq  atomic.Int32
)

const listenerLabel = "test-listener"

func stubGlobalFuncs() {
	global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsListenerPresent = func(int) bool { return false }
	global.Funcs.IsListenerOpen = func(int) bool { return false }
	global.Funcs.GetListenerID = func(int) string { return "" }
	global.Funcs.GetListenerLabel = func(*http.Request) string { return listenerLabel }
	global.Funcs.GetListenerLabelForPort = func(int) string { return listenerLabel }
	util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
}

// useDefaultListener makes the default listener, which the requests of ports that have no listener are served by, the
// one that the test needs, for the length of the test. It is the listener of the port that is given.
func useDefaultListener(t *testing.T, port int, tlsListener, mtls bool, ln net.Listener) {
	t.Helper()
	l := listeners.DefaultListener
	savedPort, savedLabel, savedHostLabel, savedTLS, savedMTLS, savedLn := l.Port, l.Label, l.HostLabel, l.TLS, l.MTLS, l.Listener
	l.Port, l.Label, l.HostLabel, l.TLS, l.MTLS, l.Listener = port, "label-of-listener", "host-of-listener", tlsListener, mtls, ln
	t.Cleanup(func() {
		l.Port, l.Label, l.HostLabel, l.TLS, l.MTLS, l.Listener = savedPort, savedLabel, savedHostLabel, savedTLS, savedMTLS, savedLn
	})
}

func newPort() int { return int(43000 + portSeq.Add(1)) }

type testRequest struct {
	req  *http.Request
	rs   *util.RequestStore
	port int
}

func newRequest(t *testing.T, port int, mutate func(*http.Request), c net.Conn) *testRequest {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
	req := httptest.NewRequest("GET", "/some/uri?x=1", nil)
	req.RemoteAddr = "10.1.2.3:5555"
	if mutate != nil {
		mutate(req)
	}
	ctx := context.WithValue(req.Context(), util.CurrentPortKey, port)
	if c != nil {
		ctx = context.WithValue(ctx, util.ConnectionKey, c)
	}
	req = req.WithContext(ctx)
	_, req, rs := util.WithRequestStore(req, nil)
	return &testRequest{req: req, rs: rs, port: port}
}

type served struct {
	rr        *httptest.ResponseRecorder
	nextCalls int
	nextSaw   http.Header
}

func serve(tr *testRequest, withNext bool) *served {
	s := &served{rr: httptest.NewRecorder()}
	var next http.Handler
	if withNext {
		next = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.nextCalls++
			s.nextSaw = w.Header().Clone()
		})
	}
	Middleware.MiddlewareHandler(next).ServeHTTP(s.rr, tr.req)
	return s
}

func localListener(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("cannot listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln
}

func logOf(tr *testRequest) string { return strings.Join(tr.rs.LogMessages, "\n") }

func TestMiddlewareHasNoRoutes(t *testing.T) {
	if Middleware.SetRoutes != nil {
		t.Error("the connection middleware has no routes of its own")
	}
	if Middleware.MiddlewareHandler == nil || Middleware.Name != "connection" {
		t.Errorf("middleware = %+v, want the connection middleware with a handler", Middleware)
	}
}

func TestMiddlewareSendsGotoHeadersAndCallsNext(t *testing.T) {
	port := newPort()
	ln := localListener(t)
	useDefaultListener(t, port, false, false, ln)
	tr := newRequest(t, port, nil, nil)
	s := serve(tr, true)
	if s.nextCalls != 1 {
		t.Errorf("next was called %d times, want once", s.nextCalls)
	}
	h := s.rr.Header()
	for name, want := range map[string]string{
		constants.HeaderViaGoto:           listenerLabel + "(HTTP)",
		constants.HeaderGotoRemoteAddress: "10.1.2.3:5555",
		constants.HeaderGotoPort:          strconv.Itoa(port),
		constants.HeaderGotoProtocol:      "HTTP/1.1",
		constants.HeaderGotoTLS:           "false",
		constants.HeaderGotoMTLS:          "false",
		constants.HeaderGotoHost:          global.Self.HostLabel,
	} {
		if got := h.Get(name); got != want {
			t.Errorf("header %s = %q, want %q", name, got, want)
		}
	}
	// The headers were there when the handler was called, so that it can add to them.
	if s.nextSaw.Get(constants.HeaderGotoProtocol) != "HTTP/1.1" {
		t.Error("the Goto headers must be set before the next handler is called")
	}
	if tr.rs.ListenerLabel != "label-of-listener" || tr.rs.HostLabel != "host-of-listener" || tr.rs.GotoProtocol != "HTTP/1.1" {
		t.Errorf("request store = listener label %q, host label %q, protocol %q, want those of the listener", tr.rs.ListenerLabel, tr.rs.HostLabel, tr.rs.GotoProtocol)
	}
	if !tr.rs.IsHeadersSent {
		t.Error("the request store must tell that the headers are sent")
	}
}

func TestMiddlewareWithoutNextHandler(t *testing.T) {
	port := newPort()
	useDefaultListener(t, port, false, false, localListener(t))
	tr := newRequest(t, port, nil, nil)
	s := serve(tr, false)
	if s.rr.Header().Get(constants.HeaderGotoProtocol) == "" {
		t.Error("the Goto headers must be sent without a next handler too")
	}
}

func TestMiddlewareTellsTheProtocol(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*http.Request)
		want   string
	}{
		{"http1", nil, "HTTP/1.1"},
		{"h2c", func(r *http.Request) { r.ProtoMajor, r.Proto = 2, "HTTP/2.0" }, "H2C"},
		{"grpc", func(r *http.Request) {
			r.ProtoMajor, r.Proto = 2, "HTTP/2.0"
			r.Header.Set(constants.HeaderContentType, "application/grpc")
		}, "GRPC"},
		{"https", func(r *http.Request) { r.TLS = &tls.ConnectionState{} }, "HTTPS"},
		{"h2 over tls", func(r *http.Request) { r.TLS = &tls.ConnectionState{}; r.ProtoMajor, r.Proto = 2, "HTTP/2.0" }, "HTTP/2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			port := newPort()
			useDefaultListener(t, port, false, false, localListener(t))
			tr := newRequest(t, port, tt.mutate, nil)
			s := serve(tr, true)
			if got := s.rr.Header().Get(constants.HeaderGotoProtocol); got != tt.want || tr.rs.GotoProtocol != tt.want {
				t.Errorf("protocol = header %q, request store %q, want %q", got, tr.rs.GotoProtocol, tt.want)
			}
			if !strings.Contains(logOf(tr), "Goto-Protocol: ["+tt.want+"]") {
				t.Errorf("log = %q, want the protocol in it", logOf(tr))
			}
		})
	}
}

func setLogRequestHeaders(t *testing.T, value bool) {
	t.Helper()
	saved := global.Flags.LogRequestHeaders
	global.Flags.LogRequestHeaders = value
	t.Cleanup(func() { global.Flags.LogRequestHeaders = saved })
}

func TestMiddlewareLogsTheRequest(t *testing.T) {
	setLogRequestHeaders(t, false)
	port := newPort()
	ln := localListener(t)
	useDefaultListener(t, port, false, false, ln)
	tr := newRequest(t, port, func(r *http.Request) {
		r.Header.Set("Content-Length", "42")
		r.Header.Set(constants.HeaderGotoTargetURL, "http://target/x")
	}, nil)
	serve(tr, true)
	want := fmt.Sprintf("Goto: [label-of-listener] LocalAddr: [%s], RemoteAddr: [10.1.2.3:5555], RequestHost: [example.com], URI: [/some/uri?x=1], Method: [GET], Protocol: [HTTP/1.1], Goto-Protocol: [HTTP/1.1], ContentLength: [42], GotoTargetURL: [http://target/x]", ln.Addr())
	if got := logOf(tr); got != want {
		t.Errorf("log =\n%s\nwant\n%s", got, want)
	}
}

func TestMiddlewareLogsRequestHeadersWhenAsked(t *testing.T) {
	setLogRequestHeaders(t, true)
	port := newPort()
	useDefaultListener(t, port, false, false, localListener(t))
	tr := newRequest(t, port, func(r *http.Request) { r.Header.Set("X-Logged", "yes") }, nil)
	serve(tr, true)
	if got := logOf(tr); !strings.Contains(got, ", Request Headers: [") || !strings.Contains(got, "X-Logged") {
		t.Errorf("log = %q, want the request headers in it", got)
	}
	global.Flags.LogRequestHeaders = false
	tr = newRequest(t, port, func(r *http.Request) { r.Header.Set("X-Logged", "yes") }, nil)
	serve(tr, true)
	if got := logOf(tr); strings.Contains(got, "Request Headers") {
		t.Errorf("log = %q, want no request headers when not asked", got)
	}
}

func TestMiddlewareUsesTheLocalAddressOfTheConnection(t *testing.T) {
	port := newPort()
	ln := localListener(t)
	useDefaultListener(t, port, false, false, ln)
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	tr := newRequest(t, port, nil, a)
	serve(tr, true)
	// A connection gives the address that it has, and the listener is not looked at.
	if got := logOf(tr); !strings.Contains(got, "LocalAddr: ["+a.LocalAddr().String()+"]") || strings.Contains(got, ln.Addr().String()) {
		t.Errorf("log = %q, want the local address of the connection", got)
	}
}

func TestMiddlewareFindsTheListenerOfThePort(t *testing.T) {
	// Requests of a port that has no listener are served as the default listener does.
	port := newPort()
	useDefaultListener(t, port+1000, false, false, localListener(t))
	tr := newRequest(t, port, nil, nil)
	serve(tr, true)
	if tr.rs.ListenerLabel != "label-of-listener" {
		t.Errorf("listener label = %q, want the one of the default listener", tr.rs.ListenerLabel)
	}
}

func TestMiddlewareHeadersOfTunnelRequestsHaveTheTunnelCount(t *testing.T) {
	port := newPort()
	useDefaultListener(t, port, false, false, localListener(t))
	tr := newRequest(t, port, nil, nil)
	tr.rs.IsTunnelRequest = true
	tr.rs.TunnelCount = 2
	tr.rs.ServerName = "sni.example"
	s := serve(tr, true)
	h := s.rr.Header()
	for name, want := range map[string]string{
		constants.HeaderGotoRemoteAddress: "10.1.2.3:5555",
		constants.HeaderGotoPort:          strconv.Itoa(port),
		constants.HeaderGotoTunnelHost:    "host-of-listener",
		constants.HeaderViaGotoTunnel:     "label-of-listener",
		constants.HeaderGotoProtocol:      "HTTP/1.1",
		constants.HeaderGotoSNI:           "sni.example",
	} {
		if got := h.Get(name + "_2"); got != want {
			t.Errorf("header %s_2 = %q, want %q", name, got, want)
		}
		if got := h.Get(name); got != "" {
			t.Errorf("header %s = %q, want only the one with the tunnel count", name, got)
		}
	}
	if s.nextCalls != 1 {
		t.Errorf("next was called %d times, want once", s.nextCalls)
	}
}

func TestMiddlewareSendsGotoHeadersOfProxiesAfterTheHandler(t *testing.T) {
	port := newPort()
	useDefaultListener(t, port, false, false, localListener(t))
	tr := newRequest(t, port, nil, nil)
	tr.rs.ProxyRouter = true
	s := serve(tr, true)
	// A proxy router leaves the headers to what comes back from the upstream, and adds its own when that is done.
	if s.nextSaw.Get(constants.HeaderGotoProtocol) != "" {
		t.Error("the headers of a proxy router must not be sent before its handler")
	}
	if s.rr.Header().Get(constants.HeaderGotoProtocol) != "HTTP/1.1" {
		t.Errorf("headers = %v, want the Goto headers after the handler", s.rr.Header())
	}

	// Once the request has been proxied, the headers are those that the proxy gave.
	tr = newRequest(t, port, nil, nil)
	tr.rs.ProxyRouter = true
	tr.rs.ProxiedRequest = true
	if s := serve(tr, true); s.rr.Header().Get(constants.HeaderGotoProtocol) != "" {
		t.Errorf("headers = %v, want no Goto headers of ours on a proxied request", s.rr.Header())
	}

	// A proxy does not send them at all.
	tr = newRequest(t, port, nil, nil)
	tr.rs.IsProxy = true
	if s := serve(tr, true); s.nextSaw.Get(constants.HeaderGotoProtocol) != "" || s.rr.Header().Get(constants.HeaderGotoProtocol) != "" {
		t.Errorf("headers = %v, want no Goto headers on the request of a proxy", s.rr.Header())
	}
}

func TestMiddlewareDoesNotPassOnTunnelConnectRequests(t *testing.T) {
	port := newPort()
	useDefaultListener(t, port, false, false, localListener(t))
	tr := newRequest(t, port, nil, nil)
	tr.rs.IsTunnelConnectRequest = true
	s := serve(tr, true)
	if s.nextCalls != 0 {
		t.Error("a tunnel connect request is served by the middleware, and must not be passed on")
	}
	if !strings.Contains(logOf(tr), "Serving Tunnel Connect Request") {
		t.Errorf("log = %q, want it to tell that the tunnel connect request is served", logOf(tr))
	}
}

// testCert makes a certificate for the name, and the configs of a server and a client that trust it.
func testCert(t *testing.T, name string, version uint16) (server, client *tls.Config) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	// Session tickets are left out: they are written after the handshake, which a pipe does not take unread.
	server = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: version, MaxVersion: version, SessionTicketsDisabled: true}
	client = &tls.Config{RootCAs: pool, ServerName: name, MinVersion: version, MaxVersion: version}
	return server, client
}

// handshaken makes the server end of a TLS connection that has completed its handshake with a client that asked for the
// name, over a pipe.
func handshaken(t *testing.T, name string, version uint16) *tls.Conn {
	t.Helper()
	serverCfg, clientCfg := testCert(t, name, version)
	p1, p2 := net.Pipe()
	t.Cleanup(func() { p1.Close(); p2.Close() })
	deadline := time.Now().Add(10 * time.Second)
	_ = p1.SetDeadline(deadline)
	_ = p2.SetDeadline(deadline)
	srv, cli := tls.Server(p1, serverCfg), tls.Client(p2, clientCfg)
	errs := make(chan error, 1)
	go func() { errs <- cli.Handshake() }()
	if err := srv.Handshake(); err != nil {
		t.Fatalf("server handshake failed: %v", err)
	}
	if err := <-errs; err != nil {
		t.Fatalf("client handshake failed: %v", err)
	}
	return srv
}

func TestMiddlewareCapturesTLSInfoOfTLSListeners(t *testing.T) {
	for _, tt := range []struct {
		name    string
		version uint16
		text    string
	}{{"tls12", tls.VersionTLS12, "1.2"}, {"tls13", tls.VersionTLS13, "1.3"}} {
		for _, wrapped := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s wrapped=%v", tt.name, wrapped), func(t *testing.T) {
				port := newPort()
				useDefaultListener(t, port, true, true, localListener(t))
				var c net.Conn = handshaken(t, "sni.test", tt.version)
				if wrapped {
					// The connection of a TLS listener that forwards to an HTTP one.
					c = tcp.NewWrappedConn(c, port)
				}
				tr := newRequest(t, port, nil, c)
				s := serve(tr, true)
				rs := tr.rs
				if !rs.IsTLS || !rs.IsMTLS || rs.ServerName != "sni.test" || rs.TLSVersion != tt.text || rs.TLSVersionNum != tt.version {
					t.Errorf("request store = TLS %v, mTLS %v, server name %q, version %q (%d), want TLS and mTLS with sni.test and %s",
						rs.IsTLS, rs.IsMTLS, rs.ServerName, rs.TLSVersion, rs.TLSVersionNum, tt.text)
				}
				h := s.rr.Header()
				if h.Get(constants.HeaderGotoTLS) != "true" || h.Get(constants.HeaderGotoMTLS) != "true" || h.Get(constants.HeaderGotoSNI) != "sni.test" || h.Get(constants.HeaderGotoProtocol) != "HTTPS" {
					t.Errorf("headers = %v, want TLS, mTLS, the SNI and the HTTPS protocol", h)
				}
				if got := logOf(tr); !strings.Contains(got, ", ServerName: [sni.test], TLSVersion: ["+tt.text+"]") {
					t.Errorf("log = %q, want the server name and the TLS version of a TLS listener", got)
				}
			})
		}
	}
}

func TestMiddlewareLeavesTLSInfoOutWhenThereIsNoTLSToCapture(t *testing.T) {
	// A TLS connection of a listener that is not TLS.
	port := newPort()
	useDefaultListener(t, port, false, false, localListener(t))
	tr := newRequest(t, port, nil, handshaken(t, "sni.test", tls.VersionTLS12))
	serve(tr, true)
	if tr.rs.IsTLS || tr.rs.ServerName != "" || tr.rs.TLSVersion != "" {
		t.Errorf("request store = TLS %v, server name %q, version %q, want nothing captured for a listener that is not TLS", tr.rs.IsTLS, tr.rs.ServerName, tr.rs.TLSVersion)
	}

	// A TLS listener, and a connection whose handshake is not done.
	port = newPort()
	useDefaultListener(t, port, true, false, localListener(t))
	serverCfg, _ := testCert(t, "sni.test", tls.VersionTLS12)
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	tr = newRequest(t, port, nil, tls.Server(a, serverCfg))
	serve(tr, true)
	if tr.rs.IsTLS || tr.rs.ServerName != "" {
		t.Errorf("request store = TLS %v, server name %q, want nothing captured before the handshake", tr.rs.IsTLS, tr.rs.ServerName)
	}

	// A TLS listener, and a connection that is not TLS.
	tr = newRequest(t, port, nil, b)
	serve(tr, true)
	if tr.rs.IsTLS || tr.rs.IsMTLS || tr.rs.TLSVersion != "" {
		t.Errorf("request store = TLS %v, mTLS %v, version %q, want nothing captured for a connection that is not TLS", tr.rs.IsTLS, tr.rs.IsMTLS, tr.rs.TLSVersion)
	}
}
