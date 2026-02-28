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

package server

import (
	"context"
	"errors"
	"fmt"
	a2aserver "goto/pkg/ai/a2a/server"
	mcpserver "goto/pkg/ai/mcp/server"
	. "goto/pkg/constants"
	"goto/pkg/events"
	"goto/pkg/global"
	"goto/pkg/metrics"
	"goto/pkg/registry/peer"
	"goto/pkg/router"
	grpcserver "goto/pkg/rpc/grpc/server"
	"goto/pkg/server/intercept"
	"goto/pkg/server/listeners"
	"goto/pkg/server/middleware"
	"goto/pkg/server/startup"
	"goto/pkg/tunnel"
	"goto/pkg/util"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

var (
	httpServer              *http.Server
	jsonRPCServer           *http.Server
	h2s                     = &http2.Server{}
	httpHandler             http.Handler
	h2cHandler              http.Handler
	mcpHandler              http.Handler
	agentsHandler           http.Handler
	aiHandler               http.Handler
	httpStarted             bool
	jsonRPCStarted          bool
	httpListenersStarted    bool
	jsonRPCListenersStarted bool
	RootRouter              *mux.Router
)

func RunHttpServer() {
	var err error
	httpRouter := configureHTTPouter()
	gRPCHandler := GRPCHandler(httpRouter)
	h2cHandler = h2c.NewHandler(gRPCHandler, h2s)
	mcpHandler = mcpserver.MCPHandler()
	agentsHandler = a2aserver.AgentsHandler()
	aiHandler = configureAIRouter()
	httpHandler = gRPCHandler
	util.HTTPHandler = httpOnlyHandler()
	err = configureAndStartHTTPServer()
	if err != nil {
		log.Fatal(err.Error())
	}
	err = configureAndStartAIServer(global.Self.JSONRPCPort)
	if err != nil {
		log.Fatal(err.Error())
	}
	go startListeners()
	grpcserver.StartDefaultGRPCServer()
	startup.Start()
	peer.RegisterPeer(global.Self.Name, global.Self.Address)
	events.SendEventJSONDirect("Server Started", global.Self.HostLabel, listeners.GetListeners())
	WaitForHttpServer()
}

func configureHTTPouter() *mux.Router {
	coreRouter := mux.NewRouter()
	coreRouter.SkipClean(true)
	RootRouter = util.CreateRouters(coreRouter)
	middleware.LinkBaseMiddlewareChain(RootRouter)
	interceptChainRouter := RootRouter.PathPrefix("").Subrouter()
	interceptChainRouter.Use(intercept.IntereceptMiddleware(preIntercept(), postIntercept()))
	middleware.LinkMiddlewareChain(interceptChainRouter)
	return coreRouter
}

func configureAIRouter() *mux.Router {
	aiRouter := mux.NewRouter()
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

func httpOnlyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs := util.GetRequestStore(r)
		l := listeners.GetListenerForPort(rs.RequestPortNum)
		handleHTTP(l, w, r, rs)
	})
}

func configureAndStartHTTPServer() error {
	httpServer = &http.Server{
		Addr:         fmt.Sprintf("0.0.0.0:%d", global.Self.ServerPort),
		WriteTimeout: 1 * time.Minute,
		ReadTimeout:  1 * time.Minute,
		IdleTimeout:  1 * time.Minute,
		ConnContext:  withConnContext,
		//ConnState:    conn.ConnState,
		Handler:  HTTPHandler(),
		ErrorLog: log.New(io.Discard, "discard", 0),
	}
	return StartHttpServer(httpServer, false)
}

func configureAndStartAIServer(port int) error {
	jsonRPCServer = &http.Server{
		Addr:         fmt.Sprintf("0.0.0.0:%d", port),
		WriteTimeout: 10 * time.Hour,
		ReadTimeout:  10 * time.Hour,
		IdleTimeout:  1 * time.Hour,
		ConnContext:  withConnContext,
		//ConnState:    conn.ConnState,
		Handler:  HTTPHandler(),
		ErrorLog: log.New(io.Discard, "discard", 0),
	}
	return StartHttpServer(jsonRPCServer, true)
}

