package mcpclient

import (
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
	mcpClientRouter := util.PathRouter(r, "/mcp/client")

	util.AddRouteWithPort(mcpClientRouter, "/details", getDetails, "GET")

	util.AddRouteMultiQWithPort(mcpClientRouter, "/list/all", listTools, []string{"url", "sse"}, "POST", "GET")
	util.AddRouteMultiQWithPort(mcpClientRouter, "/list/tools", listTools, []string{"url", "sse"}, "POST", "GET")
	util.AddRouteMultiQWithPort(mcpClientRouter, "/list/tools/names", listTools, []string{"url", "sse"}, "POST", "GET")
	util.AddRouteMultiQWithPort(mcpClientRouter, "/call", callTool, []string{"url", "server", "tool", "sse"}, "POST")
	util.AddRouteWithPort(mcpClientRouter, "/payload/{kind:sample|elicit}", addClientPayload, "POST")
	util.AddRouteWithPort(mcpClientRouter, "/payload/roots", addRoots, "POST")
}

func listTools(w http.ResponseWriter, r *http.Request) {
	url := util.GetStringParamValue(r, "url")
	sse := util.GetBoolParamValue(r, "sse")
	toolsOnly := strings.Contains(r.RequestURI, "tools")
	namesOnly := strings.Contains(r.RequestURI, "names")
	msg := ""
	operationLabel := fmt.Sprintf("[%s][tool/list]", global.Self.Name)
	client := NewClient(util.GetCurrentPort(r), sse, "Client", operationLabel)
	err := client.Connect(url)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Failed to connect to url %s with error [%s]", url, err.Error())
		return
	}
	var toolsList *mcp.ListToolsResult
	var promptsList *mcp.ListPromptsResult
	var resourcesList *mcp.ListResourcesResult
	toolsList, err = client.ListTools()
	if err == nil && !toolsOnly {
		promptsList, err = client.ListPrompts()
	}
	if err == nil && !toolsOnly {
		resourcesList, err = client.ListResources()
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
	url := util.GetStringParamValue(r, "url")
	sse := util.GetBoolParamValue(r, "sse")
	tool := util.GetStringParamValue(r, "tool")
	port := util.GetCurrentPort(r)
	b, _ := io.ReadAll(r.Body)
	msg := ""
	operationLabel := fmt.Sprintf("[%s][tool/call][%s]", global.Self.Name, tool)
	client := NewClient(port, sse, "Client", operationLabel)
	err := client.Connect(url)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Failed to connect to url %s with error [%s]", url, err.Error())
		return
	}
	var output map[string]any
	defer func() {
		if output == nil {
			log.Println("*** defer called with Nil output ***")
		}
		client.Close()
	}()
	var payload map[string]any
	if util.IsJSONContentType(r.Header) {
		payload = util.JSONFromBytes(b).Object()
	} else {
		payload = map[string]any{"text": string(b)}
	}
	payload["Goto-Client"] = util.GetCurrentListenerLabel(r)
	output, err = client.CallTool(port, tool, payload)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintln(w, err.Error())
		util.AddLogMessage(err.Error(), r)
		return
	} else {
		msg = fmt.Sprintf("Tool %s called successfully on url %s", tool, url)
	}
	client.hops.AddToOutput(output)
	util.WriteJsonPayload(w, output)
	util.AddLogMessage(msg, r)
}

func addClientPayload(w http.ResponseWriter, r *http.Request) {
	kind := util.GetStringParamValue(r, "kind")
	payload, _ := io.ReadAll(r.Body)
	msg := ""
	if err := AddPayload(kind, payload); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Failed to add client payload for kind [%s] with error [%s]", kind, err.Error())
	} else {
		msg = fmt.Sprintf("Client payload added for kind [%s] with data [%s]", kind, string(payload))
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
