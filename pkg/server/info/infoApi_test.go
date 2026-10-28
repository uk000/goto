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
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/server/middleware"
	"goto/pkg/types"
	"goto/pkg/util"

	"github.com/gorilla/mux"
	"gopkg.in/yaml.v3"
)

var (
	stubOnce  sync.Once
	routeOnce sync.Once
)

func stubGlobalFuncs() {
	global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsListenerPresent = func(int) bool { return false }
	global.Funcs.IsListenerOpen = func(int) bool { return false }
	global.Funcs.GetListenerID = func(int) string { return "" }
	global.Funcs.GetListenerLabel = func(*http.Request) string { return "" }
	global.Funcs.GetListenerLabelForPort = func(int) string { return "" }
	util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
}

type pair = types.Pair[string, []string]

// sampleRoutes are cached routes, and the curls of them, as loadAPIs makes them.
func sampleRoutes() (APIRoutes, APIRoutes) {
	routes := APIRoutes{
		"/server": {
			"/server/listeners": {
				"/server/listeners/add": {{Left: "/server/listeners/add", Right: []string{"POST", "PUT"}}},
			},
			"/server/probes": {
				"/server/probes/set": {{Left: "/server/probes/set?uri={uri}", Right: []string{"POST"}}},
			},
			"/server/bare": {},
		},
		"/goto": {
			"/goto/version": {"": {{Left: "/goto/version", Right: []string{"GET"}}}},
		},
		"/lonely": {},
	}
	curls := APIRoutes{
		"/server": {
			"/server/listeners": {
				"/server/listeners/add": {{Left: "/server/listeners/add", Right: []string{"curl -v -XPOST %s/server/listeners/add", "curl -v -XPUT %s/server/listeners/add"}}},
			},
			"/server/probes": {
				"/server/probes/set": {{Left: "/server/probes/set?uri={uri}", Right: []string{"curl -v -XPOST %s/server/probes/set?uri={uri}"}}},
			},
			"/server/bare": {},
		},
		"/goto": {
			"/goto/version": {"": {{Left: "/goto/version", Right: []string{"curl -v -XGET %s/goto/version"}}}},
		},
		"/lonely": {},
	}
	return routes, curls
}

// seed makes the given routes the cached ones for the length of the test.
func seed(t *testing.T, r, curls APIRoutes) {
	t.Helper()
	savedRoutes, savedCurls := routes, routesWithCurl
	routes, routesWithCurl = r, curls
	t.Cleanup(func() { routes, routesWithCurl = savedRoutes, savedCurls })
}

func seedSample(t *testing.T) {
	t.Helper()
	r, c := sampleRoutes()
	seed(t, r, c)
}

// useCoreRouter makes the router the one that routes are loaded from, besides the root routers.
func useCoreRouter(t *testing.T, r *mux.Router) {
	t.Helper()
	saved := util.CoreRouter
	util.CoreRouter = r
	t.Cleanup(func() { util.CoreRouter = saved })
}

func setup(t *testing.T) http.Handler {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
	routeOnce.Do(func() { Middleware.SetRoutes(nil) })
	return middleware.RootRouters["/goto"]
}

func serve(t *testing.T, method, target string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	h := setup(t)
	req := httptest.NewRequest(method, target, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, 8080))
	_, req, _ = util.WithRequestStore(req, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// shaped is what the routes look like as JSON: a pair is an object that has the route as its key and its methods (or
// curls) as the text of the value, as the pair encodes itself.
type shaped map[string]map[string]map[string][]map[string]string

func shape(r APIRoutes) shaped {
	out := shaped{}
	for k1, v1 := range r {
		out[k1] = map[string]map[string][]map[string]string{}
		for k2, v2 := range v1 {
			out[k1][k2] = map[string][]map[string]string{}
			for k3, v3 := range v2 {
				pairs := []map[string]string{}
				for _, p := range v3 {
					pairs = append(pairs, map[string]string{p.Left: fmt.Sprint(p.Right)})
				}
				out[k1][k2][k3] = pairs
			}
		}
	}
	return out
}

func decode(t *testing.T, rr *httptest.ResponseRecorder) shaped {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d %q, want 200", rr.Code, rr.Body.String())
	}
	var out shaped
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("body %q is not JSON routes: %v", rr.Body.String(), err)
	}
	return out
}

func TestVersionReportsVersionAndCommit(t *testing.T) {
	savedVersion, savedCommit := global.Version, global.Commit
	global.Version, global.Commit = "v9.9.9", "abc123"
	t.Cleanup(func() { global.Version, global.Commit = savedVersion, savedCommit })
	rr := serve(t, "GET", "/goto/version")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var got map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q is not JSON: %v", rr.Body.String(), err)
	}
	if want := (map[string]string{"version": "v9.9.9", "commit": "abc123"}); !reflect.DeepEqual(got, want) {
		t.Errorf("version = %v, want %v", got, want)
	}
	if ct := rr.Header().Get(constants.HeaderContentType); !strings.Contains(ct, "json") {
		t.Errorf("content type = %q, want JSON", ct)
	}
	if rr := serve(t, "POST", "/goto/version"); rr.Code == http.StatusOK {
		t.Error("the version is only given for GET")
	}
}

