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

package util

import (
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/global"
	"net/http"
	"strings"
)

var (
	ExcludedHeaders = map[string]bool{}
	HTTPHandler     http.Handler

	WillTunnel    func(*http.Request, *RequestStore) bool
	WillProxyGRPC func(int, any) bool
	WillProxyMCP  func(*http.Request, *RequestStore) bool
)

func SendGotoHeaders(w http.ResponseWriter, r *http.Request) {
	port := GetRequestOrListenerPort(r)
	rs := GetRequestStore(r)
	w.Header().Add(constants.HeaderGotoRemoteAddress, r.RemoteAddr)
	w.Header().Add(constants.HeaderGotoPort, port)
	w.Header().Add(constants.HeaderGotoTLS, fmt.Sprintf("%t", rs.IsTLS))
	w.Header().Add(constants.HeaderGotoHost, global.Self.HostLabel)
	w.Header().Add(constants.HeaderGotoProtocol, rs.GotoProtocol)
	w.Header().Add(constants.HeaderViaGoto, global.Funcs.GetListenerLabel(r))
	CopyHeaders("Request", r, w, r.Header, true, true, false)
	rs.IsHeadersSent = true
}

func GotoProtocol(isH2, isTLS, isGRPC bool) string {
	protocol := "HTTP"
	if isGRPC {
		protocol = "GRPC"
	} else if isTLS {
		if isH2 {
			protocol = "HTTP/2"
		} else {
			protocol = "HTTPS"
		}
	} else if isH2 {
		protocol = "H2C"
	} else {
		protocol = "HTTP/1.1"
	}
	return protocol
}

func SendBadRequest(msg string, w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusBadRequest)
	fmt.Fprintln(w, msg)
	AddLogMessage(msg, r)
}

func FixURL(url, suffix string, https bool) string {
	if !strings.HasPrefix(url, "http") {
		if https {
			url = "https://" + url
		} else {
			url = "http://" + url
		}
	}
	if !strings.HasSuffix(url, suffix) {
		if !strings.HasSuffix(url, "/") {
			url += "/"
		}
		if suffix != "/" {
			url += suffix
		}
	}
	return url
}

func IsAcceptJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "json")
}

func IsAcceptYAML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "yaml")
}

func IsAcceptText(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text")
}
