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
	"net"
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
	// lock guards Listeners, Running and done, which the admin APIs, the listeners and the goroutine that serves all
	// reach.
	lock sync.Mutex
	// done is closed when the goroutine that serves is over, which is after the server has stopped and its services
	// were told so.
	done chan struct{}
	// served tells that the server was started, and can't be started again: a gRPC server that was stopped doesn't
	// serve any more.
	served bool
}

// GRPCServerManager changes the server and its services. The operations that stop, change and start the server one
// after another hold the lock, so that the operations of two requests don't mix up.
type GRPCServerManager struct {
	lock sync.Mutex
}

type ServiceInfoProvider struct {
	services map[string]grpc.ServiceInfo
	// lock guards services, which are added from the operations that change the server, and from the ones that load
	// the services of an upstream.
	lock sync.Mutex
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
	GRPCManager     = &GRPCServerManager{}
	SIP             = &ServiceInfoProvider{
		services: map[string]grpc.ServiceInfo{},
	}
)

// servicesLock guards the flags that tell if the services of Goto are registered, which the server resets from the
// goroutine that serves when it stops.
var servicesLock sync.Mutex

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

// asListener gives the listener that is passed as any, as the manager interface of the global package does. Anything
// else is not a listener to serve on.
func asListener(listener any) *listeners.Listener {
	l, _ := listener.(*listeners.Listener)
	if l == nil {
		log.Printf("GRPC: [%T] is not a listener to serve on", listener)
	}
	return l
}

func (sm *GRPCServerManager) AddListener(listener any) {
	if l := asListener(listener); l != nil {
		TheGRPCServer.addListener(l)
	}
}

func (sm *GRPCServerManager) ServeListener(listener any) {
	l := asListener(listener)
	if l == nil {
		return
	}
	sm.lock.Lock()
	defer sm.lock.Unlock()
	TheGRPCServer.addListener(l)
	if !TheGRPCServer.isRunning() {
		TheGRPCServer.start()
	} else {
		sm.restartServer()
	}
}

func (sm *GRPCServerManager) Serve(port int, s *gotogrpc.GRPCService) {
	sm.lock.Lock()
	defer sm.lock.Unlock()
	ServiceRegistry.AddActiveService(s)
	sm.refreshGRPCServer()
	TheGRPCServer.start()
	log.Printf("GRPC Service [%s] served on port [%d]", s.Name, port)
}

func (sm *GRPCServerManager) ServeMulti(portServices map[int][]*gotogrpc.GRPCService) {
	sm.lock.Lock()
	defer sm.lock.Unlock()
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
	sm.interceptAndServe(gsd, srv)
}

func (sm *GRPCServerManager) InterceptWithMiddleware(gsd *grpc.ServiceDesc, srv any) {
	sm.interceptAndServe(gsd, srv)
}

// interceptAndServe makes the methods of the service go through the interceptors, and have the server given serve
// them. The service is not served if there is no descriptor to load it from.
func (sm *GRPCServerManager) interceptAndServe(gsd *grpc.ServiceDesc, srv any) {
	service := ServiceRegistry.GetOrCreateService(gsd)
	if service == nil {
		log.Printf("Service [%s] can't be served: no descriptor to load it from", gsd.ServiceName)
		return
	}
	if err := sm.InterceptServiceMethods(service, service, srv, true); err != nil {
		log.Printf("Error intercepting service [%s]: %v", service.Name, err)
	}
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
	// Services are registered by their full names, which include the package: the short name of a service that has one
	// is never found, and would replace the registered service with a new one on every call.
	toService = ServiceRegistry.GetService(string(psd2.FullName()))
	if toService == nil {
		toService = ServiceRegistry.NewGRPCService(psd2)
	}
	if err := sm.InterceptServiceMethods(fromService, toService, srv, false); err != nil {
		log.Printf("Error intercepting service [%s] to proxy it to [%s]: %v", fromService.Name, toService.Name, err)
		return nil, nil
	}
	fromService.Server = &gotogrpc.DynamicServiceStub{}
	ServiceRegistry.AddProxyService(fromService, toService, teeport)
	SIP.AddService(fromService)
	return fromService, toService
}

