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
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"goto/pkg/global"
	"goto/pkg/server/listeners"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

type closeWriter interface{ CloseWrite() error }

// connectHarness serves a loopback listener, a plain or a TLS one, the way the server serves a port in CONNECT mode: every
// accepted connection goes to the CONNECT proxy of the port. The port of the harness is the real port of the listener,
// so it's one that no other test uses.
type connectHarness struct {
	t     *testing.T
	ln    net.Listener
	port  int
	isTLS bool
	wg    connWG
}

func newConnectHarness(t *testing.T, isTLS bool) *connectHarness {
	t.Helper()
	setup()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h := &connectHarness{t: t, port: ln.Addr().(*net.TCPAddr).Port, isTLS: isTLS}
	if isTLS {
		// The handshake happens as the proxy reads the request of the client, which is what the server does in a step
		// of its own before it hands over the connection.
		ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{newCert(t)}})
	}
	h.ln = ln
	ClearPortProxy(h.port)
	EnableConnect(h.port, true)
	accepting := make(chan struct{})
	go func() {
		defer close(accepting)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			h.wg.Add(1)
			go func() {
				defer h.wg.Done()
				ProxyConnectConnection(h.port, c)
			}()
		}
	}()
	// Cleanups run in reverse order. This one is registered first so that it runs last: by then the clients and the
	// destination servers are gone, and the connections of the proxy are expected to be over.
	t.Cleanup(func() {
		ln.Close()
		<-accepting
		waitGroup(t, &h.wg, "tunneled connections to finish")
		proxyLock.Lock()
		delete(portProxy, h.port)
		proxyLock.Unlock()
	})
	return h
}

func (h *connectHarness) addr() string { return h.ln.Addr().String() }

func (h *connectHarness) dial() net.Conn {
	h.t.Helper()
	var c net.Conn
	var err error
	if h.isTLS {
		c, err = tls.DialWithDialer(&net.Dialer{Timeout: waitTimeout}, "tcp", h.addr(), &tls.Config{InsecureSkipVerify: true})
	} else {
		c, err = net.DialTimeout("tcp", h.addr(), waitTimeout)
	}
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { c.Close() })
	return c
}

func (h *connectHarness) proxy() *TCPProxy { return GetPortProxy(h.port) }

// counts gives the number of connections that the proxy counted, and the ones that it rejected.
func (h *connectHarness) counts() (conns int, rejects int) {
	tr := h.proxy().getTracker()
	tr.lock.RLock()
	defer tr.lock.RUnlock()
	for _, n := range tr.RejectCountsBySNI {
		rejects += n
	}
	return tr.ConnCount - rejects, rejects
}

// tunnelConn is the client side of a tunnel. What it reads comes through the reader that the answer to the CONNECT
// request was read with, which may hold what came with the answer.
type tunnelConn struct {
	net.Conn
	r io.Reader
}

func (c *tunnelConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// sendConnect writes the request as it is, and reads the answer to it.
func sendConnect(t *testing.T, c net.Conn, request string) (*tunnelConn, *http.Response) {
	t.Helper()
	c.SetWriteDeadline(time.Now().Add(waitTimeout))
	if _, err := io.WriteString(c, request); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(c)
	c.SetReadDeadline(time.Now().Add(waitTimeout))
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatalf("failed to read the answer to the CONNECT request: %v", err)
	}
	return &tunnelConn{Conn: c, r: br}, resp
}

func connectRequest(dest string) string {
	return fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", dest, dest)
}

// openTunnel asks for a tunnel to the destination, which is expected to be given. What comes after the request in the
// same write is the early data of the client.
func openTunnel(t *testing.T, c net.Conn, dest, early string) *tunnelConn {
	t.Helper()
	tc, resp := sendConnect(t, c, connectRequest(dest)+early)
	if resp.StatusCode != http.StatusOK || resp.Status != "200 Connection Established" {
		t.Fatalf("CONNECT answered %q, want 200 Connection Established", resp.Status)
	}
	return tc
}

// shortenConnectTimeouts makes the proxy give up on silent clients and tunnels quickly. The timeouts are read by the
// goroutines of the proxy, so they must only be changed while no connection is being tunneled. Call it before the
// harness is created, so that the timeouts are restored after its connections are over.
func shortenConnectTimeouts(t *testing.T, read, idle time.Duration) {
	t.Helper()
	oldRead, oldIdle := connectReadTimeout, connectIdleTimeout
	connectReadTimeout, connectIdleTimeout = read, idle
	t.Cleanup(func() { connectReadTimeout, connectIdleTimeout = oldRead, oldIdle })
}

