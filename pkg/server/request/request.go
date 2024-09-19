/**
 * Copyright 2024 uk
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

  "goto/pkg/server/request/body"
  "goto/pkg/server/request/filter"
  "goto/pkg/server/request/header"
  "goto/pkg/server/request/timeout"
  "goto/pkg/server/request/uri"
  "goto/pkg/util"

  "github.com/gorilla/mux"
)

var (
  Handler         util.ServerHandler   = util.ServerHandler{"request", SetRoutes, Middleware}
  requestHandlers []util.ServerHandler = []util.ServerHandler{header.Handler, body.Handler, timeout.Handler, uri.Handler, filter.Handler}
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  util.AddRoutes(util.PathRouter(r, "/server?/request"), r, root, requestHandlers...)
}

func Middleware(next http.Handler) http.Handler {
  return util.AddMiddlewares(next, requestHandlers...)
}
