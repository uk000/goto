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
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"fmt"
	"goto/pkg/global"
	"goto/pkg/types"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"sort"
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
	// Proxies are kept per port, so every test that needs a port of its own takes the next one.
	portSeq atomic.Int32
)

func setup() {
	setupOnce.Do(func() {
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

// shortenTimeouts makes the proxy give up on silent connections and clients quickly. The timeouts are read by the
// goroutines of the proxy, so they must only be changed while no connection is being proxied.
func shortenTimeouts(t *testing.T, idle, sniRead time.Duration) {
	t.Helper()
	oldIdle, oldSNI := idleTimeout, sniReadTimeout
	idleTimeout, sniReadTimeout = idle, sniRead
	t.Cleanup(func() { idleTimeout, sniReadTimeout = oldIdle, oldSNI })
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

// upstreamServer is a loopback TCP server that runs a handler for each connection it accepts.
type upstreamServer struct {
	ln       net.Listener
	wg       sync.WaitGroup
	mu       sync.Mutex
	conns    []net.Conn
	accepted atomic.Int32
}

func startServer(t *testing.T, handler func(net.Conn)) *upstreamServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return serveOn(t, ln, handler)
}

func serveOn(t *testing.T, ln net.Listener, handler func(net.Conn)) *upstreamServer {
	s := &upstreamServer{ln: ln}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.accepted.Add(1)
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
		waitGroup(t, &s.wg, "upstream server to stop")
	})
	return s
}

func (s *upstreamServer) Addr() string { return s.ln.Addr().String() }

func echoHandler(c net.Conn) { io.Copy(c, c) }

func startEchoServer(t *testing.T) *upstreamServer {
	t.Helper()
	return startServer(t, echoHandler)
}

// recorder collects what a server receives.
type recorder struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (r *recorder) write(b []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data.Write(b)
}

func (r *recorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.data.String()
}

// recordingHandler records what it receives, and answers every chunk with the given prefix and the chunk.
func recordingHandler(r *recorder, prefix string) func(net.Conn) {
	return func(c net.Conn) {
		buff := make([]byte, 4096)
		for {
			n, err := c.Read(buff)
			if n > 0 {
				r.write(buff[:n])
				c.Write(append([]byte(prefix), buff[:n]...))
			}
			if err != nil {
				return
			}
		}
	}
}

// closedAddress returns an address that nothing listens on.
func closedAddress(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// proxyHarness serves a loopback listener the way the server does: it hands every accepted connection to the proxy of
// the port. The port of the harness is the real port of the listener, so it's one that no other test uses.
type proxyHarness struct {
	t     *testing.T
	ln    net.Listener
	port  int
	proxy *TCPProxy
	wg    sync.WaitGroup
}

func newProxyHarness(t *testing.T) *proxyHarness {
	t.Helper()
	setup()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h := &proxyHarness{t: t, ln: ln, port: ln.Addr().(*net.TCPAddr).Port}
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
			go func() {
				defer h.wg.Done()
				ProxyTCPConnection(h.port, c)
			}()
		}
	}()
	// Cleanups run in reverse order. This one is registered first so that it runs last: by then the clients and the
	// upstream servers are gone, and the connections of the proxy are expected to be over.
	t.Cleanup(func() {
		ln.Close()
		<-accepting
		waitGroup(t, &h.wg, "proxied connections to finish")
		proxyLock.Lock()
		delete(portProxy, h.port)
		proxyLock.Unlock()
	})
	return h
}

func (h *proxyHarness) addr() string { return h.ln.Addr().String() }

// route sets a single upstream, with endpoints given by name.
func (h *proxyHarness) route(configure func(*TCPUpstream), endpoints map[string]string) *TCPUpstream {
	h.t.Helper()
	up := &TCPUpstream{Name: "up", Endpoints: map[string]*TCPEndpoint{}}
	for name, address := range endpoints {
		up.Endpoints[name] = &TCPEndpoint{Name: name, Address: address}
	}
	if configure != nil {
		configure(up)
	}
	h.proxy.AddUpstreams(map[string]*TCPUpstream{up.Name: up})
	return up
}

func (h *proxyHarness) routeTo(address string) *TCPUpstream {
	h.t.Helper()
	return h.route(nil, map[string]string{"ep": address})
}

func (h *proxyHarness) dial() *net.TCPConn {
	h.t.Helper()
	c, err := net.DialTimeout("tcp", h.addr(), waitTimeout)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { c.Close() })
	return c.(*net.TCPConn)
}

func readN(t *testing.T, c net.Conn, n int) []byte {
	t.Helper()
	buf := make([]byte, n)
	c.SetReadDeadline(time.Now().Add(waitTimeout))
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatalf("read of %d bytes failed: %v (got %q)", n, err, buf)
	}
	return buf
}

func roundTrip(t *testing.T, c net.Conn, msg string) string {
	t.Helper()
	c.SetWriteDeadline(time.Now().Add(waitTimeout))
	if _, err := c.Write([]byte(msg)); err != nil {
		t.Fatal(err)
	}
	return string(readN(t, c, len(msg)))
}

// expectEOF tells whether the connection gets closed, without any more data, in time.
func expectEOF(t *testing.T, c net.Conn) {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(waitTimeout))
	if n, err := c.Read(make([]byte, 1)); err == nil || !errors.Is(err, io.EOF) && !strings.Contains(err.Error(), "reset") {
		t.Fatalf("expected the connection to be closed, got %d bytes, err %v", n, err)
	}
}

// sessionView is what the tracker of a proxy knows about a proxied connection.
type sessionView struct {
	SNI      string
	DownAddr string
	Down     connTrackerData
	Ups      []connTrackerData
}

func sessionsOf(p *TCPProxy, upstream string) []sessionView {
	p.Tracker.lock.RLock()
	tt := p.Tracker.TargetTrackers[upstream]
	p.Tracker.lock.RUnlock()
	if tt == nil {
		return nil
	}
	tt.lock.RLock()
	sts := []*TCPSessionTracker{}
	for _, st := range tt.TCPSessionTracker {
		sts = append(sts, st)
	}
	tt.lock.RUnlock()
	views := []sessionView{}
	for _, st := range sts {
		st.lock.RLock()
		v := sessionView{SNI: st.SNI, DownAddr: st.DownAddress}
		down := st.DownConnTracker
		ups := append([]*ConnTracker{}, st.UpConnTracker...)
		st.lock.RUnlock()
		v.Down = down.snapshot()
		for _, u := range ups {
			v.Ups = append(v.Ups, u.snapshot())
		}
		views = append(views, v)
	}
	sort.Slice(views, func(i, j int) bool { return views[i].DownAddr < views[j].DownAddr })
	return views
}

// onlySession waits for the one session of the upstream to be over, and returns it.
func onlySession(t *testing.T, p *TCPProxy, upstream string) sessionView {
	t.Helper()
	var views []sessionView
	waitFor(t, "the session to be over", func() bool {
		views = sessionsOf(p, upstream)
		return len(views) == 1 && views[0].Down.Closed && len(views[0].Ups) > 0 && views[0].Ups[0].Closed
	})
	return views[0]
}

func newCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "tcpproxy-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"a.example.com", "b.example.com"},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// startTLSServer starts a TLS server that greets every client with its name, and then echoes.
func startTLSServer(t *testing.T, name string) *upstreamServer {
	t.Helper()
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := tls.NewListener(tcp, &tls.Config{Certificates: []tls.Certificate{newCert(t)}})
	return serveOn(t, ln, func(c net.Conn) {
		c.SetDeadline(time.Now().Add(waitTimeout))
		if _, err := c.Write([]byte(name)); err == nil {
			io.Copy(c, c)
		}
	})
}

