package response

import (
	"net/http"

	"goto/pkg/http/server/response/delay"
	"goto/pkg/http/server/response/header"
	"goto/pkg/http/server/response/status"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
  Handler          util.ServerHandler   = util.ServerHandler{"response", SetRoutes, Middleware}
  responseHandlers []util.ServerHandler = []util.ServerHandler{
    header.Handler, delay.Handler, status.Handler}
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  responseRouter := r.PathPrefix("/response").Subrouter()
  util.AddRoutes(responseRouter, r, root, responseHandlers...)
}

func Middleware(next http.Handler) http.Handler {
  return util.AddMiddlewares(next, responseHandlers...)
}
