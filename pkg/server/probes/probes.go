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

package probes

import (
	"fmt"
	. "goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/metrics"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/mux"
)

type PortProbes struct {
	Port                          string `json:"port"`
	ReadinessProbe                string `json:"readinessProbe"`
	ReadinessStatus               int    `json:"readinessStatus"`
	ReadinessStatusRemainingCount int    `json:"readinessStatusRemainingCount"`
	ReadinessCount                uint64 `json:"readinessCount"`
	ReadinessOverflowCount        uint64 `json:"readinessOverflowCount"`
	LivenessProbe                 string `json:"livenessProbe"`
	LivenessStatus                int    `json:"livenessStatus"`
	LivenessStatusRemainingCount  int    `json:"livenessStatusRemainingCount"`
	LivenessCount                 uint64 `json:"livenessCount"`
	LivenessOverflowCount         uint64 `json:"livenessOverflowCount"`
	lock                          sync.RWMutex
}

var (
	Middleware   = middleware.NewMiddleware("probes", setRoutes, middlewareFunc)
	probesByPort = map[string]*PortProbes{}
	lock         sync.RWMutex
)

// setRoutes registers the /probes routes to set probe URIs and statuses, clear counts and report the probes, and exposes the probe checks through the global funcs.
func setRoutes(r *mux.Router) {
	probeRouter := middleware.RootPath("/probes")
	util.AddRouteQ(probeRouter, "/{type}/set", setProbe, "uri", "PUT", "POST")
	util.AddRoute(probeRouter, "/{type}/set/status={status}", setProbeStatus, "PUT", "POST")
	util.AddRoute(probeRouter, "/counts/clear", clearProbeCounts, "POST")
	util.AddRoute(probeRouter, "", getProbes, "GET")
	global.Funcs.IsLivenessProbe = IsLivenessProbe
	global.Funcs.IsReadinessProbe = IsReadinessProbe
}

// IsLivenessProbe reports whether the request URI is the liveness probe URI of its port.
func IsLivenessProbe(r *http.Request) bool {
	pp := initPortProbes(r)
	pp.lock.RLock()
	defer pp.lock.RUnlock()
	return strings.EqualFold(r.RequestURI, pp.LivenessProbe)
}

// IsReadinessProbe reports whether the request URI is the readiness probe URI of its port.
func IsReadinessProbe(r *http.Request) bool {
	pp := initPortProbes(r)
	pp.lock.RLock()
	defer pp.lock.RUnlock()
	return strings.EqualFold(r.RequestURI, pp.ReadinessProbe)
}

// GetPortProbes returns the probes of a port, creating them on first use with /ready and /live returning 200.
func GetPortProbes(port string) *PortProbes {
	lock.Lock()
	defer lock.Unlock()
	if probesByPort[port] == nil {
		probesByPort[port] = &PortProbes{Port: port, ReadinessProbe: "/ready", ReadinessStatus: 200, LivenessProbe: "/live", LivenessStatus: 200}
	}
	return probesByPort[port]
}

// initPortProbes returns the probes of the request's port.
func initPortProbes(r *http.Request) *PortProbes {
	return GetPortProbes(util.GetRequestOrListenerPort(r))
}

