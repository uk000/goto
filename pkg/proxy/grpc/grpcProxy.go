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

package grpcproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/global"
	gotogrpc "goto/pkg/rpc/grpc"
	grpcclient "goto/pkg/rpc/grpc/client"
	"goto/pkg/types"
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
	upstream       *GRPCUpstream
	serviceProxy   *GRPCServiceProxy
	streamDown     gotogrpc.GRPCStream
	streamUp       gotogrpc.GRPCStream
	teeport        int
	tracker        *GRPCProxyTracker
}

type GRPCUpstream struct {
	ID             string                  `json:"id"`
	Endpoint       string                  `json:"endpoint"`
	Authority      string                  `json:"authority"`
	ActiveSessions map[string]*GRPCSession `json:"activeSessions"`
	PastSessions   map[string]*GRPCSession `json:"pastSessions"`
	client         *grpcclient.GRPCClient
	lock           sync.RWMutex
}

type GRPCProxyConfig struct {
	Delay *types.Delay `json:"delay"`
}

type GRPCServiceProxy struct {
	Label         string            `json:"label"`
	Port          int               `json:"port"`
	FromService   string            `json:"fromService"`
	ToService     string            `json:"toService"`
	Methods       map[string]string `json:"methods"`
	Upstream      *GRPCUpstream     `json:"upstream"`
	Config        *GRPCProxyConfig  `json:"config"`
	proxyService  *gotogrpc.GRPCService
	targetService *gotogrpc.GRPCService
	tracker       *GRPCProxyTracker
	// The delay counts down as it is applied, and the calls of a service are made concurrently.
	delayLock sync.Mutex
}

type GRPCProxy struct {
	Port           int                                   `json:"port"`
	Enabled        bool                                  `json:"enabled"`
	ServiceProxies map[string]*GRPCServiceProxy          `json:"services"`
	TeeServices    map[string]map[string]*GRPCSessionLog `json:"teeServices"`
	Tracker        *GRPCProxyTracker                     `json:"tracker"`
	lock           sync.RWMutex
}

var (
	portProxy            = map[int]*GRPCProxy{}
	GRPCServiceRegistry  = gotogrpc.ServiceRegistry
	grpcSessionIdCounter = atomic.Int32{}
	proxyLock            sync.RWMutex
)

func init() {
	util.WillProxyGRPC = _WillProxyGRPC
	gotogrpc.ProxyGRPCUnary = ProxyGRPCUnary
	gotogrpc.ProxyGRPCStream = ProxyGRPCStream
}

func _WillProxyGRPC(port int, method any) bool {
	m, ok := method.(*gotogrpc.GRPCServiceMethod)
	if !ok {
		return false
	}
	return WillProxyGRPC(port, m)
}

func WillProxyGRPC(port int, method *gotogrpc.GRPCServiceMethod) bool {
	if method == nil || method.Service == nil {
		return false
	}
	p := GetPortProxy(port)
	p.lock.RLock()
	defer p.lock.RUnlock()
	if !p.Enabled || (len(p.ServiceProxies) == 0 && len(p.TeeServices) == 0) {
		return false
	}
	if p.getTeeLogLocked(method.Service.Name, method.Name) != nil {
		return true
	}
	sp := p.ServiceProxies[method.Service.Name]
	if sp == nil {
		return false
	}
	if len(sp.Methods) > 0 {
		// A proxy that was set up for all the methods of the service has "*" for its methods.
		_, present := sp.Methods[method.URI]
		if !present {
			_, present = sp.Methods["*"]
		}
		if !present {
			return false
		}
	}
	return true
}

// getTeeLogLocked finds the session log that is recorded for the method of the service, or for all the methods of the
// service. The lock of the proxy is held by the caller.
func (p *GRPCProxy) getTeeLogLocked(service, method string) *GRPCSessionLog {
	teeMethods := p.TeeServices[service]
	if teeMethods == nil {
		return nil
	}
	if t := teeMethods[method]; t != nil {
		return t
	}
	return teeMethods["*"]
}

func (p *GRPCProxy) getTeeLog(service, method string) *GRPCSessionLog {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.getTeeLogLocked(service, method)
}

func (p *GRPCProxy) getServiceProxy(service string) *GRPCServiceProxy {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.ServiceProxies[service]
}

