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

package tcp

import (
	"bufio"
	"encoding/json"
	"goto/pkg/global"
	"goto/pkg/metrics"
	"log"
	"net"
	"sync"
	"time"
)

type TCPConfig struct {
	ListenerID             string        `json:"-"`
	Port                   int           `json:"-"`
	TLS                    bool          `json:"tls"`
	MTLS                   bool          `json:"mtls"`
	ReadTimeout            string        `json:"readTimeout"`
	WriteTimeout           string        `json:"writeTimeout"`
	ConnectTimeout         string        `json:"connectTimeout"`
	ConnIdleTimeout        string        `json:"connIdleTimeout"`
	ConnectionLife         string        `json:"connectionLife"`
	KeepOpen               bool          `json:"keepOpen"`
	Payload                bool          `json:"payload"`
	Stream                 bool          `json:"stream"`
	Echo                   bool          `json:"echo"`
	Conversation           bool          `json:"conversation"`
	SilentLife             bool          `json:"silentLife"`
	CloseAtFirstByte       bool          `json:"closeAtFirstByte"`
	ValidatePayloadLength  bool          `json:"validatePayloadLength"`
	ValidatePayloadContent bool          `json:"validatePayloadContent"`
	ExpectedPayloadLength  int           `json:"expectedPayloadLength"`
	EchoResponseSize       int           `json:"echoResponseSize"`
	EchoResponseDelay      string        `json:"echoResponseDelay"`
	ResponsePayloads       []string      `json:"responsePayloads"`
	ResponseDelay          string        `json:"responseDelay"`
	RespondAfterRead       bool          `json:"respondAfterRead"`
	StreamPayloadSize      string        `json:"streamPayloadSize"`
	StreamChunkSize        string        `json:"streamChunkSize"`
	StreamChunkCount       int           `json:"streamChunkCount"`
	StreamChunkDelay       string        `json:"streamChunkDelay"`
	StreamDuration         string        `json:"streamDuration"`
	StreamPayloadSizeV     int           `json:"-"`
	StreamChunkSizeV       int           `json:"-"`
	StreamChunkDelayD      time.Duration `json:"-"`
	StreamDurationD        time.Duration `json:"-"`
	ExpectedPayload        []byte        `json:"-"`
	ReadTimeoutD           time.Duration `json:"-"`
	WriteTimeoutD          time.Duration `json:"-"`
	ConnectTimeoutD        time.Duration `json:"-"`
	ConnIdleTimeoutD       time.Duration `json:"-"`
	EchoResponseDelayD     time.Duration `json:"-"`
	ResponseDelayD         time.Duration `json:"-"`
	ConnectionLifeD        time.Duration `json:"-"`
}

type ConnectionStatus struct {
	Port           int       `json:"port"`
	ListenerID     string    `json:"listenerID"`
	RequestID      int       `json:"requestID"`
	ConnStartTime  time.Time `json:"connStartTime"`
	ConnCloseTime  string    `json:"connCloseTime"`
	FirstByteInAt  string    `json:"firstByteInAt"`
	LastByteInAt   string    `json:"lastByteInAt"`
	FirstByteOutAt string    `json:"firstByteOutAt"`
	LastByteOutAt  string    `json:"lastByteOutAt"`
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
	// mu guards the fields above: the connection's goroutine updates them while the admin APIs report them,
	// and the listener's shutdown flags them from a third goroutine.
	mu sync.Mutex
}

type connectionStatusJSON ConnectionStatus

// MarshalJSON reports the status as it is at one moment, as the connection keeps updating it while it is reported.
func (s *ConnectionStatus) MarshalJSON() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return json.Marshal((*connectionStatusJSON)(s))
}

// update changes the status while holding its lock.
func (s *ConnectionStatus) update(f func(s *ConnectionStatus)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s)
}

// bytesRead gives the total bytes read so far.
func (s *ConnectionStatus) bytesRead() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.TotalBytesRead
}

type ConnectionHistory struct {
	Config *TCPConfig        `json:"config"`
	Status *ConnectionStatus `json:"status"`
}

// maxExpectedPayloadLength is the longest payload that a port can be set to expect (64 MiB): the connections that validate
// the content keep what they receive, and the length is what a client can make them keep.
var maxExpectedPayloadLength = 64 << 20

const HelloMessage string = "HELLO"
const GoodByeMessage string = "GOODBYE"
const Response string = "Response"
const Conversation string = "Conversation"
const Echo string = "Echo"
const Stream string = "Stream"
const PayloadValidation string = "Payload"
const SilentLife string = "SilentLife"
const CloseAtFirstByte string = "CloseAtFirstByte"

var (
	tcpListeners      = map[int]*TCPConfig{}
	connectionHistory = map[int][]*ConnectionHistory{}
	activeConnections = map[string]map[int]*TCPConnectionHandler{}
	lock              sync.RWMutex
)

// InitTCPConfig configures the TCP config of a port and stores it if valid, otherwise it returns the error message.
func InitTCPConfig(port int, tcpConfig *TCPConfig) (*TCPConfig, string) {
	if tcpConfig == nil {
		tcpConfig = &TCPConfig{Port: port}
	} else {
		tcpConfig.Port = port
	}
	msg := tcpConfig.Configure()
	if msg == "" {
		storeTCPConfig(tcpConfig)
	}
	return tcpConfig, msg
}

