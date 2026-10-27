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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"goto/pkg/global"
	"goto/pkg/server/hooks"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
)

// The hooks of a port are made with goroutines that never end, and can't be removed, so all the tests of the API share
// a port. They are told apart by their keys, URIs and header names, which are all made from the number of the test.
const hooksPort = 31001

var (
	stubOnce  sync.Once
	routeOnce sync.Once
	envSeq    atomic.Int32
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

type headerCounts struct {
	CountsByHeader      int            `json:"countsByHeader"`
	CountsByHeaderValue map[string]int `json:"countsByHeaderValue"`
}

type reportedData struct {
	ByURI             map[string]int                     `json:"byURI"`
	ByReqHeader       map[string]headerCounts            `json:"byRequestHeader"`
	ByURIAndReqHeader map[string]map[string]headerCounts `json:"byURIAndRequestHeader"`
	ByRespStatus      map[string]int                     `json:"byResponseStatus"`
}

type reported struct {
	KeyPortTracking map[string]map[string]reportedData `json:"keyPortTracking"`
}

type env struct {
	t         *testing.T
	n         int
	key       string
	uri       string
	hdr       string
	nextCalls atomic.Int32
	chain     http.Handler
}

func newEnv(t *testing.T) *env {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
	routeOnce.Do(func() { Middleware.SetRoutes(nil) })
	e := &env{t: t, n: int(envSeq.Add(1))}
	e.key = fmt.Sprintf("k%03d", e.n)
	e.uri = fmt.Sprintf("/n%03d/foo", e.n)
	e.hdr = fmt.Sprintf("x-n%03d", e.n)
	e.chain = hooks.Middleware.MiddlewareHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.nextCalls.Add(1)
	}))
	t.Cleanup(func() {
		Tracker.lock.Lock()
		delete(Tracker.KeyPort, e.key)
		Tracker.lock.Unlock()
	})
	return e
}

func (e *env) call(h http.Handler, method, target string, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Add(headers[i], headers[i+1])
	}
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, hooksPort))
	_, req, _ = util.WithRequestStore(req, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func (e *env) api(method, target string) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.call(middleware.RootRouters["/server"], method, target)
}

func (e *env) configure(method, target string) string {
	e.t.Helper()
	rr := e.api(method, target)
	if rr.Code != http.StatusOK {
		e.t.Fatalf("%s %s = %d %q, want 200", method, target, rr.Code, rr.Body.String())
	}
	return rr.Body.String()
}

// traffic sends a request through the hooks middleware, as the server does before its handlers.
func (e *env) traffic(path string, headers ...string) {
	e.call(e.chain, "GET", path, headers...)
}

func (e *env) data() reportedData {
	e.t.Helper()
	rr := e.api("GET", "/server/request/track/uri/counts?key="+e.key)
	if rr.Code != http.StatusOK {
		e.t.Fatalf("GET counts = %d %q, want 200", rr.Code, rr.Body.String())
	}
	var rep reported
	if err := json.Unmarshal(rr.Body.Bytes(), &rep); err != nil {
		e.t.Fatalf("counts %q are not JSON: %v", rr.Body.String(), err)
	}
	return rep.KeyPortTracking[e.key][fmt.Sprint(hooksPort)]
}

func TestTrackURIRejectsAnEmptyURIWithoutHeaders(t *testing.T) {
	e := newEnv(t)
	rr := e.api("POST", "/server/request/track/uri?key="+e.key+"&uri=")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
	if got := e.data(); len(got.ByURI) != 0 || len(got.ByReqHeader) != 0 {
		t.Errorf("tracking = %+v, want nothing tracked", got)
	}
}

func TestTrackURIAddsTheURI(t *testing.T) {
	e := newEnv(t)
	e.configure("POST", "/server/request/track/uri?key="+e.key+"&uri="+e.uri)
	got := e.data()
	if v, ok := got.ByURI[e.uri]; !ok || v != 0 {
		t.Errorf("byURI = %v, want %s tracked with 0", got.ByURI, e.uri)
	}
	if len(got.ByReqHeader) != 0 {
		t.Errorf("byRequestHeader = %v, want no headers", got.ByReqHeader)
	}
}

func TestTrackURIAndHeadersAddsBoth(t *testing.T) {
	e := newEnv(t)
	e.configure("POST", fmt.Sprintf("/server/request/track/uri/headers/%s-a,%s-b?key=%s&uri=%s", e.hdr, e.hdr, e.key, e.uri))
	got := e.data()
	for _, h := range []string{e.hdr + "-a", e.hdr + "-b"} {
		if _, ok := got.ByReqHeader[h]; !ok {
			t.Errorf("byRequestHeader = %v, want %s tracked", got.ByReqHeader, h)
		}
		if _, ok := got.ByURIAndReqHeader[e.uri][h]; !ok {
			t.Errorf("byURIAndRequestHeader = %v, want %s tracked for %s", got.ByURIAndReqHeader, h, e.uri)
		}
	}
}

