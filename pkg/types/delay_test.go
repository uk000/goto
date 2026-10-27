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
	"encoding/json"
	"runtime"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func ms(n int) time.Duration {
	return time.Duration(n) * time.Millisecond
}

func TestParseDurationRange(t *testing.T) {
	cases := []struct {
		in        string
		low, high time.Duration
		count     int
		ok        bool
	}{
		{"1s", time.Second, time.Second, 0, true},
		{"1s-2s", time.Second, 2 * time.Second, 0, true},
		{"2s-1s", time.Second, 2 * time.Second, 0, true},
		{"500ms-1s:3", ms(500), time.Second, 3, true},
		{"1s:5", time.Second, time.Second, 5, true},
		{"1s:0", time.Second, time.Second, 0, true},
		{"1s:-2", time.Second, time.Second, 0, true},
		{"1s:many", time.Second, time.Second, 0, true},
		{"1h30m-2h", 90 * time.Minute, 2 * time.Hour, 0, true},
		{"", 0, 0, 0, false},
		{"bad", 0, 0, 0, false},
		{"bad-1s", 0, 0, 0, false},
		{"-1s", 0, 0, 0, false},
		{"10", 0, 0, 0, false},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			low, high, count, ok := ParseDurationRange(c.in)
			if low != c.low || high != c.high || count != c.count || ok != c.ok {
				t.Errorf("ParseDurationRange(%q) = (%s, %s, %d, %t), want (%s, %s, %d, %t)", c.in, low, high, count, ok, c.low, c.high, c.count, c.ok)
			}
		})
	}
}

// A range whose upper bound can't be parsed is rejected as a whole. It used to be accepted with the upper bound 0, which
// made ParseDelay quietly return no delay for it, and the callers that don't check ok get no delay at all.
func TestParseDurationRangeRejectsAnUnparsableUpperBound(t *testing.T) {
	for _, in := range []string{"1s-bad", "1s-", "1s-bad:3", "1s-10"} {
		low, high, count, ok := ParseDurationRange(in)
		if ok || low != 0 || high != 0 || count != 0 {
			t.Errorf("ParseDurationRange(%q) = (%s, %s, %d, %t), want (0s, 0s, 0, false)", in, low, high, count, ok)
		}
		if d := ParseDelay(in); d != nil {
			t.Errorf("ParseDelay(%q) = %v, want no delay", in, d)
		}
	}
}

func TestNewDelayAndParseDelay(t *testing.T) {
	d := NewDelay(ms(10), ms(20), 3)
	if d == nil || d.Min.Duration != ms(10) || d.Max.Duration != ms(20) || d.Count != 3 {
		t.Fatalf("NewDelay(10ms, 20ms, 3) = %+v, want min 10ms max 20ms count 3", d)
	}
	if d = NewDelay(ms(10), ms(20), 0); d == nil || d.Count != -1 {
		t.Errorf("NewDelay with count 0 = %+v, want the unlimited count -1", d)
	}
	if d = NewDelay(ms(10), ms(20), -1); d == nil || d.Count != -1 {
		t.Errorf("NewDelay with count -1 = %+v, want the unlimited count -1", d)
	}
	if d = NewDelay(ms(10), 0, 1); d != nil {
		t.Errorf("NewDelay without a max = %+v, want no delay", d)
	}
	if d = NewDelay(ms(10), ms(20), -2); d != nil {
		t.Errorf("NewDelay with count -2 = %+v, want no delay", d)
	}
	if d = ParseDelay("10ms-20ms:4"); d == nil || d.Min.Duration != ms(10) || d.Max.Duration != ms(20) || d.Count != 4 {
		t.Errorf("ParseDelay(10ms-20ms:4) = %+v, want min 10ms max 20ms count 4", d)
	}
	if d = ParseDelay("garbage"); d != nil {
		t.Errorf("ParseDelay(garbage) = %+v, want no delay", d)
	}
	if d = ParseDelay("0s"); d != nil {
		t.Errorf("ParseDelay(0s) = %+v, want no delay", d)
	}
}

