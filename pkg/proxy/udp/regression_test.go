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

// The tests in this file are about bugs that were fixed in the proxy. They only use what the proxy had before the fixes
// (and the packages around it), and have helpers of their own, so that they can be run against the code from before.

import (
	"context"
	"encoding/binary"
	"fmt"
	"goto/pkg/global"
	"goto/pkg/server/listeners"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
)

var (
	regSetupOnce  sync.Once
	regRoutesOnce sync.Once
)

func regSetup() {
	regSetupOnce.Do(func() {
		log.SetOutput(io.Discard)
		global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
		global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
		global.Funcs.IsListenerPresent = listeners.IsListenerPresent
		global.Funcs.IsListenerOpen = listeners.IsListenerOpen
		global.Funcs.GetListenerID = func(int) string { return "" }
		global.Funcs.GetListenerLabel = func(*http.Request) string { return "" }
		global.Funcs.GetListenerLabelForPort = func(int) string { return "" }
		global.Funcs.CloseConnectionsForPort = func(int) {}
		util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
	})
}

// regRouter returns the router that serves the admin APIs of the proxy, which are mounted once for the process.
func regRouter() *mux.Router {
	regRoutesOnce.Do(func() {
		regSetup()
		Middleware.SetRoutes(nil)
	})
	return middleware.RootRouters["/proxy"]
}

func regCall(method, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(""))
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, 8080))
	_, req, _ = util.WithRequestStore(req, nil)
	rr := httptest.NewRecorder()
	regRouter().ServeHTTP(rr, req)
	return rr
}

// regPort takes a port that nothing uses, and cleans up what a test leaves on it: the listener, and the proxy.
func regPort(t *testing.T) int {
	t.Helper()
	regRouter()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		t.Fatal(err)
	}
	port := c.LocalAddr().(*net.UDPAddr).Port
	c.Close()
	t.Cleanup(func() {
		if l := listeners.GetListenerForPort(port); l != nil {
			l.Close()
			listeners.RemoveListener(l)
		}
		proxyLock.Lock()
		delete(udpProxyByPort, port)
		proxyLock.Unlock()
	})
	return port
}

// regListenerPort is a port that has a UDP listener, the way the API of the proxy opens one.
func regListenerPort(t *testing.T) int {
	t.Helper()
	port := regPort(t)
	if err := listeners.AddListener(port, false, true, ""); err != nil {
		t.Fatal(err)
	}
	return port
}

func regProxyOf(port int) *UDPProxy {
	proxyLock.RLock()
	defer proxyLock.RUnlock()
	return udpProxyByPort[port]
}

// regEcho is a UDP server that answers every packet with the prefix and the packet.
type regEcho struct {
	conn   *net.UDPConn
	prefix string
	jitter time.Duration
	wg     sync.WaitGroup
	mu     sync.Mutex
	sizes  []int
}

func regStartEcho(t *testing.T, prefix string, jitter time.Duration) *regEcho {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	e := &regEcho{conn: conn, prefix: prefix, jitter: jitter}
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		buff := make([]byte, 65535)
		for {
			n, from, err := conn.ReadFromUDP(buff)
			if err != nil {
				return
			}
			packet := append([]byte(nil), buff[:n]...)
			e.mu.Lock()
			e.sizes = append(e.sizes, n)
			e.mu.Unlock()
			e.wg.Add(1)
			go func() {
				defer e.wg.Done()
				if e.jitter > 0 {
					time.Sleep(time.Duration(rand.Int63n(int64(e.jitter))))
				}
				conn.WriteToUDP(append([]byte(e.prefix), packet...), from)
			}()
		}
	}()
	t.Cleanup(func() {
		conn.Close()
		done := make(chan struct{})
		go func() { e.wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Errorf("timed out waiting for the udp server to stop")
		}
	})
	return e
}

func (e *regEcho) addr() string { return e.conn.LocalAddr().String() }

func (e *regEcho) received() []int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]int{}, e.sizes...)
}

