/**
 * Copyright 2025 uk
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
	"bytes"
	"errors"
	"fmt"
	"goto/pkg/events"
	"goto/pkg/global"
	"goto/pkg/metrics"
	"goto/pkg/util"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

type TCPConfig struct {
	ListenerID             string        `json:"-"`
	Port                   int           `json:"-"`
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

type TCPConnectionHandler struct {
	TCPConfig
	conn            net.Conn
	closed          bool
	requestID       int
	status          *ConnectionStatus
	reader          *bufio.Reader
	scanner         *bufio.Scanner
	readBuffer      []byte
	writeBuffer     []byte
	readText        string
	expectedPayload []byte
	readBufferSize  int
	writeBufferSize int
	closing         bool
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
	tcpListeners      map[int]*TCPConfig                       = map[int]*TCPConfig{}
	connectionHistory map[int][]*ConnectionHistory             = map[int][]*ConnectionHistory{}
	activeConnections map[string]map[int]*TCPConnectionHandler = map[string]map[int]*TCPConnectionHandler{}
	lock              sync.RWMutex
)

func InitTCPConfig(port int, tcpConfig *TCPConfig) (*TCPConfig, string) {
	if tcpConfig == nil {
		tcpConfig = &TCPConfig{Port: port}
	} else {
		tcpConfig.Port = port
	}
	msg := tcpConfig.configure()
	if msg == "" {
		storeTCPConfig(tcpConfig)
	}
	return tcpConfig, msg
}

func storeTCPConfig(tcpConfig *TCPConfig) {
	tcpConfig.ListenerID = global.Funcs.GetListenerID(tcpConfig.Port)
	lock.Lock()
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
	lock.Unlock()
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

func (tcp *TCPConnectionHandler) resetReadBuffer() {
	tcp.readBuffer = make([]byte, tcp.readBufferSize)
	tcp.readText = ""
}

func (tcp *TCPConnectionHandler) resetWriteBuffer() {
	tcp.writeBuffer = make([]byte, tcp.writeBufferSize)
}

func (tcp *TCPConnectionHandler) closeClientConnection() {
	if tcp.conn != nil {
		tcp.closing = true
		tcp.conn.Close()
		tcp.closed = true
		tcp.status.Closed = true
		tcp.status.ConnCloseTime = time.Now().UTC().String()
	}
}

func (tcp *TCPConnectionHandler) close() {
	CloseClientConnection(tcp.ListenerID, tcp.requestID)
}

func (tcp *TCPConnectionHandler) isClosingOrClosed() bool {
	return tcp.closing || tcp.closed || !global.Funcs.IsListenerOpen(tcp.Port)
}

func (tcp *TCPConnectionHandler) processConnectionError(err error, whatFor string) {
	tcp.closing = true
	switch err {
	case io.EOF:
		log.Printf("[Listener: %s][Request: %d][%s]: Connection closed by client on port [%d]",
			tcp.ListenerID, tcp.requestID, whatFor, tcp.Port)
		tcp.status.ClientClosed = true
	default:
		tcp.status.ServerClosed = true
		if util.IsConnectionCloseError(err) {
			log.Printf("[Listener: %s][Request: %d][%s]: Connection closed by server on port [%d]",
				tcp.ListenerID, tcp.requestID, whatFor, tcp.Port)
		} else if util.IsConnectionTimeoutError(err) {
			balanceLife := util.GetConnectionRemainingLife(tcp.status.ConnStartTime, time.Now(), tcp.ConnectionLifeD, tcp.ReadTimeoutD, tcp.ConnIdleTimeoutD)
			if balanceLife < 0 {
				log.Printf("[Listener: %s][Request: %d][%s]: Max connection life [%s] reached. Closing connection on port [%d]",
					tcp.ListenerID, tcp.requestID, whatFor, tcp.ConnectionLifeD, tcp.Port)
				tcp.status.LifeTimeout = true
			} else if tcp.ConnIdleTimeoutD < tcp.ReadTimeoutD {
				log.Printf("[Listener: %s][Request: %d][%s]: Connection idle timeout [%s] reached. Closing connection on port [%d]",
					tcp.ListenerID, tcp.requestID, whatFor, tcp.ConnIdleTimeoutD, tcp.Port)
				tcp.status.IdleTimeout = true
			} else {
				log.Printf("[Listener: %s][Request: %d][%s]: Read timeout on port [%d]: %s",
					tcp.ListenerID, tcp.requestID, whatFor, tcp.Port, err.Error())
				tcp.status.ReadTimeout = true
			}
		} else {
			log.Printf("[Listener: %s][Request: %d][%s]: Error reading TCP data on port [%d]: %s",
				tcp.ListenerID, tcp.requestID, whatFor, tcp.Port, err.Error())
			tcp.status.ErrorClosed = true
		}
	}
}

func (tcp *TCPConnectionHandler) processRequest() {
	defer tcp.close()
	log.Printf("[Listener: %s][Request: %d]: Processing new request on port [%d] - {response=%t, echo=%t, stream=%t, conversation=%t, readTimeout=%s, writeTimeout=%s, connIdleTimeout=%s, connectionLife=%s}",
		tcp.ListenerID, tcp.requestID, tcp.Port, tcp.Payload, tcp.Echo, tcp.Stream, tcp.Conversation, tcp.ReadTimeout, tcp.WriteTimeout, tcp.ConnIdleTimeout, tcp.ConnectionLife)

	if tcp.Payload {
		metrics.UpdateTCPConnCount(Response)
		tcp.doResponsePayload()
	} else if tcp.Stream {
		metrics.UpdateTCPConnCount(Stream)
		tcp.doStream()
	} else if tcp.Conversation {
		metrics.UpdateTCPConnCount(Conversation)
		tcp.doConversation()
	} else if tcp.ValidatePayloadContent || tcp.ValidatePayloadLength {
		metrics.UpdateTCPConnCount(PayloadValidation)
		tcp.doPayloadValidation()
	} else if tcp.ConnectionLifeD > 0 {
		metrics.UpdateTCPConnCount(SilentLife)
		tcp.doSilentLife()
	} else if tcp.CloseAtFirstByte {
		metrics.UpdateTCPConnCount(CloseAtFirstByte)
		tcp.doCloseAtFirstByte()
	} else {
		metrics.UpdateTCPConnCount(Echo)
		tcp.doEcho()
	}
	if !global.Funcs.IsListenerOpen(tcp.Port) {
		log.Printf("[Listener: %s][Request: %d]: Listener is closed for port [%d]", tcp.ListenerID, tcp.requestID, tcp.Port)
	}
	events.SendEventJSONForPort(tcp.Port, "TCP Client Connection Closed", tcp.ListenerID, tcp.status)
}

func (tcp *TCPConnectionHandler) doSilentLife() {
	tcp.SilentLife = true
	tcp.status.TotalBytesRead = 0
	log.Printf("[Listener: %s][Request: %d][%s]: Living silent life with max connection life of [%s] on port [%d]",
		tcp.ListenerID, tcp.requestID, SilentLife, tcp.ConnectionLife, tcp.Port)
	for {
		if tcp.isClosingOrClosed() {
			log.Printf("[Listener: %s][Request: %d][%s]: Connection is closing on port [%d]",
				tcp.ListenerID, tcp.requestID, SilentLife, tcp.Port)
			tcp.sendMessage(strconv.Itoa(tcp.status.TotalBytesRead), SilentLife)
			return
		}
		if success, readSize := tcp.read(SilentLife); success {
			log.Printf("[Listener: %s][Request: %d][%s]: Read data of length [%d] for SilentLife on port [%d]. Total read so far [%d].",
				tcp.ListenerID, tcp.requestID, SilentLife, readSize, tcp.Port, tcp.status.TotalBytesRead)
		}
	}
}

func (tcp *TCPConnectionHandler) doCloseAtFirstByte() {
	tcp.CloseAtFirstByte = true
	log.Printf("[Listener: %s][Request: %d][%s]: Will close at first byte on port [%d]",
		tcp.ListenerID, tcp.requestID, CloseAtFirstByte, tcp.Port)
	tcp.conn.SetReadDeadline(time.Time{})
	tcp.status.TotalReads++
	len, err := tcp.reader.Read(make([]byte, 1))
	switch err {
	case nil:
		tcp.status.FirstByteInAt = time.Now().UTC().String()
		tcp.status.LastByteInAt = tcp.status.FirstByteInAt
		tcp.sendMessage(GoodByeMessage, CloseAtFirstByte)
		tcp.status.FirstByteOutAt = time.Now().UTC().String()
		tcp.status.LastByteOutAt = tcp.status.FirstByteOutAt
		tcp.status.TotalBytesRead = 1
		log.Printf("[Listener: %s][Request: %d][%s]: Received %d bytes, closing port [%d]",
			tcp.ListenerID, tcp.requestID, CloseAtFirstByte, len, tcp.Port)
		tcp.closing = true
		tcp.status.ServerClosed = true
	default:
		tcp.processConnectionError(err, CloseAtFirstByte)
	}
}

func (tcp *TCPConnectionHandler) doPayloadValidation() {
	if tcp.ConnectionLifeD <= 0 {
		tcp.ConnectionLifeD = 30 * time.Second
	}
	if tcp.ValidatePayloadContent {
		log.Printf("[Listener: %s][Request: %d][%s]: Will validate payload content of size [%d] over total connection life of [%s] with read timeout [%s] and idle timeout [%s] on port %d\n",
			tcp.ListenerID, tcp.requestID, PayloadValidation, tcp.ExpectedPayloadLength, tcp.ConnectionLifeD, tcp.ReadTimeoutD, tcp.ConnIdleTimeoutD, tcp.Port)
	} else {
		log.Printf("[Listener: %s][Request: %d][%s]: Will validate payload length [%d] over total connection life of [%s] with read timeout [%s] and idle timeout [%s] on port %d\n",
			tcp.ListenerID, tcp.requestID, PayloadValidation, tcp.ExpectedPayloadLength, tcp.ConnectionLifeD, tcp.ReadTimeoutD, tcp.ConnIdleTimeoutD, tcp.Port)
	}
	tcp.status.TotalBytesRead = 0
	tcp.resetReadBuffer()
	tcp.resetWriteBuffer()
	var receivedPayload []byte
	if tcp.ValidatePayloadContent {
		receivedPayload = make([]byte, tcp.ExpectedPayloadLength)
	}
	isPayloadReady := false
	checkForExcess := false
	isPayloadExcess := false
	for !isPayloadReady || checkForExcess {
		if tcp.isClosingOrClosed() {
			log.Printf("[Listener: %s][Request: %d][%s]: Ending payload validation as the connection is closing on port [%d]",
				tcp.ListenerID, tcp.requestID, PayloadValidation, tcp.Port)
			break
		}
		prevBytesRead := tcp.status.TotalBytesRead
		if success, readSize := tcp.read(PayloadValidation); success {
			if tcp.ValidatePayloadContent && tcp.status.TotalBytesRead <= tcp.ExpectedPayloadLength {
				copy(receivedPayload[prevBytesRead:prevBytesRead+readSize], tcp.readBuffer[:readSize])
			}
			log.Printf("[Listener: %s][Request: %d][%s]: Read data of length [%d] for payload validation on port [%d]. Total read so far [%d].",
				tcp.ListenerID, tcp.requestID, PayloadValidation, readSize, tcp.Port, tcp.status.TotalBytesRead)
			if tcp.status.TotalBytesRead == tcp.ExpectedPayloadLength {
				log.Printf("[Listener: %s][Request: %d][%s]: Toal payload size matches the expected length [%d] on port [%d]. Waiting for any excess byte to show up.",
					tcp.ListenerID, tcp.requestID, PayloadValidation, tcp.status.TotalBytesRead, tcp.Port)
				isPayloadReady = true
				checkForExcess = true
			} else if tcp.status.TotalBytesRead < tcp.ExpectedPayloadLength {
				log.Printf("[Listener: %s][Request: %d][%s]: Total received data of length [%d] not enough to match expected length [%d], waiting for more data on port [%d].",
					tcp.ListenerID, tcp.requestID, PayloadValidation, tcp.status.TotalBytesRead, tcp.ExpectedPayloadLength, tcp.Port)
			} else {
				log.Printf("[Listener: %s][Request: %d][%s]: Total received data of length [%d] exceeded expected length [%d] on port [%d].",
					tcp.ListenerID, tcp.requestID, PayloadValidation, tcp.status.TotalBytesRead, tcp.ExpectedPayloadLength, tcp.Port)
				isPayloadExcess = true
				isPayloadReady = true
			}
		}
	}
	msg := ""
	if !isPayloadReady {
		msg = fmt.Sprintf("[ERROR:TIMEOUT] - Timed out before receiving payload of expected length [%d] on port [%d]", tcp.ExpectedPayloadLength, tcp.Port)
	} else if isPayloadExcess {
		msg = fmt.Sprintf("[ERROR:EXCEEDED] - Payload length [%d] exceeded expected length [%d] on port [%d]", tcp.status.TotalBytesRead, tcp.ExpectedPayloadLength, tcp.Port)
	} else if tcp.ValidatePayloadContent &&
		!(bytes.Equal(receivedPayload[:tcp.ExpectedPayloadLength], tcp.expectedPayload) && tcp.readBuffer[tcp.ExpectedPayloadLength] == 0) {
		msg = fmt.Sprintf("[ERROR:CONTENT] - Payload content of length [%d] didn't match expected payload of length [%d] on port [%d]", tcp.status.TotalBytesRead, tcp.ExpectedPayloadLength, tcp.Port)
	} else {
		msg = fmt.Sprintf("[SUCCESS]: Received payload matches expected payload of length [%d] on port [%d]", tcp.status.TotalBytesRead, tcp.Port)
	}
	log.Printf("[Listener: %s][Request: %d][%s]: Sending validation result: %s.", tcp.ListenerID, tcp.requestID, PayloadValidation, msg)
	tcp.sendMessageWithDeadline(msg, PayloadValidation, false)
}

func (tcp *TCPConnectionHandler) doEcho() {
	log.Printf("[Listener: %s][Request: %d][%s]: Will echo response of size [%d] with response delay [%s] on port %d\n",
		tcp.ListenerID, tcp.requestID, Echo, tcp.EchoResponseSize, tcp.EchoResponseDelayD, tcp.Port)

	tcp.status.TotalBytesRead = 0
	tcp.writeBufferSize = tcp.EchoResponseSize
	tcp.resetWriteBuffer()
	leftover := 0
	for {
		if tcp.isClosingOrClosed() {
			log.Printf("[Listener: %s][Request: %d][%s]: Ending echo as the connection is closing on port [%d]",
				tcp.ListenerID, tcp.requestID, Echo, tcp.Port)
			return
		}
		if success, readSize := tcp.read(Echo); success {
			log.Printf("[Listener: %s][Request: %d][%s]: Read data of length [%d] for echo on port [%d]. Total read so far [%d].",
				tcp.ListenerID, tcp.requestID, Echo, readSize, tcp.Port, tcp.status.TotalBytesRead)
			if readSize+leftover >= tcp.EchoResponseSize {
				remaining := readSize + leftover
				tcp.copyInputHeadToOutput(tcp.EchoResponseSize-leftover, leftover, len(tcp.writeBuffer))
				leftover = 0
				for remaining > 0 {
					remaining = remaining - tcp.EchoResponseSize
					tcp.echoBack()
					if remaining >= tcp.EchoResponseSize {
						tcp.copyInputTailToOutput(readSize-remaining, 0, tcp.EchoResponseSize)
					} else if remaining > 0 {
						log.Printf("[Listener: %s][Request: %d][%s]: Remaining data of length [%d] not enough to match echo response size [%d], will retain for later echo on port [%d].",
							tcp.ListenerID, tcp.requestID, Echo, remaining, tcp.EchoResponseSize, tcp.Port)
						tcp.copyInputTailToOutput(readSize-remaining, 0, remaining)
						leftover = remaining
						remaining = 0
					}
				}
			} else {
				tcp.copyInputHeadToOutput(readSize, leftover, leftover+readSize)
				leftover += readSize
				log.Printf("[Listener: %s][Request: %d][%s]: Total buffered data of length [%d] not enough to match echo response size [%d], not echoing yet on port [%d].",
					tcp.ListenerID, tcp.requestID, Echo, leftover, tcp.EchoResponseSize, tcp.Port)
			}
		} else {
			log.Printf("[Listener: %s][Request: %d][%s]: Stopping echo on port [%d]",
				tcp.ListenerID, tcp.requestID, Echo, tcp.Port)
			return
		}
	}
}

func (tcp *TCPConnectionHandler) echoBack() {
	if tcp.EchoResponseDelayD > 0 {
		log.Printf("[Listener: %s][Request: %d][%s]: Delaying response by [%s] before echo on port [%d]",
			tcp.ListenerID, tcp.requestID, Echo, tcp.EchoResponseDelayD, tcp.Port)
		time.Sleep(tcp.EchoResponseDelayD)
	}
	log.Printf("[Listener: %s][Request: %d][%s]: Echoing data of length [%d] on port [%d].",
		tcp.ListenerID, tcp.requestID, Echo, tcp.EchoResponseSize, tcp.Port)
	tcp.send(Echo)
}

func (tcp *TCPConnectionHandler) doResponsePayload() {
	payloadCount := len(tcp.ResponsePayloads)
	connectionLife := tcp.ConnectionLifeD
	if connectionLife <= 0 {
		connectionLife = 30 * time.Second
	}
	log.Printf("[Listener: %s][Request: %d][%s]: Sending [%d] preconfigured responses with delay [%s], keep open [%t] and connection life [%s] on port %d\n",
		tcp.ListenerID, tcp.requestID, Response, payloadCount, tcp.ResponseDelayD, tcp.KeepOpen, connectionLife, tcp.Port)
	tcp.conn.SetWriteDeadline(time.Time{})

	responseIndex := 0
	payload := tcp.ResponsePayloads[responseIndex]

	for {
		if tcp.isClosingOrClosed() {
			log.Printf("[Listener: %s][Request: %d][%s]: Ending response as the connection is closing on port [%d]",
				tcp.ListenerID, tcp.requestID, Response, tcp.Port)
			break
		}
		if tcp.RespondAfterRead {
			if success, readSize := tcp.read(Response); success {
				log.Printf("[Listener: %s][Request: %d][%s]: Read data of length [%d] on port [%d]. Total read so far [%d].",
					tcp.ListenerID, tcp.requestID, Response, readSize, tcp.Port, tcp.status.TotalBytesRead)
			}
		}
		time.Sleep(tcp.ResponseDelayD)
		tcp.writeBufferSize = len(payload)
		tcp.resetWriteBuffer()
		if !tcp.sendDataToClient([]byte(payload), Response) {
			log.Printf("[Listener: %s][Request: %d][%s]: Ending response as failed to send data on port [%d]",
				tcp.ListenerID, tcp.requestID, Response, tcp.Port)
			break
		}
		responseIndex++
		remainingLife := util.GetConnectionRemainingLife(tcp.status.ConnStartTime, time.Now(), connectionLife, 0, 0)
		if connectionLife > 0 && remainingLife <= 0 {
			log.Printf("[Listener: %s][Request: %d][%s]: Max connection life [%s] reached. Will not send response on port [%d]",
				tcp.ListenerID, tcp.requestID, Response, connectionLife, tcp.Port)
			break
		}
		if responseIndex >= payloadCount {
			if tcp.KeepOpen && remainingLife > 0 {
				log.Printf("[Listener: %s][Request: %d][%s]: Keeping connection alive for [%s] after sending [%d] responses on port [%d]",
					tcp.ListenerID, tcp.requestID, Response, remainingLife, payloadCount, tcp.Port)
				time.Sleep(remainingLife)
			}
			break
		} else if tcp.ResponsePayloads[responseIndex] != "" {
			payload = tcp.ResponsePayloads[responseIndex]
		}
	}
	log.Printf("[Listener: %s][Request: %d][%s]: Finished processing [%d] responses on port [%d]",
		tcp.ListenerID, tcp.requestID, Response, payloadCount, tcp.Port)
}

func (tcp *TCPConnectionHandler) doStream() {
	log.Printf("[Listener: %s][Request: %d][%s]: Streaming [%d] chunks of size [%d] with delay [%s] for a duration of [%s] to serve total payload of [%d] on port %d\n",
		tcp.ListenerID, tcp.requestID, Stream, tcp.StreamChunkCount, tcp.StreamChunkSizeV, tcp.StreamChunkDelayD, tcp.StreamDurationD, tcp.StreamPayloadSizeV, tcp.Port)
	tcp.conn.SetWriteDeadline(time.Time{})
	tcp.writeBufferSize = tcp.StreamChunkSizeV
	tcp.resetWriteBuffer()
	payload := util.GenerateRandomPayload(tcp.StreamChunkSizeV)
	for i := 0; i < tcp.StreamChunkCount; i++ {
		if tcp.isClosingOrClosed() {
			log.Printf("[Listener: %s][Request: %d][%s]: Ending stream as the connection is closing on port [%d]",
				tcp.ListenerID, tcp.requestID, Stream, tcp.Port)
			return
		}
		time.Sleep(tcp.StreamChunkDelayD)
		if tcp.ConnectionLifeD > 0 && util.GetConnectionRemainingLife(tcp.status.ConnStartTime, time.Now(), tcp.ConnectionLifeD, 0, 0) <= 0 {
			log.Printf("[Listener: %s][Request: %d][%s]: Max connection life [%s] reached. Stopping stream on port [%d]",
				tcp.ListenerID, tcp.requestID, Stream, tcp.ConnectionLifeD, tcp.Port)
			break
		}
		tcp.sendDataToClient(payload, Stream)
	}
}

func (tcp *TCPConnectionHandler) doConversation() {
	log.Printf("[Listener: %s][Request: %d][%s]: Starting conversation with client with read timeout [%s], write timeout [%s], for total connection life of [%s] on port %d\n",
		tcp.ListenerID, tcp.requestID, Conversation, tcp.ReadTimeoutD, tcp.WriteTimeoutD, tcp.ConnectionLifeD, tcp.Port)
	tcp.resetWriteBuffer()
	tcp.doHello()
	for {
		if tcp.isClosingOrClosed() {
			log.Printf("[Listener: %s][Request: %d][%s]: Ending conversation as the connection is closing on port [%d]", tcp.ListenerID, tcp.requestID, Conversation, tcp.Port)
			return
		}
		if message := tcp.readMessage(); message != "" {
			log.Printf("[Listener: %s][Request: %d][%s]: Received message [%s] from client on port %d\n",
				tcp.ListenerID, tcp.requestID, Conversation, message, tcp.Port)
			if strings.Contains(strings.ToUpper(message), GoodByeMessage) {
				break
			}
			tcp.processClientMessage(message)
		} else if !tcp.isClosingOrClosed() {
			log.Printf("[Listener: %s][Request: %d][%s]: Received empty message from client on port %d\n",
				tcp.ListenerID, tcp.requestID, Conversation, tcp.Port)
		}
	}
	tcp.sendMessage(GoodByeMessage, Conversation)
}

func (tcp *TCPConnectionHandler) doHello() {
	if tcp.isClosingOrClosed() {
		log.Printf("[Listener: %s][Request: %d][Hello]: doHello called on a closing connection on port [%d]", tcp.ListenerID, tcp.requestID, tcp.Port)
		return
	}
	log.Printf("[Listener: %s][Request: %d][Hello]: Waiting for client Hello on port [%d].",
		tcp.ListenerID, tcp.requestID, tcp.Port)
	if message := tcp.readMessage(); message != "" {
		log.Printf("[Listener: %s][Request: %d][Hello]: Client said [%s] on port [%d].",
			tcp.ListenerID, tcp.requestID, message, tcp.Port)
		if strings.Contains(strings.ToUpper(message), HelloMessage) {
			log.Printf("[Listener: %s][Request: %d][Hello]: Sending %s back to client on port [%d].",
				tcp.ListenerID, tcp.requestID, HelloMessage, tcp.Port)
			tcp.sendMessage(HelloMessage, Conversation)
		}
	}
}

func (tcp *TCPConnectionHandler) processClientMessage(message string) {
	parts := strings.Split(message, "/")
	if len(parts) == 3 {
		parts[0] = strings.Trim(parts[0], " \n\r")
		parts[2] = strings.Trim(parts[2], " \n\r")
		if strings.Contains(parts[0], "BEGIN") && strings.Contains(parts[2], "END") {
			log.Printf("[Listener: %s][Request: %d][%s]: Client message was [%s] on port [%d].",
				tcp.ListenerID, tcp.requestID, Conversation, parts[1], tcp.Port)
			tcp.sendMessage(fmt.Sprintf("ACK/%s/END", parts[1]), Conversation)
			return
		}
	}
	log.Printf("[Listener: %s][Request: %d][%s]: Malformed client message [%s] on port [%d].",
		tcp.ListenerID, tcp.requestID, Conversation, message, tcp.Port)
	tcp.sendMessage("ERROR", Conversation)
}

func (tcp *TCPConnectionHandler) read(whatFor string) (bool, int) {
	return tcp.readOrScan(false, whatFor)
}

func (tcp *TCPConnectionHandler) scan(whatFor string) string {
	tcp.readOrScan(true, whatFor)
	return tcp.readText
}

func (tcp *TCPConnectionHandler) readMessage() string {
	tcp.resetReadBuffer()
	return tcp.scan(Conversation)
}

func (tcp *TCPConnectionHandler) readOrScan(scan bool, whatFor string) (bool, int) {
	if tcp.isClosingOrClosed() {
		log.Printf("[Listener: %s][Request: %d][%s]: ReadOrScan called on a closing connection on port [%d]", tcp.ListenerID, tcp.requestID, whatFor, tcp.Port)
		return false, 0
	}
	tcp.updateReadDeadline()
	readSize := 0
	var err error
	tcp.status.TotalReads++
	if scan {
		if tcp.scanner.Scan() {
			tcp.readText = tcp.scanner.Text()
			readSize = len(tcp.readText)
		} else {
			err = errors.New("No text scanned")
		}
	} else {
		readSize, err = tcp.reader.Read(tcp.readBuffer)
	}
	switch err {
	case nil:
		now := time.Now().UTC().String()
		if tcp.status.FirstByteInAt == "" {
			tcp.status.FirstByteInAt = now
		}
		tcp.status.LastByteInAt = now
		tcp.status.TotalBytesRead += readSize
		return true, readSize
	case io.EOF:
		log.Printf("[Listener: %s][Request: %d][%s]: Connection closed by client on port [%d]",
			tcp.ListenerID, tcp.requestID, whatFor, tcp.Port)
		tcp.closing = true
		return false, 0
	default:
		tcp.processConnectionError(err, whatFor)
		return false, 0
	}
}

func (tcp *TCPConnectionHandler) send(whatFor string) bool {
	return tcp.sendDataToClient(tcp.writeBuffer, whatFor)
}

func (tcp *TCPConnectionHandler) sendMessage(message, whatFor string) {
	tcp.sendMessageWithDeadline(message, whatFor, false)
}

func (tcp *TCPConnectionHandler) sendMessageWithDeadline(message, whatFor string, useConnDeadline bool) {
	message = fmt.Sprintf("[%s]%s", global.Funcs.GetHostLabelForPort(tcp.Port), message)
	if tcp.sendDataToClientWithDeadline([]byte(message), whatFor, useConnDeadline) {
		log.Printf("[Listener: %s][Request: %d][%s]: Sent {%s} on port [%d]",
			tcp.ListenerID, tcp.requestID, whatFor, message, tcp.Port)
	} else {
		log.Printf("[Listener: %s][Request: %d][%s]: Error sending {%s} on port [%d]",
			tcp.ListenerID, tcp.requestID, whatFor, message, tcp.Port)
	}
}

func (tcp *TCPConnectionHandler) sendDataToClient(data []byte, whatFor string) bool {
	return tcp.sendDataToClientWithDeadline(data, whatFor, true)
}

func (tcp *TCPConnectionHandler) sendDataToClientWithDeadline(data []byte, whatFor string, useConnDeadline bool) bool {
	if useConnDeadline && tcp.isClosingOrClosed() {
		log.Printf("[Listener: %s][Request: %d][%s]: Send called on a closing/closed connection", tcp.ListenerID, tcp.requestID, whatFor)
		return false
	}
	if useConnDeadline {
		tcp.updateWriteDeadline()
	} else {
		tcp.conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	}
	sentLength := 0
	dataLength := len(data)
	for {
		tcp.status.TotalWrites++
		if len, err := tcp.conn.Write(data); err != nil {
			tcp.status.WriteErrors++
			log.Printf("[Listener: %s][Request: %d][%s]: Error sending data of length %d: %s",
				tcp.ListenerID, tcp.requestID, whatFor, dataLength, err.Error())
			return false
		} else {
			now := time.Now().UTC().String()
			if tcp.status.FirstByteOutAt == "" {
				tcp.status.FirstByteOutAt = now
			}
			tcp.status.LastByteOutAt = now
			tcp.status.TotalBytesSent += len
			sentLength += len
			if sentLength < dataLength {
				log.Printf("[Listener: %s][Request: %d][%s]: Sent data of length %d, remaining to send: %d. Will keep sending.",
					tcp.ListenerID, tcp.requestID, whatFor, len, dataLength-sentLength)
			} else {
				log.Printf("[Listener: %s][Request: %d][%s]: Sent data of length %d, total sent: %d",
					tcp.ListenerID, tcp.requestID, whatFor, len, sentLength)
				return true
			}
		}
	}
}

func (tcp *TCPConnectionHandler) updateReadDeadline() {
	util.UpdateReadDeadline(tcp.conn, tcp.status.ConnStartTime, tcp.ConnectionLifeD, tcp.ReadTimeoutD, tcp.ConnIdleTimeoutD)
}

func (tcp *TCPConnectionHandler) updateWriteDeadline() {
	util.UpdateWriteDeadline(tcp.conn, tcp.status.ConnStartTime, tcp.ConnectionLifeD, tcp.WriteTimeoutD, tcp.ConnIdleTimeoutD)
}

func (tcp *TCPConnectionHandler) copyInputHeadToOutput(inHead, outFrom, outTo int) {
	util.CopyInputHeadToOutput(tcp.writeBuffer, tcp.readBuffer, inHead, outFrom, outTo)
}

func (tcp *TCPConnectionHandler) copyInputTailToOutput(tail, outFrom, outTo int) {
	util.CopyInputTailToOutput(tcp.writeBuffer, tcp.readBuffer, tail, outFrom, outTo)
}
