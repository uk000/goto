/**
 * Copyright 2026 uk
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
	"goto/pkg/server/middleware"
	"goto/pkg/types"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
	Middleware       = middleware.NewMiddleware("delay", setRoutes, middlewareFunc)
	delayByPort      = map[string][]time.Duration{}
	delayCountByPort = map[string]int{}
	delayLock        sync.RWMutex
)

func setRoutes(r *mux.Router) {
	delayRouter := util.PathRouter(r, "/delay")
	util.AddRoute(delayRouter, "/set/{delay}", setDelay, "POST", "PUT")
	util.AddRoute(delayRouter, "/clear", clearDelay, "POST", "PUT")
	util.AddRoute(delayRouter, "", getDelay, "GET")
	util.AddRoute(delayRouter, "/{delay}", delay)
	delayRouter = middleware.RootPath("/delay")
	util.AddRoute(delayRouter, "/{delay}", delay)
	sleepRouter := middleware.RootPath("/sleep")
	util.AddRoute(sleepRouter, "/{delay}", delay)
}

func setDelay(w http.ResponseWriter, r *http.Request) {
	delayMin, delayMax, delayCount, ok := util.GetDurationParam(r, "delay")
	if !ok {
		// An invalid delay is refused without touching the delay that is already set.
		msg := "Invalid delay param"
		w.WriteHeader(http.StatusBadRequest)
		util.AddLogMessage(msg, r)
		fmt.Fprintln(w, msg)
		return
	}
	if delayMin <= 0 {
		clearDelay(w, r)
		return
	}
	listenerPort := util.GetRequestOrListenerPort(r)
	delayRange := []time.Duration{delayMin, delayMax}
	delayLock.Lock()
	delayByPort[listenerPort] = delayRange
	delayCountByPort[listenerPort] = delayCount
	delayLock.Unlock()
	var msg string
	if delayCount > 0 {
		msg = fmt.Sprintf("Port [%s] will delay next [%d] requests with delay %s", listenerPort, delayCount, delayRange)
	} else {
		msg = fmt.Sprintf("Port [%s] will delay requests with %s forever", listenerPort, delayRange)
	}
	events.SendRequestEvent("Delay Configured", msg, r)
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func clearDelay(w http.ResponseWriter, r *http.Request) {
	listenerPort := util.GetRequestOrListenerPort(r)
	delayLock.Lock()
	delete(delayByPort, listenerPort)
	delete(delayCountByPort, listenerPort)
	delayLock.Unlock()
	msg := fmt.Sprintf("Port [%s] delay cleared", listenerPort)
	events.SendRequestEvent("Delay Cleared", msg, r)
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func delay(w http.ResponseWriter, r *http.Request) {
	metrics.UpdateRequestCount("delay")
	msg := ""
	delayMin, delayMax, _, ok := util.GetDurationParam(r, "delay")
	if delayMin > 0 {
		delay := randomDelay(delayMin, delayMax)
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

// randomDelay picks a delay from the range. A range that is narrower than the millisecond that delays are picked in is
// answered with its low end.
func randomDelay(low, high time.Duration) time.Duration {
	if high-low < time.Millisecond {
		return low
	}
	return types.RandomDuration(low, high)
}

func getDelay(w http.ResponseWriter, r *http.Request) {
	delayLock.RLock()
	defer delayLock.RUnlock()
	listenerPort := util.GetRequestOrListenerPort(r)
	delay := delayByPort[listenerPort]
	util.WriteJsonPayload(w, map[string]interface{}{"delay": delay, "count": delayCountByPort[listenerPort]})
	util.AddLogMessage("Delay reported", r)
}

// takeResponseDelay picks the delay to apply to a request, and uses up one of the requests that the delay is counted
// for. It does both under one lock, so that requests that arrive together don't use the same count.
func takeResponseDelay(r *http.Request) (delay time.Duration, remaining int, apply bool) {
	if util.IsDelayRequest(r) {
		return 0, 0, false
	}
	listenerPort := util.GetRequestOrListenerPort(r)
	delayLock.Lock()
	defer delayLock.Unlock()
	delayRange := delayByPort[listenerPort]
	count := delayCountByPort[listenerPort]
	if len(delayRange) == 0 || delayRange[0] <= 0 || count < 0 {
		return 0, 0, false
	}
	if count == 1 {
		delete(delayByPort, listenerPort)
		delete(delayCountByPort, listenerPort)
		count = 0
	} else if count > 1 {
		count--
		delayCountByPort[listenerPort] = count
	}
	return randomDelay(delayRange[0], delayRange[1]), count, true
}

func middlewareFunc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs := util.GetRequestStore(r)
		if !rs.IsKnownNonTraffic {
			if delay, remaining, apply := takeResponseDelay(r); apply {
				msg := fmt.Sprintf("Delaying [%s] for [%s]. Remaining delay count [%d].", r.RequestURI, delay.String(), remaining)
				util.AddLogMessage(msg, r)
				util.UpdateTrafficEventDetails(r, "Response Delay Applied")
				w.Header().Add(constants.HeaderGotoResponseDelay, delay.String())
				time.Sleep(delay)
			}
		}
		if next != nil {
			next.ServeHTTP(w, r)
		}
	})
}
