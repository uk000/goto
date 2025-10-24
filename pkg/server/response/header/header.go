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

package header

import (
	"fmt"
	"net/http"
	"strings"
	"sync"

	"goto/pkg/events"
	"goto/pkg/server/middleware"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
	Middleware                    = middleware.NewMiddleware("response.header", setRoutes, middlewareFunc)
	responseHeadersToAddByPort    = map[string]map[string][]string{}
	responseHeadersToRemoveByPort = map[string]map[string]bool{}
	headersLock                   sync.RWMutex
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	headersRouter := util.PathRouter(r, "/headers")
	util.AddRoute(headersRouter, "/add/{header}={value}", addResponseHeader, "PUT", "POST")
	util.AddRoute(headersRouter, "/remove/{header}", removeResponseHeader, "PUT", "POST")
	util.AddRoute(headersRouter, "/clear", clearResponseHeader, "PUT", "POST")
	util.AddRoute(headersRouter, "", getResponseHeaders, "GET")
}

func addResponseHeader(w http.ResponseWriter, r *http.Request) {
	msg := ""
	if header, present := util.GetStringParam(r, "header"); present {
		value, _ := util.GetStringParam(r, "value")
		listenerPort := util.GetRequestOrListenerPort(r)
		headersLock.Lock()
		headerMap := responseHeadersToAddByPort[listenerPort]
		if headerMap == nil {
			headerMap = map[string][]string{}
			responseHeadersToAddByPort[listenerPort] = headerMap
		}
		values, present := headerMap[header]
		if !present {
			values = []string{}
		}
		headerMap[header] = append(values, value)
		headersLock.Unlock()
		msg = fmt.Sprintf("Port [%s] Will add header [%s : %s] to responses", listenerPort, header, value)
		events.SendRequestEvent("Response Header Added", msg, r)
	} else {
		msg = "Cannot add. Invalid header"
		w.WriteHeader(http.StatusBadRequest)
	}
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func removeResponseHeader(w http.ResponseWriter, r *http.Request) {
	msg := ""
	if header, present := util.GetStringParam(r, "header"); present {
		headersLock.Lock()
		listenerPort := util.GetRequestOrListenerPort(r)
		headerMap := responseHeadersToRemoveByPort[listenerPort]
		if headerMap == nil {
			headerMap = map[string]bool{}
			responseHeadersToRemoveByPort[listenerPort] = headerMap
		}
		lh := strings.ToLower(header)
		headerMap[lh] = true
		util.ExcludedHeaders[lh] = true
		headersLock.Unlock()
		msg = fmt.Sprintf("Port [%s] Will remove header [%s] from responses", listenerPort, header)
		events.SendRequestEvent("Response Header Removed", msg, r)
	} else {
		msg = "Cannot remove. Invalid header"
		w.WriteHeader(http.StatusBadRequest)
	}
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func clearResponseHeader(w http.ResponseWriter, r *http.Request) {
	listenerPort := util.GetRequestOrListenerPort(r)
	headersLock.Lock()
	responseHeadersToAddByPort[listenerPort] = map[string][]string{}
	responseHeadersToRemoveByPort[listenerPort] = map[string]bool{}
	headersLock.Unlock()
	msg := fmt.Sprintf("Port [%s] Response header cleared", listenerPort)
	events.SendRequestEvent("Response Header Cleared", msg, r)
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

func getResponseHeaders(w http.ResponseWriter, r *http.Request) {
	headersLock.RLock()
	defer headersLock.RUnlock()
	listenerPort := util.GetRequestOrListenerPort(r)
	headersToAdd := responseHeadersToAddByPort[listenerPort]
	headersToRemove := responseHeadersToRemoveByPort[listenerPort]
	var s strings.Builder
	if len(headersToAdd) > 0 {
		s.Grow(64)
		for header, values := range headersToAdd {
			for _, value := range values {
				fmt.Fprintf(&s, "+[%s:%s] ", header, value)
				w.Header().Add(header, value)
			}
		}
	}
	if len(headersToRemove) > 0 {
		for header := range headersToRemove {
			s.Grow(16)
			fmt.Fprintf(&s, "-[%s] ", header)
			w.Header().Del(header)
		}
	}
	msg := s.String()
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func setResponseHeaders(w http.ResponseWriter, r *http.Request) {
	headersLock.RLock()
	defer headersLock.RUnlock()
	port := util.GetRequestOrListenerPort(r)
	util.CopyHeadersWithIgnore("Request", r, w.Header(), nil, responseHeadersToRemoveByPort[port], true, true, false)
	headerMap := responseHeadersToAddByPort[port]
	for header, values := range headerMap {
		for _, value := range values {
			w.Header().Add(header, value)
		}
	}
}

func middlewareFunc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if next != nil {
			next.ServeHTTP(w, r)
		}
		if !util.IsHeadersSent(r) && !util.IsAdminRequest(r) && !util.IsTunnelRequest(r) && !util.IsGRPC(r) {
			setResponseHeaders(w, r)
		}
	})
}
