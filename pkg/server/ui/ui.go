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

package ui

import (
	"embed"
	"goto/pkg/server/intercept"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"goto/pkg/watch"
	"log"
	"net/http"

	"github.com/gorilla/mux"
)

//go:embed static/*
var staticUI embed.FS

var (
	Middleware = middleware.NewMiddleware("ui", setRoutes, middlewareFunc)
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	util.AddRouteWithPort(root, "/ui", showUI, "GET")
	util.AddRoute(root, "/ui/ws", handleWebsocket, "GET")
}

func showUI(w http.ResponseWriter, r *http.Request) {
	http.ServeFileFS(w, r, staticUI, "ui/static/index.html")
}

func handleWebsocket(w http.ResponseWriter, r *http.Request) {
	conn, err := watch.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}
	watch.WebSockets.AddSocket(r.RemoteAddr, conn)
}

func middlewareFunc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var irw *intercept.InterceptResponseWriter
		if watch.WebSockets.HasOpenSockets() {
			w, irw = intercept.WithIntercept(r, w)
		}
		if next != nil {
			next.ServeHTTP(w, r)
		}
		if irw != nil {
			watch.WebSockets.Broadcast(r.RequestURI, r.Header, irw.StatusCode, irw.Header(), irw.Data)
			irw.Proceed()
		}
	})
}
