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

package probes

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
)

var (
	setupOnce sync.Once
	// Probes are kept per port, so every test uses a port of its own.
	portSeq atomic.Int32
)

func stubGlobalFuncs() {
	global.Funcs.IsListenerPresent = func(int) bool { return false }
	global.Funcs.IsListenerOpen = func(int) bool { return false }
	global.Funcs.GetListenerID = func(int) string { return "" }
	global.Funcs.GetListenerLabel = func(*http.Request) string { return "" }
	global.Funcs.GetListenerLabelForPort = func(int) string { return "" }
	util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
}

type env struct {
	port      int
	portText  string
	nextCalls atomic.Int32
	handler   http.Handler
}

// newEnv sets up the routes, which are registered on global routers and so only once. Setting them up also wires the
// probe checks into global.Funcs, as the server does.
func newEnv(t *testing.T) *env {
	t.Helper()
	setupOnce.Do(func() {
		stubGlobalFuncs()
		Middleware.SetRoutes(nil)
	})
	e := &env{port: int(42000 + portSeq.Add(1))}
	e.portText = strconv.Itoa(e.port)
	e.handler = Middleware.MiddlewareHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.nextCalls.Add(1)
		fmt.Fprint(w, "next")
	}))
	t.Cleanup(func() {
		lock.Lock()
		delete(probesByPort, e.portText)
		lock.Unlock()
	})
	return e
}

func (e *env) newRequest(method, target string, headers ...string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Add(headers[i], headers[i+1])
	}
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, e.port))
	_, req, _ = util.WithRequestStore(req, nil)
	return req
}

func (e *env) api(method, target string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	middleware.RootRouters["/probes"].ServeHTTP(rr, e.newRequest(method, target))
	return rr
}

func (e *env) configure(t *testing.T, method, target string) string {
	t.Helper()
	rr := e.api(method, target)
	if rr.Code != http.StatusOK {
		t.Fatalf("%s %s = %d %q, want 200", method, target, rr.Code, rr.Body.String())
	}
	return rr.Body.String()
}

func (e *env) traffic(target string, headers ...string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	e.handler.ServeHTTP(rr, e.newRequest("GET", target, headers...))
	return rr
}

func (e *env) reported(t *testing.T) map[string]any {
	t.Helper()
	rr := e.api("GET", "/probes")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /probes = %d %q, want 200", rr.Code, rr.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("GET /probes returned %q, which is not JSON: %v", rr.Body.String(), err)
	}
	return got
}

func TestGetPortProbesGivesTheDefaults(t *testing.T) {
	e := newEnv(t)
	pp := GetPortProbes(e.portText)
	if pp.Port != e.portText || pp.ReadinessProbe != "/ready" || pp.LivenessProbe != "/live" ||
		pp.ReadinessStatus != 200 || pp.LivenessStatus != 200 || pp.ReadinessCount != 0 || pp.LivenessCount != 0 {
		t.Errorf("probes = %+v, want the defaults for port %s", pp, e.portText)
	}
	if again := GetPortProbes(e.portText); again != pp {
		t.Error("the probes of a port must be the same ones every time")
	}
}

func TestProbesAreReportedAsJSON(t *testing.T) {
	e := newEnv(t)
	got := e.reported(t)
	want := map[string]any{
		"port": e.portText, "readinessProbe": "/ready", "readinessStatus": 200.0, "readinessStatusRemainingCount": 0.0,
		"readinessCount": 0.0, "readinessOverflowCount": 0.0, "livenessProbe": "/live", "livenessStatus": 200.0,
		"livenessStatusRemainingCount": 0.0, "livenessCount": 0.0, "livenessOverflowCount": 0.0,
	}
	if len(got) != len(want) {
		t.Errorf("probes = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("probes[%s] = %v, want %v", k, got[k], v)
		}
	}
	if rr := e.api("POST", "/probes"); rr.Code == http.StatusOK {
		t.Error("the probes are only reported for GET")
	}
}

