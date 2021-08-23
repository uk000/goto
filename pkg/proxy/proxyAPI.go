// Copyright 2021 uk
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package proxy

import (
  "fmt"
  "net/http"

  "goto/pkg/server/response/status"
  "goto/pkg/util"

  "github.com/gorilla/mux"
)

var (
  Handler         util.ServerHandler = util.ServerHandler{Name: "proxy", SetRoutes: SetRoutes, Middleware: Middleware}
  internalHandler util.ServerHandler = util.ServerHandler{Name: "uri", Middleware: middleware}
  rootRouter      *mux.Router
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  rootRouter = root
  proxyRouter := util.PathRouter(r, "/proxy")
  targetsRouter := util.PathRouter(r, "/proxy/targets")
  util.AddRouteWithPort(targetsRouter, "/add", addProxyTarget, "POST")
  util.AddRouteWithPort(targetsRouter, "/{target}/remove", removeProxyTarget, "PUT", "POST")
  util.AddRouteWithPort(targetsRouter, "/{target}/enable", enableProxyTarget, "PUT", "POST")
  util.AddRouteWithPort(targetsRouter, "/{target}/disable", disableProxyTarget, "PUT", "POST")
  util.AddRouteWithPort(targetsRouter, "/{targets}/invoke", invokeProxyTargets, "POST")
  util.AddRouteWithPort(targetsRouter, "/invoke/{targets}", invokeProxyTargets, "POST")
  util.AddRouteWithPort(targetsRouter, "/clear", clearProxyTargets, "POST")
  util.AddRouteWithPort(targetsRouter, "", getProxyTargets)
  util.AddRouteWithPort(proxyRouter, "/counts", getProxyMatchCounts, "GET")
  util.AddRouteWithPort(proxyRouter, "/counts/clear", clearProxyMatchCounts, "POST")
}

func addProxyTarget(w http.ResponseWriter, r *http.Request) {
  getPortProxy(r).addProxyTarget(w, r)
}

func getRequestedProxyTarget(r *http.Request) *ProxyTarget {
  return getPortProxy(r).getRequestedProxyTarget(r)
}

func removeProxyTarget(w http.ResponseWriter, r *http.Request) {
  getPortProxy(r).removeProxyTarget(w, r)
}

func enableProxyTarget(w http.ResponseWriter, r *http.Request) {
  getPortProxy(r).enableProxyTarget(w, r)
}

func disableProxyTarget(w http.ResponseWriter, r *http.Request) {
  getPortProxy(r).disableProxyTarget(w, r)
}

func clearProxyTargets(w http.ResponseWriter, r *http.Request) {
  listenerPort := util.GetRequestOrListenerPort(r)
  proxyLock.Lock()
  defer proxyLock.Unlock()
  proxyByPort[listenerPort] = newProxy()
  w.WriteHeader(http.StatusOK)
  util.AddLogMessage("Proxy targets cleared", r)
  fmt.Fprintln(w, "Proxy targets cleared")
}

func getProxyTargets(w http.ResponseWriter, r *http.Request) {
  p := getPortProxy(r)
  util.AddLogMessage("Reporting proxy targets", r)
  util.WriteJsonPayload(w, p)
}

func getProxyMatchCounts(w http.ResponseWriter, r *http.Request) {
  p := getPortProxy(r)
  util.AddLogMessage("Reporting proxy target match counts", r)
  util.WriteJsonPayload(w, p.proxyMatchCounts)
}

func clearProxyMatchCounts(w http.ResponseWriter, r *http.Request) {
  p := getPortProxy(r)
  p.initResults()
  w.WriteHeader(http.StatusOK)
  util.AddLogMessage("Proxy target match counts cleared", r)
  fmt.Fprintln(w, "Proxy target match counts cleared")
}

func invokeProxyTargets(w http.ResponseWriter, r *http.Request) {
  pt := getPortProxy(r)
  targets := pt.getRequestedTargets(r)
  if len(targets) > 0 {
    pt.invokeTargets(targets, w, r)
  } else {
    w.WriteHeader(http.StatusNotFound)
    util.AddLogMessage("Proxy targets not found", r)
    fmt.Fprintln(w, "Proxy targets not found")
  }
}

func handleURI(w http.ResponseWriter, r *http.Request) {
  p := getPortProxy(r)
  targets := p.getMatchingTargetsForRequest(r)
  if len(targets) > 0 {
    util.AddLogMessage(fmt.Sprintf("Proxying to matching targets %s", util.ToJSONText(targets)), r)
    p.invokeTargets(targets, w, r)
  }
}

func middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    p := getPortProxy(r)
    willProxy := false
    if !util.IsAdminRequest(r) && !status.IsForcedStatus(r) && len(p.getMatchingTargetsForRequest(r)) > 0 {
      willProxy = true
    }
    if willProxy {
      p.router.ServeHTTP(w, r)
    } else if next != nil {
      next.ServeHTTP(w, r)
    }
  })
}

func Middleware(next http.Handler) http.Handler {
  return util.AddMiddlewares(next, internalHandler)
}
