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

package jsonrpc

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goto/pkg/global"
	"goto/pkg/rpc"
	grpcrpc "goto/pkg/rpc/grpc"
	"goto/pkg/server/hooks"
	"goto/pkg/util"

	"github.com/gorilla/mux"
	gogrpc "google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
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
	if util.MatchRouter == nil {
		util.CreateRouters(mux.NewRouter())
	}
}

// uniquePort returns a port that no other test uses: registries, trackers and hooks are kept by port.
func uniquePort() int {
	stubOnce.Do(stubGlobalFuncs)
	port := int(44000 + portSeq.Add(1))
	hooks.GetPortHooks(port) // made here, since hooks of a port that are first asked for by two callers at once can be lost
	return port
}

func uniqueName(prefix string) string {
	return fmt.Sprintf("%s%d", prefix, nameSeq.Add(1))
}

func newRegistry() *JSONRPCServiceRegistry {
	r := &JSONRPCServiceRegistry{}
	r.Init()
	return r
}

func body(s string) io.ReadCloser { return io.NopCloser(strings.NewReader(s)) }

const calcBody = `{"name":"Calc","methods":{"add":{"name":"add","isBatch":true,"streamCount":3},"sub":{},"watch":{"isStreaming":true,"streamDelayMin":1000,"streamDelayMax":2000,"streamDelayCount":4}}}`

func TestGetPortRegistry(t *testing.T) {
	p1, p2 := uniquePort(), uniquePort()
	r1 := GetPortRegistry(p1)
	if r1 == nil || r1.Services == nil || len(r1.Services) != 0 {
		t.Fatalf("registry of a port that wasn't used before = %+v, want an empty one", r1)
	}
	if GetPortRegistry(p1) != r1 {
		t.Errorf("a port has one registry")
	}
	if GetPortRegistry(p2) == r1 {
		t.Errorf("ports must not share a registry")
	}
	// it is what the rpc package finds for jsonrpc
	if got := rpc.GetServiceRegistry["jsonrpc"](p1); got != rpc.RPCServiceRegistry(r1) {
		t.Errorf("registry of the rpc package = %#v, want the one of the port", got)
	}
	t.Cleanup(func() {
		lock.Lock()
		delete(PortRegistry, p1)
		delete(PortRegistry, p2)
		lock.Unlock()
	})
}

func TestRegistryInitClearsTheServices(t *testing.T) {
	r := newRegistry()
	if _, err := r.NewJSONRPCService(body(calcBody)); err != nil {
		t.Fatal(err)
	}
	r.Init()
	if len(r.Services) != 0 || r.GetService("Calc") != nil {
		t.Errorf("services after Init = %v", r.Services)
	}
}

func TestNewJSONRPCService(t *testing.T) {
	r := newRegistry()
	s, err := r.NewJSONRPCService(body(calcBody))
	if err != nil {
		t.Fatalf("NewJSONRPCService: %v", err)
	}
	if s.Name != "Calc" || s.URI != "/calc" || r.GetService("Calc") != s {
		t.Errorf("service = %q %q, registered %v", s.Name, s.URI, r.GetService("Calc") == s)
	}
	if len(s.Methods) != 3 {
		t.Fatalf("methods = %d, want 3", len(s.Methods))
	}
	add := s.Methods["add"].(*JSONRPCMethod)
	if add.Name != "add" || add.URI != "/calc/add" || add.Service != s || !add.IsBatch || add.IsStreaming || add.StreamCount != 3 {
		t.Errorf("add = %+v", add)
	}
	// the name of a method is its key when it has none
	sub := s.Methods["sub"].(*JSONRPCMethod)
	if sub.Name != "sub" || sub.URI != "/calc/sub" || sub.Service != s {
		t.Errorf("sub = %+v", sub)
	}
	watch := s.Methods["watch"].(*JSONRPCMethod)
	count, min, max, delayCount := watch.GetStreamConfig()
	if !watch.IsStreaming || count != 0 || min != time.Microsecond || max != 2*time.Microsecond || delayCount != 4 {
		t.Errorf("watch = %+v: stream config %d %s %s %d", watch, count, min, max, delayCount)
	}
}

