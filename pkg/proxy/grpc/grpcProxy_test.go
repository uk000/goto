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
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goto/pkg/constants"
	"goto/pkg/global"
	gotogrpc "goto/pkg/rpc/grpc"
	"goto/pkg/util"

	"github.com/gorilla/mux"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// The services of the tests are described at run time: Front is the service that the proxy intercepts, and Echo the one
// that it proxies to. They have the same messages. Echo has a method that Front doesn't have.
const (
	frontName = "proxytest.Front"
	echoName  = "proxytest.Echo"
)

var (
	setupOnce sync.Once
	portSeq   atomic.Int32
	fileDesc  protoreflect.FileDescriptor
)

func buildFile() protoreflect.FileDescriptor {
	str := descriptorpb.FieldDescriptorProto_TYPE_STRING
	opt := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL
	msg := func(name string) *descriptorpb.DescriptorProto {
		return &descriptorpb.DescriptorProto{Name: proto.String(name), Field: []*descriptorpb.FieldDescriptorProto{
			{Name: proto.String("text"), JsonName: proto.String("text"), Number: proto.Int32(1), Type: &str, Label: &opt}}}
	}
	method := func(name string, serverStream bool) *descriptorpb.MethodDescriptorProto {
		m := &descriptorpb.MethodDescriptorProto{Name: proto.String(name), InputType: proto.String(".proxytest.Req"), OutputType: proto.String(".proxytest.Res")}
		if serverStream {
			m.ServerStreaming = proto.Bool(true)
		}
		return m
	}
	fdp := &descriptorpb.FileDescriptorProto{
		Name:        proto.String("proxytest.proto"),
		Package:     proto.String("proxytest"),
		Syntax:      proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{msg("Req"), msg("Res")},
		Service: []*descriptorpb.ServiceDescriptorProto{
			{Name: proto.String("Front"), Method: []*descriptorpb.MethodDescriptorProto{method("Say", false), method("Other", false), method("Watch", true)}},
			{Name: proto.String("Echo"), Method: []*descriptorpb.MethodDescriptorProto{method("Say", false), method("Other", false), method("Extra", false), method("Watch", true)}},
		},
	}
	fd, err := protodesc.NewFile(fdp, nil)
	if err != nil {
		panic(err)
	}
	return fd
}

// fakeManager is the server side of what setting up a proxy needs: it registers the services as proxied, as the gRPC
// server does.
type fakeManager struct {
	fail atomic.Bool
}

func (f *fakeManager) Reflect(*grpc.ServiceDesc)                      {}
func (f *fakeManager) InterceptAndServe(*grpc.ServiceDesc, any)       {}
func (f *fakeManager) InterceptWithMiddleware(*grpc.ServiceDesc, any) {}
func (f *fakeManager) InterceptAndProxy(from, to *grpc.ServiceDesc, target string, srv any, teeport int) (global.IGRPCService, global.IGRPCService) {
	if f.fail.Load() {
		return nil, nil
	}
	fromService := gotogrpc.ServiceRegistry.GetService(from.ServiceName)
	toService := gotogrpc.ServiceRegistry.GetService(to.ServiceName)
	if fromService == nil || toService == nil {
		return nil, nil
	}
	gotogrpc.ServiceRegistry.AddProxyService(fromService, toService, teeport)
	return fromService, toService
}

var manager = &fakeManager{}

func setupGlobals() {
	setupOnce.Do(func() {
		global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
		global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
		global.Funcs.IsListenerPresent = func(int) bool { return false }
		global.Funcs.IsListenerOpen = func(int) bool { return false }
		global.Funcs.GetListenerID = func(int) string { return "" }
		global.Funcs.GetListenerLabel = func(*http.Request) string { return "test" }
		global.Funcs.GetListenerLabelForPort = func(int) string { return "test" }
		util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
		global.GRPCManager = manager
		gotogrpc.GRPCUnaryHandler = func(*gotogrpc.GRPCServiceMethod) grpc.MethodHandler { return nil }
		gotogrpc.GRPCStreamHandler = func(any, grpc.ServerStream) error { return nil }
		fileDesc = buildFile()
		for _, name := range []string{"Front", "Echo"} {
			gotogrpc.ServiceRegistry.NewGRPCService(fileDesc.Services().ByName(protoreflect.Name(name)))
		}
		Middleware.SetRoutes(mux.NewRouter())
	})
}

// upstream is a gRPC server that serves the Echo service, and records what it is called with.
type upstream struct {
	addr  string
	lock  sync.Mutex
	calls []upstreamCall
}

type upstreamCall struct {
	method string
	text   string
	md     metadata.MD
}

func (u *upstream) record(method, text string, md metadata.MD) {
	u.lock.Lock()
	defer u.lock.Unlock()
	u.calls = append(u.calls, upstreamCall{method, text, md})
}

func (u *upstream) recorded() []upstreamCall {
	u.lock.Lock()
	defer u.lock.Unlock()
	return append([]upstreamCall{}, u.calls...)
}

