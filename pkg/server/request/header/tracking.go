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

package header

import (
  "fmt"
  "net/http"
  "strings"
  "sync"

  "goto/pkg/events"
  "goto/pkg/metrics"
  "goto/pkg/server/intercept"
  "goto/pkg/util"

  "github.com/gorilla/mux"
)

var (
  Handler util.ServerHandler = util.ServerHandler{"header", SetRoutes, Middleware}
)

type HeaderData struct {
  RequestCountsByHeaderValue                   map[string]int         `json:requestCountsByHeaderValue`
  RequestCountsByHeaderValueAndRequestedStatus map[string]map[int]int `json:requestCountsByHeaderValueAndRequestedStatus`
  RequestCountsByHeaderValueAndResponseStatus  map[string]map[int]int `json:requestCountsByHeaderValueAndResponseStatus`
  lock                                         sync.RWMutex
}

type RequestTrackingData struct {
  headerMap map[string]*HeaderData
  lock      sync.RWMutex
}

type RequestTracking struct {
  requestTrackingByPort map[string]*RequestTrackingData
  lock                  sync.RWMutex
}

var (
  requestTracking RequestTracking = RequestTracking{requestTrackingByPort: map[string]*RequestTrackingData{}}
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  headerTrackingRouter := util.PathRouter(r, "/headers/track")
  util.AddRouteWithPort(headerTrackingRouter, "/clear", clearHeaders, "POST")
  util.AddRouteWithPort(headerTrackingRouter, "/add/{headers}", addHeaders, "PUT", "POST")
  util.AddRouteWithPort(headerTrackingRouter, "/{header}/remove", removeHeader, "PUT", "POST")
  util.AddRouteWithPort(headerTrackingRouter, "/{header}/counts", getHeaderCount, "GET")
  util.AddRouteWithPort(headerTrackingRouter, "/counts/clear/{headers}", clearHeaderCounts, "PUT", "POST")
  util.AddRouteWithPort(headerTrackingRouter, "/counts/clear", clearHeaderCounts, "POST")
  util.AddRouteWithPort(headerTrackingRouter, "/counts", getHeaderCount, "GET")
  util.AddRouteWithPort(headerTrackingRouter, "", getHeaders, "GET")
}

func (hd *HeaderData) init() {
  hd.lock.Lock()
  defer hd.lock.Unlock()
  hd.RequestCountsByHeaderValue = map[string]int{}
  hd.RequestCountsByHeaderValueAndRequestedStatus = map[string]map[int]int{}
  hd.RequestCountsByHeaderValueAndResponseStatus = map[string]map[int]int{}
}

func (hd *HeaderData) trackRequest(headerValue string, requestedStatus int) {
  if headerValue != "" {
    hd.lock.Lock()
    defer hd.lock.Unlock()
    hd.RequestCountsByHeaderValue[headerValue]++
    if requestedStatus > 0 {
      requestedStatusMap, present := hd.RequestCountsByHeaderValueAndRequestedStatus[headerValue]
      if !present {
        requestedStatusMap = map[int]int{}
        hd.RequestCountsByHeaderValueAndRequestedStatus[headerValue] = requestedStatusMap
      }
      requestedStatusMap[requestedStatus]++
    }
  }
}

func (hd *HeaderData) trackResponse(headerValue string, responseStatus int) {
  if headerValue != "" && responseStatus > 0 {
    hd.lock.Lock()
    defer hd.lock.Unlock()
    responseStatusMap, present := hd.RequestCountsByHeaderValueAndResponseStatus[headerValue]
    if !present {
      responseStatusMap = map[int]int{}
      hd.RequestCountsByHeaderValueAndResponseStatus[headerValue] = responseStatusMap
    }
    responseStatusMap[responseStatus]++
  }
}

func (rtd *RequestTrackingData) init() {
  rtd.lock.Lock()
  defer rtd.lock.Unlock()
  rtd.headerMap = map[string]*HeaderData{}
}

func (rtd *RequestTrackingData) initHeaders(headers []string) {
  rtd.lock.Lock()
  defer rtd.lock.Unlock()
  for _, h := range headers {
    _, present := rtd.headerMap[h]
    if !present {
      rtd.headerMap[h] = &HeaderData{}
      rtd.headerMap[h].init()
    }
  }
}

func (rtd *RequestTrackingData) removeHeader(header string) bool {
  rtd.lock.Lock()
  defer rtd.lock.Unlock()
  _, present := rtd.headerMap[header]
  if present {
    delete(rtd.headerMap, header)
  }
  return present
}

func (rtd *RequestTrackingData) clearCounts() {
  rtd.lock.Lock()
  defer rtd.lock.Unlock()
  for _, hd := range rtd.headerMap {
    hd.init()
  }
}

func (rtd *RequestTrackingData) getHeaderData(header string) *HeaderData {
  rtd.lock.RLock()
  defer rtd.lock.RUnlock()
  return rtd.headerMap[header]
}

func (rtd *RequestTrackingData) getHeaders() []string {
  rtd.lock.RLock()
  defer rtd.lock.RUnlock()
  headers := make([]string, len(rtd.headerMap))
  i := 0
  for h := range rtd.headerMap {
    headers[i] = h
    i++
  }
  return headers
}

func (rt *RequestTracking) getPortRequestTrackingData(r *http.Request) *RequestTrackingData {
  rt.lock.Lock()
  defer rt.lock.Unlock()
  listenerPort := util.GetRequestOrListenerPort(r)
  rtd, present := rt.requestTrackingByPort[listenerPort]
  if !present {
    rtd = &RequestTrackingData{}
    rtd.init()
    rt.requestTrackingByPort[listenerPort] = rtd
  }
  return rtd
}

