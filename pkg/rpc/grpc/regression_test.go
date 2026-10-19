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
	"fmt"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// Self must be the stream that it's called on, not the base stream that it embeds.
func TestRegressionSelfIsTheStreamItself(t *testing.T) {
	e := newServerEnv(t, "Bidi")
	if e.stream.Self() != e.stream || e.stream.Self().Type() != "GRPCServerStream" {
		t.Errorf("Self of a server stream is a %s", e.stream.Self().Type())
	}
	c := newClientEnv(t, "Bidi")
	s := c.bidiStream(t)
	if s.Self() != s || s.Self().Type() != "GRPCClientStream" {
		t.Errorf("Self of a client stream is a %s", s.Self().Type())
	}
}

// SendMulti of a client stream dropped the number of messages that it sent.
func TestRegressionClientStreamSendMultiReportsTheSendCount(t *testing.T) {
	c := newClientEnv(t, "ClientStream")
	_, sent, err := c.clientStream(t).SendMulti([]proto.Message{echoMessage("a"), echoMessage("b"), echoMessage("c")})
	if err != nil || sent != 3 {
		t.Errorf("SendMulti = %d sent, %v, want 3 sent", sent, err)
	}
	_, sent, err = c.clientStream(t).SendPayloads([][]byte{[]byte(`{"text":"a"}`), []byte(`{"text":"b"}`)})
	if err != nil || sent != 2 {
		t.Errorf("SendPayloads = %d sent, %v, want 2 sent", sent, err)
	}
}

// A message that couldn't be sent was reported as success: the error of the send was replaced with the result of closing
// the stream, which is a success for a server stream.
func TestRegressionSendErrorIsNotReplacedByTheResultOfClose(t *testing.T) {
	broken := errors.New("broken")

	t.Run("SendMulti", func(t *testing.T) {
		e := newServerEnv(t, "ServerStream")
		e.ss.sendErr, e.ss.failAt = broken, 3
		_, sent, err := e.stream.SendMulti([]proto.Message{echoMessage("a"), echoMessage("b"), echoMessage("c"), echoMessage("d")})
		if err != broken || sent != 2 {
			t.Errorf("SendMulti = %d sent, err %v, want 2 sent and the error of the stream", sent, err)
		}
		if got := e.ss.sentTexts(); !reflect.DeepEqual(got, []string{"a", "b"}) {
			t.Errorf("sent %v, want nothing after the error", got)
		}
	})
	t.Run("TeeStreamSend", func(t *testing.T) {
		e := newServerEnv(t, "Bidi")
		e.ss.sendErr = broken
		if sent, err := e.stream.TeeStreamSend(nil, feedChan("a", "b"), nil); err != broken || sent != 0 {
			t.Errorf("TeeStreamSend = %d sent, err %v, want none sent and the error of the stream", sent, err)
		}
	})
	t.Run("AsyncSendReceive", func(t *testing.T) {
		e := newServerEnv(t, "Bidi")
		e.ss.feed(true)
		e.ss.sendErr = broken
		if _, _, _, err := e.stream.AsyncSendReceive([]proto.Message{echoMessage("a")}); err != broken {
			t.Errorf("err = %v, want the error of the stream", err)
		}
	})
	t.Run("ChainedSendReceive", func(t *testing.T) {
		e := newServerEnv(t, "Bidi")
		e.ss.feed(true, "a")
		e.ss.sendErr = broken
		if _, _, err := e.stream.ChainedSendReceive(make(chan proto.Message, 10), feedChan("x", "y")); err != broken {
			t.Errorf("err = %v, want the error of the stream", err)
		}
	})
	t.Run("ChainedTeeStream", func(t *testing.T) {
		e := newServerEnv(t, "Bidi")
		e.ss.feed(true, "a")
		e.ss.sendErr = broken
		in, teeIn, teeOut := make(chan proto.Message, 10), make(chan proto.Message, 10), make(chan proto.Message, 10)
		if _, _, err := e.stream.ChainedTeeStream(in, feedChan("x"), teeIn, teeOut); err != broken {
			t.Errorf("err = %v, want the error of the stream", err)
		}
	})
}

