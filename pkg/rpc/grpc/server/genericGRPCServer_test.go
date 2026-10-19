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
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gotogrpc "goto/pkg/rpc/grpc"
	"goto/pkg/rpc/grpc/pb"
	"goto/pkg/server/middleware"
	"goto/pkg/server/response/payload"
	"goto/pkg/util"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

// chainRequest is what the middleware chain got of a request.
type chainRequest struct {
	method, uri, host, body string
	header                  http.Header
}

// fakeChain stands for the middleware chain of the server, which the generic handlers send the requests to. It records
// the requests, and answers each with the bodies that it is given, as the handlers of the chain do.
type fakeChain struct {
	lock      sync.Mutex
	requests  []chainRequest
	responses []string
	headers   map[string]string
	calls     atomic.Int32
}

func (f *fakeChain) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.lock.Lock()
	f.requests = append(f.requests, chainRequest{r.Method, r.RequestURI, r.Host, string(body), r.Header.Clone()})
	responses, headers := f.responses, f.headers
	f.lock.Unlock()
	f.calls.Add(1)
	for k, v := range headers {
		w.Header().Set(k, v)
	}
	for _, resp := range responses {
		w.Write([]byte(resp))
	}
}

func (f *fakeChain) got() []chainRequest {
	f.lock.Lock()
	defer f.lock.Unlock()
	return append([]chainRequest(nil), f.requests...)
}

// useChain puts a fake chain in the place of the middleware chain of the server, for a test.
func useChain(t *testing.T, responses ...string) *fakeChain {
	t.Helper()
	chain := &fakeChain{responses: responses}
	old := middleware.MiddlewareGRPCChainHead
	middleware.MiddlewareGRPCChainHead = chain
	t.Cleanup(func() { middleware.MiddlewareGRPCChainHead = old })
	return chain
}

// startGenericServer serves the sample service as a service that has no implementation, the way the services that
// are loaded from reflection are: every call goes to the generic handlers, and so to the middleware chain.
func startGenericServer(t *testing.T) (*testServer, *gotogrpc.GRPCService) {
	t.Helper()
	stubGlobals(t)
	resetRegistry(t)
	svc := ServiceRegistry.NewGRPCServiceFromSD(cloneDesc(&pb.SampleService_ServiceDesc))
	if svc == nil {
		t.Fatalf("the sample service can't be loaded")
	}
	svc.Server = &gotogrpc.DynamicServiceStub{}
	ts := startServer(t, func(s *grpc.Server, _ *testServer) { s.RegisterService(svc.GSD, svc.Server) })
	t.Cleanup(func() { payload.PayloadManager.ClearRPCResponsePayloads(ts.port) })
	return ts, svc
}

func TestGenericUnaryHandler(t *testing.T) {
	ts, svc := startGenericServer(t)
	chain := useChain(t, `{"id":7,"content":"from the chain"}`)
	chain.headers = map[string]string{"x-from-chain": "yes"}
	resp, header, err := ts.oneOne(t, "hi", "x-in", "in-value")
	if err != nil {
		t.Fatalf("OneOne: %v", err)
	}
	if resp.Id != 7 || resp.Content != "from the chain" {
		t.Errorf("response = %v, want the one that the chain wrote", resp)
	}
	if got := header.Get("x-from-chain"); len(got) != 1 || got[0] != "yes" {
		t.Errorf("headers = %v, want the header that the chain set", header)
	}
	reqs := chain.got()
	if len(reqs) != 1 {
		t.Fatalf("the chain got %d requests, want 1", len(reqs))
	}
	r := reqs[0]
	if r.uri != svc.Methods["oneOne"].URI || r.uri != "/sampleservice/oneone" {
		t.Errorf("uri = %q, want the uri of the method", r.uri)
	}
	if !strings.Contains(r.body, `"content":"hi"`) {
		t.Errorf("body = %q, want the request as JSON", r.body)
	}
	if r.host != ts.addr {
		t.Errorf("host = %q, want the authority %q", r.host, ts.addr)
	}
	if got := r.header.Get("X-In"); got != "in-value" {
		t.Errorf("the headers of the request must reach the chain: %v", r.header)
	}
}

func TestGenericUnaryHandlerWithoutAResponse(t *testing.T) {
	ts, _ := startGenericServer(t)
	chain := useChain(t)
	resp, _, err := ts.oneOne(t, "hi")
	if err != nil {
		t.Fatalf("a chain that has no response must not fail the call: %v", err)
	}
	if resp.Id != 0 || resp.Content != "" {
		t.Errorf("response = %v, want an empty one", resp)
	}
	if chain.calls.Load() != 1 {
		t.Errorf("the chain got %d requests, want 1", chain.calls.Load())
	}
}

