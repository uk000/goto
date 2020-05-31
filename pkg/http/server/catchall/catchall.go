package catchall

import (
	"fmt"
	"net/http"

	"goto/pkg/http/server/echo"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
  Handler util.ServerHandler = util.ServerHandler{Name: "status", SetRoutes: SetRoutes}
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  r.MatcherFunc(func(r *http.Request, rm *mux.RouteMatch) bool { return true }).HandlerFunc(respond)
}

func respond(w http.ResponseWriter, r *http.Request) {
  w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "CatchAll")
	echo.Echo(w, r)
}
