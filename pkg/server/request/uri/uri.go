package uri

import (
  "fmt"
  "net/http"
  "strconv"
  "strings"
  "sync"
  "time"

  "goto/pkg/events"
  "goto/pkg/server/intercept"
  "goto/pkg/server/request/uri/bypass"
  "goto/pkg/server/request/uri/ignore"
  "goto/pkg/server/response/trigger"
  "goto/pkg/util"

  "github.com/gorilla/mux"
)

type DelayConfig struct {
  URI   string
  Glob  bool
  Delay time.Duration
  Times int
}

type URIStatusConfig struct {
  URI    string
  Glob   bool
  Status int
  Times  int
}

var (
  Handler            util.ServerHandler = util.ServerHandler{"uri", SetRoutes, Middleware}
  internalHandler    util.ServerHandler = util.ServerHandler{Name: "uri", Middleware: middleware}
  uriCountsByPort    map[string]map[string]int
  uriStatusByPort    map[string]map[string]interface{}
  uriDelayByPort     map[string]map[string]interface{}
  trackURICallCounts bool
  uriLock            sync.RWMutex
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  uriRouter := util.PathRouter(r, "/uri")
  bypass.SetRoutes(uriRouter, parent, root)
  ignore.SetRoutes(uriRouter, parent, root)
  util.AddRouteQWithPort(uriRouter, "/set/status={status}", setStatus, "uri", "{uri}", "POST")
  util.AddRouteQWithPort(uriRouter, "/set/delay={delay}", setDelay, "uri", "{uri}", "POST")
  util.AddRouteWithPort(uriRouter, "/counts/enable", enableURICallCounts, "POST")
  util.AddRouteWithPort(uriRouter, "/counts/disable", disableURICallCounts, "POST")
  util.AddRouteWithPort(uriRouter, "/counts", getURICallCounts, "GET")
  util.AddRouteWithPort(uriRouter, "/counts/clear", clearURICallCounts, "POST")
  util.AddRouteWithPort(uriRouter, "", getURIConfigs, "GET")
}

