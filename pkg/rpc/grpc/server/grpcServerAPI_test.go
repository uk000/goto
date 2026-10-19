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

package grpcserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gotogrpc "goto/pkg/rpc/grpc"
	"goto/pkg/rpc/grpc/pb"
	"goto/pkg/server/middleware"
	"goto/pkg/util"

	"github.com/gorilla/mux"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var (
	apiOnce sync.Once
	apiPort atomic.Int32
)

// apiRouter returns the root router that the APIs are registered on. The routes are registered with the root routers
// of the middleware package, and not with the router given to SetRoutes.
func apiRouter() *mux.Router {
	apiOnce.Do(func() {
		apiPort.Store(47000)
		Middleware.SetRoutes(mux.NewRouter())
	})
	return middleware.RootRouters["/grpc"]
}

// resetActiveServices gives a test the services that are active as none, and leaves none behind.
func resetActiveServices(t *testing.T) {
	t.Helper()
	reset := func() {
		lock.Lock()
		defer lock.Unlock()
		ActiveServices = map[int]map[string]*gotogrpc.GRPCService{}
	}
	reset()
	t.Cleanup(reset)
}

type apiResponse struct {
	code int
	body string
}

// callAPI makes a request to the router the way the server does, with a request store and the port of the listener
// that it came from.
func callAPI(t *testing.T, port int, method, target, body string) apiResponse {
	t.Helper()
	router := apiRouter()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, port))
	_, req, _ = util.WithRequestStore(req, nil)
	rr := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		router.ServeHTTP(rr, req)
	}()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		// not a Fatalf, since this runs on the goroutines of the tests too
		t.Errorf("%s %s did not finish", method, target)
		return apiResponse{}
	}
	return apiResponse{rr.Code, strings.TrimSpace(rr.Body.String())}
}

func newAPIPort(t *testing.T) int {
	t.Helper()
	stubGlobals(t)
	apiRouter()
	return int(apiPort.Add(1))
}

func TestExtAuthzRulesAPI(t *testing.T) {
	resetExtAuthz(t)
	port := newAPIPort(t)
	if r := callAPI(t, port, "GET", "/grpc/extauthz/rules", ""); r.code != http.StatusOK || r.body != "[]" {
		t.Errorf("no rules = %d %q, want 200 []", r.code, r.body)
	}
	r := callAPI(t, port, "POST", "/grpc/extauthz/rules", `{"header":"X-A","value":"1","action":"deny"}`)
	if r.code != http.StatusOK || r.body != "Stored [1] ext authz rules" {
		t.Errorf("a rule = %d %q", r.code, r.body)
	}
	r = callAPI(t, port, "POST", "/grpc/extauthz/rules", `[{"header":"x-b","action":"allow"},{"header":"x-c","value":"v","action":"deny"},{"header":"x-bad","action":"nope"}]`)
	if r.code != http.StatusOK || r.body != "Stored [2] ext authz rules" {
		t.Errorf("a list with a rule that is not valid = %d %q, want the two valid ones stored", r.code, r.body)
	}
	r = callAPI(t, port, "GET", "/grpc/extauthz/rules", "")
	var rules []ExtAuthzRule
	if err := json.Unmarshal([]byte(r.body), &rules); err != nil {
		t.Fatalf("rules are not JSON: %v\n%s", err, r.body)
	}
	want := []ExtAuthzRule{{Header: "x-a", Value: "1", Action: "deny"}, {Header: "x-b", Action: "allow"}, {Header: "x-c", Value: "v", Action: "deny"}}
	if len(rules) != len(want) {
		t.Fatalf("rules = %+v, want %+v", rules, want)
	}
	for i := range want {
		if rules[i] != want[i] {
			t.Errorf("rule %d = %+v, want %+v", i, rules[i], want[i])
		}
	}
}