func TestGenericUnaryHandlerWithConfiguredPayload(t *testing.T) {
	ts, svc := startGenericServer(t)
	chain := useChain(t, `{"id":7,"content":"from the chain"}`)
	uri := svc.Methods["oneOne"].URI
	if err := payload.PayloadManager.SetRPCResponsePayload(ts.port, false, []byte(`{"id":5,"content":"configured"}`), "application/json", uri, "", "", "", "", 0, 60*time.Millisecond, 60*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	resp, _, err := ts.oneOne(t, "hi")
	if err != nil || resp.Id != 5 || resp.Content != "configured" {
		t.Fatalf("response = %v %v, want the payload that is configured for the method", resp, err)
	}
	if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
		t.Errorf("the delay that is configured with the payload was not applied: %s", elapsed)
	}
	if chain.calls.Load() != 1 {
		t.Errorf("the chain still gets the request: %d", chain.calls.Load())
	}
	// a payload that is not a message of the response type fails the call, with the error of the payload
	payload.PayloadManager.ClearRPCResponsePayloads(ts.port)
	if err := payload.PayloadManager.SetRPCResponsePayload(ts.port, false, []byte(`{"nosuchfield":1}`), "application/json", uri, "", "", "", "", 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ts.oneOne(t, "hi"); err == nil || !strings.Contains(err.Error(), "nosuchfield") {
		t.Errorf("with a payload that doesn't fit the response = %v, want its error", err)
	}
}

func TestGenericStreamHandlers(t *testing.T) {
	ts, svc := startGenericServer(t)
	chain := useChain(t, `{"id":1,"content":"a"}`)
	before := func() int { return len(chain.got()) }

	n := before()
	got, err := ts.oneMany(t, "m", 1, "x-in", "in-value")
	if err != nil || len(got) != 1 || got[0] != "a" {
		t.Fatalf("OneMany = %v %v, want the response of the chain", got, err)
	}
	reqs := chain.got()
	if len(reqs)-n != 1 {
		t.Fatalf("OneMany: the chain got %d requests, want 1", len(reqs)-n)
	}
	if r := reqs[n]; r.uri != svc.Methods["oneMany"].URI || !strings.Contains(r.body, `"content":"m"`) || r.header.Get("X-In") != "in-value" {
		t.Errorf("OneMany: request of the chain = %+v", r)
	}

	n = before()
	resp, err := ts.manyOne(t, []string{"x", "y"})
	if err != nil || resp.Content != "a" {
		t.Fatalf("ManyOne = %v %v, want the response of the chain", resp, err)
	}
	if len(chain.got())-n != 2 {
		t.Errorf("ManyOne: the chain got %d requests, want one for each of the 2 messages", len(chain.got())-n)
	}

	n = before()
	echo, err := ts.manyMany(t, []string{"p", "q", "r"})
	if err != nil || len(echo) != 3 || echo[0] != "a" || echo[2] != "a" {
		t.Fatalf("ManyMany = %v %v, want a response for each message", echo, err)
	}
	if len(chain.got())-n != 3 {
		t.Errorf("ManyMany: the chain got %d requests, want 3", len(chain.got())-n)
	}
}

func TestGenericStreamHandlerWithConfiguredStreamPayload(t *testing.T) {
	ts, svc := startGenericServer(t)
	chain := useChain(t, `{"id":1,"content":"from the chain"}`)
	uri := svc.Methods["oneMany"].URI
	// 3 responses are made of the 2 messages of the payload, taken in turns
	if err := payload.PayloadManager.SetRPCResponsePayload(ts.port, true, []byte(`[{"id":1,"content":"a"},{"id":2,"content":"b"}]`), "application/json", uri, "", "", "", "", 3, 0, 0); err != nil {
		t.Fatal(err)
	}
	got, err := ts.oneMany(t, "m", 1)
	if err != nil || !equalStrings(got, []string{"a", "b", "a"}) {
		t.Errorf("OneMany with a stream payload = %v %v, want [a b a]", got, err)
	}
	if chain.calls.Load() != 1 {
		t.Errorf("the chain got %d requests, want 1", chain.calls.Load())
	}
}

func TestGenericHandlersThroughForcedStatus(t *testing.T) {
	ts, _ := startGenericServer(t)
	chain := useChain(t, `{"id":1,"content":"a"}`)
	force(t, ts.port, "", "/SampleService/", "", "", []int{503}, 3)
	if _, _, err := ts.oneOne(t, "x"); !isForced(err, 503) {
		t.Errorf("OneOne = %v, want the forced status", err)
	}
	if _, err := ts.oneMany(t, "x", 1); !isForced(err, 503) {
		t.Errorf("OneMany = %v, want the forced status", err)
	}
	if _, err := ts.manyOne(t, []string{"x"}); !isForced(err, 503) {
		t.Errorf("ManyOne = %v, want the forced status", err)
	}
	if chain.calls.Load() != 0 {
		t.Errorf("a call with a forced status must not reach the chain: %d", chain.calls.Load())
	}
	if resp, _, err := ts.oneOne(t, "x"); err != nil || resp.Content != "a" {
		t.Errorf("after the status is used up: %v %v", resp, err)
	}
}

func TestGenericHandlersConcurrentCalls(t *testing.T) {
	ts, _ := startGenericServer(t)
	chain := useChain(t, `{"id":1,"content":"a"}`)
	const workers = 16
	var wg sync.WaitGroup
	var bad atomic.Int32
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			client := pb.NewSampleServiceClient(ts.conn)
			if resp, err := client.OneOne(ctx, &pb.SampleRequest{Content: fmt.Sprint("c", i)}); err != nil || resp.Content != "a" {
				bad.Add(1)
			}
			stream, err := client.OneMany(ctx, &pb.SampleRequest{Id: 1, Content: "m"})
			if err != nil {
				bad.Add(1)
				return
			}
			for {
				if _, err := stream.Recv(); err != nil {
					if !errors.Is(err, io.EOF) {
						bad.Add(1)
					}
					break
				}
			}
		}()
	}
	wg.Wait()
	if bad.Load() != 0 {
		t.Errorf("%d calls failed or got the wrong response", bad.Load())
	}
	if n := chain.calls.Load(); n != 2*workers {
		t.Errorf("the chain got %d requests, want %d", n, 2*workers)
	}
}

