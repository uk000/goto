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

package middleware

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"goto/pkg/global"
	"goto/pkg/util"

	"github.com/gorilla/mux"
	"google.golang.org/grpc/peer"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

var (
	stubOnce sync.Once
	pathSeq  atomic.Int32
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

// saveChains restores the middlewares and chains that tests set, when the test is over.
func saveChains(t *testing.T) {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
	core, interceptedCore, unintercepted, intercepted, routesOnly := Core, InterceptedCore, Unintercepted, Intercepted, RoutesOnly
	grpcHead, head, router := MiddlewareGRPCChainHead, MiddlewareChainHead, middlewareRouter
	Core, InterceptedCore, Unintercepted, Intercepted, RoutesOnly = nil, nil, nil, nil, nil
	MiddlewareGRPCChainHead, MiddlewareChainHead = nil, nil
	t.Cleanup(func() {
		Core, InterceptedCore, Unintercepted, Intercepted, RoutesOnly = core, interceptedCore, unintercepted, intercepted, routesOnly
		MiddlewareGRPCChainHead, MiddlewareChainHead, middlewareRouter = grpcHead, head, router
	})
}

// recorder is a log that the middlewares of a test write to.
type recorder struct {
	mu     sync.Mutex
	events []string
}

func (r *recorder) add(e string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recorder) get() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string{}, r.events...)
}

// tracing is a middleware that logs when it is entered and when it is left, and the one that it is given as its name.
func (r *recorder) tracing(name string) *Middleware {
	return NewMiddleware(name, nil, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			r.add(name + ">")
			next.ServeHTTP(w, req)
			r.add("<" + name)
		})
	})
}

func (r *recorder) terminal(name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.add(name)
		fmt.Fprint(w, name)
	})
}

func newRequest(method, target string) *http.Request {
	stubOnce.Do(stubGlobalFuncs)
	req := httptest.NewRequest(method, target, nil)
	_, req, _ = util.WithRequestStore(req, nil)
	return req
}

func TestNewMiddleware(t *testing.T) {
	routes := func(*mux.Router) {}
	handler := func(next http.Handler) http.Handler { return next }
	m := NewMiddleware("m", routes, handler)
	if m.Name != "m" || m.SetRoutes == nil || m.MiddlewareHandler == nil {
		t.Errorf("middleware = %+v, want the name, routes and handler that it was made with", m)
	}
	if m := NewMiddleware("bare", nil, nil); m.SetRoutes != nil || m.MiddlewareHandler != nil {
		t.Errorf("middleware = %+v, want one without routes or handler", m)
	}
}

func TestAddMiddlewaresWrapsInOrder(t *testing.T) {
	rec := &recorder{}
	skipped := NewMiddleware("no-handler", func(*mux.Router) {}, nil)
	h := AddMiddlewares(rec.terminal("end"), rec.tracing("a"), skipped, rec.tracing("b"), rec.tracing("c"))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, newRequest("GET", "/"))
	want := []string{"a>", "b>", "c>", "end", "<c", "<b", "<a"}
	if got := rec.get(); !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v: the first middleware is the outermost", got, want)
	}
	if rr.Body.String() != "end" {
		t.Errorf("body = %q, want the response of the last handler", rr.Body.String())
	}
	next := rec.terminal("only")
	if got := AddMiddlewares(next); reflect.ValueOf(got).Pointer() != reflect.ValueOf(next).Pointer() {
		t.Error("with no middlewares, the handler is given back as it is")
	}
}

func TestAddRoutes(t *testing.T) {
	r := mux.NewRouter()
	var calls []string
	mk := func(name string) *Middleware {
		return NewMiddleware(name, func(rt *mux.Router) {
			calls = append(calls, name)
			rt.HandleFunc("/"+name, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, name) })
		}, nil)
	}
	AddRoutes(r, mk("one"), NewMiddleware("none", nil, nil), mk("two"))
	if !reflect.DeepEqual(calls, []string{"one", "two"}) {
		t.Errorf("routes were set by %v, want one and two, once each, in order", calls)
	}
	for _, name := range []string{"one", "two"} {
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, httptest.NewRequest("GET", "/"+name, nil))
		if rr.Body.String() != name {
			t.Errorf("GET /%s = %q, want the route to be set", name, rr.Body.String())
		}
	}
}

