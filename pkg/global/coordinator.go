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
	"sync"

	"golang.org/x/net/http2"
	"google.golang.org/grpc"
)

type IGRPCServer interface {
	AddListener(listener any)
	ServeListener(listener any)
}

type IGRPCService interface {
}

type IGRPCManager interface {
	Reflect(s *grpc.ServiceDesc)
	InterceptAndServe(s *grpc.ServiceDesc, srv any)
	InterceptWithMiddleware(s *grpc.ServiceDesc, srv any)
	InterceptAndProxy(fromGSD, toGSD *grpc.ServiceDesc, target string, srv any, teeport int) (IGRPCService, IGRPCService)
}

type HTTPStartWatcher func(server *http.Server, h2s *http2.Server)
type HTTPStopWatcher func()
type TCPServerWatcher func(func(listenerID string, port int, listener net.Listener) error)
type UDPServerWatcher func(func(listenerID string, port int, listener net.Listener) error)
type GRPCStartListener func()
type GRPCStopListener func()
type GRPCInterceptor func(server IGRPCManager)
type ListenersStartWatcher func()
type ConnOpenWatcher func(net.Conn)
type ConnCloseWatcher func(net.Conn)

var (
	httpStartWatchers      []HTTPStartWatcher
	httpStopWatchers       []HTTPStopWatcher
	mcpStartWatchers       []HTTPStartWatcher
	mcpStopWatchers        []HTTPStopWatcher
	tcpServeWatchers       []TCPServerWatcher
	grpcStartListeners     []GRPCStartListener
	grpcStopListeners      []GRPCStopListener
	grpcIntercepts         []GRPCInterceptor
	listenersStartWatchers []ListenersStartWatcher
	connOpenWatchers       []ConnOpenWatcher
	connCloseWatchers      []ConnCloseWatcher

	GRPCServer    IGRPCServer
	GRPCManager   IGRPCManager
	ShutdownFuncs = []func(){}

	lock sync.RWMutex
)

// AddHTTPStartWatcher registers a watcher to be called when the HTTP server starts.
func AddHTTPStartWatcher(w HTTPStartWatcher) {
	lock.Lock()
	defer lock.Unlock()
	httpStartWatchers = append(httpStartWatchers, w)
}

// AddHTTPStopWatcher registers a watcher to be called when the HTTP server stops.
func AddHTTPStopWatcher(w HTTPStopWatcher) {
	lock.Lock()
	defer lock.Unlock()
	httpStopWatchers = append(httpStopWatchers, w)
}

// AddJSONRPCStartWatcher registers a watcher to be called when the JSON-RPC (MCP) server starts.
func AddJSONRPCStartWatcher(w HTTPStartWatcher) {
	lock.Lock()
	defer lock.Unlock()
	mcpStartWatchers = append(mcpStartWatchers, w)
}

// AddJSONRPCStopWatcher registers a watcher to be called when the JSON-RPC (MCP) server stops.
func AddJSONRPCStopWatcher(w HTTPStopWatcher) {
	lock.Lock()
	defer lock.Unlock()
	mcpStopWatchers = append(mcpStopWatchers, w)
}

// AddTCPServeWatcher registers a watcher that is handed the TCP serve function when TCP servers are configured.
func AddTCPServeWatcher(w TCPServerWatcher) {
	lock.Lock()
	defer lock.Unlock()
	tcpServeWatchers = append(tcpServeWatchers, w)
}

// AddGRPCStartWatcher registers a listener to be called when the gRPC server starts.
func AddGRPCStartWatcher(w GRPCStartListener) {
	lock.Lock()
	defer lock.Unlock()
	grpcStartListeners = append(grpcStartListeners, w)
}

// AddGRPCStopWatcher registers a listener to be called when the gRPC server stops.
func AddGRPCStopWatcher(w GRPCStopListener) {
	lock.Lock()
	defer lock.Unlock()
	grpcStopListeners = append(grpcStopListeners, w)
}

