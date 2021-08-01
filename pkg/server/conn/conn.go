package conn

import (
  "crypto/tls"
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
  rs.IsTLS = true
  rs.ServerName = tlsState.ServerName
  rs.TLSVersionNum = tlsState.Version
  rs.TLSVersion = util.GetTLSVersion(&tlsState)
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
    l := listeners.GetCurrentListener(r)
    rs := util.GetRequestStore(r)
    if util.IsTunnelRequest(r) {
      tunnelCount := len(r.Header[HeaderGotoHostTunnel]) + 1
      util.SetTunnelCount(r, tunnelCount)
      w.Header().Add(fmt.Sprintf("%s[%d]", HeaderGotoHostTunnel, tunnelCount), l.HostLabel)
      w.Header().Add(fmt.Sprintf("%s[%d]", HeaderViaGotoTunnel, tunnelCount), l.Label)
      w.Header().Add(fmt.Sprintf("%s[%d]", HeaderRequestHost, tunnelCount), r.Host)
      if l.TLS {
        w.Header().Add(fmt.Sprintf("%s[%d]", HeaderRequestTLSSNI, tunnelCount), rs.ServerName)
        w.Header().Add(fmt.Sprintf("%s[%d]", HeaderRequestTLSVersion, tunnelCount), rs.TLSVersion)
      }
    } else {
      port := util.GetListenerPort(r)
      w.Header().Add(HeaderGotoRemoteAddress, r.RemoteAddr)
      w.Header().Add(HeaderGotoPort, port)
      w.Header().Add(HeaderGotoHost, l.HostLabel)
      w.Header().Add(HeaderViaGoto, l.Label)
      if l.TLS {
        w.Header().Add(HeaderRequestTLSSNI, rs.ServerName)
        w.Header().Add(HeaderRequestTLSVersion, rs.TLSVersion)
      }
    }
    pieces := strings.Split(r.RemoteAddr, ":")
    remoteIP := strings.Join(pieces[:len(pieces)-1], ":")
    metrics.UpdateClientRequestCount(remoteIP)

    msg := fmt.Sprintf("Goto: [%s] LocalAddr: [%s], RemoteAddr: [%s], RequestHost: [%s], URI: [%s], Method: [%s], Protocol: [%s (%s)], ContentLength: [%s]",
      l.Label, localAddr, r.RemoteAddr, r.Host, r.RequestURI, r.Method, r.Proto, w.Header().Get(HeaderGotoProtocol), r.Header.Get("Content-Length"))
    if l.TLS {
      msg += fmt.Sprintf(", ServerName: [%s], TLSVersion: [%s]", rs.ServerName, rs.TLSVersion)
    }
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
    util.DiscardRequestBody(r)
  })
}
