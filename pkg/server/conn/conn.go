/**
 * Copyright 2021 uk
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

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
    port := util.GetListenerPort(r)
    if util.IsTunnelRequest(r) {
      w.Header().Add(fmt.Sprintf("%s|%d", HeaderGotoRemoteAddress, rs.TunnelCount), r.RemoteAddr)
      w.Header().Add(fmt.Sprintf("%s|%d", HeaderGotoPort, rs.TunnelCount), port)
      w.Header().Add(fmt.Sprintf("%s|%d", HeaderGotoTunnelHost, rs.TunnelCount), l.HostLabel)
      w.Header().Add(fmt.Sprintf("%s|%d", HeaderViaGotoTunnel, rs.TunnelCount), l.Label)
    } else {
      w.Header().Add(HeaderGotoRemoteAddress, r.RemoteAddr)
      w.Header().Add(HeaderGotoPort, port)
      w.Header().Add(HeaderGotoHost, l.HostLabel)
      w.Header().Add(HeaderViaGoto, l.Label)
    }
    pieces := strings.Split(r.RemoteAddr, ":")
    remoteIP := strings.Join(pieces[:len(pieces)-1], ":")
    metrics.UpdateClientRequestCount(remoteIP)

    msg := fmt.Sprintf("Goto: [%s] LocalAddr: [%s], RemoteAddr: [%s], RequestHost: [%s], URI: [%s], Method: [%s], Protocol: [%s], Goto-Protocol: [%s], ContentLength: [%s]",
      l.Label, localAddr, r.RemoteAddr, r.Host, r.RequestURI, r.Method, r.Proto, rs.GotoProtocol, r.Header.Get("Content-Length"))
    if l.TLS {
      msg += fmt.Sprintf(", ServerName: [%s], TLSVersion: [%s]", rs.ServerName, rs.TLSVersion)
    }
    if targetURL := r.Header.Get(HeaderGotoTargetURL); targetURL != "" {
      msg += fmt.Sprintf(", GotoTargetURL: [%s]", targetURL)
    }
    if global.LogRequestHeaders {
      msg += fmt.Sprintf(", Request Headers: [%s]", util.GetHeadersLog(r.Header))
    }
    util.AddLogMessage(msg, r)
    if rs.IsTunnelConnectRequest {
      util.AddLogMessage("Serving Tunnel Connect Request", r)
    } else if next != nil {
      next.ServeHTTP(w, r)
    }
    util.DiscardRequestBody(r)
  })
}
