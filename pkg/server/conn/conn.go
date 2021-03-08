package conn

import (
  "fmt"
  "net"
  "net/http"
  "strings"

  "goto/pkg/global"
  "goto/pkg/metrics"
  "goto/pkg/util"
)

var (
  Handler = util.ServerHandler{Name: "connection", Middleware: Middleware}
)

func GetConn(r *http.Request) net.Conn {
  if conn := r.Context().Value(util.ConnectionKey); conn != nil {
    return conn.(net.Conn)
  }
  return nil
}

func Middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    localAddr := ""
    if conn := GetConn(r); conn != nil {
      localAddr = conn.LocalAddr().String()
    } else {
      localAddr = global.PeerAddress
    }
    util.AddLogMessage(fmt.Sprintf("LocalAddr: %s, RemoteAddr: %s", localAddr, r.RemoteAddr), r)
    w.Header().Add("Goto-Remote-Address", r.RemoteAddr)
    pieces := strings.Split(r.RemoteAddr, ":")
    remoteIP := strings.Join(pieces[:len(pieces)-1], ":")
    metrics.UpdateClientRequestCount(remoteIP)
    if next != nil {
      next.ServeHTTP(w, r)
    }
    metrics.UpdateURIRequestCount(strings.ToLower(r.URL.Path))
  })
}
