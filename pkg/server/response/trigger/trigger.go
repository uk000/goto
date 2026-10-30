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

package trigger

import (
	"encoding/json"
	"errors"
	"fmt"
	"goto/pkg/invocation"
	"goto/pkg/metrics"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/gorilla/mux"
)

type TriggerHTTPTarget struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
	SendID  bool              `json:"sendID"`
}

// TriggerTarget is a target that is triggered by the responses that match its URIs, headers and statuses. The lock of
// the target guards the fields that change while the target is in use: Enabled, MatchCount, TriggerCount and
// PipeCallbacks.
type TriggerTarget struct {
	Name              string                  `json:"name"`
	HTTPTarget        *TriggerHTTPTarget      `json:"httpTarget"`
	Pipe              bool                    `json:"pipe"`
	PipeCallbacks     map[string]PipeCallback `json:"-"`
	Enabled           bool                    `json:"enabled"`
	TriggerURIs       []string                `json:"triggerURIs"`
	TriggerHeaders    map[string]string       `json:"triggerHeaders"`
	TriggerStatuses   []int                   `json:"triggerStatuses"`
	StartFrom         int                     `json:"startFrom"`
	StopAt            int                     `json:"stopAt"`
	MatchCount        int                     `json:"matchCount"`
	TriggerCount      int                     `json:"triggerCount"`
	triggerURIRegexps map[string]*regexp.Regexp
	lock              sync.RWMutex
}

// Trigger has the targets of a port. Its lock guards the maps. When locks are held together, they are taken in the
// order Trigger.lock, triggerLock, TriggerTarget.lock.
type Trigger struct {
	Targets          map[string]*TriggerTarget         `json:"targets"`
	TargetsByURIs    map[string][]string               `json:"targetsByURIs"`
	TargetsByHeaders map[string]map[string]interface{} `json:"targetsByHeaders"`
	TargetsByStatus  map[int][]string                  `json:"targetsByStatus"`
	TriggerResults   map[string]map[int]int            `json:"triggerResults"`
	lock             sync.RWMutex
}

type PipeCallback func(target, source string, port int, r *http.Request, statusCode int, responseHeaders http.Header)

type triggerTargetJSON TriggerTarget
type triggerJSON Trigger

var (
	Middleware        = middleware.NewMiddleware("trigger", setRoutes, nil)
	portTriggers      = map[string]*Trigger{}
	allTriggerTargets = map[string]*TriggerTarget{}
	triggerLock       sync.RWMutex
)

// MarshalJSON reports the target under its lock, since its counts change while triggers are run.
func (tt *TriggerTarget) MarshalJSON() ([]byte, error) {
	tt.lock.RLock()
	defer tt.lock.RUnlock()
	return json.Marshal((*triggerTargetJSON)(tt))
}

// MarshalJSON reports the targets under the lock of the port.
func (t *Trigger) MarshalJSON() ([]byte, error) {
	t.lock.RLock()
	defer t.lock.RUnlock()
	return json.Marshal((*triggerJSON)(t))
}

func setRoutes(r *mux.Router) {
	triggerRouter := util.PathRouter(r, "/triggers")
	util.AddRoute(triggerRouter, "/add", addTrigger, "POST")
	util.AddRoute(triggerRouter, "/{trigger}/remove", removeTrigger, "PUT", "POST")
	util.AddRoute(triggerRouter, "/{trigger}/enable", enableOrDisableTrigger, "PUT", "POST")
	util.AddRoute(triggerRouter, "/{trigger}/disable", enableOrDisableTrigger, "PUT", "POST")
	util.AddRoute(triggerRouter, "/{triggers}/invoke", invokeTriggers, "POST")
	util.AddRoute(triggerRouter, "/clear", clearTriggers, "POST")
	util.AddRoute(triggerRouter, "/counts", getTriggerCounts)
	util.AddRoute(triggerRouter, "/pipes", getTriggerPipes)
	util.AddRoute(triggerRouter, "", getTriggers)
}