func TestNewJSONRPCServiceWithoutMethods(t *testing.T) {
	r := newRegistry()
	s, err := r.NewJSONRPCService(body(`{"name":"Bare"}`))
	if err != nil || s.Methods == nil || len(s.Methods) != 0 {
		t.Fatalf("service = %+v err %v, want one with an empty set of methods", s, err)
	}
	if s.HasMethod("x") || s.GetMethod("x") != nil || s.GetMethodCount() != 0 {
		t.Errorf("a service without methods has none")
	}
}

func TestNewJSONRPCServiceReplacesTheServiceOfTheSameName(t *testing.T) {
	r := newRegistry()
	first, _ := r.NewJSONRPCService(body(`{"name":"S","methods":{"a":{}}}`))
	second, err := r.NewJSONRPCService(body(`{"name":"S","methods":{"b":{}}}`))
	if err != nil || r.GetService("S") != second || r.GetService("S") == first || len(r.Services) != 1 {
		t.Fatalf("services = %v err %v", r.Services, err)
	}
	if second.HasMethod("a") || !second.HasMethod("b") {
		t.Errorf("methods of the new service = %v", second.Methods)
	}
}

func TestNewJSONRPCServiceRefusesBadBodies(t *testing.T) {
	tests := map[string]struct{ body, err string }{
		"no name":                   {`{"methods":{"a":{}}}`, "No name"},
		"empty name":                {`{"name":""}`, "No name"},
		"empty body":                {``, ""},
		"not JSON":                  {`{"name":`, ""},
		"name of the wrong kind":    {`{"name":5}`, ""},
		"methods of the wrong kind": {`{"name":"S","methods":[1]}`, ""},
		"method with another name":  {`{"name":"S","methods":{"a":{"name":"b"}}}`, "named [b]"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			r := newRegistry()
			s, err := r.NewJSONRPCService(body(tt.body))
			if err == nil || s != nil || !strings.Contains(err.Error(), tt.err) {
				t.Errorf("service = %v err = %v, want an error with %q", s, err, tt.err)
			}
			if len(r.Services) != 0 {
				t.Errorf("a service that was refused must not be registered, got %v", r.Services)
			}
		})
	}
}

func TestNewJSONRPCServiceOfAMethodThatIsNull(t *testing.T) {
	r := newRegistry()
	s, err := r.NewJSONRPCService(body(`{"name":"S","methods":{"a":null}}`))
	if err != nil || s.GetMethod("a") == nil || s.GetMethod("a").GetName() != "a" || s.GetMethod("a").GetURI() != "/s/a" {
		t.Errorf("service = %+v err %v, want the method to be made from its key", s, err)
	}
}

func TestRegistryGetRemoveAndGetRPCService(t *testing.T) {
	r := newRegistry()
	s, _ := r.NewJSONRPCService(body(calcBody))
	if r.GetService("Calc") != s || r.GetService("calc") != nil {
		t.Errorf("GetService finds services by their exact name")
	}
	if got := r.GetRPCService("Calc"); got != rpc.RPCService(s) {
		t.Errorf("GetRPCService = %v, want the service", got)
	}
	if got := r.GetRPCService("Nope"); got != nil {
		t.Errorf("GetRPCService of an unknown service must be a nil interface, got %#v", got)
	}
	r.RemoveService("Nope") // nothing happens
	r.RemoveService("Calc")
	if r.GetService("Calc") != nil || len(r.Services) != 0 {
		t.Errorf("service must be gone after RemoveService, got %v", r.Services)
	}
}

func TestServiceAccessors(t *testing.T) {
	r := newRegistry()
	s, _ := r.NewJSONRPCService(body(calcBody))
	if s.IsGRPC() || !s.IsJSONRPC() || s.GetName() != "Calc" || s.GetURI() != "/calc" || s.GetMethodCount() != 3 {
		t.Errorf("service identity is wrong: grpc %v jsonrpc %v %q %q %d", s.IsGRPC(), s.IsJSONRPC(), s.GetName(), s.GetURI(), s.GetMethodCount())
	}
	if !s.HasMethod("add") || s.HasMethod("mul") || (&JSONRPCService{}).HasMethod("add") {
		t.Errorf("HasMethod is wrong")
	}
	visited := map[string]rpc.RPCMethod{}
	s.ForEachMethod(func(m rpc.RPCMethod) { visited[m.GetName()] = m })
	if len(visited) != 3 || visited["add"] != s.GetMethod("add") {
		t.Errorf("ForEachMethod visited %v", visited)
	}
	if m := s.GetMethod("add"); m == nil || m.GetName() != "add" || m.GetURI() != "/calc/add" {
		t.Errorf("GetMethod(add) = %v", m)
	}
	if m := s.GetMethod("mul"); m != nil {
		t.Errorf("GetMethod of an unknown method must be a nil interface, got %#v", m)
	}
}

