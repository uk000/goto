package uri

import (
	"fmt"
	"net/http"
	"strings"
	"sync"

	"goto/pkg/http/server/request/uri/bypass"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
  Handler         util.ServerHandler = util.ServerHandler{"uri", SetRoutes, Middleware}
  internalHandler util.ServerHandler = util.ServerHandler{Name: "uri", Middleware: middleware}
  uriCountsByPort map[string]map[string]int
  uriLock         sync.RWMutex
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  uriRouter := r.PathPrefix("/uri").Subrouter()
  bypass.SetRoutes(uriRouter, parent, root)
  util.AddRoute(uriRouter, "/counts", getURICallCounts, "GET")
  util.AddRoute(uriRouter, "/counts/clear", clearURICallCounts, "POST")
}

func initPort(r *http.Request) {
  port := util.GetListenerPort(r)
  uriLock.Lock()
  defer uriLock.Unlock()
  if uriCountsByPort == nil {
    uriCountsByPort = map[string]map[string]int{}
  }
  if uriCountsByPort[port] == nil {
    uriCountsByPort[port] = map[string]int{}
  }
}

func getURICallCounts(w http.ResponseWriter, r *http.Request) {
  initPort(r)
  uriLock.RLock()
  defer uriLock.RUnlock()
  w.WriteHeader(http.StatusOK)
  result := util.ToJSON(uriCountsByPort[util.GetListenerPort(r)])
  util.AddLogMessage(fmt.Sprintf("Reporting URI Call Counts: %s", result), r)
  fmt.Fprintf(w, "%s\n", result)
}

func clearURICallCounts(w http.ResponseWriter, r *http.Request) {
  initPort(r)
  uriLock.Lock()
  defer uriLock.Unlock()
  uriCountsByPort[util.GetListenerPort(r)] = map[string]int{}
  w.WriteHeader(http.StatusOK)
  fmt.Fprintln(w, "URI Call Counts Cleared")
  util.AddLogMessage("URI Call Counts Cleared", r)
}

func middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    util.AddLogMessage(fmt.Sprintf("Request URI: [%s], Method: [%s]", r.RequestURI, r.Method), r)
    if !util.IsAdminRequest(r) {
      initPort(r)
      port := util.GetListenerPort(r)
      uri := strings.ToLower(r.RequestURI)
      uriLock.Lock()
      uriCountsByPort[port][uri]++
      uriLock.Unlock()
    }
    next.ServeHTTP(w, r)
  })
}

func Middleware(next http.Handler) http.Handler {
  return util.AddMiddlewares(next, internalHandler, bypass.Handler)
}
