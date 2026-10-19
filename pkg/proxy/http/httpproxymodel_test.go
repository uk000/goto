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
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/mux"
)

const minimalTarget = `{"name":"t","endpoints":{"ep1":{"url":"http://127.0.0.1:1"}},"triggers":{"t1":{"matchAny":[{"uriPrefix":"/a"}],"endpoints":["ep1"]}}}`

func TestParseTargetAcceptsJSONAndYAML(t *testing.T) {
	target, err := parseTarget(strings.NewReader(minimalTarget))
	if err != nil {
		t.Fatalf("parseTarget(json): %v", err)
	}
	if target.Name != "t" || target.Endpoints["ep1"].URL != "http://127.0.0.1:1" || target.Triggers["t1"].MatchAny[0].URIPrefix != "/a" {
		t.Errorf("parsed target = %+v", target)
	}
	yamlTarget := `
name: y
enabled: true
endpoints:
  ep1:
    url: http://127.0.0.1:2
    method: POST
    requestCount: 3
    concurrent: 2
triggers:
  t1:
    matchAny:
      - uri: /exact
        headers:
          x-h: v
    endpoints: [ep1]
    trafficConfig:
      retries: 2
      retryOn: [502, 503]
`
	target, err = parseTarget(strings.NewReader(yamlTarget))
	if err != nil {
		t.Fatalf("parseTarget(yaml): %v", err)
	}
	ep := target.Endpoints["ep1"]
	m := target.Triggers["t1"].MatchAny[0]
	tc := target.Triggers["t1"].TrafficConfig
	if !target.Enabled || ep.Method != "POST" || ep.RequestCount != 3 || ep.Concurrent != 2 || m.URI != "/exact" || m.Headers["x-h"] != "v" ||
		tc.Retries != 2 || !reflect.DeepEqual(tc.RetryOn, []int{502, 503}) {
		t.Errorf("parsed yaml target = %+v, endpoint %+v, trigger match %+v, traffic config %+v", target, ep, m, tc)
	}
}

func TestParseTargetRejectsInvalidTargets(t *testing.T) {
	tests := []struct{ name, body, errPart string }{
		{"not json or yaml", `{{{`, ""},
		{"empty", ``, ""},
		{"no name", `{"endpoints":{"e":{"url":"u"}},"triggers":{"t":{"matchAny":[{"uri":"/a"}],"endpoints":["e"]}}}`, "name missing"},
		{"no endpoints", `{"name":"n","triggers":{"t":{"matchAny":[{"uri":"/a"}],"endpoints":["e"]}}}`, "endpoints missing"},
		{"endpoint without url", `{"name":"n","endpoints":{"e":{}},"triggers":{"t":{"matchAny":[{"uri":"/a"}],"endpoints":["e"]}}}`, "missing url"},
		{"no triggers", `{"name":"n","endpoints":{"e":{"url":"u"}}}`, "trigger is required"},
		{"trigger without match", `{"name":"n","endpoints":{"e":{"url":"u"}},"triggers":{"t":{"endpoints":["e"]}}}`, "must specify matchAny"},
		{"trigger without endpoints", `{"name":"n","endpoints":{"e":{"url":"u"}},"triggers":{"t":{"matchAny":[{"uri":"/a"}]}}}`, "must specify one endpoint"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			target, err := parseTarget(strings.NewReader(tc.body))
			if err == nil || target != nil {
				t.Fatalf("parseTarget(%s) = %+v, %v, want an error", tc.body, target, err)
			}
			if !strings.Contains(err.Error(), tc.errPart) {
				t.Errorf("error = %q, want it to mention %q", err.Error(), tc.errPart)
			}
		})
	}
}

