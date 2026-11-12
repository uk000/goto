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

// IsH2 tells if the request came over HTTP/2.
func IsH2(r *http.Request) bool {
	return r.ProtoMajor == 2
}

// IsH2C tells if the request store marks the request as cleartext HTTP/2.
func IsH2C(r *http.Request) bool {
	return GetRequestStore(r).IsH2C
}

// IsGRPC tells if the request is gRPC (HTTP/2 with an application/grpc content type), remembering a positive answer in the request store.
func IsGRPC(r *http.Request) bool {
	rs := GetRequestStore(r)
	if !rs.IsGRPC {
		rs.IsGRPC = r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get(constants.HeaderContentType), "application/grpc")
	}
	return rs.IsGRPC
}

// SetIsGRPC overrides the gRPC flag of the request in its request store.
func SetIsGRPC(r *http.Request, value bool) {
	GetRequestStore(r).IsGRPC = value
}

// IsJSONRPC tells if the request store marks the request as JSON-RPC.
func IsJSONRPC(r *http.Request) bool {
	return GetRequestStore(r).IsJSONRPC
}

// SetIsJSONRPC sets the JSON-RPC flag of the request in its request store.
func SetIsJSONRPC(r *http.Request, value bool) {
	GetRequestStore(r).IsJSONRPC = value
}

// IsH2Upgrade tells if the request is an HTTP/2 prior-knowledge preface or a request to upgrade to h2c.
func IsH2Upgrade(r *http.Request) bool {
	return r.Method == "PRI" || strings.EqualFold(r.Header.Get("Upgrade"), "h2c") || upgradeRegexp.MatchString(r.Header.Get("Connection"))
}

// IsPutOrPost tells if the request method is PUT or POST.
func IsPutOrPost(r *http.Request) bool {
	return strings.EqualFold(r.Method, "POST") || strings.EqualFold(r.Method, "PUT")
}

// IsHeadersSent tells if the response headers of the request were already sent.
func IsHeadersSent(r *http.Request) bool {
	return GetRequestStore(r).IsHeadersSent
}

// SetHeadersSent records in the request store whether the response headers were sent.
func SetHeadersSent(r *http.Request, sent bool) {
	GetRequestStore(r).IsHeadersSent = sent
}

// IsAdminRequest tells if the request store marks the request as one for the goto admin APIs (see CheckAdminRequest).
func IsAdminRequest(r *http.Request) bool {
	return GetRequestStore(r).IsAdminRequest
}

// CheckAdminRequest tells if the request is for one of the root paths of the goto APIs, which are not traffic: the filters
// and the rules for the traffic (forced status, delay and the like) do not apply to them. /api and /apis are not among
// them, as those are the root paths of the k8s API that goto serves as a mock, and the requests that a k8s client makes
// are traffic like any other.
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
	return uri == "version" || uri == "routes" || uri == "metrics" ||
		uri == "server" || uri == "request" || uri == "response" || uri == "listeners" ||
		uri == "label" || uri == "registry" || uri == "client" || uri == "proxy" ||
		uri == "job" || uri == "probes" || uri == "tcp" || uri == "grpc" || uri == "jsonrpc" ||
		uri == "log" || uri == "tunnels" || uri == "pipes" || uri == "scripts" ||
		uri == "k8s" || uri == "tls" || uri == "routing" || uri == "mcpapi" || uri == "a2a"
}

// IsMetricsRequest tells if the request store marks the request as a metrics request.
func IsMetricsRequest(r *http.Request) bool {
	return GetRequestStore(r).IsMetricsRequest
}

// IsReminderRequest tells if the request store marks the request as a remember (reminder) request.
func IsReminderRequest(r *http.Request) bool {
	return GetRequestStore(r).IsReminderRequest
}

// IsLockerRequest tells if the request store marks the request as a registry locker request.
func IsLockerRequest(r *http.Request) bool {
	return GetRequestStore(r).IsLockerRequest
}

