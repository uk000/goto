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

package grpcclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"goto/pkg/global"
	"goto/pkg/metrics"
	gotogrpc "goto/pkg/rpc/grpc"
	gototls "goto/pkg/tls"
	"goto/pkg/transport"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jhump/protoreflect/v2/grpcdynamic"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
)

type GRPCOptions struct {
	IsTLS          bool              `json:"isTLS"`
	VerifyTLS      bool              `json:"verifyTLS"`
	TLSVersion     uint16            `json:"tlsVersion"`
	ConnectTimeout time.Duration     `json:"connectTimeout"`
	IdleTimeout    time.Duration     `json:"idleTimeout"`
	RequestTimeout time.Duration     `json:"requestTimeout"`
	KeepOpen       time.Duration     `json:"keepOpen"`
	DialOptions    []grpc.DialOption `json:"dialOptions"`
}

type GRPCClient struct {
	transport.BaseTransportIntercept
	Label          string                `json:"label"`
	ClientPort     int                   `json:"clientPort"`
	Service        *gotogrpc.GRPCService `json:"service"`
	URL            string                `json:"url"`
	TLSServerName  string                `json:"tlsServerName"`
	Authority      string                `json:"authority"`
	Options        GRPCOptions           `json:"options"`
	tlsConfig      *tls.Config
	tlsCredentials credentials.TransportCredentials
	PeerCertInfo   *gototls.PeerCertInfo
	// connLock guards conn, stub and the request timeout default, which concurrent calls on the client all use
	connLock       sync.Mutex
	conn           *grpc.ClientConn
	stub           *grpcdynamic.Stub
	connErrorCount atomic.Int32
}

// CreateGRPCClient creates a gRPC client and connects it.
func CreateGRPCClient(label string, clientPort int, service *gotogrpc.GRPCService, targetService, url, authority, serverName string, options *GRPCOptions) (*GRPCClient, error) {
	client, err := NewGRPCClient(label, clientPort, service, url, authority, serverName, options)
	if err != nil {
		return nil, err
	}
	if err := client.Connect(); err != nil {
		return nil, err
	}
	return client, nil
}

// NewGRPCClient creates an unconnected gRPC client, with TLS configured from the options.
func NewGRPCClient(label string, clientPort int, service *gotogrpc.GRPCService, url, authority, serverName string, options *GRPCOptions) (*GRPCClient, error) {
	if options == nil {
		options = &GRPCOptions{}
	}
	if serverName == "" {
		serverName = authority
	}
	c := &GRPCClient{
		Label:         label,
		ClientPort:    clientPort,
		Service:       service,
		URL:           url,
		TLSServerName: serverName,
		Authority:     authority,
		Options:       *options,
	}
	c.Dialer.Timeout = 5 * time.Second
	c.configureTLS()
	return c, nil
}

// configureTLS builds the TLS config and credentials when TLS is enabled (clearing them otherwise) and refreshes the dial options.
func (c *GRPCClient) configureTLS() {
	if c.Options.IsTLS {
		tlsConfig := &tls.Config{
			InsecureSkipVerify:    !c.Options.VerifyTLS,
			ServerName:            c.TLSServerName,
			MinVersion:            c.Options.TLSVersion,
			MaxVersion:            c.Options.TLSVersion,
			VerifyConnection:      gototls.ExtractSNI(strconv.Itoa(c.ClientPort), "Client-"+c.Label, "", c.StoreSNI, c.StorePeerCertInfo, c.UpdatePeerStatus),
			VerifyPeerCertificate: gototls.ExtractPeerCertInfo(strconv.Itoa(c.ClientPort), "Client-"+c.Label, "", c.StorePeerCertInfo, c.UpdatePeerStatus),
		}
		if c.Options.VerifyTLS {
			tlsConfig.RootCAs = gototls.RootCAs
		}
		if cert, err := gototls.CreateCertificate([]string{global.ServerConfig.CommonName}, "", ""); err == nil {
			tlsConfig.Certificates = []tls.Certificate{*cert}
		}
		c.SetTLSConfig(tlsConfig)
	} else {
		c.tlsConfig = nil
		c.tlsCredentials = nil
	}
	c.configureDialOptions()
}

