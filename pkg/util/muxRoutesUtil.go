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

func GetAltPaths(path string, key bool) []string {
	var matches [][]int
	if key {
		matches = optionalPathKeyRegexp.FindAllStringIndex(path, -1)
	} else {
		matches = optionalPathRegexp.FindAllStringIndex(path, -1)
	}
	var paths []string
	addSubpath := func(subPath string) {
		canSkip := strings.HasSuffix(subPath, "?") && !strings.HasSuffix(subPath, "??")
		subPath = strings.ReplaceAll(subPath, "?", "")
		if len(paths) > 0 {
			for i, prefixPath := range paths {
				if canSkip {
					paths = append(paths, prefixPath)
				}
				paths[i] = prefixPath + subPath
			}
		} else {
			paths = append(paths, subPath)
		}
	}
	if len(matches) > 0 {
		start := 0
		end := 0
		for _, m := range matches {
			addSubpath(path[start:m[0]])
			addSubpath(path[m[0]:m[1]])
			start = m[1]
			end = m[1]
		}
		if end < len(path) {
			addSubpath(path[end:])
		}
		for _, prefixPath := range paths {
			paths = append(paths, prefixPath+"/")
		}
	} else {
		paths = []string{path}
	}
	return paths
}

func PathRouter(r *mux.Router, path string) *mux.Router {
	pathRouter := PathPrefix(r, path)
	for _, coRouter := range coRoutersMap[r] {
		coRoutersMap[pathRouter] = append(coRoutersMap[pathRouter], PathPrefix(coRouter, path))
	}
	return pathRouter
}

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

func AddRoute(r *mux.Router, path string, f func(http.ResponseWriter, *http.Request), methods ...string) {
	addRoutes(r, GetAltPaths(path, true), f, methods)
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

func AddRouteWithPort(r *mux.Router, path string, f func(http.ResponseWriter, *http.Request), methods ...string) {
	AddRoute(r, path, f, methods...)
	//RegisterPortRoute(r, false, path, f, methods...)
}

func AddRouteQ(r *mux.Router, path string, f func(http.ResponseWriter, *http.Request), queryParam string, methods ...string) {
	addRouteQ(r, path, f, queryParam, false, methods...)
}

func AddRouteQO(r *mux.Router, path string, f func(http.ResponseWriter, *http.Request), queryParam string, methods ...string) {
	addRouteQ(r, path, f, queryParam, true, methods...)
}

func addRouteQ(r *mux.Router, path string, f func(http.ResponseWriter, *http.Request), queryParam string, queryOptional bool, methods ...string) {
	queryKey := fmt.Sprintf("{%s}", queryParam)
	for _, p := range GetAltPaths(path, true) {
		r.HandleFunc(p, f).Queries(queryParam, queryKey).Methods(methods...)
		if queryOptional {
			r.HandleFunc(p, f).Methods(methods...)
		}
		for _, coRouter := range coRoutersMap[r] {
			coRouter.HandleFunc(p, f).Queries(queryParam, queryKey).Methods(methods...)
		}
	}
}

func addQueries(r *mux.Router, altPaths []string, f func(http.ResponseWriter, *http.Request), qPairs []string, methods []string) {
	for _, p := range altPaths {
		r.HandleFunc(p, f).Queries(qPairs...).Methods(methods...)
		for _, coRouter := range coRoutersMap[r] {
			coRouter.HandleFunc(p, f).Queries(qPairs...).Methods(methods...)
		}
	}
}

func AddRouteWithMultiQ(r *mux.Router, path string, f func(http.ResponseWriter, *http.Request), qParamsSets [][]string, methods ...string) {
	altPaths := GetAltPaths(path, true)
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
			qPairSets = append(qPairSets, qPairs)
		}
	}
	for i := len(qPairSets) - 1; i >= 0; i-- {
		addQueries(r, altPaths, f, qPairSets[i], methods)
	}
	if len(qParamsSets) == 0 || len(qParamsSets[0]) == 0 {
		addRoutes(r, altPaths, f, methods)
	}
}

func GetRootURI(uri string) (string, string) {
	uriMatch := RootURIRegexp.FindStringSubmatch(uri)
	if len(uriMatch) > 2 {
		return uriMatch[1], uriMatch[2]
	}
	return "", ""
}
