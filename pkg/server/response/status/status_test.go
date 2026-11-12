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

package status

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/server/intercept"
	"goto/pkg/server/request/uri"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
	stubOnce sync.Once
	// Statuses and counts are kept per port, so every test uses a port of its own.
	portSeq atomic.Int32
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

// testServer serves requests through the middlewares in the order that the server uses them: the URI middleware,
// which is outside of the status middleware, and then the status middleware. The handler answers 200 unless it is
// given a status.
type testServer struct {
	port    int
	api     *mux.Router
	handler http.Handler
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
	s := &testServer{port: int(40000 + portSeq.Add(1)), api: mux.NewRouter()}
	uri.Middleware.SetRoutes(s.api)
	Middleware.SetRoutes(s.api)
	s.handler = uri.Middleware.MiddlewareHandler(Middleware.MiddlewareHandler(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})))
	t.Cleanup(func() {
		statusLock.Lock()
		delete(portStatusMap, strconv.Itoa(s.port))
		statusLock.Unlock()
		TheStatusManager.Clear(s.port, "", "")
	})
	return s
}

func (s *testServer) newRequest(method, target string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, s.port))
	_, req, _ = util.WithRequestStore(req, nil)
	return req
}

// configure calls an API of the server, e.g. the ones that set a URI status or a response status.
func (s *testServer) configure(t *testing.T, method, target string) {
	t.Helper()
	rr := httptest.NewRecorder()
	s.api.ServeHTTP(rr, s.newRequest(method, target))
	if rr.Code != http.StatusOK {
		t.Fatalf("%s %s = %d %q, want 200", method, target, rr.Code, rr.Body.String())
	}
}

func (s *testServer) forceStatus(status int) {
	TheStatusManager.SetStatusFor(s.port, "", "", "", "", nil, []int{status}, -1, true)
}

type served struct {
	rr  *httptest.ResponseRecorder
	req *http.Request
	irw *intercept.InterceptResponseWriter
	w   http.ResponseWriter
}

func (s *testServer) prepare(target string) *served {
	p := &served{rr: httptest.NewRecorder(), req: s.newRequest("GET", target)}
	p.w, p.irw = intercept.WithIntercept(p.req, p.rr)
	return p
}

func (s *testServer) run(p *served) *served {
	s.handler.ServeHTTP(p.w, p.req)
	p.irw.Proceed()
	return p
}

func (s *testServer) serve(target string) *served { return s.run(s.prepare(target)) }

// counts returns the response status counts of the port, which is what /status/counts reports.
func (s *testServer) counts() map[int]int {
	statusLock.RLock()
	portStatus := portStatusMap[strconv.Itoa(s.port)]
	statusLock.RUnlock()
	counts := map[int]int{}
	if portStatus != nil {
		portStatus.lock.RLock()
		defer portStatus.lock.RUnlock()
		for status, count := range portStatus.countsByResponseStatus {
			counts[status] = count
		}
	}
	return counts
}

func (s *testServer) expectCounts(t *testing.T, want map[int]int) {
	t.Helper()
	if got := s.counts(); !reflect.DeepEqual(got, want) {
		t.Errorf("response status counts = %v, want %v", got, want)
	}
}

func expectCodes(t *testing.T, got []int, want ...int) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("response statuses = %v, want %v", got, want)
	}
}

// The response status counts must have every response of the port, whether its status comes from a URI status, from
// a status that is set for all requests, or from the handler.

func TestResponsesWithoutStatusConfigAreCounted(t *testing.T) {
	s := newTestServer(t)
	var codes []int
	for i := 0; i < 3; i++ {
		codes = append(codes, s.serve("/plain").rr.Code)
	}
	expectCodes(t, codes, 200, 200, 200)
	s.expectCounts(t, map[int]int{200: 3})
}

func TestResponsesWithURIStatusAreCounted(t *testing.T) {
	s := newTestServer(t)
	s.configure(t, "POST", "/uri/set/status=418?uri=/plain")
	var codes []int
	for i := 0; i < 3; i++ {
		codes = append(codes, s.serve("/plain").rr.Code)
	}
	codes = append(codes, s.serve("/other").rr.Code)
	expectCodes(t, codes, 418, 418, 418, 200)
	s.expectCounts(t, map[int]int{418: 3, 200: 1})
}

func TestResponsesWithLimitedURIStatusAreCounted(t *testing.T) {
	s := newTestServer(t)
	s.configure(t, "POST", "/uri/set/status=418:2?uri=/plain")
	var codes []int
	for i := 0; i < 4; i++ {
		codes = append(codes, s.serve("/plain").rr.Code)
	}
	// The call that uses the status up is counted like the calls before it.
	expectCodes(t, codes, 418, 418, 200, 200)
	s.expectCounts(t, map[int]int{418: 2, 200: 2})
}

func TestResponsesWithURIStatusOfSeveralStatusesAreCounted(t *testing.T) {
	s := newTestServer(t)
	s.configure(t, "POST", "/uri/set/status=418,503?uri=/plain")
	for i := 0; i < 20; i++ {
		s.serve("/plain")
	}
	counts := s.counts()
	if counts[418]+counts[503] != 20 || len(counts) > 2 {
		t.Errorf("response status counts = %v, want the 20 responses counted under 418 and 503", counts)
	}
}

func TestResponsesWithURIStatusUnderGlobAreCounted(t *testing.T) {
	s := newTestServer(t)
	s.configure(t, "POST", "/uri/set/status=418?uri=/glob*")
	for _, path := range []string{"/glob", "/glob/a", "/glob/b/c", "/other"} {
		s.serve(path)
	}
	s.expectCounts(t, map[int]int{418: 3, 200: 1})
}

