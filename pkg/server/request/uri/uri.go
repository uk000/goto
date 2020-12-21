package uri

import (
  "fmt"
  "net/http"
  "strconv"
  "strings"
  "sync"
  "time"

  "goto/pkg/server/intercept"
  "goto/pkg/server/request/uri/bypass"
  "goto/pkg/server/request/uri/ignore"
  "goto/pkg/util"

  "github.com/gorilla/mux"
)

type DelayConfig struct {
  delay time.Duration
  times int
}

var (
  Handler            util.ServerHandler = util.ServerHandler{"uri", SetRoutes, Middleware}
  internalHandler    util.ServerHandler = util.ServerHandler{Name: "uri", Middleware: middleware}
  uriCountsByPort    map[string]map[string]int
  uriStatusByPort    map[string]map[string][]int
  uriDelayByPort     map[string]map[string]*DelayConfig
  trackURICallCounts bool
  uriLock            sync.RWMutex
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  uriRouter := r.PathPrefix("/uri").Subrouter()
  bypass.SetRoutes(uriRouter, parent, root)
  ignore.SetRoutes(uriRouter, parent, root)
  util.AddRouteMultiQ(uriRouter, "/status/set", setStatus, "POST", "uri", "{uri}", "status", "{status}")
  util.AddRouteMultiQ(uriRouter, "/delay/set", setDelay, "POST", "uri", "{uri}", "delay", "{delay}")
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
    uriDelayByPort = map[string]map[string]*DelayConfig{}
  }
  if uriCountsByPort[port] == nil {
    uriCountsByPort[port] = map[string]int{}
    uriStatusByPort[port] = map[string][]int{}
    uriDelayByPort[port] = map[string]*DelayConfig{}
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

func setDelay(w http.ResponseWriter, r *http.Request) {
  port := initPort(r)
  msg := ""
  uriLock.Lock()
  defer uriLock.Unlock()
  if uri, present := util.GetStringParam(r, "uri"); present {
    uri = strings.ToLower(uri)
    vars := mux.Vars(r)
    delayParam := strings.Split(vars["delay"], ":")
    var delay time.Duration
    times := 0
    if len(delayParam[0]) > 0 {
      if d, err := time.ParseDuration(delayParam[0]); err == nil {
        delay = d
        if len(delayParam) > 1 {
          t, _ := strconv.ParseInt(delayParam[1], 10, 32)
          times = int(t)
        }
      }
    }
    if delay > 0 {
      uriDelayByPort[port][uri] = &DelayConfig{delay, times}
      if times > 0 {
        msg = fmt.Sprintf("Will delay next %d requests for URI %s by %s", times, uri, delay)
      } else {
        msg = fmt.Sprintf("Will delay all requests for URI %s by %s forever", uri, delay)
      }
    } else {
      delete(uriDelayByPort[port], uri)
      msg = fmt.Sprintf("URI %s delay cleared", uri)
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

func HasURIStatus(r *http.Request) bool {
  port := initPort(r)
  uri := strings.ToLower(r.URL.Path)
  uriLock.RLock()
  defer uriLock.RUnlock()
  return uriStatusByPort[port][uri] != nil && uriStatusByPort[port][uri][0] > 0 && uriStatusByPort[port][uri][1] >= 0
}

func middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    util.AddLogMessage(fmt.Sprintf("Request URI: [%s], Protocol: [%s], Method: [%s]", r.RequestURI, r.Proto, r.Method), r)
    if !util.IsAdminRequest(r) {
      track := false
      port := initPort(r)
      uri := strings.ToLower(r.URL.Path)
      statusToReport := 0
      hasURIStatus := HasURIStatus(r)
      var delay time.Duration = 0
      delayTimesLeft := 0
      statusTimesLeft := 0
      uriLock.RLock()
      track = trackURICallCounts
      if hasURIStatus {
        statusToReport = uriStatusByPort[port][uri][0]
        statusTimesLeft = uriStatusByPort[port][uri][1]
      }
      if uriDelayByPort[port][uri] != nil && uriDelayByPort[port][uri].delay > 0 {
        delay = uriDelayByPort[port][uri].delay
        delayTimesLeft = uriDelayByPort[port][uri].times
      }
      uriLock.RUnlock()
      if track {
        uriLock.Lock()
        uriCountsByPort[port][uri]++
        uriLock.Unlock()
      }
      if delay > 0 {
        uriLock.Lock()
        if uriDelayByPort[port][uri].times >= 1 {
          uriDelayByPort[port][uri].times--
          if uriDelayByPort[port][uri].times == 0 {
            delete(uriDelayByPort[port], uri)
          }
        }
        uriLock.Unlock()
        util.AddLogMessage(fmt.Sprintf("Delaying URI [%s] by [%s]", uri, delay), r)
        util.AddLogMessage(fmt.Sprintf("Remaining delay count = %d", delayTimesLeft-1), r)
        w.Header().Add("Response-Delay", delay.String())
        time.Sleep(delay)
      }
      if statusToReport > 0 {
        crw := intercept.NewInterceptResponseWriter(w, true)
        if next != nil {
          next.ServeHTTP(crw, r)
        }
        uriLock.Lock()
        if uriStatusByPort[port][uri][1] >= 1 {
          uriStatusByPort[port][uri][1]--
          if uriStatusByPort[port][uri][1] == 0 {
            delete(uriStatusByPort[port], uri)
          }
        }
        uriLock.Unlock()
        util.AddLogMessage(fmt.Sprintf("Reporting URI status: [%d] for URI [%s]", statusToReport, uri), r)
        util.AddLogMessage(fmt.Sprintf("Remaining status count = %d", statusTimesLeft-1), r)
        crw.StatusCode = statusToReport
        crw.Proceed()
      } else if next != nil {
        next.ServeHTTP(w, r)
      }
    } else if next != nil {
      next.ServeHTTP(w, r)
    }
  })
}

func Middleware(next http.Handler) http.Handler {
  return util.AddMiddlewares(next, internalHandler, bypass.Handler, ignore.Handler)
}
