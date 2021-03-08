package catchall

import (
  "io/ioutil"
  "net/http"

  "goto/pkg/metrics"
  "goto/pkg/util"

  "github.com/gorilla/mux"
)

var (
  Handler util.ServerHandler = util.ServerHandler{Name: "catchall", SetRoutes: SetRoutes}
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  r.MatcherFunc(func(r *http.Request, rm *mux.RouteMatch) bool { return true }).HandlerFunc(respond)
}

func respond(w http.ResponseWriter, r *http.Request) {
  metrics.UpdateRequestCount("catchAll")
  util.AddLogMessage("CatchAll", r)
  SendDefaultResponse(w, r)
}

func SendDefaultResponse(w http.ResponseWriter, r *http.Request) {
  response := map[string]interface{}{}
  response["CatchAll"] = true
  response["RequestProtocol"] = r.Proto
  response["RequestURI"] = r.RequestURI
  response["RequestHeaders"] = r.Header
  response["RequestQuery"] = r.URL.Query()
  if !util.IsH2C(r) {
    body, _ := ioutil.ReadAll(r.Body)
    response["RequestBody"] = string(body)
  }
  util.WriteJsonPayload(w, response)
}
