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

package results

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"goto/pkg/global"
	"goto/pkg/invocation"
)

var baseTime = time.Date(2026, time.March, 1, 10, 0, 0, 0, time.UTC)

func at(sec int) time.Time { return baseTime.Add(time.Duration(sec) * time.Second) }

// res describes a result to be added. Zero values mean "not set".
type res struct {
	uri         string
	status      int
	at          time.Time
	took        time.Duration
	reqSize     int
	respSize    int
	retries     int
	retryReason string
	cs, ss      int
	headers     map[string][]string
	errors      int
}

func (r res) build() *invocation.InvocationResult {
	status := r.status
	if status == 0 {
		status = 200
	}
	uri := r.uri
	if uri == "" {
		uri = "/"
	}
	ts := r.at
	if ts.IsZero() {
		ts = at(0)
	}
	ir := &invocation.InvocationResult{
		Request: &invocation.InvocationResultRequest{
			URI:           uri,
			PayloadSize:   r.reqSize,
			LastRequestAt: ts,
		},
		Response: &invocation.InvocationResultResponse{
			Status:            fmt.Sprintf("%d %s", status, http.StatusText(status)),
			StatusCode:        status,
			Headers:           r.headers,
			PayloadSize:       r.respSize,
			ClientStreamCount: r.cs,
			ServerStreamCount: r.ss,
		},
		Retries:         r.retries,
		LastRetryReason: r.retryReason,
		TookNanos:       r.took,
	}
	for i := 0; i < r.errors; i++ {
		ir.Errors = append(ir.Errors, map[string]interface{}{"assertionIndex": i + 1})
	}
	return ir
}

func addAll(tr *TargetResults, rs ...res) {
	for _, r := range rs {
		tr.addResult(r.build())
	}
}

func toJSON(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// drainRegistryChannels stands in for the registry senders: it consumes what the sinks queue for the registry, so
// that the sinks never block on the bounded channels, and no sender goroutines are started by the tests that use the
// sinks. It also isolates the package level state of the results.
func drainRegistryChannels(t *testing.T) {
	t.Helper()
	registrySendLock.Lock()
	prevSending := sendingToRegistry
	sendingToRegistry = true
	registrySendLock.Unlock()
	ClearResults()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-chanSendTargetsToRegistry:
			case <-chanSendInvocationToRegistry:
			case <-stop:
				return
			}
		}
	}()
	t.Cleanup(func() {
		close(stop)
		wg.Wait()
		EnableAllTargetResults(false)
		EnableInvocationResults(false)
		ClearResults()
		registrySendLock.Lock()
		sendingToRegistry = prevSending
		registrySendLock.Unlock()
	})
}

func newTracker(id uint32) *invocation.InvocationTracker {
	return &invocation.InvocationTracker{
		ID:     id,
		Status: &invocation.InvocationStatus{},
		Channels: &invocation.InvocationChannels{
			ResultChannel: make(chan *invocation.InvocationResult, 100),
			DoneChannel:   make(chan bool, 2),
		},
	}
}

func TestNewTargetResultsInitialisesAllCounters(t *testing.T) {
	tr := NewTargetResults("t1", []string{"h1"}, map[string][]string{"h1": {"h2"}}, [][]int{{0, 10}})
	if tr.Target != "t1" || tr.InvocationCount != 0 {
		t.Fatalf("unexpected target results: %+v", tr)
	}
	maps := map[string]bool{
		"CountsByStatus":               tr.CountsByStatus != nil,
		"CountsByHeaders":              tr.CountsByHeaders != nil,
		"CountsByStatusCodes":          tr.CountsByStatusCodes != nil,
		"CountsByURIs":                 tr.CountsByURIs != nil,
		"CountsByRequestPayloadSizes":  tr.CountsByRequestPayloadSizes != nil,
		"CountsByResponsePayloadSizes": tr.CountsByResponsePayloadSizes != nil,
		"CountsByRetries":              tr.CountsByRetries != nil,
		"CountsByRetryReasons":         tr.CountsByRetryReasons != nil,
		"CountsByErrors":               tr.CountsByErrors != nil,
		"CountsByTimeBuckets":          tr.CountsByTimeBuckets != nil,
	}
	for name, ok := range maps {
		if !ok {
			t.Errorf("%s is not initialised", name)
		}
	}
	if !reflect.DeepEqual(tr.crossHeadersMap, map[string]string{"h2": "h1"}) {
		t.Errorf("crossHeadersMap = %v", tr.crossHeadersMap)
	}
}

func TestUnsafeInitResetsOnlyWhenAsked(t *testing.T) {
	tr := NewTargetResults("t1", nil, nil, nil)
	addAll(tr, res{status: 200}, res{status: 500})
	tr.unsafeInit(false)
	if tr.InvocationCount != 2 {
		t.Fatalf("unsafeInit(false) reset the results: %d", tr.InvocationCount)
	}
	tr.unsafeInit(true)
	if tr.InvocationCount != 0 || len(tr.CountsByStatus) != 0 || len(tr.CountsByStatusCodes) != 0 {
		t.Fatalf("unsafeInit(true) didn't reset: %+v", tr)
	}
}

func TestUnsafeInitMissingFillsOnlyNilMaps(t *testing.T) {
	tr := &TargetResults{CountsByStatus: map[string]int{"200 OK": 4}}
	tr.unsafeInitMissing()
	if tr.CountsByStatus["200 OK"] != 4 {
		t.Errorf("existing map was replaced")
	}
	if tr.CountsByHeaders == nil || tr.CountsByStatusCodes == nil || tr.CountsByURIs == nil || tr.CountsByRequestPayloadSizes == nil ||
		tr.CountsByResponsePayloadSizes == nil || tr.CountsByRetries == nil || tr.CountsByRetryReasons == nil ||
		tr.CountsByTimeBuckets == nil || tr.CountsByErrors == nil {
		t.Errorf("missing maps not initialised: %+v", tr)
	}
}

func TestAddResultCountsByStatusURIAndSizes(t *testing.T) {
	tr := NewTargetResults("t1", nil, nil, nil)
	addAll(tr,
		res{uri: "/Orders", status: 200, at: at(1), reqSize: 10, respSize: 100, cs: 1, ss: 2},
		res{uri: "/orders", status: 200, at: at(2), reqSize: 10, respSize: 200, cs: 1, ss: 3},
		res{uri: "/items", status: 503, at: at(3), respSize: 100},
	)
	if tr.InvocationCount != 3 {
		t.Errorf("InvocationCount = %d", tr.InvocationCount)
	}
	if tr.ClientStreamCount != 2 || tr.ServerStreamCount != 5 {
		t.Errorf("stream counts = %d/%d, want 2/5", tr.ClientStreamCount, tr.ServerStreamCount)
	}
	if !reflect.DeepEqual(tr.CountsByStatus, map[string]int{"200 OK": 2, "503 Service Unavailable": 1}) {
		t.Errorf("CountsByStatus = %v", tr.CountsByStatus)
	}
	if c := tr.CountsByStatusCodes["200"]; c == nil || c.Count != 2 || c.ClientStreamCount != 2 || c.ServerStreamCount != 5 {
		t.Errorf("CountsByStatusCodes[200] = %+v", c)
	}
	if c := tr.CountsByStatusCodes["503"]; c == nil || c.Count != 1 {
		t.Errorf("CountsByStatusCodes[503] = %+v", c)
	}
	// the URIs are matched case-insensitively, so /Orders and /orders are counted together
	if len(tr.CountsByURIs) != 2 {
		t.Fatalf("CountsByURIs = %v", toJSON(t, tr.CountsByURIs))
	}
	orders := tr.CountsByURIs["/orders"]
	if orders == nil || orders.Count != 2 || orders.ByStatusCodes["200"].Count != 2 {
		t.Errorf("CountsByURIs[/orders] = %+v", orders)
	}
	if c := tr.CountsByRequestPayloadSizes["10"]; c == nil || c.Count != 2 {
		t.Errorf("CountsByRequestPayloadSizes[10] = %+v", c)
	}
	if _, ok := tr.CountsByRequestPayloadSizes["0"]; ok {
		t.Errorf("empty request payloads are not to be counted")
	}
	if c := tr.CountsByResponsePayloadSizes["100"]; c == nil || c.Count != 2 {
		t.Errorf("CountsByResponsePayloadSizes[100] = %+v", c)
	}
	if c := tr.CountsByResponsePayloadSizes["200"]; c == nil || c.Count != 1 {
		t.Errorf("CountsByResponsePayloadSizes[200] = %+v", c)
	}
	if !tr.FirstResultAt.Equal(at(1)) || !tr.LastResultAt.Equal(at(3)) {
		t.Errorf("first/last = %v/%v", tr.FirstResultAt, tr.LastResultAt)
	}
	if len(tr.CountsByTimeBuckets) != 0 {
		t.Errorf("time buckets were counted without being configured: %v", tr.CountsByTimeBuckets)
	}
}

