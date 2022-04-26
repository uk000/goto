/**
 * Copyright 2021 uk
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
  . "goto/pkg/constants"
  "goto/pkg/global"
  "io"
  "io/ioutil"
  "net"
  "net/http"
  "reflect"
  "regexp"
  "strconv"
  "strings"
  "sync"

  "github.com/gorilla/mux"
  "google.golang.org/grpc/metadata"
  "sigs.k8s.io/yaml"
)

type ContextKey struct{ Key string }

type RequestStore struct {
  IsVersionRequest        bool
  IsFilteredRequest       bool
  IsLockerRequest         bool
  IsPeerEventsRequest     bool
  IsAdminRequest          bool
  IsMetricsRequest        bool
  IsReminderRequest       bool
  IsProbeRequest          bool
  IsHealthRequest         bool
  IsStatusRequest         bool
  IsDelayRequest          bool
  IsPayloadRequest        bool
  IsTunnelConnectRequest  bool
  IsTunnelRequest         bool
  IsTunnelConfigRequest   bool
  IsTrafficEventReported  bool
  IsHeadersSent           bool
  IsTunnelResponseSent    bool
  GotoProtocol            string
  StatusCode              int
  IsH2                    bool
  IsH2C                   bool
  IsTLS                   bool
  ServerName              string
  TLSVersion              string
  TLSVersionNum           uint16
  RequestPayload          string
  RequestPayloadSize      int
  TrafficDetails          []string
  LogMessages             []string
  InterceptResponseWriter interface{}
  TunnelCount             int
  RequestedTunnels        []string
  TunnelEndpoints         interface{}
  TunnelLock              sync.RWMutex
}

var (
  RequestStoreKey   = &ContextKey{"requestStore"}
  CurrentPortKey    = &ContextKey{"currentPort"}
  IgnoredRequestKey = &ContextKey{"ignoredRequest"}
  ConnectionKey     = &ContextKey{"connection"}

  contentRegexp           = regexp.MustCompile("(?i)content")
  hostRegexp              = regexp.MustCompile("(?i)^host$")
  tunnelRegexp            = regexp.MustCompile("(?i)tunnel")
  utf8Regexp              = regexp.MustCompile("(?i)utf-8")
  knownTextMimeTypeRegexp = regexp.MustCompile(".*(text|html|json|yaml|form).*")
  upgradeRegexp           = regexp.MustCompile("(?i)upgrade")

  WillTunnel func(*http.Request, *RequestStore) bool
)

func GetRequestStore(r *http.Request) *RequestStore {
  if val := r.Context().Value(RequestStoreKey); val != nil {
    return val.(*RequestStore)
  }
  _, rs := WithRequestStore(r)
  return rs
}

func WithRequestStore(r *http.Request) (context.Context, *RequestStore) {
  rs := &RequestStore{}
  ctx := context.WithValue(r.Context(), RequestStoreKey, rs)
  isAdminRequest := CheckAdminRequest(r)
  rs.IsAdminRequest = isAdminRequest
  rs.IsVersionRequest = strings.HasPrefix(r.RequestURI, "/version")
  rs.IsLockerRequest = strings.HasPrefix(r.RequestURI, "/registry") && strings.Contains(r.RequestURI, "/locker")
  rs.IsPeerEventsRequest = strings.HasPrefix(r.RequestURI, "/registry") && strings.Contains(r.RequestURI, "/events")
  rs.IsMetricsRequest = strings.HasPrefix(r.RequestURI, "/metrics") || strings.HasPrefix(r.RequestURI, "/stats")
  rs.IsReminderRequest = strings.Contains(r.RequestURI, "/remember")
  rs.IsProbeRequest = global.IsReadinessProbe(r) || global.IsLivenessProbe(r)
  rs.IsHealthRequest = !isAdminRequest && strings.HasPrefix(r.RequestURI, "/health")
  rs.IsStatusRequest = !isAdminRequest && strings.HasPrefix(r.RequestURI, "/status")
  rs.IsDelayRequest = !isAdminRequest && strings.Contains(r.RequestURI, "/delay")
  rs.IsPayloadRequest = !isAdminRequest && (strings.Contains(r.RequestURI, "/stream") || strings.Contains(r.RequestURI, "/payload"))
  rs.IsTunnelRequest = strings.HasPrefix(r.RequestURI, "/tunnel=") || !isAdminRequest && WillTunnel(r, rs)
  rs.IsTunnelConfigRequest = strings.HasPrefix(r.RequestURI, "/tunnels")
  rs.IsH2C = r.ProtoMajor == 2
  return ctx, rs
}

func IsH2(r *http.Request) bool {
  return r.ProtoMajor == 2
}

func IsH2C(r *http.Request) bool {
  return GetRequestStore(r).IsH2C
}

func InitListenerRouter(root *mux.Router) {
  portRouter = root.PathPrefix("/port={port}").Subrouter()
}

func AddLogMessage(msg string, r *http.Request) {
  rs := GetRequestStore(r)
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

func SetFiltreredRequest(r *http.Request) {
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
  return global.ServerPort
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

func GetRequestOrListenerPort(r *http.Request) string {
  port, ok := GetStringParam(r, "port")
  if !ok {
    port = GetListenerPort(r)
  }
  return port
}

func GetRequestOrListenerPortNum(r *http.Request) int {
  port, ok := GetIntParam(r, "port")
  if !ok {
    port = GetListenerPortNum(r)
  }
  return port
}

func GetCurrentListenerLabel(r *http.Request) string {
  return global.GetListenerLabelForPort(GetCurrentPort(r))
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

func AddHeaderWithPrefix(prefix, header, value string, headers http.Header) {
  if prefix != "" {
    header = prefix + "-" + header
  }
  headers.Add(header, value)
}

func CopyHeaders(prefix string, r *http.Request, w http.ResponseWriter, headers http.Header, copyHost, copyURI, copyContentType bool) {
  rs := GetRequestStore(r)
  hostCopied := false
  responseHeaders := w.Header()
  for h, values := range headers {
    if !copyContentType && contentRegexp.MatchString(h) {
      continue
    }
    for _, v := range values {
      AddHeaderWithPrefix(prefix, h, v, responseHeaders)
    }
    if hostRegexp.MatchString(h) {
      hostCopied = true
    }
  }
  AddHeaderWithPrefix(prefix, "Payload-Size", strconv.Itoa(rs.RequestPayloadSize), responseHeaders)
  if !hostCopied && copyHost {
    AddHeaderWithPrefix(prefix, "Host", r.Host, responseHeaders)
  }
  if copyURI {
    AddHeaderWithPrefix(prefix, "URI", r.RequestURI, responseHeaders)
  }
  if rs.IsTLS && copyHost {
    if rs.ServerName != "" {
      AddHeaderWithPrefix(prefix, "TLS-SNI", rs.ServerName, responseHeaders)
    }
    if rs.TLSVersion != "" {
      AddHeaderWithPrefix(prefix, "TLS-Version", rs.TLSVersion, responseHeaders)
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
  return ToJSONText(header)
}

func ReadJsonPayload(r *http.Request, t interface{}) error {
  return ReadJsonPayloadFromBody(r.Body, t)
}

func ReadJsonPayloadFromBody(body io.ReadCloser, t interface{}) error {
  if body, err := ioutil.ReadAll(body); err == nil {
    return json.Unmarshal(body, t)
  } else {
    return err
  }
}

func WriteJsonPayload(w http.ResponseWriter, t interface{}) string {
  w.Header().Add(HeaderContentType, ContentTypeJSON)
  return WriteJson(w, t)
}

func WriteStringJsonPayload(w http.ResponseWriter, json string) {
  w.Header().Add(HeaderContentType, ContentTypeJSON)
  fmt.Fprintln(w, json)
}

func WriteJson(w io.Writer, t interface{}) string {
  if reflect.ValueOf(t).IsNil() {
    fmt.Fprintln(w, "")
  } else {
    if bytes, err := json.Marshal(t); err == nil {
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
  if reflect.ValueOf(t).IsNil() {
    fmt.Fprintln(w, "")
  } else if b, err := yaml.Marshal(t); err == nil {
    data := string(b)
    fmt.Fprintln(w, data)
    return data
  } else {
    fmt.Printf("Failed to marshal yaml with error: %s\n", err.Error())
  }
  return ""
}

func WriteErrorJson(w http.ResponseWriter, error string) {
  fmt.Fprintf(w, "{\"error\":\"%s\"}", error)
}

func IsAdminRequest(r *http.Request) bool {
  return GetRequestStore(r).IsAdminRequest
}

func CheckAdminRequest(r *http.Request) bool {
  uri := r.RequestURI
  if strings.HasPrefix(uri, "/port=") {
    uri = strings.Split(uri, "/port=")[1]
  }
  if pieces := strings.Split(uri, "/"); len(pieces) > 1 {
    uri = pieces[1]
  }
  return uri == "metrics" || uri == "server" || uri == "request" || uri == "response" ||
    uri == "listeners" || uri == "label" || uri == "registry" || uri == "client" ||
    uri == "job" || uri == "probes" || uri == "tcp" || uri == "log" || uri == "events" || uri == "tunnels"
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
  if contentType := h.Get("Content-Type"); contentType != "" {
    return strings.EqualFold(contentType, ContentTypeJSON)
  }
  return false
}

func IsYAMLContentType(h http.Header) bool {
  if contentType := h.Get("Content-Type"); contentType != "" {
    return strings.EqualFold(contentType, ContentTypeYAML)
  }
  return false
}

func IsUTF8ContentType(h http.Header) bool {
  if contentType := h.Get("Content-Type"); contentType != "" {
    return utf8Regexp.MatchString(contentType)
  }
  return false
}

func IsBinaryContentHeader(h http.Header) bool {
  if contentType := h.Get("Content-Type"); contentType != "" {
    return IsBinaryContentType(contentType)
  }
  return false
}

func IsBinaryContentType(contentType string) bool {
  return !knownTextMimeTypeRegexp.MatchString(contentType)
}

func DiscardRequestBody(r *http.Request) int {
  defer r.Body.Close()
  len, _ := io.Copy(ioutil.Discard, r.Body)
  return int(len)
}

func DiscardResponseBody(r *http.Response) int {
  defer r.Body.Close()
  len, _ := io.Copy(ioutil.Discard, r.Body)
  return int(len)
}

func CloseResponse(r *http.Response) {
  defer r.Body.Close()
  io.Copy(ioutil.Discard, r.Body)
}

func TransformPayload(sourcePayload string, transforms []*Transform, isYaml bool) string {
  var sourceJSON JSON
  isYAML := false
  if isYaml {
    sourceJSON = FromYAML(sourcePayload)
    isYAML = true
  } else {
    sourceJSON = FromJSONText(sourcePayload)
  }
  if sourceJSON.IsEmpty() {
    return sourcePayload
  }
  targetPayload := ""
  for _, t := range transforms {
    var targetJSON JSON
    if t.Payload != nil {
      targetJSON = FromJSON(t.Payload).Clone()
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
    if e != nil || low < 0 || high < 0 || (low == 0 && high == 0) || high < low {
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
