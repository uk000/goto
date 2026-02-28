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
	"fmt"
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
	ResponsePayload          []string
	ClientStreamCount        int
	ServerStreamCount        int
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

func (c *GRPCClient) WithContextDialer() grpc.DialOption {
	return grpc.WithContextDialer(func(ctx context.Context, address string) (net.Conn, error) {
		if conn, err := c.Dialer.DialContext(ctx, "tcp", address); err == nil {
			if c.Service != nil {
				metrics.ConnTracker <- c.Service.Name
			}
			return transport.NewConnTracker(conn, &c.BaseTransportIntercept)
		} else {
			log.Printf("GRPCClient.WithContextDialer: Failed to dial address [%s] with error: %s\n", address, err.Error())
			return nil, err
		}
	})
}

func (c *GRPCClient) configureDialOptions() {
	if c.Options.IdleTimeout == 0 {
		c.Options.IdleTimeout = 10 * time.Minute
	}
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

func (c *GRPCClient) createContext(headers map[string]string, md metadata.MD) (context.Context, context.CancelFunc) {
	ctx := context.Background()
	if md != nil {
		ctx = metadata.NewOutgoingContext(ctx, md)
	} else if len(headers) > 0 {
		ctx = metadata.NewOutgoingContext(ctx, metadata.New(headers))
	}
	if c.Options.ConnectTimeout == 0 {
		return ctx, nil
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
			log.Printf("GRPCClient.Connect: [ERROR] Failed to connect to target [%s] url [%s] with error: %s\n", c.Service.Name, c.URL, err.Error())
			return err
		}
	}
	c.stub = grpcdynamic.NewStub(c.GRPC())
	return nil
}

