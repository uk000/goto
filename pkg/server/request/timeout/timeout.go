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

package timeout

import (
	"fmt"
	"net/http"
	"strings"
	"sync"

	"goto/pkg/events"
	"goto/pkg/metrics"
	"goto/pkg/server/middleware"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

type TimeoutData struct {
	ConnectionClosed int `json:"connectionClosed"`
	RequestCompleted int `json:"requestCompleted"`
}

type TimeoutTracking struct {
	headersMap  map[string]map[string]*TimeoutData
	allTimeouts *TimeoutData
	lock        sync.RWMutex
}

var (
	Middleware            = middleware.NewMiddleware("timeout", setRoutes, middlewareFunc)
	timeoutTrackingByPort = map[string]*TimeoutTracking{}
	timeoutTrackingLock   sync.RWMutex
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	timeoutRouter := util.PathRouter(r, "/timeout")
	util.AddRoute(timeoutRouter, "/track/headers/{headers}", trackHeaders, "PUT", "POST")
	util.AddRoute(timeoutRouter, "/track/all", trackAll, "PUT", "POST")
	util.AddRoute(timeoutRouter, "/track/clear", clearTimeoutTracking, "POST")
	util.AddRoute(timeoutRouter, "/status", reportTimeoutTracking, "GET")
}

func (tt *TimeoutTracking) init() {
	tt.lock.Lock()
	defer tt.lock.Unlock()
	if tt.headersMap == nil {
		tt.headersMap = map[string]map[string]*TimeoutData{}
	}
}

func (tt *TimeoutTracking) addHeaders(w http.ResponseWriter, r *http.Request) {
	tt.lock.Lock()
	defer tt.lock.Unlock()
	msg := ""
	if param, present := util.GetStringParam(r, "headers"); present {
		headers := strings.Split(param, ",")
		for _, h := range headers {
			tt.headersMap[h] = map[string]*TimeoutData{}
		}
		msg = fmt.Sprintf("Will track request timeout for Headers %s", headers)
		events.SendRequestEvent("Timeout Tracking Headers Added", msg, r)
		w.WriteHeader(http.StatusOK)
	} else {
		msg = "Cannot track. Invalid header"
		w.WriteHeader(http.StatusBadRequest)
	}
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func (tt *TimeoutTracking) trackAll(w http.ResponseWriter, r *http.Request) {
	tt.lock.Lock()
	defer tt.lock.Unlock()
	tt.allTimeouts = &TimeoutData{}
	w.WriteHeader(http.StatusOK)
	msg := "Activated timeout tracking for all requests"
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
	events.SendRequestEvent("All Timeout Tracking Enabled", msg, r)
}

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

func trackHeaders(w http.ResponseWriter, r *http.Request) {
	getTimeoutTracking(r).addHeaders(w, r)
}

func trackAll(w http.ResponseWriter, r *http.Request) {
	getTimeoutTracking(r).trackAll(w, r)
}

func clearTimeoutTracking(w http.ResponseWriter, r *http.Request) {
	timeoutTrackingLock.Lock()
	defer timeoutTrackingLock.Unlock()
	listenerPort := util.GetRequestOrListenerPort(r)
	if tt := timeoutTrackingByPort[listenerPort]; tt != nil {
		timeoutTrackingByPort[listenerPort] = &TimeoutTracking{}
		timeoutTrackingByPort[listenerPort].init()
	}
	w.WriteHeader(http.StatusOK)
	msg := "Timeout Tracking Headers Cleared"
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
	events.SendRequestEvent(msg, "", r)
}

func reportTimeoutTracking(w http.ResponseWriter, r *http.Request) {
	util.AddLogMessage("Reporting timeout tracking counts", r)
	tt := getTimeoutTracking(r)
	timeoutTrackingLock.RLock()
	defer timeoutTrackingLock.RUnlock()
	result := map[string]interface{}{}
	result["headers"] = tt.headersMap
	result["all"] = tt.allTimeouts
	output := util.ToJSONText(result)
	util.AddLogMessage(output, r)
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, output)
}

func middlewareFunc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs := util.GetRequestStore(r)
		if rs.IsKnownNonTraffic {
			if next != nil {
				next.ServeHTTP(w, r)
			}
			return
		}
		trackedHeaders := [][]string{}
		tt := getTimeoutTracking(r)
		timeoutTrackingLock.RLock()
		for header, valueMap := range tt.headersMap {
			headerValue := r.Header.Get(header)
			if len(headerValue) > 0 {
				timeoutData, present := valueMap[headerValue]
				if !present {
					timeoutData = &TimeoutData{}
					timeoutTrackingLock.RUnlock()
					timeoutTrackingLock.Lock()
					valueMap[headerValue] = timeoutData
					timeoutTrackingLock.Unlock()
					timeoutTrackingLock.RLock()
				}
				trackedHeaders = append(trackedHeaders, []string{header, headerValue})
			}
		}
		if len(trackedHeaders) > 0 || tt.allTimeouts != nil {
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
					events.SendRequestEvent("Timeout Tracked", r.RequestURI, r)
				}
				timeoutTrackingLock.Lock()
				for _, kv := range trackedHeaders {
					tt.headersMap[kv[0]][kv[1]].ConnectionClosed += connectionClosed
					tt.headersMap[kv[0]][kv[1]].RequestCompleted += requestCompleted
				}
				if tt.allTimeouts != nil {
					tt.allTimeouts.ConnectionClosed += connectionClosed
					tt.allTimeouts.RequestCompleted += requestCompleted
				}
				timeoutTrackingLock.Unlock()
			}(trackedHeaders)
		}
		timeoutTrackingLock.RUnlock()
		if next != nil {
			next.ServeHTTP(w, r)
		}
	})
}
