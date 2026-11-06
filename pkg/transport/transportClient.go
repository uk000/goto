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
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	gototls "goto/pkg/tls"
	"goto/pkg/util"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/http2"
	"google.golang.org/grpc"
)

type ClientTransport interface {
	SetTLSConfig(tlsConfig *tls.Config)
	UpdateTLSConfig(sni string, tlsVersion uint16, verify bool, alpn *gototls.ALPN)
	UpdateTLSCerts(rootCAs *x509.CertPool, certs []*tls.Certificate)
	GetPeerCertInfo() *gototls.PeerCertInfo
	Transport() ITransportIntercept
	Close()
	HTTP() *http.Client
	GRPC() *grpc.ClientConn
	IsGRPC() bool
	IsHTTP() bool
	IsH2() bool
}

// ProxyClient is implemented by the transports that can send their requests via an HTTP proxy.
type ProxyClient interface {
	// SetProxy makes the transport send its requests via the proxy (nil goes back to the default, which is the proxy that
	// the environment has for the request). It is to be called before the requests are made.
	SetProxy(proxyURL *url.URL) error
}

type DefaultClientTransport struct {
	*http.Client
	GrpcConn           *grpc.ClientConn
	TransportIntercept ITransportIntercept
	GRPCIntercept      *GRPCIntercept
	SNI                string
	TLSVersion         uint16
	isH2               bool
	PeerCertInfo       *gototls.PeerCertInfo
	h1Transport        *http.Transport
	h2Transport        *http2.Transport
	port               int
	label              string
	alpn               *gototls.ALPN
	// tlsConfig is the config that the dialers use for the connections they open. It is replaced by a changed copy
	// whenever a request updates it (never changed in place), and is handed out as copies, so that requests running
	// concurrently never share a config that one of them is changing. tlsLock guards the fields below.
	tlsLock       sync.RWMutex
	tlsConfig     *tls.Config
	nextProtosSet bool
	nextProtos    []string
	peerLock      sync.Mutex
	// proxyURL is the proxy that the requests are sent via, if one was set (see SetProxy).
	proxyURL atomic.Pointer[url.URL]
}

type ClientTLSConfig struct {
	*tls.Config
	RemoteAddress string
}

// UpdateTransport replaces the HTTP client, gRPC connection, transports and intercepts that the client transport uses.
func (ct *DefaultClientTransport) UpdateTransport(c *http.Client, gc *grpc.ClientConn, h1 *http.Transport, h2 *http2.Transport, intercept ITransportIntercept, grpcIntercept *GRPCIntercept, isH2 bool) {
	ct.Client = c
	ct.GrpcConn = gc
	ct.h1Transport = h1
	ct.h2Transport = h2
	ct.TransportIntercept = intercept
	ct.GRPCIntercept = grpcIntercept
	ct.isH2 = isH2
}

// currentTLSConfigLocked returns the config that is in use, which is never to be changed by the caller. It requires tlsLock
// to be held for writing, as it sets up the config if there isn't one.
func (c *DefaultClientTransport) currentTLSConfigLocked() *tls.Config {
	if c.tlsConfig == nil {
		if c.TransportIntercept != nil {
			c.tlsConfig = c.TransportIntercept.GetTLSConfig().Clone()
		}
		if c.tlsConfig == nil {
			c.tlsConfig = &tls.Config{}
		}
	}
	return c.tlsConfig
}

// GetTLSConfig returns a copy of the TLS config that the transport uses for its new connections. Changes made to the
// copy do not affect the transport, which is only changed by UpdateTLSConfig, UpdateTLSCerts and SetTLSConfig.
func (c *DefaultClientTransport) GetTLSConfig() *tls.Config {
	c.tlsLock.Lock()
	defer c.tlsLock.Unlock()
	return c.currentTLSConfigLocked().Clone()
}

// SetTLSConfig makes the transport use a copy of the given config for its new connections.
func (c *DefaultClientTransport) SetTLSConfig(tlsConfig *tls.Config) {
	if tlsConfig != nil {
		c.tlsLock.Lock()
		closeIdle := c.SNI != tlsConfig.ServerName || c.TLSVersion != tlsConfig.MinVersion
		c.tlsConfig = tlsConfig.Clone()
		c.nextProtosSet = true
		c.SNI = tlsConfig.ServerName
		c.TLSVersion = tlsConfig.MinVersion
		c.syncProxyTLSLocked()
		c.tlsLock.Unlock()
		if closeIdle {
			c.closeIdleConnections()
		}
	}
}

