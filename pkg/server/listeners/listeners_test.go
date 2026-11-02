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

package listeners

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goto/pkg/global"
	gototls "goto/pkg/tls"
	"goto/pkg/util"

	"github.com/gorilla/mux"
	"golang.org/x/net/http2"
)

const (
	// apiPort is the port that the requests to the APIs are made on, which is not the port of any listener.
	apiPort     = 1
	dialTimeout = 2 * time.Second
	ioTimeout   = 5 * time.Second
)

var (
	setupOnce   sync.Once
	closeMu     sync.Mutex
	closedPorts = map[int]int{}
	grpcStub    = &stubGRPCServer{}
)

// TestMain keeps the certs that the listeners generate, which are written to the work dir, out of the temp dir of the
// machine.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "listeners-test-")
	if err != nil {
		panic(fmt.Sprintf("cannot make a work dir: %v", err))
	}
	global.ServerConfig.WorkDir = dir
	m.Run()
	os.RemoveAll(dir)
}

// stubGRPCServer stands in for the GRPC server, which the listeners hand their GRPC listeners to.
type stubGRPCServer struct {
	added  atomic.Int32
	served atomic.Int32
}

func (s *stubGRPCServer) AddListener(any)   { s.added.Add(1) }
func (s *stubGRPCServer) ServeListener(any) { s.served.Add(1) }

// setup stubs what the listeners get from the rest of the server. The servers that serve the listeners are made once,
// since the goroutines that serve read them, and are never replaced.
func setup(t *testing.T) {
	t.Helper()
	setupOnce.Do(func() {
		global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
		global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
		global.Funcs.CloseConnectionsForPort = func(port int) {
			closeMu.Lock()
			closedPorts[port]++
			closeMu.Unlock()
		}
		util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
		Init()
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "served") })
		httpServer = &http.Server{Handler: handler}
		jsonRPCServer = &http.Server{Handler: handler}
		h2Server = &http2.Server{}
		serveTCP = echoTCP
		global.GRPCServer = grpcStub
	})
}

// echoTCP is the TCP server of the listeners: it echoes what a connection sends first.
func echoTCP(id string, port int, ln net.Listener) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go func() {
			defer c.Close()
			c.SetDeadline(time.Now().Add(ioTimeout))
			buf := make([]byte, 256)
			if n, _ := c.Read(buf); n > 0 {
				c.Write(buf[:n])
			}
		}()
	}
}

func closedCount(port int) int {
	closeMu.Lock()
	defer closeMu.Unlock()
	return closedPorts[port]
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func freeUDPPort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}

// isListening tells if something accepts connections on the port.
func isListening(port int) bool {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), dialTimeout)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// forget closes what a test opened on the port, and removes the listener of the port, when the test is over.
func forget(t *testing.T, port int) {
	t.Helper()
	t.Cleanup(func() {
		if l := GetListenerForPort(port); l != nil && l != DefaultListener {
			l.Close()
			RemoveListener(l)
		}
	})
}

// closeAfter closes the listener when the test is over, for the listeners that are not stored.
func closeAfter(t *testing.T, l *Listener) {
	t.Helper()
	t.Cleanup(l.Close)
}

// newListenerOn gives a listener of the protocol on a free port, which is not stored or opened.
func newListenerOn(t *testing.T, protocol string) *Listener {
	t.Helper()
	setup(t)
	l := newListener(freePort(t), protocol, "l.test", false)
	l.Label = fmt.Sprintf("test-%d", l.Port)
	l.assignProtocol()
	closeAfter(t, l)
	return l
}

// addAndOpen adds the listener of the protocol on a free port, and opens it.
func addAndOpen(t *testing.T, protocol string) *Listener {
	t.Helper()
	setup(t)
	l := newListener(freePort(t), protocol, "l.test", true)
	forget(t, l.Port)
	if err, msg := AddOrUpdateListener(l); err != nil {
		t.Fatalf("AddOrUpdateListener(%s) failed: %s", protocol, msg)
	}
	if !l.Open || !isListening(l.Port) && protocol != "udp" {
		t.Fatalf("listener %s on port %d is not open", protocol, l.Port)
	}
	return l
}

func newRequest(method, target string, port int, vars map[string]string) *http.Request {
	return newRequestWithBody(method, target, "", port, vars)
}

func newRequestWithBody(method, target, body string, port int, vars map[string]string) *http.Request {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, port))
	_, req, _ = util.WithRequestStore(req, nil)
	if vars != nil {
		req = mux.SetURLVars(req, vars)
	}
	return req
}

// saveDefaults restores the state that the initial listeners change, when the test is over.
func saveDefaults(t *testing.T) {
	t.Helper()
	d := DefaultListener
	type state struct {
		port                                       int
		protocol, l8Proto, label, hostLabel        string
		isHTTP, isHTTP2, tls, assigned, verify, ac bool
	}
	saved := state{d.Port, d.Protocol, d.L8Proto, d.Label, d.HostLabel, d.IsHTTP, d.IsHTTP2, d.TLS, d.ProtoAssigned, d.VerifyClientCert, d.AutoCert}
	serverPort, jsonRPCPort := global.Self.ServerPort, global.Self.JSONRPCPort
	initial, initialDefsBefore := initialListeners, initialDefs
	initialDefs = map[int]*PortProtoConfig{}
	t.Cleanup(func() {
		listenersLock.Lock()
		delete(listeners, global.Self.JSONRPCPort)
		initialDefs = initialDefsBefore
		listenersLock.Unlock()
		d.Port, d.Protocol, d.L8Proto, d.Label, d.HostLabel = saved.port, saved.protocol, saved.l8Proto, saved.label, saved.hostLabel
		d.IsHTTP, d.IsHTTP2, d.TLS, d.ProtoAssigned, d.VerifyClientCert, d.AutoCert = saved.isHTTP, saved.isHTTP2, saved.tls, saved.assigned, saved.verify, saved.ac
		global.Self.ServerPort, global.Self.JSONRPCPort = serverPort, jsonRPCPort
		initialListeners = initial
	})
}

