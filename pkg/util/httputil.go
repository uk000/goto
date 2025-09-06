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

package util

import (
	"context"
	"encoding/json"
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/types"
	"io"
	"log"
	"net"
	"net/http"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"google.golang.org/grpc/metadata"
	"sigs.k8s.io/yaml"
)

var (
	contentRegexp           = regexp.MustCompile("(?i)content")
	hostRegexp              = regexp.MustCompile("(?i)^host$")
	tunnelRegexp            = regexp.MustCompile("(?i)tunnel")
	utf8Regexp              = regexp.MustCompile("(?i)utf-8")
	knownTextMimeTypeRegexp = regexp.MustCompile(".*(text|html|json|yaml|form).*")
	upgradeRegexp           = regexp.MustCompile("(?i)upgrade")
	ExcludedHeaders         = map[string]bool{}

	WillTunnel    func(*http.Request, *RequestStore) bool
	WillProxyHTTP func(*http.Request, *RequestStore) bool
	WillProxyGRPC func(int, any) bool
	WillProxyMCP  func(*http.Request, *RequestStore) bool
)

func WithPort(ctx context.Context, port int) context.Context {
	return context.WithValue(ctx, CurrentPortKey, port)
}

func SetSSE(ctx context.Context) context.Context {
	return context.WithValue(ctx, ProtocolKey, "SSE")
}

func IsSSE(ctx context.Context) bool {
	if val := ctx.Value(ProtocolKey); val != nil {
		return strings.EqualFold(val.(string), "SSE")
	}
	return false
}

func WithHTTPRW(r *http.Request, w http.ResponseWriter) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), HTTPRWKey, &types.Pair{Left: r, Right: w}))
}

func GetHTTPRWFromContext(ctx context.Context) (*http.Request, http.ResponseWriter) {
	if val := ctx.Value(HTTPRWKey); val != nil {
		pair := val.(*types.Pair)
		return pair.Left.(*http.Request), pair.Right.(http.ResponseWriter)
	}
	return nil, nil
}

func WithContextHeaders(ctx context.Context, headers map[string]string) context.Context {
	return context.WithValue(ctx, HeadersKey, headers)
}

func GetContextHeaders(ctx context.Context) map[string]string {
	if val := ctx.Value(HeadersKey); val != nil {
		return val.(map[string]string)
	}
	return nil
}

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

func AddLogMessage(msg string, r *http.Request) {
	rs := GetRequestStore(r)
	rs.LogMessages = append(rs.LogMessages, msg)
}

func LogMessage(ctx context.Context, msg string) {
	_, rs := GetRequestStoreForContext(ctx)
	rs.LogMessages = append(rs.LogMessages, msg)
}

func GetInterceptResponseWriter(r *http.Request) interface{} {
	return GetRequestStore(r).InterceptResponseWriter
}

func IsHeadersSent(r *http.Request) bool {
	return GetRequestStore(r).IsHeadersSent
}

func SetHeadersSent(r *http.Request, sent bool) {
	GetRequestStore(r).IsHeadersSent = sent
}

func GetTunnelCount(r *http.Request) int {
	return GetRequestStore(r).TunnelCount
}

func SetTunnelCount(r *http.Request, count int) {
	GetRequestStore(r).TunnelCount = count
}

func IsTrafficEventReported(r *http.Request) bool {
	return GetRequestStore(r).IsTrafficEventReported
}

func UpdateTrafficEventStatusCode(r *http.Request, statusCode int) {
	rs := GetRequestStore(r)
	if rs != nil && !rs.IsTrafficEventReported {
		rs.StatusCode = statusCode
	}
}

func UpdateTrafficEventDetails(r *http.Request, details string) {
	rs := GetRequestStore(r)
	if !rs.IsTrafficEventReported {
		rs.TrafficDetails = append(rs.TrafficDetails, details)
	}
}

func ReportTrafficEvent(r *http.Request) (int, []string) {
	rs := GetRequestStore(r)
	if !rs.IsTrafficEventReported {
		rs.IsTrafficEventReported = true
		return rs.StatusCode, rs.TrafficDetails
	}
	return 0, nil
}

func SetFilteredRequest(r *http.Request) {
	GetRequestStore(r).IsFilteredRequest = true
}

