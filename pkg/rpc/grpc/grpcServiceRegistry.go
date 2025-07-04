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
	"fmt"
	"goto/pkg/rpc"
	"goto/pkg/util"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type GRPCService struct {
	Name    string                         `json:"name"`
	Methods map[string]*GRPCServiceMethod  `json:"methods"`
	GSD     *grpc.ServiceDesc              `json:"-"`
	PSD     protoreflect.ServiceDescriptor `json:"-"`
}

type GRPCServiceMethod struct {
	Name              string                        `json:"name"`
	URI               string                        `json:"uri"`
	IsUnary           bool                          `json:"isUnary"`
	IsClientStreaming bool                          `json:"isClientStreaming"`
	IsServerStreaming bool                          `json:"isServerStreaming"`
	In                func(util.JSON)               `json:"-"`
	Out               func() util.JSON              `json:"-"`
	StreamOut         func() []util.JSON            `json:"-"`
	Service           *GRPCService                  `json:"-"`
	GMD               *grpc.MethodDesc              `json:"-"`
	GSD               *grpc.StreamDesc              `json:"-"`
	PMD               protoreflect.MethodDescriptor `json:"-"`
	StreamCount       int                           `json:"-"`
	StreamDelayMin    time.Duration                 `json:"-"`
	StreamDelayMax    time.Duration                 `json:"-"`
}

type GRPCServiceRegistry struct {
	Services map[string]*GRPCService
	lock     sync.RWMutex
}

type DynamicServiceInterface interface{}
type DynamicServiceStub struct{}

var _ DynamicServiceInterface = (*DynamicServiceStub)(nil)

var (
	ServiceRegistry   = &GRPCServiceRegistry{lock: sync.RWMutex{}}
	GRPCUnaryHandler  func(*GRPCServiceMethod) grpc.MethodHandler
	GRPCStreamHandler func(interface{}, grpc.ServerStream) error
)

func (gsr *GRPCServiceRegistry) Init() {
	gsr.lock.Lock()
	defer gsr.lock.Unlock()
	gsr.Services = map[string]*GRPCService{}
}

func (gsr *GRPCServiceRegistry) GetService(name string) *GRPCService {
	gsr.lock.RLock()
	defer gsr.lock.RUnlock()
	return gsr.Services[name]
}

func (gsr *GRPCServiceRegistry) GetRPCService(name string) rpc.RPCService {
	return gsr.GetService(name)
}

func (gsr *GRPCServiceRegistry) RemoveService(name string) {
	gsr.lock.Lock()
	defer gsr.lock.Unlock()
	delete(gsr.Services, name)
}

func (gsr *GRPCServiceRegistry) NewGRPCService(psd protoreflect.ServiceDescriptor) *GRPCService {
	gsd := &grpc.ServiceDesc{
		ServiceName: string(psd.FullName()),
		HandlerType: (*DynamicServiceInterface)(nil),
		Methods:     []grpc.MethodDesc{},
		Streams:     []grpc.StreamDesc{},
	}
	service := GRPCService{
		Name:    gsd.ServiceName,
		Methods: make(map[string]*GRPCServiceMethod),
		GSD:     gsd,
		PSD:     psd,
	}
	for i := 0; i < psd.Methods().Len(); i++ {
		pmd := psd.Methods().Get(i)
		methodName := string(pmd.Name())
		method := &GRPCServiceMethod{
			Name:              methodName,
			URI:               strings.ToLower(fmt.Sprintf("/%s/%s", gsd.ServiceName, methodName)),
			PMD:               pmd,
			IsClientStreaming: pmd.IsStreamingClient(),
			IsServerStreaming: pmd.IsStreamingServer(),
			IsUnary:           !pmd.IsStreamingClient() && !pmd.IsStreamingServer(),
			Service:           &service,
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

func (g *GRPCService) GetName() string {
	return g.Name
}

func (g *GRPCService) HasMethod(m string) bool {
	return g.Methods != nil && g.Methods[m] != nil
}

func (g *GRPCService) GetMethodCount() int {
	return len(g.Methods)
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

func (m *GRPCServiceMethod) SetStreamDelayMin(delay time.Duration) {
	m.StreamDelayMin = delay
}

func (m *GRPCServiceMethod) SetStreamDelayMax(delay time.Duration) {
	m.StreamDelayMax = delay
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
