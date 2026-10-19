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

package rpc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"goto/pkg/server/hooks"
	"goto/pkg/server/request/tracking"
)

// sendTraffic sends a request through the hooks middleware, the way the server does, which is what makes the trackers
// of the services count calls.
func (e *testEnv) sendTraffic(method, uri string, headers ...string) {
	e.t.Helper()
	h := hooks.Middleware.MiddlewareHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	h.ServeHTTP(httptest.NewRecorder(), e.request(method, uri, "", headers...))
}

// trackedPortData returns what the request tracking holds for a key and port, as JSON: the tracking is read through its
// JSON encoding, which is the only way to read it with the locks of the tracking held.
func trackedPortData(t *testing.T, key string, port int) map[string]json.RawMessage {
	t.Helper()
	b, err := json.Marshal(tracking.Tracker)
	if err != nil {
		t.Fatalf("marshal tracking: %v", err)
	}
	var all struct {
		ByPort map[string]map[string]map[string]json.RawMessage `json:"byPort"`
	}
	if err := json.Unmarshal(b, &all); err != nil {
		t.Fatalf("unmarshal tracking: %v", err)
	}
	return all.ByPort[key][itoa(port)]
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}

func TestGetRPCTrackerIsPerPort(t *testing.T) {
	e1, e2 := newTestEnv(t), newTestEnv(t)
	t1, t1again, t2 := GetRPCTracker(e1.port), GetRPCTracker(e1.port), GetRPCTracker(e2.port)
	if t1 == nil || t1.ServiceTrackers == nil {
		t.Fatalf("tracker of a port must be initialized, got %+v", t1)
	}
	if t1 != t1again {
		t.Errorf("GetRPCTracker must return the same tracker for a port")
	}
	if t1 == t2 {
		t.Errorf("GetRPCTracker must return a tracker of its own for every port")
	}
}

func TestGetRPCTrackerConcurrentCreatesOneTracker(t *testing.T) {
	e := newTestEnv(t)
	const n = 64
	got := make([]*RPCTracker, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i] = GetRPCTracker(e.port)
		}(i)
	}
	wg.Wait()
	for i := 1; i < n; i++ {
		if got[i] != got[0] {
			t.Fatalf("goroutine %d got a different tracker than goroutine 0", i)
		}
	}
}

func TestGetServiceTrackerJSONUntracked(t *testing.T) {
	e := newTestEnv(t)
	s := e.grpc.add(uniqueName("untracked"), "m")
	if got := GetRPCTracker(e.port).GetServiceTrackerJSON(s); got != nil {
		t.Errorf("a service that isn't tracked has no tracker, got %+v", got)
	}
}

func TestIncrementServiceCountIgnoresUntrackedService(t *testing.T) {
	e := newTestEnv(t)
	s := e.grpc.add(uniqueName("ignored"), "m")
	rt := GetRPCTracker(e.port)
	rt.IncrementServiceCount(s, s.GetMethod("m").GetURI())
	if got := rt.GetServiceTrackerJSON(s); got != nil {
		t.Errorf("counting a call of an untracked service must not start tracking it, got %+v", got)
	}
}

func TestTrackServiceCountsCalls(t *testing.T) {
	e := newTestEnv(t)
	name := uniqueName("svc")
	s := e.grpc.add(name, "Alpha", "Beta")
	rt := GetRPCTracker(e.port)
	hooks.GetPortHooks(e.port)
	rt.TrackService(e.port, s, nil, "", "")

	got := rt.GetServiceTrackerJSON(s)
	if got == nil {
		t.Fatalf("service must be tracked after TrackService")
	}
	if got.ServiceCallCounts != 0 || len(got.ServiceMethodCallCounts) != 0 {
		t.Fatalf("nothing was called yet, got %+v", got)
	}

	for i := 0; i < 3; i++ {
		e.sendTraffic("POST", s.GetMethod("Alpha").GetURI())
	}
	e.sendTraffic("POST", s.GetMethod("Beta").GetURI())

	got = rt.GetServiceTrackerJSON(s)
	if got.ServiceCallCounts != 4 {
		t.Errorf("ServiceCallCounts = %d, want 4", got.ServiceCallCounts)
	}
	want := map[string]int{s.GetMethod("Alpha").GetURI(): 3, s.GetMethod("Beta").GetURI(): 1}
	if !reflect.DeepEqual(got.ServiceMethodCallCounts, want) {
		t.Errorf("ServiceMethodCallCounts = %v, want %v", got.ServiceMethodCallCounts, want)
	}
}

