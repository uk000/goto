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

package intercept

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"goto/pkg/global"
	"goto/pkg/util"
)

var stubOnce sync.Once

func stubGlobalFuncs() {
	global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsListenerPresent = func(int) bool { return false }
	global.Funcs.IsListenerOpen = func(int) bool { return false }
	global.Funcs.GetListenerID = func(int) string { return "" }
	global.Funcs.GetListenerLabel = func(*http.Request) string { return "" }
	global.Funcs.GetListenerLabelForPort = func(int) string { return "" }
	util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
}

// newRequest gives a request with a request store, which is what a request has by the time it gets to the middlewares.
func newRequest(method, target string, body io.Reader) (*http.Request, *util.RequestStore) {
	stubOnce.Do(stubGlobalFuncs)
	req := httptest.NewRequest(method, target, body)
	_, req, rs := util.WithRequestStore(req, nil)
	return req, rs
}

// spy is a response writer that logs what is sent to it.
type spy struct {
	http.ResponseWriter
	mu    sync.Mutex
	calls []string
}

func newSpy() *spy { return &spy{ResponseWriter: httptest.NewRecorder()} }

func (s *spy) log(c string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, c)
}

func (s *spy) got() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.calls...)
}

func (s *spy) WriteHeader(code int) {
	s.log(fmt.Sprintf("WriteHeader(%d)", code))
	s.ResponseWriter.WriteHeader(code)
}

func (s *spy) Write(b []byte) (int, error) {
	s.log(fmt.Sprintf("Write(%s)", b))
	return s.ResponseWriter.Write(b)
}

func (s *spy) Flush() {
	s.log("Flush")
	s.ResponseWriter.(http.Flusher).Flush()
}

func (s *spy) rec() *httptest.ResponseRecorder { return s.ResponseWriter.(*httptest.ResponseRecorder) }

// plainWriter is a response writer that can't flush or hijack.
type plainWriter struct {
	header http.Header
	body   strings.Builder
}

func (p *plainWriter) Header() http.Header {
	if p.header == nil {
		p.header = http.Header{}
	}
	return p.header
}
func (p *plainWriter) Write(b []byte) (int, error) { return p.body.Write(b) }
func (p *plainWriter) WriteHeader(int)             {}

// hijackable is a response writer that gives a connection to whoever hijacks it.
type hijackable struct {
	*spy
	conn net.Conn
}

func (h *hijackable) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.log("Hijack")
	return h.conn, bufio.NewReadWriter(bufio.NewReader(h.conn), bufio.NewWriter(h.conn)), nil
}

func TestInterceptHoldsTheResponseUntilItProceeds(t *testing.T) {
	r, rs := newRequest("GET", "/x", nil)
	sp := newSpy()
	w, irw := WithIntercept(r, sp)
	w.WriteHeader(http.StatusCreated)
	if n, err := w.Write([]byte("hello")); n != 5 || err != nil {
		t.Errorf("Write = %d, %v, want 5 and no error", n, err)
	}
	w.Write([]byte(" world"))
	if got := sp.got(); len(got) != 0 {
		t.Fatalf("the writer was sent %v before Proceed, want nothing: the response is held", got)
	}
	if irw.StatusCode != http.StatusCreated || rs.StatusCode != http.StatusCreated || string(irw.Data) != "hello world" || irw.BodyLength != 11 {
		t.Errorf("status = %d, store status = %d, data = %q, length = %d, want 201, 201, the data that was written, and 11",
			irw.StatusCode, rs.StatusCode, irw.Data, irw.BodyLength)
	}
	irw.Proceed()
	if got, want := sp.got(), []string{"WriteHeader(201)", "Write(hello world)"}; !reflect.DeepEqual(got, want) {
		t.Errorf("after Proceed the writer was sent %v, want %v", got, want)
	}
	for _, h := range []string{"Goto-In-At", "Goto-Out-At", "Goto-Took"} {
		if sp.Header().Get(h) == "" {
			t.Errorf("header %s is not set after Proceed", h)
		}
	}
	irw.Proceed()
	if got := sp.got(); len(got) != 2 {
		t.Errorf("a second Proceed sent %v, want it to do nothing", got)
	}
	if !irw.Proceeded {
		t.Error("Proceeded is not set")
	}
}

