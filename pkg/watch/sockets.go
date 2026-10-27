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
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"gopkg.in/yaml.v3"
)

type RequestDetails struct {
	URI     string      `json:"uri"`
	Headers http.Header `json:"headers"`
}
type ResponseDetails struct {
	StatusCode int         `json:"statusCode"`
	Headers    http.Header `json:"headers"`
	Body       string      `json:"body"`
}
type SocketMessage struct {
	Request  RequestDetails  `json:"request"`
	Response ResponseDetails `json:"response"`
}
type Socket struct {
	id     string
	conn   *websocket.Conn
	stream chan *SocketMessage
}
type Sockets struct {
	sockets map[string]*Socket
	lock    sync.Mutex
}

var (
	WebSockets = &Sockets{
		sockets: map[string]*Socket{},
		lock:    sync.Mutex{},
	}
	Upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}
)

func (s *Sockets) HasOpenSockets() bool {
	s.lock.Lock()
	defer s.lock.Unlock()
	return len(s.sockets) > 0
}

func (s *Sockets) AddSocket(id string, conn *websocket.Conn) {
	s.lock.Lock()
	defer s.lock.Unlock()
	if old := s.sockets[id]; old != nil {
		// What runs for a socket of the same ID stops, and it does not take the new socket out when it does.
		log.Printf("Connection replaced for ID: %s\n", id)
		closeSocket(old)
	}
	socket := &Socket{id: id, conn: conn, stream: make(chan *SocketMessage, 100)}
	s.sockets[id] = socket
	log.Printf("Connection added for ID: %s\n", id)
	go s.run(socket)
	log.Printf("Socket for ID %s is now running\n", id)
}

func (s *Sockets) RemoveSocket(id string) {
	s.lock.Lock()
	defer s.lock.Unlock()
	if socket, ok := s.sockets[id]; ok {
		delete(s.sockets, id)
		closeSocket(socket)
	}
	log.Printf("Connection removed for ID: %s\n", id)
}

// removeSocket takes the socket out when it is still the one of its ID, which it is not when another socket has replaced it.
func (s *Sockets) removeSocket(id string, socket *Socket) {
	s.lock.Lock()
	defer s.lock.Unlock()
	if s.sockets[id] == socket {
		delete(s.sockets, id)
		closeSocket(socket)
		log.Printf("Connection removed for ID: %s\n", id)
	}
}

// closeSocket closes the connection of a socket that is not in the map anymore, and its stream, which is what tells what
// runs for the socket to stop. It is called only by the one that took the socket out of the map, with the lock held,
// which is why the stream is closed once, and is not sent to by a broadcast.
func closeSocket(socket *Socket) {
	close(socket.stream)
	if socket.conn != nil {
		socket.conn.Close()
	}
}
func (s *Sockets) GetSocket(id string) (*Socket, bool) {
	s.lock.Lock()
	defer s.lock.Unlock()
	socket, ok := s.sockets[id]
	return socket, ok
}

func (s *Sockets) Run(id string) {
	socket, ok := s.GetSocket(id)
	if !ok {
		log.Printf("No connection found for ID: %s\n", id)
		return
	}
	s.run(socket)
}

func (s *Sockets) run(socket *Socket) {
	id := socket.id
	defer s.removeSocket(id, socket)
	defer socket.conn.Close()
	for {
		select {
		case msg, ok := <-socket.stream:
			if !ok {
				log.Printf("Stream closed for ID: %s\n", id)
				return
			}
			if data, err := yaml.Marshal(msg); err != nil {
				log.Printf("Error marshalling message for ID %s: %v\n", id, err)
			} else {
				log.Printf("Message received for ID %s: %s\n", id, string(data))
				if err := socket.conn.WriteMessage(websocket.TextMessage, data); err != nil {
					log.Printf("Error writing message for ID %s: %v\n", id, err)
					return
				}
				log.Printf("Message sent to ID: %s\n", id)
			}
		case <-time.After(5 * time.Second):
			if err := socket.conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(5*time.Second)); err != nil {
				log.Printf("Error sending ping to ID %s: %v\n", id, err)
				return
			}
			log.Printf("Ping sent to ID: %s\n", id)
		}
	}
}

func (s *Sockets) Broadcast(requestURI string, requestHeaders http.Header, statusCode int, responseHeaders http.Header, body []byte) {
	// The message is written out by the socket after this returns, while the request goes on, and sets the headers of its
	// response, so the message has the headers as they are now.
	data := &SocketMessage{
		Request: RequestDetails{
			URI:     requestURI,
			Headers: requestHeaders.Clone(),
		},
		Response: ResponseDetails{
			StatusCode: statusCode,
			Headers:    responseHeaders.Clone(),
			Body:       string(body),
		},
	}
	s.lock.Lock()
	defer s.lock.Unlock()
	log.Printf("Broadcasting message: %s\n", data.Request.URI)
	for id, socket := range s.sockets {
		select {
		case socket.stream <- data:
			log.Printf("Socket [%s]: Sent Data [uri: %s]\n", id, data.Request.URI)
		default:
			log.Printf("Socket [%s]: Stream full, skipping broadcast\n", id)
		}
	}
}
