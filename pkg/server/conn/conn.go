package conn

import (
  "fmt"
  "net"
  "net/http"
  "strings"

  . "goto/pkg/constants"
  "goto/pkg/global"
  "goto/pkg/metrics"
  "goto/pkg/server/listeners"
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
    l := listeners.GetCurrentListener(r)
    if util.IsTunnelRequest(r) {
      tunnelCount := len(r.Header[HeaderGotoHostTunnel]) + 1
      util.SetTunnelCount(r, tunnelCount)
      w.Header().Add(fmt.Sprintf("%s[%d]", HeaderGotoHostTunnel, tunnelCount), l.HostLabel)
      w.Header().Add(fmt.Sprintf("%s[%d]", HeaderViaGotoTunnel, tunnelCount), l.Label)
    } else {
      port := util.GetListenerPort(r)
      w.Header().Add(HeaderGotoRemoteAddress, r.RemoteAddr)
      w.Header().Add(HeaderGotoPort, port)
      w.Header().Add(HeaderGotoHost, l.HostLabel)
      w.Header().Add(HeaderViaGoto, l.Label)
    }
    pieces := strings.Split(r.RemoteAddr, ":")
    remoteIP := strings.Join(pieces[:len(pieces)-1], ":")
    metrics.UpdateClientRequestCount(remoteIP)
    msg := fmt.Sprintf("Goto: [%s] LocalAddr: [%s], RemoteAddr: [%s], RequestHost: [%s], URI: [%s], Method: [%s], Protocol: [%s (%s)], ContentLength: [%s]",
    l.Label, localAddr, r.RemoteAddr, r.Host, r.RequestURI, r.Method, r.Proto, w.Header().Get(HeaderGotoProtocol), r.Header.Get("Content-Length"))
    if targetURL := r.Header.Get(HeaderGotoTargetURL); targetURL != "" {
      msg += fmt.Sprintf(", GotoTargetURL: [%s]", targetURL)
    }
    if global.LogRequestHeaders {
      msg += fmt.Sprintf(", Request Headers: [%s]", util.GetRequestHeadersLog(r))
    }
    util.AddLogMessage(msg, r)
    if next != nil {
      next.ServeHTTP(w, r)
    }
  })
}
