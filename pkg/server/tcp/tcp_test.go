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

package tcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goto/pkg/global"
	"goto/pkg/server/middleware"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
	stubOnce sync.Once
	// The configs, the connections and their history are kept per port, so every test uses a port of its own. The port is
	// only the key that the config is stored by: the connections of the tests come in on listeners of their own.
	portSeq atomic.Int32
	// presentPorts are the ports that the stubbed global.Funcs.IsListenerPresent tells have a listener.
	presentPorts sync.Map
	// closedPorts are the ports that the stubbed global.Funcs.IsListenerOpen tells are not open.
	closedPorts sync.Map
	routerOnce  sync.Once
	adminRouter *mux.Router
)

// The server logs every step of every connection. The log is only worth reading when a test fails, and then it is
// there with GOTO_TEST_LOGS set.
func TestMain(m *testing.M) {
	if os.Getenv("GOTO_TEST_LOGS") == "" {
		log.SetOutput(io.Discard)
	}
	os.Exit(m.Run())
}

func stubGlobalFuncs() {
	global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsListenerPresent = func(port int) bool { _, ok := presentPorts.Load(port); return ok }
	// A connection ends as soon as its listener is not open, so the listeners of the tests have to be.
	global.Funcs.IsListenerOpen = func(port int) bool { _, closed := closedPorts.Load(port); return !closed }
	global.Funcs.GetListenerID = func(port int) string { return strconv.Itoa(port) + "-1" }
	global.Funcs.GetListenerLabel = func(*http.Request) string { return "" }
	global.Funcs.GetListenerLabelForPort = func(int) string { return "" }
	util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
}

func newPort(t *testing.T) int {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
	port := int(42000 + portSeq.Add(1))
	presentPorts.Store(port, true)
	t.Cleanup(func() {
		presentPorts.Delete(port)
		lock.Lock()
		delete(tcpListeners, port)
		delete(connectionHistory, port)
		lock.Unlock()
	})
	return port
}

// configure stores the config of a new port, the way a listener does.
func configure(t *testing.T, cfg *TCPConfig) int {
	t.Helper()
	port := newPort(t)
	if cfg == nil {
		cfg = &TCPConfig{}
	}
	if _, msg := InitTCPConfig(port, cfg); msg != "" {
		t.Fatalf("InitTCPConfig(%d) rejected the config: %s", port, msg)
	}
	return port
}

func storedConfig(t *testing.T, port int) TCPConfig {
	t.Helper()
	lock.RLock()
	defer lock.RUnlock()
	c := tcpListeners[port]
	if c == nil {
		t.Fatalf("port %d has no config", port)
	}
	return *c
}

func listenerID(port int) string {
	return global.Funcs.GetListenerID(port)
}

func activeCount(port int) int {
	lock.RLock()
	defer lock.RUnlock()
	return len(activeConnections[listenerID(port)])
}

// waitFor polls until the condition holds, and fails the test if it doesn't within the time.
func waitFor(t *testing.T, what string, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", d, what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// server serves the connections that a loopback listener accepts with ServeClientConnection, the way the TCP server does.
type server struct {
	t       *testing.T
	port    int
	ln      net.Listener
	reqID   atomic.Int32
	done    chan struct{}
	conns   sync.Mutex
	clients []net.Conn
}

// startServer stores the config of a new port and serves the connections that come to a listener on the loopback. The
// server and the connections that are left are closed when the test ends, and the test fails if any connection is still
// being served after that.
func startServer(t *testing.T, cfg *TCPConfig) *server {
	t.Helper()
	port := configure(t, cfg)
	return serve(t, port)
}

func serve(t *testing.T, port int) *server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &server{t: t, port: port, ln: ln, done: make(chan struct{})}
	go func() {
		defer close(s.done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			ServeClientConnection(port, int(s.reqID.Add(1)), conn)
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		<-s.done
		s.conns.Lock()
		for _, c := range s.clients {
			c.Close()
		}
		s.conns.Unlock()
		CloseListenerConnections(listenerID(port))
		deadline := time.Now().Add(5 * time.Second)
		for activeCount(port) > 0 && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if n := activeCount(port); n != 0 {
			t.Errorf("port %d still has %d connections that are being served after the test", port, n)
		}
	})
	return s
}

