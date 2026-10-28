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
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"

	"goto/pkg/global"
	tcpproxy "goto/pkg/proxy/tcp"
	"goto/pkg/server/listeners"
)

// freePort gives a port that nothing listens on, for the tests that make the proxy open its port.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// connectOf tells what the proxy of a port reports of its CONNECT proxy: if the mode is on, and the delay that it has.
func connectOf(t *testing.T, port int) (bool, map[string]any) {
	t.Helper()
	b, err := json.Marshal(tcpproxy.GetPortProxy(port))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		HTTP struct {
			Connect      bool
			ConnectDelay map[string]any
		}
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	return got.HTTP.Connect, got.HTTP.ConnectDelay
}

func connectProxyYAML(port int, extra string) string {
	return fmt.Sprintf(`
proxy:
- tcp:
    port: %d
    enabled: true
%s`, port, extra)
}

const connectOn = `    http:
      connect: true
      connectDelay:
        min: 1s
        max: 2s
        count: 5
`

func TestLoadTCPProxyMakesThePortAConnectProxyWithItsDelay(t *testing.T) {
	resetState(t)
	port := freePort(t)
	removeListenersAfter(t, port)
	loadConfig(parse(t, "connect.yaml", connectProxyYAML(port, connectOn)))

	if !tcpproxy.WillProxyConnect(port) {
		t.Fatal("the port is not a CONNECT proxy port")
	}
	if l := mustListener(t, port); !l.IsTCP || !l.Open {
		t.Errorf("the port is not an open TCP port: %+v", l)
	}
	on, delay := connectOf(t, port)
	if !on || delay["min"] != "1s" || delay["max"] != "2s" || delay["count"] != float64(5) {
		t.Errorf("connect = %v, delay = %v", on, delay)
	}
	// the proxy counts its delay down on its own, the config keeps the one that it read
	if d := tcpProxyConfigs["connect.yaml"][port].HTTP.ConnectDelay; d == nil || d.Count != 5 || d.Min == nil || d.Max == nil {
		t.Errorf("the delay of the config = %+v", d)
	}
	if tcpproxy.WillProxyTCP(port) {
		t.Error("a port that has no upstreams is a TCP proxy port")
	}
}

func TestLoadTCPProxyWithoutADelayOrCountHasUnlimitedDelayOrNone(t *testing.T) {
	resetState(t)
	p1, p2 := freePort(t), freePort(t)
	removeListenersAfter(t, p1, p2)
	loadConfig(parse(t, "plain.yaml", connectProxyYAML(p1, "    http:\n      connect: true\n")))
	loadConfig(parse(t, "ranged.yaml", connectProxyYAML(p2, "    http:\n      connect: true\n      connectDelay:\n        min: 100ms\n        max: 300ms\n")))
	if on, delay := connectOf(t, p1); !on || delay != nil {
		t.Errorf("connect = %v, delay = %v, want a proxy without a delay", on, delay)
	}
	if on, delay := connectOf(t, p2); !on || delay["min"] != "100ms" || delay["max"] != "300ms" || delay["count"] != float64(-1) {
		t.Errorf("connect = %v, delay = %v, want the delay to be unlimited", on, delay)
	}
}

func TestLoadTCPProxyKeepsTheListenerThatTheConfigDeclared(t *testing.T) {
	resetState(t)
	port := freePort(t)
	removeListenersAfter(t, port)
	loadConfig(parse(t, "listener.yaml", fmt.Sprintf("listeners:\n  %d:\n    protocol: tcp\n    open: true\n", port)+connectProxyYAML(port, connectOn)))
	l := mustListener(t, port)
	if !l.IsTCP || !l.Open || !tcpproxy.WillProxyConnect(port) {
		t.Errorf("listener = %+v, connect = %v", l, tcpproxy.WillProxyConnect(port))
	}
	// loaded again, the port is the same one
	loadConfig(parse(t, "listener.yaml", fmt.Sprintf("listeners:\n  %d:\n    protocol: tcp\n    open: true\n", port)+connectProxyYAML(port, connectOn)))
	if mustListener(t, port) != l {
		t.Error("the listener was replaced by the reload")
	}
	if !tcpproxy.WillProxyConnect(port) {
		t.Error("the mode is off after the reload")
	}
}

func TestLoadTCPProxyWithUpstreamsAndConnectLoadsBoth(t *testing.T) {
	resetState(t)
	port := freePort(t)
	removeListenersAfter(t, port)
	loadConfig(parse(t, "both.yaml", connectProxyYAML(port, `    upstreams:
      u:
        endpoints:
          e:
            address: 127.0.0.1:1
`+connectOn)))
	if !tcpproxy.WillProxyConnect(port) || !tcpproxy.WillProxyTCP(port) {
		t.Errorf("connect = %v, tcp = %v", tcpproxy.WillProxyConnect(port), tcpproxy.WillProxyTCP(port))
	}
}

func TestLoadTCPProxyUpstreamsThatFailValidationDoNotKeepTheConnectProxyFromLoading(t *testing.T) {
	resetState(t)
	port := freePort(t)
	removeListenersAfter(t, port)
	logged := captureLog(t)
	loadConfig(parse(t, "invalid.yaml", connectProxyYAML(port, "    upstreams:\n      u:\n        name: no-endpoints\n"+connectOn)))
	if !strings.Contains(logged.String(), "Upstreams failed validation") {
		t.Errorf("the invalid upstreams were not reported: %s", logged.String())
	}
	if tcpproxy.WillProxyTCP(port) || !tcpproxy.WillProxyConnect(port) {
		t.Errorf("tcp = %v, connect = %v", tcpproxy.WillProxyTCP(port), tcpproxy.WillProxyConnect(port))
	}
}

