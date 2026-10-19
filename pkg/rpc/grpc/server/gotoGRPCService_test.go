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
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"goto/pkg/constants"
	"goto/pkg/global"
	gotogrpc "goto/pkg/rpc/grpc"
	"goto/pkg/rpc/grpc/pb"
	"goto/pkg/server/response/payload"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// gotoEnv is a server of the Goto service of the pb package, which is registered the way that the server of Goto does
// it, with the methods intercepted.
type gotoEnv struct {
	*testServer
	client pb.GotoClient
}

// startGotoServices registers the Goto services with the manager on copies of the descs, so that the descs of the pb
// package are left as they are, and serves them.
func startGotoServices(t *testing.T, descs ...*grpc.ServiceDesc) *testServer {
	t.Helper()
	stubGlobals(t)
	resetRegistry(t)
	impls := map[string]any{"Goto": &GotoGRPCService{}, "Goto2": &Goto2GRPCService{}, "Goto3": &Goto3GRPCService{}}
	for _, sd := range descs {
		GRPCManager.InterceptWithMiddleware(cloneDesc(sd), impls[sd.ServiceName])
	}
	ts := startServer(t, func(s *grpc.Server, _ *testServer) {
		for _, sd := range descs {
			svc := gotogrpc.ServiceRegistry.GetService(sd.ServiceName)
			if svc == nil {
				t.Fatalf("service %s was not registered", sd.ServiceName)
			}
			s.RegisterService(svc.GSD, svc.Server)
		}
	})
	t.Cleanup(func() { payload.PayloadManager.ClearRPCResponsePayloads(ts.port) })
	return ts
}

func startGotoServer(t *testing.T) *gotoEnv {
	t.Helper()
	ts := startGotoServices(t, &pb.Goto_ServiceDesc)
	return &gotoEnv{testServer: ts, client: pb.NewGotoClient(ts.conn)}
}

type logFlags struct{ log, body, miniBody, respBody, respMiniBody, headers bool }

// setFlags changes the flags that decide what is logged, and puts them back with the test.
func setFlags(t *testing.T, apply func(f *logFlags)) {
	t.Helper()
	old := logFlags{
		global.Flags.EnableServerLogs, global.Flags.LogRequestBody, global.Flags.LogRequestMiniBody,
		global.Flags.LogResponseBody, global.Flags.LogResponseMiniBody, global.Flags.LogRequestHeaders,
	}
	t.Cleanup(func() {
		global.Flags.EnableServerLogs, global.Flags.LogRequestBody, global.Flags.LogRequestMiniBody = old.log, old.body, old.miniBody
		global.Flags.LogResponseBody, global.Flags.LogResponseMiniBody, global.Flags.LogRequestHeaders = old.respBody, old.respMiniBody, old.headers
	})
	n := old
	apply(&n)
	global.Flags.EnableServerLogs, global.Flags.LogRequestBody, global.Flags.LogRequestMiniBody = n.log, n.body, n.miniBody
	global.Flags.LogResponseBody, global.Flags.LogResponseMiniBody, global.Flags.LogRequestHeaders = n.respBody, n.respMiniBody, n.headers
}

func setRPCPayload(t *testing.T, port int, uri, text string) {
	t.Helper()
	if err := payload.PayloadManager.SetRPCResponsePayload(port, false, []byte(text), "text/plain", uri, "", "", "", "", 0, 0, 0); err != nil {
		t.Fatalf("set payload for %q: %v", uri, err)
	}
}

func (g *gotoEnv) echo(t *testing.T, text string, md ...string) (*pb.Output, metadata.MD, error) {
	t.Helper()
	var header metadata.MD
	out, err := g.client.Echo(rpcCtx(t, md...), &pb.Input{Text: text}, grpc.Header(&header))
	return out, header, err
}

func (g *gotoEnv) streamIn(t *testing.T, texts []string, md ...string) (*pb.Output, error) {
	t.Helper()
	stream, err := g.client.StreamIn(rpcCtx(t, md...))
	if err != nil {
		return nil, err
	}
	for _, text := range texts {
		if err := stream.Send(&pb.Input{Text: text}); err != nil {
			break
		}
	}
	return stream.CloseAndRecv()
}

