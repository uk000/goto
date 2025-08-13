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

package trackers

import (
	"sync"
)

type HTTPCounts struct {
	DownstreamRequestCount       int                       `json:"downstreamRequestCount"`
	UpstreamRequestCount         int                       `json:"upstreamRequestCount"`
	RequestDropCount             int                       `json:"requestDropCount"`
	ResponseDropCount            int                       `json:"responseDropCount"`
	DownstreamRequestCountsByURI map[string]int            `json:"downstreamRequestCountsByURI"`
	UpstreamRequestCountsByURI   map[string]int            `json:"upstreamRequestCountsByURI"`
	RequestDropCountsByURI       map[string]int            `json:"requestDropCountsByURI"`
	ResponseDropCountsByURI      map[string]int            `json:"responseDropCountsByURI"`
	URIMatchCounts               map[string]int            `json:"uriMatchCounts"`
	HeaderMatchCounts            map[string]int            `json:"headerMatchCounts"`
	HeaderValueMatchCounts       map[string]map[string]int `json:"headerValueMatchCounts"`
	QueryMatchCounts             map[string]int            `json:"queryMatchCounts"`
	QueryValueMatchCounts        map[string]map[string]int `json:"queryValueMatchCounts"`
	lock                         sync.RWMutex
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
		DownstreamRequestCountsByURI: map[string]int{},
		UpstreamRequestCountsByURI:   map[string]int{},
		RequestDropCountsByURI:       map[string]int{},
		ResponseDropCountsByURI:      map[string]int{},
		URIMatchCounts:               map[string]int{},
		HeaderMatchCounts:            map[string]int{},
		HeaderValueMatchCounts:       map[string]map[string]int{},
		QueryMatchCounts:             map[string]int{},
		QueryValueMatchCounts:        map[string]map[string]int{},
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

func (pt *HTTPProxyTracker) IncrementRequestCounts(requestURI string) {
	pt.lock.Lock()
	defer pt.lock.Unlock()
	pt.DownstreamRequestCount++
	pt.DownstreamRequestCountsByURI[requestURI]++
}

func (pt *HTTPProxyTracker) IncrementTargetRequestCounts(targetName string, requestURI string) {
	pt.lock.Lock()
	defer pt.lock.Unlock()
	pt.UpstreamRequestCount++
	pt.UpstreamRequestCountsByURI[requestURI]++
	if pt.TargetTrackers[targetName] == nil {
		pt.TargetTrackers[targetName] = NewHTTPTargetTracker()
	}
	pt.TargetTrackers[targetName].lock.Lock()
	pt.TargetTrackers[targetName].DownstreamRequestCount++
	pt.TargetTrackers[targetName].DownstreamRequestCountsByURI[requestURI]++
	pt.TargetTrackers[targetName].lock.Unlock()
}

func (pt *HTTPProxyTracker) IncrementTargetMatchCounts(targetName string, uri, header, headerValue, query, queryValue string) {
	pt.IncrementMatchCounts(uri, header, headerValue, query, queryValue)
	if pt.TargetTrackers[targetName] == nil {
		pt.TargetTrackers[targetName] = NewHTTPTargetTracker()
	}
	pt.TargetTrackers[targetName].IncrementMatchCounts(uri, header, headerValue, query, queryValue)
}

func (pt *HTTPProxyTracker) IncrementTargetDropCount(targetName string, requestURI string, requestDropped bool) {
	pt.IncrementDropCount(requestURI, requestDropped)
	if pt.TargetTrackers[targetName] == nil {
		pt.TargetTrackers[targetName] = NewHTTPTargetTracker()
	}
	pt.TargetTrackers[targetName].IncrementDropCount(requestURI, requestDropped)
}
