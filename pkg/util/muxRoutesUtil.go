/**
 * Copyright 2026 uk
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package util

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
)

var (
	CoreRouter *mux.Router
	RootRouter *mux.Router
	PortRouter *mux.Router
	// portTunnelRouters     = map[string]*mux.Router{}
	coRoutersMap          = map[*mux.Router][]*mux.Router{}
	MatchRouter           *mux.Router
	RoutePrefixRegexp     string
	PortRouteRegexp       = regexp.MustCompile(`(?i)(?:^/port=([^/]+))?`)
	RootURIRegexp         = regexp.MustCompile(`^(/[^/?]+)(.*)`)
	optionalPathRegexp    = regexp.MustCompile(`(\/[^{}]+?\?)`)
	optionalPathKeyRegexp = regexp.MustCompile(`(\/(?:[^\/{}]+=)?{[^{}]+?}\?\??)`)
)

// CreateRouters sets up the port, root and match routers on the core router, and returns the root router.
func CreateRouters(coreRouter *mux.Router) *mux.Router {
	CoreRouter = coreRouter
	portRoute := coreRouter.PathPrefix("/port={port}")
	portRouteRegex, _ := portRoute.GetPathRegexp()
	portRouteRegexp := regexp.MustCompile("(?i)" + portRouteRegex)
	RoutePrefixRegexp = "(?i)(" + portRouteRegex + ")?"
	PortRouter = portRoute.Subrouter()
	RootRouter = coreRouter.PathPrefix("").Subrouter()
	RootRouter.SkipClean(true)
	MatchRouter = RootRouter.NewRoute().Subrouter()
	PortRouter.MatcherFunc(func(r *http.Request, rm *mux.RouteMatch) bool {
		return portRouteRegexp.MatchString(r.RequestURI)
	}).HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sport := GetStringParamValue(r, "port")
		port, _ := strconv.Atoi(sport)
		rs := GetRequestStore(r)
		rs.RequestPort = sport
		rs.RequestPortNum = port
		rs.RequestPortChecked = true
		uri := portRouteRegexp.ReplaceAllLiteralString(r.RequestURI, "")
		r.RequestURI = uri
		r.URL.Path = uri
		rs.RequestURI = uri
		RootRouter.ServeHTTP(w, r)
	})
	return RootRouter
}

// GetAltPaths expands the optional segments of a path (segments ending with `?`) into all the concrete paths they
// stand for, the most complete one first. Adjacent optional segments are filled left to right: for
// "/{peer}?/{label}?/x" the alternatives are "/{peer}/{label}/x", "/{peer}/x" and "/x", but not "/{label}/x", because a
// request for "/p1/x" could not be told apart from one for "/{peer}/x" and would bind to the wrong variable.
// Alternatives with a trailing slash are added for every path that has optional segments.
func GetAltPaths(path string, key bool) []string {
	var matches [][]int
	if key {
		matches = optionalPathKeyRegexp.FindAllStringIndex(path, -1)
	} else {
		matches = optionalPathRegexp.FindAllStringIndex(path, -1)
	}
	if len(matches) == 0 {
		return []string{path}
	}
	var paths []string
	// skipped[i] is true if the optional segment just before the current one was skipped in paths[i]
	var skipped []bool
	addStatic := func(subPath string) {
		if len(paths) == 0 {
			paths = append(paths, subPath)
			skipped = append(skipped, false)
			return
		}
		for i := range paths {
			paths[i] += subPath
			if subPath != "" {
				skipped[i] = false
			}
		}
	}
	addOptional := func(subPath string, adjacent bool) {
		canSkip := strings.HasSuffix(subPath, "?") && !strings.HasSuffix(subPath, "??")
		subPath = strings.ReplaceAll(subPath, "?", "")
		for i, prefixPath := range paths {
			if adjacent && skipped[i] && canSkip {
				// An earlier adjacent optional segment was skipped, so this one has to be skipped too.
				continue
			}
			if canSkip {
				paths = append(paths, prefixPath)
				skipped = append(skipped, true)
			}
			paths[i] = prefixPath + subPath
			skipped[i] = false
		}
	}
	start := 0
	for i, m := range matches {
		addStatic(path[start:m[0]])
		addOptional(path[m[0]:m[1]], i > 0 && m[0] == start)
		start = m[1]
	}
	if start < len(path) {
		addStatic(path[start:])
	}
	for _, prefixPath := range paths {
		paths = append(paths, prefixPath+"/")
	}
	return paths
}

// routePaths returns the paths to register a route for: the expanded alternatives of the optional segments, plus a
// trailing slash variant of every path that doesn't already have one.
func routePaths(path string) []string {
	altPaths := GetAltPaths(path, true)
	paths := make([]string, 0, 2*len(altPaths))
	seen := map[string]bool{}
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	for _, p := range altPaths {
		add(p)
	}
	for _, p := range altPaths {
		if !strings.HasSuffix(p, "/") {
			add(p + "/")
		}
	}
	return paths
}

// PathRouter creates a subrouter for the path prefix, along with matching subrouters on the co-routers of the router.
func PathRouter(r *mux.Router, path string) *mux.Router {
	pathRouter := PathPrefix(r, path)
	for _, coRouter := range coRoutersMap[r] {
		coRoutersMap[pathRouter] = append(coRoutersMap[pathRouter], PathPrefix(coRouter, path))
	}
	return pathRouter
}

// PathPrefix creates a subrouter for the path prefix, registering the alternatives of its optional segments and the co-router variants as co-routers of the subrouter.
func PathPrefix(r *mux.Router, path string) *mux.Router {
	var subRouter *mux.Router
	// var portSubRouter *mux.Router
	for _, p := range GetAltPaths(path, false) {
		if subRouter == nil {
			subRouter = r.PathPrefix(p).Subrouter()
		} else {
			coRoutersMap[subRouter] = append(coRoutersMap[subRouter], r.PathPrefix(p).Subrouter())
		}
		for _, coRouter := range coRoutersMap[r] {
			coRoutersMap[subRouter] = append(coRoutersMap[subRouter], coRouter.PathPrefix(p).Subrouter())
		}
		// routerPath := p
		// if lpath, err := r.NewRoute().BuildOnly().GetPathTemplate(); err == nil {
		// 	routerPath = lpath + p
		// }
		// if portSubRouter == nil {
		// 	portSubRouter = PortRouter.PathPrefix(routerPath).Subrouter()
		// 	portTunnelRouters[routerPath] = portSubRouter
		// } else {
		// 	coRoutersMap[portSubRouter] = append(coRoutersMap[portSubRouter], PortRouter.PathPrefix(routerPath).Subrouter())
		// }
	}
	return subRouter
}

// addRoutes registers the handler for each path on the router and its co-routers, limited to the methods if any are given.
func addRoutes(r *mux.Router, altPaths []string, f func(http.ResponseWriter, *http.Request), methods []string) {
	for _, p := range altPaths {
		if len(methods) > 0 {
			r.HandleFunc(p, f).Methods(methods...)
			for _, coRouter := range coRoutersMap[r] {
				coRouter.HandleFunc(p, f).Methods(methods...)
			}
		} else {
			r.HandleFunc(p, f)
			for _, coRouter := range coRoutersMap[r] {
				coRouter.HandleFunc(p, f)
			}
		}
	}
}

// AddRoute registers the handler for the path on the router and its co-routers, along with the alternatives of the optional segments and the trailing slash variants.
func AddRoute(r *mux.Router, path string, f func(http.ResponseWriter, *http.Request), methods ...string) {
	addRoutes(r, routePaths(path), f, methods)
}

// func RegisterPortRoute(r *mux.Router, hijackPort bool, path string, f func(http.ResponseWriter, *http.Request), methods ...string) error {
// 	lpath, err := r.NewRoute().BuildOnly().PathPrefix(path).GetPathTemplate()
// 	if err != nil {
// 		return err
// 	}
// 	if portTunnelRouters[lpath] == nil && hijackPort {
// 		portTunnelRouters[lpath] = r.PathPrefix(path).Subrouter()
// 	}
// 	if portTunnelRouters[lpath] != nil {
// 		AddRoute(portTunnelRouters[lpath], "", f, methods...)
// 	}
// 	return nil
// }

// AddRouteWithPort registers the route as AddRoute does; the registration of a port specific route is commented out.
func AddRouteWithPort(r *mux.Router, path string, f func(http.ResponseWriter, *http.Request), methods ...string) {
	AddRoute(r, path, f, methods...)
	//RegisterPortRoute(r, false, path, f, methods...)
}

// AddRouteQ registers a route that matches only when the query param is present, and binds its value to a route var of the same name.
func AddRouteQ(r *mux.Router, path string, f func(http.ResponseWriter, *http.Request), queryParam string, methods ...string) {
	addRouteQ(r, path, f, queryParam, false, methods...)
}

// AddRouteQO registers routes for the path with the query param, and without it, as the param is optional.
func AddRouteQO(r *mux.Router, path string, f func(http.ResponseWriter, *http.Request), queryParam string, methods ...string) {
	addRouteQ(r, path, f, queryParam, true, methods...)
}

// addRouteQ registers the route with the query param on the router and its co-routers, and one without the param if it is optional.
func addRouteQ(r *mux.Router, path string, f func(http.ResponseWriter, *http.Request), queryParam string, queryOptional bool, methods ...string) {
	queryKey := fmt.Sprintf("{%s}", queryParam)
	for _, p := range routePaths(path) {
		r.HandleFunc(p, f).Queries(queryParam, queryKey).Methods(methods...)
		if queryOptional {
			r.HandleFunc(p, f).Methods(methods...)
		}
		for _, coRouter := range coRoutersMap[r] {
			coRouter.HandleFunc(p, f).Queries(queryParam, queryKey).Methods(methods...)
			if queryOptional {
				// The route without the query is also for the co-routers, which serve the alternative paths.
				coRouter.HandleFunc(p, f).Methods(methods...)
			}
		}
	}
}

// addQueries registers the handler for each path, matching the given query pairs, on the router and its co-routers.
func addQueries(r *mux.Router, altPaths []string, f func(http.ResponseWriter, *http.Request), qPairs []string, methods []string) {
	for _, p := range altPaths {
		r.HandleFunc(p, f).Queries(qPairs...).Methods(methods...)
		for _, coRouter := range coRoutersMap[r] {
			coRouter.HandleFunc(p, f).Queries(qPairs...).Methods(methods...)
		}
	}
}

// AddRouteWithMultiQ registers a route per set of query params, where the params accumulate from one set to the
// next, and the params of a set are all expected together. For sets {a,b},{c},{d,e} it registers routes matching
// (a,b), (a,b,c) and (a,b,c,d,e), most specific first. Handlers read these params from the route vars, which only
// carry the params the matched route was registered with. A route without any query params is registered only if the
// first set is empty, e.g. {},{a,b} registers a route without params and one for (a,b).
func AddRouteWithMultiQ(r *mux.Router, path string, f func(http.ResponseWriter, *http.Request), qParamsSets [][]string, methods ...string) {
	altPaths := routePaths(path)
	qPairSets := [][]string{}
	qPairs := []string{}
	for _, qParams := range qParamsSets {
		changed := false
		for _, q := range qParams {
			if len(q) > 0 {
				qPairs = append(qPairs, q, fmt.Sprintf("{%s}", q))
				changed = true
			}
		}
		if changed {
			qPairSets = append(qPairSets, append([]string{}, qPairs...))
		}
	}
	for i := len(qPairSets) - 1; i >= 0; i-- {
		addQueries(r, altPaths, f, qPairSets[i], methods)
	}
	if len(qParamsSets) == 0 || len(qParamsSets[0]) == 0 {
		addRoutes(r, altPaths, f, methods)
	}
}

// GetRootURI splits the URI into its first path segment and the rest, or returns empty strings if it has none.
func GetRootURI(uri string) (string, string) {
	uriMatch := RootURIRegexp.FindStringSubmatch(uri)
	if len(uriMatch) > 2 {
		return uriMatch[1], uriMatch[2]
	}
	return "", ""
}
