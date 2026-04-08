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

package grpcserver

import (
	"context"
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/events"
	"goto/pkg/global"
	gg "goto/pkg/rpc/grpc"
	gotogrpc "goto/pkg/rpc/grpc"
	"goto/pkg/rpc/grpc/pb"
	"goto/pkg/server/response/payload"
	"goto/pkg/types"
	"goto/pkg/util"
	"io"
	"log"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/protobuf/proto"
)

type IGRPCService interface {
	Send(*pb.Output) error
}

type GotoGRPCService struct {
	pb.UnimplementedGotoServer
}

type Goto2GRPCService struct {
	pb.UnimplementedGoto2Server
}

type Goto3GRPCService struct {
	pb.UnimplementedGoto3Server
}

var (
	IsGotoServiceRunning = false
	counter              atomic.Uint64
)

func init() {
	global.AddGRPCIntercept(RegisterGotoServer)
	global.AddGRPCStartWatcher(OnGRPCStart)
	global.AddGRPCStopWatcher(OnGRPCStop)
}

func OnGRPCStart() {
}

func OnGRPCStop() {
	IsGotoServiceRunning = false
}

func RegisterGotoServer(server global.IGRPCManager) {
	if !IsGotoServiceRunning {
		//pb.RegisterGotoServer(TheGRPCServer.Server, &GotoGRPCService{})
		// server.InterceptAndServe(&pb.Goto_ServiceDesc, &GotoGRPCService{})
		server.InterceptWithMiddleware(&pb.Goto2_ServiceDesc, &Goto2GRPCService{})
		server.InterceptWithMiddleware(&pb.Goto3_ServiceDesc, &Goto3GRPCService{})
		gotoService := gotogrpc.ServiceRegistry.NewGRPCServiceFromSD(&pb.Goto_ServiceDesc)
		if gotoService != nil {
			gotoService.Methods["echo"].In = TransformInput
			gotoService.Methods["streamIn"].In = TransformInput
			gotoService.Methods["streamOut"].In = TransformStreamConfig
			gotoService.Methods["streamInOut"].In = TransformStreamConfig
			server.InterceptAndServe(gotoService.GSD, gotoService)
			// server.InterceptAndProxy(&pb.Goto3_ServiceDesc, &pb.Goto_ServiceDesc, pb.Goto3_ServiceDesc.ServiceName, &Goto3GRPCService{})
		}
		IsGotoServiceRunning = true
	}
	log.Println("Registered Goto GRPC Server")
}

func TransformStreamConfig(req proto.Message) proto.Message {
	if req == nil {
		return nil
	}
	req2 := new(pb.StreamConfig)
	reqValues := req.ProtoReflect()
	fields := req.ProtoReflect().Descriptor().Fields()

	req2.ChunkSize = int32(reqValues.Get(fields.ByName("chunkSize")).Int())
	req2.ChunkCount = int32(reqValues.Get(fields.ByName("chunkCount")).Int())
	req2.Interval = reqValues.Get(fields.ByName("interval")).String()
	req2.Payload = reqValues.Get(fields.ByName("payload")).String()
	return req2
}

func TransformInput(req proto.Message) proto.Message {
	if req == nil {
		return nil
	}
	req2 := new(pb.Input)
	reqValues := req.ProtoReflect()
	fields := req.ProtoReflect().Descriptor().Fields()
	req2.Payload = reqValues.Get(fields.ByName("payload")).String()
	return req2
}

func SetHeaders(ctx context.Context, port int, hostLabel, listenerLabel string, addHeaders map[string]string) (requestHeaders, responseHeaders map[string]string) {
	remoteAddress := ""
	if p, ok := peer.FromContext(ctx); ok {
		remoteAddress = p.Addr.String()
	}
	methodInfo, _ := grpc.Method(ctx)

	responseHeaders = map[string]string{
		constants.HeaderGotoHost:          hostLabel,
		constants.HeaderViaGoto:           listenerLabel,
		constants.HeaderGotoProtocol:      "GRPC",
		constants.HeaderGotoPort:          fmt.Sprint(port),
		constants.HeaderGotoRemoteAddress: remoteAddress,
		constants.HeaderGotoRPC:           methodInfo,
	}
	requestHeaders = map[string]string{}
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		for k, v := range md {
			if len(v) > 0 {
				requestHeaders[k] = v[0]
				k = strings.ReplaceAll(k, ":", "")
				responseHeaders["Request-"+k] = v[0]
			}
		}
	}
	for k, v := range addHeaders {
		responseHeaders[k] = v
	}
	grpc.SendHeader(ctx, metadata.New(responseHeaders))
	return
}

