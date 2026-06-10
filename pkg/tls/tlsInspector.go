package tls

import (
	"bufio"
	"crypto/tls"
	"net"
)

type TLSInspector struct {
	net.Listener
	tlsConfig *tls.Config
}

func NewTLSInspector(l net.Listener, tlsConfig *tls.Config) net.Listener {
	return &TLSInspector{
		Listener:  l,
		tlsConfig: tlsConfig,
	}
}

func (t *TLSInspector) Accept() (net.Conn, error) {
	c, err := t.Listener.Accept()
	if err != nil {
		return nil, err
	}
	buffered := newBufferedConn(c)

	// 0x16 (22 in decimal) is the standard TLS Handshake record type
	b, err := buffered.Peek(1)
	if err != nil {
		c.Close()
		return nil, err
	}
	if b[0] == 0x16 {
		return tls.Server(buffered, t.tlsConfig), nil
	}
	return buffered, nil
}

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func newBufferedConn(c net.Conn) *bufferedConn {
	return &bufferedConn{
		Conn: c,
		r:    bufio.NewReader(c),
	}
}

func (b *bufferedConn) Read(p []byte) (int, error) {
	return b.r.Read(p)
}

func (b *bufferedConn) Peek(n int) ([]byte, error) {
	return b.r.Peek(n)
}