func TestRoutesAndAPIsGiveTheSameRoutes(t *testing.T) {
	seedSample(t)
	routes, _ := sampleRoutes()
	want := shape(routes)
	for _, path := range []string{"/goto/routes", "/goto/apis", "/goto/routes/", "/goto/apis/"} {
		if got := decode(t, serve(t, "GET", path)); !reflect.DeepEqual(got, want) {
			t.Errorf("GET %s = %v, want %v", path, got, want)
		}
	}
	if rr := serve(t, "POST", "/goto/routes"); rr.Code == http.StatusOK {
		t.Error("routes are only given for GET")
	}
}

func TestRoutesAreGivenAsYAMLOrText(t *testing.T) {
	seedSample(t)
	rr := serve(t, "GET", "/goto/routes", "Accept", "application/yaml")
	if ct := rr.Header().Get(constants.HeaderContentType); ct != constants.ContentTypeYAML {
		t.Errorf("content type = %q, want %q", ct, constants.ContentTypeYAML)
	}
	var fromYAML shaped
	if err := yaml.Unmarshal(rr.Body.Bytes(), &fromYAML); err != nil {
		t.Fatalf("body %q is not YAML: %v", rr.Body.String(), err)
	}
	if routes, _ := sampleRoutes(); !reflect.DeepEqual(fromYAML, shape(routes)) {
		t.Errorf("YAML routes = %v, want %v", fromYAML, shape(routes))
	}

	rr = serve(t, "GET", "/goto/routes", "Accept", "text/plain")
	// A pair is a line of its own, and what has no pairs is a line of its own, all sorted.
	want := strings.Join([]string{
		"/goto/version [GET]",
		"/lonely",
		"/server/bare",
		"/server/listeners/add [POST PUT]",
		"/server/probes/set?uri={uri} [POST]",
	}, "\n") + "\n"
	if rr.Body.String() != want {
		t.Errorf("text routes =\n%s\nwant\n%s", rr.Body.String(), want)
	}
}

func TestRoutesLevel(t *testing.T) {
	seedSample(t)
	full, _ := sampleRoutes()
	// Level 1 is the roots, 2 adds the sub roots, 3 the sub paths, and 4 the routes.
	level1 := APIRoutes{"/server": {}, "/goto": {}, "/lonely": {}}
	level2 := APIRoutes{
		"/server": {"/server/listeners": {}, "/server/probes": {}, "/server/bare": {}},
		"/goto":   {"/goto/version": {}},
		"/lonely": {},
	}
	level3 := APIRoutes{
		"/server": {
			"/server/listeners": {"/server/listeners/add": {}},
			"/server/probes":    {"/server/probes/set": {}},
			"/server/bare":      {},
		},
		"/goto":   {"/goto/version": {"": {}}},
		"/lonely": {},
	}
	for level, want := range map[string]APIRoutes{"1": level1, "2": level2, "3": level3, "4": full, "9": full} {
		got := decode(t, serve(t, "GET", "/goto/routes/level="+level))
		if !reflect.DeepEqual(got, shape(want)) {
			t.Errorf("level %s = %v, want %v", level, got, want)
		}
	}
	// Level 0 is all of it.
	if got := decode(t, serve(t, "GET", "/goto/apis/level=0")); !reflect.DeepEqual(got, shape(full)) {
		t.Errorf("level 0 = %v, want all routes", got)
	}
}

func TestRoutesQuery(t *testing.T) {
	seedSample(t)
	tests := []struct {
		query string
		want  APIRoutes
	}{
		// A root that matches gives everything under it.
		{"server", APIRoutes{"/server": {
			"/server/listeners": {"/server/listeners/add": {{Left: "/server/listeners/add", Right: []string{"POST", "PUT"}}}},
			"/server/probes":    {"/server/probes/set": {{Left: "/server/probes/set?uri={uri}", Right: []string{"POST"}}}},
			"/server/bare":      {},
		}}},
		// So does a sub root.
		{"probes", APIRoutes{"/server": {
			"/server/probes": {"/server/probes/set": {{Left: "/server/probes/set?uri={uri}", Right: []string{"POST"}}}},
		}}},
		// A sub path that matches, when its parents don't, comes with them.
		{"add", APIRoutes{"/server": {
			"/server/listeners": {"/server/listeners/add": {{Left: "/server/listeners/add", Right: []string{"POST", "PUT"}}}},
		}}},
		{"nothing-matches-this", APIRoutes{}},
	}
	for _, tt := range tests {
		got := decode(t, serve(t, "GET", "/goto/routes?q="+tt.query))
		if !reflect.DeepEqual(got, shape(tt.want)) {
			t.Errorf("q=%s = %v, want %v", tt.query, got, tt.want)
		}
	}
}

