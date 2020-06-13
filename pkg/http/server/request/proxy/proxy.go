package proxy

import (
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"goto/pkg/http/invocation"
	"goto/pkg/http/server/response/status"
	"goto/pkg/http/server/response/trigger"
	"goto/pkg/util"

	"github.com/gorilla/mux"
	"github.com/gorilla/reverse"
)

type ProxyTargetMatch struct {
  Headers [][]string
  Uris    []string
  Query   [][]string
}

type ProxyTarget struct {
  Name           string           `json:"name"`
  URL            string           `json:"url"`
  SendID         bool             `json:"sendID"`
  ReplaceURI     string           `json:"replaceURI"`
  AddHeaders     [][]string       `json:"addHeaders"`
  RemoveHeaders  []string         `json:"removeHeaders"`
  AddQuery       [][]string       `json:"addQuery"`
  RemoveQuery    []string         `json:"removeQuery"`
  Match          ProxyTargetMatch `json:"match"`
  Replicas       int              `json:"replicas"`
  Enabled        bool             `json:"enabled"`
  uriRegExp      *regexp.Regexp
  captureHeaders map[string]string
  captureQuery   map[string]string
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
  lock             sync.RWMutex
}

var (
  Handler         util.ServerHandler = util.ServerHandler{Name: "proxy", SetRoutes: SetRoutes, Middleware: Middleware}
  internalHandler util.ServerHandler = util.ServerHandler{Name: "uri", Middleware: middleware}
  forwardRouter   *mux.Router
  proxyByPort     map[string]*Proxy = map[string]*Proxy{}
  proxyLock       sync.RWMutex
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  forwardRouter = root.NewRoute().Subrouter()
  proxyRouter := r.PathPrefix("/proxy").Subrouter()
  targetsRouter := proxyRouter.PathPrefix("/targets").Subrouter()
  util.AddRoute(targetsRouter, "/add", addProxyTarget, "POST")
  util.AddRoute(targetsRouter, "/{target}/remove", removeProxyTarget, "PUT", "POST")
  util.AddRoute(targetsRouter, "/{target}/enable", enableProxyTarget, "PUT", "POST")
  util.AddRoute(targetsRouter, "/{target}/disable", disableProxyTarget, "PUT", "POST")
  util.AddRoute(targetsRouter, "/{targets}/invoke", invokeProxyTargets, "POST")
  util.AddRoute(targetsRouter, "/invoke/{targets}", invokeProxyTargets, "POST")
  util.AddRoute(targetsRouter, "/clear", clearProxyTargets, "POST")
  util.AddRoute(targetsRouter, "", getProxyTargets)
  util.AddRoute(proxyRouter, "/counts", getProxyMatchCounts, "GET")
  util.AddRoute(proxyRouter, "/counts/clear", clearProxyMatchCounts, "POST")
}

func (p *Proxy) init() {
  p.lock.Lock()
  defer p.lock.Unlock()
  if p.Targets == nil {
    p.Targets = map[string]*ProxyTarget{}
    p.TargetsByHeaders = map[string]map[string]map[string]*ProxyTarget{}
    p.TargetsByUris = map[string]map[string]*ProxyTarget{}
    p.TargetsByQuery = map[string]map[string]map[string]*ProxyTarget{}
    p.initResults()
  }
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
  var err error
  if err = util.ReadJsonPayload(r, target); err == nil {
    _, err = toInvocationSpec(target, nil)
  }
  if err == nil {
    p.deleteProxyTarget(target.Name)
    p.lock.Lock()
    defer p.lock.Unlock()
    p.Targets[target.Name] = target
    p.addHeaderMatch(target)
    p.addQueryMatch(target)
    if err := p.addURIMatch(target); err == nil {
      util.AddLogMessage(fmt.Sprintf("Added proxy target: %+v", target), r)
      w.WriteHeader(http.StatusAccepted)
      fmt.Fprintf(w, "Added proxy target: %s\n", util.ToJSON(target))
    } else {
      w.WriteHeader(http.StatusBadRequest)
      fmt.Fprintf(w, "Failed to add URI Match with error: %s\n", err.Error())
    }
  } else {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintf(w, "Invalid target: %s\n", err.Error())
  }
}