func TestMethodSettings(t *testing.T) {
	m := &JSONRPCMethod{Name: "m"}
	m.SetStreamCount(5)
	m.SetStreamDelay(time.Millisecond, 3*time.Millisecond, 2)
	m.SetResponsePayload([]byte(`{"result":1}`))
	count, min, max, delayCount := m.GetStreamConfig()
	if count != 5 || min != time.Millisecond || max != 3*time.Millisecond || delayCount != 2 {
		t.Errorf("stream config = %d %s %s %d", count, min, max, delayCount)
	}
	if m.GetResponsePayload() != `{"result":1}` {
		t.Errorf("payload = %q", m.GetResponsePayload())
	}
}

func TestMethodMarshalJSON(t *testing.T) {
	m := &JSONRPCMethod{
		Name: "m", URI: "/s/m", IsBatch: true, IsStreaming: true, ResponsePayload: "p", StreamCount: 3,
		StreamDelayMin: 5, StreamDelayMax: 6, StreamDelayCount: 7,
		StreamOut: func() []util.JSON { return nil }, Service: &JSONRPCService{Name: "s"},
	}
	got := jsonMap(t, m)
	want := map[string]any{
		"name": "m", "uri": "/s/m", "isBatch": true, "isStreaming": true, "responsePayload": "p", "streamCount": float64(3),
		"streamDelayMin": float64(5), "streamDelayMax": float64(6), "streamDelayCount": float64(7),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("method JSON = %v, want %v (the function and the service are not part of it)", got, want)
	}
}

func TestServiceJSONRoundTrip(t *testing.T) {
	r := newRegistry()
	s, _ := r.NewJSONRPCService(body(calcBody))
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back JSONRPCService
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal %s: %v", b, err)
	}
	if back.Name != "Calc" || back.URI != "/calc" || len(back.Methods) != 3 {
		t.Fatalf("service = %+v", back)
	}
	add := back.Methods["add"].(*JSONRPCMethod)
	if !add.IsBatch || add.StreamCount != 3 || add.URI != "/calc/add" {
		t.Errorf("add = %+v", add)
	}
	reg, err := json.Marshal(r)
	if err != nil || !strings.HasPrefix(string(reg), `{"services":{"Calc":{`) {
		t.Errorf("registry JSON = %s %v", reg, err)
	}
}

func TestRegistryTrackService(t *testing.T) {
	port := uniquePort()
	r := newRegistry()
	s, _ := r.NewJSONRPCService(body(calcBody))
	if r.GetServiceTracker(port, "Calc") != nil {
		t.Errorf("a service that isn't tracked has no tracker")
	}
	r.TrackService(port, "Nope", nil, "", "") // no service: nothing is tracked
	if r.GetServiceTracker(port, "Nope") != nil {
		t.Errorf("an unknown service has no tracker")
	}
	r.TrackService(port, "Calc", nil, "", "")
	if got := r.GetServiceTracker(port, "Calc"); got == nil {
		t.Errorf("service must be tracked after TrackService")
	}
	if rpc.GetRPCTracker(port).GetServiceTrackerJSON(s) == nil {
		t.Errorf("the tracker of the port must have the service")
	}
}

// grpcHealth registers the health service of gRPC in the registry of the gRPC services, with handlers that do nothing.
func grpcHealth(t *testing.T) *grpcrpc.GRPCService {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
	prevUnary, prevStream := grpcrpc.GRPCUnaryHandler, grpcrpc.GRPCStreamHandler
	grpcrpc.GRPCUnaryHandler = func(*grpcrpc.GRPCServiceMethod) gogrpc.MethodHandler { return nil }
	grpcrpc.GRPCStreamHandler = func(any, gogrpc.ServerStream) error { return nil }
	psd := grpc_health_v1.File_grpc_health_v1_health_proto.Services().ByName("Health")
	service := grpcrpc.ServiceRegistry.NewGRPCService(psd)
	t.Cleanup(func() {
		grpcrpc.ServiceRegistry.RemoveService(service.Name)
		grpcrpc.GRPCUnaryHandler, grpcrpc.GRPCStreamHandler = prevUnary, prevStream
	})
	return service
}

