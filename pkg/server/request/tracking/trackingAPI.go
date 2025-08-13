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

package tracking

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"goto/pkg/events"
	"goto/pkg/server/hooks"
	"goto/pkg/server/middleware"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("tracking", setRoutes, middlewareFunc)
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	headerTrackingRouter := util.PathRouter(r, "/?headers?/track/?headers?")
	util.AddRouteWithPort(headerTrackingRouter, "/clear", clearHeaders, "POST")
	util.AddRouteWithPort(headerTrackingRouter, "/add/{headers}", addHeaders, "POST")
	util.AddRouteWithPort(headerTrackingRouter, "/remove/{headers}", removeHeaders, "POST")
	util.AddRouteWithPort(headerTrackingRouter, "/counts/clear/{headers}", clearHeaderCounts, "POST")
	util.AddRouteWithPort(headerTrackingRouter, "/counts/clear", clearHeaderCounts, "POST")
	util.AddRouteWithPort(headerTrackingRouter, "/counts", getCounts, "GET")
	util.AddRouteWithPort(headerTrackingRouter, "", getHeaders, "GET")

	trackRouter := util.PathRouter(r, "/track")
	util.AddRouteQWithPort(trackRouter, "", trackURI, "uri", "POST")
	util.AddRouteQWithPort(trackRouter, "/headers/{headers}", trackURIAndHeaders, "uri", "POST")
	util.AddRouteWithPort(trackRouter, "/headers/{headers}", trackHeaders, "POST")
	util.AddRouteQWithPort(trackRouter, "/{header}={value}", trackURIAndHeaderValue, "uri", "POST")
	util.AddRouteWithPort(trackRouter, "/headers/clear", removeHeaders, "POST")

	util.AddRouteWithPort(trackRouter, "/counts", getCounts, "GET")

}

func trackURI(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	uri := util.GetStringParamValue(r, "uri")
	msg := ""
	if uri != "" {
		Tracker.Init(port, "", uri, nil)
		hooks.GetPortHooks(port).AddHTTPHookWithListener("", uri, uri, nil, false, trackCallCounts)
		msg = fmt.Sprintf("Port [%d] will track URI [%s]", port, uri)
		events.SendRequestEvent("Tracking URI Added", msg, r)
		w.WriteHeader(http.StatusOK)
	} else {
		msg = "Invalid URI"
		w.WriteHeader(http.StatusBadRequest)
	}
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func trackHeaders(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	headers, present := util.GetListParam(r, "headers")
	msg := ""
	if present {
		Tracker.Init(port, "", "", headers)
		name := strings.Join(headers, "_")
		hooks.GetPortHooks(port).AddHTTPHookWithListener("", name, "", util.TransformHeaders(headers), false, trackCallCounts)
		msg = fmt.Sprintf("Port [%d] will track Headers [%+v]", port, headers)
		events.SendRequestEvent("Tracking Headers Added", msg, r)
	} else {
		msg = "Invalid Headers"
		w.WriteHeader(http.StatusBadRequest)
	}
}

func trackURIAndHeaders(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	uri := util.GetStringParamValue(r, "uri")
	headers, present := util.GetListParam(r, "headers")
	msg := ""
	if uri != "" && present {
		Tracker.Init(port, "", uri, headers)
		hooks.GetPortHooks(port).AddHTTPHookWithListener("", uri, uri, util.TransformHeaders(headers), false, trackCallCounts)
		msg = fmt.Sprintf("Port [%d] will track URI [%s] and Headers [%+v]", port, uri, headers)
		events.SendRequestEvent("Tracking URI and Headers Added", msg, r)
	} else {
		msg = "Invalid URI/Headers"
		w.WriteHeader(http.StatusBadRequest)
	}
}

func trackURIAndHeaderValue(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	uri := util.GetStringParamValue(r, "uri")
	header := util.GetStringParamValue(r, "header")
	value := util.GetStringParamValue(r, "value")
	msg := ""
	if uri != "" && header != "" && value != "" {
		headers := hooks.Headers{[2]string{header, value}}
		Tracker.Init(port, "", uri, []string{header})
		hooks.GetPortHooks(port).AddHTTPHookWithListener("", uri, uri, headers, false, trackCallCounts)
		msg = fmt.Sprintf("Port [%d] will track URI [%s] with Header:Value [%s:%s]", port, uri, header, value)
		events.SendRequestEvent("Tracking URI and Headers Added", msg, r)
	} else {
		msg = "Invalid URI/Header/Value"
		w.WriteHeader(http.StatusBadRequest)
	}
}

func trackCallCounts(port int, uri string, requestHeaders map[string][]string, body io.Reader) bool {
	Tracker.TrackCall(port, "", uri, requestHeaders)
	return true
}

func addHeaders(w http.ResponseWriter, r *http.Request) {
	msg := ""
	if headers := Tracker.initRequestHeaders("", r); headers != nil {
		msg = fmt.Sprintf("Port [%s] will track headers %s", util.GetRequestOrListenerPort(r), headers)
		events.SendRequestEvent("Tracking Headers Added", msg, r)
		w.WriteHeader(http.StatusOK)
	} else {
		msg = "Cannot add. Invalid header"
		w.WriteHeader(http.StatusBadRequest)
	}
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func removeHeaders(w http.ResponseWriter, r *http.Request) {
	msg := ""
	headers, present := Tracker.removeHeaders("", r)
	if present {
		msg = fmt.Sprintf("Port [%s] tracking headers %s removed", util.GetRequestOrListenerPort(r), headers)
		events.SendRequestEvent("Tracking Headers Removed", msg, r)
		w.WriteHeader(http.StatusOK)
	} else {
		msg = "No headers to remove"
		w.WriteHeader(http.StatusBadRequest)
	}
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func clearHeaders(w http.ResponseWriter, r *http.Request) {
	Tracker.clear("", r)
	msg := fmt.Sprintf("Port [%s] tracking headers cleared", util.GetRequestOrListenerPort(r))
	util.AddLogMessage(msg, r)
	events.SendRequestEvent("Tracking Headers Cleared", msg, r)
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, msg)
}

func clearHeaderCounts(w http.ResponseWriter, r *http.Request) {
	msg := ""
	if headers := Tracker.clearHeaderCounts("", r); headers != nil {
		msg = fmt.Sprintf("Port [%s] tracking counts reset for headers %s", util.GetRequestOrListenerPort(r), headers)
	} else {
		msg = fmt.Sprintf("Port [%s] tracking counts reset for all headers", util.GetRequestOrListenerPort(r))
	}
	events.SendRequestEvent("Tracked Header Counts Cleared", msg, r)
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func getCounts(w http.ResponseWriter, r *http.Request) {
	util.AddLogMessage(fmt.Sprintf("Port [%s] reporting tracking data", util.GetRequestOrListenerPort(r)), r)
	util.WriteJsonPayload(w, Tracker)
}

func getHeaders(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, util.ToJSONText(Tracker.getHeaders("", r)))
}
