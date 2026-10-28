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
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"goto/pkg/types"

	"github.com/jhump/protoreflect/v2/grpcdynamic"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/dynamicpb"
)

// recordingSTS is the transport stream of a test server stream: it records the headers that are sent through it.
type recordingSTS struct {
	method  string
	lock    sync.Mutex
	headers metadata.MD
}

func (r *recordingSTS) Method() string { return r.method }
func (r *recordingSTS) SetHeader(md metadata.MD) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.headers = metadata.Join(r.headers, md)
	return nil
}
func (r *recordingSTS) SendHeader(md metadata.MD) error { return r.SetHeader(md) }
func (r *recordingSTS) SetTrailer(metadata.MD) error    { return nil }
func (r *recordingSTS) sent() metadata.MD {
	r.lock.Lock()
	defer r.lock.Unlock()
	return r.headers.Copy()
}

// serverEnv is a server side stream of a method of the echo service over a fake gRPC stream.
type serverEnv struct {
	method *GRPCServiceMethod
	ss     *fakeServerStream
	sts    *recordingSTS
	stream GRPCStream
}

func newServerEnv(t *testing.T, methodName string) *serverEnv {
	t.Helper()
	stubHandlers(t)
	m := newRegistry().NewGRPCService(echoDesc()).Methods[methodName]
	sts := &recordingSTS{method: m.URI}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-in", "1"))
	ctx = grpc.NewContextWithServerTransportStream(ctx, sts)
	ss := newFakeServerStream(ctx)
	return &serverEnv{method: m, ss: ss, sts: sts, stream: NewServerStream(8080, m, ss, nil)}
}

func texts(msgs ...proto.Message) []string {
	var out []string
	for _, m := range msgs {
		out = append(out, textOf(m))
	}
	return out
}

func collect(ch chan proto.Message) []string {
	var out []string
	for m := range ch {
		out = append(out, textOf(m))
	}
	return out
}

func feedChan(texts ...string) chan proto.Message {
	ch := make(chan proto.Message, len(texts)+1)
	for _, text := range texts {
		ch <- echoMessage(text)
	}
	close(ch)
	return ch
}

func upper(m proto.Message) proto.Message { return echoMessage(strings.ToUpper(textOf(m))) }

func TestServerStreamIdentity(t *testing.T) {
	tests := []struct {
		method       string
		client, serv bool
	}{
		{"Unary", false, false}, {"ClientStream", true, false}, {"ServerStream", false, true}, {"Bidi", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			e := newServerEnv(t, tt.method)
			s := e.stream
			if s.Type() != "GRPCServerStream" || !s.HasStream() || s.Method() != e.method {
				t.Errorf("type %q has stream %v", s.Type(), s.HasStream())
			}
			if want := "/gototest.echo/" + strings.ToLower(tt.method) + "[server]"; s.Label() != want {
				t.Errorf("label = %q, want %q", s.Label(), want)
			}
			if s.IsClientStreaming() != tt.client || s.IsServerStreaming() != tt.serv {
				t.Errorf("client streaming %v server streaming %v, want %v %v", s.IsClientStreaming(), s.IsServerStreaming(), tt.client, tt.serv)
			}
			if s.Context() != e.ss.ctx {
				t.Errorf("the context of the stream must be the one of the gRPC stream")
			}
		})
	}
}