func TestTrackHeadersAddsHeadersForAllURIs(t *testing.T) {
	e := newEnv(t)
	e.configure("POST", fmt.Sprintf("/server/request/track/headers/add/%s?key=%s", e.hdr, e.key))
	got := e.data()
	if _, ok := got.ByReqHeader[e.hdr]; !ok {
		t.Errorf("byRequestHeader = %v, want %s tracked", got.ByReqHeader, e.hdr)
	}
	if len(got.ByURI) != 0 {
		t.Errorf("byURI = %v, want no URIs", got.ByURI)
	}
}

func TestTrackHeadersAddWithoutHeadersIsNotRouted(t *testing.T) {
	e := newEnv(t)
	for _, target := range []string{
		"/server/request/track/headers/add/?key=" + e.key,
		"/server/request/track/headers/add/" + e.hdr,
	} {
		if rr := e.api("POST", target); rr.Code == http.StatusOK {
			t.Errorf("POST %s = 200, want it rejected", target)
		}
	}
}

func TestTrackURIAndHeaderValueAddsTheHeader(t *testing.T) {
	e := newEnv(t)
	e.configure("POST", fmt.Sprintf("/server/request/track/uri/headers/%s=blue?key=%s&uri=%s", e.hdr, e.key, e.uri))
	got := e.data()
	if _, ok := got.ByURIAndReqHeader[e.uri][e.hdr]; !ok {
		t.Errorf("byURIAndRequestHeader = %v, want %s tracked for %s", got.ByURIAndReqHeader, e.hdr, e.uri)
	}
}

func TestClearForgetsTheTrackingOfTheKey(t *testing.T) {
	e := newEnv(t)
	e.configure("POST", fmt.Sprintf("/server/request/track/uri/headers/%s?key=%s&uri=%s", e.hdr, e.key, e.uri))
	msg := e.configure("POST", "/server/request/track/clear?key="+e.key)
	if !strings.Contains(msg, "tracking cleared") || !strings.Contains(msg, e.key) {
		t.Errorf("message = %q, want it to say the tracking of the key is cleared", msg)
	}
	if got := e.data(); len(got.ByURI) != 0 || len(got.ByReqHeader) != 0 || len(got.ByURIAndReqHeader) != 0 {
		t.Errorf("tracking = %+v, want nothing tracked", got)
	}
}

func TestClearCountsResetsCountsOnly(t *testing.T) {
	e := newEnv(t)
	e.configure("POST", fmt.Sprintf("/server/request/track/uri/headers/%s?key=%s&uri=%s", e.hdr, e.key, e.uri))
	e.traffic(e.uri, e.hdr, "v")
	if got := e.data(); got.ByURI[e.uri] != 1 {
		t.Fatalf("byURI = %v, want %s counted once", got.ByURI, e.uri)
	}
	msg := e.configure("POST", "/server/request/track/clear/counts?key="+e.key)
	if !strings.Contains(msg, "counts cleared") {
		t.Errorf("message = %q, want it to say the counts are cleared", msg)
	}
	got := e.data()
	if v, ok := got.ByURI[e.uri]; !ok || v != 0 {
		t.Errorf("byURI = %v, want %s still tracked with 0", got.ByURI, e.uri)
	}
	if hc, ok := got.ByReqHeader[e.hdr]; !ok || hc.CountsByHeader != 0 || len(hc.CountsByHeaderValue) != 0 {
		t.Errorf("byRequestHeader = %+v, want %s still tracked with no counts", got.ByReqHeader, e.hdr)
	}
}

func TestGetCountsReportsPerKeyAndPort(t *testing.T) {
	e := newEnv(t)
	e.configure("POST", fmt.Sprintf("/server/request/track/uri?key=%s&uri=%s", e.key, e.uri))
	rr := e.api("GET", "/server/request/track/uri/counts?key="+e.key)
	var raw map[string]map[string]map[string]map[string]json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
		t.Fatalf("counts %q are not JSON: %v", rr.Body.String(), err)
	}
	data := raw["keyPortTracking"][e.key][fmt.Sprint(hooksPort)]
	if data == nil {
		t.Fatalf("counts %q, want them under keyPortTracking/%s/%d", rr.Body.String(), e.key, hooksPort)
	}
	for _, field := range []string{"byURI", "byRequestHeader", "byURIAndRequestHeader", "byResponseHeader", "byResponseStatus",
		"byURIAndResponseHeader", "byURIAndResponseStatus", "byResponseHeaderAndStatus", "byUpstreamURI", "byUpstreamRequestHeader",
		"byUpstreamResponseHeader", "byUpstreamResponseStatus", "byUpstreamURIAndRequestHeader", "byUpstreamURIAndResponseHeader",
		"byUpstreamURIAndResponseStatus", "byUpstreamResponseHeaderAndStatus"} {
		if _, ok := data[field]; !ok {
			t.Errorf("counts have no field %s", field)
		}
	}
	if rr := e.api("POST", "/server/request/track/uri/counts?key="+e.key); rr.Code == http.StatusOK {
		t.Error("counts must only be given for GET")
	}
}

