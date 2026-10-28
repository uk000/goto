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
	"encoding/json"
	"net/http"
	"sort"
	"testing"
	"time"

	"goto/pkg/types"
)

const proxyConfig = `
proxy:
- http:
    port: 7000
    proxyResponses:
    - upResponseRange: [500, 599]
      proxyResponse: 502
    - upResponseRange: [400, 499]
      proxyResponse: 400
    targets:
      first:
        enabled: true
      second:
        enabled: false
- tcp:
    port: 9000
    upstreams:
      u1:
        name: ignored
`

func TestProcessProxySendsTheResponsesAndTargetsOfAnHTTPProxy(t *testing.T) {
	r := newRemote(t)
	useRemote(t, r.URL)
	config := ParseConfig("proxy.yaml", []byte(proxyConfig))
	if config == nil || len(config.Proxies) != 2 {
		t.Fatalf("config = %+v", config)
	}
	within(t, "processProxy", func() { processProxy(config) })
	got := r.got()
	if len(got) != 4 {
		t.Fatalf("requests = %+v, want the responses and the 2 targets of the HTTP proxy, and the upstream of the TCP one", got)
	}
	if got[3].Method != "POST" || got[3].Path != "/port=9000/proxy/tcp/upstreams/add" {
		t.Errorf("last request = %+v, want the upstream of the TCP proxy", got[3])
	}
	if got[0].Method != "POST" || got[0].Path != "/proxy/http/responses/set" || got[0].ContentType != "application/json" {
		t.Errorf("first request = %+v", got[0])
	}
	var responses []map[string]any
	if err := json.Unmarshal([]byte(got[0].Body), &responses); err != nil || len(responses) != 2 || responses[0]["proxyResponse"] != float64(502) {
		t.Errorf("responses = %s, %v", got[0].Body, err)
	}
	var names []string
	for _, req := range got[1:3] {
		if req.Method != "POST" || req.Path != "/proxy/http/targets/add" {
			t.Errorf("request = %+v, want a target to be added", req)
		}
		var target map[string]any
		if err := json.Unmarshal([]byte(req.Body), &target); err != nil {
			t.Fatalf("target = %s: %v", req.Body, err)
		}
		name, _ := target["name"].(string)
		names = append(names, name)
		if want := name == "first"; target["enabled"] != want {
			t.Errorf("target %s = %v", name, target)
		}
	}
	sort.Strings(names)
	if len(names) != 2 || names[0] != "first" || names[1] != "second" {
		t.Errorf("targets = %v, want each to be sent under the name that it is given in the config", names)
	}
}

func TestProcessProxySkipsATCPProxyWithoutAPort(t *testing.T) {
	r := newRemote(t)
	useRemote(t, r.URL)
	config := ParseConfig("proxy.yaml", []byte(`
proxy:
- tcp:
    upstreams:
      u1:
        endpoints:
          e:
            address: 127.0.0.1:1
    http:
      connect: true
- tcp:
    port: 9000
    http:
      connect: true
`))
	if config == nil || len(config.Proxies) != 2 {
		t.Fatalf("config = %+v", config)
	}
	within(t, "processProxy", func() { processProxy(config) })
	got := r.paths()
	if len(got) != 1 || got[0] != "POST /port=9000/proxy/tcp/http/connect/enable" {
		t.Errorf("requests = %v, want nothing for the proxy without a port, and only the config of the one with a port", got)
	}
}

