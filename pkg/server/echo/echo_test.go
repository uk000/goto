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

package echo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/util"

	"github.com/gorilla/mux"
	"golang.org/x/net/websocket"
)

const (
	testPort  = 8089
	testLabel = "echo-test-label"
)

var stubOnce sync.Once

func stubGlobalFuncs() {
	global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsListenerPresent = func(int) bool { return false }
	global.Funcs.IsListenerOpen = func(int) bool { return false }
	global.Funcs.GetListenerID = func(int) string { return "" }
	global.Funcs.GetListenerLabel = func(*http.Request) string { return testLabel }
	global.Funcs.GetListenerLabelForPort = func(int) string { return testLabel }
	util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
}

func newRouter() *mux.Router {
	stubOnce.Do(stubGlobalFuncs)
	r := mux.NewRouter()
	Middleware.SetRoutes(r)
	return r
}

func newRequest(method, target, body string, headers ...string) *http.Request {
	stubOnce.Do(stubGlobalFuncs)
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Add(headers[i], headers[i+1])
	}
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, testPort))
	_, req, _ = util.WithRequestStore(req, nil)
	return req
}

func serve(h http.Handler, method, target, body string, headers ...string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, newRequest(method, target, body, headers...))
	return rr
}

func decode(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d %q, want 200", rr.Code, rr.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("body %q is not JSON: %v", rr.Body.String(), err)
	}
	return out
}

func TestEchoRespondsWithTheRequest(t *testing.T) {
	r := newRouter()
	for _, target := range []string{"/echo", "/echo/"} {
		rr := serve(r, "GET", target+"?a=b&c=d", "", "X-Test", "v1", "X-Test", "v2")
		got := decode(t, rr)
		if ct := rr.Header().Get(constants.HeaderContentType); !strings.Contains(ct, "json") {
			t.Errorf("content type = %q, want JSON", ct)
		}
		for key, want := range map[string]any{
			"Request-Host":                 "example.com",
			"Request-URI":                  target + "?a=b&c=d",
			"Request-Method":               "GET",
			"Request-Protocol":             "HTTP/1.1",
			"Request-Query":                "a=b&c=d",
			"Request-PayloadSize":          0.0,
			"Response-PayloadSize":         0.0,
			constants.HeaderGotoPort:       float64(testPort),
			constants.HeaderGotoListener:   testLabel,
			constants.HeaderViaGoto:        testLabel,
			constants.HeaderGotoTLS:        false,
			constants.HeaderGotoMTLS:       false,
			constants.HeaderGotoHost:       global.Self.HostLabel,
			constants.HeaderGotoClientCert: "",
			constants.HeaderGotoServerCert: "",
		} {
			if got[key] != want {
				t.Errorf("GET %s: %s = %v, want %v", target, key, got[key], want)
			}
		}
		if _, ok := got[constants.HeaderGotoSNI]; ok {
			t.Errorf("GET %s: the SNI must only be given for TLS", target)
		}
		headers, _ := got["headers"].(map[string]any)
		if v, _ := headers["Request-X-Test"].([]any); !reflect.DeepEqual(v, []any{"v1", "v2"}) {
			t.Errorf("GET %s: headers = %v, want the request headers with the Request- prefix, all of their values", target, headers)
		}
	}
}

func TestEchoAnswersAnyMethod(t *testing.T) {
	r := newRouter()
	for _, method := range []string{"GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS"} {
		got := decode(t, serve(r, method, "/echo", "payload"))
		if got["Request-Method"] != method {
			t.Errorf("%s: Request-Method = %v", method, got["Request-Method"])
		}
	}
}

func TestEchoHeadersReportsTheHeadersAndTheInfo(t *testing.T) {
	r := newRouter()
	got := decode(t, serve(r, "GET", "/echo/headers", "", "X-Test", "v1"))
	headers, _ := got["RequestHeaders"].(map[string]any)
	if v, _ := headers["X-Test"].([]any); !reflect.DeepEqual(v, []any{"v1"}) {
		t.Errorf("RequestHeaders = %v, want the request headers as they are", headers)
	}
	info, _ := got["Goto-Info"].(map[string]any)
	if info["Request-URI"] != "/echo/headers" || info[constants.HeaderGotoListener] != testLabel {
		t.Errorf("Goto-Info = %v, want the echo of the request", info)
	}
}