func TestInterceptKeepsTheFirstStatus(t *testing.T) {
	r, rs := newRequest("GET", "/x", nil)
	sp := newSpy()
	w, irw := WithIntercept(r, sp)
	w.WriteHeader(http.StatusNotFound)
	w.WriteHeader(http.StatusInternalServerError)
	if irw.StatusCode != http.StatusNotFound || rs.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, store status = %d, want the first one, 404", irw.StatusCode, rs.StatusCode)
	}
	irw.Proceed()
	if got, want := sp.got(), []string{"WriteHeader(404)", "Write()"}; !reflect.DeepEqual(got, want) {
		t.Errorf("writer was sent %v, want %v", got, want)
	}
}

func TestInterceptResponseWithoutStatusIs200(t *testing.T) {
	r, rs := newRequest("GET", "/x", nil)
	sp := newSpy()
	_, irw := WithIntercept(r, sp)
	irw.Proceed()
	if irw.StatusCode != 200 || rs.StatusCode != 200 || sp.rec().Code != 200 {
		t.Errorf("status = %d, store status = %d, response = %d, want 200", irw.StatusCode, rs.StatusCode, sp.rec().Code)
	}
}

func TestInterceptThatIsNotHoldingSendsThrough(t *testing.T) {
	r, _ := newRequest("GET", "/x", nil)
	sp := newSpy()
	irw := NewInterceptResponseWriter(r, sp, false)
	irw.WriteHeader(http.StatusAccepted)
	irw.Write([]byte("now"))
	if got, want := sp.got(), []string{"WriteHeader(202)", "Write(now)"}; !reflect.DeepEqual(got, want) {
		t.Errorf("writer was sent %v, want %v right away", got, want)
	}
	if irw.StatusCode != http.StatusAccepted || irw.BodyLength != 3 {
		t.Errorf("status = %d, length = %d, want 202 and 3", irw.StatusCode, irw.BodyLength)
	}
}

func TestInterceptStreamsOnceFlushed(t *testing.T) {
	r, _ := newRequest("GET", "/x", nil)
	sp := newSpy()
	w, irw := WithIntercept(r, sp)
	w.Write([]byte("early"))
	w.(http.Flusher).Flush()
	if !irw.Chunked {
		t.Error("a flushed writer is not marked as chunked")
	}
	w.Write([]byte("-late"))
	// What was held goes first, and what comes after is sent as it comes.
	want := []string{"Flush", "Write(early)", "Write(-late)"}
	if got := sp.got(); !reflect.DeepEqual(got, want) {
		t.Errorf("writer was sent %v, want %v", got, want)
	}
	if body := sp.rec().Body.String(); body != "early-late" {
		t.Errorf("body = %q, want early-late", body)
	}
}

func TestInterceptThatHoldsChunkedKeepsHolding(t *testing.T) {
	r, _ := newRequest("GET", "/x", nil)
	sp := newSpy()
	w, irw := WithInterceptChunked(r, sp)
	w.Write([]byte("held"))
	w.(http.Flusher).Flush()
	w.Write([]byte("-also"))
	if irw.Chunked || len(sp.got()) != 0 {
		t.Errorf("chunked = %t, writer was sent %v, want everything held", irw.Chunked, sp.got())
	}
	if string(irw.Data) != "held-also" {
		t.Errorf("data = %q, want what was written", irw.Data)
	}
	irw.Proceed()
	if got, want := sp.got(), []string{"WriteHeader(200)", "Write(held-also)"}; !reflect.DeepEqual(got, want) {
		t.Errorf("after Proceed the writer was sent %v, want %v", got, want)
	}
}

