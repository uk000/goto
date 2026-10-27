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
	"testing"
	"time"
)

// A message is written out by the socket after Broadcast returns, while the request goes on and sets the headers of its
// response. The message must have the headers that it was given, and not share the maps with the request, which is a
// concurrent read and write of a map.
func TestBroadcastSnapshotsTheHeaders(t *testing.T) {
	s := newSockets()
	sock := &Socket{id: "x", stream: make(chan *SocketMessage, 1)}
	s.sockets["x"] = sock
	requestHeaders := http.Header{"A": {"1"}}
	responseHeaders := http.Header{"B": {"2"}}
	s.Broadcast("/u", requestHeaders, 200, responseHeaders, []byte("b"))
	requestHeaders.Set("A", "changed")
	requestHeaders.Set("New", "added")
	responseHeaders.Set("B", "changed")
	responseHeaders.Set("Late", "added")
	msg := <-sock.stream
	if got := msg.Request.Headers; len(got) != 1 || got.Get("A") != "1" {
		t.Errorf("request headers = %v, want them as they were when broadcast", got)
	}
	if got := msg.Response.Headers; len(got) != 1 || got.Get("B") != "2" {
		t.Errorf("response headers = %v, want them as they were when broadcast", got)
	}
}

// Filters are added to the maps that a watcher has, which a watcher that is registered did not have: that was a write to a
// nil map.
func TestFiltersCanBeAddedToARegisteredWatcher(t *testing.T) {
	w := newWatch()
	w.addWebhookWatcher("a", "http://a")
	within(t, "addWatcherFilter", func() {
		if !w.addWatcherFilter("a", "/Uri", "H", "v") {
			t.Error("the filter was not added")
		}
	})
	got := w.Watchers["a"]
	if !got.FilterURIs["/uri"] || got.FilterHeaders["H"] != "v" {
		t.Errorf("watcher = %+v", got)
	}
}

// Getting a watcher left it where it was, and removing one took nothing out: the two did each other's job.
func TestGettingAWatcherKeepsItAndRemovingItTakesItOut(t *testing.T) {
	w := newWatch()
	w.addWebhookWatcher("a", "http://a")
	w.getWatcher("a")
	if w.Watchers["a"] == nil {
		t.Fatal("getting a watcher took it out")
	}
	w.removeWatcher("a")
	if w.Watchers["a"] != nil {
		t.Error("removing a watcher left it in")
	}
}

// What runs for a socket stops when the socket is removed, and not at the next ping, which is seconds later, while the
// goroutine and what it holds are left behind.
func TestRemoveSocketStopsWhatRunsForIt(t *testing.T) {
	s := newSockets()
	server, client := wsPair(t)
	sock := &Socket{id: "a", conn: server, stream: make(chan *SocketMessage, 1)}
	s.sockets["a"] = sock
	done := make(chan struct{})
	go func() { s.Run("a"); close(done) }()
	// What runs for the socket is going once it writes what is sent.
	sock.stream <- &SocketMessage{Request: RequestDetails{URI: "/going"}}
	if got := readMessage(t, client).Request.URI; got != "/going" {
		t.Fatalf("message = %s", got)
	}
	s.RemoveSocket("a")
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("Run did not stop after its socket was removed")
	}
}

// A socket that is added under the ID of another one replaces it. What ran for the replaced socket must stop, and must
// not take the new socket out when it does.
func TestReplacedSocketDoesNotTakeItsReplacementOut(t *testing.T) {
	s := newSockets()
	serverA, clientA := wsPair(t)
	sockA := &Socket{id: "same", conn: serverA, stream: make(chan *SocketMessage, 10)}
	s.sockets["same"] = sockA
	doneA := make(chan struct{})
	go func() { s.Run("same"); close(doneA) }()
	// What runs for the socket has got it once it writes what is sent.
	sockA.stream <- &SocketMessage{Request: RequestDetails{URI: "/first"}}
	if got := readMessage(t, clientA).Request.URI; got != "/first" {
		t.Fatalf("message = %s", got)
	}
	serverB, clientB := wsPair(t)
	s.AddSocket("same", serverB)
	t.Cleanup(func() { s.RemoveSocket("same") })
	// If what runs for the replaced socket is still going, it ends on a failed write.
	serverA.Close()
	func() {
		defer func() { recover() }() // The stream of a replaced socket is closed.
		sockA.stream <- &SocketMessage{Request: RequestDetails{URI: "/second"}}
	}()
	select {
	case <-doneA:
	case <-time.After(deadline):
		t.Fatal("what ran for the replaced socket did not stop")
	}
	if current, ok := s.GetSocket("same"); !ok || current == sockA || current.conn != serverB {
		t.Fatalf("socket = %+v, %v, want the one that replaced the first", current, ok)
	}
	s.Broadcast("/third", nil, 200, nil, nil)
	if got := readMessage(t, clientB).Request.URI; got != "/third" {
		t.Errorf("message of the replacement = %s", got)
	}
}
