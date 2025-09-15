package grpc

import (
	"context"
	"errors"
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/types"
	"goto/pkg/util"
	"io"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jhump/protoreflect/v2/grpcdynamic"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/dynamicpb"
)

const EnableDebugHook bool = false

type GRPCStream interface {
	Self() GRPCStream
	Type() string
	Label() string
	HasStream() bool
	IsClientStreaming() bool
	IsServerStreaming() bool
	Context() context.Context
	KeepOpen(time.Duration)
	Wait()
	Close() (proto.Message, error)
	Method() *GRPCServiceMethod
	SetDelay(delayMin, delayMax time.Duration, delayCount int)
	Headers() (metadata.MD, error)
	Trailers() metadata.MD
	SendHeaders(metadata.MD) error
	SendChainHeaders(GRPCStream, HeadersHookFunc, bool) error
	Receive() (proto.Message, error)
	TeeStreamReceive(in, tee chan proto.Message) (int, error)
	ReceiveAndDiscard() (int, error)
	Send(proto.Message) error
	SendMulti([]proto.Message) (proto.Message, int, error)
	SendPayloads([][]byte) (proto.Message, int, error)
	SendJSONs([]any) (proto.Message, int, error)
	TeeStreamSend(in, out, tee chan proto.Message) (int, error)
	AsyncSendReceive(inputs []proto.Message) (outputs []proto.Message, respHeaders, respTrailers metadata.MD, err error)
	AsyncSendReceiveRaw(inputs [][]byte) (outputs [][]byte, respHeaders, respTrailers map[string][]string, err error)
	ChainedSendReceive(in, out chan proto.Message) (int, int, error)
	ChainedTeeStream(in, out, teeIn, teeOut chan proto.Message) (int, int, error)
	ChainInOut(hook HookFunc, headersHook HeadersHookFunc) (int, int, error)
	CrossHook(upstream GRPCStream, hook1, hook2 HookFunc, headersHook1, headersHook2 HeadersHookFunc) (int, int, error)
}

type HookFunc func(proto.Message) (metadata.MD, []proto.Message, error)
type HeadersHookFunc func(metadata.MD) (metadata.MD, error)

type GRPCBaseStream struct {
	self        GRPCStream
	label       string
	ctx         context.Context
	port        int
	method      *GRPCServiceMethod
	keepOpen    time.Duration
	headerFunc  func() (metadata.MD, error)
	trailerFunc func() metadata.MD
	hasStream   bool
	receiveDone chan bool
	sendDone    chan bool
	delayMin    time.Duration
	delayMax    time.Duration
	delayCount  int
	wg          *sync.WaitGroup
	tracker     *GRPCStreamTracker
}

type GRPCClientStream struct {
	GRPCBaseStream
	cStream    *grpcdynamic.ClientStream
	sStream    *grpcdynamic.ServerStream
	bidiStream *grpcdynamic.BidiStream
}

type GRPCServerStream struct {
	GRPCBaseStream
	stream grpc.ServerStream
}

type StreamHook struct {
	label       string
	context     context.Context
	hook        HookFunc
	in          chan proto.Message
	out         chan proto.Message
	link        chan []proto.Message
	output      []proto.Message
	isInStream  bool
	isOutStream bool
	headersSent bool
	debugout    chan proto.Message
}

type GRPCStreamTracker struct {
	RequestCount  int             `json:"requestCount"`
	ResponseCount int             `json:"responseCount"`
	MessageLog    []proto.Message `json:"messageLog"`
}

func init() {
	global.Flags.EnableGRPCDebugLogs = true
}

func NewGRPCStreamForClient(port int, method *GRPCServiceMethod, cs *grpcdynamic.ClientStream, ss *grpcdynamic.ServerStream, bs *grpcdynamic.BidiStream) GRPCStream {
	if method == nil && cs == nil && ss == nil && bs == nil {
		return nil
	}
	stream := &GRPCClientStream{
		GRPCBaseStream: GRPCBaseStream{
			label:       fmt.Sprintf("%s[client]", method.URI),
			port:        port,
			method:      method,
			receiveDone: make(chan bool, 10),
			sendDone:    make(chan bool, 10),
			tracker:     &GRPCStreamTracker{},
		},
		cStream:    cs,
		sStream:    ss,
		bidiStream: bs,
	}
	if cs != nil {
		stream.hasStream = true
		stream.ctx = cs.Context()
		stream.headerFunc = cs.Header
		stream.trailerFunc = cs.Trailer
	} else if ss != nil {
		stream.hasStream = true
		stream.ctx = ss.Context()
		stream.headerFunc = ss.Header
		stream.trailerFunc = ss.Trailer
	} else if bs != nil {
		stream.hasStream = true
		stream.ctx = bs.Context()
		stream.headerFunc = bs.Header
		stream.trailerFunc = bs.Trailer
	}
	stream.self = stream
	return stream
}