func regClient(t *testing.T, port int) *net.UDPConn {
	t.Helper()
	c, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// regExchange sends a packet and waits for the answer, which doesn't call anything of the test, so it can be used by
// goroutines.
func regExchange(c *net.UDPConn, msg string) (string, error) {
	c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Write([]byte(msg)); err != nil {
		return "", err
	}
	buff := make([]byte, 65535)
	n, err := c.Read(buff)
	if err != nil {
		return "", fmt.Errorf("no answer to %q: %w", msg, err)
	}
	return string(buff[:n]), nil
}

func regAsk(t *testing.T, c *net.UDPConn, msg string) string {
	t.Helper()
	got, err := regExchange(c, msg)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// regExpectSilence expects that the packet gets no answer.
func regExpectSilence(t *testing.T, c *net.UDPConn, msg string, wait time.Duration) {
	t.Helper()
	if _, err := c.Write([]byte(msg)); err != nil {
		t.Fatal(err)
	}
	c.SetReadDeadline(time.Now().Add(wait))
	if n, err := c.Read(make([]byte, 65535)); err == nil {
		t.Fatalf("got an answer of %d bytes, want none", n)
	}
}

func regWait(t *testing.T, wg *sync.WaitGroup, what string) {
	t.Helper()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// ---------------------------------------------------------------------------------------------------------------

// The proxy never received a packet: it read the listener only if the listener was connected to a remote address,
// which a listener never is, so that every read was refused as an error.
func TestRegression_ProxyRelaysPacketsToTheUpstreamAndBack(t *testing.T) {
	regSetup()
	echo := regStartEcho(t, "up:", 0)
	port := regListenerPort(t)
	ProxyUDPUpstream(port, echo.addr(), 0, 0)
	c := regClient(t, port)
	if got := regAsk(t, c, "hello"); got != "up:hello" {
		t.Errorf("answer = %q, want %q", got, "up:hello")
	}
	// The upstream got the packet as it was sent, without the rest of the buffer that it was read into
	if sizes := echo.received(); len(sizes) != 1 || sizes[0] != len("hello") {
		t.Errorf("sizes of the packets that the upstream received = %v, want [%d]", sizes, len("hello"))
	}
	big := strings.Repeat("y", 6000)
	if got := regAsk(t, c, big); got != "up:"+big {
		t.Errorf("a packet of 6000 bytes was answered with %d bytes, want %d", len(got), len(big)+3)
	}
}

// A listener that has nothing to read must not make the proxy loop: it used to log an error and go on, in a loop with
// nothing in it that waits.
func TestRegression_IdleProxyDoesNotSpin(t *testing.T) {
	regSetup()
	echo := regStartEcho(t, "", 0)
	port := regListenerPort(t)
	counter := &regLogCounter{}
	log.SetOutput(counter)
	restored := false
	restore := func() {
		if !restored {
			restored = true
			log.SetOutput(io.Discard)
		}
	}
	t.Cleanup(restore)
	ProxyUDPUpstream(port, echo.addr(), 0, 0)
	time.Sleep(300 * time.Millisecond)
	restore()
	if lines := counter.lines.Load(); lines > 50 {
		t.Errorf("an idle proxy wrote %d lines of log in 300ms, want it to wait for packets", lines)
	}
}

type regLogCounter struct{ lines atomic.Int64 }

func (c *regLogCounter) Write(p []byte) (int, error) {
	c.lines.Add(1)
	return len(p), nil
}

// Every packet used the one connection to the upstream, so that the answer that a goroutine read could be the answer
// to another packet, and clients got each other's answers.
func TestRegression_ConcurrentClientsGetTheirOwnAnswers(t *testing.T) {
	regSetup()
	echo := regStartEcho(t, "", 25*time.Millisecond)
	port := regListenerPort(t)
	ProxyUDPUpstream(port, echo.addr(), 0, 0)
	const clients, rounds = 8, 6
	errs := make(chan error, clients)
	var wg sync.WaitGroup
	for i := 0; i < clients; i++ {
		c := regClient(t, port)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for round := 0; round < rounds; round++ {
				msg := fmt.Sprintf("client %d round %d", i, round)
				got, err := regExchange(c, msg)
				if err != nil {
					errs <- err
					return
				}
				if got != msg {
					errs <- fmt.Errorf("sent %q, got %q", msg, got)
					return
				}
			}
		}()
	}
	regWait(t, &wg, "clients")
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if n := len(echo.received()); n != clients*rounds {
		t.Errorf("the upstream received %d packets, want %d", n, clients*rounds)
	}
}

// Stopping an upstream didn't stop it: the loop that served it listened to a channel that was never made, and
// connected to the upstream again as soon as it found that the connection had been closed.
func TestRegression_StoppedUpstreamGetsNoPackets(t *testing.T) {
	regSetup()
	echo := regStartEcho(t, "", 0)
	port := regListenerPort(t)
	ProxyUDPUpstream(port, echo.addr(), 0, 0)
	c := regClient(t, port)
	if got := regAsk(t, c, "one"); got != "one" {
		t.Fatalf("answer = %q", got)
	}
	StopUDPUpstream(port, echo.addr())
	regExpectSilence(t, c, "two", 300*time.Millisecond)
	regExpectSilence(t, c, "three", 100*time.Millisecond)
	if n := len(echo.received()); n != 1 {
		t.Errorf("the upstream received %d packets, want only the one from before it was stopped", n)
	}
	up := regProxyOf(port).Upstreams[echo.addr()]
	up.lock.RLock()
	running := up.isRunning
	up.lock.RUnlock()
	if running {
		t.Error("the upstream is marked as running")
	}
	// and it can be started again
	ProxyUDPUpstream(port, echo.addr(), 0, 0)
	if got := regAsk(t, c, "four"); got != "four" {
		t.Errorf("answer = %q", got)
	}
}

// Every start of an upstream started one more goroutine to read the listener, and none was stopped, so after some
// restarts the packets were taken by several goroutines that didn't know about each other.
func TestRegression_RestartsKeepOneAnswerPerPacket(t *testing.T) {
	regSetup()
	echo := regStartEcho(t, "", 0)
	port := regListenerPort(t)
	for i := 0; i < 10; i++ {
		ProxyUDPUpstream(port, echo.addr(), 0, 0)
	}
	c := regClient(t, port)
	for i := 0; i < 5; i++ {
		msg := fmt.Sprintf("packet %d", i)
		if got := regAsk(t, c, msg); got != msg {
			t.Fatalf("answer = %q, want %q", got, msg)
		}
	}
	// no packet is answered twice
	c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if n, err := c.Read(make([]byte, 100)); err == nil {
		t.Errorf("got an extra answer of %d bytes", n)
	}
	if n := len(echo.received()); n != 5 {
		t.Errorf("the upstream received %d packets, want 5", n)
	}
}

// Several upstreams of a port competed for the packets of the listener, each with a goroutine reading it. The packets
// go to the upstreams in turn.
func TestRegression_UpstreamsOfAPortTakeTurns(t *testing.T) {
	regSetup()
	a := regStartEcho(t, "A:", 0)
	b := regStartEcho(t, "B:", 0)
	port := regListenerPort(t)
	ProxyUDPUpstream(port, a.addr(), 0, 0)
	ProxyUDPUpstream(port, b.addr(), 0, 0)
	c := regClient(t, port)
	answers := map[string]int{}
	for i := 0; i < 10; i++ {
		answers[regAsk(t, c, "q")[:2]]++
	}
	if answers["A:"] != 5 || answers["B:"] != 5 || len(answers) != 2 {
		t.Errorf("answers by upstream = %v, want 5 of each", answers)
	}
	if len(a.received()) != 5 || len(b.received()) != 5 {
		t.Errorf("packets received: a %d, b %d, want 5 each", len(a.received()), len(b.received()))
	}
}

// Clients were not counted at all, and packets were only counted if they were DNS queries.
func TestRegression_ClientsAndPacketsAreCounted(t *testing.T) {
	regSetup()
	echo := regStartEcho(t, "", 0)
	port := regListenerPort(t)
	ProxyUDPUpstream(port, echo.addr(), 0, 0)
	for i := 0; i < 3; i++ {
		c := regClient(t, port)
		regAsk(t, c, "first")
		regAsk(t, c, "second")
	}
	tr := regProxyOf(port).UDPTracker
	tr.lock.RLock()
	defer tr.lock.RUnlock()
	if tr.ConnCount != 3 {
		t.Errorf("conn count = %d, want 3 clients", tr.ConnCount)
	}
	if tr.PacketCount != 6 || tr.PacketCountByUpstream[echo.addr()] != 6 {
		t.Errorf("packet count = %d, by upstream %v, want 6 packets that aren't DNS queries counted", tr.PacketCount, tr.PacketCountByUpstream)
	}
}

// A packet without a domain was counted as one of the domain "".
func TestRegression_EmptyDomainIsNotCountedAsADomain(t *testing.T) {
	tr := NewUDPTracker()
	tr.IncrementPacketCounts("u:53", "")
	tr.IncrementPacketCounts("u:53", "a.com")
	if tr.PacketCount != 2 || tr.PacketCountByUpstream["u:53"] != 2 {
		t.Errorf("packet count = %d, by upstream %v", tr.PacketCount, tr.PacketCountByUpstream)
	}
	if _, found := tr.PacketCountByDomain[""]; found || len(tr.PacketCountByDomain) != 1 {
		t.Errorf("by domain = %v, want only a.com", tr.PacketCountByDomain)
	}
	if _, found := tr.PacketCountByUpstreamDomain["u:53"][""]; found {
		t.Errorf("by upstream and domain = %v, want only a.com", tr.PacketCountByUpstreamDomain)
	}
}

// The pointer to a name that a DNS message can have in the place of a label was taken for the length of a label.
func TestRegression_ExtractDomainIgnoresCompressionPointers(t *testing.T) {
	packet := make([]byte, 12)
	binary.BigEndian.PutUint16(packet[4:], 1)
	packet = append(packet, 0xC0)
	packet = append(packet, []byte(strings.Repeat("x", 192))...)
	packet = append(packet, 0)
	if got := extractDomain(packet); got != "" {
		t.Errorf("domain = %q, want none for a name that is a pointer", got)
	}
}

// Setting the delay or stopping an upstream of a port that the proxy knows nothing about made a proxy for the port.
func TestRegression_DelayAndStopOfUnknownPortDoNotMakeProxies(t *testing.T) {
	regSetup()
	port := regPort(t)
	SetUDPDelay(port, "127.0.0.1:53", time.Second, time.Second)
	StopUDPUpstream(port, "127.0.0.1:53")
	if p := regProxyOf(port); p != nil {
		t.Errorf("a proxy was made for port %d: %+v", port, p)
	}
}

// Two callers that asked for the proxy of a new port at the same time could get proxies of their own, and one of the two
// was forgotten.
func TestRegression_ProxyOfAPortIsOne(t *testing.T) {
	regSetup()
	const ports, callers = 40, 16
	base := regPort(t)
	t.Cleanup(func() {
		proxyLock.Lock()
		for i := 0; i < ports; i++ {
			delete(udpProxyByPort, base+1000+i)
		}
		proxyLock.Unlock()
	})
	got := make([][]*UDPProxy, ports)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < ports; i++ {
		got[i] = make([]*UDPProxy, callers)
		for j := 0; j < callers; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				got[i][j] = getUDPProxyForPort(base + 1000 + i)
			}()
		}
	}
	close(start)
	regWait(t, &wg, "callers")
	for i := 0; i < ports; i++ {
		for j := 1; j < callers; j++ {
			if got[i][j] != got[i][0] {
				t.Fatalf("port %d: callers got different proxies", base+1000+i)
			}
		}
		if registered := regProxyOf(base + 1000 + i); registered != got[i][0] {
			t.Fatalf("port %d: the proxy that was registered is not the one that the callers got", base+1000+i)
		}
	}
}