func (gs *GotoGRPCService) Echo(ctx context.Context, input *pb.Input) (*pb.Output, error) {
	port := util.GetGRPCPort(ctx)
	events.TrackPortTrafficEvent(port, "GRPC.echo", 200)
	listenerLabel := global.Funcs.GetListenerLabelForPort(port)
	requestHeaders, responseHeaders := SetHeaders(ctx, port, global.Self.HostLabel, listenerLabel, nil)
	requestMiniBody := ""
	if global.Flags.LogRequestBody {
		requestMiniBody = input.Payload
	} else if global.Flags.LogRequestMiniBody {
		requestMiniBody = input.Payload[:50]
	}
	gg.LogRequest(ctx, port, "Goto", "echo", requestHeaders[constants.HeaderAuthority], requestHeaders, 1, len(input.Payload), requestMiniBody)
	id := counter.Add(1)
	if _, rp, _, found := payload.PayloadManager.GetResponsePayload(port, true, "/Goto/echo", nil, nil, nil); found {
		input.Payload = string(rp.Payload)
	}
	response := &pb.Output{Id: strconv.Itoa(int(id)), Payload: input.Payload, At: time.Now().Format(time.RFC3339Nano), GotoHost: global.Self.HostLabel, GotoPort: int32(port), ViaGoto: listenerLabel}
	responseLength := -1
	if global.Flags.LogResponseBody || global.Flags.LogResponseMiniBody {
		responseBodyText := util.ToJSONText(response)
		responseLength = len(responseBodyText)
	}
	gg.LogResponse(ctx, responseHeaders, 200, 1, responseLength, "Serving Echo")
	return response, nil
}

func (gs *GotoGRPCService) StreamIn(cs grpc.ClientStreamingServer[pb.Input, pb.Output]) error {
	ctx := cs.Context()
	port := util.GetGRPCPort(ctx)
	events.TrackPortTrafficEvent(port, "GRPC.streamIn.start", 200)
	listenerLabel := global.Funcs.GetListenerLabelForPort(port)
	requestHeaders, responseHeaders := SetHeaders(ctx, port, global.Self.HostLabel, listenerLabel, nil)
	requestCount := 0
	configPayload := ""
	if _, rp, _, found := payload.PayloadManager.GetResponsePayload(port, true, "/Goto/streamIn", nil, nil, nil); found {
		configPayload = string(rp.Payload)
	}
	responsePayload := strings.Builder{}
	for {
		input, err := cs.Recv()
		if err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		requestCount++
		log.Printf("GRPC[%d]: Received client stream input [payload: [%s]]\n", port, input.Payload)
		if len(configPayload) > 0 {
			input.Payload = configPayload
		}
		responsePayload.WriteString(fmt.Sprintf("[%s]", input.Payload))
	}
	events.TrackPortTrafficEvent(port, "GRPC.streamIn.end", 200)
	gg.LogRequest(ctx, port, "Goto", "streamIn", requestHeaders[constants.HeaderAuthority], requestHeaders, requestCount, 0, "")
	id := counter.Add(1)
	payload := responsePayload.String()
	response := &pb.Output{Id: strconv.Itoa(int(id)), Payload: payload, At: time.Now().Format(time.RFC3339Nano), GotoHost: global.Self.HostLabel, GotoPort: int32(port), ViaGoto: listenerLabel}
	log.Printf("GRPC[%d]: Sent stream response [payload: [%s]]\n", port, payload)
	cs.SendAndClose(response)
	responseLength := -1
	if global.Flags.LogResponseBody || global.Flags.LogResponseMiniBody {
		responseBodyText := util.ToJSONText(response)
		responseLength = len(responseBodyText)
	}
	gg.LogResponse(ctx, responseHeaders, 200, 1, responseLength, "Serving StreamIn")
	return nil
}

