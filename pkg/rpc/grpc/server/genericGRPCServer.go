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

package grpcserver

import (
	"context"
	"fmt"
	gotogrpc "goto/pkg/rpc/grpc"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"log"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/dynamicpb"
)

func init() {
	gotogrpc.GRPCUnaryHandler = GRPCUnaryHandler
	gotogrpc.GRPCStreamHandler = GRPCStreamHandler
}

type GRPCMethodInterceptor struct {
	originalServer  any
	originalHandler grpc.MethodHandler
	serviceMethod   *gotogrpc.GRPCServiceMethod
}

type GRPCStreamInterceptor struct {
	isClientStreaming bool
	isServerStreaming bool
	originalServer    any
	originalHandler   grpc.StreamHandler
	serviceMethod     *gotogrpc.GRPCServiceMethod
}

func invokeMiddlewareChain(ctx context.Context, port int, method *gotogrpc.GRPCServiceMethod, md map[string][]string, body []byte) (*middleware.GrpcHTTPRequestAdapter, *middleware.GrpcHTTPResponseWriterAdapter) {
	return middleware.InvokeMiddlewareChainForGRPC(ctx, port, method.Name, gotogrpc.GetRequestHost(md), method.URI, md, body, method.OutputType())
}

func unaryHandler(ctx context.Context, req interface{}) (resp interface{}, err error) {
	method, port, _, authority, md, err := gotogrpc.CommonHandler(ctx, nil)
	if err != nil {
		util.LogMessage(ctx, err.Error())
		return nil, err
	}
	gotogrpc.LogRequest(ctx, port, method.Service.Name, method.Name, authority, md, 1, 0, "")
	b, err := gotogrpc.ParseRequest(req)
	if err != nil {
		util.LogMessage(ctx, err.Error())
		return
	}
	util.LogMessage(ctx, fmt.Sprintf("Received [%d] bytes", len(b)))

	_, w := invokeMiddlewareChain(ctx, port, method, md, b)
	responseHeaders := w.ToMetadata()
	grpc.SendHeader(ctx, responseHeaders)
	responseCount := 0
	msg := ""
	if len(w.Responses) > 0 {
		msg = fmt.Sprintf("Sending unary response, count [%d]", len(w.Responses))
		responseCount = 1
		resp = w.Responses[0]
	} else {
		msg = "No response to send"
	}
	gotogrpc.LogResponse(ctx, responseHeaders, 200, responseCount, -1, msg)
	return
}

func GRPCUnaryHandler(method *gotogrpc.GRPCServiceMethod) grpc.MethodHandler {
	return func(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
		req := gotogrpc.ReadRequest(method, dec)
		if interceptor == nil {
			return unaryHandler(ctx, req)
		}
		info := &grpc.UnaryServerInfo{
			Server:     srv,
			FullMethod: fmt.Sprintf("/%s/%s", method.Service.Name, string(method.Name)),
		}
		return interceptor(ctx, req, info, unaryHandler)
	}
}

func streamHandler(ctx context.Context, port int, method *gotogrpc.GRPCServiceMethod, authority string, md map[string][]string, stream gotogrpc.GRPCStream) error {
	var w *middleware.GrpcHTTPResponseWriterAdapter

	hook := func(input proto.Message) (metadata.MD, []proto.Message, error) {
		b, err := gotogrpc.ParseRequest(input)
		if err != nil {
			util.LogMessage(ctx, err.Error())
			return nil, nil, err
		}
		util.LogMessage(ctx, fmt.Sprintf("Received [%d] bytes", len(b)))
		_, w = invokeMiddlewareChain(ctx, port, method, md, b)
		return w.ToMetadata(), w.Responses, nil
	}
	receiveCount, sendCount, err := stream.ChainInOut(hook, nil)
	stream.Close()
	gotogrpc.LogRequest(ctx, port, method.Service.Name, method.Name, authority, md, receiveCount, 0, "")
	if err != nil {
		util.LogMessage(ctx, fmt.Sprintf("Service: [%s] Method [%s]: Received error while processing stream: %s", method.Service.Name, method.Name, err.Error()))
	} else {
		util.LogMessage(ctx, fmt.Sprintf("Service: [%s] Method [%s]: Received [%d] requests", method.Service.Name, method.Name, receiveCount))
	}
	gotogrpc.LogResponse(ctx, nil, 200, sendCount, -1, fmt.Sprintf("Sent stream responses, count [%d]", sendCount))
	return err
}

func GRPCStreamHandler(_ interface{}, ss grpc.ServerStream) error {
	ctx := ss.Context()
	method, port, _, authority, md, err := gotogrpc.CommonHandler(ctx, ss)
	if err != nil {
		return err
	}
	if md == nil {
		return fmt.Errorf("Metadata not found in context")
	}
	stream := gotogrpc.NewServerStream(port, method, ss, nil)
	stream.SetDelay(method.StreamDelayMin, method.StreamDelayMax, method.StreamDelayCount)
	err = streamHandler(context.Background(), port, method, authority, md, stream)
	return err
}

func (gi *GRPCMethodInterceptor) Intercept(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	method, port, remoteAddr, authority, md, err := gotogrpc.CommonHandler(ctx, nil)
	if err != nil {
		util.LogMessage(ctx, err.Error())
		return nil, err
	}
	if gi.originalHandler != nil {
		log.Println("GRPCMethodInterceptor: Original handler found, using original handler")
		return gi.originalHandler(gi.originalServer, ctx, dec, interceptor)
	}
	if util.WillProxyGRPC(port, method) {
		log.Println("GRPCMethodInterceptor: No original handler found, using ProxyUnary")
		req := gotogrpc.ReadRequest(method, dec)
		return ProxyUnary(ctx, port, method, remoteAddr.String(), authority, md, req)
	}
	return GRPCUnaryHandler(method)(srv, ctx, dec, interceptor)
}

func (gi *GRPCStreamInterceptor) Intercept(srv any, ss grpc.ServerStream) error {
	ctx := ss.Context()
	method, port, remoteAddr, _, md, err := gotogrpc.CommonHandler(ctx, nil)
	if err != nil {
		util.LogMessage(ctx, err.Error())
		return err
	}
	if gi.originalHandler != nil {
		log.Println("GRPCStreamInterceptor: Original handler found, using original handler")
		return gi.originalHandler(gi.originalServer, ss)
	}
	if util.WillProxyGRPC(port, method) {
		log.Println("GRPCStreamInterceptor: No original handler found, using ProxyGRPCStream")
		stream := gotogrpc.NewServerStream(port, method, ss, nil)
		gotogrpc.ProxyGRPCStream(context.Background(), port, method, remoteAddr.String(), md, stream)
		return nil
	}
	return GRPCStreamHandler(srv, ss)
}

func ProxyUnary(ctx context.Context, port int, method *gotogrpc.GRPCServiceMethod, remoteAddr, authority string, md metadata.MD, req interface{}) (resp interface{}, err error) {
	output, respHeaders, _, e := gotogrpc.ProxyGRPCUnary(ctx, port, method, md, []proto.Message{req.(*dynamicpb.Message)})
	if e != nil {
		err = e
		util.LogMessage(ctx, fmt.Sprintf("GRPC Proxy[%d]: Failed to proxy request [%+v], downstream [%s], method [%s], authority [%s], md [%+v], error [%s]", port, req, remoteAddr, method.Name, authority, md, err.Error()))
		return
	}
	grpc.SendHeader(ctx, respHeaders)
	if len(output) > 0 {
		resp = output[0]
	}
	return
}
