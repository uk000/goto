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

package httpproxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func decode(t *testing.T, body string, into any) {
	t.Helper()
	if err := json.Unmarshal([]byte(body), into); err != nil {
		t.Fatalf("not the expected JSON: %v: %q", err, body)
	}
}

func TestAPIEnableAndDisableProxy(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTarget("t", "/", up.URL)
	for _, method := range []string{"POST", "PUT"} {
		rr := e.admin(method, "/proxy/http/disable", "")
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "disabled") || e.proxy.isEnabled() {
			t.Fatalf("%s disable answered %d %q, enabled=%v", method, rr.Code, rr.Body.String(), e.proxy.isEnabled())
		}
		expectLocal(t, e.get("/x"))
		rr = e.admin(method, "/proxy/http/enable", "")
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "enabled") || !e.proxy.isEnabled() {
			t.Fatalf("%s enable answered %d %q, enabled=%v", method, rr.Code, rr.Body.String(), e.proxy.isEnabled())
		}
		expectProxied(t, e.get("/x"), 200)
	}
	if up.hits() != 2 {
		t.Errorf("upstream hits = %d, want 2", up.hits())
	}
}

func TestAPIRejectsUnsupportedMethods(t *testing.T) {
	e := newEnv(t)
	for _, tc := range []struct{ method, uri string }{
		{"GET", "/proxy/http/enable"},
		{"GET", "/proxy/http/targets/add"},
		{"POST", "/proxy/http/targets"},
		{"POST", "/proxy/http/responses"},
		{"GET", "/proxy/trackers/clear"},
	} {
		rr := e.admin(tc.method, tc.uri, "")
		if rr.Code != http.StatusMethodNotAllowed && rr.Code != http.StatusNotFound {
			t.Errorf("%s %s answered %d, want it to be refused", tc.method, tc.uri, rr.Code)
		}
	}
	if !e.proxy.isEnabled() || e.proxy.hasAnyTargets() {
		t.Error("a refused request changed the proxy")
	}
}

func TestAPIProxyResponses(t *testing.T) {
	e := newEnv(t)
	rr := e.admin("GET", "/proxy/http/responses", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("answered %d: %s", rr.Code, rr.Body.String())
	}
	if rr := e.admin("PUT", "/proxy/http/responses/set", `[{"upResponseRange":[400,499],"proxyResponse":404},{"upResponseRange":[500],"proxyResponse":503}]`); rr.Code != http.StatusOK {
		t.Fatalf("setting answered %d: %s", rr.Code, rr.Body.String())
	}
	var got []*ProxyResponse
	decode(t, e.admin("GET", "/proxy/http/responses", "").Body.String(), &got)
	if len(got) != 2 || got[0].ProxyResponse != 404 || got[1].UpResponseRange[0] != 500 {
		t.Errorf("responses = %+v", got)
	}
}

func TestAPIProxyResponsesRemapUpstreamStatuses(t *testing.T) {
	e := newEnv(t)
	up500 := newUpstream(t, 500, "bad")
	up404 := newUpstream(t, 404, "missing")
	up200 := newUpstream(t, 200, "ok")
	e.addTarget("a", "/a", up500.URL)
	e.addTarget("b", "/b", up404.URL)
	e.addTarget("c", "/c", up200.URL)
	e.admin("POST", "/proxy/http/responses/set", `[{"upResponseRange":[400,499],"proxyResponse":418},{"upResponseRange":[500],"proxyResponse":503}]`)
	expectProxied(t, e.get("/a/x"), 503)
	expectProxied(t, e.get("/b/x"), 418)
	expectProxied(t, e.get("/c/x"), 200)
}

func TestAPIAddTarget(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	rr := e.admin("POST", "/proxy/http/targets/add", targetSpec("t", "/a", "", up.URL))
	if rr.Code != http.StatusOK {
		t.Fatalf("adding answered %d: %s", rr.Code, rr.Body.String())
	}
	// The target is reported back as YAML.
	if body := rr.Body.String(); !strings.Contains(body, "name: t\n") || !strings.Contains(body, "url: "+up.URL) {
		t.Errorf("reported target = %s", body)
	}
	if e.proxy.getTarget("t") == nil {
		t.Fatal("target was not added to the proxy")
	}
	expectProxied(t, e.get("/a/x"), 200)
	// A target is added as given: it is not used until it is enabled.
	rr = e.admin("PUT", "/proxy/http/targets/add", strings.Replace(targetSpec("u", "/u", "", up.URL), `"enabled":true`, `"enabled":false`, 1))
	if rr.Code != http.StatusOK {
		t.Fatalf("adding answered %d: %s", rr.Code, rr.Body.String())
	}
	expectLocal(t, e.get("/u/x"))
}