func startUpstream(t *testing.T) *upstream {
	t.Helper()
	setupGlobals()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	u := &upstream{addr: lis.Addr().String()}
	reqDesc := fileDesc.Messages().ByName("Req")
	resDesc := fileDesc.Messages().ByName("Res")
	handler := func(name string) grpc.MethodDesc {
		return grpc.MethodDesc{MethodName: name, Handler: func(_ any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
			in := dynamicpb.NewMessage(reqDesc)
			if err := dec(in); err != nil {
				return nil, err
			}
			text := in.Get(reqDesc.Fields().ByName("text")).String()
			md, _ := metadata.FromIncomingContext(ctx)
			u.record(name, text, md)
			grpc.SetHeader(ctx, metadata.Pairs("x-upstream", name))
			out := dynamicpb.NewMessage(resDesc)
			out.Set(resDesc.Fields().ByName("text"), protoreflect.ValueOfString(strings.ToLower(name)+":"+text))
			return out, nil
		}}
	}
	srv := grpc.NewServer()
	srv.RegisterService(&grpc.ServiceDesc{
		ServiceName: echoName,
		HandlerType: (*any)(nil),
		Methods:     []grpc.MethodDesc{handler("Say"), handler("Other"), handler("Extra")},
	}, struct{}{})
	go srv.Serve(lis)
	t.Cleanup(srv.Stop)
	return u
}

type env struct {
	t     *testing.T
	port  int
	proxy *GRPCProxy
	front *gotogrpc.GRPCService
	echo  *gotogrpc.GRPCService
}

func newEnv(t *testing.T) *env {
	t.Helper()
	setupGlobals()
	port := int(45000 + portSeq.Add(1))
	e := &env{t: t, port: port, proxy: GetPortProxy(port),
		front: gotogrpc.ServiceRegistry.GetService(frontName), echo: gotogrpc.ServiceRegistry.GetService(echoName)}
	manager.fail.Store(false)
	savedIn := e.echo.Methods["Say"].In
	t.Cleanup(func() {
		e.echo.Methods["Say"].In = savedIn
		manager.fail.Store(false)
		RemovePortProxy(port)
		gotogrpc.ServiceRegistry.RemoveProxyService(frontName)
		for p := range gotogrpc.ServiceRegistry.TeeServices {
			gotogrpc.ServiceRegistry.RemoveTeeService(p)
		}
	})
	return e
}

func (e *env) setup(endpoint string, methods map[string]string) {
	e.t.Helper()
	if err := e.proxy.SetupGRPCServiceProxy(frontName, echoName, methods, endpoint, "", 0, 0, 0, 0, nil); err != nil {
		e.t.Fatalf("SetupGRPCServiceProxy: %v", err)
	}
}

// serviceProxy looks at the service proxies of the proxy.
func (e *env) serviceProxy(name string) *GRPCServiceProxy {
	e.proxy.lock.RLock()
	defer e.proxy.lock.RUnlock()
	return e.proxy.ServiceProxies[name]
}

func (e *env) serviceProxyCount() int {
	e.proxy.lock.RLock()
	defer e.proxy.lock.RUnlock()
	return len(e.proxy.ServiceProxies)
}

func (e *env) message(method *gotogrpc.GRPCServiceMethod, text string) proto.Message {
	in := dynamicpb.NewMessage(method.InputType())
	in.Set(method.InputType().Fields().ByName("text"), protoreflect.ValueOfString(text))
	return in
}

func textOf(m proto.Message) string {
	return m.ProtoReflect().Get(m.ProtoReflect().Descriptor().Fields().ByName("text")).String()
}

func (e *env) call(method, text string, md metadata.MD) ([]proto.Message, metadata.MD, error) {
	e.t.Helper()
	m := e.front.Methods[method]
	out, hdr, _, err := ProxyGRPCUnary(context.Background(), e.port, m, md, []proto.Message{e.message(m, text)})
	return out, hdr, err
}

// ---------------------------------------------------------------------------------------------------------------
// Setting up proxies

func TestSetupGRPCServiceProxyDefaults(t *testing.T) {
	e := newEnv(t)
	up := startUpstream(t)
	e.setup(up.addr, nil)
	sp := e.serviceProxy(frontName)
	if sp == nil {
		t.Fatal("no service proxy was added")
	}
	if sp.FromService != frontName || sp.ToService != echoName || sp.Port != e.port || sp.Upstream.Endpoint != up.addr || sp.Upstream.ID != echoName ||
		sp.Upstream.client == nil || sp.Upstream.ActiveSessions == nil || sp.Upstream.PastSessions == nil {
		t.Errorf("service proxy = %+v, upstream %+v", sp, sp.Upstream)
	}
	if _, all := sp.Methods["*"]; !all || len(sp.Methods) != 1 {
		t.Errorf("methods = %v, want the one for all of them", sp.Methods)
	}
	if sp.Config == nil || sp.Config.Delay != nil {
		t.Errorf("config = %+v, want one without a delay", sp.Config)
	}
	from, to, tee := gotogrpc.ServiceRegistry.GetProxyService(frontName)
	if from == nil || to == nil || to.Name != echoName || tee != 0 {
		t.Errorf("proxy service in the registry = %v, %v, %d", from, to, tee)
	}
}

