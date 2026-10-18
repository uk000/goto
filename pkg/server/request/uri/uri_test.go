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

package uri

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/server/intercept"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

const (
	port1 = 18001
	port2 = 18002
)

var stubOnce sync.Once

// stubGlobalFuncs fills the global.Funcs hooks that the server package normally wires up at startup.
// util.WithRequestStore dereferences some of them.
func stubGlobalFuncs() {
	no := func(*http.Request) bool { return false }
	if global.Funcs.IsReadinessProbe == nil {
		global.Funcs.IsReadinessProbe = no
	}
	if global.Funcs.IsLivenessProbe == nil {
		global.Funcs.IsLivenessProbe = no
	}
	if global.Funcs.IsListenerPresent == nil {
		global.Funcs.IsListenerPresent = func(int) bool { return false }
	}
	if global.Funcs.IsListenerOpen == nil {
		global.Funcs.IsListenerOpen = func(int) bool { return false }
	}
	if global.Funcs.GetListenerID == nil {
		global.Funcs.GetListenerID = func(int) string { return "" }
	}
	if global.Funcs.GetListenerLabel == nil {
		global.Funcs.GetListenerLabel = func(*http.Request) string { return "" }
	}
	if global.Funcs.GetListenerLabelForPort == nil {
		global.Funcs.GetListenerLabelForPort = func(int) string { return "" }
	}
	// Set by the tunnel package at startup. Required for requests to non admin URIs.
	if util.WillTunnel == nil {
		util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
	}
}

func resetState() {
	uriLock.Lock()
	defer uriLock.Unlock()
	uriCountsByPort = map[string]map[string]int{}
	uriStatusByPort = map[string]map[string]any{}
	uriDelayByPort = map[string]map[string]any{}
	trackURICallCounts = false
}

// setup clears all URI configs, and returns a router with the URI routes registered.
func setup(t *testing.T) *mux.Router {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
	resetState()
	t.Cleanup(resetState)
	router := mux.NewRouter()
	setRoutes(router)
	return router
}

// newRequest creates a request that arrives on the given listener port and carries a RequestStore, as the server
// does before routing.
func newRequest(method, target string, port int) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, port))
	_, req, _ = util.WithRequestStore(req, nil)
	return req
}

func call(router http.Handler, method, target string, port int) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, newRequest(method, target, port))
	return rr
}

// callHandler invokes a handler directly, without route vars, which reaches the validation branches that no route
// can reach.
func callHandler(h http.HandlerFunc, port int) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	h(rr, newRequest("POST", "/uri", port))
	return rr
}

func expectStatus(t *testing.T, rr *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rr.Code != want {
		t.Fatalf("status = %d, want %d (body: %q)", rr.Code, want, rr.Body.String())
	}
}

// expectNotRouted verifies that no route accepts the request. Depending on how the routers are nested, gorilla/mux
// reports a method mismatch as either 404 or 405.
func expectNotRouted(t *testing.T, rr *httptest.ResponseRecorder) {
	t.Helper()
	if rr.Code != http.StatusNotFound && rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 404 or 405 (body: %q)", rr.Code, rr.Body.String())
	}
}

func expectBodyContains(t *testing.T, rr *httptest.ResponseRecorder, want string) {
	t.Helper()
	if !strings.Contains(rr.Body.String(), want) {
		t.Errorf("body = %q, want it to contain %q", rr.Body.String(), want)
	}
}

func portText(port int) string { return strconv.Itoa(port) }

func statusConfig(t *testing.T, port int, uri string) *URIStatusConfig {
	t.Helper()
	uriLock.RLock()
	defer uriLock.RUnlock()
	cfg, _ := uriStatusByPort[portText(port)][uri].(*URIStatusConfig)
	return cfg
}

func delayConfig(t *testing.T, port int, uri string) *DelayConfig {
	t.Helper()
	uriLock.RLock()
	defer uriLock.RUnlock()
	cfg, _ := uriDelayByPort[portText(port)][uri].(*DelayConfig)
	return cfg
}

func TestMiddlewareRegistration(t *testing.T) {
	if Middleware == nil {
		t.Fatal("Middleware is nil")
	}
	if Middleware.Name != "uri" {
		t.Errorf("Middleware.Name = %q, want %q", Middleware.Name, "uri")
	}
	if Middleware.SetRoutes == nil || Middleware.MiddlewareHandler == nil {
		t.Error("Middleware must provide both routes and a handler")
	}
}

