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
	"goto/pkg/constants"
	"net/http"
	"strings"
)

func IsH2(r *http.Request) bool {
	return r.ProtoMajor == 2
}

func IsH2C(r *http.Request) bool {
	return GetRequestStore(r).IsH2C
}

func IsGRPC(r *http.Request) bool {
	rs := GetRequestStore(r)
	if !rs.IsGRPC {
		rs.IsGRPC = r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get(constants.HeaderContentType), "application/grpc")
	}
	return rs.IsGRPC
}

func SetIsGRPC(r *http.Request, value bool) {
	GetRequestStore(r).IsGRPC = value
}

func IsJSONRPC(r *http.Request) bool {
	return GetRequestStore(r).IsJSONRPC
}

func SetIsJSONRPC(r *http.Request, value bool) {
	GetRequestStore(r).IsJSONRPC = value
}

func IsH2Upgrade(r *http.Request) bool {
	return r.Method == "PRI" || strings.EqualFold(r.Header.Get("Upgrade"), "h2c") || upgradeRegexp.MatchString(r.Header.Get("Connection"))
}

func IsPutOrPost(r *http.Request) bool {
	return strings.EqualFold(r.Method, "POST") || strings.EqualFold(r.Method, "PUT")
}

func IsHeadersSent(r *http.Request) bool {
	return GetRequestStore(r).IsHeadersSent
}

func SetHeadersSent(r *http.Request, sent bool) {
	GetRequestStore(r).IsHeadersSent = sent
}

func IsTrafficEventReported(r *http.Request) bool {
	return GetRequestStore(r).IsTrafficEventReported
}

func IsAdminRequest(r *http.Request) bool {
	return GetRequestStore(r).IsAdminRequest
}

func CheckAdminRequest(r *http.Request) bool {
	uri := r.RequestURI
	if strings.HasPrefix(uri, "/port=") {
		uri = strings.Split(uri, "/port=")[1]
	}
	// uri2 := ""
	if pieces := strings.Split(uri, "/"); len(pieces) > 1 {
		uri = pieces[1]
		// if len(pieces) > 2 {
		// 	uri2 = pieces[2]
		// }
	}
	return uri == "version" || uri == "routes" || uri == "apis" || uri == "metrics" ||
		uri == "server" || uri == "request" || uri == "response" || uri == "listeners" ||
		uri == "label" || uri == "registry" || uri == "client" || uri == "proxy" ||
		uri == "job" || uri == "probes" || uri == "tcp" || uri == "grpc" || uri == "jsonrpc" ||
		uri == "log" || uri == "events" || uri == "tunnels" || uri == "pipes" || uri == "scripts" ||
		uri == "k8s" || uri == "tls" || uri == "routing" || uri == "mcpapi" || uri == "a2a"
}

func IsMetricsRequest(r *http.Request) bool {
	return GetRequestStore(r).IsMetricsRequest
}

func IsReminderRequest(r *http.Request) bool {
	return GetRequestStore(r).IsReminderRequest
}

func IsLockerRequest(r *http.Request) bool {
	return GetRequestStore(r).IsLockerRequest
}

func IsPeerEventsRequest(r *http.Request) bool {
	return GetRequestStore(r).IsPeerEventsRequest
}

func IsStatusRequest(r *http.Request) bool {
	return GetRequestStore(r).IsStatusRequest
}

func IsDelayRequest(r *http.Request) bool {
	return GetRequestStore(r).IsDelayRequest
}

func IsPayloadRequest(r *http.Request) bool {
	return GetRequestStore(r).IsPayloadRequest
}

func IsProbeRequest(r *http.Request) bool {
	return GetRequestStore(r).IsProbeRequest
}

func IsHealthRequest(r *http.Request) bool {
	return GetRequestStore(r).IsProbeRequest
}

func IsVersionRequest(r *http.Request) bool {
	return GetRequestStore(r).IsVersionRequest
}

func IsFilteredRequest(r *http.Request) bool {
	return GetRequestStore(r).IsFilteredRequest
}

func IsTunnelRequest(r *http.Request) bool {
	rs := GetRequestStore(r)
	return rs.IsTunnelRequest && !rs.IsTunnelConfigRequest
}

func IsKnownRequest(r *http.Request) bool {
	rs := GetRequestStore(r)
	return (rs.IsProbeRequest || rs.IsReminderRequest || rs.IsHealthRequest ||
		rs.IsMetricsRequest || rs.IsVersionRequest || rs.IsLockerRequest ||
		rs.IsAdminRequest || rs.IsStatusRequest || rs.IsDelayRequest ||
		rs.IsPayloadRequest || rs.IsTunnelConfigRequest)
}

func IsJSONContentType(h http.Header) bool {
	if contentType := h.Get(constants.HeaderContentType); contentType != "" {
		return strings.EqualFold(contentType, constants.ContentTypeJSON)
	}
	return false
}

func IsYAMLContentType(h http.Header) bool {
	if contentType := h.Get(constants.HeaderContentType); contentType != "" {
		return strings.EqualFold(contentType, constants.ContentTypeYAML)
	}
	return false
}

func IsUTF8ContentType(h http.Header) bool {
	if contentType := h.Get(constants.HeaderContentType); contentType != "" {
		return utf8Regexp.MatchString(contentType)
	}
	return false
}

func IsBinaryContentHeader(h http.Header) bool {
	if contentType := h.Get(constants.HeaderContentType); contentType != "" {
		return IsBinaryContentType(contentType)
	}
	return false
}

func IsBinaryContentType(contentType string) bool {
	return !knownTextMimeTypeRegexp.MatchString(contentType)
}

func IsInIntArray(value int, arr []int) bool {
	for _, v := range arr {
		if v == value {
			return true
		}
	}
	return false
}

func IsYes(flag string) bool {
	return strings.EqualFold(flag, "y") || strings.EqualFold(flag, "yes") ||
		strings.EqualFold(flag, "true") || strings.EqualFold(flag, "1")
}
