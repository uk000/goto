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

package tls

import (
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

const ioTimeout = 5 * time.Second

type handshakeResult struct {
	data []byte
	err  error
}

// pipe returns the two ends of a connection, with deadlines that keep a broken test from hanging.
func pipe(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() })
	for _, c := range []net.Conn{a, b} {
		if err := c.SetDeadline(time.Now().Add(ioTimeout)); err != nil {
			t.Fatal(err)
		}
	}
	return a, b
}

func runHandle(a *ALPN, conn net.Conn) <-chan handshakeResult {
	done := make(chan handshakeResult, 1)
	go func() {
		data, err := a.Handle(conn)
		done <- handshakeResult{data, err}
	}()
	return done
}

func wait(t *testing.T, done <-chan handshakeResult) handshakeResult {
	t.Helper()
	select {
	case r := <-done:
		return r
	case <-time.After(ioTimeout):
		t.Fatal("handshake didn't finish")
		return handshakeResult{}
	}
}

func readN(t *testing.T, c net.Conn, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := io.ReadFull(c, b); err != nil {
		t.Fatalf("reading %d bytes: %v", n, err)
	}
	return string(b)
}

func writeAll(t *testing.T, c net.Conn, data []byte) {
	t.Helper()
	if _, err := c.Write(data); err != nil {
		t.Fatalf("writing: %v", err)
	}
}

func steps(ss ...*HandshakeStep) *ALPN {
	return NewALPN([]string{"istio"}, &TCPHandshake{Seq: ss})
}

func write(hexData string) *HandshakeStep {
	return &HandshakeStep{Write: &PreambleWrite{Preamble: hexData}}
}

func read(length uint32, nextSize, collect bool) *HandshakeStep {
	return &HandshakeStep{Read: &PreambleRead{Length: length, NextSize: nextSize, Collect: collect}}
}

func TestNewALPN(t *testing.T) {
	hs := &TCPHandshake{Protos: []string{"a"}}
	a := NewALPN([]string{"h2", "http/1.1"}, hs)
	if strings.Join(a.Protos, ",") != "h2,http/1.1" || a.Handshake != hs || a.KeepDefault {
		t.Errorf("ALPN = %+v", a)
	}
}

func TestALPNWithoutHandshake(t *testing.T) {
	c, _ := pipe(t)
	a := NewALPN([]string{"h2"}, nil)
	if data, err := a.Handle(c); data != nil || err != nil {
		t.Errorf("Handle = %q, %v", data, err)
	}
	if data, err := a.HandleServer(c, &tls.ConnectionState{NegotiatedProtocol: "h2"}); data != nil || err != nil {
		t.Errorf("HandleServer = %q, %v", data, err)
	}
	if data, err := steps().Handle(c); data != nil || err != nil {
		t.Errorf("Handle with an empty sequence = %q, %v", data, err)
	}
}

func TestALPNWriteStep(t *testing.T) {
	c, peer := pipe(t)
	done := runHandle(steps(write("68656c6c6f"), write("21")), c)
	if got := readN(t, peer, 6); got != "hello!" {
		t.Errorf("peer received %q", got)
	}
	if r := wait(t, done); r.err != nil || r.data != nil {
		t.Errorf("result = %+v", r)
	}
}

func TestALPNReadSteps(t *testing.T) {
	c, peer := pipe(t)
	done := runHandle(steps(read(3, false, false), read(5, false, true)), c)
	writeAll(t, peer, []byte("abc"))
	writeAll(t, peer, []byte("world"))
	if r := wait(t, done); r.err != nil || string(r.data) != "world" {
		t.Errorf("result = %q, %v, want the collected frame", r.data, r.err)
	}
}

func TestALPNReadWithoutCollectDeliversNothing(t *testing.T) {
	c, peer := pipe(t)
	done := runHandle(steps(read(3, false, false)), c)
	writeAll(t, peer, []byte("abc"))
	if r := wait(t, done); r.err != nil || r.data != nil {
		t.Errorf("result = %q, %v, want no data", r.data, r.err)
	}
}

func TestALPNReadSizeThenFrame(t *testing.T) {
	c, peer := pipe(t)
	done := runHandle(steps(read(4, true, false), read(0, false, true)), c)
	writeAll(t, peer, []byte{0, 0, 0, 6})
	writeAll(t, peer, []byte("framed"))
	if r := wait(t, done); r.err != nil || string(r.data) != "framed" {
		t.Errorf("result = %q, %v, want the frame of the announced size", r.data, r.err)
	}
}

func TestALPNExchange(t *testing.T) {
	c, peer := pipe(t)
	done := runHandle(steps(write("6869"), read(2, false, true)), c)
	if got := readN(t, peer, 2); got != "hi" {
		t.Errorf("peer received %q", got)
	}
	writeAll(t, peer, []byte("ok"))
	if r := wait(t, done); r.err != nil || string(r.data) != "ok" {
		t.Errorf("result = %q, %v", r.data, r.err)
	}
}

func TestALPNInvalidPreamble(t *testing.T) {
	c, _ := pipe(t)
	r := wait(t, runHandle(steps(write("zz")), c))
	if r.err == nil || !strings.Contains(r.err.Error(), "decode preamble") {
		t.Errorf("err = %v, want a decode error", r.err)
	}
}

