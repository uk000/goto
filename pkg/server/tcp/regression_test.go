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

// Regression tests of the bugs that were fixed in this package. They only use what the package gave before the fixes
// (real connections, the admin APIs, what they report), so that they can be run against the code as it was.

import (
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// mustNotPanic runs f, and fails the test if it panics instead of letting the panic end the test binary.
func mustNotPanic(t *testing.T, what string, f func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s panicked: %v", what, r)
		}
	}()
	f()
}

// A listener in payload mode that has no payloads panicked in the goroutine of the connection, which ends the server.
func TestRegressionPayloadModeWithoutPayloadsDoesNotCrash(t *testing.T) {
	s := startServer(t, &TCPConfig{Payload: true})
	conn := s.dial()
	if got := readAll(t, conn); got != "" {
		t.Errorf("got %q from a listener that has no payloads", got)
	}
	s.waitIdle()
}

// The payload mode can be turned on by the API for a listener that never had payloads.
func TestRegressionPayloadModeTurnedOnByAPIWithoutPayloads(t *testing.T) {
	s := startServer(t, &TCPConfig{Echo: true})
	adminOK(t, "POST", url(s.port, "/set/payload=y"), "")
	conn := s.dial()
	readAll(t, conn)
	s.waitIdle()
}

// The content that a connection is validated against was never given to the connection: it compared what was received
// with nothing, and so it never was a match.
func TestRegressionPayloadContentMatchIsASuccess(t *testing.T) {
	s := startServer(t, &TCPConfig{Echo: true})
	adminOK(t, "POST", url(s.port, "/expect/payload"), "hello")
	adminOK(t, "POST", url(s.port, "/mode/validate=y"), "")
	conn := s.dial()
	write(t, conn, "hello")
	closeWrite(t, conn)
	if got := readAll(t, conn); !strings.Contains(got, "[SUCCESS]") {
		t.Errorf("validating the expected content = %q, want a success", got)
	}
}

// A content of 100 bytes or more is longer than what a connection reads at a time, and indexed the read buffer out of its
// range when the content was validated.
func TestRegressionPayloadContentOfAHundredBytesOrMore(t *testing.T) {
	for _, size := range []int{99, 100, 101, 400} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			s := startServer(t, &TCPConfig{Echo: true})
			expected := strings.Repeat("x", size-1) + "y"
			adminOK(t, "POST", url(s.port, "/expect/payload"), expected)
			conn := s.dial()
			write(t, conn, expected)
			closeWrite(t, conn)
			if got := readAll(t, conn); !strings.Contains(got, "[SUCCESS]") {
				t.Errorf("validating %d bytes of the expected content = %q, want a success", size, got)
			}
		})
	}
}

// More bytes than expected, after the bytes that were expected, were only reported when the connection ran out of time.
func TestRegressionPayloadExcessAfterExactLengthIsReportedAtOnce(t *testing.T) {
	s := startServer(t, &TCPConfig{ValidatePayloadLength: true, ExpectedPayloadLength: 3, ReadTimeout: "3s"})
	conn := s.dial()
	write(t, conn, "abc")
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	write(t, conn, "d")
	got := readAll(t, conn)
	if !strings.Contains(got, "[ERROR:EXCEEDED]") {
		t.Fatalf("result = %q, want the excess to be reported", got)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("the excess was reported after %v, want at once, not at the end of the read timeout of 3s", d)
	}
}

// A duration that is shorter than the delay, a delay of less than a millisecond, and a payload smaller than a chunk all
// divided by zero when the stream config was resolved.
func TestRegressionStreamConfigDoesNotDivideByZero(t *testing.T) {
	tests := []struct {
		name                       string
		payload, chunk, dur, delay string
		count                      int
	}{
		{name: "payload smaller than chunk", payload: "5", chunk: "10", dur: "1s"},
		{name: "delay below a millisecond", payload: "100", delay: "500us", dur: "1s"},
		{name: "duration shorter than delay", payload: "100", delay: "100ms", dur: "1ms"},
		{name: "delay below a millisecond with chunk size", chunk: "10", delay: "100us", dur: "1ms"},
		{name: "payload and duration only", payload: "100", dur: "1ms", delay: "5ms"},
	}
	for _, tc := range tests {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s streaming=%t", tc.name, streaming), func(t *testing.T) {
				c := &TCPConfig{Stream: streaming}
				mustNotPanic(t, "resolving the stream config", func() {
					c.configureStreamParams(tc.payload, tc.chunk, tc.dur, tc.delay, tc.count)
				})
				if c.StreamChunkSizeV < 0 || c.StreamChunkCount < 0 || c.StreamChunkDelayD < 0 || c.StreamDurationD < 0 {
					t.Errorf("a negative stream config: %+v", c)
				}
			})
		}
	}
}

