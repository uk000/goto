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

package filter

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
)

const (
	workers = 8
	rounds  = 200
)

// The concurrent tests below follow a few rules, so that they can actually see data races:
//   - requests are created up front. Creating them inside goroutines is not what is under test.
//   - no *testing.T methods are used inside goroutines, since their internal locking would order the goroutines and
//     hide races. Problems are counted with atomics and reported by the test goroutine.
//   - totals are asserted exactly, so that lost updates fail the test even when run without -race.

func prebuild(n int, method, target string, headers ...string) []*http.Request {
	reqs := make([]*http.Request, n)
	for i := range reqs {
		reqs[i], _ = newRequest(method, target, headers...)
	}
	return reqs
}

func runWorkers(n int, f func(worker int)) {
	var wg sync.WaitGroup
	for w := 0; w < n; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			f(w)
		}(w)
	}
	wg.Wait()
}

func TestConcurrentFilteringCountsEveryRequest(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(router, "POST", "/ignore/add?uri=/foo.*"), http.StatusOK)
	expectStatus(t, call(router, "POST", "/ignore/add/header/foo=bar"), http.StatusOK)
	expectStatus(t, call(router, "POST", "/ignore/set/status=418"), http.StatusOK)

	// Even workers are filtered by URI, odd ones by header.
	reqs := make([]*http.Request, workers)
	for w := range reqs {
		if w%2 == 0 {
			reqs[w], _ = newRequest("GET", "/foo/x")
		} else {
			reqs[w], _ = newRequest("GET", "/other", "Foo", "bar")
		}
	}
	var unexpected atomic.Int64
	runWorkers(workers, func(w int) {
		for i := 0; i < rounds; i++ {
			rr := httptest.NewRecorder()
			if !filterRequest(rr, reqs[w]) || rr.Code != 418 {
				unexpected.Add(1)
			}
		}
	})

	if unexpected.Load() != 0 {
		t.Errorf("%d requests were not filtered with status 418", unexpected.Load())
	}
	if got, want := ignoreFilter.FilteredCount, int64(workers*rounds); got != want {
		t.Errorf("FilteredCount = %d, want %d: filtered requests were not counted", got, want)
	}
}

func TestConcurrentFilteringWhileConfigChanges(t *testing.T) {
	router := setup(t)
	ops := []struct{ method, target string }{
		{"POST", "/ignore/add?uri=/foo.*"},
		{"POST", "/ignore/add?uri=!/bar"},
		{"POST", "/ignore/add/header/foo=bar"},
		{"POST", "/ignore/set/status=418"},
		{"GET", "/ignore/status"},
		{"GET", "/ignore/count"},
		{"GET", "/ignore"},
		{"POST", "/ignore/remove?uri=/foo.*"},
		{"POST", "/ignore/remove/header/foo=bar"},
		{"POST", "/bypass/add?uri=/baz.*"},
		{"POST", "/bypass/set/status=202"},
		{"GET", "/bypass"},
		{"POST", "/ignore/clear"},
	}
	adminReqs := make([][]*http.Request, len(ops))
	for i, op := range ops {
		adminReqs[i] = prebuild(rounds, op.method, op.target)
	}

	traffic := make([][]*http.Request, workers)
	for w := range traffic {
		traffic[w] = []*http.Request{}
		for _, uri := range []string{"/foo/x", "/bar", "/baz/y", "/other"} {
			req, _ := newRequest("GET", uri)
			traffic[w] = append(traffic[w], req)
		}
		req, _ := newRequest("GET", "/headers", "Foo", "bar")
		traffic[w] = append(traffic[w], req)
	}

	var unexpected atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				rr := httptest.NewRecorder()
				req := traffic[w][i%len(traffic[w])]
				if filterRequest(rr, req) && rr.Code != http.StatusOK && rr.Code != 418 && rr.Code != 202 {
					unexpected.Add(1)
				}
			}
		}(w)
	}
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			for op := range ops {
				router.ServeHTTP(httptest.NewRecorder(), adminReqs[op][i])
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			ignoreFilter.ResetCount()
			bypassFilter.ResetCount()
		}
	}()
	wg.Wait()

	if unexpected.Load() != 0 {
		t.Errorf("%d filtered requests got an unexpected status", unexpected.Load())
	}
}

