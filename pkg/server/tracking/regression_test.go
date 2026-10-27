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
	"encoding/json"
	"fmt"
	"sync"
	"testing"
)

func panics(f func()) (recovered any) {
	defer func() { recovered = recover() }()
	f()
	return nil
}

// The counts of the headers of a URI were counted again for each header that a response has, instead of once per
// response.
func TestTrackResponseCountsEachResponseOnceForTheHeadersOfAURI(t *testing.T) {
	port, key, td := newData(t)
	AddResponseTracking(port, key, "/foo", []string{"x-r"})
	AddUpstreamResponseTracking(port, key, "/foo", []string{"x-r"})
	// The response has three headers, one of which is tracked.
	headers := hdrs("x-r", "a", "x-s", "b", "x-t", "c")
	TrackResponse(port, key, "/foo", 200, headers)
	TrackUpstreamResponse(port, key, "/foo", 200, headers)
	if got := td.ByURIAndRespHeader["/foo"]["x-r"].CountsByHeader; got != 1 {
		t.Errorf("ByURIAndRespHeader[/foo][x-r] counted %d for one response, want 1", got)
	}
	if got := td.ByUpURIAndRespHeader["/foo"]["x-r"].CountsByHeader; got != 1 {
		t.Errorf("ByUpURIAndRespHeader[/foo][x-r] counted %d for one response, want 1", got)
	}
	if got := td.ByRespHeader["x-r"].CountsByHeader; got != 1 {
		t.Errorf("ByRespHeader[x-r] counted %d for one response, want 1", got)
	}
}

// A response with no values for a tracked header (as a map that has the header, with an empty list) indexed the first
// value of the list.
func TestTrackResponseWithAnEmptyListOfValuesDoesNotPanic(t *testing.T) {
	port, key, td := newData(t)
	AddResponseTracking(port, key, "/foo", []string{"x-r"})
	AddUpstreamResponseTracking(port, key, "/foo", []string{"x-r"})
	headers := map[string][]string{"x-r": {}}
	if p := panics(func() { TrackResponse(port, key, "/foo", 200, headers) }); p != nil {
		t.Errorf("TrackResponse panicked: %v", p)
	}
	if p := panics(func() { TrackUpstreamResponse(port, key, "/foo", 200, headers) }); p != nil {
		t.Errorf("TrackUpstreamResponse panicked: %v", p)
	}
	if td.ByRespStatus[200] != 1 {
		t.Errorf("ByRespStatus = %v, want the response counted", td.ByRespStatus)
	}
}

// The headers of a request are named as the client sent them, canonicalized (X-Request-Id), while the ones that were
// asked to be tracked are named as they were given (x-request-id). Hooks match them whatever the case is, and the
// counting must as well.
func TestTrackedHeadersAreCountedWhateverTheirCase(t *testing.T) {
	port, key, td := newData(t)
	AddRequestTracking(port, key, "/foo", []string{"x-request-id"})
	AddUpstreamRequestTracking(port, key, "/up", []string{"x-request-id"})
	AddResponseTracking(port, key, "/foo", []string{"x-resp-id"})
	AddUpstreamResponseTracking(port, key, "/up", []string{"x-resp-id"})
	TrackRequest(port, key, "/foo", hdrs("X-Request-Id", "r1"))
	TrackUpstreamRequest(port, key, "/up", hdrs("X-Request-Id", "r1"))
	TrackResponse(port, key, "/foo", 200, hdrs("X-Resp-Id", "p1"))
	TrackUpstreamResponse(port, key, "/up", 200, hdrs("X-Resp-Id", "p1"))
	for name, hvc := range map[string]*HeaderValueCounts{
		"ByReqHeader": td.ByReqHeader["x-request-id"], "ByURIAndReqHeader": td.ByURIAndReqHeader["/foo"]["x-request-id"],
		"ByUpReqHeader": td.ByUpReqHeader["x-request-id"], "ByUpURIAndReqHeader": td.ByUpURIAndReqHeader["/up"]["x-request-id"],
	} {
		if hvc.CountsByHeader != 1 || hvc.CountsByHeaderValue["r1"] != 1 {
			t.Errorf("%s = %+v, want the request counted once with r1", name, hvc)
		}
	}
	for name, hvc := range map[string]*HeaderValueCounts{
		"ByRespHeader": td.ByRespHeader["x-resp-id"], "ByURIAndRespHeader": td.ByURIAndRespHeader["/foo"]["x-resp-id"],
		"ByUpRespHeader": td.ByUpRespHeader["x-resp-id"], "ByUpURIAndRespHeader": td.ByUpURIAndRespHeader["/up"]["x-resp-id"],
	} {
		if hvc.CountsByHeader != 1 || hvc.CountsByHeaderValue["p1"] != 1 {
			t.Errorf("%s = %+v, want the response counted once with p1", name, hvc)
		}
	}
	if got := td.ByRespHeaderAndStatus["x-resp-id"]["p1"][200]; got != 1 {
		t.Errorf("ByRespHeaderAndStatus[x-resp-id][p1][200] = %d, want 1", got)
	}
	if got := td.ByUpRespHeaderAndStatus["x-resp-id"]["p1"][200]; got != 1 {
		t.Errorf("ByUpRespHeaderAndStatus[x-resp-id][p1][200] = %d, want 1", got)
	}
}

