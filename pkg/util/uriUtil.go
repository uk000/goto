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
	"sync"

	"github.com/gorilla/mux"
	"github.com/gorilla/reverse"
)

// matchPieces tells if two lists of URI pieces are of the same length and match piece by piece, where a {var} piece on either side matches anything.
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

// getURIPieces lower-cases the URI and splits its path, without the query, into pieces.
func getURIPieces(uri string) []string {
	uri = strings.ToLower(uri)
	return strings.Split(strings.Split(uri, "?")[0], "/")
}

// MatchURI tells if two URIs match piece by piece, where {var} pieces are wildcards, ignoring the case and the query.
func MatchURI(uri1 string, uri2 string) bool {
	return matchPieces(getURIPieces(uri1), getURIPieces(uri2))
}

// FindURIInMap returns the key of the map that matches the URI (with {var} wildcards), or empty if none does.
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

// IsURIInMap tells if a key of the map matches the URI.
func IsURIInMap(uri string, m map[string]interface{}) bool {
	return FindURIInMap(uri, m) != ""
}

// routeRegistry serializes the registration of routes on the routers that URI matchers are built for (most importantly
// MatchRouter, which every payload that is set adds a route to), as a router can't be added to by concurrent calls. It also
// remembers the routes that it registered with a handler, so that a URI that is registered again (a payload set again for
// the same URI) gets what its first registration got, instead of a route that is behind the first one and never gets
// a request.
var routeRegistry = struct {
	lock   sync.Mutex
	routes map[registeredRouteKey]*registeredRoute
}{routes: map[registeredRouteKey]*registeredRoute{}}

type registeredRouteKey struct {
	router *mux.Router
	uri    string
	prefix bool
}

type registeredRoute struct {
	prefixURI string
	re        *regexp.Regexp
	subRouter *mux.Router
}

// GetURIRegexpAndRoute builds the regexp for the URI, along with a route on the router, holding the registry lock if a router is given.
func GetURIRegexpAndRoute(uri string, prefix bool, router *mux.Router) (string, *regexp.Regexp, *mux.Router, *mux.Route, error) {
	if router != nil {
		routeRegistry.lock.Lock()
		defer routeRegistry.lock.Unlock()
	}
	return getURIRegexpAndRoute(uri, router, "", prefix, false)
}

// getURIRegexpAndRoute builds the regexp for a URI, along with a route on the given router when there is one. Adding the route
// changes the router, so callers that give a router that is shared must hold routeRegistry.lock.
func getURIRegexpAndRoute(uri string, router *mux.Router, prefixRegexp string, prefix, buildOnly bool) (string, *regexp.Regexp, *mux.Router, *mux.Route, error) {
	finalURI, glob := Unglob(strings.ToLower(uri))
	vars := fillerRegexp.FindAllString(finalURI, -1)
	hasFiller := len(vars) > 0
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
		v2, found := GetFillerUnmarked(v)
		if router != nil {
			v2 = MarkFiller(v2 + ":[^/&\\?]*")
			finalURI = strings.ReplaceAll(finalURI, v, v2)
		} else if found {
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
		finalURI = strings.TrimPrefix(finalURI, "/")
		if finalURI != "" {
			if hasFiller {
				finalURI = "/" + finalURI
			} else {
				finalURI = fmt.Sprintf("/{path:(?i)%s}", finalURI)
			}
		}
		if prefix {
			route = subRouter.PathPrefix(finalURI)
		} else {
			route = subRouter.Path(finalURI)
		}
		pathRegex, err = route.GetPathRegexp()
		if err == nil {
			pathRegex = DeMux(pathRegex)
			if prefix {
				pathRegex = prefixRegexp + pathRegex + "(.*)?"
			} else {
				// The regexp of a path that is not a prefix ends the string, which would leave the query that is
				// added to it below behind.
				pathRegex = strings.TrimSuffix(pathRegex, "$")
			}
		}
	} else {
		pathRegex = prefixRegexp + finalURI
		if prefix {
			pathRegex += "(.*)?"
		}
	}
	if pathRegex != "" && err == nil {
		//path = strings.ReplaceAll(path, "$", "(/.*)?$")
		if glob && prefix {
			pathRegex += GlobRegex
		}
		pathRegex += QueryParamRegex
		re := regexp.MustCompile("(?i)" + pathRegex)
		return prefixURI, re, subRouter, route, nil
	} else {
		return uri, nil, nil, nil, err
	}
}

