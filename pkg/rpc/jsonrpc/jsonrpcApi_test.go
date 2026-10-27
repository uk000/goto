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

package jsonrpc

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"goto/pkg/server/middleware"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

const apiRoot = "/jsonrpc/services"

var routesOnce sync.Once

// apiEnv serves the APIs of the package on a port of their own, which is also the port of the registry that they change.
type apiEnv struct {
	t      *testing.T
	port   int
	router *mux.Router
}

func newAPIEnv(t *testing.T) *apiEnv {
	t.Helper()
	routesOnce.Do(func() {
		stubOnce.Do(stubGlobalFuncs)
		setRoutes(nil)
	})
	// The server gives a request to the router of the first segment of its path, which is /jsonrpc.
	e := &apiEnv{t: t, port: uniquePort(), router: middleware.RootRouters["/jsonrpc"]}
	if e.router == nil {
		t.Fatalf("the routes of %s are not registered", apiRoot)
	}
	t.Cleanup(func() {
		lock.Lock()
		delete(PortRegistry, e.port)
		lock.Unlock()
	})
	return e
}

func (e *apiEnv) request(method, target, reqBody string) *http.Request {
	var req *http.Request
	if reqBody == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(reqBody))
	}
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, e.port))
	_, req, _ = util.WithRequestStore(req, nil)
	return req
}

func (e *apiEnv) do(method, target, reqBody string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	e.router.ServeHTTP(rr, e.request(method, target, reqBody))
	return rr
}

func (e *apiEnv) registry() *JSONRPCServiceRegistry { return GetPortRegistry(e.port) }

func TestAddServiceFromBody(t *testing.T) {
	e := newAPIEnv(t)
	rr := e.do("POST", apiRoot+"/add/Calc", calcBody)
	if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != "Registered JSONRPCService: Calc with 3 methods" {
		t.Fatalf("add = %d %q", rr.Code, rr.Body.String())
	}
	s := e.registry().GetService("Calc")
	if s == nil || s.GetMethodCount() != 3 || s.URI != "/calc" {
		t.Errorf("service in the registry of the port = %+v", s)
	}
}

func TestAddServiceIsForThePortOfTheRequest(t *testing.T) {
	e1, e2 := newAPIEnv(t), newAPIEnv(t)
	if rr := e1.do("POST", apiRoot+"/add/Calc", calcBody); rr.Code != http.StatusOK {
		t.Fatalf("add = %d %q", rr.Code, rr.Body.String())
	}
	if e1.registry().GetService("Calc") == nil || e2.registry().GetService("Calc") != nil {
		t.Errorf("a service must be added to the registry of the port that the request came on")
	}
}

func TestAddServiceFromGRPC(t *testing.T) {
	e := newAPIEnv(t)
	g := grpcHealth(t)
	rr := e.do("POST", apiRoot+"/add/"+g.Name+"?fromGRPC=true", "")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Registered JSONRPCService: "+g.Name+" with 3 methods") {
		t.Fatalf("add = %d %q", rr.Code, rr.Body.String())
	}
	s := e.registry().GetService(g.Name)
	if s == nil || !s.HasMethod("Check") || !s.HasMethod("Watch") {
		t.Errorf("service = %+v", s)
	}
	// the flag decides: the body isn't read, and without the flag a body is needed
	if rr := e.do("POST", apiRoot+"/add/"+g.Name, ""); rr.Code != http.StatusInternalServerError {
		t.Errorf("add without a body and without the flag = %d %q, want 500", rr.Code, rr.Body.String())
	}
}

func TestAddServiceFailures(t *testing.T) {
	e := newAPIEnv(t)
	tests := []struct {
		name, target, body string
		want               string
	}{
		{"unknown gRPC service", "/add/no.such.Service?fromGRPC=true", "", "No GRPCService found for name: no.such.Service"},
		{"bad body", "/add/Calc", `{"name":`, "Failed to register service [Calc]"},
		{"body without a name", "/add/Calc", `{"methods":{}}`, "No name"},
		{"no body", "/add/Calc", "", "Failed to register service [Calc]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := e.do("POST", apiRoot+tt.target, tt.body)
			if rr.Code != http.StatusInternalServerError || !strings.Contains(rr.Body.String(), tt.want) {
				t.Errorf("add = %d %q, want 500 with %q", rr.Code, rr.Body.String(), tt.want)
			}
		})
	}
	if n := len(e.registry().Services); n != 0 {
		t.Errorf("services after failures = %d, want 0", n)
	}
}

