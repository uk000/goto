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

package memory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"goto/pkg/server/middleware"
	"goto/pkg/util"
)

var routesOnce sync.Once

// router is the router that serves the APIs of the memory, which the middleware package keeps for the /memory path.
func router() http.Handler {
	routesOnce.Do(func() {
		stubOnce.Do(stubGlobalFuncs)
		Middleware.SetRoutes(nil)
	})
	return middleware.RootRouters["/memory"]
}

// newAPIRequest is a request that arrives at the listener of the given port, the way the server has it.
func newAPIRequest(method, target string, port int) *http.Request {
	stubOnce.Do(stubGlobalFuncs)
	req := httptest.NewRequest(method, target, nil)
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, port))
	_, req, _ = util.WithRequestStore(req, nil)
	return req
}

// call serves a request of a listener of the given port, the way the server does.
func call(t *testing.T, port int, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := router()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, port))
	_, req, _ = util.WithRequestStore(req, nil)
	rr := httptest.NewRecorder()
	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		h.ServeHTTP(rr, req)
	}()
	select {
	case p := <-done:
		if p != nil {
			t.Fatalf("%s %s panicked: %v", method, target, p)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("%s %s did not return in time", method, target)
	}
	return rr
}

func wantStatus(t *testing.T, rr *httptest.ResponseRecorder, status int) {
	t.Helper()
	if rr.Code != status {
		t.Fatalf("status = %d, want %d, body: %s", rr.Code, status, rr.Body.String())
	}
}

func decodeMemory(t *testing.T, rr *httptest.ResponseRecorder) *Memory {
	t.Helper()
	m := &struct {
		Context string            `json:"context"`
		Items   map[string]string `json:"items"`
	}{}
	if err := json.Unmarshal(rr.Body.Bytes(), m); err != nil {
		t.Fatalf("response is not a memory: %v\n%s", err, rr.Body.String())
	}
	return &Memory{Context: m.Context, Items: m.Items}
}

func TestMiddlewareDefinition(t *testing.T) {
	if Middleware.Name != "memory" || Middleware.SetRoutes == nil || Middleware.MiddlewareHandler == nil {
		t.Errorf("middleware = %+v", Middleware)
	}
}

func TestAPIAddContextAndItems(t *testing.T) {
	m, port := registeredManager(t)
	rr := call(t, port, "POST", "/memory/contexts/add/c1", "")
	wantStatus(t, rr, 200)
	if !strings.Contains(rr.Body.String(), "Added Context [c1]") {
		t.Errorf("body = %q", rr.Body.String())
	}
	if m.ContextMemory["c1"] == nil {
		t.Fatal("the context must be added to the memory of the port")
	}
	rr = call(t, port, "POST", "/memory/context/c1/add/k1=v1", "")
	wantStatus(t, rr, 200)
	if !strings.Contains(rr.Body.String(), "Stored [k1: v1] into Context [c1]") {
		t.Errorf("body = %q", rr.Body.String())
	}
	if got := m.GetOrAddContext("c1").Get("k1"); got != "v1" {
		t.Errorf("k1 = %q, want v1", got)
	}
	// adding another item to the context keeps the ones it has
	wantStatus(t, call(t, port, "POST", "/memory/context/c1/add/k2=v2", ""), 200)
	if mem := m.GetOrAddContext("c1"); mem.Get("k1") != "v1" || mem.Get("k2") != "v2" {
		t.Errorf("items = %v, want k1 and k2", mem.Items)
	}
	// the route needs a value for the key
	if rr := call(t, port, "PUT", "/memory/context/c1/add/k3=", "from body"); rr.Code != http.StatusNotFound {
		t.Errorf("status without a value = %d, want 404", rr.Code)
	}
	// a context of another port isn't touched
	other, otherPort := registeredManager(t)
	wantStatus(t, call(t, otherPort, "POST", "/memory/context/c1/add/k9=v9", ""), 200)
	if m.GetOrAddContext("c1").Get("k9") != "" || other.GetOrAddContext("c1").Get("k9") != "v9" {
		t.Error("items go to the memory of the port that the request arrived at")
	}
}

