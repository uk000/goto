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

package grpcserver

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gotogrpc "goto/pkg/rpc/grpc"
	"goto/pkg/rpc/grpc/extauthz"
	"goto/pkg/rpc/grpc/pb"
	"goto/pkg/server/listeners"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

// These tests are for the problems that were found in the server. Each of them fails with the code as it was.

// mustNotPanic runs f, and reports a panic as a failure of the test, as a panic of a handler brings the server down.
func mustNotPanic(t *testing.T, what string, f func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s panicked: %v", what, r)
		}
	}()
	f()
}

// The statuses that are forced for a header were never applied: only the port was given to match them with, and not
// the headers of the request.
func TestRegressionForcedStatusMatchesRequestHeaders(t *testing.T) {
	ts := startSampleServer(t)
	force(t, ts.port, "", "", "x-fail", "yes", []int{503}, -1)
	if _, _, err := ts.oneOne(t, "x", "x-fail", "yes"); !isForced(err, 503) {
		t.Errorf("a call with the header = %v, want the forced status", err)
	}
	if _, err := ts.manyOne(t, []string{"a"}, "x-fail", "yes"); !isForced(err, 503) {
		t.Errorf("a stream with the header = %v, want the forced status", err)
	}
	if resp, _, err := ts.oneOne(t, "x", "x-fail", "no"); err != nil || resp.Content != "re:x" {
		t.Errorf("a call with another value of the header must be served: %v %v", resp, err)
	}
	if resp, _, err := ts.oneOne(t, "x"); err != nil || resp.Content != "re:x" {
		t.Errorf("a call without the header must be served: %v %v", resp, err)
	}
}

// Echo cut the text of the request to 50 characters to log it, which is more than a short text has.
func TestRegressionEchoLogsShortRequestBody(t *testing.T) {
	stubGlobals(t)
	setFlags(t, func(f *logFlags) { f.log, f.body, f.miniBody = true, false, true })
	for _, text := range []string{"", "short", strings.Repeat("a", 49), strings.Repeat("a", 50), strings.Repeat("a", 51)} {
		mustNotPanic(t, fmt.Sprintf("Echo of a text of %d characters", len(text)), func() {
			out, err := (&GotoGRPCService{}).Echo(context.Background(), &pb.Input{Text: text})
			if err != nil || out.Text != text {
				t.Errorf("Echo(%d characters) = %v %v", len(text), out, err)
			}
		})
	}
}

// The transformers read fields that a message of a service may not have, which took them down.
func TestRegressionTransformersTolerateMissingFields(t *testing.T) {
	empty := dynamicMessage(t, "regression_empty", map[string]any{})
	mustNotPanic(t, "TransformInput of a message without payload", func() {
		if got := TransformInput(empty).(*pb.Input); got.Text != "" {
			t.Errorf("TransformInput = %v, want no text", got)
		}
	})
	some := dynamicMessage(t, "regression_some", map[string]any{"chunkCount": int32(4)})
	mustNotPanic(t, "TransformStreamConfig of a message with some of the fields", func() {
		got := TransformStreamConfig(some).(*pb.StreamConfig)
		if got.ChunkCount != 4 || got.ChunkSize != 0 || got.Interval != "" || got.Text != "" {
			t.Errorf("TransformStreamConfig = %+v, want only the chunk count", got)
		}
	})
}

// syncBuffer is a writer that the log can write to while the test reads what was written.
type syncBuffer struct {
	lock sync.Mutex
	buf  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.lock.Lock()
	defer b.lock.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.lock.Lock()
	defer b.lock.Unlock()
	return b.buf.String()
}

// captureLog gives what the log gets for the test, and puts the writer of the log back when the test is done.
func captureLog(t *testing.T) *syncBuffer {
	t.Helper()
	b := &syncBuffer{}
	old := log.Writer()
	log.SetOutput(io.MultiWriter(b, old))
	t.Cleanup(func() { log.SetOutput(old) })
	return b
}

// StreamInOut reported the total payload length from a variable that was never set, so it was always 0.
func TestRegressionStreamInOutReportsTotalPayloadLength(t *testing.T) {
	g := startGotoServer(t)
	setFlags(t, func(f *logFlags) { f.log = true })
	logged := captureLog(t)
	outs, err := g.streamInOut(t, []*pb.StreamConfig{
		{ChunkCount: 1, Text: "a", Interval: "1ms"},
		{ChunkCount: 1, Text: "b", Interval: "1ms"},
	})
	if err != nil || len(outs) != 2 {
		t.Fatalf("StreamInOut = %v %v", texts(outs), err)
	}
	// the payloads that were sent are "a" and "a | b(FINAL)"
	want := "Served StreamInOut with total chunks [2] and total payload length [13]"
	if got := logged.String(); !strings.Contains(got, want) {
		t.Errorf("the log has no %q", want)
	}
}

