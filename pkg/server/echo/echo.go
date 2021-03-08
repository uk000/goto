package echo

import (
  "fmt"
  "io"
  "io/ioutil"
  "net/http"

  "goto/pkg/metrics"
  "goto/pkg/server/intercept"
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
  util.AddRoute(echoRouter, "/ws", wsEchoHandler, "GET", "POST", "PUT")
  util.AddRoute(echoRouter, "/stream", echoStream, "POST", "PUT")
  util.AddRoute(echoRouter, "", echo)
}

func EchoHeaders(w http.ResponseWriter, r *http.Request) {
  util.AddLogMessage("Echoing headers", r)
  fmt.Fprintf(w, "{\"EchoHeaders\": %s}", util.GetRequestHeadersLog(r))
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
    util.CopyHeaders("Stream", w, r.Header, r.Host, r.RequestURI)
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
  if util.IsPutOrPost(r) {
    body, _ := ioutil.ReadAll(r.Body)
    fmt.Fprint(w, string(body))
  } else {
    fmt.Fprintf(w, "{\"EchoHeaders\": %s}", util.GetRequestHeadersLog(r))
  }
}