func TestSetupGRPCServiceProxyWithDelayAndMethods(t *testing.T) {
	e := newEnv(t)
	up := startUpstream(t)
	if err := e.proxy.SetupGRPCServiceProxy(frontName, echoName, map[string]string{"Say": "Extra"}, up.addr, "auth", 0, 10*time.Millisecond, 20*time.Millisecond, 3, nil); err != nil {
		t.Fatal(err)
	}
	sp := e.serviceProxy(frontName)
	uri := e.front.Methods["Say"].GetURI()
	if sp.Methods[uri] != "Extra" || len(sp.Methods) != 1 {
		t.Errorf("methods = %v, want %s mapped to Extra", sp.Methods, uri)
	}
	if sp.Config == nil || sp.Config.Delay == nil || sp.Config.Delay.Count != 3 || sp.Config.Delay.Min.Duration != 10*time.Millisecond || sp.Config.Delay.Max.Duration != 20*time.Millisecond {
		t.Errorf("config = %+v", sp.Config)
	}
	if sp.Upstream.Authority != "auth" {
		t.Errorf("authority = %q", sp.Upstream.Authority)
	}
}

func TestSetupGRPCServiceProxyReplacesTheProxyOfTheService(t *testing.T) {
	e := newEnv(t)
	up := startUpstream(t)
	e.setup(up.addr, nil)
	first := e.serviceProxy(frontName)
	e.setup(up.addr, map[string]string{"Say": "Say"})
	second := e.serviceProxy(frontName)
	if first == second || e.serviceProxyCount() != 1 || len(second.Methods) != 1 {
		t.Errorf("service proxies = %d, methods %v", e.serviceProxyCount(), second.Methods)
	}
}

func TestSetupGRPCServiceProxyFailures(t *testing.T) {
	up := startUpstream(t)
	tests := []struct {
		name    string
		from    string
		to      string
		methods map[string]string
		addr    string
		fail    bool
		errPart string
	}{
		{"unknown service", "proxytest.Nope", echoName, nil, up.addr, false, "no service found for [proxytest.Nope]"},
		{"unknown target service", frontName, "proxytest.Nope", nil, up.addr, false, "no service found for [proxytest.Nope]"},
		{"unknown method", frontName, echoName, map[string]string{"Nope": "Say"}, up.addr, false, "Method [Nope] not found"},
		{"unknown target method", frontName, echoName, map[string]string{"Say": "Nope"}, up.addr, false, "Method [Nope] not found"},
		{"target method of the source service only", frontName, echoName, map[string]string{"Extra": "Say"}, up.addr, false, "Method [Extra] not found"},
		{"no endpoint", frontName, echoName, nil, "", false, "no service/endpoint"},
		{"invalid endpoint", frontName, echoName, nil, "not-an-address", false, "invalid endpoint"},
		{"server could not intercept", frontName, echoName, nil, up.addr, true, "could not setup proxy"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			manager.fail.Store(tc.fail)
			err := e.proxy.SetupGRPCServiceProxy(tc.from, tc.to, tc.methods, tc.addr, "", 0, 0, 0, 0, nil)
			if err == nil || !strings.Contains(err.Error(), tc.errPart) {
				t.Fatalf("error = %v, want one with %q", err, tc.errPart)
			}
			if n := e.serviceProxyCount(); n != 0 {
				t.Errorf("%d service proxies were left by a failed setup", n)
			}
			if from, _, _ := gotogrpc.ServiceRegistry.GetProxyService(frontName); from != nil {
				t.Error("a failed setup left the service proxied in the registry")
			}
		})
	}
}

func TestNewGRPCServiceProxy(t *testing.T) {
	e := newEnv(t)
	if sp, err := newGRPCServiceProxy(1, nil, e.echo, "127.0.0.1:1", "", nil); err == nil || sp != nil {
		t.Errorf("proxy without a source service = %v, %v", sp, err)
	}
	if sp, err := newGRPCServiceProxy(1, e.front, e.echo, "", "", nil); err == nil || sp != nil {
		t.Errorf("proxy without an endpoint = %v, %v", sp, err)
	}
	if sp, err := newGRPCServiceProxy(1, e.front, e.echo, "garbage", "", nil); err == nil || sp != nil {
		t.Errorf("proxy with an invalid endpoint = %v, %v", sp, err)
	}
	sp, err := newGRPCServiceProxy(7, e.front, nil, "127.0.0.1:9", "a", NewGRPCProxyTracker())
	if err != nil {
		t.Fatal(err)
	}
	if sp.ToService != frontName || sp.FromService != frontName || sp.Port != 7 || sp.Label != "Proxy("+frontName+")" || sp.Config == nil || sp.tracker == nil {
		t.Errorf("proxy without a target service = %+v", sp)
	}
}

