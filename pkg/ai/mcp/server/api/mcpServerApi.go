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

package mcpserverapi

import (
	"encoding/json"
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
	mcpapiRouter := util.PathRouter(r, "/mcpapi")

	util.AddRouteWithPort(mcpapiRouter, "/servers", getServers, "GET")
	util.AddRouteWithPort(mcpapiRouter, "/servers/all", getServers, "GET")
	util.AddRouteWithPort(mcpapiRouter, "/servers/names", getServers, "GET")
	util.AddRouteWithPort(mcpapiRouter, "/server/{server}?", getServers, "GET")

	util.AddRouteWithPort(mcpapiRouter, "/servers/add", addServers, "POST")
	util.AddRouteQWithPort(mcpapiRouter, "/servers/{server}/route", setServerRoute, "uri", "POST")
	util.AddRouteWithPort(mcpapiRouter, "/{s:server|servers}/start", startServer, "POST")
	util.AddRouteWithPort(mcpapiRouter, "/{s:server|servers}/{server}/start", startServer, "POST")
	util.AddRouteWithPort(mcpapiRouter, "/servers/stop", stopServer, "POST")
	util.AddRouteWithPort(mcpapiRouter, "/servers/{server}/stop", stopServer, "POST")

	util.AddRouteMultiQWithPort(mcpapiRouter, "/proxy", setupMCPProxy, []string{"endpoint", "sni", "headers"}, "POST")
	util.AddRouteMultiQWithPort(mcpapiRouter, "/proxy/{tool}", setupMCPProxy, []string{"endpoint", "to", "sni", "headers"}, "POST")

	util.AddRouteQWithPort(mcpapiRouter, "/server/{server}/payload/completion", addCompletionPayload, "type", "POST")
	util.AddRouteQWithPort(mcpapiRouter, "/server/{server}/payload/completion/delay={delay}", addCompletionPayload, "type", "POST")

	util.AddRouteWithPort(mcpapiRouter, "/{q:servers|all}/{kind:tools|prompts|resources|templates}", getComponents, "GET")
	util.AddRouteWithPort(mcpapiRouter, "/server/{server}/{kind:tools|prompts|resources|templates}", getComponents, "GET")

	util.AddRouteWithPort(mcpapiRouter, "/server/{server}/{kind:tools|prompts|resources|templates}/add", addComponent, "POST")
	util.AddRouteWithPort(mcpapiRouter, "/server/{server}/tool/{tool}/call", callTool, "POST")

	util.AddRouteWithPort(mcpapiRouter, "/server/{server}/payload/{kind:tools|prompts|resources|templates}/{name}", addComponentPayload, "POST")
	util.AddRouteWithPort(mcpapiRouter, "/server/{server}/payload/{kind:tools|prompts|resources|templates}/{name}/stream/count={count}", addComponentPayload, "POST")
	util.AddRouteWithPort(mcpapiRouter, "/server/{server}/payload/{kind:tools|prompts|resources|templates}/{name}/stream/count={count}/delay={delay}", addComponentPayload, "POST")

	util.AddRouteWithPort(mcpapiRouter, "/servers/clear/all", clearServers, "POST")
	util.AddRouteWithPort(mcpapiRouter, "/servers/clear", clearServers, "POST")
	util.AddRouteWithPort(mcpapiRouter, "/server/{server}/clear", clearServers, "POST")

	util.AddRouteQWithPort(mcpapiRouter, "/status/set/{status}", setStatus, "uri", "POST")
	util.AddRouteWithPort(mcpapiRouter, "/status/set/{status}", setStatus, "POST")
	util.AddRouteQWithPort(mcpapiRouter, "/status/set/{status}/header/{header}={value}", setStatus, "uri", "POST")
	util.AddRouteQWithPort(mcpapiRouter, "/status/set/{status}/header/{header}", setStatus, "uri", "POST")
	util.AddRouteQWithPort(mcpapiRouter, "/status/set/{status}/header/not/{header}", setStatus, "uri", "POST")
	util.AddRouteWithPort(mcpapiRouter, "/status/set/{status}/header/{header}={value}", setStatus, "POST")
	util.AddRouteWithPort(mcpapiRouter, "/status/set/{status}/header/{header}", setStatus, "POST")
	util.AddRouteWithPort(mcpapiRouter, "/status/set/{status}/header/not/{header}", setStatus, "POST")

	util.AddRouteWithPort(mcpapiRouter, "/status/configure", configureStatus, "POST")
	util.AddRouteWithPort(mcpapiRouter, "/status/clear", clearStatus, "POST")
	util.AddRouteWithPort(mcpapiRouter, "/statuses", getStatuses, "GET")
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
		var server *mcpserver.MCPServer
		name = strings.ToLower(name)
		for _, ps := range mcpserver.PortsServers {
			server = ps.GetMCPServer(name)
			if server != nil {
				break
			}
		}
		if server == nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintln(w, "{\"error\": \"MCP Server doesn't exist on any port\"}")
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
	err := util.ReadJsonPayload(r, &payloads)
	msg := ""
	if err != nil || len(payloads) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		if err != nil {
			msg = fmt.Sprintf("Failed to parse payload with error [%s]", err.Error())
		} else {
			msg = "Failed to add servers, no MCP Server in the payload"
		}
		fmt.Fprintln(w, msg)
		util.AddLogMessage(msg, r)
		return
	}
	mcpserver.AddMCPServers(port, payloads)
	names := []string{}
	for _, p := range payloads {
		if p.Port <= 0 {
			p.Port = port
		}
		names = append(names, fmt.Sprintf("%s (port: %d)", p.Name, p.Port))
	}
	msg = fmt.Sprintf("Added MCP Servers: %+v", names)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func setServerRoute(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	serverName := util.GetStringParamValue(r, "server")
	uri := util.GetStringParamValue(r, "uri")
	msg := ""
	server := mcpserver.GetMCPServer(serverName)
	if server == nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("MCP Server [%s] not configured on any port", serverName)
	} else {
		mcpserver.SetServerRoute(uri, server)
		msg = fmt.Sprintf("MCP Server [%s] will be served over URI [%s] on port [%d]", serverName, uri, port)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func configureStatus(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	sc, err := mcpserver.StatusManager.ParseStatusConfig(port, r.Body)
	msg := ""
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Failed to parse status config with error: %s", err.Error())
		fmt.Fprintln(w, msg)
	} else {
		msg = fmt.Sprintf("Parsed status config: %s", sc.Log("MCP", port))
		util.WriteJsonPayload(w, sc)
	}
	util.AddLogMessage(msg, r)
}

