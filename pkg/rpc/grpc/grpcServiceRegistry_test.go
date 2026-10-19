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
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goto/pkg/global"
	"goto/pkg/rpc"
	"goto/pkg/server/hooks"
	"goto/pkg/util"

	"github.com/gorilla/mux"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

const (
	healthService = "grpc.health.v1.Health"
	healthURI     = "/grpc.health.v1.health"
)

var (
	stubOnce sync.Once
	portSeq  atomic.Int32
	nameSeq  atomic.Int32
)

func stubGlobalFuncs() {
	global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsListenerPresent = func(int) bool { return false }
	global.Funcs.IsListenerOpen = func(int) bool { return false }
	global.Funcs.GetListenerID = func(int) string { return "" }
	global.Funcs.GetListenerLabel = func(*http.Request) string { return "" }
	global.Funcs.GetListenerLabelForPort = func(int) string { return "" }
	util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
	// Created by the server at startup. Setting a response payload builds its URI matcher on util.MatchRouter.
	if util.MatchRouter == nil {
		util.CreateRouters(mux.NewRouter())
	}
}

// uniquePort returns a port that no other test uses: hooks, trackers and payloads are kept by port and can't be reset.
func uniquePort() int {
	stubOnce.Do(stubGlobalFuncs)
	return int(43000 + portSeq.Add(1))
}

// tagHandler is the unary handler that the tests give to the services: it answers with the URI of the method that it
// was built for, which tells which method a service dispatches to.
func tagHandler(m *GRPCServiceMethod) grpc.MethodHandler {
	uri := m.URI
	return func(any, context.Context, func(any) error, grpc.UnaryServerInterceptor) (any, error) {
		return uri, nil
	}
}

// stubHandlers sets the handler factories that the server package provides at startup, and puts the previous ones back.
func stubHandlers(t *testing.T) {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
	prevUnary, prevStream := GRPCUnaryHandler, GRPCStreamHandler
	GRPCUnaryHandler = tagHandler
	GRPCStreamHandler = func(any, grpc.ServerStream) error { return nil }
	t.Cleanup(func() { GRPCUnaryHandler, GRPCStreamHandler = prevUnary, prevStream })
}

// healthDesc returns the descriptors of the gRPC health service: unary Check and List, and the server stream Watch. The
// service descriptor is a copy that can be changed (the registry sets the handler type, and keeps pointers into the
// methods and streams).
func healthDesc() (*grpc.ServiceDesc, protoreflect.ServiceDescriptor) {
	sd := grpc_health_v1.Health_ServiceDesc
	sd.Methods = append([]grpc.MethodDesc(nil), sd.Methods...)
	sd.Streams = append([]grpc.StreamDesc(nil), sd.Streams...)
	return &sd, grpc_health_v1.File_grpc_health_v1_health_proto.Services().ByName("Health")
}

func newRegistry() *GRPCServiceRegistry {
	r := &GRPCServiceRegistry{}
	r.Init()
	return r
}

var (
	echoOnce sync.Once
	echoPSD  protoreflect.ServiceDescriptor
)

// echoDesc returns the descriptors of a service that has all four kinds of methods, each with a message that has a
// string field: Unary, ClientStream, ServerStream and Bidi.
func echoDesc() protoreflect.ServiceDescriptor {
	echoOnce.Do(func() {
		msg := ".gototest.Msg"
		method := func(name string, clientStream, serverStream bool) *descriptorpb.MethodDescriptorProto {
			return &descriptorpb.MethodDescriptorProto{
				Name: &name, InputType: &msg, OutputType: &msg, ClientStreaming: &clientStream, ServerStreaming: &serverStream,
			}
		}
		str, optional := descriptorpb.FieldDescriptorProto_TYPE_STRING, descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL
		fdp := &descriptorpb.FileDescriptorProto{
			Name:    ptr("gototest/echo.proto"),
			Package: ptr("gototest"),
			Syntax:  ptr("proto3"),
			MessageType: []*descriptorpb.DescriptorProto{{
				Name:  ptr("Msg"),
				Field: []*descriptorpb.FieldDescriptorProto{{Name: ptr("text"), Number: ptr(int32(1)), Type: &str, Label: &optional, JsonName: ptr("text")}},
			}},
			Service: []*descriptorpb.ServiceDescriptorProto{{
				Name: ptr("Echo"),
				Method: []*descriptorpb.MethodDescriptorProto{
					method("Unary", false, false),
					method("ClientStream", true, false),
					method("ServerStream", false, true),
					method("Bidi", true, true),
				},
			}},
		}
		fd, err := protodesc.NewFile(fdp, nil)
		if err != nil {
			panic(err)
		}
		echoPSD = fd.Services().Get(0)
	})
	return echoPSD
}

