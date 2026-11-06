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
	"maps"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

var (
	// ExcludedHeaders has the headers (in lower case) that are left out of the headers that are logged and copied. It is
	// changed while requests are served, so it is to be changed and read through ExcludeHeader, IncludeHeader and
	// IsHeaderExcluded, which guard it. Code that uses the map directly does it without the guard.
	ExcludedHeaders     = map[string]bool{}
	excludedHeadersLock sync.RWMutex
	HTTPHandler         http.Handler

	WillTunnel    func(*http.Request, *RequestStore) bool
	WillProxyGRPC func(int, any) bool
	WillProxyMCP  func(*http.Request, *RequestStore) bool
	LowerViaGoto  = strings.ToLower(constants.HeaderViaGoto)
)

// ExcludeHeader leaves the header out of the headers that are logged and copied.
func ExcludeHeader(header string) {
	excludedHeadersLock.Lock()
	defer excludedHeadersLock.Unlock()
	ExcludedHeaders[strings.ToLower(header)] = true
}

// IncludeHeader undoes ExcludeHeader.
func IncludeHeader(header string) {
	excludedHeadersLock.Lock()
	defer excludedHeadersLock.Unlock()
	delete(ExcludedHeaders, strings.ToLower(header))
}

// IsHeaderExcluded tells if the header (case-insensitive) is among the excluded headers, reading them under the lock.
func IsHeaderExcluded(header string) bool {
	excludedHeadersLock.RLock()
	defer excludedHeadersLock.RUnlock()
	return ExcludedHeaders[strings.ToLower(header)]
}

// excludedHeadersSnapshot returns the excluded headers as they are now, for what iterates through them while others may
// be excluding more.
func excludedHeadersSnapshot() map[string]bool {
	excludedHeadersLock.RLock()
	defer excludedHeadersLock.RUnlock()
	return maps.Clone(ExcludedHeaders)
}

// SendGotoHeaders sets the Goto identity headers and the echoed request headers on the response, and declares the trailers.
func SendGotoHeaders(w http.ResponseWriter, r *http.Request) {
	port := GetRequestOrListenerPort(r)
	rs := GetRequestStore(r)
	w.Header().Add("Trailer", constants.HeaderViaGoto)
	w.Header().Add("Trailer", constants.HeaderGotoUpstreamStatus)
	label := global.Funcs.GetListenerLabel(r)
	if rs.IsMCP {
		label += "(MCP)"
	} else if rs.IsAI {
		label += "(A2A)"
	} else {
		if rs.IsProxy {
			label += "(Proxy)"
		} else if rs.IsClient {
			label += "(Client)"
		}
		label += "(HTTP)"
	}
	w.Header().Add(constants.HeaderViaGoto, label)
	w.Header().Add(constants.HeaderGotoRemoteAddress, r.RemoteAddr)
	w.Header().Add(constants.HeaderGotoPort, port)
	w.Header().Add(constants.HeaderGotoMTLS, fmt.Sprintf("%t", rs.IsMTLS))
	w.Header().Add(constants.HeaderGotoTLS, fmt.Sprintf("%t", rs.IsTLS))
	if rs.ServerName != "" || rs.IsTLS {
		w.Header().Add(constants.HeaderGotoSNI, rs.ServerName)
	}
	if rs.IsTLS {
		w.Header().Add(constants.HeaderGotoClientCert, rs.ClientCert)
		w.Header().Add(constants.HeaderGotoServerCert, rs.ServerCert)
	}
	w.Header().Add(constants.HeaderGotoHost, global.Self.HostLabel)
	w.Header().Add(constants.HeaderGotoProtocol, rs.GotoProtocol)
	CopyHeaders("Request", r, w, r.Header, true, true, false)
	rs.IsHeadersSent = true
}

// SendGotoTrailers merges the Via-Goto values on the response with the ones collected in the request store, and sets the upstream statuses.
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
	if len(rs.UpstreamStatuses) > 0 {
		if w.Header().Get(constants.HeaderGotoUpstreamStatus) == "" {
			w.Header().Add(constants.HeaderGotoUpstreamStatus, ToJSONText(rs.UpstreamStatuses))
		}
	}
}

// GotoProtocol names the protocol (GRPC, HTTP/2, HTTPS, H2C or HTTP/1.1) that the h2, tls and grpc flags describe.
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

// GetViaGotosFromUpstreamHeaders collects the Via-Goto values from the response headers of each upstream call result.
func GetViaGotosFromUpstreamHeaders(upheaders map[string]any) map[string]bool {
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

// GetViaGotosFromHeaders collects the Via-Goto header values as a set.
func GetViaGotosFromHeaders(headers http.Header) map[string]bool {
	viaGotos := map[string]bool{}
	if values := headers[constants.HeaderViaGoto]; values != nil {
		for _, value := range values {
			viaGotos[value] = true
		}
	}
	return viaGotos
}

// BuildListenerLabel builds the [self:port][pod@namespace@cluster] label of the listener on the port.
func BuildListenerLabel(port int) string {
	selfLabel := ""
	if global.Self.GivenName {
		selfLabel = global.Self.Name
	} else {
		selfLabel = global.Self.PodIP
	}
	return fmt.Sprintf("[%s:%d][%s@%s@%s]", selfLabel, port, global.Self.PodName, global.Self.Namespace, global.Self.Cluster)
}

// GetViaGotoValue returns the label of the listener on the port, which is used as the Via-Goto value.
func GetViaGotoValue(port int) string {
	return global.Funcs.GetListenerLabelForPort(port)
}

// GetUpstreamStatuses parses the JSON of the Goto-Upstream-Status header into a map, or returns nil.
func GetUpstreamStatuses(headers http.Header) map[string]any {
	if v, ok := headers[constants.HeaderGotoUpstreamStatus]; ok && len(v) > 0 {
		if json, ok := JSONFromJSONText(v[0]); ok {
			return json.Object()
		}
	}
	return nil
}

// GetIntHeaderValue returns the first value of the header as an int, or 0 if it is absent or not a number.
func GetIntHeaderValue(headers http.Header, k string) int {
	if v, ok := headers[k]; !ok || len(v) == 0 {
		return 0
	} else {
		if i, err := strconv.Atoi(v[0]); err != nil {
			return 0
		} else {
			return i
		}
	}
}

// SendResponse writes the status and the formatted message to the response, and adds the message to the request's log.
func SendResponse(w http.ResponseWriter, r *http.Request, status int, msg string, params ...any) {
	w.WriteHeader(status)
	msg = fmt.Sprintf(msg, params...)
	fmt.Fprintln(w, msg)
	AddLogMessage(msg, r)
}

// SendBadRequest sends a 400 response with the formatted message.
func SendBadRequest(w http.ResponseWriter, r *http.Request, msg string, params ...any) {
	SendResponse(w, r, http.StatusBadRequest, msg, params...)
}

// FixURL adds a missing http/https scheme to the URL and makes sure that it ends with the suffix path.
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

// IsAcceptJSON tells if the Accept header of the request mentions json.
func IsAcceptJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "json")
}

// IsAcceptYAML tells if the Accept header of the request mentions yaml.
func IsAcceptYAML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "yaml")
}

// IsAcceptText tells if the Accept header of the request mentions text.
func IsAcceptText(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text")
}
