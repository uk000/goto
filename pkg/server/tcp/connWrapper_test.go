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

package tcp

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewWrappedConnReportsForwardedLocalPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, _ := ln.Accept()
		accepted <- c
	}()
	client, err := net.DialTimeout("tcp", ln.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var server net.Conn
	select {
	case server = <-accepted:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the connection to be accepted")
	}
	defer server.Close()

	w := NewWrappedConn(server, 9443)
	if got := w.LocalAddr().String(); got != "127.0.0.1:9443" {
		t.Errorf("LocalAddr = %q, want the host of the connection with the forwarded port", got)
	}
	if w.RemoteAddr().String() != client.LocalAddr().String() {
		t.Errorf("RemoteAddr = %v, want the client's %v", w.RemoteAddr(), client.LocalAddr())
	}
	// what is written to it goes to the connection
	go w.Write([]byte("ping"))
	client.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 4)
	if _, err := io.ReadFull(client, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("read = %q, %v", buf, err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	client.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := client.Read(buf); err != io.EOF {
		t.Errorf("read after Close = %v, want EOF as the connection was closed", err)
	}
}

func TestNewWrappedConnOnConnectionWithoutHostPort(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	w := NewWrappedConn(a, 8081)
	if got := w.LocalAddr().String(); !strings.HasSuffix(got, ":8081") {
		t.Errorf("LocalAddr = %q, want the forwarded port", got)
	}
}

func TestClosingListener(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	l := NewClosingListener(a)
	if l.Addr().String() != a.LocalAddr().String() {
		t.Errorf("Addr = %v, want the connection's %v", l.Addr(), a.LocalAddr())
	}
	if l.Listener() != net.Listener(l) {
		t.Error("Listener() must be the listener itself, the one that closes")
	}
	c, err := l.Accept()
	if err != nil || c != a {
		t.Fatalf("first Accept = %v, %v, want the connection", c, err)
	}
	if c, err := l.Accept(); err != io.EOF || c != nil {
		t.Errorf("second Accept = %v, %v, want io.EOF", c, err)
	}
	if err := l.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	b.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := b.Read(make([]byte, 1)); err != io.EOF {
		t.Errorf("read on the other side after Close = %v, want EOF as the connection was closed", err)
	}
}

func TestNonClosingListenerLeavesConnectionOpen(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	l := NewNonClosingListener(a)
	if l.Listener() != net.Listener(l) {
		t.Error("Listener() must be the listener itself, the one that doesn't close")
	}
	c, err := l.Accept()
	if err != nil || c != a {
		t.Fatalf("first Accept = %v, %v, want the connection", c, err)
	}
	if c, err := l.Accept(); err != io.EOF || c != nil {
		t.Errorf("second Accept = %v, %v, want io.EOF", c, err)
	}
	if err := l.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	// the connection still works
	go a.Write([]byte("x"))
	b.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1)
	if _, err := b.Read(buf); err != nil || buf[0] != 'x' {
		t.Errorf("the connection doesn't work after the non-closing listener was closed: %q, %v", buf, err)
	}
}

func TestAdapterListenerAcceptsTheConnectionOnlyOnce(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	l := NewNonClosingListener(a)
	const callers = 20
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted, eof := 0, 0
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := l.Accept()
			mu.Lock()
			defer mu.Unlock()
			if err == nil && c == a {
				accepted++
			} else if err == io.EOF {
				eof++
			}
		}()
	}
	wg.Wait()
	if accepted != 1 || eof != callers-1 {
		t.Errorf("accepted/EOF = %d/%d, want 1/%d", accepted, eof, callers-1)
	}
}

// The adapters serve a connection that has been accepted already with an HTTP server, which must not close it when it is
// done accepting.
func TestNonClosingListenerServesHTTPOnAcceptedConnection(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "2")
		w.Write([]byte("ok"))
	})}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(NewNonClosingListener(a).Listener()) }()

	b.SetDeadline(time.Now().Add(5 * time.Second))
	go func() { io.WriteString(b, "GET / HTTP/1.1\r\nHost: x\r\n\r\n") }()
	resp, err := http.ReadResponse(bufio.NewReader(b), nil)
	if err != nil {
		t.Fatalf("reading the response: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "ok" {
		t.Errorf("response = %d %q", resp.StatusCode, body)
	}
	select {
	case err := <-served:
		if err != io.EOF {
			t.Errorf("Serve returned %v, want io.EOF as the listener has no more connections", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve didn't return after the only connection was accepted")
	}
	// a second request on the same connection works: the server's end was not closed when Serve returned
	go func() { io.WriteString(b, "GET / HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n") }()
	resp, err = http.ReadResponse(bufio.NewReader(b), nil)
	if err != nil {
		t.Fatalf("the connection was closed when the server stopped accepting: %v", err)
	}
	resp.Body.Close()
}
