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
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	gototls "goto/pkg/tls"
	"goto/pkg/util"
)

// unixListener is a listener on a socket that is registered for a port, which gives the pairs of the two ends of
// connections made to it.
type unixListener struct {
	ln   net.Listener
	path string
	port int
}

func newUnixListener(t *testing.T, port int, register bool) *unixListener {
	t.Helper()
	dir, err := os.MkdirTemp("", "u")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "c.sock")
	ln, err := util.ListenUDS(path)
	if err != nil {
		t.Fatalf("cannot listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	if register {
		util.RegisterUDSPort(path, port)
	}
	return &unixListener{ln: ln, path: path, port: port}
}

func (l *unixListener) pair(t *testing.T) (server, client net.Conn) {
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
	client, err := net.DialTimeout("unix", l.path, 5*time.Second)
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

func TestGetConnPort(t *testing.T) {
	tcp := newTCPListener(t)
	tcpServer, _ := tcp.pair(t)
	if got := getConnPort(tcpServer); got != tcp.port {
		t.Errorf("getConnPort of a TCP connection = %d, want %d", got, tcp.port)
	}
	uds := newUnixListener(t, 59121, true)
	udsServer, _ := uds.pair(t)
	if got := getConnPort(udsServer); got != 59121 {
		t.Errorf("getConnPort of a socket connection = %d, want the port of the socket", got)
	}
	wrapped := &gototls.PeekedConn{Conn: tls.Server(udsServer, &tls.Config{})}
	if got := getConnPort(wrapped); got != 59121 {
		t.Errorf("getConnPort of a wrapped socket connection = %d, want the port of the socket", got)
	}
	unregistered := newUnixListener(t, 0, false)
	other, _ := unregistered.pair(t)
	if got := getConnPort(other); got != 0 {
		t.Errorf("getConnPort of a socket that is not registered = %d, want 0", got)
	}
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if got := getConnPort(a); got != 0 {
		t.Errorf("getConnPort of a pipe = %d, want 0", got)
	}
}

func TestConnStateTracksSocketConnectionsByTheirPort(t *testing.T) {
	watchConns()
	cw := newWatcher()
	l := newUnixListener(t, 59122, true)
	s1, _ := l.pair(t)
	s2, _ := l.pair(t)
	wrapped := &gototls.PeekedConn{Conn: tls.Server(func() net.Conn { s, _ := l.pair(t); return s }(), &tls.Config{})}

	cw.ConnState(s1, http.StateNew)
	cw.ConnState(s2, http.StateNew)
	cw.ConnState(wrapped, http.StateNew)
	if cw.tracked(l.port) != 3 || cw.counted(l.port) != 3 {
		t.Errorf("tracked = %d, counted = %d, want the 3 socket connections", cw.tracked(l.port), cw.counted(l.port))
	}
	if timesOpened(s1) != 1 || timesOpened(wrapped) != 1 {
		t.Error("the open watchers must be told of the socket connections")
	}

	cw.ConnState(s1, http.StateClosed)
	cw.ConnState(wrapped, http.StateHijacked)
	if cw.tracked(l.port) != 1 || cw.counted(l.port) != 1 {
		t.Errorf("after the closes: tracked = %d, counted = %d, want 1", cw.tracked(l.port), cw.counted(l.port))
	}
	if timesClosed(s1) != 1 || timesClosed(wrapped) != 1 {
		t.Error("the close watchers must be told of the socket connections that close")
	}
}

func TestConnStateIgnoresSocketsThatAreNotRegistered(t *testing.T) {
	watchConns()
	cw := newWatcher()
	l := newUnixListener(t, 0, false)
	s, _ := l.pair(t)
	cw.ConnState(s, http.StateNew)
	cw.ConnState(s, http.StateClosed)
	if len(cw.connections) != 0 || len(cw.connCounts) != 0 {
		t.Errorf("connections = %v, counts = %v, want a socket without a port to be left out", cw.connections, cw.connCounts)
	}
	if timesOpened(s) != 0 || timesClosed(s) != 0 {
		t.Error("watchers must not be told of connections that are not tracked")
	}
}

func TestCloseConnectionsForPortClosesSocketConnections(t *testing.T) {
	cw := newWatcher()
	l := newUnixListener(t, 59123, true)
	other := newUnixListener(t, 59124, true)
	s1, c1 := l.pair(t)
	s2, c2 := other.pair(t)
	cw.ConnState(s1, http.StateNew)
	cw.ConnState(s2, http.StateNew)

	cw.CloseConnectionsForPort(l.port)

	if !isClosedByPeer(c1) {
		t.Error("the socket connection of the port must be closed")
	}
	if _, err := s2.Write([]byte("x")); err != nil {
		t.Fatalf("the connection of the other port failed to write: %v", err)
	}
	_ = c2.SetReadDeadline(time.Now().Add(2 * time.Second))
	if n, err := c2.Read(make([]byte, 1)); n != 1 || err != nil {
		t.Errorf("the connection of the other port read %d bytes with error %v, want it left open", n, err)
	}
}
