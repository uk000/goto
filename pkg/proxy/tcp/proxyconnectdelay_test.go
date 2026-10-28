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
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"goto/pkg/types"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// setDelayForTest sets the delay of the CONNECT proxy of the port, and takes it away at the end of the test, as the
// harness waits for the tunnels of the proxy to be over.
func setDelayForTest(t *testing.T, port int, delay *types.Delay) {
	t.Helper()
	SetConnectDelay(port, delay)
	t.Cleanup(func() { SetConnectDelay(port, nil) })
}

func connectDelayOf(port int) *types.Delay {
	p := GetPortProxy(port)
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.HTTP.ConnectDelay
}

func TestConnectDelayAPI(t *testing.T) {
	adminPort := apiPort(t)
	port := apiPort(t)
	// the API sets the delay of the port that the request names, and may be used before the mode is on
	for _, method := range []string{"POST", "PUT"} {
		rr := callWith(t, adminPort, port, method, connectAPI+"delay/200ms:3", "")
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Port ["+fmt.Sprint(port)+"]: CONNECT proxy delay set to [200ms-200ms], count [3]") {
			t.Fatalf("%s: status = %d, body %q", method, rr.Code, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), "once CONNECT proxy mode is enabled") {
			t.Errorf("%s: body %q doesn't say that the mode is not on", method, rr.Body.String())
		}
		d := connectDelayOf(port)
		if d == nil || d.Min.Duration != 200*time.Millisecond || d.Max.Duration != 200*time.Millisecond || d.Count != 3 {
			t.Errorf("%s: delay = %s count %d", method, d, d.Count)
		}
	}
	if WillProxyConnect(port) || WillProxyTCP(port) {
		t.Error("a delay made the port a proxy port")
	}
	// the count is optional, and the delay may be a range
	rr := callWith(t, adminPort, port, "POST", connectAPI+"delay/100ms-300ms", "")
	if d := connectDelayOf(port); rr.Code != http.StatusOK || d == nil || d.Min.Duration != 100*time.Millisecond || d.Max.Duration != 300*time.Millisecond || d.Count != -1 {
		t.Errorf("status = %d, body %q, delay = %s", rr.Code, rr.Body.String(), d)
	} else if !strings.Contains(rr.Body.String(), "count [unlimited]") {
		t.Errorf("body %q", rr.Body.String())
	}
	// and it's on in the mode that the port is in: the report of the proxy has it
	EnableConnect(port, true)
	defer EnableConnect(port, false)
	rr = callWith(t, adminPort, port, "POST", connectAPI+"delay/1s:2", "")
	if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), "once CONNECT proxy mode") {
		t.Errorf("status = %d, body %q", rr.Code, rr.Body.String())
	}
	b, err := json.Marshal(GetPortProxy(port))
	if err != nil {
		t.Fatal(err)
	}
	// both are reported under the http part of the proxy
	var got struct {
		HTTP struct {
			Connect      bool
			ConnectDelay map[string]any
		}
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if !got.HTTP.Connect || got.HTTP.ConnectDelay["min"] != "1s" || got.HTTP.ConnectDelay["max"] != "1s" || got.HTTP.ConnectDelay["count"] != float64(2) {
		t.Errorf("proxy = %s", b)
	}
}

func TestConnectDelayAPITakesTheDelayAway(t *testing.T) {
	adminPort := apiPort(t)
	port := apiPort(t)
	for _, clear := range []string{"0s", "0s:5", "0ms-0s", "clear", "CLEAR"} {
		callWith(t, adminPort, port, "POST", connectAPI+"delay/1s:4", "")
		if connectDelayOf(port) == nil {
			t.Fatal("the delay is not set")
		}
		rr := callWith(t, adminPort, port, "POST", connectAPI+"delay/"+clear, "")
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "CONNECT proxy delay cleared") || connectDelayOf(port) != nil {
			t.Errorf("%s: status = %d, body %q, delay = %v", clear, rr.Code, rr.Body.String(), connectDelayOf(port))
		}
	}
	// the report has no delay then
	b, _ := json.Marshal(GetPortProxy(port))
	if strings.Contains(string(b), "connectDelay") {
		t.Errorf("proxy = %s", b)
	}
	// taking it away from a port that has no proxy doesn't make one
	other := apiPort(t)
	if rr := callWith(t, adminPort, other, "POST", connectAPI+"delay/clear", ""); rr.Code != http.StatusOK {
		t.Errorf("status = %d, body %q", rr.Code, rr.Body.String())
	}
	proxyLock.RLock()
	_, made := portProxy[other]
	proxyLock.RUnlock()
	if made {
		t.Error("clearing the delay of a port made a proxy for it")
	}
}

