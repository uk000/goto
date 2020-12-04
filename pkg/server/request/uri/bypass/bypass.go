package bypass

import (
	"fmt"
	"goto/pkg/util"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/gorilla/mux"
)

type Bypass struct {
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
  bypassRouter := r.PathPrefix("/bypass").Subrouter()
  util.AddRouteQ(bypassRouter, "/add", addBypassURI, "uri", "{uri}", "PUT", "POST")
  util.AddRouteQ(bypassRouter, "/remove", removeBypassURI, "uri", "{uri}", "PUT", "POST")
  util.AddRoute(bypassRouter, "/status/set/{status}", setOrGetBypassStatus, "PUT", "POST")
  util.AddRoute(bypassRouter, "/status", setOrGetBypassStatus)
  util.AddRoute(bypassRouter, "/clear", clearBypassURIs, "PUT", "POST")
  util.AddRouteQ(bypassRouter, "/counts", getBypassCallCount, "uri", "{uri}", "GET")
  util.AddRoute(bypassRouter, "/list", getBypassList, "GET")
  util.AddRoute(bypassRouter, "", getBypassList, "GET")
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
    msg = fmt.Sprintf("Bypass URI %s added", uri)
    w.WriteHeader(http.StatusAccepted)
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
    msg = fmt.Sprintf("Bypass URI %s removed", uri)
    w.WriteHeader(http.StatusAccepted)
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
      msg = fmt.Sprintf("Bypass Status set to %d for next %d calls", statusCode, times)
    } else {
      msg = fmt.Sprintf("Bypass Status set to %d forever", statusCode)
    }
    w.WriteHeader(http.StatusAccepted)
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
  msg := "Bypass URIs cleared"
  w.WriteHeader(http.StatusAccepted)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func (b *Bypass) getCallCounts(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if uri, present := util.GetStringParam(r, "uri"); present {
    b.lock.RLock()
    defer b.lock.RUnlock()
    msg = fmt.Sprintf("Reporting call counts for uri %s = %d", uri, b.Uris[uri])
    w.WriteHeader(http.StatusOK)
    fmt.Fprintf(w, "%s\n", strconv.Itoa(b.Uris[uri].(int)))
  } else {
    msg = "Invalid Bypass URI"
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintln(w, msg)
  }
  util.AddLogMessage(msg, r)
}

func getBypassForPort(r *http.Request) *Bypass {
  bypassLock.Lock()
  defer bypassLock.Unlock()
  listenerPort := util.GetListenerPort(r)
  b, present := bypassByPort[listenerPort]
  if !present {
    b = &Bypass{}
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
      b.lock.Lock()
      b.Uris[uri] = b.Uris[uri].(int) + 1
      util.CopyHeaders("Bypass-Request", w, r.Header, r.Host, r.RequestURI)
      w.WriteHeader(b.BypassStatus)
      b.lock.Unlock()
      util.AddLogMessage("Bypassing URI", r)
      util.PrintLogMessages(r)
      return
    }
    next.ServeHTTP(w, r)
  })
}