// dial connects a client to the server. Every read and write of the client has a deadline.
func (s *server) dial() net.Conn {
	s.t.Helper()
	conn, err := net.DialTimeout("tcp", s.ln.Addr().String(), 3*time.Second)
	if err != nil {
		s.t.Fatalf("dial: %v", err)
	}
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	s.conns.Lock()
	s.clients = append(s.clients, conn)
	s.conns.Unlock()
	return conn
}

// waitConnections waits for the server to have accepted and registered the given number of connections.
func (s *server) waitConnections(n int) {
	s.t.Helper()
	waitFor(s.t, "connections to be registered", 5*time.Second, func() bool { return int(s.reqID.Load()) >= n && historyCount(s.port) >= n })
}

// waitIdle waits for all the connections to be done being served.
func (s *server) waitIdle() {
	s.t.Helper()
	waitFor(s.t, "connections to finish", 5*time.Second, func() bool { return activeCount(s.port) == 0 })
}

func historyCount(port int) int {
	lock.RLock()
	defer lock.RUnlock()
	return len(connectionHistory[port])
}

// statuses gives the status of the connections of a port as the admin API reports it: as JSON, in the order of
// the connections.
func statuses(t *testing.T, port int) []*ConnectionStatus {
	t.Helper()
	lock.RLock()
	hs := append([]*ConnectionHistory{}, connectionHistory[port]...)
	lock.RUnlock()
	out := make([]*ConnectionStatus, 0, len(hs))
	for _, h := range hs {
		b, err := json.Marshal(h.Status)
		if err != nil {
			t.Fatalf("marshal status: %v", err)
		}
		st := &ConnectionStatus{}
		if err := json.Unmarshal(b, st); err != nil {
			t.Fatalf("unmarshal status %s: %v", b, err)
		}
		out = append(out, st)
	}
	return out
}

func histories(t *testing.T, port int) []*ConnectionHistory {
	t.Helper()
	lock.RLock()
	defer lock.RUnlock()
	return append([]*ConnectionHistory{}, connectionHistory[port]...)
}

// readAll reads until the connection is closed by the server.
func readAll(t *testing.T, conn net.Conn) string {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(8 * time.Second))
	b, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("reading until the server closes the connection failed after %q: %v", b, err)
	}
	return string(b)
}

// readN reads exactly n bytes.
func readN(t *testing.T, conn net.Conn, n int) string {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(8 * time.Second))
	b := make([]byte, n)
	if _, err := io.ReadFull(conn, b); err != nil {
		t.Fatalf("reading %d bytes failed: %v", n, err)
	}
	return string(b)
}

// readUntil reads until what has been read satisfies the condition.
func readUntil(t *testing.T, conn net.Conn, cond func(string) bool) string {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(8 * time.Second))
	var got bytes.Buffer
	buf := make([]byte, 512)
	for !cond(got.String()) {
		n, err := conn.Read(buf)
		got.Write(buf[:n])
		if err != nil {
			if cond(got.String()) {
				break
			}
			t.Fatalf("read failed after %q: %v", got.String(), err)
		}
	}
	return got.String()
}

func write(t *testing.T, conn net.Conn, s string) {
	t.Helper()
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte(s)); err != nil {
		t.Fatalf("write failed: %v", err)
	}
}

func closeWrite(t *testing.T, conn net.Conn) {
	t.Helper()
	if err := conn.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatalf("CloseWrite: %v", err)
	}
}

// expectNoData checks that the server sends nothing within the time.
func expectNoData(t *testing.T, conn net.Conn, d time.Duration) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(d))
	buf := make([]byte, 16)
	n, err := conn.Read(buf)
	if n > 0 {
		t.Fatalf("expected no data, got %q", buf[:n])
	}
	if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
		t.Fatalf("expected the read to time out, got %v", err)
	}
}

// initAdmin makes the router that the middleware keeps for the TCP APIs, and the stubs they need, ready.
func initAdmin(t *testing.T) {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
	routerOnce.Do(func() {
		setRoutes(nil)
		adminRouter = middleware.RootRouters["/server"]
	})
	if adminRouter == nil {
		t.Fatal("the TCP routes were not registered")
	}
}