func TestDelayStringIsNonZeroAndIsLargerThan(t *testing.T) {
	d := NewDelay(ms(10), time.Second, 3)
	if s := d.String(); s != "10ms-1s" {
		t.Errorf("String() = %q, want 10ms-1s", s)
	}
	if !d.IsNonZero() {
		t.Errorf("IsNonZero() = false for %s with 3 left", d)
	}
	d.Count = 0
	if d.IsNonZero() {
		t.Errorf("IsNonZero() = true for a delay with no count left")
	}
	d.Count = -1
	if !d.IsNonZero() {
		t.Errorf("IsNonZero() = false for an unlimited delay")
	}
	d.Count = -5
	if d.IsNonZero() {
		t.Errorf("IsNonZero() = true for a delay with the invalid count -5")
	}
	cases := []struct {
		name   string
		d, d2  *Delay
		larger bool
	}{
		{"larger max", NewDelay(ms(1), ms(20), -1), NewDelay(ms(5), ms(10), -1), true},
		{"smaller max", NewDelay(ms(5), ms(10), -1), NewDelay(ms(1), ms(20), -1), false},
		{"same max, larger min", NewDelay(ms(5), ms(10), -1), NewDelay(ms(2), ms(10), -1), true},
		{"same max, smaller min", NewDelay(ms(2), ms(10), -1), NewDelay(ms(5), ms(10), -1), false},
		{"same", NewDelay(ms(5), ms(10), -1), NewDelay(ms(5), ms(10), -1), false},
	}
	for _, c := range cases {
		if got := c.d.IsLargerThan(c.d2); got != c.larger {
			t.Errorf("%s: %s.IsLargerThan(%s) = %t, want %t", c.name, c.d, c.d2, got, c.larger)
		}
	}
}

// The delays of the payloads, tools and agents are optional, and so are their bounds. A delay that is not there, or that
// misses a bound, is no delay: it is not larger than anything, and every delay is larger than it.
func TestDelayIsNilSafe(t *testing.T) {
	var nilDelay *Delay
	noMax := &Delay{Min: &Duration{time.Second}}
	noMin := &Delay{Max: &Duration{time.Second}}
	empty := &Delay{}
	ranged := NewDelay(ms(1), ms(2), -1)
	none := []*Delay{nilDelay, noMax, noMin, empty}
	expectNoPanic(t, "String", func() {
		for _, d := range none {
			if s := d.String(); s != "" {
				t.Errorf("String() of %+v = %q, want an empty string", d, s)
			}
		}
	})
	expectNoPanic(t, "IsNonZero", func() {
		for _, d := range none {
			if d.IsNonZero() {
				t.Errorf("IsNonZero() of %+v = true, want false", d)
			}
		}
	})
	expectNoPanic(t, "IsLargerThan", func() {
		for _, d := range none {
			for _, other := range append([]*Delay{ranged}, none...) {
				if d.IsLargerThan(other) {
					t.Errorf("%+v.IsLargerThan(%+v) = true, want false: a missing delay is not larger than any", d, other)
				}
			}
			if !ranged.IsLargerThan(d) {
				t.Errorf("%s.IsLargerThan(%+v) = false, want true: it is larger than a missing delay", ranged, d)
			}
		}
	})
	expectNoPanic(t, "Compute/Apply/ComputeAndApply/Block/Prepare", func() {
		for _, d := range []*Delay{nilDelay, noMax, noMin, empty} {
			if got := d.Compute(); got != 0 {
				t.Errorf("Compute() of %+v = %s, want 0", d, got)
			}
			if got := d.Apply(); got != 0 {
				t.Errorf("Apply() of %+v = %s, want 0", d, got)
			}
			var seen []time.Duration
			if got := d.ComputeAndApply(func(x time.Duration) { seen = append(seen, x) }); got != 0 {
				t.Errorf("ComputeAndApply() of %+v = %s, want 0", d, got)
			}
			if len(seen) != 1 || seen[0] != 0 {
				t.Errorf("ComputeAndApply() of %+v called back with %v, want [0s]", d, seen)
			}
			d.Prepare()
			select {
			case got := <-d.Block():
				if got != 0 {
					t.Errorf("Block() of %+v gave %s, want 0", d, got)
				}
			case <-time.After(2 * time.Second):
				t.Errorf("Block() of %+v didn't deliver", d)
			}
		}
	})
}

