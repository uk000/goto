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
	"goto/pkg/types"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

// DelayConfig and URIStatusConfig are shared by the requests that match them. URI and Glob never change after
// creation, the other fields are guarded by the lock of the config. Locks are taken in the order uriLock, then the
// lock of a config, and never the other way round.
type DelayConfig struct {
	URI   string
	Glob  bool
	Delay time.Duration
	Times int
	done  bool // set once the calls of a config with a limit are used up
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
	uriCountsByPort    = map[string]map[string]int{}
	uriStatusByPort    = map[string]map[string]any{}
	uriDelayByPort     = map[string]map[string]any{}
	trackURICallCounts bool
	uriLock            sync.RWMutex
)

func setRoutes(r *mux.Router) {
	uriRouter := util.PathRouter(r, "/uri")
	// util.AddRouteWithMultiQ(uriRouter, "/reroute", setStatus, [][]string{{"from", "to"}}, "POST", "PUT")
	util.AddRouteQ(uriRouter, "/set/status={status}", setStatus, "uri", "POST", "PUT")
	util.AddRouteQ(uriRouter, "/set/delay={delay}", setDelay, "uri", "POST", "PUT")
	util.AddRoute(uriRouter, "/counts/enable", enableURICallCounts, "POST", "PUT")
	util.AddRoute(uriRouter, "/counts/disable", disableURICallCounts, "POST", "PUT")
	util.AddRoute(uriRouter, "/counts", getURICallCounts, "GET")
	util.AddRoute(uriRouter, "/counts/clear", clearURICallCounts, "POST")
	util.AddRoute(uriRouter, "", getURIConfigs, "GET")
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
		statusCodes, times, ok := util.GetStatusParam(r)
		if rawStatus := util.GetStringParamValue(r, "status"); rawStatus != "" && (!ok || !validStatuses(statusCodes)) {
			// A status was given but can't be used. Clearing the config instead would hide the mistake.
			msg = fmt.Sprintf("Invalid status [%s]", rawStatus)
			w.WriteHeader(http.StatusBadRequest)
		} else {
			if ok && statusCodes[0] > 0 {
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
		}
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
	result := util.ToJSONText(map[string]interface{}{
		"uriDelayByPort":  snapshotConfigs(uriDelayByPort[port]),
		"uriStatusByPort": snapshotConfigs(uriStatusByPort[port]),
		"uriCountsByPort": uriCountsByPort[port],
	})
	uriLock.RUnlock()
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

// validStatuses tells if the given statuses can be used. A single 0 is valid too, as it clears the status of a URI.
func validStatuses(statuses []int) bool {
	if len(statuses) == 1 && statuses[0] == 0 {
		return true
	}
	if len(statuses) == 0 {
		return false
	}
	for _, status := range statuses {
		// net/http panics when it's asked to write a status that is out of this range
		if status < 100 || status > 999 {
			return false
		}
	}
	return true
}

type uriStatusView struct {
	URI      string
	Glob     bool
	Statuses []int
	Times    int
}

type delayView struct {
	URI   string
	Glob  bool
	Delay time.Duration
	Times int
}

// snapshotConfigs returns copies of the configs of a port, which can be encoded while requests update the originals.
// It must be called with uriLock held.
func snapshotConfigs(configs map[string]any) map[string]any {
	snapshot := make(map[string]any, len(configs))
	for uri, config := range configs {
		switch c := config.(type) {
		case *URIStatusConfig:
			c.lock.RLock()
			snapshot[uri] = uriStatusView{URI: c.URI, Glob: c.Glob, Statuses: append([]int(nil), c.Statuses...), Times: c.Times}
			c.lock.RUnlock()
		case *DelayConfig:
			c.lock.RLock()
			snapshot[uri] = delayView{URI: c.URI, Glob: c.Glob, Delay: c.Delay, Times: c.Times}
			c.lock.RUnlock()
		}
	}
	return snapshot
}

func isGlobConfig(config any) bool {
	switch c := config.(type) {
	case *URIStatusConfig:
		return c.Glob
	case *DelayConfig:
		return c.Glob
	}
	return false
}

// removeConfig removes a config that was used up, unless it has been replaced by another one in the meantime.
func removeConfig(configs map[string]map[string]any, port, uri string, config any) {
	uriLock.Lock()
	defer uriLock.Unlock()
	if configs[port][uri] == config {
		delete(configs[port], uri)
	}
}

// hasURIConfig finds the config of the request's URI: either the one of exactly that URI, or else the one of the
// longest glob URI that is a prefix of it.
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
	longest := -1
	for k, v := range portURIMap {
		if len(k) > longest && strings.HasPrefix(uri, k) && isGlobConfig(v) {
			uriConfig = v
			longest = len(k)
		}
	}
	if uriConfig != nil {
		return true, true, uriConfig
	}
	return false, false, nil
}

// HasURIStatus tells if a URI status applies to the request: one that the middleware has applied to it, or else one
// that is configured for its URI. The middleware may have used up the last count of the status and removed it by
// the time that the rest of the chain asks, which is why it remembers the status that it applied in the request
// store (AppliedURIStatus). The status middleware, which runs inside this one, relies on that to leave the status of
// the response to the URI status, and to count it.
func HasURIStatus(r *http.Request) bool {
	if util.GetRequestStore(r).AppliedURIStatus > 0 {
		return true
	}
	return GetURIStatus(r) != nil
}

func GetURIStatus(r *http.Request) *URIStatusConfig {
	if present, glob, v := hasURIConfig(r, uriStatusByPort); present {
		uriStatus := v.(*URIStatusConfig)
		uriStatus.lock.RLock()
		defer uriStatus.lock.RUnlock()
		if len(uriStatus.Statuses) > 0 && uriStatus.Statuses[0] > 0 && (uriStatus.Times > 0 || uriStatus.Times == -1) && (!glob || uriStatus.Glob) {
			return uriStatus
		}
	}
	return nil
}

func GetURIDelay(r *http.Request) *DelayConfig {
	if present, glob, v := hasURIConfig(r, uriDelayByPort); present {
		delayConfig := v.(*DelayConfig)
		delayConfig.lock.RLock()
		defer delayConfig.lock.RUnlock()
		if delayConfig.Delay > 0 && delayConfig.Times >= 0 && !delayConfig.done && (!glob || delayConfig.Glob) {
			return delayConfig
		}
	}
	return nil
}

// HasAnyURIStatusOrDelay tells if a status or a delay is configured for any URI on any port.
func HasAnyURIStatusOrDelay() bool {
	uriLock.RLock()
	defer uriLock.RUnlock()
	for _, configs := range uriStatusByPort {
		if len(configs) > 0 {
			return true
		}
	}
	for _, configs := range uriDelayByPort {
		if len(configs) > 0 {
			return true
		}
	}
	return false
}

func checkAndSkip(w http.ResponseWriter, r *http.Request, next http.Handler) (*URIStatusConfig, *DelayConfig, bool) {
	rs := util.GetRequestStore(r)
	if rs.IsKnownNonTraffic || !HasAnyURIStatusOrDelay() {
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
	if uriStatus == nil {
		return 0
	}
	statusToReport := 0
	statusTimesLeft := 0
	used := false
	uriStatus.lock.Lock()
	if uriStatus.Times == 0 {
		// Used up by other requests that found the same config before it was removed.
		uriStatus.lock.Unlock()
		return 0
	}
	if len(uriStatus.Statuses) == 1 {
		statusToReport = uriStatus.Statuses[0]
	} else {
		statusToReport = types.RandomFrom(uriStatus.Statuses)
	}
	statusTimesLeft = uriStatus.Times

	if statusToReport > 0 {
		if uriStatus.Times >= 1 {
			uriStatus.Times--
			if uriStatus.Times == 0 {
				used = true
			} else {
				w.Header().Add(HeaderGotoURIStatusRemaining, strconv.Itoa(uriStatus.Times))
			}
		}
	}
	uriStatus.lock.Unlock()
	if used {
		removeConfig(uriStatusByPort, port, uriStatus.URI, uriStatus)
	}
	msg := ""
	if statusTimesLeft-1 > 0 {
		msg = fmt.Sprintf("Reporting URI status: [%d] for URI [%s]. Remaining status count [%d].", statusToReport, uri, statusTimesLeft-1)
	} else {
		msg = fmt.Sprintf("Reporting URI status: [%d] for URI [%s].", statusToReport, uri)
	}
	util.AddLogMessage(msg, r)
	events.SendRequestEvent("URI Status Applied", msg, r)
	return statusToReport
}

func applyURIDelay(uri, port string, uriDelay *DelayConfig, r *http.Request, w http.ResponseWriter) {
	if uriDelay == nil {
		return
	}
	used := false
	uriDelay.lock.Lock()
	if uriDelay.done {
		// Used up by other requests that found the same config before it was removed.
		uriDelay.lock.Unlock()
		return
	}
	delay := uriDelay.Delay
	delayTimesLeft := uriDelay.Times
	if delay > 0 {
		if uriDelay.Times >= 1 {
			uriDelay.Times--
			if uriDelay.Times == 0 {
				uriDelay.done = true
				used = true
			}
		}
	}
	uriDelay.lock.Unlock()
	if used {
		removeConfig(uriDelayByPort, port, uriDelay.URI, uriDelay)
	}
	if delay > 0 {
		msg := fmt.Sprintf("Delaying URI [%s] by [%s]. Remaining delay count [%d]", uri, delay, delayTimesLeft-1)
		util.AddLogMessage(msg, r)
		events.SendRequestEvent("URI Delay Applied", msg, r)
		w.Header().Add(HeaderGotoResponseDelay, delay.String())
		time.Sleep(delay)
	}
}

// countURICall counts the call for its URI, if counting is enabled.
func countURICall(r *http.Request) {
	uriLock.RLock()
	enabled := trackURICallCounts
	uriLock.RUnlock()
	if !enabled {
		return
	}
	port := initPort(r)
	uri := strings.ToLower(r.URL.Path)
	uriLock.Lock()
	defer uriLock.Unlock()
	if trackURICallCounts {
		if uriCountsByPort[port] == nil {
			uriCountsByPort[port] = map[string]int{}
		}
		uriCountsByPort[port][uri]++
	}
}

func middlewareFunc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Calls are counted for all URIs, not only for the ones that have a status or a delay.
		if !util.GetRequestStore(r).IsKnownNonTraffic {
			countURICall(r)
		}
		uriStatus, uriDelay, skipped := checkAndSkip(w, r, next)
		if skipped {
			return
		}
		uri := strings.ToLower(r.URL.Path)
		port := initPort(r)
		rs := util.GetRequestStore(r)
		statusToReport := computeURIStatus(uri, port, uriStatus, r, w)
		// Told before the rest of the chain runs, see HasURIStatus.
		rs.AppliedURIStatus = statusToReport
		applyURIDelay(uri, port, uriDelay, r, w)
		if next != nil {
			next.ServeHTTP(w, r)
		}
		irw := rs.InterceptResponseWriter.(*intercept.InterceptResponseWriter)
		if statusToReport > 0 {
			irw.StatusCode = statusToReport
			w.Header().Add(HeaderGotoURIStatus, strconv.Itoa(statusToReport))
		}
		if irw.StatusCode == 0 {
			irw.StatusCode = http.StatusOK
		}
		rs.StatusCode = irw.StatusCode
		util.UpdateTrafficEventStatusCode(r, irw.StatusCode)
		trigger.RunTriggers(r, irw, irw.StatusCode)
	})
}