// closeIdleConnections closes the idle connections of the HTTP client, if there is one.
func (c *DefaultClientTransport) closeIdleConnections() {
	if c.Client != nil {
		c.CloseIdleConnections()
	}
}

// dialTLSConfig returns what a dialer needs for a new connection: a config of its own, that it can set up for the
// connection, and the ALPN handshake to run on it.
func (c *DefaultClientTransport) dialTLSConfig() (*tls.Config, *gototls.ALPN) {
	c.tlsLock.RLock()
	defer c.tlsLock.RUnlock()
	cfg := c.tlsConfig.Clone()
	if cfg == nil {
		cfg = &tls.Config{}
	}
	if !c.nextProtosSet && c.h1Transport != nil && c.h1Transport.TLSClientConfig != nil {
		// The protocols that net/http negotiates for the transport on its own (h2, when it is allowed to upgrade) are
		// set up in the config that the transport was created with, which is not changed by anyone from there on.
		cfg.NextProtos = slices.Clone(c.h1Transport.TLSClientConfig.NextProtos)
	}
	return cfg, c.alpn
}

type opaqueConn struct{ net.Conn }
type errRoundTripper struct{ err error }

// RoundTrip always fails with the error that it holds.
func (e *errRoundTripper) RoundTrip(*http.Request) (*http.Response, error) { return nil, e.err }

// alpnHandler gives the round tripper for a connection that completed TLS: an HTTP/2 client connection when h2 is on, else an HTTP/1 transport that reuses the connection.
func (c *DefaultClientTransport) alpnHandler(authority string, conn *tls.Conn) http.RoundTripper {
	log.Printf("Client Port [%d] Label [%s]: TLS Handshake Complete. Negotiated ALPN: %s ---\n", c.port, c.label, conn.ConnectionState().NegotiatedProtocol)
	if c.isH2 && c.h2Transport != nil {
		cc, err := c.h2Transport.NewClientConn(struct{ net.Conn }{conn})
		if err != nil {
			log.Printf("Client Port [%d] Label %s: NewClientConn failed: %v", c.port, c.label, err)
			return &errRoundTripper{err: err}
		}
		return cc
	}
	return &http.Transport{
		DialTLS: func(network, addr string) (net.Conn, error) {
			return conn, nil
		},
	}
}

// UpdateTLSConfig sets the SNI, TLS version, verification and ALPN that the new connections of the transport are opened
// with. It is called for every request, from requests that run concurrently, so it replaces the config of the transport
// with a changed copy, instead of changing the config that the dialers of other requests may be reading.
func (c *DefaultClientTransport) UpdateTLSConfig(sni string, tlsVersion uint16, verify bool, alpn *gototls.ALPN) {
	c.tlsLock.Lock()
	current := c.currentTLSConfigLocked()
	closeIdle := c.SNI != current.ServerName || c.TLSVersion != current.MinVersion
	tlsConfig := current.Clone()
	tlsConfig.InsecureSkipVerify = !verify
	tlsConfig.ServerName = sni
	tlsConfig.MinVersion = tlsVersion
	tlsConfig.MaxVersion = tlsVersion
	c.alpn = alpn
	if alpn != nil {
		if !alpn.KeepDefault {
			tlsConfig.NextProtos = slices.Clone(alpn.Protos)
		} else if !c.isH2 {
			tlsConfig.NextProtos = append(slices.Clone(alpn.Protos), "http/1.1")
		} else {
			for _, proto := range alpn.Protos {
				if !slices.Contains(tlsConfig.NextProtos, proto) {
					tlsConfig.NextProtos = append(tlsConfig.NextProtos, proto)
				}
			}
		}
		c.nextProtosSet = true
		if c.h1Transport != nil && !slices.Equal(c.nextProtos, tlsConfig.NextProtos) {
			// The transport looks its protocol handlers up while requests are running, so they are replaced only when
			// the protocols change, and not by every request that applies the same ALPN.
			c.nextProtos = slices.Clone(tlsConfig.NextProtos)
			handlers := map[string]func(authority string, conn *tls.Conn) http.RoundTripper{}
			for _, proto := range tlsConfig.NextProtos {
				handlers[proto] = c.alpnHandler
			}
			c.h1Transport.TLSNextProto = handlers
		}
	}
	c.tlsConfig = tlsConfig
	c.syncProxyTLSLocked()
	c.tlsLock.Unlock()
	if closeIdle {
		c.closeIdleConnections()
	}
}

