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

// Regression tests of the bugs that were fixed in this package. Each of them fails on the code before the fix.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"goto/pkg/server/hooks"
	"goto/pkg/server/request/tracking"

	"github.com/gorilla/mux"
)

// serveRecovering serves a request and turns a panic of a handler into a test failure, since a panic would otherwise
// take down the whole test binary.
func (e *testEnv) serveRecovering(method, target, body string) (rr *httptest.ResponseRecorder) {
	e.t.Helper()
	rr = httptest.NewRecorder()
	defer func() {
		if p := recover(); p != nil {
			e.t.Errorf("%s %s panicked: %v", method, target, p)
		}
	}()
	e.router.ServeHTTP(rr, e.request(method, target, body))
	return rr
}

// Tracking a service that the registry doesn't know panicked on the nil service that the registry returns.
func TestTrackServiceOfUnknownServiceIsRejected(t *testing.T) {
	e := newTestEnv(t)
	hooks.GetPortHooks(e.port)
	for _, path := range []string{"/track", "/track/headers/x-a", "/track/x-a=1"} {
		rr := e.serveRecovering("POST", "/grpc/server/NoSuchService"+path, "")
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Service proto not defined [NoSuchService]") {
			t.Errorf("POST %s: %d %q, want 400 and the service reported as not defined", path, rr.Code, rr.Body.String())
		}
	}
	if got := GetRPCTracker(e.port).ServiceTrackers; len(got) != 0 {
		t.Errorf("a rejected request must not start any tracking, got %v", got)
	}
}

// Reading the tracking of a service that the registry doesn't know panicked as well.
func TestGetServiceTrackingOfUnknownServiceIsRejected(t *testing.T) {
	e := newTestEnv(t)
	rr := e.serveRecovering("GET", "/jsonrpc/server/NoSuchService/tracking", "")
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Service proto not defined [NoSuchService]") {
		t.Errorf("GET tracking: %d %q, want 400 and the service reported as not defined", rr.Code, rr.Body.String())
	}
}

// CheckService asked reflect whether the service is nil, which panics for a nil interface and for values that aren't
// pointers (or maps, slices, etc.).
func TestCheckServiceToleratesRegistriesThatDontReturnTypedNils(t *testing.T) {
	e := newTestEnv(t)
	vars := map[string]string{"service": "Whatever"}
	check := func(reg RPCServiceRegistry) (rr *httptest.ResponseRecorder, service RPCService, ok bool) {
		rr = httptest.NewRecorder()
		defer func() {
			if p := recover(); p != nil {
				t.Errorf("CheckService panicked: %v", p)
			}
		}()
		service, _, _, _, ok = CheckService(rr, mux.SetURLVars(e.request("GET", "/x", ""), vars), reg)
		return
	}
	if rr, _, ok := check(untypedNilRegistry{}); ok || rr.Code != http.StatusBadRequest {
		t.Errorf("a registry that returns a nil interface: ok %v status %d, want the service rejected with 400", ok, rr.Code)
	}
	if rr, service, ok := check(valueRegistry{}); !ok || rr.Code != http.StatusOK || service.GetName() != "value" {
		t.Errorf("a registry that returns a non-pointer service: ok %v status %d, want the service accepted", ok, rr.Code)
	}
	if rr, _, ok := check(nil); ok || rr.Code != http.StatusBadRequest {
		t.Errorf("no registry: ok %v status %d, want 400", ok, rr.Code)
	}
}

// The payload APIs panicked (transform) or quietly answered with an empty message (set, clear) when they were called
// without a method.
func TestPayloadHandlersRequireAMethod(t *testing.T) {
	e := newTestEnv(t)
	e.json.add("NoMethodSvc", "Call")
	vars := map[string]string{"service": "NoMethodSvc"}
	handlers := map[string]func(http.ResponseWriter, *http.Request, RPCServiceRegistry){
		"set":       SetServiceResponsePayload,
		"clear":     ClearServiceResponsePayload,
		"transform": SetServicePayloadTransform,
	}
	for name, h := range handlers {
		func() {
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("%s panicked without a method: %v", name, p)
				}
			}()
			rr := e.call(h, e.json, "/x", `[{"payload":{}}]`, vars)
			if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "No method") {
				t.Errorf("%s: %d %q, want 400 and a message about the missing method", name, rr.Code, rr.Body.String())
			}
		}()
	}
}