// serveAdmin serves a request of the TCP admin APIs, and can be used from any goroutine once initAdmin was called.
func serveAdmin(method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, 8080))
	_, req, _ = util.WithRequestStore(req, nil)
	rr := httptest.NewRecorder()
	adminRouter.ServeHTTP(rr, req)
	return rr
}

// admin serves the TCP admin APIs through the router that the middleware keeps for them.
func admin(t *testing.T, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	initAdmin(t)
	return serveAdmin(method, target, body)
}

func adminOK(t *testing.T, method, target, body string) string {
	t.Helper()
	rr := admin(t, method, target, body)
	if rr.Code != http.StatusOK {
		t.Fatalf("%s %s = %d %q, want 200", method, target, rr.Code, rr.Body.String())
	}
	return rr.Body.String()
}

// ---- the config ----

func TestInitTCPConfigStoresConfigWithDefaults(t *testing.T) {
	stubOnce.Do(stubGlobalFuncs)
	port := newPort(t)
	cfg, msg := InitTCPConfig(port, nil)
	if msg != "" {
		t.Fatalf("nil config rejected: %s", msg)
	}
	if cfg == nil || cfg.Port != port {
		t.Fatalf("config = %+v, want one for port %d", cfg, port)
	}
	got := storedConfig(t, port)
	if got.ListenerID != listenerID(port) {
		t.Errorf("ListenerID = %q, want %q", got.ListenerID, listenerID(port))
	}
	if !got.Echo {
		t.Error("a config without a mode must default to echo")
	}
	if got.ReadTimeoutD != 30*time.Second || got.WriteTimeoutD != 30*time.Second || got.ConnectTimeoutD != 30*time.Second || got.ConnIdleTimeoutD != 30*time.Second {
		t.Errorf("default timeouts = %v/%v/%v/%v, want 30s each", got.ReadTimeoutD, got.WriteTimeoutD, got.ConnectTimeoutD, got.ConnIdleTimeoutD)
	}
	if got.ConnectionLifeD != 0 || got.ResponseDelayD != 0 || got.EchoResponseDelayD != 0 {
		t.Errorf("default life/delays = %v/%v/%v, want 0", got.ConnectionLifeD, got.ResponseDelayD, got.EchoResponseDelayD)
	}
	if got.EchoResponseSize != 10 {
		t.Errorf("EchoResponseSize = %d, want the default 10", got.EchoResponseSize)
	}
	if historyCount(port) != 0 {
		t.Errorf("a new port has history %d", historyCount(port))
	}
}

func TestInitTCPConfigConnectionLifeMakesSilentLife(t *testing.T) {
	port := configure(t, &TCPConfig{ConnectionLife: "5s"})
	got := storedConfig(t, port)
	if !got.SilentLife || got.Echo {
		t.Errorf("a config with a connection life and no mode: silentLife=%t echo=%t, want silentLife only", got.SilentLife, got.Echo)
	}
	if got.ConnectionLifeD != 5*time.Second {
		t.Errorf("ConnectionLifeD = %v, want 5s", got.ConnectionLifeD)
	}
}

func TestInitTCPConfigKeepsExplicitMode(t *testing.T) {
	for name, cfg := range map[string]*TCPConfig{
		"payload":      {Payload: true},
		"stream":       {Stream: true},
		"conversation": {Conversation: true},
		"validate len": {ValidatePayloadLength: true},
		"validate ctt": {ValidatePayloadContent: true},
		"silent":       {SilentLife: true},
		"close first":  {CloseAtFirstByte: true},
	} {
		t.Run(name, func(t *testing.T) {
			port := configure(t, cfg)
			if storedConfig(t, port).Echo {
				t.Error("echo must not be turned on over an explicit mode")
			}
		})
	}
}

