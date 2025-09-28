package router

import (
	"fmt"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net/http"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("routing", setRoutes, nil)
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	routingRouter := util.PathRouter(r, "/routing")
	util.AddRouteWithPort(routingRouter, "/add", addRoute, "POST")
	util.AddRouteWithPort(routingRouter, "/clear", clearRoutes, "POST")
	util.AddRouteWithPort(routingRouter, "", getRoutes, "GET")
}

func addRoute(w http.ResponseWriter, r *http.Request) {
	route := &Route{}
	err := util.ReadJsonPayload(r, route)
	msg := ""
	if err != nil {
		msg = fmt.Sprintf("Failed to parse routing payload with error [%s]", err.Error())
	} else if !route.IsValid() {
		msg = fmt.Sprintf("Invalid route: [%+v]", route)
	} else {
		pr := GetPortRouter(route.From.Port)
		pr.AddRoute(route)
		msg = fmt.Sprintf("Route added [%s]", route.Label)
		fmt.Fprintln(w, util.ToJSONText(route))
	}
	util.AddLogMessage(msg, r)
}

func clearRoutes(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	pr := GetPortRouter(port)
	pr.Clear()
	msg := fmt.Sprintf("Routes cleared on port [%d]", port)
	fmt.Fprintln(w, msg)
	util.AddLogMessage(msg, r)
}

func getRoutes(w http.ResponseWriter, r *http.Request) {
	port := util.GetRequestOrListenerPortNum(r)
	pr := GetPortRouter(port)
	util.WriteJsonPayload(w, pr)
	msg := fmt.Sprintf("Routes reported on port [%d]", port)
	util.AddLogMessage(msg, r)
}
