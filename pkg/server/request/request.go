package request

import (
  "net/http"

  "goto/pkg/server/request/body"
  "goto/pkg/server/request/header"
  "goto/pkg/server/request/proxy"
  "goto/pkg/server/request/timeout"
  "goto/pkg/server/request/uri"
  "goto/pkg/util"

  "github.com/gorilla/mux"
)

var (
  Handler         util.ServerHandler   = util.ServerHandler{"request", SetRoutes, Middleware}
  requestHandlers []util.ServerHandler = []util.ServerHandler{header.Handler, uri.Handler,
    body.Handler, timeout.Handler, proxy.Handler}
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  requestRouter := r.PathPrefix("/request").Subrouter()
  util.AddRoutes(requestRouter, r, root, requestHandlers...)
}

func Middleware(next http.Handler) http.Handler {
  return util.AddMiddlewares(next, requestHandlers...)
}
