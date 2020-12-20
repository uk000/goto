package header

import (
	"fmt"
	"net/http"
	"strings"
	"sync"

	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
  Handler               util.ServerHandler             = util.ServerHandler{"response.header", SetRoutes, Middleware}
  responseHeadersByPort map[string]map[string][]string = map[string]map[string][]string{}
  headersLock           sync.RWMutex
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  headersRouter := r.PathPrefix("/headers").Subrouter()
  util.AddRoute(headersRouter, "/add/{header}/{value}", addResponseHeader, "PUT", "POST")
  util.AddRoute(headersRouter, "/remove/{header}", removeResponseHeader, "PUT", "POST")
  util.AddRoute(headersRouter, "/clear", clearResponseHeader, "PUT", "POST")
  util.AddRoute(headersRouter, "/list", getResponseHeaders, "GET")
  util.AddRoute(headersRouter, "", getResponseHeaders, "GET")
}

func addResponseHeader(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if header, present := util.GetStringParam(r, "header"); present {
    headersLock.Lock()
    defer headersLock.Unlock()
    value, _ := util.GetStringParam(r, "value")
    listenerPort := util.GetListenerPort(r)
    headerMap := responseHeadersByPort[listenerPort]
    if headerMap == nil {
      headerMap = map[string][]string{}
      responseHeadersByPort[listenerPort] = headerMap
    }
    values, present := headerMap[header]
    if !present {
      values = []string{}
    }
    headerMap[header] = append(values, value)
    msg = fmt.Sprintf("Response header [%s : %s] added", header, value)
    w.WriteHeader(http.StatusAccepted)
  } else {
    msg = "Cannot add. Invalid header"
    w.WriteHeader(http.StatusBadRequest)
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func removeResponseHeader(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if header, present := util.GetStringParam(r, "header"); present {
    headersLock.Lock()
    defer headersLock.Unlock()
    listenerPort := util.GetListenerPort(r)
    if headerMap := responseHeadersByPort[listenerPort]; headerMap != nil {
      delete(headerMap, header)
    }
    msg = fmt.Sprintf("Response header [%s] removed", header)
    w.WriteHeader(http.StatusAccepted)
  } else {
    msg = "Cannot remove. Invalid header"
    w.WriteHeader(http.StatusBadRequest)
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func clearResponseHeader(w http.ResponseWriter, r *http.Request) {
  headersLock.Lock()
  defer headersLock.Unlock()
  responseHeadersByPort[util.GetListenerPort(r)] = map[string][]string{}
  msg := "Response headers cleared"
  w.WriteHeader(http.StatusAccepted)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func getResponseHeaders(w http.ResponseWriter, r *http.Request) {
  headersLock.RLock()
  defer headersLock.RUnlock()
  headerMap := responseHeadersByPort[util.GetListenerPort(r)]
  if len(headerMap) > 0 {
    var s strings.Builder
    s.Grow(128)
    for header, values := range headerMap {
      for _, value := range values {
        w.Header().Add(header, value)
        fmt.Fprintf(&s, "[%s:%s] ", header, value)
      }
    }
    msg := s.String()
    fmt.Fprintln(w, msg)
    util.AddLogMessage("Response headers returned: "+msg, r)
  } else {
    fmt.Fprintln(w, "No response headers set")
    util.AddLogMessage("No response headers set", r)
  }
}

func setResponseHeaders(w http.ResponseWriter, r *http.Request) {
  headersLock.RLock()
  defer headersLock.RUnlock()
  util.CopyHeaders("Request", w, r.Header, r.Host, r.RequestURI)
  headerMap := responseHeadersByPort[util.GetListenerPort(r)]
  for header, values := range headerMap {
    for _, value := range values {
      w.Header().Add(header, value)
    }
  }
  w.Header().Add("Content-Type", "application/json")
}

func Middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    if !util.IsAdminRequest(r) {
      setResponseHeaders(w, r)
    }
    if next != nil {
      next.ServeHTTP(w, r)
    }
  })
}
