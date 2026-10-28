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

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeProxy is a CONNECT proxy: it connects to the destination that a client asks for, answers, and relays the bytes.
type fakeProxy struct {
	status  int               // the answer to CONNECT, which is 200 and a tunnel if it is 0
	delay   time.Duration     // before the answer
	stall   bool              // reads the request and never answers
	headers map[string]string // added to the answer

	connects atomic.Int32 // CONNECT requests that it got
	bare     atomic.Int32 // connections that sent no request
	mu       sync.Mutex
	auths    []string // Proxy-Authorization headers that it got
}

// start listens, over TLS if there is a certificate, and gives the address.
func (fp *fakeProxy) start(t *testing.T, cert *tls.Certificate) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if cert != nil {
		ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{*cert}})
	}
	var wg sync.WaitGroup
	t.Cleanup(func() {
		ln.Close()
		wg.Wait()
	})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer conn.Close()
				fp.serve(conn)
			}()
		}
	}()
	return ln.Addr().String()
}

func (fp *fakeProxy) serve(conn net.Conn) {
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	br := bufio.NewReader(conn)
	req, err := http.ReadRequest(br)
	if err != nil {
		fp.bare.Add(1)
		return
	}
	fp.connects.Add(1)
	if a := req.Header.Get("Proxy-Authorization"); a != "" {
		fp.mu.Lock()
		fp.auths = append(fp.auths, a)
		fp.mu.Unlock()
	}
	if fp.stall {
		_, _ = io.Copy(io.Discard, conn)
		return
	}
	time.Sleep(fp.delay)
	var extra strings.Builder
	for k, v := range fp.headers {
		fmt.Fprintf(&extra, "%s: %s\r\n", k, v)
	}
	if fp.status != 0 && fp.status != http.StatusOK {
		fmt.Fprintf(conn, "HTTP/1.1 %d %s\r\n%sContent-Length: 0\r\nConnection: close\r\n\r\n", fp.status, http.StatusText(fp.status), extra.String())
		return
	}
	up, err := net.Dial("tcp", req.Host)
	if err != nil {
		fmt.Fprintf(conn, "HTTP/1.1 502 Bad Gateway\r\n%sContent-Length: 0\r\nConnection: close\r\n\r\n", extra.String())
		return
	}
	defer up.Close()
	fmt.Fprintf(conn, "HTTP/1.1 200 Connection Established\r\n%s\r\n", extra.String())
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(up, br); closeWrite(up); done <- struct{}{} }()
	go func() { _, _ = io.Copy(conn, up); closeWrite(conn); done <- struct{}{} }()
	<-done
	<-done
}

func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
		return
	}
	c.Close()
}

type seen struct {
	mu   sync.Mutex
	reqs []*http.Request
}

func (s *seen) add(r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reqs = append(s.reqs, r.Clone(context.Background()))
}

func (s *seen) all() []*http.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*http.Request{}, s.reqs...)
}

// newTarget is a server that answers with a goto-like header and a body, and records the requests that it gets.
func newTarget(t *testing.T, useTLS bool, handler http.HandlerFunc) (*httptest.Server, *seen) {
	t.Helper()
	s := &seen{}
	if handler == nil {
		handler = func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Goto-Took", "100µs")
			io.WriteString(w, "hello")
		}
	}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.add(r)
		handler(w, r)
	})
	var srv *httptest.Server
	if useTLS {
		srv = httptest.NewTLSServer(h)
	} else {
		srv = httptest.NewServer(h)
	}
	t.Cleanup(srv.Close)
	return srv, s
}

func hostPort(srv *httptest.Server) string { return srv.Listener.Addr().String() }

// testCert is a certificate that the clients of the tests don't trust.
func testCert(t *testing.T) *tls.Certificate {
	t.Helper()
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	return &srv.TLS.Certificates[0]
}

