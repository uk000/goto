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

package response

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"goto/pkg/global"
	"goto/pkg/server/middleware"
	"goto/pkg/server/response/delay"
	"goto/pkg/server/response/header"
	"goto/pkg/server/response/payload"
	"goto/pkg/server/response/status"
	"goto/pkg/server/response/trigger"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var (
	stubOnce  sync.Once
	routeOnce sync.Once
	// Payloads, delays and the rest are kept per port, so every test uses a port of its own.
	portSeq atomic.Int32
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

// Routes are registered on global routers, so they are registered only once, and tests don't run in parallel.
func setupRoutes() {
	stubOnce.Do(stubGlobalFuncs)
	routeOnce.Do(func() {
		util.CreateRouters(mux.NewRouter())
		Middleware.SetRoutes(mux.NewRouter())
	})
}

type testEnv struct {
	port      int
	nextCalls atomic.Int32
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	setupRoutes()
	e := &testEnv{port: int(47000 + portSeq.Add(1))}
	t.Cleanup(func() {
		e.serve("/server", "POST", "/server/response/payload/clear", "")
	})
	return e
}

func (e *testEnv) newRequest(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, e.port))
	_, req, _ = util.WithRequestStore(req, nil)
	return req
}

// serve sends a request to one of the routers that the routes are registered on.
func (e *testEnv) serve(root, method, target, body string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	router := middleware.RootRouters[root]
	if router == nil {
		rr.WriteHeader(http.StatusNotImplemented)
		return rr
	}
	router.ServeHTTP(rr, e.newRequest(method, target, body))
	return rr
}

func (e *testEnv) next() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.nextCalls.Add(1)
		fmt.Fprint(w, "next")
	})
}

func TestMiddlewareDefinition(t *testing.T) {
	if Middleware == nil || Middleware.Name != "response" || Middleware.SetRoutes == nil || Middleware.MiddlewareHandler == nil {
		t.Fatalf("Middleware = %+v", Middleware)
	}
	if len(CoreMiddlewares) != 3 || CoreMiddlewares[0] != status.Middleware || CoreMiddlewares[1] != delay.Middleware || CoreMiddlewares[2] != header.Middleware {
		t.Errorf("core middlewares = %v, want status, delay and header", CoreMiddlewares)
	}
	if len(responseMiddlewares) != 2 || responseMiddlewares[0] != payload.Middleware || responseMiddlewares[1] != trigger.Middleware {
		t.Errorf("response middlewares = %v, want payload and trigger", responseMiddlewares)
	}
}

func TestResponseAPIsAreServedUnderServerAndUnderRoot(t *testing.T) {
	e := newTestEnv(t)
	for _, root := range []struct{ router, prefix string }{{"/server", "/server/response"}, {"/response", "/response"}} {
		for _, api := range []string{"payload", "delay", "headers", "status", "triggers"} {
			target := root.prefix + "/" + api
			rr := e.serve(root.router, "GET", target, "")
			if rr.Code != http.StatusOK {
				t.Errorf("GET %s = %d %q, want 200", target, rr.Code, rr.Body.String())
				continue
			}
			// The listing of headers is not JSON when there are none.
			if api != "headers" && !json.Valid(rr.Body.Bytes()) {
				t.Errorf("GET %s answered %q, which is not JSON", target, rr.Body.String())
			}
		}
	}
}

func TestPayloadsAreConfiguredThroughBothPrefixes(t *testing.T) {
	e := newTestEnv(t)
	rr := e.serve("/server", "POST", "/server/response/payload/set/default", "set under server")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Default Payload set") {
		t.Fatalf("POST under /server/response = %d %q", rr.Code, rr.Body.String())
	}
	// The state is the same whichever way it is read.
	for _, root := range []struct{ router, prefix string }{{"/server", "/server/response"}, {"/response", "/response"}} {
		rr := e.serve(root.router, "GET", root.prefix+"/payload", "")
		if !strings.Contains(rr.Body.String(), "set under server") {
			t.Errorf("GET %s/payload = %q, want the payload that was set", root.prefix, rr.Body.String())
		}
	}
	rr = e.serve("/response", "POST", "/response/payload/clear", "")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Response Payload Cleared") {
		t.Fatalf("POST /response/payload/clear = %d %q", rr.Code, rr.Body.String())
	}
	rr = e.serve("/server", "GET", "/server/response/payload", "")
	if strings.Contains(rr.Body.String(), "set under server") {
		t.Errorf("the payload is still listed after it was cleared: %q", rr.Body.String())
	}
}

