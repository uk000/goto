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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goto/pkg/constants"
	"goto/pkg/global"
	gotogrpc "goto/pkg/rpc/grpc"
	"goto/pkg/rpc/grpc/extauthz"
	"goto/pkg/rpc/grpc/pb"
	"goto/pkg/server/listeners"
	gotostatus "goto/pkg/server/response/status"
	"goto/pkg/util"

	"github.com/gorilla/mux"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// The tests run real gRPC servers on 127.0.0.1:0, and call them with real clients. TCP is used over bufconn, since the
// generic handlers read the TCP address of the peer to know the port that a request is for.

var stubOnce sync.Once

// stubGlobals gives the hooks that the server code uses, and that the server of Goto sets when it starts.
func stubGlobals(t *testing.T) {
	t.Helper()
	stubOnce.Do(func() {
		global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
		global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
		global.Funcs.IsListenerPresent = func(int) bool { return false }
		global.Funcs.IsListenerOpen = func(int) bool { return false }
		global.Funcs.GetListenerID = func(int) string { return "" }
		global.Funcs.GetListenerLabel = func(*http.Request) string { return "" }
		global.Funcs.GetListenerLabelForPort = func(port int) string { return "listener-" + strconv.Itoa(port) }
		global.Funcs.IsListenerTLS = func(int) bool { return false }
		global.Funcs.CloseConnectionsForPort = func(int) {}
		util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
		// Created by the server at startup. Setting a response payload builds its URI matcher on util.MatchRouter.
		if util.MatchRouter == nil {
			util.CreateRouters(mux.NewRouter())
		}
	})
	oldProxy := util.WillProxyGRPC
	util.WillProxyGRPC = func(int, any) bool { return false }
	t.Cleanup(func() { util.WillProxyGRPC = oldProxy })
}

// resetRegistry gives a test a registry of services that is empty, and leaves one behind.
func resetRegistry(t *testing.T) {
	t.Helper()
	gotogrpc.ServiceRegistry.Init()
	t.Cleanup(gotogrpc.ServiceRegistry.Init)
}

// cloneDesc copies a service desc, so that intercepting the methods of the copy leaves the desc of the pb package as
// it is.
func cloneDesc(sd *grpc.ServiceDesc) *grpc.ServiceDesc {
	c := *sd
	c.Methods = append([]grpc.MethodDesc{}, sd.Methods...)
	c.Streams = append([]grpc.StreamDesc{}, sd.Streams...)
	return &c
}

// snapshotDescs saves the descs of the services of Goto, which the start of the server changes in place, and puts
// them back with the test.
func snapshotDescs(t *testing.T, descs ...*grpc.ServiceDesc) {
	t.Helper()
	for _, sd := range descs {
		saved := cloneDesc(sd)
		t.Cleanup(func() {
			copy(sd.Methods, saved.Methods)
			copy(sd.Streams, saved.Streams)
			sd.HandlerType = saved.HandlerType
		})
	}
}

type ctxInfo struct {
	port     int
	hasStore bool
	outPort  string
	md       metadata.MD
}

// sampleImpl serves the sample service of the pb package, and notes what the context of a call has.
type sampleImpl struct {
	pb.UnimplementedSampleServiceServer
	lock  sync.Mutex
	calls atomic.Int32
	infos []ctxInfo
}

func (s *sampleImpl) note(ctx context.Context) {
	s.calls.Add(1)
	info := ctxInfo{port: util.GetContextPort(ctx), hasStore: util.GetRequestStoreFromContextIfPresent(ctx) != nil}
	if out, ok := metadata.FromOutgoingContext(ctx); ok && len(out.Get("port")) > 0 {
		info.outPort = out.Get("port")[0]
	}
	info.md, _ = metadata.FromIncomingContext(ctx)
	s.lock.Lock()
	defer s.lock.Unlock()
	s.infos = append(s.infos, info)
}

func (s *sampleImpl) lastInfo() ctxInfo {
	s.lock.Lock()
	defer s.lock.Unlock()
	if len(s.infos) == 0 {
		return ctxInfo{}
	}
	return s.infos[len(s.infos)-1]
}

func (s *sampleImpl) OneOne(ctx context.Context, req *pb.SampleRequest) (*pb.SampleResponse, error) {
	s.note(ctx)
	if req.Content == "fail" {
		return nil, status.Error(codes.NotFound, "no such thing")
	}
	grpc.SetHeader(ctx, metadata.Pairs("x-from-handler", "yes"))
	return &pb.SampleResponse{Id: req.Id, Content: "re:" + req.Content}, nil
}

func (s *sampleImpl) OneMany(req *pb.SampleRequest, stream pb.SampleService_OneManyServer) error {
	s.note(stream.Context())
	if req.Content == "fail" {
		return status.Error(codes.NotFound, "no such thing")
	}
	for i := 1; i <= int(req.Id); i++ {
		if err := stream.Send(&pb.SampleResponse{Id: int32(i), Content: fmt.Sprintf("%s-%d", req.Content, i)}); err != nil {
			return err
		}
	}
	return nil
}

func (s *sampleImpl) ManyOne(stream pb.SampleService_ManyOneServer) error {
	s.note(stream.Context())
	var contents []string
	for {
		req, err := stream.Recv()
		if err != nil {
			break
		}
		contents = append(contents, req.Content)
	}
	return stream.SendAndClose(&pb.SampleResponse{Id: int32(len(contents)), Content: strings.Join(contents, ",")})
}

