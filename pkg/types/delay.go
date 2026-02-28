/**
 * Copyright 2025 uk
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
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Duration struct {
	time.Duration
}

type Delay struct {
	Min           *Duration `yaml:"min,omitempty" json:"min,omitempty"`
	Max           *Duration `yaml:"max,omitempty" json:"max,omitempty"`
	Count         int       `yaml:"count,omitempty" json:"count,omitempty"`
	computedDelay time.Duration
	hasComputed   bool
	delayChan     chan time.Duration
}

type delayPayload struct {
	Min   *Duration `yaml:"min,omitempty" json:"min,omitempty"`
	Max   *Duration `yaml:"max,omitempty" json:"max,omitempty"`
	Count int       `yaml:"count,omitempty" json:"count,omitempty"`
}

func ParseDurationRange(val string) (low, high time.Duration, count int, ok bool) {
	dRangeAndCount := strings.Split(val, ":")
	dRange := strings.Split(dRangeAndCount[0], "-")
	if d, err := time.ParseDuration(dRange[0]); err != nil {
		return 0, 0, 0, false
	} else {
		low = d
	}
	if len(dRange) > 1 {
		if d, err := time.ParseDuration(dRange[1]); err == nil {
			if d < low {
				high = low
				low = d
			} else {
				high = d
			}
		}
	} else {
		high = low
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
		return &Delay{Min: &Duration{min}, Max: &Duration{max}, Count: count, delayChan: make(chan time.Duration)}
	}
	return nil
}

func ParseDelay(val string) *Delay {
	if min, max, count, ok := ParseDurationRange(val); ok {
		return NewDelay(min, max, count)
	}
	return nil
}

func (d *Delay) IsNonZero() bool {
	return d.Min != nil && d.Min.Duration > 0 && d.Max != nil && d.Max.Duration > 0 && d.Count > 0
}

func (d *Delay) IsLargerThan(d2 *Delay) bool {
	return (d.Max.Duration > d2.Max.Duration) || (d.Max.Duration == d2.Max.Duration && d.Min.Duration > d2.Min.Duration)
}

func (d *Delay) Prepare() {
	if d.Min.Duration > d.Max.Duration {
		d.Min.Duration = d.Max.Duration
	}
	if d.Count == 0 || d.Count < -1 {
		d.Count = -1
	}
	d.delayChan = make(chan time.Duration)
}

func (d *Delay) Compute() time.Duration {
	if !d.hasComputed {
		if d.Count > 0 || d.Count == -1 {
			d.computedDelay = RandomDuration(d.Min.Duration, d.Max.Duration)
			d.hasComputed = true
		}
	}
	return d.computedDelay
}

func (d *Delay) Apply() time.Duration {
	if !d.hasComputed {
		d.Compute()
	}
	d.hasComputed = false
	if d.Count > 0 || d.Count == -1 {
		if d.computedDelay > 0 {
			time.Sleep(d.computedDelay)
			if d.Count > 0 {
				d.Count--
			}
			return d.computedDelay
		}
	}
	return 0
}

func (d *Delay) ComputeAndApply() time.Duration {
	d.Compute()
	return d.Apply()
}

func (d *Delay) Block() chan time.Duration {
	go func() {
		dur := d.ComputeAndApply()
		d.delayChan <- dur
	}()
	return d.delayChan
}

func (d *Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

func (d *Duration) MarshalYAML() (any, error) {
	return d.String(), nil
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var v interface{}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
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
	d.Min = dp.Min
	d.Max = dp.Max
	d.Count = dp.Count
	d.Prepare()
	return nil
}

func (d *Delay) UnmarshalYAML(node *yaml.Node) error {
	dp := &delayPayload{}
	if err := node.Decode(dp); err != nil {
		return err
	}
	d.Min = dp.Min
	d.Max = dp.Max
	d.Count = dp.Count
	d.Prepare()
	return nil
}
