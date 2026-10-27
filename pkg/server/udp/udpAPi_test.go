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

package udp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goto/pkg/global"
	"goto/pkg/server/listeners"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
)

const wait = 5 * time.Second

var setupOnce sync.Once

func setup() {
	setupOnce.Do(func() {
		global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
		global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
		global.Funcs.CloseConnectionsForPort = func(int) {}
		util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
		// Wires the listener functions that the validation of the requests uses, as the server does.
		listeners.Init()
		Middleware.SetRoutes(nil)
	})
}

// call makes a request to the API of the package.
func call(method, target string) *httptest.ResponseRecorder {
	setup()
	req := httptest.NewRequest(method, target, nil)
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, 8080))
	_, req, _ = util.WithRequestStore(req, nil)
	rr := httptest.NewRecorder()
	middleware.RootRouters["/server"].ServeHTTP(rr, req)
	return rr
}

// freeUDPPort gives a port that nothing is using now.
func freeUDPPort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}

// forget closes what the test opened on the port, and removes the listener of it.
func forget(t *testing.T, port int) {
	t.Helper()
	t.Cleanup(func() {
		if l := listeners.GetListenerForPort(port); l != nil && l != listeners.DefaultListener {
			l.Close()
			listeners.RemoveListener(l)
		}
	})
}

// storeListener adds a listener of the protocol that is not opened.
func storeListener(t *testing.T, protocol string) int {
	t.Helper()
	setup()
	port := freeUDPPort(t)
	forget(t, port)
	if err, msg := listeners.AddOrUpdateListener(&listeners.Listener{Port: port, Protocol: protocol}); err != nil {
		t.Fatalf("cannot add the %s listener: %v %s", protocol, err, msg)
	}
	return port
}

// echoUpstream is a UDP server that answers every packet with it, and a prefix. It counts the packets it gets.
type echoUpstream struct {
	addr    string
	packets atomic.Int32
}

func startEchoUpstream(t *testing.T) *echoUpstream {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	u := &echoUpstream{addr: conn.LocalAddr().String()}
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 2048)
		for {
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			u.packets.Add(1)
			conn.WriteToUDP(append([]byte("echo:"), buf[:n]...), from)
		}
	}()
	t.Cleanup(func() {
		conn.Close()
		select {
		case <-done:
		case <-time.After(wait):
			t.Error("the upstream did not stop")
		}
	})
	return u
}

// exchange sends a packet to the port and gives what comes back, and how long it took.
func exchange(t *testing.T, port int, msg string, timeout time.Duration) (string, time.Duration, error) {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(timeout))
	start := time.Now()
	if _, err := c.WriteToUDP([]byte(msg), &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2048)
	n, _, err := c.ReadFromUDP(buf)
	return string(buf[:n]), time.Since(start), err
}

func TestProxyRejectsAPortOrAnUpstreamThatIsNotOne(t *testing.T) {
	setup()
	for _, tt := range []struct{ port, upstream, wantMsg string }{
		{"0", "127.0.0.1:9", "Invalid port [0] or upstream address [127.0.0.1:9]"},
		{"-5", "127.0.0.1:9", "Invalid port [-5] or upstream address [127.0.0.1:9]"},
		{"abc", "127.0.0.1:9", "Invalid port [0] or upstream address [127.0.0.1:9]"},
		{"70000", "127.0.0.1:9", "Failed to open UDP listener on port [70000]"},
	} {
		for _, target := range []string{
			fmt.Sprintf("/server/udp/%s/proxy/%s", tt.port, tt.upstream),
			fmt.Sprintf("/server/udp/%s/proxy/%s/delay/10ms", tt.port, tt.upstream),
		} {
			rr := call("POST", target)
			if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), tt.wantMsg) {
				t.Errorf("POST %s = %d %q, want 400 with %q", target, rr.Code, rr.Body.String(), tt.wantMsg)
			}
		}
	}
	if l := listeners.GetListenerForPort(70000); l != nil {
		t.Errorf("listener for port 70000 = %+v, want none to be made", l)
	}
}

func TestDelayAndStopNeedAListener(t *testing.T) {
	port := freeUDPPort(t)
	for _, target := range []string{
		fmt.Sprintf("/server/udp/%d/delay/127.0.0.1:9/10ms", port),
		fmt.Sprintf("/server/udp/%d/stop/127.0.0.1:9", port),
	} {
		rr := call("POST", target)
		if want := fmt.Sprintf("No listener for port %d", port); rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), want) {
			t.Errorf("POST %s = %d %q, want 400 %q", target, rr.Code, rr.Body.String(), want)
		}
	}
}

func TestDelayAndStopNeedAUDPListener(t *testing.T) {
	port := storeListener(t, "http")
	for _, target := range []string{
		fmt.Sprintf("/server/udp/%d/delay/127.0.0.1:9/10ms", port),
		fmt.Sprintf("/server/udp/%d/stop/127.0.0.1:9", port),
	} {
		rr := call("POST", target)
		if want := fmt.Sprintf("Port %d is not a UDP listener", port); rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), want) {
			t.Errorf("POST %s = %d %q, want 400 %q", target, rr.Code, rr.Body.String(), want)
		}
	}
}