func getPortTrigger(r *http.Request) *Trigger {
	listenerPort := util.GetRequestOrListenerPort(r)
	triggerLock.RLock()
	trigger := portTriggers[listenerPort]
	triggerLock.RUnlock()
	if trigger == nil {
		created := &Trigger{}
		created.init()
		triggerLock.Lock()
		// Requests that arrive together for a port without triggers all get here, and they must end up with the same
		// Trigger, or else the targets that they add would be lost with the ones that didn't get to stay.
		if trigger = portTriggers[listenerPort]; trigger == nil {
			trigger = created
			portTriggers[listenerPort] = trigger
		}
		triggerLock.Unlock()
	}
	return trigger
}

func addTrigger(w http.ResponseWriter, r *http.Request) {
	getPortTrigger(r).addTrigger(w, r)
}

func removeTrigger(w http.ResponseWriter, r *http.Request) {
	getPortTrigger(r).removeTrigger(w, r)
}

func enableOrDisableTrigger(w http.ResponseWriter, r *http.Request) {
	getPortTrigger(r).enableOrDisableTrigger(w, r)
}

func clearTriggers(w http.ResponseWriter, r *http.Request) {
	listenerPort := util.GetRequestOrListenerPort(r)
	cleared := &Trigger{}
	cleared.init()
	triggerLock.Lock()
	old := portTriggers[listenerPort]
	portTriggers[listenerPort] = cleared
	triggerLock.Unlock()
	if old != nil {
		old.lock.RLock()
		targets := make(map[string]*TriggerTarget, len(old.Targets))
		for name, tt := range old.Targets {
			targets[name] = tt
		}
		old.lock.RUnlock()
		triggerLock.Lock()
		for name, tt := range targets {
			if allTriggerTargets[name] == tt {
				delete(allTriggerTargets, name)
			}
		}
		triggerLock.Unlock()
	}
	w.WriteHeader(http.StatusOK)
	msg := fmt.Sprintf("Port [%s] Triggers Cleared", listenerPort)
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func getTriggerCounts(w http.ResponseWriter, r *http.Request) {
	t := getPortTrigger(r)
	util.AddLogMessage(fmt.Sprintf("Port [%s] Get trigger counts", util.GetRequestOrListenerPort(r)), r)
	counts := map[string]map[int]int{}
	t.lock.RLock()
	for name, results := range t.TriggerResults {
		counts[name] = make(map[int]int, len(results))
		for status, count := range results {
			counts[name][status] = count
		}
	}
	t.lock.RUnlock()
	util.WriteJsonPayload(w, counts)
}

func getTriggerPipes(w http.ResponseWriter, r *http.Request) {
	util.AddLogMessage(fmt.Sprintf("Port [%s] Get trigger pipes", util.GetRequestOrListenerPort(r)), r)
	triggerLock.RLock()
	triggers := make([]*Trigger, 0, len(portTriggers))
	for _, t := range portTriggers {
		triggers = append(triggers, t)
	}
	triggerLock.RUnlock()
	triggerPipes := map[string][]string{}
	for _, t := range triggers {
		t.lock.RLock()
		for target, tt := range t.Targets {
			tt.lock.RLock()
			for pipe := range tt.PipeCallbacks {
				triggerPipes[target] = append(triggerPipes[target], pipe)
			}
			tt.lock.RUnlock()
		}
		t.lock.RUnlock()
	}
	for _, pipes := range triggerPipes {
		sort.Strings(pipes)
	}
	util.WriteJsonPayload(w, triggerPipes)
}

func getTriggers(w http.ResponseWriter, r *http.Request) {
	t := getPortTrigger(r)
	util.AddLogMessage(fmt.Sprintf("Port [%s] Get triggers", util.GetRequestOrListenerPort(r)), r)
	util.WriteJsonPayload(w, t)
}

func invokeTriggers(w http.ResponseWriter, r *http.Request) {
	t := getPortTrigger(r)
	targets := t.getRequestedTriggers(r)
	if len(targets) > 0 {
		httpTargets := map[string]*TriggerTarget{}
		pipeSources := map[string]*TriggerTarget{}
		for name, tt := range targets {
			if tt.HTTPTarget != nil {
				httpTargets[name] = tt
			}
			if tt.Pipe {
				pipeSources[name] = tt
			}
		}
		port := util.GetRequestOrListenerPortNum(r)
		responses := t.invokeHTTPTargets(httpTargets, port, w.Header(), r)
		t.invokePipeSources(pipeSources, port, w.Header(), r, http.StatusOK)
		w.WriteHeader(http.StatusOK)
		util.AddLogMessage(fmt.Sprintf("Port [%s] Trigger targets invoked", util.GetRequestOrListenerPort(r)), r)
		fmt.Fprintln(w, util.ToJSONText(responses))
	} else {
		w.WriteHeader(http.StatusNotFound)
		util.AddLogMessage("Triggers not found", r)
		fmt.Fprintln(w, "Triggers not found")
	}
}

func (t *Trigger) init() {
	t.lock.Lock()
	defer t.lock.Unlock()
	if t.Targets == nil {
		t.Targets = map[string]*TriggerTarget{}
		t.TargetsByURIs = map[string][]string{}
		t.TargetsByHeaders = map[string]map[string]interface{}{}
		t.TargetsByStatus = map[int][]string{}
		t.TriggerResults = map[string]map[int]int{}
	}
}

// buildURIRegexps builds the regexps that match the URIs of a target. It builds them without registering a route for
// them on the router of the server, which is in use while the targets are added.
func buildURIRegexps(uris []string) (map[string]*regexp.Regexp, error) {
	regexps := map[string]*regexp.Regexp{}
	for _, uri := range uris {
		finalURI, re, err := util.BuildURIRegexp(uri, true)
		if err != nil {
			return nil, fmt.Errorf("invalid trigger URI [%s]: %s", uri, err.Error())
		}
		regexps[finalURI] = re
	}
	return regexps, nil
}

func appendUnique[T comparable](values []T, value T) []T {
	for _, v := range values {
		if v == value {
			return values
		}
	}
	return append(values, value)
}

// without returns the names without the given one. It makes a new slice, since the one that it is given is shared
// with the maps of the Trigger.
func without(names []string, name string) []string {
	remaining := make([]string, 0, len(names))
	for _, n := range names {
		if n != name {
			remaining = append(remaining, n)
		}
	}
	return remaining
}

// index lists the target by what it is triggered by. A target that has URIs is listed by them, and the statuses and
// headers that it also has are checked when its URIs match. One that has statuses is listed by them, and its headers
// are checked when a status matches. One that has only headers is listed by them.
func (t *Trigger) index(tt *TriggerTarget) {
	switch {
	case len(tt.triggerURIRegexps) > 0:
		for uri := range tt.triggerURIRegexps {
			t.TargetsByURIs[uri] = appendUnique(t.TargetsByURIs[uri], tt.Name)
		}
	case len(tt.TriggerStatuses) > 0:
		for _, status := range tt.TriggerStatuses {
			t.TargetsByStatus[status] = appendUnique(t.TargetsByStatus[status], tt.Name)
		}
	case len(tt.TriggerHeaders) > 0:
		for h, hv := range tt.TriggerHeaders {
			if t.TargetsByHeaders[h] == nil {
				t.TargetsByHeaders[h] = map[string]interface{}{}
			}
			names, _ := t.TargetsByHeaders[h][hv].([]string)
			t.TargetsByHeaders[h][hv] = appendUnique(names, tt.Name)
		}
	}
}

func (t *Trigger) addTrigger(w http.ResponseWriter, r *http.Request) {
	tt := &TriggerTarget{}
	var err error
	if err = util.ReadJsonPayload(r, tt); err == nil && tt.Name == "" {
		err = errors.New("trigger name is required")
	}
	if err == nil && tt.HTTPTarget != nil {
		_, err = tt.toInvocationSpec(nil, nil)
	}
	if err == nil {
		tt.triggerURIRegexps, err = buildURIRegexps(tt.TriggerURIs)
	}
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Invalid trigger: %s\n", err.Error())
		return
	}
	t.lock.Lock()
	replaced := t.unsafeDeleteTrigger(tt.Name)
	t.Targets[tt.Name] = tt
	t.index(tt)
	t.lock.Unlock()
	triggerLock.Lock()
	if replaced != nil && allTriggerTargets[tt.Name] == replaced {
		delete(allTriggerTargets, tt.Name)
	}
	allTriggerTargets[tt.Name] = tt
	triggerLock.Unlock()
	msg := fmt.Sprintf("Port [%s] Added trigger: %s", util.GetRequestOrListenerPort(r), tt.Name)
	util.AddLogMessage(msg, r)
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, msg)
}

