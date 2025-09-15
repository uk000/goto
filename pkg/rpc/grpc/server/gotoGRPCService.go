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
	"goto/pkg/constants"
	"goto/pkg/events"
	"goto/pkg/global"
	gg "goto/pkg/rpc/grpc"
	gotogrpc "goto/pkg/rpc/grpc"
	"goto/pkg/rpc/grpc/pb"
	"goto/pkg/server/listeners"
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
		server.InterceptAndServe(&pb.Goto_ServiceDesc, &GotoGRPCService{})
		server.InterceptWithMiddleware(&pb.Goto2_ServiceDesc, &Goto2GRPCService{})
		gotoService := gotogrpc.ServiceRegistry.NewGRPCServiceFromSD(&pb.Goto_ServiceDesc)
		if gotoService != nil {
			gotoService.Methods["echo"].In = TransformInput
			gotoService.Methods["streamIn"].In = TransformInput
			gotoService.Methods["streamOut"].In = TransformStreamConfig
			gotoService.Methods["streamInOut"].In = TransformStreamConfig
			// server.InterceptAndProxy(&pb.Goto3_ServiceDesc, &pb.Goto_ServiceDesc, pb.Goto3_ServiceDesc.ServiceName, &Goto3GRPCService{})
			server.InterceptWithMiddleware(&pb.Goto3_ServiceDesc, &Goto3GRPCService{})
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

func (gs *GotoGRPCService) setHeaders(ctx context.Context, port int, hostLabel, listenerLabel string) (requestHeaders, responseHeaders map[string]string) {
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
	grpc.SendHeader(ctx, metadata.New(responseHeaders))
	return
}

func (gs *GotoGRPCService) Echo(ctx context.Context, input *pb.Input) (*pb.Output, error) {
	port := util.GetContextPort(ctx)
	events.TrackPortTrafficEvent(port, "GRPC.echo", 200)
	hostLabel := listeners.GetHostLabelForPort(port)
	listenerLabel := global.Funcs.GetListenerLabelForPort(port)
	requestHeaders, responseHeaders := gs.setHeaders(ctx, port, hostLabel, listenerLabel)
	requestMiniBody := ""
	if global.Flags.LogRequestBody {
		requestMiniBody = input.Payload
	} else if global.Flags.LogRequestMiniBody {
		requestMiniBody = input.Payload[:50]
	}
	gg.LogRequest(ctx, port, "Goto", "echo", requestHeaders[constants.HeaderAuthority], requestHeaders, 1, len(input.Payload), requestMiniBody)
	id := counter.Add(1)
	response := &pb.Output{Id: strconv.Itoa(int(id)), Payload: input.Payload, At: time.Now().Format(time.RFC3339Nano), GotoHost: hostLabel, GotoPort: int32(port), ViaGoto: listenerLabel}
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
	port := util.GetContextPort(ctx)
	events.TrackPortTrafficEvent(port, "GRPC.streamIn.start", 200)
	hostLabel := listeners.GetHostLabelForPort(port)
	listenerLabel := global.Funcs.GetListenerLabelForPort(port)
	requestHeaders, responseHeaders := gs.setHeaders(ctx, port, hostLabel, listenerLabel)
	requestCount := 0
	responsePayload := strings.Builder{}
	for {
		input, err := cs.Recv()
		if err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		requestCount++
		responsePayload.WriteString(fmt.Sprintf("[%s]", input.Payload))
		log.Printf("GRPC[%d]: Received client stream input [payload: [%s]]\n", port, input.Payload)
	}
	events.TrackPortTrafficEvent(port, "GRPC.streamIn.end", 200)
	gg.LogRequest(ctx, port, "Goto", "streamIn", requestHeaders[constants.HeaderAuthority], requestHeaders, requestCount, 0, "")
	id := counter.Add(1)
	response := &pb.Output{Id: strconv.Itoa(int(id)), Payload: responsePayload.String(), At: time.Now().Format(time.RFC3339Nano), GotoHost: hostLabel, GotoPort: int32(port), ViaGoto: listenerLabel}
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
	gs.setHeaders(ctx, port, hostLabel, listenerLabel)
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
	log.Printf("GotoGRPCService[%d]: Serving StreamOut with config [chunkSize: %d, chunkCount: %d, interval: %s, payload size: [%d]]\n", ss.Context().Value("port"), configInput.ChunkSize, configInput.ChunkCount, configInput.Interval, len(configInput.Payload))
	ctx := ss.Context()
	port := util.GetContextPort(ctx)
	events.TrackPortTrafficEvent(port, "GRPC.streamOut.start", 200)
	hostLabel := listeners.GetHostLabelForPort(port)
	listenerLabel := global.Funcs.GetListenerLabelForPort(port)
	requestHeaders, responseHeaders := gs.setHeaders(ctx, port, hostLabel, listenerLabel)
	gg.LogRequest(ctx, port, "Goto", "streamOut", requestHeaders[constants.HeaderAuthority], requestHeaders, 1, 0, "")

	responseCount, responseLength := gs.sendStreamResponse(ctx, port, hostLabel, listenerLabel, configInput, ss)
	events.TrackPortTrafficEvent(port, "GRPC.streamOut.end", 200)
	gg.LogResponse(ctx, responseHeaders, 200, responseCount, responseLength,
		fmt.Sprintf("Served StreamOut with config [chunkSize: %d, chunkCount: %d, interval: %s, payload size: %d]",
			configInput.ChunkSize, configInput.ChunkCount, configInput.Interval, len(configInput.Payload)))
	return nil
}

func (gs *GotoGRPCService) StreamInOut(bidi grpc.BidiStreamingServer[pb.StreamConfig, pb.Output]) error {
	ctx := bidi.Context()
	port := util.GetContextPort(ctx)
	events.TrackPortTrafficEvent(port, "GRPC.streamInOut.start", 200)
	hostLabel := listeners.GetHostLabelForPort(port)
	listenerLabel := global.Funcs.GetListenerLabelForPort(port)
	requestHeaders, responseHeaders := gs.setHeaders(ctx, port, hostLabel, listenerLabel)

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
		log.Printf("GRPC[%d]: Serving StreamInOut with config [chunkSize: %d, chunkCount: %d, interval: %s, payload size: [%d]]\n",
			port, configInput.ChunkSize, configInput.ChunkCount, configInput.Interval, len(configInput.Payload))
		count, length := gs.sendStreamResponse(ctx, port, hostLabel, listenerLabel, configInput, bidi)
		responseCount += count
		responseLength += length
	}
	events.TrackPortTrafficEvent(port, "GRPC.streamInOut.end", 200)
	gg.LogRequest(ctx, port, "Goto", "streamInOut", requestHeaders[constants.HeaderAuthority], requestHeaders, requestCount, 0, "")

	gg.LogResponse(ctx, responseHeaders, 200, responseCount, responseLength,
		fmt.Sprintf("Served StreamInOut with total chunks [%d] and total payload length [%d]", chunkCount, payloadLength))
	return nil
}
