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
	"strings"
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
}

type ClientTLSConfig struct {
	*tls.Config
	RemoteAddress string
}

func (ct *DefaultClientTransport) UpdateTransport(c *http.Client, gc *grpc.ClientConn, h1 *http.Transport, intercept ITransportIntercept, grpcIntercept *GRPCIntercept, isH2 bool) {
	ct.Client = c
	ct.GrpcConn = gc
	ct.h1Transport = h1
	ct.h2Transport = &http2.Transport{
		AllowHTTP:       true,
		TLSClientConfig: h1.TLSClientConfig,
		DialTLS: func(network, addr string, cfg *tls.Config) (net.Conn, error) {
			return h1.DialTLSContext(context.Background(), network, addr)
		},
	}
	ct.TransportIntercept = intercept
	ct.GRPCIntercept = grpcIntercept
	ct.isH2 = isH2
}

func (c *DefaultClientTransport) GetTLSConfig() *tls.Config {
	tlsConfig := c.TransportIntercept.GetTLSConfig()
	if tlsConfig == nil {
		tlsConfig = &tls.Config{}
		c.TransportIntercept.SetTLSConfig(tlsConfig)
	}
	return tlsConfig
}

func (c *DefaultClientTransport) SetTLSConfig(tlsConfig *tls.Config) {
	if tlsConfig != nil {
		if c.SNI != tlsConfig.ServerName || c.TLSVersion != tlsConfig.MinVersion {
			c.CloseIdleConnections()
		}
		c.TransportIntercept.SetTLSConfig(tlsConfig)
		c.SNI = tlsConfig.ServerName
		c.TLSVersion = tlsConfig.MinVersion
	}
}

type opaqueConn struct{ net.Conn }
type errRoundTripper struct{ err error }

func (e *errRoundTripper) RoundTrip(*http.Request) (*http.Response, error) { return nil, e.err }

func (c *DefaultClientTransport) alpnHandler(authority string, conn *tls.Conn) http.RoundTripper {
	log.Printf("Client Port [%d] Label [%s]: TLS Handshake Complete. Negotiated ALPN: %s ---\n", c.port, c.label, conn.ConnectionState().NegotiatedProtocol)
	if c.isH2 {
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

func (c *DefaultClientTransport) UpdateTLSConfig(sni string, tlsVersion uint16, verify bool, alpn *gototls.ALPN) {
	tlsConfig := c.GetTLSConfig()
	if c.SNI != tlsConfig.ServerName || c.TLSVersion != tlsConfig.MinVersion {
		c.CloseIdleConnections()
	}
	tlsConfig.InsecureSkipVerify = !verify
	tlsConfig.ServerName = sni
	tlsConfig.MinVersion = tlsVersion
	tlsConfig.MaxVersion = tlsVersion
	c.alpn = alpn
	if alpn != nil {
		if !alpn.KeepDefault {
			tlsConfig.NextProtos = alpn.Protos
		} else if !c.isH2 {
			tlsConfig.NextProtos = append(alpn.Protos, "http/1.1")
		} else if len(alpn.Protos) > 0 {
			tlsConfig.NextProtos = append(tlsConfig.NextProtos, alpn.Protos...)
		}
		c.h1Transport.TLSNextProto = map[string]func(authority string, conn *tls.Conn) http.RoundTripper{}
		for _, alpn := range tlsConfig.NextProtos {
			c.h1Transport.TLSNextProto[alpn] = c.alpnHandler
		}
	}
}

func (c *DefaultClientTransport) UpdateTLSCerts(rootCAs *x509.CertPool, certs []*tls.Certificate) {
	c.CloseIdleConnections()
	tlsConfig := c.GetTLSConfig()
	if rootCAs != nil {
		tlsConfig.RootCAs = rootCAs
	}
	if certs != nil {
		for _, cert := range certs {
			tlsConfig.Certificates = append(tlsConfig.Certificates, *cert)
		}
		tlsConfig.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return &tlsConfig.Certificates[0], nil
		}
	}
}

func (c *DefaultClientTransport) StorePeerCertInfo(remoteAddr string, commonName string, dnsNames, uris []string, issuer string) {
	if c.PeerCertInfo == nil {
		c.PeerCertInfo = gototls.NewPeerCertInfo(remoteAddr)
		c.PeerCertInfo.RemoteAddr = remoteAddr
	}
	c.PeerCertInfo.Subject = commonName
	c.PeerCertInfo.DNSNames = dnsNames
	c.PeerCertInfo.URIs = uris
	c.PeerCertInfo.Issuer = issuer
}

func (c *DefaultClientTransport) StoreSNI(remoteAddr string, sni, alpn string) {
	if c.PeerCertInfo == nil {
		c.PeerCertInfo = gototls.NewPeerCertInfo(remoteAddr)
		c.PeerCertInfo.RemoteAddr = remoteAddr
	}
	c.PeerCertInfo.SNI = sni
	c.PeerCertInfo.NegotiatedALPN = alpn
}

func (c *DefaultClientTransport) StoreALPN(remoteAddr string, alpn []string) {
	if c.PeerCertInfo == nil {
		c.PeerCertInfo = gototls.NewPeerCertInfo(remoteAddr)
		c.PeerCertInfo.RemoteAddr = remoteAddr
	}
	c.PeerCertInfo.ALPN = alpn
}

func (c *DefaultClientTransport) UpdatePeerStatus(remoteAddr string, finished bool, status string) {
	if c.PeerCertInfo == nil {
		c.PeerCertInfo = gototls.NewPeerCertInfo(remoteAddr)
		c.PeerCertInfo.RemoteAddr = remoteAddr
	}
	c.PeerCertInfo.Finished = finished
	c.PeerCertInfo.Status = append(c.PeerCertInfo.Status, status)
	c.PeerCertInfo.EndAt = time.Now()
}

func (c *DefaultClientTransport) GetPeerCertInfo() *gototls.PeerCertInfo {
	return c.PeerCertInfo
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
	dialTLSContext := func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialer := &net.Dialer{Timeout: connTimeout, KeepAlive: connIdleTimeout}
		rawConn, err := dialer.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		tlsConfig.VerifyConnection = gototls.ExtractSNI(network, "Client-"+label, addr, ct.StoreSNI, ct.StorePeerCertInfo, ct.UpdatePeerStatus)
		tlsConfig.VerifyPeerCertificate = gototls.ExtractPeerCertInfo(network, "Client-"+label, addr, ct.StorePeerCertInfo, ct.UpdatePeerStatus)
		tlsConn := tls.Client(rawConn, tlsConfig)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			rawConn.Close()
			return nil, err
		}
		if ct.alpn != nil {
			if data, err := ct.alpn.Handle(tlsConn); err == nil && data != nil {
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
		TLSClientConfig:       tlsConfig,
	}
	if h2 {
		if err := http2.ConfigureTransport(t); err != nil {
			panic(err)
		}
	}
	ht := NewHTTPTransportIntercept(t, label, newConnNotifierChan)
	ct.UpdateTransport(&http.Client{Timeout: requestTimeout, Transport: ht}, nil, t, ht, nil, h2)
	return ct
}
