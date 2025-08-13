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
	MiddlewareChain = []MiddlewareFunc{}
	Middlewares     = []*Middleware{}
)

type MiddlewareFunc func(http.ResponseWriter, *http.Request)

type Middleware struct {
	Name              string
	SetRoutes         func(r *mux.Router, parent *mux.Router, root *mux.Router)
	MiddlewareHandler mux.MiddlewareFunc
}

func NewMiddleware(name string, setRoutes func(r *mux.Router, parent *mux.Router, root *mux.Router), middlewareHandler mux.MiddlewareFunc) *Middleware {
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

func AddRoutes(r *mux.Router, parent *mux.Router, root *mux.Router, handlers ...*Middleware) {
	for _, h := range handlers {
		if h.SetRoutes != nil {
			h.SetRoutes(r, parent, root)
		}
	}
}

func BaseHandlerFunc() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func LinkMiddlewareChain(r *mux.Router) {
	handler := BaseHandlerFunc()
	for _, m := range Middlewares {
		if m.SetRoutes != nil {
			m.SetRoutes(r, nil, r)
		}
		if m.MiddlewareHandler != nil {
			r.Use(m.MiddlewareHandler)
		}
	}
	for i := len(Middlewares) - 1; i >= 0; i-- {
		m := Middlewares[i]
		if m.MiddlewareHandler != nil {
			handler = m.MiddlewareHandler(handler)
			MiddlewareChain = append([]MiddlewareFunc{handler.ServeHTTP}, MiddlewareChain...)
		}
	}
}

func InvokeMiddlewareChainForGRPC(ctx context.Context, port int, method, host, uri string, headers map[string][]string, body []byte, desc protoreflect.MessageDescriptor) (*GrpcHTTPRequestAdapter, *GrpcHTTPResponseWriterAdapter) {
	ctx = util.WithPort(ctx, port)
	ra := NewGrpcHTTPRequestAdapter(ctx, method, host, uri, headers, body)
	wa := NewGrpcHTTPResponseWriterAdapter(desc)
	r := ra.ToHTTPRequest()
	r = r.WithContext(ctx)
	ctx, _ = util.WithRequestStore(r)
	r = r.WithContext(ctx)
	w, irw := intercept.WithIntercept(r, wa)
	MiddlewareChain[0](w, r)
	irw.Proceed()
	return ra, wa
}
