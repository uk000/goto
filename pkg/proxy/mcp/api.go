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

package mcpproxy

import (
	"fmt"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("mcp", setRoutes, nil)
)

func setRoutes(r *mux.Router) {
	proxyRouter := middleware.RootPath("/proxy")
	mcpProxyRouter := util.PathPrefix(proxyRouter, "/mcp")

	util.AddRouteWithMultiQ(mcpProxyRouter, "/proxy", setupMCPProxy, [][]string{{"endpoint"}, {"headers"}}, "POST")
	util.AddRouteWithMultiQ(mcpProxyRouter, "/proxy/{tool}", setupMCPProxy, [][]string{{"endpoint"}, {"to"}, {"sni", "headers"}}, "POST")
}

func setupMCPProxy(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	tool := util.GetStringParamValue(r, "tool")
	toTool := util.GetStringParamValue(r, "to")
	endpoint := util.GetStringParamValue(r, "endpoint")
	// The server is given as a query param that the routes don't list, and is named after the tool when it is not given.
	serverName := util.GetStringParamValue(r, "server")
	if serverName == "" {
		serverName = r.URL.Query().Get("server")
	}
	if serverName == "" {
		serverName = tool
	}
	if serverName == "" {
		serverName = "*"
	}
	h, present := util.GetListParam(r, "headers")
	headers := map[string]string{}
	if present {
		for _, val := range h {
			// The value of a header can have colons in it.
			kv := strings.SplitN(val, ":", 2)
			if len(kv) == 2 && strings.TrimSpace(kv[0]) != "" {
				headers[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
			}
		}
	}
	msg := ""
	if err := GetMCPProxyForPort(port).SetupMCPProxy(serverName, endpoint, tool, toTool, headers); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Failed to setup MCP proxy at port [%d] for server [%s] tool [%s]: %s", port, serverName, tool, err.Error())
	} else {
		msg = fmt.Sprintf("Setup MCP proxy at port [%d] for server [%s] tool [%s] to endpoint [%s] tool [%s]", port, serverName, tool, endpoint, toTool)
	}
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}
