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

package tracking

import (
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"goto/pkg/server/hooks"
)

var (
	// Tracking data is kept per key and port in a global, so every test tracks under a key of its own.
	keySeq atomic.Int32
	// Ports of the data-level tests. They are not ports of the hooks, which are made (with goroutines) per port.
	dataPortSeq atomic.Int32
)

func newData(t *testing.T) (port int, key string, td *TrackingData) {
	t.Helper()
	port = int(50000 + dataPortSeq.Add(1))
	key = fmt.Sprintf("%s-%d", t.Name(), keySeq.Add(1))
	td = Tracker.getKeyPort(port, key)
	t.Cleanup(func() {
		Tracker.lock.Lock()
		delete(Tracker.KeyPort, key)
		Tracker.lock.Unlock()
	})
	return port, key, td
}

func hdrs(kv ...string) map[string][]string {
	m := map[string][]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = append(m[kv[i]], kv[i+1])
	}
	return m
}

func TestTrackerIsWiredAsTheHeaderTrackingFunc(t *testing.T) {
	if reflect.ValueOf(hooks.HeaderTrackingFunc).Pointer() != reflect.ValueOf(TrackRequest).Pointer() {
		t.Error("hooks.HeaderTrackingFunc must be wired to TrackRequest by init")
	}
}

func TestGetKeyPortIsSharedPerKeyAndPort(t *testing.T) {
	port, key, td := newData(t)
	if again := Tracker.getKeyPort(port, key); again != td {
		t.Error("the same key and port must give the same tracking data")
	}
	if other := Tracker.getKeyPort(port+1, key); other == td {
		t.Error("another port must have tracking data of its own")
	}
	if other := Tracker.getKeyPort(port, key+"-other"); other == td {
		t.Error("another key must have tracking data of its own")
	}
	Tracker.lock.Lock()
	delete(Tracker.KeyPort, key+"-other")
	Tracker.lock.Unlock()
	if td.ByURI == nil || td.ByReqHeader == nil || td.ByRespStatus == nil || td.ByUpRespHeaderAndStatus == nil {
		t.Error("new tracking data must have all its maps made")
	}
}

func TestAddRequestTrackingRegistersURIAndHeaders(t *testing.T) {
	port, key, td := newData(t)
	AddRequestTracking(port, key, "/foo", []string{"x-a", "x-b"})
	if v, ok := td.ByURI["/foo"]; !ok || v != 0 {
		t.Errorf("ByURI = %v, want /foo tracked with 0", td.ByURI)
	}
	for _, h := range []string{"x-a", "x-b"} {
		if td.ByReqHeader[h] == nil {
			t.Errorf("header %s is not tracked", h)
		}
		if td.ByURIAndReqHeader["/foo"][h] == nil {
			t.Errorf("header %s is not tracked for /foo", h)
		}
	}
	// Headers alone are tracked for all URIs, and no URI is registered.
	AddRequestTracking(port, key, "", []string{"x-c"})
	if td.ByReqHeader["x-c"] == nil {
		t.Error("header x-c is not tracked")
	}
	if len(td.ByURI) != 1 || len(td.ByURIAndReqHeader) != 1 {
		t.Errorf("ByURI = %v, ByURIAndReqHeader = %v, want the URI /foo only", td.ByURI, td.ByURIAndReqHeader)
	}
	// A URI alone is tracked without headers.
	AddRequestTracking(port, key, "/bar", nil)
	if _, ok := td.ByURI["/bar"]; !ok {
		t.Error("URI /bar is not tracked")
	}
	if td.ByURIAndReqHeader["/bar"] != nil {
		t.Error("a URI tracked without headers must not have header tracking")
	}
	// Request tracking leaves the other kinds alone.
	if len(td.ByUpURI)+len(td.ByUpReqHeader)+len(td.ByRespHeader)+len(td.ByURIAndRespStatus) != 0 {
		t.Error("request tracking must not register upstream or response tracking")
	}
}

func TestAddUpstreamRequestTrackingRegistersURIAndHeaders(t *testing.T) {
	port, key, td := newData(t)
	AddUpstreamRequestTracking(port, key, "/up", []string{"x-a"})
	if v, ok := td.ByUpURI["/up"]; !ok || v != 0 {
		t.Errorf("ByUpURI = %v, want /up tracked with 0", td.ByUpURI)
	}
	if td.ByUpReqHeader["x-a"] == nil || td.ByUpURIAndReqHeader["/up"]["x-a"] == nil {
		t.Error("header x-a is not tracked for upstream requests")
	}
	AddUpstreamRequestTracking(port, key, "", []string{"x-b"})
	if td.ByUpReqHeader["x-b"] == nil || len(td.ByUpURI) != 1 {
		t.Errorf("ByUpReqHeader = %v, ByUpURI = %v, want x-b tracked and no more URIs", td.ByUpReqHeader, td.ByUpURI)
	}
	if len(td.ByURI)+len(td.ByReqHeader) != 0 {
		t.Error("upstream tracking must not register request tracking")
	}
}

