package payload

import (
	"fmt"
	"goto/pkg/util"
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
  util.AddRoute(parent, "/stream/size/{size}/duration/{duration}", streamResponse, "GET", "PUT", "POST")
  util.AddRoute(parent, "/stream/size/{size}/chunk/{chunk}/delay/{delay}", streamResponse, "GET", "PUT", "POST")
  util.AddRoute(parent, "/stream/size/{size}/chunk/{chunk}", streamResponse, "GET", "PUT", "POST")
}

func (pr *PortResponse) init() {
  pr.lock.Lock()
  defer pr.lock.Unlock()
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

func streamResponse(w http.ResponseWriter, r *http.Request) {
  size := util.GetSizeParam(r, "size")
  chunk := util.GetSizeParam(r, "chunk")
  duration := util.GetDurationParam(r, "duration")
  delay := util.GetDurationParam(r, "delay")
  if delay == 0 {
    count := 10
    if chunk > 0 {
      count = size / chunk
    }
    if duration > 0 {
      delay = time.Duration(int(duration.Milliseconds())/count) * time.Millisecond
    } else {
      delay = 100 * time.Millisecond
    }
  }
  if chunk == 0 {
    chunk = size / int(duration.Truncate(time.Second).Seconds())
  }
  chunkCount := size / chunk
  pr := getPortResponse(r)
  pr.lock.RLock()
  payload := pr.DefaultResponsePayload
  pr.lock.RUnlock()
  if size > 0 {
    payload = fixPayload(payload, size)
  }
  contentType := "plain/text"
  if pr.ResponseContentType != "" {
    contentType = pr.ResponseContentType
  }
  if flusher, ok := w.(http.Flusher); ok {
    util.AddLogMessage("Responding with streaming payload", r)
    w.Header().Set("Content-Type", contentType)
    w.Header().Set("X-Content-Type-Options", "nosniff")
    w.Header().Set("Goto-Stream-Length", strconv.Itoa(size))
    w.Header().Set("Goto-Chunk-Length", strconv.Itoa(chunk))
    w.Header().Set("Goto-Chunk-Delay", delay.String())
    if duration > 0 {
      w.Header().Set("Goto-Stream-Duration", duration.String())
    }
    for i := 0; i < chunkCount; i++ {
      fmt.Fprint(w, string(payload[i*chunk:(i+1)*chunk]))
      flusher.Flush()
      if i < chunk-1 {
        time.Sleep(delay)
      }
    }
    fmt.Fprintln(w)
  } else {
    w.WriteHeader(http.StatusInternalServerError)
    fmt.Fprintln(w, "Cannot stream")
  }
}

func Middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    responseSet := false
    payload := ""
    pr := getPortResponse(r)
    if !util.IsAdminRequest(r) && !util.IsStreamRequest(r) {
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
