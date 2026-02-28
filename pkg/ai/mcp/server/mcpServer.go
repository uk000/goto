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

package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"goto/pkg/global"
	"goto/pkg/proxy"
	"goto/pkg/server/middleware"
	"goto/pkg/server/response/payload"
	"goto/pkg/server/response/status"
	"goto/pkg/types"
	"goto/pkg/util"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type MCPServer struct {
	gomcp.Implementation
	ID                string                          `json:"id"`
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
	SessionID string
	Server    *MCPServer
	finished  chan bool
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
	KindTools          = "tools"
	KindPrompts        = "prompts"
	KindResources      = "resources"
	KindTemplates      = "templates"
	HeaderMCPSessionID = "Mcp-Session-Id"
)

var (
	DefaultStatelessServer *MCPServer
	DefaultStatefulServer  *MCPServer
	PortsServers           = map[int]*PortServers{}
	ServerRoutes           = map[string]*types.Pair[string, string]{}
	AllComponents          = map[string]map[string]map[string]IMCPComponent{}
	Kinds                  = []string{KindTools, KindPrompts, KindResources, KindTemplates}
	Middleware             = middleware.NewMiddleware("mcpserver", nil, nil)
	StatusManager          = status.NewStatusManager()
	lock                   sync.RWMutex
)

func init() {
	for _, kind := range Kinds {
		AllComponents[kind] = map[string]map[string]IMCPComponent{}
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
		ID:        fmt.Sprintf("[%s][%s]", global.Funcs.GetListenerLabelForPort(p.Port), p.Name),
		Enabled:   p.Enabled,
		Host:      global.Funcs.GetHostLabelForPort(p.Port),
		Port:      p.Port,
		Stateless: p.Stateless,
		URI:       p.URI,
		URIRegex:  p.URIRegex,
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
		server.uriRegexp = regexp.MustCompile(server.URIRegex)
	} else if server.URI != "" {
		server.uriRegexp = regexp.MustCompile(fmt.Sprintf("%s%s%s", util.URIPrefixRegexParts[0], server.URI, util.URIPrefixRegexParts[1]))
	}
	server.streamHTTPHandler = gomcp.NewStreamableHTTPHandler(getServer, &gomcp.StreamableHTTPOptions{Stateless: p.Stateless})
	server.sseHandler = gomcp.NewSSEHandler(getServer, &gomcp.SSEOptions{})
	server.handler = MCPHybridHandler(server)
	server.server.AddReceivingMiddleware(server.Middleware)
	server.AddTool(&MCPTool{
		Tool: &gomcp.Tool{
			Name:        "ServerDetails",
			Description: "Get Server Details for " + server.Name,
			InputSchema: &jsonschema.Schema{
				Type: "object",
			},
		},
		Behavior: ToolBehavior{ServerDetails: true},
	})
	server.AddTool(&MCPTool{
		Tool: &gomcp.Tool{
			Name:        "ServerPaths",
			Description: "List All Registered Server URIs",
			InputSchema: &jsonschema.Schema{
				Type: "object",
			},
		},
		Behavior: ToolBehavior{ServerPaths: true},
	})
	server.AddTool(&MCPTool{
		Tool: &gomcp.Tool{
			Name:        "ListServers",
			Description: "List All MCP Servers on the port",
			InputSchema: &jsonschema.Schema{
				Type: "object",
			},
		},
		Behavior: ToolBehavior{AllServers: true},
	})
	server.AddTool(&MCPTool{
		Tool: &gomcp.Tool{
			Name:        "ListComponents",
			Description: "List all registered Components from all servers",
			InputSchema: &jsonschema.Schema{
				Type: "object",
			},
		},
		Behavior: ToolBehavior{AllComponents: true},
	})
	return server
}

