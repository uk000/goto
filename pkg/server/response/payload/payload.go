package payload

import (
  "context"
  "fmt"
  "goto/pkg/events"
  "goto/pkg/server/intercept"
  "goto/pkg/util"
  "io"
  "io/ioutil"
  "net"
  "net/http"
  "regexp"
  "strconv"
  "strings"
  "sync"
  "time"

  "github.com/gorilla/mux"
  "k8s.io/client-go/util/jsonpath"
)

type BodyPath struct {
  Path       string `json:"path"`
  CaptureKey string `json:"captureKey"`
}

type ResponsePayload struct {
  Payload          string      `json:"payload"`
  ContentType      string      `json:"contentType"`
  URIMatch         string      `json:"uriMatch"`
  HeaderMatch      string      `json:"headerMatch"`
  HeaderValueMatch string      `json:"headerValueMatch"`
  QueryMatch       string      `json:"queryMatch"`
  QueryValueMatch  string      `json:"queryValueMatch"`
  BodyMatch        []string    `json:"bodyMatch"`
  BodyPaths        []*BodyPath `json:"bodyPaths"`
  URICaptureKeys   []string    `json:"uriCaptureKeys"`
  HeaderCaptureKey string      `json:"headerCaptureKey"`
  QueryCaptureKey  string      `json:"queryCaptureKey"`
  uriRegexp        *regexp.Regexp
  bodyMatchRegexp  *regexp.Regexp
  bodyJsonPaths    []*jsonpath.JSONPath
  fillers          []string
  router           *mux.Router
}

type PortResponse struct {
  DefaultResponsePayload         *ResponsePayload                                  `json:"defaultResponsePayload"`
  ResponsePayloadByURIs          map[string]*ResponsePayload                       `json:"responsePayloadByURIs"`
  ResponsePayloadByHeaders       map[string]map[string]*ResponsePayload            `json:"responsePayloadByHeaders"`
  ResponsePayloadByURIAndHeaders map[string]map[string]map[string]*ResponsePayload `json:"responsePayloadByURIAndHeaders"`
  ResponsePayloadByQuery         map[string]map[string]*ResponsePayload            `json:"responsePayloadByQuery"`
  ResponsePayloadByURIAndQuery   map[string]map[string]map[string]*ResponsePayload `json:"responsePayloadByURIAndQuery"`
  ResponsePayloadByURIAndBody    map[string]map[string]*ResponsePayload            `json:"responsePayloadByURIAndBody"`
  allURIResponsePayloads         map[string]*ResponsePayload
  lock                           sync.RWMutex
}

var (
  Handler       util.ServerHandler       = util.ServerHandler{Name: "response.payload", SetRoutes: SetRoutes, Middleware: Middleware}
  portResponses map[string]*PortResponse = map[string]*PortResponse{}
  rootRouter    *mux.Router
  matchRouter   *mux.Router
  payloadKey    = &util.ContextKey{"payloadKey"}
  captureKey    = &util.ContextKey{"captureKey"}
  responseLock  sync.RWMutex
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  rootRouter = root
  matchRouter = rootRouter.NewRoute().Subrouter()
  payloadRouter := util.PathRouter(r, "/payload")
  util.AddRouteWithPort(payloadRouter, "/set/default/{size}", setResponsePayload, "POST")
  util.AddRouteWithPort(payloadRouter, "/set/default", setResponsePayload, "POST")
  util.AddRouteQWithPort(payloadRouter, "/set/uri", setResponsePayload, "uri", "{uri}", "POST")
  util.AddRouteQWithPort(payloadRouter, "/set/header/{header}={value}", setResponsePayload, "uri", "{uri}", "POST")
  util.AddRouteWithPort(payloadRouter, "/set/header/{header}={value}", setResponsePayload, "POST")
  util.AddRouteQWithPort(payloadRouter, "/set/header/{header}", setResponsePayload, "uri", "{uri}", "POST")
  util.AddRouteWithPort(payloadRouter, "/set/header/{header}", setResponsePayload, "POST")
  util.AddRouteQWithPort(payloadRouter, "/set/query/{q}={value}", setResponsePayload, "uri", "{uri}", "POST")
  util.AddRouteWithPort(payloadRouter, "/set/query/{q}={value}", setResponsePayload, "POST")
  util.AddRouteQWithPort(payloadRouter, "/set/query/{q}", setResponsePayload, "uri", "{uri}", "POST")
  util.AddRouteWithPort(payloadRouter, "/set/query/{q}", setResponsePayload, "POST")
  util.AddRouteQWithPort(payloadRouter, "/set/body~{regexes}", setResponsePayload, "uri", "{uri}", "POST")
  util.AddRouteQWithPort(payloadRouter, "/set/body/paths/{paths}", setResponsePayload, "uri", "{uri}", "POST")
  util.AddRouteWithPort(payloadRouter, "/clear", clearResponsePayload, "POST")
  util.AddRouteWithPort(payloadRouter, "", getResponsePayload, "GET")
  util.AddRoute(root, "/payload/{size}", respondWithPayload, "GET", "PUT", "POST")
  util.AddRoute(root, "/stream/payload={size}/duration={duration}/delay={delay}", streamResponse, "GET", "PUT", "POST")
  util.AddRoute(root, "/stream/chunksize={chunk}/duration={duration}/delay={delay}", streamResponse, "GET", "PUT", "POST")
  util.AddRoute(root, "/stream/chunksize={chunk}/count={count}/delay={delay}", streamResponse, "GET", "PUT", "POST")
  util.AddRoute(root, "/stream/duration={duration}/delay={delay}", streamResponse, "GET", "PUT", "POST")
  util.AddRoute(root, "/stream/count={count}/delay={delay}", streamResponse, "GET", "PUT", "POST")
}

