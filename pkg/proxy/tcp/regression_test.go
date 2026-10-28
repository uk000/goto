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

// Regression tests for bugs of the TCP proxy. They are written against the interface that the proxy had before they
// were fixed, and use nothing else (not even the helpers of the other test files), so that they can be run against the
// code as it was: they fail there, each for its own reason.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"goto/pkg/global"
	"goto/pkg/server/listeners"
	"goto/pkg/server/middleware"
	"goto/pkg/types"
	"goto/pkg/util"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const regWait = 5 * time.Second

var (
	regOnce      sync.Once
	regRoutes    sync.Once
	regServeOnce sync.Once
	regPortSeq   int
	regPortMu    sync.Mutex

	// The ports that a listener started serving, the channels that get closed when serving a port stops, and the
	// connections that are being proxied for listeners
	regServed   sync.Map
	regAccepted sync.Map
	regConns    connWG
)

func regSetup() {
	regOnce.Do(func() {
		log.SetOutput(io.Discard)
		global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
		global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
		global.Funcs.IsListenerPresent = func(int) bool { return false }
		global.Funcs.IsListenerOpen = func(int) bool { return false }
		global.Funcs.GetListenerID = func(int) string { return "" }
		global.Funcs.GetListenerLabel = func(*http.Request) string { return "" }
		global.Funcs.GetListenerLabelForPort = func(int) string { return "" }
		global.Funcs.CloseConnectionsForPort = func(int) {}
		util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
	})
}

// regServeListeners makes the listeners package serve the connections of the TCP listeners that it opens the way the
// server does, i.e. hand them to the proxy of the port. The serve function of the listeners package can be set only
// once for good, so this is the one that all the tests use.
func regServeListeners() {
	regSetup()
	regServeOnce.Do(func() { listeners.ConfigureTCPServer(regServeListener) })
}

func regServeListener(listenerID string, port int, ln net.Listener) error {
	regServed.Store(port, true)
	defer func() {
		if done, ok := regAccepted.Load(port); ok {
			close(done.(chan struct{}))
		}
	}()
	for {
		c, err := ln.Accept()
		if err != nil {
			return nil
		}
		if WillProxyConnect(port) {
			regConns.Add(1)
			go func() {
				defer regConns.Done()
				ProxyConnectConnection(port, c)
			}()
		} else if WillProxyTCP(port) {
			regConns.Add(1)
			go func() {
				defer regConns.Done()
				ProxyTCPConnection(port, c)
			}()
		} else {
			c.Close()
		}
	}
}

func regNextPort() int {
	regPortMu.Lock()
	defer regPortMu.Unlock()
	regPortSeq++
	return 71000 + regPortSeq
}

func regWaitGroup(t *testing.T, wg interface{ Wait() }, what string) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(regWait):
		t.Errorf("timed out waiting for %s", what)
	}
}

// regServer is a loopback server running a handler per connection.
type regServer struct {
	ln    net.Listener
	wg    connWG
	mu    sync.Mutex
	conns []net.Conn
}

func regStartServer(t *testing.T, handler func(net.Conn)) *regServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &regServer{ln: ln}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.conns = append(s.conns, c)
			s.mu.Unlock()
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				defer c.Close()
				handler(c)
			}()
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		s.mu.Lock()
		for _, c := range s.conns {
			c.Close()
		}
		s.mu.Unlock()
		regWaitGroup(t, &s.wg, "server to stop")
	})
	return s
}

func (s *regServer) addr() string { return s.ln.Addr().String() }

func regEchoServer(t *testing.T) *regServer {
	return regStartServer(t, func(c net.Conn) { io.Copy(c, c) })
}

func regClosedAddress(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// regHarness hands the connections that a loopback listener accepts to the proxy of the port, the way the server does.
// Panics of the proxy are caught, and reported as failures of the test.
type regHarness struct {
	t      *testing.T
	ln     net.Listener
	port   int
	proxy  *TCPProxy
	wg     connWG
	mu     sync.Mutex
	panics []any
}

func regNewHarness(t *testing.T) *regHarness {
	t.Helper()
	regSetup()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h := &regHarness{t: t, ln: ln, port: ln.Addr().(*net.TCPAddr).Port}
	ClearPortProxy(h.port)
	h.proxy = GetPortProxy(h.port)
	accepting := make(chan struct{})
	go func() {
		defer close(accepting)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			h.wg.Add(1)
			go h.proxyConn(c)
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		<-accepting
		regWaitGroup(t, &h.wg, "proxied connections to finish")
		proxyLock.Lock()
		delete(portProxy, h.port)
		proxyLock.Unlock()
		h.mu.Lock()
		defer h.mu.Unlock()
		if len(h.panics) > 0 {
			t.Errorf("the proxy panicked: %v", h.panics)
		}
	})
	return h
}