// getTargetMethodName tells the method of the target service that calls of the method are proxied to: the one that the
// proxy was set up to map the method to, or the one of the same name.
func (p *GRPCProxy) getTargetMethodName(sp *GRPCServiceProxy, method *gotogrpc.GRPCServiceMethod) string {
	p.lock.RLock()
	defer p.lock.RUnlock()
	if name := sp.Methods[method.URI]; name != "" {
		return name
	}
	return method.Name
}

func (p *GRPCProxy) getTracker() *GRPCProxyTracker {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.Tracker
}

// MarshalJSON reports the proxy as it is at the moment, while services are being proxied and set up.
func (p *GRPCProxy) MarshalJSON() ([]byte, error) {
	p.lock.RLock()
	defer p.lock.RUnlock()
	type plain GRPCProxy
	return json.Marshal((*plain)(p))
}

func ProxyGRPCUnary(ctx context.Context, port int, method *gotogrpc.GRPCServiceMethod, md metadata.MD, inputs []proto.Message) (output []proto.Message, respHeaders metadata.MD, respTrailers metadata.MD, err error) {
	proxy := GetPortProxy(port)
	if t := proxy.getTeeLog(method.Service.Name, method.Name); t != nil {
		return proxy.teeProxyGRPCUnary(method, t)
	}
	sp := proxy.getServiceProxy(method.Service.Name)
	if sp == nil {
		return nil, nil, nil, fmt.Errorf("No proxy mapping found for service [%s] method [%s]", method.Service.Name, method.Name)
	}
	up := sp.Upstream
	if up == nil || up.client == nil {
		return nil, nil, nil, fmt.Errorf("No upstream found for service [%s] method [%s]", method.Service.Name, method.Name)
	}
	_, toService, teeport := gotogrpc.ServiceRegistry.GetProxyService(method.Service.Name)
	if toService == nil {
		return nil, nil, nil, fmt.Errorf("No target service found for service [%s] method [%s]", method.Service.Name, method.Name)
	}
	toMethod := toService.Methods[proxy.getTargetMethodName(sp, method)]
	if toMethod == nil {
		return nil, nil, nil, fmt.Errorf("No target method found for service [%s] method [%s]", method.Service.Name, method.Name)
	}
	sessionLog := newGRPCSessionLog()
	sessionLog.ClientHeaders[int(sessionLog.logCounter.Add(1))] = md
	// What the upstream is sent is the input as it is made for the target method.
	upstreamInputs := make([]proto.Message, 0, len(inputs))
	for _, input := range inputs {
		if toMethod.In != nil {
			input = toMethod.In(input)
		}
		upstreamInputs = append(upstreamInputs, input)
		if b, err := protojson.Marshal(input); err == nil {
			sessionLog.ClientMessageLog[int(sessionLog.logCounter.Add(1))] = util.JSONFromBytes(b)
		}
	}
	delay := sp.applyDelay()
	if delay != "" {
		log.Printf("[DEBUG] GRPCProxy.ProxyGRPCMethod: Service [%s] Method [%s] Delayed Upstream [%s] by [%s]\n",
			method.Service.Name, method.Name, up.client.URL, delay)
	}
	if global.Flags.EnableProxyDebugLogs {
		log.Printf("[DEBUG] GRPCProxy.ProxyGRPCMethod: Service [%s] Method [%s] Invoking Unary to Upstream [%s] Target Service [%s] Method [%s]",
			method.Service.Name, method.Name, up.client.URL, up.client.Service.Name, toMethod.Name)
	}
	start := time.Now()
	output, respHeaders, respTrailers, err = up.client.InvokeRaw(toMethod, md, upstreamInputs)
	end := time.Now()
	tookNanos := end.Sub(start)
	if err == nil {
		if respHeaders == nil {
			respHeaders = metadata.MD{}
		}
		respHeaders.Append(constants.HeaderGotoProxyUpstreamTook, tookNanos.String())
		respHeaders.Append(constants.HeaderGotoProxyHost, global.Self.HostLabel)
		respHeaders.Append(constants.HeaderGotoProxyPort, strconv.Itoa(port))
		util.AddHeaderWithPrefixL("Proxy-", constants.HeaderViaGoto, util.GetViaGotoValue(port), respHeaders)
		respHeaders.Append(constants.HeaderViaGoto, fmt.Sprintf("%s(Proxy)", global.Funcs.GetListenerLabelForPort(port)))
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
		log.Printf("[ERROR] GRPCProxy.ProxyGRPCMethod: Service [%s] Method [%s] Error while calling upstream [%s]: %s\n",
			method.Service.Name, method.Name, up.ID, err.Error())
	}
	proxy.updateTeeSessionLog(teeport, method.Service.Name, method.Name, sessionLog)
	// A call that failed has no responses, whatever the client reports for it.
	responseCount := 0
	if err != nil {
		output = nil
	}
	for _, o := range output {
		if o != nil {
			responseCount++
		}
	}
	tracker := proxy.getTracker()
	tracker.IncrementConnCounts(up.ID)
	tracker.AddMatchCounts(up.ID, sp.proxyService.Name, method.Name,
		string(method.InputType().Name()), 1, string(method.OutputType().Name()), responseCount)
	return
}