func NewServerStream(port int, method *GRPCServiceMethod, ss grpc.ServerStream, cancelFunc context.CancelFunc) GRPCStream {
	if method == nil && ss == nil {
		return nil
	}
	stream := &GRPCServerStream{
		GRPCBaseStream: GRPCBaseStream{
			label:       fmt.Sprintf("%s[server]", method.URI),
			port:        port,
			ctx:         ss.Context(),
			hasStream:   true,
			method:      method,
			receiveDone: make(chan bool, 10),
			sendDone:    make(chan bool, 10),
			tracker:     &GRPCStreamTracker{},
		},
		stream: ss,
	}
	stream.headerFunc = stream.headerFromContext
	stream.self = stream
	return stream
}

func (s *GRPCBaseStream) Init() {
	s.receiveDone = make(chan bool, 10)
	s.sendDone = make(chan bool, 10)
	s.wg = &sync.WaitGroup{}
	s.wg.Add(2)
}

func (s *GRPCBaseStream) Self() GRPCStream {
	return s
}

func (s *GRPCBaseStream) Type() string {
	return "GRPCBaseStream"
}

func (s *GRPCBaseStream) Label() string {
	return s.label
}

func (s *GRPCBaseStream) HasStream() bool {
	return s.hasStream
}

func (s *GRPCBaseStream) IsClientStreaming() bool {
	return s.method.IsClientStream
}

func (s *GRPCBaseStream) IsServerStreaming() bool {
	return s.method.IsServerStream
}

func (s *GRPCBaseStream) Context() context.Context {
	return s.ctx
}

func (s *GRPCBaseStream) KeepOpen(d time.Duration) {
	s.keepOpen = d
}

func (s *GRPCBaseStream) Wait() {
	if s.wg != nil {
		s.wg.Wait()
	}
}

func (s *GRPCBaseStream) Close() (proto.Message, error) {
	return nil, nil
}

func (s *GRPCBaseStream) Method() *GRPCServiceMethod {
	return s.method
}

func (s *GRPCBaseStream) SetDelay(delayMin, delayMax time.Duration, delayCount int) {
	s.delayMin = delayMin
	s.delayMax = delayMax
	s.delayCount = delayCount
}

func (s *GRPCBaseStream) applyDelay() {
	if (s.delayMin > 0 || s.delayMax > 0) && (s.delayCount > 0 || s.delayCount == -1) {
		time.Sleep(types.RandomDuration(s.delayMin, s.delayMax))
		if s.delayCount > 0 {
			s.delayCount--
		}
	}
}

func (s *GRPCBaseStream) Headers() (metadata.MD, error) {
	if s.headerFunc == nil {
		return nil, errors.New("no stream")
	}
	return s.headerFunc()
}

func (s *GRPCBaseStream) headerFromContext() (metadata.MD, error) {
	if s.ctx == nil {
		return nil, errors.New("no stream")
	}
	if md, ok := metadata.FromIncomingContext(s.ctx); ok {
		return md, nil
	}
	return nil, errors.New("failed to get headers from context")
}

func (s *GRPCBaseStream) Trailers() metadata.MD {
	if s.trailerFunc != nil {
		return s.trailerFunc()
	}
	return nil
}

func (s *GRPCBaseStream) SendHeaders(md metadata.MD) error {
	return grpc.SendHeader(s.ctx, md)
}

func (s *GRPCBaseStream) SendChainHeaders(other GRPCStream, headersHook HeadersHookFunc, addProxyHeaders bool) (err error) {
	respHeaders, err := s.Headers()
	if err != nil {
		return
	}
	if headersHook != nil {
		respHeaders, err = headersHook(respHeaders)
		if err != nil {
			return
		}
	}
	if addProxyHeaders {
		if respHeaders == nil {
			respHeaders = metadata.MD{}
		}
		util.AddHeaderWithPrefixL("Proxy-", constants.HeaderGotoHost, global.Funcs.GetHostLabelForPort(s.port), respHeaders)
		util.AddHeaderWithPrefixL("Proxy-", constants.HeaderGotoPort, strconv.Itoa(s.port), respHeaders)
		util.AddHeaderWithPrefixL("Proxy-", constants.HeaderViaGoto, global.Funcs.GetListenerLabelForPort(s.port), respHeaders)
		respHeaders.Append(constants.HeaderViaGoto, global.Funcs.GetListenerLabelForPort(s.port))
	}
	if other != nil {
		err = other.SendHeaders(respHeaders)
	}
	return
}

