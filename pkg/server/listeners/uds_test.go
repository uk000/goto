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

package listeners

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"goto/pkg/global"
	"goto/pkg/util"
)

// udsPath gives a socket path that is short enough for the limit of the OS (about 100 characters), which the paths of
// t.TempDir() are not on some systems.
func udsPath(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "u")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, name)
}

// newUDSListener gives a listener of the protocol on a free port that is to be opened on the socket path.
func newUDSListener(t *testing.T, protocol, path string) *Listener {
	t.Helper()
	setup(t)
	l := newListener(freePort(t), protocol, "l.test", true)
	l.UDS = path
	forget(t, l.Port)
	return l
}

func dialUDS(t *testing.T, path string) net.Conn {
	t.Helper()
	c, err := net.DialTimeout("unix", path, dialTimeout)
	if err != nil {
		t.Fatalf("cannot dial socket %s: %v", path, err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func udsClient(path string) *http.Client {
	return &http.Client{
		Timeout: ioTimeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", path)
			},
			DisableKeepAlives: true,
		},
	}
}

func TestHTTPListenerOnUDS(t *testing.T) {
	path := udsPath(t, "h.sock")
	l := newUDSListener(t, "http", path)
	err, msg := AddOrUpdateListener(l)
	if err != nil {
		t.Fatalf("AddOrUpdateListener failed: %s", msg)
	}
	if want := fmt.Sprintf("Listener %d added and opened on UDS %s.", l.Port, path); msg != want {
		t.Errorf("message = %q, want %q", msg, want)
	}
	if !l.Open {
		t.Fatal("the listener is not open")
	}
	if isListening(l.Port) {
		t.Errorf("port %d is bound, want the listener on the socket only", l.Port)
	}
	if got := util.GetUDSPort(&net.UnixAddr{Name: path, Net: "unix"}); got != l.Port {
		t.Errorf("port of the socket = %d, want %d", got, l.Port)
	}
	resp, err := udsClient(path).Get("http://goto/foo")
	if err != nil {
		t.Fatalf("request over the socket failed: %v", err)
	}
	defer resp.Body.Close()
	if body, _ := io.ReadAll(resp.Body); resp.StatusCode != http.StatusOK || string(body) != "served" {
		t.Errorf("response = %d %q, want 200 served", resp.StatusCode, body)
	}
	if GetListenerForPort(l.Port) != l {
		t.Error("the listener must be stored under its port")
	}

	l.Close()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("stat after close = %v, want the socket file to be removed", err)
	}
	if c, err := net.DialTimeout("unix", path, dialTimeout); err == nil {
		c.Close()
		t.Error("the socket accepts connections after the listener was closed")
	}
}

func TestTCPListenerOnUDS(t *testing.T) {
	path := udsPath(t, "t.sock")
	l := newUDSListener(t, "tcp", path)
	if err, msg := AddOrUpdateListener(l); err != nil {
		t.Fatalf("AddOrUpdateListener failed: %s", msg)
	}
	c := dialUDS(t, path)
	c.SetDeadline(time.Now().Add(ioTimeout))
	if _, err := c.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 16)
	if n, _ := c.Read(buf); string(buf[:n]) != "hello" {
		t.Errorf("echo = %q, want hello", buf[:n])
	}
}

func TestReopenListenerOnUDS(t *testing.T) {
	path := udsPath(t, "r.sock")
	l := newUDSListener(t, "http", path)
	if err, msg := AddOrUpdateListener(l); err != nil {
		t.Fatalf("AddOrUpdateListener failed: %s", msg)
	}
	// A listener added again for the port takes the socket over from the one that it replaces.
	l2 := newListener(l.Port, "http", "l.test", true)
	l2.UDS = path
	if err, msg := AddOrUpdateListener(l2); err != nil {
		t.Fatalf("AddOrUpdateListener of the replacement failed: %s", msg)
	}
	resp, err := udsClient(path).Get("http://goto/foo")
	if err != nil {
		t.Fatalf("request over the socket after the reopen failed: %v", err)
	}
	resp.Body.Close()
	if GetListenerForPort(l.Port) != l2 {
		t.Error("the replacement must be the listener of the port")
	}
}

func TestListenerOnUDSFailsToOpenOnAPathThatIsNotASocket(t *testing.T) {
	path := udsPath(t, "file")
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	l := newUDSListener(t, "http", path)
	l.Open = true
	if l.openListener(true) {
		t.Error("the listener opened on a path of a file that is not a socket")
	}
	if l.Open {
		t.Error("the listener must not be open")
	}
	if data, _ := os.ReadFile(path); string(data) != "x" {
		t.Errorf("the file was changed: %q", data)
	}
}

