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

package timeout

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goto/pkg/global"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

const (
	port1 = 19001
	port2 = 19002
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

func resetTracking() {
	timeoutTrackingLock.Lock()
	defer timeoutTrackingLock.Unlock()
	timeoutTrackingByPort = map[string]*TimeoutTracking{}
}

// setup clears all timeout tracking, and returns a router with the timeout routes registered.
func setup(t *testing.T) *mux.Router {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
	resetTracking()
	t.Cleanup(resetTracking)
	router := mux.NewRouter()
	setRoutes(router)
	return router
}

// newRequest creates a request that arrives on the given listener port and carries a RequestStore, as the server
// does before routing. The request is canceled at the end of the test, so that no tracking goroutine outlives it.
func newRequest(t *testing.T, method, target string, port int, headers ...string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Add(headers[i], headers[i+1])
	}
	ctx, cancel := context.WithCancel(context.WithValue(req.Context(), util.CurrentPortKey, port))
	t.Cleanup(cancel)
	_, req, _ = util.WithRequestStore(req.WithContext(ctx), nil)
	return req
}

func call(t *testing.T, router http.Handler, method, target string, port int) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, newRequest(t, method, target, port))
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

type statusReport struct {
	All     *TimeoutData                       `json:"all"`
	Headers map[string]map[string]*TimeoutData `json:"headers"`
}

func getReport(t *testing.T, router http.Handler, target string, port int) statusReport {
	t.Helper()
	rr := call(t, router, "GET", target, port)
	expectStatus(t, rr, http.StatusOK)
	report := statusReport{}
	if err := json.Unmarshal(rr.Body.Bytes(), &report); err != nil {
		t.Fatalf("response %q is not valid JSON: %v", rr.Body.String(), err)
	}
	return report
}

func trackingFor(t *testing.T, port int) *TimeoutTracking {
	t.Helper()
	return getTimeoutTracking(newRequest(t, "GET", "/timeout", port))
}

func TestMiddlewareRegistration(t *testing.T) {
	if Middleware == nil {
		t.Fatal("Middleware is nil")
	}
	if Middleware.Name != "timeout" {
		t.Errorf("Middleware.Name = %q, want %q", Middleware.Name, "timeout")
	}
	if Middleware.SetRoutes == nil || Middleware.MiddlewareHandler == nil {
		t.Error("Middleware must provide both routes and a handler")
	}
}

func TestGetTimeoutTracking(t *testing.T) {
	setup(t)
	tt1 := trackingFor(t, port1)
	if tt1.headersMap == nil {
		t.Error("headersMap must be initialized")
	}
	if tt1.allTimeouts != nil {
		t.Error("tracking of all requests must be off by default")
	}
	if again := trackingFor(t, port1); again != tt1 {
		t.Error("the same port must always get the same tracking")
	}
	if other := trackingFor(t, port2); other == tt1 {
		t.Error("different ports must get their own tracking")
	}
}

func TestTrackHeaders(t *testing.T) {
	for _, method := range []string{"PUT", "POST"} {
		t.Run(method, func(t *testing.T) {
			router := setup(t)
			rr := call(t, router, method, "/timeout/track/headers/x,y", port1)
			expectStatus(t, rr, http.StatusOK)
			expectBodyContains(t, rr, "Will track request timeout for Headers [x y]")

			tt := trackingFor(t, port1)
			if len(tt.headersMap) != 2 || tt.headersMap["x"] == nil || tt.headersMap["y"] == nil {
				t.Errorf("headersMap = %v, want entries for x and y", tt.headersMap)
			}
			if tt.allTimeouts != nil {
				t.Error("tracking headers must not enable tracking of all requests")
			}
		})
	}
}

func TestTrackHeadersAccumulatesAcrossCalls(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(t, router, "POST", "/timeout/track/headers/x", port1), http.StatusOK)
	expectStatus(t, call(t, router, "POST", "/timeout/track/headers/y", port1), http.StatusOK)
	if tt := trackingFor(t, port1); len(tt.headersMap) != 2 {
		t.Errorf("headersMap = %v, want x and y", tt.headersMap)
	}
}

