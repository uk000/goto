package util

import (
  "bytes"
  "context"
  "crypto/tls"
  "encoding/json"
  "fmt"
  "goto/pkg/global"
  "io"
  "io/ioutil"
  "log"
  "math/rand"
  "net"
  "net/http"
  "os"
  "reflect"
  "regexp"
  "runtime"
  "strconv"
  "strings"
  "time"

  "github.com/gorilla/mux"
  "google.golang.org/grpc/metadata"
)

type ServerHandler struct {
  Name       string
  SetRoutes  func(r *mux.Router, parent *mux.Router, root *mux.Router)
  Middleware mux.MiddlewareFunc
}

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
  IsTrafficEventReported  bool
  IsHeadersSent           bool
  IsH2C                   bool
  StatusCode              int
  TrafficDetails          []string
  LogMessages             []string
  InterceptResponseWriter interface{}
}

const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*()_+-=~`{}[];:,.<>/?"

var sizes map[string]uint64 = map[string]uint64{
  "K":  1000,
  "KB": 1000,
  "M":  1000000,
  "MB": 1000000,
}

var (
  DefaultHttpClient = CreateHttpClient()
  RequestStoreKey   = &ContextKey{"requestStore"}
  CurrentPortKey    = &ContextKey{"currentPort"}
  IgnoredRequestKey = &ContextKey{"ignoredRequest"}
  ConnectionKey     = &ContextKey{"connection"}

  portRouter          *mux.Router
  portTunnelRouters   = map[string]*mux.Router{}
  fillerRegexp        = regexp.MustCompile("{({[^{}]+?})}|{([^{}]+?)}")
  contentRegexp       = regexp.MustCompile("(?i)content")
  hostRegexp          = regexp.MustCompile("(?i)^host$")
  utf8Regexp          = regexp.MustCompile("(?i)utf-8")
  knownTextTypeRegexp = regexp.MustCompile(".*(text|html|json|yaml).*")
  upgradeRegexp       = regexp.MustCompile("(?i)upgrade")
  randomCharsetLength = len(charset)
  random              = rand.New(rand.NewSource(time.Now().UnixNano()))
)

func IsH2(r *http.Request) bool {
  return r.ProtoMajor == 2
}

func IsH2C(r *http.Request) bool {
  return GetRequestStore(r).IsH2C
}

func InitListenerRouter(root *mux.Router) {
  portRouter = root.PathPrefix("/port={port}").Subrouter()
  root.PathPrefix("/tunnel={address}").Subrouter().MatcherFunc(func(*http.Request, *mux.RouteMatch) bool { return true }).HandlerFunc(Tunnel)
}

func AddLogMessage(msg string, r *http.Request) {
  rs := r.Context().Value(RequestStoreKey).(*RequestStore)
  rs.LogMessages = append(rs.LogMessages, msg)
}

func GetRequestStore(r *http.Request) *RequestStore {
  return r.Context().Value(RequestStoreKey).(*RequestStore)
}

func GetInterceptResponseWriter(r *http.Request) interface{} {
  return r.Context().Value(RequestStoreKey).(*RequestStore).InterceptResponseWriter
}

func IsHeadersSent(r *http.Request) bool {
  rs := r.Context().Value(RequestStoreKey)
  return rs != nil && rs.(*RequestStore).IsHeadersSent
}

func SetHeadersSent(r *http.Request, sent bool) {
  rs := r.Context().Value(RequestStoreKey)
  if rs != nil {
    rs.(*RequestStore).IsHeadersSent = sent
  }
}

func IsTrafficEventReported(r *http.Request) bool {
  rs := r.Context().Value(RequestStoreKey)
  return rs != nil && rs.(*RequestStore).IsTrafficEventReported
}

func UpdateTrafficEventStatusCode(r *http.Request, statusCode int) {
  rs := r.Context().Value(RequestStoreKey).(*RequestStore)
  if rs != nil && !rs.IsTrafficEventReported {
    rs.StatusCode = statusCode
  }
}

func UpdateTrafficEventDetails(r *http.Request, details string) {
  rs := r.Context().Value(RequestStoreKey).(*RequestStore)
  if rs != nil && !rs.IsTrafficEventReported {
    rs.TrafficDetails = append(rs.TrafficDetails, details)
  }
}

func ReportTrafficEvent(r *http.Request) (int, []string) {
  rs := r.Context().Value(RequestStoreKey).(*RequestStore)
  if rs != nil && !rs.IsTrafficEventReported {
    rs.IsTrafficEventReported = true
    return rs.StatusCode, rs.TrafficDetails
  }
  return 0, nil
}

func SetFiltreredRequest(r *http.Request) {
  rs := r.Context().Value(RequestStoreKey).(*RequestStore)
  if rs != nil {
    rs.IsFilteredRequest = true
  }
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

func GetContextPort(ctx context.Context) int {
  if val := ctx.Value(CurrentPortKey); val != nil {
    return val.(int)
  }
  return GetPortNumFromGRPCAuthority(ctx)
}

func GetCurrentPort(r *http.Request) int {
  return GetContextPort(r.Context())
}

func GetCurrentListenerLabel(r *http.Request) string {
  return global.GetListenerLabelForPort(GetCurrentPort(r))
}

func GetPodName() string {
  if global.PodName == "" {
    pod, present := os.LookupEnv("POD_NAME")
    if !present {
      pod, _ = os.Hostname()
    }
    global.PodName = pod
  }
  return global.PodName
}

func GetNodeName() string {
  if global.NodeName == "" {
    global.NodeName, _ = os.LookupEnv("NODE_NAME")
  }
  return global.NodeName
}

func GetCluster() string {
  if global.Cluster == "" {
    global.Cluster, _ = os.LookupEnv("CLUSTER")
  }
  return global.Cluster
}

func GetNamespace() string {
  if global.Namespace == "" {
    ns, present := os.LookupEnv("NAMESPACE")
    if !present {
      if data, err := ioutil.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
        ns = string(data)
        present = true
      }
    }
    if !present {
      ns = "local"
    }
    global.Namespace = ns
  }
  return global.Namespace
}

func GetHostIP() string {
  if global.HostIP == "" {
    if ip, present := os.LookupEnv("POD_IP"); present {
      global.HostIP = ip
    } else {
      conn, err := net.Dial("udp", "8.8.8.8:80")
      if err == nil {
        defer conn.Close()
        global.HostIP = conn.LocalAddr().(*net.UDPAddr).IP.String()
      } else {
        global.HostIP = "localhost"
      }
    }
  }
  return global.HostIP
}

func BuildHostLabel(port int) string {
  hostLabel := ""
  node := GetNodeName()
  cluster := GetCluster()
  if node != "" || cluster != "" {
    hostLabel = fmt.Sprintf("%s.%s@%s:%d(%s@%s)", GetPodName(), GetNamespace(), GetHostIP(), port, node, cluster)
  } else {
    hostLabel = fmt.Sprintf("%s.%s@%s:%d", GetPodName(), GetNamespace(), GetHostIP(), port)
  }
  return hostLabel
}

func BuildListenerLabel(port int) string {
  return fmt.Sprintf("Goto-%s:%d", GetHostIP(), port)
}

func GetHostLabel() string {
  if global.HostLabel == "" {
    global.HostLabel = BuildHostLabel(global.ServerPort)
  }
  return global.HostLabel
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

func CopyHeaders(prefix string, w http.ResponseWriter, headers http.Header, host, uri string) {
  hostCopied := false
  responseHeaders := w.Header()
  for h, values := range headers {
    if !contentRegexp.MatchString(h) {
      for _, v := range values {
        AddHeaderWithPrefix(prefix, h, v, responseHeaders)
      }
    }
    if hostRegexp.MatchString(h) {
      hostCopied = true
    }
  }
  if !hostCopied && host != "" {
    AddHeaderWithPrefix(prefix, "Host", host, responseHeaders)
  }
  if uri != "" {
    AddHeaderWithPrefix(prefix, "URI", uri, responseHeaders)
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
  r.Header["Host"] = []string{r.Host}
  r.Header["Protocol"] = []string{r.Proto}
  return ToJSON(r.Header)
}

func ReadJson(s string, t interface{}) error {
  return json.Unmarshal([]byte(s), t)
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

func WriteJsonPayload(w http.ResponseWriter, t interface{}) {
  w.Header().Add("Content-Type", "application/json")
  if reflect.ValueOf(t).IsNil() {
    fmt.Fprintln(w, "")
  } else {
    if bytes, err := json.Marshal(t); err == nil {
      fmt.Fprintln(w, string(bytes))
    } else {
      fmt.Printf("Failed to write json payload: %s\n", err.Error())
    }
  }
}

func WriteStringJsonPayload(w http.ResponseWriter, json string) {
  w.Header().Add("Content-Type", "application/json")
  fmt.Fprintln(w, json)
}

func WriteErrorJson(w http.ResponseWriter, error string) {
  fmt.Fprintf(w, "{\"error\":\"%s\"}", error)
}

func IsAdminRequest(r *http.Request) bool {
  rs := r.Context().Value(RequestStoreKey).(*RequestStore)
  return rs != nil && rs.IsAdminRequest
}

func CheckAdminRequest(r *http.Request) bool {
  return strings.HasPrefix(r.RequestURI, "/metrics") || strings.HasPrefix(r.RequestURI, "/registry") ||
    strings.HasPrefix(r.RequestURI, "/server") || strings.HasPrefix(r.RequestURI, "/client") ||
    strings.HasPrefix(r.RequestURI, "/port") || strings.HasPrefix(r.RequestURI, "/job") ||
    strings.HasPrefix(r.RequestURI, "/probes") || strings.HasPrefix(r.RequestURI, "/events")
}

func IsMetricsRequest(r *http.Request) bool {
  rs := r.Context().Value(RequestStoreKey).(*RequestStore)
  return rs != nil && rs.IsMetricsRequest
}

func IsReminderRequest(r *http.Request) bool {
  rs := r.Context().Value(RequestStoreKey).(*RequestStore)
  return rs != nil && rs.IsReminderRequest
}

func IsLockerRequest(r *http.Request) bool {
  rs := r.Context().Value(RequestStoreKey).(*RequestStore)
  return rs != nil && rs.IsLockerRequest
}

func IsPeerEventsRequest(r *http.Request) bool {
  rs := r.Context().Value(RequestStoreKey).(*RequestStore)
  return rs != nil && rs.IsPeerEventsRequest
}

func IsStatusRequest(r *http.Request) bool {
  rs := r.Context().Value(RequestStoreKey).(*RequestStore)
  return rs != nil && rs.IsStatusRequest
}

func IsDelayRequest(r *http.Request) bool {
  rs := r.Context().Value(RequestStoreKey).(*RequestStore)
  return rs != nil && rs.IsDelayRequest
}

func IsPayloadRequest(r *http.Request) bool {
  rs := r.Context().Value(RequestStoreKey).(*RequestStore)
  return rs != nil && rs.IsPayloadRequest
}

func IsProbeRequest(r *http.Request) bool {
  rs := r.Context().Value(RequestStoreKey).(*RequestStore)
  return rs != nil && rs.IsProbeRequest
}

func IsHealthRequest(r *http.Request) bool {
  rs := r.Context().Value(RequestStoreKey).(*RequestStore)
  return rs != nil && rs.IsProbeRequest
}

func IsVersionRequest(r *http.Request) bool {
  rs := r.Context().Value(RequestStoreKey).(*RequestStore)
  return rs != nil && rs.IsVersionRequest
}

func IsFilteredRequest(r *http.Request) bool {
  rs := r.Context().Value(RequestStoreKey).(*RequestStore)
  return rs != nil && rs.IsFilteredRequest
}

func IsTunnelRequest(r *http.Request) bool {
  rs := r.Context().Value(RequestStoreKey).(*RequestStore)
  return rs != nil && rs.IsTunnelRequest
}

func IsKnownRequest(r *http.Request) bool {
  rs := r.Context().Value(RequestStoreKey).(*RequestStore)
  return rs != nil && (rs.IsProbeRequest || rs.IsReminderRequest || rs.IsHealthRequest ||
    rs.IsMetricsRequest || rs.IsVersionRequest || rs.IsLockerRequest ||
    rs.IsAdminRequest || rs.IsStatusRequest || rs.IsDelayRequest ||
    rs.IsPayloadRequest || rs.IsTunnelRequest)
}

func IsKnownNonTraffic(r *http.Request) bool {
  rs := r.Context().Value(RequestStoreKey).(*RequestStore)
  return rs != nil && (rs.IsProbeRequest || rs.IsReminderRequest || rs.IsHealthRequest ||
    rs.IsMetricsRequest || rs.IsVersionRequest || rs.IsLockerRequest ||
    rs.IsAdminRequest || rs.IsTunnelRequest)
}

func IsKnownTraffic(r *http.Request) bool {
  rs := r.Context().Value(RequestStoreKey).(*RequestStore)
  return rs != nil && (rs.IsStatusRequest || rs.IsDelayRequest ||
    rs.IsPayloadRequest || rs.IsTunnelRequest)
}

func PathRouter(r *mux.Router, path string) *mux.Router {
  routerPath := path
  if lpath, err := r.NewRoute().BuildOnly().GetPathTemplate(); err == nil {
    routerPath = lpath + path
  }
  portTunnelRouters[routerPath] = portRouter.PathPrefix(routerPath).Subrouter()
  return r.PathPrefix(path).Subrouter()
}

func Tunnel(w http.ResponseWriter, r *http.Request) {
  address := GetStringParamValue(r, "address")
  uri := "/"
  if pieces := strings.Split(r.RequestURI, address); len(pieces) > 1 {
    uri = pieces[1]
  }
  proto := ""
  if strings.HasPrefix(address, "http") {
    pieces := strings.Split(address, ":")
    proto = pieces[0] + "://"
    if len(pieces) > 1 {
      pieces = pieces[1:]
      address = strings.Join(pieces, ":")
    }
  } else {
    if r.TLS != nil {
      proto = "https://"
    } else {
      proto = "http://"
    }
  }
  url := proto + address + uri
  AddLogMessage(fmt.Sprintf("Tunneling to [%s]", url), r)
  if req, err := CreateRequest(r.Method, url, r.Header, ReadBytes(r.Body)); err == nil {
    if resp, err := DefaultHttpClient.Do(req); err == nil {
      defer resp.Body.Close()
      CopyHeaders("", w, resp.Header, "", "")
      w.WriteHeader(resp.StatusCode)
      io.Copy(w, resp.Body)
    } else {
      msg := fmt.Sprintf("Error invoking tunnel to [%s]: %s", url, err.Error())
      fmt.Fprintln(w, msg)
      AddLogMessage(msg, r)
    }
  } else {
    msg := fmt.Sprintf("Error creating tunnel request for [%s]: %s", url, err.Error())
    fmt.Fprintln(w, msg)
    AddLogMessage(msg, r)
  }
}

func AddRoute(r *mux.Router, route string, f func(http.ResponseWriter, *http.Request), methods ...string) {
  if len(methods) > 0 {
    r.HandleFunc(route, f).Methods(methods...)
    r.HandleFunc(route+"/", f).Methods(methods...)
  } else {
    r.HandleFunc(route, f)
    r.HandleFunc(route+"/", f)
  }
}

func AddRouteWithPort(r *mux.Router, route string, f func(http.ResponseWriter, *http.Request), methods ...string) {
  AddRoute(r, route, f, methods...)
  if lpath, err := r.NewRoute().BuildOnly().GetPathTemplate(); err == nil && portTunnelRouters[lpath] != nil {
    AddRoute(portTunnelRouters[lpath], route, f, methods...)
  }
}

func AddRouteQ(r *mux.Router, route string, f func(http.ResponseWriter, *http.Request), queryParamName string, queryKey string, methods ...string) {
  r.HandleFunc(route, f).Queries(queryParamName, queryKey).Methods(methods...)
  r.HandleFunc(route+"/", f).Queries(queryParamName, queryKey).Methods(methods...)
}

func AddRouteQWithPort(r *mux.Router, route string, f func(http.ResponseWriter, *http.Request), queryParamName string, queryKey string, methods ...string) {
  AddRouteQ(r, route, f, queryParamName, queryKey, methods...)
  if lpath, err := r.NewRoute().BuildOnly().GetPathTemplate(); err == nil && portTunnelRouters[lpath] != nil {
    AddRouteQ(portTunnelRouters[lpath], route, f, queryParamName, queryKey, methods...)
  }
}

func AddRouteMultiQ(r *mux.Router, route string, f func(http.ResponseWriter, *http.Request), method string, queryParams ...string) {
  r.HandleFunc(route, f).Queries(queryParams...).Methods(method)
  r.HandleFunc(route+"/", f).Queries(queryParams...).Methods(method)
  for i := 0; i < len(queryParams); i += 2 {
    for j := i + 2; j < len(queryParams); j += 2 {
      r.HandleFunc(route, f).Queries(queryParams[i], queryParams[i+1], queryParams[j], queryParams[j+1]).Methods(method)
      r.HandleFunc(route+"/", f).Queries(queryParams[i], queryParams[i+1], queryParams[j], queryParams[j+1]).Methods(method)
    }
  }
  for i := 0; i < len(queryParams); i += 2 {
    r.HandleFunc(route, f).Queries(queryParams[i], queryParams[i+1]).Methods(method)
    r.HandleFunc(route+"/", f).Queries(queryParams[i], queryParams[i+1]).Methods(method)
  }
  r.HandleFunc(route, f).Methods(method)
  r.HandleFunc(route+"/", f).Methods(method)
}

func AddRouteMultiQWithPort(r *mux.Router, route string, f func(http.ResponseWriter, *http.Request), method string, queryParams ...string) {
  AddRouteMultiQ(r, route, f, method, queryParams...)
  if lpath, err := r.NewRoute().BuildOnly().GetPathTemplate(); err == nil && portTunnelRouters[lpath] != nil {
    AddRouteMultiQ(portTunnelRouters[lpath], route, f, method, queryParams...)
  }
}

func AddRoutes(r *mux.Router, parent *mux.Router, root *mux.Router, handlers ...ServerHandler) {
  for _, h := range handlers {
    if h.SetRoutes != nil {
      h.SetRoutes(r, parent, root)
    }
  }
}

func AddMiddlewares(next http.Handler, handlers ...ServerHandler) http.Handler {
  handler := next
  for i := len(handlers) - 1; i >= 0; i-- {
    if handlers[i].Middleware != nil {
      handler = handlers[i].Middleware(handler)
    }
  }
  return handler
}

func GetListenerPort(r *http.Request) string {
  ctx := r.Context()
  srvAddr := ctx.Value(http.LocalAddrContextKey).(net.Addr)
  pieces := strings.Split(srvAddr.String(), ":")
  return pieces[len(pieces)-1]
}

func GetListenerPortNum(r *http.Request) int {
  if port, err := strconv.Atoi(GetListenerPort(r)); err == nil {
    return port
  }
  return 0
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

func ToJSON(o interface{}) string {
  if output, err := json.Marshal(o); err == nil {
    return string(output)
  }
  return fmt.Sprintf("%+v", o)
}

func IsJSONContentType(h http.Header) bool {
  if contentType := h.Get("Content-Type"); contentType != "" {
    return strings.EqualFold(contentType, "application/json")
  }
  return false
}

func IsYAMLContentType(h http.Header) bool {
  if contentType := h.Get("Content-Type"); contentType != "" {
    return strings.EqualFold(contentType, "application/yaml")
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
  return !knownTextTypeRegexp.MatchString(contentType)
}

func BuildFilePath(filePath, fileName string) string {
  if filePath != "" && !strings.HasSuffix(filePath, "/") {
    filePath += "/"
  }
  filePath += fileName
  return filePath
}

func Read(r io.Reader) string {
  if body, err := ioutil.ReadAll(r); err == nil {
    return strings.Trim(string(body), " ")
  } else {
    log.Println(err.Error())
  }
  return ""
}

func ReadBytes(r io.Reader) []byte {
  if body, err := ioutil.ReadAll(r); err == nil {
    return body
  } else {
    log.Println(err.Error())
  }
  return nil
}

func DiscardRequestBody(r *http.Request) {
  defer r.Body.Close()
  io.Copy(ioutil.Discard, r.Body)
}

func ReadAndTrack(r io.Reader, collect bool) ([]byte, int, time.Time, time.Time, string) {
  buf := make([]byte, 1000)
  var result []byte
  var readSize int
  var first, last time.Time
  for {
    size, err := r.Read(buf)
    now := time.Now()
    if first.IsZero() {
      first = now
    }
    last = now
    readSize += size
    if collect {
      result = append(result, buf[0:size]...)
    }
    if err == io.EOF {
      return result, readSize, first, last, ""
    } else if err != nil {
      return result, readSize, first, last, err.Error()
    }
  }
}

func WriteAndTrack(w io.WriteCloser, data [][]byte, delay time.Duration) (int, time.Time, time.Time, string) {
  defer w.Close()
  count := len(data)
  var writeSize int
  var first, last time.Time
  for i := 0; i < count; i++ {
    d := data[i]
    size := len(d)
    for {
      n, err := w.Write(d)
      now := time.Now()
      if first.IsZero() {
        first = now
      }
      last = now
      writeSize += n
      if err != nil {
        return writeSize, first, last, err.Error()
      }
      if n >= size {
        break
      }
    }
    if i < count-1 && delay > 0 {
      time.Sleep(delay)
    }
  }
  return writeSize, first, last, ""
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

func StringArrayContains(list []string, r *regexp.Regexp) bool {
  for _, v := range list {
    if r.MatchString(v) {
      return true
    }
  }
  return false
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

func IsFiller(key string) bool {
  return fillerRegexp.MatchString(key)
}

func GetFillerMarked(key string) string {
  return "{" + key + "}"
}

func GetFillers(text string) []string {
  return fillerRegexp.FindAllString(text, -1)
}

func GetFiller(text string) (string, bool) {
  fillers := GetFillers(text)
  if len(fillers) > 0 {
    return fillers[0], true
  }
  return "", false
}

func GetFillersUnmarked(text string) []string {
  matches := GetFillers(text)
  for i, m := range matches {
    m = strings.TrimPrefix(m, "{")
    matches[i] = strings.TrimSuffix(m, "}")
  }
  return matches
}

func GetFillerUnmarked(text string) (string, bool) {
  fillers := GetFillersUnmarked(text)
  if len(fillers) > 0 {
    return fillers[0], true
  }
  return "", false
}

func RegisterURIRouteAndGetRegex(uri string, glob bool, router *mux.Router, handler func(http.ResponseWriter, *http.Request)) (*mux.Router, *regexp.Regexp, error) {
  if uri != "" {
    vars := fillerRegexp.FindAllString(uri, -1)
    for _, v := range vars {
      v2, _ := GetFillerUnmarked(v)
      v2 = GetFillerMarked(v2 + ":[^/&\\?]*")
      uri = strings.ReplaceAll(uri, v, v2)
    }
    subRouter := router.NewRoute().Subrouter()
    route := subRouter.PathPrefix(uri)
    if path, err := route.GetPathRegexp(); err == nil {
      //path = strings.ReplaceAll(path, "$", "(/.*)?$")
      pattern := path
      if glob {
        pattern += "(.*)?"
      }
      pattern += "(\\?.*)?$"
      re := regexp.MustCompile(pattern)
      route = route.MatcherFunc(func(r *http.Request, rm *mux.RouteMatch) bool {
        return re.MatchString(r.URL.Path)
      }).HandlerFunc(handler)
      return subRouter, re, nil
    } else {
      return nil, nil, err
    }
  }
  return nil, nil, fmt.Errorf("Empty URI")
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

func Random(max int) int {
  return random.Intn(max)
}

func Random64(max int64) int64 {
  return random.Int63n(max)
}

func RandomDuration(min, max time.Duration, fallback ...time.Duration) time.Duration {
  if min == 0 && max == 0 {
    if len(fallback) > 0 {
      return fallback[0]
    } else {
      return 0
    }
  }
  d := min
  if max > min {
    addOn := max - d
    d = d + time.Millisecond*time.Duration(Random64(addOn.Milliseconds()))
  }
  return d
}

func RandomFrom(vals []int) int {
  return vals[random.Intn(len(vals))]
}

func GenerateRandomPayload(size int) []byte {
  b := make([]byte, size)
  for i := range b {
    b[i] = charset[Random(randomCharsetLength)]
  }
  return b
}

func GenerateRandomString(size int) string {
  return string(GenerateRandomPayload(size))
}

func CreateRequest(method string, url string, headers http.Header, payload []byte) (*http.Request, error) {
  var payloadReader *bytes.Reader
  if len(payload) > 0 {
    payloadReader = bytes.NewReader(payload)
  } else {
    payloadReader = bytes.NewReader([]byte{})
  }
  if req, err := http.NewRequest(method, url, payloadReader); err == nil {
    for h, values := range headers {
      if strings.EqualFold(h, "host") {
        req.Host = values[0]
      } else {
        req.Header.Add(h, values[0])
      }
    }
    return req, nil
  } else {
    return nil, err
  }
}

func CreateHttpClient() *http.Client {
  tr := &http.Transport{
    MaxIdleConns: 10,
    Proxy:        http.ProxyFromEnvironment,
    DialContext: (&net.Dialer{
      Timeout:   15 * time.Second,
      KeepAlive: 3 * time.Minute,
    }).DialContext,
    TLSHandshakeTimeout: 10 * time.Second,
    TLSClientConfig: &tls.Config{
      InsecureSkipVerify: true,
    },
  }
  return &http.Client{Transport: tr}
}

func PrintCallers(level int, callee string) {
  pc := make([]uintptr, 16)
  n := runtime.Callers(1, pc)
  frames := runtime.CallersFrames(pc[:n])
  var callers []string
  i := 0
  for {
    frame, more := frames.Next()
    if !strings.Contains(frame.Function, "util") &&
      strings.Contains(frame.Function, "goto") {
      callers = append(callers, frame.Function)
      i++
    }
    if !more || i >= level {
      break
    }
  }
  fmt.Println("-----------------------------------------------")
  fmt.Printf("Callers of [%s]: %+v\n", callee, callers)
  fmt.Println("-----------------------------------------------")
}