func TestBaseHandlerFunc(t *testing.T) {
	t.Run("no getter responds OK", func(t *testing.T) {
		rr := httptest.NewRecorder()
		BaseHandlerFunc(nil).ServeHTTP(rr, newRequest("GET", "/"))
		if rr.Code != http.StatusOK || rr.Body.Len() != 0 {
			t.Errorf("response = %d %q, want an empty 200", rr.Code, rr.Body.String())
		}
	})
	t.Run("getter gives the handler that serves", func(t *testing.T) {
		rec := &recorder{}
		var calls atomic.Int32
		h := BaseHandlerFunc(func() http.Handler { calls.Add(1); return rec.terminal("linked") })
		for i := 0; i < 2; i++ {
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, newRequest("GET", "/"))
			if rr.Body.String() != "linked" {
				t.Errorf("body = %q, want the linked handler", rr.Body.String())
			}
		}
		if calls.Load() != 2 {
			t.Errorf("getter calls = %d, want one per request: the chain is looked up as requests come", calls.Load())
		}
	})
	t.Run("getter changes what it gives", func(t *testing.T) {
		var current atomic.Value
		current.Store("first")
		rec := &recorder{}
		h := BaseHandlerFunc(func() http.Handler { return rec.terminal(current.Load().(string)) })
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, newRequest("GET", "/"))
		current.Store("second")
		h.ServeHTTP(rr, newRequest("GET", "/"))
		if rr.Body.String() != "firstsecond" {
			t.Errorf("body = %q, want the handler that is current at each request", rr.Body.String())
		}
	})
}

func TestSetRoutesOnly(t *testing.T) {
	saveChains(t)
	rec := &recorder{}
	var routed []string
	RoutesOnly = []*Middleware{
		NewMiddleware("a", func(r *mux.Router) {
			routed = append(routed, "a")
			r.HandleFunc("/a", func(http.ResponseWriter, *http.Request) {})
		}, rec.tracing("a").MiddlewareHandler),
		NewMiddleware("b", nil, nil),
	}
	r := mux.NewRouter()
	SetRoutesOnly(r)
	if !reflect.DeepEqual(routed, []string{"a"}) {
		t.Errorf("routes set by %v, want only a", routed)
	}
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/a", nil))
	if len(rec.get()) != 0 {
		t.Errorf("middlewares ran for routes-only middlewares: %v, want none", rec.get())
	}
}

func TestLinkCoreAndUseCore(t *testing.T) {
	saveChains(t)
	rec := &recorder{}
	var routed []string
	core := func(name string) *Middleware {
		m := rec.tracing(name)
		m.SetRoutes = func(r *mux.Router) { routed = append(routed, name) }
		return m
	}
	Core = []*Middleware{core("c1"), NewMiddleware("c2", nil, nil), core("c3")}

	r := mux.NewRouter()
	r.HandleFunc("/x", func(w http.ResponseWriter, _ *http.Request) { rec.add("handler") })
	LinkCore(r)
	if !reflect.DeepEqual(routed, []string{"c1", "c3"}) {
		t.Errorf("routes set by %v, want c1 and c3", routed)
	}
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil))
	if got, want := rec.get(), []string{"c1>", "c3>", "handler", "<c3", "<c1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}

	// UseCore alone applies the middlewares and sets no routes.
	routed = nil
	r2 := mux.NewRouter()
	r2.HandleFunc("/x", func(http.ResponseWriter, *http.Request) {})
	UseCore(r2)
	if len(routed) != 0 {
		t.Errorf("UseCore set routes: %v", routed)
	}
}

