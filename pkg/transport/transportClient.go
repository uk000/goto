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
	UpdateTLSConfig(sni string, tlsVersion uint16, verify bool, alpn []string)
	UpdateTLSCerts(rootCAs *x509.CertPool, cert *tls.Certificate)
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
	http.RoundTripper
	*http.Client
	GrpcConn           *grpc.ClientConn
	TransportIntercept ITransportIntercept
	GRPCIntercept      *GRPCIntercept
	SNI                string
	TLSVersion         uint16
	isH2               bool
	PeerCertInfo       *gototls.PeerCertInfo
}

func (ct *DefaultClientTransport) UpdateTransport(c *http.Client, gc *grpc.ClientConn, intercept ITransportIntercept, grpcIntercept *GRPCIntercept, isH2 bool) {
	ct.Client = c
	ct.GrpcConn = gc
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

func (c *DefaultClientTransport) UpdateTLSConfig(sni string, tlsVersion uint16, verify bool, alpn []string) {
	tlsConfig := c.GetTLSConfig()
	if c.SNI != tlsConfig.ServerName || c.TLSVersion != tlsConfig.MinVersion {
		c.CloseIdleConnections()
	}
	tlsConfig.InsecureSkipVerify = !verify
	tlsConfig.ServerName = sni
	tlsConfig.MinVersion = tlsVersion
	tlsConfig.MaxVersion = tlsVersion
	// handler := c.TransportIntercept.GetALPNHandler("h2")
	// if handler != nil {
	// 	for _, a := range alpn {
	// 		c.TransportIntercept.SetALPNHandler(a, handler)
	// 	}
	// }
	tlsConfig.NextProtos = append(tlsConfig.NextProtos, alpn...)
}

func (c *DefaultClientTransport) UpdateTLSCerts(rootCAs *x509.CertPool, cert *tls.Certificate) {
	c.CloseIdleConnections()
	tlsConfig := c.GetTLSConfig()
	if rootCAs != nil {
		tlsConfig.RootCAs = rootCAs
	}
	if cert != nil {
		tlsConfig.Certificates = append(tlsConfig.Certificates, *cert)
	}
}

func (c *DefaultClientTransport) StorePeerCertInfo(remoteAddr string, commonName string, dnsNames, uris, issuers []string) {
	if c.PeerCertInfo == nil {
		c.PeerCertInfo = &gototls.PeerCertInfo{}
		c.PeerCertInfo.RemoteAddr = remoteAddr
	}
	c.PeerCertInfo.CommonName = commonName
	c.PeerCertInfo.DNSNames = dnsNames
	c.PeerCertInfo.URIs = uris
	c.PeerCertInfo.Issuers = issuers
}

func (c *DefaultClientTransport) StoreSNI(remoteAddr string, sni, alpn string) {
	if c.PeerCertInfo == nil {
		c.PeerCertInfo = &gototls.PeerCertInfo{}
		c.PeerCertInfo.RemoteAddr = remoteAddr
	}
	c.PeerCertInfo.SNI = sni
	c.PeerCertInfo.NegotiatedALPN = alpn
}

func (c *DefaultClientTransport) StoreALPN(remoteAddr string, alpn []string) {
	if c.PeerCertInfo == nil {
		c.PeerCertInfo = &gototls.PeerCertInfo{}
		c.PeerCertInfo.RemoteAddr = remoteAddr
	}
	c.PeerCertInfo.ALPN = alpn
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
	return CreateHTTPClient(port, label, h2, true, isTLS, noSNI, serverName, 30*time.Second, 30*time.Second, 3*time.Minute, newConnNotifierChan)
}

func CreateHTTPClient(port int, label string, h2, autoUpgrade, isTLS, noSNI bool, serverName string,
	requestTimeout, connTimeout, connIdleTimeout time.Duration, newConnNotifierChan chan string) ClientTransport {
	ct := &DefaultClientTransport{}
	if noSNI {
		serverName = ""
	}
	tlsConfig := &tls.Config{
		InsecureSkipVerify:    true,
		ServerName:            serverName,
		VerifyConnection:      gototls.ExtractSNI(port, label, "", ct.StoreSNI),
		VerifyPeerCertificate: gototls.ExtractPeerCertInfo(port, label, "", ct.StorePeerCertInfo),
		NextProtos:            []string{"h2"},
	}
	dialTLSContext := func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialer := &net.Dialer{Timeout: connTimeout, KeepAlive: connIdleTimeout}
		rawConn, err := dialer.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		tlsConn := tls.Client(rawConn, tlsConfig)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			rawConn.Close()
			return nil, err
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
	ct.UpdateTransport(&http.Client{Timeout: requestTimeout, Transport: ht}, nil, ht, nil, false)
	return ct
}
