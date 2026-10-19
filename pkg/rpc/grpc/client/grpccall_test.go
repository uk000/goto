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
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"sync"
	"testing"
	"time"

	"goto/pkg/types"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/dynamicpb"
)

func addHeaders(kv ...string) *types.Headers {
	h := types.NewHeaders()
	for i := 0; i+1 < len(kv); i += 2 {
		h.Request.Add[kv[i]] = kv[i+1]
	}
	return h
}

func sampleCall(e *testEnv, method string, payloads *GRPCPayloads) *GRPCCall {
	return &GRPCCall{Service: "SampleService", Method: method, Endpoint: e.addr, Payloads: payloads, Headers: types.NewHeaders()}
}

// decode reads the JSON of a response payload.
func decode(t *testing.T, payload string) map[string]any {
	t.Helper()
	m := map[string]any{}
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		t.Fatalf("response payload %q is not JSON: %v", payload, err)
	}
	return m
}

func TestInvokeUnary(t *testing.T) {
	e := newEnv(t)
	c := newClient(t, e, nil)
	call := sampleCall(e, "oneOne", samplePayloads("hello"))
	call.Headers = addHeaders("x-added", "a")

	r := invoke(t, c, call, nil)
	if r == nil || len(r.Errors) != 0 || len(r.Responses) != 1 {
		t.Fatalf("result = %+v", r)
	}
	resp := r.Responses[0]
	if resp.Status != 0 || resp.EquivalentHTTPStatusCode != http.StatusOK {
		t.Errorf("status = %d/%d, want 0/200", resp.Status, resp.EquivalentHTTPStatusCode)
	}
	if len(resp.ResponsePayload) != 1 {
		t.Fatalf("payloads = %v", resp.ResponsePayload)
	}
	if m := decode(t, resp.ResponsePayload[0]); m["content"] != "re:hello" || m["id"] != float64(1) {
		t.Errorf("payload = %v", m)
	}
	if got := resp.ResponseHeaders["x-reply-header"]; len(got) != 1 || got[0] != "h-hello" {
		t.Errorf("response headers = %v", resp.ResponseHeaders)
	}
	if got := resp.ResponseTrailers["x-reply-trailer"]; len(got) != 1 || got[0] != "t-hello" {
		t.Errorf("response trailers = %v", resp.ResponseTrailers)
	}
	if resp.ClientStreamCount != 0 || resp.ServerStreamCount != 0 {
		t.Errorf("stream counts = %d/%d, want 0/0 for a unary call", resp.ClientStreamCount, resp.ServerStreamCount)
	}
	recs := e.sample.recorded("oneOne")
	if len(recs) != 1 || recs[0].md.Get("x-added")[0] != "a" {
		t.Errorf("server did not see the header that the call adds: %+v", recs)
	}
}

func TestInvokeUnaryHeaders(t *testing.T) {
	e := newEnv(t)
	c := newClient(t, e, nil)
	call := sampleCall(e, "oneOne", samplePayloads("h"))
	call.Headers = &types.Headers{Request: &types.HeadersConfig{
		Add:     map[string]string{"x-added": "a", "x-removed": "r"},
		Remove:  []string{"x-removed"},
		Forward: []string{"x-forward", "x-absent"},
	}}
	forwarded := http.Header{}
	forwarded.Set("X-Forward", "fwd")
	forwarded.Set("X-Other", "other")
	call.RequestHeaders = forwarded
	// removals are applied before additions, so a header that is both removed and added is sent
	r := invoke(t, c, call, nil)
	if len(r.Errors) != 0 || len(r.Responses) != 1 {
		t.Fatalf("result = %+v", r)
	}
	md := e.sample.recorded("oneOne")[0].md
	if got := md.Get("x-added"); len(got) != 1 || got[0] != "a" {
		t.Errorf("x-added = %v", got)
	}
	if got := md.Get("x-forward"); len(got) != 1 || got[0] != "fwd" {
		t.Errorf("x-forward = %v, want the value from the request headers", got)
	}
	if len(md.Get("x-other")) != 0 || len(md.Get("x-absent")) != 0 {
		t.Errorf("only the headers listed to forward must be forwarded: %v", md)
	}
}

