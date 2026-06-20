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

func NewWrappedConn(conn net.Conn, port int) *WrappedConn {
	host, _, _ := net.SplitHostPort(conn.LocalAddr().String())
	addr, _ := net.ResolveTCPAddr("tcp", net.JoinHostPort(host, fmt.Sprintf("%d", port)))
	return &WrappedConn{Conn: conn, localAddr: addr}
}

func NewClosingListener(conn net.Conn) *TCPHTTPAdapterClosingListener {
	l := &TCPHTTPAdapterClosingListener{TCPHTTPAdapterListener: &TCPHTTPAdapterBaseListener{Conn: conn}}
	l.Self(l)
	return l
}

func NewNonClosingListener(conn net.Conn) *TCPHTTPAdapterNonClosingListener {
	l := &TCPHTTPAdapterNonClosingListener{TCPHTTPAdapterListener: NewClosingListener(conn)}
	l.Self(l)
	return l
}

func (l *TCPHTTPAdapterBaseListener) Self(self TCPHTTPAdapterListener) {
	l.self = self
}

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

func (l *TCPHTTPAdapterBaseListener) Addr() net.Addr {
	return l.Conn.LocalAddr()
}

func (l *TCPHTTPAdapterBaseListener) Close() error {
	log.Println("TCPHTTPAdapterBaseListener: Close")
	return l.Conn.Close()
}

func (l *TCPHTTPAdapterBaseListener) Listener() net.Listener {
	return l.self
}

func (l *TCPHTTPAdapterNonClosingListener) Close() error {
	log.Println("TCPHTTPAdapterNonClosingListener: Close")
	return nil
}

func (l *TCPHTTPAdapterClosingListener) Close() error {
	log.Println("TCPHTTPAdapterClosingListener: Close")
	return l.TCPHTTPAdapterListener.Close()
}

func (c *WrappedConn) LocalAddr() net.Addr {
	return c.localAddr
}
func (c *WrappedConn) Close() error {
	log.Printf("LoggingConn: Close called on %v -> %v", c.Conn.LocalAddr(), c.Conn.RemoteAddr())
	return c.Conn.Close()
}