func TestServerStreamSendAndReceive(t *testing.T) {
	e := newServerEnv(t, "Bidi")
	e.ss.feed(true, "a", "b")
	for _, want := range []string{"a", "b"} {
		msg, err := e.stream.Receive()
		if err != nil || textOf(msg) != want {
			t.Fatalf("Receive = %v %v, want %s", msg, err, want)
		}
	}
	if msg, err := e.stream.Receive(); err != io.EOF {
		t.Errorf("Receive at the end = %v %v, want io.EOF", msg, err)
	}
	if err := e.stream.Send(echoMessage("out")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := e.ss.sentTexts(); !reflect.DeepEqual(got, []string{"out"}) {
		t.Errorf("sent %v", got)
	}
	tracker := e.stream.(*GRPCServerStream).tracker
	if req, resp := tracker.Counts(); req != 2 || resp != 1 {
		t.Errorf("counts = %d requests %d responses, want 2 and 1", req, resp)
	}
	e.ss.sendErr = errors.New("broken")
	if err := e.stream.Send(echoMessage("lost")); err == nil {
		t.Errorf("Send must report the error of the stream")
	}
	if _, resp := tracker.Counts(); resp != 1 {
		t.Errorf("a message that wasn't sent must not be counted, got %d responses", resp)
	}
}

func TestServerStreamSendMulti(t *testing.T) {
	e := newServerEnv(t, "ServerStream")
	e.method.Out = upper
	final, sent, err := e.stream.SendMulti([]proto.Message{echoMessage("a"), echoMessage("b"), echoMessage("c")})
	if err != nil || sent != 3 || final != nil {
		t.Fatalf("SendMulti = %v %d %v, want no final message, 3 sent", final, sent, err)
	}
	if got := e.ss.sentTexts(); !reflect.DeepEqual(got, []string{"A", "B", "C"}) {
		t.Errorf("sent %v, want the messages transformed by the method", got)
	}
}

func TestServerStreamSendPayloadsAndJSONs(t *testing.T) {
	e := newServerEnv(t, "ServerStream")
	if _, sent, err := e.stream.SendPayloads([][]byte{[]byte(`{"text":"p1"}`), []byte(`{"text":"p2"}`)}); err != nil || sent != 2 {
		t.Fatalf("SendPayloads = %d %v", sent, err)
	}
	e2 := newServerEnv(t, "ServerStream")
	if _, sent, err := e2.stream.SendJSONs([]any{map[string]any{"text": "j1"}}); err != nil || sent != 1 {
		t.Fatalf("SendJSONs = %d %v", sent, err)
	}
	if got := e.ss.sentTexts(); !reflect.DeepEqual(got, []string{"p1", "p2"}) {
		t.Errorf("sent payloads %v", got)
	}
	if got := e2.ss.sentTexts(); !reflect.DeepEqual(got, []string{"j1"}) {
		t.Errorf("sent JSONs %v", got)
	}
	e3 := newServerEnv(t, "ServerStream")
	if _, sent, err := e3.stream.SendPayloads([][]byte{[]byte(`{"nosuchfield":1}`)}); err == nil || sent != 0 || len(e3.ss.sent) != 0 {
		t.Errorf("a payload that doesn't fit the message must be refused without sending, got %d %v", sent, err)
	}
}

func TestServerStreamTeeStreamReceive(t *testing.T) {
	e := newServerEnv(t, "Bidi")
	e.method.In = upper
	e.ss.feed(true, "a", "b", "c")
	in, tee := make(chan proto.Message, 10), make(chan proto.Message, 10)
	count, err := e.stream.TeeStreamReceive(in, tee)
	if err != nil || count != 3 {
		t.Fatalf("TeeStreamReceive = %d %v, want 3", count, err)
	}
	if got := collect(in); !reflect.DeepEqual(got, []string{"A", "B", "C"}) {
		t.Errorf("in channel got %v, want the messages transformed by the method", got)
	}
	if got := collect(tee); !reflect.DeepEqual(got, []string{"A", "B", "C"}) {
		t.Errorf("tee channel got %v", got)
	}
}

func TestServerStreamReceiveAndDiscard(t *testing.T) {
	e := newServerEnv(t, "ClientStream")
	e.ss.feed(true, "a", "b")
	if count, err := e.stream.ReceiveAndDiscard(); err != nil || count != 2 {
		t.Errorf("ReceiveAndDiscard = %d %v, want 2", count, err)
	}
}

func TestServerStreamTeeStreamSend(t *testing.T) {
	e := newServerEnv(t, "Bidi")
	out, tee := feedChan("a", "b"), make(chan proto.Message, 10)
	sent, err := e.stream.TeeStreamSend(nil, out, tee)
	if err != nil || sent != 2 {
		t.Fatalf("TeeStreamSend = %d %v, want 2", sent, err)
	}
	if got := e.ss.sentTexts(); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("sent %v", got)
	}
	if got := collect(tee); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("tee channel got %v, want a copy of what was sent", got)
	}
}

func TestServerStreamAsyncSendReceive(t *testing.T) {
	e := newServerEnv(t, "Bidi")
	e.ss.feed(true, "x", "y", "z")
	inMessages, headers, trailers, err := e.stream.AsyncSendReceive([]proto.Message{echoMessage("a"), echoMessage("b")})
	if err != nil {
		t.Fatalf("AsyncSendReceive: %v", err)
	}
	if len(inMessages) != 0 {
		t.Errorf("a server stream has no final message, got %v", texts(inMessages...))
	}
	if got := headers.Get("x-in"); !reflect.DeepEqual(got, []string{"1"}) || trailers != nil {
		t.Errorf("headers %v trailers %v, want the incoming headers and no trailers", headers, trailers)
	}
	if got := e.ss.sentTexts(); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("sent %v", got)
	}
}

func TestServerStreamAsyncSendReceiveRaw(t *testing.T) {
	e := newServerEnv(t, "Bidi")
	e.ss.feed(true)
	out, headers, _, err := e.stream.AsyncSendReceiveRaw([][]byte{[]byte(`{"text":"raw"}`)})
	if err != nil || len(out) != 0 || headers == nil {
		t.Errorf("AsyncSendReceiveRaw = %v %v %v", out, headers, err)
	}
	if got := e.ss.sentTexts(); !reflect.DeepEqual(got, []string{"raw"}) {
		t.Errorf("sent %v", got)
	}
	e2 := newServerEnv(t, "Bidi")
	if _, _, _, err := e2.stream.AsyncSendReceiveRaw([][]byte{[]byte(`{"nosuchfield":1}`)}); err == nil {
		t.Errorf("a payload that doesn't fit the message must be refused")
	}
}

func TestServerStreamChainedSendReceive(t *testing.T) {
	e := newServerEnv(t, "Bidi")
	e.ss.feed(true, "a", "b", "c")
	in := make(chan proto.Message, 10)
	received, sent, err := e.stream.ChainedSendReceive(in, feedChan("x", "y"))
	if err != nil || received != 3 || sent != 2 {
		t.Fatalf("ChainedSendReceive = %d received %d sent %v, want 3 and 2", received, sent, err)
	}
	if got := collect(in); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("received messages %v", got)
	}
	if got := e.ss.sentTexts(); !reflect.DeepEqual(got, []string{"x", "y"}) {
		t.Errorf("sent %v", got)
	}
}

func TestServerStreamChainedTeeStream(t *testing.T) {
	e := newServerEnv(t, "Bidi")
	e.ss.feed(true, "a", "b")
	in, teeIn, teeOut := make(chan proto.Message, 10), make(chan proto.Message, 10), make(chan proto.Message, 10)
	received, sent, err := e.stream.ChainedTeeStream(in, feedChan("x"), teeIn, teeOut)
	if err != nil || received != 2 || sent != 1 {
		t.Fatalf("ChainedTeeStream = %d received %d sent %v, want 2 and 1", received, sent, err)
	}
	if !reflect.DeepEqual(collect(in), []string{"a", "b"}) || !reflect.DeepEqual(collect(teeIn), []string{"a", "b"}) || !reflect.DeepEqual(collect(teeOut), []string{"x"}) {
		t.Errorf("channels didn't get the messages that were received and sent")
	}
}