func TestValidate(t *testing.T) {
	setupGlobals()
	valid := func() *GRPCServiceProxy {
		return &GRPCServiceProxy{FromService: frontName, ToService: echoName, Upstream: &GRPCUpstream{Endpoint: "127.0.0.1:1"}}
	}
	tests := []struct {
		name    string
		change  func(*GRPCServiceProxy)
		errPart string
	}{
		{"valid", func(*GRPCServiceProxy) {}, ""},
		{"valid with methods", func(sp *GRPCServiceProxy) { sp.Methods = map[string]string{"Say": "Extra"} }, ""},
		{"no source", func(sp *GRPCServiceProxy) { sp.FromService = "" }, "Intercepted Service Name Required"},
		{"no target", func(sp *GRPCServiceProxy) { sp.ToService = "" }, "Target Service Name Required"},
		{"no upstream", func(sp *GRPCServiceProxy) { sp.Upstream = nil }, "Upstrem Required"},
		{"unknown source", func(sp *GRPCServiceProxy) { sp.FromService = "proxytest.Nope" }, "no service found for [proxytest.Nope]"},
		{"unknown target", func(sp *GRPCServiceProxy) { sp.ToService = "proxytest.Nope" }, "no service found for [proxytest.Nope]"},
		{"unknown method", func(sp *GRPCServiceProxy) { sp.Methods = map[string]string{"Nope": "Say"} }, "method [Nope] not found in service [proxytest.Front]"},
		{"unknown target method", func(sp *GRPCServiceProxy) { sp.Methods = map[string]string{"Say": "Nope"} }, "method [Nope] not found in service [proxytest.Echo]"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sp := valid()
			tc.change(sp)
			err := sp.Validate()
			if tc.errPart == "" {
				if err != nil {
					t.Fatalf("Validate = %v", err)
				}
				if sp.proxyService == nil || sp.targetService == nil || sp.Config == nil || sp.Config.Delay == nil || sp.Config.Delay.IsNonZero() {
					t.Errorf("validated proxy = %+v config %+v", sp, sp.Config)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.errPart) {
				t.Errorf("Validate = %v, want an error with %q", err, tc.errPart)
			}
		})
	}
}

// ---------------------------------------------------------------------------------------------------------------
// Proxy state

func TestPortProxyManagement(t *testing.T) {
	e := newEnv(t)
	if GetPortProxy(e.port) != e.proxy {
		t.Error("GetPortProxy returned another proxy for the port")
	}
	if !e.proxy.Enabled || e.proxy.Port != e.port || e.proxy.Tracker == nil || e.proxy.ServiceProxies == nil || e.proxy.TeeServices == nil {
		t.Errorf("new proxy = %+v", e.proxy)
	}
	p := newGRPCProxy(e.port)
	p.Tracker = nil
	AddPortProxy(p)
	if GetPortProxy(e.port) != p || p.Tracker == nil {
		t.Error("AddPortProxy did not make the proxy the one of the port, with a tracker")
	}
	RemovePortProxy(e.port)
	if q := GetPortProxy(e.port); q == p {
		t.Error("the removed proxy is still the one of the port")
	}
}

func TestProxyClearAndRemoveServiceProxy(t *testing.T) {
	e := newEnv(t)
	up := startUpstream(t)
	e.setup(up.addr, nil)
	e.proxy.Tracker.IncrementConnCounts("x")
	oldTracker := e.proxy.Tracker
	e.proxy.RemoveServiceProxy("proxytest.Unknown")
	if e.serviceProxyCount() != 1 {
		t.Fatal("removing an unknown service proxy changed the proxy")
	}
	e.proxy.RemoveServiceProxy(frontName)
	if e.serviceProxyCount() != 0 {
		t.Error("the service proxy was not removed")
	}
	e.setup(up.addr, nil)
	e.proxy.TeeServices[frontName] = map[string]*GRPCSessionLog{"*": newGRPCSessionLog()}
	e.proxy.Clear()
	if e.serviceProxyCount() != 0 || len(e.proxy.TeeServices) != 0 || e.proxy.Tracker == oldTracker || e.proxy.Tracker.ConnCount != 0 {
		t.Errorf("proxy after Clear = %+v", e.proxy)
	}
}

func TestGetPortProxyReturnsOneProxyPerPort(t *testing.T) {
	setupGlobals()
	const ports, callers = 100, 16
	base := int(60000 + portSeq.Add(1)*ports)
	t.Cleanup(func() {
		for p := 0; p < ports; p++ {
			RemovePortProxy(base + p)
		}
	})
	got := make([][callers]*GRPCProxy, ports)
	for p := 0; p < ports; p++ {
		start := make(chan struct{})
		var wg sync.WaitGroup
		for c := 0; c < callers; c++ {
			wg.Add(1)
			go func(p, c int) {
				defer wg.Done()
				<-start
				got[p][c] = GetPortProxy(base + p)
			}(p, c)
		}
		close(start)
		wg.Wait()
	}
	for p := 0; p < ports; p++ {
		for c := 1; c < callers; c++ {
			if got[p][c] != got[p][0] {
				t.Fatalf("port %d: callers got different proxies", base+p)
			}
		}
	}
}

// ---------------------------------------------------------------------------------------------------------------
// The decision to proxy