func (pr *PortResponse) init() {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  pr.DefaultResponsePayload = nil
  pr.ResponsePayloadByURIs = map[string]*ResponsePayload{}
  pr.ResponsePayloadByHeaders = map[string]map[string]*ResponsePayload{}
  pr.ResponsePayloadByURIAndHeaders = map[string]map[string]map[string]*ResponsePayload{}
  pr.ResponsePayloadByQuery = map[string]map[string]*ResponsePayload{}
  pr.ResponsePayloadByURIAndQuery = map[string]map[string]map[string]*ResponsePayload{}
  pr.ResponsePayloadByURIAndBody = map[string]map[string]*ResponsePayload{}
  pr.allURIResponsePayloads = map[string]*ResponsePayload{}
  matchRouter = rootRouter.NewRoute().Subrouter()
}

func newResponsePayload(payload, contentType, uri, header, query, value string, bodyRegexes []string, paths []string) (*ResponsePayload, error) {
  if contentType == "" {
    contentType = "application/json"
  }
  var uriRegExp *regexp.Regexp
  var responseRouter *mux.Router
  if uri != "" {
    if rr, re, err := util.RegisterURIRouteAndGetRegex(uri, matchRouter, handleURI); err == nil {
      uriRegExp = re
      responseRouter = rr
    } else {
      return nil, fmt.Errorf("Failed to add URI match %s with error: %s\n", uri, err.Error())
    }
  }
  headerValueMatch := ""
  headerCaptureKey := ""
  queryValueMatch := ""
  queryCaptureKey := ""
  if key, present := util.GetFillerUnmarked(value); present {
    if header != "" {
      headerCaptureKey = key
    } else if query != "" {
      queryCaptureKey = key
    }
  } else if header != "" {
    headerValueMatch = value
  } else if query != "" {
    queryValueMatch = value
  }

  bodyPaths := []*BodyPath{}
  bodyJsonPaths := []*jsonpath.JSONPath{}
  for _, path := range paths {
    pathKV := strings.Split(path, "=")
    path = pathKV[0]
    key := ""
    if len(pathKV) > 1 {
      key, _ = util.GetFillerUnmarked(pathKV[1])
    }
    jp := jsonpath.New(path)
    jp.Parse("{" + path + "}")
    bodyJsonPaths = append(bodyJsonPaths, jp)
    bodyPaths = append(bodyPaths, &BodyPath{Path: path, CaptureKey: key})
  }

  var bodyMatchRegexp *regexp.Regexp
  if len(bodyRegexes) > 0 {
    bodyMatchRegexp = regexp.MustCompile("(?i)" + strings.Join(bodyRegexes, ".*") + ".*")
  }

  return &ResponsePayload{
    Payload:          payload,
    ContentType:      contentType,
    URIMatch:         uri,
    HeaderMatch:      header,
    HeaderValueMatch: headerValueMatch,
    QueryMatch:       query,
    QueryValueMatch:  queryValueMatch,
    BodyMatch:        bodyRegexes,
    BodyPaths:        bodyPaths,
    uriRegexp:        uriRegExp,
    bodyMatchRegexp:  bodyMatchRegexp,
    bodyJsonPaths:    bodyJsonPaths,
    URICaptureKeys:   util.GetFillersUnmarked(uri),
    HeaderCaptureKey: headerCaptureKey,
    QueryCaptureKey:  queryCaptureKey,
    fillers:          util.GetFillersUnmarked(payload),
    router:           responseRouter,
  }, nil
}