func (s *sampleImpl) ManyMany(stream pb.SampleService_ManyManyServer) error {
	s.note(stream.Context())
	for {
		req, err := stream.Recv()
		if err != nil {
			return nil
		}
		if err := stream.Send(&pb.SampleResponse{Id: req.Id, Content: "echo:" + req.Content}); err != nil {
			return err
		}
	}
}

// testServer is a gRPC server on a port of its own, with the interceptors that the Goto server has.
type testServer struct {
	port   int
	addr   string
	srv    *grpc.Server
	conn   *grpc.ClientConn
	sample *sampleImpl
}

func startServer(t *testing.T, register func(*grpc.Server, *testServer)) *testServer {
	t.Helper()
	stubGlobals(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ts := &testServer{
		port:   ln.Addr().(*net.TCPAddr).Port,
		addr:   ln.Addr().String(),
		srv:    grpc.NewServer(newGRPCServer().grpcOptions...),
		sample: &sampleImpl{},
	}
	register(ts.srv, ts)
	go ts.srv.Serve(ln)
	ts.conn, err = grpc.NewClient(ts.addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	t.Cleanup(func() {
		ts.conn.Close()
		ts.srv.Stop()
		gotostatus.TheStatusManager.Clear(ts.port, "", "")
	})
	return ts
}

func startSampleServer(t *testing.T) *testServer {
	t.Helper()
	return startServer(t, func(s *grpc.Server, ts *testServer) { pb.RegisterSampleServiceServer(s, ts.sample) })
}

func rpcCtx(t *testing.T, md ...string) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	if len(md) > 0 {
		ctx = metadata.AppendToOutgoingContext(ctx, md...)
	}
	return ctx
}

func (ts *testServer) oneOne(t *testing.T, content string, md ...string) (*pb.SampleResponse, metadata.MD, error) {
	t.Helper()
	var header metadata.MD
	resp, err := pb.NewSampleServiceClient(ts.conn).OneOne(rpcCtx(t, md...), &pb.SampleRequest{Id: 1, Content: content}, grpc.Header(&header))
	return resp, header, err
}

func (ts *testServer) oneMany(t *testing.T, content string, count int32, md ...string) ([]string, error) {
	t.Helper()
	stream, err := pb.NewSampleServiceClient(ts.conn).OneMany(rpcCtx(t, md...), &pb.SampleRequest{Id: count, Content: content})
	if err != nil {
		return nil, err
	}
	var out []string
	for {
		resp, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return out, nil
			}
			return out, err
		}
		out = append(out, resp.Content)
	}
}

func (ts *testServer) manyOne(t *testing.T, contents []string, md ...string) (*pb.SampleResponse, error) {
	t.Helper()
	stream, err := pb.NewSampleServiceClient(ts.conn).ManyOne(rpcCtx(t, md...))
	if err != nil {
		return nil, err
	}
	for _, c := range contents {
		if err := stream.Send(&pb.SampleRequest{Content: c}); err != nil {
			break
		}
	}
	return stream.CloseAndRecv()
}

func (ts *testServer) manyMany(t *testing.T, contents []string, md ...string) ([]string, error) {
	t.Helper()
	stream, err := pb.NewSampleServiceClient(ts.conn).ManyMany(rpcCtx(t, md...))
	if err != nil {
		return nil, err
	}
	var out []string
	for i, c := range contents {
		if err := stream.Send(&pb.SampleRequest{Id: int32(i), Content: c}); err != nil {
			return out, err
		}
		resp, err := stream.Recv()
		if err != nil {
			return out, err
		}
		out = append(out, resp.Content)
	}
	stream.CloseSend()
	if _, err := stream.Recv(); err != nil && !errors.Is(err, io.EOF) {
		return out, err
	}
	return out, nil
}

// force makes the manager answer the requests that match with the status.
func force(t *testing.T, port int, uriExact, uriPrefix, header, headerValue string, statuses []int, times int) {
	t.Helper()
	gotostatus.TheStatusManager.SetStatusFor(port, uriExact, uriPrefix, header, headerValue, nil, statuses, times, true)
	t.Cleanup(func() { gotostatus.TheStatusManager.Clear(port, "", "") })
}

func isForced(err error, statusCode int) bool {
	return status.Code(err) == codes.Internal && strings.Contains(status.Convert(err).Message(), fmt.Sprintf("%s=%d", constants.HeaderGotoForcedStatus, statusCode))
}

func TestUnaryMiddlewarePassesRequestsThrough(t *testing.T) {
	ts := startSampleServer(t)
	resp, header, err := ts.oneOne(t, "hello")
	if err != nil || resp.Content != "re:hello" {
		t.Fatalf("OneOne = %v %v", resp, err)
	}
	if got := header.Get("x-from-handler"); len(got) != 1 || got[0] != "yes" {
		t.Errorf("headers the handler sends must pass: %v", header)
	}
	info := ts.sample.lastInfo()
	if info.port != ts.port {
		t.Errorf("the handler's context has port %d, want the port of the server %d", info.port, ts.port)
	}
	if !info.hasStore {
		t.Errorf("the handler's context must have the request store")
	}
	if info.outPort != strconv.Itoa(ts.port) {
		t.Errorf("the outgoing metadata has port %q, want %d", info.outPort, ts.port)
	}

	// an error of the handler is the error of the call
	_, _, err = ts.oneOne(t, "fail")
	if status.Code(err) != codes.NotFound {
		t.Errorf("error of the handler = %v, want NotFound", err)
	}
}

