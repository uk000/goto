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
	"context"
	"errors"
	"fmt"
	. "goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/metrics"
	httpproxy "goto/pkg/proxy/http"
	"goto/pkg/registry/peer"
	"goto/pkg/router"
	grpcserver "goto/pkg/rpc/grpc/server"
	"goto/pkg/server/conn"
	"goto/pkg/server/intercept"
	"goto/pkg/server/listeners"
	"goto/pkg/server/middleware"
	"goto/pkg/server/startup"
	"goto/pkg/tunnel"
	"goto/pkg/util"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

var (
	httpServer  *http.Server
	h2s         = &http2.Server{}
	coreRouter  *mux.Router
	httpHandler http.Handler
	h2cHandler  http.Handler
	mcpHandler  http.Handler
	// The servers are flagged as started by the timer that runs 2 seconds after they are started, and the flags are read by startListeners.
	httpStarted          atomic.Bool
	httpListenersStarted atomic.Bool
	RootRouter           *mux.Router
)

// RunHttpServer builds the routers, starts the HTTP and JSON-RPC servers, schedules startup config and extra listeners, and blocks until shutdown.
func RunHttpServer() {
	var err error
	configureHTTPRouter()
	gRPCHandler := GRPCHandler(coreRouter)
	//h2cHandler = h2c.NewHandler(gRPCHandler, h2s)
	h2cHandler = h2c.NewHandler(HTTPHandler(), h2s)
	httpHandler = gRPCHandler
	util.HTTPHandler = httpOnlyHandler()
	err = configureAndStartHTTPServer()
	if err != nil {
		log.Fatal(err.Error())
	}
	RunJsonRPCServer()
	global.AddListenersStartWatcher(func() {
		startup.Start()
	})
	go startListeners()
	grpcserver.StartDefaultGRPCServer()
	peer.RegisterPeer(global.Self.Name, global.Self.Address)
	WaitForHttpServer()
}

// configureHTTPRouter builds the core router, linking the admin routes and the core, intercepted and unintercepted middleware chains.
func configureHTTPRouter() {
	coreRouter = mux.NewRouter()
	coreRouter.SkipClean(true)

	adminRouter := coreRouter.PathPrefix("").Subrouter()
	adminRouter.Use(intercept.IntereceptMiddleware(preIntercept(), postIntercept()))
	middleware.SetRoutesOnly(adminRouter)

	RootRouter = util.CreateRouters(coreRouter)
	middleware.LinkCore(RootRouter)

	interceptedChainRouter := RootRouter.PathPrefix("").Subrouter()
	interceptedChainRouter.Use(intercept.IntereceptMiddleware(preIntercept(), postIntercept()))

	middleware.UseInterceptedCore(interceptedChainRouter)
	middleware.LinkIntercepted(interceptedChainRouter)

	uninterceptedChainRouter := RootRouter.PathPrefix("").Subrouter()
	middleware.LinkUnintercepted(uninterceptedChainRouter)
}

// httpOnlyHandler returns a handler that serves a request through handleHTTP using the listener of the request's port.
func httpOnlyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs := util.GetRequestStore(r)
		l := listeners.GetListenerForPort(rs.RequestPortNum)
		handleHTTP(l, w, r, rs)
	})
}

// configureAndStartHTTPServer creates the h2c-enabled main HTTP server on the server port, with connection tracking, and starts it.
func configureAndStartHTTPServer() error {
	httpServer = &http.Server{
		Addr:         fmt.Sprintf("0.0.0.0:%d", global.Self.ServerPort),
		WriteTimeout: 1 * time.Minute,
		ReadTimeout:  1 * time.Minute,
		IdleTimeout:  1 * time.Minute,
		ConnContext:  withConnContext,
		ConnState:    conn.ConnState,
		Handler:      h2cHandler,
		ErrorLog:     newServerErrorLog(),
	}
	h2s.WriteByteTimeout = httpServer.WriteTimeout
	h2s.ReadIdleTimeout = httpServer.ReadTimeout
	h2s.IdleTimeout = httpServer.IdleTimeout
	if err := http2.ConfigureServer(httpServer, h2s); err != nil {
		log.Fatalf("Failed to configure HTTP2 on HTTP Server: %v", err)
	}
	return StartHttpServer(httpServer, false)
}

