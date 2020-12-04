package echo

import (
	"fmt"
	"io"
	"io/ioutil"
	"net/http"

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
  util.AddRoute(echoRouter, "/ws", wsEchoHandler, "GET", "POST")
  util.AddRoute(echoRouter, "", Echo)
}

func EchoHeaders(w http.ResponseWriter, r *http.Request) {
  util.AddLogMessage("Echoing headers back", r)
  util.CopyHeaders("Request", w, r.Header, r.Host, r.RequestURI)
  fmt.Fprintf(w, "{\"RequestHeaders\": %s}", util.GetRequestHeadersLog(r))
}

func Echo(w http.ResponseWriter, r *http.Request) {
  util.AddLogMessage("Echoing back", r)
  response := map[string]interface{}{}
  response["RequestProtocol"] = r.Proto
  response["RequestURI"] = r.RequestURI
  response["RequestHeaders"] = r.Header
  response["RequestQuery"] = r.URL.Query()
  body, _ := ioutil.ReadAll(r.Body)
  response["RequestBody"] = string(body)
  fmt.Fprintln(w, util.ToJSON(response))
}

func wsEchoHandler(w http.ResponseWriter, r *http.Request) {
  headers := util.GetRequestHeadersLog(r)
  s := websocket.Server{Handler: websocket.Handler(func(ws *websocket.Conn){
    ws.Write([]byte(headers))
    io.Copy(ws, ws)
  })}
  s.ServeHTTP(w, r)
}