func ProxyGRPCStream(ctx context.Context, port int, method *gotogrpc.GRPCServiceMethod, downstreamAddr string,
	md metadata.MD, downstream gotogrpc.GRPCStream) (receiveCount, sendCount int, err error) {
	proxy := GetPortProxy(port)
	if t := proxy.getTeeLog(method.Service.Name, method.Name); t != nil {
		return proxy.teeProxyGRPCStream(method, downstream, t)
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
	util.AddHeaderWithPrefixL("TeeProxy-", constants.HeaderGotoHost, global.Self.HostLabel, respHeaders)
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
	// A log can have no messages, for a call that failed or had nothing to send.
	maxMsgId := 0
	if len(messageIDs) > 0 {
		maxMsgId = messageIDs[len(messageIDs)-1]
	}

	readClientStream := func(in chan proto.Message, wg *sync.WaitGroup) {
		receiveCount, _ = downstream.TeeStreamReceive(in, nil)
		wg.Done()
	}
	sendServerStream := func(in chan proto.Message, wg *sync.WaitGroup) {
		for _, headers := range sessionLog.ServerHeaders {
			downstream.SendHeaders(p.addTeeHeaders(headers.Copy()))
			break
		}
		if in != nil {
			sendCount, err = p.replayTeeBidiStream(method, sessionLog, in, downstream)
		} else {
			_, sendCount, err = p.sendTeeProxyMessages(method, maxMsgId, -1, 0, headerIDs, sessionLog.ServerMessageLog, downstream)
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

// replayTeeBidiStream answers the messages that the client sends as the log has the server answer them: a message of the
// server is sent once the client has sent as many messages as it had when that message of the server was logged. A log
// that has the messages of the client and the ones of the server taking turns is replayed in turns, and one that has the
// client send all of its messages before the server answers (which is what a proxy logs when the client is faster than
// the upstream) is replayed after the last message of the client. The messages that the log has for the server before
// any message of the client are sent at once, and the ones that are left when the client is done are sent then.
func (p *GRPCProxy) replayTeeBidiStream(method *gotogrpc.GRPCServiceMethod, sessionLog *GRPCSessionLog, in chan proto.Message, downstream gotogrpc.GRPCStream) (sendCount int, err error) {
	clientIDs := make([]int, 0, len(sessionLog.ClientMessageLog))
	for id := range sessionLog.ClientMessageLog {
		clientIDs = append(clientIDs, id)
	}
	sort.Ints(clientIDs)
	serverIDs := make([]int, 0, len(sessionLog.ServerMessageLog))
	for id, msg := range sessionLog.ServerMessageLog {
		if msg != nil {
			serverIDs = append(serverIDs, id)
		}
	}
	sort.Ints(serverIDs)
	next := 0
	sendDue := func(clientCount int) {
		for err == nil && next < len(serverIDs) {
			id := serverIDs[next]
			// the number of messages of the client that were logged before this one of the server
			if sort.SearchInts(clientIDs, id) > clientCount {
				return
			}
			var msg proto.Message
			if msg, err = p.createResponse(method, sessionLog.ServerMessageLog[id]); err == nil {
				err = downstream.Send(msg)
			}
			if err == nil {
				sendCount++
			}
			next++
		}
	}
	sendDue(0)
	received := 0
	// The messages of the client are read to the end even when there is nothing more to send, so that the one that
	// reads them is not left blocked.
	for range in {
		received++
		sendDue(received)
	}
	sendDue(len(clientIDs))
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

func GetPortProxy(port int) *GRPCProxy {
	proxyLock.RLock()
	proxy := portProxy[port]
	proxyLock.RUnlock()
	if proxy == nil {
		proxyLock.Lock()
		defer proxyLock.Unlock()
		// Another caller may have created the proxy since the read lock was released, and all callers must get that one.
		if proxy = portProxy[port]; proxy == nil {
			proxy = newGRPCProxy(port)
			portProxy[port] = proxy
		}
	}
	return proxy
}

func AddPortProxy(p *GRPCProxy) {
	proxyLock.Lock()
	portProxy[p.Port] = p
	proxyLock.Unlock()
	p.initTracker()
}

func RemovePortProxy(port int) {
	proxyLock.Lock()
	delete(portProxy, port)
	proxyLock.Unlock()
}

func newGRPCProxy(port int) *GRPCProxy {
	p := &GRPCProxy{
		Port:           port,
		Enabled:        true,
		ServiceProxies: map[string]*GRPCServiceProxy{},
		TeeServices:    map[string]map[string]*GRPCSessionLog{},
		Tracker:        &GRPCProxyTracker{},
	}
	p.initTracker()
	return p
}

func newGRPCServiceProxy(port int, from, to *gotogrpc.GRPCService, endpoint, authority string, tracker *GRPCProxyTracker) (*GRPCServiceProxy, error) {
	if from == nil || endpoint == "" {
		return nil, errors.New("no service/endpoint given")
	}
	if to == nil {
		to = from
	}
	sp := &GRPCServiceProxy{
		Port:          port,
		Label:         fmt.Sprintf("Proxy(%s)", from.Name),
		FromService:   from.Name,
		ToService:     to.Name,
		proxyService:  from,
		targetService: to,
		Methods:       map[string]string{},
		Upstream: &GRPCUpstream{
			ID:        to.Name,
			Endpoint:  endpoint,
			Authority: authority,
		},
		Config:  &GRPCProxyConfig{},
		tracker: tracker,
	}
	if err := sp.init(tracker); err != nil {
		return nil, err
	}
	return sp, nil
}

func (p *GRPCProxy) Clear() {
	p.lock.Lock()
	p.ServiceProxies = map[string]*GRPCServiceProxy{}
	p.TeeServices = map[string]map[string]*GRPCSessionLog{}
	p.lock.Unlock()
	p.initTracker()
}

func (p *GRPCProxy) initTracker() {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.Tracker = NewGRPCProxyTracker()
}

func (sp *GRPCServiceProxy) init(tracker *GRPCProxyTracker) error {
	sp.Upstream.ActiveSessions = map[string]*GRPCSession{}
	sp.Upstream.PastSessions = map[string]*GRPCSession{}
	sp.tracker = tracker
	if host, port := util.ParseAddress(sp.Upstream.Endpoint); host != "" && port > 0 {
		if client, err := grpcclient.NewGRPCClient(sp.Label, sp.Port, sp.targetService, sp.Upstream.Endpoint, sp.Upstream.Authority, host, &grpcclient.GRPCOptions{IsTLS: false, VerifyTLS: false}); err == nil {
			sp.Upstream.client = client
			return nil
		} else {
			return err
		}
	} else {
		return fmt.Errorf("Failed to initialize gRPC client due to invalid endpoint: %s", sp.Upstream.Endpoint)
	}
}

func (p *GRPCProxy) RemoveServiceProxy(service string) {
	p.lock.Lock()
	defer p.lock.Unlock()
	delete(p.ServiceProxies, service)
}

func (p *GRPCProxy) SetupGRPCServiceProxy(from, to string, methods map[string]string, endpoint, authority string, teeport int, delayMin, delayMax time.Duration, delayCount int, sp *GRPCServiceProxy) (err error) {
	fromService := GRPCServiceRegistry.GetService(from)
	if reflect.ValueOf(fromService).IsNil() {
		return fmt.Errorf("[ERROR] GRPCProxy.SetupGRPCProxy: no service found for [%s]", from)
	}
	_, oldToService, _ := GRPCServiceRegistry.GetProxyService(from)
	toService := GRPCServiceRegistry.GetService(to)
	if reflect.ValueOf(toService).IsNil() {
		return fmt.Errorf("[ERROR] GRPCProxy.SetupGRPCProxy: no service found for [%s]", to)
	}
	// The methods are checked before anything is set up, so that a proxy that fails to be set up leaves nothing behind.
	methodMap := map[string]string{}
	if methods != nil {
		for mFrom, mTo := range methods {
			fromMethod := fromService.Methods[mFrom]
			if fromMethod == nil {
				return fmt.Errorf("[ERROR] GRPCProxy.SetupGRPCProxy: Method [%s] not found on proxied service [%s]", mFrom, from)
			}
			toMethod := toService.Methods[mTo]
			if toMethod == nil {
				return fmt.Errorf("[ERROR] GRPCProxy.SetupGRPCProxy: Method [%s] not found on proxied service [%s]", mTo, to)
			}
			methodMap[fromMethod.GetURI()] = toMethod.GetName()
		}
	} else {
		methodMap["*"] = ""
	}
	if global.Flags.EnableProxyDebugLogs {
		msg := fmt.Sprintf("[DEBUG] GRPCProxy.SetupGRPCProxy: Proxying service [%s] to target [%s @ %s]", from, to, endpoint)
		if oldToService != nil {
			msg += fmt.Sprintf(", replacing old target [%s]", oldToService.Name)
		}
		log.Println(msg)
	}
	// The service proxy is made before the server is told to intercept the service, so that an endpoint that can't be
	// used leaves nothing behind.
	tracker := p.getTracker()
	if sp == nil {
		if sp, err = newGRPCServiceProxy(p.Port, fromService, toService, endpoint, authority, tracker); err != nil {
			return err
		}
	} else {
		sp.proxyService = fromService
		sp.targetService = toService
		sp.Port = p.Port
		if sp.Label == "" {
			sp.Label = fmt.Sprintf("Proxy(%s)", from)
		}
		if err = sp.init(tracker); err != nil {
			return err
		}
	}
	_, s := global.GRPCManager.InterceptAndProxy(fromService.GSD, toService.GSD, to, nil, teeport)
	if s == nil {
		return fmt.Errorf("[ERROR] GRPCProxy.SetupGRPCProxy: could not setup proxy service for [%s]", from)
	}
	p.lock.Lock()
	sp.Methods = methodMap
	if sp.Config == nil {
		sp.Config = &GRPCProxyConfig{}
	}
	sp.Config.Delay = types.NewDelay(delayMin, delayMax, delayCount)
	p.ServiceProxies[from] = sp
	p.lock.Unlock()
	// A server that is running has the service with the handlers that it was registered with, and has to take the ones
	// that proxy it. The lock of the proxy is not held for that, as the calls that the server stops are waiting on it.
	if refresher, ok := global.GRPCManager.(serviceRefresher); ok {
		refresher.RefreshServices()
	}
	return nil
}

// serviceRefresher is what the gRPC server manager does to have a running server use the services as they are now.
type serviceRefresher interface {
	RefreshServices()
}

func (p *GRPCProxy) updateTeeSessionLog(teeport int, service, methodName string, sessionLog *GRPCSessionLog) {
	if teeport <= 0 || service == "" || methodName == "" || sessionLog == nil {
		return
	}
	teeProxy := GetPortProxy(teeport)
	teeProxy.lock.Lock()
	defer teeProxy.lock.Unlock()
	if teeProxy.TeeServices[service] == nil {
		teeProxy.TeeServices[service] = map[string]*GRPCSessionLog{}
	}
	teeProxy.TeeServices[service][methodName] = sessionLog
}

func (p *GRPCProxy) readClientMessage(method *gotogrpc.GRPCServiceMethod, toName string, downstream gotogrpc.GRPCStream) (clientMsg proto.Message, toMethod *gotogrpc.GRPCServiceMethod, teeport int, err error) {
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
	if toService == nil || toService.Methods[toName] == nil {
		err = fmt.Errorf("No target method found for service [%s] method [%s]", method.Service.Name, method.Name)
		return
	}
	toMethod = toService.Methods[toName]
	if toMethod.In != nil {
		clientMsg = toMethod.In(clientMsg)
	}
	return
}

func (p *GRPCProxy) OpenGRPCSession(ctx context.Context, method *gotogrpc.GRPCServiceMethod, downstreamAddr string, md metadata.MD, downstream gotogrpc.GRPCStream) (*GRPCSession, error) {
	p.lock.RLock()
	sp := p.ServiceProxies[method.Service.Name]
	p.lock.RUnlock()
	if sp == nil {
		return nil, fmt.Errorf("[ERROR] No upstream found for service [%s] method [%s]", method.Service.Name, method.Name)
	}
	clientMsg, toMethod, teeport, err := p.readClientMessage(method, p.getTargetMethodName(sp, method), downstream)
	if err != nil {
		return nil, err
	}
	upstream, err := sp.Upstream.client.OpenStream(p.Port, toMethod, md, clientMsg)
	if err != nil {
		return nil, err
	}
	session := sp.newGRPCSession(downstreamAddr, sp.Upstream, toMethod, downstream, upstream, teeport)
	if session.Log != nil {
		session.Log.clientTeeStream <- clientMsg
	}
	sp.Upstream.lock.Lock()
	sp.Upstream.ActiveSessions[downstreamAddr] = session
	p.updateTeeSessionLog(teeport, method.Service.Name, method.Name, session.Log)
	sp.Upstream.lock.Unlock()
	p.getTracker().IncrementConnCounts(sp.Upstream.ID)
	if global.Flags.EnableProxyDebugLogs {
		log.Printf("[DEBUG] Opened proxy session to upstream [%s] target service [%s] method [%s]", sp.Upstream.client.URL, sp.Upstream.client.Service.Name, method.Name)
	}
	return session, nil
}

func (sp *GRPCServiceProxy) Validate() error {
	if sp.FromService == "" {
		return fmt.Errorf("Intercepted Service Name Required")
	}
	if sp.ToService == "" {
		return fmt.Errorf("Target Service Name Required")
	}
	if sp.Upstream == nil {
		return fmt.Errorf("Upstrem Required")
	}
	sp.proxyService = GRPCServiceRegistry.GetService(sp.FromService)
	if sp.proxyService == nil {
		return fmt.Errorf("no service found for [%s]", sp.FromService)
	}
	sp.targetService = GRPCServiceRegistry.GetService(sp.ToService)
	if sp.targetService == nil {
		return fmt.Errorf("no service found for [%s]", sp.ToService)
	}
	for m1, m2 := range sp.Methods {
		if sp.proxyService.Methods[m1] == nil {
			return fmt.Errorf("method [%s] not found in service [%s]", m1, sp.FromService)
		}
		if sp.targetService.Methods[m2] == nil {
			return fmt.Errorf("method [%s] not found in service [%s]", m2, sp.ToService)
		}
	}
	if sp.Config == nil {
		sp.Config = &GRPCProxyConfig{}
	}
	if sp.Config.Delay == nil {
		// A delay that has no time, which is what a proxy without a delay has to be configured with.
		sp.Config.Delay = &types.Delay{Min: &types.Duration{}, Max: &types.Duration{}}
	}
	return nil
}

func (sp *GRPCServiceProxy) newGRPCSession(downstreamAddr string, target *GRPCUpstream, method *gotogrpc.GRPCServiceMethod, downstream, upstream gotogrpc.GRPCStream, teeport int) *GRPCSession {
	session := &GRPCSession{
		ID:             int(grpcSessionIdCounter.Add(1)),
		DownstreamAddr: downstreamAddr,
		upstream:       target,
		serviceProxy:   sp,
		Method:         method,
		streamDown:     downstream,
		streamUp:       upstream,
		teeport:        teeport,
		tracker:        sp.tracker,
	}
	if teeport > 0 {
		session.Log = newGRPCSessionLog()
	}
	return session
}

func (sp *GRPCServiceProxy) hasDelay() bool {
	sp.delayLock.Lock()
	defer sp.delayLock.Unlock()
	return sp.Config != nil && sp.Config.Delay != nil && sp.Config.Delay.IsNonZero()
}

// applyDelay delays the call by the delay of the proxy, counting the call against the number of calls that the delay is
// for, and tells how long the call was delayed by.
func (sp *GRPCServiceProxy) applyDelay() (delay string) {
	sp.delayLock.Lock()
	var d time.Duration
	if cfg := sp.Config; cfg != nil {
		d = cfg.Delay.Claim()
	}
	sp.delayLock.Unlock()
	if d <= 0 {
		return ""
	}
	delay = d.String()
	if global.Flags.EnableProxyDebugLogs {
		log.Printf("[DEBUG] Upstream [%s]: Delaying by [%s]\n", sp.ToService, delay)
	}
	time.Sleep(d)
	return delay
}

func (s *GRPCSession) isTee() bool {
	return s.teeport > 0
}

func (s *GRPCSession) Stream() (receiveCount, sendCount int, err error) {
	hook1 := gotogrpc.IdentityHook
	hook2 := gotogrpc.IdentityHook
	headersHook1 := gotogrpc.IdentityHeadersHook
	headersHook2 := gotogrpc.IdentityHeadersHook
	var logDone chan struct{}
	if s.isTee() {
		if s.serviceProxy.hasDelay() {
			hook1 = gotogrpc.TeeHookWithDelay(s.Log.clientTeeStream, s.serviceProxy.applyDelay)
		} else {
			hook1 = gotogrpc.TeeHook(s.Log.clientTeeStream)
		}
		hook2 = gotogrpc.TeeHook(s.Log.serverTeeStream)
		headersHook1 = gotogrpc.TeeHeadersHook(s.Log.onClientHeaders)
		headersHook2 = gotogrpc.TeeHeadersHook(s.Log.onServerHeaders)
		logDone = make(chan struct{})
		go func() {
			defer close(logDone)
			s.Log.start()
		}()
	} else if s.serviceProxy.hasDelay() {
		hook1 = gotogrpc.IdentityHookWithDelay(s.serviceProxy.applyDelay)
	}
	receiveCount, sendCount, err = s.streamDown.CrossHook(s.streamUp, hook1, hook2, headersHook1, headersHook2)
	s.streamDown.Close()
	s.streamUp.Close()
	if s.isTee() {
		close(s.Log.clientTeeStream)
		close(s.Log.serverTeeStream)
		// The session is over when its log has all that was sent to it, which a status of the proxy can then report.
		<-logDone
	}
	s.tracker.AddMatchCounts(s.upstream.ID, s.Method.Service.Name, s.Method.Name, string(s.Method.InputType().Name()), receiveCount, string(s.Method.OutputType().Name()), sendCount)
	return
}

func (g *GRPCSession) Close() (err error) {
	m, e := g.streamUp.Close()
	if e != nil {
		err = errors.Join(err, e)
	}
	if m != nil {
		e = g.streamDown.Send(m)
		if e != nil {
			err = errors.Join(err, e)
		}
	}
	_, e = g.streamDown.Close()
	if e != nil {
		err = errors.Join(err, e)
	}
	g.upstream.lock.Lock()
	g.upstream.PastSessions[g.DownstreamAddr] = g
	delete(g.upstream.ActiveSessions, g.DownstreamAddr)
	g.upstream.lock.Unlock()
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
	// A channel that is closed is left alone, and the log goes on with the other until it is closed too: the session
	// closes the one of the client first, and what the server still has in its channel is logged after that.
	clientStream, serverStream := s.clientTeeStream, s.serverTeeStream
	for clientStream != nil || serverStream != nil {
		select {
		case msg, ok := <-clientStream:
			if !ok {
				if global.Flags.EnableProxyDebugLogs {
					log.Println("[DEBUG] GRPCSessionLog: Client TEE channel was closed. Ending client session log.")
				}
				clientStream = nil
				continue
			}
			if msg == nil {
				continue
			}
			counter := int(s.logCounter.Add(1))
			if global.Flags.EnableProxyDebugLogs {
				log.Printf("[DEBUG] GRPCSessionLog: Read message #%d from client TEE channel: [%+v].\n", counter, msg)
			}
			if b, err := protojson.Marshal(msg); err == nil {
				s.ClientMessageLog[counter] = util.JSONFromBytes(b)
			}
		case msg, ok := <-serverStream:
			if !ok {
				if global.Flags.EnableProxyDebugLogs {
					log.Println("[DEBUG] GRPCSessionLog: Server TEE channel was closed. Ending server session log.")
				}
				serverStream = nil
				continue
			}
			if msg == nil {
				continue
			}
			counter := int(s.logCounter.Add(1))
			if global.Flags.EnableProxyDebugLogs {
				log.Printf("[DEBUG] GRPCSessionLog: Read message #%d from server TEE channel: [%+v].\n", counter, msg)
			}
			// The messages of the server are logged as JSON, as the ones of the client are: a message that is kept as it
			// is has no fields that the log can be written with, so the status of the proxy reported it as {}, and a
			// replay of the log made empty messages of it.
			if b, err := protojson.Marshal(msg); err == nil {
				s.ServerMessageLog[counter] = util.JSONFromBytes(b)
			}
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
