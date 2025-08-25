package util

import "time"

type Delay struct {
	Min   *Duration `json:"min,omitempty"`
	Max   *Duration `json:"max,omitempty"`
	Count int       `json:"count,omitempty"`
}

func NewDelay(min, max time.Duration, count int) *Delay {
	if max > 0 && (count > 0 || count == -1) {
		return &Delay{Min: &Duration{min}, Max: &Duration{max}, Count: count}
	}
	return nil
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
