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

package grpc

import (
	"context"
	"encoding/json"
	"fmt"
	"goto/pkg/global"
	"goto/pkg/rpc"
	"goto/pkg/server/response/payload"
	"goto/pkg/types"
	"goto/pkg/util"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jhump/protoreflect/v2/grpcreflect"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

type MessageTransformer func(proto.Message) proto.Message

type GRPCService struct {
	global.IGRPCService
	Name    string                         `json:"name"`
	URI     string                         `json:"uri"`
	Methods map[string]*GRPCServiceMethod  `json:"methods"`
	GSD     *grpc.ServiceDesc              `json:"-"`
	PSD     protoreflect.ServiceDescriptor `json:"-"`
	Server  any                            `json:"-"`
}

type GRPCServiceMethod struct {
	Name             string                        `json:"name"`
	URI              string                        `json:"uri"`
	IsUnary          bool                          `json:"isUnary"`
	IsClientStream   bool                          `json:"isClientStream"`
	IsServerStream   bool                          `json:"isServerStream"`
	IsBidiStream     bool                          `json:"isBidiStream"`
	ResponsePayload  GRPCResponsePayload           `json:"responsePayload"`
	StreamCount      int                           `json:"streamCount"`
	StreamDelayMin   time.Duration                 `json:"streamDelayMin"`
	StreamDelayMax   time.Duration                 `json:"streamDelayMax"`
	StreamDelayCount int                           `json:"streamDelayCount"`
	In               MessageTransformer            `json:"-"`
	Out              MessageTransformer            `json:"-"`
	StreamOut        func() []util.JSON            `json:"-"`
	Service          *GRPCService                  `json:"-"`
	GMD              *grpc.MethodDesc              `json:"-"`
	GSD              *grpc.StreamDesc              `json:"-"`
	PMD              protoreflect.MethodDescriptor `json:"-"`
	Interceptor      any
	Intercepted      bool
}

type GRPCResponsePayload []byte

type GRPCServiceRegistry struct {
	Services       map[string]*GRPCService
	ActiveServices map[string]*GRPCService
	ProxyServices  map[string]*types.Triple[*GRPCService, *GRPCService, int]
	TeeServices    map[int]*types.Pair[*GRPCService, *GRPCService]
	lock           sync.RWMutex
}

type DynamicServiceInterface interface{}
type DynamicServiceStub struct{}
type ProxyGRPCUnaryFunc func(context.Context, int, *GRPCServiceMethod, metadata.MD, []proto.Message) ([]proto.Message, metadata.MD, metadata.MD, error)
type ProxyGRPCStreamFunc func(context.Context, int, *GRPCServiceMethod, string, metadata.MD, GRPCStream) (int, int, error)

var _ DynamicServiceInterface = (*DynamicServiceStub)(nil)

var (
	// methodLock guards what the APIs configure on the methods (the stream settings and the response payload), which
	// the servers read while they serve. It's a lock of the package, as the methods are copied by value.
	methodLock        sync.RWMutex
	ServiceRegistry   = &GRPCServiceRegistry{lock: sync.RWMutex{}}
	GRPCUnaryHandler  func(*GRPCServiceMethod) grpc.MethodHandler
	GRPCStreamHandler func(interface{}, grpc.ServerStream) error
	ProxyGRPCUnary    ProxyGRPCUnaryFunc
	ProxyGRPCStream   ProxyGRPCStreamFunc
)

// init creates the empty registry and publishes it as the "grpc" service registry of the rpc package.
func init() {
	ServiceRegistry.Init()
	rpc.GetServiceRegistry["grpc"] = func(_ int) rpc.RPCServiceRegistry { return ServiceRegistry }
}

// Init resets the registry to empty service, active service, proxy and tee maps.
func (gsr *GRPCServiceRegistry) Init() {
	gsr.lock.Lock()
	defer gsr.lock.Unlock()
	gsr.Services = map[string]*GRPCService{}
	gsr.ActiveServices = map[string]*GRPCService{}
	gsr.ProxyServices = map[string]*types.Triple[*GRPCService, *GRPCService, int]{}
	gsr.TeeServices = map[int]*types.Pair[*GRPCService, *GRPCService]{}
}

// GetService returns the registered service of the full name, or nil.
func (gsr *GRPCServiceRegistry) GetService(name string) *GRPCService {
	gsr.lock.RLock()
	defer gsr.lock.RUnlock()
	return gsr.Services[name]
}

// GetOrCreateService returns the registered service of the descriptor, and creates it if there isn't one yet. It
// returns nil if the service isn't registered and a descriptor of it can't be loaded.
func (gsr *GRPCServiceRegistry) GetOrCreateService(gsd *grpc.ServiceDesc) *GRPCService {
	service := gsr.GetService(gsd.ServiceName)
	if service == nil {
		psd, err := grpcreflect.LoadServiceDescriptor(gsd)
		if err != nil || psd == nil {
			log.Printf("Error loading service descriptor of [%s]: %v", gsd.ServiceName, err)
			return nil
		}
		service = gsr.NewGRPCServiceFromDescriptors(gsd, psd)
	}
	return service
}

// GetActiveService returns the service of the name if it is being served, or nil.
func (gsr *GRPCServiceRegistry) GetActiveService(name string) *GRPCService {
	gsr.lock.RLock()
	defer gsr.lock.RUnlock()
	return gsr.ActiveServices[name]
}

// ServicesToRegister gives the services that a gRPC server registers when it starts: the ones that are served and the
// ones that are proxied, by name. A server takes one service of a name, and registering a second is fatal, so a service
// that is served and also proxied is given once. It is the service of the proxy, whose handlers choose for each call
// whether to proxy it or serve it, by the port that the call came in on: that is how a service is served on one port
// and proxied on another.
func (gsr *GRPCServiceRegistry) ServicesToRegister() []*GRPCService {
	gsr.lock.RLock()
	defer gsr.lock.RUnlock()
	byName := make(map[string]*GRPCService, len(gsr.ActiveServices)+len(gsr.ProxyServices))
	for _, s := range gsr.ActiveServices {
		byName[s.Name] = s
	}
	for _, triple := range gsr.ProxyServices {
		byName[triple.First.Name] = triple.First
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	services := make([]*GRPCService, 0, len(names))
	for _, name := range names {
		services = append(services, byName[name])
	}
	return services
}

// GetProxyService returns the source and target services and the tee port of the proxy configured for the service name, or nils.
func (gsr *GRPCServiceRegistry) GetProxyService(name string) (*GRPCService, *GRPCService, int) {
	gsr.lock.RLock()
	defer gsr.lock.RUnlock()
	triple := gsr.ProxyServices[name]
	if triple != nil {
		return triple.First, triple.Second, triple.Third
	}
	return nil, nil, 0
}

// GetTeeServices returns the source and target services that are tee'd on the port, or nils.
func (gsr *GRPCServiceRegistry) GetTeeServices(port int) (*GRPCService, *GRPCService) {
	gsr.lock.RLock()
	defer gsr.lock.RUnlock()
	pair := gsr.TeeServices[port]
	if pair != nil {
		return pair.Left, pair.Right
	}
	return nil, nil
}

// AddActiveService marks the service as served.
func (gsr *GRPCServiceRegistry) AddActiveService(s *GRPCService) {
	gsr.lock.Lock()
	defer gsr.lock.Unlock()
	gsr.ActiveServices[s.Name] = s
}

// AddProxyService registers a proxy of one service to another, along with a tee pair on teeport when it is positive.
func (gsr *GRPCServiceRegistry) AddProxyService(from, to *GRPCService, teeport int) {
	gsr.lock.Lock()
	defer gsr.lock.Unlock()
	gsr.ProxyServices[from.Name] = types.NewTriple(from, to, teeport)
	if teeport > 0 {
		gsr.TeeServices[teeport] = types.NewPair(from, to)
	}
}

// RemoveActiveService stops the named service from being served.
func (gsr *GRPCServiceRegistry) RemoveActiveService(name string) {
	gsr.lock.Lock()
	defer gsr.lock.Unlock()
	delete(gsr.ActiveServices, name)
}

// RemoveProxyService removes the proxy configured for the named service.
func (gsr *GRPCServiceRegistry) RemoveProxyService(name string) {
	gsr.lock.Lock()
	defer gsr.lock.Unlock()
	delete(gsr.ProxyServices, name)
}

// RemoveTeeService removes the tee pair of the port.
func (gsr *GRPCServiceRegistry) RemoveTeeService(port int) {
	gsr.lock.Lock()
	defer gsr.lock.Unlock()
	delete(gsr.TeeServices, port)
}

// GetRPCService returns the named service as an rpc.RPCService, or a nil interface if it isn't registered.
func (gsr *GRPCServiceRegistry) GetRPCService(name string) rpc.RPCService {
	if s := gsr.GetService(name); s != nil {
		return s
	}
	return nil
}

// MarshalJSON encodes the services with the lock of the registry held, since services are added and removed while the
// registry is being listed.
func (gsr *GRPCServiceRegistry) MarshalJSON() ([]byte, error) {
	gsr.lock.RLock()
	defer gsr.lock.RUnlock()
	return json.Marshal(struct {
		Services       map[string]*GRPCService
		ActiveServices map[string]*GRPCService
		ProxyServices  map[string]*types.Triple[*GRPCService, *GRPCService, int]
		TeeServices    map[int]*types.Pair[*GRPCService, *GRPCService]
	}{gsr.Services, gsr.ActiveServices, gsr.ProxyServices, gsr.TeeServices})
}

// MarshalJSON encodes the service with the lock of the methods held, since the APIs configure the methods while the
// services are being listed.
func (s *GRPCService) MarshalJSON() ([]byte, error) {
	type plain GRPCService
	methodLock.RLock()
	defer methodLock.RUnlock()
	return json.Marshal((*plain)(s))
}

// RemoveService unregisters the named service.
func (gsr *GRPCServiceRegistry) RemoveService(name string) {
	gsr.lock.Lock()
	defer gsr.lock.Unlock()
	delete(gsr.Services, name)
}

// TrackService registers the named service, if it is registered, with the RPC tracker of the port.
func (gsr *GRPCServiceRegistry) TrackService(port int, name string, headers []string, header, value string) {
	gsr.lock.Lock()
	defer gsr.lock.Unlock()
	if gsr.Services[name] != nil {
		rpc.GetRPCTracker(port).TrackService(port, gsr.Services[name], headers, header, value)
	}
}

// NewGRPCServiceFromSD loads the protobuf descriptor of a grpc.ServiceDesc and registers a service built from it, or returns nil if it can't be loaded.
func (gsr *GRPCServiceRegistry) NewGRPCServiceFromSD(sd *grpc.ServiceDesc) *GRPCService {
	psd, err := grpcreflect.LoadServiceDescriptor(sd)
	if err != nil || psd == nil {
		log.Printf("Error loading service descriptor: %v", err)
		return nil
	}
	return gsr.NewGRPCService(psd)
}

// NewGRPCServiceFromDescriptors registers a service built from existing grpc and protobuf descriptors, linking each method to the descriptor's own unary or stream entry.
func (gsr *GRPCServiceRegistry) NewGRPCServiceFromDescriptors(sd *grpc.ServiceDesc, psd protoreflect.ServiceDescriptor) *GRPCService {
	serviceName := string(psd.FullName())
	sd.HandlerType = (*DynamicServiceInterface)(nil)
	service := GRPCService{
		Name:    serviceName,
		URI:     strings.ToLower(fmt.Sprintf("/%s", serviceName)),
		Methods: make(map[string]*GRPCServiceMethod),
		GSD:     sd,
		PSD:     psd,
	}
	streamIndex := 0
	methodIndex := 0
	for i := 0; i < psd.Methods().Len(); i++ {
		pmd := psd.Methods().Get(i)
		methodName := string(pmd.Name())
		method := &GRPCServiceMethod{
			Name:           methodName,
			URI:            strings.ToLower(fmt.Sprintf("/%s/%s", serviceName, methodName)),
			PMD:            pmd,
			IsClientStream: pmd.IsStreamingClient(),
			IsServerStream: pmd.IsStreamingServer(),
			IsBidiStream:   pmd.IsStreamingClient() && pmd.IsStreamingServer(),
			IsUnary:        !pmd.IsStreamingClient() && !pmd.IsStreamingServer(),
			Service:        &service,
		}
		if pmd.IsStreamingClient() || pmd.IsStreamingServer() {
			method.GSD = &sd.Streams[streamIndex]
			streamIndex++
		} else {
			method.GMD = &sd.Methods[methodIndex]
			methodIndex++
		}
		service.Methods[methodName] = method
	}
	gsr.lock.Lock()
	defer gsr.lock.Unlock()
	gsr.Services[service.Name] = &service
	return &service
}

// NewGRPCService registers a service built from a protobuf descriptor, creating its grpc.ServiceDesc with the generic unary and stream handlers for each method.
func (gsr *GRPCServiceRegistry) NewGRPCService(psd protoreflect.ServiceDescriptor) *GRPCService {
	serviceName := string(psd.FullName())
	gsd := &grpc.ServiceDesc{
		ServiceName: serviceName,
		HandlerType: (*DynamicServiceInterface)(nil),
		Methods:     []grpc.MethodDesc{},
		Streams:     []grpc.StreamDesc{},
	}
	service := GRPCService{
		Name:    serviceName,
		URI:     strings.ToLower(fmt.Sprintf("/%s", serviceName)),
		Methods: make(map[string]*GRPCServiceMethod),
		GSD:     gsd,
		PSD:     psd,
	}
	for i := 0; i < psd.Methods().Len(); i++ {
		pmd := psd.Methods().Get(i)
		methodName := string(pmd.Name())
		method := &GRPCServiceMethod{
			Name:           methodName,
			URI:            strings.ToLower(fmt.Sprintf("/%s/%s", serviceName, methodName)),
			PMD:            pmd,
			IsClientStream: pmd.IsStreamingClient(),
			IsServerStream: pmd.IsStreamingServer(),
			IsBidiStream:   pmd.IsStreamingClient() && pmd.IsStreamingServer(),
			IsUnary:        !pmd.IsStreamingClient() && !pmd.IsStreamingServer(),
			Service:        &service,
		}
		if pmd.IsStreamingClient() || pmd.IsStreamingServer() {
			method.GSD = &grpc.StreamDesc{
				StreamName:    methodName,
				Handler:       GRPCStreamHandler,
				ClientStreams: pmd.IsStreamingClient(),
				ServerStreams: pmd.IsStreamingServer(),
			}
			gsd.Streams = append(gsd.Streams, *method.GSD)
		} else {
			method.GMD = &grpc.MethodDesc{
				MethodName: methodName,
				Handler:    GRPCUnaryHandler(method),
			}
			gsd.Methods = append(gsd.Methods, *method.GMD)
		}
		service.Methods[methodName] = method
	}
	gsr.lock.Lock()
	defer gsr.lock.Unlock()
	gsr.Services[service.Name] = &service
	return &service
}

// ConvertToTargetService copies the service descriptor under the target service name and loads the protobuf descriptor of the copy.
func (gsr *GRPCServiceRegistry) ConvertToTargetService(gsd *grpc.ServiceDesc, target string) (*grpc.ServiceDesc, protoreflect.ServiceDescriptor, error) {
	gsd2 := *gsd
	gsd2.ServiceName = target
	psd2, err := grpcreflect.LoadServiceDescriptor(&gsd2)
	if err != nil {
		return nil, nil, err
	}
	return &gsd2, psd2, nil
}

// CloneService returns the registered service of the target name, or else an unregistered copy of the service renamed to target that has its own copies of the methods and handlers.
func (gsr *GRPCServiceRegistry) CloneService(service *GRPCService, target string) (targetService *GRPCService, err error) {
	targetService = gsr.GetService(target)
	if targetService != nil {
		return
	}
	if service == nil || service.GSD == nil {
		return nil, fmt.Errorf("service [%s] has no service descriptor to clone", target)
	}
	s2 := *service
	s2.Name = target
	s2.URI = strings.ToLower(fmt.Sprintf("/%s", target))
	s2.Methods = make(map[string]*GRPCServiceMethod)
	// The descriptor is a shallow copy, so the slices of its methods and streams have to be copied too: the handlers
	// of the clone are set into them, which must not change the handlers of the service that is cloned.
	gsd2 := *service.GSD
	gsd2.ServiceName = target
	gsd2.Methods = append([]grpc.MethodDesc(nil), service.GSD.Methods...)
	gsd2.Streams = append([]grpc.StreamDesc(nil), service.GSD.Streams...)
	for i, gsdm := range gsd2.Methods {
		m := service.Methods[gsdm.MethodName]
		methodLock.RLock()
		m2 := *m
		methodLock.RUnlock()
		m2.Service = &s2
		m2.URI = strings.ToLower(fmt.Sprintf("/%s/%s", target, m.Name))
		m2.GMD = &gsd2.Methods[i]
		gsd2.Methods[i].Handler = GRPCUnaryHandler(&m2)
		s2.Methods[m2.Name] = &m2
	}
	for i, gsds := range gsd2.Streams {
		m := service.Methods[gsds.StreamName]
		methodLock.RLock()
		m2 := *m
		methodLock.RUnlock()
		m2.Service = &s2
		m2.URI = strings.ToLower(fmt.Sprintf("/%s/%s", target, m.Name))
		m2.GSD = &gsd2.Streams[i]
		gsd2.Streams[i].Handler = GRPCStreamHandler
		s2.Methods[m2.Name] = &m2
	}
	var psd2 protoreflect.ServiceDescriptor
	if psd2, err = grpcreflect.LoadServiceDescriptor(&gsd2); err != nil {
		return nil, err
	}
	s2.GSD = &gsd2
	s2.PSD = psd2
	targetService = &s2
	return
}

// ParseGRPCServiceMethod finds the registered method that the gRPC call of the context is for, or nil.
func (gsr *GRPCServiceRegistry) ParseGRPCServiceMethod(ctx context.Context) *GRPCServiceMethod {
	methodName, ok := grpc.Method(ctx)
	if !ok || methodName == "" {
		return nil
	}
	serviceName := ""
	parts := strings.Split(methodName, "/")
	if len(parts) > 2 {
		serviceName = parts[1]
		methodName = parts[2]
	}
	var method *GRPCServiceMethod
	gsr.lock.RLock()
	defer gsr.lock.RUnlock()
	if service := gsr.Services[serviceName]; service != nil {
		method = service.Methods[methodName]
	}
	return method
}

// IsGRPC reports true, as this is a gRPC service.
func (s *GRPCService) IsGRPC() bool {
	return true
}

// IsJSONRPC reports false, as this is not a JSON-RPC service.
func (s *GRPCService) IsJSONRPC() bool {
	return false
}

// GetName returns the full name of the service.
func (s *GRPCService) GetName() string {
	return s.Name
}

// GetURI returns the lowercased URI path of the service.
func (s *GRPCService) GetURI() string {
	return s.URI
}

// HasMethod tells if the service has the named method.
func (s *GRPCService) HasMethod(m string) bool {
	return s.Methods != nil && s.Methods[m] != nil
}

// GetMethodCount returns the number of methods of the service.
func (s *GRPCService) GetMethodCount() int {
	return len(s.Methods)
}

// ForEachMethod calls f for each method of the service.
func (s *GRPCService) ForEachMethod(f func(rpc.RPCMethod)) {
	for _, m := range s.Methods {
		f(m)
	}
}

// GetMethod returns the named method; a method that isn't there comes back as a nil *GRPCServiceMethod inside a non-nil interface.
func (s *GRPCService) GetMethod(name string) rpc.RPCMethod {
	return s.Methods[name]
}

// GetName returns the name of the method.
func (m *GRPCServiceMethod) GetName() string {
	return m.Name
}

// GetURI returns the lowercased URI path of the method.
func (m *GRPCServiceMethod) GetURI() string {
	return m.URI
}

// SetStreamCount sets the number of responses of a stream, with the methods lock held.
func (m *GRPCServiceMethod) SetStreamCount(count int) {
	methodLock.Lock()
	defer methodLock.Unlock()
	m.StreamCount = count
}

// SetStreamDelay sets the delay range and the count of the messages to delay in a stream, with the methods lock held.
func (m *GRPCServiceMethod) SetStreamDelay(min, max time.Duration, count int) {
	methodLock.Lock()
	defer methodLock.Unlock()
	m.StreamDelayMin = min
	m.StreamDelayMax = max
	m.StreamDelayCount = count
}

// SetResponsePayload sets the response payload of the method, with the methods lock held.
func (m *GRPCServiceMethod) SetResponsePayload(payload []byte) {
	methodLock.Lock()
	defer methodLock.Unlock()
	m.ResponsePayload = payload
}

// GetStreamConfig returns the stream settings of the method: the number of responses, and the delay between them.
func (m *GRPCServiceMethod) GetStreamConfig() (count int, delayMin, delayMax time.Duration, delayCount int) {
	methodLock.RLock()
	defer methodLock.RUnlock()
	return m.StreamCount, m.StreamDelayMin, m.StreamDelayMax, m.StreamDelayCount
}

// GetResponsePayload returns the response payload that was set for the method.
func (m *GRPCServiceMethod) GetResponsePayload() GRPCResponsePayload {
	methodLock.RLock()
	defer methodLock.RUnlock()
	return m.ResponsePayload
}

// ProcessAndSetPayload registers the payload as the RPC response payload of the method on the port with the payload manager, and on success applies the payload and the stream delay to the method.
func (m *GRPCServiceMethod) ProcessAndSetPayload(port int, data any, matches []*payload.RequestMatch, capture *payload.RequestCapture, contentType, delay string, streamCount int) error {
	content := util.ToJSONBytes(data)
	isStream := m.IsServerStream || m.IsBidiStream
	delayMin, delayMax, delayCount, _ := types.ParseDurationRange(delay)
	methodURI := m.GetURI() + "*"
	msg := ""
	if err := payload.PayloadManager.SetRPCResponsePayload(port, isStream, content, contentType, methodURI, "", "", "", "", streamCount, delayMin, delayMax); err != nil {
		msg = fmt.Sprintf("Port [%d]: Failed to set gRPC payload for service [%s] method [%s], content-type [%s], length [%d], count [%d], delay [%s-%s], with error [%s]",
			port, m.Service.Name, m.Name, contentType, len(content), streamCount, delayMin, delayMax, err.Error())
		log.Println(msg)
		return err
	} else {
		m.SetStreamCount(1)
		m.SetStreamDelay(delayMin, delayMax, delayCount)
		m.SetResponsePayload(content)
		msg = fmt.Sprintf("Port [%d]: Set gRPC payload for service [%s] method [%s], content-type [%s], length [%d], count [%d], delay [%s-%s]",
			port, m.Service.Name, m.Name, contentType, len(content), streamCount, delayMin, delayMax)
		log.Println(msg)
	}
	return nil
}

// InputType returns the protobuf descriptor of the input message of the method, or nil.
func (m *GRPCServiceMethod) InputType() protoreflect.MessageDescriptor {
	if m.PMD != nil {
		return m.PMD.Input()
	}
	return nil
}

// OutputType returns the protobuf descriptor of the output message of the method, or nil.
func (m *GRPCServiceMethod) OutputType() protoreflect.MessageDescriptor {
	if m.PMD != nil {
		return m.PMD.Output()
	}
	return nil
}

// PayloadsToInputs converts JSON payloads into dynamic messages of the input type of the method.
func (m *GRPCServiceMethod) PayloadsToInputs(payloads [][]byte) (messages []proto.Message, err error) {
	return payloadsToMessages(payloads, m.PMD.Input())
}

// JSONsToInputs converts JSON values into dynamic messages of the input type of the method.
func (m *GRPCServiceMethod) JSONsToInputs(jsons []any) (messages []proto.Message, err error) {
	return jsonsToMessages(jsons, m.PMD.Input())
}

// PayloadsToOutputs converts JSON payloads into dynamic messages of the output type of the method.
func (m *GRPCServiceMethod) PayloadsToOutputs(payloads [][]byte) (messages []proto.Message, err error) {
	return payloadsToMessages(payloads, m.PMD.Output())
}

// JSONsToOutputs converts JSON values into dynamic messages of the output type of the method.
func (m *GRPCServiceMethod) JSONsToOutputs(jsons []any) (messages []proto.Message, err error) {
	return jsonsToMessages(jsons, m.PMD.Output())
}

// MarshalJSON encodes the payload bytes as a JSON string.
func (p GRPCResponsePayload) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(p))
}