func TestAddResultFirstResultAtIsEarliestTimestamp(t *testing.T) {
	tr := NewTargetResults("t1", nil, nil, nil)
	addAll(tr, res{at: at(30)}, res{at: at(10)}, res{at: at(20)})
	if !tr.FirstResultAt.Equal(at(10)) {
		t.Errorf("FirstResultAt = %v, want %v", tr.FirstResultAt, at(10))
	}
	if c := tr.CountsByStatusCodes["200"]; !c.FirstResultAt.Equal(at(10)) {
		t.Errorf("status code FirstResultAt = %v, want %v", c.FirstResultAt, at(10))
	}
}

func TestAddResultRetries(t *testing.T) {
	tr := NewTargetResults("t1", nil, nil, nil)
	addAll(tr,
		res{status: 200},
		res{status: 200, retries: 2, retryReason: "503"},
		res{status: 500, retries: 2, retryReason: "503"},
		res{status: 200, retries: 1, retryReason: "connection reset"},
		res{status: 200, retries: 3},
	)
	if tr.RetriedInvocationCounts != 4 {
		t.Errorf("RetriedInvocationCounts = %d, want 4", tr.RetriedInvocationCounts)
	}
	r2 := tr.CountsByRetries["2"]
	if r2 == nil || r2.Count != 2 || r2.Retries != 4 {
		t.Fatalf("CountsByRetries[2] = %+v", r2)
	}
	if r2.ByStatusCodes["200"].Count != 1 || r2.ByStatusCodes["500"].Count != 1 {
		t.Errorf("CountsByRetries[2].ByStatusCodes = %v", toJSON(t, r2.ByStatusCodes))
	}
	if _, ok := tr.CountsByRetries["0"]; ok {
		t.Errorf("invocations without retries are not to be counted by retries")
	}
	if c := tr.CountsByRetryReasons["503"]; c == nil || c.Count != 2 || c.Retries != 4 {
		t.Errorf("CountsByRetryReasons[503] = %+v", c)
	}
	if c := tr.CountsByRetryReasons["connection reset"]; c == nil || c.Count != 1 {
		t.Errorf("CountsByRetryReasons[connection reset] = %+v", c)
	}
	// retries without a reason (3 retries) don't produce an entry with an empty key
	if _, ok := tr.CountsByRetryReasons[""]; ok {
		t.Errorf("empty retry reason was counted")
	}
}

func TestAddResultErrorsAreCountedByPosition(t *testing.T) {
	tr := NewTargetResults("t1", nil, nil, nil)
	addAll(tr, res{status: 200, errors: 2}, res{status: 200, errors: 1}, res{status: 200})
	if c := tr.CountsByErrors["0"]; c == nil || c.Count != 2 {
		t.Errorf("CountsByErrors[0] = %+v", c)
	}
	if c := tr.CountsByErrors["1"]; c == nil || c.Count != 1 {
		t.Errorf("CountsByErrors[1] = %+v", c)
	}
	if len(tr.CountsByErrors) != 2 {
		t.Errorf("CountsByErrors = %v", toJSON(t, tr.CountsByErrors))
	}
}

func TestAddResultTrackedHeaders(t *testing.T) {
	tr := NewTargetResults("t1", []string{"x-env", "x-missing"}, nil, nil)
	addAll(tr,
		res{status: 200, at: at(1), headers: map[string][]string{"X-Env": {"prod"}, "other": {"1"}}},
		res{status: 500, at: at(2), headers: map[string][]string{"x-env": {"prod", "dr"}}},
		res{status: 200, at: at(3), headers: map[string][]string{"x-env": {"dev"}}},
		res{status: 200, at: at(4), headers: map[string][]string{"other": {"1"}}},
	)
	if len(tr.CountsByHeaders) != 1 {
		t.Fatalf("headers counted: %v", toJSON(t, tr.CountsByHeaders))
	}
	h := tr.CountsByHeaders["x-env"]
	if h == nil || h.Header != "x-env" || h.Count != 3 {
		t.Fatalf("x-env counts = %+v", h)
	}
	if !reflect.DeepEqual(h.CountsByStatusCodes["200"].Count, 2) || h.CountsByStatusCodes["500"].Count != 1 {
		t.Errorf("by status codes = %v", toJSON(t, h.CountsByStatusCodes))
	}
	if h.CountsByValues["prod"].Count != 2 || h.CountsByValues["dr"].Count != 1 || h.CountsByValues["dev"].Count != 1 {
		t.Errorf("by values = %v", toJSON(t, h.CountsByValues))
	}
	if h.CountsByValuesStatusCodes["prod"]["200"].Count != 1 || h.CountsByValuesStatusCodes["prod"]["500"].Count != 1 {
		t.Errorf("by values and status codes = %v", toJSON(t, h.CountsByValuesStatusCodes))
	}
	if !h.FirstResultAt.Equal(at(1)) || !h.LastResultAt.Equal(at(3)) {
		t.Errorf("header first/last = %v/%v", h.FirstResultAt, h.LastResultAt)
	}
}