// echoMessage returns a message of the echo service with the given text.
func echoMessage(text string) proto.Message {
	desc := echoDesc().Methods().ByName("Unary").Input()
	m := dynamicpb.NewMessage(desc)
	m.Set(desc.Fields().ByName("text"), protoreflect.ValueOfString(text))
	return m
}

func ptr[T any](v T) *T { return &v }

func TestRegistryInitResetsEverything(t *testing.T) {
	stubHandlers(t)
	r := newRegistry()
	_, psd := healthDesc()
	s := r.NewGRPCService(psd)
	r.AddActiveService(s)
	r.AddProxyService(s, s, 9999)
	r.Init()
	if len(r.Services) != 0 || len(r.ActiveServices) != 0 || len(r.ProxyServices) != 0 || len(r.TeeServices) != 0 {
		t.Errorf("Init must clear all the services, got %d/%d/%d/%d", len(r.Services), len(r.ActiveServices), len(r.ProxyServices), len(r.TeeServices))
	}
}

func TestNewGRPCServiceBuildsServiceFromDescriptor(t *testing.T) {
	stubHandlers(t)
	r := newRegistry()
	_, psd := healthDesc()
	s := r.NewGRPCService(psd)

	if s.Name != healthService || s.URI != healthURI {
		t.Errorf("name %q uri %q, want %q %q", s.Name, s.URI, healthService, healthURI)
	}
	if r.GetService(healthService) != s {
		t.Errorf("the service must be registered by its name")
	}
	if s.GetMethodCount() != 3 {
		t.Fatalf("methods = %d, want 3 (Check, List, Watch)", s.GetMethodCount())
	}
	check, list, watch := s.Methods["Check"], s.Methods["List"], s.Methods["Watch"]
	if !check.IsUnary || check.IsClientStream || check.IsServerStream || check.IsBidiStream || check.URI != healthURI+"/check" {
		t.Errorf("Check = %+v, want a unary method with URI %s/check", check, healthURI)
	}
	if !list.IsUnary {
		t.Errorf("List must be unary")
	}
	if watch.IsUnary || watch.IsClientStream || !watch.IsServerStream || watch.IsBidiStream {
		t.Errorf("Watch = %+v, want a server stream", watch)
	}
	if check.Service != s || watch.Service != s {
		t.Errorf("methods must point to their service")
	}
	if len(s.GSD.Methods) != 2 || len(s.GSD.Streams) != 1 || s.GSD.ServiceName != healthService {
		t.Errorf("service desc = %+v, want 2 unary methods and 1 stream", s.GSD)
	}
	if check.GMD == nil || check.GMD.MethodName != "Check" || watch.GSD == nil || watch.GSD.StreamName != "Watch" || !watch.GSD.ServerStreams || watch.GSD.ClientStreams {
		t.Errorf("method descriptors = %+v / %+v", check.GMD, watch.GSD)
	}
	if got, _ := check.GMD.Handler(nil, nil, nil, nil); got != healthURI+"/check" {
		t.Errorf("handler of Check was built for %v", got)
	}
}

func TestNewGRPCServiceFromDescriptorsUsesTheGivenServiceDesc(t *testing.T) {
	stubHandlers(t)
	r := newRegistry()
	sd, psd := healthDesc()
	s := r.NewGRPCServiceFromDescriptors(sd, psd)

	if s.GSD != sd || s.PSD != psd {
		t.Errorf("the given descriptors must be kept")
	}
	if sd.HandlerType != (*DynamicServiceInterface)(nil) {
		t.Errorf("handler type = %v, want the dynamic service interface", sd.HandlerType)
	}
	if s.Methods["Check"].GMD != &sd.Methods[0] || s.Methods["List"].GMD != &sd.Methods[1] || s.Methods["Watch"].GSD != &sd.Streams[0] {
		t.Errorf("methods must point into the methods and streams of the service desc")
	}
	if r.GetService(healthService) != s {
		t.Errorf("the service must be registered")
	}
}

