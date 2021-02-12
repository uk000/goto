package ignore

import (
  "fmt"
  "goto/pkg/events"
  "goto/pkg/global"
  "goto/pkg/util"
  "net/http"
  "strings"
  "sync"

  "github.com/gorilla/mux"
)

type Ignore struct {
  Port          string                            `json:"port"`
  Uris          map[string]interface{}            `json:"uris"`
  Headers       map[string]map[string]interface{} `json:"headers"`
  IgnoreStatus  int                               `json:"ignoreStatus"`
  IgnoreCount   int64                             `json:"ignoreCount"`
  NewUris       map[string]interface{}            `json:"newUris"`
  NewHeaders    map[string]map[string]interface{} `json:"newHeaders"`
  configChanged bool
  lock          sync.RWMutex
}

var (
  Handler      util.ServerHandler = util.ServerHandler{"ignore", SetRoutes, Middleware}
  ignoreByPort map[string]*Ignore = map[string]*Ignore{}
  lock         sync.RWMutex
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  ignoreRouter := util.PathRouter(r, "/ignore")
  util.AddRouteQWithPort(ignoreRouter, "/add", addIgnoreHeaderOrURI, "uri", "{uri}", "PUT", "POST")
  util.AddRouteWithPort(ignoreRouter, "/add/header/{header}~{value}", addIgnoreHeaderOrURI, "PUT", "POST")
  util.AddRouteWithPort(ignoreRouter, "/add/header/{header}", addIgnoreHeaderOrURI, "PUT", "POST")
  util.AddRouteWithPort(ignoreRouter, "/remove/header/{header}~{value}", removeIgnoreHeaderOrURI, "PUT", "POST")
  util.AddRouteWithPort(ignoreRouter, "/remove/header/{header}", removeIgnoreHeaderOrURI, "PUT", "POST")
  util.AddRouteQWithPort(ignoreRouter, "/remove", removeIgnoreHeaderOrURI, "uri", "{uri}", "PUT", "POST")
  util.AddRouteWithPort(ignoreRouter, "/set/status={status}", setOrGetIgnoreStatus, "PUT", "POST")
  util.AddRouteWithPort(ignoreRouter, "/status", setOrGetIgnoreStatus)
  util.AddRouteWithPort(ignoreRouter, "/clear", clearIgnoreURIs, "PUT", "POST")
  util.AddRouteWithPort(ignoreRouter, "/counts", getIgnoreCallCount, "GET")
  util.AddRouteWithPort(ignoreRouter, "", getIgnoreList, "GET")
  global.IsIgnoredRequest = IsIgnoredRequest
}

func (i *Ignore) init() {
  i.lock.Lock()
  i.Uris = map[string]interface{}{}
  i.Headers = map[string]map[string]interface{}{}
  i.NewUris = map[string]interface{}{}
  i.NewHeaders = map[string]map[string]interface{}{}
  i.IgnoreStatus = http.StatusOK
  i.lock.Unlock()
}

