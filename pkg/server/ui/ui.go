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
	"goto/pkg/server/intercept"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"log"
	"net/http"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("ui", SetRoutes, MiddlewareFunc)
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	util.AddRoute(root, "/ui/ws", handleWebsocket, "GET")
}

func handleWebsocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}
	webSockets.AddSocket(r.RemoteAddr, conn)
}

func MiddlewareFunc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var irw *intercept.InterceptResponseWriter
		if webSockets.HasOpenSockets() {
			w, irw = intercept.WithIntercept(r, w)
		}
		if next != nil {
			next.ServeHTTP(w, r)
		}
		if irw != nil {
			webSockets.Broadcast(r.RequestURI, r.Header, irw.StatusCode, irw.Header(), irw.Data)
			irw.Proceed()
		}
	})
}