// UpdateTLSCerts sets the root CAs and the client certificates that the new connections of the transport are opened with.
// Applying a certificate that is already there again leaves the config as it is.
func (c *DefaultClientTransport) UpdateTLSCerts(rootCAs *x509.CertPool, certs []*tls.Certificate) {
	c.closeIdleConnections()
	c.tlsLock.Lock()
	defer c.tlsLock.Unlock()
	tlsConfig := c.currentTLSConfigLocked().Clone()
	if rootCAs != nil {
		tlsConfig.RootCAs = rootCAs
	}
	if certs != nil {
		for _, cert := range certs {
			if cert != nil && !hasCertificate(tlsConfig.Certificates, cert) {
				tlsConfig.Certificates = append(tlsConfig.Certificates, *cert)
			}
		}
		if len(tlsConfig.Certificates) > 0 {
			tlsConfig.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
				return &tlsConfig.Certificates[0], nil
			}
		}
	}
	c.tlsConfig = tlsConfig
	c.syncProxyTLSLocked()
}

// ParseProxyURL parses the URL of a proxy, which is an http or https URL. A host (with port) alone is taken as an http proxy.
func ParseProxyURL(proxy string) (*url.URL, error) {
	proxy = strings.TrimSpace(proxy)
	if proxy == "" {
		return nil, errors.New("proxy URL is empty")
	}
	if !strings.Contains(proxy, "://") {
		proxy = "http://" + proxy
	}
	u, err := url.Parse(proxy)
	if err != nil {
		// the URL is left out of the message, as it may have a password
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return nil, fmt.Errorf("invalid proxy URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("proxy URL scheme [%s] is not supported, it must be http or https", u.Scheme)
	}
	if u.Hostname() == "" {
		return nil, errors.New("proxy URL has no host")
	}
	return u, nil
}

// proxyAddress is the address that the connections to the proxy are made to, as net/http works it out for the proxy.
func proxyAddress(proxy *url.URL) string {
	port := proxy.Port()
	if port == "" {
		if proxy.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return net.JoinHostPort(proxy.Hostname(), port)
}

// SetProxy makes the requests of the transport go via the given proxy, through a tunnel that the proxy is asked for
// (CONNECT) for the target of the request, whether it is an https or an http one. Without a proxy, requests go via the
// proxy that the environment has for them, if any, which gets the requests for http targets as proxy requests.
//
// The tunnels for https targets are net/http's own, which takes the TLS settings of the transport as it opens them, so
// SetProxy is to be called before requests are made. The ones for http targets are opened by the dialer of the transport.
func (c *DefaultClientTransport) SetProxy(proxyURL *url.URL) error {
	if proxyURL != nil && c.h1Transport == nil {
		return errors.New("proxy is not supported by the HTTP/2 transport")
	}
	c.proxyURL.Store(proxyURL)
	if c.h1Transport != nil {
		c.tlsLock.Lock()
		c.syncProxyTLSLocked()
		c.tlsLock.Unlock()
		c.closeIdleConnections()
	}
	return nil
}

// proxyFor tells net/http which proxy a request is to go via: the proxy that was set, or else the one from the
// environment. The requests for http targets are not given the proxy that was set: net/http would send them to the proxy
// as proxy requests, and it only asks the proxy for a tunnel for the https ones. They are sent through a tunnel that the
// dialer opens instead (see dialTunnel).
func (c *DefaultClientTransport) proxyFor(req *http.Request) (*url.URL, error) {
	if proxy := c.proxyURL.Load(); proxy != nil {
		if req.URL.Scheme == "http" {
			return nil, nil
		}
		return proxy, nil
	}
	return http.ProxyFromEnvironment(req)
}

// syncProxyTLSLocked makes net/http use the TLS settings of the transport for the connections that it tunnels through the
// proxy. These are not opened by the dialers of the transport: net/http connects to the proxy, asks for the tunnel, and
// does the TLS handshake with the target on its own, using the config of its transport. It requires tlsLock to be held
// for writing, and does nothing unless a proxy is set.
func (c *DefaultClientTransport) syncProxyTLSLocked() {
	if c.h1Transport == nil || c.proxyURL.Load() == nil {
		return
	}
	cfg := c.currentTLSConfigLocked().Clone()
	if !c.nextProtosSet && c.h1Transport.TLSClientConfig != nil {
		cfg.NextProtos = slices.Clone(c.h1Transport.TLSClientConfig.NextProtos)
	}
	// The peer that the connection is made to is the proxy, so the target is what the peer info is kept for.
	target := cfg.ServerName
	cfg.VerifyConnection = func(cs tls.ConnectionState) error {
		return gototls.ExtractSNI("tcp", "Client-"+c.label, target, c.StoreSNI, c.StorePeerCertInfo, c.UpdatePeerStatus)(cs)
	}
	cfg.VerifyPeerCertificate = gototls.ExtractPeerCertInfo("tcp", "Client-"+c.label, target, c.StorePeerCertInfo, c.UpdatePeerStatus)
	c.h1Transport.TLSClientConfig = cfg
}

// proxyTLSConfig is the config for the connection to a proxy that is reached over TLS, which is not the connection to
// the target: it is verified for the proxy, is not offered protocols other than the proxy protocol needs, and is not what
// the peer info is about.
func (c *DefaultClientTransport) proxyTLSConfig(proxy *url.URL) *tls.Config {
	cfg, _ := c.dialTLSConfig()
	cfg.ServerName = proxy.Hostname()
	cfg.NextProtos = nil
	cfg.MinVersion, cfg.MaxVersion = 0, 0
	cfg.VerifyConnection, cfg.VerifyPeerCertificate = nil, nil
	return cfg
}

// tlsToProxy does the TLS handshake with a proxy over the connection to it, which is closed if the handshake fails.
func (c *DefaultClientTransport) tlsToProxy(ctx context.Context, conn net.Conn, proxy *url.URL) (net.Conn, error) {
	start := time.Now()
	tlsConn := tls.Client(conn, c.proxyTLSConfig(proxy))
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		conn.Close()
		return nil, err
	}
	callTimingsOf(ctx).proxyTLSDone(start)
	return tlsConn, nil
}

// dialProxyTLS opens the connection to a proxy that is reached over TLS, for the tunnel that net/http asks it for.
func (c *DefaultClientTransport) dialProxyTLS(ctx context.Context, network, addr string, proxy *url.URL, connTimeout, keepAlive time.Duration) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: connTimeout, KeepAlive: keepAlive}
	rawConn, err := dialer.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	return c.tlsToProxy(ctx, rawConn, proxy)
}

