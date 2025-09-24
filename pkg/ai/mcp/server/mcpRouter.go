package mcpserver

import (
	"context"
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/proxy"
	"goto/pkg/server/conn"
	"goto/pkg/server/listeners"
	"goto/pkg/util"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func MCPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		port := util.GetRequestOrListenerPortNum(r)
		if status, rem := StatusManager.GetStatus(port); status >= 400 {
			sendStatus(status, rem, w, r)
			return
		}
		rs := util.GetRequestStore(r)
		//HandleMCPDefault(w, r)
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
		if proxy.WillProxyMCP(l.Port, r) {
			log.Printf("MCP is configured to proxy on Port [%d]. Skipping MCP processing", l.Port)
			return
		}
		server, tool := getServerAndTool(r)
		ps := GetPortMCPServers(l.Port)
		if server == nil && rs.IsMCP {
			server = ps.DefaultServer
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
			if tool != nil {
				rs.RequestURI = r.RequestURI
				rs.RequestedMCPTool = tool.Name
				log.Printf("Port [%d] Request [%s] will be served by Stateless [%t] Server [%s] for Tool [%s]", l.Port, r.RequestURI, server.Stateless, server.Name, tool.Name)
			} else {
				log.Printf("Port [%d] Request [%s] will be served by Stateless [%t] Server [%s]", l.Port, r.RequestURI, server.Stateless, server.Name)
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
		port := util.GetRequestOrListenerPortNum(r)
		conn.SendGotoHeaders(w, r)
		r = r.WithContext(util.WithContextHeaders(r.Context(), r.Header))
		//util.CopyHeaders("Request", r, w, r.Header, true, true, false)
		rs.ResponseWriter = w
		hasSSE := strings.Contains(r.RequestURI, "/sse")
		hasMCP := strings.Contains(r.RequestURI, "/mcp")
		// w, irw := intercept.WithIntercept(r, w)
		if hasMCP && !hasSSE {
			log.Printf("Port [%d] Request [%s] will be served by [%s]/stream", port, r.RequestURI, server.Name)
			Serve(server, w, r, server.streamHTTPHandler)
		} else {
			log.Printf("Port [%d] Request [%s] will be served by [%s]/sse", port, r.RequestURI, server.Name)
			r = r.WithContext(util.SetSSE(r.Context()))
			Serve(server, w, r, server.sseHandler)
		}
		rs.RequestServed = true
		// log.Println(string(irw.Data))
		// irw.Proceed()
	})
}

func Serve(server *MCPServer, w http.ResponseWriter, r *http.Request, handler http.Handler) {
	session := server.getOrSetSessionContext(r)
	switch r.Method {
	case "DELETE":
		if session != nil {
			handler.ServeHTTP(w, r)
			close(session.finished)
			server.removeSessionContext(session.SessionID)
		}
	case "GET":
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		r = r.WithContext(ctx)
		rc := make(chan bool, 1)
		requestFinished := false
		go func() {
			handler.ServeHTTP(w, r)
			close(rc)
		}()
		if session != nil {
			select {
			case <-rc:
				requestFinished = true
			case <-session.finished:
			}
			if !requestFinished {
				ctx.Done()
			} else {
			}
		} else {
			<-rc
		}
	default:
		handler.ServeHTTP(w, r)
	}
}

func getServer(r *http.Request) *gomcp.Server {
	var server *MCPServer
	port := util.GetRequestOrListenerPortNum(r)
	defer func() {
		util.PrintRequest("MCP Request Details", r)
		if server != nil {
			rs := util.GetRequestStore(r)
			rs.ResponseWriter.Header().Add("Goto-Server", server.ID)
		} else {
			log.Printf("Not handling MCP request on port [%d]", port)
		}
	}()
	server, _ = getServerAndTool(r)
	return server.server
}