func TestReadinessProbeIsAnsweredAndCounted(t *testing.T) {
	e := newEnv(t)
	for i := 1; i <= 3; i++ {
		rr := e.traffic("/ready", "X-Custom", "v")
		if rr.Code != http.StatusOK || rr.Body.Len() != 0 {
			t.Errorf("probe %d = %d %q, want 200 and no body", i, rr.Code, rr.Body.String())
		}
		h := rr.Result().Header
		if h.Get(constants.HeaderReadinessRequestCount) != strconv.Itoa(i) || h.Get(constants.HeaderReadinessOverflowCount) != "0" {
			t.Errorf("probe %d headers = %v, want count %d and no overflow", i, h, i)
		}
		// The names of the echoed headers are added as they are, and are not canonicalized.
		if fmt.Sprint(h["Readiness-Request-URI"]) != "[/ready]" || fmt.Sprint(h["Readiness-Request-X-Custom"]) != "[v]" {
			t.Errorf("probe %d headers = %v, want the request echoed with the Readiness-Request prefix", i, h)
		}
	}
	if e.nextCalls.Load() != 0 {
		t.Error("a probe must not be handed on to the next handler")
	}
	got := e.reported(t)
	if got["readinessCount"] != 3.0 || got["livenessCount"] != 0.0 {
		t.Errorf("counts = readiness %v, liveness %v, want 3 and 0", got["readinessCount"], got["livenessCount"])
	}
}

func TestLivenessProbeIsAnsweredAndCountedApartFromReadiness(t *testing.T) {
	e := newEnv(t)
	e.traffic("/ready")
	for i := 1; i <= 2; i++ {
		rr := e.traffic("/live")
		h := rr.Result().Header
		if rr.Code != http.StatusOK || h.Get(constants.HeaderLivenessRequestCount) != strconv.Itoa(i) || h.Get(constants.HeaderLivenessOverflowCount) != "0" {
			t.Errorf("probe %d = %d %v, want 200 with count %d", i, rr.Code, h, i)
		}
		if h.Get(constants.HeaderReadinessRequestCount) != "" {
			t.Errorf("probe %d headers = %v, want no readiness headers on a liveness probe", i, h)
		}
	}
	got := e.reported(t)
	if got["readinessCount"] != 1.0 || got["livenessCount"] != 2.0 {
		t.Errorf("counts = readiness %v, liveness %v, want 1 and 2", got["readinessCount"], got["livenessCount"])
	}
}

func TestProbeMatchingIgnoresCaseAndNeedsTheWholeURI(t *testing.T) {
	e := newEnv(t)
	for _, uri := range []string{"/ready", "/READY", "/Ready"} {
		if rr := e.traffic(uri); rr.Body.String() == "next" {
			t.Errorf("%s was handed on, want it answered as the readiness probe", uri)
		}
	}
	for _, uri := range []string{"/ready/more", "/readyx", "/other", "/"} {
		if rr := e.traffic(uri); rr.Body.String() != "next" {
			t.Errorf("%s = %q, want it handed on to the next handler", uri, rr.Body.String())
		}
	}
	if got := e.reported(t)["readinessCount"]; got != 3.0 {
		t.Errorf("readiness count = %v, want 3", got)
	}
}

func TestNonProbeRequestWithoutNextHandlerIsLeftAlone(t *testing.T) {
	e := newEnv(t)
	rr := httptest.NewRecorder()
	Middleware.MiddlewareHandler(nil).ServeHTTP(rr, e.newRequest("GET", "/other"))
	if rr.Code != http.StatusOK || rr.Body.Len() != 0 {
		t.Errorf("response = %d %q, want an untouched 200", rr.Code, rr.Body.String())
	}
}

func TestSetProbeChangesTheURIAndResetsTheCount(t *testing.T) {
	e := newEnv(t)
	e.traffic("/ready")
	e.traffic("/ready")
	msg := e.configure(t, "POST", "/probes/readiness/set?uri=/MyReady")
	if !strings.Contains(msg, "Probe [readiness] URI [/myready] added, count reset") || !strings.Contains(msg, e.portText) {
		t.Errorf("message = %q, want the probe, its URI (in lower case) and the port", msg)
	}
	got := e.reported(t)
	if got["readinessProbe"] != "/myready" || got["readinessCount"] != 0.0 {
		t.Errorf("probes = %v, want the new URI and a reset count", got)
	}
	// The old URI is not a probe any more, and the new one is, whatever its case.
	if rr := e.traffic("/ready"); rr.Body.String() != "next" {
		t.Error("the old URI must be handed on")
	}
	if rr := e.traffic("/MYREADY"); rr.Result().Header.Get(constants.HeaderReadinessRequestCount) != "1" {
		t.Errorf("the new URI must be answered as the probe, got %v", rr.Result().Header)
	}
	// The liveness probe is left alone, and PUT does the same as POST.
	e.configure(t, "PUT", "/probes/liveness/set?uri=/alive")
	got = e.reported(t)
	if got["livenessProbe"] != "/alive" || got["readinessProbe"] != "/myready" {
		t.Errorf("probes = %v, want only the liveness URI changed by the second call", got)
	}
}

