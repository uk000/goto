package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"goto/pkg/ai/mcp"
	"goto/pkg/global"
	"goto/pkg/proxy"
	"goto/pkg/server/listeners"
	"goto/pkg/server/middleware"
	"goto/pkg/server/response/payload"
	"goto/pkg/util"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

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
	SSE               bool                            `json:"sse"`
	Stateless         bool                            `json:"stateless"`
	Enabled           bool                            `json:"enabled"`
	server            *gomcp.Server
	handler           *gomcp.StreamableHTTPHandler
	sseHandler        *gomcp.SSEHandler
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
	SSE          bool          `json:"sse,omitempty"`
	Enabled      bool          `json:"enabled,omitempty"`
}

type PortServers struct {
	Port          int                                                `json:"port"`
	Servers       map[string]*MCPServer                              `json:"servers"`
	DefaultServer *MCPServer                                         `json:"defaultServer,omitempty"`
	AllComponents map[string]map[string]map[string]mcp.IMCPComponent `json:"allComponents"`
	lock          sync.RWMutex
}

const (
	KindTools     = "tools"
	KindPrompts   = "prompts"
	KindResources = "resources"
	KindTemplates = "templates"
)

var (
	DefaultServer *MCPServer
	PortsServers  = map[int]*PortServers{}
	AllComponents = map[string]map[string]map[string]mcp.IMCPComponent{}
	Kinds         = []string{KindTools, KindPrompts, KindResources, KindTemplates}
	Middleware    = middleware.NewMiddleware("mcpserver", nil, MCPServerFunc)
	lock          sync.RWMutex
)

func init() {
	for _, kind := range Kinds {
		AllComponents[kind] = map[string]map[string]mcp.IMCPComponent{}
	}
}

func initDefaultServer() {
	DefaultServer = NewMCPServer(&MCPServerPayload{Port: global.Self.ServerPort, Name: "default", Version: "1.0", Description: "Default Server", Instructions: "Default Instructions"})
	DefaultServer.handler = gomcp.NewStreamableHTTPHandler(func(r *http.Request) *gomcp.Server {
		return DefaultServer.server
	}, &gomcp.StreamableHTTPOptions{Stateless: true})
	DefaultServer.sseHandler = gomcp.NewSSEHandler(func(r *http.Request) *gomcp.Server {
		return DefaultServer.server
	})
}