func TestAddResultCrossHeaders(t *testing.T) {
	tr := NewTargetResults("t1", []string{"h1", "h2", "h3"}, map[string][]string{"h1": {"h2", "h3"}}, nil)
	addAll(tr,
		res{status: 200, at: at(1), headers: map[string][]string{"h1": {"a"}, "h2": {"x"}, "h3": {"y"}}},
		res{status: 500, at: at(2), headers: map[string][]string{"h1": {"b"}, "h2": {"x", "z"}}},
		res{status: 200, at: at(3), headers: map[string][]string{"h1": {"a"}}},
	)
	h1 := tr.CountsByHeaders["h1"]
	if h1 == nil || h1.Count != 3 {
		t.Fatalf("h1 = %+v", h1)
	}
	// h1 together with h2: the first and the second results
	cross := h1.CrossHeaders["h2"]
	if cross == nil || cross.Count != 2 {
		t.Fatalf("h1 x h2 = %+v", cross)
	}
	if cross.CountsByValues["x"].Count != 2 || cross.CountsByValues["z"].Count != 1 {
		t.Errorf("h1 x h2 values = %v", toJSON(t, cross.CountsByValues))
	}
	if cross.CountsByStatusCodes["200"].Count != 1 || cross.CountsByStatusCodes["500"].Count != 1 {
		t.Errorf("h1 x h2 status codes = %v", toJSON(t, cross.CountsByStatusCodes))
	}
	// h1 together with h3: only the first result has it
	if c := h1.CrossHeaders["h3"]; c == nil || c.Count != 1 || c.CountsByValues["y"].Count != 1 {
		t.Errorf("h1 x h3 = %v", toJSON(t, c))
	}
	// the headers that follow h2 in the cross headers list are counted against h2's values: h1 x h2 x h3
	if c := cross.CrossHeaders["h3"]; c == nil || c.Count != 1 || c.CountsByValues["y"].Count != 1 {
		t.Errorf("h1 x h2 x h3 = %v", toJSON(t, c))
	}
	// per value of h1
	if c := h1.CrossHeadersByValues["a"]["h2"]; c == nil || c.Count != 1 || c.CountsByValues["x"].Count != 1 {
		t.Errorf("h1=a x h2 = %v", toJSON(t, c))
	}
	if c := h1.CrossHeadersByValues["b"]["h2"]; c == nil || c.Count != 1 || c.CountsByValues["z"].Count != 1 {
		t.Errorf("h1=b x h2 = %v", toJSON(t, c))
	}
	if _, ok := h1.CrossHeadersByValues["b"]["h3"]; ok {
		t.Errorf("h1=b has no h3 in its results")
	}
	// the cross headers are tracked on their own too
	if tr.CountsByHeaders["h2"].Count != 2 || tr.CountsByHeaders["h3"].Count != 1 {
		t.Errorf("h2/h3 counts = %d/%d", tr.CountsByHeaders["h2"].Count, tr.CountsByHeaders["h3"].Count)
	}
}

func TestAddResultTimeBuckets(t *testing.T) {
	buckets := [][]int{{0, 100}, {101, 500}, {501, 0}}
	tr := NewTargetResults("t1", nil, nil, buckets)
	ms := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }
	addAll(tr,
		res{uri: "/a", status: 200, took: ms(50), reqSize: 5, respSize: 7},
		res{uri: "/a", status: 200, took: ms(100), reqSize: 5, respSize: 7},
		res{uri: "/a", status: 500, took: ms(300), retries: 1, retryReason: "500", errors: 1},
		res{uri: "/b", status: 200, took: ms(500)},
		res{uri: "/b", status: 200, took: ms(2000)},
	)
	got := map[string]int{}
	for k, v := range tr.CountsByTimeBuckets {
		got[k] = v.Count
	}
	want := map[string]int{"[0 100]": 2, "[101 500]": 2, "[501 0]": 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CountsByTimeBuckets = %v, want %v", got, want)
	}
	if c := tr.CountsByTimeBuckets["[101 500]"].ByStatusCodes; c["500"].Count != 1 || c["200"].Count != 1 {
		t.Errorf("status codes in bucket [101 500] = %v", toJSON(t, c))
	}
	// the buckets are tracked within the URIs, the status codes and the other counts
	if c := tr.CountsByURIs["/a"].ByTimeBuckets["[0 100]"]; c == nil || c.Count != 2 || c.ByStatusCodes["200"].Count != 2 {
		t.Errorf("/a bucket [0 100] = %v", toJSON(t, c))
	}
	if c := tr.CountsByURIs["/a"].ByTimeBuckets["[101 500]"]; c == nil || c.Count != 1 {
		t.Errorf("/a bucket [101 500] = %v", toJSON(t, c))
	}
	if c := tr.CountsByURIs["/b"].ByTimeBuckets["[501 0]"]; c == nil || c.Count != 1 {
		t.Errorf("/b bucket [501 0] = %v", toJSON(t, c))
	}
	if c := tr.CountsByStatusCodes["200"].ByTimeBuckets["[0 100]"]; c == nil || c.Count != 2 {
		t.Errorf("200 bucket [0 100] = %v", toJSON(t, c))
	}
	if c := tr.CountsByRequestPayloadSizes["5"].ByTimeBuckets["[0 100]"]; c == nil || c.Count != 2 {
		t.Errorf("request size 5 bucket [0 100] = %v", toJSON(t, c))
	}
	if c := tr.CountsByResponsePayloadSizes["7"].ByTimeBuckets["[0 100]"]; c == nil || c.Count != 2 {
		t.Errorf("response size 7 bucket [0 100] = %v", toJSON(t, c))
	}
	if c := tr.CountsByRetries["1"].ByTimeBuckets["[101 500]"]; c == nil || c.Count != 1 {
		t.Errorf("retries 1 bucket [101 500] = %v", toJSON(t, c))
	}
	if c := tr.CountsByRetryReasons["500"].ByTimeBuckets["[101 500]"]; c == nil || c.Count != 1 {
		t.Errorf("retry reason 500 bucket [101 500] = %v", toJSON(t, c))
	}
	if c := tr.CountsByErrors["0"].ByTimeBuckets["[101 500]"]; c == nil || c.Count != 1 {
		t.Errorf("error 0 bucket [101 500] = %v", toJSON(t, c))
	}
}

func TestAddResultTimeBucketsThatDontCoverTheDuration(t *testing.T) {
	tr := NewTargetResults("t1", nil, nil, [][]int{{10, 20}})
	addAll(tr, res{took: 5 * time.Millisecond}, res{took: 15 * time.Millisecond}, res{took: 25 * time.Millisecond})
	if tr.CountsByTimeBuckets["[10 20]"].Count != 1 {
		t.Errorf("bucket [10 20] = %v", toJSON(t, tr.CountsByTimeBuckets))
	}
	if tr.CountsByTimeBuckets["[]"].Count != 2 {
		t.Errorf("the durations outside of all buckets are counted under [], got %v", toJSON(t, tr.CountsByTimeBuckets))
	}
}

func TestTargetsResultsGetTargetResultsCreatesTargetAndAllResults(t *testing.T) {
	trs := &TargetsResults{}
	one, all := trs.getTargetResults("t1")
	if one == nil || all == nil || one == all || one.Target != "t1" || all.Target != "" {
		t.Fatalf("unexpected results %+v %+v", one, all)
	}
	again, all2 := trs.getTargetResults("t1")
	if again != one || all2 != all {
		t.Errorf("results of an existing target must be reused")
	}
	two, _ := trs.getTargetResults("t2")
	if two == one {
		t.Errorf("targets share results")
	}
	trs.init(false)
	if len(trs.Results) != 3 {
		t.Errorf("init(false) must keep the results, got %d entries", len(trs.Results))
	}
	trs.init(true)
	if len(trs.Results) != 1 || trs.Results[""] == nil {
		t.Errorf("init(true) must leave only the all-targets results, got %v", trs.Results)
	}
}

