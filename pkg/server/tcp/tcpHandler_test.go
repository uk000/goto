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
	"strconv"
	"strings"
	"testing"
	"time"
)

// ---- echo ----

func TestEchoEchoesInChunksOfResponseSize(t *testing.T) {
	s := startServer(t, &TCPConfig{Echo: true, EchoResponseSize: 5})
	conn := s.dial()
	// 12 bytes: two chunks are echoed, 2 bytes stay until there are enough for another chunk
	write(t, conn, "abcdefghijkl")
	if got := readN(t, conn, 10); got != "abcdefghij" {
		t.Fatalf("echo = %q, want abcdefghij", got)
	}
	expectNoData(t, conn, 100*time.Millisecond)
	write(t, conn, "mn3")
	if got := readN(t, conn, 5); got != "klmn3" {
		t.Fatalf("echo of the kept bytes with the new ones = %q, want klmn3", got)
	}
	conn.Close()
	s.waitIdle()
	st := statuses(t, s.port)[0]
	if st.TotalBytesRead != 15 || st.TotalBytesSent != 15 {
		t.Errorf("bytes read/sent = %d/%d, want 15/15", st.TotalBytesRead, st.TotalBytesSent)
	}
}

func TestEchoWaitsForEnoughBytes(t *testing.T) {
	s := startServer(t, &TCPConfig{Echo: true, EchoResponseSize: 6})
	conn := s.dial()
	write(t, conn, "ab")
	expectNoData(t, conn, 100*time.Millisecond)
	write(t, conn, "cd")
	expectNoData(t, conn, 100*time.Millisecond)
	write(t, conn, "ef")
	if got := readN(t, conn, 6); got != "abcdef" {
		t.Fatalf("echo = %q, want abcdef", got)
	}
}

func TestEchoDefaultResponseSizeIsTen(t *testing.T) {
	s := startServer(t, nil)
	conn := s.dial()
	write(t, conn, "123456789")
	expectNoData(t, conn, 100*time.Millisecond)
	write(t, conn, "0")
	if got := readN(t, conn, 10); got != "1234567890" {
		t.Fatalf("echo = %q, want 1234567890", got)
	}
}

func TestEchoLargerThanReadBuffer(t *testing.T) {
	// the response is larger than what is read at a time, and the input comes in more reads than responses
	s := startServer(t, &TCPConfig{Echo: true, EchoResponseSize: 150})
	conn := s.dial()
	var in strings.Builder
	for i := 0; i < 300; i++ {
		in.WriteByte(byte('a' + i%26))
	}
	write(t, conn, in.String())
	if got := readN(t, conn, 300); got != in.String() {
		t.Fatalf("echo differs from what was sent:\n got %q\nwant %q", got, in.String())
	}
}

func TestEchoResponseDelay(t *testing.T) {
	s := startServer(t, &TCPConfig{Echo: true, EchoResponseSize: 2, EchoResponseDelay: "150ms"})
	conn := s.dial()
	start := time.Now()
	write(t, conn, "hi")
	if got := readN(t, conn, 2); got != "hi" {
		t.Fatalf("echo = %q", got)
	}
	if d := time.Since(start); d < 140*time.Millisecond {
		t.Errorf("echo came after %v, want it delayed by 150ms", d)
	}
}

func TestEchoReadTimeoutClosesConnection(t *testing.T) {
	s := startServer(t, &TCPConfig{Echo: true, ReadTimeout: "200ms"})
	conn := s.dial()
	if got := readAll(t, conn); got != "" {
		t.Errorf("an idle connection got %q", got)
	}
	s.waitIdle()
	st := statuses(t, s.port)[0]
	if !st.ReadTimeout || !st.ServerClosed || st.IdleTimeout || st.LifeTimeout {
		t.Errorf("status = readTimeout %t serverClosed %t idleTimeout %t lifeTimeout %t, want a read timeout", st.ReadTimeout, st.ServerClosed, st.IdleTimeout, st.LifeTimeout)
	}
}

func TestEchoIdleTimeoutClosesConnection(t *testing.T) {
	s := startServer(t, &TCPConfig{Echo: true, ConnIdleTimeout: "200ms"})
	conn := s.dial()
	readAll(t, conn)
	s.waitIdle()
	st := statuses(t, s.port)[0]
	if !st.IdleTimeout || !st.ServerClosed || st.ReadTimeout || st.LifeTimeout {
		t.Errorf("status = idleTimeout %t serverClosed %t readTimeout %t lifeTimeout %t, want an idle timeout", st.IdleTimeout, st.ServerClosed, st.ReadTimeout, st.LifeTimeout)
	}
}