// Stopping a service said that it was registered for serving.
func TestRegressionStopServiceSaysItStopped(t *testing.T) {
	lc := startLifecycle(t)
	resetActiveServices(t)
	l := lc.newListener(t, "regression-1")
	GRPCManager.ServeListener(l)
	port := newAPIPort(t)
	callAPI(t, port, "POST", "/grpc/server/serve/SampleService", "")
	r := callAPI(t, port, "POST", "/grpc/server/stop/SampleService", "")
	if want := fmt.Sprintf("Service [SampleService] stopped on port [%d]", port); r.body != want {
		t.Errorf("stop = %q, want %q", r.body, want)
	}
}

// The listing of all the services and the listing of the active ones were told apart by the text "active" anywhere in
// the URI of the request, so the query of a request could change what it got.
func TestRegressionServicesListingIsNotTheActiveListing(t *testing.T) {
	startGotoServices(t, &pb.Goto_ServiceDesc)
	resetActiveServices(t)
	port := newAPIPort(t)
	for _, target := range []string{"/grpc/server/services?x=active", "/grpc/server/services?inactive=1"} {
		if r := callAPI(t, port, "GET", target, ""); !strings.Contains(r.body, `"Goto"`) {
			t.Errorf("GET %s = %q, want all the services", target, r.body)
		}
	}
}

// The port of a request was read from the variables of its path, and none of the routes has a port in the path: so a
// port could not be given at all, to open or to filter the active services.
func TestRegressionAPIPortsAreReadFromTheQuery(t *testing.T) {
	startGotoServices(t, &pb.Goto_ServiceDesc)
	resetActiveServices(t)
	port := newAPIPort(t)
	r := callAPI(t, port, "POST", "/grpc/server/open?port=70000", "")
	if r.code != http.StatusBadRequest || r.body != "Invalid port [70000]" {
		t.Errorf("open of port 70000 = %d %q, want 400 with the port that was given", r.code, r.body)
	}
	lock.Lock()
	ActiveServices[1234] = map[string]*gotogrpc.GRPCService{"Goto": ServiceRegistry.GetService("Goto")}
	ActiveServices[5678] = map[string]*gotogrpc.GRPCService{"Other": ServiceRegistry.GetService("Goto")}
	lock.Unlock()
	r = callAPI(t, port, "GET", "/grpc/server/services/active?port=1234", "")
	if !strings.Contains(r.body, `"Goto"`) || strings.Contains(r.body, `"Other"`) || strings.Contains(r.body, "5678") {
		t.Errorf("active services of port 1234 = %q, want only the ones of that port", r.body)
	}
	r = callAPI(t, port, "GET", "/grpc/server/services/active", "")
	if !strings.Contains(r.body, "1234") || !strings.Contains(r.body, "5678") {
		t.Errorf("active services of all the ports = %q", r.body)
	}
}

// The active services were read without the lock that guards them, while the API that serves a service changes them.
func TestRegressionActiveServicesAreReadWithTheirLock(t *testing.T) {
	startGotoServices(t, &pb.Goto_ServiceDesc)
	resetActiveServices(t)
	port := newAPIPort(t)
	svc := ServiceRegistry.GetService("Goto")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for round := 0; round < 20; round++ {
				lock.Lock()
				if ActiveServices[i] == nil {
					ActiveServices[i] = map[string]*gotogrpc.GRPCService{}
				}
				ActiveServices[i]["Goto"] = svc
				lock.Unlock()
			}
		}()
		go func() {
			defer wg.Done()
			for round := 0; round < 20; round++ {
				callAPI(t, port, "GET", "/grpc/server/services/active", "")
			}
		}()
	}
	wg.Wait()
}

// The services were listed from the map of the registry without its lock, while services are added to it.
func TestRegressionServicesAreListedWithTheLockOfTheRegistry(t *testing.T) {
	stubGlobals(t)
	resetRegistry(t)
	port := newAPIPort(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for round := 0; round < 10; round++ {
				ServiceRegistry.GetOrCreateService(cloneDesc(&pb.SampleService_ServiceDesc))
				ServiceRegistry.RemoveService("SampleService")
			}
		}()
		go func() {
			defer wg.Done()
			for round := 0; round < 10; round++ {
				callAPI(t, port, "GET", "/grpc/server/services", "")
			}
		}()
	}
	wg.Wait()
}