// dialTunnel opens a connection to the target of an http request through the proxy: it connects to the proxy (over TLS
// if the proxy is an https one), asks it for a tunnel to the target, and gives the connection that the tunnel is on. The
// proxy is connected to by the dial function given, which is the one that the transport had.
func (c *DefaultClientTransport) dialTunnel(ctx context.Context, dial func(context.Context, string, string) (net.Conn, error), proxy *url.URL, target string) (net.Conn, error) {
	conn, err := dial(ctx, "tcp", proxyAddress(proxy))
	if err != nil {
		return nil, err
	}
	if proxy.Scheme == "https" {
		if conn, err = c.tlsToProxy(ctx, conn, proxy); err != nil {
			return nil, err
		}
	}
	tunnel, err := openTunnel(ctx, conn, proxy, target)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return tunnel, nil
}

// bufferedConn is a connection that has bytes that were read from it ahead of the reads, to be read first.
type bufferedConn struct {
	net.Conn
	r io.Reader
}

// Read reads from the buffered reader, which gives the bytes that were read ahead before those of the connection.
func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// openTunnel asks the proxy at the other end of the connection for a tunnel to the target, the way net/http does, and
// gives the connection once the proxy has said that the tunnel is there. Anything that the proxy sends right behind its
// answer is the first of what comes through the tunnel. The exchange ends with the context.
func openTunnel(ctx context.Context, conn net.Conn, proxy *url.URL, target string) (net.Conn, error) {
	req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: target}, Host: target, Header: http.Header{}}
	if user := proxy.User; user != nil {
		password, _ := user.Password()
		req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user.Username()+":"+password)))
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Unix(1, 0)) })
	br := bufio.NewReader(conn)
	var resp *http.Response
	err := req.Write(conn)
	if err == nil {
		resp, err = http.ReadResponse(br, req)
	}
	if !stop() {
		// the context was done, which is what made the exchange fail, if it did
		if err == nil {
			resp.Body.Close()
		}
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("failed to ask the proxy for a tunnel to %s: %w", target, err)
	}
	callTimingsOf(ctx).tunnelAnswered(resp.Header)
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("the proxy refused a tunnel to %s: %s", target, resp.Status)
	}
	_ = conn.SetDeadline(time.Time{})
	if n := br.Buffered(); n > 0 {
		ahead := make([]byte, n)
		_, _ = io.ReadFull(br, ahead)
		return &bufferedConn{Conn: conn, r: io.MultiReader(bytes.NewReader(ahead), conn)}, nil
	}
	return conn, nil
}

