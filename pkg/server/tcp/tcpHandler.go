package tcp

import (
	"bufio"
	"bytes"
	"fmt"
	"goto/pkg/global"
	"goto/pkg/metrics"
	"goto/pkg/types"
	"goto/pkg/util"
	"io"
	"log"
	"net"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type TCPConnectionHandler struct {
	TCPConfig
	conn            net.Conn
	closed          atomic.Bool
	requestID       int
	status          *ConnectionStatus
	reader          *bufio.Reader
	scanner         *bufio.Scanner
	readBuffer      []byte
	writeBuffer     []byte
	readText        string
	readBufferSize  int
	writeBufferSize int
	// closing and closed are set by the goroutine of the connection, and by the one that closes the listener's connections.
	closing atomic.Bool
}

// resetReadBuffer allocates a fresh read buffer and clears the last text read.
func (tcp *TCPConnectionHandler) resetReadBuffer() {
	tcp.readBuffer = make([]byte, tcp.readBufferSize)
	tcp.readText = ""
}

// resetWriteBuffer allocates a fresh write buffer of the configured write buffer size.
func (tcp *TCPConnectionHandler) resetWriteBuffer() {
	tcp.writeBuffer = make([]byte, tcp.writeBufferSize)
}

// closeClientConnection closes the client connection and marks the connection status closed with the close time.
func (tcp *TCPConnectionHandler) closeClientConnection() {
	if tcp.conn != nil {
		tcp.closing.Store(true)
		tcp.conn.Close()
		tcp.closed.Store(true)
		tcp.status.update(func(s *ConnectionStatus) {
			s.Closed = true
			s.ConnCloseTime = time.Now().UTC().String()
		})
	}
}

// close closes the connection through the listener's registry of active connections.
func (tcp *TCPConnectionHandler) close() {
	CloseClientConnection(tcp.ListenerID, tcp.requestID)
}

// isClosingOrClosed tells if the connection is closing or closed, or its listener is no longer open.
func (tcp *TCPConnectionHandler) isClosingOrClosed() bool {
	return tcp.closing.Load() || tcp.closed.Load() || !global.Funcs.IsListenerOpen(tcp.Port)
}

// processConnectionError marks the connection as closing, and records in its status whether the client closed it, or it timed out (life, idle or read), or it failed.
func (tcp *TCPConnectionHandler) processConnectionError(err error, whatFor string) {
	tcp.closing.Store(true)
	switch err {
	case io.EOF:
		log.Printf("[Listener: %s][Request: %d][%s]: Connection closed by client on port [%d]",
			tcp.ListenerID, tcp.requestID, whatFor, tcp.Port)
		tcp.status.update(func(s *ConnectionStatus) { s.ClientClosed = true })
	default:
		tcp.status.update(func(s *ConnectionStatus) { s.ServerClosed = true })
		if util.IsConnectionCloseError(err) {
			log.Printf("[Listener: %s][Request: %d][%s]: Connection closed by server on port [%d]",
				tcp.ListenerID, tcp.requestID, whatFor, tcp.Port)
		} else if util.IsConnectionTimeoutError(err) {
			balanceLife := util.GetConnectionRemainingLife(tcp.status.ConnStartTime, time.Now(), tcp.ConnectionLifeD, tcp.ReadTimeoutD, tcp.ConnIdleTimeoutD)
			if balanceLife < 0 {
				log.Printf("[Listener: %s][Request: %d][%s]: Max connection life [%s] reached. Closing connection on port [%d]",
					tcp.ListenerID, tcp.requestID, whatFor, tcp.ConnectionLifeD, tcp.Port)
				tcp.status.update(func(s *ConnectionStatus) { s.LifeTimeout = true })
			} else if tcp.ConnIdleTimeoutD < tcp.ReadTimeoutD {
				log.Printf("[Listener: %s][Request: %d][%s]: Connection idle timeout [%s] reached. Closing connection on port [%d]",
					tcp.ListenerID, tcp.requestID, whatFor, tcp.ConnIdleTimeoutD, tcp.Port)
				tcp.status.update(func(s *ConnectionStatus) { s.IdleTimeout = true })
			} else {
				log.Printf("[Listener: %s][Request: %d][%s]: Read timeout on port [%d]: %s",
					tcp.ListenerID, tcp.requestID, whatFor, tcp.Port, err.Error())
				tcp.status.update(func(s *ConnectionStatus) { s.ReadTimeout = true })
			}
		} else {
			log.Printf("[Listener: %s][Request: %d][%s]: Error reading TCP data on port [%d]: %s",
				tcp.ListenerID, tcp.requestID, whatFor, tcp.Port, err.Error())
			tcp.status.update(func(s *ConnectionStatus) { s.ErrorClosed = true })
		}
	}
}

// processRequest serves the connection in the mode that its config selects, and closes the connection when done.
func (tcp *TCPConnectionHandler) processRequest() {
	defer tcp.close()
	// The connection is served by a goroutine of its own, where a panic is not recovered by anything and ends the whole
	// server: it ends the connection instead.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[Listener: %s][Request: %d]: Recovered from panic on port [%d]: %v\n%s", tcp.ListenerID, tcp.requestID, tcp.Port, r, debug.Stack())
			tcp.closing.Store(true)
			tcp.status.update(func(s *ConnectionStatus) { s.ErrorClosed = true })
		}
	}()
	log.Printf("[Listener: %s][Request: %d]: Processing new request on port [%d] - {response=%t, echo=%t, stream=%t, conversation=%t, readTimeout=%s, writeTimeout=%s, connIdleTimeout=%s, connectionLife=%s}",
		tcp.ListenerID, tcp.requestID, tcp.Port, tcp.Payload, tcp.Echo, tcp.Stream, tcp.Conversation, tcp.ReadTimeout, tcp.WriteTimeout, tcp.ConnIdleTimeout, tcp.ConnectionLife)

	mode := tcp.mode()
	metrics.UpdateTCPConnCount(mode)
	switch mode {
	case Response:
		tcp.doResponsePayload()
	case Stream:
		tcp.doStream()
	case Conversation:
		tcp.doConversation()
	case PayloadValidation:
		tcp.doPayloadValidation()
	case SilentLife:
		tcp.doSilentLife()
	case CloseAtFirstByte:
		tcp.doCloseAtFirstByte()
	default:
		tcp.doEcho()
	}
	if !global.Funcs.IsListenerOpen(tcp.Port) {
		log.Printf("[Listener: %s][Request: %d]: Listener is closed for port [%d]", tcp.ListenerID, tcp.requestID, tcp.Port)
	}
}

