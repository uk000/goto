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
	"bytes"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

var (
	randomCharsetLength = len(charset)
	// A rand.Rand is not safe for concurrent use, but the requests of the server all draw from this one.
	random    = rand.New(rand.NewSource(time.Now().UnixNano()))
	randomMu  sync.Mutex
	IDCounter = atomic.Uint32{}
)

// Random returns a random number in [0, max), or 0 if there is no such number (max <= 0). It is safe for concurrent use.
func Random(max int) int {
	if max <= 0 {
		return 0
	}
	randomMu.Lock()
	defer randomMu.Unlock()
	return random.Intn(max)
}

// Random64 returns a random number in [0, max), or 0 if there is no such number (max <= 0). It is safe for concurrent
// use.
func Random64(max int64) int64 {
	if max <= 0 {
		return 0
	}
	randomMu.Lock()
	defer randomMu.Unlock()
	return random.Int63n(max)
}

// RandomDuration returns a random duration in [min, max), at nanosecond resolution. If max is not larger than min then
// it is min, and if both are 0 then it is the fallback (when given), or 0.
func RandomDuration(min, max time.Duration, fallback ...time.Duration) time.Duration {
	if min == 0 && max == 0 {
		if len(fallback) > 0 {
			return fallback[0]
		} else {
			return 0
		}
	}
	d := min
	if max > min {
		d = d + time.Duration(Random64(int64(max-min)))
	}
	return d
}

// RandomFrom returns one of the values at random, or 0 if there are none.
func RandomFrom(vals []int) int {
	if len(vals) == 0 {
		return 0
	}
	return vals[Random(len(vals))]
}

// GenerateRandomPayload returns a payload of the given size that starts with the prefixes and is filled with random alphanumeric characters.
func GenerateRandomPayload(size int, prefix ...string) []byte {
	b := bytes.Buffer{}
	written := 0
	for _, p := range prefix {
		written += len(p)
		b.Write([]byte(p))
		if written > size {
			break
		}
	}
	for i := written; i < size; i++ {
		b.WriteByte(charset[Random(randomCharsetLength)])
	}
	return b.Bytes()
}

// GenerateRandomString returns a random alphanumeric string of the given size.
func GenerateRandomString(size int) string {
	return string(GenerateRandomPayload(size))
}

// ID returns the next value of a counter that is shared by the process.
func ID() int {
	return int(IDCounter.Add(1))
}