func TestInterceptHijack(t *testing.T) {
	t.Run("hijacker", func(t *testing.T) {
		r, _ := newRequest("GET", "/x", nil)
		server, client := net.Pipe()
		defer server.Close()
		defer client.Close()
		h := &hijackable{spy: newSpy(), conn: server}
		w, irw := WithIntercept(r, h)
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil || conn != server || rw == nil {
			t.Fatalf("Hijack = %v, %v, %v, want the connection of the writer", conn, rw, err)
		}
		if !irw.Hijacked {
			t.Error("the writer is not marked as hijacked")
		}
		// What is written to a hijacked writer goes nowhere.
		if n, err := w.Write([]byte("lost")); n != 4 || err != nil {
			t.Errorf("Write after Hijack = %d, %v, want 4 and no error", n, err)
		}
		w.WriteHeader(500)
		if got, want := h.got(), []string{"Hijack"}; !reflect.DeepEqual(got, want) {
			t.Errorf("hijacked writer was sent %v, want %v", got, want)
		}
	})
	t.Run("connection of the request", func(t *testing.T) {
		stubOnce.Do(stubGlobalFuncs)
		server, client := net.Pipe()
		defer server.Close()
		defer client.Close()
		req := httptest.NewRequest("GET", "/x", nil)
		req = req.WithContext(util.WithConn(req.Context(), server))
		_, req, _ = util.WithRequestStore(req, nil)
		sp := newSpy()
		w, _ := WithIntercept(req, sp)
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil || conn != server || rw == nil {
			t.Errorf("Hijack = %v, %v, %v, want the connection of the request", conn, rw, err)
		}
	})
}

func TestInterceptPassesStateToTheWriterItWraps(t *testing.T) {
	r, _ := newRequest("GET", "/x", nil)
	sp := newSpy()
	_, outer := WithIntercept(r, sp)
	inner := NewInterceptResponseWriter(r, outer, true)
	if inner.parent == nil {
		t.Fatal("the writer of an intercepted writer does not know its parent")
	}
	inner.SetChunked()
	inner.SetHijacked()
	if !inner.Chunked || !inner.Hijacked || !outer.Chunked || !outer.Hijacked {
		t.Errorf("inner chunked = %t hijacked = %t, outer chunked = %t hijacked = %t, want all of them set", inner.Chunked, inner.Hijacked, outer.Chunked, outer.Hijacked)
	}
	// A writer with no parent is the end of the line.
	standalone := NewInterceptResponseWriter(r, sp.ResponseWriter, true)
	standalone.SetChunked()
	standalone.SetHijacked()
	if !standalone.Chunked || !standalone.Hijacked {
		t.Error("a writer without a parent did not set its own state")
	}
}