// A status that is set for all requests does not apply to a request that has a URI status, but both kinds of
// responses are counted.
func TestForcedStatusAndURIStatusAreBothCounted(t *testing.T) {
	s := newTestServer(t)
	s.forceStatus(503)
	s.configure(t, "POST", "/uri/set/status=418?uri=/plain")
	var codes []int
	var forcedHeaders []string
	for _, path := range []string{"/plain", "/plain", "/other", "/other"} {
		p := s.serve(path)
		codes = append(codes, p.rr.Code)
		forcedHeaders = append(forcedHeaders, p.rr.Header().Get(constants.HeaderGotoForcedStatus))
	}
	expectCodes(t, codes, 418, 418, 503, 503)
	if !reflect.DeepEqual(forcedHeaders, []string{"", "", "503", "503"}) {
		t.Errorf("%s headers = %q, want it on the responses that got the forced status only", constants.HeaderGotoForcedStatus, forcedHeaders)
	}
	s.expectCounts(t, map[int]int{418: 2, 503: 2})
}

func TestForcedStatusIsNotUsedUpByRequestsWithURIStatus(t *testing.T) {
	s := newTestServer(t)
	TheStatusManager.SetStatusFor(s.port, "", "", "", "", nil, []int{503}, 2, true)
	s.configure(t, "POST", "/uri/set/status=418?uri=/plain")
	for i := 0; i < 5; i++ {
		s.serve("/plain")
	}
	var codes []int
	for i := 0; i < 3; i++ {
		codes = append(codes, s.serve("/other").rr.Code)
	}
	expectCodes(t, codes, 503, 503, 200)
	s.expectCounts(t, map[int]int{418: 5, 503: 2, 200: 1})
}

func TestConcurrentResponsesAreCountedWhateverConfiguresTheirStatus(t *testing.T) {
	const workers, rounds = 8, 100
	s := newTestServer(t)
	s.configure(t, "POST", "/uri/set/status=418?uri=/plain")
	s.configure(t, "POST", "/uri/set/status=409:100?uri=/limited")
	s.forceStatus(503)

	paths := []string{"/plain", "/limited", "/other"}
	reqs := make([][]*served, workers)
	for w := range reqs {
		reqs[w] = make([]*served, rounds)
		for i := range reqs[w] {
			reqs[w][i] = s.prepare(paths[(w+i)%len(paths)])
		}
	}
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for _, p := range reqs[w] {
				s.run(p)
			}
		}(w)
	}
	wg.Wait()

	// 800 requests, spread over the three URIs. The limited status is used up by the requests of its URI that get
	// there first, and the rest of them get the status that is set for all requests, like the ones to the other URI.
	byPath := map[string]int{}
	for w := range reqs {
		for i := range reqs[w] {
			byPath[paths[(w+i)%len(paths)]]++
		}
	}
	limited := byPath["/limited"]
	if limited < 100 {
		t.Fatalf("test setup: %d requests of the limited URI, want enough to use its status up", limited)
	}
	s.expectCounts(t, map[int]int{418: byPath["/plain"], 409: 100, 503: byPath["/other"] + limited - 100})
}

// The response status API sets a status for an exact URI, for the URIs under a prefix, or for all requests. The URI
// status API sets one for a URI too. All of them are counted, once per response.
func TestResponsesAreCountedWhateverWayTheirStatusIsConfigured(t *testing.T) {
	s := newTestServer(t)
	s.configure(t, "POST", "/status/set/503?uri=/exact")
	s.configure(t, "POST", "/status/set/502?uriPrefix=/pre")
	s.configure(t, "POST", "/status/set/429:1?uri=/once")
	s.configure(t, "POST", "/uri/set/status=409?uri=/viauri")
	s.configure(t, "POST", "/status/set/500")

	var codes []int
	for _, path := range []string{"/exact", "/exact", "/pre/a", "/pre/b/c", "/once", "/once", "/viauri", "/viauri", "/plain", "/plain", "/plain"} {
		codes = append(codes, s.serve(path).rr.Code)
	}
	// The status of /once is used up by its first request, and the one that is set for all requests applies after it.
	expectCodes(t, codes, 503, 503, 502, 502, 429, 500, 409, 409, 500, 500, 500)
	s.expectCounts(t, map[int]int{503: 2, 502: 2, 429: 1, 409: 2, 500: 4})
}

// A status that was set for a URI, or a prefix, and is used up must not keep the statuses that are set after it from
// applying to that URI. Used up, it is as if it was not set.
func TestUsedUpStatusDoesNotShadowLaterStatuses(t *testing.T) {
	stubOnce.Do(stubGlobalFuncs)
	port := int(40000 + portSeq.Add(1))
	t.Cleanup(func() { TheStatusManager.Clear(port, "", "") })
	TheStatusManager.SetStatusFor(port, "/once", "", "", "", nil, []int{429}, 1, true)
	TheStatusManager.SetStatusFor(port, "", "/pre", "", "", nil, []int{502}, 2, true)
	TheStatusManager.SetStatusFor(port, "", "", "", "", nil, []int{500}, -1, true)

	for _, tt := range []struct {
		uri        string
		wantStatus int
		wantTimes  int
	}{
		{"/once", 429, 0},
		{"/once", 500, -1}, // used up, so the status for all requests applies
		{"/once", 500, -1},
		{"/pre/a", 502, 1},
		{"/pre/b", 502, 0},
		{"/pre/c", 500, -1},
		{"/other", 500, -1},
	} {
		if status, times := TheStatusManager.GetStatusFor(port, tt.uri, nil, "GET"); status != tt.wantStatus || times != tt.wantTimes {
			t.Errorf("GetStatusFor(%s) = (%d, %d), want (%d, %d)", tt.uri, status, times, tt.wantStatus, tt.wantTimes)
		}
	}
}

