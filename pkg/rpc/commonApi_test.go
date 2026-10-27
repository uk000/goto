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

package rpc

// The tests of this package cannot use the real gRPC/JSON-RPC registries, since those packages import this one. The
// tests use fake services and registries that behave like the real ones (including the typed nil that the real
// registries return for an unknown service), and plug them into GetServiceRegistry the way the real packages do.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goto/pkg/global"
	"goto/pkg/server/hooks"
	"goto/pkg/server/middleware"
	"goto/pkg/server/response/payload"
	"goto/pkg/util"

	"github.com/gorilla/mux"
	"sigs.k8s.io/yaml"
)

type fakeMethod struct {
	Name             string        `json:"name"`
	URI              string        `json:"uri"`
	StreamCount      int           `json:"streamCount"`
	StreamDelayMin   time.Duration `json:"streamDelayMin"`
	StreamDelayMax   time.Duration `json:"streamDelayMax"`
	StreamDelayCount int           `json:"streamDelayCount"`
	ResponsePayload  string        `json:"responsePayload"`
	lock             sync.Mutex
}

func (m *fakeMethod) GetName() string { return m.Name }
func (m *fakeMethod) GetURI() string  { return m.URI }
func (m *fakeMethod) SetStreamCount(c int) {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.StreamCount = c
}
func (m *fakeMethod) SetStreamDelay(min, max time.Duration, count int) {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.StreamDelayMin, m.StreamDelayMax, m.StreamDelayCount = min, max, count
}
func (m *fakeMethod) SetResponsePayload(b []byte) {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.ResponsePayload = string(b)
}

// MarshalJSON encodes the method with its lock held, as the APIs list the services while they configure the methods.
func (m *fakeMethod) MarshalJSON() ([]byte, error) {
	m.lock.Lock()
	defer m.lock.Unlock()
	return json.Marshal(struct {
		Name             string        `json:"name"`
		URI              string        `json:"uri"`
		StreamCount      int           `json:"streamCount"`
		StreamDelayMin   time.Duration `json:"streamDelayMin"`
		StreamDelayMax   time.Duration `json:"streamDelayMax"`
		StreamDelayCount int           `json:"streamDelayCount"`
		ResponsePayload  string        `json:"responsePayload"`
	}{m.Name, m.URI, m.StreamCount, m.StreamDelayMin, m.StreamDelayMax, m.StreamDelayCount, m.ResponsePayload})
}

// methodState is what the APIs configure on a method.
type methodState struct {
	StreamCount      int
	StreamDelayMin   time.Duration
	StreamDelayMax   time.Duration
	StreamDelayCount int
	ResponsePayload  string
}

func (m *fakeMethod) snapshot() methodState {
	m.lock.Lock()
	defer m.lock.Unlock()
	return methodState{StreamCount: m.StreamCount, StreamDelayMin: m.StreamDelayMin, StreamDelayMax: m.StreamDelayMax,
		StreamDelayCount: m.StreamDelayCount, ResponsePayload: m.ResponsePayload}
}

type fakeService struct {
	Name    string                 `json:"name"`
	URI     string                 `json:"uri"`
	Methods map[string]*fakeMethod `json:"methods"`
	grpc    bool
}

func (s *fakeService) IsGRPC() bool            { return s.grpc }
func (s *fakeService) IsJSONRPC() bool         { return !s.grpc }
func (s *fakeService) GetName() string         { return s.Name }
func (s *fakeService) GetURI() string          { return s.URI }
func (s *fakeService) HasMethod(m string) bool { return s.Methods[m] != nil }
func (s *fakeService) GetMethodCount() int     { return len(s.Methods) }
func (s *fakeService) ForEachMethod(f func(RPCMethod)) {
	for _, m := range s.Methods {
		f(m)
	}
}
func (s *fakeService) GetMethod(name string) RPCMethod { return s.Methods[name] }

type fakeRegistry struct {
	Services map[string]*fakeService `json:"services"`
	grpc     bool
	lock     sync.RWMutex
}

func newFakeRegistry(grpc bool) *fakeRegistry {
	return &fakeRegistry{Services: map[string]*fakeService{}, grpc: grpc}
}