func SetServerRoute(uri string, server *MCPServer) {
	lock.Lock()
	defer lock.Unlock()
	uri = strings.ToLower(uri)
	if strings.Contains(uri, "/mcp") {
		parts := strings.Split(uri, "/mcp")
		server.SSEURI = parts[0] + "/mcp/sse" + parts[1]
	}
	pair := types.NewPair(server.Name, "")
	ServerRoutes[uri] = pair
	ServerRoutes[server.SSEURI] = pair
	for tool := range server.Tools {
		tool = strings.ToLower(tool)
		pair := types.NewPair(server.Name, tool)
		ServerRoutes[uri+"/"+tool] = pair
		ServerRoutes[server.SSEURI+"/"+tool] = pair
	}
}

func GetMCPServerNames() map[int]map[string][]string {
	lock.RLock()
	defer lock.RUnlock()
	byPort := map[int]map[string][]string{}
	for port, servers := range PortsServers {
		byPort[port] = map[string][]string{}
		for serverName, server := range servers.Servers {
			byPort[port][serverName] = make([]string, len(server.Tools))
			i := 0
			for _, tool := range server.Tools {
				byPort[port][serverName][i] = tool.Tool.Name
				i++
			}
		}
	}
	return byPort
}

func GetPortMCPServers(port int) *PortServers {
	lock.RLock()
	defer lock.RUnlock()
	if PortsServers[port] == nil {
		PortsServers[port] = NewPortMCPServers(port)
	}
	return PortsServers[port]
}