func TestAPIAddTargetAcceptsYAML(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	rr := e.admin("POST", "/proxy/http/targets/add", fmt.Sprintf("name: y\nenabled: true\nendpoints:\n  e:\n    url: %s\ntriggers:\n  t:\n    matchAny:\n      - uriPrefix: /y\n    endpoints: [e]\n", up.URL))
	if rr.Code != http.StatusOK {
		t.Fatalf("adding answered %d: %s", rr.Code, rr.Body.String())
	}
	expectProxied(t, e.get("/y/x"), 200)
}

func TestAPIAddTargetForAnotherPort(t *testing.T) {
	e := newEnv(t)
	other := newEnv(t)
	up := newUpstream(t, 200, "ok")
	spec := strings.Replace(targetSpec("t", "/a", "", up.URL), `{"name"`, fmt.Sprintf(`{"port":%d,"name"`, other.port), 1)
	if rr := e.admin("POST", "/proxy/http/targets/add", spec); rr.Code != http.StatusOK {
		t.Fatalf("adding answered %d: %s", rr.Code, rr.Body.String())
	}
	// The port in the target is a field of the target that is not read from JSON, so the target goes to the port of the request.
	if e.proxy.getTarget("t") == nil && other.proxy.getTarget("t") == nil {
		t.Error("the target was added to neither port")
	}
}