// StartHttpServer runs the given server in the background after the configured startup delay and flags it as started 2 seconds later.
func StartHttpServer(server *http.Server, jsonRPC bool) error {
	if server == nil {
		return errors.New("Missing server")
	}
	if global.ServerConfig.StartupDelay > 0 {
		log.Printf("Sleeping %s before starting", global.ServerConfig.StartupDelay)
		time.Sleep(global.ServerConfig.StartupDelay)
	}
	go func(server *http.Server) {
		time.AfterFunc(2*time.Second, func() {
			if jsonRPC {
				jsonRPCStarted.Store(true)
			} else {
				httpStarted.Store(true)
			}
		})
		if err := server.ListenAndServe(); err != nil {
			log.Println(err)
		}
		global.OnHTTPStop()
	}(server)
	return nil
}

// startListeners waits until the HTTP and JSON-RPC servers are ready and then starts the additional listeners that depend on each.
func startListeners() {
	serverCount := 0
	if jsonRPCServer != nil {
		serverCount++
	}
	if httpServer != nil {
		serverCount++
	}
	for i := 0; i < serverCount; {
		if httpStarted.Load() && !httpListenersStarted.Load() {
			log.Printf("HTTP server [%s] is ready. Starting additional HTTP listeners.", httpServer.Addr)
			time.Sleep(1 * time.Second)
			global.OnHTTPStart(httpServer, h2s)
			httpListenersStarted.Store(true)
			i++
		} else if jsonRPCStarted.Load() && !jsonRPCListenersStarted.Load() {
			log.Printf("JSONRPC server [%s] is ready. Starting additional JSONRPC listeners.", jsonRPCServer.Addr)
			time.Sleep(1 * time.Second)
			global.OnJSONRPCStart(jsonRPCServer, h2s)
			jsonRPCListenersStarted.Store(true)
			i++
		} else {
			if !httpStarted.Load() && httpServer != nil {
				log.Printf("Waiting for HTTP server [%s] before starting additional HTTP listeners.", httpServer.Addr)
			}
			if !jsonRPCStarted.Load() && jsonRPCServer != nil {
				log.Printf("Waiting for JSONRPC server [%s] before starting additional JSONRPC listeners.", jsonRPCServer.Addr)
			}
		}
		time.Sleep(1 * time.Second)
	}
}

// WaitForHttpServer blocks until a stop signal, honors the shutdown delay, deregisters the peer, and gracefully stops the servers.
func WaitForHttpServer() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-c
	global.ServerConfig.Stopping = true
	log.Println("Received stop signal.")
	if global.ServerConfig.ShutdownDelay > 0 {
		log.Printf("Sleeping %s before stopping", global.ServerConfig.ShutdownDelay)
		signal.Notify(c, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
		select {
		case <-c:
			log.Printf("Received 2nd Interrupt. Really stopping now.")
			break
		case <-time.After(global.ServerConfig.ShutdownDelay):
			log.Printf("Slept long enough. Stopping now.")
			break
		}
	}
	go grpcserver.TheGRPCServer.Stop()
	log.Printf("Deregistering peer [%s : %s] from registry", global.Self.Name, global.Self.Address)
	go peer.DeregisterPeer(global.Self.Name, global.Self.Address)
	startup.Stop()
	// The listeners that have ALPN handlers of their own are served by servers of their own, which shut down with the others.
	listenersStopped := make(chan struct{})
	go func() {
		defer close(listenersStopped)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// as StopHttpServer waits before it shuts down, so that they stop accepting connections together
		time.Sleep(time.Second)
		listeners.ShutdownServers(ctx)
	}()
	StopHttpServer(httpServer)
	StopHttpServer(jsonRPCServer)
	<-listenersStopped
}

// StopHttpServer gracefully shuts down the given server with a 10 second timeout.
func StopHttpServer(server *http.Server) {
	log.Printf("HTTP Server %s started shutting down", server.Addr)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	time.Sleep(time.Second)
	server.Shutdown(ctx)
	log.Printf("HTTP Server %s finished shutting down", server.Addr)
}