func TestEchoBodyGivesBackTheBody(t *testing.T) {
	r := newRouter()
	for _, body := range []string{"", "hello", "line 1\nline 2\n", strings.Repeat("0123456789", 20000)} {
		rr := serve(r, "POST", "/echo/body", body)
		if rr.Code != http.StatusOK || rr.Body.String() != body {
			t.Errorf("body of %d bytes: response = %d with %d bytes, want it given back as it is", len(body), rr.Code, rr.Body.Len())
		}
	}
}

func TestEchoStreamGivesBackTheBodyAndFlushesIt(t *testing.T) {
	r := newRouter()
	for _, body := range []string{"", "one\ntwo\nthree\n", "one\ntwo\nlast without a newline"} {
		rr := serve(r, "POST", "/echo/stream", body, "X-Test", "v")
		if rr.Code != http.StatusOK || rr.Body.String() != body {
			t.Errorf("body %q: response = %d %q, want it given back as it is", body, rr.Code, rr.Body.String())
		}
		if !rr.Flushed {
			t.Errorf("body %q: the response must be flushed", body)
		}
		// The request is echoed in the headers, which are sent before the body is read.
		h := rr.Result().Header
		if fmt.Sprint(h["Request-URI"]) != "[/echo/stream]" || fmt.Sprint(h["Request-X-Test"]) != "[v]" || fmt.Sprint(h["Request-Host"]) != "[example.com]" {
			t.Errorf("body %q: headers = %v, want the request echoed", body, h)
		}
	}
}

// flushedBody has what has been flushed to the client by the time that each line was read from the request.
type flushedBody struct {
	*httptest.ResponseRecorder
	mu        sync.Mutex
	flushedAt []int
}

func (f *flushedBody) Flush() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.flushedAt = append(f.flushedAt, f.ResponseRecorder.Body.Len())
	f.ResponseRecorder.Flush()
}

func TestEchoStreamFlushesAfterEachLine(t *testing.T) {
	r := newRouter()
	w := &flushedBody{ResponseRecorder: httptest.NewRecorder()}
	r.ServeHTTP(w, newRequest("POST", "/echo/stream", "a\nbb\nccc\n"))
	w.mu.Lock()
	defer w.mu.Unlock()
	// Flushed for the headers, and after each of the lines is written, and never with something left unflushed at the end.
	var distinct []int
	for _, n := range w.flushedAt {
		if len(distinct) == 0 || distinct[len(distinct)-1] != n {
			distinct = append(distinct, n)
		}
	}
	if want := []int{0, 2, 5, 9}; !reflect.DeepEqual(distinct, want) {
		t.Errorf("flushed with %v bytes written, want a flush at each of %v", w.flushedAt, want)
	}
}

// failingFlusher fails to write after a number of writes.
type failingFlusher struct {
	*httptest.ResponseRecorder
	okWrites int
	writes   int
}

func (f *failingFlusher) Write(b []byte) (int, error) {
	f.writes++
	if f.writes > f.okWrites {
		return 0, errors.New("client is gone")
	}
	return f.ResponseRecorder.Write(b)
}

func TestEchoStreamStopsWhenTheClientIsGone(t *testing.T) {
	r := newRouter()
	w := &failingFlusher{ResponseRecorder: httptest.NewRecorder(), okWrites: 1}
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.ServeHTTP(w, newRequest("POST", "/echo/stream", strings.Repeat("line\n", 1000)))
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream did not stop after a failed write")
	}
	if w.writes < 2 || w.writes > 4 {
		t.Errorf("writes = %d, want the stream to stop at the first failed write", w.writes)
	}
}

