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
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	exitOK     = 0
	exitFailed = 1
	exitUsage  = 2

	// maxCalls bounds -n, so that a typo can't keep the probe going for hours.
	maxCalls = 100000
	// hintMinExtra is how much slower than the later calls the first one has to be for the probe to say so.
	hintMinExtra = 20 * time.Millisecond
)

// probeHeaders are the headers of goto that tell where the time went on the proxy and the server side. They are picked
// out of the answer to CONNECT, and out of the response.
var probeHeaders = []string{"Goto-Took", "Goto-Connect-Proxy-Took", "Goto-Connect-Proxy-DNS-Took", "Goto-Connect-Proxy-Dial-Took", "Goto-Connect-Proxy-Delay"}

// dur is a duration of a phase. A negative one is a phase that the call didn't have (e.g. no DNS for an IP address, no TLS
// for a plain target), or didn't get to.
type dur time.Duration

const na dur = -1

func (d dur) String() string {
	if d < 0 {
		return "-"
	}
	return fmt.Sprintf("%.2fms", float64(d)/float64(time.Millisecond))
}

// MarshalJSON gives milliseconds, and null for a phase that there is none of.
func (d dur) MarshalJSON() ([]byte, error) {
	if d < 0 {
		return []byte("null"), nil
	}
	return []byte(strconv.FormatFloat(float64(d)/float64(time.Millisecond), 'f', 3, 64)), nil
}

// endpoint is a proxy or a target, as given.
type endpoint struct {
	display   string // the URL without its password
	tls       bool
	host      string // without brackets, if it is an IPv6 address
	port      string
	addr      string // host:port
	authority string // what the URL has for the host, which is what a Host header says
	uri       string // the request URI of the URL
	user      *url.Userinfo
}

type config struct {
	proxy      *endpoint
	target     *endpoint
	count      int
	interval   time.Duration
	timeout    time.Duration
	verify     bool
	checkAddrs bool
	json       bool
}

// callResult is what a call found out. The phases are in the order that a call goes through them.
type callResult struct {
	Call           int               `json:"call"`
	DNS            dur               `json:"dnsMs"`
	TCP            dur               `json:"tcpMs"`
	ProxyTLS       dur               `json:"proxyTlsMs"`
	Connect        dur               `json:"connectMs"`
	TLS            dur               `json:"tlsMs"`
	TTFB           dur               `json:"ttfbMs"`
	Body           dur               `json:"bodyMs"`
	Total          dur               `json:"totalMs"`
	Status         int               `json:"status,omitempty"`
	FailedAt       string            `json:"failedAt,omitempty"`
	Error          string            `json:"error,omitempty"`
	ConnectHeaders map[string]string `json:"connectAnswerHeaders,omitempty"`
	Headers        map[string]string `json:"responseHeaders,omitempty"`
}

type phase struct {
	name string
	get  func(*callResult) dur
}

// phases are in the order of a call. The last one is the sum of the others, which isn't one of them.
var phases = []phase{
	{"dns", func(r *callResult) dur { return r.DNS }},
	{"tcp", func(r *callResult) dur { return r.TCP }},
	{"proxy-tls", func(r *callResult) dur { return r.ProxyTLS }},
	{"connect", func(r *callResult) dur { return r.Connect }},
	{"tls", func(r *callResult) dur { return r.TLS }},
	{"ttfb", func(r *callResult) dur { return r.TTFB }},
	{"body", func(r *callResult) dur { return r.Body }},
	{"total", func(r *callResult) dur { return r.Total }},
}

type phaseStat struct {
	N   int `json:"calls"`
	Min dur `json:"minMs"`
	Avg dur `json:"avgMs"`
	Max dur `json:"maxMs"`
}

type addrResult struct {
	IP      string `json:"ip"`
	Connect dur    `json:"connectMs"`
	Error   string `json:"error,omitempty"`
}

type addrReport struct {
	Host    string       `json:"host"`
	Resolve dur          `json:"resolveMs"`
	Error   string       `json:"error,omitempty"`
	Addrs   []addrResult `json:"addresses,omitempty"`
}

type report struct {
	Proxy     string               `json:"proxy"`
	URL       string               `json:"url"`
	Calls     []callResult         `json:"calls"`
	Summary   map[string]phaseStat `json:"summary,omitempty"`
	Hint      string               `json:"hint,omitempty"`
	Addresses *addrReport          `json:"addresses,omitempty"`
}