func forPlainAndTLS(t *testing.T, test func(t *testing.T, isTLS bool)) {
	for _, isTLS := range []bool{false, true} {
		name := "plain"
		if isTLS {
			name = "tls"
		}
		t.Run(name, func(t *testing.T) { test(t, isTLS) })
	}
}

func TestConnectTunnelsBytesInBothDirections(t *testing.T) {
	forPlainAndTLS(t, func(t *testing.T, isTLS bool) {
		echo := startEchoServer(t)
		h := newConnectHarness(t, isTLS)
		c := h.dial()
		tc := openTunnel(t, c, echo.Addr(), "")
		if got := roundTrip(t, tc, "hello"); got != "hello" {
			t.Errorf("echo = %q", got)
		}
		// More than any buffer of the proxy holds, going both ways at the same time
		payload := bytes.Repeat([]byte("0123456789abcdef"), 16*1024)
		go tc.Write(payload)
		if got := readN(t, tc, len(payload)); !bytes.Equal(got, payload) {
			t.Error("the payload came back changed")
		}
		c.Close()

		session := onlySession(t, h.proxy(), echo.Addr())
		sent := len("hello") + len(payload)
		if session.Down.TotalBytesRead != sent || session.Down.TotalBytesWritten != sent ||
			len(session.Ups) != 1 || session.Ups[0].TotalBytesWritten != sent || session.Ups[0].TotalBytesRead != sent {
			t.Errorf("tracked down = %+v, ups = %+v, want [%d] bytes each way", session.Down, session.Ups, sent)
		}
		if session.Ups[0].RemoteAddress != echo.Addr() || session.Ups[0].ConnectFailed {
			t.Errorf("tracked up = %+v", session.Ups[0])
		}
		if conns, rejects := h.counts(); conns != 1 || rejects != 0 {
			t.Errorf("counted %d connections and %d rejects, want 1 and 0", conns, rejects)
		}
	})
}

// A client that goes through the proxy to a TLS destination does a handshake with it inside the tunnel, which is the way
// HTTPS gets through a proxy. For a TLS port that is TLS inside TLS.
func TestConnectTunnelsTLSToTLSDestination(t *testing.T) {
	forPlainAndTLS(t, func(t *testing.T, isTLS bool) {
		dest := startTLSServer(t, "S")
		h := newConnectHarness(t, isTLS)
		tc := openTunnel(t, h.dial(), dest.Addr(), "")
		inner := tls.Client(tc, &tls.Config{ServerName: "a.example.com", InsecureSkipVerify: true})
		inner.SetDeadline(time.Now().Add(waitTimeout))
		if err := inner.Handshake(); err != nil {
			t.Fatalf("the handshake with the destination failed: %v", err)
		}
		if got := readN(t, inner, 1); string(got) != "S" {
			t.Errorf("greeting = %q", got)
		}
		inner.SetDeadline(time.Time{})
		if got := roundTrip(t, inner, "ping"); got != "ping" {
			t.Errorf("echo = %q", got)
		}
	})
}

func TestConnectForwardsDataThatCameWithTheRequest(t *testing.T) {
	forPlainAndTLS(t, func(t *testing.T, isTLS bool) {
		rec := &recorder{}
		dest := startServer(t, recordingHandler(rec, ">"))
		h := newConnectHarness(t, isTLS)
		tc := openTunnel(t, h.dial(), dest.Addr(), "early")
		if got := readN(t, tc, len(">early")); string(got) != ">early" {
			t.Errorf("answer = %q", got)
		}
		if got := rec.String(); got != "early" {
			t.Errorf("the destination got %q, want what was sent with the request", got)
		}
	})
}

// The end of what a client sends is passed on to the destination, which gets to answer after it.
func TestConnectPassesOnHalfClose(t *testing.T) {
	forPlainAndTLS(t, func(t *testing.T, isTLS bool) {
		dest := startServer(t, func(c net.Conn) {
			data, _ := io.ReadAll(c)
			c.Write([]byte(fmt.Sprintf("got %d", len(data))))
		})
		h := newConnectHarness(t, isTLS)
		c := h.dial()
		tc := openTunnel(t, c, dest.Addr(), "")
		if _, err := tc.Write([]byte("12345")); err != nil {
			t.Fatal(err)
		}
		if err := c.(closeWriter).CloseWrite(); err != nil {
			t.Fatal(err)
		}
		c.SetReadDeadline(time.Now().Add(waitTimeout))
		answer, err := io.ReadAll(tc)
		if err != nil || string(answer) != "got 5" {
			t.Errorf("answer = %q, err %v, want what the destination says after the end of the client", answer, err)
		}
	})
}

