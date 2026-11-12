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

package info

import (
	"fmt"
	"goto/pkg/global"
	"goto/pkg/server/middleware"
	"goto/pkg/types"
	"goto/pkg/util"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/gorilla/mux"
)

type APIRoutes map[string]map[string]map[string][]types.Pair[string, []string]

var (
	Middleware = middleware.NewMiddleware("info", setRoutes, nil)
	// routes has the routes that are found, and routesWithCurl the curl commands of the same routes, where the base URL
	// of the server is a %s. They are replaced when the routes are loaded, and are read by requests as they come, so
	// both are guarded by routesLock.
	routes         = APIRoutes{}
	routesWithCurl = APIRoutes{}
	routesLock     sync.Mutex
)

// setRoutes registers the /goto routes for the version and for listing the routes/APIs.
func setRoutes(r *mux.Router) {
	infoRouter := middleware.RootPath("/goto")
	util.AddRoute(infoRouter, "/version", showVersion, "GET")
	util.AddRoute(infoRouter, "/{k:routes|apis}/refresh", printRoutes, "GET")
	util.AddRoute(infoRouter, "/{k:routes|apis}/level={level}", printRoutes, "GET")
	util.AddRouteWithMultiQ(infoRouter, "/{k:routes|apis}", printRoutes, [][]string{{}, {"q"}, {"url"}}, "GET")
}

// showVersion responds with the goto version and commit.
func showVersion(w http.ResponseWriter, r *http.Request) {
	util.WriteJsonPayload(w, map[string]string{"version": global.Version, "commit": global.Commit})
}

// printRoutes reads the level, query, url and refresh options of the request and reports the routes/APIs as text, JSON or YAML.
func printRoutes(w http.ResponseWriter, r *http.Request) {
	level := util.GetIntParamValue(r, "level")
	q := util.GetStringParamValue(r, "q")
	baseURL := util.GetStringParamValue(r, "url")
	refresh := strings.Contains(r.RequestURI, "refresh")
	yaml := util.IsAcceptYAML(r)
	text := util.IsAcceptText(r)
	PrintRoutes(w, level, q, baseURL, refresh, text, yaml)
	util.AddLogMessage("Routes/APIs reported", r)
}

// PrintRoutes writes the selected routes/APIs either as a sorted plain text list or as JSON or YAML.
func PrintRoutes(w http.ResponseWriter, level int, query, baseURL string, refresh, text, yaml bool) {
	outputRoutes := selectRoutes(level, query, baseURL, refresh)
	if text {
		output := []string{}
		for k1, v1 := range outputRoutes {
			if len(v1) > 0 {
				for k2, v2 := range v1 {
					if len(v2) > 0 {
						for k3, v3 := range v2 {
							if len(v3) > 0 {
								for _, v4 := range v3 {
									output = append(output, fmt.Sprintf("%s %s", v4.Left, v4.Right))
								}
							} else {
								output = append(output, fmt.Sprintf("%s", k3))
							}
						}
					} else {
						output = append(output, fmt.Sprintf("%s", k2))
					}
				}
			} else {
				output = append(output, fmt.Sprintf("%s", k1))
			}
		}
		slices.Sort(output)
		for _, v := range output {
			fmt.Fprintln(w, v)
		}
	} else {
		util.WriteJsonOrYAMLPayload(w, outputRoutes, yaml)
	}
}

// selectRoutes makes the output for a request: the routes that match the query, down to the given level, and as curl
// commands for the base URL if there is one. The output has maps and slices of its own where it is changed, and the
// ones it shares with the cached routes are only read, since loading replaces the cache and does not change it.
func selectRoutes(level int, query, baseURL string, refresh bool) APIRoutes {
	routesLock.Lock()
	defer routesLock.Unlock()
	if refresh || len(routes) == 0 {
		loadAPIs()
	}
	var outputRoutes APIRoutes
	if baseURL != "" {
		outputRoutes = filterRoutes(routesWithCurl, query)
	} else {
		outputRoutes = filterAPIs(query)
	}
	if level > 0 {
		outputRoutes = trimAPIs(outputRoutes, level)
	}
	if baseURL != "" {
		outputRoutes = getCurls(outputRoutes, baseURL)
	}
	return outputRoutes
}

// filterAPIs returns the cached routes that match the query.
func filterAPIs(query string) APIRoutes {
	return filterRoutes(routes, query)
}

// filterRoutes returns the part of source whose root, sub root or sub path contains the query, where an empty query selects everything.
func filterRoutes(source APIRoutes, query string) APIRoutes {
	outputRoutes := APIRoutes{}
	for r, subRoutes := range source {
		rootMatched := strings.Contains(r, query)
		if rootMatched || query == "" {
			outputRoutes[r] = map[string]map[string][]types.Pair[string, []string]{}
		}
		for subR, subPaths := range subRoutes {
			subRMatched := rootMatched || strings.Contains(subR, query)
			if subRMatched || query == "" {
				if outputRoutes[r] == nil {
					outputRoutes[r] = map[string]map[string][]types.Pair[string, []string]{}
				}
				outputRoutes[r][subR] = map[string][]types.Pair[string, []string]{}
			}
			for subP, paths := range subPaths {
				subPMatched := subRMatched || strings.Contains(subP, query)
				if subPMatched || query == "" {
					if outputRoutes[r] == nil {
						outputRoutes[r] = map[string]map[string][]types.Pair[string, []string]{}
					}
					// The sub root is not in the output when only the sub path matches.
					if outputRoutes[r][subR] == nil {
						outputRoutes[r][subR] = map[string][]types.Pair[string, []string]{}
					}
					outputRoutes[r][subR][subP] = paths
				}
			}
		}
	}
	return outputRoutes
}

