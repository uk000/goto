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

package label

import (
  "fmt"
  "net/http"

  . "goto/pkg/constants"
  "goto/pkg/events"
  "goto/pkg/metrics"
  "goto/pkg/server/listeners"
  "goto/pkg/util"

  "github.com/gorilla/mux"
)

var (
  Handler util.ServerHandler = util.ServerHandler{"label", SetRoutes, Middleware}
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  labelRouter := util.PathRouter(r, "/server?/label")
  util.AddRouteWithPort(labelRouter, "/set/{label}", setLabel, "PUT", "POST")
  util.AddRouteWithPort(labelRouter, "/clear", setLabel, "POST")
  util.AddRouteWithPort(labelRouter, "", getLabel)
}

func setLabel(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if label := listeners.SetListenerLabel(r); label == "" {
    msg := fmt.Sprintf("Port [%s] Label Cleared", util.GetRequestOrListenerPort(r))
    events.SendRequestEvent("Label Cleared", msg, r)
  } else {
    msg = fmt.Sprintf("Will use label %s for all responses on port %s", label, util.GetRequestOrListenerPort(r))
    events.SendRequestEvent("Label Set", msg, r)
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func getLabel(w http.ResponseWriter, r *http.Request) {
  label := listeners.GetListenerLabel(r)
  msg := fmt.Sprintf("Port [%s] Label [%s]", util.GetRequestOrListenerPort(r), label)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, "Server Label: "+label)
}

func Middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    l := listeners.GetCurrentListener(r)
    rs := util.GetRequestStore(r)
    rs.GotoProtocol = util.GotoProtocol(r.ProtoMajor == 2, l.TLS)
    if util.IsTunnelRequest(r) {
      w.Header().Add(fmt.Sprintf("%s|%d", HeaderGotoProtocol, rs.TunnelCount), rs.GotoProtocol)
    } else {
      w.Header().Add(HeaderGotoProtocol, rs.GotoProtocol)
    }
    if !util.IsAdminRequest(r) {
      metrics.UpdateProtocolRequestCount(rs.GotoProtocol, r.RequestURI)
    }
    if next != nil {
      next.ServeHTTP(w, r)
    }
  })
}
