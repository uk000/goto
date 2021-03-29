package invocation

import (
  "context"
  "crypto/tls"
  "net"
  "net/http"
  "sync"

  "golang.org/x/net/http2"
)

type TransportTracker struct {
  dialer       net.Dialer
  connCount    int
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
  tracker   *TransportTracker
  closeSync sync.Once
}

type HTTPClientTracker struct {
  *http.Client
  tracker *TransportTracker
}

func (ct *ConnTracker) Close() (err error) {
  err = ct.Conn.Close()
  ct.closeSync.Do(func() {
    ct.tracker.lock.Lock()
    ct.tracker.connCount--
    ct.tracker.lock.Unlock()
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
  return t.dialer.DialContext
}

func (t *HTTP2TransportTracker) getDialer() func(string, string, *tls.Config) (net.Conn, error) {
  if t.DialTLS != nil {
    return t.DialTLS
  }
  return func(network, addr string, cfg *tls.Config) (net.Conn, error) {
    return t.dialer.Dial(network, addr)
  }
}

func NewHTTPTransportTracker(orig *http.Transport) *HTTPTransportTracker {
  t := &HTTPTransportTracker{
    Transport: orig,
  }
  t.tlsConfigPtr = &orig.TLSClientConfig
  dialer := t.getDialer()
  t.Transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
    if conn, err := dialer(ctx, network, addr); err == nil {
      return NewConnTracker(conn, &t.TransportTracker)
    } else {
      return nil, err
    }
  }
  return t
}

func NewHTTP2TransportTracker(orig *http2.Transport) *HTTP2TransportTracker {
  t := &HTTP2TransportTracker{
    Transport: orig,
  }
  t.tlsConfigPtr = &orig.TLSClientConfig
  dialer := t.getDialer()
  t.Transport.DialTLS = func(network, addr string, cfg *tls.Config) (net.Conn, error) {
    if conn, err := dialer(network, addr, cfg); err == nil {
      return NewConnTracker(conn, &t.TransportTracker)
    } else {
      return nil, err
    }
  }
  return t
}

func NewConnTracker(conn net.Conn, t *TransportTracker) (net.Conn, error) {
  t.lock.Lock()
  t.connCount++
  t.lock.Unlock()
  ct := &ConnTracker{
    Conn:    conn,
    tracker: t,
  }
  return ct, nil
}

func NewHTTPClientTracker(c *http.Client, tracker *TransportTracker) *HTTPClientTracker {
  return &HTTPClientTracker{Client: c, tracker: tracker}
}

func (t *TransportTracker) GetOpenConnectionCount() int {
  t.lock.RLock()
  defer t.lock.RUnlock()
  return t.connCount
}

func (t *TransportTracker) SetTLSConfig(tlsConfig *tls.Config) {
  *(t.tlsConfigPtr) = tlsConfig
}
