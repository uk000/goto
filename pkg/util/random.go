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