func fixPayload(payload string, size int) string {
  if size > 0 {
    if payload == "" {
      payload = util.GenerateRandomString(size)
    } else if len(payload) < size {
      payload = strings.Join([]string{payload, util.GenerateRandomString(size - len(payload))}, "")
    } else if len(payload) > size {
      a := []rune(payload)
      payload = string(a[:size])
    }
  }
  return payload
}

func (pr *PortResponse) setDefaultResponsePayload(payload, contentType string, size int) {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  if size > 0 {
    payload = fixPayload(payload, size)
  }
  pr.DefaultResponsePayload, _ = newResponsePayload(payload, contentType, "", "", "", "", nil, nil)
}

func (pr *PortResponse) unsafeIsURIMapped(uri string) bool {
  return pr.ResponsePayloadByURIs[uri] != nil || pr.ResponsePayloadByURIAndHeaders[uri] != nil ||
    pr.ResponsePayloadByURIAndQuery[uri] != nil || pr.ResponsePayloadByURIAndBody[uri] != nil
}

func (pr *PortResponse) unsafeRemoveUntrackeddURI(uri string) {
  if !pr.unsafeIsURIMapped(uri) {
    delete(pr.allURIResponsePayloads, uri)
  }
}

func (pr *PortResponse) setURIResponsePayload(uri, payload, contentType string) error {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  uri = strings.ToLower(uri)
  if payload != "" {
    if rp, err := newResponsePayload(payload, contentType, uri, "", "", "", nil, nil); err == nil {
      pr.ResponsePayloadByURIs[uri] = rp
      pr.allURIResponsePayloads[uri] = rp
    } else {
      return err
    }
  } else if pr.ResponsePayloadByURIs[uri] != nil {
    delete(pr.ResponsePayloadByURIs, uri)
    pr.unsafeRemoveUntrackeddURI(uri)
  }
  return nil
}

func (pr *PortResponse) setHeaderResponsePayload(header, value, payload, contentType string) {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  header = strings.ToLower(header)
  value = strings.ToLower(value)
  if payload != "" {
    if pr.ResponsePayloadByHeaders[header] == nil {
      pr.ResponsePayloadByHeaders[header] = map[string]*ResponsePayload{}
    }
    rp, _ := newResponsePayload(payload, contentType, "", header, "", value, nil, nil)
    pr.ResponsePayloadByHeaders[header][rp.HeaderValueMatch] = rp
  } else if pr.ResponsePayloadByHeaders[header] != nil {
    if _, present := util.GetFillerUnmarked(value); present {
      value = ""
    }
    if pr.ResponsePayloadByHeaders[header][value] != nil {
      delete(pr.ResponsePayloadByHeaders[header], value)
    }
    if len(pr.ResponsePayloadByHeaders[header]) == 0 {
      delete(pr.ResponsePayloadByHeaders, header)
    }
  }
}

