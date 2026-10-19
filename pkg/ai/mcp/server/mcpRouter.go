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
	"context"
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/global"
	mcpproxy "goto/pkg/proxy/mcp"
	"goto/pkg/rpc/jsonrpc"
	"goto/pkg/server/intercept"
	"goto/pkg/server/listeners"
	"goto/pkg/util"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

var ()

func MCPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs := util.GetRequestStore(r)
		HandleMCP(w, r)
		if !rs.RequestServed {
			util.HTTPHandler.ServeHTTP(w, r)
		}
	})
}

func HandleMCP(w http.ResponseWriter, r *http.Request) {
	l := listeners.GetCurrentListener(r)
	rs := util.GetRequestStore(r)
	isMCP := l.IsJSONRPC || rs.IsMCP
	if isMCP && !rs.IsAdminRequest {
		if mcpproxy.WillProxyMCP(l.Port, r) {
			log.Printf("MCP is configured to proxy on Port [%d]. Skipping MCP processing", l.Port)
			return
		}
		server, tool := getServerAndTool(r)
		ps := GetPortMCPServers(l.Port)
		if server == nil && rs.IsMCP {
			server, _ = ps.getDefaultServer()
			if server == nil {
				isStateless := strings.Contains(r.RequestURI, "/stateless")
				if isStateless {
					server = DefaultStatelessServer
				} else {
					server = DefaultStatefulServer
				}
			}
		}
		if server != nil {
			w.Header().Add(constants.HeaderGotoMCPServer, server.ID)
			if tool != nil {
				w.Header().Add(constants.HeaderGotoMCPTool, tool.Name)
				rs.RequestURI = r.RequestURI
				rs.RequestedMCPTool = tool.Name
				if global.Flags.VerboseMCP {
					log.Printf("Port [%d] Request [%s] will be served by Server [%s] (Stateless=%t) for Tool [%s]", l.Port, r.RequestURI, server.Name, server.Stateless, tool.Name)
				}
			}
			server.handler.ServeHTTP(w, r)
			rs.RequestServed = true
		} else {
			log.Printf("Port [%d] Request [%s] No server available. Routing to HTTP server", l.Port, r.RequestURI)
		}
	} else {
		log.Printf("Port [%d] Request [%s] skipping MCP processing", l.Port, r.RequestURI)
	}
}

func MCPHybridHandler(server *MCPServer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs := util.GetRequestStore(r)
		r = r.WithContext(util.WithRequestHeaders(r.Context(), r.Header))
		rs.ResponseWriter = w
		hasSSE := strings.Contains(r.RequestURI, "/sse")
		hasMCP := strings.Contains(r.RequestURI, "/mcp")
		if hasMCP && !hasSSE {
			Serve(server, w, r, server.streamHTTPHandler)
		} else {
			r = r.WithContext(util.SetSSE(r.Context()))
			Serve(server, w, r, server.sseHandler)
		}
		rs.RequestServed = true
	})
}

