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
	gotogrpc "goto/pkg/rpc/grpc"
	"goto/pkg/server/listeners"
	gotostatus "goto/pkg/server/response/status"
	"goto/pkg/util"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jhump/protoreflect/v2/grpcreflect"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	v1reflectiongrpc "google.golang.org/grpc/reflection/grpc_reflection_v1"
	v1alphareflectiongrpc "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"google.golang.org/grpc/status"
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
	services map[string]grpc.ServiceInfo
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
		f.restartServer()
	}
}

func (f *GRPCServerManager) Serve(port int, s *gotogrpc.GRPCService) {
	ServiceRegistry.AddActiveService(s)
	f.refreshGRPCServer()
	TheGRPCServer.start()
	log.Printf("GRPC Service [%s] served on port [%d]", s.Name, port)
}

func (f *GRPCServerManager) ServeMulti(portServices map[int][]*gotogrpc.GRPCService) {
	for port, services := range portServices {
		for _, s := range services {
			ServiceRegistry.AddActiveService(s)
			log.Printf("GRPC Service [%s] served on port [%d]", s.Name, port)
		}
	}
	f.refreshGRPCServer()
	TheGRPCServer.start()
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

func (f *GRPCServerManager) refreshGRPCServer() {
	TheGRPCServer.Stop()
	log.Println("GRPC Server stopped")
	TheGRPCServer.refreshServer()
}

func (f *GRPCServerManager) restartServer() {
	f.refreshGRPCServer()
	TheGRPCServer.start()
}

func (f *GRPCServerManager) StopService(s *gotogrpc.GRPCService) {
	ServiceRegistry.RemoveActiveService(s.Name)
	f.refreshGRPCServer()
	TheGRPCServer.start()
}

func (f *GRPCServerManager) StartWithCallback(l *listeners.Listener, callback func()) {
	f.refreshGRPCServer()
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
		Listeners: map[string]*listeners.Listener{},
		Running:   false,
		grpcOptions: []grpc.ServerOption{
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
		},
	}
}

func (g *GRPCServer) refreshServer() {
	if g.Running {
		return
	}
	g.Server = grpc.NewServer(g.grpcOptions...)
}

func (g *GRPCServer) recycleListeners(port int) {
	for _, l := range g.Listeners {
		if l.Listener == nil {
			if !l.InitListener() {
				log.Printf("Aboring GRPC operation for failed listener [%s]", l.ListenerID)
				return
			}
		} else if l.Port == port {
			if !l.ReopenListener() {
				log.Printf("listener [%s] failed to reopen", l.ListenerID)
			}
		}
	}
}

func (g *GRPCServer) registerServices() {
	registeredServices := g.Server.GetServiceInfo()
	for _, svc := range ServiceRegistry.ActiveServices {
		if _, present := registeredServices[svc.Name]; !present {
			g.Server.RegisterService(svc.GSD, svc.Server)
			SIP.AddService(svc)
		}
	}
	for _, triple := range ServiceRegistry.ProxyServices {
		svc := triple.First
		if _, present := registeredServices[svc.Name]; !present {
			g.Server.RegisterService(svc.GSD, svc.Server)
		}
	}

	_, v1present := registeredServices[v1reflectiongrpc.ServerReflection_ServiceDesc.ServiceName]
	_, v1alphapresent := registeredServices[v1alphareflectiongrpc.ServerReflection_ServiceDesc.ServiceName]
	if !v1present && !v1alphapresent {
		reflection.Register(g.Server)
	}
}

func (g *GRPCServer) start() {
	msg := ""
	if g.Running {
		msg = fmt.Sprintf("Restarting GRPC Server [GRPC-%s] on ports:", global.Self.HostLabel)
		g.Stop()
	} else {
		msg = fmt.Sprintf("Starting GRPC Server [GRPC-%s] on ports:", global.Self.HostLabel)
	}
	g.recycleListeners(0)
	for _, l := range g.Listeners {
		msg += fmt.Sprintf(" [%d]", l.Port)
	}
	global.OnGRPCStart()
	global.GRPCIntercept(GRPCManager)

	g.registerServices()
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
	port := util.GetGRPCPort(ctx)
	if port <= 0 {
		port = global.Self.GRPCPort
	}
	md := metadata.Pairs("port", fmt.Sprint(port))
	ctx, _ = util.WithRequestStoreForContext(util.WithPort(metadata.NewOutgoingContext(ctx, md), port))
	listenerLabel := global.Funcs.GetListenerLabelForPort(port)
	if statusCode, rem := gotostatus.TheStatusManager.GetStatusFor(port, info.FullMethod, md, ""); statusCode > 0 && statusCode != 200 {
		codeText := strconv.Itoa(statusCode)
		SetHeaders(ctx, port, global.Self.HostLabel, listenerLabel, map[string]string{
			constants.HeaderGotoForcedStatus:          codeText,
			constants.HeaderGotoForcedStatusRemaining: strconv.Itoa(rem),
		})
		return nil, status.Error(codes.Internal, fmt.Sprintf("%s=%s", constants.HeaderGotoForcedStatus, codeText))
	}
	resp, err := handler(ctx, req)
	if err != nil {
		util.LogMessage(ctx, fmt.Sprintf("Service/Method [%s]: Error handling unary request: %v", info.FullMethod, err))
	}
	_, rs := util.GetRequestStoreFromContext(ctx)
	if len(rs.LogMessages) > 0 {
		log.Printf("gRPC Access Log: %s\n", strings.Join(rs.LogMessages, " --> "))
	}
	return resp, err
}

func streamMiddleware(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	ctx := ss.Context()
	port := util.GetGRPCPort(ctx)
	if port <= 0 {
		port = global.Self.GRPCPort
	}
	md := metadata.Pairs("port", fmt.Sprint(port))
	ctx, _ = util.WithRequestStoreForContext(util.WithPort(metadata.NewOutgoingContext(ctx, md), port))
	listenerLabel := global.Funcs.GetListenerLabelForPort(port)
	if statusCode, rem := gotostatus.TheStatusManager.GetStatusFor(port, info.FullMethod, md, ""); statusCode > 0 && statusCode != 200 {
		codeText := strconv.Itoa(statusCode)
		SetHeaders(ctx, port, global.Self.HostLabel, listenerLabel, map[string]string{
			constants.HeaderGotoForcedStatus:          codeText,
			constants.HeaderGotoForcedStatusRemaining: strconv.Itoa(rem),
		})
		return status.Error(codes.Internal, fmt.Sprintf("%s=%s", constants.HeaderGotoForcedStatus, codeText))
	}
	err := handler(srv, NewWrappedStream(ss, ctx))
	if err != nil {
		util.LogMessage(ctx, fmt.Sprintf("Service/Method [%s]: Error handling stream: %v", info.FullMethod, err))
	}
	_, rs := util.GetRequestStoreFromContext(ctx)
	if len(rs.LogMessages) > 0 {
		log.Printf("gRPC Access Log: %s\n", strings.Join(rs.LogMessages, " --> "))
	}
	return err
}
