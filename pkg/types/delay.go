package types

import (
	"encoding/json"
	"errors"
	"time"
)

type Duration struct {
	time.Duration
}

type Delay struct {
	Min       *Duration `json:"min,omitempty"`
	Max       *Duration `json:"max,omitempty"`
	Count     int       `json:"count,omitempty"`
	delayChan chan time.Duration
}

func NewDelay(min, max time.Duration, count int) *Delay {
	if max > 0 && (count > 0 || count == -1) {
		return &Delay{Min: &Duration{min}, Max: &Duration{max}, Count: count, delayChan: make(chan time.Duration)}
	}
	return nil
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

func (d *Delay) Apply() time.Duration {
	if d.Count > 0 || d.Count == -1 {
		delay := RandomDuration(d.Min.Duration, d.Max.Duration)
		if delay > 0 {
			time.Sleep(delay)
			if d.Count > 0 {
				d.Count--
			}
			return delay
		}
	}
	return 0
}

func (d *Delay) Block() chan time.Duration {
	go func() {
		dur := d.Apply()
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
