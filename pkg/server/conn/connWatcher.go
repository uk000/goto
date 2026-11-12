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

package conn

import (
	"crypto/tls"
	"goto/pkg/global"
	gototls "goto/pkg/tls"
	"goto/pkg/util"
	"net"
	"net/http"
	"sync"
)

type ConnectionWatcher struct {
	connections map[int]map[net.Conn]struct{}
	// connPorts is the port that each tracked connection was counted under, as the port of a connection that has closed
	// can't always be found any more (a listener on a unix domain socket has its port forgotten when it closes).
	connPorts  map[net.Conn]int
	connCounts map[int]int
	lock       sync.Mutex
}

var (
	ConnWatcher = &ConnectionWatcher{
		connections: map[int]map[net.Conn]struct{}{},
		connPorts:   map[net.Conn]int{},
		connCounts:  map[int]int{},
	}
	ConnState = ConnWatcher.ConnState
)

// init exposes CloseConnectionsForPort through the global funcs.
func init() {
	global.Funcs.CloseConnectionsForPort = ConnWatcher.CloseConnectionsForPort
}

// getBaseConn gives the TCP or unix domain socket connection that is under the wrappers of the connection.
func getBaseConn(c net.Conn) net.Conn {
	switch conn := c.(type) {
	case *net.TCPConn, *net.UnixConn:
		return conn
	case *tls.Conn:
		return getBaseConn(conn.NetConn())
	case *gototls.PeekedConn:
		return getBaseConn(conn.Conn)
	}
	return nil
}

// getConnPort gives the port of the listener that accepted the connection, or 0 if it's not a TCP or UDS connection.
func getConnPort(c net.Conn) int {
	switch conn := getBaseConn(c).(type) {
	case *net.TCPConn:
		if tcpAddr, ok := conn.LocalAddr().(*net.TCPAddr); ok && tcpAddr != nil {
			return tcpAddr.Port
		}
	case *net.UnixConn:
		return util.GetUDSPort(conn.LocalAddr())
	}
	return 0
}

// ConnState is the http.Server ConnState hook that tracks open connections per local port and notifies the global conn open/close callbacks.
func (cw *ConnectionWatcher) ConnState(c net.Conn, state http.ConnState) {
	cw.lock.Lock()
	defer cw.lock.Unlock()
	switch state {
	case http.StateNew:
		port := getConnPort(c)
		if port == 0 {
			return
		}
		if cw.connections[port] == nil {
			cw.connections[port] = map[net.Conn]struct{}{}
		}
		if cw.connPorts == nil {
			cw.connPorts = map[net.Conn]int{}
		}
		cw.connections[port][c] = struct{}{}
		cw.connPorts[c] = port
		cw.connCounts[port]++
		global.OnConnOpen(c)
	case http.StateClosed, http.StateHijacked:
		port, ok := cw.connPorts[c]
		if !ok {
			return
		}
		delete(cw.connPorts, c)
		if cw.connections[port] != nil {
			delete(cw.connections[port], c)
		}
		cw.connCounts[port]--
		global.OnConnClose(c)
	}
	//log.Printf("ConnState [%s]: Port [%d] Current Conn Count [%d].", state, port, cw.connCounts[port])
}

// CloseConnectionsForPort closes all tracked connections on the given port.
func (cw *ConnectionWatcher) CloseConnectionsForPort(port int) {
	cw.lock.Lock()
	defer cw.lock.Unlock()
	//log.Printf("CloseConnectionsForPort: Port [%d] Current Conn Count [%d].", port, cw.connCounts[port])
	if cw.connections[port] != nil {
		for c := range cw.connections[port] {
			c.Close()
		}
	}
}