func TestUseInterceptedCore(t *testing.T) {
	saveChains(t)
	rec := &recorder{}
	InterceptedCore = []*Middleware{rec.tracing("i1"), NewMiddleware("skip", nil, nil), rec.tracing("i2")}
	r := mux.NewRouter()
	r.HandleFunc("/x", func(http.ResponseWriter, *http.Request) { rec.add("handler") })
	UseInterceptedCore(r)
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil))
	if got, want := rec.get(), []string{"i1>", "i2>", "handler", "<i2", "<i1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// The chain of the GRPC calls has the middlewares that the HTTP requests go through, in the same order: the core ones,
// the intercepted core ones, and the unintercepted ones, and then the chain of the intercepted ones.
func TestLinkUninterceptedBuildsTheGRPCChain(t *testing.T) {
	saveChains(t)
	rec := &recorder{}
	var routed []string
	un := func(name string) *Middleware {
		m := rec.tracing(name)
		m.SetRoutes = func(r *mux.Router) { routed = append(routed, name) }
		return m
	}
	Core = []*Middleware{rec.tracing("core")}
	InterceptedCore = []*Middleware{rec.tracing("icore")}
	Unintercepted = []*Middleware{un("u1"), NewMiddleware("u2", nil, nil), un("u3")}
	MiddlewareChainHead = rec.terminal("intercepted-chain")

	r := mux.NewRouter()
	r.HandleFunc("/x", func(http.ResponseWriter, *http.Request) { rec.add("handler") })
	LinkUnintercepted(r)
	if !reflect.DeepEqual(routed, []string{"u1", "u3"}) {
		t.Errorf("routes set by %v, want u1 and u3", routed)
	}
	if middlewareRouter != r {
		t.Error("the router that was linked is not the one that is remembered")
	}
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil))
	if got, want := rec.get(), []string{"u1>", "u3>", "handler", "<u3", "<u1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("HTTP order = %v, want %v", got, want)
	}

	if MiddlewareGRPCChainHead == nil {
		t.Fatal("the GRPC chain was not linked")
	}
	rec.events = nil
	rr := httptest.NewRecorder()
	MiddlewareGRPCChainHead.ServeHTTP(rr, newRequest("GET", "/x"))
	want := []string{"core>", "icore>", "u1>", "u3>", "intercepted-chain", "<u3", "<u1", "<icore", "<core"}
	if got := rec.get(); !reflect.DeepEqual(got, want) {
		t.Errorf("GRPC order = %v, want %v", got, want)
	}
}

func TestLinkUninterceptedChainEndsAtTheInterceptedChainOnceItIsLinked(t *testing.T) {
	saveChains(t)
	rec := &recorder{}
	Unintercepted = []*Middleware{rec.tracing("u")}
	LinkUnintercepted(mux.NewRouter())
	// The intercepted chain is linked after this one, which the chain must find when requests come.
	MiddlewareChainHead = rec.terminal("late")
	rr := httptest.NewRecorder()
	MiddlewareGRPCChainHead.ServeHTTP(rr, newRequest("GET", "/"))
	if got, want := rec.get(), []string{"u>", "late", "<u"}; !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestLinkIntercepted(t *testing.T) {
	saveChains(t)
	rec := &recorder{}
	var routed []string
	m := rec.tracing("only")
	m.SetRoutes = func(r *mux.Router) { routed = append(routed, "only") }
	Intercepted = []*Middleware{m, NewMiddleware("skip", func(*mux.Router) { routed = append(routed, "skip") }, nil)}
	r := mux.NewRouter()
	r.HandleFunc("/x", func(http.ResponseWriter, *http.Request) { rec.add("handler") })
	LinkIntercepted(r)
	if !reflect.DeepEqual(routed, []string{"only", "skip"}) {
		t.Errorf("routes set by %v, want both, in order", routed)
	}
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil))
	if got, want := rec.get(), []string{"only>", "handler", "<only"}; !reflect.DeepEqual(got, want) {
		t.Errorf("HTTP order = %v, want %v", got, want)
	}
	if MiddlewareChainHead == nil {
		t.Fatal("the chain of the intercepted middlewares was not linked")
	}
	rec.events = nil
	rr := httptest.NewRecorder()
	MiddlewareChainHead.ServeHTTP(rr, newRequest("GET", "/x"))
	if got, want := rec.get(), []string{"only>", "<only"}; !reflect.DeepEqual(got, want) {
		t.Errorf("chain order = %v, want %v: the chain ends in the base handler, which responds OK", got, want)
	}
	if rr.Code != http.StatusOK {
		t.Errorf("chain status = %d, want 200", rr.Code)
	}
}

func TestLinkInterceptedWithoutMiddlewaresLeavesNoChain(t *testing.T) {
	saveChains(t)
	LinkIntercepted(mux.NewRouter())
	if MiddlewareChainHead != nil {
		t.Errorf("chain = %v, want none without intercepted middlewares", MiddlewareChainHead)
	}
}

