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

package httpproxy

import (
	"maps"
	"strconv"
	"sync"
)

type HTTPCounts struct {
	DownstreamRequestCount                int                       `json:"downstreamRequestCount"`
	UpstreamRequestCount                  int                       `json:"upstreamRequestCount"`
	RequestDropCount                      int                       `json:"requestDropCount"`
	ResponseDropCount                     int                       `json:"responseDropCount"`
	DownstreamRequestCountsByURI          map[string]int            `json:"downstreamRequestCountsByURI"`
	UpstreamRequestCountsByURI            map[string]int            `json:"upstreamRequestCountsByURI"`
	UpstreamRequestCountsByEndpoint       map[string]int            `json:"upstreamRequestCountsByEndpoint"`
	UpstreamRequestCountByStatus          map[string]int            `json:"upstreamRequestCountByStatus"`
	UpstreamRequestCountsByURIStatus      map[string]map[string]int `json:"upstreamRequestCountsByURIStatus"`
	UpstreamRequestCountsByEndpointStatus map[string]map[string]int `json:"upstreamRequestCountsByEndpointStatus"`
	RequestDropCountsByURI                map[string]int            `json:"requestDropCountsByURI"`
	ResponseDropCountsByURI               map[string]int            `json:"responseDropCountsByURI"`
	URIMatchCounts                        map[string]int            `json:"uriMatchCounts"`
	HeaderMatchCounts                     map[string]int            `json:"headerMatchCounts"`
	HeaderValueMatchCounts                map[string]map[string]int `json:"headerValueMatchCounts"`
	QueryMatchCounts                      map[string]int            `json:"queryMatchCounts"`
	QueryValueMatchCounts                 map[string]map[string]int `json:"queryValueMatchCounts"`
	lock                                  sync.RWMutex
}

type HTTPTargetTracker struct {
	*HTTPCounts
}

type HTTPProxyTracker struct {
	*HTTPCounts
	TargetTrackers map[string]*HTTPTargetTracker `json:"targetTrackers"`
}

func NewHTTPCounts() *HTTPCounts {
	return &HTTPCounts{
		DownstreamRequestCountsByURI:          map[string]int{},
		UpstreamRequestCountsByURI:            map[string]int{},
		UpstreamRequestCountsByEndpoint:       map[string]int{},
		UpstreamRequestCountByStatus:          map[string]int{},
		UpstreamRequestCountsByURIStatus:      map[string]map[string]int{},
		UpstreamRequestCountsByEndpointStatus: map[string]map[string]int{},
		RequestDropCountsByURI:                map[string]int{},
		ResponseDropCountsByURI:               map[string]int{},
		URIMatchCounts:                        map[string]int{},
		HeaderMatchCounts:                     map[string]int{},
		HeaderValueMatchCounts:                map[string]map[string]int{},
		QueryMatchCounts:                      map[string]int{},
		QueryValueMatchCounts:                 map[string]map[string]int{},
	}
}

func (hc *HTTPCounts) IncrementMatchCounts(uri, header, headerValue, query, queryValue string) {
	hc.lock.Lock()
	defer hc.lock.Unlock()
	if uri != "" {
		hc.URIMatchCounts[uri]++
	}
	if header != "" {
		if headerValue != "" {
			if hc.HeaderValueMatchCounts[header] == nil {
				hc.HeaderValueMatchCounts[header] = map[string]int{}
			}
			hc.HeaderValueMatchCounts[header][headerValue]++
		} else {
			hc.HeaderMatchCounts[header]++
		}
	}
	if query != "" {
		if queryValue != "" {
			if hc.QueryValueMatchCounts[query] == nil {
				hc.QueryValueMatchCounts[query] = map[string]int{}
			}
			hc.QueryValueMatchCounts[query][queryValue]++
		} else {
			hc.QueryMatchCounts[query]++
		}
	}
}

func (hc *HTTPCounts) IncrementDropCount(uri string, requestDropped bool) {
	hc.lock.Lock()
	defer hc.lock.Unlock()
	if requestDropped {
		hc.RequestDropCount++
	} else {
		hc.ResponseDropCount++
	}
	if uri != "" {
		if requestDropped {
			hc.RequestDropCountsByURI[uri]++
		} else {
			hc.ResponseDropCountsByURI[uri]++
		}
	}
}