func GetMCPServer(name string) (server *MCPServer) {
	name = strings.ToLower(name)
	lock.RLock()
	defer lock.RUnlock()
	for _, ps := range PortsServers {
		server = ps.GetMCPServer(name)
		if server != nil {
			break
		}
	}
	return
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

func ClearAllMCPServers() {
	lock.Lock()
	defer lock.Unlock()
	PortsServers = map[int]*PortServers{}
	AllComponents = map[string]map[string]map[string]IMCPComponent{}
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

func GetAllComponents(kind string) map[string]map[string]IMCPComponent {
	lock.RLock()
	defer lock.RUnlock()
	return AllComponents[kind]
}

func (ps *PortServers) AllServers() map[int]*PortServers {
	return PortsServers
}

func (ps *PortServers) GetMCPServer(name string) *MCPServer {
	name = strings.ToLower(name)
	ps.lock.RLock()
	defer ps.lock.RUnlock()
	return ps.Servers[name]
}

func (ps *PortServers) Start(server string) {
	ps.lock.Lock()
	defer ps.lock.Unlock()
	for _, s := range ps.Servers {
		if server != "" {
			if strings.EqualFold(s.Name, server) {
				s.Enabled = true
				break
			}
		} else {
			s.Enabled = true
		}
	}
}

func (ps *PortServers) Stop(server string) {
	ps.lock.Lock()
	defer ps.lock.Unlock()
	for _, s := range ps.Servers {
		if server != "" {
			if strings.EqualFold(s.Name, server) {
				s.Enabled = false
				break
			}
		} else {
			s.Enabled = false
		}
	}
}

func (ps *PortServers) Clear() {
	ps.lock.Lock()
	defer ps.lock.Unlock()
	for _, s := range ps.Servers {
		s.Clear()
	}
}

func (ps *PortServers) GetComponents(kind string) any {
	ps.lock.RLock()
	defer ps.lock.RUnlock()
	return ps.AllComponents[kind]
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
	m.Tools = map[string]*MCPTool{}
	m.Prompts = map[string]*MCPPrompt{}
	m.Resources = map[string]*MCPResource{}
	m.ResourceTemplates = map[string]*MCPResourceTemplate{}
	m.CompletionPayload = map[string]*payload.Payload{}
	m.sessionContexts = map[string]*SessionContext{}
}

func (m *MCPServer) GetComponents(kind string) any {
	m.lock.RLock()
	defer m.lock.RUnlock()
	switch kind {
	case KindTools:
		return m.Tools
	case KindPrompts:
		return m.Prompts
	case KindResources:
		return m.Resources
	case KindTemplates:
		return m.ResourceTemplates
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

func (m *MCPServer) AddPayload(name, kind string, payload []byte, isJSON, isStream bool, streamCount int, delayMin, delayMax time.Duration, delayCount int) error {
	c := m.GetComponent(name, kind)
	if c != nil {
		c.SetPayload(payload, isJSON, isStream, streamCount, delayMin, delayMax, delayCount)
		return nil
	}
	return errors.New("component not found")
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
	arr := util.ToJSONArray(b)
	tools := []*MCPTool{}
	names := []string{}
	for _, b2 := range arr {
		tool, err := ParseTool(b2)
		if err != nil {
			return nil, err
		}
		if tool.IsProxy {
			proxy.GetMCPProxyForPort(m.Port).SetupMCPProxy(m.Name, tool.Config.RemoteTool.URL, "", tool.Tool.Name, tool.Tool.Name, nil)
		}
		m.AddTool(tool)
		tools = append(tools, tool)
		names = append(names, tool.Name)
	}
	//m.ps.defaultServer = m
	log.Printf("Server [%s] added Tools [%+v] ", m.Name, names)
	return names, nil
}

func (m *MCPServer) AddTool(tool *MCPTool) {
	m.server.AddTool(tool.Tool, tool.Handle)
	tool.Server = m
	tool.SetName(tool.Tool.Name)
	if tool.URI == "" {
		tool.URI = strings.ToLower("/" + tool.Name)
	}
	tool.BuildLabel()
	m.lock.Lock()
	m.Tools[tool.Tool.Name] = tool
	m.ToolsByURI[tool.URI] = tool
	pair := types.NewPair(m.Name, tool.Name)
	ServerRoutes[m.URI+tool.URI] = pair
	ServerRoutes[m.SSEURI+tool.URI] = pair
	m.lock.Unlock()
	if m.ps != nil {
		m.ps.addComponentToAll(tool, m.Name)
	}
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
		m.server.AddPrompt(prompt.Prompt, prompt.Handle)
		prompt.Server = m
		prompt.SetName(prompt.Prompt.Name)
		prompt.BuildLabel()
		prompts = append(prompts, prompt)
	}
	for _, prompt := range prompts {
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
		m.server.AddResource(resource.Resource, resource.Handle)
		resource.Server = m
		resource.SetName(resource.Resource.Name)
		resource.BuildLabel()
		resources = append(resources, resource)
	}
	for _, resource := range resources {
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
		m.server.AddResourceTemplate(template.ResourceTemplate, template.Handle)
		template.Server = m
		template.SetName(template.ResourceTemplate.Name)
		template.BuildLabel()
		templates = append(templates, template)
	}
	for _, template := range templates {
		m.lock.Lock()
		m.ResourceTemplates[template.ResourceTemplate.Name] = template
		m.lock.Unlock()
		if m.ps != nil {
			m.ps.addComponentToAll(template, m.Name)
			names = append(names, template.Name)
		}
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

func (m *MCPServer) onRootsListChanged(ctx context.Context, req *gomcp.RootsListChangedRequest) {
	log.Printf("MCPServer[%d][%s]: Roots List Changed: [%s]", m.Port, m.Name, util.ToJSONText(req.Params))
}

func (m *MCPServer) onProgressNotification(ctx context.Context, req *gomcp.ProgressNotificationServerRequest) {
	log.Printf("MCPServer[%d][%s]: Progress Notification Received: [%s]", m.Port, m.Name, util.ToJSONText(req.Params))
}

func (m *MCPServer) AddCompletionPayload(completionType string, b []byte, delayMin, delayMax time.Duration, delayCount int) int {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.CompletionPayload[completionType] = payload.NewStreamTextPayload(nil, b, 0, delayMin, delayMax, delayCount)
	return len(m.CompletionPayload[completionType].TextStream)
}

func (m *MCPServer) onCompletion(ctx context.Context, req *gomcp.CompleteRequest) (*gomcp.CompleteResult, error) {
	payload := m.CompletionPayload[req.Params.Ref.Type]
	suggestions := []string{}
	if payload != nil {
		payload.RangeTextWithDelay(func(s string, count int, restarted bool) error {
			suggestions = append(suggestions, s)
			return nil
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

func (m *MCPServer) onSubscribed(ctx context.Context, req *gomcp.SubscribeRequest) error {
	log.Printf("MCPServer[%d][%s]: Session [%s] Client [%+v] Subscribed to [%s]", m.Port, m.Name, req.Session.ID(), req.Session.InitializeParams().ClientInfo, req.Params.URI)
	return nil
}

func (m *MCPServer) onUnsubscribed(ctx context.Context, req *gomcp.UnsubscribeRequest) error {
	log.Printf("MCPServer[%d][%s]: Session [%s] Client [%+v] Unsubscribed to [%s]", m.Port, m.Name, req.Session.ID(), req.Session.InitializeParams().ClientInfo, req.Params.URI)
	return nil
}

func (m *MCPServer) getSessionContext(sessionID string) *SessionContext {
	m.lock.Lock()
	defer m.lock.Unlock()
	return m.sessionContexts[sessionID]
}

func (m *MCPServer) removeSessionContext(sessionID string) {
	m.lock.Lock()
	defer m.lock.Unlock()
	delete(m.sessionContexts, sessionID)
}

func (m *MCPServer) setSessionContext(sessionID string, ctx *SessionContext) {
	m.lock.Lock()
	m.sessionContexts[sessionID] = ctx
	m.lock.Unlock()
}

func (m *MCPServer) getOrSetSessionContext(r *http.Request) (session *SessionContext) {
	sessionID := r.Header.Get(HeaderMCPSessionID)
	if sessionID != "" {
		session = m.getSessionContext(sessionID)
		if session == nil {
			session = &SessionContext{SessionID: sessionID, Server: m, finished: make(chan bool, 10)}
			m.setSessionContext(sessionID, session)
		} else {
			session.Server = m
		}
	}
	return
}

func (m *MCPServer) Middleware(next gomcp.MethodHandler) gomcp.MethodHandler {
	return func(ctx context.Context, method string, req gomcp.Request) (result gomcp.Result, err error) {
		if method == "ping" {
			log.Println("PING: MCP Ping handled")
			return next(ctx, method, req)
		}
		log.Println("------ MCP ------")
		if strings.HasPrefix(method, "tools/") || strings.HasPrefix(method, "prompts/") || strings.HasPrefix(method, "resource") {
			log.Println("===================== *** Tools/Prompts/Resources Call *** ========================")
		}
		session := req.GetSession()
		callToolParams, ctOk := req.GetParams().(*gomcp.CallToolParams)
		var duration time.Duration
		var toolName string
		if ctOk && callToolParams != nil {
			start := time.Now()
			toolName := callToolParams.Name
			TrackToolCall(m.Port, m.Name, session.ID(), toolName)
			log.Printf("MCPServer[%d][%s]: Session [%s] Method [%s] Tool [%s] Params: [%s]", m.Port, m.Name, session.ID(), method, toolName, util.ToJSONText(req))
			result, err = next(ctx, method, req)
			duration = time.Since(start)
		} else {
			result, err = next(ctx, method, req)
		}
		msg := ""
		if err != nil {
			msg = fmt.Sprintf("MCPServer[%d][%s]: Session [%s] Method [%s] Tool [%s] call finished in [%s] with error [%s]",
				m.Port, m.Name, session.ID(), method, toolName, duration.String(), err.Error())
			TrackToolCallResult(m.Port, m.Name, toolName, duration, false)
		} else {
			msg = fmt.Sprintf("MCPServer[%d][%s]: Session [%s] Method [%s] Tool [%s] call finished in [%s]",
				m.Port, m.Name, session.ID(), method, toolName, duration.String())
			TrackToolCallResult(m.Port, m.Name, toolName, duration, true)
		}
		log.Println(msg)
		log.Println("----- End MCP -----")
		return result, err
	}
}