func TestWillProxyGRPC(t *testing.T) {
	e := newEnv(t)
	say, other := e.front.Methods["Say"], e.front.Methods["Other"]
	if WillProxyGRPC(e.port, say) {
		t.Error("a port without proxies proxies")
	}
	up := startUpstream(t)
	e.setup(up.addr, nil)
	if !WillProxyGRPC(e.port, say) || !WillProxyGRPC(e.port, other) {
		t.Error("a proxy of all the methods of the service does not proxy them")
	}
	if WillProxyGRPC(e.port, e.echo.Methods["Say"]) {
		t.Error("a service that is not proxied is proxied")
	}
	e.proxy.lock.Lock()
	e.proxy.Enabled = false
	e.proxy.lock.Unlock()
	if WillProxyGRPC(e.port, say) {
		t.Error("a disabled proxy proxies")
	}
	e.proxy.lock.Lock()
	e.proxy.Enabled = true
	e.proxy.lock.Unlock()
	e.setup(up.addr, map[string]string{"Say": "Say"})
	if !WillProxyGRPC(e.port, say) {
		t.Error("a method that the proxy was set up for is not proxied")
	}
	if WillProxyGRPC(e.port, other) {
		t.Error("a method that the proxy was not set up for is proxied")
	}
	if WillProxyGRPC(e.port+1000, say) {
		t.Error("another port proxies")
	}
	if WillProxyGRPC(e.port, nil) {
		t.Error("no method is proxied")
	}
}

func TestWillProxyGRPCTeeServices(t *testing.T) {
	e := newEnv(t)
	say, other := e.front.Methods["Say"], e.front.Methods["Other"]
	e.proxy.lock.Lock()
	e.proxy.TeeServices[frontName] = map[string]*GRPCSessionLog{"Say": newGRPCSessionLog()}
	e.proxy.lock.Unlock()
	if !WillProxyGRPC(e.port, say) {
		t.Error("a method with a recorded call is not served by the proxy")
	}
	if WillProxyGRPC(e.port, other) {
		t.Error("a method without a recorded call is served by the proxy")
	}
	e.proxy.lock.Lock()
	e.proxy.TeeServices[frontName]["*"] = newGRPCSessionLog()
	e.proxy.lock.Unlock()
	if !WillProxyGRPC(e.port, other) {
		t.Error("a method is not served by the recording for all the methods")
	}
}

func TestWillProxyGRPCHookIgnoresWhatIsNotAMethod(t *testing.T) {
	e := newEnv(t)
	up := startUpstream(t)
	e.setup(up.addr, nil)
	if !util.WillProxyGRPC(e.port, e.front.Methods["Say"]) {
		t.Error("the hook of the server does not proxy a method of a proxied service")
	}
	if util.WillProxyGRPC(e.port, "not a method") || util.WillProxyGRPC(e.port, nil) {
		t.Error("the hook of the server proxies what is not a method")
	}
}

// ---------------------------------------------------------------------------------------------------------------
// Unary calls