// An error of the receiver that wasn't the end of the stream was dropped: the receiver stopped and reported success.
func TestRegressionReceiveErrorIsReported(t *testing.T) {
	t.Run("server stream", func(t *testing.T) {
		e := newServerEnv(t, "Bidi")
		e.ss.recvErr = status.Error(codes.Canceled, "client went away")
		e.ss.feed(true, "a", "b")
		count, err := e.stream.TeeStreamReceive(make(chan proto.Message, 10), nil)
		if status.Code(err) != codes.Canceled || count != 2 {
			t.Errorf("TeeStreamReceive = %d received, err %v, want 2 received and the status of the stream", count, err)
		}
	})
	t.Run("ReceiveAndDiscard", func(t *testing.T) {
		e := newServerEnv(t, "ClientStream")
		e.ss.recvErr = status.Error(codes.DataLoss, "lost")
		e.ss.feed(true, "a")
		if count, err := e.stream.ReceiveAndDiscard(); status.Code(err) != codes.DataLoss || count != 1 {
			t.Errorf("ReceiveAndDiscard = %d received, err %v, want 1 received and the status of the stream", count, err)
		}
	})
	t.Run("AsyncSendReceive of a client", func(t *testing.T) {
		c := newClientEnv(t, "Bidi")
		_, _, _, err := c.bidiStream(t).AsyncSendReceive([]proto.Message{echoMessage("a"), echoMessage("fail"), echoMessage("b")})
		if status.Code(err) != codes.Aborted {
			t.Errorf("err = %v, want the status that the server ended the call with", err)
		}
	})
	t.Run("end of stream isn't an error", func(t *testing.T) {
		e := newServerEnv(t, "Bidi")
		e.ss.feed(true, "a")
		if count, err := e.stream.TeeStreamReceive(make(chan proto.Message, 10), nil); err != nil || count != 1 {
			t.Errorf("TeeStreamReceive = %d received, err %v, want 1 received and no error", count, err)
		}
	})
}

// The stream constructors were given streams that don't exist, and made streams that panic when used.
func TestRegressionStreamConstructorsRefuseMissingInputs(t *testing.T) {
	stubHandlers(t)
	m := newRegistry().NewGRPCService(echoDesc()).Methods["Bidi"]
	if s := NewGRPCStreamForClient(1, nil, nil, nil, nil); s != nil {
		t.Errorf("a client stream without a method must be nil, got %v", s)
	}
	if s := NewServerStream(1, nil, newFakeServerStream(context.Background()), nil); s != nil {
		t.Errorf("a server stream without a method must be nil, got %v", s)
	}
	if s := NewServerStream(1, m, nil, nil); s != nil {
		t.Errorf("a server stream without a gRPC stream must be nil, got %v", s)
	}
}

// The calls of a stream that does the sending and receiving in two goroutines gave the results from them with nothing
// ordering the assignments before the reads. Run with -race.
func TestRegressionStreamResultsAreSafeToRead(t *testing.T) {
	for i := 0; i < 20; i++ {
		e := newServerEnv(t, "Bidi")
		e.ss.feed(true, "a", "b", "c")
		in := make(chan proto.Message, 10)
		received, sent, err := e.stream.ChainedSendReceive(in, feedChan("x", "y"))
		if err != nil || received != 3 || sent != 2 {
			t.Fatalf("round %d: ChainedSendReceive = %d %d %v, want 3 2", i, received, sent, err)
		}
		e2 := newServerEnv(t, "Bidi")
		e2.ss.feed(true, "a")
		if _, _, _, err := e2.stream.AsyncSendReceive([]proto.Message{echoMessage("a")}); err != nil {
			t.Fatalf("round %d: AsyncSendReceive: %v", i, err)
		}
		e3 := newServerEnv(t, "Bidi")
		e3.ss.feed(true, "a")
		if _, _, err := e3.stream.ChainedTeeStream(make(chan proto.Message, 10), feedChan("x"), make(chan proto.Message, 10), make(chan proto.Message, 10)); err != nil {
			t.Fatalf("round %d: ChainedTeeStream: %v", i, err)
		}
	}
}

// Cloning a service copied the descriptors and the methods by value, which left the clone's handlers, the slices and the
// pointers into the descriptors shared with (or dangling from) the source, so that changing the clone changed the source.
func TestRegressionCloneServiceDoesNotShareStateWithTheSource(t *testing.T) {
	stubHandlers(t)
	r := newRegistry()
	sd, psd := healthDesc()
	sd.Metadata = psd
	// handlers that tell the source from what the clone makes of them
	sourceHandler := func(any, context.Context, func(any) error, grpc.UnaryServerInterceptor) (any, error) {
		return "source", nil
	}
	sourceStream := func(any, grpc.ServerStream) error { return errors.New("source stream") }
	for i := range sd.Methods {
		sd.Methods[i].Handler = sourceHandler
	}
	for i := range sd.Streams {
		sd.Streams[i].Handler = sourceStream
	}
	source := r.NewGRPCServiceFromDescriptors(sd, psd)

	clone, err := r.CloneService(source, "clone.Health")
	if err != nil || clone == nil {
		t.Fatalf("CloneService = %v %v", clone, err)
	}
	for i := range source.GSD.Methods {
		if got, _ := source.GSD.Methods[i].Handler(nil, nil, nil, nil); got != "source" {
			t.Errorf("handler %d of the source answers %v after cloning, want it unchanged", i, got)
		}
	}
	if err := source.GSD.Streams[0].Handler(nil, nil); err == nil || err.Error() != "source stream" {
		t.Errorf("stream handler of the source was replaced by the clone, got %v", err)
	}
	if source.GSD.ServiceName != healthService || source.Name != healthService {
		t.Errorf("the source was renamed to %q %q", source.GSD.ServiceName, source.Name)
	}
	if got, _ := clone.GSD.Methods[0].Handler(nil, nil, nil, nil); got != "/clone.health/check" {
		t.Errorf("handler of the clone answers for %v", got)
	}
	clone.GSD.Methods[0].MethodName = "Changed"
	if source.GSD.Methods[0].MethodName != "Check" {
		t.Errorf("the method descs of the clone are the ones of the source")
	}
	if source.Methods["Check"].GMD != &source.GSD.Methods[0] {
		t.Errorf("the methods of the source must keep pointing into the service desc of the source")
	}
	// a service that can't be cloned gives nil, not a half made service
	if got, err := r.CloneService(&GRPCService{Name: "x"}, "y.Z"); err == nil || got != nil {
		t.Errorf("CloneService of a service without descriptors = %v %v, want an error and nil", got, err)
	}
}

