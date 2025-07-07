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

package trigger

import (
	"fmt"
	"goto/pkg/events"
	"goto/pkg/invocation"
	"goto/pkg/metrics"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/gorilla/mux"
)

type TriggerHTTPTarget struct {
	Method  string     `json:"method"`
	URL     string     `json:"url"`
	Headers [][]string `json:"headers"`
	Body    string     `json:"body"`
	SendID  bool       `json:"sendID"`
}

type TriggerTarget struct {
	Name              string             `json:"name"`
	HTTPTarget        *TriggerHTTPTarget `json:"httpTarget"`
	Pipe              bool               `json:"pipe"`
	PipeCallbacks     map[string]PipeCallback
	Enabled           bool       `json:"enabled"`
	TriggerURIs       []string   `json:"triggerURIs"`
	TriggerHeaders    [][]string `json:"triggerHeaders"`
	TriggerStatuses   []int      `json:"triggerStatuses"`
	StartFrom         int        `json:"startFrom"`
	StopAt            int        `json:"stopAt"`
	MatchCount        int        `json:"matchCount"`
	TriggerCount      int        `json:"triggerCount"`
	triggerURIRegexps map[string]*regexp.Regexp
	lock              sync.RWMutex
}

type Trigger struct {
	Targets          map[string]*TriggerTarget         `json:"targets"`
	TargetsByURIs    map[string][]string               `json:"targetsByURIs"`
	TargetsByHeaders map[string]map[string]interface{} `json:"targetsByHeaders"`
	TargetsByStatus  map[int][]string                  `json:"targetsByStatus"`
	TriggerResults   map[string]map[int]int            `json:"triggerResults"`
	lock             sync.RWMutex
}

type PipeCallback func(target, source string, port int, r *http.Request, statusCode int, responseHeaders http.Header)

var (
	rootRouter        *mux.Router
	Middleware        = middleware.NewMiddleware("trigger", setRoutes, nil)
	portTriggers      = map[string]*Trigger{}
	allTriggerTargets = map[string]*TriggerTarget{}
	triggerLock       sync.RWMutex
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	rootRouter = root
	triggerRouter := util.PathRouter(r, "/triggers")
	util.AddRouteWithPort(triggerRouter, "/add", addTrigger, "POST")
	util.AddRouteWithPort(triggerRouter, "/{trigger}/remove", removeTrigger, "PUT", "POST")
	util.AddRouteWithPort(triggerRouter, "/{trigger}/enable", enableOrDisableTrigger, "PUT", "POST")
	util.AddRouteWithPort(triggerRouter, "/{trigger}/disable", enableOrDisableTrigger, "PUT", "POST")
	util.AddRouteWithPort(triggerRouter, "/{triggers}/invoke", invokeTriggers, "POST")
	util.AddRouteWithPort(triggerRouter, "/clear", clearTriggers, "POST")
	util.AddRouteWithPort(triggerRouter, "/counts", getTriggerCounts)
	util.AddRouteWithPort(triggerRouter, "/pipes", getTriggerPipes)
	util.AddRouteWithPort(triggerRouter, "", getTriggers)
}