func TestConnectEndsWhenTheDestinationCloses(t *testing.T) {
	dest := startServer(t, func(c net.Conn) { c.Write([]byte("bye")) })
	h := newConnectHarness(t, false)
	tc := openTunnel(t, h.dial(), dest.Addr(), "")
	if got := readN(t, tc, 3); string(got) != "bye" {
		t.Errorf("answer = %q", got)
	}
	// The client is told that the destination is done, and the tunnel is over once the client is done too
	expectEOF(t, tc)
	tc.Close()
	session := onlySession(t, h.proxy(), dest.Addr())
	if !session.Ups[0].RemoteClosed {
		t.Errorf("tracked up = %+v, want the close of the destination to be noted", session.Ups[0])
	}
}

func TestConnectRejectsBadRequests(t *testing.T) {
	cases := []struct {
		name    string
		request string
		status  int
		allow   string
	}{
		{"not a CONNECT", "GET / HTTP/1.1\r\nHost: example.com\r\n\r\n", http.StatusMethodNotAllowed, "CONNECT"},
		{"no port", connectRequest("example.com"), http.StatusBadRequest, ""},
		{"no host", connectRequest(":443"), http.StatusBadRequest, ""},
		{"port out of range", connectRequest("example.com:70000"), http.StatusBadRequest, ""},
		{"port zero", connectRequest("example.com:0"), http.StatusBadRequest, ""},
		{"port not a number", connectRequest("example.com:https"), http.StatusBadRequest, ""},
		{"a path", "CONNECT example.com:443/x HTTP/1.1\r\nHost: example.com\r\n\r\n", http.StatusBadRequest, ""},
		{"not HTTP", "hello there\r\n\r\n", http.StatusBadRequest, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newConnectHarness(t, false)
			c := h.dial()
			conn, resp := sendConnect(t, c, tc.request)
			// The reader of the answer takes the Connection header off the headers, and tells it with Close
			if resp.StatusCode != tc.status || resp.Header.Get("Allow") != tc.allow || !resp.Close {
				t.Errorf("answered %q Allow [%s] Close [%v], want %d Allow [%s] and the connection to be closed",
					resp.Status, resp.Header.Get("Allow"), resp.Close, tc.status, tc.allow)
			}
			expectEOF(t, conn)
			waitFor(t, "the rejection to be counted", func() bool { _, rejects := h.counts(); return rejects == 1 })
			if conns, _ := h.counts(); conns != 0 {
				t.Errorf("counted %d connections, want only the rejection", conns)
			}
		})
	}
}

func TestConnectAnswersBadGatewayWhenDestinationIsUnreachable(t *testing.T) {
	dest := closedAddress(t)
	h := newConnectHarness(t, false)
	c := h.dial()
	_, resp := sendConnect(t, c, connectRequest(dest))
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("answered %q, want 502", resp.Status)
	}
	session := onlySession(t, h.proxy(), dest)
	if len(session.Ups) != 1 || !session.Ups[0].ConnectFailed || !session.Down.Closed {
		t.Errorf("tracked down = %+v, ups = %+v, want a failed connect", session.Down, session.Ups)
	}
}

func TestConnectGivesUpOnClientThatSendsNoRequest(t *testing.T) {
	shortenConnectTimeouts(t, 100*time.Millisecond, time.Minute)
	h := newConnectHarness(t, false)
	c := h.dial()
	// The proxy closes the connection without a word to a client that said nothing
	expectEOF(t, c)
	waitFor(t, "the connection to be counted", func() bool { _, rejects := h.counts(); return rejects == 1 })
}

func TestConnectGivesUpOnSilentTunnel(t *testing.T) {
	shortenConnectTimeouts(t, time.Minute, 150*time.Millisecond)
	echo := startEchoServer(t)
	h := newConnectHarness(t, false)
	tc := openTunnel(t, h.dial(), echo.Addr(), "")
	if got := roundTrip(t, tc, "x"); got != "x" {
		t.Fatalf("echo = %q", got)
	}
	expectEOF(t, tc)
	session := onlySession(t, h.proxy(), echo.Addr())
	if !session.Down.Closed || !session.Ups[0].Closed {
		t.Errorf("tracked down = %+v, ups = %+v", session.Down, session.Ups)
	}
}

