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

package mcpclient

import (
	"errors"
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	Middleware = middleware.NewMiddleware("mcpClient", setRoutes, nil)
)

// setRoutes registers the /mcpapi/client routes for listing tools, calling a tool, adding client payloads and roots, and getting details.
func setRoutes(r *mux.Router) {
	mcpapi := middleware.RootPath("/mcpapi")
	mcpClient := util.PathRouter(mcpapi, "/client")

	util.AddRoute(mcpClient, "/{name}?/details", getDetails, "GET")

	util.AddRouteWithMultiQ(mcpClient, "/list/all", listTools, [][]string{{"url"}, {"sse", "tls", "authority"}}, "POST", "GET")
	util.AddRouteWithMultiQ(mcpClient, "/list/tools", listTools, [][]string{{"url"}, {"sse", "tls", "authority"}}, "POST", "GET")
	util.AddRouteWithMultiQ(mcpClient, "/list/tools/names", listTools, [][]string{{"url"}, {"sse", "tls", "authority"}}, "POST", "GET")

	util.AddRoute(mcpClient, "/call/tool", callTool, "POST")

	util.AddRoute(mcpClient, "/{name}?/payload/{kind:sample|elicit}", addClientPayload, "POST")
	util.AddRoute(mcpClient, "/{name}?/payload/roots", addRoots, "POST")
}

// queryParam returns the value of a param of the request. The routes group the query params, and a request that has
// only some of the params of a group is matched with the routes of other groups, which don't have the others as vars,
// so the query of the request is the fallback.
func queryParam(r *http.Request, name string) string {
	if v := util.GetStringParamValue(r, name); v != "" {
		return v
	}
	return r.URL.Query().Get(name)
}

// listTools lists the tools (and, unless tools only, the prompts and resources) of the MCP server at the URL, as names or in full.
func listTools(w http.ResponseWriter, r *http.Request) {
	rs := util.GetRequestStore(r)
	url := queryParam(r, "url")
	sse := util.IsYes(queryParam(r, "sse"))
	tls := util.IsYes(queryParam(r, "tls"))
	authority := queryParam(r, "authority")
	// What to list comes from the path of the request. The request URI has the query too, where a URL may have
	// anything, like tools.
	path := strings.TrimSuffix(r.URL.Path, "/")
	namesOnly := strings.HasSuffix(path, "/names")
	toolsOnly := strings.HasSuffix(strings.TrimSuffix(path, "/names"), "/tools")
	msg := ""
	clientId := fmt.Sprintf("MCPClient[%s][tool/list]", global.Self.HostLabel)
	client := NewClient(rs.RequestPortNum, sse, false, tls, clientId, util.GetCurrentListenerLabel(r), authority, 0, nil, nil, nil)
	session := client.CreateSession(r.Context(), url, "tool/list", nil, r.Header)
	session.SetAuthority(authority)
	var toolsList *mcp.ListToolsResult
	var promptsList *mcp.ListPromptsResult
	var resourcesList *mcp.ListResourcesResult
	toolsList, err := session.ListTools()
	if err == nil && !toolsOnly {
		promptsList, err = session.ListPrompts()
	}
	if err == nil && !toolsOnly {
		resourcesList, err = session.ListResources()
	}
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Failed to list tools/prompts/resources on url %s with error [%s]", url, err.Error())
		fmt.Fprintln(w, msg)
	} else {
		result := map[string]any{}
		names := map[string][]string{}
		if namesOnly {
			names["tools"] = []string{}
			for _, item := range toolsList.Tools {
				names["tools"] = append(names["tools"], item.Name)
			}
		} else {
			result["tools"] = toolsList.Tools
		}
		if !toolsOnly {
			if namesOnly {
				names["prompts"] = []string{}
				for _, item := range promptsList.Prompts {
					names["prompts"] = append(names["prompts"], item.Name)
				}
				names["resources"] = []string{}
				for _, item := range resourcesList.Resources {
					names["resources"] = append(names["resources"], item.Name)
				}
			} else {
				result["prompts"] = promptsList.Prompts
				result["resources"] = resourcesList.Resources
			}
		}
		if namesOnly {
			util.WriteJsonPayload(w, names)
		} else {
			util.WriteJsonPayload(w, result)
		}
		msg = fmt.Sprintf("Fetched from MCP Server [%s]: [%d] tools", url, len(toolsList.Tools))
		if !toolsOnly {
			msg += fmt.Sprintf(", [%d] prompts, [%d] resources", len(promptsList.Prompts), len(resourcesList.Resources))
		}
	}
	util.AddLogMessage(msg, r)
}

