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

package tcpproxy

import (
	"context"
	"errors"
	"goto/pkg/global"
	"goto/pkg/util"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	minReadBuffer = 4096
	// writeQueueSize is how many chunks of downstream data can wait for an upstream endpoint to take them.
	writeQueueSize = 4
)

// tcpPump relays the bytes of one downstream connection to the endpoints of an upstream, and the bytes that the first
// endpoint answers back to the downstream.
//
// Every goroutine of the pump is done by the time run() returns, and so are all the connections. The pump is over
// when the first endpoint is done with the connection. If the downstream is done first, the endpoints just get to see
// the end of the stream (a half close), and the pump waits for the first endpoint to answer and finish.
type tcpPump struct {
	port       int
	up         *TCPUpstream
	down       net.Conn
	downT      *ConnTracker
	ctx        context.Context
	cancel     context.CancelFunc
	lastActive atomic.Int64
	wg         sync.WaitGroup
}

type tcpEndpointConn struct {
	address string
	conn    net.Conn
	tracker *ConnTracker
	queue   chan []byte
}

// touch records the current time as the last time that the pump moved data.
func (pm *tcpPump) touch() {
	pm.lastActive.Store(time.Now().UnixNano())
}

// idleFor tells how long the pump has been without moving data.
func (pm *tcpPump) idleFor() time.Duration {
	return time.Since(time.Unix(0, pm.lastActive.Load()))
}

// readBufferSize returns the size of the buffer to read connections with, which is the max MTU size but at least a minimum.
func readBufferSize() int {
	if n := global.ServerConfig.MaxMTUSize; n > minReadBuffer {
		return n
	}
	return minReadBuffer
}

// isTimeout tells whether the error is a network timeout.
func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// sleepCtx sleeps for the given duration, unless the context is done first. It tells whether it slept in full.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// proxyPipe connects to the endpoints of the upstream and relays traffic between them and the downstream connection.
// The data that the proxy has already consumed from the downstream (the TLS client hello) is passed as pendingData,
// to be sent to every endpoint ahead of everything else.
func (st *TCPSessionTracker) proxyPipe(downConn net.Conn, up *TCPUpstream, pendingData []byte) {
	endpoints := up.sortedEndpoints()
	if len(endpoints) == 0 {
		log.Printf("TCP Proxy[%d]: No upstream endpoints\n", st.ProxyPort)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pm := &tcpPump{port: st.ProxyPort, up: up, down: downConn, downT: st.downTracker(), ctx: ctx, cancel: cancel}
	pm.touch()
	eps := pm.connect(st, endpoints)
	defer func() {
		now := time.Now()
		pm.downT.markClosed(now)
		for _, ep := range eps {
			ep.conn.Close()
			ep.tracker.markClosed(now)
		}
	}()
	if len(eps) == 0 {
		log.Printf("TCP Proxy[%d]: Failed to connect to any endpoint of upstream [%s] for downstream [%s]\n", st.ProxyPort, up.Name, st.DownAddress)
		return
	}
	pm.run(eps, pendingData)
	log.Printf("TCP Proxy[%d]: Finished proxying connection [%s] to upstream [%s]\n", st.ProxyPort, st.DownAddress, up.Name)
}

// connect dials the endpoints, retrying failed attempts, and returns the connections that got established. Endpoints
// that can't be connected to are tracked as such, and left out.
func (pm *tcpPump) connect(st *TCPSessionTracker, endpoints []*TCPEndpoint) []*tcpEndpointConn {
	eps := []*tcpEndpointConn{}
	for _, endpoint := range endpoints {
		tracker := st.addUpTracker(endpoint.Address, endpoint.Address, time.Now())
		conn, err := pm.dial(endpoint.Address)
		if err != nil {
			log.Printf("TCP Proxy[%d]: Failed to connect to upstream endpoint [%s]: %s\n", pm.port, endpoint.Address, err.Error())
			tracker.markConnectFailed()
			tracker.markClosed(time.Now())
			continue
		}
		tracker.init(pm.port, endpoint.Address, conn.RemoteAddr().String(), time.Now())
		eps = append(eps, &tcpEndpointConn{
			address: endpoint.Address,
			conn:    conn,
			tracker: tracker,
			queue:   make(chan []byte, writeQueueSize),
		})
	}
	return eps
}

// dial connects to the address, retrying a failed attempt after the retry delay of the upstream, until it connects or the pump is done.
func (pm *tcpPump) dial(address string) (net.Conn, error) {
	attempts := pm.up.Retries + 1
	var err error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			if delay := pm.up.nextRetryDelay(); delay > 0 {
				log.Printf("TCP Proxy[%d]: Sleeping for [%s] before retrying\n", pm.port, delay)
				if !sleepCtx(pm.ctx, delay) {
					return nil, pm.ctx.Err()
				}
			}
		}
		var conn net.Conn
		dialer := net.Dialer{Timeout: dialTimeout}
		if conn, err = dialer.DialContext(pm.ctx, "tcp", address); err == nil {
			return conn, nil
		}
		log.Printf("TCP Proxy[%d]: Error while dialing upstream address [%s]: %s\n", pm.port, address, err.Error())
	}
	return nil, err
}