func TestValidateUDS(t *testing.T) {
	setup(t)
	path := udsPath(t, "v.sock")
	other := udsPath(t, "o.sock")

	udp := newListener(freeUDPPort(t), "udp", "", false)
	udp.UDS = path
	udp.assignProtocol()
	if msg := validateUDS(udp); !strings.Contains(msg, "UDP") {
		t.Errorf("validateUDS of a UDP listener = %q, want it rejected", msg)
	}
	if err, msg := AddOrUpdateListener(udp); err == nil || !strings.Contains(msg, "UDS") {
		t.Errorf("AddOrUpdateListener of a UDP listener with a socket = %v, %q, want it rejected", err, msg)
	}

	l := newUDSListener(t, "http", path)
	l.assignProtocol()
	if msg := validateUDS(l); msg != "" {
		t.Errorf("validateUDS = %q, want a path that is free to be accepted", msg)
	}

	before := global.ServerConfig.UDSPath
	global.ServerConfig.UDSPath = path
	t.Cleanup(func() { global.ServerConfig.UDSPath = before })
	if msg := validateUDS(l); !strings.Contains(msg, "server") {
		t.Errorf("validateUDS of the path of the server = %q, want it rejected", msg)
	}
	global.ServerConfig.UDSPath = other

	if err, msg := AddOrUpdateListener(l); err != nil {
		t.Fatalf("AddOrUpdateListener failed: %s", msg)
	}
	// The listener may be updated with the path that it has, but another port can't take the path.
	if msg := validateUDS(l); msg != "" {
		t.Errorf("validateUDS of the listener that has the path = %q, want it accepted", msg)
	}
	l2 := newUDSListener(t, "http", path)
	l2.assignProtocol()
	if msg := validateUDS(l2); !strings.Contains(msg, "already used") {
		t.Errorf("validateUDS of a path of another listener = %q, want it rejected", msg)
	}
	if err, _ := AddOrUpdateListener(l2); err == nil {
		t.Error("a listener must not be added on the path of another listener")
	}
	if GetListenerForPort(l2.Port) != nil {
		t.Error("the rejected listener must not be stored")
	}
}

func TestCloseUDSListenersClosesOnlyTheListenersOnSockets(t *testing.T) {
	setup(t)
	httpPath, tcpPath := udsPath(t, "h.sock"), udsPath(t, "t.sock")
	onHTTP := newUDSListener(t, "http", httpPath)
	onTCP := newUDSListener(t, "tcp", tcpPath)
	for _, l := range []*Listener{onHTTP, onTCP} {
		if err, msg := AddOrUpdateListener(l); err != nil {
			t.Fatalf("AddOrUpdateListener failed: %s", msg)
		}
	}
	onPort := addAndOpen(t, "tcp")

	CloseUDSListeners()

	for _, path := range []string{httpPath, tcpPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("stat %s = %v, want the socket file removed", path, err)
		}
	}
	if onHTTP.isListening() || onTCP.isListening() {
		t.Error("the listeners on sockets must be closed")
	}
	if !onPort.isListening() || !isListening(onPort.Port) {
		t.Error("a listener on a TCP port must be left as it is")
	}
	// Closing again, as when the servers have closed some of them already, is fine.
	CloseUDSListeners()
}

func TestDiffTellsOfAChangedUDS(t *testing.T) {
	l1 := newListener(8000, "http", "l.test", true)
	l2 := newListener(8000, "http", "l.test", true)
	if Diff(l1, l2) {
		t.Fatal("listeners that are the same must not differ")
	}
	l2.UDS = "/tmp/goto.sock"
	if !Diff(l1, l2) {
		t.Error("a listener that has a socket must differ from one that has none")
	}
	l1.UDS = "/tmp/goto.sock"
	if Diff(l1, l2) {
		t.Error("listeners with the same socket must not differ")
	}
	l1.UDS = "/tmp/other.sock"
	if !Diff(l1, l2) {
		t.Error("listeners with different sockets must differ")
	}
}

func TestListenerJSONHasUDSWhenSet(t *testing.T) {
	l := newListener(8000, "http", "l.test", false)
	data, _ := json.Marshal(l)
	if strings.Contains(string(data), `"uds"`) {
		t.Errorf("json = %s, want no uds for a listener without a socket", data)
	}
	l.UDS = "/tmp/goto.sock"
	data, _ = json.Marshal(l)
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil || out["uds"] != "/tmp/goto.sock" {
		t.Errorf("json = %s, want uds /tmp/goto.sock", data)
	}
	parsed := newListener(0, "", "", false)
	if err := util.ReadJson(`{"port":9091, "protocol":"grpc", "uds":"/tmp/goto-9091.sock", "open":true}`, parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Port != 9091 || parsed.UDS != "/tmp/goto-9091.sock" || parsed.Protocol != "grpc" {
		t.Errorf("parsed = %d %q %q, want the port, socket and protocol of the payload", parsed.Port, parsed.UDS, parsed.Protocol)
	}
}

// logCapture holds what is logged, which goroutines of other tests may log to as well.
type logCapture struct {
	lock sync.Mutex
	buf  bytes.Buffer
}

func (c *logCapture) Write(p []byte) (int, error) {
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.buf.Write(p)
}

// line gives the logged line that has the text, or "" if there is none.
func (c *logCapture) line(text string) string {
	c.lock.Lock()
	defer c.lock.Unlock()
	for _, line := range strings.Split(c.buf.String(), "\n") {
		if strings.Contains(line, text) {
			return line
		}
	}
	return ""
}

func TestCloseLogsTheUDSOfAListener(t *testing.T) {
	capture := &logCapture{}
	prev := log.Writer()
	log.SetOutput(capture)
	t.Cleanup(func() { log.SetOutput(prev) })

	plain := newListener(8001, "http", "l.test", false)
	plain.Close()
	if line := capture.line("Closing http listener: 8001"); line == "" || strings.Contains(line, "UDS") {
		t.Errorf("log line = %q, want the close of a listener without a socket logged without one", line)
	}
	withUDS := newListener(8002, "http", "l.test", false)
	withUDS.UDS = "/tmp/goto.sock"
	withUDS.Close()
	if line := capture.line("Closing http listener: 8002"); !strings.Contains(line, "(UDS: /tmp/goto.sock)") {
		t.Errorf("log line = %q, want the close of a listener with a socket logged with it", line)
	}
}