func TestDelayPrepare(t *testing.T) {
	d := &Delay{Min: &Duration{5 * time.Second}, Max: &Duration{time.Second}}
	d.Prepare()
	if d.Min.Duration != time.Second || d.Max.Duration != time.Second || d.Count != -1 {
		t.Errorf("Prepare() of min 5s max 1s count 0 = %s count %d, want the min cut to the max 1s and the unlimited count -1", d, d.Count)
	}
	d = &Delay{Min: &Duration{ms(1)}, Max: &Duration{ms(2)}, Count: -7}
	d.Prepare()
	if d.Min.Duration != ms(1) || d.Count != -1 {
		t.Errorf("Prepare() of count -7 = %s count %d, want the unlimited count -1", d, d.Count)
	}
	d = &Delay{Min: &Duration{ms(1)}, Max: &Duration{ms(2)}, Count: 4}
	d.Prepare()
	if d.Count != 4 {
		t.Errorf("Prepare() of count 4 = count %d, want 4", d.Count)
	}
	d = &Delay{Max: &Duration{ms(2)}, Count: 2}
	expectNoPanic(t, "Prepare of a delay without a min", d.Prepare)
	if d.Min != nil || d.Max.Duration != ms(2) || d.Count != 2 {
		t.Errorf("Prepare() of a delay without a min changed it to %+v, want it left as it is", d)
	}
}

func TestDelayUnmarshalJSON(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		min, max time.Duration
		count    int
	}{
		{"strings", `{"min":"10ms","max":"20ms","count":2}`, ms(10), ms(20), 2},
		{"numbers are nanoseconds", `{"min":1000000,"max":2000000}`, ms(1), ms(2), -1},
		{"min above max is cut to max", `{"min":"1s","max":"10ms"}`, ms(10), ms(10), -1},
		{"no count is unlimited", `{"min":"1ms","max":"2ms"}`, ms(1), ms(2), -1},
		{"count below -1 is unlimited", `{"min":"1ms","max":"2ms","count":-4}`, ms(1), ms(2), -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var d Delay
			if err := json.Unmarshal([]byte(c.in), &d); err != nil {
				t.Fatalf("Unmarshal(%s) failed: %v", c.in, err)
			}
			if d.Min == nil || d.Max == nil || d.Min.Duration != c.min || d.Max.Duration != c.max || d.Count != c.count {
				t.Errorf("Unmarshal(%s) = %+v, want min %s max %s count %d", c.in, d, c.min, c.max, c.count)
			}
		})
	}
	var d Delay
	if err := json.Unmarshal([]byte(`{"min":"soon","max":"1s"}`), &d); err == nil {
		t.Errorf("Unmarshal of an invalid duration succeeded: %+v", d)
	}
	if err := json.Unmarshal([]byte(`{"min":true,"max":"1s"}`), &d); err == nil {
		t.Errorf("Unmarshal of a boolean duration succeeded: %+v", d)
	}
	if err := json.Unmarshal([]byte(`[1]`), &d); err == nil {
		t.Errorf("Unmarshal of an array succeeded: %+v", d)
	}
}

func TestDelayUnmarshalYAML(t *testing.T) {
	var d Delay
	if err := yaml.Unmarshal([]byte("min: 10ms\nmax: 20ms\ncount: 2\n"), &d); err != nil {
		t.Fatal(err)
	}
	if d.Min == nil || d.Max == nil || d.Min.Duration != ms(10) || d.Max.Duration != ms(20) || d.Count != 2 {
		t.Errorf("Unmarshal = %+v, want min 10ms max 20ms count 2", d)
	}
	d = Delay{}
	if err := yaml.Unmarshal([]byte("min: 1s\nmax: 10ms\n"), &d); err != nil {
		t.Fatal(err)
	}
	if d.Min.Duration != ms(10) || d.Max.Duration != ms(10) || d.Count != -1 {
		t.Errorf("Unmarshal of min 1s max 10ms = %s count %d, want the min cut to the max and the unlimited count", &d, d.Count)
	}
	d = Delay{}
	if err := yaml.Unmarshal([]byte("min: soon\nmax: 1s\n"), &d); err == nil {
		t.Errorf("Unmarshal of an invalid duration succeeded: %+v", d)
	}
}

