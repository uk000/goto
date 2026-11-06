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
	"context"
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/types"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// WithRequestHeaders returns a context that carries the given request headers.
func WithRequestHeaders(ctx context.Context, headers map[string][]string) context.Context {
	return context.WithValue(ctx, RequestHeadersKey, headers)
}

// GetRequestHeaders returns the request headers carried by the context, or nil if there are none.
func GetRequestHeaders(ctx context.Context) map[string][]string {
	if headers, ok := ctx.Value(RequestHeadersKey).(map[string][]string); ok {
		return headers
	}
	return nil
}

// WithContextHeaders returns a context that carries the given headers config.
func WithContextHeaders(ctx context.Context, headers *types.Headers) context.Context {
	return context.WithValue(ctx, HeadersKey, headers)
}

// GetContextHeaders returns the headers config carried by the context, or nil if there is none.
func GetContextHeaders(ctx context.Context) *types.Headers {
	if headers, ok := ctx.Value(HeadersKey).(*types.Headers); ok {
		return headers
	}
	return nil
}

// GetHeaderValues returns the first value of each request header, keyed by the lower-cased header name.
func GetHeaderValues(r *http.Request) map[string]string {
	headerValuesMap := map[string]string{}
	for h, values := range r.Header {
		if len(values) > 0 {
			h = strings.ToLower(h)
			headerValuesMap[h] = values[0]
		}
	}
	return headerValuesMap
}

// GetQueryParams returns the first value of each query param of the request, keyed by the lower-cased param name.
func GetQueryParams(r *http.Request) map[string]string {
	queryParamsMap := map[string]string{}
	for key, values := range r.URL.Query() {
		if len(values) > 0 {
			key = strings.ToLower(key)
			queryParamsMap[key] = values[0]
		}
	}
	return queryParamsMap
}

// AddHeaderWithPrefix appends the value to the header that is named prefix+header, keeping the case.
func AddHeaderWithPrefix(prefix, header, value any, headers map[string][]string) {
	key := fmt.Sprintf("%s%s", prefix, header)
	headers[key] = append(headers[key], fmt.Sprint(value))
}

// AddHeaderWithPrefixL appends the value to the header that is named prefix+header, in lower case.
func AddHeaderWithPrefixL(prefix, header, value string, headers map[string][]string) {
	key := strings.ToLower(fmt.Sprintf("%s%s", prefix, header))
	headers[key] = append(headers[key], value)
}

// AddHeaderWithSuffix appends the value to the header that is named header+suffix, keeping the case.
func AddHeaderWithSuffix(header, suffix, value string, headers map[string][]string) {
	key := fmt.Sprintf("%s%s", header, suffix)
	headers[key] = append(headers[key], value)
}

// AddHeaderWithSuffixL appends the value to the header that is named header+suffix, in lower case.
func AddHeaderWithSuffixL(header, suffix, value string, headers map[string][]string) {
	key := strings.ToLower(fmt.Sprintf("%s%s", header, suffix))
	headers[key] = append(headers[key], value)
}

// CopyHeadersWithPrefix copies all the values of the in headers to the out headers, adding the prefix to the header names.
func CopyHeadersWithPrefix(prefix string, in, out map[string][]string) {
	for h, values := range in {
		for _, v := range values {
			AddHeaderWithPrefix(prefix, h, v, out)
		}
	}
}

// CopyHeaders copies the request details and the given headers (the request's if nil) onto the response headers under the prefix, skipping the excluded headers.
func CopyHeaders(prefix string, r *http.Request, w http.ResponseWriter, headers http.Header, copyHost, copyURI, copyContentType bool) {
	CopyRequestHeadersWithIgnore(prefix, r, w.Header(), headers, excludedHeadersSnapshot(), copyHost, copyURI, copyContentType)
}

// CopyRequestHeadersWithIgnore adds prefixed request details to out, and then copies the headers (the request's if nil) except the ignored ones.
func CopyRequestHeadersWithIgnore(prefix string, r *http.Request, out map[string][]string, headers http.Header, ignoreHeaders map[string]bool, copyHost, copyURI, copyContentType bool) {
	rs := GetRequestStore(r)
	hostCopied := false
	if prefix != "" {
		prefix += "-"
		if rs.RequestPayloadSize > 0 {
			AddHeaderWithPrefix(prefix, constants.HeaderPayloadSize, strconv.Itoa(rs.RequestPayloadSize), out)
		}
		AddHeaderWithPrefix(prefix, constants.HeaderMethod, r.Method, out)

		if !hostCopied && copyHost && r != nil {
			AddHeaderWithPrefix(prefix, "Host", r.Host, out)
			hostCopied = true
		}
		if copyURI && r != nil {
			AddHeaderWithPrefix(prefix, "URI", r.RequestURI, out)
		}
		if rs.IsTLS && copyHost {
			if rs.ServerName != "" {
				AddHeaderWithPrefix(prefix, "TLS-SNI", rs.ServerName, out)
			}
			if rs.TLSVersion != "" {
				AddHeaderWithPrefix(prefix, "TLS-Version", rs.TLSVersion, out)
			}
		}
	}
	if headers == nil {
		headers = r.Header
	}
	CopyHeadersWithIgnore(prefix, headers, out, ignoreHeaders, !hostCopied && copyHost, copyURI, copyContentType)
}

