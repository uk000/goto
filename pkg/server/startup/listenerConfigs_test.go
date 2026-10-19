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

package startup

import (
	"fmt"
	"strings"
	"testing"

	"goto/ctl"
	"goto/pkg/global"
	"goto/pkg/server/listeners"
)

func tcpListenerYAML(ports ...int) string {
	var b strings.Builder
	b.WriteString("listeners:\n")
	for _, p := range ports {
		fmt.Fprintf(&b, "  %d:\n    protocol: tcp\n", p)
	}
	return b.String()
}

func TestProcessListenersNothingToDo(t *testing.T) {
	resetState(t)
	processListeners(nil)
	processListeners(ctl.Listeners{})
	clearListeners(nil)
	clearRemovedListeners(nil, nil)
	clearRemovedListeners(ctl.Listeners{}, nil)
	clearRemovedListeners(nil, ctl.Listeners{})
}

func TestProcessListenersAddsListenersByPort(t *testing.T) {
	resetState(t)
	p1, p2 := newPort(), newPort()
	removeListenersAfter(t, p1, p2)
	logged := captureLog(t)
	ll := ctl.Listeners{p1: {Protocol: "tcp"}, p2: {Protocol: "tcp", CommonName: "named.test"}}
	processListeners(ll)

	l1, l2 := mustListener(t, p1), mustListener(t, p2)
	if l1.Port != p1 || l2.Port != p2 {
		t.Errorf("ports of the listeners = %d, %d, want the ones that they are keyed by: %d, %d", l1.Port, l2.Port, p1, p2)
	}
	if !l1.IsTCP || l1.TCP == nil {
		t.Errorf("listener %d is not set up as a tcp listener: %+v", p1, l1)
	}
	if l1.CommonName != global.ServerConfig.CommonName {
		t.Errorf("common name of a listener without one = %q, want the server's %q", l1.CommonName, global.ServerConfig.CommonName)
	}
	if l2.CommonName != "named.test" {
		t.Errorf("common name = %q, want the configured one", l2.CommonName)
	}
	if n := strings.Count(logged.String(), "Adding Listener"); n != 2 {
		t.Errorf("listeners added = %d, want 2", n)
	}
}

func TestProcessListenersSkipsListenerWithoutConfig(t *testing.T) {
	resetState(t)
	skipped, added := newPort(), newPort()
	removeListenersAfter(t, skipped, added)
	logged := captureLog(t)
	processListeners(ctl.Listeners{skipped: nil, added: {Protocol: "tcp"}})
	if listeners.GetListenerForPort(skipped) != nil {
		t.Error("a listener without a config was added")
	}
	if listeners.GetListenerForPort(added) == nil {
		t.Error("the listener after the one without a config was not added")
	}
	if !strings.Contains(logged.String(), fmt.Sprintf("Listener [%d] has no config", skipped)) {
		t.Errorf("the listener without a config was not reported: %s", logged.String())
	}
}

func TestProcessListenersDoesNotReconfigureTheDefaultPorts(t *testing.T) {
	resetState(t)
	for name, port := range map[string]int{"HTTP": global.Self.ServerPort, "gRPC": global.Self.GRPCPort, "JSONRPC": global.Self.JSONRPCPort} {
		logged := captureLog(t)
		l := &listeners.Listener{Protocol: "tcp"}
		processListeners(ctl.Listeners{port: l})
		if !strings.Contains(logged.String(), "cannot be reconfigured") {
			t.Errorf("%s port %d: reconfiguring it was not refused: %s", name, port, logged.String())
		}
		if l.Port != 0 {
			t.Errorf("%s port %d: the config was applied (port %d)", name, port, l.Port)
		}
	}
}

