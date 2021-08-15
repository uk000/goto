/**
 * Copyright 2021 uk
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

package catchall

import (
  "net/http"

  "goto/pkg/metrics"
  "goto/pkg/server/echo"
  "goto/pkg/util"

  "github.com/gorilla/mux"
)

var (
  Handler util.ServerHandler = util.ServerHandler{Name: "catchall", SetRoutes: SetRoutes}
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  r.MatcherFunc(func(r *http.Request, rm *mux.RouteMatch) bool { return true }).HandlerFunc(respond)
}

func respond(w http.ResponseWriter, r *http.Request) {
  metrics.UpdateRequestCount("catchAll")
  util.AddLogMessage("CatchAll", r)
  SendDefaultResponse(w, r)
}

func SendDefaultResponse(w http.ResponseWriter, r *http.Request) {
  response := echo.GetEchoResponse(w, r)
  response["CatchAll"] = true
  util.WriteJsonPayload(w, response)
}