func TestUsedUpStatusLeavesRequestsWithoutAnyOtherStatusAlone(t *testing.T) {
	stubOnce.Do(stubGlobalFuncs)
	port := int(40000 + portSeq.Add(1))
	t.Cleanup(func() { TheStatusManager.Clear(port, "", "") })
	TheStatusManager.SetStatusFor(port, "/once", "", "", "", nil, []int{429}, 1, true)

	if status, _ := TheStatusManager.GetStatusFor(port, "/once", nil, "GET"); status != 429 {
		t.Fatalf("first use = %d, want 429", status)
	}
	if status, times := TheStatusManager.GetStatusFor(port, "/once", nil, "GET"); status != 0 || times != 0 {
		t.Errorf("after it was used up = (%d, %d), want no status", status, times)
	}
}

// A status that was set to 0 for a URI still matches its requests, so the status that is set for all requests does not
// apply to them. That is not the same as a status that is used up, which is passed over.
func TestURIThatWasSetToStatusZeroKeepsMatchingItsRequests(t *testing.T) {
	s := newTestServer(t)
	s.configure(t, "POST", "/status/set/0?uri=/exempt")
	s.configure(t, "POST", "/status/set/500")
	var codes []int
	for _, path := range []string{"/exempt", "/exempt", "/plain"} {
		codes = append(codes, s.serve(path).rr.Code)
	}
	expectCodes(t, codes, 200, 200, 500)
	s.expectCounts(t, map[int]int{200: 2, 500: 1})
}

func TestResponsesAreCountedAfterAStatusIsUsedUp(t *testing.T) {
	s := newTestServer(t)
	s.configure(t, "POST", "/status/set/502:2?uriPrefix=/pre")
	s.configure(t, "POST", "/status/set/500")
	var codes []int
	for _, path := range []string{"/pre/a", "/pre/b", "/pre/c", "/pre/d", "/other"} {
		codes = append(codes, s.serve(path).rr.Code)
	}
	expectCodes(t, codes, 502, 502, 500, 500, 500)
	s.expectCounts(t, map[int]int{502: 2, 500: 3})
}

// Every use of a status is counted once, however many requests ask for it at the same time, and the requests that
// come after it was used up get the status that follows.
func TestConcurrentUseOfALimitedStatusIsExact(t *testing.T) {
	const workers, rounds, limit = 8, 200, 300
	stubOnce.Do(stubGlobalFuncs)
	port := int(40000 + portSeq.Add(1))
	t.Cleanup(func() { TheStatusManager.Clear(port, "", "") })
	TheStatusManager.SetStatusFor(port, "/limited", "", "", "", nil, []int{429}, limit, true)
	TheStatusManager.SetStatusFor(port, "", "", "", "", nil, []int{500}, -1, true)

	var limited, fallback, unexpected atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				switch status, _ := TheStatusManager.GetStatusFor(port, "/limited", nil, "GET"); status {
				case 429:
					limited.Add(1)
				case 500:
					fallback.Add(1)
				default:
					unexpected.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if limited.Load() != limit || fallback.Load() != workers*rounds-limit || unexpected.Load() != 0 {
		t.Errorf("limited status used %d times, status for all requests %d times, other %d times; want %d, %d, 0",
			limited.Load(), fallback.Load(), unexpected.Load(), limit, workers*rounds-limit)
	}
}

// Statuses are set and cleared while requests are using them.
func TestConcurrentUseWhileStatusesChange(t *testing.T) {
	const workers, rounds = 8, 300
	stubOnce.Do(stubGlobalFuncs)
	port := int(40000 + portSeq.Add(1))
	t.Cleanup(func() { TheStatusManager.Clear(port, "", "") })

	var unexpected atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			uri := "/uri" + strconv.Itoa(w%3)
			for i := 0; i < rounds; i++ {
				switch status, _ := TheStatusManager.GetStatusFor(port, uri, nil, "GET"); status {
				case 0, 429, 500, 502:
				default:
					unexpected.Add(1)
				}
			}
		}(w)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			uri := "/uri" + strconv.Itoa(i%3)
			TheStatusManager.SetStatusFor(port, uri, "", "", "", nil, []int{429}, 1+i%5, true)
			TheStatusManager.SetStatusFor(port, "", "/uri", "", "", nil, []int{502}, -1, true)
			TheStatusManager.SetStatusFor(port, "", "", "", "", nil, []int{500}, -1, true)
			TheStatusManager.SetStatusFor(port, uri, "", "", "", nil, []int{0}, -1, true)
			if i%7 == 0 {
				TheStatusManager.Clear(port, uri, "")
			}
		}
	}()
	wg.Wait()
	if unexpected.Load() != 0 {
		t.Errorf("%d requests got a status that was never set", unexpected.Load())
	}
}