// GetURIRegexp builds the prefix matching regexp for the URI without registering any route.
func GetURIRegexp(uri string) (string, *regexp.Regexp, error) {
	if uri != "" {
		if prefixURI, re, _, _, err := getURIRegexpAndRoute(uri, nil, RoutePrefixRegexp, true, true); err == nil {
			return prefixURI, re, nil
		} else {
			return uri, nil, err
		}
	}
	return uri, nil, fmt.Errorf("no uri")
}

// BuildURIMatcher registers a route with the handler for the URI on MatchRouter, and returns its prefix URI, regexp and subrouter.
func BuildURIMatcher(uri string, prefix bool, handlerFunc func(w http.ResponseWriter, r *http.Request)) (string, *regexp.Regexp, *mux.Router, error) {
	if prefixURI, re, rr, err := registerURIRouteAndGetRegex(uri, prefix, handlerFunc, MatchRouter); err == nil {
		return prefixURI, re, rr, nil
	} else {
		return uri, nil, nil, err
	}
}

// BuildURIRegexp builds the same regexp as BuildURIMatcher does, but registers no route. It is for callers that match
// URIs themselves and only observe the requests (e.g. hooks). A route registered on MatchRouter is matched by the
// server's router before the handlers that serve traffic, and it answers every request matching its URI with an
// empty response, so such callers must not use BuildURIMatcher: tracking a URI would take over that URI, and
// tracking just headers (the empty URI matches everything) would take over all traffic.
func BuildURIRegexp(uri string, prefix bool) (string, *regexp.Regexp, error) {
	// The route that the regexp is built from lives on a router of its own, so that nothing is added to MatchRouter.
	// The root of that router has no path of its own, so the regexp is the same as the one built on MatchRouter.
	prefixURI, re, _, _, err := getURIRegexpAndRoute(uri, mux.NewRouter(), "", prefix, true)
	if err == nil && re == nil {
		err = fmt.Errorf("no regexp for uri [%s]", uri)
	}
	if err != nil {
		return uri, nil, err
	}
	return prefixURI, re, nil
}

// BuildURIMatcherForRouter registers a route with the handler for the URI on the given router, and returns its regexp.
func BuildURIMatcherForRouter(uri string, prefix bool, handlerFunc func(w http.ResponseWriter, r *http.Request), router *mux.Router) (*regexp.Regexp, error) {
	if _, re, _, err := registerURIRouteAndGetRegex(uri, prefix, handlerFunc, router); err == nil {
		return re, nil
	} else {
		return nil, err
	}
}

// registerURIRouteAndGetRegex registers the route for the URI on the router the first time, and gives the same result when the URI is registered again.
func registerURIRouteAndGetRegex(uri string, prefix bool, handler func(http.ResponseWriter, *http.Request), router *mux.Router) (string, *regexp.Regexp, *mux.Router, error) {
	if router == nil {
		return uri, nil, nil, fmt.Errorf("no router to register uri [%s] on", uri)
	}
	routeRegistry.lock.Lock()
	defer routeRegistry.lock.Unlock()
	key := registeredRouteKey{router: router, uri: strings.ToLower(uri), prefix: prefix}
	if registered := routeRegistry.routes[key]; registered != nil {
		return registered.prefixURI, registered.re, registered.subRouter, nil
	}
	if prefixURI, re, subRouter, route, err := getURIRegexpAndRoute(uri, router, "", prefix, false); err == nil {
		route = route.MatcherFunc(func(r *http.Request, rm *mux.RouteMatch) bool {
			return re.MatchString(r.URL.Path)
		}).HandlerFunc(handler)
		routeRegistry.routes[key] = &registeredRoute{prefixURI: prefixURI, re: re, subRouter: subRouter}
		return prefixURI, re, subRouter, nil
	} else {
		return uri, nil, nil, err
	}
}

// TransposeURI builds the URI to forward to from the target template, filling in vars and headers and adjusting the query params.
func TransposeURI(requestURI, sourceURI, targetURI string, vars, headers, queries map[string]string, addQuery map[string]string, removeQuery []string) string {
	path := requestURI
	if targetURI == "" {
		targetURI = path
	}
	if targetURI == "/" {
		targetURI = "/*"
	}
	// The route is only there to build the target URI from, so it lives on a router of its own. A route added to MatchRouter
	// would stay there for good, and be added to it by concurrent requests.
	targetRoute := mux.NewRouter().NewRoute().BuildOnly().Path(targetURI)
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
	if strings.HasSuffix(path, "*") {
		sourceURI = strings.TrimRight(sourceURI, "/*")
		sourcePieces := strings.Split(sourceURI, "/")
		targetPrefix := strings.Split(path, "*")
		requestURIPieces := strings.Split(requestURI, "/")
		path = targetPrefix[0] + strings.Join(requestURIPieces[len(sourcePieces):], "/")
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