func TestExtAuthzRulesAPIRejectsInvalidPayloads(t *testing.T) {
	resetExtAuthz(t)
	port := newAPIPort(t)
	tests := []struct {
		name string
		body string
		msg  string
	}{
		{"empty body", "", "Invalid ext authz rules payload"},
		{"not JSON", "header=x-a", "Invalid ext authz rules payload"},
		{"object without a header", `{"action":"deny"}`, "Invalid ext authz rules payload"},
		{"empty list", `[]`, "No ext authz rules given"},
		{"rule without an action", `{"header":"x-a"}`, "No valid ext authz rules stored"},
		{"rule with an action that is not known", `{"header":"x-a","action":"maybe"}`, "No valid ext authz rules stored"},
		{"list of rules that are not valid", `[{"header":"x-a"},{"action":"deny"}]`, "No valid ext authz rules stored"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := callAPI(t, port, "POST", "/grpc/extauthz/rules", tc.body)
			if r.code != http.StatusBadRequest || !strings.HasPrefix(r.body, tc.msg) {
				t.Errorf("%q = %d %q, want 400 %q", tc.body, r.code, r.body, tc.msg)
			}
		})
	}
	if rules := GetExtAuthzRules(); len(rules) != 0 {
		t.Errorf("rules after the payloads that were not valid = %+v", rules)
	}
}

func TestExtAuthzRulesAPIRemove(t *testing.T) {
	resetExtAuthz(t)
	port := newAPIPort(t)
	store(t, "x-a", "1", "deny")
	store(t, "x-a", "2", "deny")
	store(t, "x-b", "", "deny")
	if r := callAPI(t, port, "DELETE", "/grpc/extauthz/rules?header=x-none", ""); r.code != http.StatusOK || r.body != "No ext authz rule found for header [x-none]" {
		t.Errorf("remove of a rule that doesn't exist = %d %q", r.code, r.body)
	}
	if r := callAPI(t, port, "DELETE", "/grpc/extauthz/rules?header=x-a", ""); !strings.HasPrefix(r.body, "No ext authz rule found") {
		t.Errorf("a rule that has a value isn't removed without it: %q", r.body)
	}
	if r := callAPI(t, port, "DELETE", "/grpc/extauthz/rules?header=X-A&value=2", ""); r.body != "Removed ext authz rule for header [x-a]" {
		t.Errorf("remove with a value = %q", r.body)
	}
	if rules := GetExtAuthzRules(); len(rules) != 2 {
		t.Errorf("rules = %+v, want 2", rules)
	}
	if r := callAPI(t, port, "DELETE", "/grpc/extauthz/rules", ""); r.body != "Cleared all ext authz rules" {
		t.Errorf("remove without a header = %q", r.body)
	}
	if rules := GetExtAuthzRules(); len(rules) != 0 {
		t.Errorf("rules after clear = %+v", rules)
	}
}

func TestExtAuthzRulesAPIChangesTheAnswersOfTheService(t *testing.T) {
	e := startExtAuthzServer(t)
	port := newAPIPort(t)
	req := checkRequest("GET", "h", "/", map[string]string{"x-deny": "yes"})
	resp, _, err := e.check(t, req)
	requireAllowed(t, resp, err)
	if r := callAPI(t, port, "POST", "/grpc/extauthz/rules", `{"header":"x-deny","value":"yes","action":"deny"}`); r.code != http.StatusOK {
		t.Fatalf("POST = %d %q", r.code, r.body)
	}
	resp, _, err = e.check(t, req)
	requireDenied(t, resp, err)
	callAPI(t, port, "DELETE", "/grpc/extauthz/rules", "")
	resp, _, err = e.check(t, req)
	requireAllowed(t, resp, err)
}

func TestExtAuthzRulesAPIConcurrentUse(t *testing.T) {
	resetExtAuthz(t)
	port := newAPIPort(t)
	var wg sync.WaitGroup
	var bad atomic.Int32
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			header := fmt.Sprintf("x-api-%d", i)
			body := fmt.Sprintf(`{"header":%q,"action":"deny"}`, header)
			if r := callAPI(t, port, "POST", "/grpc/extauthz/rules", body); r.code != http.StatusOK {
				bad.Add(1)
			}
			callAPI(t, port, "GET", "/grpc/extauthz/rules", "")
			if i%2 == 0 {
				callAPI(t, port, "DELETE", "/grpc/extauthz/rules?header="+header, "")
			}
		}()
	}
	wg.Wait()
	if bad.Load() != 0 {
		t.Errorf("%d requests failed", bad.Load())
	}
	if rules := GetExtAuthzRules(); len(rules) != 5 {
		t.Errorf("rules = %d, want the 5 that were not removed", len(rules))
	}
}

