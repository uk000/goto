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

package mcpserverapi

import (
	"encoding/json"
	"fmt"
	mcpserver "goto/pkg/ai/mcp/server"
	"goto/pkg/constants"
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

func setRoutes(r *mux.Router) {
	mcpapi := middleware.RootPath("/mcpapi")
	mcpServers := util.PathRouter(mcpapi, "/servers")

	util.AddRoute(mcpServers, "/add", addServers, "POST")

	util.AddRoute(mcpServers, "", getServers, "GET")
	util.AddRoute(mcpServers, "/all", getServers, "GET")
	util.AddRoute(mcpServers, "/names", getServers, "GET")
	util.AddRoute(mcpServers, "/{server}?", getServers, "GET")

	util.AddRouteQ(mcpServers, "/{server}/route", setServerRoute, "uri", "POST")
	util.AddRoute(mcpServers, "/start", startServer, "POST")
	util.AddRoute(mcpServers, "/{server}/start", startServer, "POST")
	util.AddRoute(mcpServers, "/stop", stopServer, "POST")
	util.AddRoute(mcpServers, "/{server}/stop", stopServer, "POST")

	util.AddRouteQ(mcpServers, "/{server}/payload/completion", addCompletionPayload, "type", "POST")
	util.AddRouteQ(mcpServers, "/{server}/payload/completion/delay={delay}", addCompletionPayload, "type", "POST")

	util.AddRoute(mcpServers, "/{kind:tools|prompts|resources|templates}", getComponents, "GET")
	util.AddRoute(mcpServers, "/{server}/{kind:tools|prompts|resources|templates}", getComponents, "GET")
	util.AddRoute(mcpServers, "/{server}/{kind:tools|prompts|resources|templates}/{name}", getComponents, "GET")

	util.AddRoute(mcpServers, "/{server}/{kind:tools|prompts|resources|templates}/add", addComponent, "POST")
	util.AddRoute(mcpServers, "/{server}/{t:tools|tool}/{tool}/call", callTool, "POST")

	util.AddRoute(mcpServers, "/{server}/payload/{kind:tools|prompts|resources|templates}/{name}", addComponentPayload, "POST")
	util.AddRoute(mcpServers, "/{server}/payload/{kind:tools|prompts|resources|templates}/{name}/stream/count={count}", addComponentPayload, "POST")
	util.AddRoute(mcpServers, "/{server}/payload/{kind:tools|prompts|resources|templates}/{name}/stream/count={count}/delay={delay}", addComponentPayload, "POST")

	util.AddRoute(mcpServers, "/clear/all", clearServers, "POST")
	util.AddRoute(mcpServers, "/clear", clearServers, "POST")
	util.AddRoute(mcpServers, "/{server}/clear", clearServers, "POST")

	mcpStatus := util.PathRouter(mcpapi, "/status")
	util.AddRouteQO(mcpStatus, "/set/{status}", setStatus, "uri", "POST")
	util.AddRouteQO(mcpStatus, "/set/{status}/header/{header}={value}", setStatus, "uri", "POST")
	util.AddRouteQO(mcpStatus, "/set/{status}/header/{header}", setStatus, "uri", "POST")
	util.AddRouteQO(mcpStatus, "/set/{status}/header/not/{header}", setStatus, "uri", "POST")

	util.AddRoute(mcpStatus, "/configure", configureStatus, "POST")
	util.AddRoute(mcpStatus, "/clear", clearStatus, "POST")
	util.AddRoute(mcpStatus, "es", getStatuses, "GET")
}

func getServers(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	all := strings.Contains(r.RequestURI, "all")
	names := strings.Contains(r.RequestURI, "names")
	name := util.GetStringParamValue(r, "server")
	yaml := strings.EqualFold(r.Header.Get("Accept"), "application/yaml")
	if names {
		util.WriteJsonOrYAMLPayload(w, mcpserver.GetMCPServerNames(), yaml)
	} else if all {
		util.WriteJsonOrYAMLPayload(w, mcpserver.PortsServers, yaml)
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
			util.WriteJsonOrYAMLPayload(w, server, yaml)
		}
	} else {
		util.WriteJsonOrYAMLPayload(w, mcpserver.GetPortMCPServers(port), yaml)
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
	server := mcpserver.GetMCPServer(port, serverName)
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
	mcpserver.StatusManager.Clear(port, "")
	msg := fmt.Sprintf("Status cleared on port [%d]", port)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func getStatuses(w http.ResponseWriter, r *http.Request) {
	util.WriteJsonPayload(w, mcpserver.StatusManager.PortStatus)
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
		server := mcpserver.GetMCPServer(port, name)
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
	serverName := util.GetStringParamValue(r, "server")
	kind := util.GetStringParamValue(r, "kind")
	name := util.GetStringParamValue(r, "name")
	yaml := strings.EqualFold(r.Header.Get("Accept"), "application/yaml")
	if serverName != "" {
		server := mcpserver.GetMCPServer(port, serverName)
		if server == nil {
			w.WriteHeader(http.StatusBadRequest)
			msg := fmt.Sprintf("MCP Server [%s] not configured on any port", serverName)
			fmt.Fprintln(w, msg)
			util.AddLogMessage(msg, r)
			return
		} else if name == "" {
			util.WriteJsonOrYAMLPayload(w, server.GetComponents(kind), yaml)
		} else {
			util.WriteJsonOrYAMLPayload(w, server.GetComponent(name, kind), yaml)
		}
	} else if q == "servers" {
		ps := mcpserver.GetPortMCPServers(port)
		util.WriteJsonOrYAMLPayload(w, ps.GetComponents(kind), yaml)
	} else {
		util.WriteJsonOrYAMLPayload(w, mcpserver.GetAllComponents(kind), yaml)
	}
}

func addComponent(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	kind := util.GetStringParamValue(r, "kind")
	serverName := util.GetStringParamValue(r, "server")
	b, _ := io.ReadAll(r.Body)
	msg := ""
	server := mcpserver.GetMCPServer(port, serverName)
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
	server := mcpserver.GetMCPServer(port, name)
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
	server := mcpserver.GetMCPServer(port, serverName)
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

func sendBadRequest(msg string, w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusBadRequest)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func callTool(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	serverName := util.GetStringParamValue(r, "server")
	toolName := util.GetStringParamValue(r, "tool")
	msg := ""
	server := mcpserver.GetMCPServer(port, serverName)
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