func initPort(r *http.Request) string {
  port := util.GetRequestOrListenerPort(r)
  uriLock.Lock()
  defer uriLock.Unlock()
  if uriCountsByPort == nil {
    uriCountsByPort = map[string]map[string]int{}
    uriStatusByPort = map[string]map[string]interface{}{}
    uriDelayByPort = map[string]map[string]interface{}{}
  }
  if uriCountsByPort[port] == nil {
    uriCountsByPort[port] = map[string]int{}
    uriStatusByPort[port] = map[string]interface{}{}
    uriDelayByPort[port] = map[string]interface{}{}
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
    glob := false
    matchURI := uri
    if strings.HasSuffix(uri, "*") {
      matchURI = strings.ReplaceAll(uri, "*", "")
      glob = true
    }
    if statusCode, times, statusCodePresent := util.GetStatusParam(r); statusCodePresent && statusCode > 0 {
      uriStatusByPort[port][matchURI] = &URIStatusConfig{URI: matchURI, Glob: glob, Status: statusCode, Times: times}
      if times > 0 {
        msg = fmt.Sprintf("Port [%s] URI [%s] status set to [%d] for next [%d] calls", port, uri, statusCode, times)
        events.SendRequestEvent("URI Status Configured", msg, r)
      } else {
        msg = fmt.Sprintf("Port [%s] URI [%s] status set to [%d] forever", port, uri, statusCode)
        events.SendRequestEvent("URI Status Configured", msg, r)
      }
    } else {
      delete(uriStatusByPort[port], matchURI)
      msg = fmt.Sprintf("Port [%s] URI [%s] status cleared", port, uri)
      events.SendRequestEvent("URI Status Cleared", msg, r)
    }
    w.WriteHeader(http.StatusOK)
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
    glob := false
    matchURI := uri
    if strings.HasSuffix(uri, "*") {
      matchURI = strings.ReplaceAll(uri, "*", "")
      glob = true
    }
    if delay > 0 {
      uriDelayByPort[port][matchURI] = &DelayConfig{URI: matchURI, Glob: glob, Delay: delay, Times: times}
      if times > 0 {
        msg = fmt.Sprintf("Port [%s] will delay next [%d] requests for URI [%s] by [%s]", port, times, uri, delay)
        events.SendRequestEvent("URI Delay Configured", msg, r)
      } else {
        msg = fmt.Sprintf("Port [%s] will delay all requests for URI [%s] by [%s] forever", port, uri, delay)
        events.SendRequestEvent("URI Delay Configured", msg, r)
      }
    } else {
      delete(uriDelayByPort[port], matchURI)
      msg = fmt.Sprintf("Port [%s] URI [%s] delay cleared", port, uri)
      events.SendRequestEvent("URI Delay Cleared", msg, r)
    }
    w.WriteHeader(http.StatusOK)
  } else {
    msg = "Cannot add. Invalid URI"
    w.WriteHeader(http.StatusBadRequest)
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func getURIConfigs(w http.ResponseWriter, r *http.Request) {
  port := initPort(r)
  uriLock.RLock()
  defer uriLock.RUnlock()
  result := util.ToJSON(map[string]interface{}{
    "uriDelayByPort":  uriDelayByPort[port],
    "uriStatusByPort": uriStatusByPort[port],
    "uriCountsByPort": uriCountsByPort[port],
  })
  util.AddLogMessage(fmt.Sprintf("Port [%s] Reporting URI Configs: %s", port, result), r)
  fmt.Fprintf(w, "%s\n", result)
}

func getURICallCounts(w http.ResponseWriter, r *http.Request) {
  port := initPort(r)
  uriLock.RLock()
  defer uriLock.RUnlock()
  result := util.ToJSON(uriCountsByPort[port])
  util.AddLogMessage(fmt.Sprintf("Port [%s] Reporting URI Call Counts: %s", port, result), r)
  fmt.Fprintf(w, "%s\n", result)
}

func clearURICallCounts(w http.ResponseWriter, r *http.Request) {
  port := initPort(r)
  uriLock.Lock()
  defer uriLock.Unlock()
  uriCountsByPort[port] = map[string]int{}
  msg := fmt.Sprintf("Port [%s] URI Call Counts Cleared", port)
  fmt.Fprintln(w, msg)
  util.AddLogMessage(msg, r)
  events.SendRequestEvent("URI Call Counts Cleared", msg, r)
}

func enableURICallCounts(w http.ResponseWriter, r *http.Request) {
  uriLock.Lock()
  trackURICallCounts = true
  uriLock.Unlock()
  msg := fmt.Sprintf("Port [%s] URI Call Counts Enabled", util.GetRequestOrListenerPort(r))
  fmt.Fprintln(w, msg)
  util.AddLogMessage(msg, r)
  events.SendRequestEvent("URI Call Counts Enabled", msg, r)
}

func disableURICallCounts(w http.ResponseWriter, r *http.Request) {
  uriLock.Lock()
  trackURICallCounts = false
  uriLock.Unlock()
  msg := fmt.Sprintf("Port [%s] URI Call Counts Disabled", util.GetRequestOrListenerPort(r))
  fmt.Fprintln(w, msg)
  util.AddLogMessage(msg, r)
  events.SendRequestEvent("URI Call Counts Disabled", msg, r)
}

func hasURIConfig(r *http.Request, uriMap map[string]map[string]interface{}) (bool, bool, interface{}) {
  port := initPort(r)
  uri := strings.ToLower(r.URL.Path)
  uriLock.RLock()
  defer uriLock.RUnlock()
  portURIMap := uriMap[port]
  if portURIMap == nil {
    return false, false, nil
  }
  if uriConfig, present := portURIMap[uri]; present {
    return true, false, uriConfig
  }
  var uriConfig interface{}
  for k, v := range portURIMap {
    if strings.HasPrefix(uri, k) {
      uriConfig = v
      break
    }
  }
  if uriConfig != nil {
    return true, true, uriConfig
  }
  return false, false, nil
}

func HasURIStatus(r *http.Request) bool {
  if present, glob, v := hasURIConfig(r, uriStatusByPort); present {
    uriStatus := v.(*URIStatusConfig)
    return uriStatus.Status > 0 && uriStatus.Times >= 0 && (!glob || uriStatus.Glob)
  }
  return false
}

func GetURIStatus(r *http.Request) *URIStatusConfig {
  if present, glob, v := hasURIConfig(r, uriStatusByPort); present {
    uriStatus := v.(*URIStatusConfig)
    if uriStatus.Status > 0 && uriStatus.Times >= 0 && (!glob || uriStatus.Glob) {
      return uriStatus
    }
  }
  return nil
}

func GetURIDelay(r *http.Request) *DelayConfig {
  if present, glob, v := hasURIConfig(r, uriDelayByPort); present {
    delayConfig := v.(*DelayConfig)
    if delayConfig.Delay > 0 && delayConfig.Times >= 0 && (!glob || delayConfig.Glob) {
      return delayConfig
    }
  }
  return nil
}

func middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    util.AddLogMessage(fmt.Sprintf("Request URI: [%s], Protocol: [%s], Method: [%s]", r.RequestURI, r.Proto, r.Method), r)
    if util.IsAdminRequest(r) || util.IsMetricsRequest(r) {
      if next != nil {
        next.ServeHTTP(w, r)
      }
      return
    }
    port := initPort(r)
    uri := strings.ToLower(r.URL.Path)
    statusToReport := 0
    uriStatus := GetURIStatus(r)
    uriDelay := GetURIDelay(r)
    var delay time.Duration = 0
    delayTimesLeft := 0
    statusTimesLeft := 0
    uriLock.RLock()
    if uriStatus != nil {
      statusToReport = uriStatus.Status
      statusTimesLeft = uriStatus.Times
    }
    if uriDelay != nil {
      delay = uriDelay.Delay
      delayTimesLeft = uriDelay.Times
    }
    uriLock.RUnlock()
    if trackURICallCounts {
      uriLock.Lock()
      uriCountsByPort[port][uri]++
      uriLock.Unlock()
    }
    if delay > 0 {
      uriLock.Lock()
      if uriDelay.Times >= 1 {
        uriDelay.Times--
        if uriDelay.Times == 0 {
          delete(uriDelayByPort[port], uriDelay.URI)
        }
      }
      uriLock.Unlock()
      msg := fmt.Sprintf("Delaying URI [%s] by [%s]. Remaining delay count [%d]", uri, delay, delayTimesLeft-1)
      util.AddLogMessage(msg, r)
      events.SendRequestEvent("URI Delay Applied", msg, r)
      w.Header().Add("Goto-Response-Delay", delay.String())
      time.Sleep(delay)
    }
    crw := intercept.NewInterceptResponseWriter(w, true)
    if next != nil {
      next.ServeHTTP(crw, r)
    }
    if statusToReport > 0 {
      uriLock.Lock()
      if uriStatus.Times >= 1 {
        uriStatus.Times--
        if uriStatus.Times == 0 {
          delete(uriStatusByPort[port], uriStatus.URI)
        }
      }
      uriLock.Unlock()
      msg := ""
      if statusTimesLeft-1 > 0 {
        msg = fmt.Sprintf("Reporting URI status: [%d] for URI [%s]. Remaining status count [%d].", statusToReport, uri, statusTimesLeft-1)
      } else {
        msg = fmt.Sprintf("Reporting URI status: [%d] for URI [%s].", statusToReport, uri)
      }
      util.AddLogMessage(msg, r)
      events.SendRequestEvent("URI Status Applied", msg, r)
      crw.StatusCode = statusToReport
    }
    if crw.StatusCode == 0 {
      crw.StatusCode = 200
    }
    w.Header().Add("Goto-Response-Status", strconv.Itoa(crw.StatusCode))
    crw.Proceed()
    util.UpdateTrafficEventStatusCode(r, crw.StatusCode)
    trigger.RunTriggers(r, crw, crw.StatusCode)
  })
}

func Middleware(next http.Handler) http.Handler {
  return util.AddMiddlewares(next, internalHandler, bypass.Handler, ignore.Handler)
}
