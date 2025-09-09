package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"goto/pkg/global"
	"goto/pkg/proxy"
	"goto/pkg/server/conn"
	"goto/pkg/server/listeners"
	"goto/pkg/server/middleware"
	"goto/pkg/server/response/payload"
	"goto/pkg/util"
	"log"
	"net/http"
	"regexp"
	"strconv"
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
	Server   *MCPServer
	RS       *util.RequestStore
	finished chan bool
}

type PortServers struct {
	Port          int                                            `json:"port"`
	Servers       map[string]*MCPServer                          `json:"servers"`
	DefaultServer *MCPServer                                     `json:"defaultServer,omitempty"`
	AllComponents map[string]map[string]map[string]IMCPComponent `json:"allComponents"`
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
	ServerRoutes           = map[string]*MCPServer{}
	AllComponents          = map[string]map[string]map[string]IMCPComponent{}
	Kinds                  = []string{KindTools, KindPrompts, KindResources, KindTemplates}
	Middleware             = middleware.NewMiddleware("mcpserver", nil, nil)
	lock                   sync.RWMutex
)

func init() {
	for _, kind := range Kinds {
		AllComponents[kind] = map[string]map[string]IMCPComponent{}
	}
}

func InitDefaultServer() {
	p := &MCPServerPayload{
		Port:         global.Self.MCPPort,
		Name:         "default-stateless",
		Version:      "1.0",
		Description:  "Default Stateless Server",
		Instructions: "Default Stateless Instructions",
		URI:          "/mcp/default/stateless",
		Stateless:    true,
		Enabled:      true,
	}
	DefaultStatelessServer = GetPortMCPServers(p.Port).AddMCPServer(p)
	DefaultStatelessServer.sseHandler = gomcp.NewSSEHandler(func(r *http.Request) *gomcp.Server { return DefaultStatelessServer.server })
	DefaultStatelessServer.streamHTTPHandler = gomcp.NewStreamableHTTPHandler(func(r *http.Request) *gomcp.Server { return DefaultStatelessServer.server }, &gomcp.StreamableHTTPOptions{Stateless: true})
	DefaultStatelessServer.handler = MCPHybridHandler(DefaultStatelessServer)

	p2 := &MCPServerPayload{
		Port:         global.Self.MCPPort,
		Name:         "default-stateful",
		Version:      "1.0",
		Description:  "Default Stateful Server",
		Instructions: "Default Stateful Instructions",
		URI:          "/mcp/default/stateful",
		Stateless:    false,
		Enabled:      true,
	}
	DefaultStatefulServer = GetPortMCPServers(p2.Port).AddMCPServer(p2)
	DefaultStatefulServer.sseHandler = gomcp.NewSSEHandler(func(r *http.Request) *gomcp.Server { return DefaultStatefulServer.server })
	DefaultStatefulServer.streamHTTPHandler = gomcp.NewStreamableHTTPHandler(func(r *http.Request) *gomcp.Server { return DefaultStatefulServer.server }, &gomcp.StreamableHTTPOptions{Stateless: false})
	DefaultStatefulServer.handler = MCPHybridHandler(DefaultStatefulServer)

}