func StartHttpServer(server *http.Server, jsonRPC bool) error {
	if server == nil {
		return errors.New("Missing server")
	}
	if global.ServerConfig.StartupDelay > 0 {
		log.Printf("Sleeping %s before starting", global.ServerConfig.StartupDelay)
		time.Sleep(global.ServerConfig.StartupDelay)
	}
	events.StartSender()
	go func(server *http.Server) {
		time.AfterFunc(2*time.Second, func() {
			if jsonRPC {
				jsonRPCStarted = true
			} else {
				httpStarted = true
			}
		})
		if err := server.ListenAndServe(); err != nil {
			log.Println(err)
		}
		global.OnHTTPStop()
	}(server)
	return nil
}

func startListeners() {
	serverCount := 0
	if jsonRPCServer != nil {
		serverCount++
	}
	if httpServer != nil {
		serverCount++
	}
	for i := 0; i < serverCount; {
		if httpStarted && !httpListenersStarted {
			log.Printf("HTTP server [%s] is ready. Starting additional HTTP listeners.", httpServer.Addr)
			time.Sleep(1 * time.Second)
			global.OnHTTPStart(httpServer)
			httpListenersStarted = true
			i++
		} else if jsonRPCStarted && !jsonRPCListenersStarted {
			log.Printf("JSONRPC server [%s] is ready. Starting additional JSONRPC listeners.", jsonRPCServer.Addr)
			time.Sleep(1 * time.Second)
			global.OnJSONRPCStart(jsonRPCServer)
			jsonRPCListenersStarted = true
			i++
		} else {
			if !httpStarted && httpServer != nil {
				log.Printf("Waiting for HTTP server [%s] before starting additional HTTP listeners.", httpServer.Addr)
			}
			if !jsonRPCStarted && jsonRPCServer != nil {
				log.Printf("Waiting for JSONRPC server [%s] before starting additional JSONRPC listeners.", jsonRPCServer.Addr)
			}
		}
		time.Sleep(1 * time.Second)
	}
}

func ServeHTTPListener(l *listeners.Listener) {
	go func() {
		msg := ""
		var server *http.Server
		if l.IsJSONRPC {
			msg = fmt.Sprintf("Starting JSONRPC Listener [%s]", l.ListenerID)
			server = jsonRPCServer
		} else {
			msg = fmt.Sprintf("Starting HTTP Listener [%s]", l.ListenerID)
			server = httpServer
		}
		if l.TLS {
			msg += fmt.Sprintf(" With TLS [CN: %s]", l.CommonName)
		}
		log.Println(msg)
		if err := server.Serve(l.Listener); err != nil {
			log.Printf("Listener [%d]: %s", l.Port, err.Error())
		}
	}()
}

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
	go events.StopSender()
	log.Printf("Deregistering peer [%s : %s] from registry", global.Self.Name, global.Self.Address)
	go peer.DeregisterPeer(global.Self.Name, global.Self.Address)
	startup.Stop()
	StopHttpServer(httpServer)
	StopHttpServer(jsonRPCServer)
}

func StopHttpServer(server *http.Server) {
	log.Printf("HTTP Server %s started shutting down", server.Addr)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	time.Sleep(time.Second)
	server.Shutdown(ctx)
	events.SendEventJSONDirect("Server Stopped", global.Self.HostLabel, listeners.GetListeners())
	log.Printf("HTTP Server %s finished shutting down", server.Addr)
}

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
		routeHandler := router.WillRoute(rs.RequestPortNum, r)
		if routeHandler != nil {
			routeHandler.ServeHTTP(w, r)
		} else if rs.IsAdminRequest {
			handleHTTP(l, w, r, rs)
		} else {
			if rs.IsMCP || rs.IsAI {
				aiHandler.ServeHTTP(w, r)
			} else {
				handleHTTP(l, w, r, rs)
			}
			statusCodeText := strconv.Itoa(rs.StatusCode)
			metrics.UpdateURIRequestCount(r.RequestURI, statusCodeText)
			metrics.UpdatePortRequestCount(rs.RequestPort, r.RequestURI)
		}
		go PrintLogMessages(rs.StatusCode, 0, nil, w.Header(), r.Context().Value(util.RequestStoreKey).(*util.RequestStore))
		if rs.ReReader != nil {
			rs.ReReader.ReallyClose()
		}
		rs.ReportTime(w)
	})
}