func (i *Ignore) addIgnoreHeaderOrURI(w http.ResponseWriter, r *http.Request) {
  msg := ""
  uri := util.GetStringParamValue(r, "uri")
  header := util.GetStringParamValue(r, "header")
  value := util.GetStringParamValue(r, "value")
  if uri != "" {
    uri = strings.ToLower(uri)
    i.lock.Lock()
    i.NewUris[uri] = 0
    i.configChanged = true
    i.lock.Unlock()
    msg = fmt.Sprintf("Port [%s] will ignore URI [%s]", i.Port, uri)
    events.SendRequestEvent("Ignore URI Added", msg, r)
  } else if header != "" {
    header = strings.ToLower(header)
    if value != "" {
      value = strings.ToLower(value)
    }
    i.lock.Lock()
    if i.NewHeaders[header] == nil {
      i.NewHeaders[header] = map[string]interface{}{}
    }
    i.NewHeaders[header][value] = 0
    i.configChanged = true
    i.lock.Unlock()
    msg = fmt.Sprintf("Port [%s] will ignore header [%s : %s]", i.Port, header, value)
    events.SendRequestEvent("Ignore Header Added", msg, r)
  } else {
    msg = "Cannot add ignore. No URI or Header"
    w.WriteHeader(http.StatusBadRequest)
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func (i *Ignore) removeIgnoreHeaderOrURI(w http.ResponseWriter, r *http.Request) {
  msg := ""
  uri := util.GetStringParamValue(r, "uri")
  header := util.GetStringParamValue(r, "header")
  value := util.GetStringParamValue(r, "value")
  if uri != "" {
    uri = strings.ToLower(uri)
    i.lock.Lock()
    delete(i.NewUris, uri)
    i.configChanged = true
    i.lock.Unlock()
    msg = fmt.Sprintf("Port [%s] Ignore URI [%s] removed", i.Port, uri)
    events.SendRequestEvent("Ignore URI Removed", msg, r)
  } else if header != "" {
    header = strings.ToLower(header)
    if value != "" {
      value = strings.ToLower(value)
    }
    i.lock.Lock()
    if i.NewHeaders[header] != nil {
      delete(i.NewHeaders[header], value)
      if len(i.NewHeaders[header]) == 0 {
        delete(i.NewHeaders, header)
      }
    }
    i.configChanged = true
    i.lock.Unlock()
    msg = fmt.Sprintf("Port [%s] Ignore Header [%s: %s] removed", i.Port, header, value)
    events.SendRequestEvent("Ignore Header Removed", msg, r)
  } else {
    msg = "Cannot remove ignore. No URI or Header"
    w.WriteHeader(http.StatusBadRequest)
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func (i *Ignore) setStatus(w http.ResponseWriter, r *http.Request) {
  msg := ""
  statusCode, _, present := util.GetStatusParam(r)
  if present {
    i.IgnoreStatus = statusCode
    msg = fmt.Sprintf("Port[%s] Ignore Status set to [%d] forever", i.Port, statusCode)
    events.SendRequestEvent("Ignore Status Configured", msg, r)
  } else {
    msg = fmt.Sprintf("Ignore Status %d", i.IgnoreStatus)
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func (i *Ignore) clear(w http.ResponseWriter, r *http.Request) {
  i.lock.Lock()
  i.NewUris = map[string]interface{}{}
  i.NewHeaders = map[string]map[string]interface{}{}
  i.IgnoreCount = 0
  i.configChanged = true
  i.lock.Unlock()
  msg := fmt.Sprintf("Port[%s] Ignore Config Cleared", i.Port)
  w.WriteHeader(http.StatusOK)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
  events.SendRequestEvent("Ignore Config Cleared", msg, r)
}

func (i *Ignore) getCallCounts(w http.ResponseWriter, r *http.Request) {
  msg := "Reporting ignore call counts"
  util.WriteJsonPayload(w, map[string]interface{}{"ignoredRequests": i.IgnoreCount})
  util.AddLogMessage(msg, r)
}

func (i *Ignore) getIgnoredURI(r *http.Request) string {
  if i.configChanged {
    i.lock.Lock()
    i.Uris = i.NewUris
    i.Headers = i.NewHeaders
    i.configChanged = false
    i.lock.Unlock()
  }
  i.lock.RLock()
  uris := i.Uris
  i.lock.RUnlock()
  return util.FindURIInMap(r.RequestURI, uris)
}

func (i *Ignore) getIgnoredHeader(r *http.Request) (string, string) {
  if i.configChanged {
    i.lock.Lock()
    i.Uris = i.NewUris
    i.Headers = i.NewHeaders
    i.configChanged = false
    i.lock.Unlock()
  }
  i.lock.RLock()
  headers := i.Headers
  i.lock.RUnlock()
  if len(headers) == 0 {
    return "", ""
  }
  for h, values := range r.Header {
    h = strings.ToLower(h)
    hvIgnoreMap := headers[h]
    if hvIgnoreMap == nil {
      continue
    }
    if hvIgnoreMap[""] != nil {
      return h, ""
    }
    for ignoreV, _ := range hvIgnoreMap {
      for _, v := range values {
        v = strings.ToLower(v)
        if strings.Contains(v, ignoreV) {
          return h, ignoreV
        }
      }
    }
  }
  return "", ""
}

func getIgnoreForPort(r *http.Request) *Ignore {
  listenerPort := util.GetRequestOrListenerPort(r)
  lock.RLock()
  i, present := ignoreByPort[listenerPort]
  lock.RUnlock()
  if !present {
    i = &Ignore{Port: listenerPort}
    i.init()
    lock.Lock()
    ignoreByPort[listenerPort] = i
    lock.Unlock()
  }
  return i
}

func addIgnoreHeaderOrURI(w http.ResponseWriter, r *http.Request) {
  getIgnoreForPort(r).addIgnoreHeaderOrURI(w, r)
}

func removeIgnoreHeaderOrURI(w http.ResponseWriter, r *http.Request) {
  getIgnoreForPort(r).removeIgnoreHeaderOrURI(w, r)
}

func setOrGetIgnoreStatus(w http.ResponseWriter, r *http.Request) {
  getIgnoreForPort(r).setStatus(w, r)
}

func clearIgnoreURIs(w http.ResponseWriter, r *http.Request) {
  getIgnoreForPort(r).clear(w, r)
}

func getIgnoreCallCount(w http.ResponseWriter, r *http.Request) {
  getIgnoreForPort(r).getCallCounts(w, r)
}

func getIgnoreList(w http.ResponseWriter, r *http.Request) {
  util.AddLogMessage("Reporting Ignored URIs", r)
  lock.RLock()
  result := util.ToJSON(ignoreByPort)
  lock.RUnlock()
  fmt.Fprintln(w, string(result))
}

func IsIgnoredRequest(r *http.Request) bool {
  ignore := getIgnoreForPort(r)
  if ignore.getIgnoredURI(r) != "" {
    return true
  }
  h, _ := ignore.getIgnoredHeader(r)
  return h != ""
}

func Middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    if !util.IsAdminRequest(r) {
      ignore := getIgnoreForPort(r)
      uri := ignore.getIgnoredURI(r)
      header, _ := ignore.getIgnoredHeader(r)
      ignoreCount := ignore.IgnoreCount + 1
      if uri != "" || header != "" {
        ignore.IgnoreCount = ignoreCount
        w.Header().Add("Goto-Ignored-Request", "true")
        util.CopyHeaders("Ignore-Request", w, r.Header, r.Host, r.RequestURI)
        if ignore.IgnoreStatus > 0 {
          w.WriteHeader(ignore.IgnoreStatus)
        }
        util.SetIgnoredRequest(r)
        return
      }
    }
    if next != nil {
      next.ServeHTTP(w, r)
    }
  })
}