func TestTrackHeadersWithoutHeaders(t *testing.T) {
	setup(t)
	req := newRequest(t, "POST", "/timeout/track/headers/", port1)
	rr := httptest.NewRecorder()
	trackHeaders(rr, req)
	expectStatus(t, rr, http.StatusBadRequest)
	expectBodyContains(t, rr, "Cannot track. Invalid header")
	if tt := trackingFor(t, port1); len(tt.headersMap) != 0 {
		t.Errorf("headersMap = %v, want it empty", tt.headersMap)
	}
}

func TestTrackAll(t *testing.T) {
	for _, method := range []string{"PUT", "POST"} {
		t.Run(method, func(t *testing.T) {
			router := setup(t)
			rr := call(t, router, method, "/timeout/track/all", port1)
			expectStatus(t, rr, http.StatusOK)
			expectBodyContains(t, rr, "Activated timeout tracking for all requests")

			tt := trackingFor(t, port1)
			if tt.allTimeouts == nil || *tt.allTimeouts != (TimeoutData{}) {
				t.Errorf("allTimeouts = %+v, want zero counts", tt.allTimeouts)
			}
			if len(tt.headersMap) != 0 {
				t.Errorf("headersMap = %v, tracking all must not track headers", tt.headersMap)
			}
		})
	}
}

func TestTrackAllResetsCounts(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(t, router, "POST", "/timeout/track/all", port1), http.StatusOK)
	trackingFor(t, port1).allTimeouts.ConnectionClosed = 4
	expectStatus(t, call(t, router, "POST", "/timeout/track/all", port1), http.StatusOK)
	if got := *trackingFor(t, port1).allTimeouts; got != (TimeoutData{}) {
		t.Errorf("allTimeouts = %+v, want counts reset by enabling again", got)
	}
}

func TestTrackingRoutesRejectOtherMethods(t *testing.T) {
	router := setup(t)
	expectNotRouted(t, call(t, router, "GET", "/timeout/track/all", port1))
	expectNotRouted(t, call(t, router, "GET", "/timeout/track/headers/x", port1))
	expectNotRouted(t, call(t, router, "PUT", "/timeout/track/clear", port1))
	expectNotRouted(t, call(t, router, "POST", "/timeout/status", port1))
	tt := trackingFor(t, port1)
	if tt.allTimeouts != nil || len(tt.headersMap) != 0 {
		t.Error("rejected requests must not change the tracking")
	}
}

func TestReportTimeoutTracking(t *testing.T) {
	for _, target := range []string{"/timeout/status", "/timeout"} {
		t.Run(target, func(t *testing.T) {
			router := setup(t)

			report := getReport(t, router, target, port1)
			if report.All != nil || len(report.Headers) != 0 {
				t.Errorf("report = %+v, want nothing tracked", report)
			}

			expectStatus(t, call(t, router, "POST", "/timeout/track/headers/x,y", port1), http.StatusOK)
			expectStatus(t, call(t, router, "POST", "/timeout/track/all", port1), http.StatusOK)
			tt := trackingFor(t, port1)
			tt.headersMap["x"]["x1"] = &TimeoutData{ConnectionClosed: 1, RequestCompleted: 5}
			tt.headersMap["x"]["x2"] = &TimeoutData{ConnectionClosed: 2}
			tt.allTimeouts.ConnectionClosed = 3
			tt.allTimeouts.RequestCompleted = 5

			report = getReport(t, router, target, port1)
			if report.All == nil || *report.All != (TimeoutData{ConnectionClosed: 3, RequestCompleted: 5}) {
				t.Errorf("all = %+v, want closed=3 completed=5", report.All)
			}
			if got := report.Headers["x"]["x1"]; got == nil || *got != (TimeoutData{ConnectionClosed: 1, RequestCompleted: 5}) {
				t.Errorf("headers[x][x1] = %+v, want closed=1 completed=5", got)
			}
			if got := report.Headers["x"]["x2"]; got == nil || *got != (TimeoutData{ConnectionClosed: 2}) {
				t.Errorf("headers[x][x2] = %+v, want closed=2 completed=0", got)
			}
			if values, present := report.Headers["y"]; !present || len(values) != 0 {
				t.Errorf("headers[y] = %v, want tracked without values", values)
			}
		})
	}
}