func TestServerStreamChainInOutWithIdentityHook(t *testing.T) {
	e := newServerEnv(t, "Bidi")
	e.ss.feed(true, "a", "b", "c")
	received, sent, err := e.stream.ChainInOut(IdentityHook, IdentityHeadersHook)
	if err != nil || received != 3 || sent != 3 {
		t.Fatalf("ChainInOut = %d received %d sent %v, want 3 and 3", received, sent, err)
	}
	if got := e.ss.sentTexts(); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("sent %v, want everything that was received", got)
	}
}

func TestServerStreamChainInOutWithHook(t *testing.T) {
	e := newServerEnv(t, "Bidi")
	e.ss.feed(true, "a", "drop", "b")
	hook := func(m proto.Message) (metadata.MD, []proto.Message, *types.Delay, error) {
		switch textOf(m) {
		case "drop":
			return nil, nil, nil, errors.New("dropped")
		case "a":
			return metadata.Pairs("x-hook", "1"), []proto.Message{echoMessage("a1"), echoMessage("a2")}, nil, nil
		}
		return metadata.Pairs("x-hook", "2"), []proto.Message{upper(m)}, nil, nil
	}
	received, sent, err := e.stream.ChainInOut(hook, nil)
	if err != nil || received != 3 || sent != 3 {
		t.Fatalf("ChainInOut = %d received %d sent %v, want 3 and 3", received, sent, err)
	}
	if got := e.ss.sentTexts(); !reflect.DeepEqual(got, []string{"a1", "a2", "B"}) {
		t.Errorf("sent %v, want the outputs of the hook, in order, and nothing for the message that the hook failed on", got)
	}
	if got := e.sts.sent().Get("x-hook"); !reflect.DeepEqual(got, []string{"1"}) {
		t.Errorf("headers of the hook = %v, want only the headers of the first output to be sent", got)
	}
}

func TestServerStreamChainInOutOfAServerStreamingMethodReadsOneMessage(t *testing.T) {
	e := newServerEnv(t, "ServerStream")
	e.ss.feed(true, "only")
	hook := func(m proto.Message) (metadata.MD, []proto.Message, *types.Delay, error) {
		return nil, []proto.Message{echoMessage(textOf(m) + "-1"), echoMessage(textOf(m) + "-2")}, nil, nil
	}
	_, sent, err := e.stream.ChainInOut(hook, nil)
	if err != nil || sent != 2 {
		t.Fatalf("ChainInOut = %d sent %v, want 2", sent, err)
	}
	if got := e.ss.sentTexts(); !reflect.DeepEqual(got, []string{"only-1", "only-2"}) {
		t.Errorf("sent %v", got)
	}
}

func TestServerStreamCrossHookOfTwoServerStreams(t *testing.T) {
	// Both ends are server side streams here: what one receives is sent by the other, through the hooks.
	down := newServerEnv(t, "Bidi")
	up := newServerEnv(t, "Bidi")
	down.ss.feed(true, "d1", "d2")
	up.ss.feed(true, "u1")
	hookDown := func(m proto.Message) (metadata.MD, []proto.Message, *types.Delay, error) {
		return nil, []proto.Message{echoMessage(textOf(m) + ">up")}, nil, nil
	}
	hookUp := func(m proto.Message) (metadata.MD, []proto.Message, *types.Delay, error) {
		return nil, []proto.Message{echoMessage(textOf(m) + ">down")}, nil, nil
	}
	if _, _, err := down.stream.CrossHook(up.stream, hookDown, hookUp, IdentityHeadersHook, IdentityHeadersHook); err != nil {
		t.Fatalf("CrossHook: %v", err)
	}
	if got := up.ss.sentTexts(); !reflect.DeepEqual(got, []string{"d1>up", "d2>up"}) {
		t.Errorf("upstream was sent %v", got)
	}
	if got := down.ss.sentTexts(); !reflect.DeepEqual(got, []string{"u1>down"}) {
		t.Errorf("downstream was sent %v", got)
	}
}

func TestServerStreamHeaders(t *testing.T) {
	e := newServerEnv(t, "Bidi")
	if md, err := e.stream.Headers(); err != nil || md.Get("x-in")[0] != "1" {
		t.Errorf("Headers = %v %v, want the incoming headers", md, err)
	}
	if md := e.stream.Trailers(); md != nil {
		t.Errorf("a server stream has no trailers to read, got %v", md)
	}
	if err := e.stream.SendHeaders(metadata.Pairs("x-out", "1")); err != nil || e.sts.sent().Get("x-out")[0] != "1" {
		t.Errorf("SendHeaders: %v, sent %v", err, e.sts.sent())
	}
	bare := &GRPCBaseStream{}
	if _, err := bare.Headers(); err == nil {
		t.Errorf("a stream without a way to read the headers must give an error")
	}
	if bare.Trailers() != nil {
		t.Errorf("a stream without trailers gives none")
	}
}