// SetTLSConfig enables TLS on the client with the given config.
func (c *GRPCClient) SetTLSConfig(tlsConfig *tls.Config) {
	c.Options.IsTLS = true
	c.tlsConfig = tlsConfig
	c.tlsCredentials = credentials.NewTLS(c.tlsConfig)
	if c.Authority == "" && c.TLSServerName != "" {
		c.Authority = c.TLSServerName
	}
}

// UpdateTLSConfig changes the TLS server name, version and verification, and reconfigures TLS.
func (c *GRPCClient) UpdateTLSConfig(serverName string, tlsVersion uint16, verify bool, alpn *gototls.ALPN) {
	c.TLSServerName = serverName
	c.Authority = serverName
	c.Options.TLSVersion = tlsVersion
	c.Options.VerifyTLS = verify
	c.configureTLS()
}

// UpdateTLSCerts is a no-op.
func (c *GRPCClient) UpdateTLSCerts(rootCAs *x509.CertPool, cert []*tls.Certificate) {
}

// StorePeerCertInfo records the certificate details of the peer.
func (c *GRPCClient) StorePeerCertInfo(remoteAddr string, commonName string, dnsNames, uris []string, issuer string) {
	if c.PeerCertInfo == nil {
		c.PeerCertInfo = gototls.NewPeerCertInfo(remoteAddr)
		c.PeerCertInfo.RemoteAddr = remoteAddr
	}
	c.PeerCertInfo.Subject = commonName
	c.PeerCertInfo.DNSNames = dnsNames
	c.PeerCertInfo.URIs = uris
	c.PeerCertInfo.Issuer = issuer
}

// StoreSNI records the SNI and the negotiated ALPN of the connection.
func (c *GRPCClient) StoreSNI(remoteAddr string, sni, alpn string) {
	if c.PeerCertInfo == nil {
		c.PeerCertInfo = gototls.NewPeerCertInfo(remoteAddr)
		c.PeerCertInfo.RemoteAddr = remoteAddr
	}
	c.PeerCertInfo.SNI = sni
	c.PeerCertInfo.NegotiatedALPN = alpn
}

// StoreALPN records the ALPN protocols offered by the peer.
func (c *GRPCClient) StoreALPN(remoteAddr string, alpn []string) {
	if c.PeerCertInfo == nil {
		c.PeerCertInfo = gototls.NewPeerCertInfo(remoteAddr)
		c.PeerCertInfo.RemoteAddr = remoteAddr
	}
	c.PeerCertInfo.ALPN = alpn
}

// UpdatePeerStatus records the handshake progress and end time of the peer connection.
func (c *GRPCClient) UpdatePeerStatus(remoteAddr string, finished bool, status string) {
	if c.PeerCertInfo == nil {
		c.PeerCertInfo = gototls.NewPeerCertInfo(remoteAddr)
		c.PeerCertInfo.RemoteAddr = remoteAddr
	}
	c.PeerCertInfo.Finished = finished
	c.PeerCertInfo.Status = append(c.PeerCertInfo.Status, status)
	c.PeerCertInfo.EndAt = time.Now()
}

// GetPeerCertInfo returns the recorded peer certificate info.
func (c *GRPCClient) GetPeerCertInfo() *gototls.PeerCertInfo {
	return c.PeerCertInfo
}

type permanentDialError struct {
	error
}

// Temporary reports the dial error as not temporary.
func (e permanentDialError) Temporary() bool {
	return false
}

// Timeout reports the dial error as not a timeout.
func (e permanentDialError) Timeout() bool {
	return false
}

