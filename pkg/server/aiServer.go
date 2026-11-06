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

package server

import (
	"fmt"
	a2aserver "goto/pkg/ai/a2a/server"
	mcpserver "goto/pkg/ai/mcp/server"
	"goto/pkg/global"
	"goto/pkg/server/intercept"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"golang.org/x/net/http2"
)

var (
	jsonRPCServer           *http.Server
	agentsHandler           http.Handler
	aiHandler               http.Handler
	jsonRPCStarted          bool
	jsonRPCListenersStarted bool
)

// RunJsonRPCServer builds the MCP and A2A handlers and the AI router, then starts the JSON-RPC server on the configured port.
func RunJsonRPCServer() {
	var err error
	mcpHandler = mcpserver.MCPHandler()
	agentsHandler = a2aserver.AgentsHandler()
	aiHandler = configureAIRouter()
	err = configureAndStartAIServer(global.Self.JSONRPCPort)
	if err != nil {
		log.Fatal(err.Error())
	}
}

// RestartJsonRPCServer closes the running JSON-RPC server and starts it again on the configured port.
func RestartJsonRPCServer() {
	if jsonRPCServer != nil {
		_ = jsonRPCServer.Close()
	}

	if err := configureAndStartAIServer(global.Self.JSONRPCPort); err != nil {
		log.Fatal(err.Error())
	}
}

// configureAIRouter creates the router that sends MCP requests to the MCP handler and AI requests to the A2A agents handler.
func configureAIRouter() *mux.Router {
	aiRouter := mux.NewRouter()
	aiRouter.SkipClean(true)
	middleware.UseCore(aiRouter)
	aiRouter.Use(intercept.IntereceptMiddleware(nil, postIntercept()))
	aiRouter.MatcherFunc(func(r *http.Request, rm *mux.RouteMatch) bool {
		return true
	}).HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs := util.GetRequestStore(r)
		if rs.IsMCP {
			mcpHandler.ServeHTTP(w, r)
		} else if rs.IsAI {
			agentsHandler.ServeHTTP(w, r)
		}
	})
	return aiRouter
}

// configureAndStartAIServer creates the h2c-enabled JSON-RPC http.Server for the given port and starts it.
func configureAndStartAIServer(port int) error {
	jsonRPCServer = &http.Server{
		Addr:         fmt.Sprintf("0.0.0.0:%d", port),
		WriteTimeout: 10 * time.Hour,
		ReadTimeout:  10 * time.Hour,
		IdleTimeout:  1 * time.Hour,
		ConnContext:  withConnContext,
		//ConnState:    conn.ConnState,
		Handler:  h2cHandler,
		ErrorLog: newServerErrorLog(),
	}
	if err := http2.ConfigureServer(jsonRPCServer, h2s); err != nil {
		log.Fatalf("Failed to configure HTTP2 on JSONRPC Server: %v", err)
	}
	return StartHttpServer(jsonRPCServer, true)
}
