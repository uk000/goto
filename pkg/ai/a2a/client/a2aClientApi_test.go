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

package a2aclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"goto/pkg/constants"
	"goto/pkg/server/middleware"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

var routesOnce sync.Once

// clientRouter returns the router that serves the /a2a routes of the client.
func clientRouter(t *testing.T) *mux.Router {
	t.Helper()
	setup(t)
	routesOnce.Do(func() { Middleware.SetRoutes(nil) })
	r := middleware.RootRouters["/a2a"]
	if r == nil {
		t.Fatal("the a2a routes were not registered")
	}
	return r
}

// serveAPI calls the API the way the server does: the request has a request store, and the port of the listener.
func serveAPI(t *testing.T, method, target, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	router := clientRouter(t)
	req := httptest.NewRequest(method, target, strings.NewReader(body)).WithContext(context.WithValue(context.Background(), util.CurrentPortKey, 4242))
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Add(headers[i], headers[i+1])
	}
	_, req, _ = util.WithRequestStore(req, nil)
	ctx, cancel := context.WithTimeout(req.Context(), callTimeout)
	defer cancel()
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req.WithContext(ctx))
	return rr
}

func callBody(call map[string]any) string {
	b, _ := json.Marshal(call)
	return string(b)
}

func decodeObject(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	m := map[string]any{}
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("response %d is not a JSON object: %v\n%s", rr.Code, err, rr.Body.String())
	}
	return m
}

func TestMiddlewareDefinition(t *testing.T) {
	if Middleware.Name != "a2a" || Middleware.SetRoutes == nil {
		t.Errorf("middleware = %+v", Middleware)
	}
}

func TestAPIFetchAgentCard(t *testing.T) {
	u := newUpstream(t, "api-card", true, echoProcessor)
	rr := serveAPI(t, "GET", "/a2a/client/agent/card?url="+url.QueryEscape(u.URL), "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	card := decodeObject(t, rr)
	if card["name"] != "api-card" || card["url"] != u.URL {
		t.Errorf("card = %v", card)
	}
}

func TestAPIFetchAgentCardWithAuthority(t *testing.T) {
	u := newUpstream(t, "api-card-auth", false, echoProcessor)
	rr := serveAPI(t, "GET", "/a2a/client/agent/card?url="+url.QueryEscape(u.URL)+"&authority=agents.example.com", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	if got := u.requests()[0].Host; got != "agents.example.com" {
		t.Errorf("host = %q, want the authority", got)
	}
}

func TestAPIFetchAgentCardFailures(t *testing.T) {
	u := newUpstream(t, "api-card-fail", false, echoProcessor)
	u.cardStatus.Store(http.StatusNotFound)
	rr := serveAPI(t, "GET", "/a2a/client/agent/card?url="+url.QueryEscape(u.URL), "")
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Error fetching agent card") {
		t.Errorf("response = %d %q, want a bad request", rr.Code, rr.Body.String())
	}
	// There is no route without the URL.
	if rr := serveAPI(t, "GET", "/a2a/client/agent/card", ""); rr.Code != http.StatusNotFound && rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d without url, want the request not to be routed", rr.Code)
	}
}

func TestAPICallAgent(t *testing.T) {
	u := newUpstream(t, "api-call", false, echoProcessor)
	body := callBody(map[string]any{"name": "api-call", "cardURL": u.URL, "message": "api hello", "args": map[string]any{"k": "v"}})
	for _, target := range []string{"/a2a/client/call", "/a2a/client/call/result", "/a2a/client/agent/api-call/call"} {
		t.Run(target, func(t *testing.T) {
			rr := serveAPI(t, "POST", target, body)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
			}
			out := decodeObject(t, rr)
			if _, found := out["headers"]; !found {
				t.Errorf("response = %v, want the headers", out)
			}
			if !strings.Contains(rr.Body.String(), "echo: api hello") {
				t.Errorf("response = %s, want the reply of the agent", rr.Body.String())
			}
		})
	}
	if got := len(u.calls()); got != 3 {
		t.Errorf("agent got %d calls, want 3", got)
	}
}

func TestAPICallAgentNameFromPath(t *testing.T) {
	u := newUpstream(t, "path-name", false, echoProcessor)
	body := callBody(map[string]any{"cardURL": u.URL, "message": "hi"})
	rr := serveAPI(t, "POST", "/a2a/client/agent/renamed/call", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "[renamed @") {
		t.Errorf("response = %s, want the name of the path to be the name of the call", rr.Body.String())
	}
}

func TestAPICallAgentForwardsHeaders(t *testing.T) {
	u := newUpstream(t, "api-hdr", false, echoProcessor)
	body := callBody(map[string]any{"name": "api-hdr", "cardURL": u.URL, "message": "hi",
		"headers": map[string]any{"request": map[string]any{"add": map[string]string{"X-Added": "a"}, "forward": []string{"X-Fwd"}}}})
	rr := serveAPI(t, "POST", "/a2a/client/call", body, "X-Fwd", "forwarded")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	h := u.calls()[0].Headers
	if h.Get("X-Added") != "a" || h.Get("X-Fwd") != "forwarded" {
		t.Errorf("headers = %v", h)
	}
}

