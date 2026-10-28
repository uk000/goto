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
	"fmt"
	"goto/pkg/global"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// proxyHeaderLines are the lines of the headers that the proxy adds to the responses on top of Via-Goto, which have values
// that change from one run to the other (the time that the proxy took, and the address of the client).
var proxyHeaderLines = regexp.MustCompile(`(?m)^(Goto-Connect-Proxy-[A-Za-z-]+): [^\r\n]*\r?\n`)

// setViaLabels makes the labels of the listeners, which the proxy puts in the Via-Goto header, ones that tell the port.
// Call it before the harness is created, so that the labels are restored after its connections are over.
func setViaLabels(t *testing.T) {
	t.Helper()
	setup()
	old := global.Funcs.GetListenerLabelForPort
	global.Funcs.GetListenerLabelForPort = func(port int) string { return fmt.Sprintf("[proxy:%d]", port) }
	t.Cleanup(func() { global.Funcs.GetListenerLabelForPort = old })
}

// shortenMaxHead makes the proxy give up on the heads of HTTP messages that are larger than the given size. It is to be
// called, like the other changes of what the proxy reads, before the harness is created.
func shortenMaxHead(t *testing.T, size int) {
	t.Helper()
	old := connectMaxHeadBytes
	connectMaxHeadBytes = size
	t.Cleanup(func() { connectMaxHeadBytes = old })
}

func (h *connectHarness) via() string { return fmt.Sprintf("[proxy:%d](Proxy)(HTTP)", h.port) }

// httpTunnel is a client that speaks HTTP/1.x through a tunnel.
type httpTunnel struct {
	t  *testing.T
	tc *tunnelConn
	br *bufio.Reader
}

func openHTTPTunnel(t *testing.T, h *connectHarness, dest string) *httpTunnel {
	t.Helper()
	tc := openTunnel(t, h.dial(), dest, "")
	return &httpTunnel{t: t, tc: tc, br: bufio.NewReader(tc)}
}

func (c *httpTunnel) send(raw string) {
	c.t.Helper()
	c.tc.SetWriteDeadline(time.Now().Add(waitTimeout))
	if _, err := io.WriteString(c.tc, raw); err != nil {
		c.t.Fatal(err)
	}
}

// readHead reads the head of a response, which is for a request of the given method.
func (c *httpTunnel) readHead(method string) *http.Response {
	c.t.Helper()
	c.tc.SetReadDeadline(time.Now().Add(waitTimeout))
	resp, err := http.ReadResponse(c.br, &http.Request{Method: method})
	if err != nil {
		c.t.Fatalf("failed to read the response to %s: %v", method, err)
	}
	return resp
}

// read reads a response, with its body, which is for a request of the given method.
func (c *httpTunnel) read(method string) (*http.Response, string) {
	c.t.Helper()
	resp := c.readHead(method)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.t.Fatalf("failed to read the body of the response to %s: %v", method, err)
	}
	resp.Body.Close()
	return resp, string(body)
}

func get(path string) string { return fmt.Sprintf("GET %s HTTP/1.1\r\nHost: x\r\n\r\n", path) }

func head(path string) string { return fmt.Sprintf("HEAD %s HTTP/1.1\r\nHost: x\r\n\r\n", path) }

func expectVia(t *testing.T, what string, resp *http.Response, want ...string) {
	t.Helper()
	if got := resp.Header["Via-Goto"]; !reflect.DeepEqual(got, want) {
		t.Errorf("%s: Via-Goto = %q, want %q", what, got, want)
	}
}

func newHTTPDestination(t *testing.T) string {
	mux := http.NewServeMux()
	mux.HandleFunc("/goto", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Via-Goto", "upstream-goto")
		io.WriteString(w, "goto")
	})
	mux.HandleFunc("/len", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "5")
		io.WriteString(w, "12345")
	})
	mux.HandleFunc("/chunked", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ab")
		w.(http.Flusher).Flush()
		io.WriteString(w, "cde")
		w.(http.Flusher).Flush()
	})
	mux.HandleFunc("/trailers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Trailer", "X-T")
		io.WriteString(w, "body")
		w.Header().Set("X-T", "tv")
	})
	mux.HandleFunc("/204", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("/304", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotModified) })
	mux.HandleFunc("/head", func(w http.ResponseWriter, r *http.Request) {
		// The response to HEAD has the length of the body that it doesn't have
		w.Header().Set("Content-Length", "4")
		io.WriteString(w, "body")
	})
	mux.HandleFunc("/post", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fmt.Fprintf(w, "got %d", len(body))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().String()
}

