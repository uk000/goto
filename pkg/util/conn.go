/**
 * Copyright 2021 uk
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
  GrpcConn   *grpc.ClientConn
  Tracker    *TransportTracker
  IsGRPC     bool
  SNI        string
  TLSVersion uint16
}

func (client *ClientTracker) UpdateTLSConfig(sni string, tlsVersion uint16) {
  if client.SNI != sni || client.TLSVersion != tlsVersion {
    client.CloseIdleConnections()
  }
  client.Tracker.SetTLSConfig(&tls.Config{
    InsecureSkipVerify: true,
    ServerName:         sni,
    MinVersion:         tlsVersion,
    MaxVersion:         tlsVersion,
  })
  client.SNI = sni
  client.TLSVersion = tlsVersion
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
