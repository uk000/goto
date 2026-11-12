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
	"fmt"
	"goto/pkg/global"
	"goto/pkg/server/listeners"
	"goto/pkg/server/tcp"
	gototls "goto/pkg/tls"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var tcpTestPorts = 45900

// tcpTestPort returns a port number that no listener has.
func tcpTestPort() int {
	tcpTestPorts++
	return tcpTestPorts
}

var stubListenerFuncsOnce sync.Once

// stubListenerFuncs makes the listeners that the tests serve connections for look open. It is done once, and is not undone:
// the connections that a test leaves keep reading what it sets for a while after the test.
func stubListenerFuncs(t *testing.T) {
	t.Helper()
	stubListenerFuncsOnce.Do(func() {
		global.Funcs.IsListenerOpen = func(int) bool { return true }
		global.Funcs.GetListenerID = func(port int) string { return strconv.Itoa(port) + "-1" }
	})
}

// registerAsDefaultListener makes the default listener the listener of the port, and puts it back as it was when the test ends.
func registerAsDefaultListener(t *testing.T, port int) *listeners.Listener {
	t.Helper()
	l := listeners.DefaultListener
	prevPort, prevForward, prevALPN, prevTLS := l.Port, l.ForwardListener, l.ALPN, l.TLS
	l.Port, l.ForwardListener, l.ALPN, l.TLS = port, nil, nil, false
	t.Cleanup(func() { l.Port, l.ForwardListener, l.ALPN, l.TLS = prevPort, prevForward, prevALPN, prevTLS })
	return l
}

// serveTCPOn serves the connections that a loopback listener accepts with serveTCPRequests, as the server does for the
// port, and returns the address of the listener. The loop is stopped when the test ends.
func serveTCPOn(t *testing.T, port int) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- serveTCPRequests(fmt.Sprintf("%d-test", port), port, ln) }()
	t.Cleanup(func() {
		ln.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serveTCPRequests ended with %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("the loop that accepts the connections did not end when its listener was closed")
		}
	})
	return ln.Addr().String()
}

func dialTCP(t *testing.T, addr string) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	t.Cleanup(func() { conn.Close() })
	return conn
}

// A connection that is accepted when the port has no listener registered (as when the listener is being removed, or has
// not been stored yet) was served with the nil listener, which ended the server.
func TestServeTCPRequestsClosesConnectionsOfAPortThatHasNoListener(t *testing.T) {
	stubListenerFuncs(t)
	port := tcpTestPort()
	if listeners.GetListenerForPort(port) != nil {
		t.Fatalf("port %d has a listener", port)
	}
	addr := serveTCPOn(t, port)
	// the loop goes on serving what comes after the connection that it closed
	for i := 0; i < 3; i++ {
		conn := dialTCP(t, addr)
		if n, err := conn.Read(make([]byte, 1)); n != 0 || err == nil {
			t.Errorf("connection %d: read %d bytes and %v, want the connection to be closed by the server", i, n, err)
		}
	}
}

// The connections that several accept loops hand to the TCP handlers get their request IDs from one counter.
func TestServeTCPRequestsOfSeveralListenersCountsTheRequestsOnce(t *testing.T) {
	stubListenerFuncs(t)
	port := tcpTestPort()
	registerAsDefaultListener(t, port)
	if _, msg := tcp.InitTCPConfig(port, &tcp.TCPConfig{Echo: true, EchoResponseSize: 1}); msg != "" {
		t.Fatal(msg)
	}
	addrs := []string{serveTCPOn(t, port), serveTCPOn(t, port)}
	var wg sync.WaitGroup
	errs := make(chan error, 100)
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(addr string) {
			defer wg.Done()
			for i := 0; i < 15; i++ {
				conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
				if err != nil {
					errs <- err
					return
				}
				conn.SetDeadline(time.Now().Add(10 * time.Second))
				buf := make([]byte, 1)
				if _, err := conn.Write([]byte("x")); err != nil {
					errs <- err
				} else if _, err := io.ReadFull(conn, buf); err != nil || buf[0] != 'x' {
					errs <- fmt.Errorf("echo = %q, %v", buf, err)
				}
				conn.Close()
			}
		}(addrs[w%len(addrs)])
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// A listener forwards its connections to the HTTP server of another one. When it has ALPN settings without protocols
// it takes those of the forward listener, which may have none, and has no settings of its own to be given them.
func TestServeTCPRequestsOfAListenerThatForwardsWithALPN(t *testing.T) {
	for _, tc := range []struct {
		name         string
		forwardALPN  *gototls.ALPN
		wantForwardP int
	}{
		{"forward listener without ALPN", nil, 0},
		{"forward listener with ALPN protocols", &gototls.ALPN{Protos: []string{"h2", "http/1.1"}}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubListenerFuncs(t)
			prevServer := httpServer
			httpServer = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "forwarded") })}
			t.Cleanup(func() { httpServer = prevServer })
			port := tcpTestPort()
			l := registerAsDefaultListener(t, port)
			l.ALPN = &gototls.ALPN{}
			l.ForwardListener = &listeners.Listener{Port: port + 1000, ALPN: tc.forwardALPN}
			addr := serveTCPOn(t, port)

			conn := dialTCP(t, addr)
			if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")); err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(conn)
			if !strings.Contains(string(b), "200 OK") || !strings.HasSuffix(string(b), "forwarded") {
				t.Errorf("response = %q, want the one of the HTTP server", b)
			}
			if got := len(l.ALPN.Protos); got != 0 {
				t.Errorf("the ALPN of the listener was given %d protocols by a connection, want none", got)
			}
		})
	}
}
