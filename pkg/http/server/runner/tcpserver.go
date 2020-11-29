package runner

import (
  "bufio"
  "fmt"
  "goto/pkg/http/server/listeners"
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

type TCPHandler struct {
  listener           *listeners.Listener
  requestID          int
  listenerID         string
  port               int
  conn               *net.TCPConn
  reader             *bufio.Reader
  scanner            *bufio.Scanner
  readBuffer         []byte
  writeBuffer        []byte
  readText           string
  echo               bool
  stream             bool
  conversation       bool
  readTimeout        time.Duration
  writeTimeout       time.Duration
  connIdleTimeout    time.Duration
  connectionLife     time.Duration
  responseDelay      time.Duration
  responsePacketSize int
  streamPayloadSize  int
  streamChunkSize    int
  streamChunkCount   int
  streamChunkDelay   time.Duration
  streamDuration     time.Duration
  connStartTime      time.Time
  totalBytesRead     int
  closing            bool
  closed             bool
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
      tcp.echo = l.Echo
      tcp.stream = l.Stream
      tcp.conversation = l.Conversation
      tcp.readTimeout = l.ReadTimeoutD
      tcp.writeTimeout = l.WriteTimeoutD
      tcp.connIdleTimeout = l.ConnIdleTimeoutD
      tcp.connectionLife = l.ConnectionLifeD
      tcp.responseDelay = l.ResponseDelayD
      tcp.responsePacketSize = l.EchoPacketSize
      tcp.streamPayloadSize = l.StreamPayloadSizeV
      tcp.streamChunkSize = l.StreamChunkSizeV
      tcp.streamChunkCount = l.StreamChunkCount
      tcp.streamChunkDelay = l.StreamChunkDelayD
      tcp.streamDuration = l.StreamDurationD
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
  tcp.readBuffer = make([]byte, tcp.responsePacketSize)
  tcp.readText = ""
}

func (tcp *TCPHandler) resetWriteBuffer() {
  tcp.writeBuffer = make([]byte, tcp.responsePacketSize)
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
  log.Printf("[Listener: %s][Request: %d]: Processing new request on port [%d] - {echo=%t, stream=%t connectionLife=%d}",
    tcp.listenerID, tcp.requestID, tcp.port, tcp.echo, tcp.stream, tcp.connectionLife)

  if tcp.stream {
    tcp.doStream()
  } else if tcp.echo {
    tcp.doEcho()
  } else if tcp.conversation {
    tcp.doConversation()
  } else {
    if tcp.connectionLife > 0 {
      select {
      case <-time.After(tcp.connectionLife):
        log.Printf("[Listener: %s][Request: %d]: Max connection life [%s] reached. Closing connection on port [%d]",
          tcp.listenerID, tcp.requestID, tcp.connectionLife, tcp.port)
        tcp.closing = true
        tcp.sendMessage(GoodByeMessage)
      }
    } else {
      log.Printf("[Listener: %s][Request: %d]: Waiting for a byte before closing port [%d]", tcp.listenerID, tcp.requestID, tcp.port)
      tcp.conn.SetReadDeadline(time.Time{})
      len, err := tcp.reader.Read(make([]byte, 1))
      switch err {
      case nil:
        log.Printf("[Listener: %s][Request: %d]: Received %d bytes, closing port [%d]", tcp.listenerID, tcp.requestID, len, tcp.port)
        tcp.closing = true
        tcp.sendMessage(GoodByeMessage)
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

func (tcp *TCPHandler) updateReadDeadline() {
  tcp.conn.SetReadDeadline(util.GetConnectionReadTimeout(tcp.connStartTime, tcp.connectionLife, tcp.readTimeout, tcp.connIdleTimeout))
}

func (tcp *TCPHandler) updateWriteDeadline() {
  tcp.conn.SetWriteDeadline(time.Now().Add(tcp.writeTimeout))
}

func (tcp *TCPHandler) sendMessage(message string) {
  if tcp.sendDataToClient([]byte(message), Conversation) {
    log.Printf("[Listener: %s][Request: %d][%s]: Sent %s on port [%d]",
      tcp.listenerID, tcp.requestID, Conversation, message, tcp.port)
  } else {
    log.Printf("[Listener: %s][Request: %d][%s]: Error sending %s on port [%d]",
      tcp.listenerID, tcp.requestID, Conversation, message, tcp.port)
  }
}

func (tcp *TCPHandler) sendDataToClient(data []byte, whatFor string) bool {
  if tcp.isClosingOrClosed() {
    log.Printf("[Listener: %s][Request: %d][%s]: Send called on a closing connection", tcp.listenerID, tcp.requestID, whatFor)
    return false
  }
  tcp.updateWriteDeadline()
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

func (tcp *TCPHandler) send(whatFor string) bool {
  return tcp.sendDataToClient(tcp.writeBuffer, whatFor)
}

func (tcp *TCPHandler) copyInputHeadToOutput(inHead, outFrom, outTo int) {
  copy(tcp.writeBuffer[outFrom:outTo], tcp.readBuffer[:inHead])
}

func (tcp *TCPHandler) copyInputTailToOutput(tail, outFrom, outTo int) {
  copy(tcp.writeBuffer[outFrom:outTo], tcp.readBuffer[tail:])
}

func (tcp *TCPHandler) read(whatFor string) (bool, int) {
  return tcp.readOrScan(false, whatFor)
}

func (tcp *TCPHandler) scan(whatFor string) string {
  tcp.readOrScan(true, whatFor)
  return tcp.readText
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
      balanceLife := util.GetConnectionRemainingLife(tcp.connStartTime, tcp.connectionLife, tcp.readTimeout, tcp.connIdleTimeout)
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

func (tcp *TCPHandler) readMessage() string {
  tcp.resetReadBuffer()
  return tcp.scan(Conversation)
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
      tcp.sendMessage(HelloMessage)
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
      tcp.sendMessage(fmt.Sprintf("ACK/%s/END", parts[1]))
      return
    }
  }
  log.Printf("[Listener: %s][Request: %d][%s]: Malformed client message [%s] on port [%d].",
    tcp.listenerID, tcp.requestID, Conversation, message, tcp.port)
  tcp.sendMessage("ERROR")
}

func (tcp *TCPHandler) doConversation() {
  log.Printf("[Listener: %s][Request: %d][%s]: Starting conversation with client with delay [%s], read timeout [%s], write timeout [%s], for total connection life of [%s] on port %d\n",
    tcp.listenerID, tcp.requestID, Conversation, tcp.responseDelay, tcp.readTimeout, tcp.writeTimeout, tcp.connectionLife, tcp.port)
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
  tcp.sendMessage(GoodByeMessage)
}

func (tcp *TCPHandler) echoBack(leftover int) {
  tcp.copyInputHeadToOutput(tcp.responsePacketSize-leftover, leftover, len(tcp.writeBuffer))
  if tcp.responseDelay > 0 {
    log.Printf("[Listener: %s][Request: %d]: Delaying response by [%s] before echo on port [%d]",
      tcp.listenerID, tcp.requestID, tcp.responseDelay, tcp.port)
    time.Sleep(tcp.responseDelay)
  }
  tcp.send(Echo)
}

func (tcp *TCPHandler) doEcho() {
  log.Printf("[Listener: %s][Request: %d][%s]: Will echo packets of size [%d] with packet delay [%s], read timeout [%s], write timeout [%s], for total connection life of [%s] on port %d\n",
    tcp.listenerID, tcp.requestID, Echo, tcp.responsePacketSize, tcp.responseDelay, tcp.readTimeout, tcp.writeTimeout, tcp.connectionLife, tcp.port)

  tcp.totalBytesRead = 0
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
      if readSize+leftover >= tcp.responsePacketSize {
        tcp.echoBack(leftover)
        if readSize+leftover > tcp.responsePacketSize {
          leftover = readSize - (tcp.responsePacketSize - leftover)
          tcp.copyInputTailToOutput(readSize-leftover, 0, leftover)
        } else {
          leftover = 0
        }
      } else {
        tcp.copyInputHeadToOutput(readSize, leftover, leftover+readSize)
        leftover += readSize
        log.Printf("[Listener: %s][Request: %d][%s]: Total buffered data of length [%d] not enough to match echo packet size [%d], not echoing yet on port [%d].",
          tcp.listenerID, tcp.requestID, Echo, leftover, tcp.responsePacketSize, tcp.port)
      }
    } else {
      log.Printf("[Listener: %s][Request: %d][%s]: Stopping echo on port [%d]",
        tcp.listenerID, tcp.requestID, Echo, tcp.port)
      return
    }
  }
}

func (tcp *TCPHandler) doStream() {
  log.Printf("[Listener: %s][Request: %d][%s]: Streaming [%d] chunks of size [%d] with delay [%s] for a duration of [%s] to serve total payload of [%d] on port %d\n",
    tcp.listenerID, tcp.requestID, Stream, tcp.streamChunkCount, tcp.streamChunkSize, tcp.streamChunkDelay, tcp.streamDuration, tcp.streamPayloadSize, tcp.port)
  tcp.conn.SetWriteDeadline(time.Time{})

  payload := []byte(util.GenerateRandomString(tcp.streamChunkSize))
  for i := 0; i < tcp.streamChunkCount; i++ {
    if tcp.isClosingOrClosed() {
      log.Printf("[Listener: %s][Request: %d][%s]: Ending stream as the connection is closing on port [%d]",
        tcp.listenerID, tcp.requestID, Stream, tcp.port)
      return
    }
    time.Sleep(tcp.streamChunkDelay)
    if tcp.connectionLife > 0 && util.GetConnectionRemainingLife(tcp.connStartTime, tcp.connectionLife, 0, 0) <= 0 {
      log.Printf("[Listener: %s][Request: %d][%s]: Max connection life [%s] reached. Stopping stream on port [%d]",
        tcp.listenerID, tcp.requestID, Stream, tcp.connectionLife, tcp.port)
      break
    }
    tcp.sendDataToClient(payload, Stream)
  }
}

func isConnectionCloseError(err error) bool {
  return strings.Contains(err.Error(), "closed network connection")
}

func isConnectionTimeoutError(err error) bool {
  return strings.Contains(err.Error(), "timeout")
}