func getPortTrigger(r *http.Request) *Trigger {
	triggerLock.RLock()
	listenerPort := util.GetRequestOrListenerPort(r)
	trigger := portTriggers[listenerPort]
	triggerLock.RUnlock()
	if trigger == nil {
		triggerLock.Lock()
		defer triggerLock.Unlock()
		trigger = &Trigger{}
		trigger.init()
		portTriggers[listenerPort] = trigger
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
	triggerLock.Lock()
	defer triggerLock.Unlock()
	if t := portTriggers[listenerPort]; t != nil {
		for name, _ := range t.Targets {
			delete(allTriggerTargets, name)
		}
	}
	portTriggers[listenerPort] = &Trigger{}
	portTriggers[listenerPort].init()
	w.WriteHeader(http.StatusOK)
	msg := fmt.Sprintf("Port [%s] Triggers Cleared", util.GetRequestOrListenerPort(r))
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
	events.SendRequestEvent("Triggers Cleared", msg, r)
}

func getTriggerCounts(w http.ResponseWriter, r *http.Request) {
	t := getPortTrigger(r)
	triggerLock.RLock()
	defer triggerLock.RUnlock()
	util.AddLogMessage(fmt.Sprintf("Port [%s] Get trigger counts", util.GetRequestOrListenerPort(r)), r)
	util.WriteJsonPayload(w, t.TriggerResults)
}

func getTriggerPipes(w http.ResponseWriter, r *http.Request) {
	triggerLock.RLock()
	defer triggerLock.RUnlock()
	util.AddLogMessage(fmt.Sprintf("Port [%s] Get trigger pipes", util.GetRequestOrListenerPort(r)), r)
	triggerPipes := map[string][]string{}
	for _, t := range portTriggers {
		for target, tt := range t.Targets {
			for pipe, _ := range tt.PipeCallbacks {
				triggerPipes[target] = append(triggerPipes[target], pipe)
			}
		}
	}
	util.WriteJsonPayload(w, triggerPipes)
}

func getTriggers(w http.ResponseWriter, r *http.Request) {
	t := getPortTrigger(r)
	triggerLock.RLock()
	defer triggerLock.RUnlock()
	util.AddLogMessage(fmt.Sprintf("Port [%s] Get triggers", util.GetRequestOrListenerPort(r)), r)
	util.WriteJsonPayload(w, t)
}

func invokeTriggers(w http.ResponseWriter, r *http.Request) {
	t := getPortTrigger(r)
	targets := t.getRequestedTriggers(r)
	if len(targets) > 0 {
		httpTargets := map[string]*TriggerTarget{}
		pipeSources := map[string]*TriggerTarget{}
		for name, t := range targets {
			if t.HTTPTarget != nil {
				httpTargets[name] = t
			}
			if t.Pipe {
				pipeSources[name] = t
			}
		}
		responses := t.invokeHTTPTargets(httpTargets, w, r)
		t.invokePipeSources(pipeSources, r, http.StatusOK, w)
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

func (t *Trigger) addTrigger(w http.ResponseWriter, r *http.Request) {
	tt := &TriggerTarget{}
	var err error
	if err = util.ReadJsonPayload(r, tt); err == nil {
		if tt.HTTPTarget != nil {
			_, err = tt.toInvocationSpec(nil, nil)
		}
	}
	if err == nil {
		t.deleteTrigger(tt.Name)
		t.lock.Lock()
		t.Targets[tt.Name] = tt
		if len(tt.TriggerURIs) > 0 {
			tt.triggerURIRegexps = map[string]*regexp.Regexp{}
			for _, uri := range tt.TriggerURIs {
				if finalURI, re, _, _, err := util.GetURIRegexpAndRoute(uri, rootRouter); err == nil {
					t.TargetsByURIs[finalURI] = append(t.TargetsByURIs[finalURI], tt.Name)
					tt.triggerURIRegexps[finalURI] = re
				}
			}
		} else if len(tt.TriggerHeaders) > 0 {
			for _, hv := range tt.TriggerHeaders {
				if len(hv) == 0 {
					continue
				}
				h := hv[0]
				if t.TargetsByHeaders[h] == nil {
					t.TargetsByHeaders[h] = map[string]interface{}{}
				}
				v := ""
				if len(hv) > 1 {
					v = hv[1]
				}
				if t.TargetsByHeaders[h][v] == nil {
					t.TargetsByHeaders[h][v] = []string{}
				}
				targets := t.TargetsByHeaders[h][v].([]string)
				t.TargetsByHeaders[h][v] = append(targets, tt.Name)
			}
		} else if len(tt.TriggerStatuses) > 0 {
			for _, status := range tt.TriggerStatuses {
				t.TargetsByStatus[status] = append(t.TargetsByStatus[status], tt.Name)
			}
		}
		t.lock.Unlock()
		triggerLock.Lock()
		allTriggerTargets[tt.Name] = tt
		triggerLock.Unlock()
		msg := fmt.Sprintf("Port [%s] Added trigger: %s", util.GetRequestOrListenerPort(r), tt.Name)
		util.AddLogMessage(msg, r)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, msg)
		events.SendRequestEventJSON("Trigger Added", tt.Name, tt, r)
	} else {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Invalid trigger: %s\n", err.Error())
	}
}

func (t *Trigger) deleteTrigger(targetName string) {
	t.lock.Lock()
	defer t.lock.Unlock()
	delete(t.Targets, targetName)
	for uri, targets := range t.TargetsByURIs {
		for i, name := range targets {
			if name == targetName {
				targets = append(targets[:i], targets[i+1:]...)
			}
		}
		if len(targets) == 0 {
			delete(t.TargetsByURIs, uri)
		}
	}
	for h, hvTargets := range t.TargetsByHeaders {
		for hv := range hvTargets {
			targets := hvTargets[hv].([]string)
			for i, name := range targets {
				if name == targetName {
					targets = append(targets[:i], targets[i+1:]...)
				}
			}
			if len(targets) == 0 {
				delete(hvTargets, hv)
			}
		}
		if len(hvTargets) == 0 {
			delete(t.TargetsByHeaders, h)
		}
	}

	for status, targets := range t.TargetsByStatus {
		for i, name := range targets {
			if name == targetName {
				targets = append(targets[:i], targets[i+1:]...)
			}
		}
		if len(targets) == 0 {
			delete(t.TargetsByStatus, status)
		}
	}
	triggerLock.Lock()
	defer triggerLock.Unlock()
	delete(allTriggerTargets, targetName)
}

func (t *Trigger) removeTrigger(w http.ResponseWriter, r *http.Request) {
	if tt := t.getRequestedTrigger(r); tt != nil {
		t.deleteTrigger(tt.Name)
		msg := fmt.Sprintf("Port [%s] Trigger Removed: %s", util.GetRequestOrListenerPort(r), tt.Name)
		util.AddLogMessage(msg, r)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, msg)
		events.SendRequestEvent("Trigger Removed", msg, r)
	} else {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintln(w, "No triggers")
	}
}

func (t *Trigger) enableOrDisableTrigger(w http.ResponseWriter, r *http.Request) {
	name := util.GetStringParamValue(r, "trigger")
	enable := strings.Contains(r.RequestURI, "enable")
	action := "enabled"
	if !enable {
		action = "disabled"
	}
	if tt := t.getRequestedTrigger(r); tt != nil {
		t.lock.Lock()
		tt.Enabled = enable
		t.lock.Unlock()
		msg := fmt.Sprintf("Port [%s] Trigger [%s] %s", util.GetRequestOrListenerPort(r), name, action)
		util.AddLogMessage(msg, r)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, msg)
		events.SendRequestEvent("Trigger Enabled/Disabled", msg, r)
	} else {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintln(w, fmt.Sprintf("Port [%s] Trigger [%s] not found", util.GetRequestOrListenerPort(r), name))
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
		tnames := strings.Split(tnamesParam, ",")
		for _, tname := range tnames {
			if target, found := t.Targets[tname]; found {
				targets[target.Name] = target
			}
		}
	} else {
		targets = t.Targets
	}
	return targets
}