// A direction that is silent doesn't end the tunnel while the other one is at it.
func TestConnectKeepsTunnelThatIsActiveInOneDirection(t *testing.T) {
	shortenConnectTimeouts(t, time.Minute, 200*time.Millisecond)
	dest := startServer(t, func(c net.Conn) {
		for i := 0; i < 12; i++ {
			if _, err := c.Write([]byte("x")); err != nil {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	})
	h := newConnectHarness(t, false)
	tc := openTunnel(t, h.dial(), dest.Addr(), "")
	if got := readN(t, tc, 12); string(got) != strings.Repeat("x", 12) {
		t.Errorf("got %q, want all that the destination sent over longer than the idle timeout", got)
	}
}

func TestConnectDestination(t *testing.T) {
	cases := []struct {
		target string
		want   string
		fail   bool
	}{
		{target: "example.com:443", want: "example.com:443"},
		{target: "10.1.2.3:8080", want: "10.1.2.3:8080"},
		{target: "[::1]:8443", want: "[::1]:8443"},
		{target: "example.com", fail: true},
		{target: ":443", fail: true},
		{target: "example.com:0", fail: true},
		{target: "example.com:65536", fail: true},
		{target: "example.com:abc", fail: true},
		{target: "example.com:443/path", fail: true},
		{target: "example.com:443?q=1", fail: true},
		{target: "user@example.com:443", fail: true},
	}
	for _, tc := range cases {
		t.Run(tc.target, func(t *testing.T) {
			req, err := http.ReadRequest(bufio.NewReader(strings.NewReader(connectRequest(tc.target))))
			var got string
			if err == nil {
				got, err = connectDestination(req)
			}
			if tc.fail {
				if err == nil {
					t.Errorf("destination = %q, want an error", got)
				}
			} else if err != nil || got != tc.want {
				t.Errorf("destination = %q, err %v, want %q", got, err, tc.want)
			}
		})
	}
}

func TestConnectModeOfAPort(t *testing.T) {
	port := apiPort(t)
	exists := func() bool {
		proxyLock.RLock()
		defer proxyLock.RUnlock()
		return portProxy[port] != nil
	}
	if WillProxyConnect(port) || exists() {
		t.Fatal("a port that was never configured must not be a CONNECT proxy port, and must not get a proxy by being asked about")
	}
	EnableConnect(port, false)
	if exists() {
		t.Error("turning the mode off for a port that has no proxy must not create one")
	}
	EnableConnect(port, true)
	if !WillProxyConnect(port) {
		t.Fatal("the mode must be on")
	}
	if got := string(mustJSON(t, GetPortProxy(port))); !strings.Contains(got, `"http":{"connect":true}`) {
		t.Errorf("proxy = %s, want it to report the mode", got)
	}
	EnableConnect(port, false)
	if WillProxyConnect(port) {
		t.Error("the mode must be off")
	}
	EnableConnect(port, true)
	ClearPortProxy(port)
	if WillProxyConnect(port) {
		t.Error("clearing the proxy of the port must clear its mode")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// ---------------------------------------------------------------------------------------------------------------
// The API, with the listeners of the server behind it. The listeners package opens real ports on all the interfaces of
// the host.

const connectAPI = "/proxy/tcp/http/connect/"

func TestConnectAPIDisables(t *testing.T) {
	adminPort, port := apiPort(t), apiPort(t)
	for _, method := range []string{"POST", "PUT"} {
		EnableConnect(port, true)
		rr := callWith(t, adminPort, port, method, connectAPI+"disable", "")
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), fmt.Sprintf("Port [%d]: CONNECT proxy mode disabled", port)) || WillProxyConnect(port) {
			t.Errorf("%s: status = %d, body %q, mode = %v", method, rr.Code, rr.Body.String(), WillProxyConnect(port))
		}
	}
}

func TestConnectAPIRefusesPortsThatServeHTTP(t *testing.T) {
	adminPort := apiPort(t)
	// The port that this very request comes over, and the main port of the server, can't stop serving HTTP
	for name, port := range map[string]int{"the port of the request": adminPort, "the port of the server": global.Self.ServerPort} {
		rr := callWith(t, adminPort, port, "POST", connectAPI+"enable", "")
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "can't be a CONNECT proxy port") {
			t.Errorf("%s: status = %d, body %q", name, rr.Code, rr.Body.String())
		}
		if WillProxyConnect(port) {
			t.Errorf("%s: the mode must not be on", name)
		}
	}
	if l := listeners.GetListenerForPort(adminPort); l != nil {
		t.Error("the port of the request must not get a listener of another kind")
	}
}

func TestConnectAPIFailsWhenPortCantBeOpened(t *testing.T) {
	regServeListeners()
	adminPort := apiPort(t)
	t.Cleanup(func() {
		// the listeners package keeps the listener that it failed to open
		if l := listeners.GetListenerForPort(70000); l != nil {
			listeners.RemoveListener(l)
		}
	})
	rr := callWith(t, adminPort, 70000, "POST", connectAPI+"enable", "")
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Port [70000] failed to enable CONNECT proxy mode") {
		t.Errorf("status = %d, body %q", rr.Code, rr.Body.String())
	}
	if WillProxyConnect(70000) {
		t.Error("the mode must not be on for a port that couldn't be opened")
	}
}

