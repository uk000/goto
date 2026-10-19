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

package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"goto/pkg/global"
	mcpproxy "goto/pkg/proxy/mcp"
	"goto/pkg/server/listeners"
	"goto/pkg/server/response/payload"
	"goto/pkg/server/response/status"
	"goto/pkg/types"
	"goto/pkg/util"
	"log"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type ToolSchema struct {
	Name        string `json:"name"`
	InputSchema any    `json:"inputSchema"`
}

type MCPServer struct {
	gomcp.Implementation
	ID                string                          `json:"id"`
	Description       string                          `json:"description,omitempty"`
	Host              string                          `json:"host"`
	Port              int                             `json:"port"`
	Tools             map[string]*MCPTool             `json:"tools"`
	Prompts           map[string]*MCPPrompt           `json:"prompts"`
	Resources         map[string]*MCPResource         `json:"resources"`
	ResourceTemplates map[string]*MCPResourceTemplate `json:"templates"`
	CompletionPayload map[string]*payload.Payload     `json:"completionPayload"`
	Stateless         bool                            `json:"stateless"`
	Enabled           bool                            `json:"enabled"`
	URI               string                          `json:"uri,omitempty"`
	SSEURI            string                          `json:"sseURI,omitempty"`
	ToolsByURI        map[string]*MCPTool             `json:"toolsByURI,omitempty"`
	URIRegex          string                          `json:"uriRegex,omitempty"`
	uriRegexp         *regexp.Regexp
	server            *gomcp.Server
	handler           http.Handler
	sseHandler        *gomcp.SSEHandler
	streamHTTPHandler *gomcp.StreamableHTTPHandler
	sessionContexts   map[string]*SessionContext
	ps                *PortServers
	lock              sync.RWMutex
}

type MCPServerPayload struct {
	Port         int           `json:"port"`
	Name         string        `json:"name"`
	Version      string        `json:"version"`
	Description  string        `json:"description,omitempty"`
	Instructions string        `json:"instructions,omitempty"`
	KeepAlive    time.Duration `json:"keepAlive,omitempty"`
	PageSize     int           `json:"pageSize,omitempty"`
	HasPrompts   bool          `json:"hasPrompts,omitempty"`
	HasResources bool          `json:"hasResources,omitempty"`
	HasTools     bool          `json:"hasTools,omitempty"`
	Stateless    bool          `json:"stateless,omitempty"`
	Enabled      bool          `json:"enabled,omitempty"`
	URI          string        `json:"uri,omitempty"`
	URIRegex     string        `json:"uriRegex,omitempty"`
}

type SessionContext struct {
	SessionID  string
	Server     *MCPServer
	MS         *util.MCPRequestStore
	finished   chan bool
	finishOnce sync.Once
}

// finish tells the requests that wait for the session that it is over. It can be called any number of times, e.g. when
// the session is deleted more than once at the same time.
func (s *SessionContext) finish() {
	s.finishOnce.Do(func() { close(s.finished) })
}

type PortServers struct {
	Port          int                                            `json:"port"`
	Servers       map[string]*MCPServer                          `json:"servers"`
	DefaultServer string                                         `json:"defaultServer"`
	defaultServer *MCPServer                                     `json:"-"`
	AllComponents map[string]map[string]map[string]IMCPComponent `json:"-"`
	lock          sync.RWMutex
}

const (
	KindTools           = "tools"
	KindPrompts         = "prompts"
	KindResources       = "resources"
	KindTemplates       = "templates"
	HeaderMCPSessionID  = "Mcp-Session-Id"
	HeaderXMCPSessionID = "X-MCP-Session-ID"
)

var (
	DefaultStatelessServer   *MCPServer
	DefaultStatefulServer    *MCPServer
	PortsServers             = map[int]*PortServers{}
	ServerRoutes             = map[string]*types.Pair[string, string]{}
	AllComponents            = map[string]map[string]map[string]IMCPComponent{}
	ToolSchemas              = map[string]any{}
	Kinds                    = []string{KindTools, KindPrompts, KindResources, KindTemplates}
	StatusManager            = status.NewStatusManager()
	MCPRequestStoreBySession = map[string]*util.MCPRequestStore{}
	lock                     sync.RWMutex
)

func init() {
	for _, kind := range Kinds {
		AllComponents[kind] = map[string]map[string]IMCPComponent{}
	}
	ToolSchemas["default"] = map[string]any{
		"type":     "object",
		"required": []string{},
	}
	ToolSchemas["basic"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"text":  map[string]any{"type": "string"},
			"delay": map[string]any{"type": "string"},
			"count": map[string]any{"type": "int"},
			"size":  map[string]any{"type": "int"},
		},
		"required": []string{},
	}
}

