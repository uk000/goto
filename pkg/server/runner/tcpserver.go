package runner

import (
  "bufio"
  "bytes"
  "fmt"
  "goto/pkg/server/listeners"
  "goto/pkg/util"
  "io"
  "log"
  "net"
  "strings"
  "sync"
  "time"
)

var (
  requestCounter    int
  activeConnections map[string]map[int]*TCPHandler = map[string]map[int]*TCPHandler{}
  lock              sync.RWMutex
)

const HelloMessage string = "HELLO"
const GoodByeMessage string = "GOODBYE"
const Conversation string = "Conversation"
const Echo string = "Echo"
const Stream string = "Stream"
const PayloadValidation string = "PayloadValidation"
const Silent string = "Silent"

type TCPHandler struct {
  listener               *listeners.Listener
  requestID              int
  listenerID             string
  port                   int
  conn                   *net.TCPConn
  reader                 *bufio.Reader
  scanner                *bufio.Scanner
  readBuffer             []byte
  writeBuffer            []byte
  readText               string
  echo                   bool
  stream                 bool
  conversation           bool
  readTimeout            time.Duration
  writeTimeout           time.Duration
  connIdleTimeout        time.Duration
  connectionLife         time.Duration
  echoResponseDelay      time.Duration
  echoResponseSize       int
  expectedPayloadLength  int
  expectedPayload        []byte
  validatePayloadLength  bool
  validatePayloadContent bool
  streamPayloadSize      int
  streamChunkSize        int
  streamChunkCount       int
  streamChunkDelay       time.Duration
  streamDuration         time.Duration
  connStartTime          time.Time
  readBufferSize         int
  writeBufferSize        int
  totalBytesRead         int
  closing                bool
  closed                 bool
}

func StartTCPServer(l *listeners.Listener) {
  go serveTCPRequests(l)
}

func serveTCPRequests(l *listeners.Listener) {
  l.Lock.RLock()
  listener := l.Listener
  listenerID := l.ListenerID
  l.Lock.RUnlock()
  if listener == nil {
    log.Printf("Listener [%s] not open for business", listenerID)
    return
  }
  stopped := false
  activeConnections[listenerID] = map[int]*TCPHandler{}
  for !stopped {
    if conn, err := listener.Accept(); err == nil {
      tcp := &TCPHandler{}
      lock.Lock()
      requestCounter++
      tcp.requestID = requestCounter
      tcp.conn = conn.(*net.TCPConn)
      activeConnections[listenerID][requestCounter] = tcp
      lock.Unlock()

      l.Lock.RLock()
      tcp.listener = l
      tcp.listenerID = listenerID
      tcp.port = l.Port
      tcp.reader = bufio.NewReader(tcp.conn)
      tcp.scanner = bufio.NewScanner(tcp.reader)
      tcp.echo = l.TCP.Echo
      tcp.stream = l.TCP.Stream
      tcp.conversation = l.TCP.Conversation
      tcp.readTimeout = l.TCP.ReadTimeoutD
      tcp.writeTimeout = l.TCP.WriteTimeoutD
      tcp.connIdleTimeout = l.TCP.ConnIdleTimeoutD
      tcp.connectionLife = l.TCP.ConnectionLifeD
      tcp.echoResponseDelay = l.TCP.EchoResponseDelayD
      tcp.echoResponseSize = l.TCP.EchoResponseSize
      tcp.validatePayloadContent = l.TCP.ValidatePayloadContent
      tcp.validatePayloadLength = l.TCP.ValidatePayloadLength
      tcp.expectedPayload = l.TCP.ExpectedPayload
      tcp.expectedPayloadLength = l.TCP.ExpectedPayloadLength
      tcp.streamPayloadSize = l.TCP.StreamPayloadSizeV
      tcp.streamChunkSize = l.TCP.StreamChunkSizeV
      tcp.streamChunkCount = l.TCP.StreamChunkCount
      tcp.streamChunkDelay = l.TCP.StreamChunkDelayD
      tcp.streamDuration = l.TCP.StreamDurationD
      tcp.readBufferSize = 100
      tcp.writeBufferSize = 100
      tcp.resetReadBuffer()
      tcp.resetWriteBuffer()
      tcp.connStartTime = time.Now()
      l.Lock.RUnlock()

      go tcp.processRequest()
    } else if !isConnectionCloseError(err) {
      log.Println(err)
      continue
    } else {
      stopped = true
    }
  }
  if l.Restarted {
    log.Printf("[Listener: %s] has been restarted. Stopping to serve requests on old listener.", l.ListenerID)
  } else {
    log.Printf("[Listener: %s] has been closed. Stopping to serve requests.", l.ListenerID)
  }
  log.Printf("[Listener: %s] Force closing active client connections for closed listener.", l.ListenerID)
  lock.Lock()
  if activeConnections[l.ListenerID] != nil {
    for _, tcp := range activeConnections[l.ListenerID] {
      if !tcp.closed && tcp.conn != nil {
        tcp.conn.Close()
        tcp.closing = true
        tcp.closed = true
      }
    }
    delete(activeConnections, l.ListenerID)
  }
  lock.Unlock()
}

