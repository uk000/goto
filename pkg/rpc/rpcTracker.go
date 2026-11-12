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

package rpc

import (
	"encoding/json"
	"fmt"
	"goto/pkg/server/hooks"
	"goto/pkg/server/request/tracking"
	"io"
	"net/http"
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

// GetRPCTracker returns the tracker of the port, creating it on first use.
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

// Clear drops all service trackers.
func (r *RPCTracker) Clear() {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.ServiceTrackers = map[string]*ServiceTracker{}
}

// TrackService tracks calls to a service and all of its methods.
func (r *RPCTracker) TrackService(port int, s RPCService, headers []string, header, value string) {
	r.TrackServiceMethod(port, s, nil, headers, header, value)
}

// TrackServiceMethod registers request tracking and call-counting hooks for a service and one method (or all methods when nil).
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
		hookHeaders = anyValueHeaders(headers)
	}
	// The hooks match header names without regard to case, but the headers of the matched requests are reported to the
	// tracking by the names that the requests carry, which are canonical. The tracking only counts the headers that
	// it was initialized with by those names.
	headers = canonicalHeaderNames(headers)
	serviceName := s.GetName()
	isGRPC := s.IsGRPC()
	track := func(id, uri string) {
		tracking.Tracker.Init(port, serviceName, uri, headers)
		hooks.GetPortHooks(port).AddHTTPHookWithListener(serviceName, id, uri+"/*", hookHeaders, !isGRPC, trackCallCounts(s))
	}
	// A hook is identified by its id, and ids must not repeat: a method named like another service, or the methods
	// of different services that have the same name, would replace each other's hooks otherwise.
	track(serviceName, s.GetURI())
	if m != nil {
		track(methodHookID(serviceName, m), m.GetURI())
	} else {
		s.ForEachMethod(func(method RPCMethod) {
			track(methodHookID(serviceName, method), method.GetURI())
		})
	}
}

// anyValueHeaders gives the headers to a hook so that it matches them whatever their value is. A hook doesn't match a
// header that is given without a value (the empty value matches nothing), but the glob matches any value.
func anyValueHeaders(headers []string) hooks.Headers {
	hookHeaders := make(hooks.Headers, 0, len(headers))
	for _, h := range headers {
		hookHeaders = append(hookHeaders, [2]string{h, "*"})
	}
	return hookHeaders
}

// methodHookID builds a hook id unique to the method within its service.
func methodHookID(serviceName string, m RPCMethod) string {
	return fmt.Sprintf("%s:%s", serviceName, m.GetURI())
}

// canonicalHeaderNames converts header names to their canonical HTTP form.
func canonicalHeaderNames(headers []string) []string {
	if len(headers) == 0 {
		return headers
	}
	names := make([]string, len(headers))
	for i, h := range headers {
		names[i] = http.CanonicalHeaderKey(h)
	}
	return names
}

// IncrementServiceCount counts a call to a tracked service and to the methods that the request URI falls under.
func (r *RPCTracker) IncrementServiceCount(s RPCService, requestURI string) {
	r.lock.Lock()
	defer r.lock.Unlock()
	requestURI = strings.ToLower(requestURI)
	serviceURI := s.GetURI()
	if r.ServiceTrackers[serviceURI] != nil {
		r.ServiceTrackers[serviceURI].ServiceCallCounts++
		s.ForEachMethod(func(method RPCMethod) {
			if hasURIPrefix(requestURI, method.GetURI()) {
				r.ServiceTrackers[serviceURI].ServiceMethodCallCounts[method.GetURI()]++
			}
		})
	}
}

// GetServiceTrackerJSON returns a snapshot of the tracker of the service, or nil if the service isn't tracked. The
// snapshot is the caller's to keep and to change, it is not affected by calls that are counted later.
func (r *RPCTracker) GetServiceTrackerJSON(s RPCService) *ServiceTracker {
	r.lock.RLock()
	defer r.lock.RUnlock()
	st := r.ServiceTrackers[s.GetURI()]
	if st == nil {
		return nil
	}
	snapshot := &ServiceTracker{
		ServiceCallCounts:       st.ServiceCallCounts,
		ServiceMethodCallCounts: make(map[string]int, len(st.ServiceMethodCallCounts)),
		PortTracking:            st.PortTracking,
	}
	for uri, count := range st.ServiceMethodCallCounts {
		snapshot.ServiceMethodCallCounts[uri] = count
	}
	return snapshot
}

// portTrackingOf returns the request tracking of a service, which holds the tracking data by port. The tracking is only
// read through its JSON encoding, which holds the locks of the tracking: its maps change as requests are tracked.
func portTrackingOf(key string) any {
	b, err := json.Marshal(tracking.Tracker)
	if err != nil {
		return nil
	}
	var all struct {
		ByPort map[string]json.RawMessage `json:"byPort"`
	}
	if err := json.Unmarshal(b, &all); err != nil {
		return nil
	}
	if data, found := all.ByPort[key]; found {
		return data
	}
	return nil
}

// hasURIPrefix tells if a request URI is the given URI, or one below it: the URI is followed by a path separator, or
// a query (or is the end of the request URI). A plain string prefix would take /svc/m1 for a URI below /svc/m.
func hasURIPrefix(requestURI, uri string) bool {
	if !strings.HasPrefix(requestURI, uri) {
		return false
	}
	if len(requestURI) == len(uri) || strings.HasSuffix(uri, "/") {
		return true
	}
	switch requestURI[len(uri)] {
	case '/', '?', '#':
		return true
	}
	return false
}

// trackCallCounts returns a hook listener that counts each matching request against the service.
func trackCallCounts(s RPCService) func(port int, uri string, requestHeaders map[string][]string, body io.Reader) bool {
	return func(port int, uri string, requestHeaders map[string][]string, body io.Reader) bool {
		uri = strings.ToLower(uri)
		if hasURIPrefix(uri, s.GetURI()) {
			GetRPCTracker(port).IncrementServiceCount(s, uri)
		}
		return true
	}
}