func TestSetDelayOfAUDPListener(t *testing.T) {
	port := storeListener(t, "udp")
	if l := listeners.GetListenerForPort(port); l == nil || !l.IsUDP {
		t.Fatalf("listener = %+v, want a UDP listener", l)
	}
	for _, delay := range []string{"10ms", "10ms-50ms", "50ms-10ms", "1s:3"} {
		rr := call("POST", fmt.Sprintf("/server/udp/%d/delay/127.0.0.1:9/%s", port, delay))
		if want := fmt.Sprintf("Delay configured for UDP port [%d]\n", port); rr.Code != http.StatusOK || rr.Body.String() != want {
			t.Errorf("delay %s = %d %q, want 200 %q", delay, rr.Code, rr.Body.String(), want)
		}
	}
	for _, delay := range []string{"abc", "10", "10ms-x", "-", "5parsecs"} {
		rr := call("POST", fmt.Sprintf("/server/udp/%d/delay/127.0.0.1:9/%s", port, delay))
		if want := fmt.Sprintf("Invalid delay value [%s]\n", delay); rr.Code != http.StatusBadRequest || rr.Body.String() != want {
			t.Errorf("delay %s = %d %q, want 400 %q", delay, rr.Code, rr.Body.String(), want)
		}
	}
}

func TestStopOfAUDPListener(t *testing.T) {
	port := storeListener(t, "udp")
	rr := call("POST", fmt.Sprintf("/server/udp/%d/stop/127.0.0.1:9", port))
	if want := fmt.Sprintf("Stopped Proxying UDP on port [%d] to upstream [127.0.0.1:9]\n", port); rr.Code != http.StatusOK || rr.Body.String() != want {
		t.Errorf("stop = %d %q, want 200 %q", rr.Code, rr.Body.String(), want)
	}
}

func TestRoutesAreOnlyThoseOfTheAPI(t *testing.T) {
	port := storeListener(t, "udp")
	for _, tt := range []struct{ method, target string }{
		{"GET", fmt.Sprintf("/server/udp/%d/proxy/127.0.0.1:9", port)},
		{"PUT", fmt.Sprintf("/server/udp/%d/proxy/127.0.0.1:9", port)},
		{"GET", fmt.Sprintf("/server/udp/%d/stop/127.0.0.1:9", port)},
		{"GET", fmt.Sprintf("/server/udp/%d/delay/127.0.0.1:9/10ms", port)},
		{"POST", fmt.Sprintf("/server/udp/%d/delay/10ms", port)},
		{"POST", fmt.Sprintf("/server/udp/%d", port)},
		{"POST", "/server/udp"},
	} {
		if rr := call(tt.method, tt.target); rr.Code != http.StatusNotFound && rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d %q, want it not to be a route", tt.method, tt.target, rr.Code, rr.Body.String())
		}
	}
}

func TestProxyToAPortThatIsTakenFails(t *testing.T) {
	setup()
	taken, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	port := taken.LocalAddr().(*net.UDPAddr).Port
	forget(t, port)
	rr := call("POST", fmt.Sprintf("/server/udp/%d/proxy/127.0.0.1:9", port))
	if want := fmt.Sprintf("Failed to open UDP listener on port [%d]", port); rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), want) {
		t.Errorf("proxy = %d %q, want 400 %q", rr.Code, rr.Body.String(), want)
	}
}

func TestProxyForwardsPacketsToTheUpstreamUntilItIsStopped(t *testing.T) {
	setup()
	upstream := startEchoUpstream(t)
	port := freeUDPPort(t)
	forget(t, port)

	rr := call("POST", fmt.Sprintf("/server/udp/%d/proxy/%s", port, upstream.addr))
	want := fmt.Sprintf("Proxying UDP on port [%d] to upstream [%s] with delay [0s-0s]\n", port, upstream.addr)
	if rr.Code != http.StatusOK || rr.Body.String() != want {
		t.Fatalf("proxy = %d %q, want 200 %q", rr.Code, rr.Body.String(), want)
	}
	if l := listeners.GetListenerForPort(port); l == nil || !l.IsUDP || !l.Open {
		t.Fatalf("listener = %+v, want it to be an open UDP listener", l)
	}
	for _, msg := range []string{"ping", "another packet", strings.Repeat("x", 1000)} {
		got, _, err := exchange(t, port, msg, wait)
		if err != nil || got != "echo:"+msg {
			t.Errorf("packet of %d bytes: answer = %d bytes (%v), want the answer of the upstream", len(msg), len(got), err)
		}
	}
	if got := upstream.packets.Load(); got != 3 {
		t.Errorf("upstream got %d packets, want 3", got)
	}

	// Asking again to proxy to what is proxied is fine.
	if rr := call("POST", fmt.Sprintf("/server/udp/%d/proxy/%s", port, upstream.addr)); rr.Code != http.StatusOK {
		t.Errorf("proxy again = %d %q, want 200", rr.Code, rr.Body.String())
	}

	rr = call("POST", fmt.Sprintf("/server/udp/%d/stop/%s", port, upstream.addr))
	if rr.Code != http.StatusOK {
		t.Fatalf("stop = %d %q, want 200", rr.Code, rr.Body.String())
	}
	if got, _, err := exchange(t, port, "after the stop", 500*time.Millisecond); err == nil {
		t.Errorf("answer after the stop = %q, want none", got)
	}
	if got := upstream.packets.Load(); got != 3 {
		t.Errorf("upstream got %d packets, want none after the stop", got)
	}

	// The upstream is proxied again when it is asked for again.
	if rr := call("POST", fmt.Sprintf("/server/udp/%d/proxy/%s", port, upstream.addr)); rr.Code != http.StatusOK {
		t.Fatalf("proxy after the stop = %d %q, want 200", rr.Code, rr.Body.String())
	}
	if got, _, err := exchange(t, port, "back again", wait); err != nil || got != "echo:back again" {
		t.Errorf("answer = %q (%v), want the answer of the upstream", got, err)
	}
}

