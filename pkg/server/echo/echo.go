package echo

import (
  "fmt"
  "io"
  "net/http"

  "goto/pkg/metrics"
  "goto/pkg/util"

  "github.com/gorilla/mux"
  "golang.org/x/net/websocket"
)

var (
  Handler util.ServerHandler = util.ServerHandler{Name: "echo", SetRoutes: SetRoutes}
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  echoRouter := r.PathPrefix("/echo").Subrouter()
  util.AddRoute(echoRouter, "/headers", EchoHeaders, "GET", "PUT", "POST", "OPTIONS", "HEAD", "DELETE")
  util.AddRoute(echoRouter, "/body", echoBody, "GET", "PUT", "POST", "OPTIONS", "HEAD", "DELETE")
  util.AddRoute(echoRouter, "/ws", wsEchoHandler, "GET", "PUT", "POST", "OPTIONS", "HEAD", "DELETE")
  util.AddRoute(echoRouter, "", echo, "GET", "PUT", "POST", "OPTIONS", "HEAD", "DELETE")
}

func EchoHeaders(w http.ResponseWriter, r *http.Request) {
  util.AddLogMessage("Echoing headers back", r)
  util.WriteJsonPayload(w, map[string]interface{}{"RequestHeaders": r.Header})
}

func echoBody(w http.ResponseWriter, r *http.Request) {
  metrics.UpdateRequestCount("echo")
  util.AddLogMessage("Echoing Body", r)
  io.Copy(w, r.Body)
}

func echo(w http.ResponseWriter, r *http.Request) {
  metrics.UpdateRequestCount("echo")
  Echo(w, r)
  fmt.Fprintln(w)
}

func Echo(w http.ResponseWriter, r *http.Request) {
  util.AddLogMessage("Echoing back", r)
  response := map[string]interface{}{}
  response["RequestProtocol"] = r.Proto
  response["RequestURI"] = r.RequestURI
  response["RequestHeaders"] = r.Header
  response["RequestQuery"] = r.URL.Query()
  r.Body.Close()
  util.WriteJsonPayload(w, response)
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
