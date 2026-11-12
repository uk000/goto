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
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func url(port int, path string) string {
	return "/server/tcp/" + strconv.Itoa(port) + path
}

func TestAdminConfigure(t *testing.T) {
	port := newPort(t)
	body := `{"echo":true,"echoResponseSize":7,"readTimeout":"3s","writeTimeout":"4s","connIdleTimeout":"5s","connectionLife":"6s",
	  "responsePayloads":["a","b"],"responseDelay":"1s","keepOpen":true,"streamChunkSize":"20","streamChunkCount":3,"streamChunkDelay":"2ms"}`
	msg := adminOK(t, "POST", url(port, "/configure"), body)
	if !strings.Contains(msg, "TCP configuration applied to port "+strconv.Itoa(port)) {
		t.Errorf("response = %q", msg)
	}
	c := storedConfig(t, port)
	if c.Port != port || c.ListenerID != listenerID(port) || !c.Echo || c.EchoResponseSize != 7 || !c.KeepOpen {
		t.Errorf("config = %+v", c)
	}
	if c.ReadTimeoutD != 3*time.Second || c.WriteTimeoutD != 4*time.Second || c.ConnIdleTimeoutD != 5*time.Second ||
		c.ConnectionLifeD != 6*time.Second || c.ResponseDelayD != time.Second {
		t.Errorf("durations = %v/%v/%v/%v/%v", c.ReadTimeoutD, c.WriteTimeoutD, c.ConnIdleTimeoutD, c.ConnectionLifeD, c.ResponseDelayD)
	}
	if len(c.ResponsePayloads) != 2 || c.StreamChunkSizeV != 20 || c.StreamChunkCount != 3 || c.StreamChunkDelayD != 2*time.Millisecond {
		t.Errorf("payloads/stream = %v/%d/%d/%v", c.ResponsePayloads, c.StreamChunkSizeV, c.StreamChunkCount, c.StreamChunkDelayD)
	}
	// the port in the URL is the port of the config, whatever the payload says
	adminOK(t, "POST", url(port, "/configure"), `{"port":1,"listenerID":"x","echo":true}`)
	if c := storedConfig(t, port); c.Port != port || c.ListenerID != listenerID(port) {
		t.Errorf("port/listener = %d/%q, want those of the URL", c.Port, c.ListenerID)
	}
}

func TestAdminConfigureRejections(t *testing.T) {
	port := newPort(t)
	tests := []struct {
		name, method, target, body string
		want                       int
		wantMsg                    string
	}{
		{"invalid JSON", "POST", url(port, "/configure"), `{"echo":`, 400, "Failed to parse json"},
		{"wrong JSON type", "POST", url(port, "/configure"), `{"echo":"yes"}`, 400, "Failed to parse json"},
		{"negative duration", "POST", url(port, "/configure"), `{"readTimeout":"-1s"}`, 400, "Invalid read timeout"},
		{"negative expected payload length", "POST", url(port, "/configure"), `{"validatePayloadContent":true,"expectedPayloadLength":-1}`, 400, "Invalid expected payload length: -1"},
		{"huge expected payload length", "POST", url(port, "/configure"), `{"validatePayloadLength":true,"expectedPayloadLength":1099511627776}`, 400, "Invalid expected payload length: 1099511627776"},
		{"no listener", "POST", url(port+500, "/configure"), `{}`, 400, "No listener for port"},
		{"wrong method", "GET", url(port, "/configure"), ``, 404, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rr := admin(t, tc.method, tc.target, tc.body)
			if rr.Code != tc.want || !strings.Contains(rr.Body.String(), tc.wantMsg) {
				t.Errorf("%s %s = %d %q, want %d with %q", tc.method, tc.target, rr.Code, rr.Body.String(), tc.want, tc.wantMsg)
			}
		})
	}
	if getTCPConfig(port) != nil {
		t.Error("a config that was rejected must not be stored")
	}
}