func TestKeyResultJSONRoundTrip(t *testing.T) {
	kr := KeyResult{
		"/a": &KeyResultCounts{
			CountInfo:     CountInfo{Count: 3, Retries: 1, FirstResultAt: at(1), LastResultAt: at(5)},
			ByStatusCodes: KeyResult{"200": &KeyResultCounts{CountInfo: CountInfo{Count: 3}}},
			ByTimeBuckets: KeyResult{},
		},
		"/b": &KeyResultCounts{CountInfo: CountInfo{Count: 1}},
	}
	b, err := json.Marshal(kr)
	if err != nil {
		t.Fatal(err)
	}
	back := KeyResult{}
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back["/a"].Count != 3 || back["/a"].Retries != 1 || !back["/a"].FirstResultAt.Equal(at(1)) || !back["/a"].LastResultAt.Equal(at(5)) {
		t.Errorf("/a = %+v", back["/a"])
	}
	if back["/a"].ByStatusCodes["200"].Count != 3 {
		t.Errorf("/a by status codes = %+v", back["/a"].ByStatusCodes)
	}
	if back["/a"].ByTimeBuckets != nil || back["/b"].ByStatusCodes != nil {
		t.Errorf("empty maps must come back as nil: %+v %+v", back["/a"].ByTimeBuckets, back["/b"].ByStatusCodes)
	}
	if err := json.Unmarshal([]byte(`[1,2]`), &back); err == nil {
		t.Errorf("an array is not a KeyResult")
	}
}

func TestSummaryCountsJSON(t *testing.T) {
	plain := &SummaryCounts{Count: 7, ClientStreamCount: 1}
	if got := toJSON(t, plain); got != "7" {
		t.Errorf("plain counts = %s, want 7", got)
	}
	detailed := &SummaryCounts{Count: 5, ClientStreamCount: 1, ServerStreamCount: 2,
		ByStatusCodes: map[string]int{"200": 4, "500": 1}, ByTimeBuckets: map[string]int{"[0 10]": 5}}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(toJSON(t, detailed)), &m); err != nil {
		t.Fatal(err)
	}
	if m["count"] != 5.0 || m["clientStreamCount"] != 1.0 || m["serverStreamCount"] != 2.0 {
		t.Errorf("detailed counts = %v", m)
	}
	if !reflect.DeepEqual(m["byStatusCodes"], map[string]interface{}{"200": 4.0, "500": 1.0}) {
		t.Errorf("byStatusCodes = %v", m["byStatusCodes"])
	}
	if !reflect.DeepEqual(m["byTimeBuckets"], map[string]interface{}{"[0 10]": 5.0}) {
		t.Errorf("byTimeBuckets = %v", m["byTimeBuckets"])
	}
	onlyBuckets := &SummaryCounts{Count: 2, ByTimeBuckets: map[string]int{"[]": 2}}
	m = nil
	if err := json.Unmarshal([]byte(toJSON(t, onlyBuckets)), &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["byStatusCodes"]; ok {
		t.Errorf("empty status codes must be left out: %v", m)
	}
	sr := SummaryResult{"/a": plain}
	if got := toJSON(t, sr); got != `{"/a":7}` {
		t.Errorf("summary result = %s", got)
	}
}

func TestTargetResultsJSONRoundTrip(t *testing.T) {
	tr := NewTargetResults("t1", []string{"h1"}, nil, [][]int{{0, 100}})
	addAll(tr,
		res{uri: "/a", status: 200, at: at(1), took: 10 * time.Millisecond, reqSize: 4, respSize: 9, headers: map[string][]string{"h1": {"v"}}},
		res{uri: "/b", status: 500, at: at(2), took: 200 * time.Millisecond, retries: 1, retryReason: "500", errors: 1},
	)
	back := &TargetResults{}
	if err := json.Unmarshal([]byte(toJSON(t, tr)), back); err != nil {
		t.Fatal(err)
	}
	if back.Target != "t1" || back.InvocationCount != 2 || back.RetriedInvocationCounts != 1 {
		t.Errorf("round trip lost the counts: %+v", back)
	}
	if toJSON(t, back) != toJSON(t, tr) {
		t.Errorf("round trip changed the results:\n%s\n%s", toJSON(t, tr), toJSON(t, back))
	}
}

func TestGetAndLoadTargetsResultsJSON(t *testing.T) {
	drainRegistryChannels(t)
	targetsResults.lock.Lock()
	targetsResults.Results = nil
	targetsResults.lock.Unlock()
	if got := GetTargetsResultsJSON(false); got != "{}" {
		t.Fatalf("no results = %q, want {}", got)
	}
	trs, _ := targetsResults.getTargetResults("t1")
	addAll(trs, res{uri: "/a", status: 200, at: at(1)}, res{uri: "/a", status: 404, at: at(2)})
	compact := GetTargetsResultsJSON(false)
	pretty := GetTargetsResultsJSON(true)
	if strings.Contains(compact, "\n") || !strings.Contains(pretty, "\n  ") {
		t.Errorf("pretty = %v, compact = %v", pretty, compact)
	}
	var decoded map[string]*TargetResults
	if err := json.Unmarshal([]byte(compact), &decoded); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if decoded["t1"] == nil || decoded["t1"].InvocationCount != 2 || decoded[""] == nil {
		t.Fatalf("decoded = %s", compact)
	}

	ClearResults()
	if got := GetTargetsResultsJSON(false); strings.Contains(got, `"t1"`) {
		t.Fatalf("ClearResults left the results: %s", got)
	}
	if err := LoadTargetsResultsJSON(compact); err != nil {
		t.Fatalf("load: %v", err)
	}
	loaded := GetTargetsResultsJSON(false)
	var again map[string]*TargetResults
	if err := json.Unmarshal([]byte(loaded), &again); err != nil || again["t1"] == nil || again["t1"].InvocationCount != 2 {
		t.Fatalf("loaded results = %s, err %v", loaded, err)
	}
	if err := LoadTargetsResultsJSON("{not json"); err == nil {
		t.Errorf("invalid JSON must be reported")
	}
}

func TestLoadedTargetResultsKeepCounting(t *testing.T) {
	drainRegistryChannels(t)
	content := `{"":{"target":"","invocationCount":0},"t1":{"target":"t1","invocationCount":4,"countsByStatus":{"200 OK":4}}}`
	if err := LoadTargetsResultsJSON(content); err != nil {
		t.Fatal(err)
	}
	tr, all := targetsResults.getTargetResults("t1")
	if all == nil {
		t.Fatal("no results for all targets")
	}
	addAll(tr, res{status: 200})
	if tr.InvocationCount != 5 || tr.CountsByStatus["200 OK"] != 5 {
		t.Errorf("results = %+v", tr)
	}
	if tr.CountsByURIs == nil || tr.CountsByStatusCodes == nil {
		t.Errorf("the maps that were missing in the loaded content must be filled in")
	}
}

func TestInvocationResultsJSONRoundTrip(t *testing.T) {
	drainRegistryChannels(t)
	if got := GetInvocationResultsJSON(false); got != "{}" {
		t.Fatalf("no invocations = %q", got)
	}
	ir := invocationsResults.getInvocation(7)
	ir.Target = &invocation.InvocationSpec{Name: "t1"}
	ir.addResult(res{uri: "/a", status: 201}.build())
	ir.finish()
	compact := GetInvocationResultsJSON(false)
	if !strings.Contains(GetInvocationResultsJSON(true), "\n  ") {
		t.Errorf("pretty output is not indented")
	}
	var decoded map[string]*InvocationResults
	if err := json.Unmarshal([]byte(compact), &decoded); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, compact)
	}
	d := decoded["7"]
	if d == nil || d.InvocationIndex != 7 || !d.Finished || len(d.Results) != 1 || d.Results[0].Response.StatusCode != 201 || d.Target.Name != "t1" {
		t.Fatalf("decoded = %+v from %s", d, compact)
	}
	ClearResults()
	if got := GetInvocationResultsJSON(false); got != "{}" {
		t.Errorf("ClearResults left %s", got)
	}
	if err := LoadInvocationResultsJSON(compact); err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := GetInvocationResultsJSON(false); !strings.Contains(got, `"invocationIndex":7`) {
		t.Errorf("loaded results = %s", got)
	}
	if err := LoadInvocationResultsJSON("[1]"); err == nil {
		t.Errorf("invalid content must be reported")
	}
}

