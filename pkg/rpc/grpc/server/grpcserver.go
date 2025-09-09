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

	"github.com/jhump/protoreflect/v2/grpcreflect"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type WrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

type GRPCServer struct {
	Server      *grpc.Server
	Listeners   map[string]*listeners.Listener
	Running     bool
	grpcOptions []grpc.ServerOption
}

type GRPCServerManager struct {
	lock sync.RWMutex
}

type ServiceInfoProvider struct {
	services            map[string]grpc.ServiceInfo
	reflectionServer    grpc_reflection_v1.ServerReflectionServer
	reflectionServerOld grpc_reflection_v1alpha.ServerReflectionServer
}

const (
	grpcKeepaliveTime        = 30 * time.Second
	grpcKeepaliveTimeout     = 5 * time.Second
	grpcKeepaliveMinTime     = 30 * time.Second
	grpcMaxConcurrentStreams = 1000000
)

var (
	TheGRPCServer   *GRPCServer
	ServiceRegistry *gotogrpc.GRPCServiceRegistry
	GRPCManager     = &GRPCServerManager{
		lock: sync.RWMutex{},
	}
	SIP = &ServiceInfoProvider{
		services: map[string]grpc.ServiceInfo{},
	}
)

func init() {
	global.GRPCServer = GRPCManager
	global.GRPCManager = GRPCManager
	ServiceRegistry = gotogrpc.ServiceRegistry
}

func StartDefaultGRPCServer() {
	TheGRPCServer = newGRPCServer()
	TheGRPCServer.refreshServer()
	listeners.InitDefaultGRPCListener()
	listeners.AddInitialGRPCListeners()
	GRPCManager.ServeListener(listeners.DefaultGRPCListener)
}

func (f *GRPCServerManager) AddListener(listener any) {
	l := listener.(*listeners.Listener)
	f.lock.Lock()
	TheGRPCServer.Listeners[l.ListenerID] = l
	f.lock.Unlock()
}

func (f *GRPCServerManager) ServeListener(listener any) {
	l := listener.(*listeners.Listener)
	f.AddListener(l)
	if !TheGRPCServer.Running {
		TheGRPCServer.start()
	} else {
		f.restartServer(l.Port)
	}
}

func (f *GRPCServerManager) Serve(port int, s *gotogrpc.GRPCService) {
	ServiceRegistry.AddActiveService(s)
	f.refreshGRPCServer(port)
	TheGRPCServer.start()
	log.Printf("GRPC Service [%s] served on port [%d]", s.Name, port)
}

func (f *GRPCServerManager) InterceptAndServe(gsd *grpc.ServiceDesc, srv any) {
	service := ServiceRegistry.GetOrCreateService(gsd)
	f.InterceptServiceMethods(service, service, srv, true)
	service.Server = srv
	ServiceRegistry.AddActiveService(service)
}

func (f *GRPCServerManager) InterceptWithMiddleware(gsd *grpc.ServiceDesc, srv any) {
	service := ServiceRegistry.GetOrCreateService(gsd)
	f.InterceptServiceMethods(service, nil, srv, false)
	service.Server = &gotogrpc.DynamicServiceStub{}
	ServiceRegistry.AddActiveService(service)
}

func (f *GRPCServerManager) InterceptAndProxy(fromGSD, toGSD *grpc.ServiceDesc, target string, srv any, teeport int) (global.IGRPCService, global.IGRPCService) {
	fromService := ServiceRegistry.GetService(fromGSD.ServiceName)
	if fromService == nil {
		psd, err := grpcreflect.LoadServiceDescriptor(fromGSD)
		if err != nil || psd == nil {
			log.Printf("Error loading service descriptor: %v", err)
			return nil, nil
		}
		fromService = ServiceRegistry.NewGRPCService(psd)
	}
	var toService *gotogrpc.GRPCService
	var err error
	var psd2 protoreflect.ServiceDescriptor
	if toGSD == nil {
		_, psd2, err = ServiceRegistry.ConvertToTargetService(fromGSD, target)
	} else {
		psd2, err = grpcreflect.LoadServiceDescriptor(toGSD)
	}
	if err != nil || psd2 == nil {
		log.Printf("Error converting service to target: %v", err)
		return nil, nil
	}
	toService = ServiceRegistry.GetService(string(psd2.Name()))
	if toService == nil {
		toService = ServiceRegistry.NewGRPCService(psd2)
	}
	f.InterceptServiceMethods(fromService, toService, srv, false)
	fromService.Server = &gotogrpc.DynamicServiceStub{}
	ServiceRegistry.AddProxyService(fromService, toService, teeport)
	SIP.AddService(fromService)
	return fromService, toService
}

func (f *GRPCServerManager) Reflect(s *grpc.ServiceDesc) {
	psd, _ := grpcreflect.LoadServiceDescriptor(s)
	SIP.AddService(ServiceRegistry.NewGRPCService(psd))
}

func (f *GRPCServerManager) InterceptServiceMethods(proxy, original *gotogrpc.GRPCService, srv any, serviceByOriginal bool) {
	var method *gotogrpc.GRPCServiceMethod
	for i, stream := range proxy.GSD.Streams {
		if original != nil {
			method = original.GetMethod(stream.StreamName).(*gotogrpc.GRPCServiceMethod)
		} else {
			method = proxy.GetMethod(stream.StreamName).(*gotogrpc.GRPCServiceMethod)
		}
		interceptor := &GRPCStreamInterceptor{
			originalServer:    srv,
			isClientStreaming: stream.ClientStreams,
			isServerStreaming: stream.ServerStreams,
			serviceMethod:     method,
		}
		if original != nil && serviceByOriginal {
			interceptor.originalHandler = original.GSD.Streams[i].Handler
		}
		proxy.GSD.Streams[i].Handler = interceptor.Intercept
	}
	for i, unary := range proxy.GSD.Methods {
		if original != nil {
			method = original.GetMethod(unary.MethodName).(*gotogrpc.GRPCServiceMethod)
		} else {
			method = proxy.GetMethod(unary.MethodName).(*gotogrpc.GRPCServiceMethod)
		}
		interceptor := &GRPCMethodInterceptor{
			originalServer: srv,
			serviceMethod:  method,
		}
		if original != nil && serviceByOriginal {
			interceptor.originalHandler = original.GSD.Methods[i].Handler
		}
		proxy.GSD.Methods[i].Handler = interceptor.Intercept
	}
}

