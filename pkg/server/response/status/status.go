package status

import (
  "fmt"
  "net/http"
  "strconv"
  "sync"
  "time"

  "goto/pkg/events"
  "goto/pkg/metrics"
  "goto/pkg/server/intercept"
  "goto/pkg/server/request/uri"
  "goto/pkg/util"

  "github.com/gorilla/mux"
)

type FlipFlopConfig struct {
  times           int
  statusCount     int
  lastStatusIndex int
}

type PortStatus struct {
  alwaysReportStatus      int
  alwaysReportStatusCount int
  countsByRequestedStatus map[int]int
  countsByResponseStatus  map[int]int
  flipflopConfigs         map[string]*FlipFlopConfig
  lock                    sync.RWMutex
}

var (
  Handler       util.ServerHandler     = util.ServerHandler{"status", SetRoutes, Middleware}
  portStatusMap map[string]*PortStatus = map[string]*PortStatus{}
  statusLock    sync.RWMutex
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  statusRouter := util.PathRouter(r, "/status")
  util.AddRouteWithPort(statusRouter, "/set/{status}", setStatus, "PUT", "POST")
  util.AddRouteWithPort(statusRouter, "/counts/clear", clearStatusCounts, "PUT", "POST")
  util.AddRouteWithPort(statusRouter, "/counts/{status}", getStatusCount, "GET")
  util.AddRouteWithPort(statusRouter, "/counts", getStatusCount, "GET")
  util.AddRouteWithPort(statusRouter, "/clear", setStatus, "PUT", "POST")
  util.AddRouteWithPort(statusRouter, "", getStatus, "GET")
  util.AddRoute(root, "/status/clear", setStatus, "PUT", "POST")
  util.AddRoute(root, "/status/{status}", getStatus, "GET", "PUT", "POST", "OPTIONS", "HEAD", "DELETE")
  util.AddRouteQ(root, "/status={status}", getStatus, "x-request-id", "{requestId}", "GET", "PUT", "POST", "OPTIONS", "HEAD", "DELETE")
  util.AddRoute(root, "/status={status}", getStatus, "GET", "PUT", "POST", "OPTIONS", "HEAD", "DELETE")
  util.AddRouteQ(root, "/status={status}/delay={delay}", getStatus, "x-request-id", "{requestId}", "GET", "PUT", "POST", "OPTIONS", "HEAD", "DELETE")
  util.AddRoute(root, "/status={status}/delay={delay}", getStatus, "GET", "PUT", "POST", "OPTIONS", "HEAD", "DELETE")
  util.AddRouteQ(root, "/status={status}/flipflop", flipflop, "x-request-id", "{requestId}", "GET", "PUT", "POST", "OPTIONS", "HEAD", "DELETE")
  util.AddRoute(root, "/status={status}/flipflop", flipflop, "GET", "PUT", "POST", "OPTIONS", "HEAD", "DELETE")
  util.AddRouteQ(root, "/status={status}/delay={delay}/flipflop", flipflop, "x-request-id", "{requestId}", "GET", "PUT", "POST", "OPTIONS", "HEAD", "DELETE")
  util.AddRoute(root, "/status={status}/delay={delay}/flipflop", flipflop, "GET", "PUT", "POST", "OPTIONS", "HEAD", "DELETE")
}

func getOrCreatePortStatus(r *http.Request) *PortStatus {
  listenerPort := util.GetRequestOrListenerPort(r)
  statusLock.Lock()
  defer statusLock.Unlock()
  portStatus := portStatusMap[listenerPort]
  if portStatus == nil {
    portStatus = &PortStatus{
      countsByRequestedStatus: map[int]int{},
      countsByResponseStatus:  map[int]int{},
      flipflopConfigs:         map[string]*FlipFlopConfig{},
    }
    portStatusMap[listenerPort] = portStatus

  }
  return portStatus
}

func setStatus(w http.ResponseWriter, r *http.Request) {
  statusCode, times, _ := util.GetStatusParam(r)
  portStatus := getOrCreatePortStatus(r)
  portStatus.lock.Lock()
  portStatus.alwaysReportStatusCount = -1
  portStatus.alwaysReportStatus = 200
  if statusCode > 0 {
    portStatus.alwaysReportStatus = statusCode
    portStatus.alwaysReportStatusCount = 0
    if times > 1 {
      portStatus.alwaysReportStatusCount = times
    }
  } else {
    portStatus.flipflopConfigs = map[string]*FlipFlopConfig{}
  }
  portStatus.lock.Unlock()
  msg := ""
  port := util.GetRequestOrListenerPort(r)
  if portStatus.alwaysReportStatusCount > 0 {
    msg = fmt.Sprintf("Port [%s] will respond with forced status: %d times %d",
      port, portStatus.alwaysReportStatus, portStatus.alwaysReportStatusCount)
    events.SendRequestEvent("Response Status Configured", msg, r)
  } else if portStatus.alwaysReportStatusCount == 0 {
    msg = fmt.Sprintf("Port [%s] will respond with forced status: %d forever",
      port, portStatus.alwaysReportStatus)
    events.SendRequestEvent("Response Status Configured", msg, r)
  } else {
    msg = fmt.Sprintf("Port [%s] will respond normally", port)
    events.SendRequestEvent("Response Status Cleared", msg, r)
  }
  util.AddLogMessage(msg, r)
  w.WriteHeader(http.StatusOK)
  fmt.Fprintln(w, msg)
}

