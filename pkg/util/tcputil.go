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
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	// connectReadTimeout bounds the wait for the CONNECT request of a client.
	connectReadTimeout = 10 * time.Second
	// connectDialTimeout bounds the attempt to connect to the destination of a tunnel.
	connectDialTimeout = 10 * time.Second
	// connectIdleTimeout is how long a tunnel may stay silent, in both directions, before the proxy gives up on it.
	// Unlike the connections of a TCP proxy, the ones of a tunnel are the long lived ones of the clients of the proxy.
	connectIdleTimeout = 5 * time.Minute
	// connectMaxRequestBytes bounds what the proxy reads for the CONNECT request, which is the request line and headers.
	connectMaxRequestBytes int64 = 64 * 1024
)

// firstByteReader remembers when it first gave bytes.
type FirstByteReader struct {
	Reader io.Reader
	At     time.Time
}

// IsConnectionCloseError tells if the error is about the use of a closed network connection.
func IsConnectionCloseError(err error) bool {
	return strings.Contains(err.Error(), "closed network connection")
}

// IsConnectionTimeoutError tells if the error is a timeout.
func IsConnectionTimeoutError(err error) bool {
	return strings.Contains(err.Error(), "timeout")
}

// CopyInputHeadToOutput copies the first inHead bytes of the read buffer into the write buffer at [outFrom:outTo].
func CopyInputHeadToOutput(writeBuffer, readBuffer []byte, inHead, outFrom, outTo int) {
	copy(writeBuffer[outFrom:outTo], readBuffer[:inHead])
}

// CopyInputTailToOutput copies the read buffer from tail onwards into the write buffer at [outFrom:outTo].
func CopyInputTailToOutput(writeBuffer, readBuffer []byte, tail, outFrom, outTo int) {
	copy(writeBuffer[outFrom:outTo], readBuffer[tail:])
}

// GetConnectionRemainingLife computes how much longer a connection may live, as the least of the limits that are set (24h if none is).
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

// GetConnectionReadWriteTimeout returns the deadline for the next read or write, which is the current time plus the remaining life of the connection.
func GetConnectionReadWriteTimeout(startTime time.Time, connectionLife, readWriteTimeout, connIdleTimeout time.Duration) time.Time {
	now := time.Now()
	return now.Add(GetConnectionRemainingLife(startTime, now, connectionLife, readWriteTimeout, connIdleTimeout))
}

// UpdateReadDeadline sets the read deadline of the connection from its remaining life and its timeouts.
func UpdateReadDeadline(conn net.Conn, connStartTime time.Time, connectionLife, readTimeout, connIdleTimeout time.Duration) {
	conn.SetReadDeadline(GetConnectionReadWriteTimeout(connStartTime, connectionLife, readTimeout, connIdleTimeout))
}

// UpdateWriteDeadline sets the write deadline of the connection from its remaining life and its timeouts.
func UpdateWriteDeadline(conn net.Conn, connStartTime time.Time, connectionLife, writeTimeout, connIdleTimeout time.Duration) {
	conn.SetWriteDeadline(GetConnectionReadWriteTimeout(connStartTime, connectionLife, writeTimeout, connIdleTimeout))
}

// CheckForReadability probes the connection with a zero-length read and sends the outcome on the channel.
func CheckForReadability(conn net.Conn, c chan bool) {
	_, err := conn.Read([]byte{})
	if err != nil {
		c <- false
	}
	c <- true
}

// Write writes the whole buffer to the connection, writing the rest once more after a short write, and fails if it is still incomplete.
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

// ParseAddress splits an address of the form host:port into the endpoint and the port number, and returns empty values if it has no colon.
func ParseAddress(address string) (endpoint string, port int) {
	pieces := strings.Split(address, ":")
	if len(pieces) > 1 {
		endpoint = pieces[0]
		port, _ = strconv.Atoi(pieces[len(pieces)-1])
	}
	return
}

// Read reads from the underlying reader and records the time at which it first gave bytes.
func (f *FirstByteReader) Read(p []byte) (int, error) {
	n, err := f.Reader.Read(p)
	if n > 0 && f.At.IsZero() {
		f.At = time.Now()
	}
	return n, err
}