func TestInterceptWithoutThePeerOrTheMethod(t *testing.T) {
	stubGlobals(t)
	resetRegistry(t)
	mi := &GRPCMethodInterceptor{}
	if _, err := mi.Intercept(nil, context.Background(), nil, nil); err == nil {
		t.Errorf("a call that has no peer must fail")
	}
	ctx := peer.NewContext(context.Background(), &peer.Peer{Addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}, LocalAddr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 2}})
	if _, err := mi.Intercept(nil, ctx, nil, nil); err == nil || !strings.Contains(err.Error(), "method not found") {
		t.Errorf("a call that has no method = %v, want an error that the method is not found", err)
	}
	si := &GRPCStreamInterceptor{}
	if err := si.Intercept(nil, &fakeServerStream{ctx: context.Background()}); err == nil {
		t.Errorf("a stream that has no peer must fail")
	}
	if err := si.Intercept(nil, &fakeServerStream{ctx: ctx}); err == nil || !strings.Contains(err.Error(), "method not found") {
		t.Errorf("a stream that has no method = %v, want an error that the method is not found", err)
	}
	if err := GRPCStreamHandler(nil, &fakeServerStream{ctx: ctx}); err == nil {
		t.Errorf("the stream handler must fail for a stream that has no method")
	}
}

// fakeServerStream is a server stream that has only a context.
type fakeServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (f *fakeServerStream) Context() context.Context { return f.ctx }

// useProxy makes the server proxy the calls, to the functions that stand for the proxy of Goto.
func useProxy(t *testing.T, unary gotogrpc.ProxyGRPCUnaryFunc, stream gotogrpc.ProxyGRPCStreamFunc) {
	t.Helper()
	oldUnary, oldStream, oldWill := gotogrpc.ProxyGRPCUnary, gotogrpc.ProxyGRPCStream, util.WillProxyGRPC
	gotogrpc.ProxyGRPCUnary, gotogrpc.ProxyGRPCStream = unary, stream
	util.WillProxyGRPC = func(int, any) bool { return true }
	t.Cleanup(func() {
		gotogrpc.ProxyGRPCUnary, gotogrpc.ProxyGRPCStream, util.WillProxyGRPC = oldUnary, oldStream, oldWill
	})
}

func outputMessage(t *testing.T, method *gotogrpc.GRPCServiceMethod, text string) proto.Message {
	t.Helper()
	desc := method.OutputType()
	msg := dynamicpb.NewMessage(desc)
	field := desc.Fields().ByName(protoreflect.Name("text"))
	if field == nil {
		field = desc.Fields().ByName(protoreflect.Name("content"))
	}
	msg.Set(field, protoreflect.ValueOfString(text))
	return msg
}

