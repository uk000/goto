package runner

import (
  "bufio"
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
  activeConnections map[string]map[int]net.Conn = map[string]map[int]net.Conn{}
  lock              sync.RWMutex
)

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
  activeConnections[listenerID] = map[int]net.Conn{}
  for !stopped {
    if conn, err := listener.Accept(); err == nil {
      lock.Lock()
      requestCounter++
      requestID := requestCounter
      activeConnections[listenerID][requestID] = conn
      lock.Unlock()
      go processRequest(l, conn, requestID)
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
  for _, conn := range activeConnections[l.ListenerID] {
    conn.Close()
  }
  delete(activeConnections, l.ListenerID)
  lock.Unlock()
}

func isConnectionCloseError(err error) bool {
  return strings.Contains(err.Error(), "closed network connection")
}

func isConnectionTimeoutError(err error) bool {
  return strings.Contains(err.Error(), "timeout")
}

func sendDataToClient(data []byte, conn net.Conn, requestID int, listenerID string) bool {
  if _, err := conn.Write(data); err != nil {
    log.Printf("[Listener: %s][Request: %d]: Error sending data of length %d: %s", listenerID, requestID, len(data), err.Error())
    return false
  } else {
    log.Printf("[Listener: %s][Request: %d]: Sent data of length %d", listenerID, requestID, len(data))
    return true
  }
}

func closeClientConnection(conn net.Conn, requestID int, listenerID string, port int) {
  if sendDataToClient([]byte("\nGOODBYE\n"), conn, requestID, listenerID) {
    log.Printf("[Listener: %s][Request: %d]: Sent GOODBYE on port [%d]", listenerID, requestID, port)
  } else {
    log.Printf("[Listener: %s][Request: %d]: Error sending GOODBYE on port [%d]", listenerID, requestID, port)
  }
}

func getConnectionRemainingLife(startTime time.Time, connectionLife, readTimeout, connIdleTimeout time.Duration) time.Duration {
  now := time.Now()
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

func getConnectionReadTimeout(startTime time.Time, connectionLife, readTimeout, connIdleTimeout time.Duration) time.Time {
  now := time.Now()
  return now.Add(getConnectionRemainingLife(startTime, connectionLife, readTimeout, connIdleTimeout))
}

func doStream(l *listeners.Listener, conn net.Conn, requestID int) {
  l.Lock.RLock()
  listenerID := l.ListenerID
  port := l.Port
  streamPayloadSize := l.StreamPayloadSizeV
  streamChunkSize := l.StreamChunkSizeV
  streamChunkCount := l.StreamChunkCount
  streamChunkDelay := l.StreamChunkDelayD
  streamDuration := l.StreamDurationD
  connectionLife := l.ConnectionLifeD
  l.Lock.RUnlock()

  log.Printf("[Listener: %s][Request: %d]: Streaming [%d] chunks of size [%d] with delay [%s] for a duration of [%s] to serve total payload of [%d] on port %d\n",
    listenerID, requestID, streamChunkCount, streamChunkSize, streamChunkDelay, streamDuration, streamPayloadSize, port)
  conn.SetWriteDeadline(time.Time{})

  payload := []byte(util.GenerateRandomString(streamChunkSize))
  startTime := time.Now()
  for i := 0; i < streamChunkCount; i++ {
    if !l.Open {
      log.Printf("[Listener: %s][Request: %d]: Connection closed by server on port [%d]", listenerID, requestID, port)
      break
    }
    time.Sleep(streamChunkDelay)
    if connectionLife > 0 && getConnectionRemainingLife(startTime, connectionLife, 0, 0) <= 0 {
      log.Printf("[Listener: %s][Request: %d]: Max connection life [%s] reached. Stopping stream on port [%d]", listenerID, requestID, connectionLife, port)
      break
    }
    sendDataToClient(payload, conn, requestID, listenerID)
  }
}

func doEcho(l *listeners.Listener, conn net.Conn, requestID int) {
  l.Lock.RLock()
  listenerID := l.ListenerID
  port := l.Port
  echoPacketSize := l.EchoPacketSize
  responseDelay := l.ResponseDelayD
  connectionLife := l.ConnectionLifeD
  readTimeout := l.ReadTimeoutD
  writeTimeout := l.WriteTimeoutD
  connIdleTimeout := l.ConnIdleTimeoutD
  l.Lock.RUnlock()

  log.Printf("[Listener: %s][Request: %d]: Will echo packets of size [%d] with packet delay [%s], read timeout [%s], write timeout [%s], for total connection life of [%s] on port %d\n",
    listenerID, requestID, echoPacketSize, responseDelay, readTimeout, writeTimeout, connectionLife, port)

  startTime := time.Now()
  reader := bufio.NewReader(conn)
  inputBuffer := make([]byte, echoPacketSize)
  outputBuffer := make([]byte, echoPacketSize)
  totalRead := 0
  leftover := 0
  for {
    l.Lock.RLock()
    isListenerOpen := l.Open
    l.Lock.RUnlock()
    if !isListenerOpen {
      log.Printf("[Listener: %s][Request: %d]: Connection closed by server on port [%d]", listenerID, requestID, port)
      break
    }
    readSize := 0
    var err error
    conn.SetReadDeadline(getConnectionReadTimeout(startTime, connectionLife, readTimeout, connIdleTimeout))
    readSize, err = reader.Read(inputBuffer)
    switch err {
    case nil:
      totalRead += readSize
      log.Printf("[Listener: %s][Request: %d]: Read data of length [%d] for echo on port [%d]. Total read so far [%d].",
        listenerID, requestID, readSize, port, totalRead)
      if readSize+leftover >= echoPacketSize {
        copy(outputBuffer[leftover:], inputBuffer[:echoPacketSize-leftover])
        if responseDelay > 0 {
          log.Printf("[Listener: %s][Request: %d]: Delaying response by [%s] before echo on port [%d]", listenerID, requestID, responseDelay, port)
          time.Sleep(responseDelay)
        }
        conn.SetWriteDeadline(time.Now().Add(writeTimeout))
        sendDataToClient(outputBuffer, conn, requestID, listenerID)
        outputBuffer = make([]byte, echoPacketSize)
        if readSize+leftover > echoPacketSize {
          leftover = readSize - (echoPacketSize - leftover)
          copy(outputBuffer[0:leftover], inputBuffer[readSize-leftover:])
        } else {
          leftover = 0
        }
      } else {
        copy(outputBuffer[leftover:leftover+readSize], inputBuffer[:readSize])
        leftover += readSize
        log.Printf("[Listener: %s][Request: %d]: Total buffered data of length [%d] not enough to match echo packet size [%d], not echoing yet on port [%d].",
          listenerID, requestID, leftover, echoPacketSize, port)
      }
    case io.EOF:
      log.Printf("[Listener: %s][Request: %d]: Connection closed by client on port [%d]", listenerID, requestID, port)
      return
    default:
      if isConnectionCloseError(err) {
        log.Printf("[Listener: %s][Request: %d]: Connection closed by server on port [%d]", listenerID, requestID, port)
      } else if isConnectionTimeoutError(err) {
        balanceLife := getConnectionRemainingLife(startTime, connectionLife, readTimeout, connIdleTimeout)
        if balanceLife < 0 {
          log.Printf("[Listener: %s][Request: %d]: Max connection life [%s] reached. Closing connection on port [%d]", listenerID, requestID, connectionLife, port)
        } else if connIdleTimeout < readTimeout {
          log.Printf("[Listener: %s][Request: %d]: Connection idle timeout [%s] reached. Closing connection on port [%d]", listenerID, requestID, connIdleTimeout, port)
        } else {
          log.Printf("[Listener: %s][Request: %d]: Read timeout on port [%d]: %s", listenerID, requestID, port, err.Error())
        }
      } else {
        log.Printf("[Listener: %s][Request: %d]: Error reading TCP data on port [%d]: %s", listenerID, requestID, port, err.Error())
      }
      return
    }
  }
}

func processRequest(l *listeners.Listener, conn net.Conn, requestID int) {
  l.Lock.RLock()
  listenerID := l.ListenerID
  port := l.Port
  echo := l.Echo
  connectionLife := l.ConnectionLifeD
  stream := l.Stream
  l.Lock.RUnlock()

  defer func() {
    conn.Close()
    lock.Lock()
    delete(activeConnections[listenerID], requestID)
    lock.Unlock()
  }()
  log.Printf("[Listener: %s][Request: %d]: Processing new request on port [%d] - {echo=%t, stream=%t connectionLife=%d}",
    listenerID, requestID, port, echo, stream, connectionLife)

  if stream {
    doStream(l, conn, requestID)
  } else if echo {
    doEcho(l, conn, requestID)
  } else {
    if connectionLife > 0 {
      select {
      case <-time.After(connectionLife):
        log.Printf("[Listener: %s][Request: %d]: Max connection life [%s] reached. Closing connection on port [%d]", listenerID, requestID, connectionLife, port)
        closeClientConnection(conn, requestID, listenerID, port)
      }
    } else {
      log.Printf("[Listener: %s][Request: %d]: Waiting for a byte before closing port [%d]", listenerID, requestID, port)
      conn.SetReadDeadline(time.Time{})
      len, err := bufio.NewReader(conn).Read(make([]byte, 1))
      switch err {
      case nil:
        log.Printf("[Listener: %s][Request: %d]: Received %d bytes, closing port [%d]", listenerID, requestID, len, port)
      case io.EOF:
        log.Printf("[Listener: %s][Request: %d]: Connection closed by client on port [%d]", listenerID, requestID, port)
      default:
        if isConnectionCloseError(err) {
          log.Printf("[Listener: %s][Request: %d]: Connection closed by server on port [%d]", listenerID, requestID, port)
        } else {
          log.Printf("[Listener: %s][Request: %d]: Error reading TCP data on port [%d]: %s", listenerID, requestID, port, err.Error())
        }
      }
    }
  }
  if !l.Open {
    log.Printf("[Listener: %s][Request: %d]: Listener is closed for port [%d]", listenerID, requestID, port)
  }
}