func TestServerStreamSendChainHeaders(t *testing.T) {
	src := newServerEnv(t, "Bidi")
	dst := newServerEnv(t, "Bidi")
	hook := func(md metadata.MD) (metadata.MD, error) {
		md = md.Copy()
		md.Append("x-hooked", "yes")
		return md, nil
	}
	if err := src.stream.SendChainHeaders(dst.stream, hook, true); err != nil {
		t.Fatalf("SendChainHeaders: %v", err)
	}
	got := dst.sts.sent()
	if got.Get("x-in")[0] != "1" || got.Get("x-hooked")[0] != "yes" {
		t.Errorf("headers sent to the other stream = %v, want the incoming headers as changed by the hook", got)
	}
	via := got.Get("via-goto")
	if len(via) == 0 || !strings.HasSuffix(via[len(via)-1], "(proxy)") {
		t.Errorf("via-goto = %v, want the proxy added", via)
	}
	proxyHeaders := 0
	for k := range got {
		if strings.HasPrefix(k, "goto-proxy-") || k == "proxy-via-goto" {
			proxyHeaders++
		}
	}
	if proxyHeaders != 3 {
		t.Errorf("proxy headers = %d in %v, want 3", proxyHeaders, got)
	}

	failing := func(metadata.MD) (metadata.MD, error) { return nil, errors.New("hook failed") }
	if err := src.stream.SendChainHeaders(dst.stream, failing, false); err == nil {
		t.Errorf("the error of the hook must be returned")
	}
	if err := (&GRPCBaseStream{}).SendChainHeaders(nil, nil, false); err == nil {
		t.Errorf("a stream without headers to send must give an error")
	}
}

func TestServerStreamDelay(t *testing.T) {
	e := newServerEnv(t, "ServerStream")
	e.stream.SetDelay(5*time.Millisecond, 5*time.Millisecond, 2)
	start := time.Now()
	e.stream.Send(echoMessage("1"))
	e.stream.Send(echoMessage("2"))
	if elapsed := time.Since(start); elapsed < 10*time.Millisecond {
		t.Errorf("two sends took %s, want at least 10ms of delay", elapsed)
	}
	start = time.Now()
	e.stream.Send(echoMessage("3"))
	if elapsed := time.Since(start); elapsed >= 5*time.Millisecond {
		t.Errorf("the delay applies to the first 2 sends only, the third took %s", elapsed)
	}
	if got := e.stream.(*GRPCServerStream).delayCount; got != 0 {
		t.Errorf("delay count = %d, want 0 once it was used up", got)
	}
	e.stream.SetDelay(time.Millisecond, time.Millisecond, -1)
	e.stream.Send(echoMessage("4"))
	if got := e.stream.(*GRPCServerStream).delayCount; got != -1 {
		t.Errorf("delay count = %d, want -1 (unlimited) to stay", got)
	}
}

func TestServerStreamClose(t *testing.T) {
	e := newServerEnv(t, "Bidi")
	if msg, err := e.stream.Close(); msg != nil || err != nil {
		t.Errorf("Close = %v %v", msg, err)
	}
	var bare GRPCServerStream
	if _, err := bare.Close(); err == nil {
		t.Errorf("a stream that isn't open can't be closed")
	}
	if _, _, err := bare.SendPayloads(nil); err == nil {
		t.Errorf("a stream that isn't open can't send")
	}
	if _, _, err := bare.SendJSONs(nil); err == nil {
		t.Errorf("a stream that isn't open can't send")
	}
	if _, err := bare.Receive(); err == nil {
		t.Errorf("a stream that isn't open can't receive")
	}
	if err := bare.Send(nil); err == nil {
		t.Errorf("a stream that isn't open can't send")
	}
	if bare.Context() != nil {
		t.Errorf("a stream that isn't open has no context")
	}
}

func TestStreamHookOfASingleInputReadsOnlyTheFirstMessage(t *testing.T) {
	var calls []string
	hook := func(m proto.Message) (metadata.MD, []proto.Message, *types.Delay, error) {
		calls = append(calls, textOf(m))
		return nil, []proto.Message{echoMessage(textOf(m) + "!")}, nil, nil
	}
	sh := NewStreamHook("test", context.Background(), hook, false, false)
	sh.Run()
	sh.in <- echoMessage("first")
	sh.in <- echoMessage("second")
	close(sh.in)
	got := collect(sh.out)
	if !reflect.DeepEqual(calls, []string{"first"}) || !reflect.DeepEqual(got, []string{"first!"}) {
		t.Errorf("hook called with %v and output %v, want only the first message to be processed and its output flushed", calls, got)
	}
}

func TestStreamHookOfAStreamOutputsEachInput(t *testing.T) {
	sh := NewStreamHook("test", context.Background(), IdentityHook, true, true)
	sh.Run()
	for _, text := range []string{"a", "b", "c"} {
		sh.in <- echoMessage(text)
	}
	close(sh.in)
	if got := collect(sh.out); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("output %v", got)
	}
}

func TestStreamHookOfASingleOutputKeepsTheLastOutput(t *testing.T) {
	sh := NewStreamHook("test", context.Background(), IdentityHook, true, false)
	sh.Run()
	for _, text := range []string{"a", "b", "c"} {
		sh.in <- echoMessage(text)
	}
	close(sh.in)
	if got := collect(sh.out); !reflect.DeepEqual(got, []string{"c"}) {
		t.Errorf("output %v, want only the last output to be sent when the method has a single response", got)
	}
}

func TestStreamHookWithDelayOnTheOutput(t *testing.T) {
	delay := types.NewDelay(5*time.Millisecond, 5*time.Millisecond, 2)
	hook := func(m proto.Message) (metadata.MD, []proto.Message, *types.Delay, error) {
		return nil, []proto.Message{m}, delay, nil
	}
	sh := NewStreamHook("test", context.Background(), hook, true, true)
	sh.Run()
	start := time.Now()
	for _, text := range []string{"a", "b"} {
		sh.in <- echoMessage(text)
	}
	close(sh.in)
	if got := collect(sh.out); len(got) != 2 {
		t.Fatalf("output %v", got)
	}
	if elapsed := time.Since(start); elapsed < 10*time.Millisecond {
		t.Errorf("took %s, want the delay of the hook to be applied to each output", elapsed)
	}
}

