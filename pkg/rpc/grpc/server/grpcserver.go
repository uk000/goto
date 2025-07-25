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
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
)

type WrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

type GRPCServer struct {
	Server      *grpc.Server
	Listener    *listeners.Listener
	Services    map[string]*gotogrpc.GRPCService
	Running     bool
	grpcOptions []grpc.ServerOption
	wg          *sync.WaitGroup
}

const (
	grpcKeepaliveTime        = 30 * time.Second
	grpcKeepaliveTimeout     = 5 * time.Second
	grpcKeepaliveMinTime     = 30 * time.Second
	grpcMaxConcurrentStreams = 1000000
)

var (
	TheGRPCServer   *GRPCServer
	PortGRPCServers = map[int]*GRPCServer{}
)

func StartDefaultGRPCServer() {
	TheGRPCServer = &GRPCServer{}
	TheGRPCServer.init()
	TheGRPCServer.refreshServer()
	listeners.InitDefaultGRPCListener()
	TheGRPCServer.start(listeners.DefaultGRPCListener)
	PortGRPCServers[listeners.DefaultGRPCListener.Port] = TheGRPCServer
}

func AddService(s *gotogrpc.GRPCService, l *listeners.Listener) {
	gs := PortGRPCServers[l.Port]
	if gs == nil {
		gs = &GRPCServer{}
		PortGRPCServers[l.Port] = gs
		log.Printf("GRPC Server initialized for port [%d]", l.Port)
	}
	gs.Services[s.Name] = s
	log.Printf("GRPC Service [%s] added for port [%d]", s.Name, l.Port)
}

func Serve(s *gotogrpc.GRPCService, l *listeners.Listener) {
	gs := PortGRPCServers[l.Port]
	if gs == nil {
		gs = &GRPCServer{}
		PortGRPCServers[l.Port] = gs
		log.Printf("GRPC Server initialized for port [%d]", l.Port)
	} else {
		gs.Stop()
		log.Printf("GRPC Server stopped for port [%d]", l.Port)
	}
	gs.Services[s.Name] = s
	gs.refreshServer()
	gs.start(l)
}

func StopService(s *gotogrpc.GRPCService, l *listeners.Listener) {
	gs := PortGRPCServers[l.Port]
	if gs != nil {
		gs.Stop()
		log.Printf("GRPC Server stopped for port [%d]", l.Port)
	}
	delete(gs.Services, s.Name)
	gs.refreshServer()
	gs.start(l)
}

func createOrGetGRPCServer(l *listeners.Listener) *GRPCServer {
	gs := PortGRPCServers[l.Port]
	if gs == nil {
		gs = &GRPCServer{}
		PortGRPCServers[l.Port] = gs
		log.Printf("GRPC Server initialized for port [%d]", l.Port)
	} else {
		gs.Stop()
		log.Printf("GRPC Server stopped for port [%d]", l.Port)
	}
	gs.refreshServer()
	return gs
}

func StartWithCallback(l *listeners.Listener, callback func(gs *grpc.Server)) {
	gs := createOrGetGRPCServer(l)
	callback(gs.Server)
	gs.start(l)
}

func Start(l *listeners.Listener) {
	gs := createOrGetGRPCServer(l)
	gs.start(l)
}

func Stop(l *listeners.Listener) {
	gs := PortGRPCServers[l.Port]
	if gs != nil {
		gs.Stop()
		log.Printf("GRPC Server stopped for port [%d]", l.Port)
	}
}

func (g *GRPCServer) init() {
	if g.Running {
		return
	}
	if g.Services == nil {
		g.Services = map[string]*gotogrpc.GRPCService{}
	}
}

func (g *GRPCServer) refreshServer() {
	if g.Running {
		return
	}
	g.wg = &sync.WaitGroup{}
	g.grpcOptions = append(g.grpcOptions,
		grpc.MaxConcurrentStreams(grpcMaxConcurrentStreams),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    grpcKeepaliveTime,
			Timeout: grpcKeepaliveTimeout,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             grpcKeepaliveMinTime,
			PermitWithoutStream: true,
		}),
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(unaryMiddleware),
		grpc.ChainStreamInterceptor(streamMiddleware),
	)
	g.Server = grpc.NewServer(g.grpcOptions...)
}

func (g *GRPCServer) start(l *listeners.Listener) {
	g.Listener = l
	if l.Listener == nil {
		l.InitListener()
	}
	if len(g.Services) > 0 {
		for _, svc := range g.Services {
			g.Server.RegisterService(svc.GSD, &gotogrpc.DynamicServiceStub{})
			log.Printf("GRPC Service [%s] registered", svc.Name)
		}
	} else if len(g.Server.GetServiceInfo()) == 0 {
		RegisterGotoServer(g)
	}
	reflection.Register(g.Server)
	msg := ""
	if g.Running {
		msg = fmt.Sprintf("Restarting GRPC Server for port [%d]", l.Port)
		g.Stop()
	} else {
		msg = fmt.Sprintf("Starting GRPC Server for port [%d]", l.Port)
	}
	log.Println(msg)
	events.SendEventDirect(msg, "")
	go g.run()
	g.Running = true
}

func (g *GRPCServer) run() {
	global.OnGRPCStart(g)
	g.wg.Add(1)
	go g.serveListener(g.Listener)
	g.wg.Wait()
	global.OnGRPCStop()
}

func (g *GRPCServer) Serve(i interface{}) {
	l := i.(*listeners.Listener)
	g.Listener = l
	g.wg.Add(1)
	go g.serveListener(l)
}

func (g *GRPCServer) serveListener(l *listeners.Listener) {
	msg := fmt.Sprintf("GRPC Server [GRPC-%s] ready to serve services: ", l.HostLabel)
	for s := range g.Server.GetServiceInfo() {
		msg += fmt.Sprintf("[%s], ", s)
	}
	log.Println(msg)
	if err := g.Server.Serve(l.Listener); err != nil {
		log.Println(err)
	}
	global.OnGRPCStop()
}

func (g *GRPCServer) Stop() {
	if g.Running {
		log.Println("GRPC server shutting down")
		g.Server.Stop()
		time.Sleep(2 * time.Second)
		g.Running = false
		global.OnGRPCStop()
		events.SendEventDirect("GRPC Server Stopped", "")
	}
}

// func (g *GRPCServer) HandleGRPC(httpHandler http.Handler) http.Handler {
// 	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 		if util.IsGRPC(r) {
// 			g.server.ServeHTTP(w, r)
// 		} else {
// 			httpHandler.ServeHTTP(w, r)
// 		}
// 	})
// }

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
		util.AddLogMessageForContext(ctx, fmt.Sprintf("Service/Method [%s]: Error handling unary request: %v", info.FullMethod, err))
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
		util.AddLogMessageForContext(ctx, fmt.Sprintf("Service/Method [%s]: Error handling stream: %v", info.FullMethod, err))
	}
	_, rs := util.GetRequestStoreForContext(ctx)
	log.Print(strings.Join(rs.LogMessages, " --> "))
	return err
}
