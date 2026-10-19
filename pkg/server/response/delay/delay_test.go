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

package delay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/server/middleware"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
	stubOnce  sync.Once
	routeOnce sync.Once
	// api serves the admin routes, mounted where the server mounts them: under /server/response.
	api *mux.Router
	// Delays are kept per port, so every test uses a port of its own.
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

// Routes are registered on global routers, so they are registered only once, and tests don't run in parallel.
func setupRoutes() {
	stubOnce.Do(stubGlobalFuncs)
	routeOnce.Do(func() {
		api = mux.NewRouter()
		Middleware.SetRoutes(api.PathPrefix("/server/response").Subrouter())
	})
}

type testEnv struct {
	port int
	// next is the handler behind the middleware. It counts its calls and answers 200 "next".
	nextCalls atomic.Int32
	handler   http.Handler
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	setupRoutes()
	e := &testEnv{port: int(41000 + portSeq.Add(1))}
	e.handler = Middleware.MiddlewareHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.nextCalls.Add(1)
		fmt.Fprint(w, "next")
	}))
	t.Cleanup(func() {
		delayLock.Lock()
		delete(delayByPort, strconv.Itoa(e.port))
		delete(delayCountByPort, strconv.Itoa(e.port))
		delayLock.Unlock()
	})
	return e
}

func (e *testEnv) newRequest(method, target string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, e.port))
	_, req, _ = util.WithRequestStore(req, nil)
	return req
}

func (e *testEnv) serveAPI(method, target string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	api.ServeHTTP(rr, e.newRequest(method, target))
	return rr
}

func (e *testEnv) serveRoot(root, method, target string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	middleware.RootRouters[root].ServeHTTP(rr, e.newRequest(method, target))
	return rr
}

func (e *testEnv) serveTraffic(target string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	e.handler.ServeHTTP(rr, e.newRequest("GET", target))
	return rr
}

// configure calls the admin API and expects it to succeed.
func (e *testEnv) configure(t *testing.T, method, target string) string {
	t.Helper()
	rr := e.serveAPI(method, target)
	if rr.Code != http.StatusOK {
		t.Fatalf("%s %s = %d %q, want 200", method, target, rr.Code, rr.Body.String())
	}
	return rr.Body.String()
}

type reportedDelay struct {
	Delay []time.Duration `json:"delay"`
	Count int             `json:"count"`
}

func (e *testEnv) reported(t *testing.T) reportedDelay {
	t.Helper()
	rr := e.serveAPI("GET", "/server/response/delay")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /server/response/delay = %d %q, want 200", rr.Code, rr.Body.String())
	}
	var d reportedDelay
	if err := json.Unmarshal(rr.Body.Bytes(), &d); err != nil {
		t.Fatalf("GET /server/response/delay returned %q, which is not JSON: %v", rr.Body.String(), err)
	}
	return d
}

func appliedDelay(rr *httptest.ResponseRecorder) string {
	return rr.Result().Header.Get(constants.HeaderGotoResponseDelay)
}

func parseDelayHeader(t *testing.T, rr *httptest.ResponseRecorder) time.Duration {
	t.Helper()
	v := appliedDelay(rr)
	if v == "" {
		t.Fatalf("response has no %s header", constants.HeaderGotoResponseDelay)
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		t.Fatalf("%s header %q is not a duration: %v", constants.HeaderGotoResponseDelay, v, err)
	}
	return d
}

func TestSetDelayReportsAndStoresSingleDelay(t *testing.T) {
	e := newTestEnv(t)
	msg := e.configure(t, "POST", "/server/response/delay/set/50ms")
	if !strings.Contains(msg, "will delay requests") || !strings.Contains(msg, "forever") {
		t.Errorf("message = %q, want it to say that requests are delayed forever", msg)
	}
	got := e.reported(t)
	want := reportedDelay{Delay: []time.Duration{50 * time.Millisecond, 50 * time.Millisecond}, Count: 0}
	if len(got.Delay) != 2 || got.Delay[0] != want.Delay[0] || got.Delay[1] != want.Delay[1] || got.Count != want.Count {
		t.Errorf("reported delay = %+v, want %+v", got, want)
	}
}

