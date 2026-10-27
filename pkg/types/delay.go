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
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type Duration struct {
	time.Duration
}

type Delay struct {
	Min   *Duration `yaml:"min,omitempty" json:"min,omitempty"`
	Max   *Duration `yaml:"max,omitempty" json:"max,omitempty"`
	Count int       `yaml:"count,omitempty" json:"count,omitempty"`
	// The delay that Compute() computed, for Apply() to apply.
	computedDelay time.Duration
	hasComputed   bool
}

// delayMu guards the state of the delays: their Count, and the computed delay that waits to be applied. A delay is shared
// by the concurrent requests that it applies to. It is one lock for all delays instead of a field of Delay, as Delays are
// copied by value (a lock must not be), and it is only held for the bookkeeping, never while sleeping or calling back.
var delayMu sync.Mutex

type delayPayload struct {
	Min   *Duration `yaml:"min,omitempty" json:"min,omitempty"`
	Max   *Duration `yaml:"max,omitempty" json:"max,omitempty"`
	Count int       `yaml:"count,omitempty" json:"count,omitempty"`
}

// ParseDurationRange parses "low", "low-high" or either of them followed by ":count", e.g. "100ms-1s:5". The bounds are
// put in order. A bound that is not a duration makes the whole range invalid (ok is false). A count that is not a
// positive number is left out (0), which is unlimited.
func ParseDurationRange(val string) (low, high time.Duration, count int, ok bool) {
	dRangeAndCount := strings.Split(val, ":")
	dRange := strings.Split(dRangeAndCount[0], "-")
	low, err := time.ParseDuration(dRange[0])
	if err != nil {
		return 0, 0, 0, false
	}
	high = low
	if len(dRange) > 1 {
		d, err := time.ParseDuration(dRange[1])
		if err != nil {
			return 0, 0, 0, false
		}
		if d < low {
			high = low
			low = d
		} else {
			high = d
		}
	}
	if len(dRangeAndCount) > 1 {
		if c, err := strconv.ParseInt(dRangeAndCount[1], 10, 32); err == nil {
			if c > 0 {
				count = int(c)
			}
		}
	}
	return low, high, count, true
}

func NewDelay(min, max time.Duration, count int) *Delay {
	if max > 0 && (count >= -1) {
		if count == 0 {
			count = -1
		}
		return &Delay{Min: &Duration{min}, Max: &Duration{max}, Count: count}
	}
	return nil
}

func ParseDelay(val string) *Delay {
	if min, max, count, ok := ParseDurationRange(val); ok {
		return NewDelay(min, max, count)
	}
	return nil
}

func (d *Delay) String() string {
	if d == nil {
		return ""
	}
	delayMu.Lock()
	defer delayMu.Unlock()
	if d.Min != nil && d.Max != nil {
		return fmt.Sprintf("%s-%s", d.Min.Duration.String(), d.Max.Duration.String())
	}
	return ""
}

// rangeLocked gives the bounds of the delay. A delay that is missing, or misses a bound, is no delay (0-0).
func (d *Delay) rangeLocked() (min, max time.Duration) {
	if d == nil || d.Min == nil || d.Max == nil {
		return 0, 0
	}
	return d.Min.Duration, d.Max.Duration
}

// countLeft tells if the count of the delay is not used up (-1 is unlimited).
func (d *Delay) countLeft() bool {
	return d.Count > 0 || d.Count == -1
}

// IsNonZero tells if the delay has a min and a max that are above 0, and a count that is not used up. A delay without a
// min or a max is not (and, as it always was, neither is one that starts from 0, like 0s-1s).
func (d *Delay) IsNonZero() bool {
	if d == nil {
		return false
	}
	delayMu.Lock()
	defer delayMu.Unlock()
	return d.Min != nil && d.Min.Duration > 0 && d.Max != nil && d.Max.Duration > 0 && d.countLeft()
}

// IsLargerThan compares by max, then by min. A delay that is missing, or misses a bound, counts as no delay.
func (d *Delay) IsLargerThan(d2 *Delay) bool {
	delayMu.Lock()
	defer delayMu.Unlock()
	min, max := d.rangeLocked()
	min2, max2 := d2.rangeLocked()
	return (max > max2) || (max == max2 && min > min2)
}

func (d *Delay) normalizeLocked() {
	if d.Min != nil && d.Max != nil && d.Min.Duration > d.Max.Duration {
		d.Min.Duration = d.Max.Duration
	}
	if d.Count == 0 || d.Count < -1 {
		d.Count = -1
	}
}