func Serve(server *MCPServer, w http.ResponseWriter, r *http.Request, handler http.Handler) {
	if r.Header == nil {
		r.Header = http.Header{}
	}
	// The key of a request is for the server to give. One that the client sends would let it use the store of another
	// request, or of a session.
	r.Header.Del(headerRequestKey)
	sessionID := r.Header.Get(HeaderMCPSessionID)
	session := server.getOrSetSessionContext(sessionID)
	if r.Method != "GET" && r.Method != "DELETE" {
		// The request has a store of its own, since the requests of a session, and all the requests of a stateless server,
		// are served at the same time, and each of them forces its own status.
		key := newRequestKey()
		r.Header.Set(headerRequestKey, key)
		defer server.DeleteMCPSessionStore(key)
	}
	ms, rs := server.SetMCPSessionStore(r)
	switch r.Method {
	case "DELETE":
		if session != nil {
			handler.ServeHTTP(w, r)
			session.finish()
			server.removeSessionContext(session.SessionID)
			server.DeleteMCPSessionStore(sessionID)
		}
	case "GET":
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		r = r.WithContext(ctx)
		rc := make(chan struct{})
		go func() {
			defer close(rc)
			handler.ServeHTTP(w, r)
		}()
		if session != nil {
			select {
			case <-rc:
			case <-session.finished:
				// The session is over, so is the stream of it. The handler must be done with the response before this
				// returns, since the response is not valid after that.
				cancel()
				<-rc
			}
		} else {
			<-rc
		}
	default:
		var irw *intercept.InterceptResponseWriter
		var status, rem int
		stream := false
		rr := util.CreateOrGetReReader(r.Body)
		r.Body = rr
		j, err := jsonrpc.ParseJSONRPCRequest(r.Body)
		if err == nil && j != nil && j.MCPMethod != nil && j.MCPMethod.ToolsCall {
			var tool *MCPTool
			if toolName, ok := j.Params["name"].(string); ok {
				tool = server.GetTool(toolName)
			}
			// A call is forced one status at most, and uses one count of it: the one of the server, or else the one of
			// the tool. A status that is set to 0 for the server is the one of the call, which it exempts from the
			// status of the tool as well, and not one that the call passes over after using a count of it.
			_, serverLeft := StatusManager.PeekStatusFor(server.Port, server.URI, r.Header, r.Method)
			status, rem = StatusManager.GetStatusFor(server.Port, server.URI, r.Header, r.Method)
			if status == 0 && serverLeft == 0 && tool != nil {
				status, rem = StatusManager.GetStatusFor(server.Port, tool.ServerURI, r.Header, r.Method)
			}
			// The response of a call that has a forced status is held until it's complete, to send it with the status.
			stream = tool != nil && tool.Behavior.Stream && status == 0
		}
		rr.Rewind()
		w, irw = intercept.WithInterceptAndStatus(r, w, status, !stream)
		// Nothing is forced by a previous request of the session, only by this one: by the status above, or by the tool.
		setForcedStatus(ms, 0)
		if status > 0 {
			rs.StatusCode = status
		}
		handler.ServeHTTP(w, r)
		if forced := getForcedStatus(ms); forced > 0 {
			status = forced
			irw.StatusCode = forced
		}
		if status > 0 {
			sendStatus(server.ID, status, rem, irw.ResponseWriter, r)
		}
		rs.StatusCode = irw.StatusCode
		irw.Proceed()
	}
}

func getServer(r *http.Request) *gomcp.Server {
	var server *MCPServer
	port := util.GetRequestOrListenerPortNum(r)
	defer func() {
		if server != nil {
			rs := util.GetRequestStore(r)
			rs.ResponseWriter.Header().Add("Goto-Server", server.ID)
		} else {
			log.Printf("Not handling MCP request on port [%d]", port)
		}
	}()
	server, _ = getServerAndTool(r)
	if server == nil {
		return nil
	}
	return server.server
}

func serverNameOf(server *MCPServer) string {
	if server == nil {
		return "<none>"
	}
	return server.Name
}

func getServerAndTool(r *http.Request) (*MCPServer, *MCPTool) {
	var server *MCPServer
	port := util.GetRequestOrListenerPortNum(r)
	rs := util.GetRequestStore(r)
	uri := r.URL.Path
	uri, server = findServerForURI(port, uri)
	_, serverName, toolName := getPortServerToolFromURI(r.RequestURI)
	if server == nil {
		server = GetMCPServer(port, serverName)
		if server == nil && rs.IsMCP {
			ps := GetPortMCPServers(port)
			defaultServer, count := ps.getDefaultServer()
			if count == 0 {
				log.Printf("Falling back to Default MCP Server [%s] on port [%d]", serverNameOf(DefaultStatelessServer), port)
				server = DefaultStatelessServer
			} else {
				server = defaultServer
				log.Printf("MCP Server [%s] not found on port [%d], using PortDefault server [%s]", serverName, port, serverNameOf(server))
			}
		}
	}
	if server != nil && !server.isEnabled() {
		log.Printf("MCP Server [%s] is disabled on port [%d]. Falling back to Default MCP Server [%s].", server.Name, port, serverNameOf(DefaultStatelessServer))
		server = DefaultStatelessServer
	}
	var tool *MCPTool
	if server != nil {
		if toolName != "" {
			tool = server.GetTool(toolName)
			if global.Flags.VerboseMCP {
				log.Printf("Server [%s] will handle MCP Tool Request [%s] based on URI match [%s] on port [%d]", server.Name, toolName, uri, port)
			}
		} else {
			if global.Flags.VerboseMCP {
				log.Printf("Server [%s] will handle MCP request based on URI match [%s] on port [%d]", server.Name, uri, port)
			}
		}
	} else {
		log.Printf("getServerAndTool: Failed to find a server on port [%d]", port)
	}
	return server, tool
}

