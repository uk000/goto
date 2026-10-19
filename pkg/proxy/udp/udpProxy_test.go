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
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"goto/pkg/global"
	"goto/pkg/server/listeners"
	"goto/pkg/types"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goto/pkg/util"
)

const waitTimeout = 5 * time.Second

var (
	setupOnce sync.Once
	portSeq   atomic.Int32
)

func setup() {
	setupOnce.Do(func() {
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

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitGroup(t *testing.T, wg *sync.WaitGroup, what string) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(waitTimeout):
		t.Errorf("timed out waiting for %s", what)
	}
}

// shortenUpstreamTimeout makes the proxy give up on upstreams that don't answer quickly. The timeout is read by the
// goroutines of the proxy, so it must only be changed while no packet is being proxied.
func shortenUpstreamTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	old := upstreamTimeout
	upstreamTimeout = d
	t.Cleanup(func() { upstreamTimeout = old })
}

// udpServer is a loopback UDP server that answers packets. The answer is the prefix and the packet, unless the server
// is told not to answer.
type udpServer struct {
	conn     *net.UDPConn
	prefix   string
	silent   atomic.Bool
	received atomic.Int32
	wg       sync.WaitGroup
	mu       sync.Mutex
	packets  [][]byte
	// the answers take a random time up to this long (in nanoseconds), so that they are not given in the order of the
	// questions
	jitter atomic.Int64
}

func startUDPServer(t *testing.T, prefix string) *udpServer {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	s := &udpServer{conn: conn, prefix: prefix}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		buff := make([]byte, 65535)
		for {
			n, from, err := conn.ReadFromUDP(buff)
			if err != nil {
				return
			}
			s.received.Add(1)
			packet := append([]byte(nil), buff[:n]...)
			s.mu.Lock()
			s.packets = append(s.packets, packet)
			s.mu.Unlock()
			if s.silent.Load() {
				continue
			}
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				if j := s.jitter.Load(); j > 0 {
					time.Sleep(time.Duration(rand.Int63n(j)))
				}
				conn.WriteToUDP(append([]byte(s.prefix), packet...), from)
			}()
		}
	}()
	t.Cleanup(func() {
		conn.Close()
		waitGroup(t, &s.wg, "udp server to stop")
	})
	return s
}

func (s *udpServer) Addr() string { return s.conn.LocalAddr().String() }

func (s *udpServer) got() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]byte{}, s.packets...)
}

// harness is a proxy that reads from a loopback socket, which stands for the listener of the port.
type harness struct {
	t     *testing.T
	proxy *UDPProxy
	conn  *net.UDPConn
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	setup()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, conn: conn, proxy: newUDPProxy(conn.LocalAddr().(*net.UDPAddr).Port)}
	t.Cleanup(func() {
		conn.Close()
		waitFor(t, "the proxy to stop reading from the closed socket", func() bool { return !h.proxy.isServing(conn) })
		// The packets that are being proxied use the settings that the test may restore once this is over. Taking all the
		// slots for packets in flight is what tells that they are over, in a way that the race detector understands.
		deadline := time.After(waitTimeout)
		for i := 0; i < cap(h.proxy.inFlight); i++ {
			select {
			case h.proxy.inFlight <- struct{}{}:
			case <-deadline:
				t.Errorf("timed out waiting for the packets in flight to be over")
				return
			}
		}
	})
	return h
}

func (h *harness) addr() string { return h.conn.LocalAddr().String() }

func (h *harness) start(upstream string, delayMin, delayMax time.Duration) {
	h.t.Helper()
	if err := h.proxy.startUpstream(fmt.Sprintf("%d-%s", h.proxy.Port, upstream), upstream, h.conn, delayMin, delayMax); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) client() *net.UDPConn {
	h.t.Helper()
	c, err := net.DialTimeout("udp", h.addr(), waitTimeout)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { c.Close() })
	return c.(*net.UDPConn)
}