func TestInvokeUnaryLinearAndConcurrent(t *testing.T) {
	e := newEnv(t)
	c := newClient(t, e, nil)

	r := invoke(t, c, sampleCall(e, "oneOne", samplePayloads("one", "two", "three")), nil)
	if len(r.Errors) != 0 || len(r.Responses) != 3 {
		t.Fatalf("linear result: %d responses, errors %v", len(r.Responses), r.Errors)
	}
	var order []string
	for _, rec := range e.sample.recorded("oneOne") {
		order = append(order, rec.content)
	}
	if len(order) != 3 || order[0] != "one" || order[1] != "two" || order[2] != "three" {
		t.Errorf("linear payloads must be sent in order, got %v", order)
	}
	for i, resp := range r.Responses {
		if want := []string{"one", "two", "three"}[i]; decode(t, resp.ResponsePayload[0])["content"] != "re:"+want {
			t.Errorf("response %d = %v", i, resp.ResponsePayload)
		}
	}

	conc := &GRPCPayloads{}
	for _, s := range []string{"1", "2", "3", "4", "5"} {
		conc.Concurrent = append(conc.Concurrent, &GRPCPayload{
			Payload: `{"content":"c` + s + `"}`,
			Headers: addHeaders("x-stream", s),
		})
	}
	call := sampleCall(e, "oneOne", conc)
	call.Headers = addHeaders("x-base", "b")
	r = invoke(t, c, call, nil)
	if len(r.Errors) != 0 || len(r.Responses) != 5 {
		t.Fatalf("concurrent result: %d responses, errors %v", len(r.Responses), r.Errors)
	}
	var seen []string
	for _, rec := range e.sample.recorded("oneOne")[3:] {
		if rec.md.Get("x-base")[0] != "b" {
			t.Errorf("call level header missing: %v", rec.md)
		}
		seen = append(seen, rec.md.Get("x-stream")[0]+":"+rec.content)
	}
	sort.Strings(seen)
	if len(seen) != 5 || seen[0] != "1:c1" || seen[4] != "5:c5" {
		t.Errorf("every payload is sent with the headers of its own: %v", seen)
	}
}

func TestInvokeUnaryErrors(t *testing.T) {
	e := newEnv(t)
	c := newClient(t, e, nil)

	r := invoke(t, c, sampleCall(e, "oneOne", samplePayloads("fail")), nil)
	if len(r.Errors) != 1 || status.Code(r.Errors[0]) != codes.NotFound {
		t.Fatalf("errors = %v, want one NotFound", r.Errors)
	}
	if len(r.Responses) != 1 || r.Responses[0].Status != int(codes.NotFound) {
		t.Fatalf("a call that fails with a gRPC status still has a response with the status: %+v", r.Responses)
	}
	if got := r.Responses[0].ResponseHeaders["x-reply-header"]; len(got) != 1 || got[0] != "h-fail" {
		t.Errorf("headers of the failed call = %v", r.Responses[0].ResponseHeaders)
	}

	// a payload that is not valid for the method input is an error, and is not sent
	before := len(e.sample.recorded("oneOne"))
	bad := &GRPCPayloads{Linear: []*GRPCPayload{{Payload: `{"noSuchField": 1}`}}}
	r = invoke(t, c, sampleCall(e, "oneOne", bad), nil)
	if len(r.Errors) != 1 || len(r.Responses) != 0 {
		t.Errorf("invalid payload: errors %v responses %d", r.Errors, len(r.Responses))
	}
	if len(e.sample.recorded("oneOne")) != before {
		t.Errorf("an invalid payload must not reach the server")
	}
}

