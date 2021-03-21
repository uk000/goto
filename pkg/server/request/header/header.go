package header

import (
  "fmt"
  "net/http"

  "goto/pkg/global"
  "goto/pkg/server/request/header/tracking"
  "goto/pkg/util"

  "github.com/gorilla/mux"
)

var (
  Handler util.ServerHandler = util.ServerHandler{"header", SetRoutes, Middleware}
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  tracking.SetRoutes(util.PathRouter(r, "/headers"), r, root)
}

func Middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    util.AddLogMessage(fmt.Sprintf("Request URI: [%s], Protocol: [%s], Method: [%s]", r.RequestURI, r.Proto, r.Method), r)
    if global.LogRequestHeaders {
      util.AddLogMessage("Request Headers: "+util.GetRequestHeadersLog(r), r)
    }
    tracking.Middleware(next).ServeHTTP(w, r)
  })
}