// A delay that is given without its min or its max, in a request or in a config file, used to panic.
func TestDelayUnmarshalWithoutMinOrMaxDoesNotPanic(t *testing.T) {
	cases := []struct {
		name       string
		json, yaml string
		hasMin     bool
		hasMax     bool
		count      int
	}{
		{"only max", `{"max":"1s"}`, "max: 1s\n", false, true, -1},
		{"only min", `{"min":"1s"}`, "min: 1s\n", true, false, -1},
		{"only count", `{"count":3}`, "count: 3\n", false, false, 3},
		{"nothing", `{}`, "{}\n", false, false, -1},
	}
	for _, c := range cases {
		t.Run(c.name+" json", func(t *testing.T) {
			var d Delay
			expectNoPanic(t, "Unmarshal JSON", func() {
				if err := json.Unmarshal([]byte(c.json), &d); err != nil {
					t.Errorf("Unmarshal(%s) failed: %v", c.json, err)
				}
			})
			checkPartialDelay(t, &d, c.hasMin, c.hasMax, c.count)
		})
		t.Run(c.name+" yaml", func(t *testing.T) {
			var d Delay
			expectNoPanic(t, "Unmarshal YAML", func() {
				if err := yaml.Unmarshal([]byte(c.yaml), &d); err != nil {
					t.Errorf("Unmarshal(%q) failed: %v", c.yaml, err)
				}
			})
			checkPartialDelay(t, &d, c.hasMin, c.hasMax, c.count)
		})
	}
	// The same through the structs that hold the delays.
	type holder struct {
		Delay *Delay `yaml:"delay" json:"delay"`
	}
	var h holder
	expectNoPanic(t, "Unmarshal of a holder", func() {
		if err := json.Unmarshal([]byte(`{"delay":{"max":"1s"}}`), &h); err != nil {
			t.Errorf("Unmarshal of a holder failed: %v", err)
		}
	})
	if h.Delay == nil || h.Delay.Min != nil || h.Delay.Max == nil || h.Delay.Max.Duration != time.Second {
		t.Errorf("holder = %+v, want a delay with only the max", h.Delay)
	}
}

// What is missing stays missing, so that whoever validates the delay can tell (an upstream of the TCP proxy is refused for
// a delay without min or max), and the delay doesn't delay anything.
func checkPartialDelay(t *testing.T, d *Delay, hasMin, hasMax bool, count int) {
	t.Helper()
	if (d.Min != nil) != hasMin || (d.Max != nil) != hasMax {
		t.Errorf("delay = %+v, want min present: %t, max present: %t", d, hasMin, hasMax)
	}
	if d.Count != count {
		t.Errorf("delay count = %d, want %d", d.Count, count)
	}
	if d.IsNonZero() {
		t.Errorf("IsNonZero() = true for the partial delay %+v", d)
	}
	expectNoPanic(t, "Compute of a partial delay", func() {
		if got := d.Compute(); got != 0 {
			t.Errorf("Compute() of the partial delay %+v = %s, want 0", d, got)
		}
	})
}

func TestDelayMarshalOutputIsUnchanged(t *testing.T) {
	d := NewDelay(ms(10), time.Second, 3)
	check := func(when string) {
		t.Helper()
		j, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		if want := `{"min":"10ms","max":"1s","count":3}`; string(j) != want {
			t.Errorf("%s: JSON = %s, want %s", when, j, want)
		}
		y, err := yaml.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		if want := "min: 10ms\nmax: 1s\ncount: 3\n"; string(y) != want {
			t.Errorf("%s: YAML = %q, want %q", when, y, want)
		}
	}
	check("new")
	d.Compute() // state that is kept for the next Apply must not show in the output
	check("computed")
	j, _ := json.Marshal(NewDelay(ms(1), ms(2), 0))
	if want := `{"min":"1ms","max":"2ms","count":-1}`; string(j) != want {
		t.Errorf("unlimited JSON = %s, want %s", j, want)
	}
	j, _ = json.Marshal(&Delay{Max: &Duration{ms(5)}})
	if want := `{"max":"5ms"}`; string(j) != want {
		t.Errorf("partial JSON = %s, want %s", j, want)
	}
	// What is written is read back.
	var back Delay
	if err := json.Unmarshal([]byte(`{"min":"10ms","max":"1s","count":3}`), &back); err != nil {
		t.Fatal(err)
	}
	if back.String() != d.String() || back.Count != d.Count {
		t.Errorf("round trip = %s count %d, want %s count %d", &back, back.Count, d, d.Count)
	}
}

