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

package target

import (
	"fmt"
	"goto/pkg/client/results"
	"goto/pkg/events"
	"goto/pkg/global"
	"goto/pkg/invocation"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.Middleware{Name: "client", SetRoutes: SetRoutes}
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	targetsRouter := r.PathPrefix("/targets").Subrouter()
	util.AddRoute(targetsRouter, "/add", addTarget, "POST")
	util.AddRoute(targetsRouter, "/{targets}/remove", removeTargets, "POST")
	util.AddRoute(targetsRouter, "/{targets}/invoke", invokeTargets, "POST")
	util.AddRoute(targetsRouter, "/invoke/all", invokeTargets, "POST")
	util.AddRoute(targetsRouter, "/{targets}/stop", stopTargets, "POST")
	util.AddRoute(targetsRouter, "/stop/all", stopTargets, "POST")
	util.AddRoute(targetsRouter, "/clear", clearTargets, "POST")
	util.AddRoute(targetsRouter, "/active", getActiveTargets, "GET")
	util.AddRoute(targetsRouter, "/{target}?", getTargets, "GET")

	util.AddRoute(r, "/track/headers/clear", clearTrackingHeaders, "POST")
	util.AddRoute(r, "/track/headers/{headers}", addTrackingHeaders, "POST", "PUT")
	util.AddRoute(r, "/track/headers", getTrackingHeaders, "GET")

	util.AddRoute(r, "/track/time/clear", clearTrackingTimeBuckets, "POST")
	util.AddRoute(r, "/track/time/{buckets}", addTrackingTimeBuckets, "POST", "PUT")
	util.AddRoute(r, "/track/time", getTrackingTimeBuckets, "GET")

	util.AddRoute(r, "/results/all/{enable}", enableAllTargetsResultsCollection, "POST", "PUT")
	util.AddRoute(r, "/results/invocations/{enable}", enableInvocationResultsCollection, "POST", "PUT")
	util.AddRoute(r, "/results", getResults, "GET")
	util.AddRoute(r, "/results/invocations", getInvocationResults, "GET")
	util.AddRoute(r, "/results/clear", clearResults, "POST")
}

func addTarget(w http.ResponseWriter, r *http.Request) {
	msg := ""
	t := &Target{}
	if err := util.ReadJsonPayload(r, t); err == nil {
		if err := Client.AddTarget(t, r); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			msg = fmt.Sprintf("Invalid target spec: %s", err.Error())
		} else {
			w.WriteHeader(http.StatusOK)
			msg = fmt.Sprintf("Added target: %s", util.ToJSONText(t))
			events.SendRequestEventJSON(events.Client_TargetAdded, t.Name, t, r)
		}
	} else {
		w.WriteHeader(http.StatusBadRequest)
		msg = fmt.Sprintf("Failed to parse json with error: %s", err.Error())
	}
	if global.Flags.EnableClientLogs {
		util.AddLogMessage(msg, r)
	}
	fmt.Fprintln(w, msg)
}

func removeTargets(w http.ResponseWriter, r *http.Request) {
	msg := ""
	if targets, present := util.GetListParam(r, "targets"); present {
		if Client.removeTargets(targets) {
			w.WriteHeader(http.StatusOK)
			msg = fmt.Sprintf("Targets Removed: %+v", targets)
			events.SendRequestEventJSON(events.Client_TargetsRemoved, util.GetStringParamValue(r, "targets"), targets, r)
		} else {
			w.WriteHeader(http.StatusNotAcceptable)
			msg = fmt.Sprintf("Targets cannot be removed while traffic is running")
		}
	} else {
		w.WriteHeader(http.StatusBadRequest)
		msg = "No target given"
	}
	if global.Flags.EnableClientLogs {
		util.AddLogMessage(msg, r)
	}
	fmt.Fprintln(w, msg)
}

func clearTargets(w http.ResponseWriter, r *http.Request) {
	msg := ""
	if Client.init() {
		w.WriteHeader(http.StatusOK)
		msg = "Targets cleared"
		events.SendRequestEvent(events.Client_TargetsCleared, "", r)
	} else {
		w.WriteHeader(http.StatusNotAcceptable)
		msg = fmt.Sprintf("Targets cannot be cleared while traffic is running")
	}
	if global.Flags.EnableClientLogs {
		util.AddLogMessage(msg, r)
	}
	fmt.Fprintln(w, msg)
}

func getTargets(w http.ResponseWriter, r *http.Request) {
	if t, present := util.GetStringParam(r, "target"); present {
		if Client.targets[t] != nil {
			util.WriteJsonPayload(w, Client.targets[t])
		} else {
			util.WriteErrorJson(w, "Target not found: "+t)
		}
	} else {
		util.WriteJsonPayload(w, Client.targets)
	}
	if global.Flags.EnableClientLogs {
		util.AddLogMessage("Reporting targets", r)
	}
}

func addTrackingHeaders(w http.ResponseWriter, r *http.Request) {
	msg := ""
	if h, present := util.GetStringParam(r, "headers"); present {
		Client.AddTrackingHeaders(h)
		w.WriteHeader(http.StatusOK)
		msg = fmt.Sprintf("Header %s will be tracked", h)
		events.SendRequestEvent(events.Client_TrackingHeadersAdded, msg, r)
	} else {
		w.WriteHeader(http.StatusBadRequest)
		msg = "Invalid header name"
	}
	if global.Flags.EnableClientLogs {
		util.AddLogMessage(msg, r)
	}
	fmt.Fprintln(w, msg)
}

