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

package hooks

import (
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

// The ports of the tests of this file, apart from the ones of the other tests (30000 and up).
var regressionPortSeq atomic.Int32

func freshPort() int {
	return int(50000 + regressionPortSeq.Add(1))
}

func TestGetPortHooksIsTheSameForAPort(t *testing.T) {
	port := freshPort()
	h := GetPortHooks(port)
	if h == nil || h.port != port {
		t.Fatalf("GetPortHooks(%d) = %+v, want the hooks of the port", port, h)
	}
	if again := GetPortHooks(port); again != h {
		t.Errorf("GetPortHooks(%d) gave other hooks the second time", port)
	}
	other := GetPortHooks(freshPort())
	if other == h || other.port == port {
		t.Errorf("the hooks of another port are the same as the ones of port %d", port)
	}
	if err := h.AddHTTPHookWithListener("k", "id", "/a", nil, false, nil); err != nil {
		t.Fatal(err)
	}
	if got := GetPortHooks(port); len(got.httpHooks) != 1 {
		t.Errorf("the hooks of port %d have %d http hooks, want the 1 that was added", port, len(got.httpHooks))
	}
}

// The first use of a port by several requests at once made hooks of the same port: every caller that found none made its
// own and put it in place of the ones before. The hooks that were added to the replaced ones were lost (and the map was
// read while it was written). Run with -race.
func TestGetPortHooksConcurrentFirstUseKeepsEveryHook(t *testing.T) {
	const ports, callers = 40, 16
	for p := 0; p < ports; p++ {
		port := freshPort()
		start := make(chan struct{})
		var wg sync.WaitGroup
		gotHooks := make([]*Hooks, callers)
		addErrors := make([]error, callers)
		for i := 0; i < callers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				h := GetPortHooks(port)
				gotHooks[i] = h
				addErrors[i] = h.AddHTTPHookWithListener("key", fmt.Sprintf("hook-%d", i), fmt.Sprintf("/uri%d", i), nil, false, nil)
			}()
		}
		close(start)
		wg.Wait()
		final := GetPortHooks(port)
		for i := 0; i < callers; i++ {
			if addErrors[i] != nil {
				t.Fatalf("port %d: adding hook %d failed: %v", port, i, addErrors[i])
			}
			if gotHooks[i] != final {
				t.Errorf("port %d: caller %d was given other hooks than the ones that the port has now", port, i)
			}
		}
		final.lock.RLock()
		present := map[string]bool{}
		for id := range final.httpHooks {
			present[id] = true
		}
		final.lock.RUnlock()
		for i := 0; i < callers; i++ {
			if !present[fmt.Sprintf("hook-%d", i)] {
				t.Errorf("port %d: hook-%d was lost, the port has %d of %d hooks", port, i, len(present), callers)
				break
			}
		}
		if len(present) != callers {
			t.Errorf("port %d has %d hooks, want %d", port, len(present), callers)
		}
	}
}

// A header that is given without a value is matched whatever its value is (when it is there at all). It never matched
// as the matcher has an empty, not a missing, map of values.
func TestHeaderHookWithoutValuesMatchesThePresenceOfTheHeader(t *testing.T) {
	h := GetPortHooks(freshPort())
	if err := h.AddHTTPTracking("names", "/api", Headers{{"X-Track", ""}, {"x-other", ""}}, false); err != nil {
		t.Fatal(err)
	}
	all, matched := h.MatchHTTPRequest("/api/call", map[string][]string{
		"X-Track":  {"v1", "v2"},
		"X-Other":  {""},
		"X-Unseen": {"z"},
	})
	if len(all) != 1 {
		t.Fatalf("the hook matched %d times, want once: %v", len(all), all)
	}
	want := map[string]map[string][]string{"names": {"X-Track": {"v1"}, "X-Other": {""}}}
	if !reflect.DeepEqual(matched, want) {
		t.Errorf("matched headers = %v, want %v: the headers that the hook is given, with their first value", matched, want)
	}

	// Without the header there is nothing to report, but the URI still matches.
	all, matched = h.MatchHTTPRequest("/api/call", map[string][]string{"X-Unseen": {"z"}})
	if len(all) != 1 || len(matched) != 0 {
		t.Errorf("without the headers: matches = %d, matched headers = %v, want the URI to match and no headers", len(all), matched)
	}

	// The URI has to match for the headers to count.
	all, matched = h.MatchHTTPRequest("/elsewhere", map[string][]string{"X-Track": {"v1"}})
	if len(all) != 0 || len(matched) != 0 {
		t.Errorf("another URI: matches = %d, matched headers = %v, want none", len(all), matched)
	}

	// The names of the headers are matched without regard to case.
	_, matched = h.MatchHTTPRequest("/api", map[string][]string{"x-track": {"lower"}})
	if want := map[string]map[string][]string{"names": {"x-track": {"lower"}}}; !reflect.DeepEqual(matched, want) {
		t.Errorf("lower case header: matched headers = %v, want %v", matched, want)
	}
}

// What a hook that is given the headers with values matched before and still does: the values are the ones that count,
// and the glob matches any.
func TestHeaderHookWithValuesMatchesTheValues(t *testing.T) {
	h := GetPortHooks(freshPort())
	if err := h.AddHTTPTracking("exact", "/api", Headers{{"X-Mode", "fast"}}, false); err != nil {
		t.Fatal(err)
	}
	if err := h.AddHTTPTracking("glob", "/api", Headers{{"X-Mode", "*"}}, false); err != nil {
		t.Fatal(err)
	}
	_, matched := h.MatchHTTPRequest("/api", map[string][]string{"X-Mode": {"fast"}})
	want := map[string]map[string][]string{"exact": {"X-Mode": {"fast"}}, "glob": {"X-Mode": {"fast"}}}
	if !reflect.DeepEqual(matched, want) {
		t.Errorf("X-Mode: fast: matched headers = %v, want %v", matched, want)
	}
	_, matched = h.MatchHTTPRequest("/api", map[string][]string{"X-Mode": {"slow"}})
	want = map[string]map[string][]string{"glob": {"X-Mode": {"slow"}}}
	if !reflect.DeepEqual(matched, want) {
		t.Errorf("X-Mode: slow: matched headers = %v, want %v: only the glob matches any value", matched, want)
	}
}