func TestInitPort(t *testing.T) {
	setup(t)
	port := initPort(newRequest("GET", "/foo", port1))
	if port != portText(port1) {
		t.Errorf("initPort = %q, want %q", port, portText(port1))
	}
	if uriCountsByPort[port] == nil || uriStatusByPort[port] == nil || uriDelayByPort[port] == nil {
		t.Error("initPort must create the maps of the port")
	}

	// The maps themselves are created on demand too.
	uriCountsByPort, uriStatusByPort, uriDelayByPort = nil, nil, nil
	initPort(newRequest("GET", "/foo", port2))
	if uriCountsByPort[portText(port2)] == nil || uriStatusByPort[portText(port2)] == nil || uriDelayByPort[portText(port2)] == nil {
		t.Error("initPort must create the maps when none exist")
	}
}

func TestSetStatus(t *testing.T) {
	tests := []struct {
		name         string
		target       string
		uri          string
		wantStatuses []int
		wantTimes    int
		wantGlob     bool
		wantMessage  string
	}{
		{"forever", "/uri/set/status=418?uri=/foo", "/foo", []int{418}, -1, false,
			"Port [" + portText(port1) + "] URI [/foo] status set to [418] forever"},
		{"limited number of calls", "/uri/set/status=418:2?uri=/foo", "/foo", []int{418}, 2, false,
			"URI [/foo] status set to [418] for next [2] calls"},
		{"one of several statuses", "/uri/set/status=418,500:3?uri=/foo", "/foo", []int{418, 500}, 3, false,
			"URI [/foo] status set to [418 500] for next [3] calls"},
		{"glob", "/uri/set/status=503?uri=/foo*", "/foo", []int{503}, -1, true,
			"URI [/foo*] status set to [503] forever"},
		{"uri is lowercased", "/uri/set/status=404?uri=/Foo/Bar", "/foo/bar", []int{404}, -1, false,
			"URI [/foo/bar] status set to [404] forever"},
	}
	for _, tt := range tests {
		for _, method := range []string{"POST", "PUT"} {
			t.Run(tt.name+"/"+method, func(t *testing.T) {
				router := setup(t)
				rr := call(router, method, tt.target, port1)
				expectStatus(t, rr, http.StatusOK)
				expectBodyContains(t, rr, tt.wantMessage)

				cfg := statusConfig(t, port1, tt.uri)
				if cfg == nil {
					t.Fatalf("uriStatusByPort = %v, want a config for %s", uriStatusByPort, tt.uri)
				}
				if cfg.URI != tt.uri || cfg.Glob != tt.wantGlob || cfg.Times != tt.wantTimes || !reflect.DeepEqual(cfg.Statuses, tt.wantStatuses) {
					t.Errorf("config = %+v, want uri=%s glob=%v times=%d statuses=%v", cfg, tt.uri, tt.wantGlob, tt.wantTimes, tt.wantStatuses)
				}
				if statusConfig(t, port2, tt.uri) != nil {
					t.Error("the config must only apply to the requested port")
				}
			})
		}
	}
}

func TestSetStatusReplacesAndClears(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(router, "POST", "/uri/set/status=418?uri=/foo", port1), http.StatusOK)
	expectStatus(t, call(router, "POST", "/uri/set/status=500:1?uri=/foo", port1), http.StatusOK)
	if cfg := statusConfig(t, port1, "/foo"); cfg == nil || cfg.Statuses[0] != 500 || cfg.Times != 1 {
		t.Fatalf("config = %+v, want it replaced by 500 for 1 call", cfg)
	}

	expectStatus(t, call(router, "POST", "/uri/set/status=418?uri=/bar", port1), http.StatusOK)
	rr := call(router, "POST", "/uri/set/status=0?uri=/foo", port1)
	expectStatus(t, rr, http.StatusOK)
	expectBodyContains(t, rr, "URI [/foo] status cleared")
	if statusConfig(t, port1, "/foo") != nil {
		t.Error("status 0 must clear the config")
	}
	if statusConfig(t, port1, "/bar") == nil {
		t.Error("clearing one URI must not affect others")
	}
}

func TestSetStatusClearsGlobConfig(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(router, "POST", "/uri/set/status=418?uri=/foo*", port1), http.StatusOK)
	expectStatus(t, call(router, "POST", "/uri/set/status=0?uri=/foo*", port1), http.StatusOK)
	if statusConfig(t, port1, "/foo") != nil {
		t.Error("a glob config must be cleared with the same glob URI")
	}
}

