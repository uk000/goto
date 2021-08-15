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

package proxy

import (
  "fmt"
  "log"
  "net/http"
  "regexp"
  "strings"
  "sync"

  "goto/pkg/events"
  "goto/pkg/invocation"
  "goto/pkg/metrics"
  "goto/pkg/server/response/status"
  "goto/pkg/server/response/trigger"
  "goto/pkg/util"

  "github.com/gorilla/mux"
  "github.com/gorilla/reverse"
)

type ProxyTargetMatch struct {
  Headers      [][]string
  Uris         []string
  Query        [][]string
  MatchAllURIs bool
}

type ProxyTarget struct {
  Name           string            `json:"name"`
  URL            string            `json:"url"`
  SendID         bool              `json:"sendID"`
  ReplaceURI     string            `json:"replaceURI"`
  StripURI       string            `json:"stripURI"`
  AddHeaders     [][]string        `json:"addHeaders"`
  RemoveHeaders  []string          `json:"removeHeaders"`
  AddQuery       [][]string        `json:"addQuery"`
  RemoveQuery    []string          `json:"removeQuery"`
  MatchAny       *ProxyTargetMatch `json:"matchAny"`
  MatchAll       *ProxyTargetMatch `json:"matchAll"`
  Replicas       int               `json:"replicas"`
  Enabled        bool              `json:"enabled"`
  uriRegExp      *regexp.Regexp
  stripURIRegExp *regexp.Regexp
  captureHeaders map[string]string
  captureQuery   map[string]string
  router         *mux.Router
}

type ProxyMatchCounts struct {
  CountsByTargets            map[string]int                       `json:"countsByTargets"`
  CountsByHeaders            map[string]int                       `json:"countsByHeaders"`
  CountsByHeaderValues       map[string]map[string]int            `json:"countsByHeaderValues"`
  CountsByHeaderTargets      map[string]map[string]int            `json:"countsByHeaderTargets"`
  CountsByHeaderValueTargets map[string]map[string]map[string]int `json:"countsByHeaderValueTargets"`
  CountsByUris               map[string]int                       `json:"countsByUris"`
  CountsByUriTargets         map[string]map[string]int            `json:"countsByUriTargets"`
  CountsByQuery              map[string]int                       `json:"countsByQuery"`
  CountsByQueryValues        map[string]map[string]int            `json:"countsByQueryValues"`
  CountsByQueryTargets       map[string]map[string]int            `json:"countsByQueryTargets"`
  CountsByQueryValueTargets  map[string]map[string]map[string]int `json:"countsByQueryValueTargets"`
  lock                       sync.RWMutex
}

type Proxy struct {
  Targets          map[string]*ProxyTarget                       `json:"targets"`
  TargetsByHeaders map[string]map[string]map[string]*ProxyTarget `json:"targetsByHeaders"`
  TargetsByUris    map[string]map[string]*ProxyTarget            `json:"targetsByUris"`
  TargetsByQuery   map[string]map[string]map[string]*ProxyTarget `json:"targetsByQuery"`
  proxyMatchCounts *ProxyMatchCounts
  router           *mux.Router
  lock             sync.RWMutex
}

