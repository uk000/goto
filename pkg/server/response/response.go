package response

import (
  "net/http"

  "goto/pkg/server/response/delay"
  "goto/pkg/server/response/header"
  "goto/pkg/server/response/payload"
  "goto/pkg/server/response/status"
  "goto/pkg/server/response/trigger"
  "goto/pkg/util"

  "github.com/gorilla/mux"
)

var (
  Handler          util.ServerHandler   = util.ServerHandler{"response", SetRoutes, Middleware}
  responseHandlers []util.ServerHandler = []util.ServerHandler{
    status.Handler, delay.Handler, header.Handler, payload.Handler, trigger.Handler}
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  util.AddRoutes(util.PathRouter(r, "/response"), r, root, responseHandlers...)
}

func Middleware(next http.Handler) http.Handler {
  return util.AddMiddlewares(next, responseHandlers...)
}