func InitDefaultServer() {
	p := &MCPServerPayload{
		Port:         global.Self.JSONRPCPort,
		Name:         "default-stateless",
		Version:      "1.0",
		Description:  "Default Stateless Server",
		Instructions: "Default Stateless Instructions",
		URI:          "/mcp/default/stateless",
		Stateless:    true,
		Enabled:      true,
	}
	DefaultStatelessServer = GetPortMCPServers(p.Port).AddMCPServer(p)
	DefaultStatelessServer.sseHandler = gomcp.NewSSEHandler(func(r *http.Request) *gomcp.Server { return DefaultStatelessServer.server }, &gomcp.SSEOptions{})
	DefaultStatelessServer.streamHTTPHandler = gomcp.NewStreamableHTTPHandler(func(r *http.Request) *gomcp.Server { return DefaultStatelessServer.server }, &gomcp.StreamableHTTPOptions{Stateless: true})
	DefaultStatelessServer.handler = MCPHybridHandler(DefaultStatelessServer)

	p2 := &MCPServerPayload{
		Port:         global.Self.JSONRPCPort,
		Name:         "default-stateful",
		Version:      "1.0",
		Description:  "Default Stateful Server",
		Instructions: "Default Stateful Instructions",
		URI:          "/mcp/default/stateful",
		Stateless:    false,
		Enabled:      true,
	}
	DefaultStatefulServer = GetPortMCPServers(p2.Port).AddMCPServer(p2)
	DefaultStatefulServer.sseHandler = gomcp.NewSSEHandler(func(r *http.Request) *gomcp.Server { return DefaultStatefulServer.server }, &gomcp.SSEOptions{})
	DefaultStatefulServer.streamHTTPHandler = gomcp.NewStreamableHTTPHandler(func(r *http.Request) *gomcp.Server { return DefaultStatefulServer.server }, &gomcp.StreamableHTTPOptions{Stateless: false})
	DefaultStatefulServer.handler = MCPHybridHandler(DefaultStatefulServer)

}

func NewMCPServer(p *MCPServerPayload) *MCPServer {
	server := &MCPServer{
		ID:          fmt.Sprintf("[%s:%d][%s][%s]", p.Name, p.Port, global.Self.Namespace, global.Self.Cluster),
		Description: p.Description,
		Enabled:     p.Enabled,
		Host:        fmt.Sprintf("[%s][%s]", global.Self.PodName, listeners.GetListenerLabelForPort(p.Port)),
		Port:        p.Port,
		Stateless:   p.Stateless,
		URI:         p.URI,
		URIRegex:    p.URIRegex,
		Implementation: gomcp.Implementation{
			Name:    p.Name,
			Title:   p.Name,
			Version: p.Version,
		},
		Tools:             map[string]*MCPTool{},
		ToolsByURI:        map[string]*MCPTool{},
		Prompts:           map[string]*MCPPrompt{},
		Resources:         map[string]*MCPResource{},
		ResourceTemplates: map[string]*MCPResourceTemplate{},
		CompletionPayload: map[string]*payload.Payload{},
		sessionContexts:   map[string]*SessionContext{},
	}
	server.server = gomcp.NewServer(&server.Implementation, &gomcp.ServerOptions{
		Instructions:                p.Instructions,
		PageSize:                    p.PageSize,
		KeepAlive:                   p.KeepAlive,
		InitializedHandler:          server.onInitialized,
		RootsListChangedHandler:     server.onRootsListChanged,
		ProgressNotificationHandler: server.onProgressNotification,
		CompletionHandler:           server.onCompletion,
		SubscribeHandler:            server.onSubscribed,
		UnsubscribeHandler:          server.onUnsubscribed,
		HasPrompts:                  p.HasPrompts,
		HasResources:                p.HasResources,
		HasTools:                    p.HasTools,
	})
	if server.URIRegex != "" {
		if re, err := regexp.Compile(server.URIRegex); err == nil {
			server.uriRegexp = re
		} else {
			log.Printf("MCP Server [%s] ignoring invalid URI regex [%s]: %s", server.Name, server.URIRegex, err.Error())
			server.URIRegex = ""
		}
	}
	if server.uriRegexp == nil && server.URI != "" {
		server.uriRegexp = uriRegexpFor(server.URI)
	}
	server.streamHTTPHandler = gomcp.NewStreamableHTTPHandler(getServer, &gomcp.StreamableHTTPOptions{Stateless: p.Stateless})
	server.sseHandler = gomcp.NewSSEHandler(getServer, &gomcp.SSEOptions{})
	server.handler = MCPHybridHandler(server)
	server.server.AddReceivingMiddleware(server.Middleware)
	server.addDefaultTools()
	return server
}