// HTTPHandler returns the top-level handler that sets up the request store, picks the route, admin, proxy, AI or HTTP handler, and records metrics and the access log.
func HTTPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if global.ServerConfig.Stopping && global.Funcs.IsReadinessProbe(r) {
			util.CopyHeaders(HeaderStoppingReadinessRequest, r, w, nil, true, true, false)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		r, rs, l, err := initRequestStore(w, r)
		if err != nil {
			fmt.Fprintln(w, err.Error())
			return
		}
		rs.JSONResponse = util.IsAcceptJSON(r)
		rs.YAMLResponse = util.IsAcceptYAML(r)
		routeHandler := router.WillRoute(rs.RequestPortNum, r)
		w, statusIRW := intercept.WithIntercept(r, w)
		if routeHandler != nil {
			routeHandler.ServeHTTP(w, r)
		} else {
			loadRouter(r, rs)
			if rs.IsAdminRequest {
				handleHTTP(l, w, r, rs)
			} else {
				if rs.IsProxy {
					handleHTTP(l, w, r, rs)
				} else if rs.IsMCP || rs.IsAI {
					aiHandler.ServeHTTP(w, r)
				} else {
					handleHTTP(l, w, r, rs)
				}
			}
		}
		statusIRW.Proceed()
		rs.StatusCode = statusIRW.StatusCode
		statusCodeText := strconv.Itoa(rs.StatusCode)
		metrics.UpdateURIRequestCount(r.RequestURI, statusCodeText)
		metrics.UpdatePortRequestCount(rs.RequestPort, r.RequestURI)
		go PrintLogMessages(rs.StatusCode, 0, nil, w.Header(), r.Context().Value(util.RequestStoreKey).(*util.RequestStore))
		if rs.ReReader != nil {
			rs.ReReader.ReallyClose()
		}
	})
}

// handleHTTP dispatches the request to the admin router, gRPC server, HTTP/2 handler or the current router based on the request type.
func handleHTTP(l *listeners.Listener, w http.ResponseWriter, r *http.Request, rs *util.RequestStore) {
	if rs.IsAdminRequest {
		rs.CurrentRouter.ServeHTTP(w, r)
	} else if rs.IsGRPC {
		r.ProtoMajor = 2
		if rs.IsTLS {
			rs.IsH2 = true
		} else {
			rs.IsH2C = true
		}
		grpcserver.TheGRPCServer.Server.ServeHTTP(w, r)
	} else if l.IsHTTP2 && (util.IsH2Upgrade(r) || r.ProtoMajor == 2) {
		r.ProtoMajor = 2
		if rs.IsTLS {
			rs.IsH2 = true
		} else {
			rs.IsH2C = true
		}
		httpHandler.ServeHTTP(w, r)
	} else if rs.CurrentRouter != nil {
		rs.CurrentRouter.ServeHTTP(w, r)
	} else {
		httpHandler.ServeHTTP(w, r)
	}
}

// GRPCHandler returns a handler that serves gRPC requests with the gRPC server and all others with the current router or the given handler.
func GRPCHandler(httpHandler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs := util.GetRequestStore(r)
		if rs.IsGRPC {
			grpcserver.TheGRPCServer.Server.ServeHTTP(w, r)
		} else if rs.CurrentRouter != nil {
			rs.CurrentRouter.ServeHTTP(w, r)
		} else {
			httpHandler.ServeHTTP(w, r)
		}
	})
}

// loadRouter strips a port prefix from the request URI and picks the router for the request: proxy, root URI router, or core router.
func loadRouter(r *http.Request, rs *util.RequestStore) {
	portMatch := util.PortRouteRegexp.FindStringSubmatch(r.RequestURI)
	if len(portMatch) > 1 && portMatch[0] != "" && portMatch[1] != "" {
		port, _ := strconv.Atoi(portMatch[1])
		if port > 0 {
			rs.RequestPort = portMatch[1]
			rs.RequestPortNum = port
			rs.RequestPortChecked = true
			r.RequestURI = util.PortRouteRegexp.ReplaceAllLiteralString(r.RequestURI, "")
			r.URL.Path = util.PortRouteRegexp.ReplaceAllLiteralString(r.URL.Path, "")
			rs.RequestURI = r.RequestURI
		}
	}
	rootURI, _ := util.GetRootURI(r.RequestURI)
	var uriRouter *mux.Router
	if rootURI != "" {
		if ok, router := httpproxy.WillProxyHTTP(r); ok && router != nil {
			uriRouter = router
			rs.IsProxy = true
		}
		if uriRouter == nil {
			uriRouter = middleware.RootRouterFor(rootURI, r)
		}
		if uriRouter != nil {
			rs.CurrentRouter = uriRouter
		}
	}
	if rs.CurrentRouter == nil {
		rs.CurrentRouter = coreRouter
	}
}

