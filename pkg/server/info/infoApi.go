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

	"github.com/gorilla/mux"
)

type APIRoutes map[string]map[string]map[string][]types.Pair[string, []string]

var (
	Middleware     = middleware.NewMiddleware("info", setRoutes, nil)
	routes         = APIRoutes{}
	routesWithCurl = APIRoutes{}
)

func setRoutes(r *mux.Router) {
	infoRouter := middleware.RootPath("/goto")
	util.AddRoute(infoRouter, "/version", showVersion, "GET")
	util.AddRoute(infoRouter, "/{k:routes|apis}/refresh", showApis, "GET")
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
	refresh := strings.Contains(r.RequestURI, "refresh")
	yaml := strings.Contains(r.Header.Get("Accept"), "yaml")
	text := strings.Contains(r.Header.Get("Accept"), "text")
	PrintRoutes(w, level, q, baseURL, refresh, text, yaml)
	util.AddLogMessage("Routes/APIs reported", r)
}

func PrintRoutes(w http.ResponseWriter, level int, query, baseURL string, refresh, text, yaml bool) {
	if refresh || len(routes) == 0 {
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

func filterAPIs(query string) APIRoutes {
	outputRoutes := APIRoutes{}
	for r, subRoutes := range routes {
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
					outputRoutes[r][subR][subP] = paths
				}
			}
		}
	}
	return outputRoutes
}

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

func getCurls(matches APIRoutes, baseURL string) APIRoutes {
	for k1, v1 := range matches {
		for k2, v2 := range v1 {
			for k3, v3 := range v2 {
				for _, v4 := range v3 {
					curls := []string{}
					for _, c := range v4.Right {
						curl := fmt.Sprintf(c, baseURL)
						curl = strings.ReplaceAll(curl, `?`, `\?`)
						curl = strings.ReplaceAll(curl, `&`, `\&`)
						curls = append(curls, curl)
					}
					matches[k1][k2][k3] = append(matches[k1][k2][k3], types.Pair[string, []string]{v4.Left, curls})
				}
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
			routes[root][subroot][subpath] = append(routes[root][subroot][subpath], types.Pair[string, []string]{fullPath, methods})
			curls := []string{}
			for _, method := range methods {
				curls = append(curls, fmt.Sprintf("curl -v -X%s %s%s", method, "%s", fullPath))
			}
			routesWithCurl[root][subroot][subpath] = append(routesWithCurl[root][subroot][subpath], types.Pair[string, []string]{fullPath, curls})
		}
		return nil
	})
}