// storeTCPConfig stores the config of a port, defaulting to echo (or silent life) mode if no mode is set.
func storeTCPConfig(tcpConfig *TCPConfig) {
	lock.Lock()
	defer lock.Unlock()
	tcpConfig.ListenerID = global.Funcs.GetListenerID(tcpConfig.Port)
	if !tcpConfig.Payload && !tcpConfig.Echo && !tcpConfig.Stream && !tcpConfig.Conversation &&
		!tcpConfig.ValidatePayloadContent && !tcpConfig.ValidatePayloadLength &&
		!tcpConfig.SilentLife && !tcpConfig.CloseAtFirstByte {
		if tcpConfig.ConnectionLifeD > 0 {
			tcpConfig.SilentLife = true
		} else {
			tcpConfig.Echo = true
		}
	}
	if tcpListeners[tcpConfig.Port] == nil {
		tcpListeners[tcpConfig.Port] = tcpConfig
	} else {
		*tcpListeners[tcpConfig.Port] = *tcpConfig
	}
	if connectionHistory[tcpConfig.Port] == nil {
		connectionHistory[tcpConfig.Port] = []*ConnectionHistory{}
	}
}

// getTCPConfig gives the stored TCP config of a port, if any.
func getTCPConfig(port int) *TCPConfig {
	lock.RLock()
	defer lock.RUnlock()
	return tcpListeners[port]
}

// updateTCPConfig changes the config of a port while holding the lock that guards the configs, which the connections
// copy from. It tells whether the port has a config.
func updateTCPConfig(port int, update func(c *TCPConfig)) bool {
	lock.Lock()
	defer lock.Unlock()
	c := tcpListeners[port]
	if c == nil {
		return false
	}
	update(c)
	return true
}

// mode tells which of the modes a connection with this config operates in. It is the order in which the modes win
// when more than one of them is configured.
func (tcp *TCPConfig) mode() string {
	switch {
	case tcp.Payload:
		return Response
	case tcp.Stream:
		return Stream
	case tcp.Conversation:
		return Conversation
	case tcp.ValidatePayloadContent || tcp.ValidatePayloadLength:
		return PayloadValidation
	case tcp.ConnectionLifeD > 0:
		return SilentLife
	case tcp.CloseAtFirstByte:
		return CloseAtFirstByte
	}
	return Echo
}

// ServeClientConnection serves the connection in the mode that the port is configured with, in a goroutine of its own. A
// connection to a port that has no config is closed, as there is nothing to serve it with.
func ServeClientConnection(port, requestID int, conn net.Conn) bool {
	metrics.UpdateConnCount("tcp")
	lock.Lock()
	tcpConfig := tcpListeners[port]
	if tcpConfig == nil {
		lock.Unlock()
		log.Printf("Cannot serve TCP on port %d without any config", port)
		if conn != nil {
			conn.Close()
		}
		return false
	}
	connectionStatus := &ConnectionStatus{Port: port, ListenerID: tcpConfig.ListenerID, RequestID: requestID, ConnStartTime: time.Now()}
	tcpHandler := &TCPConnectionHandler{conn: conn, requestID: requestID, status: connectionStatus}
	// The connection works on a copy of the config, that the changes to the config of the port don't reach.
	tcpHandler.TCPConfig = *tcpConfig
	if tcpHandler.mode() == SilentLife {
		// The mode is reported by the config of the connection, which must not change once the connection is reported.
		tcpHandler.SilentLife = true
	}
	connectionHistory[port] = append(connectionHistory[port], &ConnectionHistory{Config: &tcpHandler.TCPConfig, Status: connectionStatus})
	if activeConnections[tcpConfig.ListenerID] == nil {
		activeConnections[tcpConfig.ListenerID] = map[int]*TCPConnectionHandler{}
	}
	activeConnections[tcpConfig.ListenerID][requestID] = tcpHandler
	lock.Unlock()
	tcpHandler.reader = bufio.NewReader(conn)
	tcpHandler.scanner = bufio.NewScanner(tcpHandler.reader)
	tcpHandler.readBufferSize = 100
	tcpHandler.writeBufferSize = 100
	tcpHandler.resetReadBuffer()
	tcpHandler.resetWriteBuffer()
	go tcpHandler.processRequest()
	return true
}

// CloseListenerConnections closes all the connections that a listener has, which are the ones of the listener's port that
// were accepted by that listener (and not by the one that it replaced).
func CloseListenerConnections(listenerID string) {
	lock.Lock()
	handlers := make([]*TCPConnectionHandler, 0, len(activeConnections[listenerID]))
	for _, tcpHandler := range activeConnections[listenerID] {
		handlers = append(handlers, tcpHandler)
	}
	delete(activeConnections, listenerID)
	lock.Unlock()
	// The connections are closed after the lock is let go: closing a connection may take time, and the APIs and the other
	// connections wait for that lock.
	for _, tcpHandler := range handlers {
		tcpHandler.status.update(func(s *ConnectionStatus) { s.ServerClosed = true })
		tcpHandler.closeClientConnection()
	}
}

// CloseClientConnection closes a connection of a listener, if the listener still has it.
func CloseClientConnection(listenerID string, requestID int) {
	var tcpHandler *TCPConnectionHandler
	lock.Lock()
	if activeConnections[listenerID] != nil {
		tcpHandler = activeConnections[listenerID][requestID]
		delete(activeConnections[listenerID], requestID)
		if len(activeConnections[listenerID]) == 0 {
			delete(activeConnections, listenerID)
		}
	}
	lock.Unlock()
	if tcpHandler != nil {
		tcpHandler.closeClientConnection()
	}
}

// type PeerMetadata struct {
// 	Name      string            `json:"name,omitempty"`
// 	Namespace string            `json:"namespace,omitempty"`
// 	Labels    map[string]string `json:"labels,omitempty"`
// 	Owner     string            `json:"owner,omitempty"`
// 	Platform  string            `json:"platform,omitempty"`
// 	Workload  string            `json:"workload_name,omitempty"`
// }