// findServerForURI finds the server of the port that the URI is routed to: by a route that is the URI, or else by the
// URI regexp of a server. The routes are of the servers of all ports, so a route can be of a server that the port
// doesn't have.
func findServerForURI(port int, uri string) (matchedURI string, server *MCPServer) {
	lock.RLock()
	pair := ServerRoutes[uri]
	lock.RUnlock()
	if pair != nil {
		if server = getMCPServerIfPresent(port, pair.LeftS()); server != nil {
			return uri, server
		}
	}
	routes := snapshotServerRoutes()
	uris := make([]string, 0, len(routes))
	for uri2 := range routes {
		uris = append(uris, uri2)
	}
	// In an order that doesn't change from a request to the next.
	sort.Strings(uris)
	for _, uri2 := range uris {
		if s := getMCPServerIfPresent(port, routes[uri2].LeftS()); s != nil && s.uriRegexp != nil && s.uriRegexp.MatchString(uri) {
			return uri2, s
		}
	}
	return "", nil
}

// getMCPServerIfPresent finds the server of the port, without adding the port when it has no servers.
func getMCPServerIfPresent(port int, name string) *MCPServer {
	lock.RLock()
	ps := PortsServers[port]
	lock.RUnlock()
	if ps == nil {
		return nil
	}
	return ps.GetMCPServer(name)
}

func getPortServerToolFromURI(uri string) (port int, server, tool string) {
	isMCP := strings.Contains(uri, "/mcp")
	isSSE := strings.Contains(uri, "/sse")
	if !isMCP && !isSSE {
		return
	}
	if isSSE && isMCP {
		uri = strings.ReplaceAll(uri, "/sse", "")
		isSSE = false
	}
	parts := strings.Split(uri, "/mcp")
	if len(parts) > 1 {
		subParts := strings.Split(parts[0], "=")
		if len(subParts) > 1 {
			port, _ = strconv.Atoi(subParts[1])
		}
		if strings.HasPrefix(parts[1], "/") {
			parts[1] = strings.TrimLeft(parts[1], "/")
		}
		subParts = strings.Split(parts[1], "/")
		if len(subParts) >= 1 {
			server = subParts[0]
			if len(subParts) > 1 {
				tool = subParts[1]
			}
		}
	}
	return
}

// HandleMCPDefault serves a request with the default MCP server that suits it: the stateful one for a URI that has
// "/stateful" in it, the stateless one otherwise, over the SSE transport for a URI that has "/sse" in it, or else over the
// streamable HTTP transport. The server that serves it also keeps the session of it.
func HandleMCPDefault(w http.ResponseWriter, r *http.Request) {
	rs := util.GetRequestStore(r)
	hasSSE := strings.Contains(r.RequestURI, "/sse")
	hasMCP := strings.Contains(r.RequestURI, "/mcp")
	isStateful := strings.Contains(r.RequestURI, "/stateful")
	server := DefaultStatelessServer
	if isStateful {
		server = DefaultStatefulServer
	}
	rs.RequestServed = true
	if server == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintln(w, "No default MCP server is available")
		return
	}
	if hasMCP && !hasSSE {
		Serve(server, w, r, server.streamHTTPHandler)
	} else {
		Serve(server, w, r, server.sseHandler)
	}
}

func sendStatus(id string, status, rem int, w http.ResponseWriter, r *http.Request) {
	w.Header().Add(constants.HeaderGotoForcedStatus, strconv.Itoa(status))
	w.Header().Add(constants.HeaderGotoForcedStatusRemaining, strconv.Itoa(rem))
	w.WriteHeader(status)
	rr := util.CreateOrGetReReader(r.Body)
	rr.Rewind()
	r.Body = rr
	msg := fmt.Sprintf("%s Reporting status [%d], Remaining status count [%d]. MCP Request Headers [%s], Payload: %s", id, status, rem, util.ToJSONText(r.Header), string(rr.Content))
	util.AddLogMessage(msg, r)
}
