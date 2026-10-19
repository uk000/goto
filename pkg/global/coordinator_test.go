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

package global

import (
	"net"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"golang.org/x/net/http2"
	"google.golang.org/grpc"
)

// emptyCoordinator removes all of the watchers for the test, and gives back the ones that were there when it is done.
func emptyCoordinator(t *testing.T) {
	t.Helper()
	lock.Lock()
	saved := struct {
		httpStart, mcpStart []HTTPStartWatcher
		httpStop, mcpStop   []HTTPStopWatcher
		tcp                 []TCPServerWatcher
		grpcStart           []GRPCStartListener
		grpcStop            []GRPCStopListener
		intercepts          []GRPCInterceptor
		listeners           []ListenersStartWatcher
		connOpen            []ConnOpenWatcher
		connClose           []ConnCloseWatcher
		shutdown            []func()
		grpcServer          IGRPCServer
		grpcManager         IGRPCManager
	}{httpStartWatchers, mcpStartWatchers, httpStopWatchers, mcpStopWatchers, tcpServeWatchers, grpcStartListeners,
		grpcStopListeners, grpcIntercepts, listenersStartWatchers, connOpenWatchers, connCloseWatchers, ShutdownFuncs,
		GRPCServer, GRPCManager}
	httpStartWatchers, mcpStartWatchers, httpStopWatchers, mcpStopWatchers = nil, nil, nil, nil
	tcpServeWatchers, grpcStartListeners, grpcStopListeners, grpcIntercepts = nil, nil, nil, nil
	listenersStartWatchers, connOpenWatchers, connCloseWatchers, ShutdownFuncs = nil, nil, nil, []func(){}
	lock.Unlock()
	t.Cleanup(func() {
		lock.Lock()
		defer lock.Unlock()
		httpStartWatchers, mcpStartWatchers, httpStopWatchers, mcpStopWatchers = saved.httpStart, saved.mcpStart, saved.httpStop, saved.mcpStop
		tcpServeWatchers, grpcStartListeners, grpcStopListeners, grpcIntercepts = saved.tcp, saved.grpcStart, saved.grpcStop, saved.intercepts
		listenersStartWatchers, connOpenWatchers, connCloseWatchers, ShutdownFuncs = saved.listeners, saved.connOpen, saved.connClose, saved.shutdown
		GRPCServer, GRPCManager = saved.grpcServer, saved.grpcManager
	})
}

type fakeConn struct{ net.Conn }

func (fakeConn) String() string { return "fake" }

type fakeManager struct{ IGRPCManager }

func TestHTTPWatchersAreCalledInOrderOfRegistration(t *testing.T) {
	emptyCoordinator(t)
	var calls []string
	server, h2s := &http.Server{Addr: ":1"}, &http2.Server{}
	AddHTTPStartWatcher(func(s *http.Server, h *http2.Server) {
		if s != server || h != h2s {
			t.Errorf("first start watcher got %v %v, want the server that started", s, h)
		}
		calls = append(calls, "start1")
	})
	AddHTTPStartWatcher(func(*http.Server, *http2.Server) { calls = append(calls, "start2") })
	AddHTTPStopWatcher(func() { calls = append(calls, "stop1") })
	AddHTTPStopWatcher(func() { calls = append(calls, "stop2") })
	OnHTTPStart(server, h2s)
	OnHTTPStop()
	OnHTTPStop()
	want := []string{"start1", "start2", "stop1", "stop2", "stop1", "stop2"}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("calls = %v, want %v", calls, want)
	}
}

func TestJSONRPCWatchersAreSeparateFromHTTPOnes(t *testing.T) {
	emptyCoordinator(t)
	var calls []string
	server, h2s := &http.Server{}, &http2.Server{}
	AddHTTPStartWatcher(func(*http.Server, *http2.Server) { calls = append(calls, "http start") })
	AddHTTPStopWatcher(func() { calls = append(calls, "http stop") })
	AddJSONRPCStartWatcher(func(s *http.Server, h *http2.Server) {
		if s != server || h != h2s {
			t.Errorf("start watcher got %v %v, want the server that started", s, h)
		}
		calls = append(calls, "rpc start")
	})
	AddJSONRPCStopWatcher(func() { calls = append(calls, "rpc stop") })
	OnJSONRPCStart(server, h2s)
	if !reflect.DeepEqual(calls, []string{"rpc start"}) {
		t.Fatalf("calls = %v, want only the JSON-RPC start", calls)
	}
	OnMCPStop()
	if !reflect.DeepEqual(calls, []string{"rpc start", "rpc stop"}) {
		t.Errorf("calls = %v, want the JSON-RPC stop after the start", calls)
	}
}

func TestOnEventsWithoutWatchersDoNothing(t *testing.T) {
	emptyCoordinator(t)
	OnHTTPStart(nil, nil)
	OnHTTPStop()
	OnJSONRPCStart(nil, nil)
	OnMCPStop()
	OnGRPCStart()
	OnGRPCStop()
	OnListenersStarted()
	OnConnOpen(nil)
	OnConnClose(nil)
	ConfigureTCPServer(nil)
	GRPCIntercept(nil)
	Shutdown()
}

