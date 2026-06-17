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

func AddHTTPStartWatcher(w HTTPStartWatcher) {
	lock.Lock()
	defer lock.Unlock()
	httpStartWatchers = append(httpStartWatchers, w)
}

func AddHTTPStopWatcher(w HTTPStopWatcher) {
	lock.Lock()
	defer lock.Unlock()
	httpStopWatchers = append(httpStopWatchers, w)
}

func AddJSONRPCStartWatcher(w HTTPStartWatcher) {
	lock.Lock()
	defer lock.Unlock()
	mcpStartWatchers = append(mcpStartWatchers, w)
}

func AddJSONRPCStopWatcher(w HTTPStopWatcher) {
	lock.Lock()
	defer lock.Unlock()
	mcpStopWatchers = append(mcpStopWatchers, w)
}

func AddTCPServeWatcher(w TCPServerWatcher) {
	lock.Lock()
	defer lock.Unlock()
	tcpServeWatchers = append(tcpServeWatchers, w)
}

func AddGRPCStartWatcher(w GRPCStartListener) {
	lock.Lock()
	defer lock.Unlock()
	grpcStartListeners = append(grpcStartListeners, w)
}

func AddGRPCStopWatcher(w GRPCStopListener) {
	lock.Lock()
	defer lock.Unlock()
	grpcStopListeners = append(grpcStopListeners, w)
}

func AddGRPCIntercept(w GRPCInterceptor) {
	lock.Lock()
	defer lock.Unlock()
	grpcIntercepts = append(grpcIntercepts, w)
}

func AddListenersStartWatcher(w ListenersStartWatcher) {
	lock.Lock()
	defer lock.Unlock()
	listenersStartWatchers = append(listenersStartWatchers, w)
}

func AddConnOpenWatcher(w ConnOpenWatcher) {
	lock.Lock()
	defer lock.Unlock()
	connOpenWatchers = append(connOpenWatchers, w)
}

func AddConnCloseWatcher(w ConnCloseWatcher) {
	lock.Lock()
	defer lock.Unlock()
	connCloseWatchers = append(connCloseWatchers, w)
}

func OnListenersStarted() {
	lock.RLock()
	defer lock.RUnlock()
	for _, w := range listenersStartWatchers {
		w()
	}
}

func OnHTTPStart(server *http.Server, h2s *http2.Server) {
	lock.RLock()
	defer lock.RUnlock()
	for _, w := range httpStartWatchers {
		w(server, h2s)
	}
}

func OnHTTPStop() {
	lock.RLock()
	defer lock.RUnlock()
	for _, w := range httpStopWatchers {
		w()
	}
}

func OnJSONRPCStart(server *http.Server, h2s *http2.Server) {
	lock.RLock()
	defer lock.RUnlock()
	for _, w := range mcpStartWatchers {
		w(server, h2s)
	}
}

func OnMCPStop() {
	lock.RLock()
	defer lock.RUnlock()
	for _, w := range mcpStopWatchers {
		w()
	}
}

func OnConnOpen(c net.Conn) {
	lock.RLock()
	defer lock.RUnlock()
	for _, w := range connOpenWatchers {
		w(c)
	}
}

func OnConnClose(c net.Conn) {
	lock.RLock()
	defer lock.RUnlock()
	for _, w := range connCloseWatchers {
		w(c)
	}
}

func ConfigureTCPServer(serve func(listenerID string, port int, listener net.Listener) error) {
	lock.RLock()
	defer lock.RUnlock()
	for _, w := range tcpServeWatchers {
		w(serve)
	}
}

func OnGRPCStart() {
	lock.RLock()
	defer lock.RUnlock()
	for _, w := range grpcStartListeners {
		w()
	}
}

func OnGRPCStop() {
	lock.RLock()
	defer lock.RUnlock()
	for _, w := range grpcStopListeners {
		w()
	}
}

func GRPCIntercept(server IGRPCManager) {
	for _, w := range grpcIntercepts {
		w(server)
	}
}

func Shutdown() {
	for _, f := range ShutdownFuncs {
		if f != nil {
			f()
		}
	}
}

func OnShutdown(funcs ...func()) bool {
	for _, fn := range funcs {
		if fn != nil {
			ShutdownFuncs = append(ShutdownFuncs, fn)
		}
	}
	return true
}
