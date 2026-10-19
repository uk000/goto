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

package log

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"goto/pkg/server/middleware"
	"goto/pkg/util"
)

// Headers are excluded from logs through the API while requests are being served, which read the list of the excluded
// headers. The list is guarded by util, and the API has to use what util gives to change it (this is meant to be run
// with the race detector, and the map of the headers would also fail with a fatal error on concurrent writes).
func TestExcludingHeadersWhileRequestsReadThem(t *testing.T) {
	const writers, perWriter = 8, 50
	names := []string{}
	for w := 0; w < writers; w++ {
		for i := 0; i < perWriter; i++ {
			names = append(names, fmt.Sprintf("x-regression-%d-%d", w, i))
		}
	}
	saveFlags(t, names...)
	// The router and the requests are made before the goroutines start, so that these goroutines only use the router,
	// which the server does too.
	admin("GET", "/log")
	router := middleware.RootRouters["/log"]
	requests := make([][]*http.Request, writers)
	for w := 0; w < writers; w++ {
		for i := 0; i < perWriter; i++ {
			req := httptest.NewRequest("POST", "/log/request/headers/ignore/"+names[w*perWriter+i], nil)
			_, req, _ = util.WithRequestStore(req, nil)
			requests[w] = append(requests[w], req)
		}
	}
	var wg sync.WaitGroup
	statuses := make([]int, writers*perWriter)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i, req := range requests[w] {
				rr := httptest.NewRecorder()
				router.ServeHTTP(rr, req)
				statuses[w*perWriter+i] = rr.Code
				util.IsHeaderExcluded(names[(w*perWriter+i+7)%len(names)])
			}
		}(w)
	}
	wg.Wait()
	for i, code := range statuses {
		if code != http.StatusOK {
			t.Errorf("request %d (%s) answered %d", i, names[i], code)
		}
	}
	for _, name := range names {
		if !util.IsHeaderExcluded(name) {
			t.Errorf("header %q was not excluded", name)
		}
	}
}
