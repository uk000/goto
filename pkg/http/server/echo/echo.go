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

func SetRoutes(r *mux.Router, parent *mux.Router) {
  echoRouter := r.PathPrefix("/echo").Subrouter()
  util.AddRoute(echoRouter, "/headers", echoHeaders)
  util.AddRoute(echoRouter, "", echo)
}

func echoHeaders(w http.ResponseWriter, r *http.Request) {
  util.AddLogMessage("Echoing headers back", r)
  util.CopyHeaders(w, r.Header, r.Host)
  w.WriteHeader(http.StatusOK)
  fmt.Fprintf(w, "%s", util.GetRequestHeadersLog(r))
}

func echo(w http.ResponseWriter, r *http.Request) {
  util.AddLogMessage("Echoing back", r)
  util.CopyHeaders(w, r.Header, r.Host)
  w.WriteHeader(http.StatusOK)
  fmt.Fprintln(w, util.GetRequestHeadersLog(r))
  body, _ := ioutil.ReadAll(r.Body)
  fmt.Fprintln(w, "Request Body:")
  fmt.Fprintln(w, string(body))
}
