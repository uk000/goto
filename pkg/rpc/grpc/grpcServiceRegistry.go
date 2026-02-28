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

package grpc

import (
	"context"
	"encoding/json"
	"fmt"
	"goto/pkg/global"
	"goto/pkg/rpc"
	"goto/pkg/types"
	"goto/pkg/util"
	"log"
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
	ServiceRegistry   = &GRPCServiceRegistry{lock: sync.RWMutex{}}
	GRPCUnaryHandler  func(*GRPCServiceMethod) grpc.MethodHandler
	GRPCStreamHandler func(interface{}, grpc.ServerStream) error
	ProxyGRPCUnary    ProxyGRPCUnaryFunc
	ProxyGRPCStream   ProxyGRPCStreamFunc
)

func init() {
	ServiceRegistry.Init()
	rpc.GetServiceRegistry["grpc"] = func(_ int) rpc.RPCServiceRegistry { return ServiceRegistry }
}

func (gsr *GRPCServiceRegistry) Init() {
	gsr.lock.Lock()
	defer gsr.lock.Unlock()
	gsr.Services = map[string]*GRPCService{}
	gsr.ActiveServices = map[string]*GRPCService{}
	gsr.ProxyServices = map[string]*types.Triple[*GRPCService, *GRPCService, int]{}
	gsr.TeeServices = map[int]*types.Pair[*GRPCService, *GRPCService]{}
}

func (gsr *GRPCServiceRegistry) GetService(name string) *GRPCService {
	gsr.lock.RLock()
	defer gsr.lock.RUnlock()
	return gsr.Services[name]
}

func (gsr *GRPCServiceRegistry) GetOrCreateService(gsd *grpc.ServiceDesc) *GRPCService {
	service := gsr.GetService(gsd.ServiceName)
	if service == nil {
		psd, _ := grpcreflect.LoadServiceDescriptor(gsd)
		service = gsr.NewGRPCServiceFromDescriptors(gsd, psd)
	}
	return service
}

func (gsr *GRPCServiceRegistry) GetActiveService(name string) *GRPCService {
	gsr.lock.RLock()
	defer gsr.lock.RUnlock()
	return gsr.ActiveServices[name]
}

func (gsr *GRPCServiceRegistry) GetProxyService(name string) (*GRPCService, *GRPCService, int) {
	gsr.lock.RLock()
	defer gsr.lock.RUnlock()
	triple := gsr.ProxyServices[name]
	if triple != nil {
		return triple.First, triple.Second, triple.Third
	}
	return nil, nil, 0
}

func (gsr *GRPCServiceRegistry) GetTeeServices(port int) (*GRPCService, *GRPCService) {
	gsr.lock.RLock()
	defer gsr.lock.RUnlock()
	pair := gsr.TeeServices[port]
	if pair != nil {
		return pair.Left, pair.Right
	}
	return nil, nil
}

func (gsr *GRPCServiceRegistry) AddActiveService(s *GRPCService) {
	gsr.lock.Lock()
	defer gsr.lock.Unlock()
	gsr.ActiveServices[s.Name] = s
}

func (gsr *GRPCServiceRegistry) AddProxyService(from, to *GRPCService, teeport int) {
	gsr.lock.Lock()
	defer gsr.lock.Unlock()
	gsr.ProxyServices[from.Name] = types.NewTriple(from, to, teeport)
	if teeport > 0 {
		gsr.TeeServices[teeport] = types.NewPair(from, to)
	}
}

func (gsr *GRPCServiceRegistry) RemoveActiveService(s *GRPCService) {
	gsr.lock.Lock()
	defer gsr.lock.Unlock()
	delete(gsr.ActiveServices, s.Name)
}

func (gsr *GRPCServiceRegistry) RemoveProxyService(s *GRPCService) {
	gsr.lock.Lock()
	defer gsr.lock.Unlock()
	delete(gsr.ProxyServices, s.Name)
}

func (gsr *GRPCServiceRegistry) RemoveTeeService(port int) {
	gsr.lock.Lock()
	defer gsr.lock.Unlock()
	delete(gsr.TeeServices, port)
}

func (gsr *GRPCServiceRegistry) GetRPCService(name string) rpc.RPCService {
	return gsr.GetService(name)
}

func (gsr *GRPCServiceRegistry) RemoveService(name string) {
	gsr.lock.Lock()
	defer gsr.lock.Unlock()
	delete(gsr.Services, name)
}

func (gsr *GRPCServiceRegistry) TrackService(port int, name string, headers []string, header, value string) {
	gsr.lock.Lock()
	defer gsr.lock.Unlock()
	if gsr.Services[name] != nil {
		rpc.GetRPCTracker(port).TrackService(port, gsr.Services[name], headers, header, value)
	}
}

func (gsr *GRPCServiceRegistry) NewGRPCServiceFromSD(sd *grpc.ServiceDesc) *GRPCService {
	psd, err := grpcreflect.LoadServiceDescriptor(sd)
	if err != nil || psd == nil {
		log.Printf("Error loading service descriptor: %v", err)
		return nil
	}
	return gsr.NewGRPCService(psd)
}

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