// What the JSON output has, with the durations as the numbers (or the nulls) that they are there.
type jsonCall struct {
	Call           int               `json:"call"`
	DNS            *float64          `json:"dnsMs"`
	TCP            *float64          `json:"tcpMs"`
	ProxyTLS       *float64          `json:"proxyTlsMs"`
	Connect        *float64          `json:"connectMs"`
	TLS            *float64          `json:"tlsMs"`
	TTFB           *float64          `json:"ttfbMs"`
	Body           *float64          `json:"bodyMs"`
	Total          *float64          `json:"totalMs"`
	Status         int               `json:"status"`
	FailedAt       string            `json:"failedAt"`
	Error          string            `json:"error"`
	ConnectHeaders map[string]string `json:"connectAnswerHeaders"`
	Headers        map[string]string `json:"responseHeaders"`
}

type jsonReport struct {
	Proxy   string     `json:"proxy"`
	URL     string     `json:"url"`
	Calls   []jsonCall `json:"calls"`
	Summary map[string]struct {
		Calls int     `json:"calls"`
		Min   float64 `json:"minMs"`
		Avg   float64 `json:"avgMs"`
		Max   float64 `json:"maxMs"`
	} `json:"summary"`
	Hint      string `json:"hint"`
	Addresses *struct {
		Host      string   `json:"host"`
		Resolve   *float64 `json:"resolveMs"`
		Error     string   `json:"error"`
		Addresses []struct {
			IP      string   `json:"ip"`
			Connect *float64 `json:"connectMs"`
			Error   string   `json:"error"`
		} `json:"addresses"`
	} `json:"addresses"`
}

func probe(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	return probeCtx(t, context.Background(), args...)
}

