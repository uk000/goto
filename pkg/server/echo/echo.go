package echo

import (
  "fmt"
  "io"
  "net/http"

  . "goto/pkg/constants"
  "goto/pkg/metrics"
  "goto/pkg/server/intercept"
  "goto/pkg/server/listeners"
  "goto/pkg/util"

  "github.com/gorilla/mux"
  "golang.org/x/net/websocket"
)

var (
  Handler util.ServerHandler = util.ServerHandler{Name: "echo", SetRoutes: SetRoutes}
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  echoRouter := r.PathPrefix("/echo").Subrouter()
  util.AddRoute(echoRouter, "/headers", EchoHeaders)
  util.AddRoute(echoRouter, "/body", echoBody)
  util.AddRoute(echoRouter, "/ws", wsEchoHandler, "GET", "POST", "PUT")
  util.AddRoute(echoRouter, "/stream", echoStream, "POST", "PUT")
  util.AddRoute(echoRouter, "", echo)
}

func EchoHeaders(w http.ResponseWriter, r *http.Request) {
  metrics.UpdateRequestCount("echo")
  util.AddLogMessage("Echoing headers", r)
  util.WriteJsonPayload(w, map[string]interface{}{"RequestHeaders": r.Header})
}

func echoBody(w http.ResponseWriter, r *http.Request) {
  metrics.UpdateRequestCount("echo")
  util.AddLogMessage("Echoing Body", r)
  io.Copy(w, r.Body)
}

func echo(w http.ResponseWriter, r *http.Request) {
  metrics.UpdateRequestCount("echo")
  util.AddLogMessage("Echoing", r)
  Echo(w, r)
}

func echoStream(w http.ResponseWriter, r *http.Request) {
  metrics.UpdateRequestCount("echo")
  util.AddLogMessage("Streaming Echo", r)
  var writer io.Writer = w
  if util.IsH2(r) {
    fw := intercept.NewFlushWriter(r, w)
    util.CopyHeaders("Request", w, r.Header, r.Host, r.RequestURI, false)
    util.SetHeadersSent(r, true)
    fw.Flush()
    writer = fw
  }
  if _, err := io.Copy(writer, r.Body); err != nil {
    fmt.Println(err.Error())
  }
}

func wsEchoHandler(w http.ResponseWriter, r *http.Request) {
  metrics.UpdateRequestCount("wsecho")
  headers := util.GetRequestHeadersLog(r)
  s := websocket.Server{Handler: websocket.Handler(func(ws *websocket.Conn) {
    ws.Write([]byte(headers))
    io.Copy(ws, ws)
  })}
  s.ServeHTTP(w, r)
}

func Echo(w http.ResponseWriter, r *http.Request) {
  util.WriteJsonPayload(w, GetEchoResponse(w, r))
}

func GetEchoResponse(w http.ResponseWriter, r *http.Request) map[string]interface{} {
  l := listeners.GetCurrentListener(r)
  response := map[string]interface{}{
    "RemoteAddress":      r.RemoteAddr,
    "RequestHost":        r.Host,
    "RequestURI":         r.RequestURI,
    "RequestMethod":      r.Method,
    "RequestProtcol":     r.Proto,
    "RequestHeaders":     r.Header,
    "RequestQuery":       r.URL.Query(),
    "RequestBody":        fmt.Sprintf("[%d bytes]", util.DiscardRequestBody(r)),
    HeaderGotoTargetURL:  r.Header.Get(HeaderGotoTargetURL),
    HeaderGotoHost:       l.HostLabel,
    HeaderGotoPort:       l.Port,
    HeaderViaGoto:        l.Label,
    HeaderGotoProtocol:   w.Header().Get(HeaderGotoProtocol),
    HeaderGotoHostTunnel: r.Header.Get(HeaderGotoHostTunnel),
    HeaderViaGotoTunnel:  r.Header.Get(HeaderViaGotoTunnel),
  }
  if !util.IsH2C(r) {
    response["RequestBody"] = fmt.Sprintf("[%d bytes]", util.DiscardRequestBody(r))
  }
  return response
}