// WithContextDialer gives a dial option that tracks connections and fails permanently after repeated dial errors.
func (c *GRPCClient) WithContextDialer() grpc.DialOption {
	return grpc.WithContextDialer(func(ctx context.Context, address string) (net.Conn, error) {
		if c.connErrorCount.Load() > 1 {
			return nil, permanentDialError{error: errors.New("max connection attempt reached")}
		}
		if conn, err := c.Dialer.DialContext(ctx, "tcp", address); err == nil {
			if name := c.serviceName(); name != "" {
				metrics.ConnTracker <- name
			}
			return transport.NewConnTracker(conn, &c.BaseTransportIntercept)
		} else {
			c.connErrorCount.Add(1)
			log.Printf("GRPCClient.WithContextDialer: Failed to dial address [%s] with error: %s\n", address, err.Error())
			return nil, permanentDialError{error: err}
		}
	})
}

// configureDialOptions builds the client's dial options from the manager defaults, authority, keepalive and TLS settings.
func (c *GRPCClient) configureDialOptions() {
	if c.Options.IdleTimeout == 0 {
		c.Options.IdleTimeout = 10 * time.Minute
	}
	c.Options.DialOptions = append(Manager.options.DialOptions,
		c.WithContextDialer(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDisableRetry(),
		grpc.WithMaxCallAttempts(1),
		grpc.WithAuthority(c.Authority),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:    c.Options.IdleTimeout / 2,
			Timeout: c.Options.IdleTimeout / 2,
		}))
	if c.Options.IsTLS {
		c.Options.DialOptions = append(c.Options.DialOptions, grpc.WithTransportCredentials(c.tlsCredentials))
	} else {
		c.Options.DialOptions = append(c.Options.DialOptions, grpc.WithInsecure())
	}
}

// GetOpenConnectionCount returns the number of open connections of the client.
func (c *GRPCClient) GetOpenConnectionCount() int {
	return c.ConnCount
}

// GetDialer returns the dialer that the client uses to connect.
func (c *GRPCClient) GetDialer() *net.Dialer {
	return &c.Dialer
}

// Transport returns the client itself as its transport interceptor.
func (c *GRPCClient) Transport() transport.ITransportIntercept {
	return c
}

// Close closes the client's connection if there is one.
func (c *GRPCClient) Close() {
	c.connLock.Lock()
	conn := c.conn
	c.connLock.Unlock()
	if conn != nil {
		conn.Close()
	}
}

// HTTP returns nil, as this is not an HTTP client.
func (c *GRPCClient) HTTP() *http.Client {
	return nil
}

// GRPC returns the client's gRPC connection.
func (c *GRPCClient) GRPC() *grpc.ClientConn {
	c.connLock.Lock()
	defer c.connLock.Unlock()
	return c.conn
}

// serviceName returns the name of the client's service, or empty if it has none.
func (c *GRPCClient) serviceName() string {
	c.connLock.Lock()
	defer c.connLock.Unlock()
	if c.Service != nil {
		return c.Service.Name
	}
	return ""
}

// getStub returns the dynamic stub of the connection.
func (c *GRPCClient) getStub() *grpcdynamic.Stub {
	c.connLock.Lock()
	defer c.connLock.Unlock()
	return c.stub
}

// requestTimeout returns the request timeout of the client's options.
func (c *GRPCClient) requestTimeout() time.Duration {
	c.connLock.Lock()
	defer c.connLock.Unlock()
	return c.Options.RequestTimeout
}

// IsGRPC returns true as this is a gRPC client.
func (c *GRPCClient) IsGRPC() bool {
	return true
}

// IsHTTP returns false as this is not an HTTP client.
func (c *GRPCClient) IsHTTP() bool {
	return false
}

// IsH2 returns false, as the client isn't reported as an HTTP/2 client.
func (c *GRPCClient) IsH2() bool {
	return false
}

