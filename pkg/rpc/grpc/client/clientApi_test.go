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

package grpcclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"goto/pkg/constants"
	"goto/pkg/server/middleware"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var apiOnce sync.Once

// apiRouters returns the root routers that the client APIs are registered on. The routes are registered with the
// root routers of the middleware package, and not with the router given to SetRoutes.
func apiRouters() (grpcRouter, clientRouter *mux.Router) {
	apiOnce.Do(func() { Middleware.SetRoutes(mux.NewRouter()) })
	return middleware.RootRouters["/grpc"], middleware.RootRouters["/client"]
}

type apiResult struct {
	Headers map[string][]string
	Payload []map[string]any
}

// callAPI makes a request to the router the way the server does, with a request store and the listener port.
func callAPI(t *testing.T, router *mux.Router, method, target, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	stubGlobals(t)
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Add(headers[i], headers[i+1])
	}
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, 4000))
	_, req, _ = util.WithRequestStore(req, nil)
	rr := httptest.NewRecorder()
	within(t, 30*time.Second, method+" "+target, func() { router.ServeHTTP(rr, req) })
	return rr
}

func parseResults(t *testing.T, rr *httptest.ResponseRecorder) []apiResult {
	t.Helper()
	var out []apiResult
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not a JSON array of results (status %d): %v\n%s", rr.Code, err, rr.Body.String())
	}
	return out
}

func callJSON(e *testEnv, service, method string, payloads string, extra string) string {
	return fmt.Sprintf(`{"service":%q,"method":%q,"endpoint":%q,"payloads":%s%s}`, service, method, e.addr, payloads, extra)
}

func TestCallAPI(t *testing.T) {
	e := newEnv(t)
	grpcRouter, clientRouter := apiRouters()
	body := callJSON(e, "SampleService", "oneOne", `{"linear":[{"payload":"{\"id\":1,\"content\":\"hi\"}"}]}`, `,"result":true`)

	for _, prefix := range []struct {
		router *mux.Router
		path   string
	}{{grpcRouter, "/grpc/client/call"}, {clientRouter, "/client/grpc/call"}} {
		rr := callAPI(t, prefix.router, "POST", prefix.path, body)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s = %d %q", prefix.path, rr.Code, rr.Body.String())
		}
		results := parseResults(t, rr)
		if len(results) != 1 || len(results[0].Payload) != 1 || results[0].Payload[0]["content"] != "re:hi" {
			t.Fatalf("%s results = %+v", prefix.path, results)
		}
		if got := results[0].Headers["x-reply-header"]; len(got) != 1 || got[0] != "h-hi" {
			t.Errorf("%s headers = %v", prefix.path, results[0].Headers)
		}
		if got := results[0].Headers["x-reply-trailer"]; len(got) != 1 || got[0] != "t-hi" {
			t.Errorf("%s the result has the trailers along with the headers: %v", prefix.path, results[0].Headers)
		}
	}
	if n := len(e.sample.recorded("oneOne")); n != 2 {
		t.Errorf("server calls = %d, want 2", n)
	}

	// without the result flag, the results carry headers only
	rr := callAPI(t, grpcRouter, "POST", "/grpc/client/call", callJSON(e, "SampleService", "oneOne", `{"linear":[{"payload":"{\"content\":\"hi\"}"}]}`, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("no-result call = %d %q", rr.Code, rr.Body.String())
	}
	if results := parseResults(t, rr); len(results) != 1 || results[0].Payload != nil {
		t.Errorf("payload must be left out unless asked for: %+v", results)
	}
}

func TestCallAPIForwardsRequestHeaders(t *testing.T) {
	e := newEnv(t)
	grpcRouter, _ := apiRouters()
	body := callJSON(e, "SampleService", "oneOne", `{"linear":[{"payload":"{\"content\":\"f\"}"}]}`,
		`,"headers":{"request":{"forward":["x-fwd"],"add":{"x-added":"a"}}}`)
	rr := callAPI(t, grpcRouter, "POST", "/grpc/client/call", body, "X-Fwd", "fwd-value", "X-Not-Forwarded", "nope")
	if rr.Code != http.StatusOK {
		t.Fatalf("call = %d %q", rr.Code, rr.Body.String())
	}
	md := e.sample.recorded("oneOne")[0].md
	if got := md.Get("x-fwd"); len(got) != 1 || got[0] != "fwd-value" {
		t.Errorf("x-fwd = %v", got)
	}
	if got := md.Get("x-added"); len(got) != 1 || got[0] != "a" {
		t.Errorf("x-added = %v", got)
	}
	if len(md.Get("x-not-forwarded")) != 0 {
		t.Errorf("only the listed headers are forwarded: %v", md)
	}
}

