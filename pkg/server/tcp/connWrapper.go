package tcp

import (
	"fmt"
	"io"
	"log"
	"net"
	"sync"
)

type WrappedConn struct {
	net.Conn
	localAddr net.Addr
}

type TCPHTTPAdapterListener interface {
	Accept() (net.Conn, error)
	Close() error
	Addr() net.Addr
	Listener() net.Listener
	Self(TCPHTTPAdapterListener)
}

type TCPHTTPAdapterBaseListener struct {
	Conn net.Conn
	once sync.Once
	self TCPHTTPAdapterListener
}

type TCPHTTPAdapterClosingListener struct {
	TCPHTTPAdapterListener
}

type TCPHTTPAdapterNonClosingListener struct {
	TCPHTTPAdapterListener
}

// NewWrappedConn wraps a connection so that its local address reports the given port.
func NewWrappedConn(conn net.Conn, port int) *WrappedConn {
	host, _, _ := net.SplitHostPort(conn.LocalAddr().String())
	addr, _ := net.ResolveTCPAddr("tcp", net.JoinHostPort(host, fmt.Sprintf("%d", port)))
	return &WrappedConn{Conn: conn, localAddr: addr}
}

// NewClosingListener makes a single-use listener that hands out the connection once, and closes it on Close.
func NewClosingListener(conn net.Conn) *TCPHTTPAdapterClosingListener {
	l := &TCPHTTPAdapterClosingListener{TCPHTTPAdapterListener: &TCPHTTPAdapterBaseListener{Conn: conn}}
	l.Self(l)
	return l
}

// NewNonClosingListener makes a single-use listener that hands out the connection once, and leaves it open on Close.
func NewNonClosingListener(conn net.Conn) *TCPHTTPAdapterNonClosingListener {
	l := &TCPHTTPAdapterNonClosingListener{TCPHTTPAdapterListener: NewClosingListener(conn)}
	l.Self(l)
	return l
}

// Self sets the listener that this base listener presents itself as.
func (l *TCPHTTPAdapterBaseListener) Self(self TCPHTTPAdapterListener) {
	l.self = self
}

// Accept gives the connection on the first call, and io.EOF on every call after.
func (l *TCPHTTPAdapterBaseListener) Accept() (net.Conn, error) {
	var c net.Conn
	l.once.Do(func() {
		c = l.Conn
	})
	if c != nil {
		return c, nil
	}
	return nil, io.EOF
}

// Addr gives the local address of the connection.
func (l *TCPHTTPAdapterBaseListener) Addr() net.Addr {
	return l.Conn.LocalAddr()
}

// Close closes the connection.
func (l *TCPHTTPAdapterBaseListener) Close() error {
	log.Println("TCPHTTPAdapterBaseListener: Close")
	return l.Conn.Close()
}

// Listener gives the listener that this one presents itself as.
func (l *TCPHTTPAdapterBaseListener) Listener() net.Listener {
	return l.self
}

// Close does nothing, so that the connection stays open.
func (l *TCPHTTPAdapterNonClosingListener) Close() error {
	log.Println("TCPHTTPAdapterNonClosingListener: Close")
	return nil
}

// Close closes the connection through the wrapped listener.
func (l *TCPHTTPAdapterClosingListener) Close() error {
	log.Println("TCPHTTPAdapterClosingListener: Close")
	return l.TCPHTTPAdapterListener.Close()
}

// LocalAddr gives the local address with the port that the connection was wrapped with.
func (c *WrappedConn) LocalAddr() net.Addr {
	return c.localAddr
}

// Close closes the underlying connection.
func (c *WrappedConn) Close() error {
	log.Printf("LoggingConn: Close called on %v -> %v", c.Conn.LocalAddr(), c.Conn.RemoteAddr())
	return c.Conn.Close()
}