func (s *GRPCBaseStream) Receive() (proto.Message, error) {
	return nil, nil
}

func (s *GRPCBaseStream) TeeStreamReceive(in, tee chan proto.Message) (int, error) {
	return 0, nil
}

func (s *GRPCBaseStream) ReceiveAndDiscard() (int, error) {
	return s.internalReceiveMulti(nil, nil, nil, false)
}

func (s *GRPCBaseStream) handleReceivedMessage(msg proto.Message, in, tee chan proto.Message, tracker func(), isOutput bool) {
	selfType := s.self.Type()
	if !isOutput && s.method.In != nil {
		msg = s.method.In(msg)
	} else if isOutput && s.method.Out != nil {
		msg = s.method.Out(msg)
	}
	if tracker != nil {
		tracker()
	}
	if in != nil {
		if global.Flags.EnableGRPCDebugLogs {
			log.Printf("%s.handleReceivedMessage: [%s] [INFO] Sending message to IN channel.\n", selfType, s.label)
		}
		in <- msg
	}
	if tee != nil {
		if global.Flags.EnableGRPCDebugLogs {
			log.Printf("%s.handleReceivedMessage: [%s] [INFO] Sending message to TEE channel.\n", selfType, s.label)
		}
		tee <- msg
	}
}

func (s *GRPCBaseStream) debugReceive(msg proto.Message) {
	log.Printf("%s.debugReceive: [%s] [%+v].\n", s.self.Type(), s.label, msg)
	if s.self.Type() == "GRPCClientStream" {
		log.Printf("%s debugReceive Client Break: [%s]\n", s.self.Type(), s.label)
	}
	if s.self.Type() == "GRPCServerStream" {
		log.Printf("%s debugReceive Server Break: [%s]\n", s.self.Type(), s.label)
	}
}

func (s *GRPCBaseStream) internalReceiveMulti(in, tee chan proto.Message, tracker func(), isOutput bool) (receiveCount int, err error) {
	selfType := s.self.Type()
	defer func() {
		if s.receiveDone != nil {
			select {
			case s.receiveDone <- true:
			default:
			}
			close(s.receiveDone)
		}
		if in != nil {
			close(in)
		}
		if tee != nil {
			close(tee)
		}
		if s.wg != nil {
			s.wg.Done()
		}
	}()
	if !s.hasStream {
		return 0, errors.New("no stream")
	}
	receiveCount = s.tracker.RequestCount
	if isOutput {
		receiveCount = s.tracker.ResponseCount
	}
	for {
		msg, e := s.self.Receive()
		if EnableDebugHook {
			s.debugReceive(msg)
		}
		if e == io.EOF {
			if global.Flags.EnableGRPCDebugLogs {
				log.Printf("%s.internalReceiveMulti: [%s] [INFO] Stream closed with %d messages received\n", selfType, s.label, receiveCount)
			}
			break
		} else if e != nil {
			if global.Flags.EnableGRPCDebugLogs {
				log.Printf("%s.internalReceiveMulti: [%s] [ERROR] %s\n", selfType, s.label, e.Error())
			}
			break
		} else if msg == nil {
			if global.Flags.EnableGRPCDebugLogs {
				log.Printf("%s.internalReceiveMulti: [%s] [INFO] Received NULL message", selfType, s.label)
			}
		} else {
			receiveCount++
			if global.Flags.EnableGRPCDebugLogs {
				log.Printf("%s.internalReceiveMulti: [%s] [INFO] Received message #%d [%+v]", selfType, s.label, receiveCount, msg)
			}
			if in != nil || tee != nil {
				s.handleReceivedMessage(msg, in, tee, tracker, isOutput)
			}
		}
	}
	if global.Flags.EnableGRPCDebugLogs {
		log.Printf("%s.internalReceiveMulti: [%s] [INFO] Receiver is done.\n", selfType, s.label)
	}
	return
}

func (s *GRPCBaseStream) Send(proto.Message) error {
	return nil
}

func (s *GRPCBaseStream) SendMulti(messages []proto.Message) (message proto.Message, sendCount int, err error) {
	return nil, 0, nil
}

