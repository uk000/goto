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
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"goto/pkg/server/middleware"
)

// POST /delay/clear is documented as the way to remove the delay. The route has no delay parameter, so it used to be
// answered as an invalid delay, with a 400, even though it did remove the delay.
func TestRegressionClearDelayAnswersOK(t *testing.T) {
	e := newTestEnv(t)
	e.configure(t, "POST", "/server/response/delay/set/10ms")
	for _, method := range []string{"POST", "PUT"} {
		e.configure(t, "POST", "/server/response/delay/set/10ms")
		rr := e.serveAPI(method, "/server/response/delay/clear")
		if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), "Invalid") {
			t.Errorf("%s /delay/clear = %d %q, want 200 and a message about the delay being cleared", method, rr.Code, rr.Body.String())
		}
		if got := e.reported(t); len(got.Delay) != 0 {
			t.Errorf("%s /delay/clear left the delay %+v", method, got)
		}
	}
}

// A call with an invalid delay is refused, and the delay that was set stays. It used to wipe the delay before it
// found out that the new one is invalid.
func TestRegressionInvalidSetKeepsExistingDelay(t *testing.T) {
	e := newTestEnv(t)
	e.configure(t, "POST", "/server/response/delay/set/10ms:4")
	rr := e.serveAPI("POST", "/server/response/delay/set/abc")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("POST /delay/set/abc = %d, want 400", rr.Code)
	}
	got := e.reported(t)
	if len(got.Delay) != 2 || got.Delay[0] != 10*time.Millisecond || got.Count != 4 {
		t.Errorf("delay after the refused call = %+v, want the previous delay of 10ms with count 4", got)
	}
	if rr := e.serveTraffic("/traffic"); appliedDelay(rr) == "" {
		t.Errorf("traffic is not delayed after a refused call, want the previous delay to stay")
	}
}

// The delay of a request that is counted is used once per request, however many requests arrive together. The count
// used to be read, and later written back decremented, by each request on its own, so that requests that arrived
// together all used the same count and the delay was applied to far more requests than it was configured for.
func TestRegressionConcurrentRequestsUseExactDelayCount(t *testing.T) {
	e := newTestEnv(t)
	const requests, limit = 200, 10
	e.configure(t, "POST", "/server/response/delay/set/1ms:10")
	reqs := make([]*http.Request, requests)
	recs := make([]*httptest.ResponseRecorder, requests)
	for i := range reqs {
		reqs[i] = e.newRequest("GET", "/traffic")
		recs[i] = httptest.NewRecorder()
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range reqs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			e.handler.ServeHTTP(recs[i], reqs[i])
		}(i)
	}
	close(start)
	wg.Wait()
	delayed := 0
	for _, rr := range recs {
		if appliedDelay(rr) != "" {
			delayed++
		}
	}
	if delayed != limit {
		t.Errorf("delayed responses = %d, want exactly %d", delayed, limit)
	}
	if got := int(e.nextCalls.Load()); got != requests {
		t.Errorf("next handler calls = %d, want %d", got, requests)
	}
	if got := e.reported(t); len(got.Delay) != 0 {
		t.Errorf("delay after its count was used up = %+v, want none", got)
	}
}

// A request that waits for its requested delay must not hold up the configuration of delays, nor the traffic that
// comes after it. The handler used to hold the read lock of the delays while it slept, so that a call that sets the
// delay waited for the sleep to end, and every request after that call waited too.
func TestRegressionDelayRequestDoesNotBlockConfiguration(t *testing.T) {
	e := newTestEnv(t)
	req := e.newRequest("GET", "/delay/900ms")
	done := make(chan struct{})
	go func() {
		defer close(done)
		middleware.RootRouters["/delay"].ServeHTTP(httptest.NewRecorder(), req)
	}()
	time.Sleep(150 * time.Millisecond)
	start := time.Now()
	e.configure(t, "POST", "/server/response/delay/set/1ms")
	trafficStart := time.Now()
	e.serveTraffic("/traffic")
	setTook, trafficTook := trafficStart.Sub(start), time.Since(trafficStart)
	<-done
	if setTook > 400*time.Millisecond {
		t.Errorf("setting the delay took %s while a delay request was waiting, want it not to wait for the request", setTook)
	}
	if trafficTook > 400*time.Millisecond {
		t.Errorf("traffic took %s while a delay request was waiting, want it not to wait for the request", trafficTook)
	}
}

// Delays are picked in milliseconds from the range that is asked for. A range that is narrower than a millisecond used
// to make the pick panic, for the delay of requests as well as for the delay of responses.
func TestRegressionRangeNarrowerThanMillisecondDoesNotPanic(t *testing.T) {
	e := newTestEnv(t)
	call := func(name string, f func() *httptest.ResponseRecorder) (rr *httptest.ResponseRecorder) {
		defer func() {
			if p := recover(); p != nil {
				t.Errorf("%s panicked: %v", name, p)
			}
		}()
		return f()
	}
	if rr := call("GET /delay/100us-900us", func() *httptest.ResponseRecorder {
		return e.serveRoot("/delay", "GET", "/delay/100us-900us")
	}); rr == nil || rr.Code != http.StatusOK || appliedDelay(rr) == "" {
		t.Errorf("GET /delay/100us-900us did not respond with an applied delay: %v", rr)
	}
	e.configure(t, "POST", "/server/response/delay/set/100us-900us")
	if rr := call("traffic with a response delay of 100us-900us", func() *httptest.ResponseRecorder {
		return e.serveTraffic("/traffic")
	}); rr == nil || rr.Code != http.StatusOK || appliedDelay(rr) == "" {
		t.Errorf("traffic with a response delay of 100us-900us did not get the delay: %v", rr)
	}
}