func TestAssignProtocol(t *testing.T) {
	type want struct {
		protocol, l8 string
		tls, verify  bool
		http, h2     bool
		grpc, xds    bool
		jsonrpc      bool
		udp, tcp     bool
	}
	tests := []struct {
		protocol string
		want     want
	}{
		{"http", want{protocol: "http", l8: "h2c", http: true, h2: true}},
		{"HTTP", want{protocol: "HTTP", l8: "h2c", http: true, h2: true}},
		{"https", want{protocol: "https", l8: "h2", tls: true, http: true, h2: true}},
		{"httpss", want{protocol: "httpss", l8: "h2", tls: true, verify: true, http: true, h2: true}},
		{"http1", want{protocol: "http", l8: "http", http: true}},
		{"https1", want{protocol: "https", l8: "https", tls: true, http: true}},
		{"grpc", want{protocol: "grpc", l8: "grpc", grpc: true}},
		{"grpcs", want{protocol: "grpc", l8: "grpc", tls: true, grpc: true}},
		{"grpcss", want{protocol: "grpc", l8: "grpc", tls: true, verify: true, grpc: true}},
		{"xds", want{protocol: "grpc", l8: "xds", grpc: true, xds: true}},
		{"jsonrpc", want{protocol: "http", l8: "jsonrpc", http: true, h2: true, jsonrpc: true}},
		{"rpc", want{protocol: "http", l8: "jsonrpc", http: true, h2: true, jsonrpc: true}},
		{"jsonrpcs", want{protocol: "https", l8: "jsonrpc", tls: true, http: true, h2: true, jsonrpc: true}},
		{"rpcs", want{protocol: "https", l8: "jsonrpc", tls: true, http: true, h2: true, jsonrpc: true}},
		{"udp", want{protocol: "udp", l8: "udp", udp: true}},
		{"tcp", want{protocol: "tcp", l8: "tcp", tcp: true}},
		{"tls", want{protocol: "tcp", l8: "tcp", tls: true, tcp: true}},
		{"", want{protocol: "tcp", l8: "tcp", tcp: true}},
		{"nonsense", want{protocol: "tcp", l8: "tcp", tcp: true}},
	}
	for _, tt := range tests {
		t.Run("protocol="+tt.protocol, func(t *testing.T) {
			l := newListener(1, tt.protocol, "cn", false)
			l.assignProtocol()
			got := want{l.Protocol, l.L8Proto, l.TLS, l.VerifyClientCert, l.IsHTTP, l.IsHTTP2, l.IsGRPC, l.IsXDS, l.IsJSONRPC, l.IsUDP, l.IsTCP}
			if got != tt.want {
				t.Errorf("assignProtocol(%q) = %+v, want %+v", tt.protocol, got, tt.want)
			}
			if !l.ProtoAssigned {
				t.Error("ProtoAssigned must be set")
			}
			if l.AutoCert != tt.want.tls {
				t.Errorf("AutoCert = %t, want %t: a TLS listener without certs makes its own", l.AutoCert, tt.want.tls)
			}
		})
	}
	t.Run("TLS with given certs does not auto-cert", func(t *testing.T) {
		l := newListener(1, "https", "cn", false)
		l.RawCert = []byte("cert")
		l.assignProtocol()
		if !l.TLS || l.AutoCert {
			t.Errorf("TLS = %t, AutoCert = %t, want TLS without AutoCert", l.TLS, l.AutoCert)
		}
	})
}