// The APIs that report the statuses, or that return what they have just set, run while requests use the statuses and
// count them down, and while the first request of a port creates the counts of the port.
func TestReportingStatusesWhileTheyAreUsed(t *testing.T) {
	const servers, rounds = 4, 150
	var all []*testServer
	for i := 0; i < servers; i++ {
		s := newTestServer(t)
		s.configure(t, "POST", "/status/set/502:100000?uriPrefix=/pre")
		s.configure(t, "POST", "/status/set/500:100000")
		all = append(all, s)
	}
	admin := all[0]

	traffic := make([][]*served, servers)
	for i, s := range all {
		for j := 0; j < rounds; j++ {
			path := "/pre/a"
			if j%2 == 1 {
				path = "/other"
			}
			traffic[i] = append(traffic[i], s.prepare(path))
		}
	}
	type call struct {
		method, target, body string
	}
	calls := []call{
		{"GET", "/status", ""},
		{"POST", "/status/set/502:100000?uriPrefix=/pre", ""},
		{"POST", "/status/set/429:100000?uri=/exact", ""},
		{"POST", "/status/configure", `{"port":` + strconv.Itoa(admin.port) + `,"statuses":[503],"times":100000,"match":{"uri":{"exact":"/cfg"}}}`},
	}
	adminReqs := make([][]*http.Request, len(calls))
	for i, c := range calls {
		for j := 0; j < rounds; j++ {
			req := httptest.NewRequest(c.method, c.target, strings.NewReader(c.body))
			req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, admin.port))
			_, req, _ = util.WithRequestStore(req, nil)
			adminReqs[i] = append(adminReqs[i], req)
		}
	}

	var wg sync.WaitGroup
	for i, s := range all {
		wg.Add(1)
		go func(s *testServer, reqs []*served) {
			defer wg.Done()
			for _, p := range reqs {
				s.run(p)
			}
		}(s, traffic[i])
	}
	wg.Add(1)
	var badReports atomic.Int64
	go func() {
		defer wg.Done()
		for j := 0; j < rounds; j++ {
			for i := range calls {
				rr := httptest.NewRecorder()
				admin.api.ServeHTTP(rr, adminReqs[i][j])
				if rr.Code != http.StatusOK || rr.Body.Len() == 0 {
					badReports.Add(1)
				}
			}
		}
	}()
	wg.Wait()
	if badReports.Load() != 0 {
		t.Errorf("%d calls of the status APIs failed or reported nothing", badReports.Load())
	}
}

// What the manager returns, or reports, is a copy: it does not change as requests use the statuses, which would
// have the readers race with them.
func TestReturnedStatusConfigsAreSnapshots(t *testing.T) {
	stubOnce.Do(stubGlobalFuncs)
	port := int(40000 + portSeq.Add(1))
	t.Cleanup(func() { TheStatusManager.Clear(port, "", "") })

	set := TheStatusManager.SetStatusFor(port, "/foo", "", "", "", nil, []int{429}, 3, true)
	TheStatusManager.GetStatusFor(port, "/foo", nil, "GET")
	TheStatusManager.GetStatusFor(port, "/foo", nil, "GET")
	if set.Times != 3 {
		t.Errorf("config returned by SetStatusFor has Times %d, want 3 as it was set", set.Times)
	}
	snapshot := TheStatusManager.Snapshot()[port]
	if len(snapshot) != 1 || snapshot[0].Times != 1 {
		t.Fatalf("snapshot = %+v, want the one config with 1 use left", snapshot)
	}
	TheStatusManager.GetStatusFor(port, "/foo", nil, "GET")
	if snapshot[0].Times != 1 {
		t.Errorf("snapshot has Times %d after a use, want it to stay 1", snapshot[0].Times)
	}
	if status, _ := TheStatusManager.GetStatusFor(port, "/foo", nil, "GET"); status != 0 {
		t.Errorf("status = %d after all uses, want none", status)
	}

	config := &StatusConfig{Statuses: []int{418}, Times: 2, Match: &StatusMatch{URIMatch: &StatusURIMatch{Exact: "/cfg"}}}
	if err := TheStatusManager.ApplyStatusConfig(port, config); err != nil {
		t.Fatal(err)
	}
	if status, _ := TheStatusManager.GetStatusFor(port, "/cfg", nil, "GET"); status != 418 {
		t.Errorf("status of the applied config = %d, want 418", status)
	}
	if config.Times != 2 {
		t.Errorf("config that was applied has Times %d, want it to be left as it was given: the manager keeps a copy", config.Times)
	}
}

func TestSnapshotKeepsTheShapeOfTheReport(t *testing.T) {
	stubOnce.Do(stubGlobalFuncs)
	port := int(40000 + portSeq.Add(1))
	t.Cleanup(func() { TheStatusManager.Clear(port, "", "") })
	// Setting a status to 0 for a config that exists empties its statuses.
	TheStatusManager.SetStatusFor(port, "/foo", "", "", "", nil, []int{429}, -1, true)
	TheStatusManager.SetStatusFor(port, "/foo", "", "", "", nil, []int{0}, -1, true)
	TheStatusManager.SetStatusFor(port, "/bar", "", "", "", nil, []int{418, 503}, 2, true)

	data, err := json.Marshal(TheStatusManager.Snapshot()[port])
	if err != nil {
		t.Fatal(err)
	}
	report := string(data)
	for _, want := range []string{`"statuses":[]`, `"statuses":[418,503]`, `"times":2`, `"exact":"/foo"`} {
		if !strings.Contains(report, want) {
			t.Errorf("report %s does not contain %s", report, want)
		}
	}
}