func TestProxiedUnaryCall(t *testing.T) {
	g := startGotoServer(t)
	var port atomic.Int32
	var method atomic.Value
	var gotInput atomic.Value
	useProxy(t, func(ctx context.Context, p int, m *gotogrpc.GRPCServiceMethod, md metadata.MD, in []proto.Message) ([]proto.Message, metadata.MD, metadata.MD, error) {
		port.Store(int32(p))
		method.Store(m.Name)
		gotInput.Store(fmt.Sprint(in[0]))
		return []proto.Message{outputMessage(t, m, "proxied")}, metadata.Pairs("x-proxied", "yes"), nil, nil
	}, nil)
	before := counter.Load()
	out, header, err := g.echo(t, "hello")
	if err != nil || out.Text != "proxied" {
		t.Fatalf("Echo = %v %v, want the output of the proxy", out, err)
	}
	if got := header.Get("x-proxied"); len(got) != 1 || got[0] != "yes" {
		t.Errorf("headers = %v, want the ones of the proxy", header)
	}
	if int(port.Load()) != g.port || method.Load() != "echo" {
		t.Errorf("the proxy got port %d method %v, want %d echo", port.Load(), method.Load(), g.port)
	}
	if got, _ := gotInput.Load().(string); !strings.Contains(got, "hello") {
		t.Errorf("the proxy got the input %q, want the request", got)
	}
	if counter.Load() != before {
		t.Errorf("a proxied call must not be served by the service")
	}
}

func TestProxiedUnaryCallFailure(t *testing.T) {
	g := startGotoServer(t)
	useProxy(t, func(context.Context, int, *gotogrpc.GRPCServiceMethod, metadata.MD, []proto.Message) ([]proto.Message, metadata.MD, metadata.MD, error) {
		return nil, nil, nil, status.Error(codes.Unavailable, "upstream is down")
	}, nil)
	if _, _, err := g.echo(t, "hello"); status.Code(err) != codes.Unavailable || !strings.Contains(err.Error(), "upstream is down") {
		t.Errorf("Echo = %v, want the error of the proxy", err)
	}
	// a proxy that has nothing to answer with gives an empty response
	useProxy(t, func(context.Context, int, *gotogrpc.GRPCServiceMethod, metadata.MD, []proto.Message) ([]proto.Message, metadata.MD, metadata.MD, error) {
		return nil, nil, nil, nil
	}, nil)
	if out, _, err := g.echo(t, "hello"); err != nil || out.Text != "" {
		t.Errorf("Echo with no output of the proxy = %v %v", out, err)
	}
}

func TestProxiedStreamCall(t *testing.T) {
	g := startGotoServer(t)
	var method atomic.Value
	var port atomic.Int32
	useProxy(t, nil, func(ctx context.Context, p int, m *gotogrpc.GRPCServiceMethod, remote string, md metadata.MD, stream gotogrpc.GRPCStream) (int, int, error) {
		port.Store(int32(p))
		method.Store(m.Name + "@" + remote)
		for _, text := range []string{"p1", "p2"} {
			if err := stream.Send(outputMessage(t, m, text)); err != nil {
				return 0, 0, err
			}
		}
		return 0, 2, nil
	})
	before := counter.Load()
	outs, err := g.streamOut(t, &pb.StreamConfig{ChunkCount: 5, Text: "x"})
	if err != nil || !equalStrings(texts(outs), []string{"p1", "p2"}) {
		t.Fatalf("StreamOut = %v %v, want the messages of the proxy", texts(outs), err)
	}
	if int(port.Load()) != g.port || !strings.HasPrefix(fmt.Sprint(method.Load()), "streamOut@127.0.0.1:") {
		t.Errorf("the proxy got port %d and %v", port.Load(), method.Load())
	}
	if counter.Load() != before {
		t.Errorf("a proxied call must not be served by the service")
	}
}

func TestProxyUnaryDirectly(t *testing.T) {
	_, svc := startGenericServer(t)
	method := svc.Methods["oneOne"]
	var gotPort int
	useProxy(t, func(ctx context.Context, p int, m *gotogrpc.GRPCServiceMethod, md metadata.MD, in []proto.Message) ([]proto.Message, metadata.MD, metadata.MD, error) {
		gotPort = p
		return []proto.Message{outputMessage(t, m, "x")}, nil, nil, nil
	}, nil)
	req := dynamicpb.NewMessage(method.InputType())
	resp, err := ProxyUnary(context.Background(), 4321, method, "1.2.3.4:5", "authority", metadata.MD{}, req)
	if err != nil || resp == nil || gotPort != 4321 {
		t.Errorf("ProxyUnary = %v %v with port %d", resp, err, gotPort)
	}
}