func TestRegressionStreamConfigThroughConfigureAndAPIDoesNotDivideByZero(t *testing.T) {
	port := newPort(t)
	mustNotPanic(t, "configuring a stream listener", func() {
		InitTCPConfig(port, &TCPConfig{Stream: true, StreamPayloadSize: "5", StreamChunkSize: "10", StreamDuration: "1s"})
	})
	mustNotPanic(t, "the stream API", func() {
		admin(t, "POST", url(port, "/stream/payload=100/duration=1ms/delay=100ms"), "")
	})
	mustNotPanic(t, "the stream API with a delay below a millisecond", func() {
		admin(t, "POST", url(port, "/stream/payload=100/duration=1s/delay=100us"), "")
	})
}

// A negative size, count or time in the stream config ended up as a negative size of the buffer of the connection, which
// panicked in the goroutine of the connection and ended the server.
func TestRegressionNegativeStreamValuesDoNotCrashTheConnection(t *testing.T) {
	s := startServer(t, &TCPConfig{Echo: true})
	adminOK(t, "POST", url(s.port, "/stream/chunksize=-5/count=3/delay=1ms"), "")
	conn := s.dial()
	readAll(t, conn)
	s.waitIdle()
}

// A connection to a port that has no config is closed, instead of being left open with nothing that serves it.
func TestRegressionConnectionWithoutConfigIsClosed(t *testing.T) {
	stubOnce.Do(stubGlobalFuncs)
	port := int(42000 + portSeq.Add(1))
	server, client := net.Pipe()
	defer client.Close()
	defer server.Close()
	if ServeClientConnection(port, 1, server) {
		t.Fatal("a connection without config was served")
	}
	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := client.Read(make([]byte, 1)); err != io.EOF {
		t.Errorf("read = %v, want EOF: the connection that cannot be served has to be closed", err)
	}
}

// The durations that the APIs set were only set as values: the API that turns a mode on reads the config again from the
// text of its durations, and took them back.
func TestRegressionModeAPIKeepsDurationsThatWereSet(t *testing.T) {
	port := configure(t, nil)
	adminOK(t, "POST", url(port, "/connection/set/life=12s"), "")
	adminOK(t, "POST", url(port, "/timeout/set/read=7s"), "")
	adminOK(t, "POST", url(port, "/timeout/set/write=8s"), "")
	adminOK(t, "POST", url(port, "/mode/echo=y"), "")
	adminOK(t, "POST", url(port, "/set/payload=y"), "")
	c := storedConfig(t, port)
	if c.ConnectionLifeD != 12*time.Second || c.ReadTimeoutD != 7*time.Second || c.WriteTimeoutD != 8*time.Second {
		t.Errorf("life/read/write = %v/%v/%v after the modes were turned on, want 12s/7s/8s as they were set", c.ConnectionLifeD, c.ReadTimeoutD, c.WriteTimeoutD)
	}
}

// A stream went on when the client was gone: every chunk that was not sent was tried, with its delay, for as long as the
// stream would have lasted.
func TestRegressionStreamEndsWhenClientIsGone(t *testing.T) {
	s := startServer(t, &TCPConfig{Stream: true, StreamChunkSize: "1000", StreamChunkCount: 2000, StreamChunkDelay: "5ms"})
	conn := s.dial()
	readN(t, conn, 1000)
	conn.Close()
	waitFor(t, "the stream to end after the client left", 4*time.Second, func() bool { return activeCount(s.port) == 0 })
}

// The status of a connection told that the client closed it only in the modes that read a byte at a time.
func TestRegressionClientClosedIsReportedByEveryMode(t *testing.T) {
	configs := map[string]*TCPConfig{
		"echo":       {Echo: true},
		"silentlife": {SilentLife: true, ConnectionLife: "30s"},
		"validation": {ValidatePayloadLength: true, ExpectedPayloadLength: 5, ReadTimeout: "5s"},
		"payload":    {Payload: true, ResponsePayloads: []string{"x"}, RespondAfterRead: true, ReadTimeout: "5s"},
	}
	for name, cfg := range configs {
		t.Run(name, func(t *testing.T) {
			s := startServer(t, cfg)
			conn := s.dial()
			s.waitConnections(1)
			conn.Close()
			s.waitIdle()
			st := statuses(t, s.port)[0]
			if !st.ClientClosed {
				t.Errorf("status = %+v, want clientClosed as the client closed the connection", st)
			}
		})
	}
}