func TestConnWatchersGetTheConnection(t *testing.T) {
	emptyCoordinator(t)
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	var opened, closed []net.Conn
	AddConnOpenWatcher(func(c net.Conn) { opened = append(opened, c) })
	AddConnCloseWatcher(func(c net.Conn) { closed = append(closed, c) })
	OnConnOpen(c1)
	OnConnClose(c2)
	if len(opened) != 1 || opened[0] != c1 || len(closed) != 1 || closed[0] != c2 {
		t.Errorf("opened = %v, closed = %v, want the connection of each call", opened, closed)
	}
}

func TestTCPServeWatchersGetTheServeFunction(t *testing.T) {
	emptyCoordinator(t)
	type served struct {
		id   string
		port int
	}
	var got []served
	serve := func(id string, port int, l net.Listener) error {
		got = append(got, served{id, port})
		return nil
	}
	var watchers int
	for i := 0; i < 2; i++ {
		AddTCPServeWatcher(func(s func(string, int, net.Listener) error) {
			watchers++
			if err := s("listener", 9000+watchers, nil); err != nil {
				t.Error(err)
			}
		})
	}
	ConfigureTCPServer(serve)
	if watchers != 2 || !reflect.DeepEqual(got, []served{{"listener", 9001}, {"listener", 9002}}) {
		t.Errorf("watchers = %d, served = %v", watchers, got)
	}
}

func TestGRPCListenersAndIntercepts(t *testing.T) {
	emptyCoordinator(t)
	var calls []string
	AddGRPCStartWatcher(func() { calls = append(calls, "start") })
	AddGRPCStopWatcher(func() { calls = append(calls, "stop") })
	AddListenersStartWatcher(func() { calls = append(calls, "listeners") })
	manager := &fakeManager{}
	AddGRPCIntercept(func(m IGRPCManager) {
		if m != IGRPCManager(manager) {
			t.Errorf("intercept got %v, want the manager that was given", m)
		}
		calls = append(calls, "intercept1")
	})
	AddGRPCIntercept(func(IGRPCManager) { calls = append(calls, "intercept2") })
	OnListenersStarted()
	OnGRPCStart()
	GRPCIntercept(manager)
	OnGRPCStop()
	want := []string{"listeners", "start", "intercept1", "intercept2", "stop"}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("calls = %v, want %v", calls, want)
	}
}

func TestShutdownRunsFunctionsInOrderAndSkipsNil(t *testing.T) {
	emptyCoordinator(t)
	var calls []int
	if !OnShutdown(func() { calls = append(calls, 1) }, nil, func() { calls = append(calls, 2) }) {
		t.Error("OnShutdown did not report success")
	}
	if !OnShutdown(func() { calls = append(calls, 3) }) || !OnShutdown() {
		t.Error("OnShutdown did not report success")
	}
	if len(ShutdownFuncs) != 3 {
		t.Errorf("%d shutdown functions, want 3: a nil function is not kept", len(ShutdownFuncs))
	}
	Shutdown()
	if !reflect.DeepEqual(calls, []int{1, 2, 3}) {
		t.Errorf("calls = %v, want the functions in order", calls)
	}
	Shutdown()
	if !reflect.DeepEqual(calls, []int{1, 2, 3, 1, 2, 3}) {
		t.Errorf("calls = %v, want a second shutdown to run them again", calls)
	}
}

func TestShutdownFuncsGivenBeforeAreKept(t *testing.T) {
	emptyCoordinator(t)
	n := 0
	ShutdownFuncs = append(ShutdownFuncs, func() { n++ }, nil)
	Shutdown()
	if n != 1 {
		t.Errorf("ran %d functions, want 1 (and no panic on a nil one)", n)
	}
}

func TestWatchersCanBeAddedWhileEventsAreSent(t *testing.T) {
	emptyCoordinator(t)
	var count atomic.Int64
	var wg sync.WaitGroup
	const adders, perAdder = 4, 50
	for a := 0; a < adders; a++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perAdder; i++ {
				AddHTTPStopWatcher(func() { count.Add(1) })
				AddConnOpenWatcher(func(net.Conn) {})
				OnHTTPStop()
				OnConnOpen(nil)
			}
		}()
	}
	wg.Wait()
	before := count.Load()
	OnHTTPStop()
	if got := count.Load() - before; got != adders*perAdder {
		t.Errorf("a stop called %d watchers, want all %d that were added", got, adders*perAdder)
	}
}

func TestInterfacesCanBeImplementedAndKept(t *testing.T) {
	emptyCoordinator(t)
	GRPCManager = &fakeManager{}
	GRPCServer = &fakeServer{}
	GRPCServer.AddListener(1)
	GRPCServer.ServeListener(2)
	if s := GRPCServer.(*fakeServer); !reflect.DeepEqual(s.calls, []string{"add", "serve"}) {
		t.Errorf("calls = %v", s.calls)
	}
	var _ IGRPCService = struct{}{}
	var _ *grpc.ServiceDesc
}

type fakeServer struct{ calls []string }

func (f *fakeServer) AddListener(any)   { f.calls = append(f.calls, "add") }
func (f *fakeServer) ServeListener(any) { f.calls = append(f.calls, "serve") }
