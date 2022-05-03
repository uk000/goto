// Copyright 2022 uk
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
  tcpTargetsRouter := util.PathRouter(r, "/proxy/tcp/targets")
  targetsRouter := util.PathRouter(r, "/proxy/targets")
  util.AddRouteWithPort(targetsRouter, "/add", addProxyTarget, "POST", "PUT")

  util.AddRouteMultiQWithPort(targetsRouter, "/add/{target}", addHTTPProxyTarget, []string{"url", "proto"}, "POST", "PUT")
  util.AddRouteMultiQWithPort(targetsRouter, "/{target}/route", addTargetRoute, []string{"from", "to"}, "PUT", "POST")
  util.AddRouteWithPort(targetsRouter, "/{target}/match/header/{key}={value}", addHeaderMatch, "PUT", "POST")
  util.AddRouteWithPort(targetsRouter, "/{target}/match/header/{key}", addHeaderMatch, "PUT", "POST")
  util.AddRouteWithPort(targetsRouter, "/{target}/match/query/{key}={value}", addQueryMatch, "PUT", "POST")
  util.AddRouteWithPort(targetsRouter, "/{target}/match/query/{key}", addQueryMatch, "PUT", "POST")
  util.AddRouteWithPort(targetsRouter, "/{target}/headers/add/{key}={value}", addTargetHeader, "PUT", "POST")
  util.AddRouteWithPort(targetsRouter, "/{target}/headers/remove/{key}", removeTargetHeader, "PUT", "POST")
  util.AddRouteWithPort(targetsRouter, "/{target}/query/add/{key}={value}", addTargetQuery, "PUT", "POST")
  util.AddRouteWithPort(targetsRouter, "/{target}/query/remove/{key}", removeTargetQuery, "PUT", "POST")

  util.AddRouteQWithPort(tcpTargetsRouter, "/add/{target}", addTCPProxyTarget, "address", "POST", "PUT")
  util.AddRouteQWithPort(tcpTargetsRouter, "/add/{target}/sni={sni}", addTCPTargetForSNI, "address", "POST", "PUT")

  util.AddRouteWithPort(targetsRouter, "/{target}/delay/set/{delay}", setProxyTargetDelay, "PUT", "POST")
  util.AddRouteWithPort(targetsRouter, "/{target}/delay/clear", clearProxyTargetDelay, "POST")
  util.AddRouteWithPort(tcpTargetsRouter, "/{target}/delay/set/{delay}", setProxyTargetDelay, "PUT", "POST")
  util.AddRouteWithPort(tcpTargetsRouter, "/{target}/delay/clear", clearProxyTargetDelay, "POST")
  util.AddRouteWithPort(tcpTargetsRouter, "/{target}/drops/set/{drops}", setProxyTargetDrops, "PUT", "POST")
  util.AddRouteWithPort(tcpTargetsRouter, "/{target}/drops/clear", clearProxyTargetDrops, "POST")
  util.AddRouteWithPort(tcpTargetsRouter, "/{target}/report", getProxyTargetReport, "GET")

  util.AddRouteWithPort(targetsRouter, "/{target}/remove", removeProxyTarget, "POST")
  util.AddRouteWithPort(targetsRouter, "/{target}/enable", enableProxyTarget, "POST")
  util.AddRouteWithPort(targetsRouter, "/{target}/disable", disableProxyTarget, "POST")
  util.AddRouteWithPort(targetsRouter, "/clear", clearProxyTargets, "POST")
  util.AddRouteWithPort(targetsRouter, "", getProxyTargets, "GET")

  util.AddRouteWithPort(proxyRouter, "/enable", enableProxy, "POST")
  util.AddRouteWithPort(proxyRouter, "/disable", disableProxy, "POST")

  util.AddRouteWithPort(proxyRouter, "/counts", getProxyMatchCounts, "GET")
  util.AddRouteWithPort(proxyRouter, "/counts/clear", clearProxyMatchCounts, "POST")
}

func addProxyTarget(w http.ResponseWriter, r *http.Request) {
  getPortProxy(r).addProxyTarget(w, r)
}

func addHTTPProxyTarget(w http.ResponseWriter, r *http.Request) {
  getPortProxy(r).addNewProxyTarget(w, r, false, false)
}

func addTCPProxyTarget(w http.ResponseWriter, r *http.Request) {
  getPortProxy(r).addNewProxyTarget(w, r, true, false)
}

func addTCPTargetForSNI(w http.ResponseWriter, r *http.Request) {
  getPortProxy(r).addNewProxyTarget(w, r, true, true)
}

func addTargetRoute(w http.ResponseWriter, r *http.Request) {
  getPortProxy(r).addTargetRoute(w, r)
}

func addHeaderMatch(w http.ResponseWriter, r *http.Request) {
  getPortProxy(r).addHeaderOrQueryMatch(w, r, true)
}

func addQueryMatch(w http.ResponseWriter, r *http.Request) {
  getPortProxy(r).addHeaderOrQueryMatch(w, r, false)
}

func addTargetHeader(w http.ResponseWriter, r *http.Request) {
  getPortProxy(r).addTargetHeaderOrQuery(w, r, true)
}

func removeTargetHeader(w http.ResponseWriter, r *http.Request) {
  getPortProxy(r).removeTargetHeaderOrQuery(w, r, true)
}

func addTargetQuery(w http.ResponseWriter, r *http.Request) {
  getPortProxy(r).addTargetHeaderOrQuery(w, r, false)
}

func removeTargetQuery(w http.ResponseWriter, r *http.Request) {
  getPortProxy(r).removeTargetHeaderOrQuery(w, r, false)
}

func getRequestedProxyTarget(r *http.Request) *ProxyTarget {
  return getPortProxy(r).getRequestedProxyTarget(r)
}

func setProxyTargetDelay(w http.ResponseWriter, r *http.Request) {
  getPortProxy(r).setProxyTargetDelay(w, r)
}

func clearProxyTargetDelay(w http.ResponseWriter, r *http.Request) {
  getPortProxy(r).clearProxyTargetDelay(w, r)
}

func setProxyTargetDrops(w http.ResponseWriter, r *http.Request) {
  getPortProxy(r).setProxyTargetDrops(w, r)
}

func clearProxyTargetDrops(w http.ResponseWriter, r *http.Request) {
  getPortProxy(r).clearProxyTargetDrops(w, r)
}

func getProxyTargetReport(w http.ResponseWriter, r *http.Request) {
  getPortProxy(r).getProxyTargetReport(w, r)
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
  listenerPort := util.GetRequestOrListenerPortNum(r)
  proxyLock.Lock()
  defer proxyLock.Unlock()
  proxyByPort[listenerPort] = newProxy(listenerPort)
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

func enableProxy(w http.ResponseWriter, r *http.Request) {
  p := getPortProxy(r)
  p.enable(true)
  w.WriteHeader(http.StatusOK)
  msg := fmt.Sprintf("Proxy enabled on port [%d]", p.Port)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func disableProxy(w http.ResponseWriter, r *http.Request) {
  p := getPortProxy(r)
  p.enable(false)
  w.WriteHeader(http.StatusOK)
  msg := fmt.Sprintf("Proxy disabled on port [%d]", p.Port)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}