func TestWebSocketEchoSendsTheHeadersThenEchoesMessages(t *testing.T) {
	r := newRouter()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, req, _ = util.WithRequestStore(req, w)
		r.ServeHTTP(w, req)
	}))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/echo/ws"
	cfg, err := websocket.NewConfig(wsURL, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Header.Set("X-Test", "ws-value")
	ws, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatalf("cannot connect: %v", err)
	}
	defer ws.Close()
	_ = ws.SetDeadline(time.Now().Add(10 * time.Second))

	// The first message is the headers of the request that opened the connection.
	var first string
	if err := websocket.Message.Receive(ws, &first); err != nil {
		t.Fatalf("cannot read the headers message: %v", err)
	}
	if !strings.Contains(first, "X-Test") || !strings.Contains(first, "ws-value") {
		t.Errorf("first message = %q, want the headers of the request", first)
	}
	for _, msg := range []string{"hello", "second message", strings.Repeat("x", 3000)} {
		if err := websocket.Message.Send(ws, msg); err != nil {
			t.Fatalf("cannot send %d bytes: %v", len(msg), err)
		}
		var echoed string
		if err := websocket.Message.Receive(ws, &echoed); err != nil {
			t.Fatalf("cannot read the echo of %d bytes: %v", len(msg), err)
		}
		if echoed != msg {
			t.Errorf("echo of %d bytes = %d bytes, want the message back", len(msg), len(echoed))
		}
	}
	// A message that is bigger than what is read at a time may come back in more than one message, but all of it does.
	big := strings.Repeat("0123456789", 1000)
	if err := websocket.Message.Send(ws, big); err != nil {
		t.Fatalf("cannot send %d bytes: %v", len(big), err)
	}
	var echoed strings.Builder
	for echoed.Len() < len(big) {
		var part string
		if err := websocket.Message.Receive(ws, &part); err != nil {
			t.Fatalf("cannot read the echo of %d bytes after %d: %v", len(big), echoed.Len(), err)
		}
		echoed.WriteString(part)
	}
	if echoed.String() != big {
		t.Errorf("echo of %d bytes = %d bytes that are not the message", len(big), echoed.Len())
	}
}

func TestGetEchoResponseHasTheGivenValues(t *testing.T) {
	stubOnce.Do(stubGlobalFuncs)
	headers := map[string][]string{"X-A": {"1", "2"}, "X-B": {"3"}}
	got := GetEchoResponse("lbl", "10.0.0.1:99", "host.example", "/u?q=1", "POST", "HTTP/2.0", "q=1", "", 9090, 12, 34, headers, false, false, "server-cert", "client-cert")
	want := map[string]interface{}{
		"Request-Host": "host.example", "Request-URI": "/u?q=1", "Request-Method": "POST", "Request-Protocol": "HTTP/2.0",
		"Request-Query": "q=1", "Request-PayloadSize": 12, "Response-PayloadSize": 34,
		constants.HeaderGotoRemoteAddress: "10.0.0.1:99", constants.HeaderGotoHost: global.Self.HostLabel,
		constants.HeaderGotoListener: "lbl", constants.HeaderGotoPort: 9090, constants.HeaderGotoTLS: false, constants.HeaderGotoMTLS: false,
		constants.HeaderViaGoto: testLabel, "headers": map[string][]string{"Request-X-A": {"1", "2"}, "Request-X-B": {"3"}},
		constants.HeaderGotoClientCert: "client-cert", constants.HeaderGotoServerCert: "server-cert",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("response = %v, want %v", got, want)
	}
}

func TestGetEchoResponseHasTheSNIOfTLSRequests(t *testing.T) {
	stubOnce.Do(stubGlobalFuncs)
	for _, tt := range []struct {
		name         string
		sni          string
		isTLS        bool
		wantSNI      bool
		wantSNIValue string
	}{
		{"plain", "", false, false, ""},
		{"tls without sni", "", true, true, ""},
		{"tls with sni", "sni.example", true, true, "sni.example"},
		{"sni of a request that is not tls here", "sni.example", false, true, "sni.example"},
	} {
		got := GetEchoResponse("l", "a", "h", "/", "GET", "HTTP/1.1", "", tt.sni, 1, 0, 0, nil, tt.isTLS, tt.isTLS, "", "")
		v, ok := got[constants.HeaderGotoSNI]
		if ok != tt.wantSNI || (ok && v != tt.wantSNIValue) {
			t.Errorf("%s: SNI = %v (present %v), want %q (present %v)", tt.name, v, ok, tt.wantSNIValue, tt.wantSNI)
		}
		if got[constants.HeaderGotoTLS] != tt.isTLS || got[constants.HeaderGotoMTLS] != tt.isTLS {
			t.Errorf("%s: TLS = %v, mTLS = %v, want %v", tt.name, got[constants.HeaderGotoTLS], got[constants.HeaderGotoMTLS], tt.isTLS)
		}
	}
}

