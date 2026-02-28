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

package rpc

import (
	"goto/pkg/server/hooks"
	"goto/pkg/server/request/tracking"
	"goto/pkg/util"
	"io"
	"strings"
	"sync"
)

type ServiceTracker struct {
	ServiceCallCounts       int            `json:"serviceCallCounts"`
	ServiceMethodCallCounts map[string]int `json:"serviceMethodCallCounts"`
	PortTracking            any            `json:"portTracking"`
}

type RPCTracker struct {
	ServiceTrackers map[string]*ServiceTracker `json:"serviceTrackers"`
	lock            sync.RWMutex
}

var (
	PortTracker = map[int]*RPCTracker{}
	lock        sync.RWMutex
)

func GetRPCTracker(port int) *RPCTracker {
	lock.Lock()
	defer lock.Unlock()
	if PortTracker[port] == nil {
		PortTracker[port] = &RPCTracker{
			ServiceTrackers: map[string]*ServiceTracker{},
			lock:            sync.RWMutex{},
		}
	}
	return PortTracker[port]
}

func (r *RPCTracker) Clear() {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.ServiceTrackers = map[string]*ServiceTracker{}
}

func (r *RPCTracker) TrackService(port int, s RPCService, headers []string, header, value string) {
	r.TrackServiceMethod(port, s, nil, headers, header, value)
}

func (r *RPCTracker) TrackServiceMethod(port int, s RPCService, m RPCMethod, headers []string, header, value string) {
	r.lock.Lock()
	defer r.lock.Unlock()
	serviceURI := s.GetURI()
	if r.ServiceTrackers[serviceURI] == nil {
		r.ServiceTrackers[serviceURI] = &ServiceTracker{ServiceMethodCallCounts: map[string]int{}}
	}
	var hookHeaders hooks.Headers
	if header != "" && value != "" {
		hookHeaders = hooks.Headers{[2]string{header, value}}
		headers = []string{header}
	} else {
		hookHeaders = util.TransformHeaders(headers)
	}
	serviceName := s.GetName()
	isGRPC := s.IsGRPC()
	track := func(id, uri string) {
		tracking.Tracker.Init(port, serviceName, uri, headers)
		hooks.GetPortHooks(port).AddHTTPHookWithListener(serviceName, id, uri+"/*", hookHeaders, !isGRPC, trackCallCounts(s))
	}
	track(s.GetName(), s.GetURI())
	if m != nil {
		track(m.GetName(), m.GetURI())
	} else {
		s.ForEachMethod(func(r RPCMethod) {
			track(r.GetName(), r.GetURI())
		})
	}
}

func (r *RPCTracker) IncrementServiceCount(s RPCService, requestURI string) {
	r.lock.Lock()
	defer r.lock.Unlock()
	serviceURI := s.GetURI()
	if r.ServiceTrackers[serviceURI] != nil {
		r.ServiceTrackers[serviceURI].ServiceCallCounts++
		s.ForEachMethod(func(method RPCMethod) {
			if strings.HasPrefix(requestURI, method.GetURI()) {
				r.ServiceTrackers[serviceURI].ServiceMethodCallCounts[method.GetURI()]++
			}
		})
	}
}

func (r *RPCTracker) GetServiceTrackerJSON(s RPCService) *ServiceTracker {
	r.lock.RLock()
	defer r.lock.RUnlock()
	return r.ServiceTrackers[s.GetURI()]
}

func trackCallCounts(s RPCService) func(port int, uri string, requestHeaders map[string][]string, body io.Reader) bool {
	return func(port int, uri string, requestHeaders map[string][]string, body io.Reader) bool {
		uri = strings.ToLower(uri)
		if strings.HasPrefix(uri, s.GetURI()) {
			GetRPCTracker(port).IncrementServiceCount(s, uri)
		}
		return true
	}
}