func TestIdentityAndTeeHooks(t *testing.T) {
	msg := echoMessage("m")
	if md, out, delay, err := IdentityHook(msg); md != nil || len(out) != 1 || out[0] != msg || delay != nil || err != nil {
		t.Errorf("IdentityHook = %v %v %v %v", md, out, delay, err)
	}
	var delays []string
	idDelay := IdentityHookWithDelay(func() string { delays = append(delays, "d"); return "1s" })
	if _, out, _, err := idDelay(msg); len(out) != 1 || err != nil || len(delays) != 1 {
		t.Errorf("IdentityHookWithDelay: %v %v, delay func called %d times", out, err, len(delays))
	}
	if _, out, _, _ := IdentityHookWithDelay(nil)(msg); len(out) != 1 {
		t.Errorf("IdentityHookWithDelay without a delay func must still pass the message")
	}

	tee := make(chan proto.Message, 4)
	if _, out, _, err := TeeHook(tee)(msg); err != nil || len(out) != 1 || out[0] != msg {
		t.Errorf("TeeHook = %v %v", out, err)
	}
	if _, out, _, err := TeeHookWithDelay(tee, func() string { return "" })(msg); err != nil || len(out) != 1 {
		t.Errorf("TeeHookWithDelay = %v %v", out, err)
	}
	if _, out, _, err := TeeHookWithDelay(tee, nil)(msg); err != nil || len(out) != 1 {
		t.Errorf("TeeHookWithDelay without a delay func = %v %v", out, err)
	}
	if len(tee) != 3 {
		t.Errorf("tee channel has %d messages, want 3", len(tee))
	}

	md := metadata.Pairs("k", "v")
	copied, err := IdentityHeadersHook(md)
	if err != nil || !reflect.DeepEqual(copied, md) {
		t.Errorf("IdentityHeadersHook = %v %v", copied, err)
	}
	copied.Set("k", "changed")
	if md.Get("k")[0] != "v" {
		t.Errorf("IdentityHeadersHook must return a copy")
	}
	var seen metadata.MD
	got, err := TeeHeadersHook(func(m metadata.MD) { seen = m })(md)
	if err != nil || !reflect.DeepEqual(got, md) || !reflect.DeepEqual(seen, md) {
		t.Errorf("TeeHeadersHook = %v %v, callback saw %v", got, err, seen)
	}
	seen.Set("k", "changed")
	if md.Get("k")[0] != "v" {
		t.Errorf("the callback of TeeHeadersHook must get a copy")
	}
}

func TestGRPCStreamTracker(t *testing.T) {
	var tr GRPCStreamTracker
	tr.IncrementRequestCount()
	tr.IncrementRequestCount()
	tr.IncrementResponseCount()
	tr.AddMessage(echoMessage("logged"))
	if req, resp := tr.Counts(); req != 2 || resp != 1 || len(tr.MessageLog) != 1 {
		t.Errorf("counts %d %d log %d", req, resp, len(tr.MessageLog))
	}
}

// Both directions of a stream count at the same time. Run with -race.
func TestGRPCStreamTrackerConcurrentCounting(t *testing.T) {
	var tr GRPCStreamTracker
	const goroutines, perGoroutine = 8, 500
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				tr.IncrementRequestCount()
				tr.IncrementResponseCount()
				tr.Counts()
			}
		}()
	}
	wg.Wait()
	if req, resp := tr.Counts(); req != goroutines*perGoroutine || resp != goroutines*perGoroutine {
		t.Errorf("counts = %d %d, want %d each", req, resp, goroutines*perGoroutine)
	}
}

// ---- client side streams, over a real in-memory gRPC connection ----

// echoServer is the service that the client streams talk to: Unary answers "echo:<text>", ClientStream counts what it
// gets and answers "got:<count>", ServerStream sends as many messages "r0", "r1", ... as the number that it's asked
// for, and Bidi answers every message with "echo:<text>".
func echoServer(t *testing.T) *grpcdynamic.Stub {
	t.Helper()
	desc := echoDesc().Methods().ByName("Unary").Input()
	newMsg := func() *dynamicpb.Message { return dynamicpb.NewMessage(desc) }
	reply := func(text string) proto.Message { return echoMessage(text) }
	sd := &grpc.ServiceDesc{
		ServiceName: "gototest.Echo",
		HandlerType: (*any)(nil),
		Methods: []grpc.MethodDesc{{MethodName: "Unary", Handler: func(_ any, _ context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
			in := newMsg()
			if err := dec(in); err != nil {
				return nil, err
			}
			return reply("echo:" + textOf(in)), nil
		}}},
		Streams: []grpc.StreamDesc{
			{StreamName: "ClientStream", ClientStreams: true, Handler: func(_ any, ss grpc.ServerStream) error {
				ss.SendHeader(metadata.Pairs("x-header", "h"))
				ss.SetTrailer(metadata.Pairs("x-trailer", "t"))
				count := 0
				for {
					if err := ss.RecvMsg(newMsg()); err == io.EOF {
						return ss.SendMsg(reply(fmt.Sprintf("got:%d", count)))
					} else if err != nil {
						return err
					}
					count++
				}
			}},
			{StreamName: "ServerStream", ServerStreams: true, Handler: func(_ any, ss grpc.ServerStream) error {
				in := newMsg()
				if err := ss.RecvMsg(in); err != nil {
					return err
				}
				ss.SendHeader(metadata.Pairs("x-header", "h"))
				ss.SetTrailer(metadata.Pairs("x-trailer", "t"))
				var n int
				fmt.Sscanf(textOf(in), "%d", &n)
				for i := 0; i < n; i++ {
					if err := ss.SendMsg(reply(fmt.Sprintf("r%d", i))); err != nil {
						return err
					}
				}
				return nil
			}},
			{StreamName: "Bidi", ClientStreams: true, ServerStreams: true, Handler: func(_ any, ss grpc.ServerStream) error {
				ss.SendHeader(metadata.Pairs("x-header", "h"))
				ss.SetTrailer(metadata.Pairs("x-trailer", "t"))
				for {
					in := newMsg()
					if err := ss.RecvMsg(in); err == io.EOF {
						return nil
					} else if err != nil {
						return err
					}
					if textOf(in) == "fail" {
						return status.Error(codes.Aborted, "asked to fail")
					}
					if err := ss.SendMsg(reply("echo:" + textOf(in))); err != nil {
						return err
					}
				}
			}},
		},
	}
	conn := listen(t, func(s *grpc.Server) { s.RegisterService(sd, struct{}{}) })
	return grpcdynamic.NewStub(conn)
}

