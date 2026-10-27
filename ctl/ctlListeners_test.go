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

package ctl

import (
	"net/http"
	"sync"
	"testing"

	"goto/pkg/global"
	"goto/pkg/server/listeners"
	"goto/pkg/util"
)

var stubOnce sync.Once

// stubGlobalFuncs sets the hooks that the server sets when it starts, which the code of listeners and TCP calls.
func stubGlobalFuncs() {
	global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsListenerPresent = func(int) bool { return false }
	global.Funcs.IsListenerOpen = func(int) bool { return false }
	global.Funcs.GetListenerID = func(int) string { return "" }
	global.Funcs.GetListenerLabel = func(*http.Request) string { return "" }
	global.Funcs.GetListenerLabelForPort = func(int) string { return "" }
	util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
}

// The ports of these listeners are only keys of the listeners that the process has: the listeners are not opened.
const listenersConfig = `
listeners:
  47311:
    label: first-listener
    protocol: http
  47312:
    protocol: tcp
`

func removeListeners(t *testing.T, ports ...int) {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
	t.Cleanup(func() {
		for _, port := range ports {
			if l := listeners.GetListenerForPort(port); l != nil {
				listeners.RemoveListener(l)
			}
		}
	})
}

func TestProcessListenersAddsThemUnderTheirPorts(t *testing.T) {
	removeListeners(t, 47311, 47312)
	config := ParseConfig("listeners.yaml", []byte(listenersConfig))
	if config == nil || len(config.Listeners) != 2 {
		t.Fatalf("config = %+v", config)
	}
	within(t, "processListeners", func() { processListeners(config.Listeners) })
	first := listeners.GetListenerForPort(47311)
	if first == nil || first.Port != 47311 || first.Label != "first-listener" || first.Protocol != "http" {
		t.Fatalf("listener 47311 = %+v", first)
	}
	if first.Open {
		t.Error("the listener was opened, which the config did not ask for")
	}
	second := listeners.GetListenerForPort(47312)
	if second == nil || second.Port != 47312 || second.Protocol != "tcp" || second.Label == "" {
		t.Errorf("listener 47312 = %+v, want one with a label made for the port", second)
	}
}

func TestProcessListenersTakesThePortFromTheKey(t *testing.T) {
	removeListeners(t, 47313)
	config := ParseConfig("listeners.yaml", []byte("listeners:\n  47313:\n    port: 1\n    protocol: http\n"))
	within(t, "processListeners", func() { processListeners(config.Listeners) })
	if l := listeners.GetListenerForPort(47313); l == nil || l.Port != 47313 {
		t.Errorf("listener = %+v, want the port of the key", l)
	}
	if l := listeners.GetListenerForPort(1); l != nil {
		t.Errorf("a listener was added for the port that was in the listener: %+v", l)
	}
}

func TestProcessListenersWithNothingDoesNothing(t *testing.T) {
	stubOnce.Do(stubGlobalFuncs)
	within(t, "processListeners", func() {
		processListeners(nil)
		processListeners(Listeners{})
	})
}

func TestProcessListenersWithAnInvalidPortAddsNothing(t *testing.T) {
	stubOnce.Do(stubGlobalFuncs)
	within(t, "processListeners", func() {
		processListeners(Listeners{70000: &listeners.Listener{Protocol: "http"}, -5: &listeners.Listener{Protocol: "http"}})
	})
	if l := listeners.GetListenerForPort(70000); l != nil {
		t.Errorf("a listener was added for an invalid port: %+v", l)
	}
}
