package conn

import (
  "context"
  "fmt"
  "io"
  "io/ioutil"
  "net"
  "net/http"
  "strings"

  "goto/pkg/global"
  "goto/pkg/metrics"
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

func Middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    localAddr := ""
    if conn := GetConn(r); conn != nil {
      localAddr = conn.LocalAddr().String()
    } else {
      localAddr = global.PeerAddress
    }
    util.AddLogMessage(fmt.Sprintf("LocalAddr: %s, RemoteAddr: %s, Protocol %s, Host: %s, Content Length: [%s]",
      localAddr, r.RemoteAddr, r.Proto, r.Host, r.Header.Get("Content-Length")), r)
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