func TestRoutesWithURLGiveCurlCommands(t *testing.T) {
	seedSample(t)
	got := decode(t, serve(t, "GET", "/goto/routes?q=server&url=http://h:8080"))
	want := APIRoutes{"/server": {
		"/server/listeners": {"/server/listeners/add": {{Left: "/server/listeners/add", Right: []string{
			"curl -v -XPOST http://h:8080/server/listeners/add", "curl -v -XPUT http://h:8080/server/listeners/add"}}}},
		// The characters of the query that the shell takes are escaped.
		"/server/probes": {"/server/probes/set": {{Left: "/server/probes/set?uri={uri}", Right: []string{
			`curl -v -XPOST http://h:8080/server/probes/set\?uri={uri}`}}}},
		"/server/bare": {},
	}}
	if !reflect.DeepEqual(got, shape(want)) {
		t.Errorf("curls = %v, want %v", got, shape(want))
	}
	// The cached routes are not changed by giving them curls.
	if wantRoutes, wantCurls := sampleRoutes(); !reflect.DeepEqual(routes, wantRoutes) || !reflect.DeepEqual(routesWithCurl, wantCurls) {
		t.Error("giving the routes as curls changed the cached routes")
	}
}

func TestRoutesAreLoadedWhenNoneAreCached(t *testing.T) {
	seed(t, APIRoutes{}, APIRoutes{})
	core := mux.NewRouter()
	core.HandleFunc("/loaded/first/second", func(http.ResponseWriter, *http.Request) {}).Methods("GET")
	useCoreRouter(t, core)
	got := decode(t, serve(t, "GET", "/goto/routes?q=loaded"))
	want := APIRoutes{"/loaded": {"/loaded/first": {"/loaded/first/second": {{Left: "/loaded/first/second", Right: []string{"GET"}}}}}}
	if !reflect.DeepEqual(got, shape(want)) {
		t.Errorf("routes = %v, want %v", got, shape(want))
	}
}

func TestRefreshReplacesTheCachedRoutes(t *testing.T) {
	r, c := sampleRoutes()
	seed(t, r, c)
	core := mux.NewRouter()
	core.HandleFunc("/brandnew/one/two", func(http.ResponseWriter, *http.Request) {}).Methods("GET")
	useCoreRouter(t, core)
	// Not refreshed, the cached routes are given.
	if got := decode(t, serve(t, "GET", "/goto/routes?q=brandnew")); len(got) != 0 {
		t.Fatalf("routes = %v, want the cache to be used without a refresh", got)
	}
	refreshed := decode(t, serve(t, "GET", "/goto/routes/refresh"))
	if _, ok := refreshed["/brandnew"]; !ok {
		t.Errorf("refreshed routes = %v, want /brandnew", refreshed)
	}
	if _, ok := refreshed["/lonely"]; ok {
		t.Errorf("refreshed routes = %v, want the stale /lonely to be gone", refreshed)
	}
	// What was refreshed is cached.
	if got := decode(t, serve(t, "GET", "/goto/apis?q=brandnew")); len(got) != 1 {
		t.Errorf("routes = %v, want /brandnew cached", got)
	}
}

