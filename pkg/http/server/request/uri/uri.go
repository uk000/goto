package uri

import (
	"fmt"
	"net/http"
	"strings"
	"sync"

	"goto/pkg/http/server/intercept"
	"goto/pkg/http/server/request/uri/bypass"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
  Handler            util.ServerHandler = util.ServerHandler{"uri", SetRoutes, Middleware}
  internalHandler    util.ServerHandler = util.ServerHandler{Name: "uri", Middleware: middleware}
  uriCountsByPort    map[string]map[string]int
  uriStatusByPort    map[string]map[string][]int
  trackURICallCounts bool
  uriLock            sync.RWMutex
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  uriRouter := r.PathPrefix("/uri").Subrouter()
  bypass.SetRoutes(uriRouter, parent, root)
  util.AddRouteMultiQ(uriRouter, "/status/set", setStatus, "POST", "uri", "{uri}", "status", "{status}")
  util.AddRoute(uriRouter, "/counts/enable", enableURICallCounts, "POST")
  util.AddRoute(uriRouter, "/counts/disable", disableURICallCounts, "POST")
  util.AddRoute(uriRouter, "/counts", getURICallCounts, "GET")
  util.AddRoute(uriRouter, "/counts/clear", clearURICallCounts, "POST")
}

func initPort(r *http.Request) string {
  port := util.GetListenerPort(r)
  uriLock.Lock()
  defer uriLock.Unlock()
  if uriCountsByPort == nil {
    uriCountsByPort = map[string]map[string]int{}
    uriStatusByPort = map[string]map[string][]int{}
  }
  if uriCountsByPort[port] == nil {
    uriCountsByPort[port] = map[string]int{}
    uriStatusByPort[port] = map[string][]int{}
  }
  return port
}

func setStatus(w http.ResponseWriter, r *http.Request) {
  port := initPort(r)
  msg := ""
  uriLock.Lock()
  defer uriLock.Unlock()
  if uri, present := util.GetStringParam(r, "uri"); present {
    uri = strings.ToLower(uri)
    if statusCode, times, statusCodePresent := util.GetStatusParam(r); statusCodePresent && statusCode > 0 {
      uriStatusByPort[port][uri] = []int{statusCode, times}
      if times > 0 {
        msg = fmt.Sprintf("URI %s status set to %d for next %d calls", uri, statusCode, times)
      } else {
        msg = fmt.Sprintf("URI %s status set to %d forever", uri, statusCode)
      }
    } else {
      delete(uriStatusByPort[port], uri)
      msg = fmt.Sprintf("URI %s status cleared", uri)
    }
    w.WriteHeader(http.StatusAccepted)
  } else {
    msg = "Cannot add. Invalid URI"
    w.WriteHeader(http.StatusBadRequest)
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
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

func enableURICallCounts(w http.ResponseWriter, r *http.Request) {
  uriLock.Lock()
  trackURICallCounts = true
  uriLock.Unlock()
  w.WriteHeader(http.StatusOK)
  fmt.Fprintln(w, "URI Call Counts Enabled")
  util.AddLogMessage("URI Call Counts Enabled", r)
}

func disableURICallCounts(w http.ResponseWriter, r *http.Request) {
  uriLock.Lock()
  trackURICallCounts = false
  uriLock.Unlock()
  w.WriteHeader(http.StatusOK)
  fmt.Fprintln(w, "URI Call Counts Disabled")
  util.AddLogMessage("URI Call Counts Disabled", r)
}

func middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    util.AddLogMessage(fmt.Sprintf("Request URI: [%s], Protocol: [%s], Method: [%s]", r.RequestURI, r.Proto, r.Method), r)
    if !util.IsAdminRequest(r) {
      track := false
      port := initPort(r)
      uri := strings.ToLower(r.URL.Path)
      var statusToReport = 0
      uriLock.RLock()
      track = trackURICallCounts
      if uriStatusByPort[port][uri] != nil && uriStatusByPort[port][uri][0] > 0 && uriStatusByPort[port][uri][1] >= 0 {
        statusToReport = uriStatusByPort[port][uri][0]
      }
      uriLock.RUnlock()
      if track {
        uriLock.Lock()
        uriCountsByPort[port][uri]++
        uriLock.Unlock()
      }
      if statusToReport > 0 {
        uriLock.Lock()
        if uriStatusByPort[port][uri][1] >= 1 {
          uriStatusByPort[port][uri][1]--
          if uriStatusByPort[port][uri][1] == 0 {
            delete(uriStatusByPort[port], uri)
          }
        }
        uriLock.Unlock()
        crw := intercept.NewInterceptResponseWriter(w, true)
        next.ServeHTTP(crw, r)
        util.AddLogMessage(fmt.Sprintf("Reporting URI status: [%d] for URI [%s]", statusToReport, uri), r)
        crw.StatusCode = statusToReport
        crw.Proceed()
      } else {
        next.ServeHTTP(w, r)
      }
    } else {
      next.ServeHTTP(w, r)
    }
  })
}

func Middleware(next http.Handler) http.Handler {
  return util.AddMiddlewares(next, internalHandler, bypass.Handler)
}