func TestSetProbeRejectsABadTypeAndAnEmptyURI(t *testing.T) {
	e := newEnv(t)
	rr := e.api("POST", "/probes/startup/set?uri=/x")
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Invalid probe type") {
		t.Errorf("bad type = %d %q, want 400 Invalid probe type", rr.Code, rr.Body.String())
	}
	rr = e.api("POST", "/probes/readiness/set?uri=")
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Invalid URI") {
		t.Errorf("empty uri = %d %q, want 400 Invalid URI", rr.Code, rr.Body.String())
	}
	if got := e.reported(t); got["readinessProbe"] != "/ready" || got["livenessProbe"] != "/live" {
		t.Errorf("probes = %v, want them unchanged by the rejected calls", got)
	}
	// The probe type is not case sensitive.
	e.configure(t, "POST", "/probes/READINESS/set?uri=/r2")
	if got := e.reported(t); got["readinessProbe"] != "/r2" {
		t.Errorf("readiness probe = %v, want /r2", got["readinessProbe"])
	}
}

func TestSetProbeStatusAnswersTheStatusUntilChanged(t *testing.T) {
	e := newEnv(t)
	msg := e.configure(t, "POST", "/probes/readiness/set/status=503")
	if !strings.Contains(msg, "Status [503] set") || strings.Contains(msg, "remaining count") {
		t.Errorf("message = %q, want the status set for good", msg)
	}
	for i := 0; i < 5; i++ {
		if rr := e.traffic("/ready"); rr.Code != http.StatusServiceUnavailable {
			t.Errorf("probe %d = %d, want 503", i, rr.Code)
		}
	}
	// Liveness is not affected.
	if rr := e.traffic("/live"); rr.Code != http.StatusOK {
		t.Errorf("liveness = %d, want 200", rr.Code)
	}
	e.configure(t, "PUT", "/probes/readiness/set/status=200")
	if rr := e.traffic("/ready"); rr.Code != http.StatusOK {
		t.Errorf("probe after resetting the status = %d, want 200", rr.Code)
	}
}

func TestSetProbeStatusWithCountAnswersTheStatusThatManyTimes(t *testing.T) {
	e := newEnv(t)
	msg := e.configure(t, "POST", "/probes/liveness/set/status=500:2")
	if !strings.Contains(msg, "Status [500] set with remaining count [2]") {
		t.Errorf("message = %q, want the status and the count", msg)
	}
	if got := e.reported(t)["livenessStatusRemainingCount"]; got != 2.0 {
		t.Errorf("remaining count = %v, want 2", got)
	}
	var statuses []int
	for i := 0; i < 5; i++ {
		statuses = append(statuses, e.traffic("/live").Code)
	}
	if fmt.Sprint(statuses) != "[500 500 200 200 200]" {
		t.Errorf("statuses = %v, want exactly two 500s and then 200s", statuses)
	}
	got := e.reported(t)
	if got["livenessStatus"] != 200.0 || got["livenessStatusRemainingCount"] != -1.0 || got["livenessCount"] != 5.0 {
		t.Errorf("probes = %v, want the status back to 200, no remaining count, and 5 probes counted", got)
	}
}

func TestSetProbeStatusZeroMeansOK(t *testing.T) {
	e := newEnv(t)
	e.configure(t, "POST", "/probes/readiness/set/status=0")
	if rr := e.traffic("/ready"); rr.Code != http.StatusOK {
		t.Errorf("probe = %d, want 200", rr.Code)
	}
}

func TestSetProbeStatusRejectsABadTypeAndABadStatus(t *testing.T) {
	e := newEnv(t)
	rr := e.api("POST", "/probes/startup/set/status=503")
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Invalid probe type") {
		t.Errorf("bad type = %d %q, want 400 Invalid probe type", rr.Code, rr.Body.String())
	}
	rr = e.api("POST", "/probes/readiness/set/status=abc")
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Invalid status code") {
		t.Errorf("bad status = %d %q, want 400 Invalid status code", rr.Code, rr.Body.String())
	}
	if got := e.reported(t); got["readinessStatus"] != 200.0 || got["livenessStatus"] != 200.0 {
		t.Errorf("probes = %v, want statuses unchanged", got)
	}
}

func TestClearCountsResetsBothCounts(t *testing.T) {
	e := newEnv(t)
	e.traffic("/ready")
	e.traffic("/live")
	e.traffic("/live")
	msg := e.configure(t, "POST", "/probes/counts/clear")
	if !strings.Contains(msg, "Counts Cleared") || !strings.Contains(msg, e.portText) {
		t.Errorf("message = %q, want the port's counts cleared", msg)
	}
	got := e.reported(t)
	if got["readinessCount"] != 0.0 || got["livenessCount"] != 0.0 {
		t.Errorf("counts = %v, want both cleared", got)
	}
	if rr := e.api("GET", "/probes/counts/clear"); rr.Code == http.StatusOK {
		t.Error("counts are cleared by POST only")
	}
}

