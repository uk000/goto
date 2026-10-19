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
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"goto/pkg/constants"
	gotogrpc "goto/pkg/rpc/grpc"
	"goto/pkg/util"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/dynamicpb"
)

func unaryMethodForTest() *gotogrpc.GRPCServiceMethod {
	return &gotogrpc.GRPCServiceMethod{Name: "m", IsUnary: true}
}

// Invoke reported a service that is not configured by reading the name of the service that is not there.
func TestRegressionInvokeServiceNotConfigured(t *testing.T) {
	stubGlobals(t)
	c, _ := NewGRPCClient("c", 1, nil, "127.0.0.1:1", "", "", &GRPCOptions{})
	t.Cleanup(c.Close)
	r := invoke(t, c, &GRPCCall{Method: "m", Payloads: samplePayloads("x")}, nil)
	if r == nil || len(r.Errors) != 1 {
		t.Fatalf("a client without a service must give a result with the error, got %+v", r)
	}
	if got := r.GetErrors(); !strings.Contains(got, "not configured") {
		t.Errorf("errors = %q", got)
	}
}

// Invoke added the error to a result that did not exist yet, when the method was not found.
func TestRegressionInvokeMethodNotFound(t *testing.T) {
	stubGlobals(t)
	c, _ := NewGRPCClient("c", 1, &gotogrpc.GRPCService{Name: "svc", Methods: map[string]*gotogrpc.GRPCServiceMethod{}}, "127.0.0.1:1", "", "", &GRPCOptions{})
	t.Cleanup(c.Close)
	r := invoke(t, c, &GRPCCall{Method: "nope", Payloads: samplePayloads("x")}, nil)
	if r == nil || len(r.Errors) != 1 || !strings.Contains(r.GetErrors(), "nope") {
		t.Fatalf("a method that is not found must give a result with the error, got %+v", r)
	}
}

// Invoke added the error to a result that did not exist yet, when the client failed to connect.
func TestRegressionInvokeConnectError(t *testing.T) {
	stubGlobals(t)
	svc := &gotogrpc.GRPCService{Name: "svc", Methods: map[string]*gotogrpc.GRPCServiceMethod{"m": unaryMethodForTest()}}
	c, _ := NewGRPCClient("c", 1, svc, "127.0.0.1:1", "", "", &GRPCOptions{})
	t.Cleanup(c.Close)
	c.Options.DialOptions = nil // a client without transport security can't be created
	r := invoke(t, c, &GRPCCall{Method: "m", Payloads: samplePayloads("x")}, nil)
	if r == nil || len(r.Errors) != 1 {
		t.Fatalf("a client that fails to connect must give a result with the error, got %+v", r)
	}
}

// Connect logged a failure with the name of the service, which a client may not have.
func TestRegressionConnectErrorWithoutService(t *testing.T) {
	stubGlobals(t)
	c, _ := NewGRPCClient("c", 1, nil, "127.0.0.1:1", "", "", &GRPCOptions{})
	t.Cleanup(c.Close)
	c.Options.DialOptions = nil // a client without transport security can't be created
	var err error
	within(t, 5*time.Second, "Connect", func() { err = c.Connect() })
	if err == nil {
		t.Errorf("connecting with no transport security must fail")
	}
}

// A call that has no headers, such as the one that the call/{service}/{method}/{endpoint} API makes, failed to read them.
func TestRegressionInvokeWithoutCallHeaders(t *testing.T) {
	e := newEnv(t)
	c := newClient(t, e, nil)
	call := &GRPCCall{Service: "SampleService", Method: "oneOne", Endpoint: e.addr, Payloads: samplePayloads("nh")}
	r := invoke(t, c, call, nil)
	if len(r.Errors) != 0 || len(r.Responses) != 1 {
		t.Fatalf("a call without headers must work: errors %v responses %d", r.Errors, len(r.Responses))
	}
}

// A stream call without payloads failed on the payloads, while a unary one reported that there is no payload.
func TestRegressionInvokeWithoutPayloads(t *testing.T) {
	e := newEnv(t)
	c := newClient(t, e, nil)
	for _, method := range []string{"oneOne", "oneMany", "manyOne", "manyMany"} {
		r := invoke(t, c, &GRPCCall{Service: "SampleService", Method: method, Endpoint: e.addr, Headers: addHeaders()}, nil)
		if r == nil || len(r.Errors) == 0 || len(r.Responses) != 0 {
			t.Errorf("%s without payloads: result %+v, want an error and no response", method, r)
		}
	}
}