func TestInvokeServerStream(t *testing.T) {
	e := newEnv(t)
	c := newClient(t, e, nil)
	var lock sync.Mutex
	var seen []string
	var seenHeaders []metadata.MD
	callback := func(m proto.Message, h metadata.MD) {
		b, _ := json.Marshal(m)
		lock.Lock()
		defer lock.Unlock()
		seen = append(seen, string(b))
		seenHeaders = append(seenHeaders, h)
	}
	call := sampleCall(e, "oneMany", &GRPCPayloads{Linear: []*GRPCPayload{{Payload: `{"id":3,"content":"s"}`}}})
	r := invoke(t, c, call, callback)
	if len(r.Errors) != 0 || len(r.Responses) != 1 {
		t.Fatalf("result = %+v errors %v", r, r.Errors)
	}
	resp := r.Responses[0]
	if len(resp.ResponsePayload) != 3 || resp.ServerStreamCount != 3 || resp.ClientStreamCount != 1 {
		t.Fatalf("payloads %d, counts client/server %d/%d, want 3 and 1/3", len(resp.ResponsePayload), resp.ClientStreamCount, resp.ServerStreamCount)
	}
	for i, p := range resp.ResponsePayload {
		if m := decode(t, p); m["content"] != []string{"s-1", "s-2", "s-3"}[i] {
			t.Errorf("message %d = %v", i, m)
		}
	}
	lock.Lock()
	defer lock.Unlock()
	if len(seen) != 3 {
		t.Fatalf("callback called %d times, want 3", len(seen))
	}
	for _, h := range seenHeaders {
		if h.Get("x-reply-header")[0] != "h-s" {
			t.Errorf("callback got headers %v", h)
		}
	}
	if got := resp.ResponseTrailers["x-reply-trailer"]; len(got) != 1 || got[0] != "t-s" {
		t.Errorf("trailers = %v", resp.ResponseTrailers)
	}
}

func TestInvokeClientStream(t *testing.T) {
	e := newEnv(t)
	c := newClient(t, e, nil)
	payloads := &GRPCPayloads{Linear: []*GRPCPayload{{Stream: []string{`{"content":"a"}`, `{"content":"b"}`, `{"content":"c"}`}}}}
	r := invoke(t, c, sampleCall(e, "manyOne", payloads), nil)
	if len(r.Errors) != 0 || len(r.Responses) != 1 {
		t.Fatalf("result errors %v responses %d", r.Errors, len(r.Responses))
	}
	resp := r.Responses[0]
	if len(resp.ResponsePayload) != 1 {
		t.Fatalf("payloads = %v", resp.ResponsePayload)
	}
	if m := decode(t, resp.ResponsePayload[0]); m["content"] != "a,b,c" || m["id"] != float64(3) {
		t.Errorf("payload = %v, want the 3 messages joined", m)
	}
	if resp.ClientStreamCount != 3 || resp.ServerStreamCount != 0 {
		t.Errorf("stream counts = %d/%d, want 3/0", resp.ClientStreamCount, resp.ServerStreamCount)
	}
	if got := resp.ResponseHeaders["x-reply-header"]; len(got) != 1 || got[0] != "h-many" {
		t.Errorf("headers = %v", resp.ResponseHeaders)
	}
	if got := resp.ResponseTrailers["x-reply-trailer"]; len(got) != 1 || got[0] != "t-many" {
		t.Errorf("trailers = %v", resp.ResponseTrailers)
	}

	// a single payload is a stream of one
	single := &GRPCPayloads{Linear: []*GRPCPayload{{Payload: `{"content":"only"}`}}}
	r = invoke(t, c, sampleCall(e, "manyOne", single), nil)
	if len(r.Errors) != 0 || len(r.Responses) != 1 || decode(t, r.Responses[0].ResponsePayload[0])["content"] != "only" {
		t.Errorf("single payload client stream: %+v %v", r.Responses, r.Errors)
	}
}