func TestProxyGRPCUnaryCallsUpstream(t *testing.T) {
	e := newEnv(t)
	up := startUpstream(t)
	e.setup(up.addr, nil)
	out, hdr, err := e.call("Say", "hello", metadata.Pairs("x-in", "1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || textOf(out[0]) != "say:hello" {
		t.Fatalf("output = %v", out)
	}
	calls := up.recorded()
	if len(calls) != 1 || calls[0].method != "Say" || calls[0].text != "hello" || calls[0].md.Get("x-in")[0] != "1" {
		t.Errorf("upstream calls = %+v", calls)
	}
	if hdr.Get("x-upstream")[0] != "Say" {
		t.Errorf("headers of the upstream are not passed on: %v", hdr)
	}
	if len(hdr.Get(constants.HeaderGotoProxyUpstreamTook)) != 1 || len(hdr.Get(constants.HeaderViaGoto)) == 0 {
		t.Errorf("response headers = %v, want the headers of the proxy", hdr)
	}
	tr := e.proxy.Tracker
	if tr.ConnCount != 1 || tr.ConnCountByUpstream[echoName] != 1 || tr.RequestCountByUpstream[echoName] != 1 || tr.ResponseCountByUpstream[echoName] != 1 ||
		tr.RequestCountBySvcMethod[frontName]["Say"] != 1 || tr.MessageCountByType["Req"] != 1 || tr.MessageCountByType["Res"] != 1 {
		t.Errorf("tracker = %+v", tr)
	}
}

func TestProxyGRPCUnaryUsesTheMappedTargetMethod(t *testing.T) {
	e := newEnv(t)
	up := startUpstream(t)
	e.setup(up.addr, map[string]string{"Say": "Extra", "Other": "Other"})
	out, _, err := e.call("Say", "hi", nil)
	if err != nil {
		t.Fatal(err)
	}
	if textOf(out[0]) != "extra:hi" {
		t.Errorf("output = %q, want the answer of the method that Say is mapped to", textOf(out[0]))
	}
	if _, _, err := e.call("Other", "o", nil); err != nil {
		t.Fatal(err)
	}
	calls := up.recorded()
	if len(calls) != 2 || calls[0].method != "Extra" || calls[1].method != "Other" {
		t.Errorf("upstream calls = %+v, want Extra and Other", calls)
	}
}

func TestProxyGRPCUnarySendsTheInputOfTheTargetMethod(t *testing.T) {
	e := newEnv(t)
	up := startUpstream(t)
	e.setup(up.addr, nil)
	e.echo.Methods["Say"].In = func(m proto.Message) proto.Message {
		out := dynamicpb.NewMessage(m.ProtoReflect().Descriptor())
		out.Set(out.Descriptor().Fields().ByName("text"), protoreflect.ValueOfString(strings.ToUpper(textOf(m))))
		return out
	}
	if _, _, err := e.call("Say", "shout", nil); err != nil {
		t.Fatal(err)
	}
	if calls := up.recorded(); len(calls) != 1 || calls[0].text != "SHOUT" {
		t.Errorf("upstream calls = %+v, want the input as the target method gets it", calls)
	}
}

func TestProxyGRPCUnaryDelaysCalls(t *testing.T) {
	e := newEnv(t)
	up := startUpstream(t)
	if err := e.proxy.SetupGRPCServiceProxy(frontName, echoName, nil, up.addr, "", 0, 40*time.Millisecond, 40*time.Millisecond, 1, nil); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, hdr, err := e.call("Say", "slow", nil)
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took < 40*time.Millisecond {
		t.Errorf("call took %s, want it delayed by 40ms", took)
	}
	if got := hdr.Get(constants.HeaderGotoProxyDelay); len(got) != 1 || got[0] != "40ms" {
		t.Errorf("delay header = %v, want 40ms", got)
	}
	// The delay was for one call.
	_, hdr, err = e.call("Say", "fast", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := hdr.Get(constants.HeaderGotoProxyDelay); len(got) != 0 {
		t.Errorf("delay header of the call after the delay was used up = %v", got)
	}
}

func TestProxyGRPCUnaryFailures(t *testing.T) {
	e := newEnv(t)
	if _, _, err := e.call("Say", "x", nil); err == nil || !strings.Contains(err.Error(), "No proxy mapping") {
		t.Errorf("call of a service that is not proxied = %v", err)
	}
	// An upstream that is not there.
	e.setup("127.0.0.1:1", nil)
	if out, _, err := e.call("Say", "x", nil); err == nil || len(out) != 0 {
		t.Errorf("call to an upstream that is not there = %v, %v", out, err)
	}
	if tr := e.proxy.Tracker; tr.ConnCount != 1 || tr.ResponseCountByUpstream[echoName] != 0 {
		t.Errorf("tracker = %+v, want the failed call counted as a connection without a response", tr)
	}
	// A service that the registry doesn't have as proxied anymore.
	gotogrpc.ServiceRegistry.RemoveProxyService(frontName)
	if _, _, err := e.call("Say", "x", nil); err == nil || !strings.Contains(err.Error(), "No target service") {
		t.Errorf("call of a service that is not in the registry as proxied = %v", err)
	}
}

func TestProxyGRPCUnaryRecordsAndReplaysCalls(t *testing.T) {
	e := newEnv(t)
	teePort := e.port + 5000
	t.Cleanup(func() { RemovePortProxy(teePort) })
	up := startUpstream(t)
	if err := e.proxy.SetupGRPCServiceProxy(frontName, echoName, nil, up.addr, "", teePort, 0, 0, 0, nil); err != nil {
		t.Fatal(err)
	}
	if out, _, err := e.call("Say", "recorded", nil); err != nil || textOf(out[0]) != "say:recorded" {
		t.Fatalf("call = %v, %v", out, err)
	}
	teeProxy := GetPortProxy(teePort)
	teeProxy.lock.RLock()
	log := teeProxy.TeeServices[frontName]["Say"]
	teeProxy.lock.RUnlock()
	if log == nil || len(log.ClientMessageLog) != 1 || len(log.ServerMessageLog) != 1 || len(log.ClientHeaders) != 1 || len(log.ServerHeaders) != 1 {
		t.Fatalf("recorded session log = %+v", log)
	}
	// The port that the calls are recorded for serves them from the recording, without the upstream.
	say := e.front.Methods["Say"]
	if !WillProxyGRPC(teePort, say) {
		t.Fatal("the port of the recording does not proxy the recorded method")
	}
	out, hdr, _, err := ProxyGRPCUnary(context.Background(), teePort, say, nil, []proto.Message{e.message(say, "again")})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || textOf(out[0]) != "say:recorded" {
		t.Errorf("replayed output = %v, want the recorded response", out)
	}
	if len(hdr.Get(constants.HeaderViaGoto)) == 0 {
		t.Errorf("replayed headers = %v", hdr)
	}
	if n := len(up.recorded()); n != 1 {
		t.Errorf("upstream received %d calls, want only the one that was recorded", n)
	}
}

func TestTeeProxyGRPCUnaryWithoutLogOrMessages(t *testing.T) {
	e := newEnv(t)
	say := e.front.Methods["Say"]
	if out, hdr, _, err := e.proxy.teeProxyGRPCUnary(say, nil); out != nil || hdr != nil || err != nil {
		t.Errorf("replay of no log = %v, %v, %v", out, hdr, err)
	}
	if out, _, _, err := e.proxy.teeProxyGRPCUnary(say, newGRPCSessionLog()); len(out) != 0 || err != nil {
		t.Errorf("replay of an empty log = %v, %v", out, err)
	}
	bad := newGRPCSessionLog()
	bad.ServerMessageLog[1] = map[string]any{"nope": 1}
	if _, _, _, err := e.proxy.teeProxyGRPCUnary(say, bad); err == nil {
		t.Error("replay of a message that is not one of the method succeeded")
	}
}

// ---------------------------------------------------------------------------------------------------------------
// Streams

// fakeStream is the downstream of a stream: it keeps what it is sent, and has nothing to receive.
type fakeStream struct {
	gotogrpc.GRPCStream
	lock    sync.Mutex
	sent    []proto.Message
	headers int
}

func (f *fakeStream) ReceiveAndDiscard() (int, error) { return 0, nil }
func (f *fakeStream) SendHeaders(metadata.MD) error {
	f.lock.Lock()
	defer f.lock.Unlock()
	f.headers++
	return nil
}
func (f *fakeStream) Send(m proto.Message) error {
	f.lock.Lock()
	defer f.lock.Unlock()
	f.sent = append(f.sent, m)
	return nil
}

func TestTeeProxyGRPCStreamReplaysTheRecordedMessages(t *testing.T) {
	e := newEnv(t)
	watch := e.front.Methods["Watch"]
	log := newGRPCSessionLog()
	log.ServerHeaders[1] = metadata.Pairs("x-h", "v")
	log.ServerMessageLog[2] = map[string]any{"text": "one"}
	log.ServerMessageLog[3] = map[string]any{"text": "two"}
	down := &fakeStream{}
	if _, sent, err := e.proxy.teeProxyGRPCStream(watch, down, log); err != nil || sent != 2 {
		t.Fatalf("replay = sent %d, %v", sent, err)
	}
	if len(down.sent) != 2 || textOf(down.sent[0]) != "one" || textOf(down.sent[1]) != "two" || down.headers != 1 {
		t.Errorf("sent %d messages (%d headers)", len(down.sent), down.headers)
	}
}

func TestTeeProxyGRPCStreamWithNothingRecorded(t *testing.T) {
	e := newEnv(t)
	watch := e.front.Methods["Watch"]
	down := &fakeStream{}
	if _, sent, err := e.proxy.teeProxyGRPCStream(watch, down, nil); sent != 0 || err != nil {
		t.Errorf("replay of no log = sent %d, %v", sent, err)
	}
	if _, sent, err := e.proxy.teeProxyGRPCStream(watch, down, newGRPCSessionLog()); sent != 0 || err != nil {
		t.Errorf("replay of an empty log = sent %d, %v", sent, err)
	}
	if len(down.sent) != 0 {
		t.Errorf("sent %d messages from an empty log", len(down.sent))
	}
}

// ---------------------------------------------------------------------------------------------------------------
// Concurrency

func TestProxyGRPCConcurrentCallsAndChanges(t *testing.T) {
	e := newEnv(t)
	up := startUpstream(t)
	e.setup(up.addr, nil)
	say := e.front.Methods["Say"]
	const callers, rounds = 4, 5
	var wg sync.WaitGroup
	var failures atomic.Int32
	for c := 0; c < callers; c++ {
		wg.Add(1)
		go func(c int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				if !WillProxyGRPC(e.port, say) {
					failures.Add(1)
				}
				if _, _, _, err := ProxyGRPCUnary(context.Background(), e.port, say, nil, []proto.Message{e.message(say, fmt.Sprintf("%d-%d", c, i))}); err != nil {
					failures.Add(1)
				}
			}
		}(c)
	}
	// Other services are set up, removed and reported along with the calls.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			e.proxy.mu(func() { e.proxy.TeeServices[echoName] = map[string]*GRPCSessionLog{"x": newGRPCSessionLog()} })
			e.proxy.RemoveServiceProxy("proxytest.Other")
			if _, err := json.Marshal(e.proxy); err != nil {
				failures.Add(1)
			}
		}
	}()
	wg.Wait()
	if failures.Load() != 0 {
		t.Errorf("%d calls or decisions failed", failures.Load())
	}
	if n := len(up.recorded()); n != callers*rounds {
		t.Errorf("upstream received %d calls, want %d", n, callers*rounds)
	}
	if tr := e.proxy.Tracker; tr.ConnCount != callers*rounds || tr.RequestCountByUpstream[echoName] != callers*rounds {
		t.Errorf("tracker = %+v", tr)
	}
}