// hasCertificate tells if the certificate is already in the list.
func hasCertificate(certs []tls.Certificate, cert *tls.Certificate) bool {
	for i := range certs {
		if slices.EqualFunc(certs[i].Certificate, cert.Certificate, bytes.Equal) {
			return true
		}
	}
	return false
}

// peerCertInfoLocked returns the info of the peer that the transport connects to, which requires peerLock to be held.
func (c *DefaultClientTransport) peerCertInfoLocked(remoteAddr string) *gototls.PeerCertInfo {
	if c.PeerCertInfo == nil {
		c.PeerCertInfo = gototls.NewPeerCertInfo(remoteAddr)
		c.PeerCertInfo.RemoteAddr = remoteAddr
	}
	return c.PeerCertInfo
}

// StorePeerCertInfo records the subject, DNS names, URIs and issuer of the peer certificate for the remote address.
func (c *DefaultClientTransport) StorePeerCertInfo(remoteAddr string, commonName string, dnsNames, uris []string, issuer string) {
	c.peerLock.Lock()
	defer c.peerLock.Unlock()
	pci := c.peerCertInfoLocked(remoteAddr)
	pci.Subject = commonName
	pci.DNSNames = dnsNames
	pci.URIs = uris
	pci.Issuer = issuer
}

// StoreSNI records the SNI and the negotiated ALPN of the connection to the remote address.
func (c *DefaultClientTransport) StoreSNI(remoteAddr string, sni, alpn string) {
	c.peerLock.Lock()
	defer c.peerLock.Unlock()
	pci := c.peerCertInfoLocked(remoteAddr)
	pci.SNI = sni
	pci.NegotiatedALPN = alpn
}

// StoreALPN records the ALPN protocols for the remote address.
func (c *DefaultClientTransport) StoreALPN(remoteAddr string, alpn []string) {
	c.peerLock.Lock()
	defer c.peerLock.Unlock()
	c.peerCertInfoLocked(remoteAddr).ALPN = alpn
}

// UpdatePeerStatus records whether the handshake with the remote address finished, and adds a status message to it.
func (c *DefaultClientTransport) UpdatePeerStatus(remoteAddr string, finished bool, status string) {
	c.peerLock.Lock()
	defer c.peerLock.Unlock()
	pci := c.peerCertInfoLocked(remoteAddr)
	pci.Finished = finished
	pci.Status = append(pci.Status, status)
	pci.EndAt = time.Now()
}

// GetPeerCertInfo returns a copy of what the handshakes of the transport found out about its peer, which is not
// affected by the handshakes that follow.
func (c *DefaultClientTransport) GetPeerCertInfo() *gototls.PeerCertInfo {
	c.peerLock.Lock()
	defer c.peerLock.Unlock()
	if c.PeerCertInfo == nil {
		return nil
	}
	pci := *c.PeerCertInfo
	pci.Status = slices.Clone(pci.Status)
	return &pci
}