// The flipflop configs of a port are reported while requests change them.
func TestReportingFlipflopConfigsWhileTheyAreUsed(t *testing.T) {
	const workers, rounds = 4, 200
	s := newTestServer(t)
	newAPIRequest := func(target string) *http.Request {
		req := httptest.NewRequest("GET", target, nil)
		req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, s.port))
		_, req, _ = util.WithRequestStore(req, nil)
		return req
	}
	// The first request creates the port's status, and the flipflop config of its request id.
	s.api.ServeHTTP(httptest.NewRecorder(), newAPIRequest("/status/418:100000"))

	uses := make([][]*http.Request, workers)
	for w := range uses {
		for i := 0; i < rounds; i++ {
			uses[w] = append(uses[w], newAPIRequest("/status/418:100000"))
		}
	}
	reports := make([]*http.Request, rounds)
	for i := range reports {
		reports[i] = newAPIRequest("/status/flipflop")
	}

	var badReports atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(reqs []*http.Request) {
			defer wg.Done()
			for _, req := range reqs {
				s.api.ServeHTTP(httptest.NewRecorder(), req)
			}
		}(uses[w])
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for _, req := range reports {
			rr := httptest.NewRecorder()
			s.api.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "statusCount") {
				badReports.Add(1)
			}
		}
	}()
	wg.Wait()
	if badReports.Load() != 0 {
		t.Errorf("%d reports of the flipflop configs failed or were empty", badReports.Load())
	}
}

// A status that was set to 0 for a URI keeps the status that is set for all requests from applying to it, and that
// must not depend on which of them was set first.
func TestURIThatWasSetToStatusZeroIsExemptWhateverTheOrder(t *testing.T) {
	for name, order := range map[string][]string{
		"exempt first": {"/status/set/0?uri=/exempt", "/status/set/500"},
		"exempt last":  {"/status/set/500", "/status/set/0?uri=/exempt"},
	} {
		t.Run(name, func(t *testing.T) {
			s := newTestServer(t)
			for _, target := range order {
				s.configure(t, "POST", target)
			}
			var codes []int
			for _, path := range []string{"/exempt", "/exempt", "/plain", "/plain"} {
				codes = append(codes, s.serve(path).rr.Code)
			}
			expectCodes(t, codes, 200, 200, 500, 500)
			s.expectCounts(t, map[int]int{200: 2, 500: 2})
		})
	}
}

// A status that is set for a URI applies to it, whether it was set before or after the status for all requests.
func TestStatusOfURIAppliesWhateverTheOrderOfStatusForAllRequests(t *testing.T) {
	for name, order := range map[string][]string{
		"uri first": {"/status/set/418?uri=/foo", "/status/set/500"},
		"uri last":  {"/status/set/500", "/status/set/418?uri=/foo"},
	} {
		t.Run(name, func(t *testing.T) {
			s := newTestServer(t)
			for _, target := range order {
				s.configure(t, "POST", target)
			}
			var codes []int
			for _, path := range []string{"/foo", "/bar", "/foo"} {
				codes = append(codes, s.serve(path).rr.Code)
			}
			expectCodes(t, codes, 418, 500, 418)
			s.expectCounts(t, map[int]int{418: 2, 500: 1})
		})
	}
}

// Setting the status for all requests to 0 puts back the requests to respond normally, and the status ahead of it that
// was set for a URI must not be kept from applying by what is left of the status for all requests.
func TestStatusZeroForAllRequestsDoesNotShadowLaterStatuses(t *testing.T) {
	s := newTestServer(t)
	s.configure(t, "POST", "/status/set/500")
	s.configure(t, "POST", "/status/set/0")
	s.configure(t, "POST", "/status/set/418?uri=/foo")
	var codes []int
	for _, path := range []string{"/foo", "/bar", "/foo"} {
		codes = append(codes, s.serve(path).rr.Code)
	}
	expectCodes(t, codes, 418, 200, 418)
	s.expectCounts(t, map[int]int{418: 2, 200: 1})

	// A status for all requests that is set again applies as it did.
	s.configure(t, "POST", "/status/set/503")
	codes = nil
	for _, path := range []string{"/foo", "/bar"} {
		codes = append(codes, s.serve(path).rr.Code)
	}
	expectCodes(t, codes, 418, 503)
}

// Statuses of more specific requests keep the order that they were set in, and the first that was set applies.
func TestStatusesOfURIsKeepTheOrderTheyWereSetIn(t *testing.T) {
	stubOnce.Do(stubGlobalFuncs)
	port := int(40000 + portSeq.Add(1))
	t.Cleanup(func() { TheStatusManager.Clear(port, "", "") })
	TheStatusManager.SetStatusFor(port, "", "/api", "", "", nil, []int{503}, -1, true)
	TheStatusManager.SetStatusFor(port, "/api/x", "", "", "", nil, []int{418}, -1, true)
	if status, _ := TheStatusManager.GetStatusFor(port, "/api/x", nil, "GET"); status != 503 {
		t.Errorf("/api/x = %d, want 503 from the prefix that was set first", status)
	}
	if status, _ := TheStatusManager.GetStatusFor(port, "/api/y", nil, "GET"); status != 503 {
		t.Errorf("/api/y = %d, want 503", status)
	}
}