func TestFromGRPCService(t *testing.T) {
	g := grpcHealth(t)
	check, watch := g.Methods["Check"], g.Methods["Watch"]
	check.SetStreamCount(4)
	check.SetStreamDelay(time.Millisecond, 2*time.Millisecond, 6)
	streamed := false
	check.StreamOut = func() []util.JSON { streamed = true; return nil }

	r := newRegistry()
	s, err := r.FromGRPCService("grpc.health.v1.Health")
	if err != nil {
		t.Fatalf("FromGRPCService: %v", err)
	}
	if s.Name != g.Name || s.URI != g.URI || r.GetService(g.Name) != s || len(s.Methods) != len(g.Methods) {
		t.Fatalf("service = %q %q with %d methods, registered %v", s.Name, s.URI, len(s.Methods), r.GetService(g.Name) == s)
	}
	c := s.Methods["Check"].(*JSONRPCMethod)
	if c.URI != check.URI || c.Service != s || c.IsBatch || c.IsStreaming || c.StreamCount != 4 ||
		c.StreamDelayMin != time.Millisecond || c.StreamDelayMax != 2*time.Millisecond || c.StreamDelayCount != 6 {
		t.Errorf("Check = %+v, want the settings of the gRPC method", c)
	}
	if c.StreamOut == nil {
		t.Fatalf("the stream output of the gRPC method must be carried over")
	}
	c.StreamOut()
	if !streamed {
		t.Errorf("StreamOut of the method is not the one of the gRPC method")
	}
	if w := s.Methods["Watch"].(*JSONRPCMethod); !w.IsStreaming || w.IsBatch || w.URI != watch.URI {
		t.Errorf("Watch = %+v, want a streaming method", w)
	}
	// the settings are copied: later changes of the gRPC method don't reach the service
	check.SetStreamCount(9)
	if c.StreamCount != 4 {
		t.Errorf("the service must hold a copy of the settings, got %d", c.StreamCount)
	}
}

func TestFromGRPCServiceOfAnUnknownService(t *testing.T) {
	stubOnce.Do(stubGlobalFuncs)
	r := newRegistry()
	s, err := r.FromGRPCService("no.such.Service")
	if err == nil || s != nil || !strings.Contains(err.Error(), "no.such.Service") {
		t.Errorf("service = %v err = %v, want an error that names the service", s, err)
	}
	if err != nil && strings.HasSuffix(err.Error(), "\n") {
		t.Errorf("error %q must not end with a line break", err)
	}
	if len(r.Services) != 0 {
		t.Errorf("nothing must be registered, got %v", r.Services)
	}
}

// The services are added, looked up, listed and removed at the same time. Run with -race.
func TestRegistryConcurrentUse(t *testing.T) {
	r := newRegistry()
	const writers, perWriter = 8, 25
	var wg sync.WaitGroup
	errs := make(chan error, writers*perWriter*2)
	done := make(chan struct{})
	for w := 0; w < writers; w++ {
		w := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				name := fmt.Sprintf("svc-%d-%d", w, i)
				s, err := r.NewJSONRPCService(body(fmt.Sprintf(`{"name":%q,"methods":{"m":{}}}`, name)))
				if err != nil {
					errs <- err
					continue
				}
				s.GetMethod("m").SetStreamCount(i)
				s.GetMethod("m").SetResponsePayload([]byte("x"))
				if i%5 == 0 {
					r.RemoveService(name)
				}
			}
		}()
	}
	var readers sync.WaitGroup
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				if _, err := json.Marshal(r); err != nil {
					errs <- err
					return
				}
				r.GetRPCService("svc-0-1")
			}
		}()
	}
	wg.Wait()
	close(done)
	readers.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if want := writers * (perWriter - perWriter/5); len(r.Services) != want {
		t.Errorf("services = %d, want %d", len(r.Services), want)
	}
}
