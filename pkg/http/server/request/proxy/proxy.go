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
  Name           string
  URL            string
  SendID         bool
  ReplaceURI     string
  AddHeaders     [][]string
  RemoveHeaders  []string
  AddQuery       [][]string
  RemoveQuery    []string
  Match          ProxyTargetMatch
  Replicas       int
  Enabled        bool
  uriRegExp      *regexp.Regexp
  captureHeaders map[string]string
  captureQuery   map[string]string
  invocationSpec *invocation.InvocationSpec
}

type Proxy struct {
  Targets          map[string]*ProxyTarget
  TargetsByHeaders map[string]map[string]map[string]*ProxyTarget
  TargetsByUris    map[string]map[string]*ProxyTarget
  TargetsByQuery   map[string]map[string]map[string]*ProxyTarget
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
}

func (p *Proxy) init() {
  p.lock.Lock()
  defer p.lock.Unlock()
  if p.Targets == nil {
    p.Targets = map[string]*ProxyTarget{}
    p.TargetsByHeaders = map[string]map[string]map[string]*ProxyTarget{}
    p.TargetsByUris = map[string]map[string]*ProxyTarget{}
    p.TargetsByQuery = map[string]map[string]map[string]*ProxyTarget{}
  }
}

func toInvocationSpec(target *ProxyTarget) (*invocation.InvocationSpec, error) {
  is := &invocation.InvocationSpec{}
  is.Name = target.Name
  is.Method = "GET"
  is.URL = target.URL
  is.Replicas = target.Replicas
  is.SendID = target.SendID
  return is, invocation.ValidateSpec(is)
}

func (p *Proxy) addProxyTarget(w http.ResponseWriter, r *http.Request) {
  p.lock.Lock()
  defer p.lock.Unlock()
  var target ProxyTarget
  var err error
  if err = util.ReadJsonPayload(r, &target); err == nil {
    target.invocationSpec, err = toInvocationSpec(&target)
  }
  if err == nil {
    p.Targets[target.Name] = &target
    p.addHeaderMatch(&target)
    p.addQueryMatch(&target)
    if err := p.addURIMatch(&target); err == nil {
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
    targetsByValue, present := targetsByQuery[key]
    if !present {
      targetsByValue = map[string]*ProxyTarget{}
      targetsByQuery[key] = targetsByValue
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
    if target.Enabled && target.uriRegExp != nil && target.uriRegExp.MatchString(r.RequestURI) {
      return true
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

func (p *Proxy) removeProxyTarget(w http.ResponseWriter, r *http.Request) {
  if t := p.getRequestedProxyTarget(r); t != nil {
    p.lock.Lock()
    defer p.lock.Unlock()
    delete(p.Targets, t.Name)
    for h, valueMap := range p.TargetsByHeaders {
      for hv, valueTargets := range valueMap {
        for name := range valueTargets {
          if name == t.Name {
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
          if name == t.Name {
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
        if name == t.Name {
          delete(uriTargets, name)
        }
      }
      if len(uriTargets) == 0 {
        delete(p.TargetsByUris, uri)
      }
    }
    util.AddLogMessage(fmt.Sprintf("Removed proxy target: %+v", t), r)
    w.WriteHeader(http.StatusAccepted)
    fmt.Fprintf(w, "Removed proxy target: %s\n", util.ToJSON(t))
  } else {
    w.WriteHeader(http.StatusNotFound)
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
    w.WriteHeader(http.StatusNotFound)
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
    w.WriteHeader(http.StatusNotFound)
    fmt.Fprintln(w, "Proxy target not found")
  }
}

func (p *Proxy) getRequestedTargets(r *http.Request) map[string]*ProxyTarget {
  p.lock.RLock()
  defer p.lock.RUnlock()
  var targets map[string]*ProxyTarget = map[string]*ProxyTarget{}
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

func updateInvocationSpec(target *ProxyTarget, r *http.Request) {
  target.invocationSpec.URL = prepareTargetURL(target, r)
  target.invocationSpec.Headers = prepareTargetHeaders(target, r)
  target.invocationSpec.Method = r.Method
  target.invocationSpec.BodyReader = r.Body
}

func (p *Proxy) invokeTargets(targets map[string]*ProxyTarget, w http.ResponseWriter, r *http.Request) {
  p.lock.Lock()
  defer p.lock.Unlock()
  if len(targets) > 0 {
    invocationSpecs := []*invocation.InvocationSpec{}
    for _, target := range targets {
      updateInvocationSpec(target, r)
      invocationSpecs = append(invocationSpecs, target.invocationSpec)
    }
    i := invocation.InvocationChannels{}
    i.ID = util.GetListenerPortNum(r)
    responses := invocation.InvokeTargets(invocationSpecs, &i, true)
    for _, response := range responses {
      util.CopyHeaders(w, response.Headers, "")
      if response.StatusCode == 0 {
        response.StatusCode = 503
      }
      status.IncrementStatusCount(response.StatusCode, r)
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
  proxyLock.Lock()
  listenerPort := util.GetListenerPort(r)
  proxy := proxyByPort[listenerPort]
  proxyLock.Unlock()
  if proxy == nil {
    proxyLock.Lock()
    defer proxyLock.Unlock()
    listenerPort := util.GetListenerPort(r)
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
  getPortProxy(r).init()
  w.WriteHeader(http.StatusAccepted)
  util.AddLogMessage("Proxy targets cleared", r)
  fmt.Fprintln(w, "Proxy targets cleared")
}

func getProxyTargets(w http.ResponseWriter, r *http.Request) {
  pt := getPortProxy(r)
  util.AddLogMessage(fmt.Sprintf("Get proxy target: %+v", pt), r)
  util.WriteJsonPayload(w, pt)
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

func (pt *Proxy) getMatchingTargetsForRequest(r *http.Request) map[string]*ProxyTarget {
  pt.lock.RLock()
  defer pt.lock.RUnlock()
  targets := map[string]*ProxyTarget{}
  headerValuesMap := util.GetHeaderValues(r)
  for header, valueMap := range pt.TargetsByHeaders {
    if headerValues, present := headerValuesMap[header]; present {
      if valueTargets, found := valueMap[""]; found {
        for name, target := range valueTargets {
          if target.Enabled {
            targets[name] = target
          }
        }
      }
      for headerValue := range headerValues {
        if len(headerValue) > 0 {
          if valueTargets, found := valueMap[headerValue]; found {
            for name, target := range valueTargets {
              if target.Enabled {
                targets[name] = target
              }
            }
          }
        }
      }
    }
  }
  queryParamsMap := util.GetQueryParams(r)
  for key, valueMap := range pt.TargetsByQuery {
    if queryValues, present := queryParamsMap[key]; present {
      if valueTargets, found := valueMap[""]; found {
        for name, target := range valueTargets {
          if target.Enabled {
            targets[name] = target
          }
        }
      }
      for queryValue := range queryValues {
        if len(queryValue) > 0 {
          if valueTargets, found := valueMap[queryValue]; found {
            for name, target := range valueTargets {
              if target.Enabled {
                targets[name] = target
              }
            }
          }
        }
      }
    }
  }
  for _, target := range pt.Targets {
    if target.Enabled && target.uriRegExp != nil && target.uriRegExp.MatchString(r.RequestURI) {
      targets[target.Name] = target
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