func TestDurationUnmarshal(t *testing.T) {
	var d Duration
	if err := json.Unmarshal([]byte(`"1m30s"`), &d); err != nil || d.Duration != 90*time.Second {
		t.Errorf("JSON string duration = %s, %v, want 1m30s", d, err)
	}
	if err := json.Unmarshal([]byte(`2000000`), &d); err != nil || d.Duration != ms(2) {
		t.Errorf("JSON number duration = %s, %v, want 2ms (nanoseconds)", d, err)
	}
	if err := json.Unmarshal([]byte(`{"a":1}`), &d); err == nil {
		t.Errorf("JSON object duration was accepted")
	}
	if err := yaml.Unmarshal([]byte(`1m30s`), &d); err != nil || d.Duration != 90*time.Second {
		t.Errorf("YAML string duration = %s, %v, want 1m30s", d, err)
	}
	b, err := json.Marshal(&Duration{ms(1500)})
	if err != nil || string(b) != `"1.5s"` {
		t.Errorf("JSON of 1500ms = %s, %v, want \"1.5s\"", b, err)
	}
	y, err := yaml.Marshal(&Duration{ms(1500)})
	if err != nil || string(y) != "1.5s\n" {
		t.Errorf("YAML of 1500ms = %q, %v, want 1.5s", y, err)
	}
}

// null is a no-op for every type that is unmarshalled from JSON, it must not be refused as an invalid duration.
func TestDurationUnmarshalJSONNullKeepsTheDuration(t *testing.T) {
	d := Duration{ms(7)}
	if err := json.Unmarshal([]byte(`null`), &d); err != nil || d.Duration != ms(7) {
		t.Errorf("JSON null duration = %s, %v, want it unchanged (7ms) without an error", d, err)
	}
	var holder struct {
		Timeout Duration `json:"timeout"`
		Wait    *Duration
	}
	if err := json.Unmarshal([]byte(`{"timeout":null,"Wait":null}`), &holder); err != nil || holder.Timeout.Duration != 0 || holder.Wait != nil {
		t.Errorf("null durations in a struct = %+v, %v, want zero values without an error", holder, err)
	}
}

// A number is a duration in nanoseconds in JSON. The same number in YAML (an int there, not a float) was refused.
func TestDurationUnmarshalYAMLNumber(t *testing.T) {
	var d Duration
	if err := yaml.Unmarshal([]byte(`2000000`), &d); err != nil || d.Duration != ms(2) {
		t.Errorf("YAML number duration = %s, %v, want 2ms (nanoseconds)", d, err)
	}
	var delay Delay
	if err := yaml.Unmarshal([]byte("min: 1000000\nmax: 3000000\ncount: 2\n"), &delay); err != nil {
		t.Fatalf("YAML delay with numbers failed: %v", err)
	}
	if delay.Min.Duration != ms(1) || delay.Max.Duration != ms(3) || delay.Count != 2 {
		t.Errorf("YAML delay with numbers = %s count %d, want 1ms-3ms count 2", &delay, delay.Count)
	}
	if err := yaml.Unmarshal([]byte(`1.5e6`), &d); err != nil || d.Duration != 1500*time.Microsecond {
		t.Errorf("YAML float duration = %s, %v, want 1.5ms", d, err)
	}
}