// GetRPCService returns a typed nil for an unknown service, like the real registries do.
func (r *fakeRegistry) GetRPCService(name string) RPCService {
	r.lock.RLock()
	defer r.lock.RUnlock()
	return r.Services[name]
}

func (r *fakeRegistry) add(name string, methods ...string) *fakeService {
	r.lock.Lock()
	defer r.lock.Unlock()
	s := &fakeService{Name: name, URI: strings.ToLower("/" + name), Methods: map[string]*fakeMethod{}, grpc: r.grpc}
	for _, m := range methods {
		s.Methods[m] = &fakeMethod{Name: m, URI: strings.ToLower("/" + name + "/" + m)}
	}
	r.Services[name] = s
	return s
}

// untypedNilRegistry returns a plain nil interface for an unknown service.
type untypedNilRegistry struct{}

func (untypedNilRegistry) GetRPCService(string) RPCService { return nil }

// valueService is an RPCService that is not a pointer, so reflect's IsNil cannot be asked about it.
type valueService struct{}

func (valueService) IsGRPC() bool                  { return true }
func (valueService) IsJSONRPC() bool               { return false }
func (valueService) GetName() string               { return "value" }
func (valueService) GetURI() string                { return "/value" }
func (valueService) HasMethod(string) bool         { return true }
func (valueService) GetMethodCount() int           { return 0 }
func (valueService) ForEachMethod(func(RPCMethod)) {}
func (valueService) GetMethod(string) RPCMethod    { return nil }

type valueRegistry struct{}

func (valueRegistry) GetRPCService(string) RPCService { return valueService{} }

var (
	setupOnce sync.Once
	portSeq   atomic.Int32
	nameSeq   atomic.Int32
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
	// Created by the server at startup. Setting a response payload builds its URI matcher on util.MatchRouter.
	if util.MatchRouter == nil {
		util.CreateRouters(mux.NewRouter())
	}
}

// uniqueName returns a service name that no other test of the process uses, since the tracking and hooks state of the
// process cannot be reset.
func uniqueName(prefix string) string {
	return fmt.Sprintf("%s%d", prefix, nameSeq.Add(1))
}

// testEnv serves the rpc APIs the way the server does, with the registries of the test plugged into
// GetServiceRegistry, and a port of its own.
type testEnv struct {
	t      *testing.T
	port   int
	router http.Handler
	grpc   *fakeRegistry
	json   *fakeRegistry
}

// serverRouting hands a request to the router of its root path, as the server does, so the tests reach the routes the
// way a request to the server does: through the router of the first segment of the path.
type serverRouting struct{}

func (serverRouting) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	root, _ := util.GetRootURI(r.RequestURI)
	if router := middleware.RootRouterFor(root, r); router != nil {
		router.ServeHTTP(w, r)
		return
	}
	http.NotFound(w, r)
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	setupOnce.Do(func() {
		stubGlobalFuncs()
		setRoutes(nil)
	})
	e := &testEnv{
		t:      t,
		port:   int(41000 + portSeq.Add(1)),
		router: serverRouting{},
		grpc:   newFakeRegistry(true),
		json:   newFakeRegistry(false),
	}
	if middleware.RootRouters[grpcRoot] == nil || middleware.RootRouters[jsonrpcRoot] == nil {
		t.Fatalf("rpc routes are not registered")
	}
	prevGRPC, prevJSON := GetServiceRegistry["grpc"], GetServiceRegistry["jsonrpc"]
	GetServiceRegistry["grpc"] = func(int) RPCServiceRegistry { return e.grpc }
	GetServiceRegistry["jsonrpc"] = func(int) RPCServiceRegistry { return e.json }
	t.Cleanup(func() {
		payload.PayloadManager.ClearRPCResponsePayloads(e.port)
		restore := func(key string, prev func(int) RPCServiceRegistry) {
			if prev == nil {
				delete(GetServiceRegistry, key)
			} else {
				GetServiceRegistry[key] = prev
			}
		}
		restore("grpc", prevGRPC)
		restore("jsonrpc", prevJSON)
	})
	return e
}

