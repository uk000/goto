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
	"goto/pkg/server/intercept"
	"goto/pkg/util"
	"net/http"

	"github.com/gorilla/mux"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var (
	Core                    = []*Middleware{}
	InterceptedCore         = []*Middleware{}
	Unintercepted           = []*Middleware{}
	Intercepted             = []*Middleware{}
	RoutesOnly              = []*Middleware{}
	MiddlewareGRPCChainHead http.Handler
	MiddlewareChainHead     http.Handler
	middlewareRouter        *mux.Router
	RootRouters             = map[string]*mux.Router{}
	// RootGuards has, by root path, the check that a request must pass to be served by the router of the root path.
	RootGuards = map[string]func(*http.Request) bool{}
)

type MiddlewareFunc func(http.ResponseWriter, *http.Request)

type Middleware struct {
	Name              string
	SetRoutes         func(r *mux.Router)
	MiddlewareHandler mux.MiddlewareFunc
}

func NewMiddleware(name string, setRoutes func(r *mux.Router), middlewareHandler mux.MiddlewareFunc) *Middleware {
	m := &Middleware{
		Name:              name,
		SetRoutes:         setRoutes,
		MiddlewareHandler: middlewareHandler,
	}
	return m
}

func AddMiddlewares(next http.Handler, middlewares ...*Middleware) http.Handler {
	handler := next
	for i := len(middlewares) - 1; i >= 0; i-- {
		if middlewares[i].MiddlewareHandler != nil {
			handler = middlewares[i].MiddlewareHandler(handler)
		}
	}
	return handler
}

func AddRoutes(r *mux.Router, handlers ...*Middleware) {
	for _, h := range handlers {
		if h.SetRoutes != nil {
			h.SetRoutes(r)
		}
	}
}

// BaseHandlerFunc is the handler at the end of a chain, which hands the request to the handler that it is given when
// there is one, and responds with 200 when there is none. The handler is looked up as the request comes, since the
// chain that it stands for is linked after this one is made.
func BaseHandlerFunc(getHandler func() http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if getHandler != nil {
			if h := getHandler(); h != nil {
				h.ServeHTTP(w, r)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
	})
}

func SetRoutesOnly(r *mux.Router) {
	for _, m := range RoutesOnly {
		if m.SetRoutes != nil {
			m.SetRoutes(r)
		}
	}
}

func LinkCore(r *mux.Router) {
	for _, m := range Core {
		if m.SetRoutes != nil {
			m.SetRoutes(r)
		}
	}
	UseCore(r)
}

func UseCore(r *mux.Router) {
	for _, m := range Core {
		if m.MiddlewareHandler != nil {
			r.Use(m.MiddlewareHandler)
		}
	}
}

func UseInterceptedCore(r *mux.Router) {
	for _, m := range InterceptedCore {
		if m.MiddlewareHandler != nil {
			r.Use(m.MiddlewareHandler)
		}
	}
}

func LinkUnintercepted(r *mux.Router) {
	for _, m := range Unintercepted {
		if m.SetRoutes != nil {
			m.SetRoutes(r)
		}
		if m.MiddlewareHandler != nil {
			r.Use(m.MiddlewareHandler)
		}
	}
	middlewareRouter = r
	linkToGRPCChain := BaseHandlerFunc(func() http.Handler { return MiddlewareChainHead })
	for i := len(Unintercepted) - 1; i >= 0; i-- {
		m := Unintercepted[i]
		if m.MiddlewareHandler != nil {
			linkToGRPCChain = m.MiddlewareHandler(linkToGRPCChain)
			MiddlewareGRPCChainHead = linkToGRPCChain
		}
	}
	for i := len(InterceptedCore) - 1; i >= 0; i-- {
		m := InterceptedCore[i]
		if m.MiddlewareHandler != nil {
			linkToGRPCChain = m.MiddlewareHandler(linkToGRPCChain)
			MiddlewareGRPCChainHead = linkToGRPCChain
		}
	}
	for i := len(Core) - 1; i >= 0; i-- {
		m := Core[i]
		if m.MiddlewareHandler != nil {
			linkToGRPCChain = m.MiddlewareHandler(linkToGRPCChain)
			MiddlewareGRPCChainHead = linkToGRPCChain
		}
	}
}

func LinkIntercepted(r *mux.Router) {
	for _, m := range Intercepted {
		if m.SetRoutes != nil {
			m.SetRoutes(r)
		}
		if m.MiddlewareHandler != nil {
			r.Use(m.MiddlewareHandler)
		}
	}
	handler := BaseHandlerFunc(nil)
	for i := len(Intercepted) - 1; i >= 0; i-- {
		m := Intercepted[i]
		if m.MiddlewareHandler != nil {
			MiddlewareChainHead = m.MiddlewareHandler(handler)
		}
	}
}

func InvokeMiddlewareChainForGRPC(ctx context.Context, port int, method, host, uri string, headers map[string][]string, body []byte, desc protoreflect.MessageDescriptor) (*GrpcHTTPRequestAdapter, *GrpcHTTPResponseWriterAdapter) {
	ctx = util.WithPort(ctx, port)
	ra := NewGrpcHTTPRequestAdapter(ctx, method, host, uri, headers, body)
	wa := NewGrpcHTTPResponseWriterAdapter(desc)
	r := ra.ToHTTPRequest()
	r = r.WithContext(ctx)
	ctx, r, _ = util.WithRequestStore(r, wa)
	w, irw := intercept.WithIntercept(r, wa)
	// A chain that is not linked, which is the case until the HTTP server is configured, has no middlewares to run.
	head := MiddlewareGRPCChainHead
	if head == nil {
		head = BaseHandlerFunc(nil)
	}
	head.ServeHTTP(w, r)
	irw.Proceed()
	return ra, wa
}

func RootPath(path string) *mux.Router {
	if RootRouters[path] == nil {
		r := mux.NewRouter().SkipClean(true).PathPrefix(path).Subrouter()
		UseCore(r)
		r.Use(intercept.IntereceptMiddleware(nil, postIntercept()))
		UseInterceptedCore(r)
		RootRouters[path] = r
	}
	return RootRouters[path]
}

// RootPathIf is RootPath for a root path that is served only for the requests that the guard allows. The other requests
// are handled as if there was no router for the root path, i.e. like those of any other URI, so a guard that fails does
// not turn what would have been responded to by the catch all into a not found. The guard belongs to the root path, so
// it applies to all the routes that are added under it, and replaces any that was set earlier.
func RootPathIf(path string, allow func(*http.Request) bool) *mux.Router {
	r := RootPath(path)
	RootGuards[path] = allow
	return r
}

// RootRouterFor returns the router of the root path, if there is one that serves the request: it has none when no router
// is registered for the root path, or when its guard does not allow the request.
func RootRouterFor(rootURI string, r *http.Request) *mux.Router {
	router := RootRouters[rootURI]
	if router == nil {
		return nil
	}
	if allow := RootGuards[rootURI]; allow != nil && !allow(r) {
		return nil
	}
	return router
}

func postIntercept() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		util.SendGotoTrailers(w, r)
	})
}
