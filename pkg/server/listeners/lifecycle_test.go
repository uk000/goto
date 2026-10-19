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

import "testing"

// These tests look at the flags of the servers that are started, which the listeners keep as atomics.

func TestOnGRPCStartAndStop(t *testing.T) {
	setup(t)
	saved := grpcStarted.Load()
	defer func() { grpcStarted.Store(saved) }()
	l, err := AddGRPCListener(freePort(t), false)
	if err != nil {
		t.Fatal(err)
	}
	forget(t, l.Port)
	httpWas := httpStarted.Load()
	httpStarted.Store(false)
	defer func() { httpStarted.Store(httpWas) }()
	OnGRPCStart()
	if !grpcStarted.Load() {
		t.Error("OnGRPCStart did not mark GRPC as started")
	}
	OnGRPCStop()
	if grpcStarted.Load() {
		t.Error("OnGRPCStop did not mark GRPC as stopped")
	}
	if l.Open || l.Listener != nil || isListening(l.Port) {
		t.Errorf("after OnGRPCStop: open = %t, listener = %v, listening = %t, want the GRPC listeners closed", l.Open, l.Listener, isListening(l.Port))
	}
}

func TestOnHTTPAndJSONRPCStartStop(t *testing.T) {
	setup(t)
	// The servers are kept as they are. The start and stop only hand them over, and start the initial listeners, of which
	// there are none here.
	savedInitial := initialListeners
	initialListeners = nil
	started, startedRPC := initialHTTPStarted, initialJSONRPCStarted
	initialHTTPStarted, initialJSONRPCStarted = true, true
	grpcWas := grpcStarted.Load()
	grpcStarted.Store(false)
	defer func() {
		initialListeners = savedInitial
		initialHTTPStarted, initialJSONRPCStarted = started, startedRPC
		grpcStarted.Store(grpcWas)
	}()
	OnHTTPStart(httpServer, h2Server)
	if !httpStarted.Load() || httpServer == nil || h2Server == nil {
		t.Errorf("httpStarted = %t, server = %v, h2 = %v after OnHTTPStart", httpStarted.Load(), httpServer, h2Server)
	}
	server := httpServer
	OnHTTPStop()
	if httpStarted.Load() || httpServer != nil {
		t.Errorf("httpStarted = %t, server = %v after OnHTTPStop, want stopped", httpStarted.Load(), httpServer)
	}
	httpServer = server
	OnJSONRPCStart(jsonRPCServer, h2Server)
	if !jsonRPCStarted.Load() || jsonRPCServer == nil {
		t.Errorf("jsonRPCStarted = %t, server = %v after OnJSONRPCStart", jsonRPCStarted.Load(), jsonRPCServer)
	}
	rpc := jsonRPCServer
	OnJSONRPCStop()
	if jsonRPCStarted.Load() || jsonRPCServer != nil {
		t.Errorf("jsonRPCStarted = %t, server = %v after OnJSONRPCStop, want stopped", jsonRPCStarted.Load(), jsonRPCServer)
	}
	jsonRPCServer = rpc
}