// doSilentLife reads and discards what the client sends until the connection ends, then reports the total bytes read.
func (tcp *TCPConnectionHandler) doSilentLife() {
	tcp.status.update(func(s *ConnectionStatus) { s.TotalBytesRead = 0 })
	log.Printf("[Listener: %s][Request: %d][%s]: Living silent life with max connection life of [%s] on port [%d]",
		tcp.ListenerID, tcp.requestID, SilentLife, tcp.ConnectionLife, tcp.Port)
	for {
		if tcp.isClosingOrClosed() {
			log.Printf("[Listener: %s][Request: %d][%s]: Connection is closing on port [%d]",
				tcp.ListenerID, tcp.requestID, SilentLife, tcp.Port)
			tcp.sendMessage(strconv.Itoa(tcp.status.bytesRead()), SilentLife)
			return
		}
		if success, readSize := tcp.read(SilentLife); success {
			log.Printf("[Listener: %s][Request: %d][%s]: Read data of length [%d] for SilentLife on port [%d]. Total read so far [%d].",
				tcp.ListenerID, tcp.requestID, SilentLife, readSize, tcp.Port, tcp.status.bytesRead())
		}
	}
}

// doCloseAtFirstByte waits for the first byte from the client, responds with GOODBYE, and closes the connection.
func (tcp *TCPConnectionHandler) doCloseAtFirstByte() {
	log.Printf("[Listener: %s][Request: %d][%s]: Will close at first byte on port [%d]",
		tcp.ListenerID, tcp.requestID, CloseAtFirstByte, tcp.Port)
	tcp.conn.SetReadDeadline(time.Time{})
	tcp.status.update(func(s *ConnectionStatus) { s.TotalReads++ })
	len, err := tcp.reader.Read(make([]byte, 1))
	switch err {
	case nil:
		firstByteAt := time.Now().UTC().String()
		tcp.status.update(func(s *ConnectionStatus) {
			s.FirstByteInAt = firstByteAt
			s.LastByteInAt = firstByteAt
		})
		tcp.sendMessage(GoodByeMessage, CloseAtFirstByte)
		tcp.status.update(func(s *ConnectionStatus) {
			s.FirstByteOutAt = time.Now().UTC().String()
			s.LastByteOutAt = s.FirstByteOutAt
			s.TotalBytesRead = 1
			s.ServerClosed = true
		})
		log.Printf("[Listener: %s][Request: %d][%s]: Received %d bytes, closing port [%d]",
			tcp.ListenerID, tcp.requestID, CloseAtFirstByte, len, tcp.Port)
		tcp.closing.Store(true)
	default:
		tcp.processConnectionError(err, CloseAtFirstByte)
	}
}

