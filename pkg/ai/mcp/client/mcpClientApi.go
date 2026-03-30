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

func setRoutes(r *mux.Router) {
	mcpapi := middleware.RootPath("/mcpapi")
	mcpClient := util.PathRouter(mcpapi, "/client")

	util.AddRoute(mcpClient, "/{name}?/details", getDetails, "GET")

	util.AddRouteWithMultiQ(mcpClient, "/list/all", listTools, [][]string{{"url"}, {"sse", "tls", "authority"}}, "POST", "GET")
	util.AddRouteWithMultiQ(mcpClient, "/list/tools", listTools, [][]string{{"url"}, {"sse", "tls", "authority"}}, "POST", "GET")
	util.AddRouteWithMultiQ(mcpClient, "/list/tools/names", listTools, [][]string{{"url"}, {"sse", "tls", "authority"}}, "POST", "GET")

	util.AddRoute(mcpClient, "/call", callTool, "POST")

	util.AddRoute(mcpClient, "/{name}?/payload/{kind:sample|elicit}", addClientPayload, "POST")
	util.AddRoute(mcpClient, "/{name}?/payload/roots", addRoots, "POST")
}

func listTools(w http.ResponseWriter, r *http.Request) {
	rs := util.GetRequestStore(r)
	url := util.GetStringParamValue(r, "url")
	sse := util.GetBoolParamValue(r, "sse")
	tls := util.GetBoolParamValue(r, "tls")
	authority := util.GetStringParamValue(r, "authority")
	toolsOnly := strings.Contains(r.RequestURI, "tools")
	namesOnly := strings.Contains(r.RequestURI, "names")
	msg := ""
	clientId := fmt.Sprintf("[%s][Client: tool/list]", global.Self.HostLabel)
	client := NewClient(rs.RequestPortNum, sse, false, tls, clientId, util.GetCurrentListenerLabel(r), authority, nil, nil)
	session := client.CreateSession(url, "tool/list", nil, nil)
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

func callTool(w http.ResponseWriter, r *http.Request) {
	rs := util.GetRequestStore(r)
	b, _ := io.ReadAll(r.Body)
	msg := ""
	tc, err := ParseToolCall(b)
	var output *MCPResult
	if err != nil || tc == nil {
		if err != nil {
			msg = fmt.Sprintf("Failed to parse tool call payload with error [%s]", err.Error())
		} else {
			err = errors.New("No tool call payload given")
		}
	} else {
		output, err = doToolCall(rs.RequestPortNum, tc, r)
		if err != nil {
			msg = err.Error()
		} else {
			msg = fmt.Sprintf("Tool %s called successfully on url %s", tc.Tool, tc.URL)
		}
	}
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintln(w, msg)
		util.AddLogMessage(msg, r)
	} else {
		util.WriteJsonPayload(w, output)
		util.AddLogMessage(msg, r)
	}
}

func doToolCall(port int, tc *ToolCall, r *http.Request) (output *MCPResult, err error) {
	clientId := fmt.Sprintf("[%s][Client: tool/call][%s]", global.Self.HostLabel, tc.Tool)
	client := NewClient(port, tc.ForceSSE, tc.H2, tc.TLS, clientId, util.GetCurrentListenerLabel(r), tc.Authority, nil, nil)
	session := client.CreateSession(tc.URL, tc.Tool, tc, r.Header)
	session.SetAuthority(tc.Authority)
	defer func() {
		if output == nil {
			log.Println("*** defer called with Nil output ***")
		}
	}()
	output, err = session.CallTool(tc, nil, r.Header)
	if err == nil {
		if output != nil && len(output.CallResults) > 0 {
			session.Hops.AddToOutput(output.CallResults[0].RemoteData)
		}
	}
	return
}

func addClientPayload(w http.ResponseWriter, r *http.Request) {
	kind := util.GetStringParamValue(r, "kind")
	name := util.GetStringParamValue(r, "name")
	payload, _ := io.ReadAll(r.Body)
	msg := ""
	if err := AddPayload(name, kind, payload); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Failed to add client payload for MCP [%s] kind [%s] with error [%s]", name, kind, err.Error())
	} else {
		msg = fmt.Sprintf("Client payload added for MCP [%s] kind [%s]", kind)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

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

func getDetails(w http.ResponseWriter, r *http.Request) {
	name := util.GetStringParamValue(r, "name")
	yaml := strings.EqualFold(r.Header.Get("Accept"), "application/yaml")
	var output any
	if name != "" {
		output = getNamedClientPayload(name)
	} else {
		output = NamedClientPayloads
	}
	util.WriteJsonOrYAMLPayload(w, output, yaml)
	util.AddLogMessage(fmt.Sprintf("Client [%s] details: [%+v]", name, output), r)
}