func TestNewGRPCServiceFromSD(t *testing.T) {
	stubHandlers(t)
	r := newRegistry()
	sd, _ := healthDesc()
	s := r.NewGRPCServiceFromSD(sd)
	if s == nil || s.Name != healthService || s.GetMethodCount() != 3 {
		t.Fatalf("service = %+v, want the health service", s)
	}
	unknown := &grpc.ServiceDesc{ServiceName: "no.such.Service"}
	if got := r.NewGRPCServiceFromSD(unknown); got != nil {
		t.Errorf("a service whose descriptor can't be loaded must give nil, got %+v", got)
	}
}

func TestGetOrCreateService(t *testing.T) {
	stubHandlers(t)
	r := newRegistry()
	sd, _ := healthDesc()
	first := r.GetOrCreateService(sd)
	if first == nil || first.Name != healthService {
		t.Fatalf("service = %+v", first)
	}
	if again := r.GetOrCreateService(sd); again != first {
		t.Errorf("the service must be created once")
	}
}

func TestGetServiceAndGetRPCService(t *testing.T) {
	stubHandlers(t)
	r := newRegistry()
	_, psd := healthDesc()
	s := r.NewGRPCService(psd)
	if r.GetService(healthService) != s || r.GetService("nope") != nil {
		t.Errorf("GetService must find the registered service only")
	}
	if got := r.GetRPCService(healthService); got != s {
		t.Errorf("GetRPCService = %v, want the registered service", got)
	}
	if got := r.GetRPCService("nope"); got != nil {
		t.Errorf("GetRPCService of an unknown service must be a nil interface, got %#v", got)
	}
	r.RemoveService(healthService)
	if r.GetService(healthService) != nil {
		t.Errorf("service must be gone after RemoveService")
	}
}

func TestActiveProxyAndTeeServices(t *testing.T) {
	stubHandlers(t)
	r := newRegistry()
	_, psd := healthDesc()
	from := r.NewGRPCService(psd)
	to := &GRPCService{Name: "other.Service"}

	if r.GetActiveService(healthService) != nil {
		t.Errorf("no service is active at first")
	}
	r.AddActiveService(from)
	if r.GetActiveService(healthService) != from {
		t.Errorf("service must be active after AddActiveService")
	}
	r.RemoveActiveService(healthService)
	if r.GetActiveService(healthService) != nil {
		t.Errorf("service must not be active after RemoveActiveService")
	}

	if a, b, port := r.GetProxyService(healthService); a != nil || b != nil || port != 0 {
		t.Errorf("no proxy at first, got %v %v %d", a, b, port)
	}
	r.AddProxyService(from, to, 7070)
	if a, b, port := r.GetProxyService(healthService); a != from || b != to || port != 7070 {
		t.Errorf("proxy = %v %v %d, want %v %v 7070", a, b, port, from, to)
	}
	if a, b := r.GetTeeServices(7070); a != from || b != to {
		t.Errorf("tee = %v %v, want %v %v", a, b, from, to)
	}
	if a, b := r.GetTeeServices(1); a != nil || b != nil {
		t.Errorf("no tee on another port, got %v %v", a, b)
	}
	r.RemoveTeeService(7070)
	if a, b := r.GetTeeServices(7070); a != nil || b != nil {
		t.Errorf("tee must be gone after RemoveTeeService, got %v %v", a, b)
	}
	r.RemoveProxyService(healthService)
	if a, _, _ := r.GetProxyService(healthService); a != nil {
		t.Errorf("proxy must be gone after RemoveProxyService")
	}
	// a proxy without a tee port doesn't add a tee service
	r.AddProxyService(from, to, 0)
	if a, _ := r.GetTeeServices(0); a != nil {
		t.Errorf("a proxy without a tee port must not add a tee service")
	}
}

func TestRegistryTrackService(t *testing.T) {
	stubHandlers(t)
	port := uniquePort()
	hooks.GetPortHooks(port)
	r := newRegistry()
	_, psd := healthDesc()
	s := r.NewGRPCService(psd)

	r.TrackService(port, "no.such.Service", nil, "", "") // no service: nothing is tracked
	r.TrackService(port, healthService, nil, "", "")
	if got := rpc.GetRPCTracker(port).GetServiceTrackerJSON(s); got == nil {
		t.Errorf("service must be tracked after TrackService")
	}
}