func (pr *PortResponse) setQueryResponsePayload(query, value, payload, contentType string) {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  query = strings.ToLower(query)
  value = strings.ToLower(value)
  if payload != "" {
    if pr.ResponsePayloadByQuery[query] == nil {
      pr.ResponsePayloadByQuery[query] = map[string]*ResponsePayload{}
    }
    rp, _ := newResponsePayload(payload, contentType, "", "", query, value, nil, nil)
    pr.ResponsePayloadByQuery[query][rp.QueryValueMatch] = rp
  } else if pr.ResponsePayloadByQuery[query] != nil {
    if _, present := util.GetFillerUnmarked(value); present {
      value = ""
    }
    if pr.ResponsePayloadByQuery[query][value] != nil {
      delete(pr.ResponsePayloadByQuery[query], value)
    }
    if len(pr.ResponsePayloadByQuery[query]) == 0 {
      delete(pr.ResponsePayloadByQuery, query)
    }
  }
}

func (pr *PortResponse) setResponsePayloadForURIWithHeader(uri, header, value, payload, contentType string) error {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  uri = strings.ToLower(uri)
  header = strings.ToLower(header)
  value = strings.ToLower(value)
  if payload != "" {
    if pr.ResponsePayloadByURIAndHeaders[uri] == nil {
      pr.ResponsePayloadByURIAndHeaders[uri] = map[string]map[string]*ResponsePayload{}
    }
    if pr.ResponsePayloadByURIAndHeaders[uri][header] == nil {
      pr.ResponsePayloadByURIAndHeaders[uri][header] = map[string]*ResponsePayload{}
    }
    if rp, err := newResponsePayload(payload, contentType, uri, header, "", value, nil, nil); err == nil {
      pr.ResponsePayloadByURIAndHeaders[uri][header][rp.HeaderValueMatch] = rp
      pr.allURIResponsePayloads[uri] = rp
    } else {
      return err
    }
  } else if pr.ResponsePayloadByURIAndHeaders[uri] != nil {
    if pr.ResponsePayloadByURIAndHeaders[uri][header] != nil {
      if _, present := util.GetFillerUnmarked(value); present {
        value = ""
      }
      if pr.ResponsePayloadByURIAndHeaders[uri][header][value] != nil {
        delete(pr.ResponsePayloadByURIAndHeaders[uri][header], value)
      }
      if len(pr.ResponsePayloadByURIAndHeaders[uri][header]) == 0 {
        delete(pr.ResponsePayloadByURIAndHeaders[uri], header)
      }
    }
    if len(pr.ResponsePayloadByURIAndHeaders[uri]) == 0 {
      delete(pr.ResponsePayloadByURIAndHeaders, uri)
      pr.unsafeRemoveUntrackeddURI(uri)
    }
  }
  return nil
}

func (pr *PortResponse) setResponsePayloadForURIWithQuery(uri, query, value, payload, contentType string) error {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  uri = strings.ToLower(uri)
  query = strings.ToLower(query)
  value = strings.ToLower(value)
  if payload != "" {
    if pr.ResponsePayloadByURIAndQuery[uri] == nil {
      pr.ResponsePayloadByURIAndQuery[uri] = map[string]map[string]*ResponsePayload{}
    }
    if pr.ResponsePayloadByURIAndQuery[uri][query] == nil {
      pr.ResponsePayloadByURIAndQuery[uri][query] = map[string]*ResponsePayload{}
    }
    if rp, err := newResponsePayload(payload, contentType, uri, "", query, value, nil, nil); err == nil {
      pr.ResponsePayloadByURIAndQuery[uri][query][rp.QueryValueMatch] = rp
      pr.allURIResponsePayloads[uri] = rp
    } else {
      return err
    }
  } else if pr.ResponsePayloadByURIAndQuery[uri] != nil {
    if pr.ResponsePayloadByURIAndQuery[uri][query] != nil {
      if _, present := util.GetFillerUnmarked(value); present {
        value = ""
      }
      if pr.ResponsePayloadByURIAndQuery[uri][query][value] != nil {
        delete(pr.ResponsePayloadByURIAndQuery[uri][query], value)
      }
      if len(pr.ResponsePayloadByURIAndQuery[uri][query]) == 0 {
        delete(pr.ResponsePayloadByURIAndQuery[uri], query)
      }
    }
    if len(pr.ResponsePayloadByURIAndQuery[uri]) == 0 {
      delete(pr.ResponsePayloadByURIAndQuery, uri)
      pr.unsafeRemoveUntrackeddURI(uri)
    }
  }
  return nil
}

