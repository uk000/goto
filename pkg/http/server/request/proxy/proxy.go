package proxy

import (
	"fmt"
	"net/http"
	"strings"
	"sync"

	"goto/pkg/http/invocation"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

type ProxyTargetMatch struct {
  Headers [][]string
  Uris    []string
}

type ProxyTarget struct {
  Name           string
  Url            string
  OverrideUri    string
  AddHeaders     [][]string
  Match          ProxyTargetMatch
  Replicas       int
  Enable         bool
  invocationSpec *invocation.InvocationSpec
}

type ProxyTargets struct {
  Targets          map[string]*ProxyTarget
  TargetsByHeaders map[string]map[string]map[string]*ProxyTarget
  TargetsByUris    map[string]map[string]*ProxyTarget
  lock             sync.RWMutex
}

var (
  Handler            util.ServerHandler       = util.ServerHandler{"proxy", SetRoutes, Middleware}
  proxyTargetsByPort map[string]*ProxyTargets = map[string]*ProxyTargets{}
  proxyTargetsLock   sync.RWMutex
)

func SetRoutes(r *mux.Router, parent *mux.Router) {
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

func (pt *ProxyTargets) init() {
  pt.lock.Lock()
  defer pt.lock.Unlock()
  if pt.Targets == nil {
    pt.Targets = map[string]*ProxyTarget{}
    pt.TargetsByHeaders = map[string]map[string]map[string]*ProxyTarget{}
    pt.TargetsByUris = map[string]map[string]*ProxyTarget{}
  }
}

func (pt *ProxyTargets) addProxyTarget(w http.ResponseWriter, r *http.Request) {
  pt.lock.Lock()
  defer pt.lock.Unlock()
  var target ProxyTarget
  var err error
  if err = util.ReadJsonPayload(r, &target); err == nil {
    target.invocationSpec, err = toInvocationSpec(&target)
  }
  if err == nil {
    pt.Targets[target.Name] = &target
    for _, h := range target.Match.Headers {
      header := strings.ToLower(strings.Trim(h[0], " "))
      headerValue := ""
      if len(h) > 1 {
        headerValue = strings.ToLower(strings.Trim(h[1], " "))
      }
      targetsByHeaders, present := pt.TargetsByHeaders[header]
      if !present {
        targetsByHeaders = map[string]map[string]*ProxyTarget{}
        pt.TargetsByHeaders[header] = targetsByHeaders
      }
      targetsByValue, present := targetsByHeaders[headerValue]
      if !present {
        targetsByValue = map[string]*ProxyTarget{}
        targetsByHeaders[headerValue] = targetsByValue
      }
      targetsByValue[target.Name] = &target
    }
    for _, uri := range target.Match.Uris {
      uri = strings.ToLower(uri)
      targetsByUri, present := pt.TargetsByUris[uri]
      if !present {
        targetsByUri = map[string]*ProxyTarget{}
        pt.TargetsByUris[uri] = targetsByUri
      }
      targetsByUri[target.Name] = &target
    }
    util.AddLogMessage(fmt.Sprintf("Added proxy target: %+v", target), r)
    w.WriteHeader(http.StatusAccepted)
    fmt.Fprintf(w, "Added proxy target: %s\n", util.ToJSON(target))
  } else {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintf(w, "Invalid target: %s\n", err.Error())
  }
}

func toInvocationSpec(target *ProxyTarget) (*invocation.InvocationSpec, error) {
  is := &invocation.InvocationSpec{}
  is.Name = target.Name
  is.Method = "GET"
  is.Url = target.Url
  is.Replicas = target.Replicas
  return is, invocation.ValidateSpec(is)
}

func (pt *ProxyTargets) getRequestedProxyTarget(r *http.Request) *ProxyTarget {
  pt.lock.RLock()
  defer pt.lock.RUnlock()
  if tname, present := util.GetStringParam(r, "target"); present {
    return pt.Targets[tname]
  }
  return nil
}

func (pt *ProxyTargets) removeProxyTarget(w http.ResponseWriter, r *http.Request) {
  if t := pt.getRequestedProxyTarget(r); t != nil {
    pt.lock.Lock()
    defer pt.lock.Unlock()
    delete(pt.Targets, t.Name)
    for h, valueMap := range pt.TargetsByHeaders {
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
        delete(pt.TargetsByHeaders, h)
      }
    }
    for uri, uriTargets := range pt.TargetsByUris {
      for name := range uriTargets {
        if name == t.Name {
          delete(uriTargets, name)
        }
      }
      if len(uriTargets) == 0 {
        delete(pt.TargetsByUris, uri)
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

func (pt *ProxyTargets) enableProxyTarget(w http.ResponseWriter, r *http.Request) {
  if t := pt.getRequestedProxyTarget(r); t != nil {
    pt.lock.Lock()
    defer pt.lock.Unlock()
    t.Enable = true
    util.AddLogMessage(fmt.Sprintf("Enabled proxy target: %+v", t), r)
    w.WriteHeader(http.StatusAccepted)
    fmt.Fprintf(w, "Enabled proxy target: %s\n", util.ToJSON(t))
  } else {
    w.WriteHeader(http.StatusNotFound)
    fmt.Fprintln(w, "Proxy target not found")
  }
}

func (pt *ProxyTargets) disableProxyTarget(w http.ResponseWriter, r *http.Request) {
  if t := pt.getRequestedProxyTarget(r); t != nil {
    pt.lock.Lock()
    defer pt.lock.Unlock()
    t.Enable = false
    util.AddLogMessage(fmt.Sprintf("Disbled proxy target: %+v", t), r)
    w.WriteHeader(http.StatusAccepted)
    fmt.Fprintf(w, "Disabled proxy target: %s\n", util.ToJSON(t))
  } else {
    w.WriteHeader(http.StatusNotFound)
    fmt.Fprintln(w, "Proxy target not found")
  }
}

func (pt *ProxyTargets) getMatchingTargetsForRequest(r *http.Request) map[string]*ProxyTarget {
  pt.lock.RLock()
  defer pt.lock.RUnlock()
  var targets map[string]*ProxyTarget = map[string]*ProxyTarget{}
  headerValuesMap := util.GetHeaderValues(r)
  for header, valueMap := range pt.TargetsByHeaders {
    if headerValues, present := headerValuesMap[header]; present {
      if valueTargets, found := valueMap[""]; found {
        for name, target := range valueTargets {
          if target.Enable {
            targets[name] = target
          }
        }
      }
      for headerValue := range headerValues {
        if len(headerValue) > 0 {
          if valueTargets, found := valueMap[headerValue]; found {
            for name, target := range valueTargets {
              if target.Enable {
                targets[name] = target
              }
            }
          }
        }
      }
    }
  }
  for uri, uriTargets := range pt.TargetsByUris {
    requestURI := strings.ToLower(strings.Split(r.RequestURI, "?")[0])
    if strings.EqualFold(requestURI, uri) {
      for name, target := range uriTargets {
        if target.Enable {
          targets[name] = target
        }
      }
    }
  }
  return targets
}

func (pt *ProxyTargets) getRequestedTargets(r *http.Request) map[string]*ProxyTarget {
  pt.lock.RLock()
  defer pt.lock.RUnlock()
  var targets map[string]*ProxyTarget = map[string]*ProxyTarget{}
  if tnamesParam, present := util.GetStringParam(r, "targets"); present {
    tnames := strings.Split(tnamesParam, ",")
    for _, tname := range tnames {
      if target, found := pt.Targets[tname]; found {
        targets[target.Name] = target
      }
    }
  } else {
    targets = pt.Targets
  }
  return targets
}

func prepareTargetHeaders(t *ProxyTarget, r *http.Request) [][]string {
  var headers [][]string = [][]string{}
  for k, v := range r.Header {
    headers = append(headers, []string{k, v[0]})
  }
  for _, h := range t.AddHeaders {
    header := strings.Trim(h[0], " ")
    headerValue := ""
    if len(h) > 1 {
      headerValue = strings.Trim(h[1], " ")
    }
    headers = append(headers, []string{header, headerValue})
  }
  return headers
}

func prepareTargetUrl(t *ProxyTarget, r *http.Request) string {
  url := t.Url
  if len(t.OverrideUri) > 0 {
    url += t.OverrideUri
  } else {
    url += r.RequestURI
  }
  return url
}

func updateInvocationSpec(target *ProxyTarget, r *http.Request) {
  target.invocationSpec.Url = prepareTargetUrl(target, r)
  target.invocationSpec.Headers = prepareTargetHeaders(target, r)
  target.invocationSpec.Method = r.Method
  target.invocationSpec.BodyReader = r.Body
}

func (pt *ProxyTargets) invokeTargets(targets map[string]*ProxyTarget, w http.ResponseWriter, r *http.Request) {
  pt.lock.RLock()
  defer pt.lock.RUnlock()
  if len(targets) > 0 {
    invocationSpecs := []*invocation.InvocationSpec{}
    for _, target := range targets {
      updateInvocationSpec(target, r)
      invocationSpecs = append(invocationSpecs, target.invocationSpec)
    }
    responses := invocation.InvokeTargets(invocationSpecs, true)
    if len(responses) == 1 {
      util.CopyHeaders(w, responses[0].Headers, "")
      if responses[0].StatusCode == 0 {
        responses[0].StatusCode = 503
      }
      w.WriteHeader(responses[0].StatusCode)
      fmt.Fprintln(w, responses[0].Body)
    } else {
      w.WriteHeader(http.StatusAlreadyReported)
      fmt.Fprintln(w, util.ToJSON(responses))
    }
  }
}

func addProxyTarget(w http.ResponseWriter, r *http.Request) {
  proxyTargetsLock.Lock()
  defer proxyTargetsLock.Unlock()
  listenerPort := util.GetListenerPort(r)
  pt, present := proxyTargetsByPort[listenerPort]
  if !present {
    pt = &ProxyTargets{}
    pt.init()
    proxyTargetsByPort[listenerPort] = pt
  }
  pt.addProxyTarget(w, r)
}

func getRequestedProxyTarget(r *http.Request) *ProxyTarget {
  proxyTargetsLock.RLock()
  defer proxyTargetsLock.RUnlock()
  if pt := proxyTargetsByPort[util.GetListenerPort(r)]; pt != nil {
    return pt.getRequestedProxyTarget(r)
  }
  return nil
}

func removeProxyTarget(w http.ResponseWriter, r *http.Request) {
  proxyTargetsLock.Lock()
  defer proxyTargetsLock.Unlock()
  if pt := proxyTargetsByPort[util.GetListenerPort(r)]; pt != nil {
    pt.removeProxyTarget(w, r)
  } else {
    w.WriteHeader(http.StatusNotFound)
    fmt.Fprintln(w, "Proxy target not found")
  }
}

func enableProxyTarget(w http.ResponseWriter, r *http.Request) {
  proxyTargetsLock.Lock()
  defer proxyTargetsLock.Unlock()
  if pt := proxyTargetsByPort[util.GetListenerPort(r)]; pt != nil {
    pt.enableProxyTarget(w, r)
  } else {
    w.WriteHeader(http.StatusNotFound)
    fmt.Fprintln(w, "Proxy target not found")
  }
}

func disableProxyTarget(w http.ResponseWriter, r *http.Request) {
  proxyTargetsLock.Lock()
  defer proxyTargetsLock.Unlock()
  if pt := proxyTargetsByPort[util.GetListenerPort(r)]; pt != nil {
    pt.disableProxyTarget(w, r)
  } else {
    w.WriteHeader(http.StatusNotFound)
    fmt.Fprintln(w, "Proxy target not found")
  }
}

func clearProxyTargets(w http.ResponseWriter, r *http.Request) {
  proxyTargetsLock.Lock()
  defer proxyTargetsLock.Unlock()
  listenerPort := util.GetListenerPort(r)
  if _, present := proxyTargetsByPort[listenerPort]; present {
    proxyTargetsByPort[listenerPort] = &ProxyTargets{}
    proxyTargetsByPort[listenerPort].init()
  }
  w.WriteHeader(http.StatusAccepted)
  util.AddLogMessage("Proxy targets cleared", r)
  fmt.Fprintln(w, "Proxy targets cleared")
}

func getProxyTargets(w http.ResponseWriter, r *http.Request) {
  proxyTargetsLock.Lock()
  defer proxyTargetsLock.Unlock()
  if pt := proxyTargetsByPort[util.GetListenerPort(r)]; pt != nil {
    util.AddLogMessage(fmt.Sprintf("Get proxy target: %+v", pt), r)
    util.WriteJsonPayload(w, pt)
  } else {
    w.WriteHeader(http.StatusNotFound)
    fmt.Fprintln(w, "{}")
  }
}

func invokeProxyTargets(w http.ResponseWriter, r *http.Request) {
  proxyTargetsLock.RLock()
  defer proxyTargetsLock.RUnlock()
  if pt := proxyTargetsByPort[util.GetListenerPort(r)]; pt != nil {
    targets := pt.getRequestedTargets(r)
    if len(targets) > 0 {
      pt.invokeTargets(targets, w, r)
    } else {
      w.WriteHeader(http.StatusNotFound)
      util.AddLogMessage("Proxy targets not found", r)
      fmt.Fprintln(w, "Proxy targets not found")
    }
  } else {
    w.WriteHeader(http.StatusNotFound)
    util.AddLogMessage("Proxy targets not found", r)
    fmt.Fprintln(w, "Proxy targets not found")
  }
}

func WillProxy(r *http.Request) (bool, map[string]*ProxyTarget) {
  if util.IsAdminRequest(r) {
    return false, nil
  }
  proxyTargetsLock.RLock()
  pt := proxyTargetsByPort[util.GetListenerPort(r)]
  proxyTargetsLock.RUnlock()
  if pt != nil {
    targets := pt.getMatchingTargetsForRequest(r)
    if len(targets) > 0 {
      return true, targets
    }
  }
  return false, nil
}

func Middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    willProxy, targets := WillProxy(r)
    if !willProxy {
      next.ServeHTTP(w, r)
      return
    }
    util.AddLogMessage(fmt.Sprintf("Proxying to matching targets %s", util.ToJSON(targets)), r)
    proxyTargetsLock.RLock()
    pt := proxyTargetsByPort[util.GetListenerPort(r)]
    proxyTargetsLock.RUnlock()
    pt.invokeTargets(targets, w, r)
  })
}