func (p *Proxy) addHeaderMatch(target *ProxyTarget) {
  for _, h := range target.Match.Headers {
    header := strings.ToLower(strings.Trim(h[0], " "))
    headerValue := ""
    if len(h) > 1 {
      headerValue = strings.ToLower(strings.Trim(h[1], " "))
      if strings.HasPrefix(headerValue, "{") && strings.HasSuffix(headerValue, "}") {
        headerValue = strings.TrimLeft(headerValue, "{")
        headerValue = strings.TrimRight(headerValue, "}")
        if target.captureHeaders == nil {
          target.captureHeaders = map[string]string{}
        }
        target.captureHeaders[header] = headerValue
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
  for _, q := range target.Match.Query {
    key := strings.ToLower(strings.Trim(q[0], " "))
    value := ""
    if len(q) > 1 {
      value = strings.ToLower(strings.Trim(q[1], " "))
      if strings.HasPrefix(value, "{") && strings.HasSuffix(value, "}") {
        value = strings.TrimLeft(value, "{")
        value = strings.TrimRight(value, "}")
        if target.captureQuery == nil {
          target.captureQuery = map[string]string{}
        }
        target.captureQuery[key] = value
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
  for _, uri := range target.Match.Uris {
    uri = strings.ToLower(uri)
    uriTargets, present := p.TargetsByUris[uri]
    if !present {
      uriTargets = map[string]*ProxyTarget{}
      p.TargetsByUris[uri] = uriTargets
    }
    route := forwardRouter.Path(uri).MatcherFunc(matchURI)
    if re, err := route.GetPathRegexp(); err == nil {
      target.uriRegExp = regexp.MustCompile(re)
    } else {
      log.Printf("Failed to add URI match %s with error: %s\n", uri, err.Error())
      return err
    }
    uriTargets[target.Name] = target
  }
  return nil
}

func matchURI(r *http.Request, rm *mux.RouteMatch) bool {
  if status.IsForcedStatus(r) {
    return false
  }
  p := getPortProxy(r)
  for _, target := range p.Targets {
    if target.Enabled {
      if target.uriRegExp != nil && target.uriRegExp.MatchString(r.RequestURI) {
        return true
      }
      for _, uri := range target.Match.Uris {
        if uri == "/" {
          return true
        }
      }
    }
  }
  return false
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
    w.WriteHeader(http.StatusAccepted)
    fmt.Fprintf(w, "Removed proxy target: %s\n", util.ToJSON(t))
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
    util.AddLogMessage(fmt.Sprintf("Enabled proxy target: %+v", t), r)
    w.WriteHeader(http.StatusAccepted)
    fmt.Fprintf(w, "Enabled proxy target: %s\n", util.ToJSON(t))
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
    util.AddLogMessage(fmt.Sprintf("Disbled proxy target: %+v", t), r)
    w.WriteHeader(http.StatusAccepted)
    fmt.Fprintf(w, "Disabled proxy target: %s\n", util.ToJSON(t))
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

func prepareTargetHeaders(target *ProxyTarget, r *http.Request) [][]string {
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
    if strings.HasPrefix(headerValue, "{") && strings.HasSuffix(headerValue, "}") {
      captureKey := strings.TrimLeft(headerValue, "{")
      captureKey = strings.TrimRight(captureKey, "}")
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

func prepareTargetURL(target *ProxyTarget, r *http.Request) string {
  url := target.URL
  if len(target.ReplaceURI) > 0 {
    forwardRoute := forwardRouter.NewRoute().BuildOnly().Path(target.ReplaceURI)
    vars := mux.Vars(r)
    targetVars := []string{}
    if rep, err := reverse.NewGorillaPath(target.ReplaceURI, false); err == nil {
      for _, k := range rep.Groups() {
        targetVars = append(targetVars, k, vars[k])
      }
      if netURL, err := forwardRoute.URLPath(targetVars...); err == nil {
        url += netURL.Path
      } else {
        log.Printf("Failed to set vars on ReplaceURI %s with error: %s. Using ReplaceURI as is.", target.ReplaceURI, err.Error())
        url += target.ReplaceURI
      }
    } else {
      log.Printf("Failed to parse path vars from ReplaceURI %s with error: %s. Using ReplaceURI as is.", target.ReplaceURI, err.Error())
      url += target.ReplaceURI
    }
  } else {
    url += r.URL.Path
  }
  url = prepareTargetQuery(url, target, r)
  return url
}

func prepareTargetQuery(url string, target *ProxyTarget, r *http.Request) string {
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
    if strings.HasPrefix(addValue, "{") && strings.HasSuffix(addValue, "}") {
      captureKey := strings.TrimLeft(addValue, "{")
      captureKey = strings.TrimRight(captureKey, "}")
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

func toInvocationSpec(target *ProxyTarget, r *http.Request) (*invocation.InvocationSpec, error) {
  is := &invocation.InvocationSpec{}
  is.Name = target.Name
  is.Method = "GET"
  is.URL = target.URL
  is.Replicas = target.Replicas
  is.SendID = target.SendID
  if r != nil {
    is.URL = prepareTargetURL(target, r)
    is.Headers = prepareTargetHeaders(target, r)
    is.Method = r.Method
    is.BodyReader = r.Body
  }
  return is, invocation.ValidateSpec(is)
}

func (p *Proxy) invokeTargets(targets map[string]*ProxyTarget, w http.ResponseWriter, r *http.Request) {
  p.lock.Lock()
  defer p.lock.Unlock()
  if len(targets) > 0 {
    invocationSpecs := []*invocation.InvocationSpec{}
    for _, target := range targets {
      is, _ := toInvocationSpec(target, r)
      invocationSpecs = append(invocationSpecs, is)
    }
    ic := invocation.RegisterInvocation(util.GetListenerPortNum(r))
    responses := invocation.InvokeTargets(invocationSpecs, ic, true)
    invocation.DeregisterInvocation(ic)
    for _, response := range responses {
      util.CopyHeaders(w, response.Headers, "")
      if response.StatusCode == 0 {
        response.StatusCode = 503
      }
      status.IncrementStatusCount(response.StatusCode, r)
      trigger.RunTriggers(r, w, response.StatusCode)
    }
    if len(responses) == 1 {
      w.WriteHeader(responses[0].StatusCode)
      fmt.Fprintln(w, responses[0].Body)
    } else {
      w.WriteHeader(http.StatusAlreadyReported)
      fmt.Fprintln(w, util.ToJSON(responses))
    }
  }
}

func getPortProxy(r *http.Request) *Proxy {
  proxyLock.RLock()
  listenerPort := util.GetListenerPort(r)
  proxy := proxyByPort[listenerPort]
  proxyLock.RUnlock()
  if proxy == nil {
    proxyLock.Lock()
    defer proxyLock.Unlock()
    proxy = &Proxy{}
    proxy.init()
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
  listenerPort := util.GetListenerPort(r)
  proxyLock.Lock()
  defer proxyLock.Unlock()
  proxyByPort[listenerPort] = &Proxy{}
  proxyByPort[listenerPort].init()
  w.WriteHeader(http.StatusAccepted)
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
  w.WriteHeader(http.StatusAccepted)
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
      } else {
        for _, uri := range target.Match.Uris {
          if uri == "/" {
            targets[target.Name] = target
            if matchInfo[target.Name] == nil {
              matchInfo[target.Name] = &targetMatchInfo{headers: map[string]string{}, query: map[string]string{}, uris: []string{}}
            }
            matchInfo[target.Name].uris = append(matchInfo[target.Name].uris, "/")
          }
        }
      }
    }
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

func WillProxy(r *http.Request) (bool, map[string]*ProxyTarget) {
  if util.IsAdminRequest(r) || status.IsForcedStatus(r) {
    return false, nil
  }

  targets := getPortProxy(r).getMatchingTargetsForRequest(r)
  if len(targets) > 0 {
    return true, targets
  }
  return false, nil
}

func middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    willProxy, targets := WillProxy(r)
    if !willProxy {
      next.ServeHTTP(w, r)
      return
    }
    util.AddLogMessage(fmt.Sprintf("Proxying to matching targets %s", util.ToJSON(targets)), r)
    getPortProxy(r).invokeTargets(targets, w, r)
  })
}

func Middleware(next http.Handler) http.Handler {
  return util.AddMiddlewares(next, internalHandler)
}
