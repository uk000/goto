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
	"goto/pkg/rpc/grpc/pb"
	"goto/pkg/server/listeners"
	"goto/pkg/util"
	"io"
	"log"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

type IGRPCService interface {
	Send(*pb.Output) error
}

type GotoGRPCService struct {
	pb.UnimplementedGotoServer
}

func RegisterGotoServer(g *GRPCServer) {
	pb.RegisterGotoServer(g.server, &GotoGRPCService{})
	log.Println("Registered Goto GRPC Server")
}

func (gs *GotoGRPCService) setHeaders(ctx context.Context, port int, hostLabel, listenerLabel string) (requestHeaders, responseHeaders map[string]string) {
	remoteAddress := ""
	if p, ok := peer.FromContext(ctx); ok {
		remoteAddress = p.Addr.String()
	}
	responseHeaders = map[string]string{
		constants.HeaderGotoHost:          hostLabel,
		constants.HeaderViaGoto:           listenerLabel,
		constants.HeaderGotoProtocol:      "GRPC",
		constants.HeaderGotoPort:          fmt.Sprint(port),
		constants.HeaderGotoRemoteAddress: remoteAddress,
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
	gg.AddRequestLogMessage(ctx, port, "Goto", "echo", requestHeaders[constants.HeaderAuthority], requestHeaders, 1, len(input.Payload), requestMiniBody)

	response := &pb.Output{Payload: input.Payload, At: time.Now().Format(time.RFC3339Nano), GotoHost: hostLabel, GotoPort: int32(port), ViaGoto: listenerLabel}
	responseLength := -1
	if global.Flags.LogResponseBody || global.Flags.LogResponseMiniBody {
		responseBodyText := util.ToJSONText(response)
		responseLength = len(responseBodyText)
	}
	gg.AddResponseLogMessage(ctx, responseHeaders, 200, 1, responseLength, "Serving Echo")
	return response, nil
}

func (gs *GotoGRPCService) sendStreamResponse(ctx context.Context, port int, hostLabel, listenerLabel string, configInput *pb.StreamConfig, ss IGRPCService) (int, int) {
	gs.setHeaders(ctx, port, hostLabel, listenerLabel)
	payload := ""
	if configInput.Payload != "" {
		payload = configInput.Payload
	} else {
		payload = util.GenerateRandomString(int(configInput.ChunkSize))
	}
	interval, err := time.ParseDuration(configInput.Interval)
	if err != nil {
		interval = 100 * time.Millisecond
	}
	for i := 0; i < int(configInput.ChunkCount); i++ {
		ss.Send(&pb.Output{Payload: payload, At: time.Now().Format(time.RFC3339Nano),
			GotoHost: hostLabel, GotoPort: int32(port), ViaGoto: listenerLabel})
		time.Sleep(interval)
	}
	return int(configInput.ChunkCount), len(payload)
}

func (gs *GotoGRPCService) StreamOut(configInput *pb.StreamConfig, os pb.Goto_StreamOutServer) error {
	ctx := os.Context()
	port := util.GetContextPort(ctx)
	events.TrackPortTrafficEvent(port, "GRPC.streamOut.start", 200)
	hostLabel := listeners.GetHostLabelForPort(port)
	listenerLabel := global.Funcs.GetListenerLabelForPort(port)
	requestHeaders, responseHeaders := gs.setHeaders(ctx, port, hostLabel, listenerLabel)
	gg.AddRequestLogMessage(ctx, port, "Goto", "streamOut", requestHeaders[constants.HeaderAuthority], requestHeaders, 1, 0, "")

	responseCount, responseLength := gs.sendStreamResponse(ctx, port, hostLabel, listenerLabel, configInput, os)
	events.TrackPortTrafficEvent(port, "GRPC.streamOut.end", 200)
	gg.AddResponseLogMessage(ctx, responseHeaders, 200, responseCount, responseLength,
		fmt.Sprintf("Served StreamOut with config [chunkSize: %d, chunkCount: %d, interval: %s, payload size: %d]",
			configInput.ChunkSize, configInput.ChunkCount, configInput.Interval, len(configInput.Payload)))
	return nil
}

func (gs *GotoGRPCService) StreamInOut(ios pb.Goto_StreamInOutServer) error {
	ctx := ios.Context()
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
		configInput, err := ios.Recv()
		if err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		requestCount++
		chunkCount += int(configInput.ChunkCount)
		log.Printf("GRPC[%d]: Serving StreamInOut with config [chunkSize: %d, chunkCount: %d, interval: %s, payload size: [%d]]\n",
			port, configInput.ChunkSize, configInput.ChunkCount, configInput.Interval, len(configInput.Payload))
		count, length := gs.sendStreamResponse(ctx, port, hostLabel, listenerLabel, configInput, ios)
		responseCount += count
		responseLength += length
	}
	events.TrackPortTrafficEvent(port, "GRPC.streamInOut.end", 200)
	gg.AddRequestLogMessage(ctx, port, "Goto", "streamInOut", requestHeaders[constants.HeaderAuthority], requestHeaders, requestCount, 0, "")

	gg.AddResponseLogMessage(ctx, responseHeaders, 200, responseCount, responseLength,
		fmt.Sprintf("Served StreamInOut with total chunks [%d] and total payload length [%d]", chunkCount, payloadLength))
	return nil
}
