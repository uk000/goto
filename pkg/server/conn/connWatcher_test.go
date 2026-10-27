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
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"goto/pkg/global"
	gototls "goto/pkg/tls"
)

func newWatcher() *ConnectionWatcher {
	return &ConnectionWatcher{connections: map[int]map[net.Conn]struct{}{}, connCounts: map[int]int{}}
}

func (cw *ConnectionWatcher) tracked(port int) int {
	cw.lock.Lock()
	defer cw.lock.Unlock()
	return len(cw.connections[port])
}

func (cw *ConnectionWatcher) counted(port int) int {
	cw.lock.Lock()
	defer cw.lock.Unlock()
	return cw.connCounts[port]
}

// tcpListener is a listener on a free port, which gives the pairs of the two ends of connections made to it.
type tcpListener struct {
	ln   net.Listener
	port int
}

func newTCPListener(t *testing.T) *tcpListener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("cannot listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	return &tcpListener{ln: ln, port: ln.Addr().(*net.TCPAddr).Port}
}

// pair makes a connection to the listener, and gives its server end and its client end.
func (l *tcpListener) pair(t *testing.T) (server, client net.Conn) {
	t.Helper()
	type accepted struct {
		c   net.Conn
		err error
	}
	ch := make(chan accepted, 1)
	go func() {
		c, err := l.ln.Accept()
		ch <- accepted{c, err}
	}()
	client, err := net.DialTimeout("tcp", l.ln.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatalf("cannot dial: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	select {
	case a := <-ch:
		if a.err != nil {
			t.Fatalf("cannot accept: %v", a.err)
		}
		t.Cleanup(func() { a.c.Close() })
		return a.c, client
	case <-time.After(5 * time.Second):
		t.Fatal("the connection was not accepted")
	}
	return nil, nil
}

// isClosedByPeer tells if the connection has been closed from the other end: it reads end of file.
func isClosedByPeer(c net.Conn) bool {
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err := c.Read(make([]byte, 1))
	return err == io.EOF || (err != nil && !isTimeout(err))
}

func isTimeout(err error) bool {
	ne, ok := err.(net.Error)
	return ok && ne.Timeout()
}

// openedAndClosed records the connections that the global watchers are told of. The watchers can't be removed, so
// they are added once, and tests look for the connections that they made.
var (
	watchOnce sync.Once
	watchLock sync.Mutex
	opened    = map[net.Conn]int{}
	closed    = map[net.Conn]int{}
)

func watchConns() {
	watchOnce.Do(func() {
		global.AddConnOpenWatcher(func(c net.Conn) {
			watchLock.Lock()
			defer watchLock.Unlock()
			opened[c]++
		})
		global.AddConnCloseWatcher(func(c net.Conn) {
			watchLock.Lock()
			defer watchLock.Unlock()
			closed[c]++
		})
	})
}

func timesOpened(c net.Conn) int {
	watchLock.Lock()
	defer watchLock.Unlock()
	return opened[c]
}

func timesClosed(c net.Conn) int {
	watchLock.Lock()
	defer watchLock.Unlock()
	return closed[c]
}

func TestConnStateTracksOpenConnectionsPerPort(t *testing.T) {
	watchConns()
	cw := newWatcher()
	l1, l2 := newTCPListener(t), newTCPListener(t)
	s1a, _ := l1.pair(t)
	s1b, _ := l1.pair(t)
	s2, _ := l2.pair(t)

	cw.ConnState(s1a, http.StateNew)
	cw.ConnState(s1b, http.StateNew)
	cw.ConnState(s2, http.StateNew)
	if cw.tracked(l1.port) != 2 || cw.counted(l1.port) != 2 || cw.tracked(l2.port) != 1 || cw.counted(l2.port) != 1 {
		t.Errorf("tracked = %d and %d, counted = %d and %d, want 2 and 1 for each", cw.tracked(l1.port), cw.tracked(l2.port), cw.counted(l1.port), cw.counted(l2.port))
	}
	for _, c := range []net.Conn{s1a, s1b, s2} {
		if got := timesOpened(c); got != 1 {
			t.Errorf("the open watchers were told %d times of a new connection, want once", got)
		}
	}

	// A connection that is in use is still open.
	cw.ConnState(s1a, http.StateActive)
	cw.ConnState(s1a, http.StateIdle)
	if cw.tracked(l1.port) != 2 || cw.counted(l1.port) != 2 {
		t.Error("states other than new, closed and hijacked must not change what is tracked")
	}
	if timesClosed(s1a) != 0 || timesOpened(s1a) != 1 {
		t.Error("watchers must only be told of connections that open and close")
	}

	cw.ConnState(s1a, http.StateClosed)
	if cw.tracked(l1.port) != 1 || cw.counted(l1.port) != 1 || cw.tracked(l2.port) != 1 {
		t.Errorf("after a close: tracked = %d, counted = %d, want 1 on the port of the closed connection and the other port as it was", cw.tracked(l1.port), cw.counted(l1.port))
	}
	if timesClosed(s1a) != 1 {
		t.Errorf("the close watchers were told %d times, want once", timesClosed(s1a))
	}

	// A hijacked connection is not the server's any more.
	cw.ConnState(s1b, http.StateHijacked)
	if cw.tracked(l1.port) != 0 || cw.counted(l1.port) != 0 || timesClosed(s1b) != 1 {
		t.Errorf("after a hijack: tracked = %d, counted = %d, close notices = %d, want none left", cw.tracked(l1.port), cw.counted(l1.port), timesClosed(s1b))
	}
}

func TestConnStateTracksConnectionsThatAreWrappedAroundTCP(t *testing.T) {
	watchConns()
	cw := newWatcher()
	l := newTCPListener(t)
	raw, _ := l.pair(t)
	tlsConn := tls.Server(raw, &tls.Config{})
	peekedRaw, _ := l.pair(t)
	peeked := &gototls.PeekedConn{Conn: peekedRaw}
	peekedTLSRaw, _ := l.pair(t)
	peekedTLS := &gototls.PeekedConn{Conn: tls.Server(peekedTLSRaw, &tls.Config{})}

	for _, c := range []net.Conn{tlsConn, peeked, peekedTLS} {
		cw.ConnState(c, http.StateNew)
	}
	if cw.tracked(l.port) != 3 || cw.counted(l.port) != 3 {
		t.Errorf("tracked = %d, counted = %d, want the 3 connections that wrap a TCP connection", cw.tracked(l.port), cw.counted(l.port))
	}
	// What is tracked is the connection that was given, so that closing it closes the wrapper.
	cw.ConnState(tlsConn, http.StateClosed)
	if cw.tracked(l.port) != 2 {
		t.Errorf("tracked = %d, want the wrapper removed when it closes", cw.tracked(l.port))
	}
	if timesOpened(tlsConn) != 1 || timesClosed(tlsConn) != 1 {
		t.Error("the watchers must be given the connection that the server has")
	}
}

func TestConnStateIgnoresConnectionsThatAreNotTCP(t *testing.T) {
	watchConns()
	cw := newWatcher()
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	cw.ConnState(a, http.StateNew)
	cw.ConnState(a, http.StateClosed)
	if len(cw.connections) != 0 || len(cw.connCounts) != 0 {
		t.Errorf("connections = %v, counts = %v, want connections that are not TCP to be left out", cw.connections, cw.connCounts)
	}
	if timesOpened(a) != 0 || timesClosed(a) != 0 {
		t.Error("watchers must not be told of connections that are not tracked")
	}
}

func TestCloseConnectionsForPortClosesOnlyTheConnectionsOfThePort(t *testing.T) {
	cw := newWatcher()
	l1, l2 := newTCPListener(t), newTCPListener(t)
	s1a, c1a := l1.pair(t)
	s1b, c1b := l1.pair(t)
	s2, c2 := l2.pair(t)
	for _, c := range []net.Conn{s1a, s1b, s2} {
		cw.ConnState(c, http.StateNew)
	}

	cw.CloseConnectionsForPort(l1.port)

	if !isClosedByPeer(c1a) || !isClosedByPeer(c1b) {
		t.Error("the connections of the port must be closed")
	}
	// The connection of the other port works: it gets what is written to it.
	if _, err := s2.Write([]byte("x")); err != nil {
		t.Fatalf("the connection of the other port failed to write: %v", err)
	}
	_ = c2.SetReadDeadline(time.Now().Add(2 * time.Second))
	if n, err := c2.Read(make([]byte, 1)); n != 1 || err != nil {
		t.Errorf("the connection of the other port read %d bytes with error %v, want it left open", n, err)
	}
	// The connections are left to their closing to be forgotten, as the server tells of that.
	if cw.tracked(l1.port) != 2 || cw.tracked(l2.port) != 1 {
		t.Errorf("tracked = %d and %d, want them kept until the server tells that they are closed", cw.tracked(l1.port), cw.tracked(l2.port))
	}
	// A port without connections is fine.
	cw.CloseConnectionsForPort(l1.port + 100000)
}

func TestConnectionWatcherIsWiredIntoTheServer(t *testing.T) {
	if global.Funcs.CloseConnectionsForPort == nil {
		t.Fatal("global.Funcs.CloseConnectionsForPort must be set by the package")
	}
	l := newTCPListener(t)
	s, c := l.pair(t)
	ConnState(s, http.StateNew)
	global.Funcs.CloseConnectionsForPort(l.port)
	if !isClosedByPeer(c) {
		t.Error("the connection must be closed by the function that is set in global.Funcs")
	}
	ConnState(s, http.StateClosed)
	if ConnWatcher.tracked(l.port) != 0 || ConnWatcher.counted(l.port) != 0 {
		t.Errorf("tracked = %d, counted = %d, want none left", ConnWatcher.tracked(l.port), ConnWatcher.counted(l.port))
	}
}

// The watcher is made to be the ConnState of an http server: connections of requests are tracked while they are open,
// and forgotten when the server closes them.
func TestConnStateTracksTheConnectionsOfAnHTTPServer(t *testing.T) {
	cw := newWatcher()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Config.ConnState = cw.ConnState
	srv.Start()
	defer srv.Close()
	port := srv.Listener.Addr().(*net.TCPAddr).Port

	resp, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if cw.tracked(port) != 1 || cw.counted(port) != 1 {
		t.Errorf("tracked = %d, counted = %d after a request, want its connection", cw.tracked(port), cw.counted(port))
	}

	cw.CloseConnectionsForPort(port)
	deadline := time.Now().Add(5 * time.Second)
	for cw.tracked(port) != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if cw.tracked(port) != 0 || cw.counted(port) != 0 {
		t.Errorf("tracked = %d, counted = %d, want the connection forgotten once the server saw it closed", cw.tracked(port), cw.counted(port))
	}
}

func TestConcurrentConnectionStatesAreTrackedExactly(t *testing.T) {
	cw := newWatcher()
	l := newTCPListener(t)
	const conns = 16
	servers := make([]net.Conn, conns)
	for i := range servers {
		servers[i], _ = l.pair(t)
	}
	var wg sync.WaitGroup
	for _, c := range servers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cw.ConnState(c, http.StateNew)
			cw.ConnState(c, http.StateActive)
			cw.CloseConnectionsForPort(l.port)
		}()
	}
	wg.Wait()
	if cw.tracked(l.port) != conns || cw.counted(l.port) != conns {
		t.Fatalf("tracked = %d, counted = %d, want %d", cw.tracked(l.port), cw.counted(l.port), conns)
	}
	for _, c := range servers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cw.ConnState(c, http.StateClosed)
		}()
	}
	wg.Wait()
	if cw.tracked(l.port) != 0 || cw.counted(l.port) != 0 {
		t.Errorf("tracked = %d, counted = %d, want none left", cw.tracked(l.port), cw.counted(l.port))
	}
}