func TestRootPathIfServesOnlyTheRequestsThatTheGuardAllows(t *testing.T) {
	saveChains(t)
	path := fmt.Sprintf("/mwguarded%d", pathSeq.Add(1))
	unguarded := fmt.Sprintf("/mwunguarded%d", pathSeq.Add(1))
	t.Cleanup(func() {
		delete(RootRouters, path)
		delete(RootGuards, path)
		delete(RootRouters, unguarded)
	})
	allowHost := func(host string) func(*http.Request) bool {
		return func(r *http.Request) bool { return r.Host == host }
	}

	r := RootPathIf(path, allowHost("a.example.com"))
	if r == nil || r != RootRouters[path] || r != RootPath(path) {
		t.Fatalf("RootPathIf = %v, want the router that RootPath gives for the path", r)
	}
	req := func(host string) *http.Request {
		q := newRequest("GET", path+"/x")
		q.Host = host
		return q
	}
	if got := RootRouterFor(path, req("a.example.com")); got != r {
		t.Errorf("router for a request that the guard allows = %v, want %v", got, r)
	}
	if got := RootRouterFor(path, req("b.example.com")); got != nil {
		t.Errorf("router for a request that the guard does not allow = %v, want none", got)
	}
	// A later guard replaces the earlier one, for the same router.
	if again := RootPathIf(path, allowHost("b.example.com")); again != r {
		t.Error("a second guard made another router for the same path")
	}
	if got := RootRouterFor(path, req("a.example.com")); got != nil {
		t.Errorf("router after the guard was replaced, for the host of the old one = %v, want none", got)
	}
	if got := RootRouterFor(path, req("b.example.com")); got != r {
		t.Errorf("router after the guard was replaced, for the host of the new one = %v, want %v", got, r)
	}
	// Without a guard, or without a router, there is no check.
	plain := RootPath(unguarded)
	if got := RootRouterFor(unguarded, req("any.example.com")); got != plain {
		t.Errorf("router of a root path without a guard = %v, want %v", got, plain)
	}
	if got := RootRouterFor("/mwmissing", req("a.example.com")); got != nil {
		t.Errorf("router of a root path that has none = %v, want none", got)
	}
}

func TestRootPath(t *testing.T) {
	saveChains(t)
	rec := &recorder{}
	Core = []*Middleware{rec.tracing("core")}
	InterceptedCore = []*Middleware{rec.tracing("icore")}
	path := fmt.Sprintf("/mwroot%d", pathSeq.Add(1))
	t.Cleanup(func() { delete(RootRouters, path) })

	r := RootPath(path)
	if r == nil || RootRouters[path] != r {
		t.Fatalf("RootPath = %v, want a router that is kept for the path", r)
	}
	if again := RootPath(path); again != r {
		t.Error("a second call made another router for the same path")
	}
	if other := RootPath(path + "x"); other == r {
		t.Error("another path shares a router")
	} else {
		delete(RootRouters, path+"x")
	}
	// The routes of the router are relative to its path.
	r.HandleFunc("/hello", func(w http.ResponseWriter, req *http.Request) {
		rec.add("handler")
		w.WriteHeader(http.StatusTeapot)
		fmt.Fprint(w, "hi")
	}).Methods("GET")

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, newRequest("GET", path+"/hello"))
	if got, want := rec.get(), []string{"core>", "icore>", "handler", "<icore", "<core"}; !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v: the core ones, intercepted, and then the handler", got, want)
	}
	if rr.Code != http.StatusTeapot || rr.Body.String() != "hi" {
		t.Errorf("response = %d %q, want 418 hi", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Goto-Response-Status"); got != "418" {
		t.Errorf("Goto-Response-Status = %q, want the status that the handler gave, 418", got)
	}
	// What is not under the path is not served by it.
	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, newRequest("GET", "/elsewhere"))
	if rr.Code != http.StatusNotFound {
		t.Errorf("GET /elsewhere = %d, want 404", rr.Code)
	}
}

// grpcChain is a chain head that responds as a GRPC server of strings does, with the given response and header.
func grpcChain(rec *recorder, response string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := ""
		if r.Body != nil {
			b := make([]byte, 64)
			n, _ := r.Body.Read(b)
			body = string(b[:n])
		}
		rec.add(fmt.Sprintf("%s %s host=%s uri=%s port=%d body=%q h2=%t", r.Method, r.RequestURI, r.Host, r.URL.Path, util.GetContextPort(r.Context()), body, r.ProtoMajor == 2))
		rec.add("header:" + r.Header.Get("X-In") + ":" + r.Header.Get("Content-Type"))
		w.Header().Set("X-Out", "yes")
		w.Header().Set(":status", "ok")
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, response)
	})
}