func TestInvocationResultsGetInvocationReusesIndex(t *testing.T) {
	drainRegistryChannels(t)
	a := invocationsResults.getInvocation(1)
	b := invocationsResults.getInvocation(1)
	c := invocationsResults.getInvocation(2)
	if a != b || a == c || c.InvocationIndex != 2 {
		t.Fatalf("invocation results are not reused per index")
	}
	a.addResult(res{}.build())
	a.addResult(res{}.build())
	if len(a.Results) != 2 || a.Finished {
		t.Errorf("results = %d finished = %v", len(a.Results), a.Finished)
	}
	a.finish()
	if !a.Finished {
		t.Errorf("finish didn't mark the invocation as finished")
	}
}

func TestResultSinkFactoryCollectsTargetResults(t *testing.T) {
	drainRegistryChannels(t)
	spec := &invocation.InvocationSpec{Name: "sink-target"}
	factory := ResultSinkFactory(spec, []string{"x-h"}, nil, [][]int{{0, 0}})
	tracker := newTracker(11)
	sink := factory(tracker)
	if sink == nil {
		t.Fatal("no sink")
	}
	sink(res{uri: "/a", status: 200, headers: map[string][]string{"X-H": {"V"}}}.build())
	sink(res{uri: "/a", status: 500}.build())
	sink(nil)
	tr := targetsResults.Results["sink-target"]
	if tr == nil || tr.InvocationCount != 2 {
		t.Fatalf("target results = %+v", tr)
	}
	// the headers of the results are lower-cased by the sink, and so match the tracked header
	if h := tr.CountsByHeaders["x-h"]; h == nil || h.Count != 1 {
		t.Errorf("tracked header counts = %+v", h)
	}
	if all := targetsResults.Results[""]; all.InvocationCount != 0 {
		t.Errorf("all-targets results are collected while disabled: %d", all.InvocationCount)
	}
	if inv := invocationsResults.Results[11]; len(inv.Results) != 0 {
		t.Errorf("invocation results are collected while disabled")
	}
	if inv := invocationsResults.Results[11]; inv.Target != spec || inv.Status != tracker.Status {
		t.Errorf("invocation results don't refer to the target and status of the tracker")
	}

	EnableAllTargetResults(true)
	EnableInvocationResults(true)
	sink(res{uri: "/b", status: 200}.build())
	if all := targetsResults.Results[""]; all.InvocationCount != 1 {
		t.Errorf("all-targets results = %d, want 1", all.InvocationCount)
	}
	if inv := invocationsResults.Results[11]; len(inv.Results) != 1 {
		t.Errorf("invocation results = %d, want 1", len(inv.Results))
	}
	if tr.InvocationCount != 3 {
		t.Errorf("target results = %d, want 3", tr.InvocationCount)
	}
}

func TestResultChannelSinkFactoryDrainsChannelsOnDone(t *testing.T) {
	drainRegistryChannels(t)
	spec := &invocation.InvocationSpec{Name: "channel-target"}
	factory := ResultChannelSinkFactory(spec, nil, nil, nil)
	tracker := newTracker(21)
	if sink := factory(tracker); sink != nil {
		t.Fatalf("channel sinks work from the channels, but got a sink func")
	}
	for i := 0; i < 20; i++ {
		tracker.Channels.ResultChannel <- res{uri: "/a", status: 200}.build()
	}
	tracker.Channels.DoneChannel <- true
	inv := invocationsResults.getInvocation(21)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		inv.lock.RLock()
		finished := inv.Finished
		inv.lock.RUnlock()
		if finished {
			break
		}
		time.Sleep(time.Millisecond)
	}
	tr := targetsResults.Results["channel-target"]
	tr.lock.RLock()
	count := tr.InvocationCount
	tr.lock.RUnlock()
	if count != 20 {
		t.Errorf("channel sink counted %d results, want 20", count)
	}
	inv.lock.RLock()
	finished := inv.Finished
	inv.lock.RUnlock()
	if !finished {
		t.Errorf("the invocation was not finished")
	}
}

func TestProcessTrackingConfigSharesTrackingWithAllResults(t *testing.T) {
	tr := NewTargetResults("t1", nil, nil, nil)
	all := NewTargetResults("", nil, nil, nil)
	processTrackingConfig(tr, all, []string{"a", "b"}, map[string][]string{"a": {"b"}}, [][]int{{1, 2}})
	if !reflect.DeepEqual(tr.trackingHeaders, []string{"a", "b"}) || !reflect.DeepEqual(all.trackingHeaders, tr.trackingHeaders) {
		t.Errorf("tracking headers = %v / %v", tr.trackingHeaders, all.trackingHeaders)
	}
	if all.crossHeadersMap["b"] != "a" || all.crossTrackingHeaders["a"][0] != "b" {
		t.Errorf("cross headers were not passed on: %v", all.crossHeadersMap)
	}
	if !reflect.DeepEqual(tr.trackingTimeBuckets, [][]int{{1, 2}}) {
		t.Errorf("time buckets = %v", tr.trackingTimeBuckets)
	}
	processTrackingConfig(tr, nil, []string{"c"}, nil, nil)
	if !reflect.DeepEqual(tr.trackingHeaders, []string{"c"}) {
		t.Errorf("tracking headers without all-results = %v", tr.trackingHeaders)
	}
}

// ---- merging of results (AddDeltaResults) and the aggregate views ----

func mergeSample() []res {
	ms := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }
	return []res{
		{uri: "/a", status: 200, at: at(1), took: ms(10), reqSize: 4, respSize: 8, cs: 1, ss: 2, headers: map[string][]string{"h1": {"x"}, "h2": {"p"}}},
		{uri: "/a", status: 200, at: at(2), took: ms(150), reqSize: 4, respSize: 9, headers: map[string][]string{"h1": {"y"}, "h2": {"q"}}},
		{uri: "/b", status: 503, at: at(3), took: ms(700), retries: 2, retryReason: "503", errors: 1, headers: map[string][]string{"h1": {"x"}}},
		{uri: "/b", status: 200, at: at(4), took: ms(20), retries: 1, retryReason: "reset", respSize: 8, headers: map[string][]string{"h1": {"x"}, "h2": {"p"}}},
		{uri: "/c", status: 200, at: at(5), took: ms(120), headers: map[string][]string{"h2": {"p"}}},
		{uri: "/a", status: 404, at: at(6), took: ms(30), errors: 2},
	}
}

func newMergeTarget(name string) *TargetResults {
	return NewTargetResults(name, []string{"h1", "h2"}, map[string][]string{"h1": {"h2"}}, [][]int{{0, 100}, {101, 500}, {501, 0}})
}

func roundTrip(t *testing.T, tr *TargetResults) *TargetResults {
	t.Helper()
	back := &TargetResults{}
	if err := json.Unmarshal([]byte(toJSON(t, tr)), back); err != nil {
		t.Fatal(err)
	}
	return back
}

