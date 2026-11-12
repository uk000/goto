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

package util

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// udsTestPath gives a socket path that is short enough for the limit of the OS (about 100 characters), which the paths
// of t.TempDir() are not on some systems.
func udsTestPath(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "u")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, name)
}

func unixAddr(path string) net.Addr {
	return &net.UnixAddr{Name: path, Net: "unix"}
}

func TestListenUDS(t *testing.T) {
	path := udsTestPath(t, "a.sock")
	ln, err := ListenUDS(path)
	if err != nil {
		t.Fatalf("ListenUDS(%q) failed: %v", path, err)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode()&os.ModeSocket == 0 {
		t.Fatalf("stat = %v, %v, want a socket file at the path", fi, err)
	}
	if perm := fi.Mode().Perm(); perm != 0660 {
		t.Errorf("socket permissions = %o, want 660", perm)
	}
	accepted := make(chan net.Conn, 1)
	go func() {
		if c, err := ln.Accept(); err == nil {
			accepted <- c
		}
	}()
	c, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		t.Fatalf("cannot dial the socket: %v", err)
	}
	defer c.Close()
	select {
	case s := <-accepted:
		s.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("the connection was not accepted")
	}
	ln.Close()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("stat after close = %v, want the socket file to be removed", err)
	}
}

func TestListenUDSReplacesStaleSocket(t *testing.T) {
	path := udsTestPath(t, "stale.sock")
	stale, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	// Closing without removing the file leaves the socket of a process that died.
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	stale.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the stale socket file is not there: %v", err)
	}
	ln, err := ListenUDS(path)
	if err != nil {
		t.Fatalf("ListenUDS over a stale socket failed: %v", err)
	}
	ln.Close()
}

func TestListenUDSKeepsSocketThatIsInUse(t *testing.T) {
	path := udsTestPath(t, "live.sock")
	live, err := ListenUDS(path)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	if ln, err := ListenUDS(path); err == nil {
		ln.Close()
		t.Fatal("a socket that is in use must not be replaced")
	} else if !strings.Contains(err.Error(), "in use") {
		t.Errorf("error = %q, want it to say that the socket is in use", err.Error())
	}
	accepted := make(chan net.Conn, 1)
	go func() {
		if c, err := live.Accept(); err == nil {
			accepted <- c
		}
	}()
	c, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		t.Fatalf("the socket in use can't be dialed after the attempt to replace it: %v", err)
	}
	c.Close()
	select {
	case s := <-accepted:
		s.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("the socket in use no longer gets connections")
	}
}

func TestListenUDSRejectsBadPaths(t *testing.T) {
	if ln, err := ListenUDS(""); err == nil {
		ln.Close()
		t.Error("an empty path must be rejected")
	}
	file := udsTestPath(t, "regular")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if ln, err := ListenUDS(file); err == nil {
		ln.Close()
		t.Error("a path of a file that is not a socket must be rejected")
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "x" {
		t.Errorf("the file that is not a socket was changed: %q, %v", data, err)
	}
	if ln, err := ListenUDS(filepath.Join(file, "child.sock")); err == nil {
		ln.Close()
		t.Error("a path under a file must be rejected")
	}
}

func TestUnregisterUDSPort(t *testing.T) {
	path := udsTestPath(t, "u.sock")
	RegisterUDSPort(path, 9093)
	UnregisterUDSPort(path, 9094)
	if got := GetUDSPort(unixAddr(path)); got != 9093 {
		t.Errorf("GetUDSPort after the unregistering of another port = %d, want 9093", got)
	}
	UnregisterUDSPort(path, 9093)
	if got := GetUDSPort(unixAddr(path)); got != 0 {
		t.Errorf("GetUDSPort after the unregistering = %d, want 0", got)
	}
	UnregisterUDSPort(path, 9093)
}

func TestUDSPorts(t *testing.T) {
	path := udsTestPath(t, "p.sock")
	if got := GetUDSPort(unixAddr(path)); got != 0 {
		t.Errorf("GetUDSPort of a path that is not registered = %d, want 0", got)
	}
	RegisterUDSPort(path, 9091)
	if got := GetUDSPort(unixAddr(path)); got != 9091 {
		t.Errorf("GetUDSPort = %d, want 9091", got)
	}
	RegisterUDSPort(path, 9092)
	if got := GetUDSPort(unixAddr(path)); got != 9092 {
		t.Errorf("GetUDSPort after the path was registered again = %d, want 9092", got)
	}
	if got := GetUDSPort(&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9091}); got != 0 {
		t.Errorf("GetUDSPort of a TCP address = %d, want 0", got)
	}
	if got := GetUDSPort(nil); got != 0 {
		t.Errorf("GetUDSPort of no address = %d, want 0", got)
	}
}

func TestIsUnixAddr(t *testing.T) {
	if !IsUnixAddr(unixAddr("/tmp/x.sock")) {
		t.Error("a unix address must be recognized")
	}
	if IsUnixAddr(&net.TCPAddr{}) || IsUnixAddr(&net.UDPAddr{}) || IsUnixAddr(nil) {
		t.Error("addresses that are not unix must not be recognized")
	}
}

func TestWithConnGivesTheRegisteredPortOfASocket(t *testing.T) {
	path := udsTestPath(t, "c.sock")
	ln, err := ListenUDS(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		if c, err := ln.Accept(); err == nil {
			accepted <- c
		}
	}()
	client, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var server net.Conn
	select {
	case server = <-accepted:
		defer server.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("the connection was not accepted")
	}

	// A socket that is not registered has no port to give.
	ctx := WithConn(context.Background(), server)
	if ctx.Value(CurrentPortKey) != nil {
		t.Errorf("port = %v, want none for a socket that is not registered", ctx.Value(CurrentPortKey))
	}
	if ctx.Value(ConnectionKey) != server {
		t.Error("the connection must be in the context")
	}

	RegisterUDSPort(path, 9091)
	ctx = WithConn(context.Background(), server)
	if port, _ := ctx.Value(CurrentPortKey).(int); port != 9091 {
		t.Errorf("port = %v, want 9091, the port that the socket is registered for", ctx.Value(CurrentPortKey))
	}
}

func TestPortOfRequestsOnASocket(t *testing.T) {
	// A path may have what looks like a port in it, which is not one.
	addr := unixAddr("/tmp/goto:81.sock")
	ctx := context.WithValue(context.Background(), http.LocalAddrContextKey, addr)
	if got := GetPortValueFromLocalAddressContext(ctx); got != "" {
		t.Errorf("GetPortValueFromLocalAddressContext = %q, want none for a unix address", got)
	}
	ctx = context.WithValue(ctx, CurrentPortKey, 7070)
	if got := GetContextPort(ctx); got != 7070 {
		t.Errorf("GetContextPort = %d, want 7070, the port that the connection was given", got)
	}
	r := httptest.NewRequest("GET", "/x", nil).WithContext(ctx)
	if got := GetConnectionPortNum(r); got != 7070 {
		t.Errorf("GetConnectionPortNum = %d, want 7070 and not a port made of the path", got)
	}
}