func clearTrackingHeaders(w http.ResponseWriter, r *http.Request) {
	Client.clearTrackingHeaders()
	msg := "All tracking headers cleared"
	events.SendRequestEvent(events.Client_TrackingHeadersCleared, msg, r)
	fmt.Fprintln(w, msg)
	if global.Flags.EnableClientLogs {
		util.AddLogMessage(msg, r)
	}
}

func getTrackingHeaders(w http.ResponseWriter, r *http.Request) {
	msg := fmt.Sprintf("Tracking headers: %s", strings.Join(Client.getTrackingHeaders(), ","))
	fmt.Fprintln(w, msg)
	if global.Flags.EnableClientLogs {
		util.AddLogMessage(msg, r)
	}
}

func addTrackingTimeBuckets(w http.ResponseWriter, r *http.Request) {
	msg := ""
	b := util.GetStringParamValue(r, "buckets")
	if !Client.AddTrackingTimeBuckets(b) {
		w.WriteHeader(http.StatusBadRequest)
		msg = "Invalid time bucket"
	} else {
		msg = fmt.Sprintf("Time Buckets [%s] will be tracked", b)
		events.SendRequestEvent(events.Client_TrackingTimeBucketAdded, msg, r)
	}
	if global.Flags.EnableClientLogs {
		util.AddLogMessage(msg, r)
	}
	fmt.Fprintln(w, msg)
}

func clearTrackingTimeBuckets(w http.ResponseWriter, r *http.Request) {
	Client.clearTrackingTimeBuckets()
	msg := "All tracking time buckets cleared"
	events.SendRequestEvent(events.Client_TrackingTimeBucketsCleared, msg, r)
	fmt.Fprintln(w, msg)
	if global.Flags.EnableClientLogs {
		util.AddLogMessage(msg, r)
	}
}

func getTrackingTimeBuckets(w http.ResponseWriter, r *http.Request) {
	util.WriteJsonPayload(w, Client.trackTimeBuckets)
	if global.Flags.EnableClientLogs {
		util.AddLogMessage("Tracking TimeBuckets Reported", r)
	}
}

func getInvocationResults(w http.ResponseWriter, r *http.Request) {
	util.WriteStringJsonPayload(w, results.GetInvocationResultsJSON(false))
	if global.Flags.EnableClientLogs {
		util.AddLogMessage("Reporting results", r)
	}
}

func getResults(w http.ResponseWriter, r *http.Request) {
	util.WriteStringJsonPayload(w, results.GetTargetsResultsJSON(false))
	if global.Flags.EnableClientLogs {
		util.AddLogMessage("Reporting results", r)
	}
}

func getActiveTargets(w http.ResponseWriter, r *http.Request) {
	if global.Flags.EnableClientLogs {
		util.AddLogMessage("Reporting active invocations", r)
	}
	result := map[string]interface{}{}
	pc := Client
	pc.targetsLock.RLock()
	result["activeCount"] = pc.activeTargetsCount
	pc.targetsLock.RUnlock()
	result["activeInvocations"] = invocation.GetActiveInvocations()
	util.WriteJsonPayload(w, result)
}

func clearResults(w http.ResponseWriter, r *http.Request) {
	results.ClearResults()
	w.WriteHeader(http.StatusOK)
	msg := events.Client_ResultsCleared
	events.SendRequestEvent(msg, "", r)
	if global.Flags.EnableClientLogs {
		util.AddLogMessage(msg, r)
	}
	fmt.Fprintln(w, msg)
}

func enableAllTargetsResultsCollection(w http.ResponseWriter, r *http.Request) {
	enable := util.GetStringParamValue(r, "enable")
	results.EnableAllTargetResults(util.IsYes(enable))
	w.WriteHeader(http.StatusOK)
	msg := "Changed all targets summary results collection"
	if global.Flags.EnableClientLogs {
		util.AddLogMessage(msg, r)
	}
	fmt.Fprintln(w, msg)
}

func enableInvocationResultsCollection(w http.ResponseWriter, r *http.Request) {
	enable := util.GetBoolParamValue(r, "enable")
	results.EnableInvocationResults(enable)
	msg := ""
	if enable {
		msg = "Will collect invocation results"
	} else {
		msg = "Will not collect invocation results"
	}
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func stopTargets(w http.ResponseWriter, r *http.Request) {
	msg := ""
	c := Client
	targets, _ := util.GetListParam(r, "targets")
	hasActive, _ := c.stopTargets(targets)
	if hasActive {
		w.WriteHeader(http.StatusOK)
		msg = fmt.Sprintf("Targets %+v stopped", targets)
		events.SendRequestEvent(events.Client_TargetsStopped, msg, r)
	} else {
		w.WriteHeader(http.StatusOK)
		msg = "No targets to stop"
	}
	if global.Flags.EnableClientLogs {
		util.AddLogMessage(msg, r)
	}
	fmt.Fprintln(w, msg)
}

func invokeTargets(w http.ResponseWriter, r *http.Request) {
	c := Client
	targetsToInvoke := c.getTargetsToInvoke(r)
	if len(targetsToInvoke) > 0 {
		for _, target := range targetsToInvoke {
			go c.invokeTarget(target)
		}
		w.WriteHeader(http.StatusOK)
		if global.Flags.EnableClientLogs {
			util.AddLogMessage("Targets invoked", r)
		}
	} else {
		w.WriteHeader(http.StatusNotAcceptable)
		fmt.Fprintln(w, "No targets to invoke")
		if global.Flags.EnableClientLogs {
			util.AddLogMessage("No targets to invoke", r)
		}
	}
}
