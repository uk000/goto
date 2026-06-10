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
	LowerViaGoto  = strings.ToLower(constants.HeaderViaGoto)
)

func SendGotoHeaders(w http.ResponseWriter, r *http.Request) {
	port := GetRequestOrListenerPort(r)
	rs := GetRequestStore(r)
	w.Header().Add("Trailer", constants.HeaderViaGoto)
	label := global.Funcs.GetListenerLabel(r)
	if rs.IsMCP {
		label += "(MCP)"
	} else if rs.IsAI {
		label += "(A2A)"
	} else {
		if rs.IsProxy {
			label += "(Proxy)"
		}
		label += "(HTTP)"
	}
	w.Header().Add(constants.HeaderViaGoto, label)
	w.Header().Add(constants.HeaderGotoRemoteAddress, r.RemoteAddr)
	w.Header().Add(constants.HeaderGotoPort, port)
	w.Header().Add(constants.HeaderGotoTLS, fmt.Sprintf("%t", rs.IsTLS))
	if rs.ServerName != "" || rs.IsTLS {
		w.Header().Add(constants.HeaderGotoSNI, rs.ServerName)
	}
	w.Header().Add(constants.HeaderGotoHost, global.Self.HostLabel)
	w.Header().Add(constants.HeaderGotoProtocol, rs.GotoProtocol)
	CopyHeaders("Request", r, w, r.Header, true, true, false)
	rs.IsHeadersSent = true
}

func SendGotoTrailers(w http.ResponseWriter, r *http.Request) {
	rs := GetRequestStore(r)
	viaGotos := map[string]bool{}
	old := w.Header()[constants.HeaderViaGoto]
	for _, v := range old {
		viaGotos[v] = true
	}
	for _, v := range rs.ViaGotos {
		viaGotos[v] = true
	}
	values := []string{}
	for v := range viaGotos {
		values = append(values, v)
	}
	w.Header()[constants.HeaderViaGoto] = values
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

func GetViaGotosFromHeaders(upheaders map[string]any) map[string]bool {
	viaGotos := map[string]bool{}
	for _, v := range upheaders {
		if m, ok := v.(map[string]any); ok {
			rh := m["ResponseHeaders"]
			if rh == nil {
				continue
			}
			if responseHeaders, ok := rh.(map[string]any); ok {
				if v2 := responseHeaders[constants.HeaderViaGoto]; v2 != nil {
					if values, ok := v2.([]any); ok {
						for _, value := range values {
							viaGotos[fmt.Sprint(value)] = true
						}
					}
				}
			} else if responseHeaders, ok := rh.(http.Header); ok {
				if values := responseHeaders[constants.HeaderViaGoto]; values != nil {
					for _, value := range values {
						viaGotos[fmt.Sprint(value)] = true
					}
				}

			}
		}
	}
	return viaGotos
}

func BuildListenerLabel(port int) string {
	selfLabel := ""
	if global.Self.GivenName {
		selfLabel = global.Self.Name
	} else {
		selfLabel = global.Self.PodIP
	}
	return fmt.Sprintf("[%s:%d][%s@%s@%s]", selfLabel, port, global.Self.PodName, global.Self.Namespace, global.Self.Cluster)
}

func GetViaGotoValue(port int) string {
	return global.Funcs.GetListenerLabelForPort(port)
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