func TestStreamMiddlewarePassesRequestsThrough(t *testing.T) {
	ts := startSampleServer(t)
	got, err := ts.oneMany(t, "m", 3)
	if err != nil || len(got) != 3 || got[0] != "m-1" || got[2] != "m-3" {
		t.Fatalf("OneMany = %v %v", got, err)
	}
	info := ts.sample.lastInfo()
	if info.port != ts.port || !info.hasStore || info.outPort != strconv.Itoa(ts.port) {
		t.Errorf("the context of a stream handler: %+v, want port %d with the request store", info, ts.port)
	}
	resp, err := ts.manyOne(t, []string{"a", "b", "c"})
	if err != nil || resp.Content != "a,b,c" || resp.Id != 3 {
		t.Fatalf("ManyOne = %v %v", resp, err)
	}
	echo, err := ts.manyMany(t, []string{"x", "y"})
	if err != nil || len(echo) != 2 || echo[0] != "echo:x" || echo[1] != "echo:y" {
		t.Fatalf("ManyMany = %v %v", echo, err)
	}
	_, err = ts.oneMany(t, "fail", 1)
	if status.Code(err) != codes.NotFound {
		t.Errorf("error of a stream handler = %v, want NotFound", err)
	}
}

func TestUnaryMiddlewareForcedStatus(t *testing.T) {
	ts := startSampleServer(t)
	force(t, ts.port, "/SampleService/oneOne", "", "", "", []int{503}, 2)
	for i, wantRemaining := range []string{"1", "0"} {
		resp, header, err := ts.oneOne(t, "x")
		if !isForced(err, 503) || resp != nil {
			t.Fatalf("call %d = %v %v, want the forced status 503", i, resp, err)
		}
		if got := header.Get(strings.ToLower(constants.HeaderGotoForcedStatus)); len(got) != 1 || got[0] != "503" {
			t.Errorf("call %d: forced status header = %v", i, got)
		}
		if got := header.Get(strings.ToLower(constants.HeaderGotoForcedStatusRemaining)); len(got) != 1 || got[0] != wantRemaining {
			t.Errorf("call %d: remaining header = %v, want %s", i, got, wantRemaining)
		}
		if got := header.Get(strings.ToLower(constants.HeaderGotoPort)); len(got) != 1 || got[0] != strconv.Itoa(ts.port) {
			t.Errorf("call %d: port header = %v", i, got)
		}
	}
	if n := ts.sample.calls.Load(); n != 0 {
		t.Errorf("a call with a forced status must not reach the handler, got %d", n)
	}
	// the status is used up: the call is served
	if resp, _, err := ts.oneOne(t, "x"); err != nil || resp.Content != "re:x" {
		t.Errorf("after the forced status is used up: %v %v", resp, err)
	}
	// a method that the status is not for is served
	force(t, ts.port, "/SampleService/oneMany", "", "", "", []int{500}, -1)
	if resp, _, err := ts.oneOne(t, "y"); err != nil || resp.Content != "re:y" {
		t.Errorf("a call that the status is not for: %v %v", resp, err)
	}
}

func TestStreamMiddlewareForcedStatus(t *testing.T) {
	ts := startSampleServer(t)
	force(t, ts.port, "", "/SampleService/", "", "", []int{502}, 3)
	if _, err := ts.oneMany(t, "m", 2); !isForced(err, 502) {
		t.Errorf("OneMany = %v, want the forced status 502", err)
	}
	if _, err := ts.manyOne(t, []string{"a"}); !isForced(err, 502) {
		t.Errorf("ManyOne = %v, want the forced status 502", err)
	}
	if _, err := ts.manyMany(t, []string{"a"}); !isForced(err, 502) {
		t.Errorf("ManyMany = %v, want the forced status 502", err)
	}
	if n := ts.sample.calls.Load(); n != 0 {
		t.Errorf("calls with a forced status must not reach the handler, got %d", n)
	}
	// the three are used, and the stream is served
	if got, err := ts.oneMany(t, "m", 2); err != nil || len(got) != 2 {
		t.Errorf("after the forced status is used up: %v %v", got, err)
	}
}

func TestForcedStatusRules(t *testing.T) {
	ts := startSampleServer(t)
	// the status of a method wins over the status of all the requests, whichever was set first
	force(t, ts.port, "", "", "", "", []int{503}, -1)
	force(t, ts.port, "/SampleService/oneOne", "", "", "", []int{200}, -1)
	if resp, _, err := ts.oneOne(t, "ok"); err != nil || resp.Content != "re:ok" {
		t.Errorf("a status of 200 for the method must let the call through: %v %v", resp, err)
	}
	if _, err := ts.manyOne(t, []string{"a"}); !isForced(err, 503) {
		t.Errorf("the status for all the requests applies to the other methods: %v", err)
	}
	gotostatus.TheStatusManager.Clear(ts.port, "", "")

	// 0 for all the requests is no status
	gotostatus.TheStatusManager.SetStatusFor(ts.port, "", "", "", "", nil, []int{0}, -1, true)
	if resp, _, err := ts.oneOne(t, "zero"); err != nil || resp.Content != "re:zero" {
		t.Errorf("a status of 0 must not fail the call: %v %v", resp, err)
	}
}