func (h *regHarness) proxyConn(c net.Conn) {
	defer h.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			h.mu.Lock()
			h.panics = append(h.panics, r)
			h.mu.Unlock()
			c.Close()
		}
	}()
	ProxyTCPConnection(h.port, c)
}

func (h *regHarness) dial() *net.TCPConn {
	h.t.Helper()
	c, err := net.DialTimeout("tcp", h.ln.Addr().String(), regWait)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { c.Close() })
	return c.(*net.TCPConn)
}

func (h *regHarness) route(retries int, configure func(*TCPUpstream), addresses ...string) *TCPUpstream {
	up := &TCPUpstream{Name: "up", Retries: retries, Endpoints: map[string]*TCPEndpoint{}}
	for i, address := range addresses {
		name := fmt.Sprintf("ep%d", i+1)
		up.Endpoints[name] = &TCPEndpoint{Name: name, Address: address}
	}
	if configure != nil {
		configure(up)
	}
	h.proxy.AddUpstreams(map[string]*TCPUpstream{"up": up})
	return up
}

func regReadN(t *testing.T, c net.Conn, n int) []byte {
	t.Helper()
	buf := make([]byte, n)
	c.SetReadDeadline(time.Now().Add(regWait))
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatalf("read of %d bytes failed: %v (got %q)", n, err, buf)
	}
	return buf
}

// regTracker is the report of the tracker of a proxy, the way the admin API gives it.
type regConn struct {
	TotalBytesRead    int       `json:"totalBytesRead"`
	TotalBytesWritten int       `json:"totalBytesWritten"`
	DelayCount        int       `json:"delayCount"`
	Closed            bool      `json:"closed"`
	EndTime           time.Time `json:"endTime"`
}

type regSession struct {
	Down regConn   `json:"downConn"`
	Ups  []regConn `json:"upConn"`
}

type regTarget struct {
	ConnCount int                   `json:"connCount"`
	Sessions  map[string]regSession `json:"tcpSessions"`
}

type regTracker struct {
	ConnCount int                  `json:"connCount"`
	Rejects   map[string]int       `json:"rejectCountsBySNI"`
	Targets   map[string]regTarget `json:"targetTrackers"`
}

