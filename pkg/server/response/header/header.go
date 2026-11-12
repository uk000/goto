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

package header

import (
	"fmt"
	"net/http"
	"strings"
	"sync"

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

// setRoutes registers the response header config routes under /headers.
func setRoutes(r *mux.Router) {
	headersRouter := util.PathRouter(r, "/headers")
	util.AddRoute(headersRouter, "/add/{header}={value}", addResponseHeader, "PUT", "POST")
	util.AddRoute(headersRouter, "/remove/{header}", removeResponseHeader, "PUT", "POST")
	util.AddRoute(headersRouter, "/clear", clearResponseHeader, "PUT", "POST")
	util.AddRoute(headersRouter, "", getResponseHeaders, "GET")
}

// addResponseHeader configures a header value to be added to all responses on the request's port; values accumulate per header.
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
	} else {
		msg = "Cannot add. Invalid header"
		w.WriteHeader(http.StatusBadRequest)
	}
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

// removeResponseHeader configures a header to be stripped from all responses on the request's port.
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
	} else {
		msg = "Cannot remove. Invalid header"
		w.WriteHeader(http.StatusBadRequest)
	}
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

// clearResponseHeader clears the response headers configured to be added or removed on the request's port.
func clearResponseHeader(w http.ResponseWriter, r *http.Request) {
	listenerPort := util.GetRequestOrListenerPort(r)
	headersLock.Lock()
	responseHeadersToAddByPort[listenerPort] = map[string][]string{}
	responseHeadersToRemoveByPort[listenerPort] = map[string]bool{}
	headersLock.Unlock()
	msg := fmt.Sprintf("Port [%s] Response header cleared", listenerPort)
	util.AddLogMessage(msg, r)
	fmt.Fprintln(w, msg)
}

// getResponseHeaders reports the port's configured response headers to add (+) and remove (-), and applies them to this response too.
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

// SetResponseHeaders copies the request headers to the response (skipping the ones configured for removal) and adds the configured response headers of the port.
func SetResponseHeaders(w http.ResponseWriter, r *http.Request) {
	headersLock.RLock()
	defer headersLock.RUnlock()
	port := util.GetRequestOrListenerPort(r)
	util.CopyRequestHeadersWithIgnore("Request", r, w.Header(), nil, responseHeadersToRemoveByPort[port], true, true, false)
	headerMap := responseHeadersToAddByPort[port]
	for header, values := range headerMap {
		for _, value := range values {
			w.Header().Add(header, value)
		}
	}
}

// middlewareFunc calls next, then applies the configured response headers unless headers were already sent or the request is admin, tunnel or gRPC.
func middlewareFunc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if next != nil {
			next.ServeHTTP(w, r)
		}
		rs := util.GetRequestStore(r)
		if !rs.IsHeadersSent && !rs.IsAdminRequest && !rs.IsTunnelRequest && !rs.IsGRPC {
			SetResponseHeaders(w, r)
		}
	})
}