func SetTunnelRequest(r *http.Request) {
	GetRequestStore(r).IsTunnelRequest = true
}

func UnsetTunnelRequest(r *http.Request) {
	GetRequestStore(r).IsTunnelRequest = false
}

func GetPortNumFromGRPCAuthority(ctx context.Context) int {
	if headers, ok := metadata.FromIncomingContext(ctx); ok && len(headers[":authority"]) > 0 {
		if pieces := strings.Split(headers[":authority"][0], ":"); len(pieces) > 1 {
			if portNum, err := strconv.Atoi(pieces[1]); err == nil {
				return portNum
			}
		}
	}
	return global.Self.GRPCPort
}

func GetPortFromAddress(addr string) int {
	if pieces := strings.Split(addr, ":"); len(pieces) > 1 {
		if port, err := strconv.Atoi(pieces[len(pieces)-1]); err == nil {
			return port
		}
	}
	return 0
}

func GetPortValueFromLocalAddressContext(ctx context.Context) string {
	if val := ctx.Value(http.LocalAddrContextKey); val != nil {
		srvAddr := ctx.Value(http.LocalAddrContextKey).(net.Addr)
		if pieces := strings.Split(srvAddr.String(), ":"); len(pieces) > 1 {
			return pieces[len(pieces)-1]
		}
	}
	return ""
}

func GetContextPort(ctx context.Context) int {
	if val := ctx.Value(CurrentPortKey); val != nil {
		return val.(int)
	}
	if val := GetPortValueFromLocalAddressContext(ctx); val != "" {
		if port, err := strconv.Atoi(val); err == nil {
			return port
		}
	}
	return GetPortNumFromGRPCAuthority(ctx)
}

func GetCurrentPort(r *http.Request) int {
	return GetContextPort(r.Context())
}

func GetListenerPort(r *http.Request) string {
	return GetPortValueFromLocalAddressContext(r.Context())
}

func GetListenerPortNum(r *http.Request) int {
	return GetContextPort(r.Context())
}

func computeRequestPort(r *http.Request, rs *RequestStore) (port string, portNum int) {
	if port, _ = GetStringParam(r, "port"); port != "" {
		portNum, _ = strconv.Atoi(port)
	} else {
		portNum = GetListenerPortNum(r)
		port = strconv.Itoa(portNum)
	}
	if rs != nil {
		rs.RequestPort = port
		rs.RequestPortNum = portNum
		rs.RequestPortChecked = true
	}
	return
}

func getRequestOrListenerPort(r *http.Request) (port string, portNum int) {
	rs := GetRequestStoreIfPresent(r)
	if rs != nil && rs.RequestPortChecked {
		return rs.RequestPort, rs.RequestPortNum
	}
	return computeRequestPort(r, rs)
}

func GetRequestOrListenerPort(r *http.Request) string {
	port, _ := getRequestOrListenerPort(r)
	return port
}
func GetRequestOrListenerPortNum(r *http.Request) int {
	_, port := getRequestOrListenerPort(r)
	return port
}

func GetCurrentListenerLabel(r *http.Request) string {
	return global.Funcs.GetListenerLabelForPort(GetCurrentPort(r))
}

func ValidateListener(w http.ResponseWriter, r *http.Request) (bool, string) {
	port := GetIntParamValue(r, "port")
	if !global.Funcs.IsListenerPresent(port) {
		w.WriteHeader(http.StatusBadRequest)
		msg := fmt.Sprintf("No listener for port %d", port)
		fmt.Fprintln(w, msg)
		AddLogMessage(msg, r)
		return false, msg
	}
	return true, ""
}

func GetHeaderValues(r *http.Request) map[string]map[string]int {
	headerValuesMap := map[string]map[string]int{}
	for h, values := range r.Header {
		h = strings.ToLower(h)
		if headerValuesMap[h] == nil {
			headerValuesMap[h] = map[string]int{}
		}
		for _, value := range values {
			value = strings.ToLower(value)
			headerValuesMap[h][value]++
		}
	}
	return headerValuesMap
}

func GetQueryParams(r *http.Request) map[string]map[string]int {
	queryParamsMap := map[string]map[string]int{}
	for key, values := range r.URL.Query() {
		key = strings.ToLower(key)
		if queryParamsMap[key] == nil {
			queryParamsMap[key] = map[string]int{}
		}
		for _, value := range values {
			value = strings.ToLower(value)
			queryParamsMap[key][value]++
		}
	}
	return queryParamsMap
}