func TestConnectAddsViaGotoToHTTPResponses(t *testing.T) {
	forPlainAndTLS(t, func(t *testing.T, isTLS bool) {
		setViaLabels(t)
		dest := newHTTPDestination(t)
		h := newConnectHarness(t, isTLS)
		via := h.via()
		c := openHTTPTunnel(t, h, dest)

		// Messages of all the kinds, one after the other on a connection, and the one that is last tells whether the proxy
		// found the end of each of them
		cases := []struct {
			name     string
			method   string
			request  string
			status   int
			body     string
			upstream []string
		}{
			{"no header yet", "GET", get("/len"), 200, "12345", nil},
			{"the header exists", "GET", get("/goto"), 200, "goto", []string{"upstream-goto"}},
			{"chunked", "GET", get("/chunked"), 200, "abcde", nil},
			{"chunked with trailers", "GET", get("/trailers"), 200, "body", nil},
			{"no content", "GET", get("/204"), 204, "", nil},
			{"not modified", "GET", get("/304"), 304, "", nil},
			{"response to head", "HEAD", head("/head"), 200, "", nil},
			{"post with a length", "POST", "POST /post HTTP/1.1\r\nHost: x\r\nContent-Length: 5\r\n\r\n12345", 200, "got 5", nil},
			{"post in chunks", "POST", "POST /post HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\n3\r\nabc\r\n2;ext=1\r\nde\r\n0\r\nT: v\r\n\r\n", 200, "got 5", nil},
			{"the last one", "GET", get("/len"), 200, "12345", nil},
		}
		for _, tc := range cases {
			c.send(tc.request)
			resp, body := c.read(tc.method)
			if resp.StatusCode != tc.status || body != tc.body {
				t.Fatalf("%s: got %d %q, want %d %q", tc.name, resp.StatusCode, body, tc.status, tc.body)
			}
			expectVia(t, tc.name, resp, append(append([]string{}, tc.upstream...), via)...)
		}
	})
}

func TestConnectAddsViaGotoToResponsesOfPipelinedRequests(t *testing.T) {
	setViaLabels(t)
	dest := newHTTPDestination(t)
	h := newConnectHarness(t, false)
	c := openHTTPTunnel(t, h, dest)
	// The response to the HEAD in the middle has no body, and has to be told from the others by the proxy
	c.send(get("/len") + head("/head") + get("/len"))
	for i, method := range []string{"GET", "HEAD", "GET"} {
		resp, body := c.read(method)
		want := "12345"
		if method == "HEAD" {
			want = ""
		}
		if resp.StatusCode != 200 || body != want {
			t.Fatalf("response %d: got %d %q, want 200 %q", i, resp.StatusCode, body, want)
		}
		expectVia(t, fmt.Sprintf("response %d", i), resp, h.via())
	}
}

func TestConnectDoesNotAddViaGotoToInformationalResponses(t *testing.T) {
	setViaLabels(t)
	dest := newHTTPDestination(t)
	h := newConnectHarness(t, false)
	c := openHTTPTunnel(t, h, dest)
	c.send("POST /post HTTP/1.1\r\nHost: x\r\nContent-Length: 5\r\nExpect: 100-continue\r\n\r\n")
	interim := c.readHead("POST")
	if interim.StatusCode != http.StatusContinue {
		t.Fatalf("got %d, want 100", interim.StatusCode)
	}
	expectVia(t, "100 Continue", interim)
	c.send("12345")
	resp, body := c.read("POST")
	if resp.StatusCode != 200 || body != "got 5" {
		t.Fatalf("got %d %q", resp.StatusCode, body)
	}
	expectVia(t, "the response", resp, h.via())
}

// The traffic that goes to the destination is passed on as it came, and is not changed by the proxy following it.
func TestConnectPassesOnHTTPRequestsAsTheyCame(t *testing.T) {
	setViaLabels(t)
	want := "GET /x HTTP/1.1\r\nhOsT: x\r\nX-Weird:    spaced   \r\n\r\n" +
		"POST /p HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\n3;ext=1\r\nabc\r\n0\r\nT: v\r\n\r\n"
	rec := &recorder{}
	dest := startServer(t, func(c net.Conn) {
		buff := make([]byte, len(want))
		if _, err := io.ReadFull(c, buff); err != nil {
			return
		}
		rec.write(buff)
		io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\nHTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")
	})
	h := newConnectHarness(t, false)
	c := openHTTPTunnel(t, h, dest.Addr())
	c.send(want)
	for _, method := range []string{"GET", "POST"} {
		resp, _ := c.read(method)
		expectVia(t, method, resp, h.via())
	}
	if got := rec.String(); got != want {
		t.Errorf("the destination got %q, want %q", got, want)
	}
}

