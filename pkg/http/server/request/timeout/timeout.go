package timeout

import (
	"fmt"
	"net/http"
	"strings"
	"sync"

	"goto/pkg/util"

	"github.com/gorilla/mux"
)

type TimeoutData struct {
  ConnectionClosed int `json:"connectionClosed"`
  RequestCompleted int `json:"requestCompleted"`
}

type TimeoutTracking struct {
  headersMap  map[string]map[string]*TimeoutData
  allTimeouts *TimeoutData
  lock        sync.RWMutex
}

var (
  Handler               util.ServerHandler          = util.ServerHandler{"timeout", SetRoutes, Middleware}
  timeoutTrackingByPort map[string]*TimeoutTracking = map[string]*TimeoutTracking{}
  timeoutTrackingLock   sync.RWMutex
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  timeoutRouter := r.PathPrefix("/timeout").Subrouter()
  util.AddRoute(timeoutRouter, "/track/headers/{headers}", trackHeaders, "PUT", "POST")
  util.AddRoute(timeoutRouter, "/track/all", trackAll, "PUT", "POST")
  util.AddRoute(timeoutRouter, "/track/clear", clearTimeoutTracking, "POST")
  util.AddRoute(timeoutRouter, "/status", reportTimeoutTracking, "GET")
}

func (tt *TimeoutTracking) init() {
  tt.lock.Lock()
  defer tt.lock.Unlock()
  if tt.headersMap == nil {
    tt.headersMap = map[string]map[string]*TimeoutData{}
  }
}

func (tt *TimeoutTracking) addHeaders(w http.ResponseWriter, r *http.Request) {
  tt.lock.Lock()
  defer tt.lock.Unlock()
  msg := ""
  if param, present := util.GetStringParam(r, "headers"); present {
    headers := strings.Split(param, ",")
    for _, h := range headers {
      tt.headersMap[h] = map[string]*TimeoutData{}
    }
    msg = fmt.Sprintf("Will track request timeout for Headers %s", headers)
    w.WriteHeader(http.StatusAccepted)
  } else {
    msg = "Cannot track. Invalid header"
    w.WriteHeader(http.StatusBadRequest)
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func (tt *TimeoutTracking) trackAll(w http.ResponseWriter, r *http.Request) {
  tt.lock.Lock()
  defer tt.lock.Unlock()
  tt.allTimeouts = &TimeoutData{}
  w.WriteHeader(http.StatusAccepted)
  util.AddLogMessage("Activated timeout tracking for all requests", r)
  fmt.Fprintln(w, "Activated timeout tracking for all requests")
}

func (tt *TimeoutTracking) clear(w http.ResponseWriter, r *http.Request) {
  tt.lock.Lock()
  defer tt.lock.Unlock()
  tt.headersMap = map[string]map[string]*TimeoutData{}
  tt.allTimeouts = nil
  w.WriteHeader(http.StatusAccepted)
  util.AddLogMessage("Cleared timeout tracking headers", r)
  fmt.Fprintln(w, "Cleared timeout tracking headers")
}

func getTimeoutTracking(r *http.Request) *TimeoutTracking {
  listenerPort := util.GetListenerPort(r)
  timeoutTrackingLock.Lock()
  defer timeoutTrackingLock.Unlock()
  tt, present := timeoutTrackingByPort[listenerPort]
  if !present {
    tt = &TimeoutTracking{}
    tt.init()
    timeoutTrackingByPort[listenerPort] = tt
  }
  return tt
}

func trackHeaders(w http.ResponseWriter, r *http.Request) {
  tt := getTimeoutTracking(r)
  tt.addHeaders(w, r)
}

func trackAll(w http.ResponseWriter, r *http.Request) {
  tt := getTimeoutTracking(r)
  tt.trackAll(w, r)
}

func clearTimeoutTracking(w http.ResponseWriter, r *http.Request) {
  timeoutTrackingLock.Lock()
  defer timeoutTrackingLock.Unlock()
  listenerPort := util.GetListenerPort(r)
  if tt := timeoutTrackingByPort[listenerPort]; tt != nil {
    timeoutTrackingByPort[listenerPort] = &TimeoutTracking{}
    timeoutTrackingByPort[listenerPort].init()
  }
  w.WriteHeader(http.StatusAccepted)
  util.AddLogMessage("Cleared timeout tracking headers", r)
  fmt.Fprintln(w, "Cleared timeout tracking headers")
}

func reportTimeoutTracking(w http.ResponseWriter, r *http.Request) {
  util.AddLogMessage("Reporting timeout tracking counts", r)
  tt := getTimeoutTracking(r)
  timeoutTrackingLock.RLock()
  defer timeoutTrackingLock.RUnlock()
  result := map[string]interface{}{}
  result["headers"] = tt.headersMap
  result["all"] = tt.allTimeouts
  output := util.ToJSON(result)
  util.AddLogMessage(output, r)
  w.WriteHeader(http.StatusOK)
  fmt.Fprintln(w, output)
}

func Middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    if util.IsAdminRequest(r) {
      next.ServeHTTP(w, r)
      return
    }
    trackedHeaders := [][]string{}
    tt := getTimeoutTracking(r)
    timeoutTrackingLock.RLock()
    for header, valueMap := range tt.headersMap {
      headerValue := r.Header.Get(header)
      if len(headerValue) > 0 {
        timeoutData, present := valueMap[headerValue]
        if !present {
          timeoutData = &TimeoutData{}
          timeoutTrackingLock.RUnlock()
          timeoutTrackingLock.Lock()
          valueMap[headerValue] = timeoutData
          timeoutTrackingLock.Unlock()
          timeoutTrackingLock.RLock()
        }
        trackedHeaders = append(trackedHeaders, []string{header, headerValue})
      }
    }
    if len(trackedHeaders) > 0 || tt.allTimeouts != nil {
      notify := w.(http.CloseNotifier).CloseNotify()
      go func(trackedHeaders [][]string) {
        connectionClosed := 0
        requestCompleted := 0
        select {
        case <-notify:
          connectionClosed++
        case <-r.Context().Done():
          requestCompleted++
        }
        timeoutTrackingLock.Lock()
        for _, kv := range trackedHeaders {
          tt.headersMap[kv[0]][kv[1]].ConnectionClosed += connectionClosed
          tt.headersMap[kv[0]][kv[1]].RequestCompleted += requestCompleted
        }
        if tt.allTimeouts != nil {
          tt.allTimeouts.ConnectionClosed += connectionClosed
          tt.allTimeouts.RequestCompleted += requestCompleted
        }
        timeoutTrackingLock.Unlock()
      }(trackedHeaders)
    }
    timeoutTrackingLock.RUnlock()
    next.ServeHTTP(w, r)
  })
}