// AddGRPCIntercept registers an interceptor that is given the gRPC manager to wrap or register services.
func AddGRPCIntercept(w GRPCInterceptor) {
	lock.Lock()
	defer lock.Unlock()
	grpcIntercepts = append(grpcIntercepts, w)
}

// AddListenersStartWatcher registers a watcher to be called once the listeners have started.
func AddListenersStartWatcher(w ListenersStartWatcher) {
	lock.Lock()
	defer lock.Unlock()
	listenersStartWatchers = append(listenersStartWatchers, w)
}

// AddConnOpenWatcher registers a watcher to be called with each newly opened connection.
func AddConnOpenWatcher(w ConnOpenWatcher) {
	lock.Lock()
	defer lock.Unlock()
	connOpenWatchers = append(connOpenWatchers, w)
}

// AddConnCloseWatcher registers a watcher to be called with each closed connection.
func AddConnCloseWatcher(w ConnCloseWatcher) {
	lock.Lock()
	defer lock.Unlock()
	connCloseWatchers = append(connCloseWatchers, w)
}

// OnListenersStarted notifies all registered listeners-start watchers.
func OnListenersStarted() {
	lock.RLock()
	defer lock.RUnlock()
	for _, w := range listenersStartWatchers {
		w()
	}
}

// OnHTTPStart calls all registered HTTP start watchers with the server.
func OnHTTPStart(server *http.Server, h2s *http2.Server) {
	lock.RLock()
	defer lock.RUnlock()
	for _, w := range httpStartWatchers {
		w(server, h2s)
	}
}

// OnHTTPStop calls all registered HTTP stop watchers.
func OnHTTPStop() {
	lock.RLock()
	defer lock.RUnlock()
	for _, w := range httpStopWatchers {
		w()
	}
}

// OnJSONRPCStart calls all registered JSON-RPC (MCP) start watchers with the server.
func OnJSONRPCStart(server *http.Server, h2s *http2.Server) {
	lock.RLock()
	defer lock.RUnlock()
	for _, w := range mcpStartWatchers {
		w(server, h2s)
	}
}

// OnMCPStop calls all registered JSON-RPC (MCP) stop watchers.
func OnMCPStop() {
	lock.RLock()
	defer lock.RUnlock()
	for _, w := range mcpStopWatchers {
		w()
	}
}

// OnConnOpen calls all registered watchers with the opened connection.
func OnConnOpen(c net.Conn) {
	lock.RLock()
	defer lock.RUnlock()
	for _, w := range connOpenWatchers {
		w(c)
	}
}

// OnConnClose calls all registered watchers with the closed connection.
func OnConnClose(c net.Conn) {
	lock.RLock()
	defer lock.RUnlock()
	for _, w := range connCloseWatchers {
		w(c)
	}
}

// ConfigureTCPServer hands the TCP serve function to every registered TCP serve watcher.
func ConfigureTCPServer(serve func(listenerID string, port int, listener net.Listener) error) {
	lock.RLock()
	defer lock.RUnlock()
	for _, w := range tcpServeWatchers {
		w(serve)
	}
}

// OnGRPCStart calls all registered gRPC start listeners.
func OnGRPCStart() {
	lock.RLock()
	defer lock.RUnlock()
	for _, w := range grpcStartListeners {
		w()
	}
}

// OnGRPCStop calls all registered gRPC stop listeners.
func OnGRPCStop() {
	lock.RLock()
	defer lock.RUnlock()
	for _, w := range grpcStopListeners {
		w()
	}
}

// GRPCIntercept gives the gRPC manager to every registered interceptor.
func GRPCIntercept(server IGRPCManager) {
	for _, w := range grpcIntercepts {
		w(server)
	}
}

// Shutdown runs all registered shutdown functions in registration order.
func Shutdown() {
	for _, f := range ShutdownFuncs {
		if f != nil {
			f()
		}
	}
}

// OnShutdown registers the non-nil functions to run at Shutdown, and returns true so it can initialize a package var.
func OnShutdown(funcs ...func()) bool {
	for _, fn := range funcs {
		if fn != nil {
			ShutdownFuncs = append(ShutdownFuncs, fn)
		}
	}
	return true
}