func TestAdminSetDurations(t *testing.T) {
	port := configure(t, nil)
	tests := []struct {
		path string
		get  func(c TCPConfig) time.Duration
		text func(c TCPConfig) string
	}{
		{"/timeout/set/read=7s", func(c TCPConfig) time.Duration { return c.ReadTimeoutD }, func(c TCPConfig) string { return c.ReadTimeout }},
		{"/timeout/set/write=8s", func(c TCPConfig) time.Duration { return c.WriteTimeoutD }, func(c TCPConfig) string { return c.WriteTimeout }},
		{"/timeout/set/idle=9s", func(c TCPConfig) time.Duration { return c.ConnIdleTimeoutD }, func(c TCPConfig) string { return c.ConnIdleTimeout }},
		{"/connection/set/life=10s", func(c TCPConfig) time.Duration { return c.ConnectionLifeD }, func(c TCPConfig) string { return c.ConnectionLife }},
		{"/echo/response/set/delay=11ms", func(c TCPConfig) time.Duration { return c.EchoResponseDelayD }, func(c TCPConfig) string { return c.EchoResponseDelay }},
	}
	for _, tc := range tests {
		for _, method := range []string{"PUT", "POST"} {
			t.Run(method+tc.path, func(t *testing.T) {
				msg := adminOK(t, method, url(port, tc.path), "")
				eq := strings.Index(tc.path, "=")
				want, _ := time.ParseDuration(tc.path[eq+1:])
				c := storedConfig(t, port)
				if got := tc.get(c); got != want {
					t.Errorf("duration = %v, want %v", got, want)
				}
				if got := tc.text(c); got != tc.path[eq+1:] {
					t.Errorf("text of the duration = %q, want %q", got, tc.path[eq+1:])
				}
				if !strings.Contains(msg, "listener "+strconv.Itoa(port)) {
					t.Errorf("response = %q", msg)
				}
			})
		}
	}
}

func TestAdminSetDurationRejections(t *testing.T) {
	port := configure(t, &TCPConfig{ReadTimeout: "2s"})
	if rr := admin(t, "POST", url(port, "/timeout/set/read=-5s"), ""); rr.Code != 400 || !strings.Contains(rr.Body.String(), "Invalid duration") {
		t.Errorf("negative duration = %d %q, want 400", rr.Code, rr.Body.String())
	}
	if got := storedConfig(t, port).ReadTimeoutD; got != 2*time.Second {
		t.Errorf("ReadTimeoutD = %v after a rejected duration, want it kept at 2s", got)
	}
	if rr := admin(t, "POST", url(port+500, "/timeout/set/read=5s"), ""); rr.Code != 400 || !strings.Contains(rr.Body.String(), "No listener") {
		t.Errorf("port without listener = %d %q, want 400", rr.Code, rr.Body.String())
	}
	// a listener that is not a TCP listener
	other := newPort(t)
	if rr := admin(t, "POST", url(other, "/timeout/set/read=5s"), ""); rr.Code != 400 || !strings.Contains(rr.Body.String(), "is not a TCP listener") {
		t.Errorf("listener that is not TCP = %d %q, want 400", rr.Code, rr.Body.String())
	}
}