// ask sends a packet and waits for the answer.
func ask(t *testing.T, c *net.UDPConn, packet []byte) []byte {
	t.Helper()
	c.SetWriteDeadline(time.Now().Add(waitTimeout))
	if _, err := c.Write(packet); err != nil {
		t.Fatal(err)
	}
	c.SetReadDeadline(time.Now().Add(waitTimeout))
	buff := make([]byte, 65535)
	n, err := c.Read(buff)
	if err != nil {
		t.Fatalf("no answer to %q: %v", packet, err)
	}
	return buff[:n]
}

// askAgain is for the packets that follow a stretch in which the test made the proxy's upstream timeout short. A
// packet whose answer comes later than that timeout, because the machine is busy, is given up on by the proxy like
// the ones before it, so it is sent again, as a UDP client would, before the test concludes that nothing comes back.
func askAgain(t *testing.T, c *net.UDPConn, packet []byte, attempts int) []byte {
	t.Helper()
	buff := make([]byte, 65535)
	for i := 0; i < attempts; i++ {
		c.SetWriteDeadline(time.Now().Add(waitTimeout))
		if _, err := c.Write(packet); err != nil {
			t.Fatal(err)
		}
		c.SetReadDeadline(time.Now().Add(time.Second))
		if n, err := c.Read(buff); err == nil {
			return append([]byte(nil), buff[:n]...)
		}
	}
	t.Fatalf("no answer to %q in %d attempts", packet, attempts)
	return nil
}

// expectNoAnswer sends a packet and expects nothing to come back for a while.
func expectNoAnswer(t *testing.T, c *net.UDPConn, packet []byte, wait time.Duration) {
	t.Helper()
	if _, err := c.Write(packet); err != nil {
		t.Fatal(err)
	}
	c.SetReadDeadline(time.Now().Add(wait))
	if n, err := c.Read(make([]byte, 65535)); err == nil {
		t.Fatalf("got an answer of %d bytes, want none", n)
	}
}

// dnsQuery builds a DNS query for the name.
func dnsQuery(id uint16, name string) []byte {
	b := make([]byte, 12)
	binary.BigEndian.PutUint16(b[0:], id)
	binary.BigEndian.PutUint16(b[2:], 0x0100)
	binary.BigEndian.PutUint16(b[4:], 1)
	for _, label := range strings.Split(name, ".") {
		b = append(b, byte(len(label)))
		b = append(b, label...)
	}
	b = append(b, 0, 0, 1, 0, 1)
	return b
}

// ---------------------------------------------------------------------------------------------------------------

