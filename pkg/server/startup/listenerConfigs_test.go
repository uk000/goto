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
	"net"
	"strings"
	"testing"
	"time"

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

// openTCPListenerYAML is a config of tcp listeners that are opened, which is what makes them take their ports.
func openTCPListenerYAML(ports ...int) string {
	var b strings.Builder
	b.WriteString("listeners:\n")
	for _, p := range ports {
		fmt.Fprintf(&b, "  %d:\n    protocol: tcp\n    open: true\n", p)
	}
	return b.String()
}

// portIsFree tells if the port can be bound, which it can't while a listener that was dropped without being closed holds it.
func portIsFree(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

// isAccepting tells if something accepts connections on the port.
func isAccepting(port int) bool {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// configOf gives the listener that the config of a file keeps for a port.
func configOf(file string, port int) *listeners.Listener {
	lock.Lock()
	defer lock.Unlock()
	return listenerConfigs[file][port]
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

// ---- a config whose listener did not change ----
//
// A config whose listener did not change is not applied, so the listener that the file keeps is not the one that is live
// on the port. The file's listener is what is closed when the file goes away or drops the listener, so it must be the live
// one: closing the other one and dropping the port leaves the live listener open, unreachable, and the port taken for good
// (the next load of the listener fails with "bind: address already in use").

func TestProcessListenersKeepsTheLiveListenerForAnUnchangedConfig(t *testing.T) {
	resetState(t)
	port := newPort()
	removeListenersAfter(t, port)
	processListeners(ctl.Listeners{port: {Protocol: "tcp", Open: true}})
	live := mustListener(t, port)

	unchanged := ctl.Listeners{port: {Protocol: "tcp", Open: true}}
	processListeners(unchanged)
	if mustListener(t, port) != live {
		t.Fatal("a listener that did not change was replaced, which the test needs it not to be")
	}
	if unchanged[port] != live {
		t.Errorf("listener that the config keeps for port %d = %p, want the live one %p", port, unchanged[port], live)
	}
}

func TestProcessListenersKeepsTheNewListenerForAChangedConfig(t *testing.T) {
	resetState(t)
	port := newPort()
	removeListenersAfter(t, port)
	processListeners(ctl.Listeners{port: {Protocol: "tcp", Open: true}})
	old := mustListener(t, port)

	changed := ctl.Listeners{port: {Protocol: "tcp", Open: true, CommonName: "changed.test"}}
	configured := changed[port]
	processListeners(changed)
	live := mustListener(t, port)
	if live == old || live != configured {
		t.Fatalf("live listener = %p, want the one of the changed config %p (old one %p)", live, configured, old)
	}
	if changed[port] != live {
		t.Errorf("listener that the config keeps for port %d = %p, want the live one %p", port, changed[port], live)
	}
}

func TestLoadConfigUnchangedReloadKeepsTheFileOnTheLiveListener(t *testing.T) {
	resetState(t)
	port := newPort()
	removeListenersAfter(t, port)
	loadConfig(parse(t, "ls.yaml", openTCPListenerYAML(port)))
	live := mustListener(t, port)
	loadConfig(parse(t, "ls.yaml", openTCPListenerYAML(port)))
	if mustListener(t, port) != live {
		t.Fatal("a listener that did not change was replaced, which the test needs it not to be")
	}
	if got := configOf("ls.yaml", port); got != live {
		t.Errorf("listener that the file keeps for port %d = %p, want the live one %p", port, got, live)
	}
}

func TestRemoveConfigsAfterAnUnchangedReloadClosesTheLiveListener(t *testing.T) {
	resetState(t)
	port := newPort()
	removeListenersAfter(t, port)
	loadConfig(parse(t, "ls.yaml", openTCPListenerYAML(port)))
	live := mustListener(t, port)
	loadConfig(parse(t, "ls.yaml", openTCPListenerYAML(port)))
	if mustListener(t, port) != live {
		t.Fatal("a listener that did not change was replaced, which the test needs it not to be")
	}
	if !isAccepting(port) {
		t.Fatalf("port %d is not open before the file is removed", port)
	}

	removeConfigs("/configs/ls.yaml")
	if l := listeners.GetListenerForPort(port); l != nil {
		t.Errorf("listener of port %d = %p after its file was removed, want none", port, l)
	}
	if !portIsFree(port) {
		t.Fatalf("port %d is still taken after its file was removed: the live listener was dropped without being closed", port)
	}

	// the file comes back, as it does when a config map is changed
	logged := captureLog(t)
	loadConfig(parse(t, "ls.yaml", openTCPListenerYAML(port)))
	if strings.Contains(logged.String(), "address already in use") {
		t.Errorf("the listener could not be opened again: %s", logged.String())
	}
	if !isAccepting(port) {
		t.Errorf("port %d is not open after its file was loaded again", port)
	}
}

func TestRemoveAllConfigsAfterAnUnchangedReloadClosesTheLiveListener(t *testing.T) {
	resetState(t)
	port := newPort()
	removeListenersAfter(t, port)
	loadConfig(parse(t, "ls.yaml", openTCPListenerYAML(port)))
	loadConfig(parse(t, "ls.yaml", openTCPListenerYAML(port)))
	if !isAccepting(port) {
		t.Fatalf("port %d is not open before the configs are removed", port)
	}

	removeAllConfigs()
	if !portIsFree(port) {
		t.Fatalf("port %d is still taken after all the configs were removed: the live listener was dropped without being closed", port)
	}
}

func TestLoadConfigAfterAnUnchangedReloadClosesTheListenersThatTheFileDropped(t *testing.T) {
	resetState(t)
	kept, dropped := newPort(), newPort()
	removeListenersAfter(t, kept, dropped)
	loadConfig(parse(t, "ls.yaml", openTCPListenerYAML(kept, dropped)))
	liveDropped := mustListener(t, dropped)
	loadConfig(parse(t, "ls.yaml", openTCPListenerYAML(kept, dropped)))
	if mustListener(t, dropped) != liveDropped {
		t.Fatal("a listener that did not change was replaced, which the test needs it not to be")
	}

	loadConfig(parse(t, "ls.yaml", openTCPListenerYAML(kept)))
	if listeners.GetListenerForPort(dropped) != nil {
		t.Errorf("the listener that the file dropped is still there")
	}
	if !portIsFree(dropped) {
		t.Errorf("port %d is still taken after the file dropped its listener: the live listener was dropped without being closed", dropped)
	}
	if !isAccepting(kept) {
		t.Errorf("port %d, which the file kept, is not open", kept)
	}
}

func TestClearListenersClosesTheLiveListenerAndNotTheOneOfTheConfig(t *testing.T) {
	removers := map[string]func(port int, config *listeners.Listener){
		"clearListeners": func(port int, config *listeners.Listener) {
			clearListeners(ctl.Listeners{port: config})
		},
		"clearRemovedListeners": func(port int, config *listeners.Listener) {
			clearRemovedListeners(ctl.Listeners{port: config}, ctl.Listeners{})
		},
	}
	for name, remove := range removers {
		t.Run(name, func(t *testing.T) {
			resetState(t)
			port := newPort()
			removeListenersAfter(t, port)
			live := &listeners.Listener{Port: port, Protocol: "tcp", Open: true}
			if err, msg := listeners.AddOrUpdateListener(live); err != nil {
				t.Fatalf("failed to add the live listener: %s", msg)
			}
			if !isAccepting(port) {
				t.Fatalf("port %d is not open", port)
			}
			// the listener of the config has the same settings, and was never opened or registered
			remove(port, &listeners.Listener{Protocol: "tcp"})
			if l := listeners.GetListenerForPort(port); l != nil {
				t.Errorf("listener of port %d = %p after it was removed, want none", port, l)
			}
			if !portIsFree(port) {
				t.Errorf("port %d is still taken after its listener was removed", port)
			}
		})
	}
}

func TestClearListenersLeavesTheDefaultPortsAlone(t *testing.T) {
	resetState(t)
	if global.Self.ServerPort == 0 || global.Self.GRPCPort == 0 {
		t.Fatalf("the default ports are not set (HTTP %d, gRPC %d), so this test would check nothing", global.Self.ServerPort, global.Self.GRPCPort)
	}
	for name, port := range map[string]int{"HTTP": global.Self.ServerPort, "gRPC": global.Self.GRPCPort, "JSONRPC": global.Self.JSONRPCPort} {
		for remover, remove := range map[string]func(){
			"clearListeners":        func() { clearListeners(ctl.Listeners{port: {Protocol: "tcp"}}) },
			"clearRemovedListeners": func() { clearRemovedListeners(ctl.Listeners{port: {Protocol: "tcp"}}, ctl.Listeners{}) },
		} {
			before := listeners.GetListenerForPort(port)
			logged := captureLog(t)
			remove()
			if strings.Contains(logged.String(), "Closing") {
				t.Errorf("%s port %d, %s: the listener of the server was closed: %s", name, port, remover, logged.String())
			}
			if after := listeners.GetListenerForPort(port); after != before {
				t.Errorf("%s port %d, %s: listener of the port = %p, want it unchanged %p", name, port, remover, after, before)
			}
		}
	}
}

// ---- a port that is one of the startup arguments ----
//
// A config can take a port of --ports over with settings of its own. When the config goes away the port goes back to what
// the arguments define for it, and it is not closed: the process was started with that listener.

// startupListener makes the port one of the startup arguments, and opens it as the start of the process does.
func startupListener(t *testing.T, port int) *listeners.Listener {
	t.Helper()
	l := listeners.AddInitialListener(&listeners.PortProtoConfig{Port: port, Proto: "tcp"})
	if l == nil {
		t.Fatalf("port %d could not be made an initial listener", port)
	}
	// registered after resetState, so it is undone before the configs are removed with the last of the cleanups
	t.Cleanup(func() { listeners.RemoveInitialListener(port) })
	removeListenersAfter(t, port)
	if err, msg := listeners.AddOrUpdateListener(l); err != nil {
		t.Fatalf("the initial listener of port %d did not open: %s", port, msg)
	}
	if !isAccepting(port) {
		t.Fatalf("port %d is not open", port)
	}
	return l
}

// changedTCPListenerYAML is a config that takes a tcp port over with a setting of its own.
func changedTCPListenerYAML(port int) string {
	return fmt.Sprintf("listeners:\n  %d:\n    protocol: tcp\n    open: true\n    commonName: changed.test\n", port)
}

func TestRemoveConfigsGivesAStartupPortBackToTheArguments(t *testing.T) {
	resetState(t)
	port := newPort()
	startup := startupListener(t, port)
	loadConfig(parse(t, "ls.yaml", changedTCPListenerYAML(port)))
	taken := mustListener(t, port)
	if taken == startup || taken.CommonName != "changed.test" {
		t.Fatalf("the config did not take the port over: listener %p (common name %q), the startup one is %p", taken, taken.CommonName, startup)
	}

	logged := captureLog(t)
	removeConfigs("/configs/ls.yaml")
	back := mustListener(t, port)
	if back == taken || back.CommonName != global.ServerConfig.CommonName || !back.IsTCP {
		t.Errorf("listener after the config was removed = %p (common name %q), want a new one with the settings of the arguments (the config's was %p)", back, back.CommonName, taken)
	}
	if !isAccepting(port) {
		t.Errorf("port %d is not open after the config was removed", port)
	}
	if !strings.Contains(logged.String(), "back to the startup arguments") || strings.Contains(logged.String(), "Removing Listener [") {
		t.Errorf("a startup port was not reported as going back to the arguments: %s", logged.String())
	}
}

func TestRemoveConfigsLeavesAStartupListenerThatTheConfigDidNotChange(t *testing.T) {
	resetState(t)
	port := newPort()
	startup := startupListener(t, port)
	generation := startup.Generation
	loadConfig(parse(t, "ls.yaml", openTCPListenerYAML(port)))
	if mustListener(t, port) != startup {
		t.Fatal("a config that did not change the listener replaced it, which the test needs it not to")
	}

	removeConfigs("/configs/ls.yaml")
	if mustListener(t, port) != startup || startup.Generation != generation {
		t.Errorf("the startup listener was replaced or restarted when the config that matched it went away (generation %d, was %d)", startup.Generation, generation)
	}
	if !isAccepting(port) {
		t.Errorf("port %d is not open", port)
	}
}

func TestReloadThatDropsAStartupPortGivesItBackToTheArguments(t *testing.T) {
	resetState(t)
	kept, dropped := newPort(), newPort()
	startupListener(t, dropped)
	removeListenersAfter(t, kept)
	loadConfig(parse(t, "ls.yaml", changedTCPListenerYAML(dropped)+fmt.Sprintf("  %d:\n    protocol: tcp\n    open: true\n", kept)))
	taken := mustListener(t, dropped)
	if taken.CommonName != "changed.test" {
		t.Fatalf("the config did not take the port over: common name %q", taken.CommonName)
	}

	loadConfig(parse(t, "ls.yaml", openTCPListenerYAML(kept)))
	back := mustListener(t, dropped)
	if back == taken || back.CommonName != global.ServerConfig.CommonName {
		t.Errorf("listener of the port that the file dropped = %p (common name %q), want a new one with the settings of the arguments (the config's was %p)", back, back.CommonName, taken)
	}
	if !isAccepting(dropped) || !isAccepting(kept) {
		t.Errorf("ports open: dropped %t, kept %t, want both", isAccepting(dropped), isAccepting(kept))
	}
}

func TestRemoveConfigsStillClosesAPortThatIsNotAStartupPort(t *testing.T) {
	resetState(t)
	startupPort, otherPort := newPort(), newPort()
	startupListener(t, startupPort)
	removeListenersAfter(t, otherPort)
	loadConfig(parse(t, "ls.yaml", openTCPListenerYAML(otherPort)))
	if !isAccepting(otherPort) {
		t.Fatalf("port %d is not open", otherPort)
	}

	removeConfigs("/configs/ls.yaml")
	if listeners.GetListenerForPort(otherPort) != nil || !portIsFree(otherPort) {
		t.Errorf("a port that nothing defines but the config is not closed after the config was removed")
	}
	if !isAccepting(startupPort) {
		t.Errorf("the startup port %d, which the config had nothing to do with, is not open", startupPort)
	}
}
