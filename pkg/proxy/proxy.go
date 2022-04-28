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

  "goto/pkg/constants"
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
  Headers [][]string
  Query   [][]string
}

type TargetMatchInfo struct {
  Headers [][]string
  Query   [][]string
  URI     string
  target  *ProxyTarget
}

type ProxyTarget struct {
  Name           string            `json:"name"`
  URL            string            `json:"url"`
  Routes         map[string]string `json:"routes"`
  SendID         bool              `json:"sendID"`
  StripURI       string            `json:"stripURI"`
  AddHeaders     [][]string        `json:"addHeaders"`
  RemoveHeaders  []string          `json:"removeHeaders"`
  AddQuery       [][]string        `json:"addQuery"`
  RemoveQuery    []string          `json:"removeQuery"`
  MatchAny       *ProxyTargetMatch `json:"matchAny"`
  MatchAll       *ProxyTargetMatch `json:"matchAll"`
  Replicas       int               `json:"replicas"`
  Enabled        bool              `json:"enabled"`
  matchAllURIs   bool
  stripURIRegExp *regexp.Regexp
  headerMatch    map[string][]string
  queryMatch     map[string][]string
  captureHeaders map[string]string
  captureQuery   map[string]string
  uriRegexps     map[string]*regexp.Regexp
  uriRouters     map[string]*mux.Router
  callCount      int
  lock           sync.RWMutex
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
  Port             string                  `json:"port"`
  Targets          map[string]*ProxyTarget `json:"targets"`
  proxyMatchCounts *ProxyMatchCounts
  router           *mux.Router
  lock             sync.RWMutex
}

var (
  proxyByPort map[string]*Proxy = map[string]*Proxy{}
  proxyLock   sync.RWMutex
)

func newProxy(port string) *Proxy {
  p := &Proxy{Port: port}
  p.Targets = map[string]*ProxyTarget{}
  p.initResults()
  p.router = rootRouter.NewRoute().Subrouter()
  return p
}

func getPortProxy(r *http.Request) *Proxy {
  proxyLock.RLock()
  listenerPort := util.GetRequestOrListenerPort(r)
  proxy := proxyByPort[listenerPort]
  proxyLock.RUnlock()
  if proxy == nil {
    proxyLock.Lock()
    defer proxyLock.Unlock()
    proxy = newProxy(listenerPort)
    proxyByPort[listenerPort] = proxy
  }
  return proxy
}