func TestInvokeMiddlewareChainForGRPC(t *testing.T) {
	saveChains(t)
	rec := &recorder{}
	MiddlewareGRPCChainHead = grpcChain(rec, `"hello"`)
	desc := wrapperspb.String("").ProtoReflect().Descriptor()
	ra, wa := InvokeMiddlewareChainForGRPC(context.Background(), 7001, "POST", "goto.test", "/pkg.Svc/Method", map[string][]string{"X-In": {"in1", "in2"}}, []byte("req"), desc)

	got := rec.get()
	if len(got) != 2 || got[0] != `POST /pkg.Svc/Method host=goto.test uri=/pkg.Svc/Method port=7001 body="req" h2=true` || got[1] != "header:in1:application/grpc" {
		t.Errorf("the chain saw %q, want the call as an HTTP/2 request on port 7001 with the headers of the call", got)
	}
	if ra == nil || ra.Method != "POST" || ra.Host != "goto.test" || ra.Header.Get("method") != "POST" || ra.Header.Get("uri") != "/pkg.Svc/Method" {
		t.Errorf("request adapter = %+v, want the call that was made", ra)
	}
	if wa.StatusCode != http.StatusAccepted {
		t.Errorf("status = %d, want the one that the chain gave, 202", wa.StatusCode)
	}
	if len(wa.Responses) != 1 {
		t.Fatalf("responses = %d, want 1", len(wa.Responses))
	}
	if v := wa.Responses[0].ProtoReflect().Get(desc.Fields().ByName("value")).String(); v != "hello" {
		t.Errorf("response value = %q, want hello", v)
	}
	md := wa.ToMetadata()
	if md["x-out"][0] != "yes" || md["status"][0] != "ok" {
		t.Errorf("metadata = %v, want the headers that the chain set, lowercased and without colons", md)
	}
}

func TestInvokeMiddlewareChainForGRPCWithEmptyResponse(t *testing.T) {
	saveChains(t)
	MiddlewareGRPCChainHead = grpcChain(&recorder{}, "")
	_, wa := InvokeMiddlewareChainForGRPC(context.Background(), 1, "POST", "h", "/a/b", nil, nil, wrapperspb.String("").ProtoReflect().Descriptor())
	if len(wa.Responses) != 0 || wa.StatusCode != http.StatusAccepted {
		t.Errorf("responses = %d, status = %d, want none and 202", len(wa.Responses), wa.StatusCode)
	}
}

func TestGrpcHTTPRequestAdapter(t *testing.T) {
	p := &peer.Peer{Addr: &net.TCPAddr{IP: net.IPv4(10, 1, 2, 3), Port: 4567}}
	ctx := peer.NewContext(context.Background(), p)
	a := NewGrpcHTTPRequestAdapter(ctx, "POST", "svc.test:80", "/pkg.Svc/Do?x=1", map[string][]string{"A": {"1", "2"}, "b": {"3"}}, []byte("payload"))
	if a.Method != "POST" || a.Host != "svc.test:80" || a.RequestURI != "/pkg.Svc/Do?x=1" || a.RemoteAddr != "10.1.2.3:4567" || a.ProtoMajor != 2 {
		t.Errorf("request = %s %s %s from %s h%d, want the call, from the peer, over HTTP/2", a.Method, a.Host, a.RequestURI, a.RemoteAddr, a.ProtoMajor)
	}
	if a.URL.Path != "/pkg.Svc/Do" || a.URL.Query().Get("x") != "1" {
		t.Errorf("url = %v, want the parsed uri", a.URL)
	}
	if got := a.Header.Values("A"); !reflect.DeepEqual(got, []string{"1", "2"}) || a.Header.Get("B") != "3" {
		t.Errorf("headers = %v, want the ones of the call", a.Header)
	}
	if a.Header.Get("Content-Type") != "application/grpc" || a.Header.Get("method") != "POST" || a.Header.Get("uri") != "/pkg.Svc/Do?x=1" {
		t.Errorf("headers = %v, want content type grpc, and the method and uri", a.Header)
	}
	if a.ContentLength != 7 {
		t.Errorf("content length = %d, want 7", a.ContentLength)
	}
	buf := make([]byte, 3)
	if n, _ := a.Read(buf); n != 3 || string(buf) != "pay" {
		t.Errorf("Read = %q, want the start of the payload", buf[:n])
	}
	if a.Ctx != ctx || a.Context() != ctx {
		t.Error("the context of the call is not the context of the request")
	}
	if r := a.ToHTTPRequest(); r.Context() != ctx || r.Method != "POST" {
		t.Errorf("HTTP request = %v, want one with the context of the call", r)
	}
	if err := a.Close(); err != nil {
		t.Errorf("Close = %v", err)
	}
}