func (e *testEnv) request(method, target, body string, headers ...string) *http.Request {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, e.port))
	_, req, _ = util.WithRequestStore(req, nil)
	return req
}

// do calls an API of the server and returns the recorded response.
func (e *testEnv) do(method, target, body string, headers ...string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	e.router.ServeHTTP(rr, e.request(method, target, body, headers...))
	return rr
}

func (e *testEnv) mustDo(method, target, body string, headers ...string) *httptest.ResponseRecorder {
	e.t.Helper()
	rr := e.do(method, target, body, headers...)
	if rr.Code != http.StatusOK {
		e.t.Fatalf("%s %s = %d %q, want 200", method, target, rr.Code, rr.Body.String())
	}
	return rr
}

func decodeJSON(t *testing.T, body string, out any) {
	t.Helper()
	if err := json.Unmarshal([]byte(body), out); err != nil {
		t.Fatalf("response is not JSON (%v): %q", err, body)
	}
}

// ---- the APIs registered by setRoutes ----

func yamlOf(t *testing.T, body string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := yaml.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("response is not YAML (%v): %q", err, body)
	}
	return out
}

func TestRouteGetServicesListsTheRegistryOfTheRPCType(t *testing.T) {
	e := newTestEnv(t)
	e.grpc.add("GrpcOne", "a", "b")
	e.grpc.add("GrpcTwo")
	e.json.add("JsonOne", "c")

	grpcServices := yamlOf(t, e.mustDo("GET", "/grpc/server", "").Body.String())["services"].(map[string]any)
	if len(grpcServices) != 2 || grpcServices["GrpcOne"] == nil || grpcServices["GrpcTwo"] == nil {
		t.Errorf("grpc services = %v, want GrpcOne and GrpcTwo", grpcServices)
	}
	jsonServices := yamlOf(t, e.mustDo("GET", "/jsonrpc/server", "").Body.String())["services"].(map[string]any)
	if len(jsonServices) != 1 || jsonServices["JsonOne"] == nil {
		t.Errorf("jsonrpc services = %v, want JsonOne only", jsonServices)
	}
	// the trailing slash variant is registered as well
	if rr := e.do("GET", "/grpc/server/", ""); rr.Code != 200 {
		t.Errorf("GET /grpc/server/ = %d", rr.Code)
	}
}

func TestRouteGetServicesPortsGetTheirOwnRegistry(t *testing.T) {
	e := newTestEnv(t)
	other := newFakeRegistry(true)
	other.add("OnlyOnOtherPort")
	GetServiceRegistry["grpc"] = func(port int) RPCServiceRegistry {
		if port == e.port {
			return e.grpc
		}
		return other
	}
	e.grpc.add("OnThisPort")
	services := yamlOf(t, e.mustDo("GET", "/grpc/server", "").Body.String())["services"].(map[string]any)
	if services["OnThisPort"] == nil || services["OnlyOnOtherPort"] != nil {
		t.Errorf("services = %v, want the services of the port of the request only", services)
	}
}

func TestRouteUnknownRPCTypeIsNotServed(t *testing.T) {
	e := newTestEnv(t)
	if rr := e.do("GET", "/thrift/server", ""); rr.Code != http.StatusNotFound {
		t.Errorf("GET /thrift/server = %d, want 404", rr.Code)
	}
}

func TestRouteGetService(t *testing.T) {
	e := newTestEnv(t)
	e.grpc.add("Detail", "One", "Two")

	got := yamlOf(t, e.mustDo("GET", "/grpc/server/Detail", "").Body.String())
	methods, _ := got["methods"].(map[string]any)
	if got["name"] != "Detail" || got["uri"] != "/detail" || len(methods) != 2 || methods["One"] == nil || methods["Two"] == nil {
		t.Errorf("service = %v, want Detail with methods One and Two", got)
	}
	rr := e.do("GET", "/grpc/server/Missing", "")
	if rr.Code != 400 || !strings.Contains(rr.Body.String(), "Service proto not defined [Missing]") {
		t.Errorf("unknown service: %d %q", rr.Code, rr.Body.String())
	}
	rr = e.do("GET", "/jsonrpc/server/Detail", "")
	if rr.Code != 400 {
		t.Errorf("a grpc service must not be served as a jsonrpc service, got %d %q", rr.Code, rr.Body.String())
	}
}

