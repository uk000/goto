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

package stream

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
)

var (
	stubOnce  sync.Once
	routeOnce sync.Once
	// label is what the listener of the requests is labeled as, which generated payloads start with.
	label = ""
)

func stubGlobalFuncs() {
	global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsListenerPresent = func(int) bool { return false }
	global.Funcs.IsListenerOpen = func(int) bool { return false }
	global.Funcs.GetListenerID = func(int) string { return "" }
	global.Funcs.GetListenerLabel = func(*http.Request) string { return label }
	global.Funcs.GetListenerLabelForPort = func(int) string { return label }
	util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
}

func setup(t *testing.T, listenerLabel string) http.Handler {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
	routeOnce.Do(func() { Middleware.SetRoutes(nil) })
	label = listenerLabel
	t.Cleanup(func() { label = "" })
	return middleware.RootRouters["/stream"]
}

func newRequest(method, target, body string, headers ...string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, 8080))
	_, req, _ = util.WithRequestStore(req, nil)
	return req
}

func serve(h http.Handler, method, target, body string, headers ...string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, newRequest(method, target, body, headers...))
	return rr
}

func hdr(rr *httptest.ResponseRecorder, name string) string { return rr.Result().Header.Get(name) }

// chunks splits a streamed body into its chunks, each of which is followed by a newline.
func chunks(t *testing.T, body string) []string {
	t.Helper()
	if body == "" {
		return nil
	}
	if !strings.HasSuffix(body, "\n") {
		t.Fatalf("body %q does not end with a newline", body)
	}
	return strings.Split(strings.TrimSuffix(body, "\n"), "\n")
}