func TestInvokeBidiStream(t *testing.T) {
	e := newEnv(t)
	c := newClient(t, e, nil)
	var lock sync.Mutex
	var seen []string
	callback := func(m proto.Message, h metadata.MD) {
		b, _ := json.Marshal(m)
		lock.Lock()
		defer lock.Unlock()
		seen = append(seen, string(b))
	}
	payloads := &GRPCPayloads{Linear: []*GRPCPayload{{Stream: []string{`{"id":1,"content":"a"}`, `{"id":2,"content":"b"}`, `{"id":3,"content":"c"}`}}}}
	r := invoke(t, c, sampleCall(e, "manyMany", payloads), callback)
	if len(r.Errors) != 0 || len(r.Responses) != 1 {
		t.Fatalf("result errors %v responses %d", r.Errors, len(r.Responses))
	}
	resp := r.Responses[0]
	if len(resp.ResponsePayload) != 3 || resp.ClientStreamCount != 3 || resp.ServerStreamCount != 3 {
		t.Fatalf("payloads %d, counts %d/%d, want 3 and 3/3", len(resp.ResponsePayload), resp.ClientStreamCount, resp.ServerStreamCount)
	}
	for i, p := range resp.ResponsePayload {
		if m := decode(t, p); m["content"] != "echo:"+[]string{"a", "b", "c"}[i] || m["id"] != float64(i+1) {
			t.Errorf("message %d = %v", i, m)
		}
	}
	lock.Lock()
	n := len(seen)
	lock.Unlock()
	if n != 3 {
		t.Errorf("callback called %d times, want 3", n)
	}
	if got := resp.ResponseHeaders["x-reply-header"]; len(got) != 1 || got[0] != "h-bidi" {
		t.Errorf("headers = %v", resp.ResponseHeaders)
	}
	if got := resp.ResponseTrailers["x-reply-trailer"]; len(got) != 1 || got[0] != "t-bidi" {
		t.Errorf("trailers = %v", resp.ResponseTrailers)
	}
}

func TestInvokeConcurrentStreams(t *testing.T) {
	e := newEnv(t)
	c := newClient(t, e, nil)
	payloads := &GRPCPayloads{}
	for _, s := range []string{"1", "2", "3"} {
		payloads.Concurrent = append(payloads.Concurrent, &GRPCPayload{
			Stream:  []string{`{"content":"x` + s + `"}`, `{"content":"y` + s + `"}`},
			Headers: addHeaders("x-stream", s),
		})
	}
	call := sampleCall(e, "manyOne", payloads)
	call.Headers = addHeaders("x-base", "b")
	r := invoke(t, c, call, nil)
	if len(r.Errors) != 0 || len(r.Responses) != 3 {
		t.Fatalf("result errors %v responses %d", r.Errors, len(r.Responses))
	}
	var got []string
	for _, resp := range r.Responses {
		got = append(got, decode(t, resp.ResponsePayload[0])["content"].(string))
	}
	sort.Strings(got)
	if got[0] != "x1,y1" || got[1] != "x2,y2" || got[2] != "x3,y3" {
		t.Errorf("each stream must carry its own messages: %v", got)
	}
	var streams []string
	for _, rec := range e.sample.recorded("manyOne") {
		if rec.md.Get("x-base")[0] != "b" {
			t.Errorf("call level header missing: %v", rec.md)
		}
		streams = append(streams, rec.md.Get("x-stream")[0])
	}
	sort.Strings(streams)
	if len(streams) != 3 || streams[0] != "1" || streams[2] != "3" {
		t.Errorf("streams = %v", streams)
	}
}