func TestLoadTCPProxyWithoutTheHTTPPartIsNoConnectProxy(t *testing.T) {
	resetState(t)
	port := freePort(t)
	removeListenersAfter(t, port)
	loadConfig(parse(t, "tcp.yaml", connectProxyYAML(port, "    upstreams:\n      u:\n        endpoints:\n          e:\n            address: 127.0.0.1:1\n")))
	if tcpproxy.WillProxyConnect(port) || !tcpproxy.WillProxyTCP(port) {
		t.Errorf("connect = %v, tcp = %v", tcpproxy.WillProxyConnect(port), tcpproxy.WillProxyTCP(port))
	}
	if l := listeners.GetListenerForPort(port); l != nil {
		t.Errorf("a proxy that is not a CONNECT one opened a listener: %+v", l)
	}
}

func TestLoadTCPProxyKeepsTheDelayOfAPortThatIsNotAConnectProxy(t *testing.T) {
	resetState(t)
	port := freePort(t)
	removeListenersAfter(t, port)
	loadConfig(parse(t, "delay.yaml", connectProxyYAML(port, "    http:\n      connect: false\n      connectDelay:\n        min: 1s\n        max: 1s\n")))
	if on, delay := connectOf(t, port); on || delay["max"] != "1s" {
		t.Errorf("connect = %v, delay = %v, want the delay for when the mode is turned on", on, delay)
	}
	if listeners.GetListenerForPort(port) != nil {
		t.Error("the port got a listener without being a CONNECT proxy port")
	}
}

func TestLoadTCPProxyIgnoresADelayThatHasNoMax(t *testing.T) {
	resetState(t)
	port := freePort(t)
	removeListenersAfter(t, port)
	logged := captureLog(t)
	loadConfig(parse(t, "nomax.yaml", connectProxyYAML(port, "    http:\n      connect: true\n      connectDelay:\n        min: 1s\n")))
	if on, delay := connectOf(t, port); !on || delay != nil {
		t.Errorf("connect = %v, delay = %v", on, delay)
	}
	if !strings.Contains(logged.String(), "delay has no max") {
		t.Errorf("the delay that is not one was not reported: %s", logged.String())
	}
}

func TestLoadTCPProxyRefusesPortsThatCantBeConnectProxies(t *testing.T) {
	resetState(t)
	for name, port := range map[string]int{"HTTP": global.Self.ServerPort, "gRPC": global.Self.GRPCPort, "JSONRPC": global.Self.JSONRPCPort, "none": 0, "negative": -1, "too large": 70000} {
		logged := captureLog(t)
		loadConfig(parse(t, "refused.yaml", connectProxyYAML(port, connectOn)))
		if tcpproxy.WillProxyConnect(port) {
			t.Errorf("%s port %d: made a CONNECT proxy port", name, port)
		}
		if !strings.Contains(logged.String(), "ERROR") {
			t.Errorf("%s port %d: refusing it was not reported: %s", name, port, logged.String())
		}
	}
}

func TestLoadTCPProxyReloadAppliesTheChangedConnectConfig(t *testing.T) {
	resetState(t)
	port := freePort(t)
	removeListenersAfter(t, port)
	loadConfig(parse(t, "reload.yaml", connectProxyYAML(port, connectOn)))
	if on, delay := connectOf(t, port); !on || delay == nil {
		t.Fatalf("connect = %v, delay = %v", on, delay)
	}
	// the delay is taken out of the file
	loadConfig(parse(t, "reload.yaml", connectProxyYAML(port, "    http:\n      connect: true\n")))
	if on, delay := connectOf(t, port); !on || delay != nil {
		t.Errorf("after the delay was taken out: connect = %v, delay = %v", on, delay)
	}
	// and then the mode is
	loadConfig(parse(t, "reload.yaml", connectProxyYAML(port, "")))
	if on, delay := connectOf(t, port); on || delay != nil {
		t.Errorf("after the http part was taken out: connect = %v, delay = %v", on, delay)
	}
}

func TestRemoveConfigsTakesTheConnectProxyAway(t *testing.T) {
	resetState(t)
	port := freePort(t)
	removeListenersAfter(t, port)
	loadConfig(parse(t, "gone.yaml", connectProxyYAML(port, connectOn)))
	if !tcpproxy.WillProxyConnect(port) {
		t.Fatal("the file didn't load the CONNECT proxy")
	}
	// the tunnels that are open hold on to the proxy of the port, which is what they delay by
	held := tcpproxy.GetPortProxy(port)
	removeConfigs("/configs/gone.yaml")
	if b, _ := json.Marshal(held); strings.Contains(string(b), "connectDelay") || strings.Contains(string(b), `"connect":true`) {
		t.Errorf("the proxy that was replaced still has the delay or the mode: %s", b)
	}
	if on, delay := connectOf(t, port); on || delay != nil || tcpProxyConfigs["gone.yaml"] != nil {
		t.Errorf("connect = %v, delay = %v, configs = %v", on, delay, tcpProxyConfigs)
	}

	loadConfig(parse(t, "all.yaml", connectProxyYAML(port, connectOn)))
	if !tcpproxy.WillProxyConnect(port) {
		t.Fatal("the file didn't load the CONNECT proxy")
	}
	removeAllConfigs()
	if on, delay := connectOf(t, port); on || delay != nil {
		t.Errorf("connect = %v, delay = %v", on, delay)
	}
}