// payloadsToMessages decodes each JSON payload into a dynamic message of the descriptor type.
func payloadsToMessages(payloads [][]byte, desc protoreflect.MessageDescriptor) (messages []proto.Message, err error) {
	for _, payload := range payloads {
		json := util.JSONFromBytes(payload)
		msg := dynamicpb.NewMessage(desc)
		if err = fillInput(msg, util.ToJSONBytes(json)); err != nil {
			return nil, err
		} else {
			log.Printf("payloadsToMessages: %+v, %+s\n", json, protojson.Format(msg))
			messages = append(messages, msg)
		}
	}
	return
}

// jsonsToMessages decodes each JSON value into a dynamic message of the descriptor type.
func jsonsToMessages(jsons []any, desc protoreflect.MessageDescriptor) (messages []proto.Message, err error) {
	for _, json := range jsons {
		msg := dynamicpb.NewMessage(desc)
		if err = fillInput(msg, util.ToJSONBytes(json)); err != nil {
			return nil, err
		} else {
			messages = append(messages, msg)
		}
	}
	return
}

// fillInput unmarshals the JSON payload into the dynamic message, and logs the failure if it can't.
func fillInput(input *dynamicpb.Message, payload []byte) error {
	if err := protojson.Unmarshal(payload, input); err != nil {
		log.Printf("GRPCServiceRegistry.fillInput: [ERROR] Failed to unmarshal payload into method input type [%s] with error: %s\n", input.Descriptor().FullName(), err.Error())
		return err
	}
	return nil
}