// createContext builds the outgoing call context with the given metadata or headers, and defaults the request timeout.
func (c *GRPCClient) createContext(headers map[string]string, md metadata.MD) (context.Context, metadata.MD) {
	ctx := context.Background()
	if md != nil {
		ctx = metadata.NewOutgoingContext(ctx, md)
	} else if len(headers) > 0 {
		md = metadata.New(headers)
		ctx = metadata.NewOutgoingContext(ctx, md)
	}
	c.connLock.Lock()
	if c.Options.RequestTimeout == 0 {
		c.Options.RequestTimeout = 5 * time.Minute
	}
	c.connLock.Unlock()
	return ctx, md
}

// Connect creates the gRPC connection and dynamic stub, replacing a connection that has failed or shut down.
func (c *GRPCClient) Connect() error {
	c.connLock.Lock()
	defer c.connLock.Unlock()
	c.connErrorCount.Store(0)
	if c.conn != nil {
		connState := c.conn.GetState()
		if connState != connectivity.Connecting && connState != connectivity.Idle && connState != connectivity.Ready {
			c.conn.Close()
			c.conn = nil
		}
	}
	if c.conn == nil {
		var err error
		if c.conn, err = grpc.NewClient(c.URL, c.Options.DialOptions...); err != nil {
			serviceName := ""
			if c.Service != nil {
				serviceName = c.Service.Name
			}
			log.Printf("GRPCClient.Connect: [ERROR] Failed to connect to target [%s] url [%s] with error: %s\n", serviceName, c.URL, err.Error())
			c.conn = nil
			return err
		}
		c.stub = grpcdynamic.NewStub(c.conn)
	} else if c.stub == nil {
		c.stub = grpcdynamic.NewStub(c.conn)
	}
	return nil
}

// monitorAndAbort closes the connection if it keeps failing to connect beyond a 5-second retry budget.
func (c *GRPCClient) monitorAndAbort() {
	conn := c.GRPC()
	if conn == nil {
		return
	}
	go func() {
		time.Sleep(1 * time.Second)
		for {
			state := conn.GetState()
			if state == connectivity.TransientFailure || state == connectivity.Connecting {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				log.Println("Connection failed. Monitoring deadline to abort retries...")
				changed := conn.WaitForStateChange(ctx, state)
				cancel()
				if !changed {
					log.Println("Exceeded 5-second retry budget. Force closing gRPC channel pool.")
					conn.Close()
					return
				}
			} else {
				return
			}
		}
	}()
}

// ConnectWithHeadersOrMD connects and returns the outgoing call context and metadata built from the given metadata or headers.
func (c *GRPCClient) ConnectWithHeadersOrMD(headers map[string]string, md metadata.MD) (context.Context, metadata.MD, error) {
	err := c.Connect()
	if err != nil {
		return nil, nil, err
	}
	ctx, md := c.createContext(headers, md)
	return ctx, md, nil
}

// LoadServiceMethodFromReflection loads the service and method from the server's reflection (v1, falling back to v1alpha) as the client's service.
func (c *GRPCClient) LoadServiceMethodFromReflection(serviceName, methodName string) (err error) {
	conn := c.GRPC()
	service, err := gotogrpc.LoadRemoteReflectedServiceV1(conn, serviceName, methodName)
	if err != nil {
		service, err = gotogrpc.LoadRemoteReflectedServiceV1Alpha(conn, serviceName, methodName)
	}
	c.connLock.Lock()
	c.Service = service
	c.connLock.Unlock()
	return
}

// LoadRemoteReflectedServices connects to the upstream and registers the services that its reflection exposes.
func LoadRemoteReflectedServices(upstream string) (err error) {
	c, err := CreateGRPCClient("Reflect", global.Self.ServerPort, nil, "", upstream, "", "", &GRPCOptions{IsTLS: false, VerifyTLS: false})
	if err != nil {
		return err
	}
	defer c.Close()
	return gotogrpc.LoadRemoteReflectedServices(c.GRPC())
}