func TestProbesOfPortsAreIndependent(t *testing.T) {
	e1, e2 := newEnv(t), newEnv(t)
	e1.configure(t, "POST", "/probes/readiness/set?uri=/only-first")
	e1.configure(t, "POST", "/probes/liveness/set/status=503")
	e1.traffic("/only-first")
	if got := e2.reported(t); got["readinessProbe"] != "/ready" || got["livenessStatus"] != 200.0 || got["readinessCount"] != 0.0 {
		t.Errorf("second port probes = %v, want the defaults", got)
	}
	if rr := e2.traffic("/live"); rr.Code != http.StatusOK {
		t.Errorf("second port liveness = %d, want 200", rr.Code)
	}
}

func TestCountsOverflowIntoTheOverflowCount(t *testing.T) {
	e := newEnv(t)
	pp := GetPortProbes(e.portText)
	pp.lock.Lock()
	pp.ReadinessCount = math.MaxUint64
	pp.LivenessCount = math.MaxUint64 - 1
	pp.lock.Unlock()
	h := e.traffic("/ready").Result().Header
	if h.Get(constants.HeaderReadinessRequestCount) != "0" || h.Get(constants.HeaderReadinessOverflowCount) != "1" {
		t.Errorf("readiness headers = %v, want count 0 and 1 overflow", h)
	}
	h = e.traffic("/live").Result().Header
	if h.Get(constants.HeaderLivenessRequestCount) != "18446744073709551615" || h.Get(constants.HeaderLivenessOverflowCount) != "0" {
		t.Errorf("liveness headers = %v, want the max count and no overflow", h)
	}
	h = e.traffic("/live").Result().Header
	if h.Get(constants.HeaderLivenessRequestCount) != "0" || h.Get(constants.HeaderLivenessOverflowCount) != "1" {
		t.Errorf("liveness headers = %v, want count 0 and 1 overflow", h)
	}
	got := e.reported(t)
	if got["readinessOverflowCount"] != 1.0 || got["livenessOverflowCount"] != 1.0 {
		t.Errorf("overflow counts = %v, want 1 each", got)
	}
}

func TestSetRoutesWiresTheProbeChecksIntoTheServer(t *testing.T) {
	e := newEnv(t)
	ready, live, other := e.newRequest("GET", "/ready"), e.newRequest("GET", "/live"), e.newRequest("GET", "/other")
	if !global.Funcs.IsReadinessProbe(ready) || global.Funcs.IsReadinessProbe(live) || global.Funcs.IsReadinessProbe(other) {
		t.Error("global.Funcs.IsReadinessProbe must tell the readiness probe")
	}
	if !global.Funcs.IsLivenessProbe(live) || global.Funcs.IsLivenessProbe(ready) || global.Funcs.IsLivenessProbe(other) {
		t.Error("global.Funcs.IsLivenessProbe must tell the liveness probe")
	}
	// The request store of a probe says that it is not traffic, which the middlewares after it leave alone.
	if !util.GetRequestStore(ready).IsProbeRequest || !util.GetRequestStore(ready).IsKnownNonTraffic ||
		!util.GetRequestStore(live).IsProbeRequest || util.GetRequestStore(other).IsKnownNonTraffic {
		t.Error("probe requests must be known as non traffic, and others not")
	}
	e.configure(t, "POST", "/probes/readiness/set?uri=/changed")
	if global.Funcs.IsReadinessProbe(e.newRequest("GET", "/ready")) || !global.Funcs.IsReadinessProbe(e.newRequest("GET", "/changed")) {
		t.Error("the checks must follow the probes that are configured")
	}
	if !IsReadinessProbe(e.newRequest("GET", "/changed")) || !IsLivenessProbe(e.newRequest("GET", "/live")) {
		t.Error("the exported checks must tell the probes")
	}
}

func TestConcurrentProbesAreCountedExactly(t *testing.T) {
	e := newEnv(t)
	const workers, each = 8, 50
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				e.traffic("/ready")
				e.traffic("/live")
			}
		}()
	}
	wg.Wait()
	got := e.reported(t)
	if got["readinessCount"] != float64(workers*each) || got["livenessCount"] != float64(workers*each) {
		t.Errorf("counts = readiness %v, liveness %v, want %d each", got["readinessCount"], got["livenessCount"], workers*each)
	}
}
