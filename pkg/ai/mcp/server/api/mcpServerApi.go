package mcpserverapi

import (
	"fmt"
	mcpserver "goto/pkg/ai/mcp/server"
	"goto/pkg/constants"
	"goto/pkg/proxy"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"io"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	Middleware = middleware.NewMiddleware("mcp", setRoutes, nil)
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	mcpRouter := util.PathRouter(r, "/mcp")
	sseRouter := util.PathRouter(r, "/sse")

	util.AddRouteWithPort(mcpRouter, "", mcpserver.HandleMCP, "GET", "POST", "DELETE", "OPTIONS")
	util.AddRouteWithPort(mcpRouter, "/sse", mcpserver.HandleMCP, "GET", "POST", "DELETE", "OPTIONS")
	util.AddRouteWithPort(sseRouter, "", mcpserver.HandleMCP, "GET", "POST", "DELETE", "OPTIONS")

	util.AddRouteWithPort(mcpRouter, "/servers", getServers, "GET")
	util.AddRouteWithPort(mcpRouter, "/servers/all", getServers, "GET")
	util.AddRouteWithPort(mcpRouter, "/servers/names", getServers, "GET")
	util.AddRouteWithPort(mcpRouter, "/server/{server}?", getServers, "GET")

	util.AddRouteWithPort(mcpRouter, "/servers/add", addServers, "POST")

	util.AddRouteWithPort(mcpRouter, "/servers/start", startServer, "POST")
	util.AddRouteWithPort(mcpRouter, "/server/{server}/start", startServer, "POST")
	util.AddRouteWithPort(mcpRouter, "/servers/stop", stopServer, "POST")
	util.AddRouteWithPort(mcpRouter, "/servers/{server}/stop", stopServer, "POST")

	util.AddRouteMultiQWithPort(mcpRouter, "/proxy", setupMCPProxy, []string{"endpoint", "sni", "headers"}, "POST")
	util.AddRouteMultiQWithPort(mcpRouter, "/proxy/{tool}", setupMCPProxy, []string{"endpoint", "to", "sni", "headers"}, "POST")

	util.AddRouteQWithPort(mcpRouter, "/server/{server}/payload/completion", addCompletionPayload, "type", "POST")
	util.AddRouteQWithPort(mcpRouter, "/server/{server}/payload/completion/delay={delay}", addCompletionPayload, "type", "POST")

	util.AddRouteWithPort(mcpRouter, "/{q:servers|all}/{kind:tools|prompts|resources|templates}", getComponents, "GET")
	util.AddRouteWithPort(mcpRouter, "/server/{server}/{kind:tools|prompts|resources|templates}", getComponents, "GET")

	util.AddRouteWithPort(mcpRouter, "/server/{server}/{kind:tools|prompts|resources|templates}/add", addComponent, "POST")
	util.AddRouteWithPort(mcpRouter, "/server/{server}/tool/{tool}/call", callTool, "POST")

	util.AddRouteQWithPort(mcpRouter, "/server/{server}/payload/{kind:tools|prompts|resources|templates}/{name}/remote", addComponentPayload, "url", "POST")
	util.AddRouteWithPort(mcpRouter, "/server/{server}/payload/{kind:tools|prompts|resources|templates}/{name}", addComponentPayload, "POST")
	util.AddRouteWithPort(mcpRouter, "/server/{server}/payload/{kind:tools|prompts|resources|templates}/{name}/stream/count={count}", addComponentPayload, "POST")
	util.AddRouteWithPort(mcpRouter, "/server/{server}/payload/{kind:tools|prompts|resources|templates}/{name}/stream/count={count}/delay={delay}", addComponentPayload, "POST")
}