func TestConnectDelayAPIRejectsInvalidDelays(t *testing.T) {
	adminPort := apiPort(t)
	port := apiPort(t)
	callWith(t, adminPort, port, "POST", connectAPI+"delay/1s:4", "")
	for _, bad := range []string{"abc", "100", "-1s", "1s-", "1s-abc"} {
		rr := callWith(t, adminPort, port, "POST", connectAPI+"delay/"+bad, "")
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Invalid delay value ["+bad+"]") {
			t.Errorf("%s: status = %d, body %q", bad, rr.Code, rr.Body.String())
		}
		if d := connectDelayOf(port); d == nil || d.Count != 4 {
			t.Errorf("%s: the delay that was set was changed to %v", bad, d)
		}
	}
}

func TestConnectDelaysTheConnection(t *testing.T) {
	forPlainAndTLS(t, func(t *testing.T, isTLS bool) {
		echo := startEchoServer(t)
		h := newConnectHarness(t, isTLS)
		const delay = 150 * time.Millisecond
		setDelayForTest(t, h.port, types.NewDelay(delay, delay, 1))
		c := h.dial()
		start := time.Now()
		tc := openTunnel(t, c, echo.Addr(), "")
		if took := time.Since(start); took < delay {
			t.Errorf("the connection took %v, want it delayed by %v", took, delay)
		}
		// the count of the delay was for this one event
		if d := connectDelayOf(h.port); d.Count != 0 {
			t.Errorf("delay count = %d, want the one connection to have used it up", d.Count)
		}
		// the transfers are not delayed once the count is used up, which the delays that are counted say
		if got := roundTrip(t, tc, "hello"); got != "hello" {
			t.Errorf("echo = %q", got)
		}
		c.Close()
		session := onlySession(t, h.proxy(), echo.Addr())
		if session.Down.DelayCount != 1 || session.Ups[0].DelayCount != 0 {
			t.Errorf("delays counted: down %d, up %d, want 1 and 0", session.Down.DelayCount, session.Ups[0].DelayCount)
		}
	})
}

func TestConnectDelaysEveryTransferInBothDirections(t *testing.T) {
	forPlainAndTLS(t, func(t *testing.T, isTLS bool) {
		echo := startEchoServer(t)
		h := newConnectHarness(t, isTLS)
		const delay = 100 * time.Millisecond
		c := h.dial()
		tc := openTunnel(t, c, echo.Addr(), "")
		// a tunnel that is open is delayed from the moment that the delay is set
		roundTrip(t, tc, "fast")
		setDelayForTest(t, h.port, types.NewDelay(delay, delay, -1))
		for i := 1; i <= 2; i++ {
			start := time.Now()
			if got := roundTrip(t, tc, "slow"); got != "slow" {
				t.Errorf("echo = %q", got)
			}
			// what the client sends is held back, and so is what the destination answers
			if took := time.Since(start); took < 2*delay {
				t.Errorf("round trip %d took %v, want it to have both ways delayed by %v", i, took, delay)
			}
		}
		// and a tunnel that is open is not delayed anymore once the delay is taken away, which the delays that are counted say
		SetConnectDelay(h.port, nil)
		roundTrip(t, tc, "fast again")
		c.Close()
		session := onlySession(t, h.proxy(), echo.Addr())
		if session.Down.DelayCount != 2 || session.Ups[0].DelayCount != 2 {
			t.Errorf("delays counted: down %d, up %d, want 2 each", session.Down.DelayCount, session.Ups[0].DelayCount)
		}
	})
}