var (
  Handler         util.ServerHandler = util.ServerHandler{Name: "proxy", SetRoutes: SetRoutes, Middleware: Middleware}
  internalHandler util.ServerHandler = util.ServerHandler{Name: "uri", Middleware: middleware}
  rootRouter      *mux.Router
  proxyByPort     map[string]*Proxy = map[string]*Proxy{}
  proxyLock       sync.RWMutex
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

func newProxy() *Proxy {
  p := &Proxy{}
  p.Targets = map[string]*ProxyTarget{}
  p.TargetsByHeaders = map[string]map[string]map[string]*ProxyTarget{}
  p.TargetsByUris = map[string]map[string]*ProxyTarget{}
  p.TargetsByQuery = map[string]map[string]map[string]*ProxyTarget{}
  p.initResults()
  p.router = rootRouter.NewRoute().Subrouter()
  return p
}

func (p *Proxy) initResults() {
  p.proxyMatchCounts = &ProxyMatchCounts{}
  p.proxyMatchCounts.CountsByTargets = map[string]int{}
  p.proxyMatchCounts.CountsByUris = map[string]int{}
  p.proxyMatchCounts.CountsByUriTargets = map[string]map[string]int{}
  p.proxyMatchCounts.CountsByHeaders = map[string]int{}
  p.proxyMatchCounts.CountsByHeaderValues = map[string]map[string]int{}
  p.proxyMatchCounts.CountsByHeaderTargets = map[string]map[string]int{}
  p.proxyMatchCounts.CountsByHeaderValueTargets = map[string]map[string]map[string]int{}
  p.proxyMatchCounts.CountsByQuery = map[string]int{}
  p.proxyMatchCounts.CountsByQueryValues = map[string]map[string]int{}
  p.proxyMatchCounts.CountsByQueryTargets = map[string]map[string]int{}
  p.proxyMatchCounts.CountsByQueryValueTargets = map[string]map[string]map[string]int{}
}

func (p *Proxy) incrementTargetMatchCounts(t *ProxyTarget) {
  p.proxyMatchCounts.lock.Lock()
  defer p.proxyMatchCounts.lock.Unlock()
  p.proxyMatchCounts.CountsByTargets[t.Name]++
}

func (p *Proxy) incrementMatchCounts(t *ProxyTarget, uri string, header string, headerValue string, query string, queryValue string) {
  p.proxyMatchCounts.lock.Lock()
  defer p.proxyMatchCounts.lock.Unlock()
  if uri != "" {
    p.proxyMatchCounts.CountsByUris[uri]++
    if p.proxyMatchCounts.CountsByUriTargets[uri] == nil {
      p.proxyMatchCounts.CountsByUriTargets[uri] = map[string]int{}
    }
    p.proxyMatchCounts.CountsByUriTargets[uri][t.Name]++
  }
  if header != "" {
    p.proxyMatchCounts.CountsByHeaders[header]++
    if p.proxyMatchCounts.CountsByHeaderTargets[header] == nil {
      p.proxyMatchCounts.CountsByHeaderTargets[header] = map[string]int{}
    }
    p.proxyMatchCounts.CountsByHeaderTargets[header][t.Name]++
    if headerValue != "" {
      if p.proxyMatchCounts.CountsByHeaderValues[header] == nil {
        p.proxyMatchCounts.CountsByHeaderValues[header] = map[string]int{}
      }
      p.proxyMatchCounts.CountsByHeaderValues[header][headerValue]++
      if p.proxyMatchCounts.CountsByHeaderValueTargets[header] == nil {
        p.proxyMatchCounts.CountsByHeaderValueTargets[header] = map[string]map[string]int{}
      }
      if p.proxyMatchCounts.CountsByHeaderValueTargets[header][headerValue] == nil {
        p.proxyMatchCounts.CountsByHeaderValueTargets[header][headerValue] = map[string]int{}
      }
      p.proxyMatchCounts.CountsByHeaderValueTargets[header][headerValue][t.Name]++
    }
  }
  if query != "" {
    p.proxyMatchCounts.CountsByQuery[query]++
    if p.proxyMatchCounts.CountsByQueryTargets[query] == nil {
      p.proxyMatchCounts.CountsByQueryTargets[query] = map[string]int{}
    }
    p.proxyMatchCounts.CountsByQueryTargets[query][t.Name]++
    if queryValue != "" {
      if p.proxyMatchCounts.CountsByQueryValues[query] == nil {
        p.proxyMatchCounts.CountsByQueryValues[query] = map[string]int{}
      }
      p.proxyMatchCounts.CountsByQueryValues[query][queryValue]++
      if p.proxyMatchCounts.CountsByQueryValueTargets[query] == nil {
        p.proxyMatchCounts.CountsByQueryValueTargets[query] = map[string]map[string]int{}
      }
      if p.proxyMatchCounts.CountsByQueryValueTargets[query][queryValue] == nil {
        p.proxyMatchCounts.CountsByQueryValueTargets[query][queryValue] = map[string]int{}
      }
      p.proxyMatchCounts.CountsByQueryValueTargets[query][queryValue][t.Name]++
    }
  }
}

func (p *Proxy) addProxyTarget(w http.ResponseWriter, r *http.Request) {
  target := &ProxyTarget{}
  payload := util.Read(r.Body)
  if err := util.ReadJson(payload, target); err != nil {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintf(w, "Invalid target: %s\n", err.Error())
    events.SendRequestEventJSON("Proxy Target Rejected", err.Error(),
      map[string]interface{}{"error": err.Error(), "payload": payload}, r)
    return
  }
  if target.MatchAll != nil && target.MatchAny != nil {
    w.WriteHeader(http.StatusBadRequest)
    msg := "Only one of matchAll and matchAny should be specified"
    fmt.Fprintln(w, msg)
    events.SendRequestEventJSON("Proxy Target Rejected", msg,
      map[string]interface{}{"error": msg, "payload": payload}, r)
    return
  }
  if _, err := p.toInvocationSpec(target, nil); err == nil {
    p.deleteProxyTarget(target.Name)
    p.lock.Lock()
    defer p.lock.Unlock()
    if target.StripURI != "" {
      target.stripURIRegExp = regexp.MustCompile("^(.*)(" + target.StripURI + ")(/.+).*$")
    }
    p.Targets[target.Name] = target
    p.addHeaderMatch(target)
    p.addQueryMatch(target)
    if err := p.addURIMatch(target); err == nil {
      util.AddLogMessage(fmt.Sprintf("Added proxy target: %+v", target), r)
      w.WriteHeader(http.StatusOK)
      fmt.Fprintf(w, "Added proxy target: %s\n", util.ToJSON(target))
      events.SendRequestEventJSON("Proxy Target Added", target.Name, target, r)
    } else {
      w.WriteHeader(http.StatusBadRequest)
      events.SendRequestEventJSON("Proxy Target Rejected", err.Error(),
        map[string]interface{}{"error": err.Error(), "payload": payload}, r)
      fmt.Fprintf(w, "Failed to add URI Match with error: %s\n", err.Error())
    }
  } else {
    w.WriteHeader(http.StatusBadRequest)
    events.SendRequestEventJSON("Proxy Target Rejected", err.Error(),
      map[string]interface{}{"error": err.Error(), "payload": payload}, r)
    fmt.Fprintf(w, "Invalid target: %s\n", err.Error())
  }
}

func (p *Proxy) addHeaderMatch(target *ProxyTarget) {
  matchHeaders := [][]string{}
  if target.MatchAny != nil {
    matchHeaders = append(matchHeaders, target.MatchAny.Headers...)
  }
  if target.MatchAll != nil {
    matchHeaders = append(matchHeaders, target.MatchAll.Headers...)
  }
  for _, h := range matchHeaders {
    header := strings.ToLower(strings.Trim(h[0], " "))
    headerValue := ""
    if len(h) > 1 {
      headerValue = strings.ToLower(strings.Trim(h[1], " "))
      if captureKey, found := util.GetFillerUnmarked(headerValue); found {
        if target.captureHeaders == nil {
          target.captureHeaders = map[string]string{}
        }
        target.captureHeaders[header] = captureKey
        headerValue = ""
      }
    }
    targetsByHeaders, present := p.TargetsByHeaders[header]
    if !present {
      targetsByHeaders = map[string]map[string]*ProxyTarget{}
      p.TargetsByHeaders[header] = targetsByHeaders
    }
    targetsByValue, present := targetsByHeaders[headerValue]
    if !present {
      targetsByValue = map[string]*ProxyTarget{}
      targetsByHeaders[headerValue] = targetsByValue
    }
    targetsByValue[target.Name] = target
  }
}

func (p *Proxy) addQueryMatch(target *ProxyTarget) {
  matchQuery := [][]string{}
  if target.MatchAny != nil {
    matchQuery = append(matchQuery, target.MatchAny.Query...)
  }
  if target.MatchAll != nil {
    matchQuery = append(matchQuery, target.MatchAll.Query...)
  }

  for _, q := range matchQuery {
    key := strings.ToLower(strings.Trim(q[0], " "))
    value := ""
    if len(q) > 1 {
      value = strings.ToLower(strings.Trim(q[1], " "))
      if filler, found := util.GetFillerUnmarked(value); found {
        if target.captureQuery == nil {
          target.captureQuery = map[string]string{}
        }
        target.captureQuery[key] = filler
        value = ""
      }
    }
    targetsByQuery, present := p.TargetsByQuery[key]
    if !present {
      targetsByQuery = map[string]map[string]*ProxyTarget{}
      p.TargetsByQuery[key] = targetsByQuery
    }
    targetsByValue, present := targetsByQuery[value]
    if !present {
      targetsByValue = map[string]*ProxyTarget{}
      targetsByQuery[value] = targetsByValue
    }
    targetsByValue[target.Name] = target
  }
}

func (p *Proxy) addURIMatch(target *ProxyTarget) error {
  matchURIs := []string{}
  if target.MatchAny != nil {
    matchURIs = append(matchURIs, target.MatchAny.Uris...)
    for _, uri := range target.MatchAny.Uris {
      if strings.EqualFold(uri, "/") {
        target.MatchAny.MatchAllURIs = true
      }
    }
  }
  if target.MatchAll != nil {
    matchURIs = append(matchURIs, target.MatchAll.Uris...)
    for _, uri := range target.MatchAll.Uris {
      if strings.EqualFold(uri, "/") {
        target.MatchAll.MatchAllURIs = true
      }
    }
  }

  for _, uri := range matchURIs {
    uri = strings.ToLower(uri)
    uriTargets, present := p.TargetsByUris[uri]
    if !present {
      uriTargets = map[string]*ProxyTarget{}
      p.TargetsByUris[uri] = uriTargets
    }
    glob := false
    matchURI := uri
    if strings.HasSuffix(uri, "*") {
      matchURI = strings.ReplaceAll(uri, "*", "")
      glob = true
    }
    if router, re, err := util.RegisterURIRouteAndGetRegex(matchURI, glob, p.router, handleURI); err == nil {
      target.uriRegExp = re
      target.router = router
    } else {
      log.Printf("Failed to add URI match %s with error: %s\n", uri, err.Error())
      return err
    }
    uriTargets[target.Name] = target
  }
  return nil
}

func (p *Proxy) getRequestedProxyTarget(r *http.Request) *ProxyTarget {
  p.lock.RLock()
  defer p.lock.RUnlock()
  if tname, present := util.GetStringParam(r, "target"); present {
    return p.Targets[tname]
  }
  return nil
}

func (p *Proxy) deleteProxyTarget(targetName string) {
  p.lock.Lock()
  defer p.lock.Unlock()
  delete(p.Targets, targetName)
  for h, valueMap := range p.TargetsByHeaders {
    for hv, valueTargets := range valueMap {
      for name := range valueTargets {
        if name == targetName {
          delete(valueTargets, name)
        }
      }
      if len(valueTargets) == 0 {
        delete(valueMap, hv)
      }
    }
    if len(valueMap) == 0 {
      delete(p.TargetsByHeaders, h)
    }
  }
  for h, valueMap := range p.TargetsByQuery {
    for hv, valueTargets := range valueMap {
      for name := range valueTargets {
        if name == targetName {
          delete(valueTargets, name)
        }
      }
      if len(valueTargets) == 0 {
        delete(valueMap, hv)
      }
    }
    if len(valueMap) == 0 {
      delete(p.TargetsByQuery, h)
    }
  }
  for uri, uriTargets := range p.TargetsByUris {
    for name := range uriTargets {
      if name == targetName {
        delete(uriTargets, name)
      }
    }
    if len(uriTargets) == 0 {
      delete(p.TargetsByUris, uri)
    }
  }
}

func (p *Proxy) removeProxyTarget(w http.ResponseWriter, r *http.Request) {
  if t := p.getRequestedProxyTarget(r); t != nil {
    p.deleteProxyTarget(t.Name)
    util.AddLogMessage(fmt.Sprintf("Removed proxy target: %+v", t), r)
    w.WriteHeader(http.StatusOK)
    fmt.Fprintf(w, "Removed proxy target: %s\n", util.ToJSON(t))
    events.SendRequestEventJSON("Proxy Target Removed", t.Name, t, r)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintln(w, "No targets")
  }
}

func (p *Proxy) enableProxyTarget(w http.ResponseWriter, r *http.Request) {
  if t := p.getRequestedProxyTarget(r); t != nil {
    p.lock.Lock()
    defer p.lock.Unlock()
    t.Enabled = true
    msg := fmt.Sprintf("Enabled proxy target: %s", t.Name)
    util.AddLogMessage(msg, r)
    w.WriteHeader(http.StatusOK)
    fmt.Fprintf(w, util.ToJSON(map[string]string{"result": msg}))
    events.SendRequestEvent("Proxy Target Enabled", msg, r)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintln(w, "Proxy target not found")
  }
}

func (p *Proxy) disableProxyTarget(w http.ResponseWriter, r *http.Request) {
  if t := p.getRequestedProxyTarget(r); t != nil {
    p.lock.Lock()
    defer p.lock.Unlock()
    t.Enabled = false
    msg := fmt.Sprintf("Disabled proxy target: %s", t.Name)
    util.AddLogMessage(msg, r)
    events.SendRequestEvent("Proxy Target Disabled", msg, r)
    w.WriteHeader(http.StatusOK)
    fmt.Fprintf(w, util.ToJSON(map[string]string{"result": msg}))
  } else {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintln(w, "Proxy target not found")
  }
}

func (p *Proxy) getRequestedTargets(r *http.Request) map[string]*ProxyTarget {
  p.lock.RLock()
  defer p.lock.RUnlock()
  targets := map[string]*ProxyTarget{}
  if tnamesParam, present := util.GetStringParam(r, "targets"); present {
    tnames := strings.Split(tnamesParam, ",")
    for _, tname := range tnames {
      if target, found := p.Targets[tname]; found {
        targets[target.Name] = target
      }
    }
  } else {
    targets = p.Targets
  }
  return targets
}

func (p *Proxy) prepareTargetHeaders(target *ProxyTarget, r *http.Request) [][]string {
  var headers [][]string = [][]string{}
  for k, values := range r.Header {
    for _, v := range values {
      headers = append(headers, []string{k, v})
    }
  }
  for _, h := range target.AddHeaders {
    header := strings.Trim(h[0], " ")
    headerValue := ""
    if len(h) > 1 {
      headerValue = strings.Trim(h[1], " ")
    }
    if captureKey, found := util.GetFillerUnmarked(headerValue); found {
      for requestHeader, requestCaptureKey := range target.captureHeaders {
        if strings.EqualFold(captureKey, requestCaptureKey) &&
          r.Header.Get(requestHeader) != "" {
          headerValue = r.Header.Get(requestHeader)
        }
      }
    }
    headers = append(headers, []string{header, headerValue})
  }
  for _, header := range target.RemoveHeaders {
    header := strings.Trim(header, " ")
    for i, h := range headers {
      if strings.EqualFold(h[0], header) {
        headers = append(headers[:i], headers[i+1:]...)
      }
    }
  }
  return headers
}

func (p *Proxy) prepareTargetURL(target *ProxyTarget, r *http.Request) string {
  url := target.URL
  path := r.URL.Path
  if len(target.ReplaceURI) > 0 {
    forwardRoute := target.router.NewRoute().BuildOnly().Path(target.ReplaceURI)
    vars := mux.Vars(r)
    targetVars := []string{}
    if rep, err := reverse.NewGorillaPath(target.ReplaceURI, false); err == nil {
      for _, k := range rep.Groups() {
        targetVars = append(targetVars, k, vars[k])
      }
      if netURL, err := forwardRoute.URLPath(targetVars...); err == nil {
        path = netURL.Path
      } else {
        log.Printf("Failed to set vars on ReplaceURI %s with error: %s. Using ReplaceURI as is.", target.ReplaceURI, err.Error())
        path = target.ReplaceURI
      }
    } else {
      log.Printf("Failed to parse path vars from ReplaceURI %s with error: %s. Using ReplaceURI as is.", target.ReplaceURI, err.Error())
      path = target.ReplaceURI
    }
  } else if len(target.StripURI) > 0 {
    path = target.stripURIRegExp.ReplaceAllString(path, "$1$3")
  }
  url += path
  url = p.prepareTargetQuery(url, target, r)
  return url
}

func (p *Proxy) prepareTargetQuery(url string, target *ProxyTarget, r *http.Request) string {
  var params [][]string = [][]string{}
  for k, values := range r.URL.Query() {
    for _, v := range values {
      params = append(params, []string{k, v})
    }
  }
  for _, q := range target.AddQuery {
    addKey := strings.Trim(q[0], " ")
    addValue := ""
    if len(q) > 1 {
      addValue = strings.Trim(q[1], " ")
    }
    if captureKey, found := util.GetFillerUnmarked(addValue); found {
      for reqKey, requestCaptureKey := range target.captureQuery {
        if strings.EqualFold(captureKey, requestCaptureKey) && r.URL.Query().Get(reqKey) != "" {
          addValue = r.URL.Query().Get(reqKey)
        }
      }
    }
    params = append(params, []string{addKey, addValue})
  }
  for _, k := range target.RemoveQuery {
    key := strings.Trim(k, " ")
    for i, q := range params {
      if strings.EqualFold(q[0], key) {
        params = append(params[:i], params[i+1:]...)
      }
    }
  }
  if len(params) > 0 {
    url += "?"
    for _, q := range params {
      url += q[0] + "=" + q[1] + "&"
    }
    url = strings.TrimRight(url, "&")
  }
  return url
}

func (p *Proxy) toInvocationSpec(target *ProxyTarget, r *http.Request) (*invocation.InvocationSpec, error) {
  is := &invocation.InvocationSpec{}
  is.Name = target.Name
  is.Method = "GET"
  is.URL = target.URL
  is.Replicas = target.Replicas
  is.SendID = target.SendID
  if r != nil {
    is.URL = p.prepareTargetURL(target, r)
    is.Headers = p.prepareTargetHeaders(target, r)
    is.Method = r.Method
    is.BodyReader = r.Body
  }
  is.CollectResponse = true
  return is, invocation.ValidateSpec(is)
}

func (p *Proxy) invokeTargets(targets map[string]*ProxyTarget, w http.ResponseWriter, r *http.Request) {
  p.lock.Lock()
  defer p.lock.Unlock()
  if len(targets) > 0 {
    responses := []*invocation.InvocationResult{}
    for _, target := range targets {
      events.SendRequestEventJSON("Proxy Target Invoked", target.Name, target, r)
      metrics.UpdateProxiedRequestCount(target.Name)
      is, _ := p.toInvocationSpec(target, r)
      tracker := invocation.RegisterInvocation(is)
      response := invocation.StartInvocation(tracker, true)
      responses = append(responses, response...)
    }
    for _, response := range responses {
      util.CopyHeaders("", r, w, response.ResponseHeaders, false, false, true)
      if response.StatusCode == 0 {
        response.StatusCode = 503
      }
      status.IncrementStatusCount(response.StatusCode, r)
      trigger.RunTriggers(r, w, response.StatusCode)
    }
    if len(responses) == 1 {
      w.WriteHeader(responses[0].StatusCode)
      fmt.Fprintln(w, responses[0].Data)
    } else {
      w.WriteHeader(http.StatusOK)
      fmt.Fprintln(w, util.ToJSON(responses))
    }
  }
}

func getPortProxy(r *http.Request) *Proxy {
  proxyLock.RLock()
  listenerPort := util.GetRequestOrListenerPort(r)
  proxy := proxyByPort[listenerPort]
  proxyLock.RUnlock()
  if proxy == nil {
    proxyLock.Lock()
    defer proxyLock.Unlock()
    proxy = newProxy()
    proxyByPort[listenerPort] = proxy
  }
  return proxy
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

type targetMatchInfo struct {
  headers map[string]string
  query   map[string]string
  uris    []string
}

func (p *Proxy) getMatchingTargetsForRequest(r *http.Request) map[string]*ProxyTarget {
  p.lock.RLock()
  defer p.lock.RUnlock()
  targets := map[string]*ProxyTarget{}
  matchInfo := map[string]*targetMatchInfo{}
  headerValuesMap := util.GetHeaderValues(r)
  for header, valueMap := range p.TargetsByHeaders {
    if headerValues, present := headerValuesMap[header]; present {
      if valueTargets, found := valueMap[""]; found {
        for name, target := range valueTargets {
          if target.Enabled {
            targets[name] = target
            if matchInfo[target.Name] == nil {
              matchInfo[target.Name] = &targetMatchInfo{headers: map[string]string{}, query: map[string]string{}, uris: []string{}}
            }
            matchInfo[target.Name].headers[header] = ""
          }
        }
      }
      for headerValue := range headerValues {
        if len(headerValue) > 0 {
          if valueTargets, found := valueMap[headerValue]; found {
            for name, target := range valueTargets {
              if target.Enabled {
                targets[name] = target
                if matchInfo[target.Name] == nil {
                  matchInfo[target.Name] = &targetMatchInfo{headers: map[string]string{}, query: map[string]string{}, uris: []string{}}
                }
                matchInfo[target.Name].headers[header] = headerValue
              }
            }
          }
        }
      }
    }
  }
  queryParamsMap := util.GetQueryParams(r)
  for key, valueMap := range p.TargetsByQuery {
    if queryValues, present := queryParamsMap[key]; present {
      if valueTargets, found := valueMap[""]; found {
        for name, target := range valueTargets {
          if target.Enabled {
            targets[name] = target
            if matchInfo[target.Name] == nil {
              matchInfo[target.Name] = &targetMatchInfo{headers: map[string]string{}, query: map[string]string{}, uris: []string{}}
            }
            matchInfo[target.Name].query[key] = ""
          }
        }
      }
      for queryValue := range queryValues {
        if len(queryValue) > 0 {
          if valueTargets, found := valueMap[queryValue]; found {
            for name, target := range valueTargets {
              if target.Enabled {
                targets[name] = target
                if matchInfo[target.Name] == nil {
                  matchInfo[target.Name] = &targetMatchInfo{headers: map[string]string{}, query: map[string]string{}, uris: []string{}}
                }
                matchInfo[target.Name].query[key] = queryValue
              }
            }
          }
        }
      }
    }
  }
  for _, target := range p.Targets {
    if target.Enabled {
      if target.uriRegExp != nil && target.uriRegExp.MatchString(r.RequestURI) {
        targets[target.Name] = target
        if matchInfo[target.Name] == nil {
          matchInfo[target.Name] = &targetMatchInfo{headers: map[string]string{}, query: map[string]string{}, uris: []string{}}
        }
        matchInfo[target.Name].uris = append(matchInfo[target.Name].uris, r.RequestURI)
      } else if target.MatchAny != nil && target.MatchAny.MatchAllURIs ||
        target.MatchAll != nil && target.MatchAll.MatchAllURIs {
        targets[target.Name] = target
        if matchInfo[target.Name] == nil {
          matchInfo[target.Name] = &targetMatchInfo{headers: map[string]string{}, query: map[string]string{}, uris: []string{}}
        }
        matchInfo[target.Name].uris = append(matchInfo[target.Name].uris, "/")
      }
    }
  }
  targetsToBeRemoved := []string{}
  for _, t := range targets {
    m := matchInfo[t.Name]
    if t.MatchAll != nil {
      if len(t.MatchAll.Uris) > 0 && len(m.uris) == 0 ||
        len(t.MatchAll.Headers) > 0 && len(m.headers) == 0 ||
        len(t.MatchAll.Query) > 0 && len(m.query) == 0 {
        targetsToBeRemoved = append(targetsToBeRemoved, t.Name)
      }
    }
  }
  for _, t := range targetsToBeRemoved {
    delete(targets, t)
  }
  for _, t := range targets {
    p.incrementTargetMatchCounts(t)
    reported := false
    for _, uri := range matchInfo[t.Name].uris {
      if !reported {
        p.incrementMatchCounts(t, uri, "", "", "", "")
        reported = true
      }
    }
    for h, v := range matchInfo[t.Name].headers {
      if !reported {
        p.incrementMatchCounts(t, "", h, v, "", "")
        reported = true
      }
    }
    for q, v := range matchInfo[t.Name].query {
      if !reported {
        p.incrementMatchCounts(t, "", "", "", q, v)
        reported = true
      }
    }
  }
  return targets
}

func handleURI(w http.ResponseWriter, r *http.Request) {
  p := getPortProxy(r)
  targets := p.getMatchingTargetsForRequest(r)
  if len(targets) > 0 {
    util.AddLogMessage(fmt.Sprintf("Proxying to matching targets %s", util.ToJSON(targets)), r)
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