func TestSetDelayAcceptsPutAndRange(t *testing.T) {
	e := newTestEnv(t)
	e.configure(t, "PUT", "/server/response/delay/set/10ms-30ms")
	got := e.reported(t)
	if len(got.Delay) != 2 || got.Delay[0] != 10*time.Millisecond || got.Delay[1] != 30*time.Millisecond {
		t.Errorf("reported delay = %+v, want the range 10ms-30ms", got)
	}
	// A range given high to low is the same range.
	e.configure(t, "POST", "/server/response/delay/set/30ms-10ms")
	got = e.reported(t)
	if len(got.Delay) != 2 || got.Delay[0] != 10*time.Millisecond || got.Delay[1] != 30*time.Millisecond {
		t.Errorf("reported delay = %+v, want the range 10ms-30ms", got)
	}
}

func TestSetDelayWithCount(t *testing.T) {
	e := newTestEnv(t)
	msg := e.configure(t, "POST", "/server/response/delay/set/10ms:3")
	if !strings.Contains(msg, "next [3] requests") {
		t.Errorf("message = %q, want it to mention the next 3 requests", msg)
	}
	if got := e.reported(t); got.Count != 3 || len(got.Delay) != 2 {
		t.Errorf("reported delay = %+v, want count 3", got)
	}
}

func TestSetDelayReplacesPreviousDelay(t *testing.T) {
	e := newTestEnv(t)
	e.configure(t, "POST", "/server/response/delay/set/10ms:3")
	e.configure(t, "POST", "/server/response/delay/set/20ms")
	got := e.reported(t)
	if got.Count != 0 || len(got.Delay) != 2 || got.Delay[0] != 20*time.Millisecond {
		t.Errorf("reported delay = %+v, want the second delay with no count", got)
	}
}

func TestSetDelayWithZeroClearsDelay(t *testing.T) {
	e := newTestEnv(t)
	e.configure(t, "POST", "/server/response/delay/set/10ms")
	msg := e.configure(t, "POST", "/server/response/delay/set/0s")
	if !strings.Contains(msg, "cleared") {
		t.Errorf("message = %q, want it to say that the delay is cleared", msg)
	}
	if got := e.reported(t); len(got.Delay) != 0 {
		t.Errorf("reported delay = %+v, want none", got)
	}
	if rr := e.serveTraffic("/traffic"); appliedDelay(rr) != "" {
		t.Errorf("traffic was delayed after the delay was cleared")
	}
}

func TestSetDelayRejectsInvalidDelays(t *testing.T) {
	e := newTestEnv(t)
	for _, d := range []string{"abc", "10", "-5s", "1x"} {
		rr := e.serveAPI("POST", "/server/response/delay/set/"+d)
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Invalid delay") {
			t.Errorf("POST /delay/set/%s = %d %q, want 400 Invalid delay", d, rr.Code, rr.Body.String())
		}
	}
}

func TestSetDelayRoutesAcceptOnlyWriteMethods(t *testing.T) {
	e := newTestEnv(t)
	e.configure(t, "POST", "/server/response/delay/set/10ms")
	for _, target := range []string{"/server/response/delay/set/20ms", "/server/response/delay/clear"} {
		for _, method := range []string{"GET", "DELETE"} {
			// /delay/clear with a method that is not allowed is a request for a delay of "clear", which is refused too.
			if rr := e.serveAPI(method, target); rr.Code == http.StatusOK {
				t.Errorf("%s %s = %d, want it to be refused", method, target, rr.Code)
			}
		}
	}
	if got := e.reported(t); len(got.Delay) != 2 || got.Delay[0] != 10*time.Millisecond {
		t.Errorf("a refused call changed the delay: %+v", got)
	}
}

func TestClearDelayRemovesDelay(t *testing.T) {
	e := newTestEnv(t)
	e.configure(t, "POST", "/server/response/delay/set/10ms:5")
	msg := e.configure(t, "PUT", "/server/response/delay/clear")
	if !strings.Contains(msg, "cleared") {
		t.Errorf("message = %q, want it to say that the delay is cleared", msg)
	}
	if got := e.reported(t); len(got.Delay) != 0 || got.Count != 0 {
		t.Errorf("reported delay = %+v, want none", got)
	}
	// Clearing when nothing is set works too.
	e.configure(t, "POST", "/server/response/delay/clear")
}