func setStatus(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	uri := util.GetStringParamValue(r, "uri")
	header := util.GetStringParamValue(r, "header")
	value := util.GetStringParamValue(r, "value")
	noHeader := strings.Contains(r.RequestURI, "not")
	statusCodes, times, ok := util.GetStatusParam(r)
	if !ok {
		util.AddLogMessage("Invalid status", r)
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintln(w, "Invalid Status")
		return
	}
	status := mcpserver.StatusManager.SetStatusFor(port, uri, header, value, statusCodes, times, noHeader)
	msg := status.Log("MCP", port)
	util.AddLogMessage(msg, r)
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, msg)
}

func clearStatus(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	mcpserver.StatusManager.Clear(port)
	msg := fmt.Sprintf("Status cleared on port [%d]", port)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func getStatuses(w http.ResponseWriter, r *http.Request) {
	util.WriteJsonPayload(w, mcpserver.StatusManager.Statuses)
	util.AddLogMessage("Delivered statuses", r)
}

func clearServers(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	all := strings.Contains(r.RequestURI, "all")
	name := util.GetStringParamValue(r, "server")
	msg := ""
	if all {
		mcpserver.ClearAllMCPServers()
		msg = "Cleared all MCP servers"
	} else if name != "" {
		server := mcpserver.GetMCPServer(name)
		if server == nil {
			w.WriteHeader(http.StatusBadRequest)
			msg = fmt.Sprintf("MCP Server [%s] not configured on any port", name)
		} else {
			msg = fmt.Sprintf("Cleared MCP server [%s] on port [%d]", name, port)
		}
	} else {
		ps := mcpserver.GetPortMCPServers(port)
		ps.Clear()
		msg = fmt.Sprintf("Cleared all MCP servers on port [%d]", port)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func startServer(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	name := util.GetStringParamValue(r, "server")
	msg := ""
	ps := mcpserver.GetPortMCPServers(port)
	ps.Start(name)
	msg = fmt.Sprintf("Started [%d] MCP Servers at port [%d]", len(ps.Servers), port)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func stopServer(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	name := util.GetStringParamValue(r, "server")
	msg := ""
	ps := mcpserver.GetPortMCPServers(port)
	ps.Stop(name)
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
	if name != "" {
		server := mcpserver.GetMCPServer(name)
		if server == nil {
			w.WriteHeader(http.StatusBadRequest)
			msg := fmt.Sprintf("MCP Server [%s] not configured on any port", name)
			fmt.Fprintln(w, msg)
			util.AddLogMessage(msg, r)
			return
		} else {
			util.WriteJsonPayload(w, server.GetComponents(kind))
		}
	} else if q == "servers" {
		ps := mcpserver.GetPortMCPServers(port)
		util.WriteJsonPayload(w, ps.GetComponents(kind))
	} else {
		util.WriteJsonPayload(w, mcpserver.GetAllComponents(kind))
	}
}

func addComponent(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	kind := util.GetStringParamValue(r, "kind")
	serverName := util.GetStringParamValue(r, "server")
	b, _ := io.ReadAll(r.Body)
	msg := ""
	server := mcpserver.GetMCPServer(serverName)
	if server == nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("MCP Server [%s] not configured on any port", serverName)
	} else {
		names, err := server.AddComponents(kind, b)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			msg = fmt.Sprintf("Failed to add MCP %s to server [%s] on port [%d] with error [%s]", kind, serverName, port, err.Error())
		} else {
			msg = fmt.Sprintf("Added %s to server [%s] on port [%d]: %+v", kind, serverName, port, names)
		}
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
	server := mcpserver.GetMCPServer(name)
	msg := ""
	if server == nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("MCP Server [%s] not configured on any port", name)
	} else {
		count := server.AddCompletionPayload(completionType, payload, delayMin, delayMax, delayCount)
		msg = fmt.Sprintf("Set completion payload (count [%d]) for server [%s] on port [%d]", count, server.Name, port)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func addComponentPayload(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	serverName := util.GetStringParamValue(r, "server")
	kind := util.GetStringParamValue(r, "kind")
	name := util.GetStringParamValue(r, "name")
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
	server := mcpserver.GetMCPServer(serverName)
	msg := ""
	if server == nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("MCP Server [%s] not configured on any port", serverName)
	} else if err := server.AddPayload(name, kind, payload, isJSON, isStream, streamCount, delayMin, delayMax, delayCount); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Failed to set payload for component [%s] in MCP %s to server [%s] on port [%d] with error [%s]", name, kind, serverName, port, err.Error())
	} else {
		msg = fmt.Sprintf("Set payload for component [%s] in MCP %s to server [%s] on port [%d]", name, kind, serverName, port)
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

func sendBadRequest(msg string, w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusBadRequest)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func callTool(w http.ResponseWriter, r *http.Request) {
	serverName := util.GetStringParamValue(r, "server")
	toolName := util.GetStringParamValue(r, "tool")
	msg := ""
	server := mcpserver.GetMCPServer(serverName)
	if server == nil {
		sendBadRequest(fmt.Sprintf("MCP Server [%s] not configured on any port", serverName), w, r)
		return
	}
	tool := server.GetTool(toolName)
	if tool == nil {
		sendBadRequest(fmt.Sprintf("MCP Server [%s] Tool [%s] not configured", serverName, toolName), w, r)
		return
	}
	b, err := io.ReadAll(r.Body)
	if err != nil {
		sendBadRequest(fmt.Sprintf("Failed to read request body with error: %s", err.Error()), w, r)
		return
	}
	args := json.RawMessage{}
	err = args.UnmarshalJSON(b)
	if err != nil {
		sendBadRequest(fmt.Sprintf("Failed to parse args from request body. Calling Tool [%s] without payload", toolName), w, r)
	}
	req := &gomcp.CallToolRequest{
		Params: &gomcp.CallToolParamsRaw{
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
