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

package transport

import (
	"net"
	"sync"
)

type ConnTracker struct {
	net.Conn
	TransportIntercept *BaseTransportIntercept
	closeSync          sync.Once
}

func NewConnTracker(conn net.Conn, t *BaseTransportIntercept) (net.Conn, error) {
	t.lock.Lock()
	t.ConnCount++
	t.lock.Unlock()
	ct := &ConnTracker{
		Conn:               conn,
		TransportIntercept: t,
	}
	return ct, nil
}

func (ct *ConnTracker) Close() (err error) {
	err = ct.Conn.Close()
	ct.closeSync.Do(func() {
		ct.TransportIntercept.lock.Lock()
		ct.TransportIntercept.ConnCount--
		ct.TransportIntercept.lock.Unlock()
	})
	return err
}
