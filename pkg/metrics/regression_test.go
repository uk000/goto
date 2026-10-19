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

package metrics

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"goto/pkg/server/middleware"
	"goto/pkg/util"
)

// The stats are counted into by goroutines, while the stats API reports them. Encoding the maps of the stats while a
// goroutine adds to them is a data race on the maps (a fatal error, in the server), so the API has to report a copy
// that it makes with the counting held off. This is meant to be run with the race detector.
func TestReportingStatsWhileRequestsAreCounted(t *testing.T) {
	clean(t)
	admin("/stats", "GET", "/stats")
	router := middleware.RootRouters["/stats"]
	const requests = 400
	stop := make(chan struct{})
	var readers sync.WaitGroup
	var mu sync.Mutex
	var failures []string
	for r := 0; r < 2; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				req := httptest.NewRequest("GET", "/stats", nil)
				_, req, _ = util.WithRequestStore(req, nil)
				rr := httptest.NewRecorder()
				router.ServeHTTP(rr, req)
				var stats map[string]json.RawMessage
				if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &stats) != nil {
					mu.Lock()
					failures = append(failures, fmt.Sprintf("answered %d %q", rr.Code, rr.Body.String()))
					mu.Unlock()
				}
			}
		}()
	}
	for i := 0; i < requests; i++ {
		port := fmt.Sprint(8000 + i%3)
		UpdateHeaderRequestCount(port, "/uri", "x-header", fmt.Sprint("v", i%5))
		UpdateURIRequestCount("/uri", "200")
		UpdatePortRequestCount(port, "/uri")
		UpdateProtocolRequestCount("http", "/uri")
	}
	waitStats(t, "all requests to be counted", func(s *ServerStats) bool {
		return s.RequestCountsByURIs["/uri"] == requests && s.RequestCountsByHeaders["x-header"] == requests &&
			s.RequestCountsByURIsAndProtocol["/uri"]["http"] == requests
	})
	close(stop)
	readers.Wait()
	for _, f := range failures {
		t.Error(f)
	}
	total := 0
	for _, count := range statsSnapshot().RequestCountsByPortAndHeaders["8000"] {
		total += count
	}
	if total == 0 {
		t.Error("nothing was counted for the first port")
	}
}
