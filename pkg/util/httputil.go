package util

import (
  "context"
  "encoding/json"
  "fmt"
  "goto/pkg/global"
  "goto/pkg/server/intercept"
  "io"
  "io/ioutil"
  "log"
  "math/rand"
  "net"
  "net/http"
  "os"
  "reflect"
  "regexp"
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

var (
  portRouter             *mux.Router
  listenerPathSubRouters = map[string]*mux.Router{}
  logMessagesKey         = &ContextKey{"logMessagesKey"}
  currentPortKey         = &ContextKey{"currentPort"}
  trafficEventKey        = &ContextKey{"trafficEventKey"}
  fillerRegexp           = regexp.MustCompile("({.+?})")
  contentRegexp          = regexp.MustCompile("(?i)content")
  hostRegexp             = regexp.MustCompile("(?i)^host$")
)

const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*()_+-=~`{}[];:,.<>/?"

var sizes map[string]uint64 = map[string]uint64{
  "K":  1000,
  "KB": 1000,
  "M":  1000000,
  "MB": 1000000,
}

type MessageStore struct {
  messages []string
}

type TrafficEventStore struct {
  reported   bool
  statusCode int
  details    []string
}

func InitListenerRouter(root *mux.Router) {
  portRouter = root.PathPrefix("/port={port}").Subrouter()
}

func ContextMiddleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    if global.Stopping && global.IsReadinessProbe(r) {
      CopyHeaders("Stopping-Readiness-Request", w, r.Header, r.Host, r.RequestURI)
      w.WriteHeader(http.StatusNotFound)
    } else if next != nil {
      next.ServeHTTP(w, r.WithContext(WithLogMessages(WithTrafficEvent(
        ContextWithPort(r.Context(), GetListenerPortNum(r))))))
    }
  })
}

func ContextWithPort(ctx context.Context, port int) context.Context {
  return context.WithValue(ctx, currentPortKey, port)
}

func WithLogMessages(ctx context.Context) context.Context {
  return context.WithValue(ctx, logMessagesKey, &MessageStore{})
}

func WithTrafficEvent(ctx context.Context) context.Context {
  return context.WithValue(ctx, trafficEventKey, &TrafficEventStore{})
}

func LoggingMiddleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    crw := intercept.NewInterceptResponseWriter(w, false)
    if next != nil {
      next.ServeHTTP(crw, r)
    }
    AddLogMessage(GetResponseHeadersLog(w), r)
    AddLogMessage(fmt.Sprintf("Response Status Code: [%d]", crw.StatusCode), r)
    AddLogMessage(fmt.Sprintf("Response Body Length: [%d]", crw.BodyLength), r)
    PrintLogMessages(r)
  })
}

func AddLogMessage(msg string, r *http.Request) {
  m := r.Context().Value(logMessagesKey).(*MessageStore)
  m.messages = append(m.messages, msg)
}

func PrintLogMessages(r *http.Request) {
  m := r.Context().Value(logMessagesKey).(*MessageStore)
  if (!IsLockerRequest(r) || global.EnableRegistryLockerLogs) &&
    (!IsPeerEventsRequest(r) || global.EnableRegistryEventsLogs) &&
    (!IsAdminRequest(r) || global.EnableAdminLogs) &&
    (!IsReminderRequest(r) || global.EnableRegistryReminderLogs) &&
    (!IsProbeRequest(r) || global.EnableProbeLogs) &&
    (!IsHealthRequest(r) || global.EnablePeerHealthLogs) &&
    (!IsMetricsRequest(r) || global.EnableMetricsLogs) &&
    !global.IsIgnoredURI(r) && global.EnableServerLogs {
    log.Println(strings.Join(m.messages, " --> "))
    if flusher, ok := log.Writer().(http.Flusher); ok {
      flusher.Flush()
    }
  }
  m.messages = m.messages[:0]
}

func IsTrafficEventReported(r *http.Request) bool {
  te := r.Context().Value(trafficEventKey)
  return te != nil && te.(*TrafficEventStore).reported
}

func UpdateTrafficEventStatusCode(r *http.Request, statusCode int) {
  te := r.Context().Value(trafficEventKey).(*TrafficEventStore)
  if te != nil && !te.reported {
    te.statusCode = statusCode
  }
}

func UpdateTrafficEventDetails(r *http.Request, details string) {
  te := r.Context().Value(trafficEventKey).(*TrafficEventStore)
  if te != nil && !te.reported {
    te.details = append(te.details, details)
  }
}

func ReportTrafficEvent(r *http.Request) (int, []string) {
  te := r.Context().Value(trafficEventKey).(*TrafficEventStore)
  if te != nil && !te.reported {
    te.reported = true
    return te.statusCode, te.details
  }
  return 0, nil
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
  if val := ctx.Value(currentPortKey); val != nil {
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

func GetStatusParam(r *http.Request) (int, int, bool) {
  vars := mux.Vars(r)
  status := vars["status"]
  if len(status) == 0 {
    return 0, 0, false
  }
  pieces := strings.Split(status, ":")
  var statusCode, times int
  if len(pieces[0]) > 0 {
    s, _ := strconv.ParseInt(pieces[0], 10, 32)
    statusCode = int(s)
    if statusCode > 0 {
      if len(pieces) > 1 {
        s, _ := strconv.ParseInt(pieces[1], 10, 32)
        times = int(s)
      }
    }
  }
  return statusCode, times, true
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

func GetDurationParam(r *http.Request, name string) time.Duration {
  return ParseDuration(mux.Vars(r)[name])
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

func GetResponseHeadersLog(w http.ResponseWriter) string {
  var s strings.Builder
  s.Grow(128)
  fmt.Fprintf(&s, "{\"ResponseHeaders\": %s}", ToJSON(w.Header()))
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
    bytes, _ := json.Marshal(t)
    fmt.Fprintln(w, string(bytes))
  }
}

func IsAdminRequest(r *http.Request) bool {
  return strings.HasPrefix(r.RequestURI, "/port") ||
    strings.HasPrefix(r.RequestURI, "/request") || strings.HasPrefix(r.RequestURI, "/response") ||
    strings.HasPrefix(r.RequestURI, "/listeners") || strings.HasPrefix(r.RequestURI, "/label") ||
    strings.HasPrefix(r.RequestURI, "/client") || strings.HasPrefix(r.RequestURI, "/job") ||
    strings.HasPrefix(r.RequestURI, "/probes") || strings.HasPrefix(r.RequestURI, "/tcp") ||
    strings.HasPrefix(r.RequestURI, "/events") || strings.HasPrefix(r.RequestURI, "/registry")
}

func IsMetricsRequest(r *http.Request) bool {
  return strings.Contains(r.RequestURI, "/metrics")
}

func IsReminderRequest(r *http.Request) bool {
  return strings.Contains(r.RequestURI, "/remember")
}

func IsLockerRequest(r *http.Request) bool {
  return strings.HasPrefix(r.RequestURI, "/registry") && strings.Contains(r.RequestURI, "/locker")
}

func IsPeerEventsRequest(r *http.Request) bool {
  return strings.HasPrefix(r.RequestURI, "/registry") && strings.Contains(r.RequestURI, "/events")
}

func IsStatusRequest(r *http.Request) bool {
  return !IsAdminRequest(r) && strings.Contains(r.RequestURI, "/status")
}

func IsDelayRequest(r *http.Request) bool {
  return !IsAdminRequest(r) && strings.Contains(r.RequestURI, "/delay")
}

func IsPayloadRequest(r *http.Request) bool {
  return !IsAdminRequest(r) && (strings.Contains(r.RequestURI, "/stream") || strings.Contains(r.RequestURI, "/payload"))
}

func IsProbeRequest(r *http.Request) bool {
  return global.IsReadinessProbe(r) || global.IsLivenessProbe(r)
}

func IsHealthRequest(r *http.Request) bool {
  return !IsAdminRequest(r) && strings.Contains(r.RequestURI, "/health")
}

func IsKnownRequest(r *http.Request) bool {
  return IsProbeRequest(r) || IsReminderRequest(r) || IsHealthRequest(r) || IsMetricsRequest(r) ||
    IsLockerRequest(r) || IsAdminRequest(r) || IsStatusRequest(r) || IsDelayRequest(r) || IsPayloadRequest(r)
}

func PathRouter(r *mux.Router, path string) *mux.Router {
  if lpath, err := r.NewRoute().BuildOnly().GetPathTemplate(); err == nil {
    lpath = lpath + path
    listenerPathSubRouters[lpath] = portRouter.PathPrefix(lpath).Subrouter()
  } else {
    listenerPathSubRouters[path] = portRouter.PathPrefix(path).Subrouter()
  }
  return r.PathPrefix(path).Subrouter()
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
  if lpath, err := r.NewRoute().BuildOnly().GetPathTemplate(); err == nil && listenerPathSubRouters[lpath] != nil {
    AddRoute(listenerPathSubRouters[lpath], route, f, methods...)
  }
}

func AddRouteQ(r *mux.Router, route string, f func(http.ResponseWriter, *http.Request), queryParamName string, queryKey string, methods ...string) {
  r.HandleFunc(route, f).Queries(queryParamName, queryKey).Methods(methods...)
  r.HandleFunc(route+"/", f).Queries(queryParamName, queryKey).Methods(methods...)
}

func AddRouteQWithPort(r *mux.Router, route string, f func(http.ResponseWriter, *http.Request), queryParamName string, queryKey string, methods ...string) {
  AddRouteQ(r, route, f, queryParamName, queryKey, methods...)
  if lpath, err := r.NewRoute().BuildOnly().GetPathTemplate(); err == nil && listenerPathSubRouters[lpath] != nil {
    AddRouteQ(listenerPathSubRouters[lpath], route, f, queryParamName, queryKey, methods...)
  }
}

func AddRouteMultiQ(r *mux.Router, route string, f func(http.ResponseWriter, *http.Request), method string, queryParams ...string) {
  r.HandleFunc(route, f).Queries(queryParams...).Methods(method)
  r.HandleFunc(route+"/", f).Queries(queryParams...).Methods(method)
}

func AddRouteMultiQWithPort(r *mux.Router, route string, f func(http.ResponseWriter, *http.Request), method string, queryParams ...string) {
  AddRouteMultiQ(r, route, f, method, queryParams...)
  if lpath, err := r.NewRoute().BuildOnly().GetPathTemplate(); err == nil && listenerPathSubRouters[lpath] != nil {
    AddRouteMultiQ(listenerPathSubRouters[lpath], route, f, method, queryParams...)
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

func Read(r io.Reader) string {
  if body, err := ioutil.ReadAll(r); err == nil {
    return string(body)
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

func CloseResponse(r *http.Response) {
  defer r.Body.Close()
  io.Copy(ioutil.Discard, r.Body)
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

func IsYes(flag string) bool {
  return strings.EqualFold(flag, "y") || strings.EqualFold(flag, "yes") ||
    strings.EqualFold(flag, "true") || strings.EqualFold(flag, "1")
}

func GetFillerMarked(key string) string {
  return "{" + key + "}"
}

func GetFillers(text string) []string {
  return fillerRegexp.FindAllString(text, -1)
}

func GetFillersUnmarked(text string) []string {
  matches := GetFillers(text)
  for i, m := range matches {
    m = strings.TrimLeft(m, "{")
    matches[i] = strings.TrimRight(m, "}")
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

func RegisterURIRouteAndGetRegex(uri string, router *mux.Router, handler func(http.ResponseWriter, *http.Request)) (*mux.Router, *regexp.Regexp, error) {
  if uri != "" {
    vars := fillerRegexp.FindAllString(uri, -1)
    for _, v := range vars {
      v2, _ := GetFillerUnmarked(v)
      v2 = GetFillerMarked(v2 + ":.*")
      uri = strings.ReplaceAll(uri, v, v2)
    }
    subRouter := router.NewRoute().Subrouter()
    route := subRouter.PathPrefix(uri).HandlerFunc(handler)
    if re, err := route.GetPathRegexp(); err == nil {
      re = strings.ReplaceAll(re, "$", "(/.*)?$")
      return subRouter, regexp.MustCompile(re), nil
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

func BuildCrossHeadersMap(crossTrackingHeaders map[string][]string) map[string]string {
  crossHeadersMap := map[string]string{}
  for header, subheaders := range crossTrackingHeaders {
    for _, subheader := range subheaders {
      crossHeadersMap[subheader] = header
    }
  }
  return crossHeadersMap
}

func GenerateRandomString(size int) string {
  r := rand.New(rand.NewSource(time.Now().UnixNano()))
  b := make([]byte, size)
  for i := range b {
    b[i] = charset[r.Intn(len(charset))]
  }
  return string(b)
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
  }
  return &http.Client{Transport: tr}
}