func (tt *TriggerTarget) prepareTargetHeaders(r *http.Request, w http.ResponseWriter) [][]string {
	var headers [][]string = [][]string{}
	for _, kv := range tt.HTTPTarget.Headers {
		if strings.HasPrefix(kv[1], "{") && strings.HasSuffix(kv[1], "}") {
			captureKey := strings.TrimLeft(kv[1], "{")
			captureKey = strings.TrimRight(captureKey, "}")
			if strings.EqualFold(captureKey, "request.uri") {
				kv[1] = r.RequestURI
			} else if strings.EqualFold(captureKey, "request.headers") {
				kv[1] = util.ToJSONText(r.Header)
			} else if captureValue := w.Header().Get(captureKey); captureValue != "" {
				kv[1] = captureValue
			}
		}
		headers = append(headers, []string{kv[0], kv[1]})
	}
	return headers
}

func (tt *TriggerTarget) toInvocationSpec(r *http.Request, w http.ResponseWriter) (*invocation.InvocationSpec, error) {
	is := &invocation.InvocationSpec{}
	is.Name = tt.Name
	is.Method = tt.HTTPTarget.Method
	is.URL = tt.HTTPTarget.URL
	is.Headers = tt.HTTPTarget.Headers
	is.Body = tt.HTTPTarget.Body
	is.SendID = tt.HTTPTarget.SendID
	is.Replicas = 1
	if r != nil {
		is.Headers = tt.prepareTargetHeaders(r, w)
	}
	return is, invocation.ValidateSpec(is)
}

func (t *Trigger) invokeHTTPTargets(targets map[string]*TriggerTarget, w http.ResponseWriter, r *http.Request) []*invocation.InvocationResult {
	responses := []*invocation.InvocationResult{}
	if len(targets) > 0 {
		for _, target := range targets {
			target.lock.Lock()
			target.TriggerCount++
			target.lock.Unlock()
			events.SendRequestEventJSON("Trigger Target Invoked", target.Name, target, r)
			metrics.UpdateTriggerCount(target.Name)
			is, _ := target.toInvocationSpec(r, w)
			if tracker, err := invocation.RegisterInvocation(is); err == nil {
				results := invocation.StartInvocation(tracker, true)
				responses = append(responses, results...)
			} else {
				log.Println(err.Error())
			}
		}
		for _, response := range responses {
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
	return nil
}

func (t *Trigger) invokePipeSources(pipeSources map[string]*TriggerTarget, r *http.Request, statusCode int, w http.ResponseWriter) {
	port := util.GetRequestOrListenerPortNum(r)
	for name, tt := range pipeSources {
		tt.TriggerCount++
		for source, callback := range tt.PipeCallbacks {
			callback(name, source, port, r, statusCode, w.Header())
		}
	}
}

func (t *Trigger) findMatchingTriggers(r *http.Request, statusCode int) (httpTargets, pipeSources map[string]*TriggerTarget) {
	uri := strings.ToLower(r.RequestURI)
	triggerTargets := map[string]*TriggerTarget{}
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
		tt.lock.Unlock()
		if tt.Enabled && tt.MatchCount >= tt.StartFrom && (tt.StopAt == 0 || tt.MatchCount <= tt.StopAt) {
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

func RunTriggers(r *http.Request, w http.ResponseWriter, statusCode int) {
	if !util.IsAdminRequest(r) && !util.IsMetricsRequest(r) {
		t := getPortTrigger(r)
		httpTargets, pipeSources := t.findMatchingTriggers(r, statusCode)
		go t.invokeHTTPTargets(httpTargets, w, r)
		go t.invokePipeSources(pipeSources, r, statusCode, w)
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
		return
	}
}
