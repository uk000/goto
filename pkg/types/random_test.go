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

package types

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// expectNoPanic runs f and reports a panic as a test failure, so that one panicking call doesn't hide the other checks.
func expectNoPanic(t *testing.T, what string, f func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s panicked: %v", what, r)
		}
	}()
	f()
}

func TestRandomDurationWithinRange(t *testing.T) {
	cases := []struct {
		name     string
		min, max time.Duration
	}{
		{"milliseconds", 10 * time.Millisecond, 50 * time.Millisecond},
		{"seconds", time.Second, 3 * time.Second},
		{"from zero", 0, 5 * time.Millisecond},
		{"just over a millisecond", 5 * time.Millisecond, 6*time.Millisecond + time.Microsecond},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for i := 0; i < 500; i++ {
				if d := RandomDuration(c.min, c.max); d < c.min || d >= c.max {
					t.Fatalf("RandomDuration(%s, %s) = %s, want a duration in [min, max)", c.min, c.max, d)
				}
			}
		})
	}
}

func TestRandomDurationFixedAndZero(t *testing.T) {
	if d := RandomDuration(7*time.Millisecond, 7*time.Millisecond); d != 7*time.Millisecond {
		t.Errorf("RandomDuration(min == max) = %s, want %s", d, 7*time.Millisecond)
	}
	// An inverted range is not randomised: the minimum is used.
	if d := RandomDuration(9*time.Millisecond, 3*time.Millisecond); d != 9*time.Millisecond {
		t.Errorf("RandomDuration(min > max) = %s, want the min %s", d, 9*time.Millisecond)
	}
	if d := RandomDuration(0, 0); d != 0 {
		t.Errorf("RandomDuration(0, 0) = %s, want 0", d)
	}
	if d := RandomDuration(0, 0, 42*time.Millisecond); d != 42*time.Millisecond {
		t.Errorf("RandomDuration(0, 0, fallback) = %s, want the fallback %s", d, 42*time.Millisecond)
	}
	if d := RandomDuration(time.Millisecond, time.Millisecond, 42*time.Millisecond); d != time.Millisecond {
		t.Errorf("RandomDuration(1ms, 1ms, fallback) = %s, want the fixed duration, the fallback is only for 0-0", d)
	}
}

// A range that is narrower than a millisecond used to panic (rand.Int63n of 0).
func TestRandomDurationSubMillisecondRange(t *testing.T) {
	min, max := 100*time.Microsecond, 600*time.Microsecond
	seen := map[time.Duration]bool{}
	expectNoPanic(t, "RandomDuration of a range narrower than a millisecond", func() {
		for i := 0; i < 2000; i++ {
			d := RandomDuration(min, max)
			if d < min || d >= max {
				t.Fatalf("RandomDuration(%s, %s) = %s, want a duration in [min, max)", min, max, d)
			}
			seen[d] = true
		}
	})
	if len(seen) < 2 {
		t.Errorf("RandomDuration(%s, %s) gave the same duration %v every time, want it to be random", min, max, seen)
	}
	// The narrowest range there is: min and max are a nanosecond apart.
	expectNoPanic(t, "RandomDuration of a 1ns range", func() {
		if d := RandomDuration(time.Second, time.Second+1); d != time.Second {
			t.Errorf("RandomDuration(1s, 1s+1ns) = %s, want 1s, as max is exclusive", d)
		}
	})
}

// Random(0) and Random64(0) used to panic, as did RandomFrom of no values.
func TestRandomOfNothingIsZero(t *testing.T) {
	expectNoPanic(t, "Random(0)", func() {
		if r := Random(0); r != 0 {
			t.Errorf("Random(0) = %d, want 0", r)
		}
	})
	expectNoPanic(t, "Random(-5)", func() {
		if r := Random(-5); r != 0 {
			t.Errorf("Random(-5) = %d, want 0", r)
		}
	})
	expectNoPanic(t, "Random64(0)", func() {
		if r := Random64(0); r != 0 {
			t.Errorf("Random64(0) = %d, want 0", r)
		}
	})
	expectNoPanic(t, "Random64(-5)", func() {
		if r := Random64(-5); r != 0 {
			t.Errorf("Random64(-5) = %d, want 0", r)
		}
	})
	expectNoPanic(t, "RandomFrom(nil)", func() {
		if r := RandomFrom(nil); r != 0 {
			t.Errorf("RandomFrom(nil) = %d, want 0", r)
		}
	})
	expectNoPanic(t, "RandomFrom(empty)", func() {
		if r := RandomFrom([]int{}); r != 0 {
			t.Errorf("RandomFrom(empty) = %d, want 0", r)
		}
	})
}