// Close closes the gRPC connection and the idle connections of the HTTP client, and lets go of both.
func (c *DefaultClientTransport) Close() {
	if c.GrpcConn != nil {
		c.GrpcConn.Close()
		c.GrpcConn = nil
	}
	if c.Client != nil {
		c.CloseIdleConnections()
		c.Client = nil
	}
}

// Transport returns the transport intercept.
func (c *DefaultClientTransport) Transport() ITransportIntercept {
	return c.TransportIntercept
}

// RoundTrip sends the request through the transport of the HTTP client.
func (c *DefaultClientTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	log.Println("ClientTracker Roundtrip called")
	util.PrintCallers(3, "ClientTracker.RoundTrip")
	return c.Client.Transport.RoundTrip(r)
}

// HTTP returns the HTTP client, which is nil for a gRPC transport.
func (c *DefaultClientTransport) HTTP() *http.Client {
	return c.Client
}

// GRPC returns the gRPC connection, which is nil for an HTTP transport.
func (c *DefaultClientTransport) GRPC() *grpc.ClientConn {
	return c.GrpcConn
}

// IsGRPC tells if the transport has a gRPC connection.
func (c *DefaultClientTransport) IsGRPC() bool {
	return c.GrpcConn != nil
}

// IsHTTP tells if the transport has an HTTP client.
func (c *DefaultClientTransport) IsHTTP() bool {
	return c.HTTP() != nil
}

// IsH2 tells if the transport is an HTTP client that uses HTTP/2.
func (c *DefaultClientTransport) IsH2() bool {
	return c.HTTP() != nil && c.isH2
}

// CreateRequest builds an HTTP request with the payload (or the reader) as its body, and sets the headers, using the Host header as the host of the request.
func CreateRequest(method string, url string, headers http.Header, payload []byte, payloadReader io.ReadCloser) (*http.Request, error) {
	if payloadReader == nil {
		if payload == nil {
			payload = []byte{}
		}
		payloadReader = io.NopCloser(bytes.NewReader(payload))
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

// CreateSimpleHTTPClient creates an HTTP client with a pooled transport that uses the proxy of the environment and long (10 minute) timeouts.
func CreateSimpleHTTPClient() *http.Client {
	tr := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     time.Minute * 10,
		Proxy:               http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   time.Minute * 10,
			KeepAlive: time.Minute * 5,
		}).DialContext,
	}
	return &http.Client{Transport: tr, Timeout: 10 * time.Minute}
}

// CreateDefaultHTTPClient creates a client transport with the default timeouts (10m for requests, 3m to connect and idle) and HTTP/2 auto-upgrade.
func CreateDefaultHTTPClient(port int, label string, h2, isTLS, noSNI bool, serverName string, newConnNotifierChan chan string) ClientTransport {
	return CreateHTTPClient(port, label, h2, true, isTLS, noSNI, serverName, 10*time.Minute, 3*time.Minute, 3*time.Minute, newConnNotifierChan)
}