func TestForcedStatusCountsAcrossConcurrentCalls(t *testing.T) {
	ts := startSampleServer(t)
	const calls, forced = 40, 15
	force(t, ts.port, "", "/SampleService/", "", "", []int{503}, forced)
	var failed, served atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < calls; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var err error
			if i%2 == 0 {
				_, _, err = ts.oneOne(t, "c")
			} else {
				_, err = ts.manyOne(t, []string{"c"})
			}
			if err == nil {
				served.Add(1)
			} else if isForced(err, 503) {
				failed.Add(1)
			}
		}()
	}
	wg.Wait()
	if failed.Load() != forced || served.Load() != calls-forced {
		t.Errorf("failed %d served %d, want exactly %d and %d", failed.Load(), served.Load(), forced, calls-forced)
	}
	if n := ts.sample.calls.Load(); n != calls-forced {
		t.Errorf("handler calls = %d, want %d", n, calls-forced)
	}
}

func TestWrappedStream(t *testing.T) {
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "v")
	ws := NewWrappedStream(nil, ctx)
	if ws.Context() != ctx {
		t.Errorf("a wrapped stream has the context given")
	}
	if ws.Context().Value(key{}) != "v" {
		t.Errorf("value of the wrapped context lost")
	}
}

func TestSetHeaders(t *testing.T) {
	var gotReq, gotResp map[string]string
	ts := startServer(t, func(s *grpc.Server, ts *testServer) {
		s.RegisterService(&grpc.ServiceDesc{
			ServiceName: "SampleService",
			HandlerType: (*pb.SampleServiceServer)(nil),
			Methods: []grpc.MethodDesc{{MethodName: "oneOne", Handler: func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
				gotReq, gotResp = SetHeaders(ctx, ts.port, "my-host", "my-listener", map[string]string{"X-Added": "a", constants.HeaderGotoHost: "override"})
				return &pb.SampleResponse{Content: "done"}, nil
			}}},
			Metadata: "sample.proto",
		}, &pb.UnimplementedSampleServiceServer{})
	})
	var header metadata.MD
	_, err := pb.NewSampleServiceClient(ts.conn).OneOne(rpcCtx(t, "x-in", "in-value"), &pb.SampleRequest{}, grpc.Header(&header))
	if err != nil {
		t.Fatal(err)
	}
	if gotResp[constants.HeaderGotoPort] != strconv.Itoa(ts.port) || gotResp[constants.HeaderGotoProtocol] != "gRPC" {
		t.Errorf("response headers = %v", gotResp)
	}
	if gotResp[constants.HeaderViaGoto] != "listener-"+strconv.Itoa(ts.port) {
		t.Errorf("%s = %q", constants.HeaderViaGoto, gotResp[constants.HeaderViaGoto])
	}
	if gotResp[constants.HeaderGotoRPCMethod] != "/SampleService/oneOne" {
		t.Errorf("%s = %q", constants.HeaderGotoRPCMethod, gotResp[constants.HeaderGotoRPCMethod])
	}
	if !strings.HasPrefix(gotResp[constants.HeaderGotoRemoteAddress], "127.0.0.1:") {
		t.Errorf("%s = %q", constants.HeaderGotoRemoteAddress, gotResp[constants.HeaderGotoRemoteAddress])
	}
	if gotResp[constants.HeaderGotoHost] != "override" || gotResp["X-Added"] != "a" {
		t.Errorf("the headers to add win over the defaults: %v", gotResp)
	}
	if gotResp["Request-x-in"] != "in-value" || gotReq["x-in"] != "in-value" {
		t.Errorf("request headers must be reflected: req %v resp %v", gotReq, gotResp)
	}
	if _, found := gotResp["Request-:authority"]; found {
		t.Errorf("pseudo header names must lose the colon: %v", gotResp)
	}
	if gotResp["Request-authority"] != ts.addr {
		t.Errorf("Request-authority = %q, want %q", gotResp["Request-authority"], ts.addr)
	}
	// and the client got them as response headers
	if got := header.Get("x-added"); len(got) != 1 || got[0] != "a" {
		t.Errorf("client headers = %v", header)
	}
	if got := header.Get(strings.ToLower(constants.HeaderGotoHost)); len(got) != 1 || got[0] != "override" {
		t.Errorf("client headers = %v", header)
	}
}

// lifecycle holds a server of Goto that is built the way StartDefaultGRPCServer builds it, with the sample service as
// its active service and a listener on 127.0.0.1.
type lifecycle struct {
	g      *GRPCServer
	sample *sampleImpl
	lock   sync.Mutex
	opened []*listeners.Listener
}

var (
	activeLifecycle atomic.Pointer[lifecycle]
	watcherOnce     sync.Once
)

// newListener gives a gRPC listener of the server, on a port that the system picks. It is open as the listeners of
// Goto are after they are opened.
func (lc *lifecycle) newListener(t *testing.T, id string) *listeners.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	l := &listeners.Listener{ListenerID: id, Label: id, Port: ln.Addr().(*net.TCPAddr).Port, Listener: ln, IsGRPC: true, Open: true}
	lc.lock.Lock()
	lc.opened = append(lc.opened, l)
	lc.lock.Unlock()
	return l
}

// onStop does what the listeners package does when the server stops: it closes the listeners of the server, so that
// the start that follows opens them again. That is on the loopback here, where the package opens all the interfaces.
func (lc *lifecycle) onStop() {
	lc.lock.Lock()
	defer lc.lock.Unlock()
	for _, l := range lc.opened {
		l.Close()
		if ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(l.Port))); err == nil {
			l.Listener = ln
		}
	}
}