func TestSetStatusWithoutURI(t *testing.T) {
	setup(t)
	rr := callHandler(setStatus, port1)
	expectStatus(t, rr, http.StatusBadRequest)
	expectBodyContains(t, rr, "Cannot add. Invalid URI")
}

func TestSetStatusRoutesRequireURIAndMethod(t *testing.T) {
	router := setup(t)
	expectNotRouted(t, call(router, "POST", "/uri/set/status=418", port1))
	expectNotRouted(t, call(router, "GET", "/uri/set/status=418?uri=/foo", port1))
	if statusConfig(t, port1, "/foo") != nil {
		t.Error("rejected requests must not change the config")
	}
}

func TestSetDelay(t *testing.T) {
	tests := []struct {
		name        string
		target      string
		uri         string
		wantDelay   time.Duration
		wantTimes   int
		wantGlob    bool
		wantMessage string
	}{
		{"forever", "/uri/set/delay=1ms?uri=/foo", "/foo", time.Millisecond, 0, false,
			"Port [" + portText(port1) + "] will delay all requests for URI [/foo] by [1ms] forever"},
		{"limited number of calls", "/uri/set/delay=2s:3?uri=/foo", "/foo", 2 * time.Second, 3, false,
			"will delay next [3] requests for URI [/foo] by [2s]"},
		{"glob", "/uri/set/delay=1ms?uri=/foo*", "/foo", time.Millisecond, 0, true,
			"will delay all requests for URI [/foo*] by [1ms] forever"},
		{"uri is lowercased", "/uri/set/delay=1ms?uri=/Foo", "/foo", time.Millisecond, 0, false,
			"URI [/foo] by [1ms]"},
		{"invalid count means forever", "/uri/set/delay=1ms:abc?uri=/foo", "/foo", time.Millisecond, 0, false,
			"will delay all requests for URI [/foo] by [1ms] forever"},
	}
	for _, tt := range tests {
		for _, method := range []string{"POST", "PUT"} {
			t.Run(tt.name+"/"+method, func(t *testing.T) {
				router := setup(t)
				rr := call(router, method, tt.target, port1)
				expectStatus(t, rr, http.StatusOK)
				expectBodyContains(t, rr, tt.wantMessage)

				cfg := delayConfig(t, port1, tt.uri)
				if cfg == nil {
					t.Fatalf("uriDelayByPort = %v, want a config for %s", uriDelayByPort, tt.uri)
				}
				if cfg.URI != tt.uri || cfg.Glob != tt.wantGlob || cfg.Times != tt.wantTimes || cfg.Delay != tt.wantDelay {
					t.Errorf("config = %+v, want uri=%s glob=%v times=%d delay=%s", cfg, tt.uri, tt.wantGlob, tt.wantTimes, tt.wantDelay)
				}
				if delayConfig(t, port2, tt.uri) != nil {
					t.Error("the config must only apply to the requested port")
				}
			})
		}
	}
}

func TestSetDelayClears(t *testing.T) {
	for _, delay := range []string{"0s", "invalid", ""} {
		t.Run("delay="+delay, func(t *testing.T) {
			router := setup(t)
			expectStatus(t, call(router, "POST", "/uri/set/delay=1ms?uri=/foo", port1), http.StatusOK)
			expectStatus(t, call(router, "POST", "/uri/set/delay=1ms?uri=/bar", port1), http.StatusOK)

			rr := call(router, "POST", "/uri/set/delay="+delay+"?uri=/foo", port1)
			if delay == "" {
				expectNotRouted(t, rr) // the route needs a delay value
				return
			}
			expectStatus(t, rr, http.StatusOK)
			expectBodyContains(t, rr, "URI [/foo] delay cleared")
			if delayConfig(t, port1, "/foo") != nil {
				t.Error("a delay that is zero or invalid must clear the config")
			}
			if delayConfig(t, port1, "/bar") == nil {
				t.Error("clearing one URI must not affect others")
			}
		})
	}
}

func TestSetDelayWithoutURI(t *testing.T) {
	setup(t)
	rr := callHandler(setDelay, port1)
	expectStatus(t, rr, http.StatusBadRequest)
	expectBodyContains(t, rr, "Cannot add. Invalid URI")
}