// uriRegexpFor is the regexp that the URIs of a server match. The URI is used as it is, unless it has characters that make
// it an invalid regexp.
func uriRegexpFor(uri string) *regexp.Regexp {
	for _, u := range []string{uri, regexp.QuoteMeta(uri)} {
		if re, err := regexp.Compile(fmt.Sprintf("%s%s%s", util.URIPrefixRegexParts[0], u, util.URIPrefixRegexParts[1])); err == nil {
			return re
		}
	}
	return nil
}

func SetServerRoute(uri string, server *MCPServer) {
	lock.Lock()
	defer lock.Unlock()
	uri = strings.ToLower(uri)
	server.lock.Lock()
	defer server.lock.Unlock()
	if strings.Contains(uri, "/mcp") {
		parts := strings.SplitN(uri, "/mcp", 2)
		server.SSEURI = parts[0] + "/mcp/sse" + parts[1]
	}
	pair := types.NewPair(server.Name, "")
	ServerRoutes[uri] = pair
	if server.SSEURI != "" {
		ServerRoutes[server.SSEURI] = pair
	}
	for tool := range server.Tools {
		tool = strings.ToLower(tool)
		pair := types.NewPair(server.Name, tool)
		ServerRoutes[uri+"/"+tool] = pair
		if server.SSEURI != "" {
			ServerRoutes[server.SSEURI+"/"+tool] = pair
		}
	}
}

// addToolRoutes routes the URIs of the tool to the server. A server that has no route has no SSE URI either, which is
// not a prefix to route the tool under.
func (m *MCPServer) addToolRoutes(tool *MCPTool) {
	lock.Lock()
	defer lock.Unlock()
	pair := types.NewPair(m.Name, tool.Name)
	ServerRoutes[tool.ServerURI] = pair
	m.lock.RLock()
	sseURI := m.SSEURI
	m.lock.RUnlock()
	if sseURI != "" {
		ServerRoutes[sseURI+tool.URI] = pair
	}
}

func allPortServers() []*PortServers {
	lock.RLock()
	defer lock.RUnlock()
	all := make([]*PortServers, 0, len(PortsServers))
	for _, ps := range PortsServers {
		all = append(all, ps)
	}
	return all
}

func (ps *PortServers) allServers() []*MCPServer {
	ps.lock.RLock()
	defer ps.lock.RUnlock()
	servers := make([]*MCPServer, 0, len(ps.Servers))
	for _, s := range ps.Servers {
		servers = append(servers, s)
	}
	return servers
}

func GetMCPServerNames() map[int]map[string][]string {
	byPort := map[int]map[string][]string{}
	for _, ps := range allPortServers() {
		servers := map[string][]string{}
		ps.lock.RLock()
		names := make(map[string]*MCPServer, len(ps.Servers))
		maps.Copy(names, ps.Servers)
		ps.lock.RUnlock()
		for serverName, server := range names {
			server.lock.RLock()
			tools := make([]string, 0, len(server.Tools))
			for _, tool := range server.Tools {
				tools = append(tools, tool.Tool.Name)
			}
			server.lock.RUnlock()
			servers[serverName] = tools
		}
		byPort[ps.Port] = servers
	}
	return byPort
}

func GetPortMCPServers(port int) *PortServers {
	lock.RLock()
	ps := PortsServers[port]
	lock.RUnlock()
	if ps != nil {
		return ps
	}
	lock.Lock()
	defer lock.Unlock()
	// Another request could have added it in the meantime.
	if ps = PortsServers[port]; ps == nil {
		ps = NewPortMCPServers(port)
		PortsServers[port] = ps
	}
	return ps
}

func GetMCPServer(port int, name string) (server *MCPServer) {
	return GetPortMCPServers(port).GetMCPServer(name)
}

func NewPortMCPServers(port int) *PortServers {
	ps := &PortServers{
		Port:          port,
		Servers:       map[string]*MCPServer{},
		AllComponents: map[string]map[string]map[string]IMCPComponent{},
	}
	for _, kind := range Kinds {
		ps.AllComponents[kind] = map[string]map[string]IMCPComponent{}
	}
	return ps
}

func GetPortDefaultMCPServer(port int) *MCPServer {
	ps := GetPortMCPServers(port)
	if len(ps.Servers) > 0 {
		return ps.defaultServer
	}
	if DefaultStatelessServer == nil {
		InitDefaultServer()
	}
	return DefaultStatelessServer
}

func AddMCPServers(port int, payloads []*MCPServerPayload) {
	for _, p := range payloads {
		if p.Port <= 0 {
			p.Port = port
		}
		GetPortMCPServers(p.Port).AddMCPServer(p)
	}
}

func RemoveMCPServer(port int, server string) {
	GetPortMCPServers(port).RemoveMCPServer(server)
}