// A method that is in the desc of a service, but that the service doesn't know of, was found as a nil method that was
// used, which panicked.
func TestRegressionInterceptOfAMethodThatTheServiceDoesNotKnow(t *testing.T) {
	stubGlobals(t)
	resetRegistry(t)
	svc := newService(t, &pb.SampleService_ServiceDesc)
	svc.GSD.Methods = append(svc.GSD.Methods, grpc.MethodDesc{MethodName: "extra"})
	svc.GSD.Streams = append(svc.GSD.Streams, grpc.StreamDesc{StreamName: "extraStream", ServerStreams: true})
	mustNotPanic(t, "InterceptServiceMethod of a method that the service doesn't know of", func() {
		if err := GRPCManager.InterceptServiceMethod(svc, svc, "extra", "extra", nil, false, true); err == nil {
			t.Errorf("a method that the service doesn't know of must be an error")
		}
		if err := GRPCManager.InterceptServiceMethod(svc, svc, "extraStream", "extraStream", nil, true, true); err == nil {
			t.Errorf("a stream that the service doesn't know of must be an error")
		}
	})
}

// Services that were not there, or that could not be loaded, took the interceptors down.
func TestRegressionInterceptOfServicesThatAreNotThere(t *testing.T) {
	stubGlobals(t)
	resetRegistry(t)
	mustNotPanic(t, "InterceptServiceMethods without services", func() {
		if err := GRPCManager.InterceptServiceMethods(nil, nil, nil, true); err == nil {
			t.Errorf("services that are not there must be an error")
		}
	})
	mustNotPanic(t, "InterceptAndServe of a service that can't be loaded", func() {
		GRPCManager.InterceptAndServe(unloadableDesc(), &sampleImpl{})
	})
	mustNotPanic(t, "InterceptWithMiddleware of a service that can't be loaded", func() {
		GRPCManager.InterceptWithMiddleware(unloadableDesc(), &sampleImpl{})
	})
	mustNotPanic(t, "Reflect of a service that can't be loaded", func() {
		GRPCManager.Reflect(unloadableDesc())
	})
}

// The target of a proxy was looked up by its short name, but services are registered by their full names, so a service
// that has a package was never found, and replaced the one that was registered, each time.
func TestRegressionProxyFindsTheTargetByFullName(t *testing.T) {
	stubGlobals(t)
	resetRegistry(t)
	oldServices := SIP.services
	SIP.services = map[string]grpc.ServiceInfo{}
	t.Cleanup(func() { SIP.services = oldServices })
	const name = "envoy.service.auth.v3.Authorization"
	from, to := GRPCManager.InterceptAndProxy(cloneDesc(&extauthz.Authorization_ServiceDesc), cloneDesc(&extauthz.Authorization_ServiceDesc), name, nil, 0)
	if from == nil || to == nil {
		t.Fatalf("InterceptAndProxy = %v %v", from, to)
	}
	if registered := ServiceRegistry.GetService(name); registered == nil || registered != from.(*gotogrpc.GRPCService) || registered != to.(*gotogrpc.GRPCService) {
		t.Errorf("the registry has %p, want the one that the proxy is of: %p and %p", registered, from, to)
	}
}

// A proxy between services that don't have the same methods panicked, and the error of intercepting was not looked at.
func TestRegressionProxyOfServicesThatDontMatch(t *testing.T) {
	stubGlobals(t)
	resetRegistry(t)
	mustNotPanic(t, "InterceptAndProxy of services that don't match", func() {
		from, to := GRPCManager.InterceptAndProxy(cloneDesc(&pb.Goto_ServiceDesc), cloneDesc(&pb.SampleService_ServiceDesc), "SampleService", nil, 0)
		if from != nil || to != nil {
			t.Errorf("InterceptAndProxy = %v %v, want none", from, to)
		}
	})
	if f, _, _ := ServiceRegistry.GetProxyService("Goto"); f != nil {
		t.Errorf("a proxy that failed must not be registered")
	}
}

