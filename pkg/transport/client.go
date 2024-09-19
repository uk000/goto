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
  "bytes"
  "context"
  "crypto/tls"
  "io"
  "io/ioutil"
  "net"
  "net/http"
  "strings"
  "time"

  "golang.org/x/net/http2"
  "google.golang.org/grpc"
)

type TransportClient interface {
  SetTLSConfig(tlsConfig *tls.Config)
  UpdateTLSConfig(sni string, tlsVersion uint16)
  Transport() TransportIntercept
  Close()
  HTTP() *http.Client
  GRPC() *grpc.ClientConn
  IsGRPC() bool
  IsHTTP() bool
}

type ClientTracker struct {
  *http.Client
  GrpcConn           *grpc.ClientConn
  TransportIntercept *BaseTransportIntercept
  GRPCIntercept      *GRPCIntercept
  SNI                string
  TLSVersion         uint16
}

func NewTransportClient(c *http.Client, gc *grpc.ClientConn, tracker *BaseTransportIntercept, grpcIntercept *GRPCIntercept) TransportClient {
  return &ClientTracker{Client: c, GrpcConn: gc, TransportIntercept: tracker, GRPCIntercept: grpcIntercept}
}

func NewGRPCClient(label string, url string, ctx context.Context, dialOpts []grpc.DialOption, newConnNotifierChan chan string) (TransportClient, error) {
  g := NewGRPCIntercept(label, dialOpts, newConnNotifierChan)
  if conn, err := grpc.DialContext(ctx, url, g.dialOpts...); err == nil {
    return NewTransportClient(nil, conn, nil, g), nil
  } else {
    return nil, err
  }
}

func (c *ClientTracker) SetTLSConfig(tlsConfig *tls.Config) {
}

func (c *ClientTracker) UpdateTLSConfig(sni string, tlsVersion uint16) {
  if c.SNI != sni || c.TLSVersion != tlsVersion {
    c.CloseIdleConnections()
  }
  c.TransportIntercept.SetTLSConfig(&tls.Config{
    InsecureSkipVerify: true,
    ServerName:         sni,
    MinVersion:         tlsVersion,
    MaxVersion:         tlsVersion,
  })
  c.SNI = sni
  c.TLSVersion = tlsVersion
}

func (c *ClientTracker) Close() {
  if c.GrpcConn != nil {
    c.GrpcConn.Close()
    c.GrpcConn = nil
  }
  if c.Client != nil {
    c.CloseIdleConnections()
    c.Client = nil
  }
}

func (c *ClientTracker) Transport() TransportIntercept {
  return c.TransportIntercept
}

func (c *ClientTracker) HTTP() *http.Client {
  return c.Client
}

func (c *ClientTracker) GRPC() *grpc.ClientConn {
  return c.GrpcConn
}

func (c *ClientTracker) IsGRPC() bool {
  return c.GrpcConn != nil
}

func (c *ClientTracker) IsHTTP() bool {
  return c.HTTP() != nil
}

func CreateRequest(method string, url string, headers http.Header, payload []byte, payloadReader io.ReadCloser) (*http.Request, error) {
  if payloadReader == nil {
    if payload == nil {
      payload = []byte{}
    }
    payloadReader = ioutil.NopCloser(bytes.NewReader(payload))
  }
  if req, err := http.NewRequest(method, url, payloadReader); err == nil {
    for h, values := range headers {
      if strings.EqualFold(h, "host") {
        req.Host = values[0]
      }
      req.Header[h] = values
    }
    return req, nil
  } else {
    return nil, err
  }
}

func CreateDefaultHTTPClient(label string, h2, isTLS bool, newConnNotifierChan chan string) TransportClient {
  return CreateHTTPClient(label, h2, true, isTLS, "", 0, 30*time.Second, 30*time.Second, 3*time.Minute, newConnNotifierChan)
}

func CreateHTTPClient(label string, h2, autoUpgrade, isTLS bool, serverName string, tlsVersion uint16,
  requestTimeout, connTimeout, connIdleTimeout time.Duration, newConnNotifierChan chan string) TransportClient {
  var transport http.RoundTripper
  var tracker *BaseTransportIntercept
  if !h2 {
    ht := NewHTTPTransportIntercept(&http.Transport{
      MaxIdleConns:          300,
      MaxIdleConnsPerHost:   300,
      IdleConnTimeout:       connIdleTimeout,
      Proxy:                 http.ProxyFromEnvironment,
      DisableCompression:    true,
      ExpectContinueTimeout: requestTimeout,
      ResponseHeaderTimeout: requestTimeout,
      DialContext: (&net.Dialer{
        Timeout:   connTimeout,
        KeepAlive: connIdleTimeout,
      }).DialContext,
      TLSHandshakeTimeout: connTimeout,
      ForceAttemptHTTP2:   autoUpgrade,
      TLSClientConfig: &tls.Config{
        InsecureSkipVerify: true,
        ServerName:         serverName,
        MinVersion:         tlsVersion,
        MaxVersion:         tlsVersion,
      },
    }, label, newConnNotifierChan)
    tracker = &ht.BaseTransportIntercept
    transport = ht.Transport
  } else {
    tr := &http2.Transport{
      ReadIdleTimeout: connIdleTimeout,
      PingTimeout:     connTimeout,
      AllowHTTP:       true,
      TLSClientConfig: &tls.Config{
        InsecureSkipVerify: true,
        ServerName:         serverName,
        MinVersion:         tlsVersion,
        MaxVersion:         tlsVersion,
      },
    }
    tr.DialTLS = func(network, addr string, cfg *tls.Config) (net.Conn, error) {
      if isTLS {
        return tls.Dial(network, addr, cfg)
      }
      return net.Dial(network, addr)
    }
    h2t := NewHTTP2TransportIntercept(tr, label, newConnNotifierChan)
    tracker = &h2t.BaseTransportIntercept
    transport = h2t.Transport
  }
  return NewTransportClient(&http.Client{Timeout: requestTimeout, Transport: transport}, nil, tracker, nil)
}