func TestDelayAppliesItsComputedDelay(t *testing.T) {
	d := NewDelay(ms(5), ms(40), -1)
	x := d.Compute()
	if x < ms(5) || x >= ms(40) {
		t.Fatalf("Compute() = %s, want a delay in [5ms, 40ms)", x)
	}
	// Computing again keeps the delay that is waiting to be applied.
	if y := d.Compute(); y != x {
		t.Errorf("second Compute() = %s, want the pending %s", y, x)
	}
	start := time.Now()
	if got := d.Apply(); got != x {
		t.Errorf("Apply() = %s, want the computed %s", got, x)
	}
	if elapsed := time.Since(start); elapsed < x {
		t.Errorf("Apply() slept %s, want at least %s", elapsed, x)
	}
	if d.Count != -1 {
		t.Errorf("Count = %d after an unlimited delay was applied, want -1", d.Count)
	}
	// Without a computed delay Apply computes its own.
	start = time.Now()
	got := d.Apply()
	if got < ms(5) || got >= ms(40) || time.Since(start) < got {
		t.Errorf("Apply() without Compute() = %s after %s, want a delay in [5ms, 40ms) that was slept", got, time.Since(start))
	}
}

func TestDelayComputeAndApplyCallsBackWithTheDelayItApplies(t *testing.T) {
	d := NewDelay(ms(2), ms(30), -1)
	for i := 0; i < 5; i++ {
		var seen []time.Duration
		start := time.Now()
		got := d.ComputeAndApply(func(x time.Duration) { seen = append(seen, x) })
		if len(seen) != 1 || seen[0] != got {
			t.Fatalf("ComputeAndApply() = %s with the callback given %v, want the callback to get the delay that was applied", got, seen)
		}
		if got < ms(2) || got >= ms(30) || time.Since(start) < got {
			t.Fatalf("ComputeAndApply() = %s after %s, want a delay in [2ms, 30ms) that was slept", got, time.Since(start))
		}
	}
	// A delay that was computed before (to report it) is the one that is applied.
	x := d.Compute()
	var seen time.Duration
	if got := d.ComputeAndApply(func(c time.Duration) { seen = c }); got != x || seen != x {
		t.Errorf("ComputeAndApply() = %s with the callback given %s, want the pending %s for both", got, seen, x)
	}
}

func TestDelayCountsDown(t *testing.T) {
	d := NewDelay(ms(2), ms(2), 2)
	for i := 1; i >= 0; i-- {
		if got := d.Compute(); got != ms(2) {
			t.Fatalf("Compute() with %d left = %s, want 2ms", i+1, got)
		}
		if got := d.Apply(); got != ms(2) {
			t.Fatalf("Apply() with %d left = %s, want 2ms", i+1, got)
		}
		if d.Count != i {
			t.Fatalf("Count = %d after an Apply(), want %d", d.Count, i)
		}
	}
	if d.IsNonZero() {
		t.Errorf("IsNonZero() = true after the count ran out")
	}
	// Nothing is left to delay with: no stale delay is reported, and none is applied.
	if got := d.Compute(); got != 0 {
		t.Errorf("Compute() after the count ran out = %s, want 0", got)
	}
	var seen []time.Duration
	start := time.Now()
	if got := d.ComputeAndApply(func(x time.Duration) { seen = append(seen, x) }); got != 0 {
		t.Errorf("ComputeAndApply() after the count ran out = %s, want 0", got)
	}
	if len(seen) != 1 || seen[0] != 0 {
		t.Errorf("ComputeAndApply() after the count ran out called back with %v, want [0s]", seen)
	}
	if got := d.Apply(); got != 0 {
		t.Errorf("Apply() after the count ran out = %s, want 0", got)
	}
	if elapsed := time.Since(start); elapsed >= ms(2) {
		t.Errorf("the delay that ran out still slept %s", elapsed)
	}
	if d.Count != 0 {
		t.Errorf("Count = %d after the count ran out, want it to stay 0", d.Count)
	}
}

// A delay that is shared by the concurrent requests of a server must apply as many times as it has count, no more. The
// count used to be decremented after the sleep, so every request that came in meanwhile passed the check (and the count
// went below -1, which is the unlimited count).
func TestDelaySharedByConcurrentCallersAppliesExactlyItsCount(t *testing.T) {
	const callers, count = 40, 10
	d := NewDelay(20*time.Millisecond, 20*time.Millisecond, count)
	results := make([]time.Duration, callers)
	callbacks := make([]time.Duration, callers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i] = d.ComputeAndApply(func(x time.Duration) { callbacks[i] = x })
		}()
	}
	// Other users of the same delay at the same time.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 50; j++ {
				_ = d.IsNonZero()
				_ = d.String()
				_ = d.IsLargerThan(d)
			}
		}()
	}
	close(start)
	wg.Wait()
	applied := 0
	for i := range results {
		if results[i] != callbacks[i] {
			t.Errorf("caller %d applied %s but its callback was given %s", i, results[i], callbacks[i])
		}
		switch results[i] {
		case 20 * time.Millisecond:
			applied++
		case 0:
		default:
			t.Errorf("caller %d applied %s, want 20ms or nothing", i, results[i])
		}
	}
	if applied != count {
		t.Errorf("the delay was applied %d times, want exactly its count %d", applied, count)
	}
	if d.Count != 0 {
		t.Errorf("Count = %d after the callers were done, want 0", d.Count)
	}
}