func (tcp *TCPHandler) resetReadBuffer() {
  tcp.readBuffer = make([]byte, tcp.readBufferSize)
  tcp.readText = ""
}

func (tcp *TCPHandler) resetWriteBuffer() {
  tcp.writeBuffer = make([]byte, tcp.writeBufferSize)
}

func (tcp *TCPHandler) close() {
  if tcp.conn != nil {
    tcp.conn.Close()
  }
  tcp.closing = true
  tcp.closed = true
  lock.Lock()
  delete(activeConnections[tcp.listenerID], tcp.requestID)
  lock.Unlock()
}

func (tcp *TCPHandler) isClosingOrClosed() bool {
  return tcp.closing || tcp.closed || !tcp.listener.Open
}

func (tcp *TCPHandler) processRequest() {
  defer tcp.close()
  log.Printf("[Listener: %s][Request: %d]: Processing new request on port [%d] - {echo=%t, stream=%t, conversation=%t, readTimeout=%s, writeTimeout=%s, connIdleTimeout=%s, connectionLife=%s}",
    tcp.listenerID, tcp.requestID, tcp.port, tcp.echo, tcp.stream, tcp.conversation, tcp.readTimeout, tcp.writeTimeout, tcp.connIdleTimeout, tcp.connectionLife)

  if tcp.stream {
    tcp.doStream()
  } else if tcp.echo {
    tcp.doEcho()
  } else if tcp.conversation {
    tcp.doConversation()
  } else if tcp.validatePayloadLength || tcp.validatePayloadLength {
    tcp.doPayloadValidation()
  } else {
    if tcp.connectionLife > 0 {
      select {
      case <-time.After(tcp.connectionLife):
        tcp.sendMessage(GoodByeMessage, Silent)
        log.Printf("[Listener: %s][Request: %d]: Max connection life [%s] reached. Closing connection on port [%d]",
          tcp.listenerID, tcp.requestID, tcp.connectionLife, tcp.port)
        tcp.closing = true
      }
    } else {
      log.Printf("[Listener: %s][Request: %d]: Waiting for a byte before closing port [%d]", tcp.listenerID, tcp.requestID, tcp.port)
      tcp.conn.SetReadDeadline(time.Time{})
      len, err := tcp.reader.Read(make([]byte, 1))
      switch err {
      case nil:
        tcp.sendMessage(GoodByeMessage, Silent)
        log.Printf("[Listener: %s][Request: %d]: Received %d bytes, closing port [%d]", tcp.listenerID, tcp.requestID, len, tcp.port)
        tcp.closing = true
      case io.EOF:
        log.Printf("[Listener: %s][Request: %d]: Connection closed by client on port [%d]", tcp.listenerID, tcp.requestID, tcp.port)
        tcp.closing = true
      default:
        if isConnectionCloseError(err) {
          log.Printf("[Listener: %s][Request: %d]: Connection closed by server on port [%d]", tcp.listenerID, tcp.requestID, tcp.port)
        } else {
          log.Printf("[Listener: %s][Request: %d]: Error reading TCP data on port [%d]: %s",
            tcp.listenerID, tcp.requestID, tcp.port, err.Error())
        }
        tcp.closing = true
      }
    }
  }
  if !tcp.listener.Open {
    log.Printf("[Listener: %s][Request: %d]: Listener is closed for port [%d]", tcp.listenerID, tcp.requestID, tcp.port)
  }
}