func TestDelaysHeadersAndStatusesAreConfiguredThroughTheRoutes(t *testing.T) {
	e := newTestEnv(t)
	t.Cleanup(func() {
		e.serve("/server", "POST", "/server/response/delay/clear", "")
		e.serve("/server", "POST", "/server/response/headers/clear", "")
		e.serve("/server", "POST", "/server/response/status/clear", "")
	})
	if rr := e.serve("/server", "POST", "/server/response/delay/set/3ms", ""); rr.Code != http.StatusOK {
		t.Errorf("setting a delay = %d %q", rr.Code, rr.Body.String())
	}
	if rr := e.serve("/response", "GET", "/response/delay", ""); !strings.Contains(rr.Body.String(), "3000000") {
		t.Errorf("GET /response/delay = %q, want the delay that was set", rr.Body.String())
	}
	if rr := e.serve("/server", "POST", "/server/response/headers/add/x-response-test=yes", ""); rr.Code != http.StatusOK {
		t.Errorf("adding a header = %d %q", rr.Code, rr.Body.String())
	}
	if rr := e.serve("/response", "GET", "/response/headers", ""); !strings.Contains(strings.ToLower(rr.Body.String()), "x-response-test") {
		t.Errorf("GET /response/headers = %q, want the header that was added", rr.Body.String())
	}
	if rr := e.serve("/server", "POST", "/server/response/status/set/418", ""); rr.Code != http.StatusOK {
		t.Errorf("setting a status = %d %q", rr.Code, rr.Body.String())
	}
	if rr := e.serve("/response", "GET", "/response/status", ""); !strings.Contains(rr.Body.String(), "418") {
		t.Errorf("GET /response/status = %q, want the status that was set", rr.Body.String())
	}
}

func TestTriggersAreListedThroughBothPrefixes(t *testing.T) {
	e := newTestEnv(t)
	for _, root := range []struct{ router, prefix string }{{"/server", "/server/response"}, {"/response", "/response"}} {
		rr := e.serve(root.router, "GET", root.prefix+"/triggers/counts", "")
		if rr.Code != http.StatusOK || !json.Valid(rr.Body.Bytes()) {
			t.Errorf("GET %s/triggers/counts = %d %q", root.prefix, rr.Code, rr.Body.String())
		}
	}
}

func TestRoutesForTrafficAreRegistered(t *testing.T) {
	e := newTestEnv(t)
	for _, root := range []string{"/status", "/delay", "/sleep", "/server", "/response"} {
		if middleware.RootRouters[root] == nil {
			t.Errorf("no router is registered for %s", root)
		}
	}
	if rr := e.serve("/status", "GET", "/status/418", ""); rr.Code != http.StatusTeapot {
		t.Errorf("GET /status/418 = %d, want 418", rr.Code)
	}
	if rr := e.serve("/delay", "GET", "/delay/1ms", ""); rr.Code != http.StatusOK {
		t.Errorf("GET /delay/1ms = %d, want 200", rr.Code)
	}
	if rr := e.serve("/sleep", "GET", "/sleep/1ms", ""); rr.Code != http.StatusOK {
		t.Errorf("GET /sleep/1ms = %d, want 200", rr.Code)
	}
}

func TestRoutesAreRegisteredOnceForTheRouters(t *testing.T) {
	// Registering the routes again, as the server may do for more than one router, doesn't break the ones that exist.
	e := newTestEnv(t)
	Middleware.SetRoutes(mux.NewRouter())
	if rr := e.serve("/server", "GET", "/server/response/payload", ""); rr.Code != http.StatusOK {
		t.Errorf("GET /server/response/payload = %d after the routes were registered again", rr.Code)
	}
}

func TestUnknownResponseAPIsAreNotServed(t *testing.T) {
	e := newTestEnv(t)
	for _, target := range []string{"/server/response/nothing", "/server/response/payload/set/default/x/y/z", "/response/nothing"} {
		root := "/server"
		if strings.HasPrefix(target, "/response") {
			root = "/response"
		}
		if rr := e.serve(root, "DELETE", target, ""); rr.Code != http.StatusNotFound && rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("DELETE %s = %d, want 404 or 405", target, rr.Code)
		}
	}
}

