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

package watch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"goto/pkg/global"
	"goto/pkg/server/middleware"
	"goto/pkg/util"

	"github.com/gorilla/websocket"
)

var (
	stubOnce   sync.Once
	routesOnce sync.Once
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

// router is the router that serves the APIs of the watch, which the middleware package keeps for the /watch path.
func router() http.Handler {
	routesOnce.Do(func() {
		stubOnce.Do(stubGlobalFuncs)
		Middleware.SetRoutes(nil)
	})
	return middleware.RootRouters["/watch"]
}

func newRequest(method, target string, body io.Reader) *http.Request {
	stubOnce.Do(stubGlobalFuncs)
	req := httptest.NewRequest(method, target, body)
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, 8080))
	_, req, _ = util.WithRequestStore(req, nil)
	return req
}

func callAPI(t *testing.T, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	h := router()
	rr := httptest.NewRecorder()
	within(t, method+" "+target, func() { h.ServeHTTP(rr, newRequest(method, target, nil)) })
	return rr
}

// serveThroughMiddleware serves a request through the middleware of the watch, with the handler that is given at the end.
func serveThroughMiddleware(t *testing.T, next http.Handler, target string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := newRequest("GET", target, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Add(headers[i], headers[i+1])
	}
	rr := httptest.NewRecorder()
	h := Middleware.MiddlewareHandler(next)
	within(t, "the middleware", func() { h.ServeHTTP(rr, req) })
	return rr
}

// watchSockets connects a client to the package-wide websockets, the way the UI does, and returns the client. The socket
// is taken out when the test is done.
func watchSockets(t *testing.T) *websocket.Conn {
	t.Helper()
	server, client := wsPair(t)
	id := client.LocalAddr().String()
	WebSockets.AddSocket(id, server)
	t.Cleanup(func() { WebSockets.RemoveSocket(id) })
	return client
}

func TestMiddlewareDefinition(t *testing.T) {
	if Middleware.Name != "watch" || Middleware.SetRoutes == nil || Middleware.MiddlewareHandler == nil {
		t.Errorf("middleware = %+v", Middleware)
	}
}

func TestMiddlewareWithoutSocketsPassesThrough(t *testing.T) {
	if WebSockets.HasOpenSockets() {
		t.Fatal("the package-wide sockets are not empty")
	}
	rr := serveThroughMiddleware(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Res", "v")
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, "the body")
	}), "/x")
	if rr.Code != http.StatusAccepted || rr.Body.String() != "the body" || rr.Header().Get("X-Res") != "v" {
		t.Errorf("response = %d %q %v", rr.Code, rr.Body.String(), rr.Header())
	}
}

func TestMiddlewareReportsRequestsAndResponsesToSockets(t *testing.T) {
	client := watchSockets(t)
	rr := serveThroughMiddleware(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Res", "v")
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, "payload")
	}), "/path?q=1", "X-Req", "yes")
	// The response is still sent.
	if rr.Code != http.StatusAccepted || rr.Body.String() != "payload" || rr.Header().Get("X-Res") != "v" {
		t.Errorf("response = %d %q %v", rr.Code, rr.Body.String(), rr.Header())
	}
	msg := readMessage(t, client)
	if msg.Request.URI != "/path?q=1" || msg.Request.Headers.Get("X-Req") != "yes" {
		t.Errorf("request = %+v", msg.Request)
	}
	if msg.Response.StatusCode != 202 || msg.Response.Body != "payload" || msg.Response.Headers.Get("X-Res") != "v" {
		t.Errorf("response = %+v", msg.Response)
	}
	expectNoMessage(t, client)
}

func TestMiddlewareReportsEveryRequestOnce(t *testing.T) {
	a, b := watchSockets(t), watchSockets(t)
	for i := 0; i < 3; i++ {
		serveThroughMiddleware(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, r.URL.Path)
		}), fmt.Sprintf("/r%d", i))
	}
	for name, client := range map[string]*websocket.Conn{"a": a, "b": b} {
		for i := 0; i < 3; i++ {
			if msg := readMessage(t, client); msg.Request.URI != fmt.Sprintf("/r%d", i) || msg.Response.Body != fmt.Sprintf("/r%d", i) {
				t.Errorf("client %s message %d = %+v", name, i, msg)
			}
		}
		expectNoMessage(t, client)
	}
}

