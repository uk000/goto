package util

import (
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
