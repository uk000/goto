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
	"errors"
	"goto/pkg/constants"
	"goto/pkg/metrics"
	gotogrpc "goto/pkg/rpc/grpc"
	gototls "goto/pkg/tls"
	"goto/pkg/transport"
	"log"
	"net"
	"net/http"
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
	Service        *gotogrpc.GRPCService `json:"service"`
	URL            string                `json:"url"`
	TLSServerName  string                `json:"tlsServerName"`
	Authority      string                `json:"authority"`
	Options        GRPCOptions           `json:"options"`
	tlsConfig      *tls.Config
	tlsCredentials credentials.TransportCredentials
	conn           *grpc.ClientConn
	stub           *grpcdynamic.Stub
	connErrorCount int
}

func CreateGRPCClient(service *gotogrpc.GRPCService, targetService, url, authority, serverName string, options *GRPCOptions) (*GRPCClient, error) {
	client, err := NewGRPCClient(service, url, authority, serverName, options)
	if err != nil {
		return nil, err
	}
	if err := client.Connect(); err != nil {
		return nil, err
	}
	return client, nil
}

func NewGRPCClient(service *gotogrpc.GRPCService, url, authority, serverName string, options *GRPCOptions) (*GRPCClient, error) {
	if serverName == "" {
		serverName = authority
	}
	c := &GRPCClient{
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

func (c *GRPCClient) configureTLS() {
	if c.Options.IsTLS {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: !c.Options.VerifyTLS,
			ServerName:         c.TLSServerName,
			MinVersion:         c.Options.TLSVersion,
			MaxVersion:         c.Options.TLSVersion,
		}
		if c.Options.VerifyTLS {
			tlsConfig.RootCAs = gototls.RootCAs
		}
		if cert, err := gototls.CreateCertificate(constants.DefaultCommonName, ""); err == nil {
			tlsConfig.Certificates = []tls.Certificate{*cert}
		}
		c.SetTLSConfig(tlsConfig)
	} else {
		c.tlsConfig = nil
		c.tlsCredentials = nil
	}
	c.configureDialOptions()
}

func (c *GRPCClient) SetTLSConfig(tlsConfig *tls.Config) {
	c.Options.IsTLS = true
	c.tlsConfig = tlsConfig
	c.tlsCredentials = credentials.NewTLS(c.tlsConfig)
	if c.Authority == "" && c.TLSServerName != "" {
		c.Authority = c.TLSServerName
	}
}

func (c *GRPCClient) UpdateTLSConfig(serverName string, tlsVersion uint16) {
	c.TLSServerName = serverName
	c.Authority = serverName
	c.Options.TLSVersion = tlsVersion
	c.configureTLS()
}

type permanentDialError struct {
	error
}

func (e permanentDialError) Temporary() bool {
	return false
}
func (e permanentDialError) Timeout() bool {
	return false
}

func (c *GRPCClient) WithContextDialer() grpc.DialOption {
	return grpc.WithContextDialer(func(ctx context.Context, address string) (net.Conn, error) {
		if c.connErrorCount > 1 {
			return nil, permanentDialError{error: errors.New("max connection attempt reached")}
		}
		if conn, err := c.Dialer.DialContext(ctx, "tcp", address); err == nil {
			if c.Service != nil {
				metrics.ConnTracker <- c.Service.Name
			}
			return transport.NewConnTracker(conn, &c.BaseTransportIntercept)
		} else {
			c.connErrorCount++
			log.Printf("GRPCClient.WithContextDialer: Failed to dial address [%s] with error: %s\n", address, err.Error())
			return nil, permanentDialError{error: err}
		}
	})
}

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

func (c *GRPCClient) GetOpenConnectionCount() int {
	return c.ConnCount
}

func (c *GRPCClient) GetDialer() *net.Dialer {
	return &c.Dialer
}

func (c *GRPCClient) Transport() transport.ITransportIntercept {
	return c
}

func (c *GRPCClient) Close() {
	if c.conn != nil {
		c.conn.Close()
	}
}

func (c *GRPCClient) HTTP() *http.Client {
	return nil
}

func (c *GRPCClient) GRPC() *grpc.ClientConn {
	return c.conn
}

func (c *GRPCClient) IsGRPC() bool {
	return true
}

func (c *GRPCClient) IsHTTP() bool {
	return false
}

func (c *GRPCClient) IsH2() bool {
	return false
}

func (c *GRPCClient) createContext(headers map[string]string, md metadata.MD) (context.Context, metadata.MD) {
	ctx := context.Background()
	if md != nil {
		ctx = metadata.NewOutgoingContext(ctx, md)
	} else if len(headers) > 0 {
		md = metadata.New(headers)
		ctx = metadata.NewOutgoingContext(ctx, md)
	}
	if c.Options.RequestTimeout == 0 {
		c.Options.RequestTimeout = 5 * time.Minute
	}
	return ctx, md
}

func (c *GRPCClient) Connect() error {
	c.connErrorCount = 0
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
			log.Printf("GRPCClient.Connect: [ERROR] Failed to connect to target [%s] url [%s] with error: %s\n", c.Service.Name, c.URL, err.Error())
			return err
		}
	}
	c.stub = grpcdynamic.NewStub(c.conn)
	return nil
}

func (c *GRPCClient) monitorAndAbort() {
	go func() {
		time.Sleep(1 * time.Second)
		for {
			state := c.conn.GetState()
			if state == connectivity.TransientFailure || state == connectivity.Connecting {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				log.Println("Connection failed. Monitoring deadline to abort retries...")
				if !c.conn.WaitForStateChange(ctx, state) {
					log.Println("Exceeded 5-second retry budget. Force closing gRPC channel pool.")
					c.conn.Close()
					return
				}
			} else {
				return
			}
		}
	}()
}

func (c *GRPCClient) ConnectWithHeadersOrMD(headers map[string]string, md metadata.MD) (context.Context, metadata.MD, error) {
	err := c.Connect()
	if err != nil {
		return nil, nil, err
	}
	ctx, md := c.createContext(headers, md)
	return ctx, md, nil
}

func (c *GRPCClient) LoadServiceMethodFromReflection(serviceName, methodName string) (err error) {
	c.Service, err = gotogrpc.LoadRemoteReflectedServiceV1(c.conn, serviceName, methodName)
	if err != nil {
		c.Service, err = gotogrpc.LoadRemoteReflectedServiceV1Alpha(c.conn, serviceName, methodName)
	}
	return
}

func LoadRemoteReflectedServices(upstream string) (err error) {
	c, err := CreateGRPCClient(nil, "", upstream, "", "", &GRPCOptions{IsTLS: false, VerifyTLS: false})
	if err != nil {
		return err
	}
	return gotogrpc.LoadRemoteReflectedServices(c.conn)
}