func TestInvokeRaw(t *testing.T) {
	e := newEnv(t)
	c := newClient(t, e, nil)
	newMsg := func(m string, id int32, content string) proto.Message {
		msg := dynamicpb.NewMessage(c.Service.Methods[m].InputType())
		fields := msg.Descriptor().Fields()
		msg.Set(fields.ByName("id"), protoInt32(id))
		msg.Set(fields.ByName("content"), protoString(content))
		return msg
	}
	content := func(m proto.Message) string {
		return m.ProtoReflect().Get(m.ProtoReflect().Descriptor().Fields().ByName("content")).String()
	}
	md := metadata.Pairs("x-raw", "r")
	var (
		responses []proto.Message
		hdr, trl  metadata.MD
		err       error
	)
	within(t, 15*time.Second, "InvokeRaw", func() {
		responses, hdr, trl, err = c.InvokeRaw(c.Service.Methods["oneOne"], md, []proto.Message{newMsg("oneOne", 1, "raw")})
	})
	if err != nil || len(responses) != 1 || content(responses[0]) != "re:raw" {
		t.Fatalf("unary raw: %v %v", responses, err)
	}
	if hdr.Get("x-reply-header")[0] != "h-raw" || trl.Get("x-reply-trailer")[0] != "t-raw" {
		t.Errorf("unary raw headers/trailers: %v %v", hdr, trl)
	}
	if got := e.sample.recorded("oneOne")[0].md.Get("x-raw"); len(got) != 1 || got[0] != "r" {
		t.Errorf("metadata given to InvokeRaw must reach the server: %v", got)
	}

	within(t, 15*time.Second, "InvokeRaw server stream", func() {
		responses, hdr, trl, err = c.InvokeRaw(c.Service.Methods["oneMany"], nil, []proto.Message{newMsg("oneMany", 2, "ss")})
	})
	if err != nil || len(responses) != 2 || content(responses[0]) != "ss-1" || content(responses[1]) != "ss-2" {
		t.Fatalf("server stream raw: %v %v", responses, err)
	}
	if hdr.Get("x-reply-header")[0] != "h-ss" {
		t.Errorf("server stream raw headers: %v", hdr)
	}

	within(t, 15*time.Second, "InvokeRaw client stream", func() {
		responses, _, _, err = c.InvokeRaw(c.Service.Methods["manyOne"], nil,
			[]proto.Message{newMsg("manyOne", 1, "a"), newMsg("manyOne", 2, "b"), newMsg("manyOne", 3, "c")})
	})
	if err != nil || len(responses) != 1 || content(responses[0]) != "a,b,c" {
		t.Fatalf("client stream raw: %v %v", responses, err)
	}

	within(t, 15*time.Second, "InvokeRaw bidi stream", func() {
		responses, _, _, err = c.InvokeRaw(c.Service.Methods["manyMany"], nil,
			[]proto.Message{newMsg("manyMany", 1, "a"), newMsg("manyMany", 2, "b")})
	})
	if err != nil || len(responses) != 2 || content(responses[0]) != "echo:a" || content(responses[1]) != "echo:b" {
		t.Fatalf("bidi stream raw: %v %v", responses, err)
	}

	// a unary call that fails is an error with the status
	within(t, 15*time.Second, "InvokeRaw failing", func() {
		_, _, _, err = c.InvokeRaw(c.Service.Methods["oneOne"], nil, []proto.Message{newMsg("oneOne", 1, "fail")})
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("failing unary raw: %v, want NotFound", err)
	}
}

func TestOpenStream(t *testing.T) {
	e := newEnv(t)
	c := newClient(t, e, nil)
	newMsg := func(m string, id int32, content string) proto.Message {
		msg := dynamicpb.NewMessage(c.Service.Methods[m].InputType())
		fields := msg.Descriptor().Fields()
		msg.Set(fields.ByName("id"), protoInt32(id))
		msg.Set(fields.ByName("content"), protoString(content))
		return msg
	}

	within(t, 15*time.Second, "OpenStream server", func() {
		stream, err := c.OpenStream(7, c.Service.Methods["oneMany"], nil, newMsg("oneMany", 2, "o"))
		if err != nil || stream == nil {
			t.Errorf("OpenStream server stream: %v %v", stream, err)
			return
		}
		if !stream.HasStream() || stream.IsClientStreaming() || !stream.IsServerStreaming() {
			t.Errorf("server stream flags: has=%v client=%v server=%v", stream.HasStream(), stream.IsClientStreaming(), stream.IsServerStreaming())
		}
		if stream.Method() != c.Service.Methods["oneMany"] || stream.Label() == "" || stream.Type() != "GRPCClientStream" {
			t.Errorf("stream identity: %v %q %q", stream.Method(), stream.Label(), stream.Type())
		}
		for i, want := range []string{"o-1", "o-2"} {
			msg, err := stream.Receive()
			if err != nil || msg == nil {
				t.Errorf("receive %d: %v %v", i, msg, err)
				return
			}
			if got := msg.ProtoReflect().Get(msg.ProtoReflect().Descriptor().Fields().ByName("content")).String(); got != want {
				t.Errorf("message %d = %q, want %q", i, got, want)
			}
		}
		if _, err := stream.Receive(); err == nil {
			t.Errorf("receive after the last message must fail with EOF")
		}
	})

	within(t, 15*time.Second, "OpenStream bidi", func() {
		stream, err := c.OpenStream(7, c.Service.Methods["manyMany"], nil, newMsg("manyMany", 1, "first"))
		if err != nil || stream == nil {
			t.Errorf("OpenStream bidi: %v %v", stream, err)
			return
		}
		if !stream.IsClientStreaming() || !stream.IsServerStreaming() {
			t.Errorf("bidi flags wrong")
		}
		if err := stream.Send(newMsg("manyMany", 2, "second")); err != nil {
			t.Errorf("send: %v", err)
		}
		for i, want := range []string{"echo:first", "echo:second"} {
			msg, err := stream.Receive()
			if err != nil {
				t.Errorf("receive %d: %v", i, err)
				return
			}
			if got := msg.ProtoReflect().Get(msg.ProtoReflect().Descriptor().Fields().ByName("content")).String(); got != want {
				t.Errorf("bidi message %d = %q, want %q", i, got, want)
			}
		}
		stream.Close()
	})

	within(t, 15*time.Second, "OpenStream client", func() {
		stream, err := c.OpenStream(7, c.Service.Methods["manyOne"], nil, newMsg("manyOne", 1, "x"))
		if err != nil || stream == nil {
			t.Errorf("OpenStream client stream: %v %v", stream, err)
			return
		}
		if !stream.IsClientStreaming() || stream.IsServerStreaming() {
			t.Errorf("client stream flags wrong")
		}
		out, _, err := stream.SendMulti([]proto.Message{newMsg("manyOne", 2, "y")})
		if err != nil || out == nil {
			t.Errorf("SendMulti: %v %v", out, err)
			return
		}
		if got := out.ProtoReflect().Get(out.ProtoReflect().Descriptor().Fields().ByName("content")).String(); got != "x,y" {
			t.Errorf("client stream result = %q, want x,y", got)
		}
	})
}

func TestGRPCResult(t *testing.T) {
	r := newGRPCResult()
	if r.GetErrors() != "" {
		t.Errorf("a result without errors has no error text")
	}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); r.AddError(errors.New("e")) }()
		go func() { defer wg.Done(); r.AddResponse(&GRPCResponse{}) }()
	}
	wg.Wait()
	if len(r.Errors) != 50 || len(r.Responses) != 50 {
		t.Errorf("errors %d responses %d, want 50 each", len(r.Errors), len(r.Responses))
	}
	r2 := newGRPCResult()
	r2.AddError(errors.New("first"))
	r2.AddError(errors.New("second"))
	if got := r2.GetErrors(); got != "first\nsecond\n" {
		t.Errorf("GetErrors = %q", got)
	}
}