func TestReportUsesJSONFieldNames(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(t, router, "POST", "/timeout/track/all", port1), http.StatusOK)
	rr := call(t, router, "GET", "/timeout/status", port1)
	expectBodyContains(t, rr, `"all":{"connectionClosed":0,"requestCompleted":0}`)
	expectBodyContains(t, rr, `"headers":{}`)
}

func TestClearTimeoutTracking(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(t, router, "POST", "/timeout/track/headers/x", port1), http.StatusOK)
	expectStatus(t, call(t, router, "POST", "/timeout/track/all", port1), http.StatusOK)
	expectStatus(t, call(t, router, "POST", "/timeout/track/headers/x", port2), http.StatusOK)

	rr := call(t, router, "POST", "/timeout/track/clear", port1)
	expectStatus(t, rr, http.StatusOK)
	expectBodyContains(t, rr, "Timeout Tracking Headers Cleared")

	report := getReport(t, router, "/timeout/status", port1)
	if report.All != nil || len(report.Headers) != 0 {
		t.Errorf("report = %+v, want nothing tracked after clear", report)
	}
	if other := getReport(t, router, "/timeout/status", port2); len(other.Headers) != 1 {
		t.Errorf("other port report = %+v, clear must only affect the requested port", other)
	}
}

func TestClearTimeoutTrackingWhenNothingTracked(t *testing.T) {
	router := setup(t)
	rr := call(t, router, "POST", "/timeout/track/clear", port1)
	expectStatus(t, rr, http.StatusOK)
	expectBodyContains(t, rr, "Timeout Tracking Headers Cleared")
}

func TestTrackingIsPerPort(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(t, router, "POST", "/timeout/track/headers/x", port1), http.StatusOK)
	expectStatus(t, call(t, router, "POST", "/timeout/track/all", port2), http.StatusOK)

	r1 := getReport(t, router, "/timeout/status", port1)
	if len(r1.Headers) != 1 || r1.All != nil {
		t.Errorf("port %d report = %+v, want only header x", port1, r1)
	}
	r2 := getReport(t, router, "/timeout/status", port2)
	if len(r2.Headers) != 0 || r2.All == nil {
		t.Errorf("port %d report = %+v, want only all", port2, r2)
	}
}

// notifyRecorder is a ResponseRecorder that, like the writer of a real connection, can notify when the client
// closes the connection. The timeout middleware requires it.
type notifyRecorder struct {
	*httptest.ResponseRecorder
	closed chan bool
	calls  atomic.Int32
}

func newNotifyRecorder() *notifyRecorder {
	return &notifyRecorder{ResponseRecorder: httptest.NewRecorder(), closed: make(chan bool, 1)}
}

func (n *notifyRecorder) CloseNotify() <-chan bool {
	n.calls.Add(1)
	return n.closed
}

func (n *notifyRecorder) closeConnection() { n.closed <- true }

type tracked struct {
	req    *http.Request
	cancel context.CancelFunc
	w      *notifyRecorder
	called bool
}

// serve runs the timeout middleware for a request, with a cancelable context so that the request can be completed.
func serve(t *testing.T, port int, target string, headers ...string) *tracked {
	t.Helper()
	tr := &tracked{w: newNotifyRecorder()}
	req := httptest.NewRequest("GET", target, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Add(headers[i], headers[i+1])
	}
	ctx, cancel := context.WithCancel(context.WithValue(req.Context(), util.CurrentPortKey, port))
	t.Cleanup(cancel)
	tr.cancel = cancel
	_, tr.req, _ = util.WithRequestStore(req.WithContext(ctx), nil)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tr.called = true
		w.WriteHeader(http.StatusAccepted)
	})
	Middleware.MiddlewareHandler(next).ServeHTTP(tr.w, tr.req)
	return tr
}

