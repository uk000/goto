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

package jsonrpc

import (
	"fmt"
	"goto/pkg/rpc"
	"goto/pkg/rpc/grpc"
	"goto/pkg/util"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type JSONRPCService struct {
	Name    string                   `json:"name"`
	URI     string                   `json:"uri"`
	Methods map[string]rpc.RPCMethod `json:"methods"`
}

type JSONRPCMethod struct {
	Name             string             `json:"name"`
	URI              string             `json:"uri"`
	IsBatch          bool               `json:"isBatch"`
	IsStreaming      bool               `json:"isStreaming"`
	ResponsePayload  string             `json:"responsePayload"`
	StreamCount      int                `json:"streamCount"`
	StreamDelayMin   time.Duration      `json:"streamDelayMin"`
	StreamDelayMax   time.Duration      `json:"streamDelayMax"`
	StreamDelayCount int                `json:"streamDelayCount"`
	StreamOut        func() []util.JSON `json:"-"`
	Service          *JSONRPCService    `json:"-"`
}

type JSONRPCServiceRegistry struct {
	Services map[string]*JSONRPCService `json:"services"`
	lock     sync.RWMutex
}

var (
	PortRegistry = map[int]*JSONRPCServiceRegistry{}
	lock         sync.RWMutex
)

func init() {
	rpc.GetServiceRegistry["jsonrpc"] = func(port int) rpc.RPCServiceRegistry { return PortRegistry[port] }
}

func GetJSONRPCRegistry(r *http.Request) *JSONRPCServiceRegistry {
	lock.Lock()
	defer lock.Unlock()
	port := util.GetRequestOrListenerPortNum(r)
	if PortRegistry[port] == nil {
		PortRegistry[port] = &JSONRPCServiceRegistry{
			Services: map[string]*JSONRPCService{},
			lock:     sync.RWMutex{},
		}
	}
	return PortRegistry[port]
}

func (jr *JSONRPCServiceRegistry) Init() {
	jr.lock.Lock()
	defer jr.lock.Unlock()
	jr.Services = map[string]*JSONRPCService{}
}

func (jr *JSONRPCServiceRegistry) GetService(name string) *JSONRPCService {
	jr.lock.RLock()
	defer jr.lock.RUnlock()
	return jr.Services[name]
}

func (jr *JSONRPCServiceRegistry) GetRPCService(name string) rpc.RPCService {
	return jr.GetService(name)
}

func (jr *JSONRPCServiceRegistry) RemoveService(name string) {
	jr.lock.Lock()
	defer jr.lock.Unlock()
	delete(jr.Services, name)
}

func (jr *JSONRPCServiceRegistry) TrackService(port int, name string, headers []string, header, value string) {
	jr.lock.Lock()
	defer jr.lock.Unlock()
	if jr.Services[name] != nil {
		rpc.GetRPCTracker(port).TrackService(port, jr.Services[name], headers, header, value)
	}
}

func (jr *JSONRPCServiceRegistry) GetServiceTracker(port int, name string) *rpc.ServiceTracker {
	jr.lock.RLock()
	defer jr.lock.RUnlock()
	if jr.Services[name] != nil {
		return rpc.GetRPCTracker(port).GetServiceTrackerJSON(jr.Services[name])
	}
	return nil
}

func (jr *JSONRPCServiceRegistry) NewJSONRPCService(body io.ReadCloser) (*JSONRPCService, error) {
	jr.lock.Lock()
	defer jr.lock.Unlock()
	service := &JSONRPCService{
		Methods: map[string]rpc.RPCMethod{},
	}
	if err := util.ReadJsonPayloadFromBody(body, service); err != nil {
		return nil, err
	} else if service.Name == "" {
		return nil, fmt.Errorf("No name")
	} else {
		service.URI = strings.ToLower(fmt.Sprintf("/%s", service.Name))
		jr.Services[service.Name] = service
		for _, m := range service.Methods {
			method := m.(*JSONRPCMethod)
			method.Service = service
			method.URI = strings.ToLower(fmt.Sprintf("/%s/%s", service.Name, method.Name))
		}
	}
	return service, nil
}

func (jr *JSONRPCServiceRegistry) FromGRPCService(name string) (*JSONRPCService, error) {
	g := grpc.ServiceRegistry.GetService(name)
	if g == nil {
		return nil, fmt.Errorf("No GRPCService found for name: %s\n", name)
	}
	jr.lock.Lock()
	defer jr.lock.Unlock()
	service := &JSONRPCService{
		Name:    g.Name,
		URI:     g.URI,
		Methods: map[string]rpc.RPCMethod{},
	}
	for _, method := range g.Methods {
		jsonMethod := &JSONRPCMethod{
			Name:           method.Name,
			URI:            method.URI,
			IsBatch:        method.IsClientStream,
			IsStreaming:    method.IsServerStream,
			StreamOut:      method.StreamOut,
			StreamCount:    method.StreamCount,
			StreamDelayMin: method.StreamDelayMin,
			StreamDelayMax: method.StreamDelayMax,
			Service:        service,
		}
		service.Methods[method.Name] = jsonMethod
	}
	jr.Services[service.Name] = service
	return service, nil
}

func (s *JSONRPCService) IsGRPC() bool {
	return false
}

func (s *JSONRPCService) IsJSONRPC() bool {
	return true
}

func (s *JSONRPCService) GetName() string {
	return s.Name
}

func (s *JSONRPCService) GetURI() string {
	return s.URI
}

func (s *JSONRPCService) HasMethod(m string) bool {
	return s.Methods != nil && s.Methods[m] != nil
}

func (s *JSONRPCService) GetMethodCount() int {
	return len(s.Methods)
}

func (s *JSONRPCService) ForEachMethod(f func(rpc.RPCMethod)) {
	for _, m := range s.Methods {
		f(m)
	}
}

func (s *JSONRPCService) GetMethod(name string) rpc.RPCMethod {
	return s.Methods[name]
}

func (m *JSONRPCMethod) GetName() string {
	return m.Name
}

func (m *JSONRPCMethod) GetURI() string {
	return m.URI
}

func (m *JSONRPCMethod) SetStreamCount(count int) {
	m.StreamCount = count
}

func (m *JSONRPCMethod) SetStreamDelay(min, max time.Duration, count int) {
	m.StreamDelayMin = min
	m.StreamDelayMax = max
	m.StreamDelayCount = count
}

func (m *JSONRPCMethod) SetResponsePayload(payload []byte) {
	m.ResponsePayload = string(payload)
}