func TestNewGRPCResponse(t *testing.T) {
	m := dynamicpb.NewMessage(samplePayloadDescriptor(t))
	m.Set(m.Descriptor().Fields().ByName("content"), protoString("c"))
	resp := newGRPCResponse(metadata.Pairs("a", "1"), metadata.Pairs("b", "2"), []proto.Message{m, nil}, 2, 1)
	if len(resp.ResponsePayload) != 1 || decode(t, resp.ResponsePayload[0])["content"] != "c" {
		t.Errorf("payloads = %v, nil messages are skipped", resp.ResponsePayload)
	}
	if resp.ResponseHeaders["a"][0] != "1" || resp.ResponseTrailers["b"][0] != "2" || resp.ClientStreamCount != 2 || resp.ServerStreamCount != 1 {
		t.Errorf("response = %+v", resp)
	}
}

func TestProcessResponseStatus(t *testing.T) {
	m := &gotogrpcMethodForTest
	r := &GRPCResponse{}
	if !processResponseStatus(m, r, nil) || r.Status != 0 || r.EquivalentHTTPStatusCode != http.StatusOK {
		t.Errorf("no error: ok status %d http %d", r.Status, r.EquivalentHTTPStatusCode)
	}
	r = &GRPCResponse{}
	if !processResponseStatus(m, r, status.Error(codes.PermissionDenied, "no")) || r.Status != int(codes.PermissionDenied) {
		t.Errorf("gRPC status error: status %d", r.Status)
	}
	r = &GRPCResponse{}
	if processResponseStatus(m, r, errors.New("plain error")) {
		t.Errorf("an error that is not a gRPC status is not a response")
	}
	if r.Status != int(codes.Unknown) {
		t.Errorf("status of a plain error = %d, want Unknown (%d)", r.Status, int(codes.Unknown))
	}
}