func TestConnectAddsViaGotoToEveryResponseOfOneRequest(t *testing.T) {
	setViaLabels(t)
	dest := startServer(t, func(c net.Conn) {
		if _, err := http.ReadRequest(bufio.NewReader(c)); err != nil {
			return
		}
		// A response with trailers, and the one that follows it with no request to answer to
		io.WriteString(c, "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nTrailer: X-T\r\n\r\n3\r\nabc\r\n0\r\nX-T: tv\r\n\r\n"+
			"HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
	})
	h := newConnectHarness(t, false)
	c := openHTTPTunnel(t, h, dest.Addr())
	c.send(get("/"))
	resp, body := c.read("GET")
	if body != "abc" || resp.Trailer.Get("X-T") != "tv" {
		t.Errorf("first: body = %q, trailer = %v", body, resp.Trailer)
	}
	expectVia(t, "first", resp, h.via())
	resp, body = c.read("GET")
	if body != "ok" {
		t.Errorf("second: body = %q", body)
	}
	expectVia(t, "second", resp, h.via())
}

func TestConnectAddsViaGotoToResponseThatLastsUntilClose(t *testing.T) {
	setViaLabels(t)
	dest := startServer(t, func(c net.Conn) {
		if _, err := http.ReadRequest(bufio.NewReader(c)); err != nil {
			return
		}
		io.WriteString(c, "HTTP/1.0 200 OK\r\nX-A: b\r\n\r\nbody until close")
	})
	h := newConnectHarness(t, false)
	c := openHTTPTunnel(t, h, dest.Addr())
	c.send(get("/"))
	resp, body := c.read("GET")
	if body != "body until close" || resp.Header.Get("X-A") != "b" {
		t.Errorf("got %q, headers %v", body, resp.Header)
	}
	expectVia(t, "the response", resp, h.via())
}

// What follows a protocol switch is not HTTP, and is passed on as it is.
func TestConnectPassesOnWhatFollowsProtocolSwitch(t *testing.T) {
	setViaLabels(t)
	dest := startServer(t, func(c net.Conn) {
		br := bufio.NewReader(c)
		if _, err := http.ReadRequest(br); err != nil {
			return
		}
		io.WriteString(c, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: echo\r\nConnection: Upgrade\r\n\r\n")
		io.Copy(c, br)
	})
	h := newConnectHarness(t, false)
	c := openHTTPTunnel(t, h, dest.Addr())
	c.send("GET / HTTP/1.1\r\nHost: x\r\nConnection: Upgrade\r\nUpgrade: echo\r\n\r\n")
	resp := c.readHead("GET")
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("got %d, want 101", resp.StatusCode)
	}
	expectVia(t, "the switch", resp, h.via())
	// Bytes that look like HTTP, and bytes that don't
	for _, msg := range []string{"ping", "HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n", "GET / HTTP/1.1\r\n\r\n"} {
		c.send(msg)
		c.tc.SetReadDeadline(time.Now().Add(waitTimeout))
		got := make([]byte, len(msg))
		if _, err := io.ReadFull(c.br, got); err != nil || string(got) != msg {
			t.Fatalf("echo of %q = %q, err %v", msg, got, err)
		}
	}
}

func TestConnectDoesNotChangeTrafficThatIsNotHTTP(t *testing.T) {
	setViaLabels(t)
	// A destination that speaks first, and then echoes
	dest := startServer(t, func(c net.Conn) {
		io.WriteString(c, "220 ready\r\n")
		io.Copy(c, c)
	})
	h := newConnectHarness(t, false)
	tc := openTunnel(t, h.dial(), dest.Addr(), "")
	if got := readN(t, tc, len("220 ready\r\n")); string(got) != "220 ready\r\n" {
		t.Errorf("greeting = %q", got)
	}
	if got := roundTrip(t, tc, "HELO example.com\r\n"); got != "HELO example.com\r\n" {
		t.Errorf("echo = %q", got)
	}
}

// What can't be followed is passed on as it is, and nothing is added to what comes before the proxy finds out. A response
// that has a head that the proxy can make sense of gets its header, and what the proxy can't follow after it is passed on.
func TestConnectPassesOnHTTPThatItCantFollow(t *testing.T) {
	cases := map[string]struct{ response, want string }{
		"a header that is not one":    {"HTTP/1.1 200 OK\r\nthis is not a header\r\n\r\nrest-of-it", ""},
		"two lengths":                 {"HTTP/1.1 200 OK\r\nContent-Length: 2\r\nContent-Length: 3\r\n\r\nrest-of-it", ""},
		"a head that is too large":    {"HTTP/1.1 200 OK\r\nX-Large: " + strings.Repeat("a", 5000) + "\r\n\r\nrest-of-it", ""},
		"a start line that is not":    {"HTTP/1.1 OK\r\nContent-Length: 2\r\n\r\nrest-of-it", ""},
		"a head that is not whole":    {"HTTP/1.1 200 OK\r\nContent-Le", ""},
		"a version that is not HTTP1": {"HTTP/2.0 200 OK\r\nContent-Length: 2\r\n\r\nrest-of-it", ""},
		"a chunk size that is not": {"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\nzz\r\nrest-of-it",
			"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nVia-Goto: %VIA%\r\n\r\nzz\r\nrest-of-it"},
		"a body that is cut short": {"HTTP/1.1 200 OK\r\nContent-Length: 20\r\n\r\nshort",
			"HTTP/1.1 200 OK\r\nContent-Length: 20\r\nVia-Goto: %VIA%\r\n\r\nshort"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			setViaLabels(t)
			shortenMaxHead(t, 1024)
			dest := startServer(t, func(c net.Conn) {
				if _, err := http.ReadRequest(bufio.NewReader(c)); err != nil {
					return
				}
				io.WriteString(c, tc.response)
			})
			h := newConnectHarness(t, false)
			conn := openTunnel(t, h.dial(), dest.Addr(), "")
			io.WriteString(conn, get("/"))
			conn.SetReadDeadline(time.Now().Add(waitTimeout))
			got, err := io.ReadAll(conn)
			want := tc.want
			if want == "" {
				want = tc.response
			}
			want = strings.ReplaceAll(want, "%VIA%", h.via())
			// The other headers that the proxy adds are not what this is about
			got = proxyHeaderLines.ReplaceAll(got, nil)
			if err != nil || string(got) != want {
				t.Errorf("got %q, err %v, want %q", got, err, want)
			}
		})
	}
}