func TestGetDelayWithoutDelay(t *testing.T) {
	e := newTestEnv(t)
	rr := e.serveAPI("GET", "/server/response/delay")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Header().Get("Content-Type"), "json") {
		t.Fatalf("GET /delay = %d with content-type %q, want 200 JSON", rr.Code, rr.Header().Get("Content-Type"))
	}
	if got := e.reported(t); len(got.Delay) != 0 {
		t.Errorf("reported delay = %+v, want none", got)
	}
}

func TestDelaysAreKeptPerPort(t *testing.T) {
	a, b := newTestEnv(t), newTestEnv(t)
	a.configure(t, "POST", "/server/response/delay/set/10ms:2")
	if got := b.reported(t); len(got.Delay) != 0 {
		t.Errorf("port %d reports %+v, want the delay of port %d to stay there", b.port, got, a.port)
	}
	if rr := b.serveTraffic("/traffic"); appliedDelay(rr) != "" {
		t.Errorf("traffic of port %d was delayed by the delay of port %d", b.port, a.port)
	}
	if rr := a.serveTraffic("/traffic"); appliedDelay(rr) == "" {
		t.Errorf("traffic of port %d was not delayed", a.port)
	}
	b.configure(t, "POST", "/server/response/delay/clear")
	if got := a.reported(t); got.Count != 1 || len(got.Delay) != 2 {
		t.Errorf("port %d reports %+v after port %d cleared its delay, want 1 delay left", a.port, got, b.port)
	}
}

func TestDelayRequestRespondsAfterRequestedDelay(t *testing.T) {
	e := newTestEnv(t)
	for _, tc := range []struct{ root, method string }{
		{"/delay", "GET"}, {"/delay", "PUT"}, {"/delay", "POST"}, {"/delay", "DELETE"}, {"/sleep", "GET"},
	} {
		start := time.Now()
		rr := e.serveRoot(tc.root, tc.method, tc.root+"/30ms")
		took := time.Since(start)
		if rr.Code != http.StatusOK {
			t.Errorf("%s %s/30ms = %d %q, want 200", tc.method, tc.root, rr.Code, rr.Body.String())
		}
		if got := parseDelayHeader(t, rr); got != 30*time.Millisecond {
			t.Errorf("%s %s/30ms: %s = %s, want 30ms", tc.method, tc.root, constants.HeaderGotoResponseDelay, got)
		}
		if took < 30*time.Millisecond {
			t.Errorf("%s %s/30ms took %s, want at least 30ms", tc.method, tc.root, took)
		}
	}
}

func TestDelayRequestWithRangePicksFromRange(t *testing.T) {
	e := newTestEnv(t)
	for i := 0; i < 5; i++ {
		rr := e.serveRoot("/delay", "GET", "/delay/5ms-15ms")
		if rr.Code != http.StatusOK {
			t.Fatalf("GET /delay/5ms-15ms = %d, want 200", rr.Code)
		}
		if got := parseDelayHeader(t, rr); got < 5*time.Millisecond || got > 15*time.Millisecond {
			t.Errorf("applied delay %s is outside of 5ms-15ms", got)
		}
	}
}

func TestDelayRequestWithZeroDelayRespondsWithoutDelay(t *testing.T) {
	e := newTestEnv(t)
	for _, d := range []string{"0", "0s", "0ms"} {
		start := time.Now()
		rr := e.serveRoot("/delay", "GET", "/delay/"+d)
		if rr.Code != http.StatusOK || appliedDelay(rr) != "" {
			t.Errorf("GET /delay/%s = %d with %s %q, want 200 without the header", d, rr.Code, constants.HeaderGotoResponseDelay, appliedDelay(rr))
		}
		if took := time.Since(start); took > 500*time.Millisecond {
			t.Errorf("GET /delay/%s took %s, want no delay", d, took)
		}
	}
}

func TestDelayRequestRejectsInvalidDelays(t *testing.T) {
	e := newTestEnv(t)
	for _, root := range []string{"/delay", "/sleep"} {
		for _, d := range []string{"abc", "10", "-5s"} {
			rr := e.serveRoot(root, "GET", root+"/"+d)
			if rr.Code != http.StatusBadRequest || appliedDelay(rr) != "" {
				t.Errorf("GET %s/%s = %d with delay %q, want 400 without delay", root, d, rr.Code, appliedDelay(rr))
			}
		}
	}
}