func (sm *GRPCServerManager) Reflect(s *grpc.ServiceDesc) {
	psd, err := grpcreflect.LoadServiceDescriptor(s)
	if err != nil || psd == nil {
		log.Printf("Error loading service descriptor of [%s]: %v", s.ServiceName, err)
		return
	}
	SIP.AddService(ServiceRegistry.NewGRPCService(psd))
}

func (sm *GRPCServerManager) InterceptServiceMethods(proxy, original *gotogrpc.GRPCService, srv any, serviceByOriginal bool) error {
	if proxy == nil || original == nil || original.GSD == nil {
		return fmt.Errorf("Original and Proxy services required")
	}
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
	if proxy == nil || original == nil || proxy.GSD == nil || original.GSD == nil {
		return fmt.Errorf("Original and Proxy services required")
	}
	// A method that the service doesn't have comes back as a nil pointer in a non-nil interface.
	origMethod, _ := original.GetMethod(origMethodName).(*gotogrpc.GRPCServiceMethod)
	if origMethod == nil {
		return fmt.Errorf("Method [%s] not found on Service [%s]", origMethodName, original.Name)
	}
	proxyMethod, _ := proxy.GetMethod(proxyMethodName).(*gotogrpc.GRPCServiceMethod)
	if proxyMethod == nil {
		return fmt.Errorf("Method [%s] not found on Service [%s]", proxyMethodName, proxy.Name)
	}
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
			// the server that is given now is the one to serve, as it is when the service is registered again
			if srv != nil {
				interceptor.originalServer = srv
			}
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
			if srv != nil {
				interceptor.originalServer = srv
			}
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
	sm.lock.Lock()
	defer sm.lock.Unlock()
	ServiceRegistry.RemoveActiveService(s.Name)
	sm.refreshGRPCServer()
	TheGRPCServer.start()
}

func (sm *GRPCServerManager) StartWithCallback(l *listeners.Listener, callback func()) {
	sm.lock.Lock()
	defer sm.lock.Unlock()
	sm.refreshGRPCServer()
	TheGRPCServer.addListener(l)
	callback()
	TheGRPCServer.start()
}

// GetServiceInfo gives the services as they are now, since they are added while the server is changed.
func (sp *ServiceInfoProvider) GetServiceInfo() map[string]grpc.ServiceInfo {
	sp.lock.Lock()
	defer sp.lock.Unlock()
	services := make(map[string]grpc.ServiceInfo, len(sp.services))
	for name, si := range sp.services {
		services[name] = si
	}
	return services
}

func (sp *ServiceInfoProvider) GetService(name string) grpc.ServiceInfo {
	sp.lock.Lock()
	defer sp.lock.Unlock()
	return sp.services[name]
}