func TestSetDelayRoutesRequireURIAndMethod(t *testing.T) {
	router := setup(t)
	expectNotRouted(t, call(router, "POST", "/uri/set/delay=1ms", port1))
	expectNotRouted(t, call(router, "GET", "/uri/set/delay=1ms?uri=/foo", port1))
	if delayConfig(t, port1, "/foo") != nil {
		t.Error("rejected requests must not change the config")
	}
}

func TestEnableAndDisableURICallCounts(t *testing.T) {
	for _, method := range []string{"POST", "PUT"} {
		t.Run(method, func(t *testing.T) {
			router := setup(t)
			rr := call(router, method, "/uri/counts/enable", port1)
			expectStatus(t, rr, http.StatusOK)
			expectBodyContains(t, rr, "Port ["+portText(port1)+"] URI Call Counts Enabled")
			if !trackURICallCounts {
				t.Error("counting must be enabled")
			}

			rr = call(router, method, "/uri/counts/disable", port1)
			expectStatus(t, rr, http.StatusOK)
			expectBodyContains(t, rr, "Port ["+portText(port1)+"] URI Call Counts Disabled")
			if trackURICallCounts {
				t.Error("counting must be disabled")
			}
		})
	}
	router := setup(t)
	expectNotRouted(t, call(router, "GET", "/uri/counts/enable", port1))
	expectNotRouted(t, call(router, "GET", "/uri/counts/disable", port1))
}

func TestGetURICallCounts(t *testing.T) {
	router := setup(t)

	rr := call(router, "GET", "/uri/counts", port1)
	expectStatus(t, rr, http.StatusOK)
	if got := strings.TrimSpace(rr.Body.String()); got != "{}" {
		t.Errorf("counts = %s, want {} when nothing was counted", got)
	}

	initPort(newRequest("GET", "/", port1))
	initPort(newRequest("GET", "/", port2))
	uriCountsByPort[portText(port1)]["/foo"] = 3
	uriCountsByPort[portText(port1)]["/bar"] = 1
	uriCountsByPort[portText(port2)]["/other"] = 9

	counts := map[string]int{}
	rr = call(router, "GET", "/uri/counts", port1)
	expectStatus(t, rr, http.StatusOK)
	if err := json.Unmarshal(rr.Body.Bytes(), &counts); err != nil {
		t.Fatalf("response %q is not valid JSON: %v", rr.Body.String(), err)
	}
	if !reflect.DeepEqual(counts, map[string]int{"/foo": 3, "/bar": 1}) {
		t.Errorf("counts = %v, want only the counts of the requested port", counts)
	}

	expectNotRouted(t, call(router, "POST", "/uri/counts", port1))
}

func TestClearURICallCounts(t *testing.T) {
	router := setup(t)
	initPort(newRequest("GET", "/", port1))
	initPort(newRequest("GET", "/", port2))
	uriCountsByPort[portText(port1)]["/foo"] = 3
	uriCountsByPort[portText(port2)]["/foo"] = 5

	rr := call(router, "POST", "/uri/counts/clear", port1)
	expectStatus(t, rr, http.StatusOK)
	expectBodyContains(t, rr, "Port ["+portText(port1)+"] URI Call Counts Cleared")
	if len(uriCountsByPort[portText(port1)]) != 0 {
		t.Errorf("counts = %v, want them cleared", uriCountsByPort[portText(port1)])
	}
	if uriCountsByPort[portText(port2)]["/foo"] != 5 {
		t.Error("clearing must only affect the requested port")
	}

	expectNotRouted(t, call(router, "GET", "/uri/counts/clear", port1))
	expectNotRouted(t, call(router, "PUT", "/uri/counts/clear", port1))
}

