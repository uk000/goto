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
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/server/hooks"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
	stubOnce sync.Once
	// Hooks are registered per port and can't be removed, so every test uses a port of its own.
	portSeq atomic.Int32
)

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
	// Created by the server at startup. Registering a hook builds its URI matcher on util.MatchRouter.
	if util.MatchRouter == nil {
		util.CreateRouters(mux.NewRouter())
	}
}

func resetTracker() {
	Tracker.lock.Lock()
	defer Tracker.lock.Unlock()
	Tracker.KeyPort = map[string]map[int]*TrackingData{}
}

// setup clears the tracker, and returns a router with the tracking routes registered, together with a port that
// no other test uses.
func setup(t *testing.T) (*mux.Router, int) {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
	resetTracker()
	t.Cleanup(resetTracker)
	router := mux.NewRouter()
	setRoutes(router)
	return router, int(20000 + portSeq.Add(1))
}

// newRequest creates a request that arrives on the given listener port and carries a RequestStore, as the server
// does before routing.
func newRequest(method, target string, port int, headers ...string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Add(headers[i], headers[i+1])
	}
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, port))
	_, req, _ = util.WithRequestStore(req, nil)
	return req
}

func call(router http.Handler, method, target string, port int) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, newRequest(method, target, port))
	return rr
}

