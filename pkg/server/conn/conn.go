package conn

import (
	"context"
	"fmt"
	"net"
	"net/http"

	"goto/pkg/global"
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
    //log.Printf("Number of goroutines: %d\n", runtime.NumGoroutine())
    localAddr := ""
    if conn := GetConn(r); conn != nil {
      localAddr = conn.LocalAddr().String()
    } else {
      localAddr = global.PeerAddress
    }
    util.AddLogMessage(fmt.Sprintf("LocalAddr: %s, RemoteAddr: %s", localAddr, r.RemoteAddr), r)
    if next != nil {
      next.ServeHTTP(w, r)
    }
  })
}