// doPayloadValidation reads until the expected payload length arrives (checking the content if configured, and for any excess), then sends the validation result.
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
	tcp.status.update(func(s *ConnectionStatus) { s.TotalBytesRead = 0 })
	tcp.resetReadBuffer()
	tcp.resetWriteBuffer()
	// what is received is kept as it arrives, as a connection that sends nothing keeps nothing
	var receivedPayload []byte
	isPayloadReady := false
	checkForExcess := false
	isPayloadExcess := false
	for !isPayloadReady || checkForExcess {
		if tcp.isClosingOrClosed() {
			log.Printf("[Listener: %s][Request: %d][%s]: Ending payload validation as the connection is closing on port [%d]",
				tcp.ListenerID, tcp.requestID, PayloadValidation, tcp.Port)
			break
		}
		if success, readSize := tcp.read(PayloadValidation); success {
			totalRead := tcp.status.bytesRead()
			if tcp.ValidatePayloadContent && totalRead <= tcp.ExpectedPayloadLength {
				receivedPayload = append(receivedPayload, tcp.readBuffer[:readSize]...)
			}
			log.Printf("[Listener: %s][Request: %d][%s]: Read data of length [%d] for payload validation on port [%d]. Total read so far [%d].",
				tcp.ListenerID, tcp.requestID, PayloadValidation, readSize, tcp.Port, totalRead)
			if totalRead == tcp.ExpectedPayloadLength {
				log.Printf("[Listener: %s][Request: %d][%s]: Toal payload size matches the expected length [%d] on port [%d]. Waiting for any excess byte to show up.",
					tcp.ListenerID, tcp.requestID, PayloadValidation, totalRead, tcp.Port)
				isPayloadReady = true
				checkForExcess = true
			} else if totalRead < tcp.ExpectedPayloadLength {
				log.Printf("[Listener: %s][Request: %d][%s]: Total received data of length [%d] not enough to match expected length [%d], waiting for more data on port [%d].",
					tcp.ListenerID, tcp.requestID, PayloadValidation, totalRead, tcp.ExpectedPayloadLength, tcp.Port)
			} else {
				log.Printf("[Listener: %s][Request: %d][%s]: Total received data of length [%d] exceeded expected length [%d] on port [%d].",
					tcp.ListenerID, tcp.requestID, PayloadValidation, totalRead, tcp.ExpectedPayloadLength, tcp.Port)
				isPayloadExcess = true
				isPayloadReady = true
				// there is no more to wait for: more than expected has come, however it came
				checkForExcess = false
			}
		}
	}
	totalRead := tcp.status.bytesRead()
	msg := ""
	if !isPayloadReady {
		msg = fmt.Sprintf("[ERROR:TIMEOUT] - Timed out before receiving payload of expected length [%d] on port [%d]", tcp.ExpectedPayloadLength, tcp.Port)
	} else if isPayloadExcess {
		msg = fmt.Sprintf("[ERROR:EXCEEDED] - Payload length [%d] exceeded expected length [%d] on port [%d]", totalRead, tcp.ExpectedPayloadLength, tcp.Port)
	} else if tcp.ValidatePayloadContent && !bytes.Equal(receivedPayload, tcp.ExpectedPayload) {
		msg = fmt.Sprintf("[ERROR:CONTENT] - Payload content of length [%d] didn't match expected payload of length [%d] on port [%d]", totalRead, tcp.ExpectedPayloadLength, tcp.Port)
	} else {
		msg = fmt.Sprintf("[SUCCESS]: Received payload matches expected payload of length [%d] on port [%d]", totalRead, tcp.Port)
	}
	log.Printf("[Listener: %s][Request: %d][%s]: Sending validation result: %s.", tcp.ListenerID, tcp.requestID, PayloadValidation, msg)
	tcp.sendMessageWithDeadline(msg, PayloadValidation, false)
}

