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

package mcpproxy

import (
	"bytes"
	"errors"
	"goto/pkg/server/response/status"
	"goto/pkg/types"
	"goto/pkg/util"
	"maps"
	"net/http"
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
	tracker        *MCPProxyTracker
}

type MCPTarget struct {
	Name           string                 `json:"name"`
	Protocol       string                 `json:"protocol"`
	Endpoint       string                 `json:"endpoint"`
	Authority      string                 `json:"authority"`
	Headers        map[string]string      `json:"headers"`
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
	Port       int                   `json:"port"`
	Enabled    bool                  `json:"enabled"`
	Targets    map[string]*MCPTarget `json:"targets"`
	MCPTracker *MCPProxyTracker      `json:"tracker"`
	lock       sync.RWMutex
}

var (
	mcpProxyByPort = map[int]*MCPProxy{}
	mcpProxyLock   sync.RWMutex
)

// WillProxyMCP tells if the request is a call of a tool that has a proxy target on the port, and if so puts the matched
// targets in the request store. The body of the request is left as it was for whoever serves the request next.
func WillProxyMCP(port int, r *http.Request) (willProxy bool) {
	rs := util.GetRequestStore(r)
	if rs.IsAdminRequest || !rs.IsMCP {
		return false
	}
	p := GetMCPProxyForPort(port)
	if !p.isEnabled() || !p.hasTargets() || status.IsForcedStatus(r) {
		return false
	}
	// The body is kept by the reader, which is read without moving it, as a request that is not proxied is served
	// by reading the same body.
	reReader, ok := r.Body.(*util.ReReader)
	if !ok {
		return false
	}
	body := reReader.Content
	if !bytes.Contains(body, []byte("tools/call")) {
		return false
	}
	tool := util.JSONFromBytes(body).GetText("params.name")
	if tool == "" {
		return false
	}
	if target := p.getTarget(tool); target != nil {
		rs.ProxiedRequest = true
		rs.ProxyTargets = map[string]*MatchedTarget{tool: {target: target, matchedTool: tool}}
		return true
	}
	return false
}

// SetupMCPProxy adds a proxy target for the tool, which proxies it to the endpoint under the name of the other tool.
func (p *MCPProxy) SetupMCPProxy(server, endpoint, fromTool, toTool string, headers map[string]string) error {
	_, err := p.addMCPTarget(server, endpoint, fromTool, toTool, headers)
	if err != nil {
		return err
	}
	return nil
}

// GetMCPProxyForPort returns the MCP proxy of the port, creating and registering it if it doesn't exist.
func GetMCPProxyForPort(port int) *MCPProxy {
	mcpProxyLock.RLock()
	proxy := mcpProxyByPort[port]
	mcpProxyLock.RUnlock()
	if proxy == nil {
		mcpProxyLock.Lock()
		defer mcpProxyLock.Unlock()
		// Another caller may have created the proxy since the read lock was released, and all callers must get that one.
		if proxy = mcpProxyByPort[port]; proxy == nil {
			proxy = newMCPProxy(port)
			mcpProxyByPort[port] = proxy
		}
	}
	return proxy
}

// isEnabled tells whether the proxy is enabled.
func (p *MCPProxy) isEnabled() bool {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.Enabled
}

// hasTargets tells whether the proxy has any targets.
func (p *MCPProxy) hasTargets() bool {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return len(p.Targets) > 0
}

// getTarget finds the target of the tool, which is the one for all tools ("*") if the tool has none of its own.
func (p *MCPProxy) getTarget(tool string) *MCPTarget {
	p.lock.RLock()
	defer p.lock.RUnlock()
	if target := p.Targets[tool]; target != nil {
		return target
	}
	return p.Targets["*"]
}

// newMCPProxy creates an enabled proxy for the port, with no targets and a fresh tracker.
func newMCPProxy(port int) *MCPProxy {
	p := &MCPProxy{
		Port:       port,
		Enabled:    true,
		Targets:    map[string]*MCPTarget{},
		MCPTracker: &MCPProxyTracker{},
	}
	p.initTracker()
	return p
}

// Init removes all the targets of the proxy and resets its tracker.
func (p *MCPProxy) Init() {
	p.lock.Lock()
	p.Targets = map[string]*MCPTarget{}
	p.lock.Unlock()
	p.initTracker()
}

// initTracker replaces the tracker of the proxy with a fresh one.
func (p *MCPProxy) initTracker() {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.MCPTracker = NewMCPProxyTracker()
}

// RemoveProxy removes the target of the tool of the given name, and the targets of the server of the given name.
func (p *MCPProxy) RemoveProxy(server string) {
	p.lock.Lock()
	defer p.lock.Unlock()
	for tool, target := range p.Targets {
		if tool == server || target.Name == server {
			delete(p.Targets, tool)
		}
	}
}

// addMCPTarget creates a target for the tool ("*" if none) with a copy of the headers, and registers it on the proxy.
func (p *MCPProxy) addMCPTarget(server, endpoint, fromTool, toTool string, headers map[string]string) (*MCPTarget, error) {
	if server == "" || endpoint == "" {
		return nil, errors.New("no endpoint given")
	}
	if fromTool == "" {
		// A proxy that is not for a tool is for all of them.
		fromTool = "*"
	}
	target := &MCPTarget{
		Name:           server,
		Endpoint:       endpoint,
		Headers:        maps.Clone(headers),
		Tools:          map[string]string{},
		ActiveSessions: map[string]*MCPSession{},
		PastSessions:   map[string]*MCPSession{},
	}
	target.Tools[fromTool] = toTool
	p.lock.Lock()
	p.Targets[fromTool] = target
	p.lock.Unlock()
	return target, nil
}