func TestInitTCPConfigRejectsInvalidDurations(t *testing.T) {
	stubOnce.Do(stubGlobalFuncs)
	for name, c := range map[string]*TCPConfig{
		"readTimeout":       {ReadTimeout: "-1s"},
		"writeTimeout":      {WriteTimeout: "-1s"},
		"connectTimeout":    {ConnectTimeout: "-1s"},
		"connIdleTimeout":   {ConnIdleTimeout: "-1s"},
		"connectionLife":    {ConnectionLife: "-1s"},
		"responseDelay":     {ResponseDelay: "-1s"},
		"echoResponseDelay": {EchoResponseDelay: "-1s"},
	} {
		t.Run(name, func(t *testing.T) {
			port := newPort(t)
			_, msg := InitTCPConfig(port, c)
			if !strings.Contains(msg, "Invalid") {
				t.Fatalf("msg = %q, want a rejection", msg)
			}
			if getTCPConfig(port) != nil {
				t.Error("a config that was rejected must not be stored")
			}
		})
	}
}

func TestInitTCPConfigAccumulatesAllRejections(t *testing.T) {
	stubOnce.Do(stubGlobalFuncs)
	port := newPort(t)
	_, msg := InitTCPConfig(port, &TCPConfig{ReadTimeout: "-1s", ConnectionLife: "-2s", ResponseDelay: "-3s"})
	for _, want := range []string{"read timeout", "connection life", "response delay"} {
		if !strings.Contains(msg, want) {
			t.Errorf("msg %q doesn't tell about the invalid %s", msg, want)
		}
	}
}

func TestInitTCPConfigReplacesConfigOfPortInPlace(t *testing.T) {
	port := configure(t, &TCPConfig{Echo: true, EchoResponseSize: 4})
	first := getTCPConfig(port)
	InitTCPConfig(port, &TCPConfig{Stream: true, StreamChunkSize: "5", StreamChunkCount: 2, StreamChunkDelay: "1ms"})
	second := getTCPConfig(port)
	if first != second {
		t.Error("the config of a port that is reconfigured must stay the same object, which the listener holds")
	}
	got := storedConfig(t, port)
	if !got.Stream || got.Echo || got.StreamChunkSizeV != 5 {
		t.Errorf("config after reconfiguring = stream %t echo %t chunk %d, want the new one", got.Stream, got.Echo, got.StreamChunkSizeV)
	}
}

func TestConfigureStreamDefaults(t *testing.T) {
	c := &TCPConfig{Stream: true}
	c.configureStream()
	if c.StreamChunkSizeV != 100 || c.StreamChunkCount != 10 || c.StreamChunkDelayD != 100*time.Millisecond ||
		c.StreamDurationD != time.Second || c.StreamPayloadSizeV != 1000 {
		t.Errorf("stream defaults = size %d count %d delay %v duration %v payload %d, want 100/10/100ms/1s/1000",
			c.StreamChunkSizeV, c.StreamChunkCount, c.StreamChunkDelayD, c.StreamDurationD, c.StreamPayloadSizeV)
	}
}

func TestConfigureStreamNoDefaultsWhenNotStreaming(t *testing.T) {
	c := &TCPConfig{}
	c.configureStream()
	if c.StreamChunkSizeV != 0 || c.StreamChunkCount != 0 || c.StreamChunkDelayD != 0 || c.StreamDurationD != 0 {
		t.Errorf("a listener that doesn't stream got stream values: size %d count %d delay %v duration %v",
			c.StreamChunkSizeV, c.StreamChunkCount, c.StreamChunkDelayD, c.StreamDurationD)
	}
}