func TestFlushWriter(t *testing.T) {
	t.Run("writer that can't flush", func(t *testing.T) {
		if fw := NewFlushWriter(&plainWriter{}); fw != nil {
			t.Errorf("NewFlushWriter = %v, want nil for a writer that can't flush", fw)
		}
		if fw := CreateOrGetFlushWriter(&plainWriter{}); fw != nil {
			t.Errorf("CreateOrGetFlushWriter = %v, want nil for a writer that can't flush", fw)
		}
	})
	t.Run("writer that flushes", func(t *testing.T) {
		sp := newSpy()
		fw := NewFlushWriter(sp)
		if n, err := fw.Write([]byte("abc")); n != 3 || err != nil {
			t.Errorf("Write = %d, %v", n, err)
		}
		fw.Flush()
		if got, want := sp.got(), []string{"Write(abc)", "Flush", "Flush"}; !reflect.DeepEqual(got, want) {
			t.Errorf("writer was sent %v, want %v: a write is flushed, and so is a flush", got, want)
		}
	})
	t.Run("a failed write is not flushed", func(t *testing.T) {
		sp := newSpy()
		fw := &FlushWriterImpl{w: failingWriter{}, flusher: sp}
		if _, err := fw.Write([]byte("x")); err == nil {
			t.Error("the error of the writer was lost")
		}
		if len(sp.got()) != 0 {
			t.Errorf("a failed write was followed by %v", sp.got())
		}
	})
	t.Run("intercepting writer becomes chunked", func(t *testing.T) {
		r, _ := newRequest("GET", "/x", nil)
		_, irw := WithIntercept(r, newSpy())
		if fw := NewFlushWriter(irw); fw == nil || !irw.Chunked {
			t.Errorf("flush writer = %v, chunked = %t, want one, and the writer to be chunked", fw, irw.Chunked)
		}
	})
	t.Run("existing flush writer is reused", func(t *testing.T) {
		fw := NewFlushWriter(newSpy())
		if got := CreateOrGetFlushWriter(fw); got != fw {
			t.Error("CreateOrGetFlushWriter made another writer for a flush writer")
		}
		sp := newSpy()
		if got := CreateOrGetFlushWriter(sp); got == nil {
			t.Error("CreateOrGetFlushWriter made no writer for a writer that flushes")
		}
	})
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestChanWriter(t *testing.T) {
	c := make(chan []byte, 3)
	sp := newSpy()
	cw := NewChanWriter(c, sp)
	if n, err := cw.Write([]byte("one")); n != 3 || err != nil {
		t.Errorf("Write = %d, %v, want 3 and no error", n, err)
	}
	cw.Write([]byte("two"))
	if got := string(<-c) + string(<-c); got != "onetwo" {
		t.Errorf("channel got %q, want onetwo", got)
	}
	cw.Header().Set("X-A", "1")
	if sp.Header().Get("X-A") != "1" {
		t.Error("headers of the chan writer are not the ones of the writer that it wraps")
	}
	if cw.(*ChanWriterImpl).Chan() != c {
		t.Error("Chan() is not the channel that it was made with")
	}
	if len(sp.got()) != 0 {
		t.Errorf("the wrapped writer was sent %v, want the data to go to the channel only", sp.got())
	}
}

func TestBodyTracker(t *testing.T) {
	rr := util.NewReReader(io.NopCloser(strings.NewReader("abcdef")))
	b := &BodyTracker{rr}
	buf := make([]byte, 6)
	if n, _ := io.ReadFull(b, buf); n != 6 || string(buf) != "abcdef" {
		t.Fatalf("Read = %q", buf[:n])
	}
	b.Rewind()
	again, _ := io.ReadAll(b)
	if string(again) != "abcdef" {
		t.Errorf("after Rewind = %q, want the body again", again)
	}
	if err := b.Close(); err != nil {
		t.Errorf("Close = %v", err)
	}
	// A body that can't be rewound is left alone.
	plain := &BodyTracker{io.NopCloser(strings.NewReader("xyz"))}
	plain.Rewind()
	if got, _ := io.ReadAll(plain); string(got) != "xyz" {
		t.Errorf("body = %q, want it unchanged", got)
	}
	r, _ := newRequest("POST", "/x", strings.NewReader("abc"))
	trackRequestBody(r)
	if _, ok := r.Body.(*BodyTracker); !ok {
		t.Errorf("body = %T, want a tracker", r.Body)
	}
}

func TestInterceptWriterLookup(t *testing.T) {
	r, rs := newRequest("GET", "/x", nil)
	sp := newSpy()
	if got := GetInterceptWriter(r); got != nil {
		t.Errorf("GetInterceptWriter before any = %v, want nil", got)
	}
	w, irw := GetOrCreateInterceptWriter(r, sp)
	if irw == nil || w != http.ResponseWriter(irw) || rs.InterceptResponseWriter != irw {
		t.Fatalf("GetOrCreateInterceptWriter = %v, %v, want a new writer that is kept by the request store", w, irw)
	}
	w2, irw2 := GetOrCreateInterceptWriter(r, sp)
	if irw2 != irw || w2 != w {
		t.Error("a second GetOrCreateInterceptWriter made another writer")
	}
	if got := GetInterceptWriter(r); got != irw {
		t.Errorf("GetInterceptWriter = %v, want the writer that was made", got)
	}
	if util.GetInterceptResponseWriter(r) != irw {
		t.Error("the util lookup does not find the writer")
	}
}

func TestWithInterceptAndStatus(t *testing.T) {
	r, rs := newRequest("GET", "/x", nil)
	sp := newSpy()
	w, irw := WithInterceptAndStatus(r, sp, http.StatusServiceUnavailable, true)
	if irw.StatusCode != http.StatusServiceUnavailable || rs.StatusCode != http.StatusServiceUnavailable || !irw.HoldChunked {
		t.Errorf("status = %d, store status = %d, hold chunked = %t, want 503, 503, true", irw.StatusCode, rs.StatusCode, irw.HoldChunked)
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("body"))
	irw.Proceed()
	if sp.rec().Code != http.StatusServiceUnavailable || sp.rec().Body.String() != "body" {
		t.Errorf("response = %d %q, want 503 with the body", sp.rec().Code, sp.rec().Body.String())
	}
	// A status of 0 forces nothing.
	r2, _ := newRequest("GET", "/x", nil)
	_, irw2 := WithInterceptAndStatus(r2, newSpy(), 0, false)
	if irw2.StatusCode != 0 || irw2.HoldChunked {
		t.Errorf("status = %d, hold chunked = %t, want nothing forced", irw2.StatusCode, irw2.HoldChunked)
	}
}

func TestWithHeadersIntercept(t *testing.T) {
	r, rs := newRequest("GET", "/x", nil)
	sp := newSpy()
	w, hw := WithHeadersIntercept(r, sp)
	if hw == nil || rs.HeadersInterceptRW != hw {
		t.Fatalf("writer = %v, want one, which the request store keeps", hw)
	}
	if hw.HeadersSent() {
		t.Error("headers are sent before any is set")
	}
	w.Header().Set("X-A", "1")
	if !hw.HeadersSent() {
		t.Error("headers are not sent after one is set")
	}
	// Requests that are not traffic are left alone.
	r2, rs2 := newRequest("GET", "/x", nil)
	rs2.IsKnownNonTraffic = true
	w2, hw2 := WithHeadersIntercept(r2, sp)
	if hw2 != nil || w2 != http.ResponseWriter(sp) || rs2.HeadersInterceptRW != nil {
		t.Errorf("writer = %v, %v, want the request to be left alone", w2, hw2)
	}
}

func TestPostInterceptHeaders(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(*http.Request, *util.RequestStore)
		header string
		value  string
	}{
		{"plain", func(r *http.Request, rs *util.RequestStore) {}, "Goto-Response-Status", "418"},
		{"proxied", func(r *http.Request, rs *util.RequestStore) { rs.ProxiedRequest = true }, "Proxy-Goto-Response-Status", "418"},
		{"tunnel", func(r *http.Request, rs *util.RequestStore) {
			rs.IsTunnelRequest = true
			r.Header["Goto-Requested-Tunnel"] = []string{"t1", "t2"}
		}, "Goto-Tunnel", "t1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, rs := newRequest("GET", "/x", nil)
			rs.StatusCode = 418
			tt.setup(r, rs)
			rr := httptest.NewRecorder()
			postIntercept(rr, r)
			if got := rr.Header().Get(tt.header); got != tt.value {
				t.Errorf("%s = %q, want %q (headers: %v)", tt.header, got, tt.value, rr.Header())
			}
		})
	}
	// Every value of the requested tunnels is handed on.
	r, rs := newRequest("GET", "/x", nil)
	rs.IsTunnelRequest = true
	r.Header["Goto-Requested-Tunnel"] = []string{"t1", "t2"}
	rr := httptest.NewRecorder()
	postIntercept(rr, r)
	if got := rr.Header()["Goto-Tunnel"]; !reflect.DeepEqual(got, []string{"t1", "t2"}) {
		t.Errorf("Goto-Tunnel = %v, want the requested tunnels", got)
	}
}