// Headers of a payload were added to metadata that is nil when the call has no headers of its own.
func TestRegressionPayloadHeadersWithoutBaseMetadata(t *testing.T) {
	var got []string
	uCall := func(md metadata.MD, payload []byte, wg *sync.WaitGroup) { got = append(got, join(md.Get("x-own"))) }
	p := &GRPCPayloads{Linear: []*GRPCPayload{{Payload: "{}", Headers: addHeaders("x-own", "v")}}}
	within(t, 5*time.Second, "Process", func() { p.Process(&GRPCCall{}, nil, uCall, nil) })
	if len(got) != 1 || got[0] != "v" {
		t.Errorf("handler got headers %v, want the payload header", got)
	}

	// and end to end
	e := newEnv(t)
	c := newClient(t, e, nil)
	call := &GRPCCall{Service: "SampleService", Method: "oneOne", Endpoint: e.addr,
		Payloads: &GRPCPayloads{Linear: []*GRPCPayload{{Payload: `{"content":"ph"}`, Headers: addHeaders("x-own", "v")}}}}
	r := invoke(t, c, call, nil)
	if len(r.Errors) != 0 || len(r.Responses) != 1 {
		t.Fatalf("errors %v responses %d", r.Errors, len(r.Responses))
	}
	if md := e.sample.recorded("oneOne")[0].md; len(md.Get("x-own")) != 1 || md.Get("x-own")[0] != "v" {
		t.Errorf("server did not get the payload header: %v", md)
	}
}

// Concurrent payloads waited for as many calls as the count of a payload, which is more than what a payload that
// has an empty payload within calls, so that the wait never ended.
func TestRegressionConcurrentPayloadsAlwaysFinish(t *testing.T) {
	var lock sync.Mutex
	calls := 0
	uCall := func(md metadata.MD, payload []byte, wg *sync.WaitGroup) {
		if wg != nil {
			defer wg.Done()
		}
		lock.Lock()
		calls++
		lock.Unlock()
	}
	p := &GRPCPayloads{Concurrent: []*GRPCPayload{
		{Linear: []*GRPCPayload{{Payload: "a"}, {}}},
		{Linear: []*GRPCPayload{{Payload: "b"}, {Linear: []*GRPCPayload{{Payload: "c"}}}}},
		{Payload: "d"},
	}}
	within(t, 5*time.Second, "concurrent Process", func() { p.Process(&GRPCCall{}, metadata.MD{}, uCall, nil) })
	lock.Lock()
	defer lock.Unlock()
	if calls != 4 {
		t.Errorf("handler calls = %d, want 4", calls)
	}
}

// A payload that is a stream called the stream handler, which a unary call doesn't have.
func TestRegressionStreamPayloadOnUnaryMethod(t *testing.T) {
	var got []string
	uCall := func(md metadata.MD, payload []byte, wg *sync.WaitGroup) { got = append(got, string(payload)) }
	p := &GRPCPayloads{Linear: []*GRPCPayload{{Stream: []string{"one", "two"}}}}
	within(t, 5*time.Second, "Process", func() { p.Process(&GRPCCall{}, metadata.MD{}, uCall, nil) })
	if len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Errorf("a stream payload on a unary call is sent as one call per message, got %v", got)
	}

	e := newEnv(t)
	c := newClient(t, e, nil)
	call := &GRPCCall{Service: "SampleService", Method: "oneOne", Endpoint: e.addr, Headers: addHeaders(),
		Payloads: &GRPCPayloads{Linear: []*GRPCPayload{{Stream: []string{`{"content":"s1"}`, `{"content":"s2"}`}}}}}
	r := invoke(t, c, call, nil)
	if len(r.Errors) != 0 || len(r.Responses) != 2 {
		t.Errorf("errors %v responses %d, want 2 responses", r.Errors, len(r.Responses))
	}
}

