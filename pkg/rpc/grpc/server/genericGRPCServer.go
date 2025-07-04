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
	"io"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/dynamicpb"
)

func init() {
	gotogrpc.GRPCUnaryHandler = GRPCUnaryHandler
	gotogrpc.GRPCStreamHandler = GRPCStreamHandler
}

func parseRequest(req interface{}) ([]byte, error) {
	msg, ok := req.(*dynamicpb.Message)
	if !ok {
		return nil, fmt.Errorf("unexpected message type")
	}
	if b, err := protojson.Marshal(msg); err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	} else {
		return b, nil
	}
}

// func parseRequestJSON(req interface{}) (util.JSON, error) {
// 	if b, err := parseRequest(req); err != nil {
// 		return nil, err
// 	} else {
// 		return util.FromBytes(b), nil
// 	}
// }

func getRequestHeaders(ctx context.Context) (map[string][]string, error) {
	headers := make(map[string][]string)
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		for k, v := range md {
			headers[k] = v
		}
	} else {
		return nil, fmt.Errorf("failed to get headers from context")
	}

	return headers, nil
}

func getRequestHost(md map[string][]string) string {
	host := ""
	if v, ok := md[":authority"]; ok {
		host = v[0]
		md["host"] = v
	} else if v, ok := md["host"]; ok {
		host = v[0]
	} else if v, ok := md["hostName"]; ok {
		host = v[0]
	}
	return host
}

func getRequestRemoteAddr(md map[string][]string) string {
	remoteAddr := ""
	if v, ok := md["remoteAddr"]; ok {
		remoteAddr = v[0]
	}
	return remoteAddr
}

func createDummyRequest(method *gotogrpc.GRPCServiceMethod, dec func(interface{}) error) *dynamicpb.Message {
	req := dynamicpb.NewMessage(method.InputType())
	if err := dec(req); err != nil {
		return nil
	}
	return req
}

func buildResponse(method *gotogrpc.GRPCServiceMethod, resp [][]byte) (*dynamicpb.Message, error) {
	dmsg := dynamicpb.NewMessage(method.OutputType())
	if err := protojson.Unmarshal(resp[0], dmsg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response to dynamic message: %w", err)
	}
	return dmsg, nil
}

func sendStreamResponse(method *gotogrpc.GRPCServiceMethod, stream grpc.ServerStream, resp [][]byte, from, to int) error {
	rem := to - from
	for i := from; i < to; i++ {
		if i >= len(resp) {
			i = 0
		}
		rem--
		dmsg := dynamicpb.NewMessage(method.OutputType())
		if err := protojson.Unmarshal(resp[i], dmsg); err != nil {
			return fmt.Errorf("failed to unmarshal response to dynamic message: %w", err)
		}
		if err := stream.SendMsg(dmsg); err != nil {
			return err
		}
		if rem == 0 {
			break
		}
	}
	return nil
}

func commonHandler(ctx context.Context, stream grpc.ServerStream) (*gotogrpc.GRPCServiceMethod, map[string][]string, error) {
	if ctx == nil {
		ctx = stream.Context()
	}
	method := gotogrpc.ServiceRegistry.ParseGRPCServiceMethod(ctx)
	if method == nil {
		return nil, nil, fmt.Errorf("method not found in context")
	}
	md, err := getRequestHeaders(ctx)
	if err != nil || md == nil {
		return nil, nil, fmt.Errorf("Metadata not found in context")
	}
	return method, md, nil
}

func invokeMiddlewareChain(ctx context.Context, method *gotogrpc.GRPCServiceMethod, md map[string][]string, body []byte) (*middleware.GrpcHTTPRequestAdapter, *middleware.GrpcHTTPResponseWriterAdapter) {
	return middleware.InvokeMiddlewareChainForGRPC(ctx, method.Name, getRequestHost(md), method.URI, md, body)
}

func unaryHandler(ctx context.Context, req interface{}) (interface{}, error) {
	method, md, err := commonHandler(ctx, nil)
	if err != nil {
		return nil, err
	}
	if md == nil {
		return nil, fmt.Errorf("Metadata not found in context")
	}
	b, err := parseRequest(req)
	if err != nil || b == nil {
		return nil, fmt.Errorf("Request body not readable")
	}
	util.AddLogMessageForContext(fmt.Sprintf("Service: [%s] Method [%s]: Received [%d] bytes", method.Service.Name, method.Name, len(b)), ctx)
	_, w := invokeMiddlewareChain(ctx, method, md, b)
	grpc.SendHeader(ctx, metadata.New(w.ToMetadata()))
	if len(w.Responses) > 0 {
		resp, err := buildResponse(method, w.Responses)
		if err != nil {
			return nil, err
		}
		util.AddLogMessageForContext(fmt.Sprintf("Sending unary response, count [%d]", len(w.Responses)), ctx)
		return resp, nil
	}
	return nil, nil
}

func GRPCUnaryHandler(method *gotogrpc.GRPCServiceMethod) grpc.MethodHandler {
	return func(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
		req := createDummyRequest(method, dec)
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
	method, md, err := commonHandler(ctx, stream)
	if err != nil {
		return err
	}
	if md == nil {
		return fmt.Errorf("Metadata not found in context")
	}

	var w *middleware.GrpcHTTPResponseWriterAdapter
	responseCount := 0
	for {
		req := dynamicpb.NewMessage(method.InputType())
		if err := stream.RecvMsg(req); err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		b, err := parseRequest(req)
		if err != nil || b == nil {
			return fmt.Errorf("Request body not readable")
		} else {
			util.AddLogMessageForContext(fmt.Sprintf("Service: [%s] Method [%s]: Received [%d] bytes", method.Service.Name, method.Name, len(b)), ctx)
		}
		_, w = invokeMiddlewareChain(ctx, method, md, b)
		if method.IsServerStreaming && method.IsClientStreaming && responseCount < method.StreamCount-1 {
			err = sendStreamResponse(method, stream, w.Responses, responseCount, responseCount+1)
			if err != nil {
				return err
			}
			util.AddLogMessageForContext(fmt.Sprintf("Sending stream responses, count [%d]", responseCount), ctx)
		}
	}
	err = sendStreamResponse(method, stream, w.Responses, responseCount, method.StreamCount-responseCount)
	if err != nil {
		return err
	}
	util.AddLogMessageForContext(fmt.Sprintf("Sent stream responses, count [%d]", responseCount), ctx)
	return nil
}