func probeCtx(t *testing.T, ctx context.Context, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(ctx, args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func probeJSON(t *testing.T, args ...string) (int, jsonReport) {
	t.Helper()
	code, out, errOut := probe(t, append(args, "-json")...)
	var rep jsonReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("the output is not JSON: %v\nstdout: %s\nstderr: %s", err, out, errOut)
	}
	return code, rep
}

func has(ds ...*float64) bool {
	for _, d := range ds {
		if d == nil {
			return false
		}
	}
	return true
}

func TestPlainTargetThroughProxy(t *testing.T) {
	target, reqs := newTarget(t, false, nil)
	fp := &fakeProxy{headers: map[string]string{"Goto-Connect-Proxy-Took": "1.5ms"}}
	proxy := fp.start(t, nil)
	code, rep := probeJSON(t, "-proxy", proxy, "-url", "http://"+hostPort(target)+"/echo?x=1", "-n", "3")
	if code != exitOK || len(rep.Calls) != 3 {
		t.Fatalf("exit %d with %d calls, want 0 with 3: %+v", code, len(rep.Calls), rep)
	}
	for _, c := range rep.Calls {
		if c.Status != http.StatusOK || c.Error != "" {
			t.Errorf("call %d: status %d, error %q", c.Call, c.Status, c.Error)
		}
		if c.DNS != nil || c.ProxyTLS != nil || c.TLS != nil {
			t.Errorf("call %d has phases that it can't have: dns %v, proxy-tls %v, tls %v", c.Call, c.DNS, c.ProxyTLS, c.TLS)
		}
		if !has(c.TCP, c.Connect, c.TTFB, c.Body, c.Total) {
			t.Errorf("call %d is missing a phase: %+v", c.Call, c)
		}
		if c.ConnectHeaders["Goto-Connect-Proxy-Took"] != "1.5ms" {
			t.Errorf("call %d: headers of the answer to CONNECT = %v", c.Call, c.ConnectHeaders)
		}
		if c.Headers["Goto-Took"] != "100µs" {
			t.Errorf("call %d: headers of the response = %v", c.Call, c.Headers)
		}
	}
	if fp.connects.Load() != 3 {
		t.Errorf("the proxy got %d CONNECT requests, want one for each call", fp.connects.Load())
	}
	got := reqs.all()
	if len(got) != 3 || got[0].URL.Path != "/echo" || got[0].URL.RawQuery != "x=1" || got[0].Host != hostPort(target) || got[0].Method != http.MethodGet {
		t.Errorf("the target got %d requests, the first one %+v", len(got), got[0])
	}
	if s := rep.Summary["total"]; s.Calls != 3 || s.Min > s.Avg || s.Avg > s.Max {
		t.Errorf("summary of total = %+v", s)
	}
}

func TestTextOutput(t *testing.T) {
	target, _ := newTarget(t, false, nil)
	proxy := (&fakeProxy{headers: map[string]string{"Goto-Connect-Proxy-Took": "1.5ms"}}).start(t, nil)
	code, out, errOut := probe(t, "-proxy", proxy, "-url", hostPort(target), "-n", "2")
	if code != exitOK || errOut != "" {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	for _, want := range []string{"-> http://" + hostPort(target), "proxy-tls", "connect", "CONNECT answer: Goto-Connect-Proxy-Took=1.5ms",
		"response: Goto-Took=100µs", "summary over the calls", "200"} {
		if !strings.Contains(out, want) {
			t.Errorf("the output doesn't have %q:\n%s", want, out)
		}
	}
	rows := 0
	for _, line := range strings.Split(out, "\n") {
		if len(line) > 0 && (line[0] == '1' || line[0] == '2') && strings.Contains(line, "ms") {
			rows++
		}
	}
	if rows != 2 {
		t.Errorf("%d rows for the calls, want 2:\n%s", rows, out)
	}
}

func TestTLSTargetThroughProxy(t *testing.T) {
	target, reqs := newTarget(t, true, nil)
	proxy := (&fakeProxy{}).start(t, nil)
	code, rep := probeJSON(t, "-proxy", proxy, "-url", "https://"+hostPort(target)+"/secure", "-n", "2")
	if code != exitOK {
		t.Fatalf("exit %d: %+v", code, rep.Calls)
	}
	for _, c := range rep.Calls {
		if c.Status != http.StatusOK || !has(c.TLS) || c.ProxyTLS != nil {
			t.Errorf("call %d: status %d, tls %v, proxy-tls %v", c.Call, c.Status, c.TLS, c.ProxyTLS)
		}
	}
	if got := reqs.all(); len(got) != 2 || got[0].TLS == nil || got[0].URL.Path != "/secure" {
		t.Errorf("the target got %d requests, want 2 over TLS", len(got))
	}
}

func TestTLSProxy(t *testing.T) {
	target, _ := newTarget(t, false, nil)
	proxy := (&fakeProxy{}).start(t, testCert(t))
	code, rep := probeJSON(t, "-proxy", "https://"+proxy, "-url", hostPort(target), "-n", "2")
	if code != exitOK {
		t.Fatalf("exit %d: %+v", code, rep.Calls)
	}
	for _, c := range rep.Calls {
		if c.Status != http.StatusOK || !has(c.ProxyTLS) || c.TLS != nil {
			t.Errorf("call %d: status %d, proxy-tls %v, tls %v", c.Call, c.Status, c.ProxyTLS, c.TLS)
		}
	}
	// the certificates are not trusted, and are not looked at unless that is asked for
	code, rep = probeJSON(t, "-proxy", "https://"+proxy, "-url", hostPort(target), "-n", "1", "-verify")
	if code != exitFailed || rep.Calls[0].FailedAt != "proxy-tls" || !strings.Contains(rep.Calls[0].Error, "certificate") {
		t.Errorf("with -verify: exit %d, %+v", code, rep.Calls[0])
	}
}

func TestVerifyTargetCertificate(t *testing.T) {
	target, _ := newTarget(t, true, nil)
	proxy := (&fakeProxy{}).start(t, nil)
	code, rep := probeJSON(t, "-proxy", proxy, "-url", "https://"+hostPort(target), "-n", "1", "-verify")
	if code != exitFailed || rep.Calls[0].FailedAt != "tls" || !strings.Contains(rep.Calls[0].Error, "certificate") {
		t.Errorf("exit %d, %+v", code, rep.Calls[0])
	}
	if !has(rep.Calls[0].Connect, rep.Calls[0].Total) {
		t.Errorf("the phases that were done are not reported: %+v", rep.Calls[0])
	}
}

func TestResponsesOfAnyStatusAndLength(t *testing.T) {
	big := strings.Repeat("0123456789", 20000)
	target, _ := newTarget(t, false, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/503":
			w.WriteHeader(http.StatusServiceUnavailable)
		case "/chunked":
			io.WriteString(w, "ab")
			w.(http.Flusher).Flush()
			io.WriteString(w, big)
		}
	})
	proxy := (&fakeProxy{}).start(t, nil)
	for path, status := range map[string]int{"/503": 503, "/chunked": 200, "/empty": 200} {
		code, rep := probeJSON(t, "-proxy", proxy, "-url", "http://"+hostPort(target)+path, "-n", "1")
		if code != exitOK || rep.Calls[0].Status != status || !has(rep.Calls[0].Body) {
			t.Errorf("%s: exit %d, %+v, want status %d (a response is a response, whatever its status)", path, code, rep.Calls[0], status)
		}
	}
}

