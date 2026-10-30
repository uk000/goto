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

package tracking

import (
	"fmt"
	"io"
	"net/http"

	"goto/pkg/server/hooks"
	"goto/pkg/server/middleware"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("tracking", setRoutes, middlewareFunc)
)

func setRoutes(r *mux.Router) {
	trackRouter := util.PathRouter(r, "/track")
	// Must be registered before the /headers router below, which would otherwise take /headers/{headers} first
	// and leave this route unreachable.
	util.AddRouteQ(trackRouter, "/headers/{headers}", trackURIAndHeaders, "uri", "POST")
	headerTrackingRouter := util.PathRouter(trackRouter, "/headers")
	util.AddRoute(headerTrackingRouter, "/clear", clearHeaders, "POST")
	util.AddRoute(headerTrackingRouter, "/remove/{headers}", removeHeaders, "POST")
	util.AddRoute(headerTrackingRouter, "/counts/clear/{headers}", clearHeaderCounts, "POST")
	util.AddRoute(headerTrackingRouter, "/counts/clear", clearHeaderCounts, "POST")
	util.AddRoute(headerTrackingRouter, "/counts", getCounts, "GET")
	util.AddRoute(headerTrackingRouter, "/add/{headers}", addHeaders, "POST")
	util.AddRoute(headerTrackingRouter, "/{headers}", trackHeaders, "POST")
	util.AddRoute(headerTrackingRouter, "", getHeaders, "GET")

	util.AddRouteQ(trackRouter, "", trackURI, "uri", "POST")
	util.AddRouteQ(trackRouter, "/{header}={value}", trackURIAndHeaderValue, "uri", "POST")

	util.AddRoute(trackRouter, "/counts", getCounts, "GET")
	util.AddRoute(trackRouter, "", getCounts, "GET")
}

func trackURI(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	uri := util.GetStringParamValue(r, "uri")
	msg := ""
	if uri != "" {
		Tracker.Init(port, "", uri, nil)
		hooks.GetPortHooks(port).AddHTTPHookWithListener("", uri, uri, nil, false, trackCallCounts)
		msg = fmt.Sprintf("Port [%d] will track URI [%s]", port, uri)
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
		// Headers are counted for all requests by the tracking middleware (see track), so no hook is needed. A hook
		// without a URI would match every request, and count every URI that gets requested.
		Tracker.initHeaders(port, "", headers)
		msg = fmt.Sprintf("Port [%d] will track Headers [%+v]", port, headers)
	} else {
		msg = "Invalid Headers"
		w.WriteHeader(http.StatusBadRequest)
	}
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func trackURIAndHeaders(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	uri := util.GetStringParamValue(r, "uri")
	headers, present := util.GetListParam(r, "headers")
	msg := ""
	if uri != "" && present {
		Tracker.Init(port, "", uri, headers)
		// The hook matches the URI only. Its listener counts the headers that were added to the tracking. Giving the
		// headers to the hook too would have the hooks middleware count the request a second time (see Track).
		hooks.GetPortHooks(port).AddHTTPHookWithListener("", uri, uri, nil, false, trackCallCounts)
		msg = fmt.Sprintf("Port [%d] will track URI [%s] and Headers [%+v]", port, uri, headers)
	} else {
		msg = "Invalid URI/Headers"
		w.WriteHeader(http.StatusBadRequest)
	}
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func trackURIAndHeaderValue(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	uri := util.GetStringParamValue(r, "uri")
	header := util.GetStringParamValue(r, "header")
	value := util.GetStringParamValue(r, "value")
	msg := ""
	if uri != "" && header != "" && value != "" {
		Tracker.Init(port, "", uri, []string{header})
		// As above: the hook matches the URI only. The counts of the header are kept per value.
		hooks.GetPortHooks(port).AddHTTPHookWithListener("", uri, uri, nil, false, trackCallCounts)
		msg = fmt.Sprintf("Port [%d] will track URI [%s] with Header:Value [%s:%s]", port, uri, header, value)
	} else {
		msg = "Invalid URI/Header/Value"
		w.WriteHeader(http.StatusBadRequest)
	}
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func trackCallCounts(port int, uri string, requestHeaders map[string][]string, body io.Reader) bool {
	Tracker.TrackCall(port, "", uri, requestHeaders)
	return true
}

func addHeaders(w http.ResponseWriter, r *http.Request) {
	msg := ""
	if headers := Tracker.initRequestHeaders("", r); headers != nil {
		msg = fmt.Sprintf("Port [%s] will track headers %s", util.GetRequestOrListenerPort(r), headers)
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