func TestConfigureStreamParamsDerivations(t *testing.T) {
	tests := []struct {
		name                       string
		payload, chunk, dur, delay string
		count                      int
		wantSize, wantCount        int
		wantDelay, wantDur         time.Duration
		wantPayload                int
		streaming                  bool
	}{
		{name: "payload chunk duration", payload: "1000", chunk: "100", dur: "1s", wantSize: 100, wantCount: 10, wantDelay: 100 * time.Millisecond, wantDur: time.Second, wantPayload: 1000},
		{name: "chunk count delay", chunk: "10", count: 5, delay: "20ms", wantSize: 10, wantCount: 5, wantDelay: 20 * time.Millisecond, wantDur: 100 * time.Millisecond, wantPayload: 50},
		{name: "payload count delay", payload: "100", count: 4, delay: "10ms", wantSize: 25, wantCount: 4, wantDelay: 10 * time.Millisecond, wantDur: 40 * time.Millisecond, wantPayload: 100},
		{name: "chunk duration delay", chunk: "10", dur: "100ms", delay: "10ms", wantSize: 10, wantCount: 10, wantDelay: 10 * time.Millisecond, wantDur: 100 * time.Millisecond, wantPayload: 100},
		{name: "size suffix", payload: "2K", count: 2, delay: "1ms", wantSize: 1000, wantCount: 2, wantDelay: time.Millisecond, wantDur: 2 * time.Millisecond, wantPayload: 2000},
		{name: "streaming defaults", streaming: true, wantSize: 100, wantCount: 10, wantDelay: 100 * time.Millisecond, wantDur: time.Second, wantPayload: 1000},
		{name: "negative values are none", payload: "-5", chunk: "-5", dur: "-1s", delay: "-1s", count: -3, streaming: true, wantSize: 100, wantCount: 10, wantDelay: 100 * time.Millisecond, wantDur: time.Second, wantPayload: 1000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &TCPConfig{Stream: tc.streaming}
			c.configureStreamParams(tc.payload, tc.chunk, tc.dur, tc.delay, tc.count)
			if c.StreamChunkSizeV != tc.wantSize || c.StreamChunkCount != tc.wantCount || c.StreamChunkDelayD != tc.wantDelay ||
				c.StreamDurationD != tc.wantDur || c.StreamPayloadSizeV != tc.wantPayload {
				t.Errorf("size %d count %d delay %v duration %v payload %d, want %d/%d/%v/%v/%d",
					c.StreamChunkSizeV, c.StreamChunkCount, c.StreamChunkDelayD, c.StreamDurationD, c.StreamPayloadSizeV,
					tc.wantSize, tc.wantCount, tc.wantDelay, tc.wantDur, tc.wantPayload)
			}
			if c.StreamChunkSize != strconv.Itoa(c.StreamChunkSizeV) || c.StreamChunkDelay != c.StreamChunkDelayD.String() || c.StreamDuration != c.StreamDurationD.String() {
				t.Errorf("text of the stream config %q/%q/%q doesn't match its values", c.StreamChunkSize, c.StreamChunkDelay, c.StreamDuration)
			}
		})
	}
}

func TestComputeFunctions(t *testing.T) {
	if got := computeChunkCount(100, 10, 0, 0); got != 10 {
		t.Errorf("computeChunkCount(payload,chunk) = %d, want 10", got)
	}
	if got := computeChunkCount(0, 0, 10*time.Millisecond, time.Second); got != 100 {
		t.Errorf("computeChunkCount(duration,delay) = %d, want 100", got)
	}
	if got := computeChunkCount(0, 0, 0, 0); got != 0 {
		t.Errorf("computeChunkCount() = %d, want 0", got)
	}
	if got := computeChunkDelay(0, 0, 4, time.Second); got != 250*time.Millisecond {
		t.Errorf("computeChunkDelay(count) = %v, want 250ms", got)
	}
	if got := computeChunkDelay(100, 10, 0, time.Second); got != 100*time.Millisecond {
		t.Errorf("computeChunkDelay(payload,chunk) = %v, want 100ms", got)
	}
	if got := computeChunkDelay(0, 0, 0, 0); got != 0 {
		t.Errorf("computeChunkDelay() = %v, want 0", got)
	}
	if got := computeChunkSize(100, 4, 0, 0); got != 25 {
		t.Errorf("computeChunkSize(count) = %d, want 25", got)
	}
	if got := computeChunkSize(100, 0, 10*time.Millisecond, 100*time.Millisecond); got != 10 {
		t.Errorf("computeChunkSize(duration,delay) = %d, want 10", got)
	}
	if got := computePayloadSize(10, 4, 0, 0); got != 40 {
		t.Errorf("computePayloadSize(count) = %d, want 40", got)
	}
	if got := computePayloadSize(10, 0, 10*time.Millisecond, 100*time.Millisecond); got != 100 {
		t.Errorf("computePayloadSize(duration,delay) = %d, want 100", got)
	}
	if got := computeStreamDuration(0, 0, 4, 10*time.Millisecond); got != 40*time.Millisecond {
		t.Errorf("computeStreamDuration(count) = %v, want 40ms", got)
	}
	if got := computeStreamDuration(100, 10, 0, 10*time.Millisecond); got != 100*time.Millisecond {
		t.Errorf("computeStreamDuration(payload,chunk) = %v, want 100ms", got)
	}
	if got := computeStreamDuration(100, 10, 0, 0); got != 0 {
		t.Errorf("computeStreamDuration without delay = %v, want 0", got)
	}
}