func TestStreamsGeneratedChunksOfTheGivenSize(t *testing.T) {
	h := setup(t, "")
	rr := serve(h, "GET", "/stream?count=3&delay=1ms&chunksize=8", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	got := chunks(t, rr.Body.String())
	if len(got) != 3 {
		t.Fatalf("body %q has %d chunks, want 3", rr.Body.String(), len(got))
	}
	for i, c := range got {
		if len(c) != 8 {
			t.Errorf("chunk %d = %q, want 8 characters", i, c)
		}
	}
	for name, want := range map[string]string{
		constants.HeaderGotoChunkCount:      "3",
		constants.HeaderGotoChunkLength:     "8",
		constants.HeaderGotoChunkDelay:      "1ms",
		constants.HeaderGotoStreamLength:    "24",
		constants.HeaderContentType:         "plain/text",
		constants.HeaderXContentTypeOptions: "nosniff",
	} {
		if v := hdr(rr, name); v != want {
			t.Errorf("header %s = %q, want %q", name, v, want)
		}
	}
	if v := hdr(rr, constants.HeaderGotoStreamDuration); v != "" {
		t.Errorf("header %s = %q, want none when no duration is given", constants.HeaderGotoStreamDuration, v)
	}
	if !rr.Flushed {
		t.Error("the stream must be flushed")
	}
}

func TestGeneratedChunksStartWithTheLabelOfTheListener(t *testing.T) {
	h := setup(t, "LBL")
	rr := serve(h, "GET", "/stream?count=2&delay=1ms&chunksize=8", "")
	got := chunks(t, rr.Body.String())
	if len(got) != 2 {
		t.Fatalf("body %q has %d chunks, want 2", rr.Body.String(), len(got))
	}
	for i, c := range got {
		if len(c) != 8 || !strings.HasPrefix(c, "LBL") {
			t.Errorf("chunk %d = %q, want 8 characters starting with LBL", i, c)
		}
	}
	// Generated chunks are the same one, repeated.
	if got[0] != got[1] {
		t.Errorf("chunks %q and %q, want the same chunk repeated", got[0], got[1])
	}
}

func TestStreamsTheGivenPayloadInChunks(t *testing.T) {
	h := setup(t, "")
	for _, method := range []string{"PUT", "POST"} {
		rr := serve(h, method, "/stream?count=10&delay=1ms&chunksize=4", "abcdefghij")
		// The payload is streamed once, and the last chunk has what is left of it.
		if want := "abcd\nefgh\nij\n"; rr.Body.String() != want {
			t.Errorf("%s body = %q, want %q", method, rr.Body.String(), want)
		}
	}
}

func TestStreamsAPayloadThatFitsTheChunkAsItIs(t *testing.T) {
	h := setup(t, "")
	rr := serve(h, "POST", "/stream?count=3&delay=1ms&chunksize=4", "abcd")
	if want := "abcd\n"; rr.Body.String() != want {
		t.Errorf("body = %q, want %q", rr.Body.String(), want)
	}
}

func TestStreamStopsAfterTheCountOfChunks(t *testing.T) {
	h := setup(t, "")
	rr := serve(h, "POST", "/stream?count=2&delay=1ms&chunksize=1", "abcdef")
	if want := "a\nb\n"; rr.Body.String() != want {
		t.Errorf("body = %q, want %q", rr.Body.String(), want)
	}
}

func TestDurationAndDelayGiveTheCountOfChunks(t *testing.T) {
	h := setup(t, "")
	// 5ms at one chunk per ms: the chunk at 0 and one more for each ms.
	rr := serve(h, "GET", "/stream?duration=5ms&delay=1ms&chunksize=4", "")
	if got := chunks(t, rr.Body.String()); len(got) != 6 {
		t.Errorf("body %q has %d chunks, want 6", rr.Body.String(), len(got))
	}
	if v := hdr(rr, constants.HeaderGotoChunkCount); v != "6" {
		t.Errorf("header %s = %q, want 6", constants.HeaderGotoChunkCount, v)
	}
	if v := hdr(rr, constants.HeaderGotoStreamDuration); v != "5ms" {
		t.Errorf("header %s = %q, want 5ms", constants.HeaderGotoStreamDuration, v)
	}
	if v := hdr(rr, constants.HeaderGotoStreamLength); v != "24" {
		t.Errorf("header %s = %q, want 24", constants.HeaderGotoStreamLength, v)
	}
}

func TestDurationPicksFromTheGivenRange(t *testing.T) {
	h := setup(t, "")
	rr := serve(h, "GET", "/stream?duration=3ms-6ms&delay=1ms&chunksize=2", "")
	n := len(chunks(t, rr.Body.String()))
	if n < 4 || n > 7 {
		t.Errorf("body %q has %d chunks, want between 4 and 7", rr.Body.String(), n)
	}
	d, err := time.ParseDuration(hdr(rr, constants.HeaderGotoStreamDuration))
	if err != nil || d < 3*time.Millisecond || d > 6*time.Millisecond {
		t.Errorf("header %s = %q, want a duration between 3ms and 6ms", constants.HeaderGotoStreamDuration, hdr(rr, constants.HeaderGotoStreamDuration))
	}
}

func TestZeroCountStreamsTenChunks(t *testing.T) {
	h := setup(t, "")
	rr := serve(h, "GET", "/stream?count=0&delay=1ms&chunksize=2", "")
	if got := chunks(t, rr.Body.String()); len(got) != 10 {
		t.Errorf("body %q has %d chunks, want the default 10", rr.Body.String(), len(got))
	}
}

func TestContentTypeOfTheRequestIsUsed(t *testing.T) {
	h := setup(t, "")
	rr := serve(h, "POST", "/stream?count=1&delay=1ms&chunksize=2", "ab", constants.HeaderContentType, "application/x-test")
	if v := hdr(rr, constants.HeaderContentType); v != "application/x-test" {
		t.Errorf("content type = %q, want the one of the request", v)
	}
}

func TestStreamMethods(t *testing.T) {
	h := setup(t, "")
	for _, method := range []string{"GET", "PUT", "POST"} {
		if rr := serve(h, method, "/stream?count=1&delay=1ms&chunksize=2", ""); rr.Code != http.StatusOK {
			t.Errorf("%s status = %d, want 200", method, rr.Code)
		}
	}
	if rr := serve(h, "DELETE", "/stream?count=1&delay=1ms&chunksize=2", ""); rr.Code == http.StatusOK {
		t.Error("DELETE must not be served")
	}
}

func TestChunksAreSentWithTheGivenDelayInBetween(t *testing.T) {
	h := setup(t, "")
	start := time.Now()
	rr := serve(h, "GET", "/stream?count=3&delay=30ms&chunksize=2", "")
	if got := chunks(t, rr.Body.String()); len(got) != 3 {
		t.Fatalf("body %q has %d chunks, want 3", rr.Body.String(), len(got))
	}
	// There is a delay between chunks, and none after the last: two of them.
	if elapsed := time.Since(start); elapsed < 60*time.Millisecond {
		t.Errorf("3 chunks took %v, want at least 2 delays of 30ms", elapsed)
	}
}

// nonFlusher is a response writer that cannot flush, which a stream cannot be sent on.
type nonFlusher struct {
	h      http.Header
	status int
	body   strings.Builder
}

func (n *nonFlusher) Header() http.Header         { return n.h }
func (n *nonFlusher) WriteHeader(status int)      { n.status = status }
func (n *nonFlusher) Write(b []byte) (int, error) { return n.body.Write(b) }

func TestStreamNeedsAWriterThatFlushes(t *testing.T) {
	setup(t, "")
	w := &nonFlusher{h: http.Header{}}
	streamResponse(w, newRequest("GET", "/stream?count=1&delay=1ms&chunksize=2", ""))
	if w.status != http.StatusInternalServerError || !strings.Contains(w.body.String(), "Cannot stream") {
		t.Errorf("response = %d %q, want 500 Cannot stream", w.status, w.body.String())
	}
}

// failingWriter flushes, and fails to write after a number of writes.
type failingWriter struct {
	httptest.ResponseRecorder
	okWrites int
	writes   int
}

func (f *failingWriter) Write(b []byte) (int, error) {
	f.writes++
	if f.writes > f.okWrites {
		return 0, errors.New("client is gone")
	}
	return f.ResponseRecorder.Write(b)
}

func TestStreamStopsWhenTheClientIsGone(t *testing.T) {
	h := setup(t, "")
	w := &failingWriter{ResponseRecorder: *httptest.NewRecorder(), okWrites: 1}
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(w, newRequest("GET", "/stream?count=50&delay=1ms&chunksize=2", ""))
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream did not stop after a failed write")
	}
	// The chunk that worked, the one that failed, the report of the failure (which fails too), and what is left to
	// write when the response is completed: not one write for each of the 50 chunks.
	if w.writes < 2 || w.writes > 5 {
		t.Errorf("writes = %d, want the stream to stop at the first failed write", w.writes)
	}
}