// callTool parses a tool call payload, makes the call, and responds with its result and the last response status.
func callTool(w http.ResponseWriter, r *http.Request) {
	rs := util.GetRequestStore(r)
	msg := ""
	tc, err := ParseToolCall(r.Body)
	var output *MCPResult
	responseStatus := 0
	if err != nil || tc == nil {
		if err != nil {
			msg = fmt.Sprintf("Failed to parse tool call payload with error [%s]", err.Error())
		} else {
			err = errors.New("No tool call payload given")
		}
	} else {
		output, err = doToolCall(rs.RequestPortNum, tc, w, r)
		if !util.IsNil(err) {
			msg = err.Error()
		} else {
			msg = fmt.Sprintf("Tool %s called successfully on url %s", tc.Tool, tc.URL)
		}
		if output != nil {
			if output.LastResponseStatus > 0 {
				responseStatus = output.LastResponseStatus
				// The SSE transport posts its messages, which the server acknowledges with 202. That is not the
				// status of the call.
				if tc.ForceSSE && responseStatus == http.StatusAccepted && err == nil {
					responseStatus = 0
				}
			}
			for v := range output.RemoteGotos {
				w.Header().Add(constants.HeaderViaGoto, v)
			}
		}
	}
	if responseStatus == 0 {
		if err != nil {
			responseStatus = http.StatusBadRequest
		} else {
			responseStatus = http.StatusOK
		}
	}
	w.WriteHeader(responseStatus)
	if output != nil {
		util.WriteJsonPayload(w, output)
	} else {
		fmt.Fprintln(w, msg)
	}
	util.AddLogMessage(msg, r)
}

// doToolCall makes the tool call with a new client, streaming the updates to the response unless events are disabled.
func doToolCall(port int, tc *ToolCall, w http.ResponseWriter, r *http.Request) (output *MCPResult, err error) {
	clientId := fmt.Sprintf("MCPClient[%s][tool/call][%s]", global.Self.HostLabel, tc.Tool)
	var streamHandler func(string, any, bool) error
	var endHandler func(string, any, bool)
	if !tc.NoEvents {
		streamHandler = streamMCPResponse(tc.Tool, w, r)
		endHandler = endMCPResponse(tc.Tool, w, r)
	}
	client := NewClient(port, tc.ForceSSE, tc.H2, tc.TLS, clientId, util.GetCurrentListenerLabel(r), tc.Authority, tc.RequestTimeoutD, nil, streamHandler, endHandler)
	session := client.CreateSession(r.Context(), tc.URL, clientId, tc, r.Header)
	session.SetAuthority(tc.Authority)
	defer func() {
		if output == nil {
			log.Println("*** defer called with Nil output ***")
		}
	}()
	output, err = session.CallTool(tc.Args)
	return
}

// mcpUpdateHandler writes an update of a tool call to the response, flushing it, and logs it.
func mcpUpdateHandler(msg string, data any, json bool, toolLabel string, fw http.Flusher, w http.ResponseWriter, r *http.Request) {
	if data != nil {
		msg = fmt.Sprintf("%s : %+v", msg, data)
	}
	if msg != "" {
		fmt.Fprintln(w, msg)
	}
	if fw != nil {
		fw.Flush()
	}
	util.AddLogMessage(fmt.Sprintf("Received stream response from tool [%s], response: %s", toolLabel, msg), r)
}

// streamMCPResponse returns the callback that streams the updates of a tool call to the response.
func streamMCPResponse(toolLabel string, w http.ResponseWriter, r *http.Request) func(string, any, bool) error {
	var fw http.Flusher
	if f, ok := w.(http.Flusher); ok {
		fw = f
	}
	return func(msg string, data any, json bool) error {
		mcpUpdateHandler(msg, data, json, toolLabel, fw, w, r)
		return nil
	}
}

// endMCPResponse returns the callback that writes the final update of a tool call to the response.
func endMCPResponse(toolLabel string, w http.ResponseWriter, r *http.Request) func(string, any, bool) {
	var fw http.Flusher
	if f, ok := w.(http.Flusher); ok {
		fw = f
	}
	return func(msg string, data any, json bool) {
		mcpUpdateHandler(msg, data, json, toolLabel, fw, w, r)
	}
}

// addClientPayload stores the request body as the sample or elicit payload of the named client.
func addClientPayload(w http.ResponseWriter, r *http.Request) {
	kind := util.GetStringParamValue(r, "kind")
	name := util.GetStringParamValue(r, "name")
	payload, _ := io.ReadAll(r.Body)
	msg := ""
	if err := AddPayload(name, kind, payload); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Failed to add client payload for MCP [%s] kind [%s] with error [%s]", name, kind, err.Error())
	} else {
		msg = fmt.Sprintf("Client payload added for MCP [%s] kind [%s]", name, kind)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

// addRoots stores the request body as the roots of the named client.
func addRoots(w http.ResponseWriter, r *http.Request) {
	name := util.GetStringParamValue(r, "name")
	payload, _ := io.ReadAll(r.Body)
	msg := ""
	if err := SetRoots(name, payload); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Failed to set client roots for MCP [%s] with error [%s]", name, err.Error())
	} else {
		msg = fmt.Sprintf("Client roots added for MCP [%s]: [%s]", name, string(payload))
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

// getDetails responds with the payload of the named client, or with all the client payloads, as JSON or YAML.
func getDetails(w http.ResponseWriter, r *http.Request) {
	name := util.GetStringParamValue(r, "name")
	yaml := util.IsAcceptYAML(r)
	var output any
	if name != "" {
		output = getNamedClientPayload(name)
	} else {
		output = namedClientPayloadsSnapshot()
	}
	util.WriteJsonOrYAMLPayload(w, output, yaml)
	util.AddLogMessage(fmt.Sprintf("Client [%s] details: [%+v]", name, output), r)
}
