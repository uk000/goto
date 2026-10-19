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

package metrics

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
)

var (
	stubOnce  sync.Once
	routeOnce sync.Once
)

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

// clean empties the metrics and the stats, for a test that looks at exact numbers. The counting is done by goroutines
// that the tests wait for, so there is nothing left in flight from an earlier test.
func clean(t *testing.T) {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
	promMetrics.reset()
	serverStats.init()
	t.Cleanup(func() {
		promMetrics.reset()
		serverStats.init()
	})
}

// metricValue reads a metric of the registry of the server, as the metrics API would report it.
func metricValue(name string, labels map[string]string) (float64, bool) {
	families, err := promMetrics.registry.Gather()
	if err != nil {
		return 0, false
	}
	for _, mf := range families {
		if mf.GetName() != name {
			continue
		}
	metrics:
		for _, m := range mf.GetMetric() {
			if len(m.GetLabel()) != len(labels) {
				continue
			}
			for _, l := range m.GetLabel() {
				if labels[l.GetName()] != l.GetValue() {
					continue metrics
				}
			}
			if m.GetCounter() != nil {
				return m.GetCounter().GetValue(), true
			}
			return m.GetGauge().GetValue(), true
		}
	}
	return 0, false
}

func waitMetric(t *testing.T, name string, labels map[string]string, want float64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, found := metricValue(name, labels)
		if found && got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("metric %s%v = %v (present: %t), want %v", name, labels, got, found, want)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func lbl(kv ...string) map[string]string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

func statsSnapshot() *ServerStats { return serverStats.snapshot() }

func waitStats(t *testing.T, what string, cond func(*ServerStats) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond(statsSnapshot()) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s: %+v", what, statsSnapshot())
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func admin(root, method, target string) *httptest.ResponseRecorder {
	stubOnce.Do(stubGlobalFuncs)
	routeOnce.Do(func() { Middleware.SetRoutes(nil) })
	req := httptest.NewRequest(method, target, nil)
	_, req, _ = util.WithRequestStore(req, nil)
	rr := httptest.NewRecorder()
	middleware.RootRouters[root].ServeHTTP(rr, req)
	return rr
}

func TestNewPrometheusMetricsHasAllCollectorsInARegistryOfItsOwn(t *testing.T) {
	pm := NewPrometheusMetrics()
	other := NewPrometheusMetrics()
	if pm.registry == nil || pm.registry == other.registry {
		t.Fatal("every set of metrics has to have a registry of its own")
	}
	v := reflect.ValueOf(pm).Elem()
	for i := 0; i < v.NumField(); i++ {
		if v.Field(i).IsNil() {
			t.Errorf("collector %s is not made", v.Type().Field(i).Name)
		}
	}
	pm.requestCounts.WithLabelValues("a").Inc()
	if families, err := other.registry.Gather(); err != nil || len(families) != 0 {
		t.Errorf("the registry of another set of metrics has %d families (err %v), want none", len(families), err)
	}
	families, err := pm.registry.Gather()
	if err != nil || len(families) != 1 || families[0].GetName() != "goto_requests_by_type" {
		t.Errorf("families = %v, err = %v, want the one that was counted", families, err)
	}
}

func TestUpdateCountsOfRequests(t *testing.T) {
	type check struct {
		name   string
		labels map[string]string
	}
	tests := []struct {
		name   string
		update func(label string)
		checks func(label string) []check
	}{
		{"by type", func(l string) { UpdateRequestCount(l) },
			func(l string) []check { return []check{{"goto_requests_by_type", lbl("requestType", l)}} }},
		{"by uri", func(l string) { UpdateURIRequestCount(l, "200") },
			func(l string) []check { return []check{{"goto_requests_by_uris", lbl("uri", l)}} }},
		{"by port", func(l string) { UpdatePortRequestCount("8080", l) },
			func(l string) []check {
				return []check{
					{"goto_requests_by_port_and_uris", lbl("port", "8080", "uri", l)},
				}
			}},
		{"by protocol", func(l string) { UpdateProtocolRequestCount(l, "/proto-uri") },
			func(l string) []check {
				return []check{
					{"goto_requests_by_protocol", lbl("protocol", l)},
					{"goto_requests_by_protocol_and_uris", lbl("protocol", l, "uri", "/proto-uri")},
				}
			}},
		{"by header", func(l string) { UpdateHeaderRequestCount("9090", "/uri", l, "v1") },
			func(l string) []check {
				return []check{
					{"goto_requests_by_headers", lbl("requestHeader", l)},
					{"goto_requests_by_header_values", lbl("requestHeader", l, "headerValue", "v1")},
					{"goto_requests_by_headers_and_uris", lbl("requestHeader", l, "uri", "/uri")},
					{"goto_requests_by_port_and_headers", lbl("port", "9090", "requestHeader", l)},
					{"goto_requests_by_port_and_header_values", lbl("port", "9090", "requestHeader", l, "headerValue", "v1")},
				}
			}},
		{"by target", func(l string) { UpdateTargetRequestCount(l) },
			func(l string) []check { return []check{{"goto_invocations_by_targets", lbl("target", l)}} }},
		{"failures by target", func(l string) { UpdateTargetFailureCount(l) },
			func(l string) []check { return []check{{"goto_failed_invocations_by_targets", lbl("target", l)}} }},
		{"by client", func(l string) { UpdateClientRequestCount(l) },
			func(l string) []check { return []check{{"goto_requests_by_client", lbl("client", l)}} }},
		{"proxied", func(l string) { UpdateProxiedRequestCount(l) },
			func(l string) []check { return []check{{"goto_proxied_requests", lbl("proxyTarget", l)}} }},
		{"triggers", func(l string) { UpdateTriggerCount(l) },
			func(l string) []check { return []check{{"goto_triggers", lbl("triggerTarget", l)}} }},
		{"connections", func(l string) { UpdateConnCount(l) },
			func(l string) []check { return []check{{"goto_conn_counts", lbl("connType", l)}} }},
		{"tcp connections", func(l string) { UpdateTCPConnCount(l) },
			func(l string) []check { return []check{{"goto_tcp_conn_counts", lbl("tcpType", l)}} }},
		{"total connections of target", func(l string) { IncrementTargetConnCount(l) },
			func(l string) []check { return []check{{"goto_total_conns_by_targets", lbl("target", l)}} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clean(t)
			label := "label-" + strings.ReplaceAll(tt.name, " ", "-")
			const n = 7
			for i := 0; i < n; i++ {
				tt.update(label)
			}
			for _, c := range tt.checks(label) {
				waitMetric(t, c.name, c.labels, n)
			}
			// A label of another value is counted on its own.
			tt.update("other-" + label)
			for _, c := range tt.checks(label) {
				waitMetric(t, c.name, c.labels, n)
			}
		})
	}
}

func TestUpdatePortRequestCountCountsPortAlone(t *testing.T) {
	clean(t)
	UpdatePortRequestCount("8081", "/a")
	UpdatePortRequestCount("8081", "/b")
	UpdatePortRequestCount("8082", "/a")
	waitMetric(t, "goto_requests_by_port", lbl("port", "8081"), 2)
	waitMetric(t, "goto_requests_by_port", lbl("port", "8082"), 1)
	waitMetric(t, "goto_requests_by_port_and_uris", lbl("port", "8081", "uri", "/b"), 1)
}

func TestUpdateActiveTargetConnCountSetsTheGauge(t *testing.T) {
	clean(t)
	UpdateActiveTargetConnCount("t1", 5)
	waitMetric(t, "goto_active_conn_counts_by_targets", lbl("target", "t1"), 5)
	UpdateActiveTargetConnCount("t1", 2)
	waitMetric(t, "goto_active_conn_counts_by_targets", lbl("target", "t1"), 2)
	UpdateActiveTargetConnCount("t2", 9)
	waitMetric(t, "goto_active_conn_counts_by_targets", lbl("target", "t2"), 9)
	waitMetric(t, "goto_active_conn_counts_by_targets", lbl("target", "t1"), 2)
}

func TestConnTrackerCountsConnectionsOfTargets(t *testing.T) {
	clean(t)
	for i := 0; i < 3; i++ {
		ConnTracker <- "tracked-target"
	}
	ConnTracker <- "other-target"
	waitMetric(t, "goto_total_conns_by_targets", lbl("target", "tracked-target"), 3)
	waitMetric(t, "goto_total_conns_by_targets", lbl("target", "other-target"), 1)
}

func trackerGoroutines() int {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return strings.Count(string(buf[:n]), "pkg/metrics.trackConnections(")
		}
		buf = make([]byte, 2*len(buf))
	}
}

