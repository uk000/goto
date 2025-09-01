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
	"embed"
	"fmt"
	. "goto/pkg/constants"
	"goto/pkg/events"
	"goto/pkg/global"
	"goto/pkg/metrics"
	"goto/pkg/registry/peer"
	grpcserver "goto/pkg/rpc/grpc/server"
	"goto/pkg/scripts"
	"goto/pkg/server/conn"
	"goto/pkg/server/intercept"
	"goto/pkg/server/listeners"
	"goto/pkg/server/middleware"
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
	httpServer *http.Server
	h2s        = &http2.Server{}
	RootRouter *mux.Router
)

//go:embed ui/static/*
var staticUI embed.FS

func RunHttpServer() {
	coreRouter := mux.NewRouter()
	coreRouter.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		util.WriteJsonPayload(w, map[string]string{"version": global.Version, "commit": global.Commit})
	})
	coreRouter.HandleFunc("/ui", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, staticUI, "ui/static/index.html")
	}).Methods("GET")
	coreRouter.HandleFunc("/{k:routes|apis}", func(w http.ResponseWriter, r *http.Request) {
		coreRouter.Walk(func(route *mux.Route, router *mux.Router, ancestors []*mux.Route) error {
			path, _ := route.GetPathTemplate()
			methods, _ := route.GetMethods()
			fmt.Fprintln(w, path, methods)
			return nil
		})
	}).Methods("GET")
	RootRouter = coreRouter.PathPrefix("").Subrouter()
	RootRouter.SkipClean(true)
	util.InitListenerRouter(RootRouter)
	RootRouter.Use(ContextMiddleware)
	middleware.LinkBaseMiddlewareChain(RootRouter)
	interceptChainRouter := RootRouter.PathPrefix("").Subrouter()
	interceptChainRouter.Use(IntereceptMiddleware)
	middleware.LinkMiddlewareChain(interceptChainRouter)
	h2 := HTTPHandler(coreRouter, h2c.NewHandler(RootRouter, h2s))
	httpServer = &http.Server{
		Addr:         fmt.Sprintf("0.0.0.0:%d", global.Self.ServerPort),
		WriteTimeout: 1 * time.Minute,
		ReadTimeout:  1 * time.Minute,
		IdleTimeout:  1 * time.Minute,
		ConnContext:  withConnContext,
		ConnState:    conn.ConnState,
		Handler:      h2,
		ErrorLog:     log.New(io.Discard, "discard", 0),
	}
	grpcserver.StartDefaultGRPCServer()
	StartHttpServer(httpServer)
	RunStartupScript()
	peer.RegisterPeer(global.Self.Name, global.Self.Address)
	events.SendEventJSONDirect("Server Started", global.Self.HostLabel, listeners.GetListeners())
	WaitForHttpServer(httpServer)
}

func HTTPHandler(httpHandler, h2cHandler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, r, rs := util.WithRequestStore(r)
		rs.ResponseWriter = w
		l := listeners.GetListenerForPort(util.GetCurrentPort(r))
		if l.IsHTTP2 && (util.IsH2Upgrade(r) || r.ProtoMajor == 2) {
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
	})
}

func ContextMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if global.ServerConfig.Stopping && global.Funcs.IsReadinessProbe(r) {
			util.CopyHeaders(HeaderStoppingReadinessRequest, r, w, r.Header, true, true, false)
			w.WriteHeader(http.StatusNotFound)
		} else if next != nil {
			if err := checkRequestPort(r); err != nil {
				fmt.Fprintln(w, err.Error())
				return
			}
			ctx, rs := util.PopulateRequestStore(r)
			r = r.WithContext(withPort(ctx, util.GetRequestOrListenerPortNum(r)))
			l := listeners.GetCurrentListener(r)
			rs.IsJSONRPC = l.IsJSONRPC
			rs.IsGRPC = l.IsGRPC
			reReader := util.NewReReader(r.Body)
			r.Body = reReader
			rs.BodyLength = reReader.Length()
			next.ServeHTTP(w, r)
			statusCodeText := strconv.Itoa(rs.StatusCode)
			if !rs.IsAdminRequest {
				metrics.UpdateURIRequestCount(r.RequestURI, statusCodeText)
				metrics.UpdatePortRequestCount(util.GetListenerPort(r), r.RequestURI)
			}
			go PrintLogMessages(rs.StatusCode, rs.BodyLength, nil, w.Header(), r.Context().Value(util.RequestStoreKey).(*util.RequestStore))
			reReader.ReallyClose()
		}
	})
}

func IntereceptMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if next != nil {
			rs := util.GetRequestStore(r)
			startTime := time.Now()
			var irw *intercept.InterceptResponseWriter
			w, irw = intercept.WithIntercept(r, w)
			tunnel.CheckTunnelRequest(r)
			var statusCode int
			next.ServeHTTP(w, r)
			statusCode = http.StatusOK
			if !util.IsKnownNonTraffic(r) && irw != nil {
				statusCode = irw.StatusCode
			}
			statusCodeText := strconv.Itoa(statusCode)
			endTime := time.Now()
			if rs.IsTunnelRequest {
				w.Header().Add(fmt.Sprintf("%s-%d", HeaderGotoInAt, rs.TunnelCount), startTime.UTC().String())
				w.Header().Add(fmt.Sprintf("%s-%d", HeaderGotoOutAt, rs.TunnelCount), endTime.UTC().String())
				w.Header().Add(fmt.Sprintf("%s-%d", HeaderGotoTook, rs.TunnelCount), endTime.Sub(startTime).String())
				w.Header()[HeaderGotoTunnel] = r.Header[HeaderGotoRequestedTunnel]
			} else if rs.WillProxy {
				irw.Header().Add(fmt.Sprintf("Proxy-%s", HeaderGotoInAt), startTime.UTC().String())
				irw.Header().Add(fmt.Sprintf("Proxy-%s", HeaderGotoOutAt), endTime.UTC().String())
				irw.Header().Add(fmt.Sprintf("Proxy-%s", HeaderGotoTook), endTime.Sub(startTime).String())
				irw.Header().Add(fmt.Sprintf("Proxy-%s", HeaderGotoResponseStatus), statusCodeText)
			} else {
				w.Header().Add(HeaderGotoResponseStatus, statusCodeText)
				w.Header().Add(HeaderGotoInAt, startTime.UTC().String())
				w.Header().Add(HeaderGotoOutAt, endTime.UTC().String())
				w.Header().Add(HeaderGotoTook, endTime.Sub(startTime).String())
			}
			if rs.IsTunnelConnectRequest {
				if !tunnel.HijackConnect(r, w) {
					w.Header().Add(fmt.Sprintf("%s|%d", HeaderGotoTunnelStatus, rs.TunnelCount), strconv.Itoa(http.StatusInternalServerError))
				}
			}
			if irw != nil {
				irw.Proceed()
			}
			util.DiscardRequestBody(r)
		}
	})
}

func withConnContext(ctx context.Context, conn net.Conn) context.Context {
	return context.WithValue(ctx, util.ConnectionKey, conn)
}

func withPort(ctx context.Context, port int) context.Context {
	return context.WithValue(ctx, util.CurrentPortKey, port)
}

func checkRequestPort(r *http.Request) error {
	uri := r.RequestURI
	rs := util.GetRequestStore(r)
	if !strings.HasPrefix(uri, "/port=") {
		rs.RequestPort = util.GetListenerPort(r)
		rs.RequestPortNum, _ = strconv.Atoi(rs.RequestPort)
	} else {
		rs.RequestPort = strings.Split(strings.Split(uri, "/port=")[1], "/")[0]
		rs.RequestPortNum, _ = strconv.Atoi(rs.RequestPort)
		l := listeners.GetListenerForPort(rs.RequestPortNum)
		if l == nil {
			return fmt.Errorf("Port [%s] not configured", rs.RequestPort)
		}
	}
	rs.RequestPortChecked = true
	return nil
}

func RunStartupScript() {
	if len(global.ServerConfig.StartupScript) > 0 {
		scripts.RunCommands("startup", global.ServerConfig.StartupScript)
	}
}

func withRequestStore(r *http.Request) (ctx context.Context, r2 *http.Request, rs *util.RequestStore) {
	if v := r.Context().Value(util.RequestStoreKey); v != nil {
		rs = v.(*util.RequestStore)
		ctx = r.Context()
	} else {
		ctx, r2, rs = util.WithRequestStore(r)
	}
	return
}

func StartHttpServer(server *http.Server) {
	if global.ServerConfig.StartupDelay > 0 {
		log.Printf("Sleeping %s before starting", global.ServerConfig.StartupDelay)
		time.Sleep(global.ServerConfig.StartupDelay)
	}
	events.StartSender()
	go func() {
		log.Printf("Server %s ready", server.Addr)
		global.OnHTTPStart(server)
		if err := server.ListenAndServe(); err != nil {
			log.Println(err)
		}
		global.OnHTTPStop()
	}()
}

func ServeHTTPListener(l *listeners.Listener) {
	go func() {
		msg := fmt.Sprintf("Starting HTTP Listener [%s]", l.ListenerID)
		if l.TLS {
			msg += fmt.Sprintf(" With TLS [CN: %s]", l.CommonName)
		}
		log.Println(msg)
		if err := httpServer.Serve(l.Listener); err != nil {
			log.Printf("Listener [%d]: %s", l.Port, err.Error())
		}
	}()
}

func WaitForHttpServer(server *http.Server) {
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
	StopHttpServer(server)
}

func StopHttpServer(server *http.Server) {
	log.Printf("HTTP Server %s started shutting down", server.Addr)
	go grpcserver.TheGRPCServer.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go events.StopSender()
	log.Printf("Deregistering peer [%s : %s] from registry", global.Self.Name, global.Self.Address)
	go peer.DeregisterPeer(global.Self.Name, global.Self.Address)
	time.Sleep(time.Second)
	server.Shutdown(ctx)
	events.SendEventJSONDirect("Server Stopped", global.Self.HostLabel, listeners.GetListeners())
	log.Printf("HTTP Server %s finished shutting down", server.Addr)
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
		log.Println(strings.Join(rs.LogMessages, " --> "))
		if flusher, ok := log.Writer().(http.Flusher); ok {
			flusher.Flush()
		}
	}
	rs.LogMessages = rs.LogMessages[:0]
}