func TestAddServiceWithoutANameIsRefused(t *testing.T) {
	e := newAPIEnv(t)
	req := mux.SetURLVars(e.request("POST", apiRoot+"/add/", calcBody), map[string]string{"service": ""})
	rr := httptest.NewRecorder()
	addService(rr, req)
	if rr.Code != http.StatusBadRequest || strings.TrimSpace(rr.Body.String()) != "No name" {
		t.Errorf("add = %d %q, want 400 No name", rr.Code, rr.Body.String())
	}
	if n := len(e.registry().Services); n != 0 {
		t.Errorf("services = %d, want 0", n)
	}
}

func TestRemoveService(t *testing.T) {
	for _, target := range []string{"/remove/Calc", "/Calc/remove"} {
		t.Run(target, func(t *testing.T) {
			e := newAPIEnv(t)
			e.do("POST", apiRoot+"/add/Calc", calcBody)
			e.do("POST", apiRoot+"/add/Other", `{"name":"Other"}`)
			rr := e.do("POST", apiRoot+target, "")
			if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != "JSONRPCService [Calc] removed" {
				t.Errorf("remove = %d %q", rr.Code, rr.Body.String())
			}
			if e.registry().GetService("Calc") != nil || e.registry().GetService("Other") == nil {
				t.Errorf("only the service that was asked for must be removed, got %v", e.registry().Services)
			}
			// removing a service that isn't there is not an error
			if rr := e.do("POST", apiRoot+target, ""); rr.Code != http.StatusOK {
				t.Errorf("remove of a removed service = %d %q", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestRemoveServiceWithoutANameIsRefused(t *testing.T) {
	e := newAPIEnv(t)
	req := mux.SetURLVars(e.request("POST", apiRoot+"/remove/", ""), map[string]string{"service": ""})
	rr := httptest.NewRecorder()
	removeService(rr, req)
	if rr.Code != http.StatusBadRequest || strings.TrimSpace(rr.Body.String()) != "No name" {
		t.Errorf("remove = %d %q, want 400 No name", rr.Code, rr.Body.String())
	}
}

func TestRemoveAllServices(t *testing.T) {
	e := newAPIEnv(t)
	e.do("POST", apiRoot+"/add/Calc", calcBody)
	e.do("POST", apiRoot+"/add/Other", `{"name":"Other"}`)
	if n := len(e.registry().Services); n != 2 {
		t.Fatalf("services = %d, want 2", n)
	}
	rr := e.do("POST", apiRoot+"/clear", "")
	if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != "All services removed" {
		t.Errorf("clear = %d %q", rr.Code, rr.Body.String())
	}
	if n := len(e.registry().Services); n != 0 {
		t.Errorf("services after clear = %d, want 0", n)
	}
	if rr := e.do("POST", apiRoot+"/clear", ""); rr.Code != http.StatusOK {
		t.Errorf("clear of an empty registry = %d", rr.Code)
	}
}

func TestAPIsOnlyAnswerPost(t *testing.T) {
	e := newAPIEnv(t)
	for _, target := range []string{"/add/Calc", "/remove/Calc", "/Calc/remove", "/clear"} {
		rr := e.do("GET", apiRoot+target, "")
		if rr.Code == http.StatusOK {
			t.Errorf("GET %s = %d, want it to be refused", target, rr.Code)
		}
	}
	if rr := e.do("POST", apiRoot+"/nosuchapi", ""); rr.Code != http.StatusNotFound {
		t.Errorf("unknown API = %d, want 404", rr.Code)
	}
}

// Services are added, removed and cleared by requests that come at the same time. Run with -race.
func TestAPIsConcurrentUse(t *testing.T) {
	e := newAPIEnv(t)
	const clients, perClient = 8, 15
	reqs := make([][]*http.Request, clients)
	for c := range reqs {
		for i := 0; i < perClient; i++ {
			name := fmt.Sprintf("svc-%d-%d", c, i)
			reqs[c] = append(reqs[c], e.request("POST", apiRoot+"/add/"+name, fmt.Sprintf(`{"name":%q,"methods":{"m":{}}}`, name)))
		}
	}
	codes := make(chan int, clients*perClient)
	var wg sync.WaitGroup
	for c := range reqs {
		c := c
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, req := range reqs[c] {
				rr := httptest.NewRecorder()
				e.router.ServeHTTP(rr, req)
				codes <- rr.Code
			}
		}()
	}
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != http.StatusOK {
			t.Errorf("add = %d, want 200", code)
		}
	}
	if n := len(e.registry().Services); n != clients*perClient {
		t.Errorf("services = %d, want %d", n, clients*perClient)
	}
}
