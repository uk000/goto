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
  "goto/pkg/server/response/trigger"
  "goto/pkg/util"

  "github.com/gorilla/mux"
)

type PortStatus struct {
  alwaysReportStatuses    []int
  alwaysReportStatusCount int
  countsByRequestedStatus map[int]int
  countsByResponseStatus  map[int]int
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
  util.AddRoute(root, "/status/{status}", status, "GET", "PUT", "POST", "OPTIONS", "HEAD")
  util.AddRoute(root, "/status/{status}/delay/{delay}", status, "GET", "PUT", "POST", "OPTIONS", "HEAD")
}

func getOrCreatePortStatus(r *http.Request) *PortStatus {
  listenerPort := util.GetRequestOrListenerPort(r)
  statusLock.Lock()
  defer statusLock.Unlock()
  portStatus := portStatusMap[listenerPort]
  if portStatus == nil {
    portStatus = &PortStatus{countsByRequestedStatus: map[int]int{}, countsByResponseStatus: map[int]int{}}
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
  statusLock.Lock()
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
  statusLock.Unlock()
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
  statusLock.RLock()
  defer statusLock.RUnlock()
  return len(portStatus.alwaysReportStatuses) > 0 && portStatus.alwaysReportStatuses[0] > 0 && portStatus.alwaysReportStatusCount >= 0
}

func computeResponseStatus(originalStatus int, r *http.Request) (int, bool) {
  portStatus := getOrCreatePortStatus(r)
  statusLock.Lock()
  defer statusLock.Unlock()
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
  port := util.GetRequestOrListenerPort(r)
  statusLock.RLock()
  portStatus := portStatusMap[port]
  statusLock.RUnlock()
  if portStatus != nil {
    if status, present := util.GetIntParam(r, "status"); present {
      statusLock.RLock()
      requestCount := portStatus.countsByRequestedStatus[status]
      responseCount := portStatus.countsByResponseStatus[status]
      statusLock.RUnlock()
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
      statusLock.RLock()
      fmt.Fprintln(w, util.ToJSON(map[string]interface{}{
        "port":                    port,
        "countsByRequestedStatus": portStatus.countsByRequestedStatus,
        "countsByResponseStatus":  portStatus.countsByResponseStatus,
      }))
      statusLock.RUnlock()
    }
  } else {
    w.WriteHeader(http.StatusNoContent)
    fmt.Fprintln(w, "{}")
  }
}

func clearStatusCounts(w http.ResponseWriter, r *http.Request) {
  portStatus := getOrCreatePortStatus(r)
  statusLock.Lock()
  portStatus.countsByRequestedStatus = map[int]int{}
  portStatus.countsByResponseStatus = map[int]int{}
  statusLock.Unlock()
  msg := fmt.Sprintf("Port [%s] Response Status Counts Cleared", util.GetRequestOrListenerPort(r))
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
  events.SendRequestEvent("Response Status Counts Cleared", msg, r)
}

func status(w http.ResponseWriter, r *http.Request) {
  portStatus := getOrCreatePortStatus(r)
  requestedStatuses, _, _ := util.GetStatusParam(r)
  delayMin, delayMax, _, _ := util.GetDurationParam(r, "delay")
  requestedStatus := 200
  if len(requestedStatuses) == 1 {
    requestedStatus = requestedStatuses[0]
  } else if len(requestedStatuses) > 1 {
    requestedStatus = util.RandomFrom(requestedStatuses)
  }
  metrics.UpdateRequestCount("status")
  statusLock.Lock()
  portStatus.countsByRequestedStatus[requestedStatus]++
  statusLock.Unlock()
  delay := 0 * time.Second
  delayText := ""
  if delayMin > 0 {
    delay = util.RandomDuration(delayMin, delayMax)
    time.Sleep(delay)
    delayText = delay.String()
    w.Header().Add("Goto-Response-Delay", delayText)
  }
  util.AddLogMessage(fmt.Sprintf("Requested status [%d] with delay [%s]", requestedStatus, delayText), r)
  w.Header().Add("Goto-Requested-Status", strconv.Itoa(requestedStatus))
  if !IsForcedStatus(r) {
    w.WriteHeader(requestedStatus)
  }
}

func IncrementStatusCount(statusCode int, r *http.Request) {
  portStatus := getOrCreatePortStatus(r)
  statusLock.Lock()
  defer statusLock.Unlock()
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
        w.Header().Add("Goto-Forced-Status", strconv.Itoa(irw.StatusCode))
        if ps.alwaysReportStatusCount > 0 {
          w.Header().Add("Goto-Forced-Status-Remaining", strconv.Itoa(ps.alwaysReportStatusCount))
        }
        msg = fmt.Sprintf("Reporting status: [%d] for URI [%s]. Remaining status count [%d].",
          irw.StatusCode, r.RequestURI, ps.alwaysReportStatusCount)
      } else {
        msg = fmt.Sprintf("Reporting status: [%d] for URI [%s].", irw.StatusCode, r.RequestURI)
      }
      util.AddLogMessage(msg, r)
      trigger.RunTriggers(r, irw, irw.StatusCode)
    }
  })
}