// unsafeDeleteTrigger removes the target from the maps of the Trigger, which the caller must hold the lock of. It
// returns the target that it removed.
func (t *Trigger) unsafeDeleteTrigger(targetName string) *TriggerTarget {
	removed := t.Targets[targetName]
	delete(t.Targets, targetName)
	for uri, names := range t.TargetsByURIs {
		if names = without(names, targetName); len(names) == 0 {
			delete(t.TargetsByURIs, uri)
		} else {
			t.TargetsByURIs[uri] = names
		}
	}
	for h, hvTargets := range t.TargetsByHeaders {
		for hv, targets := range hvTargets {
			names, _ := targets.([]string)
			if names = without(names, targetName); len(names) == 0 {
				delete(hvTargets, hv)
			} else {
				hvTargets[hv] = names
			}
		}
		if len(hvTargets) == 0 {
			delete(t.TargetsByHeaders, h)
		}
	}
	for status, names := range t.TargetsByStatus {
		if names = without(names, targetName); len(names) == 0 {
			delete(t.TargetsByStatus, status)
		} else {
			t.TargetsByStatus[status] = names
		}
	}
	return removed
}

func (t *Trigger) deleteTrigger(targetName string) {
	t.lock.Lock()
	removed := t.unsafeDeleteTrigger(targetName)
	t.lock.Unlock()
	if removed != nil {
		triggerLock.Lock()
		if allTriggerTargets[targetName] == removed {
			delete(allTriggerTargets, targetName)
		}
		triggerLock.Unlock()
	}
}