// CreateHTTPClient creates an HTTP/1 or HTTP/2 client transport with intercepts that track connections, a TLS dialer that records the peer cert info and ALPN, and proxy support.
func CreateHTTPClient(port int, label string, h2, autoUpgrade, isTLS, noSNI bool, serverName string,
	requestTimeout, connTimeout, connIdleTimeout time.Duration, newConnNotifierChan chan string) ClientTransport {
	ct := &DefaultClientTransport{
		port:  port,
		label: label,
	}
	if noSNI {
		serverName = ""
	}
	tlsConfig := &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         serverName,
	}
	if h2 {
		tlsConfig.NextProtos = []string{"h2"}
	}
	// The config that the dialers use is kept by the client, which changes it as requests ask for other TLS settings. The
	// transports below get configs of their own, which nobody changes, as they read these while requests are running.
	ct.tlsConfig = tlsConfig
	dialTLSContext := func(ctx context.Context, network, addr string) (net.Conn, error) {
		if proxy := ct.proxyURL.Load(); proxy != nil && proxy.Scheme == "https" && addr == proxyAddress(proxy) {
			return ct.dialProxyTLS(ctx, network, addr, proxy, connTimeout, connIdleTimeout)
		}
		dialer := &net.Dialer{Timeout: connTimeout, KeepAlive: connIdleTimeout}
		rawConn, err := dialer.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		connTLSConfig, alpn := ct.dialTLSConfig()
		connTLSConfig.VerifyConnection = gototls.ExtractSNI(network, "Client-"+label, addr, ct.StoreSNI, ct.StorePeerCertInfo, ct.UpdatePeerStatus)
		connTLSConfig.VerifyPeerCertificate = gototls.ExtractPeerCertInfo(network, "Client-"+label, addr, ct.StorePeerCertInfo, ct.UpdatePeerStatus)
		tlsConn := tls.Client(rawConn, connTLSConfig)
		tlsStart := time.Now()
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			rawConn.Close()
			return nil, err
		}
		callTimingsOf(ctx).targetTLSDone(tlsStart)
		if alpn != nil {
			if data, err := alpn.Handle(tlsConn); err == nil && data != nil {
				if j := util.ProtoToJSON(data); j != nil {
					log.Printf("TransportClient [%d][%s]: Read TLS Handshake Preamble: %s\n", port, label, j.ToJSONText())
				} else {
					log.Printf("TransportClient [%d][%s]: Read TLS Handshake Preamble: %s\n", port, label, util.CleanText(data))
				}
			} else {
				log.Printf("TransportClient [%d][%s]: TLS Handshake Finished with No Preamble. Error: %+v\n", port, label, err)
			}
		}
		return tlsConn, nil
	}
	var ht IHTTPTransportIntercept
	if h2 {
		h2t := &http2.Transport{
			ReadIdleTimeout: connIdleTimeout,
			PingTimeout:     connTimeout,
			AllowHTTP:       true,
			TLSClientConfig: tlsConfig.Clone(),
		}
		h2t.DialTLSContext = func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
			if isTLS {
				return dialTLSContext(ctx, network, addr)
			}
			return net.Dial(network, addr)
		}
		ht = NewHTTP2TransportIntercept(h2t, label, newConnNotifierChan)
		ct.UpdateTransport(&http.Client{Timeout: requestTimeout, Transport: ht}, nil, nil, h2t, ht, nil, true)
		// if err := http2.ConfigureTransport(t); err != nil {
		// 	panic(err)
		// }
	} else {
		t := &http.Transport{
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   100,
			MaxConnsPerHost:       100,
			IdleConnTimeout:       connIdleTimeout,
			Proxy:                 ct.proxyFor,
			DisableCompression:    true,
			ExpectContinueTimeout: requestTimeout,
			ResponseHeaderTimeout: requestTimeout,
			DialTLSContext:        dialTLSContext,
			TLSHandshakeTimeout:   connTimeout,
			ForceAttemptHTTP2:     autoUpgrade,
			TLSClientConfig:       tlsConfig.Clone(),
		}
		ht = NewHTTPTransportIntercept(t, label, newConnNotifierChan)
		// The tunnels that net/http asks the proxy for (for https targets) are timed here, as the ones of http targets are in
		// openTunnel.
		t.OnProxyConnectResponse = func(ctx context.Context, _ *url.URL, _ *http.Request, res *http.Response) error {
			callTimingsOf(ctx).tunnelAnswered(res.Header)
			return nil
		}
		// The dials of net/http to the proxy are its own (for the tunnels that it asks the proxy for, for https targets),
		// and all others are for the targets of http requests, which are reached through a tunnel of the proxy when
		// there is one. An http target that has the address of the proxy can't be told from the proxy.
		tracked := t.DialContext
		t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			if proxy := ct.proxyURL.Load(); proxy != nil && addr != proxyAddress(proxy) {
				return ct.dialTunnel(ctx, tracked, proxy, addr)
			}
			return tracked(ctx, network, addr)
		}
		ct.UpdateTransport(&http.Client{Timeout: requestTimeout, Transport: ht}, nil, t, nil, ht, nil, false)
	}
	return ct
}
