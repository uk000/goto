/**
 * Copyright 2025 uk
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
	"goto/pkg/global"
	"net"
	"net/http"
	"sync"
)

type ConnectionWatcher struct {
	connections map[int]map[net.Conn]struct{}
	connCounts  map[int]int
	mu          sync.Mutex
	wg          sync.WaitGroup
}

var (
	ConnWatcher = &ConnectionWatcher{
		connections: map[int]map[net.Conn]struct{}{},
		connCounts:  map[int]int{},
	}
	ConnState = ConnWatcher.ConnState
)

func init() {
	global.Funcs.CloseConnectionsForPort = ConnWatcher.CloseConnectionsForPort
}

func (cw *ConnectionWatcher) ConnState(c net.Conn, state http.ConnState) {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	tcpConn := c.(*net.TCPConn)
	tcpAddr := tcpConn.LocalAddr().(*net.TCPAddr)
	switch state {
	case http.StateNew:
		if cw.connections[tcpAddr.Port] == nil {
			cw.connections[tcpAddr.Port] = map[net.Conn]struct{}{}
		}
		cw.connections[tcpAddr.Port][c] = struct{}{}
		cw.connCounts[tcpAddr.Port]++
		cw.wg.Add(1)
	case http.StateClosed, http.StateHijacked:
		if cw.connections[tcpAddr.Port] != nil {
			delete(cw.connections[tcpAddr.Port], c)
		}
		cw.connCounts[tcpAddr.Port]--
		cw.wg.Done()
	}
	//log.Printf("ConnState [%s]: Port [%d] Current Conn Count [%d].", state, tcpAddr.Port, cw.connCounts[tcpAddr.Port])
}

func (cw *ConnectionWatcher) CloseConnectionsForPort(port int) {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	//log.Printf("CloseConnectionsForPort: Port [%d] Current Conn Count [%d].", port, cw.connCounts[port])
	if cw.connections[port] != nil {
		for c := range cw.connections[port] {
			c.Close()
		}
	}
}
