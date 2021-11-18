/**
 * Copyright 2021 uk
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

package invocation

import (
	"bufio"
	"goto/pkg/util"
	"log"
	"net"
	"time"
)

type ConnectionStatus struct {
  Port           int       `json:"port"`
  RequestID      int       `json:"requestID"`
  ConnStartTime  time.Time `json:"connStartTime"`
  ConnCloseTime  time.Time `json:"connCloseTime"`
  FirstByteInAt  time.Time `json:"firstByteInAt"`
  LastByteInAt   time.Time `json:"lastByteInAt"`
  FirstByteOutAt time.Time `json:"firstByteOutAt"`
  LastByteOutAt  time.Time `json:"lastByteOutAt"`
  TotalBytesRead int       `json:"totalBytesRead"`
  TotalBytesSent int       `json:"totalBytesSent"`
  TotalReads     int       `json:"totalReads"`
  TotalWrites    int       `json:"totalWrites"`
  Closed         bool      `json:"closed"`
  ClientClosed   bool      `json:"clientClosed"`
  ServerClosed   bool      `json:"serverClosed"`
  ErrorClosed    bool      `json:"errorClosed"`
  ReadTimeout    bool      `json:"readTimeout"`
  IdleTimeout    bool      `json:"idleTimeout"`
  LifeTimeout    bool      `json:"lifeTimeout"`
  WriteErrors    int       `json:"writeErrors"`
}

type TCPClient struct {
  target       *InvocationSpec
  tracker      *InvocationTracker
  conn         *net.TCPConn
  reader       *bufio.Reader
  status *ConnectionStatus
}

func (t *TCPClient) init(target *InvocationSpec, tracker *InvocationTracker) {
  t.target = target
  t.tracker = tracker
  t.status = &ConnectionStatus{}
}

func (t *TCPClient) connect() {
  d := net.Dialer{Timeout: t.target.connTimeoutD}
  if conn, err := d.Dial("tcp", t.target.URL); err == nil {
    t.conn = conn.(*net.TCPConn)
    t.reader = bufio.NewReader(conn)
  } else {
    log.Printf("Invocation[%d]: Failed to connect to [%s] with error: %s", t.tracker.ID, t.target.URL, err.Error())
  }
}

func (t *TCPClient) disconnect() {
  if t.conn != nil {
    t.conn.Close()
    t.conn = nil
    t.reader = nil
  }
  log.Printf("Invocation[%d]: Disconnected from [%s]", t.tracker.ID, t.target.URL)
}

func (t *TCPClient) read() []byte {
  if t.reader != nil {
    inputBuffer := make([]byte, 100)
    startTime := time.Now()
    t.conn.SetReadDeadline(util.GetConnectionReadWriteTimeout(startTime, 0, t.target.requestTimeoutD, t.target.connIdleTimeoutD))
    readSize, err := t.reader.Read(inputBuffer)
    switch err {
    case nil:
      t.status.TotalBytesRead += readSize
      return inputBuffer
    }
  }
  return nil
}