// run starts the goroutines that write to and read from the endpoints and the downstream, and waits for all of them to finish.
func (pm *tcpPump) run(eps []*tcpEndpointConn, pendingData []byte) {
	// Closing the connections is what gets the goroutines out of their blocking reads and writes.
	pm.wg.Add(1)
	go func() {
		defer pm.wg.Done()
		<-pm.ctx.Done()
		pm.down.Close()
		for _, ep := range eps {
			ep.conn.Close()
		}
	}()
	for i, ep := range eps {
		primary := i == 0
		pm.wg.Add(2)
		go pm.writeUpstream(ep, primary, pendingData)
		if primary {
			go pm.relayToDownstream(ep)
		} else {
			go pm.discardUpstream(ep)
		}
	}
	pm.wg.Add(1)
	go pm.readDownstream(eps)
	pm.wg.Wait()
}

// read reads from the connection, and keeps waiting through the idle periods of the connection as long as the pump is
// not idle altogether, i.e. the other direction is still at it.
func (pm *tcpPump) read(conn net.Conn, buff []byte) (n int, err error) {
	for {
		_ = conn.SetReadDeadline(time.Now().Add(idleTimeout))
		n, err = conn.Read(buff)
		if n == 0 && err == nil {
			continue
		}
		if err != nil && n == 0 && isTimeout(err) && pm.ctx.Err() == nil && pm.idleFor() < idleTimeout {
			continue
		}
		return
	}
}

// noteReadError classifies the error of a read (closed, reset, timeout or other) and marks it on the tracker, unless the pump is closing.
func (pm *tcpPump) noteReadError(err error, t *ConnTracker, label, address string) {
	if pm.ctx.Err() != nil {
		log.Printf("TCP Proxy[%d]: [READ] [%s] connection [%s] session closed\n", pm.port, label, address)
		return
	}
	switch {
	case errors.Is(err, io.EOF):
		t.markRemoteClosed()
		log.Printf("TCP Proxy[%d]: [READ] Connection [%s] closed by %s\n", pm.port, address, label)
	case errors.Is(err, syscall.ECONNRESET):
		t.markRemoteClosed()
		log.Printf("TCP Proxy[%d]: [READ] Connection [%s] reset by %s\n", pm.port, address, label)
	case isTimeout(err):
		t.markReadError()
		log.Printf("TCP Proxy[%d]: [READ] [%s] connection [%s] Read timed out\n", pm.port, label, address)
	default:
		t.markReadError()
		log.Printf("TCP Proxy[%d]: [READ] Error reading from %s connection [%s]: %s\n", pm.port, label, address, err.Error())
	}
}

// readDownstream reads the downstream and queues each chunk for every endpoint, and closes the queues if the downstream is done or cancels the pump if it fails.
func (pm *tcpPump) readDownstream(eps []*tcpEndpointConn) {
	defer pm.wg.Done()
	address := pm.down.RemoteAddr().String()
	buff := make([]byte, readBufferSize())
	for {
		n, err := pm.read(pm.down, buff)
		if n > 0 {
			pm.touch()
			pm.downT.recordRead(n, time.Now())
			if debugLogs() {
				log.Printf("TCP Proxy[%d]: [READ] [%d] bytes from downstream [%s]\n", pm.port, n, address)
			}
			// The chunk is handed to goroutines that outlive the next read, so it can't be a slice of the read buffer
			chunk := append([]byte(nil), buff[:n]...)
			for _, ep := range eps {
				select {
				case ep.queue <- chunk:
				case <-pm.ctx.Done():
					return
				}
			}
		}
		if err != nil {
			pm.noteReadError(err, pm.downT, "downstream", address)
			if errors.Is(err, io.EOF) {
				for _, ep := range eps {
					close(ep.queue)
				}
			} else {
				pm.cancel()
			}
			return
		}
	}
}