func NewHTTPTracker() *HTTPProxyTracker {
	return &HTTPProxyTracker{
		HTTPCounts:     NewHTTPCounts(),
		TargetTrackers: map[string]*HTTPTargetTracker{},
	}
}

func NewHTTPTargetTracker() *HTTPTargetTracker {
	return &HTTPTargetTracker{
		HTTPCounts: NewHTTPCounts(),
	}
}

func (pt *HTTPProxyTracker) IncrementDownstreamRequestCounts(requestURI string) {
	pt.lock.Lock()
	defer pt.lock.Unlock()
	pt.DownstreamRequestCount++
	pt.DownstreamRequestCountsByURI[requestURI]++
}

func (pt *HTTPProxyTracker) IncrementUpstreamRequestCounts(requestURI string) {
	pt.lock.Lock()
	defer pt.lock.Unlock()
	pt.UpstreamRequestCount++
	pt.UpstreamRequestCountsByURI[requestURI]++
}

func (pt *HTTPProxyTracker) IncrementTargetDownstreamCounts(targetName string, requestURI string) {
	pt.lock.Lock()
	defer pt.lock.Unlock()
	if pt.TargetTrackers[targetName] == nil {
		pt.TargetTrackers[targetName] = NewHTTPTargetTracker()
	}
	pt.TargetTrackers[targetName].lock.Lock()
	pt.TargetTrackers[targetName].DownstreamRequestCount++
	pt.TargetTrackers[targetName].DownstreamRequestCountsByURI[requestURI]++
	pt.TargetTrackers[targetName].lock.Unlock()
}

func (pt *HTTPProxyTracker) IncrementTargetUpstreamCounts(targetName, endpoint, requestURI string) {
	pt.lock.Lock()
	defer pt.lock.Unlock()
	if pt.TargetTrackers[targetName] == nil {
		pt.TargetTrackers[targetName] = NewHTTPTargetTracker()
	}
	pt.TargetTrackers[targetName].lock.Lock()
	pt.TargetTrackers[targetName].UpstreamRequestCount++
	pt.TargetTrackers[targetName].UpstreamRequestCountsByURI[requestURI]++
	pt.TargetTrackers[targetName].UpstreamRequestCountsByEndpoint[endpoint]++
	pt.TargetTrackers[targetName].lock.Unlock()
}

func (pt *HTTPProxyTracker) IncrementTargetUpstreamStatusCounts(targetName, endpoint, requestURI string, statusCode int) {
	pt.lock.Lock()
	defer pt.lock.Unlock()
	if pt.TargetTrackers[targetName] == nil {
		pt.TargetTrackers[targetName] = NewHTTPTargetTracker()
	}
	status := strconv.Itoa(statusCode)
	pt.UpstreamRequestCountByStatus[status]++
	pt.TargetTrackers[targetName].lock.Lock()
	pt.TargetTrackers[targetName].UpstreamRequestCountByStatus[status]++
	if pt.TargetTrackers[targetName].UpstreamRequestCountsByURIStatus[requestURI] == nil {
		pt.TargetTrackers[targetName].UpstreamRequestCountsByURIStatus[requestURI] = map[string]int{}
	}
	pt.TargetTrackers[targetName].UpstreamRequestCountsByURIStatus[requestURI][status]++
	if pt.TargetTrackers[targetName].UpstreamRequestCountsByEndpointStatus[endpoint] == nil {
		pt.TargetTrackers[targetName].UpstreamRequestCountsByEndpointStatus[endpoint] = map[string]int{}
	}
	pt.TargetTrackers[targetName].UpstreamRequestCountsByEndpointStatus[endpoint][status]++
	pt.TargetTrackers[targetName].lock.Unlock()
}

func (pt *HTTPProxyTracker) IncrementTargetMatchCounts(targetName string, uri, header, headerValue, query, queryValue string) {
	pt.IncrementMatchCounts(uri, header, headerValue, query, queryValue)
	pt.getOrCreateTargetTracker(targetName).IncrementMatchCounts(uri, header, headerValue, query, queryValue)
}

