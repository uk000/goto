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

func setRoutes(r *mux.Router, root *mux.Router) {
	proxyRouter := middleware.RootPath("/proxy")
	mcpProxyRouter := util.PathPrefix(proxyRouter, "/mcp")

	util.AddRouteWithMultiQ(mcpProxyRouter, "/proxy", setupMCPProxy, [][]string{{"endpoint"}, {"headers"}}, "POST")
	util.AddRouteWithMultiQ(mcpProxyRouter, "/proxy/{tool}", setupMCPProxy, [][]string{{"endpoint"}, {"to"}, {"sni", "headers"}}, "POST")
}

func setupMCPProxy(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	serverName := util.GetStringParamValue(r, "server")
	tool := util.GetStringParamValue(r, "tool")
	toTool := util.GetStringParamValue(r, "to")
	endpoint := util.GetStringParamValue(r, "endpoint")
	h, present := util.GetListParam(r, "headers")
	headers := map[string]string{}
	if present {
		for _, val := range h {
			kv := strings.Split(val, ":")
			if len(kv) == 2 {
				headers[kv[0]] = kv[1]
			}
		}
	}
	GetMCPProxyForPort(port).SetupMCPProxy(serverName, endpoint, tool, toTool, headers)
	msg := fmt.Sprintf("Setup MCP proxy at port [%d] for server [%s] tool [%s] to endpoint [%s] tool [%s]", port, serverName, tool, endpoint, toTool)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}