// startLifecycle sets up the server with the sample service, but doesn't start it. The registrations that the start of
// the server makes are on the descs of the pb package, which are put back with the test, and so are the flags and the
// globals.
func startLifecycle(t *testing.T) *lifecycle {
	t.Helper()
	stubGlobals(t)
	resetRegistry(t)
	snapshotDescs(t, &pb.Goto_ServiceDesc, &pb.Goto2_ServiceDesc, &pb.Goto3_ServiceDesc, &extauthz.Authorization_ServiceDesc)
	setRunning(t, false, false)
	oldServer, oldServices := TheGRPCServer, SIP.services
	SIP.services = map[string]grpc.ServiceInfo{}
	TheGRPCServer = newGRPCServer()
	TheGRPCServer.refreshServer()
	lc := &lifecycle{g: TheGRPCServer, sample: &sampleImpl{}}
	watcherOnce.Do(func() {
		global.AddGRPCStopWatcher(func() {
			if lc := activeLifecycle.Load(); lc != nil {
				lc.onStop()
			}
		})
	})
	activeLifecycle.Store(lc)
	svc := ServiceRegistry.GetOrCreateService(cloneDesc(&pb.SampleService_ServiceDesc))
	if svc == nil {
		t.Fatalf("the sample service can't be loaded")
	}
	svc.Server = lc.sample
	ServiceRegistry.AddActiveService(svc)
	t.Cleanup(func() {
		lc.g.Stop()
		activeLifecycle.Store(nil)
		lc.lock.Lock()
		defer lc.lock.Unlock()
		for _, l := range lc.opened {
			l.Close()
		}
		TheGRPCServer, SIP.services = oldServer, oldServices
	})
	return lc
}

