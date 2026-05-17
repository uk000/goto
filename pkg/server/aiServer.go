package server

import (
	"fmt"
	a2aserver "goto/pkg/ai/a2a/server"
	mcpserver "goto/pkg/ai/mcp/server"
	"goto/pkg/global"
	"goto/pkg/server/intercept"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"
)

var (
	jsonRPCServer           *http.Server
	agentsHandler           http.Handler
	aiHandler               http.Handler
	jsonRPCStarted          bool
	jsonRPCListenersStarted bool
)

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

func configureAndStartAIServer(port int) error {
	jsonRPCServer = &http.Server{
		Addr:         fmt.Sprintf("0.0.0.0:%d", port),
		WriteTimeout: 10 * time.Hour,
		ReadTimeout:  10 * time.Hour,
		IdleTimeout:  1 * time.Hour,
		ConnContext:  withConnContext,
		//ConnState:    conn.ConnState,
		Handler:  h2cHandler,
		ErrorLog: log.New(io.Discard, "discard", 0),
	}
	return StartHttpServer(jsonRPCServer, true)
}