// Unary payloads sent concurrently wrote the context of the invocation, which they share.
func TestRegressionConcurrentUnaryPayloadsShareNoState(t *testing.T) {
	e := newEnv(t)
	c := newClient(t, e, nil)
	payloads := &GRPCPayloads{}
	for i := 0; i < 8; i++ {
		payloads.Concurrent = append(payloads.Concurrent, &GRPCPayload{Payload: `{"content":"c"}`, Headers: addHeaders("x-n", string(rune('a'+i)))})
	}
	call := &GRPCCall{Service: "SampleService", Method: "oneOne", Endpoint: e.addr, Headers: addHeaders("x-base", "b"), Payloads: payloads}
	r := invoke(t, c, call, nil)
	if len(r.Errors) != 0 || len(r.Responses) != 8 {
		t.Fatalf("errors %v responses %d, want 8 responses", r.Errors, len(r.Responses))
	}
	seen := map[string]bool{}
	for _, rec := range e.sample.recorded("oneOne") {
		seen[join(rec.md.Get("x-n"))] = true
	}
	if len(seen) != 8 {
		t.Errorf("every payload must go with its own headers: %v", seen)
	}
}

// The result of the sender of a bidi stream was read without waiting for the sender.
func TestRegressionBidiResultsAreSynchronized(t *testing.T) {
	e := newEnv(t)
	c := newClient(t, e, nil)
	for i := 0; i < 5; i++ {
		payloads := &GRPCPayloads{Linear: []*GRPCPayload{{Stream: []string{`{"id":1,"content":"a"}`, `{"id":2,"content":"b"}`}}}}
		r := invoke(t, c, &GRPCCall{Service: "SampleService", Method: "manyMany", Endpoint: e.addr, Headers: addHeaders(), Payloads: payloads}, nil)
		if len(r.Errors) != 0 || len(r.Responses) != 1 || len(r.Responses[0].ResponsePayload) != 2 {
			t.Fatalf("round %d: errors %v responses %+v", i, r.Errors, r.Responses)
		}
	}
}

// The trailers of a server stream were read when the headers arrived, before the stream was done.
func TestRegressionServerStreamTrailers(t *testing.T) {
	e := newEnv(t)
	c := newClient(t, e, nil)
	call := &GRPCCall{Service: "SampleService", Method: "oneMany", Endpoint: e.addr, Headers: addHeaders(),
		Payloads: &GRPCPayloads{Linear: []*GRPCPayload{{Payload: `{"id":3,"content":"slow"}`}}}}
	r := invoke(t, c, call, nil)
	if len(r.Errors) != 0 || len(r.Responses) != 1 || len(r.Responses[0].ResponsePayload) != 3 {
		t.Fatalf("errors %v responses %+v", r.Errors, r.Responses)
	}
	if got := r.Responses[0].ResponseTrailers["x-reply-trailer"]; len(got) != 1 || got[0] != "t-slow" {
		t.Errorf("trailers = %v, want the ones sent when the stream ended", r.Responses[0].ResponseTrailers)
	}

	// same with the raw API
	newMsg := dynamicpb.NewMessage(c.Service.Methods["oneMany"].InputType())
	fields := newMsg.Descriptor().Fields()
	newMsg.Set(fields.ByName("id"), protoInt32(2))
	newMsg.Set(fields.ByName("content"), protoString("slow"))
	var trailers metadata.MD
	within(t, 15*time.Second, "InvokeServerStreamRaw", func() {
		_, _, trailers, _ = c.InvokeServerStreamRaw(c.Service.Methods["oneMany"], nil, newMsg, nil)
	})
	if got := trailers.Get("x-reply-trailer"); len(got) != 1 || got[0] != "t-slow" {
		t.Errorf("raw trailers = %v", trailers)
	}
}