func recvAll(recv func() (*pb.Output, error)) ([]*pb.Output, error) {
	var outs []*pb.Output
	for {
		out, err := recv()
		if errors.Is(err, io.EOF) {
			return outs, nil
		}
		if err != nil {
			return outs, err
		}
		outs = append(outs, out)
	}
}

func (g *gotoEnv) streamOut(t *testing.T, cfg *pb.StreamConfig, md ...string) ([]*pb.Output, error) {
	t.Helper()
	stream, err := g.client.StreamOut(rpcCtx(t, md...), cfg)
	if err != nil {
		return nil, err
	}
	return recvAll(stream.Recv)
}

func (g *gotoEnv) streamInOut(t *testing.T, cfgs []*pb.StreamConfig, md ...string) ([]*pb.Output, error) {
	t.Helper()
	stream, err := g.client.StreamInOut(rpcCtx(t, md...))
	if err != nil {
		return nil, err
	}
	for _, cfg := range cfgs {
		if err := stream.Send(cfg); err != nil {
			return nil, err
		}
	}
	// the answer to a config is sent when the next one, or the end of the stream, comes: so the stream must be closed
	// before the answers are read
	if err := stream.CloseSend(); err != nil {
		return nil, err
	}
	return recvAll(stream.Recv)
}

func texts(outs []*pb.Output) []string {
	var s []string
	for _, o := range outs {
		s = append(s, o.Text)
	}
	return s
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestEcho(t *testing.T) {
	g := startGotoServer(t)
	out, header, err := g.echo(t, "hello", "x-in", "in-value")
	if err != nil {
		t.Fatalf("Echo: %v", err)
	}
	if out.Text != "hello" {
		t.Errorf("Text = %q, want hello", out.Text)
	}
	if out.GotoHost != global.Self.HostLabel {
		t.Errorf("GotoHost = %q, want %q", out.GotoHost, global.Self.HostLabel)
	}
	if out.GotoPort != int32(g.port) {
		t.Errorf("GotoPort = %d, want %d", out.GotoPort, g.port)
	}
	if out.ViaGoto != "listener-"+strconv.Itoa(g.port) {
		t.Errorf("ViaGoto = %q", out.ViaGoto)
	}
	if out.GotoTLS != "false" {
		t.Errorf("GotoTLS = %q, want false", out.GotoTLS)
	}
	if out.RequestURI != "/Goto/echo" {
		t.Errorf("RequestURI = %q, want /Goto/echo", out.RequestURI)
	}
	if _, err := time.Parse(time.RFC3339Nano, out.At); err != nil {
		t.Errorf("At = %q is not a time: %v", out.At, err)
	}
	if n, err := strconv.Atoi(out.Id); err != nil || n <= 0 {
		t.Errorf("Id = %q, want a positive number", out.Id)
	}
	if out.RequestHeaders["x-in"] != "in-value" {
		t.Errorf("RequestHeaders = %v, want x-in", out.RequestHeaders)
	}
	if out.ResponseHeaders["Request-x-in"] != "in-value" || out.ResponseHeaders[constants.HeaderGotoPort] != strconv.Itoa(g.port) {
		t.Errorf("ResponseHeaders = %v", out.ResponseHeaders)
	}
	if got := header.Get(strings.ToLower(constants.HeaderGotoProtocol)); len(got) != 1 || got[0] != "gRPC" {
		t.Errorf("the protocol header sent = %v", header)
	}
	if got := header.Get(strings.ToLower(constants.HeaderGotoRPCMethod)); len(got) != 1 || got[0] != "/Goto/echo" {
		t.Errorf("the method header sent = %v", header)
	}
}

func TestEchoIdsGrowWithEachResponse(t *testing.T) {
	g := startGotoServer(t)
	var last uint64
	for i := 0; i < 5; i++ {
		out, _, err := g.echo(t, "x")
		if err != nil {
			t.Fatal(err)
		}
		id, err := strconv.ParseUint(out.Id, 10, 64)
		if err != nil || id <= last {
			t.Fatalf("call %d: id %q after %d, ids must grow", i, out.Id, last)
		}
		last = id
	}
}

func TestEchoEmptyAndLargeText(t *testing.T) {
	g := startGotoServer(t)
	if out, _, err := g.echo(t, ""); err != nil || out.Text != "" {
		t.Errorf("empty text: %v %v", out, err)
	}
	large := strings.Repeat("0123456789", 50000)
	if out, _, err := g.echo(t, large); err != nil || out.Text != large {
		t.Errorf("large text echoed back wrong: %v", err)
	}
}

func TestEchoWithConfiguredPayload(t *testing.T) {
	g := startGotoServer(t)
	other := startGotoServer(t)
	setRPCPayload(t, g.port, "/Goto/echo", "configured")
	if out, _, err := g.echo(t, "hello"); err != nil || out.Text != "configured" {
		t.Errorf("with a payload for the method = %v %v, want configured", out, err)
	}
	if out, _, err := other.echo(t, "hello"); err != nil || out.Text != "hello" {
		t.Errorf("a payload of a port must not apply on another port: %v %v", out, err)
	}
	payload.PayloadManager.ClearRPCResponsePayloads(g.port)
	if out, _, err := g.echo(t, "hello"); err != nil || out.Text != "hello" {
		t.Errorf("after the payloads are cleared: %v %v", out, err)
	}
}

func TestEchoWithLogFlags(t *testing.T) {
	g := startGotoServer(t)
	setFlags(t, func(f *logFlags) {
		f.log, f.body, f.respBody, f.headers = true, true, true, true
	})
	if out, _, err := g.echo(t, "with body logs"); err != nil || out.Text != "with body logs" {
		t.Errorf("Echo with the body logged: %v %v", out, err)
	}
	setFlags(t, func(f *logFlags) {
		f.log, f.body, f.miniBody, f.respMiniBody = true, false, true, true
	})
	long := strings.Repeat("m", 120)
	if out, _, err := g.echo(t, long); err != nil || out.Text != long {
		t.Errorf("Echo with the mini body logged, for a long text: %v %v", out, err)
	}
}

func TestEchoThroughForcedStatus(t *testing.T) {
	g := startGotoServer(t)
	force(t, g.port, "/Goto/echo", "", "", "", []int{418}, 1)
	if _, _, err := g.echo(t, "x"); !isForced(err, 418) {
		t.Fatalf("Echo = %v, want the forced status 418", err)
	}
	if out, _, err := g.echo(t, "x"); err != nil || out.Text != "x" {
		t.Errorf("after the status is used up: %v %v", out, err)
	}
}

func TestStreamIn(t *testing.T) {
	g := startGotoServer(t)
	out, err := g.streamIn(t, []string{"a", "b", "c"}, "x-in", "v")
	if err != nil {
		t.Fatalf("StreamIn: %v", err)
	}
	if out.Text != "[a][b][c]" {
		t.Errorf("Text = %q, want [a][b][c]", out.Text)
	}
	if out.GotoPort != int32(g.port) || out.RequestURI != "/Goto/streamIn" || out.ViaGoto != "listener-"+strconv.Itoa(g.port) {
		t.Errorf("output = %+v", out)
	}
	if out.RequestHeaders["x-in"] != "v" {
		t.Errorf("RequestHeaders = %v", out.RequestHeaders)
	}
	if out, err := g.streamIn(t, nil); err != nil || out.Text != "" {
		t.Errorf("a stream without messages = %v %v, want an empty text", out, err)
	}
	if out, err := g.streamIn(t, []string{"only"}); err != nil || out.Text != "[only]" {
		t.Errorf("a stream of one message = %v %v", out, err)
	}
}

func TestStreamInWithConfiguredPayload(t *testing.T) {
	g := startGotoServer(t)
	setRPCPayload(t, g.port, "/Goto/streamIn", "cfg")
	out, err := g.streamIn(t, []string{"a", "b"})
	if err != nil || out.Text != "[cfg][cfg]" {
		t.Errorf("StreamIn = %v %v, want [cfg][cfg]", out, err)
	}
}

func TestStreamInSendsResponseHeaders(t *testing.T) {
	g := startGotoServer(t)
	stream, err := g.client.StreamIn(rpcCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	stream.Send(&pb.Input{Text: "a"})
	if _, err := stream.CloseAndRecv(); err != nil {
		t.Fatal(err)
	}
	header, err := stream.Header()
	if err != nil {
		t.Fatal(err)
	}
	if got := header.Get(strings.ToLower(constants.HeaderGotoPort)); len(got) != 1 || got[0] != strconv.Itoa(g.port) {
		t.Errorf("headers = %v", header)
	}
}

func TestStreamOut(t *testing.T) {
	g := startGotoServer(t)
	outs, err := g.streamOut(t, &pb.StreamConfig{ChunkCount: 3, Text: "x", Interval: "1ms"})
	if err != nil {
		t.Fatalf("StreamOut: %v", err)
	}
	if want := []string{"x(1/3)", "x(2/3)", "x(3/3)"}; !equalStrings(texts(outs), want) {
		t.Fatalf("texts = %v, want %v", texts(outs), want)
	}
	for _, o := range outs {
		if o.GotoPort != int32(g.port) || o.RequestURI != "/Goto/streamOut" {
			t.Errorf("output = %+v", o)
		}
	}
	if outs[0].Id == outs[1].Id || outs[1].Id == outs[2].Id {
		t.Errorf("each response needs an id of its own: %s %s %s", outs[0].Id, outs[1].Id, outs[2].Id)
	}
}

func TestStreamOutRandomPayload(t *testing.T) {
	g := startGotoServer(t)
	outs, err := g.streamOut(t, &pb.StreamConfig{ChunkSize: 16, ChunkCount: 2, Interval: "1ms"})
	if err != nil || len(outs) != 2 {
		t.Fatalf("StreamOut = %v %v", outs, err)
	}
	for i, o := range outs {
		suffix := "(" + strconv.Itoa(i+1) + "/2)"
		if !strings.HasSuffix(o.Text, suffix) || len(o.Text) != 16+len(suffix) {
			t.Errorf("response %d = %q, want 16 characters and %s", i, o.Text, suffix)
		}
	}
}

func TestStreamOutNoChunks(t *testing.T) {
	g := startGotoServer(t)
	outs, err := g.streamOut(t, &pb.StreamConfig{ChunkCount: 0, Text: "x"})
	if err != nil || len(outs) != 0 {
		t.Errorf("StreamOut of no chunks = %v %v, want no responses", texts(outs), err)
	}
}

func TestStreamOutInvalidIntervalFallsBackToDefault(t *testing.T) {
	g := startGotoServer(t)
	start := time.Now()
	outs, err := g.streamOut(t, &pb.StreamConfig{ChunkCount: 2, Text: "x", Interval: "not-a-duration"})
	if err != nil || len(outs) != 2 {
		t.Fatalf("StreamOut = %v %v", texts(outs), err)
	}
	if elapsed := time.Since(start); elapsed < 90*time.Millisecond {
		t.Errorf("an invalid interval means the default of 100ms between responses, took %s", elapsed)
	}
}

func TestStreamOutWithConfiguredPayload(t *testing.T) {
	g := startGotoServer(t)
	setRPCPayload(t, g.port, "/Goto/streamOut", "cfg")
	outs, err := g.streamOut(t, &pb.StreamConfig{ChunkCount: 2, Text: "ignored", Interval: "1ms"})
	if err != nil || !equalStrings(texts(outs), []string{"cfg(1/2)", "cfg(2/2)"}) {
		t.Errorf("StreamOut = %v %v", texts(outs), err)
	}
}

func TestStreamInOut(t *testing.T) {
	g := startGotoServer(t)
	outs, err := g.streamInOut(t, []*pb.StreamConfig{
		{ChunkCount: 1, Text: "a", Interval: "1ms"},
		{ChunkCount: 1, Text: "b", Interval: "1ms"},
	})
	if err != nil {
		t.Fatalf("StreamInOut: %v", err)
	}
	if want := []string{"a(1/1)", "a | b(FINAL)(1/1)"}; !equalStrings(texts(outs), want) {
		t.Errorf("texts = %v, want %v", texts(outs), want)
	}
	for _, o := range outs {
		if o.GotoPort != int32(g.port) || o.RequestURI != "/Goto/streamInOut" {
			t.Errorf("output = %+v", o)
		}
	}
}

func TestStreamInOutShapes(t *testing.T) {
	g := startGotoServer(t)
	cfg := func(count int32, text string) *pb.StreamConfig {
		return &pb.StreamConfig{ChunkCount: count, Text: text, Interval: "1ms"}
	}
	tests := []struct {
		name string
		in   []*pb.StreamConfig
		want []string
	}{
		{"no messages", nil, nil},
		{"one message is the final one", []*pb.StreamConfig{cfg(1, "a")}, []string{"a(FINAL)(1/1)"}},
		{"chunks of the last message", []*pb.StreamConfig{cfg(1, "a"), cfg(2, "b")}, []string{"a(1/1)", "a | b(FINAL)(1/2)", "a | b(FINAL)(2/2)"}},
		{"three messages", []*pb.StreamConfig{cfg(1, "a"), cfg(1, "b"), cfg(1, "c")},
			[]string{"a(1/1)", "a | b(1/1)", "a | b | c(FINAL)(1/1)"}},
		{"a message without chunks sends nothing", []*pb.StreamConfig{cfg(0, "a"), cfg(1, "b")}, []string{"a | b(FINAL)(1/1)"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			outs, err := g.streamInOut(t, tc.in)
			if err != nil {
				t.Fatalf("StreamInOut: %v", err)
			}
			if !equalStrings(texts(outs), tc.want) {
				t.Errorf("texts = %q, want %q", texts(outs), tc.want)
			}
		})
	}
}

func TestStreamInOutWithConfiguredPayload(t *testing.T) {
	g := startGotoServer(t)
	setRPCPayload(t, g.port, "/Goto/streamInOut", "cfg")
	outs, err := g.streamInOut(t, []*pb.StreamConfig{
		{ChunkCount: 1, Text: "a", Interval: "1ms"},
		{ChunkCount: 1, Text: "b", Interval: "1ms"},
	})
	if err != nil || !equalStrings(texts(outs), []string{"cfg(1/1)", "cfg(1/1)"}) {
		t.Errorf("StreamInOut = %v %v", texts(outs), err)
	}
}

func TestGotoStreamsThroughForcedStatus(t *testing.T) {
	g := startGotoServer(t)
	force(t, g.port, "", "/Goto/", "", "", []int{503}, 3)
	if _, err := g.streamIn(t, []string{"a"}); !isForced(err, 503) {
		t.Errorf("StreamIn = %v, want the forced status", err)
	}
	if _, err := g.streamOut(t, &pb.StreamConfig{ChunkCount: 1, Text: "x"}); !isForced(err, 503) {
		t.Errorf("StreamOut = %v, want the forced status", err)
	}
	if _, err := g.streamInOut(t, []*pb.StreamConfig{{ChunkCount: 1, Text: "x"}}); !isForced(err, 503) {
		t.Errorf("StreamInOut = %v, want the forced status", err)
	}
	if out, err := g.streamIn(t, []string{"a"}); err != nil || out.Text != "[a]" {
		t.Errorf("after the status is used up: %v %v", out, err)
	}
}

func TestGotoConcurrentCalls(t *testing.T) {
	g := startGotoServer(t)
	const workers = 20
	var wg sync.WaitGroup
	errs := make(chan error, workers*3)
	ids := make(chan string, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			text := "w" + strconv.Itoa(i)
			out, err := g.client.Echo(ctx, &pb.Input{Text: text})
			if err != nil {
				errs <- err
				return
			}
			if out.Text != text {
				errs <- errors.New("echo of " + text + " returned " + out.Text)
			}
			ids <- out.Id
			stream, err := g.client.StreamOut(ctx, &pb.StreamConfig{ChunkCount: 2, Text: text, Interval: "1ms"})
			if err != nil {
				errs <- err
				return
			}
			got, err := recvAll(stream.Recv)
			if err != nil || !equalStrings(texts(got), []string{text + "(1/2)", text + "(2/2)"}) {
				errs <- errors.New("stream out of " + text + " returned " + strings.Join(texts(got), ","))
			}
		}()
	}
	wg.Wait()
	close(errs)
	close(ids)
	for err := range errs {
		t.Error(err)
	}
	seen := map[string]bool{}
	for id := range ids {
		if seen[id] {
			t.Errorf("id %s was given twice", id)
		}
		seen[id] = true
	}
	if len(seen) != workers {
		t.Errorf("got %d distinct ids, want %d", len(seen), workers)
	}
}

func TestGoto2And3AreNotImplemented(t *testing.T) {
	ts := startGotoServices(t, &pb.Goto2_ServiceDesc, &pb.Goto3_ServiceDesc)
	if _, err := pb.NewGoto2Client(ts.conn).Echo(rpcCtx(t), &pb.Input{Text: "x"}); status.Code(err) != codes.Unimplemented {
		t.Errorf("Goto2.Echo = %v, want Unimplemented", err)
	}
	if _, err := pb.NewGoto3Client(ts.conn).Echo(rpcCtx(t), &pb.Input{Text: "x"}); status.Code(err) != codes.Unimplemented {
		t.Errorf("Goto3.Echo = %v, want Unimplemented", err)
	}
	stream, err := pb.NewGoto3Client(ts.conn).StreamOut(rpcCtx(t), &pb.StreamConfig{ChunkCount: 1})
	if err == nil {
		_, err = stream.Recv()
	}
	if status.Code(err) != codes.Unimplemented {
		t.Errorf("Goto3.StreamOut = %v, want Unimplemented", err)
	}
}

// fakeManager notes what the registration functions ask the manager for.
type fakeManager struct {
	lock  sync.Mutex
	calls []string
}

func (f *fakeManager) note(call string) {
	f.lock.Lock()
	defer f.lock.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeManager) Reflect(s *grpc.ServiceDesc)                  { f.note("reflect:" + s.ServiceName) }
func (f *fakeManager) InterceptAndServe(s *grpc.ServiceDesc, _ any) { f.note("serve:" + s.ServiceName) }
func (f *fakeManager) InterceptWithMiddleware(s *grpc.ServiceDesc, _ any) {
	f.note("middleware:" + s.ServiceName)
}
func (f *fakeManager) InterceptAndProxy(from, to *grpc.ServiceDesc, target string, _ any, _ int) (global.IGRPCService, global.IGRPCService) {
	f.note("proxy:" + from.ServiceName)
	return nil, nil
}

func (f *fakeManager) got() []string {
	f.lock.Lock()
	defer f.lock.Unlock()
	return append([]string(nil), f.calls...)
}

// setRunning changes the flags that tell if the services of Goto are registered, and puts them back with the test.
func setRunning(t *testing.T, goto_, extAuthz bool) {
	t.Helper()
	servicesLock.Lock()
	oldGoto, oldExt := IsGotoServiceRunning, IsExtAuthzServiceRunning
	IsGotoServiceRunning, IsExtAuthzServiceRunning = goto_, extAuthz
	servicesLock.Unlock()
	t.Cleanup(func() {
		servicesLock.Lock()
		defer servicesLock.Unlock()
		IsGotoServiceRunning, IsExtAuthzServiceRunning = oldGoto, oldExt
	})
}

func servicesRunning() (goto_, extAuthz bool) {
	servicesLock.Lock()
	defer servicesLock.Unlock()
	return IsGotoServiceRunning, IsExtAuthzServiceRunning
}

func TestRegisterGotoServer(t *testing.T) {
	setRunning(t, false, false)
	fm := &fakeManager{}
	RegisterGotoServer(fm)
	if want := []string{"middleware:Goto", "middleware:Goto2", "middleware:Goto3"}; !equalStrings(fm.got(), want) {
		t.Errorf("registered %v, want %v", fm.got(), want)
	}
	if running, _ := servicesRunning(); !running {
		t.Errorf("the services are registered: running must be set")
	}
	// registered once while it runs
	RegisterGotoServer(fm)
	if len(fm.got()) != 3 {
		t.Errorf("registered again while running: %v", fm.got())
	}
	// and again once the server has stopped
	OnGRPCStop()
	if running, _ := servicesRunning(); running {
		t.Errorf("a stop of the server must reset the running flag")
	}
	RegisterGotoServer(fm)
	if len(fm.got()) != 6 {
		t.Errorf("after a stop the services are registered again: %v", fm.got())
	}
	OnGRPCStart()
}

func TestRegisterExtAuthzServer(t *testing.T) {
	setRunning(t, false, false)
	fm := &fakeManager{}
	RegisterExtAuthzServer(fm)
	if want := []string{"middleware:envoy.service.auth.v3.Authorization"}; !equalStrings(fm.got(), want) {
		t.Errorf("registered %v, want %v", fm.got(), want)
	}
	if _, running := servicesRunning(); !running {
		t.Errorf("running must be set")
	}
	RegisterExtAuthzServer(fm)
	if len(fm.got()) != 1 {
		t.Errorf("registered again while running: %v", fm.got())
	}
	OnExtAuthzStop()
	if _, running := servicesRunning(); running {
		t.Errorf("a stop of the server must reset the running flag")
	}
	OnExtAuthzStart()
}

// dynamicMessage builds a message that has just the fields given of the generic requests that the transformers read.
func dynamicMessage(t *testing.T, name string, fields map[string]any) proto.Message {
	t.Helper()
	typ := protoreflect.StringKind
	intType := protoreflect.Int32Kind
	var descs []*descriptorpb.FieldDescriptorProto
	num := int32(1)
	for _, f := range []struct {
		name string
		kind protoreflect.Kind
	}{{"payload", typ}, {"chunkSize", intType}, {"chunkCount", intType}, {"interval", typ}} {
		if _, ok := fields[f.name]; !ok {
			continue
		}
		ft := descriptorpb.FieldDescriptorProto_TYPE_STRING
		if f.kind == protoreflect.Int32Kind {
			ft = descriptorpb.FieldDescriptorProto_TYPE_INT32
		}
		descs = append(descs, &descriptorpb.FieldDescriptorProto{
			Name: proto.String(f.name), Number: proto.Int32(num), Type: ft.Enum(),
			Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(), JsonName: proto.String(f.name),
		})
		num++
	}
	fd, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name: proto.String(name + ".proto"), Package: proto.String("transformtest"), Syntax: proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{{Name: proto.String("Msg"), Field: descs}},
	}, nil)
	if err != nil {
		t.Fatalf("descriptor: %v", err)
	}
	md := fd.Messages().Get(0)
	msg := dynamicpb.NewMessage(md)
	for k, v := range fields {
		switch val := v.(type) {
		case string:
			msg.Set(md.Fields().ByName(protoreflect.Name(k)), protoreflect.ValueOfString(val))
		case int32:
			msg.Set(md.Fields().ByName(protoreflect.Name(k)), protoreflect.ValueOfInt32(val))
		}
	}
	return msg
}

