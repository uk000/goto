/**
 * Copyright 2025 uk
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

type HTTPStartWatcher func(server *http.Server)
type HTTPStopWatcher func()
type TCPServeWatcher func(func(listenerID string, port int, listener net.Listener) error)
type GRPCStartListener func()
type GRPCStopListener func()
type GRPCInterceptor func(server IGRPCManager)

var (
	httpStartWatchers  []HTTPStartWatcher
	httpStopWatchers   []HTTPStopWatcher
	mcpStartWatchers   []HTTPStartWatcher
	mcpStopWatchers    []HTTPStopWatcher
	tcpServeWatchers   []TCPServeWatcher
	grpcStartListeners []GRPCStartListener
	grpcStopListeners  []GRPCStopListener
	grpcIntercepts     []GRPCInterceptor
	GRPCServer         IGRPCServer
	GRPCManager        IGRPCManager
	ShutdownFuncs      = []func(){}
)

func AddHTTPStartWatcher(w HTTPStartWatcher) {
	httpStartWatchers = append(httpStartWatchers, w)
}

func AddHTTPStopWatcher(w HTTPStopWatcher) {
	httpStopWatchers = append(httpStopWatchers, w)
}

func AddMCPStartWatcher(w HTTPStartWatcher) {
	mcpStartWatchers = append(mcpStartWatchers, w)
}

func AddMCPStopWatcher(w HTTPStopWatcher) {
	mcpStopWatchers = append(mcpStopWatchers, w)
}

func AddTCPServeWatcher(w TCPServeWatcher) {
	tcpServeWatchers = append(tcpServeWatchers, w)
}

func AddGRPCStartWatcher(w GRPCStartListener) {
	grpcStartListeners = append(grpcStartListeners, w)
}

func AddGRPCStopWatcher(w GRPCStopListener) {
	grpcStopListeners = append(grpcStopListeners, w)
}

func AddGRPCIntercept(w GRPCInterceptor) {
	grpcIntercepts = append(grpcIntercepts, w)
}

func OnHTTPStart(server *http.Server) {
	for _, w := range httpStartWatchers {
		w(server)
	}
}

func OnHTTPStop() {
	for _, w := range httpStopWatchers {
		w()
	}
}

func OnMCPStart(server *http.Server) {
	for _, w := range mcpStartWatchers {
		w(server)
	}
}

func OnMCPStop() {
	for _, w := range mcpStopWatchers {
		w()
	}
}

func ConfigureTCPServer(serve func(listenerID string, port int, listener net.Listener) error) {
	for _, w := range tcpServeWatchers {
		w(serve)
	}
}

func OnGRPCStart() {
	for _, w := range grpcStartListeners {
		w()
	}
}

func OnGRPCStop() {
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