// UpdateTLSConfig did not take the verify flag.
func TestRegressionUpdateTLSConfigVerify(t *testing.T) {
	c, _ := NewGRPCClient("c", 1, nil, "u", "", "old", &GRPCOptions{IsTLS: true, VerifyTLS: false, TLSVersion: tls.VersionTLS13})
	if !c.tlsConfig.InsecureSkipVerify {
		t.Fatalf("precondition: not verifying")
	}
	c.UpdateTLSConfig("new.example", tls.VersionTLS12, true, nil)
	if c.tlsConfig.InsecureSkipVerify || !c.Options.VerifyTLS {
		t.Errorf("verify=true must turn on verification: skip=%v option=%v", c.tlsConfig.InsecureSkipVerify, c.Options.VerifyTLS)
	}
	c.UpdateTLSConfig("new.example", tls.VersionTLS12, false, nil)
	if !c.tlsConfig.InsecureSkipVerify || c.Options.VerifyTLS {
		t.Errorf("verify=false must turn off verification: skip=%v option=%v", c.tlsConfig.InsecureSkipVerify, c.Options.VerifyTLS)
	}
}

// A client created without options failed on them.
func TestRegressionNilOptions(t *testing.T) {
	stubGlobals(t)
	var c *GRPCClient
	var err error
	within(t, 5*time.Second, "NewGRPCClient", func() { c, err = NewGRPCClient("c", 1, nil, "127.0.0.1:1", "", "", nil) })
	if err != nil || c == nil {
		t.Fatalf("NewGRPCClient without options: %v %v", c, err)
	}
	if c.Options.IsTLS || c.Options.IdleTimeout != 10*time.Minute {
		t.Errorf("options = %+v, want the defaults", c.Options)
	}
}

// Reflecting services from an upstream left the connection that it made open.
func TestRegressionReflectionLoadClosesConnection(t *testing.T) {
	e := newEnv(t)
	if err := LoadRemoteReflectedServices(e.addr); err != nil {
		t.Fatalf("LoadRemoteReflectedServices: %v", err)
	}
	if e.lis.accepted.Load() == 0 {
		t.Fatalf("the upstream was not reached")
	}
	eventually(t, 3*time.Second, "connection of the reflection client closed", func() bool { return e.lis.open() == 0 })
}

// The targets were kept in a map that was used without a lock.
func TestRegressionTargetsManagerConcurrentUse(t *testing.T) {
	e := newEnv(t)
	resetManager(t)
	registerSampleService(t, e)
	var wg sync.WaitGroup
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				Manager.AddTarget("SampleService", e.addr, "", "", &GRPCOptions{})
				Manager.ConnectTarget("SampleService")
				Manager.SetTargetTLS("SampleService", i%2 == 0, false)
				Manager.RemoveTarget("SampleService")
			}
		}()
	}
	wg.Wait()
	Manager.ClearTargets()
}

// The request timeout of the client was never used for a call.
func TestRegressionRequestTimeoutAppliesToUnary(t *testing.T) {
	e := newEnv(t)
	c := newClient(t, e, &GRPCOptions{RequestTimeout: 300 * time.Millisecond})
	call := &GRPCCall{Service: "SampleService", Method: "oneOne", Endpoint: e.addr, Headers: addHeaders(), Payloads: samplePayloads("block")}
	start := time.Now()
	r := invoke(t, c, call, nil)
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("the call took %s, with a request timeout of 300ms", took)
	}
	if len(r.Errors) != 1 || status.Code(r.Errors[0]) != codes.DeadlineExceeded {
		t.Errorf("errors = %v, want DeadlineExceeded", r.Errors)
	}
}

// OpenStream gave neither a stream nor an error for a method that is not a stream.
func TestRegressionOpenStreamRejectsUnaryMethod(t *testing.T) {
	e := newEnv(t)
	c := newClient(t, e, nil)
	within(t, 10*time.Second, "OpenStream", func() {
		stream, err := c.OpenStream(1, c.Service.Methods["oneOne"], nil, nil)
		if err == nil || stream != nil {
			t.Errorf("OpenStream of a unary method: %v %v, want an error and no stream", stream, err)
		}
	})
}

// GetErrors of a result that does not exist failed.
func TestRegressionNilResultGetErrors(t *testing.T) {
	var r *GRPCResult
	within(t, 5*time.Second, "GetErrors", func() {
		if got := r.GetErrors(); got != "" {
			t.Errorf("GetErrors of nil = %q", got)
		}
	})
}