func TestDelayRequestViaAdminRoute(t *testing.T) {
	e := newTestEnv(t)
	rr := e.serveAPI("GET", "/server/response/delay/20ms")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /server/response/delay/20ms = %d %q, want 200", rr.Code, rr.Body.String())
	}
	if got := parseDelayHeader(t, rr); got != 20*time.Millisecond {
		t.Errorf("applied delay = %s, want 20ms", got)
	}
	// The delay request is not subject to the delay that is set for responses.
	e.configure(t, "POST", "/server/response/delay/set/5s")
	start := time.Now()
	rr = e.serveAPI("GET", "/server/response/delay/10ms")
	if got := parseDelayHeader(t, rr); got != 10*time.Millisecond {
		t.Errorf("applied delay = %s, want 10ms", got)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("delay request took %s, want it to ignore the response delay", took)
	}
}

func TestMiddlewareWithoutDelayPassesThrough(t *testing.T) {
	e := newTestEnv(t)
	rr := e.serveTraffic("/traffic")
	if rr.Code != http.StatusOK || rr.Body.String() != "next" || appliedDelay(rr) != "" {
		t.Errorf("response = %d %q with delay %q, want 200 next without delay", rr.Code, rr.Body.String(), appliedDelay(rr))
	}
	if got := e.nextCalls.Load(); got != 1 {
		t.Errorf("next handler calls = %d, want 1", got)
	}
}

func TestMiddlewareWithoutNextHandler(t *testing.T) {
	e := newTestEnv(t)
	h := Middleware.MiddlewareHandler(nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, e.newRequest("GET", "/traffic"))
	e.configure(t, "POST", "/server/response/delay/set/5ms")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, e.newRequest("GET", "/traffic"))
	if appliedDelay(rr) == "" {
		t.Errorf("delay was not applied when there is no next handler")
	}
}

func TestMiddlewareDelaysTrafficForever(t *testing.T) {
	e := newTestEnv(t)
	e.configure(t, "POST", "/server/response/delay/set/20ms")
	for i := 0; i < 4; i++ {
		start := time.Now()
		rr := e.serveTraffic("/traffic")
		if got := parseDelayHeader(t, rr); got != 20*time.Millisecond {
			t.Errorf("request %d: applied delay = %s, want 20ms", i, got)
		}
		if took := time.Since(start); took < 20*time.Millisecond {
			t.Errorf("request %d took %s, want at least 20ms", i, took)
		}
		if rr.Body.String() != "next" {
			t.Errorf("request %d: body = %q, want the response of the next handler", i, rr.Body.String())
		}
	}
	if got := e.reported(t); len(got.Delay) != 2 || got.Count != 0 {
		t.Errorf("reported delay = %+v, want the delay to stay", got)
	}
}

func TestMiddlewareDelaysOnlyTheConfiguredCount(t *testing.T) {
	e := newTestEnv(t)
	e.configure(t, "POST", "/server/response/delay/set/5ms:3")
	var delayed []bool
	for i := 0; i < 6; i++ {
		delayed = append(delayed, appliedDelay(e.serveTraffic("/traffic")) != "")
	}
	want := []bool{true, true, true, false, false, false}
	for i := range want {
		if delayed[i] != want[i] {
			t.Fatalf("delayed requests = %v, want %v", delayed, want)
		}
	}
	if got := e.reported(t); len(got.Delay) != 0 {
		t.Errorf("reported delay = %+v, want the delay removed once its count is used up", got)
	}
	if got := e.nextCalls.Load(); got != 6 {
		t.Errorf("next handler calls = %d, want 6", got)
	}
}

func TestMiddlewareCountDecreases(t *testing.T) {
	e := newTestEnv(t)
	e.configure(t, "POST", "/server/response/delay/set/1ms:5")
	e.serveTraffic("/traffic")
	e.serveTraffic("/traffic")
	if got := e.reported(t); got.Count != 3 {
		t.Errorf("count = %d after 2 delayed requests of 5, want 3", got.Count)
	}
}

func TestMiddlewareDelayFromRange(t *testing.T) {
	e := newTestEnv(t)
	e.configure(t, "POST", "/server/response/delay/set/5ms-15ms")
	for i := 0; i < 5; i++ {
		if got := parseDelayHeader(t, e.serveTraffic("/traffic")); got < 5*time.Millisecond || got > 15*time.Millisecond {
			t.Errorf("applied delay %s is outside of 5ms-15ms", got)
		}
	}
}