// IsStatusRequest tells if the request store marks the request as a status request.
func IsStatusRequest(r *http.Request) bool {
	return GetRequestStore(r).IsStatusRequest
}

// IsDelayRequest tells if the request store marks the request as a delay request.
func IsDelayRequest(r *http.Request) bool {
	return GetRequestStore(r).IsDelayRequest
}

// IsPayloadRequest tells if the request store marks the request as a payload or stream request.
func IsPayloadRequest(r *http.Request) bool {
	return GetRequestStore(r).IsPayloadRequest
}

// IsProbeRequest tells if the request store marks the request as a readiness or liveness probe.
func IsProbeRequest(r *http.Request) bool {
	return GetRequestStore(r).IsProbeRequest
}

// IsHealthRequest tells if the request is a probe/health request, as flagged by the probe flag of the request store.
func IsHealthRequest(r *http.Request) bool {
	return GetRequestStore(r).IsProbeRequest
}

// IsVersionRequest tells if the request store marks the request as a version request.
func IsVersionRequest(r *http.Request) bool {
	return GetRequestStore(r).IsVersionRequest
}

// IsFilteredRequest tells if the request store marks the request as filtered out.
func IsFilteredRequest(r *http.Request) bool {
	return GetRequestStore(r).IsFilteredRequest
}

// IsTunnelRequest tells if the request is to be tunneled, which excludes the requests that configure tunnels.
func IsTunnelRequest(r *http.Request) bool {
	rs := GetRequestStore(r)
	return rs.IsTunnelRequest && !rs.IsTunnelConfigRequest
}

// IsKnownRequest tells if the request is one of goto's own kinds (probe, admin, metrics, status, delay, payload and the like) rather than ordinary traffic.
func IsKnownRequest(r *http.Request) bool {
	rs := GetRequestStore(r)
	return (rs.IsProbeRequest || rs.IsReminderRequest || rs.IsHealthRequest ||
		rs.IsMetricsRequest || rs.IsVersionRequest || rs.IsLockerRequest ||
		rs.IsAdminRequest || rs.IsStatusRequest || rs.IsDelayRequest ||
		rs.IsPayloadRequest || rs.IsTunnelConfigRequest)
}

// IsJSONContentType tells if the Content-Type header is the JSON content type.
func IsJSONContentType(h http.Header) bool {
	if contentType := h.Get(constants.HeaderContentType); contentType != "" {
		return strings.EqualFold(contentType, constants.ContentTypeJSON)
	}
	return false
}

// IsYAMLContentType tells if the Content-Type header is the YAML content type.
func IsYAMLContentType(h http.Header) bool {
	if contentType := h.Get(constants.HeaderContentType); contentType != "" {
		return strings.EqualFold(contentType, constants.ContentTypeYAML)
	}
	return false
}

// IsUTF8ContentType tells if the Content-Type header declares a UTF-8 charset.
func IsUTF8ContentType(h http.Header) bool {
	if contentType := h.Get(constants.HeaderContentType); contentType != "" {
		return utf8Regexp.MatchString(contentType)
	}
	return false
}

// IsBinaryContentHeader tells if the Content-Type header is present and is not a known text type.
func IsBinaryContentHeader(h http.Header) bool {
	if contentType := h.Get(constants.HeaderContentType); contentType != "" {
		return IsBinaryContentType(contentType)
	}
	return false
}

// IsBinaryContentType tells if the content type is not a known text mime type.
func IsBinaryContentType(contentType string) bool {
	return !knownTextMimeTypeRegexp.MatchString(contentType)
}

// IsInIntArray tells if the value is one of the elements of the array.
func IsInIntArray(value int, arr []int) bool {
	for _, v := range arr {
		if v == value {
			return true
		}
	}
	return false
}

// IsYes tells if the flag is a truthy word (y, yes, true or 1), ignoring case.
func IsYes(flag string) bool {
	return strings.EqualFold(flag, "y") || strings.EqualFold(flag, "yes") ||
		strings.EqualFold(flag, "true") || strings.EqualFold(flag, "1")
}
