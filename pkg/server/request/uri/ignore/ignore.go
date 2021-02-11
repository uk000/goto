package ignore

import (
  "fmt"
  "goto/pkg/events"
  "goto/pkg/global"
  "goto/pkg/metrics"
  "goto/pkg/util"
  "net/http"
  "strings"
  "sync"

  "github.com/gorilla/mux"
)

type Ignore struct {
  Port         string                            `json:"port"`
  Uris         map[string]interface{}            `json:"uris"`
  Headers      map[string]map[string]interface{} `json:"headers"`
  IgnoreStatus int                               `json:"ignoreStatus"`
  statusCount  int
  lock         sync.RWMutex
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
  i.Uris = map[string]interface{}{}
  i.Headers = map[string]map[string]interface{}{}
  i.IgnoreStatus = 0
}

func (i *Ignore) addIgnoreHeaderOrURI(w http.ResponseWriter, r *http.Request) {
  msg := ""
  uri := util.GetStringParamValue(r, "uri")
  header := util.GetStringParamValue(r, "header")
  value := util.GetStringParamValue(r, "value")
  if uri != "" {
    uri = strings.ToLower(uri)
    i.lock.Lock()
    i.Uris[uri] = 0
    i.lock.Unlock()
    msg = fmt.Sprintf("Port [%s] will ignore URI [%s]", i.Port, uri)
    events.SendRequestEvent("Ignore URI Added", msg, r)
  } else if header != "" {
    header = strings.ToLower(header)
    if value != "" {
      value = strings.ToLower(value)
    }
    i.lock.Lock()
    if i.Headers[header] == nil {
      i.Headers[header] = map[string]interface{}{}
    }
    i.Headers[header][value] = 0
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
    delete(i.Uris, uri)
    i.lock.Unlock()
    msg = fmt.Sprintf("Port [%s] Ignore URI [%s] removed", i.Port, uri)
    events.SendRequestEvent("Ignore URI Removed", msg, r)
  } else if header != "" {
    header = strings.ToLower(header)
    if value != "" {
      value = strings.ToLower(value)
    }
    i.lock.Lock()
    if i.Headers[header] != nil {
      delete(i.Headers[header], value)
      if len(i.Headers[header]) == 0 {
        delete(i.Headers, header)
      }
    }
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
    i.lock.Lock()
    defer i.lock.Unlock()
    i.IgnoreStatus = statusCode
    msg = fmt.Sprintf("Port[%s] Ignore Status set to [%d] forever", i.Port, statusCode)
    events.SendRequestEvent("Ignore Status Configured", msg, r)
    w.WriteHeader(http.StatusOK)
  } else {
    msg = fmt.Sprintf("Ignore Status %d", i.IgnoreStatus)
    w.WriteHeader(http.StatusOK)
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func (i *Ignore) clear(w http.ResponseWriter, r *http.Request) {
  i.lock.Lock()
  defer i.lock.Unlock()
  i.Uris = map[string]interface{}{}
  i.Headers = map[string]map[string]interface{}{}
  msg := fmt.Sprintf("Port[%s] Ignore Config Cleared", i.Port)
  w.WriteHeader(http.StatusOK)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
  events.SendRequestEvent("Ignore Config Cleared", msg, r)
}

func (i *Ignore) getCallCounts(w http.ResponseWriter, r *http.Request) {
  i.lock.RLock()
  defer i.lock.RUnlock()
  msg := "Reporting ignore call counts"
  util.WriteJsonPayload(w, map[string]interface{}{"uris": i.Uris, "headers": i.Headers})
  util.AddLogMessage(msg, r)
}

func (i *Ignore) getIgnoredURI(r *http.Request) string {
  i.lock.RLock()
  defer i.lock.RUnlock()
  return util.FindURIInMap(r.RequestURI, i.Uris)
}

func (i *Ignore) getIgnoredHeader(r *http.Request) (string, string) {
  i.lock.RLock()
  defer i.lock.RUnlock()
  if len(i.Headers) == 0 {
    return "", ""
  }
  for h, values := range r.Header {
    h = strings.ToLower(h)
    hvIgnoreMap := i.Headers[h]
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
  lock.Lock()
  defer lock.Unlock()
  listenerPort := util.GetRequestOrListenerPort(r)
  i, present := ignoreByPort[listenerPort]
  if !present {
    i = &Ignore{Port: listenerPort}
    i.init()
    ignoreByPort[listenerPort] = i
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
      ignore.lock.RLock()
      uri := ignore.getIgnoredURI(r)
      header, value := ignore.getIgnoredHeader(r)
      ignore.lock.RUnlock()
      ignored := false
      if uri != "" {
        ignored = true
        ignore.lock.Lock()
        ignore.Uris[uri] = ignore.Uris[uri].(int) + 1
        ignore.lock.Unlock()
      } else if header != "" {
        ignored = true
        ignore.lock.Lock()
        ignore.Headers[header][value] = ignore.Headers[header][value].(int) + 1
        ignore.lock.Unlock()
      }
      if ignored {
        metrics.UpdateURIRequestCount(uri)
        w.Header().Add("Goto-Ignored-Request", "true")
        util.CopyHeaders("Ignore-Request", w, r.Header, r.Host, r.RequestURI)
        if ignore.IgnoreStatus > 0 {
          w.WriteHeader(ignore.IgnoreStatus)
        }
        return
      }
    }
    if next != nil {
      next.ServeHTTP(w, r)
    }
  })
}