func TestRemoveFilterIsCaseInsensitive(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(router, "POST", "/ignore/add?uri=/Foo/Bar"), http.StatusOK)
	expectStatus(t, call(router, "POST", "/ignore/add?uri=!/Baz"), http.StatusOK)
	expectStatus(t, call(router, "POST", "/ignore/add/header/Foo=Bar"), http.StatusOK)
	expectStatus(t, call(router, "POST", "/ignore/add/header/Other=Val*"), http.StatusOK)

	expectStatus(t, call(router, "POST", "/ignore/remove?uri=/foo/bar"), http.StatusOK)
	if len(ignoreFilter.UriUpdates) != 0 {
		t.Errorf("UriUpdates = %v, want /Foo/Bar removed by /foo/bar", ignoreFilter.UriUpdates)
	}
	expectStatus(t, call(router, "POST", "/ignore/remove?uri=!/BAZ"), http.StatusOK)
	if len(ignoreFilter.NotUriUpdates) != 0 {
		t.Errorf("NotUriUpdates = %v, want !/Baz removed by !/BAZ", ignoreFilter.NotUriUpdates)
	}
	expectStatus(t, call(router, "POST", "/ignore/remove/header/FOO=bar"), http.StatusOK)
	if _, present := ignoreFilter.HeaderUpdates["foo"]; present {
		t.Errorf("HeaderUpdates = %v, want foo=Bar removed by FOO=bar", ignoreFilter.HeaderUpdates)
	}
	expectStatus(t, call(router, "POST", "/ignore/remove/header/other=val*"), http.StatusOK)
	if len(ignoreFilter.HeaderUpdates) != 0 || ignoreFilter.hasHeaders {
		t.Errorf("HeaderUpdates=%v hasHeaders=%v, want everything removed", ignoreFilter.HeaderUpdates, ignoreFilter.hasHeaders)
	}
}

func TestSetStatusRejectsInvalidStatus(t *testing.T) {
	for _, ft := range filterTypes {
		for _, status := range []string{"abc", ",", ":3", "99", "1000"} {
			t.Run(ft+"/"+status, func(t *testing.T) {
				router := setup(t)
				expectStatus(t, call(router, "POST", "/"+ft+"/set/status=418"), http.StatusOK)

				rr := call(router, "POST", "/"+ft+"/set/status="+status)
				expectStatus(t, rr, http.StatusBadRequest)
				expectBodyContains(t, rr, "Invalid status")
				if got := filterFor(ft).Status; got != 418 {
					t.Errorf("Status = %d, want 418 to be kept after the rejected request", got)
				}
			})
		}
	}
}

func TestSetStatusAcceptsLimits(t *testing.T) {
	router := setup(t)
	for _, status := range []int{100, 599, 999} {
		expectStatus(t, call(router, "POST", "/ignore/set/status="+strconv.Itoa(status)), http.StatusOK)
		if ignoreFilter.Status != status {
			t.Errorf("Status = %d, want %d", ignoreFilter.Status, status)
		}
	}
}

func TestApplyUpdatesRefreshesHeaderFlag(t *testing.T) {
	router := setup(t)
	expectStatus(t, call(router, "POST", "/ignore/add/header/foo=bar"), http.StatusOK)
	expectStatus(t, call(router, "POST", "/ignore/clear"), http.StatusOK)
	req, _ := newRequest("GET", "/x", "Foo", "bar")
	if ignoreFilter.matchHeaders(req) {
		t.Fatal("a cleared filter must not match")
	}
	if ignoreFilter.hasHeaders {
		t.Error("hasHeaders must be false once the cleared state is applied, so that later requests skip header matching")
	}
}
