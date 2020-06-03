package echo

import (
	"fmt"
	"io/ioutil"
	"net/http"

	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
  Handler util.ServerHandler = util.ServerHandler{Name: "echo", SetRoutes: SetRoutes}
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  echoRouter := r.PathPrefix("/echo").Subrouter()
  util.AddRoute(echoRouter, "/headers", EchoHeaders)
  util.AddRoute(echoRouter, "", Echo)
}

func EchoHeaders(w http.ResponseWriter, r *http.Request) {
  util.AddLogMessage("Echoing headers back", r)
  util.CopyHeaders(w, r.Header, r.Host)
  w.WriteHeader(http.StatusOK)
  fmt.Fprintf(w, "{\"RequestHeaders\": %s}", util.GetRequestHeadersLog(r))
}

func Echo(w http.ResponseWriter, r *http.Request) {
  util.AddLogMessage("Echoing back", r)
  util.CopyHeaders(w, r.Header, r.Host)
  w.WriteHeader(http.StatusOK)
  response := map[string]interface{}{}
  response["RequestURI"] = r.RequestURI
  response["RequestHeaders"] = r.Header
  response["RequestQuery"] = r.URL.Query()
  body, _ := ioutil.ReadAll(r.Body)
  response["RequestBody"] = string(body)
  fmt.Fprintln(w, util.ToJSON(response))
}