func TestALPNReadFailure(t *testing.T) {
	c, peer := pipe(t)
	done := runHandle(steps(read(4, false, true)), c)
	writeAll(t, peer, []byte("ab"))
	peer.Close()
	if r := wait(t, done); r.err == nil || r.data != nil {
		t.Errorf("result = %q, %v, want an error for a connection closed in the middle of a frame", r.data, r.err)
	}
}

func TestALPNWriteFailureClosesTheConnection(t *testing.T) {
	c, peer := pipe(t)
	peer.Close()
	if r := wait(t, runHandle(steps(write("6869")), c)); r.err == nil {
		t.Error("expected a write error")
	}
	if _, err := c.Write([]byte("x")); err == nil {
		t.Error("the connection should have been closed after the failed write")
	}
}

func TestALPNSizeFrameNeedsFourBytes(t *testing.T) {
	c, peer := pipe(t)
	done := runHandle(steps(read(2, true, false)), c)
	// Whether or not the peer has sent anything, the read can't deliver a size
	go func() { peer.Write([]byte{0, 1}) }()
	if r := wait(t, done); r.err == nil {
		t.Error("expected an error instead of a size taken from less than 4 bytes")
	}
}

func TestALPNHandleServerProtocols(t *testing.T) {
	a := NewALPN([]string{"istio", "h2"}, &TCPHandshake{Protos: []string{"istio"}, Seq: []*HandshakeStep{write("6869")}})
	c, peer := pipe(t)

	// A protocol that the handshake isn't meant for leaves the connection alone.
	if data, err := a.HandleServer(c, &tls.ConnectionState{NegotiatedProtocol: "h2"}); data != nil || err != nil {
		t.Fatalf("HandleServer for another protocol = %q, %v", data, err)
	}
	_ = peer.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	if n, _ := peer.Read(make([]byte, 1)); n != 0 {
		t.Fatal("nothing should have been written for another protocol")
	}
	_ = peer.SetReadDeadline(time.Now().Add(ioTimeout))

	done := make(chan handshakeResult, 1)
	go func() {
		data, err := a.HandleServer(c, &tls.ConnectionState{NegotiatedProtocol: "istio"})
		done <- handshakeResult{data, err}
	}()
	if got := readN(t, peer, 2); got != "hi" {
		t.Errorf("peer received %q", got)
	}
	if r := wait(t, done); r.err != nil {
		t.Errorf("HandleServer = %v", r.err)
	}
}

func TestALPNHandleServerWithoutProtocolRestriction(t *testing.T) {
	a := NewALPN(nil, &TCPHandshake{Seq: []*HandshakeStep{read(2, false, true)}})
	c, peer := pipe(t)
	done := make(chan handshakeResult, 1)
	go func() {
		data, err := a.HandleServer(c, &tls.ConnectionState{NegotiatedProtocol: "anything"})
		done <- handshakeResult{data, err}
	}()
	writeAll(t, peer, []byte("yo"))
	if r := wait(t, done); r.err != nil || string(r.data) != "yo" {
		t.Errorf("HandleServer = %q, %v", r.data, r.err)
	}
}

func TestALPNStepsCalledDirectly(t *testing.T) {
	c, peer := pipe(t)
	go func() { peer.Write([]byte("abcdef")) }()
	next, data, err := (&HandshakeStep{Read: &PreambleRead{Length: 3, Collect: true}}).Handle(c, 0)
	if err != nil || next != 0 || string(data) != "abc" {
		t.Errorf("HandshakeStep read = %d, %q, %v", next, data, err)
	}
	next, data, err = (&PreambleRead{Collect: true}).Handle(c, 3)
	if err != nil || next != 0 || string(data) != "def" {
		t.Errorf("PreambleRead with a size = %d, %q, %v", next, data, err)
	}
	if _, data, err := (&HandshakeStep{}).Handle(c, 0); data != nil || err != nil {
		t.Errorf("a step without a read or write = %q, %v", data, err)
	}
	go func() { io.ReadFull(peer, make([]byte, 2)) }()
	if err := (&PreambleWrite{Preamble: "0102"}).Handle(c); err != nil {
		t.Errorf("PreambleWrite = %v", err)
	}
}

func TestALPNJSON(t *testing.T) {
	in := `{"protos":["istio","h2"],"keepDefault":true,"handshake":{"protos":["istio"],"seq":[{"write":{"preamble":"0a0b"}},{"read":{"len":4,"nextSize":true}},{"read":{"collect":true}}]}}`
	a := &ALPN{}
	if err := json.Unmarshal([]byte(in), a); err != nil {
		t.Fatal(err)
	}
	if !a.KeepDefault || len(a.Protos) != 2 || a.Handshake == nil || len(a.Handshake.Seq) != 3 {
		t.Fatalf("ALPN = %+v", a)
	}
	if a.Handshake.Seq[0].Write.Preamble != "0a0b" || a.Handshake.Seq[1].Read.Length != 4 || !a.Handshake.Seq[1].Read.NextSize || !a.Handshake.Seq[2].Read.Collect {
		t.Errorf("handshake = %+v", a.Handshake)
	}
	out, err := json.Marshal(a)
	if err != nil || string(out) != in {
		t.Errorf("marshalled %s, %v", out, err)
	}
}