func (gsr *GRPCServiceRegistry) ConvertToTargetService(gsd *grpc.ServiceDesc, target string) (*grpc.ServiceDesc, protoreflect.ServiceDescriptor, error) {
	gsd2 := *gsd
	gsd2.ServiceName = target
	psd2, err := grpcreflect.LoadServiceDescriptor(&gsd2)
	if err != nil {
		return nil, nil, err
	}
	return &gsd2, psd2, nil
}

func (gsr *GRPCServiceRegistry) CloneService(service *GRPCService, target string) (targetService *GRPCService, err error) {
	targetService = gsr.GetService(target)
	if targetService != nil {
		return
	}
	s2 := *service
	s2.Name = target
	s2.URI = strings.ToLower(fmt.Sprintf("/%s", target))
	s2.Methods = make(map[string]*GRPCServiceMethod)
	gsd2 := *service.GSD
	gsd2.ServiceName = target
	for i, gsdm := range gsd2.Methods {
		m := service.Methods[gsdm.MethodName]
		m2 := *m
		m2.Service = &s2
		m2.URI = strings.ToLower(fmt.Sprintf("/%s/%s", target, m.Name))
		gsd2.Methods[i].Handler = GRPCUnaryHandler(&m2)
		s2.Methods[m2.Name] = &m2
	}
	for i, gsds := range gsd2.Streams {
		m := service.Methods[gsds.StreamName]
		m2 := *m
		m2.Service = &s2
		m2.URI = strings.ToLower(fmt.Sprintf("/%s/%s", target, m.Name))
		gsd2.Streams[i].Handler = GRPCStreamHandler
		s2.Methods[m2.Name] = &m2
	}
	var psd2 protoreflect.ServiceDescriptor
	if psd2, err = grpcreflect.LoadServiceDescriptor(&gsd2); err != nil {
		return
	}
	s2.GSD = &gsd2
	s2.PSD = psd2
	targetService = &s2
	return
}

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

func (s *GRPCService) IsGRPC() bool {
	return true
}

func (s *GRPCService) IsJSONRPC() bool {
	return false
}

func (s *GRPCService) GetName() string {
	return s.Name
}

func (s *GRPCService) GetURI() string {
	return s.URI
}

func (s *GRPCService) HasMethod(m string) bool {
	return s.Methods != nil && s.Methods[m] != nil
}

func (s *GRPCService) GetMethodCount() int {
	return len(s.Methods)
}

func (s *GRPCService) ForEachMethod(f func(rpc.RPCMethod)) {
	for _, m := range s.Methods {
		f(m)
	}
}

func (s *GRPCService) GetMethod(name string) rpc.RPCMethod {
	return s.Methods[name]
}

func (m *GRPCServiceMethod) GetName() string {
	return m.Name
}

func (m *GRPCServiceMethod) GetURI() string {
	return m.URI
}

func (m *GRPCServiceMethod) SetStreamCount(count int) {
	m.StreamCount = count
}

func (m *GRPCServiceMethod) SetStreamDelay(min, max time.Duration, count int) {
	m.StreamDelayMin = min
	m.StreamDelayMax = max
	m.StreamDelayCount = count
}

func (m *GRPCServiceMethod) SetResponsePayload(payload []byte) {
	m.ResponsePayload = payload
}

func (m *GRPCServiceMethod) InputType() protoreflect.MessageDescriptor {
	if m.PMD != nil {
		return m.PMD.Input()
	}
	return nil
}

func (m *GRPCServiceMethod) OutputType() protoreflect.MessageDescriptor {
	if m.PMD != nil {
		return m.PMD.Output()
	}
	return nil
}

func (m *GRPCServiceMethod) PayloadsToInputs(payloads [][]byte) (messages []proto.Message, err error) {
	return payloadsToMessages(payloads, m.PMD.Input())
}

func (m *GRPCServiceMethod) JSONsToInputs(jsons []any) (messages []proto.Message, err error) {
	return jsonsToMessages(jsons, m.PMD.Input())
}

func (m *GRPCServiceMethod) PayloadsToOutputs(payloads [][]byte) (messages []proto.Message, err error) {
	return payloadsToMessages(payloads, m.PMD.Output())
}

func (m *GRPCServiceMethod) JSONsToOutputs(jsons []any) (messages []proto.Message, err error) {
	return jsonsToMessages(jsons, m.PMD.Output())
}

func (p GRPCResponsePayload) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(p))
}

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

func fillInput(input *dynamicpb.Message, payload []byte) error {
	if err := protojson.Unmarshal(payload, input); err != nil {
		log.Printf("GRPCServiceRegistry.fillInput: [ERROR] Failed to unmarshal payload into method input type [%s] with error: %s\n", input.Descriptor().FullName(), err.Error())
		return err
	}
	return nil
}

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

func LoadRemoteReflectedServiceV1(conn *grpc.ClientConn, serviceName, methodName string) (*GRPCService, error) {
	return LoadRemoteReflectedService(grpcreflect.NewClientV1(context.Background(), grpc_reflection_v1.NewServerReflectionClient(conn)), serviceName, methodName)
}

func LoadRemoteReflectedServiceV1Alpha(conn *grpc.ClientConn, serviceName, methodName string) (*GRPCService, error) {
	return LoadRemoteReflectedService(grpcreflect.NewClientV1Alpha(context.Background(), grpc_reflection_v1alpha.NewServerReflectionClient(conn)), serviceName, methodName)
}
