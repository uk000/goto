package util

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
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
  logmessagesKey *ContextKey = &ContextKey{"logmessages"}
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
  if !IsAdminRequest(r) {
    log.Println(strings.Join(m.messages, " --> "))
  }
  m.messages = m.messages[:0]
}

func GetHostIP() string {
  conn, err := net.Dial("udp", "8.8.8.8:80")
  if err == nil {
    defer conn.Close()
    return conn.LocalAddr().(*net.UDPAddr).IP.String()
  }
  return "localhost"
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

func ReadJsonPayload(r *http.Request, t interface{}) error {
  if body, err := ioutil.ReadAll(r.Body); err != nil {
    return err
  } else {
    return json.Unmarshal([]byte(body), t)
  }
}

func WriteJsonPayload(w http.ResponseWriter, t interface{}) {
  w.WriteHeader(http.StatusOK)
  bytes, _ := json.Marshal(t)
  fmt.Fprintf(w, "%s\n", string(bytes))
}

func IsAdminRequest(r *http.Request) bool {
  return strings.HasPrefix(r.RequestURI, "/request") || strings.HasPrefix(r.RequestURI, "/response") ||
    strings.HasPrefix(r.RequestURI, "/listeners") || strings.HasPrefix(r.RequestURI, "/client") ||
    strings.HasPrefix(r.RequestURI, "/label")
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
