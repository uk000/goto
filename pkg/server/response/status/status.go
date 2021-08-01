package status

import (
  "fmt"
  "net/http"
  "strconv"
  "strings"
  "sync"
  "time"

  . "goto/pkg/constants"
  "goto/pkg/events"
  "goto/pkg/metrics"
  "goto/pkg/server/intercept"
  "goto/pkg/server/request/uri"
  "goto/pkg/server/response/trigger"
  "goto/pkg/util"

  "github.com/gorilla/mux"
)

type FlipFlopConfig struct {
  times           int
  statusCount     int
  lastStatusIndex int
}

type PortStatus struct {
  alwaysReportStatuses    []int
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
  util.AddRoute(root, "/status/flipflop", getStatusCount, "GET")
  util.AddRoute(root, "/status/flipflop/clear", clearStatusCounts, "POST")
  util.AddRoute(root, "/status/{status}", status, "GET", "PUT", "POST", "OPTIONS", "HEAD")
  util.AddRoute(root, "/status={status}", status, "GET", "PUT", "POST", "OPTIONS", "HEAD")
  util.AddRouteQ(root, "/status={status}/flipflop", status, "x-request-id", "{requestId}", "GET", "PUT", "POST", "OPTIONS", "HEAD")
  util.AddRoute(root, "/status={status}/flipflop", status, "GET", "PUT", "POST", "OPTIONS", "HEAD")
  util.AddRoute(root, "/status={status}/delay={delay}", status, "GET", "PUT", "POST", "OPTIONS", "HEAD")
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
  statusCodes, times, ok := util.GetStatusParam(r)
  if !ok {
    util.AddLogMessage("Invalid status", r)
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintln(w, "Invalid Status")
    return
  }
  portStatus := getOrCreatePortStatus(r)
  portStatus.lock.Lock()
  if len(statusCodes) > 0 && statusCodes[0] > 0 {
    portStatus.alwaysReportStatuses = statusCodes
    portStatus.alwaysReportStatusCount = 0
    if times > 1 {
      portStatus.alwaysReportStatusCount = times
    }
  } else {
    portStatus.alwaysReportStatuses = []int{}
    portStatus.alwaysReportStatusCount = -1
  }
  portStatus.lock.Unlock()
  msg := ""
  port := util.GetRequestOrListenerPort(r)
  if portStatus.alwaysReportStatusCount > 0 {
    msg = fmt.Sprintf("Port [%s] will respond with forced statuses %+v for next [%d] requests",
      port, portStatus.alwaysReportStatuses, portStatus.alwaysReportStatusCount)
    events.SendRequestEvent("Response Status Configured", msg, r)
  } else if portStatus.alwaysReportStatusCount == 0 {
    msg = fmt.Sprintf("Port [%s] will respond with forced statuses %+v forever",
      port, portStatus.alwaysReportStatuses)
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
  return len(portStatus.alwaysReportStatuses) > 0 && portStatus.alwaysReportStatuses[0] > 0 && portStatus.alwaysReportStatusCount >= 0
}

func computeResponseStatus(originalStatus int, r *http.Request) (int, bool) {
  portStatus := getOrCreatePortStatus(r)
  portStatus.lock.Lock()
  defer portStatus.lock.Unlock()
  overriddenStatus := false
  responseStatus := originalStatus
  if len(portStatus.alwaysReportStatuses) > 0 && portStatus.alwaysReportStatuses[0] > 0 && portStatus.alwaysReportStatusCount >= 0 {
    if len(portStatus.alwaysReportStatuses) == 1 {
      responseStatus = portStatus.alwaysReportStatuses[0]
    } else {
      responseStatus = util.RandomFrom(portStatus.alwaysReportStatuses)
    }
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
  msg := ""
  port := util.GetRequestOrListenerPort(r)
  if portStatus.alwaysReportStatusCount > 0 {
    msg = fmt.Sprintf("Port [%s] responding with forced statuses %+v for next [%d] requests",
      port, portStatus.alwaysReportStatuses, portStatus.alwaysReportStatusCount)
  } else if portStatus.alwaysReportStatusCount == 0 {
    msg = fmt.Sprintf("Port [%s] responding with forced statuses %+v forever",
      port, portStatus.alwaysReportStatuses)
  } else {
    msg = fmt.Sprintf("Port [%s] responding normally", port)
  }
  w.WriteHeader(http.StatusOK)
  fmt.Fprintln(w, msg)
}

func getStatusCount(w http.ResponseWriter, r *http.Request) {
  msg := ""
  port := util.GetRequestOrListenerPort(r)
  statusLock.RLock()
  portStatus := portStatusMap[port]
  statusLock.RUnlock()
  if portStatus != nil {
    if strings.Contains(r.RequestURI, "flipflop") {
      util.WriteJsonPayload(w, portStatus.flipflopConfigs)
      msg = "FlipFlop Status Reproted"
    } else if status, present := util.GetIntParam(r, "status"); present {
      portStatus.lock.RLock()
      requestCount := portStatus.countsByRequestedStatus[status]
      responseCount := portStatus.countsByResponseStatus[status]
      portStatus.lock.RUnlock()
      msg = fmt.Sprintf("Port [%s] Status: %d, Request count: %d, Response count: %d",
        port, status, requestCount, responseCount)
      fmt.Fprintln(w, util.ToJSON(map[string]interface{}{
        "port":          port,
        "status":        status,
        "requestCount":  requestCount,
        "responseCount": responseCount,
      }))
    } else {
      msg = fmt.Sprintf("Port [%s] reporting count for all statuses", port)
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
    msg = "No status data to report"
  }
  util.AddLogMessage(msg, r)
}

func clearStatusCounts(w http.ResponseWriter, r *http.Request) {
  portStatus := getOrCreatePortStatus(r)
  flipflop := strings.Contains(r.RequestURI, "flipflop")
  portStatus.lock.Lock()
  if flipflop {
    portStatus.flipflopConfigs = map[string]*FlipFlopConfig{}
  } else {
    portStatus.countsByRequestedStatus = map[int]int{}
    portStatus.countsByResponseStatus = map[int]int{}
  }
  portStatus.lock.Unlock()
  msg := fmt.Sprintf("Port [%s] Response Status Counts Cleared", util.GetRequestOrListenerPort(r))
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
  events.SendRequestEvent("Response Status Counts Cleared", msg, r)
}

func (ps *PortStatus) flipflop(requestId string, requestedStatuses []int, times int, r *http.Request, w http.ResponseWriter) int {
  flipflopConfig := ps.flipflopConfigs[requestId]
  if flipflopConfig == nil {
    flipflopConfig = &FlipFlopConfig{
      times:           times,
      statusCount:     -1,
      lastStatusIndex: 0,
    }
    ps.flipflopConfigs[requestId] = flipflopConfig
  }
  requestedStatus := http.StatusOK
  if len(requestedStatuses) > 1 {
    if len(requestedStatuses) > flipflopConfig.lastStatusIndex {
      requestedStatus = requestedStatuses[flipflopConfig.lastStatusIndex]
      flipflopConfig.lastStatusIndex++
    } else {
      w.Header().Add(HeaderGotoStatusFlip, strconv.Itoa(requestedStatuses[flipflopConfig.lastStatusIndex-1]))
      flipflopConfig.lastStatusIndex = 0
      delete(ps.flipflopConfigs, requestId)
    }
  } else if len(requestedStatuses) == 1 {
    requestedStatus = requestedStatuses[0]
    if times != flipflopConfig.times {
      flipflopConfig.statusCount = -1
      flipflopConfig.times = times
    }
    if flipflopConfig.statusCount == -1 {
      flipflopConfig.statusCount = times
      if times > 0 {
        flipflopConfig.statusCount--
      }
    } else if flipflopConfig.statusCount == 0 {
      w.Header().Add(HeaderGotoStatusFlip, strconv.Itoa(requestedStatuses[flipflopConfig.lastStatusIndex-1]))
      requestedStatus = http.StatusOK
      flipflopConfig.statusCount--
    } else if times > 0 {
      flipflopConfig.statusCount--
    }
    if flipflopConfig.statusCount >= 0 {
      ps.flipflopConfigs[requestId] = flipflopConfig
    } else {
      delete(ps.flipflopConfigs, requestId)
    }
  }
  util.AddLogMessage(fmt.Sprintf("Flipflop status [%d] with statux index [%d], current count [%d]", requestedStatus, flipflopConfig.lastStatusIndex, flipflopConfig.statusCount), r)
  return requestedStatus
}

func status(w http.ResponseWriter, r *http.Request) {
  portStatus := getOrCreatePortStatus(r)
  requestedStatuses, times, _ := util.GetStatusParam(r)
  delayMin, delayMax, _, _ := util.GetDurationParam(r, "delay")
  requestId := util.GetStringParamValue(r, "requestId")
  flipflop := strings.Contains(r.RequestURI, "flipflop")
  requestedStatus := 200
  if times <= 0 {
    times = len((requestedStatuses))
  }
  portStatus.lock.Lock()
  if flipflop {
    requestedStatus = portStatus.flipflop(requestId, requestedStatuses, times, r, w)
  } else if len(requestedStatuses) == 1 {
    requestedStatus = requestedStatuses[0]
  } else if len(requestedStatuses) > 1 {
    requestedStatus = util.RandomFrom(requestedStatuses)
  }
  portStatus.countsByRequestedStatus[requestedStatus]++
  portStatus.lock.Unlock()
  metrics.UpdateRequestCount("status")
  delay := 0 * time.Second
  delayText := ""
  if delayMin > 0 {
    delay = util.RandomDuration(delayMin, delayMax)
    time.Sleep(delay)
    delayText = delay.String()
    w.Header().Add(HeaderGotoResponseDelay, delayText)
  }
  if flipflop {
  } else {
    util.AddLogMessage(fmt.Sprintf("Requested status [%d] with delay [%s]", requestedStatus, delayText), r)
  }
  w.Header().Add(HeaderGotoRequestedStatus, strconv.Itoa(requestedStatus))
  if !IsForcedStatus(r) {
    w.WriteHeader(requestedStatus)
  }
}

func IncrementStatusCount(statusCode int, r *http.Request) {
  portStatus := getOrCreatePortStatus(r)
  portStatus.lock.Lock()
  defer portStatus.lock.Unlock()
  portStatus.countsByResponseStatus[statusCode]++
}

func Middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    if next != nil {
      next.ServeHTTP(w, r)
    }
    if util.IsKnownNonTraffic(r) {
      return
    }
    overriddenStatus := false
    irw := util.GetInterceptResponseWriter(r).(*intercept.InterceptResponseWriter)
    if !uri.HasURIStatus(r) {
      ps := getOrCreatePortStatus(r)
      irw.StatusCode, overriddenStatus = computeResponseStatus(irw.StatusCode, r)
      if irw.StatusCode == 0 {
        irw.StatusCode = http.StatusOK
      }
      IncrementStatusCount(irw.StatusCode, r)
      msg := ""
      if overriddenStatus {
        w.Header().Add(HeaderGotoForcedStatus, strconv.Itoa(irw.StatusCode))
        if ps.alwaysReportStatusCount > 0 {
          w.Header().Add(HeaderGotoForcedStatusRemaining, strconv.Itoa(ps.alwaysReportStatusCount))
        }
        msg = fmt.Sprintf("Reporting status: [%d] for URI [%s]. Remaining status count [%d].",
          irw.StatusCode, r.RequestURI, ps.alwaysReportStatusCount)
      } else {
        msg = fmt.Sprintf("Reporting status: [%d] for URI [%s].", irw.StatusCode, r.RequestURI)
      }
      util.UpdateTrafficEventStatusCode(r, irw.StatusCode)
      util.AddLogMessage(msg, r)
      trigger.RunTriggers(r, irw, irw.StatusCode)
    }
  })
}
