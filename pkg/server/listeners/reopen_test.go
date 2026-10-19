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
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"goto/pkg/global"
)

// These tests shorten the waits of the listeners, which are for the servers that start, and for ports that are taken.

func fastRetries(t *testing.T) {
	t.Helper()
	savedRetry, savedInitial := reopenRetryDelay, initialListenersDelay
	reopenRetryDelay, initialListenersDelay = time.Millisecond, 0
	t.Cleanup(func() { reopenRetryDelay, initialListenersDelay = savedRetry, savedInitial })
}

func takePort(t *testing.T) (port int) {
	t.Helper()
	taken, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { taken.Close() })
	return taken.Addr().(*net.TCPAddr).Port
}

func TestReopenListenerGivesUpWhenPortStaysTaken(t *testing.T) {
	setup(t)
	fastRetries(t)
	port := takePort(t)
	l := newListener(port, "http", "", true)
	l.assignProtocol()
	start := time.Now()
	if l.ReopenListener() {
		l.Close()
		t.Fatal("ReopenListener succeeded on a port that is taken")
	}
	if l.Open || l.Listener != nil {
		t.Errorf("open = %t, listener = %v, want nothing open", l.Open, l.Listener)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("ReopenListener did not use the retry delay")
	}
}

func TestReopenListenerWaitsForThePortOfTheOldListener(t *testing.T) {
	setup(t)
	fastRetries(t)
	old := addAndOpen(t, "http")
	port := old.Port
	// The new listener is made on the same port, and takes it over by closing the old one.
	l := newListener(port, "http", "", true)
	l.assignProtocol()
	if !l.ReopenListener() {
		t.Fatal("ReopenListener failed")
	}
	if !old.Restarted || old.Open || !l.Open || l.Listener == nil {
		t.Errorf("old restarted = %t, old open = %t, new open = %t, new listener = %v, want the old one closed and the new one open", old.Restarted, old.Open, l.Open, l.Listener)
	}
	t.Cleanup(l.Close)
	if body := httpGet(t, fmt.Sprintf("http://127.0.0.1:%d/", port), nil); body != "served" {
		t.Errorf("body = %q, want served", body)
	}
}

func TestAddOrUpdateListenerReportsFailedRestart(t *testing.T) {
	setup(t)
	fastRetries(t)
	old := addAndOpen(t, "http")
	port := old.Port
	old.Close()
	foreign, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer foreign.Close()
	l := newListener(port, "http", "", true)
	err, msg := AddOrUpdateListener(l)
	if err != nil || !strings.Contains(msg, "already present, failed to restart") {
		t.Errorf("AddOrUpdateListener = %v, %q, want the failed restart to be reported", err, msg)
	}
	if GetListenerForPort(port) == l {
		t.Error("a listener that couldn't be opened was stored")
	}
}

func TestAddOrUpdateListenerReportsFailedOpen(t *testing.T) {
	setup(t)
	port := takePort(t)
	forget(t, port)
	l := newListener(port, "http", "", true)
	err, msg := AddOrUpdateListener(l)
	if err != nil || !strings.Contains(msg, fmt.Sprintf("Listener %d added but failed to open.", port)) {
		t.Errorf("AddOrUpdateListener = %v, %q, want the failure to open to be reported", err, msg)
	}
	if IsListenerPresent(port) {
		t.Error("a listener that couldn't be opened was stored")
	}
}

func TestStartInitialTCPListeners(t *testing.T) {
	setup(t)
	savedInitial := initialListeners
	savedTCP, savedHTTP, savedRPC := initialTCPStarted, initialHTTPStarted, initialJSONRPCStarted
	savedServe := serveTCP
	var started atomic.Int32
	globalWatcher(&started)
	defer func() {
		initialListeners = savedInitial
		initialTCPStarted, initialHTTPStarted, initialJSONRPCStarted = savedTCP, savedHTTP, savedRPC
		serveTCP = savedServe
	}()
	initialTCPStarted, initialHTTPStarted, initialJSONRPCStarted = false, false, false

	tcpListener := createPortListener(freePort(t), 0, "tcp", false, false, map[int]bool{})
	httpListener := createPortListener(freePort(t), 0, "http", false, false, map[int]bool{})
	initialListeners = []*Listener{tcpListener, httpListener}
	forget(t, tcpListener.Port)
	forget(t, httpListener.Port)

	ConfigureTCPServer(echoTCP)
	if !initialTCPStarted || !isListening(tcpListener.Port) || !IsListenerOpen(tcpListener.Port) {
		t.Errorf("initial TCP started = %t, listening = %t, want the initial TCP listener opened", initialTCPStarted, isListening(tcpListener.Port))
	}
	if IsListenerPresent(httpListener.Port) {
		t.Error("the TCP start opened an HTTP listener")
	}
	if started.Load() != 0 {
		t.Error("the listeners were announced as started before the HTTP and JSONRPC ones were")
	}
	// The last of the three announces that the listeners are started.
	initialHTTPStarted, initialJSONRPCStarted = true, true
	startInitialTCPListeners()
	if started.Load() != 1 {
		t.Errorf("start announcements = %d, want 1 once all of the initial listeners are started", started.Load())
	}
}