func snapshotAllComponents(src map[string]map[string]map[string]IMCPComponent) map[string]map[string]map[string]IMCPComponent {
	out := make(map[string]map[string]map[string]IMCPComponent, len(src))
	for kind, byName := range src {
		out[kind] = make(map[string]map[string]IMCPComponent, len(byName))
		for name, byServer := range byName {
			out[kind][name] = maps.Clone(byServer)
		}
	}
	return out
}

func currentComponents() map[string]map[string]map[string]IMCPComponent {
	lock.RLock()
	defer lock.RUnlock()
	return snapshotAllComponents(AllComponents)
}

func snapshotServerRoutes() map[string]*types.Pair[string, string] {
	lock.RLock()
	defer lock.RUnlock()
	return maps.Clone(ServerRoutes)
}

func ClearAllMCPServers() {
	lock.Lock()
	defer lock.Unlock()
	PortsServers = map[int]*PortServers{}
	AllComponents = map[string]map[string]map[string]IMCPComponent{}
	for _, kind := range Kinds {
		AllComponents[kind] = map[string]map[string]IMCPComponent{}
	}
}

func (ps *PortServers) AddMCPServer(p *MCPServerPayload) *MCPServer {
	s := NewMCPServer(p)
	s.ps = ps
	ps.lock.Lock()
	ps.Servers[strings.ToLower(p.Name)] = s
	ps.defaultServer = s
	ps.DefaultServer = s.Name
	ps.lock.Unlock()
	if s.uriRegexp != nil {
		uri := s.URI
		if s.URI == "" {
			uri = s.URIRegex
		}
		SetServerRoute(uri, s)
	}
	return s
}

func (ps *PortServers) RemoveMCPServer(server string) {
	// Servers are kept by their lower case name, and their components by the name that they were given.
	key := strings.ToLower(server)
	s := ps.GetMCPServer(key)
	if s == nil {
		return
	}
	ps.removeServerComponents(s.Name)
	ps.lock.Lock()
	defer ps.lock.Unlock()
	delete(ps.Servers, key)
	if ps.defaultServer == s || strings.EqualFold(server, ps.DefaultServer) {
		count := len(ps.Servers)
		if count > 0 {
			i := 0
			for _, s := range ps.Servers {
				if i == count-1 {
					ps.defaultServer = s
					ps.DefaultServer = ps.defaultServer.Name
					break
				}
				i++
			}
		} else if DefaultStatefulServer != nil {
			ps.defaultServer = DefaultStatefulServer
			ps.DefaultServer = DefaultStatefulServer.Name
		} else {
			ps.defaultServer = nil
			ps.DefaultServer = ""
		}
	}
}

// getDefaultServer gives the server that serves the requests that don't tell which server they are for, and the number
// of servers of the port.
func (ps *PortServers) getDefaultServer() (*MCPServer, int) {
	ps.lock.RLock()
	defer ps.lock.RUnlock()
	return ps.defaultServer, len(ps.Servers)
}

func (ps *PortServers) AllServers() map[int]*PortServers {
	lock.RLock()
	defer lock.RUnlock()
	return maps.Clone(PortsServers)
}

// MarshalJSON reports the servers of the port, without them changing while it does.
func (ps *PortServers) MarshalJSON() ([]byte, error) {
	type portServers PortServers
	ps.lock.RLock()
	defer ps.lock.RUnlock()
	return json.Marshal((*portServers)(ps))
}

// MarshalJSON reports the server, without its components changing while it does.
func (m *MCPServer) MarshalJSON() ([]byte, error) {
	type mcpServer MCPServer
	m.lock.RLock()
	defer m.lock.RUnlock()
	return json.Marshal((*mcpServer)(m))
}

func (m *MCPServer) isEnabled() bool {
	m.lock.RLock()
	defer m.lock.RUnlock()
	return m.Enabled
}

func (m *MCPServer) setEnabled(enabled bool) {
	m.lock.Lock()
	m.Enabled = enabled
	m.lock.Unlock()
}

func (ps *PortServers) GetMCPServer(name string) *MCPServer {
	name = strings.ToLower(name)
	ps.lock.RLock()
	defer ps.lock.RUnlock()
	return ps.Servers[name]
}

func (ps *PortServers) Start(server string) {
	ps.setEnabled(server, true)
}

func (ps *PortServers) Stop(server string) {
	ps.setEnabled(server, false)
}

// setEnabled starts or stops the server of the given name, or all servers of the port if there is no name.
func (ps *PortServers) setEnabled(server string, enabled bool) {
	for _, s := range ps.allServers() {
		if server == "" {
			s.setEnabled(enabled)
		} else if strings.EqualFold(s.Name, server) {
			s.setEnabled(enabled)
			break
		}
	}
}

func (ps *PortServers) Clear() {
	for _, s := range ps.allServers() {
		s.Clear()
	}
}

