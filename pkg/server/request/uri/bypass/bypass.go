package bypass

import (
  "fmt"
  "goto/pkg/events"
  "goto/pkg/metrics"
  "goto/pkg/util"
  "net/http"
  "strings"
  "sync"

  "github.com/gorilla/mux"
)

type Bypass struct {
  Port         string                 `json:"port"`
  Uris         map[string]interface{} `json:"uris"`
  BypassStatus int                    `json:"bypassStatus"`
  statusCount  int
  lock         sync.RWMutex
}

var (
  Handler      util.ServerHandler = util.ServerHandler{"bypass", SetRoutes, Middleware}
  bypassByPort map[string]*Bypass = map[string]*Bypass{}
  bypassLock   sync.RWMutex
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  bypassRouter := util.PathRouter(r, "/bypass")
  util.AddRouteQWithPort(bypassRouter, "/add", addBypassURI, "uri", "{uri}", "PUT", "POST")
  util.AddRouteQWithPort(bypassRouter, "/remove", removeBypassURI, "uri", "{uri}", "PUT", "POST")
  util.AddRouteWithPort(bypassRouter, "/set/status={status}", setOrGetBypassStatus, "PUT", "POST")
  util.AddRouteWithPort(bypassRouter, "/status", setOrGetBypassStatus)
  util.AddRouteWithPort(bypassRouter, "/clear", clearBypassURIs, "PUT", "POST")
  util.AddRouteQWithPort(bypassRouter, "/counts", getBypassCallCount, "uri", "{uri}", "GET")
  util.AddRouteWithPort(bypassRouter, "/counts", getBypassCallCount, "GET")
  util.AddRouteWithPort(bypassRouter, "", getBypassList, "GET")
}

func (b *Bypass) init() {
  b.Uris = map[string]interface{}{}
  b.BypassStatus = http.StatusOK
}

func (b *Bypass) addURI(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if uri, present := util.GetStringParam(r, "uri"); present {
    b.lock.Lock()
    defer b.lock.Unlock()
    uri = strings.ToLower(uri)
    b.Uris[uri] = 0
    msg = fmt.Sprintf("Port [%s] Bypass URI [%s] added", b.Port, uri)
    events.SendRequestEvent("Bypass URI Added", msg, r)
    w.WriteHeader(http.StatusOK)
  } else {
    msg = "Cannot add. Invalid Bypass URI"
    w.WriteHeader(http.StatusBadRequest)
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func (b *Bypass) removeURI(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if uri, present := util.GetStringParam(r, "uri"); present {
    b.lock.Lock()
    defer b.lock.Unlock()
    uri = strings.ToLower(uri)
    delete(b.Uris, uri)
    msg = fmt.Sprintf("Port [%s] Bypass URI [%s] removed", b.Port, uri)
    events.SendRequestEvent("Bypass URI Removed", msg, r)
    w.WriteHeader(http.StatusOK)
  } else {
    msg = "Cannot remove. Invalid Bypass URI"
    w.WriteHeader(http.StatusBadRequest)
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func (b *Bypass) setStatus(w http.ResponseWriter, r *http.Request) {
  msg := ""
  statusCode, times, present := util.GetStatusParam(r)
  if present {
    b.lock.Lock()
    defer b.lock.Unlock()
    b.BypassStatus = statusCode
    b.statusCount = times
    if times > 0 {
      msg = fmt.Sprintf("Port [%s] Bypass Status set to [%d] for next [%d] calls", b.Port, statusCode, times)
    } else {
      msg = fmt.Sprintf("Port [%s] Bypass Status set to [%d] forever", b.Port, statusCode)
    }
    events.SendRequestEvent("Bypass Status Configured", msg, r)
    w.WriteHeader(http.StatusOK)
  } else {
    msg = fmt.Sprintf("Bypass Status %d", b.BypassStatus)
    w.WriteHeader(http.StatusOK)
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func (b *Bypass) clear(w http.ResponseWriter, r *http.Request) {
  b.lock.Lock()
  defer b.lock.Unlock()
  b.Uris = map[string]interface{}{}
  msg := fmt.Sprintf("Port[%s] Bypass URIs Cleared", b.Port)
  w.WriteHeader(http.StatusOK)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
  events.SendRequestEvent("Bypass URIs Cleared", msg, r)
}

func (b *Bypass) getCallCounts(w http.ResponseWriter, r *http.Request) {
  b.lock.RLock()
  defer b.lock.RUnlock()
  msg := ""
  if uri, present := util.GetStringParam(r, "uri"); present {
    if bypassURI := b.Uris[uri]; bypassURI != nil {
      msg = fmt.Sprintf("Reporting call counts for bypass uri %s = %d", uri, bypassURI)
      fmt.Fprintf(w, "{\"%s\": %d}", uri, bypassURI.(int))
    } else {
      msg = fmt.Sprintf("Invalid bypass uri %s", uri)
      w.WriteHeader(http.StatusBadRequest)
      fmt.Fprintf(w, "{\"error\": \"%s\"}", msg)
    }
  } else {
    msg = "Reporting call counts for all bypass uris"
    fmt.Fprintln(w, util.ToJSON(b.Uris))
  }
  util.AddLogMessage(msg, r)
}

func getBypassForPort(r *http.Request) *Bypass {
  bypassLock.Lock()
  defer bypassLock.Unlock()
  listenerPort := util.GetRequestOrListenerPort(r)
  b, present := bypassByPort[listenerPort]
  if !present {
    b = &Bypass{Port: listenerPort}
    b.init()
    bypassByPort[listenerPort] = b
  }
  return b
}

func addBypassURI(w http.ResponseWriter, r *http.Request) {
  getBypassForPort(r).addURI(w, r)
}

func removeBypassURI(w http.ResponseWriter, r *http.Request) {
  getBypassForPort(r).removeURI(w, r)
}

func setOrGetBypassStatus(w http.ResponseWriter, r *http.Request) {
  getBypassForPort(r).setStatus(w, r)
}

func clearBypassURIs(w http.ResponseWriter, r *http.Request) {
  getBypassForPort(r).clear(w, r)
}

func getBypassCallCount(w http.ResponseWriter, r *http.Request) {
  getBypassForPort(r).getCallCounts(w, r)
}

func getBypassList(w http.ResponseWriter, r *http.Request) {
  b := getBypassForPort(r)
  util.AddLogMessage(fmt.Sprintf("Reporting Bypass URIs: %+v", b), r)
  b.lock.RLock()
  result := util.ToJSON(b)
  b.lock.RUnlock()
  w.WriteHeader(http.StatusOK)
  fmt.Fprintln(w, string(result))
}

func IsBypassURI(r *http.Request) bool {
  b := getBypassForPort(r)
  b.lock.RLock()
  defer b.lock.RUnlock()
  return util.IsURIInMap(r.RequestURI, b.Uris)
}

func Middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    b := getBypassForPort(r)
    b.lock.RLock()
    uri := util.FindURIInMap(r.RequestURI, b.Uris)
    b.lock.RUnlock()
    if uri != "" {
      metrics.UpdateURIRequestCount(uri)
      b.lock.Lock()
      b.Uris[uri] = b.Uris[uri].(int) + 1
      util.CopyHeaders("Bypass-Request", w, r.Header, r.Host, r.RequestURI)
      w.WriteHeader(b.BypassStatus)
      b.lock.Unlock()
      util.AddLogMessage("Bypassing URI", r)
      util.PrintLogMessages(r)
      return
    }
    if next != nil {
      next.ServeHTTP(w, r)
    }
  })
}