const usageText = `connectprobe times each phase of a call through an HTTP CONNECT proxy to an http or https target, with the DNS
resolver and dialer of Go (what the goto client uses). Run it where the client runs.

Usage:
  connectprobe -proxy <[http[s]://]host:port> -url <[http[s]://]host[:port][/path]> [options]

Options:
`

const examplesText = `
Phases (ms), in the order of a call:
  dns        resolving the name of the proxy ("-" for an IP address)
  tcp        connecting to the proxy once its name is resolved (includes the wait for the other address family)
  proxy-tls  TLS handshake with the proxy, if the proxy is an https one
  connect    the CONNECT exchange: the proxy connects to the target before it answers
  tls        TLS handshake with the target, if it is an https one (over the tunnel)
  ttfb       from sending the request to getting the head of the response
  body       reading the body of the response
  total      from the start of the call to the end of the body

Examples:
  connectprobe -proxy goto-proxy:9000 -url http://server:8081/echo
  connectprobe -proxy https://goto-proxy:9443 -url https://example.com/ -n 10 -interval 30s
  connectprobe -proxy 10.0.0.5:9000 -url http://server:8081/ -check-addrs -json

Exit status: 0 if all calls got a response, 1 if any call failed, 2 for wrong usage.
`

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	cfg, err := parseArgs(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		fmt.Fprintln(stderr, "connectprobe:", err)
		fmt.Fprintln(stderr, "run it with -h for the usage")
		return exitUsage
	}
	if !cfg.json {
		fmt.Fprintf(stdout, "proxy %s -> %s\n\n", cfg.proxy.display, cfg.target.display)
		fmt.Fprintf(stdout, "%-5s %-10s %-10s %-10s %-10s %-10s %-10s %-10s %-10s %s\n", "call", "dns", "tcp", "proxy-tls",
			"connect", "tls", "ttfb", "body", "total", "status")
	}
	var results []callResult
	failed := false
	interrupted := false
	for i := 1; i <= cfg.count; i++ {
		r := probeCall(ctx, cfg, i)
		results = append(results, r)
		if r.Error != "" {
			failed = true
		}
		if !cfg.json {
			printCall(stdout, &r)
		}
		if ctx.Err() != nil {
			interrupted = true
			break
		}
		if i < cfg.count && cfg.interval > 0 && !sleepCtx(ctx, cfg.interval) {
			interrupted = true
			break
		}
	}
	rep := &report{Proxy: cfg.proxy.display, URL: cfg.target.display, Calls: results, Summary: summarize(results), Hint: coldStartHint(results)}
	if cfg.checkAddrs {
		// after the calls, so that what it resolves doesn't warm the caches that the first call is to find cold
		rep.Addresses = checkAddresses(ctx, cfg)
	}
	if cfg.json {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rep)
	} else {
		printReport(stdout, rep)
	}
	if interrupted {
		fmt.Fprintln(stderr, "connectprobe: interrupted")
		failed = true
	}
	if failed {
		return exitFailed
	}
	return exitOK
}

func parseArgs(args []string, stderr io.Writer) (*config, error) {
	fs := flag.NewFlagSet("connectprobe", flag.ContinueOnError)
	fs.SetOutput(stderr)
	proxy := fs.String("proxy", "", "the proxy, as host:port, or a URL: http://host:port, or https://host:port for a proxy that is reached over TLS (required)")
	target := fs.String("url", "", "the URL to get through the proxy; the scheme is http if there is none (required)")
	count := fs.Int("n", 5, "number of calls, each on a connection of its own, as the goto client makes them")
	interval := fs.Duration("interval", 0, "wait between the calls, e.g. 30s, to see what a cache that expires does")
	timeout := fs.Duration("timeout", 10*time.Second, "limit for a call, and for each connection that the probe opens")
	verify := fs.Bool("verify", false, "verify the certificates of the proxy and the target (the goto client doesn't, by default)")
	checkAddrs := fs.Bool("check-addrs", false, "after the calls, resolve the name of the proxy again and connect to each address on its own. "+
		"The proxy logs these bare connections as clients that sent no request")
	asJSON := fs.Bool("json", false, "print one JSON document instead of the table")
	fs.Usage = func() {
		fmt.Fprint(stderr, usageText)
		fs.PrintDefaults()
		fmt.Fprint(stderr, examplesText)
	}
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected argument [%s]", fs.Arg(0))
	}
	if *proxy == "" || *target == "" {
		return nil, errors.New("-proxy and -url are required")
	}
	if *count < 1 || *count > maxCalls {
		return nil, fmt.Errorf("-n must be between 1 and %d", maxCalls)
	}
	if *timeout <= 0 {
		return nil, errors.New("-timeout must be positive")
	}
	if *interval < 0 {
		return nil, errors.New("-interval can't be negative")
	}
	cfg := &config{count: *count, interval: *interval, timeout: *timeout, verify: *verify, checkAddrs: *checkAddrs, json: *asJSON}
	var err error
	if cfg.proxy, err = parseEndpoint(*proxy, "proxy"); err != nil {
		return nil, err
	}
	if cfg.target, err = parseEndpoint(*target, "url"); err != nil {
		return nil, err
	}
	return cfg, nil
}