func (p *Proxy) hasAnyProxy() bool {
  return len(p.Targets) > 0
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

func newProxyTarget() *ProxyTarget {
  return &ProxyTarget{
    Routes:         map[string]string{},
    Replicas:       1,
    Enabled:        true,
    headerMatch:    map[string][]string{},
    queryMatch:     map[string][]string{},
    captureHeaders: map[string]string{},
    captureQuery:   map[string]string{},
    uriRegexps:     map[string]*regexp.Regexp{},
    uriRouters:     map[string]*mux.Router{},
  }
}

func (p *Proxy) initProxyTarget(w http.ResponseWriter, r *http.Request) {
  target := newProxyTarget()
  target.Name = util.GetStringParamValue(r, "target")
  target.URL = util.GetStringParamValue(r, "url")
  p.deleteProxyTarget(target.Name)
  p.lock.Lock()
  p.Targets[target.Name] = target
  p.lock.Unlock()
  msg := fmt.Sprintf("Port [%s]: Added empty proxy target [%s] with URL [%s]", p.Port, target.Name, target.URL)
  util.AddLogMessage(msg, r)
  w.WriteHeader(http.StatusOK)
  fmt.Fprintf(w, msg)
  events.SendRequestEventJSON("Proxy Target Added", target.Name, target, r)
}

func (p *Proxy) addTargetRoute(w http.ResponseWriter, r *http.Request) {
  name := util.GetStringParamValue(r, "target")
  from := util.GetStringParamValue(r, "from")
  to := util.GetStringParamValue(r, "to")
  p.lock.RLock()
  target := p.Targets[name]
  p.lock.RUnlock()
  if target == nil {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintf(w, "Invalid target: %s\n", name)
    return
  }
  target.lock.Lock()
  target.Routes[from] = to
  target.lock.Unlock()
  p.addURIMatch(target, from)
  msg := fmt.Sprintf("Port [%s]: Added URI routing for Target [%s], URL [%s], From [%s] To [%s]", p.Port, target.Name, target.URL, from, to)
  util.AddLogMessage(msg, r)
  w.WriteHeader(http.StatusOK)
  fmt.Fprintf(w, msg)
}

func (p *Proxy) addTargetHeaderOrQueryMatch(w http.ResponseWriter, r *http.Request, isHeader bool) {
  name := util.GetStringParamValue(r, "target")
  key := util.LowerAndTrim(util.GetStringParamValue(r, "key"))
  value := util.LowerAndTrim(util.GetStringParamValue(r, "value"))
  p.lock.RLock()
  target := p.Targets[name]
  p.lock.RUnlock()
  if target == nil {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintf(w, "Invalid target: %s\n", name)
    return
  }
  msg := ""
  target.lock.Lock()
  if target.MatchAny == nil {
    target.MatchAny = &ProxyTargetMatch{}
  }
  if isHeader {
    target.MatchAny.Headers = append(target.MatchAny.Headers, []string{key, value})
    p.addHeaderMatch(target, key, value)
    msg = fmt.Sprintf("Port [%s]: Added header match criteria for Target [%s], URL [%s], Key [%s] Value [%s]", p.Port, target.Name, target.URL, key, value)
  } else {
    target.MatchAny.Query = append(target.MatchAny.Query, []string{key, value})
    p.addQueryMatch(target, key, value)
    msg = fmt.Sprintf("Port [%s]: Added query match criteria for Target [%s], URL [%s], Key [%s] Value [%s]", p.Port, target.Name, target.URL, key, value)
  }
  target.lock.Unlock()
  util.AddLogMessage(msg, r)
  w.WriteHeader(http.StatusOK)
  fmt.Fprintf(w, msg)
}

func (p *Proxy) addTargetHeaderOrQuery(w http.ResponseWriter, r *http.Request, isHeader bool) {
  name := util.GetStringParamValue(r, "target")
  key := util.GetStringParamValue(r, "key")
  value := util.GetStringParamValue(r, "value")
  p.lock.RLock()
  target := p.Targets[name]
  p.lock.RUnlock()
  if target == nil {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintf(w, "Invalid target: %s\n", name)
    return
  }
  msg := ""
  target.lock.Lock()
  if isHeader {
    target.AddHeaders = append(target.AddHeaders, []string{key, value})
    msg = fmt.Sprintf("Port [%s]: Recorded header to add for Target [%s], URL [%s], Key [%s] Value [%s]", p.Port, target.Name, target.URL, key, value)
  } else {
    target.AddQuery = append(target.AddQuery, []string{key, value})
    msg = fmt.Sprintf("Port [%s]: Recorded query to add for Target [%s], URL [%s], Key [%s] Value [%s]", p.Port, target.Name, target.URL, key, value)
  }
  target.lock.Unlock()
  util.AddLogMessage(msg, r)
  w.WriteHeader(http.StatusOK)
  fmt.Fprintf(w, msg)
}

func (p *Proxy) removeTargetHeaderOrQuery(w http.ResponseWriter, r *http.Request, isHeader bool) {
  name := util.GetStringParamValue(r, "target")
  key := util.GetStringParamValue(r, "key")
  p.lock.RLock()
  target := p.Targets[name]
  p.lock.RUnlock()
  if target == nil {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintf(w, "Invalid target: %s\n", name)
    return
  }
  msg := ""
  target.lock.Lock()
  if isHeader {
    target.RemoveHeaders = append(target.RemoveHeaders, key)
    msg = fmt.Sprintf("Port [%s]: Recorded header to remove for Target [%s], URL [%s], Key [%s]", p.Port, target.Name, target.URL, key)
  } else {
    target.RemoveQuery = append(target.RemoveQuery, key)
    msg = fmt.Sprintf("Port [%s]: Recorded query to remove for Target [%s], URL [%s], Key [%s]", p.Port, target.Name, target.URL, key)
  }
  target.lock.Unlock()
  util.AddLogMessage(msg, r)
  w.WriteHeader(http.StatusOK)
  fmt.Fprintf(w, msg)
}

func (p *Proxy) addProxyTarget(w http.ResponseWriter, r *http.Request) {
  target := newProxyTarget()
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
  if _, err := p.toInvocationSpec(target, "/", nil); err == nil {
    p.deleteProxyTarget(target.Name)
    p.lock.Lock()
    defer p.lock.Unlock()
    if target.StripURI != "" {
      target.stripURIRegExp = regexp.MustCompile("^(.*)(" + target.StripURI + ")(/.+).*$")
    }
    p.Targets[target.Name] = target
    p.addHeadersAndQueriesMatch(target)
    if err := p.addRoutes(target); err == nil {
      util.AddLogMessage(fmt.Sprintf("Added proxy target: %+v", target), r)
      w.WriteHeader(http.StatusOK)
      fmt.Fprintf(w, "Port [%s]: Added proxy target: %s\n", p.Port, util.ToJSONText(target))
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

func (p *Proxy) addHeadersAndQueriesMatch(target *ProxyTarget) {
  headerMatches := [][]string{}
  queryMatches := [][]string{}
  if target.MatchAny != nil {
    headerMatches = append(headerMatches, target.MatchAny.Headers...)
    queryMatches = append(queryMatches, target.MatchAny.Query...)
  }
  if target.MatchAll != nil {
    headerMatches = append(headerMatches, target.MatchAll.Headers...)
    queryMatches = append(queryMatches, target.MatchAll.Query...)
  }
  extractKV := func(kv []string) (k string, v string) {
    if len(kv) > 0 {
      k = util.LowerAndTrim(kv[0])
    }
    if len(kv) > 1 {
      v = util.LowerAndTrim(kv[1])
    }
    return
  }
  for _, m := range headerMatches {
    key, value := extractKV(m)
    p.addHeaderMatch(target, key, value)
  }
  for _, m := range queryMatches {
    key, value := extractKV(m)
    p.addQueryMatch(target, key, value)
  }
}

func (p *Proxy) addHeaderMatch(target *ProxyTarget, header, value string) {
  if value != "" {
    if captureKey, found := util.GetFillerUnmarked(value); found {
      if target.captureHeaders == nil {
        target.captureHeaders = map[string]string{}
      }
      target.captureHeaders[header] = captureKey
      value = ""
    }
  }
  target.headerMatch[header] = append(target.headerMatch[header], value)
}

func (p *Proxy) addQueryMatch(target *ProxyTarget, key, value string) {
  if value != "" {
    if filler, found := util.GetFillerUnmarked(value); found {
      if target.captureQuery == nil {
        target.captureQuery = map[string]string{}
      }
      target.captureQuery[key] = filler
      value = ""
    }
  }
  target.queryMatch[key] = append(target.queryMatch[key], value)
}

func (p *Proxy) addRoutes(target *ProxyTarget) error {
  for uri := range target.Routes {
    if err := p.addURIMatch(target, uri); err != nil {
      return err
    }
  }
  return nil
}

func (p *Proxy) addURIMatch(target *ProxyTarget, uri string) error {
  uri = strings.ToLower(uri)
  glob := false
  if strings.EqualFold(uri, "/") {
    target.matchAllURIs = true
    glob = true
  }
  matchURI := uri
  if strings.HasSuffix(uri, "*") {
    matchURI = strings.ReplaceAll(uri, "*", "")
    glob = true
  }
  if router, re, err := util.RegisterURIRouteAndGetRegex(matchURI, glob, p.router, handleURI); err == nil {
    target.uriRegexps[uri] = re
    target.uriRouters[uri] = router
  } else {
    log.Printf("Proxy: Failed to add URI match %s with error: %s\n", uri, err.Error())
    return err
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
}

func (p *Proxy) removeProxyTarget(w http.ResponseWriter, r *http.Request) {
  if t := p.getRequestedProxyTarget(r); t != nil {
    p.deleteProxyTarget(t.Name)
    util.AddLogMessage(fmt.Sprintf("Removed proxy target: %+v", t), r)
    w.WriteHeader(http.StatusOK)
    fmt.Fprintf(w, "Port [%s]: Removed proxy target: %s\n", p.Port, util.ToJSONText(t))
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
    msg := fmt.Sprintf("Port [%s]: Enabled proxy target: %s", p.Port, t.Name)
    util.AddLogMessage(msg, r)
    w.WriteHeader(http.StatusOK)
    fmt.Fprintf(w, util.ToJSONText(map[string]string{"result": msg}))
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
    msg := fmt.Sprintf("Port [%s]: Disabled proxy target: %s", p.Port, t.Name)
    util.AddLogMessage(msg, r)
    events.SendRequestEvent("Proxy Target Disabled", msg, r)
    w.WriteHeader(http.StatusOK)
    fmt.Fprintf(w, util.ToJSONText(map[string]string{"result": msg}))
  } else {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintln(w, "Proxy target not found")
  }
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

func (p *Proxy) prepareTargetURL(target *ProxyTarget, uri string, r *http.Request) string {
  url := target.URL
  path := r.URL.Path
  targetURI := path
  if len(target.Routes) > 0 && target.Routes[uri] != "" {
    targetURI = target.Routes[uri]
  }
  if targetURI != "" {
    forwardRoute := target.uriRouters[uri].NewRoute().BuildOnly().Path(targetURI)
    vars := mux.Vars(r)
    targetVars := []string{}
    if rep, err := reverse.NewGorillaPath(targetURI, false); err == nil {
      for _, k := range rep.Groups() {
        targetVars = append(targetVars, k, vars[k])
      }
      if netURL, err := forwardRoute.URLPath(targetVars...); err == nil {
        path = netURL.Path
      } else {
        log.Printf("Proxy: Failed to set vars on target URI %s with error: %s. Using target URI as is.", targetURI, err.Error())
        path = targetURI
      }
    } else {
      log.Printf("Proxy: Failed to parse path vars from target URI %s with error: %s. Using target URI as is.", targetURI, err.Error())
      path = targetURI
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

func (p *Proxy) toInvocationSpec(target *ProxyTarget, uri string, r *http.Request) (*invocation.InvocationSpec, error) {
  is := &invocation.InvocationSpec{}
  is.Name = target.Name
  is.Method = "GET"
  is.URL = target.URL
  is.Replicas = target.Replicas
  is.SendID = target.SendID
  if r != nil {
    is.URL = p.prepareTargetURL(target, uri, r)
    is.Headers = p.prepareTargetHeaders(target, r)
    is.Method = r.Method
    is.BodyReader = r.Body
  }
  is.CollectResponse = true
  is.TrackPayload = true
  return is, invocation.ValidateSpec(is)
}

func (p *Proxy) invokeTargets(targetsMatches map[string]*TargetMatchInfo, w http.ResponseWriter, r *http.Request) {
  p.lock.Lock()
  defer p.lock.Unlock()
  if len(targetsMatches) > 0 {
    responses := []*invocation.InvocationResultResponse{}
    for _, m := range targetsMatches {
      events.SendRequestEventJSON("Proxy Target Invoked", m.target.Name, m.target, r)
      metrics.UpdateProxiedRequestCount(m.target.Name)
      is, _ := p.toInvocationSpec(m.target, m.URI, r)
      if tracker, err := invocation.RegisterInvocation(is); err == nil {
        m.target.lock.Lock()
        m.target.callCount++
        tracker.CustomID = m.target.callCount
        m.target.lock.Unlock()
        invocationResponses := invocation.StartInvocation(tracker, true)
        if !util.IsBinaryContentHeader(invocationResponses[0].Response.Headers) {
          invocationResponses[0].Response.PayloadText = string(invocationResponses[0].Response.Payload)
        }
        responses = append(responses, invocationResponses[0].Response)
        util.AddHeaderWithSuffix(constants.HeaderUpstreamStatus, "|"+m.target.Name,
          invocationResponses[0].Response.Status, w.Header())
        util.AddHeaderWithSuffix(constants.HeaderUpstreamTook, "|"+m.target.Name,
          invocationResponses[0].TookNanos.String(), w.Header())
      } else {
        log.Println(err.Error())
      }
    }
    for _, response := range responses {
      util.CopyHeaders("", r, w, response.Headers, false, false, false)
      if response.StatusCode == 0 {
        response.StatusCode = 503
      }
      status.IncrementStatusCount(response.StatusCode, r)
      trigger.RunTriggers(r, w, response.StatusCode)
    }
    if len(responses) == 1 {
      if util.IsBinaryContentHeader(responses[0].Headers) {
        fmt.Fprintln(w, responses[0].Payload)
      } else {
        if hv := responses[0].Headers[constants.HeaderContentTypeLower]; len(hv) > 0 {
          w.Header().Add(constants.HeaderContentType, hv[0])
        }
        fmt.Fprintln(w, responses[0].PayloadText)
      }
      w.WriteHeader(responses[0].StatusCode)
    } else {
      w.WriteHeader(http.StatusOK)
      fmt.Fprintln(w, util.ToJSONText(responses))
    }
  }
}

func (p *Proxy) getMatchingTargetsForRequest(r *http.Request) map[string]*TargetMatchInfo {
  rs := util.GetRequestStore(r)
  if rs.ProxyTargets != nil {
    return rs.ProxyTargets.(map[string]*TargetMatchInfo)
  }
  targets := p.checkMatchingTargetsForRequest(r)
  rs.ProxyTargets = targets
  return targets
}

func (p *Proxy) checkMatchingTargetsForRequest(r *http.Request) map[string]*TargetMatchInfo {
  p.lock.RLock()
  defer p.lock.RUnlock()
  matchInfo := map[string]*TargetMatchInfo{}
  for name, target := range p.Targets {
    if target.Enabled {
      if target.matchAllURIs {
        matchInfo[name] = &TargetMatchInfo{target: target, URI: "/"}
      } else {
        for uri, re := range target.uriRegexps {
          if re.MatchString(r.RequestURI) {
            matchInfo[name] = &TargetMatchInfo{target: target, URI: uri}
            break
          }
        }
      }
    }
  }

  var headerValuesMap map[string]map[string]int
  var queryParamsMap map[string]map[string]int
  for _, m := range matchInfo {
    if len(m.target.headerMatch) > 0 {
      if headerValuesMap == nil {
        headerValuesMap = util.GetHeaderValues(r)
      }
      for header, values := range m.target.headerMatch {
        if valueMap, present := headerValuesMap[header]; present {
          for _, value := range values {
            if value == "" {
              m.Headers = append(m.Headers, []string{header, ""})
            } else if _, found := valueMap[value]; found {
              m.Headers = append(m.Headers, []string{header, value})
            }
          }
        }
      }
    }
    if len(m.target.queryMatch) > 0 {
      if queryParamsMap == nil {
        queryParamsMap = util.GetQueryParams(r)
      }
      for key, values := range m.target.queryMatch {
        if valueMap, present := queryParamsMap[key]; present {
          for _, value := range values {
            if value == "" {
              m.Query = append(m.Query, []string{key, ""})
            } else if _, found := valueMap[value]; found {
              m.Query = append(m.Query, []string{key, value})
            }
          }
        }
      }
    }
  }
  targetsToBeRemoved := []string{}
  for _, m := range matchInfo {
    if m.target.MatchAll != nil {
      if len(m.target.MatchAll.Headers) != len(m.Headers) ||
        len(m.target.MatchAll.Query) != len(m.Query) {
        targetsToBeRemoved = append(targetsToBeRemoved, m.target.Name)
      }
    } else if m.target.MatchAny != nil {
      if len(m.target.MatchAny.Headers)+len(m.target.MatchAny.Query) > 0 &&
        len(m.Headers)+len(m.Query) == 0 {
        targetsToBeRemoved = append(targetsToBeRemoved, m.target.Name)
      }
    }
  }
  for _, t := range targetsToBeRemoved {
    delete(matchInfo, t)
  }
  for _, m := range matchInfo {
    p.incrementTargetMatchCounts(m.target)
    if m.URI != "" {
      p.incrementMatchCounts(m.target, m.URI, "", "", "", "")
    }
    for _, hv := range m.Headers {
      p.incrementMatchCounts(m.target, "", hv[0], hv[1], "", "")
    }
    for _, qv := range m.Query {
      p.incrementMatchCounts(m.target, "", "", "", qv[0], qv[1])
    }
  }
  return matchInfo
}