func handleHTTP(l *listeners.Listener, w http.ResponseWriter, r *http.Request, rs *util.RequestStore) {
	if rs.IsGRPC {
		r.ProtoMajor = 2
		if rs.IsTLS {
			rs.IsH2 = true
			grpcserver.TheGRPCServer.Server.ServeHTTP(w, r)
		} else {
			rs.IsH2C = true
			h2cHandler.ServeHTTP(w, r)
		}
	} else if l.IsHTTP2 && (util.IsH2Upgrade(r) || r.ProtoMajor == 2) {
		if rs.IsTLS {
			rs.IsH2 = true
			httpHandler.ServeHTTP(w, r)
		} else {
			r.ProtoMajor = 2
			rs.IsH2C = true
			h2cHandler.ServeHTTP(w, r)
		}
	} else {
		httpHandler.ServeHTTP(w, r)
	}
}

func GRPCHandler(httpHandler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs := util.GetRequestStore(r)
		if rs.IsGRPC {
			grpcserver.TheGRPCServer.Server.ServeHTTP(w, r)
		} else {
			httpHandler.ServeHTTP(w, r)
		}
	})
}

func initRequestStore(w http.ResponseWriter, r *http.Request) (*http.Request, *util.RequestStore, *listeners.Listener, error) {
	_, r, rs := util.WithRequestStore(r)
	rs.ResponseWriter = w
	l := listeners.GetListenerForPort(rs.RequestPortNum)
	if l == nil {
		return nil, nil, nil, fmt.Errorf("Port [%s] not configured", rs.RequestPortNum)
	}
	rs.IsJSONRPC = rs.IsJSONRPC || l.IsJSONRPC
	rs.IsGRPC = rs.IsGRPC || l.IsGRPC
	return r, rs, l, nil
}

func preIntercept() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tunnel.CheckTunnelRequest(r)
	})
}

func postIntercept() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs := util.GetRequestStore(r)
		statusCodeText := strconv.Itoa(rs.StatusCode)
		if rs.IsTunnelRequest {
			w.Header()[HeaderGotoTunnel] = r.Header[HeaderGotoRequestedTunnel]
		} else if rs.WillProxy {
			w.Header().Add(fmt.Sprintf("Proxy-%s", HeaderGotoResponseStatus), statusCodeText)
		} else {
			w.Header().Add(HeaderGotoResponseStatus, statusCodeText)
		}
		if rs.IsTunnelConnectRequest {
			if !tunnel.HijackConnect(r, w) {
				w.Header().Add(fmt.Sprintf("%s|%d", HeaderGotoTunnelStatus, rs.TunnelCount), strconv.Itoa(http.StatusInternalServerError))
			}
		}
	})
}

func withConnContext(ctx context.Context, conn net.Conn) context.Context {
	return context.WithValue(ctx, util.ConnectionKey, conn)
}

func PrintLogMessages(statusCode, bodyLength int, payload []byte, headers http.Header, rs *util.RequestStore) {
	if (!rs.IsLockerRequest || global.Flags.EnableRegistryLockerLogs) &&
		(!rs.IsPeerEventsRequest || global.Flags.EnableRegistryEventsLogs) &&
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
		log.Printf("HTTP Log: %s\n", strings.Join(rs.LogMessages, " --> "))
		if flusher, ok := log.Writer().(http.Flusher); ok {
			flusher.Flush()
		}
	}
	rs.LogMessages = rs.LogMessages[:0]
}
