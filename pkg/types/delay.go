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

// Delay is a range of delays, with the number of times that it applies (a count of -1 is unlimited). A delay is shared by
// the concurrent requests that it applies to, and is safe for them to use through its methods. Its lock is only held for
// the bookkeeping, never while sleeping or calling back, and makes the delay one that must not be copied (see Clone).
type Delay struct {
	Min   *Duration `yaml:"min,omitempty" json:"min,omitempty"`
	Max   *Duration `yaml:"max,omitempty" json:"max,omitempty"`
	Count int       `yaml:"count,omitempty" json:"count,omitempty"`
	// The delay that Compute() computed, for Apply() to apply.
	computedDelay time.Duration
	hasComputed   bool
	// lock guards the state of the delay: its Count, and the computed delay that waits to be applied.
	lock sync.Mutex
}

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

// NewDelay creates a delay between min and max that applies count times (0 is unlimited), or nil if max isn't positive or count is below -1.
func NewDelay(min, max time.Duration, count int) *Delay {
	if max > 0 && (count >= -1) {
		if count == 0 {
			count = -1
		}
		return &Delay{Min: &Duration{min}, Max: &Duration{max}, Count: count}
	}
	return nil
}

// ParseDelay parses a "low-high:count" range into a Delay, or gives nil if it is invalid.
func ParseDelay(val string) *Delay {
	if min, max, count, ok := ParseDurationRange(val); ok {
		return NewDelay(min, max, count)
	}
	return nil
}

// String returns the delay as "min-max", or "" if there is no delay or it has no bounds.
func (d *Delay) String() string {
	if d == nil {
		return ""
	}
	d.lock.Lock()
	defer d.lock.Unlock()
	if d.Min != nil && d.Max != nil {
		return fmt.Sprintf("%s-%s", d.Min.Duration.String(), d.Max.Duration.String())
	}
	return ""
}

// bounds gives the bounds of the delay. A delay that is missing, or misses a bound, is no delay (0-0).
func (d *Delay) bounds() (min, max time.Duration) {
	if d == nil {
		return 0, 0
	}
	d.lock.Lock()
	defer d.lock.Unlock()
	if d.Min == nil || d.Max == nil {
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
	d.lock.Lock()
	defer d.lock.Unlock()
	return d.Min != nil && d.Min.Duration > 0 && d.Max != nil && d.Max.Duration > 0 && d.countLeft()
}

// IsLargerThan compares by max, then by min. A delay that is missing, or misses a bound, counts as no delay.
func (d *Delay) IsLargerThan(d2 *Delay) bool {
	// the delays are looked at one after the other, and never both locked, as two callers that compare the same two
	// delays the other way round would wait for each other
	min, max := d.bounds()
	min2, max2 := d2.bounds()
	return (max > max2) || (max == max2 && min > min2)
}

// normalizeLocked cuts the min to the max and turns an invalid count into unlimited (-1), and requires the lock to be held.
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
	d.lock.Lock()
	defer d.lock.Unlock()
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
	d.lock.Lock()
	defer d.lock.Unlock()
	delay := d.computeLocked()
	d.hasComputed = false
	if delay > 0 && d.Count > 0 {
		d.Count--
	}
	return delay
}

// Claim takes the delay that is next to be applied (the computed one, if there is), and counts it, without sleeping, for
// the callers that sleep on their own, e.g. in a way that they can give up on. It is 0 if the delay doesn't delay anymore.
func (d *Delay) Claim() time.Duration {
	if d == nil {
		return 0
	}
	return d.claim()
}

// Compute computes the delay that Apply() will apply, and returns it. Until it is applied, computing again returns the
// same delay. It is 0 if the delay doesn't delay anymore.
func (d *Delay) Compute() time.Duration {
	if d == nil {
		return 0
	}
	d.lock.Lock()
	defer d.lock.Unlock()
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

// Clone gives a delay of its own that is the same as this one, with the bounds that it has now and the count that is left,
// and the delay that was computed for Apply(). A delay is not copied by value, which would copy its lock.
func (d *Delay) Clone() *Delay {
	if d == nil {
		return nil
	}
	d.lock.Lock()
	defer d.lock.Unlock()
	c := &Delay{Count: d.Count, computedDelay: d.computedDelay, hasComputed: d.hasComputed}
	if d.Min != nil {
		c.Min = &Duration{d.Min.Duration}
	}
	if d.Max != nil {
		c.Max = &Duration{d.Max.Duration}
	}
	return c
}

// payload gives what the delay is made of as it is now, in a payload that doesn't change with the delay.
func (d *Delay) payload() *delayPayload {
	d.lock.Lock()
	defer d.lock.Unlock()
	dp := &delayPayload{Count: d.Count}
	if d.Min != nil {
		dp.Min = &Duration{d.Min.Duration}
	}
	if d.Max != nil {
		dp.Max = &Duration{d.Max.Duration}
	}
	return dp
}

// MarshalJSON encodes a snapshot of the delay, or null if there is no delay.
func (d *Delay) MarshalJSON() ([]byte, error) {
	if d == nil {
		return []byte("null"), nil
	}
	return json.Marshal(d.payload())
}

// MarshalYAML encodes a snapshot of the delay, or nothing if there is no delay.
func (d *Delay) MarshalYAML() (any, error) {
	if d == nil {
		return nil, nil
	}
	return d.payload(), nil
}

// MarshalJSON encodes the duration as a string like "1s", or null if there is none.
func (d *Duration) MarshalJSON() ([]byte, error) {
	if d == nil {
		return []byte("null"), nil
	}
	return json.Marshal(d.String())
}

// MarshalYAML encodes the duration as a string like "1s", or nothing if there is none.
func (d *Duration) MarshalYAML() (any, error) {
	if d == nil {
		return nil, nil
	}
	return d.String(), nil
}

// UnmarshalJSON decodes the duration from a JSON number (nanoseconds) or string, and leaves it as it is for null.
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

// UnmarshalYAML decodes the duration from a YAML number (nanoseconds) or string.
func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var v any
	if err := node.Decode(&v); err != nil {
		return err
	}
	return d.Unmarshal(v)
}

// Unmarshal sets the duration from a number (nanoseconds) or a duration string, and fails for any other type.
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

// UnmarshalJSON loads the delay from its JSON payload and normalises it.
func (d *Delay) UnmarshalJSON(b []byte) error {
	dp := &delayPayload{}
	if err := json.Unmarshal(b, dp); err != nil {
		return err
	}
	d.load(dp)
	return nil
}

// UnmarshalYAML loads the delay from its YAML payload and normalises it.
func (d *Delay) UnmarshalYAML(node *yaml.Node) error {
	dp := &delayPayload{}
	if err := node.Decode(dp); err != nil {
		return err
	}
	d.load(dp)
	return nil
}

// load replaces the state of the delay with the payload, dropping any computed delay and normalising it.
func (d *Delay) load(dp *delayPayload) {
	d.lock.Lock()
	defer d.lock.Unlock()
	d.Min = dp.Min
	d.Max = dp.Max
	d.Count = dp.Count
	d.computedDelay = 0
	d.hasComputed = false
	d.normalizeLocked()
}
