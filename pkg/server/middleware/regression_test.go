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
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/protobuf/types/known/wrapperspb"
)

// Each test in this file failed on the code before it was fixed.

// guard turns a panic into a failure of the test, so that the other tests still run.
func guard(t *testing.T, what string, f func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s panicked: %v", what, r)
		}
	}()
	f()
}

// The chain of the unintercepted middlewares ends in the chain of the intercepted ones, which is linked after it. A
// request that comes before there is a chain to hand it to must be answered, and not take the handler down.
func TestBaseHandlerFuncWithNoChainToHandOverTo(t *testing.T) {
	var head http.Handler
	h := BaseHandlerFunc(func() http.Handler { return head })
	rr := httptest.NewRecorder()
	guard(t, "BaseHandlerFunc whose getter gives no handler", func() {
		h.ServeHTTP(rr, newRequest("GET", "/"))
	})
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}

// A GRPC call that comes while the chain is not linked, which is before the HTTP server is configured, is answered with
// what the chain would answer with when it has nothing to do: no response.
func TestInvokeMiddlewareChainForGRPCWithoutChain(t *testing.T) {
	saveChains(t)
	desc := wrapperspb.String("").ProtoReflect().Descriptor()
	guard(t, "InvokeMiddlewareChainForGRPC with no chain", func() {
		ra, wa := InvokeMiddlewareChainForGRPC(context.Background(), 5001, "POST", "h", "/a/b", nil, nil, desc)
		if ra == nil || wa == nil {
			t.Fatalf("adapters = %v, %v, want both", ra, wa)
		}
		if wa.StatusCode != http.StatusOK || len(wa.Responses) != 0 {
			t.Errorf("status = %d, responses = %d, want 200 and none", wa.StatusCode, len(wa.Responses))
		}
	})
}

// A uri that can't be parsed must not leave the request without a URL, which everything that handles the request reads.
func TestGrpcRequestAdapterWithUnparsableURI(t *testing.T) {
	saveChains(t)
	const uri = "/pkg.Svc/%zz"
	a := NewGrpcHTTPRequestAdapter(context.Background(), "POST", "h", uri, nil, nil)
	if a.URL == nil || a.URL.Path != uri {
		t.Errorf("url = %v, want one with the path %q", a.URL, uri)
	}
	MiddlewareGRPCChainHead = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	guard(t, "InvokeMiddlewareChainForGRPC with a uri that can't be parsed", func() {
		InvokeMiddlewareChainForGRPC(context.Background(), 5002, "POST", "h", uri, nil, nil, wrapperspb.String("").ProtoReflect().Descriptor())
	})
}

// A response is parsed with the type of the responses of the call, which is not known if the call is not.
func TestGrpcResponseWriterAdapterWithoutDescriptor(t *testing.T) {
	w := NewGrpcHTTPResponseWriterAdapter(nil)
	guard(t, "Write without a descriptor", func() {
		if n, err := w.Write([]byte(`"x"`)); err == nil || n != 0 {
			t.Errorf("Write = %d, %v, want an error", n, err)
		}
	})
	if n, err := w.Write(nil); n != 0 || err != nil {
		t.Errorf("Write(nil) = %d, %v, want nothing to be written without an error", n, err)
	}
}