func headerCounts(port int, header, value string) (TimeoutData, bool) {
	timeoutTrackingLock.RLock()
	defer timeoutTrackingLock.RUnlock()
	tt := timeoutTrackingByPort[portKey(port)]
	if tt == nil || tt.headersMap[header] == nil || tt.headersMap[header][value] == nil {
		return TimeoutData{}, false
	}
	return *tt.headersMap[header][value], true
}

func allCounts(port int) (TimeoutData, bool) {
	timeoutTrackingLock.RLock()
	defer timeoutTrackingLock.RUnlock()
	tt := timeoutTrackingByPort[portKey(port)]
	if tt == nil || tt.allTimeouts == nil {
		return TimeoutData{}, false
	}
	return *tt.allTimeouts, true
}

func portKey(port int) string {
	return util.GetRequestOrListenerPort(newPortRequest(port))
}

func newPortRequest(port int) *http.Request {
	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, port))
	_, req, _ = util.WithRequestStore(req, nil)
	return req
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestMiddlewareWithoutTracking(t *testing.T) {
	setup(t)
	tr := serve(t, port1, "/foo", "X", "x1")
	if !tr.called {
		t.Error("request must reach the next handler")
	}
	if tr.w.calls.Load() != 0 {
		t.Error("requests must not be watched when nothing is tracked")
	}
}

func TestMiddlewareIgnoresRequestsWithoutTrackedHeader(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(t, router, "POST", "/timeout/track/headers/x", port1), http.StatusOK)

	tr := serve(t, port1, "/foo", "Other", "o1")
	if !tr.called {
		t.Error("request must reach the next handler")
	}
	if tr.w.calls.Load() != 0 {
		t.Error("requests without a tracked header must not be watched")
	}
	if counts, _ := headerCounts(port1, "x", "o1"); counts != (TimeoutData{}) {
		t.Errorf("counts = %+v, want none", counts)
	}
}

func TestMiddlewareTracksCompletedRequestByHeader(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(t, router, "POST", "/timeout/track/headers/x", port1), http.StatusOK)

	tr := serve(t, port1, "/foo", "X", "x1")
	if !tr.called {
		t.Fatal("request must reach the next handler")
	}
	if tr.w.Code != http.StatusAccepted {
		t.Errorf("status = %d, want %d", tr.w.Code, http.StatusAccepted)
	}
	if tr.w.calls.Load() != 1 {
		t.Fatalf("CloseNotify calls = %d, want the request to be watched once", tr.w.calls.Load())
	}
	if counts, present := headerCounts(port1, "x", "x1"); !present || counts != (TimeoutData{}) {
		t.Errorf("counts before the request ends = %+v/%v, want tracked with zero counts", counts, present)
	}

	tr.cancel() // the request completed
	waitFor(t, "request completion to be counted", func() bool {
		counts, _ := headerCounts(port1, "x", "x1")
		return counts.RequestCompleted == 1
	})
	if counts, _ := headerCounts(port1, "x", "x1"); counts.ConnectionClosed != 0 {
		t.Errorf("counts = %+v, a completed request must not count as a closed connection", counts)
	}
}

func TestMiddlewareTracksClosedConnectionByHeader(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(t, router, "POST", "/timeout/track/headers/x", port1), http.StatusOK)

	tr := serve(t, port1, "/foo", "X", "x1")
	tr.w.closeConnection() // the client gave up
	waitFor(t, "closed connection to be counted", func() bool {
		counts, _ := headerCounts(port1, "x", "x1")
		return counts.ConnectionClosed == 1
	})
	if counts, _ := headerCounts(port1, "x", "x1"); counts.RequestCompleted != 0 {
		t.Errorf("counts = %+v, a closed connection must not count as a completed request", counts)
	}
}