func TestPayloadCountAndProcess(t *testing.T) {
	cases := []struct {
		name string
		p    *GRPCPayload
		want int
	}{
		{"empty", &GRPCPayload{}, 0},
		{"payload", &GRPCPayload{Payload: "{}"}, 1},
		{"stream", &GRPCPayload{Stream: []string{"{}", "{}"}}, 1},
		{"linear", &GRPCPayload{Linear: []*GRPCPayload{{Payload: "{}"}, {Payload: "{}"}}}, 2},
	}
	for _, tc := range cases {
		if got := tc.p.Count(); got != tc.want {
			t.Errorf("%s: Count = %d, want %d", tc.name, got, tc.want)
		}
	}

	var lock sync.Mutex
	var unary, stream []string
	uCall := func(md metadata.MD, payload []byte, wg *sync.WaitGroup) {
		if wg != nil {
			defer wg.Done()
		}
		lock.Lock()
		defer lock.Unlock()
		unary = append(unary, string(payload)+"|"+join(md.Get("h")))
	}
	sCall := func(md metadata.MD, payload [][]byte, wg *sync.WaitGroup) {
		if wg != nil {
			defer wg.Done()
		}
		lock.Lock()
		defer lock.Unlock()
		s := ""
		for _, p := range payload {
			s += string(p) + ";"
		}
		stream = append(stream, s+"|"+join(md.Get("h")))
	}
	call := &GRPCCall{Headers: types.NewHeaders()}

	// a payload with headers: the headers apply to it and to its linear payloads, and don't leak to the siblings
	base := metadata.Pairs("h", "base")
	p := &GRPCPayloads{Linear: []*GRPCPayload{
		{Payload: "a", Headers: addHeaders("h", "own")},
		{Payload: "b"},
		{Linear: []*GRPCPayload{{Payload: "c"}, {Payload: "d"}}, Headers: addHeaders("h", "grp")},
		{Stream: []string{"s1", "s2"}},
	}}
	p.Process(call, base, uCall, sCall)
	if len(unary) != 4 || len(stream) != 1 {
		t.Fatalf("calls: unary %v stream %v", unary, stream)
	}
	if unary[0] != "a|base,own" || unary[1] != "b|base" || unary[2] != "c|base,grp" || unary[3] != "d|base,grp" {
		t.Errorf("unary calls = %v", unary)
	}
	if stream[0] != "s1;s2;|base" {
		t.Errorf("stream calls = %v", stream)
	}
	if got := base.Get("h"); len(got) != 1 || got[0] != "base" {
		t.Errorf("the metadata of the caller must not be changed by the payload headers: %v", got)
	}

	// a plain payload goes to the stream handler as a stream of one, when there is no unary handler
	stream = nil
	(&GRPCPayloads{Linear: []*GRPCPayload{{Payload: "only"}}}).Process(call, base, nil, sCall)
	if len(stream) != 1 || stream[0] != "only;|base" {
		t.Errorf("single payload as stream: %v", stream)
	}

	// concurrent payloads all run, and the call returns after the last one
	unary = nil
	conc := &GRPCPayloads{Concurrent: []*GRPCPayload{
		{Payload: "1"}, {Payload: "2"}, {Linear: []*GRPCPayload{{Payload: "3"}, {Payload: "4"}}},
	}}
	within(t, 10*time.Second, "concurrent Process", func() { conc.Process(call, base, uCall, nil) })
	sort.Strings(unary)
	if len(unary) != 4 || unary[0] != "1|base" || unary[3] != "4|base" {
		t.Errorf("concurrent unary calls = %v", unary)
	}

	// no payloads: nothing is called
	unary, stream = nil, nil
	(&GRPCPayloads{}).Process(call, base, uCall, sCall)
	if len(unary) != 0 || len(stream) != 0 {
		t.Errorf("empty payloads called handlers")
	}
}