func TestAddTargetDefaultsAndTrafficConfigPropagation(t *testing.T) {
	e := newEnv(t)
	target := e.addTargetSpec(`{"name":"t","enabled":true,"endpoints":{"ep1":{"url":"http://127.0.0.1:1"},"ep2":{"url":"http://127.0.0.1:2","requestCount":4,"concurrent":3}},` +
		`"trafficConfig":{"stream":true,"payload":true,"jsonPayload":true,"yamlPayload":true},` +
		`"triggers":{"t1":{"matchAny":[{"uriPrefix":"/a"}],"endpoints":["ep1","ep2"]}}}`)
	ep1, ep2 := target.Endpoints["ep1"], target.Endpoints["ep2"]
	if ep1.RequestCount != 1 || ep1.Concurrent != 1 {
		t.Errorf("endpoint defaults = %d x %d, want 1 x 1", ep1.RequestCount, ep1.Concurrent)
	}
	if ep2.RequestCount != 4 || ep2.Concurrent != 3 {
		t.Errorf("endpoint settings = %d x %d, want 4 x 3", ep2.RequestCount, ep2.Concurrent)
	}
	for name, ep := range target.Endpoints {
		if !ep.Stream || !ep.Payload || !ep.JsonPayload || !ep.YamlPayload || ep.name != name || ep.target != target {
			t.Errorf("endpoint %s = %+v, want the traffic config of the target applied", name, ep)
		}
	}
	if !target.streaming {
		t.Error("a target with a streaming endpoint is not marked as streaming")
	}
	if got := e.proxy.getTarget("t"); got != target {
		t.Errorf("proxy target = %p, want %p", got, target)
	}
	if specs := target.Triggers["t1"].epSpecs; len(specs) != 2 || specs["ep1"] == nil || specs["ep2"] == nil {
		t.Errorf("trigger endpoint specs = %+v", specs)
	}
}

