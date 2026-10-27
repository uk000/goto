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
	if len(got) != 3 {
		t.Fatalf("requests = %+v, want the responses and the 2 targets of the HTTP proxy, and nothing for TCP", got)
	}
	if got[0].Method != "POST" || got[0].Path != "/proxy/http/responses/set" || got[0].ContentType != "application/json" {
		t.Errorf("first request = %+v", got[0])
	}
	var responses []map[string]any
	if err := json.Unmarshal([]byte(got[0].Body), &responses); err != nil || len(responses) != 2 || responses[0]["proxyResponse"] != float64(502) {
		t.Errorf("responses = %s, %v", got[0].Body, err)
	}
	var names []string
	for _, req := range got[1:] {
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
	if got := r.got(); len(got) != 3 {
		t.Errorf("requests = %d, want all 3 to be tried", len(got))
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
	if len(got) != 1 || got[0].Path != "/proxy/tcp/upstreams/add" || got[0].Method != "POST" {
		t.Fatalf("requests = %+v", got)
	}
	var upstream map[string]any
	if err := json.Unmarshal([]byte(got[0].Body), &upstream); err != nil || upstream["name"] != "u1" {
		t.Errorf("upstream = %s, %v, want it to have the name that it is given in the config", got[0].Body, err)
	}
}