// Requests that the proxy can't follow make it stop following the responses, which it couldn't tell the end of.
func TestConnectStopsFollowingResponsesWhenItLostTheRequests(t *testing.T) {
	setViaLabels(t)
	dest := startServer(t, func(c net.Conn) {
		br := bufio.NewReader(c)
		readHead := func() bool {
			for {
				line, err := br.ReadString('\n')
				if err != nil {
					return false
				}
				if line == "\r\n" {
					return true
				}
			}
		}
		if !readHead() {
			return
		}
		io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Length: 1\r\n\r\na")
		if !readHead() {
			return
		}
		// The response to a HEAD request, that the proxy didn't get to see
		io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\n")
		if !readHead() {
			return
		}
		io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Length: 1\r\n\r\nc")
	})
	h := newConnectHarness(t, false)
	c := openHTTPTunnel(t, h, dest.Addr())
	c.send(get("/a"))
	resp, body := c.read("GET")
	if body != "a" {
		t.Fatalf("first: body = %q", body)
	}
	expectVia(t, "first", resp, h.via())
	// Two lengths: the proxy can't tell where this request ends
	c.send("HEAD /b HTTP/1.1\r\nContent-Length: 1\r\nContent-Length: 2\r\n\r\n")
	expectVia(t, "second", c.readHead("HEAD"), h.via())
	// Had the proxy taken the response to the HEAD request for one with a body, it would have eaten the start of this one
	c.send(get("/c"))
	resp, body = c.read("GET")
	if resp.StatusCode != 200 || body != "c" {
		t.Fatalf("third: got %d %q", resp.StatusCode, body)
	}
	expectVia(t, "third", resp)
}

func TestConnectFollowsRequestsThatComeInPieces(t *testing.T) {
	setViaLabels(t)
	dest := newHTTPDestination(t)
	h := newConnectHarness(t, false)
	c := openHTTPTunnel(t, h, dest)
	for _, piece := range []string{"HE", "AD /he", "ad HTTP/1.1\r\nHo", "st: x\r\n", "\r\n"} {
		c.send(piece)
		time.Sleep(20 * time.Millisecond)
	}
	resp, body := c.read("HEAD")
	if resp.StatusCode != 200 || body != "" {
		t.Fatalf("head: got %d %q", resp.StatusCode, body)
	}
	expectVia(t, "head", resp, h.via())
	c.send(get("/len"))
	resp, body = c.read("GET")
	if resp.StatusCode != 200 || body != "12345" {
		t.Fatalf("get: got %d %q", resp.StatusCode, body)
	}
	expectVia(t, "get", resp, h.via())
}

// ---------------------------------------------------------------------------------------------------------------