func getServerAndTool(r *http.Request) (*MCPServer, *MCPTool) {
	var server *MCPServer
	port := util.GetRequestOrListenerPortNum(r)
	rs := util.GetRequestStore(r)
	uri := r.RequestURI
	uri, server = findServerForURI(uri)
	_, serverName, toolName := getPortServerToolFromURI(r.RequestURI)
	if server == nil {
		server = GetMCPServer(serverName)
		if server == nil && rs.IsMCP {
			ps := PortsServers[port]
			if ps == nil || len(ps.Servers) == 0 {
				log.Printf("Falling back to Default MCP Server [%s] on port [%d]", DefaultStatelessServer.Name, port)
				server = DefaultStatelessServer
			} else {
				server = ps.DefaultServer
				log.Printf("MCP Server [%s] not found on port [%d], using PortDefault server [%s]", serverName, port, server.Name)
			}
		}
	}
	if server != nil && !server.Enabled {
		log.Printf("MCP Server [%s] is disabled on port [%d]. Falling back to Default MCP Server [%s].", server.Name, port, DefaultStatelessServer.Name)
		server = DefaultStatelessServer
	}
	var tool *MCPTool
	if server != nil {
		tool = server.Tools[toolName]
		if toolName != "" {
			log.Printf("Server [%s] will handle MCP Tool Request [%s] based on URI match [%s] on port [%d]", server.Name, toolName, uri, port)
		} else {
			log.Printf("Server [%s] will handle MCP request based on URI match [%s] on port [%d]", server.Name, uri, port)
		}
	} else {
		log.Printf("getServerAndTool: Failed to find a server on port [%d]", port)
	}
	return server, tool
}

func findServerForURI(uri string) (matchedURI string, server *MCPServer) {
	pair := ServerRoutes[uri]
	if pair == nil {
		for uri2, pair2 := range ServerRoutes {
			s := GetMCPServer(pair2.LeftS())
			if s.uriRegexp != nil {
				if s.uriRegexp.MatchString(uri) {
					matchedURI = uri2
					server = s
					break
				}
			}
		}
	} else {
		matchedURI = uri
	}
	log.Printf("Matced URI = [%s] vs original URI: %s\n", matchedURI, uri)
	return
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
	var parts []string
	if isSSE {
		parts = strings.Split(uri, "/sse")
	} else {
		parts = strings.Split(uri, "/mcp")
	}
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

func HandleMCPDefault(w http.ResponseWriter, r *http.Request) {
	util.PrintRequest("Default MCP Handler: Request Details", r)
	l := listeners.GetCurrentListener(r)
	rs := util.GetRequestStore(r)
	hasSSE := strings.Contains(r.RequestURI, "/sse")
	hasMCP := strings.Contains(r.RequestURI, "/mcp")
	isStateful := strings.Contains(r.RequestURI, "/stateful")
	if hasMCP && !hasSSE {
		if isStateful {
			log.Printf("Port [%d] Request [%s] will be served by DefaultStatefulServer/stream", l.Port, r.RequestURI)
			Serve(DefaultStatefulServer, w, r, DefaultStatefulServer.streamHTTPHandler)
		} else {
			log.Printf("Port [%d] Request [%s] will be served by DefaultStatelessServer/stream", l.Port, r.RequestURI)
			// DefaultServer.streamHTTPHandler.ServeHTTP(w, r)
			Serve(DefaultStatefulServer, w, r, DefaultStatelessServer.streamHTTPHandler)
		}
	} else {
		if isStateful {
			log.Printf("Port [%d] Request [%s] will be served by DefaultStatefulServer/SSE", l.Port, r.RequestURI)
			Serve(DefaultStatefulServer, w, r, DefaultStatefulServer.sseHandler)
		} else {
			log.Printf("Port [%d] Request [%s] will be served by DefaultStatelessServer/SSE", l.Port, r.RequestURI)
			// DefaultServer.streamHTTPHandler.ServeHTTP(w, r)
			Serve(DefaultStatefulServer, w, r, DefaultStatelessServer.sseHandler)
		}
	}
	rs.RequestServed = true
}

func sendStatus(status, rem int, w http.ResponseWriter, r *http.Request) {
	w.Header().Add(constants.HeaderGotoForcedStatusRemaining, strconv.Itoa(rem))
	w.WriteHeader(status)
	b, _ := io.ReadAll(r.Body)
	msg := fmt.Sprintf("Reporting status [%d], Remaining status count [%d]. MCP Request Headers [%s], Payload: %s", status, rem, util.ToJSONText(r.Header), string(b))
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}