func TestCallAPIValidation(t *testing.T) {
	e := newEnv(t)
	grpcRouter, _ := apiRouters()
	cases := []struct {
		name string
		body string
		want string
	}{
		{"not a payload", `{not json or yaml`, "Failed to parse payload"},
		{"no service", fmt.Sprintf(`{"method":"oneOne","endpoint":%q,"payloads":{"linear":[]}}`, e.addr), "Invalid payload"},
		{"no method", fmt.Sprintf(`{"service":"SampleService","endpoint":%q,"payloads":{"linear":[]}}`, e.addr), "Invalid payload"},
		{"no endpoint", `{"service":"SampleService","method":"oneOne","payloads":{"linear":[]}}`, "Invalid payload"},
		{"no payloads", fmt.Sprintf(`{"service":"SampleService","method":"oneOne","endpoint":%q}`, e.addr), "Invalid payload"},
	}
	for _, tc := range cases {
		rr := callAPI(t, grpcRouter, "POST", "/grpc/client/call", tc.body)
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), tc.want) {
			t.Errorf("%s: %d %q, want 400 with %q", tc.name, rr.Code, rr.Body.String(), tc.want)
		}
	}
	if n := len(e.sample.recorded("oneOne")); n != 0 {
		t.Errorf("an invalid call must not reach the server, got %d calls", n)
	}

	// the payload may as well be YAML
	yaml := fmt.Sprintf("service: SampleService\nmethod: oneOne\nendpoint: %s\nresult: true\npayloads:\n  linear:\n  - payload: '{\"content\": \"y\"}'\n", e.addr)
	rr := callAPI(t, grpcRouter, "POST", "/grpc/client/call", yaml)
	if rr.Code != http.StatusOK {
		t.Fatalf("yaml call = %d %q", rr.Code, rr.Body.String())
	}
	if results := parseResults(t, rr); len(results) != 1 || results[0].Payload[0]["content"] != "re:y" {
		t.Errorf("yaml call results = %+v", results)
	}
}

func TestCallAPIUpstreamProblems(t *testing.T) {
	e := newEnv(t)
	grpcRouter, _ := apiRouters()

	rr := callAPI(t, grpcRouter, "POST", "/grpc/client/call", callJSON(e, "NoSuchService", "oneOne", `{"linear":[{"payload":"{}"}]}`, ""))
	if rr.Code != http.StatusBadRequest || rr.Body.Len() == 0 {
		t.Errorf("unknown service: %d %q, want 400 with the reason", rr.Code, rr.Body.String())
	}
	rr = callAPI(t, grpcRouter, "POST", "/grpc/client/call", callJSON(e, "SampleService", "noSuchMethod", `{"linear":[{"payload":"{}"}]}`, ""))
	if rr.Code != http.StatusBadRequest || rr.Body.Len() == 0 {
		t.Errorf("unknown method: %d %q, want 400 with the reason", rr.Code, rr.Body.String())
	}

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := ln.Addr().String()
	ln.Close()
	rr = callAPI(t, grpcRouter, "POST", "/grpc/client/call", fmt.Sprintf(`{"service":"SampleService","method":"oneOne","endpoint":%q,"payloads":{"linear":[{"payload":"{}"}]}}`, dead))
	if rr.Code != http.StatusBadRequest || rr.Body.Len() == 0 {
		t.Errorf("dead upstream: %d %q, want 400 with the reason", rr.Code, rr.Body.String())
	}

	// a call that fails at the upstream is reported as unavailable, with the error
	rr = callAPI(t, grpcRouter, "POST", "/grpc/client/call", callJSON(e, "SampleService", "oneOne", `{"linear":[{"payload":"{\"content\":\"fail\"}"}]}`, ""))
	if rr.Code != http.StatusServiceUnavailable || !strings.Contains(rr.Body.String(), "no such thing") {
		t.Errorf("failed call: %d %q, want 503 with the upstream error", rr.Code, rr.Body.String())
	}
	if n := len(e.sample.recorded("oneOne")); n != 1 {
		t.Errorf("only the failing call reaches the server, got %d", n)
	}
}

