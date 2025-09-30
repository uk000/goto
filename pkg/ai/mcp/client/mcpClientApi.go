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
	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	Middleware = middleware.NewMiddleware("mcpClient", setRoutes, nil)
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	mcpapiRouter := util.PathRouter(r, "/mcpapi/client")

	util.AddRouteWithPort(mcpapiRouter, "/details", getDetails, "GET")

	util.AddRouteMultiQWithPort(mcpapiRouter, "/list/all", listTools, []string{"url", "sse", "authority"}, "POST", "GET")
	util.AddRouteMultiQWithPort(mcpapiRouter, "/list/tools", listTools, []string{"url", "sse", "authority"}, "POST", "GET")
	util.AddRouteMultiQWithPort(mcpapiRouter, "/list/tools/names", listTools, []string{"url", "sse", "authority"}, "POST", "GET")

	util.AddRouteWithPort(mcpapiRouter, "/call", callTool, "POST")

	util.AddRouteWithPort(mcpapiRouter, "/payload/{kind:sample|elicit}", addClientPayload, "POST")
	util.AddRouteWithPort(mcpapiRouter, "/payload/roots", addRoots, "POST")
}

func listTools(w http.ResponseWriter, r *http.Request) {
	url := util.GetStringParamValue(r, "url")
	sse := util.GetBoolParamValue(r, "sse")
	authority := util.GetStringParamValue(r, "authority")
	toolsOnly := strings.Contains(r.RequestURI, "tools")
	namesOnly := strings.Contains(r.RequestURI, "names")
	msg := ""
	clientId := fmt.Sprintf("[%s][Client: tool/list]", global.Self.HostLabel)
	client := NewClient(util.GetCurrentPort(r), sse, clientId, r.Header, nil)
	session, err := client.Connect(url, "tool/list", r.Header)
	session.SetAuthority(authority)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Failed to connect to url %s with error [%s]", url, err.Error())
		util.AddLogMessage(msg, r)
		fmt.Fprintln(w, msg)
		return
	}
	var toolsList *mcp.ListToolsResult
	var promptsList *mcp.ListPromptsResult
	var resourcesList *mcp.ListResourcesResult
	toolsList, err = session.ListTools()
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
	port := util.GetCurrentPort(r)
	b, _ := io.ReadAll(r.Body)
	msg := ""
	tc, err := ParseToolCall(b)
	var output map[string]any
	if err != nil || tc == nil {
		if err != nil {
			msg = fmt.Sprintf("Failed to parse tool call payload with error [%s]", err.Error())
		} else {
			err = errors.New("No tool call payload given")
		}
	} else {
		output, err = doToolCall(port, tc)
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

func doToolCall(port int, tc *ToolCall) (output map[string]any, err error) {
	clientId := fmt.Sprintf("[%s][Client: tool/call][%s]", global.Self.HostLabel, tc.Tool)
	client := NewClient(port, tc.ForceSSE, clientId, tc.Headers, nil)
	session, err := client.Connect(tc.URL, tc.Tool, tc.Headers)
	if err != nil {
		return nil, err
	}
	defer func() {
		if output == nil {
			log.Println("*** defer called with Nil output ***")
		}
		session.Close()
	}()
	output, err = session.CallTool(tc, nil)
	if err == nil {
		session.Hops.AddToOutput(output)
	}
	return
}

func addClientPayload(w http.ResponseWriter, r *http.Request) {
	kind := util.GetStringParamValue(r, "kind")
	payload, _ := io.ReadAll(r.Body)
	msg := ""
	if err := AddPayload(kind, payload); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Failed to add client payload for kind [%s] with error [%s]", kind, err.Error())
	} else {
		msg = fmt.Sprintf("Client payload added for kind [%s]", kind)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func addRoots(w http.ResponseWriter, r *http.Request) {
	var roots []*gomcp.Root
	util.ReadJsonPayloadFromBody(r.Body, &roots)
	SetRoots(roots)
	msg := fmt.Sprintf("Client roots added: [%+v]", roots)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func getDetails(w http.ResponseWriter, r *http.Request) {
	output := map[string]any{}
	output["roots"] = Roots
	output["elicit"] = ElicitPayload
	output["sample"] = SamplePayload
	util.WriteJsonPayload(w, output)
	util.AddLogMessage(fmt.Sprintf("Client details: [%+v]", output), r)
}