func (ps *PortServers) GetComponents(kind string) any {
	ps.lock.RLock()
	defer ps.lock.RUnlock()
	byName := ps.AllComponents[kind]
	out := make(map[string]map[string]IMCPComponent, len(byName))
	for name, byServer := range byName {
		out[name] = maps.Clone(byServer)
	}
	return out
}

func (m *MCPServer) GetID() string {
	return m.ID
}

func (m *MCPServer) GetHost() string {
	return m.Host
}
func (m *MCPServer) GetName() string {
	return m.Name
}

func (m *MCPServer) GetPort() int {
	return m.Port
}

func (m *MCPServer) Clear() {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.Tools = map[string]*MCPTool{}
	m.ToolsByURI = map[string]*MCPTool{}
	m.Prompts = map[string]*MCPPrompt{}
	m.Resources = map[string]*MCPResource{}
	m.ResourceTemplates = map[string]*MCPResourceTemplate{}
	m.CompletionPayload = map[string]*payload.Payload{}
	m.sessionContexts = map[string]*SessionContext{}
}

// GetComponents gives a copy of the components of the kind, as they are while the server is changed.
func (m *MCPServer) GetComponents(kind string) any {
	m.lock.RLock()
	defer m.lock.RUnlock()
	switch kind {
	case KindTools:
		return maps.Clone(m.Tools)
	case KindPrompts:
		return maps.Clone(m.Prompts)
	case KindResources:
		return maps.Clone(m.Resources)
	case KindTemplates:
		return maps.Clone(m.ResourceTemplates)
	}
	return nil
}

func (m *MCPServer) GetComponent(name, kind string) IMCPComponent {
	lock.RLock()
	defer lock.RUnlock()
	if m1 := AllComponents[kind]; m1 != nil {
		if m2 := m1[name]; m2 != nil {
			return m2[m.Name]
		}
	}
	return nil
}

func (m *MCPServer) GetTool(name string) *MCPTool {
	m.lock.RLock()
	defer m.lock.RUnlock()
	return m.Tools[name]
}

// safely turns the panic of the SDK, for the components that it doesn't accept, into an error.
func safely(what string, f func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%s: %v", what, r)
		}
	}()
	f()
	return nil
}

func (m *MCPServer) AddPayload(name, kind string, payload []byte, isJSON, isStream bool, streamCount int, delayMin, delayMax time.Duration, delayCount int) error {
	c := m.GetComponent(name, kind)
	if c != nil {
		c.SetPayload(payload, isJSON, isStream, streamCount, delayMin, delayMax, delayCount)
		return nil
	}
	return fmt.Errorf("%s [%s] not found", kind, name)
}

func (ps *PortServers) addComponentToAll(c IMCPComponent, server string) {
	ps.lock.Lock()
	kind := c.GetKind()
	name := c.GetName()
	if ps.AllComponents[kind][name] == nil {
		ps.AllComponents[kind][name] = map[string]IMCPComponent{}
	}
	ps.AllComponents[kind][name][server] = c
	ps.lock.Unlock()
	lock.Lock()
	if AllComponents[kind][name] == nil {
		AllComponents[kind][name] = map[string]IMCPComponent{}
	}
	AllComponents[kind][name][server] = c
	lock.Unlock()
}

func (ps *PortServers) removeServerComponents(server string) {
	ps.lock.Lock()
	defer ps.lock.Unlock()
	lock.Lock()
	defer lock.Unlock()
	for kind, values := range ps.AllComponents {
		for name := range values {
			delete(ps.AllComponents[kind][name], server)
			delete(AllComponents[kind][name], server)
		}
	}
}

func (s *MCPServer) AddComponents(kind string, b []byte) (names []string, err error) {
	switch kind {
	case KindTools:
		names, err = s.AddTools(b)
	case KindPrompts:
		names, err = s.AddPrompts(b)
	case KindResources:
		names, err = s.AddResources(b)
	case KindTemplates:
		names, err = s.AddResourceTemplates(b)
	}
	if err == nil {
		return names, nil
	} else {
		return nil, err
	}
}

func (m *MCPServer) AddTools(b []byte) ([]string, error) {
	tools, err := ParseTools(b)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, tool := range tools {
		if err := m.addTool(tool); err != nil {
			return names, err
		}
		names = append(names, tool.Name)
	}
	log.Printf("Server [%s][%d] added Tools [%+v] ", m.Name, m.Port, names)
	return names, nil
}

func (m *MCPServer) AddTool(tool *MCPTool) {
	if err := m.addTool(tool); err != nil {
		log.Printf("Server [%s][%d] failed to add Tool: %s", m.Name, m.Port, err.Error())
	}
}

