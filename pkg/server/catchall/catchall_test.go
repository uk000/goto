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

package catchall

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/util"

	"github.com/gorilla/mux"
	"gopkg.in/yaml.v3"
)

const (
	testPort  = 8091
	testLabel = "catchall-label"
)

var stubOnce sync.Once

func stubGlobalFuncs() {
	global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsListenerPresent = func(int) bool { return false }
	global.Funcs.IsListenerOpen = func(int) bool { return false }
	global.Funcs.GetListenerID = func(int) string { return "" }
	global.Funcs.GetListenerLabel = func(*http.Request) string { return testLabel }
	global.Funcs.GetListenerLabelForPort = func(int) string { return testLabel }
	util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
}

func newRequest(method, target string, headers ...string) *http.Request {
	stubOnce.Do(stubGlobalFuncs)
	req := httptest.NewRequest(method, target, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Add(headers[i], headers[i+1])
	}
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, testPort))
	_, req, _ = util.WithRequestStore(req, nil)
	return req
}

func newRouter() *mux.Router {
	r := mux.NewRouter()
	Middleware.SetRoutes(r)
	return r
}

func decodeJSON(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d %q, want 200", rr.Code, rr.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("body %q is not JSON: %v", rr.Body.String(), err)
	}
	return out
}

func TestEveryRequestIsAnsweredWithTheEchoOfIt(t *testing.T) {
	r := newRouter()
	for _, method := range []string{"GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS", "HEAD"} {
		for _, target := range []string{"/", "/anything", "/a/b/c?d=e", "/echo-not", "/server/unknown", "/x%20y"} {
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, newRequest(method, target))
			if rr.Code != http.StatusOK {
				t.Errorf("%s %s = %d, want 200 from the catch all", method, target, rr.Code)
				continue
			}
			if method == "HEAD" {
				continue
			}
			var got map[string]any
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Errorf("%s %s: body %q is not JSON: %v", method, target, rr.Body.String(), err)
				continue
			}
			if got["Request-Method"] != method || got["Request-URI"] != target {
				t.Errorf("%s %s: echoed %v %v", method, target, got["Request-Method"], got["Request-URI"])
			}
		}
	}
}

func TestDefaultResponseHasTheEchoThePortAndTheTime(t *testing.T) {
	r := newRouter()
	before := time.Now().Add(-2 * time.Second).Truncate(time.Second)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, newRequest("GET", "/hello?a=b", "X-Test", "v"))
	got := decodeJSON(t, rr)
	after := time.Now().Add(2 * time.Second)

	if ct := rr.Header().Get(constants.HeaderContentType); ct != constants.ContentTypeJSON {
		t.Errorf("content type = %q, want %q", ct, constants.ContentTypeJSON)
	}
	if got["Goto-Port"] != fmt.Sprint(testPort) {
		t.Errorf("Goto-Port = %#v, want the port of the request as text", got["Goto-Port"])
	}
	at, ok := got["At"].(string)
	if !ok {
		t.Fatalf("At = %#v, want a time", got["At"])
	}
	parsed, err := time.ParseInLocation(time.DateTime, at, time.Local)
	if err != nil {
		t.Fatalf("At = %q, want the format %q: %v", at, time.DateTime, err)
	}
	if parsed.Before(before) || parsed.After(after) {
		t.Errorf("At = %v, want a time that is about now", parsed)
	}
	for key, want := range map[string]any{
		"Request-Host": "example.com", "Request-Query": "a=b", constants.HeaderGotoListener: testLabel,
	} {
		if got[key] != want {
			t.Errorf("%s = %v, want %v", key, got[key], want)
		}
	}
	headers, _ := got["headers"].(map[string]any)
	if v, _ := headers["Request-X-Test"].([]any); len(v) != 1 || v[0] != "v" {
		t.Errorf("headers = %v, want the request headers", headers)
	}
}