func (t *Trigger) removeTrigger(w http.ResponseWriter, r *http.Request) {
	if tt := t.getRequestedTrigger(r); tt != nil {
		t.deleteTrigger(tt.Name)
		msg := fmt.Sprintf("Port [%s] Trigger Removed: %s", util.GetRequestOrListenerPort(r), tt.Name)
		util.AddLogMessage(msg, r)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, msg)
	} else {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintln(w, "No triggers")
	}
}

func (t *Trigger) enableOrDisableTrigger(w http.ResponseWriter, r *http.Request) {
	name := util.GetStringParamValue(r, "trigger")
	// The action is the last segment of the path. The name of the trigger, or the query, may well have "enable" in it.
	enable := strings.HasSuffix(strings.TrimRight(r.URL.Path, "/"), "/enable")
	action := "enabled"
	if !enable {
		action = "disabled"
	}
	if tt := t.getRequestedTrigger(r); tt != nil {
		tt.lock.Lock()
		tt.Enabled = enable
		tt.lock.Unlock()
		msg := fmt.Sprintf("Port [%s] Trigger [%s] %s", util.GetRequestOrListenerPort(r), name, action)
		util.AddLogMessage(msg, r)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, msg)
	} else {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Port [%s] Trigger [%s] not found\n", util.GetRequestOrListenerPort(r), name)
	}
}