// parseEndpoint reads a proxy or a URL, which is taken as an http one if it has no scheme (as the proxy of a call of the
// goto client is), with the default port of its scheme if it has no port.
func parseEndpoint(raw, what string) (*endpoint, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("%s is empty", what)
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		// what is left out of the message is the URL, as it can have a password
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return nil, fmt.Errorf("invalid %s: %w", what, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("the scheme [%s] of the %s is not supported, it must be http or https", u.Scheme, what)
	}
	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("the %s has no host", what)
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
		return nil, fmt.Errorf("invalid port [%s] in the %s", port, what)
	}
	return &endpoint{
		display:   u.Redacted(),
		tls:       u.Scheme == "https",
		host:      host,
		port:      port,
		addr:      net.JoinHostPort(host, port),
		authority: u.Host,
		uri:       u.RequestURI(),
		user:      u.User,
	}, nil
}

// bufConn is a connection that is read through a reader that may have bytes that it read ahead.
type bufConn struct {
	net.Conn
	r io.Reader
}

func (c *bufConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// probeCall makes a call the way the goto client does: a connection of its own to the proxy, a CONNECT for the target, and
// a GET through the tunnel, over TLS if the target is an https one.
func probeCall(ctx context.Context, cfg *config, i int) callResult {
	r := callResult{Call: i, DNS: na, TCP: na, ProxyTLS: na, Connect: na, TLS: na, TTFB: na, Body: na, Total: na}
	start := time.Now()
	deadline := start.Add(cfg.timeout)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	fail := func(at string, err error) callResult {
		r.FailedAt = at
		r.Error = err.Error()
		r.Total = dur(time.Since(start))
		return r
	}

	var dnsStart, dnsDone time.Time
	trace := &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) { dnsStart = time.Now() },
		DNSDone:  func(httptrace.DNSDoneInfo) { dnsDone = time.Now() },
	}
	var conn net.Conn
	conn, err := (&net.Dialer{}).DialContext(httptrace.WithClientTrace(ctx, trace), "tcp", cfg.proxy.addr)
	if err != nil {
		return fail("dial", err)
	}
	defer func() { conn.Close() }()
	_ = conn.SetDeadline(deadline)
	if !dnsStart.IsZero() && !dnsDone.IsZero() {
		r.DNS = dur(dnsDone.Sub(dnsStart))
	}
	r.TCP = dur(time.Since(start))
	if r.DNS >= 0 {
		r.TCP -= r.DNS
	}

	if cfg.proxy.tls {
		t := time.Now()
		tc := tls.Client(conn, tlsConfig(cfg, cfg.proxy.host, nil))
		if err := tc.HandshakeContext(ctx); err != nil {
			return fail("proxy-tls", err)
		}
		conn = tc
		r.ProxyTLS = dur(time.Since(t))
	}

	// CONNECT, as the goto client asks for it
	creq := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: cfg.target.addr}, Host: cfg.target.addr, Header: http.Header{}}
	if user := cfg.proxy.user; user != nil {
		password, _ := user.Password()
		creq.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user.Username()+":"+password)))
	}
	br := bufio.NewReader(conn)
	t := time.Now()
	if err := creq.Write(conn); err != nil {
		return fail("connect", err)
	}
	cresp, err := http.ReadResponse(br, creq)
	if err != nil {
		return fail("connect", err)
	}
	cresp.Body.Close()
	r.Connect = dur(time.Since(t))
	r.ConnectHeaders = pickHeaders(cresp.Header)
	if cresp.StatusCode/100 != 2 {
		return fail("connect", fmt.Errorf("the proxy answered CONNECT with %s", cresp.Status))
	}

	// the tunnel, which has what the proxy read ahead of the answer in the reader
	var tunnel net.Conn = &bufConn{Conn: conn, r: br}
	reader := br
	if cfg.target.tls {
		t := time.Now()
		tc := tls.Client(tunnel, tlsConfig(cfg, cfg.target.host, []string{"http/1.1"}))
		if err := tc.HandshakeContext(ctx); err != nil {
			return fail("tls", err)
		}
		tunnel = tc
		reader = bufio.NewReader(tc)
		r.TLS = dur(time.Since(t))
	}

	t = time.Now()
	if _, err := fmt.Fprintf(tunnel, "GET %s HTTP/1.1\r\nHost: %s\r\nUser-Agent: goto-connectprobe\r\nAccept: */*\r\n\r\n", cfg.target.uri, cfg.target.authority); err != nil {
		return fail("request", err)
	}
	resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		return fail("ttfb", err)
	}
	r.TTFB = dur(time.Since(t))
	r.Status = resp.StatusCode
	r.Headers = pickHeaders(resp.Header)
	t = time.Now()
	_, err = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if err != nil {
		return fail("body", err)
	}
	r.Body = dur(time.Since(t))
	r.Total = dur(time.Since(start))
	return r
}