func TestAdminStreamConfig(t *testing.T) {
	port := configure(t, &TCPConfig{Echo: true})
	adminOK(t, "POST", url(port, "/stream/payload=1000/duration=1s/delay=100ms"), "")
	c := storedConfig(t, port)
	if !c.Stream || c.Echo || c.StreamPayloadSizeV != 1000 || c.StreamChunkCount != 10 || c.StreamChunkSizeV != 100 || c.StreamChunkDelayD != 100*time.Millisecond {
		t.Errorf("stream by payload/duration/delay = %+v", c)
	}
	msg := adminOK(t, "PUT", url(port, "/stream/chunksize=10/duration=100ms/delay=20ms"), "")
	c = storedConfig(t, port)
	if c.StreamChunkSizeV != 10 || c.StreamChunkCount != 5 || c.StreamChunkDelayD != 20*time.Millisecond || c.StreamDurationD != 100*time.Millisecond {
		t.Errorf("stream by chunk/duration/delay = size %d count %d delay %v duration %v", c.StreamChunkSizeV, c.StreamChunkCount, c.StreamChunkDelayD, c.StreamDurationD)
	}
	if !strings.Contains(msg, "[5] chunks of size [10]") {
		t.Errorf("response = %q", msg)
	}
	adminOK(t, "POST", url(port, "/stream/chunksize=25/count=4/delay=3ms"), "")
	c = storedConfig(t, port)
	if c.StreamChunkSizeV != 25 || c.StreamChunkCount != 4 || c.StreamPayloadSizeV != 100 || c.StreamChunkDelayD != 3*time.Millisecond || c.StreamDurationD != 12*time.Millisecond {
		t.Errorf("stream by chunk/count/delay = %+v", c)
	}
}

func TestAdminStreamConfigWithoutValuesThatCannotBeResolved(t *testing.T) {
	port := configure(t, &TCPConfig{Echo: true})
	// a count of 0 and no duration leave nothing to know how long the stream is
	rr := admin(t, "POST", url(port, "/stream/chunksize=10/count=0/delay=1ms"), "")
	if rr.Code != 400 || !strings.Contains(rr.Body.String(), "Invalid parameters for streaming") {
		t.Errorf("stream without count or duration = %d %q, want 400", rr.Code, rr.Body.String())
	}
	if c := storedConfig(t, port); c.Stream || !c.Echo {
		t.Errorf("a stream config that was rejected changed the modes: %+v", c)
	}
}

func TestAdminExpectedPayload(t *testing.T) {
	port := configure(t, &TCPConfig{Echo: true})
	msg := adminOK(t, "POST", url(port, "/expect/payload/length=12"), "")
	c := storedConfig(t, port)
	if !c.ValidatePayloadLength || c.ValidatePayloadContent || c.Echo || c.ExpectedPayloadLength != 12 || c.ExpectedPayload != nil {
		t.Errorf("config after expecting a length = %+v", c)
	}
	if !strings.Contains(msg, "[12]") {
		t.Errorf("response = %q", msg)
	}
	msg = adminOK(t, "PUT", url(port, "/expect/payload"), "abcdef")
	c = storedConfig(t, port)
	if !c.ValidatePayloadLength || !c.ValidatePayloadContent || c.ExpectedPayloadLength != 6 || string(c.ExpectedPayload) != "abcdef" {
		t.Errorf("config after expecting a payload = %+v", c)
	}
	if !strings.Contains(msg, "[6]") {
		t.Errorf("response = %q", msg)
	}
	// expecting a length again takes the expected content away
	adminOK(t, "POST", url(port, "/expect/payload/length=3"), "")
	if c := storedConfig(t, port); c.ValidatePayloadContent || c.ExpectedPayload != nil || c.ExpectedPayloadLength != 3 {
		t.Errorf("config after expecting a length again = %+v", c)
	}
}

func TestAdminExpectedPayloadRejectsWhatCannotBeServed(t *testing.T) {
	port := configure(t, &TCPConfig{Echo: true})
	adminOK(t, "POST", url(port, "/expect/payload/length=12"), "")
	prev := maxExpectedPayloadLength
	maxExpectedPayloadLength = 10
	t.Cleanup(func() { maxExpectedPayloadLength = prev })
	for _, tc := range []struct{ name, target, body string }{
		{"negative length", url(port, "/expect/payload/length=-1"), ""},
		{"length over the maximum", url(port, "/expect/payload/length=11"), ""},
		{"huge length", url(port, "/expect/payload/length=1099511627776"), ""},
		{"content over the maximum", url(port, "/expect/payload"), "01234567890"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := admin(t, "POST", tc.target, tc.body)
			if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Invalid expected payload length") {
				t.Errorf("POST %s = %d %q, want 400 with the length being invalid", tc.target, rr.Code, rr.Body.String())
			}
			if c := storedConfig(t, port); c.ExpectedPayloadLength != 12 || !c.ValidatePayloadLength || c.Echo {
				t.Errorf("config after the rejection = %+v, want the one that was set before it", c)
			}
		})
	}
	// the maximum itself, and zero, are served
	adminOK(t, "POST", url(port, "/expect/payload/length=10"), "")
	adminOK(t, "POST", url(port, "/expect/payload/length=0"), "")
	adminOK(t, "POST", url(port, "/expect/payload"), "0123456789")
	if c := storedConfig(t, port); c.ExpectedPayloadLength != 10 || string(c.ExpectedPayload) != "0123456789" {
		t.Errorf("config = %+v", c)
	}
}

