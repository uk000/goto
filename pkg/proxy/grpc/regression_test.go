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
	"net/http"
	"strings"
	"testing"
	"time"

	gotogrpc "goto/pkg/rpc/grpc"
	"goto/pkg/types"
	"goto/pkg/util"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

// The tests here are regressions of bugs that were fixed in the gRPC proxy. They only use what the proxy had before the
// fixes, and they don't set the proxy up with SetupGRPCServiceProxy (which failed for every call), except for the ones
// that are about it, so that each of them can be run against the code before the fixes and fail for its own reason.

// manualProxy puts a service proxy on the port the way a setup does, without the setup.
func (e *env) manualProxy(endpoint string, methods map[string]string) *GRPCServiceProxy {
	e.t.Helper()
	sp, err := newGRPCServiceProxy(e.port, e.front, e.echo, endpoint, "", e.proxy.Tracker)
	if err != nil {
		e.t.Fatal(err)
	}
	sp.Config = &GRPCProxyConfig{}
	sp.Methods = methods
	e.proxy.lock.Lock()
	e.proxy.ServiceProxies[frontName] = sp
	e.proxy.lock.Unlock()
	gotogrpc.ServiceRegistry.AddProxyService(e.front, e.echo, 0)
	return sp
}

// Setting up a proxy dereferenced the config of the service proxy, which it didn't have.
func TestRegressionSetupWithoutDelayDoesNotPanic(t *testing.T) {
	e := newEnv(t)
	up := startUpstream(t)
	if err := e.proxy.SetupGRPCServiceProxy(frontName, echoName, nil, up.addr, "", 0, 0, 0, 0, nil); err != nil {
		t.Fatal(err)
	}
	if e.serviceProxy(frontName) == nil {
		t.Error("the service proxy was not set up")
	}
}

// A method that the service doesn't have was looked up through an interface that holds a nil pointer for it, and the
// method of the target service was looked up on the source service.
func TestRegressionSetupChecksMethodsOnTheirServices(t *testing.T) {
	e := newEnv(t)
	up := startUpstream(t)
	for _, methods := range []map[string]string{{"Nope": "Say"}, {"Say": "Nope"}, {"Extra": "Say"}} {
		if err := e.proxy.SetupGRPCServiceProxy(frontName, echoName, methods, up.addr, "", 0, 0, 0, 0, nil); err == nil {
			t.Errorf("setup with methods %v succeeded", methods)
		}
	}
	// Extra is a method of the target service only.
	if err := e.proxy.SetupGRPCServiceProxy(frontName, echoName, map[string]string{"Say": "Extra"}, up.addr, "", 0, 0, 0, 0, nil); err != nil {
		t.Errorf("setup with a method of the target service: %v", err)
	}
}

// The server was told to intercept the service before the endpoint was checked, so a setup that failed left it intercepted.
func TestRegressionFailedSetupLeavesNothingBehind(t *testing.T) {
	e := newEnv(t)
	if err := e.proxy.SetupGRPCServiceProxy(frontName, echoName, nil, "not-an-address", "", 0, 0, 0, 0, nil); err == nil {
		t.Fatal("setup with an invalid endpoint succeeded")
	}
	if from, _, _ := gotogrpc.ServiceRegistry.GetProxyService(frontName); from != nil {
		t.Error("the service is proxied in the registry after the setup failed")
	}
}

// A proxy that was set up for all the methods has "*" for them, which the decision didn't know.
func TestRegressionProxyOfAllMethodsProxiesTheMethods(t *testing.T) {
	e := newEnv(t)
	e.manualProxy("127.0.0.1:9", map[string]string{"*": ""})
	if !WillProxyGRPC(e.port, e.front.Methods["Say"]) {
		t.Error("a proxy of all the methods does not proxy a method")
	}
}

func TestRegressionHookDoesNotPanicOnWhatIsNotAMethod(t *testing.T) {
	setupGlobals()
	defer func() {
		if p := recover(); p != nil {
			t.Errorf("the hook panicked: %v", p)
		}
	}()
	if util.WillProxyGRPC(1, "not a method") {
		t.Error("what is not a method is proxied")
	}
}

