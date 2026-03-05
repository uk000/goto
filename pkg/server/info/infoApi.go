/**
 * Copyright 2025 uk
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

	"github.com/gorilla/mux"
)

type APISubPaths map[string]map[string][]string
type APIRoutes map[string]map[string][]types.Pair[string, []string]
type APIRouteLookup map[string]map[string]map[string][]string

var (
	Middleware     = middleware.NewMiddleware("info", setRoutes, nil)
	subPaths       = APISubPaths{}
	routes         = APIRoutes{}
	routesWithCurl = APIRoutes{}
	routeLookup    = APIRouteLookup{}
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	infoRouter := middleware.RootPath("/goto")
	util.AddRoute(infoRouter, "/version", showVersion, "GET")
	util.AddRoute(infoRouter, "/{k:routes|apis}/level={level}", showApis, "GET")
	util.AddRouteWithMultiQ(infoRouter, "/{k:routes|apis}", showApis, [][]string{{}, {"q"}, {"url"}}, "GET")
}

func showVersion(w http.ResponseWriter, r *http.Request) {
	util.WriteJsonPayload(w, map[string]string{"version": global.Version, "commit": global.Commit})
}

func showApis(w http.ResponseWriter, r *http.Request) {
	level := util.GetIntParamValue(r, "level")
	q := util.GetStringParamValue(r, "q")
	baseURL := util.GetStringParamValue(r, "url")
	yaml := strings.Contains(r.Header.Get("Accept"), "yaml")
	text := strings.Contains(r.Header.Get("Accept"), "text")
	PrintRoutes(w, level, q, baseURL, text, yaml)
	util.AddLogMessage("Routes/APIs reported", r)
}

func PrintRoutes(w http.ResponseWriter, level int, query, baseURL string, text bool, yaml bool) {
	if len(routes) == 0 {
		loadAPIs()
	}
	outputRoutes := filterAPIs(query)
	if level > 0 {
		outputRoutes = trimAPIs(outputRoutes, level)
	}
	if baseURL != "" {
		outputRoutes = getCurls(outputRoutes, baseURL)
	}
	if text {
		output := []string{}
		for k1, v1 := range outputRoutes {
			if len(v1) > 0 {
				for k2, v2 := range v1 {
					if len(v2) > 0 {
						for _, v3 := range v2 {
							output = append(output, fmt.Sprintf("%s %s", v3.Left, v3.Right))
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

func filterAPIs(query string) APIRoutes {
	outputRoutes := APIRoutes{}
	for r, subRoutes := range routes {
		rootMatched := strings.Contains(r, query)
		if rootMatched || query == "" {
			outputRoutes[r] = map[string][]types.Pair[string, []string]{}
		}
		for subR, _ := range subRoutes {
			if subR != "" {
				subRMatched := rootMatched || strings.Contains(subR, query)
				if subRMatched || query == "" {
					if outputRoutes[r] == nil {
						outputRoutes[r] = map[string][]types.Pair[string, []string]{}
					}
					outputRoutes[r][subR] = []types.Pair[string, []string]{}
				}
				for _, subP := range subPaths[r][subR] {
					if subP != "" {
						subPMatched := subRMatched || strings.Contains(subP, query)
						if subPMatched || query == "" {
							if outputRoutes[r] == nil {
								outputRoutes[r] = map[string][]types.Pair[string, []string]{}
							}
							outputRoutes[r][subR] = append(outputRoutes[r][subR], types.Pair[string, []string]{subP, routeLookup[r][subR][subP]})
						}
					}
				}
			}
		}
	}
	return outputRoutes
}

func trimAPIs(matches APIRoutes, level int) APIRoutes {
	outputRoutes := APIRoutes{}
	for k1, v1 := range matches {
		outputRoutes[k1] = map[string][]types.Pair[string, []string]{}
		if level == 1 {
			continue
		}
		for k2, v2 := range v1 {
			outputRoutes[k1][k2] = []types.Pair[string, []string]{}
			if level == 2 {
				continue
			} else {
				for _, pair := range v2 {
					outputRoutes[k1][k2] = append(outputRoutes[k1][k2], pair)
				}
			}
		}
	}
	return outputRoutes
}

func getCurls(matches APIRoutes, baseURL string) APIRoutes {
	for k1, v1 := range matches {
		for k2 := range v1 {
			for _, v3 := range routesWithCurl[k1][k2] {
				curls := []string{}
				for _, c := range v3.Right {
					curl := fmt.Sprintf(c, baseURL)
					curl = strings.ReplaceAll(curl, `?`, `\?`)
					curl = strings.ReplaceAll(curl, `&`, `\&`)
					curls = append(curls, curl)
				}
				matches[k1][k2] = append(matches[k1][k2], types.Pair[string, []string]{v3.Left, curls})
			}
		}
	}
	return matches
}

func loadAPIs() {
	loadAPIsFromRouter(util.CoreRouter)
	for _, router := range middleware.RootRouters {
		loadAPIsFromRouter(router)
	}
}

func loadAPIsFromRouter(r *mux.Router) {
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
		if routes[root] == nil {
			routes[root] = map[string][]types.Pair[string, []string]{}
			routesWithCurl[root] = map[string][]types.Pair[string, []string]{}
			routeLookup[root] = map[string]map[string][]string{}
			subPaths[root] = map[string][]string{}
		}
		subroot := ""
		if len(pieces) >= index+1 {
			subroot = root + "/" + pieces[index]
			index++
		} else {
			subroot = root
		}
		if subroot != "" || len(methods) > 0 {
			if routes[root][subroot] == nil {
				routes[root][subroot] = []types.Pair[string, []string]{}
				routesWithCurl[root][subroot] = []types.Pair[string, []string]{}
				routeLookup[root][subroot] = map[string][]string{}
				subPaths[root][subroot] = []string{}
			}
		}
		subpath := ""
		if len(pieces) > index {
			subpath = path
		} else {
			subpath = subroot
		}
		if (subroot != "" || len(methods) > 0) && route.GetHandler() != nil {
			queries, _ := route.GetQueriesTemplates()
			if len(queries) > 0 {
				subpath += "?"
				for i, q := range queries {
					subpath = fmt.Sprintf("%s%s", subpath, q)
					if i < len(queries)-1 {
						subpath += "&"
					}
				}
			}
			routes[root][subroot] = append(routes[root][subroot], types.Pair[string, []string]{subpath, methods})
			curls := []string{}
			for _, method := range methods {
				curls = append(curls, fmt.Sprintf("curl -v -X%s %s%s", method, "%s", subpath))
			}
			routesWithCurl[root][subroot] = append(routesWithCurl[root][subroot], types.Pair[string, []string]{subpath, curls})
			routeLookup[root][subroot][subpath] = methods
			subPaths[root][subroot] = append(subPaths[root][subroot], subpath)
			// log.Printf("%s -> %+v", subpath, route.GetHandler())
		}
		return nil
	})
}
