package payload

import (
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
)

type ResponsePayload struct {
  Payload          string   `json:"payload"`
  ContentType      string   `json:"contentType"`
  URIMatch         string   `json:"uriMatch"`
  HeaderMatch      string   `json:"headerMatch"`
  HeaderValueMatch string   `json:"headerValueMatch"`
  QueryMatch       string   `json:"queryMatch"`
  QueryValueMatch  string   `json:"queryValueMatch"`
  BodyMatch        []string `json:"bodyMatch"`
  URICaptureKeys   []string `json:"uriCaptureKeys"`
  HeaderCaptureKey string   `json:"headerCaptureKey"`
  QueryCaptureKey  string   `json:"queryCaptureKey"`
  uriRegExp        *regexp.Regexp
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
  responseLock  sync.RWMutex
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  rootRouter = root
  matchRouter = rootRouter.NewRoute().Subrouter()
  payloadRouter := r.PathPrefix("/payload").Subrouter()
  util.AddRoute(payloadRouter, "/set/default/{size}", setResponsePayload, "POST")
  util.AddRoute(payloadRouter, "/set/default", setResponsePayload, "POST")
  util.AddRouteQ(payloadRouter, "/set/uri", setResponsePayload, "uri", "{uri}", "POST")
  util.AddRouteQ(payloadRouter, "/set/header/{header}/{value}", setResponsePayload, "uri", "{uri}", "POST")
  util.AddRoute(payloadRouter, "/set/header/{header}/{value}", setResponsePayload, "POST")
  util.AddRouteQ(payloadRouter, "/set/header/{header}", setResponsePayload, "uri", "{uri}", "POST")
  util.AddRoute(payloadRouter, "/set/header/{header}", setResponsePayload, "POST")
  util.AddRouteQ(payloadRouter, "/set/query/{q}/{value}", setResponsePayload, "uri", "{uri}", "POST")
  util.AddRoute(payloadRouter, "/set/query/{q}/{value}", setResponsePayload, "POST")
  util.AddRouteQ(payloadRouter, "/set/query/{q}", setResponsePayload, "uri", "{uri}", "POST")
  util.AddRoute(payloadRouter, "/set/query/{q}", setResponsePayload, "POST")
  util.AddRouteQ(payloadRouter, "/set/body/contains/{keywords}", setResponsePayload, "uri", "{uri}", "POST")
  util.AddRoute(payloadRouter, "/clear", clearResponsePayload, "POST")
  util.AddRoute(payloadRouter, "", getResponsePayload, "GET")
  util.AddRoute(parent, "/payload/{size}", respondWithPayload, "GET", "PUT", "POST")
  util.AddRoute(parent, "/stream/size/{size}/duration/{duration}/delay/{delay}", streamResponse, "GET", "PUT", "POST")
  util.AddRoute(parent, "/stream/chunk/{chunk}/duration/{duration}/delay/{delay}", streamResponse, "GET", "PUT", "POST")
  util.AddRoute(parent, "/stream/chunk/{chunk}/count/{count}/delay/{delay}", streamResponse, "GET", "PUT", "POST")
  util.AddRoute(parent, "/stream/duration/{duration}/delay/{delay}", streamResponse, "GET", "PUT", "POST")
  util.AddRoute(parent, "/stream/count/{count}/delay/{delay}", streamResponse, "GET", "PUT", "POST")
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

func newResponsePayload(payload, contentType, uri, header, query, value string, bodyMatch []string) (*ResponsePayload, error) {
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

  return &ResponsePayload{
    Payload:          payload,
    ContentType:      contentType,
    URIMatch:         uri,
    HeaderMatch:      header,
    HeaderValueMatch: headerValueMatch,
    QueryMatch:       query,
    QueryValueMatch:  queryValueMatch,
    BodyMatch:        bodyMatch,
    uriRegExp:        uriRegExp,
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
  pr.DefaultResponsePayload, _ = newResponsePayload(payload, contentType, "", "", "", "", nil)
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
    if rp, err := newResponsePayload(payload, contentType, uri, "", "", "", nil); err == nil {
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
    rp, _ := newResponsePayload(payload, contentType, "", header, "", value, nil)
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
    rp, _ := newResponsePayload(payload, contentType, "", "", query, value, nil)
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
    if rp, err := newResponsePayload(payload, contentType, uri, header, "", value, nil); err == nil {
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
    if rp, err := newResponsePayload(payload, contentType, uri, "", query, value, nil); err == nil {
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

func (pr *PortResponse) setResponsePayloadForURIWithBody(uri, keywords, payload, contentType string) error {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  uri = strings.ToLower(uri)
  keywords = strings.ToLower(keywords)
  if payload != "" {
    bodyMatch := strings.Split(keywords, ",")
    if rp, err := newResponsePayload(payload, contentType, uri, "", "", "", bodyMatch); err == nil {
      if pr.ResponsePayloadByURIAndBody[uri] == nil {
        pr.ResponsePayloadByURIAndBody[uri] = map[string]*ResponsePayload{}
      }
      pr.ResponsePayloadByURIAndBody[uri][keywords] = rp
      pr.allURIResponsePayloads[uri] = rp
    } else {
      return err
    }
  } else if pr.ResponsePayloadByURIAndBody[uri] != nil {
    if pr.ResponsePayloadByURIAndBody[uri][keywords] != nil {
      delete(pr.ResponsePayloadByURIAndBody[uri], keywords)
    }
    if len(pr.ResponsePayloadByURIAndBody[uri]) == 0 {
      delete(pr.ResponsePayloadByURIAndBody, uri)
      pr.unsafeRemoveUntrackeddURI(uri)
    }
  }
  return nil
}

func getPortResponse(r *http.Request) *PortResponse {
  port := util.GetListenerPort(r)
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
  payload := util.Read(r.Body)
  pr := getPortResponse(r)
  contentType := r.Header.Get("Content-Type")
  uri := util.GetStringParamValue(r, "uri")
  header := util.GetStringParamValue(r, "header")
  query := util.GetStringParamValue(r, "q")
  value := util.GetStringParamValue(r, "value")
  keywords := util.GetStringParamValue(r, "keywords")
  if header != "" && uri != "" {
    if err := pr.setResponsePayloadForURIWithHeader(uri, header, value, payload, contentType); err == nil {
      msg = fmt.Sprintf("Payload set for URI [%s] and header [%s : %s] : [%s: %s]", uri, header, value, contentType, payload)
    } else {
      msg = fmt.Sprintf("Failed to set payload for URI [%s] and header [%s : %s] : [%s: %s] with error [%s]", uri, header, value, contentType, payload, err.Error())
    }
  } else if query != "" && uri != "" {
    if err := pr.setResponsePayloadForURIWithQuery(uri, query, value, payload, contentType); err == nil {
      msg = fmt.Sprintf("Payload set for URI [%s] and query [%s : %s] : [%s: %s]", uri, query, value, contentType, payload)
    } else {
      msg = fmt.Sprintf("Failed to set payload for URI [%s] and query [%s : %s] : [%s: %s] with error [%s]", uri, query, value, contentType, payload, err.Error())
    }
  } else if uri != "" && keywords != "" {
    if err := pr.setResponsePayloadForURIWithBody(uri, keywords, payload, contentType); err == nil {
      msg = fmt.Sprintf("Payload set for URI [%s] and keywords [%+v] : [%s: %s]", uri, keywords, contentType, payload)
    } else {
      msg = fmt.Sprintf("Failed to set payload for URI [%s] and keywords [%+v] : [%s: %s] with error [%s]", uri, keywords, contentType, payload, err.Error())
    }
  } else if uri != "" {
    pr.setURIResponsePayload(uri, payload, contentType)
    msg = fmt.Sprintf("Payload set for URI [%s] : [%s: %s]", uri, contentType, payload)
  } else if header != "" {
    pr.setHeaderResponsePayload(header, value, payload, contentType)
    msg = fmt.Sprintf("Payload set for header [%s : %s] : [%s: %s]", header, value, contentType, payload)
  } else if query != "" {
    pr.setQueryResponsePayload(query, value, payload, contentType)
    msg = fmt.Sprintf("Payload set for query [%s : %s] : [%s: %s]", query, value, contentType, payload)
  } else {
    size := util.GetSizeParam(r, "size")
    pr.setDefaultResponsePayload(payload, contentType, size)
    if size > 0 {
      msg = fmt.Sprintf("Default Payload set with content-type: %s, size: %d", contentType, size)
    } else {
      msg = fmt.Sprintf("Default Payload set : [%s: %s]", pr.DefaultResponsePayload.ContentType, pr.DefaultResponsePayload.Payload)
    }
  }
  w.WriteHeader(http.StatusOK)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
  events.SendRequestEvent("Response Payload Configured", msg, r)
}

func clearResponsePayload(w http.ResponseWriter, r *http.Request) {
  getPortResponse(r).init()
  msg := "Response Payload Cleared"
  w.WriteHeader(http.StatusOK)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
  events.SendRequestEvent(msg, "", r)
}

func getResponsePayload(w http.ResponseWriter, r *http.Request) {
  w.WriteHeader(http.StatusOK)
  fmt.Fprintln(w, util.ToJSON(getPortResponse(r)))
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
        irw.Chunked = true
      }
      writer = w
    }
  }
  if conn == nil && flusher == nil {
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

func getFilledPayload(rp *ResponsePayload, r *http.Request) string {
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
  return payload
}

func getPayloadForBodyMatch(r *http.Request, bodyMatches map[string]*ResponsePayload) (*ResponsePayload, bool) {
  if len(bodyMatches) == 0 {
    return nil, false
  }
  body := strings.ToLower(util.Read(r.Body))
  var matchedResponsePayload *ResponsePayload
  for _, rp := range bodyMatches {
    text := body
    currentMatch := true
    for _, key := range rp.BodyMatch {
      if strings.Contains(text, key) {
        if pieces := strings.SplitN(text, key, 2); len(pieces) == 2 {
          text = pieces[1]
        } else {
          text = ""
        }
      } else {
        currentMatch = false
        break
      }
    }
    if currentMatch {
      matchedResponsePayload = rp
      break
    }
  }
  r.Body = ioutil.NopCloser(strings.NewReader(body))
  if matchedResponsePayload != nil {
    return matchedResponsePayload, true
  }
  return nil, false
}

func (pr *PortResponse) getResponsePayload(r *http.Request) (*ResponsePayload, bool) {
  var payload *ResponsePayload
  found := false
  for uri, rp := range pr.allURIResponsePayloads {
    if rp.uriRegExp.MatchString(r.RequestURI) {
      if pr.ResponsePayloadByURIAndHeaders[uri] != nil {
        payload, found = getPayloadForKV(r.Header, pr.ResponsePayloadByURIAndHeaders[uri])
      } else if pr.ResponsePayloadByURIAndQuery[uri] != nil {
        payload, found = getPayloadForKV(r.URL.Query(), pr.ResponsePayloadByURIAndQuery[uri])
      } else if pr.ResponsePayloadByURIAndBody[uri] != nil {
        payload, found = getPayloadForBodyMatch(r, pr.ResponsePayloadByURIAndBody[uri])
      } else {
        payload = rp
        found = true
      }
      break
    }
  }
  if !found {
    payload, found = getPayloadForKV(r.Header, pr.ResponsePayloadByHeaders)
  }
  if !found {
    payload, found = getPayloadForKV(r.URL.Query(), pr.ResponsePayloadByQuery)
  }
  return payload, found
}

func handleURI(w http.ResponseWriter, r *http.Request) {
  pr := getPortResponse(r)
  pr.lock.RLock()
  defer pr.lock.RUnlock()
  rp, matched := pr.getResponsePayload(r)
  if !matched && pr.DefaultResponsePayload != nil {
    rp = pr.DefaultResponsePayload
    matched = true
  }
  if matched {
    payload := getFilledPayload(rp, r)
    length := len(payload)
    w.Header().Set("Content-Length", strconv.Itoa(length))
    w.Header().Set("Content-Type", rp.ContentType)
    fmt.Fprint(w, payload)
    msg := fmt.Sprintf("Responding with configured payload of length [%d] and content type [%s] for URI [%s]", length, rp.ContentType, r.RequestURI)
    util.AddLogMessage(msg, r)
    events.SendRequestEvent("Response Payload Applied", msg, r)
  }
}

func Middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    var uriResponseMatch *ResponsePayload
    pr := getPortResponse(r)
    if !util.IsAdminRequest(r) && !util.IsPayloadRequest(r) {
      pr.lock.RLock()
      for _, rp := range pr.allURIResponsePayloads {
        if rp.uriRegExp.MatchString(r.RequestURI) {
          uriResponseMatch = rp
          break
        }
      }
      pr.lock.RUnlock()
      if uriResponseMatch != nil {
        uriResponseMatch.router.ServeHTTP(w, r)
      }
    }
    if next != nil && (uriResponseMatch == nil || util.IsStatusRequest(r) || util.IsDelayRequest(r)) {
      next.ServeHTTP(w, r)
    }
  })
}