func TestGetServicesAPI(t *testing.T) {
	startGotoServices(t, &pb.Goto_ServiceDesc, &pb.SampleService_ServiceDesc)
	port := newAPIPort(t)
	r := callAPI(t, port, "GET", "/grpc/server/services", "")
	if r.code != http.StatusOK {
		t.Fatalf("GET /services = %d %q", r.code, r.body)
	}
	var services map[string]struct {
		Name    string
		URI     string
		Methods map[string]struct {
			Name           string
			IsUnary        bool
			IsClientStream bool
			IsServerStream bool
		}
	}
	if err := json.Unmarshal([]byte(r.body), &services); err != nil {
		t.Fatalf("services are not JSON: %v\n%s", err, r.body)
	}
	goto_ := services["Goto"]
	if goto_.Name != "Goto" || len(goto_.Methods) != 4 || !goto_.Methods["echo"].IsUnary || !goto_.Methods["streamIn"].IsClientStream || !goto_.Methods["streamOut"].IsServerStream {
		t.Errorf("service Goto = %+v", goto_)
	}
}

func TestGetServicesAPIIsNotTheActiveServices(t *testing.T) {
	startGotoServices(t, &pb.Goto_ServiceDesc)
	resetActiveServices(t)
	port := newAPIPort(t)
	// a request is for the services that are known, whatever the text of its query is
	r := callAPI(t, port, "GET", "/grpc/server/services?filter=active", "")
	if !strings.Contains(r.body, `"Goto"`) {
		t.Errorf("GET /services?filter=active = %d %q, want all the services", r.code, r.body)
	}
	if r := callAPI(t, port, "GET", "/grpc/server/services/active", ""); strings.Contains(r.body, `"Goto"`) {
		t.Errorf("GET /services/active = %q, want the services that are served by the API", r.body)
	}
}

func TestServeAndStopServiceAPIs(t *testing.T) {
	lc := startLifecycle(t)
	resetActiveServices(t)
	l := lc.newListener(t, "api-1")
	GRPCManager.ServeListener(l)
	port := newAPIPort(t)
	otherPort := newAPIPort(t)

	// the service is not served on the port until it is asked to
	if r := callAPI(t, port, "GET", "/grpc/server/services/active", ""); r.code != http.StatusOK || r.body != "{}" {
		t.Errorf("active services at the start = %d %q, want {}", r.code, r.body)
	}
	r := callAPI(t, port, "POST", "/grpc/server/serve/SampleService", "")
	if r.code != http.StatusOK || r.body != fmt.Sprintf("Service [SampleService] registered for serving on port [%d]", port) {
		t.Errorf("serve = %d %q", r.code, r.body)
	}
	if resp, err := call(t, l.Port, "served"); err != nil || resp.Content != "re:served" {
		t.Errorf("call after the service is served = %v %v", resp, err)
	}
	r = callAPI(t, port, "GET", "/grpc/server/services/active", "")
	var active map[string]map[string]any
	if err := json.Unmarshal([]byte(r.body), &active); err != nil || active[strconv.Itoa(port)]["SampleService"] == nil {
		t.Errorf("active services = %q (%v), want SampleService on port %d", r.body, err, port)
	}
	// as asked for a port
	r = callAPI(t, port, "GET", fmt.Sprintf("/grpc/server/services/active?port=%d", port), "")
	var onPort map[string]any
	if err := json.Unmarshal([]byte(r.body), &onPort); err != nil || onPort["SampleService"] == nil {
		t.Errorf("active services of the port = %q (%v)", r.body, err)
	}
	r = callAPI(t, port, "GET", fmt.Sprintf("/grpc/server/services/active?port=%d", otherPort), "")
	if strings.Contains(r.body, "SampleService") {
		t.Errorf("active services of another port = %q, want none", r.body)
	}

	// the other forms of the routes
	if r := callAPI(t, otherPort, "POST", "/grpc/server/services/SampleService/serve", ""); !strings.HasPrefix(r.body, "Service [SampleService] registered for serving") {
		t.Errorf("serve through /services/{service}/serve = %d %q", r.code, r.body)
	}
	if r := callAPI(t, otherPort, "POST", "/grpc/server/services/SampleService/stop", ""); r.body != fmt.Sprintf("Service [SampleService] stopped on port [%d]", otherPort) {
		t.Errorf("stop through /services/{service}/stop = %d %q", r.code, r.body)
	}

	r = callAPI(t, port, "POST", "/grpc/server/stop/SampleService", "")
	if r.code != http.StatusOK || r.body != fmt.Sprintf("Service [SampleService] stopped on port [%d]", port) {
		t.Errorf("stop = %d %q", r.code, r.body)
	}
	if err := callWithin(t, l.Port, 5*time.Second); err == nil || !strings.Contains(err.Error(), "Unimplemented") {
		t.Errorf("call after the service is stopped = %v, want Unimplemented", err)
	}
	if r := callAPI(t, port, "GET", "/grpc/server/services/active", ""); strings.Contains(r.body, "SampleService") {
		t.Errorf("active services after the stop = %q, want none", r.body)
	}
}

