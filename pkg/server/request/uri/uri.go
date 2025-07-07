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

package uri

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	. "goto/pkg/constants"
	"goto/pkg/events"
	"goto/pkg/server/intercept"
	"goto/pkg/server/middleware"
	"goto/pkg/server/response/trigger"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

type DelayConfig struct {
	URI   string
	Glob  bool
	Delay time.Duration
	Times int
	lock  sync.RWMutex
}

type URIStatusConfig struct {
	URI      string
	Glob     bool
	Statuses []int
	Times    int
	lock     sync.RWMutex
}

var (
	Middleware         = middleware.NewMiddleware("uri", setRoutes, middlewareFunc)
	uriCountsByPort    map[string]map[string]int
	uriStatusByPort    map[string]map[string]interface{}
	uriDelayByPort     map[string]map[string]interface{}
	trackURICallCounts bool
	uriLock            sync.RWMutex
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	uriRouter := util.PathRouter(r, "/uri")
	util.AddRouteQWithPort(uriRouter, "/set/status={status}", setStatus, "uri", "POST", "PUT")
	util.AddRouteQWithPort(uriRouter, "/set/delay={delay}", setDelay, "uri", "POST", "PUT")
	util.AddRouteWithPort(uriRouter, "/counts/enable", enableURICallCounts, "POST", "PUT")
	util.AddRouteWithPort(uriRouter, "/counts/disable", disableURICallCounts, "POST", "PUT")
	util.AddRouteWithPort(uriRouter, "/counts", getURICallCounts, "GET")
	util.AddRouteWithPort(uriRouter, "/counts/clear", clearURICallCounts, "POST")
	util.AddRouteWithPort(uriRouter, "", getURIConfigs, "GET")
}

func initPort(r *http.Request) string {
	port := util.GetRequestOrListenerPort(r)
	uriLock.Lock()
	defer uriLock.Unlock()
	if uriCountsByPort == nil {
		uriCountsByPort = map[string]map[string]int{}
		uriStatusByPort = map[string]map[string]interface{}{}
		uriDelayByPort = map[string]map[string]interface{}{}
	}
	if uriCountsByPort[port] == nil {
		uriCountsByPort[port] = map[string]int{}
		uriStatusByPort[port] = map[string]interface{}{}
		uriDelayByPort[port] = map[string]interface{}{}
	}
	return port
}