// The scanner of the conversation mode has no error of the connection to tell: a client that closed, and a timeout, were
// both reported as an error.
func TestRegressionConversationEndsAreReportedForWhatTheyAre(t *testing.T) {
	t.Run("client closed", func(t *testing.T) {
		s := startServer(t, &TCPConfig{Conversation: true, ReadTimeout: "5s"})
		conn := s.dial()
		s.waitConnections(1)
		conn.Close()
		s.waitIdle()
		if st := statuses(t, s.port)[0]; !st.ClientClosed || st.ErrorClosed {
			t.Errorf("status = clientClosed %t errorClosed %t, want the client to have closed", st.ClientClosed, st.ErrorClosed)
		}
	})
	t.Run("read timeout", func(t *testing.T) {
		s := startServer(t, &TCPConfig{Conversation: true, ReadTimeout: "200ms"})
		conn := s.dial()
		readAll(t, conn)
		s.waitIdle()
		if st := statuses(t, s.port)[0]; !st.ReadTimeout || st.ErrorClosed {
			t.Errorf("status = readTimeout %t errorClosed %t, want a read timeout", st.ReadTimeout, st.ErrorClosed)
		}
	})
}

// ---- races (the race detector is what fails these, on the code as it was) ----

// The config of a port was copied by the connections as the APIs changed it, and the APIs changed it without a lock.
func TestRegressionRaceConfigChangesWhileConnectionsComeIn(t *testing.T) {
	s := startServer(t, &TCPConfig{Echo: true, EchoResponseSize: 2})
	p := strconv.Itoa(s.port)
	initAdmin(t)
	stop := make(chan struct{})
	var apis sync.WaitGroup
	for i := 0; i < 3; i++ {
		apis.Add(1)
		go func(i int) {
			defer apis.Done()
			targets := []string{
				"/server/tcp/" + p + "/timeout/set/read=" + strconv.Itoa(i+3) + "s",
				"/server/tcp/" + p + "/mode/echo=y",
				"/server/tcp/" + p + "/connection/set/life=0s",
				"/server/tcp/" + p + "/echo/response/set/delay=1ms",
				"/server/tcp/" + p + "/mode/stream=n",
			}
			for n := 0; ; n++ {
				select {
				case <-stop:
					return
				default:
				}
				serveAdmin("POST", targets[n%len(targets)], "")
			}
		}(i)
	}
	var clients sync.WaitGroup
	for i := 0; i < 15; i++ {
		clients.Add(1)
		go func() {
			defer clients.Done()
			conn, err := net.DialTimeout("tcp", s.ln.Addr().String(), 3*time.Second)
			if err != nil {
				return
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(5 * time.Second))
			conn.Write([]byte("ab"))
			io.ReadFull(conn, make([]byte, 2))
		}()
	}
	clients.Wait()
	close(stop)
	apis.Wait()
	if historyCount(s.port) < 1 {
		t.Error("no connection was served")
	}
}

// The status of a connection was reported as it was updated, and the history of the port as it was added to.
func TestRegressionRaceReportingWhileConnectionsAreServed(t *testing.T) {
	s := startServer(t, &TCPConfig{Echo: true, EchoResponseSize: 1})
	p := strconv.Itoa(s.port)
	initAdmin(t)
	stop := make(chan struct{})
	var reporters sync.WaitGroup
	for _, target := range []string{"/server/tcp/" + p + "/active", "/server/tcp/active", "/server/tcp/" + p + "/history", "/server/tcp/history/echo"} {
		reporters.Add(1)
		go func(target string) {
			defer reporters.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				serveAdmin("GET", target, "")
			}
		}(target)
	}
	var clients sync.WaitGroup
	for i := 0; i < 8; i++ {
		clients.Add(1)
		go func() {
			defer clients.Done()
			conn, err := net.DialTimeout("tcp", s.ln.Addr().String(), 3*time.Second)
			if err != nil {
				return
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(5 * time.Second))
			buf := make([]byte, 1)
			for n := 0; n < 20; n++ {
				if _, err := conn.Write([]byte("x")); err != nil {
					return
				}
				if _, err := io.ReadFull(conn, buf); err != nil {
					return
				}
			}
		}()
	}
	clients.Wait()
	close(stop)
	reporters.Wait()
}

// Closing the connections of a listener flagged them from another goroutine than the one that serves them.
func TestRegressionRaceClosingListenerWhileConnectionsAreServed(t *testing.T) {
	s := startServer(t, &TCPConfig{Echo: true, EchoResponseSize: 1})
	var clients sync.WaitGroup
	for i := 0; i < 8; i++ {
		clients.Add(1)
		go func() {
			defer clients.Done()
			conn, err := net.DialTimeout("tcp", s.ln.Addr().String(), 3*time.Second)
			if err != nil {
				return
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(5 * time.Second))
			buf := make([]byte, 1)
			for n := 0; n < 200; n++ {
				if _, err := conn.Write([]byte("x")); err != nil {
					return
				}
				if _, err := io.ReadFull(conn, buf); err != nil {
					return
				}
			}
		}()
	}
	for i := 0; i < 20; i++ {
		time.Sleep(2 * time.Millisecond)
		CloseListenerConnections(listenerID(s.port))
	}
	clients.Wait()
	s.waitIdle()
}
