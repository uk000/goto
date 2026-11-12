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

package transport

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/net/http2"
	"google.golang.org/grpc"
)

type IHTTPRequestIntercept interface {
	InterceptRequest(r *http.Request)
}

type IHTTPResponseIntercept interface {
	InterceptResponse(r *http.Response)
}

type ITransportIntercept interface {
	GetTLSConfig() *tls.Config
	SetTLSConfig(*tls.Config)
	GetOpenConnectionCount() int
	GetDialer() *net.Dialer
	AsHTTP() IHTTPTransportIntercept
}

type IHTTPTransportIntercept interface {
	ITransportIntercept
	http.RoundTripper
	SetRequestIntercept(r IHTTPRequestIntercept)
	SetResponseIntercept(r IHTTPResponseIntercept)
	Original() any
}

type BaseTransportIntercept struct {
	Dialer       net.Dialer
	ConnCount    int
	lock         sync.RWMutex
	tlsConfigPtr **tls.Config
}

type HTTPTransportIntercept struct {
	*http.Transport
	*BaseTransportIntercept
	requestIntercept  IHTTPRequestIntercept
	responseIntercept IHTTPResponseIntercept
}

type HTTP2TransportIntercept struct {
	*http2.Transport
	*BaseTransportIntercept
	requestIntercept  IHTTPRequestIntercept
	responseIntercept IHTTPResponseIntercept
}

type GRPCIntercept struct {
	*BaseTransportIntercept
	dialOpts []grpc.DialOption
}

// NewHTTPTransportInterceptWithWatch wraps an *http.Transport and hooks its dial, so that new connections are counted by a ConnTracker and announced by label on newConnNotifierChan.
func NewHTTPTransportInterceptWithWatch(orig any, label string, newConnNotifierChan chan string, watcher IConnWatcher) *HTTPTransportIntercept {
	ht := orig.(*http.Transport)
	t := &HTTPTransportIntercept{
		Transport:              ht,
		BaseTransportIntercept: &BaseTransportIntercept{},
	}
	t.Dialer.Timeout = 5 * time.Second
	t.tlsConfigPtr = &ht.TLSClientConfig
	dialer := t.getDialer()
	t.Transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if conn, err := dialer(ctx, network, addr); err == nil {
			if newConnNotifierChan != nil {
				newConnNotifierChan <- label
			}
			return NewConnTrackerWithWatch(conn, t.BaseTransportIntercept, watcher)
		} else {
			return nil, err
		}
	}
	return t
}

// NewHTTPTransportIntercept is NewHTTPTransportInterceptWithWatch without a watcher.
func NewHTTPTransportIntercept(orig any, label string, newConnNotifierChan chan string) *HTTPTransportIntercept {
	return NewHTTPTransportInterceptWithWatch(orig, label, newConnNotifierChan, nil)
}

// NewHTTP2TransportIntercept wraps an *http2.Transport and hooks its dial, so that new connections are counted by a ConnTracker and announced by label on newConnNotifierChan.
func NewHTTP2TransportIntercept(orig any, label string, newConnNotifierChan chan string) *HTTP2TransportIntercept {
	ht := orig.(*http2.Transport)
	t := &HTTP2TransportIntercept{
		Transport:              ht,
		BaseTransportIntercept: &BaseTransportIntercept{},
	}
	t.Dialer.Timeout = 5 * time.Second
	t.tlsConfigPtr = &ht.TLSClientConfig
	dialer := t.getDialer()
	t.Transport.DialTLS = func(network, addr string, cfg *tls.Config) (net.Conn, error) {
		if conn, err := dialer(context.Background(), network, addr, cfg); err == nil {
			if newConnNotifierChan != nil {
				newConnNotifierChan <- label
			}
			return NewConnTracker(conn, t.BaseTransportIntercept)
		} else {
			return nil, err
		}
	}
	return t
}

// AsHTTP returns the intercept itself as an IHTTPTransportIntercept.
func (t *HTTPTransportIntercept) AsHTTP() IHTTPTransportIntercept {
	return t
}

