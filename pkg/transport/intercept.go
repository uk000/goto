/**
 * Copyright 2024 uk
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

package transport

import (
  "context"
  "crypto/tls"
  "log"
  "net"
  "net/http"
  "sync"

  "golang.org/x/net/http2"
  "google.golang.org/grpc"
)

type TransportIntercept interface {
  SetTLSConfig(tlsConfig *tls.Config)
  GetOpenConnectionCount() int
  GetDialer() *net.Dialer
}

type BaseTransportIntercept struct {
  Dialer       net.Dialer
  ConnCount    int
  lock         sync.RWMutex
  tlsConfigPtr **tls.Config
}

type HTTPTransportIntercept struct {
  *http.Transport
  BaseTransportIntercept
}

type HTTP2TransportIntercept struct {
  *http2.Transport
  BaseTransportIntercept
}

type GRPCIntercept struct {
  BaseTransportIntercept
  dialOpts []grpc.DialOption
}

func NewHTTPTransportIntercept(orig *http.Transport, label string, newConnNotifierChan chan string) *HTTPTransportIntercept {
  t := &HTTPTransportIntercept{
    Transport: orig,
  }
  t.tlsConfigPtr = &orig.TLSClientConfig
  dialer := t.getDialer()
  t.Transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
    if conn, err := dialer(ctx, network, addr); err == nil {
      if newConnNotifierChan != nil {
        newConnNotifierChan <- label
      }
      return NewConnTracker(conn, &t.BaseTransportIntercept)
    } else {
      return nil, err
    }
  }
  return t
}

func NewHTTP2TransportIntercept(orig *http2.Transport, label string, newConnNotifierChan chan string) *HTTP2TransportIntercept {
  t := &HTTP2TransportIntercept{
    Transport: orig,
  }
  t.tlsConfigPtr = &orig.TLSClientConfig
  dialer := t.getDialer()
  t.Transport.DialTLS = func(network, addr string, cfg *tls.Config) (net.Conn, error) {
    if conn, err := dialer(network, addr, cfg); err == nil {
      if newConnNotifierChan != nil {
        newConnNotifierChan <- label
      }
      return NewConnTracker(conn, &t.BaseTransportIntercept)
    } else {
      return nil, err
    }
  }
  return t
}

func NewGRPCIntercept(label string, dialOpts []grpc.DialOption, newConnNotifierChan chan string) *GRPCIntercept {
  g := &GRPCIntercept{
    dialOpts: dialOpts,
  }
  contextDialer := func(ctx context.Context, address string) (net.Conn, error) {
    if conn, err := g.Dialer.DialContext(ctx, "tcp", address); err == nil {
      if newConnNotifierChan != nil {
        newConnNotifierChan <- label
      }
      return NewConnTracker(conn, &g.BaseTransportIntercept)
    } else {
      log.Printf("Failed to dial address [%s] with error: %s\n", address, err.Error())
      return nil, err
    }
  }
  g.dialOpts = append(dialOpts, grpc.WithContextDialer(contextDialer))
  return g
}

func (t *HTTPTransportIntercept) getDialer() func(context.Context, string, string) (net.Conn, error) {
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

func (t *HTTP2TransportIntercept) getDialer() func(string, string, *tls.Config) (net.Conn, error) {
  if t.DialTLS != nil {
    return t.DialTLS
  }
  return func(network, addr string, cfg *tls.Config) (net.Conn, error) {
    return t.Dialer.Dial(network, addr)
  }
}

func (t *BaseTransportIntercept) GetOpenConnectionCount() int {
  t.lock.RLock()
  defer t.lock.RUnlock()
  return t.ConnCount
}

func (t *BaseTransportIntercept) SetTLSConfig(tlsConfig *tls.Config) {
  *(t.tlsConfigPtr) = tlsConfig
}

func (t *BaseTransportIntercept) GetDialer() *net.Dialer {
  return &t.Dialer
}
