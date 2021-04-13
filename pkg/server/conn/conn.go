package conn

import (
  "context"
  "crypto/tls"
  "fmt"
  "io"
  "io/ioutil"
  "net"
  "net/http"
  "strings"

  "goto/pkg/global"
  "goto/pkg/metrics"
  "goto/pkg/server/listeners"
  "goto/pkg/util"
)

var (
  Handler       util.ServerHandler = util.ServerHandler{Name: "connection", Middleware: Middleware}
  connectionKey *util.ContextKey   = &util.ContextKey{"connection"}
)

func SaveConnInContext(ctx context.Context, c net.Conn) context.Context {
  return context.WithValue(ctx, connectionKey, c)
}

func GetConn(r *http.Request) net.Conn {
  if conn := r.Context().Value(connectionKey); conn != nil {
    return conn.(net.Conn)
  }
  return nil
}

func captureTLSInfo(r *http.Request) {
  var conn net.Conn
  if conn = GetConn(r); conn == nil {
    return
  }
  if l := listeners.GetListenerForPort(util.GetCurrentPort(r)); l == nil || !l.TLS {
    return
  }
  tlsConn, ok := conn.(*tls.Conn)
  if !ok {
    return
  }
  rs := util.GetRequestStore(r)
  if rs == nil {
    return
  }
  tlsState := tlsConn.ConnectionState()
  if !tlsState.HandshakeComplete {
    return
  }
  fmt.Printf("ServerName: %+v\n", tlsState.ServerName)
  rs.ServerName = tlsState.ServerName
  switch tlsState.Version {
  case tls.VersionTLS13:
    rs.TLSVersion = "1.3"
  case tls.VersionTLS12:
    rs.TLSVersion = "1.2"
  case tls.VersionTLS11:
    rs.TLSVersion = "1.1"
  case tls.VersionTLS10:
    rs.TLSVersion = "1.0"
  default:
    rs.TLSVersion = "???"
  }
}

func Middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    localAddr := ""
    if conn := GetConn(r); conn != nil {
      captureTLSInfo(r)
      localAddr = conn.LocalAddr().String()
    } else {
      localAddr = global.PeerAddress
    }
    rs := util.GetRequestStore(r)
    msg := fmt.Sprintf("LocalAddr: [%s], RemoteAddr: [%s], Protocol [%s], Host: [%s], Content Length: [%s]",
      localAddr, r.RemoteAddr, r.Proto, r.Host, r.Header.Get("Content-Length"))
    if l := listeners.GetListenerForPort(util.GetCurrentPort(r)); l != nil && l.TLS {
      msg += fmt.Sprintf(", ServerName: [%s], TLSVersion: [%s]", rs.ServerName, rs.TLSVersion)
      w.Header().Add("Request-TLS-SNI", rs.ServerName)
      w.Header().Add("Request-TLS-Version", rs.TLSVersion)
    }
    util.AddLogMessage(msg, r)
    w.Header().Add("Goto-Remote-Address", r.RemoteAddr)
    pieces := strings.Split(r.RemoteAddr, ":")
    remoteIP := strings.Join(pieces[:len(pieces)-1], ":")
    metrics.UpdateClientRequestCount(remoteIP)
    if next != nil {
      next.ServeHTTP(w, r)
    }
    io.Copy(ioutil.Discard, r.Body)
    metrics.UpdateURIRequestCount(strings.ToLower(r.URL.Path))
  })
}