// Original returns the wrapped *http.Transport.
func (t *HTTPTransportIntercept) Original() any {
	return t.Transport
}

// getDialer returns the dial function that the transport has (DialContext, else Dial), falling back to the dialer of the intercept.
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

// RoundTrip calls the request intercept, sends the request on the wrapped transport, and calls the response intercept with the response.
func (t *HTTPTransportIntercept) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.requestIntercept != nil {
		t.requestIntercept.InterceptRequest(req)
	}
	resp, err := t.Transport.RoundTrip(req)
	if resp != nil && t.responseIntercept != nil {
		t.responseIntercept.InterceptResponse(resp)
	}
	return resp, err
}

// SetRequestIntercept sets the hook that is called with each request before it is sent.
func (t *HTTPTransportIntercept) SetRequestIntercept(ri IHTTPRequestIntercept) {
	t.requestIntercept = ri
}

// SetResponseIntercept sets the hook that is called with each response that is received.
func (t *HTTPTransportIntercept) SetResponseIntercept(ri IHTTPResponseIntercept) {
	t.responseIntercept = ri
}

// getDialer returns the DialTLSContext of the transport if it has one, falling back to the dialer of the intercept.
func (t *HTTP2TransportIntercept) getDialer() func(context.Context, string, string, *tls.Config) (net.Conn, error) {
	if t.DialTLSContext != nil {
		return t.DialTLSContext
	}
	return func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
		return t.Dialer.DialContext(ctx, network, addr)
	}
}

// RoundTrip calls the request intercept, sends the request on the wrapped transport, and calls the response intercept with the response.
func (t *HTTP2TransportIntercept) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.requestIntercept != nil {
		t.requestIntercept.InterceptRequest(req)
	}
	resp, err := t.Transport.RoundTrip(req)
	if resp != nil && t.responseIntercept != nil {
		t.responseIntercept.InterceptResponse(resp)
	}
	return resp, err
}

// RoundTripOpt is RoundTrip with the given HTTP/2 options, and calls the same intercepts.
func (t *HTTP2TransportIntercept) RoundTripOpt(req *http.Request, opt http2.RoundTripOpt) (*http.Response, error) {
	if t.requestIntercept != nil {
		t.requestIntercept.InterceptRequest(req)
	}
	resp, err := t.Transport.RoundTripOpt(req, opt)
	if resp != nil && t.responseIntercept != nil {
		t.responseIntercept.InterceptResponse(resp)
	}
	return resp, err
}

// SetRequestIntercept sets the hook that is called with each request before it is sent.
func (t *HTTP2TransportIntercept) SetRequestIntercept(ri IHTTPRequestIntercept) {
	t.requestIntercept = ri
}

// SetResponseIntercept sets the hook that is called with each response that is received.
func (t *HTTP2TransportIntercept) SetResponseIntercept(ri IHTTPResponseIntercept) {
	t.responseIntercept = ri
}

// Original returns the wrapped *http2.Transport.
func (t *HTTP2TransportIntercept) Original() any {
	return t.Transport
}

// GetOpenConnectionCount returns the number of tracked connections that are open.
func (t *BaseTransportIntercept) GetOpenConnectionCount() int {
	t.lock.RLock()
	defer t.lock.RUnlock()
	return t.ConnCount
}

// GetTLSConfig returns the TLS config of the wrapped transport.
func (t *BaseTransportIntercept) GetTLSConfig() *tls.Config {
	return *(t.tlsConfigPtr)
}

// SetTLSConfig replaces the TLS config of the wrapped transport.
func (t *BaseTransportIntercept) SetTLSConfig(tlsConfig *tls.Config) {
	*(t.tlsConfigPtr) = tlsConfig
}

// GetDialer returns the dialer of the intercept.
func (t *BaseTransportIntercept) GetDialer() *net.Dialer {
	return &t.Dialer
}

// AsHTTP returns nil, as a base intercept is not an HTTP transport intercept.
func (t *BaseTransportIntercept) AsHTTP() IHTTPTransportIntercept {
	return nil
}