func TestMiddlewareTracksHeaderValuesSeparately(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(t, router, "POST", "/timeout/track/headers/x", port1), http.StatusOK)

	for _, v := range []string{"x1", "x1", "x2"} {
		serve(t, port1, "/foo", "X", v).cancel()
	}
	waitFor(t, "all requests to be counted", func() bool {
		c1, _ := headerCounts(port1, "x", "x1")
		c2, _ := headerCounts(port1, "x", "x2")
		return c1.RequestCompleted == 2 && c2.RequestCompleted == 1
	})
}

func TestMiddlewareTracksMultipleHeaders(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(t, router, "POST", "/timeout/track/headers/x,y", port1), http.StatusOK)

	serve(t, port1, "/foo", "X", "x1", "Y", "y1").w.closeConnection()
	waitFor(t, "closed connection to be counted for both headers", func() bool {
		cx, _ := headerCounts(port1, "x", "x1")
		cy, _ := headerCounts(port1, "y", "y1")
		return cx.ConnectionClosed == 1 && cy.ConnectionClosed == 1
	})
}

func TestMiddlewareTracksAllRequests(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(t, router, "POST", "/timeout/track/all", port1), http.StatusOK)

	// No tracked header is needed, and any request is watched.
	serve(t, port1, "/foo").cancel()
	serve(t, port1, "/bar", "Any", "thing").cancel()
	serve(t, port1, "/baz").w.closeConnection()
	waitFor(t, "all requests to be counted", func() bool {
		counts, _ := allCounts(port1)
		return counts == TimeoutData{ConnectionClosed: 1, RequestCompleted: 2}
	})
}

func TestMiddlewareTracksAllAndHeaders(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(t, router, "POST", "/timeout/track/headers/x", port1), http.StatusOK)
	expectStatus(t, call(t, router, "POST", "/timeout/track/all", port1), http.StatusOK)

	serve(t, port1, "/foo", "X", "x1").cancel()
	serve(t, port1, "/foo").cancel() // not carrying the header, still counted for all
	waitFor(t, "requests to be counted", func() bool {
		all, _ := allCounts(port1)
		hdr, _ := headerCounts(port1, "x", "x1")
		return all.RequestCompleted == 2 && hdr.RequestCompleted == 1
	})
}

func TestMiddlewareOnlyTracksConfiguredPort(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(t, router, "POST", "/timeout/track/headers/x", port1), http.StatusOK)
	expectStatus(t, call(t, router, "POST", "/timeout/track/all", port1), http.StatusOK)

	tr := serve(t, port2, "/foo", "X", "x1")
	if !tr.called {
		t.Error("request must reach the next handler")
	}
	if tr.w.calls.Load() != 0 {
		t.Error("requests on another port must not be watched")
	}
}

func TestMiddlewareSkipsKnownNonTraffic(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(t, router, "POST", "/timeout/track/headers/x", port1), http.StatusOK)
	expectStatus(t, call(t, router, "POST", "/timeout/track/all", port1), http.StatusOK)

	tr := serve(t, port1, "/server/request/timeout", "X", "x1")
	if !util.GetRequestStore(tr.req).IsKnownNonTraffic {
		t.Fatal("admin uri must be treated as non traffic")
	}
	if !tr.called {
		t.Error("request must reach the next handler")
	}
	if tr.w.calls.Load() != 0 {
		t.Error("non traffic requests must not be watched")
	}
	if counts, _ := allCounts(port1); counts != (TimeoutData{}) {
		t.Errorf("all counts = %+v, want none", counts)
	}
}

func TestMiddlewareWithNilNext(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(t, router, "POST", "/timeout/track/all", port1), http.StatusOK)

	for _, target := range []string{"/foo", "/server/request/timeout"} {
		req := newRequest(t, "GET", target, port1)
		Middleware.MiddlewareHandler(nil).ServeHTTP(newNotifyRecorder(), req)
	}
}
