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
	"fmt"
	gotogrpc "goto/pkg/rpc/grpc"
	"goto/pkg/types"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/dynamicpb"
)

type GRPCCall struct {
	Service        string                `json:"service"`
	Method         string                `json:"method"`
	Endpoint       string                `json:"endpoint"`
	Headers        *types.Headers        `json:"headers"`
	Payloads       *GRPCPayloads         `json:"payloads"`
	Push           bool                  `json:"push"`
	Result         bool                  `json:"result"`
	RequestHeaders types.ReadableHeaders `json:"-"`
}

type GRPCResult struct {
	Responses []*GRPCResponse
	Errors    []error
	lock      sync.Mutex
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

func (c *GRPCClient) Invoke(call *GRPCCall, callback func(proto.Message, metadata.MD)) (result *GRPCResult) {
	if c.Service == nil || c.Service.Methods == nil {
		result.AddError(fmt.Errorf("GRPCClient.Invoke: [ERROR] service [%s] not configured", c.Service.Name))
		return
	}
	grpcMethod := c.Service.Methods[call.Method]
	if grpcMethod == nil {
		result.AddError(fmt.Errorf("GRPCClient.Invoke: [ERROR] method [%s] not found in Service [%s]", call.Method, c.Service.Name))
		return
	}
	headers := types.SimpleHTTPHeaders{}
	call.UpdateHeaders(headers)
	ctx, md, err := c.ConnectWithHeadersOrMD(headers, nil)
	if err != nil {
		result.AddError(err)
		return
	}
	result = newGRPCResult()
	c.monitorAndAbort()
	if grpcMethod.IsUnary {
		c.InvokeUnary(call, grpcMethod, md, ctx, result)
	} else if grpcMethod.IsClientStream && grpcMethod.IsServerStream {
		c.InvokeBidiStream(call, grpcMethod, md, c.Options.KeepOpen, callback, result)
	} else if grpcMethod.IsClientStream {
		c.InvokeClientStream(call, grpcMethod, md, c.Options.KeepOpen, result)
	} else if grpcMethod.IsServerStream {
		c.InvokeServerStream(call, grpcMethod, md, callback, result)
	}
	return
}

func (c *GRPCClient) InvokeRaw(method *gotogrpc.GRPCServiceMethod, inMD metadata.MD, inputs []proto.Message) (responses []proto.Message, respHeaders metadata.MD, respTrailers metadata.MD, err error) {
	ctx, md, err := c.ConnectWithHeadersOrMD(nil, inMD)
	if err != nil {
		return
	}
	c.monitorAndAbort()
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
		responses, respHeaders, respTrailers, err = c.InvokeBidiStreamRaw(method, md, inputs, nil)
		if err != nil {
			log.Printf("GRPCClient.InvokeRaw: Service [%s] Method [%s] InvokeBidiStreamRaw failed with ERROR [%s]\n", method.Service.Name, method.Name, err.Error())
		}
	} else if method.IsClientStream {
		responses, respHeaders, respTrailers, err = c.InvokeClientStreamRaw(method, md, inputs)
		if err != nil {
			log.Printf("GRPCClient.InvokeRaw: Service [%s] Method [%s] InvokeClientStreamRaw failed with ERROR [%s]\n", method.Service.Name, method.Name, err.Error())
		}
	} else if method.IsServerStream {
		responses, respHeaders, respTrailers, err = c.InvokeServerStreamRaw(method, md, input, nil)
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

func (c *GRPCClient) InvokeUnary(call *GRPCCall, m *gotogrpc.GRPCServiceMethod, md metadata.MD, ctx context.Context, result *GRPCResult) {
	if call == nil || call.Payloads == nil {
		result.AddError(fmt.Errorf("No payload"))
		return
	}
	invokeUnary := func(md metadata.MD, payload []byte, wg *sync.WaitGroup) {
		if wg != nil {
			defer wg.Done()
		}
		input := dynamicpb.NewMessage(m.InputType())
		if len(payload) > 0 {
			if err := fillInput(input, payload); err != nil {
				result.AddError(err)
				return
			}
		}
		var respHeaders metadata.MD
		var respTrailers metadata.MD
		ctx = metadata.NewOutgoingContext(ctx, md)
		output, err := c.stub.InvokeRpc(ctx, m.PMD, input, grpc.Trailer(&respTrailers), grpc.Header(&respHeaders))
		response := newGRPCResponse(respHeaders, respTrailers, []proto.Message{output}, 0, 0)
		if processResponseStatus(m, response, err) {
			result.AddResponse(response)
		}
		if err != nil {
			result.AddError(err)
		}
	}
	call.Payloads.Process(call, md, invokeUnary, nil)
}

func (c *GRPCClient) InvokeUnaryRaw(ctx context.Context, m *gotogrpc.GRPCServiceMethod, input proto.Message) (responses []proto.Message, respHeaders metadata.MD, respTrailers metadata.MD, err error) {
	respHeaders = metadata.MD{}
	respTrailers = metadata.MD{}
	r, e := c.stub.InvokeRpc(ctx, m.PMD, input, grpc.Header(&respHeaders), grpc.Trailer(&respTrailers))
	responses = []proto.Message{r}
	err = e
	return
}

func (c *GRPCClient) InvokeClientStream(call *GRPCCall, m *gotogrpc.GRPCServiceMethod, md metadata.MD, keepOpen time.Duration, result *GRPCResult) {
	invokeCS := func(md metadata.MD, payload [][]byte, wg *sync.WaitGroup) {
		if wg != nil {
			defer wg.Done()
		}
		responses, respHeaders, respTrailers, err := c.internalInvokeClientStream(m, md, nil, payload, keepOpen)
		response := newGRPCResponse(respHeaders, respTrailers, responses, len(payload), 0)
		if processResponseStatus(m, response, err) {
			result.AddResponse(response)
		}
		if err != nil {
			result.AddError(err)
		}
	}
	call.Payloads.Process(call, md, nil, invokeCS)
}

func (c *GRPCClient) InvokeClientStreamRaw(m *gotogrpc.GRPCServiceMethod, md metadata.MD, messages []proto.Message) (responses []proto.Message, respHeaders metadata.MD, respTrailers metadata.MD, err error) {
	return c.internalInvokeClientStream(m, md, messages, nil, 0)
}

func (c *GRPCClient) internalInvokeClientStream(m *gotogrpc.GRPCServiceMethod, md metadata.MD, messages []proto.Message, payloads [][]byte, keepOpen time.Duration) (responses []proto.Message, respHeaders metadata.MD, respTrailers metadata.MD, err error) {
	var input proto.Message
	input, messages, payloads, err = c.createFirstInput(m, messages, payloads)
	if err != nil {
		log.Printf("GRPCClient.InvokeClientStream: Service [%s] Method [%s] [ERROR] Failed to create stream input with error: %s\n", m.Service.Name, m.Name, err.Error())
		return nil, nil, nil, err
	}
	stream, err := c.OpenStream(0, m, md, input)
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

func (c *GRPCClient) InvokeServerStream(call *GRPCCall, m *gotogrpc.GRPCServiceMethod, md metadata.MD, callback func(proto.Message, metadata.MD), result *GRPCResult) {
	invokeSS := func(md metadata.MD, payload []byte, wg *sync.WaitGroup) {
		if wg != nil {
			defer wg.Done()
		}
		input := dynamicpb.NewMessage(m.InputType())
		if len(payload) > 0 {
			if err := fillInput(input, payload); err != nil {
				result.AddError(err)
				return
			}
		}
		responses, respHeaders, respTrailers, err := c.InvokeServerStreamRaw(m, md, input, callback)
		response := newGRPCResponse(respHeaders, respTrailers, responses, 1, len(responses))
		if processResponseStatus(m, response, err) {
			result.AddResponse(response)
		}
		if err != nil {
			result.AddError(err)
		}
	}
	call.Payloads.Process(call, md, invokeSS, nil)
}

func (c *GRPCClient) InvokeServerStreamRaw(m *gotogrpc.GRPCServiceMethod, md metadata.MD, input proto.Message, callback func(proto.Message, metadata.MD)) (responses []proto.Message, respHeaders metadata.MD, respTrailers metadata.MD, err error) {
	stream, err := c.OpenStream(0, m, md, input)
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
	respHeaders, err = stream.Headers()
	respTrailers = stream.Trailers()
	for msg := range in {
		responses = append(responses, msg)
		if callback != nil {
			callback(msg, respHeaders)
		}
	}
	return
}

func (c *GRPCClient) InvokeBidiStream(call *GRPCCall, m *gotogrpc.GRPCServiceMethod, md metadata.MD, keepOpen time.Duration, callback func(proto.Message, metadata.MD), result *GRPCResult) {
	invokeBS := func(md metadata.MD, payload [][]byte, wg *sync.WaitGroup) {
		if wg != nil {
			defer wg.Done()
		}
		output, respHeaders, respTrailers, err := c.internalInvokeBidiStream(m, md, nil, payload, keepOpen, callback)
		response := newGRPCResponse(respHeaders, respTrailers, output, len(payload), len(output))
		if processResponseStatus(m, response, err) {
			result.AddResponse(response)
		}
		if err != nil {
			result.AddError(err)
		}
	}
	call.Payloads.Process(call, md, nil, invokeBS)
}

func (c *GRPCClient) InvokeBidiStreamRaw(m *gotogrpc.GRPCServiceMethod, md metadata.MD, messages []proto.Message, callback func(proto.Message, metadata.MD)) (responses []proto.Message, respHeaders metadata.MD, respTrailers metadata.MD, err error) {
	return c.internalInvokeBidiStream(m, md, messages, nil, 0, callback)
}

func (c *GRPCClient) internalInvokeBidiStream(m *gotogrpc.GRPCServiceMethod, md metadata.MD, messages []proto.Message, payloads [][]byte, keepOpen time.Duration, callback func(proto.Message, metadata.MD)) (responses []proto.Message, respHeaders metadata.MD, respTrailers metadata.MD, err error) {
	// var input proto.Message
	// input, messages, payloads, err = c.createFirstInput(m, messages, payloads)
	stream, err := c.OpenStream(0, m, md, nil)
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
	h, e := stream.Headers()
	if e == nil {
		respHeaders = h
	} else {
		err = e
	}
	for msg := range in {
		log.Printf("GRPCClient.InvokeBidiStreamRaw: Received message [%+v] from Service [%s] Method [%s] stream\n", msg, m.Service.Name, m.Name)
		responses = append(responses, msg)
		if callback != nil {
			callback(msg, respHeaders)
		}
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
	t := stream.Trailers()
	if t != nil {
		respTrailers = t
	}
	return
}

func (call *GRPCCall) UpdateHeaders(headers types.MutatingHeaders) {
	if call.Headers.Request != nil {
		call.Headers.Request.UpdateHeaders(headers)
		if call.Headers.Request.Forward != nil {
			types.ForwardHeaders(call.RequestHeaders, headers, slices.Values(call.Headers.Request.Forward))
		}
	}
}

func (call *GRPCCall) UpdateStreamHeaders(headers types.MutatingHeaders, p *GRPCPayload) {
	if p.Headers != nil && p.Headers.Request != nil {
		p.Headers.Request.UpdateHeaders(headers)
		if p.Headers.Request.Forward != nil {
			types.ForwardHeaders(call.RequestHeaders, headers, slices.Values(p.Headers.Request.Forward))
		}
	}
}

func newGRPCResult() *GRPCResult {
	return &GRPCResult{}
}

func (r *GRPCResult) AddError(err error) {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.Errors = append(r.Errors, err)
}

func (r *GRPCResult) AddResponse(response *GRPCResponse) {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.Responses = append(r.Responses, response)
}

func (r *GRPCResult) GetErrors() string {
	b := strings.Builder{}
	for _, err := range r.Errors {
		b.WriteString(err.Error())
		b.WriteString("\n")
	}
	return b.String()
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
		} else {
			r.EquivalentHTTPStatusCode = r.Status
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
