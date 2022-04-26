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

package payload

import (
  "bytes"
  "context"
  "encoding/json"
  "fmt"
  . "goto/pkg/constants"
  "goto/pkg/events"
  "goto/pkg/server/conn"
  "goto/pkg/server/intercept"
  "goto/pkg/util"
  "io"
  "io/ioutil"
  "net/http"
  "regexp"
  "strconv"
  "strings"
  "sync"
  "time"

  "github.com/gorilla/mux"
  "k8s.io/client-go/util/jsonpath"
)

type ResponsePayload struct {
  Payload          []byte            `json:"payload"`
  ContentType      string            `json:"contentType"`
  URIMatch         string            `json:"uriMatch"`
  HeaderMatch      string            `json:"headerMatch"`
  HeaderValueMatch string            `json:"headerValueMatch"`
  QueryMatch       string            `json:"queryMatch"`
  QueryValueMatch  string            `json:"queryValueMatch"`
  BodyMatch        []string          `json:"bodyMatch"`
  BodyPaths        map[string]string `json:"bodyPaths"`
  URICaptureKeys   []string          `json:"uriCaptureKeys"`
  HeaderCaptureKey string            `json:"headerCaptureKey"`
  QueryCaptureKey  string            `json:"queryCaptureKey"`
  Transforms       []*util.Transform `json:"transforms"`
  uriRegexp        *regexp.Regexp
  queryMatchRegexp *regexp.Regexp
  bodyMatchRegexp  *regexp.Regexp
  bodyJsonPaths    map[string]*jsonpath.JSONPath
  isBinary         bool
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
  util.AddRouteWithPort(payloadRouter, "/set/default/binary/{size}", setResponsePayload, "POST")
  util.AddRouteWithPort(payloadRouter, "/set/default/binary", setResponsePayload, "POST")
  util.AddRouteWithPort(payloadRouter, "/set/default/{size}", setResponsePayload, "POST")
  util.AddRouteWithPort(payloadRouter, "/set/default", setResponsePayload, "POST")
  util.AddRouteQWithPort(payloadRouter, "/set/uri", setResponsePayload, "uri", "POST")
  util.AddRouteQWithPort(payloadRouter, "/set/header/{header}={value}", setResponsePayload, "uri", "POST")
  util.AddRouteWithPort(payloadRouter, "/set/header/{header}={value}", setResponsePayload, "POST")
  util.AddRouteQWithPort(payloadRouter, "/set/header/{header}", setResponsePayload, "uri", "POST")
  util.AddRouteWithPort(payloadRouter, "/set/header/{header}", setResponsePayload, "POST")
  util.AddRouteQWithPort(payloadRouter, "/set/query/{q}={value}", setResponsePayload, "uri", "POST")
  util.AddRouteWithPort(payloadRouter, "/set/query/{q}={value}", setResponsePayload, "POST")
  util.AddRouteQWithPort(payloadRouter, "/set/query/{q}", setResponsePayload, "uri", "POST")
  util.AddRouteWithPort(payloadRouter, "/set/query/{q}", setResponsePayload, "POST")
  util.AddRouteQWithPort(payloadRouter, "/set/body~{regexes}", setResponsePayload, "uri", "POST")
  util.AddRouteQWithPort(payloadRouter, "/set/body/paths/{paths}", setResponsePayload, "uri", "POST")
  util.AddRouteQWithPort(payloadRouter, "/transform", setPayloadTransform, "uri", "POST")
  util.AddRouteWithPort(payloadRouter, "/clear", clearResponsePayload, "POST")
  util.AddRouteWithPort(payloadRouter, "", getResponsePayload, "GET")
  util.AddRoute(root, "/payload/{size}", respondWithPayload, "GET", "PUT", "POST")
  util.AddRoute(root, "/stream/payload={payloadSize}/duration={duration}/delay={delay}", streamResponse, "GET", "PUT", "POST")
  util.AddRoute(root, "/stream/chunksize={chunkSize}/duration={duration}/delay={delay}", streamResponse, "GET", "PUT", "POST")
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

func newResponsePayload(payload []byte, binary bool, contentType, uri, header, query, value string,
  bodyRegexes []string, paths []string, transforms []*util.Transform) (*ResponsePayload, error) {
  if contentType == "" {
    contentType = ContentTypeJSON
  }
  var uriRegExp *regexp.Regexp
  var responseRouter *mux.Router
  if uri != "" {
    matchURI := uri
    glob := false
    if strings.HasSuffix(matchURI, "*") {
      matchURI = strings.ReplaceAll(matchURI, "*", "")
      glob = true
    }
    if rr, re, err := util.RegisterURIRouteAndGetRegex(matchURI, glob, matchRouter, handleURI); err == nil {
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
  if util.IsFiller(value) {
    if header != "" {
      headerCaptureKey = value
    } else if query != "" {
      queryCaptureKey = value
    }
  } else if header != "" {
    headerValueMatch = value
  } else if query != "" {
    queryValueMatch = value
  }

  jsonPaths := util.NewJSONPath().Parse(paths)

  var bodyMatchRegexp *regexp.Regexp
  if len(bodyRegexes) > 0 {
    bodyMatchRegexp = regexp.MustCompile("(?i)" + strings.Join(bodyRegexes, ".*") + ".*")
  }

  var fillers []string
  if !binary {
    fillers = util.GetFillersUnmarked(string(payload))
  }
  for _, t := range transforms {
    for _, m := range t.Mappings {
      m.Init()
    }
  }

  return &ResponsePayload{
    Payload:          payload,
    ContentType:      contentType,
    isBinary:         util.IsBinaryContentType(contentType),
    URIMatch:         uri,
    HeaderMatch:      header,
    HeaderValueMatch: headerValueMatch,
    QueryMatch:       query,
    QueryValueMatch:  queryValueMatch,
    BodyMatch:        bodyRegexes,
    BodyPaths:        jsonPaths.TextPaths,
    uriRegexp:        uriRegExp,
    queryMatchRegexp: regexp.MustCompile("(?i)" + query),
    bodyMatchRegexp:  bodyMatchRegexp,
    bodyJsonPaths:    jsonPaths.Paths,
    URICaptureKeys:   util.GetFillersUnmarked(uri),
    HeaderCaptureKey: headerCaptureKey,
    QueryCaptureKey:  queryCaptureKey,
    Transforms:       transforms,
    fillers:          fillers,
    router:           responseRouter,
  }, nil
}

func (rp ResponsePayload) MarshalJSON() ([]byte, error) {
  data := map[string]interface{}{
    "contentType":      rp.ContentType,
    "uriMatch":         rp.URIMatch,
    "headerMatch":      rp.HeaderMatch,
    "headerValueMatch": rp.HeaderValueMatch,
    "queryMatch":       rp.QueryMatch,
    "queryValueMatch":  rp.QueryValueMatch,
    "bodyMatch":        rp.BodyMatch,
    "uriCaptureKeys":   rp.URICaptureKeys,
    "headerCaptureKey": rp.HeaderCaptureKey,
    "queryCaptureKey":  rp.QueryCaptureKey,
    "transforms":       rp.Transforms,
    "binary":           rp.isBinary,
  }
  if rp.isBinary || len(rp.Payload) > 10000 {
    data["payload"] = fmt.Sprintf("...(%d bytes)", len(rp.Payload))
  } else {
    data["payload"] = string(rp.Payload)
  }
  return json.Marshal(data)
}

func fixPayload(payload []byte, size int) []byte {
  if size > 0 {
    if len(payload) == 0 {
      payload = util.GenerateRandomPayload(size)
    } else if len(payload) < size {
      payload = bytes.Join([][]byte{payload, util.GenerateRandomPayload(size - len(payload))}, []byte{})
    } else if len(payload) > size {
      payload = payload[:size]
    }
  }
  return payload
}

func (pr *PortResponse) setDefaultResponsePayload(payload []byte, contentType string, size int) {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  if size > 0 {
    payload = fixPayload(payload, size)
  }
  pr.DefaultResponsePayload, _ = newResponsePayload(payload, true, contentType, "", "", "", "", nil, nil, nil)
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

func (pr *PortResponse) setURIResponsePayload(payload []byte, binary bool, uri, contentType string, transforms []*util.Transform) error {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  uri = strings.ToLower(uri)
  if len(payload) > 0 || len(transforms) > 0 {
    if rp, err := newResponsePayload(payload, binary, contentType, uri, "", "", "", nil, nil, transforms); err == nil {
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

func (pr *PortResponse) setHeaderResponsePayload(payload []byte, binary bool, header, value, contentType string) {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  header = strings.ToLower(header)
  value = strings.ToLower(value)
  if len(payload) > 0 {
    if pr.ResponsePayloadByHeaders[header] == nil {
      pr.ResponsePayloadByHeaders[header] = map[string]*ResponsePayload{}
    }
    rp, _ := newResponsePayload(payload, binary, contentType, "", header, "", value, nil, nil, nil)
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

func (pr *PortResponse) setQueryResponsePayload(payload []byte, binary bool, query, value, contentType string) {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  query = strings.ToLower(query)
  value = strings.ToLower(value)
  if len(payload) > 0 {
    if pr.ResponsePayloadByQuery[query] == nil {
      pr.ResponsePayloadByQuery[query] = map[string]*ResponsePayload{}
    }
    rp, _ := newResponsePayload(payload, binary, contentType, "", "", query, value, nil, nil, nil)
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

func (pr *PortResponse) setResponsePayloadForURIWithHeader(payload []byte, binary bool, uri, header, value, contentType string) error {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  uri = strings.ToLower(uri)
  header = strings.ToLower(header)
  value = strings.ToLower(value)
  if len(payload) > 0 {
    if pr.ResponsePayloadByURIAndHeaders[uri] == nil {
      pr.ResponsePayloadByURIAndHeaders[uri] = map[string]map[string]*ResponsePayload{}
    }
    if pr.ResponsePayloadByURIAndHeaders[uri][header] == nil {
      pr.ResponsePayloadByURIAndHeaders[uri][header] = map[string]*ResponsePayload{}
    }
    if rp, err := newResponsePayload(payload, binary, contentType, uri, header, "", value, nil, nil, nil); err == nil {
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

func (pr *PortResponse) setResponsePayloadForURIWithQuery(payload []byte, binary bool, uri, query, value, contentType string) error {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  uri = strings.ToLower(uri)
  query = strings.ToLower(query)
  value = strings.ToLower(value)
  if len(payload) > 0 {
    if pr.ResponsePayloadByURIAndQuery[uri] == nil {
      pr.ResponsePayloadByURIAndQuery[uri] = map[string]map[string]*ResponsePayload{}
    }
    if pr.ResponsePayloadByURIAndQuery[uri][query] == nil {
      pr.ResponsePayloadByURIAndQuery[uri][query] = map[string]*ResponsePayload{}
    }
    if rp, err := newResponsePayload(payload, binary, contentType, uri, "", query, value, nil, nil, nil); err == nil {
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

func (pr *PortResponse) setResponsePayloadForURIWithBodyMatch(payload []byte, binary bool, uri, match, contentType string, isPaths bool) error {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  uri = strings.ToLower(uri)
  if !isPaths {
    match = strings.ToLower(match)
  }
  if len(payload) > 0 {
    var rp *ResponsePayload
    var err error
    bodyMatch := strings.Split(match, ",")
    if isPaths {
      rp, err = newResponsePayload(payload, binary, contentType, uri, "", "", "", nil, bodyMatch, nil)
    } else {
      rp, err = newResponsePayload(payload, binary, contentType, uri, "", "", "", bodyMatch, nil, nil)
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
  payload := util.ReadBytes(r.Body)
  pr := getPortResponse(r)
  binary := util.IsBinaryContentHeader(r.Header) || strings.Contains(r.RequestURI, "binary")
  uri := util.GetStringParamValue(r, "uri")
  header := util.GetStringParamValue(r, "header")
  query := util.GetStringParamValue(r, "q")
  value := util.GetStringParamValue(r, "value")
  regexes := util.GetStringParamValue(r, "regexes")
  paths := util.GetStringParamValue(r, "paths")
  contentType := r.Header.Get("Content-Type")
  if contentType == "" {
    if binary {
      contentType = "application/octet-stream"
    } else {
      contentType = "plain/text"
    }
  }
  if header != "" && uri != "" {
    if err := pr.setResponsePayloadForURIWithHeader(payload, binary, uri, header, value, contentType); err == nil {
      msg = fmt.Sprintf("Port [%s] Payload set for URI [%s] and header [%s : %s] : content-type [%s], length [%d]",
        port, uri, header, value, contentType, len(payload))
    } else {
      msg = fmt.Sprintf("Port [%s] Failed to set payload for URI [%s] and header [%s : %s] : content-type [%s], length [%d] with error [%s]",
        port, uri, header, value, contentType, len(payload), err.Error())
    }
  } else if query != "" && uri != "" {
    if err := pr.setResponsePayloadForURIWithQuery(payload, binary, uri, query, value, contentType); err == nil {
      msg = fmt.Sprintf("Port [%s] Payload set for URI [%s] and query [%s : %s] : content-type [%s], length [%d]",
        port, uri, query, value, contentType, len(payload))
    } else {
      msg = fmt.Sprintf("Port [%s] Failed to set payload for URI [%s] and query [%s : %s] : content-type [%s], length [%d] with error [%s]",
        port, uri, query, value, contentType, len(payload), err.Error())
    }
  } else if uri != "" && (regexes != "" || paths != "") {
    match := regexes
    if match == "" {
      match = paths
    }
    if err := pr.setResponsePayloadForURIWithBodyMatch(payload, binary, uri, match, contentType, paths != ""); err == nil {
      msg = fmt.Sprintf("Port [%s] Payload set for URI [%s] and match [%+v] : content-type [%s], length [%d]",
        port, uri, match, contentType, len(payload))
    } else {
      msg = fmt.Sprintf("Port [%s] Failed to set payload for URI [%s] and match [%+v] : content-type [%s], length [%d] with error [%s]",
        port, uri, match, contentType, len(payload), err.Error())
    }
  } else if uri != "" {
    pr.setURIResponsePayload(payload, binary, uri, contentType, nil)
    msg = fmt.Sprintf("Port [%s] Payload set for URI [%s] : content-type [%s], length [%d]",
      port, uri, contentType, len(payload))
  } else if header != "" {
    pr.setHeaderResponsePayload(payload, binary, header, value, contentType)
    msg = fmt.Sprintf("Port [%s] Payload set for header [%s : %s] : content-type [%s], length [%d]",
      port, header, value, contentType, len(payload))
  } else if query != "" {
    pr.setQueryResponsePayload(payload, binary, query, value, contentType)
    msg = fmt.Sprintf("Port [%s] Payload set for query [%s : %s] : content-type [%s], length [%d]",
      port, query, value, contentType, len(payload))
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
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
  events.SendRequestEvent("Response Payload Configured", msg, r)
}

func setPayloadTransform(w http.ResponseWriter, r *http.Request) {
  msg := ""
  port := util.GetRequestOrListenerPort(r)
  pr := getPortResponse(r)
  contentType := r.Header.Get("Content-Type")
  if contentType == "" {
    contentType = ContentTypeJSON
  }
  var transforms []*util.Transform
  if err := util.ReadJsonPayload(r, &transforms); err == nil {
    uri := util.GetStringParamValue(r, "uri")
    if uri != "" && transforms != nil {
      pr.setURIResponsePayload(nil, false, uri, contentType, transforms)
      msg = fmt.Sprintf("Port [%s] transform paths set for URI [%s] : [%s: %+v]",
        port, uri, contentType, util.ToJSONText(transforms))
      events.SendRequestEvent("Response Payload Configured", msg, r)
    } else {
      msg = "Invalid transformation. Missing URI or payload."
    }
  } else {
    msg = fmt.Sprintf("Invalid transformations: %s", err.Error())
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
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
  size := util.GetSizeParam(r, "payloadSize")
  chunkSize := util.GetSizeParam(r, "chunkSize")
  durMin, durMax, _, _ := util.GetDurationParam(r, "duration")
  delayText := util.GetStringParamValue(r, "delay")
  delayMin, delayMax, _, _ := util.GetDurationParam(r, "delay")
  count := util.GetIntParamValue(r, "count")
  repeat := false
  var payload []byte
  contentType := r.Header.Get("Content-Type")
  if contentType == "" {
    contentType = "plain/text"
  }
  duration := util.RandomDuration(durMin, durMax)
  delay := util.RandomDuration(delayMin, delayMax)
  if delay == 0 {
    delay = 1 * time.Millisecond
  }
  if duration > 0 {
    count = int((duration.Milliseconds() / delay.Milliseconds()))
  }
  if size > 0 {
    repeat = true
    chunkSize = size / count
    payload = util.GenerateRandomPayload(chunkSize)
  } else {
    size = len(payload)
    repeat = size == 0
  }
  if size < chunkSize {
    payload = fixPayload(payload, chunkSize)
  }
  if chunkSize == 0 && count > 0 && size > 0 {
    chunkSize = size/count + 1
  }
  if chunkSize == 0 || count == 0 {
    w.WriteHeader(http.StatusBadRequest)
    util.AddLogMessage("Invalid parameters for streaming or no payload", r)
    fmt.Fprintln(w, "{error: 'Invalid parameters for streaming'}")
    return
  }

  w.Header().Set("Content-Type", contentType)
  w.Header().Set("X-Content-Type-Options", "nosniff")
  w.Header().Set("Goto-Chunk-Count", strconv.Itoa(count))
  w.Header().Set("Goto-Chunk-Length", strconv.Itoa(chunkSize))
  w.Header().Set("Goto-Chunk-Delay", delayText)
  if size > 0 {
    w.Header().Set("Goto-Stream-Length", strconv.Itoa(size))
  }
  if duration > 0 {
    w.Header().Set("Goto-Stream-Duration", duration.String())
  }

  var flusher http.Flusher
  var writer io.Writer
  if f, ok := w.(http.Flusher); ok {
    flusher = f
    if irw, ok := w.(*intercept.InterceptResponseWriter); ok {
      irw.SetChunked()
    }
    writer = w
  }
  if writer == nil && flusher == nil {
    w.WriteHeader(http.StatusInternalServerError)
    fmt.Fprintln(w, "Cannot stream")
    return
  }
  if c := conn.GetConn(r); c != nil {
    c.SetWriteDeadline(time.Time{})
  }
  util.AddLogMessage("Responding with streaming payload", r)
  payloadIndex := 0
  payloadSize := len(payload)
  payloadChunkCount := payloadSize / chunkSize
  if payloadSize%chunkSize > 0 {
    payloadChunkCount++
  }
  for i := 0; i < count; i++ {
    start := payloadIndex * chunkSize
    end := (payloadIndex + 1) * chunkSize
    if end > payloadSize {
      end = payloadSize
    }
    writer.Write(payload[start:end])
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
      delay = util.RandomDuration(delayMin, delayMax, delay)
      time.Sleep(delay)
    }
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

func getFilledPayload(rp *ResponsePayload, r *http.Request, captures map[string]string) []byte {
  vars := mux.Vars(r)
  payload := string(rp.Payload)
  payload = util.SubstitutePayloadMarkers(payload, rp.URICaptureKeys, vars)
  if rp.HeaderCaptureKey != "" {
    if value := r.Header.Get(rp.HeaderMatch); value != "" {
      payload = strings.Replace(payload, rp.HeaderCaptureKey, value, -1)
    }
  }
  if rp.QueryCaptureKey != "" {
    for k, values := range r.URL.Query() {
      if rp.queryMatchRegexp.MatchString(k) && len(values) > 0 {
        payload = strings.Replace(payload, rp.QueryCaptureKey, values[0], -1)
      }
    }
  }
  if len(rp.Transforms) > 0 {
    payload = util.TransformPayload(util.Read(r.Body), rp.Transforms, util.IsYAMLContentType(r.Header))
  }
  for k, v := range captures {
    payload = strings.Replace(payload, util.MarkFiller(k), v, -1)
  }
  return []byte(payload)
}

func getPayloadForBodyMatch(r *http.Request, bodyMatchResponses map[string]*ResponsePayload) (matchedResponsePayload *ResponsePayload, captures map[string]string, matched bool) {
  if len(bodyMatchResponses) == 0 {
    return nil, nil, false
  }
  body := util.Read(r.Body)
  lowerBody := strings.ToLower(body)
  for _, rp := range bodyMatchResponses {
    if rp.bodyMatchRegexp != nil && rp.bodyMatchRegexp.MatchString(lowerBody) {
      matchedResponsePayload = rp
      break
    } else if len(rp.bodyJsonPaths) > 0 {
      allMatched := true
      captures = map[string]string{}
      var data map[string]interface{}
      if err := util.ReadJson(body, &data); err == nil {
        for key, jp := range rp.bodyJsonPaths {
          if matches, err := jp.FindResults(data); err == nil && len(matches) > 0 && len(matches[0]) > 0 {
            if key != "" {
              captures[key] = fmt.Sprintf("%v", matches[0][0].Interface())
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

func (pr *PortResponse) hasAnyPayload() bool {
  return len(pr.allURIResponsePayloads) > 0 || len(pr.ResponsePayloadByHeaders) > 0 ||
    len(pr.ResponsePayloadByQuery) > 0 || pr.DefaultResponsePayload != nil
}

func (pr *PortResponse) unsafeGetResponsePayload(r *http.Request) (responsePayload *ResponsePayload, captures map[string]string, found bool) {
  msg := ""
  for uri, rp := range pr.allURIResponsePayloads {
    if rp.uriRegexp.MatchString(r.RequestURI) {
      msg = fmt.Sprintf("Request URI has response", r.RequestURI)
      if !found && pr.ResponsePayloadByURIAndHeaders[uri] != nil {
        responsePayload, found = getPayloadForKV(r.Header, pr.ResponsePayloadByURIAndHeaders[uri])
        if found {
          msg = fmt.Sprintf("Have custom response for Request URI and Headers")
        }
      }
      if !found && pr.ResponsePayloadByURIAndQuery[uri] != nil {
        responsePayload, found = getPayloadForKV(r.URL.Query(), pr.ResponsePayloadByURIAndQuery[uri])
        if found {
          msg = fmt.Sprintf("Have custom response for Request URI and Query Params")
        }
      }
      if !found && pr.ResponsePayloadByURIAndBody[uri] != nil {
        responsePayload, captures, found = getPayloadForBodyMatch(r, pr.ResponsePayloadByURIAndBody[uri])
        if found {
          msg = fmt.Sprintf("Have custom response for Request URI and Body")
        }
      }
      if !found && pr.ResponsePayloadByURIs[uri] != nil {
        responsePayload = pr.ResponsePayloadByURIs[uri]
        found = true
        msg = fmt.Sprintf("Have custom response for Request URI")
      }
      if found {
        break
      }
    }
  }
  if !found {
    responsePayload, found = getPayloadForKV(r.Header, pr.ResponsePayloadByHeaders)
    if found {
      msg = fmt.Sprintf("Have custom response for Headers")
    }
  }
  if !found {
    responsePayload, found = getPayloadForKV(r.URL.Query(), pr.ResponsePayloadByQuery)
    if found {
      msg = fmt.Sprintf("Have custom response for Request URI and Query Params")
    }
  }
  if !found && pr.DefaultResponsePayload != nil {
    responsePayload = pr.DefaultResponsePayload
    found = true
    msg = fmt.Sprintf("Have default response")
  }
  util.AddLogMessage(msg, r)
  return responsePayload, captures, found
}

func processPayload(w http.ResponseWriter, r *http.Request, rp *ResponsePayload, captures map[string]string) {
  var payload []byte
  contentType := ""
  if !rp.isBinary {
    payload = getFilledPayload(rp, r, captures)
  } else {
    payload = rp.Payload
  }
  contentType = rp.ContentType
  length := strconv.Itoa(len(payload))
  w.Header().Set("Content-Length", length)
  w.Header().Set("Content-Type", contentType)
  w.Header().Set("Goto-Payload-Length", length)
  w.Header().Set("Goto-Payload-Content-Type", contentType)
  msg := fmt.Sprintf("Responding with configured payload of length [%s] and content type [%s] for URI [%s]",
    length, contentType, r.RequestURI)
  util.AddLogMessage(msg, r)
  if n, err := w.Write(payload); err != nil {
    msg = fmt.Sprintf("Failed to write payload of length [%s] with error: %s", length, err.Error())
  } else {
    msg = fmt.Sprintf("Written payload of length [%d] compared to configured size [%s]", n, length)
  }
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
    pr := getPortResponse(r)
    if !util.IsPayloadRequest(r) && pr.hasAnyPayload() {
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
