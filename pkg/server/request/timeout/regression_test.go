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
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"goto/pkg/util"
)

const (
	workers = 8
	rounds  = 100 // must be even
)

// The concurrent tests below follow a few rules, so that they can actually see data races:
//   - requests are created up front. Creating them inside goroutines is not what is under test.
//   - no *testing.T methods are used inside goroutines, since their internal locking would order the goroutines and
//     hide races.
//   - totals are asserted exactly, so that lost updates fail the test even when run without -race.

// tracked is a request that is ready to go through the timeout middleware.
type trackedRequest struct {
	req    *http.Request
	cancel context.CancelFunc
	w      *notifyRecorder
}

func prebuildTracked(port int, headers ...string) *trackedRequest {
	req := httptest.NewRequest("GET", "/foo", nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Add(headers[i], headers[i+1])
	}
	ctx, cancel := context.WithCancel(context.WithValue(req.Context(), util.CurrentPortKey, port))
	_, req, _ = util.WithRequestStore(req.WithContext(ctx), nil)
	return &trackedRequest{req: req, cancel: cancel, w: newNotifyRecorder()}
}

func prebuildAdmin(n int, method, target string, port int) []*http.Request {
	reqs := make([]*http.Request, n)
	for i := range reqs {
		req := httptest.NewRequest(method, target, nil)
		req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, port))
		_, reqs[i], _ = util.WithRequestStore(req, nil)
	}
	return reqs
}

func (tr *trackedRequest) run() {
	Middleware.MiddlewareHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(tr.w, tr.req)
}

// finish ends the request in one of the two ways that are tracked: even rounds close the connection, odd rounds
// complete the request.
func (tr *trackedRequest) finish(round int) {
	if round%2 == 0 {
		tr.w.closeConnection()
	} else {
		tr.cancel()
	}
}

func prebuildWorkers(t *testing.T, port int, headers ...string) [][]*trackedRequest {
	all := make([][]*trackedRequest, workers)
	for w := range all {
		all[w] = make([]*trackedRequest, rounds)
		for i := range all[w] {
			all[w][i] = prebuildTracked(port, headers...)
			t.Cleanup(all[w][i].cancel)
		}
	}
	return all
}

func TestConcurrentTrackingCountsEveryRequest(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(t, router, "POST", "/timeout/track/headers/x,y", port1), http.StatusOK)
	expectStatus(t, call(t, router, "POST", "/timeout/track/all", port1), http.StatusOK)
	all := prebuildWorkers(t, port1, "X", "x1", "Y", "y1")

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i, tr := range all[w] {
				tr.run()
				tr.finish(i)
			}
		}(w)
	}
	wg.Wait()

	want := TimeoutData{ConnectionClosed: workers * rounds / 2, RequestCompleted: workers * rounds / 2}
	waitFor(t, "all requests to be counted", func() bool {
		a, _ := allCounts(port1)
		x, _ := headerCounts(port1, "x", "x1")
		y, _ := headerCounts(port1, "y", "y1")
		return a == want && x == want && y == want
	})
}

func TestConcurrentTrackingWhileConfigChanges(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(t, router, "POST", "/timeout/track/headers/x", port1), http.StatusOK)
	all := prebuildWorkers(t, port1, "X", "x1", "Y", "y1")
	ops := []struct{ method, target string }{
		{"POST", "/timeout/track/headers/x,y"},
		{"POST", "/timeout/track/all"},
		{"GET", "/timeout/status"},
		{"GET", "/timeout"},
		{"POST", "/timeout/track/headers/x"},
		{"POST", "/timeout/track/clear"},
	}
	adminReqs := make([][]*http.Request, len(ops))
	for i, op := range ops {
		adminReqs[i] = prebuildAdmin(rounds, op.method, op.target, port1)
	}

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i, tr := range all[w] {
				tr.run()
				tr.finish(i)
			}
		}(w)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			for op := range ops {
				router.ServeHTTP(httptest.NewRecorder(), adminReqs[op][i])
			}
		}
	}()
	wg.Wait()
}

// A request that is in flight while its header is tracked again must still be counted, and must not crash the
// goroutine that waits for the end of the request.
func TestReAddingHeaderKeepsCountsOfInFlightRequests(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(t, router, "POST", "/timeout/track/headers/x", port1), http.StatusOK)
	tr := serve(t, port1, "/foo", "X", "x1")

	expectStatus(t, call(t, router, "POST", "/timeout/track/headers/x", port1), http.StatusOK)
	tr.cancel()

	waitFor(t, "the request to be counted", func() bool {
		counts, _ := headerCounts(port1, "x", "x1")
		return counts.RequestCompleted == 1
	})
}

func TestReAddingHeaderKeepsExistingCounts(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(t, router, "POST", "/timeout/track/headers/x", port1), http.StatusOK)
	serve(t, port1, "/foo", "X", "x1").cancel()
	waitFor(t, "the request to be counted", func() bool {
		counts, _ := headerCounts(port1, "x", "x1")
		return counts.RequestCompleted == 1
	})

	expectStatus(t, call(t, router, "POST", "/timeout/track/headers/x,y", port1), http.StatusOK)
	if counts, _ := headerCounts(port1, "x", "x1"); counts.RequestCompleted != 1 {
		t.Errorf("counts = %+v, tracking a header again must keep its counts", counts)
	}
	report := getReport(t, router, "/timeout/status", port1)
	if _, present := report.Headers["y"]; !present {
		t.Errorf("headers = %v, want y to be tracked too", report.Headers)
	}
}

func TestClearWhileRequestIsInFlight(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(t, router, "POST", "/timeout/track/headers/x", port1), http.StatusOK)
	expectStatus(t, call(t, router, "POST", "/timeout/track/all", port1), http.StatusOK)
	tr := serve(t, port1, "/foo", "X", "x1")

	expectStatus(t, call(t, router, "POST", "/timeout/track/clear", port1), http.StatusOK)
	tr.cancel() // must not crash, and must not count into the cleared tracking
	waitFor(t, "the request to end", func() bool { return tr.req.Context().Err() != nil })

	// Tracking that is set up after the clear starts from scratch.
	expectStatus(t, call(t, router, "POST", "/timeout/track/all", port1), http.StatusOK)
	if counts, _ := allCounts(port1); counts != (TimeoutData{}) {
		t.Errorf("all counts = %+v, want a fresh start after clear", counts)
	}
}
