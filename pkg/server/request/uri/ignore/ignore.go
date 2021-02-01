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
  Uris         map[string]interface{} `json:"uris"`
  IgnoreStatus int                    `json:"ignoreStatus"`
  statusCount  int
  lock         sync.RWMutex
}

var (
  Handler      util.ServerHandler = util.ServerHandler{"ignore", SetRoutes, Middleware}
  ignoreByPort map[string]*Ignore = map[string]*Ignore{}
  lock         sync.RWMutex
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  ignoreRouter := r.PathPrefix("/ignore").Subrouter()
  util.AddRouteQ(ignoreRouter, "/add", addIgnoreURI, "uri", "{uri}", "PUT", "POST")
  util.AddRouteQ(ignoreRouter, "/remove", removeIgnoreURI, "uri", "{uri}", "PUT", "POST")
  util.AddRoute(ignoreRouter, "/status/set/{status}", setOrGetIgnoreStatus, "PUT", "POST")
  util.AddRoute(ignoreRouter, "/status", setOrGetIgnoreStatus)
  util.AddRoute(ignoreRouter, "/clear", clearIgnoreURIs, "PUT", "POST")
  util.AddRouteQ(ignoreRouter, "/counts", getIgnoreCallCount, "uri", "{uri}", "GET")
  util.AddRoute(ignoreRouter, "/counts", getIgnoreCallCount, "GET")
  util.AddRoute(ignoreRouter, "", getIgnoreList, "GET")
  global.IsIgnoredURI = IsIgnoredURI
}

func (i *Ignore) init() {
  i.Uris = map[string]interface{}{}
  i.IgnoreStatus = 0
}

func (i *Ignore) addURI(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if uri, present := util.GetStringParam(r, "uri"); present {
    i.lock.Lock()
    defer i.lock.Unlock()
    uri = strings.ToLower(uri)
    i.Uris[uri] = 0
    msg = fmt.Sprintf("Ignore URI %s added", uri)
    events.SendRequestEvent("Ignore URI Added", msg, r)
    w.WriteHeader(http.StatusOK)
  } else {
    msg = "Cannot add. Invalid Ignore URI"
    w.WriteHeader(http.StatusBadRequest)
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func (i *Ignore) removeURI(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if uri, present := util.GetStringParam(r, "uri"); present {
    i.lock.Lock()
    defer i.lock.Unlock()
    uri = strings.ToLower(uri)
    delete(i.Uris, uri)
    msg = fmt.Sprintf("Ignore URI %s removed", uri)
    events.SendRequestEvent("Ignore URI Removed", msg, r)
    w.WriteHeader(http.StatusOK)
  } else {
    msg = "Cannot remove. Invalid Ignore URI"
    w.WriteHeader(http.StatusBadRequest)
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func (i *Ignore) setStatus(w http.ResponseWriter, r *http.Request) {
  msg := ""
  statusCode, times, present := util.GetStatusParam(r)
  if present {
    i.lock.Lock()
    defer i.lock.Unlock()
    i.IgnoreStatus = statusCode
    i.statusCount = times
    if times > 0 {
      msg = fmt.Sprintf("Ignore Status set to %d for next %d calls", statusCode, times)
    } else {
      msg = fmt.Sprintf("Ignore Status set to %d forever", statusCode)
    }
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
  msg := "Ignore URIs Cleared"
  w.WriteHeader(http.StatusOK)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
  events.SendRequestEvent(msg, "", r)
}

func (i *Ignore) getCallCounts(w http.ResponseWriter, r *http.Request) {
  i.lock.RLock()
  defer i.lock.RUnlock()
  msg := ""
  if uri, present := util.GetStringParam(r, "uri"); present {
    if ignoreURI := i.Uris[uri]; ignoreURI != nil {
      msg = fmt.Sprintf("Reporting call counts for ignored uri %s = %d", uri, ignoreURI)
      fmt.Fprintf(w, "{\"%s\": %d}", uri, ignoreURI.(int))
    } else {
      msg = fmt.Sprintf("Invalid ignored uri %s", uri)
      w.WriteHeader(http.StatusBadRequest)
      fmt.Fprintf(w, "{\"error\": \"%s\"}", msg)
    }
  } else {
    msg = "Reporting call counts for all ignored uris"
    fmt.Fprintln(w, util.ToJSON(i.Uris))
  }
  util.AddLogMessage(msg, r)
}

func getIgnoreForPort(r *http.Request) *Ignore {
  lock.Lock()
  defer lock.Unlock()
  listenerPort := util.GetListenerPort(r)
  i, present := ignoreByPort[listenerPort]
  if !present {
    i = &Ignore{}
    i.init()
    ignoreByPort[listenerPort] = i
  }
  return i
}

func addIgnoreURI(w http.ResponseWriter, r *http.Request) {
  getIgnoreForPort(r).addURI(w, r)
}

func removeIgnoreURI(w http.ResponseWriter, r *http.Request) {
  getIgnoreForPort(r).removeURI(w, r)
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
  ignore := getIgnoreForPort(r)
  util.AddLogMessage(fmt.Sprintf("Reporting Ignored URIs: %+v", ignore), r)
  ignore.lock.RLock()
  result := util.ToJSON(ignore)
  ignore.lock.RUnlock()
  w.WriteHeader(http.StatusOK)
  fmt.Fprintln(w, string(result))
}

func IsIgnoredURI(r *http.Request) bool {
  ignore := getIgnoreForPort(r)
  ignore.lock.RLock()
  defer ignore.lock.RUnlock()
  return util.IsURIInMap(r.RequestURI, ignore.Uris)
}

func Middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    if !util.IsAdminRequest(r) {
      ignore := getIgnoreForPort(r)
      ignore.lock.RLock()
      uri := util.FindURIInMap(r.RequestURI, ignore.Uris)
      ignore.lock.RUnlock()
      if uri != "" {
        metrics.UpdateURIRequestCount(uri)
        ignore.lock.Lock()
        ignore.Uris[uri] = ignore.Uris[uri].(int) + 1
        ignore.lock.Unlock()
        w.Header().Add("Ignored-Request", "true")
        if ignore.IgnoreStatus > 0 {
          util.CopyHeaders("Ignore-Request", w, r.Header, r.Host, r.RequestURI)
          w.WriteHeader(ignore.IgnoreStatus)
          return
        }
      }
    }
    if next != nil {
      next.ServeHTTP(w, r)
    }
  })
}