func TestAPICallAgentStream(t *testing.T) {
	u := newUpstream(t, "api-stream", true, streamProcessor("first update", "second update"))
	body := callBody(map[string]any{"name": "api-stream", "cardURL": u.URL, "message": "go"})
	rr := serveAPI(t, "POST", "/a2a/client/call/stream", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	out := rr.Body.String()
	i1, i2 := strings.Index(out, "first update"), strings.Index(out, "second update")
	if i1 < 0 || i2 < i1 {
		t.Errorf("response = %s, want the updates streamed in order", out)
	}
}

func TestAPICallAgentErrors(t *testing.T) {
	t.Run("bad payload", func(t *testing.T) {
		rr := serveAPI(t, "POST", "/a2a/client/call", "{not valid")
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Failed to parse payload") {
			t.Errorf("response = %d %q", rr.Code, rr.Body.String())
		}
	})
	t.Run("no card url", func(t *testing.T) {
		rr := serveAPI(t, "POST", "/a2a/client/call", callBody(map[string]any{"name": "x"}))
		if rr.Code != http.StatusBadGateway {
			t.Errorf("status = %d, want 502 for a call without a response", rr.Code)
		}
	})
	t.Run("agent error status", func(t *testing.T) {
		u := newUpstream(t, "api-err", false, echoProcessor)
		u.forcedStatus.Store(http.StatusServiceUnavailable)
		rr := serveAPI(t, "POST", "/a2a/client/call", callBody(map[string]any{"name": "api-err", "cardURL": u.URL, "message": "hi"}))
		if rr.Code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want the status of the agent (503): %s", rr.Code, rr.Body.String())
		}
	})
	t.Run("card unreachable", func(t *testing.T) {
		setup(t)
		srv := httptest.NewServer(http.NotFoundHandler())
		url := srv.URL
		srv.Close()
		rr := serveAPI(t, "POST", "/a2a/client/call", callBody(map[string]any{"name": "gone", "cardURL": url}))
		if rr.Code != http.StatusBadGateway {
			t.Errorf("status = %d, want 502", rr.Code)
		}
	})
}

func TestAPICallAgentReportsViaGotos(t *testing.T) {
	u := newUpstream(t, "api-via", false, echoProcessor)
	u.setResponseHeader(constants.HeaderViaGoto, "remote-goto")
	rr := serveAPI(t, "POST", "/a2a/client/call", callBody(map[string]any{"name": "api-via", "cardURL": u.URL, "message": "hi"}))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	if got := strings.Join(rr.Header().Values(constants.HeaderViaGoto), ","); !strings.Contains(got, "remote-goto") {
		t.Errorf("%s = %q, want the remote goto", constants.HeaderViaGoto, got)
	}
}

func TestAPIPushReceiver(t *testing.T) {
	if rr := serveAPI(t, "POST", "/a2a/client/push", `{"id":"task-1","status":{"state":"completed"}}`); rr.Code != http.StatusOK {
		t.Errorf("valid notification = %d %q, want 200", rr.Code, rr.Body.String())
	}
	rr := serveAPI(t, "POST", "/a2a/client/push", `not json`)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Error parsing notification JSON") {
		t.Errorf("invalid notification = %d %q, want 400", rr.Code, rr.Body.String())
	}
}

func TestStreamAgentResponseUnary(t *testing.T) {
	setup(t)
	output := map[string]map[string]any{}
	req := httptest.NewRequest("POST", "/x", nil)
	_, req, _ = util.WithRequestStore(req, nil)
	cb := streamAgentResponse("agent", false, output, httptest.NewRecorder(), req)
	cb("r1", "a message", nil)
	cb("r1", "another", nil)
	cb("r1", "", map[string]any{"single": 1})
	cb("r1", "multi", map[string]any{"a": 1, "b": 2})
	if msgs, _ := output["r1"]["messages"].([]string); len(msgs) != 2 || msgs[0] != "a message" || msgs[1] != "another" {
		t.Errorf("messages = %v", output["r1"]["messages"])
	}
	if output["r1"]["single"] != 1 {
		t.Errorf("a single value of data must be set under its own key: %v", output["r1"])
	}
	if m, ok := output["r1"]["multi"].(map[string]any); !ok || m["b"] != 2 {
		t.Errorf("data with several values must be set under the message: %v", output["r1"])
	}
}

func TestStreamAgentResponseStreams(t *testing.T) {
	setup(t)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/x", nil)
	_, req, _ = util.WithRequestStore(req, nil)
	output := map[string]map[string]any{}
	cb := streamAgentResponse("agent", true, output, rr, req)
	cb("r1", "streamed text", nil)
	if !rr.Flushed || !strings.Contains(rr.Body.String(), "streamed text") {
		t.Errorf("flushed = %v, body = %q", rr.Flushed, rr.Body.String())
	}
	if len(output) != 0 {
		t.Errorf("output = %v, want nothing collected when streaming", output)
	}
}
