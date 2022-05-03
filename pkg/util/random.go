/**
 * Copyright 2022 uk
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
  "math/rand"
  "time"
)

const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*()_+-=~`{}[];:,.<>/?"

var sizes map[string]uint64 = map[string]uint64{
  "K":  1000,
  "KB": 1000,
  "M":  1000000,
  "MB": 1000000,
}

var (
  randomCharsetLength = len(charset)
  random              = rand.New(rand.NewSource(time.Now().UnixNano()))
)

func Random(max int) int {
  return random.Intn(max)
}

func Random64(max int64) int64 {
  return random.Int63n(max)
}

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
    addOn := max - d
    d = d + time.Millisecond*time.Duration(Random64(addOn.Milliseconds()))
  }
  return d
}

func RandomFrom(vals []int) int {
  return vals[random.Intn(len(vals))]
}

func GenerateRandomPayload(size int) []byte {
  b := make([]byte, size)
  for i := range b {
    b[i] = charset[Random(randomCharsetLength)]
  }
  return b
}

func GenerateRandomString(size int) string {
  return string(GenerateRandomPayload(size))
}