func TestMiddlewareLeavesAdminAndDelayRequestsAlone(t *testing.T) {
	e := newTestEnv(t)
	e.configure(t, "POST", "/server/response/delay/set/3s:1")
	for _, uri := range []string{"/server/anything", "/request/anything", "/delay/1ms", "/health"} {
		start := time.Now()
		rr := e.serveTraffic(uri)
		if appliedDelay(rr) != "" || time.Since(start) > time.Second {
			t.Errorf("GET %s was delayed, want admin, health and delay requests to be exempt", uri)
		}
	}
	// None of them used up the count.
	if got := e.reported(t); got.Count != 1 {
		t.Errorf("count = %d, want 1: exempt requests must not use the delay up", got.Count)
	}
}

func TestMiddlewareAppliesDelayToRequestForOtherPortViaPortPrefix(t *testing.T) {
	// A request made to one port can be addressed to another port with /port=N/..., and the delay of that port is
	// what gets applied.
	a, b := newTestEnv(t), newTestEnv(t)
	b.configure(t, "POST", "/server/response/delay/set/5ms")
	req := httptest.NewRequest("GET", fmt.Sprintf("/port=%d/traffic", b.port), nil)
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, a.port))
	_, req, _ = util.WithRequestStore(req, nil)
	rr := httptest.NewRecorder()
	a.handler.ServeHTTP(rr, req)
	if appliedDelay(rr) == "" {
		t.Errorf("the delay of port %d was not applied to a request addressed to it", b.port)
	}
}

func TestConcurrentConfigurationAndTraffic(t *testing.T) {
	e := newTestEnv(t)
	const trafficWorkers, requestsPerWorker, configWorkers, configRounds = 8, 25, 4, 25
	var wg sync.WaitGroup
	start := make(chan struct{})
	traffic := make([][]*http.Request, trafficWorkers)
	for w := range traffic {
		for i := 0; i < requestsPerWorker; i++ {
			traffic[w] = append(traffic[w], e.newRequest("GET", "/traffic"))
		}
	}
	type call struct{ method, target string }
	configs := make([][]call, configWorkers)
	for w := range configs {
		for i := 0; i < configRounds; i++ {
			configs[w] = append(configs[w],
				call{"POST", "/server/response/delay/set/1ms:2"}, call{"POST", "/server/response/delay/set/1ms"},
				call{"POST", "/server/response/delay/clear"}, call{"GET", "/server/response/delay"})
		}
	}
	configReqs := make([][]*http.Request, configWorkers)
	for w := range configs {
		for _, c := range configs[w] {
			configReqs[w] = append(configReqs[w], e.newRequest(c.method, c.target))
		}
	}
	configCodes := make([][]int, configWorkers)
	for w := range configReqs {
		configCodes[w] = make([]int, len(configReqs[w]))
	}
	for w := range traffic {
		wg.Add(1)
		go func(reqs []*http.Request) {
			defer wg.Done()
			<-start
			for _, req := range reqs {
				e.handler.ServeHTTP(httptest.NewRecorder(), req)
			}
		}(traffic[w])
	}
	for w := range configReqs {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			for i, req := range configReqs[w] {
				rr := httptest.NewRecorder()
				api.ServeHTTP(rr, req)
				configCodes[w][i] = rr.Code
			}
		}(w)
	}
	close(start)
	wg.Wait()
	if got, want := int(e.nextCalls.Load()), trafficWorkers*requestsPerWorker; got != want {
		t.Errorf("next handler calls = %d, want every request to reach it: %d", got, want)
	}
	for w := range configCodes {
		for i, code := range configCodes[w] {
			if code != http.StatusOK {
				t.Errorf("config worker %d call %d (%s %s) = %d, want 200", w, i, configs[w][i].method, configs[w][i].target, code)
			}
		}
	}
}

func TestMiddlewareHandlerIsRegisteredInResponseChain(t *testing.T) {
	if Middleware == nil || Middleware.Name != "delay" || Middleware.SetRoutes == nil || Middleware.MiddlewareHandler == nil {
		t.Errorf("Middleware = %+v, want the delay middleware with routes and a handler", Middleware)
	}
}