func TestLoadAPIsFromRouterMakesRoutesAndCurls(t *testing.T) {
	seed(t, APIRoutes{}, APIRoutes{})
	h := func(http.ResponseWriter, *http.Request) {}
	r := mux.NewRouter()
	r.HandleFunc("/alpha/beta/gamma", h).Methods("GET", "POST")
	r.HandleFunc("/alpha/beta", h).Methods("GET")
	r.HandleFunc("/alpha", h).Methods("PUT")
	r.HandleFunc("/nomethods/x/y", h)
	r.HandleFunc("/", h).Methods("GET")
	r.HandleFunc("/q/one/two", h).Methods("GET").Queries("a", "{a}", "b", "{b}")
	r.PathPrefix("/prefix").Subrouter().HandleFunc("/sub/leaf", h).Methods("DELETE")
	loadAPIsFromRouter(r)

	wantRoutes := APIRoutes{
		"/alpha": {
			"/alpha/beta": {
				"/alpha/beta/gamma": {{Left: "/alpha/beta/gamma", Right: []string{"GET", "POST"}}},
				"":                  {{Left: "/alpha/beta", Right: []string{"GET"}}},
			},
			"": {"": {{Left: "/alpha", Right: []string{"PUT"}}}},
		},
		"/q":      {"/q/one": {"/q/one/two": {{Left: "/q/one/two?a={a}&b={b}", Right: []string{"GET"}}}}},
		"/prefix": {"/prefix/sub": {"/prefix/sub/leaf": {{Left: "/prefix/sub/leaf", Right: []string{"DELETE"}}}}},
	}
	if !reflect.DeepEqual(routes, wantRoutes) {
		t.Errorf("routes = %v, want %v", routes, wantRoutes)
	}
	wantCurls := APIRoutes{
		"/alpha": {
			"/alpha/beta": {
				"/alpha/beta/gamma": {{Left: "/alpha/beta/gamma", Right: []string{"curl -v -XGET %s/alpha/beta/gamma", "curl -v -XPOST %s/alpha/beta/gamma"}}},
				"":                  {{Left: "/alpha/beta", Right: []string{"curl -v -XGET %s/alpha/beta"}}},
			},
			"": {"": {{Left: "/alpha", Right: []string{"curl -v -XPUT %s/alpha"}}}},
		},
		"/q":      {"/q/one": {"/q/one/two": {{Left: "/q/one/two?a={a}&b={b}", Right: []string{"curl -v -XGET %s/q/one/two?a={a}&b={b}"}}}}},
		"/prefix": {"/prefix/sub": {"/prefix/sub/leaf": {{Left: "/prefix/sub/leaf", Right: []string{"curl -v -XDELETE %s/prefix/sub/leaf"}}}}},
	}
	if !reflect.DeepEqual(routesWithCurl, wantCurls) {
		t.Errorf("curls = %v, want %v", routesWithCurl, wantCurls)
	}
}

func TestLoadAPIsFromRouterReportsTrailingSlashRoutesOnce(t *testing.T) {
	seed(t, APIRoutes{}, APIRoutes{})
	h := func(http.ResponseWriter, *http.Request) {}
	r := mux.NewRouter()
	r.HandleFunc("/alpha/beta/gamma", h).Methods("GET")
	r.HandleFunc("/alpha/beta/gamma/", h).Methods("GET")
	r.HandleFunc("/alpha/beta", h).Methods("GET")
	r.HandleFunc("/alpha/beta/", h).Methods("GET")
	// Different methods of the same path are different routes.
	r.HandleFunc("/alpha/beta/gamma", h).Methods("POST")
	r.HandleFunc("/alpha/beta/gamma/", h).Methods("POST")
	loadAPIsFromRouter(r)

	wantRoutes := APIRoutes{"/alpha": {"/alpha/beta": {
		"/alpha/beta/gamma": {
			{Left: "/alpha/beta/gamma", Right: []string{"GET"}},
			{Left: "/alpha/beta/gamma", Right: []string{"POST"}},
		},
		"": {{Left: "/alpha/beta", Right: []string{"GET"}}},
	}}}
	if !reflect.DeepEqual(routes, wantRoutes) {
		t.Errorf("routes = %v, want %v", routes, wantRoutes)
	}
	if len(routesWithCurl["/alpha"]["/alpha/beta"]["/alpha/beta/gamma"]) != 2 {
		t.Errorf("curls = %v, want 2 without the trailing slash duplicates", routesWithCurl)
	}
}

func TestPrintRoutesWritesToTheGivenWriter(t *testing.T) {
	seedSample(t)
	rr := httptest.NewRecorder()
	PrintRoutes(rr, 1, "", "", false, true, false)
	if want := "/goto\n/lonely\n/server\n"; rr.Body.String() != want {
		t.Errorf("text routes = %q, want %q", rr.Body.String(), want)
	}
	rr = httptest.NewRecorder()
	PrintRoutes(rr, 1, "", "", false, false, true)
	var got shaped
	if err := yaml.Unmarshal(rr.Body.Bytes(), &got); err != nil || len(got) != 3 {
		t.Errorf("YAML routes = %q (%v), want 3 roots", rr.Body.String(), err)
	}
}

func TestFilterAPIsKeepsTheCachedRoutesApart(t *testing.T) {
	seedSample(t)
	out := filterAPIs("")
	delete(out, "/goto")
	out["/server"]["/new"] = map[string][]pair{}
	if want, _ := sampleRoutes(); !reflect.DeepEqual(routes, want) {
		t.Error("changing the filtered routes changed the cached ones")
	}
}

func TestTrimAPIsKeepsTheGivenRoutesApart(t *testing.T) {
	full, _ := sampleRoutes()
	trimmed := trimAPIs(full, 3)
	trimmed["/server"]["/server/listeners"]["/server/listeners/add"] = append(trimmed["/server"]["/server/listeners"]["/server/listeners/add"], pair{Left: "x"})
	if want, _ := sampleRoutes(); !reflect.DeepEqual(full, want) {
		t.Error("trimming changed the routes that were given")
	}
}