func TestEchoActivityKeepsConnectionWithinReadTimeout(t *testing.T) {
	s := startServer(t, &TCPConfig{Echo: true, EchoResponseSize: 1, ReadTimeout: "400ms"})
	conn := s.dial()
	// the read timeout is per read, so the connection outlives it as long as bytes keep coming
	for i := 0; i < 4; i++ {
		time.Sleep(200 * time.Millisecond)
		write(t, conn, "x")
		if got := readN(t, conn, 1); got != "x" {
			t.Fatalf("echo %d = %q", i, got)
		}
	}
}

// ---- silent life ----

func TestSilentLifeCountsBytesAndReportsThemAtTheEndOfLife(t *testing.T) {
	s := startServer(t, &TCPConfig{ConnectionLife: "400ms"})
	conn := s.dial()
	write(t, conn, "hello")
	write(t, conn, "!!")
	expectNoData(t, conn, 100*time.Millisecond)
	got := readAll(t, conn)
	if !strings.HasSuffix(got, "7") {
		t.Fatalf("message at the end of life = %q, want the total bytes read (7) at its end", got)
	}
	s.waitIdle()
	st := statuses(t, s.port)[0]
	if !st.LifeTimeout || !st.ServerClosed || st.ReadTimeout || st.IdleTimeout {
		t.Errorf("status = lifeTimeout %t serverClosed %t readTimeout %t idleTimeout %t, want the life to have ended", st.LifeTimeout, st.ServerClosed, st.ReadTimeout, st.IdleTimeout)
	}
	if st.TotalBytesRead != 7 {
		t.Errorf("TotalBytesRead = %d, want 7", st.TotalBytesRead)
	}
}

func TestSilentLifeEndsWhenClientCloses(t *testing.T) {
	s := startServer(t, &TCPConfig{SilentLife: true, ConnectionLife: "30s"})
	conn := s.dial()
	write(t, conn, "abc")
	closeWrite(t, conn)
	if got := readAll(t, conn); !strings.HasSuffix(got, "3") {
		t.Errorf("message after the client closed = %q, want one with 3 at its end", got)
	}
	s.waitIdle()
	if st := statuses(t, s.port)[0]; !st.ClientClosed {
		t.Errorf("status = %+v, want the client to have closed", st)
	}
}

// ---- close at first byte ----

func TestCloseAtFirstByteClosesAfterFirstByte(t *testing.T) {
	s := startServer(t, &TCPConfig{CloseAtFirstByte: true})
	conn := s.dial()
	expectNoData(t, conn, 150*time.Millisecond)
	write(t, conn, "x")
	got := readAll(t, conn)
	if !strings.HasSuffix(got, GoodByeMessage) {
		t.Fatalf("message after the first byte = %q, want %s at its end", got, GoodByeMessage)
	}
	s.waitIdle()
	st := statuses(t, s.port)[0]
	if st.TotalBytesRead != 1 || !st.ServerClosed || st.FirstByteInAt == "" || st.FirstByteOutAt == "" {
		t.Errorf("status = %+v", st)
	}
}

func TestCloseAtFirstByteClientClosesFirst(t *testing.T) {
	s := startServer(t, &TCPConfig{CloseAtFirstByte: true})
	conn := s.dial()
	s.waitConnections(1)
	conn.Close()
	s.waitIdle()
	st := statuses(t, s.port)[0]
	if !st.ClientClosed || st.TotalBytesRead != 0 {
		t.Errorf("status = %+v, want the client to have closed before sending anything", st)
	}
}

// ---- payload ----

func TestPayloadSendsAllPayloadsAndCloses(t *testing.T) {
	s := startServer(t, &TCPConfig{Payload: true, ResponsePayloads: []string{"one", "two", "three"}, ResponseDelay: "20ms"})
	conn := s.dial()
	start := time.Now()
	if got := readAll(t, conn); got != "onetwothree" {
		t.Fatalf("payloads = %q, want onetwothree", got)
	}
	if d := time.Since(start); d < 55*time.Millisecond {
		t.Errorf("3 payloads with 20ms delay came in %v", d)
	}
	s.waitIdle()
	st := statuses(t, s.port)[0]
	if st.TotalBytesSent != 11 || st.TotalWrites != 3 {
		t.Errorf("bytes sent/writes = %d/%d, want 11/3", st.TotalBytesSent, st.TotalWrites)
	}
}

