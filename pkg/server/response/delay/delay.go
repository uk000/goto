package delay

import (
  "fmt"
  "net/http"
  "strconv"
  "strings"
  "sync"
  "time"

  "goto/pkg/events"
  "goto/pkg/metrics"
  "goto/pkg/util"

  "github.com/gorilla/mux"
)

var (
  Handler          util.ServerHandler       = util.ServerHandler{"delay", SetRoutes, Middleware}
  delayByPort      map[string]time.Duration = map[string]time.Duration{}
  delayCountByPort map[string]int           = map[string]int{}
  delayLock        sync.RWMutex
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  delayRouter := util.PathRouter(r, "/delay")
  util.AddRouteWithPort(delayRouter, "/set/{delay}", setDelay, "POST", "PUT")
  util.AddRouteWithPort(delayRouter, "/clear", setDelay, "POST", "PUT")
  util.AddRouteWithPort(delayRouter, "", getDelay, "GET")
  util.AddRoute(root, "/delay/{delay}", delay, "GET", "PUT", "POST", "OPTIONS", "HEAD")
  util.AddRoute(root, "/delay", delay, "GET", "PUT", "POST", "OPTIONS", "HEAD")
}

func setDelay(w http.ResponseWriter, r *http.Request) {
  vars := mux.Vars(r)
  delayParam := strings.Split(vars["delay"], ":")
  listenerPort := util.GetRequestOrListenerPort(r)
  delayLock.Lock()
  defer delayLock.Unlock()
  delayCountByPort[listenerPort] = -1
  delayByPort[listenerPort] = 0
  msg := ""
  if len(delayParam[0]) > 0 {
    if delay, err := time.ParseDuration(delayParam[0]); err == nil {
      delayByPort[listenerPort] = delay
      if delay > 0 {
        delayCountByPort[listenerPort] = 0
      }
      if len(delayParam) > 1 {
        times, _ := strconv.ParseInt(delayParam[1], 10, 32)
        delayCountByPort[listenerPort] = int(times)
      }
      if delayCountByPort[listenerPort] > 0 {
        msg = fmt.Sprintf("Port [%s] will delay next %d requests with %s",
          listenerPort, delayCountByPort[listenerPort], delayByPort[listenerPort])
        events.SendRequestEvent("Delay Configured", msg, r)
      } else if delayCountByPort[listenerPort] == 0 {
        msg = fmt.Sprintf("Port [%s] will delay requests with %s until reset",
          listenerPort, delayByPort[listenerPort])
        events.SendRequestEvent("Delay Configured", msg, r)
      } else {
        msg = fmt.Sprintf("Port [%s] delay cleared", listenerPort)
        events.SendRequestEvent("Delay Cleared", msg, r)
      }
    } else {
      msg = "Invalid delay param"
      w.WriteHeader(http.StatusBadRequest)
    }
  } else {
    msg = fmt.Sprintf("Port [%s] delay cleared", listenerPort)
    events.SendRequestEvent("Delay Cleared", msg, r)
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func delay(w http.ResponseWriter, r *http.Request) {
  metrics.UpdateRequestCount("delay")
  delayLock.RLock()
  defer delayLock.RUnlock()
  msg := ""
  delayParam := util.GetStringParamValue(r, "delay")
  if delay, err := time.ParseDuration(delayParam); err == nil {
    msg = fmt.Sprintf("Delayed by: %s", delay.String())
    time.Sleep(delay)
    w.Header().Add("Goto-Response-Delay", delay.String())
    w.WriteHeader(http.StatusOK)
  } else if delayParam != "" {
    msg = err.Error()
    w.WriteHeader(http.StatusBadRequest)
  }
  util.AddLogMessage(msg, r)
}

func getDelay(w http.ResponseWriter, r *http.Request) {
  delayLock.RLock()
  defer delayLock.RUnlock()
  listenerPort := util.GetRequestOrListenerPort(r)
  delay := delayByPort[listenerPort]
  msg := fmt.Sprintf("Port [%s] Current delay: %s", listenerPort, delay.String())
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func Middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    if util.IsKnownNonTraffic(r) {
      if next != nil {
        next.ServeHTTP(w, r)
      }
      return
    }
    delayLock.RLock()
    listenerPort := util.GetRequestOrListenerPort(r)
    delay := delayByPort[listenerPort]
    delayCount := delayCountByPort[listenerPort]
    delayLock.RUnlock()
    if delay > 0 && delayCount >= 0 && !util.IsDelayRequest(r) {
      if delayCount > 0 {
        newDelay := delay
        if delayCount == 1 {
          delayCount = -1
          newDelay = 0
        } else {
          delayCount--
        }
        delayLock.Lock()
        delayByPort[listenerPort] = newDelay
        delayCountByPort[listenerPort] = delayCount
        delayLock.Unlock()
      }
      msg := fmt.Sprintf("Delaying [%s] for [%s]. Remaining delay count [%d].", r.RequestURI, delay.String(), delayCount)
      util.AddLogMessage(msg, r)
      util.UpdateTrafficEventDetails(r, "Response Delay Applied")
      w.Header().Add("Goto-Response-Delay", delay.String())
      time.Sleep(delay)
    }
    if next != nil {
      next.ServeHTTP(w, r)
    }
  })
}