// trimAPIs returns a copy of the routes cut to the given nesting level: 1 for roots, 2 for sub roots, 3 for sub paths.
func trimAPIs(matches APIRoutes, level int) APIRoutes {
	outputRoutes := APIRoutes{}
	for k1, v1 := range matches {
		outputRoutes[k1] = map[string]map[string][]types.Pair[string, []string]{}
		if level == 1 {
			continue
		}
		for k2, v2 := range v1 {
			outputRoutes[k1][k2] = map[string][]types.Pair[string, []string]{}
			if level == 2 {
				continue
			} else {
				for k3, v3 := range v2 {
					outputRoutes[k1][k2][k3] = []types.Pair[string, []string]{}
					if level == 3 {
						continue
					} else {
						for _, pair := range v3 {
							outputRoutes[k1][k2][k3] = append(outputRoutes[k1][k2][k3], pair)
						}
					}
				}
			}
		}
	}
	return outputRoutes
}

// getCurls makes the curl commands of the matches, which are the curls of the routes (see routesWithCurl) with the base
// URL put in them, and escapes what the shell takes in them. The matches are not changed.
func getCurls(matches APIRoutes, baseURL string) APIRoutes {
	outputRoutes := APIRoutes{}
	for k1, v1 := range matches {
		outputRoutes[k1] = map[string]map[string][]types.Pair[string, []string]{}
		for k2, v2 := range v1 {
			outputRoutes[k1][k2] = map[string][]types.Pair[string, []string]{}
			for k3, v3 := range v2 {
				pairs := make([]types.Pair[string, []string], 0, len(v3))
				for _, v4 := range v3 {
					curls := make([]string, 0, len(v4.Right))
					for _, c := range v4.Right {
						// The curl has the base URL as its first %s. The rest of it can have a % of its own.
						curl := strings.Replace(c, "%s", baseURL, 1)
						curl = strings.ReplaceAll(curl, `?`, `\?`)
						curl = strings.ReplaceAll(curl, `&`, `\&`)
						curls = append(curls, curl)
					}
					pairs = append(pairs, types.Pair[string, []string]{Left: v4.Left, Right: curls})
				}
				outputRoutes[k1][k2][k3] = pairs
			}
		}
	}
	return outputRoutes
}

// loadAPIs replaces the cached routes with the ones that are found now. It must be called with routesLock held.
func loadAPIs() {
	routes = APIRoutes{}
	routesWithCurl = APIRoutes{}
	loadAPIsFromRouter(util.CoreRouter)
	for _, router := range middleware.RootRouters {
		loadAPIsFromRouter(router)
	}
}

// loadAPIsFromRouter walks a router and records its routes by root, sub root and sub path, with their methods and curl commands, skipping duplicates.
func loadAPIsFromRouter(r *mux.Router) {
	// The core router is not there until the server is set up.
	if r == nil {
		return
	}
	// Routes are registered both with and without a trailing slash, but are the same route, so they are reported once,
	// without the trailing slash. The set has the routes (path with queries, and methods) that are already reported.
	seen := map[string]struct{}{}
	r.Walk(func(route *mux.Route, router *mux.Router, ancestors []*mux.Route) error {
		methods, _ := route.GetMethods()
		if len(methods) == 0 {
			return nil
		}
		path, _ := route.GetPathTemplate()
		path = strings.TrimRight(path, "/")
		pieces := strings.Split(path, "/")
		if len(pieces) < 2 || pieces[1] == "" {
			return nil
		}
		root := ""
		index := 1
		root = "/" + pieces[index]
		index++
		if routes[root] == nil {
			routes[root] = map[string]map[string][]types.Pair[string, []string]{}
			routesWithCurl[root] = map[string]map[string][]types.Pair[string, []string]{}
		}
		subroot := ""
		if len(pieces) > index {
			subroot = root + "/" + pieces[index]
			index++
		}
		if routes[root][subroot] == nil {
			routes[root][subroot] = map[string][]types.Pair[string, []string]{}
			routesWithCurl[root][subroot] = map[string][]types.Pair[string, []string]{}
		}

		subpath := ""
		if len(pieces) > index {
			subpath = subroot + "/" + pieces[index]
			index++
		}
		if routes[root][subroot][subpath] == nil {
			routes[root][subroot][subpath] = []types.Pair[string, []string]{}
			routesWithCurl[root][subroot][subpath] = []types.Pair[string, []string]{}
		}

		fullPath := path
		if route.GetHandler() != nil {
			queries, _ := route.GetQueriesTemplates()
			if len(queries) > 0 {
				fullPath += "?"
				for i, q := range queries {
					fullPath = fmt.Sprintf("%s%s", fullPath, q)
					if i < len(queries)-1 {
						fullPath += "&"
					}
				}
			}
			key := fullPath + " " + strings.Join(methods, ",")
			if _, found := seen[key]; found {
				return nil
			}
			seen[key] = struct{}{}
			routes[root][subroot][subpath] = append(routes[root][subroot][subpath], types.Pair[string, []string]{Left: fullPath, Right: methods})
			curls := []string{}
			for _, method := range methods {
				curls = append(curls, fmt.Sprintf("curl -v -X%s %s%s", method, "%s", fullPath))
			}
			routesWithCurl[root][subroot][subpath] = append(routesWithCurl[root][subroot][subpath], types.Pair[string, []string]{Left: fullPath, Right: curls})
		}
		return nil
	})
}