func waitTrackers(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for trackerGoroutines() != want {
		if time.Now().After(deadline) {
			t.Fatalf("%d goroutines track connections, want %d", trackerGoroutines(), want)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// Shutdown stops the goroutine that tracks connections, and is run by the shutdown of the server.
func TestShutdownStopsConnectionTracking(t *testing.T) {
	clean(t)
	waitTrackers(t, 1)
	// Whatever the other tests of the package did, the tracking is started again when this test is done.
	t.Cleanup(func() {
		if trackerGoroutines() == 0 {
			go trackConnections()
		}
		waitTrackers(t, 1)
	})
	global.Shutdown()
	waitTrackers(t, 0)
}

func TestStatsOfRequests(t *testing.T) {
	clean(t)
	UpdateHeaderRequestCount("8080", "/a", "x-h", "v1")
	UpdateHeaderRequestCount("8080", "/a", "x-h", "v1")
	UpdateHeaderRequestCount("8080", "/a", "x-h", "v2")
	UpdateHeaderRequestCount("9090", "/b", "x-i", "v1")
	UpdateURIRequestCount("/a", "200")
	UpdateURIRequestCount("/a", "500")
	UpdateURIRequestCount("/b", "200")
	UpdatePortRequestCount("8080", "/a")
	UpdatePortRequestCount("9090", "/b")
	UpdateProtocolRequestCount("http", "/a")
	UpdateProtocolRequestCount("grpc", "/a")
	UpdateProtocolRequestCount("http", "/a")
	waitStats(t, "all requests to be counted", func(s *ServerStats) bool {
		return s.RequestCountsByHeaders["x-h"] == 3 && s.RequestCountsByHeaders["x-i"] == 1 &&
			s.RequestCountsByURIs["/a"] == 2 && s.RequestCountsByURIs["/b"] == 1 &&
			len(s.RequestCountsByPortAndURIs) == 2 && s.RequestCountsByURIsAndProtocol["/a"]["http"] == 2
	})
	s := statsSnapshot()
	want := &ServerStats{
		RequestCountsByHeaders:             map[string]int{"x-h": 3, "x-i": 1},
		RequestCountsByURIs:                map[string]int{"/a": 2, "/b": 1},
		RequestCountsByURIsAndHeaders:      map[string]map[string]int{"/a": {"x-h": 3}, "/b": {"x-i": 1}},
		RequestCountsByURIsAndHeaderValues: map[string]map[string]map[string]int{"/a": {"x-h": {"v1": 2, "v2": 1}}, "/b": {"x-i": {"v1": 1}}},
		RequestCountsByPortAndURIs:         map[string]map[string]int{"8080": {"/a": 1}, "9090": {"/b": 1}},
		RequestCountsByPortAndHeaders:      map[string]map[string]int{"8080": {"x-h": 3}, "9090": {"x-i": 1}},
		RequestCountsByPortAndHeaderValues: map[string]map[string]map[string]int{"8080": {"x-h": {"v1": 2, "v2": 1}}, "9090": {"x-i": {"v1": 1}}},
		RequestCountsByURIsAndProtocol:     map[string]map[string]int{"/a": {"http": 2, "grpc": 1}},
	}
	if !reflect.DeepEqual(s.RequestCountsByHeaders, want.RequestCountsByHeaders) ||
		!reflect.DeepEqual(s.RequestCountsByURIs, want.RequestCountsByURIs) ||
		!reflect.DeepEqual(s.RequestCountsByURIsAndHeaders, want.RequestCountsByURIsAndHeaders) ||
		!reflect.DeepEqual(s.RequestCountsByURIsAndHeaderValues, want.RequestCountsByURIsAndHeaderValues) ||
		!reflect.DeepEqual(s.RequestCountsByPortAndURIs, want.RequestCountsByPortAndURIs) ||
		!reflect.DeepEqual(s.RequestCountsByPortAndHeaders, want.RequestCountsByPortAndHeaders) ||
		!reflect.DeepEqual(s.RequestCountsByPortAndHeaderValues, want.RequestCountsByPortAndHeaderValues) ||
		!reflect.DeepEqual(s.RequestCountsByURIsAndProtocol, want.RequestCountsByURIsAndProtocol) {
		t.Errorf("stats = %+v\nwant %+v", s, want)
	}
}

func TestStatsSnapshotIsACopy(t *testing.T) {
	clean(t)
	UpdateHeaderRequestCount("1", "/u", "h", "v")
	waitStats(t, "the header to be counted", func(s *ServerStats) bool { return s.RequestCountsByHeaders["h"] == 1 })
	s := statsSnapshot()
	s.RequestCountsByHeaders["h"] = 100
	s.RequestCountsByURIsAndHeaderValues["/u"]["h"]["v"] = 100
	s.RequestCountsByPortAndHeaders["1"]["h"] = 100
	s2 := statsSnapshot()
	if s2.RequestCountsByHeaders["h"] != 1 || s2.RequestCountsByURIsAndHeaderValues["/u"]["h"]["v"] != 1 || s2.RequestCountsByPortAndHeaders["1"]["h"] != 1 {
		t.Errorf("changing a snapshot changed the stats: %+v", s2)
	}
}

func TestNewServerStatsIsEmptyAndUsable(t *testing.T) {
	ss := NewServerStats()
	for name, m := range map[string]interface{}{
		"headers": ss.RequestCountsByHeaders, "uris": ss.RequestCountsByURIs, "uris and headers": ss.RequestCountsByURIsAndHeaders,
		"uris and header values": ss.RequestCountsByURIsAndHeaderValues, "port and uris": ss.RequestCountsByPortAndURIs,
		"port and headers": ss.RequestCountsByPortAndHeaders, "port and header values": ss.RequestCountsByPortAndHeaderValues,
		"protocol": ss.RequestCountsByURIsAndProtocol,
	} {
		if v := reflect.ValueOf(m); v.IsNil() || v.Len() != 0 {
			t.Errorf("%s = %v, want an empty map that can be written to", name, m)
		}
	}
}

func TestAPIGetStats(t *testing.T) {
	clean(t)
	UpdateURIRequestCount("/api-stats", "200")
	UpdateURIRequestCount("/api-stats", "200")
	waitStats(t, "the uri to be counted", func(s *ServerStats) bool { return s.RequestCountsByURIs["/api-stats"] == 2 })
	rr := admin("/stats", "GET", "/stats")
	if rr.Code != http.StatusOK || rr.Header().Get(constants.HeaderContentType) != constants.ContentTypeJSON {
		t.Fatalf("answered %d with content type %q", rr.Code, rr.Header().Get(constants.HeaderContentType))
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q is not JSON: %v", rr.Body.String(), err)
	}
	for _, key := range []string{"requestCountsByHeaders", "requestCountsByURIs", "requestCountsByURIsAndHeaders",
		"requestCountsByURIsAndHeaderValues", "requestCountsByPortAndURIs", "requestCountsByPortAndHeaders",
		"requestCountsByPortAndHeaderValues", "requestCountsByURIsAndProtocol"} {
		if _, ok := got[key]; !ok {
			t.Errorf("stats %s have no %q", rr.Body.String(), key)
		}
	}
	var uris map[string]int
	if err := json.Unmarshal(got["requestCountsByURIs"], &uris); err != nil || !reflect.DeepEqual(uris, map[string]int{"/api-stats": 2}) {
		t.Errorf("requestCountsByURIs = %s (%v), want the 2 requests", got["requestCountsByURIs"], err)
	}
}

func TestAPIClearStats(t *testing.T) {
	clean(t)
	UpdateURIRequestCount("/to-clear", "200")
	waitStats(t, "the uri to be counted", func(s *ServerStats) bool { return s.RequestCountsByURIs["/to-clear"] == 1 })
	rr := admin("/stats", "POST", "/stats/clear")
	if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != "Stats cleared" {
		t.Errorf("answered %d %q", rr.Code, rr.Body.String())
	}
	if s := statsSnapshot(); len(s.RequestCountsByURIs) != 0 {
		t.Errorf("stats after clearing = %+v", s)
	}
	// Counting goes on after the clearing.
	UpdateURIRequestCount("/after-clear", "200")
	waitStats(t, "the count after clearing", func(s *ServerStats) bool { return s.RequestCountsByURIs["/after-clear"] == 1 })
}

func TestAPIGetMetrics(t *testing.T) {
	clean(t)
	UpdateRequestCount("api-metrics")
	waitMetric(t, "goto_requests_by_type", lbl("requestType", "api-metrics"), 1)
	rr := admin("/metrics", "GET", "/metrics")
	if rr.Code != http.StatusOK {
		t.Fatalf("answered %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `goto_requests_by_type{requestType="api-metrics"} 1`) {
		t.Errorf("metrics do not have the counted request:\n%s", rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "go_goroutines") {
		t.Error("the metrics of the server have the metrics of the Go runtime, which are on their own path")
	}
}

func TestAPIGetGoMetrics(t *testing.T) {
	stubOnce.Do(stubGlobalFuncs)
	rr := admin("/metrics", "GET", "/metrics/go")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "go_goroutines") {
		t.Errorf("answered %d, want the metrics of the Go runtime", rr.Code)
	}
}

func TestAPIClearMetrics(t *testing.T) {
	clean(t)
	UpdateRequestCount("to-clear")
	waitMetric(t, "goto_requests_by_type", lbl("requestType", "to-clear"), 1)
	rr := admin("/metrics", "POST", "/metrics/clear")
	if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != "Metrics cleared" {
		t.Errorf("answered %d %q", rr.Code, rr.Body.String())
	}
	if _, found := metricValue("goto_requests_by_type", lbl("requestType", "to-clear")); found {
		t.Error("the metric is still there after clearing")
	}
	UpdateRequestCount("to-clear")
	waitMetric(t, "goto_requests_by_type", lbl("requestType", "to-clear"), 1)
}

func TestAPIMethodsAreChecked(t *testing.T) {
	stubOnce.Do(stubGlobalFuncs)
	for _, c := range []struct{ root, method, target string }{
		{"/metrics", "POST", "/metrics"}, {"/metrics", "GET", "/metrics/clear"},
		{"/stats", "POST", "/stats"}, {"/stats", "GET", "/stats/clear"},
	} {
		if rr := admin(c.root, c.method, c.target); rr.Code != http.StatusMethodNotAllowed && rr.Code != http.StatusNotFound {
			t.Errorf("%s %s answered %d, want it not to be served", c.method, c.target, rr.Code)
		}
	}
}

func serveThroughMiddleware(next http.Handler, target string) *http.Request {
	req := httptest.NewRequest("GET", target, nil)
	_, req, _ = util.WithRequestStore(req, nil)
	Middleware.MiddlewareHandler(next).ServeHTTP(httptest.NewRecorder(), req)
	return req
}

func TestMiddlewareCountsRequestsByKind(t *testing.T) {
	clean(t)
	served := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { served++ })
	serveThroughMiddleware(next, "/some/traffic")
	serveThroughMiddleware(next, "/another")
	serveThroughMiddleware(next, "/server/response/status/set/200")
	req := serveThroughMiddleware(next, "/metrics")
	serveThroughMiddleware(next, "/stats")
	if served != 5 {
		t.Errorf("the handler served %d requests, want 5", served)
	}
	waitMetric(t, "goto_requests_by_type", lbl("requestType", "all"), 5)
	waitMetric(t, "goto_requests_by_type", lbl("requestType", "admin"), 1)
	waitMetric(t, "goto_requests_by_type", lbl("requestType", "metrics"), 2)
	// What was counted is final.
	time.Sleep(20 * time.Millisecond)
	for kind, want := range map[string]float64{"all": 5, "admin": 1, "metrics": 2} {
		if got, _ := metricValue("goto_requests_by_type", lbl("requestType", kind)); got != want {
			t.Errorf("requests of kind %q = %v, want %v", kind, got, want)
		}
	}
	if logs := util.GetRequestStore(req).LogMessages; !reflect.DeepEqual(logs, []string{"Metrics reported"}) {
		t.Errorf("log messages of the metrics request = %v", logs)
	}
}

func TestMiddlewareWithoutNextHandler(t *testing.T) {
	clean(t)
	serveThroughMiddleware(nil, "/x")
	waitMetric(t, "goto_requests_by_type", lbl("requestType", "all"), 1)
}

func TestMiddlewareIdentity(t *testing.T) {
	if Middleware == nil || Middleware.Name != "metrics" || Middleware.SetRoutes == nil || Middleware.MiddlewareHandler == nil {
		t.Errorf("middleware = %+v", Middleware)
	}
	if cap(ConnTracker) == 0 {
		t.Error("the tracker of connections has no room for reports")
	}
}