func tlsConfig(cfg *config, serverName string, protos []string) *tls.Config {
	return &tls.Config{ServerName: serverName, InsecureSkipVerify: !cfg.verify, NextProtos: protos}
}

// pickHeaders gives the headers of goto that tell the time, joined if there is more than one line of a header.
func pickHeaders(h http.Header) map[string]string {
	picked := map[string]string{}
	for _, name := range probeHeaders {
		if values := h.Values(name); len(values) > 0 {
			picked[name] = strings.Join(values, ",")
		}
	}
	if len(picked) == 0 {
		return nil
	}
	return picked
}

// sleepCtx waits for the duration, and tells false if the context ended first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// checkAddresses resolves the name of the proxy, and connects to each of its addresses on its own, to tell whether one of
// the addresses of a name that has several is slow or dead, which is what makes a dialer wait for the other family.
func checkAddresses(ctx context.Context, cfg *config) *addrReport {
	rep := &addrReport{Host: cfg.proxy.host, Resolve: na}
	if ip := net.ParseIP(cfg.proxy.host); ip != nil {
		rep.Addrs = []addrResult{connectAddr(ctx, cfg, ip.String())}
		return rep
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.timeout)
	defer cancel()
	t := time.Now()
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, cfg.proxy.host)
	rep.Resolve = dur(time.Since(t))
	if err != nil {
		rep.Error = err.Error()
		return rep
	}
	for _, a := range addrs {
		rep.Addrs = append(rep.Addrs, connectAddr(ctx, cfg, a.IP.String()))
	}
	return rep
}

func connectAddr(ctx context.Context, cfg *config, ip string) addrResult {
	res := addrResult{IP: ip, Connect: na}
	ctx, cancel := context.WithTimeout(ctx, cfg.timeout)
	defer cancel()
	t := time.Now()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(ip, cfg.proxy.port))
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.Connect = dur(time.Since(t))
	conn.Close()
	return res
}

// summarize gives the min, average and max of each phase, over the calls that had it. It is nil for a single call.
func summarize(results []callResult) map[string]phaseStat {
	if len(results) < 2 {
		return nil
	}
	stats := map[string]phaseStat{}
	for _, ph := range phases {
		var s phaseStat
		var sum dur
		for i := range results {
			if results[i].Error != "" {
				continue
			}
			d := ph.get(&results[i])
			if d < 0 {
				continue
			}
			if s.N == 0 || d < s.Min {
				s.Min = d
			}
			if d > s.Max {
				s.Max = d
			}
			sum += d
			s.N++
		}
		if s.N > 0 {
			s.Avg = sum / dur(s.N)
			stats[ph.name] = s
		}
	}
	return stats
}

