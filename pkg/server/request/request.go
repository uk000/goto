package request

import (
  "net/http"

  "goto/pkg/server/request/body"
  "goto/pkg/server/request/filter"
  "goto/pkg/server/request/header"
  "goto/pkg/server/request/timeout"
  "goto/pkg/server/request/uri"
  "goto/pkg/util"

  "github.com/gorilla/mux"
)

var (
  Handler         util.ServerHandler   = util.ServerHandler{"request", SetRoutes, Middleware}
  requestHandlers []util.ServerHandler = []util.ServerHandler{header.Handler, body.Handler, timeout.Handler, uri.Handler, filter.Handler}
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  util.AddRoutes(util.PathRouter(r, "/request"), r, root, requestHandlers...)
}

func Middleware(next http.Handler) http.Handler {
  return util.AddMiddlewares(next, requestHandlers...)
}