func TestPayloadEmptyItemRepeatsThePreviousPayload(t *testing.T) {
	s := startServer(t, &TCPConfig{Payload: true, ResponsePayloads: []string{"a", "", "b"}})
	conn := s.dial()
	if got := readAll(t, conn); got != "aab" {
		t.Fatalf("payloads = %q, want aab", got)
	}
}

func TestPayloadRespondAfterReadWaitsForClient(t *testing.T) {
	s := startServer(t, &TCPConfig{Payload: true, ResponsePayloads: []string{"hello"}, RespondAfterRead: true, ReadTimeout: "5s"})
	conn := s.dial()
	expectNoData(t, conn, 200*time.Millisecond)
	write(t, conn, "x")
	if got := readAll(t, conn); got != "hello" {
		t.Fatalf("payload after the read = %q, want hello", got)
	}
	s.waitIdle()
	if st := statuses(t, s.port)[0]; st.TotalBytesRead != 1 {
		t.Errorf("TotalBytesRead = %d, want 1", st.TotalBytesRead)
	}
}

func TestPayloadKeepOpenKeepsConnectionForItsLife(t *testing.T) {
	s := startServer(t, &TCPConfig{Payload: true, ResponsePayloads: []string{"hi"}, KeepOpen: true, ConnectionLife: "500ms"})
	conn := s.dial()
	start := time.Now()
	if got := readN(t, conn, 2); got != "hi" {
		t.Fatalf("payload = %q, want hi", got)
	}
	readAll(t, conn)
	if d := time.Since(start); d < 450*time.Millisecond {
		t.Errorf("a connection that is kept open for 500ms closed after %v", d)
	}
}

func TestPayloadWithoutPayloadsClosesConnection(t *testing.T) {
	s := startServer(t, &TCPConfig{Payload: true})
	conn := s.dial()
	if got := readAll(t, conn); got != "" {
		t.Errorf("a payload listener without payloads sent %q", got)
	}
	s.waitIdle()
}

// ---- stream ----

func TestStreamSendsConfiguredChunks(t *testing.T) {
	s := startServer(t, &TCPConfig{Stream: true, StreamChunkSize: "10", StreamChunkCount: 4, StreamChunkDelay: "20ms"})
	conn := s.dial()
	start := time.Now()
	got := readAll(t, conn)
	if len(got) != 40 {
		t.Fatalf("streamed %d bytes, want 40 (4 chunks of 10)", len(got))
	}
	if d := time.Since(start); d < 75*time.Millisecond {
		t.Errorf("4 chunks with 20ms delay streamed in %v", d)
	}
	s.waitIdle()
	st := statuses(t, s.port)[0]
	if st.TotalWrites != 4 || st.TotalBytesSent != 40 {
		t.Errorf("writes/bytes sent = %d/%d, want 4/40", st.TotalWrites, st.TotalBytesSent)
	}
}

func TestStreamStopsAtEndOfConnectionLife(t *testing.T) {
	s := startServer(t, &TCPConfig{Stream: true, StreamChunkSize: "5", StreamChunkCount: 100, StreamChunkDelay: "50ms", ConnectionLife: "200ms"})
	conn := s.dial()
	got := readAll(t, conn)
	if len(got) == 0 || len(got) >= 500 {
		t.Fatalf("streamed %d bytes, want some, but less than the 100 chunks", len(got))
	}
	if len(got)%5 != 0 {
		t.Errorf("streamed %d bytes, want whole chunks of 5", len(got))
	}
}

func TestStreamStopsWhenClientIsGone(t *testing.T) {
	s := startServer(t, &TCPConfig{Stream: true, StreamChunkSize: "1000", StreamChunkCount: 2000, StreamChunkDelay: "5ms"})
	conn := s.dial()
	readN(t, conn, 1000)
	conn.Close()
	// 2000 chunks with 5ms delay would take 10s: the stream is over as soon as sending fails
	waitFor(t, "the stream to end after the client left", 5*time.Second, func() bool { return activeCount(s.port) == 0 })
}

// ---- payload validation ----

func TestPayloadValidationLength(t *testing.T) {
	tests := []struct {
		name  string
		send  string
		want  string
		close bool
	}{
		{"exact", "12345", "[SUCCESS]: Received payload matches expected payload of length [5]", true},
		{"short", "123", "[ERROR:TIMEOUT]", true},
		{"nothing", "", "[ERROR:TIMEOUT]", true},
		{"excess", "123456789", "[ERROR:EXCEEDED] - Payload length [9] exceeded expected length [5]", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := startServer(t, &TCPConfig{ValidatePayloadLength: true, ExpectedPayloadLength: 5, ReadTimeout: "5s"})
			conn := s.dial()
			if tc.send != "" {
				write(t, conn, tc.send)
			}
			if tc.close {
				closeWrite(t, conn)
			}
			got := readAll(t, conn)
			if !strings.Contains(got, tc.want) {
				t.Errorf("result = %q, want it to have %q", got, tc.want)
			}
		})
	}
}

