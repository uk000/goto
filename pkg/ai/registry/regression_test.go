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

package registry

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	a2amodel "goto/pkg/ai/a2a/model"
)

func noPanic(t *testing.T, what string, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s panicked: %v", what, r)
		}
	}()
	fn()
}

// waitWithin waits for the goroutines of the group, and fails the test when they don't finish in time, which is how a
// deadlock shows up.
func waitWithin(t *testing.T, wg *sync.WaitGroup, d time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("the goroutines did not finish within %s, they are deadlocked", d)
	}
}

// An agent without a card has no name to be registered with. Adding it used to panic.
func TestRegressionAddAgentWithoutCard(t *testing.T) {
	r := newAgentRegistry()
	noPanic(t, "AddAgent without a card", func() { r.AddAgent(&a2amodel.Agent{ID: "no-card"}, 1) })
	noPanic(t, "AddAgent of nil", func() { r.AddAgent(nil, 1) })
	noPanic(t, "AddAgents with invalid agents", func() {
		r.AddAgents([]*a2amodel.Agent{nil, {ID: "no-card"}, newAgent("valid")}, 1)
	})
	if got := agentNames(r); len(got) != 1 || got[0] != "valid" {
		t.Errorf("names = %v, want only the valid agent to be registered", got)
	}
}

// The same through the API, where a payload of agents without cards panicked while the names were listed.
func TestRegressionAddAgentsAPIWithoutCards(t *testing.T) {
	resetRegistry(t)
	for _, body := range []string{`[{}]`, `[{"id":"x"}]`, `[null]`, `[{"card":{"name":""}}]`} {
		var rr *httptest.ResponseRecorder
		noPanic(t, "POST /ai/agents/add "+body, func() { rr = call(t, "POST", "/ai/agents/add", body, nextPort()) })
		if rr == nil {
			continue
		}
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", body, rr.Code)
		}
		if got := rr.Body.String(); !strings.HasPrefix(got, "Failed to add agents") {
			t.Errorf("%s: body = %q", body, got)
		}
	}
	// A bad agent keeps the good ones of the payload from being registered, as the request failed.
	var rr *httptest.ResponseRecorder
	noPanic(t, "POST of a good and a bad agent", func() {
		rr = call(t, "POST", "/ai/agents/add", `[{"card":{"name":"good"}},{}]`, nextPort())
	})
	if rr != nil && rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
	if got := agentNames(TheAgentRegistry); len(got) != 0 {
		t.Errorf("registry = %v, want nothing registered by failed requests", got)
	}
}

// GetAgents handed out the map that the registry changes, so that callers raced with the registry, and could change it.
func TestRegressionGetAgentsReturnsACopy(t *testing.T) {
	r := newAgentRegistry()
	r.AddAgent(newAgent("alpha"), 1)
	got := r.GetAgents("alpha")
	got[2] = newAgent("alpha")
	delete(got, 1)
	if now := r.GetAgents("alpha"); len(now) != 1 || now[1] == nil || now[2] != nil {
		t.Errorf("agents = %+v, want the registry not to follow changes of what it returned", now)
	}
}

func TestRegressionConcurrentReadsAndWrites(t *testing.T) {
	r := newAgentRegistry()
	const workers, perWorker = 6, 60
	var wg sync.WaitGroup
	var readFailures sync.Map
	for w := 0; w < workers; w++ {
		wg.Add(3)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				r.AddAgent(newAgent("shared"), w*1000+i)
			}
		}(w)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				for port, a := range r.GetAgents("shared") {
					if a == nil || a.Card.Name != "shared" || port < 0 {
						readFailures.Store(fmt.Sprintf("reader %d, port %d", w, port), fmt.Sprintf("%+v", a))
						return
					}
				}
			}
		}(w)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				r.AddAgent(newAgent(fmt.Sprintf("own-%d", w)), i)
				if i%20 == 19 {
					r.RemoveAgent(fmt.Sprintf("own-%d", w))
				}
			}
		}(w)
	}
	waitWithin(t, &wg, 30*time.Second)
	readFailures.Range(func(k, v any) bool {
		t.Errorf("%v saw the agent %v", k, v)
		return true
	})
	if got := len(r.GetAgents("shared")); got != workers*perWorker {
		t.Errorf("shared agents = %d, want %d", got, workers*perWorker)
	}
}

// The clear API read the registry and replaced it without holding its lock, so it raced with everything else.
func TestRegressionClearAgentsAPIConcurrentlyWithOtherRequests(t *testing.T) {
	resetRegistry(t)
	stubOnce.Do(stubGlobalFuncs)
	const workers, perWorker = 6, 25
	jobs := make([][]*http.Request, workers)
	for w := range jobs {
		for i := 0; i < perWorker; i++ {
			switch {
			case w%3 == 0:
				jobs[w] = append(jobs[w], newRequest("POST", "/ai/agents/clear", "", nextPort()))
			case w%3 == 1:
				jobs[w] = append(jobs[w], newRequest("POST", "/ai/agents/add", agentsJSON(fmt.Sprintf("conc-%d-%d", w, i)), nextPort()))
			default:
				jobs[w] = append(jobs[w], newRequest("GET", "/ai/agents", "", nextPort()))
			}
		}
	}
	codes := make([][]int, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for _, req := range jobs[w] {
				rr := httptest.NewRecorder()
				router().ServeHTTP(rr, req)
				codes[w] = append(codes[w], rr.Code)
			}
		}(w)
	}
	waitWithin(t, &wg, 30*time.Second)
	for w, cs := range codes {
		for i, c := range cs {
			if c != http.StatusOK {
				t.Errorf("worker %d request %d = %d, want 200", w, i, c)
			}
		}
	}
	TheAgentRegistry.Clear()
	if got := agentNames(TheAgentRegistry); len(got) != 0 {
		t.Errorf("registry = %v after the final clear", got)
	}
}

// The API to list the agents answered with an empty body, whatever was registered.
func TestRegressionGetAgentsAPIIsNotEmpty(t *testing.T) {
	resetRegistry(t)
	port := nextPort()
	call(t, "POST", "/ai/agents/add", agentsJSON("regr-listed"), port)
	rr := call(t, "GET", "/ai/agents", "", port)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "regr-listed") {
		t.Errorf("GET /ai/agents = %d %q, want the registered agent", rr.Code, rr.Body.String())
	}
}
