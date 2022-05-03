/**
 * Copyright 2022 uk
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

package delay

import (
  "fmt"
  "net/http"
  "sync"
  "time"

  "goto/pkg/constants"
  "goto/pkg/events"
  "goto/pkg/metrics"
  "goto/pkg/util"

  "github.com/gorilla/mux"
)

var (
  Handler          util.ServerHandler         = util.ServerHandler{"delay", SetRoutes, Middleware}
  delayByPort      map[string][]time.Duration = map[string][]time.Duration{}
  delayCountByPort map[string]int             = map[string]int{}
  delayLock        sync.RWMutex
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  delayRouter := util.PathRouter(r, "/delay")
  util.AddRouteWithPort(delayRouter, "/set/{delay}", setDelay, "POST", "PUT")
  util.AddRouteWithPort(delayRouter, "/clear", setDelay, "POST", "PUT")
  util.AddRouteWithPort(delayRouter, "", getDelay, "GET")
  util.AddRoute(root, "/delay/{delay}", delay)
}

func setDelay(w http.ResponseWriter, r *http.Request) {
  delayMin, delayMax, delayCount, ok := util.GetDurationParam(r, "delay")
  listenerPort := util.GetRequestOrListenerPort(r)
  delayLock.Lock()
  defer delayLock.Unlock()
  delayCountByPort[listenerPort] = -1
  delayByPort[listenerPort] = nil
  msg := ""
  if ok {
    if delayMin > 0 {
      delayByPort[listenerPort] = []time.Duration{delayMin, delayMax}
      delayCountByPort[listenerPort] = delayCount
      if delayCount > 0 {
        msg = fmt.Sprintf("Port [%s] will delay next [%d] requests with delay %s",
          listenerPort, delayCountByPort[listenerPort], delayByPort[listenerPort])
        events.SendRequestEvent("Delay Configured", msg, r)
      } else if delayCount == 0 {
        msg = fmt.Sprintf("Port [%s] will delay requests with %s forever",
          listenerPort, delayByPort[listenerPort])
        events.SendRequestEvent("Delay Configured", msg, r)
      }
    } else {
      delete(delayByPort, listenerPort)
      delete(delayCountByPort, listenerPort)
      msg = fmt.Sprintf("Port [%s] delay cleared", listenerPort)
      events.SendRequestEvent("Delay Cleared", msg, r)
    }
  } else {
    msg = "Invalid delay param"
    w.WriteHeader(http.StatusBadRequest)
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func delay(w http.ResponseWriter, r *http.Request) {
  metrics.UpdateRequestCount("delay")
  delayLock.RLock()
  defer delayLock.RUnlock()
  msg := ""
  delayMin, delayMax, _, ok := util.GetDurationParam(r, "delay")
  if delayMin > 0 {
    delay := util.RandomDuration(delayMin, delayMax)
    val := delay.String()
    msg = fmt.Sprintf("Delayed by: %s", val)
    time.Sleep(delay)
    w.Header().Add(constants.HeaderGotoResponseDelay, val)
    w.WriteHeader(http.StatusOK)
  } else if !ok {
    msg = "Invalid Delay"
    w.WriteHeader(http.StatusBadRequest)
  }
  util.AddLogMessage(msg, r)
}

func getDelay(w http.ResponseWriter, r *http.Request) {
  delayLock.RLock()
  defer delayLock.RUnlock()
  listenerPort := util.GetRequestOrListenerPort(r)
  delay := delayByPort[listenerPort]
  util.WriteJsonPayload(w, map[string]interface{}{"delay": delay, "count": delayCountByPort[listenerPort]})
  util.AddLogMessage("Delay reported", r)
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
    delayRange := delayByPort[listenerPort]
    delayCount := delayCountByPort[listenerPort]
    delayLock.RUnlock()
    if len(delayRange) > 0 && delayRange[0] > 0 && delayCount >= 0 && !util.IsDelayRequest(r) {
      if delayCount > 0 {
        delayLock.Lock()
        if delayCount == 1 {
          delete(delayByPort, listenerPort)
          delete(delayCountByPort, listenerPort)
        } else {
          delayCount--
          delayCountByPort[listenerPort] = delayCount
        }
        delayLock.Unlock()
      }
      delay := util.RandomDuration(delayRange[0], delayRange[1])
      msg := fmt.Sprintf("Delaying [%s] for [%s]. Remaining delay count [%d].", r.RequestURI, delay.String(), delayCount)
      util.AddLogMessage(msg, r)
      util.UpdateTrafficEventDetails(r, "Response Delay Applied")
      w.Header().Add(constants.HeaderGotoResponseDelay, delay.String())
      time.Sleep(delay)
    }
    if next != nil {
      next.ServeHTTP(w, r)
    }
  })
}
