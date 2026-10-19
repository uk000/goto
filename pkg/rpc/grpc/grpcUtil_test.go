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

package grpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"

	"goto/pkg/global"
	"goto/pkg/util"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/dynamicpb"
)

// fakeServerStream is a grpc.ServerStream that is fed with the messages that a client would send, and records what the
// server sends.
type fakeServerStream struct {
	ctx     context.Context
	lock    sync.Mutex
	in      chan proto.Message
	sent    []proto.Message
	sendErr error
	failAt  int   // the send that fails with sendErr (1 based), 0 for none
	recvErr error // what RecvMsg gives instead of io.EOF once the queued messages are consumed
	headers metadata.MD
}

func newFakeServerStream(ctx context.Context) *fakeServerStream {
	return &fakeServerStream{ctx: ctx, in: make(chan proto.Message, 100)}
}

func (f *fakeServerStream) Context() context.Context { return f.ctx }

func (f *fakeServerStream) RecvMsg(m any) error {
	msg, ok := <-f.in
	if !ok {
		if f.recvErr != nil {
			return f.recvErr
		}
		return io.EOF
	}
	proto.Reset(m.(proto.Message))
	proto.Merge(m.(proto.Message), msg)
	return nil
}

func (f *fakeServerStream) SendMsg(m any) error {
	f.lock.Lock()
	defer f.lock.Unlock()
	if f.sendErr != nil && (f.failAt == 0 || len(f.sent)+1 == f.failAt) {
		return f.sendErr
	}
	f.sent = append(f.sent, m.(proto.Message))
	return nil
}

func (f *fakeServerStream) SetHeader(md metadata.MD) error {
	f.lock.Lock()
	defer f.lock.Unlock()
	f.headers = metadata.Join(f.headers, md)
	return nil
}
func (f *fakeServerStream) SendHeader(md metadata.MD) error { return f.SetHeader(md) }
func (f *fakeServerStream) SetTrailer(metadata.MD)          {}

func (f *fakeServerStream) sentTexts() []string {
	f.lock.Lock()
	defer f.lock.Unlock()
	var texts []string
	for _, m := range f.sent {
		texts = append(texts, textOf(m))
	}
	return texts
}

// feed queues messages for the server to receive, and closes the stream after them if asked.
func (f *fakeServerStream) feed(closeAfter bool, texts ...string) {
	for _, text := range texts {
		f.in <- echoMessage(text)
	}
	if closeAfter {
		close(f.in)
	}
}

func textOf(m proto.Message) string {
	r := m.ProtoReflect()
	f := r.Descriptor().Fields().ByName("text")
	if f == nil {
		return protojson.Format(m)
	}
	return r.Get(f).String()
}

func TestParseRequest(t *testing.T) {
	stubHandlers(t)
	_, psd := healthDesc()
	m := newRegistry().NewGRPCService(psd).Methods["Check"]
	msg := dynamicpb.NewMessage(m.InputType())
	if err := protojson.Unmarshal([]byte(`{"service":"abc"}`), msg); err != nil {
		t.Fatal(err)
	}
	b, err := ParseRequest(msg)
	if err != nil {
		t.Fatalf("ParseRequest: %v", err)
	}
	var got map[string]string
	if json.Unmarshal(b, &got) != nil || !reflect.DeepEqual(got, map[string]string{"service": "abc"}) {
		t.Errorf("request = %s, want {service: abc}", b)
	}
	if b, err := ParseRequest("not a message"); err == nil || b != nil {
		t.Errorf("anything but a dynamic message must be refused, got %s %v", b, err)
	}
}

func TestGetRequestHeaders(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-a", "1", "x-a", "2", "x-b", "3"))
	got, err := GetRequestHeaders(ctx)
	if err != nil || !reflect.DeepEqual(got, map[string][]string{"x-a": {"1", "2"}, "x-b": {"3"}}) {
		t.Errorf("headers = %v %v", got, err)
	}
	got["x-a"] = nil
	again, _ := GetRequestHeaders(ctx)
	if len(again["x-a"]) != 2 {
		t.Errorf("the result is a copy of the map, changing it must not change the metadata of the context")
	}
	if got, err := GetRequestHeaders(context.Background()); err == nil || got != nil {
		t.Errorf("a context without metadata must give an error, got %v %v", got, err)
	}
}

