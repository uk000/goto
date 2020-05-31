package request

import (
	"net/http"

	"goto/pkg/http/server/request/header"
	"goto/pkg/http/server/request/proxy"
	"goto/pkg/http/server/request/timeout"
	"goto/pkg/http/server/request/uri"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
  Handler         util.ServerHandler   = util.ServerHandler{"request", SetRoutes, Middleware}
  requestHandlers []util.ServerHandler = []util.ServerHandler{uri.Handler, timeout.Handler, header.Handler,
    proxy.Handler}
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  requestRouter := r.PathPrefix("/request").Subrouter()
  util.AddRoutes(requestRouter, r, root, requestHandlers...)
}

func Middleware(next http.Handler) http.Handler {
  return util.AddMiddlewares(next, requestHandlers...)
}
