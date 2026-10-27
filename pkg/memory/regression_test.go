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

package memory

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"goto/pkg/util"
)

// Every call that adds an extraction took the lock of the manager and then took it again to get the extractor, so the
// first call blocked for good.
func TestRegression_AddingExtractionsDoesNotDeadlock(t *testing.T) {
	adds := map[string]func(*MemoryManager){
		"context match": func(m *MemoryManager) { m.AddContextMatch("k", "X-Ctx") },
		"header":        func(m *MemoryManager) { m.AddHeaderMatch("k", "X-H") },
		"query":         func(m *MemoryManager) { m.AddQueryMatch("k", "q") },
		"regex":         func(m *MemoryManager) { m.AddBodyRegexMatch("k", "r", "x+") },
		"json path":     func(m *MemoryManager) { m.AddBodyJsonPathMatch("k", "a={.a}") },
		"transform": func(m *MemoryManager) {
			m.SetBodyTransformation("k", []*util.Transform{{Payload: map[string]any{"a": ""}}})
		},
		"get extractor": func(m *MemoryManager) { m.GetOrAddContextExtractor("k") },
	}
	for name, add := range adds {
		t.Run(name, func(t *testing.T) {
			m := newManager(t)
			within(t, "adding the "+name, func() { add(m) })
			if mx, _ := m.GetContextExtractors("k").(*MemoryExtractor); mx == nil {
				t.Errorf("the extractor of the %s was not added", name)
			}
		})
	}
}

// The extraction held the read lock of the manager while it added the context of a request that it hadn't seen, which
// needs the write lock.
func TestRegression_ExtractingIntoANewContextDoesNotDeadlock(t *testing.T) {
	m := newManager(t)
	within(t, "setup", func() {
		m.AddHeaderMatch("k", "X-H")
		m.AddQueryMatch("k", "q")
		m.AddBodyRegexMatch("k", "r", "x+")
	})
	within(t, "extracting from headers", func() { m.ExtractFromHeaders("k", "new1", http.Header{"X-H": {"v"}}) })
	within(t, "extracting from the query", func() { m.ExtractFromQuery("k", "new2", url.Values{"q": {"v"}}) })
	within(t, "extracting from the body", func() { m.ExtractFromBody("k", "new3", body("xx"), false) })
	for ctx, want := range map[string][2]string{"new1": {"X-H", "v"}, "new2": {"q", "v"}, "new3": {"r", "xx"}} {
		if got := m.GetOrAddContext(ctx).Get(want[0]); got != want[1] {
			t.Errorf("%s: %s = %q, want %q", ctx, want[0], got, want[1])
		}
	}
}

// Two callers that found no memory for a context both added one, and the later one replaced the other, which dropped
// the items that the first had stored in it.
func TestRegression_GetOrAddContextYieldsOneMemoryUnderContention(t *testing.T) {
	for round := 0; round < 20; round++ {
		m := newManager(t)
		const workers = 24
		got := make([]*Memory, workers)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				got[i] = m.GetOrAddContext("shared")
				got[i].Add(fmt.Sprintf("k%d", i), "v")
			}()
		}
		close(start)
		wg.Wait()
		for i := 1; i < workers; i++ {
			if got[i] != got[0] {
				t.Fatalf("round %d: workers got different memories for one context", round)
			}
		}
		if n := len(m.GetOrAddContext("shared").Items); n != workers {
			t.Fatalf("round %d: items kept = %d, want %d (items of a worker were lost)", round, n, workers)
		}
	}
}

// Adding an item through the API replaced the memory of the context, so only the latest item stayed.
func TestRegression_AddingAnItemKeepsTheOtherItemsOfTheContext(t *testing.T) {
	m, port := registeredManager(t)
	wantStatus(t, call(t, port, "POST", "/memory/context/c/add/k1=v1", ""), 200)
	wantStatus(t, call(t, port, "POST", "/memory/context/c/add/k2=v2", ""), 200)
	wantStatus(t, call(t, port, "POST", "/memory/context/c/add/k1=v1b", ""), 200)
	mem := m.GetOrAddContext("c")
	if mem.Get("k1") != "v1b" || mem.Get("k2") != "v2" || len(mem.Items) != 2 {
		t.Errorf("items = %v, want k1=v1b and k2=v2", mem.Items)
	}
}