func IsForcedStatus(r *http.Request) bool {
  portStatus := getOrCreatePortStatus(r)
  portStatus.lock.RLock()
  defer portStatus.lock.RUnlock()
  return portStatus.alwaysReportStatus > 0 && portStatus.alwaysReportStatusCount >= 0
}

func computeResponseStatus(originalStatus int, r *http.Request) (int, bool) {
  portStatus := getOrCreatePortStatus(r)
  portStatus.lock.Lock()
  defer portStatus.lock.Unlock()
  overriddenStatus := false
  responseStatus := originalStatus
  if portStatus.alwaysReportStatus > 0 && portStatus.alwaysReportStatusCount >= 0 {
    responseStatus = portStatus.alwaysReportStatus
    overriddenStatus = true
    if portStatus.alwaysReportStatusCount > 1 {
      portStatus.alwaysReportStatusCount--
    } else if portStatus.alwaysReportStatusCount == 1 {
      portStatus.alwaysReportStatusCount = -1
    }
  }
  return responseStatus, overriddenStatus
}

func getStatus(w http.ResponseWriter, r *http.Request) {
  portStatus := getOrCreatePortStatus(r)
  requestedStatus, times, _ := util.GetStatusParam(r)
  if requestedStatus <= 0 {
    requestedStatus = 200
  }
  if times > 0 {
    portStatus.doFlipflop(w, r, false)
    return
  }
  if !util.IsAdminRequest(r) {
    delay := util.GetDurationParam(r, "delay")
    delayText := ""
    if delay <= 0 {
      delay = 0
    } else {
      delayText = delay.String()
      time.Sleep(delay)
      w.Header().Add("Goto-Response-Delay", delayText)
    }
    metrics.UpdateRequestCount("status")
    portStatus.lock.Lock()
    portStatus.countsByRequestedStatus[requestedStatus]++
    portStatus.lock.Unlock()
    util.AddLogMessage(fmt.Sprintf("Requested status [%d] with delay [%s]", requestedStatus, delayText), r)
    w.Header().Add("Goto-Requested-Status", strconv.Itoa(requestedStatus))
    if !IsForcedStatus(r) {
      w.WriteHeader(requestedStatus)
    }
  } else {
    msg := ""
    port := util.GetRequestOrListenerPort(r)
    if portStatus.alwaysReportStatusCount > 0 {
      msg = fmt.Sprintf("Port [%s] responding with forced status: %d times %d",
        port, portStatus.alwaysReportStatus, portStatus.alwaysReportStatusCount)
    } else if portStatus.alwaysReportStatusCount == 0 {
      msg = fmt.Sprintf("Port [%s] responding with forced status: %d forever",
        port, portStatus.alwaysReportStatus)
    } else {
      msg = fmt.Sprintf("Port [%s] responding normally", port)
    }
    w.WriteHeader(http.StatusOK)
    fmt.Fprintln(w, msg)
  }
}

func flipflop(w http.ResponseWriter, r *http.Request) {
  getOrCreatePortStatus(r).doFlipflop(w, r, true)
}