func TestGetURIConfigs(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(router, "POST", "/uri/set/status=418:2?uri=/foo", port1), http.StatusOK)
	expectStatus(t, call(router, "POST", "/uri/set/delay=1s?uri=/bar*", port1), http.StatusOK)
	expectStatus(t, call(router, "POST", "/uri/set/status=500?uri=/other", port2), http.StatusOK)
	uriCountsByPort[portText(port1)]["/foo"] = 4

	rr := call(router, "GET", "/uri", port1)
	expectStatus(t, rr, http.StatusOK)
	config := struct {
		Delays   map[string]*DelayConfig     `json:"uriDelayByPort"`
		Statuses map[string]*URIStatusConfig `json:"uriStatusByPort"`
		Counts   map[string]int              `json:"uriCountsByPort"`
	}{}
	if err := json.Unmarshal(rr.Body.Bytes(), &config); err != nil {
		t.Fatalf("response %q is not valid JSON: %v", rr.Body.String(), err)
	}
	if got := config.Statuses["/foo"]; got == nil || got.URI != "/foo" || got.Times != 2 || !reflect.DeepEqual(got.Statuses, []int{418}) {
		t.Errorf("statuses = %+v, want /foo with 418 for 2 calls", config.Statuses)
	}
	if got := config.Delays["/bar"]; got == nil || got.URI != "/bar" || !got.Glob || got.Delay != time.Second {
		t.Errorf("delays = %+v, want glob /bar with a delay of 1s", config.Delays)
	}
	if !reflect.DeepEqual(config.Counts, map[string]int{"/foo": 4}) {
		t.Errorf("counts = %v, want /foo=4", config.Counts)
	}
	if _, present := config.Statuses["/other"]; present || len(config.Statuses) != 1 {
		t.Errorf("statuses = %v, want only the configs of the requested port", config.Statuses)
	}

	expectNotRouted(t, call(router, "POST", "/uri", port1))
}

func TestGetURIConfigsWhenEmpty(t *testing.T) {
	router := setup(t)
	rr := call(router, "GET", "/uri", port1)
	expectStatus(t, rr, http.StatusOK)
	for _, key := range []string{"uriDelayByPort", "uriStatusByPort", "uriCountsByPort"} {
		expectBodyContains(t, rr, `"`+key+`":{}`)
	}
}

func TestGetURIStatus(t *testing.T) {
	tests := []struct {
		name   string
		config *URIStatusConfig
		path   string
		port   int
		want   bool
	}{
		{"exact match", &URIStatusConfig{URI: "/foo", Statuses: []int{418}, Times: 2}, "/foo", port1, true},
		{"exact match forever", &URIStatusConfig{URI: "/foo", Statuses: []int{418}, Times: -1}, "/foo", port1, true},
		{"match is case insensitive", &URIStatusConfig{URI: "/foo", Statuses: []int{418}, Times: 1}, "/FOO", port1, true},
		{"query is ignored", &URIStatusConfig{URI: "/foo", Statuses: []int{418}, Times: 1}, "/foo?x=1", port1, true},
		{"glob prefix match", &URIStatusConfig{URI: "/foo", Glob: true, Statuses: []int{418}, Times: 1}, "/foo/bar", port1, true},
		{"glob exact match", &URIStatusConfig{URI: "/foo", Glob: true, Statuses: []int{418}, Times: 1}, "/foo", port1, true},
		{"prefix does not match without glob", &URIStatusConfig{URI: "/foo", Statuses: []int{418}, Times: 1}, "/foo/bar", port1, false},
		{"other uri", &URIStatusConfig{URI: "/foo", Statuses: []int{418}, Times: 1}, "/bar", port1, false},
		{"other port", &URIStatusConfig{URI: "/foo", Statuses: []int{418}, Times: 1}, "/foo", port2, false},
		{"no calls left", &URIStatusConfig{URI: "/foo", Statuses: []int{418}, Times: 0}, "/foo", port1, false},
		{"no statuses", &URIStatusConfig{URI: "/foo", Times: 1}, "/foo", port1, false},
		{"zero status", &URIStatusConfig{URI: "/foo", Statuses: []int{0}, Times: 1}, "/foo", port1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setup(t)
			initPort(newRequest("GET", "/", port1))
			uriStatusByPort[portText(port1)][tt.config.URI] = tt.config

			req := newRequest("GET", tt.path, tt.port)
			got := GetURIStatus(req)
			if (got != nil) != tt.want {
				t.Fatalf("GetURIStatus(%s) = %+v, want match=%v", tt.path, got, tt.want)
			}
			if got != nil && got != tt.config {
				t.Errorf("GetURIStatus returned %+v, want the configured %+v", got, tt.config)
			}
		})
	}
}

func TestHasURIStatus(t *testing.T) {
	setup(t)
	initPort(newRequest("GET", "/", port1))
	uriStatusByPort[portText(port1)]["/foo"] = &URIStatusConfig{URI: "/foo", Statuses: []int{418}, Times: 2}
	uriStatusByPort[portText(port1)]["/glob"] = &URIStatusConfig{URI: "/glob", Glob: true, Statuses: []int{418}, Times: 2}

	for _, tt := range []struct {
		path string
		port int
		want bool
	}{
		{"/foo", port1, true},
		{"/FOO", port1, true},
		{"/foo/bar", port1, false}, // not a glob config
		{"/glob/bar", port1, true},
		{"/bar", port1, false},
		{"/foo", port2, false},
	} {
		if got := HasURIStatus(newRequest("GET", tt.path, tt.port)); got != tt.want {
			t.Errorf("HasURIStatus(%s, port %d) = %v, want %v", tt.path, tt.port, got, tt.want)
		}
	}
}