// doEcho echoes the data it reads back to the client in responses of the configured echo size, with the configured delay.
func (tcp *TCPConnectionHandler) doEcho() {
	log.Printf("[Listener: %s][Request: %d][%s]: Will echo response of size [%d] with response delay [%s] on port %d\n",
		tcp.ListenerID, tcp.requestID, Echo, tcp.EchoResponseSize, tcp.EchoResponseDelayD, tcp.Port)

	tcp.status.update(func(s *ConnectionStatus) { s.TotalBytesRead = 0 })
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
				tcp.ListenerID, tcp.requestID, Echo, readSize, tcp.Port, tcp.status.bytesRead())
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

// echoBack sends the write buffer to the client after the echo response delay, if any.
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

// doResponsePayload sends the preconfigured response payloads in turn, with the response delay, optionally after reading, and keeps the connection open for its remaining life if configured.
func (tcp *TCPConnectionHandler) doResponsePayload() {
	payloadCount := len(tcp.ResponsePayloads)
	if payloadCount == 0 {
		log.Printf("[Listener: %s][Request: %d][%s]: No response payloads configured, nothing to send on port [%d]",
			tcp.ListenerID, tcp.requestID, Response, tcp.Port)
		return
	}
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
					tcp.ListenerID, tcp.requestID, Response, readSize, tcp.Port, tcp.status.bytesRead())
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

// doStream sends the configured count of random payload chunks to the client, with the chunk delay between them.
func (tcp *TCPConnectionHandler) doStream() {
	log.Printf("[Listener: %s][Request: %d][%s]: Streaming [%d] chunks of size [%d] with delay [%s] for a duration of [%s] to serve total payload of [%d] on port %d\n",
		tcp.ListenerID, tcp.requestID, Stream, tcp.StreamChunkCount, tcp.StreamChunkSizeV, tcp.StreamChunkDelayD, tcp.StreamDurationD, tcp.StreamPayloadSizeV, tcp.Port)
	tcp.conn.SetWriteDeadline(time.Time{})
	tcp.writeBufferSize = tcp.StreamChunkSizeV
	tcp.resetWriteBuffer()
	payload := types.GenerateRandomPayload(tcp.StreamChunkSizeV)
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
		if !tcp.sendDataToClient(payload, Stream) {
			log.Printf("[Listener: %s][Request: %d][%s]: Ending stream as failed to send data on port [%d]",
				tcp.ListenerID, tcp.requestID, Stream, tcp.Port)
			return
		}
	}
}

// doConversation exchanges messages with the client after the hello, until the client says goodbye or the connection closes.
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

// doHello waits for the client's hello message and replies with hello.
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

// processClientMessage acknowledges a client message of the form BEGIN/text/END with ACK/text/END, and replies ERROR to any other.
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

// read reads the available bytes from the client into the read buffer, and tells if it succeeded with the size read.
func (tcp *TCPConnectionHandler) read(whatFor string) (bool, int) {
	return tcp.readOrScan(false, whatFor)
}

// scan reads the next line of text from the client, and gives the text read (the previous text if the read failed).
func (tcp *TCPConnectionHandler) scan(whatFor string) string {
	tcp.readOrScan(true, whatFor)
	return tcp.readText
}

// readMessage reads a line of text from the client as a conversation message.
func (tcp *TCPConnectionHandler) readMessage() string {
	tcp.resetReadBuffer()
	return tcp.scan(Conversation)
}

// readOrScan reads bytes or scans a line from the client under the read deadlines, and updates the status with the bytes read or the way the connection ended.
func (tcp *TCPConnectionHandler) readOrScan(scan bool, whatFor string) (bool, int) {
	if tcp.isClosingOrClosed() {
		log.Printf("[Listener: %s][Request: %d][%s]: ReadOrScan called on a closing connection on port [%d]", tcp.ListenerID, tcp.requestID, whatFor, tcp.Port)
		return false, 0
	}
	tcp.updateReadDeadline()
	readSize := 0
	var err error
	tcp.status.update(func(s *ConnectionStatus) { s.TotalReads++ })
	if scan {
		if tcp.scanner.Scan() {
			tcp.readText = tcp.scanner.Text()
			readSize = len(tcp.readText)
		} else if err = tcp.scanner.Err(); err == nil {
			// The scanner is out of text, which is the client having closed the connection.
			err = io.EOF
		}
	} else {
		readSize, err = tcp.reader.Read(tcp.readBuffer)
	}
	switch err {
	case nil:
		now := time.Now().UTC().String()
		tcp.status.update(func(s *ConnectionStatus) {
			if s.FirstByteInAt == "" {
				s.FirstByteInAt = now
			}
			s.LastByteInAt = now
			s.TotalBytesRead += readSize
		})
		return true, readSize
	case io.EOF:
		log.Printf("[Listener: %s][Request: %d][%s]: Connection closed by client on port [%d]",
			tcp.ListenerID, tcp.requestID, whatFor, tcp.Port)
		tcp.closing.Store(true)
		tcp.status.update(func(s *ConnectionStatus) { s.ClientClosed = true })
		return false, 0
	default:
		tcp.processConnectionError(err, whatFor)
		return false, 0
	}
}