func (pt *HTTPProxyTracker) IncrementTargetDropCount(targetName string, requestURI string, requestDropped bool) {
	pt.IncrementDropCount(requestURI, requestDropped)
	pt.getOrCreateTargetTracker(targetName).IncrementDropCount(requestURI, requestDropped)
}

// getOrCreateTargetTracker finds the tracker of the target, which requests for other targets may be adding at the
// same time.
func (pt *HTTPProxyTracker) getOrCreateTargetTracker(targetName string) *HTTPTargetTracker {
	pt.lock.Lock()
	defer pt.lock.Unlock()
	if pt.TargetTrackers[targetName] == nil {
		pt.TargetTrackers[targetName] = NewHTTPTargetTracker()
	}
	return pt.TargetTrackers[targetName]
}

// GetTargetTracker returns the tracker of the target, or nil if the target has not been tracked.
func (pt *HTTPProxyTracker) GetTargetTracker(targetName string) *HTTPTargetTracker {
	pt.lock.RLock()
	defer pt.lock.RUnlock()
	return pt.TargetTrackers[targetName]
}

func cloneNested(m map[string]map[string]int) map[string]map[string]int {
	c := make(map[string]map[string]int, len(m))
	for k, v := range m {
		c[k] = maps.Clone(v)
	}
	return c
}

// snapshot returns a copy of the counts, for reporting them while requests keep counting.
func (hc *HTTPCounts) snapshot() *HTTPCounts {
	hc.lock.RLock()
	defer hc.lock.RUnlock()
	return &HTTPCounts{
		DownstreamRequestCount:                hc.DownstreamRequestCount,
		UpstreamRequestCount:                  hc.UpstreamRequestCount,
		RequestDropCount:                      hc.RequestDropCount,
		ResponseDropCount:                     hc.ResponseDropCount,
		DownstreamRequestCountsByURI:          maps.Clone(hc.DownstreamRequestCountsByURI),
		UpstreamRequestCountsByURI:            maps.Clone(hc.UpstreamRequestCountsByURI),
		UpstreamRequestCountsByEndpoint:       maps.Clone(hc.UpstreamRequestCountsByEndpoint),
		UpstreamRequestCountByStatus:          maps.Clone(hc.UpstreamRequestCountByStatus),
		UpstreamRequestCountsByURIStatus:      cloneNested(hc.UpstreamRequestCountsByURIStatus),
		UpstreamRequestCountsByEndpointStatus: cloneNested(hc.UpstreamRequestCountsByEndpointStatus),
		RequestDropCountsByURI:                maps.Clone(hc.RequestDropCountsByURI),
		ResponseDropCountsByURI:               maps.Clone(hc.ResponseDropCountsByURI),
		URIMatchCounts:                        maps.Clone(hc.URIMatchCounts),
		HeaderMatchCounts:                     maps.Clone(hc.HeaderMatchCounts),
		HeaderValueMatchCounts:                cloneNested(hc.HeaderValueMatchCounts),
		QueryMatchCounts:                      maps.Clone(hc.QueryMatchCounts),
		QueryValueMatchCounts:                 cloneNested(hc.QueryValueMatchCounts),
	}
}

// snapshot returns a copy of the tracker with the trackers of its targets, for reporting them while requests keep
// counting. It returns nil for a nil tracker.
func (pt *HTTPProxyTracker) snapshot() *HTTPProxyTracker {
	if pt == nil {
		return nil
	}
	pt.lock.RLock()
	targetTrackers := maps.Clone(pt.TargetTrackers)
	pt.lock.RUnlock()
	snap := &HTTPProxyTracker{HTTPCounts: pt.HTTPCounts.snapshot(), TargetTrackers: make(map[string]*HTTPTargetTracker, len(targetTrackers))}
	for name, tt := range targetTrackers {
		snap.TargetTrackers[name] = tt.snapshot()
	}
	return snap
}

// snapshot returns a copy of the counts of the target. It returns nil for a nil tracker.
func (tt *HTTPTargetTracker) snapshot() *HTTPTargetTracker {
	if tt == nil {
		return nil
	}
	return &HTTPTargetTracker{HTTPCounts: tt.HTTPCounts.snapshot()}
}
