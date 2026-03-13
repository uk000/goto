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
	"maps"
	"net/http"
	"reflect"
	"regexp"
	"strings"

	"github.com/gorilla/mux"
	"github.com/gorilla/reverse"
)

func matchPieces(pieces1 []string, pieces2 []string) bool {
	if len(pieces1) != len(pieces2) {
		return false
	}
	for i, piece1 := range pieces1 {
		piece2 := pieces2[i]
		if piece1 != piece2 &&
			!((strings.HasPrefix(piece1, "{") && strings.HasSuffix(piece1, "}")) ||
				(strings.HasPrefix(piece2, "{") && strings.HasSuffix(piece2, "}"))) {
			return false
		}
	}
	return true
}

func getURIPieces(uri string) []string {
	uri = strings.ToLower(uri)
	return strings.Split(strings.Split(uri, "?")[0], "/")
}

func MatchURI(uri1 string, uri2 string) bool {
	return matchPieces(getURIPieces(uri1), getURIPieces(uri2))
}

func FindURIInMap(uri string, i interface{}) string {
	if m := reflect.ValueOf(i); m.Kind() == reflect.Map {
		uriPieces1 := getURIPieces(uri)
		for _, k := range m.MapKeys() {
			uri2 := k.String()
			uriPieces2 := getURIPieces(uri2)
			if matchPieces(uriPieces1, uriPieces2) {
				return uri2
			}
		}
	}
	return ""
}

func IsURIInMap(uri string, m map[string]interface{}) bool {
	return FindURIInMap(uri, m) != ""
}

func GetURIRegexpAndRoute(uri string, router *mux.Router) (string, *regexp.Regexp, *mux.Router, *mux.Route, error) {
	return getURIRegexpAndRoute(uri, router, "", false)
}

func getURIRegexpAndRoute(uri string, router *mux.Router, prefixRegexp string, buildOnly bool) (string, *regexp.Regexp, *mux.Router, *mux.Route, error) {
	if uri != "" {
		finalURI, glob := Unglob(strings.ToLower(uri))
		vars := fillerRegexp.FindAllString(finalURI, -1)
		var prefixURI string
		if len(vars) > 0 {
			if pieces := strings.Split(finalURI, vars[0]); len(pieces) > 0 {
				prefixURI = pieces[0]
			}
		}
		if prefixURI == "" {
			prefixURI = finalURI
		}
		for _, v := range vars {
			v2, hasFiller := GetFillerUnmarked(v)
			if router != nil {
				v2 = MarkFiller(v2 + ":[^/&\\?]*")
				finalURI = strings.ReplaceAll(finalURI, v, v2)
			} else if hasFiller {
				v2 = fmt.Sprintf("(?P<%s>[^/&\\?]*)", v2)
				finalURI = strings.ReplaceAll(finalURI, v, v2)
			}
		}
		var subRouter *mux.Router
		var route *mux.Route
		var pathRegex string
		var err error
		if router != nil {
			if buildOnly {
				subRouter = router.NewRoute().BuildOnly().Subrouter()
			} else {
				subRouter = router.NewRoute().Subrouter()
			}
			route = subRouter.PathPrefix(finalURI)
			pathRegex, err = route.GetPathRegexp()
			pathRegex = prefixRegexp + pathRegex + "(.*)?"
		} else {
			pathRegex = prefixRegexp + finalURI + "(.*)?"
		}
		if pathRegex != "" && err == nil {
			//path = strings.ReplaceAll(path, "$", "(/.*)?$")
			if glob {
				pathRegex += GlobRegex
			}
			pathRegex += QueryParamRegex
			re := regexp.MustCompile("(?i)" + pathRegex)
			return prefixURI, re, subRouter, route, nil
		} else {
			return uri, nil, nil, nil, err
		}
	}
	return uri, nil, nil, nil, fmt.Errorf("Empty URI")
}

func GetURIRegexp(uri string) (string, *regexp.Regexp, error) {
	if uri != "" {
		if prefixURI, re, _, _, err := getURIRegexpAndRoute(uri, nil, RoutePrefixRegexp, true); err == nil {
			return prefixURI, re, nil
		} else {
			return uri, nil, err
		}
	}
	return uri, nil, fmt.Errorf("no uri")
}

func BuildURIMatcher(uri string, handlerFunc func(w http.ResponseWriter, r *http.Request)) (string, *regexp.Regexp, *mux.Router, error) {
	if uri != "" {
		if prefixURI, re, rr, err := registerURIRouteAndGetRegex(uri, handlerFunc, MatchRouter); err == nil {
			return prefixURI, re, rr, nil
		} else {
			return uri, nil, nil, err
		}
	}
	return uri, nil, nil, fmt.Errorf("no uri")
}

func BuildURIMatcherForRouter(uri string, handlerFunc func(w http.ResponseWriter, r *http.Request), router *mux.Router) (*regexp.Regexp, error) {
	if uri != "" {
		if _, re, _, err := registerURIRouteAndGetRegex(uri, handlerFunc, router); err == nil {
			return re, nil
		} else {
			return nil, err
		}
	}
	return nil, fmt.Errorf("no uri")
}

func registerURIRouteAndGetRegex(uri string, handler func(http.ResponseWriter, *http.Request), router *mux.Router) (string, *regexp.Regexp, *mux.Router, error) {
	if prefixURI, re, subRouter, route, err := getURIRegexpAndRoute(uri, router, "", false); err == nil {
		route = route.MatcherFunc(func(r *http.Request, rm *mux.RouteMatch) bool {
			return re.MatchString(r.URL.Path)
		}).HandlerFunc(handler)
		return prefixURI, re, subRouter, nil
	} else {
		return uri, nil, nil, err
	}
}

func TransposeURI(sourcePath, targetURI string, vars, headers, queries map[string]string, addQuery map[string]string, removeQuery []string) string {
	path := sourcePath
	if targetURI == "" {
		targetURI = path
	}
	if targetURI == "/" {
		targetURI = "/*"
	}
	targetRoute := MatchRouter.NewRoute().BuildOnly().Path(targetURI)
	targetVars := []string{}
	if targetPath, err := reverse.NewGorillaPath(targetURI, false); err == nil {
		for _, k := range targetPath.Groups() {
			targetVars = append(targetVars, k, vars[k])
		}
		if netURL, err := targetRoute.URLPath(targetVars...); err == nil {
			path = netURL.Path
		} else {
			path = targetURI
		}
	} else {
		path = targetURI
	}
	q := map[string]string{}
	cleanStart := false
	if len(removeQuery) == 1 && removeQuery[0] == "*" {
		cleanStart = true
		removeQuery = nil
	}
	if !cleanStart {
		maps.Copy(q, queries)
	}
	if len(addQuery) > 0 {
		for k, v := range addQuery {
			q[k] = ""
			if captureKey, found := GetFillerUnmarked(v); found {
				qv := queries[captureKey]
				if qv != "" {
					v = qv
				}
			}
			q[k] = v
		}
	}
	for _, k := range removeQuery {
		k = strings.Trim(k, " ")
		delete(q, k)
	}
	if len(q) > 0 {
		path += "?"
		for k, v := range q {
			path += k + "=" + v + "&"
		}
		path = strings.TrimRight(path, "&")
	}
	path = FillValues(path, vars)
	path = FillValues(path, headers)
	return path
}