// A service that was registered again kept being served by the server that it was first registered with.
func TestRegressionInterceptAgainUsesTheNewServer(t *testing.T) {
	stubGlobals(t)
	resetRegistry(t)
	sd := cloneDesc(&pb.SampleService_ServiceDesc)
	first, second := &sampleImpl{}, &sampleImpl{}
	GRPCManager.InterceptWithMiddleware(sd, first)
	GRPCManager.InterceptWithMiddleware(sd, second)
	svc := ServiceRegistry.GetActiveService("SampleService")
	ts := startServer(t, func(s *grpc.Server, _ *testServer) { s.RegisterService(svc.GSD, svc.Server) })
	if resp, _, err := ts.oneOne(t, "x"); err != nil || resp.Content != "re:x" {
		t.Fatalf("OneOne = %v %v", resp, err)
	}
	if first.calls.Load() != 0 || second.calls.Load() != 1 {
		t.Errorf("calls to the first server %d and to the second %d, want 0 and 1", first.calls.Load(), second.calls.Load())
	}
}

// The interceptors logged to the context that the common handler gave, which is nil when it fails.
func TestRegressionInterceptWithoutAPeerDoesNotPanic(t *testing.T) {
	stubGlobals(t)
	resetRegistry(t)
	mustNotPanic(t, "a call without a peer", func() {
		if _, err := (&GRPCMethodInterceptor{}).Intercept(nil, context.Background(), nil, nil); err == nil {
			t.Errorf("a call without a peer must fail")
		}
	})
	mustNotPanic(t, "a stream without a peer", func() {
		if err := (&GRPCStreamInterceptor{}).Intercept(nil, &fakeServerStream{ctx: context.Background()}); err == nil {
			t.Errorf("a stream without a peer must fail")
		}
	})
	mustNotPanic(t, "the unary handler without a peer", func() {
		if _, err := unaryHandler(context.Background(), nil); err == nil {
			t.Errorf("a call without a peer must fail")
		}
	})
}

// A request that could not be decoded was taken for a message and proxied, which panicked.
func TestRegressionProxyUnaryOfARequestThatCantBeRead(t *testing.T) {
	_, svc := startGenericServer(t)
	method := svc.Methods["oneOne"]
	var proxied atomic.Int32
	useProxy(t, func(context.Context, int, *gotogrpc.GRPCServiceMethod, metadata.MD, []proto.Message) ([]proto.Message, metadata.MD, metadata.MD, error) {
		proxied.Add(1)
		return nil, nil, nil, nil
	}, nil)
	mustNotPanic(t, "ProxyUnary of a request that can't be read", func() {
		if _, err := ProxyUnary(context.Background(), 1, method, "r", "a", metadata.MD{}, nil); err == nil {
			t.Errorf("a request that can't be read must be an error")
		}
		var unread *struct{}
		if _, err := ProxyUnary(context.Background(), 1, method, "r", "a", metadata.MD{}, unread); err == nil {
			t.Errorf("a request that is not a message must be an error")
		}
	})
	if proxied.Load() != 0 {
		t.Errorf("a request that can't be read must not be proxied")
	}
}

// The flags that tell if the services of Goto are registered were reset by the goroutine that serves, when the server
// stopped, while the server was started again, without anything to order the two.
func TestRegressionServiceFlagsAreGuarded(t *testing.T) {
	setRunning(t, false, false)
	fm := &fakeManager{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(4)
		go func() { defer wg.Done(); RegisterGotoServer(fm) }()
		go func() { defer wg.Done(); OnGRPCStop() }()
		go func() { defer wg.Done(); RegisterExtAuthzServer(fm) }()
		go func() { defer wg.Done(); OnExtAuthzStop() }()
	}
	wg.Wait()
}

// The listeners of the server were iterated by the goroutines that start and recycle, while a listener was added, and
// the server that is running or not was read while it changed.
func TestRegressionServerStateIsGuarded(t *testing.T) {
	stubGlobals(t)
	g := newGRPCServer()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	oldServer := TheGRPCServer
	TheGRPCServer = g
	t.Cleanup(func() { TheGRPCServer = oldServer })
	port := ln.Addr().(*net.TCPAddr).Port
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			for round := 0; round < 50; round++ {
				// a listener that is open, as the ones of the server are when it is recycled
				l := &listeners.Listener{ListenerID: fmt.Sprintf("guarded-%d-%d", i, round), Port: port, Listener: ln, IsGRPC: true}
				GRPCManager.AddListener(l)
			}
		}()
		go func() {
			defer wg.Done()
			for round := 0; round < 50; round++ {
				g.recycleListeners(0)
			}
		}()
		go func() {
			defer wg.Done()
			for round := 0; round < 50; round++ {
				g.Stop()
				g.isRunning()
			}
		}()
	}
	wg.Wait()
	if n := len(g.listenerList()); n != 8*50 {
		t.Errorf("the server has %d listeners, want %d", n, 8*50)
	}
}