func TestGrpcHTTPRequestAdapterWithoutContext(t *testing.T) {
	a := NewGrpcHTTPRequestAdapter(context.Background(), "POST", "h", "/a", nil, nil)
	if a.RemoteAddr != "" {
		t.Errorf("remote addr = %q, want none without a peer", a.RemoteAddr)
	}
	a2 := &GrpcHTTPRequestAdapter{Request: a.Request}
	if a2.Context() == nil {
		t.Error("Context() gave nil: it must fall back to a background context")
	}
	if r := a2.ToHTTPRequest(); r.Context() == nil || a2.Ctx == nil {
		t.Error("ToHTTPRequest must give the request a background context when it has none")
	}
}

func TestGrpcHTTPResponseWriterAdapter(t *testing.T) {
	desc := wrapperspb.String("").ProtoReflect().Descriptor()
	w := NewGrpcHTTPResponseWriterAdapter(desc)
	if w.Status() != http.StatusOK || len(w.Responses) != 0 || w.Header() == nil {
		t.Errorf("new adapter = status %d, %d responses, header %v, want 200 and nothing else", w.Status(), len(w.Responses), w.Header())
	}
	w.Header().Add("X-A", "1")
	if n, err := w.Write([]byte(`"one"`)); n != 5 || err != nil {
		t.Errorf("Write = %d, %v, want 5 and no error", n, err)
	}
	if n, err := w.Write(nil); n != 0 || err != nil {
		t.Errorf("Write(nil) = %d, %v, want nothing", n, err)
	}
	if n, err := w.Write([]byte(`"two"`)); n != 5 || err != nil || len(w.Responses) != 2 {
		t.Errorf("second Write = %d, %v with %d responses, want two responses", n, err, len(w.Responses))
	}
	if n, err := w.Write([]byte(`not json`)); n != 0 || err == nil || !strings.Contains(err.Error(), "failed to unmarshal") {
		t.Errorf("Write(not json) = %d, %v, want a failure to unmarshal", n, err)
	}
	if len(w.Responses) != 2 {
		t.Errorf("responses = %d, want a response that fails to parse not to be kept", len(w.Responses))
	}
	// The first status is kept: a write is a status of 200 if there was none, and later ones are ignored.
	w.WriteHeader(http.StatusTeapot)
	if w.Status() != http.StatusOK {
		t.Errorf("status = %d, want 200: the write made the status", w.Status())
	}
	w2 := NewGrpcHTTPResponseWriterAdapter(desc)
	w2.WriteHeader(http.StatusConflict)
	w2.WriteHeader(http.StatusTeapot)
	if w2.Status() != http.StatusConflict {
		t.Errorf("status = %d, want the first one, 409", w2.Status())
	}
	md := w.ToMetadata()
	if !reflect.DeepEqual(md["x-a"], []string{"1"}) {
		t.Errorf("metadata = %v, want x-a", md)
	}
}

func TestHTTPToMDHeaders(t *testing.T) {
	got := HTTPToMDHeaders(http.Header{"Content-Type": {"a"}, ":Status": {"200"}, "X-Multi": {"1", "2"}})
	want := map[string][]string{"content-type": {"a"}, "status": {"200"}, "x-multi": {"1", "2"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("HTTPToMDHeaders = %v, want %v", got, want)
	}
	if got := HTTPToMDHeaders(nil); got == nil || len(got) != 0 {
		t.Errorf("HTTPToMDHeaders(nil) = %v, want an empty map", got)
	}
}
