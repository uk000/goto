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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gotogrpc "goto/pkg/rpc/grpc"
	"goto/pkg/server/middleware"
	"goto/pkg/types"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

func (e *env) newRequest(method, target string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req = req.WithContext(context.WithValue(req.Context(), util.CurrentPortKey, e.port))
	_, req, _ = util.WithRequestStore(req, nil)
	return req
}

// admin sends a request to the admin API, which is on the router of /proxy.
func (e *env) admin(method, target string) *httptest.ResponseRecorder {
	e.t.Helper()
	setupGlobals()
	rr := httptest.NewRecorder()
	middleware.RootRouters["/proxy"].ServeHTTP(rr, e.newRequest(method, target))
	return rr
}

func TestAPISetupProxy(t *testing.T) {
	e := newEnv(t)
	up := startUpstream(t)
	rr := e.admin("POST", "/proxy/grpc/"+frontName+"/"+up.addr+"/"+echoName)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "will be proxied") {
		t.Fatalf("answered %d %q", rr.Code, rr.Body.String())
	}
	sp := e.serviceProxy(frontName)
	if sp == nil || sp.ToService != echoName || sp.Upstream.Endpoint != up.addr {
		t.Fatalf("service proxy = %+v", sp)
	}
	if out, _, err := e.call("Say", "api", nil); err != nil || textOf(out[0]) != "say:api" {
		t.Errorf("call through the proxy = %v, %v", out, err)
	}
}

func TestAPISetupProxyForTheSameService(t *testing.T) {
	e := newEnv(t)
	rr := e.admin("POST", "/proxy/grpc/"+frontName+"/127.0.0.1:9")
	if rr.Code != http.StatusOK {
		t.Fatalf("answered %d %q", rr.Code, rr.Body.String())
	}
	if sp := e.serviceProxy(frontName); sp == nil || sp.ToService != frontName {
		t.Errorf("service proxy = %+v, want one to the service itself", sp)
	}
}

func TestAPISetupProxyWithTeeAndDelay(t *testing.T) {
	e := newEnv(t)
	if min, max, count, ok := types.ParseDurationRange("20ms-30ms:2"); !ok || min != 20*time.Millisecond || max != 30*time.Millisecond || count != 2 {
		t.Fatalf("delay format = %v %v %v %v", min, max, count, ok)
	}
	rr := e.admin("POST", "/proxy/grpc/"+frontName+"/127.0.0.1:9/"+echoName+"/delay/20ms-30ms:2")
	if rr.Code != http.StatusOK {
		t.Fatalf("answered %d %q", rr.Code, rr.Body.String())
	}
	sp := e.serviceProxy(frontName)
	if sp == nil || sp.Config == nil || sp.Config.Delay == nil || sp.Config.Delay.Count != 2 || sp.Config.Delay.Max.Duration != 30*time.Millisecond {
		t.Fatalf("service proxy = %+v", sp)
	}
	rr = e.admin("POST", "/proxy/grpc/"+frontName+"/127.0.0.1:9/"+echoName+"/tee/52999")
	if rr.Code != http.StatusOK {
		t.Fatalf("answered %d %q", rr.Code, rr.Body.String())
	}
	if _, _, tee := gotogrpc.ServiceRegistry.GetProxyService(frontName); tee != 52999 {
		t.Errorf("tee port in the registry = %d, want 52999", tee)
	}
}

func TestAPISetupProxyFailures(t *testing.T) {
	e := newEnv(t)
	for name, tc := range map[string]struct {
		uri  string
		code int
		msg  string
	}{
		"unknown service":                 {"/proxy/grpc/proxytest.Nope/127.0.0.1:9", http.StatusBadRequest, "Service proto not defined"},
		"unknown target":                  {"/proxy/grpc/" + frontName + "/127.0.0.1:9/proxytest.Nope", http.StatusBadRequest, "Failed to setup gRPC proxy"},
		"upstream that is not an address": {"/proxy/grpc/" + frontName + "/nowhere/" + echoName, http.StatusBadRequest, "Failed to setup gRPC proxy"},
	} {
		t.Run(name, func(t *testing.T) {
			rr := e.admin("POST", tc.uri)
			if rr.Code != tc.code || !strings.Contains(rr.Body.String(), tc.msg) {
				t.Errorf("answered %d %q, want %d with %q", rr.Code, rr.Body.String(), tc.code, tc.msg)
			}
			if n := e.serviceProxyCount(); n != 0 {
				t.Errorf("%d service proxies were set up", n)
			}
		})
	}
}

func TestAPIRefusesOtherMethods(t *testing.T) {
	e := newEnv(t)
	for _, tc := range []struct{ method, uri string }{
		{"GET", "/proxy/grpc/clear"},
		{"POST", "/proxy/grpc/status"},
		{"GET", "/proxy/grpc/" + frontName + "/127.0.0.1:9"},
	} {
		if rr := e.admin(tc.method, tc.uri); rr.Code == http.StatusOK {
			t.Errorf("%s %s answered 200", tc.method, tc.uri)
		}
	}
	if e.serviceProxyCount() != 0 {
		t.Error("a refused request set up a proxy")
	}
}

func TestAPIStatusAndClear(t *testing.T) {
	e := newEnv(t)
	up := startUpstream(t)
	e.setup(up.addr, nil)
	if _, _, err := e.call("Say", "x", nil); err != nil {
		t.Fatal(err)
	}
	rr := e.admin("GET", "/proxy/grpc/status")
	var got struct {
		Port     int  `json:"port"`
		Enabled  bool `json:"enabled"`
		Services map[string]struct {
			FromService string `json:"fromService"`
			ToService   string `json:"toService"`
			Upstream    struct {
				Endpoint string `json:"endpoint"`
			} `json:"upstream"`
		} `json:"services"`
		Tracker struct {
			ConnCount int `json:"connCount"`
		} `json:"tracker"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("not the expected JSON: %v: %s", err, rr.Body.String())
	}
	svc := got.Services[frontName]
	if got.Port != e.port || !got.Enabled || svc.ToService != echoName || svc.Upstream.Endpoint != up.addr || got.Tracker.ConnCount != 1 {
		t.Errorf("status = %s", rr.Body.String())
	}
	if rr := e.admin("POST", "/proxy/grpc/clear"); rr.Code != http.StatusOK {
		t.Fatalf("clearing answered %d", rr.Code)
	}
	if e.serviceProxyCount() != 0 || e.proxy.Tracker.ConnCount != 0 {
		t.Errorf("proxy after clearing = %+v", e.proxy)
	}
	if WillProxyGRPC(e.port, e.front.Methods["Say"]) {
		t.Error("the service is still proxied after clearing")
	}
}

func TestRemoveGRPCProxyHandler(t *testing.T) {
	e := newEnv(t)
	up := startUpstream(t)
	e.setup(up.addr, nil)
	req := mux.SetURLVars(e.newRequest("POST", "/remove"), map[string]string{"service": frontName})
	rr := httptest.NewRecorder()
	removeGRPCProxy(rr, req)
	if e.serviceProxyCount() != 0 {
		t.Error("the service proxy was not removed")
	}
}
