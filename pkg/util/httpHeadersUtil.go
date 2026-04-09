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
	"goto/pkg/types"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

func WithRequestHeaders(ctx context.Context, headers map[string][]string) context.Context {
	return context.WithValue(ctx, RequestHeadersKey, headers)
}

func GetRequestHeaders(ctx context.Context) map[string][]string {
	if val := ctx.Value(RequestHeadersKey); val != nil {
		return val.(map[string][]string)
	}
	return nil
}

func WithContextHeaders(ctx context.Context, headers *types.Headers) context.Context {
	return context.WithValue(ctx, HeadersKey, headers)
}

func GetContextHeaders(ctx context.Context) *types.Headers {
	if val := ctx.Value(RequestHeadersKey); val != nil {
		return val.(*types.Headers)
	}
	return nil
}

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

func AddHeaderWithPrefix(prefix, header, value any, headers map[string][]string) {
	key := fmt.Sprintf("%s%s", prefix, header)
	headers[key] = append(headers[key], fmt.Sprint(value))
}

func AddHeaderWithPrefixL(prefix, header, value string, headers map[string][]string) {
	key := strings.ToLower(fmt.Sprintf("%s%s", prefix, header))
	headers[key] = append(headers[key], value)
}

func AddHeaderWithSuffix(header, suffix, value string, headers map[string][]string) {
	key := fmt.Sprintf("%s%s", header, suffix)
	headers[key] = append(headers[key], value)
}

func AddHeaderWithSuffixL(header, suffix, value string, headers map[string][]string) {
	key := strings.ToLower(fmt.Sprintf("%s%s", header, suffix))
	headers[key] = append(headers[key], value)
}

func CopyHeadersWithPrefix(prefix string, in, out map[string][]string) {
	for h, values := range in {
		for _, v := range values {
			AddHeaderWithPrefix(prefix, h, v, out)
		}
	}
}

func CopyHeaders(prefix string, r *http.Request, w http.ResponseWriter, headers http.Header, copyHost, copyURI, copyContentType bool) {
	CopyHeadersWithIgnore(prefix, r, w.Header(), headers, ExcludedHeaders, copyHost, copyURI, copyContentType)
}

func CopyHeadersWithIgnore(prefix string, r *http.Request, out map[string][]string, headers http.Header, ignoreHeaders map[string]bool, copyHost, copyURI, copyContentType bool) {
	rs := GetRequestStore(r)
	hostCopied := false
	if prefix != "" {
		prefix += "-"
		AddHeaderWithPrefix(prefix, "Payload-Size", strconv.Itoa(rs.RequestPayloadSize), out)
		if !hostCopied && copyHost && r != nil {
			AddHeaderWithPrefix(prefix, "Host", r.Host, out)
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
	for h, values := range headers {
		lh := strings.ToLower(h)
		if ignoreHeaders[lh] {
			continue
		}
		if !copyContentType && contentRegexp.MatchString(h) {
			continue
		}
		for _, v := range values {
			AddHeaderWithPrefix(prefix, h, v, out)
		}
		if hostRegexp.MatchString(h) {
			hostCopied = true
		}
	}
}

func ToLowerHeaders(headers map[string][]string) map[string][]string {
	newHeaders := map[string][]string{}
	for h, v := range headers {
		newHeaders[strings.ToLower(h)] = v
	}
	return newHeaders
}

func ToLowerHeadersValues(headers map[string][]string) map[string]string {
	newHeaders := map[string]string{}
	for h, v := range headers {
		if len(v) > 0 {
			newHeaders[strings.ToLower(h)] = v[0]
		}
	}
	return newHeaders
}

func ToLowerHeader(headers map[string]string) map[string]string {
	newHeaders := map[string]string{}
	for h, v := range headers {
		newHeaders[strings.ToLower(h)] = v
	}
	return newHeaders
}

func GetHeadersLog(header http.Header) string {
	headers := map[string][]string{}
	for k, v := range header {
		if !ExcludedHeaders[strings.ToLower(k)] {
			headers[k] = v
		}
	}
	return ToJSONText(headers)
}

func ConvertHeadersArrayToMultiArray(headers []string) [][2]string {
	newHeaders := [][2]string{}
	for _, h := range headers {
		newHeaders = append(newHeaders, [2]string{h, ""})
	}
	return newHeaders
}

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

func ContainsAllHeaders(headers http.Header, expected map[string]*regexp.Regexp) bool {
	for h, r := range expected {
		if h != "" && (headers[h] == nil || r != nil && !StringArrayContains(headers[h], r)) {
			return false
		}
	}
	return true
}

func TransformHeaders(vars, sourceHeaders map[string]string, addHeaders map[string]string, removeHeaders []string) (map[string]string, string) {
	headers := map[string]string{}
	host := ""
	cleanStart := false
	if len(removeHeaders) == 1 && removeHeaders[0] == "*" {
		cleanStart = true
		removeHeaders = nil
	}
	if !cleanStart {
		for k, v := range sourceHeaders {
			if strings.EqualFold(k, "Host") || strings.EqualFold(k, ":authority") {
				host = v
			} else {
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