func TestWithViaGoto(t *testing.T) {
	cases := []struct{ name, raw, want string }{
		{"headers", "HTTP/1.1 200 OK\r\nA: b\r\n\r\n", "HTTP/1.1 200 OK\r\nA: b\r\nVia-Goto: V\r\n\r\n"},
		{"the header exists", "HTTP/1.1 200 OK\r\nVia-Goto: other\r\n\r\n", "HTTP/1.1 200 OK\r\nVia-Goto: other\r\nVia-Goto: V\r\n\r\n"},
		{"no headers", "HTTP/1.1 204 No Content\r\n\r\n", "HTTP/1.1 204 No Content\r\nVia-Goto: V\r\n\r\n"},
		{"line feeds", "HTTP/1.1 200 OK\nA: b\n\n", "HTTP/1.1 200 OK\nA: b\nVia-Goto: V\n\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(withViaGoto([]byte(tc.raw), "V")); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestConnectViaGoto(t *testing.T) {
	setup()
	old := global.Funcs.GetListenerLabelForPort
	t.Cleanup(func() { global.Funcs.GetListenerLabelForPort = old })
	global.Funcs.GetListenerLabelForPort = func(port int) string { return fmt.Sprintf("[host:%d]\r\n", port) }
	if got := connectViaGoto(7); got != "[host:7](Proxy)(HTTP)" {
		t.Errorf("via = %q, want the label of the port, as the HTTP proxy puts it, and no line breaks", got)
	}
	global.Funcs.GetListenerLabelForPort = nil
	if got := connectViaGoto(7); got != "(Proxy)(HTTP)" {
		t.Errorf("via = %q without a label", got)
	}
}

func TestClassifyHTTPStart(t *testing.T) {
	cases := []struct {
		in          string
		response    bool
		decided, ok bool
	}{
		{"", false, false, false},
		{"G", false, false, false},
		{"GET", false, false, false},
		{"GET ", false, false, false},
		{"GET / HTTP/1.1", false, false, false},
		{"GET / HTTP/1.1\r\n", false, true, true},
		{"GET / HTTP/1.0\n", false, true, true},
		{"M-SEARCH * HTTP/1.1\r\nHost: x\r\n\r\n", false, true, true},
		// Commands of the protocols that are not HTTP
		{"HELO example.com\r\n", false, true, false},
		{"GET key\r\n", false, true, false},
		{"GET /\r\n", false, true, false},
		{"GET / HTTP/2.0\r\n", false, true, false},
		{"GET / HTTP/1.1 extra\r\n", false, true, false},
		{"get / HTTP/1.1\r\n", false, true, false},
		{" GET / HTTP/1.1\r\n", false, true, false},
		{"\x16\x03\x01", false, true, false},
		{strings.Repeat("A", 40), false, true, false},
		{"GET /" + strings.Repeat("a", connectMaxStartLine), false, true, false},
		{"", true, false, false},
		{"H", true, false, false},
		{"HTTP/1", true, false, false},
		{"HTTP/1.", true, true, true},
		{"HTTP/1.1 200 OK", true, true, true},
		{"HTTP/2.0", true, true, false},
		{"HTTX", true, true, false},
		{"\x16\x03\x03", true, true, false},
	}
	for _, tc := range cases {
		decided, ok := classifyHTTPStart([]byte(tc.in), tc.response)
		if decided != tc.decided || ok != tc.ok {
			t.Errorf("classify(%q, response=%v) = %v, %v, want %v, %v", tc.in, tc.response, decided, ok, tc.decided, tc.ok)
		}
	}
}

func TestParseHTTPHead(t *testing.T) {
	type want struct {
		method  string
		status  int
		chunked bool
		length  int64
		fail    bool
	}
	cases := []struct {
		name     string
		raw      string
		response bool
		want     want
	}{
		{"request", "GET / HTTP/1.1\r\nHost: x\r\n\r\n", false, want{method: "GET"}},
		{"request with a length", "POST / HTTP/1.1\r\nContent-Length: 12\r\n\r\n", false, want{method: "POST", length: 12}},
		{"request in chunks", "POST / HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n", false, want{method: "POST", chunked: true}},
		{"chunks that are last of the codings", "POST / HTTP/1.1\r\nTransfer-Encoding: gzip, Chunked\r\n\r\n", false, want{method: "POST", chunked: true}},
		{"chunks win over a length", "POST / HTTP/1.1\r\nContent-Length: 4\r\nTransfer-Encoding: chunked\r\n\r\n", false, want{method: "POST", chunked: true}},
		{"request with coding that has no end", "POST / HTTP/1.1\r\nTransfer-Encoding: gzip\r\n\r\n", false, want{fail: true}},
		{"request that is not one", "GET /\r\n\r\n", false, want{fail: true}},
		{"request of another HTTP", "GET / HTTP/2.0\r\n\r\n", false, want{fail: true}},
		{"request with a method that is not one", "get / HTTP/1.1\r\n\r\n", false, want{fail: true}},
		{"response", "HTTP/1.1 200 OK\r\nContent-Length: 3\r\n\r\n", true, want{status: 200, length: 3}},
		{"response with no reason", "HTTP/1.1 204\r\n\r\n", true, want{status: 204, length: -1}},
		{"response that has no length", "HTTP/1.0 200 OK\r\n\r\n", true, want{status: 200, length: -1}},
		{"response in chunks", "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n", true, want{status: 200, chunked: true, length: -1}},
		{"response with coding that has no end", "HTTP/1.1 200 OK\r\nTransfer-Encoding: gzip\r\n\r\n", true, want{status: 200, length: -1}},
		{"same length twice", "HTTP/1.1 200 OK\r\nContent-Length: 3, 3\r\nContent-Length: 3\r\n\r\n", true, want{status: 200, length: 3}},
		{"two lengths", "HTTP/1.1 200 OK\r\nContent-Length: 3\r\nContent-Length: 4\r\n\r\n", true, want{fail: true}},
		{"length that is not one", "HTTP/1.1 200 OK\r\nContent-Length: +3\r\n\r\n", true, want{fail: true}},
		{"negative length", "HTTP/1.1 200 OK\r\nContent-Length: -3\r\n\r\n", true, want{fail: true}},
		{"status that is not one", "HTTP/1.1 2x0 OK\r\n\r\n", true, want{fail: true}},
		{"status that is too low", "HTTP/1.1 099 OK\r\n\r\n", true, want{fail: true}},
		{"header that is not one", "HTTP/1.1 200 OK\r\nnot a header\r\n\r\n", true, want{fail: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			head, err := parseHTTPHead([]byte(tc.raw), tc.response)
			if tc.want.fail {
				if err == nil {
					t.Errorf("got %+v, want an error", head)
				}
				return
			}
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if head.method != tc.want.method || head.status != tc.want.status || head.chunked != tc.want.chunked || head.length != tc.want.length {
				t.Errorf("got method %q status %d chunked %v length %d, want %+v", head.method, head.status, head.chunked, head.length, tc.want)
			}
		})
	}
}

func TestReadHTTPHead(t *testing.T) {
	read := func(in string) (string, error) {
		raw, err := readHTTPHead(bufio.NewReaderSize(strings.NewReader(in), 16))
		return string(raw), err
	}
	// Heads that are longer than the buffer that they are read through
	long := "HTTP/1.1 200 OK\r\nX: " + strings.Repeat("a", 100) + "\r\n\r\n"
	if got, err := read(long + "body"); err != nil || got != long {
		t.Errorf("long head = %q, err %v", got, err)
	}
	if got, err := read("HTTP/1.1 200 OK\nA: b\n\nbody"); err != nil || got != "HTTP/1.1 200 OK\nA: b\n\n" {
		t.Errorf("head with line feeds = %q, err %v", got, err)
	}
	if got, err := read("\r\nGET / HTTP/1.1\r\n\r\n"); err != errMalformedHTTP || got != "\r\n" {
		t.Errorf("head that starts with a blank line = %q, err %v, want it malformed", got, err)
	}
	if got, err := read("HTTP/1.1 200 OK\r\nContent-Le"); err != io.EOF || got != "HTTP/1.1 200 OK\r\nContent-Le" {
		t.Errorf("head that is not whole = %q, err %v, want what was read, and the end", got, err)
	}
	if got, err := read(""); err != io.EOF || got != "" {
		t.Errorf("no head = %q, err %v", got, err)
	}
	old := connectMaxHeadBytes
	connectMaxHeadBytes = 64
	defer func() { connectMaxHeadBytes = old }()
	if _, err := read(long); err != errHeadTooLarge {
		t.Errorf("err = %v, want the head to be too large", err)
	}
}

func TestCopyChunked(t *testing.T) {
	const rest = "GET / HTTP/1.1\r\n\r\n"
	for name, body := range map[string]string{
		"chunks":     "3\r\nabc\r\n5\r\nhello\r\n0\r\n\r\n",
		"extensions": "3;a=b\r\nabc\r\n0;c=d\r\n\r\n",
		"trailers":   "3\r\nabc\r\n0\r\nT: v\r\nU: w\r\n\r\n",
		"hex sizes":  "A\r\n0123456789\r\n0\r\n\r\n",
		"no chunks":  "0\r\n\r\n",
		"line feeds": "3\nabc\n0\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			br := bufio.NewReaderSize(strings.NewReader(body+rest), 16)
			if err := copyChunked(&out, br); err != nil || out.String() != body {
				t.Fatalf("copied %q, err %v, want %q", out.String(), err, body)
			}
			// What comes after the body is left to be read
			if left, _ := io.ReadAll(br); string(left) != rest {
				t.Errorf("left %q, want %q", left, rest)
			}
		})
	}
	for name, body := range map[string]string{
		"a size that is not":      "zz\r\nabc\r\n",
		"data without line break": "3\r\nabcde\r\n",
		"a size that is too long": strings.Repeat("1", 40) + "\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			err := copyChunked(&out, bufio.NewReader(strings.NewReader(body)))
			if err != errMalformedHTTP {
				t.Errorf("err = %v, want it malformed", err)
			}
		})
	}
	t.Run("cut short", func(t *testing.T) {
		var out bytes.Buffer
		if err := copyChunked(&out, bufio.NewReader(strings.NewReader("5\r\nabc"))); err == nil || out.String() != "5\r\nabc" {
			t.Errorf("copied %q, err %v, want what there was, and an error", out.String(), err)
		}
	})
}