func TestRandomBounds(t *testing.T) {
	counts := map[int]int{}
	for i := 0; i < 3000; i++ {
		r := Random(3)
		if r < 0 || r > 2 {
			t.Fatalf("Random(3) = %d, want 0..2", r)
		}
		counts[r]++
		if r64 := Random64(1000); r64 < 0 || r64 >= 1000 {
			t.Fatalf("Random64(1000) = %d, want 0..999", r64)
		}
		if r := Random(1); r != 0 {
			t.Fatalf("Random(1) = %d, want 0", r)
		}
	}
	for v := 0; v < 3; v++ {
		if counts[v] == 0 {
			t.Errorf("Random(3) never gave %d in 3000 calls: %v", v, counts)
		}
	}
}

func TestRandomFromPicksFromTheValues(t *testing.T) {
	if r := RandomFrom([]int{503}); r != 503 {
		t.Errorf("RandomFrom of a single value = %d, want 503", r)
	}
	vals := []int{200, 404, 503}
	seen := map[int]bool{}
	for i := 0; i < 1000; i++ {
		r := RandomFrom(vals)
		if r != 200 && r != 404 && r != 503 {
			t.Fatalf("RandomFrom(%v) = %d, want one of the values", vals, r)
		}
		seen[r] = true
	}
	if len(seen) != len(vals) {
		t.Errorf("RandomFrom(%v) gave only %v in 1000 calls, want all the values to be picked", vals, seen)
	}
}

// The random generator is shared by all the requests of the server, so the functions are called concurrently. This
// test is for the race detector (go test -race): the generator that backs them used to be shared without a lock.
func TestRandomIsSafeForConcurrentUse(t *testing.T) {
	const goroutines, iterations = 16, 300
	var wg sync.WaitGroup
	var mu sync.Mutex
	var problems []string
	report := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		problems = append(problems, fmt.Sprintf(format, args...))
	}
	start := make(chan struct{})
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < iterations; i++ {
				if r := Random(10); r < 0 || r >= 10 {
					report("Random(10) = %d", r)
				}
				if r := Random64(10); r < 0 || r >= 10 {
					report("Random64(10) = %d", r)
				}
				if r := RandomFrom([]int{1, 2, 3}); r < 1 || r > 3 {
					report("RandomFrom = %d", r)
				}
				if d := RandomDuration(time.Millisecond, 3*time.Millisecond); d < time.Millisecond || d >= 3*time.Millisecond {
					report("RandomDuration = %s", d)
				}
				if p := GenerateRandomPayload(16); len(p) != 16 {
					report("GenerateRandomPayload(16) has %d bytes", len(p))
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	if len(problems) > 0 {
		t.Errorf("%d bad results from concurrent use, first: %s", len(problems), problems[0])
	}
}

func TestGenerateRandomPayload(t *testing.T) {
	for _, size := range []int{0, 1, 10, 1000} {
		p := GenerateRandomPayload(size)
		if len(p) != size {
			t.Errorf("GenerateRandomPayload(%d) has %d bytes, want %d", size, len(p), size)
		}
		for _, b := range p {
			if !strings.ContainsRune(charset, rune(b)) {
				t.Errorf("GenerateRandomPayload(%d) has the byte %q that is not in the charset", size, b)
				break
			}
		}
	}
	if s := GenerateRandomString(32); len(s) != 32 {
		t.Errorf("GenerateRandomString(32) has %d characters, want 32", len(s))
	}
	if a, b := GenerateRandomString(64), GenerateRandomString(64); a == b {
		t.Errorf("two random strings of 64 characters are the same: %s", a)
	}
}

func TestGenerateRandomPayloadWithPrefix(t *testing.T) {
	p := GenerateRandomPayload(20, "label:")
	if len(p) != 20 || !strings.HasPrefix(string(p), "label:") {
		t.Errorf("GenerateRandomPayload(20, label:) = %q, want 20 bytes that start with the prefix", p)
	}
	p = GenerateRandomPayload(10, "ab", "cd")
	if len(p) != 10 || !strings.HasPrefix(string(p), "abcd") {
		t.Errorf("GenerateRandomPayload(10, ab, cd) = %q, want 10 bytes that start with abcd", p)
	}
	if p = GenerateRandomPayload(6, "abcdef"); string(p) != "abcdef" {
		t.Errorf("GenerateRandomPayload(6, abcdef) = %q, want the prefix alone, it fills the size", p)
	}
}

func TestIDIsUniqueAndIncreasing(t *testing.T) {
	first := ID()
	second := ID()
	if second != first+1 {
		t.Errorf("ID() gave %d after %d, want the next number", second, first)
	}
	const goroutines, each = 8, 100
	ids := make(chan int, goroutines*each)
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				ids <- ID()
			}
		}()
	}
	wg.Wait()
	close(ids)
	seen := map[int]bool{}
	for id := range ids {
		if seen[id] {
			t.Fatalf("ID() gave %d twice", id)
		}
		seen[id] = true
	}
	if len(seen) != goroutines*each {
		t.Errorf("got %d distinct ids, want %d", len(seen), goroutines*each)
	}
}