func TestGetURIDelay(t *testing.T) {
	tests := []struct {
		name   string
		config *DelayConfig
		path   string
		port   int
		want   bool
	}{
		{"exact match forever", &DelayConfig{URI: "/foo", Delay: time.Millisecond}, "/foo", port1, true},
		{"exact match with calls left", &DelayConfig{URI: "/foo", Delay: time.Millisecond, Times: 3}, "/foo", port1, true},
		{"match is case insensitive", &DelayConfig{URI: "/foo", Delay: time.Millisecond}, "/FOO", port1, true},
		{"glob prefix match", &DelayConfig{URI: "/foo", Glob: true, Delay: time.Millisecond}, "/foo/bar", port1, true},
		{"prefix does not match without glob", &DelayConfig{URI: "/foo", Delay: time.Millisecond}, "/foo/bar", port1, false},
		{"other uri", &DelayConfig{URI: "/foo", Delay: time.Millisecond}, "/bar", port1, false},
		{"other port", &DelayConfig{URI: "/foo", Delay: time.Millisecond}, "/foo", port2, false},
		{"no delay", &DelayConfig{URI: "/foo"}, "/foo", port1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setup(t)
			initPort(newRequest("GET", "/", port1))
			uriDelayByPort[portText(port1)][tt.config.URI] = tt.config

			got := GetURIDelay(newRequest("GET", tt.path, tt.port))
			if (got != nil) != tt.want {
				t.Fatalf("GetURIDelay(%s) = %+v, want match=%v", tt.path, got, tt.want)
			}
			if got != nil && got != tt.config {
				t.Errorf("GetURIDelay returned %+v, want the configured %+v", got, tt.config)
			}
		})
	}
}

func TestHasAnyURIStatusOrDelay(t *testing.T) {
	router := setup(t)
	if HasAnyURIStatusOrDelay() {
		t.Error("nothing is configured yet")
	}
	expectStatus(t, call(router, "POST", "/uri/set/status=418?uri=/foo", port1), http.StatusOK)
	if !HasAnyURIStatusOrDelay() {
		t.Error("a port with a configured status must be reported")
	}

	setup(t)
	expectStatus(t, call(router, "POST", "/uri/set/delay=1ms?uri=/foo", port1), http.StatusOK)
	if !HasAnyURIStatusOrDelay() {
		t.Error("a port with a configured delay must be reported")
	}
}

// served is the outcome of a request that went through the URI middleware.
type served struct {
	rr       *httptest.ResponseRecorder
	rs       *util.RequestStore
	irw      *intercept.InterceptResponseWriter
	reached  bool
	duration time.Duration
}

// serve runs a GET request through the URI middleware, in front of a handler that responds with 202. As in the
// server, the response is intercepted, and held until Proceed.
func serve(path string, port int) *served {
	s := &served{rr: httptest.NewRecorder()}
	req := newRequest("GET", path, port)
	s.rs = util.GetRequestStore(req)
	w, irw := intercept.WithIntercept(req, s.rr)
	s.irw = irw
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.reached = true
		w.WriteHeader(http.StatusAccepted)
	})
	start := time.Now()
	Middleware.MiddlewareHandler(next).ServeHTTP(w, req)
	s.duration = time.Since(start)
	irw.Proceed()
	return s
}

func TestMiddlewareWithoutConfigs(t *testing.T) {
	setup(t)
	rr := httptest.NewRecorder()
	req := newRequest("GET", "/foo", port1)
	reached := false
	Middleware.MiddlewareHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusAccepted)
	})).ServeHTTP(rr, req)
	if !reached || rr.Code != http.StatusAccepted {
		t.Errorf("reached=%v status=%d, the request must be served as is", reached, rr.Code)
	}
}