func (rt *RequestTracking) initHeaders(r *http.Request) []string {
  if headers, present := util.GetListParam(r, "headers"); present {
    rtd := rt.getPortRequestTrackingData(r)
    rtd.initHeaders(headers)
    return headers
  }
  return nil
}

func (rt *RequestTracking) removeHeaders(r *http.Request) (headers []string, present bool) {
  if headers, present := util.GetListParam(r, "headers"); present {
    rtd := rt.getPortRequestTrackingData(r)
    for _, h := range headers {
      rtd.removeHeader(h)
    }
    return headers, present
  } else {
    return nil, false
  }
}

func (rt *RequestTracking) clear(r *http.Request) {
  rt.getPortRequestTrackingData(r).init()
}

func (rt *RequestTracking) clearHeaderCounts(r *http.Request) []string {
  rtd := rt.getPortRequestTrackingData(r)
  if headersP, hp := util.GetStringParam(r, "headers"); hp {
    headers := strings.Split(headersP, ",")
    rtd.initHeaders(headers)
    return headers
  }
  rtd.clearCounts()
  return nil
}

func (rt *RequestTracking) getHeaderCounts(r *http.Request) (header string, headerData *HeaderData) {
  rtd := rt.getPortRequestTrackingData(r)
  if header, hp := util.GetStringParam(r, "header"); hp {
    return header, rtd.getHeaderData(header)
  }
  return "", nil
}

func (rt *RequestTracking) getHeaders(r *http.Request) []string {
  return rt.getPortRequestTrackingData(r).getHeaders()
}

func addHeaders(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if headers := requestTracking.initHeaders(r); headers != nil {
    msg = fmt.Sprintf("Port [%s] will track headers %s", util.GetRequestOrListenerPort(r), headers)
    events.SendRequestEvent("Tracking Headers Added", msg, r)
    w.WriteHeader(http.StatusOK)
  } else {
    msg = "Cannot add. Invalid header"
    w.WriteHeader(http.StatusBadRequest)
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func removeHeader(w http.ResponseWriter, r *http.Request) {
  msg := ""
  headers, present := requestTracking.removeHeaders(r)
  if present {
    msg = fmt.Sprintf("Port [%s] tracking headers %s removed", util.GetRequestOrListenerPort(r), headers)
    events.SendRequestEvent("Tracking Headers Removed", msg, r)
    w.WriteHeader(http.StatusOK)
  } else {
    msg = "No headers to remove"
    w.WriteHeader(http.StatusBadRequest)
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func clearHeaders(w http.ResponseWriter, r *http.Request) {
  requestTracking.clear(r)
  msg := fmt.Sprintf("Port [%s] tracking headers cleared", util.GetRequestOrListenerPort(r))
  util.AddLogMessage(msg, r)
  events.SendRequestEvent("Tracking Headers Cleared", msg, r)
  w.WriteHeader(http.StatusOK)
  fmt.Fprintln(w, msg)
}

func clearHeaderCounts(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if headers := requestTracking.clearHeaderCounts(r); headers != nil {
    msg = fmt.Sprintf("Port [%s] tracking counts reset for headers %s", util.GetRequestOrListenerPort(r), headers)
  } else {
    msg = fmt.Sprintf("Port [%s] tracking counts reset for all headers", util.GetRequestOrListenerPort(r))
  }
  events.SendRequestEvent("Tracked Header Counts Cleared", msg, r)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func getHeaderCount(w http.ResponseWriter, r *http.Request) {
  header, headerData := requestTracking.getHeaderCounts(r)
  if header != "" {
    util.AddLogMessage(fmt.Sprintf("Port [%s] reporting counts for header %s", util.GetRequestOrListenerPort(r), header), r)
    if headerData != nil {
      result := util.ToJSONText(headerData)
      util.AddLogMessage(result, r)
      w.WriteHeader(http.StatusOK)
      fmt.Fprintln(w, result)
    } else {
      util.AddLogMessage(fmt.Sprintf("Port [%s] No header tracking data to report", util.GetRequestOrListenerPort(r)), r)
      w.WriteHeader(http.StatusOK)
      fmt.Fprintln(w, "{}")
    }
  } else {
    util.AddLogMessage(fmt.Sprintf("Port [%s] reporting counts for all headers", util.GetRequestOrListenerPort(r)), r)
    result := util.ToJSONText(requestTracking.getPortRequestTrackingData(r).headerMap)
    util.AddLogMessage(result, r)
    w.WriteHeader(http.StatusOK)
    fmt.Fprintln(w, result)
  }
}

func getHeaders(w http.ResponseWriter, r *http.Request) {
  fmt.Fprintln(w, util.ToJSONText(requestTracking.getHeaders(r)))
}

func track(port string, headers http.Header, uri string, requestedStatus, responseStatus int, rtd *RequestTrackingData) {
  for h, hd := range rtd.headerMap {
    if hv := headers.Get(h); hv != "" {
      metrics.UpdateHeaderRequestCount(port, uri, h, hv, fmt.Sprint(responseStatus))
      hd.trackRequest(hv, requestedStatus)
      hd.trackResponse(hv, responseStatus)
    }
  }
}

func Middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    if next != nil {
      next.ServeHTTP(w, r)
    }
    if !util.IsKnownNonTraffic(r) {
      rtd := requestTracking.getPortRequestTrackingData(r)
      requestedStatus := util.GetIntParamValue(r, "status")
      irw := util.GetInterceptResponseWriter(r).(*intercept.InterceptResponseWriter)
      go func(port string, headers http.Header, rtd *RequestTrackingData, requestedStatus, responseStatus int) {
        track(port, headers, r.RequestURI, requestedStatus, responseStatus, rtd)
      }(util.GetListenerPort(r), r.Header, rtd, requestedStatus, irw.StatusCode)
    }
  })
}
