/**
 * Copyright 2025 uk
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
	"goto/pkg/server/middleware"
	"goto/pkg/server/request/uri"
	"goto/pkg/server/response/trigger"
	"goto/pkg/types"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

type FlipFlopConfig struct {
	Times           int `json:"times"`
	StatusCount     int `json:"statusCount"`
	LastStatusIndex int `json:"lastStatusIndex"`
	LastStatus      int `json:"lastStatus"`
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
	Middleware    = middleware.NewMiddleware("status", setRoutes, middlewareFunc)
	portStatusMap = map[string]*PortStatus{}
	statusLock    sync.RWMutex
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	statusRouter := util.PathRouter(r, "/status")
	util.AddRouteWithPort(statusRouter, "/set/{status}", setStatus, "PUT", "POST")
	util.AddRouteWithPort(statusRouter, "/counts/clear", clearStatus, "PUT", "POST")
	util.AddRouteWithPort(statusRouter, "/counts/{status}", getStatusCount, "GET")
	util.AddRouteWithPort(statusRouter, "/counts", getStatusCount, "GET")
	util.AddRouteWithPort(statusRouter, "/clear", setStatus, "PUT", "POST")
	util.AddRouteWithPort(statusRouter, "", getStatus, "GET")
	util.AddRoute(root, "/status/flipflop", getStatusCount, "GET")
	util.AddRoute(root, "/status/clear", clearStatus, "POST")
	util.AddRoute(root, "/status/{status}", status)
	util.AddRouteQ(root, "/status={status}", status, "x-request-id", "GET", "PUT", "POST", "OPTIONS", "HEAD", "DELETE")
	util.AddRoute(root, "/status={status}", status)
	util.AddRouteQ(root, "/status={status}/delay={delay}", status, "x-request-id", "GET", "PUT", "POST", "OPTIONS", "HEAD", "DELETE")
	util.AddRoute(root, "/status={status}/delay={delay}", status)
	util.AddRouteQ(root, "/status={status}/flipflop", status, "x-request-id", "GET", "PUT", "POST", "OPTIONS", "HEAD", "DELETE")
	util.AddRoute(root, "/status={status}/flipflop", status)
	util.AddRouteQ(root, "/status={status}/delay={delay}/flipflop", status, "x-request-id", "GET", "PUT", "POST", "OPTIONS", "HEAD", "DELETE")
	util.AddRoute(root, "/status={status}/delay={delay}/flipflop", status)
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
		} else {
			portStatus.flipflopConfigs = map[string]*FlipFlopConfig{}
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
			responseStatus = types.RandomFrom(portStatus.alwaysReportStatuses)
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
			fmt.Fprintln(w, util.ToJSONText(map[string]interface{}{
				"port":          port,
				"status":        status,
				"requestCount":  requestCount,
				"responseCount": responseCount,
			}))
		} else {
			msg = fmt.Sprintf("Port [%s] reporting count for all statuses", port)
			portStatus.lock.RLock()
			fmt.Fprintln(w, util.ToJSONText(map[string]interface{}{
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

func clearStatus(w http.ResponseWriter, r *http.Request) {
	portStatus := getOrCreatePortStatus(r)
	counts := strings.Contains(r.RequestURI, "counts")
	msg := ""
	portStatus.lock.Lock()
	if counts {
		portStatus.countsByRequestedStatus = map[int]int{}
		portStatus.countsByResponseStatus = map[int]int{}
		msg = fmt.Sprintf("Port [%s] Response Status Counts Cleared", util.GetRequestOrListenerPort(r))
		events.SendRequestEvent("Response Status Counts Cleared", msg, r)
	} else {
		portStatus.flipflopConfigs = map[string]*FlipFlopConfig{}
		msg = fmt.Sprintf("Port [%s] Response Status State Cleared", util.GetRequestOrListenerPort(r))
		events.SendRequestEvent("Response Status Cleared", msg, r)
	}
	portStatus.lock.Unlock()
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func (ps *PortStatus) flipflop(requestId string, requestedStatuses []int, times int, restart bool, r *http.Request, w http.ResponseWriter) int {
	flipflopConfig := ps.flipflopConfigs[requestId]
	if flipflopConfig == nil {
		flipflopConfig = &FlipFlopConfig{
			Times:           times,
			StatusCount:     times,
			LastStatusIndex: 0,
		}
		ps.flipflopConfigs[requestId] = flipflopConfig
	}
	requestedStatus := http.StatusOK
	if len(requestedStatuses) > 1 {
		if len(requestedStatuses) > flipflopConfig.LastStatusIndex {
			requestedStatus = requestedStatuses[flipflopConfig.LastStatusIndex]
			flipflopConfig.LastStatusIndex++
		} else {
			w.Header().Add(HeaderGotoStatusFlip, strconv.Itoa(requestedStatuses[flipflopConfig.LastStatusIndex-1]))
			flipflopConfig.LastStatusIndex = 0
			delete(ps.flipflopConfigs, requestId)
		}
	} else if len(requestedStatuses) == 1 {
		requestedStatus = requestedStatuses[0]
		if times != flipflopConfig.Times || requestedStatus != flipflopConfig.LastStatus {
			flipflopConfig.StatusCount = times
			flipflopConfig.Times = times
			flipflopConfig.LastStatus = requestedStatus
		}
		if flipflopConfig.StatusCount == -1 && restart {
			flipflopConfig.StatusCount = times
			if times > 0 {
				flipflopConfig.StatusCount--
			}
		} else if flipflopConfig.StatusCount == 0 || (!restart && flipflopConfig.StatusCount == -1) {
			w.Header().Add(HeaderGotoStatusFlip, strconv.Itoa(requestedStatuses[len(requestedStatuses)-1]))
			requestedStatus = http.StatusOK
			flipflopConfig.StatusCount = -1
		} else if times > 0 {
			flipflopConfig.StatusCount--
		}
		if flipflopConfig.StatusCount >= 0 {
			ps.flipflopConfigs[requestId] = flipflopConfig
		} else if restart {
			delete(ps.flipflopConfigs, requestId)
		}
	}
	util.AddLogMessage(fmt.Sprintf("Flipflop: request id [%s], status [%d], count [%d], remaining [%d]", requestId, requestedStatus, times, flipflopConfig.StatusCount), r)
	return requestedStatus
}

func status(w http.ResponseWriter, r *http.Request) {
	portStatus := getOrCreatePortStatus(r)
	requestedStatuses, times, _ := util.GetStatusParam(r)
	delayMin, delayMax, _, _ := util.GetDurationParam(r, "delay")
	requestId := util.GetStringParamValue(r, "x-request-id")
	flipflop := strings.Contains(r.RequestURI, "flipflop")
	requestedStatus := 200
	restart := flipflop
	if times > 0 {
		flipflop = true
	} else {
		times = len(requestedStatuses)
	}
	portStatus.lock.Lock()
	if flipflop {
		requestedStatus = portStatus.flipflop(requestId, requestedStatuses, times, restart, r, w)
	} else if len(requestedStatuses) == 1 {
		requestedStatus = requestedStatuses[0]
	} else if len(requestedStatuses) > 1 {
		requestedStatus = types.RandomFrom(requestedStatuses)
	}
	portStatus.countsByRequestedStatus[requestedStatus]++
	portStatus.lock.Unlock()
	metrics.UpdateRequestCount("status")
	delay := 0 * time.Second
	delayText := ""
	if delayMin > 0 {
		delay = types.RandomDuration(delayMin, delayMax)
		time.Sleep(delay)
		delayText = delay.String()
		w.Header().Add(HeaderGotoResponseDelay, delayText)
	}
	util.AddLogMessage(fmt.Sprintf("Requested status [%d] with delay [%s]", requestedStatus, delayText), r)
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

func middlewareFunc(next http.Handler) http.Handler {
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