func TestProxyRefusesTunnel(t *testing.T) {
	fp := &fakeProxy{status: http.StatusBadGateway, headers: map[string]string{"Goto-Connect-Proxy-Took": "84ms"}}
	proxy := fp.start(t, nil)
	code, rep := probeJSON(t, "-proxy", proxy, "-url", "http://example.invalid:80/", "-n", "2")
	if code != exitFailed || len(rep.Calls) != 2 {
		t.Fatalf("exit %d with %d calls, want 1 with both of them made", code, len(rep.Calls))
	}
	for _, c := range rep.Calls {
		if c.FailedAt != "connect" || !strings.Contains(c.Error, "502") || !has(c.Connect, c.Total) || c.Status != 0 {
			t.Errorf("call %d: %+v", c.Call, c)
		}
		if c.ConnectHeaders["Goto-Connect-Proxy-Took"] != "84ms" {
			t.Errorf("call %d: the headers of the refusal are %v", c.Call, c.ConnectHeaders)
		}
	}
	// the text says where it failed
	_, out, _ := probe(t, "-proxy", proxy, "-url", "http://example.invalid:80/", "-n", "1")
	if !strings.Contains(out, "FAILED at connect") || !strings.Contains(out, "502 Bad Gateway") {
		t.Errorf("the output doesn't say what failed:\n%s", out)
	}
}

func TestProxyThatCantReachTheTarget(t *testing.T) {
	closed := closedAddress(t)
	proxy := (&fakeProxy{}).start(t, nil)
	code, rep := probeJSON(t, "-proxy", proxy, "-url", "http://"+closed+"/", "-n", "1")
	if code != exitFailed || rep.Calls[0].FailedAt != "connect" || !strings.Contains(rep.Calls[0].Error, "502") {
		t.Errorf("exit %d, %+v", code, rep.Calls[0])
	}
}

func TestStalledProxyIsGivenUpOn(t *testing.T) {
	proxy := (&fakeProxy{stall: true}).start(t, nil)
	start := time.Now()
	code, rep := probeJSON(t, "-proxy", proxy, "-url", "http://127.0.0.1:1/", "-n", "1", "-timeout", "300ms")
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("took %s to give up on a proxy that doesn't answer, with a timeout of 300ms", took)
	}
	if code != exitFailed || rep.Calls[0].FailedAt != "connect" || !has(rep.Calls[0].Total) {
		t.Errorf("exit %d, %+v", code, rep.Calls[0])
	}
}

func TestProxyThatCantBeReached(t *testing.T) {
	code, rep := probeJSON(t, "-proxy", closedAddress(t), "-url", "http://127.0.0.1:1/", "-n", "2")
	if code != exitFailed || len(rep.Calls) != 2 || rep.Calls[0].FailedAt != "dial" || rep.Calls[0].Connect != nil {
		t.Errorf("exit %d, %+v", code, rep.Calls)
	}
}

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

func TestProxyCredentials(t *testing.T) {
	target, _ := newTarget(t, false, nil)
	fp := &fakeProxy{}
	proxy := fp.start(t, nil)
	code, out, errOut := probe(t, "-proxy", "http://user:s3cret@"+proxy, "-url", hostPort(target), "-n", "1")
	if code != exitOK {
		t.Fatalf("exit %d: %s %s", code, out, errOut)
	}
	fp.mu.Lock()
	defer fp.mu.Unlock()
	if len(fp.auths) != 1 || fp.auths[0] != "Basic dXNlcjpzM2NyZXQ=" {
		t.Errorf("the proxy got %q, want the credentials as basic authorization", fp.auths)
	}
	if strings.Contains(out+errOut, "s3cret") {
		t.Errorf("the password is in the output:\n%s%s", out, errOut)
	}
}