func TestGetEchoResponseFromRSFillsTheListenerLabel(t *testing.T) {
	stubOnce.Do(stubGlobalFuncs)
	rs := &util.RequestStore{RequestPortNum: 7070, RequestHost: "h", RequestURI: "/x", RequestMethod: "GET", RequestProtocol: "HTTP/1.1",
		RequestHeaders: map[string][]string{"X-One": {"1"}}, DownstreamAddr: "1.2.3.4:5"}
	got := GetEchoResponseFromRS(rs)
	if got[constants.HeaderGotoListener] != testLabel || rs.ListenerLabel != testLabel {
		t.Errorf("listener = %v, store label = %q, want the label of the port", got[constants.HeaderGotoListener], rs.ListenerLabel)
	}
	// A label that is there is not looked up again.
	rs.ListenerLabel = "given"
	if got := GetEchoResponseFromRS(rs); got[constants.HeaderGotoListener] != "given" {
		t.Errorf("listener = %v, want the label of the store", got[constants.HeaderGotoListener])
	}
	if _, ok := got[constants.HeaderGotoTargetURL]; ok {
		t.Error("the tunnel headers must only be given for tunnel requests")
	}
}

func TestGetEchoResponseFromRSHasTheTunnelHeadersOfTunnelRequests(t *testing.T) {
	stubOnce.Do(stubGlobalFuncs)
	rs := &util.RequestStore{IsTunnelRequest: true, ListenerLabel: "l", RequestHeaders: map[string][]string{
		constants.HeaderGotoTargetURL: {"http://target"}, constants.HeaderGotoTunnelHost: {"tunnel-host"}, constants.HeaderViaGotoTunnel: {"via-1", "via-2"},
	}}
	got := GetEchoResponseFromRS(rs)
	for key, want := range map[string][]string{
		constants.HeaderGotoTargetURL: {"http://target"}, constants.HeaderGotoTunnelHost: {"tunnel-host"}, constants.HeaderViaGotoTunnel: {"via-1", "via-2"},
	} {
		if !reflect.DeepEqual(got[key], want) {
			t.Errorf("%s = %v, want %v", key, got[key], want)
		}
	}
	// A tunnel request without the headers has them empty.
	rs = &util.RequestStore{IsTunnelRequest: true, ListenerLabel: "l"}
	if got := GetEchoResponseFromRS(rs); !reflect.DeepEqual(got[constants.HeaderGotoTargetURL], []string(nil)) {
		t.Errorf("%s = %#v, want it empty", constants.HeaderGotoTargetURL, got[constants.HeaderGotoTargetURL])
	}
}

func TestGetEchoResponseWithAddendumAddsAndOverrides(t *testing.T) {
	stubOnce.Do(stubGlobalFuncs)
	rs := &util.RequestStore{ListenerLabel: "l", RequestURI: "/x"}
	got := GetEchoResponseWithAddendum(rs, map[string]any{"Extra": 1, "Request-URI": "/overridden"})
	if got["Extra"] != 1 || got["Request-URI"] != "/overridden" || got[constants.HeaderGotoListener] != "l" {
		t.Errorf("response = %v, want the addendum added to the echo, and its values over it", got)
	}
	if plain := GetEchoResponseWithAddendum(rs, nil); plain["Request-URI"] != "/x" {
		t.Errorf("response = %v, want the echo as it is without an addendum", plain)
	}
}

func TestEchoOfConcurrentRequestsIsOfEachRequest(t *testing.T) {
	r := newRouter()
	const requests = 32
	results := make([]string, requests)
	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rr := serve(r, "GET", fmt.Sprintf("/echo?n=%d", i), "", "X-N", fmt.Sprint(i))
			var out map[string]any
			if err := json.Unmarshal(rr.Body.Bytes(), &out); err == nil {
				results[i] = fmt.Sprintf("%v %v", out["Request-URI"], out["headers"])
			}
		}()
	}
	wg.Wait()
	for i, got := range results {
		if want := fmt.Sprintf("/echo?n=%d map[Request-X-N:[%d]]", i, i); got != want {
			t.Errorf("request %d echoed %q, want %q", i, got, want)
		}
	}
}

func TestEchoBodyOfConcurrentRequestsIsOfEachRequest(t *testing.T) {
	r := newRouter()
	const requests = 16
	results := make([]string, requests)
	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rr := serve(r, "POST", "/echo/body", strings.Repeat(fmt.Sprint(i%10), 1000+i))
			results[i] = rr.Body.String()
		}()
	}
	wg.Wait()
	for i, got := range results {
		if want := strings.Repeat(fmt.Sprint(i%10), 1000+i); got != want {
			t.Errorf("request %d got %d bytes back, want its own %d", i, len(got), len(want))
		}
	}
}

var _ = io.EOF