func (pr *PortResponse) setResponsePayloadForURIWithBodyMatch(uri, match, payload, contentType string, isPaths bool) error {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  uri = strings.ToLower(uri)
  if !isPaths {
    match = strings.ToLower(match)
  }
  if payload != "" {
    var rp *ResponsePayload
    var err error
    bodyMatch := strings.Split(match, ",")
    if isPaths {
      rp, err = newResponsePayload(payload, contentType, uri, "", "", "", nil, bodyMatch)
    } else {
      rp, err = newResponsePayload(payload, contentType, uri, "", "", "", bodyMatch, nil)
    }
    if err == nil {
      if pr.ResponsePayloadByURIAndBody[uri] == nil {
        pr.ResponsePayloadByURIAndBody[uri] = map[string]*ResponsePayload{}
      }
      pr.ResponsePayloadByURIAndBody[uri][match] = rp
      pr.allURIResponsePayloads[uri] = rp
    } else {
      return err
    }
  } else if pr.ResponsePayloadByURIAndBody[uri] != nil {
    if pr.ResponsePayloadByURIAndBody[uri][match] != nil {
      delete(pr.ResponsePayloadByURIAndBody[uri], match)
    }
    if len(pr.ResponsePayloadByURIAndBody[uri]) == 0 {
      delete(pr.ResponsePayloadByURIAndBody, uri)
      pr.unsafeRemoveUntrackeddURI(uri)
    }
  }
  return nil
}

func getPortResponse(r *http.Request) *PortResponse {
  port := util.GetRequestOrListenerPort(r)
  responseLock.Lock()
  defer responseLock.Unlock()
  pr := portResponses[port]
  if pr == nil {
    pr = &PortResponse{}
    pr.init()
    portResponses[port] = pr
  }
  return pr
}

func setResponsePayload(w http.ResponseWriter, r *http.Request) {
  msg := ""
  port := util.GetRequestOrListenerPort(r)
  payload := util.Read(r.Body)
  pr := getPortResponse(r)
  contentType := r.Header.Get("Content-Type")
  if contentType == "" {
    contentType = "plain/text"
  }
  uri := util.GetStringParamValue(r, "uri")
  header := util.GetStringParamValue(r, "header")
  query := util.GetStringParamValue(r, "q")
  value := util.GetStringParamValue(r, "value")
  regexes := util.GetStringParamValue(r, "regexes")
  paths := util.GetStringParamValue(r, "paths")
  if header != "" && uri != "" {
    if err := pr.setResponsePayloadForURIWithHeader(uri, header, value, payload, contentType); err == nil {
      msg = fmt.Sprintf("Port [%s] Payload set for URI [%s] and header [%s : %s] : [%s: %s]",
        port, uri, header, value, contentType, payload)
    } else {
      msg = fmt.Sprintf("Port [%s] Failed to set payload for URI [%s] and header [%s : %s] : [%s: %s] with error [%s]",
        port, uri, header, value, contentType, payload, err.Error())
    }
  } else if query != "" && uri != "" {
    if err := pr.setResponsePayloadForURIWithQuery(uri, query, value, payload, contentType); err == nil {
      msg = fmt.Sprintf("Port [%s] Payload set for URI [%s] and query [%s : %s] : [%s: %s]",
        port, uri, query, value, contentType, payload)
    } else {
      msg = fmt.Sprintf("Port [%s] Failed to set payload for URI [%s] and query [%s : %s] : [%s: %s] with error [%s]",
        port, uri, query, value, contentType, payload, err.Error())
    }
  } else if uri != "" && (regexes != "" || paths != "") {
    match := regexes
    if match == "" {
      match = paths
    }
    if err := pr.setResponsePayloadForURIWithBodyMatch(uri, match, payload, contentType, paths != ""); err == nil {
      msg = fmt.Sprintf("Port [%s] Payload set for URI [%s] and match [%+v] : [%s: %s]",
        port, uri, match, contentType, payload)
    } else {
      msg = fmt.Sprintf("Port [%s] Failed to set payload for URI [%s] and match [%+v] : [%s: %s] with error [%s]",
        port, uri, match, contentType, payload, err.Error())
    }
  } else if uri != "" {
    pr.setURIResponsePayload(uri, payload, contentType)
    msg = fmt.Sprintf("Port [%s] Payload set for URI [%s] : [%s: %s]",
      port, uri, contentType, payload)
  } else if header != "" {
    pr.setHeaderResponsePayload(header, value, payload, contentType)
    msg = fmt.Sprintf("Port [%s] Payload set for header [%s : %s] : [%s: %s]",
      port, header, value, contentType, payload)
  } else if query != "" {
    pr.setQueryResponsePayload(query, value, payload, contentType)
    msg = fmt.Sprintf("Port [%s] Payload set for query [%s : %s] : [%s: %s]",
      port, query, value, contentType, payload)
  } else {
    size := util.GetSizeParam(r, "size")
    pr.setDefaultResponsePayload(payload, contentType, size)
    if size > 0 {
      msg = fmt.Sprintf("Port [%s] Default Payload set with content-type: %s, size: %d",
        port, contentType, size)
    } else {
      msg = fmt.Sprintf("Port [%s] Default Payload set with content-type: %s, size: %d",
        port, contentType, len(pr.DefaultResponsePayload.Payload))
    }
  }
  w.WriteHeader(http.StatusOK)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
  events.SendRequestEvent("Response Payload Configured", msg, r)
}

