/**
 * Copyright 2025 uk
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
)

type MiddlewareFunc func(http.ResponseWriter, *http.Request)

type Middleware struct {
	Name              string
	SetRoutes         func(r *mux.Router, root *mux.Router)
	MiddlewareHandler mux.MiddlewareFunc
}

func NewMiddleware(name string, setRoutes func(r *mux.Router, root *mux.Router), middlewareHandler mux.MiddlewareFunc) *Middleware {
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

func AddRoutes(r *mux.Router, root *mux.Router, handlers ...*Middleware) {
	for _, h := range handlers {
		if h.SetRoutes != nil {
			h.SetRoutes(r, root)
		}
	}
}

func BaseHandlerFunc(getHandler func() http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if getHandler != nil {
			getHandler().ServeHTTP(w, r)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	})
}

func SetRoutesOnly(r *mux.Router) {
	for _, m := range RoutesOnly {
		if m.SetRoutes != nil {
			m.SetRoutes(r, r)
		}
	}
}

func LinkCore(r *mux.Router) {
	for _, m := range Core {
		if m.SetRoutes != nil {
			m.SetRoutes(r, r)
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

func LinkInterceptedCore(r *mux.Router) {
	for _, m := range InterceptedCore {
		if m.SetRoutes != nil {
			m.SetRoutes(r, r)
		}
	}
	UseInterceptedCore(r)
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
			m.SetRoutes(r, r)
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
			m.SetRoutes(r, r)
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
	ctx, r, _ = util.WithRequestStore(r)
	w, irw := intercept.WithIntercept(r, wa)
	MiddlewareGRPCChainHead.ServeHTTP(w, r)
	irw.Proceed()
	return ra, wa
}

func RootPath(path string) *mux.Router {
	if RootRouters[path] == nil {
		r := mux.NewRouter().SkipClean(true).PathPrefix(path).Subrouter()
		UseCore(r)
		UseInterceptedCore(r)
		RootRouters[path] = r
	}
	return RootRouters[path]
}

func AddRouterPath(router *mux.Router, path string) *mux.Router {
	if RootRouters[path] == nil {
		RootRouters[path] = router
	}
	return router.PathPrefix(path).Subrouter()
}