func TestAPIAddTargetRejectsInvalidTargets(t *testing.T) {
	e := newEnv(t)
	for name, body := range map[string]string{
		"not parseable":       `{{`,
		"no name":             `{"endpoints":{"e":{"url":"u"}},"triggers":{"t":{"matchAny":[{"uri":"/a"}],"endpoints":["e"]}}}`,
		"undefined endpoint":  `{"name":"n","endpoints":{"e":{"url":"http://127.0.0.1:1"}},"triggers":{"t":{"matchAny":[{"uri":"/a"}],"endpoints":["zz"]}}}`,
		"invalid spec":        `{"name":"n","endpoints":{"e":{"url":"http://127.0.0.1:1","requestCount":1,"concurrent":2}},"triggers":{"t":{"matchAny":[{"uri":"/a"}],"endpoints":["e"]}}}`,
		"trigger no endpoint": `{"name":"n","endpoints":{"e":{"url":"u"}},"triggers":{"t":{"matchAny":[{"uri":"/a"}]}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			rr := e.admin("POST", "/proxy/http/targets/add", body)
			if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Failed") {
				t.Errorf("answered %d %q, want 400 with the failure", rr.Code, rr.Body.String())
			}
		})
	}
	if e.proxy.hasAnyTargets() {
		t.Errorf("targets = %v, want none", e.proxy.getTargets())
	}
}

func TestAPITargetLifecycle(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTarget("a", "/a", up.URL)
	e.addTarget("b", "/b", up.URL)
	var listed struct {
		Port int                `json:"port"`
		HTTP map[string]*Target `json:"http"`
	}
	decode(t, e.admin("GET", "/proxy/http/targets", "").Body.String(), &listed)
	if listed.Port != e.port || len(listed.HTTP) != 2 || listed.HTTP["a"] == nil || listed.HTTP["b"] == nil {
		t.Fatalf("listed targets = %+v", listed)
	}
	if rr := e.admin("POST", "/proxy/http/targets/a/disable", ""); !strings.Contains(rr.Body.String(), "disabled") {
		t.Errorf("disabling answered %d %q", rr.Code, rr.Body.String())
	}
	expectLocal(t, e.get("/a/x"))
	expectProxied(t, e.get("/b/x"), 200)
	if rr := e.admin("PUT", "/proxy/http/targets/a/enable", ""); !strings.Contains(rr.Body.String(), "enabled") {
		t.Errorf("enabling answered %d %q", rr.Code, rr.Body.String())
	}
	expectProxied(t, e.get("/a/x"), 200)
	if rr := e.admin("POST", "/proxy/http/targets/nope/enable", ""); !strings.Contains(rr.Body.String(), "doesn't exist") {
		t.Errorf("enabling an unknown target answered %d %q", rr.Code, rr.Body.String())
	}
	if rr := e.admin("POST", "/proxy/http/targets/a/remove", ""); !strings.Contains(rr.Body.String(), "removed") {
		t.Errorf("removing answered %d %q", rr.Code, rr.Body.String())
	}
	if rr := e.admin("POST", "/proxy/http/targets/a/remove", ""); !strings.Contains(rr.Body.String(), "doesn't exist") {
		t.Errorf("removing it again answered %d %q", rr.Code, rr.Body.String())
	}
	expectLocal(t, e.get("/a/x"))
	if rr := e.admin("POST", "/proxy/http/targets/clear", ""); rr.Code != http.StatusOK {
		t.Errorf("clearing answered %d", rr.Code)
	}
	if e.proxy.hasAnyTargets() {
		t.Error("targets are left after clearing them")
	}
	expectLocal(t, e.get("/b/x"))
}

func TestAPIHeaders(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTarget("t", "/", up.URL)
	for _, uri := range []string{
		"/proxy/http/targets/t/headers/add/x-target=1",
		"/proxy/http/targets/t/t1/headers/add/x-trigger=2",
		"/proxy/http/targets/t/headers/remove/x-drop",
	} {
		if rr := e.admin("POST", uri, ""); rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), "Failed") {
			t.Fatalf("POST %s answered %d %q", uri, rr.Code, rr.Body.String())
		}
	}
	expectProxied(t, e.get("/x", "x-drop", "d"), 200)
	got := up.only(t).Headers
	if got.Get("x-target") != "1" || got.Get("x-trigger") != "2" || got.Get("x-drop") != "" {
		t.Errorf("upstream headers = %v", got)
	}
	for _, uri := range []string{
		"/proxy/http/targets/t/headers/clear/x-target",
		"/proxy/http/targets/t/t1/headers/clear",
	} {
		if rr := e.admin("POST", uri, ""); rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), "Failed") {
			t.Fatalf("POST %s answered %d %q", uri, rr.Code, rr.Body.String())
		}
	}
	expectProxied(t, e.get("/y", "x-drop", "d"), 200)
	reqs := up.requests()
	if len(reqs) != 2 {
		t.Fatalf("upstream requests = %d, want 2", len(reqs))
	}
	if h := reqs[1].Headers; h.Get("x-target") != "" || h.Get("x-trigger") != "" {
		t.Errorf("upstream headers after clearing = %v", h)
	}
}

func TestAPIHeaderChangesOfUnknownTargetReportFailure(t *testing.T) {
	e := newEnv(t)
	e.addTarget("t", "/", "http://127.0.0.1:1")
	for _, uri := range []string{
		"/proxy/http/targets/nope/headers/add/h=v",
		"/proxy/http/targets/t/nope/headers/add/h=v",
		"/proxy/http/targets/nope/headers/remove/h",
		"/proxy/http/targets/nope/headers/clear/h",
		"/proxy/http/targets/t/nope/headers/clear",
	} {
		if rr := e.admin("POST", uri, ""); !strings.Contains(rr.Body.String(), "Failed to configure headers") {
			t.Errorf("POST %s answered %d %q, want a failure", uri, rr.Code, rr.Body.String())
		}
	}
}

func TestAPIGetProxyAndAllProxies(t *testing.T) {
	e := newEnv(t)
	other := newEnv(t)
	e.addTarget("t", "/", "http://127.0.0.1:1")
	var one struct {
		Port int    `json:"port"`
		HTTP *Proxy `json:"http"`
	}
	decode(t, e.admin("GET", "/proxy/http", "").Body.String(), &one)
	if one.Port != e.port || one.HTTP == nil || one.HTTP.Targets["t"] == nil || !one.HTTP.Enabled {
		t.Errorf("reported proxy = %+v", one)
	}
	all := map[string]json.RawMessage{}
	decode(t, e.admin("GET", "/proxy/http/all", "").Body.String(), &all)
	if all[strconv.Itoa(e.port)] == nil || all[strconv.Itoa(other.port)] == nil {
		t.Errorf("reported proxies of ports %v, want those of %d and %d", keys(all), e.port, other.port)
	}
	allTargets := map[string]map[string]json.RawMessage{}
	decode(t, e.admin("GET", "/proxy/http/targets/all", "").Body.String(), &allTargets)
	if allTargets[strconv.Itoa(e.port)]["t"] == nil {
		t.Errorf("reported targets of all ports = %v", allTargets)
	}
}

func keys[V any](m map[string]V) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestAPITrackers(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTarget("t", "/", up.URL)
	for i := 0; i < 3; i++ {
		expectProxied(t, e.get("/x/y?q=1", "x-h", "v"), 200)
	}
	var tracker struct {
		Port    int  `json:"port"`
		Enabled bool `json:"enabled"`
		HTTP    struct {
			DownstreamRequestCount int            `json:"downstreamRequestCount"`
			UpstreamRequestCount   int            `json:"upstreamRequestCount"`
			ByStatus               map[string]int `json:"upstreamRequestCountByStatus"`
			Targets                map[string]*struct {
				DownstreamRequestCount          int            `json:"downstreamRequestCount"`
				UpstreamRequestCount            int            `json:"upstreamRequestCount"`
				UpstreamRequestCountsByEndpoint map[string]int `json:"upstreamRequestCountsByEndpoint"`
			} `json:"targetTrackers"`
		} `json:"http"`
	}
	decode(t, e.admin("GET", "/proxy/http/trackers", "").Body.String(), &tracker)
	if tracker.Port != e.port || !tracker.Enabled || tracker.HTTP.DownstreamRequestCount != 3 || tracker.HTTP.UpstreamRequestCount != 3 || tracker.HTTP.ByStatus["200"] != 3 {
		t.Errorf("tracker = %+v", tracker)
	}
	if tt := tracker.HTTP.Targets["t"]; tt == nil || tt.DownstreamRequestCount != 3 || tt.UpstreamRequestCount != 3 || tt.UpstreamRequestCountsByEndpoint["ep1"] != 3 {
		t.Errorf("target tracker = %+v", tracker.HTTP.Targets["t"])
	}
	var targetTracker struct {
		Port  int                        `json:"port"`
		Total map[string]json.RawMessage `json:"total"`
		T     json.RawMessage            `json:"t"`
	}
	rr := e.admin("GET", "/proxy/http/targets/t/tracker", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("target tracker answered %d: %s", rr.Code, rr.Body.String())
	}
	decode(t, rr.Body.String(), &targetTracker)
	if targetTracker.Port != e.port || targetTracker.Total["downstreamRequestCount"] == nil || len(targetTracker.T) == 0 {
		t.Errorf("target tracker = %s", rr.Body.String())
	}
	all := map[string]json.RawMessage{}
	decode(t, e.admin("GET", "/proxy/http/trackers/all", "").Body.String(), &all)
	if all[strconv.Itoa(e.port)] == nil {
		t.Errorf("trackers of all ports = %v", keys(all))
	}
	if rr := e.admin("POST", "/proxy/trackers/clear", ""); rr.Code != http.StatusOK {
		t.Fatalf("clearing answered %d: %s", rr.Code, rr.Body.String())
	}
	if tr := e.proxy.HTTPTracker; tr.DownstreamRequestCount != 0 || len(tr.TargetTrackers) != 0 {
		t.Errorf("tracker after clearing = %+v", tr)
	}
	expectProxied(t, e.get("/x"), 200)
	if rr := e.admin("POST", "/proxy/trackers/all/clear", ""); rr.Code != http.StatusOK {
		t.Fatalf("clearing all answered %d: %s", rr.Code, rr.Body.String())
	}
	if tr := e.proxy.getTracker(); tr.DownstreamRequestCount != 0 {
		t.Errorf("tracker after clearing all = %+v", tr)
	}
}

func TestAPIConcurrentAdminAndTraffic(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTarget("base", "/base", up.URL)
	const rounds = 25
	var wg sync.WaitGroup
	for w := 0; w < 3; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			name := fmt.Sprintf("dyn%d", w)
			for i := 0; i < rounds; i++ {
				e.admin("POST", "/proxy/http/targets/add", targetSpec(name, "/"+name, "", up.URL))
				e.admin("GET", "/proxy/http/targets", "")
				e.admin("GET", "/proxy/http", "")
				e.admin("GET", "/proxy/http/trackers", "")
				e.admin("POST", "/proxy/http/targets/"+name+"/headers/add/x-h=1", "")
				e.admin("POST", "/proxy/http/targets/"+name+"/disable", "")
				e.admin("POST", "/proxy/http/targets/"+name+"/remove", "")
			}
		}(w)
	}
	hits := 0
	for i := 0; i < rounds; i++ {
		if s := e.get(fmt.Sprintf("/base/%d", i)); s.proxied {
			hits++
		}
	}
	wg.Wait()
	if hits != rounds || up.hits() != rounds {
		t.Errorf("proxied %d requests, upstream hits = %d, want %d of each", hits, up.hits(), rounds)
	}
}
