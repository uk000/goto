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

// The APIs of the TCP and UDP servers were on routers of the root paths /server/tcp and /server/udp, which no request is
// given to, as a request goes to the router of the first segment of its path, which is /server: they were all a 404.
func TestTCPAndUDPServerAPIsAreRoutedToTheirHandlers(t *testing.T) {
	for _, tt := range []struct{ method, uri string }{
		{"GET", "/server/tcp/active"},
		{"GET", "/server/tcp/history"},
		{"POST", "/server/tcp/9999/configure"},
		{"POST", "/server/tcp/9999/mode/stream=y"},
		{"POST", "/server/udp/9999/proxy/127.0.0.1:9"},
		{"POST", "/server/udp/9999/stop/127.0.0.1:9"},
	} {
		rr := serveAdmin(t, tt.method, tt.uri)
		// Whatever the handler answers for a port that is not a listener, the router answers a route that it does not
		// have with its page not found.
		if strings.Contains(rr.Body.String(), "404 page not found") {
			t.Errorf("%s %s = %d %q, want the handler of the API to answer", tt.method, tt.uri, rr.Code, rr.Body.String())
		}
	}
}

// The APIs of the k8s yaml and of the registry of agents were on routers of the root paths /k8s/yaml and /ai/agents,
// which no request is given to, as a request goes to the router of the first segment of its path. Those of the agents
// were not among the routes that the server sets at all.
func TestK8sYamlAndAgentsAPIsAreRoutedToTheirHandlers(t *testing.T) {
	for _, tt := range []struct{ method, uri string }{
		{"GET", "/k8s/yaml"},
		{"GET", "/k8s/yaml/nosuchyaml"},
		{"POST", "/k8s/yaml/clear"},
		{"POST", "/k8s/yaml/apply/nosuchyaml"},
		{"GET", "/ai/agents"},
		{"POST", "/ai/agents/clear"},
		{"POST", "/ai/agents/add"},
	} {
		rr := serveAdmin(t, tt.method, tt.uri)
		if strings.Contains(rr.Body.String(), "404 page not found") {
			t.Errorf("%s %s = %d %q, want the handler of the API to answer", tt.method, tt.uri, rr.Code, rr.Body.String())
		}
	}
	// The routes that were already there under /k8s are still served.
	if rr := serveAdmin(t, "POST", "/k8s/clear"); rr.Code != http.StatusOK {
		t.Errorf("POST /k8s/clear = %d %q", rr.Code, rr.Body.String())
	}
}

// Only the requests for the API of the agents are given to the router of /ai, and the others are handled as any other.
func TestAIRouterTakesOnlyTheAgentsAPI(t *testing.T) {
	setRPCRoutes()
	for uri, want := range map[string]bool{
		"/ai": false, "/ai/": false, "/ai/other": false, "/ai/agentsx": false, "/aix/agents": false,
		"/ai/agents": true, "/ai/agents/add": true, "/ai/agents?x=1": true,
	} {
		r := httptest.NewRequest("GET", uri, nil)
		root, _ := util.GetRootURI(r.RequestURI)
		if got := middleware.RootRouterFor(root, r) != nil; got != want {
			t.Errorf("%s is given to a router: %v, want %v", uri, got, want)
		}
	}
}
