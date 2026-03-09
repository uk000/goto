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

package mcpproxy

import (
	"errors"
	"goto/pkg/proxy/trackers"
	"goto/pkg/server/response/status"
	"goto/pkg/types"
	"goto/pkg/util"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type MCPSessionLog struct {
	logCounter       atomic.Int32
	ClientMessageLog map[int]any `json:"clientMessageLog"`
	ServerMessageLog map[int]any `json:"serverMessageLog"`
	err              string
}

type MCPSession struct {
	ID             string         `json:"id"`
	DownstreamAddr string         `json:"downstreamAddr"`
	Log            *MCPSessionLog `json:"log"`
	target         *MCPTarget
	tracker        *trackers.MCPProxyTracker
}

type MCPTarget struct {
	Name           string                 `json:"name"`
	Protocol       string                 `json:"protocol"`
	Endpoint       string                 `json:"endpoint"`
	Authority      string                 `json:"authority"`
	Delay          *types.Delay           `json:"delay"`
	Retries        int                    `json:"retries"`
	RetryDelay     time.Duration          `json:"retryDelay"`
	Tools          map[string]string      `json:"tools"`
	ActiveSessions map[string]*MCPSession `json:"activeSessions"`
	PastSessions   map[string]*MCPSession `json:"pastSessions"`
	lock           sync.RWMutex
}

type MatchedTarget struct {
	target      *MCPTarget
	matchedTool string
}

type MCPProxy struct {
	Port       int                       `json:"port"`
	Enabled    bool                      `json:"enabled"`
	Targets    map[string]*MCPTarget     `json:"targets"`
	MCPTracker *trackers.MCPProxyTracker `json:"tracker"`
	lock       sync.RWMutex
}

var (
	mcpProxyByPort = map[int]*MCPProxy{}
	mcpProxyLock   sync.RWMutex
)

func WillProxyMCP(port int, r *http.Request) (willProxy bool) {
	rs := util.GetRequestStore(r)
	if rs.IsAdminRequest || !rs.IsMCP {
		return false
	}
	p := GetMCPProxyForPort(port)
	if !p.Enabled || len(p.Targets) == 0 || status.IsForcedStatus(r) {
		return false
	}
	tool := ""
	if reReader, ok := r.Body.(*util.ReReader); ok {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			return false
		}
		bodyText := string(b)
		if !strings.Contains(bodyText, "tools/call") {
			return false
		}
		json := util.JSONFromReader(reReader)
		reReader.Rewind()
		tool = json.GetText("params.name")
	}
	if tool == "" {
		return false
	}
	target := p.Targets[tool]
	if target == nil {
		target = p.Targets["*"]
	}
	if target != nil {
		matches := map[string]*MatchedTarget{tool: {target: target, matchedTool: tool}}
		rs.ProxiedRequest = true
		rs.ProxyTargets = matches
		return true
	}
	return
}

func (p *MCPProxy) SetupMCPProxy(server, endpoint, fromTool, toTool string, headers map[string]string) error {
	_, err := p.addMCPTarget(server, endpoint, fromTool, toTool, headers)
	if err != nil {
		return err
	}
	return nil
}

func GetMCPProxyForPort(port int) *MCPProxy {
	mcpProxyLock.RLock()
	proxy := mcpProxyByPort[port]
	mcpProxyLock.RUnlock()
	if proxy == nil {
		proxy = newMCPProxy(port)
		mcpProxyLock.Lock()
		mcpProxyByPort[port] = proxy
		mcpProxyLock.Unlock()
	}
	return proxy
}

func newMCPProxy(port int) *MCPProxy {
	p := &MCPProxy{
		Port:       port,
		Enabled:    true,
		Targets:    map[string]*MCPTarget{},
		MCPTracker: &trackers.MCPProxyTracker{},
	}
	p.initTracker()
	return p
}

func (p *MCPProxy) Init() {
	p.lock.Lock()
	p.Targets = map[string]*MCPTarget{}
	p.lock.Unlock()
	p.initTracker()
}

func (p *MCPProxy) initTracker() {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.MCPTracker = trackers.NewMCPProxyTracker()
}

func (p *MCPProxy) RemoveProxy(server string) {
	delete(p.Targets, server)
}

func (p *MCPProxy) addMCPTarget(server, endpoint, fromTool, toTool string, headers map[string]string) (*MCPTarget, error) {
	if server == "" || endpoint == "" {
		return nil, errors.New("no endpoint given")
	}
	target := &MCPTarget{
		Name:           server,
		Endpoint:       endpoint,
		Tools:          map[string]string{},
		ActiveSessions: map[string]*MCPSession{},
		PastSessions:   map[string]*MCPSession{},
	}
	target.Tools[fromTool] = toTool
	return target, nil
}
