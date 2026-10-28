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

package tcpproxy

import (
	"encoding/json"
	"testing"
	"time"

	"goto/pkg/types"

	"gopkg.in/yaml.v3"
)

func TestConnectConfigIsReadFromTheHTTPPartOfTheYAML(t *testing.T) {
	var p TCPProxy
	err := yaml.Unmarshal([]byte(`
port: 9000
enabled: true
http:
  connect: true
  connectDelay:
    min: 1s
    max: 2s
    count: 5
`), &p)
	if err != nil {
		t.Fatal(err)
	}
	d := p.HTTP.ConnectDelay
	if p.Port != 9000 || !p.Enabled || !p.HTTP.Connect || d == nil || d.Min.Duration != time.Second || d.Max.Duration != 2*time.Second || d.Count != 5 {
		t.Errorf("proxy = %+v, delay = %+v", &p, d)
	}
}

func TestConnectConfigWithoutHTTPOrDelayIsNoConnectProxy(t *testing.T) {
	var none, noDelay TCPProxy
	if err := yaml.Unmarshal([]byte("port: 9000\nenabled: true\n"), &none); err != nil {
		t.Fatal(err)
	}
	if none.HTTP.Connect || none.HTTP.ConnectDelay != nil {
		t.Errorf("a proxy without an http part has %+v", none.HTTP)
	}
	if err := yaml.Unmarshal([]byte("port: 9000\nhttp:\n  connect: true\n"), &noDelay); err != nil {
		t.Fatal(err)
	}
	if !noDelay.HTTP.Connect || noDelay.HTTP.ConnectDelay != nil {
		t.Errorf("a proxy with no delay has %+v", noDelay.HTTP)
	}
	// the count of a delay is unlimited unless it is given
	var unlimited TCPProxy
	if err := yaml.Unmarshal([]byte("port: 9000\nhttp:\n  connectDelay:\n    min: 1s\n    max: 2s\n"), &unlimited); err != nil {
		t.Fatal(err)
	}
	if d := unlimited.HTTP.ConnectDelay; d == nil || d.Count != -1 {
		t.Errorf("delay = %+v", d)
	}
}

func TestConnectConfigIsReportedUnderHTTPAsJSONThatReadsBack(t *testing.T) {
	var p TCPProxy
	if err := yaml.Unmarshal([]byte("port: 9000\nhttp:\n  connect: true\n  connectDelay:\n    min: 1s\n    max: 2s\n    count: 5\n"), &p); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(&p)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		HTTP TCPProxyHTTP `json:"http"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	d := got.HTTP.ConnectDelay
	if !got.HTTP.Connect || d == nil || d.Min.Duration != time.Second || d.Max.Duration != 2*time.Second || d.Count != 5 {
		t.Errorf("proxy = %s", b)
	}
	// a proxy that has no delay doesn't report one
	b, _ = json.Marshal(newTCPProxy(1))
	var plain map[string]map[string]any
	_ = json.Unmarshal(b, &plain)
	if h := plain["http"]; h == nil || h["connect"] != false || h["connectDelay"] != nil {
		t.Errorf("proxy = %s", b)
	}
}

// The tunnels that are open hold on to the proxy that they were made through, which is replaced when the proxy of the port
// is cleared: what they delay by must go with it.
func TestClearingAProxyTakesTheConnectConfigAwayFromTheOneThatTunnelsHold(t *testing.T) {
	for name, clear := range map[string]func(port int){
		"port": ClearPortProxy,
		"all":  func(int) { ClearAllProxies() },
	} {
		t.Run(name, func(t *testing.T) {
			port := apiPort(t)
			t.Cleanup(func() { ClearPortProxy(port) })
			EnableConnect(port, true)
			SetConnectDelay(port, types.NewDelay(time.Hour, time.Hour, -1))
			held := GetPortProxy(port)
			if held.nextConnectDelay() != time.Hour {
				t.Fatal("the delay is not set")
			}
			clear(port)
			if d := held.nextConnectDelay(); d != 0 {
				t.Errorf("the proxy that the tunnels hold delays by %v after it was cleared", d)
			}
			if held.isConnectEnabled() || WillProxyConnect(port) {
				t.Error("the mode is on after the proxy was cleared")
			}
			if GetPortProxy(port) == held {
				t.Error("the proxy was not replaced")
			}
		})
	}
}