func (portStatus *PortStatus) doFlipflop(w http.ResponseWriter, r *http.Request, restart bool) {
  requestedStatuses, times, _ := util.GetStatusRangeParam(r)
  if times <= 0 {
    times = len(requestedStatuses)
  }
  requestId := util.GetStringParamValue(r, "requestId")
  delay := util.GetDurationParam(r, "delay")
  delayText := ""
  if delay <= 0 {
    delay = 0
  } else {
    delayText = delay.String()
    time.Sleep(delay)
    w.Header().Add("Goto-Response-Delay", delayText)
  }
  requestedStatus := 200
  portStatus.lock.Lock()
  flipflopConfig := portStatus.flipflopConfigs[requestId]
  if flipflopConfig == nil {
    flipflopConfig = &FlipFlopConfig{
      times:           times,
      statusCount:     times,
      lastStatusIndex: 0,
    }
    portStatus.flipflopConfigs[requestId] = flipflopConfig
  }
  if len(requestedStatuses) > 1 {
    if len(requestedStatuses) > flipflopConfig.lastStatusIndex {
      requestedStatus = requestedStatuses[flipflopConfig.lastStatusIndex]
      flipflopConfig.lastStatusIndex++
    } else {
      w.Header().Add("Goto-Status-Flip", strconv.Itoa(requestedStatuses[flipflopConfig.lastStatusIndex-1]))
      flipflopConfig.lastStatusIndex = 0
      delete(portStatus.flipflopConfigs, requestId)
    }
  } else if len(requestedStatuses) == 1 {
    requestedStatus = requestedStatuses[0]
    if times != flipflopConfig.times {
      flipflopConfig.statusCount = -1
      flipflopConfig.times = times
    }
    if flipflopConfig.statusCount == -1 && restart {
      flipflopConfig.statusCount = times
      if times > 0 {
        flipflopConfig.statusCount--
      }
    } else if flipflopConfig.statusCount == 0 || (!restart && flipflopConfig.statusCount == -1) {
      w.Header().Add("Goto-Status-Flip", strconv.Itoa(requestedStatus))
      requestedStatus = http.StatusOK
      flipflopConfig.statusCount = -1
    } else if times > 0 {
      flipflopConfig.statusCount--
    }
    if flipflopConfig.statusCount >= 0 {
      portStatus.flipflopConfigs[requestId] = flipflopConfig
    } else if restart {
      delete(portStatus.flipflopConfigs, requestId)
    }
  }
  portStatus.countsByRequestedStatus[requestedStatus]++
  portStatus.lock.Unlock()
  metrics.UpdateRequestCount("status")
  util.AddLogMessage(fmt.Sprintf("Flipflop status [%d] with statux index [%d], current count [%d], delay [%s]", requestedStatus, flipflopConfig.lastStatusIndex, flipflopConfig.statusCount, delayText), r)
  if !IsForcedStatus(r) {
    w.WriteHeader(requestedStatus)
  }
}
func getStatusCount(w http.ResponseWriter, r *http.Request) {
  port := util.GetRequestOrListenerPort(r)
  statusLock.RLock()
  portStatus := portStatusMap[port]
  statusLock.RUnlock()
  if portStatus != nil {
    if status, present := util.GetIntParam(r, "status"); present {
      portStatus.lock.RLock()
      requestCount := portStatus.countsByRequestedStatus[status]
      responseCount := portStatus.countsByResponseStatus[status]
      portStatus.lock.RUnlock()
      util.AddLogMessage(fmt.Sprintf("Port [%s] Status: %d, Request count: %d, Response count: %d",
        port, status, requestCount, responseCount), r)
      fmt.Fprintln(w, util.ToJSON(map[string]interface{}{
        "port":          port,
        "status":        status,
        "requestCount":  requestCount,
        "responseCount": responseCount,
      }))
    } else {
      msg := fmt.Sprintf("Port [%s] reporting count for all statuses", port)
      util.AddLogMessage(msg, r)
      portStatus.lock.RLock()
      fmt.Fprintln(w, util.ToJSON(map[string]interface{}{
        "port":                    port,
        "countsByRequestedStatus": portStatus.countsByRequestedStatus,
        "countsByResponseStatus":  portStatus.countsByResponseStatus,
      }))
      portStatus.lock.RUnlock()
    }
  } else {
    w.WriteHeader(http.StatusNoContent)
    fmt.Fprintln(w, "{}")
  }
}

func clearStatusCounts(w http.ResponseWriter, r *http.Request) {
  portStatus := getOrCreatePortStatus(r)
  portStatus.lock.Lock()
  portStatus.countsByRequestedStatus = map[int]int{}
  portStatus.countsByResponseStatus = map[int]int{}
  portStatus.lock.Unlock()
  msg := fmt.Sprintf("Port [%s] Response Status Counts Cleared", util.GetRequestOrListenerPort(r))
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
  events.SendRequestEvent("Response Status Counts Cleared", msg, r)
}

func IncrementStatusCount(statusCode int, r *http.Request) {
  portStatus := getOrCreatePortStatus(r)
  portStatus.lock.Lock()
  defer portStatus.lock.Unlock()
  portStatus.countsByResponseStatus[statusCode]++
}

func Middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    if util.IsKnownNonTraffic(r) {
      if next != nil {
        next.ServeHTTP(w, r)
      }
      return
    }
    crw := intercept.NewInterceptResponseWriter(w, true)
    if next != nil {
      next.ServeHTTP(crw, r)
    }
    overriddenStatus := false
    if !uri.HasURIStatus(r) {
      ps := getOrCreatePortStatus(r)
      crw.StatusCode, overriddenStatus = computeResponseStatus(crw.StatusCode, r)
      IncrementStatusCount(crw.StatusCode, r)
      if overriddenStatus {
        msg := ""
        if overriddenStatus {
          w.Header().Add("Goto-Forced-Status", strconv.Itoa(crw.StatusCode))
          w.Header().Add("Goto-Forced-Status-Remaining", strconv.Itoa(ps.alwaysReportStatusCount))
          msg = fmt.Sprintf("Reporting status: [%d] for URI [%s]. Remaining status count [%d].",
            crw.StatusCode, r.RequestURI, ps.alwaysReportStatusCount)
        } else {
          msg = fmt.Sprintf("Reporting status: [%d] for URI [%s].", crw.StatusCode, r.RequestURI)
        }
        util.AddLogMessage(msg, r)
      }
    }
    if crw.StatusCode == 0 {
      crw.StatusCode = http.StatusOK
    }
    crw.Proceed()
  })
}