func TestTransformInput(t *testing.T) {
	if got := TransformInput(nil); got != nil {
		t.Errorf("TransformInput(nil) = %v, want nil", got)
	}
	msg := dynamicMessage(t, "transform_input", map[string]any{"payload": "hello"})
	got, ok := TransformInput(msg).(*pb.Input)
	if !ok || got.Text != "hello" {
		t.Errorf("TransformInput = %v, want an Input of text hello", got)
	}
}

func TestTransformStreamConfig(t *testing.T) {
	if got := TransformStreamConfig(nil); got != nil {
		t.Errorf("TransformStreamConfig(nil) = %v, want nil", got)
	}
	msg := dynamicMessage(t, "transform_config", map[string]any{
		"payload": "data", "chunkSize": int32(7), "chunkCount": int32(3), "interval": "5ms",
	})
	got, ok := TransformStreamConfig(msg).(*pb.StreamConfig)
	if !ok {
		t.Fatalf("TransformStreamConfig = %T, want a StreamConfig", got)
	}
	if got.Text != "data" || got.ChunkSize != 7 || got.ChunkCount != 3 || got.Interval != "5ms" {
		t.Errorf("TransformStreamConfig = %+v", got)
	}
}

func TestStartWatchersDoNothing(t *testing.T) {
	setRunning(t, true, true)
	OnGRPCStart()
	OnExtAuthzStart()
	if goto_, ext := servicesRunning(); !goto_ || !ext {
		t.Errorf("a start must not reset what is registered: goto %v extauthz %v", goto_, ext)
	}
}