// deadlineConn records the write deadlines that it is set.
type deadlineConn struct {
	net.Conn
	mu        sync.Mutex
	deadlines []time.Time
}

func (c *deadlineConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deadlines = append(c.deadlines, t)
	return nil
}

func TestStreamClearsTheWriteDeadlineOfTheConnection(t *testing.T) {
	h := setup(t, "")
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	conn := &deadlineConn{Conn: server}
	req := newRequest("GET", "/stream?count=1&delay=1ms&chunksize=2", "")
	req = req.WithContext(context.WithValue(req.Context(), util.ConnectionKey, net.Conn(conn)))
	h.ServeHTTP(httptest.NewRecorder(), req)
	conn.mu.Lock()
	defer conn.mu.Unlock()
	if len(conn.deadlines) != 1 || !conn.deadlines[0].IsZero() {
		t.Errorf("write deadlines = %v, want it cleared once, as a stream outlasts any deadline", conn.deadlines)
	}
}

func TestConcurrentStreamsDoNotShareChunks(t *testing.T) {
	h := setup(t, "")
	const streams = 8
	results := make([]string, streams)
	var wg sync.WaitGroup
	for i := 0; i < streams; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, newRequest("POST", "/stream?count=10&delay=1ms&chunksize=3", strings.Repeat(string(rune('a'+i)), 7)))
			results[i] = rr.Body.String()
		}()
	}
	wg.Wait()
	for i, got := range results {
		c := string(rune('a' + i))
		if want := strings.Repeat(c, 3) + "\n" + strings.Repeat(c, 3) + "\n" + c + "\n"; got != want {
			t.Errorf("stream %d = %q, want %q", i, got, want)
		}
	}
}