// writeUpstream writes the pending data and then the queued chunks of the downstream to the endpoint, with the drops and the delays of the upstream.
func (pm *tcpPump) writeUpstream(ep *tcpEndpointConn, primary bool, pendingData []byte) {
	defer pm.wg.Done()
	failed := false
	fail := func() {
		failed = true
		if primary {
			pm.cancel()
		} else {
			ep.conn.Close()
		}
	}
	if len(pendingData) > 0 && !pm.write(ep.conn, ep.tracker, pendingData, "upstream", ep.address) {
		fail()
	}
	for {
		select {
		case <-pm.ctx.Done():
			return
		case chunk, ok := <-ep.queue:
			if !ok {
				// The downstream is done sending. Let the endpoint know, and leave it to answer.
				if cw, ok := ep.conn.(interface{ CloseWrite() error }); ok && !failed {
					cw.CloseWrite()
				}
				return
			}
			if failed {
				continue
			}
			if pm.up.shouldDrop() {
				ep.tracker.recordDrop()
				log.Printf("TCP Proxy[%d]: [WRITE] Packet dropped for upstream connection [%s]\n", pm.port, ep.address)
				continue
			}
			if !pm.delay(ep.tracker, ep.address) {
				return
			}
			if !pm.write(ep.conn, ep.tracker, chunk, "upstream", ep.address) {
				fail()
			}
		}
	}
}

// relayToDownstream relays what the primary endpoint sends to the downstream. It ends the pump when it's done.
func (pm *tcpPump) relayToDownstream(ep *tcpEndpointConn) {
	defer pm.wg.Done()
	defer pm.cancel()
	downAddress := pm.down.RemoteAddr().String()
	buff := make([]byte, readBufferSize())
	for {
		n, err := pm.read(ep.conn, buff)
		if n > 0 {
			pm.touch()
			ep.tracker.recordRead(n, time.Now())
			if debugLogs() {
				log.Printf("TCP Proxy[%d]: [READ] [%d] bytes from upstream [%s]\n", pm.port, n, ep.address)
			}
			if pm.up.shouldDrop() {
				pm.downT.recordDrop()
				log.Printf("TCP Proxy[%d]: [WRITE] Packet dropped for downstream connection [%s]\n", pm.port, downAddress)
			} else if !pm.delay(pm.downT, downAddress) || !pm.write(pm.down, pm.downT, buff[:n], "downstream", downAddress) {
				return
			}
		}
		if err != nil {
			pm.noteReadError(err, ep.tracker, "upstream", ep.address)
			return
		}
	}
}

// discardUpstream reads what the other endpoints send, to keep them going and to track them.
func (pm *tcpPump) discardUpstream(ep *tcpEndpointConn) {
	defer pm.wg.Done()
	buff := make([]byte, readBufferSize())
	for {
		n, err := pm.read(ep.conn, buff)
		if n > 0 {
			pm.touch()
			ep.tracker.recordRead(n, time.Now())
			if debugLogs() {
				log.Printf("TCP Proxy[%d]: [READ] Discarded [%d] bytes from upstream [%s]\n", pm.port, n, ep.address)
			}
		}
		if err != nil {
			pm.noteReadError(err, ep.tracker, "upstream", ep.address)
			return
		}
	}
}

// delay applies the delay of the upstream, if there is one to apply, before a write to the connection tracked by t.
// It returns false if the pump was done in the meantime.
func (pm *tcpPump) delay(t *ConnTracker, address string) bool {
	d := pm.up.nextDelay()
	if d <= 0 {
		return true
	}
	t.recordDelay()
	if debugLogs() {
		log.Printf("[DEBUG] Proxy[%d]: Delaying write to [%s] for Target [%s] by [%s]\n", pm.port, address, pm.up.Name, d)
	}
	return sleepCtx(pm.ctx, d)
}

// write writes to the connection. It returns false if the write failed, unless it's the pump that is closing the
// connection.
func (pm *tcpPump) write(conn net.Conn, t *ConnTracker, data []byte, label, address string) bool {
	err := util.Write(data, conn)
	if err != nil {
		if pm.ctx.Err() != nil {
			return false
		}
		if errors.Is(err, io.EOF) {
			t.markRemoteClosed()
			log.Printf("TCP Proxy[%d]: [WRITE] Connection [%s] closed by %s\n", pm.port, address, label)
		} else {
			t.markWriteError()
			log.Printf("TCP Proxy[%d]: [WRITE] Error writing to %s connection [%s]: %s\n", pm.port, label, address, err.Error())
		}
		return false
	}
	pm.touch()
	t.recordWrite(len(data), time.Now())
	if debugLogs() {
		log.Printf("TCP Proxy[%d]: [WRITE] Wrote [%d] bytes to %s [%s]\n", pm.port, len(data), label, address)
	}
	return true
}