func regReport(t *testing.T, p *TCPProxy) regTracker {
	t.Helper()
	b, err := json.Marshal(p.Tracker)
	if err != nil {
		t.Fatal(err)
	}
	var r regTracker
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

// regOnlySession waits for the proxied connection of the upstream to be over and reports it.
func regOnlySession(t *testing.T, p *TCPProxy, upstream string) regSession {
	t.Helper()
	deadline := time.Now().Add(regWait)
	for {
		r := regReport(t, p)
		if sessions := r.Targets[upstream].Sessions; len(sessions) == 1 {
			for _, s := range sessions {
				if s.Down.Closed {
					return s
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for the session of %s to be over: %+v", upstream, r)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func regRouter() http.Handler {
	regRoutes.Do(func() {
		regSetup()
		Middleware.SetRoutes(nil)
	})
	return middleware.RootRouters["/proxy"]
}

// regCall serves an admin request, and reports a panic of the handler instead of crashing.
func regCall(method, target, body string) (rr *httptest.ResponseRecorder, panicked any) {
	h := regRouter()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, 59000))
	_, req, _ = util.WithRequestStore(req, nil)
	rr = httptest.NewRecorder()
	func() {
		defer func() { panicked = recover() }()
		h.ServeHTTP(rr, req)
	}()
	return
}

func regNoPanic(t *testing.T, what string, f func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s panicked: %v", what, r)
		}
	}()
	f()
}

// ---------------------------------------------------------------------------------------------------------------

// The upstream that the API sets up has no retries, and nothing that a client sent was ever written to it.
func TestRegression_DefaultRetriesStillForwardsData(t *testing.T) {
	echo := regEchoServer(t)
	h := regNewHarness(t)
	h.proxy.addNewUpstream("default", echo.addr())
	c := h.dial()
	c.SetWriteDeadline(time.Now().Add(regWait))
	if _, err := c.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if got := regReadN(t, c, 5); string(got) != "hello" {
		t.Errorf("echo = %q", got)
	}
}

func TestRegression_DeadUpstreamClosesClientWithoutCrashing(t *testing.T) {
	h := regNewHarness(t)
	h.route(1, nil, regClosedAddress(t))
	c := h.dial()
	c.Write([]byte("hello"))
	c.SetReadDeadline(time.Now().Add(regWait))
	if n, err := c.Read(make([]byte, 10)); err != io.EOF && (err == nil || !strings.Contains(err.Error(), "reset")) {
		t.Errorf("read %d bytes, err %v; want the connection to be closed", n, err)
	}
}

func TestRegression_LargeTransferIsNotCorrupted(t *testing.T) {
	echo := regEchoServer(t)
	h := regNewHarness(t)
	h.route(1, nil, echo.addr())
	c := h.dial()
	payload := make([]byte, 1<<20)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	go func() {
		c.SetWriteDeadline(time.Now().Add(3 * regWait))
		c.Write(payload)
	}()
	got := make([]byte, len(payload))
	c.SetReadDeadline(time.Now().Add(3 * regWait))
	n, err := io.ReadFull(c, got)
	if err != nil {
		t.Fatalf("read %d of %d bytes: %v", n, len(payload), err)
	}
	if sha256.Sum256(got) != sha256.Sum256(payload) {
		t.Fatal("the bytes that came back are not the ones that were sent")
	}
}

func TestRegression_HalfClosedClientGetsTheAnswer(t *testing.T) {
	echo := regEchoServer(t)
	h := regNewHarness(t)
	h.route(1, nil, echo.addr())
	c := h.dial()
	c.Write([]byte("request"))
	c.CloseWrite()
	c.SetReadDeadline(time.Now().Add(regWait))
	got, err := io.ReadAll(c)
	if err != nil || string(got) != "request" {
		t.Errorf("answer = %q, %v; want the echo of the request", got, err)
	}
}

// The first connection to an opaque proxy wasn't counted for its target.
func TestRegression_OpaqueProxyCountsFirstConnectionPerTarget(t *testing.T) {
	echo := regEchoServer(t)
	h := regNewHarness(t)
	h.route(1, nil, echo.addr())
	c := h.dial()
	c.Write([]byte("x"))
	regReadN(t, c, 1)
	c.Close()
	regOnlySession(t, h.proxy, "up")
	r := regReport(t, h.proxy)
	if r.ConnCount != 1 || r.Targets["up"].ConnCount != 1 {
		t.Errorf("conn count of the proxy %d, of the target %d; want 1 each", r.ConnCount, r.Targets["up"].ConnCount)
	}
}

// The end time of connections was never set.
func TestRegression_EndTimeIsRecorded(t *testing.T) {
	echo := regEchoServer(t)
	h := regNewHarness(t)
	h.route(1, nil, echo.addr())
	c := h.dial()
	c.Write([]byte("x"))
	regReadN(t, c, 1)
	c.Close()
	s := regOnlySession(t, h.proxy, "up")
	if s.Down.EndTime.IsZero() {
		t.Error("downstream end time is not set")
	}
	if len(s.Ups) != 1 || s.Ups[0].EndTime.IsZero() {
		t.Errorf("upstream trackers = %+v, want an end time", s.Ups)
	}
}

// Delays were only counted when the debug logs of the proxy were on.
func TestRegression_DelayIsCountedWithoutDebugLogs(t *testing.T) {
	regSetup()
	debug := global.Flags.EnableProxyDebugLogs
	global.Flags.EnableProxyDebugLogs = false
	t.Cleanup(func() { global.Flags.EnableProxyDebugLogs = debug })
	echo := regEchoServer(t)
	h := regNewHarness(t)
	h.route(1, func(up *TCPUpstream) {
		up.Delay = types.NewDelay(30*time.Millisecond, 30*time.Millisecond, -1)
	}, echo.addr())
	c := h.dial()
	c.Write([]byte("x"))
	regReadN(t, c, 1)
	c.Close()
	s := regOnlySession(t, h.proxy, "up")
	delays := s.Down.DelayCount
	for _, u := range s.Ups {
		delays += u.DelayCount
	}
	if delays == 0 {
		t.Errorf("the delayed writes were not counted: %+v", s)
	}
}

// Choosing a delay between limits that are less than a millisecond apart panicked, in the goroutine of a connection.
func TestRegression_SubMillisecondDelayRangeDoesNotPanic(t *testing.T) {
	regSetup()
	echo := regEchoServer(t)
	h := regNewHarness(t)
	h.route(1, func(up *TCPUpstream) {
		up.Delay = types.NewDelay(100*time.Microsecond, 600*time.Microsecond, -1)
	}, echo.addr())
	c := h.dial()
	c.Write([]byte("x"))
	if got := regReadN(t, c, 1); string(got) != "x" {
		t.Errorf("echo = %q", got)
	}
}

// The delays of connections that are proxied at the same time were chosen with a random source that can't be shared by
// goroutines, and from a delay setting that the connections changed without agreeing on who goes first.
func TestRegression_DelaysOfConcurrentConnectionsAreSafe(t *testing.T) {
	regSetup()
	echo := regEchoServer(t)
	h := regNewHarness(t)
	h.route(1, func(up *TCPUpstream) {
		up.Delay = types.NewDelay(time.Millisecond, 3*time.Millisecond, -1)
	}, echo.addr())
	const conns, rounds = 6, 5
	errs := make(chan error, conns)
	var wg sync.WaitGroup
	for i := 0; i < conns; i++ {
		c := h.dial()
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, 1)
			for r := 0; r < rounds; r++ {
				c.SetDeadline(time.Now().Add(regWait))
				if _, err := c.Write([]byte("x")); err != nil {
					errs <- err
					return
				}
				if _, err := io.ReadFull(c, buf); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	regWaitGroup(t, &wg, "clients")
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

type regFixedAddrConn struct {
	net.Conn
	addr net.Addr
}

func (c *regFixedAddrConn) RemoteAddr() net.Addr { return c.addr }

// A client that connects again from the address it used before got the session of the earlier connection, with the
// connections of that, and the proxy broke on it.
func TestRegression_ReconnectingClientAddressDoesNotBreakSession(t *testing.T) {
	echo := regEchoServer(t)
	h := regNewHarness(t)
	h.route(1, nil, echo.addr())
	for i := 1; i <= 2; i++ {
		down, client := net.Pipe()
		conn := &regFixedAddrConn{Conn: down, addr: &net.TCPAddr{IP: net.IPv4(10, 1, 1, 1), Port: 4444}}
		h.wg.Add(1)
		go h.proxyConn(conn)
		client.SetDeadline(time.Now().Add(regWait))
		if _, err := client.Write([]byte("hi")); err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
		if got := regReadN(t, client, 2); string(got) != "hi" {
			t.Fatalf("round %d: echo = %q", i, got)
		}
		client.Close()
		deadline := time.Now().Add(regWait)
		for {
			r := regReport(t, h.proxy)
			var closed bool
			for _, s := range r.Targets["up"].Sessions {
				closed = s.Down.Closed
			}
			if closed || time.Now().After(deadline) {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	r := regReport(t, h.proxy)
	for addr, s := range r.Targets["up"].Sessions {
		if len(s.Ups) != 1 {
			t.Errorf("session %s has %d upstream connections, want the one of the last connection", addr, len(s.Ups))
		}
	}
}

func TestRegression_GetPortProxyIsAtomic(t *testing.T) {
	regSetup()
	const ports, callers = 100, 32
	base := regNextPort() * 1000
	t.Cleanup(func() {
		proxyLock.Lock()
		for i := 0; i < ports; i++ {
			delete(portProxy, base+i)
		}
		proxyLock.Unlock()
	})
	got := make([][]*TCPProxy, ports)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < ports; i++ {
		got[i] = make([]*TCPProxy, callers)
		for j := 0; j < callers; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				got[i][j] = GetPortProxy(base + i)
			}()
		}
	}
	close(start)
	regWaitGroup(t, &wg, "callers")
	different := 0
	for i := 0; i < ports; i++ {
		registered := GetPortProxy(base + i)
		for j := 0; j < callers; j++ {
			if got[i][j] != registered {
				different++
				break
			}
		}
	}
	if different > 0 {
		t.Errorf("on %d of %d ports, callers got a proxy other than the registered one", different, ports)
	}
}

// A pattern that isn't a regular expression made the add API panic, instead of failing the request.
func TestRegression_InvalidSNIMatchIsRejected(t *testing.T) {
	regSetup()
	body := `{"a": {"endpoints": {"e": {"address": "localhost:1"}}, "match": {"sni": "*.example.com"}}}`
	rr, panicked := regCall("POST", "/proxy/tcp/upstreams/add", body)
	if panicked != nil {
		t.Fatalf("the handler panicked: %v", panicked)
	}
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, body %q; want 400", rr.Code, rr.Body.String())
	}
	ups := map[string]*TCPUpstream{"a": {Endpoints: map[string]*TCPEndpoint{"e": {Address: "localhost:1"}}, Match: &UpstreamMatch{SNI: "[x"}}}
	if err := ValidateUpstreams(ups); err == nil {
		t.Error("an SNI match that can't be compiled has to fail validation")
	}
	regNoPanic(t, "AddUpstreams", func() { newTCPProxy(1).AddUpstreams(ups) })
}

func TestRegression_NilConfigEntriesDoNotPanic(t *testing.T) {
	regNoPanic(t, "validation of a nil upstream", func() {
		if err := ValidateUpstreams(map[string]*TCPUpstream{"a": nil}); err == nil {
			t.Error("a nil upstream has to fail validation")
		}
	})
	regNoPanic(t, "validation of a nil endpoint", func() {
		if err := ValidateUpstreams(map[string]*TCPUpstream{"a": {Endpoints: map[string]*TCPEndpoint{"e": nil}}}); err == nil {
			t.Error("a nil endpoint has to fail validation")
		}
	})
	regNoPanic(t, "AddUpstreams of a nil upstream", func() {
		p := newTCPProxy(1)
		p.AddUpstreams(map[string]*TCPUpstream{"a": nil})
		p.hasSNITargets()
		p.getMatchingTCPUpstream("")
	})
	regNoPanic(t, "addNewUpstream after clearing the upstreams", func() {
		p := newTCPProxy(1)
		p.AddUpstreams(nil)
		p.addNewUpstream("default", "localhost:1")
	})
}

func TestRegression_UpstreamWithoutEndpointsIsRejected(t *testing.T) {
	if err := ValidateUpstreams(map[string]*TCPUpstream{"a": {Endpoints: map[string]*TCPEndpoint{}}}); err == nil {
		t.Error("an upstream without any endpoint has nothing to proxy to, and has to fail validation")
	}
}

func TestRegression_ValidationRejectsUnusableValues(t *testing.T) {
	ep := func(address string) map[string]*TCPEndpoint { return map[string]*TCPEndpoint{"e": {Address: address}} }
	for name, up := range map[string]*TCPUpstream{
		"address without a port": {Endpoints: ep("localhost")},
		"address with bad port":  {Endpoints: ep("localhost:99999")},
		"negative retries":       {Endpoints: ep("h:1"), Retries: -1},
		"drop percentage > 100":  {Endpoints: ep("h:1"), DropPct: 101},
	} {
		regNoPanic(t, name, func() {
			if err := ValidateUpstreams(map[string]*TCPUpstream{"a": up}); err == nil {
				t.Errorf("%s: validation passed", name)
			}
		})
	}
}

func TestRegression_PartialDelayInPayloadIsAnErrorNotAPanic(t *testing.T) {
	regNoPanic(t, "parsing an upstream with a delay that has no max", func() {
		ups, err := parseUpstreams(strings.NewReader(`{"a": {"endpoints": {"e": {"address": "h:1"}}, "delay": {"min": "1s"}}}`))
		if err == nil {
			t.Errorf("upstreams = %+v, want an error", ups)
		}
	})
}

// The upstream of a client was picked in the order that a map gets walked.
func TestRegression_SNIChoiceIsDeterministic(t *testing.T) {
	p := newTCPProxy(1)
	ups := map[string]*TCPUpstream{}
	for _, name := range []string{"e", "c", "a", "d", "b"} {
		ups[name] = &TCPUpstream{Name: name, Match: &UpstreamMatch{SNI: "x.example.com"}, Endpoints: map[string]*TCPEndpoint{"e": {Address: "h:1"}}}
	}
	p.AddUpstreams(ups)
	for i := 0; i < 100; i++ {
		if up := p.getMatchingTCPUpstream("x.example.com"); up == nil || up.Name != "a" {
			t.Fatalf("round %d: upstream = %+v, want a, the first of the matching ones by name", i, up)
		}
	}
}

// A client without an SNI went to any of the upstreams, even though all of them want a specific one.
func TestRegression_ClientWithoutSNIIsNotRoutedToSNIUpstream(t *testing.T) {
	p := newTCPProxy(1)
	p.AddUpstreams(map[string]*TCPUpstream{"a": {Name: "a", Match: &UpstreamMatch{SNI: "a.example.com"}, Endpoints: map[string]*TCPEndpoint{"e": {Address: "h:1"}}}})
	if up := p.getMatchingTCPUpstream(""); up != nil {
		t.Errorf("a client that asks for no server was given upstream %q", up.Name)
	}
}

func TestRegression_PlainClientOfSNIPortIsRefused(t *testing.T) {
	h := regNewHarness(t)
	h.proxy.AddUpstreams(map[string]*TCPUpstream{"a": {Name: "a", Match: &UpstreamMatch{SNI: "a.example.com"}, Endpoints: map[string]*TCPEndpoint{"e": {Address: regClosedAddress(t)}}}})
	c := h.dial()
	c.Write([]byte("GET / HTTP/1.1\r\n\r\n"))
	c.SetReadDeadline(time.Now().Add(regWait))
	if n, err := c.Read(make([]byte, 10)); err != io.EOF && (err == nil || !strings.Contains(err.Error(), "reset")) {
		t.Errorf("read %d bytes, %v; want the connection to be refused", n, err)
	}
	regWaitGroup(t, &h.wg, "the proxied connection")
	if r := regReport(t, h.proxy); r.Rejects[""] != 1 {
		t.Errorf("rejects = %v, want the client to be counted as rejected", r.Rejects)
	}
}

// A TLS record of no length crashed the SNI parser, and took the server along.
func TestRegression_MalformedHelloDoesNotPanic(t *testing.T) {
	h := regNewHarness(t)
	h.proxy.AddUpstreams(map[string]*TCPUpstream{"a": {Name: "a", Match: &UpstreamMatch{SNI: "a.example.com"}, Endpoints: map[string]*TCPEndpoint{"e": {Address: regClosedAddress(t)}}}})
	c := h.dial()
	c.Write([]byte{0x16, 0x03, 0x01, 0x00, 0x00})
	c.SetReadDeadline(time.Now().Add(regWait))
	if n, err := c.Read(make([]byte, 10)); err != io.EOF && (err == nil || !strings.Contains(err.Error(), "reset")) {
		t.Errorf("read %d bytes, %v; want the connection to be closed", n, err)
	}
	regWaitGroup(t, &h.wg, "the proxied connection")
	if r := regReport(t, h.proxy); r.Rejects[""] != 1 {
		t.Errorf("rejects = %v, want the client to be counted as rejected", r.Rejects)
	}
}

// A client that connected to an SNI port and stayed silent kept a goroutine and a connection for ever. This takes as
// long as the proxy is willing to wait for a hello.
func TestRegression_SilentClientOfSNIPortIsDroppedEventually(t *testing.T) {
	h := regNewHarness(t)
	h.proxy.AddUpstreams(map[string]*TCPUpstream{"a": {Name: "a", Match: &UpstreamMatch{SNI: "a.example.com"}, Endpoints: map[string]*TCPEndpoint{"e": {Address: regClosedAddress(t)}}}})
	c := h.dial()
	c.SetReadDeadline(time.Now().Add(2 * regWait))
	if n, err := c.Read(make([]byte, 10)); err != io.EOF && (err == nil || !strings.Contains(err.Error(), "reset")) {
		t.Errorf("read %d bytes, %v; want the proxy to give up on the client", n, err)
	}
}

// Any "all" in the URI, such as the one in a query, made the reports cover all the ports.
func TestRegression_AllIsMatchedOnPathOnly(t *testing.T) {
	regSetup()
	other := regNextPort()
	GetPortProxy(other)
	t.Cleanup(func() {
		proxyLock.Lock()
		delete(portProxy, other)
		proxyLock.Unlock()
	})
	for _, uri := range []string{"/proxy/tcp?note=all", "/proxy/tcp/upstreams?note=all"} {
		rr, panicked := regCall("GET", uri, "")
		if panicked != nil {
			t.Fatalf("GET %s panicked: %v", uri, panicked)
		}
		var got map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got["port"] != float64(59000) {
			t.Errorf("GET %s = %v, want the report of a single port", uri, got)
		}
	}
}

// Asking whether a port is proxied registered a proxy for the port, for every port that a connection ever came to.
func TestRegression_WillProxyTCPDoesNotRegisterPorts(t *testing.T) {
	regSetup()
	port := regNextPort()
	if WillProxyTCP(port) {
		t.Fatal("nothing is configured for the port")
	}
	proxyLock.RLock()
	_, found := portProxy[port]
	proxyLock.RUnlock()
	if found {
		proxyLock.Lock()
		delete(portProxy, port)
		proxyLock.Unlock()
		t.Error("asking whether a port is proxied registered a proxy for it")
	}
}

// The proxy opened a listener for the API call, whatever the address of the upstream was.
func TestRegression_InvalidUpstreamAddressDoesNotOpenListener(t *testing.T) {
	regServeListeners()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	t.Cleanup(func() {
		if l := listeners.GetListenerForPort(port); l != nil {
			l.Close()
			listeners.RemoveListener(l)
		}
		proxyLock.Lock()
		delete(portProxy, port)
		proxyLock.Unlock()
	})
	rr, panicked := regCall("POST", fmt.Sprintf("/proxy/tcp/%d?address=not-an-address", port), "")
	if panicked != nil {
		t.Fatalf("the handler panicked: %v", panicked)
	}
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, body %q; want 400", rr.Code, rr.Body.String())
	}
	if listeners.GetListenerForPort(port) != nil {
		t.Error("a listener was opened for an upstream that can't be connected to")
	}
}

// Everything that the proxy does, and the reports of it, go on at the same time. The maps of the proxies and the
// counters of the trackers were read while they were written. This is a test for the race detector.
func TestRegression_ConcurrentAdminAndTraffic(t *testing.T) {
	echo := regEchoServer(t)
	h := regNewHarness(t)
	h.route(1, nil, echo.addr())
	extra := regNextPort() * 10
	t.Cleanup(func() {
		proxyLock.Lock()
		for i := 0; i < 50; i++ {
			delete(portProxy, extra+i)
		}
		proxyLock.Unlock()
	})
	stop := make(chan struct{})
	var admin sync.WaitGroup
	loop := func(f func(i int)) {
		admin.Add(1)
		go func() {
			defer admin.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
					f(i)
					time.Sleep(100 * time.Microsecond)
				}
			}
		}()
	}
	var panics sync.Map
	guard := func(name string, f func(i int)) func(int) {
		return func(i int) {
			defer func() {
				if r := recover(); r != nil {
					panics.Store(name, r)
				}
			}()
			f(i)
		}
	}
	loop(guard("report all", func(int) { regCall("GET", "/proxy/tcp/all", "") }))
	loop(guard("report upstreams", func(int) { regCall("GET", "/proxy/tcp/upstreams/all", "") }))
	loop(guard("new ports", func(i int) { GetPortProxy(extra + i%50) }))
	loop(guard("will proxy", func(int) { WillProxyTCP(h.port) }))
	loop(guard("add upstreams", func(int) {
		h.proxy.AddUpstreams(map[string]*TCPUpstream{"up": {Name: "up", Retries: 1, Endpoints: map[string]*TCPEndpoint{"ep1": {Name: "ep1", Address: echo.addr()}}}})
	}))
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := net.DialTimeout("tcp", h.ln.Addr().String(), regWait)
			if err != nil {
				errs <- err
				return
			}
			defer c.Close()
			c.SetDeadline(time.Now().Add(regWait))
			for round := 0; round < 10; round++ {
				if _, err := c.Write([]byte("0123456789")); err != nil {
					errs <- err
					return
				}
				if _, err := io.ReadFull(c, make([]byte, 10)); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	regWaitGroup(t, &wg, "clients")
	close(stop)
	regWaitGroup(t, &admin, "admin activity")
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	panics.Range(func(k, v any) bool {
		t.Errorf("%s panicked: %v", k, v)
		return true
	})
}