// Starting the server again after it was stopped served with the grpc server that was stopped, which doesn't serve.
func TestRegressionServerStartsAgainAfterItStopped(t *testing.T) {
	lc := startLifecycle(t)
	l := lc.newListener(t, "regression-1")
	GRPCManager.ServeListener(l)
	if _, err := call(t, l.Port, "one"); err != nil {
		t.Fatalf("call: %v", err)
	}
	lc.g.Stop()
	GRPCManager.ServeListener(l)
	if resp, err := call(t, l.Port, "again"); err != nil || resp.Content != "re:again" {
		t.Errorf("call after the server started again = %v %v", resp, err)
	}
}

// Stopping the server of a process that has none, as the shutdown does when it is early, panicked.
func TestRegressionStopOfNoServerDoesNotPanic(t *testing.T) {
	mustNotPanic(t, "Stop of no server", func() {
		var g *GRPCServer
		g.Stop()
	})
}

// Stopping waited for 2 seconds whatever happened, instead of for the server to be stopped.
func TestRegressionStopDoesNotSleepAFixedTime(t *testing.T) {
	lc := startLifecycle(t)
	l := lc.newListener(t, "regression-1")
	GRPCManager.ServeListener(l)
	if _, err := call(t, l.Port, "one"); err != nil {
		t.Fatalf("call: %v", err)
	}
	start := time.Now()
	lc.g.Stop()
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Stop of a server with nothing to wait for took %s", elapsed)
	}
}

// Changes of the server by many requests at once must be served in turn, and all of them take effect.
func TestRegressionConcurrentListenersAllServe(t *testing.T) {
	lc := startLifecycle(t)
	var ls []*listeners.Listener
	for i := 0; i < 4; i++ {
		ls = append(ls, lc.newListener(t, "regression-"+strconv.Itoa(i)))
	}
	var wg sync.WaitGroup
	for _, l := range ls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			GRPCManager.ServeListener(l)
		}()
	}
	wg.Wait()
	for _, l := range ls {
		if resp, err := call(t, l.Port, "c"); err != nil || resp.Content != "re:c" {
			t.Errorf("call on port %d = %v %v", l.Port, resp, err)
		}
	}
}

// Something that is not a listener, given to the manager of the server as the global package does, panicked.
func TestRegressionListenerOfAnotherTypeIsRefused(t *testing.T) {
	startLifecycle(t)
	mustNotPanic(t, "AddListener of a string", func() { GRPCManager.AddListener("not a listener") })
	mustNotPanic(t, "AddListener of a nil", func() { GRPCManager.AddListener(nil) })
	mustNotPanic(t, "AddListener of a nil listener", func() { GRPCManager.AddListener((*listeners.Listener)(nil)) })
	mustNotPanic(t, "ServeListener of a number", func() { GRPCManager.ServeListener(42) })
	if n := len(TheGRPCServer.listenerList()); n != 0 {
		t.Errorf("listeners of the server = %d, want none", n)
	}
}

// The services of the info provider were added to a map that is used from the operations that run at once, and the
// provider could not tell a service without a descriptor.
func TestRegressionServiceInfoProviderIsGuarded(t *testing.T) {
	sip := &ServiceInfoProvider{services: map[string]grpc.ServiceInfo{}}
	mustNotPanic(t, "AddService of nothing", func() {
		sip.AddService(nil)
		sip.AddService(&gotogrpc.GRPCService{Name: "without-descriptor"})
	})
	if si := sip.GetService("without-descriptor"); si.Metadata != nil {
		t.Errorf("service without a descriptor has metadata %v", si.Metadata)
	}
	var wg sync.WaitGroup
	for w := 0; w < 6; w++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				sip.AddService(&gotogrpc.GRPCService{Name: "svc-" + strconv.Itoa(w) + "-" + strconv.Itoa(i), GSD: &grpc.ServiceDesc{Metadata: "m.proto"}})
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_ = sip.GetServiceInfo()
				_ = sip.GetService("svc-0-0")
			}
		}()
	}
	wg.Wait()
	if n := len(sip.GetServiceInfo()); n != 6*50+1 {
		t.Errorf("services = %d, want %d", n, 6*50+1)
	}
}