func clearResponsePayload(w http.ResponseWriter, r *http.Request) {
  getPortResponse(r).init()
  msg := fmt.Sprintf("Port [%s] Response Payload Cleared", util.GetRequestOrListenerPort(r))
  w.WriteHeader(http.StatusOK)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
  events.SendRequestEvent("Response Payload Cleared", msg, r)
}

func getResponsePayload(w http.ResponseWriter, r *http.Request) {
  util.WriteJsonPayload(w, getPortResponse(r))
}

func respondWithPayload(w http.ResponseWriter, r *http.Request) {
  sizeV := util.GetStringParamValue(r, "size")
  size := util.GetSizeParam(r, "size")
  if size <= 0 {
    size = 100
  }
  payload := util.GenerateRandomString(size)
  fmt.Fprint(w, payload)
  w.Header().Set("Content-Length", sizeV)
  w.Header().Set("Content-Type", "plain/text")
  w.Header().Set("Goto-Payload-Length", sizeV)
  util.AddLogMessage(fmt.Sprintf("Responding with requested payload of length %d", size), r)
}

func streamResponse(w http.ResponseWriter, r *http.Request) {
  size := util.GetSizeParam(r, "size")
  chunk := util.GetSizeParam(r, "chunk")
  duration := util.GetDurationParam(r, "duration")
  delay := util.GetDurationParam(r, "delay")
  count := util.GetIntParamValue(r, "count")
  repeat := false
  payload := ""
  contentType := "plain/text"
  pr := getPortResponse(r)
  pr.lock.RLock()
  if pr.DefaultResponsePayload != nil {
    payload = pr.DefaultResponsePayload.Payload
    contentType = pr.DefaultResponsePayload.ContentType
  }
  pr.lock.RUnlock()

  if duration > 0 {
    count = int((duration.Milliseconds() / delay.Milliseconds()))
  }
  if size > 0 {
    repeat = true
    chunk = size / count
    payload = util.GenerateRandomString(chunk)
  } else {
    size = len(payload)
    repeat = size == 0
  }
  if size < chunk {
    payload = fixPayload(payload, chunk)
  }
  if delay == 0 {
    delay = 10 * time.Millisecond
  }
  if chunk == 0 && count > 0 && size > 0 {
    chunk = size/count + 1
  }
  if chunk == 0 || count == 0 {
    w.WriteHeader(http.StatusBadRequest)
    util.AddLogMessage("Invalid parameters for streaming or no payload", r)
    fmt.Fprintln(w, "{error: 'Invalid parameters for streaming'}")
    return
  }

  w.Header().Set("Content-Type", contentType)
  w.Header().Set("X-Content-Type-Options", "nosniff")
  w.Header().Set("Goto-Chunk-Count", strconv.Itoa(count))
  w.Header().Set("Goto-Chunk-Length", strconv.Itoa(chunk))
  w.Header().Set("Goto-Chunk-Delay", delay.String())
  if size > 0 {
    w.Header().Set("Goto-Stream-Length", strconv.Itoa(size))
  }
  if duration > 0 {
    w.Header().Set("Goto-Stream-Duration", duration.String())
  }

  var conn net.Conn
  var flusher http.Flusher
  var writer io.Writer
  if h, ok := w.(http.Hijacker); ok {
    if conn, _, _ = h.Hijack(); conn != nil {
      conn.SetWriteDeadline(time.Time{})
      writer = conn
    }
  }
  if conn == nil {
    if f, ok := w.(http.Flusher); ok {
      flusher = f
      if irw, ok := w.(*intercept.InterceptResponseWriter); ok {
        irw.SetChunked()
      }
      writer = w
    }
  }
  if writer == nil && flusher == nil {
    w.WriteHeader(http.StatusInternalServerError)
    fmt.Fprintln(w, "Cannot stream")
    return
  }
  util.AddLogMessage("Responding with streaming payload", r)
  payloadIndex := 0
  payloadSize := len(payload)
  payloadChunkCount := payloadSize / chunk
  if payloadSize%chunk > 0 {
    payloadChunkCount++
  }
  for i := 0; i < count; i++ {
    start := payloadIndex * chunk
    end := (payloadIndex + 1) * chunk
    if end > payloadSize {
      end = payloadSize
    }
    chunkResponse := string(payload[start:end])
    fmt.Fprint(writer, chunkResponse)
    if flusher != nil {
      flusher.Flush()
    }
    payloadIndex++
    if payloadIndex == payloadChunkCount {
      if repeat {
        payloadIndex = 0
      } else {
        break
      }
    }
    if i < count-1 {
      time.Sleep(delay)
    }
  }
  if conn != nil {
    conn.Close()
  }
}

