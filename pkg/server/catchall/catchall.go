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

package catchall

import (
	"net/http"
	"time"

	"goto/pkg/metrics"
	"goto/pkg/server/echo"
	"goto/pkg/server/middleware"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("catchall", setRoutes, middlewareFunc)
)

// setRoutes registers the match-everything route that answers with the catchall response.
func setRoutes(r *mux.Router) {
	r.MatcherFunc(func(r *http.Request, rm *mux.RouteMatch) bool { return true }).HandlerFunc(respond)
}

// respond counts the request and sends the default echo response.
func respond(w http.ResponseWriter, r *http.Request) {
	metrics.UpdateRequestCount("catchAll")
	util.AddLogMessage("CatchAll", r)
	SendDefaultResponse(w, r)
}

// SendDefaultResponse writes the request's echo response plus timestamp and port as JSON or YAML.
func SendDefaultResponse(w http.ResponseWriter, r *http.Request) {
	response := echo.GetEchoResponseForRequest(w, r)
	response["At"] = time.Now().Local().Format(time.DateTime)
	response["Goto-Port"] = util.GetRequestOrListenerPort(r)
	util.WriteJsonOrYAMLPayload(w, response, util.IsAcceptYAML(r))
}

// middlewareFunc calls the next handler, or at the end of the chain sends the default response unless the request is known non-traffic.
func middlewareFunc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if next != nil {
			next.ServeHTTP(w, r)
		} else {
			rs := util.GetRequestStore(r)
			if !rs.IsKnownNonTraffic {
				SendDefaultResponse(w, r)
			}
		}
	})
}