func TestTrackServiceDoesNotCountOtherServices(t *testing.T) {
	e := newTestEnv(t)
	s := e.grpc.add(uniqueName("mine"), "m")
	other := e.grpc.add(uniqueName("other"), "m")
	hooks.GetPortHooks(e.port)
	rt := GetRPCTracker(e.port)
	rt.TrackService(e.port, s, nil, "", "")

	e.sendTraffic("POST", other.GetMethod("m").GetURI())
	e.sendTraffic("POST", "/unrelated/path")

	if got := rt.GetServiceTrackerJSON(s); got.ServiceCallCounts != 0 {
		t.Errorf("calls of other URIs must not be counted, got %+v", got)
	}
	if got := rt.GetServiceTrackerJSON(other); got != nil {
		t.Errorf("a service that wasn't asked to be tracked must not be tracked, got %+v", got)
	}
}

func TestTrackServiceIsPerPort(t *testing.T) {
	e1, e2 := newTestEnv(t), newTestEnv(t)
	name := uniqueName("shared")
	s1, s2 := e1.grpc.add(name, "m"), e2.grpc.add(name, "m")
	hooks.GetPortHooks(e1.port)
	hooks.GetPortHooks(e2.port)
	GetRPCTracker(e1.port).TrackService(e1.port, s1, nil, "", "")
	GetRPCTracker(e2.port).TrackService(e2.port, s2, nil, "", "")

	e1.sendTraffic("POST", s1.GetMethod("m").GetURI())
	e1.sendTraffic("POST", s1.GetMethod("m").GetURI())
	e2.sendTraffic("POST", s2.GetMethod("m").GetURI())

	if got := GetRPCTracker(e1.port).GetServiceTrackerJSON(s1).ServiceCallCounts; got != 2 {
		t.Errorf("port 1 count = %d, want 2", got)
	}
	if got := GetRPCTracker(e2.port).GetServiceTrackerJSON(s2).ServiceCallCounts; got != 1 {
		t.Errorf("port 2 count = %d, want 1", got)
	}
}

func TestTrackServiceMethodTracksOnlyTheGivenMethodURI(t *testing.T) {
	e := newTestEnv(t)
	name := uniqueName("svc")
	s := e.grpc.add(name, "One", "Two")
	hooks.GetPortHooks(e.port)
	rt := GetRPCTracker(e.port)
	rt.TrackServiceMethod(e.port, s, s.GetMethod("One"), nil, "", "")

	data := trackedPortData(t, name, e.port)
	var byURI map[string]int
	if err := json.Unmarshal(data["byURI"], &byURI); err != nil {
		t.Fatalf("byURI: %v (%s)", err, data["byURI"])
	}
	if _, ok := byURI[s.URI]; !ok {
		t.Errorf("the service URI must be tracked, got %v", byURI)
	}
	if _, ok := byURI[s.GetMethod("One").GetURI()]; !ok {
		t.Errorf("the given method URI must be tracked, got %v", byURI)
	}
	if _, ok := byURI[s.GetMethod("Two").GetURI()]; ok {
		t.Errorf("a method that wasn't given must not be tracked, got %v", byURI)
	}
}

func TestTrackServiceTracksAllMethodURIs(t *testing.T) {
	e := newTestEnv(t)
	name := uniqueName("svc")
	s := e.grpc.add(name, "One", "Two", "Three")
	hooks.GetPortHooks(e.port)
	GetRPCTracker(e.port).TrackService(e.port, s, nil, "", "")

	data := trackedPortData(t, name, e.port)
	var byURI map[string]int
	if err := json.Unmarshal(data["byURI"], &byURI); err != nil {
		t.Fatalf("byURI: %v (%s)", err, data["byURI"])
	}
	want := map[string]int{s.URI: 0}
	for _, m := range s.Methods {
		want[m.URI] = 0
	}
	if !reflect.DeepEqual(byURI, want) {
		t.Errorf("tracked URIs = %v, want %v", byURI, want)
	}
}

func TestTrackServiceWithHeaders(t *testing.T) {
	e := newTestEnv(t)
	name := uniqueName("svc")
	s := e.grpc.add(name, "m")
	hooks.GetPortHooks(e.port)
	GetRPCTracker(e.port).TrackService(e.port, s, []string{"x-track-a", "x-track-b"}, "", "")

	uri := s.GetMethod("m").GetURI()
	e.sendTraffic("POST", uri, "X-Track-A", "one")
	e.sendTraffic("POST", uri, "X-Track-A", "one")
	e.sendTraffic("POST", uri, "X-Track-A", "two", "X-Track-B", "b1")
	e.sendTraffic("POST", uri)

	if got := GetRPCTracker(e.port).GetServiceTrackerJSON(s).ServiceCallCounts; got != 4 {
		t.Errorf("ServiceCallCounts = %d, want 4", got)
	}
	data := trackedPortData(t, name, e.port)
	var byURIAndHeader map[string]map[string]struct {
		RequestCounts              int            `json:"requestCounts"`
		RequestCountsByHeaderValue map[string]int `json:"requestCountsByHeaderValue"`
	}
	if err := json.Unmarshal(data["byURIAndHeader"], &byURIAndHeader); err != nil {
		t.Fatalf("byURIAndHeader: %v (%s)", err, data["byURIAndHeader"])
	}
	a := byURIAndHeader[uri]["X-Track-A"]
	if a.RequestCounts != 3 || a.RequestCountsByHeaderValue["one"] != 2 || a.RequestCountsByHeaderValue["two"] != 1 {
		t.Errorf("X-Track-A counts = %+v, want 3 requests, one:2, two:1", a)
	}
	b := byURIAndHeader[uri]["X-Track-B"]
	if b.RequestCounts != 1 || b.RequestCountsByHeaderValue["b1"] != 1 {
		t.Errorf("X-Track-B counts = %+v, want 1 request, b1:1", b)
	}
}