// CopyHeadersWithIgnore copies the headers to out under the prefix, skipping the ignored ones and the ones that out already has.
func CopyHeadersWithIgnore(prefix string, in, out http.Header, ignoreHeaders map[string]bool, copyHost, copyURI, copyContentType bool) {
	for h, values := range in {
		lh := strings.ToLower(h)
		if out.Get(h) != "" || out.Get(lh) != "" {
			continue
		}
		if ignoreHeaders[lh] {
			continue
		}
		if !copyContentType && contentRegexp.MatchString(h) {
			continue
		}
		if !copyHost && hostRegexp.MatchString(h) {
			continue
		}
		for _, v := range values {
			AddHeaderWithPrefix(prefix, h, v, out)
		}
	}
}

// ToLowerHeaders returns a copy of the headers with lower-cased names.
func ToLowerHeaders(headers map[string][]string) map[string][]string {
	newHeaders := map[string][]string{}
	for h, v := range headers {
		newHeaders[strings.ToLower(h)] = v
	}
	return newHeaders
}

// ToLowerHeadersValues returns a copy of the headers with lower-cased names, keeping the first value of each.
func ToLowerHeadersValues(headers map[string][]string) map[string]string {
	newHeaders := map[string]string{}
	for h, v := range headers {
		if len(v) > 0 {
			newHeaders[strings.ToLower(h)] = v[0]
		}
	}
	return newHeaders
}

// ToLowerHeader returns a copy of the single-valued headers with lower-cased names.
func ToLowerHeader(headers map[string]string) map[string]string {
	newHeaders := map[string]string{}
	for h, v := range headers {
		newHeaders[strings.ToLower(h)] = v
	}
	return newHeaders
}

// GetHeadersLog returns the headers, except the excluded ones, as JSON text for logging.
func GetHeadersLog(header http.Header) string {
	headers := map[string][]string{}
	for k, v := range header {
		if !IsHeaderExcluded(k) {
			headers[k] = v
		}
	}
	return ToJSONText(headers)
}

// ConvertHeadersArrayToMultiArray turns the header names into [name, ""] pairs.
func ConvertHeadersArrayToMultiArray(headers []string) [][2]string {
	newHeaders := [][2]string{}
	for _, h := range headers {
		newHeaders = append(newHeaders, [2]string{h, ""})
	}
	return newHeaders
}

// MatchAllHeaders tells if any of the expected headers is present with the expected value (any value if the expected one is empty).
func MatchAllHeaders(headers http.Header, expected map[string]string) bool {
	for eh, ehv := range expected {
		if eh == "" {
			continue
		}
		hv := headers.Get(eh)
		if hv == "" {
			continue
		}
		if len(ehv) == 0 {
			return true
		}
		if ehv == "" || strings.EqualFold(ehv, hv) {
			return true
		}
	}
	return false
}

// GetIfAnyHeaderMatched returns the data of the first expected header whose value matches (any value if the expected one is empty), or nil.
func GetIfAnyHeaderMatched(headers http.Header, expected map[string]map[string]interface{}) interface{} {
	for eh, ehMap := range expected {
		if eh == "" {
			continue
		}
		hv := headers.Get(eh)
		if hv == "" {
			continue
		}
		for ehv, data := range ehMap {
			if ehv == "" || strings.EqualFold(ehv, hv) {
				return data
			}
		}
	}
	return nil
}

// ContainsAllHeaders tells if every expected header is present and, where a regexp is given, one of its values matches it.
func ContainsAllHeaders(headers http.Header, expected map[string]*regexp.Regexp) bool {
	for h, r := range expected {
		if h != "" && (headers[h] == nil || r != nil && !StringArrayContains(headers[h], r)) {
			return false
		}
	}
	return true
}

// TransformHeaders builds headers from the source ones by renaming, removing and adding headers, and returns them with the resulting host.
func TransformHeaders(vars, sourceHeaders map[string]string, addHeaders, mapHeaders map[string]string, removeHeaders []string) (map[string]string, string) {
	headers := map[string]string{}
	host := ""
	cleanStart := false
	if len(removeHeaders) == 1 && removeHeaders[0] == "*" {
		cleanStart = true
		removeHeaders = nil
	}

	for k, v := range sourceHeaders {
		if strings.EqualFold(k, "Host") || strings.EqualFold(k, ":authority") {
			host = v
		} else {
			k2, ok := mapHeaders[k]
			if !ok {
				k2, ok = mapHeaders[strings.ToLower(k)]
			}
			if ok {
				k = k2
			}
			if !cleanStart || k2 != "" {
				headers[k] = v
			}
		}
	}
	for _, h := range removeHeaders {
		delete(headers, h)
	}
	for h, hv := range addHeaders {
		hv = FillValues(hv, vars)
		if strings.EqualFold(h, "Host") || strings.EqualFold(h, ":authority") {
			host = hv
		}
		headers[h] = hv
	}
	return headers, host
}