func (s *GRPCBaseStream) SendPayloads(payloads [][]byte) (message proto.Message, sendCount int, err error) {
	return nil, 0, nil
}

func (s *GRPCBaseStream) SendJSONs([]any) (proto.Message, int, error) {
	return nil, 0, nil
}

func (s *GRPCBaseStream) TeeStreamSend(in, out, tee chan proto.Message) (int, error) {
	return 0, nil
}

func (s *GRPCBaseStream) debugSend(msg proto.Message) {
	log.Printf("%s.debugSend: [%s] [%+v].\n", s.self.Type(), s.label, msg)
	if s.self.Type() == "GRPCClientStream" {
		log.Printf("%s debugSend Client Break: [%s]\n", s.self.Type(), s.label)
	}
	if s.self.Type() == "GRPCServerStream" {
		log.Printf("%s debugSend Server Break: [%s]\n", s.self.Type(), s.label)
	}
}

func (s *GRPCBaseStream) internalSendMulti(messages []proto.Message, in, out, tee chan proto.Message, tracker func(), isOutput bool) (msg proto.Message, sendCount int, err error) {
	selfType := s.self.Type()
	defer func() {
		select {
		case s.sendDone <- true:
		default:
		}
		close(s.sendDone)
		if tee != nil {
			close(tee)
		}
		if s.wg != nil {
			s.wg.Done()
		}
	}()
	if !s.hasStream {
		return nil, 0, errors.New("no stream")
	}
	sendFunc := func(m proto.Message) error {
		if isOutput && s.method.Out != nil {
			m = s.method.Out(m)
		} else if !isOutput && s.method.In != nil {
			m = s.method.In(m)
		}
		if err = s.self.Send(m); err != nil {
			if global.Flags.EnableGRPCDebugLogs {
				log.Printf("%s.internalSendMulti: [%s] [ERROR] Error while sending to stream: %s\n", selfType, s.label, err.Error())
			}
			return err
		}
		sendCount++
		if global.Flags.EnableGRPCDebugLogs {
			log.Printf("%s.internalSendMulti: [%s] [INFO] Sent message #%d [%+v]\n", selfType, s.label, sendCount, m)
		}
		if tee != nil {
			if global.Flags.EnableGRPCDebugLogs {
				log.Printf("%s.internalSendMulti: [%s] [INFO] Sending to TEE channel \n", selfType, s.label)
			}
			tee <- m
		}
		tracker()
		return nil
	}
	startTime := time.Now()
	if messages != nil {
		for _, m := range messages {
			if global.Flags.EnableGRPCDebugLogs {
				log.Printf("%s.internalSendMulti: [%s] [INFO] Sending message [%+v]\n", selfType, s.label, m)
			}
			if err = sendFunc(m); err != nil {
				break
			}
		}
	} else if out != nil {
		for m := range out {
			if EnableDebugHook {
				s.debugSend(m)
			}
			if global.Flags.EnableGRPCDebugLogs {
				log.Printf("%s.internalSendMulti: [%s] [INFO] Read message from out channel [%+v]\n", selfType, s.label, m)
			}
			if err = sendFunc(m); err != nil {
				if global.Flags.EnableGRPCDebugLogs {
					log.Printf("%s.internalSendMulti: [%s] [ERROR] Failed to send message with error [%s]\n", selfType, s.label, err.Error())
				}
				break
			}
		}
		if global.Flags.EnableGRPCDebugLogs {
			log.Printf("%s.internalSendMulti: [%s] [INFO] Out channel closed. Sender closing stream.\n", selfType, s.label)
		}
	}
	msg, err = s.self.Close()
	if err != nil {
		if global.Flags.EnableGRPCDebugLogs {
			log.Printf("%s.internalSendMulti: [%s] [ERROR] Error while closing stream: %s\n", selfType, s.label, err.Error())
		}
	} else if msg == nil {
		if global.Flags.EnableGRPCDebugLogs {
			log.Printf("%s.internalSendMulti: [%s] [INFO] Received final message as null\n", selfType, s.label)
		}
	} else {
		if global.Flags.EnableGRPCDebugLogs {
			log.Printf("%s.internalSendMulti: [%s] [INFO] Read final message upon close [%+v]\n", selfType, s.label, msg)
		}
		s.handleReceivedMessage(msg, in, tee, tracker, true)
	}
	if s.keepOpen > 0 {
		sleep := s.keepOpen - time.Since(startTime)
		if sleep > 0 {
			log.Printf("%s.internalSendMulti: [%s] [INFO] Sender keeping stream open for %s.\n", selfType, s.label, sleep.String())
			time.Sleep(sleep)
		}
	}
	if global.Flags.EnableGRPCDebugLogs {
		log.Printf("%s.internalSendMulti: [%s] [INFO] Sender is done after %d messages.\n", selfType, s.label, sendCount)
	}
	return
}