func TestServeAndStopServiceAPIsOfServicesThatAreNotKnown(t *testing.T) {
	startGotoServices(t, &pb.Goto_ServiceDesc)
	resetActiveServices(t)
	port := newAPIPort(t)
	for _, target := range []string{"/grpc/server/serve/NoSuchService", "/grpc/server/stop/NoSuchService",
		"/grpc/server/services/NoSuchService/serve", "/grpc/server/services/NoSuchService/stop"} {
		r := callAPI(t, port, "POST", target, "")
		if r.code != http.StatusBadRequest || r.body != "Service proto not defined [NoSuchService]" {
			t.Errorf("POST %s = %d %q, want 400 with the service that is not defined", target, r.code, r.body)
		}
	}
}

func TestOpenPortAPIRejectsInvalidPorts(t *testing.T) {
	port := newAPIPort(t)
	for _, target := range []string{"/grpc/server/open", "/grpc/server/open?port=0", "/grpc/server/open?port=-1",
		"/grpc/server/open?port=65536", "/grpc/server/open?port=notanumber"} {
		r := callAPI(t, port, "POST", target, "")
		if r.code != http.StatusBadRequest || !strings.HasPrefix(r.body, "Invalid port [") {
			t.Errorf("POST %s = %d %q, want 400 invalid port", target, r.code, r.body)
		}
	}
}

func TestLoadReflectedServicesAPI(t *testing.T) {
	stubGlobals(t)
	resetRegistry(t)
	ts := startServer(t, func(s *grpc.Server, ts *testServer) {
		pb.RegisterSampleServiceServer(s, ts.sample)
		reflection.Register(s)
	})
	port := newAPIPort(t)
	r := callAPI(t, port, "POST", "/grpc/server/services/reflect/"+ts.addr, "")
	if r.code != http.StatusOK {
		t.Fatalf("reflect = %d %q", r.code, r.body)
	}
	var services map[string]struct{ Name string }
	if err := json.Unmarshal([]byte(r.body), &services); err != nil {
		t.Fatalf("services are not JSON: %v\n%s", err, r.body)
	}
	if services["SampleService"].Name != "SampleService" {
		t.Errorf("services after the reflection = %s, want SampleService", r.body)
	}
	if ServiceRegistry.GetService("SampleService") == nil {
		t.Errorf("the service that was loaded is not in the registry")
	}
}

func TestLoadReflectedServicesAPIOfAnUpstreamThatIsDown(t *testing.T) {
	stubGlobals(t)
	resetRegistry(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	port := newAPIPort(t)
	r := callAPI(t, port, "POST", "/grpc/server/services/reflect/"+addr, "")
	if r.code != http.StatusBadRequest || r.body == "" {
		t.Errorf("reflect of an upstream that is down = %d %q, want 400 with the error", r.code, r.body)
	}
}

func TestGetServicesAPIWhileServicesAreAdded(t *testing.T) {
	stubGlobals(t)
	resetRegistry(t)
	port := newAPIPort(t)
	var wg sync.WaitGroup
	var bad atomic.Int32
	for i := 0; i < 6; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			sd := cloneDesc(&pb.SampleService_ServiceDesc)
			sd.ServiceName = "SampleService"
			ServiceRegistry.GetOrCreateService(sd)
			ServiceRegistry.RemoveService("SampleService")
		}()
		go func() {
			defer wg.Done()
			if r := callAPI(t, port, "GET", "/grpc/server/services", ""); r.code != http.StatusOK {
				bad.Add(1)
			}
		}()
	}
	wg.Wait()
	if bad.Load() != 0 {
		t.Errorf("%d requests failed", bad.Load())
	}
}