func TestAddDeltaResultsMergesResultsOfPeers(t *testing.T) {
	sample := mergeSample()
	whole := newMergeTarget("t1")
	addAll(whole, sample...)
	first := newMergeTarget("t1")
	addAll(first, sample[:3]...)
	second := newMergeTarget("t1")
	addAll(second, sample[3:]...)

	for _, detailed := range []bool{true, false} {
		merged := newMergeTarget("t1")
		// the registry reads the results of the peers from their JSON
		AddDeltaResults(merged, roundTrip(t, first), detailed)
		AddDeltaResults(merged, roundTrip(t, second), detailed)
		if merged.InvocationCount != 6 || merged.ClientStreamCount != 1 || merged.ServerStreamCount != 2 {
			t.Errorf("detailed=%v: counts = %d/%d/%d", detailed, merged.InvocationCount, merged.ClientStreamCount, merged.ServerStreamCount)
		}
		if !merged.FirstResultAt.Equal(at(1)) || !merged.LastResultAt.Equal(at(6)) {
			t.Errorf("detailed=%v: first/last = %v/%v", detailed, merged.FirstResultAt, merged.LastResultAt)
		}
		if !reflect.DeepEqual(merged.CountsByStatus, whole.CountsByStatus) {
			t.Errorf("detailed=%v: CountsByStatus = %v, want %v", detailed, merged.CountsByStatus, whole.CountsByStatus)
		}
		for name, pair := range map[string][2]KeyResult{
			"status codes": {merged.CountsByStatusCodes, whole.CountsByStatusCodes},
			"URIs":         {merged.CountsByURIs, whole.CountsByURIs},
			"request size": {merged.CountsByRequestPayloadSizes, whole.CountsByRequestPayloadSizes},
			"response":     {merged.CountsByResponsePayloadSizes, whole.CountsByResponsePayloadSizes},
			"retries":      {merged.CountsByRetries, whole.CountsByRetries},
			"retry reason": {merged.CountsByRetryReasons, whole.CountsByRetryReasons},
			"errors":       {merged.CountsByErrors, whole.CountsByErrors},
			"time buckets": {merged.CountsByTimeBuckets, whole.CountsByTimeBuckets},
		} {
			for key, w := range pair[1] {
				m := pair[0][key]
				if m == nil {
					t.Errorf("detailed=%v: %s: missing key %s", detailed, name, key)
					continue
				}
				if m.Count != w.Count || m.Retries != w.Retries || m.ClientStreamCount != w.ClientStreamCount || m.ServerStreamCount != w.ServerStreamCount {
					t.Errorf("detailed=%v: %s[%s] = %+v, want %+v", detailed, name, key, m.CountInfo, w.CountInfo)
				}
				if detailed && name != "status codes" && name != "time buckets" && len(w.ByStatusCodes) != len(m.ByStatusCodes) {
					t.Errorf("detailed=%v: %s[%s] status codes = %v, want %v", detailed, name, key, toJSON(t, m.ByStatusCodes), toJSON(t, w.ByStatusCodes))
				}
				if detailed && name != "time buckets" && len(w.ByTimeBuckets) != len(m.ByTimeBuckets) {
					t.Errorf("detailed=%v: %s[%s] time buckets = %v, want %v", detailed, name, key, toJSON(t, m.ByTimeBuckets), toJSON(t, w.ByTimeBuckets))
				}
			}
			if len(pair[0]) != len(pair[1]) {
				t.Errorf("detailed=%v: %s has %d keys, want %d", detailed, name, len(pair[0]), len(pair[1]))
			}
		}
		for header, w := range whole.CountsByHeaders {
			m := merged.CountsByHeaders[header]
			if m == nil || m.Count != w.Count {
				t.Errorf("detailed=%v: header %s = %+v, want count %d", detailed, header, m, w.Count)
				continue
			}
			for v, wc := range w.CountsByValues {
				if m.CountsByValues[v] == nil || m.CountsByValues[v].Count != wc.Count {
					t.Errorf("detailed=%v: header %s value %s = %+v, want %d", detailed, header, v, m.CountsByValues[v], wc.Count)
				}
			}
			if detailed {
				for sc, wc := range w.CountsByStatusCodes {
					if m.CountsByStatusCodes[sc] == nil || m.CountsByStatusCodes[sc].Count != wc.Count {
						t.Errorf("detailed: header %s status %s = %+v, want %d", header, sc, m.CountsByStatusCodes[sc], wc.Count)
					}
				}
			}
		}
	}
}

func TestAddDeltaResultsDetailedMergesCrossHeaders(t *testing.T) {
	sample := mergeSample()
	whole := newMergeTarget("t1")
	addAll(whole, sample...)
	first := newMergeTarget("t1")
	addAll(first, sample[:3]...)
	second := newMergeTarget("t1")
	addAll(second, sample[3:]...)
	merged := newMergeTarget("t1")
	AddDeltaResults(merged, roundTrip(t, first), true)
	AddDeltaResults(merged, roundTrip(t, second), true)

	w := whole.CountsByHeaders["h1"]
	m := merged.CountsByHeaders["h1"]
	if m.CrossHeaders["h2"] == nil || m.CrossHeaders["h2"].Count != w.CrossHeaders["h2"].Count {
		t.Fatalf("h1 x h2 = %+v, want count %d", m.CrossHeaders["h2"], w.CrossHeaders["h2"].Count)
	}
	for v, wc := range w.CrossHeaders["h2"].CountsByValues {
		if m.CrossHeaders["h2"].CountsByValues[v].Count != wc.Count {
			t.Errorf("h1 x h2 value %s = %d, want %d", v, m.CrossHeaders["h2"].CountsByValues[v].Count, wc.Count)
		}
	}
	for value, byHeader := range w.CrossHeadersByValues {
		for ch, wc := range byHeader {
			mc := m.CrossHeadersByValues[value][ch]
			if mc == nil || mc.Count != wc.Count {
				t.Errorf("h1=%s x %s = %+v, want count %d", value, ch, mc, wc.Count)
			}
		}
	}

	// without details the cross headers are not merged
	plain := newMergeTarget("t1")
	AddDeltaResults(plain, roundTrip(t, whole), false)
	if h := plain.CountsByHeaders["h1"]; h.Count != 4 || len(h.CrossHeaders) != 0 || len(h.CountsByStatusCodes) != 0 {
		t.Errorf("non-detailed header counts = %+v", h)
	}
}

func TestAddDeltaResultsIntoEmptyResultsKeepsDeltaTimestamps(t *testing.T) {
	delta := newMergeTarget("t1")
	addAll(delta, res{at: at(5)}, res{at: at(9)})
	merged := NewTargetResults("t1", nil, nil, nil)
	AddDeltaResults(merged, delta, true)
	if !merged.FirstResultAt.Equal(at(5)) || !merged.LastResultAt.Equal(at(9)) {
		t.Errorf("first/last = %v/%v", merged.FirstResultAt, merged.LastResultAt)
	}
	// a delta that came from other peers earlier or later extends the interval
	earlier := newMergeTarget("t1")
	addAll(earlier, res{at: at(1)})
	later := newMergeTarget("t1")
	addAll(later, res{at: at(20)})
	AddDeltaResults(merged, later, false)
	AddDeltaResults(merged, earlier, false)
	if !merged.FirstResultAt.Equal(at(1)) || !merged.LastResultAt.Equal(at(20)) {
		t.Errorf("first/last = %v/%v", merged.FirstResultAt, merged.LastResultAt)
	}
	if merged.InvocationCount != 4 {
		t.Errorf("InvocationCount = %d, want 4", merged.InvocationCount)
	}
}