// Prepare cuts the min to the max, and turns an invalid count to the unlimited one (-1). The bounds that are missing are
// left missing.
func (d *Delay) Prepare() {
	if d == nil {
		return
	}
	delayMu.Lock()
	defer delayMu.Unlock()
	d.normalizeLocked()
}

// computeLocked gives the delay that is next to be applied, and keeps it for Apply() until then. It is 0 if the delay
// has no bounds, or its count is used up.
func (d *Delay) computeLocked() time.Duration {
	if d.Min == nil || d.Max == nil || !d.countLeft() {
		d.computedDelay = 0
		d.hasComputed = false
		return 0
	}
	if !d.hasComputed {
		d.computedDelay = RandomDuration(d.Min.Duration, d.Max.Duration)
		d.hasComputed = true
	}
	return d.computedDelay
}

// claim takes the delay to apply (the computed one if there is, or a new one), and counts it. The delay is counted when
// it is taken, so that the callers that share the delay don't all take the last count while the first is sleeping.
func (d *Delay) claim() time.Duration {
	delayMu.Lock()
	defer delayMu.Unlock()
	delay := d.computeLocked()
	d.hasComputed = false
	if delay > 0 && d.Count > 0 {
		d.Count--
	}
	return delay
}

// Compute computes the delay that Apply() will apply, and returns it. Until it is applied, computing again returns the
// same delay. It is 0 if the delay doesn't delay anymore.
func (d *Delay) Compute() time.Duration {
	if d == nil {
		return 0
	}
	delayMu.Lock()
	defer delayMu.Unlock()
	return d.computeLocked()
}

// Apply sleeps for the delay that was computed (or for a new one), and returns it. It returns 0 without sleeping if the
// delay doesn't delay anymore.
func (d *Delay) Apply() time.Duration {
	if d == nil {
		return 0
	}
	delay := d.claim()
	if delay > 0 {
		time.Sleep(delay)
	}
	return delay
}

// ComputeAndApply gives the delay to the callback (if any) just before it sleeps for it, and returns it.
func (d *Delay) ComputeAndApply(callback ...func(time.Duration)) time.Duration {
	var delay time.Duration
	if d != nil {
		delay = d.claim()
	}
	if len(callback) > 0 && callback[0] != nil {
		callback[0](delay)
	}
	if delay > 0 {
		time.Sleep(delay)
	}
	return delay
}

// Block applies the delay in the background, and delivers it on the channel when it is over. Every call has a channel of
// its own, which doesn't need to be read.
func (d *Delay) Block() chan time.Duration {
	delayChan := make(chan time.Duration, 1)
	go func() {
		delayChan <- d.ComputeAndApply()
	}()
	return delayChan
}

func (d *Duration) MarshalJSON() ([]byte, error) {
	if d == nil {
		return []byte("null"), nil
	}
	return json.Marshal(d.String())
}

func (d *Duration) MarshalYAML() (any, error) {
	if d == nil {
		return nil, nil
	}
	return d.String(), nil
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var v interface{}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	if v == nil {
		// null leaves the duration as it is, as it does for all the types
		return nil
	}
	return d.Unmarshal(v)
}

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var v any
	if err := node.Decode(&v); err != nil {
		return err
	}
	return d.Unmarshal(v)
}

func (d *Duration) Unmarshal(v interface{}) error {
	switch value := v.(type) {
	case float64:
		d.Duration = time.Duration(value)
	case int:
		// YAML numbers are ints, where JSON numbers are floats: all are nanoseconds
		d.Duration = time.Duration(value)
	case int64:
		d.Duration = time.Duration(value)
	case uint64:
		d.Duration = time.Duration(value)
	case string:
		var err error
		d.Duration, err = time.ParseDuration(value)
		if err != nil {
			return err
		}
	default:
		return errors.New("Invalid duration")
	}
	return nil
}

func (d *Delay) UnmarshalJSON(b []byte) error {
	dp := &delayPayload{}
	if err := json.Unmarshal(b, dp); err != nil {
		return err
	}
	d.load(dp)
	return nil
}

func (d *Delay) UnmarshalYAML(node *yaml.Node) error {
	dp := &delayPayload{}
	if err := node.Decode(dp); err != nil {
		return err
	}
	d.load(dp)
	return nil
}

func (d *Delay) load(dp *delayPayload) {
	delayMu.Lock()
	defer delayMu.Unlock()
	d.Min = dp.Min
	d.Max = dp.Max
	d.Count = dp.Count
	d.computedDelay = 0
	d.hasComputed = false
	d.normalizeLocked()
}
