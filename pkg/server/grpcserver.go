/**
 * Copyright 2021 uk
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

package server

import (
  "context"
  "fmt"
  . "goto/pkg/constants"
  "goto/pkg/events"
  "goto/pkg/global"
  "goto/pkg/grpc/pb"
  "goto/pkg/server/listeners"
  "goto/pkg/util"
  "io"
  "log"
  "net/http"
  "strings"
  "time"

  "google.golang.org/grpc"
  "google.golang.org/grpc/metadata"
  "google.golang.org/grpc/peer"
  "google.golang.org/grpc/reflection"
)

type IGRPCServer interface {
  Send(*pb.Output) error
}

type GRPCServer struct{}

type WrappedStream struct {
  grpc.ServerStream
  ctx context.Context
}

var (
  grpcServer *grpc.Server
)

func StartGRPCServer() {
  grpcServer = grpc.NewServer(grpc.UnaryInterceptor(ContextGRPCMiddleware), grpc.StreamInterceptor(ContextGRPCStreamMiddleware))
  reflection.Register(grpcServer)
  pb.RegisterGotoServer(grpcServer, &GRPCServer{})
  msg := "GRPC Server Started"
  log.Println(msg)
  events.SendEventDirect(msg, "")
}

func GRPCHandler(httpHandler http.Handler) http.Handler {
  StartGRPCServer()
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    if r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
      grpcServer.ServeHTTP(w, r)
    } else {
      httpHandler.ServeHTTP(w, r)
    }
  })
}

func NewWrappedStream(ss grpc.ServerStream, ctx context.Context) grpc.ServerStream {
  return WrappedStream{ServerStream: ss, ctx: ctx}
}

func (ws WrappedStream) Context() context.Context {
  return ws.ctx
}

func ContextGRPCMiddleware(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
  port := util.GetPortNumFromGRPCAuthority(ctx)
  if port <= 0 {
    port = global.ServerPort
  }
  md := metadata.Pairs("port", fmt.Sprint(port))
  return handler(withPort(metadata.NewOutgoingContext(ctx, md), port), req)
}

func ContextGRPCStreamMiddleware(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
  ctx := ss.Context()
  port := util.GetPortNumFromGRPCAuthority(ctx)
  if port <= 0 {
    port = global.ServerPort
  }
  return handler(srv, NewWrappedStream(ss, withPort(metadata.NewOutgoingContext(ctx, metadata.Pairs("port", fmt.Sprint(port))), port)))
}

func StopGRPCServer() {
  if grpcServer != nil {
    log.Println("GRPC server shutting down")
    grpcServer.GracefulStop()
    events.SendEventDirect("GRPC Server Stopped", "")
  }
}

func ServeGRPCListener(l *listeners.Listener) {
  go func() {
    msg := fmt.Sprintf("Starting GRPC Listener %s", l.ListenerID)
    log.Println(msg)
    events.SendEventForPort(l.Port, "GRPC Listener Started", msg)
    if err := grpcServer.Serve(l.Listener); err != nil {
      log.Println(err)
    }
  }()
}

func (gs *GRPCServer) setHeaders(ctx context.Context, port int) {
  hostLabel := util.GetHostLabel()
  listenerLabel := global.GetListenerLabelForPort(port)
  remoteAddress := ""
  if p, ok := peer.FromContext(ctx); ok {
    remoteAddress = p.Addr.String()
  }
  grpc.SendHeader(ctx, metadata.New(map[string]string{
    HeaderGotoHost:          hostLabel,
    HeaderViaGoto:           listenerLabel,
    HeaderGotoProtocol:      "GRPC",
    HeaderGotoPort:          fmt.Sprint(port),
    HeaderGotoRemoteAddress: remoteAddress,
  }))
}

func (gs *GRPCServer) Echo(ctx context.Context, input *pb.Input) (*pb.Output, error) {
  port := util.GetContextPort(ctx)
  log.Printf("GRPC[%d]: Serving Echo with payload length [%d]\n", port, len(input.Payload))
  events.TrackPortTrafficEvent(port, "GRPC.echo", 200)

  gs.setHeaders(ctx, port)
  hostLabel := util.GetHostLabel()
  listenerLabel := global.GetListenerLabelForPort(port)
  return &pb.Output{Payload: input.Payload, At: time.Now().Format(time.RFC3339Nano),
    GotoHost: hostLabel, GotoPort: int32(port), ViaGoto: listenerLabel}, nil
}

func (gs *GRPCServer) sendStreamResponse(ctx context.Context, port int, s *pb.StreamConfig, ss IGRPCServer) {
  gs.setHeaders(ctx, port)
  payload := ""
  if s.Payload != "" {
    payload = s.Payload
  } else {
    payload = util.GenerateRandomString(int(s.ChunkSize))
  }
  interval, err := time.ParseDuration(s.Interval)
  if err != nil {
    interval = 100 * time.Millisecond
  }
  hostLabel := util.GetHostLabel()
  listenerLabel := global.GetListenerLabelForPort(port)
  for i := 0; i < int(s.ChunkCount); i++ {
    ss.Send(&pb.Output{Payload: payload, At: time.Now().Format(time.RFC3339Nano),
      GotoHost: hostLabel, GotoPort: int32(port), ViaGoto: listenerLabel})
    time.Sleep(interval)
  }
}

func (gs *GRPCServer) StreamOut(s *pb.StreamConfig, os pb.Goto_StreamOutServer) error {
  ctx := os.Context()
  port := util.GetContextPort(ctx)
  log.Printf("GRPC[%d]: Serving StreamOut with config [chunkSize: %d, chunkCount: %d, interval: %s, payload: %s]\n",
    port, s.ChunkSize, s.ChunkCount, s.Interval, s.Payload)
  events.TrackPortTrafficEvent(port, "GRPC.streamOut.start", 200)
  gs.sendStreamResponse(ctx, port, s, os)
  events.TrackPortTrafficEvent(port, "GRPC.streamOut.end", 200)
  return nil
}

func (gs *GRPCServer) StreamInOut(ios pb.Goto_StreamInOutServer) error {
  ctx := ios.Context()
  port := util.GetContextPort(ctx)
  events.TrackPortTrafficEvent(port, "GRPC.streamInOut.start", 200)
  for {
    s, err := ios.Recv()
    if err == io.EOF {
      break
    } else if err != nil {
      return err
    }
    log.Printf("GRPC[%d]: Serving StreamOut with config [chunkSize: %d, chunkCount: %d, interval: %s, payload: %s]\n",
      port, s.ChunkSize, s.ChunkCount, s.Interval, s.Payload)
    gs.sendStreamResponse(ctx, port, s, ios)
  }
  events.TrackPortTrafficEvent(port, "GRPC.streamInOut.end", 200)
  return nil
}