func TestPayloadValidationExactLengthSucceedsAfterReadTimeoutWhenClientStaysOpen(t *testing.T) {
	s := startServer(t, &TCPConfig{ValidatePayloadLength: true, ExpectedPayloadLength: 3, ReadTimeout: "250ms"})
	conn := s.dial()
	write(t, conn, "abc")
	got := readAll(t, conn)
	if !strings.Contains(got, "[SUCCESS]") {
		t.Errorf("result = %q, want success as nothing excess came while waiting", got)
	}
}

func TestPayloadValidationExcessAfterExactLength(t *testing.T) {
	s := startServer(t, &TCPConfig{ValidatePayloadLength: true, ExpectedPayloadLength: 3, ReadTimeout: "5s"})
	conn := s.dial()
	write(t, conn, "abc")
	time.Sleep(100 * time.Millisecond)
	write(t, conn, "d")
	got := readAll(t, conn)
	if !strings.Contains(got, "[ERROR:EXCEEDED] - Payload length [4] exceeded expected length [3]") {
		t.Errorf("result = %q, want the excess byte to have been caught", got)
	}
}

func TestPayloadValidationContentThroughAPI(t *testing.T) {
	s := startServer(t, &TCPConfig{Echo: true})
	p := strconv.Itoa(s.port)
	adminOK(t, "POST", "/server/tcp/"+p+"/expect/payload", "hello")
	adminOK(t, "POST", "/server/tcp/"+p+"/mode/validate=y", "")

	good := s.dial()
	write(t, good, "hello")
	closeWrite(t, good)
	if got := readAll(t, good); !strings.Contains(got, "[SUCCESS]: Received payload matches expected payload of length [5]") {
		t.Errorf("result of the expected content = %q, want success", got)
	}

	bad := s.dial()
	write(t, bad, "hellp")
	closeWrite(t, bad)
	if got := readAll(t, bad); !strings.Contains(got, "[ERROR:CONTENT] - Payload content of length [5] didn't match expected payload of length [5]") {
		t.Errorf("result of other content = %q, want a content error", got)
	}
}

func TestPayloadValidationContentLongerThanReadBuffer(t *testing.T) {
	s := startServer(t, &TCPConfig{Echo: true})
	p := strconv.Itoa(s.port)
	expected := strings.Repeat("0123456789", 25)
	adminOK(t, "POST", "/server/tcp/"+p+"/expect/payload", expected)

	good := s.dial()
	write(t, good, expected)
	closeWrite(t, good)
	if got := readAll(t, good); !strings.Contains(got, "[SUCCESS]: Received payload matches expected payload of length [250]") {
		t.Errorf("result of the expected 250 bytes = %q, want success", got)
	}

	bad := s.dial()
	write(t, bad, expected[:249]+"X")
	closeWrite(t, bad)
	if got := readAll(t, bad); !strings.Contains(got, "[ERROR:CONTENT]") {
		t.Errorf("result of 250 bytes that differ at the end = %q, want a content error", got)
	}
}

// A length that is not valid is not stored by the API, but the config of a connection is only as good as the code that
// stored it. A negative length used to make the connection allocate a buffer of that size, which panicked in the goroutine
// of the connection and ended the whole server, and a huge one tried to allocate what the server doesn't have.
func TestPayloadValidationOfALengthThatCannotBeServedEndsTheConnectionOnly(t *testing.T) {
	for _, length := range []int{-1, 1 << 40} {
		t.Run(strconv.Itoa(length), func(t *testing.T) {
			stubOnce.Do(stubGlobalFuncs)
			port := newPort(t)
			storeTCPConfig(&TCPConfig{Port: port, ValidatePayloadContent: true, ValidatePayloadLength: true, ExpectedPayloadLength: length,
				ReadTimeoutD: 5 * time.Second, WriteTimeoutD: 5 * time.Second, ConnIdleTimeoutD: 5 * time.Second})
			s := serve(t, port)
			conn := s.dial()
			write(t, conn, "abc")
			closeWrite(t, conn)
			readAll(t, conn)
			s.waitIdle()
		})
	}
}

