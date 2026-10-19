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

package client

import (
	"fmt"
	"sync"
	"testing"
)

// A request for a context that does not exist must not take the current client away.
func TestSwitchToMissingContextKeepsCurrentClient(t *testing.T) {
	isolate(t)
	CreateK8sClientForConfig("current", "http://127.0.0.1:8010", "")
	CreateK8sClientForConfig("other", "http://127.0.0.1:8011", "")
	if !SetCurrentK8sClient("current") {
		t.Fatal("could not switch to a client that exists")
	}
	want := CurrentClient
	if SetCurrentK8sClient("missing") {
		t.Error("switched to a client that does not exist")
	}
	if CurrentClient == nil || CurrentClient != want {
		t.Errorf("current client = %s after a failed switch, want it to stay %s", desc(CurrentClient), desc(want))
	}
}

// The admin APIs add clients and switch between them while other requests are served, so the clients need to be kept
// with the care that this takes (this is meant to be run with the race detector).
func TestClientsAreConfiguredAndSwitchedConcurrently(t *testing.T) {
	isolate(t)
	const workers, perWorker = 8, 10
	var wg sync.WaitGroup
	errs := make(chan error, workers*perWorker)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				name := fmt.Sprintf("worker-%d-%d", w, i)
				if err := CreateK8sClientForConfig(name, "http://127.0.0.1:8012", ""); err != nil {
					errs <- err
				}
				SetCurrentK8sClient(name)
				SetCurrentK8sClient("not-there")
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	lock.RLock()
	defer lock.RUnlock()
	count := 0
	for name := range Clients {
		if len(name) > 7 && name[:7] == "worker-" {
			count++
		}
	}
	if count != workers*perWorker {
		t.Errorf("%d clients were configured, want %d", count, workers*perWorker)
	}
}
