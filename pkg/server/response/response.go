/**
 * Copyright 2022 uk
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

package response

import (
  "net/http"

  "goto/pkg/server/response/delay"
  "goto/pkg/server/response/header"
  "goto/pkg/server/response/payload"
  "goto/pkg/server/response/status"
  "goto/pkg/server/response/trigger"
  "goto/pkg/util"

  "github.com/gorilla/mux"
)

var (
  Handler          util.ServerHandler   = util.ServerHandler{"response", SetRoutes, Middleware}
  responseHandlers []util.ServerHandler = []util.ServerHandler{
    status.Handler, delay.Handler, header.Handler, payload.Handler, trigger.Handler}
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  util.AddRoutes(util.PathRouter(r, "/server?/response"), r, root, responseHandlers...)
}

func Middleware(next http.Handler) http.Handler {
  return util.AddMiddlewares(next, responseHandlers...)
}