type clientEnv struct {
	stub   *grpcdynamic.Stub
	method *GRPCServiceMethod
}

func newClientEnv(t *testing.T, methodName string) *clientEnv {
	t.Helper()
	stubHandlers(t)
	return &clientEnv{stub: echoServer(t), method: newRegistry().NewGRPCService(echoDesc()).Methods[methodName]}
}

func (c *clientEnv) ctx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func (c *clientEnv) clientStream(t *testing.T) GRPCStream {
	cs, err := c.stub.InvokeRpcClientStream(c.ctx(t), c.method.PMD)
	if err != nil {
		t.Fatalf("open client stream: %v", err)
	}
	return NewGRPCStreamForClient(1, c.method, cs, nil, nil)
}

func (c *clientEnv) serverStream(t *testing.T, count string) GRPCStream {
	ss, err := c.stub.InvokeRpcServerStream(c.ctx(t), c.method.PMD, echoMessage(count))
	if err != nil {
		t.Fatalf("open server stream: %v", err)
	}
	return NewGRPCStreamForClient(1, c.method, nil, ss, nil)
}

func (c *clientEnv) bidiStream(t *testing.T) GRPCStream {
	bs, err := c.stub.InvokeRpcBidiStream(c.ctx(t), c.method.PMD)
	if err != nil {
		t.Fatalf("open bidi stream: %v", err)
	}
	return NewGRPCStreamForClient(1, c.method, nil, nil, bs)
}

func TestClientStreamIdentity(t *testing.T) {
	c := newClientEnv(t, "Bidi")
	s := c.bidiStream(t)
	if s.Type() != "GRPCClientStream" || !s.HasStream() || s.Method() != c.method || s.Context() == nil {
		t.Errorf("type %q has stream %v", s.Type(), s.HasStream())
	}
	if s.Label() != "/gototest.echo/bidi[client]" || !s.IsClientStreaming() || !s.IsServerStreaming() {
		t.Errorf("label %q client streaming %v server streaming %v", s.Label(), s.IsClientStreaming(), s.IsServerStreaming())
	}
}

func TestClientStreamSendMultiOfAClientStreamingMethod(t *testing.T) {
	c := newClientEnv(t, "ClientStream")
	s := c.clientStream(t)
	final, sent, err := s.SendMulti([]proto.Message{echoMessage("a"), echoMessage("b"), echoMessage("c")})
	if err != nil || sent != 3 || final == nil || textOf(final) != "got:3" {
		t.Fatalf("SendMulti = %v %d %v, want the final message got:3 after 3 sent", final, sent, err)
	}
	if md, err := s.Headers(); err != nil || md.Get("x-header")[0] != "h" {
		t.Errorf("Headers = %v %v", md, err)
	}
	if md := s.Trailers(); md.Get("x-trailer")[0] != "t" {
		t.Errorf("Trailers = %v", md)
	}
}

func TestClientStreamSendPayloadsAndJSONs(t *testing.T) {
	c := newClientEnv(t, "ClientStream")
	final, sent, err := c.clientStream(t).SendPayloads([][]byte{[]byte(`{"text":"p1"}`), []byte(`{"text":"p2"}`)})
	if err != nil || sent != 2 || textOf(final) != "got:2" {
		t.Errorf("SendPayloads = %v %d %v", final, sent, err)
	}
	final, sent, err = c.clientStream(t).SendJSONs([]any{map[string]any{"text": "j1"}})
	if err != nil || sent != 1 || textOf(final) != "got:1" {
		t.Errorf("SendJSONs = %v %d %v", final, sent, err)
	}
	if _, sent, err := c.clientStream(t).SendPayloads([][]byte{[]byte(`{"nosuchfield":1}`)}); err == nil || sent != 0 {
		t.Errorf("a payload that doesn't fit the message must be refused, got %d %v", sent, err)
	}
}

func TestClientStreamSendOneByOne(t *testing.T) {
	c := newClientEnv(t, "ClientStream")
	s := c.clientStream(t)
	for _, text := range []string{"a", "b"} {
		if err := s.Send(echoMessage(text)); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}
	final, err := s.Close()
	if err != nil || textOf(final) != "got:2" {
		t.Errorf("Close = %v %v, want the final message got:2", final, err)
	}
	if req, _ := s.(*GRPCClientStream).tracker.Counts(); req != 2 {
		t.Errorf("requests counted = %d, want 2", req)
	}
}