func getPayloadForKV(kvMap map[string][]string, payloadMap map[string]map[string]*ResponsePayload) (*ResponsePayload, bool) {
  if len(kvMap) == 0 || len(payloadMap) == 0 {
    return nil, false
  }
  for k, kv := range kvMap {
    k = strings.ToLower(k)
    if payloadMap[k] != nil {
      for _, v := range kv {
        v = strings.ToLower(v)
        if p, found := payloadMap[k][v]; found {
          return p, found
        }
      }
      if p, found := payloadMap[k][""]; found {
        return p, found
      }
    }
  }
  return nil, false
}

func getFilledPayload(rp *ResponsePayload, r *http.Request, captures map[string]string) string {
  vars := mux.Vars(r)
  payload := rp.Payload
  for _, key := range rp.URICaptureKeys {
    if vars[key] != "" {
      payload = strings.Replace(payload, util.GetFillerMarked(key), vars[key], -1)
    }
  }
  if rp.HeaderCaptureKey != "" {
    if value := r.Header.Get(rp.HeaderMatch); value != "" {
      payload = strings.Replace(payload, util.GetFillerMarked(rp.HeaderCaptureKey), value, -1)
    }
  }
  if rp.QueryCaptureKey != "" {
    for k, values := range r.URL.Query() {
      if strings.EqualFold(strings.ToLower(k), rp.QueryMatch) && len(values) > 0 {
        payload = strings.Replace(payload, util.GetFillerMarked(rp.QueryCaptureKey), values[0], -1)
      }
    }
  }
  for k, v := range captures {
    payload = strings.Replace(payload, util.GetFillerMarked(k), v, -1)
  }
  return payload
}

func getPayloadForBodyMatch(r *http.Request, bodyMatchResponses map[string]*ResponsePayload) (*ResponsePayload, map[string]string, bool) {
  if len(bodyMatchResponses) == 0 {
    return nil, nil, false
  }
  body := util.Read(r.Body)
  lowerBody := strings.ToLower(body)
  var matchedResponsePayload *ResponsePayload
  var captures map[string]string
  for _, rp := range bodyMatchResponses {
    if rp.bodyMatchRegexp != nil && rp.bodyMatchRegexp.MatchString(lowerBody) {
      matchedResponsePayload = rp
      break
    } else if len(rp.bodyJsonPaths) > 0 {
      allMatched := true
      captures = map[string]string{}
      var data map[string]interface{}
      if err := util.ReadJson(body, &data); err == nil {
        for i, jp := range rp.bodyJsonPaths {
          if matches, err := jp.FindResults(data); err == nil && len(matches) > 0 && len(matches[0]) > 0 {
            if rp.BodyPaths[i].CaptureKey != "" {
              captures[rp.BodyPaths[i].CaptureKey] = fmt.Sprintf("%v", matches[0][0].Interface())
            }
          } else {
            allMatched = false
            break
          }
        }
      } else {
        allMatched = false
        break
      }
      if allMatched {
        matchedResponsePayload = rp
        break
      }
    }
  }
  r.Body = ioutil.NopCloser(strings.NewReader(body))
  if matchedResponsePayload != nil {
    return matchedResponsePayload, captures, true
  }
  return nil, nil, false
}