// A port without a listener made the goroutine of the proxy fail on a nil listener, which ended the server.
func TestRegression_ProxyingOnAPortWithoutListenerDoesNotCrash(t *testing.T) {
	regSetup()
	port := regPort(t)
	ProxyUDPUpstream(port, "127.0.0.1:53", 0, 0)
	time.Sleep(200 * time.Millisecond)
	if p := regProxyOf(port); p != nil {
		p.lock.RLock()
		n := len(p.Upstreams)
		p.lock.RUnlock()
		if n != 0 {
			t.Errorf("%d upstreams were set up for a port without a listener", n)
		}
	}
}

// The API opened the listener of the port for an upstream that it couldn't use, and for a delay that it couldn't read.
func TestRegression_APIRejectsUnusableUpstreamAndDelay(t *testing.T) {
	regSetup()
	for _, tt := range []struct{ name, uri string }{
		{"address without a port", "/proxy/udp/%d/nonsense"},
		{"address with port zero", "/proxy/udp/%d/127.0.0.1:0"},
		{"address with a port out of range", "/proxy/udp/%d/127.0.0.1:99999"},
		{"delay that isn't a duration", "/proxy/udp/%d/127.0.0.1:53/delay/abc"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			port := regPort(t)
			rr := regCall("POST", fmt.Sprintf(tt.uri, port))
			if rr.Code != http.StatusBadRequest {
				t.Errorf("status = %d, body %q, want 400", rr.Code, rr.Body.String())
			}
			if listeners.IsListenerPresent(port) {
				t.Errorf("a listener was opened for the request that was refused")
			}
		})
	}
}