// Enabling the mode makes the port a TCP port, which the proxy serves without the HTTP server.
func TestConnectAPIMakesPortATCPPortThatTunnels(t *testing.T) {
	regServeListeners()
	echo := startEchoServer(t)
	adminPort := apiPort(t)
	port := freePort(t)
	done := make(chan struct{})
	regAccepted.Store(port, done)
	t.Cleanup(func() {
		proxyLock.Lock()
		delete(portProxy, port)
		proxyLock.Unlock()
	})
	rr := callWith(t, adminPort, port, "POST", connectAPI+"enable", "")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), fmt.Sprintf("Port [%d]: CONNECT proxy mode enabled", port)) {
		t.Fatalf("status = %d, body %q", rr.Code, rr.Body.String())
	}
	l := listeners.GetListenerForPort(port)
	if l == nil || !l.IsTCP || l.IsHTTP {
		t.Fatalf("listener = %+v, want a TCP one", l)
	}
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
	if !WillProxyConnect(port) || WillProxyTCP(port) {
		t.Fatal("the port must be a CONNECT proxy port, and not a TCP proxy one")
	}

	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), waitTimeout)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	tc := openTunnel(t, c, echo.Addr(), "")
	if got := roundTrip(t, tc, "through the listener"); got != "through the listener" {
		t.Errorf("echo = %q", got)
	}
	c.Close()
	t.Cleanup(func() { waitGroup(t, &regConns, "tunneled connections") })

	// The mode is reported with the proxy of the port
	proxy := decode(t, callWith(t, adminPort, port, "GET", "/proxy/tcp", ""))["tcp"].(map[string]any)
	if h, _ := proxy["http"].(map[string]any); h["connect"] != true {
		t.Errorf("proxy = %v, want the mode to be reported", proxy)
	}

	// Disabling the mode leaves the port a TCP port, that doesn't tunnel anymore
	rr = callWith(t, adminPort, port, "POST", connectAPI+"disable", "")
	if rr.Code != http.StatusOK || WillProxyConnect(port) {
		t.Errorf("disable: status = %d, body %q, mode = %v", rr.Code, rr.Body.String(), WillProxyConnect(port))
	}
	if l2 := listeners.GetListenerForPort(port); l2 != l || !l2.IsTCP {
		t.Error("the listener of the port must stay")
	}
}

// A port that is a TLS listener already stays one, which is how a CONNECT proxy port gets to talk TLS to its clients.
func TestConnectAPIKeepsTLSListenerOfThePort(t *testing.T) {
	regServeListeners()
	adminPort := apiPort(t)
	port := freePort(t)
	done := make(chan struct{})
	regAccepted.Store(port, done)
	t.Cleanup(func() {
		proxyLock.Lock()
		delete(portProxy, port)
		proxyLock.Unlock()
	})
	tlsListener := &listeners.Listener{Port: port, Protocol: "tls", Open: true}
	if err, msg := listeners.AddOrUpdateListener(tlsListener); err != nil {
		t.Fatalf("failed to add the TLS listener: %s", msg)
	}
	t.Cleanup(func() {
		tlsListener.Close()
		listeners.RemoveListener(tlsListener)
		select {
		case <-done:
		case <-time.After(waitTimeout):
			t.Error("the listener did not stop serving")
		}
	})
	rr := callWith(t, adminPort, port, "POST", connectAPI+"enable", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rr.Code, rr.Body.String())
	}
	if l := listeners.GetListenerForPort(port); l != tlsListener || !l.IsTCP || !l.TLS {
		t.Errorf("listener = %+v, want the TLS one that the port had", l)
	}
	if !WillProxyConnect(port) {
		t.Error("the mode must be on")
	}
}