// What a connection panics with, wherever it comes from, ends the connection and not the server, which a goroutine that
// doesn't recover does.
func TestProcessRequestRecoversFromAPanic(t *testing.T) {
	stubOnce.Do(stubGlobalFuncs)
	port := newPort(t)
	// there is no connection to read from, which the first read of the echo panics with
	h := &TCPConnectionHandler{TCPConfig: TCPConfig{Port: port, ListenerID: listenerID(port), Echo: true}, requestID: 1, status: &ConnectionStatus{Port: port}}
	lock.Lock()
	activeConnections[h.ListenerID] = map[int]*TCPConnectionHandler{1: h}
	lock.Unlock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.processRequest()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the connection that panicked was not ended")
	}
	h.status.mu.Lock()
	errored := h.status.ErrorClosed
	h.status.mu.Unlock()
	if !errored {
		t.Error("the connection that panicked is not reported as ended by an error")
	}
	if n := activeCount(port); n != 0 {
		t.Errorf("active connections after the panic = %d, want the connection to be gone", n)
	}
}

// ---- conversation ----

func TestConversation(t *testing.T) {
	s := startServer(t, &TCPConfig{Conversation: true, ReadTimeout: "5s"})
	conn := s.dial()
	hasSuffix := func(suffix string) func(string) bool {
		return func(got string) bool { return strings.HasSuffix(got, suffix) }
	}
	write(t, conn, "HELLO\n")
	if got := readUntil(t, conn, hasSuffix(HelloMessage)); !strings.HasSuffix(got, "]"+HelloMessage) {
		t.Fatalf("answer to hello = %q", got)
	}
	write(t, conn, "BEGIN/some text/END\n")
	if got := readUntil(t, conn, hasSuffix("/END")); !strings.HasSuffix(got, "]ACK/some text/END") {
		t.Fatalf("answer to a message = %q, want ACK/some text/END", got)
	}
	write(t, conn, "not the format\n")
	if got := readUntil(t, conn, hasSuffix("ERROR")); !strings.HasSuffix(got, "]ERROR") {
		t.Fatalf("answer to a malformed message = %q, want ERROR", got)
	}
	write(t, conn, "BEGIN/only begin\n")
	if got := readUntil(t, conn, hasSuffix("ERROR")); !strings.HasSuffix(got, "]ERROR") {
		t.Fatalf("answer to a message without end = %q, want ERROR", got)
	}
	write(t, conn, "goodbye\n")
	if got := readAll(t, conn); !strings.HasSuffix(got, "]"+GoodByeMessage) {
		t.Fatalf("answer to goodbye = %q, want %s and the connection to be closed", got, GoodByeMessage)
	}
	s.waitIdle()
}

func TestConversationHelloMustBeHello(t *testing.T) {
	s := startServer(t, &TCPConfig{Conversation: true, ReadTimeout: "5s"})
	conn := s.dial()
	write(t, conn, "hi there\n")
	// not a hello: there is no HELLO back, and the next message is taken as it comes
	expectNoData(t, conn, 150*time.Millisecond)
	write(t, conn, "BEGIN/x/END\n")
	if got := readUntil(t, conn, func(s string) bool { return strings.HasSuffix(s, "/END") }); !strings.HasSuffix(got, "]ACK/x/END") {
		t.Fatalf("answer = %q", got)
	}
}

func TestConversationClientClosesReportsClientClosed(t *testing.T) {
	s := startServer(t, &TCPConfig{Conversation: true, ReadTimeout: "5s"})
	conn := s.dial()
	write(t, conn, "HELLO\n")
	readUntil(t, conn, func(s string) bool { return strings.HasSuffix(s, HelloMessage) })
	conn.Close()
	s.waitIdle()
	st := statuses(t, s.port)[0]
	if !st.ClientClosed || st.ErrorClosed || st.ReadTimeout {
		t.Errorf("status = clientClosed %t errorClosed %t readTimeout %t, want the client to have closed", st.ClientClosed, st.ErrorClosed, st.ReadTimeout)
	}
}

func TestConversationReadTimeoutIsReportedAsReadTimeout(t *testing.T) {
	s := startServer(t, &TCPConfig{Conversation: true, ReadTimeout: "200ms"})
	conn := s.dial()
	readAll(t, conn)
	s.waitIdle()
	st := statuses(t, s.port)[0]
	if !st.ReadTimeout || st.ErrorClosed {
		t.Errorf("status = readTimeout %t errorClosed %t, want a read timeout", st.ReadTimeout, st.ErrorClosed)
	}
}
