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

package request

import (
	"net/http"

	"goto/pkg/server/middleware"
	"goto/pkg/server/request/filter"
	"goto/pkg/server/request/timeout"
	"goto/pkg/server/request/tracking"
	"goto/pkg/server/request/uri"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
	Middleware         = middleware.NewMiddleware("request", setRoutes, middlewareFunc)
	requestMiddlewares = []*middleware.Middleware{timeout.Middleware, filter.Middleware}
	CoreMiddlewares    = []*middleware.Middleware{tracking.Middleware, uri.Middleware}
)

func setRoutes(r *mux.Router) {
	server := middleware.RootPath("/server")
	requestRouter := util.PathRouter(server, "/request")
	middleware.AddRoutes(requestRouter, CoreMiddlewares...)
	middleware.AddRoutes(requestRouter, requestMiddlewares...)
}

func middlewareFunc(next http.Handler) http.Handler {
	return middleware.AddMiddlewares(next, requestMiddlewares...)
}
