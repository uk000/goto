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
	"goto/pkg/events"
	"goto/pkg/global"
	gotogrpc "goto/pkg/rpc/grpc"
	"goto/pkg/server/listeners"
	"goto/pkg/util"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	otelgrpc "go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
)

type WrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

type GRPCServer struct {
	server    *grpc.Server
	listeners map[string]*listeners.Listener
	running   bool
	wg        *sync.WaitGroup
}

var (
	TheGRPCServer *GRPCServer
)

func StartGRPCServer() {
	TheGRPCServer = &GRPCServer{}
	TheGRPCServer.init()
	TheGRPCServer.Start(nil)
	//RegisterGotoServer(TheGRPCServer)
}

func serve(s *gotogrpc.GRPCService, l *listeners.Listener) {
	TheGRPCServer.RegisterService(s.GSD)
	TheGRPCServer.Start(l)
}

func (g *GRPCServer) init() {
	g.listeners = map[string]*listeners.Listener{}
	g.wg = &sync.WaitGroup{}
	g.server = grpc.NewServer(
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(unaryMiddleware),
		grpc.ChainStreamInterceptor(streamMiddleware))
	reflection.Register(g.server)
}

func (g *GRPCServer) RegisterService(sd *grpc.ServiceDesc) {
	g.Stop()
	g.init()
	g.server.RegisterService(sd, &gotogrpc.DynamicServiceStub{})
	log.Printf("GRPC Service [%s] registered", sd.ServiceName)
}

func (g *GRPCServer) Start(l *listeners.Listener) {
	if l != nil {
		g.listeners[l.ListenerID] = l
	} else {
		l = listeners.DefaultGRPCListener
		g.listeners[listeners.DefaultGRPCListener.ListenerID] = l
	}
	if l.Listener == nil {
		l.InitListener()
	}
	msg := ""
	if g.running {
		msg = "Restarting GRPC Server..."
		g.Stop()
	} else {
		msg = "Starting GRPC Server..."
	}
	log.Println(msg)
	events.SendEventDirect(msg, "")
	go g.run()
	g.running = true
}

func (g *GRPCServer) run() {
	global.OnGRPCStart(g)
	for _, l := range g.listeners {
		g.wg.Add(1)
		go g.serveListener(l)
	}
	g.wg.Wait()
	global.OnGRPCStop()
}

func (g *GRPCServer) Serve(i interface{}) {
	l := i.(*listeners.Listener)
	g.listeners[l.ListenerID] = l
	g.wg.Add(1)
	go g.serveListener(l)
}

func (g *GRPCServer) serveListener(l *listeners.Listener) {
	msg := fmt.Sprintf("GRPC Server [GRPC-%s] ready to serve services: ", l.HostLabel)
	for s := range g.server.GetServiceInfo() {
		msg += fmt.Sprintf("[%s], ", s)
	}
	log.Println(msg)
	if err := g.server.Serve(l.Listener); err != nil {
		log.Println(err)
	}
	global.OnGRPCStop()
}

func (g *GRPCServer) Stop() {
	if g.running {
		log.Println("GRPC server shutting down")
		g.server.Stop()
		time.Sleep(2 * time.Second)
		g.running = false
		global.OnGRPCStop()
		events.SendEventDirect("GRPC Server Stopped", "")
	}
}

func (g *GRPCServer) HandleGRPC(httpHandler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if util.IsGRPC(r) {
			g.server.ServeHTTP(w, r)
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

func unaryMiddleware(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	port := util.GetPortNumFromGRPCAuthority(ctx)
	if port <= 0 {
		port = global.Self.GRPCPort
	}
	md := metadata.Pairs("port", fmt.Sprint(port))
	ctx, _ = util.WithRequestStoreForContext(util.WithPort(metadata.NewOutgoingContext(ctx, md), port))
	resp, err := handler(ctx, req)
	if err != nil {
		util.AddLogMessageForContext(fmt.Sprintf("Service/Method [%s]: Error handling unary request: %v", info.FullMethod, err), ctx)
	}
	_, rs := util.GetRequestStoreForContext(ctx)
	log.Print(strings.Join(rs.LogMessages, " --> "))
	return resp, err
}

func streamMiddleware(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	ctx := ss.Context()
	port := util.GetPortNumFromGRPCAuthority(ctx)
	if port <= 0 {
		port = global.Self.GRPCPort
	}
	md := metadata.Pairs("port", fmt.Sprint(port))
	ctx, _ = util.WithRequestStoreForContext(util.WithPort(metadata.NewOutgoingContext(ctx, md), port))
	err := handler(srv, NewWrappedStream(ss, ctx))
	if err != nil {
		util.AddLogMessageForContext(fmt.Sprintf("Service/Method [%s]: Error handling stream: %v", info.FullMethod, err), ctx)
	}
	_, rs := util.GetRequestStoreForContext(ctx)
	log.Print(strings.Join(rs.LogMessages, " --> "))
	return err
}