func TestAddResponseTrackingRegistersURIAndHeaders(t *testing.T) {
	port, key, td := newData(t)
	AddResponseTracking(port, key, "/foo", []string{"x-r"})
	if td.ByURIAndRespStatus["/foo"] == nil {
		t.Error("status counts of /foo are not tracked")
	}
	if td.ByRespHeader["x-r"] == nil || td.ByURIAndRespHeader["/foo"]["x-r"] == nil {
		t.Error("header x-r is not tracked for responses")
	}
	if td.ByRespHeaderAndStatus["x-r"] == nil {
		t.Error("header x-r is not tracked by status")
	}
	AddResponseTracking(port, key, "/bar", nil)
	if td.ByURIAndRespStatus["/bar"] == nil || td.ByURIAndRespHeader["/bar"] != nil {
		t.Error("a URI tracked without headers has status counts only")
	}
}

func TestAddUpstreamResponseTrackingRegistersURIAndHeaders(t *testing.T) {
	port, key, td := newData(t)
	AddUpstreamResponseTracking(port, key, "/foo", []string{"x-r"})
	if td.ByUpURIAndRespStatus["/foo"] == nil {
		t.Error("upstream status counts of /foo are not tracked")
	}
	if td.ByUpRespHeader["x-r"] == nil || td.ByUpURIAndRespHeader["/foo"]["x-r"] == nil || td.ByUpRespHeaderAndStatus["x-r"] == nil {
		t.Error("header x-r is not tracked for upstream responses")
	}
	AddUpstreamResponseTracking(port, key, "/bar", nil)
	if td.ByUpURIAndRespStatus["/bar"] == nil || td.ByUpURIAndRespHeader["/bar"] != nil {
		t.Error("a URI tracked without headers has status counts only")
	}
}

func TestTrackRequestCountsTrackedURIAndHeaders(t *testing.T) {
	port, key, td := newData(t)
	AddRequestTracking(port, key, "/foo", []string{"x-a"})
	for i := 0; i < 3; i++ {
		TrackRequest(port, key, "/foo", hdrs("x-a", "one"))
	}
	TrackRequest(port, key, "/foo", hdrs("x-a", "two"))
	TrackRequest(port, key, "/foo", hdrs("x-other", "ignored"))
	if got := td.ByURI["/foo"]; got != 5 {
		t.Errorf("ByURI[/foo] = %d, want 5", got)
	}
	for name, hvc := range map[string]*HeaderValueCounts{"ByReqHeader": td.ByReqHeader["x-a"], "ByURIAndReqHeader": td.ByURIAndReqHeader["/foo"]["x-a"]} {
		if hvc.CountsByHeader != 4 || !reflect.DeepEqual(hvc.CountsByHeaderValue, map[string]int{"one": 3, "two": 1}) {
			t.Errorf("%s[x-a] = %+v, want 4 requests with one:3 two:1", name, hvc.CountsByHeaderValue)
		}
	}
	if _, ok := td.ByReqHeader["x-other"]; ok {
		t.Error("a header that is not tracked must not be counted")
	}
}

func TestTrackRequestCountsHeaderOnlyTrackingForAllURIs(t *testing.T) {
	port, key, td := newData(t)
	AddRequestTracking(port, key, "", []string{"x-a"})
	TrackRequest(port, key, "/one", hdrs("x-a", "v"))
	TrackRequest(port, key, "/two", hdrs("x-a", "v"))
	if got := td.ByReqHeader["x-a"]; got.CountsByHeader != 2 || got.CountsByHeaderValue["v"] != 2 {
		t.Errorf("ByReqHeader[x-a] = %+v, want 2 requests of value v", got)
	}
	if td.ByURIAndReqHeader["/one"] != nil {
		t.Error("header-only tracking must not create URI-and-header tracking")
	}
}

func TestTrackRequestEmptyValueCountsTheHeaderOnly(t *testing.T) {
	port, key, td := newData(t)
	AddRequestTracking(port, key, "", []string{"x-a"})
	TrackRequest(port, key, "/one", hdrs("x-a", ""))
	got := td.ByReqHeader["x-a"]
	if got.CountsByHeader != 1 || len(got.CountsByHeaderValue) != 0 {
		t.Errorf("ByReqHeader[x-a] = %+v, want the header counted once without values", got)
	}
}

