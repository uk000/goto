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
	"strings"
	"sync"
	"time"
)

type JSONRPCService struct {
	Name    string                    `json:"name"`
	Methods map[string]*JSONRPCMethod `json:"methods"`
}

type JSONRPCMethod struct {
	Name           string             `json:"name"`
	URI            string             `json:"uri"`
	IsBatch        bool               `json:"isBatch"`
	IsStreaming    bool               `json:"isStreaming"`
	In             func(util.JSON)    `json:"-"`
	Out            func() util.JSON   `json:"-"`
	StreamOut      func() []util.JSON `json:"-"`
	Service        *JSONRPCService    `json:"-"`
	StreamCount    int                `json:"-"`
	StreamDelayMin time.Duration      `json:"-"`
	StreamDelayMax time.Duration      `json:"-"`
}

type JSONRPCError struct {
	Code    int       `json:"code"`
	Message string    `json:"message"`
	Data    util.JSON `json:"data,omitempty"`
}

type JSONRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Method  string      `json:"method"`
	Params  util.JSON   `json:"params"`
}

type JSONRPCResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      interface{}   `json:"id"`
	Result  util.JSON     `json:"result,omitempty"`
	Error   *JSONRPCError `json:"error,omitempty"`
}

type JSONRPCServiceRegistry struct {
	Services map[string]*JSONRPCService `json:"services"`
	lock     sync.RWMutex
}

var (
	JSONRPCRegistry = &JSONRPCServiceRegistry{
		Services: map[string]*JSONRPCService{},
		lock:     sync.RWMutex{},
	}
)

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

func (jr *JSONRPCServiceRegistry) NewJSONRPCService(body io.ReadCloser) (*JSONRPCService, error) {
	jr.lock.Lock()
	defer jr.lock.Unlock()
	service := &JSONRPCService{
		Methods: map[string]*JSONRPCMethod{},
	}
	if err := util.ReadJsonPayloadFromBody(body, service); err != nil {
		return nil, err
	} else if service.Name == "" {
		return nil, fmt.Errorf("No name")
	} else {
		jr.Services[service.Name] = service
		for _, method := range service.Methods {
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
		Methods: map[string]*JSONRPCMethod{},
	}
	for _, method := range g.Methods {
		jsonMethod := &JSONRPCMethod{
			Name:           method.Name,
			URI:            method.URI,
			IsBatch:        method.IsClientStreaming,
			IsStreaming:    method.IsServerStreaming,
			In:             method.In,
			Out:            method.Out,
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

func (j *JSONRPCService) GetName() string {
	return j.Name
}

func (j *JSONRPCService) HasMethod(m string) bool {
	return j.Methods != nil && j.Methods[m] != nil
}

func (j *JSONRPCService) GetMethodCount() int {
	return len(j.Methods)
}

func (m *JSONRPCMethod) GetName() string {
	return m.Name
}

func (m *JSONRPCMethod) GetURI() string {
	return m.URI
}