func TestAdminValidateMode(t *testing.T) {
	port := configure(t, &TCPConfig{Echo: true})
	adminOK(t, "POST", url(port, "/expect/payload/length=4"), "")
	adminOK(t, "POST", url(port, "/mode/validate=n"), "")
	if c := storedConfig(t, port); c.ValidatePayloadLength || c.ValidatePayloadContent {
		t.Errorf("validation not turned off: %+v", c)
	}
	if msg := adminOK(t, "POST", url(port, "/mode/validate=y"), ""); !strings.Contains(msg, "validate payload length [4]") {
		t.Errorf("response = %q", msg)
	}
	if c := storedConfig(t, port); !c.ValidatePayloadLength || c.ValidatePayloadContent || c.Echo {
		t.Errorf("length validation not turned on: %+v", c)
	}
	adminOK(t, "POST", url(port, "/expect/payload"), "abcd")
	adminOK(t, "POST", url(port, "/mode/validate=n"), "")
	if msg := adminOK(t, "POST", url(port, "/mode/validate=y"), ""); !strings.Contains(msg, "validate payload content of size [4]") {
		t.Errorf("response = %q", msg)
	}
	if c := storedConfig(t, port); !c.ValidatePayloadContent || !c.ValidatePayloadLength {
		t.Errorf("content validation not turned on: %+v", c)
	}
}

func TestAdminModes(t *testing.T) {
	port := configure(t, &TCPConfig{Echo: true})
	modes := []struct {
		path string
		on   func(c TCPConfig) bool
	}{
		{"/mode/stream", func(c TCPConfig) bool { return c.Stream }},
		{"/mode/echo", func(c TCPConfig) bool { return c.Echo }},
		{"/mode/conversation", func(c TCPConfig) bool { return c.Conversation }},
		{"/mode/silentlife", func(c TCPConfig) bool { return c.SilentLife }},
		{"/mode/closeatfirst", func(c TCPConfig) bool { return c.CloseAtFirstByte }},
		{"/set/payload", func(c TCPConfig) bool { return c.Payload }},
	}
	for _, m := range modes {
		t.Run(m.path, func(t *testing.T) {
			adminOK(t, "POST", url(port, m.path+"=y"), "")
			c := storedConfig(t, port)
			if !m.on(c) {
				t.Errorf("mode not on: %+v", c)
			}
			for _, other := range modes {
				if other.path != m.path && other.on(c) {
					t.Errorf("turning %s on left %s on: %+v", m.path, other.path, c)
				}
			}
			adminOK(t, "PUT", url(port, m.path+"=n"), "")
			if m.on(storedConfig(t, port)) {
				t.Errorf("mode not off: %+v", storedConfig(t, port))
			}
		})
	}
}

func TestAdminModeInvalidPortAndMode(t *testing.T) {
	port := configure(t, nil)
	if rr := admin(t, "POST", url(port+500, "/mode/echo=y"), ""); rr.Code != 400 {
		t.Errorf("mode of a port without a listener = %d, want 400", rr.Code)
	}
	if rr := admin(t, "POST", url(port, "/mode/nosuchmode=y"), ""); rr.Code != 404 {
		t.Errorf("unknown mode = %d, want 404", rr.Code)
	}
}