// The delay was applied but never reported.
func TestRegressionApplyDelayReportsTheDelay(t *testing.T) {
	sp := &GRPCServiceProxy{Config: &GRPCProxyConfig{Delay: types.NewDelay(5*time.Millisecond, 5*time.Millisecond, 1)}}
	if got := sp.applyDelay(); got != "5ms" {
		t.Errorf("delay = %q, want 5ms", got)
	}
	if got := sp.applyDelay(); got != "" {
		t.Errorf("delay after the one call that it was for = %q, want none", got)
	}
}

// The startup of the server reads the minimum of the delay of every service proxy that it loads.
func TestRegressionValidatedProxyHasADelayToRead(t *testing.T) {
	setupGlobals()
	sp := &GRPCServiceProxy{FromService: frontName, ToService: echoName, Upstream: &GRPCUpstream{Endpoint: "127.0.0.1:1"}}
	if err := sp.Validate(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if p := recover(); p != nil {
			t.Errorf("reading the delay of a validated proxy panicked: %v", p)
		}
	}()
	if sp.Config.Delay.Min.Duration != 0 || sp.Config.Delay.Max.Duration != 0 || sp.Config.Delay.Count != 0 {
		t.Errorf("delay = %+v", sp.Config.Delay)
	}
}

func TestRegressionUnaryCallUsesTheMappedMethod(t *testing.T) {
	e := newEnv(t)
	up := startUpstream(t)
	e.manualProxy(up.addr, map[string]string{e.front.Methods["Say"].GetURI(): "Extra"})
	out, _, err := e.call("Say", "hi", nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls := up.recorded(); len(calls) != 1 || calls[0].method != "Extra" || textOf(out[0]) != "extra:hi" {
		t.Errorf("upstream calls = %+v, output %v, want the call to be of Extra", calls, out)
	}
}

// The input made for the target method was dropped: the call sent the input as it came.
func TestRegressionUnaryCallSendsTheInputOfTheTargetMethod(t *testing.T) {
	e := newEnv(t)
	up := startUpstream(t)
	e.manualProxy(up.addr, map[string]string{"*": ""})
	saved := e.echo.Methods["Say"].In
	t.Cleanup(func() { e.echo.Methods["Say"].In = saved })
	e.echo.Methods["Say"].In = func(m proto.Message) proto.Message {
		out := dynamicpb.NewMessage(m.ProtoReflect().Descriptor())
		out.Set(out.Descriptor().Fields().ByName("text"), protoreflect.ValueOfString(strings.ToUpper(textOf(m))))
		return out
	}
	if _, _, err := e.call("Say", "shout", nil); err != nil {
		t.Fatal(err)
	}
	if calls := up.recorded(); len(calls) != 1 || calls[0].text != "SHOUT" {
		t.Errorf("upstream calls = %+v, want the input of the target method", calls)
	}
}

// A call that failed was reported with one response, which was a nil message.
func TestRegressionFailedUnaryCallHasNoResponses(t *testing.T) {
	e := newEnv(t)
	e.manualProxy("127.0.0.1:1", map[string]string{"*": ""})
	out, _, err := e.call("Say", "x", nil)
	if err == nil || len(out) != 0 {
		t.Errorf("call to an upstream that is not there = %v, %v", out, err)
	}
	if got := e.proxy.Tracker.ResponseCountByUpstream[echoName]; got != 0 {
		t.Errorf("responses counted for the failed call = %d", got)
	}
}

// The call of a service that the registry doesn't have as proxied was a nil dereference.
func TestRegressionUnaryCallWithoutTargetServiceIsAnError(t *testing.T) {
	e := newEnv(t)
	e.manualProxy("127.0.0.1:1", map[string]string{"*": ""})
	gotogrpc.ServiceRegistry.RemoveProxyService(frontName)
	defer func() {
		if p := recover(); p != nil {
			t.Errorf("the call panicked: %v", p)
		}
	}()
	if _, _, err := e.call("Say", "x", nil); err == nil {
		t.Error("the call succeeded")
	}
}

// A setup that failed was answered with a 200.
func TestRegressionFailedAdminSetupIsABadRequest(t *testing.T) {
	e := newEnv(t)
	rr := e.admin("POST", "/proxy/grpc/"+frontName+"/nowhere/"+echoName)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("answered %d %q, want 400", rr.Code, rr.Body.String())
	}
}