// A count is for the events of the port altogether: the connection, and the transfers of the tunnels that it has.
func TestConnectDelayCountIsSharedByConnectionAndTransfers(t *testing.T) {
	echo := startEchoServer(t)
	h := newConnectHarness(t, false)
	const delay = 50 * time.Millisecond
	setDelayForTest(t, h.port, types.NewDelay(delay, delay, 3))
	c := h.dial()
	start := time.Now()
	tc := openTunnel(t, c, echo.Addr(), "")
	for i := 0; i < 3; i++ {
		roundTrip(t, tc, "x")
	}
	// the connection, what is sent and what comes back are the 3 events that were delayed
	if took := time.Since(start); took < 3*delay {
		t.Errorf("took %v, want the 3 delays of %v", took, delay)
	}
	if d := connectDelayOf(h.port); d.Count != 0 {
		t.Errorf("delay count = %d", d.Count)
	}
	c.Close()
	session := onlySession(t, h.proxy(), echo.Addr())
	if got := session.Down.DelayCount + session.Ups[0].DelayCount; got != 3 {
		t.Errorf("%d events were delayed, want the 3 that were configured", got)
	}
	if session.Down.DelayCount != 2 || session.Ups[0].DelayCount != 1 {
		t.Errorf("delays counted: down %d, up %d, want 2 (the connection and the request) and 1 (the answer)", session.Down.DelayCount, session.Ups[0].DelayCount)
	}
}

// What the proxy makes of HTTP in a tunnel is not changed by the transfers being delayed, which come in pieces of their own.
func TestConnectDelayedTransfersOfHTTP(t *testing.T) {
	setViaLabels(t)
	dest := newHTTPDestination(t)
	h := newConnectHarness(t, false)
	setDelayForTest(t, h.port, types.NewDelay(20*time.Millisecond, 30*time.Millisecond, -1))
	c := openHTTPTunnel(t, h, dest)
	for i := 0; i < 3; i++ {
		c.send(get("/goto"))
		resp, body := c.read("GET")
		if body != "goto" {
			t.Errorf("body = %q", body)
		}
		expectVia(t, "the delayed response", resp, "upstream-goto", h.via())
	}
	c.send(get("/chunked"))
	if _, body := c.read("GET"); body != "abcde" {
		t.Errorf("chunked body = %q", body)
	}
}

// A delay that is being waited for is given up on when the tunnel is aborted, and the tunnel is not idle while it waits.
func TestConnectDelayTransferEndsWithTheTunnel(t *testing.T) {
	a, _ := net.Pipe()
	b, _ := net.Pipe()
	p := newTCPProxy(1)
	p.HTTP.ConnectDelay = types.NewDelay(time.Hour, time.Hour, -1)
	tn := &connectTunnel{port: 1, proxy: p, down: a, up: b}
	tn.ctx, tn.cancel = context.WithCancel(context.Background())
	tn.touch()
	done := make(chan bool, 1)
	go func() { done <- tn.delayTransfer(&ConnTracker{}, "test") }()
	time.Sleep(50 * time.Millisecond)
	tn.abort()
	select {
	case ok := <-done:
		if ok {
			t.Error("the delay of an aborted tunnel was waited for in full")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the delay is still waited for after the tunnel was aborted")
	}
}

func TestConnectDelayTransferKeepsTheTunnelFromBeingIdle(t *testing.T) {
	old := connectIdleTimeout
	connectIdleTimeout = 100 * time.Millisecond
	defer func() { connectIdleTimeout = old }()
	a, _ := net.Pipe()
	b, _ := net.Pipe()
	defer a.Close()
	defer b.Close()
	p := newTCPProxy(1)
	p.HTTP.ConnectDelay = types.NewDelay(400*time.Millisecond, 400*time.Millisecond, 1)
	tn := &connectTunnel{port: 1, proxy: p, down: a, up: b}
	tn.ctx, tn.cancel = context.WithCancel(context.Background())
	defer tn.cancel()
	tn.touch()
	idleSeen := make(chan time.Duration, 1)
	go func() {
		// what the other direction of the tunnel looks at while this one waits
		var longest time.Duration
		for end := time.Now().Add(350 * time.Millisecond); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
			if idle := tn.idleFor(); idle > longest {
				longest = idle
			}
		}
		idleSeen <- longest
	}()
	if !tn.delayTransfer(&ConnTracker{}, "test") {
		t.Fatal("the delay was given up on")
	}
	if longest := <-idleSeen; longest >= connectIdleTimeout {
		t.Errorf("the tunnel was idle for %v during the delay, which is as long as the idle timeout of %v", longest, connectIdleTimeout)
	}
}