func TestProcessProxyWithNothingSendsNothing(t *testing.T) {
	r := newRemote(t)
	useRemote(t, r.URL)
	within(t, "processProxy", func() {
		processProxy(&GotoConfig{})
		processProxy(&GotoConfig{Proxies: []*Proxy{}})
		processProxy(&GotoConfig{Proxies: []*Proxy{{}}})
	})
	if got := r.got(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

func TestProcessProxyWhenTheRemoteFailsStillSendsTheTargets(t *testing.T) {
	r := newRemote(t)
	r.setStatus(http.StatusInternalServerError)
	useRemote(t, r.URL)
	config := ParseConfig("proxy.yaml", []byte(proxyConfig))
	within(t, "processProxy", func() { processProxy(config) })
	if got := r.got(); len(got) != 4 {
		t.Errorf("requests = %d, want all 4 to be tried", len(got))
	}
}

func TestProcessProxyWhenTheRemoteIsGone(t *testing.T) {
	r := newRemote(t)
	url := r.URL
	r.Close()
	useRemote(t, url)
	config := ParseConfig("proxy.yaml", []byte(proxyConfig))
	within(t, "processProxy", func() { processProxy(config) })
}

func TestSendTCPProxySendsTheUpstreams(t *testing.T) {
	r := newRemote(t)
	useRemote(t, r.URL)
	config := ParseConfig("proxy.yaml", []byte(proxyConfig))
	within(t, "sendTCPPProxy", func() { sendTCPPProxy(config.Proxies[1].TCP) })
	got := r.got()
	if len(got) != 1 || got[0].Path != "/port=9000/proxy/tcp/upstreams/add" || got[0].Method != "POST" {
		t.Fatalf("requests = %+v", got)
	}
	var upstream map[string]any
	if err := json.Unmarshal([]byte(got[0].Body), &upstream); err != nil || upstream["name"] != "u1" {
		t.Errorf("upstream = %s, %v, want it to have the name that it is given in the config", got[0].Body, err)
	}
}

const connectProxyConfig = `
proxy:
- tcp:
    port: 9000
    enabled: true
    http:
      connect: true
      connectDelay:
        min: 1s
        max: 2s
        count: 5
`

func TestProcessProxySendsTheDelayAndThenTheModeOfAConnectProxy(t *testing.T) {
	r := newRemote(t)
	useRemote(t, r.URL)
	config := ParseConfig("proxy.yaml", []byte(connectProxyConfig))
	if config == nil || len(config.Proxies) != 1 || config.Proxies[0].TCP == nil {
		t.Fatalf("config = %+v", config)
	}
	within(t, "processProxy", func() { processProxy(config) })
	want := []string{
		"POST /port=9000/proxy/tcp/http/connect/delay/1s-2s:5",
		"POST /port=9000/proxy/tcp/http/connect/enable",
	}
	got := r.paths()
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("requests = %v, want %v: the delay first, for it to apply to the first connection, then the mode, of the port of the proxy", got, want)
	}
}

func TestSendTCPProxyOfAConnectProxyWithoutUpstreamsSendsOnlyTheConnectConfig(t *testing.T) {
	for name, tc := range map[string]struct {
		yaml string
		want []string
	}{
		"mode only": {"port: 9000\nhttp:\n  connect: true\n", []string{"POST /port=9000/proxy/tcp/http/connect/enable"}},
		"delay only, which is for when the mode is on": {"port: 9000\nhttp:\n  connectDelay:\n    max: 500ms\n", []string{"POST /port=9000/proxy/tcp/http/connect/delay/0s-500ms"}},
		"mode turned off, with a delay":                {"port: 9000\nhttp:\n  connect: false\n  connectDelay:\n    min: 100ms\n    max: 300ms\n", []string{"POST /port=9000/proxy/tcp/http/connect/delay/100ms-300ms"}},
		"no http part":                                 {"port: 9000\nenabled: true\n", nil},
		"empty http part":                              {"port: 9000\nhttp: {}\n", nil},
		"delay without a max is no delay":              {"port: 9000\nhttp:\n  connect: true\n  connectDelay:\n    min: 1s\n", []string{"POST /port=9000/proxy/tcp/http/connect/enable"}},
		"no port, with a connect config":               {"http:\n  connect: true\n", nil},
		"no port, with a connect delay":                {"http:\n  connectDelay:\n    max: 1s\n", nil},
		"no port, with upstreams":                      {"upstreams:\n  u1:\n    endpoints:\n      e:\n        address: 127.0.0.1:1\n", nil},
		"zero port, with upstreams":                    {"port: 0\nupstreams:\n  u1:\n    endpoints:\n      e:\n        address: 127.0.0.1:1\n", nil},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRemote(t)
			useRemote(t, r.URL)
			config := ParseConfig("proxy.yaml", []byte("proxy:\n- tcp:\n"+indent(tc.yaml)))
			if config == nil || len(config.Proxies) != 1 || config.Proxies[0].TCP == nil {
				t.Fatalf("config = %+v", config)
			}
			within(t, "sendTCPPProxy", func() { sendTCPPProxy(config.Proxies[0].TCP) })
			got := r.paths()
			if len(got) != len(tc.want) {
				t.Fatalf("requests = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("request %d = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// indent puts the lines of a YAML text under a key of a list item.
func indent(y string) string {
	out := ""
	for _, line := range splitLines(y) {
		out += "    " + line + "\n"
	}
	return out
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func TestSendTCPProxySendsTheUpstreamsToThePortOfTheProxy(t *testing.T) {
	r := newRemote(t)
	useRemote(t, r.URL)
	config := ParseConfig("proxy.yaml", []byte(`
proxy:
- tcp:
    port: 9000
    upstreams:
      u1:
        endpoints:
          e:
            address: 127.0.0.1:1
    http:
      connect: true
`))
	within(t, "sendTCPPProxy", func() { sendTCPPProxy(config.Proxies[0].TCP) })
	got := r.paths()
	if len(got) != 2 || got[0] != "POST /port=9000/proxy/tcp/upstreams/add" || got[1] != "POST /port=9000/proxy/tcp/http/connect/enable" {
		t.Errorf("requests = %v, want the upstream and then the mode, for the port of the proxy", got)
	}
}

func TestSendTCPProxyKeepsSendingTheConnectConfigWhenAnUpstreamFails(t *testing.T) {
	r := newRemote(t)
	r.setStatus(http.StatusBadRequest)
	useRemote(t, r.URL)
	config := ParseConfig("proxy.yaml", []byte(connectProxyConfig+`
    upstreams:
      u1:
        endpoints:
          e:
            address: 127.0.0.1:1
`))
	within(t, "sendTCPPProxy", func() { sendTCPPProxy(config.Proxies[0].TCP) })
	if got := r.paths(); len(got) != 3 {
		t.Errorf("requests = %v, want the upstream, the delay and the mode all to be tried", got)
	}
}

func TestSendTCPProxyWhenTheRemoteIsGone(t *testing.T) {
	r := newRemote(t)
	url := r.URL
	r.Close()
	useRemote(t, url)
	config := ParseConfig("proxy.yaml", []byte(connectProxyConfig))
	within(t, "sendTCPPProxy", func() { sendTCPPProxy(config.Proxies[0].TCP) })
}

func TestConnectDelayParamIsWhatTheAPIParses(t *testing.T) {
	for _, tc := range []struct {
		delay *types.Delay
		want  string
	}{
		{types.NewDelay(time.Second, 2*time.Second, 5), "1s-2s:5"},
		{types.NewDelay(time.Second, 2*time.Second, -1), "1s-2s"},
		{types.NewDelay(0, 250*time.Millisecond, 1), "0s-250ms:1"},
		{types.NewDelay(90*time.Second, 90*time.Second, 0), "1m30s-1m30s"},
		{&types.Delay{Max: &types.Duration{Duration: time.Second}}, "0s-1s"},
		{&types.Delay{Min: &types.Duration{Duration: -time.Second}, Max: &types.Duration{Duration: time.Second}}, "0s-1s"},
		{&types.Delay{Min: &types.Duration{Duration: time.Second}}, ""},
		{&types.Delay{}, ""},
	} {
		got := connectDelayParam(tc.delay)
		if got != tc.want {
			t.Errorf("delay %+v = %q, want %q", tc.delay.String(), got, tc.want)
			continue
		}
		// what is sent is read by the delay parser of the remote as the delay that was sent
		if got != "" {
			parsed := types.ParseDelay(got)
			if parsed == nil || parsed.Max.Duration != tc.delay.Max.Duration {
				t.Errorf("%q parsed to %v", got, parsed)
			}
		}
	}
}