func TestClientTargetsAggregateResultsView(t *testing.T) {
	t1 := newMergeTarget("t1")
	addAll(t1, mergeSample()...)
	t2 := newMergeTarget("t2")
	addAll(t2, res{uri: "/a", status: 200, at: at(10), took: 5 * time.Millisecond, headers: map[string][]string{"h1": {"x"}}},
		res{uri: "/z", status: 500, at: at(11), took: 600 * time.Millisecond})

	for _, detailed := range []bool{false, true} {
		view := NewClientTargetsAggregateResults()
		view.AddTargetResult(t1, detailed)
		view.AddTargetResult(t2, detailed)

		if view.CountsByStatusCodes["200"].Count != 5 || view.CountsByStatusCodes["503"].Count != 1 ||
			view.CountsByStatusCodes["500"].Count != 1 || view.CountsByStatusCodes["404"].Count != 1 {
			t.Errorf("detailed=%v: status codes = %s", detailed, toJSON(t, view.CountsByStatusCodes))
		}
		if view.CountsByURIs["/a"].Count != 4 || view.CountsByURIs["/z"].Count != 1 || view.CountsByURIs["/b"].Count != 2 {
			t.Errorf("detailed=%v: URIs = %s", detailed, toJSON(t, view.CountsByURIs))
		}
		if view.CountsByHeaders["h1"] != 5 || view.CountsByHeaders["h2"] != 4 {
			t.Errorf("detailed=%v: headers = %v", detailed, view.CountsByHeaders)
		}
		if view.CountsByHeaderValues["h1"]["x"] != 4 || view.CountsByHeaderValues["h1"]["y"] != 1 {
			t.Errorf("detailed=%v: header values = %v", detailed, view.CountsByHeaderValues)
		}
		if view.CountsByRetries["2"].Count != 1 || view.CountsByRetries["1"].Count != 1 {
			t.Errorf("detailed=%v: retries = %s", detailed, toJSON(t, view.CountsByRetries))
		}
		if view.CountsByRetryReasons["503"].Count != 1 || view.CountsByRetryReasons["reset"].Count != 1 {
			t.Errorf("detailed=%v: retry reasons = %s", detailed, toJSON(t, view.CountsByRetryReasons))
		}
		if view.CountsByErrors["0"].Count != 2 || view.CountsByErrors["1"].Count != 1 {
			t.Errorf("detailed=%v: errors = %s", detailed, toJSON(t, view.CountsByErrors))
		}
		if view.CountsByRequestPayloadSizes["4"].Count != 2 || view.CountsByResponsePayloadSizes["8"].Count != 2 {
			t.Errorf("detailed=%v: payload sizes = %s / %s", detailed, toJSON(t, view.CountsByRequestPayloadSizes), toJSON(t, view.CountsByResponsePayloadSizes))
		}
		if view.CountsByTimeBuckets["[0 100]"].Count != 4 || view.CountsByTimeBuckets["[501 0]"].Count != 2 {
			t.Errorf("detailed=%v: time buckets = %s", detailed, toJSON(t, view.CountsByTimeBuckets))
		}
		if detailed {
			if view.CountsByURIs["/a"].ByStatusCodes["200"] != 3 || view.CountsByURIs["/a"].ByStatusCodes["404"] != 1 {
				t.Errorf("URIs by status codes = %s", toJSON(t, view.CountsByURIs["/a"]))
			}
			if view.CountsByStatusCodes["200"].ByTimeBuckets["[0 100]"] != 3 {
				t.Errorf("status code 200 by time buckets = %s", toJSON(t, view.CountsByStatusCodes["200"]))
			}
		} else if view.CountsByURIs["/a"].ByStatusCodes != nil || view.CountsByStatusCodes["200"].ByTimeBuckets != nil {
			t.Errorf("non-detailed view has details: %s", toJSON(t, view.CountsByURIs["/a"]))
		}

		by1 := view.ResultsByTargets["t1"]
		by2 := view.ResultsByTargets["t2"]
		if by1 == nil || by2 == nil || by1.InvocationCount != 6 || by2.InvocationCount != 2 {
			t.Fatalf("detailed=%v: by targets = %+v / %+v", detailed, by1, by2)
		}
		if !by1.FirstResultAt.Equal(at(1)) || !by1.LastResultAt.Equal(at(6)) || !by2.FirstResultAt.Equal(at(10)) || !by2.LastResultAt.Equal(at(11)) {
			t.Errorf("detailed=%v: target intervals %v-%v / %v-%v", detailed, by1.FirstResultAt, by1.LastResultAt, by2.FirstResultAt, by2.LastResultAt)
		}
		if by2.CountsByStatusCodes["200"].Count != 1 || by2.CountsByStatusCodes["500"].Count != 1 || by2.CountsByHeaders["h1"] != 1 {
			t.Errorf("detailed=%v: t2 view = %s", detailed, toJSON(t, by2))
		}
	}
}

func TestClientAggregateResultsViewAccumulatesTargetsOfOneName(t *testing.T) {
	a := NewTargetResults("t1", nil, nil, nil)
	addAll(a, res{at: at(3)}, res{at: at(4)})
	b := NewTargetResults("t1", nil, nil, nil)
	addAll(b, res{at: at(1)})
	view := NewClientTargetsAggregateResults()
	view.AddTargetResult(a, false)
	view.AddTargetResult(b, false)
	t1 := view.ResultsByTargets["t1"]
	if t1.InvocationCount != 3 || !t1.FirstResultAt.Equal(at(1)) || !t1.LastResultAt.Equal(at(4)) {
		t.Errorf("t1 = %+v", t1)
	}
	if view.CountsByStatusCodes["200"].Count != 3 {
		t.Errorf("status codes = %s", toJSON(t, view.CountsByStatusCodes))
	}
	if got := toJSON(t, view.CountsByStatusCodes); got != `{"200":3}` {
		t.Errorf("non-detailed status codes JSON = %s", got)
	}
}

func TestIncrementKeyResultCountsIgnoresNonMaps(t *testing.T) {
	result := SummaryResult{}
	incrementKeyResultCounts(result, "not a map", true)
	incrementKeyResultCounts(result, 5, true)
	if len(result) != 0 {
		t.Errorf("non-map input counted: %v", result)
	}
}

// ---- concurrency ----

func TestAddResultConcurrentExactTotals(t *testing.T) {
	tr := NewTargetResults("t1", []string{"h1"}, nil, [][]int{{0, 100}})
	const workers, per = 16, 100
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				status := 200
				if i%4 == 0 {
					status = 500
				}
				tr.addResult(res{uri: fmt.Sprintf("/u%d", w%4), status: status, at: at(i), took: time.Millisecond,
					headers: map[string][]string{"h1": {"v"}}}.build())
			}
		}(w)
	}
	wg.Wait()
	if tr.InvocationCount != workers*per {
		t.Errorf("InvocationCount = %d, want %d", tr.InvocationCount, workers*per)
	}
	if tr.CountsByStatusCodes["500"].Count != workers*per/4 || tr.CountsByStatusCodes["200"].Count != workers*per*3/4 {
		t.Errorf("status codes = %s", toJSON(t, tr.CountsByStatusCodes))
	}
	sum := 0
	for _, u := range tr.CountsByURIs {
		sum += u.Count
	}
	if sum != workers*per || len(tr.CountsByURIs) != 4 {
		t.Errorf("URIs = %d keys, %d total", len(tr.CountsByURIs), sum)
	}
	if tr.CountsByHeaders["h1"].Count != workers*per || tr.CountsByTimeBuckets["[0 100]"].Count != workers*per {
		t.Errorf("header/time bucket counts = %d/%d", tr.CountsByHeaders["h1"].Count, tr.CountsByTimeBuckets["[0 100]"].Count)
	}
}