func TestMiddlewareAppliesForcedStatus(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(router, "POST", "/uri/set/status=418?uri=/foo", port1), http.StatusOK)

	s := serve("/foo", port1)
	if !s.reached {
		t.Error("the request must still reach the next handler")
	}
	if s.rr.Code != 418 || s.irw.StatusCode != 418 || s.rs.StatusCode != 418 {
		t.Errorf("status response/intercepted/store = %d/%d/%d, want 418 for all", s.rr.Code, s.irw.StatusCode, s.rs.StatusCode)
	}
	if got := s.rr.Header().Get(constants.HeaderGotoURIStatus); got != "418" {
		t.Errorf("%s = %q, want 418", constants.HeaderGotoURIStatus, got)
	}
	if got := s.rr.Header().Get(constants.HeaderGotoURIStatusRemaining); got != "" {
		t.Errorf("%s = %q, want none for a status that is not limited", constants.HeaderGotoURIStatusRemaining, got)
	}

	// A status without a call limit stays configured.
	if s := serve("/foo", port1); s.rr.Code != 418 {
		t.Errorf("status of the second call = %d, want 418", s.rr.Code)
	}
	if statusConfig(t, port1, "/foo") == nil {
		t.Error("a status without a call limit must stay configured")
	}
}

func TestMiddlewareForcedStatusForLimitedCalls(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(router, "POST", "/uri/set/status=418:2?uri=/foo", port1), http.StatusOK)

	s := serve("/foo", port1)
	if s.rr.Code != 418 {
		t.Errorf("status of the first call = %d, want 418", s.rr.Code)
	}
	if got := s.rr.Header().Get(constants.HeaderGotoURIStatusRemaining); got != "1" {
		t.Errorf("%s = %q, want 1", constants.HeaderGotoURIStatusRemaining, got)
	}

	s = serve("/foo", port1)
	if s.rr.Code != 418 {
		t.Errorf("status of the second call = %d, want 418", s.rr.Code)
	}
	if got := s.rr.Header().Get(constants.HeaderGotoURIStatusRemaining); got != "" {
		t.Errorf("%s = %q, want none on the last call", constants.HeaderGotoURIStatusRemaining, got)
	}
	if statusConfig(t, port1, "/foo") != nil {
		t.Error("the config must be removed once its calls are used up")
	}

	s = serve("/foo", port1)
	if s.rr.Code != http.StatusAccepted || s.rr.Header().Get(constants.HeaderGotoURIStatus) != "" {
		t.Errorf("status of the third call = %d, want the handler's 202 without forced status", s.rr.Code)
	}
}

func TestMiddlewareForcedStatusFromList(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(router, "POST", "/uri/set/status=418,500,503?uri=/foo", port1), http.StatusOK)
	for i := 0; i < 20; i++ {
		switch s := serve("/foo", port1); s.rr.Code {
		case 418, 500, 503:
		default:
			t.Fatalf("status = %d, want one of the configured statuses", s.rr.Code)
		}
	}
}

func TestMiddlewareForcedStatusMatching(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(router, "POST", "/uri/set/status=418?uri=/exact", port1), http.StatusOK)
	expectStatus(t, call(router, "POST", "/uri/set/status=503?uri=/prefix*", port1), http.StatusOK)

	for _, tt := range []struct {
		path string
		port int
		want int
	}{
		{"/exact", port1, 418},
		{"/EXACT", port1, 418},
		{"/exact?x=1", port1, 418},
		{"/exact/more", port1, http.StatusAccepted},
		{"/prefix", port1, 503},
		{"/prefix/more", port1, 503},
		{"/prefixed", port1, 503},
		{"/other", port1, http.StatusAccepted},
		{"/exact", port2, http.StatusAccepted},
	} {
		if s := serve(tt.path, tt.port); s.rr.Code != tt.want {
			t.Errorf("status for %s on port %d = %d, want %d", tt.path, tt.port, s.rr.Code, tt.want)
		}
	}
}

func TestMiddlewareAppliesDelay(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(router, "POST", "/uri/set/delay=5ms?uri=/foo", port1), http.StatusOK)

	s := serve("/foo", port1)
	if !s.reached || s.rr.Code != http.StatusAccepted {
		t.Errorf("reached=%v status=%d, a delayed request must be served normally", s.reached, s.rr.Code)
	}
	if s.duration < 5*time.Millisecond {
		t.Errorf("request took %s, want at least the 5ms delay", s.duration)
	}
	if got := s.rr.Header().Get(constants.HeaderGotoResponseDelay); got != "5ms" {
		t.Errorf("%s = %q, want 5ms", constants.HeaderGotoResponseDelay, got)
	}
	if delayConfig(t, port1, "/foo") == nil {
		t.Error("a delay without a call limit must stay configured")
	}

	if s := serve("/bar", port1); s.rr.Header().Get(constants.HeaderGotoResponseDelay) != "" {
		t.Error("other URIs must not be delayed")
	}
}

