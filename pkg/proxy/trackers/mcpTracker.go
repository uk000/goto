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

import "sync"

type MCPProxyTracker struct {
	*HTTPProxyTracker
	ConnCount                 int                       `json:"connCount"`
	ConnCountByUpstream       map[string]int            `json:"connCountByUpstream"`
	RequestCountByUpstream    map[string]int            `json:"requestCountByUpstream"`
	RequestCountByServer      map[string]int            `json:"requestCountByServer"`
	RequestCountByServerTool  map[string]map[string]int `json:"requestCountByServerTool"`
	ResponseCountByUpstream   map[string]int            `json:"responseCountByUpstream"`
	ResponseCountByServer     map[string]int            `json:"responseCountByServer"`
	ResponseCountByServerTool map[string]map[string]int `json:"responseCountByServerTool"`
	MessageCountByType        map[string]int            `json:"messageCountByType"`
	lock                      sync.RWMutex
}

func NewMCPProxyTracker() *MCPProxyTracker {
	return &MCPProxyTracker{
		HTTPProxyTracker:          NewHTTPTracker(),
		ConnCount:                 0,
		ConnCountByUpstream:       map[string]int{},
		RequestCountByUpstream:    map[string]int{},
		RequestCountByServer:      map[string]int{},
		RequestCountByServerTool:  map[string]map[string]int{},
		ResponseCountByUpstream:   map[string]int{},
		ResponseCountByServer:     map[string]int{},
		ResponseCountByServerTool: map[string]map[string]int{},
		MessageCountByType:        map[string]int{},
	}
}

func (pt *MCPProxyTracker) IncrementConnCounts(upstream string) {
	pt.lock.Lock()
	defer pt.lock.Unlock()
	pt.ConnCount++
	if upstream != "" {
		pt.ConnCountByUpstream[upstream]++
	}
}

func (pt *MCPProxyTracker) AddMatchCounts(upstream, server, tool, requestMessageType string, requestCount int, responseMessageType string, responseCount int) {
	pt.lock.Lock()
	defer pt.lock.Unlock()
	if upstream != "" {
		pt.RequestCountByUpstream[upstream] += requestCount
		pt.ResponseCountByUpstream[upstream] += responseCount
	}
	if server != "" {
		pt.RequestCountByServer[server] += requestCount
		pt.ResponseCountByServer[server] += responseCount
		if tool != "" {
			if pt.RequestCountByServerTool[server] == nil {
				pt.RequestCountByServerTool[server] = map[string]int{}
			}
			pt.RequestCountByServerTool[server][tool] += requestCount
			if pt.ResponseCountByServerTool[server] == nil {
				pt.ResponseCountByServerTool[server] = map[string]int{}
			}
			pt.ResponseCountByServerTool[server][tool] += responseCount
		}
	}
	if requestMessageType != "" {
		pt.MessageCountByType[requestMessageType] += requestCount
	}
	if responseMessageType != "" {
		pt.MessageCountByType[responseMessageType] += responseCount
	}
}