func (tcp *TCPHandler) doPayloadValidation() {
  if tcp.connectionLife <= 0 {
    tcp.connectionLife = 30 * time.Second
  }
  if tcp.validatePayloadContent {
    log.Printf("[Listener: %s][Request: %d][%s]: Will validate payload content of size [%d] over total connection life of [%s] with read timeout [%s] and idle timeout [%s] on port %d\n",
      tcp.listenerID, tcp.requestID, PayloadValidation, tcp.expectedPayloadLength, tcp.connectionLife, tcp.readTimeout, tcp.connIdleTimeout, tcp.port)
  } else {
    log.Printf("[Listener: %s][Request: %d][%s]: Will validate payload length [%d] over total connection life of [%s] with read timeout [%s] and idle timeout [%s] on port %d\n",
      tcp.listenerID, tcp.requestID, PayloadValidation, tcp.expectedPayloadLength, tcp.connectionLife, tcp.readTimeout, tcp.connIdleTimeout, tcp.port)
  }
  tcp.totalBytesRead = 0
  tcp.resetReadBuffer()
  tcp.resetWriteBuffer()
  var receivedPayload []byte
  if tcp.validatePayloadContent {
    receivedPayload = make([]byte, tcp.expectedPayloadLength)
  }
  isPayloadReady := false
  checkForExcess := false
  isPayloadExcess := false
  for !isPayloadReady || checkForExcess {
    if tcp.isClosingOrClosed() {
      log.Printf("[Listener: %s][Request: %d][%s]: Ending payload validation as the connection is closing on port [%d]",
        tcp.listenerID, tcp.requestID, PayloadValidation, tcp.port)
      break
    }
    prevBytesRead := tcp.totalBytesRead
    if success, readSize := tcp.read(PayloadValidation); success {
      if tcp.validatePayloadContent && tcp.totalBytesRead <= tcp.expectedPayloadLength {
        copy(receivedPayload[prevBytesRead:prevBytesRead+readSize], tcp.readBuffer[:readSize])
      }
      log.Printf("[Listener: %s][Request: %d][%s]: Read data of length [%d] for payload validation on port [%d]. Total read so far [%d].",
        tcp.listenerID, tcp.requestID, PayloadValidation, readSize, tcp.port, tcp.totalBytesRead)
      if tcp.totalBytesRead == tcp.expectedPayloadLength {
        log.Printf("[Listener: %s][Request: %d][%s]: Toal payload size matches the expected length [%d] on port [%d]. Waiting for any excess byte to show up.",
          tcp.listenerID, tcp.requestID, PayloadValidation, tcp.totalBytesRead, tcp.port)
        isPayloadReady = true
        checkForExcess = true
      } else if tcp.totalBytesRead < tcp.expectedPayloadLength {
        log.Printf("[Listener: %s][Request: %d][%s]: Total received data of length [%d] not enough to match expected length [%d], waiting for more data on port [%d].",
          tcp.listenerID, tcp.requestID, PayloadValidation, tcp.totalBytesRead, tcp.expectedPayloadLength, tcp.port)
      } else {
        log.Printf("[Listener: %s][Request: %d][%s]: Total received data of length [%d] exceeded expected length [%d] on port [%d].",
          tcp.listenerID, tcp.requestID, PayloadValidation, tcp.totalBytesRead, tcp.expectedPayloadLength, tcp.port)
        isPayloadExcess = true
        isPayloadReady = true
      }
    }
  }
  msg := ""
  if !isPayloadReady {
    msg = fmt.Sprintf("[ERROR:TIMEOUT] - Timed out before receiving payload of expected length [%d] on port [%d]", tcp.expectedPayloadLength, tcp.port)
  } else if isPayloadExcess {
    msg = fmt.Sprintf("[ERROR:EXCEEDED] - Payload length [%d] exceeded expected length [%d] on port [%d]", tcp.totalBytesRead, tcp.expectedPayloadLength, tcp.port)
  } else if tcp.validatePayloadContent &&
    !(bytes.Equal(receivedPayload[:tcp.expectedPayloadLength], tcp.expectedPayload) && tcp.readBuffer[tcp.expectedPayloadLength] == 0) {
    msg = fmt.Sprintf("[ERROR:CONTENT] - Payload content of length [%d] didn't match expected payload of length [%d] on port [%d]", tcp.totalBytesRead, tcp.expectedPayloadLength, tcp.port)
  } else {
    msg = fmt.Sprintf("[SUCCESS]: Received pyload matches expected payload of length [%d] on port [%d]", tcp.totalBytesRead, tcp.port)
  }
  log.Printf("[Listener: %s][Request: %d][%s]: Sending validation result: %s.", tcp.listenerID, tcp.requestID, PayloadValidation, msg)
  tcp.sendMessageWithDeadline(msg, PayloadValidation, false)
}