// Sleeping must not hold up the others: the lock that guards the delay is not held while it sleeps.
func TestDelaySleepsOfConcurrentCallersOverlap(t *testing.T) {
	const callers = 8
	d := NewDelay(100*time.Millisecond, 100*time.Millisecond, -1)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			d.ComputeAndApply()
		}()
	}
	begin := time.Now()
	close(start)
	wg.Wait()
	// One after the other they would take callers * 100ms = 800ms.
	if elapsed := time.Since(begin); elapsed >= 600*time.Millisecond {
		t.Errorf("%d callers of a 100ms delay took %s, want them to sleep together, in about 100ms", callers, elapsed)
	}
	if d.Count != -1 {
		t.Errorf("Count = %d, want the unlimited -1", d.Count)
	}
}

func TestDelayBlock(t *testing.T) {
	d := NewDelay(ms(2), ms(6), -1)
	start := time.Now()
	select {
	case got := <-d.Block():
		if got < ms(2) || got >= ms(6) || time.Since(start) < got {
			t.Errorf("Block() gave %s after %s, want a delay in [2ms, 6ms) that was slept", got, time.Since(start))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Block() didn't deliver")
	}
	d = NewDelay(ms(1), ms(1), 1)
	select {
	case got := <-d.Block():
		if got != ms(1) {
			t.Errorf("Block() gave %s, want 1ms", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Block() didn't deliver")
	}
	select {
	case got := <-d.Block():
		if got != 0 {
			t.Errorf("Block() of a delay that ran out gave %s, want 0", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Block() of a delay that ran out didn't deliver")
	}
}

// A delay that is written as a literal, instead of being made by NewDelay or Prepare, can be blocked on too. Its channel
// used to be nil, which never delivers.
func TestDelayBlockOfADelayThatWasNotPrepared(t *testing.T) {
	d := &Delay{Min: &Duration{ms(1)}, Max: &Duration{ms(1)}, Count: -1}
	select {
	case got := <-d.Block():
		if got != ms(1) {
			t.Errorf("Block() gave %s, want 1ms", got)
		}
	case <-time.After(2 * time.Second):
		t.Error("Block() of a delay that was not prepared didn't deliver")
	}
}

// Every Block() has a channel of its own: the ones that are asked for at the same time don't take each other's delays.
func TestDelayBlockChannelsAreNotShared(t *testing.T) {
	d := NewDelay(ms(1), ms(1), -1)
	c1, c2 := d.Block(), d.Block()
	if c1 == c2 {
		t.Errorf("two calls of Block() returned the same channel")
	}
	for i, c := range []chan time.Duration{c1, c2} {
		select {
		case got := <-c:
			if got != ms(1) {
				t.Errorf("Block() #%d gave %s, want 1ms", i+1, got)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("Block() #%d didn't deliver", i+1)
		}
	}
}

// A caller that stops waiting (the agent's wait is a select that can end by the cancellation of the task) must not leave a
// goroutine behind that waits for ever to deliver the delay.
func TestDelayBlockAbandonedDoesNotLeakGoroutines(t *testing.T) {
	d := NewDelay(ms(1), ms(1), -1)
	before := runtime.NumGoroutine()
	for i := 0; i < 30; i++ {
		d.Block() // the channel is dropped
	}
	deadline := time.Now().Add(3 * time.Second)
	after := runtime.NumGoroutine()
	for after > before+2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		after = runtime.NumGoroutine()
	}
	if after > before+2 {
		t.Errorf("%d goroutines are left from 30 abandoned Block() calls (%d before), want them to end", after-before, before)
	}
}
