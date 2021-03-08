package status

import (
  "fmt"
  "net/http"
  "strconv"
  "sync"

  "goto/pkg/events"
  "goto/pkg/metrics"
  "goto/pkg/server/intercept"
  "goto/pkg/server/request/uri"
  "goto/pkg/server/response/trigger"
  "goto/pkg/util"

  "github.com/gorilla/mux"
)

type PortStatus struct {
  alwaysReportStatus      int
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
  util.AddRoute(root, "/status/{status}", getStatus, "GET", "PUT", "POST", "OPTIONS", "HEAD")
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
  statusCode, times, _ := util.GetStatusParam(r)
  portStatus := getOrCreatePortStatus(r)
  statusLock.Lock()
  portStatus.alwaysReportStatusCount = -1
  portStatus.alwaysReportStatus = 200
  if statusCode > 0 {
    portStatus.alwaysReportStatus = statusCode
    portStatus.alwaysReportStatusCount = 0
    if times > 1 {
      portStatus.alwaysReportStatusCount = times
    }
  }
  statusLock.Unlock()
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
  statusLock.RLock()
  defer statusLock.RUnlock()
  return portStatus.alwaysReportStatus > 0 && portStatus.alwaysReportStatusCount >= 0
}

func computeResponseStatus(originalStatus int, r *http.Request) (int, bool) {
  portStatus := getOrCreatePortStatus(r)
  statusLock.Lock()
  defer statusLock.Unlock()
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
  requestedStatus, _ := util.GetIntParam(r, "status", 200)
  if !util.IsAdminRequest(r) {
    metrics.UpdateRequestCount("status")
    statusLock.Lock()
    portStatus.countsByRequestedStatus[requestedStatus]++
    statusLock.Unlock()
    util.AddLogMessage(fmt.Sprintf("Requested status: [%d]", requestedStatus), r)
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
      IncrementStatusCount(irw.StatusCode, r)
      msg := ""
      if overriddenStatus {
        w.Header().Add("Goto-Forced-Status", strconv.Itoa(irw.StatusCode))
        w.Header().Add("Goto-Forced-Status-Remaining", strconv.Itoa(ps.alwaysReportStatusCount))
        msg = fmt.Sprintf("Reporting status: [%d] for URI [%s]. Remaining status count [%d].",
          irw.StatusCode, r.RequestURI, ps.alwaysReportStatusCount)
      } else {
        msg = fmt.Sprintf("Reporting status: [%d] for URI [%s].", irw.StatusCode, r.RequestURI)
      }
      util.AddLogMessage(msg, r)
      trigger.RunTriggers(r, irw, irw.StatusCode)
    }
    if irw.StatusCode == 0 {
      irw.StatusCode = http.StatusOK
    }
  })
}