// A regex that doesn't compile panicked in the handler of the request, which leaves the caller without a response.
func TestRegression_InvalidRegexIsRefused(t *testing.T) {
	m, port := registeredManager(t)
	rr := call(t, port, "POST", "/memory/context/k/memory/from/body/regex/bad=(unclosed", "")
	wantStatus(t, rr, 400)
	if !strings.Contains(rr.Body.String(), "invalid regex") {
		t.Errorf("body = %q, want the reason", rr.Body.String())
	}
	if mx, _ := m.GetContextExtractors("k").(*MemoryExtractor); mx != nil && len(mx.BodyRegexMatches) != 0 {
		t.Errorf("regexes = %v, want none to be added", mx.BodyRegexMatches)
	}
}

// The conditions of the mappings are compiled with MustCompile, which panics for a condition that isn't a regex.
func TestRegression_InvalidTransformConditionIsRefused(t *testing.T) {
	m, port := registeredManager(t)
	payload := `[{"mappings":[{"source":"a","target":"b","ifContains":"(unclosed"}]}]`
	rr := call(t, port, "POST", "/memory/context/k/memory/from/body/transform", payload)
	wantStatus(t, rr, 400)
	if !strings.Contains(rr.Body.String(), "invalid condition") {
		t.Errorf("body = %q, want the reason", rr.Body.String())
	}
	if m.HasAnyMatches() {
		t.Error("a transform that was refused must not be kept")
	}
	payload = `[{"mappings":[{"source":"a","target":"b","ifNotContains":"x["}]}]`
	wantStatus(t, call(t, port, "POST", "/memory/context/k/memory/from/body/transform", payload), 400)
	// conditions that are valid are accepted, and are applied
	payload = `[{"payload":{"b":""},"mappings":[{"source":"a","target":"b","mode":"replace","ifContains":"^ok"}]}]`
	wantStatus(t, call(t, port, "POST", "/memory/context/k/memory/from/body/transform", payload), 200)
	m.AddBodyJsonPathMatch("k", "b={.b}")
	within(t, "extracting", func() {
		m.ExtractFromBody("k", "match", body(`{"a":"ok-1"}`), false)
		m.ExtractFromBody("k", "nomatch", body(`{"a":"nope"}`), false)
	})
	if got := m.GetOrAddContext("match").Get("b"); got != "ok-1" {
		t.Errorf("b = %q, want the value that the condition lets through", got)
	}
	if got := m.GetOrAddContext("nomatch").Get("b"); got != "" {
		t.Errorf("b = %q, want nothing, the condition doesn't hold", got)
	}
}

// Init cleared the extractors, but the manager kept saying that it had matches.
func TestRegression_InitResetsTheMatches(t *testing.T) {
	m := newManager(t)
	within(t, "setup", func() { m.AddHeaderMatch("k", "X-H") })
	if !m.HasAnyMatches() {
		t.Fatal("the manager must have matches")
	}
	m.Init()
	if m.HasAnyMatches() {
		t.Error("a manager that was cleared has no matches")
	}
}

// The extractors and the memory are reported while extractions are being added and memory is being stored, which the
// race detector reports when what is reported is what's being changed.
func TestRegression_ReportingWhileUpdating(t *testing.T) {
	m, port := registeredManager(t)
	stop := make(chan struct{})
	var writers, readers sync.WaitGroup
	var failures sync.Map
	for w := 0; w < 3; w++ {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				m.AddHeaderMatch("k", fmt.Sprintf("X-H%d", w))
				m.AddBodyRegexMatch("k", fmt.Sprintf("r%d", w), "x+")
				m.GetOrAddContext("c").Add(fmt.Sprintf("k%d-%d", w, i%5), "v")
				m.GetOrAddContext(fmt.Sprintf("c%d", i%7)).Add("x", "y")
			}
		}()
	}
	for r := 0; r < 3; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for i := 0; i < 30; i++ {
				for _, target := range []string{"/memory/context/k", "/memory/contexts", "/memory/context/c/memory", "/memory/contexts/memory"} {
					if rr := call2(port, target); rr != 200 {
						failures.Store(target, rr)
					}
				}
			}
		}()
	}
	readers.Wait()
	close(stop)
	writers.Wait()
	failures.Range(func(k, v any) bool {
		t.Errorf("GET %v = %v, want 200", k, v)
		return true
	})
}

// call2 is call for goroutines other than the test's, which report the status instead of failing the test.
func call2(port int, target string) int {
	rr := serveForGoroutine(port, "GET", target)
	return rr
}

func serveForGoroutine(port int, method, target string) int {
	h := router()
	req := newAPIRequest(method, target, port)
	rr := httptest.NewRecorder()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		h.ServeHTTP(rr, req)
	}()
	select {
	case <-finished:
		return rr.Code
	case <-time.After(10 * time.Second):
		return -1
	}
}