func GetMCPServerNames() map[int]map[string][]string {
	lock.RLock()
	defer lock.RUnlock()
	byPort := map[int]map[string][]string{}
	for port, servers := range PortsServers {
		byPort[port] = map[string][]string{}
		for serverName, server := range servers.Servers {
			byPort[port][serverName] = []string{}
			for _, tool := range server.Tools {
				byPort[port][serverName] = append(byPort[port][server.Name], tool.Tool.Name)
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

func NewPortMCPServers(port int) *PortServers {
	ps := &PortServers{
		Port:          port,
		Servers:       map[string]*MCPServer{},
		AllComponents: map[string]map[string]map[string]mcp.IMCPComponent{},
	}
	for _, kind := range Kinds {
		ps.AllComponents[kind] = map[string]map[string]mcp.IMCPComponent{}
	}
	return ps
}

func GetPortDefaultMCPServer(port int) *MCPServer {
	ps := GetPortMCPServers(port)
	if len(ps.Servers) > 0 {
		return ps.DefaultServer
	}
	if DefaultServer == nil {
		initDefaultServer()
	}
	return DefaultServer
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
	AllComponents = map[string]map[string]map[string]mcp.IMCPComponent{}
}

func (ps *PortServers) AddMCPServer(p *MCPServerPayload) {
	ps.lock.Lock()
	defer ps.lock.Unlock()
	s := NewMCPServer(p)
	ps.Servers[p.Name] = s
	if ps.DefaultServer == nil {
		ps.DefaultServer = s
	}
}

func NewMCPServer(p *MCPServerPayload) *MCPServer {
	mcpserver := &MCPServer{
		ID:        fmt.Sprintf("Goto-%s[%s]", p.Name, global.Funcs.GetListenerLabelForPort(p.Port)),
		Host:      global.Funcs.GetHostLabelForPort(p.Port),
		Port:      p.Port,
		SSE:       p.SSE,
		Stateless: p.Stateless,
		Implementation: gomcp.Implementation{
			Name:    p.Name,
			Title:   p.Name,
			Version: p.Version,
		},
		Tools:             map[string]*MCPTool{},
		Prompts:           map[string]*MCPPrompt{},
		Resources:         map[string]*MCPResource{},
		ResourceTemplates: map[string]*MCPResourceTemplate{},
		CompletionPayload: map[string]*payload.Payload{},
	}
	mcpserver.server = gomcp.NewServer(&mcpserver.Implementation, &gomcp.ServerOptions{
		Instructions:                p.Instructions,
		PageSize:                    p.PageSize,
		KeepAlive:                   p.KeepAlive,
		InitializedHandler:          mcpserver.onInitialized,
		RootsListChangedHandler:     mcpserver.onRootsListChanged,
		ProgressNotificationHandler: mcpserver.onProgressNotification,
		CompletionHandler:           mcpserver.onCompletion,
		SubscribeHandler:            mcpserver.onSubscribed,
		UnsubscribeHandler:          mcpserver.onUnsubscribed,
		HasPrompts:                  p.HasPrompts,
		HasResources:                p.HasResources,
		HasTools:                    p.HasTools,
	})
	mcpserver.handler = gomcp.NewStreamableHTTPHandler(func(r *http.Request) *gomcp.Server {
		return mcpserver.server
	}, &gomcp.StreamableHTTPOptions{Stateless: p.Stateless})
	mcpserver.sseHandler = gomcp.NewSSEHandler(func(r *http.Request) *gomcp.Server {
		return mcpserver.server
	})
	mcpserver.server.AddReceivingMiddleware(mcpserver.Middleware)
	return mcpserver
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

func GetAllComponents(kind string) map[string]map[string]mcp.IMCPComponent {
	lock.RLock()
	defer lock.RUnlock()
	return AllComponents[kind]
}

func (ps *PortServers) GetMCPServer(name string) *MCPServer {
	ps.lock.RLock()
	defer ps.lock.RUnlock()
	return ps.Servers[name]
}

func (ps *PortServers) Start() {
	ps.lock.Lock()
	defer ps.lock.Unlock()
	for _, s := range ps.Servers {
		s.Enabled = true
	}
}

func (ps *PortServers) Stop() {
	ps.lock.Lock()
	defer ps.lock.Unlock()
	for _, s := range ps.Servers {
		s.Enabled = false
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

func (ps *PortServers) AddComponents(server, kind string, b []byte) (count int, err error) {
	ps.lock.RLock()
	s := ps.DefaultServer
	if server != "" {
		s = ps.Servers[server]
	}
	ps.lock.RUnlock()
	if s != nil {
		switch kind {
		case KindTools:
			count, err = s.AddTools(b, ps)
		case KindPrompts:
			count, err = s.AddPrompts(b, ps)
		case KindResources:
			count, err = s.AddResources(b, ps)
		case KindTemplates:
			count, err = s.AddResourceTemplates(b, ps)
		}
		if err == nil {
			return count, nil
		} else {
			return 0, err
		}
	} else {
		return 0, errors.New("server not found")
	}
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

func (m *MCPServer) getComponent(name, kind string) mcp.IMCPComponent {
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
	c := m.getComponent(name, kind)
	if c != nil {
		c.SetPayload(payload, isJSON, isStream, streamCount, delayMin, delayMax, delayCount)
		return nil
	}
	return errors.New("component not found")
}

func (ps *PortServers) addComponentToAll(c mcp.IMCPComponent, server string) {
	ps.lock.Lock()
	kind := c.GetKind()
	name := c.GetName()
	if ps.AllComponents[kind][name] == nil {
		ps.AllComponents[kind][name] = map[string]mcp.IMCPComponent{}
	}
	ps.AllComponents[kind][name][server] = c
	ps.lock.Unlock()
	lock.Lock()
	if AllComponents[kind][name] == nil {
		AllComponents[kind][name] = map[string]mcp.IMCPComponent{}
	}
	AllComponents[kind][name][server] = c
	lock.Unlock()
}

func (m *MCPServer) AddTools(b []byte, ps *PortServers) (int, error) {
	arr := util.ToJSONArray(b)
	tools := []*MCPTool{}
	for _, b2 := range arr {
		tool, err := ParseTool(b2)
		if err != nil {
			return 0, err
		}
		if tool.IsProxy {
			proxy.GetMCPProxyForPort(m.Port).SetupMCPProxy(m.Name, tool.Config.Remote.URL, "", tool.Tool.Name, tool.Tool.Name, nil)
		}
		m.server.AddTool(tool.Tool, tool.Handle)
		tool.Server = m
		tool.SetName(tool.Tool.Name)
		tool.BuildLabel()
		tools = append(tools, tool)
	}
	for _, tool := range tools {
		m.lock.Lock()
		m.Tools[tool.Tool.Name] = tool
		m.lock.Unlock()
		ps.addComponentToAll(tool, m.Name)
	}
	return len(tools), nil
}

func (m *MCPServer) AddPrompts(b []byte, ps *PortServers) (int, error) {
	arr := util.ToJSONArray(b)
	prompts := []*MCPPrompt{}
	for _, b2 := range arr {
		prompt, err := ParsePrompt(b2)
		if err != nil {
			return 0, err
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
		ps.addComponentToAll(prompt, m.Name)
	}
	return len(prompts), nil
}

func (m *MCPServer) AddResources(b []byte, ps *PortServers) (int, error) {
	arr := util.ToJSONArray(b)
	resources := []*MCPResource{}
	for _, b2 := range arr {
		resource, err := ParseResource(b2)
		if err != nil {
			return 0, err
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
		ps.addComponentToAll(resource, m.Name)
	}
	return len(resources), nil
}

func (m *MCPServer) AddResourceTemplates(b []byte, ps *PortServers) (int, error) {
	arr := util.ToJSONArray(b)
	templates := []*MCPResourceTemplate{}
	for _, b2 := range arr {
		template, err := ParseResourceTemplate(b2)
		if err != nil {
			return 0, err
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
		ps.addComponentToAll(template, m.Name)
	}
	return len(templates), nil
}

func (m *MCPServer) onInitialized(ctx context.Context, req *gomcp.InitializedRequest) {
	ip := req.Session.InitializeParams()
	log.Printf("MCPServer[%d][%s]: Session [%s] Client [%+v] Protocol Version [%s] Capabilities: [%+v] Initialized",
		m.Port, m.Name, req.Session.ID(), ip.ClientInfo, ip.ProtocolVersion, ip.Capabilities)
}

func (m *MCPServer) onRootsListChanged(ctx context.Context, req *gomcp.RootsListChangedRequest) {
	log.Printf("MCPServer[%d][%s]: Session [%s] Client [%+v] Roots List Changed",
		m.Port, m.Name, req.Session.ID(), req.Session.InitializeParams().ClientInfo)
}

func (m *MCPServer) onProgressNotification(ctx context.Context, req *gomcp.ProgressNotificationServerRequest) {
	//called when "notifications/progress" is received.
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
		payload.RangeText(func(s string) {
			suggestions = append(suggestions, s)
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

func (m *MCPServer) Serve(w http.ResponseWriter, r *http.Request) {
	r = r.WithContext(util.SetHTTPRW(r.Context(), r, w))
	isSSE := strings.Contains(r.RequestURI, "/sse") || r.Header.Get("Sec-Fetch-Mode") != ""
	isMCP := strings.Contains(r.RequestURI, "/mcp") && !strings.Contains(r.RequestURI, "/mcp/sse")
	if isSSE && !isMCP {
		r = r.WithContext(util.SetSSE(r.Context()))
		util.AddLogMessage("Handling MCP SSE Request", r)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Cache-Control")
		m.sseHandler.ServeHTTP(w, r)
	} else {
		util.AddLogMessage("Handling MCP Request", r)
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		m.handler.ServeHTTP(w, r)
	}
}

func (m *MCPServer) Middleware(next gomcp.MethodHandler) gomcp.MethodHandler {
	return func(ctx context.Context, method string, req gomcp.Request) (result gomcp.Result, err error) {
		if method == "ping" {
			log.Println("PING: MCP Ping handled")
			return next(ctx, method, req)
		}
		log.Println("===================== *** MCP *** ========================")
		start := time.Now()
		session := req.GetSession()
		callToolParams, ctOk := req.GetParams().(*gomcp.CallToolParams)
		toolName := ""
		if ctOk && callToolParams != nil {
			toolName = callToolParams.Name
			TrackToolCall(m.Port, m.Name, session.ID(), toolName)
		}
		log.Printf("MCPServer[%d][%s]: Session [%s] Method [%s] Tool [%s] Params: [%s]", m.Port, m.Name, session.ID(), method, toolName, util.ToJSONText(req))
		result, err = next(ctx, method, req)
		duration := time.Since(start)
		msg := ""
		if err != nil {
			msg = fmt.Sprintf("MCPServer[%d][%s]: Session [%s] Method [%s] Tool [%s] call finished in [%s] with error [%s]", m.Port, m.Name, session.ID(), method, toolName, duration.String(), err.Error())
			TrackToolCallResult(m.Port, m.Name, toolName, duration, false)
		} else {
			msg = fmt.Sprintf("MCPServer[%d][%s]: Session [%s] Method [%s] Tool [%s] call finished in [%s] with result [%s]", m.Port, m.Name, session.ID(), method, toolName, duration.String(), util.ToJSONText(result))
			TrackToolCallResult(m.Port, m.Name, toolName, duration, true)
		}
		log.Println(msg)
		log.Println("===================== *** End MCP *** ========================")
		return result, err
	}
}

func MCPServerFunc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		HandleMCP(w, r)
		rs := util.GetRequestStore(r)
		if !rs.RequestServed && next != nil {
			next.ServeHTTP(w, r)
		}
	})
}

func HandleMCP(w http.ResponseWriter, r *http.Request) {
	l := listeners.GetCurrentListener(r)
	rs := util.GetRequestStore(r)
	isMCP := l.IsMCP || rs.IsMCP
	if isMCP && !rs.IsAdminRequest {
		log.Println("---------------- *** Pre-MCP *** ----------------")
		if proxy.WillProxyMCP(l.Port, r) {
			log.Printf("MCP is configured to proxy on Port [%d]. Skipping MCP processing", l.Port)
			return
		}
		port, serverName := getPortAndMCPServerNameFromURI(r.RequestURI)
		if port == 0 {
			port = l.Port
		}
		ps := PortsServers[port]
		if ps != nil {
			server := ps.Servers[serverName]
			if server == nil {
				server = ps.DefaultServer
				if server != nil {
					if server.Enabled {
						if serverName == "" {
							log.Printf("No MCP Server name was given, using PortDefault server [%s] on port [%d]", server.Name, port)
						} else {
							log.Printf("MCP Server [%s] not found on port [%d], using PortDefault server [%s]", serverName, port, server.Name)
						}
					} else {
						log.Printf("MCP Server [%s] not found on port [%d], and PortDefault server is disabled.", serverName, port)
					}
				} else {
					log.Printf("No MCP Servers present on port [%d] to be used as PortDefault server", port)
				}
			}
			if server == nil || !server.Enabled {
				server = GetPortDefaultMCPServer(port)
				if server != nil {
					log.Printf("Falling back to Default MCP Server [%s] on port [%d]", server.Name, port)
				} else {
					log.Printf("Default MCP Server not configured on port [%d] either. Will fall back to HTTP handling", port)
				}
			}
			if server != nil {
				log.Printf("Server [%s] will handle MCP on port [%d]", server.Name, port)
				server.Serve(w, r)
				rs.RequestServed = true
			} else {
				log.Printf("No MCP Server handling port [%d]. Will route to HTTP server.", port)
			}
		}
		log.Println("---------------- *** Finish Pre-MCP *** ----------------")
	}
}

func getPortAndMCPServerNameFromURI(uri string) (port int, name string) {
	isMCP := strings.Contains(uri, "/mcp")
	isSSE := strings.Contains(uri, "/sse")
	if !isMCP && !isSSE {
		return
	}
	var parts []string
	if isSSE {
		parts = strings.Split(uri, "/sse")
	} else {
		parts = strings.Split(uri, "/mcp")
		if len(parts) > 1 && strings.HasPrefix(parts[1], "/sse") {
			subparts := strings.Split(parts[1], "/sse")
			if len(subparts) > 0 {
				parts[1] = subparts[0]
			}
		}
	}
	if len(parts) > 1 {
		subParts := strings.Split(parts[0], "=")
		if len(subParts) > 1 {
			port, _ = strconv.Atoi(subParts[1])
		}
		subParts = strings.Split(parts[1], "/")
		if len(subParts) > 1 {
			name = subParts[1]
		}
	}
	return
}