// tlsHello dials the address and starts a TLS handshake for the server name, and returns the greeting of the server it
// got through to, or the error of the handshake.
func tlsGreeting(addr, serverName string) (string, error) {
	c, err := net.DialTimeout("tcp", addr, waitTimeout)
	if err != nil {
		return "", err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(waitTimeout))
	tc := tls.Client(c, &tls.Config{ServerName: serverName, InsecureSkipVerify: true})
	if err := tc.Handshake(); err != nil {
		return "", err
	}
	buf := make([]byte, 1)
	if _, err := io.ReadFull(tc, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

func upstreamMap(ups ...*TCPUpstream) map[string]*TCPUpstream {
	m := map[string]*TCPUpstream{}
	for _, up := range ups {
		m[up.Name] = up
	}
	return m
}

func sniUpstream(name, sni, address string) *TCPUpstream {
	return &TCPUpstream{
		Name:      name,
		Match:     &UpstreamMatch{SNI: sni},
		Endpoints: map[string]*TCPEndpoint{"ep": {Name: "ep", Address: address}},
	}
}

// ---------------------------------------------------------------------------------------------------------------
// Config parsing and validation

func TestValidateUpstreams(t *testing.T) {
	ep := func(address string) map[string]*TCPEndpoint {
		return map[string]*TCPEndpoint{"e1": {Address: address}}
	}
	tests := []struct {
		name      string
		upstreams map[string]*TCPUpstream
		wantErr   string
	}{
		{"nil map", nil, ""},
		{"valid", map[string]*TCPUpstream{"a": {Endpoints: ep("localhost:8080")}}, ""},
		{"valid ipv6 endpoint", map[string]*TCPUpstream{"a": {Endpoints: ep("[::1]:8080")}}, ""},
		{"valid sni list", map[string]*TCPUpstream{"a": {Endpoints: ep("h:1"), Match: &UpstreamMatch{SNI: `a\.com, b\.com`}}}, ""},
		{"valid delays", map[string]*TCPUpstream{"a": {Endpoints: ep("h:1"),
			Delay: types.NewDelay(time.Millisecond, 2*time.Millisecond, -1), RetryDelay: types.NewDelay(time.Millisecond, time.Millisecond, 2)}}, ""},
		{"nil upstream", map[string]*TCPUpstream{"a": nil}, "upstream [a] is empty"},
		{"no endpoints", map[string]*TCPUpstream{"a": {}}, "upstream endpoints missing"},
		{"empty endpoints", map[string]*TCPUpstream{"a": {Endpoints: map[string]*TCPEndpoint{}}}, "upstream endpoints missing"},
		{"nil endpoint", map[string]*TCPUpstream{"a": {Endpoints: map[string]*TCPEndpoint{"e1": nil}}}, "target endpoint [e1] missing address/port"},
		{"empty address", map[string]*TCPUpstream{"a": {Endpoints: ep("")}}, "target endpoint [e1] missing address/port"},
		{"address without port", map[string]*TCPUpstream{"a": {Endpoints: ep("localhost")}}, "invalid address [localhost]"},
		{"address with bad port", map[string]*TCPUpstream{"a": {Endpoints: ep("localhost:http")}}, "invalid port in address"},
		{"address with port 0", map[string]*TCPUpstream{"a": {Endpoints: ep("localhost:0")}}, "invalid port in address"},
		{"address with huge port", map[string]*TCPUpstream{"a": {Endpoints: ep("localhost:70000")}}, "invalid port in address"},
		{"negative retries", map[string]*TCPUpstream{"a": {Endpoints: ep("h:1"), Retries: -1}}, "negative retries"},
		{"negative dropPct", map[string]*TCPUpstream{"a": {Endpoints: ep("h:1"), DropPct: -1}}, "invalid dropPct"},
		{"dropPct above 100", map[string]*TCPUpstream{"a": {Endpoints: ep("h:1"), DropPct: 101}}, "invalid dropPct"},
		{"delay without max", map[string]*TCPUpstream{"a": {Endpoints: ep("h:1"), Delay: &types.Delay{Min: &types.Duration{Duration: time.Second}}}}, "delay without min or max"},
		{"retry delay without min", map[string]*TCPUpstream{"a": {Endpoints: ep("h:1"), RetryDelay: &types.Delay{Max: &types.Duration{Duration: time.Second}}}}, "delay without min or max"},
		{"bad sni regexp", map[string]*TCPUpstream{"a": {Endpoints: ep("h:1"), Match: &UpstreamMatch{SNI: "*.example.com"}}}, "invalid SNI match"},
		{"bad sni regexp in a list", map[string]*TCPUpstream{"a": {Endpoints: ep("h:1"), Match: &UpstreamMatch{SNI: "ok.com,[x"}}}, "invalid SNI match"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateUpstreams(tt.upstreams)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateUpstreamsFillsInNames(t *testing.T) {
	ups := map[string]*TCPUpstream{"web": {Endpoints: map[string]*TCPEndpoint{"primary": {Address: "h:1"}}}}
	if err := ValidateUpstreams(ups); err != nil {
		t.Fatal(err)
	}
	if ups["web"].Name != "web" {
		t.Errorf("upstream name = %q, want the key web", ups["web"].Name)
	}
	if ups["web"].Endpoints["primary"].Name != "primary" {
		t.Errorf("endpoint name = %q, want the key primary", ups["web"].Endpoints["primary"].Name)
	}
	ups = map[string]*TCPUpstream{"web": {Name: "custom", Endpoints: map[string]*TCPEndpoint{"p": {Name: "mine", Address: "h:1"}}}}
	if err := ValidateUpstreams(ups); err != nil {
		t.Fatal(err)
	}
	if ups["web"].Name != "custom" || ups["web"].Endpoints["p"].Name != "mine" {
		t.Errorf("names that are set must be kept, got %q and %q", ups["web"].Name, ups["web"].Endpoints["p"].Name)
	}
}

func TestValidateUpstreamsReportsFirstErrorByName(t *testing.T) {
	ups := map[string]*TCPUpstream{"b": nil, "a": {}}
	for i := 0; i < 20; i++ {
		if err := ValidateUpstreams(ups); err == nil || !strings.Contains(err.Error(), "endpoints missing") {
			t.Fatalf("error = %v, want the one of upstream a, which comes first", err)
		}
	}
}

func TestParseUpstreams(t *testing.T) {
	body := `{
	  "web": {
	    "endpoints": {"ep1": {"address": "localhost:8081"}, "ep2": {"address": "localhost:8082"}},
	    "match": {"sni": "a.example.com,b.example.com"},
	    "delay": {"min": "10ms", "max": "20ms", "count": 3},
	    "retries": 2,
	    "retryDelay": {"min": "1ms", "max": "2ms"},
	    "dropPct": 10
	  }
	}`
	ups, err := parseUpstreams(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	up := ups["web"]
	if up == nil {
		t.Fatalf("upstreams = %+v, want web", ups)
	}
	if up.Name != "web" || len(up.Endpoints) != 2 || up.Endpoints["ep2"].Address != "localhost:8082" ||
		up.Retries != 2 || up.DropPct != 10 || up.Match.SNI != "a.example.com,b.example.com" {
		t.Errorf("parsed upstream = %+v", up)
	}
	if up.Delay == nil || up.Delay.Min.Duration != 10*time.Millisecond || up.Delay.Max.Duration != 20*time.Millisecond || up.Delay.Count != 3 {
		t.Errorf("delay = %+v", up.Delay)
	}
	if up.RetryDelay == nil || up.RetryDelay.Max.Duration != 2*time.Millisecond {
		t.Errorf("retryDelay = %+v", up.RetryDelay)
	}
}

func TestParseUpstreamsAcceptsAnyCaseOfSNIKey(t *testing.T) {
	for _, key := range []string{"sni", "SNI"} {
		body := fmt.Sprintf(`{"a": {"endpoints": {"e": {"address": "h:1"}}, "match": {%q: "x.com"}}}`, key)
		ups, err := parseUpstreams(strings.NewReader(body))
		if err != nil || ups["a"].Match == nil || ups["a"].Match.SNI != "x.com" {
			t.Errorf("key %q: upstreams = %+v, err = %v", key, ups, err)
		}
	}
}

func TestParseUpstreamsRejectsBadPayloads(t *testing.T) {
	tests := map[string]string{
		"not json":             `{{{`,
		"empty body":           ``,
		"wrong type":           `["a"]`,
		"null upstream":        `{"a": null}`,
		"no endpoints":         `{"a": {}}`,
		"null endpoint":        `{"a": {"endpoints": {"e": null}}}`,
		"missing address":      `{"a": {"endpoints": {"e": {}}}}`,
		"bad address":          `{"a": {"endpoints": {"e": {"address": "nope"}}}}`,
		"bad sni":              `{"a": {"endpoints": {"e": {"address": "h:1"}}, "match": {"sni": "*.x.com"}}}`,
		"bad delay type":       `{"a": {"endpoints": {"e": {"address": "h:1"}}, "delay": {"min": true}}}`,
		"delay with only min":  `{"a": {"endpoints": {"e": {"address": "h:1"}}, "delay": {"min": "1s"}}}`,
		"delay with only max":  `{"a": {"endpoints": {"e": {"address": "h:1"}}, "retryDelay": {"max": "1s"}}}`,
		"negative drop":        `{"a": {"endpoints": {"e": {"address": "h:1"}}, "dropPct": -5}}`,
		"retries as a string":  `{"a": {"endpoints": {"e": {"address": "h:1"}}, "retries": "two"}}`,
		"endpoints as a array": `{"a": {"endpoints": []}}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			ups, err := parseUpstreams(strings.NewReader(body))
			if err == nil || ups != nil {
				t.Fatalf("parseUpstreams = %+v, %v; want an error", ups, err)
			}
		})
	}
}

func TestUpstreamJSON(t *testing.T) {
	up := &TCPUpstream{
		Name:       "web",
		Endpoints:  map[string]*TCPEndpoint{"ep1": {Name: "ep1", Address: "h:1"}},
		Match:      &UpstreamMatch{SNI: "a.com"},
		Delay:      types.NewDelay(time.Second, 2*time.Second, 5),
		Retries:    2,
		RetryDelay: types.NewDelay(time.Millisecond, time.Millisecond, -1),
		DropPct:    10,
	}
	b, err := json.Marshal(map[string]*TCPUpstream{"web": up})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	web := got["web"]
	for _, key := range []string{"name", "endpoints", "match", "delay", "retries", "retryDelay", "dropPct"} {
		if _, ok := web[key]; !ok {
			t.Errorf("json %s lacks key %q", b, key)
		}
	}
	if web["match"].(map[string]any)["sni"] != "a.com" {
		t.Errorf("match = %v, want the sni key", web["match"])
	}
	// What the API reports has to be what the API takes
	ups, err := parseUpstreams(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("the marshaled upstream does not parse back: %v", err)
	}
	if again := ups["web"]; again.Retries != 2 || again.DropPct != 10 || again.Delay.Count != 5 || again.Match.SNI != "a.com" {
		t.Errorf("round trip changed the upstream: %+v", again)
	}
}

func TestCompileSNIMatch(t *testing.T) {
	re, err := compileSNIMatch(" a.example.com , ,b.example.com,")
	if err != nil {
		t.Fatal(err)
	}
	for sni, want := range map[string]bool{"a.example.com": true, "b.example.com": true, "c.example.com": false, "": false} {
		if got := re.MatchString(sni); got != want {
			t.Errorf("match of %q = %v, want %v", sni, got, want)
		}
	}
	for _, empty := range []string{"", " ", ",", " , "} {
		if re, err := compileSNIMatch(empty); re != nil || err != nil {
			t.Errorf("compileSNIMatch(%q) = %v, %v; want no match at all", empty, re, err)
		}
	}
	if _, err := compileSNIMatch("(unclosed"); err == nil {
		t.Error("an invalid pattern must be an error")
	}
}

func TestAddUpstreams(t *testing.T) {
	setup()
	p := newTCPProxy(1)
	if p.hasAnyTargets() || p.hasSNITargets() {
		t.Fatal("a new proxy has no targets")
	}
	first := &TCPUpstream{Endpoints: map[string]*TCPEndpoint{"e": {Address: "h:1"}}}
	sni := &TCPUpstream{Name: "tls", Match: &UpstreamMatch{SNI: "a.com,b.com"}, Endpoints: map[string]*TCPEndpoint{"e": {Address: "h:2"}}}
	in := map[string]*TCPUpstream{"first": first, "tls": sni, "nothing": nil}
	p.AddUpstreams(in)
	if len(p.Upstreams) != 2 || p.Upstreams["first"] != first || p.Upstreams["tls"] != sni {
		t.Fatalf("upstreams = %+v, want first and tls", p.Upstreams)
	}
	if first.Name != "first" {
		t.Errorf("an upstream without a name takes the key as its name, got %q", first.Name)
	}
	if first.proxyPort != 1 || sni.proxyPort != 1 {
		t.Errorf("proxy ports = %d, %d, want 1", first.proxyPort, sni.proxyPort)
	}
	if sni.Match.sniRegexp == nil || !sni.Match.sniRegexp.MatchString("b.com") {
		t.Errorf("the sni match was not compiled: %+v", sni.Match)
	}
	if !p.hasAnyTargets() || !p.hasSNITargets() {
		t.Error("the proxy must now have targets, with SNI ones")
	}
	// The proxy owns its upstreams map
	delete(in, "first")
	if len(p.Upstreams) != 2 {
		t.Error("the proxy must not share the map of the caller")
	}
	// A new set replaces the old one
	p.AddUpstreams(map[string]*TCPUpstream{"second": {Endpoints: map[string]*TCPEndpoint{"e": {Address: "h:3"}}}})
	if len(p.Upstreams) != 1 || p.Upstreams["second"] == nil || p.hasSNITargets() {
		t.Errorf("upstreams = %+v, want only second, with no SNI targets", p.Upstreams)
	}
	// Clearing leaves a usable map behind
	p.AddUpstreams(nil)
	if p.hasAnyTargets() {
		t.Error("no targets expected")
	}
	p.addNewUpstream("default", "h:4")
	if len(p.Upstreams) != 1 {
		t.Errorf("upstreams = %+v, want the one that was added", p.Upstreams)
	}
}

func TestAddUpstreamsIgnoresInvalidSNIMatch(t *testing.T) {
	p := newTCPProxy(1)
	bad := &TCPUpstream{Name: "bad", Match: &UpstreamMatch{SNI: "*.example.com"}, Endpoints: map[string]*TCPEndpoint{"e": {Address: "h:1"}}}
	good := &TCPUpstream{Name: "good", Match: &UpstreamMatch{SNI: "a.example.com"}, Endpoints: map[string]*TCPEndpoint{"e": {Address: "h:2"}}}
	p.AddUpstreams(map[string]*TCPUpstream{"bad": bad, "good": good})
	if len(p.Upstreams) != 1 || p.Upstreams["good"] == nil {
		t.Fatalf("upstreams = %+v, want only the good one: a broken match must not turn into a catch-all", p.Upstreams)
	}
}

func TestAddNewUpstream(t *testing.T) {
	p := newTCPProxy(7)
	p.addNewUpstream("default", "h:1")
	p.addNewUpstream("default", "h:1")
	p.addNewUpstream("default", "h:2")
	if len(p.Upstreams) != 2 {
		t.Fatalf("upstreams = %+v, want one per address", p.Upstreams)
	}
	up := p.Upstreams["h:1"]
	if up.Name != "h:1" || up.Endpoints["default"] == nil || up.Endpoints["default"].Address != "h:1" || up.proxyPort != 7 {
		t.Errorf("upstream = %+v", up)
	}
}

// ---------------------------------------------------------------------------------------------------------------
// Target selection

func TestGetMatchingTCPUpstream(t *testing.T) {
	ep := map[string]*TCPEndpoint{"e": {Address: "h:1"}}
	mk := func(name, sni string) *TCPUpstream {
		up := &TCPUpstream{Name: name, Endpoints: ep}
		if sni != "-" {
			up.Match = &UpstreamMatch{SNI: sni}
		}
		return up
	}
	tests := []struct {
		name      string
		upstreams []*TCPUpstream
		sni       string
		want      string
	}{
		{"no upstreams", nil, "", ""},
		{"no upstreams, with sni", nil, "a.com", ""},
		{"opaque proxy picks the first by name", []*TCPUpstream{mk("b", "-"), mk("a", "-"), mk("c", "-")}, "", "a"},
		{"sni match", []*TCPUpstream{mk("a", `a\.com`), mk("b", `b\.com`)}, "b.com", "b"},
		{"sni matches one of a list", []*TCPUpstream{mk("a", `x\.com,y\.com`), mk("b", `z\.com`)}, "y.com", "a"},
		{"sni is a regular expression", []*TCPUpstream{mk("a", `.*\.example\.com`)}, "foo.example.com", "a"},
		{"first of the matching ones by name", []*TCPUpstream{mk("c", "x.com"), mk("a", "x.com"), mk("b", "x.com")}, "x.com", "a"},
		{"no match", []*TCPUpstream{mk("a", `a\.com`)}, "other.com", ""},
		{"an upstream without a match doesn't take clients that name a server", []*TCPUpstream{mk("a", "-"), mk("b", `b\.com`)}, "other.com", ""},
		{"a client that names no server doesn't get an upstream that wants one", []*TCPUpstream{mk("a", `a\.com`)}, "", ""},
		{"a client that names no server goes to the upstream without a match", []*TCPUpstream{mk("a", `a\.com`), mk("z", "-")}, "", "z"},
		{"empty sni match is no match", []*TCPUpstream{mk("a", ""), mk("b", `b\.com`)}, "", "a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTCPProxy(1)
			p.AddUpstreams(upstreamMap(tt.upstreams...))
			// The upstreams are kept in a map: the choice must not depend on how it gets walked
			for i := 0; i < 30; i++ {
				got := p.getMatchingTCPUpstream(tt.sni)
				switch {
				case tt.want == "" && got != nil:
					t.Fatalf("upstream = %q, want none", got.Name)
				case tt.want != "" && (got == nil || got.Name != tt.want):
					t.Fatalf("upstream = %+v, want %q", got, tt.want)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------------------------------------------
// Proxy registry

func TestPortProxyRegistry(t *testing.T) {
	setup()
	port := int(50000 + portSeq.Add(1))
	t.Cleanup(func() {
		proxyLock.Lock()
		delete(portProxy, port)
		proxyLock.Unlock()
	})
	p := GetPortProxy(port)
	if p == nil || p.Port != port || !p.Enabled || p.Tracker == nil || p.Tracker.ProxyPort != port {
		t.Fatalf("proxy = %+v", p)
	}
	if GetPortProxy(port) != p {
		t.Error("the proxy of a port must be one and the same")
	}
	p.addNewUpstream("default", "h:1")
	ClearPortProxy(port)
	again := GetPortProxy(port)
	if again == p || again.hasAnyTargets() || again.Tracker == p.Tracker || again.Port != port {
		t.Errorf("clearing a port must start from scratch: %+v", again)
	}
	other := GetPortProxy(port + 100000)
	t.Cleanup(func() {
		proxyLock.Lock()
		delete(portProxy, port+100000)
		proxyLock.Unlock()
	})
	if other == again || other.Port != port+100000 {
		t.Error("ports must not share proxies")
	}
}

func TestClearAllProxies(t *testing.T) {
	setup()
	port := int(50000 + portSeq.Add(1))
	p := GetPortProxy(port)
	p.addNewUpstream("default", "h:1")
	ClearAllProxies()
	t.Cleanup(ClearAllProxies)
	if len(snapshotProxies()) != 0 {
		t.Error("no proxies expected after clearing")
	}
	if WillProxyTCP(port) {
		t.Error("a cleared port must not be proxied")
	}
	if GetPortProxy(port) == p {
		t.Error("a new proxy is expected after clearing")
	}
}

func TestWillProxyTCP(t *testing.T) {
	setup()
	port := int(50000 + portSeq.Add(1))
	t.Cleanup(func() {
		proxyLock.Lock()
		delete(portProxy, port)
		proxyLock.Unlock()
	})
	if WillProxyTCP(port) {
		t.Fatal("a port that was never set up is not proxied")
	}
	if _, found := snapshotProxies()[port]; found {
		t.Error("asking must not register a proxy for the port")
	}
	p := GetPortProxy(port)
	if WillProxyTCP(port) {
		t.Error("a proxy without upstreams proxies nothing")
	}
	p.addNewUpstream("default", "h:1")
	if !WillProxyTCP(port) {
		t.Error("a proxy with an upstream is expected to proxy")
	}
	p.lock.Lock()
	p.Enabled = false
	p.lock.Unlock()
	if WillProxyTCP(port) {
		t.Error("a disabled proxy proxies nothing")
	}
}

func TestGetPortProxyConcurrently(t *testing.T) {
	setup()
	const ports, callers = 40, 16
	base := int(60000 + portSeq.Add(1)*1000)
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
	waitGroup(t, &wg, "callers")
	for i := 0; i < ports; i++ {
		for j := 1; j < callers; j++ {
			if got[i][j] != got[i][0] {
				t.Fatalf("port %d: callers got different proxies", base+i)
			}
		}
		if registered := GetPortProxy(base + i); registered != got[i][0] {
			t.Fatalf("port %d: callers got a proxy that is not the registered one", base+i)
		}
	}
}

// ---------------------------------------------------------------------------------------------------------------
// Dropping and delaying

func TestShouldDrop(t *testing.T) {
	dropsIn100 := func(pct int) int {
		up := &TCPUpstream{DropPct: pct}
		n := 0
		for i := 0; i < 100; i++ {
			if up.shouldDrop() {
				n++
			}
		}
		return n
	}
	// The proxy skips every 100/pct-th write
	// (which is more than the percentage for the values that don't divide 100: 30 drops every 3rd, 60 drops all)
	for pct, want := range map[int]int{-5: 0, 0: 0, 1: 1, 5: 5, 10: 10, 20: 20, 25: 25, 30: 33, 50: 50, 60: 100, 100: 100, 150: 100} {
		if got := dropsIn100(pct); got != want {
			t.Errorf("dropPct %d dropped %d of 100 writes, want %d", pct, got, want)
		}
	}
	up := &TCPUpstream{DropPct: 25}
	for i := 1; i <= 12; i++ {
		if got, want := up.shouldDrop(), i%4 == 0; got != want {
			t.Errorf("write %d: drop = %v, want %v", i, got, want)
		}
	}
}

func TestShouldDropConcurrently(t *testing.T) {
	up := &TCPUpstream{DropPct: 20}
	var dropped atomic.Int32
	var wg sync.WaitGroup
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if up.shouldDrop() {
					dropped.Add(1)
				}
			}
		}()
	}
	waitGroup(t, &wg, "writers")
	if got := dropped.Load(); got != 200 {
		t.Errorf("dropped %d of 1000 writes, want exactly 200", got)
	}
}

func TestNextDelay(t *testing.T) {
	if d := nextDelay(nil); d != 0 {
		t.Errorf("no delay config: delay = %s", d)
	}
	if d := nextDelay(&types.Delay{}); d != 0 {
		t.Errorf("a delay without min and max: delay = %s", d)
	}
	if d := nextDelay(types.NewDelay(0, 0, -1)); d != 0 {
		t.Errorf("zero delay: %s", d)
	}
	fixed := types.NewDelay(30*time.Millisecond, 30*time.Millisecond, -1)
	for i := 0; i < 5; i++ {
		if d := nextDelay(fixed); d != 30*time.Millisecond {
			t.Fatalf("delay = %s, want 30ms", d)
		}
	}
	if fixed.Count != -1 {
		t.Errorf("an unlimited delay must stay unlimited, count = %d", fixed.Count)
	}
	ranged := types.NewDelay(10*time.Millisecond, 50*time.Millisecond, -1)
	for i := 0; i < 50; i++ {
		if d := nextDelay(ranged); d < 10*time.Millisecond || d > 50*time.Millisecond {
			t.Fatalf("delay = %s, outside of 10ms-50ms", d)
		}
	}
	counted := types.NewDelay(time.Millisecond, time.Millisecond, 3)
	for i := 0; i < 3; i++ {
		if d := nextDelay(counted); d != time.Millisecond {
			t.Fatalf("delay %d = %s, want 1ms", i, d)
		}
	}
	for i := 0; i < 3; i++ {
		if d := nextDelay(counted); d != 0 {
			t.Fatalf("delay %d after the count ran out = %s, want none", i, d)
		}
	}
}

func TestNextDelayConcurrently(t *testing.T) {
	up := &TCPUpstream{Delay: types.NewDelay(time.Millisecond, time.Millisecond, 500)}
	var delayed atomic.Int32
	var wg sync.WaitGroup
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if up.nextDelay() > 0 {
					delayed.Add(1)
				}
			}
		}()
	}
	waitGroup(t, &wg, "callers")
	if got := delayed.Load(); got != 500 {
		t.Errorf("%d writes were delayed, want exactly the 500 that were configured", got)
	}
}

// ---------------------------------------------------------------------------------------------------------------
// Reading SNI

// helloFor produces the client hello that a TLS client sends for the server name.
func helloFor(t *testing.T, serverName string) []byte {
	t.Helper()
	client, server := net.Pipe()
	defer server.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer client.Close()
		tls.Client(client, &tls.Config{ServerName: serverName, InsecureSkipVerify: true}).Handshake()
	}()
	server.SetReadDeadline(time.Now().Add(waitTimeout))
	buf := make([]byte, 16384)
	n, err := server.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	server.Close()
	<-done
	return buf[:n]
}

func TestReadClientSNI(t *testing.T) {
	setup()
	hello := helloFor(t, "svc.example.com")
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	go func() {
		client.Write(hello)
		client.Write([]byte("more"))
	}()
	sni, got, err := readClientSNI(server)
	if err != nil {
		t.Fatal(err)
	}
	if sni != "svc.example.com" {
		t.Errorf("sni = %q", sni)
	}
	if !bytes.Equal(got, hello) {
		t.Errorf("the hello that was read is %d bytes, want the %d that the client sent, and nothing else", len(got), len(hello))
	}
	// What follows the hello stays on the connection
	rest := make([]byte, 4)
	server.SetReadDeadline(time.Now().Add(waitTimeout))
	if _, err := io.ReadFull(server, rest); err != nil || string(rest) != "more" {
		t.Errorf("rest = %q, %v", rest, err)
	}
}

func TestReadClientSNIWithoutServerName(t *testing.T) {
	setup()
	// Clients don't send an SNI for IP addresses
	hello := helloFor(t, "127.0.0.1")
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	go client.Write(hello)
	sni, got, err := readClientSNI(server)
	if sni != "" || !bytes.Equal(got, hello) {
		t.Errorf("sni = %q, hello of %d bytes (want %d), err %v", sni, len(got), len(hello), err)
	}
}

func TestReadClientSNIOfNonTLSClient(t *testing.T) {
	setup()
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	go client.Write([]byte("GET / HTTP/1.1\r\n\r\n"))
	sni, got, err := readClientSNI(server)
	if err == nil || errors.Is(err, errMalformedHello) {
		t.Errorf("err = %v, want the parser's complaint about a handshake that isn't one", err)
	}
	if sni != "" {
		t.Errorf("sni = %q", sni)
	}
	if string(got) != "GET /" {
		t.Errorf("bytes consumed = %q, want the 5 of the record header, which are to be forwarded", got)
	}
}

func TestReadClientSNIOfEmptyHandshakeRecord(t *testing.T) {
	setup()
	// A handshake record of zero length makes the SNI parser of the tls package index an empty buffer
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	go client.Write([]byte{0x16, 0x03, 0x01, 0x00, 0x00})
	_, _, err := readClientSNI(server)
	if !errors.Is(err, errMalformedHello) {
		t.Errorf("err = %v, want a malformed hello", err)
	}
}

func TestReadClientSNIOfTruncatedHello(t *testing.T) {
	setup()
	hello := helloFor(t, "svc.example.com")
	for _, cut := range []int{0, 3, 5, 20, len(hello) / 2, len(hello) - 1} {
		client, server := net.Pipe()
		go func() {
			client.Write(hello[:cut])
			client.Close()
		}()
		sni, _, err := readClientSNI(server)
		server.Close()
		if err == nil || sni != "" {
			t.Errorf("hello cut at %d: sni = %q, err = %v; want an error", cut, sni, err)
		}
	}
}

func TestReadClientSNITimesOut(t *testing.T) {
	setup()
	shortenTimeouts(t, idleTimeout, 100*time.Millisecond)
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	start := time.Now()
	_, _, err := readClientSNI(server)
	if !isTimeout(err) {
		t.Errorf("err = %v, want a timeout", err)
	}
	if time.Since(start) > waitTimeout {
		t.Error("the read took too long")
	}
}

// ---------------------------------------------------------------------------------------------------------------
// Proxying

func TestOpaqueProxyEchoes(t *testing.T) {
	echo := startEchoServer(t)
	h := newProxyHarness(t)
	h.routeTo(echo.Addr())
	c := h.dial()
	for _, msg := range []string{"hello", "second message", strings.Repeat("x", 3000)} {
		if got := roundTrip(t, c, msg); got != msg {
			t.Fatalf("echo of %d bytes = %d bytes", len(msg), len(got))
		}
	}
	c.Close()

	s := onlySession(t, h.proxy, "up")
	total := 5 + 14 + 3000
	if s.Down.TotalBytesRead != total || s.Down.TotalBytesWritten != total {
		t.Errorf("downstream read/wrote %d/%d bytes, want %d each", s.Down.TotalBytesRead, s.Down.TotalBytesWritten, total)
	}
	if len(s.Ups) != 1 {
		t.Fatalf("upstream trackers = %+v, want one", s.Ups)
	}
	u := s.Ups[0]
	if u.TotalBytesRead != total || u.TotalBytesWritten != total {
		t.Errorf("upstream read/wrote %d/%d bytes, want %d each", u.TotalBytesRead, u.TotalBytesWritten, total)
	}
	if s.Down.TotalReads < 3 || s.Down.TotalWrites < 3 || u.TotalReads < 3 || u.TotalWrites < 3 {
		t.Errorf("reads/writes = %d/%d down, %d/%d up, want at least 3 each", s.Down.TotalReads, s.Down.TotalWrites, u.TotalReads, u.TotalWrites)
	}
	if s.Down.StartTime.IsZero() || s.Down.EndTime.IsZero() || s.Down.EndTime.Before(s.Down.StartTime) {
		t.Errorf("downstream start/end = %v/%v", s.Down.StartTime, s.Down.EndTime)
	}
	if u.StartTime.IsZero() || u.EndTime.IsZero() {
		t.Errorf("upstream start/end = %v/%v", u.StartTime, u.EndTime)
	}
	if s.Down.FirstByteInAt.IsZero() || s.Down.FirstByteOutAt.IsZero() || s.Down.LastByteInAt.Before(s.Down.FirstByteInAt) {
		t.Errorf("downstream byte times = %v %v %v", s.Down.FirstByteInAt, s.Down.LastByteInAt, s.Down.FirstByteOutAt)
	}
	if !s.Down.RemoteClosed || s.Down.ReadError || s.Down.WriteError || s.Down.DropCount != 0 || s.Down.DelayCount != 0 {
		t.Errorf("downstream flags = %+v", s.Down)
	}
	if s.Down.ProxyPort != h.port || s.Down.RemoteAddress != c.LocalAddr().String() || s.Down.Upstream != "up" {
		t.Errorf("downstream identity = %d %q %q", s.Down.ProxyPort, s.Down.RemoteAddress, s.Down.Upstream)
	}
	if u.Upstream != echo.Addr() || u.RemoteAddress != echo.Addr() || u.ProxyPort != h.port {
		t.Errorf("upstream identity = %d %q %q", u.ProxyPort, u.Upstream, u.RemoteAddress)
	}
	tr := h.proxy.Tracker
	tr.lock.RLock()
	conns, rejects, targetConns := tr.ConnCount, len(tr.RejectCountsBySNI), tr.TargetTrackers["up"].ConnCount
	tr.lock.RUnlock()
	if conns != 1 || rejects != 0 || targetConns != 1 {
		t.Errorf("proxy conn count %d, rejects %d, target conn count %d; want 1, 0, 1", conns, rejects, targetConns)
	}
}

func TestProxyKeepsBytesIntactInBothDirections(t *testing.T) {
	echo := startEchoServer(t)
	h := newProxyHarness(t)
	h.routeTo(echo.Addr())
	c := h.dial()
	payload := make([]byte, 3<<20)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	writeErr := make(chan error, 1)
	go func() {
		c.SetWriteDeadline(time.Now().Add(4 * waitTimeout))
		_, err := c.Write(payload)
		if err == nil {
			err = c.CloseWrite()
		}
		writeErr <- err
	}()
	c.SetReadDeadline(time.Now().Add(4 * waitTimeout))
	got, err := io.ReadAll(c)
	if err != nil {
		t.Fatalf("read failed after %d bytes: %v", len(got), err)
	}
	if err := <-writeErr; err != nil {
		t.Fatal(err)
	}
	if len(got) != len(payload) || sha256.Sum256(got) != sha256.Sum256(payload) {
		t.Fatalf("got %d bytes back, want the %d sent, unchanged", len(got), len(payload))
	}
	s := onlySession(t, h.proxy, "up")
	if s.Down.TotalBytesRead != len(payload) || s.Down.TotalBytesWritten != len(payload) ||
		s.Ups[0].TotalBytesWritten != len(payload) || s.Ups[0].TotalBytesRead != len(payload) {
		t.Errorf("bytes tracked: down %d/%d, up %d/%d; want %d each", s.Down.TotalBytesRead, s.Down.TotalBytesWritten,
			s.Ups[0].TotalBytesRead, s.Ups[0].TotalBytesWritten, len(payload))
	}
}

func TestProxyHalfClosedClientStillGetsTheAnswer(t *testing.T) {
	echo := startEchoServer(t)
	h := newProxyHarness(t)
	h.routeTo(echo.Addr())
	c := h.dial()
	if _, err := c.Write([]byte("request")); err != nil {
		t.Fatal(err)
	}
	if err := c.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	c.SetReadDeadline(time.Now().Add(waitTimeout))
	got, err := io.ReadAll(c)
	if err != nil || string(got) != "request" {
		t.Fatalf("answer = %q, %v; want the echo of the request, then the end of the stream", got, err)
	}
	s := onlySession(t, h.proxy, "up")
	if !s.Down.RemoteClosed || !s.Ups[0].RemoteClosed {
		t.Errorf("remote closed flags = down %v, up %v, want both", s.Down.RemoteClosed, s.Ups[0].RemoteClosed)
	}
}

func TestProxyEndsWhenUpstreamCloses(t *testing.T) {
	srv := startServer(t, func(c net.Conn) {
		c.Write([]byte("bye"))
	})
	h := newProxyHarness(t)
	h.routeTo(srv.Addr())
	c := h.dial()
	c.SetReadDeadline(time.Now().Add(waitTimeout))
	got, err := io.ReadAll(c)
	if err != nil || string(got) != "bye" {
		t.Fatalf("got %q, %v; want the farewell and then the end of the stream", got, err)
	}
	s := onlySession(t, h.proxy, "up")
	if !s.Ups[0].RemoteClosed || s.Down.TotalBytesWritten != 3 {
		t.Errorf("upstream remoteClosed = %v, bytes written to downstream = %d", s.Ups[0].RemoteClosed, s.Down.TotalBytesWritten)
	}
}

func TestProxyClosesClientWhenUpstreamIsDown(t *testing.T) {
	h := newProxyHarness(t)
	h.routeTo(closedAddress(t))
	c := h.dial()
	expectEOF(t, c)
	waitGroup(t, &h.wg, "the proxied connection")
	views := sessionsOf(h.proxy, "up")
	if len(views) != 1 || !views[0].Down.Closed || len(views[0].Ups) != 1 {
		t.Fatalf("sessions = %+v", views)
	}
	if u := views[0].Ups[0]; !u.ConnectFailed || !u.Closed || u.EndTime.IsZero() || u.TotalBytesWritten != 0 {
		t.Errorf("upstream = %+v, want a connection that failed", u)
	}
}

func TestProxyRetriesConnections(t *testing.T) {
	// The upstream comes up after the proxy made its first attempts
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	probe.Close()

	h := newProxyHarness(t)
	h.route(func(up *TCPUpstream) {
		up.Retries = 20
		up.RetryDelay = types.NewDelay(50*time.Millisecond, 50*time.Millisecond, -1)
	}, map[string]string{"ep": addr})
	c := h.dial()
	time.Sleep(120 * time.Millisecond)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("the port of the upstream was taken: %v", err)
	}
	serveOn(t, ln, echoHandler)
	if got := roundTrip(t, c, "late"); got != "late" {
		t.Errorf("echo = %q", got)
	}
}

func TestProxyGivesUpAfterRetries(t *testing.T) {
	h := newProxyHarness(t)
	up := h.route(func(up *TCPUpstream) {
		up.Retries = 2
		up.RetryDelay = types.NewDelay(10*time.Millisecond, 10*time.Millisecond, 1)
	}, map[string]string{"ep": closedAddress(t)})
	c := h.dial()
	expectEOF(t, c)
	waitGroup(t, &h.wg, "the proxied connection")
	if !sessionsOf(h.proxy, "up")[0].Ups[0].ConnectFailed {
		t.Error("the connection failure must be tracked")
	}
	// Each attempt but the first waited, until the delay ran out of count
	if up.RetryDelay.Count != 0 {
		t.Errorf("retry delay count = %d, want it used up by the retries", up.RetryDelay.Count)
	}
}

func TestProxyMirrorsToAllEndpointsAndAnswersFromTheFirst(t *testing.T) {
	var recA, recB recorder
	a := startServer(t, recordingHandler(&recA, "A:"))
	b := startServer(t, recordingHandler(&recB, "B:"))
	h := newProxyHarness(t)
	// The endpoints are taken in the order of their names
	h.route(nil, map[string]string{"z": b.Addr(), "a": a.Addr()})
	c := h.dial()
	for _, msg := range []string{"one", "two"} {
		c.Write([]byte(msg))
		if got := string(readN(t, c, 2+len(msg))); got != "A:"+msg {
			t.Fatalf("answer to %q = %q, want the one of endpoint a", msg, got)
		}
	}
	waitFor(t, "both endpoints to get the data", func() bool {
		return recA.String() == "onetwo" && recB.String() == "onetwo"
	})
	c.Close()
	s := onlySession(t, h.proxy, "up")
	if len(s.Ups) != 2 {
		t.Fatalf("upstream trackers = %d, want 2", len(s.Ups))
	}
	byAddr := map[string]connTrackerData{}
	for _, u := range s.Ups {
		byAddr[u.Upstream] = u
	}
	if byAddr[a.Addr()].TotalBytesWritten != 6 || byAddr[b.Addr()].TotalBytesWritten != 6 {
		t.Errorf("bytes written: a %d, b %d, want 6 each", byAddr[a.Addr()].TotalBytesWritten, byAddr[b.Addr()].TotalBytesWritten)
	}
	if byAddr[b.Addr()].TotalBytesRead == 0 {
		t.Error("what the secondary endpoint answers is read, and discarded")
	}
	// The client got the answers of a, and none of b
	if s.Down.TotalBytesWritten != byAddr[a.Addr()].TotalBytesRead {
		t.Errorf("downstream was written %d bytes, but endpoint a sent %d", s.Down.TotalBytesWritten, byAddr[a.Addr()].TotalBytesRead)
	}
}

func TestProxyAnswersFromTheFirstEndpointThatConnects(t *testing.T) {
	var rec recorder
	b := startServer(t, recordingHandler(&rec, "B:"))
	h := newProxyHarness(t)
	h.route(nil, map[string]string{"a": closedAddress(t), "b": b.Addr()})
	c := h.dial()
	if got := roundTrip(t, c, "hi"); got != "B:" {
		t.Fatalf("answer = %q, want the one of the only endpoint that is up", got)
	}
	c.Close()
	s := onlySession(t, h.proxy, "up")
	if len(s.Ups) != 2 || !s.Ups[0].ConnectFailed || s.Ups[1].ConnectFailed {
		t.Errorf("upstream trackers = %+v, want a failed one and a good one", s.Ups)
	}
}

func TestProxyIdleConnectionIsClosed(t *testing.T) {
	echo := startEchoServer(t)
	shortenTimeouts(t, 200*time.Millisecond, sniReadTimeout)
	h := newProxyHarness(t)
	h.routeTo(echo.Addr())
	c := h.dial()
	if got := roundTrip(t, c, "ping"); got != "ping" {
		t.Fatalf("echo = %q", got)
	}
	start := time.Now()
	expectEOF(t, c)
	if took := time.Since(start); took < 150*time.Millisecond || took > 3*time.Second {
		t.Errorf("an idle connection was closed after %s, want about the idle timeout", took)
	}
	s := onlySession(t, h.proxy, "up")
	if !s.Down.ReadError && !s.Ups[0].ReadError {
		t.Errorf("a timed out read must be tracked: down %+v, up %+v", s.Down, s.Ups[0])
	}
}

func TestProxyConnectionIsAliveWhileTheOtherDirectionIsActive(t *testing.T) {
	// The server streams for several times the idle timeout while the client says nothing
	srv := startServer(t, func(c net.Conn) {
		for i := 0; i < 8; i++ {
			if _, err := c.Write([]byte{byte('a' + i)}); err != nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
	shortenTimeouts(t, 250*time.Millisecond, sniReadTimeout)
	h := newProxyHarness(t)
	h.routeTo(srv.Addr())
	c := h.dial()
	c.SetReadDeadline(time.Now().Add(waitTimeout))
	got, err := io.ReadAll(c)
	if err != nil || string(got) != "abcdefgh" {
		t.Fatalf("got %q, %v; want the whole stream", got, err)
	}
}

func TestProxyDelaysWrites(t *testing.T) {
	echo := startEchoServer(t)
	h := newProxyHarness(t)
	h.route(func(up *TCPUpstream) {
		up.Delay = types.NewDelay(150*time.Millisecond, 150*time.Millisecond, -1)
	}, map[string]string{"ep": echo.Addr()})
	c := h.dial()
	start := time.Now()
	if got := roundTrip(t, c, "slow"); got != "slow" {
		t.Fatalf("echo = %q", got)
	}
	// The write to the upstream, and the write of the answer to the downstream, are both delayed
	if took := time.Since(start); took < 300*time.Millisecond {
		t.Errorf("the round trip took %s, want it delayed twice by 150ms", took)
	}
	c.Close()
	s := onlySession(t, h.proxy, "up")
	if s.Down.DelayCount != 1 || s.Ups[0].DelayCount != 1 {
		t.Errorf("delay counts: down %d, up %d, want 1 each", s.Down.DelayCount, s.Ups[0].DelayCount)
	}
}

func TestProxyDelayCountRunsOut(t *testing.T) {
	echo := startEchoServer(t)
	h := newProxyHarness(t)
	up := h.route(func(up *TCPUpstream) {
		up.Delay = types.NewDelay(20*time.Millisecond, 20*time.Millisecond, 2)
	}, map[string]string{"ep": echo.Addr()})
	c := h.dial()
	for i := 0; i < 4; i++ {
		roundTrip(t, c, "x")
	}
	c.Close()
	s := onlySession(t, h.proxy, "up")
	if got := s.Down.DelayCount + s.Ups[0].DelayCount; got != 2 {
		t.Errorf("%d writes were delayed, want the 2 that were configured", got)
	}
	if up.Delay.Count != 0 {
		t.Errorf("delay count = %d", up.Delay.Count)
	}
}

func TestProxyDropsEveryWriteAtFullDropPct(t *testing.T) {
	var rec recorder
	srv := startServer(t, recordingHandler(&rec, ""))
	h := newProxyHarness(t)
	h.route(func(up *TCPUpstream) { up.DropPct = 100 }, map[string]string{"ep": srv.Addr()})
	c := h.dial()
	c.Write([]byte("lost"))
	c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if n, err := c.Read(make([]byte, 10)); err == nil || n != 0 {
		t.Fatalf("read %d bytes, %v; want nothing to come back", n, err)
	}
	c.Close()
	s := onlySession(t, h.proxy, "up")
	if s.Ups[0].DropCount != 1 || s.Ups[0].TotalBytesWritten != 0 || rec.String() != "" {
		t.Errorf("up drops %d, up bytes written %d, upstream got %q; want the write dropped", s.Ups[0].DropCount, s.Ups[0].TotalBytesWritten, rec.String())
	}
}

func TestProxyDropsInBothDirections(t *testing.T) {
	var rec recorder
	srv := startServer(t, recordingHandler(&rec, ""))
	h := newProxyHarness(t)
	// Every 2nd write is skipped, counting the writes of both directions: the request goes through, its answer doesn't
	h.route(func(up *TCPUpstream) { up.DropPct = 50 }, map[string]string{"ep": srv.Addr()})
	c := h.dial()
	c.Write([]byte("req"))
	waitFor(t, "the request to reach the upstream", func() bool { return rec.String() == "req" })
	c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if n, err := c.Read(make([]byte, 10)); err == nil || n != 0 {
		t.Fatalf("read %d bytes, %v; want the answer to be dropped", n, err)
	}
	c.Close()
	s := onlySession(t, h.proxy, "up")
	if s.Down.DropCount != 1 || s.Ups[0].DropCount != 0 || s.Down.TotalBytesWritten != 0 {
		t.Errorf("down drops %d, up drops %d, bytes written to downstream %d", s.Down.DropCount, s.Ups[0].DropCount, s.Down.TotalBytesWritten)
	}
}

func TestProxyRejectsClientsWithoutUpstream(t *testing.T) {
	h := newProxyHarness(t)
	c := h.dial()
	expectEOF(t, c)
	waitGroup(t, &h.wg, "the proxied connection")
	tr := h.proxy.Tracker
	tr.lock.RLock()
	defer tr.lock.RUnlock()
	if tr.ConnCount != 1 || tr.RejectCountsBySNI[""] != 1 || len(tr.TargetTrackers) != 0 {
		t.Errorf("conn count %d, rejects %v, targets %d; want one rejected connection", tr.ConnCount, tr.RejectCountsBySNI, len(tr.TargetTrackers))
	}
}

func TestProxyUpstreamWithoutEndpointsClosesClient(t *testing.T) {
	h := newProxyHarness(t)
	// An upstream that wasn't validated may have nothing to connect to
	h.proxy.AddUpstreams(map[string]*TCPUpstream{"up": {Name: "up"}})
	c := h.dial()
	expectEOF(t, c)
	waitGroup(t, &h.wg, "the proxied connection")
}

func TestProxyConcurrentClients(t *testing.T) {
	echo := startEchoServer(t)
	h := newProxyHarness(t)
	h.routeTo(echo.Addr())
	const clients = 24
	errs := make(chan error, clients)
	var wg sync.WaitGroup
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := net.DialTimeout("tcp", h.addr(), waitTimeout)
			if err != nil {
				errs <- err
				return
			}
			defer c.Close()
			c.SetDeadline(time.Now().Add(waitTimeout))
			for round := 0; round < 5; round++ {
				msg := bytes.Repeat([]byte{byte('A' + i)}, 100+i+round)
				if _, err := c.Write(msg); err != nil {
					errs <- err
					return
				}
				got := make([]byte, len(msg))
				if _, err := io.ReadFull(c, got); err != nil {
					errs <- err
					return
				}
				if !bytes.Equal(got, msg) {
					errs <- fmt.Errorf("client %d round %d: got someone else's bytes", i, round)
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
	waitFor(t, "all the sessions to be over", func() bool {
		views := sessionsOf(h.proxy, "up")
		if len(views) != clients {
			return false
		}
		for _, v := range views {
			if !v.Down.Closed {
				return false
			}
		}
		return true
	})
	tr := h.proxy.Tracker
	tr.lock.RLock()
	defer tr.lock.RUnlock()
	if tr.ConnCount != clients || tr.TargetTrackers["up"].ConnCount != clients {
		t.Errorf("conn counts = %d, %d; want %d", tr.ConnCount, tr.TargetTrackers["up"].ConnCount, clients)
	}
}

// A client that connects again from the very address it used before (a reused port) is a new session: nothing of the
// previous one is carried over.
func TestProxySessionOfReconnectingClient(t *testing.T) {
	echo := startEchoServer(t)
	h := newProxyHarness(t)
	h.routeTo(echo.Addr())
	for i := 1; i <= 3; i++ {
		down, client := net.Pipe()
		conn := &fixedAddrConn{Conn: down, addr: &net.TCPAddr{IP: net.IPv4(10, 1, 1, 1), Port: 4444}}
		done := make(chan struct{})
		go func() {
			defer close(done)
			ProxyTCPConnection(h.port, conn)
		}()
		msg := strings.Repeat("m", i)
		client.SetDeadline(time.Now().Add(waitTimeout))
		if _, err := client.Write([]byte(msg)); err != nil {
			t.Fatal(err)
		}
		if got := readN(t, client, i); string(got) != msg {
			t.Fatalf("round %d: echo = %q", i, got)
		}
		client.Close()
		select {
		case <-done:
		case <-time.After(waitTimeout):
			t.Fatalf("round %d: the proxy did not finish", i)
		}
		views := sessionsOf(h.proxy, "up")
		if len(views) != 1 || len(views[0].Ups) != 1 {
			t.Fatalf("round %d: sessions = %+v, want one session with one upstream connection", i, views)
		}
		if got := views[0].Down.TotalBytesRead; got != i {
			t.Fatalf("round %d: downstream bytes read = %d, want %d, nothing left from the earlier connections", i, got, i)
		}
	}
	h.proxy.Tracker.lock.RLock()
	defer h.proxy.Tracker.lock.RUnlock()
	if h.proxy.Tracker.ConnCount != 3 || h.proxy.Tracker.TargetTrackers["up"].ConnCount != 3 {
		t.Errorf("conn counts = %d, %d; want 3", h.proxy.Tracker.ConnCount, h.proxy.Tracker.TargetTrackers["up"].ConnCount)
	}
}

type fixedAddrConn struct {
	net.Conn
	addr net.Addr
}

func (c *fixedAddrConn) RemoteAddr() net.Addr { return c.addr }

func TestProxyConnectionClosedOnReturn(t *testing.T) {
	setup()
	ClearPortProxy(55001)
	t.Cleanup(func() {
		proxyLock.Lock()
		delete(portProxy, 55001)
		proxyLock.Unlock()
	})
	down, client := net.Pipe()
	defer client.Close()
	ProxyTCPConnection(55001, &fixedAddrConn{Conn: down, addr: &net.TCPAddr{IP: net.IPv4(10, 1, 1, 1), Port: 1}})
	client.SetReadDeadline(time.Now().Add(waitTimeout))
	if _, err := client.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Errorf("err = %v, want the downstream connection to be closed", err)
	}
}

// A connection that the proxy chokes on must not take the server down with it
func TestProxyContainsPanics(t *testing.T) {
	setup()
	port := int(55200 + portSeq.Add(1))
	ClearPortProxy(port)
	t.Cleanup(func() {
		proxyLock.Lock()
		delete(portProxy, port)
		proxyLock.Unlock()
	})
	GetPortProxy(port).addNewUpstream("default", "127.0.0.1:1")
	down, client := net.Pipe()
	defer client.Close()
	// a connection without a remote address makes the proxy trip over
	ProxyTCPConnection(port, &fixedAddrConn{Conn: down})
	client.SetReadDeadline(time.Now().Add(waitTimeout))
	if _, err := client.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Errorf("err = %v, want the connection to be closed", err)
	}
}

// A secondary endpoint that stops taking data doesn't end the session, nor does it hold the others up
func TestProxyCarriesOnWhenSecondaryEndpointFails(t *testing.T) {
	var recA recorder
	a := startServer(t, recordingHandler(&recA, ""))
	b := startServer(t, func(net.Conn) {})
	h := newProxyHarness(t)
	h.route(nil, map[string]string{"a": a.Addr(), "b": b.Addr()})
	c := h.dial()
	chunk := bytes.Repeat([]byte("z"), 64<<10)
	const chunks = 12
	go func() {
		c.SetWriteDeadline(time.Now().Add(2 * waitTimeout))
		for i := 0; i < chunks; i++ {
			if _, err := c.Write(chunk); err != nil {
				return
			}
		}
	}()
	if got := readN(t, c, len(chunk)*chunks); !bytes.Equal(got, bytes.Repeat(chunk, chunks)) {
		t.Fatal("the primary endpoint's answers are not what was sent")
	}
	c.Close()
	s := onlySession(t, h.proxy, "up")
	byAddr := map[string]connTrackerData{}
	for _, u := range s.Ups {
		byAddr[u.Upstream] = u
	}
	if ub := byAddr[b.Addr()]; !ub.WriteError && !ub.RemoteClosed {
		t.Errorf("the failing endpoint must be tracked as such: %+v", ub)
	}
	if ua := byAddr[a.Addr()]; ua.TotalBytesWritten != len(chunk)*chunks || ua.WriteError {
		t.Errorf("the primary endpoint got %d bytes, want %d: %+v", ua.TotalBytesWritten, len(chunk)*chunks, ua)
	}
}

// The primary endpoint that stops taking data ends the session
func TestProxyEndsWhenPrimaryEndpointStopsTakingData(t *testing.T) {
	srv := startServer(t, func(net.Conn) {})
	h := newProxyHarness(t)
	h.routeTo(srv.Addr())
	c := h.dial()
	chunk := bytes.Repeat([]byte("z"), 64<<10)
	c.SetWriteDeadline(time.Now().Add(waitTimeout))
	for i := 0; i < 200; i++ {
		if _, err := c.Write(chunk); err != nil {
			break
		}
	}
	waitGroup(t, &h.wg, "the proxied connection")
	views := sessionsOf(h.proxy, "up")
	if len(views) != 1 || !views[0].Down.Closed || !views[0].Ups[0].Closed {
		t.Fatalf("sessions = %+v", views)
	}
	if u := views[0].Ups[0]; !u.RemoteClosed && !u.WriteError {
		t.Errorf("the upstream must be tracked as one that went away: %+v", u)
	}
}

func TestReadBufferSize(t *testing.T) {
	old := global.ServerConfig.MaxMTUSize
	t.Cleanup(func() { global.ServerConfig.MaxMTUSize = old })
	for mtu, want := range map[int]int{-1: minReadBuffer, 0: minReadBuffer, 1500: minReadBuffer, minReadBuffer: minReadBuffer, 9000: 9000, 65536: 65536} {
		global.ServerConfig.MaxMTUSize = mtu
		if got := readBufferSize(); got != want {
			t.Errorf("MTU %d: buffer = %d, want %d", mtu, got, want)
		}
	}
}

func TestSleepCtx(t *testing.T) {
	if !sleepCtx(context.Background(), time.Millisecond) {
		t.Error("an undisturbed sleep has to report that it slept in full")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if sleepCtx(ctx, time.Minute) || time.Since(start) > waitTimeout {
		t.Error("a sleep must end when its context is done")
	}
}

// ---------------------------------------------------------------------------------------------------------------
// SNI routing

func TestProxyRoutesBySNI(t *testing.T) {
	a := startTLSServer(t, "A")
	b := startTLSServer(t, "B")
	h := newProxyHarness(t)
	h.proxy.AddUpstreams(upstreamMap(
		sniUpstream("a", `a\.example\.com`, a.Addr()),
		sniUpstream("b", `b\.example\.com`, b.Addr()),
	))
	for _, tt := range []struct{ sni, want string }{
		{"a.example.com", "A"}, {"b.example.com", "B"}, {"a.example.com", "A"},
	} {
		got, err := tlsGreeting(h.addr(), tt.sni)
		if err != nil || got != tt.want {
			t.Errorf("SNI %s reached %q, %v; want %q", tt.sni, got, err, tt.want)
		}
	}
	for _, sni := range []string{"c.example.com", "127.0.0.1"} {
		if got, err := tlsGreeting(h.addr(), sni); err == nil {
			t.Errorf("SNI %q reached %q, want the connection to be refused", sni, got)
		}
	}
	waitGroup(t, &h.wg, "proxied connections")

	tr := h.proxy.Tracker
	tr.lock.RLock()
	defer tr.lock.RUnlock()
	if tr.ConnCount != 5 {
		t.Errorf("conn count = %d, want 5", tr.ConnCount)
	}
	if !equalCounts(tr.ConnCountsBySNI, map[string]int{"a.example.com": 2, "b.example.com": 1}) {
		t.Errorf("conn counts by SNI = %v", tr.ConnCountsBySNI)
	}
	if !equalCounts(tr.RejectCountsBySNI, map[string]int{"c.example.com": 1, "": 1}) {
		t.Errorf("reject counts by SNI = %v", tr.RejectCountsBySNI)
	}
	if ta := tr.TargetTrackers["a"]; ta == nil || ta.ConnCount != 2 || !equalCounts(ta.ConnCountsBySNI, map[string]int{"a.example.com": 2}) {
		t.Errorf("target a = %+v", ta)
	}
	if tb := tr.TargetTrackers["b"]; tb == nil || tb.ConnCount != 1 {
		t.Errorf("target b = %+v", tb)
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

func TestProxyTracksSNIOfSession(t *testing.T) {
	a := startTLSServer(t, "A")
	h := newProxyHarness(t)
	h.proxy.AddUpstreams(upstreamMap(sniUpstream("a", `a\.example\.com`, a.Addr())))
	if got, err := tlsGreeting(h.addr(), "a.example.com"); err != nil || got != "A" {
		t.Fatalf("greeting = %q, %v", got, err)
	}
	s := onlySession(t, h.proxy, "a")
	if s.SNI != "a.example.com" {
		t.Errorf("session SNI = %q", s.SNI)
	}
	// The hello was read by the proxy, and written to the upstream
	if s.Down.TotalBytesRead == 0 || s.Down.TotalReads == 0 || s.Down.FirstByteInAt.IsZero() {
		t.Errorf("downstream = %+v, want the hello to be accounted for", s.Down)
	}
	if s.Ups[0].TotalBytesWritten == 0 {
		t.Error("the hello must be forwarded to the upstream")
	}
}

func TestProxyRoutesClientsWithoutSNIToUpstreamWithoutMatch(t *testing.T) {
	a := startTLSServer(t, "A")
	fallback := startTLSServer(t, "F")
	h := newProxyHarness(t)
	h.proxy.AddUpstreams(upstreamMap(
		sniUpstream("a", `a\.example\.com`, a.Addr()),
		&TCPUpstream{Name: "zz", Endpoints: map[string]*TCPEndpoint{"ep": {Address: fallback.Addr()}}},
	))
	if got, err := tlsGreeting(h.addr(), "127.0.0.1"); err != nil || got != "F" {
		t.Errorf("a client without SNI reached %q, %v; want the upstream that has no match", got, err)
	}
	if got, err := tlsGreeting(h.addr(), "a.example.com"); err != nil || got != "A" {
		t.Errorf("a client with SNI reached %q, %v", got, err)
	}
	if got, err := tlsGreeting(h.addr(), "other.example.com"); err == nil {
		t.Errorf("a client with an unknown SNI reached %q, want it refused", got)
	}
}

func TestProxyForwardsNonTLSClientsOfSNIPortToUpstreamWithoutMatch(t *testing.T) {
	echo := startEchoServer(t)
	h := newProxyHarness(t)
	h.proxy.AddUpstreams(upstreamMap(
		sniUpstream("a", `a\.example\.com`, closedAddress(t)),
		&TCPUpstream{Name: "zz", Endpoints: map[string]*TCPEndpoint{"ep": {Address: echo.Addr()}}},
	))
	c := h.dial()
	// The first bytes are consumed to look for a TLS handshake, and have to reach the upstream all the same
	if got := roundTrip(t, c, "hello world"); got != "hello world" {
		t.Errorf("echo = %q", got)
	}
}

func TestProxyRefusesNonTLSClientsOfSNIOnlyPort(t *testing.T) {
	h := newProxyHarness(t)
	h.proxy.AddUpstreams(upstreamMap(sniUpstream("a", `a\.example\.com`, closedAddress(t))))
	c := h.dial()
	c.Write([]byte("GET / HTTP/1.1\r\n\r\n"))
	expectEOF(t, c)
	waitGroup(t, &h.wg, "the proxied connection")
	h.proxy.Tracker.lock.RLock()
	defer h.proxy.Tracker.lock.RUnlock()
	if h.proxy.Tracker.RejectCountsBySNI[""] != 1 || len(h.proxy.Tracker.TargetTrackers) != 0 {
		t.Errorf("rejects = %v, targets = %d", h.proxy.Tracker.RejectCountsBySNI, len(h.proxy.Tracker.TargetTrackers))
	}
}

func TestProxyRefusesMalformedHello(t *testing.T) {
	h := newProxyHarness(t)
	h.proxy.AddUpstreams(upstreamMap(sniUpstream("a", `a\.example\.com`, closedAddress(t))))
	c := h.dial()
	c.Write([]byte{0x16, 0x03, 0x01, 0x00, 0x00})
	expectEOF(t, c)
	waitGroup(t, &h.wg, "the proxied connection")
	h.proxy.Tracker.lock.RLock()
	defer h.proxy.Tracker.lock.RUnlock()
	if h.proxy.Tracker.RejectCountsBySNI[""] != 1 || h.proxy.Tracker.ConnCount != 1 {
		t.Errorf("rejects = %v, conn count = %d", h.proxy.Tracker.RejectCountsBySNI, h.proxy.Tracker.ConnCount)
	}
}

func TestProxyGivesUpOnClientsThatSendNoHello(t *testing.T) {
	shortenTimeouts(t, idleTimeout, 150*time.Millisecond)
	h := newProxyHarness(t)
	h.proxy.AddUpstreams(upstreamMap(sniUpstream("a", `a\.example\.com`, closedAddress(t))))
	c := h.dial()
	expectEOF(t, c)
	waitGroup(t, &h.wg, "the proxied connection")
	h.proxy.Tracker.lock.RLock()
	defer h.proxy.Tracker.lock.RUnlock()
	if h.proxy.Tracker.RejectCountsBySNI[""] != 1 {
		t.Errorf("rejects = %v", h.proxy.Tracker.RejectCountsBySNI)
	}
}

// ---------------------------------------------------------------------------------------------------------------
// Concurrency

// Connections, config changes and reports all go on at the same time.
func TestProxyConcurrentTrafficAndAdmin(t *testing.T) {
	echo := startEchoServer(t)
	h := newProxyHarness(t)
	h.routeTo(echo.Addr())
	stop := make(chan struct{})
	var admin sync.WaitGroup
	reports := []func(){
		func() { _, _ = json.Marshal(h.proxy) },
		func() { _, _ = json.Marshal(snapshotProxies()) },
		func() { _ = WillProxyTCP(h.port) },
		func() { h.proxy.getUpstreams() },
		func() { GetPortProxy(h.port).getMatchingTCPUpstream("") },
		func() {
			h.proxy.AddUpstreams(upstreamMap(&TCPUpstream{Name: "up", Endpoints: map[string]*TCPEndpoint{"ep": {Address: echo.Addr()}}}))
		},
	}
	for _, report := range reports {
		admin.Add(1)
		go func() {
			defer admin.Done()
			for {
				select {
				case <-stop:
					return
				default:
					report()
					time.Sleep(200 * time.Microsecond)
				}
			}
		}()
	}
	const clients = 8
	var wg sync.WaitGroup
	errs := make(chan error, clients)
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := net.DialTimeout("tcp", h.addr(), waitTimeout)
			if err != nil {
				errs <- err
				return
			}
			defer c.Close()
			c.SetDeadline(time.Now().Add(waitTimeout))
			for round := 0; round < 20; round++ {
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
	waitGroup(t, &wg, "clients")
	close(stop)
	waitGroup(t, &admin, "admin activity")
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestTLSTerminatedConnectionsAreProxied(t *testing.T) {
	// The server hands a TLS connection, once it did the handshake, to the proxy: half closes and idle reads have to
	// work on those just like on plain ones
	echo := startEchoServer(t)
	setup()
	port := int(55100 + portSeq.Add(1))
	ClearPortProxy(port)
	t.Cleanup(func() {
		proxyLock.Lock()
		delete(portProxy, port)
		proxyLock.Unlock()
	})
	GetPortProxy(port).addNewUpstream("default", echo.Addr())
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tlsLn := tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{newCert(t)}})
	var wg sync.WaitGroup
	accepting := make(chan struct{})
	go func() {
		defer close(accepting)
		for {
			c, err := tlsLn.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				ProxyTCPConnection(port, c)
			}()
		}
	}()
	t.Cleanup(func() {
		tlsLn.Close()
		<-accepting
		waitGroup(t, &wg, "proxied connections")
	})
	raw, err := net.DialTimeout("tcp", ln.Addr().String(), waitTimeout)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	raw.SetDeadline(time.Now().Add(waitTimeout))
	c := tls.Client(raw, &tls.Config{ServerName: "a.example.com", InsecureSkipVerify: true})
	if err := c.Handshake(); err != nil {
		t.Fatal(err)
	}
	if got := roundTrip(t, c, "over tls"); got != "over tls" {
		t.Errorf("echo = %q", got)
	}
	if err := c.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if rest, err := io.ReadAll(c); err != nil || len(rest) != 0 {
		t.Errorf("rest = %q, %v", rest, err)
	}
}