// A request that has a tracked header is counted once for the URI of its hook and once for the header, however many
// hooks of the key match it, and whatever the case of the header names is.
func TestTrafficIsCountedOncePerRequest(t *testing.T) {
	e := newEnv(t)
	// A URI tracked with a header, and the same header tracked for all URIs, under one key: both hooks match requests
	// of the URI, and must not count them twice.
	e.configure("POST", fmt.Sprintf("/server/request/track/uri/headers/%s?key=%s&uri=%s", e.hdr, e.key, e.uri))
	e.configure("POST", fmt.Sprintf("/server/request/track/headers/add/%s?key=%s", e.hdr, e.key))
	const requests = 4
	for i := 0; i < requests; i++ {
		e.traffic(e.uri, e.hdr, "v1")
	}
	got := e.data()
	if got.ByURI[e.uri] != requests {
		t.Errorf("byURI[%s] = %d, want %d", e.uri, got.ByURI[e.uri], requests)
	}
	if hc := got.ByReqHeader[e.hdr]; hc.CountsByHeader != requests || hc.CountsByHeaderValue["v1"] != requests {
		t.Errorf("byRequestHeader[%s] = %+v, want %d requests of v1", e.hdr, hc, requests)
	}
	if hc := got.ByURIAndReqHeader[e.uri][e.hdr]; hc.CountsByHeader != requests || hc.CountsByHeaderValue["v1"] != requests {
		t.Errorf("byURIAndRequestHeader[%s][%s] = %+v, want %d requests of v1", e.uri, e.hdr, hc, requests)
	}
}

func TestTrafficOfAnotherKeyIsCountedSeparately(t *testing.T) {
	e1, e2 := newEnv(t), newEnv(t)
	e1.configure("POST", fmt.Sprintf("/server/request/track/headers/add/%s?key=%s", e1.hdr, e1.key))
	e2.configure("POST", fmt.Sprintf("/server/request/track/headers/add/%s?key=%s", e2.hdr, e2.key))
	for i := 0; i < 3; i++ {
		e1.traffic("/anything", e1.hdr, "a")
	}
	e2.traffic("/anything", e2.hdr, "b")
	if hc := e1.data().ByReqHeader[e1.hdr]; hc.CountsByHeader != 3 || hc.CountsByHeaderValue["a"] != 3 {
		t.Errorf("first key counts = %+v, want 3 of a", hc)
	}
	if hc := e2.data().ByReqHeader[e2.hdr]; hc.CountsByHeader != 1 || hc.CountsByHeaderValue["b"] != 1 {
		t.Errorf("second key counts = %+v, want 1 of b", hc)
	}
	if _, ok := e1.data().ByReqHeader[e2.hdr]; ok {
		t.Error("the headers of a key must not leak into another key")
	}
}

func TestTrafficCountsOnlyTheTrackedValueOfAHeader(t *testing.T) {
	e := newEnv(t)
	e.configure("POST", fmt.Sprintf("/server/request/track/uri/headers/%s=blue?key=%s&uri=%s", e.hdr, e.key, e.uri))
	e.traffic(e.uri, e.hdr, "blue")
	e.traffic(e.uri, e.hdr, "blue")
	e.traffic(e.uri, e.hdr, "red")
	hc := e.data().ByReqHeader[e.hdr]
	if hc.CountsByHeader != 2 || hc.CountsByHeaderValue["blue"] != 2 || len(hc.CountsByHeaderValue) != 1 {
		t.Errorf("byRequestHeader[%s] = %+v, want 2 requests of blue and none of red", e.hdr, hc)
	}
}

func TestTrafficOfOtherURIsIsNotCountedForAHeaderOfAnotherKey(t *testing.T) {
	e := newEnv(t)
	e.configure("POST", fmt.Sprintf("/server/request/track/uri/headers/%s?key=%s&uri=%s", e.hdr, e.key, e.uri))
	e.traffic("/unrelated/"+e.key, e.hdr, "v")
	if hc := e.data().ByReqHeader[e.hdr]; hc.CountsByHeader != 0 {
		t.Errorf("byRequestHeader[%s] = %+v, want requests of other URIs left uncounted", e.hdr, hc)
	}
}