func (sp *ServiceInfoProvider) AddService(s *gotogrpc.GRPCService) {
	if s == nil {
		return
	}
	si := grpc.ServiceInfo{
		Methods: []grpc.MethodInfo{},
	}
	if s.GSD != nil {
		si.Metadata = s.GSD.Metadata
	}
	for _, method := range s.Methods {
		mi := grpc.MethodInfo{
			Name:           method.Name,
			IsClientStream: method.IsClientStream,
			IsServerStream: method.IsServerStream,
		}
		si.Methods = append(si.Methods, mi)
	}
	sp.lock.Lock()
	defer sp.lock.Unlock()
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

func (g *GRPCServer) addListener(l *listeners.Listener) {
	g.lock.Lock()
	defer g.lock.Unlock()
	g.Listeners[l.ListenerID] = l
}

func (g *GRPCServer) isRunning() bool {
	g.lock.Lock()
	defer g.lock.Unlock()
	return g.Running
}

func (g *GRPCServer) setRunning(running bool) {
	g.lock.Lock()
	defer g.lock.Unlock()
	g.Running = running
}

// listenerList gives the listeners as they are now, since they are added while the server uses them.
func (g *GRPCServer) listenerList() []*listeners.Listener {
	g.lock.Lock()
	defer g.lock.Unlock()
	list := make([]*listeners.Listener, 0, len(g.Listeners))
	for _, l := range g.Listeners {
		list = append(list, l)
	}
	return list
}

func (g *GRPCServer) refreshServer() {
	g.lock.Lock()
	defer g.lock.Unlock()
	if g.Running {
		return
	}
	g.Server = grpc.NewServer(g.grpcOptions...)
	g.served = false
}

// refreshServerIfServed gives the server that has been started, and stopped, a new one to start with.
func (g *GRPCServer) refreshServerIfServed() {
	g.lock.Lock()
	stale := g.served || g.Server == nil
	g.lock.Unlock()
	if stale {
		g.refreshServer()
	}
}

func (g *GRPCServer) recycleListeners(port int) {
	for _, l := range g.listenerList() {
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
	if g.isRunning() {
		msg = fmt.Sprintf("Restarting GRPC Server [GRPC-%s] on ports:", global.Self.HostLabel)
		g.Stop()
	} else {
		msg = fmt.Sprintf("Starting GRPC Server [GRPC-%s] on ports:", global.Self.HostLabel)
	}
	g.refreshServerIfServed()
	g.recycleListeners(0)
	ls := g.listenerList()
	serving := make([]net.Listener, 0, len(ls))
	for _, l := range ls {
		msg += fmt.Sprintf(" [%d]", l.Port)
		serving = append(serving, l.Listener)
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
	done := make(chan struct{})
	g.lock.Lock()
	g.done = done
	g.served = true
	server := g.Server
	g.lock.Unlock()
	go g.run(server, serving, done)
	g.setRunning(true)
}

// run serves on the listeners until the server is stopped. The server and the listeners are the ones that the start
// had, since the goroutine can outlive the server: a restart gives the server another one.
func (g *GRPCServer) run(server *grpc.Server, lns []net.Listener, done chan struct{}) {
	defer close(done)
	msg := "GRPC Server started with services: "
	for s := range server.GetServiceInfo() {
		msg += fmt.Sprintf("[%s], ", s)
	}
	log.Println(msg)
	wg := sync.WaitGroup{}
	wg.Add(len(lns))
	for _, ln := range lns {
		go func() {
			defer wg.Done()
			if err := server.Serve(ln); err != nil {
				log.Println(err)
			}
		}()
	}
	wg.Wait()
	log.Println("GRPC Server stopped")
	global.OnGRPCStop()
}

func (g *GRPCServer) Stop() {
	if g == nil {
		return
	}
	g.lock.Lock()
	running, server, done := g.Running, g.Server, g.done
	g.lock.Unlock()
	if running {
		log.Println("GRPC server shutting down")
		server.Stop()
		// The listeners are closed once the server is stopped, and the goroutine that serves is over when it has
		// told the services of that. Wait for it, instead of a time that may not be enough, so that the server
		// can be started again with nothing left of the one that stopped.
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			log.Println("GRPC server took too long to stop")
		}
		g.setRunning(false)
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

// getForcedStatus finds the status that is forced for a call, if any, which counts as a use of it. The headers that
// statuses are matched with are the ones of the request, along with the port.
func getForcedStatus(ctx context.Context, port int, method string, md metadata.MD) (statusCode, remaining int) {
	headers := map[string][]string{}
	if in, ok := metadata.FromIncomingContext(ctx); ok {
		for k, v := range in {
			headers[k] = v
		}
	}
	for k, v := range md {
		headers[k] = v
	}
	return gotostatus.TheStatusManager.GetStatusFor(port, method, headers, "")
}

func unaryMiddleware(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	port := util.GetGRPCPort(ctx)
	if port <= 0 {
		port = global.Self.GRPCPort
	}
	md := metadata.Pairs("port", fmt.Sprint(port))
	ctx, _ = util.WithRequestStoreForContext(util.WithPort(metadata.NewOutgoingContext(ctx, md), port))
	listenerLabel := global.Funcs.GetListenerLabelForPort(port)
	if statusCode, rem := getForcedStatus(ctx, port, info.FullMethod, md); statusCode > 0 && statusCode != 200 {
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
	if statusCode, rem := getForcedStatus(ctx, port, info.FullMethod, md); statusCode > 0 && statusCode != 200 {
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