func getServers(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	all := strings.Contains(r.RequestURI, "all")
	names := strings.Contains(r.RequestURI, "names")
	name := util.GetStringParamValue(r, "server")
	if names {
		util.WriteJsonPayload(w, mcpserver.GetMCPServerNames())
	} else if all {
		util.WriteJsonPayload(w, mcpserver.PortsServers)
	} else if name != "" {
		ps := mcpserver.GetPortMCPServers(port)
		server := ps.GetMCPServer(name)
		if server == nil {
			fmt.Fprintf(w, "{\"error\": \"MCP Server doesn't exist at port [%d]\"}\n", port)
		} else {
			util.WriteJsonPayload(w, server)
		}
	} else {
		util.WriteJsonPayload(w, mcpserver.GetPortMCPServers(port))
	}
}

func addServers(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	payloads := []*mcpserver.MCPServerPayload{}
	util.ReadJsonPayload(r, &payloads)
	mcpserver.AddMCPServers(port, payloads)
	names := []string{}
	for _, p := range payloads {
		if p.Port <= 0 {
			p.Port = port
		}
		names = append(names, fmt.Sprintf("%s@%d", p.Name, p.Port))
	}
	msg := fmt.Sprintf("Added MCP Servers [%+v]", names)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func startServer(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	msg := ""
	ps := mcpserver.GetPortMCPServers(port)
	ps.Start()
	msg = fmt.Sprintf("Started [%d] MCP Servers at port [%d]", len(ps.Servers), port)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func stopServer(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	msg := ""
	ps := mcpserver.GetPortMCPServers(port)
	ps.Start()
	msg = fmt.Sprintf("Stopped [%d] MCP Servers at port [%d]", len(ps.Servers), port)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func getComponent(kind string) (isTools, isPrompts, isResources, isTemplates bool) {
	switch kind {
	case "tools":
		isTools = true
	case "prompts":
		isPrompts = true
	case "resources":
		isResources = true
	case "templates":
		isTemplates = true
	}
	return
}

func getComponents(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	q := util.GetStringParamValue(r, "q")
	name := util.GetStringParamValue(r, "server")
	kind := util.GetStringParamValue(r, "kind")
	ps := mcpserver.GetPortMCPServers(port)
	if name != "" {
		server := ps.GetMCPServer(name)
		if server == nil {
			w.WriteHeader(http.StatusBadRequest)
			msg := fmt.Sprintf("MCP Server [%s] doesn't exist at port [%d]", name, port)
			fmt.Fprintln(w, msg)
			util.AddLogMessage(msg, r)
			return
		} else {
			util.WriteJsonPayload(w, server.GetComponents(kind))
		}
	} else if q == "servers" {
		util.WriteJsonPayload(w, ps.GetComponents(kind))
	} else {
		util.WriteJsonPayload(w, mcpserver.GetAllComponents(kind))
	}
}

func addComponent(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	kind := util.GetStringParamValue(r, "kind")
	server := util.GetStringParamValue(r, "server")
	b, _ := io.ReadAll(r.Body)
	msg := ""
	ps := mcpserver.GetPortMCPServers(port)
	count, err := ps.AddComponents(server, kind, b)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Failed to add MCP %s to server [%s] on port [%d] with error [%s]", kind, server, port, err.Error())
	} else {
		msg = fmt.Sprintf("Added %d MCP %s to server [%s] on port [%d]", count, kind, server, port)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func addCompletionPayload(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	name := util.GetStringParamValue(r, "server")
	completionType := util.GetStringParamValue(r, "type")
	delayMin, delayMax, delayCount, _ := util.GetDurationParam(r, "delay")
	if delayCount == 0 {
		delayCount = -1
	}
	payload, _ := io.ReadAll(r.Body)
	ps := mcpserver.GetPortMCPServers(port)
	server := ps.GetMCPServer(name)
	count := server.AddCompletionPayload(completionType, payload, delayMin, delayMax, delayCount)
	msg := fmt.Sprintf("Set completion payload (count [%d]) for server [%s] on port [%d]", count, server.Name, port)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func addComponentPayload(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	serverName := util.GetStringParamValue(r, "server")
	kind := util.GetStringParamValue(r, "kind")
	name := util.GetStringParamValue(r, "name")
	url := util.GetStringParamValue(r, "url")
	isRemote := strings.Contains(r.RequestURI, "remote")
	isStream := strings.Contains(r.RequestURI, "stream")
	streamCount := util.GetIntParamValue(r, "count")
	delayMin, delayMax, delayCount, _ := util.GetDurationParam(r, "delay")
	if delayCount == 0 {
		delayCount = -1
	}
	contentType := r.Header.Get(constants.HeaderResponseContentType)
	if contentType == "" {
		contentType = constants.ContentTypeJSON
	}
	isJSON := contentType == constants.ContentTypeJSON

	payload, _ := io.ReadAll(r.Body)
	ps := mcpserver.GetPortMCPServers(port)
	server := ps.GetMCPServer(serverName)
	msg := ""
	if err := server.AddPayload(name, kind, payload, url, isRemote, isJSON, isStream, streamCount, delayMin, delayMax, delayCount); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Failed to set payload for component [%s] in MCP %s to server [%s] on port [%d] with error [%s]", name, kind, server.Name, port, err.Error())
	} else {
		msg = fmt.Sprintf("Set payload for component [%s] in MCP %s to server [%s] on port [%d]", name, kind, server.Name, port)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func setupMCPProxy(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	serverName := util.GetStringParamValue(r, "server")
	tool := util.GetStringParamValue(r, "tool")
	toTool := util.GetStringParamValue(r, "to")
	endpoint := util.GetStringParamValue(r, "endpoint")
	sni := util.GetStringParamValue(r, "sni")
	h, present := util.GetListParam(r, "headers")
	headers := [][]string{}
	if present {
		for _, val := range h {
			kv := strings.Split(val, ":")
			headers = append(headers, []string{kv[0], kv[1]})
		}
	}
	proxy.GetMCPProxyForPort(port).SetupMCPProxy(serverName, endpoint, sni, tool, toTool, headers)
	msg := fmt.Sprintf("Setup MCP proxy at port [%d] for server [%s] tool [%s] to endpoint [%s] tool [%s] with sni [%s]", port, serverName, tool, endpoint, toTool, sni)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func callTool(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	serverName := util.GetStringParamValue(r, "server")
	toolName := util.GetStringParamValue(r, "tool")
	msg := ""
	ps := mcpserver.GetPortMCPServers(port)
	server := ps.GetMCPServer(serverName)
	var tool *mcpserver.MCPTool
	if server != nil {
		tool = server.GetTool(toolName)
	}
	if server == nil || tool == nil {
		w.WriteHeader(http.StatusBadRequest)
		msg := fmt.Sprintf("MCP Server [%s] Tool [%s] doesn't exist at port [%d]", serverName, toolName, port)
		fmt.Fprintln(w, msg)
		util.AddLogMessage(msg, r)
		return
	}
	args := map[string]any{}
	err := util.ReadJsonPayloadFromBody(r.Body, &args)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg := fmt.Sprintf("MCP Server [%s] Tool [%s] failed to read arguments from body with error [%s]", serverName, toolName, err.Error())
		fmt.Fprintln(w, msg)
		util.AddLogMessage(msg, r)
		return
	}
	req := &gomcp.CallToolRequest{
		Params: &gomcp.CallToolParams{
			Name:      toolName,
			Arguments: args,
		},
		Session: &gomcp.ServerSession{},
	}
	result, err := tool.Handle(r.Context(), req)
	if err != nil {
		msg = fmt.Sprintf("Server [%s] Tool [%s] returned error: [%s]", serverName, toolName, err.Error())
		fmt.Fprintln(w, msg)
	} else {
		msg = fmt.Sprintf("Server [%s] Tool [%s] successful", serverName, toolName)
		util.WriteJsonPayload(w, result)
	}
	util.AddLogMessage(msg, r)
}