func TestMethodQueue(t *testing.T) {
	var q methodQueue
	if got := q.pop(); got != (exchange{}) {
		t.Errorf("pop of nothing = %+v", got)
	}
	start := time.Now()
	q.push(exchange{method: "GET", start: start, delayed: time.Second})
	q.push(exchange{method: "HEAD"})
	if a, b, c := q.pop(), q.pop(), q.pop(); a != (exchange{method: "GET", start: start, delayed: time.Second}) || b.method != "HEAD" || c.method != "" {
		t.Errorf("popped %+v, %+v, %+v, want them in the order they were pushed", a, b, c)
	}
	old := connectMaxPipelined
	connectMaxPipelined = 2
	defer func() { connectMaxPipelined = old }()
	q.push(exchange{method: "A"})
	q.push(exchange{method: "B"})
	q.push(exchange{method: "C"})
	if a, b, c := q.pop(), q.pop(), q.pop(); a.method != "A" || b.method != "B" || c.method != "" {
		t.Errorf("popped %+v, %+v, %+v, want the requests beyond the bound to be left out", a, b, c)
	}
}

// The proxy answers a CONNECT request on its own, and its answer carries its identity as the responses that it passes on do.
func TestConnectResponseHasViaGoto(t *testing.T) {
	forPlainAndTLS(t, func(t *testing.T, isTLS bool) {
		setViaLabels(t)
		dest := newHTTPDestination(t)
		h := newConnectHarness(t, isTLS)
		tc, resp := sendConnect(t, h.dial(), connectRequest(dest))
		if resp.StatusCode != http.StatusOK || resp.Status != "200 Connection Established" {
			t.Fatalf("CONNECT answered %q", resp.Status)
		}
		expectVia(t, "the answer to CONNECT", resp, h.via())
		// the answer is no more than that: the tunnel starts right behind it
		if _, found := resp.Header["Content-Length"]; found {
			t.Errorf("the answer to CONNECT has a length: %v", resp.Header)
		}
		// and the responses of the tunneled traffic have it as well, on top of what the destination says
		c := &httpTunnel{t: t, tc: tc, br: bufio.NewReader(tc)}
		c.send(get("/goto"))
		got, body := c.read("GET")
		if body != "goto" {
			t.Errorf("body = %q", body)
		}
		expectVia(t, "the tunneled response", got, "upstream-goto", h.via())
	})
}