// callHandler invokes a handler directly. The route vars are only set when given, which lets tests reach the
// validation branches that no route can reach.
func callHandler(h http.HandlerFunc, port int, vars map[string]string) *httptest.ResponseRecorder {
	req := newRequest("POST", "/track", port)
	if vars != nil {
		req = mux.SetURLVars(req, vars)
	}
	rr := httptest.NewRecorder()
	h(rr, req)
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

func dataFor(port int) *TrackingData { return Tracker.getPortRequestTrackingData(port, "") }

func headerNames(rtd *TrackingData) []string {
	names := rtd.getHeaders()
	sort.Strings(names)
	return names
}

func hookMatches(port int, uri string, headers map[string][]string) bool {
	all, _ := hooks.GetPortHooks(port).MatchHTTPRequest(uri, headers)
	return len(all) > 0
}

func TestMiddlewareRegistration(t *testing.T) {
	if Middleware == nil {
		t.Fatal("Middleware is nil")
	}
	if Middleware.Name != "tracking" {
		t.Errorf("Middleware.Name = %q, want %q", Middleware.Name, "tracking")
	}
	if Middleware.SetRoutes == nil || Middleware.MiddlewareHandler == nil {
		t.Error("Middleware must provide both routes and a handler")
	}
	if reflect.ValueOf(hooks.HeaderTrackingFunc).Pointer() != reflect.ValueOf(Track).Pointer() {
		t.Error("hooks.HeaderTrackingFunc must be wired to Track")
	}
}

func TestAddHeaders(t *testing.T) {
	router, port := setup(t)
	rr := call(router, "POST", "/track/headers/add/h1,h2", port)
	expectStatus(t, rr, http.StatusOK)
	expectBodyContains(t, rr, "Port ["+portText(port)+"] will track headers [h1 h2]")

	rtd := dataFor(port)
	if got := headerNames(rtd); !reflect.DeepEqual(got, []string{"h1", "h2"}) {
		t.Fatalf("tracked headers = %v, want [h1 h2]", got)
	}
	if hd := rtd.getHeaderData("h1"); hd.RequestCountsByHeaderValue == nil || hd.RequestCountsByHeader != 0 {
		t.Errorf("header data = %+v, want it initialized without counts", hd)
	}

	expectNotRouted(t, call(router, "GET", "/track/headers/add/h3", port))
}

func TestAddHeadersKeepsExistingCounts(t *testing.T) {
	router, port := setup(t)
	expectStatus(t, call(router, "POST", "/track/headers/add/h1", port), http.StatusOK)
	dataFor(port).getHeaderData("h1").trackRequest("v")

	expectStatus(t, call(router, "POST", "/track/headers/add/h1,h2", port), http.StatusOK)
	if hd := dataFor(port).getHeaderData("h1"); hd.RequestCountsByHeader != 1 || hd.RequestCountsByHeaderValue["v"] != 1 {
		t.Errorf("header data = %+v, adding a header again must not reset its counts", hd)
	}
}

func TestAddHeadersWithoutHeaders(t *testing.T) {
	_, port := setup(t)
	rr := callHandler(addHeaders, port, nil)
	expectStatus(t, rr, http.StatusBadRequest)
	expectBodyContains(t, rr, "Cannot add. Invalid header")
	if got := headerNames(dataFor(port)); len(got) != 0 {
		t.Errorf("tracked headers = %v, want none", got)
	}
}

func TestRemoveHeaders(t *testing.T) {
	router, port := setup(t)
	expectStatus(t, call(router, "POST", "/track/headers/add/h1,h2,h3", port), http.StatusOK)

	rr := call(router, "POST", "/track/headers/remove/h1,h2", port)
	expectStatus(t, rr, http.StatusOK)
	expectBodyContains(t, rr, "Port ["+portText(port)+"] tracking headers [h1 h2] removed")
	if got := headerNames(dataFor(port)); !reflect.DeepEqual(got, []string{"h3"}) {
		t.Errorf("tracked headers = %v, want [h3]", got)
	}

	// Removing a header that isn't tracked is not an error.
	expectStatus(t, call(router, "POST", "/track/headers/remove/unknown", port), http.StatusOK)
	if got := headerNames(dataFor(port)); !reflect.DeepEqual(got, []string{"h3"}) {
		t.Errorf("tracked headers = %v, want [h3]", got)
	}

	expectNotRouted(t, call(router, "GET", "/track/headers/remove/h3", port))
}

func TestRemoveHeadersWithoutHeaders(t *testing.T) {
	_, port := setup(t)
	rr := callHandler(removeHeaders, port, nil)
	expectStatus(t, rr, http.StatusBadRequest)
	expectBodyContains(t, rr, "No headers to remove")
}

func TestClearHeaders(t *testing.T) {
	router, port := setup(t)
	expectStatus(t, call(router, "POST", "/track/headers/add/h1,h2", port), http.StatusOK)
	expectStatus(t, call(router, "POST", "/track/headers/add/h3", port+1000), http.StatusOK)
	dataFor(port).getHeaderData("h1").trackRequest("v")

	rr := call(router, "POST", "/track/headers/clear", port)
	expectStatus(t, rr, http.StatusOK)
	expectBodyContains(t, rr, "Port ["+portText(port)+"] tracking headers cleared")
	if got := headerNames(dataFor(port)); len(got) != 0 {
		t.Errorf("tracked headers = %v, want none after clear", got)
	}
	if got := headerNames(dataFor(port + 1000)); !reflect.DeepEqual(got, []string{"h3"}) {
		t.Errorf("headers of another port = %v, clear must only affect the requested port", got)
	}

	expectNotRouted(t, call(router, "GET", "/track/headers/clear", port))
}

func TestClearHeaderCounts(t *testing.T) {
	router, port := setup(t)
	expectStatus(t, call(router, "POST", "/track/headers/add/h1,h2", port), http.StatusOK)
	rtd := dataFor(port)
	rtd.getHeaderData("h1").trackRequest("v1")
	rtd.getHeaderData("h2").trackRequest("v2")

	rr := call(router, "POST", "/track/headers/counts/clear", port)
	expectStatus(t, rr, http.StatusOK)
	expectBodyContains(t, rr, "Port ["+portText(port)+"] tracking counts reset for all headers")
	for _, h := range []string{"h1", "h2"} {
		if hd := rtd.getHeaderData(h); hd == nil || len(hd.RequestCountsByHeaderValue) != 0 {
			t.Errorf("header %s data = %+v, want it tracked with its value counts cleared", h, hd)
		}
	}
	if got := headerNames(rtd); !reflect.DeepEqual(got, []string{"h1", "h2"}) {
		t.Errorf("tracked headers = %v, clearing counts must keep the headers tracked", got)
	}

	rr = call(router, "POST", "/track/headers/counts/clear/h1", port)
	expectStatus(t, rr, http.StatusOK)
	expectBodyContains(t, rr, "Port ["+portText(port)+"] tracking counts reset for headers [h1]")
	if got := headerNames(rtd); !reflect.DeepEqual(got, []string{"h1", "h2"}) {
		t.Errorf("tracked headers = %v, clearing counts must keep the headers tracked", got)
	}

	expectNotRouted(t, call(router, "GET", "/track/headers/counts/clear", port))
}

type headerCountsReport struct {
	RequestCounts              int            `json:"requestCounts"`
	RequestCountsByHeaderValue map[string]int `json:"requestCountsByHeaderValue"`
}

type trackerReport struct {
	ByPort map[string]map[string]struct {
		ByHeader       map[string]headerCountsReport            `json:"byHeader"`
		ByURI          map[string]int                           `json:"byURI"`
		ByURIAndHeader map[string]map[string]headerCountsReport `json:"byURIAndHeader"`
	} `json:"byPort"`
}

func TestGetCounts(t *testing.T) {
	for _, target := range []string{"/track/headers/counts", "/track/counts", "/track"} {
		t.Run(target, func(t *testing.T) {
			router, port := setup(t)
			Tracker.Init(port, "", "/foo", []string{"h1"})
			Tracker.initHeaders(port, "", []string{"h2"})
			Tracker.TrackCall(port, "", "/foo", map[string][]string{"h1": {"a", "b"}, "h2": {"c"}})
			Tracker.TrackCall(port, "", "/foo", map[string][]string{"h1": {"a"}})
			// The tracked headers are counted by the middleware.
			track(portText(port), http.Header{"H2": {"c"}}, "/foo", dataFor(port))

			rr := call(router, "GET", target, port)
			expectStatus(t, rr, http.StatusOK)
			if ct := rr.Header().Get(constants.HeaderContentType); ct != constants.ContentTypeJSON {
				t.Errorf("Content-Type = %q, want %q", ct, constants.ContentTypeJSON)
			}
			report := trackerReport{}
			if err := json.Unmarshal(rr.Body.Bytes(), &report); err != nil {
				t.Fatalf("response %q is not valid JSON: %v", rr.Body.String(), err)
			}
			data, present := report.ByPort[""][portText(port)]
			if !present {
				t.Fatalf("report = %s, want data for port %d under the default key", rr.Body.String(), port)
			}
			if data.ByURI["/foo"] != 2 {
				t.Errorf("byURI = %v, want /foo=2", data.ByURI)
			}
			h1 := data.ByURIAndHeader["/foo"]["h1"]
			if h1.RequestCounts != 3 || h1.RequestCountsByHeaderValue["a"] != 2 || h1.RequestCountsByHeaderValue["b"] != 1 {
				t.Errorf("byURIAndHeader[/foo][h1] = %+v, want 3 requests with a=2 b=1", h1)
			}
			h2 := data.ByHeader["h2"]
			if h2.RequestCounts != 1 || h2.RequestCountsByHeaderValue["c"] != 1 {
				t.Errorf("byHeader[h2] = %+v, want 1 request with c=1", h2)
			}
		})
	}
}

func TestGetCountsRejectsOtherMethods(t *testing.T) {
	router, port := setup(t)
	expectNotRouted(t, call(router, "DELETE", "/track/counts", port))
	expectNotRouted(t, call(router, "PUT", "/track/headers/counts", port))
}

func TestGetHeaders(t *testing.T) {
	router, port := setup(t)

	rr := call(router, "GET", "/track/headers", port)
	expectStatus(t, rr, http.StatusOK)
	if got := strings.TrimSpace(rr.Body.String()); got != "[]" {
		t.Errorf("headers = %s, want [] when nothing is tracked", got)
	}

	expectStatus(t, call(router, "POST", "/track/headers/add/h2,h1", port), http.StatusOK)
	expectStatus(t, call(router, "POST", "/track/headers/add/h3", port+1000), http.StatusOK)
	rr = call(router, "GET", "/track/headers", port)
	expectStatus(t, rr, http.StatusOK)
	headers := []string{}
	if err := json.Unmarshal(rr.Body.Bytes(), &headers); err != nil {
		t.Fatalf("response %q is not valid JSON: %v", rr.Body.String(), err)
	}
	sort.Strings(headers)
	if !reflect.DeepEqual(headers, []string{"h1", "h2"}) {
		t.Errorf("headers = %v, want [h1 h2]", headers)
	}
}

func TestTrackHeaders(t *testing.T) {
	router, port := setup(t)
	rr := call(router, "POST", "/track/headers/h1,h2", port)
	expectStatus(t, rr, http.StatusOK)

	// Headers are counted by the middleware, in the same way as the headers that are added with /headers/add.
	rtd := dataFor(port)
	for _, h := range []string{"h1", "h2"} {
		if rtd.getHeaderData(h) == nil {
			t.Errorf("byHeader = %v, want %s to be tracked", rtd.ByHeader, h)
		}
	}
	if len(rtd.ByURI) != 0 || len(rtd.ByURIAndHeader) != 0 {
		t.Errorf("byURI = %v, byURIAndHeader = %v, want no URI to be tracked", rtd.ByURI, rtd.ByURIAndHeader)
	}
	// A hook without a URI matches all requests of the port, and counts every URI that is requested.
	if hookMatches(port, "/any", map[string][]string{"h1": {"v"}}) {
		t.Error("no hook must be registered for headers only")
	}
}

func TestTrackHeadersWithoutHeaders(t *testing.T) {
	_, port := setup(t)
	rr := callHandler(trackHeaders, port, nil)
	expectStatus(t, rr, http.StatusBadRequest)
	if hookMatches(port, "/any", map[string][]string{"h1": {"v"}}) {
		t.Error("no hook must be registered for invalid input")
	}
}

func TestTrackURI(t *testing.T) {
	router, port := setup(t)
	rr := call(router, "POST", "/track?uri=/foo", port)
	expectStatus(t, rr, http.StatusOK)

	rtd := dataFor(port)
	if count, present := rtd.ByURI["/foo"]; !present || count != 0 {
		t.Errorf("byURI = %v, want /foo tracked with 0 calls", rtd.ByURI)
	}
	if !hookMatches(port, "/foo", nil) {
		t.Error("a hook for the URI must be registered on the port")
	}
	if hookMatches(port, "/bar", nil) {
		t.Error("the hook must only match the tracked URI")
	}

	expectNotRouted(t, call(router, "POST", "/track", port))
	expectNotRouted(t, call(router, "PUT", "/track?uri=/foo", port))
}

func TestTrackURIWithoutURI(t *testing.T) {
	_, port := setup(t)
	rr := callHandler(trackURI, port, nil)
	expectStatus(t, rr, http.StatusBadRequest)
	expectBodyContains(t, rr, "Invalid URI")
	if len(dataFor(port).ByURI) != 0 {
		t.Errorf("byURI = %v, want none for invalid input", dataFor(port).ByURI)
	}
}

func TestTrackURIAndHeaders(t *testing.T) {
	_, port := setup(t)
	rr := callHandler(trackURIAndHeaders, port, map[string]string{"uri": "/foo", "headers": "h1,h2"})
	expectStatus(t, rr, http.StatusOK)

	rtd := dataFor(port)
	if count, present := rtd.ByURI["/foo"]; !present || count != 0 {
		t.Errorf("byURI = %v, want /foo tracked with 0 calls", rtd.ByURI)
	}
	for _, h := range []string{"h1", "h2"} {
		if _, present := rtd.ByURIAndHeader["/foo"][h]; !present {
			t.Errorf("byURIAndHeader = %v, want header %s tracked for /foo", rtd.ByURIAndHeader, h)
		}
	}
	if !hookMatches(port, "/foo", map[string][]string{"h1": {"v"}}) {
		t.Error("a hook for the URI and headers must be registered on the port")
	}
}

func TestTrackURIAndHeadersWithMissingParams(t *testing.T) {
	_, port := setup(t)
	for name, vars := range map[string]map[string]string{
		"no params":    nil,
		"only uri":     {"uri": "/foo"},
		"only headers": {"headers": "h1"},
	} {
		t.Run(name, func(t *testing.T) {
			expectStatus(t, callHandler(trackURIAndHeaders, port, vars), http.StatusBadRequest)
		})
	}
	if len(dataFor(port).ByURI) != 0 {
		t.Errorf("byURI = %v, want none for invalid input", dataFor(port).ByURI)
	}
}

func TestTrackURIAndHeaderValue(t *testing.T) {
	router, port := setup(t)
	rr := call(router, "POST", "/track/foo=bar?uri=/x", port)
	expectStatus(t, rr, http.StatusOK)

	rtd := dataFor(port)
	if count, present := rtd.ByURI["/x"]; !present || count != 0 {
		t.Errorf("byURI = %v, want /x tracked with 0 calls", rtd.ByURI)
	}
	if _, present := rtd.ByURIAndHeader["/x"]["foo"]; !present {
		t.Errorf("byURIAndHeader = %v, want header foo tracked for /x", rtd.ByURIAndHeader)
	}
	if !hookMatches(port, "/x", map[string][]string{"foo": {"bar"}}) {
		t.Error("a hook for the URI must be registered on the port")
	}

	expectNotRouted(t, call(router, "POST", "/track/foo=bar", port))
}

func TestTrackURIAndHeaderValueWithMissingParams(t *testing.T) {
	_, port := setup(t)
	for name, vars := range map[string]map[string]string{
		"no params": nil,
		"no uri":    {"header": "foo", "value": "bar"},
		"no header": {"uri": "/x", "value": "bar"},
		"no value":  {"uri": "/x", "header": "foo"},
	} {
		t.Run(name, func(t *testing.T) {
			expectStatus(t, callHandler(trackURIAndHeaderValue, port, vars), http.StatusBadRequest)
		})
	}
	if len(dataFor(port).ByURI) != 0 {
		t.Errorf("byURI = %v, want none for invalid input", dataFor(port).ByURI)
	}
}

func TestTrackCallCountsHook(t *testing.T) {
	_, port := setup(t)
	Tracker.Init(port, "", "/foo", nil)
	if !trackCallCounts(port, "/foo", map[string][]string{"h": {"v"}}, nil) {
		t.Error("the hook listener must let the request continue")
	}
	if got := dataFor(port).ByURI["/foo"]; got != 1 {
		t.Errorf("byURI[/foo] = %d, want 1", got)
	}
}

func TestInitAndTrackCall(t *testing.T) {
	_, port := setup(t)
	Tracker.Init(port, "", "/foo", []string{"h1"})

	rtd := dataFor(port)
	if count, present := rtd.ByURI["/foo"]; !present || count != 0 {
		t.Errorf("byURI = %v, want /foo with 0 calls", rtd.ByURI)
	}
	if hd := rtd.ByURIAndHeader["/foo"]["h1"]; hd == nil || hd.RequestCountsByHeaderValue == nil {
		t.Errorf("byURIAndHeader = %v, want h1 initialized for /foo", rtd.ByURIAndHeader)
	}

	Tracker.TrackCall(port, "", "/foo", map[string][]string{"h1": {"a", "b"}, "h2": {"c"}})
	Tracker.TrackCall(port, "", "/foo", map[string][]string{"h1": {"a"}})
	Tracker.TrackCall(port, "", "/foo", nil)

	if rtd.ByURI["/foo"] != 3 {
		t.Errorf("byURI[/foo] = %d, want 3", rtd.ByURI["/foo"])
	}
	hd := rtd.ByURIAndHeader["/foo"]["h1"]
	if hd.RequestCountsByHeader != 3 || hd.RequestCountsByHeaderValue["a"] != 2 || hd.RequestCountsByHeaderValue["b"] != 1 {
		t.Errorf("h1 data = %+v, want 3 requests with a=2 b=1", hd)
	}
	if _, present := rtd.ByURIAndHeader["/foo"]["h2"]; present {
		t.Error("headers that were not initialized must not be tracked")
	}
	if len(rtd.ByHeader) != 0 {
		t.Errorf("byHeader = %v, want none when no header was added", rtd.ByHeader)
	}
}

func TestTrackCallForUntrackedURI(t *testing.T) {
	_, port := setup(t)
	Tracker.TrackCall(port, "", "/new", nil)
	if got := dataFor(port).ByURI["/new"]; got != 1 {
		t.Errorf("byURI[/new] = %d, want the call to be counted", got)
	}
}

// The tracked headers are counted by the middleware, for all requests. If the hooks of tracked URIs counted them too,
// the requests of such a URI that carry a tracked header would be counted twice.
func TestTrackCallLeavesTrackedHeadersToTheMiddleware(t *testing.T) {
	_, port := setup(t)
	Tracker.initHeaders(port, "", []string{"h1"})
	Tracker.TrackCall(port, "", "/foo", map[string][]string{"h1": {"a"}, "h2": {"b"}})
	Tracker.TrackCall(port, "", "/bar", map[string][]string{"h1": {"a", "c"}})

	if hd := dataFor(port).getHeaderData("h1"); hd.RequestCountsByHeader != 0 || len(hd.RequestCountsByHeaderValue) != 0 {
		t.Errorf("h1 data = %+v, want nothing counted by the hooks", hd)
	}
	if got := dataFor(port).ByURI; got["/foo"] != 1 || got["/bar"] != 1 {
		t.Errorf("byURI = %v, want the URIs to be counted", got)
	}
}

// Header names are canonicalized in requests (x-request-id is received as X-Request-Id), so a header must be found
// whatever the case of the name it was added with.
func TestTrackCallFindsHeadersWhateverTheCaseOfTheirNames(t *testing.T) {
	_, port := setup(t)
	Tracker.Init(port, "", "/foo", []string{"x-request-id", "Y"})
	Tracker.TrackCall(port, "", "/foo", http.Header{"X-Request-Id": {"a"}, "Y": {"b", "c"}})
	Tracker.TrackCall(port, "", "/foo", map[string][]string{"x-request-id": {"a"}})

	rtd := dataFor(port)
	if hd := rtd.ByURIAndHeader["/foo"]["x-request-id"]; hd.RequestCountsByHeader != 2 || hd.RequestCountsByHeaderValue["a"] != 2 {
		t.Errorf("x-request-id data = %+v, want 2 requests with a", hd)
	}
	if hd := rtd.ByURIAndHeader["/foo"]["Y"]; hd.RequestCountsByHeader != 2 || hd.RequestCountsByHeaderValue["b"] != 1 || hd.RequestCountsByHeaderValue["c"] != 1 {
		t.Errorf("Y data = %+v, want both values of the header", hd)
	}
}

func TestTrackingIsPerPortAndKey(t *testing.T) {
	_, port := setup(t)
	Tracker.Init(port, "", "/foo", nil)
	Tracker.Init(port+1, "", "/foo", nil)
	Tracker.Init(port, "key", "/foo", nil)

	Tracker.TrackCall(port, "", "/foo", nil)
	Tracker.TrackCall(port, "", "/foo", nil)
	Tracker.TrackCall(port+1, "", "/foo", nil)
	Tracker.TrackCall(port, "key", "/foo", nil)

	for _, tt := range []struct {
		port int
		key  string
		want int
	}{{port, "", 2}, {port + 1, "", 1}, {port, "key", 1}} {
		if got := Tracker.getPortRequestTrackingData(tt.port, tt.key).ByURI["/foo"]; got != tt.want {
			t.Errorf("byURI[/foo] for port %d key %q = %d, want %d", tt.port, tt.key, got, tt.want)
		}
	}
}

func TestTrack(t *testing.T) {
	_, port := setup(t)
	Tracker.Init(port, "", "/foo", []string{"h1"})
	Tracker.initHeaders(port, "", []string{"h1", "h3"})

	// Only the first matched value of a header is used.
	Track(port, "", "/foo", map[string][]string{"h1": {"a", "b"}, "h2": {"c"}, "h3": {}})
	Track(port, "", "/foo", map[string][]string{"h1": {"a"}})

	rtd := dataFor(port)
	if rtd.ByURI["/foo"] != 2 {
		t.Errorf("byURI[/foo] = %d, want 2", rtd.ByURI["/foo"])
	}
	byHeader := rtd.getHeaderData("h1")
	if byHeader.RequestCountsByHeader != 2 || byHeader.RequestCountsByHeaderValue["a"] != 2 || len(byHeader.RequestCountsByHeaderValue) != 1 {
		t.Errorf("byHeader[h1] = %+v, want 2 requests, all with a", byHeader)
	}
	byURI := rtd.ByURIAndHeader["/foo"]["h1"]
	if byURI.RequestCountsByHeader != 2 || byURI.RequestCountsByHeaderValue["a"] != 2 {
		t.Errorf("byURIAndHeader[/foo][h1] = %+v, want 2 requests, all with a", byURI)
	}
	if hd := rtd.getHeaderData("h3"); hd.RequestCountsByHeader != 0 {
		t.Errorf("byHeader[h3] = %+v, headers without values must not be counted", hd)
	}
	if _, present := rtd.ByURIAndHeader["/foo"]["h2"]; present {
		t.Error("untracked headers must not be added")
	}
}

func TestTrackCreatesURIEntry(t *testing.T) {
	_, port := setup(t)
	Track(port, "", "/new", map[string][]string{"h": {"v"}})
	rtd := dataFor(port)
	if rtd.ByURI["/new"] != 1 {
		t.Errorf("byURI[/new] = %d, want 1", rtd.ByURI["/new"])
	}
	if rtd.ByURIAndHeader["/new"] == nil {
		t.Error("byURIAndHeader[/new] must be created")
	}
}

func TestTrackUnexported(t *testing.T) {
	_, port := setup(t)
	Tracker.Init(port, "", "/foo", []string{"h1"})
	Tracker.initHeaders(port, "", []string{"h2"})
	rtd := dataFor(port)

	headers := http.Header{}
	headers.Add("H1", "a")
	headers.Add("H2", "b")
	headers.Add("H3", "c")
	track(portText(port), headers, "/foo", rtd)
	track(portText(port), headers, "/untracked", rtd)
	track(portText(port), nil, "/foo", nil) // must not panic

	// The URIs, and the headers tracked for them, are counted by the hooks of the tracked URIs.
	if rtd.ByURI["/foo"] != 0 {
		t.Errorf("byURI[/foo] = %d, want 0: URIs are not counted by the middleware", rtd.ByURI["/foo"])
	}
	if _, present := rtd.ByURI["/untracked"]; present {
		t.Error("calls to URIs that are not tracked must not create an entry")
	}
	if hd := rtd.ByURIAndHeader["/foo"]["h1"]; hd.RequestCountsByHeader != 0 {
		t.Errorf("byURIAndHeader[/foo][h1] = %+v, want 0: not counted by the middleware", hd)
	}
	// http.Header.Get canonicalizes the name, so tracked headers are found whatever their case.
	if hd := rtd.getHeaderData("h2"); hd.RequestCountsByHeader != 2 || hd.RequestCountsByHeaderValue["b"] != 2 {
		t.Errorf("byHeader[h2] = %+v, want 2 requests with b", hd)
	}
	if rtd.getHeaderData("h3") != nil {
		t.Error("untracked headers must not be added")
	}
}

func TestHeaderDataTrackRequest(t *testing.T) {
	hd := &HeaderData{}
	hd.init()
	hd.trackRequest("a")
	hd.trackRequest("a")
	hd.trackRequest("")
	if hd.RequestCountsByHeader != 3 {
		t.Errorf("RequestCountsByHeader = %d, want 3", hd.RequestCountsByHeader)
	}
	if !reflect.DeepEqual(hd.RequestCountsByHeaderValue, map[string]int{"a": 2}) {
		t.Errorf("RequestCountsByHeaderValue = %v, want only non empty values counted", hd.RequestCountsByHeaderValue)
	}
	hd.init()
	if len(hd.RequestCountsByHeaderValue) != 0 {
		t.Errorf("RequestCountsByHeaderValue = %v, want it reset by init", hd.RequestCountsByHeaderValue)
	}
}

func TestTrackingDataHeaders(t *testing.T) {
	rtd := &TrackingData{}
	rtd.init()
	if rtd.ByHeader == nil || rtd.ByURI == nil || rtd.ByURIAndHeader == nil {
		t.Fatal("init must initialize all maps")
	}

	rtd.initHeaders([]string{"h1", "h2"})
	if got := headerNames(rtd); !reflect.DeepEqual(got, []string{"h1", "h2"}) {
		t.Errorf("headers = %v, want [h1 h2]", got)
	}
	if rtd.getHeaderData("unknown") != nil {
		t.Error("getHeaderData must return nil for an untracked header")
	}

	if !rtd.removeHeader("h1") || rtd.removeHeader("h1") {
		t.Error("removeHeader must report whether the header was tracked")
	}
	if got := headerNames(rtd); !reflect.DeepEqual(got, []string{"h2"}) {
		t.Errorf("headers = %v, want [h2]", got)
	}

	rtd.getHeaderData("h2").trackRequest("v")
	rtd.clearCounts()
	if hd := rtd.getHeaderData("h2"); hd == nil || len(hd.RequestCountsByHeaderValue) != 0 {
		t.Errorf("h2 data = %+v, want it tracked with value counts cleared", hd)
	}

	rtd.initURI("/foo")
	if count, present := rtd.ByURI["/foo"]; !present || count != 0 || rtd.ByURIAndHeader["/foo"] == nil {
		t.Errorf("initURI: byURI=%v byURIAndHeader=%v", rtd.ByURI, rtd.ByURIAndHeader)
	}

	rtd.init()
	if len(rtd.ByHeader) != 0 || len(rtd.ByURI) != 0 || len(rtd.ByURIAndHeader) != 0 {
		t.Error("init must reset all tracking data")
	}
}

func TestRequestTrackingHelpers(t *testing.T) {
	_, port := setup(t)
	Tracker.initURI(port, "", "/foo")
	if _, present := dataFor(port).ByURI["/foo"]; !present {
		t.Error("initURI must start tracking the URI")
	}
	Tracker.initURIAndHeaders(port, "", "/bar", []string{"h"})
	if _, present := dataFor(port).ByURIAndHeader["/bar"]["h"]; !present {
		t.Error("initURIAndHeaders must start tracking the header for the URI")
	}
	Tracker.initHeaders(port, "", []string{"x"})
	if dataFor(port).getHeaderData("x") == nil {
		t.Error("initHeaders must start tracking the header")
	}
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

func headerRequestCount(hd *HeaderData) int {
	hd.lock.RLock()
	defer hd.lock.RUnlock()
	return hd.RequestCountsByHeader
}

func TestMiddlewareTracksRequests(t *testing.T) {
	_, port := setup(t)
	Tracker.initURI(port, "", "/foo")
	Tracker.initHeaders(port, "", []string{"X"})
	hd := dataFor(port).getHeaderData("X")

	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusAccepted)
	})
	rr := httptest.NewRecorder()
	Middleware.MiddlewareHandler(next).ServeHTTP(rr, newRequest("GET", "/foo", port, "X", "v1"))
	if !reached || rr.Code != http.StatusAccepted {
		t.Fatalf("reached=%v status=%d, the request must be served by the next handler", reached, rr.Code)
	}

	// Requests are counted asynchronously. The middleware counts the tracked headers. The URIs are counted by the
	// hooks that are registered for them, which run before the middleware.
	waitFor(t, "the request to be tracked", func() bool { return headerRequestCount(hd) == 1 })
	if got := dataFor(port).ByURI["/foo"]; got != 0 {
		t.Errorf("byURI[/foo] = %d, want 0: the middleware must not count URIs, or tracked URIs would be counted twice", got)
	}
	hd.lock.RLock()
	defer hd.lock.RUnlock()
	if hd.RequestCountsByHeaderValue["v1"] != 1 {
		t.Errorf("byHeader[X] = %+v, want 1 request with v1", hd)
	}
}

