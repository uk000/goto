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

package watch

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"gopkg.in/yaml.v3"
)

const deadline = 5 * time.Second

// newSockets is a set of sockets that the test owns alone.
func newSockets() *Sockets {
	return &Sockets{sockets: map[string]*Socket{}}
}

// wsPair makes a websocket connection, and returns the end of the server and the end of the client. Both are closed when
// the test is done.
func wsPair(t *testing.T) (server, client *websocket.Conn) {
	t.Helper()
	serverSide := make(chan *websocket.Conn, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := Upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverSide <- nil
			return
		}
		serverSide <- conn
	}))
	t.Cleanup(ts.Close)
	dialer := websocket.Dialer{HandshakeTimeout: deadline}
	client, _, err := dialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	select {
	case server = <-serverSide:
		if server == nil {
			t.Fatal("the server did not upgrade the connection")
		}
	case <-time.After(deadline):
		t.Fatal("the server did not get the connection")
	}
	t.Cleanup(func() { server.Close() })
	return server, client
}

// readMessage reads what a socket was sent, which is a SocketMessage in YAML.
func readMessage(t *testing.T, client *websocket.Conn) *SocketMessage {
	t.Helper()
	client.SetReadDeadline(time.Now().Add(deadline))
	kind, data, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if kind != websocket.TextMessage {
		t.Fatalf("message type = %d, want text", kind)
	}
	msg := &SocketMessage{}
	if err := yaml.Unmarshal(data, msg); err != nil {
		t.Fatalf("message is not a SocketMessage: %v\n%s", err, data)
	}
	return msg
}

// expectNoMessage checks that nothing is sent to the client for a while.
func expectNoMessage(t *testing.T, client *websocket.Conn) {
	t.Helper()
	client.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if _, data, err := client.ReadMessage(); err == nil {
		t.Errorf("unexpected message: %s", data)
	}
}

// expectClosed checks that the connection of the client is closed by the server, and not left open.
func expectClosed(t *testing.T, client *websocket.Conn) {
	t.Helper()
	client.SetReadDeadline(time.Now().Add(deadline))
	if _, _, err := client.ReadMessage(); err == nil {
		t.Error("read a message from a connection that should be closed")
	} else if ne, ok := err.(interface{ Timeout() bool }); ok && ne.Timeout() {
		t.Errorf("the connection is still open: %v", err)
	}
}

// add adds a socket for the connection, which is taken out when the test is done.
func add(t *testing.T, s *Sockets, id string, conn *websocket.Conn) {
	t.Helper()
	s.AddSocket(id, conn)
	t.Cleanup(func() { s.RemoveSocket(id) })
}

// within runs fn, and fails the test when it has not returned by the deadline.
func within(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		fn()
	}()
	select {
	case p := <-done:
		if p != nil {
			t.Fatalf("%s panicked: %v", what, p)
		}
	case <-time.After(deadline):
		t.Fatalf("%s did not return in time", what)
	}
}

