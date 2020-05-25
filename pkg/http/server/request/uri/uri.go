package uri

import (
	"fmt"
	"net/http"

	"goto/pkg/http/server/request/uri/bypass"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
	Handler         util.ServerHandler = util.ServerHandler{"uri", SetRoutes, Middleware}
	internalHandler util.ServerHandler = util.ServerHandler{Name: "uri", Middleware: middleware}
)

func SetRoutes(r *mux.Router, parent *mux.Router) {
	uriRouter := r.PathPrefix("/uri").Subrouter()
	bypass.SetRoutes(uriRouter, parent)
}

func middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		util.AddLogMessage(fmt.Sprintf("Request URI: [%s], Method: [%s]", r.RequestURI, r.Method), r)
		next.ServeHTTP(w, r)
	})
}

func Middleware(next http.Handler) http.Handler {
	return util.AddMiddlewares(next, internalHandler, bypass.Handler)
}