func TestGetRequestHost(t *testing.T) {
	tests := []struct {
		name string
		md   map[string][]string
		want string
	}{
		{"authority", map[string][]string{":authority": {"a:1"}}, "a:1"},
		{"authority wins", map[string][]string{":authority": {"a:1"}, "host": {"h"}}, "a:1"},
		{"host", map[string][]string{"host": {"h"}}, "h"},
		{"hostName", map[string][]string{"hostName": {"n"}}, "n"},
		{"none", map[string][]string{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetRequestHost(tt.md); got != tt.want {
				t.Errorf("host = %q, want %q", got, tt.want)
			}
		})
	}
	md := map[string][]string{":authority": {"a:1"}}
	GetRequestHost(md)
	if !reflect.DeepEqual(md["host"], []string{"a:1"}) {
		t.Errorf("the authority is also set as the host, got %v", md)
	}
}

func TestGetRequestRemoteAddr(t *testing.T) {
	if got := GetRequestRemoteAddr(map[string][]string{"remoteAddr": {"1.2.3.4:5"}}); got != "1.2.3.4:5" {
		t.Errorf("remote addr = %q", got)
	}
	if got := GetRequestRemoteAddr(map[string][]string{}); got != "" {
		t.Errorf("remote addr = %q, want none", got)
	}
}

func TestReadRequest(t *testing.T) {
	stubHandlers(t)
	_, psd := healthDesc()
	m := newRegistry().NewGRPCService(psd).Methods["Check"]
	req := ReadRequest(m, func(in any) error {
		return protojson.Unmarshal([]byte(`{"service":"read"}`), in.(proto.Message))
	})
	if req == nil || !strings.Contains(protojson.Format(req), "read") {
		t.Errorf("request = %v, want the decoded message", req)
	}
	if got := ReadRequest(m, func(any) error { return errors.New("bad") }); got != nil {
		t.Errorf("a message that can't be decoded must give nil, got %v", got)
	}
}

func TestSendStreamResponse(t *testing.T) {
	stubHandlers(t)
	responses := []proto.Message{echoMessage("r0"), echoMessage("r1"), echoMessage("r2")}
	tests := []struct {
		name     string
		from, to int
		want     []string
	}{
		{"all", 0, 3, []string{"r0", "r1", "r2"}},
		{"from the middle", 1, 3, []string{"r1", "r2"}},
		{"wraps around", 0, 5, []string{"r0", "r1", "r2", "r0", "r1"}},
		{"from beyond the end restarts", 10, 12, []string{"r0", "r1"}},
		{"nothing", 2, 2, nil},
		{"backwards", 3, 1, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ss := newFakeServerStream(context.Background())
			if err := SendStreamResponse(nil, ss, responses, tt.from, tt.to); err != nil {
				t.Fatalf("SendStreamResponse: %v", err)
			}
			if got := ss.sentTexts(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("sent %v, want %v", got, tt.want)
			}
		})
	}
	t.Run("send error", func(t *testing.T) {
		ss := newFakeServerStream(context.Background())
		ss.sendErr, ss.failAt = errors.New("broken"), 2
		if err := SendStreamResponse(nil, ss, responses, 0, 3); err == nil || err.Error() != "broken" {
			t.Errorf("error = %v, want the error of the stream", err)
		}
		if got := ss.sentTexts(); !reflect.DeepEqual(got, []string{"r0"}) {
			t.Errorf("sent %v, want only what was sent before the error", got)
		}
	})
}