func TestClientStreamReceiveFromAServerStream(t *testing.T) {
	c := newClientEnv(t, "ServerStream")
	s := c.serverStream(t, "3")
	var got []string
	for {
		msg, err := s.Receive()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Receive: %v", err)
		}
		got = append(got, textOf(msg))
	}
	if !reflect.DeepEqual(got, []string{"r0", "r1", "r2"}) {
		t.Errorf("received %v", got)
	}
	if md, err := s.Headers(); err != nil || md.Get("x-header")[0] != "h" {
		t.Errorf("Headers = %v %v", md, err)
	}
	if md := s.Trailers(); md.Get("x-trailer")[0] != "t" {
		t.Errorf("Trailers = %v", md)
	}
	if msg, err := s.Close(); msg != nil || err != nil {
		t.Errorf("Close of a server stream = %v %v", msg, err)
	}
}

func TestClientStreamTeeStreamReceiveFromAServerStream(t *testing.T) {
	c := newClientEnv(t, "ServerStream")
	s := c.serverStream(t, "4")
	in, tee := make(chan proto.Message, 10), make(chan proto.Message, 10)
	count, err := s.TeeStreamReceive(in, tee)
	if err != nil || count != 4 {
		t.Fatalf("TeeStreamReceive = %d %v, want 4", count, err)
	}
	want := []string{"r0", "r1", "r2", "r3"}
	if !reflect.DeepEqual(collect(in), want) || !reflect.DeepEqual(collect(tee), want) {
		t.Errorf("channels didn't get %v", want)
	}
}

func TestClientStreamReceiveAndDiscardFromAServerStream(t *testing.T) {
	c := newClientEnv(t, "ServerStream")
	if count, err := c.serverStream(t, "5").ReceiveAndDiscard(); err != nil || count != 5 {
		t.Errorf("ReceiveAndDiscard = %d %v, want 5", count, err)
	}
}

func TestClientStreamBidiSendAndReceive(t *testing.T) {
	c := newClientEnv(t, "Bidi")
	s := c.bidiStream(t)
	for _, text := range []string{"a", "b"} {
		if err := s.Send(echoMessage(text)); err != nil {
			t.Fatalf("Send: %v", err)
		}
		msg, err := s.Receive()
		if err != nil || textOf(msg) != "echo:"+text {
			t.Fatalf("Receive = %v %v, want echo:%s", msg, err, text)
		}
	}
	if msg, err := s.Close(); msg != nil || err != nil {
		t.Errorf("Close = %v %v", msg, err)
	}
	if msg, err := s.Receive(); err != io.EOF {
		t.Errorf("Receive after the server is done = %v %v, want io.EOF", msg, err)
	}
}

func TestClientStreamChainedSendReceiveOfABidiStream(t *testing.T) {
	c := newClientEnv(t, "Bidi")
	s := c.bidiStream(t)
	in := make(chan proto.Message, 10)
	received, sent, err := s.ChainedSendReceive(in, feedChan("a", "b", "c"))
	if err != nil || received != 3 || sent != 3 {
		t.Fatalf("ChainedSendReceive = %d received %d sent %v, want 3 and 3", received, sent, err)
	}
	if got := collect(in); !reflect.DeepEqual(got, []string{"echo:a", "echo:b", "echo:c"}) {
		t.Errorf("received %v", got)
	}
}

func TestClientStreamChainedTeeStreamOfABidiStream(t *testing.T) {
	c := newClientEnv(t, "Bidi")
	s := c.bidiStream(t)
	in, teeIn, teeOut := make(chan proto.Message, 10), make(chan proto.Message, 10), make(chan proto.Message, 10)
	received, sent, err := s.ChainedTeeStream(in, feedChan("a", "b"), teeIn, teeOut)
	if err != nil || received != 2 || sent != 2 {
		t.Fatalf("ChainedTeeStream = %d received %d sent %v, want 2 and 2", received, sent, err)
	}
	if !reflect.DeepEqual(collect(in), []string{"echo:a", "echo:b"}) || !reflect.DeepEqual(collect(teeIn), []string{"echo:a", "echo:b"}) ||
		!reflect.DeepEqual(collect(teeOut), []string{"a", "b"}) {
		t.Errorf("channels didn't get what was received and sent")
	}
}

func TestClientStreamAsyncSendReceive(t *testing.T) {
	c := newClientEnv(t, "ClientStream")
	inMessages, headers, trailers, err := c.clientStream(t).AsyncSendReceive([]proto.Message{echoMessage("a"), echoMessage("b")})
	if err != nil || len(inMessages) != 1 || textOf(inMessages[0]) != "got:2" {
		t.Fatalf("AsyncSendReceive = %v %v", texts(inMessages...), err)
	}
	if headers.Get("x-header")[0] != "h" || trailers.Get("x-trailer")[0] != "t" {
		t.Errorf("headers %v trailers %v", headers, trailers)
	}
}

func TestClientStreamAsyncSendReceiveRaw(t *testing.T) {
	c := newClientEnv(t, "ClientStream")
	payloads, headers, _, err := c.clientStream(t).AsyncSendReceiveRaw([][]byte{[]byte(`{"text":"a"}`)})
	if err != nil || len(payloads) != 1 || !strings.Contains(string(payloads[0]), "got:1") || headers == nil {
		t.Errorf("AsyncSendReceiveRaw = %q %v %v", payloads, headers, err)
	}
}