func TestIsMode(t *testing.T) {
	tests := []struct {
		cfg  TCPConfig
		mode string
		want bool
	}{
		{TCPConfig{Payload: true}, "payload", true},
		{TCPConfig{Payload: true}, "echo", false},
		{TCPConfig{Conversation: true}, "Conversation", true},
		{TCPConfig{Echo: true}, "ECHO", true},
		{TCPConfig{Stream: true}, "stream", true},
		{TCPConfig{ValidatePayloadLength: true}, "payload", true},
		{TCPConfig{ValidatePayloadContent: true}, "PayloadValidation", false},
		{TCPConfig{SilentLife: true}, "silentlife", true},
		{TCPConfig{CloseAtFirstByte: true}, "closeatfirstbyte", true},
		{TCPConfig{}, "echo", false},
		{TCPConfig{Echo: true, Stream: true}, "stream", false},
	}
	for _, tc := range tests {
		if got := isMode(&tc.cfg, tc.mode); got != tc.want {
			t.Errorf("isMode(%+v, %q) = %t, want %t", tc.cfg, tc.mode, got, tc.want)
		}
	}
}

func TestTurnOffAllModes(t *testing.T) {
	c := &TCPConfig{Payload: true, Conversation: true, Echo: true, Stream: true, ValidatePayloadLength: true,
		ValidatePayloadContent: true, SilentLife: true, CloseAtFirstByte: true, KeepOpen: true}
	c.turnOffAllModes()
	if c.Payload || c.Conversation || c.Echo || c.Stream || c.ValidatePayloadLength || c.ValidatePayloadContent || c.SilentLife || c.CloseAtFirstByte {
		t.Errorf("modes left on: %+v", c)
	}
	if !c.KeepOpen {
		t.Error("turning the modes off must not touch the other settings")
	}
}

func TestModeOrder(t *testing.T) {
	tests := []struct {
		cfg  TCPConfig
		want string
	}{
		{TCPConfig{Payload: true, Stream: true, Echo: true}, Response},
		{TCPConfig{Stream: true, Conversation: true}, Stream},
		{TCPConfig{Conversation: true, ValidatePayloadLength: true}, Conversation},
		{TCPConfig{ValidatePayloadContent: true, ConnectionLifeD: time.Second}, PayloadValidation},
		{TCPConfig{ConnectionLifeD: time.Second, CloseAtFirstByte: true}, SilentLife},
		{TCPConfig{CloseAtFirstByte: true}, CloseAtFirstByte},
		{TCPConfig{Echo: true}, Echo},
		{TCPConfig{}, Echo},
	}
	for _, tc := range tests {
		if got := tc.cfg.mode(); got != tc.want {
			t.Errorf("mode(%+v) = %q, want %q", tc.cfg, got, tc.want)
		}
	}
}

func TestConnectionStatusJSON(t *testing.T) {
	st := &ConnectionStatus{Port: 9, ListenerID: "9-1", RequestID: 3, TotalBytesRead: 7, ClientClosed: true, WriteErrors: 2}
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("status is not valid JSON: %v: %s", err, b)
	}
	if m["port"] != float64(9) || m["listenerID"] != "9-1" || m["requestID"] != float64(3) || m["totalBytesRead"] != float64(7) ||
		m["clientClosed"] != true || m["writeErrors"] != float64(2) {
		t.Errorf("status JSON = %s", b)
	}
	if _, ok := m["mu"]; ok {
		t.Errorf("the lock must not be part of the status JSON: %s", b)
	}
	// and as a part of what the APIs report
	b, err = json.Marshal(&ConnectionHistory{Config: &TCPConfig{Echo: true}, Status: st})
	if err != nil || !strings.Contains(string(b), `"requestID":3`) || !strings.Contains(string(b), `"echo":true`) {
		t.Errorf("history JSON = %s, %v", b, err)
	}
}