func (tcp *TCPHandler) doEcho() {
  log.Printf("[Listener: %s][Request: %d][%s]: Will echo response of size [%d] with response delay [%s] on port %d\n",
    tcp.listenerID, tcp.requestID, Echo, tcp.echoResponseSize, tcp.echoResponseDelay, tcp.port)

  tcp.totalBytesRead = 0
  tcp.writeBufferSize = tcp.echoResponseSize
  tcp.resetWriteBuffer()
  leftover := 0
  for {
    if tcp.isClosingOrClosed() {
      log.Printf("[Listener: %s][Request: %d][%s]: Ending echo as the connection is closing on port [%d]",
        tcp.listenerID, tcp.requestID, Echo, tcp.port)
      return
    }
    if success, readSize := tcp.read(Echo); success {
      log.Printf("[Listener: %s][Request: %d][%s]: Read data of length [%d] for echo on port [%d]. Total read so far [%d].",
        tcp.listenerID, tcp.requestID, Echo, readSize, tcp.port, tcp.totalBytesRead)
      if readSize+leftover >= tcp.echoResponseSize {
        tcp.echoBack(leftover)
        if readSize+leftover > tcp.echoResponseSize {
          leftover = readSize - (tcp.echoResponseSize - leftover)
          tcp.copyInputTailToOutput(readSize-leftover, 0, leftover)
        } else {
          leftover = 0
        }
      } else {
        tcp.copyInputHeadToOutput(readSize, leftover, leftover+readSize)
        leftover += readSize
        log.Printf("[Listener: %s][Request: %d][%s]: Total buffered data of length [%d] not enough to match echo response size [%d], not echoing yet on port [%d].",
          tcp.listenerID, tcp.requestID, Echo, leftover, tcp.echoResponseSize, tcp.port)
      }
    } else {
      log.Printf("[Listener: %s][Request: %d][%s]: Stopping echo on port [%d]",
        tcp.listenerID, tcp.requestID, Echo, tcp.port)
      return
    }
  }
}

func (tcp *TCPHandler) echoBack(leftover int) {
  tcp.copyInputHeadToOutput(tcp.echoResponseSize-leftover, leftover, len(tcp.writeBuffer))
  if tcp.echoResponseDelay > 0 {
    log.Printf("[Listener: %s][Request: %d]: Delaying response by [%s] before echo on port [%d]",
      tcp.listenerID, tcp.requestID, tcp.echoResponseDelay, tcp.port)
    time.Sleep(tcp.echoResponseDelay)
  }
  tcp.send(Echo)
}

func (tcp *TCPHandler) doStream() {
  log.Printf("[Listener: %s][Request: %d][%s]: Streaming [%d] chunks of size [%d] with delay [%s] for a duration of [%s] to serve total payload of [%d] on port %d\n",
    tcp.listenerID, tcp.requestID, Stream, tcp.streamChunkCount, tcp.streamChunkSize, tcp.streamChunkDelay, tcp.streamDuration, tcp.streamPayloadSize, tcp.port)
  tcp.conn.SetWriteDeadline(time.Time{})
  tcp.writeBufferSize = tcp.streamChunkSize
  tcp.resetWriteBuffer()
  payload := []byte(util.GenerateRandomString(tcp.streamChunkSize))
  for i := 0; i < tcp.streamChunkCount; i++ {
    if tcp.isClosingOrClosed() {
      log.Printf("[Listener: %s][Request: %d][%s]: Ending stream as the connection is closing on port [%d]",
        tcp.listenerID, tcp.requestID, Stream, tcp.port)
      return
    }
    time.Sleep(tcp.streamChunkDelay)
    if tcp.connectionLife > 0 && util.GetConnectionRemainingLife(tcp.connStartTime, time.Now(), tcp.connectionLife, 0, 0) <= 0 {
      log.Printf("[Listener: %s][Request: %d][%s]: Max connection life [%s] reached. Stopping stream on port [%d]",
        tcp.listenerID, tcp.requestID, Stream, tcp.connectionLife, tcp.port)
      break
    }
    tcp.sendDataToClient(payload, Stream)
  }
}