func TestProcessListenersLeavesAnUnchangedListenerAlone(t *testing.T) {
	resetState(t)
	port := newPort()
	removeListenersAfter(t, port)
	processListeners(ctl.Listeners{port: {Protocol: "tcp"}})
	first := mustListener(t, port)
	logged := captureLog(t)
	processListeners(ctl.Listeners{port: {Protocol: "tcp"}})
	if strings.Contains(logged.String(), "Adding Listener") || strings.Contains(logged.String(), "changed. Reloading") {
		t.Errorf("a listener that did not change was processed again: %s", logged.String())
	}
	if mustListener(t, port) != first {
		t.Error("a listener that did not change was replaced")
	}
}

func TestProcessListenersReloadsAChangedListener(t *testing.T) {
	resetState(t)
	port := newPort()
	removeListenersAfter(t, port)
	processListeners(ctl.Listeners{port: {Protocol: "tcp"}})
	logged := captureLog(t)
	processListeners(ctl.Listeners{port: {Protocol: "tcp", CommonName: "changed.test"}})
	if !strings.Contains(logged.String(), "changed. Reloading") {
		t.Errorf("a changed listener was not reloaded: %s", logged.String())
	}
	if cn := mustListener(t, port).CommonName; cn != "changed.test" {
		t.Errorf("common name = %q, want the changed one", cn)
	}
}

func TestClearListenersRemovesThem(t *testing.T) {
	resetState(t)
	p1, p2 := newPort(), newPort()
	removeListenersAfter(t, p1, p2)
	ll := ctl.Listeners{p1: {Protocol: "tcp"}, p2: {Protocol: "tcp"}}
	processListeners(ll)
	clearListeners(ctl.Listeners{p1: ll[p1], 0: nil}) // an entry without a config is skipped
	if listeners.GetListenerForPort(p1) != nil {
		t.Error("the listener that was cleared is still there")
	}
	if listeners.GetListenerForPort(p2) == nil {
		t.Error("a listener that was not cleared is gone")
	}
}

func TestClearRemovedListenersKeepsTheOnesStillInTheConfig(t *testing.T) {
	resetState(t)
	kept, dropped := newPort(), newPort()
	removeListenersAfter(t, kept, dropped)
	old := ctl.Listeners{kept: {Protocol: "tcp"}, dropped: {Protocol: "tcp"}}
	processListeners(old)
	clearRemovedListeners(old, ctl.Listeners{kept: {Protocol: "tcp"}})
	if listeners.GetListenerForPort(dropped) != nil {
		t.Error("the listener that is not in the new config is still there")
	}
	if listeners.GetListenerForPort(kept) == nil {
		t.Error("the listener that is in the new config was removed")
	}
	clearRemovedListeners(ctl.Listeners{kept: nil}, ctl.Listeners{}) // an old entry without a config is skipped
}

func TestLoadConfigListenersReloadRemovesTheListenersThatTheFileDropped(t *testing.T) {
	resetState(t)
	kept, dropped := newPort(), newPort()
	removeListenersAfter(t, kept, dropped)
	loadConfig(parse(t, "ls.yaml", tcpListenerYAML(kept, dropped)))
	mustListener(t, dropped)
	loadConfig(parse(t, "ls.yaml", tcpListenerYAML(kept)))
	if listeners.GetListenerForPort(dropped) != nil {
		t.Error("the listener that the file dropped is still there")
	}
	mustListener(t, kept)
}

func TestLoadConfigListenersAreKeptByFileAndRemovedWithTheirFile(t *testing.T) {
	resetState(t)
	p1, p2 := newPort(), newPort()
	removeListenersAfter(t, p1, p2)
	loadConfig(parse(t, "one.yaml", tcpListenerYAML(p1)))
	loadConfig(parse(t, "two.yaml", tcpListenerYAML(p2)))
	removeConfigs("/configs/one.yaml")
	if listeners.GetListenerForPort(p1) != nil || listeners.GetListenerForPort(p2) == nil {
		t.Errorf("after the first file was removed: listener %d present %t, listener %d present %t", p1, listeners.GetListenerForPort(p1) != nil, p2, listeners.GetListenerForPort(p2) != nil)
	}
}