// ---- serving connections ----

func TestServeClientConnectionWithoutConfigClosesConnection(t *testing.T) {
	stubOnce.Do(stubGlobalFuncs)
	port := int(42000 + portSeq.Add(1))
	server, client := net.Pipe()
	defer client.Close()
	if ServeClientConnection(port, 1, server) {
		t.Fatal("a connection to a port without config must not be served")
	}
	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := client.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("read on the client of a connection that cannot be served = %v, want EOF as the server closed it", err)
	}
}

func TestServeClientConnectionRegistersAndRemovesConnection(t *testing.T) {
	s := startServer(t, &TCPConfig{Echo: true, EchoResponseSize: 3})
	conn := s.dial()
	write(t, conn, "abc")
	if got := readN(t, conn, 3); got != "abc" {
		t.Fatalf("echo = %q, want abc", got)
	}
	if n := activeCount(s.port); n != 1 {
		t.Errorf("active connections = %d, want 1", n)
	}
	hs := histories(t, s.port)
	if len(hs) != 1 || hs[0].Status.RequestID != 1 || hs[0].Status.Port != s.port || hs[0].Status.ListenerID != listenerID(s.port) {
		t.Fatalf("history = %+v", hs)
	}
	conn.Close()
	s.waitIdle()
	st := statuses(t, s.port)[0]
	if !st.Closed || !st.ClientClosed || st.ServerClosed || st.ErrorClosed {
		t.Errorf("status after the client closed = closed %t clientClosed %t serverClosed %t errorClosed %t, want the client to have closed",
			st.Closed, st.ClientClosed, st.ServerClosed, st.ErrorClosed)
	}
	if st.ConnCloseTime == "" || st.FirstByteInAt == "" || st.FirstByteOutAt == "" {
		t.Errorf("status misses times: %+v", st)
	}
	if st.TotalBytesRead != 3 || st.TotalBytesSent != 3 {
		t.Errorf("bytes read/sent = %d/%d, want 3/3", st.TotalBytesRead, st.TotalBytesSent)
	}
	if st.TotalReads < 1 || st.TotalWrites != 1 {
		t.Errorf("reads/writes = %d/%d", st.TotalReads, st.TotalWrites)
	}
}

func TestServeClientConnectionConfigIsCopiedPerConnection(t *testing.T) {
	s := startServer(t, &TCPConfig{Echo: true, EchoResponseSize: 2})
	c1 := s.dial()
	write(t, c1, "ab")
	readN(t, c1, 2)
	// a change of the config after the connection came in doesn't reach the connection
	adminOK(t, "POST", "/server/tcp/"+strconv.Itoa(s.port)+"/mode/stream=true", "")
	hs := histories(t, s.port)
	if len(hs) != 1 || !hs[0].Config.Echo || hs[0].Config.Stream {
		t.Fatalf("config of the first connection changed with the port's: %+v", hs[0].Config)
	}
	write(t, c1, "cd")
	if got := readN(t, c1, 2); got != "cd" {
		t.Errorf("the first connection stopped echoing after the port was reconfigured: %q", got)
	}
}

func TestServeClientConnectionReportsSilentLifeMode(t *testing.T) {
	s := startServer(t, &TCPConfig{ConnectionLife: "10s", SilentLife: false, Echo: true})
	conn := s.dial()
	s.waitConnections(1)
	hs := histories(t, s.port)
	if !hs[0].Config.SilentLife {
		t.Error("a connection that lives a silent life must report the silentLife mode")
	}
	conn.Close()
}

func TestCloseListenerConnectionsClosesAll(t *testing.T) {
	s := startServer(t, &TCPConfig{SilentLife: true, ConnectionLife: "30s"})
	conns := []net.Conn{s.dial(), s.dial(), s.dial()}
	s.waitConnections(3)
	waitFor(t, "3 active connections", 3*time.Second, func() bool { return activeCount(s.port) == 3 })
	CloseListenerConnections(listenerID(s.port))
	if n := activeCount(s.port); n != 0 {
		t.Errorf("active connections after closing the listener's = %d, want 0", n)
	}
	for i, c := range conns {
		c.SetReadDeadline(time.Now().Add(5 * time.Second))
		b, err := io.ReadAll(c)
		if err != nil {
			t.Errorf("connection %d: %v after %q", i, err, b)
		}
	}
	for i, st := range statuses(t, s.port) {
		if !st.ServerClosed || !st.Closed {
			t.Errorf("connection %d status: serverClosed %t closed %t, want both", i, st.ServerClosed, st.Closed)
		}
	}
}

