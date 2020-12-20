package runner

import (
  "goto/pkg/global"
  "goto/pkg/server/tcp"
  "goto/pkg/util"
  "log"
  "net"
  "sync"
)

var (
  requestCounter int
  lock           sync.RWMutex
)

func StartTCPServer(listenerID string, port int, listener net.Listener) {
  go serveTCPRequests(listenerID, port, listener)
}

func serveTCPRequests(listenerID string, port int, listener net.Listener) {
  if listener == nil {
    log.Printf("Listener [%s] not open for business", listenerID)
    return
  }
  stopped := false
  for !stopped {
    if conn, err := listener.Accept(); err == nil {
      lock.Lock()
      requestCounter++
      lock.Unlock()
      go tcp.ServeClientConnection(port, requestCounter, conn.(*net.TCPConn))
    } else if !util.IsConnectionCloseError(err) {
      log.Println(err)
      continue
    } else {
      stopped = true
    }
  }
  if global.IsListenerOpen(port) {
    log.Printf("[Listener: %s] has been restarted. Stopping to serve requests on old listener.", listenerID)
  } else {
    log.Printf("[Listener: %s] has been closed. Stopping to serve requests.", listenerID)
  }
  log.Printf("[Listener: %s] Force closing active client connections for closed listener.", listenerID)
  tcp.CloseListenerConnections(listenerID)
}