func (gs *GotoGRPCService) sendStreamResponse(ctx context.Context, port int, hostLabel, listenerLabel string, configInput *pb.StreamConfig, ss IGRPCService) (int, int) {
	SetHeaders(ctx, port, hostLabel, listenerLabel, nil)
	payload := ""
	if configInput.Payload != "" {
		payload = configInput.Payload
	} else {
		payload = types.GenerateRandomString(int(configInput.ChunkSize))
	}
	interval, err := time.ParseDuration(configInput.Interval)
	if err != nil {
		interval = 100 * time.Millisecond
	}
	for i := 0; i < int(configInput.ChunkCount); i++ {
		id := counter.Add(1)
		ss.Send(&pb.Output{Id: strconv.Itoa(int(id)), Payload: payload, At: time.Now().Format(time.RFC3339Nano),
			GotoHost: hostLabel, GotoPort: int32(port), ViaGoto: listenerLabel})
		if i < int(configInput.ChunkCount)-1 {
			time.Sleep(interval)
		}
	}
	return int(configInput.ChunkCount), len(payload)
}

func (gs *GotoGRPCService) StreamOut(configInput *pb.StreamConfig, ss grpc.ServerStreamingServer[pb.Output]) error {
	ctx := ss.Context()
	port := util.GetGRPCPort(ctx)
	events.TrackPortTrafficEvent(port, "GRPC.streamOut.start", 200)
	listenerLabel := global.Funcs.GetListenerLabelForPort(port)
	requestHeaders, responseHeaders := SetHeaders(ctx, port, global.Self.HostLabel, listenerLabel, nil)
	gg.LogRequest(ctx, port, "Goto", "streamOut", requestHeaders[constants.HeaderAuthority], requestHeaders, 1, 0, "")
	payloadType := ""
	if _, rp, _, found := payload.PayloadManager.GetResponsePayload(port, true, "/Goto/streamOut", nil, nil, nil); found {
		configInput.Payload = string(rp.Payload)
		payloadType = " pre-configured"
	}
	log.Printf("GotoGRPCService[%d]: Serving StreamOut with config [chunkSize: %d, chunkCount: %d, interval: %s,%s payload size: [%d]]\n",
		port, configInput.ChunkSize, configInput.ChunkCount, configInput.Interval, payloadType, len(configInput.Payload))
	responseCount, responseLength := gs.sendStreamResponse(ctx, port, global.Self.HostLabel, listenerLabel, configInput, ss)
	events.TrackPortTrafficEvent(port, "GRPC.streamOut.end", 200)
	gg.LogResponse(ctx, responseHeaders, 200, responseCount, responseLength,
		fmt.Sprintf("Served StreamOut with config [chunkSize: %d, chunkCount: %d, interval: %s,%s payload size: %d]",
			configInput.ChunkSize, configInput.ChunkCount, configInput.Interval, payloadType, len(configInput.Payload)))
	return nil
}

func (gs *GotoGRPCService) StreamInOut(bidi grpc.BidiStreamingServer[pb.StreamConfig, pb.Output]) error {
	ctx := bidi.Context()
	port := util.GetGRPCPort(ctx)
	events.TrackPortTrafficEvent(port, "GRPC.streamInOut.start", 200)
	listenerLabel := global.Funcs.GetListenerLabelForPort(port)
	requestHeaders, responseHeaders := SetHeaders(ctx, port, global.Self.HostLabel, listenerLabel, nil)
	configPayload := ""
	payloadType := ""
	if _, rp, _, found := payload.PayloadManager.GetResponsePayload(port, true, "/Goto/streamInOut", nil, nil, nil); found {
		configPayload = string(rp.Payload)
		payloadType = " pre-configured"
	}
	requestCount := 0
	responseCount := 0
	responseLength := 0
	payloadLength := 0
	chunkCount := 0
	for {
		configInput, err := bidi.Recv()
		if err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		requestCount++
		chunkCount += int(configInput.ChunkCount)
		if len(configPayload) > 0 {
			configInput.Payload = configPayload
		}
		log.Printf("GRPC[%d]: Serving StreamInOut with config [chunkSize: %d, chunkCount: %d, interval: %s,%s payload size: [%d]]\n",
			port, configInput.ChunkSize, configInput.ChunkCount, configInput.Interval, payloadType, len(configInput.Payload))
		count, length := gs.sendStreamResponse(ctx, port, global.Self.HostLabel, listenerLabel, configInput, bidi)
		responseCount += count
		responseLength += length
	}
	events.TrackPortTrafficEvent(port, "GRPC.streamInOut.end", 200)
	gg.LogRequest(ctx, port, "Goto", "streamInOut", requestHeaders[constants.HeaderAuthority], requestHeaders, requestCount, 0, "")

	gg.LogResponse(ctx, responseHeaders, 200, responseCount, responseLength,
		fmt.Sprintf("Served StreamInOut with total chunks [%d] and total payload length [%d]", chunkCount, payloadLength))
	return nil
}