// Tracking was added to the maps that requests are counted in without holding their lock.
func TestAddingTrackingWhileTrackingDoesNotRace(t *testing.T) {
	port, key, _ := newData(t)
	AddRequestTracking(port, key, "/seed", []string{"x-a"})
	AddResponseTracking(port, key, "/seed", []string{"x-r"})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			uri := fmt.Sprintf("/u%d", i)
			AddRequestTracking(port, key, uri, []string{"x-a"})
			AddUpstreamRequestTracking(port, key, uri, []string{"x-a"})
			AddResponseTracking(port, key, uri, []string{"x-r"})
			AddUpstreamResponseTracking(port, key, uri, []string{"x-r"})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			uri := fmt.Sprintf("/u%d", i)
			TrackRequest(port, key, uri, hdrs("x-a", "v"))
			TrackUpstreamRequest(port, key, uri, hdrs("x-a", "v"))
			TrackResponse(port, key, uri, 200, hdrs("x-r", "v"))
			TrackUpstreamResponse(port, key, uri, 200, hdrs("x-r", "v"))
		}
	}()
	wg.Wait()
}

// The counts were encoded without the locks that requests hold when they are counted.
func TestReportingCountsWhileTrackingDoesNotRace(t *testing.T) {
	port, key, _ := newData(t)
	AddRequestTracking(port, key, "/seed", []string{"x-a"})
	AddUpstreamRequestTracking(port, key, "/seed", []string{"x-a"})
	AddResponseTracking(port, key, "/seed", []string{"x-r"})
	AddUpstreamResponseTracking(port, key, "/seed", []string{"x-r"})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			TrackRequest(port, key, "/seed", hdrs("x-a", "v"))
			TrackUpstreamRequest(port, key, "/seed", hdrs("x-a", "v"))
			TrackResponse(port, key, "/seed", 200+i%3, hdrs("x-r", "v"))
			TrackUpstreamResponse(port, key, "/seed", 200, hdrs("x-r", "v"))
			if i%50 == 49 {
				Tracker.clearCounts(port, key)
			}
		}
	}()
	errs := make(chan error, 1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			if _, err := json.Marshal(Tracker); err != nil {
				errs <- err
				return
			}
		}
	}()
	wg.Wait()
	select {
	case err := <-errs:
		t.Errorf("encoding the counts failed: %v", err)
	default:
	}
	var rep reported
	b, err := json.Marshal(Tracker)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatalf("counts %q are not JSON: %v", b, err)
	}
	if _, ok := rep.KeyPortTracking[key][fmt.Sprint(port)]; !ok {
		t.Errorf("counts %s have no data for the key and port", b)
	}
}

// /uri/headers/{header}={value} was registered after /uri/headers/{headers}, which matches everything that the first
// does, so the first was never reached and "x=y" was tracked as a header of that name.
func TestHeaderAndValueRouteIsReachable(t *testing.T) {
	e := newEnv(t)
	e.configure("POST", fmt.Sprintf("/server/request/track/uri/headers/%s=blue?key=%s&uri=%s", e.hdr, e.key, e.uri))
	got := e.data()
	if _, ok := got.ByReqHeader[e.hdr]; !ok {
		t.Errorf("byRequestHeader = %v, want the header %s tracked by its name", got.ByReqHeader, e.hdr)
	}
	if _, ok := got.ByReqHeader[e.hdr+"=blue"]; ok {
		t.Errorf("byRequestHeader = %v, want the header and value not to be tracked as a header name", got.ByReqHeader)
	}
}
