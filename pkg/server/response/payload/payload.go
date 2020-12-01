package payload

import (
	"fmt"
	"goto/pkg/server/intercept"
	"goto/pkg/util"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
)

type PortResponse struct {
  ResponseContentType      string                       `json:"responseContentType"`
  DefaultResponsePayload   string                       `json:"defaultResponsePayload"`
  ResponsePayloadByURIs    map[string]interface{}       `json:"responsePayloadByURIs"`
  ResponsePayloadByHeaders map[string]map[string]string `json:"responsePayloadByHeaders"`
  lock                     sync.RWMutex
}

var (
  Handler       util.ServerHandler       = util.ServerHandler{"response.payload", SetRoutes, Middleware}
  portResponses map[string]*PortResponse = map[string]*PortResponse{}
  responseLock  sync.RWMutex
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  payloadRouter := r.PathPrefix("/payload").Subrouter()
  util.AddRoute(payloadRouter, "/set/default/{size}", setResponsePayload, "POST")
  util.AddRoute(payloadRouter, "/set/default", setResponsePayload, "POST")
  util.AddRouteQ(payloadRouter, "/set/uri", setResponsePayload, "uri", "{uri}", "POST")
  util.AddRoute(payloadRouter, "/set/header/{header}/value/{value}", setResponsePayload, "POST")
  util.AddRoute(payloadRouter, "/set/header/{header}", setResponsePayload, "POST")
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
  pr.ResponseContentType = ""
  pr.DefaultResponsePayload = ""
  pr.ResponsePayloadByURIs = map[string]interface{}{}
  pr.ResponsePayloadByHeaders = map[string]map[string]string{}
}

