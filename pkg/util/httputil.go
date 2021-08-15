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
  "bytes"
  "context"
  "crypto/tls"
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
  "time"

  "github.com/gorilla/mux"
  "golang.org/x/net/http2"
  "google.golang.org/grpc/metadata"
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
  IsTunnelRequest         bool
  IsTunnelConfigRequest   bool
  IsTrafficEventReported  bool
  IsHeadersSent           bool
  IsTunnelResponseSent    bool
  GotoProtocol            string
  TunnelCount             int
  StatusCode              int
  IsH2C                   bool
  IsTLS                   bool
  ServerName              string
  TLSVersion              string
  TLSVersionNum           uint16
  TrafficDetails          []string
  LogMessages             []string
  InterceptResponseWriter interface{}
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
  knownTextMimeTypeRegexp = regexp.MustCompile(".*(text|html|json|yaml).*")
  upgradeRegexp           = regexp.MustCompile("(?i)upgrade")
)

func GetRequestStore(r *http.Request) *RequestStore {
  if val := r.Context().Value(RequestStoreKey); val != nil {
    return val.(*RequestStore)
  }
  _, rs := WithRequestStore(r)
  return rs
}

func WithRequestStore(r *http.Request) (context.Context, *RequestStore) {
  isAdminRequest := CheckAdminRequest(r)
  rs := &RequestStore{
    IsAdminRequest:        isAdminRequest,
    IsVersionRequest:      strings.HasPrefix(r.RequestURI, "/version"),
    IsLockerRequest:       strings.HasPrefix(r.RequestURI, "/registry") && strings.Contains(r.RequestURI, "/locker"),
    IsPeerEventsRequest:   strings.HasPrefix(r.RequestURI, "/registry") && strings.Contains(r.RequestURI, "/events"),
    IsMetricsRequest:      strings.HasPrefix(r.RequestURI, "/metrics") || strings.HasPrefix(r.RequestURI, "/stats"),
    IsReminderRequest:     strings.Contains(r.RequestURI, "/remember"),
    IsProbeRequest:        global.IsReadinessProbe(r) || global.IsLivenessProbe(r),
    IsHealthRequest:       !isAdminRequest && strings.HasPrefix(r.RequestURI, "/health"),
    IsStatusRequest:       !isAdminRequest && strings.HasPrefix(r.RequestURI, "/status"),
    IsDelayRequest:        !isAdminRequest && strings.Contains(r.RequestURI, "/delay"),
    IsPayloadRequest:      !isAdminRequest && (strings.Contains(r.RequestURI, "/stream") || strings.Contains(r.RequestURI, "/payload")),
    IsTunnelRequest:       strings.HasPrefix(r.RequestURI, "/tunnel=") || global.HasTunnel(r, nil),
    IsTunnelConfigRequest: strings.HasPrefix(r.RequestURI, "/tunnels"),
  }
  return context.WithValue(r.Context(), RequestStoreKey, rs), rs
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

func GetIntParam(r *http.Request, param string, defaultVal ...int) (int, bool) {
  vars := mux.Vars(r)
  switch {
  case len(vars[param]) > 0:
    s, _ := strconv.ParseInt(vars[param], 10, 32)
    return int(s), true
  case len(defaultVal) > 0:
    return defaultVal[0], false
  default:
    return 0, false
  }
}

func GetIntParamValue(r *http.Request, param string, defaultVal ...int) int {
  val, _ := GetIntParam(r, param, defaultVal...)
  return val
}

func GetStringParam(r *http.Request, param string, defaultVal ...string) (string, bool) {
  vars := mux.Vars(r)
  switch {
  case len(vars[param]) > 0:
    return vars[param], true
  case len(defaultVal) > 0:
    return defaultVal[0], false
  default:
    return "", false
  }
}

func GetStringParamValue(r *http.Request, param string, defaultVal ...string) string {
  val, _ := GetStringParam(r, param, defaultVal...)
  return val
}

func GetBoolParamValue(r *http.Request, param string, defaultVal ...bool) bool {
  val, _ := GetStringParam(r, param)
  if val != "" {
    return IsYes(val)
  }
  if len(defaultVal) > 0 {
    return defaultVal[0]
  }
  return false
}

func GetListParam(r *http.Request, param string) ([]string, bool) {
  values := []string{}
  if v, present := GetStringParam(r, param); present {
    values = strings.Split(v, ",")
  }
  return values, len(values) > 0 && len(values[0]) > 0
}

func GetStatusParam(r *http.Request) (statusCodes []int, times int, present bool) {
  vars := mux.Vars(r)
  status := vars["status"]
  if len(status) == 0 {
    return nil, 0, false
  }
  pieces := strings.Split(status, ":")
  if len(pieces[0]) > 0 {
    for _, s := range strings.Split(pieces[0], ",") {
      if sc, err := strconv.ParseInt(s, 10, 32); err == nil {
        statusCodes = append(statusCodes, int(sc))
      }
    }
    if len(pieces) > 1 {
      s, _ := strconv.ParseInt(pieces[1], 10, 32)
      times = int(s)
    }
  }
  return statusCodes, times, true
}

func ParseSize(value string) int {
  size := 0
  multiplier := 1
  if len(value) == 0 {
    return 0
  }
  for k, v := range sizes {
    if strings.Contains(value, k) {
      multiplier = int(v)
      value = strings.Split(value, k)[0]
      break
    }
  }
  if len(value) > 0 {
    s, _ := strconv.ParseInt(value, 10, 32)
    size = int(s)
  } else {
    size = 1
  }
  size = size * multiplier
  return size
}

func GetSizeParam(r *http.Request, name string) int {
  return ParseSize(mux.Vars(r)[name])
}

func ParseDuration(value string) time.Duration {
  if d, err := time.ParseDuration(value); err == nil {
    return d
  }
  return 0
}

func GetDurationParam(r *http.Request, name string) (low, high time.Duration, count int, ok bool) {
  if val := mux.Vars(r)[name]; val != "" {
    dRangeAndCount := strings.Split(val, ":")
    dRange := strings.Split(dRangeAndCount[0], "-")
    if d, err := time.ParseDuration(dRange[0]); err != nil {
      return 0, 0, 0, false
    } else {
      low = d
    }
    if len(dRange) > 1 {
      if d, err := time.ParseDuration(dRange[1]); err == nil {
        if d < low {
          high = low
          low = d
        } else {
          high = d
        }
      }
    } else {
      high = low
    }
    if len(dRangeAndCount) > 1 {
      if c, err := strconv.ParseInt(dRangeAndCount[1], 10, 32); err == nil {
        if c > 0 {
          count = int(c)
        }
      }
    }
    return low, high, count, true
  }
  return 0, 0, 0, false
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

func CopyHeaders(prefix string, r *http.Request,  w http.ResponseWriter, headers http.Header, copyHost, copyURI, copyContentType bool) {
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

func GetResponseHeadersLog(header http.Header) string {
  var s strings.Builder
  s.Grow(128)
  fmt.Fprintf(&s, "{\"ResponseHeaders\": %s}", ToJSON(header))
  return s.String()
}

func GetRequestHeadersLog(r *http.Request) string {
  return ToJSON(r.Header)
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

func WriteStringJsonPayload(w http.ResponseWriter, json string) {
  w.Header().Add(HeaderContentType, ContentTypeJSON)
  fmt.Fprintln(w, json)
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
    uri == "listeners" || uri == "label" || uri == "registry" ||uri == "client" || 
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
  isTunnelRequest := rs.IsTunnelRequest && !rs.IsTunnelConfigRequest
  if !isTunnelRequest {
    if gotoTunnel := r.Header[HeaderGotoTunnel]; len(gotoTunnel) > 0 {
      isTunnelRequest = true
      rs.IsTunnelRequest = true
    }
  }
  return isTunnelRequest
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

func IsH2Upgrade(r *http.Request) bool {
  return strings.EqualFold(r.Header.Get("Upgrade"), "h2c") || upgradeRegexp.MatchString(r.Header.Get("Connection"))
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

func ContainsAllHeaders(headers http.Header, expected map[string]*regexp.Regexp) bool {
  for h, r := range expected {
    if h != "" && (headers[h] == nil || r != nil && !StringArrayContains(headers[h], r)) {
      return false
    }
  }
  return true
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

func CreateRequest(method string, url string, headers http.Header, payload []byte, payloadReader io.ReadCloser) (*http.Request, error) {
  if payloadReader == nil {
    if payload == nil {
      payload = []byte{}
    }
    payloadReader = ioutil.NopCloser(bytes.NewReader(payload))
  }
  if req, err := http.NewRequest(method, url, payloadReader); err == nil {
    for h, values := range headers {
      if strings.EqualFold(h, "host") {
        req.Host = values[0]
      }
      req.Header[h] = values
    }
    return req, nil
  } else {
    return nil, err
  }
}

func CreateDefaultHTTPClient(label string, h2, isTLS bool, newConnNotifierChan chan string) *ClientTracker {
  return CreateHTTPClient(label, h2, true, isTLS, "", 0, 30*time.Second, 30*time.Second, 3*time.Minute, newConnNotifierChan)
}

func CreateHTTPClient(label string, h2, autoUpgrade, isTLS bool, serverName string, tlsVersion uint16,
  requestTimeout, connTimeout, connIdleTimeout time.Duration, newConnNotifierChan chan string) *ClientTracker {
  var transport http.RoundTripper
  var tracker *TransportTracker
  if !h2 {
    ht := NewHTTPTransportTracker(&http.Transport{
      MaxIdleConns:          300,
      MaxIdleConnsPerHost:   300,
      IdleConnTimeout:       connIdleTimeout,
      Proxy:                 http.ProxyFromEnvironment,
      DisableCompression:    true,
      ExpectContinueTimeout: requestTimeout,
      ResponseHeaderTimeout: requestTimeout,
      DialContext: (&net.Dialer{
        Timeout:   connTimeout,
        KeepAlive: connIdleTimeout,
      }).DialContext,
      TLSHandshakeTimeout: connTimeout,
      ForceAttemptHTTP2:   autoUpgrade,
      TLSClientConfig: &tls.Config{
        InsecureSkipVerify: true,
        ServerName:         serverName,
        MinVersion:         tlsVersion,
        MaxVersion:         tlsVersion,
      },
    }, label, newConnNotifierChan)
    tracker = &ht.TransportTracker
    transport = ht.Transport
  } else {
    tr := &http2.Transport{
      ReadIdleTimeout: connIdleTimeout,
      PingTimeout:     connTimeout,
      AllowHTTP:       true,
      TLSClientConfig: &tls.Config{
        InsecureSkipVerify: true,
        ServerName:         serverName,
        MinVersion:         tlsVersion,
        MaxVersion:         tlsVersion,
      },
    }
    tr.DialTLS = func(network, addr string, cfg *tls.Config) (net.Conn, error) {
      if isTLS {
        return tls.Dial(network, addr, cfg)
      }
      return net.Dial(network, addr)
    }
    h2t := NewHTTP2TransportTracker(tr, label, newConnNotifierChan)
    tracker = &h2t.TransportTracker
    transport = h2t.Transport
  }
  return NewHTTPClientTracker(&http.Client{Timeout: requestTimeout, Transport: transport}, nil, tracker)
}