func (tcp *TCPHandler) doConversation() {
  log.Printf("[Listener: %s][Request: %d][%s]: Starting conversation with client with read timeout [%s], write timeout [%s], for total connection life of [%s] on port %d\n",
    tcp.listenerID, tcp.requestID, Conversation, tcp.readTimeout, tcp.writeTimeout, tcp.connectionLife, tcp.port)
  tcp.resetWriteBuffer()
  tcp.doHello()
  for {
    if tcp.isClosingOrClosed() {
      log.Printf("[Listener: %s][Request: %d][%s]: Ending conversation as the connection is closing on port [%d]", tcp.listenerID, tcp.requestID, Conversation, tcp.port)
      return
    }
    if message := tcp.readMessage(); message != "" {
      log.Printf("[Listener: %s][Request: %d][%s]: Received message [%s] from client on port %d\n",
        tcp.listenerID, tcp.requestID, Conversation, message, tcp.port)
      if strings.Contains(strings.ToUpper(message), GoodByeMessage) {
        break
      }
      tcp.processClientMessage(message)
    } else if !tcp.isClosingOrClosed() {
      log.Printf("[Listener: %s][Request: %d][%s]: Received empty message from client on port %d\n",
        tcp.listenerID, tcp.requestID, Conversation, tcp.port)
    }
  }
  tcp.sendMessage(GoodByeMessage, Conversation)
}

func (tcp *TCPHandler) doHello() {
  if tcp.isClosingOrClosed() {
    log.Printf("[Listener: %s][Request: %d][Hello]: doHello called on a closing connection on port [%d]", tcp.listenerID, tcp.requestID, tcp.port)
    return
  }
  log.Printf("[Listener: %s][Request: %d][Hello]: Waiting for client Hello on port [%d].",
    tcp.listenerID, tcp.requestID, tcp.port)
  if message := tcp.readMessage(); message != "" {
    log.Printf("[Listener: %s][Request: %d][Hello]: Client said [%s] on port [%d].",
      tcp.listenerID, tcp.requestID, message, tcp.port)
    if strings.Contains(strings.ToUpper(message), HelloMessage) {
      log.Printf("[Listener: %s][Request: %d][Hello]: Sending %s back to client on port [%d].",
        tcp.listenerID, tcp.requestID, HelloMessage, tcp.port)
      tcp.sendMessage(HelloMessage, Conversation)
    }
  }
}

func (tcp *TCPHandler) processClientMessage(message string) {
  parts := strings.Split(message, "/")
  if len(parts) == 3 {
    parts[0] = strings.Trim(parts[0], " \n\r")
    parts[2] = strings.Trim(parts[2], " \n\r")
    if strings.Contains(parts[0], "BEGIN") && strings.Contains(parts[2], "END") {
      log.Printf("[Listener: %s][Request: %d][%s]: Client message was [%s] on port [%d].",
        tcp.listenerID, tcp.requestID, Conversation, parts[1], tcp.port)
      tcp.sendMessage(fmt.Sprintf("ACK/%s/END", parts[1]), Conversation)
      return
    }
  }
  log.Printf("[Listener: %s][Request: %d][%s]: Malformed client message [%s] on port [%d].",
    tcp.listenerID, tcp.requestID, Conversation, message, tcp.port)
  tcp.sendMessage("ERROR", Conversation)
}

func (tcp *TCPHandler) read(whatFor string) (bool, int) {
  return tcp.readOrScan(false, whatFor)
}

func (tcp *TCPHandler) scan(whatFor string) string {
  tcp.readOrScan(true, whatFor)
  return tcp.readText
}

func (tcp *TCPHandler) readMessage() string {
  tcp.resetReadBuffer()
  return tcp.scan(Conversation)
}