func (p *GRPCProxy) mu(f func()) {
	p.lock.Lock()
	defer p.lock.Unlock()
	f()
}

// bidiStream is the downstream of a stream both ways: the messages that its client sends are given to the channel that
// the replay reads, and the ones that it is sent are kept.
type bidiStream struct {
	fakeStream
	clientMessages []proto.Message
}

func (b *bidiStream) TeeStreamReceive(in, _ chan proto.Message) (int, error) {
	defer close(in)
	for _, m := range b.clientMessages {
		in <- m
	}
	return len(b.clientMessages), nil
}

// A stream both ways is replayed from the log in the turns that the log has: a log that has the server answer after
// all the messages of the client (the proxy logs that when the client is faster than the upstream) is answered after the
// last of them, and not never, as it was when the replay took the numbers of the log for the numbers of the messages.
func TestTeeProxyGRPCStreamReplaysAStreamBothWays(t *testing.T) {
	e := newEnv(t)
	bidi := *e.front.Methods["Watch"]
	bidi.IsBidiStream, bidi.IsClientStream = true, true
	in := func(texts ...string) []proto.Message {
		var msgs []proto.Message
		for _, text := range texts {
			msgs = append(msgs, e.message(e.front.Methods["Say"], text))
		}
		return msgs
	}
	texts := func(down *bidiStream) []string {
		down.lock.Lock()
		defer down.lock.Unlock()
		var got []string
		for _, m := range down.sent {
			got = append(got, textOf(m))
		}
		return got
	}
	for name, tt := range map[string]struct {
		clientIDs, serverIDs []int
		clientCount          int
		want                 []string
	}{
		// the client sent 3 messages (1, 3, 4) and the server answered them all (6, 7, 8), as the logs of a proxy have it
		"the server answers after all the messages of the client": {[]int{1, 3, 4}, []int{6, 7, 8}, 3, []string{"s6", "s7", "s8"}},
		"the server and the client take turns":                    {[]int{1, 3, 5}, []int{2, 4, 6}, 3, []string{"s2", "s4", "s6"}},
		"the server sends before the client does":                 {[]int{3}, []int{1, 2, 4}, 1, []string{"s1", "s2", "s4"}},
		"the client sends no more than it did":                    {[]int{1, 2}, []int{3}, 0, []string{"s3"}},
	} {
		t.Run(name, func(t *testing.T) {
			log := newGRPCSessionLog()
			var clientTexts []string
			for _, id := range tt.clientIDs {
				log.ClientMessageLog[id] = map[string]any{"text": fmt.Sprintf("c%d", id)}
			}
			for i := 0; i < tt.clientCount; i++ {
				clientTexts = append(clientTexts, fmt.Sprintf("c%d", i))
			}
			for _, id := range tt.serverIDs {
				log.ServerMessageLog[id] = map[string]any{"text": fmt.Sprintf("s%d", id)}
			}
			down := &bidiStream{clientMessages: in(clientTexts...)}
			done := make(chan struct{})
			var sent int
			var err error
			go func() {
				defer close(done)
				_, sent, err = e.proxy.teeProxyGRPCStream(&bidi, down, log)
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("the replay does not end")
			}
			if got := texts(down); err != nil || sent != len(tt.want) || fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Errorf("replay sent %d %v (%v), want %v", sent, got, err, tt.want)
			}
		})
	}
}