func TestMiddlewareSkipsKnownNonTraffic(t *testing.T) {
	_, port := setup(t)
	Tracker.initURI(port, "", "/server/request/track")
	Tracker.initURI(port, "", "/foo")
	Tracker.initHeaders(port, "", []string{"X"})
	hd := dataFor(port).getHeaderData("X")

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusAccepted) })
	handler := Middleware.MiddlewareHandler(next)
	adminReq := newRequest("GET", "/server/request/track", port, "X", "admin")
	if !util.GetRequestStore(adminReq).IsKnownNonTraffic {
		t.Fatal("admin uri must be treated as non traffic")
	}
	handler.ServeHTTP(httptest.NewRecorder(), adminReq)
	// A request that is tracked afterwards shows when tracking is done.
	handler.ServeHTTP(httptest.NewRecorder(), newRequest("GET", "/foo", port, "X", "traffic"))

	waitFor(t, "the traffic request to be tracked", func() bool { return headerRequestCount(hd) >= 1 })
	time.Sleep(20 * time.Millisecond) // give a wrongly tracked admin request the chance to show up
	hd.lock.RLock()
	defer hd.lock.RUnlock()
	if hd.RequestCountsByHeader != 1 || hd.RequestCountsByHeaderValue["admin"] != 0 {
		t.Errorf("byHeader[X] = %+v, want only the traffic request counted", hd)
	}
}

func TestMiddlewareWithNilNext(t *testing.T) {
	_, port := setup(t)
	Tracker.initURI(port, "", "/foo")
	Middleware.MiddlewareHandler(nil).ServeHTTP(httptest.NewRecorder(), newRequest("GET", "/foo", port))
	Middleware.MiddlewareHandler(nil).ServeHTTP(httptest.NewRecorder(), newRequest("GET", "/server/foo", port))
}