func TestRouteTrackServiceRoutes(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantMsg string
	}{
		{name: "plain", path: "/track", wantMsg: "Tracking grpc Service [%s] with headers [[[]  ]]"},
		{name: "headers", path: "/track/headers/x-a,x-b", wantMsg: "Tracking grpc Service [%s] with headers [[[x-a x-b]  ]]"},
		{name: "header value", path: "/track/x-a=1", wantMsg: "Tracking grpc Service [%s] with headers [[[] x-a 1]]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newTestEnv(t)
			name := uniqueName("Track")
			s := e.grpc.add(name, "m")
			hooks.GetPortHooks(e.port)
			rr := e.mustDo("POST", "/grpc/server/"+name+tt.path, "")
			want := strings.Replace(tt.wantMsg, "%s", name, 1)
			if !strings.Contains(rr.Body.String(), want) {
				t.Errorf("response = %q, want it to contain %q", rr.Body.String(), want)
			}
			if got := GetRPCTracker(e.port).GetServiceTrackerJSON(s); got == nil {
				t.Errorf("the service must be tracked on the port of the request")
			}
			if got := GetRPCTracker(e.port + 1000).GetServiceTrackerJSON(s); got != nil {
				t.Errorf("the service must not be tracked on another port")
			}
		})
	}
}

func TestRouteGetServiceTracking(t *testing.T) {
	e := newTestEnv(t)
	name := uniqueName("Tracked")
	s := e.json.add(name, "Alpha", "Beta")
	hooks.GetPortHooks(e.port)

	// a known service that isn't tracked yet
	if rr := e.do("GET", "/jsonrpc/server/"+name+"/tracking", ""); rr.Code != http.StatusNotFound {
		t.Errorf("tracking of a service that isn't tracked = %d %q, want 404", rr.Code, rr.Body.String())
	}

	e.mustDo("POST", "/jsonrpc/server/"+name+"/track/headers/x-id", "")
	for i := 0; i < 2; i++ {
		e.sendTraffic("POST", s.Methods["Alpha"].URI, "X-Id", "a")
	}
	e.sendTraffic("POST", s.Methods["Beta"].URI, "X-Id", "b")

	var tracked struct {
		ServiceCallCounts       int            `json:"serviceCallCounts"`
		ServiceMethodCallCounts map[string]int `json:"serviceMethodCallCounts"`
		PortTracking            map[string]struct {
			ByURI          map[string]int `json:"byURI"`
			ByURIAndHeader map[string]map[string]struct {
				RequestCounts              int            `json:"requestCounts"`
				RequestCountsByHeaderValue map[string]int `json:"requestCountsByHeaderValue"`
			} `json:"byURIAndHeader"`
		} `json:"portTracking"`
	}
	rr := e.mustDo("GET", "/jsonrpc/server/"+name+"/tracking", "")
	decodeJSON(t, rr.Body.String(), &tracked)
	if tracked.ServiceCallCounts != 3 {
		t.Errorf("serviceCallCounts = %d, want 3", tracked.ServiceCallCounts)
	}
	if want := map[string]int{s.Methods["Alpha"].URI: 2, s.Methods["Beta"].URI: 1}; !reflect.DeepEqual(tracked.ServiceMethodCallCounts, want) {
		t.Errorf("serviceMethodCallCounts = %v, want %v", tracked.ServiceMethodCallCounts, want)
	}
	port := tracked.PortTracking[strconv.Itoa(e.port)]
	alpha, beta := port.ByURIAndHeader[s.Methods["Alpha"].URI]["X-Id"], port.ByURIAndHeader[s.Methods["Beta"].URI]["X-Id"]
	if alpha.RequestCounts != 2 || alpha.RequestCountsByHeaderValue["a"] != 2 || beta.RequestCounts != 1 || beta.RequestCountsByHeaderValue["b"] != 1 {
		t.Errorf("portTracking = %+v, want the header counts of port %d: alpha 2 (a:2), beta 1 (b:1)", tracked.PortTracking, e.port)
	}
	if got := rr.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("content type = %q, want application/json", got)
	}
}