func TestSendResponse(t *testing.T) {
	stubHandlers(t)
	_, psd := healthDesc()
	desc := newRegistry().NewGRPCService(psd).Methods["Check"].OutputType()
	ss := newFakeServerStream(context.Background())
	if err := SendResponse(desc, ss, []byte(`{"status":"SERVING"}`)); err != nil {
		t.Fatalf("SendResponse: %v", err)
	}
	if len(ss.sent) != 1 || !strings.Contains(protojson.Format(ss.sent[0]), "SERVING") {
		t.Errorf("sent %v, want the response message", ss.sent)
	}
	if err := SendResponse(desc, ss, []byte(`{"status":`)); err == nil || len(ss.sent) != 1 {
		t.Errorf("a response that can't be decoded must be refused without being sent, got %v and %d sent", err, len(ss.sent))
	}
	ss.sendErr = errors.New("broken")
	if err := SendResponse(desc, ss, []byte(`{"status":"SERVING"}`)); err == nil || err.Error() != "broken" {
		t.Errorf("error = %v, want the error of the stream", err)
	}
}

// commonHandlerContext builds the context of a request that arrived over a connection with the given addresses.
func commonHandlerContext(local, remote net.Addr, method string, md metadata.MD) context.Context {
	ctx := peer.NewContext(context.Background(), &peer.Peer{Addr: remote, LocalAddr: local})
	if method != "" {
		ctx = grpc.NewContextWithServerTransportStream(ctx, fakeTransportStream{method: method})
	}
	if md != nil {
		ctx = metadata.NewIncomingContext(ctx, md)
	}
	return ctx
}

func registerHealthInGlobalRegistry(t *testing.T) *GRPCService {
	t.Helper()
	stubHandlers(t)
	_, psd := healthDesc()
	s := ServiceRegistry.NewGRPCService(psd)
	t.Cleanup(func() { ServiceRegistry.RemoveService(healthService) })
	return s
}

func TestCommonHandler(t *testing.T) {
	service := registerHealthInGlobalRegistry(t)
	const method = "/grpc.health.v1.Health/Check"
	tcp := func(port int) *net.TCPAddr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port} }
	md := metadata.Pairs(":authority", "authority.example:8080", "x-a", "1")

	t.Run("tcp connection", func(t *testing.T) {
		ctx := commonHandlerContext(tcp(8080), tcp(5555), method, md)
		ctx2, m, port, remote, authority, headers, err := CommonHandler(ctx, nil)
		if err != nil {
			t.Fatalf("CommonHandler: %v", err)
		}
		if m != service.Methods["Check"] || port != 8080 || remote == nil || remote.Port != 5555 || authority != "authority.example:8080" {
			t.Errorf("method %v port %d remote %v authority %q", m, port, remote, authority)
		}
		if util.GetContextPort(ctx2) != 8080 || len(headers["x-a"]) != 1 {
			t.Errorf("port of the returned context %d, headers %v", util.GetContextPort(ctx2), headers)
		}
	})
	t.Run("context from the stream", func(t *testing.T) {
		ss := newFakeServerStream(commonHandlerContext(tcp(8081), tcp(1), method, md))
		_, m, port, _, _, _, err := CommonHandler(nil, ss)
		if err != nil || m == nil || port != 8081 {
			t.Errorf("method %v port %d err %v, want the context of the stream to be used", m, port, err)
		}
	})
	t.Run("port from the context when the address has none", func(t *testing.T) {
		ctx := util.WithPort(commonHandlerContext(tcp(0), tcp(1), method, md), 9191)
		_, _, port, _, _, _, err := CommonHandler(ctx, nil)
		if err != nil || port != 9191 {
			t.Errorf("port %d err %v, want 9191", port, err)
		}
	})
	t.Run("no peer", func(t *testing.T) {
		if _, _, _, _, _, _, err := CommonHandler(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "peer") {
			t.Errorf("err = %v, want one about the missing peer", err)
		}
	})
	t.Run("unknown method", func(t *testing.T) {
		ctx := commonHandlerContext(tcp(8080), tcp(1), "/grpc.health.v1.Health/Nope", md)
		if _, m, _, _, _, _, err := CommonHandler(ctx, nil); err == nil || m != nil {
			t.Errorf("method %v err %v, want an error", m, err)
		}
	})
	t.Run("no headers", func(t *testing.T) {
		ctx := commonHandlerContext(tcp(8080), tcp(1), method, nil)
		if _, _, _, _, _, _, err := CommonHandler(ctx, nil); err == nil {
			t.Errorf("a request without metadata must give an error")
		}
	})
	t.Run("no authority", func(t *testing.T) {
		ctx := commonHandlerContext(tcp(8080), tcp(1), method, metadata.Pairs("x-a", "1"))
		if _, _, _, _, authority, _, err := CommonHandler(ctx, nil); err != nil || authority != "" {
			t.Errorf("authority %q err %v, want none", authority, err)
		}
	})
	t.Run("remote address that isn't tcp", func(t *testing.T) {
		ctx := commonHandlerContext(tcp(8080), &net.UnixAddr{Name: "/tmp/x", Net: "unix"}, method, md)
		if _, _, _, remote, _, _, err := CommonHandler(ctx, nil); err != nil || remote != nil {
			t.Errorf("remote %v err %v, want no remote address and no error", remote, err)
		}
	})
}