func (c *GRPCClient) ConnectWithHeadersOrMD(headers map[string]string, md metadata.MD) (context.Context, context.CancelFunc, error) {
	err := c.Connect()
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := c.createContext(headers, md)
	return ctx, cancel, nil
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

func (c *GRPCClient) Invoke(method string, headers map[string]string, payloads [][]byte) (response *GRPCResponse, err error) {
	if c.Service == nil || c.Service.Methods == nil {
		return nil, fmt.Errorf("GRPCClient.Invoke: [ERROR] service [%s] not configured", c.Service.Name)
	}
	grpcMethod := c.Service.Methods[method]
	if grpcMethod == nil {
		return nil, fmt.Errorf("GRPCClient.Invoke: [ERROR] method [%s] not found in Service [%s]", method, c.Service.Name)
	}
	ctx, _, err := c.ConnectWithHeadersOrMD(headers, nil)
	if err != nil {
		return nil, err
	}
	if grpcMethod.IsUnary {
		return c.InvokeUnary(grpcMethod, ctx, payloads[0])
	} else if grpcMethod.IsClientStream && grpcMethod.IsServerStream {
		return c.InvokeBidiStream(grpcMethod, payloads, c.Options.KeepOpen)
	} else if grpcMethod.IsClientStream {
		return c.InvokeClientStream(grpcMethod, payloads, c.Options.KeepOpen)
	} else if grpcMethod.IsServerStream {
		return c.InvokeServerStream(grpcMethod, payloads[0])
	}
	return nil, nil
}

func (c *GRPCClient) InvokeRaw(method *gotogrpc.GRPCServiceMethod, md metadata.MD, inputs []proto.Message) (responses []proto.Message, respHeaders metadata.MD, respTrailers metadata.MD, err error) {
	ctx, _, err := c.ConnectWithHeadersOrMD(nil, md)
	if err != nil {
		return
	}
	var input proto.Message
	if len(inputs) > 0 {
		input = inputs[0]
	}
	if method.IsUnary {
		responses, respHeaders, respTrailers, err = c.InvokeUnaryRaw(ctx, method, input)
		if err != nil {
			log.Printf("GRPCClient.InvokeRaw: Service [%s] Method [%s] InvokeUnaryRaw failed with ERROR [%s]\n", method.Service.Name, method.Name, err.Error())
		}
	} else if method.IsClientStream && method.IsServerStream {
		responses, respHeaders, respTrailers, err = c.InvokeBidiStreamRaw(method, inputs)
		if err != nil {
			log.Printf("GRPCClient.InvokeRaw: Service [%s] Method [%s] InvokeBidiStreamRaw failed with ERROR [%s]\n", method.Service.Name, method.Name, err.Error())
		}
	} else if method.IsClientStream {
		responses, respHeaders, respTrailers, err = c.InvokeClientStreamRaw(method, inputs)
		if err != nil {
			log.Printf("GRPCClient.InvokeRaw: Service [%s] Method [%s] InvokeClientStreamRaw failed with ERROR [%s]\n", method.Service.Name, method.Name, err.Error())
		}
	} else if method.IsServerStream {
		responses, respHeaders, respTrailers, err = c.InvokeServerStreamRaw(method, input)
		if err != nil {
			log.Printf("GRPCClient.InvokeRaw: Service [%s] Method [%s] InvokeServerStreamRaw failed with ERROR [%s]\n", method.Service.Name, method.Name, err.Error())
		}
	}
	return
}

func (c *GRPCClient) OpenStream(port int, method *gotogrpc.GRPCServiceMethod, md metadata.MD, input proto.Message) (stream gotogrpc.GRPCStream, err error) {
	ctx, _, err := c.ConnectWithHeadersOrMD(nil, md)
	if err != nil {
		return
	}
	if method.IsClientStream && method.IsServerStream {
		if bs, e := c.stub.InvokeRpcBidiStream(ctx, method.PMD); e == nil {
			stream = gotogrpc.NewGRPCStreamForClient(port, method, nil, nil, bs)
			if input != nil {
				stream.Send(input)
			}
		} else {
			err = e
		}
	} else if method.IsClientStream {
		if cs, e := c.stub.InvokeRpcClientStream(ctx, method.PMD); e == nil {
			stream = gotogrpc.NewGRPCStreamForClient(port, method, cs, nil, nil)
			if input != nil {
				stream.Send(input)
			}
		} else {
			err = e
		}
	} else if method.IsServerStream {
		if ss, e := c.stub.InvokeRpcServerStream(ctx, method.PMD, input); e == nil {
			stream = gotogrpc.NewGRPCStreamForClient(port, method, nil, ss, nil)
		} else {
			err = e
		}
	}
	return
}

func (c *GRPCClient) InvokeUnary(m *gotogrpc.GRPCServiceMethod, ctx context.Context, payload []byte) (response *GRPCResponse, err error) {
	input := dynamicpb.NewMessage(m.InputType())
	if len(payload) > 0 {
		if err = fillInput(input, payload); err != nil {
			return
		}
	}
	var respHeaders metadata.MD
	var respTrailers metadata.MD
	output, err := c.stub.InvokeRpc(ctx, m.PMD, input, grpc.Trailer(&respTrailers), grpc.Header(&respHeaders))
	response = newGRPCResponse(respHeaders, respTrailers, []proto.Message{output}, 0, 0)
	if processResponseStatus(m, response, err) {
		return response, nil
	} else {
		return response, err
	}
}

func (c *GRPCClient) InvokeUnaryRaw(ctx context.Context, m *gotogrpc.GRPCServiceMethod, input proto.Message) (responses []proto.Message, respHeaders metadata.MD, respTrailers metadata.MD, err error) {
	respHeaders = metadata.MD{}
	respTrailers = metadata.MD{}
	r, e := c.stub.InvokeRpc(ctx, m.PMD, input, grpc.Header(&respHeaders), grpc.Trailer(&respTrailers))
	responses = []proto.Message{r}
	err = e
	return
}

func (c *GRPCClient) InvokeClientStream(m *gotogrpc.GRPCServiceMethod, payloads [][]byte, keepOpen time.Duration) (response *GRPCResponse, err error) {
	responses, respHeaders, respTrailers, err := c.internalInvokeClientStream(m, nil, payloads, keepOpen)
	response = newGRPCResponse(respHeaders, respTrailers, responses, len(payloads), 0)
	if processResponseStatus(m, response, err) {
		return response, nil
	} else {
		return response, err
	}
}

func (c *GRPCClient) InvokeClientStreamRaw(m *gotogrpc.GRPCServiceMethod, messages []proto.Message) (responses []proto.Message, respHeaders metadata.MD, respTrailers metadata.MD, err error) {
	return c.internalInvokeClientStream(m, messages, nil, 0)
}

func (c *GRPCClient) internalInvokeClientStream(m *gotogrpc.GRPCServiceMethod, messages []proto.Message, payloads [][]byte, keepOpen time.Duration) (responses []proto.Message, respHeaders metadata.MD, respTrailers metadata.MD, err error) {
	var input proto.Message
	input, messages, payloads, err = c.createFirstInput(m, messages, payloads)
	if err != nil {
		log.Printf("GRPCClient.InvokeClientStream: Service [%s] Method [%s] [ERROR] Failed to create stream input with error: %s\n", m.Service.Name, m.Name, err.Error())
		return nil, nil, nil, err
	}
	stream, err := c.OpenStream(0, m, nil, input)
	if err != nil {
		log.Printf("GRPCClient.InvokeClientStream: Service [%s] Method [%s] [ERROR] Failed to initiate Client stream with error: %s\n", m.Service.Name, m.Name, err.Error())
		return nil, nil, nil, err
	}
	if keepOpen > 0 {
		stream.KeepOpen(keepOpen)
	}
	var output proto.Message
	if messages != nil {
		output, _, err = stream.SendMulti(messages)
	} else {
		output, _, err = stream.SendPayloads(payloads)
	}
	if err == nil {
		respHeaders, err = stream.Headers()
		respTrailers = stream.Trailers()
		responses = []proto.Message{output}
	}
	return

}

func (c *GRPCClient) InvokeServerStream(m *gotogrpc.GRPCServiceMethod, payload []byte) (response *GRPCResponse, err error) {
	input := dynamicpb.NewMessage(m.InputType())
	if len(payload) > 0 {
		if err = fillInput(input, payload); err != nil {
			return
		}
	}
	responses, respHeaders, respTrailers, err := c.InvokeServerStreamRaw(m, input)
	response = newGRPCResponse(respHeaders, respTrailers, responses, 1, len(responses))
	if processResponseStatus(m, response, err) {
		return response, nil
	} else {
		return response, err
	}
}

func (c *GRPCClient) InvokeServerStreamRaw(m *gotogrpc.GRPCServiceMethod, input proto.Message) (responses []proto.Message, respHeaders metadata.MD, respTrailers metadata.MD, err error) {
	stream, err := c.OpenStream(0, m, nil, input)
	if err != nil {
		log.Printf("GRPCClient.InvokeServerStreamRaw: Service [%s] Method [%s] [ERROR] Failed to initiate Server stream with error: %s\n", m.Service.Name, m.Name, err.Error())
		return
	}
	in := make(chan proto.Message)
	go func() {
		receiveCount, e := stream.TeeStreamReceive(in, nil)
		if e != nil {
			err = e
			log.Printf("GRPCClient.InvokeServerStream: Service [%s] Method [%s] Failed to initiate Server stream with error: %s\n", m.Service.Name, m.Name, err.Error())
		} else {
			log.Printf("GRPCClient.InvokeServerStream: Service [%s] Method [%s] stream received [%d] messages\n", m.Service.Name, m.Name, receiveCount)
		}
	}()
	for msg := range in {
		responses = append(responses, msg)
	}
	respHeaders, err = stream.Headers()
	respTrailers = stream.Trailers()
	return
}

func (c *GRPCClient) InvokeBidiStream(m *gotogrpc.GRPCServiceMethod, payloads [][]byte, keepOpen time.Duration) (response *GRPCResponse, err error) {
	output, respHeaders, respTrailers, err := c.internalInvokeBidiStream(m, nil, payloads, keepOpen)
	response = newGRPCResponse(respHeaders, respTrailers, output, len(payloads), len(output))
	if processResponseStatus(m, response, err) {
		return response, nil
	} else {
		return response, err
	}
}

func (c *GRPCClient) InvokeBidiStreamRaw(m *gotogrpc.GRPCServiceMethod, messages []proto.Message) (responses []proto.Message, respHeaders metadata.MD, respTrailers metadata.MD, err error) {
	return c.internalInvokeBidiStream(m, messages, nil, 0)
}

func (c *GRPCClient) internalInvokeBidiStream(m *gotogrpc.GRPCServiceMethod, messages []proto.Message, payloads [][]byte, keepOpen time.Duration) (responses []proto.Message, respHeaders metadata.MD, respTrailers metadata.MD, err error) {
	// var input proto.Message
	// input, messages, payloads, err = c.createFirstInput(m, messages, payloads)
	stream, err := c.OpenStream(0, m, nil, nil)
	if err != nil {
		log.Printf("GRPCClient.InvokeBidiStreamRaw: Service [%s] Method [%s] [ERROR] Failed to initiate Bidi stream with error: %s\n", m.Service.Name, m.Name, err.Error())
		return
	}
	if keepOpen > 0 {
		stream.KeepOpen(keepOpen)
	}
	var finalResponse proto.Message
	var sendError, recvError error
	in := make(chan proto.Message)
	go func() {
		if messages != nil {
			finalResponse, _, sendError = stream.SendMulti(messages)
		} else {
			finalResponse, _, sendError = stream.SendPayloads(payloads)
		}
		if sendError != nil {
			log.Printf("GRPCClient.InvokeBidiStreamRaw: Service [%s] Method [%s] Failed to send to Bidi stream with error: %s. Closing stream.\n", m.Service.Name, m.Name, sendError.Error())
			stream.Close()
		}
	}()
	go func() {
		receiveCount, e := stream.TeeStreamReceive(in, nil)
		if e == nil {
			log.Printf("GRPCClient.InvokeBidiStreamRaw: Service [%s] Method [%s] stream received [%d] messages\n", m.Service.Name, m.Name, receiveCount)
		} else {
			recvError = e
			log.Printf("GRPCClient.InvokeBidiStreamRaw: Service [%s] Method [%s] Failed to initiate Bidi stream with error: %s\n", m.Service.Name, m.Name, err.Error())
		}
	}()
	stream.Wait()
	for msg := range in {
		log.Printf("GRPCClient.InvokeBidiStreamRaw: Received message [%+v] from Service [%s] Method [%s] stream\n", msg, m.Service.Name, m.Name)
		responses = append(responses, msg)
	}
	if sendError != nil {
		err = sendError
	} else if recvError != nil {
		err = recvError
	} else {
		if finalResponse != nil {
			responses = append(responses, finalResponse)
		}
	}
	h, e := stream.Headers()
	if e == nil {
		respHeaders = h
	} else {
		err = e
	}
	t := stream.Trailers()
	if t != nil {
		respTrailers = t
	}
	return
}

func (c *GRPCClient) createFirstInput(m *gotogrpc.GRPCServiceMethod, messages []proto.Message, payloads [][]byte) (proto.Message, []proto.Message, [][]byte, error) {
	var input proto.Message
	if len(messages) > 0 {
		input = messages[0]
		messages = messages[1:]
	} else if len(payloads) > 0 && len(payloads[0]) > 0 {
		msg := dynamicpb.NewMessage(m.InputType())
		if err := fillInput(msg, payloads[0]); err != nil {
			return nil, nil, nil, err
		}
		input = msg
		payloads = payloads[1:]
	}
	return input, messages, payloads, nil
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
	for _, resp := range responsePayloads {
		if resp != nil {
			if b, err := protojson.Marshal(resp); err == nil {
				r.ResponsePayload = append(r.ResponsePayload, string(b))
			}
		}
	}
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