// initRequestStore creates the request store and returns the listener of the request's port, failing if the port isn't configured.
func initRequestStore(w http.ResponseWriter, r *http.Request) (*http.Request, *util.RequestStore, *listeners.Listener, error) {
	_, r, rs := util.WithRequestStore(r, w)
	l := listeners.GetListenerForPort(rs.RequestPortNum)
	if l == nil {
		return nil, nil, nil, fmt.Errorf("Port [%d] not configured", rs.RequestPortNum)
	}
	rs.IsJSONRPC = rs.IsJSONRPC || l.IsJSONRPC
	rs.IsGRPC = rs.IsGRPC || l.IsGRPC
	return r, rs, l, nil
}

// preIntercept returns a handler that runs before the intercepted chain and checks for tunnel requests.
func preIntercept() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tunnel.CheckTunnelRequest(r)
	})
}

// postIntercept returns a handler that runs after the intercepted chain, completing tunnel CONNECT hijacks and sending goto trailers.
func postIntercept() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs := util.GetRequestStore(r)
		if rs.IsTunnelConnectRequest {
			if !tunnel.HijackConnect(r, w) {
				w.Header().Add(fmt.Sprintf("%s|%d", HeaderGotoTunnelStatus, rs.TunnelCount), strconv.Itoa(http.StatusInternalServerError))
			}
		}
		util.SendGotoTrailers(w, r)
	})
}

// withConnContext stores the client connection in the request context.
func withConnContext(ctx context.Context, conn net.Conn) context.Context {
	return util.WithConn(ctx, conn)
}

// PrintLogMessages emits the buffered access log of a request if the log flags and request type allow it, then resets the buffer.
func PrintLogMessages(statusCode, bodyLength int, payload []byte, headers http.Header, rs *util.RequestStore) {
	if (!rs.IsLockerRequest || global.Flags.EnableRegistryLockerLogs) &&
		(!rs.IsAdminRequest || global.Flags.EnableAdminLogs) &&
		(!rs.IsReminderRequest || global.Flags.EnableRegistryReminderLogs) &&
		(!rs.IsProbeRequest || global.Flags.EnableProbeLogs) &&
		(!rs.IsHealthRequest || global.Flags.EnablePeerHealthLogs) &&
		(!rs.IsMetricsRequest || global.Flags.EnableMetricsLogs) &&
		(!rs.IsFilteredRequest && global.Flags.EnableServerLogs) {
		if global.Flags.LogResponseHeaders {
			rs.LogMessages = append(rs.LogMessages, "Response Headers: ", util.GetHeadersLog(headers))
		}
		if statusCode == 0 {
			statusCode = 200
		}
		rs.LogMessages = append(rs.LogMessages, fmt.Sprintf("Response Status: [%d], Response Body Length: [%d]", statusCode, bodyLength))
		bodyLog := ""
		logLabel := ""
		if payload != nil && !rs.IsAdminRequest {
			if global.Flags.LogResponseMiniBody {
				logLabel = "Mini Body"
				if len(payload) > 50 {
					bodyLog = fmt.Sprintf("%s...", payload[:50])
					bodyLog += fmt.Sprintf("%s", payload[len(payload)-50:])
				} else {
					bodyLog = fmt.Sprintf("%s", payload)
				}
			} else if global.Flags.LogResponseBody {
				logLabel = "Body"
				bodyLog = fmt.Sprintf("%s", payload)
			}
			if bodyLog != "" {
				rs.LogMessages = append(rs.LogMessages, fmt.Sprintf("Response %s: [%s]", logLabel, bodyLog))
			}
		}
		log.Printf("HTTP Access Log: %s\n", strings.Join(rs.LogMessages, " --> "))
		if flusher, ok := log.Writer().(http.Flusher); ok {
			flusher.Flush()
		}
	}
	rs.LogMessages = rs.LogMessages[:0]
}