func NewMCPServer(p *MCPServerPayload) *MCPServer {
	server := &MCPServer{
		ID:        fmt.Sprintf("%s[%s]", p.Name, global.Funcs.GetListenerLabelForPort(p.Port)),
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
	server.sseHandler = gomcp.NewSSEHandler(getServer)
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
			Name:        "AllServers",
			Description: "List All MCP Servers on the port",
			InputSchema: &jsonschema.Schema{
				Type: "object",
			},
		},
		Behavior: ToolBehavior{AllServers: true},
	})
	server.AddTool(&MCPTool{
		Tool: &gomcp.Tool{
			Name:        "AllComponents",
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
	sseURI := ""
	if strings.Contains(uri, "/mcp") {
		parts := strings.Split(uri, "/mcp")
		sseURI = parts[0] + "/mcp/sse" + parts[1]
	}
	ServerRoutes[uri] = server
	ServerRoutes[sseURI] = server
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
		return ps.DefaultServer
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
	ps.DefaultServer = s
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

func (m *MCPServer) getComponent(name, kind string) IMCPComponent {
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
			proxy.GetMCPProxyForPort(m.Port).SetupMCPProxy(m.Name, tool.Config.Remote.URL, "", tool.Tool.Name, tool.Tool.Name, nil)
		}
		m.AddTool(tool)
		tools = append(tools, tool)
		names = append(names, tool.Name)
	}
	m.ps.DefaultServer = m
	log.Printf("Server [%s] added Tools [%+v] ", m.Name, names)
	return names, nil
}

func (m *MCPServer) AddTool(tool *MCPTool) {
	m.server.AddTool(tool.Tool, tool.Handle)
	tool.Server = m
	tool.SetName(tool.Tool.Name)
	tool.BuildLabel()
	m.lock.Lock()
	m.Tools[tool.Tool.Name] = tool
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
	log.Printf("MCPServer[%d][%s]: Initialized: [%s]", m.Port, m.Name, util.ToJSONText(req.Params))
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

func (m *MCPServer) GetSessionContext(sessionID string) *SessionContext {
	log.Println("GetSessionContext")
	m.lock.Lock()
	defer m.lock.Unlock()
	return m.sessionContexts[sessionID]
}

func (m *MCPServer) GetAndClearSessionContext(sessionID string) *SessionContext {
	log.Println("GetAndClearSessionContext")
	m.lock.Lock()
	defer m.lock.Unlock()
	sc := m.sessionContexts[sessionID]
	delete(m.sessionContexts, sessionID)
	return sc
}

func (m *MCPServer) SetSessionContext(sessionID string, ctx *SessionContext) {
	log.Println("SetSessionContext")
	m.lock.Lock()
	m.sessionContexts[sessionID] = ctx
	m.lock.Unlock()
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

func (m *MCPServer) getOrSetSessionContext(r *http.Request) (session *SessionContext) {
	sessionID := r.Header.Get(HeaderMCPSessionID)
	rs := util.GetRequestStore(r)
	if sessionID != "" {
		session = m.GetSessionContext(sessionID)
		if session == nil {
			session = &SessionContext{Server: m, RS: rs, finished: make(chan bool, 10)}
			m.SetSessionContext(sessionID, session)
		} else {
			session.Server = m
			session.RS = rs
		}
	}
	return
}

func getPortAndMCPServerNameFromURI(uri string) (port int, name string) {
	isMCP := strings.Contains(uri, "/mcp")
	isSSE := strings.Contains(uri, "/sse")
	if !isMCP && !isSSE {
		return
	}
	if isSSE && isMCP {
		uri = strings.ReplaceAll(uri, "/sse", "")
		isSSE = false
	}
	var parts []string
	if isSSE {
		parts = strings.Split(uri, "/sse")
	} else {
		parts = strings.Split(uri, "/mcp")
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

func findServerForURI(uri string) (matchedURI string, server *MCPServer) {
	log.Printf("Searching for URI [%s] in Server Routes: %+v\n", uri, ServerRoutes)
	server = ServerRoutes[uri]
	if server == nil {
		for uri2, server2 := range ServerRoutes {
			if server2.uriRegexp != nil {
				if server2.uriRegexp.MatchString(uri) {
					matchedURI = uri2
					server = server2
					break
				}
			}
		}
	} else {
		matchedURI = uri
	}
	log.Printf("Matced URI = [%s] vs original URI: %s\n", matchedURI, uri)
	return
}

func getServer(r *http.Request) *gomcp.Server {
	var server *MCPServer
	port := util.GetRequestOrListenerPortNum(r)
	defer func() {
		log.Println("-------- MCP Request Details --------")
		util.PrintRequest(r)
		if server != nil {
			rs := util.GetRequestStore(r)
			rs.ResponseWriter.Header().Add("Goto-Server", server.ID)
		} else {
			log.Printf("Not handling MCP request on port [%d]", port)
		}
		server.getOrSetSessionContext(r)
	}()
	uri := r.RequestURI
	uri, server = findServerForURI(uri)
	if server != nil {
		log.Printf("Server [%s] will handle MCP request based on URI match [%s] on port [%d]", server.Name, uri, port)
		return server.server
	}
	ps := PortsServers[port]
	if ps == nil || len(ps.Servers) == 0 {
		log.Printf("Falling back to Default MCP Server [%s] on port [%d]", DefaultStatelessServer.Name, port)
		return DefaultStatelessServer.server
	}
	_, serverName := getPortAndMCPServerNameFromURI(r.RequestURI)
	server = ps.Servers[serverName]
	if server == nil {
		server = ps.DefaultServer
		log.Printf("MCP Server [%s] not found on port [%d], using PortDefault server [%s]", serverName, port, server.Name)
	}
	if !server.Enabled {
		log.Printf("MCP Server [%s] is disabled on port [%d]. Falling back to Default MCP Server [%s].", server.Name, port, DefaultStatelessServer.Name)
		server = DefaultStatelessServer
	}
	return server.server
}

func (m *MCPServer) Serve(w http.ResponseWriter, r *http.Request, handler http.Handler) {
	sessionID := r.Header.Get(HeaderMCPSessionID)
	session := m.GetSessionContext(sessionID)
	switch r.Method {
	case "DELETE":
		log.Println("-------- MCPServer.Serve: Serving DELETE --------")
		if session != nil {
			handler.ServeHTTP(w, r)
			log.Println("-------- MCPServer.Serve: DELETE closing session --------")
			close(session.finished)
		}
	case "GET":
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		r = r.WithContext(ctx)
		rc := make(chan bool, 1)
		requestFinished := false
		go func() {
			log.Println("-------- MCPServer.Serve: Serving GET --------")
			handler.ServeHTTP(w, r)
			log.Println("-------- MCPServer.Serve: GET returned --------")
			close(rc)
			log.Println("-------- MCPServer.Serve: GET notified request channel --------")
		}()
		if session != nil {
			select {
			case <-rc:
				requestFinished = true
				log.Println("-------- MCPServer.Serve: Request channel finished --------")
			case <-session.finished:
				log.Println("-------- MCPServer.Serve: Session channel closed --------")
			}
			if !requestFinished {
				log.Println("-------- MCPServer.Serve: Request not finished, marking contxt done --------")
				ctx.Done()
			} else {
				log.Println("-------- MCPServer.Serve: Request finished. All GOOD --------")
			}
		} else {
			<-rc
			log.Println("-------- MCPServer.Serve: Request finished. All GOOD --------")
		}
	default:
		log.Println("-------- MCPServer.Serve: Serving Normal --------")
		handler.ServeHTTP(w, r)
	}
	log.Println("-------- MCPServer.Serve: Finished --------")
}

func MCPHybridHandler(server *MCPServer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, r, rs := util.WithRequestStore(r)
		port := util.GetRequestOrListenerPortNum(r)
		conn.SendGotoHeaders(w, r)
		//util.CopyHeaders("Request", r, w, r.Header, true, true, false)
		rs.ResponseWriter = w
		hasSSE := strings.Contains(r.RequestURI, "/sse")
		hasMCP := strings.Contains(r.RequestURI, "/mcp")
		// w, irw := intercept.WithIntercept(r, w)
		if hasMCP && !hasSSE {
			log.Printf("Port [%d] Request [%s] will be served by [%s]/stream", port, r.RequestURI, server.Name)
			// w.Header().Set(constants.HeaderContentType, "text/event-stream")
			// w.Header().Set(constants.HeaderCacheControl, "no-cache")
			// w.Header().Set(constants.HeaderTransferEncoding, "chunked")
			server.Serve(w, r, server.streamHTTPHandler)
		} else {
			log.Printf("Port [%d] Request [%s] will be served by [%s]/sse", port, r.RequestURI, server.Name)
			// w.Header().Set("Connection", "keep-alive")
			// w.Header().Set("Access-Control-Allow-Origin", "*")
			// w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, DELETE")
			// w.Header().Set("Access-Control-Allow-Headers", "Cache-Control, Content-Type, Authorization")
			// w.Header().Set(constants.HeaderContentType, "text/event-stream")
			// w.Header().Set(constants.HeaderCacheControl, "no-cache")
			// w.Header().Set(constants.HeaderTransferEncoding, "chunked")
			r = r.WithContext(util.SetSSE(r.Context()))
			server.Serve(w, r, server.sseHandler)
		}
		rs.RequestServed = true
		// log.Println(string(irw.Data))
		// irw.Proceed()
	})
}

func HandleMCPDefault(w http.ResponseWriter, r *http.Request) {
	log.Println("-------- HandleMCPDefault: MCP Request Details --------")
	util.PrintRequest(r)
	l := listeners.GetCurrentListener(r)
	rs := util.GetRequestStore(r)
	hasSSE := strings.Contains(r.RequestURI, "/sse")
	hasMCP := strings.Contains(r.RequestURI, "/mcp")
	isStateful := strings.Contains(r.RequestURI, "/stateful")
	if hasMCP && !hasSSE {
		if isStateful {
			log.Printf("Port [%d] Request [%s] will be served by DefaultStatefulServer/stream", l.Port, r.RequestURI)
			DefaultStatefulServer.Serve(w, r, DefaultStatefulServer.streamHTTPHandler)
		} else {
			log.Printf("Port [%d] Request [%s] will be served by DefaultStatelessServer/stream", l.Port, r.RequestURI)
			// DefaultServer.streamHTTPHandler.ServeHTTP(w, r)
			DefaultStatelessServer.Serve(w, r, DefaultStatelessServer.streamHTTPHandler)
		}
	} else {
		if isStateful {
			log.Printf("Port [%d] Request [%s] will be served by DefaultStatefulServer/SSE", l.Port, r.RequestURI)
			DefaultStatefulServer.Serve(w, r, DefaultStatefulServer.sseHandler)
		} else {
			log.Printf("Port [%d] Request [%s] will be served by DefaultStatelessServer/SSE", l.Port, r.RequestURI)
			// DefaultServer.streamHTTPHandler.ServeHTTP(w, r)
			DefaultStatelessServer.Serve(w, r, DefaultStatelessServer.sseHandler)
		}
	}
	rs.RequestServed = true
}

func HandleMCP(w http.ResponseWriter, r *http.Request) {
	l := listeners.GetCurrentListener(r)
	rs := util.GetRequestStore(r)
	isMCP := l.IsMCP || rs.IsMCP
	if isMCP && !rs.IsAdminRequest {
		log.Println("------- MayBe MCP ------")
		if proxy.WillProxyMCP(l.Port, r) {
			log.Printf("MCP is configured to proxy on Port [%d]. Skipping MCP processing", l.Port)
			return
		}
		_, server := findServerForURI(r.RequestURI)
		ps := GetPortMCPServers(l.Port)
		if server == nil {
			_, serverName := getPortAndMCPServerNameFromURI(r.RequestURI)
			server = ps.Servers[serverName]
		}
		if server == nil && rs.IsMCP {
			server = ps.DefaultServer
			if server == nil {
				isStateless := strings.Contains(r.RequestURI, "/stateless")
				if isStateless {
					server = DefaultStatelessServer
				} else {
					server = DefaultStatefulServer
				}
			}
		}
		if server != nil {
			log.Printf("Port [%d] Request [%s] will be served by Server [%s] Stateless [%t]", l.Port, r.RequestURI, server.Name, server.Stateless)
			server.handler.ServeHTTP(w, r)
			rs.RequestServed = true
		} else {
			log.Printf("Port [%d] Request [%s] No server available. Routing to HTTP server", l.Port, r.RequestURI)
		}
		log.Println("---- After MayBe MCP ----")
	} else {
		log.Printf("Port [%d] Request [%s] skipping MCP processing", l.Port, r.RequestURI)
	}
}

func MCPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs := util.GetRequestStore(r)
		//HandleMCPDefault(w, r)
		HandleMCP(w, r)
		if !rs.RequestServed {
			util.HTTPHandler.ServeHTTP(w, r)
		}
	})
}
