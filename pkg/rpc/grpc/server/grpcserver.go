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

func (sm *GRPCServerManager) AddListener(listener any) {
	l := listener.(*listeners.Listener)
	sm.lock.Lock()
	TheGRPCServer.Listeners[l.ListenerID] = l
	sm.lock.Unlock()
}

func (sm *GRPCServerManager) ServeListener(listener any) {
	l := listener.(*listeners.Listener)
	sm.AddListener(l)
	if !TheGRPCServer.Running {
		TheGRPCServer.start()
	} else {
		sm.restartServer()
	}
}

func (sm *GRPCServerManager) Serve(port int, s *gotogrpc.GRPCService) {
	ServiceRegistry.AddActiveService(s)
	sm.refreshGRPCServer()
	TheGRPCServer.start()
	log.Printf("GRPC Service [%s] served on port [%d]", s.Name, port)
}

func (sm *GRPCServerManager) ServeMulti(portServices map[int][]*gotogrpc.GRPCService) {
	for port, services := range portServices {
		for _, s := range services {
			ServiceRegistry.AddActiveService(s)
			log.Printf("GRPC Service [%s] served on port [%d]", s.Name, port)
		}
	}
	sm.refreshGRPCServer()
	TheGRPCServer.start()
}

func (sm *GRPCServerManager) InterceptAndServe(gsd *grpc.ServiceDesc, srv any) {
	service := ServiceRegistry.GetOrCreateService(gsd)
	sm.InterceptServiceMethods(service, service, srv, true)
	service.Server = srv
	ServiceRegistry.AddActiveService(service)
}

func (sm *GRPCServerManager) InterceptWithMiddleware(gsd *grpc.ServiceDesc, srv any) {
	service := ServiceRegistry.GetOrCreateService(gsd)
	sm.InterceptServiceMethods(service, service, srv, true)
	//service.Server = &gotogrpc.DynamicServiceStub{}
	service.Server = srv
	ServiceRegistry.AddActiveService(service)
}

func (sm *GRPCServerManager) InterceptAndProxy(fromGSD, toGSD *grpc.ServiceDesc, target string, srv any, teeport int) (global.IGRPCService, global.IGRPCService) {
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
	sm.InterceptServiceMethods(fromService, toService, srv, false)
	fromService.Server = &gotogrpc.DynamicServiceStub{}
	ServiceRegistry.AddProxyService(fromService, toService, teeport)
	SIP.AddService(fromService)
	return fromService, toService
}

func (sm *GRPCServerManager) Reflect(s *grpc.ServiceDesc) {
	psd, _ := grpcreflect.LoadServiceDescriptor(s)
	SIP.AddService(ServiceRegistry.NewGRPCService(psd))
}

func (sm *GRPCServerManager) InterceptServiceMethods(proxy, original *gotogrpc.GRPCService, srv any, serviceByOriginal bool) error {
	for i := range original.GSD.Streams {
		stream := &original.GSD.Streams[i]
		if err := sm.InterceptServiceMethod(proxy, original, stream.StreamName, stream.StreamName, srv, true, serviceByOriginal); err != nil {
			return err
		}
	}
	for i := range original.GSD.Methods {
		m := &original.GSD.Methods[i]
		if err := sm.InterceptServiceMethod(proxy, original, m.MethodName, m.MethodName, srv, false, serviceByOriginal); err != nil {
			return err
		}
	}
	return nil
}

func getStream(sd *grpc.ServiceDesc, stream string) *grpc.StreamDesc {
	for i := range sd.Streams {
		s := &sd.Streams[i]
		if strings.EqualFold(s.StreamName, stream) {
			return s
		}
	}
	return nil
}

func getMethod(sd *grpc.ServiceDesc, method string) *grpc.MethodDesc {
	for i := range sd.Methods {
		m := &sd.Methods[i]
		if strings.EqualFold(m.MethodName, method) {
			return m
		}
	}
	return nil
}

func (sm *GRPCServerManager) InterceptServiceMethod(proxy, original *gotogrpc.GRPCService, proxyMethodName, origMethodName string, srv any, stream bool, serviceByOriginal bool) error {
	if proxy == nil || original == nil {
		return fmt.Errorf("Original and Proxy services required")
	}
	origMethod := original.GetMethod(origMethodName).(*gotogrpc.GRPCServiceMethod)
	proxyMethod := proxy.GetMethod(proxyMethodName).(*gotogrpc.GRPCServiceMethod)
	if stream {
		origStreamDesc := getStream(original.GSD, origMethodName)
		if origStreamDesc == nil {
			return fmt.Errorf("Stream [%s] not found on Service [%s]", origMethodName, original.Name)
		}
		proxyStreamDesc := getStream(proxy.GSD, proxyMethodName)
		if proxyStreamDesc == nil {
			return fmt.Errorf("Stream [%s] not found on Service [%s]", proxyMethodName, proxy.Name)
		}
		var interceptor *GRPCStreamInterceptor
		if origMethod.Intercepted && origMethod.Interceptor != nil {
			interceptor = origMethod.Interceptor.(*GRPCStreamInterceptor)
		} else {
			interceptor = &GRPCStreamInterceptor{
				originalServer:    srv,
				isClientStreaming: origStreamDesc.ClientStreams,
				isServerStreaming: origStreamDesc.ServerStreams,
				serviceMethod:     origMethod,
				proxyMethod:       proxyMethod,
			}
			origMethod.Interceptor = interceptor
			origMethod.Intercepted = true
			if serviceByOriginal {
				interceptor.handler = origStreamDesc.Handler
			} else {
				interceptor.handler = proxyStreamDesc.Handler
			}
		}
		origStreamDesc.Handler = interceptor.Intercept
	} else {
		origMethodDesc := getMethod(original.GSD, origMethodName)
		if origMethodDesc == nil {
			return fmt.Errorf("Method [%s] not found on Service [%s]", origMethodName, original.Name)
		}
		proxyMethodDesc := getMethod(proxy.GSD, proxyMethodName)
		if proxyMethodDesc == nil {
			return fmt.Errorf("Method [%s] not found on Service [%s]", proxyMethodName, proxy.Name)
		}
		var interceptor *GRPCMethodInterceptor
		if origMethod.Intercepted && origMethod.Interceptor != nil {
			interceptor = origMethod.Interceptor.(*GRPCMethodInterceptor)
		} else {
			interceptor = &GRPCMethodInterceptor{
				originalServer: srv,
				serviceMethod:  origMethod,
				proxyMethod:    proxyMethod,
			}
			origMethod.Interceptor = interceptor
			origMethod.Intercepted = true
			if serviceByOriginal {
				interceptor.handler = origMethodDesc.Handler
			} else {
				interceptor.handler = proxyMethodDesc.Handler
			}
		}
		origMethodDesc.Handler = interceptor.Intercept
	}
	return nil
}

func (sm *GRPCServerManager) refreshGRPCServer() {
	TheGRPCServer.Stop()
	log.Println("GRPC Server stopped")
	TheGRPCServer.refreshServer()
}

func (sm *GRPCServerManager) restartServer() {
	sm.refreshGRPCServer()
	TheGRPCServer.start()
}

func (sm *GRPCServerManager) StopService(s *gotogrpc.GRPCService) {
	ServiceRegistry.RemoveActiveService(s.Name)
	sm.refreshGRPCServer()
	TheGRPCServer.start()
}

func (sm *GRPCServerManager) StartWithCallback(l *listeners.Listener, callback func()) {
	sm.refreshGRPCServer()
	sm.AddListener(l)
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