func AddHeaderWithPrefix(prefix, header, value string, headers map[string][]string) {
	key := fmt.Sprintf("%s%s", prefix, header)
	headers[key] = append(headers[key], value)
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

func CopyHeadersTo(prefix string, r *http.Request, out map[string][]string, copyHost, copyURI, copyContentType bool) {
	CopyHeadersWithIgnore(prefix, r, out, nil, ExcludedHeaders, copyHost, copyURI, copyContentType)
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

func GetHeadersLog(header http.Header) string {
	headers := map[string][]string{}
	for k, v := range header {
		if !ExcludedHeaders[strings.ToLower(k)] {
			headers[k] = v
		}
	}
	return ToJSONText(headers)
}

func ReadJsonPayload(r *http.Request, t interface{}) error {
	return ReadJsonPayloadFromBody(r.Body, t)
}

func ReadJsonPayloadFromBody(body io.ReadCloser, t interface{}) error {
	if body, err := io.ReadAll(body); err == nil {
		return json.Unmarshal(body, t)
	} else {
		return err
	}
}

func WriteJsonPayload(w http.ResponseWriter, t interface{}) string {
	w.Header().Add(constants.HeaderContentType, constants.ContentTypeJSON)
	return WriteJson(w, t)
}

func WriteStringJsonPayload(w http.ResponseWriter, json string) {
	w.Header().Add(constants.HeaderContentType, constants.ContentTypeJSON)
	fmt.Fprintln(w, json)
}

func WriteJson(w io.Writer, j interface{}) string {
	if reflect.ValueOf(j).IsNil() {
		fmt.Fprintln(w, "")
	} else {
		if bytes, err := json.MarshalIndent(j, "", "  "); err == nil {
			data := string(bytes)
			fmt.Fprintln(w, data)
			return data
		} else {
			fmt.Printf("Failed to write json payload: %s\n", err.Error())
		}
	}
	return ""
}

func WriteYaml(w io.Writer, t interface{}) string {
	data := ""
	if !reflect.ValueOf(t).IsNil() {
		if b, err := yaml.Marshal(t); err == nil {
			data = string(b)
		} else {
			fmt.Printf("Failed to marshal yaml with error: %s\n", err.Error())
		}
	}
	if w != nil {
		fmt.Fprintln(w, data)
	}
	return data
}

func WriteErrorJson(w http.ResponseWriter, error string) {
	fmt.Fprintf(w, "{\"error\":\"%s\"}", error)
}

func ToBytes(v any) []byte {
	if b, err := json.Marshal(v); err == nil {
		return b
	} else {
		fmt.Printf("Failed to marshal value to bytes: %s\n", err.Error())
	}
	return nil
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
	return uri == "metrics" || uri == "server" || uri == "request" || uri == "response" || uri == "listeners" ||
		uri == "label" || uri == "registry" || uri == "client" || uri == "proxy" || uri == "job" || uri == "probes" ||
		uri == "tcp" || uri == "log" || uri == "events" || uri == "tunnels" || uri == "grpc" || uri == "jsonrpc" ||
		uri == "k8s" || uri == "pipes" || uri == "scripts" || uri == "tls" || uri == "routing" || uri == "mcpapi"
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

func IsKnownNonTraffic(r *http.Request) bool {
	rs := GetRequestStore(r)
	return rs.IsProbeRequest || rs.IsReminderRequest || rs.IsHealthRequest ||
		rs.IsMetricsRequest || rs.IsVersionRequest || rs.IsLockerRequest ||
		rs.IsAdminRequest || rs.IsTunnelConfigRequest
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

func DiscardRequestBody(r *http.Request) int {
	defer r.Body.Close()
	len, _ := io.Copy(io.Discard, r.Body)
	return int(len)
}

func DiscardResponseBody(r *http.Response) int {
	defer r.Body.Close()
	len, _ := io.Copy(io.Discard, r.Body)
	return int(len)
}

func CloseResponse(r *http.Response) {
	defer r.Body.Close()
	io.Copy(io.Discard, r.Body)
}

func TransformPayload(sourcePayload string, transforms []*Transform, isYaml bool) string {
	var sourceJSON JSON
	isYAML := false
	if isYaml {
		sourceJSON = JSONFromYAML(sourcePayload)
		isYAML = true
	} else {
		sourceJSON = JSONFromJSONText(sourcePayload)
	}
	if sourceJSON.IsEmpty() {
		return sourcePayload
	}
	targetPayload := ""
	for _, t := range transforms {
		var targetJSON JSON
		if t.Payload != nil {
			targetJSON = JSONFromJSON(t.Payload).Clone()
		} else {
			targetJSON = sourceJSON
		}
		if targetJSON != nil && !targetJSON.IsEmpty() {
			if targetJSON.Transform(t.Mappings, sourceJSON) {
				if isYAML {
					targetPayload = targetJSON.ToYAML()
				} else {
					targetPayload = targetJSON.ToJSONText()
				}
			}
			targetPayload = targetJSON.TransformPatterns(targetPayload)
		}
		if targetPayload != "" {
			break
		}
	}
	if targetPayload == "" {
		targetPayload = sourcePayload
	}
	return targetPayload
}

func SubstitutePayloadMarkers(payload string, keys []string, values map[string]string) string {
	for _, key := range keys {
		if values[key] != "" {
			payload = strings.Replace(payload, MarkFiller(key), values[key], -1)
		}
	}
	return payload
}

func IsH2Upgrade(r *http.Request) bool {
	return r.Method == "PRI" || strings.EqualFold(r.Header.Get("Upgrade"), "h2c") || upgradeRegexp.MatchString(r.Header.Get("Connection"))
}

func IsPutOrPost(r *http.Request) bool {
	return strings.EqualFold(r.Method, "POST") || strings.EqualFold(r.Method, "PUT")
}

func matchPieces(pieces1 []string, pieces2 []string) bool {
	if len(pieces1) != len(pieces2) {
		return false
	}
	for i, piece1 := range pieces1 {
		piece2 := pieces2[i]
		if piece1 != piece2 &&
			!((strings.HasPrefix(piece1, "{") && strings.HasSuffix(piece1, "}")) ||
				(strings.HasPrefix(piece2, "{") && strings.HasSuffix(piece2, "}"))) {
			return false
		}
	}
	return true
}

func getURIPieces(uri string) []string {
	uri = strings.ToLower(uri)
	return strings.Split(strings.Split(uri, "?")[0], "/")
}

func MatchURI(uri1 string, uri2 string) bool {
	return matchPieces(getURIPieces(uri1), getURIPieces(uri2))
}

func FindURIInMap(uri string, i interface{}) string {
	if m := reflect.ValueOf(i); m.Kind() == reflect.Map {
		uriPieces1 := getURIPieces(uri)
		for _, k := range m.MapKeys() {
			uri2 := k.String()
			uriPieces2 := getURIPieces(uri2)
			if matchPieces(uriPieces1, uriPieces2) {
				return uri2
			}
		}
	}
	return ""
}

func IsURIInMap(uri string, m map[string]interface{}) bool {
	return FindURIInMap(uri, m) != ""
}

func TransformHeaders(headers []string) [][2]string {
	newHeaders := [][2]string{}
	for _, h := range headers {
		newHeaders = append(newHeaders, [2]string{h, ""})
	}
	return newHeaders
}

func MatchAllHeaders(headers http.Header, expected [][]string) bool {
	for _, ehArr := range expected {
		if len(ehArr) < 1 {
			continue
		}
		hv := headers.Get(ehArr[0])
		if hv == "" {
			continue
		}
		if len(ehArr) == 1 {
			return true
		}
		if ehArr[1] == "" || strings.EqualFold(ehArr[1], hv) {
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

func ParseTrackingHeaders(headers string) ([]string, map[string][]string) {
	trackingHeaders := []string{}
	crossTrackingHeaders := map[string][]string{}
	pieces := strings.Split(headers, ",")
	for _, piece := range pieces {
		crossHeaders := strings.Split(piece, "|")
		for i, h := range crossHeaders {
			crossHeaders[i] = strings.ToLower(h)
		}
		if len(crossHeaders) > 1 {
			crossTrackingHeaders[crossHeaders[0]] = append(crossTrackingHeaders[crossHeaders[0]], crossHeaders[1:]...)
		}
		for _, h := range crossHeaders {
			exists := false
			for _, eh := range trackingHeaders {
				if strings.EqualFold(h, eh) {
					exists = true
				}
			}
			if !exists {
				trackingHeaders = append(trackingHeaders, strings.ToLower(h))
			}
		}
	}
	return trackingHeaders, crossTrackingHeaders
}

func ParseTimeBuckets(b string) ([][]int, bool) {
	pieces := strings.Split(b, ",")
	buckets := [][]int{}
	var e error
	hasError := false
	for _, piece := range pieces {
		bucket := strings.Split(piece, "-")
		low := 0
		high := 0
		if len(bucket) == 2 {
			if low, e = strconv.Atoi(bucket[0]); e == nil {
				high, e = strconv.Atoi(bucket[1])
			}
		}
		if e != nil || low < 0 || high < 0 || (low == 0 && high == 0) || (high != 0 && high < low) {
			hasError = true
			break
		} else {
			buckets = append(buckets, []int{low, high})
		}
	}
	return buckets, !hasError
}

func BuildCrossHeadersMap(crossTrackingHeaders map[string][]string) map[string]string {
	crossHeadersMap := map[string]string{}
	for header, subheaders := range crossTrackingHeaders {
		for _, subheader := range subheaders {
			crossHeadersMap[subheader] = header
		}
	}
	return crossHeadersMap
}

func UpdateTrackingCountsByURIAndID(id string, uri string,
	countsByIDs map[string]int,
	countsByURIs map[string]int,
	countsByURIIDs map[string]map[string]int,
) {
	if countsByIDs != nil {
		countsByIDs[id]++
	}
	if countsByURIs != nil {
		countsByURIs[uri]++
	}
	if countsByURIIDs != nil {
		if countsByURIIDs[uri] == nil {
			countsByURIIDs[uri] = map[string]int{}
		}
		countsByURIIDs[uri][id]++
	}
}

func UpdateTrackingCountsByURIKeyValuesID(id string, uri string,
	trackingKeys []string,
	actualKeyValues map[string][]string,
	countsByKeys map[string]int,
	countsByKeyValues map[string]map[string]int,
	countsByURIKeys map[string]map[string]int,
	countsByKeyIDs map[string]map[string]int,
) {
	if trackingKeys == nil {
		return
	}
	for _, key := range trackingKeys {
		if values := actualKeyValues[key]; len(values) > 0 {
			if countsByKeys != nil {
				countsByKeys[key]++
			}
			if countsByKeyValues != nil {
				if countsByKeyValues[key] == nil {
					countsByKeyValues[key] = map[string]int{}
				}
				countsByKeyValues[key][values[0]]++
			}
			if countsByURIKeys != nil {
				if countsByURIKeys[uri] == nil {
					countsByURIKeys[uri] = map[string]int{}
				}
				countsByURIKeys[uri][key]++
			}
			if countsByURIKeys != nil {
				if countsByURIKeys[uri] == nil {
					countsByURIKeys[uri] = map[string]int{}
				}
				countsByURIKeys[uri][key]++
			}
			if countsByKeyIDs != nil {
				if countsByKeyIDs[key] == nil {
					countsByKeyIDs[key] = map[string]int{}
				}
				countsByKeyIDs[key][id]++
			}
		}
	}
}

func GotoProtocol(isH2, isTLS bool) string {
	protocol := "HTTP"
	if isTLS {
		if isH2 {
			protocol = "HTTP/2"
		} else {
			protocol = "HTTPS"
		}
	} else if isH2 {
		protocol = "H2C"
	}
	return protocol
}

func PrintRequest(r *http.Request) {
	log.Printf(">> Method: %s", ToJSONText(r.Method))
	log.Printf(">> URI: %s", ToJSONText(r.RequestURI))
	log.Printf(">> Headers: %s", ToJSONText(r.Header))
	log.Printf(">> Query: %s", ToJSONText(r.URL.Query()))
	if rr, ok := r.Body.(*ReReader); ok {
		log.Printf(">> Body: %s", string(rr.Content))
	}
}

func PrintResponse(w http.ResponseWriter) {
	log.Println(ToJSONText(w))
}