// addTool adds the tool to the server, if the tool is one that the server can serve. It is ready to run when the SDK
// gets to know it, so a call can't find a tool that has nothing to run.
func (m *MCPServer) addTool(tool *MCPTool) error {
	if tool == nil || tool.Tool == nil {
		return errors.New("Missing tool definition")
	}
	if err := tool.validateBehavior(); err != nil {
		return err
	}
	tool.Kind = KindTools
	tool.Server = m
	tool.SetName(tool.Tool.Name)
	if tool.URI == "" {
		tool.URI = strings.ToLower("/" + tool.Name)
	}
	tool.ServerURI = m.URI + tool.URI
	tool.BuildLabel()
	if err := tool.prepareBehavior(); err != nil {
		return err
	}
	if err := safely("Tool ["+tool.Name+"]", func() { m.server.AddTool(tool.Tool, tool.Handle) }); err != nil {
		return err
	}
	if tool.IsProxy {
		mcpproxy.GetMCPProxyForPort(m.Port).SetupMCPProxy(m.Name, tool.Config.RemoteTool.URL, tool.Tool.Name, tool.Tool.Name, nil)
	}
	m.lock.Lock()
	m.Tools[tool.Tool.Name] = tool
	m.ToolsByURI[tool.URI] = tool
	m.lock.Unlock()
	m.addToolRoutes(tool)
	if m.ps != nil {
		m.ps.addComponentToAll(tool, m.Name)
	}
	return nil
}

func (s *MCPServer) addDefaultTools() {
	t := &MCPTool{
		Tool: &gomcp.Tool{
			Name:        "ServerDetails",
			Description: "Get Server Details for " + s.Name,
			InputSchema: &jsonschema.Schema{
				Type: "object",
			},
		},
		Behavior: ToolBehavior{ServerDetails: true},
	}
	s.AddTool(t)
	t = &MCPTool{
		Tool: &gomcp.Tool{
			Name:        "ServerPaths",
			Description: "List All Registered Server URIs for " + s.Name,
			InputSchema: &jsonschema.Schema{
				Type: "object",
			},
		},
		Behavior: ToolBehavior{ServerPaths: true},
	}
	s.AddTool(t)
	t = &MCPTool{
		Tool: &gomcp.Tool{
			Name:        "ListServers",
			Description: "List All MCP Servers on the port",
			InputSchema: &jsonschema.Schema{
				Type: "object",
			},
		},
		Behavior: ToolBehavior{AllServers: true},
	}
	s.AddTool(t)
	t = &MCPTool{
		Tool: &gomcp.Tool{
			Name:        "ListComponents",
			Description: "List all registered Components from all servers",
			InputSchema: &jsonschema.Schema{
				Type: "object",
			},
		},
		Behavior: ToolBehavior{AllComponents: true},
	}
	s.AddTool(t)
	t = &MCPTool{
		Tool: &gomcp.Tool{
			Name:        "AddTool",
			Description: "Add a tool on-the-fly to the server",
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"name":        {Type: "string", Description: "Tool name"},
					"description": {Type: "string", Description: "Tool description"},
					"schema":      {Type: "string", Description: "Tool schema (default, basic-text-delay-schema, tool-call-schema, fetch-schema, remote-tool-schema, agent-call-schema)"},
					"behavior":    {Type: "object", Description: "Tool Behavior"},
				},
				Required: []string{"name", "schema"},
			},
		},
		Behavior: ToolBehavior{AddTool: true},
	}
	s.AddTool(t)
}

func (m *MCPServer) AddPrompts(b []byte) ([]string, error) {
	arr := util.ToJSONArray(b)
	prompts := []*MCPPrompt{}
	names := []string{}
	for _, b2 := range arr {
		prompt, err := ParsePrompt(b2)
		if err != nil {
			return nil, err
		}
		if prompt.Prompt == nil || prompt.Prompt.Name == "" {
			return nil, errors.New("Missing prompt definition")
		}
		prompts = append(prompts, prompt)
	}
	for _, prompt := range prompts {
		if err := safely("Prompt ["+prompt.Prompt.Name+"]", func() { m.server.AddPrompt(prompt.Prompt, prompt.Handle) }); err != nil {
			return nil, err
		}
		prompt.Server = m
		prompt.SetName(prompt.Prompt.Name)
		prompt.BuildLabel()
		m.lock.Lock()
		m.Prompts[prompt.Prompt.Name] = prompt
		m.lock.Unlock()
		if m.ps != nil {
			m.ps.addComponentToAll(prompt, m.Name)
		}
		names = append(names, prompt.Name)
	}
	return names, nil
}

