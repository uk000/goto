package info

import (
	"goto/pkg/global"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

var (
	Middleware = middleware.NewMiddleware("info", setRoutes, nil)
	apis       = map[string]map[string]map[string][]string{}
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	infoRouter := util.PathRouter(r, "/")

	util.AddRouteWithPort(infoRouter, "/version", showVersion, "GET")
	util.AddRouteWithPort(infoRouter, "/{k:routes|apis}", showApis, "GET")
	util.AddRouteWithPort(infoRouter, "/{k:routes|apis}/port", showApis, "GET")
}

func showVersion(w http.ResponseWriter, r *http.Request) {
	util.WriteJsonPayload(w, map[string]string{"version": global.Version, "commit": global.Commit})
}

func showApis(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.RequestURI, "port") {
		PrintRoutes(w, util.PortRouter)
	} else {
		PrintRoutes(w, util.RootRouter)
	}
}

func PrintRoutes(w http.ResponseWriter, r *mux.Router) {
	if len(apis) == 0 {
		r.Walk(func(route *mux.Route, router *mux.Router, ancestors []*mux.Route) error {
			methods, _ := route.GetMethods()
			if len(methods) == 0 {
				return nil
			}
			path, _ := route.GetPathTemplate()
			pieces := strings.Split(path, "/")
			if len(pieces) < 2 || pieces[1] == "" {
				return nil
			}
			root := ""
			index := 1
			root = "/" + pieces[index]
			index++
			if apis[root] == nil {
				apis[root] = map[string]map[string][]string{}
			}
			subroot := ""
			if len(pieces) > index+1 {
				subroot = root + "/" + pieces[index]
				index++
			}
			if apis[root][subroot] == nil {
				apis[root][subroot] = map[string][]string{}
			}
			subpath := ""
			if len(pieces) > index+1 {
				subpath = subroot + "/" + strings.Join(pieces[index:], "/")
				index++
			}
			if len(methods) > 0 && route.GetHandler() != nil {
				apis[root][subroot][subpath] = methods
				// log.Printf("%s -> %+v", subpath, route.GetHandler())
			}
			return nil
		})
	}
	util.WriteJsonPayload(w, apis)
}
