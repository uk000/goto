/**
 * Copyright 2021 uk
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

package filter

import (
  "fmt"
  . "goto/pkg/constants"
  "goto/pkg/events"
  "goto/pkg/util"
  "net/http"
  "regexp"
  "strings"
  "sync"

  "github.com/gorilla/mux"
)

type RequestFilter struct {
  Uris           map[string]*regexp.Regexp            `json:"uris"`
  NotUris        map[string]*regexp.Regexp            `json:"notUris"`
  Headers        map[string]map[string]*regexp.Regexp `json:"headers"`
  UriUpdates     map[string]*regexp.Regexp            `json:"uriUpdates"`
  NotUriUpdates  map[string]*regexp.Regexp            `json:"notUriUpdates"`
  HeaderUpdates  map[string]map[string]*regexp.Regexp `json:"headerUpdates"`
  Status         int                                  `json:"status"`
  FilteredCount  int64                                `json:"filteredCount"`
  PendingUpdates bool                                 `json:"pendingUpdates"`
  filterType     string
  hasURIs        bool
  hasHeaders     bool
  lock           sync.RWMutex
}

var (
  Handler      = util.ServerHandler{"filter", SetRoutes, Middleware}
  ignoreFilter = newRequestFilter()
  bypassFilter = newRequestFilter()
  lock         sync.RWMutex
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  ignoreFilter.SetRoutes("ignore", r)
  bypassFilter.SetRoutes("bypass", r)
}

func (rf *RequestFilter) SetRoutes(filterType string, r *mux.Router) {
  rf.filterType = filterType
  filterRouter := util.PathRouter(r, "/"+filterType)
  util.AddRouteQWithPort(filterRouter, "/add", rf.addFilterHeaderOrURI, "uri", "{uri}", "PUT", "POST")
  util.AddRouteWithPort(filterRouter, "/add/header/{header}={value}", rf.addFilterHeaderOrURI, "PUT", "POST")
  util.AddRouteWithPort(filterRouter, "/add/header/{header}", rf.addFilterHeaderOrURI, "PUT", "POST")
  util.AddRouteWithPort(filterRouter, "/remove/header/{header}={value}", rf.removeIgnoreHeaderOrURI, "PUT", "POST")
  util.AddRouteWithPort(filterRouter, "/remove/header/{header}", rf.removeIgnoreHeaderOrURI, "PUT", "POST")
  util.AddRouteQWithPort(filterRouter, "/remove", rf.removeIgnoreHeaderOrURI, "uri", "{uri}", "PUT", "POST")
  util.AddRouteWithPort(filterRouter, "/set/status={status}", rf.setOrGetIgnoreStatus, "PUT", "POST")
  util.AddRouteWithPort(filterRouter, "/status", rf.setOrGetIgnoreStatus)
  util.AddRouteWithPort(filterRouter, "/clear", rf.clear, "PUT", "POST")
  util.AddRouteWithPort(filterRouter, "/count", rf.getFilteredRequestCount, "GET")
  util.AddRouteWithPort(filterRouter, "", rf.getRequestFilterConfig, "GET")
}

func newRequestFilter() *RequestFilter {
  rf := &RequestFilter{
    Uris:          map[string]*regexp.Regexp{},
    NotUris:       map[string]*regexp.Regexp{},
    Headers:       map[string]map[string]*regexp.Regexp{},
    UriUpdates:    map[string]*regexp.Regexp{},
    NotUriUpdates: map[string]*regexp.Regexp{},
    HeaderUpdates: map[string]map[string]*regexp.Regexp{},
    Status:        http.StatusOK,
  }
  return rf
}

func (rf *RequestFilter) ResetCount() {
  rf.lock.Lock()
  defer rf.lock.Unlock()
  rf.FilteredCount = 0
}

func (rf *RequestFilter) addFilterHeaderOrURI(w http.ResponseWriter, r *http.Request) {
  msg := ""
  uri := util.GetStringParamValue(r, "uri")
  header := util.GetStringParamValue(r, "header")
  value := util.GetStringParamValue(r, "value")
  if uri != "" {
    matchURI := strings.ToLower(uri)
    var match *regexp.Regexp
    negative := false
    if strings.HasPrefix(uri, "!") {
      matchURI = strings.TrimLeft(matchURI, "!")
      negative = true
    }
    if matchURI == "/" {
      match = regexp.MustCompile("^\\/?$")
    } else {
      match = regexp.MustCompile("(?i)^" + matchURI + "$")
    }
    rf.lock.Lock()
    if negative {
      rf.NotUriUpdates[uri] = match
    } else {
      rf.UriUpdates[uri] = match
    }
    rf.PendingUpdates = true
    rf.hasURIs = true
    rf.lock.Unlock()
    msg = fmt.Sprintf("Will %s URI [%s]", rf.filterType, uri)
    events.SendRequestEvent("Request Filter Added", msg, r)
  } else if header != "" {
    header = strings.ToLower(header)
    if value != "" {
      value = strings.ToLower(value)
    }
    rf.lock.Lock()
    if rf.HeaderUpdates[header] == nil {
      rf.HeaderUpdates[header] = map[string]*regexp.Regexp{}
    }
    rf.HeaderUpdates[header][value] = regexp.MustCompile("(?i)^" + value + "$")
    rf.PendingUpdates = true
    rf.hasHeaders = true
    rf.lock.Unlock()
    msg = fmt.Sprintf("Will %s header [%s : %s]", rf.filterType, header, value)
    events.SendRequestEvent("Request Filter Added", msg, r)
  } else {
    msg = "Cannot add request filter. No URI or Header"
    w.WriteHeader(http.StatusBadRequest)
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func (rf *RequestFilter) removeIgnoreHeaderOrURI(w http.ResponseWriter, r *http.Request) {
  msg := ""
  uri := util.GetStringParamValue(r, "uri")
  header := util.GetStringParamValue(r, "header")
  value := util.GetStringParamValue(r, "value")
  if uri != "" {
    uri = strings.ToLower(uri)
    rf.lock.Lock()
    delete(rf.UriUpdates, uri)
    delete(rf.NotUriUpdates, uri)
    rf.PendingUpdates = true
    rf.lock.Unlock()
    msg = fmt.Sprintf("Will not %s URI[%s]", rf.filterType, uri)
    events.SendRequestEvent("Request Filter Removed", msg, r)
  } else if header != "" {
    header = strings.ToLower(header)
    if value != "" {
      value = strings.ToLower(value)
    }
    rf.lock.Lock()
    if rf.HeaderUpdates[header] != nil {
      delete(rf.HeaderUpdates[header], value)
      if len(rf.HeaderUpdates[header]) == 0 {
        delete(rf.HeaderUpdates, header)
      }
    }
    rf.PendingUpdates = true
    rf.hasHeaders = len(rf.HeaderUpdates) > 0
    rf.lock.Unlock()
    msg = fmt.Sprintf("Will not %s Header [%s: %s]", rf.filterType, header, value)
    events.SendRequestEvent("Request Filter Removed", msg, r)
  } else {
    msg = "Cannot remove request filter. No URI or Header"
    w.WriteHeader(http.StatusBadRequest)
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func (rf *RequestFilter) setOrGetIgnoreStatus(w http.ResponseWriter, r *http.Request) {
  msg := ""
  statusCodes, _, present := util.GetStatusParam(r)
  if present {
    rf.Status = statusCodes[0]
    msg = fmt.Sprintf("Status for %s set to [%d]", rf.filterType, statusCodes[0])
    events.SendRequestEvent("Request Filter Status Configured", msg, r)
  } else {
    msg = fmt.Sprintf("Status for %s: %d", rf.filterType, rf.Status)
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func (rf *RequestFilter) clear(w http.ResponseWriter, r *http.Request) {
  rf.lock.Lock()
  rf.UriUpdates = map[string]*regexp.Regexp{}
  rf.NotUriUpdates = map[string]*regexp.Regexp{}
  rf.HeaderUpdates = map[string]map[string]*regexp.Regexp{}
  rf.FilteredCount = 0
  rf.PendingUpdates = true
  rf.lock.Unlock()
  msg := fmt.Sprintf("Request filter %s Cleared", rf.filterType)
  w.WriteHeader(http.StatusOK)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
  events.SendRequestEvent("Request Filters Cleared", msg, r)
}

func (rf *RequestFilter) getFilteredRequestCount(w http.ResponseWriter, r *http.Request) {
  msg := fmt.Sprintf("Request filter %s count: %d", rf.FilteredCount)
  util.WriteJsonPayload(w, map[string]interface{}{"ignoredRequests": rf.FilteredCount})
  util.AddLogMessage(msg, r)
}

func (rf *RequestFilter) getRequestFilterConfig(w http.ResponseWriter, r *http.Request) {
  util.AddLogMessage("Reporting Request Filter Config", r)
  rf.lock.RLock()
  defer rf.lock.RUnlock()
  util.WriteJsonPayload(w, rf)
}

func cloneUris(orig map[string]*regexp.Regexp) map[string]*regexp.Regexp {
  copy := map[string]*regexp.Regexp{}
  for k, v := range orig {
    copy[k] = v
  }
  return copy
}

func cloneHeaders(orig map[string]map[string]*regexp.Regexp) map[string]map[string]*regexp.Regexp {
  copy := map[string]map[string]*regexp.Regexp{}
  for h, vMap := range orig {
    copy[h] = map[string]*regexp.Regexp{}
    for k, v := range vMap {
      copy[h][k] = v
    }
  }
  return copy
}

func (rf *RequestFilter) applyUpdates() {
  if rf.PendingUpdates {
    rf.lock.Lock()
    rf.Uris = cloneUris(rf.UriUpdates)
    rf.NotUris = cloneUris(rf.NotUriUpdates)
    rf.Headers = cloneHeaders(rf.HeaderUpdates)
    rf.PendingUpdates = false
    rf.hasURIs = len(rf.Uris) > 0 || len(rf.NotUris) > 0
    rf.lock.Unlock()
  }
}

func (rf *RequestFilter) matchURI(r *http.Request) bool {
  if !rf.hasURIs {
    return false
  }
  filters := []*regexp.Regexp{}
  negativeFilters := []*regexp.Regexp{}
  rf.applyUpdates()
  rf.lock.RLock()
  for _, re := range rf.Uris {
    filters = append(filters, re)
  }
  for _, re := range rf.NotUris {
    negativeFilters = append(negativeFilters, re)
  }
  rf.lock.RUnlock()
  if len(filters) == 0 && len(negativeFilters) == 0 {
    return false
  }
  for _, re := range filters {
    if re.MatchString(r.RequestURI) {
      rf.FilteredCount++
      return true
    }
  }
  negativeMatch := len(negativeFilters) > 0
  for _, re := range negativeFilters {
    if re.MatchString(r.RequestURI) {
      negativeMatch = false
      break
    }
  }
  if negativeMatch {
    rf.FilteredCount++
    return true
  }
  return false
}

func (rf *RequestFilter) matchHeaders(r *http.Request) bool {
  if !rf.hasHeaders {
    return false
  }
  rf.applyUpdates()
  rf.lock.RLock()
  headers := rf.Headers
  rf.lock.RUnlock()
  if len(headers) == 0 {
    return false
  }
  for h, values := range r.Header {
    h = strings.ToLower(h)
    hvIgnoreMap := headers[h]
    if hvIgnoreMap == nil {
      continue
    }
    if hvIgnoreMap[""] != nil {
      rf.FilteredCount++
      return true
    }
    for _, re := range hvIgnoreMap {
      for _, v := range values {
        if re.MatchString(v) {
          rf.FilteredCount++
          return true
        }
      }
    }
  }
  return false
}

func filterRequest(w http.ResponseWriter, r *http.Request) bool {
  statusCode := 0
  if ignoreFilter.matchURI(r) {
    statusCode = ignoreFilter.Status
  } else if bypassFilter.matchURI(r) {
    statusCode = bypassFilter.Status
  } else if ignoreFilter.matchHeaders(r) {
    statusCode = ignoreFilter.Status
  } else if bypassFilter.matchHeaders(r) {
    statusCode = bypassFilter.Status
  }
  if statusCode > 0 {
    w.Header().Add(HeaderGotoFilteredRequest, "true")
    w.WriteHeader(statusCode)
    util.SetFiltreredRequest(r)
  }
  return statusCode > 0
}

func Middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    if util.IsKnownNonTraffic(r) || !filterRequest(w, r) {
      if next != nil {
        next.ServeHTTP(w, r)
      }
    }
  })
}