func TestTrackUpstreamRequestCountsTrackedURIAndHeaders(t *testing.T) {
	port, key, td := newData(t)
	AddUpstreamRequestTracking(port, key, "/up", []string{"x-a"})
	for i := 0; i < 2; i++ {
		TrackUpstreamRequest(port, key, "/up", hdrs("x-a", "v"))
	}
	if got := td.ByUpURI["/up"]; got != 2 {
		t.Errorf("ByUpURI[/up] = %d, want 2", got)
	}
	for name, hvc := range map[string]*HeaderValueCounts{"ByUpReqHeader": td.ByUpReqHeader["x-a"], "ByUpURIAndReqHeader": td.ByUpURIAndReqHeader["/up"]["x-a"]} {
		if hvc.CountsByHeader != 2 || hvc.CountsByHeaderValue["v"] != 2 {
			t.Errorf("%s[x-a] = %+v, want 2 requests of value v", name, hvc)
		}
	}
	if len(td.ByURI) != 0 {
		t.Error("upstream requests must not be counted as requests")
	}
}

func TestTrackResponseCountsStatusesAndHeaders(t *testing.T) {
	port, key, td := newData(t)
	AddResponseTracking(port, key, "/foo", []string{"x-r"})
	TrackResponse(port, key, "/foo", 200, hdrs("x-r", "a"))
	TrackResponse(port, key, "/foo", 200, hdrs("x-r", "a"))
	TrackResponse(port, key, "/foo", 503, hdrs("x-r", "b"))
	TrackResponse(port, key, "/other", 404, hdrs("x-r", "a"))
	if want := (StatusCounts{200: 2, 503: 1, 404: 1}); !reflect.DeepEqual(td.ByRespStatus, want) {
		t.Errorf("ByRespStatus = %v, want %v", td.ByRespStatus, want)
	}
	if want := (StatusCounts{200: 2, 503: 1}); !reflect.DeepEqual(td.ByURIAndRespStatus["/foo"], want) {
		t.Errorf("ByURIAndRespStatus[/foo] = %v, want %v", td.ByURIAndRespStatus["/foo"], want)
	}
	if _, ok := td.ByURIAndRespStatus["/other"]; ok {
		t.Error("the statuses of a URI that is not tracked must not be counted per URI")
	}
	if got := td.ByRespHeader["x-r"]; got.CountsByHeader != 4 || !reflect.DeepEqual(got.CountsByHeaderValue, map[string]int{"a": 3, "b": 1}) {
		t.Errorf("ByRespHeader[x-r] = %+v, want 4 responses with a:3 b:1", got)
	}
	if got := td.ByURIAndRespHeader["/foo"]["x-r"]; got.CountsByHeader != 3 || !reflect.DeepEqual(got.CountsByHeaderValue, map[string]int{"a": 2, "b": 1}) {
		t.Errorf("ByURIAndRespHeader[/foo][x-r] = %+v, want 3 responses with a:2 b:1", got)
	}
	wantByStatus := HeaderValueStatusCounts{"a": {200: 2, 404: 1}, "b": {503: 1}}
	if !reflect.DeepEqual(td.ByRespHeaderAndStatus["x-r"], wantByStatus) {
		t.Errorf("ByRespHeaderAndStatus[x-r] = %v, want %v", td.ByRespHeaderAndStatus["x-r"], wantByStatus)
	}
}

func TestTrackUpstreamResponseCountsStatusesAndHeaders(t *testing.T) {
	port, key, td := newData(t)
	AddUpstreamResponseTracking(port, key, "/foo", []string{"x-r"})
	TrackUpstreamResponse(port, key, "/foo", 200, hdrs("x-r", "a"))
	TrackUpstreamResponse(port, key, "/foo", 500, hdrs("x-r", "a"))
	if want := (StatusCounts{200: 1, 500: 1}); !reflect.DeepEqual(td.ByUpRespStatus, want) || !reflect.DeepEqual(td.ByUpURIAndRespStatus["/foo"], want) {
		t.Errorf("ByUpRespStatus = %v, ByUpURIAndRespStatus[/foo] = %v, want %v", td.ByUpRespStatus, td.ByUpURIAndRespStatus["/foo"], want)
	}
	if got := td.ByUpRespHeader["x-r"]; got.CountsByHeader != 2 || got.CountsByHeaderValue["a"] != 2 {
		t.Errorf("ByUpRespHeader[x-r] = %+v, want 2 responses of value a", got)
	}
	if got := td.ByUpURIAndRespHeader["/foo"]["x-r"]; got.CountsByHeader != 2 || got.CountsByHeaderValue["a"] != 2 {
		t.Errorf("ByUpURIAndRespHeader[/foo][x-r] = %+v, want 2 responses of value a", got)
	}
	if want := (HeaderValueStatusCounts{"a": {200: 1, 500: 1}}); !reflect.DeepEqual(td.ByUpRespHeaderAndStatus["x-r"], want) {
		t.Errorf("ByUpRespHeaderAndStatus[x-r] = %v, want %v", td.ByUpRespHeaderAndStatus["x-r"], want)
	}
	if len(td.ByRespStatus) != 0 {
		t.Error("upstream responses must not be counted as responses")
	}
}