func TestExtractDomain(t *testing.T) {
	query := dnsQuery(7, "www.example.com")
	truncatedInLabel := append([]byte{}, query[:16]...)
	noTerminator := query[:len(query)-5]
	noQuestion := append([]byte{}, query...)
	binary.BigEndian.PutUint16(noQuestion[4:], 0)
	withPointer := append(append([]byte{}, query[:12]...), 0xC0, 0x0C, 0, 0, 1, 0, 1)
	rootName := append(append([]byte{}, query[:12]...), 0, 0, 1, 0, 1)
	tests := []struct {
		name   string
		packet []byte
		want   string
	}{
		{"query", query, "www.example.com"},
		{"single label", dnsQuery(1, "localhost"), "localhost"},
		{"case is kept", dnsQuery(1, "WWW.Example.COM"), "WWW.Example.COM"},
		{"long name", dnsQuery(1, strings.Repeat("a", 63)+"."+strings.Repeat("b", 63)), strings.Repeat("a", 63) + "." + strings.Repeat("b", 63)},
		{"nil", nil, ""},
		{"too short for a header", query[:11], ""},
		{"header only", query[:12], ""},
		{"no question", noQuestion, ""},
		{"root name", rootName, ""},
		{"cut in a label", truncatedInLabel, ""},
		{"no end of name", noTerminator, ""},
		{"pointer instead of a label", withPointer, ""},
		{"text", []byte("this is not a dns query at all"), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractDomain(tt.packet); got != tt.want {
				t.Errorf("extractDomain = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStartUpstreamValidatesAddress(t *testing.T) {
	setup()
	p := newUDPProxy(1)
	for _, bad := range []string{"", "nonsense", "localhost", "127.0.0.1:notaport", "127.0.0.1:99999"} {
		if err := p.startUpstream("n", bad, nil, 0, 0); err == nil {
			t.Errorf("address %q was accepted", bad)
		}
	}
	if len(p.Upstreams) != 0 {
		t.Errorf("upstreams = %+v, want none to be added for invalid addresses", p.Upstreams)
	}
}

func TestStartUpstream(t *testing.T) {
	setup()
	p := newUDPProxy(5)
	if err := p.startUpstream("5-127.0.0.1:53", "127.0.0.1:53", nil, 10*time.Millisecond, 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	up := p.Upstreams["127.0.0.1:53"]
	if up == nil || up.Name != "5-127.0.0.1:53" || up.Protocol != "udp" || up.Endpoint != "127.0.0.1:53" {
		t.Fatalf("upstream = %+v", up)
	}
	if !up.running() || up.addr().String() != "127.0.0.1:53" {
		t.Errorf("running = %v, address = %v", up.running(), up.addr())
	}
	if up.Delay == nil || up.Delay.Min.Duration != 10*time.Millisecond || up.Delay.Max.Duration != 20*time.Millisecond || up.Delay.Count != -1 {
		t.Errorf("delay = %+v", up.Delay)
	}
	if p.conn != nil {
		t.Error("without a socket to read from, the proxy reads nothing")
	}
	// Starting it again is a restart of the same upstream
	up.Stop()
	if err := p.startUpstream("other-name", "127.0.0.1:53", nil, 0, 0); err != nil {
		t.Fatal(err)
	}
	if again := p.Upstreams["127.0.0.1:53"]; again != up || !up.running() || up.Delay != nil || up.Name != "5-127.0.0.1:53" {
		t.Errorf("restarted upstream = %+v, want the same one, running, without a delay", again)
	}
}

func TestGetUDPProxyForPortAndRegistry(t *testing.T) {
	setup()
	port := int(50000 + portSeq.Add(1))
	t.Cleanup(func() {
		proxyLock.Lock()
		delete(udpProxyByPort, port)
		proxyLock.Unlock()
	})
	if findUDPProxy(port) != nil {
		t.Fatal("no proxy expected for a port that was not used")
	}
	// Setting a delay or stopping on a port without a proxy is not an error, and doesn't create one
	SetUDPDelay(port, "127.0.0.1:1", time.Second, time.Second)
	StopUDPUpstream(port, "127.0.0.1:1")
	if n := setUDPDelayOfPort(port, "", time.Second, time.Second); n != 0 || findUDPProxy(port) != nil {
		t.Errorf("set the delay of %d upstreams, proxy = %v", n, findUDPProxy(port))
	}
	p := getUDPProxyForPort(port)
	if p == nil || p.Port != port || !p.Enabled || p.UDPTracker == nil || p.Upstreams == nil {
		t.Fatalf("proxy = %+v", p)
	}
	if getUDPProxyForPort(port) != p || findUDPProxy(port) != p {
		t.Error("the proxy of a port must be one and the same")
	}
	old := p.UDPTracker
	p.initTracker()
	if p.UDPTracker == old || p.UDPTracker == nil {
		t.Error("initTracker must start a new tracker")
	}
}

func TestGetUDPProxyForPortConcurrently(t *testing.T) {
	setup()
	const ports, callers = 40, 16
	base := int(60000 + portSeq.Add(1)*1000)
	t.Cleanup(func() {
		proxyLock.Lock()
		for i := 0; i < ports; i++ {
			delete(udpProxyByPort, base+i)
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
				got[i][j] = getUDPProxyForPort(base + i)
			}()
		}
	}
	close(start)
	waitGroup(t, &wg, "callers")
	for i := 0; i < ports; i++ {
		for j := 0; j < callers; j++ {
			if got[i][j] != findUDPProxy(base+i) {
				t.Fatalf("port %d: a caller got a proxy that is not the registered one", base+i)
			}
		}
	}
}

func TestProxyRelaysPacketsAndAnswers(t *testing.T) {
	srv := startUDPServer(t, "A:")
	h := newHarness(t)
	h.start(srv.Addr(), 0, 0)
	c := h.client()
	for _, msg := range []string{"one", "two", strings.Repeat("x", 3000)} {
		if got := ask(t, c, []byte(msg)); string(got) != "A:"+msg {
			t.Fatalf("answer to %d bytes = %d bytes %q", len(msg), len(got), got[:5])
		}
	}
	if srv.received.Load() != 3 {
		t.Errorf("the upstream received %d packets, want 3", srv.received.Load())
	}
	tr := h.proxy.tracker()
	tr.lock.RLock()
	defer tr.lock.RUnlock()
	if tr.PacketCount != 3 || tr.PacketCountByUpstream[srv.Addr()] != 3 || tr.ConnCount != 1 {
		t.Errorf("packets %d, by upstream %v, conns %d; want 3 packets for the upstream, from one client", tr.PacketCount, tr.PacketCountByUpstream, tr.ConnCount)
	}
	if len(tr.PacketCountByDomain) != 0 {
		t.Errorf("packets that aren't DNS queries have no domain, got %v", tr.PacketCountByDomain)
	}
}

func TestProxyCountsDNSQueriesByDomain(t *testing.T) {
	srv := startUDPServer(t, "")
	h := newHarness(t)
	h.start(srv.Addr(), 0, 0)
	c := h.client()
	for i, name := range []string{"a.example.com", "b.example.com", "a.example.com"} {
		q := dnsQuery(uint16(i), name)
		if got := ask(t, c, q); !bytes.Equal(got, q) {
			t.Fatalf("answer to %s = %v", name, got)
		}
	}
	tr := h.proxy.tracker()
	tr.lock.RLock()
	defer tr.lock.RUnlock()
	up := srv.Addr()
	if tr.PacketCount != 3 || tr.PacketCountByUpstream[up] != 3 ||
		!equalCounts(tr.PacketCountByDomain, map[string]int{"a.example.com": 2, "b.example.com": 1}) ||
		!equalCounts(tr.PacketCountByUpstreamDomain[up], map[string]int{"a.example.com": 2, "b.example.com": 1}) {
		t.Errorf("tracker = %+v", tr)
	}
}

func equalCounts(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestProxyCountsClientsAsConnections(t *testing.T) {
	srv := startUDPServer(t, "")
	h := newHarness(t)
	h.start(srv.Addr(), 0, 0)
	c1, c2, c3 := h.client(), h.client(), h.client()
	for round := 0; round < 3; round++ {
		for _, c := range []*net.UDPConn{c1, c2, c3} {
			ask(t, c, []byte("hi"))
		}
	}
	tr := h.proxy.tracker()
	tr.lock.RLock()
	defer tr.lock.RUnlock()
	if tr.ConnCount != 3 || tr.PacketCount != 9 {
		t.Errorf("conns %d, packets %d; want 3 clients and 9 packets", tr.ConnCount, tr.PacketCount)
	}
}

// Every client gets the answers to its own packets, however many are being proxied at the same time
func TestProxyAnswersEachClientItsOwnPackets(t *testing.T) {
	srv := startUDPServer(t, "")
	srv.jitter.Store(int64(30 * time.Millisecond))
	h := newHarness(t)
	h.start(srv.Addr(), 0, 0)
	const clients, rounds = 16, 10
	errs := make(chan error, clients)
	var wg sync.WaitGroup
	for i := 0; i < clients; i++ {
		c := h.client()
		wg.Add(1)
		go func() {
			defer wg.Done()
			buff := make([]byte, 100)
			for round := 0; round < rounds; round++ {
				msg := fmt.Sprintf("client %d round %d", i, round)
				c.SetDeadline(time.Now().Add(waitTimeout))
				if _, err := c.Write([]byte(msg)); err != nil {
					errs <- err
					return
				}
				n, err := c.Read(buff)
				if err != nil {
					errs <- fmt.Errorf("%s: %w", msg, err)
					return
				}
				if string(buff[:n]) != msg {
					errs <- fmt.Errorf("sent %q, got %q", msg, buff[:n])
					return
				}
			}
		}()
	}
	waitGroup(t, &wg, "clients")
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	tr := h.proxy.tracker()
	tr.lock.RLock()
	defer tr.lock.RUnlock()
	if tr.PacketCount != clients*rounds || tr.ConnCount != clients {
		t.Errorf("packets %d, conns %d; want %d and %d", tr.PacketCount, tr.ConnCount, clients*rounds, clients)
	}
}

func TestProxyGivesUpOnSilentUpstreamAndGoesOn(t *testing.T) {
	shortenUpstreamTimeout(t, 150*time.Millisecond)
	srv := startUDPServer(t, "")
	srv.silent.Store(true)
	h := newHarness(t)
	h.start(srv.Addr(), 0, 0)
	c := h.client()
	expectNoAnswer(t, c, []byte("lost"), 400*time.Millisecond)
	if srv.received.Load() != 1 {
		t.Errorf("the upstream received %d packets, want 1", srv.received.Load())
	}
	waitFor(t, "the packet to be given up on", func() bool { return len(h.proxy.inFlight) == 0 })
	// The upstream recovers, and the proxy has no memory of the packet it gave up on
	srv.silent.Store(false)
	if got := askAgain(t, c, []byte("again"), 5); string(got) != "again" {
		t.Errorf("answer = %q", got)
	}
}

func TestProxyDropsPacketsBeyondInFlightLimit(t *testing.T) {
	old := maxInFlight
	maxInFlight = 2
	t.Cleanup(func() { maxInFlight = old })
	shortenUpstreamTimeout(t, 500*time.Millisecond)
	srv := startUDPServer(t, "")
	srv.silent.Store(true)
	h := newHarness(t)
	h.start(srv.Addr(), 0, 0)
	c := h.client()
	for i := 0; i < 6; i++ {
		c.Write([]byte{byte(i)})
	}
	time.Sleep(150 * time.Millisecond)
	if got := srv.received.Load(); got != 2 {
		t.Errorf("the upstream received %d packets, want the 2 that fit", got)
	}
	// Once the packets in flight are over, there's room again
	waitFor(t, "the packets in flight to be over", func() bool { return len(h.proxy.inFlight) == 0 })
	srv.silent.Store(false)
	if got := askAgain(t, c, []byte("room"), 5); string(got) != "room" {
		t.Errorf("answer = %q", got)
	}
}

func TestProxyDelaysPacketsAndAnswers(t *testing.T) {
	srv := startUDPServer(t, "")
	h := newHarness(t)
	h.start(srv.Addr(), 100*time.Millisecond, 100*time.Millisecond)
	c := h.client()
	start := time.Now()
	ask(t, c, []byte("slow"))
	// the packet and the answer are delayed
	if took := time.Since(start); took < 200*time.Millisecond {
		t.Errorf("the round trip took %s, want it delayed twice by 100ms", took)
	}
	// A delay of nothing clears the delay
	h.start(srv.Addr(), 0, 0)
	start = time.Now()
	ask(t, c, []byte("fast"))
	if took := time.Since(start); took > 150*time.Millisecond {
		t.Errorf("the round trip took %s, want no delay", took)
	}
}

func TestSetUDPDelay(t *testing.T) {
	setup()
	p := newUDPProxy(1)
	for _, ep := range []string{"127.0.0.1:1", "127.0.0.1:2", "127.0.0.1:3"} {
		if err := p.startUpstream("n", ep, nil, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	if n := p.setUDPDelay("127.0.0.1:2", time.Second, 2*time.Second); n != 1 {
		t.Errorf("set the delay of %d upstreams, want 1", n)
	}
	for ep, up := range p.Upstreams {
		if has := up.Delay != nil; has != (ep == "127.0.0.1:2") {
			t.Errorf("upstream %s has a delay: %v", ep, has)
		}
	}
	if n := p.setUDPDelay("127.0.0.1:9", time.Second, time.Second); n != 0 {
		t.Errorf("set the delay of %d upstreams, want none for an unknown one", n)
	}
	if n := p.setUDPDelay("", 3*time.Second, 4*time.Second); n != 3 {
		t.Errorf("set the delay of %d upstreams, want all 3", n)
	}
	for ep, up := range p.Upstreams {
		if up.Delay == nil || up.Delay.Min.Duration != 3*time.Second || up.Delay.Max.Duration != 4*time.Second {
			t.Errorf("upstream %s delay = %+v", ep, up.Delay)
		}
	}
	p.setUDPDelay("", 0, 0)
	for ep, up := range p.Upstreams {
		if up.Delay != nil {
			t.Errorf("upstream %s still has a delay", ep)
		}
	}
}

func TestUpstreamNextDelay(t *testing.T) {
	up := newUDPUpstream("n", "127.0.0.1:1")
	if d := up.nextDelay(); d != 0 {
		t.Errorf("delay = %s, want none", d)
	}
	up.Delay = &types.Delay{}
	if d := up.nextDelay(); d != 0 {
		t.Errorf("delay of a config without min and max = %s", d)
	}
	up.Delay = types.NewDelay(time.Millisecond, time.Millisecond, 2)
	for i := 0; i < 2; i++ {
		if d := up.nextDelay(); d != time.Millisecond {
			t.Fatalf("delay %d = %s", i, d)
		}
	}
	if d := up.nextDelay(); d != 0 {
		t.Errorf("delay after the count ran out = %s", d)
	}
	up.setUDPDelay(10*time.Millisecond, 20*time.Millisecond)
	for i := 0; i < 20; i++ {
		if d := up.nextDelay(); d < 10*time.Millisecond || d > 20*time.Millisecond {
			t.Fatalf("delay = %s, outside of 10ms-20ms", d)
		}
	}
	// Used from several goroutines, a counted delay is used exactly as many times as it counts
	up.Delay = types.NewDelay(time.Millisecond, time.Millisecond, 300)
	var used atomic.Int32
	var wg sync.WaitGroup
	for g := 0; g < 6; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if up.nextDelay() > 0 {
					used.Add(1)
				}
			}
		}()
	}
	waitGroup(t, &wg, "callers")
	if used.Load() != 300 {
		t.Errorf("the delay was applied %d times, want 300", used.Load())
	}
}

func TestProxyTakesTurnsBetweenUpstreams(t *testing.T) {
	a := startUDPServer(t, "A:")
	b := startUDPServer(t, "B:")
	h := newHarness(t)
	h.start(a.Addr(), 0, 0)
	h.start(b.Addr(), 0, 0)
	first, second := "A:", "B:"
	if b.Addr() < a.Addr() {
		first, second = "B:", "A:"
	}
	c := h.client()
	for i := 0; i < 6; i++ {
		got := string(ask(t, c, []byte("q")))
		want := first + "q"
		if i%2 == 1 {
			want = second + "q"
		}
		if got != want {
			t.Fatalf("packet %d was answered %q, want %q: packets go to the upstreams in turn, in the order of their addresses", i, got, want)
		}
	}
	if a.received.Load() != 3 || b.received.Load() != 3 {
		t.Errorf("packets received: a %d, b %d, want 3 each", a.received.Load(), b.received.Load())
	}
}

func TestStopUDPUpstream(t *testing.T) {
	a := startUDPServer(t, "A:")
	b := startUDPServer(t, "B:")
	h := newHarness(t)
	h.start(a.Addr(), 0, 0)
	h.start(b.Addr(), 0, 0)
	c := h.client()
	h.proxy.stopUpstream(a.Addr())
	for i := 0; i < 4; i++ {
		if got := string(ask(t, c, []byte("q"))); got != "B:q" {
			t.Fatalf("answer = %q, want the one of the upstream that wasn't stopped", got)
		}
	}
	if a.received.Load() != 0 {
		t.Errorf("a stopped upstream received %d packets", a.received.Load())
	}
	// With nothing running, packets are dropped
	h.proxy.stopUpstream(b.Addr())
	expectNoAnswer(t, c, []byte("q"), 200*time.Millisecond)
	if b.received.Load() != 4 {
		t.Errorf("b received %d packets, want the 4 from before it was stopped", b.received.Load())
	}
	// A stopped upstream is started by asking for it again
	h.start(a.Addr(), 0, 0)
	if got := string(ask(t, c, []byte("q"))); got != "A:q" {
		t.Errorf("answer = %q, want the one of the upstream that was started again", got)
	}
	// Stopping what isn't there is fine
	h.proxy.stopUpstream("127.0.0.1:1")
}

func TestRemoveUpstream(t *testing.T) {
	a := startUDPServer(t, "A:")
	h := newHarness(t)
	h.start(a.Addr(), 0, 0)
	up := h.proxy.Upstreams[a.Addr()]
	h.proxy.removeUpstream("127.0.0.1:1")
	h.proxy.removeUpstream(a.Addr())
	if len(h.proxy.Upstreams) != 0 || up.running() {
		t.Errorf("upstreams = %+v, upstream running = %v; want it gone and stopped", h.proxy.Upstreams, up.running())
	}
	expectNoAnswer(t, h.client(), []byte("q"), 200*time.Millisecond)
}

// A proxy reads a socket with one goroutine, however many upstreams or restarts it gets: two readers of one socket would
// take packets from each other.
func TestProxyReadsFromOneSocketOnce(t *testing.T) {
	srv := startUDPServer(t, "")
	setup()
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	conn := &countingConn{UDPConn: udp}
	p := newUDPProxy(1)
	t.Cleanup(func() {
		conn.Close()
		waitFor(t, "the proxy to stop", func() bool { return !p.isServing(conn) })
	})
	for i := 0; i < 20; i++ {
		if err := p.startUpstream("n", srv.Addr(), conn, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	c, err := net.DialUDP("udp", nil, udp.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for i := 0; i < 5; i++ {
		ask(t, c, []byte("q"))
	}
	if srv.received.Load() != 5 {
		t.Errorf("the upstream received %d packets, want 5", srv.received.Load())
	}
	if max := conn.max.Load(); max != 1 {
		t.Errorf("the socket was read by %d goroutines at a time, want 1", max)
	}
}

// countingConn tells how many goroutines were reading from it at the same time, at most.
type countingConn struct {
	*net.UDPConn
	active, max atomic.Int32
}

func (c *countingConn) ReadFrom(b []byte) (int, net.Addr, error) {
	n := c.active.Add(1)
	defer c.active.Add(-1)
	for {
		m := c.max.Load()
		if n <= m || c.max.CompareAndSwap(m, n) {
			break
		}
	}
	return c.UDPConn.ReadFrom(b)
}

func TestProxyFollowsTheListenerThatIsReplaced(t *testing.T) {
	srv := startUDPServer(t, "")
	h := newHarness(t)
	h.start(srv.Addr(), 0, 0)
	ask(t, h.client(), []byte("before"))

	// the listener of a port gets closed, and opened again
	old := h.conn
	h.conn.Close()
	waitFor(t, "the proxy to stop reading from the closed socket", func() bool { return !h.proxy.isServing(old) })
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	h.conn = conn
	t.Cleanup(func() {
		conn.Close()
		waitFor(t, "the proxy to stop reading from the new socket", func() bool { return !h.proxy.isServing(conn) })
	})
	h.start(srv.Addr(), 0, 0)
	if got := ask(t, h.client(), []byte("after")); string(got) != "after" {
		t.Errorf("answer = %q", got)
	}
}

func TestProxyStopsReadingWhenSocketIsClosed(t *testing.T) {
	h := newHarness(t)
	srv := startUDPServer(t, "")
	h.start(srv.Addr(), 0, 0)
	if !h.proxy.isServing(h.conn) {
		t.Fatal("the proxy must be reading")
	}
	h.conn.Close()
	waitFor(t, "the proxy to stop", func() bool { return !h.proxy.isServing(h.conn) })
}

func TestProxyToUnreachableUpstreamDoesNotStall(t *testing.T) {
	shortenUpstreamTimeout(t, 200*time.Millisecond)
	// nothing listens on the port that the upstream is at
	closed, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	addr := closed.LocalAddr().String()
	closed.Close()
	h := newHarness(t)
	h.start(addr, 0, 0)
	c := h.client()
	expectNoAnswer(t, c, []byte("q"), 500*time.Millisecond)
	waitFor(t, "the packet to be given up on", func() bool { return len(h.proxy.inFlight) == 0 })
	tr := h.proxy.tracker()
	tr.lock.RLock()
	defer tr.lock.RUnlock()
	if tr.PacketCount != 1 {
		t.Errorf("packet count = %d, want the packet to be counted even if nobody answers", tr.PacketCount)
	}
}

func TestProxyAndAdminConcurrently(t *testing.T) {
	a := startUDPServer(t, "")
	b := startUDPServer(t, "")
	h := newHarness(t)
	h.start(a.Addr(), 0, 0)
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
					time.Sleep(200 * time.Microsecond)
				}
			}
		}()
	}
	loop(func(int) { h.proxy.startUpstream("n", b.Addr(), h.conn, time.Millisecond, 2*time.Millisecond) })
	loop(func(int) { h.proxy.stopUpstream(b.Addr()) })
	loop(func(int) { h.proxy.setUDPDelay("", time.Millisecond, time.Millisecond) })
	loop(func(int) { h.proxy.setUDPDelay(a.Addr(), 0, 0) })
	loop(func(int) { h.proxy.nextUpstream() })
	loop(func(int) { _, _ = json.Marshal(h.proxy.tracker()) })
	loop(func(int) { h.proxy.initTracker() })
	const clients = 6
	var wg sync.WaitGroup
	for i := 0; i < clients; i++ {
		c := h.client()
		wg.Add(1)
		go func() {
			defer wg.Done()
			buff := make([]byte, 10)
			for round := 0; round < 20; round++ {
				c.SetDeadline(time.Now().Add(300 * time.Millisecond))
				c.Write([]byte("q"))
				// packets sent to the upstream that gets stopped and started don't always make it
				c.Read(buff)
			}
		}()
	}
	waitGroup(t, &wg, "clients")
	close(stop)
	waitGroup(t, &admin, "admin activity")
	if a.received.Load() == 0 {
		t.Error("the upstream that was never stopped must have received packets")
	}
}

func TestProxyIgnoresPacketsOfUnreadableClients(t *testing.T) {
	// A proxy that is asked to read from a conn that fails with something else than a close keeps going
	srv := startUDPServer(t, "")
	setup()
	conn := &flakyConn{}
	conn.UDPConn, _ = net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	p := newUDPProxy(1)
	if err := p.startUpstream("n", srv.Addr(), conn, 0, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		conn.Close()
		waitFor(t, "the proxy to stop", func() bool { return !p.isServing(conn) })
	})
	c, err := net.DialUDP("udp", nil, conn.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if got := ask(t, c, []byte("q")); string(got) != "q" {
		t.Errorf("answer = %q", got)
	}
	if conn.failures.Load() == 0 {
		t.Error("the conn was meant to fail first")
	}
}

// flakyConn fails its first read with an error that doesn't mean that it's closed
type flakyConn struct {
	*net.UDPConn
	failures atomic.Int32
}

func (c *flakyConn) ReadFrom(b []byte) (int, net.Addr, error) {
	if c.failures.CompareAndSwap(0, 1) {
		return 0, nil, errors.New("flaky")
	}
	return c.UDPConn.ReadFrom(b)
}
