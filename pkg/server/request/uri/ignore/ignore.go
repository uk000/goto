package ignore

import (
  "fmt"
  "goto/pkg/global"
  "goto/pkg/util"
  "net/http"
  "strconv"
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
  Handler      util.ServerHandler = util.ServerHandler{Name: "ignore", SetRoutes: SetRoutes}
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
  util.AddRoute(ignoreRouter, "", getIgnoreList, "GET")
  global.IsIgnoredURI = IsIgnoredURI
}

func (b *Ignore) init() {
  b.Uris = map[string]interface{}{}
  b.IgnoreStatus = http.StatusOK
}

func (b *Ignore) addURI(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if uri, present := util.GetStringParam(r, "uri"); present {
    b.lock.Lock()
    defer b.lock.Unlock()
    uri = strings.ToLower(uri)
    b.Uris[uri] = 0
    msg = fmt.Sprintf("Ignore URI %s added", uri)
    w.WriteHeader(http.StatusAccepted)
  } else {
    msg = "Cannot add. Invalid Ignore URI"
    w.WriteHeader(http.StatusBadRequest)
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func (b *Ignore) removeURI(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if uri, present := util.GetStringParam(r, "uri"); present {
    b.lock.Lock()
    defer b.lock.Unlock()
    uri = strings.ToLower(uri)
    delete(b.Uris, uri)
    msg = fmt.Sprintf("Ignore URI %s removed", uri)
    w.WriteHeader(http.StatusAccepted)
  } else {
    msg = "Cannot remove. Invalid Ignore URI"
    w.WriteHeader(http.StatusBadRequest)
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func (b *Ignore) setStatus(w http.ResponseWriter, r *http.Request) {
  msg := ""
  statusCode, times, present := util.GetStatusParam(r)
  if present {
    b.lock.Lock()
    defer b.lock.Unlock()
    b.IgnoreStatus = statusCode
    b.statusCount = times
    if times > 0 {
      msg = fmt.Sprintf("Ignore Status set to %d for next %d calls", statusCode, times)
    } else {
      msg = fmt.Sprintf("Ignore Status set to %d forever", statusCode)
    }
    w.WriteHeader(http.StatusAccepted)
  } else {
    msg = fmt.Sprintf("Ignore Status %d", b.IgnoreStatus)
    w.WriteHeader(http.StatusOK)
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func (b *Ignore) clear(w http.ResponseWriter, r *http.Request) {
  b.lock.Lock()
  defer b.lock.Unlock()
  b.Uris = map[string]interface{}{}
  msg := "Ignore URIs cleared"
  w.WriteHeader(http.StatusAccepted)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func (b *Ignore) getCallCounts(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if uri, present := util.GetStringParam(r, "uri"); present {
    b.lock.RLock()
    defer b.lock.RUnlock()
    msg = fmt.Sprintf("Reporting call counts for uri %s = %d", uri, b.Uris[uri])
    w.WriteHeader(http.StatusOK)
    fmt.Fprintf(w, "%s\n", strconv.Itoa(b.Uris[uri].(int)))
  } else {
    msg = "Invalid Ignore URI"
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintln(w, msg)
  }
  util.AddLogMessage(msg, r)
}

func getIgnoreForPort(r *http.Request) *Ignore {
  lock.Lock()
  defer lock.Unlock()
  listenerPort := util.GetListenerPort(r)
  b, present := ignoreByPort[listenerPort]
  if !present {
    b = &Ignore{}
    b.init()
    ignoreByPort[listenerPort] = b
  }
  return b
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
