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
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
)

// The handshake types below are configuration that is shared by all the connections of a listener (or a client), so
// the handshake never writes to them: the direction and the position of a step are passed down the calls instead.

type PreambleWrite struct {
	Preamble string `json:"preamble,omitempty"`
}

type PreambleRead struct {
	Length   uint32 `json:"len,omitempty"`
	NextSize bool   `json:"nextSize,omitempty"`
	Collect  bool   `json:"collect,omitempty"`
}

type HandshakeStep struct {
	Read  *PreambleRead  `json:"read,omitempty"`
	Write *PreambleWrite `json:"write,omitempty"`
}

type TCPHandshake struct {
	Protos []string         `json:"protos,omitempty"`
	Seq    []*HandshakeStep `json:"seq,omitempty"`
}

type ALPN struct {
	Protos      []string      `json:"protos,omitempty"`
	KeepDefault bool          `json:"keepDefault,omitempty"`
	Handshake   *TCPHandshake `json:"handshake,omitempty"`
}

func NewALPN(protos []string, handshake *TCPHandshake) *ALPN {
	return &ALPN{
		Protos:    protos,
		Handshake: handshake,
	}
}

// HandleServer performs the server side of the handshake on the connection if the negotiated protocol is one that
// the handshake is meant for (or if it isn't restricted to any protocols).
func (a *ALPN) HandleServer(conn net.Conn, state *tls.ConnectionState) (preamble []byte, err error) {
	if a.Handshake == nil {
		return nil, nil
	}
	if len(a.Handshake.Protos) > 0 {
		for _, proto := range a.Handshake.Protos {
			if state.NegotiatedProtocol == proto {
				return a.handle(conn, true)
			}
		}
	} else {
		return a.handle(conn, true)
	}
	return nil, nil
}

// Handle performs the client side of the handshake on the connection.
func (a *ALPN) Handle(conn net.Conn) (preamble []byte, err error) {
	return a.handle(conn, false)
}

func (a *ALPN) handle(conn net.Conn, server bool) (preamble []byte, err error) {
	if a.Handshake == nil {
		return nil, nil
	}
	var nextSize uint32
	sizeRead := false
	for i, h := range a.Handshake.Seq {
		if h.Read != nil && !sizeRead {
			nextSize = h.Read.Length
		}
		nextSize, preamble, err = h.handle(conn, nextSize, i, server)
		if err != nil {
			return nil, err
		}
		if h.Read != nil && h.Read.NextSize {
			sizeRead = true
		}
	}
	return
}

func (h *HandshakeStep) Handle(conn net.Conn, readSize uint32) (nextSize uint32, data []byte, err error) {
	return h.handle(conn, readSize, 0, false)
}

func (h *HandshakeStep) handle(conn net.Conn, readSize uint32, index int, server bool) (nextSize uint32, data []byte, err error) {
	if h.Read != nil {
		log.Printf("TCPHandshake[%s]: Reading [%d] Bytes\n", side(server), readSize)
		nextSize, data, err = h.Read.handle(conn, readSize, index, server)
	} else if h.Write != nil {
		log.Printf("TCPHandshake[%s]: Writing [%s]\n", side(server), h.Write.Preamble)
		err = h.Write.handle(conn, index, server)
	}
	return
}

func side(server bool) string {
	if server {
		return "server"
	}
	return "client"
}

func (p *PreambleRead) Handle(conn net.Conn, size uint32) (uint32, []byte, error) {
	return p.handle(conn, size, 0, false)
}

func (p *PreambleRead) handle(conn net.Conn, size uint32, index int, server bool) (uint32, []byte, error) {
	var b []byte
	if size > 0 {
		b = make([]byte, size)
	} else {
		b = make([]byte, p.Length)
	}
	if p.NextSize && len(b) < 4 {
		return 0, nil, fmt.Errorf("a read that delivers the size of the next read needs 4 bytes, but is for [%d]", len(b))
	}
	n, err := io.ReadFull(conn, b)
	if err != nil {
		return 0, nil, err
	}
	log.Printf("TCPHandshake[%s]: Read Frame[%d], Size [%d], Data [%s]\n", side(server), index, n, string(b))
	if p.NextSize {
		return binary.BigEndian.Uint32(b), nil, nil
	} else if p.Collect {
		return 0, b, nil
	}
	return 0, nil, nil
}

func (p *PreambleWrite) Handle(conn net.Conn) (err error) {
	return p.handle(conn, 0, false)
}

func (p *PreambleWrite) handle(conn net.Conn, index int, server bool) (err error) {
	data, err := hex.DecodeString(p.Preamble)
	if err != nil {
		return fmt.Errorf("Failed to decode preamble: %v\n", err)
	}
	_, err = conn.Write(data)
	if err != nil {
		conn.Close()
		return err
	}
	log.Printf("TCPHandshake[%s]: Wrote Frame[%d], Size [%d]\n", side(server), index, len(data))
	return nil
}