func TestAddTargetRejectsTriggerWithUndefinedEndpoint(t *testing.T) {
	e := newEnv(t)
	target, err := parseTarget(strings.NewReader(`{"name":"t","endpoints":{"ep1":{"url":"http://127.0.0.1:1"}},` +
		`"triggers":{"t1":{"matchAny":[{"uriPrefix":"/a"}],"endpoints":["nope"]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	err = e.proxy.AddTarget(target)
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("AddTarget error = %v, want one about the undefined endpoint", err)
	}
	if e.proxy.getTarget("t") != nil || e.proxy.hasAnyTargets() {
		t.Error("a target that failed to be added is in the proxy")
	}
}

func TestAddTargetReplacesTargetOfSameName(t *testing.T) {
	e := newEnv(t)
	up1 := newUpstream(t, 200, "one")
	up2 := newUpstream(t, 200, "two")
	e.addTarget("t", "/a", up1.URL)
	e.addTarget("t", "/a", up2.URL)
	if len(e.proxy.getTargets()) != 1 {
		t.Fatalf("targets = %v, want the one that was added last", e.proxy.getTargets())
	}
	expectProxied(t, e.get("/a/x"), 200)
	if up1.hits() != 0 || up2.hits() != 1 {
		t.Errorf("upstream hits = %d, %d, want 0 and 1", up1.hits(), up2.hits())
	}
}

func TestProxyTargetManagement(t *testing.T) {
	e := newEnv(t)
	p := e.proxy
	if p.hasAnyTargets() {
		t.Fatal("a new proxy has targets")
	}
	if !p.isEnabled() {
		t.Error("a new proxy is not enabled")
	}
	e.addTarget("a", "/a", "http://127.0.0.1:1")
	e.addTarget("b", "/b", "http://127.0.0.1:1")
	if !p.hasAnyTargets() || len(p.getTargets()) != 2 {
		t.Fatalf("targets = %v", p.getTargets())
	}
	// The map that is returned is a copy: changing it doesn't change the proxy.
	delete(p.getTargets(), "a")
	if len(p.getTargets()) != 2 {
		t.Error("the targets of the proxy were changed through the copy that getTargets returns")
	}
	if p.enableTarget("nope", true) {
		t.Error("enabling an unknown target succeeded")
	}
	if !p.enableTarget("a", false) || p.getTarget("a").Enabled {
		t.Error("target a was not disabled")
	}
	if !p.enableTarget("a", true) || !p.getTarget("a").Enabled {
		t.Error("target a was not enabled")
	}
	if p.removeTarget("nope") {
		t.Error("removing an unknown target succeeded")
	}
	if !p.removeTarget("a") || p.getTarget("a") != nil || len(p.getTargets()) != 1 {
		t.Errorf("target a was not removed: %v", p.getTargets())
	}
	p.clearTargets()
	if p.hasAnyTargets() {
		t.Error("targets are left after clearing them")
	}
	p.enable(false)
	if p.isEnabled() {
		t.Error("proxy is still enabled")
	}
	p.enable(true)
	if !p.isEnabled() {
		t.Error("proxy is not enabled")
	}
}

func TestGetPortProxyReturnsTheSameProxyForAPort(t *testing.T) {
	e := newEnv(t)
	if GetPortProxy(e.port) != e.proxy {
		t.Error("GetPortProxy returned another proxy for the port")
	}
	if p := GetPortProxy(e.port + 1000); p == e.proxy || p.Port != e.port+1000 {
		t.Errorf("GetPortProxy for another port = %+v", p)
	}
	proxyLock.Lock()
	delete(portProxy, e.port+1000)
	proxyLock.Unlock()
}

func TestClearPortProxyReplacesTheProxyOfThePort(t *testing.T) {
	e := newEnv(t)
	e.addTarget("a", "/a", "http://127.0.0.1:1")
	ClearPortProxy(e.port)
	fresh := GetPortProxy(e.port)
	if fresh == e.proxy || fresh.hasAnyTargets() || !fresh.isEnabled() {
		t.Errorf("proxy after clearing = %+v", fresh)
	}
}

func TestClearAllProxiesRemovesAllProxies(t *testing.T) {
	e := newEnv(t)
	e.addTarget("a", "/a", "http://127.0.0.1:1")
	other := e.port + 2000
	GetPortProxy(other)
	ClearAllProxies()
	if got := snapshotProxies(); len(got) != 0 {
		t.Errorf("proxies after clearing them all = %v", got)
	}
	if GetPortProxy(e.port).hasAnyTargets() {
		t.Error("the proxy of the port still has its targets")
	}
}

func TestSnapshotProxiesIsACopy(t *testing.T) {
	e := newEnv(t)
	snap := snapshotProxies()
	if snap[e.port] != e.proxy {
		t.Fatalf("snapshot has %v for the port", snap[e.port])
	}
	delete(snap, e.port)
	if snapshotProxies()[e.port] != e.proxy {
		t.Error("deleting from the snapshot deleted the proxy")
	}
}

// ---------------------------------------------------------------------------------------------------------------
// Headers

func prepared(tt *TrafficTransform) *TrafficTransform {
	tt.prepare()
	return tt
}

func headersOf(tt *TrafficTransform) (map[string]string, []string) {
	if tt == nil || tt.Headers == nil {
		return nil, nil
	}
	return tt.Headers.Add, tt.Headers.Remove
}

func TestTargetLevelHeadersApplyToAllTriggers(t *testing.T) {
	e := newEnv(t)
	target := e.addTargetSpec(`{"name":"t","enabled":true,"endpoints":{"ep1":{"url":"http://127.0.0.1:1"}},"triggers":{` +
		`"t1":{"matchAny":[{"uriPrefix":"/a"}],"endpoints":["ep1"]},` +
		`"t2":{"matchAny":[{"uriPrefix":"/b"}],"endpoints":["ep1"],"transform":{"headers":{"add":{"own":"1"}}}}}}`)
	if !e.proxy.addRemoveHeader("t", "", "x-add", "v", false) || !e.proxy.addRemoveHeader("t", "", "x-rm", "", true) {
		t.Fatal("headers were not configured on an existing target")
	}
	add, remove := headersOf(target.Transform)
	if add["x-add"] != "v" || !reflect.DeepEqual(remove, []string{"x-rm"}) {
		t.Errorf("target headers = %v, %v", add, remove)
	}
	// A trigger without a transform shares the one of the target, and a trigger with its own gets the headers added.
	if target.Triggers["t1"].Transform != target.Transform {
		t.Error("trigger t1 does not use the transform of the target")
	}
	add, remove = headersOf(target.Triggers["t2"].Transform)
	if add["x-add"] != "v" || add["own"] != "1" || !reflect.DeepEqual(remove, []string{"x-rm"}) {
		t.Errorf("trigger t2 headers = %v, %v", add, remove)
	}
}

func TestTriggerLevelHeaders(t *testing.T) {
	e := newEnv(t)
	target := e.addTargetSpec(targetSpec("t", "/", "", "http://127.0.0.1:1"))
	if !e.proxy.addRemoveHeader("t", "t1", "x-add", "v", false) || !e.proxy.addRemoveHeader("t", "t1", "x-rm", "", true) {
		t.Fatal("headers were not configured on an existing trigger")
	}
	add, remove := headersOf(target.Triggers["t1"].Transform)
	if add["x-add"] != "v" || !reflect.DeepEqual(remove, []string{"x-rm"}) {
		t.Errorf("trigger headers = %v, %v", add, remove)
	}
	if target.Transform != nil {
		t.Errorf("target transform = %+v, want none for a header set on a trigger", target.Transform)
	}
	if !e.proxy.clearHeaders("t", "t1", "x-add") {
		t.Fatal("clearing a header of the trigger failed")
	}
	add, remove = headersOf(target.Triggers["t1"].Transform)
	if len(add) != 0 || !reflect.DeepEqual(remove, []string{"x-rm"}) {
		t.Errorf("trigger headers after clearing x-add = %v, %v", add, remove)
	}
	if !e.proxy.clearHeaders("t", "t1", "") {
		t.Fatal("clearing all headers of the trigger failed")
	}
	add, remove = headersOf(target.Triggers["t1"].Transform)
	if len(add) != 0 || len(remove) != 0 {
		t.Errorf("trigger headers after clearing them all = %v, %v", add, remove)
	}
}

func TestHeaderChangesOfUnknownTargetOrTriggerFail(t *testing.T) {
	e := newEnv(t)
	e.addTarget("t", "/", "http://127.0.0.1:1")
	if e.proxy.addRemoveHeader("nope", "", "h", "v", false) || e.proxy.addRemoveHeader("t", "nope", "h", "v", false) {
		t.Error("adding a header to an unknown target or trigger succeeded")
	}
	if e.proxy.addRemoveHeader("nope", "", "h", "", true) || e.proxy.addRemoveHeader("t", "nope", "h", "", true) {
		t.Error("removing a header from an unknown target or trigger succeeded")
	}
	if e.proxy.clearHeaders("nope", "", "h") || e.proxy.clearHeaders("t", "nope", "h") {
		t.Error("clearing a header of an unknown target or trigger succeeded")
	}
	// A trigger that has no headers has nothing to clear.
	if e.proxy.clearHeaders("t", "t1", "h") {
		t.Error("clearing a header of a trigger without a transform succeeded")
	}
}

func TestTargetLevelClearRemovesHeadersEverywhere(t *testing.T) {
	e := newEnv(t)
	target := e.addTargetSpec(`{"name":"t","enabled":true,"endpoints":{"ep1":{"url":"http://127.0.0.1:1"}},"triggers":{` +
		`"t1":{"matchAny":[{"uriPrefix":"/a"}],"endpoints":["ep1"]},` +
		`"t2":{"matchAny":[{"uriPrefix":"/b"}],"endpoints":["ep1"],"transform":{"headers":{"add":{"x-add":"1","keep":"1"},"remove":["x-rm"]}}}}}`)
	e.proxy.addRemoveHeader("t", "", "x-add", "v", false)
	e.proxy.addRemoveHeader("t", "", "x-rm", "", true)
	if !e.proxy.clearHeaders("t", "", "x-add") || !e.proxy.clearHeaders("t", "", "x-rm") {
		t.Fatal("clearing the headers failed")
	}
	for name, tr := range target.Triggers {
		add, remove := headersOf(tr.Transform)
		if _, ok := add["x-add"]; ok || len(remove) != 0 {
			t.Errorf("trigger %s headers = %v, %v, want x-add and x-rm cleared", name, add, remove)
		}
	}
	if add, _ := headersOf(target.Triggers["t2"].Transform); add["keep"] != "1" {
		t.Errorf("a header that was not cleared is gone: %v", add)
	}
}

func TestHeaderChangesWhileRequestsAreServed(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTarget("t", "/", up.URL)
	const rounds = 30
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			h := fmt.Sprintf("x-h%d", i%3)
			e.proxy.addRemoveHeader("t", "", h, "v", false)
			e.proxy.addRemoveHeader("t", "t1", h, "", true)
			e.proxy.clearHeaders("t", "", h)
		}
	}()
	for i := 0; i < rounds; i++ {
		expectProxied(t, e.get(fmt.Sprintf("/r/%d", i)), 200)
	}
	wg.Wait()
	if up.hits() != rounds {
		t.Errorf("upstream hits = %d, want %d", up.hits(), rounds)
	}
}

// ---------------------------------------------------------------------------------------------------------------
// Matching

func TestMatchingURIsAndPrefixes(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTargetSpec(fmt.Sprintf(`{"name":"t","enabled":true,"endpoints":{"ep1":{"url":%q}},"triggers":{`+
		`"exact":{"matchAny":[{"uri":"/svc/exact"}],"endpoints":["ep1"]},`+
		`"prefix":{"matchAny":[{"uriPrefix":"/svc/pre"}],"endpoints":["ep1"]}}}`, up.URL))
	tests := []struct {
		uri   string
		match bool
	}{
		{"/svc/exact", true},
		{"/svc/exact?x=1", true},
		{"/svc/exact/more", false},
		{"/svc/pre", true},
		{"/svc/pre/deeper/still", true},
		// A prefix is a prefix of the URI as a string, not of its segments.
		{"/svc/prefix-continues", true},
		{"/svc/pr", false},
		{"/svc/other", false},
		{"/other/exact", false},
	}
	for _, tc := range tests {
		t.Run(tc.uri, func(t *testing.T) {
			s := e.get(tc.uri)
			if s.proxied != tc.match {
				t.Errorf("GET %s proxied = %v, want %v (answered %d %q)", tc.uri, s.proxied, tc.match, s.rr.Code, s.rr.Body.String())
			}
		})
	}
}

func TestMatchingHeadersRequiresAllOfThem(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTargetSpec(fmt.Sprintf(`{"name":"t","enabled":true,"endpoints":{"ep1":{"url":%q}},"triggers":{`+
		`"t1":{"matchAny":[{"uriPrefix":"/h","headers":{"x-a":"1","x-b":""}}],"endpoints":["ep1"]}}}`, up.URL))
	expectLocal(t, e.get("/h/x"))
	expectLocal(t, e.get("/h/x", "x-a", "1"))
	expectLocal(t, e.get("/h/x", "x-a", "2", "x-b", "z"))
	expectProxied(t, e.get("/h/x", "x-a", "1", "x-b", "anything"), 200)
	if up.hits() != 1 {
		t.Errorf("upstream hits = %d, want 1", up.hits())
	}
}

func TestMatchingAnyOfSeveralMatches(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTargetSpec(fmt.Sprintf(`{"name":"t","enabled":true,"endpoints":{"ep1":{"url":%q}},"triggers":{`+
		`"t1":{"matchAny":[{"uriPrefix":"/m1"},{"uriPrefix":"/m2"}],"endpoints":["ep1"]}}}`, up.URL))
	expectProxied(t, e.get("/m1/x"), 200)
	expectProxied(t, e.get("/m2/x"), 200)
	expectLocal(t, e.get("/m3/x"))
	if up.hits() != 2 {
		t.Errorf("upstream hits = %d, want 2", up.hits())
	}
}

func TestMatchingURIVariables(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	e.addTargetSpec(fmt.Sprintf(`{"name":"t","enabled":true,"endpoints":{"ep1":{"url":%q}},"triggers":{`+
		`"t1":{"matchAny":[{"uriPrefix":"/api/{ver}/items","vars":{"ver":{"in":["v1","v2"]}}}],"endpoints":["ep1"]}}}`, up.URL))
	tests := []struct {
		ver   string
		match bool
	}{{"v1", true}, {"v2", true}, {"v3", false}}
	for _, tc := range tests {
		t.Run(tc.ver, func(t *testing.T) {
			req := e.newRequest("GET", "/api/"+tc.ver+"/items", nil)
			req = mux.SetURLVars(req, map[string]string{"ver": tc.ver})
			if s := e.serve(req); s.proxied != tc.match {
				t.Errorf("version %s proxied = %v, want %v", tc.ver, s.proxied, tc.match)
			}
		})
	}
}

func TestMatchingExactBeforePrefix(t *testing.T) {
	e := newEnv(t)
	up1 := newUpstream(t, 200, "exact")
	up2 := newUpstream(t, 200, "prefix")
	e.addTargetSpec(fmt.Sprintf(`{"name":"t","enabled":true,"endpoints":{"exact":{"url":%q},"prefix":{"url":%q}},"triggers":{`+
		`"p":{"matchAny":[{"uriPrefix":"/svc"}],"endpoints":["prefix"]},`+
		`"e":{"matchAny":[{"uri":"/svc/exact"}],"endpoints":["exact"]}}}`, up1.URL, up2.URL))
	expectProxied(t, e.get("/svc/exact"), 200)
	expectProxied(t, e.get("/svc/other"), 200)
	if up1.hits() != 1 || up2.hits() != 1 {
		t.Errorf("hits of the exact and prefix endpoints = %d, %d, want 1 each", up1.hits(), up2.hits())
	}
}

// ---------------------------------------------------------------------------------------------------------------
// Request context and invocation specs

func TestRequestContextBodyReaderGivesEveryCallerTheWholeBody(t *testing.T) {
	rc := &RequestContext{body: strings.NewReader("payload")}
	for i := 0; i < 3; i++ {
		r := rc.bodyReader()
		if r == nil {
			t.Fatalf("reader %d is nil", i)
		}
		if b, _ := io.ReadAll(r); string(b) != "payload" {
			t.Errorf("reader %d read %q, want the whole body", i, b)
		}
	}
	if empty := (&RequestContext{}).bodyReader(); empty != nil {
		t.Errorf("reader of a request without a body = %v, want nil", empty)
	}
	if empty := (&RequestContext{body: strings.NewReader("")}).bodyReader(); empty != nil {
		t.Errorf("reader of an empty body = %v, want nil", empty)
	}
}

func TestPrepareInvocationSpec(t *testing.T) {
	ep := &TargetEndpoint{name: "ep", URL: "http://127.0.0.1:1", Method: "PUT", Authority: "auth.example", RequestCount: 6, Concurrent: 3, JsonPayload: true}
	is, err := ep.prepareInvocationSpec(&TrafficConfig{Retries: 2, RetryOn: []int{502}, Payload: true})
	if err != nil {
		t.Fatal(err)
	}
	if is.Name != "ep" || is.URL != "http://127.0.0.1:1" || is.Method != "PUT" || is.Host != "auth.example" || is.RequestCount != 6 || is.Replicas != 3 ||
		is.Retries != 2 || !reflect.DeepEqual(is.RetriableStatusCodes, []int{502}) || !is.CollectResponse || !is.LongRunning || !is.TrackPayload {
		t.Errorf("spec = %+v", is)
	}
	// The method of the request is used when the endpoint has none, though validation of the spec defaults it to GET.
	ep = &TargetEndpoint{name: "ep", URL: "http://127.0.0.1:1", RequestCount: 1, Concurrent: 1}
	is, err = ep.prepareInvocationSpec(nil)
	if err != nil {
		t.Fatal(err)
	}
	if is.Method != "" || is.CollectResponse {
		t.Errorf("spec method = %q collect = %v, want no method and no collecting of responses", is.Method, is.CollectResponse)
	}
	for _, tc := range []TrafficConfig{{Payload: true}, {JsonPayload: true}, {YamlPayload: true}} {
		if is, err := ep.prepareInvocationSpec(&tc); err != nil || !is.CollectResponse {
			t.Errorf("spec for %+v = %+v, %v, want it to collect responses", tc, is, err)
		}
	}
}

func TestPrepareInvocationSpecRejectsInvalidSpec(t *testing.T) {
	// Validation of the spec doesn't accept fewer requests than concurrent callers.
	ep := &TargetEndpoint{name: "ep", URL: "http://127.0.0.1:1", RequestCount: 1, Concurrent: 2}
	if is, err := ep.prepareInvocationSpec(nil); err == nil || !strings.Contains(err.Error(), "RequestCount") {
		t.Errorf("spec with fewer requests than callers = %+v, %v, want an error about RequestCount", is, err)
	}
}

func TestAddTargetFailsForEndpointWithInvalidSpec(t *testing.T) {
	e := newEnv(t)
	target, err := parseTarget(strings.NewReader(`{"name":"t","endpoints":{"ep1":{"url":"http://127.0.0.1:1","requestCount":1,"concurrent":2}},` +
		`"triggers":{"t1":{"matchAny":[{"uriPrefix":"/a"}],"endpoints":["ep1"]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.proxy.AddTarget(target); err == nil {
		t.Error("a target with an endpoint that has an invalid spec was added")
	}
}

func TestPrepareURL(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	target := e.addTarget("t", "/", up.URL)
	ep := target.Triggers["t1"].epSpecs["ep1"]
	tests := []struct {
		name       string
		tt         *TrafficTransform
		matchedURI string
		path       string
		requestURI string
		vars       map[string]string
		want       string
	}{
		{"path is kept", nil, "/foo", "/foo/bar", "/foo/bar", nil, "/foo/bar"},
		{"uri map by request uri", &TrafficTransform{URIMap: map[string]string{"/foo/bar": "/mapped"}}, "/foo", "/foo/bar", "/foo/bar", nil, "/mapped"},
		{"uri map by matched uri", &TrafficTransform{URIMap: map[string]string{"/foo": "/mapped"}}, "/foo", "/foo", "/foo", nil, "/mapped"},
		{"uri map of an unrelated uri", &TrafficTransform{URIMap: map[string]string{"/other": "/mapped"}}, "/foo", "/foo/bar", "/foo/bar", nil, "/foo/bar"},
		{"uri map with variables", &TrafficTransform{URIMap: map[string]string{"/f/{id}": "/g/{id}/x"}}, "/f/{id}", "/f/7", "/f/7", map[string]string{"id": "7"}, "/g/7/x"},
		{"query add", &TrafficTransform{Queries: &Keys{Add: map[string]string{"k": "v"}}}, "/foo", "/foo/bar", "/foo/bar", nil, "/foo/bar?k=v"},
		{"strip uri", prepared(&TrafficTransform{StripURI: "/strip"}), "/foo", "/foo/strip/bar", "/foo/strip/bar", nil, "/foo/bar"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rc := &RequestContext{path: tc.path, r: e.newRequest("GET", tc.requestURI, nil), vars: tc.vars, headers: map[string]string{}, queries: map[string]string{}}
			got := ep.prepareURL(tc.matchedURI, tc.tt, rc, e.proxy.HTTPTracker)
			if got != up.URL+tc.want {
				t.Errorf("url = %q, want %q", got, up.URL+tc.want)
			}
		})
	}
	tr := e.proxy.HTTPTracker
	if tr.UpstreamRequestCount != len(tests) {
		t.Errorf("upstream request count = %d, want %d", tr.UpstreamRequestCount, len(tests))
	}
}

func TestTargetAndEndpointsAreReportedAsJSON(t *testing.T) {
	e := newEnv(t)
	up := newUpstream(t, 200, "ok")
	target := e.addTarget("t", "/", up.URL)
	expectProxied(t, e.get("/x"), 200)
	b, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Name      string `json:"name"`
		Enabled   bool   `json:"enabled"`
		CallCount int    `json:"callCount"`
		Endpoints map[string]struct {
			URL       string `json:"url"`
			CallCount int    `json:"callCount"`
		} `json:"endpoints"`
		Triggers map[string]struct {
			CallCount int `json:"callCount"`
		} `json:"triggers"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	if got.Name != "t" || !got.Enabled || got.CallCount != 1 || got.Endpoints["ep1"].URL != up.URL || got.Endpoints["ep1"].CallCount != 1 || got.Triggers["t1"].CallCount != 1 {
		t.Errorf("reported target = %s", b)
	}
	if _, err := json.Marshal(e.proxy); err != nil {
		t.Errorf("marshaling the proxy: %v", err)
	}
}