func TestResolvedNameOfTheProxy(t *testing.T) {
	if addrs, err := net.LookupHost("localhost"); err != nil || !contains(addrs, "127.0.0.1") {
		t.Skipf("localhost doesn't resolve to 127.0.0.1: %v %v", addrs, err)
	}
	target, _ := newTarget(t, false, nil)
	fp := &fakeProxy{}
	proxy := fp.start(t, nil)
	_, port, _ := net.SplitHostPort(proxy)
	code, rep := probeJSON(t, "-proxy", "localhost:"+port, "-url", hostPort(target), "-n", "2", "-check-addrs")
	if code != exitOK {
		t.Fatalf("exit %d: %+v", code, rep.Calls)
	}
	for _, c := range rep.Calls {
		if !has(c.DNS, c.TCP) {
			t.Errorf("call %d: a name of a proxy has a dns phase: %+v", c.Call, c)
		}
	}
	a := rep.Addresses
	if a == nil || a.Host != "localhost" || !has(a.Resolve) {
		t.Fatalf("addresses = %+v", a)
	}
	found := false
	for _, addr := range a.Addresses {
		if addr.IP == "127.0.0.1" {
			found = has(addr.Connect) && addr.Error == ""
		}
	}
	if !found {
		t.Errorf("the connection to 127.0.0.1 is not reported: %+v", a.Addresses)
	}
	// the proxy notices that the connections closed without a request when it gets to them
	for deadline := time.Now().Add(2 * time.Second); fp.bare.Load() < 1 && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	if fp.bare.Load() < 1 {
		t.Errorf("the proxy got %d bare connections, want the ones of the address check", fp.bare.Load())
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestAddressCheckOfAnIPAddress(t *testing.T) {
	target, _ := newTarget(t, false, nil)
	proxy := (&fakeProxy{}).start(t, nil)
	host, _, _ := net.SplitHostPort(proxy)
	_, rep := probeJSON(t, "-proxy", proxy, "-url", hostPort(target), "-n", "1", "-check-addrs")
	if a := rep.Addresses; a == nil || a.Resolve != nil || len(a.Addresses) != 1 || a.Addresses[0].IP != host || !has(a.Addresses[0].Connect) {
		t.Errorf("addresses = %+v, want the one address, and no resolution", a)
	}
	// and there is no check unless it is asked for
	_, rep = probeJSON(t, "-proxy", proxy, "-url", hostPort(target), "-n", "1")
	if rep.Addresses != nil {
		t.Errorf("addresses = %+v without -check-addrs", rep.Addresses)
	}
}

func TestInterruptedBetweenCalls(t *testing.T) {
	target, _ := newTarget(t, false, nil)
	proxy := (&fakeProxy{}).start(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(400 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	code, out, errOut := probeCtx(t, ctx, "-proxy", proxy, "-url", hostPort(target), "-n", "5", "-interval", "30s")
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("took %s to stop, with an interval of 30s", took)
	}
	if code != exitFailed || !strings.Contains(errOut, "interrupted") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
	if !strings.Contains(out, "total") {
		t.Errorf("what was done is not reported:\n%s", out)
	}
}

func TestIntervalBetweenCalls(t *testing.T) {
	target, _ := newTarget(t, false, nil)
	proxy := (&fakeProxy{}).start(t, nil)
	start := time.Now()
	code, rep := probeJSON(t, "-proxy", proxy, "-url", hostPort(target), "-n", "3", "-interval", "100ms")
	if took := time.Since(start); code != exitOK || len(rep.Calls) != 3 || took < 200*time.Millisecond {
		t.Errorf("exit %d, %d calls in %s, want 3 with the interval between them (and not after the last)", code, len(rep.Calls), took)
	}
}

func TestUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"nothing", nil, "-proxy and -url are required"},
		{"no url", []string{"-proxy", "h:1"}, "-proxy and -url are required"},
		{"no proxy", []string{"-url", "h:1"}, "-proxy and -url are required"},
		{"no calls", []string{"-proxy", "h:1", "-url", "h:2", "-n", "0"}, "-n must be"},
		{"too many calls", []string{"-proxy", "h:1", "-url", "h:2", "-n", "100001"}, "-n must be"},
		{"bad timeout", []string{"-proxy", "h:1", "-url", "h:2", "-timeout", "0s"}, "-timeout must be positive"},
		{"bad interval", []string{"-proxy", "h:1", "-url", "h:2", "-interval", "-1s"}, "-interval can't be negative"},
		{"bad scheme", []string{"-proxy", "ftp://h:1", "-url", "h:2"}, "scheme [ftp] of the proxy is not supported"},
		{"bad url", []string{"-proxy", "h:1", "-url", "http://"}, "the url has no host"},
		{"bad port", []string{"-proxy", "h:70000", "-url", "h:2"}, "invalid port [70000] in the proxy"},
		{"extra argument", []string{"-proxy", "h:1", "-url", "h:2", "extra"}, "unexpected argument [extra]"},
		{"unknown flag", []string{"-nope"}, "flag provided but not defined"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, out, errOut := probe(t, tc.args...)
			if code != exitUsage || !strings.Contains(errOut, tc.want) || out != "" {
				t.Errorf("exit %d, stdout %q, stderr %q, want %d and %q", code, out, errOut, exitUsage, tc.want)
			}
		})
	}
	code, _, errOut := probe(t, "-h")
	if code != exitOK || !strings.Contains(errOut, "Usage:") || !strings.Contains(errOut, "-check-addrs") || !strings.Contains(errOut, "Phases") {
		t.Errorf("-h: exit %d, stderr %q", code, errOut)
	}
}