func TestClientStreamSendMultiStopsWhenTheServerEndsTheCall(t *testing.T) {
	c := newClientEnv(t, "Bidi")
	s := c.bidiStream(t)
	// the server ends the call at "fail", the sends after that get a status, whichever of them is the first to see it
	for i := 0; i < 1000; i++ {
		if err := s.Send(echoMessage("fail")); err != nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Errorf("sends must fail after the server ended the call")
}

func TestClientStreamDelay(t *testing.T) {
	c := newClientEnv(t, "ClientStream")
	s := c.clientStream(t)
	s.SetDelay(5*time.Millisecond, 5*time.Millisecond, 2)
	start := time.Now()
	s.Send(echoMessage("1"))
	s.Send(echoMessage("2"))
	if elapsed := time.Since(start); elapsed < 10*time.Millisecond {
		t.Errorf("two sends took %s, want at least 10ms of delay", elapsed)
	}
}

func TestClientStreamWithoutAStream(t *testing.T) {
	stubHandlers(t)
	m := newRegistry().NewGRPCService(echoDesc()).Methods["Unary"]
	s := NewGRPCStreamForClient(1, m, nil, nil, nil)
	if s == nil || s.HasStream() {
		t.Fatalf("a stream without a call is created without a stream, got %v", s)
	}
	if err := s.Send(echoMessage("a")); err == nil {
		t.Errorf("Send without a stream must fail")
	}
	if _, err := s.Receive(); err == nil {
		t.Errorf("Receive without a stream must fail")
	}
	if _, err := s.Close(); err == nil {
		t.Errorf("Close without a stream must fail")
	}
	if _, _, err := s.SendPayloads(nil); err == nil {
		t.Errorf("SendPayloads without a stream must fail")
	}
	if _, _, err := s.SendJSONs(nil); err == nil {
		t.Errorf("SendJSONs without a stream must fail")
	}
	if _, err := s.ReceiveAndDiscard(); err == nil {
		t.Errorf("ReceiveAndDiscard without a stream must fail")
	}
	if _, _, _, err := s.AsyncSendReceive(nil); err == nil {
		t.Errorf("AsyncSendReceive without a stream must fail")
	}
	if _, _, err := s.ChainedSendReceive(nil, nil); err == nil {
		t.Errorf("ChainedSendReceive without a stream must fail")
	}
	if _, _, err := s.ChainedTeeStream(nil, nil, nil, nil); err == nil {
		t.Errorf("ChainedTeeStream without a stream must fail")
	}
	if _, err := s.Headers(); err == nil {
		t.Errorf("Headers without a stream must fail")
	}
	if s.Trailers() != nil || s.Context() != nil {
		t.Errorf("a stream without a call has no trailers and no context")
	}
}

func TestBaseStreamDefaults(t *testing.T) {
	b := &GRPCBaseStream{}
	if b.Type() != "GRPCBaseStream" || b.Self() != b || b.Label() != "" || b.HasStream() || b.Context() != nil || b.Method() != nil {
		t.Errorf("defaults of a base stream are wrong")
	}
	if msg, err := b.Close(); msg != nil || err != nil {
		t.Errorf("Close = %v %v", msg, err)
	}
	if msg, err := b.Receive(); msg != nil || err != nil {
		t.Errorf("Receive = %v %v", msg, err)
	}
	if err := b.Send(nil); err != nil {
		t.Errorf("Send = %v", err)
	}
	b.KeepOpen(time.Second)
	b.Wait() // no wait group: returns at once
}

// A proxy: the stream that a client opens to the proxy is joined to a stream that the proxy opens to the upstream
// service, with CrossHook, which is how the proxy of the server package uses the streams.
func TestCrossHookProxiesABidiStreamBetweenARealClientAndServer(t *testing.T) {
	stubHandlers(t)
	upstream := echoServer(t)
	psd := echoDesc()
	registry := newRegistry()
	proxied := registry.NewGRPCService(psd)
	proxyMethod := proxied.Methods["Bidi"]
	upstreamMethod := proxyMethod

	var proxyErr error
	var proxyDone sync.WaitGroup
	proxyDone.Add(1)
	proxyDesc := &grpc.ServiceDesc{
		ServiceName: "gototest.Echo",
		HandlerType: (*any)(nil),
		Streams: []grpc.StreamDesc{{StreamName: "Bidi", ClientStreams: true, ServerStreams: true, Handler: func(_ any, ss grpc.ServerStream) error {
			defer proxyDone.Done()
			down := NewServerStream(1, proxyMethod, ss, nil)
			bs, err := upstream.InvokeRpcBidiStream(context.Background(), upstreamMethod.PMD)
			if err != nil {
				proxyErr = err
				return err
			}
			up := NewGRPCStreamForClient(1, upstreamMethod, nil, nil, bs)
			upperHook := func(m proto.Message) (metadata.MD, []proto.Message, *types.Delay, error) {
				return nil, []proto.Message{echoMessage(strings.ToUpper(textOf(m)))}, nil, nil
			}
			_, _, proxyErr = down.CrossHook(up, IdentityHook, upperHook, IdentityHeadersHook, IdentityHeadersHook)
			return proxyErr
		}}},
	}
	proxyConn := listen(t, func(s *grpc.Server) { s.RegisterService(proxyDesc, struct{}{}) })
	client := grpcdynamic.NewStub(proxyConn)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	bs, err := client.InvokeRpcBidiStream(ctx, proxyMethod.PMD)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for _, text := range []string{"a", "b", "c"} {
		if err := bs.SendMsg(echoMessage(text)); err != nil {
			t.Fatalf("send %s: %v", text, err)
		}
	}
	if err := bs.CloseSend(); err != nil {
		t.Fatalf("close send: %v", err)
	}
	var got []string
	for {
		msg, err := bs.RecvMsg()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("receive: %v", err)
		}
		got = append(got, textOf(msg))
	}
	proxyDone.Wait()
	if proxyErr != nil {
		t.Errorf("CrossHook: %v", proxyErr)
	}
	// the upstream echoes, and the hook on its way back to the client makes the text upper case
	if want := []string{"ECHO:A", "ECHO:B", "ECHO:C"}; !reflect.DeepEqual(got, want) {
		t.Errorf("client received %v, want %v", got, want)
	}
}