func TestLogRequestAndResponse(t *testing.T) {
	stubOnce.Do(stubGlobalFuncs)
	flags := global.Flags
	t.Cleanup(func() { global.Flags = flags })
	logs := func(ctx context.Context) []string { return util.GetRequestStoreFromContextIfPresent(ctx).LogMessages }
	newCtx := func() context.Context {
		ctx, _ := util.WithRequestStoreForContext(peer.NewContext(context.Background(), &peer.Peer{Addr: &net.TCPAddr{Port: 5}}))
		return ctx
	}

	global.Flags.EnableServerLogs = false
	ctx := newCtx()
	LogRequest(ctx, 8080, "svc", "m", "host", map[string]string{"a": "b"}, 1, 2, "mini")
	LogResponse(ctx, nil, 0, 1, 2, "act")
	if got := logs(ctx); len(got) != 0 {
		t.Errorf("nothing must be logged with server logs disabled, got %v", got)
	}

	global.Flags.EnableServerLogs = true
	global.Flags.LogRequestHeaders, global.Flags.LogRequestMiniBody, global.Flags.LogResponseHeaders = false, false, false
	ctx = newCtx()
	LogRequest(ctx, 8080, "svc", "m", "host", map[string]string{"a": "b"}, 3, 20, "mini")
	LogResponse(ctx, map[string]string{"r": "h"}, 0, 1, 7, "act")
	got := logs(ctx)
	if len(got) != 2 || !strings.Contains(got[0], "Service [svc] Method [m]") || !strings.Contains(got[0], "Count [3], Content Length [20]") ||
		strings.Contains(got[0], "Request Headers") || strings.Contains(got[0], "Mini Body") {
		t.Errorf("request log = %v", got)
	}
	if !strings.Contains(got[1], "act --> Response Status [0], Response Body Length [7]") || strings.Contains(got[1], "Response Headers") {
		t.Errorf("response log = %v", got)
	}

	global.Flags.LogRequestHeaders, global.Flags.LogRequestMiniBody, global.Flags.LogResponseHeaders = true, true, true
	ctx = newCtx()
	LogRequest(ctx, 8080, "svc", "m", "host", map[string]string{"a": "b"}, 1, 2, "mini")
	LogResponse(ctx, map[string]string{"r": "h"}, 0, 1, 2, "act")
	got = logs(ctx)
	if len(got) != 2 || !strings.Contains(got[0], `Request Headers: [{"a":"b"}]`) || !strings.Contains(got[0], "Request Mini Body [mini]") ||
		!strings.Contains(got[1], `Response Headers: [{"r":"h"}]`) {
		t.Errorf("logs with headers and body = %v", got)
	}
}
