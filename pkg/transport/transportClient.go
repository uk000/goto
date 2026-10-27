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
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	gototls "goto/pkg/tls"
	"goto/pkg/util"
	"io"
	"log"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
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
}

type ClientTLSConfig struct {
	*tls.Config
	RemoteAddress string
}

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
		c.tlsLock.Unlock()
		if closeIdle {
			c.closeIdleConnections()
		}
	}
}

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

func (e *errRoundTripper) RoundTrip(*http.Request) (*http.Response, error) { return nil, e.err }

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
}

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

func (c *DefaultClientTransport) StorePeerCertInfo(remoteAddr string, commonName string, dnsNames, uris []string, issuer string) {
	c.peerLock.Lock()
	defer c.peerLock.Unlock()
	pci := c.peerCertInfoLocked(remoteAddr)
	pci.Subject = commonName
	pci.DNSNames = dnsNames
	pci.URIs = uris
	pci.Issuer = issuer
}

func (c *DefaultClientTransport) StoreSNI(remoteAddr string, sni, alpn string) {
	c.peerLock.Lock()
	defer c.peerLock.Unlock()
	pci := c.peerCertInfoLocked(remoteAddr)
	pci.SNI = sni
	pci.NegotiatedALPN = alpn
}

func (c *DefaultClientTransport) StoreALPN(remoteAddr string, alpn []string) {
	c.peerLock.Lock()
	defer c.peerLock.Unlock()
	c.peerCertInfoLocked(remoteAddr).ALPN = alpn
}

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

func (c *DefaultClientTransport) Transport() ITransportIntercept {
	return c.TransportIntercept
}

func (c *DefaultClientTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	log.Println("ClientTracker Roundtrip called")
	util.PrintCallers(3, "ClientTracker.RoundTrip")
	return c.Client.Transport.RoundTrip(r)
}

func (c *DefaultClientTransport) HTTP() *http.Client {
	return c.Client
}

func (c *DefaultClientTransport) GRPC() *grpc.ClientConn {
	return c.GrpcConn
}

func (c *DefaultClientTransport) IsGRPC() bool {
	return c.GrpcConn != nil
}

func (c *DefaultClientTransport) IsHTTP() bool {
	return c.HTTP() != nil
}

func (c *DefaultClientTransport) IsH2() bool {
	return c.HTTP() != nil && c.isH2
}

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

func CreateDefaultHTTPClient(port int, label string, h2, isTLS, noSNI bool, serverName string, newConnNotifierChan chan string) ClientTransport {
	return CreateHTTPClient(port, label, h2, true, isTLS, noSNI, serverName, 10*time.Minute, 3*time.Minute, 3*time.Minute, newConnNotifierChan)
}

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
		dialer := &net.Dialer{Timeout: connTimeout, KeepAlive: connIdleTimeout}
		rawConn, err := dialer.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		connTLSConfig, alpn := ct.dialTLSConfig()
		connTLSConfig.VerifyConnection = gototls.ExtractSNI(network, "Client-"+label, addr, ct.StoreSNI, ct.StorePeerCertInfo, ct.UpdatePeerStatus)
		connTLSConfig.VerifyPeerCertificate = gototls.ExtractPeerCertInfo(network, "Client-"+label, addr, ct.StorePeerCertInfo, ct.UpdatePeerStatus)
		tlsConn := tls.Client(rawConn, connTLSConfig)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			rawConn.Close()
			return nil, err
		}
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
			Proxy:                 http.ProxyFromEnvironment,
			DisableCompression:    true,
			ExpectContinueTimeout: requestTimeout,
			ResponseHeaderTimeout: requestTimeout,
			DialTLSContext:        dialTLSContext,
			TLSHandshakeTimeout:   connTimeout,
			ForceAttemptHTTP2:     autoUpgrade,
			TLSClientConfig:       tlsConfig.Clone(),
		}
		ht = NewHTTPTransportIntercept(t, label, newConnNotifierChan)
		ct.UpdateTransport(&http.Client{Timeout: requestTimeout, Transport: ht}, nil, t, nil, ht, nil, false)
	}
	return ct
}