// The delays that the proxy adds are told by the proxy: the one of the connection with the answer to CONNECT, and the
// ones of the transfers of an exchange with the response, which are in the time that the proxy took.
func TestConnectReportsDelaysItAdds(t *testing.T) {
	setViaLabels(t)
	dest := newHTTPDestination(t)
	h := newConnectHarness(t, false)
	const delay = 40 * time.Millisecond
	setDelayForTest(t, h.port, types.NewDelay(delay, delay, -1))
	tc, answer := sendConnect(t, h.dial(), connectRequest(dest))
	if got := answer.Header["Goto-Connect-Proxy-Delay"]; len(got) != 1 || got[0] != delay.String() {
		t.Errorf("the answer to CONNECT has Goto-Connect-Proxy-Delay = %q, want the delay of the connection", got)
	}
	if took, err := time.ParseDuration(answer.Header.Get("Goto-Connect-Proxy-Took")); err != nil || took < delay {
		t.Errorf("the answer to CONNECT has Goto-Connect-Proxy-Took = %q, err %v, want the delay in it", answer.Header.Get("Goto-Connect-Proxy-Took"), err)
	}
	c := &httpTunnel{t: t, tc: tc, br: bufio.NewReader(tc)}
	for i := 0; i < 2; i++ {
		c.send(get("/len"))
		resp, body := c.read("GET")
		if body != "12345" {
			t.Fatalf("body = %q", body)
		}
		// the request and the response were held back once each, and no more
		got, err := time.ParseDuration(resp.Header.Get("Goto-Connect-Proxy-Delay"))
		if err != nil || got < 2*delay || got > 4*delay {
			t.Errorf("exchange %d: Goto-Connect-Proxy-Delay = %q, err %v, want the delays of the request and the response", i, resp.Header.Get("Goto-Connect-Proxy-Delay"), err)
		}
		took, err := time.ParseDuration(resp.Header.Get("Goto-Connect-Proxy-Took"))
		if err != nil || took < got {
			t.Errorf("exchange %d: Goto-Connect-Proxy-Took = %q, err %v, want the delays (%s) in it", i, resp.Header.Get("Goto-Connect-Proxy-Took"), err, got)
		}
	}
}

// The time of a request is the one of its first byte: a body that comes later is not the start of another request, and
// the time that a connection is idle between requests is not time that the proxy took.
func TestConnectTimeOfARequestStartsAtItsFirstByte(t *testing.T) {
	setViaLabels(t)
	dest := newHTTPDestination(t)
	h := newConnectHarness(t, false)
	const delay = 30 * time.Millisecond
	setDelayForTest(t, h.port, types.NewDelay(delay, delay, -1))
	c := openHTTPTunnel(t, h, dest)
	// a head, and the body that comes a while later
	c.send("POST /post HTTP/1.1\r\nHost: x\r\nContent-Length: 5\r\n\r\n")
	time.Sleep(100 * time.Millisecond)
	c.send("12345")
	if resp, body := c.read("POST"); body != "got 5" || resp.StatusCode != 200 {
		t.Fatalf("got %d %q", resp.StatusCode, body)
	}
	// and a request that comes after the connection was idle
	time.Sleep(250 * time.Millisecond)
	c.send(get("/len"))
	resp, body := c.read("GET")
	if body != "12345" {
		t.Fatalf("body = %q", body)
	}
	took, err := time.ParseDuration(resp.Header.Get("Goto-Connect-Proxy-Took"))
	if err != nil || took < 2*delay || took >= 200*time.Millisecond {
		t.Errorf("Goto-Connect-Proxy-Took = %q, err %v, want the delays in it, and not the time that the connection was idle", resp.Header.Get("Goto-Connect-Proxy-Took"), err)
	}
	got, err := time.ParseDuration(resp.Header.Get("Goto-Connect-Proxy-Delay"))
	if err != nil || got < 2*delay || got > 3*delay {
		t.Errorf("Goto-Connect-Proxy-Delay = %q, err %v, want the delays of this exchange only (%s for the request and the response)", resp.Header.Get("Goto-Connect-Proxy-Delay"), err, 2*delay)
	}
}