// A service whose descriptor can't be loaded was given back as an empty service that the callers went on with.
func TestRegressionUnknownServicesAreNil(t *testing.T) {
	stubHandlers(t)
	r := newRegistry()
	sd, _ := healthDesc()
	sd.ServiceName = "no.such.Service"
	if got := r.GetOrCreateService(sd); got != nil {
		t.Errorf("GetOrCreateService of an unknown service = %+v, want nil", got)
	}
	if got := r.GetRPCService("no.such.Service"); got != nil {
		t.Errorf("GetRPCService of an unknown service is not a nil interface: %#v", got)
	}
	if got := r.GetRPCService(""); got != nil {
		t.Errorf("GetRPCService of no name is not a nil interface: %#v", got)
	}
}

// CommonHandler asserted that the local address of the peer is a TCP address, and panicked on connections of any other
// kind or without a local address.
func TestRegressionCommonHandlerDoesNotPanicOnOtherAddresses(t *testing.T) {
	registerHealthInGlobalRegistry(t)
	const method = "/grpc.health.v1.Health/Check"
	md := metadata.Pairs("x-a", "1")
	tests := map[string]net.Addr{
		"unix":      &net.UnixAddr{Name: "/tmp/x", Net: "unix"},
		"no local":  nil,
		"in memory": pipeAddr{},
	}
	for name, local := range tests {
		t.Run(name, func(t *testing.T) {
			ctx := commonHandlerContext(local, &net.TCPAddr{Port: 1}, method, md)
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("CommonHandler panicked: %v", r)
				}
			}()
			if _, _, _, _, _, _, err := CommonHandler(ctx, nil); err == nil {
				t.Logf("accepted, with the port taken from the context")
			}
		})
	}
}

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }

// A stream response without responses sent the response at an index that doesn't exist.
func TestRegressionSendStreamResponseWithoutResponses(t *testing.T) {
	stubHandlers(t)
	for _, responses := range [][]proto.Message{nil, {}} {
		ss := newFakeServerStream(context.Background())
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("SendStreamResponse panicked on %d responses: %v", len(responses), r)
				}
			}()
			if err := SendStreamResponse(nil, ss, responses, 0, 3); err != nil || len(ss.sent) != 0 {
				t.Errorf("SendStreamResponse = %v with %d sent, want nothing to send and no error", err, len(ss.sent))
			}
		}()
	}
}

// The registry and the services were marshaled while they were registered and changed. Run with -race.
func TestRegressionMarshalWhileRegisteringAndChanging(t *testing.T) {
	stubHandlers(t)
	r := newRegistry()
	_, psd := healthDesc()
	s := r.NewGRPCService(psd)
	const workers, rounds = 4, 50

	var wg sync.WaitGroup
	errs := make(chan error, workers*4)
	run := func(f func(i int) error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				if err := f(i); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	for w := 0; w < workers; w++ {
		w := w
		run(func(i int) error {
			s.Methods["Check"].SetStreamCount(i)
			s.Methods["Check"].SetStreamDelay(time.Duration(i), time.Duration(i), i)
			s.Methods["Check"].SetResponsePayload([]byte(fmt.Sprintf(`{"w":%d}`, w)))
			return nil
		})
		run(func(i int) error {
			r.AddActiveService(s)
			r.AddProxyService(s, s, 10000+w)
			r.RemoveActiveService(healthService)
			r.RemoveTeeService(10000 + w)
			return nil
		})
		run(func(int) error {
			if _, err := json.Marshal(r); err != nil {
				return fmt.Errorf("marshal registry: %w", err)
			}
			if _, err := json.Marshal(s); err != nil {
				return fmt.Errorf("marshal service: %w", err)
			}
			return nil
		})
		run(func(int) error {
			s.Methods["Check"].GetStreamConfig()
			s.Methods["Check"].GetResponsePayload()
			r.GetRPCService(healthService)
			return nil
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