func TestAPIAddContextMatchAndExtractors(t *testing.T) {
	m, port := registeredManager(t)
	wantStatus(t, call(t, port, "POST", "/memory/context/session/from/header/X-Session", ""), 200)
	wantStatus(t, call(t, port, "POST", "/memory/context/session/memory/from/header/X-User", ""), 200)
	wantStatus(t, call(t, port, "POST", "/memory/context/session/memory/from/query/page", ""), 200)
	wantStatus(t, call(t, port, "POST", "/memory/context/session/memory/from/body/regex/order=order-\\d+", ""), 200)
	wantStatus(t, call(t, port, "POST", "/memory/context/session/memory/from/body/paths/name={.user.name}", ""), 200)

	rr := call(t, port, "GET", "/memory/context/session", "")
	wantStatus(t, rr, 200)
	var mx MemoryExtractor
	if err := json.Unmarshal(rr.Body.Bytes(), &mx); err != nil {
		t.Fatalf("extractor is not json: %v\n%s", err, rr.Body.String())
	}
	if mx.ContextKey != "session" || mx.ContextHeaderMatch != "X-Session" || len(mx.HeaderMatches) != 1 || mx.HeaderMatches[0] != "X-User" ||
		len(mx.QueryMatches) != 1 || mx.QueryMatches[0] != "page" || mx.BodyRegexMatches["order"] != `order-\d+` {
		t.Errorf("extractor = %+v", mx)
	}
	if !strings.Contains(rr.Body.String(), `{.user.name}`) {
		t.Errorf("the JSON path is missing from %s", rr.Body.String())
	}

	rr = call(t, port, "GET", "/memory/contexts", "")
	wantStatus(t, rr, 200)
	all := map[string]*MemoryExtractor{}
	if err := json.Unmarshal(rr.Body.Bytes(), &all); err != nil || all["session"] == nil {
		t.Errorf("all extractors = %s (%v)", rr.Body.String(), err)
	}
	if !m.HasAnyMatches() {
		t.Error("the manager must have matches after extractions were added")
	}
	// an extractor that doesn't exist reports nothing
	rr = call(t, port, "GET", "/memory/context/unknown", "")
	wantStatus(t, rr, 200)
	if strings.TrimSpace(rr.Body.String()) != "" && strings.TrimSpace(rr.Body.String()) != "null" {
		t.Errorf("an unknown extractor reported %q", rr.Body.String())
	}
}