func TestRoutePayloadVariants(t *testing.T) {
	e := newTestEnv(t)
	s := e.grpc.add("Variants", "Call")
	uri := s.Methods["Call"].URI
	base := "/grpc/server/Variants/Call/payload"

	t.Run("header and value", func(t *testing.T) {
		e.mustDo("POST", base+"/header/x-want=yes", `{"v":"header-value"}`)
		if rp, found := lookupPayload(e.port, uri, http.Header{"X-Want": {"yes"}}, ""); !found || string(rp.Payload) != `{"v":"header-value"}` {
			t.Errorf("payload for the matching header value = %+v found %v", rp, found)
		}
		if _, found := lookupPayload(e.port, uri, http.Header{"X-Want": {"no"}}, ""); found {
			t.Errorf("a request with another value of the header must not get the payload")
		}
		if _, found := lookupPayload(e.port, uri, nil, ""); found {
			t.Errorf("a request without the header must not get the payload")
		}
	})
	t.Run("header any value", func(t *testing.T) {
		e.mustDo("POST", base+"/header/x-any", `{"v":"header-any"}`)
		if rp, found := lookupPayload(e.port, uri, http.Header{"X-Any": {"whatever"}}, ""); !found || string(rp.Payload) != `{"v":"header-any"}` {
			t.Errorf("payload for any value of the header = %+v found %v", rp, found)
		}
	})
	t.Run("body regex", func(t *testing.T) {
		e.mustDo("POST", base+"/body~needle", `{"v":"body-regex"}`)
		if rp, found := lookupPayload(e.port, uri, nil, `{"find":"the NEEDLE here"}`); !found || string(rp.Payload) != `{"v":"body-regex"}` {
			t.Errorf("payload for a matching body = %+v found %v", rp, found)
		}
		if _, found := lookupPayload(e.port, uri, nil, `{"find":"nothing"}`); found {
			t.Errorf("a request with another body must not get the payload")
		}
	})
	t.Run("body paths", func(t *testing.T) {
		e.mustDo("POST", base+"/body/paths/{.user.id}", `{"v":"body-paths"}`)
		if rp, found := lookupPayload(e.port, uri, nil, `{"user":{"id":7}}`); !found || string(rp.Payload) != `{"v":"body-paths"}` {
			t.Errorf("payload for a body with the path = %+v found %v", rp, found)
		}
		if _, found := lookupPayload(e.port, uri, nil, `{"other":1}`); found {
			t.Errorf("a request whose body lacks the path must not get the payload")
		}
	})
	t.Run("transform", func(t *testing.T) {
		e.mustDo("POST", base+"/transform", `[{"mappings":[{"source":"a","target":"b"}],"payload":{"x":1}}]`)
		if rp, found := lookupPayload(e.port, uri, nil, ""); !found || len(rp.Transforms) != 1 {
			t.Errorf("payload after the transform was set = %+v found %v", rp, found)
		}
	})
	t.Run("stream with header", func(t *testing.T) {
		e.mustDo("POST", base+"/stream/count=2/delay=1ms/header/x-stream=on", `[{"n":1},{"n":2}]`)
		rp, found := lookupPayload(e.port, uri, http.Header{"X-Stream": {"on"}}, "")
		if !found || !rp.IsStream || len(rp.StreamPayload) != 2 {
			t.Errorf("stream payload for the matching header = %+v found %v", rp, found)
		}
	})
	t.Run("stream with header any value", func(t *testing.T) {
		e.mustDo("POST", base+"/stream/count=1/delay=1ms/header/x-stream-any", `[{"n":1}]`)
		rp, found := lookupPayload(e.port, uri, http.Header{"X-Stream-Any": {"anything"}}, "")
		if !found || !rp.IsStream {
			t.Errorf("stream payload for any value of the header = %+v found %v", rp, found)
		}
	})
	t.Run("clear", func(t *testing.T) {
		e.mustDo("POST", base+"/clear", "")
		if _, found := lookupPayload(e.port, uri, http.Header{"X-Want": {"yes"}}, ""); found {
			t.Errorf("payloads must be gone after clear")
		}
	})
}

