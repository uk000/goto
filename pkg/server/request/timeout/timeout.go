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

package timeout

import (
	"fmt"
	"net/http"
	"strings"
	"sync"

	"goto/pkg/metrics"
	"goto/pkg/server/middleware"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

type TimeoutData struct {
	ConnectionClosed int `json:"connectionClosed"`
	RequestCompleted int `json:"requestCompleted"`
}

// TimeoutTracking holds the timeout tracking of a port. All access to its fields, and to the TimeoutData it points
// to, is guarded by timeoutTrackingLock, which is shared by all ports. A lock of its own would not be enough, since
// the end-of-request goroutines of the middleware update counts of tracking that may be replaced or cleared meanwhile.
type TimeoutTracking struct {
	headersMap  map[string]map[string]*TimeoutData
	allTimeouts *TimeoutData
}

var (
	Middleware            = middleware.NewMiddleware("timeout", setRoutes, middlewareFunc)
	timeoutTrackingByPort = map[string]*TimeoutTracking{}
	timeoutTrackingLock   sync.RWMutex
)

// setRoutes registers the timeout tracking routes under /timeout.
func setRoutes(r *mux.Router) {
	timeoutRouter := util.PathRouter(r, "/timeout")
	util.AddRoute(timeoutRouter, "/track/headers/{headers}", trackHeaders, "PUT", "POST")
	util.AddRoute(timeoutRouter, "/track/all", trackAll, "PUT", "POST")
	util.AddRoute(timeoutRouter, "/track/clear", clearTimeoutTracking, "POST")
	util.AddRoute(timeoutRouter, "/status", reportTimeoutTracking, "GET")
	util.AddRoute(timeoutRouter, "", reportTimeoutTracking, "GET")
}

// init must be called before the tracking is shared, or with timeoutTrackingLock held.
func (tt *TimeoutTracking) init() {
	if tt.headersMap == nil {
		tt.headersMap = map[string]map[string]*TimeoutData{}
	}
}

// addHeaders starts tracking request timeouts for the comma-separated headers given, keeping the counts of headers already tracked.
func (tt *TimeoutTracking) addHeaders(w http.ResponseWriter, r *http.Request) {
	msg := ""
	if param, present := util.GetStringParam(r, "headers"); present {
		headers := strings.Split(param, ",")
		timeoutTrackingLock.Lock()
		for _, h := range headers {
			// Counts of a header that is already tracked are kept. Replacing them would also drop the entries that
			// requests in flight are about to update.
			if tt.headersMap[h] == nil {
				tt.headersMap[h] = map[string]*TimeoutData{}
			}
		}
		timeoutTrackingLock.Unlock()
		msg = fmt.Sprintf("Will track request timeout for Headers %s", headers)
		w.WriteHeader(http.StatusOK)
	} else {
		msg = "Cannot track. Invalid header"
		w.WriteHeader(http.StatusBadRequest)
	}
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

// trackAll starts tracking request timeouts for all requests, resetting their counts.
func (tt *TimeoutTracking) trackAll(w http.ResponseWriter, r *http.Request) {
	timeoutTrackingLock.Lock()
	tt.allTimeouts = &TimeoutData{}
	timeoutTrackingLock.Unlock()
	w.WriteHeader(http.StatusOK)
	msg := "Activated timeout tracking for all requests"
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

// getTimeoutTracking returns the timeout tracking of the request's port, creating it if missing.
func getTimeoutTracking(r *http.Request) *TimeoutTracking {
	listenerPort := util.GetRequestOrListenerPort(r)
	timeoutTrackingLock.Lock()
	defer timeoutTrackingLock.Unlock()
	tt, present := timeoutTrackingByPort[listenerPort]
	if !present {
		tt = &TimeoutTracking{}
		tt.init()
		timeoutTrackingByPort[listenerPort] = tt
	}
	return tt
}

// trackHeaders starts tracking request timeouts for the given headers on the request's port.
func trackHeaders(w http.ResponseWriter, r *http.Request) {
	getTimeoutTracking(r).addHeaders(w, r)
}

// trackAll starts tracking request timeouts for all requests on the request's port.
func trackAll(w http.ResponseWriter, r *http.Request) {
	getTimeoutTracking(r).trackAll(w, r)
}

// clearTimeoutTracking replaces the port's timeout tracking with a fresh, empty one.
func clearTimeoutTracking(w http.ResponseWriter, r *http.Request) {
	listenerPort := util.GetRequestOrListenerPort(r)
	timeoutTrackingLock.Lock()
	if tt := timeoutTrackingByPort[listenerPort]; tt != nil {
		timeoutTrackingByPort[listenerPort] = &TimeoutTracking{}
		timeoutTrackingByPort[listenerPort].init()
	}
	timeoutTrackingLock.Unlock()
	w.WriteHeader(http.StatusOK)
	msg := "Timeout Tracking Headers Cleared"
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

// reportTimeoutTracking reports the per-header and all-request timeout counts of the port as JSON.
func reportTimeoutTracking(w http.ResponseWriter, r *http.Request) {
	util.AddLogMessage("Reporting timeout tracking counts", r)
	tt := getTimeoutTracking(r)
	result := map[string]interface{}{}
	timeoutTrackingLock.RLock()
	result["headers"] = tt.headersMap
	result["all"] = tt.allTimeouts
	output := util.ToJSONText(result)
	timeoutTrackingLock.RUnlock()
	util.AddLogMessage(output, r)
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, output)
}

// middlewareFunc, for tracked requests, counts in the background whether the client connection closed or the request completed, per tracked header value and for all requests.
func middlewareFunc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs := util.GetRequestStore(r)
		if rs.IsKnownNonTraffic {
			if next != nil {
				next.ServeHTTP(w, r)
			}
			return
		}
		tt := getTimeoutTracking(r)
		trackedHeaders, trackAll := findTrackedHeaders(tt, r)
		if len(trackedHeaders) > 0 || trackAll {
			notify := w.(http.CloseNotifier).CloseNotify()
			go func(trackedHeaders [][]string) {
				connectionClosed := 0
				requestCompleted := 0
				select {
				case <-notify:
					connectionClosed++
				case <-r.Context().Done():
					requestCompleted++
				}
				if connectionClosed > 0 {
					metrics.UpdateRequestCount("timeout")
				}
				timeoutTrackingLock.Lock()
				for _, kv := range trackedHeaders {
					// The tracking may have been changed while the request was in flight.
					if timeoutData := tt.headersMap[kv[0]][kv[1]]; timeoutData != nil {
						timeoutData.ConnectionClosed += connectionClosed
						timeoutData.RequestCompleted += requestCompleted
					}
				}
				if tt.allTimeouts != nil {
					tt.allTimeouts.ConnectionClosed += connectionClosed
					tt.allTimeouts.RequestCompleted += requestCompleted
				}
				timeoutTrackingLock.Unlock()
			}(trackedHeaders)
		}
		if next != nil {
			next.ServeHTTP(w, r)
		}
	})
}

// findTrackedHeaders returns the [header, value] pairs of the request that are tracked, adding the values that are
// seen for the first time, and tells if all requests are tracked.
func findTrackedHeaders(tt *TimeoutTracking, r *http.Request) (trackedHeaders [][]string, trackAll bool) {
	timeoutTrackingLock.RLock()
	newValues := [][]string{}
	for header, valueMap := range tt.headersMap {
		if headerValue := r.Header.Get(header); len(headerValue) > 0 {
			trackedHeaders = append(trackedHeaders, []string{header, headerValue})
			if valueMap[headerValue] == nil {
				newValues = append(newValues, []string{header, headerValue})
			}
		}
	}
	trackAll = tt.allTimeouts != nil
	timeoutTrackingLock.RUnlock()
	if len(newValues) > 0 {
		timeoutTrackingLock.Lock()
		for _, kv := range newValues {
			if valueMap := tt.headersMap[kv[0]]; valueMap != nil && valueMap[kv[1]] == nil {
				valueMap[kv[1]] = &TimeoutData{}
			}
		}
		timeoutTrackingLock.Unlock()
	}
	return
}