func TestParseListenerPorts(t *testing.T) {
	saveDefaults(t)
	got := parseListenerPorts([]string{"9100", "9101/GRPC", "9102/https/mtls", "9103/tls/mtlss", "9104/tcp/->9101", "9105", "9106/tcp/->bad"})
	want := []*PortProtoConfig{
		{Port: 9100, Proto: "http"},
		{Port: 9101, Proto: "grpc"},
		{Port: 9102, Proto: "https", MTLS: true},
		{Port: 9103, Proto: "tls", MTLS: true, Verify: true},
		{Port: 9104, Proto: "tcp", Forward: 9101},
		{Port: 9105},
		{Port: 9106, Proto: "tcp"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseListenerPorts() = %s, want %s", dump(got), dump(want))
	}
	if global.Self.ServerPort != 9100 {
		t.Errorf("ServerPort = %d, want the first port, 9100", global.Self.ServerPort)
	}
	// The first port can be left to the default.
	global.Self.ServerPort = 8765
	if got := parseListenerPorts([]string{"0"}); len(got) != 1 || got[0].Port != 8765 || got[0].Proto != "http" {
		t.Errorf("parseListenerPorts(0) = %s, want the default port 8765 with http", dump(got))
	}
	if got := parseListenerPorts(nil); len(got) != 0 {
		t.Errorf("parseListenerPorts(nil) = %s, want none", dump(got))
	}
}

func dump(pp []*PortProtoConfig) string {
	parts := []string{}
	for _, p := range pp {
		parts = append(parts, fmt.Sprintf("%+v", *p))
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func TestCreatePortListener(t *testing.T) {
	setup(t)
	existing := map[int]bool{}
	l := createPortListener(9201, 9202, "", true, true, existing)
	if l == nil {
		t.Fatal("createPortListener returned nil for a new port")
	}
	if l.Port != 9201 || l.ForwardPort != 9202 || l.Protocol != "http" || !l.IsHTTP || !l.MTLS || !l.VerifyClientCert || !l.Open {
		t.Errorf("listener = port %d forward %d protocol %q http %t mtls %t verify %t open %t, want 9201 -> 9202 http with mTLS verified, open",
			l.Port, l.ForwardPort, l.Protocol, l.IsHTTP, l.MTLS, l.VerifyClientCert, l.Open)
	}
	if l.Label != util.BuildListenerLabel(9201) || l.HostLabel != global.Self.HostLabel {
		t.Errorf("labels = %q, %q, want %q, %q", l.Label, l.HostLabel, util.BuildListenerLabel(9201), global.Self.HostLabel)
	}
	if !existing[9201] {
		t.Error("the port must be recorded, so that it is not used twice")
	}
	if dup := createPortListener(9201, 0, "tcp", false, false, existing); dup != nil {
		t.Errorf("a port that is already used gave a listener: %+v", dup)
	}
	g := createPortListener(9203, 0, "grpc", false, false, existing)
	if g == nil || !g.IsGRPC || g.MTLS || g.VerifyClientCert {
		t.Errorf("grpc listener = %+v, want a grpc listener without mTLS", g)
	}
}

func TestAddInitialListeners(t *testing.T) {
	setup(t)
	saveDefaults(t)
	initialListeners = nil
	global.Self.JSONRPCPort = 9300
	AddInitialListeners([]string{"9301", "9302/grpc", "9303/https/mtls", "9304/tcp/->9302", "9302/tcp"})

	if DefaultListener.Port != 9301 || DefaultListener.Protocol != "http" || !DefaultListener.IsHTTP || DefaultListener.TLS {
		t.Errorf("default listener = port %d protocol %q http %t tls %t, want an http listener on 9301",
			DefaultListener.Port, DefaultListener.Protocol, DefaultListener.IsHTTP, DefaultListener.TLS)
	}
	if want := util.BuildListenerLabel(9301); DefaultListener.Label != want {
		t.Errorf("default listener label = %q, want %q", DefaultListener.Label, want)
	}
	if l := GetListenerForPort(9300); l == nil || !l.IsJSONRPC || l.Port != 9300 {
		t.Errorf("JSONRPC listener = %+v, want one on 9300", l)
	}
	if len(initialListeners) != 3 {
		t.Fatalf("initial listeners = %d, want 3: the grpc, https and tcp ones, and not the repeated port", len(initialListeners))
	}
	byPort := map[int]*Listener{}
	for _, l := range initialListeners {
		byPort[l.Port] = l
	}
	if g := byPort[9302]; g == nil || !g.IsGRPC {
		t.Errorf("listener 9302 = %+v, want the grpc one, which came first", g)
	}
	if h := byPort[9303]; h == nil || !h.TLS || !h.MTLS || !h.IsHTTP {
		t.Errorf("listener 9303 = %+v, want https with mTLS", h)
	}
	if c := byPort[9304]; c == nil || !c.IsTCP || c.ForwardPort != 9302 {
		t.Errorf("listener 9304 = %+v, want tcp that forwards to 9302", c)
	}

	// what the arguments define is kept apart from the listeners, for the ports that go back to it
	if len(initialDefs) != 3 {
		t.Errorf("definitions = %d, want one for each of the initial listeners", len(initialDefs))
	}
	if d := initialDefs[9302]; d == nil || d.Proto != "grpc" || d.MTLS {
		t.Errorf("definition of 9302 = %+v, want the grpc one, which came first", d)
	}
	if d := initialDefs[9303]; d == nil || d.Proto != "https" || !d.MTLS {
		t.Errorf("definition of 9303 = %+v, want https with mTLS", d)
	}
	if d := initialDefs[9304]; d == nil || d.Proto != "tcp" || d.Forward != 9302 {
		t.Errorf("definition of 9304 = %+v, want tcp that forwards to 9302", d)
	}
	if initialDefs[9301] != nil {
		t.Error("the first port is the default listener, and is not one of the initial listeners")
	}
}

func TestAddAndRemoveInitialListener(t *testing.T) {
	setup(t)
	saveDefaults(t)
	initialListeners = nil
	port := freePort(t)
	pp := &PortProtoConfig{Port: port, Proto: "tcp", Forward: 9302}
	l := AddInitialListener(pp)
	if l == nil || l.Port != port || !l.IsTCP || l.ForwardPort != 9302 || !l.Open {
		t.Fatalf("initial listener = %+v, want a tcp listener to open on port %d that forwards to 9302", l, port)
	}
	if len(initialListeners) != 1 || initialListeners[0] != l || initialDefs[port] != pp {
		t.Errorf("initial listeners = %v, definition = %v, want the listener and the definition that were added", initialListeners, initialDefs[port])
	}
	if again := AddInitialListener(pp); again != nil || len(initialListeners) != 1 {
		t.Errorf("a port that is already an initial listener was added again: %v, initial listeners = %d", again, len(initialListeners))
	}

	RemoveInitialListener(port)
	if len(initialListeners) != 0 || initialDefs[port] != nil {
		t.Errorf("initial listeners = %d, definition = %v after the port was removed, want none", len(initialListeners), initialDefs[port])
	}
	RemoveInitialListener(port) // a port that is not one is nothing to remove
}

func TestRestoreInitialListenerIsNotForOtherPorts(t *testing.T) {
	l := addAndOpen(t, "tcp")
	saveDefaults(t)
	if RestoreInitialListener(l.Port) {
		t.Error("a port that is not an initial listener was restored")
	}
	if GetListenerForPort(l.Port) != l || !isListening(l.Port) {
		t.Error("the listener of a port that is not an initial listener was touched")
	}
}

func TestRestoreInitialListenerPutsBackWhatTheArgumentsDefine(t *testing.T) {
	setup(t)
	saveDefaults(t)
	port := freePort(t)
	forget(t, port)
	initial := AddInitialListener(&PortProtoConfig{Port: port, Proto: "tcp"})
	t.Cleanup(func() { RemoveInitialListener(port) })
	if err, msg := AddOrUpdateListener(initial); err != nil || !isListening(port) {
		t.Fatalf("the initial listener did not open: %v %s", err, msg)
	}

	// a config takes the port over with settings of its own
	changed := newListener(port, "tcp", "changed.test", true)
	changed.Label = "changed"
	if err, msg := AddOrUpdateListener(changed); err != nil || GetListenerForPort(port) != changed {
		t.Fatalf("the changed listener did not take the port: %v %s", err, msg)
	}

	if !RestoreInitialListener(port) {
		t.Fatal("an initial listener was not restored")
	}
	restored := GetListenerForPort(port)
	if restored == nil || restored == changed || restored.CommonName != global.ServerConfig.CommonName || !restored.IsTCP {
		t.Fatalf("listener after the restore = %+v, want a new one with the settings of the arguments (not %+v)", restored, changed)
	}
	if !isListening(port) {
		t.Errorf("port %d is not open after the restore", port)
	}
	if changed.isOpen() {
		t.Error("the listener of the config is still open after the restore")
	}
}

func TestRestoreInitialListenerLeavesAListenerThatIsAsDefinedAlone(t *testing.T) {
	setup(t)
	saveDefaults(t)
	port := freePort(t)
	forget(t, port)
	initial := AddInitialListener(&PortProtoConfig{Port: port, Proto: "tcp"})
	t.Cleanup(func() { RemoveInitialListener(port) })
	if err, msg := AddOrUpdateListener(initial); err != nil || !isListening(port) {
		t.Fatalf("the initial listener did not open: %v %s", err, msg)
	}
	generation := initial.Generation

	if !RestoreInitialListener(port) {
		t.Fatal("an initial listener was not restored")
	}
	if GetListenerForPort(port) != initial || initial.Generation != generation {
		t.Errorf("a listener that is as the arguments define it was replaced or restarted (generation %d, was %d)", initial.Generation, generation)
	}
}

func TestRestoreInitialListenerOpensAListenerThatIsClosed(t *testing.T) {
	setup(t)
	saveDefaults(t)
	port := freePort(t)
	forget(t, port)
	initial := AddInitialListener(&PortProtoConfig{Port: port, Proto: "tcp"})
	t.Cleanup(func() { RemoveInitialListener(port) })
	if err, msg := AddOrUpdateListener(initial); err != nil || !isListening(port) {
		t.Fatalf("the initial listener did not open: %v %s", err, msg)
	}
	initial.Close()
	if isListening(port) {
		t.Fatal("the listener is still open after it was closed")
	}

	if !RestoreInitialListener(port) || !isListening(port) {
		t.Errorf("a closed initial listener was not opened again by the restore (port %d accepting %t)", port, isListening(port))
	}
}

func TestLinkForwardListeners(t *testing.T) {
	setup(t)
	from := newListener(freePort(t), "tcp", "", false)
	to := newListener(freePort(t), "tcp", "", false)
	from.ForwardPort = to.Port
	listenersLock.Lock()
	listeners[from.Port], listeners[to.Port] = from, to
	listenersLock.Unlock()
	t.Cleanup(func() { RemoveListener(from); RemoveListener(to) })

	LinkForwardListeners()
	if from.ForwardListener != to {
		t.Errorf("forward listener = %v, want the listener of port %d", from.ForwardListener, to.Port)
	}
	if to.ForwardListener != nil {
		t.Errorf("a listener with no forward port got a forward listener: %v", to.ForwardListener)
	}
	// The link is dropped when its target goes away.
	RemoveListener(to)
	LinkForwardListeners()
	if from.ForwardListener != nil {
		t.Errorf("forward listener = %v, want none after the target was removed", from.ForwardListener)
	}
}

// Removing a listener that has the port of the registered one, but is not it, must leave the registered one alone: a
// listener that is dropped while it is open keeps the port bound, and nothing can close it anymore.
func TestRemoveListenerLeavesTheRegisteredListenerOfThePortAlone(t *testing.T) {
	l := addAndOpen(t, "tcp")
	other := newListener(l.Port, "tcp", "l.test", false)

	RemoveListener(other)
	if got := GetListenerForPort(l.Port); got != l {
		t.Fatalf("listener of port %d = %v after removing another listener of the port, want the registered one", l.Port, got)
	}
	if !isListening(l.Port) {
		t.Errorf("port %d stopped accepting connections", l.Port)
	}

	RemoveListener(l)
	if got := GetListenerForPort(l.Port); got != nil {
		t.Errorf("listener of port %d = %v after removing the registered one, want none", l.Port, got)
	}
}

func TestRemoveListenerChecksTheRegisteredListenerOfEachKind(t *testing.T) {
	setup(t)
	for _, kind := range []string{"grpc", "udp"} {
		t.Run(kind, func(t *testing.T) {
			port := freePort(t)
			registered := &Listener{Port: port, IsGRPC: kind == "grpc", IsUDP: kind == "udp"}
			other := &Listener{Port: port, IsGRPC: registered.IsGRPC, IsUDP: registered.IsUDP}
			byKind := func() map[int]*Listener {
				if registered.IsGRPC {
					return grpcListeners
				}
				return udpListeners
			}
			listenersLock.Lock()
			listeners[port], byKind()[port] = registered, registered
			listenersLock.Unlock()
			t.Cleanup(func() { RemoveListener(registered) })

			RemoveListener(other)
			listenersLock.RLock()
			inAll, inKind := listeners[port] == registered, byKind()[port] == registered
			listenersLock.RUnlock()
			if !inAll || !inKind {
				t.Errorf("after removing another %s listener of the port: registered in all listeners %t, in the %s ones %t, want both", kind, inAll, kind, inKind)
			}

			RemoveListener(registered)
			listenersLock.RLock()
			inAll, inKind = listeners[port] != nil, byKind()[port] != nil
			listenersLock.RUnlock()
			if inAll || inKind {
				t.Errorf("after removing the registered %s listener: still in all listeners %t, in the %s ones %t, want neither", kind, inAll, kind, inKind)
			}
		})
	}
}

func TestDiff(t *testing.T) {
	base := func() *Listener {
		l := newListener(8080, "http", "cn", true)
		l.Open = true
		return l
	}
	if Diff(nil, nil) {
		t.Error("two nil listeners must not differ")
	}
	if !Diff(nil, base()) || !Diff(base(), nil) {
		t.Error("a nil listener must differ from a listener")
	}
	if Diff(base(), base()) {
		t.Error("equal listeners must not differ")
	}
	changes := map[string]func(*Listener){
		"port":        func(l *Listener) { l.Port = 8081 },
		"forward":     func(l *Listener) { l.ForwardPort = 8081 },
		"protocol":    func(l *Listener) { l.Protocol = "grpc" },
		"open":        func(l *Listener) { l.Open = false },
		"autosni":     func(l *Listener) { l.AutoSNI = true },
		"commonname":  func(l *Listener) { l.CommonName = "other" },
		"tls":         func(l *Listener) { l.TLS = true },
		"mtls":        func(l *Listener) { l.MTLS = true },
		"verify":      func(l *Listener) { l.VerifyClientCert = true },
		"alpn added":  func(l *Listener) { l.ALPN = &gototls.ALPN{Protos: []string{"h2"}} },
		"alpn protos": func(l *Listener) { l.ALPN = &gototls.ALPN{Protos: []string{"h3"}} },
	}
	for name, change := range changes {
		other := base()
		change(other)
		if !Diff(base(), other) || !Diff(other, base()) {
			t.Errorf("a change of %s must be a difference", name)
		}
	}
	a, b := base(), base()
	a.ALPN = &gototls.ALPN{Protos: []string{"h2", "http/1.1"}}
	b.ALPN = &gototls.ALPN{Protos: []string{"h2", "http/1.1"}}
	if Diff(a, b) {
		t.Error("listeners with the same ALPN protos must not differ")
	}
	b.ALPN.Protos = []string{"h2"}
	if !Diff(a, b) {
		t.Error("listeners with different ALPN protos must differ")
	}
	a.ALPN.Handshake = &gototls.TCPHandshake{Protos: []string{"x"}}
	b.ALPN.Protos = a.ALPN.Protos
	if !Diff(a, b) {
		t.Error("a listener with an ALPN handshake must differ from one without")
	}
}

func TestSetCertificates(t *testing.T) {
	l := newListener(1, "https", "cn", false)
	l.SetCertificates(nil)
	if l.Certificates != nil || l.ServerCert != nil {
		t.Errorf("no certs set certificates = %v, server cert = %v, want none", l.Certificates, l.ServerCert)
	}
	cert, err := gototls.CreateCertificate([]string{"set.test", "alt.test"}, "spiffe://goto/set", "")
	if err != nil {
		t.Fatal(err)
	}
	l.ALPN = &gototls.ALPN{Protos: []string{"h2", "x"}}
	l.SetCertificates([]*tls.Certificate{cert})
	if len(l.Certificates) != 1 || l.ServerCert == nil {
		t.Fatalf("certificates = %d, server cert = %v, want 1 and a server cert", len(l.Certificates), l.ServerCert)
	}
	sc := l.ServerCert
	if !reflect.DeepEqual(sc.DNSNames, []string{"set.test", "alt.test"}) || !reflect.DeepEqual(sc.URIs, []string{"spiffe://goto/set"}) {
		t.Errorf("server cert names = %v, uris = %v, want the ones of the cert", sc.DNSNames, sc.URIs)
	}
	if !strings.Contains(sc.Subject, "cn=[set.test]") || sc.Issuer == "" || !reflect.DeepEqual(sc.ALPN, []string{"h2", "x"}) {
		t.Errorf("server cert subject = %q, issuer = %q, alpn = %v, want subject of set.test, an issuer, and the ALPNs of the listener",
			sc.Subject, sc.Issuer, sc.ALPN)
	}
	if sc.StartAt.IsZero() {
		t.Error("server cert has no start time")
	}
}

func TestGetCertificateIsCachedPerSNI(t *testing.T) {
	l := newListener(1, "https", "l.test", false)
	l.AltNames = []string{"alt.test"}
	a1, err := l.GetCertificate(&tls.ClientHelloInfo{ServerName: "a.test"})
	if err != nil || a1 == nil {
		t.Fatalf("GetCertificate(a.test) = %v, %v", a1, err)
	}
	a2, _ := l.GetCertificate(&tls.ClientHelloInfo{ServerName: "a.test"})
	b, _ := l.GetCertificate(&tls.ClientHelloInfo{ServerName: "b.test"})
	if a2 != a1 {
		t.Error("the cert of an SNI must be made once")
	}
	if b == a1 || b == nil {
		t.Error("another SNI must get a cert of its own")
	}
	leaf, err := x509Leaf(a1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(leaf.DNSNames, []string{"a.test", "l.test", "alt.test"}) {
		t.Errorf("cert names = %v, want the SNI, the common name and the alt names", leaf.DNSNames)
	}
}

func x509Leaf(c *tls.Certificate) (*x509.Certificate, error) {
	return x509.ParseCertificate(c.Certificate[0])
}

func TestGetCertificateConcurrent(t *testing.T) {
	l := newListener(1, "https", "l.test", false)
	var wg sync.WaitGroup
	results := make([]*tls.Certificate, 8)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Whoever loses the race for an SNI must still end up with a cert, and the cache must stay consistent.
			results[i], _ = l.GetCertificate(&tls.ClientHelloInfo{ServerName: fmt.Sprintf("c%d.test", i%2)})
		}()
	}
	wg.Wait()
	for i, c := range results {
		if c == nil {
			t.Errorf("request %d got no cert", i)
		}
	}
	l.lock.RLock()
	defer l.lock.RUnlock()
	if len(l.CertsCache) != 2 {
		t.Errorf("cache has %d certs, want 2, one per SNI", len(l.CertsCache))
	}
}

// A listener binds the port when it is initialized, and gives it back when it is closed.
func TestInitAndCloseListener(t *testing.T) {
	l := newListenerOn(t, "http")
	if l.Open || l.Listener != nil {
		t.Fatal("a new listener must not be open")
	}
	if !l.InitListener() {
		t.Fatal("InitListener failed on a free port")
	}
	if l.Listener == nil || !l.Open || !isListening(l.Port) {
		t.Errorf("listener = %v, open = %t, listening = %t, want an open listener that accepts connections", l.Listener, l.Open, isListening(l.Port))
	}
	before := closedCount(l.Port)
	l.Close()
	if l.Listener != nil || l.Open || isListening(l.Port) {
		t.Errorf("after Close: listener = %v, open = %t, listening = %t, want closed", l.Listener, l.Open, isListening(l.Port))
	}
	if got := closedCount(l.Port); got != before+1 {
		t.Errorf("connections of the port were closed %d times, want once", got-before)
	}
	// Closing again, as the GRPC stop does for listeners that the server already closed, must be harmless.
	l.Close()
	if got := closedCount(l.Port); got != before+1 {
		t.Errorf("a second Close closed the connections again: %d, want %d", got, before+1)
	}
}

func TestInitListenerFailsWhenPortIsTaken(t *testing.T) {
	setup(t)
	taken, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	l := newListener(taken.Addr().(*net.TCPAddr).Port, "http", "", true)
	l.assignProtocol()
	if l.InitListener() {
		l.Close()
		t.Error("InitListener succeeded on a port that is taken")
	}
	if l.Listener != nil || l.Open {
		t.Errorf("listener = %v, open = %t, want nothing opened", l.Listener, l.Open)
	}
}

func TestOpenListenerServesHTTP(t *testing.T) {
	l := addAndOpen(t, "http")
	if l.Generation < 1 || l.ListenerID != fmt.Sprintf("%d-%d", l.Port, l.Generation) {
		t.Errorf("generation = %d, id = %q, want id <port>-<generation>", l.Generation, l.ListenerID)
	}
	body := httpGet(t, fmt.Sprintf("http://127.0.0.1:%d/", l.Port), nil)
	if body != "served" {
		t.Errorf("body = %q, want the response of the HTTP server", body)
	}
	if !IsListenerPresent(l.Port) || !IsListenerOpen(l.Port) || GetListenerID(l.Port) != l.ListenerID {
		t.Errorf("present = %t, open = %t, id = %q, want the listener to be known as open", IsListenerPresent(l.Port), IsListenerOpen(l.Port), GetListenerID(l.Port))
	}
	// Opening on the same port again is a reopen: a new generation takes over the port.
	first := l.Generation
	l2 := newListener(l.Port, "http", "", true)
	if err, msg := AddOrUpdateListener(l2); err != nil {
		t.Fatalf("update failed: %s", msg)
	}
	if l2.Generation <= first {
		t.Errorf("generation after reopen = %d, want more than %d", l2.Generation, first)
	}
	if got := GetListenerForPort(l.Port); got != l2 {
		t.Errorf("the stored listener is not the one that was added last")
	}
	if !l.Restarted {
		t.Error("the replaced listener must be marked as restarted")
	}
	if body := httpGet(t, fmt.Sprintf("http://127.0.0.1:%d/", l.Port), nil); body != "served" {
		t.Errorf("body after reopen = %q, want served", body)
	}
}

func httpGet(t *testing.T, url string, client *http.Client) string {
	t.Helper()
	if client == nil {
		client = &http.Client{Timeout: ioTimeout, Transport: &http.Transport{DisableKeepAlives: true}}
	}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func TestOpenListenerServesJSONRPC(t *testing.T) {
	l := addAndOpen(t, "jsonrpc")
	if !l.IsJSONRPC || l.L8Proto != "jsonrpc" {
		t.Fatalf("listener = jsonrpc %t, l8 %q", l.IsJSONRPC, l.L8Proto)
	}
	if body := httpGet(t, fmt.Sprintf("http://127.0.0.1:%d/", l.Port), nil); body != "served" {
		t.Errorf("body = %q, want the response of the JSONRPC server", body)
	}
}

func TestTCPListenerEchoes(t *testing.T) {
	l := addAndOpen(t, "tcp")
	if l.TCP == nil || l.TCP.ListenerID != l.ListenerID {
		t.Errorf("tcp config = %+v, want one that knows the listener id %q", l.TCP, l.ListenerID)
	}
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", l.Port), dialTimeout)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(ioTimeout))
	fmt.Fprint(c, "ping\n")
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil || line != "ping\n" {
		t.Errorf("echo = %q, %v, want ping", line, err)
	}
}

func TestOpenListenerWithoutServerDoesNotCrash(t *testing.T) {
	setup(t)
	// The servers go away when they stop. A listener that opens then has nothing to serve it, which must not take the
	// process down from a goroutine. Only the nil checks are exercised, on the listeners that don't serve.
	httpSaved, jsonSaved, tcpSaved := httpServer, jsonRPCServer, serveTCP
	httpServer, jsonRPCServer, serveTCP = nil, nil, nil
	defer func() { httpServer, jsonRPCServer, serveTCP = httpSaved, jsonSaved, tcpSaved }()
	for _, protocol := range []string{"http", "jsonrpc", "tcp"} {
		l := newListener(freePort(t), protocol, "", true)
		forget(t, l.Port)
		if err, msg := AddOrUpdateListener(l); err != nil {
			t.Errorf("%s: AddOrUpdateListener failed: %s", protocol, msg)
		} else if !l.Open {
			t.Errorf("%s: listener is not open", protocol)
		}
	}
}

func TestAddListenerTCPAndUDP(t *testing.T) {
	setup(t)
	port := freePort(t)
	forget(t, port)
	if err := AddListener(port, true, false, ""); err != nil {
		t.Fatal(err)
	}
	l := GetListenerForPort(port)
	if l == nil || !l.IsTCP || !l.Open || !isListening(port) {
		t.Fatalf("listener = %+v, want an open tcp listener", l)
	}
	gen := l.Generation

	// Adding what is there, and open, changes nothing.
	if err := AddListener(port, true, false, ""); err != nil {
		t.Fatal(err)
	}
	if got := GetListenerForPort(port); got != l || l.Generation != gen {
		t.Errorf("adding a TCP listener that is open replaced it: same = %t, generation %d -> %d", got == l, gen, l.Generation)
	}

	// Closed listeners are opened again.
	l.Close()
	if err := AddListener(port, true, false, ""); err != nil {
		t.Fatal(err)
	}
	if !l.Open || !isListening(port) {
		t.Errorf("open = %t, listening = %t, want the closed listener to be opened again", l.Open, isListening(port))
	}

	// A listener of the other kind takes the port over.
	if err := AddListener(port, false, true, ""); err != nil {
		t.Fatal(err)
	}
	u := GetListenerForPort(port)
	if u == l || u == nil || !u.IsUDP || u.UDPConn == nil {
		t.Errorf("listener = %+v, want a new UDP listener with a connection", u)
	}
	if l.Open || isListening(port) {
		t.Errorf("the TCP listener is open = %t, listening = %t, want it closed", l.Open, isListening(port))
	}
	udpListenersLock(func() {
		if udpListeners[port] != u {
			t.Error("the UDP listener is not tracked as a UDP listener")
		}
	})
}

func udpListenersLock(f func()) {
	listenersLock.RLock()
	defer listenersLock.RUnlock()
	f()
}

func TestAddListenerFailsWhenPortIsTaken(t *testing.T) {
	setup(t)
	taken, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	port := taken.Addr().(*net.TCPAddr).Port
	forget(t, port)
	err = AddListener(port, true, false, "")
	if err == nil || !strings.Contains(err.Error(), "failed to open tcp listener") {
		t.Errorf("AddListener on a port that is taken = %v, want a failure to open", err)
	}
}

func TestUDPListenerOpensAndCloses(t *testing.T) {
	setup(t)
	port := freeUDPPort(t)
	forget(t, port)
	if err := AddListener(port, false, true, ""); err != nil {
		t.Fatal(err)
	}
	l := GetListenerForPort(port)
	if l == nil || !l.IsUDP || l.UDPConn == nil || !l.Open {
		t.Fatalf("listener = %+v, want an open UDP listener", l)
	}
	if _, err := net.ListenUDP("udp", &net.UDPAddr{Port: port}); err == nil {
		t.Error("the port of a UDP listener can be bound again")
	}
	l.Close()
	if l.UDPConn != nil || l.Open {
		t.Errorf("after Close: conn = %v, open = %t, want closed", l.UDPConn, l.Open)
	}
	c, err := net.ListenUDP("udp", &net.UDPAddr{Port: port})
	if err != nil {
		t.Errorf("the port is not free after Close: %v", err)
	} else {
		c.Close()
	}
}

func TestAddGRPCListener(t *testing.T) {
	setup(t)
	port := freePort(t)
	forget(t, port)
	l, err := AddGRPCListener(port, false)
	if err != nil || l == nil || !l.IsGRPC || !l.Open {
		t.Fatalf("AddGRPCListener = %+v, %v, want an open grpc listener", l, err)
	}
	listenersLock.RLock()
	tracked := grpcListeners[port] == l
	listenersLock.RUnlock()
	if !tracked {
		t.Error("the listener is not tracked as a GRPC listener")
	}
	// The same listener is given for the same port while it is open.
	if l2, err := AddGRPCListener(port, false); err != nil || l2 != l {
		t.Errorf("AddGRPCListener again = %v, %v, want the same listener", l2, err)
	}
	// With serve, the GRPC server is handed the listener: added while it is not started, served when it is.
	added := grpcStub.added.Load()
	port2 := freePort(t)
	forget(t, port2)
	if _, err := AddGRPCListener(port2, true); err != nil {
		t.Fatal(err)
	}
	if got := grpcStub.added.Load(); got != added+1 {
		t.Errorf("listeners added to the GRPC server = %d, want %d", got, added+1)
	}
	// A listener of another kind on the port is closed and replaced.
	port3 := freePort(t)
	forget(t, port3)
	if err := AddListener(port3, true, false, ""); err != nil {
		t.Fatal(err)
	}
	tcpListener := GetListenerForPort(port3)
	g, err := AddGRPCListener(port3, false)
	if err != nil || g == tcpListener || !g.IsGRPC || tcpListener.Open {
		t.Errorf("AddGRPCListener over a TCP listener = %+v, %v, tcp open = %t, want a new grpc listener and the tcp one closed", g, err, tcpListener.Open)
	}
}

func TestGetters(t *testing.T) {
	setup(t)
	l := addAndOpen(t, "http")
	if got := GetListenerForPort(l.Port); got != l {
		t.Errorf("GetListenerForPort = %v, want the listener", got)
	}
	if got := GetListenerForPort(DefaultListener.Port); got != DefaultListener {
		t.Errorf("GetListenerForPort(default port) = %v, want the default listener", got)
	}
	if got := GetListenerForPort(1); got != nil {
		t.Errorf("GetListenerForPort(unknown) = %v, want nil", got)
	}
	if IsListenerPresent(1) || IsListenerOpen(1) || IsListenerTLS(1) || GetListenerID(1) != "" {
		t.Error("an unknown port must not be present, open, TLS, or have an id")
	}
	if IsListenerTLS(l.Port) {
		t.Error("an HTTP listener is not TLS")
	}
	if GetListenerLabelForPort(l.Port) != l.Label {
		t.Errorf("label = %q, want %q", GetListenerLabelForPort(l.Port), l.Label)
	}
	if got, want := GetListenerLabelForPort(2), util.BuildListenerLabel(2); got != want {
		t.Errorf("label of an unknown port = %q, want %q", got, want)
	}
	ports := GetListenerPorts()
	if ports[l.Port] != "h2c" || ports[DefaultListener.Port] != DefaultListener.Protocol {
		t.Errorf("ports = %v, want %d -> h2c and the default port with its protocol", ports, l.Port)
	}
	all := GetListeners()
	if all[l.Port] != l || all[DefaultListener.Port] != DefaultListener {
		t.Errorf("listeners = %v, want the listener and the default one", all)
	}
	// Changing the views doesn't change the listeners.
	delete(all, l.Port)
	delete(ports, l.Port)
	if !IsListenerPresent(l.Port) {
		t.Error("deleting from the view removed the listener")
	}

	r := newRequest("GET", "/x", l.Port, nil)
	if got := GetCurrentListener(r); got != l {
		t.Errorf("GetCurrentListener = %v, want the listener of the port of the request", got)
	}
	if got := GetRequestedListener(r); got != l {
		t.Errorf("GetRequestedListener = %v, want the listener of the port of the request", got)
	}
	if got := GetListenerLabel(r); got != l.Label {
		t.Errorf("GetListenerLabel = %q, want %q", got, l.Label)
	}
	if got := GetCurrentListener(newRequest("GET", "/x", 2, nil)); got != DefaultListener {
		t.Errorf("GetCurrentListener of an unknown port = %v, want the default listener", got)
	}
}

func TestInitRegistersFuncs(t *testing.T) {
	setup(t)
	f := global.Funcs
	if f.IsListenerPresent == nil || f.IsListenerOpen == nil || f.GetListenerID == nil || f.GetListenerLabel == nil ||
		f.GetListenerLabelForPort == nil || f.IsListenerTLS == nil || f.GetCertInfo == nil {
		t.Fatalf("Init left functions of the listeners unset: %+v", f)
	}
	l := newListener(freePort(t), "tcp", "", false)
	listenersLock.Lock()
	listeners[l.Port] = l
	listenersLock.Unlock()
	t.Cleanup(func() { RemoveListener(l) })
	if !f.IsListenerPresent(l.Port) || f.IsListenerOpen(l.Port) || f.GetListenerLabelForPort(l.Port) != l.Label {
		t.Error("the registered functions do not answer for the listeners")
	}
}

func TestSetListenerLabel(t *testing.T) {
	setup(t)
	l := addAndOpen(t, "http")
	r := newRequest("PUT", "/server/label/set/mine", l.Port, map[string]string{"label": "mine"})
	if got := SetListenerLabel(r); got != "mine" {
		t.Errorf("SetListenerLabel = %q, want mine", got)
	}
	if l.Label != "mine" || GetListenerLabelForPort(l.Port) != "mine" {
		t.Errorf("label = %q, want the one that was set", l.Label)
	}
	// A port with no stored listener, which is the default one, sets the default label.
	savedLabel, savedDefault := DefaultLabel, DefaultListener.Label
	defer func() { DefaultLabel, DefaultListener.Label = savedLabel, savedDefault }()
	r = newRequest("PUT", "/server/label/set/dflt", DefaultListener.Port, map[string]string{"label": "dflt"})
	if got := SetListenerLabel(r); got != "dflt" || DefaultLabel != "dflt" || DefaultListener.Label != "dflt" {
		t.Errorf("label = %q, default label = %q, default listener label = %q, want dflt everywhere", got, DefaultLabel, DefaultListener.Label)
	}
}

func TestValidateUDPListener(t *testing.T) {
	setup(t)
	port := freeUDPPort(t)
	forget(t, port)
	if err := AddListener(port, false, true, ""); err != nil {
		t.Fatal(err)
	}
	tcpPort := freePort(t)
	forget(t, tcpPort)
	if err := AddListener(tcpPort, true, false, ""); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		port int
		ok   bool
		code int
	}{
		{"udp", port, true, http.StatusOK},
		{"tcp listener", tcpPort, false, http.StatusBadRequest},
		{"no listener", 1, false, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			r := newRequest("POST", "/", apiPort, map[string]string{"port": fmt.Sprint(tt.port)})
			if got := ValidateUDPListener(rr, r); got != tt.ok || rr.Code != tt.code {
				t.Errorf("ValidateUDPListener = %t with %d %q, want %t with %d", got, rr.Code, rr.Body.String(), tt.ok, tt.code)
			}
			if !tt.ok && rr.Body.Len() == 0 {
				t.Error("a rejected listener must be told why")
			}
		})
	}
}

func TestAddAndRemoveListenerCert(t *testing.T) {
	setup(t)
	l := addAndOpen(t, "https1")
	if !l.TLS || l.ServerCert == nil {
		t.Fatalf("tls = %t, server cert = %v, want an auto-cert TLS listener", l.TLS, l.ServerCert)
	}
	if err := AddListenerCert(l.Port+0, nil, nil, false); err == nil || !strings.Contains(err.Error(), "No payload") {
		t.Errorf("AddListenerCert with nothing = %v, want a No payload error", err)
	}
	if err := AddListenerCert(1, []byte("k"), nil, false); err == nil || !strings.Contains(err.Error(), "No listener") {
		t.Errorf("AddListenerCert on an unknown port = %v, want a No listener error", err)
	}
	cert, key := certAndKey(t, "added.test")
	if err := AddListenerCert(l.Port, key, cert, false); err != nil {
		t.Fatal(err)
	}
	if string(l.RawCert) != string(cert) || string(l.RawKey) != string(key) || l.AutoCert || l.Certificates != nil || l.CommonName != "<from cert>" {
		t.Errorf("after AddListenerCert: raw cert matches = %t, raw key matches = %t, auto cert = %t, certificates = %v, cn = %q",
			string(l.RawCert) == string(cert), string(l.RawKey) == string(key), l.AutoCert, l.Certificates, l.CommonName)
	}
	// Only the cert is replaced when only a cert is given.
	other, _ := certAndKey(t, "other.test")
	if err := AddListenerCert(l.Port, nil, other, false); err != nil {
		t.Fatal(err)
	}
	if string(l.RawCert) != string(other) || string(l.RawKey) != string(key) {
		t.Error("giving a cert changed the key, or didn't change the cert")
	}
	if err := AddListenerCert(l.Port, key, cert, true); err != nil {
		t.Fatalf("AddListenerCert with reopen: %v", err)
	}
	if !isListening(l.Port) {
		t.Error("the listener is not open after a reopen")
	}
	if err := RemoveListenerCert(1); err == nil {
		t.Error("RemoveListenerCert on an unknown port succeeded")
	}
	if err := RemoveListenerCert(l.Port); err != nil {
		t.Fatal(err)
	}
	// The listener that is reopened without a cert is the one of the port now.
	cur := GetListenerForPort(l.Port)
	if cur.RawCert != nil || cur.RawKey != nil || cur.Certificates != nil && len(cur.RawCert) > 0 {
		t.Errorf("certs remain after RemoveListenerCert: raw cert %d, raw key %d", len(cur.RawCert), len(cur.RawKey))
	}
}

func certAndKey(t *testing.T, domain string) (cert, key []byte) {
	t.Helper()
	c, err := gototls.CreateCertificate([]string{domain}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if cert, err = gototls.EncodeX509Cert(c); err != nil {
		t.Fatal(err)
	}
	if key, err = gototls.EncodeX509Key(c); err != nil {
		t.Fatal(err)
	}
	return
}

// A TLS listener records what the peers present, which is reported back to the requests that they make.
func TestTLSListenerRecordsPeers(t *testing.T) {
	l := addAndOpen(t, "tls")
	if !l.TLS || l.ServerCert == nil {
		t.Fatalf("tls = %t, server cert = %v, want a TLS listener with a server cert", l.TLS, l.ServerCert)
	}
	c, err := tls.DialWithDialer(&net.Dialer{Timeout: dialTimeout}, "tcp", fmt.Sprintf("127.0.0.1:%d", l.Port),
		&tls.Config{InsecureSkipVerify: true, ServerName: "peer.test"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(ioTimeout))
	fmt.Fprint(c, "hello\n")
	if line, err := bufio.NewReader(c).ReadString('\n'); err != nil || line != "hello\n" {
		t.Fatalf("echo over TLS = %q, %v", line, err)
	}
	peer := c.LocalAddr().String()

	certs := GetPeerCerts(l.Port)
	pci := certs[l.Port][peer]
	if pci == nil {
		t.Fatalf("peer certs of the port = %v, want the peer %s", certs, peer)
	}
	if pci.SNI != "peer.test" || pci.RemoteAddr != peer || len(pci.Status) == 0 {
		t.Errorf("peer = sni %q, remote %q, status %v, want sni peer.test from %s with a status", pci.SNI, pci.RemoteAddr, pci.Status, peer)
	}
	server, client := GetPeerCert(l.Port, peer)
	if !strings.Contains(server, "Subject:") || !strings.Contains(client, "SNI: peer.test") || !strings.Contains(client, "Finished: true") {
		t.Errorf("server cert summary = %q, client cert summary = %q, want the server subject and the client SNI", server, client)
	}
	// Peers that were not seen have nothing reported, but the server cert is.
	if server2, client2 := GetPeerCert(l.Port, "10.0.0.1:1"); server2 == "" || client2 != "" {
		t.Errorf("GetPeerCert for an unknown peer = %q, %q, want the server cert only", server2, client2)
	}
	ClearPeerCerts(l.Port)
	if got := GetPeerCerts(l.Port)[l.Port]; len(got) != 0 {
		t.Errorf("peer certs after ClearPeerCerts = %v, want none", got)
	}
	// Clearing the peers of another port leaves them alone.
	l.StoreSNI("1.1.1.1:1", "kept.test", "")
	ClearPeerCerts(1)
	if got := GetPeerCerts(l.Port)[l.Port]; len(got) != 1 {
		t.Errorf("peers after clearing another port = %d, want 1", len(got))
	}
	ClearPeerCerts(0)
	if got := GetPeerCerts(0)[l.Port]; len(got) != 0 {
		t.Errorf("peers after clearing all = %d, want 0", len(got))
	}
}

func TestPeerCertInfoStores(t *testing.T) {
	l := newListener(freePort(t), "tls", "", false)
	l.StoreALPN("p:1", []string{"h2"})
	l.StoreSNI("p:1", "sni.test", "h2")
	l.StorePeerCertInfo("p:1", "subject", []string{"d.test"}, []string{"spiffe://x"}, "issuer")
	l.UpdatePeerStatus("p:1", false, "first")
	l.UpdatePeerStatus("p:1", true, "second")
	l.lock.RLock()
	defer l.lock.RUnlock()
	pci := l.peerCertInfos["p:1"]
	if pci == nil || len(l.peerCertInfos) != 1 {
		t.Fatalf("peers = %v, want one", l.peerCertInfos)
	}
	if !reflect.DeepEqual(pci.ALPN, []string{"h2"}) || pci.SNI != "sni.test" || pci.NegotiatedALPN != "h2" || pci.Subject != "subject" ||
		!reflect.DeepEqual(pci.DNSNames, []string{"d.test"}) || !reflect.DeepEqual(pci.URIs, []string{"spiffe://x"}) || pci.Issuer != "issuer" {
		t.Errorf("peer = %+v, want everything that was stored", pci)
	}
	if !pci.Finished || !reflect.DeepEqual(pci.Status, []string{"first", "second"}) || pci.EndAt.IsZero() {
		t.Errorf("finished = %t, status = %v, end = %v, want finished with both statuses", pci.Finished, pci.Status, pci.EndAt)
	}
}