func (tcp *TCPHandler) readOrScan(scan bool, whatFor string) (bool, int) {
  if tcp.isClosingOrClosed() {
    log.Printf("[Listener: %s][Request: %d][%s]: ReadOrScan called on a closing connection on port [%d]", tcp.listenerID, tcp.requestID, whatFor, tcp.port)
    return false, 0
  }
  tcp.updateReadDeadline()
  readSize := 0
  var err error
  if scan && tcp.scanner.Scan() {
    tcp.readText = tcp.scanner.Text()
    readSize = len(tcp.readText)
  } else {
    readSize, err = tcp.reader.Read(tcp.readBuffer)
  }
  switch err {
  case nil:
    tcp.totalBytesRead += readSize
    return true, readSize
  case io.EOF:
    log.Printf("[Listener: %s][Request: %d][%s]: Connection closed by client on port [%d]",
      tcp.listenerID, tcp.requestID, whatFor, tcp.port)
    tcp.closing = true
    return false, 0
  default:
    tcp.closing = true
    if isConnectionCloseError(err) {
      log.Printf("[Listener: %s][Request: %d][%s]: Connection closed by server on port [%d]",
        tcp.listenerID, tcp.requestID, whatFor, tcp.port)
    } else if isConnectionTimeoutError(err) {
      balanceLife := util.GetConnectionRemainingLife(tcp.connStartTime, time.Now(), tcp.connectionLife, tcp.readTimeout, tcp.connIdleTimeout)
      if balanceLife < 0 {
        log.Printf("[Listener: %s][Request: %d][%s]: Max connection life [%s] reached. Closing connection on port [%d]",
          tcp.listenerID, tcp.requestID, whatFor, tcp.connectionLife, tcp.port)
      } else if tcp.connIdleTimeout < tcp.readTimeout {
        log.Printf("[Listener: %s][Request: %d][%s]: Connection idle timeout [%s] reached. Closing connection on port [%d]",
          tcp.listenerID, tcp.requestID, whatFor, tcp.connIdleTimeout, tcp.port)
      } else {
        log.Printf("[Listener: %s][Request: %d][%s]: Read timeout on port [%d]: %s",
          tcp.listenerID, tcp.requestID, whatFor, tcp.port, err.Error())
      }
    } else {
      log.Printf("[Listener: %s][Request: %d][%s]: Error reading TCP data on port [%d]: %s",
        tcp.listenerID, tcp.requestID, whatFor, tcp.port, err.Error())
    }
    return false, 0
  }
}

func (tcp *TCPHandler) send(whatFor string) bool {
  return tcp.sendDataToClient(tcp.writeBuffer, whatFor)
}

func (tcp *TCPHandler) sendMessage(message, whatFor string) {
  tcp.sendMessageWithDeadline(message, whatFor, false)
}

func (tcp *TCPHandler) sendMessageWithDeadline(message, whatFor string, useConnDeadline bool) {
  if tcp.sendDataToClientWithDeadline([]byte(message), whatFor, useConnDeadline) {
    log.Printf("[Listener: %s][Request: %d][%s]: Sent {%s} on port [%d]",
      tcp.listenerID, tcp.requestID, whatFor, message, tcp.port)
  } else {
    log.Printf("[Listener: %s][Request: %d][%s]: Error sending {%s} on port [%d]",
      tcp.listenerID, tcp.requestID, whatFor, message, tcp.port)
  }
}

func (tcp *TCPHandler) sendDataToClient(data []byte, whatFor string) bool {
  return tcp.sendDataToClientWithDeadline(data, whatFor, true)
}

func (tcp *TCPHandler) sendDataToClientWithDeadline(data []byte, whatFor string, useConnDeadline bool) bool {
  if (useConnDeadline && tcp.isClosingOrClosed()) || tcp.closed {
    log.Printf("[Listener: %s][Request: %d][%s]: Send called on a closing/closed connection", tcp.listenerID, tcp.requestID, whatFor)
    return false
  }
  if useConnDeadline {
    tcp.updateWriteDeadline()
  } else {
    tcp.conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
  }
  if _, err := tcp.conn.Write(data); err != nil {
    log.Printf("[Listener: %s][Request: %d][%s]: Error sending data of length %d: %s",
      tcp.listenerID, tcp.requestID, whatFor, len(data), err.Error())
    return false
  } else {
    log.Printf("[Listener: %s][Request: %d][%s]: Sent data of length %d",
      tcp.listenerID, tcp.requestID, whatFor, len(data))
    return true
  }
}

func (tcp *TCPHandler) updateReadDeadline() {
  tcp.conn.SetReadDeadline(util.GetConnectionReadWriteTimeout(tcp.connStartTime, tcp.connectionLife, tcp.readTimeout, tcp.connIdleTimeout))
}

func (tcp *TCPHandler) updateWriteDeadline() {
  tcp.conn.SetWriteDeadline(util.GetConnectionReadWriteTimeout(tcp.connStartTime, tcp.connectionLife, tcp.writeTimeout, tcp.connIdleTimeout))
}

func (tcp *TCPHandler) copyInputHeadToOutput(inHead, outFrom, outTo int) {
  copy(tcp.writeBuffer[outFrom:outTo], tcp.readBuffer[:inHead])
}

func (tcp *TCPHandler) copyInputTailToOutput(tail, outFrom, outTo int) {
  copy(tcp.writeBuffer[outFrom:outTo], tcp.readBuffer[tail:])
}

func isConnectionCloseError(err error) bool {
  return strings.Contains(err.Error(), "closed network connection")
}

func isConnectionTimeoutError(err error) bool {
  return strings.Contains(err.Error(), "timeout")
}