// call makes a unary call of the sample service on the port, and waits for the server to be there if it isn't yet.
func call(t *testing.T, port int, content string) (*pb.SampleResponse, error) {
	t.Helper()
	conn, err := grpc.NewClient(net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	defer conn.Close()
	return pb.NewSampleServiceClient(conn).OneOne(rpcCtx(t), &pb.SampleRequest{Id: 1, Content: content}, grpc.WaitForReady(true))
}

// callWithin is call with a limit of time, for the calls that must not be served.
func callWithin(t *testing.T, port int, timeout time.Duration) error {
	t.Helper()
	conn, err := grpc.NewClient(net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, err = pb.NewSampleServiceClient(conn).OneOne(ctx, &pb.SampleRequest{Id: 1, Content: "x"})
	return err
}

func (lc *lifecycle) services() map[string]grpc.ServiceInfo {
	lc.g.lock.Lock()
	server := lc.g.Server
	lc.g.lock.Unlock()
	return server.GetServiceInfo()
}

func sampleService(t *testing.T) *gotogrpc.GRPCService {
	t.Helper()
	svc := ServiceRegistry.GetService("SampleService")
	if svc == nil {
		t.Fatalf("SampleService is not registered")
	}
	return svc
}

func TestServeListenerStartsTheServer(t *testing.T) {
	lc := startLifecycle(t)
	if lc.g.isRunning() {
		t.Fatalf("the server is not running before a listener is served")
	}
	l := lc.newListener(t, "lifecycle-1")
	GRPCManager.ServeListener(l)
	if !lc.g.isRunning() {
		t.Errorf("ServeListener must start the server")
	}
	if resp, err := call(t, l.Port, "one"); err != nil || resp.Content != "re:one" {
		t.Fatalf("call = %v %v", resp, err)
	}
	if lc.sample.calls.Load() != 1 {
		t.Errorf("the service got %d calls, want 1", lc.sample.calls.Load())
	}
	services := lc.services()
	for _, name := range []string{"SampleService", "Goto", "Goto2", "Goto3", "envoy.service.auth.v3.Authorization",
		"grpc.reflection.v1.ServerReflection", "grpc.reflection.v1alpha.ServerReflection"} {
		if _, found := services[name]; !found {
			t.Errorf("service %s is not served: %v", name, services)
		}
	}
	if running, ext := servicesRunning(); !running || !ext {
		t.Errorf("the services of Goto are registered by the start: goto %v extauthz %v", running, ext)
	}
	if info := SIP.GetService("SampleService"); len(info.Methods) != 4 {
		t.Errorf("service info of SampleService = %+v, want its 4 methods", info)
	}
	if _, found := SIP.GetServiceInfo()["Goto"]; !found {
		t.Errorf("the services that are served are in the service info: %v", SIP.GetServiceInfo())
	}
}

func TestStopServer(t *testing.T) {
	lc := startLifecycle(t)
	l := lc.newListener(t, "lifecycle-1")
	GRPCManager.ServeListener(l)
	if _, err := call(t, l.Port, "one"); err != nil {
		t.Fatalf("call: %v", err)
	}
	lc.g.Stop()
	if lc.g.isRunning() {
		t.Errorf("Stop must end the running state")
	}
	if running, ext := servicesRunning(); running || ext {
		t.Errorf("a stop must tell the services of Goto: goto %v extauthz %v", running, ext)
	}
	if err := callWithin(t, l.Port, 500*time.Millisecond); err == nil {
		t.Errorf("a call after the server stopped must fail")
	}
	// stopping the server that is stopped is nothing
	start := time.Now()
	lc.g.Stop()
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("a second Stop took %s", elapsed)
	}
	// and the server starts again
	GRPCManager.ServeListener(l)
	if resp, err := call(t, l.Port, "again"); err != nil || resp.Content != "re:again" {
		t.Errorf("call after the server started again = %v %v", resp, err)
	}
}

func TestStopOfNoServer(t *testing.T) {
	var g *GRPCServer
	g.Stop()
}

func TestServeListenerWhileRunningRestarts(t *testing.T) {
	lc := startLifecycle(t)
	first := lc.newListener(t, "lifecycle-1")
	GRPCManager.ServeListener(first)
	if _, err := call(t, first.Port, "one"); err != nil {
		t.Fatalf("call: %v", err)
	}
	second := lc.newListener(t, "lifecycle-2")
	GRPCManager.ServeListener(second)
	for _, l := range []*listeners.Listener{first, second} {
		if resp, err := call(t, l.Port, "both"); err != nil || resp.Content != "re:both" {
			t.Errorf("call on port %d after the second listener = %v %v", l.Port, resp, err)
		}
	}
	if n := len(lc.g.listenerList()); n != 2 {
		t.Errorf("the server has %d listeners, want 2", n)
	}
}

func TestServeAndStopService(t *testing.T) {
	lc := startLifecycle(t)
	l := lc.newListener(t, "lifecycle-1")
	GRPCManager.ServeListener(l)
	svc := sampleService(t)
	GRPCManager.StopService(svc)
	if err := callWithin(t, l.Port, 5*time.Second); status.Code(err) != codes.Unimplemented {
		t.Errorf("a service that was stopped = %v, want Unimplemented", err)
	}
	if _, found := lc.services()["SampleService"]; found {
		t.Errorf("a service that was stopped must not be registered with the server")
	}
	if _, found := lc.services()["Goto"]; !found {
		t.Errorf("the other services stay")
	}
	GRPCManager.Serve(l.Port, svc)
	if resp, err := call(t, l.Port, "back"); err != nil || resp.Content != "re:back" {
		t.Errorf("call after the service is served again = %v %v", resp, err)
	}
	// ServeMulti serves many at once
	GRPCManager.StopService(svc)
	GRPCManager.ServeMulti(map[int][]*gotogrpc.GRPCService{l.Port: {svc}})
	if resp, err := call(t, l.Port, "multi"); err != nil || resp.Content != "re:multi" {
		t.Errorf("call after ServeMulti = %v %v", resp, err)
	}
}

func TestStartWithCallback(t *testing.T) {
	lc := startLifecycle(t)
	l := lc.newListener(t, "lifecycle-1")
	var calls int
	var listenersAtCallback int
	GRPCManager.StartWithCallback(l, func() {
		calls++
		listenersAtCallback = len(lc.g.listenerList())
	})
	if calls != 1 {
		t.Errorf("the callback ran %d times, want 1", calls)
	}
	if listenersAtCallback != 1 {
		t.Errorf("the listener is added before the callback runs: %d listeners", listenersAtCallback)
	}
	if resp, err := call(t, l.Port, "cb"); err != nil || resp.Content != "re:cb" {
		t.Errorf("call after StartWithCallback = %v %v", resp, err)
	}
}

func TestConcurrentServeListeners(t *testing.T) {
	lc := startLifecycle(t)
	const workers = 6
	var ls []*listeners.Listener
	for i := 0; i < workers; i++ {
		ls = append(ls, lc.newListener(t, fmt.Sprintf("lifecycle-%d", i)))
	}
	var wg sync.WaitGroup
	for _, l := range ls {
		wg.Add(2)
		go func() {
			defer wg.Done()
			GRPCManager.ServeListener(l)
		}()
		go func() {
			defer wg.Done()
			// the server is read while the others change it
			lc.g.isRunning()
			lc.g.listenerList()
			GRPCManager.AddListener(l)
		}()
	}
	wg.Wait()
	if n := len(lc.g.listenerList()); n != workers {
		t.Errorf("the server has %d listeners, want %d", n, workers)
	}
	for _, l := range ls {
		if resp, err := call(t, l.Port, "c"); err != nil || resp.Content != "re:c" {
			t.Errorf("call on port %d = %v %v", l.Port, resp, err)
		}
	}
}

func TestConcurrentServiceChanges(t *testing.T) {
	lc := startLifecycle(t)
	l := lc.newListener(t, "lifecycle-1")
	GRPCManager.ServeListener(l)
	svc := sampleService(t)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i%2 == 0 {
				GRPCManager.StopService(svc)
			} else {
				GRPCManager.Serve(l.Port, svc)
			}
		}()
	}
	wg.Wait()
	// whatever the order was, the server is up, and a service that is served is served
	GRPCManager.Serve(l.Port, svc)
	if resp, err := call(t, l.Port, "after"); err != nil || resp.Content != "re:after" {
		t.Errorf("call after the changes = %v %v", resp, err)
	}
}

func newService(t *testing.T, sd *grpc.ServiceDesc) *gotogrpc.GRPCService {
	t.Helper()
	svc := ServiceRegistry.GetOrCreateService(cloneDesc(sd))
	if svc == nil {
		t.Fatalf("service %s can't be loaded", sd.ServiceName)
	}
	return svc
}

// unloadableDesc is a service desc that no descriptor can be found for.
func unloadableDesc() *grpc.ServiceDesc {
	return &grpc.ServiceDesc{ServiceName: "NoSuchService", HandlerType: (*any)(nil), Metadata: "no_such_file.proto"}
}

