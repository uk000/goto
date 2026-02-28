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
	"goto/pkg/util"
	"log"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

type APIRoutes map[string]map[string]map[string][]string

var (
	Middleware = middleware.NewMiddleware("info", setRoutes, nil)
	apis       = APIRoutes{}
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	infoRouter := util.PathRouter(r, "/")

	util.AddRoute(infoRouter, "/version", showVersion, "GET")
	util.AddRouteMultiQ(infoRouter, "/{k:routes|apis}", showApis, []string{"q", "url"}, "GET")
	util.AddRoute(infoRouter, "/{k:routes|apis}/level={level}", showApis, "GET")
}

func showVersion(w http.ResponseWriter, r *http.Request) {
	util.WriteJsonPayload(w, map[string]string{"version": global.Version, "commit": global.Commit})
}

func showApis(w http.ResponseWriter, r *http.Request) {
	level := util.GetIntParamValue(r, "level")
	q := util.GetStringParamValue(r, "q")
	baseURL := util.GetStringParamValue(r, "url")
	PrintRoutes(w, util.RootRouter, level, q, baseURL)
}

func PrintRoutes(w http.ResponseWriter, r *mux.Router, level int, query, baseURL string) {
	if len(apis) == 0 {
		loadAPIs(r)
	}
	a := apis
	if query != "" {
		a = filterAPIs(a, query)
	}
	if level > 0 {
		a = trimAPIs(a, level)
	}
	if baseURL != "" {
		a = getCurls(a, baseURL)
	}
	util.WriteJsonPayload(w, a)
}

func filterAPIs(apis APIRoutes, query string) APIRoutes {
	apis2 := APIRoutes{}
	for k1, v1 := range apis {
		if strings.Contains(k1, query) {
			apis2[k1] = v1
			continue
		}
		for k2, v2 := range v1 {
			if strings.Contains(k2, query) {
				if apis2[k1] == nil {
					apis2[k1] = map[string]map[string][]string{}
				}
				apis2[k1][k2] = v2
				continue
			}
			for k3, v3 := range v2 {
				if strings.Contains(k3, query) {
					if apis2[k1] == nil {
						apis2[k1] = map[string]map[string][]string{}
					}
					if apis2[k1][k2] == nil {
						apis2[k1][k2] = map[string][]string{}
					}
					apis2[k1][k2][k3] = v3
				}
			}
		}
	}
	return apis2
}

func trimAPIs(apis APIRoutes, level int) APIRoutes {
	apis2 := APIRoutes{}
	for k1, v1 := range apis {
		apis2[k1] = map[string]map[string][]string{}
		if level == 1 {
			continue
		}
		for k2, v2 := range v1 {
			apis2[k1][k2] = map[string][]string{}
			if level == 2 {
				continue
			}
			for k3, v3 := range v2 {
				if level == 3 {
					apis2[k1][k2][k3] = []string{}
				} else {
					apis2[k1][k2][k3] = v3
				}
			}
		}
	}
	return apis2
}

func getCurls(apis APIRoutes, baseURL string) APIRoutes {
	curls := APIRoutes{}
	for k1, v1 := range apis {
		curls[k1] = map[string]map[string][]string{}
		for k2, v2 := range v1 {
			curls[k1][k2] = map[string][]string{}
			for k3, v3 := range v2 {
				curls[k1][k2][k3] = []string{}
				for _, c := range v3 {
					curls[k1][k2][k3] = append(curls[k1][k2][k3], fmt.Sprintf(c, baseURL))
				}
			}
		}
	}
	return curls
}

func loadAPIs(r *mux.Router) {
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
		if len(pieces) >= index+1 {
			subroot = root + "/" + pieces[index]
			index++
		} else {
			subroot = root
		}
		if subroot != "" || len(methods) > 0 {
			if apis[root][subroot] == nil {
				apis[root][subroot] = map[string][]string{}
			}
		}
		subpath := ""
		if len(pieces) > index+1 {
			subpath = path
		} else {
			subpath = subroot
		}
		if (subroot != "" || len(methods) > 0) && route.GetHandler() != nil {
			if subroot == "" && subpath == "" {
				log.Println(path)
			}
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
			for _, method := range methods {
				apis[root][subroot][subpath] = append(apis[root][subroot][subpath], fmt.Sprintf("curl -v -X%s %s%s", method, "%s", subpath))
			}
			// log.Printf("%s -> %+v", subpath, route.GetHandler())
		}
		return nil
	})
}