func TestPeekingAtStatusDoesNotUseItUp(t *testing.T) {
	stubOnce.Do(stubGlobalFuncs)
	port := int(40000 + portSeq.Add(1))
	t.Cleanup(func() { TheStatusManager.Clear(port, "", "") })
	TheStatusManager.SetStatusFor(port, "/limited", "", "", "", nil, []int{503}, 2, true)
	for i := 0; i < 10; i++ {
		if status, times := TheStatusManager.PeekStatusFor(port, "/limited", nil, "GET"); status != 503 || times != 2 {
			t.Fatalf("peek %d = (%d, %d), want (503, 2)", i, status, times)
		}
		if !TheStatusManager.HasStatusFor(port, "/limited", nil, "GET") {
			t.Fatalf("peek %d: status not reported as forced", i)
		}
	}
	if TheStatusManager.HasStatusFor(port, "/other", nil, "GET") {
		t.Errorf("status reported as forced for a URI that has none")
	}
	if status, times := TheStatusManager.GetStatusFor(port, "/limited", nil, "GET"); status != 503 || times != 1 {
		t.Errorf("first use = (%d, %d), want (503, 1)", status, times)
	}
	if status, times := TheStatusManager.GetStatusFor(port, "/limited", nil, "GET"); status != 503 || times != 0 {
		t.Errorf("second use = (%d, %d), want (503, 0)", status, times)
	}
	if TheStatusManager.HasStatusFor(port, "/limited", nil, "GET") {
		t.Errorf("status reported as forced after it was used up")
	}
}

func TestIsForcedStatusDoesNotUseUpTheStatus(t *testing.T) {
	for name, tt := range map[string]struct {
		set        string
		wantCodes  []int
		wantForced []bool
	}{
		"limited":   {"/status/set/503:2", []int{503, 503, 200}, []bool{true, true, false}},
		"last one":  {"/status/set/503:1", []int{503, 200, 200}, []bool{true, false, false}},
		"unlimited": {"/status/set/503", []int{503, 503, 503}, []bool{true, true, true}},
	} {
		t.Run(name, func(t *testing.T) {
			s := newTestServer(t)
			s.configure(t, "POST", tt.set)
			var codes []int
			var forced []bool
			for range tt.wantCodes {
				p := s.prepare("/foo")
				// What the proxies do before they decide to send the request on.
				forced = append(forced, IsForcedStatus(p.req), IsForcedStatus(p.req))
				codes = append(codes, s.run(p).rr.Code)
			}
			for i, want := range tt.wantForced {
				if forced[2*i] != want || forced[2*i+1] != want {
					t.Errorf("request %d: IsForcedStatus = %v, %v, want %v", i, forced[2*i], forced[2*i+1], want)
				}
			}
			expectCodes(t, codes, tt.wantCodes...)
		})
	}
}

// The /status/{code} URI reports the status that was asked for unless a status is forced, and it must not use up a
// forced status by checking for it.
func TestStatusURIDoesNotUseUpForcedStatus(t *testing.T) {
	s := newTestServer(t)
	s.configure(t, "POST", "/status/set/503:2")
	handler := uri.Middleware.MiddlewareHandler(Middleware.MiddlewareHandler(s.api))
	var codes []int
	for i := 0; i < 4; i++ {
		p := s.prepare("/status/200")
		handler.ServeHTTP(p.w, p.req)
		p.irw.Proceed()
		codes = append(codes, p.rr.Code)
	}
	expectCodes(t, codes, 503, 503, 200, 200)
}

func applyConfig(t *testing.T, port int, body string) {
	t.Helper()
	if _, err := TheStatusManager.ParseAndApplyStatusConfig(port, strings.NewReader(body)); err != nil {
		t.Fatalf("applying %s: %v", body, err)
	}
}

func TestStatusConfigOfMethodsAppliesToThoseMethodsOnly(t *testing.T) {
	stubOnce.Do(stubGlobalFuncs)
	port := int(40000 + portSeq.Add(1))
	t.Cleanup(func() { TheStatusManager.Clear(port, "", "") })
	applyConfig(t, port, `{"statuses":[503],"match":{"methods":["post","PUT"]}}`)
	for _, tt := range []struct {
		method string
		want   int
	}{
		{"POST", 503}, {"put", 503}, {"GET", 0}, {"DELETE", 0},
		{"", 503}, // the method is not known, e.g. for gRPC and for reporting
	} {
		if status, _ := TheStatusManager.GetStatusFor(port, "/foo", nil, tt.method); status != tt.want {
			t.Errorf("method %q = %d, want %d", tt.method, status, tt.want)
		}
	}
}

func TestStatusConfigOfURIRegex(t *testing.T) {
	stubOnce.Do(stubGlobalFuncs)
	port := int(40000 + portSeq.Add(1))
	t.Cleanup(func() { TheStatusManager.Clear(port, "", "") })
	applyConfig(t, port, `{"statuses":[503],"match":{"uri":{"regex":"^/API/v[0-9]+/"}}}`)
	applyConfig(t, port, `{"statuses":[418],"match":{"uri":{"regex":"^/internal/","not":true}}}`)
	for uri, want := range map[string]int{
		"/api/v2/users": 503, // matched in any case
		"/Api/V10/x":    503,
		"/api/users":    418,
		"/internal/x":   0,
	} {
		if status, _ := TheStatusManager.GetStatusFor(port, uri, nil, "GET"); status != want {
			t.Errorf("%s = %d, want %d", uri, status, want)
		}
	}
	if _, err := TheStatusManager.ParseAndApplyStatusConfig(port, strings.NewReader(`{"statuses":[503],"match":{"uri":{"regex":"("}}}`)); err == nil {
		t.Errorf("invalid regex was accepted")
	}
}