func TestMiddlewareWithoutNextHandler(t *testing.T) {
	client := watchSockets(t)
	rr := serveThroughMiddleware(t, nil, "/nothing")
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if msg := readMessage(t, client); msg.Request.URI != "/nothing" || msg.Response.Body != "" {
		t.Errorf("message = %+v", msg)
	}
}

func TestMiddlewareStopsReportingWhenSocketsAreGone(t *testing.T) {
	client := watchSockets(t)
	WebSockets.RemoveSocket(client.LocalAddr().String())
	rr := serveThroughMiddleware(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}), "/x")
	if rr.Code != http.StatusTeapot {
		t.Errorf("status = %d, want 418", rr.Code)
	}
	expectClosed(t, client)
}

func TestWebsocketAPIRegistersTheConnection(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handleWebsocket(w, r)
	}))
	t.Cleanup(ts.Close)
	client, _, err := (&websocket.Dialer{HandshakeTimeout: deadline}).Dial("ws"+strings.TrimPrefix(ts.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	id := client.LocalAddr().String()
	t.Cleanup(func() { WebSockets.RemoveSocket(id); client.Close() })
	waitFor(t, "the socket to be registered", func() bool { _, ok := WebSockets.GetSocket(id); return ok })
	if !WebSockets.HasOpenSockets() {
		t.Error("the connection is not among the open sockets")
	}
	WebSockets.Broadcast("/seen", nil, 200, nil, []byte("hello"))
	if msg := readMessage(t, client); msg.Request.URI != "/seen" || msg.Response.Body != "hello" {
		t.Errorf("message = %+v", msg)
	}
}

func TestWebsocketAPIRefusesRequestsThatAreNotUpgrades(t *testing.T) {
	rr := callAPI(t, "GET", "/watch/ws")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
	if WebSockets.HasOpenSockets() {
		t.Error("a request that was not an upgrade registered a socket")
	}
}

func TestAddWebhookWatcherAPI(t *testing.T) {
	name := "api-watcher-1"
	t.Cleanup(func() { watch.removeWatcher(name) })
	rr := callAPI(t, "POST", "/watch/add/"+name+"?url=http://hook.example/x")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rr.Code, rr.Body.String())
	}
	if want := "Registered Webhook Watch: Name [api-watcher-1], URL [http://hook.example/x]\n"; rr.Body.String() != want {
		t.Errorf("body = %q, want %q", rr.Body.String(), want)
	}
	w := get(watch, name)
	if w == nil || w.Name != name || w.URL != "http://hook.example/x" {
		t.Fatalf("watcher = %+v", w)
	}
	// A watcher of the same name is replaced.
	callAPI(t, "POST", "/watch/add/"+name+"?url=http://other")
	if w := get(watch, name); w == nil || w.URL != "http://other" {
		t.Errorf("watcher after adding it again = %+v", w)
	}
}

func TestAddWebhookWatcherAPIRoutesAreChecked(t *testing.T) {
	for _, tt := range []struct{ method, target string }{
		{"POST", "/watch/add/no-url"},
		{"GET", "/watch/add/wrong-method?url=http://x"},
		{"POST", "/watch/ws"},
	} {
		rr := callAPI(t, tt.method, tt.target)
		if rr.Code != http.StatusNotFound && rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d, want it to be refused", tt.method, tt.target, rr.Code)
		}
	}
	if w := get(watch, "no-url"); w != nil {
		t.Errorf("a request without a URL registered %+v", w)
	}
	if w := get(watch, "wrong-method"); w != nil {
		t.Errorf("a request of the wrong method registered %+v", w)
	}
}