func (m *MCPServer) AddResources(b []byte) ([]string, error) {
	arr := util.ToJSONArray(b)
	resources := []*MCPResource{}
	names := []string{}
	for _, b2 := range arr {
		resource, err := ParseResource(b2)
		if err != nil {
			return nil, err
		}
		if resource == nil || resource.Resource == nil {
			return nil, errors.New("Missing resource definition")
		}
		if _, err := url.Parse(resource.Resource.URI); err != nil {
			return nil, fmt.Errorf("Resource [%s] has an invalid URI: %s", resource.Resource.Name, err.Error())
		}
		resources = append(resources, resource)
	}
	for _, resource := range resources {
		if err := safely("Resource ["+resource.Resource.Name+"]", func() { m.server.AddResource(resource.Resource, resource.Handle) }); err != nil {
			return nil, err
		}
		resource.Server = m
		resource.SetName(resource.Resource.Name)
		resource.BuildLabel()
		m.lock.Lock()
		m.Resources[resource.Resource.Name] = resource
		m.lock.Unlock()
		if m.ps != nil {
			m.ps.addComponentToAll(resource, m.Name)
		}
		names = append(names, resource.Name)
	}
	return names, nil
}

func (m *MCPServer) AddResourceTemplates(b []byte) ([]string, error) {
	arr := util.ToJSONArray(b)
	templates := []*MCPResourceTemplate{}
	names := []string{}
	for _, b2 := range arr {
		template, err := ParseResourceTemplate(b2)
		if err != nil {
			return nil, err
		}
		if template == nil || template.ResourceTemplate == nil {
			return nil, errors.New("Missing resource template definition")
		}
		templates = append(templates, template)
	}
	for _, template := range templates {
		if err := safely("Resource template ["+template.ResourceTemplate.Name+"]", func() { m.server.AddResourceTemplate(template.ResourceTemplate, template.Handle) }); err != nil {
			return nil, err
		}
		template.Server = m
		template.SetName(template.ResourceTemplate.Name)
		template.BuildLabel()
		m.lock.Lock()
		m.ResourceTemplates[template.ResourceTemplate.Name] = template
		m.lock.Unlock()
		if m.ps != nil {
			m.ps.addComponentToAll(template, m.Name)
		}
		names = append(names, template.Name)
	}
	return names, nil
}

func GetComponentType(kind string) (isTools, isPrompts, isResources, isTemplates bool) {
	switch kind {
	case KindTools:
		isTools = true
	case KindPrompts:
		isPrompts = true
	case KindResources:
		isResources = true
	case KindTemplates:
		isTemplates = true
	}
	return
}

// GetAllComponents gives a copy of the components of the kind of all servers, as they are while servers are changed.
func GetAllComponents(kind string) map[string]map[string]IMCPComponent {
	lock.RLock()
	defer lock.RUnlock()
	byName := AllComponents[kind]
	out := make(map[string]map[string]IMCPComponent, len(byName))
	for name, byServer := range byName {
		out[name] = maps.Clone(byServer)
	}
	return out
}

func AddToolSchemas(b []byte) ([]string, error) {
	toolSchemas := []*ToolSchema{}
	if err := util.ReadJsonOrYamlPayloadFromBytes(b, &toolSchemas); err != nil {
		log.Println(string(b))
		return nil, err
	}
	names := []string{}
	lock.Lock()
	defer lock.Unlock()
	for _, ts := range toolSchemas {
		ToolSchemas[ts.Name] = ts.InputSchema
		names = append(names, ts.Name)
	}
	return names, nil
}

func (m *MCPServer) onInitialized(ctx context.Context, req *gomcp.InitializedRequest) {
	msg := fmt.Sprintf("MCPServer[%d][%s]: Initialized.", m.Port, m.Name)
	// params := &gomcp.ProgressNotificationParams{
	// 	ProgressToken: req.Params.Meta.GetMeta()["progressToken"],
	// 	Total:         float64(0),
	// 	Progress:      0,
	// 	Message:       msg,
	// }
	// req.Session.NotifyProgress(ctx, params)
	log.Println(msg)
}

// requestHeaders gives the headers that the request came with, if it came with any.
func requestHeaders(extra *gomcp.RequestExtra) http.Header {
	if extra == nil {
		return nil
	}
	return extra.Header
}

func (m *MCPServer) onRootsListChanged(ctx context.Context, req *gomcp.RootsListChangedRequest) {
	msg := fmt.Sprintf("MCPServer[%d][%s]: Roots List Changed: [%s]", m.Port, m.Name, util.ToJSONText(requestHeaders(req.Extra)))
	log.Println(msg)
	notifyClient(ctx, req.Params, msg, req.Session, nil)
}

func (m *MCPServer) onProgressNotification(ctx context.Context, req *gomcp.ProgressNotificationServerRequest) {
	msg := fmt.Sprintf("MCPServer[%d][%s]: Progress Notification Received: [%s]", m.Port, m.Name, util.ToJSONText(requestHeaders(req.Extra)))
	log.Println(msg)
	notifyClient(ctx, req.Params, msg, req.Session, nil)
}