// Clearing the status of a URI removes whatever was set for it, however it was set: for the exact URI or as a prefix,
// and with or without a header or a method to match.
func TestClearStatusOfURIRemovesWhatWasSetForIt(t *testing.T) {
	for name, set := range map[string][]string{
		"exact uri":        {"/status/set/503?uri=/foo"},
		"uri prefix":       {"/status/set/503?uriPrefix=/foo"},
		"exact and prefix": {"/status/set/503?uri=/foo&uriPrefix=/foo"},
		"twice":            {"/status/set/503?uri=/foo", "/status/set/503?uriPrefix=/foo"},
	} {
		t.Run(name, func(t *testing.T) {
			s := newTestServer(t)
			for _, target := range set {
				s.configure(t, "POST", target)
			}
			s.configure(t, "POST", "/status/set/418?uri=/other")
			if code := s.serve("/foo").rr.Code; code != 503 {
				t.Fatalf("before clear /foo = %d, want 503", code)
			}
			s.configure(t, "POST", "/status/clear?uri=/foo")
			if code := s.serve("/foo").rr.Code; code != 200 {
				t.Errorf("after clear /foo = %d, want 200", code)
			}
			if code := s.serve("/other").rr.Code; code != 418 {
				t.Errorf("status of another URI = %d after clear, want it kept at 418", code)
			}
		})
	}
}

func TestClearStatusOfURIRemovesConfigsThatMatchHeaders(t *testing.T) {
	stubOnce.Do(stubGlobalFuncs)
	port := int(40000 + portSeq.Add(1))
	t.Cleanup(func() { TheStatusManager.Clear(port, "", "") })
	TheStatusManager.SetStatusFor(port, "/svc", "/svc", "x-env", "dev", []string{"POST"}, []int{503}, -1, true)
	TheStatusManager.SetStatusFor(port, "/svc", "/svc", "x-env", "", []string{"POST"}, []int{502}, -1, true)
	TheStatusManager.SetStatusFor(port, "/svc", "/svc", "x-env", "", []string{"POST"}, []int{504}, -1, false)
	TheStatusManager.SetStatusFor(port, "/svc", "/svc", "", "", []string{"POST"}, []int{500}, -1, true)
	TheStatusManager.SetStatusFor(port, "/svc2", "/svc2", "x-env", "dev", nil, []int{501}, -1, true)

	TheStatusManager.Clear(port, "/svc", "/svc")
	if status, _ := TheStatusManager.GetStatusFor(port, "/svc", map[string][]string{"X-Env": {"dev"}}, "POST"); status != 0 {
		t.Errorf("status of the cleared URI = %d, want none", status)
	}
	if status, _ := TheStatusManager.GetStatusFor(port, "/svc", nil, "POST"); status != 0 {
		t.Errorf("status of the cleared URI without the header = %d, want none", status)
	}
	if status, _ := TheStatusManager.GetStatusFor(port, "/svc2", map[string][]string{"X-Env": {"dev"}}, "GET"); status != 501 {
		t.Errorf("status of another URI = %d, want it kept at 501", status)
	}
}

func TestParseMethods(t *testing.T) {
	for name, tt := range map[string]struct {
		param string
		def   []string
		want  []string
		ok    bool
	}{
		"empty uses the default":       {"", []string{"POST"}, []string{"POST"}, true},
		"blank uses the default":       {"  ", []string{"POST"}, []string{"POST"}, true},
		"empty without a default":      {"", nil, nil, true},
		"one":                          {"get", []string{"POST"}, []string{"GET"}, true},
		"several in any case":          {"get, Post ,DELETE", []string{"POST"}, []string{"GET", "POST", "DELETE"}, true},
		"repeated":                     {"GET,get,GET", nil, []string{"GET"}, true},
		"star is all methods":          {"*", []string{"POST"}, nil, true},
		"any is all methods":           {"any", []string{"POST"}, nil, true},
		"all is all methods":           {"ALL", []string{"POST"}, nil, true},
		"star among others":            {"GET,*", []string{"POST"}, nil, true},
		"trailing comma":               {"GET,", nil, []string{"GET"}, true},
		"only commas":                  {",,", []string{"POST"}, nil, false},
		"not a name":                   {"GET,P0ST", nil, nil, false},
		"with a space inside":          {"GE T", nil, nil, false},
		"with punctuation":             {"GET;POST", nil, nil, false},
		"not even http method letters": {"GÉT", nil, nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := ParseMethods(tt.param, tt.def...)
			if ok != tt.ok || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseMethods(%q, %v) = %v, %v; want %v, %v", tt.param, tt.def, got, ok, tt.want, tt.ok)
			}
		})
	}
}

// A status that is set for some methods is a status of its own: setting one for GET doesn't change the one that was set
// for POST of the same URI, and setting it again changes the one that it was.
func TestStatusesOfDifferentMethodsOfAURIAreSeparate(t *testing.T) {
	stubOnce.Do(stubGlobalFuncs)
	port := int(40000 + portSeq.Add(1))
	t.Cleanup(func() { TheStatusManager.Clear(port, "", "") })
	TheStatusManager.SetStatusFor(port, "/m", "/m", "", "", []string{"POST"}, []int{502}, -1, true)
	TheStatusManager.SetStatusFor(port, "/m", "/m", "", "", []string{"GET"}, []int{503}, -1, true)
	TheStatusManager.SetStatusFor(port, "/m", "/m", "", "", []string{"get"}, []int{429}, 1, true)
	if n := len(TheStatusManager.Snapshot()[port]); n != 2 {
		t.Fatalf("%d statuses were set, want 2: one for POST and one for GET", n)
	}
	for _, tt := range []struct {
		method string
		want   int
	}{
		{"POST", 502}, {"GET", 429}, {"GET", 0}, {"DELETE", 0}, {"POST", 502},
	} {
		// The GET status was set again, for 1 request: 429 once, and then it is used up.
		if status, _ := TheStatusManager.GetStatusFor(port, "/m", nil, tt.method); status != tt.want {
			t.Errorf("%s /m = %d, want %d", tt.method, status, tt.want)
		}
	}
}

