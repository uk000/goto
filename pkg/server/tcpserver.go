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

package server

import (
	"goto/pkg/global"
	"goto/pkg/proxy"
	"goto/pkg/server/tcp"
	"goto/pkg/util"
	"log"
	"net"
	"sync"
)

var (
	requestCounter int
	lock           sync.RWMutex
)

func StartTCPServer(listenerID string, port int, listener net.Listener) {
	go serveTCPRequests(listenerID, port, listener)
}

func serveTCPRequests(listenerID string, port int, listener net.Listener) {
	if listener == nil {
		log.Printf("Listener [%s] not open for business", listenerID)
		return
	}
	stopped := false
	for !stopped {
		if conn, err := listener.Accept(); err == nil {
			lock.Lock()
			requestCounter++
			lock.Unlock()
			if proxy.WillProxyTCP(port) {
				go proxy.ProxyTCPConnection(port, conn)
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
}