// setProbe sets the readiness or liveness probe URI of the request's port and resets its count.
func setProbe(w http.ResponseWriter, r *http.Request) {
	msg := ""
	probeType := util.GetStringParamValue(r, "type")
	isReadiness := strings.EqualFold(probeType, "readiness")
	isLiveness := strings.EqualFold(probeType, "liveness")
	if !isReadiness && !isLiveness {
		msg = "Cannot add. Invalid probe type"
		w.WriteHeader(http.StatusBadRequest)
	} else if uri, present := util.GetStringParam(r, "uri"); !present {
		msg = "Cannot add. Invalid URI"
		w.WriteHeader(http.StatusBadRequest)
	} else {
		pp := initPortProbes(r)
		uri = strings.ToLower(uri)
		pp.lock.Lock()
		if isReadiness {
			pp.ReadinessProbe = uri
			pp.ReadinessCount = 0
		} else if isLiveness {
			pp.LivenessProbe = uri
			pp.LivenessCount = 0
		}
		pp.lock.Unlock()
		msg = fmt.Sprintf("Port [%s] Probe [%s] URI [%s] added, count reset", pp.Port, probeType, uri)
	}
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

// setProbeStatus sets the status that the readiness or liveness probe of the request's port returns, optionally for a limited number of calls.
func setProbeStatus(w http.ResponseWriter, r *http.Request) {
	msg := ""
	probeType := util.GetStringParamValue(r, "type")
	isReadiness := strings.EqualFold(probeType, "readiness")
	isLiveness := strings.EqualFold(probeType, "liveness")
	if !isReadiness && !isLiveness {
		msg = "Cannot add. Invalid probe type"
		w.WriteHeader(http.StatusBadRequest)
	} else if statuses, count, present := util.GetStatusParam(r); !present {
		msg = "Cannot set. Invalid status code"
		w.WriteHeader(http.StatusBadRequest)
	} else {
		pp := initPortProbes(r)
		status := 200
		if statuses[0] > 0 {
			status = statuses[0]
		}
		if count <= 0 {
			count = -1
		}
		pp.lock.Lock()
		if isReadiness {
			pp.ReadinessStatus = status
			pp.ReadinessStatusRemainingCount = count
		} else if isLiveness {
			pp.LivenessStatus = status
			pp.LivenessStatusRemainingCount = count
		}
		pp.lock.Unlock()
		if count > 0 {
			msg = fmt.Sprintf("Port [%s] Probe [%s] Status [%d] set with remaining count [%d]", pp.Port, probeType, status, count)
		} else {
			msg = fmt.Sprintf("Port [%s] Probe [%s] Status [%d] set", pp.Port, probeType, status)
		}
	}
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

// getProbes reports the probes and their counts for the request's port as JSON.
func getProbes(w http.ResponseWriter, r *http.Request) {
	pp := initPortProbes(r)
	pp.lock.RLock()
	util.WriteJsonPayload(w, pp)
	pp.lock.RUnlock()
	util.AddLogMessage("Reporting probe counts", r)
}

// clearProbeCounts resets the readiness and liveness counts of the request's port.
func clearProbeCounts(w http.ResponseWriter, r *http.Request) {
	pp := initPortProbes(r)
	pp.lock.Lock()
	pp.ReadinessCount = 0
	pp.LivenessCount = 0
	pp.lock.Unlock()
	msg := fmt.Sprintf("Port [%s] Counts Cleared", pp.Port)
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

// middlewareFunc answers readiness and liveness probe requests with their configured status and count headers, and passes other requests to next.
func middlewareFunc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pp := initPortProbes(r)
		if IsReadinessProbe(r) {
			metrics.UpdateRequestCount("readinessProbe")
			pp.lock.Lock()
			status := pp.ReadinessStatus
			if pp.ReadinessStatusRemainingCount > 0 {
				pp.ReadinessStatusRemainingCount--
				if pp.ReadinessStatusRemainingCount == 0 {
					pp.ReadinessStatusRemainingCount = -1
					pp.ReadinessStatus = 200
				}
			}
			pp.ReadinessCount++
			if pp.ReadinessCount == 0 {
				pp.ReadinessOverflowCount++
			}
			// What is reported is what this probe made it, and not what the probes that come after it do.
			count, overflowCount, probe := pp.ReadinessCount, pp.ReadinessOverflowCount, pp.ReadinessProbe
			pp.lock.Unlock()
			util.CopyHeaders("Readiness-Request", r, w, nil, true, true, false)
			w.Header().Add(HeaderReadinessRequestCount, fmt.Sprint(count))
			w.Header().Add(HeaderReadinessOverflowCount, fmt.Sprint(overflowCount))
			w.WriteHeader(status)
			util.SetHeadersSent(r, true)
			util.AddLogMessage(fmt.Sprintf("Serving Readiness Probe: [%s]", probe), r)
		} else if IsLivenessProbe(r) {
			metrics.UpdateRequestCount("livenessProbe")
			pp.lock.Lock()
			status := pp.LivenessStatus
			if pp.LivenessStatusRemainingCount > 0 {
				pp.LivenessStatusRemainingCount--
				if pp.LivenessStatusRemainingCount == 0 {
					pp.LivenessStatusRemainingCount = -1
					pp.LivenessStatus = 200
				}
			}
			pp.LivenessCount++
			if pp.LivenessCount == 0 {
				pp.LivenessOverflowCount++
			}
			count, overflowCount, probe := pp.LivenessCount, pp.LivenessOverflowCount, pp.LivenessProbe
			pp.lock.Unlock()
			util.CopyHeaders("Liveness-Request", r, w, nil, true, true, false)
			w.Header().Add(HeaderLivenessRequestCount, fmt.Sprint(count))
			w.Header().Add(HeaderLivenessOverflowCount, fmt.Sprint(overflowCount))
			w.WriteHeader(status)
			util.SetHeadersSent(r, true)
			util.AddLogMessage(fmt.Sprintf("Serving Liveness Probe: [%s]", probe), r)
		} else if next != nil {
			next.ServeHTTP(w, r)
		}
	})
}