func TestClearCountsKeepsWhatIsTracked(t *testing.T) {
	port, key, td := newData(t)
	AddRequestTracking(port, key, "/foo", []string{"x-a"})
	AddUpstreamRequestTracking(port, key, "/up", []string{"x-a"})
	AddResponseTracking(port, key, "/foo", []string{"x-r"})
	AddUpstreamResponseTracking(port, key, "/up", []string{"x-r"})
	TrackRequest(port, key, "/foo", hdrs("x-a", "v"))
	TrackUpstreamRequest(port, key, "/up", hdrs("x-a", "v"))
	TrackResponse(port, key, "/foo", 200, hdrs("x-r", "v"))
	TrackUpstreamResponse(port, key, "/up", 200, hdrs("x-r", "v"))

	Tracker.clearCounts(port, key)

	if td.ByURI["/foo"] != 0 || td.ByUpURI["/up"] != 0 {
		t.Errorf("ByURI = %v, ByUpURI = %v, want zero counts", td.ByURI, td.ByUpURI)
	}
	for name, hvc := range map[string]*HeaderValueCounts{
		"ByReqHeader": td.ByReqHeader["x-a"], "ByURIAndReqHeader": td.ByURIAndReqHeader["/foo"]["x-a"],
		"ByUpReqHeader": td.ByUpReqHeader["x-a"], "ByUpURIAndReqHeader": td.ByUpURIAndReqHeader["/up"]["x-a"],
		"ByRespHeader": td.ByRespHeader["x-r"], "ByURIAndRespHeader": td.ByURIAndRespHeader["/foo"]["x-r"],
		"ByUpRespHeader": td.ByUpRespHeader["x-r"], "ByUpURIAndRespHeader": td.ByUpURIAndRespHeader["/up"]["x-r"],
	} {
		if hvc == nil {
			t.Errorf("%s: the tracked header is gone after the counts are cleared", name)
		} else if hvc.CountsByHeader != 0 || len(hvc.CountsByHeaderValue) != 0 {
			t.Errorf("%s = %+v, want cleared counts", name, hvc)
		}
	}
	if len(td.ByRespStatus) != 0 || len(td.ByUpRespStatus) != 0 {
		t.Errorf("ByRespStatus = %v, ByUpRespStatus = %v, want cleared", td.ByRespStatus, td.ByUpRespStatus)
	}
	if len(td.ByURIAndRespStatus["/foo"]) != 0 || len(td.ByUpURIAndRespStatus["/up"]) != 0 {
		t.Error("statuses of the tracked URIs must be cleared")
	}
	if td.ByURIAndRespStatus["/foo"] == nil || td.ByUpURIAndRespStatus["/up"] == nil {
		t.Error("the URIs must stay tracked for statuses")
	}
	if got := td.ByRespHeaderAndStatus["x-r"]; got == nil || len(got) != 0 {
		t.Errorf("ByRespHeaderAndStatus[x-r] = %v, want empty and still tracked", got)
	}
	if got := td.ByUpRespHeaderAndStatus["x-r"]; got == nil || len(got) != 0 {
		t.Errorf("ByUpRespHeaderAndStatus[x-r] = %v, want empty and still tracked", got)
	}
	// Counting goes on with what stayed tracked.
	TrackRequest(port, key, "/foo", hdrs("x-a", "v"))
	if td.ByURI["/foo"] != 1 || td.ByReqHeader["x-a"].CountsByHeader != 1 {
		t.Error("tracking must keep counting after the counts are cleared")
	}
}