func (s *GRPCBaseStream) AsyncSendReceive(outMessages []proto.Message) (inMessages []proto.Message, respHeaders, respTrailers metadata.MD, err error) {
	if !s.hasStream {
		err = errors.New("no stream")
		return
	}
	s.Init()
	var finalResponse proto.Message
	var sendError, recvError error
	go func() {
		finalResponse, _, sendError = s.self.SendMulti(outMessages)
	}()
	go func() {
		_, recvError = s.self.TeeStreamReceive(nil, nil)
	}()
	s.wg.Wait()

	respHeaders, err = s.self.Headers()
	respTrailers = s.self.Trailers()
	if sendError != nil {
		err = sendError
	} else if recvError != nil {
		err = recvError
	} else if finalResponse != nil {
		inMessages = append(inMessages, finalResponse)
	}
	return
}

func (s *GRPCBaseStream) AsyncSendReceiveRaw(outPayloads [][]byte) (inPayloads [][]byte, respHeaders, respTrailers map[string][]string, err error) {
	var messages []proto.Message
	messages, err = s.method.PayloadsToInputs(outPayloads)
	if err != nil {
		return
	}
	messages, respHeaders, respTrailers, err = s.self.AsyncSendReceive(messages)
	if err != nil {
		return
	}
	for _, msg := range messages {
		if b, e := protojson.Marshal(msg); e != nil {
			err = e
			return
		} else {
			inPayloads = append(inPayloads, b)
		}
	}
	return
}

func (s *GRPCBaseStream) ChainedSendReceive(in, out chan proto.Message) (receiveCount, sendCount int, err error) {
	if !s.hasStream {
		err = errors.New("no stream")
		return
	}
	s.Init()
	var sendError, recvError error
	go func() {
		receiveCount, recvError = s.self.TeeStreamReceive(in, nil)
	}()
	go func() {
		sendCount, sendError = s.self.TeeStreamSend(in, out, nil)
	}()
	s.wg.Wait()

	if sendError != nil {
		err = sendError
	} else if recvError != nil {
		err = recvError
	}
	return
}

func (s *GRPCBaseStream) ChainedTeeStream(in, out, teeIn, teeOut chan proto.Message) (receiveCount, sendCount int, err error) {
	if !s.hasStream {
		err = errors.New("no stream")
		return
	}
	s.Init()
	var sendError, recvError error

	go func() {
		receiveCount, recvError = s.self.TeeStreamReceive(in, teeIn)
	}()
	go func() {
		sendCount, sendError = s.self.TeeStreamSend(in, out, teeOut)
	}()
	s.wg.Wait()

	if sendError != nil {
		err = sendError
	} else if recvError != nil {
		err = recvError
	}
	return
}

func (s *GRPCBaseStream) ChainInOut(hook HookFunc, headersHook HeadersHookFunc) (receiveCount, sendCount int, err error) {
	s.Init()
	sh := NewStreamHook(s.label, s.ctx, hook, s.method.IsClientStream, s.method.IsServerStream)
	sh.Run()
	s.self.SendChainHeaders(nil, headersHook, false)
	return s.self.ChainedSendReceive(sh.in, sh.out)
}

func (s *GRPCBaseStream) CrossHook(upstream GRPCStream, hook1, hook2 HookFunc, headersHook1, headersHook2 HeadersHookFunc) (receiveCount, sendCount int, err error) {
	s.Init()
	downInUpOut := NewStreamHook(s.label+"-downup", s.ctx, hook1, s.method.IsClientStream, s.method.IsClientStream)
	upInDownOut := NewStreamHook(s.label+"-updown", s.ctx, hook2, s.method.IsServerStream, s.method.IsServerStream)
	downInUpOut.Run()
	upInDownOut.Run()
	var err1, err2 error

	wg := &sync.WaitGroup{}
	wg.Add(2)
	s.self.SendChainHeaders(upstream, headersHook1, false)
	go func() {
		//Receive from downstream into In chan, Read from out chan and send to downstream
		receiveCount, sendCount, err1 = s.self.ChainedSendReceive(downInUpOut.in, upInDownOut.out)
		wg.Done()
	}()
	go upstream.SendChainHeaders(s.self, headersHook2, true)
	go func() {
		//Receive from upstream into In chan, Read from out chan and send to upstream
		_, _, err2 = upstream.ChainedSendReceive(upInDownOut.in, downInUpOut.out)
		wg.Done()
	}()
	wg.Wait()
	err = errors.Join(err1, err2)
	return
}