func TestMiddlewareDelayForLimitedCalls(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(router, "POST", "/uri/set/delay=1ms:2?uri=/foo", port1), http.StatusOK)

	for i := 1; i <= 2; i++ {
		if s := serve("/foo", port1); s.rr.Header().Get(constants.HeaderGotoResponseDelay) != "1ms" {
			t.Errorf("call %d must be delayed", i)
		}
	}
	if delayConfig(t, port1, "/foo") != nil {
		t.Error("the config must be removed once its calls are used up")
	}
	if s := serve("/foo", port1); s.rr.Header().Get(constants.HeaderGotoResponseDelay) != "" {
		t.Error("the third call must not be delayed")
	}
}

func TestMiddlewareAppliesStatusAndDelay(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(router, "POST", "/uri/set/status=418?uri=/foo", port1), http.StatusOK)
	expectStatus(t, call(router, "POST", "/uri/set/delay=1ms?uri=/foo", port1), http.StatusOK)

	s := serve("/foo", port1)
	if s.rr.Code != 418 || s.rr.Header().Get(constants.HeaderGotoResponseDelay) != "1ms" {
		t.Errorf("status=%d delay=%q, want 418 with a delay of 1ms", s.rr.Code, s.rr.Header().Get(constants.HeaderGotoResponseDelay))
	}
}

func TestMiddlewareCountsCalls(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(router, "POST", "/uri/set/status=418?uri=/foo", port1), http.StatusOK)

	serve("/foo", port1)
	if counts := uriCountsByPort[portText(port1)]; len(counts) != 0 {
		t.Errorf("counts = %v, nothing must be counted while counting is disabled", counts)
	}

	expectStatus(t, call(router, "POST", "/uri/counts/enable", port1), http.StatusOK)
	serve("/foo", port1)
	serve("/FOO?x=1", port1)
	if got := uriCountsByPort[portText(port1)]["/foo"]; got != 2 {
		t.Errorf("count of /foo = %d, want 2 (case and query are ignored)", got)
	}

	expectStatus(t, call(router, "POST", "/uri/counts/disable", port1), http.StatusOK)
	serve("/foo", port1)
	if got := uriCountsByPort[portText(port1)]["/foo"]; got != 2 {
		t.Errorf("count of /foo = %d, want it unchanged after disabling", got)
	}
}

func TestMiddlewareSkipsKnownNonTraffic(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(router, "POST", "/uri/set/status=418?uri=/server/request/uri*", port1), http.StatusOK)
	expectStatus(t, call(router, "POST", "/uri/set/delay=50ms?uri=/server/request/uri*", port1), http.StatusOK)

	rr := httptest.NewRecorder()
	req := newRequest("GET", "/server/request/uri", port1)
	if !util.GetRequestStore(req).IsKnownNonTraffic {
		t.Fatal("admin uri must be treated as non traffic")
	}
	reached := false
	start := time.Now()
	Middleware.MiddlewareHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusAccepted)
	})).ServeHTTP(rr, req)
	if !reached || rr.Code != http.StatusAccepted {
		t.Errorf("reached=%v status=%d, admin requests must be served as is", reached, rr.Code)
	}
	if time.Since(start) >= 50*time.Millisecond || rr.Header().Get(constants.HeaderGotoResponseDelay) != "" {
		t.Error("admin requests must not be delayed")
	}
}

func TestMiddlewareWithNilNext(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(router, "POST", "/uri/set/status=418?uri=/foo", port1), http.StatusOK)

	// Unconfigured URIs, configured ones, and admin ones must not panic without a next handler.
	for _, path := range []string{"/bar", "/server/foo"} {
		Middleware.MiddlewareHandler(nil).ServeHTTP(httptest.NewRecorder(), newRequest("GET", path, port1))
	}
	rr := httptest.NewRecorder()
	req := newRequest("GET", "/foo", port1)
	w, irw := intercept.WithIntercept(req, rr)
	Middleware.MiddlewareHandler(nil).ServeHTTP(w, req)
	irw.Proceed()
	if rr.Code != 418 {
		t.Errorf("status = %d, want the forced 418 even without a next handler", rr.Code)
	}
}
