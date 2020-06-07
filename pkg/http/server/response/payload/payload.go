package payload

import (
	"fmt"
	"goto/pkg/util"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/mux"
)

type PortResponse struct {
  ResponseContentType      string
  DefaultResponsePayload   string
  ResponsePayloadByURIs    map[string]interface{}
  ResponsePayloadByHeaders map[string]map[string]string
  lock                     sync.RWMutex
}

var (
  Handler       util.ServerHandler       = util.ServerHandler{"response.payload", SetRoutes, Middleware}
  portResponses map[string]*PortResponse = map[string]*PortResponse{}
  responseLock  sync.RWMutex
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  payloadRouter := r.PathPrefix("/payload").Subrouter()
  util.AddRoute(payloadRouter, "/set/default", setResponsePayload, "POST")
  util.AddRouteQ(payloadRouter, "/set/uri", setResponsePayload, "uri", "{uri}", "POST")
  util.AddRoute(payloadRouter, "/set/header/{header}", setResponsePayload, "POST")
  util.AddRoute(payloadRouter, "/set/header/{header}/value/{value}", setResponsePayload, "POST")
  util.AddRoute(payloadRouter, "/clear", clearResponsePayload, "POST")

  util.AddRoute(payloadRouter, "", getResponsePayload, "GET")
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

func (pr *PortResponse) setDefaultResponsePayload(payload string) {
  pr.lock.Lock()
  defer pr.lock.Unlock()
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
  getPortResponse(r).setResponseContentType(r.Header.Get("Content-Type"))
  if header, present := util.GetStringParam(r, "header"); present {
    value, _ := util.GetStringParam(r, "value")
    getPortResponse(r).setHeaderResponsePayload(header, value, payload)
    msg = fmt.Sprintf("Payload set for Response header [%s : %s] : %s", header, value, payload)
  } else if uri, present := util.GetStringParam(r, "uri"); present {
    getPortResponse(r).setURIResponsePayload(uri, payload)
    msg = fmt.Sprintf("Payload set for Response URI [%s] : %s", uri, payload)
  } else {
    getPortResponse(r).setDefaultResponsePayload(payload)
    msg = fmt.Sprintf("Default Payload set : %s", payload)
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

func Middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    responseSet := false
    pr := getPortResponse(r)
    if !util.IsAdminRequest(r) {
      pr.lock.RLock()
      defer pr.lock.RUnlock()
      if uri := util.FindURIInMap(r.RequestURI, pr.ResponsePayloadByURIs); uri != "" {
        fmt.Fprintln(w, pr.ResponsePayloadByURIs[uri].(string))
        responseSet = true
      } else {
        for h, hv := range r.Header {
          h = strings.ToLower(h)
          if pr.ResponsePayloadByHeaders[h] != nil {
            for _, v := range hv {
              v = strings.ToLower(v)
              if pr.ResponsePayloadByHeaders[h][v] != "" {
                fmt.Fprintln(w, pr.ResponsePayloadByHeaders[h][v])
                responseSet = true
                break
              }
            }
            if !responseSet && pr.ResponsePayloadByHeaders[h][""] != "" {
              fmt.Fprintln(w, pr.ResponsePayloadByHeaders[h][""])
              responseSet = true
              break
            }
          }
        }
      }
      if !responseSet && pr.DefaultResponsePayload != "" {
        fmt.Fprintln(w, pr.DefaultResponsePayload)
        responseSet = true
      }
      if responseSet {
        w.Header().Set("Content-Type", pr.ResponseContentType)
      }
    }
    if !responseSet {
      next.ServeHTTP(w, r)
    }
  })
}
