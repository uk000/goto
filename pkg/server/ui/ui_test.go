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

package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"goto/pkg/global"
	"goto/pkg/server/middleware"
	"goto/pkg/util"
	"goto/pkg/watch"

	"github.com/gorilla/websocket"
	"gopkg.in/yaml.v3"
)

const wait = 10 * time.Second

var setupOnce sync.Once

func setup() {
	setupOnce.Do(func() {
		global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
		global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
		global.Funcs.IsListenerPresent = func(int) bool { return false }
		global.Funcs.IsListenerOpen = func(int) bool { return false }
		global.Funcs.GetListenerID = func(int) string { return "" }
		global.Funcs.GetListenerLabel = func(*http.Request) string { return "" }
		global.Funcs.GetListenerLabelForPort = func(int) string { return "" }
		util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
		Middleware.SetRoutes(nil)
	})
}

func newRequest(method, target string) *http.Request {
	setup()
	req := httptest.NewRequest(method, target, nil)
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, 8093))
	_, req, _ = util.WithRequestStore(req, nil)
	return req
}

func serveUI(method, target string) *httptest.ResponseRecorder {
	setup()
	rr := httptest.NewRecorder()
	middleware.RootRouters["/ui"].ServeHTTP(rr, newRequest(method, target))
	return rr
}

func TestUIPageIsServed(t *testing.T) {
	for _, tt := range []struct{ target, file string }{
		{"/ui", "static/index.html"},
		{"/ui/api", "static/api-console.html"},
	} {
		want, err := staticUI.ReadFile(tt.file)
		if err != nil || len(want) == 0 {
			t.Fatalf("cannot read %s: %d bytes, %v", tt.file, len(want), err)
		}
		rr := serveUI("GET", tt.target)
		if rr.Code != http.StatusOK {
			t.Errorf("GET %s = %d %q, want 200", tt.target, rr.Code, rr.Body.String())
			continue
		}
		if rr.Body.String() != string(want) {
			t.Errorf("GET %s: body of %d bytes, want the %d bytes of %s", tt.target, rr.Body.Len(), len(want), tt.file)
		}
		if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("GET %s: content type = %q, want HTML", tt.target, ct)
		}
		if !strings.Contains(strings.ToLower(rr.Body.String()), "<html") {
			t.Errorf("GET %s: body is not an HTML page", tt.target)
		}
	}
}

func TestUIPagesAreDifferent(t *testing.T) {
	if a, b := serveUI("GET", "/ui").Body.String(), serveUI("GET", "/ui/api").Body.String(); a == b {
		t.Error("the UI and the API console give the same page")
	}
}

func TestUIRoutesAreForGETOnly(t *testing.T) {
	for _, method := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		for _, target := range []string{"/ui", "/ui/api", "/ui/ws"} {
			if rr := serveUI(method, target); rr.Code != http.StatusNotFound && rr.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s = %d, want it not to be a route", method, target, rr.Code)
			}
		}
	}
	for _, target := range []string{"/ui/nothing", "/ui/static/index.html", "/ui/api/more"} {
		if rr := serveUI("GET", target); rr.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", target, rr.Code)
		}
	}
}

func TestUIWebSocketRouteWithoutAnUpgradeIsRefused(t *testing.T) {
	if rr := serveUI("GET", "/ui/ws"); rr.Code < 400 {
		t.Errorf("GET /ui/ws without an upgrade = %d %q, want it refused", rr.Code, rr.Body.String())
	}
	if watch.WebSockets.HasOpenSockets() {
		t.Error("a request that is not an upgrade must not add a socket")
	}
}

func TestConcurrentRequestsForThePagesGetThemWhole(t *testing.T) {
	want, _ := staticUI.ReadFile("static/api-console.html")
	const requests = 20
	got := make([]string, requests)
	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i] = serveUI("GET", "/ui/api").Body.String()
		}()
	}
	wg.Wait()
	for i, body := range got {
		if body != string(want) {
			t.Errorf("request %d got %d bytes, want the %d bytes of the page", i, len(body), len(want))
		}
	}
}

// wsEnv is a server of the UI routes that take websockets, with the clients that are connected to it.
type wsEnv struct {
	srv *httptest.Server
}

func newWSEnv(t *testing.T) *wsEnv {
	t.Helper()
	setup()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(context.WithValue(r.Context(), util.CurrentPortKey, 8093))
		_, r, _ = util.WithRequestStore(r, w)
		middleware.RootRouters["/ui"].ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return &wsEnv{srv: srv}
}

