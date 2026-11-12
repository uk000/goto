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

package util

import (
	"testing"
	"time"
)

// The words for seconds, minutes and hours are the durations of their own units.
func TestExtractDurationHintUnits(t *testing.T) {
	for _, tc := range []struct {
		text string
		want time.Duration
	}{
		{"wait 5 s", 5 * time.Second},
		{"wait 5 sec", 5 * time.Second},
		{"wait 5 secs", 5 * time.Second},
		{"wait 2 m", 2 * time.Minute},
		{"wait 2 min", 2 * time.Minute},
		{"wait 2 mins", 2 * time.Minute},
		{"wait 3 h", 3 * time.Hour},
		{"wait 3 hour", 3 * time.Hour},
		{"wait 3 hours", 3 * time.Hour},
		{"no duration", 0},
	} {
		if _, got := ExtractDurationHint(tc.text); got != tc.want {
			t.Errorf("ExtractDurationHint(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}
