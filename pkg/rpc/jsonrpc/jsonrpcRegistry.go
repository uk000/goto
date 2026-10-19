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

package jsonrpc

import (
	"encoding/json"
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
	lock             sync.RWMutex
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
	rpc.GetServiceRegistry["jsonrpc"] = func(port int) rpc.RPCServiceRegistry { return GetPortRegistry(port) }
}

func GetJSONRPCRegistry(r *http.Request) *JSONRPCServiceRegistry {
	return GetPortRegistry(util.GetRequestOrListenerPortNum(r))
}

// GetPortRegistry returns the registry of the port, which is created when the port is first asked for.
func GetPortRegistry(port int) *JSONRPCServiceRegistry {
	lock.Lock()
	defer lock.Unlock()
	if PortRegistry[port] == nil {
		PortRegistry[port] = &JSONRPCServiceRegistry{
			Services: map[string]*JSONRPCService{},
			lock:     sync.RWMutex{},
		}
	}
	return PortRegistry[port]
}

// MarshalJSON encodes the services with the lock of the registry held, since the services are added and removed by the
// APIs while the registry is being listed.
func (jr *JSONRPCServiceRegistry) MarshalJSON() ([]byte, error) {
	jr.lock.RLock()
	defer jr.lock.RUnlock()
	return json.Marshal(struct {
		Services map[string]*JSONRPCService `json:"services"`
	}{jr.Services})
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
	if s := jr.GetService(name); s != nil {
		return s
	}
	return nil
}

func (jr *JSONRPCServiceRegistry) RemoveService(name string) {
	jr.lock.Lock()
	defer jr.lock.Unlock()
	delete(jr.Services, name)
}

func (jr *JSONRPCServiceRegistry) TrackService(port int, name string, headers []string, header, value string) {
	if service := jr.GetService(name); service != nil {
		rpc.GetRPCTracker(port).TrackService(port, service, headers, header, value)
	}
}

func (jr *JSONRPCServiceRegistry) GetServiceTracker(port int, name string) *rpc.ServiceTracker {
	if service := jr.GetService(name); service != nil {
		return rpc.GetRPCTracker(port).GetServiceTrackerJSON(service)
	}
	return nil
}

// NewJSONRPCService adds the service that is described by the body: its name, and optionally its methods by name, e.g.
// {"name": "calc", "methods": {"add": {"name": "add", "isBatch": false}, "sub": {}}}. The URIs of the service and of its
// methods are derived from their names. The name of a method defaults to its key.
func (jr *JSONRPCServiceRegistry) NewJSONRPCService(body io.ReadCloser) (*JSONRPCService, error) {
	service := &JSONRPCService{}
	if err := util.ReadJsonPayloadFromBody(body, service); err != nil {
		return nil, err
	} else if service.Name == "" {
		return nil, fmt.Errorf("No name")
	}
	service.URI = strings.ToLower(fmt.Sprintf("/%s", service.Name))
	for _, m := range service.Methods {
		method := m.(*JSONRPCMethod)
		method.Service = service
		method.URI = strings.ToLower(fmt.Sprintf("/%s/%s", service.Name, method.Name))
	}
	jr.lock.Lock()
	defer jr.lock.Unlock()
	jr.Services[service.Name] = service
	return service, nil
}

func (jr *JSONRPCServiceRegistry) FromGRPCService(name string) (*JSONRPCService, error) {
	g := grpc.ServiceRegistry.GetService(name)
	if g == nil {
		return nil, fmt.Errorf("No GRPCService found for name: %s", name)
	}
	service := &JSONRPCService{
		Name:    g.Name,
		URI:     g.URI,
		Methods: map[string]rpc.RPCMethod{},
	}
	for _, method := range g.Methods {
		streamCount, delayMin, delayMax, delayCount := method.GetStreamConfig()
		jsonMethod := &JSONRPCMethod{
			Name:             method.Name,
			URI:              method.URI,
			IsBatch:          method.IsClientStream,
			IsStreaming:      method.IsServerStream,
			StreamOut:        method.StreamOut,
			StreamCount:      streamCount,
			StreamDelayMin:   delayMin,
			StreamDelayMax:   delayMax,
			StreamDelayCount: delayCount,
			Service:          service,
		}
		service.Methods[method.Name] = jsonMethod
	}
	jr.lock.Lock()
	defer jr.lock.Unlock()
	jr.Services[service.Name] = service
	return service, nil
}

// UnmarshalJSON reads the methods of a service as JSONRPCMethod, which can't be done by the default decoding, since
// the methods are held by their interface.
func (s *JSONRPCService) UnmarshalJSON(b []byte) error {
	var data struct {
		Name    string                    `json:"name"`
		URI     string                    `json:"uri"`
		Methods map[string]*JSONRPCMethod `json:"methods"`
	}
	if err := json.Unmarshal(b, &data); err != nil {
		return err
	}
	s.Name = data.Name
	s.URI = data.URI
	s.Methods = make(map[string]rpc.RPCMethod, len(data.Methods))
	for key, method := range data.Methods {
		if method == nil {
			method = &JSONRPCMethod{}
		}
		if method.Name == "" {
			method.Name = key
		} else if method.Name != key {
			return fmt.Errorf("Method [%s] is named [%s]", key, method.Name)
		}
		s.Methods[key] = method
	}
	return nil
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
	m.lock.Lock()
	defer m.lock.Unlock()
	m.StreamCount = count
}

func (m *JSONRPCMethod) SetStreamDelay(min, max time.Duration, count int) {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.StreamDelayMin = min
	m.StreamDelayMax = max
	m.StreamDelayCount = count
}

func (m *JSONRPCMethod) SetResponsePayload(payload []byte) {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.ResponsePayload = string(payload)
}

// GetStreamConfig returns the stream settings of the method: the number of responses, and the delay between them.
func (m *JSONRPCMethod) GetStreamConfig() (count int, delayMin, delayMax time.Duration, delayCount int) {
	m.lock.RLock()
	defer m.lock.RUnlock()
	return m.StreamCount, m.StreamDelayMin, m.StreamDelayMax, m.StreamDelayCount
}

// GetResponsePayload returns the response payload that was set for the method.
func (m *JSONRPCMethod) GetResponsePayload() string {
	m.lock.RLock()
	defer m.lock.RUnlock()
	return m.ResponsePayload
}

// MarshalJSON encodes the method with its lock held, as the APIs list the services while they configure the methods.
func (m *JSONRPCMethod) MarshalJSON() ([]byte, error) {
	m.lock.RLock()
	defer m.lock.RUnlock()
	return json.Marshal(struct {
		Name             string        `json:"name"`
		URI              string        `json:"uri"`
		IsBatch          bool          `json:"isBatch"`
		IsStreaming      bool          `json:"isStreaming"`
		ResponsePayload  string        `json:"responsePayload"`
		StreamCount      int           `json:"streamCount"`
		StreamDelayMin   time.Duration `json:"streamDelayMin"`
		StreamDelayMax   time.Duration `json:"streamDelayMax"`
		StreamDelayCount int           `json:"streamDelayCount"`
	}{m.Name, m.URI, m.IsBatch, m.IsStreaming, m.ResponsePayload, m.StreamCount, m.StreamDelayMin, m.StreamDelayMax, m.StreamDelayCount})
}