func TestConnectRejectionsHaveViaGoto(t *testing.T) {
	setViaLabels(t)
	h := newConnectHarness(t, false)
	closed := closedAddress(t)
	tests := []struct {
		name    string
		request string
		status  int
		allow   string
	}{
		{"not CONNECT", "GET http://example.com/ HTTP/1.1\r\nHost: example.com\r\n\r\n", http.StatusMethodNotAllowed, "CONNECT"},
		{"bad destination", "CONNECT not-an-authority HTTP/1.1\r\nHost: x\r\n\r\n", http.StatusBadRequest, ""},
		{"destination unreachable", connectRequest(closed), http.StatusBadGateway, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, resp := sendConnect(t, h.dial(), tc.request)
			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.status)
			}
			expectVia(t, "the answer", resp, h.via())
			if got := resp.Header.Get("Allow"); got != tc.allow {
				t.Errorf("Allow = %q, want %q", got, tc.allow)
			}
			if !resp.Close {
				t.Error("the answer doesn't say that the connection is closed")
			}
		})
	}
}

// ---------------------------------------------------------------------------------------------------------------

// setHostLabel sets the label that the proxy tells its host by, for the length of the test.
func setHostLabel(t *testing.T, label string) {
	t.Helper()
	old := global.Self.HostLabel
	global.Self.HostLabel = label
	t.Cleanup(func() { global.Self.HostLabel = old })
}

