/**
 * Copyright 2024 uk
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package header

import (
  "fmt"
  "net/http"
  "strings"
  "sync"

  "goto/pkg/events"
  "goto/pkg/util"

  "github.com/gorilla/mux"
)

var (
  Handler               util.ServerHandler             = util.ServerHandler{"response.header", SetRoutes, Middleware}
  responseHeadersByPort map[string]map[string][]string = map[string]map[string][]string{}
  headersLock           sync.RWMutex
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  headersRouter := util.PathRouter(r, "/headers")
  util.AddRouteWithPort(headersRouter, "/add/{header}={value}", addResponseHeader, "PUT", "POST")
  util.AddRouteWithPort(headersRouter, "/remove/{header}", removeResponseHeader, "PUT", "POST")
  util.AddRouteWithPort(headersRouter, "/clear", clearResponseHeader, "PUT", "POST")
  util.AddRouteWithPort(headersRouter, "", getResponseHeaders, "GET")
}

func addResponseHeader(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if header, present := util.GetStringParam(r, "header"); present {
    value, _ := util.GetStringParam(r, "value")
    listenerPort := util.GetRequestOrListenerPort(r)
    headersLock.Lock()
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
    headersLock.Unlock()
    msg = fmt.Sprintf("Port [%s] Response header [%s : %s] added", listenerPort, header, value)
    events.SendRequestEvent("Response Header Added", msg, r)
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
    listenerPort := util.GetRequestOrListenerPort(r)
    if headerMap := responseHeadersByPort[listenerPort]; headerMap != nil {
      delete(headerMap, header)
    }
    headersLock.Unlock()
    msg = fmt.Sprintf("Port [%s] Response header [%s] removed", listenerPort, header)
    events.SendRequestEvent("Response Header Removed", msg, r)
  } else {
    msg = "Cannot remove. Invalid header"
    w.WriteHeader(http.StatusBadRequest)
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func clearResponseHeader(w http.ResponseWriter, r *http.Request) {
  listenerPort := util.GetRequestOrListenerPort(r)
  headersLock.Lock()
  responseHeadersByPort[listenerPort] = map[string][]string{}
  headersLock.Unlock()
  msg := fmt.Sprintf("Port [%s] Response header cleared", listenerPort)
  events.SendRequestEvent("Response Header Cleared", msg, r)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func getResponseHeaders(w http.ResponseWriter, r *http.Request) {
  headersLock.RLock()
  defer headersLock.RUnlock()
  listenerPort := util.GetRequestOrListenerPort(r)
  headerMap := responseHeadersByPort[util.GetRequestOrListenerPort(r)]
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
    util.AddLogMessage(fmt.Sprintf("Port [%s] Response headers returned: %s", listenerPort, msg), r)
  } else {
    msg := fmt.Sprintf("Port [%s] No response headers set", listenerPort)
    fmt.Fprintln(w, msg)
    util.AddLogMessage(msg, r)
  }
}

func setResponseHeaders(w http.ResponseWriter, r *http.Request) {
  headersLock.RLock()
  defer headersLock.RUnlock()
  util.CopyHeaders("Request", r, w, r.Header, true, true, false)
  headerMap := responseHeadersByPort[util.GetRequestOrListenerPort(r)]
  for header, values := range headerMap {
    for _, value := range values {
      w.Header().Add(header, value)
    }
  }
}

func Middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    if next != nil {
      next.ServeHTTP(w, r)
    }
    if !util.IsHeadersSent(r) && !util.IsAdminRequest(r) && !util.IsTunnelRequest(r) {
      setResponseHeaders(w, r)
    }
  })
}