func TestParseEndpoint(t *testing.T) {
	tests := []struct {
		in                              string
		tls                             bool
		host, port, addr, authority, ri string
		display                         string
	}{
		{"proxy.example:9000", false, "proxy.example", "9000", "proxy.example:9000", "proxy.example:9000", "/", "http://proxy.example:9000"},
		{"https://proxy.example", true, "proxy.example", "443", "proxy.example:443", "proxy.example", "/", "https://proxy.example"},
		{"http://proxy.example", false, "proxy.example", "80", "proxy.example:80", "proxy.example", "/", "http://proxy.example"},
		{"http://[::1]:8081/a/b?q=1", false, "::1", "8081", "[::1]:8081", "[::1]:8081", "/a/b?q=1", "http://[::1]:8081/a/b?q=1"},
		{"[::1]:9000", false, "::1", "9000", "[::1]:9000", "[::1]:9000", "/", "http://[::1]:9000"},
		{"  10.0.0.1:80  ", false, "10.0.0.1", "80", "10.0.0.1:80", "10.0.0.1:80", "/", "http://10.0.0.1:80"},
		{"user:pw@host:80", false, "host", "80", "host:80", "host:80", "/", "http://user:xxxxx@host:80"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			ep, err := parseEndpoint(tc.in, "proxy")
			if err != nil {
				t.Fatal(err)
			}
			if ep.tls != tc.tls || ep.host != tc.host || ep.port != tc.port || ep.addr != tc.addr || ep.authority != tc.authority || ep.uri != tc.ri || ep.display != tc.display {
				t.Errorf("got %+v, want tls %v, host %q, port %q, addr %q, authority %q, uri %q, display %q", *ep, tc.tls, tc.host, tc.port, tc.addr, tc.authority, tc.ri, tc.display)
			}
		})
	}
	for _, bad := range []string{"", "  ", "ftp://h:1", "http://", "http://h:0", "http://h:abc", "http://user:pa%zzss@h:1"} {
		_, err := parseEndpoint(bad, "proxy")
		if err == nil {
			t.Errorf("%q was accepted", bad)
		} else if strings.Contains(err.Error(), "user:") {
			t.Errorf("%q: the error has the credentials: %v", bad, err)
		}
	}
}

func ms(n float64) dur { return dur(n * float64(time.Millisecond)) }

func result(call int, dns, tcp, connect, total float64) callResult {
	r := callResult{Call: call, DNS: na, TCP: na, ProxyTLS: na, Connect: na, TLS: na, TTFB: na, Body: na, Total: ms(total)}
	if dns >= 0 {
		r.DNS = ms(dns)
	}
	r.TCP = ms(tcp)
	r.Connect = ms(connect)
	return r
}