func TestSinksConcurrentWithReadersAndToggles(t *testing.T) {
	drainRegistryChannels(t)
	const targets, workers, per = 3, 4, 50
	var wg sync.WaitGroup
	stop := make(chan struct{})
	var readers sync.WaitGroup
	readers.Add(2)
	go func() {
		defer readers.Done()
		for {
			select {
			case <-stop:
				return
			default:
				GetTargetsResultsJSON(false)
				GetInvocationResultsJSON(false)
			}
		}
	}()
	go func() {
		defer readers.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
				EnableAllTargetResults(i%2 == 0)
				EnableInvocationResults(i%2 == 1)
			}
		}
	}()
	for tg := 0; tg < targets; tg++ {
		spec := &invocation.InvocationSpec{Name: fmt.Sprintf("conc-%d", tg)}
		factory := ResultSinkFactory(spec, []string{"h"}, nil, [][]int{{0, 0}})
		for w := 0; w < workers; w++ {
			sink := factory(newTracker(uint32(tg*100 + w)))
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < per; i++ {
					sink(res{uri: "/a", status: 200, at: at(i), headers: map[string][]string{"H": {"v"}}}.build())
				}
			}()
		}
	}
	wg.Wait()
	close(stop)
	readers.Wait()
	for tg := 0; tg < targets; tg++ {
		tr := targetsResults.Results[fmt.Sprintf("conc-%d", tg)]
		if tr == nil || tr.InvocationCount != workers*per {
			t.Errorf("target conc-%d count = %+v, want %d", tg, tr, workers*per)
		}
	}
}

// ---- registry sender ----

type fakeRegistry struct {
	srv   *httptest.Server
	lock  sync.Mutex
	posts []fakePost
}

type fakePost struct {
	path string
	body string
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	t.Helper()
	fr := &fakeRegistry{}
	fr.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		fr.lock.Lock()
		fr.posts = append(fr.posts, fakePost{path: r.URL.Path, body: string(b)})
		fr.lock.Unlock()
	}))
	// the client of the results talks HTTP/2 without TLS, as the servers of goto do
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	fr.srv.Config.Protocols = protocols
	fr.srv.Start()
	t.Cleanup(fr.srv.Close)
	return fr
}

func (fr *fakeRegistry) find(pathSuffix string, match func(body string) bool) (fakePost, bool) {
	fr.lock.Lock()
	defer fr.lock.Unlock()
	for i := len(fr.posts) - 1; i >= 0; i-- {
		if strings.HasSuffix(fr.posts[i].path, pathSuffix) && match(fr.posts[i].body) {
			return fr.posts[i], true
		}
	}
	return fakePost{}, false
}

func (fr *fakeRegistry) paths() []string {
	fr.lock.Lock()
	defer fr.lock.Unlock()
	var paths []string
	for _, p := range fr.posts {
		paths = append(paths, p.path)
	}
	return paths
}

func useRegistry(t *testing.T, url string) {
	t.Helper()
	prevSelf, prevLocker := global.Self, global.Flags.UseLocker
	global.Self.RegistryURL = url
	global.Self.Name = "peer1"
	global.Self.Address = "10.0.0.1:8080"
	global.Flags.UseLocker = true
	t.Cleanup(func() {
		global.Self = prevSelf
		global.Flags.UseLocker = prevLocker
	})
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func waitForSendersToStop(t *testing.T) {
	t.Helper()
	stopped := make(chan struct{})
	go func() {
		registrySenders.Wait()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Errorf("the registry senders didn't stop")
	}
}

// withRegistrySenders runs the real registry senders against a fake registry, with short pauses between their rounds.
func withRegistrySenders(t *testing.T) {
	t.Helper()
	prevInterval := registrySendInterval
	registrySendInterval = 2 * time.Millisecond
	registrySendLock.Lock()
	prevSending := sendingToRegistry
	sendingToRegistry = false
	registrySendLock.Unlock()
	ClearResults()
	startRegistrySender()
	t.Cleanup(func() {
		StopRegistrySender()
		waitForSendersToStop(t)
		EnableAllTargetResults(false)
		EnableInvocationResults(false)
		ClearResults()
		registrySendLock.Lock()
		sendingToRegistry = prevSending
		registrySendLock.Unlock()
		registrySendInterval = prevInterval
	})
}

func TestRegistrySendersStoreTargetAndInvocationResultsInRegistry(t *testing.T) {
	fr := newFakeRegistry(t)
	useRegistry(t, fr.srv.URL)
	withRegistrySenders(t)
	EnableInvocationResults(true)

	spec := &invocation.InvocationSpec{Name: "sent-target"}
	sink := ResultSinkFactory(spec, nil, nil, nil)(newTracker(31))
	const n = 25
	for i := 0; i < n; i++ {
		sink(res{uri: "/a", status: 200, at: at(i)}.build())
	}
	targetPath := "/registry/peers/peer1/10.0.0.1:8080/locker/store/client,sent-target"
	waitFor(t, "the final target results to be stored in the registry", func() bool {
		_, ok := fr.find(targetPath, func(body string) bool {
			tr := &TargetResults{}
			return json.Unmarshal([]byte(body), tr) == nil && tr.InvocationCount == n
		})
		return ok
	})
	invPath := "/registry/peers/peer1/10.0.0.1:8080/locker/store/client,invocations,31"
	waitFor(t, "the invocation results to be stored in the registry", func() bool {
		_, ok := fr.find(invPath, func(body string) bool {
			ir := &InvocationResults{}
			return json.Unmarshal([]byte(body), ir) == nil && len(ir.Results) == n && ir.InvocationIndex == 31
		})
		return ok
	})
}

func TestRegistrySendersDoNothingWithoutLocker(t *testing.T) {
	fr := newFakeRegistry(t)
	useRegistry(t, fr.srv.URL)
	global.Flags.UseLocker = false
	withRegistrySenders(t)
	sink := ResultSinkFactory(&invocation.InvocationSpec{Name: "quiet"}, nil, nil, nil)(newTracker(32))
	for i := 0; i < 5; i++ {
		sink(res{}.build())
	}
	// the senders go through their rounds without sending anything
	waitFor(t, "the senders to drain the queue", func() bool { return len(chanSendTargetsToRegistry) == 0 })
	time.Sleep(50 * time.Millisecond)
	if p := fr.paths(); len(p) != 0 {
		t.Errorf("results were sent while the locker is disabled: %v", p)
	}
}

func TestSendResultToRegistryAndLockInvocation(t *testing.T) {
	fr := newFakeRegistry(t)
	useRegistry(t, fr.srv.URL)
	sendResultToRegistry([]string{"client", "a", "b"}, map[string]int{"x": 1})
	lockInvocationRegistryLocker(5)
	if _, ok := fr.find("/locker/store/client,a,b", func(b string) bool { return b == `{"x":1}` }); !ok {
		t.Errorf("result was not stored: %v", fr.paths())
	}
	if _, ok := fr.find("/locker/lock/client_5", func(string) bool { return true }); !ok {
		t.Errorf("invocation locker was not locked: %v", fr.paths())
	}
	global.Flags.UseLocker = false
	before := len(fr.paths())
	sendResultToRegistry([]string{"client"}, 1)
	lockInvocationRegistryLocker(6)
	if len(fr.paths()) != before {
		t.Errorf("registry was called while the locker is disabled")
	}
	global.Flags.UseLocker = true
	global.Self.RegistryURL = ""
	sendResultToRegistry([]string{"client"}, 1)
	lockInvocationRegistryLocker(7)
	if len(fr.paths()) != before {
		t.Errorf("registry was called without a registry URL")
	}
}