// The delay API looked for an upstream that the URI has no name for, so that it never set a delay, and said it did.
func TestRegression_DelayAPISetsTheDelayOfTheUpstreams(t *testing.T) {
	regSetup()
	echo := regStartEcho(t, "", 0)
	port := regListenerPort(t)
	ProxyUDPUpstream(port, echo.addr(), 0, 0)
	rr := regCall("POST", fmt.Sprintf("/proxy/udp/%d/delay/50ms", port))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rr.Code, rr.Body.String())
	}
	up := regProxyOf(port).Upstreams[echo.addr()]
	up.lock.RLock()
	d := up.Delay
	up.lock.RUnlock()
	if d == nil || d.Min.Duration != 50*time.Millisecond || d.Max.Duration != 50*time.Millisecond {
		t.Fatalf("delay of the upstream = %+v, want 50ms", d)
	}
	c := regClient(t, port)
	start := time.Now()
	regAsk(t, c, "slow")
	if took := time.Since(start); took < 100*time.Millisecond {
		t.Errorf("the round trip took %s, want the packet and the answer delayed", took)
	}
}

// The delay API said that it set a delay, for a port that has nothing to set it for.
func TestRegression_DelayAPIWithoutUpstreamsIsRefused(t *testing.T) {
	regSetup()
	port := regListenerPort(t)
	rr := regCall("POST", fmt.Sprintf("/proxy/udp/%d/delay/50ms", port))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, body %q, want 400", rr.Code, rr.Body.String())
	}
}