func TestRoutePayloadRejectsUnknownMethod(t *testing.T) {
	e := newTestEnv(t)
	e.grpc.add("Strict", "Call")
	for _, path := range []string{"/payload", "/payload/clear", "/payload/transform"} {
		rr := e.do("POST", "/grpc/server/Strict/Nope"+path, `{}`)
		if rr.Code != 400 || !strings.Contains(rr.Body.String(), "Method [Nope] not defined for service [Strict]") {
			t.Errorf("POST %s: %d %q", path, rr.Code, rr.Body.String())
		}
	}
}

// Many callers use the APIs at once: payloads of different methods are set and cleared, services are tracked and
// listed, and calls are counted. The totals of the counts are exact. Run with -race.
func TestRoutesConcurrentUse(t *testing.T) {
	e := newTestEnv(t)
	hooks.GetPortHooks(e.port)
	const services, perService = 6, 20
	var names []string
	var svcs []*fakeService
	for i := 0; i < services; i++ {
		name := uniqueName("Conc")
		names = append(names, name)
		svcs = append(svcs, e.json.add(name, "Call"))
		e.mustDo("POST", "/jsonrpc/server/"+name+"/track", "")
	}
	// requests are built up front, they must not be made in the goroutines
	type work struct {
		req  *http.Request
		call func(rr *httptest.ResponseRecorder, req *http.Request)
	}
	var jobs []work
	serve := func(rr *httptest.ResponseRecorder, req *http.Request) { e.router.ServeHTTP(rr, req) }
	// Setting a payload registers a route on util.MatchRouter, which a mux router doesn't allow to do concurrently
	// (util.BuildURIMatcher doesn't synchronize this). The test sets payloads one at a time, and does all else at once.
	var payloadSetLock sync.Mutex
	servePayloadSet := func(rr *httptest.ResponseRecorder, req *http.Request) {
		payloadSetLock.Lock()
		defer payloadSetLock.Unlock()
		e.router.ServeHTTP(rr, req)
	}
	for i, name := range names {
		for j := 0; j < perService; j++ {
			jobs = append(jobs,
				work{e.request("POST", "/jsonrpc/server/"+name+"/Call/payload", fmt.Sprintf(`{"i":%d}`, j)), servePayloadSet},
				work{e.request("GET", "/jsonrpc/server/"+name+"/tracking", ""), serve},
				work{e.request("GET", "/jsonrpc/server", ""), serve},
				work{e.request("GET", "/jsonrpc/server/"+name, ""), serve},
				work{e.request("POST", "/jsonrpc/server/"+name+"/Call/payload/header/x-h=v", `{"h":1}`), servePayloadSet},
				work{e.request("POST", "/jsonrpc/server/"+name+"/Call/payload/clear", ""), serve},
				work{e.request("POST", svcs[i].Methods["Call"].URI, ""), func(rr *httptest.ResponseRecorder, req *http.Request) {
					hooks.Middleware.MiddlewareHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(rr, req)
				}},
			)
		}
	}
	codes := make([]int, len(jobs))
	var wg sync.WaitGroup
	for i := range jobs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rr := httptest.NewRecorder()
			jobs[i].call(rr, jobs[i].req)
			codes[i] = rr.Code
		}(i)
	}
	wg.Wait()
	for i, code := range codes {
		if code != http.StatusOK {
			t.Fatalf("job %d (%s %s) = %d, want 200", i, jobs[i].req.Method, jobs[i].req.RequestURI, code)
		}
	}
	for i, s := range svcs {
		if got := GetRPCTracker(e.port).GetServiceTrackerJSON(s); got == nil || got.ServiceCallCounts != perService {
			t.Errorf("service %s: tracker = %+v, want %d counted calls", names[i], got, perService)
		}
	}
}
