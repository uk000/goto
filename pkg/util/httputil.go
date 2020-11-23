package util

import (
	"context"
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
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

type ServerHandler struct {
  Name       string
  SetRoutes  func(r *mux.Router, parent *mux.Router, root *mux.Router)
  Middleware mux.MiddlewareFunc
}

type ContextKey struct{ Key string }

var (
  logmessagesKey *ContextKey    = &ContextKey{"logmessages"}
  fillerRegExp   *regexp.Regexp = regexp.MustCompile("({.+?})")
)

const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*()_+-=~`{}[];:,.<>/?"

var sizes map[string]uint64 = map[string]uint64{
  "K":  1000,
  "KB": 1000,
  "M":  1000000,
  "MB": 1000000,
}

type messagestore struct {
  messages []string
}

func ContextMiddleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    if global.Stopping && IsReadinessProbe(r) {
      CopyHeaders("Stopping-Readiness-Request", w, r.Header, r.Host)
      w.WriteHeader(http.StatusNotFound)
    } else {
      next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), logmessagesKey, &messagestore{})))
    }
  })
}

func LoggingMiddleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    next.ServeHTTP(w, r)
    PrintLogMessages(r)
  })
}

func AddLogMessage(msg string, r *http.Request) {
  m := r.Context().Value(logmessagesKey).(*messagestore)
  m.messages = append(m.messages, msg)
}

func PrintLogMessages(r *http.Request) {
  m := r.Context().Value(logmessagesKey).(*messagestore)
  if (!IsLockerRequest(r) || global.EnableRegistryLockerLogs) &&
    (!IsAdminRequest(r) || global.EnableAdminLogs) &&
    (!IsReminderRequest(r) || global.EnableRegistryReminderLogs) &&
    (!IsProbeRequest(r) || global.EnableProbeLogs) &&
    global.EnableTrackingLogs {
    log.Println(strings.Join(m.messages, " --> "))
    if flusher, ok := log.Writer().(http.Flusher); ok {
      flusher.Flush()
    }
  }
  m.messages = m.messages[:0]
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
  if ip, present := os.LookupEnv("POD_IP"); present {
    return ip
  }
  conn, err := net.Dial("udp", "8.8.8.8:80")
  if err == nil {
    defer conn.Close()
    return conn.LocalAddr().(*net.UDPAddr).IP.String()
  }
  return "localhost"
}

func GetHostLabel() string {
  if global.HostLabel == "" {
    node := GetNodeName()
    cluster := GetCluster()
    if node != "" || cluster != "" {
      global.HostLabel = fmt.Sprintf("%s.%s@%s(%s@%s)", GetPodName(), GetNamespace(), global.PeerAddress, node, cluster)
    } else {
      global.HostLabel = fmt.Sprintf("%s.%s@%s", GetPodName(), GetNamespace(), global.PeerAddress)
    }
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
  vars := mux.Vars(r)
  return ParseSize(vars[name])
}

func GetDurationParam(r *http.Request, name string) time.Duration {
  vars := mux.Vars(r)
  param := vars[name]
  if d, err := time.ParseDuration(param); err == nil {
    return d
  }
  return 0
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

func CopyHeaders(prefix string, w http.ResponseWriter, headers http.Header, host string) {
  hostCopied := false
  for h, values := range headers {
    if !strings.Contains(h, "content") && !strings.Contains(h, "Content") {
      h2 := h
      if prefix != "" {
        h2 = prefix + "-" + h
      }
      for _, v := range values {
        w.Header().Add(h2, v)
      }
    }
    if strings.EqualFold(h, "host") {
      hostCopied = true
    }
  }
  if !hostCopied && host != "" {
    hostHeader := "Host"
    if prefix != "" {
      hostHeader = prefix + "-" + hostHeader
    }
    w.Header().Add(hostHeader, host)
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
  return strings.HasPrefix(r.RequestURI, "/request") || strings.HasPrefix(r.RequestURI, "/response") ||
    strings.HasPrefix(r.RequestURI, "/listeners") || strings.HasPrefix(r.RequestURI, "/client") ||
    strings.HasPrefix(r.RequestURI, "/label") || strings.HasPrefix(r.RequestURI, "/job") ||
    strings.HasPrefix(r.RequestURI, "/registry")
}

func IsReminderRequest(r *http.Request) bool {
  return strings.Contains(r.RequestURI, "/remember")
}

func IsLockerRequest(r *http.Request) bool {
  return strings.HasPrefix(r.RequestURI, "/registry") && strings.Contains(r.RequestURI, "/locker")
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

func AddRoute(r *mux.Router, route string, f func(http.ResponseWriter, *http.Request), methods ...string) {
  if len(methods) > 0 {
    r.HandleFunc(route, f).Methods(methods...)
    r.HandleFunc(route+"/", f).Methods(methods...)
  } else {
    r.HandleFunc(route, f)
    r.HandleFunc(route+"/", f)
  }
}

func AddRouteQ(r *mux.Router, route string, f func(http.ResponseWriter, *http.Request), queryParamName string, queryKey string, methods ...string) {
  r.HandleFunc(route, f).Queries(queryParamName, queryKey).Methods(methods...)
  r.HandleFunc(route+"/", f).Queries(queryParamName, queryKey).Methods(methods...)
}

func AddRouteMultiQ(r *mux.Router, route string, f func(http.ResponseWriter, *http.Request), method string, queryParams ...string) {
  r.HandleFunc(route, f).Queries(queryParams...).Methods(method)
  r.HandleFunc(route+"/", f).Queries(queryParams...).Methods(method)
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
  // for _, h := range handlers {
  // 	if h.Middleware != nil {
  // 		handler = h.Middleware(handler)
  // 	}
  // }
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

func FindURIInMap(uri string, m map[string]interface{}) string {
  if m != nil {
    uriPieces1 := getURIPieces(uri)
    for uri2 := range m {
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

func GetFillerMarker(label string) string {
  return "{" + label + "}"
}

func GetFillers(text string) []string {
  return fillerRegExp.FindAllString(text, -1)
}

func GetFillersUnmarked(text string) []string {
  matches := GetFillers(text)
  for i, m := range matches {
    m = strings.TrimLeft(m, "{")
    matches[i] = strings.TrimRight(m, "}")
  }
  return matches
}

func GetFillerUnmarked(text string) string {
  fillers := GetFillersUnmarked(text)
  if len(fillers) > 0 {
    return fillers[0]
  }
  return ""
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

func IsReadinessProbe(r *http.Request) bool {
  return strings.EqualFold(r.RequestURI, global.ReadinessProbe)
}

func IsLivenessProbe(r *http.Request) bool {
  return strings.EqualFold(r.RequestURI, global.LivenessProbe)
}

func IsProbeRequest(r *http.Request) bool {
  return IsReadinessProbe(r) || IsLivenessProbe(r)
}

func GenerateRandomString(size int) string {
  r := rand.New(rand.NewSource(time.Now().UnixNano()))
  b := make([]byte, size)
  for i := range b {
    b[i] = charset[r.Intn(len(charset))]
  }
  return string(b)
}
