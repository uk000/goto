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

package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"goto/pkg/global"
	"goto/pkg/server/middleware"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

func routerFor(t *testing.T, host, uri string) *mux.Router {
	t.Helper()
	r := httptest.NewRequest("GET", uri, nil)
	r.Host = host
	r = r.WithContext(context.WithValue(r.Context(), util.CurrentPortKey, 8080))
	_, r, rs := util.WithRequestStore(r, nil)
	loadRouter(r, rs)
	return rs.CurrentRouter
}

// A root path that has a guard is served by its router only for the requests that the guard allows, and the others go to
// the core router, which is where the requests of any URI that has no router of its own go to, and is what leads them to
// the rest of the rules of goto.
func TestLoadRouterLeavesTheRequestsThatAGuardDoesNotAllowToTheCoreRouter(t *testing.T) {
	global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsListenerPresent = func(int) bool { return false }
	global.Funcs.IsListenerOpen = func(int) bool { return false }
	global.Funcs.GetListenerID = func(int) string { return "" }
	global.Funcs.GetListenerLabel = func(*http.Request) string { return "" }
	global.Funcs.GetListenerLabelForPort = func(int) string { return "" }
	util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }

	oldCore := coreRouter
	coreRouter = mux.NewRouter()
	t.Cleanup(func() { coreRouter = oldCore })

	const guarded, open = "/guardedroot", "/openroot"
	guardedRouter := middleware.RootPathIf(guarded, func(r *http.Request) bool { return strings.HasPrefix(r.Host, "k8s.") })
	openRouter := middleware.RootPath(open)
	t.Cleanup(func() {
		delete(middleware.RootRouters, guarded)
		delete(middleware.RootGuards, guarded)
		delete(middleware.RootRouters, open)
	})

	for _, uri := range []string{guarded, guarded + "/x/y"} {
		if got := routerFor(t, "k8s.example.com", uri); got != guardedRouter {
			t.Errorf("%s for a host that the guard allows: router = %p, want the router of the root path %p", uri, got, guardedRouter)
		}
		for _, host := range []string{"example.com", "app.example.com:8080", ""} {
			if got := routerFor(t, host, uri); got != coreRouter {
				t.Errorf("%s for host %q that the guard does not allow: router = %p, want the core router %p", uri, host, got, coreRouter)
			}
		}
	}
	// A root path without a guard is served for all the hosts, and a URI without a router is left to the core router.
	for _, host := range []string{"k8s.example.com", "example.com", ""} {
		if got := routerFor(t, host, open+"/x"); got != openRouter {
			t.Errorf("%s for host %q: router = %p, want the router of the root path %p", open, host, got, openRouter)
		}
		if got := routerFor(t, host, "/norouter/x"); got != coreRouter {
			t.Errorf("a URI that has no router for host %q: router = %p, want the core router %p", host, got, coreRouter)
		}
	}
}