func TestAdminModesKeepDurationsThatWereSet(t *testing.T) {
	port := configure(t, nil)
	adminOK(t, "POST", url(port, "/timeout/set/read=7s"), "")
	adminOK(t, "POST", url(port, "/timeout/set/idle=9s"), "")
	adminOK(t, "POST", url(port, "/echo/response/set/delay=11ms"), "")
	adminOK(t, "POST", url(port, "/mode/echo=y"), "")
	c := storedConfig(t, port)
	if c.ReadTimeoutD != 7*time.Second || c.ConnIdleTimeoutD != 9*time.Second || c.EchoResponseDelayD != 11*time.Millisecond {
		t.Errorf("durations after a mode was turned on = %v/%v/%v, want 7s/9s/11ms as they were set", c.ReadTimeoutD, c.ConnIdleTimeoutD, c.EchoResponseDelayD)
	}
}

func TestAdminActiveConnections(t *testing.T) {
	s := startServer(t, &TCPConfig{SilentLife: true, ConnectionLife: "30s"})
	p := strconv.Itoa(s.port)
	if body := strings.TrimSpace(adminOK(t, "GET", url(s.port, "/active"), "")); body != `{"`+p+`":{}}` && body != "{}" {
		t.Errorf("active connections of a port without any = %q", body)
	}
	c1, c2 := s.dial(), s.dial()
	s.waitConnections(2)
	waitFor(t, "2 active connections", 3*time.Second, func() bool { return activeCount(s.port) == 2 })

	var byPort map[string]map[string]struct {
		Status *ConnectionStatus `json:"status"`
		Config *TCPConfig        `json:"config"`
	}
	for _, target := range []string{url(s.port, "/active"), "/server/tcp/active"} {
		byPort = nil
		if err := json.Unmarshal([]byte(adminOK(t, "GET", target, "")), &byPort); err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		conns := byPort[p]
		if len(conns) != 2 {
			t.Fatalf("%s reports %d connections of the port, want 2: %v", target, len(conns), byPort)
		}
		for id, c := range conns {
			if strconv.Itoa(c.Status.RequestID) != id || c.Status.Port != s.port || !c.Config.SilentLife || c.Status.Closed {
				t.Errorf("%s: connection %s = status %+v config %+v", target, id, c.Status, c.Config)
			}
		}
	}
	c1.Close()
	waitFor(t, "1 active connection", 3*time.Second, func() bool { return activeCount(s.port) == 1 })
	byPort = nil
	json.Unmarshal([]byte(adminOK(t, "GET", url(s.port, "/active"), "")), &byPort)
	if len(byPort[p]) != 1 || byPort[p]["2"].Status.RequestID != 2 {
		t.Errorf("active connections after one closed = %v", byPort)
	}
	c2.Close()
}

