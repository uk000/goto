/**
 * Copyright 2025 uk
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
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/metrics"
	gotogrpc "goto/pkg/rpc/grpc"
	"goto/pkg/rpc/grpc/protos"
	gototls "goto/pkg/tls"
	"goto/pkg/transport"
	"goto/pkg/util"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/jhump/protoreflect/v2/grpcdynamic"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/dynamicpb"
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

type GRPCResponse struct {
	Status                   int
	EquivalentHTTPStatusCode int
	ResponseHeaders          map[string][]string
	ResponseTrailers         map[string][]string
	ResponsePayload          []byte
	ClientStreamCount        int
	ServerStreamCount        int
}

type GRPCClient struct {
	transport.BaseTransportIntercept
	Service        *gotogrpc.GRPCService `json:"service"`
	URL            string                `json:"url"`
	ServerName     string                `json:"serverName"`
	Authority      string                `json:"authority"`
	Options        GRPCOptions           `json:"options"`
	tlsConfig      *tls.Config
	tlsCredentials credentials.TransportCredentials
	conn           *grpc.ClientConn
}

type GRPCStreams struct {
	clientStreamUnaryServer *grpcdynamic.ClientStream
	serverStream            *grpcdynamic.ServerStream
	bidiStream              *grpcdynamic.BidiStream
}

func (s *GRPCStreams) Context() context.Context {
	if s.clientStreamUnaryServer != nil {
		return s.clientStreamUnaryServer.Context()
	} else if s.serverStream != nil {
		return s.serverStream.Context()
	} else if s.bidiStream != nil {
		return s.bidiStream.Context()
	}
	return context.Background()
}

func (s *GRPCStreams) Method() *gotogrpc.GRPCServiceMethod {
	return gotogrpc.ServiceRegistry.ParseGRPCServiceMethod(s.Context())
}

func (s *GRPCStreams) Header() (metadata.MD, error) {
	if s.clientStreamUnaryServer != nil {
		return s.clientStreamUnaryServer.Header()
	} else if s.serverStream != nil {
		return s.serverStream.Header()
	} else if s.bidiStream != nil {
		return s.bidiStream.Header()
	}
	return nil, errors.New("no stream")
}

func (s *GRPCStreams) Trailer() metadata.MD {
	if s.clientStreamUnaryServer != nil {
		return s.clientStreamUnaryServer.Trailer()
	} else if s.serverStream != nil {
		return s.serverStream.Trailer()
	} else if s.bidiStream != nil {
		return s.bidiStream.Trailer()
	}
	return nil
}

func (s *GRPCStreams) RecvMsg() (proto.Message, error) {
	if s.clientStreamUnaryServer != nil {
		return s.clientStreamUnaryServer.CloseAndReceive()
	} else if s.serverStream != nil {
		return s.serverStream.RecvMsg()
	} else if s.bidiStream != nil {
		return s.bidiStream.RecvMsg()
	}
	return nil, errors.New("no stream")
}

func (s *GRPCStreams) SendMsg(m proto.Message) error {
	if s.clientStreamUnaryServer != nil {
		return s.clientStreamUnaryServer.SendMsg(m)
	} else if s.bidiStream != nil {
		return s.bidiStream.SendMsg(m)
	}
	return errors.New("no stream")
}

func (s *GRPCStreams) Close() (proto.Message, error) {
	if s.clientStreamUnaryServer != nil {
		return s.clientStreamUnaryServer.CloseAndReceive()
	} else if s.bidiStream != nil {
		return nil, s.bidiStream.CloseSend()
	}
	return nil, errors.New("no stream")
}

func CreateGRPCClient(name, url, authority, serverName string, options *GRPCOptions) (transport.TransportClient, error) {
	ep, err := NewGRPCClient(name, url, authority, serverName, options)
	if err != nil {
		return nil, err
	}
	if err := ep.Connect(); err != nil {
		return nil, err
	}
	return ep, nil
}

func NewGRPCClient(name, url, authority, serverName string, options *GRPCOptions) (*GRPCClient, error) {
	if serverName == "" {
		serverName = authority
	}
	service := protos.ProtosRegistry.GetService(name)
	if service == nil {
		return nil, fmt.Errorf("no proto configured for service [%s]", name)
	}
	c := &GRPCClient{
		Service:    service,
		URL:        url,
		ServerName: serverName,
		Authority:  authority,
		Options:    *options,
	}
	c.configureTLS()
	c.configureDialOptions()
	return c, nil
}

func (c *GRPCClient) configureTLS() {
	if c.Options.IsTLS {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: !c.Options.VerifyTLS,
			ServerName:         c.ServerName,
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
	if c.Authority == "" && c.ServerName != "" {
		c.Authority = c.ServerName
	}
}

func (c *GRPCClient) UpdateTLSConfig(serverName string, tlsVersion uint16) {
	c.ServerName = serverName
	c.Authority = serverName
	c.Options.TLSVersion = tlsVersion
	c.configureTLS()
}

func (c *GRPCClient) WithContextDialer() grpc.DialOption {
	return grpc.WithContextDialer(func(ctx context.Context, address string) (net.Conn, error) {
		if conn, err := c.Dialer.DialContext(ctx, "tcp", address); err == nil {
			metrics.ConnTracker <- c.Service.Name
			return transport.NewConnTracker(conn, &c.BaseTransportIntercept)
		} else {
			log.Printf("GRPCClient.NewGRPCClient: Failed to dial address [%s] with error: %s\n", address, err.Error())
			return nil, err
		}
	})
}

func (c *GRPCClient) configureDialOptions() {
	c.Options.DialOptions = append(Manager.options.DialOptions,
		c.WithContextDialer(),
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

func (c *GRPCClient) Transport() transport.TransportIntercept {
	return &c.BaseTransportIntercept
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

func (c *GRPCClient) createContext(headers map[string]string) (context.Context, context.CancelFunc) {
	ctx := context.Background()
	if len(headers) > 0 {
		ctx = metadata.NewOutgoingContext(ctx, metadata.New(headers))
	}
	return context.WithTimeout(ctx, c.Options.ConnectTimeout)
}

func (c *GRPCClient) Connect() error {
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
			log.Printf("GRPCClient.ConnectWithHeaders: Failed to connect to target [%s] url [%s] with error: %s\n", c.Service.Name, c.URL, err.Error())
			return err
		}
	}
	return nil
}

func (c *GRPCClient) ConnectWithHeaders(headers map[string]string) (context.Context, context.CancelFunc, error) {
	err := c.Connect()
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := c.createContext(headers)
	return ctx, cancel, nil
}

func (c *GRPCClient) Invoke(method string, headers map[string]string, payloads [][]byte) (response *GRPCResponse, err error) {
	if c.Service == nil || c.Service.Methods == nil {
		return nil, fmt.Errorf("service [%s] not configured", c.Service.Name)
	}
	m := c.Service.Methods[method]
	if m == nil {
		return nil, fmt.Errorf("method [%s] not found in Service [%s]", method, c.Service.Name)
	}
	grpcMethod := m.(*gotogrpc.GRPCServiceMethod)
	ctx, cancel, err := c.ConnectWithHeaders(headers)
	if err != nil {
		return nil, err
	}
	defer cancel()
	stub := grpcdynamic.NewStub(c.GRPC())
	if grpcMethod.IsUnary {
		return InvokeUnary(grpcMethod, ctx, stub, payloads)
	} else if grpcMethod.IsClientStreaming && grpcMethod.IsServerStreaming {
		return InvokeBidiStream(grpcMethod, ctx, stub, payloads, c.Options.KeepOpen)
	} else if grpcMethod.IsClientStreaming {
		return InvokeClientStream(grpcMethod, ctx, stub, payloads, c.Options.KeepOpen)
	} else if grpcMethod.IsServerStreaming {
		return InvokeServerStream(grpcMethod, ctx, stub, payloads)
	}
	return nil, nil
}

func InvokeUnary(m *gotogrpc.GRPCServiceMethod, ctx context.Context, stub *grpcdynamic.Stub, payloads [][]byte) (response *GRPCResponse, err error) {
	input := dynamicpb.NewMessage(m.InputType())
	if len(payloads) > 0 {
		if err = fillInput(input, payloads[0]); err != nil {
			return
		}
	}
	var respHeaders metadata.MD
	var respTrailers metadata.MD
	output, err := stub.InvokeRpc(ctx, m.PMD, input, grpc.Trailer(&respTrailers), grpc.Header(&respHeaders))
	response = newGRPCResponse(respHeaders, respTrailers, []proto.Message{output}, 0, 0)
	if processResponseStatus(m, response, err) {
		return response, nil
	} else {
		return response, err
	}
}

func InvokeClientStream(m *gotogrpc.GRPCServiceMethod, ctx context.Context, stub *grpcdynamic.Stub, payloads [][]byte, keepOpen time.Duration) (response *GRPCResponse, err error) {
	stream, err := stub.InvokeRpcClientStream(ctx, m.PMD)
	if err != nil {
		log.Printf("GRPCClient.InvokeClientStream: Method [%s] Failed to initiate Client stream with error: %s\n", m.Name, err.Error())
		return nil, err
	}
	inputs := []proto.Message{}
	for _, payload := range payloads {
		input := dynamicpb.NewMessage(m.InputType())
		if err = fillInput(input, payload); err != nil {
			inputs = append(inputs, input)
		}
	}
	var output proto.Message
	output, err = sendToStream(&GRPCStreams{clientStreamUnaryServer: stream}, inputs, keepOpen, nil)
	var respHeaders metadata.MD
	var respTrailers metadata.MD
	if err == nil {
		respHeaders, err = stream.Header()
		respTrailers = stream.Trailer()
	}
	response = newGRPCResponse(respHeaders, respTrailers, []proto.Message{output}, len(payloads), 0)
	if processResponseStatus(m, response, err) {
		return response, nil
	} else {
		return response, err
	}
}

func InvokeServerStream(m *gotogrpc.GRPCServiceMethod, ctx context.Context, stub *grpcdynamic.Stub, payloads [][]byte) (response *GRPCResponse, err error) {
	input := dynamicpb.NewMessage(m.InputType())
	if len(payloads) > 0 {
		if err = fillInput(input, payloads[0]); err != nil {
			return
		}
	}
	stream, err := stub.InvokeRpcServerStream(ctx, m.PMD, input)
	if err != nil {
		log.Printf("GRPCClient.InvokeServerStream: Method [%s] Failed to initiate Server stream with error: %s\n", m.Name, err.Error())
		return
	}
	var output []proto.Message
	output, err = receiveFromStream(&GRPCStreams{serverStream: stream}, nil)
	if err != nil {
		log.Printf("GRPCClient.InvokeServerStream: Method [%s] Failed to initiate Server stream with error: %s\n", m.Name, err.Error())
		return nil, err
	}
	var respHeaders metadata.MD
	respHeaders, err = stream.Header()
	respTrailers := stream.Trailer()
	response = newGRPCResponse(respHeaders, respTrailers, output, 0, len(output))
	if processResponseStatus(m, response, err) {
		return response, nil
	} else {
		return response, err
	}
}

func InvokeBidiStream(m *gotogrpc.GRPCServiceMethod, ctx context.Context, stub *grpcdynamic.Stub, payloads [][]byte, keepOpen time.Duration) (response *GRPCResponse, err error) {
	stream, err := stub.InvokeRpcBidiStream(ctx, m.PMD)
	if err != nil {
		log.Printf("GRPCClient.InvokeBidiStream: Method [%s] Failed to initiate Bidi stream with error: %s\n", m.Name, err.Error())
		return nil, err
	}
	inputs := []proto.Message{}
	for _, payload := range payloads {
		input := dynamicpb.NewMessage(m.InputType())
		if err = fillInput(input, payload); err != nil {
			inputs = append(inputs, input)
		}
	}
	var output []proto.Message
	wg := &sync.WaitGroup{}
	wg.Add(2)
	go func() {
		if out, e := sendToStream(&GRPCStreams{bidiStream: stream}, inputs, keepOpen, wg); e != nil {
			if out != nil {
				output = append(output, out)
			}
			err = e
		}
	}()
	go func() {
		output, err = receiveFromStream(&GRPCStreams{bidiStream: stream}, wg)
	}()
	wg.Wait()
	var respHeaders metadata.MD
	respHeaders, err = stream.Header()
	respTrailers := stream.Trailer()
	response = newGRPCResponse(respHeaders, respTrailers, output, len(payloads), len(output))
	if processResponseStatus(m, response, err) {
		return response, nil
	} else {
		return response, err
	}
}

func fillInput(input *dynamicpb.Message, payload []byte) error {
	if err := protojson.Unmarshal(payload, input); err != nil {
		log.Printf("GRPCClient.newInput: Failed to unmarshal payload into method input type [%s] with error: %s\n", input.Descriptor().FullName(), err.Error())
		return err
	}
	return nil
}

func newGRPCResponse(respHeaders metadata.MD, respTrailers metadata.MD, responsePayloads []proto.Message, clientStreamCount, serverStreamCount int) *GRPCResponse {
	r := &GRPCResponse{
		ResponseHeaders:  respHeaders,
		ResponseTrailers: respTrailers,
	}
	r.ClientStreamCount = clientStreamCount
	r.ServerStreamCount = serverStreamCount
	r.ResponsePayload = []byte(util.ToJSONText(responsePayloads))
	return r
}

func processResponseStatus(m *gotogrpc.GRPCServiceMethod, r *GRPCResponse, err error) bool {
	if status, ok := status.FromError(err); ok {
		r.Status = int(status.Code())
		if r.Status == 0 {
			r.EquivalentHTTPStatusCode = http.StatusOK
		}
		return true
	} else {
		if err != nil {
			log.Printf("GRPCClient.processResponseStatus: Method [%s] GRPC call failed with error: %s\n", m.Name, err.Error())
		} else {
			log.Printf("GRPCClient.processResponseStatus: Method [%s] GRPC call failed without any error!\n", m.Name)
		}
		if status != nil {
			r.Status = int(status.Code())
		}
	}
	return false
}

func receiveFromStream(stream *GRPCStreams, wg *sync.WaitGroup) (output []proto.Message, err error) {
	defer func() {
		if wg != nil {
			wg.Done()
		}
	}()
	for {
		if out, e := stream.RecvMsg(); e == nil {
			if out != nil {
				output = append(output, out)
			}
		} else {
			if e != io.EOF {
				fmt.Printf("Invocation: [ERROR] %s\n", e.Error())
				err = e
			} else if out, e = stream.Close(); out != nil {
				output = append(output, out)
			}
			log.Printf("Invocation: [INFO] Stream closed with %d messages received\n", len(output))
			break
		}
	}
	return
}

func sendToStream(stream *GRPCStreams, payloads []proto.Message, keepOpen time.Duration, wg *sync.WaitGroup) (output proto.Message, err error) {
	defer func() {
		if wg != nil {
			wg.Done()
		}
	}()
	startTime := time.Now()
	for _, payload := range payloads {
		if err = stream.SendMsg(payload); err != nil {
			log.Printf("GRPCClient.sendClientStream: Method [%s] Closing stream due to send error: %s\n", stream.Method().Name, err.Error())
			break
		}
	}
	if keepOpen > 0 {
		sleep := time.Since(startTime)
		if sleep > 0 {
			time.Sleep(sleep)
		}
	}
	if err == nil || err == io.EOF {
		output, err = stream.Close()
	}
	if err != nil {
		log.Printf("GRPCClient.sendClientStream: Method [%s] Error while sending client stream: %s\n", stream.Method().Name, err.Error())
	}
	return
}