// The connection of a call that was made through the API was never closed.
func TestRegressionDoCallClosesClient(t *testing.T) {
	e := newEnv(t)
	grpcRouter, _ := apiRouters()
	body := callJSON(e, "SampleService", "oneOne", `{"linear":[{"payload":"{\"content\":\"c\"}"}]}`, `,"headers":{}`)
	rr := callAPI(t, grpcRouter, "POST", "/grpc/client/call", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("call = %d %q", rr.Code, rr.Body.String())
	}
	if e.lis.accepted.Load() == 0 {
		t.Fatalf("the upstream was not reached")
	}
	eventually(t, 3*time.Second, "connection of the call closed", func() bool { return e.lis.open() == 0 })
}

// A call was taken for a stream call when "stream" was anywhere in the URI, such as a query or an endpoint.
func TestRegressionStreamRouteDetection(t *testing.T) {
	e := newEnv(t)
	grpcRouter, _ := apiRouters()
	rr := callAPI(t, grpcRouter, "POST", "/grpc/client/call/SampleService/oneMany/"+e.addr+"?via=upstream", `{"id":2,"content":"r"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("call = %d %q", rr.Code, rr.Body.String())
	}
	var out []apiResult
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("a call that is not a stream call has the results only, but the body is: %v\n%s", err, rr.Body.String())
	}
	if len(out) != 1 {
		t.Errorf("results = %+v", out)
	}
}

// The calls that push their messages checked for the via-goto headers without the lock that guards what they set.
func TestRegressionConcurrentPushedStreamsViaGoto(t *testing.T) {
	e := newEnv(t)
	grpcRouter, _ := apiRouters()
	payloads := `{"concurrent":[`
	for i := 0; i < 6; i++ {
		if i > 0 {
			payloads += ","
		}
		payloads += `{"payload":"{\"id\":3,\"content\":\"via\"}"}`
	}
	payloads += `]}`
	body := callJSON(e, "SampleService", "oneMany", payloads, `,"push":true,"headers":{}`)
	rr := callAPI(t, grpcRouter, "POST", "/grpc/client/call", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("call = %d %q", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Values(constants.HeaderViaGoto); len(got) != 1 || got[0] != "remote-goto(gRPC)" {
		t.Errorf("%s = %v, want one value for the upstream goto", constants.HeaderViaGoto, got)
	}
	if n := strings.Count(rr.Body.String(), `"via-3"`); n != 6 {
		t.Errorf("pushed messages = %d, want 6 (the last of every stream)", n)
	}
	if n := len(e.sample.recorded("oneMany")); n != 6 {
		t.Errorf("server calls = %d, want 6", n)
	}
}

// An error that a server stream ended with was set by the goroutine that received the messages, racing with the caller
// that returned the error, and so it could be lost.
func TestRegressionServerStreamFailureIsReported(t *testing.T) {
	e := newEnv(t)
	c := newClient(t, e, nil)
	for i := 0; i < 5; i++ {
		call := &GRPCCall{Service: "SampleService", Method: "oneMany", Endpoint: e.addr, Headers: addHeaders(),
			Payloads: &GRPCPayloads{Linear: []*GRPCPayload{{Payload: `{"id":3,"content":"fail"}`}}}}
		r := invoke(t, c, call, nil)
		if len(r.Errors) != 1 || status.Code(r.Errors[0]) != codes.Aborted {
			t.Fatalf("round %d: errors = %v, want the Aborted that the server ended the stream with", i, r.Errors)
		}
	}
}

// The log of a bidi stream that failed to receive used an error that was not set yet, which is a nil pointer.
func TestRegressionBidiStreamFailureIsReported(t *testing.T) {
	e := newEnv(t)
	c := newClient(t, e, nil)
	call := &GRPCCall{Service: "SampleService", Method: "manyMany", Endpoint: e.addr, Headers: addHeaders(),
		Payloads: &GRPCPayloads{Linear: []*GRPCPayload{{Stream: []string{`{"id":1,"content":"fail"}`}}}}}
	r := invoke(t, c, call, nil)
	if len(r.Errors) == 0 {
		t.Errorf("a bidi stream that the server ended with an error has no error in the result: %+v", r.Responses)
	}
}

// The result of a call is read for its errors while the calls of the payloads that run concurrently add to them.
func TestRegressionResultErrorsAreReadWithTheirLock(t *testing.T) {
	r := newGRPCResult()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				r.AddError(errors.New("e"))
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = r.GetErrors()
			}
		}()
	}
	wg.Wait()
	if n := len(r.Errors); n != 400 {
		t.Errorf("errors = %d, want 400", n)
	}
}

// A writer that can't flush is what the handler gets when something that wraps the writer hides the flusher. A call that
// pushes its messages must still deliver them.
type plainWriter struct {
	header http.Header
	code   int
	body   strings.Builder
}

func (w *plainWriter) Header() http.Header         { return w.header }
func (w *plainWriter) Write(b []byte) (int, error) { return w.body.Write(b) }
func (w *plainWriter) WriteHeader(code int)        { w.code = code }

func TestRegressionPushToAWriterThatCantFlush(t *testing.T) {
	e := newEnv(t)
	stubGlobals(t)
	body := callJSON(e, "SampleService", "oneMany", `{"linear":[{"payload":"{\"id\":2,\"content\":\"push\"}"}]}`, `,"push":true,"headers":{}`)
	req := httptest.NewRequest("POST", "/grpc/client/call", strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, 4000))
	_, req, _ = util.WithRequestStore(req, nil)
	w := &plainWriter{header: http.Header{}}
	within(t, 30*time.Second, "push call", func() { call(w, req) })
	for _, want := range []string{`"push-1"`, `"push-2"`} {
		if n := strings.Count(w.body.String(), want); n != 1 {
			t.Errorf("pushed messages with %s = %d, want 1\n%s", want, n, w.body.String())
		}
	}
}

// Connect made a new stub on every call, and left the connection that is alive as it is, so the calls that are on the
// way kept using a stub that was replaced.
func TestRegressionConnectKeepsTheStubOfALiveConnection(t *testing.T) {
	e := newEnv(t)
	c := newClient(t, e, nil)
	stub, conn := c.getStub(), c.GRPC()
	if stub == nil || conn == nil {
		t.Fatalf("precondition: stub %v conn %v", stub, conn)
	}
	for i := 0; i < 3; i++ {
		if err := c.Connect(); err != nil {
			t.Fatalf("Connect: %v", err)
		}
	}
	if c.getStub() != stub || c.GRPC() != conn {
		t.Errorf("Connect replaced the connection or the stub of a connection that is alive")
	}
}

// The monitor of a client that has no connection looked at the connection after a second, which is a nil pointer.
func TestRegressionMonitorOfAClientWithoutConnection(t *testing.T) {
	c := &GRPCClient{}
	c.monitorAndAbort()
	time.Sleep(1500 * time.Millisecond) // the monitor looks at the connection after a second
}

// The connection, the stub, the service and the request timeout of a client are used by the calls on it while it connects
// again and loads the service again.
func TestRegressionClientStateIsGuarded(t *testing.T) {
	e := newEnv(t)
	c, err := NewGRPCClient("guarded", e.port, nil, e.addr, "", "", &GRPCOptions{})
	if err != nil {
		t.Fatalf("NewGRPCClient: %v", err)
	}
	t.Cleanup(c.Close)
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := c.LoadServiceMethodFromReflection("SampleService", ""); err != nil {
		t.Fatalf("LoadServiceMethodFromReflection: %v", err)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	run := func(f func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					f()
				}
			}
		}()
	}
	run(func() { c.Close(); c.Connect() }) // a closed connection is replaced, along with the stub
	run(func() { c.ConnectWithHeadersOrMD(nil, nil) })
	run(func() { _ = c.getStub() })
	run(func() { _ = c.GRPC() })
	run(func() { c.Close() })
	run(func() { _ = c.serviceName() })
	run(func() { _ = c.requestTimeout() })
	run(func() { c.LoadServiceMethodFromReflection("SampleService", "") })
	run(func() { // a timeout that is not set yet is defaulted by the next call
		c.connLock.Lock()
		c.Options.RequestTimeout = 0
		c.connLock.Unlock()
	})
	time.Sleep(300 * time.Millisecond)
	close(stop)
	within(t, 60*time.Second, "concurrent use of the client", wg.Wait)

	// the client is still good for use
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := c.LoadServiceMethodFromReflection("SampleService", ""); err != nil || c.serviceName() != "SampleService" {
		t.Errorf("the client can't load the service after the concurrent use: %v %q", err, c.serviceName())
	}
}