// The messages of the server of a stream were logged as they are, as messages and not as JSON as the messages of the
// client are, so the status of the proxy had {} for each of them, and replaying the log made empty messages.
func TestSessionLogKeepsTheMessagesOfTheServerAsJSON(t *testing.T) {
	e := newEnv(t)
	say := e.front.Methods["Say"]
	log := newGRPCSessionLog()
	done := make(chan struct{})
	go func() {
		defer close(done)
		log.start()
	}()
	log.serverStream() <- e.message(say, "from the server")
	log.clientStream() <- e.message(say, "from the client")
	close(log.clientStream())
	close(log.serverStream())
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the log does not end with its channels")
	}
	b, err := json.Marshal(log.ServerMessageLog)
	if err != nil || !strings.Contains(string(b), "from the server") {
		t.Errorf("server messages of the log = %s (%v), want the text of the message", b, err)
	}
	for _, logged := range log.ServerMessageLog {
		if msg, err := e.proxy.createResponse(say, logged); err != nil || textOf(msg) != "from the server" {
			t.Errorf("replayed message = %v (%v)", msg, err)
		}
	}
}

// The session closes the channel of the client before the one of the server, and the log stopped at the first one that
// was closed, so the messages of the server that were still in its channel (the last ones of a stream) were never logged,
// and a replay of the log had fewer messages than the stream had.
func TestSessionLogKeepsTheMessagesLeftWhenAChannelIsClosed(t *testing.T) {
	e := newEnv(t)
	say := e.front.Methods["Say"]
	for round := 0; round < 50; round++ {
		log := newGRPCSessionLog()
		for i := 0; i < 5; i++ {
			log.serverStream() <- e.message(say, fmt.Sprintf("s%d", i))
		}
		log.clientStream() <- e.message(say, "c")
		close(log.clientStream())
		close(log.serverStream())
		done := make(chan struct{})
		go func() {
			defer close(done)
			log.start()
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("the log does not end when its channels are closed")
		}
		if len(log.ServerMessageLog) != 5 || len(log.ClientMessageLog) != 1 {
			t.Fatalf("round %d: logged %d server and %d client messages, want 5 and 1", round, len(log.ServerMessageLog), len(log.ClientMessageLog))
		}
	}
}