// newStatusDestination is a destination that answers with the status in the path of the request, after the time that the
// query asks for, and says its own things about statuses, as a goto behind a goto does.
func newStatusDestination(t *testing.T) string {
	mux := http.NewServeMux()
	mux.HandleFunc("/status/", func(w http.ResponseWriter, r *http.Request) {
		var status int
		fmt.Sscanf(strings.TrimPrefix(r.URL.Path, "/status/"), "%d", &status)
		if d, err := time.ParseDuration(r.URL.Query().Get("sleep")); err == nil {
			time.Sleep(d)
		}
		w.Header().Set("Goto-Upstream-Status", `{"behind":200}`)
		// what a goto HTTP proxy that is behind the tunnel says: it is not the CONNECT proxy, and has headers of its own
		w.Header().Set("Goto-Proxy-Upstream-Status", `{"t":{"ep":["200"]}}`)
		w.Header().Set("Goto-Host", "destination-host")
		w.WriteHeader(status)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().String()
}

// The proxy tells about itself and about the status that the destination answered with, as the HTTP proxy does, in
// headers that are its own, and what the destination says is passed on as it is, which is what the proxy does with all
// that it doesn't add.
func TestConnectAddsProxyHeadersToHTTPResponses(t *testing.T) {
	forPlainAndTLS(t, func(t *testing.T, isTLS bool) {
		setViaLabels(t)
		setHostLabel(t, "proxy-host")
		dest := newStatusDestination(t)
		h := newConnectHarness(t, isTLS)
		c := openHTTPTunnel(t, h, dest)
		for _, status := range []int{200, 404, 503, 204} {
			c.send(get(fmt.Sprintf("/status/%d", status)))
			resp := c.readHead("GET")
			if resp.StatusCode != status {
				t.Fatalf("got %d, want %d", resp.StatusCode, status)
			}
			if got := resp.Header["Goto-Connect-Proxy-Host"]; !reflect.DeepEqual(got, []string{"proxy-host"}) {
				t.Errorf("%d: Goto-Connect-Proxy-Host = %q", status, got)
			}
			if got := resp.Header["Goto-Connect-Proxy-Port"]; !reflect.DeepEqual(got, []string{fmt.Sprint(h.port)}) {
				t.Errorf("%d: Goto-Connect-Proxy-Port = %q, want the port of the proxy", status, got)
			}
			if got := resp.Header.Get("Goto-Connect-Proxy-Remote-Address"); !strings.Contains(got, ":") {
				t.Errorf("%d: Goto-Connect-Proxy-Remote-Address = %q, want the address of the client", status, got)
			}
			// what the destination says about itself is kept, and is not taken for what the proxy says
			if got := resp.Header["Goto-Host"]; !reflect.DeepEqual(got, []string{"destination-host"}) {
				t.Errorf("%d: Goto-Host = %q, want the one of the destination", status, got)
			}
			if got := resp.Header["Goto-Upstream-Status"]; !reflect.DeepEqual(got, []string{`{"behind":200}`}) {
				t.Errorf("%d: Goto-Upstream-Status = %q, want the one of the destination", status, got)
			}
			// the status of the destination is told by target and endpoint, which is all that the CONNECT proxy says in its
			// header: what an HTTP proxy behind it said is in the header of that proxy, as it came
			want := []string{fmt.Sprintf(`{%q:{%q:%d}}`, dest, dest, status)}
			if got := resp.Header["Goto-Connect-Proxy-Upstream-Status"]; !reflect.DeepEqual(got, want) {
				t.Errorf("%d: Goto-Connect-Proxy-Upstream-Status = %q, want %q", status, got, want)
			}
			if got := resp.Header["Goto-Proxy-Upstream-Status"]; !reflect.DeepEqual(got, []string{`{"t":{"ep":["200"]}}`}) {
				t.Errorf("%d: Goto-Proxy-Upstream-Status = %q, want the one of the proxy behind the tunnel, as it came", status, got)
			}
			if took, err := time.ParseDuration(resp.Header.Get("Goto-Connect-Proxy-Took")); err != nil || took < 0 || took > 5*time.Second {
				t.Errorf("%d: Goto-Connect-Proxy-Took = %q, err %v", status, resp.Header.Get("Goto-Connect-Proxy-Took"), err)
			}
			if got := resp.Header["Goto-Connect-Proxy-Delay"]; len(got) != 0 {
				t.Errorf("%d: Goto-Connect-Proxy-Delay = %q, want none as the proxy added no delay", status, got)
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	})
}

// The time that the proxy takes goes from the first byte of the request to the head of the response, so it has the time
// that the destination takes.
func TestConnectReportsTimeOfHTTPExchanges(t *testing.T) {
	setViaLabels(t)
	dest := newStatusDestination(t)
	h := newConnectHarness(t, false)
	tc, answer := sendConnect(t, h.dial(), connectRequest(dest))
	if took, err := time.ParseDuration(answer.Header.Get("Goto-Connect-Proxy-Took")); err != nil || took < 0 {
		t.Errorf("the answer to CONNECT has Goto-Connect-Proxy-Took = %q, err %v", answer.Header.Get("Goto-Connect-Proxy-Took"), err)
	}
	if got := answer.Header["Goto-Connect-Proxy-Delay"]; len(got) != 0 {
		t.Errorf("the answer to CONNECT has Goto-Connect-Proxy-Delay = %q, want none", got)
	}
	c := &httpTunnel{t: t, tc: tc, br: bufio.NewReader(tc)}
	c.send(get("/status/200?sleep=80ms") + get("/status/200"))
	slow := c.readHead("GET")
	fast := c.readHead("GET")
	tookOf := func(what string, resp *http.Response) time.Duration {
		took, err := time.ParseDuration(resp.Header.Get("Goto-Connect-Proxy-Took"))
		if err != nil {
			t.Fatalf("%s: Goto-Connect-Proxy-Took = %q: %v", what, resp.Header.Get("Goto-Connect-Proxy-Took"), err)
		}
		return took
	}
	if took := tookOf("the slow one", slow); took < 80*time.Millisecond || took > 5*time.Second {
		t.Errorf("the slow one took %s, want at least the 80ms that the destination took", took)
	}
	// requests of a connection that wait for each other: the one that waited tells the time that it was waiting
	if took := tookOf("the one behind it", fast); took < 80*time.Millisecond {
		t.Errorf("the one behind took %s, want it to have waited for the slow one", took)
	}
}

// A response that the proxy can't tell the request of is told no time, and the rest is told all the same.
func TestConnectHasNoTimeForResponsesOfRequestsItLost(t *testing.T) {
	setViaLabels(t)
	dest := startServer(t, func(c net.Conn) {
		br := bufio.NewReader(c)
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			if line == "\r\n" {
				break
			}
		}
		io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")
	})
	h := newConnectHarness(t, false)
	c := openHTTPTunnel(t, h, dest.Addr())
	// Two lengths: the proxy can't follow the request
	c.send("GET /a HTTP/1.1\r\nContent-Length: 1\r\nContent-Length: 2\r\n\r\n")
	resp := c.readHead("GET")
	expectVia(t, "the response", resp, h.via())
	if got := resp.Header["Goto-Connect-Proxy-Took"]; len(got) != 0 {
		t.Errorf("Goto-Connect-Proxy-Took = %q, want none for a request that the proxy lost", got)
	}
	if got := resp.Header.Get("Goto-Connect-Proxy-Upstream-Status"); got == "" {
		t.Error("the response has no Goto-Connect-Proxy-Upstream-Status")
	}
}

func TestWithHeaders(t *testing.T) {
	got := string(withHeaders([]byte("HTTP/1.1 200 OK\r\nA: b\r\n\r\n"), []headerLine{{"X-One", "1"}, {"X-Two", "a\r\nb"}}))
	if want := "HTTP/1.1 200 OK\r\nA: b\r\nX-One: 1\r\nX-Two: ab\r\n\r\n"; got != want {
		t.Errorf("got %q, want %q, with the line breaks of values taken out", got, want)
	}
	if got := string(withHeaders([]byte("HTTP/1.1 204 No Content\n\n"), nil)); got != "HTTP/1.1 204 No Content\n\n" {
		t.Errorf("no headers changed the head to %q", got)
	}
}