func setStatus(w http.ResponseWriter, r *http.Request) {
	port := initPort(r)
	msg := ""
	uriLock.Lock()
	defer uriLock.Unlock()
	if uri, present := util.GetStringParam(r, "uri"); present {
		uri = strings.ToLower(uri)
		glob := false
		matchURI := uri
		if strings.HasSuffix(uri, "*") {
			matchURI = strings.ReplaceAll(uri, "*", "")
			glob = true
		}
		if statusCodes, times, ok := util.GetStatusParam(r); ok && statusCodes[0] > 0 {
			uriStatusByPort[port][matchURI] = &URIStatusConfig{URI: matchURI, Glob: glob, Statuses: statusCodes, Times: times}
			if times > 0 {
				msg = fmt.Sprintf("Port [%s] URI [%s] status set to %d for next [%d] calls", port, uri, statusCodes, times)
				events.SendRequestEvent("URI Status Configured", msg, r)
			} else {
				msg = fmt.Sprintf("Port [%s] URI [%s] status set to %d forever", port, uri, statusCodes)
				events.SendRequestEvent("URI Status Configured", msg, r)
			}
		} else {
			delete(uriStatusByPort[port], matchURI)
			msg = fmt.Sprintf("Port [%s] URI [%s] status cleared", port, uri)
			events.SendRequestEvent("URI Status Cleared", msg, r)
		}
		w.WriteHeader(http.StatusOK)
	} else {
		msg = "Cannot add. Invalid URI"
		w.WriteHeader(http.StatusBadRequest)
	}
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func setDelay(w http.ResponseWriter, r *http.Request) {
	port := initPort(r)
	msg := ""
	uriLock.Lock()
	defer uriLock.Unlock()
	if uri, present := util.GetStringParam(r, "uri"); present {
		uri = strings.ToLower(uri)
		vars := mux.Vars(r)
		delayParam := strings.Split(vars["delay"], ":")
		var delay time.Duration
		times := 0
		if len(delayParam[0]) > 0 {
			if d, err := time.ParseDuration(delayParam[0]); err == nil {
				delay = d
				if len(delayParam) > 1 {
					t, _ := strconv.ParseInt(delayParam[1], 10, 32)
					times = int(t)
				}
			}
		}
		glob := false
		matchURI := uri
		if strings.HasSuffix(uri, "*") {
			matchURI = strings.ReplaceAll(uri, "*", "")
			glob = true
		}
		if delay > 0 {
			uriDelayByPort[port][matchURI] = &DelayConfig{URI: matchURI, Glob: glob, Delay: delay, Times: times}
			if times > 0 {
				msg = fmt.Sprintf("Port [%s] will delay next [%d] requests for URI [%s] by [%s]", port, times, uri, delay)
				events.SendRequestEvent("URI Delay Configured", msg, r)
			} else {
				msg = fmt.Sprintf("Port [%s] will delay all requests for URI [%s] by [%s] forever", port, uri, delay)
				events.SendRequestEvent("URI Delay Configured", msg, r)
			}
		} else {
			delete(uriDelayByPort[port], matchURI)
			msg = fmt.Sprintf("Port [%s] URI [%s] delay cleared", port, uri)
			events.SendRequestEvent("URI Delay Cleared", msg, r)
		}
		w.WriteHeader(http.StatusOK)
	} else {
		msg = "Cannot add. Invalid URI"
		w.WriteHeader(http.StatusBadRequest)
	}
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func getURIConfigs(w http.ResponseWriter, r *http.Request) {
	port := initPort(r)
	uriLock.RLock()
	defer uriLock.RUnlock()
	result := util.ToJSONText(map[string]interface{}{
		"uriDelayByPort":  uriDelayByPort[port],
		"uriStatusByPort": uriStatusByPort[port],
		"uriCountsByPort": uriCountsByPort[port],
	})
	util.AddLogMessage(fmt.Sprintf("Port [%s] Reporting URI Configs: %s", port, result), r)
	fmt.Fprintf(w, "%s\n", result)
}

func getURICallCounts(w http.ResponseWriter, r *http.Request) {
	port := initPort(r)
	uriLock.RLock()
	defer uriLock.RUnlock()
	result := util.ToJSONText(uriCountsByPort[port])
	util.AddLogMessage(fmt.Sprintf("Port [%s] Reporting URI Call Counts: %s", port, result), r)
	fmt.Fprintf(w, "%s\n", result)
}

func clearURICallCounts(w http.ResponseWriter, r *http.Request) {
	port := initPort(r)
	uriLock.Lock()
	defer uriLock.Unlock()
	uriCountsByPort[port] = map[string]int{}
	msg := fmt.Sprintf("Port [%s] URI Call Counts Cleared", port)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
	events.SendRequestEvent("URI Call Counts Cleared", msg, r)
}

func enableURICallCounts(w http.ResponseWriter, r *http.Request) {
	uriLock.Lock()
	trackURICallCounts = true
	uriLock.Unlock()
	msg := fmt.Sprintf("Port [%s] URI Call Counts Enabled", util.GetRequestOrListenerPort(r))
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
	events.SendRequestEvent("URI Call Counts Enabled", msg, r)
}

func disableURICallCounts(w http.ResponseWriter, r *http.Request) {
	uriLock.Lock()
	trackURICallCounts = false
	uriLock.Unlock()
	msg := fmt.Sprintf("Port [%s] URI Call Counts Disabled", util.GetRequestOrListenerPort(r))
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
	events.SendRequestEvent("URI Call Counts Disabled", msg, r)
}

func hasURIConfig(r *http.Request, uriMap map[string]map[string]interface{}) (present bool, glob bool, config interface{}) {
	port := initPort(r)
	uri := strings.ToLower(r.URL.Path)
	uriLock.RLock()
	defer uriLock.RUnlock()
	portURIMap := uriMap[port]
	if portURIMap == nil {
		return false, false, nil
	}
	if uriConfig, present := portURIMap[uri]; present {
		return true, false, uriConfig
	}
	var uriConfig interface{}
	for k, v := range portURIMap {
		if strings.HasPrefix(uri, k) {
			uriConfig = v
			break
		}
	}
	if uriConfig != nil {
		return true, true, uriConfig
	}
	return false, false, nil
}

func HasURIStatus(r *http.Request) bool {
	if present, glob, v := hasURIConfig(r, uriStatusByPort); present {
		uriStatus := v.(*URIStatusConfig)
		return len(uriStatus.Statuses) > 0 && uriStatus.Statuses[0] > 0 && uriStatus.Times >= 0 && (!glob || uriStatus.Glob)
	}
	return false
}

func GetURIStatus(r *http.Request) *URIStatusConfig {
	if present, glob, v := hasURIConfig(r, uriStatusByPort); present {
		uriStatus := v.(*URIStatusConfig)
		if len(uriStatus.Statuses) > 0 && uriStatus.Statuses[0] > 0 && uriStatus.Times >= 0 && (!glob || uriStatus.Glob) {
			return uriStatus
		}
	}
	return nil
}

func GetURIDelay(r *http.Request) *DelayConfig {
	if present, glob, v := hasURIConfig(r, uriDelayByPort); present {
		delayConfig := v.(*DelayConfig)
		if delayConfig.Delay > 0 && delayConfig.Times >= 0 && (!glob || delayConfig.Glob) {
			return delayConfig
		}
	}
	return nil
}

func HasAnyURIStatusOrDelay() bool {
	return len(uriStatusByPort) > 0 || len(uriDelayByPort) > 0
}

func checkAndSkip(w http.ResponseWriter, r *http.Request, next http.Handler) (*URIStatusConfig, *DelayConfig, bool) {
	if util.IsKnownNonTraffic(r) || !HasAnyURIStatusOrDelay() {
		if next != nil {
			next.ServeHTTP(w, r)
		}
		return nil, nil, true
	}
	uriStatus := GetURIStatus(r)
	uriDelay := GetURIDelay(r)
	if uriStatus == nil && uriDelay == nil {
		if next != nil {
			next.ServeHTTP(w, r)
		}
		return nil, nil, true
	}
	return uriStatus, uriDelay, false
}

func computeURIStatus(uri, port string, uriStatus *URIStatusConfig, r *http.Request, w http.ResponseWriter) int {
	statusToReport := 0
	statusTimesLeft := 0
	if uriStatus != nil {
		uriStatus.lock.Lock()
		if len(uriStatus.Statuses) == 1 {
			statusToReport = uriStatus.Statuses[0]
		} else {
			statusToReport = util.RandomFrom(uriStatus.Statuses)
		}
		statusTimesLeft = uriStatus.Times

		if statusToReport > 0 {
			if uriStatus.Times >= 1 {
				uriStatus.Times--
				if uriStatus.Times == 0 {
					uriLock.Lock()
					delete(uriStatusByPort[port], uriStatus.URI)
					uriLock.Unlock()
				} else {
					w.Header().Add(HeaderGotoURIStatusRemaining, strconv.Itoa(uriStatus.Times))
				}
			}
		}
		uriStatus.lock.Unlock()
		msg := ""
		if statusTimesLeft-1 > 0 {
			msg = fmt.Sprintf("Reporting URI status: [%d] for URI [%s]. Remaining status count [%d].", statusToReport, uri, statusTimesLeft-1)
		} else {
			msg = fmt.Sprintf("Reporting URI status: [%d] for URI [%s].", statusToReport, uri)
		}
		util.AddLogMessage(msg, r)
		events.SendRequestEvent("URI Status Applied", msg, r)
	}
	return statusToReport
}

func applyURIDelay(uri, port string, uriDelay *DelayConfig, r *http.Request, w http.ResponseWriter) {
	delay := time.Duration(0)
	delayTimesLeft := 0
	if uriDelay != nil {
		uriDelay.lock.Lock()
		delay = uriDelay.Delay
		delayTimesLeft = uriDelay.Times
		if delay > 0 {
			if uriDelay.Times >= 1 {
				uriDelay.Times--
				if uriDelay.Times == 0 {
					uriLock.Lock()
					delete(uriDelayByPort[port], uriDelay.URI)
					uriLock.Unlock()
				}
			}
		}
		uriDelay.lock.Unlock()
		if delay > 0 {
			msg := fmt.Sprintf("Delaying URI [%s] by [%s]. Remaining delay count [%d]", uri, delay, delayTimesLeft-1)
			util.AddLogMessage(msg, r)
			events.SendRequestEvent("URI Delay Applied", msg, r)
			w.Header().Add(HeaderGotoResponseDelay, delay.String())
			time.Sleep(delay)
		}
	}
}

func middlewareFunc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uriStatus, uriDelay, skipped := checkAndSkip(w, r, next)
		if skipped {
			return
		}
		uri := strings.ToLower(r.URL.Path)
		port := initPort(r)
		if trackURICallCounts {
			uriLock.Lock()
			uriCountsByPort[port][uri]++
			uriLock.Unlock()
		}
		statusToReport := computeURIStatus(uri, port, uriStatus, r, w)
		applyURIDelay(uri, port, uriDelay, r, w)
		if next != nil {
			next.ServeHTTP(w, r)
		}
		irw := util.GetInterceptResponseWriter(r).(*intercept.InterceptResponseWriter)
		if statusToReport > 0 {
			irw.StatusCode = statusToReport
			w.Header().Add(HeaderGotoURIStatus, strconv.Itoa(statusToReport))
		}
		if irw.StatusCode == 0 {
			irw.StatusCode = http.StatusOK
		}
		util.UpdateTrafficEventStatusCode(r, irw.StatusCode)
		trigger.RunTriggers(r, irw, irw.StatusCode)
	})
}