func TestInterceptMiddleware(t *testing.T) {
	var order []string
	mk := func(name string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, name)
			w.Header().Add("X-"+name, "1")
		})
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "handler")
		w.Header().Set("X-Handler", "1")
		w.WriteHeader(http.StatusTeapot)
		fmt.Fprint(w, "short and stout")
	})
	h := IntereceptMiddleware(mk("pre"), mk("post"))(handler)
	body := strings.NewReader("request body that the handler doesn't read")
	r, rs := newRequest("POST", "/x", body)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)

	if want := []string{"pre", "handler", "post"}; !reflect.DeepEqual(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
	if rr.Code != http.StatusTeapot || rr.Body.String() != "short and stout" {
		t.Errorf("response = %d %q, want 418 and the body", rr.Code, rr.Body.String())
	}
	for h, want := range map[string]string{"X-Pre": "1", "X-Post": "1", "X-Handler": "1", "Goto-Response-Status": "418"} {
		if got := rr.Header().Get(h); got != want {
			t.Errorf("%s = %q, want %q", h, got, want)
		}
	}
	if rr.Header().Get("Goto-In-At") == "" {
		t.Error("the response has no timing headers")
	}
	if rs.StatusCode != http.StatusTeapot {
		t.Errorf("store status = %d, want 418", rs.StatusCode)
	}
	if body.Len() != 0 {
		t.Errorf("%d bytes of the request body are left unread, want it discarded", body.Len())
	}
	if rs.InterceptResponseWriter == nil {
		t.Error("the request store does not have the intercepting writer")
	}
}