// connect opens a websocket, which is taken out of the sockets when the test is done.
func (e *wsEnv) connect(t *testing.T) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(e.srv.URL, "http") + "/ui/ws"
	c, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("cannot connect: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", resp.StatusCode)
	}
	// The socket is known by the address that the server sees of the client. It is added after the upgrade is answered.
	id := c.LocalAddr().String()
	t.Cleanup(func() {
		c.Close()
		watch.WebSockets.RemoveSocket(id)
	})
	deadline := time.Now().Add(wait)
	for {
		if _, ok := watch.WebSockets.GetSocket(id); ok {
			return c
		}
		if time.Now().After(deadline) {
			t.Fatalf("the socket of %s is not added", id)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// next is what the middleware is in front of: it responds with a status, a header and a body.
func next(status int, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-From-Next", "yes")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	})
}

func readMessage(t *testing.T, c *websocket.Conn) watch.SocketMessage {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(wait))
	_, data, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("cannot read a message: %v", err)
	}
	var msg watch.SocketMessage
	if err := yaml.Unmarshal(data, &msg); err != nil {
		t.Fatalf("message %q is not YAML of a socket message: %v", data, err)
	}
	return msg
}

func TestMiddlewareWithoutSocketsPassesTheRequestOnAsItIs(t *testing.T) {
	setup()
	if watch.WebSockets.HasOpenSockets() {
		t.Fatal("no sockets are expected to be open")
	}
	rr := httptest.NewRecorder()
	var seen http.ResponseWriter
	req := newRequest("GET", "/some/uri")
	Middleware.MiddlewareHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = w
		next(http.StatusAccepted, "answer").ServeHTTP(w, r)
	})).ServeHTTP(rr, req)
	if seen != http.ResponseWriter(rr) {
		t.Error("the response writer is replaced when no sockets are open, want it as it is")
	}
	if rr.Code != http.StatusAccepted || rr.Body.String() != "answer" || rr.Header().Get("X-From-Next") != "yes" {
		t.Errorf("response = %d %q %v, want what the next handler gave", rr.Code, rr.Body.String(), rr.Header())
	}
}

func TestMiddlewareWithoutANextHandlerDoesNotPanic(t *testing.T) {
	setup()
	rr := httptest.NewRecorder()
	Middleware.MiddlewareHandler(nil).ServeHTTP(rr, newRequest("GET", "/some/uri"))
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}

func TestSocketGetsTheRequestsAndResponsesThatPassThrough(t *testing.T) {
	e := newWSEnv(t)
	c := e.connect(t)

	for i := 0; i < 3; i++ {
		req := newRequest("POST", fmt.Sprintf("/traffic/%d?x=y", i))
		req.Header.Set("X-Request-Header", fmt.Sprintf("value-%d", i))
		rr := httptest.NewRecorder()
		Middleware.MiddlewareHandler(next(http.StatusAccepted, fmt.Sprintf("body-%d", i))).ServeHTTP(rr, req)

		// The response goes to the client as it would without the sockets.
		if rr.Code != http.StatusAccepted || rr.Body.String() != fmt.Sprintf("body-%d", i) || rr.Header().Get("X-From-Next") != "yes" {
			t.Fatalf("response = %d %q %v, want what the next handler gave", rr.Code, rr.Body.String(), rr.Header())
		}
		msg := readMessage(t, c)
		if msg.Request.URI != fmt.Sprintf("/traffic/%d?x=y", i) {
			t.Errorf("message %d: uri = %q, want the one of the request", i, msg.Request.URI)
		}
		if got := msg.Request.Headers.Get("X-Request-Header"); got != fmt.Sprintf("value-%d", i) {
			t.Errorf("message %d: request header = %q, want value-%d", i, got, i)
		}
		if msg.Response.StatusCode != http.StatusAccepted || msg.Response.Body != fmt.Sprintf("body-%d", i) {
			t.Errorf("message %d: response = %d %q, want 202 body-%d", i, msg.Response.StatusCode, msg.Response.Body, i)
		}
		if got := msg.Response.Headers.Get("X-From-Next"); got != "yes" {
			t.Errorf("message %d: response header = %q, want the header of the response", i, got)
		}
	}
}

func TestEverySocketGetsTheTraffic(t *testing.T) {
	e := newWSEnv(t)
	clients := []*websocket.Conn{e.connect(t), e.connect(t), e.connect(t)}
	rr := httptest.NewRecorder()
	Middleware.MiddlewareHandler(next(http.StatusOK, "to all")).ServeHTTP(rr, newRequest("GET", "/to-all"))
	for i, c := range clients {
		if msg := readMessage(t, c); msg.Request.URI != "/to-all" || msg.Response.Body != "to all" {
			t.Errorf("client %d: message = %+v, want the traffic", i, msg)
		}
	}
}

func TestSocketsStopGettingTrafficWhenRemoved(t *testing.T) {
	e := newWSEnv(t)
	c := e.connect(t)
	if !watch.WebSockets.HasOpenSockets() {
		t.Fatal("the socket is expected to be open")
	}
	watch.WebSockets.RemoveSocket(c.LocalAddr().String())
	if watch.WebSockets.HasOpenSockets() {
		t.Fatal("the socket is expected to be gone")
	}

	var seen http.ResponseWriter
	rr := httptest.NewRecorder()
	Middleware.MiddlewareHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = w })).ServeHTTP(rr, newRequest("GET", "/after-removal"))
	if seen != http.ResponseWriter(rr) {
		t.Error("the response writer is replaced after the sockets are gone, want it as it is")
	}
}
