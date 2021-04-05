package catchall

import (
  "net/http"

  "goto/pkg/metrics"
  "goto/pkg/server/echo"
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
  response := echo.GetEchoResponse(w, r)
  response["CatchAll"] = true
  util.WriteJsonPayload(w, response)
}