// coldStartHint says so when the first call is a lot slower than the ones that follow, and where the extra time went. A
// first call that is slow while the others are not is what a cold cache looks like: the caches that the first call fills
// (DNS, on either side of the proxy) are found filled by the ones that follow, until their entries expire.
func coldStartHint(results []callResult) string {
	if len(results) < 3 || results[0].Error != "" {
		return ""
	}
	first := &results[0]
	var rest []*callResult
	var restTotal dur
	for i := 1; i < len(results); i++ {
		if results[i].Error == "" {
			rest = append(rest, &results[i])
			restTotal += results[i].Total
		}
	}
	if len(rest) < 2 {
		return ""
	}
	avg := restTotal / dur(len(rest))
	if first.Total < 2*avg || time.Duration(first.Total-avg) < hintMinExtra {
		return ""
	}
	name, most := "", dur(0)
	for _, ph := range phases[:len(phases)-1] {
		f := ph.get(first)
		if f < 0 {
			continue
		}
		var sum dur
		n := 0
		for _, r := range rest {
			if d := ph.get(r); d >= 0 {
				sum += d
				n++
			}
		}
		base := dur(0)
		if n > 0 {
			base = sum / dur(n)
		}
		if extra := f - base; extra > most {
			name, most = ph.name, extra
		}
	}
	if name == "" {
		return ""
	}
	hint := fmt.Sprintf("call 1 took %s, against %s on average for the calls after it. Most of the extra time is in %s (+%s). "+
		"A slow first call that the others don't repeat is what a cold cache looks like (DNS, for one); the cache is found filled "+
		"until its entries expire, so try -interval to see it come back.", first.Total, avg, name, most)
	if name == "connect" {
		if took, err := time.ParseDuration(first.ConnectHeaders["Goto-Connect-Proxy-Took"]); err == nil && took > 0 {
			hint += fmt.Sprintf(" The proxy says %s of the CONNECT exchange was its own (Goto-Connect-Proxy-Took in its answer): the lookup of the target and the connection to it.", dur(took))
			// a proxy that tells the two apart says which of them it was
			var parts []string
			for _, h := range []struct{ name, what string }{{"Goto-Connect-Proxy-DNS-Took", "the lookup"}, {"Goto-Connect-Proxy-Dial-Took", "the connection"}} {
				if d, err := time.ParseDuration(first.ConnectHeaders[h.name]); err == nil {
					parts = append(parts, fmt.Sprintf("%s was %s", h.what, dur(d)))
				}
			}
			if len(parts) > 0 {
				hint += " Of it, " + strings.Join(parts, " and ") + "."
			}
		}
	}
	return hint
}

func printCall(w io.Writer, r *callResult) {
	status := "-"
	if r.Status > 0 {
		status = strconv.Itoa(r.Status)
	}
	fmt.Fprintf(w, "%-5d %-10s %-10s %-10s %-10s %-10s %-10s %-10s %-10s %s\n", r.Call, r.DNS, r.TCP, r.ProxyTLS, r.Connect, r.TLS,
		r.TTFB, r.Body, r.Total, status)
	if r.Error != "" {
		fmt.Fprintf(w, "      FAILED at %s after %s: %s\n", r.FailedAt, r.Total, r.Error)
	}
	if line := headerLine(r.ConnectHeaders); line != "" {
		fmt.Fprintf(w, "      CONNECT answer: %s\n", line)
	}
	if line := headerLine(r.Headers); line != "" {
		fmt.Fprintf(w, "      response: %s\n", line)
	}
}

func headerLine(h map[string]string) string {
	var parts []string
	for _, name := range probeHeaders {
		if v, ok := h[name]; ok {
			parts = append(parts, name+"="+v)
		}
	}
	return strings.Join(parts, " ")
}

func printReport(w io.Writer, rep *report) {
	if rep.Summary != nil {
		fmt.Fprintf(w, "\nsummary over the calls that got a response:\n")
		fmt.Fprintf(w, "%-10s %-6s %-10s %-10s %s\n", "phase", "calls", "min", "avg", "max")
		for _, ph := range phases {
			if s, ok := rep.Summary[ph.name]; ok {
				fmt.Fprintf(w, "%-10s %-6d %-10s %-10s %s\n", ph.name, s.N, s.Min, s.Avg, s.Max)
			}
		}
	}
	if rep.Hint != "" {
		fmt.Fprintf(w, "\nnote: %s\n", rep.Hint)
	}
	if a := rep.Addresses; a != nil {
		fmt.Fprintf(w, "\naddresses of %s", a.Host)
		if a.Resolve >= 0 {
			fmt.Fprintf(w, " (resolved in %s)", a.Resolve)
		}
		fmt.Fprintln(w, ":")
		if a.Error != "" {
			fmt.Fprintf(w, "  failed to resolve: %s\n", a.Error)
		}
		for _, addr := range a.Addrs {
			if addr.Error != "" {
				fmt.Fprintf(w, "  %s: failed to connect: %s\n", addr.IP, addr.Error)
			} else {
				fmt.Fprintf(w, "  %s: connected alone in %s\n", addr.IP, addr.Connect)
			}
		}
	}
}
