/**
 * Copyright 2026 uk
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

package server

import (
	"crypto/tls"
	"errors"
	"fmt"
	"goto/pkg/global"
	tcpproxy "goto/pkg/proxy/tcp"
	"goto/pkg/server/listeners"
	"goto/pkg/server/tcp"
	gototls "goto/pkg/tls"
	"goto/pkg/util"
	"log"
	"net"
	"sync"
	"time"
)

var (
	requestCounter int
	lock           sync.RWMutex
)

func InitTCPServer() {
	global.ConfigureTCPServer(serveTCPRequests)
}

func serveTCPRequests(listenerID string, port int, listener net.Listener) error {
	if listener == nil {
		return fmt.Errorf("Listener [%s] not open for business", listenerID)
	}
	stopped := false
	for !stopped {
		if conn, err := listener.Accept(); err == nil {
			lock.Lock()
			requestCounter++
			lock.Unlock()
			l := listeners.GetListenerForPort(port)
			fl := l.ForwardListener
			isTLS := l.TLS
			tlsConfig := l.TLSConfig
			alpn := l.ALPN
			if fl != nil {
				isTLS = fl.TLS
				if tlsConfig == nil {
					tlsConfig = fl.TLSConfig
				}
				if alpn == nil {
					alpn = fl.ALPN
				} else if len(alpn.Protos) == 0 {
					alpn.Protos = fl.ALPN.Protos
				}
			}
			if isTLS {
				tlsConn, pre, err := PeerHandshake(conn, tlsConfig, alpn)
				if err != nil || tlsConn == nil {
					log.Printf("[Listener: %s] TLS Handshake Failed with Error: %s.", listenerID, err.Error())
					conn.Close()
					continue
				}
				if j := util.ProtoToJSON(pre); j != nil {
					log.Printf("[Listener: %s] Read TLS Handshake Preamble from Client: %s.", listenerID, j.ToJSONText())
				} else {
					log.Printf("[Listener: %s] Read TLS Handshake Preamble from Client: %s.", listenerID, util.CleanText(pre))
				}
				conn = tlsConn
			}
			if tcpproxy.WillProxyTCP(port) {
				go tcpproxy.ProxyTCPConnection(port, conn)
			} else if fl != nil {
				var wrappedConn net.Conn = tcp.NewWrappedConn(conn, fl.Port)
				nonClosing := tcp.NewNonClosingListener(wrappedConn)
				httpServer.Serve(nonClosing.Listener())
			} else {
				go tcp.ServeClientConnection(port, requestCounter, conn)
			}
		} else if !util.IsConnectionCloseError(err) {
			log.Println(err)
			continue
		} else {
			stopped = true
		}
	}
	if global.Funcs.IsListenerOpen(port) {
		log.Printf("[Listener: %s] has been restarted. Stopping to serve requests on old listener.", listenerID)
	} else {
		log.Printf("[Listener: %s] has been closed. Stopping to serve requests.", listenerID)
	}
	log.Printf("[Listener: %s] Force closing active client connections for closed listener.", listenerID)
	tcp.CloseListenerConnections(listenerID)
	return nil
}

func PeerHandshake(conn net.Conn, tlsConfig *tls.Config, alpn *gototls.ALPN) (*tls.Conn, []byte, error) {
	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		tlsConn = tls.Server(conn, tlsConfig)
		ok = true
	}
	if !ok || tlsConfig == nil {
		return nil, nil, errors.New("Invalid TLS Conn")
	}
	_ = tlsConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err := tlsConn.Handshake(); err != nil {
		conn.Close()
		return nil, nil, err
	}
	_ = tlsConn.SetReadDeadline(time.Time{}) // reset
	state := tlsConn.ConnectionState()
	if alpn != nil {
		if b, err := alpn.HandleServer(tlsConn, &state); err != nil {
			return nil, nil, err
		} else {
			return tlsConn, b, nil
		}
	}
	return tlsConn, nil, nil
}