// LoadRemoteReflectedServices lists the services that a remote server exposes through reflection and registers each one whose descriptor can be fetched.
func LoadRemoteReflectedServices(conn *grpc.ClientConn) (err error) {
	client := grpcreflect.NewClientAuto(context.Background(), conn)
	services, err := client.ListServices()
	if err != nil {
		return err
	}
	loadedService := map[string]bool{}
	for _, s := range services {
		if _, ok := loadedService[string(s.Name())]; ok {
			continue
		}
		fd, err := client.FileContainingSymbol(s)
		if err != nil {
			continue
		}
		for i := 0; i < fd.Services().Len(); i++ {
			sd := fd.Services().Get(i)
			service := ServiceRegistry.NewGRPCService(sd)
			loadedService[service.Name] = true
		}
	}
	return
}

// LoadRemoteReflectedService fetches a service from a reflection client, checks that it has the method if one is given, and registers it.
func LoadRemoteReflectedService(client *grpcreflect.Client, serviceName, methodName string) (*GRPCService, error) {
	symbol := serviceName
	if methodName != "" {
		symbol += "." + methodName
	}
	fd, err := client.FileContainingSymbol(protoreflect.FullName(symbol))
	if err != nil {
		return nil, err
	}
	exists := false
	var sd protoreflect.ServiceDescriptor
	for i := 0; i < fd.Services().Len(); i++ {
		sd = fd.Services().Get(i)
		if string(sd.FullName()) == serviceName {
			exists = true
			break
		}
	}
	if !exists {
		return nil, fmt.Errorf("Service [%s] not found", serviceName)
	}
	if methodName != "" {
		exists = false
		for i := 0; i < sd.Methods().Len(); i++ {
			md := sd.Methods().Get(i)
			if string(md.Name()) == methodName {
				exists = true
				break
			}
		}
		if !exists {
			return nil, fmt.Errorf("Service [%s] Method [%s] not found", serviceName, methodName)
		}
	}
	service := ServiceRegistry.NewGRPCService(sd)
	return service, nil
}

// LoadRemoteReflectedServiceV1 loads a service from the connection using the v1 reflection API.
func LoadRemoteReflectedServiceV1(conn *grpc.ClientConn, serviceName, methodName string) (*GRPCService, error) {
	return LoadRemoteReflectedService(grpcreflect.NewClientV1(context.Background(), grpc_reflection_v1.NewServerReflectionClient(conn)), serviceName, methodName)
}

// LoadRemoteReflectedServiceV1Alpha loads a service from the connection using the v1alpha reflection API.
func LoadRemoteReflectedServiceV1Alpha(conn *grpc.ClientConn, serviceName, methodName string) (*GRPCService, error) {
	return LoadRemoteReflectedService(grpcreflect.NewClientV1Alpha(context.Background(), grpc_reflection_v1alpha.NewServerReflectionClient(conn)), serviceName, methodName)
}