func TestConvertToTargetService(t *testing.T) {
	r := newRegistry()
	sd, psd := healthDesc()
	sd.Metadata = psd // the descriptor of the service is available from the service desc
	gsd2, psd2, err := r.ConvertToTargetService(sd, "other.Health")
	if err != nil || gsd2 == nil || psd2 == nil {
		t.Fatalf("ConvertToTargetService = %v %v %v", gsd2, psd2, err)
	}
	if gsd2.ServiceName != "other.Health" || sd.ServiceName != healthService {
		t.Errorf("the target has name %q and the source %q, want other.Health and the unchanged source", gsd2.ServiceName, sd.ServiceName)
	}
	// without the descriptor in the metadata the target must be found by its name, which doesn't exist
	plain, _ := healthDesc()
	if gsd, psd, err := r.ConvertToTargetService(plain, "no.such.Target"); err == nil || gsd != nil || psd != nil {
		t.Errorf("an unknown target must fail, got %v %v %v", gsd, psd, err)
	}
}

func TestCloneService(t *testing.T) {
	stubHandlers(t)
	r := newRegistry()
	sd, psd := healthDesc()
	sd.Metadata = psd
	source := r.NewGRPCServiceFromDescriptors(sd, psd)
	source.Methods["Check"].SetStreamCount(5)

	clone, err := r.CloneService(source, "clone.Health")
	if err != nil || clone == nil {
		t.Fatalf("CloneService = %v %v", clone, err)
	}
	if clone.Name != "clone.Health" || clone.URI != "/clone.health" || clone.GSD.ServiceName != "clone.Health" {
		t.Errorf("clone = %q %q %q", clone.Name, clone.URI, clone.GSD.ServiceName)
	}
	if len(clone.Methods) != 3 {
		t.Fatalf("clone methods = %d, want 3", len(clone.Methods))
	}
	for name, m := range clone.Methods {
		if m.Service != clone || m.URI != "/clone.health/"+strings.ToLower(name) {
			t.Errorf("method %s of the clone: service ok %v, uri %q", name, m.Service == clone, m.URI)
		}
		if m == source.Methods[name] {
			t.Errorf("method %s must be a copy", name)
		}
	}
	if clone.Methods["Check"].GMD != &clone.GSD.Methods[0] || clone.Methods["Watch"].GSD != &clone.GSD.Streams[0] {
		t.Errorf("the methods of the clone must point into the service desc of the clone")
	}
	if got, _ := clone.GSD.Methods[0].Handler(nil, nil, nil, nil); got != "/clone.health/check" {
		t.Errorf("handler of Check in the clone was built for %v", got)
	}
	if clone.Methods["Check"].StreamCount != 5 {
		t.Errorf("the copy must carry the settings of the method, got %d", clone.Methods["Check"].StreamCount)
	}
	clone.Methods["Check"].SetStreamCount(9)
	if source.Methods["Check"].StreamCount != 5 {
		t.Errorf("the settings of the clone must not change the source")
	}
	// the registry is not changed by cloning, and a registered target is returned as is
	if r.GetService("clone.Health") != nil {
		t.Errorf("the clone must not be registered by CloneService")
	}
	registered := &GRPCService{Name: "clone.Health"}
	r.Services["clone.Health"] = registered
	if got, err := r.CloneService(source, "clone.Health"); err != nil || got != registered {
		t.Errorf("an existing target must be returned, got %v %v", got, err)
	}
}

func TestParseGRPCServiceMethod(t *testing.T) {
	stubHandlers(t)
	r := newRegistry()
	_, psd := healthDesc()
	s := r.NewGRPCService(psd)
	withMethod := func(m string) context.Context {
		return grpc.NewContextWithServerTransportStream(context.Background(), fakeTransportStream{method: m})
	}
	if got := r.ParseGRPCServiceMethod(withMethod("/grpc.health.v1.Health/Check")); got != s.Methods["Check"] {
		t.Errorf("method = %v, want Check", got)
	}
	for _, m := range []string{"/grpc.health.v1.Health/Nope", "/no.such.Service/Check", "/grpc.health.v1.Health", "garbage"} {
		if got := r.ParseGRPCServiceMethod(withMethod(m)); got != nil {
			t.Errorf("%q: method = %v, want none", m, got)
		}
	}
	if got := r.ParseGRPCServiceMethod(context.Background()); got != nil {
		t.Errorf("a context without a method must give none, got %v", got)
	}
}