func (m *MCPServer) AddCompletionPayload(completionType string, b []byte, delayMin, delayMax time.Duration, delayCount int) int {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.CompletionPayload[completionType] = payload.NewStreamTextPayload(nil, b, 0, delayMin, delayMax, delayCount)
	return len(m.CompletionPayload[completionType].TextStream)
}

func (m *MCPServer) onCompletion(ctx context.Context, req *gomcp.CompleteRequest) (*gomcp.CompleteResult, error) {
	msg := fmt.Sprintf("MCPServer[%d][%s]: Completion Request Received: [%s]", m.Port, m.Name, util.ToJSONText(requestHeaders(req.Extra)))
	log.Println(msg)
	notifyClient(ctx, req.Params, msg, req.Session, nil)
	var completion *payload.Payload
	if req.Params != nil && req.Params.Ref != nil {
		m.lock.RLock()
		completion = m.CompletionPayload[req.Params.Ref.Type]
		m.lock.RUnlock()
	}
	suggestions := []string{}
	if completion != nil {
		completion.RangeTextWithDelay(0, nil, func(s string, count int, restarted bool) (bool, error) {
			suggestions = append(suggestions, s)
			return true, nil
		})
	} else {
		suggestions = append(suggestions, "<No Completion>")
	}
	return &gomcp.CompleteResult{
		Completion: gomcp.CompletionResultDetails{
			HasMore: false,
			Total:   len(suggestions),
			Values:  suggestions,
		},
	}, nil
}

func clientInfoOf(session *gomcp.ServerSession) *gomcp.Implementation {
	if session != nil {
		if params := session.InitializeParams(); params != nil {
			return params.ClientInfo
		}
	}
	return nil
}

func (m *MCPServer) onSubscribed(ctx context.Context, req *gomcp.SubscribeRequest) error {
	msg := fmt.Sprintf("MCPServer[%d][%s]: Session [%s] Client [%+v] Subscribed to [%s]", m.Port, m.Name, req.Session.ID(), clientInfoOf(req.Session), req.Params.URI)
	log.Println(msg)
	notifyClient(ctx, req.Params, msg, req.Session, nil)
	return nil
}

func (m *MCPServer) onUnsubscribed(ctx context.Context, req *gomcp.UnsubscribeRequest) error {
	msg := fmt.Sprintf("MCPServer[%d][%s]: Session [%s] Client [%+v] Unsubscribed to [%s]", m.Port, m.Name, req.Session.ID(), clientInfoOf(req.Session), req.Params.URI)
	log.Println(msg)
	notifyClient(ctx, req.Params, msg, req.Session, nil)
	return nil
}

func (m *MCPServer) Middleware(next gomcp.MethodHandler) gomcp.MethodHandler {
	return func(ctx context.Context, method string, req gomcp.Request) (result gomcp.Result, err error) {
		if method == "ping" {
			log.Println("PING: MCP Ping handled")
			return next(ctx, method, req)
		}
		log.Println("------ MCP ------")
		if strings.HasPrefix(method, "tools/") || strings.HasPrefix(method, "prompts/") || strings.HasPrefix(method, "resource") {
			log.Printf("===================== *** Tools/Prompts/Resources Call [Port: %d] *** ========================\n", m.Port)
		}
		session := req.GetSession()
		callToolParams, isToolCall := req.GetParams().(*gomcp.CallToolParamsRaw)
		isToolCall = isToolCall && callToolParams != nil
		var duration time.Duration
		var toolName string
		if isToolCall {
			start := time.Now()
			toolName = callToolParams.Name
			TrackToolCall(m.Port, m.Name, session.ID(), toolName)
			log.Printf("MCPServer[%d][%s]: Session [%s] Method [%s] Tool [%s] Params: [%s]", m.Port, m.Name, session.ID(), method, toolName, util.ToJSONText(callToolParams))
			result, err = next(ctx, method, req)
			duration = time.Since(start)
		} else {
			result, err = next(ctx, method, req)
		}
		msg := ""
		if err != nil {
			msg = fmt.Sprintf("MCPServer[%d][%s]: Session [%s] Method [%s] Tool [%s] call finished in [%s] with error [%s]",
				m.Port, m.Name, session.ID(), method, toolName, duration.String(), err.Error())
		} else {
			msg = fmt.Sprintf("MCPServer[%d][%s]: Session [%s] Method [%s] Tool [%s] call finished in [%s]",
				m.Port, m.Name, session.ID(), method, toolName, duration.String())
		}
		if isToolCall {
			TrackToolCallResult(m.Port, m.Name, toolName, duration, err == nil)
		}
		log.Println(msg)
		log.Println("----- End MCP -----")
		return result, err
	}
}