func TestCloseListenerConnectionsOnlyClosesThatListener(t *testing.T) {
	a := startServer(t, &TCPConfig{SilentLife: true, ConnectionLife: "30s"})
	b := startServer(t, &TCPConfig{SilentLife: true, ConnectionLife: "30s"})
	ca, cb := a.dial(), b.dial()
	a.waitConnections(1)
	b.waitConnections(1)
	CloseListenerConnections(listenerID(a.port))
	readAll(t, ca)
	if n := activeCount(b.port); n != 1 {
		t.Errorf("the other listener has %d active connections, want 1", n)
	}
	expectNoData(t, cb, 100*time.Millisecond)
	// closing a listener that has no connections is harmless
	CloseListenerConnections("no-such-listener")
}

func TestCloseClientConnectionClosesOnlyThatConnection(t *testing.T) {
	s := startServer(t, &TCPConfig{SilentLife: true, ConnectionLife: "30s"})
	c1, c2 := s.dial(), s.dial()
	s.waitConnections(2)
	waitFor(t, "2 active connections", 3*time.Second, func() bool { return activeCount(s.port) == 2 })
	CloseClientConnection(listenerID(s.port), 1)
	readAll(t, c1)
	if n := activeCount(s.port); n != 1 {
		t.Errorf("active connections = %d, want 1", n)
	}
	expectNoData(t, c2, 100*time.Millisecond)
	// closing what is gone, or never was, is harmless
	CloseClientConnection(listenerID(s.port), 1)
	CloseClientConnection(listenerID(s.port), 99)
	CloseClientConnection("no-such-listener", 1)
	if n := activeCount(s.port); n != 1 {
		t.Errorf("active connections after closing missing ones = %d, want 1", n)
	}
}

func TestServeManyConnectionsConcurrently(t *testing.T) {
	const clients = 25
	s := startServer(t, &TCPConfig{Echo: true, EchoResponseSize: 4})
	var wg sync.WaitGroup
	errs := make(chan string, clients)
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			conn, err := net.DialTimeout("tcp", s.ln.Addr().String(), 3*time.Second)
			if err != nil {
				errs <- err.Error()
				return
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(8 * time.Second))
			msg := "m" + strconv.Itoa(100+i)
			if _, err := conn.Write([]byte(msg + msg)); err != nil {
				errs <- err.Error()
				return
			}
			got := make([]byte, 2*len(msg))
			if _, err := io.ReadFull(conn, got); err != nil {
				errs <- err.Error()
				return
			}
			if string(got) != msg+msg {
				errs <- "echo of " + msg + " came back as " + string(got)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	s.waitIdle()
	sts := statuses(t, s.port)
	if len(sts) != clients {
		t.Fatalf("history has %d connections, want %d", len(sts), clients)
	}
	seen := map[int]bool{}
	for _, st := range sts {
		if seen[st.RequestID] {
			t.Errorf("request id %d is in the history twice", st.RequestID)
		}
		seen[st.RequestID] = true
		if st.TotalBytesRead != 8 || st.TotalBytesSent != 8 {
			t.Errorf("request %d read/sent %d/%d bytes, want 8/8", st.RequestID, st.TotalBytesRead, st.TotalBytesSent)
		}
	}
}

func TestConnectionEndsWhenListenerIsNotOpen(t *testing.T) {
	s := startServer(t, &TCPConfig{Echo: true, EchoResponseSize: 1})
	conn := s.dial()
	write(t, conn, "a")
	readN(t, conn, 1)
	closedPorts.Store(s.port, true)
	t.Cleanup(func() { closedPorts.Delete(s.port) })
	write(t, conn, "b")
	// the byte that arrives is read, and the connection then ends instead of echoing it
	if got := readAll(t, conn); got != "" {
		t.Errorf("connection of a closed listener echoed %q", got)
	}
	s.waitIdle()
}
