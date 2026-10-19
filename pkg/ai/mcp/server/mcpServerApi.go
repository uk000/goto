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
	"encoding/json"
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/server/middleware"
	"goto/pkg/server/response/status"
	"goto/pkg/util"
	"io"
	"net/http"
	"regexp"
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
	mcpServerRouter := util.PathRouter(mcpapi, "/server/{server}")

	util.AddRoute(mcpapi, "/schemas/add", addToolSchemas, "POST")

	util.AddRoute(mcpServers, "/add", addServers, "POST")
	util.AddRoute(mcpServers, "", getServers, "GET")
	util.AddRoute(mcpServers, "/all", getServers, "GET")
	util.AddRoute(mcpServers, "/names", getServers, "GET")
	util.AddRoute(mcpServers, "/start", startServer, "POST")
	util.AddRoute(mcpServers, "/stop", stopServer, "POST")
	util.AddRoute(mcpServers, "/{kind:tools|prompts|resources|templates}", getComponents, "GET")
	util.AddRoute(mcpServers, "/clear/all", clearServers, "POST")
	util.AddRoute(mcpServers, "/clear", clearServers, "POST")
	util.AddRoute(mcpServers, "/status", getStatuses, "GET")
	util.AddRoute(mcpServers, "/status/clear", clearStatus, "POST")

	util.AddRoute(mcpServerRouter, "", getServers, "GET")
	util.AddRouteQ(mcpServerRouter, "/route", setServerRoute, "uri", "POST")
	util.AddRoute(mcpServerRouter, "/start", startServer, "POST")
	util.AddRoute(mcpServerRouter, "/stop", stopServer, "POST")

	util.AddRouteQ(mcpServerRouter, "/payload/completion", addCompletionPayload, "type", "POST")
	util.AddRouteQ(mcpServerRouter, "/payload/completion/delay={delay}", addCompletionPayload, "type", "POST")

	util.AddRoute(mcpServerRouter, "/{kind:tools|prompts|resources|templates}/add", addComponent, "POST")
	util.AddRoute(mcpServerRouter, "/{kind:tools|prompts|resources|templates}", getComponents, "GET")
	util.AddRoute(mcpServerRouter, "/{kind:tools|prompts|resources|templates}/{name}", getComponents, "GET")
	util.AddRoute(mcpServerRouter, "/payload/{kind:tools|prompts|resources|templates}/{name}", addComponentPayload, "POST")
	util.AddRoute(mcpServerRouter, "/payload/{kind:tools|prompts|resources|templates}/{name}/stream/count={count}", addComponentPayload, "POST")
	util.AddRoute(mcpServerRouter, "/payload/{kind:tools|prompts|resources|templates}/{name}/stream/count={count}/delay={delay}", addComponentPayload, "POST")
	util.AddRoute(mcpServerRouter, "/clear", clearServers, "POST")

	util.AddRoute(mcpServerRouter, "/tool/{tool}/call", callTool, "POST")

	util.AddRoute(mcpServerRouter, "/status/set/{status}", setStatus, "POST")
	util.AddRoute(mcpServerRouter, "/tool/{tool}/status/set/{status}", setStatus, "POST")
	util.AddRoute(mcpServerRouter, "/status/set/{status}/header/{header}={value}", setStatus, "POST")
	util.AddRoute(mcpServerRouter, "/status/set/{status}/header/{header}", setStatus, "POST")
	util.AddRoute(mcpServerRouter, "/status/set/{status}/header/not/{header}", setStatus, "POST")
	util.AddRoute(mcpServerRouter, "/status/configure", configureStatus, "POST")
	util.AddRoute(mcpServerRouter, "/status/clear", clearStatus, "POST")
	util.AddRoute(mcpServerRouter, "/status", getStatuses, "GET")
}

// pathEndsWith tells if the path of the request is the route, which is how the handlers that are the handlers of several
// routes tell which of them is for the request. The URI of the request can't tell it, since it has the name of the server.
func pathEndsWith(r *http.Request, route string) bool {
	return strings.HasSuffix(strings.TrimRight(r.URL.Path, "/"), route)
}