// waitFor fails the test when the function does not get true by the deadline.
func waitFor(t *testing.T, what string, fn func() bool) {
	t.Helper()
	end := time.Now().Add(deadline)
	for !fn() {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestMessageTypesHaveTheirJSONShape(t *testing.T) {
	typ := reflect.TypeOf(SocketMessage{})
	if f, _ := typ.FieldByName("Request"); f.Tag.Get("json") != "request" {
		t.Errorf("Request tag = %q", f.Tag)
	}
	if f, _ := typ.FieldByName("Response"); f.Tag.Get("json") != "response" {
		t.Errorf("Response tag = %q", f.Tag)
	}
	for name, tag := range map[string]string{"StatusCode": "statusCode", "Headers": "headers", "Body": "body"} {
		if f, _ := reflect.TypeOf(ResponseDetails{}).FieldByName(name); f.Tag.Get("json") != tag {
			t.Errorf("ResponseDetails.%s tag = %q, want %q", name, f.Tag.Get("json"), tag)
		}
	}
}

func TestHasOpenSockets(t *testing.T) {
	s := newSockets()
	if s.HasOpenSockets() {
		t.Error("a new set of sockets has open sockets")
	}
	server, _ := wsPair(t)
	add(t, s, "a", server)
	if !s.HasOpenSockets() {
		t.Error("the set of sockets does not have the socket that was added")
	}
	s.RemoveSocket("a")
	if s.HasOpenSockets() {
		t.Error("the set of sockets has the socket that was removed")
	}
}

func TestAddAndGetSocket(t *testing.T) {
	s := newSockets()
	if _, ok := s.GetSocket("a"); ok {
		t.Error("a socket was found in an empty set")
	}
	server, _ := wsPair(t)
	add(t, s, "a", server)
	socket, ok := s.GetSocket("a")
	if !ok || socket.id != "a" || socket.conn != server || cap(socket.stream) != 100 {
		t.Errorf("socket = %+v, %v, want the one that was added with a stream of 100", socket, ok)
	}
}

func TestBroadcastDeliversTheMessage(t *testing.T) {
	s := newSockets()
	server, client := wsPair(t)
	add(t, s, "a", server)
	s.Broadcast("/path?q=1", http.Header{"X-Req": {"1", "2"}}, 201, http.Header{"X-Res": {"v"}}, []byte("the body"))
	got := readMessage(t, client)
	want := &SocketMessage{
		Request:  RequestDetails{URI: "/path?q=1", Headers: http.Header{"X-Req": {"1", "2"}}},
		Response: ResponseDetails{StatusCode: 201, Headers: http.Header{"X-Res": {"v"}}, Body: "the body"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("message = %+v, want %+v", got, want)
	}
	expectNoMessage(t, client)
}

func TestBroadcastReachesEverySocketOnceInOrder(t *testing.T) {
	s := newSockets()
	var clients []*websocket.Conn
	for _, id := range []string{"a", "b", "c"} {
		server, client := wsPair(t)
		add(t, s, id, server)
		clients = append(clients, client)
	}
	s.Broadcast("/one", nil, 200, nil, []byte("1"))
	s.Broadcast("/two", nil, 200, nil, []byte("2"))
	for i, client := range clients {
		if got := readMessage(t, client).Request.URI; got != "/one" {
			t.Errorf("client %d first message = %s, want /one", i, got)
		}
		if got := readMessage(t, client).Request.URI; got != "/two" {
			t.Errorf("client %d second message = %s, want /two", i, got)
		}
		expectNoMessage(t, client)
	}
}

func TestBroadcastWithoutSocketsDoesNothing(t *testing.T) {
	s := newSockets()
	within(t, "Broadcast", func() { s.Broadcast("/x", nil, 200, nil, nil) })
}

func TestBroadcastDoesNotBlockOnAFullStream(t *testing.T) {
	s := newSockets()
	full := &Socket{id: "full", stream: make(chan *SocketMessage, 2)}
	s.sockets["full"] = full
	within(t, "Broadcast", func() {
		for _, uri := range []string{"/1", "/2", "/3", "/4"} {
			s.Broadcast(uri, nil, 200, nil, nil)
		}
	})
	if len(full.stream) != 2 {
		t.Fatalf("stream has %d messages, want the 2 that fit", len(full.stream))
	}
	if first, second := (<-full.stream).Request.URI, (<-full.stream).Request.URI; first != "/1" || second != "/2" {
		t.Errorf("messages = %s, %s, want the first two, with the ones that did not fit dropped", first, second)
	}
}

func TestBroadcastKeepsTheBodyAsText(t *testing.T) {
	s := newSockets()
	sock := &Socket{id: "x", stream: make(chan *SocketMessage, 1)}
	s.sockets["x"] = sock
	body := []byte("original")
	s.Broadcast("/", nil, 200, nil, body)
	copy(body, "changed!")
	if got := (<-sock.stream).Response.Body; got != "original" {
		t.Errorf("body = %q, want the body as it was", got)
	}
}

func TestRemoveSocketClosesTheConnection(t *testing.T) {
	s := newSockets()
	server, client := wsPair(t)
	add(t, s, "a", server)
	s.RemoveSocket("a")
	if _, ok := s.GetSocket("a"); ok {
		t.Error("the socket is still there after it was removed")
	}
	expectClosed(t, client)
	// Messages go nowhere after that.
	within(t, "Broadcast", func() { s.Broadcast("/x", nil, 200, nil, nil) })
}

func TestRemoveUnknownSocketDoesNothing(t *testing.T) {
	s := newSockets()
	within(t, "RemoveSocket", func() { s.RemoveSocket("nobody") })
	server, _ := wsPair(t)
	add(t, s, "a", server)
	s.RemoveSocket("b")
	if !s.HasOpenSockets() {
		t.Error("removing another ID took out the socket")
	}
}

func TestRunOfUnknownSocketReturns(t *testing.T) {
	s := newSockets()
	within(t, "Run", func() { s.Run("nobody") })
}

func TestRunTakesTheSocketOutWhenTheClientIsGone(t *testing.T) {
	s := newSockets()
	server, _ := wsPair(t)
	sock := &Socket{id: "a", conn: server, stream: make(chan *SocketMessage, 1)}
	s.sockets["a"] = sock
	done := make(chan struct{})
	go func() { s.Run("a"); close(done) }()
	// Writing fails on a connection that is closed.
	server.Close()
	sock.stream <- &SocketMessage{}
	select {
	case <-done:
	case <-time.After(deadline):
		t.Fatal("Run did not return after a write failed")
	}
	if _, ok := s.GetSocket("a"); ok {
		t.Error("the socket is still there after its write failed")
	}
}

func TestAddSocketRunsTheSocket(t *testing.T) {
	// A message that is broadcast is written out by what AddSocket started, even when it was added a moment ago.
	s := newSockets()
	for i := 0; i < 3; i++ {
		server, client := wsPair(t)
		id := string(rune('a' + i))
		add(t, s, id, server)
		s.Broadcast("/"+id, nil, 204, nil, nil)
		if got := readMessage(t, client); got.Request.URI != "/"+id || got.Response.StatusCode != 204 {
			t.Errorf("message = %+v", got)
		}
	}
}
