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
	"fmt"
	"strconv"
	"sync"
	"testing"

	"goto/pkg/constants"
)

// The count that a probe reported was read after the lock was released, so probes that came together reported the
// count of each other, and some counts were reported twice, and some never.
func TestConcurrentProbesReportTheirOwnCount(t *testing.T) {
	e := newEnv(t)
	const workers, each = 8, 40
	counts := make(chan string, 2*workers*each)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				counts <- "r" + e.traffic("/ready").Result().Header.Get(constants.HeaderReadinessRequestCount)
				counts <- "l" + e.traffic("/live").Result().Header.Get(constants.HeaderLivenessRequestCount)
			}
		}()
	}
	wg.Wait()
	close(counts)
	seen := map[string]int{}
	for c := range counts {
		seen[c]++
	}
	for _, kind := range []string{"r", "l"} {
		for n := 1; n <= workers*each; n++ {
			if got := seen[kind+strconv.Itoa(n)]; got != 1 {
				t.Errorf("%s count %d was reported %d times, want once", kind, n, got)
				return
			}
		}
	}
}

// The probe URIs were read to tell the probes without the lock that changes them hold.
func TestChangingTheProbesWhileProbingDoesNotRace(t *testing.T) {
	e := newEnv(t)
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			e.api("POST", fmt.Sprintf("/probes/readiness/set?uri=/ready%d", i%3))
			e.api("POST", fmt.Sprintf("/probes/liveness/set?uri=/live%d", i%3))
			e.api("POST", "/probes/readiness/set/status=200:1")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			e.traffic(fmt.Sprintf("/ready%d", i%3))
			e.traffic(fmt.Sprintf("/live%d", i%3))
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			e.api("GET", "/probes")
			e.api("POST", "/probes/counts/clear")
		}
	}()
	wg.Wait()
}