// send sends the write buffer to the client.
func (tcp *TCPConnectionHandler) send(whatFor string) bool {
	return tcp.sendDataToClient(tcp.writeBuffer, whatFor)
}

// sendMessage sends a text message to the client.
func (tcp *TCPConnectionHandler) sendMessage(message, whatFor string) {
	tcp.sendMessageWithDeadline(message, whatFor, false)
}

// sendMessageWithDeadline sends a message prefixed with the host label, using the connection's write deadline only if asked to.
func (tcp *TCPConnectionHandler) sendMessageWithDeadline(message, whatFor string, useConnDeadline bool) {
	message = fmt.Sprintf("[%s]%s", global.Self.HostLabel, message)
	if tcp.sendDataToClientWithDeadline([]byte(message), whatFor, useConnDeadline) {
		log.Printf("[Listener: %s][Request: %d][%s]: Sent {%s} on port [%d]",
			tcp.ListenerID, tcp.requestID, whatFor, message, tcp.Port)
	} else {
		log.Printf("[Listener: %s][Request: %d][%s]: Error sending {%s} on port [%d]",
			tcp.ListenerID, tcp.requestID, whatFor, message, tcp.Port)
	}
}

// sendDataToClient sends the data to the client under the connection's write deadline.
func (tcp *TCPConnectionHandler) sendDataToClient(data []byte, whatFor string) bool {
	return tcp.sendDataToClientWithDeadline(data, whatFor, true)
}

// sendDataToClientWithDeadline writes all of the data to the client and records the bytes and writes in the status, failing if the connection is closing or the write fails.
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
		tcp.status.update(func(s *ConnectionStatus) { s.TotalWrites++ })
		if len, err := tcp.conn.Write(data); err != nil {
			tcp.status.update(func(s *ConnectionStatus) { s.WriteErrors++ })
			log.Printf("[Listener: %s][Request: %d][%s]: Error sending data of length %d: %s",
				tcp.ListenerID, tcp.requestID, whatFor, dataLength, err.Error())
			return false
		} else {
			now := time.Now().UTC().String()
			tcp.status.update(func(s *ConnectionStatus) {
				if s.FirstByteOutAt == "" {
					s.FirstByteOutAt = now
				}
				s.LastByteOutAt = now
				s.TotalBytesSent += len
			})
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

// updateReadDeadline sets the connection's read deadline from the connection life, read and idle timeouts.
func (tcp *TCPConnectionHandler) updateReadDeadline() {
	util.UpdateReadDeadline(tcp.conn, tcp.status.ConnStartTime, tcp.ConnectionLifeD, tcp.ReadTimeoutD, tcp.ConnIdleTimeoutD)
}

// updateWriteDeadline sets the connection's write deadline from the connection life, write and idle timeouts.
func (tcp *TCPConnectionHandler) updateWriteDeadline() {
	util.UpdateWriteDeadline(tcp.conn, tcp.status.ConnStartTime, tcp.ConnectionLifeD, tcp.WriteTimeoutD, tcp.ConnIdleTimeoutD)
}

// copyInputHeadToOutput copies the head of the read buffer into a range of the write buffer.
func (tcp *TCPConnectionHandler) copyInputHeadToOutput(inHead, outFrom, outTo int) {
	util.CopyInputHeadToOutput(tcp.writeBuffer, tcp.readBuffer, inHead, outFrom, outTo)
}

// copyInputTailToOutput copies the tail of the read buffer into a range of the write buffer.
func (tcp *TCPConnectionHandler) copyInputTailToOutput(tail, outFrom, outTo int) {
	util.CopyInputTailToOutput(tcp.writeBuffer, tcp.readBuffer, tail, outFrom, outTo)
}
