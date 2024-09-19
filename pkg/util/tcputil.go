/**
 * Copyright 2024 uk
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
  "fmt"
  "net"
  "strings"
  "time"
)

func IsConnectionCloseError(err error) bool {
  return strings.Contains(err.Error(), "closed network connection")
}

func IsConnectionTimeoutError(err error) bool {
  return strings.Contains(err.Error(), "timeout")
}

func CopyInputHeadToOutput(writeBuffer, readBuffer []byte, inHead, outFrom, outTo int) {
  copy(writeBuffer[outFrom:outTo], readBuffer[:inHead])
}

func CopyInputTailToOutput(writeBuffer, readBuffer []byte, tail, outFrom, outTo int) {
  copy(writeBuffer[outFrom:outTo], readBuffer[tail:])
}

func GetConnectionRemainingLife(startTime, atTime time.Time, connectionLife, readTimeout, connIdleTimeout time.Duration) time.Duration {
  now := time.Now()
  if connectionLife <= 0 && readTimeout <= 0 && connIdleTimeout <= 0 {
    return 24 * time.Hour
  }
  remainingLife := 0 * time.Second
  if connectionLife > 0 {
    remainingLife = connectionLife - (now.Sub(startTime))
  }
  if readTimeout > 0 {
    if connectionLife == 0 || readTimeout < remainingLife {
      remainingLife = readTimeout
    }
  }
  if connIdleTimeout > 0 {
    if connectionLife == 0 && readTimeout == 0 || connIdleTimeout < remainingLife {
      remainingLife = connIdleTimeout
    }
  }
  return remainingLife
}

func GetConnectionReadWriteTimeout(startTime time.Time, connectionLife, readWriteTimeout, connIdleTimeout time.Duration) time.Time {
  now := time.Now()
  return now.Add(GetConnectionRemainingLife(startTime, now, connectionLife, readWriteTimeout, connIdleTimeout))
}

func UpdateReadDeadline(conn net.Conn, connStartTime time.Time, connectionLife, readTimeout, connIdleTimeout time.Duration) {
  conn.SetReadDeadline(GetConnectionReadWriteTimeout(connStartTime, connectionLife, readTimeout, connIdleTimeout))
}

func UpdateWriteDeadline(conn net.Conn, connStartTime time.Time, connectionLife, writeTimeout, connIdleTimeout time.Duration) {
  conn.SetWriteDeadline(GetConnectionReadWriteTimeout(connStartTime, connectionLife, writeTimeout, connIdleTimeout))
}

func CheckForReadability(conn net.Conn, c chan bool) {
  _, err := conn.Read([]byte{})
  if err != nil {
    c <- false
  }
  c <- true
}

func Write(buff []byte, conn net.Conn) error {
  size := len(buff)
  n, err := conn.Write(buff)
  if err != nil {
    return err
  }
  if n < size {
    n2, err := conn.Write(buff[n:])
    if err != nil {
      return err
    }
    n += n2
  }
  if n < size {
    return fmt.Errorf("Wrote less data [%d] than buffer [%d]\n", size, n)
  }
  return nil
}