func globalWatcher(counter *atomic.Int32) {
	global.AddListenersStartWatcher(func() { counter.Add(1) })
}

func TestStartInitialHTTPAndJSONRPCListeners(t *testing.T) {
	setup(t)
	fastRetries(t)
	savedInitial := initialListeners
	savedTCP, savedHTTP, savedRPC := initialTCPStarted, initialHTTPStarted, initialJSONRPCStarted
	savedGRPC := grpcStarted.Load()
	defer func() {
		initialListeners = savedInitial
		initialTCPStarted, initialHTTPStarted, initialJSONRPCStarted = savedTCP, savedHTTP, savedRPC
		grpcStarted.Store(savedGRPC)
	}()
	initialTCPStarted, initialHTTPStarted, initialJSONRPCStarted = false, false, false

	httpListener := createPortListener(freePort(t), 0, "http", false, false, map[int]bool{})
	rpcListener := createPortListener(freePort(t), 0, "jsonrpc", false, false, map[int]bool{})
	initialListeners = []*Listener{httpListener, rpcListener}
	forget(t, httpListener.Port)
	forget(t, rpcListener.Port)

	// HTTP listeners start when both the HTTP and the GRPC servers are up.
	httpStarted.Store(false)
	grpcStarted.Store(false)
	OnHTTPStart(httpServer, h2Server)
	if initialHTTPStarted || IsListenerPresent(httpListener.Port) {
		t.Error("the initial HTTP listeners started before the GRPC server did")
	}
	OnGRPCStart()
	if !initialHTTPStarted || !isListening(httpListener.Port) {
		t.Errorf("initial HTTP started = %t, listening = %t, want the HTTP listener opened", initialHTTPStarted, isListening(httpListener.Port))
	}
	if IsListenerPresent(rpcListener.Port) {
		t.Error("the HTTP start opened a JSONRPC listener")
	}
	// A second start finds them started, and doesn't touch them.
	gen := httpListener.Generation
	OnGRPCStart()
	if httpListener.Generation != gen {
		t.Errorf("generation = %d, want %d: listeners that started are not started again", httpListener.Generation, gen)
	}

	OnJSONRPCStart(jsonRPCServer, h2Server)
	if !initialJSONRPCStarted || !isListening(rpcListener.Port) {
		t.Errorf("initial JSONRPC started = %t, listening = %t, want the JSONRPC listener opened", initialJSONRPCStarted, isListening(rpcListener.Port))
	}
}

func TestAddInitialGRPCListeners(t *testing.T) {
	setup(t)
	savedInitial := initialListeners
	savedGRPC := grpcStarted.Load()
	defer func() { initialListeners = savedInitial; grpcStarted.Store(savedGRPC) }()
	grpcStarted.Store(false)
	g := createPortListener(freePort(t), 0, "grpc", false, false, map[int]bool{})
	other := createPortListener(freePort(t), 0, "tcp", false, false, map[int]bool{})
	initialListeners = []*Listener{g, other}
	forget(t, g.Port)
	forget(t, other.Port)
	added := grpcStub.added.Load()

	AddInitialGRPCListeners()
	if !g.Open || !IsListenerPresent(g.Port) || IsListenerPresent(other.Port) {
		t.Errorf("grpc open = %t, present = %t, tcp present = %t, want only the grpc listener opened", g.Open, IsListenerPresent(g.Port), IsListenerPresent(other.Port))
	}
	if got := grpcStub.added.Load(); got != added+1 {
		t.Errorf("listeners handed to the GRPC server = %d, want %d", got, added+1)
	}
}

func TestInitDefaultGRPCListener(t *testing.T) {
	setup(t)
	savedPort, savedGRPC := global.Self.GRPCPort, grpcStarted.Load()
	savedLabel, savedListenerPort := DefaultGRPCListener.Label, DefaultGRPCListener.Port
	defer func() {
		global.Self.GRPCPort = savedPort
		grpcStarted.Store(savedGRPC)
		DefaultGRPCListener.Label, DefaultGRPCListener.Port = savedLabel, savedListenerPort
	}()
	grpcStarted.Store(false)
	global.Self.GRPCPort = freePort(t)
	forget(t, global.Self.GRPCPort)
	InitDefaultGRPCListener()
	l := GetListenerForPort(global.Self.GRPCPort)
	if l != DefaultGRPCListener || DefaultGRPCListener.HostLabel != global.Self.HostLabel || !l.IsGRPC {
		t.Errorf("listener of the GRPC port = %v, want the default GRPC listener with the host label", l)
	}
}
