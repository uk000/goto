package util

import (
	"context"
	"encoding/json"
	"fmt"
	"goto/pkg/global"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"

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

type messagestore struct {
  messages []string
}

func ContextMiddleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), logmessagesKey, &messagestore{})))
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
  if !IsLockerRequest(r) && (!IsAdminRequest(r) || global.EnableAdminLogging) {
    log.Println(strings.Join(m.messages, " --> "))
  }
  m.messages = m.messages[:0]
}

func GetPodName() string {
  pod, present := os.LookupEnv("POD_NAME")
  if !present {
    pod, _ = os.Hostname()
  }
  return pod
}

func GetNamespace() string {
  ns, present := os.LookupEnv("NAMESPACE")
  if !present {
    ns = "local"
  }
  return ns
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
  return fmt.Sprintf("%s.%s@%s", GetPodName(), GetNamespace(), global.PeerAddress)
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

func GetListParam(r *http.Request, param string) ([]string, bool) {
  values := []string{}
  if v, present := GetStringParam(r, param); present {
    values = strings.Split(v, ",")
  }
  return values, len(values) > 0 && len(values[0]) > 0
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

func CopyHeaders(w http.ResponseWriter, headers http.Header, host string) {
  hostCopied := false
  for h, values := range headers {
    if !strings.Contains(h, "content") && !strings.Contains(h, "Content") {
      for _, v := range values {
        w.Header().Add(h, v)
      }
    }
    if strings.EqualFold(h, "host") {
      hostCopied = true
    }
  }
  if !hostCopied && host != "" {
    w.Header().Add("Host", host)
  }
}

func GetResponseHeadersLog(w http.ResponseWriter) string {
  var s strings.Builder
  s.Grow(128)
  fmt.Fprintf(&s, "{\"ResponseHeaders\": %s}", ToJSON(w.Header()))
  return s.String()
}

func GetRequestHeadersLog(r *http.Request) string {
  r.Header["Host"] = []string{r.Host}
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

func IsLockerRequest(r *http.Request) bool {
  return strings.HasPrefix(r.RequestURI, "/registry") && strings.Contains(r.RequestURI, "/locker") 
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