func TestInterceptMiddlewareWithoutHooks(t *testing.T) {
	h := IntereceptMiddleware(nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") }))
	r, rs := newRequest("GET", "/x", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if rr.Code != 200 || rr.Body.String() != "ok" || rr.Header().Get("Goto-Response-Status") != "200" || rs.StatusCode != 200 {
		t.Errorf("response = %d %q status header %q, store status %d, want 200 ok with status 200", rr.Code, rr.Body.String(), rr.Header().Get("Goto-Response-Status"), rs.StatusCode)
	}
}

func TestInterceptMiddlewareForChunkedRequests(t *testing.T) {
	h := IntereceptMiddleware(nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "a")
		w.(http.Flusher).Flush()
		fmt.Fprint(w, "b")
	}))
	r, rs := newRequest("GET", "/x", nil)
	rs.InterceptChunked = true
	sp := newSpy()
	h.ServeHTTP(sp, r)
	if got, want := sp.got(), []string{"WriteHeader(200)", "Write(ab)"}; !reflect.DeepEqual(got, want) {
		t.Errorf("writer was sent %v, want %v: the response is held whole", got, want)
	}
	if irw := GetInterceptWriter(r); irw == nil || !irw.HoldChunked {
		t.Errorf("writer = %v, want one that holds chunks", irw)
	}
}

func TestInterceptMiddlewareNested(t *testing.T) {
	inner := IntereceptMiddleware(nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, "inner")
	}))
	outer := IntereceptMiddleware(nil, nil)(inner)
	r, _ := newRequest("GET", "/x", nil)
	rr := httptest.NewRecorder()
	outer.ServeHTTP(rr, r)
	if rr.Code != http.StatusAccepted || rr.Body.String() != "inner" {
		t.Errorf("response = %d %q, want the response of the inner handler once", rr.Code, rr.Body.String())
	}
}

// Responses that are intercepted reach the clients of a real server, streamed ones as they are produced.
func TestInterceptOverARealServer(t *testing.T) {
	stubOnce.Do(stubGlobalFuncs)
	release := make(chan struct{})
	handler := IntereceptMiddleware(nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/stream":
			fmt.Fprint(w, "first\n")
			w.(http.Flusher).Flush()
			<-release
			fmt.Fprint(w, "second\n")
		default:
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, "whole")
		}
	}))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, r, _ = util.WithRequestStore(r, w)
		handler.ServeHTTP(w, r)
	}))
	defer srv.Close()
	client := &http.Client{Timeout: 10 * time.Second}

	resp, err := client.Get(srv.URL + "/whole")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted || string(b) != "whole" || resp.Header.Get("Goto-Response-Status") != "202" {
		t.Errorf("response = %d %q with status header %q, want 202 whole", resp.StatusCode, b, resp.Header.Get("Goto-Response-Status"))
	}

	resp, err = client.Get(srv.URL + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || line != "first\n" {
		t.Fatalf("first chunk = %q, %v: it must arrive before the handler is done", line, err)
	}
	close(release)
	rest, _ := io.ReadAll(resp.Body)
	if string(rest) != "second\n" {
		t.Errorf("rest = %q, want the second chunk", rest)
	}
}

func TestInterceptIsSafeForConcurrentRequests(t *testing.T) {
	stubOnce.Do(stubGlobalFuncs)
	handler := IntereceptMiddleware(nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var code int
		fmt.Sscan(r.URL.Query().Get("code"), &code)
		w.WriteHeader(code)
		fmt.Fprintf(w, "code-%d", code)
	}))
	var wg sync.WaitGroup
	results := make([]string, 20)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, _ := newRequest("GET", fmt.Sprintf("/x?code=%d", 200+i), nil)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, r)
			results[i] = fmt.Sprintf("%d:%s:%s", rr.Code, rr.Body.String(), rr.Header().Get("Goto-Response-Status"))
		}()
	}
	wg.Wait()
	for i, got := range results {
		if want := fmt.Sprintf("%d:code-%d:%d", 200+i, 200+i, 200+i); got != want {
			t.Errorf("request %d = %q, want %q", i, got, want)
		}
	}
}