// Header names are matched case insensitively by the hooks, but the tracking is reported by the names that the
// requests carry, which are canonical (a header that was asked to be tracked as x-case-test was never counted).
func TestTrackServiceCountsHeadersWhateverTheCaseTheyWereGivenIn(t *testing.T) {
	e := newTestEnv(t)
	name := uniqueName("casesvc")
	s := e.grpc.add(name, "m")
	hooks.GetPortHooks(e.port)
	GetRPCTracker(e.port).TrackService(e.port, s, []string{"x-case-test"}, "", "")
	uri := s.GetMethod("m").GetURI()
	e.sendTraffic("POST", uri, "X-Case-Test", "v1")
	e.sendTraffic("POST", uri, "x-case-test", "v1")

	data := trackedPortData(t, name, e.port)
	if got := string(data["byURIAndHeader"]); !strings.Contains(got, `"requestCounts":2`) || !strings.Contains(got, `"v1":2`) {
		t.Errorf("byURIAndHeader = %s, want the header counted for both requests", got)
	}
}

// Hooks are identified by an id, and a method hook was identified by the bare name of the method. A method that is
// named like another service replaced the hook of that service, which then didn't count any calls.
func TestTrackServiceHookIDsDoNotCollide(t *testing.T) {
	e := newTestEnv(t)
	hooks.GetPortHooks(e.port)
	first := uniqueName("clash")
	svcFirst := e.grpc.add(first, "Ping")
	svcSecond := e.grpc.add(uniqueName("other"), first) // a method that is named like the first service
	rt := GetRPCTracker(e.port)
	rt.TrackService(e.port, svcFirst, nil, "", "")
	rt.TrackService(e.port, svcSecond, nil, "", "")

	e.sendTraffic("POST", svcFirst.GetMethod("Ping").GetURI())
	e.sendTraffic("POST", svcSecond.GetMethod(first).GetURI())

	if got := rt.GetServiceTrackerJSON(svcFirst).ServiceCallCounts; got != 1 {
		t.Errorf("calls of the first service = %d, want 1", got)
	}
	if got := rt.GetServiceTrackerJSON(svcSecond).ServiceCallCounts; got != 1 {
		t.Errorf("calls of the second service = %d, want 1", got)
	}
}

// A call of method m1 was also counted as a call of method m, since the URIs were compared as plain prefixes.
func TestMethodCallCountsDoNotCountMethodsWithTheSamePrefix(t *testing.T) {
	e := newTestEnv(t)
	hooks.GetPortHooks(e.port)
	name := uniqueName("prefix")
	s := e.grpc.add(name, "m", "m1")
	rt := GetRPCTracker(e.port)
	rt.TrackService(e.port, s, nil, "", "")

	e.sendTraffic("POST", s.GetMethod("m1").GetURI())
	e.sendTraffic("POST", s.GetMethod("m").GetURI())
	e.sendTraffic("POST", s.GetMethod("m").GetURI()+"?x=1")

	got := rt.GetServiceTrackerJSON(s)
	want := map[string]int{s.GetMethod("m").GetURI(): 2, s.GetMethod("m1").GetURI(): 1}
	if got.ServiceCallCounts != 3 || !reflect.DeepEqual(got.ServiceMethodCallCounts, want) {
		t.Errorf("counts = %d %v, want 3 %v", got.ServiceCallCounts, got.ServiceMethodCallCounts, want)
	}
}

