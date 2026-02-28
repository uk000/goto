/**
 * Copyright 2025 uk
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

package proxy

import (
	"context"
	"errors"
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/proxy/trackers"
	"goto/pkg/rpc"
	gotogrpc "goto/pkg/rpc/grpc"
	grpcclient "goto/pkg/rpc/grpc/client"
	"goto/pkg/util"
	"log"
	"reflect"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/dynamicpb"
)

type GRPCSessionLog struct {
	logCounter       atomic.Int32
	ClientMessageLog map[int]any         `json:"clientMessageLog"`
	ServerMessageLog map[int]any         `json:"serverMessageLog"`
	ClientHeaders    map[int]metadata.MD `json:"clientHeaders"`
	ServerHeaders    map[int]metadata.MD `json:"serverHeaders"`
	clientTeeStream  chan proto.Message
	serverTeeStream  chan proto.Message
	err              string
}

type GRPCSession struct {
	ID             int                         `json:"id"`
	DownstreamAddr string                      `json:"downstreamAddr"`
	Method         *gotogrpc.GRPCServiceMethod `json:"method"`
	Log            *GRPCSessionLog             `json:"log"`
	target         *GRPCTarget
	downstream     gotogrpc.GRPCStream
	upstream       gotogrpc.GRPCStream
	teeport        int
	tracker        *trackers.GRPCProxyTracker
}

type GRPCTarget struct {
	*ProxyTarget
	ActiveSessions map[string]*GRPCSession `json:"activeSessions"`
	PastSessions   map[string]*GRPCSession `json:"pastSessions"`
	ProxyService   *gotogrpc.GRPCService   `json:"proxyService"`
	TargetService  *gotogrpc.GRPCService   `json:"targetService"`
	client         *grpcclient.GRPCClient
}

type GRPCProxy struct {
	*Proxy
	TeeServices map[string]map[string]*GRPCSessionLog `json:"teeServices"`
	Tracker     *trackers.GRPCProxyTracker            `json:"tracker"`
}

var (
	grpcProxyByPort      = map[int]*GRPCProxy{}
	GRPCServiceRegistry  = gotogrpc.ServiceRegistry
	grpcSessionIdCounter = atomic.Int32{}
)

func init() {
	util.WillProxyGRPC = _WillProxyGRPC
	gotogrpc.ProxyGRPCUnary = ProxyGRPCUnary
	gotogrpc.ProxyGRPCStream = ProxyGRPCStream
}

func _WillProxyGRPC(port int, method any) bool {
	return WillProxyGRPC(port, method.(*gotogrpc.GRPCServiceMethod))
}

func WillProxyGRPC(port int, method *gotogrpc.GRPCServiceMethod) bool {
	p := GetGRPCProxyForPort(port)
	if !p.Enabled || (!p.hasAnyTargets() && len(p.TeeServices) == 0) {
		return false
	}
	if p.TeeServices[method.Service.Name] != nil {
		target, present := p.TeeServices[method.Service.Name][method.Name]
		if !present {
			target, present = p.TeeServices[method.Service.Name]["*"]
		}
		if present && target != nil {
			return present
		}
	}
	target := p.Targets[method.Service.Name]
	if target == nil {
		return false
	}
	gTarget := target.(*GRPCTarget)
	_, present := gTarget.uriRegexps[method.GetURI()]
	if !present {
		_, present = gTarget.uriRegexps["*"]
	}
	return present
}

func ProxyGRPCUnary(ctx context.Context, port int, method *gotogrpc.GRPCServiceMethod, md metadata.MD, inputs []proto.Message) (output []proto.Message, respHeaders metadata.MD, respTrailers metadata.MD, err error) {
	proxy := GetGRPCProxyForPort(port)
	if teeMethods := proxy.TeeServices[method.Service.Name]; teeMethods != nil {
		t := teeMethods[method.Name]
		if t == nil {
			t = teeMethods["*"]
		}
		if t != nil {
			return proxy.teeProxyGRPCUnary(method, t)
		}
	}
	t := proxy.Targets[method.Service.Name]
	if t == nil {
		return nil, nil, nil, fmt.Errorf("No upstream found for service [%s] method [%s]", method.Service.Name, method.Name)
	}
	target := t.(*GRPCTarget)
	sessionLog := newGRPCSessionLog()
	sessionLog.ClientHeaders[int(sessionLog.logCounter.Add(1))] = md
	_, toService, teeport := gotogrpc.ServiceRegistry.GetProxyService(method.Service.Name)
	toMethod := toService.Methods[method.Name]
	for _, input := range inputs {
		if toMethod.In != nil {
			input = toMethod.In(input)
		}
		if b, err := protojson.Marshal(input); err == nil {
			sessionLog.ClientMessageLog[int(sessionLog.logCounter.Add(1))] = util.JSONFromBytes(b)
		}
	}
	delay := target.ApplyDelay()
	if delay != "" {
		log.Printf("[DEBUG] GRPCProxy.ProxyGRPCMethod: Service [%s] Method [%s] Delayed Upstream [%s] by [%s]\n", method.Service.Name, method.Name, target.client.URL, delay)
	}
	if global.Flags.EnableProxyDebugLogs {
		log.Printf("[DEBUG] GRPCProxy.ProxyGRPCMethod: Service [%s] Method [%s] Invoking Unary to Upstream [%s] Target Service [%s] Method [%s]", method.Service.Name, method.Name, target.client.URL, target.client.Service.Name, toMethod.Name)
	}
	start := time.Now()
	output, respHeaders, respTrailers, err = target.client.InvokeRaw(toMethod, md, inputs)
	end := time.Now()
	tookNanos := end.Sub(start)
	if err == nil {
		respHeaders.Append(constants.HeaderGotoProxyUpstreamTook, tookNanos.String())
		util.AddHeaderWithPrefixL("Proxy-", constants.HeaderGotoHost, global.Funcs.GetHostLabelForPort(port), respHeaders)
		util.AddHeaderWithPrefixL("Proxy-", constants.HeaderGotoPort, strconv.Itoa(port), respHeaders)
		util.AddHeaderWithPrefixL("Proxy-", constants.HeaderViaGoto, global.Funcs.GetListenerLabelForPort(port), respHeaders)
		respHeaders.Append(constants.HeaderViaGoto, global.Funcs.GetListenerLabelForPort(port))
		if delay != "" {
			respHeaders.Append(constants.HeaderGotoProxyDelay, delay)
		}
		sessionLog.ServerHeaders[int(sessionLog.logCounter.Add(1))] = respHeaders
		for _, output := range output {
			if b, err := protojson.Marshal(output); err == nil {
				sessionLog.ServerMessageLog[int(sessionLog.logCounter.Add(1))] = util.JSONFromBytes(b)
			}
		}
	} else {
		sessionLog.err = err.Error()
		log.Printf("[ERROR] GRPCProxy.ProxyGRPCMethod: Service [%s] Method [%s] Error while calling upstream [%s]: %s\n", method.Service.Name, method.Name, t.GetName(), err.Error())
	}
	proxy.updateTeeSessionLog(teeport, method.Service.Name, method.Name, sessionLog)
	proxy.Tracker.IncrementConnCounts(t.GetName())
	proxy.Tracker.AddMatchCounts(t.GetName(), target.ProxyService.Name, method.Name, string(method.InputType().Name()), 1, string(method.OutputType().Name()), len(output))
	return
}

func ProxyGRPCStream(ctx context.Context, port int, method *gotogrpc.GRPCServiceMethod, downstreamAddr string, md metadata.MD, downstream gotogrpc.GRPCStream) (receiveCount, sendCount int, err error) {
	proxy := GetGRPCProxyForPort(port)
	if teeMethods := proxy.TeeServices[method.Service.Name]; teeMethods != nil {
		t := teeMethods[method.Name]
		if t == nil {
			t = teeMethods["*"]
		}
		if t != nil {
			return proxy.teeProxyGRPCStream(method, downstream, t)
		}
	}
	session, err := proxy.OpenGRPCSession(ctx, method, downstreamAddr, md, downstream)
	if err != nil {
		return 0, 0, err
	}
	receiveCount, sendCount, err = session.Stream()
	session.Close()
	return
}

func (p *GRPCProxy) createResponse(method *gotogrpc.GRPCServiceMethod, json any) (msg proto.Message, err error) {
	msg = dynamicpb.NewMessage(method.OutputType())
	err = protojson.Unmarshal(util.ToJSONBytes(json), msg)
	return
}

func (p *GRPCProxy) addTeeHeaders(respHeaders metadata.MD) metadata.MD {
	util.AddHeaderWithPrefixL("TeeProxy-", constants.HeaderGotoHost, global.Funcs.GetHostLabelForPort(p.Port), respHeaders)
	util.AddHeaderWithPrefixL("TeeProxy-", constants.HeaderGotoPort, strconv.Itoa(p.Port), respHeaders)
	util.AddHeaderWithPrefixL("TeeProxy-", constants.HeaderViaGoto, global.Funcs.GetListenerLabelForPort(p.Port), respHeaders)
	respHeaders.Append(constants.HeaderViaGoto, global.Funcs.GetListenerLabelForPort(p.Port))
	return respHeaders
}

func (p *GRPCProxy) teeProxyGRPCUnary(method *gotogrpc.GRPCServiceMethod, sessionLog *GRPCSessionLog) (output []proto.Message, respHeaders metadata.MD, respTrailers metadata.MD, err error) {
	if sessionLog == nil {
		return
	}
	responseIDs := make([]int, 0, len(sessionLog.ServerMessageLog))
	for index := range sessionLog.ServerMessageLog {
		responseIDs = append(responseIDs, index)
	}
	sort.Ints(responseIDs)
	var msg proto.Message
	for _, index := range responseIDs {
		msg, err = p.createResponse(method, sessionLog.ServerMessageLog[index])
		if err != nil {
			return
		}
		output = append(output, msg)
	}
	for _, headers := range sessionLog.ServerHeaders {
		respHeaders = p.addTeeHeaders(headers.Copy())
		break
	}
	return
}

func (p *GRPCProxy) getLogIDs(sessionLog *GRPCSessionLog) (messageIDs []int, headerIDs map[int]bool) {
	messageIDs = make([]int, 0, len(sessionLog.ServerMessageLog)+len(sessionLog.ClientMessageLog))
	for index := range sessionLog.ServerMessageLog {
		messageIDs = append(messageIDs, index)
	}
	for index := range sessionLog.ClientMessageLog {
		messageIDs = append(messageIDs, index)
	}
	sort.Ints(messageIDs)
	headerIDs = map[int]bool{}
	for index := range sessionLog.ClientHeaders {
		headerIDs[index] = true
	}
	for index := range sessionLog.ServerHeaders {
		headerIDs[index] = true
	}
	return
}

func (p *GRPCProxy) teeProxyGRPCStream(method *gotogrpc.GRPCServiceMethod, downstream gotogrpc.GRPCStream, sessionLog *GRPCSessionLog) (receiveCount, sendCount int, err error) {
	if sessionLog == nil {
		return
	}
	messageIDs, headerIDs := p.getLogIDs(sessionLog)
	maxMsgId := messageIDs[len(messageIDs)-1]

	readClientStream := func(in chan proto.Message, wg *sync.WaitGroup) {
		receiveCount, _ = downstream.TeeStreamReceive(in, nil)
		wg.Done()
	}
	sendServerStream := func(in chan proto.Message, wg *sync.WaitGroup) {
		for _, headers := range sessionLog.ServerHeaders {
			downstream.SendHeaders(p.addTeeHeaders(headers.Copy()))
			break
		}
		sendIndex := 0
		if in != nil {
			for range in {
				sendIndex++
				sendIndex, sendCount, err = p.sendTeeProxyMessages(method, maxMsgId, sendIndex, sendCount, headerIDs, sessionLog.ServerMessageLog, downstream)
				if err != nil {
					break
				}
			}
		} else {
			sendIndex, sendCount, err = p.sendTeeProxyMessages(method, maxMsgId, -1, -1, headerIDs, sessionLog.ServerMessageLog, downstream)
		}
		if wg != nil {
			wg.Done()
		}
	}

	if !method.IsBidiStream {
		receiveCount, _ = downstream.ReceiveAndDiscard()
		sendServerStream(nil, nil)
	} else {
		wg := &sync.WaitGroup{}
		wg.Add(2)
		in := make(chan proto.Message, 10)
		go readClientStream(in, wg)
		go sendServerStream(in, wg)
		wg.Wait()
	}
	return
}

func (p *GRPCProxy) sendTeeProxyMessages(method *gotogrpc.GRPCServiceMethod, maxMsgId, lastIndex, prevSendCount int, headerIDs map[int]bool, messageLog map[int]any, downstream gotogrpc.GRPCStream) (sendIndex int, sendCount int, err error) {
	sendCount = prevSendCount
	sendAll := false
	if lastIndex == -1 {
		sendAll = true
	}
	for i := lastIndex + 1; i <= maxMsgId; i++ {
		if messageLog[i] == nil {
			if sendAll || headerIDs[i] {
				continue
			} else {
				break
			}
		}
		var msg proto.Message
		msg, err = p.createResponse(method, messageLog[i])
		if err != nil {
			return
		}
		err = downstream.Send(msg)
		if err != nil {
			return
		}
		sendIndex = i
		sendCount++
	}
	return
}

func GetGRPCProxyForPort(port int) *GRPCProxy {
	proxyLock.RLock()
	proxy := grpcProxyByPort[port]
	proxyLock.RUnlock()
	if proxy == nil {
		proxy = newGRPCProxy(port)
		proxyLock.Lock()
		grpcProxyByPort[port] = proxy
		proxyLock.Unlock()
	}
	return proxy
}

func newGRPCProxy(port int) *GRPCProxy {
	p := &GRPCProxy{
		Proxy:       newProxy(port),
		TeeServices: map[string]map[string]*GRPCSessionLog{},
		Tracker:     &trackers.GRPCProxyTracker{},
	}
	p.initTracker()
	return p
}

func newGRPCTarget(from, to *gotogrpc.GRPCService, endpoint, authority string) (*GRPCTarget, error) {
	if from == nil || endpoint == "" {
		return nil, errors.New("no service/endpoint given")
	}
	if to == nil {
		to = from
	}
	target := &GRPCTarget{
		ProxyTarget:    newProxyTarget(to.Name, "grpc", endpoint),
		ActiveSessions: map[string]*GRPCSession{},
		PastSessions:   map[string]*GRPCSession{},
		ProxyService:   from,
		TargetService:  to,
	}
	target.Authority = authority
	if host, port := util.ParseAddress(endpoint); host != "" && port > 0 {
		if client, err := grpcclient.NewGRPCClient(to, endpoint, authority, host, &grpcclient.GRPCOptions{IsTLS: false, VerifyTLS: false}); err == nil {
			target.client = client
		} else {
			return nil, err
		}
	}
	return target, nil
}

func (p *GRPCProxy) Init() {
	p.Proxy.init()
	p.initTracker()
}

func (p *GRPCProxy) initTracker() {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.Tracker = trackers.NewGRPCProxyTracker()
}

func (p *GRPCProxy) RemoveProxy(service string) {
	p.Proxy.deleteProxyTarget(service)
}

func (p *GRPCProxy) SetupGRPCProxy(from, to string, methods map[string]rpc.RPCMethod, endpoint, authority string, teeport int, delayMin, delayMax time.Duration, delayCount int) error {
	fromService, oldToService, _ := GRPCServiceRegistry.GetProxyService(from)
	fromService = GRPCServiceRegistry.GetService(from)
	if reflect.ValueOf(fromService).IsNil() {
		return fmt.Errorf("[ERROR] GRPCProxy.SetupGRPCProxy: no service found for [%s]", from)
	}
	toService := GRPCServiceRegistry.GetService(to)
	if reflect.ValueOf(toService).IsNil() {
		return fmt.Errorf("[ERROR] GRPCProxy.SetupGRPCProxy: no service found for [%s]", to)
	}
	if global.Flags.EnableProxyDebugLogs {
		msg := fmt.Sprintf("[DEBUG] GRPCProxy.SetupGRPCProxy: Proxying service [%s] to target [%s @ %s]", from, to, endpoint)
		if !reflect.ValueOf(oldToService).IsNil() {
			msg += fmt.Sprintf(", replacing old target [%s]", oldToService.Name)
		}
		log.Println(msg)
	}
	_, s := global.GRPCManager.InterceptAndProxy(fromService.GSD, toService.GSD, to, nil, teeport)
	if s == nil {
		return fmt.Errorf("[ERROR] GRPCProxy.SetupGRPCProxy: could not setup proxy service for [%s]", from)
	}

	p.lock.Lock()
	defer p.lock.Unlock()
	target, err := newGRPCTarget(fromService, toService, endpoint, authority)
	if err != nil {
		return err
	}
	p.Targets[from] = target
	if methods != nil {
		for _, method := range methods {
			target.uriRegexps[method.GetURI()] = nil
		}
	} else {
		target.uriRegexps["*"] = nil
	}
	target.SetDelay(delayMin, delayMax, delayCount)
	return nil
}

func (p *GRPCProxy) updateTeeSessionLog(teeport int, service, methodName string, sessionLog *GRPCSessionLog) {
	if teeport <= 0 || service == "" || methodName == "" || sessionLog == nil {
		return
	}
	teeProxy := GetGRPCProxyForPort(teeport)
	if teeProxy.TeeServices[service] == nil {
		teeProxy.TeeServices[service] = map[string]*GRPCSessionLog{}
	}
	teeProxy.TeeServices[service][methodName] = sessionLog
}

func (p *GRPCProxy) readClientMessage(method *gotogrpc.GRPCServiceMethod, downstream gotogrpc.GRPCStream) (clientMsg proto.Message, toMethod *gotogrpc.GRPCServiceMethod, teeport int, err error) {
	clientMsg, err = downstream.Receive()
	if err != nil {
		return
	}
	if clientMsg == nil {
		err = fmt.Errorf("No client message")
		return
	}
	if global.Flags.EnableProxyDebugLogs {
		log.Printf("[DEBUG] Proxy.readClientMessage: Read initial message from downstream for Service [%s] Method [%s]: [%+v].\n", method.Service.Name, method.Name, clientMsg)
	}
	var toService *gotogrpc.GRPCService
	_, toService, teeport = gotogrpc.ServiceRegistry.GetProxyService(method.Service.Name)
	toMethod = toService.Methods[method.Name]
	if toMethod.In != nil {
		clientMsg = toMethod.In(clientMsg)
	}
	return
}

func (p *GRPCProxy) OpenGRPCSession(ctx context.Context, method *gotogrpc.GRPCServiceMethod, downstreamAddr string, md metadata.MD, downstream gotogrpc.GRPCStream) (*GRPCSession, error) {
	p.lock.RLock()
	t := p.Targets[method.Service.Name]
	p.lock.RUnlock()
	if t == nil {
		return nil, fmt.Errorf("[ERROR] No upstream found for service [%s] method [%s]", method.Service.Name, method.Name)
	}
	target := t.(*GRPCTarget)
	clientMsg, toMethod, teeport, err := p.readClientMessage(method, downstream)
	if err != nil {
		return nil, err
	}
	upstream, err := target.client.OpenStream(p.Port, toMethod, md, clientMsg)
	if err != nil {
		return nil, err
	}
	session := p.newGRPCSession(downstreamAddr, target, toMethod, downstream, upstream, teeport)
	session.Log.clientTeeStream <- clientMsg
	target.lock.Lock()
	target.ActiveSessions[downstreamAddr] = session
	p.updateTeeSessionLog(teeport, method.Service.Name, method.Name, session.Log)
	target.lock.Unlock()
	p.Tracker.IncrementConnCounts(t.GetName())
	if global.Flags.EnableProxyDebugLogs {
		log.Printf("[DEBUG] Opened proxy session to upstream [%s] target service [%s] method [%s]", target.client.URL, target.client.Service.Name, method.Name)
	}
	return session, nil
}

func (p *GRPCProxy) newGRPCSession(downstreamAddr string, target *GRPCTarget, method *gotogrpc.GRPCServiceMethod, downstream, upstream gotogrpc.GRPCStream, teeport int) *GRPCSession {
	session := &GRPCSession{
		ID:             int(grpcSessionIdCounter.Add(1)),
		DownstreamAddr: downstreamAddr,
		target:         target,
		Method:         method,
		downstream:     downstream,
		upstream:       upstream,
		teeport:        teeport,
		tracker:        p.Tracker,
	}
	if teeport > 0 {
		session.Log = newGRPCSessionLog()
	}
	return session
}

func (p *GRPCProxy) getGRPCSession(port int, downstreamAddr string, method *gotogrpc.GRPCServiceMethod) *GRPCSession {
	proxy := GetGRPCProxyForPort(port)
	if t := proxy.Targets[method.Service.Name]; t != nil {
		return t.(*GRPCTarget).ActiveSessions[downstreamAddr]
	}
	return nil
}

func (s *GRPCSession) isTee() bool {
	return s.teeport > 0
}

func (s *GRPCSession) Stream() (receiveCount, sendCount int, err error) {
	hook1 := gotogrpc.IdentityHook
	hook2 := gotogrpc.IdentityHook
	headersHook1 := gotogrpc.IdentityHeadersHook
	headersHook2 := gotogrpc.IdentityHeadersHook
	if s.isTee() {
		if s.target.HasDelay() {
			hook1 = gotogrpc.TeeHookWithDelay(s.Log.clientTeeStream, s.target.ApplyDelay)
		} else {
			hook1 = gotogrpc.TeeHook(s.Log.clientTeeStream)
		}
		hook2 = gotogrpc.TeeHook(s.Log.serverTeeStream)
		headersHook1 = gotogrpc.TeeHeadersHook(s.Log.onClientHeaders)
		headersHook2 = gotogrpc.TeeHeadersHook(s.Log.onServerHeaders)
		go s.Log.start()
	} else if s.target.HasDelay() {
		hook1 = gotogrpc.IdentityHookWithDelay(s.target.ApplyDelay)
	}
	receiveCount, sendCount, err = s.downstream.CrossHook(s.upstream, hook1, hook2, headersHook1, headersHook2)
	s.downstream.Close()
	s.upstream.Close()
	if s.isTee() {
		close(s.Log.clientTeeStream)
		close(s.Log.serverTeeStream)
	}
	s.tracker.AddMatchCounts(s.target.Name, s.Method.Service.Name, s.Method.Name, string(s.Method.InputType().Name()), receiveCount, string(s.Method.OutputType().Name()), sendCount)
	return
}

func (g *GRPCSession) Close() (err error) {
	m, e := g.upstream.Close()
	if e != nil {
		err = errors.Join(err, e)
	}
	if m != nil {
		e = g.downstream.Send(m)
		if e != nil {
			err = errors.Join(err, e)
		}
	}
	_, e = g.downstream.Close()
	if e != nil {
		err = errors.Join(err, e)
	}
	g.target.lock.Lock()
	g.target.PastSessions[g.DownstreamAddr] = g
	delete(g.target.ActiveSessions, g.DownstreamAddr)
	g.target.lock.Unlock()
	return
}

func newGRPCSessionLog() *GRPCSessionLog {
	return &GRPCSessionLog{
		ClientMessageLog: map[int]any{},
		ServerMessageLog: map[int]any{},
		ClientHeaders:    map[int]metadata.MD{},
		ServerHeaders:    map[int]metadata.MD{},
		clientTeeStream:  make(chan proto.Message, 10),
		serverTeeStream:  make(chan proto.Message, 10),
	}
}

func (s *GRPCSessionLog) start() {
	for {
		select {
		case msg, ok := <-s.clientTeeStream:
			if !ok || msg == nil {
				if global.Flags.EnableProxyDebugLogs {
					log.Println("[DEBUG] GRPCSessionLog: Client TEE channel was closed. Ending client session log.")
				}
				return
			}
			counter := int(s.logCounter.Add(1))
			if global.Flags.EnableProxyDebugLogs {
				log.Printf("[DEBUG] GRPCSessionLog: Read message #%d from client TEE channel: [%+v].\n", counter, msg)
			}
			if b, err := protojson.Marshal(msg); err == nil {
				s.ClientMessageLog[counter] = util.JSONFromBytes(b)
			}
		case msg, ok := <-s.serverTeeStream:
			if !ok || msg == nil {
				if global.Flags.EnableProxyDebugLogs {
					log.Println("[DEBUG] GRPCSessionLog: Server TEE channel was closed. Ending server session log.")
				}
				return
			}
			counter := int(s.logCounter.Add(1))
			if global.Flags.EnableProxyDebugLogs {
				log.Printf("[DEBUG] GRPCSessionLog: Read message #%d from server TEE channel: [%+v].\n", counter, msg)
			}
			s.ServerMessageLog[counter] = msg
		}
	}
}

func (s *GRPCSessionLog) clientStream() chan proto.Message {
	return s.clientTeeStream
}

func (s *GRPCSessionLog) serverStream() chan proto.Message {
	return s.serverTeeStream
}

func (s *GRPCSessionLog) onClientHeaders(md metadata.MD) {
	s.ClientHeaders[int(s.logCounter.Add(1))] = md
}

func (s *GRPCSessionLog) onServerHeaders(md metadata.MD) {
	s.ServerHeaders[int(s.logCounter.Add(1))] = md
}