func TestStatusesForAllMethodsAndForOneAreSeparate(t *testing.T) {
	stubOnce.Do(stubGlobalFuncs)
	port := int(40000 + portSeq.Add(1))
	t.Cleanup(func() { TheStatusManager.Clear(port, "", "") })
	TheStatusManager.SetStatusFor(port, "/m", "", "", "", []string{"GET"}, []int{503}, -1, true)
	TheStatusManager.SetStatusFor(port, "/m", "", "", "", nil, []int{500}, -1, true)
	if n := len(TheStatusManager.Snapshot()[port]); n != 2 {
		t.Fatalf("%d statuses were set, want 2", n)
	}
	for method, want := range map[string]int{"GET": 503, "POST": 500, "PUT": 500} {
		if status, _ := TheStatusManager.GetStatusFor(port, "/m", nil, method); status != want {
			t.Errorf("%s /m = %d, want %d", method, status, want)
		}
	}
}

// A status that is set for a header is not the status of the URI without the header. Looking for the config of the one
// found the other, since a config that has no header to match matched whatever header it was looked up with.
func TestStatusWithAHeaderAndWithoutAreSeparate(t *testing.T) {
	stubOnce.Do(stubGlobalFuncs)
	port := int(40000 + portSeq.Add(1))
	t.Cleanup(func() { TheStatusManager.Clear(port, "", "") })
	TheStatusManager.SetStatusFor(port, "/h", "", "", "", nil, []int{500}, -1, true)
	TheStatusManager.SetStatusFor(port, "/h", "", "x-env", "dev", nil, []int{503}, -1, true)
	TheStatusManager.SetStatusFor(port, "/h", "", "x-env", "dev", nil, []int{504}, -1, true)
	TheStatusManager.SetStatusFor(port, "/h", "", "x-env", "dev", nil, []int{418}, -1, false)

	var got []string
	for _, sc := range TheStatusManager.Snapshot()[port] {
		got = append(got, fmt.Sprintf("%v:%d", sc.Statuses, len(sc.Match.HeaderMatches)))
	}
	if want := []string{"[500]:0", "[504]:1", "[418]:1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("statuses = %v, want %v: one without a header, and one for the header present and one for it not present", got, want)
	}
}

// serveRecovering serves the request the way net/http serves it, which recovers a panic of a handler and goes on to the next
// request, and tells whether the handler panicked.
func (s *testServer) serveRecovering(target string) (rr *httptest.ResponseRecorder, panicked any) {
	rr = httptest.NewRecorder()
	defer func() { panicked = recover() }()
	req := httptest.NewRequest("GET", target, nil)
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, s.port))
	_, req, _ = util.WithRequestStore(req, nil)
	s.api.ServeHTTP(rr, req)
	return rr, nil
}

func (s *testServer) portStatus() *PortStatus {
	statusLock.RLock()
	defer statusLock.RUnlock()
	return portStatusMap[strconv.Itoa(s.port)]
}

// The progress through a list of statuses is kept for the request id, which requests with lists of other lengths share. A
// list that is shorter than the progress that was made through a longer one used to read past its end while the status of the
// port was locked, and the lock was never released: the next request of the port, whatever it was for, waited for it forever.
func TestFlipflopWithAListShorterThanTheProgressMadeThroughALongerOne(t *testing.T) {
	s := newTestServer(t)
	for i, want := range []int{200, 201, 202} {
		if rr, p := s.serveRecovering("/status/200,201,202,203:5"); p != nil || rr.Code != want {
			t.Fatalf("request %d of the long list = %d, panic %v, want %d", i+1, rr.Code, p, want)
		}
	}
	for i, want := range []int{418, 419} {
		rr, p := s.serveRecovering("/status/418,419:5")
		if p != nil {
			t.Fatalf("request %d of the shorter list panicked: %v", i+1, p)
		}
		if rr.Code != want {
			t.Errorf("request %d of the shorter list = %d, want %d: the list starts over", i+1, rr.Code, want)
		}
	}
	// the list is done, and the status flips to 200
	if rr, p := s.serveRecovering("/status/418,419:5"); p != nil || rr.Code != 200 || rr.Header().Get(constants.HeaderGotoStatusFlip) != "419" {
		t.Errorf("request after the list = %d %v, panic %v, want 200 flipping from 419", rr.Code, rr.Header(), p)
	}
	ps := s.portStatus()
	if ps == nil || !ps.lock.TryLock() {
		t.Fatal("the status of the port is still locked, or there is none")
	}
	ps.lock.Unlock()
	if rr, _ := s.serveRecovering("/status/counts"); rr.Code != 200 {
		t.Errorf("status counts of the port = %d, want 200", rr.Code)
	}
}

func TestStatusOfAPortIsNotLeftLockedWhenServingAStatusPanics(t *testing.T) {
	s := newTestServer(t)
	if rr, p := s.serveRecovering("/status/200"); p != nil || rr.Code != 200 {
		t.Fatalf("first request = %d, panic %v, want 200", rr.Code, p)
	}
	ps := s.portStatus()
	if ps == nil {
		t.Fatal("the port has no status")
	}
	// writing to the nil map of the flipflop configs panics while the status is locked
	ps.lock.Lock()
	ps.FlipflopConfigs = nil
	ps.lock.Unlock()
	if _, p := s.serveRecovering("/status/418:3"); p == nil {
		t.Fatal("serving a flipflop status with no flipflop configs didn't panic, which the test needs")
	}
	if !ps.lock.TryLock() {
		t.Fatal("the status of the port is still locked after the panic")
	}
	ps.lock.Unlock()
}