func (pr *PortResponse) unsafeGetResponsePayload(r *http.Request) (*ResponsePayload, map[string]string, bool) {
  var payload *ResponsePayload
  var captures map[string]string
  found := false
  for uri, rp := range pr.allURIResponsePayloads {
    if rp.uriRegexp.MatchString(r.RequestURI) {
      if !found && pr.ResponsePayloadByURIAndHeaders[uri] != nil {
        payload, found = getPayloadForKV(r.Header, pr.ResponsePayloadByURIAndHeaders[uri])
      }
      if !found && pr.ResponsePayloadByURIAndQuery[uri] != nil {
        payload, found = getPayloadForKV(r.URL.Query(), pr.ResponsePayloadByURIAndQuery[uri])
      }
      if !found && pr.ResponsePayloadByURIAndBody[uri] != nil {
        payload, captures, found = getPayloadForBodyMatch(r, pr.ResponsePayloadByURIAndBody[uri])
      }
      if !found && pr.ResponsePayloadByURIs[uri] != nil {
        payload = pr.ResponsePayloadByURIs[uri]
        found = true
      }
      if found {
        break
      }
    }
  }
  if !found {
    payload, found = getPayloadForKV(r.Header, pr.ResponsePayloadByHeaders)
  }
  if !found {
    payload, found = getPayloadForKV(r.URL.Query(), pr.ResponsePayloadByQuery)
  }
  if !found && pr.DefaultResponsePayload != nil {
    payload = pr.DefaultResponsePayload
    found = true
  }
  return payload, captures, found
}

func processPayload(w http.ResponseWriter, r *http.Request, rp *ResponsePayload, captures map[string]string) {
  payload := ""
  contentType := ""
  payload = getFilledPayload(rp, r, captures)
  contentType = rp.ContentType
  length := strconv.Itoa(len(payload))
  w.Header().Set("Content-Length", length)
  w.Header().Set("Content-Type", contentType)
  w.Header().Set("Goto-Payload-Length", length)
  w.Header().Set("Goto-Payload-Content-Type", contentType)
  fmt.Fprint(w, payload)
  msg := fmt.Sprintf("Responding with configured payload of length [%s] and content type [%s] for URI [%s]",
    length, contentType, r.RequestURI)
  util.AddLogMessage(msg, r)
  util.UpdateTrafficEventDetails(r, "Response Payload Applied")
}

func handleURI(w http.ResponseWriter, r *http.Request) {
  processPayload(w, r, r.Context().Value(payloadKey).(*ResponsePayload), r.Context().Value(captureKey).(map[string]string))
}

func Middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    if util.IsKnownNonTraffic(r) {
      if next != nil {
        next.ServeHTTP(w, r)
      }
      return
    }
    var payload *ResponsePayload
    if !util.IsPayloadRequest(r) {
      pr := getPortResponse(r)
      pr.lock.RLock()
      rp, captures, found := pr.unsafeGetResponsePayload(r)
      pr.lock.RUnlock()
      if found {
        payload = rp
        if rp.router != nil {
          rp.router.ServeHTTP(w, r.WithContext(context.WithValue(
            context.WithValue(r.Context(), payloadKey, payload), captureKey, captures)))
        } else {
          processPayload(w, r, rp, captures)
        }
      }
    }
    if next != nil && (payload == nil || util.IsStatusRequest(r) || util.IsDelayRequest(r)) {
      next.ServeHTTP(w, r)
    }
  })
}