func TestAPIExtractionAddedByAPIsWorksOnTraffic(t *testing.T) {
	m, port := registeredManager(t)
	wantStatus(t, call(t, port, "POST", "/memory/context/session/from/header/X-Session", ""), 200)
	wantStatus(t, call(t, port, "POST", "/memory/context/session/memory/from/header/X-User", ""), 200)
	wantStatus(t, call(t, port, "POST", "/memory/context/session/memory/from/body/regex/order=order-\\d+", ""), 200)
	req := httptest.NewRequest("POST", "/traffic", strings.NewReader("buying order-5"))
	req.Header.Set("X-Session", "S9")
	req.Header.Set("X-User", "carol")
	serveThroughMiddleware(t, port, nil, req, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	rr := call(t, port, "GET", "/memory/context/S9/memory", "")
	wantStatus(t, rr, 200)
	got := decodeMemory(t, rr)
	if got.Items["X-User"] != "carol" || got.Items["order"] != "order-5" {
		t.Errorf("memory = %v", got.Items)
	}
	if !m.HasAnyMatches() {
		t.Error("the manager must have matches")
	}
}

func TestAPIGetMemory(t *testing.T) {
	m, port := registeredManager(t)
	m.GetOrAddContext("c1").Add("k1", "v1")
	m.GetOrAddContext("c1").Add("k2", "v2")
	m.GetOrAddContext("c2").Add("x", "y")

	rr := call(t, port, "GET", "/memory/context/c1/memory", "")
	wantStatus(t, rr, 200)
	if got := decodeMemory(t, rr); len(got.Items) != 2 || got.Items["k1"] != "v1" || got.Items["k2"] != "v2" {
		t.Errorf("memory of c1 = %v", got.Items)
	}
	rr = call(t, port, "GET", "/memory/context/c1/memory/get/k2", "")
	wantStatus(t, rr, 200)
	if got := strings.TrimSpace(rr.Body.String()); got != "v2" {
		t.Errorf("k2 = %q, want v2", got)
	}
	rr = call(t, port, "GET", "/memory/context/c1/memory/get/missing", "")
	wantStatus(t, rr, 200)
	if got := strings.TrimSpace(rr.Body.String()); got != "" {
		t.Errorf("a missing key = %q, want empty", got)
	}
	rr = call(t, port, "GET", "/memory/contexts/memory", "")
	wantStatus(t, rr, 200)
	all := map[string]*struct {
		Items map[string]string `json:"items"`
	}{}
	if err := json.Unmarshal(rr.Body.Bytes(), &all); err != nil {
		t.Fatalf("response is not json: %v\n%s", err, rr.Body.String())
	}
	if all["c1"] == nil || all["c1"].Items["k1"] != "v1" || all["c2"] == nil || all["c2"].Items["x"] != "y" {
		t.Errorf("all memory = %s", rr.Body.String())
	}
	// "-" names the context of no name
	rr = call(t, port, "GET", "/memory/context/-/memory", "")
	wantStatus(t, rr, 200)
	if got := decodeMemory(t, rr); len(got.Items) != 0 {
		t.Errorf("memory of the empty context = %v", got.Items)
	}
}

func TestAPIPayloadTransform(t *testing.T) {
	m, port := registeredManager(t)
	transforms := `[{"payload":{"customer":""},"mappings":[{"source":"user.name","target":"customer","mode":"replace"}]}]`
	wantStatus(t, call(t, port, "POST", "/memory/context/k/memory/from/body/transform", transforms), 200)
	wantStatus(t, call(t, port, "POST", "/memory/context/k/memory/from/body/regex/cust=customer\\W+\\w+", ""), 200)
	mx := m.GetContextExtractors("k").(*MemoryExtractor)
	if len(mx.BodyTransforms) != 1 || len(mx.BodyTransforms[0].Mappings) != 1 {
		t.Fatalf("transforms = %+v", mx.BodyTransforms)
	}
	within(t, "ExtractFromBody", func() {
		m.ExtractFromBody("k", "ctx", body(`{"user":{"name":"dave"}}`), false)
	})
	if got := m.GetOrAddContext("ctx").Get("cust"); got != `customer":"dave` {
		t.Errorf("cust = %q, want the customer of the transformed body", got)
	}
	// a payload that isn't a list of transforms is refused
	rr := call(t, port, "POST", "/memory/context/k/memory/from/body/transform", "not json")
	wantStatus(t, rr, 400)
	if !strings.Contains(rr.Body.String(), "Invalid payload") {
		t.Errorf("body = %q", rr.Body.String())
	}
}

func TestAPIRejectsIncompleteRequests(t *testing.T) {
	_, port := registeredManager(t)
	for _, tc := range []struct {
		method, target, want string
		status               int
	}{
		{"POST", "/memory/context/c/add/k=", "", 404},
		{"POST", "/memory/context/c/add/=v", "", 404},
		// the route for a URI as the context match doesn't take a header, which is what the API needs
		{"POST", "/memory/context/k/from/uri?uri=/x", "Missing header key", 400},
	} {
		t.Run(tc.method+" "+tc.target, func(t *testing.T) {
			rr := call(t, port, tc.method, tc.target, "")
			if rr.Code != tc.status {
				t.Errorf("status = %d, want %d, body %q", rr.Code, tc.status, rr.Body.String())
			}
			if tc.want != "" && !strings.Contains(rr.Body.String(), tc.want) {
				t.Errorf("body = %q, want it to contain %q", rr.Body.String(), tc.want)
			}
		})
	}
}

func TestAPIRoutesOnlyAcceptTheirMethods(t *testing.T) {
	_, port := registeredManager(t)
	for _, tc := range []struct{ method, target string }{
		{"GET", "/memory/contexts/add/c"},
		{"GET", "/memory/context/c/add/k=v"},
		{"POST", "/memory/context/c/memory"},
		{"POST", "/memory/contexts"},
		{"GET", "/memory/context/k/memory/from/header/h"},
	} {
		if rr := call(t, port, tc.method, tc.target, ""); rr.Code != http.StatusMethodNotAllowed && rr.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 405/404", tc.method, tc.target, rr.Code)
		}
	}
}