func TestDefaultResponseIsYAMLWhenYAMLIsAccepted(t *testing.T) {
	r := newRouter()
	for _, accept := range []string{"application/yaml", "text/yaml", "application/json, application/x-yaml;q=0.9"} {
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, newRequest("GET", "/yaml/please", "Accept", accept))
		if rr.Code != http.StatusOK {
			t.Fatalf("Accept %q: status = %d %q, want 200", accept, rr.Code, rr.Body.String())
		}
		if ct := rr.Header().Get(constants.HeaderContentType); ct != constants.ContentTypeYAML {
			t.Errorf("Accept %q: content type = %q, want %q", accept, ct, constants.ContentTypeYAML)
		}
		var got map[string]any
		if err := yaml.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatalf("Accept %q: body %q is not YAML: %v", accept, rr.Body.String(), err)
		}
		if got["Request-URI"] != "/yaml/please" || got["Goto-Port"] != fmt.Sprint(testPort) || got["At"] == nil {
			t.Errorf("Accept %q: body = %v, want the default response", accept, got)
		}
	}
}

func TestDefaultResponseIsJSONForOtherAcceptHeaders(t *testing.T) {
	r := newRouter()
	for _, accept := range []string{"", "application/json", "*/*", "text/plain"} {
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, newRequest("GET", "/json", "Accept", accept))
		if ct := rr.Header().Get(constants.HeaderContentType); ct != constants.ContentTypeJSON {
			t.Errorf("Accept %q: content type = %q, want %q", accept, ct, constants.ContentTypeJSON)
		}
		decodeJSON(t, rr)
	}
}

func TestCatchAllLosesToTheRoutesAddedBeforeIt(t *testing.T) {
	r := mux.NewRouter()
	r.HandleFunc("/known", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "known") })
	Middleware.SetRoutes(r)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, newRequest("GET", "/known"))
	if rr.Body.String() != "known" {
		t.Errorf("GET /known = %q, want the route that is there first", rr.Body.String())
	}
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, newRequest("GET", "/unknown"))
	decodeJSON(t, rr)
}

func TestSendDefaultResponseDirectly(t *testing.T) {
	rr := httptest.NewRecorder()
	SendDefaultResponse(rr, newRequest("POST", "/direct"))
	got := decodeJSON(t, rr)
	if got["Request-URI"] != "/direct" || got["Request-Method"] != "POST" || got["Goto-Port"] != fmt.Sprint(testPort) {
		t.Errorf("response = %v, want the echo of the request", got)
	}
}

func TestMiddlewareHandsTheRequestToTheNextHandler(t *testing.T) {
	var calls atomic.Int32
	h := Middleware.MiddlewareHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTeapot)
		fmt.Fprint(w, "next")
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, newRequest("GET", "/through"))
	if calls.Load() != 1 || rr.Code != http.StatusTeapot || rr.Body.String() != "next" {
		t.Errorf("calls = %d, response = %d %q, want the response of the next handler", calls.Load(), rr.Code, rr.Body.String())
	}
}

func TestMiddlewareWithoutANextHandlerRespondsByDefault(t *testing.T) {
	h := Middleware.MiddlewareHandler(nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, newRequest("GET", "/the-end"))
	got := decodeJSON(t, rr)
	if got["Request-URI"] != "/the-end" || got["Goto-Port"] != fmt.Sprint(testPort) {
		t.Errorf("response = %v, want the default response", got)
	}
}

func TestMiddlewareWithoutANextHandlerLeavesNonTrafficAlone(t *testing.T) {
	h := Middleware.MiddlewareHandler(nil)
	for _, target := range []string{"/health", "/metrics", "/stats", "/remember/x"} {
		req := newRequest("GET", target)
		if !util.GetRequestStore(req).IsKnownNonTraffic {
			t.Fatalf("%s is expected to be known as not being traffic", target)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Body.Len() != 0 || rr.Code != http.StatusOK {
			t.Errorf("%s: response = %d %q, want nothing written", target, rr.Code, rr.Body.String())
		}
	}
}

func TestMiddlewareWithANextHandlerCallsItForNonTrafficToo(t *testing.T) {
	var calls atomic.Int32
	h := Middleware.MiddlewareHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	req := newRequest("GET", "/health")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want the next handler to be called", calls.Load())
	}
}

func TestConcurrentRequestsGetTheirOwnResponses(t *testing.T) {
	r := newRouter()
	const requests = 40
	results := make([]string, requests)
	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, newRequest("GET", fmt.Sprintf("/path/%d", i)))
			var out map[string]any
			if json.Unmarshal(rr.Body.Bytes(), &out) == nil {
				results[i], _ = out["Request-URI"].(string)
			}
		}()
	}
	wg.Wait()
	for i, got := range results {
		if want := fmt.Sprintf("/path/%d", i); got != want {
			t.Errorf("request %d echoed %q, want %q", i, got, want)
		}
	}
}