type fakeTransportStream struct{ method string }

func (f fakeTransportStream) Method() string                { return f.method }
func (fakeTransportStream) SetHeader(md metadata.MD) error  { return nil }
func (fakeTransportStream) SendHeader(md metadata.MD) error { return nil }
func (fakeTransportStream) SetTrailer(md metadata.MD) error { return nil }

func TestServiceAccessors(t *testing.T) {
	stubHandlers(t)
	r := newRegistry()
	_, psd := healthDesc()
	s := r.NewGRPCService(psd)
	if !s.IsGRPC() || s.IsJSONRPC() || s.GetName() != healthService || s.GetURI() != healthURI {
		t.Errorf("service identity = grpc %v jsonrpc %v %q %q", s.IsGRPC(), s.IsJSONRPC(), s.GetName(), s.GetURI())
	}
	if !s.HasMethod("Check") || s.HasMethod("Nope") || (&GRPCService{}).HasMethod("Check") {
		t.Errorf("HasMethod is wrong")
	}
	visited := map[string]bool{}
	s.ForEachMethod(func(m rpc.RPCMethod) {
		visited[m.GetName()] = true
	})
	if len(visited) != 3 || !visited["Check"] || !visited["List"] || !visited["Watch"] {
		t.Errorf("ForEachMethod visited %v", visited)
	}
	if m := s.GetMethod("Check"); m == nil || m.GetName() != "Check" || m.GetURI() != healthURI+"/check" {
		t.Errorf("GetMethod(Check) = %v", m)
	}
}

func TestMethodSettings(t *testing.T) {
	stubHandlers(t)
	r := newRegistry()
	_, psd := healthDesc()
	m := r.NewGRPCService(psd).Methods["Check"]

	m.SetStreamCount(4)
	m.SetStreamDelay(time.Millisecond, 2*time.Millisecond, 3)
	m.SetResponsePayload([]byte(`{"status":"SERVING"}`))
	count, min, max, delayCount := m.GetStreamConfig()
	if count != 4 || min != time.Millisecond || max != 2*time.Millisecond || delayCount != 3 {
		t.Errorf("stream config = %d %s %s %d", count, min, max, delayCount)
	}
	if string(m.GetResponsePayload()) != `{"status":"SERVING"}` {
		t.Errorf("payload = %q", m.GetResponsePayload())
	}
	if in := m.InputType(); in == nil || in.FullName() != "grpc.health.v1.HealthCheckRequest" {
		t.Errorf("input type = %v", in)
	}
	if out := m.OutputType(); out == nil || out.FullName() != "grpc.health.v1.HealthCheckResponse" {
		t.Errorf("output type = %v", out)
	}
	bare := &GRPCServiceMethod{}
	if bare.InputType() != nil || bare.OutputType() != nil {
		t.Errorf("a method without a descriptor has no types")
	}
}

func TestResponsePayloadMarshalsAsText(t *testing.T) {
	b, err := json.Marshal(struct{ P GRPCResponsePayload }{GRPCResponsePayload(`{"a":1}`)})
	if err != nil || string(b) != `{"P":"{\"a\":1}"}` {
		t.Errorf("payload JSON = %s %v, want the payload as a JSON string", b, err)
	}
}