func TestMiddlewareLetsRequestsThroughWhenThereIsNoPayload(t *testing.T) {
	e := newTestEnv(t)
	rr := httptest.NewRecorder()
	Middleware.MiddlewareHandler(e.next()).ServeHTTP(rr, e.newRequest("GET", "/some/traffic", ""))
	if rr.Body.String() != "next" || e.nextCalls.Load() != 1 {
		t.Errorf("response = %q after %d calls of the next handler, want it to answer once", rr.Body.String(), e.nextCalls.Load())
	}
}

func TestMiddlewareServesThePayloadInPlaceOfTheNextHandler(t *testing.T) {
	e := newTestEnv(t)
	e.serve("/server", "POST", "/server/response/payload/set/default", "the payload")
	handler := Middleware.MiddlewareHandler(e.next())
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, e.newRequest("GET", "/some/traffic", ""))
	if rr.Body.String() != "the payload" || e.nextCalls.Load() != 0 {
		t.Errorf("response = %q after %d calls of the next handler, want the payload alone", rr.Body.String(), e.nextCalls.Load())
	}
	// A request for a status is handed on after the payload, so that the status is applied.
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, e.newRequest("GET", "/status/200", ""))
	if rr.Body.String() != "the payloadnext" || e.nextCalls.Load() != 1 {
		t.Errorf("response = %q after %d calls of the next handler, want the payload and then the next handler's answer", rr.Body.String(), e.nextCalls.Load())
	}
	// Requests for the admin APIs are not given the payload.
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, e.newRequest("GET", "/server/response/payload", ""))
	if rr.Body.String() != "next" {
		t.Errorf("response for an admin API = %q, want the next handler's answer", rr.Body.String())
	}
}

func TestMiddlewareWithoutNextHandler(t *testing.T) {
	e := newTestEnv(t)
	handler := Middleware.MiddlewareHandler(nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, e.newRequest("GET", "/some/traffic", ""))
	if rr.Code != http.StatusOK || rr.Body.Len() != 0 {
		t.Errorf("response = %d %q, want an empty 200", rr.Code, rr.Body.String())
	}
	e.serve("/server", "POST", "/server/response/payload/set/default", "the payload")
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, e.newRequest("GET", "/some/traffic", ""))
	if rr.Body.String() != "the payload" {
		t.Errorf("response = %q, want the payload", rr.Body.String())
	}
}

func TestMiddlewareChainSkipsMiddlewaresWithoutAHandler(t *testing.T) {
	if trigger.Middleware.MiddlewareHandler != nil {
		t.Skip("the trigger middleware has a handler now, and the chain needs no special handling for it")
	}
	e := newTestEnv(t)
	rr := httptest.NewRecorder()
	Middleware.MiddlewareHandler(e.next()).ServeHTTP(rr, e.newRequest("GET", "/some/traffic", ""))
	if rr.Body.String() != "next" {
		t.Errorf("response = %q, want the next handler to be reached past the middleware that has no handler", rr.Body.String())
	}
}

func TestMiddlewareServesPayloadsConcurrently(t *testing.T) {
	e := newTestEnv(t)
	e.serve("/server", "POST", "/server/response/payload/set/default", "the payload")
	handler := Middleware.MiddlewareHandler(e.next())
	const workers, perWorker = 8, 20
	requests := make([][]*http.Request, workers)
	for w := range requests {
		for i := 0; i < perWorker; i++ {
			requests[w] = append(requests[w], e.newRequest("GET", fmt.Sprintf("/traffic/%d/%d", w, i), ""))
		}
	}
	bodies := make([][]string, workers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for w := range requests {
		bodies[w] = make([]string, perWorker)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i, req := range requests[w] {
				rr := httptest.NewRecorder()
				handler.ServeHTTP(rr, req)
				bodies[w][i] = rr.Body.String()
			}
		}()
	}
	close(start)
	wg.Wait()
	for w := range bodies {
		for i, got := range bodies[w] {
			if got != "the payload" {
				t.Errorf("worker %d, request %d: response = %q", w, i, got)
			}
		}
	}
	if e.nextCalls.Load() != 0 {
		t.Errorf("the next handler was called %d times, want none", e.nextCalls.Load())
	}
}
