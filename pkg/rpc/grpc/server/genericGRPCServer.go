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
	"errors"
	"fmt"
	gg "goto/pkg/rpc/grpc"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"io"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/dynamicpb"
)

func init() {
	gg.GRPCUnaryHandler = GRPCUnaryHandler
	gg.GRPCStreamHandler = GRPCStreamHandler
}

func invokeMiddlewareChain(ctx context.Context, method *gg.GRPCServiceMethod, md map[string][]string, body []byte) (*middleware.GrpcHTTPRequestAdapter, *middleware.GrpcHTTPResponseWriterAdapter) {
	return middleware.InvokeMiddlewareChainForGRPC(ctx, method.Name, gg.GetRequestHost(md), method.URI, md, body)
}

func unaryHandler(ctx context.Context, req interface{}) (interface{}, error) {
	method, port, authority, md, err := gg.CommonHandler(ctx, nil)
	if err != nil {
		util.AddLogMessageForContext(ctx, err.Error())
		return nil, err
	}
	gg.AddRequestLogMessage(ctx, port, method.Service.Name, method.Name, authority, md, 1, 0, "")

	b, err := gg.ParseRequest(req)
	if err != nil {
		util.AddLogMessageForContext(ctx, err.Error())
		return nil, err
	}
	util.AddLogMessageForContext(ctx, fmt.Sprintf("Received [%d] bytes", len(b)))

	_, w := invokeMiddlewareChain(ctx, method, md, b)
	responseHeaders := w.ToMetadata()
	grpc.SendHeader(ctx, responseHeaders)
	var resp *dynamicpb.Message
	responseCount := 0
	msg := ""
	if len(w.Responses) > 0 {
		resp, err = gg.BuildResponse(method, w.Responses)
		if err != nil {
			return nil, err
		}
		msg = fmt.Sprintf("Sending unary response, count [%d]", len(w.Responses))
		responseCount = 1
	} else {
		msg = "No response to send"
	}
	gg.AddResponseLogMessage(ctx, responseHeaders, 200, responseCount, -1, msg)
	return resp, nil
}

func GRPCUnaryHandler(method *gg.GRPCServiceMethod) grpc.MethodHandler {
	return func(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
		req := gg.CreateDummyRequest(method, dec)
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

func GRPCStreamHandler(_ interface{}, stream grpc.ServerStream) error {
	ctx := stream.Context()
	method, port, authority, md, err := gg.CommonHandler(ctx, stream)
	if err != nil {
		return err
	}
	if md == nil {
		return fmt.Errorf("Metadata not found in context")
	}

	var w *middleware.GrpcHTTPResponseWriterAdapter
	requestCount := 0
	responseCount := 0
	bytesReceived := 0
	var responseHeaders metadata.MD
	headersSent := false

	for {
		req := dynamicpb.NewMessage(method.InputType())
		if err := stream.RecvMsg(req); err == io.EOF || errors.Is(err, context.Canceled) {
			break
		} else if err != nil {
			return err
		}
		requestCount++
		b, err := gg.ParseRequest(req)
		if err != nil || b == nil {
			return fmt.Errorf("Request body not readable")
		} else {
			bytesReceived += len(b)
		}
		_, w = invokeMiddlewareChain(ctx, method, md, b)
		if !headersSent {
			responseHeaders = w.ToMetadata()
			grpc.SendHeader(ctx, responseHeaders)
			headersSent = true
		}
		if method.IsServerStreaming && method.IsClientStreaming && responseCount < method.StreamCount-1 {
			responseCount++
			err = gg.SendStreamResponse(method, stream, w.Responses, responseCount, responseCount+1)
			if err != nil {
				return err
			}
		}
	}
	err = gg.SendStreamResponse(method, stream, w.Responses, responseCount, method.StreamCount-responseCount)
	if err != nil {
		return err
	}
	gg.AddRequestLogMessage(ctx, port, method.Service.Name, method.Name, authority, md, 1, 0, "")
	util.AddLogMessageForContext(ctx, fmt.Sprintf("Service: [%s] Method [%s]: Received [%d] bytes from [%d] requests", method.Service.Name, method.Name, bytesReceived, requestCount))
	gg.AddResponseLogMessage(ctx, responseHeaders, 200, responseCount, -1,
		fmt.Sprintf("Sent stream responses, count [%d]", responseCount))
	return nil
}