func (s *GRPCClientStream) Context() (ctx context.Context) {
	if s.cStream != nil {
		ctx = s.cStream.Context()
	} else if s.sStream != nil {
		ctx = s.sStream.Context()
	} else if s.bidiStream != nil {
		ctx = s.bidiStream.Context()
	}
	return
}

func (s *GRPCClientStream) Receive() (message proto.Message, err error) {
	if !s.hasStream {
		return nil, errors.New("no stream")
	}
	s.tracker.IncrementRequestCount()
	if s.cStream != nil {
		<-s.sendDone
		message, err = s.cStream.CloseAndReceive()
	} else if s.sStream != nil {
		message, err = s.sStream.RecvMsg()
	} else if s.bidiStream != nil {
		message, err = s.bidiStream.RecvMsg()
	}
	if message != nil {
		s.tracker.IncrementResponseCount()
	}
	return
}

func (s *GRPCClientStream) Type() string {
	return "GRPCClientStream"
}

func (s *GRPCClientStream) TeeStreamReceive(in, tee chan proto.Message) (receiveCount int, err error) {
	return s.internalReceiveMulti(in, tee, s.tracker.IncrementResponseCount, true)
}

func (s *GRPCClientStream) Send(msg proto.Message) (err error) {
	if !s.hasStream {
		return errors.New("no stream")
	}
	s.applyDelay()
	if s.cStream != nil {
		err = s.cStream.SendMsg(msg)
	} else if s.bidiStream != nil {
		err = s.bidiStream.SendMsg(msg)
	}
	if err == nil {
		s.tracker.IncrementRequestCount()
	}
	return
}

func (s *GRPCClientStream) SendPayloads(payloads [][]byte) (message proto.Message, sendCount int, err error) {
	if !s.hasStream {
		return nil, 0, errors.New("no stream")
	}
	messages, err := s.method.PayloadsToInputs(payloads)
	if err != nil {
		return nil, 0, err
	}
	return s.SendMulti(messages)
}

func (s *GRPCClientStream) SendJSONs(jsons []any) (message proto.Message, sendCount int, err error) {
	if !s.hasStream {
		return nil, 0, errors.New("no stream")
	}
	messages, err := s.method.JSONsToInputs(jsons)
	if err != nil {
		return nil, 0, err
	}
	return s.SendMulti(messages)
}

func (s *GRPCClientStream) SendMulti(messages []proto.Message) (message proto.Message, sendCount int, err error) {
	message, _, err = s.internalSendMulti(messages, nil, nil, nil, s.tracker.IncrementRequestCount, false)
	return
}

func (s *GRPCClientStream) TeeStreamSend(in, out, tee chan proto.Message) (sendCount int, err error) {
	_, sendCount, err = s.internalSendMulti(nil, in, out, tee, s.tracker.IncrementRequestCount, false)
	return
}

func (s *GRPCClientStream) Close() (proto.Message, error) {
	if !s.hasStream {
		return nil, errors.New("no stream")
	}
	if s.cStream != nil {
		return s.cStream.CloseAndReceive()
	} else if s.bidiStream != nil {
		return nil, s.bidiStream.CloseSend()
	} else if s.sStream != nil {
		s.sStream.Context().Done()
		return nil, nil
	}
	return nil, errors.New("no stream")
}

func (s *GRPCServerStream) Type() string {
	return "GRPCServerStream"
}

func (s *GRPCServerStream) Context() (ctx context.Context) {
	if s.stream != nil {
		ctx = s.stream.Context()
	}
	return
}

func (s *GRPCServerStream) Receive() (message proto.Message, err error) {
	if !s.hasStream {
		return nil, errors.New("no stream")
	}
	if s.stream != nil {
		m := dynamicpb.NewMessage(s.method.InputType())
		err = s.stream.RecvMsg(m)
		message = m
	} else {
		err = errors.New("no stream")
	}
	if err == nil {
		s.tracker.IncrementRequestCount()
	}
	return
}

func (s *GRPCServerStream) TeeStreamReceive(in, tee chan proto.Message) (receiveCount int, err error) {
	return s.internalReceiveMulti(in, tee, s.tracker.IncrementRequestCount, false)
}