func (f *GRPCServerManager) refreshGRPCServer(port int) {
	TheGRPCServer.Stop()
	log.Printf("GRPC Server stopped for port [%d]", port)
	TheGRPCServer.refreshServer()
}

func (f *GRPCServerManager) restartServer(port int) {
	f.refreshGRPCServer(port)
	TheGRPCServer.start()
}

func (f *GRPCServerManager) StopService(port int, s *gotogrpc.GRPCService) {
	ServiceRegistry.RemoveActiveService(s)
	f.refreshGRPCServer(port)
	TheGRPCServer.start()
}

func (f *GRPCServerManager) StopPort(port int) {
	TheGRPCServer.Stop()
	log.Printf("GRPC Server stopped for port [%d]", port)
}

func (f *GRPCServerManager) StartWithCallback(l *listeners.Listener, callback func()) {
	f.refreshGRPCServer(l.Port)
	f.AddListener(l)
	callback()
	TheGRPCServer.start()
}

func (sp *ServiceInfoProvider) GetServiceInfo() map[string]grpc.ServiceInfo {
	return sp.services
}

func (sp *ServiceInfoProvider) GetService(name string) grpc.ServiceInfo {
	return sp.services[name]
}

func (sp *ServiceInfoProvider) AddService(s *gotogrpc.GRPCService) {
	si := grpc.ServiceInfo{
		Methods:  []grpc.MethodInfo{},
		Metadata: s.GSD.Metadata,
	}
	for _, method := range s.Methods {
		mi := grpc.MethodInfo{
			Name:           method.Name,
			IsClientStream: method.IsClientStream,
			IsServerStream: method.IsServerStream,
		}
		si.Methods = append(si.Methods, mi)
	}
	sp.services[s.Name] = si
}

func newGRPCServer() *GRPCServer {
	return &GRPCServer{
		Listeners:   map[string]*listeners.Listener{},
		Running:     false,
		grpcOptions: []grpc.ServerOption{},
	}
}

func (g *GRPCServer) refreshServer() {
	if g.Running {
		return
	}
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

func (g *GRPCServer) start() {
	msg := ""
	if g.Running {
		msg = fmt.Sprintf("Restarting GRPC Server [GRPC-%s] on ports:", global.Self.HostLabel)
		g.Stop()
	} else {
		msg = fmt.Sprintf("Starting GRPC Server [GRPC-%s] on ports:", global.Self.HostLabel)
	}
	for _, l := range g.Listeners {
		if l.Listener == nil {
			if !l.InitListener() {
				log.Printf("Aboring GRPC operation for failed listener [%s]", l.ListenerID)
				return
			}
		}
		msg += fmt.Sprintf(" [%d]", l.Port)
	}
	global.OnGRPCStart()
	global.GRPCIntercept(GRPCManager)
	for _, svc := range ServiceRegistry.ActiveServices {
		g.Server.RegisterService(svc.GSD, svc.Server)
		SIP.AddService(svc)
	}
	registeredServices := g.Server.GetServiceInfo()
	for _, triple := range ServiceRegistry.ProxyServices {
		svc := triple.First.(*gotogrpc.GRPCService)
		if _, present := registeredServices[svc.Name]; !present {
			g.Server.RegisterService(svc.GSD, svc.Server)
		}
	}
	//reflection.Register(g.Server)
	if SIP.reflectionServer == nil {
		SIP.reflectionServer = reflection.NewServerV1(reflection.ServerOptions{Services: SIP})
		SIP.reflectionServerOld = reflection.NewServer(reflection.ServerOptions{Services: SIP})
	}
	grpc_reflection_v1.RegisterServerReflectionServer(g.Server, SIP.reflectionServer)
	grpc_reflection_v1alpha.RegisterServerReflectionServer(g.Server, SIP.reflectionServerOld)

	msg += ", with services:"
	for svc := range g.Server.GetServiceInfo() {
		msg += fmt.Sprintf(" [%s]", svc)
	}
	log.Println(msg)
	events.SendEventDirect(msg, "")
	go g.run()
	g.Running = true
}

func (g *GRPCServer) run() {
	msg := "GRPC Server started with services: "
	for s := range g.Server.GetServiceInfo() {
		msg += fmt.Sprintf("[%s], ", s)
	}
	log.Println(msg)
	wg := sync.WaitGroup{}
	wg.Add(len(g.Listeners))
	for _, l := range g.Listeners {
		go func() {
			if err := g.Server.Serve(l.Listener); err != nil {
				log.Println(err)
			}
			wg.Done()
		}()
	}
	wg.Wait()
	log.Println("GRPC Server stopped")
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
		util.LogMessage(ctx, fmt.Sprintf("Service/Method [%s]: Error handling unary request: %v", info.FullMethod, err))
	}
	_, rs := util.GetRequestStoreFromContext(ctx)
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
		util.LogMessage(ctx, fmt.Sprintf("Service/Method [%s]: Error handling stream: %v", info.FullMethod, err))
	}
	_, rs := util.GetRequestStoreFromContext(ctx)
	log.Print(strings.Join(rs.LogMessages, " --> "))
	return err
}
