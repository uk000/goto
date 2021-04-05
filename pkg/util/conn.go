package util

import (
  "context"
  "crypto/tls"
  "net"
  "net/http"
  "sync"

  "golang.org/x/net/http2"
  "google.golang.org/grpc"
)

type TransportTracker struct {
  Dialer       net.Dialer
  ConnCount    int
  lock         sync.RWMutex
  tlsConfigPtr **tls.Config
}

type HTTPTransportTracker struct {
  *http.Transport
  TransportTracker
}

type HTTP2TransportTracker struct {
  *http2.Transport
  TransportTracker
}

type ConnTracker struct {
  net.Conn
  Tracker   *TransportTracker
  closeSync sync.Once
}

type ClientTracker struct {
  *http.Client
  GrpcConn *grpc.ClientConn
  Tracker  *TransportTracker
  IsGRPC   bool
}

func (ct *ConnTracker) Close() (err error) {
  err = ct.Conn.Close()
  ct.closeSync.Do(func() {
    ct.Tracker.lock.Lock()
    ct.Tracker.ConnCount--
    ct.Tracker.lock.Unlock()
  })
  return err
}

func (t *HTTPTransportTracker) getDialer() func(context.Context, string, string) (net.Conn, error) {
  if t.DialContext != nil {
    return t.DialContext
  }
  if t.Dial != nil {
    return func(ctx context.Context, network, addr string) (net.Conn, error) {
      return t.Dial(network, addr)
    }
  }
  return t.Dialer.DialContext
}

func (t *HTTP2TransportTracker) getDialer() func(string, string, *tls.Config) (net.Conn, error) {
  if t.DialTLS != nil {
    return t.DialTLS
  }
  return func(network, addr string, cfg *tls.Config) (net.Conn, error) {
    return t.Dialer.Dial(network, addr)
  }
}

func NewHTTPTransportTracker(orig *http.Transport, label string, newConnNotifierChan chan string) *HTTPTransportTracker {
  t := &HTTPTransportTracker{
    Transport: orig,
  }
  t.tlsConfigPtr = &orig.TLSClientConfig
  dialer := t.getDialer()
  t.Transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
    if conn, err := dialer(ctx, network, addr); err == nil {
      if newConnNotifierChan != nil {
        newConnNotifierChan <- label
      }
      return NewConnTracker(conn, &t.TransportTracker)
    } else {
      return nil, err
    }
  }
  return t
}

func NewHTTP2TransportTracker(orig *http2.Transport, label string, newConnNotifierChan chan string) *HTTP2TransportTracker {
  t := &HTTP2TransportTracker{
    Transport: orig,
  }
  t.tlsConfigPtr = &orig.TLSClientConfig
  dialer := t.getDialer()
  t.Transport.DialTLS = func(network, addr string, cfg *tls.Config) (net.Conn, error) {
    if conn, err := dialer(network, addr, cfg); err == nil {
      if newConnNotifierChan != nil {
        newConnNotifierChan <- label
      }
      return NewConnTracker(conn, &t.TransportTracker)
    } else {
      return nil, err
    }
  }
  return t
}

func NewConnTracker(conn net.Conn, t *TransportTracker) (net.Conn, error) {
  t.lock.Lock()
  t.ConnCount++
  t.lock.Unlock()
  ct := &ConnTracker{
    Conn:    conn,
    Tracker: t,
  }
  return ct, nil
}

func NewHTTPClientTracker(c *http.Client, gc *grpc.ClientConn, tracker *TransportTracker) *ClientTracker {
  return &ClientTracker{Client: c, GrpcConn: gc, Tracker: tracker, IsGRPC: gc != nil}
}

func (t *TransportTracker) GetOpenConnectionCount() int {
  t.lock.RLock()
  defer t.lock.RUnlock()
  return t.ConnCount
}

func (t *TransportTracker) SetTLSConfig(tlsConfig *tls.Config) {
  *(t.tlsConfigPtr) = tlsConfig
}