func TestSummarize(t *testing.T) {
	if summarize([]callResult{result(1, 1, 1, 1, 3)}) != nil {
		t.Error("a single call has a summary")
	}
	failed := result(3, 500, 500, 500, 500)
	failed.Error = "boom"
	stats := summarize([]callResult{result(1, 2, 1, 10, 13), result(2, -1, 3, 20, 30), failed, result(4, 4, 2, 30, 40)})
	if s := stats["total"]; s.N != 3 || s.Min != ms(13) || s.Max != ms(40) || s.Avg != ms(83)/3 {
		t.Errorf("total = %+v, want the 3 calls that got a response", s)
	}
	if s := stats["dns"]; s.N != 2 || s.Min != ms(2) || s.Max != ms(4) || s.Avg != ms(3) {
		t.Errorf("dns = %+v, want the 2 calls that had the phase", s)
	}
	if _, found := stats["tls"]; found {
		t.Error("a phase that no call had is in the summary")
	}
}

func TestColdStartHint(t *testing.T) {
	first := result(1, 2, 1, 350, 400)
	first.ConnectHeaders = map[string]string{"Goto-Connect-Proxy-Took": "300ms"}
	rest := []callResult{result(2, 2, 1, 2, 10), result(3, 2, 1, 2, 12), result(4, 2, 1, 2, 11)}
	hint := coldStartHint(append([]callResult{first}, rest...))
	for _, want := range []string{"call 1 took 400.00ms", "Most of the extra time is in connect", "+348.00ms", "The proxy says 300.00ms"} {
		if !strings.Contains(hint, want) {
			t.Errorf("the hint doesn't have %q: %s", want, hint)
		}
	}
	// a proxy that tells the lookup and the connecting apart says how much of its time each took
	first.ConnectHeaders = map[string]string{"Goto-Connect-Proxy-Took": "300ms", "Goto-Connect-Proxy-DNS-Took": "280ms", "Goto-Connect-Proxy-Dial-Took": "1ms"}
	if hint := coldStartHint(append([]callResult{first}, rest...)); !strings.Contains(hint, "Of it, the lookup was 280.00ms and the connection was 1.00ms.") {
		t.Errorf("the hint doesn't say how the proxy spent its time: %s", hint)
	}
	// without what the proxy says, it says what it can
	first.ConnectHeaders = nil
	if hint := coldStartHint(append([]callResult{first}, rest...)); !strings.Contains(hint, "in connect") || strings.Contains(hint, "The proxy says") {
		t.Errorf("hint = %s", hint)
	}
	// the extra time in another phase
	slowDNS := result(1, 380, 1, 2, 400)
	if hint := coldStartHint(append([]callResult{slowDNS}, rest...)); !strings.Contains(hint, "in dns") {
		t.Errorf("hint = %s", hint)
	}
	none := map[string][]callResult{
		"too few calls":                     {first, rest[0]},
		"the first one failed":              {func() callResult { f := first; f.Error = "x"; return f }(), rest[0], rest[1]},
		"the first one is as fast":          {result(1, 2, 1, 2, 12), rest[0], rest[1], rest[2]},
		"slower, but by less than a margin": {result(1, 2, 1, 2, 25), result(2, 2, 1, 2, 5), result(3, 2, 1, 2, 5)},
		"only one call to compare with":     {first, rest[0], func() callResult { f := rest[1]; f.Error = "x"; return f }()},
	}
	for name, results := range none {
		if hint := coldStartHint(results); hint != "" {
			t.Errorf("%s: hint = %s", name, hint)
		}
	}
}

func TestDurFormats(t *testing.T) {
	if got := ms(1.5).String(); got != "1.50ms" {
		t.Errorf("String = %q", got)
	}
	if got := na.String(); got != "-" {
		t.Errorf("String of na = %q", got)
	}
	b, _ := json.Marshal(struct{ A, B dur }{ms(1.5), na})
	if string(b) != `{"A":1.500,"B":null}` {
		t.Errorf("JSON = %s", b)
	}
}