func TestCallUpdateHeaders(t *testing.T) {
	call := &GRPCCall{Headers: &types.Headers{Request: &types.HeadersConfig{
		Add:     map[string]string{"a": "1", "gone": "x"},
		Remove:  []string{"gone"},
		Forward: []string{"f"},
	}}}
	src := http.Header{}
	src.Set("F", "fv")
	call.RequestHeaders = src
	h := types.SimpleHTTPHeaders{"gone": "x"}
	call.UpdateHeaders(h)
	if h["a"] != "1" || h["f"] != "fv" {
		t.Errorf("headers = %v", h)
	}
	// no request header config: nothing to do
	(&GRPCCall{Headers: &types.Headers{}}).UpdateHeaders(h)

	// stream headers are those of the payload
	md := types.MD(metadata.MD{})
	call.UpdateStreamHeaders(md, &GRPCPayload{Headers: &types.Headers{Request: &types.HeadersConfig{
		Add: map[string]string{"p": "pv"}, Forward: []string{"f"},
	}}})
	if md["p"][0] != "pv" || md["f"][0] != "fv" {
		t.Errorf("stream headers = %v", md)
	}
	call.UpdateStreamHeaders(md, &GRPCPayload{})
	call.UpdateStreamHeaders(md, &GRPCPayload{Headers: &types.Headers{}})
	if len(md) != 2 {
		t.Errorf("payloads without request headers must not change the headers: %v", md)
	}
}

func TestInvokeConcurrentCallsOnOneClient(t *testing.T) {
	e := newEnv(t)
	c := newClient(t, e, nil)
	const workers = 8
	var wg sync.WaitGroup
	results := make([]*GRPCResult, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			method := []string{"oneOne", "oneMany", "manyOne", "manyMany"}[i%4]
			var payloads *GRPCPayloads
			switch method {
			case "oneOne":
				payloads = samplePayloads("w")
			case "oneMany":
				payloads = &GRPCPayloads{Linear: []*GRPCPayload{{Payload: `{"id":2,"content":"w"}`}}}
			default:
				payloads = &GRPCPayloads{Linear: []*GRPCPayload{{Stream: []string{`{"id":1,"content":"a"}`, `{"id":2,"content":"b"}`}}}}
			}
			results[i] = c.Invoke(sampleCall(e, method, payloads), nil)
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatalf("concurrent invocations did not finish")
	}
	for i, r := range results {
		if r == nil || len(r.Errors) != 0 || len(r.Responses) != 1 {
			t.Errorf("call %d: %+v", i, r)
		}
	}
	for _, method := range []string{"oneOne", "oneMany", "manyOne", "manyMany"} {
		if n := len(e.sample.recorded(method)); n != 2 {
			t.Errorf("server calls of %s = %d, want 2", method, n)
		}
	}
}

func TestInvokeKeepOpenHoldsClientStream(t *testing.T) {
	e := newEnv(t)
	c := newClient(t, e, &GRPCOptions{KeepOpen: 400 * time.Millisecond})
	payloads := &GRPCPayloads{Linear: []*GRPCPayload{{Stream: []string{`{"content":"a"}`, `{"content":"b"}`}}}}
	start := time.Now()
	r := invoke(t, c, sampleCall(e, "manyOne", payloads), nil)
	if len(r.Errors) != 0 || len(r.Responses) != 1 {
		t.Fatalf("errors %v responses %d", r.Errors, len(r.Responses))
	}
	if took := time.Since(start); took < 350*time.Millisecond {
		t.Errorf("the stream must be kept open for the time given, call took %s", took)
	}
}
