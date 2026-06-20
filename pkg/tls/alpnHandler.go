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

type PreambleWrite struct {
	index    int
	Preamble string `json:"preamble,omitempty"`
	hex      []byte
	server   bool
}

type PreambleRead struct {
	index    int
	Length   uint32 `json:"len,omitempty"`
	NextSize bool   `json:"nextSize,omitempty"`
	Collect  bool   `json:"collect,omitempty"`
	server   bool
}

type HandshakeStep struct {
	Read   *PreambleRead  `json:"read,omitempty"`
	Write  *PreambleWrite `json:"write,omitempty"`
	server bool
}

type TCPHandshake struct {
	Protos []string         `json:"protos,omitempty"`
	Seq    []*HandshakeStep `json:"seq,omitempty"`
	server bool
}

type ALPN struct {
	Protos      []string      `json:"protos,omitempty"`
	KeepDefault bool          `json:"keepDefault,omitempty"`
	Handshake   *TCPHandshake `json:"handshake,omitempty"`
	server      bool
}

func NewALPN(protos []string, handshake *TCPHandshake) *ALPN {
	return &ALPN{
		Protos:    protos,
		Handshake: handshake,
	}
}

func (a *ALPN) HandleServer(conn net.Conn, state *tls.ConnectionState) (preamble []byte, err error) {
	if a.Handshake == nil {
		return nil, nil
	}
	a.server = true
	if len(a.Handshake.Protos) > 0 {
		for _, proto := range a.Handshake.Protos {
			if state.NegotiatedProtocol == proto {
				return a.Handle(conn)
			}
		}
	} else {
		return a.Handle(conn)
	}
	return nil, nil
}

func (a *ALPN) Handle(conn net.Conn) (preamble []byte, err error) {
	if a.Handshake == nil {
		return nil, nil
	}
	var nextSize uint32
	sizeRead := false
	for i, h := range a.Handshake.Seq {
		if h.Read != nil {
			h.Read.index = i
		}
		if h.Write != nil {
			h.Write.index = i
		}
		h.server = a.server
		if h.Read != nil && !sizeRead {
			nextSize = h.Read.Length
		}
		nextSize, preamble, err = h.Handle(conn, nextSize)
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
	if h.Read != nil {
		h.Read.server = h.server
		if h.server {
			log.Printf("TCPHandshake[server]: Reading [%d] Bytes\n", readSize)
		} else {
			log.Printf("TCPHandshake[client]: Reading [%d] Bytes\n", readSize)
		}
		nextSize, data, err = h.Read.Handle(conn, readSize)
	} else if h.Write != nil {
		h.Write.server = h.server
		if h.server {
			log.Printf("TCPHandshake[server]: Writing [%s]\n", h.Write.Preamble)
		} else {
			log.Printf("TCPHandshake[client]: Writing [%s]\n", h.Write.Preamble)
		}
		err = h.Write.Handle(conn)
	}
	return
}

func (p *PreambleRead) Handle(conn net.Conn, size uint32) (uint32, []byte, error) {
	var b []byte
	if size > 0 {
		b = make([]byte, size)
	} else {
		b = make([]byte, p.Length)
	}
	n, err := io.ReadFull(conn, b)
	if err != nil {
		return 0, nil, err
	}
	if p.server {
		log.Printf("TCPHandshake[server]: Read Frame[%d], Size [%d], Data [%s]\n", p.index, n, string(b))
	} else {
		log.Printf("TCPHandshake[client]: Read Frame[%d], Size [%d], Data [%s]\n", p.index, n, string(b))
	}
	if p.NextSize {
		return binary.BigEndian.Uint32(b), nil, nil
	} else if p.Collect {
		return 0, b, nil
	}
	return 0, nil, nil
}

func (p *PreambleWrite) Handle(conn net.Conn) (err error) {
	if p.hex == nil {
		p.hex, err = hex.DecodeString(p.Preamble)
		if err != nil {
			return fmt.Errorf("Failed to decode preamble: %v\n", err)
		}
	}
	_, err = conn.Write(p.hex)
	if err != nil {
		conn.Close()
		return err
	}
	if p.server {
		log.Printf("TCPHandshake[server]: Wrote Frame[%d], Size [%d]\n", p.index, len(p.hex))
	} else {
		log.Printf("TCPHandshake[client]: Wrote Frame[%d], Size [%d]\n", p.index, len(p.hex))
	}
	return nil
}