func (t *Trigger) getRequestedTrigger(r *http.Request) *TriggerTarget {
	t.lock.RLock()
	defer t.lock.RUnlock()
	if tname, present := util.GetStringParam(r, "trigger"); present {
		return t.Targets[tname]
	}
	return nil
}

func (t *Trigger) getRequestedTriggers(r *http.Request) map[string]*TriggerTarget {
	t.lock.RLock()
	defer t.lock.RUnlock()
	targets := map[string]*TriggerTarget{}
	if tnamesParam, present := util.GetStringParam(r, "triggers"); present {
		for _, tname := range strings.Split(tnamesParam, ",") {
			if target, found := t.Targets[tname]; found {
				targets[target.Name] = target
			}
		}
	} else {
		for name, target := range t.Targets {
			targets[name] = target
		}
	}
	return targets
}

// prepareTargetHeaders resolves the headers of the target. A header value like {request.uri}, {request.headers} or
// {name} is replaced with the URI of the request, its headers, or the value of the response header of that name.
func (tt *TriggerTarget) prepareTargetHeaders(r *http.Request, responseHeaders http.Header) map[string]string {
	headers := map[string]string{}
	for h, hv := range tt.HTTPTarget.Headers {
		if strings.HasPrefix(hv, "{") && strings.HasSuffix(hv, "}") {
			captureKey := strings.TrimLeft(hv, "{")
			captureKey = strings.TrimRight(captureKey, "}")
			if strings.EqualFold(captureKey, "request.uri") {
				hv = r.RequestURI
			} else if strings.EqualFold(captureKey, "request.headers") {
				hv = util.ToJSONText(r.Header)
			} else if captureValue := responseHeaders.Get(captureKey); captureValue != "" {
				hv = captureValue
			}
		}
		headers[h] = hv
	}
	return headers
}

func (tt *TriggerTarget) toInvocationSpec(r *http.Request, responseHeaders http.Header) (*invocation.InvocationSpec, error) {
	is := &invocation.InvocationSpec{}
	is.Name = tt.Name
	is.Method = tt.HTTPTarget.Method
	is.URL = tt.HTTPTarget.URL
	is.Headers = tt.HTTPTarget.Headers
	is.Body = tt.HTTPTarget.Body
	is.SendID = tt.HTTPTarget.SendID
	is.Replicas = 1
	if r != nil {
		is.Headers = tt.prepareTargetHeaders(r, responseHeaders)
	}
	return is, invocation.ValidateSpec(is)
}

func (t *Trigger) invokeHTTPTargets(targets map[string]*TriggerTarget, port int, responseHeaders http.Header, r *http.Request) []*invocation.InvocationResult {
	if len(targets) == 0 {
		return nil
	}
	responses := []*invocation.InvocationResult{}
	for _, target := range targets {
		target.lock.Lock()
		target.TriggerCount++
		target.lock.Unlock()
		metrics.UpdateTriggerCount(target.Name)
		is, _ := target.toInvocationSpec(r, responseHeaders)
		if tracker, err := invocation.RegisterInvocation(port, is); err == nil {
			results := invocation.StartInvocation(tracker, true)
			responses = append(responses, results...)
		} else {
			log.Println(err.Error())
		}
	}
	for _, response := range responses {
		if response.Response == nil {
			response.Response = &invocation.InvocationResultResponse{}
		}
		if response.Response.StatusCode == 0 {
			response.Response.StatusCode = 503
		}
		t.lock.Lock()
		if t.TriggerResults[response.TargetName] == nil {
			t.TriggerResults[response.TargetName] = map[int]int{}
		}
		t.TriggerResults[response.TargetName][response.Response.StatusCode]++
		t.lock.Unlock()
	}
	return responses
}

