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
	"sync"
	"testing"

	"goto/pkg/global"
	"goto/pkg/server/middleware"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var rpcRoutesOnce sync.Once

// setRPCRoutes sets the routes of all the APIs, with the functions of the server that the requests need stubbed.
func setRPCRoutes() {
	rpcRoutesOnce.Do(func() {
		global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
		global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
		global.Funcs.IsListenerPresent = func(int) bool { return false }
		global.Funcs.IsListenerOpen = func(int) bool { return false }
		global.Funcs.GetListenerID = func(int) string { return "" }
		global.Funcs.GetListenerLabel = func(*http.Request) string { return "" }
		global.Funcs.GetListenerLabelForPort = func(int) string { return "" }
		util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
		if util.MatchRouter == nil {
			util.CreateRouters(mux.NewRouter())
		}
		middleware.SetRoutesOnly(mux.NewRouter())
	})
}

// serveAdmin gives a request to the router of its root path, as the server does, once the routes of all the APIs are set.
func serveAdmin(t *testing.T, method, uri string) *httptest.ResponseRecorder {
	t.Helper()
	setRPCRoutes()
	r := httptest.NewRequest(method, uri, nil)
	r = r.WithContext(context.WithValue(r.Context(), util.CurrentPortKey, 8080))
	_, r, _ = util.WithRequestStore(r, nil)
	root, _ := util.GetRootURI(r.RequestURI)
	router := middleware.RootRouterFor(root, r)
	rr := httptest.NewRecorder()
	if router == nil {
		http.NotFound(rr, r)
		return rr
	}
	router.ServeHTTP(rr, r)
	return rr
}

// The APIs of the services under /grpc/server (and /jsonrpc/server) were on a router of a pattern that no request is given
// to, as a request goes to the router of the first segment of its path: those for the payloads of the methods, and for
// tracking, were never reached.
func TestRPCServiceAPIsAreRoutedToTheirHandlers(t *testing.T) {
	for _, root := range []string{"/grpc", "/jsonrpc"} {
		for _, tt := range []struct{ method, path string }{
			{"POST", "/server/NoSuchService/NoSuchMethod/payload"},
			{"POST", "/server/NoSuchService/NoSuchMethod/payload/clear"},
			{"POST", "/server/NoSuchService/track"},
			{"GET", "/server/NoSuchService/tracking"},
			{"GET", "/server/NoSuchService"},
		} {
			rr := serveAdmin(t, tt.method, root+tt.path)
			// The handlers answer a service that is not there with a message that says so. A route that is not there
			// answers with the not found of the router, which has no such message.
			if !strings.Contains(rr.Body.String(), "NoSuchService") {
				t.Errorf("%s %s%s = %d %q, want the handler of the API to answer for the service", tt.method, root, tt.path, rr.Code, rr.Body.String())
			}
		}
	}
}

// The routes of the services take any name for a service, so they must come after the ones of the gRPC server that have
// a fixed name under the same path, which they would answer for with a service that is not there.
func TestRPCServiceRoutesDontTakeTheAPIsOfTheGRPCServer(t *testing.T) {
	for _, path := range []string{"/grpc/server/services", "/grpc/server/services/active"} {
		rr := serveAdmin(t, "GET", path)
		if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), "not found") {
			t.Errorf("GET %s = %d %q, want the API of the gRPC server", path, rr.Code, rr.Body.String())
		}
	}
}

// Requests under /jsonrpc that are not for an API are not the business of its router, and are handled as any other.
func TestJSONRPCRouterTakesOnlyTheAPIs(t *testing.T) {
	setRPCRoutes()
	for uri, want := range map[string]bool{
		"/jsonrpc": false, "/jsonrpc/": false, "/jsonrpc/other": false, "/jsonrpcx": false,
		"/jsonrpc/server": true, "/jsonrpc/server/Calc/tracking": true, "/jsonrpc/server?x=1": true,
		"/jsonrpc/services/add/Calc": true, "/jsonrpc/services": true,
	} {
		r := httptest.NewRequest("POST", uri, nil)
		root, _ := util.GetRootURI(r.RequestURI)
		if got := middleware.RootRouterFor(root, r) != nil; got != want {
			t.Errorf("%s is given to a router of jsonrpc: %v, want %v", uri, got, want)
		}
	}
}