func TestCallServiceMethodAPI(t *testing.T) {
	e := newEnv(t)
	grpcRouter, clientRouter := apiRouters()
	for _, path := range []struct {
		router *mux.Router
		path   string
	}{
		{grpcRouter, "/grpc/client/call/SampleService/oneOne/" + e.addr},
		{clientRouter, "/client/grpc/call/SampleService/oneOne/" + e.addr + "/"},
	} {
		rr := callAPI(t, path.router, "POST", path.path, `{"id":5,"content":"m"}`)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s = %d %q", path.path, rr.Code, rr.Body.String())
		}
		results := parseResults(t, rr)
		if len(results) != 1 || results[0].Headers["x-reply-header"][0] != "h-m" {
			t.Errorf("%s results = %+v", path.path, results)
		}
	}
	if n := len(e.sample.recorded("oneOne")); n != 2 {
		t.Errorf("server calls = %d, want 2", n)
	}

	// the headers of the request are not forwarded unless the call says so, and the trailers that the upstream
	// reports as via-goto are added to the response
	rr := callAPI(t, grpcRouter, "POST", "/grpc/client/call/SampleService/oneOne/"+e.addr, `{"content":"via"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("via call = %d %q", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Values(constants.HeaderViaGoto); len(got) != 1 || got[0] != "remote-goto(gRPC)" {
		t.Errorf("%s = %v, want the upstream goto marked as gRPC", constants.HeaderViaGoto, got)
	}

	rr = callAPI(t, grpcRouter, "POST", "/grpc/client/call/SampleService/oneOne/"+e.addr, `{"content":"fail"}`)
	if rr.Code != http.StatusServiceUnavailable || !strings.Contains(rr.Body.String(), "no such thing") {
		t.Errorf("failed call: %d %q", rr.Code, rr.Body.String())
	}
	rr = callAPI(t, grpcRouter, "POST", "/grpc/client/call/NoSuchService/oneOne/"+e.addr, `{}`)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("unknown service = %d, want 400", rr.Code)
	}
	rr = callAPI(t, grpcRouter, "POST", "/grpc/client/call/SampleService/oneOne/"+e.addr, `{"noSuchField":1}`)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("a payload that is not valid for the method = %d, want 503", rr.Code)
	}
}

func TestCallServiceMethodAPIStream(t *testing.T) {
	e := newEnv(t)
	grpcRouter, _ := apiRouters()
	// the stream route pushes the messages as they arrive
	rr := callAPI(t, grpcRouter, "POST", "/grpc/client/call/SampleService/oneMany/"+e.addr+"/stream", `{"id":2,"content":"p"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("stream call = %d %q", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"p-1"`) || !strings.Contains(body, `"p-2"`) {
		t.Errorf("a stream call pushes the messages: %q", body)
	}

	// the route without stream only has the result
	rr = callAPI(t, grpcRouter, "POST", "/grpc/client/call/SampleService/oneMany/"+e.addr, `{"id":2,"content":"q"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("call = %d %q", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), `"q-1"`) {
		t.Errorf("a call that is not a stream call does not push messages: %q", rr.Body.String())
	}
	results := parseResults(t, rr)
	if len(results) != 1 || results[0].Headers["x-reply-header"][0] != "h-q" {
		t.Errorf("results = %+v", results)
	}
}

func TestCallAPIClientStreamAndBidi(t *testing.T) {
	e := newEnv(t)
	grpcRouter, _ := apiRouters()
	start := time.Now()
	body := callJSON(e, "SampleService", "manyOne", `{"linear":[{"stream":["{\"content\":\"a\"}","{\"content\":\"b\"}"]}]}`, `,"result":true`)
	rr := callAPI(t, grpcRouter, "POST", "/grpc/client/call", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("client stream call = %d %q", rr.Code, rr.Body.String())
	}
	results := parseResults(t, rr)
	if len(results) != 1 || len(results[0].Payload) != 1 || results[0].Payload[0]["content"] != "a,b" {
		t.Errorf("client stream results = %+v", results)
	}
	if time.Since(start) > 20*time.Second {
		t.Errorf("a client stream call keeps the stream open for a second, not %s", time.Since(start))
	}

	body = callJSON(e, "SampleService", "manyMany", `{"linear":[{"stream":["{\"id\":1,\"content\":\"a\"}","{\"id\":2,\"content\":\"b\"}"]}]}`, `,"result":true`)
	rr = callAPI(t, grpcRouter, "POST", "/grpc/client/call", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("bidi call = %d %q", rr.Code, rr.Body.String())
	}
	results = parseResults(t, rr)
	if len(results) != 1 || len(results[0].Payload) != 2 || results[0].Payload[0]["content"] != "echo:a" || results[0].Payload[1]["content"] != "echo:b" {
		t.Errorf("bidi results = %+v", results)
	}
}