func TestAdminConnectionHistory(t *testing.T) {
	echo := startServer(t, &TCPConfig{Echo: true, EchoResponseSize: 1})
	stream := startServer(t, &TCPConfig{Stream: true, StreamChunkSize: "1", StreamChunkCount: 1, StreamChunkDelay: "1ms"})
	for i := 0; i < 2; i++ {
		c := echo.dial()
		write(t, c, "x")
		readN(t, c, 1)
		c.Close()
	}
	readAll(t, stream.dial())
	echo.waitIdle()
	stream.waitIdle()
	ep, sp := strconv.Itoa(echo.port), strconv.Itoa(stream.port)

	decode := func(target string) map[string]map[string]ConnectionHistory {
		var m map[string]map[string]ConnectionHistory
		if err := json.Unmarshal([]byte(adminOK(t, "GET", target, "")), &m); err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		return m
	}
	if h := decode(url(echo.port, "/history")); len(h) != 1 || len(h[ep]) != 2 {
		t.Errorf("history of the echo port = %v, want its 2 connections", h)
	}
	all := decode("/server/tcp/history")
	if len(all[ep]) != 2 || len(all[sp]) != 1 {
		t.Errorf("history of all ports = %v, want 2 + 1 connections", all)
	}
	for id, h := range all[ep] {
		if strconv.Itoa(h.Status.RequestID) != id || !h.Config.Echo || h.Status.TotalBytesRead != 1 || !h.Status.Closed {
			t.Errorf("history entry %s = %+v/%+v", id, h.Config, h.Status)
		}
	}
	if h := decode(url(echo.port, "/history/echo")); len(h[ep]) != 2 {
		t.Errorf("echo history of the echo port = %v", h)
	}
	if h := decode(url(echo.port, "/history/stream")); len(h[ep]) != 0 {
		t.Errorf("stream history of the echo port = %v, want none", h)
	}
	if h := decode("/server/tcp/history/stream"); len(h[ep]) != 0 || len(h[sp]) != 1 {
		t.Errorf("stream history of all ports = %v, want the stream port's connection", h)
	}
	if h := decode("/server/tcp/history/ECHO"); len(h[ep]) != 2 || len(h[sp]) != 0 {
		t.Errorf("echo history of all ports (any case) = %v", h)
	}
	if body := strings.TrimSpace(adminOK(t, "GET", url(echo.port+300, "/history"), "")); body != "{}" && body != "" {
		t.Errorf("history of a port that has none = %q", body)
	}
}

func TestAdminClearHistory(t *testing.T) {
	a := startServer(t, &TCPConfig{CloseAtFirstByte: true})
	b := startServer(t, &TCPConfig{CloseAtFirstByte: true})
	for _, s := range []*server{a, b} {
		c := s.dial()
		write(t, c, "x")
		readAll(t, c)
		s.waitIdle()
	}
	if msg := adminOK(t, "POST", url(a.port, "/history/clear"), ""); !strings.Contains(msg, "for port "+strconv.Itoa(a.port)) {
		t.Errorf("response = %q", msg)
	}
	if historyCount(a.port) != 0 || historyCount(b.port) != 1 {
		t.Errorf("history after clearing one port = %d/%d, want 0/1", historyCount(a.port), historyCount(b.port))
	}
	adminOK(t, "POST", "/server/tcp/history/clear", "")
	if historyCount(b.port) != 0 {
		t.Errorf("history of the other port after clearing all = %d, want 0", historyCount(b.port))
	}
	// a connection after that is recorded again
	c := a.dial()
	write(t, c, "x")
	readAll(t, c)
	a.waitIdle()
	if historyCount(a.port) != 1 {
		t.Errorf("history after a new connection = %d, want 1", historyCount(a.port))
	}
}

func TestAdminRoutesServeTheirMethodsOnly(t *testing.T) {
	port := configure(t, nil)
	for _, target := range []string{
		url(port, "/timeout/set/read=1s"), url(port, "/timeout/set/write=1s"), url(port, "/timeout/set/idle=1s"),
		url(port, "/connection/set/life=1s"), url(port, "/echo/response/set/delay=1ms"),
		url(port, "/mode/echo=y"), url(port, "/set/payload=n"),
	} {
		for _, method := range []string{"POST", "PUT"} {
			if rr := admin(t, method, target, ""); rr.Code != http.StatusOK {
				t.Errorf("%s %s = %d, want 200", method, target, rr.Code)
			}
		}
		if rr := admin(t, "GET", target, ""); rr.Code == http.StatusOK {
			t.Errorf("GET %s = %d, want the method not to be served", target, rr.Code)
		}
	}
	for _, target := range []string{url(port, "/active"), "/server/tcp/active", url(port, "/history"), "/server/tcp/history", url(port, "/history/echo"), "/server/tcp/history/echo"} {
		if rr := admin(t, "GET", target, ""); rr.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", target, rr.Code)
		}
		if rr := admin(t, "DELETE", target, ""); rr.Code == http.StatusOK {
			t.Errorf("DELETE %s = %d, want the method not to be served", target, rr.Code)
		}
	}
}