func TestProxyWithADelayDelaysTheAnswers(t *testing.T) {
	setup()
	upstream := startEchoUpstream(t)
	port := freeUDPPort(t)
	forget(t, port)

	rr := call("POST", fmt.Sprintf("/server/udp/%d/proxy/%s/delay/300ms", port, upstream.addr))
	if want := "with delay [300ms-300ms]"; rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), want) {
		t.Fatalf("proxy = %d %q, want 200 with %q", rr.Code, rr.Body.String(), want)
	}
	got, took, err := exchange(t, port, "slow", wait)
	if err != nil || got != "echo:slow" {
		t.Fatalf("answer = %q (%v), want the answer of the upstream", got, err)
	}
	if took < 250*time.Millisecond {
		t.Errorf("the answer took %v, want it to be delayed by 300ms", took)
	}
}

func TestSetDelayOfAProxyThatIsRunning(t *testing.T) {
	setup()
	upstream := startEchoUpstream(t)
	port := freeUDPPort(t)
	forget(t, port)
	if rr := call("POST", fmt.Sprintf("/server/udp/%d/proxy/%s", port, upstream.addr)); rr.Code != http.StatusOK {
		t.Fatalf("proxy = %d %q, want 200", rr.Code, rr.Body.String())
	}
	if _, took, err := exchange(t, port, "fast", wait); err != nil || took >= 250*time.Millisecond {
		t.Fatalf("without a delay: took %v (%v), want it to be quick", took, err)
	}
	if rr := call("POST", fmt.Sprintf("/server/udp/%d/delay/%s/300ms", port, upstream.addr)); rr.Code != http.StatusOK {
		t.Fatalf("delay = %d %q, want 200", rr.Code, rr.Body.String())
	}
	got, took, err := exchange(t, port, "slow", wait)
	if err != nil || got != "echo:slow" || took < 250*time.Millisecond {
		t.Errorf("with the delay: answer = %q, took %v (%v), want the answer of the upstream after the delay", got, took, err)
	}
}

func TestConcurrentConfigurationWhileProxying(t *testing.T) {
	setup()
	upstream := startEchoUpstream(t)
	port := freeUDPPort(t)
	forget(t, port)
	if rr := call("POST", fmt.Sprintf("/server/udp/%d/proxy/%s", port, upstream.addr)); rr.Code != http.StatusOK {
		t.Fatalf("proxy = %d %q, want 200", rr.Code, rr.Body.String())
	}
	var wg sync.WaitGroup
	var failures atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				if rr := call("POST", fmt.Sprintf("/server/udp/%d/delay/%s/%dms", port, upstream.addr, 1+j)); rr.Code != http.StatusOK {
					failures.Add(1)
				}
				if rr := call("POST", fmt.Sprintf("/server/udp/%d/proxy/%s/delay/%dms", port, upstream.addr, 1+i)); rr.Code != http.StatusOK {
					failures.Add(1)
				}
			}
		}()
	}
	var answered atomic.Int32
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				failures.Add(1)
				return
			}
			defer c.Close()
			for j := 0; j < 5; j++ {
				c.SetDeadline(time.Now().Add(wait))
				msg := fmt.Sprintf("packet-%d-%d", i, j)
				if _, err := c.WriteToUDP([]byte(msg), &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}); err != nil {
					failures.Add(1)
					continue
				}
				buf := make([]byte, 256)
				if n, _, err := c.ReadFromUDP(buf); err == nil && string(buf[:n]) == "echo:"+msg {
					answered.Add(1)
				} else {
					failures.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if failures.Load() != 0 || answered.Load() != 20 {
		t.Errorf("failures = %d, answered = %d, want no failures and every packet answered with its own echo", failures.Load(), answered.Load())
	}
}