func TestClearForgetsEverythingTracked(t *testing.T) {
	port, key, td := newData(t)
	AddRequestTracking(port, key, "/foo", []string{"x-a"})
	AddResponseTracking(port, key, "/foo", []string{"x-r"})
	TrackRequest(port, key, "/foo", hdrs("x-a", "v"))
	Tracker.clear(port, key)
	if len(td.ByURI)+len(td.ByReqHeader)+len(td.ByURIAndReqHeader)+len(td.ByRespHeader)+len(td.ByURIAndRespStatus)+len(td.ByRespHeaderAndStatus) != 0 {
		t.Error("clear must forget all tracking")
	}
	// A URI that is not tracked any more is still counted by the hook that may be left, creating its entry.
	TrackRequest(port, key, "/foo", nil)
	if td.ByURI["/foo"] != 1 {
		t.Errorf("ByURI = %v, want /foo counted once", td.ByURI)
	}
}

func TestDataIsKeptPerKeyAndPort(t *testing.T) {
	port, key, td := newData(t)
	otherKey := key + "-2"
	t.Cleanup(func() {
		Tracker.lock.Lock()
		delete(Tracker.KeyPort, otherKey)
		Tracker.lock.Unlock()
	})
	AddRequestTracking(port, key, "/foo", nil)
	AddRequestTracking(port, otherKey, "/foo", nil)
	AddRequestTracking(port+1000, key, "/foo", nil)
	TrackRequest(port, key, "/foo", nil)
	TrackRequest(port, key, "/foo", nil)
	TrackRequest(port, otherKey, "/foo", nil)
	if td.ByURI["/foo"] != 2 {
		t.Errorf("ByURI[/foo] = %d, want 2", td.ByURI["/foo"])
	}
	if got := Tracker.getKeyPort(port, otherKey).ByURI["/foo"]; got != 1 {
		t.Errorf("other key ByURI[/foo] = %d, want 1", got)
	}
	if got := Tracker.getKeyPort(port+1000, key).ByURI["/foo"]; got != 0 {
		t.Errorf("other port ByURI[/foo] = %d, want 0", got)
	}
	Tracker.lock.Lock()
	delete(Tracker.KeyPort[key], port+1000)
	Tracker.lock.Unlock()
}

func TestHeaderValueCountsTrack(t *testing.T) {
	hvc := newHeaderValueCounts()
	hvc.track("a")
	hvc.track("a")
	hvc.track("")
	if hvc.CountsByHeader != 3 || !reflect.DeepEqual(hvc.CountsByHeaderValue, map[string]int{"a": 2}) {
		t.Errorf("counts = %+v, want 3 with a:2", hvc)
	}
	hvc.clearCounts()
	if hvc.CountsByHeader != 0 || len(hvc.CountsByHeaderValue) != 0 || hvc.CountsByHeaderValue == nil {
		t.Errorf("cleared counts = %+v, want 0 and an empty map", hvc)
	}
}

func TestHeaderValueStatusCountsIgnoreEmptyValue(t *testing.T) {
	sc := newHeaderValueStatusCounts()
	sc.track("", 200)
	sc.track("a", 200)
	sc.track("a", 500)
	if want := (HeaderValueStatusCounts{"a": {200: 1, 500: 1}}); !reflect.DeepEqual(sc, want) {
		t.Errorf("counts = %v, want %v", sc, want)
	}
}

// Everything that is counted is exact when requests, responses, additions and clears come from many goroutines.
func TestConcurrentTrackingCountsExactly(t *testing.T) {
	port, key, td := newData(t)
	AddRequestTracking(port, key, "/foo", []string{"x-a"})
	AddResponseTracking(port, key, "/foo", []string{"x-r"})
	const workers, each = 8, 100
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				TrackRequest(port, key, "/foo", hdrs("x-a", "v"))
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				TrackResponse(port, key, "/foo", 200, hdrs("x-r", "v"))
			}
		}()
	}
	wg.Wait()
	if got := td.ByURI["/foo"]; got != workers*each {
		t.Errorf("ByURI[/foo] = %d, want %d", got, workers*each)
	}
	if got := td.ByReqHeader["x-a"]; got.CountsByHeader != workers*each || got.CountsByHeaderValue["v"] != workers*each {
		t.Errorf("ByReqHeader[x-a] = %+v, want %d", got, workers*each)
	}
	if got := td.ByRespStatus[200]; got != workers*each {
		t.Errorf("ByRespStatus[200] = %d, want %d", got, workers*each)
	}
	if got := td.ByURIAndRespHeader["/foo"]["x-r"]; got.CountsByHeader != workers*each {
		t.Errorf("ByURIAndRespHeader[/foo][x-r] = %+v, want %d", got, workers*each)
	}
}