func (t *Trigger) invokePipeSources(pipeSources map[string]*TriggerTarget, port int, responseHeaders http.Header, r *http.Request, statusCode int) {
	for name, tt := range pipeSources {
		tt.lock.Lock()
		if tt.HTTPTarget == nil {
			// A target that also has an HTTP target is counted when that is invoked.
			tt.TriggerCount++
		}
		callbacks := make(map[string]PipeCallback, len(tt.PipeCallbacks))
		for source, callback := range tt.PipeCallbacks {
			callbacks[source] = callback
		}
		tt.lock.Unlock()
		for source, callback := range callbacks {
			callback(name, source, port, r, statusCode, responseHeaders)
		}
	}
}

func (t *Trigger) findMatchingTriggers(r *http.Request, statusCode int) (httpTargets, pipeSources map[string]*TriggerTarget) {
	uri := strings.ToLower(r.RequestURI)
	triggerTargets := map[string]*TriggerTarget{}
	t.lock.RLock()
	defer t.lock.RUnlock()
	for _, targets := range t.TargetsByURIs {
		for _, name := range targets {
			if tt := t.Targets[name]; tt != nil {
				matched := false
				for _, re := range tt.triggerURIRegexps {
					matched = re.MatchString(uri)
					if matched && len(tt.TriggerStatuses) > 0 {
						matched = util.IsInIntArray(statusCode, tt.TriggerStatuses)
					}
					if matched && len(tt.TriggerHeaders) > 0 {
						matched = util.MatchAllHeaders(r.Header, tt.TriggerHeaders)
					}
					if matched {
						triggerTargets[name] = tt
						break
					}
				}
			}
		}
	}
	for _, name := range t.TargetsByStatus[statusCode] {
		if triggerTargets[name] != nil {
			continue
		}
		if tt := t.Targets[name]; tt != nil {
			matched := true
			if len(tt.TriggerHeaders) > 0 {
				matched = util.MatchAllHeaders(r.Header, tt.TriggerHeaders)
			}
			if matched {
				triggerTargets[name] = tt
			}
		}
	}
	if targets := util.GetIfAnyHeaderMatched(r.Header, t.TargetsByHeaders); targets != nil {
		for _, name := range targets.([]string) {
			if tt := t.Targets[name]; tt != nil {
				triggerTargets[name] = tt
			}
		}
	}
	httpTargets = map[string]*TriggerTarget{}
	pipeSources = map[string]*TriggerTarget{}
	for _, tt := range triggerTargets {
		tt.lock.Lock()
		tt.MatchCount++
		fire := tt.Enabled && tt.MatchCount >= tt.StartFrom && (tt.StopAt == 0 || tt.MatchCount <= tt.StopAt)
		tt.lock.Unlock()
		if fire {
			if tt.HTTPTarget != nil {
				httpTargets[tt.Name] = tt
			}
			if tt.Pipe {
				pipeSources[tt.Name] = tt
			}
		}
	}
	return
}

// RunTriggers runs the targets that the response matches. The targets are invoked in the background, after the
// handler is done with the response, so they get a copy of its headers.
func RunTriggers(r *http.Request, w http.ResponseWriter, statusCode int) {
	if !util.IsAdminRequest(r) && !util.IsMetricsRequest(r) {
		t := getPortTrigger(r)
		httpTargets, pipeSources := t.findMatchingTriggers(r, statusCode)
		responseHeaders := w.Header().Clone()
		port := util.GetRequestOrListenerPortNum(r)
		if len(httpTargets) > 0 {
			go t.invokeHTTPTargets(httpTargets, port, responseHeaders, r)
		}
		if len(pipeSources) > 0 {
			go t.invokePipeSources(pipeSources, port, responseHeaders, r, statusCode)
		}
	}
}

func RegisterPipeCallback(target, pipe string, callback PipeCallback) {
	triggerLock.Lock()
	defer triggerLock.Unlock()
	if tt := allTriggerTargets[target]; tt != nil {
		tt.lock.Lock()
		if tt.PipeCallbacks == nil {
			tt.PipeCallbacks = map[string]PipeCallback{}
		}
		tt.PipeCallbacks[pipe] = callback
		tt.lock.Unlock()
	} else {
		log.Printf("Trigger target %s not found for pipe callback registration", target)
	}
}