func newWatch() *Watch {
	return &Watch{Watchers: map[string]*Watcher{}}
}

// get is the watcher of the name. It is called through reflection, as the regression tests call getWatcher without
// using what it returns.
func get(w *Watch, name string) *Watcher {
	out := reflect.ValueOf(w.getWatcher).Call([]reflect.Value{reflect.ValueOf(name)})
	if len(out) == 0 {
		return nil
	}
	got, _ := out[0].Interface().(*Watcher)
	return got
}

func TestWatchAddAndGetWatcher(t *testing.T) {
	w := newWatch()
	if get(w, "a") != nil {
		t.Error("an empty watch has a watcher")
	}
	w.addWebhookWatcher("a", "http://a")
	w.addWebhookWatcher("b", "http://b")
	if got := get(w, "a"); got == nil || got.Name != "a" || got.URL != "http://a" {
		t.Errorf("watcher a = %+v", got)
	}
	if len(w.Watchers) != 2 {
		t.Errorf("watchers = %d, want 2", len(w.Watchers))
	}
}

func TestWatchRemoveWatcher(t *testing.T) {
	w := newWatch()
	w.addWebhookWatcher("a", "http://a")
	w.addWebhookWatcher("b", "http://b")
	removed := w.removeWatcher("a")
	if removed == nil || removed.Name != "a" {
		t.Errorf("removed = %+v, want the watcher that was removed", removed)
	}
	if get(w, "a") != nil || get(w, "b") == nil {
		t.Errorf("watchers left = %v", w.Watchers)
	}
	if w.removeWatcher("a") != nil {
		t.Error("a watcher was removed twice")
	}
}

func TestWatchAddWatcherFilter(t *testing.T) {
	w := newWatch()
	if w.addWatcherFilter("missing", "/x", "", "") {
		t.Error("a filter was added to a watcher that is not there")
	}
	w.addWebhookWatcher("a", "http://a")
	if w.addWatcherFilter("a", "", "", "") {
		t.Error("an empty filter was reported as added")
	}
	if !w.addWatcherFilter("a", "/Some/Path", "", "") {
		t.Fatal("a URI filter was not added")
	}
	if !w.addWatcherFilter("a", "", "X-Header", "value") {
		t.Fatal("a header filter was not added")
	}
	if !w.addWatcherFilter("a", "/other", "X-Two", "2") {
		t.Fatal("a filter of a URI and a header was not added")
	}
	got := get(w, "a")
	if want := map[string]bool{"/some/path": true, "/other": true}; !reflect.DeepEqual(got.FilterURIs, want) {
		t.Errorf("URI filters = %v, want %v, in lower case", got.FilterURIs, want)
	}
	if want := map[string]string{"X-Header": "value", "X-Two": "2"}; !reflect.DeepEqual(got.FilterHeaders, want) {
		t.Errorf("header filters = %v, want %v", got.FilterHeaders, want)
	}
}

func TestWatcherJSONFields(t *testing.T) {
	typ := reflect.TypeOf(Watcher{})
	for field, tag := range map[string]string{"Name": "name", "URL": "url", "FilterURIs": "filterURIs", "FilterHeaders": "filterHeaders"} {
		if f, ok := typ.FieldByName(field); !ok || f.Tag.Get("json") != tag {
			t.Errorf("Watcher.%s = %v, want json tag %q", field, f.Tag, tag)
		}
	}
}

func TestConcurrentWatchUse(t *testing.T) {
	w := newWatch()
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("w%d", i)
			w.addWebhookWatcher(name, "http://"+name)
			if !w.addWatcherFilter(name, "/u", "H", "v") {
				errs <- fmt.Errorf("%s: filter not added", name)
			}
			if get(w, name) == nil {
				errs <- fmt.Errorf("%s: watcher not found", name)
			}
			if w.removeWatcher(name) == nil {
				errs <- fmt.Errorf("%s: watcher not removed", name)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if len(w.Watchers) != 0 {
		t.Errorf("watchers left = %v", w.Watchers)
	}
}