func (s *GRPCServerStream) Send(msg proto.Message) (err error) {
	if !s.hasStream {
		return errors.New("no stream")
	}
	s.applyDelay()
	if s.stream != nil {
		err = s.stream.SendMsg(msg)
	}
	if err == nil {
		s.tracker.IncrementResponseCount()
	}
	return
}

func (s *GRPCServerStream) SendMulti(messages []proto.Message) (message proto.Message, sendCount int, err error) {
	message, sendCount, err = s.internalSendMulti(messages, nil, nil, nil, s.tracker.IncrementResponseCount, true)
	return
}

func (s *GRPCServerStream) TeeStreamSend(in, out, tee chan proto.Message) (sendCount int, err error) {
	_, sendCount, err = s.internalSendMulti(nil, in, out, tee, s.tracker.IncrementResponseCount, true)
	return
}

func (s *GRPCServerStream) SendPayloads(payloads [][]byte) (output proto.Message, sendCount int, err error) {
	if !s.hasStream {
		return nil, 0, errors.New("no stream")
	}
	messages, err := s.method.PayloadsToOutputs(payloads)
	if err != nil {
		return nil, 0, err
	}
	return s.SendMulti(messages)
}

func (s *GRPCServerStream) SendJSONs(jsons []any) (output proto.Message, sendCount int, err error) {
	if !s.hasStream {
		return nil, 0, errors.New("no stream")
	}
	messages, err := s.method.JSONsToOutputs(jsons)
	if err != nil {
		return nil, 0, err
	}
	return s.SendMulti(messages)
}

func (s *GRPCServerStream) Close() (proto.Message, error) {
	if !s.hasStream {
		return nil, errors.New("no stream")
	}
	if s.stream != nil {
		s.stream.Context().Done()
		return nil, nil
	}
	return nil, errors.New("no stream")
}

func (t *GRPCStreamTracker) AddMessage(msg proto.Message) {
	t.MessageLog = append(t.MessageLog, msg)
}

func (t *GRPCStreamTracker) IncrementRequestCount() {
	t.RequestCount++
}

func (t *GRPCStreamTracker) IncrementResponseCount() {
	t.ResponseCount++
}

func NewStreamHook(label string, ctx context.Context, hook HookFunc, isInStream, isOutStream bool) *StreamHook {
	return &StreamHook{
		label:       label,
		context:     ctx,
		hook:        hook,
		isInStream:  isInStream,
		isOutStream: isOutStream,
		headersSent: false,
		in:          make(chan proto.Message, 10),
		out:         make(chan proto.Message, 10),
		debugout:    make(chan proto.Message, 10),
		link:        make(chan []proto.Message, 10),
	}
}

func (sh *StreamHook) Run() {
	go sh.sendOutput()
	go sh.readInput()
	if EnableDebugHook {
		go sh.debugSend()
	}
}

func (sh *StreamHook) debugSend() {
	for msg := range sh.debugout {
		log.Printf("StreamHook.debugSend: [%s] [%+v].\n", sh.label, msg)
		if strings.Contains(sh.label, "downup") {
			log.Printf("StreamHook.debugSend down-to-up: [%s] [%+v].\n", sh.label, msg)
		}
		if strings.Contains(sh.label, "updown") {
			log.Printf("StreamHook.debugSend up-to-down: [%s] [%+v].\n", sh.label, msg)
		}
		sh.out <- msg
	}
	close(sh.out)
}
func (sh *StreamHook) processMessage(input proto.Message) {
	if global.Flags.EnableGRPCDebugLogs {
		log.Printf("StreamHook: [%s] Invoking hook with input [%+v].\n", sh.label, input)
	}
	md, output, err := sh.hook(input)
	if err != nil {
		return
	}
	if len(output) > 0 && global.Flags.EnableGRPCDebugLogs {
		log.Printf("StreamHook: [%s] Hook returned %d outputs.\n", sh.label, len(output))
	}
	if md != nil && !sh.headersSent {
		if err := grpc.SetHeader(sh.context, md); err != nil {
			log.Printf("StreamHook: [%s] Failed to send headers with error [%s]\n", sh.label, err.Error())
		}
		sh.headersSent = true
	}
	if len(output) > 0 {
		if sh.isOutStream {
			if global.Flags.EnableGRPCDebugLogs {
				log.Printf("StreamHook: [%s] Sending hook output to link.\n", sh.label)
			}
			sh.link <- output
		} else if len(output) > 0 {
			if global.Flags.EnableGRPCDebugLogs {
				log.Printf("StreamHook: [%s] Retaining hook output for future flush.\n", sh.label)
			}
			sh.output = output
		}
	} else {
		log.Printf("StreamHook: [%s] No responses in hook output.\n", sh.label)
	}
}

