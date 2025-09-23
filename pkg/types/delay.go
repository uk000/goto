package types

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

type Duration struct {
	time.Duration
}

type Delay struct {
	Min           *Duration `json:"min,omitempty"`
	Max           *Duration `json:"max,omitempty"`
	Count         int       `json:"count,omitempty"`
	computedDelay time.Duration
	delayChan     chan time.Duration
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

func (d *Delay) IsLargerThan(d2 *Delay) bool {
	return (d.Max.Duration > d2.Max.Duration) || (d.Max.Duration == d2.Max.Duration && d.Min.Duration > d2.Min.Duration)
}

func (d *Delay) Prepare() {
	if d.Min.Duration > d.Max.Duration {
		d.Min.Duration = d.Max.Duration
	}
	if d.Count < -1 {
		d.Count = -1
	}
	d.delayChan = make(chan time.Duration)
}

func (d *Delay) Compute() time.Duration {
	if d.Count > 0 || d.Count == -1 {
		d.computedDelay = RandomDuration(d.Min.Duration, d.Max.Duration)
		return d.computedDelay
	}
	return 0
}

func (d *Delay) Apply() time.Duration {
	if d.Count > 0 || d.Count == -1 {
		if d.computedDelay == 0 {
			d.Compute()
		}
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

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var v interface{}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch value := v.(type) {
	case float64:
		d.Duration = time.Duration(value)
		return nil
	case string:
		var err error
		d.Duration, err = time.ParseDuration(value)
		if err != nil {
			return err
		}
		return nil
	default:
		return errors.New("Invalid duration")
	}
}
