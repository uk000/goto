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
)

type IWatchedServer interface {
	Serve(l interface{})
}
type GRPCStartWatcher func(server IWatchedServer)
type GRPCStopWatcher func()
type HTTPStartWatcher func(server *http.Server)
type HTTPStopWatcher func()
type TCPServeWatcher func(func(listenerID string, port int, listener net.Listener) error)

var (
	grpcStartWatchers []GRPCStartWatcher
	grpcStopWatchers  []GRPCStopWatcher
	httpStartWatchers []HTTPStartWatcher
	httpStopWatchers  []HTTPStopWatcher
	tcpServeWatchers  []TCPServeWatcher
	ShutdownFuncs     = []func(){}
)

func AddGRPCStartWatcher(w GRPCStartWatcher) {
	grpcStartWatchers = append(grpcStartWatchers, w)
}

func AddGRPCStopWatcher(w GRPCStopWatcher) {
	grpcStopWatchers = append(grpcStopWatchers, w)
}

func AddHTTPStartWatcher(w HTTPStartWatcher) {
	httpStartWatchers = append(httpStartWatchers, w)
}

func AddHTTPStopWatcher(w HTTPStopWatcher) {
	httpStopWatchers = append(httpStopWatchers, w)
}

func AddTCPServeWatcher(w TCPServeWatcher) {
	tcpServeWatchers = append(tcpServeWatchers, w)
}

func OnGRPCStart(server IWatchedServer) {
	for _, w := range grpcStartWatchers {
		w(server)
	}
}

func OnGRPCStop() {
	for _, w := range grpcStopWatchers {
		w()
	}
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

func ConfigureTCPServer(serve func(listenerID string, port int, listener net.Listener) error) {
	for _, w := range tcpServeWatchers {
		w(serve)
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