func (pr *PortResponse) setResponseContentType(contentType string) {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  if contentType != "" {
    pr.ResponseContentType = contentType
  } else {
    pr.ResponseContentType = "application/json"
  }
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

func (pr *PortResponse) setDefaultResponsePayload(payload string, size int) {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  if size > 0 {
    payload = fixPayload(payload, size)
  }
  pr.DefaultResponsePayload = payload
}

func (pr *PortResponse) setURIResponsePayload(uri string, payload string) {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  uri = strings.ToLower(uri)
  if payload != "" {
    pr.ResponsePayloadByURIs[uri] = payload
  } else if pr.ResponsePayloadByURIs[uri].(string) != "" {
    delete(pr.ResponsePayloadByURIs, uri)
  }
}

func (pr *PortResponse) setHeaderResponsePayload(header string, value string, payload string) {
  pr.lock.Lock()
  defer pr.lock.Unlock()
  header = strings.ToLower(header)
  value = strings.ToLower(value)
  if payload != "" {
    if pr.ResponsePayloadByHeaders[header] == nil {
      pr.ResponsePayloadByHeaders[header] = map[string]string{}
    }
    pr.ResponsePayloadByHeaders[header][value] = payload
  } else if pr.ResponsePayloadByHeaders[header] != nil {
    if pr.ResponsePayloadByHeaders[header][value] != "" {
      delete(pr.ResponsePayloadByHeaders[header], value)
    }
    if len(pr.ResponsePayloadByHeaders[header]) == 0 {
      delete(pr.ResponsePayloadByHeaders, header)
    }
  }
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
  pr.setResponseContentType(r.Header.Get("Content-Type"))
  if header, present := util.GetStringParam(r, "header"); present {
    value, _ := util.GetStringParam(r, "value")
    pr.setHeaderResponsePayload(header, value, payload)
    msg = fmt.Sprintf("Payload set for Response header [%s : %s] : %s", header, value, payload)
  } else if uri, present := util.GetStringParam(r, "uri"); present {
    pr.setURIResponsePayload(uri, payload)
    msg = fmt.Sprintf("Payload set for Response URI [%s] : %s", uri, payload)
  } else {
    size := util.GetSizeParam(r, "size")
    pr.setDefaultResponsePayload(payload, size)
    if size > 0 {
      msg = fmt.Sprintf("Default Payload set with size: %d", size)
    } else {
      msg = fmt.Sprintf("Default Payload set : %s", pr.DefaultResponsePayload)
    }
  }
  w.WriteHeader(http.StatusAccepted)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func clearResponsePayload(w http.ResponseWriter, r *http.Request) {
  getPortResponse(r).init()
  msg := "Response payload cleared"
  w.WriteHeader(http.StatusAccepted)
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func getResponsePayload(w http.ResponseWriter, r *http.Request) {
  w.WriteHeader(http.StatusOK)
  fmt.Fprintln(w, util.ToJSON(getPortResponse(r)))
}

func respondWithPayload(w http.ResponseWriter, r *http.Request) {
  size := util.GetSizeParam(r, "size")
  payload := util.GenerateRandomString(size)
  fmt.Fprint(w, payload)
  w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
  w.Header().Set("Content-Type", "plain/text")
  w.Header().Set("Goto-Payload-Length", strconv.Itoa(size))
  util.AddLogMessage(fmt.Sprintf("Responding with requested payload of length %d", size), r)
}

func streamResponse(w http.ResponseWriter, r *http.Request) {
  size := util.GetSizeParam(r, "size")
  chunk := util.GetSizeParam(r, "chunk")
  duration := util.GetDurationParam(r, "duration")
  delay := util.GetDurationParam(r, "delay")
  count := util.GetIntParamValue(r, "count")
  repeat := false

  pr := getPortResponse(r)
  pr.lock.RLock()
  payload := pr.DefaultResponsePayload
  pr.lock.RUnlock()

  if duration > 0 {
    count = int((duration.Milliseconds()/delay.Milliseconds()))
  }
  if size > 0 {
    repeat = true
    chunk = size/count
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

  contentType := "plain/text"
  if pr.ResponseContentType != "" {
    contentType = pr.ResponseContentType
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
  payloadChunkCount := payloadSize/chunk
  if payloadSize%chunk > 0 {
    payloadChunkCount++
  }
  for i := 0; i < count; i++ {
    start := payloadIndex*chunk
    end := (payloadIndex+1)*chunk
    if end > payloadSize {
      end = payloadSize
    }
    chunkResponse := string(payload[start : end])
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

func Middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    responseSet := false
    payload := ""
    pr := getPortResponse(r)
    if !util.IsAdminRequest(r) && !util.IsPayloadRequest(r) {
      pr.lock.RLock()
      defer pr.lock.RUnlock()
      if uri := util.FindURIInMap(r.RequestURI, pr.ResponsePayloadByURIs); uri != "" {
        payload = pr.ResponsePayloadByURIs[uri].(string)
        responseSet = true
      } else {
        for h, hv := range r.Header {
          h = strings.ToLower(h)
          if pr.ResponsePayloadByHeaders[h] != nil {
            for _, v := range hv {
              v = strings.ToLower(v)
              if pr.ResponsePayloadByHeaders[h][v] != "" {
                payload = pr.ResponsePayloadByHeaders[h][v]
                responseSet = true
                break
              }
            }
            if !responseSet && pr.ResponsePayloadByHeaders[h][""] != "" {
              payload = pr.ResponsePayloadByHeaders[h][""]
              responseSet = true
              break
            }
          }
        }
      }
      if !responseSet && pr.DefaultResponsePayload != "" {
        payload = pr.DefaultResponsePayload
        responseSet = true
      }
      if responseSet {
        fmt.Fprint(w, payload)
        w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
        w.Header().Set("Content-Type", pr.ResponseContentType)
        util.AddLogMessage("Responding with configured payload", r)
      }
    }
    if !responseSet || util.IsStatusRequest(r) || util.IsDelayRequest(r) {
      next.ServeHTTP(w, r)
    }
  })
}