func TestInterceptServiceMethodErrors(t *testing.T) {
	stubGlobals(t)
	resetRegistry(t)
	sample := newService(t, &pb.SampleService_ServiceDesc)
	goto_ := newService(t, &pb.Goto_ServiceDesc)
	tests := []struct {
		name            string
		proxy, original *gotogrpc.GRPCService
		proxyName       string
		origName        string
		stream          bool
		want            string
	}{
		{"no proxy", nil, sample, "oneOne", "oneOne", false, "required"},
		{"no original", sample, nil, "oneOne", "oneOne", false, "required"},
		{"method that is not there", sample, sample, "nope", "nope", false, "Method [nope] not found on Service [SampleService]"},
		{"stream that is not there", sample, sample, "nope", "nope", true, "Method [nope] not found on Service [SampleService]"},
		{"stream asked as a method", sample, sample, "oneMany", "oneMany", false, "Method [oneMany] not found"},
		{"method asked as a stream", sample, sample, "oneOne", "oneOne", true, "Stream [oneOne] not found"},
		{"method that the proxy doesn't have", goto_, sample, "oneOne", "oneOne", false, "Method [oneOne] not found on Service [Goto]"},
		{"stream that the proxy doesn't have", goto_, sample, "oneMany", "oneMany", true, "Method [oneMany] not found on Service [Goto]"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := GRPCManager.InterceptServiceMethod(tc.proxy, tc.original, tc.proxyName, tc.origName, nil, tc.stream, true)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want one with %q", err, tc.want)
			}
		})
	}
	if err := GRPCManager.InterceptServiceMethods(nil, nil, nil, true); err == nil {
		t.Errorf("InterceptServiceMethods without services must fail")
	}
}

func TestInterceptServiceMethodsMarksTheMethods(t *testing.T) {
	stubGlobals(t)
	resetRegistry(t)
	svc := newService(t, &pb.SampleService_ServiceDesc)
	for name, m := range svc.Methods {
		if m.Intercepted {
			t.Fatalf("method %s is intercepted before it is asked to be", name)
		}
	}
	if err := GRPCManager.InterceptServiceMethods(svc, svc, &sampleImpl{}, true); err != nil {
		t.Fatalf("InterceptServiceMethods: %v", err)
	}
	for name, m := range svc.Methods {
		if !m.Intercepted || m.Interceptor == nil {
			t.Errorf("method %s is not intercepted", name)
		}
		if _, isUnary := m.Interceptor.(*GRPCMethodInterceptor); isUnary != m.IsUnary {
			t.Errorf("method %s has the interceptor %T", name, m.Interceptor)
		}
	}
	// intercepting what is intercepted keeps its interceptor
	first := svc.Methods["oneOne"].Interceptor
	if err := GRPCManager.InterceptServiceMethods(svc, svc, &sampleImpl{}, true); err != nil {
		t.Fatalf("InterceptServiceMethods again: %v", err)
	}
	if svc.Methods["oneOne"].Interceptor != first {
		t.Errorf("the interceptor of a method that is intercepted must be kept")
	}
}

func TestInterceptAndServe(t *testing.T) {
	stubGlobals(t)
	resetRegistry(t)
	impl := &sampleImpl{}
	GRPCManager.InterceptAndServe(cloneDesc(&pb.SampleService_ServiceDesc), impl)
	svc := ServiceRegistry.GetActiveService("SampleService")
	if svc == nil || svc.Server != impl {
		t.Fatalf("active service = %+v, want the service with the implementation given", svc)
	}
	ts := startServer(t, func(s *grpc.Server, _ *testServer) { s.RegisterService(svc.GSD, svc.Server) })
	if resp, _, err := ts.oneOne(t, "served"); err != nil || resp.Content != "re:served" {
		t.Errorf("OneOne = %v %v", resp, err)
	}
	if impl.calls.Load() != 1 {
		t.Errorf("the implementation got %d calls, want 1", impl.calls.Load())
	}
	if got, err := ts.oneMany(t, "m", 2); err != nil || len(got) != 2 {
		t.Errorf("OneMany = %v %v", got, err)
	}
}

func TestInterceptWithMiddlewareServesWithTheLatestServer(t *testing.T) {
	stubGlobals(t)
	resetRegistry(t)
	sd := cloneDesc(&pb.SampleService_ServiceDesc)
	first, second := &sampleImpl{}, &sampleImpl{}
	GRPCManager.InterceptWithMiddleware(sd, first)
	svc := ServiceRegistry.GetActiveService("SampleService")
	if svc == nil || svc.Server != first {
		t.Fatalf("service = %+v", svc)
	}
	// the server is registered again, as it is each time that the server of Goto starts
	GRPCManager.InterceptWithMiddleware(sd, second)
	if svc.Server != second {
		t.Fatalf("the service must have the latest implementation")
	}
	ts := startServer(t, func(s *grpc.Server, _ *testServer) { s.RegisterService(svc.GSD, svc.Server) })
	if resp, _, err := ts.oneOne(t, "x"); err != nil || resp.Content != "re:x" {
		t.Fatalf("OneOne = %v %v", resp, err)
	}
	if first.calls.Load() != 0 || second.calls.Load() != 1 {
		t.Errorf("calls to the first %d and to the second %d, want 0 and 1", first.calls.Load(), second.calls.Load())
	}
	if _, err := ts.oneMany(t, "x", 1); err != nil || first.calls.Load() != 0 || second.calls.Load() != 2 {
		t.Errorf("a stream: err %v, calls to the first %d and to the second %d, want 0 and 2", err, first.calls.Load(), second.calls.Load())
	}
}