// Calls of another service whose name starts with the name of a tracked service were counted for the tracked service.
func TestServiceCallCountsDoNotCountServicesWithTheSamePrefix(t *testing.T) {
	e := newTestEnv(t)
	hooks.GetPortHooks(e.port)
	name := uniqueName("pfx")
	s := e.grpc.add(name, "m")
	longer := e.grpc.add(name+"x", "m")
	rt := GetRPCTracker(e.port)
	rt.TrackService(e.port, s, nil, "", "")

	e.sendTraffic("POST", longer.GetMethod("m").GetURI())
	e.sendTraffic("POST", s.GetMethod("m").GetURI())

	if got := rt.GetServiceTrackerJSON(s).ServiceCallCounts; got != 1 {
		t.Errorf("ServiceCallCounts = %d, want 1: only the call of the service itself", got)
	}
}

// The tracker that GetServiceTrackerJSON returned was the live one, which the API then wrote PortTracking into, and
// which was encoded while calls were being counted. The handler also read tracking.Tracker.KeyPort without its lock.
// Run with -race: the test fails on the data races of the old code.
func TestGetServiceTrackingConcurrentWithCountingAndTracking(t *testing.T) {
	e := newTestEnv(t)
	hooks.GetPortHooks(e.port)
	name := uniqueName("racy")
	s := e.json.add(name, "a", "b")
	rt := GetRPCTracker(e.port)
	rt.TrackService(e.port, s, nil, "", "")
	target := "/jsonrpc/server/" + name + "/tracking"

	const readers, readsPerReader, writers, writesPerWriter = 4, 25, 4, 100
	reqs := make([]*http.Request, 0, readers*readsPerReader)
	for i := 0; i < readers*readsPerReader; i++ {
		reqs = append(reqs, e.request("GET", target, ""))
	}
	codes := make([]int, len(reqs))
	var wg sync.WaitGroup
	for i := range reqs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rr := httptest.NewRecorder()
			e.router.ServeHTTP(rr, reqs[i])
			codes[i] = rr.Code
		}(i)
	}
	for w := 0; w < writers; w++ {
		wg.Add(2)
		go func(w int) { // counts calls
			defer wg.Done()
			for i := 0; i < writesPerWriter; i++ {
				rt.IncrementServiceCount(s, s.GetMethod("a").GetURI())
			}
		}(w)
		go func(w int) { // adds tracking data under other keys, which changes the map that the handler read
			defer wg.Done()
			for i := 0; i < writesPerWriter; i++ {
				tracking.Tracker.Init(e.port, fmt.Sprintf("%s-other-%d-%d", name, w, i), "/x", nil)
			}
		}(w)
	}
	wg.Wait()
	for i, code := range codes {
		if code != http.StatusOK {
			t.Fatalf("read %d = %d, want 200", i, code)
		}
	}
	if got := rt.GetServiceTrackerJSON(s); got.ServiceCallCounts != writers*writesPerWriter {
		t.Errorf("ServiceCallCounts = %d, want %d", got.ServiceCallCounts, writers*writesPerWriter)
	}
}

// The tracker that is returned is a copy: changing it, as the API does with PortTracking, must not change what the
// tracker holds.
func TestGetServiceTrackerJSONReturnsACopy(t *testing.T) {
	e := newTestEnv(t)
	hooks.GetPortHooks(e.port)
	s := e.grpc.add(uniqueName("copy"), "m")
	rt := GetRPCTracker(e.port)
	rt.TrackService(e.port, s, nil, "", "")
	rt.IncrementServiceCount(s, s.GetMethod("m").GetURI())

	got := rt.GetServiceTrackerJSON(s)
	got.PortTracking = "changed"
	got.ServiceCallCounts = 100
	got.ServiceMethodCallCounts[s.GetMethod("m").GetURI()] = 100

	again := rt.GetServiceTrackerJSON(s)
	if again.PortTracking != nil || again.ServiceCallCounts != 1 || again.ServiceMethodCallCounts[s.GetMethod("m").GetURI()] != 1 {
		t.Errorf("tracker = %+v, want it not to be affected by changes to a previous result", again)
	}
}