func (sh *StreamHook) flushOut() {
	if len(sh.output) > 0 {
		if global.Flags.EnableGRPCDebugLogs {
			log.Printf("StreamHook: [%s] Flushing hook output to link.\n", sh.label)
		}
		sh.link <- sh.output
	} else {
		if global.Flags.EnableGRPCDebugLogs {
			log.Printf("StreamHook: [%s] No output retained for flushing to link.\n", sh.label)
		}
	}
	close(sh.link)
}

func (sh *StreamHook) sendOutput() {
	for responses := range sh.link {
		if global.Flags.EnableGRPCDebugLogs {
			log.Printf("StreamHook: [%s] Sending %d responses to out.\n", sh.label, len(responses))
		}
		for _, resp := range responses {
			if EnableDebugHook {
				sh.debugout <- resp
			} else {
				sh.out <- resp
			}
		}
	}
	if global.Flags.EnableGRPCDebugLogs {
		log.Printf("StreamHook: [%s] Hook link was closed, closing out channel.\n", sh.label)
	}
	if EnableDebugHook {
		close(sh.debugout)
	} else {
		close(sh.out)
	}
}

func (sh *StreamHook) readInput() {
	if sh.isInStream {
		if global.Flags.EnableGRPCDebugLogs {
			log.Printf("StreamHook: [%s] Reading stream from in channel.\n", sh.label)
		}
		for input := range sh.in {
			sh.processMessage(input)
		}
		if global.Flags.EnableGRPCDebugLogs {
			log.Printf("StreamHook: [%s] In stream channel was closed.\n", sh.label)
		}
	} else {
		if global.Flags.EnableGRPCDebugLogs {
			log.Printf("StreamHook: [%s] Reading single message from in channel.\n", sh.label)
		}
		req, ok := <-sh.in
		if ok {
			sh.processMessage(req)
		} else {
			if global.Flags.EnableGRPCDebugLogs {
				log.Printf("StreamHook: [%s] Single-messagae channel was closed.\n", sh.label)
			}
		}
	}
	sh.flushOut()
}

func IdentityHook(msg proto.Message) (metadata.MD, []proto.Message, error) {
	return nil, []proto.Message{msg}, nil
}

func IdentityHookWithDelay(delayFunc func() string) func(proto.Message) (metadata.MD, []proto.Message, error) {
	return func(msg proto.Message) (metadata.MD, []proto.Message, error) {
		if delayFunc != nil {
			delay := delayFunc()
			if global.Flags.EnableGRPCDebugLogs && delay != "" {
				log.Printf("[DEBUG] IdentityHookWithDelay: Delayed by [%s]\n", delay)
			}
		}
		return IdentityHook(msg)
	}
}

func IdentityHeadersHook(md metadata.MD) (metadata.MD, error) {
	return md.Copy(), nil
}

func doTee(tee chan proto.Message, msg proto.Message) (metadata.MD, []proto.Message, error) {
	if global.Flags.EnableGRPCDebugLogs {
		log.Printf("[DEBUG] TeeHook: Sending message to TEE channel: [%+v].\n", msg)
	}
	tee <- msg
	return nil, []proto.Message{msg}, nil
}

func TeeHook(tee chan proto.Message) func(proto.Message) (metadata.MD, []proto.Message, error) {
	return func(msg proto.Message) (metadata.MD, []proto.Message, error) {
		return doTee(tee, msg)
	}
}

func TeeHookWithDelay(tee chan proto.Message, delayFunc func() string) func(proto.Message) (metadata.MD, []proto.Message, error) {
	return func(msg proto.Message) (metadata.MD, []proto.Message, error) {
		if delayFunc != nil {
			delay := delayFunc()
			if global.Flags.EnableGRPCDebugLogs && delay != "" {
				log.Printf("[DEBUG] TeeHookWithDelay: Delayed by [%s]\n", delay)
			}
		}
		return doTee(tee, msg)
	}
}

func TeeHeadersHook(onHeaders func(md metadata.MD)) func(md metadata.MD) (metadata.MD, error) {
	return func(md metadata.MD) (metadata.MD, error) {
		if global.Flags.EnableGRPCDebugLogs {
			log.Printf("TeeHook: [INFO] Sending headers to TEE callback: [%+v].\n", md)
		}
		onHeaders(md.Copy())
		return md, nil
	}
}
