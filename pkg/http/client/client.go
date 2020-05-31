package client

import (
	"goto/pkg/http/client/target"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
  Handler        util.ServerHandler   = util.ServerHandler{Name: "client", SetRoutes: SetRoutes}
  clientHandlers []util.ServerHandler = []util.ServerHandler{target.Handler}
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  clientRouter := r.PathPrefix("/client").Subrouter()
  util.AddRoutes(clientRouter, r, root, clientHandlers...)
}