func TestMessageConversions(t *testing.T) {
	stubHandlers(t)
	r := newRegistry()
	_, psd := healthDesc()
	m := r.NewGRPCService(psd).Methods["Check"]

	inputs, err := m.PayloadsToInputs([][]byte{[]byte(`{"service":"a"}`), []byte(`{"service":"b"}`)})
	if err != nil || len(inputs) != 2 {
		t.Fatalf("PayloadsToInputs = %v %v", inputs, err)
	}
	if got := inputs[0].ProtoReflect().Descriptor().FullName(); got != "grpc.health.v1.HealthCheckRequest" {
		t.Errorf("input type = %s", got)
	}
	jsons, err := m.JSONsToInputs([]any{map[string]any{"service": "x"}})
	if err != nil || len(jsons) != 1 {
		t.Errorf("JSONsToInputs = %v %v", jsons, err)
	}
	outs, err := m.PayloadsToOutputs([][]byte{[]byte(`{"status":"SERVING"}`)})
	if err != nil || len(outs) != 1 || outs[0].ProtoReflect().Descriptor().FullName() != "grpc.health.v1.HealthCheckResponse" {
		t.Errorf("PayloadsToOutputs = %v %v", outs, err)
	}
	outs, err = m.JSONsToOutputs([]any{map[string]any{"status": "NOT_SERVING"}})
	if err != nil || len(outs) != 1 {
		t.Errorf("JSONsToOutputs = %v %v", outs, err)
	}
	if got, err := m.PayloadsToInputs([][]byte{[]byte(`{"nosuchfield":1}`)}); err == nil || got != nil {
		t.Errorf("a payload that doesn't fit the type must fail, got %v %v", got, err)
	}
	if got, err := m.JSONsToOutputs([]any{map[string]any{"status": "NOT_A_STATUS"}}); err == nil || got != nil {
		t.Errorf("a value that doesn't fit the type must fail, got %v %v", got, err)
	}
}

func TestProcessAndSetPayload(t *testing.T) {
	stubHandlers(t)
	port := uniquePort()
	r := newRegistry()
	_, psd := healthDesc()
	m := r.NewGRPCService(psd).Methods["Check"]

	if err := m.ProcessAndSetPayload(port, map[string]any{"status": "SERVING"}, nil, nil, "application/json", "10ms-20ms:2", 3); err != nil {
		t.Fatalf("ProcessAndSetPayload: %v", err)
	}
	count, min, max, delayCount := m.GetStreamConfig()
	if count != 1 || min != 10*time.Millisecond || max != 20*time.Millisecond || delayCount != 2 {
		t.Errorf("stream config = %d %s %s %d, want 1 10ms 20ms 2", count, min, max, delayCount)
	}
	if got := strings.ReplaceAll(string(m.GetResponsePayload()), " ", ""); got != `{"status":"SERVING"}` {
		t.Errorf("payload = %q", got)
	}
	// data that can't be encoded gives no payload, which is refused
	if err := m.ProcessAndSetPayload(port, make(chan int), nil, nil, "application/json", "", 1); err == nil {
		t.Errorf("an empty payload must be refused")
	}
}

// listen serves a gRPC server over an in-memory connection and returns a client connection to it.
func listen(t *testing.T, register func(*grpc.Server)) *grpc.ClientConn {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	register(srv)
	go srv.Serve(lis)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() {
		conn.Close()
		srv.Stop()
		lis.Close()
	})
	return conn
}

func TestMarshalRegistryAndServiceJSON(t *testing.T) {
	stubHandlers(t)
	r := newRegistry()
	_, psd := healthDesc()
	s := r.NewGRPCService(psd)
	r.AddActiveService(s)
	s.Methods["Check"].SetResponsePayload([]byte("x"))

	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal registry: %v", err)
	}
	var got struct {
		Services       map[string]struct{ Name, URI string }
		ActiveServices map[string]struct{ Name string }
		ProxyServices  map[string]any
		TeeServices    map[string]any
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("registry JSON %s: %v", b, err)
	}
	if got.Services[healthService].URI != healthURI || got.ActiveServices[healthService].Name != healthService ||
		got.ProxyServices == nil || got.TeeServices == nil {
		t.Errorf("registry JSON = %s", b)
	}
	var svc struct {
		Name    string
		Methods map[string]struct {
			Name            string `json:"name"`
			URI             string `json:"uri"`
			IsUnary         bool   `json:"isUnary"`
			ResponsePayload string `json:"responsePayload"`
		} `json:"methods"`
	}
	b, err = json.Marshal(s)
	if err != nil || json.Unmarshal(b, &svc) != nil {
		t.Fatalf("service JSON %s: %v", b, err)
	}
	if svc.Name != healthService || svc.Methods["Check"].URI != healthURI+"/check" || !svc.Methods["Check"].IsUnary || svc.Methods["Check"].ResponsePayload != "x" {
		t.Errorf("service JSON = %s", b)
	}
}