func TestTrackServiceWithHeaderValue(t *testing.T) {
	e := newTestEnv(t)
	name := uniqueName("svc")
	s := e.grpc.add(name, "m")
	hooks.GetPortHooks(e.port)
	GetRPCTracker(e.port).TrackService(e.port, s, []string{"ignored"}, "x-only", "yes")

	uri := s.GetMethod("m").GetURI()
	e.sendTraffic("POST", uri, "X-Only", "yes")
	e.sendTraffic("POST", uri, "X-Only", "no")

	data := trackedPortData(t, name, e.port)
	var byURIAndHeader map[string]map[string]struct {
		RequestCounts              int            `json:"requestCounts"`
		RequestCountsByHeaderValue map[string]int `json:"requestCountsByHeaderValue"`
	}
	if err := json.Unmarshal(data["byURIAndHeader"], &byURIAndHeader); err != nil {
		t.Fatalf("byURIAndHeader: %v (%s)", err, data["byURIAndHeader"])
	}
	if _, ok := byURIAndHeader[uri]["Ignored"]; ok {
		t.Errorf("when a header/value is given the headers list is ignored, got %v", byURIAndHeader[uri])
	}
	h := byURIAndHeader[uri]["X-Only"]
	if h.RequestCounts != 1 || h.RequestCountsByHeaderValue["yes"] != 1 || h.RequestCountsByHeaderValue["no"] != 0 {
		t.Errorf("X-Only counts = %+v, want exactly the matching value counted once", h)
	}
}

func TestRPCTrackerClear(t *testing.T) {
	e := newTestEnv(t)
	s := e.grpc.add(uniqueName("svc"), "m")
	hooks.GetPortHooks(e.port)
	rt := GetRPCTracker(e.port)
	rt.TrackService(e.port, s, nil, "", "")
	e.sendTraffic("POST", s.GetMethod("m").GetURI())
	if got := rt.GetServiceTrackerJSON(s); got == nil || got.ServiceCallCounts != 1 {
		t.Fatalf("expected one counted call before Clear, got %+v", got)
	}
	rt.Clear()
	if got := rt.GetServiceTrackerJSON(s); got != nil {
		t.Errorf("Clear must forget all the services, got %+v", got)
	}
	// the hook of the tracked service is still there, but without a tracker for the service nothing is counted
	e.sendTraffic("POST", s.GetMethod("m").GetURI())
	if got := rt.GetServiceTrackerJSON(s); got != nil {
		t.Errorf("a call after Clear must not start tracking again, got %+v", got)
	}
}

// Exact totals are expected when many goroutines count calls and read the tracker concurrently. Run with -race.
func TestRPCTrackerConcurrentIncrementAndRead(t *testing.T) {
	e := newTestEnv(t)
	s := e.grpc.add(uniqueName("svc"), "a", "b")
	hooks.GetPortHooks(e.port)
	rt := GetRPCTracker(e.port)
	rt.TrackService(e.port, s, nil, "", "")

	const writers, perWriter, readers = 8, 200, 4
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			uri := s.GetMethod("a").GetURI()
			if w%2 == 1 {
				uri = s.GetMethod("b").GetURI()
			}
			for i := 0; i < perWriter; i++ {
				rt.IncrementServiceCount(s, uri)
			}
		}(w)
	}
	stop := make(chan struct{})
	var rwg sync.WaitGroup
	for r := 0; r < readers; r++ {
		rwg.Add(1)
		go func() {
			defer rwg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					if got := rt.GetServiceTrackerJSON(s); got != nil {
						if _, err := json.Marshal(got); err != nil {
							return
						}
					}
				}
			}
		}()
	}
	wg.Wait()
	close(stop)
	rwg.Wait()

	got := rt.GetServiceTrackerJSON(s)
	if got.ServiceCallCounts != writers*perWriter {
		t.Errorf("ServiceCallCounts = %d, want %d", got.ServiceCallCounts, writers*perWriter)
	}
	want := map[string]int{s.GetMethod("a").GetURI(): writers / 2 * perWriter, s.GetMethod("b").GetURI(): writers / 2 * perWriter}
	if !reflect.DeepEqual(got.ServiceMethodCallCounts, want) {
		t.Errorf("ServiceMethodCallCounts = %v, want %v", got.ServiceMethodCallCounts, want)
	}
}