func getServers(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	all := pathEndsWith(r, "/servers/all")
	names := pathEndsWith(r, "/servers/names")
	name := util.GetStringParamValue(r, "server")
	yaml := util.IsAcceptYAML(r)
	if names {
		util.WriteJsonOrYAMLPayload(w, GetMCPServerNames(), yaml)
	} else if all {
		util.WriteJsonOrYAMLPayload(w, (*PortServers)(nil).AllServers(), yaml)
	} else if name != "" {
		var server *MCPServer
		name = strings.ToLower(name)
		for _, ps := range allPortServers() {
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
		util.WriteJsonOrYAMLPayload(w, GetPortMCPServers(port), yaml)
	}
}
func addServers(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	payloads := []*MCPServerPayload{}
	err := util.ReadJsonOrYamlPayloadFromBody(r.Body, &payloads)
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
	for _, p := range payloads {
		if p == nil || p.Name == "" {
			sendBadRequest("Failed to add servers, an MCP Server has no name", w, r)
			return
		}
		if p.URIRegex != "" {
			if _, err := regexp.Compile(p.URIRegex); err != nil {
				sendBadRequest(fmt.Sprintf("Failed to add servers, MCP Server [%s] has an invalid URI regex: %s", p.Name, err.Error()), w, r)
				return
			}
		}
	}
	AddMCPServers(port, payloads)
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
	server := GetMCPServer(port, serverName)
	if server == nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("MCP Server [%s] not configured on any port", serverName)
	} else {
		SetServerRoute(uri, server)
		msg = fmt.Sprintf("MCP Server [%s] will be served over URI [%s] on port [%d]", serverName, uri, port)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func configureStatus(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	serverName := util.GetStringParamValue(r, "server")
	server := GetMCPServer(port, serverName)
	if server == nil {
		sendBadRequest(fmt.Sprintf("MCP Server [%s] not configured on any port", serverName), w, r)
		return
	}
	sc, err := StatusManager.ParseStatusConfig(r.Body)
	if err != nil {
		sendBadRequest(fmt.Sprintf("Failed to parse status config with error: %s", err.Error()), w, r)
		return
	}
	if sc.Match == nil {
		sc.Match = &status.StatusMatch{}
	}
	if sc.Match.URIMatch == nil {
		sc.Match.URIMatch = &status.StatusURIMatch{}
	}
	sc.Match.URIMatch.Exact = server.URI
	if err = StatusManager.ApplyStatusConfig(port, sc); err != nil {
		sendBadRequest(fmt.Sprintf("Failed to apply status config with error: %s", err.Error()), w, r)
		return
	}
	util.WriteJsonPayload(w, sc)
	util.AddLogMessage(fmt.Sprintf("Applied status config: %s", sc.Log("MCP", port)), r)
}
func setStatus(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	serverName := util.GetStringParamValue(r, "server")
	toolName := util.GetStringParamValue(r, "tool")
	header := util.GetStringParamValue(r, "header")
	value := util.GetStringParamValue(r, "value")
	// By the route, since the name of a server or of a header can have "not" in it.
	noHeader := strings.Contains(r.URL.Path, "/header/not/")
	statusCodes, times, ok := util.GetStatusParam(r)
	if !ok {
		util.AddLogMessage("Invalid status", r)
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintln(w, "Invalid Status")
		return
	}
	server := GetMCPServer(port, serverName)
	if server == nil {
		msg := fmt.Sprintf("MCP Server [%s] not configured on any port", serverName)
		w.WriteHeader(http.StatusBadRequest)
		util.AddLogMessage(msg, r)
		fmt.Fprintln(w, msg)
		return
	}
	uri := server.URI
	if toolName != "" {
		tool := server.GetTool(toolName)
		if tool == nil {
			msg := fmt.Sprintf("MCP Server [%s] has no tool [%s]", serverName, toolName)
			w.WriteHeader(http.StatusBadRequest)
			util.AddLogMessage(msg, r)
			fmt.Fprintln(w, msg)
			return
		}
		uri = tool.ServerURI
	}
	// The status is for the calls that have the header, or for the ones that don't if it is the "not" route.
	status := StatusManager.SetStatusFor(port, uri, "", header, value, []string{"POST"}, statusCodes, times, !noHeader)
	msg := status.Log("MCP", port)
	util.AddLogMessage(msg, r)
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, msg)
}

func clearStatus(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	serverName := util.GetStringParamValue(r, "server")
	msg := ""
	if serverName == "" {
		StatusManager.Clear(port, "", "")
		msg = fmt.Sprintf("Status cleared on port [%d]", port)
	} else if server := GetMCPServer(port, serverName); server == nil {
		sendBadRequest(fmt.Sprintf("MCP Server [%s] not configured on any port", serverName), w, r)
		return
	} else {
		clearServerStatus(port, server)
		msg = fmt.Sprintf("Status cleared on port [%d] for server [%s]", port, serverName)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

// clearServerStatus clears the statuses of the URI of the server, and of the URIs of its tools. A status that is for
// the calls that have a header can't be cleared one by one, only with the ones of the port.
func clearServerStatus(port int, server *MCPServer) {
	if server.URI == "" {
		StatusManager.Clear(port, "", "")
		return
	}
	uris := []string{server.URI}
	server.lock.RLock()
	for _, tool := range server.Tools {
		uris = append(uris, tool.ServerURI)
	}
	server.lock.RUnlock()
	type exactAndPrefix struct{ exact, prefix string }
	var configs []exactAndPrefix
	for _, sc := range StatusManager.Snapshot()[port] {
		if sc.Match == nil || sc.Match.URIMatch == nil || len(sc.Match.HeaderMatches) > 0 {
			continue
		}
		for _, uri := range uris {
			if strings.EqualFold(sc.Match.URIMatch.Exact, uri) {
				configs = append(configs, exactAndPrefix{uri, sc.Match.URIMatch.Prefix})
				break
			}
		}
	}
	for _, c := range configs {
		StatusManager.Clear(port, c.exact, c.prefix)
	}
}
func getStatuses(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	serverName := util.GetStringParamValue(r, "server")
	if serverName == "" {
		util.WriteJsonPayload(w, StatusManager.Snapshot())
		util.AddLogMessage("Delivered statuses", r)
		return
	}
	server := GetMCPServer(port, serverName)
	if server == nil {
		sendBadRequest(fmt.Sprintf("MCP Server [%s] not configured on any port", serverName), w, r)
		return
	}
	// Only reports the status, so it must not count as a use of it.
	status, times := StatusManager.PeekStatusFor(port, server.URI, nil, "")
	msg := fmt.Sprintf("Port [%d] Server [%s] Reporting Status [%d] for [%d] times", port, serverName, status, times)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}
func clearServers(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	all := pathEndsWith(r, "/servers/clear/all")
	name := util.GetStringParamValue(r, "server")
	msg := ""
	if all {
		ClearAllMCPServers()
		msg = "Cleared all MCP servers"
	} else if name != "" {
		server := GetMCPServer(port, name)
		if server == nil {
			w.WriteHeader(http.StatusBadRequest)
			msg = fmt.Sprintf("MCP Server [%s] not configured on any port", name)
		} else {
			server.Clear()
			msg = fmt.Sprintf("Cleared MCP server [%s] on port [%d]", name, port)
		}
	} else {
		ps := GetPortMCPServers(port)
		ps.Clear()
		msg = fmt.Sprintf("Cleared all MCP servers on port [%d]", port)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}
func startServer(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	name := util.GetStringParamValue(r, "server")
	ps := GetPortMCPServers(port)
	count := len(ps.allServers())
	if name != "" {
		if ps.GetMCPServer(name) == nil {
			sendBadRequest(fmt.Sprintf("MCP Server [%s] not configured on port [%d]", name, port), w, r)
			return
		}
		count = 1
	}
	ps.Start(name)
	msg := fmt.Sprintf("Started [%d] MCP Servers at port [%d]", count, port)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}
func stopServer(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	name := util.GetStringParamValue(r, "server")
	ps := GetPortMCPServers(port)
	count := len(ps.allServers())
	if name != "" {
		if ps.GetMCPServer(name) == nil {
			sendBadRequest(fmt.Sprintf("MCP Server [%s] not configured on port [%d]", name, port), w, r)
			return
		}
		count = 1
	}
	ps.Stop(name)
	msg := fmt.Sprintf("Stopped [%d] MCP Servers at port [%d]", count, port)
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
	yaml := util.IsAcceptYAML(r)
	if serverName != "" {
		server := GetMCPServer(port, serverName)
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
		ps := GetPortMCPServers(port)
		util.WriteJsonOrYAMLPayload(w, ps.GetComponents(kind), yaml)
	} else {
		util.WriteJsonOrYAMLPayload(w, GetAllComponents(kind), yaml)
	}
}

func addComponent(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	kind := util.GetStringParamValue(r, "kind")
	serverName := util.GetStringParamValue(r, "server")
	b, _ := io.ReadAll(r.Body)
	msg := ""
	server := GetMCPServer(port, serverName)
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

func addToolSchemas(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	names, err := AddToolSchemas(b)
	if err != nil {
		sendBadRequest(fmt.Sprintf("Failed to add tool schemas with error [%s]", err.Error()), w, r)
		return
	}
	msg := fmt.Sprintf("Added tool schemas: %+v", names)
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
	server := GetMCPServer(port, name)
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
	// By the route, since the name of a server or of a component can have "stream" in it.
	isStream := strings.Contains(r.URL.Path, "/stream/count=")
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
	server := GetMCPServer(port, serverName)
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
	server := GetMCPServer(port, serverName)
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
	if len(b) > 0 {
		if !json.Valid(b) {
			sendBadRequest(fmt.Sprintf("Failed to parse args of Tool [%s] from request body", toolName), w, r)
			return
		}
		args = json.RawMessage(b)
	}
	req := &gomcp.CallToolRequest{
		Params: &gomcp.CallToolParamsRaw{
			Name:      toolName,
			Arguments: args,
		},
		// There is no client on the other end of this call, so there is no session either.
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
