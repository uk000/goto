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
	"goto/pkg/events"
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
}

type ConnectionHistory struct {
	Config *TCPConfig        `json:"config"`
	Status *ConnectionStatus `json:"status"`
}

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

func getTCPConfig(port int) *TCPConfig {
	lock.RLock()
	defer lock.RUnlock()
	return tcpListeners[port]
}

func ServeClientConnection(port, requestID int, conn net.Conn) bool {
	metrics.UpdateConnCount("tcp")
	tcpConfig := getTCPConfig(port)
	if tcpConfig == nil {
		log.Printf("Cannot serve TCP on port %d without any config", port)
		return false
	}
	connectionStatus := &ConnectionStatus{Port: port, ListenerID: tcpConfig.ListenerID, RequestID: requestID, ConnStartTime: time.Now()}
	tcpHandler := &TCPConnectionHandler{conn: conn, requestID: requestID, status: connectionStatus}
	tcpHandler.TCPConfig = *tcpConfig
	events.SendEventJSONForPort(port, "New TCP Client Connection", tcpConfig.ListenerID, tcpHandler)
	lock.Lock()
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

func CloseListenerConnections(listenerID string) {
	lock.Lock()
	defer lock.Unlock()
	for _, tcpHandler := range activeConnections[listenerID] {
		tcpHandler.status.ServerClosed = true
		tcpHandler.closeClientConnection()
	}
	delete(activeConnections, listenerID)
}

func CloseClientConnection(listenerID string, requestID int) {
	lock.Lock()
	defer lock.Unlock()
	if activeConnections[listenerID] != nil {
		if tcpHandler := activeConnections[listenerID][requestID]; tcpHandler != nil {
			tcpHandler.closeClientConnection()
			delete(activeConnections[listenerID], requestID)
		}
		if len(activeConnections[listenerID]) == 0 {
			delete(activeConnections, listenerID)
		}
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