func TestInterceptOfServicesThatCantBeLoaded(t *testing.T) {
	stubGlobals(t)
	resetRegistry(t)
	GRPCManager.InterceptAndServe(unloadableDesc(), &sampleImpl{})
	GRPCManager.InterceptWithMiddleware(unloadableDesc(), &sampleImpl{})
	if ServiceRegistry.GetService("NoSuchService") != nil || ServiceRegistry.GetActiveService("NoSuchService") != nil {
		t.Errorf("a service that can't be loaded must not be registered")
	}
	from, to := GRPCManager.InterceptAndProxy(unloadableDesc(), &pb.Goto2_ServiceDesc, "Goto2", nil, 0)
	if from != nil || to != nil {
		t.Errorf("a proxy of a service that can't be loaded = %v %v, want none", from, to)
	}
	from, to = GRPCManager.InterceptAndProxy(cloneDesc(&pb.Goto_ServiceDesc), unloadableDesc(), "NoSuchService", nil, 0)
	if from != nil || to != nil {
		t.Errorf("a proxy to a service that can't be loaded = %v %v, want none", from, to)
	}
}

func TestInterceptAndProxy(t *testing.T) {
	stubGlobals(t)
	resetRegistry(t)
	oldServices := SIP.services
	SIP.services = map[string]grpc.ServiceInfo{}
	t.Cleanup(func() { SIP.services = oldServices })
	from, to := GRPCManager.InterceptAndProxy(cloneDesc(&pb.Goto_ServiceDesc), cloneDesc(&pb.Goto2_ServiceDesc), "Goto2", nil, 7777)
	fromService, ok1 := from.(*gotogrpc.GRPCService)
	toService, ok2 := to.(*gotogrpc.GRPCService)
	if !ok1 || !ok2 || fromService.Name != "Goto" || toService.Name != "Goto2" {
		t.Fatalf("InterceptAndProxy = %v %v, want the services Goto and Goto2", from, to)
	}
	if f, tt, tee := ServiceRegistry.GetProxyService("Goto"); f != fromService || tt != toService || tee != 7777 {
		t.Errorf("proxy service = %v %v %d", f, tt, tee)
	}
	if f, tt := ServiceRegistry.GetTeeServices(7777); f != fromService || tt != toService {
		t.Errorf("tee services = %v %v", f, tt)
	}
	if _, isStub := fromService.Server.(*gotogrpc.DynamicServiceStub); !isStub {
		t.Errorf("a service that is proxied has no implementation of its own: %T", fromService.Server)
	}
	if info := SIP.GetService("Goto"); len(info.Methods) != 4 {
		t.Errorf("service info of the proxied service = %+v, want its 4 methods", info)
	}
	for name, m := range toService.Methods {
		if !m.Intercepted {
			t.Errorf("method %s of the target is not intercepted", name)
		}
	}
	// the same again finds the services that exist
	from2, to2 := GRPCManager.InterceptAndProxy(cloneDesc(&pb.Goto_ServiceDesc), cloneDesc(&pb.Goto2_ServiceDesc), "Goto2", nil, 0)
	if from2 != from || to2 != to {
		t.Errorf("a second InterceptAndProxy must find the services of the first")
	}
}

func TestInterceptAndProxyToATargetByName(t *testing.T) {
	stubGlobals(t)
	resetRegistry(t)
	from, to := GRPCManager.InterceptAndProxy(cloneDesc(&pb.Goto_ServiceDesc), nil, "Goto3", nil, 0)
	toService, ok := to.(*gotogrpc.GRPCService)
	if from == nil || !ok || toService.Name != "Goto3" {
		t.Errorf("InterceptAndProxy to a target by name = %v %v, want the target Goto3", from, to)
	}
}

func TestInterceptAndProxyOfServicesThatDontMatch(t *testing.T) {
	stubGlobals(t)
	resetRegistry(t)
	from, to := GRPCManager.InterceptAndProxy(cloneDesc(&pb.Goto_ServiceDesc), cloneDesc(&pb.SampleService_ServiceDesc), "SampleService", nil, 0)
	if from != nil || to != nil {
		t.Errorf("a proxy between services that don't have the same methods = %v %v, want none", from, to)
	}
	if f, _, _ := ServiceRegistry.GetProxyService("Goto"); f != nil {
		t.Errorf("a proxy that failed must not be registered")
	}
}

func TestReflectAndServiceInfo(t *testing.T) {
	stubGlobals(t)
	resetRegistry(t)
	oldServices := SIP.services
	SIP.services = map[string]grpc.ServiceInfo{}
	t.Cleanup(func() { SIP.services = oldServices })
	if info := SIP.GetService("Goto"); info.Methods != nil {
		t.Errorf("service info before reflection = %+v", info)
	}
	GRPCManager.Reflect(&pb.Goto_ServiceDesc)
	info := SIP.GetService("Goto")
	if len(info.Methods) != 4 {
		t.Fatalf("service info of Goto = %+v, want its 4 methods", info)
	}
	streams := map[string][2]bool{}
	for _, m := range info.Methods {
		streams[m.Name] = [2]bool{m.IsClientStream, m.IsServerStream}
	}
	want := map[string][2]bool{"echo": {false, false}, "streamIn": {true, false}, "streamOut": {false, true}, "streamInOut": {true, true}}
	for name, kinds := range want {
		if streams[name] != kinds {
			t.Errorf("method %s streams = %v, want %v", name, streams[name], kinds)
		}
	}
	if _, found := SIP.GetServiceInfo()["Goto"]; !found {
		t.Errorf("GetServiceInfo = %v, want Goto", SIP.GetServiceInfo())
	}
	// a service that can't be loaded is left out
	GRPCManager.Reflect(unloadableDesc())
	if _, found := SIP.GetServiceInfo()["NoSuchService"]; found {
		t.Errorf("a service that can't be loaded must not be in the service info")
	}
}
