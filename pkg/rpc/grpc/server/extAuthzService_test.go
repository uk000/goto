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

package grpcserver

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goto/pkg/constants"
	"goto/pkg/rpc/grpc/extauthz"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type extAuthzEnv struct {
	*testServer
	client extauthz.AuthorizationClient
}

// startExtAuthzServer serves the ext authz service the way the server of Goto registers it, with the methods
// intercepted.
func startExtAuthzServer(t *testing.T) *extAuthzEnv {
	t.Helper()
	stubGlobals(t)
	resetRegistry(t)
	resetExtAuthz(t)
	GRPCManager.InterceptWithMiddleware(cloneDesc(&extauthz.Authorization_ServiceDesc), &ExtAuthzGRPCService{})
	svc := ServiceRegistry.GetService("envoy.service.auth.v3.Authorization")
	if svc == nil {
		t.Fatalf("the ext authz service was not registered")
	}
	ts := startServer(t, func(s *grpc.Server, _ *testServer) { s.RegisterService(svc.GSD, svc.Server) })
	return &extAuthzEnv{testServer: ts, client: extauthz.NewAuthorizationClient(ts.conn)}
}

func checkRequest(method, host, path string, headers map[string]string) *extauthz.CheckRequest {
	return &extauthz.CheckRequest{Attributes: &extauthz.AttributeContext{
		Request: &extauthz.AttributeContext_Request{
			Http: &extauthz.AttributeContext_HttpRequest{Method: method, Host: host, Path: path, Headers: headers},
		},
	}}
}

func (e *extAuthzEnv) check(t *testing.T, req *extauthz.CheckRequest) (*extauthz.CheckResponse, metadata.MD, error) {
	t.Helper()
	var header metadata.MD
	resp, err := e.client.Check(rpcCtx(t), req, grpc.Header(&header))
	return resp, header, err
}

func requireAllowed(t *testing.T, resp *extauthz.CheckResponse, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if resp.GetStatus().GetCode() != 0 || resp.GetStatus().GetMessage() != "OK" || resp.GetOkResponse() == nil || resp.GetDeniedResponse() != nil {
		t.Errorf("response = %v, want allowed: code 0, message OK, an ok response", resp)
	}
}

func requireDenied(t *testing.T, resp *extauthz.CheckResponse, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if resp.GetStatus().GetCode() != 7 || resp.GetStatus().GetMessage() != "Permission denied" {
		t.Errorf("status = %v, want code 7 (permission denied)", resp.GetStatus())
	}
	if resp.GetOkResponse() != nil || resp.GetDeniedResponse().GetStatus().GetCode() != extauthz.StatusCode_Forbidden {
		t.Errorf("response = %v, want a denied response of 403", resp)
	}
}

func TestExtAuthzAllowsByDefault(t *testing.T) {
	e := startExtAuthzServer(t)
	resp, header, err := e.check(t, checkRequest("GET", "example.com", "/path", map[string]string{"x-a": "1"}))
	requireAllowed(t, resp, err)
	if got := header.Get(strings.ToLower(constants.HeaderGotoPort)); len(got) != 1 || got[0] != strconv.Itoa(e.port) {
		t.Errorf("response headers = %v, want the port", header)
	}
	if got := header.Get(strings.ToLower(constants.HeaderGotoRPCMethod)); len(got) != 1 || got[0] != "/envoy.service.auth.v3.Authorization/Check" {
		t.Errorf("response headers = %v, want the method", header)
	}
}

func TestExtAuthzRules(t *testing.T) {
	e := startExtAuthzServer(t)
	store(t, "x-block", "", "deny")
	store(t, "x-user", "mallory", "deny")
	store(t, "x-user", "alice", "allow")
	store(t, "x-wild", "", "deny")
	tests := []struct {
		name    string
		headers map[string]string
		denied  bool
	}{
		{"no matching header", map[string]string{"x-other": "1"}, false},
		{"header present", map[string]string{"x-block": "anything"}, true},
		{"header present with an empty value", map[string]string{"x-block": ""}, true},
		{"value that is denied", map[string]string{"x-user": "mallory"}, true},
		{"value that is allowed", map[string]string{"x-user": "alice"}, false},
		{"value that has no rule", map[string]string{"x-user": "bob"}, false},
		{"first match wins: the rule for x-block is before the one that allows alice", map[string]string{"x-user": "alice", "x-block": "1"}, true},
		{"no headers", nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, _, err := e.check(t, checkRequest("GET", "h", "/", tc.headers))
			if tc.denied {
				requireDenied(t, resp, err)
			} else {
				requireAllowed(t, resp, err)
			}
		})
	}
	// the rules change what the next call gets
	if !RemoveExtAuthzRule("x-block", "") {
		t.Fatal("rule not removed")
	}
	resp, _, err := e.check(t, checkRequest("GET", "h", "/", map[string]string{"x-block": "1"}))
	requireAllowed(t, resp, err)
}

func TestExtAuthzRuleForAnyRequest(t *testing.T) {
	e := startExtAuthzServer(t)
	store(t, "*", "", "deny")
	resp, _, err := e.check(t, checkRequest("GET", "h", "/", nil))
	requireDenied(t, resp, err)
	// also for a request that has no attributes of the http request at all
	resp, _, err = e.check(t, &extauthz.CheckRequest{})
	requireDenied(t, resp, err)
	resp, _, err = e.check(t, &extauthz.CheckRequest{Attributes: &extauthz.AttributeContext{}})
	requireDenied(t, resp, err)
	// and a rule for a header doesn't apply to such a request
	ClearExtAuthzRules()
	store(t, "x-a", "", "deny")
	resp, _, err = e.check(t, &extauthz.CheckRequest{})
	requireAllowed(t, resp, err)
}

func TestExtAuthzRuleHeadersAreLowerCase(t *testing.T) {
	e := startExtAuthzServer(t)
	store(t, "X-Upper", "", "deny")
	// envoy gives the names of the headers in lower case
	resp, _, err := e.check(t, checkRequest("GET", "h", "/", map[string]string{"x-upper": "1"}))
	requireDenied(t, resp, err)
}

func TestExtAuthzLongRequestIsLogged(t *testing.T) {
	e := startExtAuthzServer(t)
	setFlags(t, func(f *logFlags) { f.log, f.miniBody, f.respBody, f.headers = true, true, true, true })
	long := "/" + strings.Repeat("p", 200)
	resp, _, err := e.check(t, checkRequest("POST", "example.com", long, nil))
	requireAllowed(t, resp, err)
}

func TestExtAuthzCheckWithoutARequest(t *testing.T) {
	stubGlobals(t)
	resetExtAuthz(t)
	svc := &ExtAuthzGRPCService{}
	resp, err := svc.Check(context.Background(), nil)
	requireAllowed(t, resp, err)
	store(t, "*", "", "deny")
	resp, err = svc.Check(context.Background(), &extauthz.CheckRequest{})
	requireDenied(t, resp, err)
}

func TestExtAuthzThroughForcedStatus(t *testing.T) {
	e := startExtAuthzServer(t)
	force(t, e.port, "/envoy.service.auth.v3.Authorization/Check", "", "", "", []int{503}, 1)
	if _, _, err := e.check(t, checkRequest("GET", "h", "/", nil)); !isForced(err, 503) {
		t.Fatalf("Check = %v, want the forced status", err)
	}
	resp, _, err := e.check(t, checkRequest("GET", "h", "/", nil))
	requireAllowed(t, resp, err)
}

func TestExtAuthzConcurrentChecksWhileRulesChange(t *testing.T) {
	e := startExtAuthzServer(t)
	store(t, "x-deny", "", "deny")
	const workers, rounds = 12, 10
	var wrong atomic.Int32
	var failed atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			for round := 0; round < rounds; round++ {
				if i%3 == 0 {
					// the rules of other headers come and go, and don't touch the answers below
					header := fmt.Sprintf("x-noise-%d", i)
					StoreExtAuthzRule(&ExtAuthzRule{Header: header, Action: "allow"})
					RemoveExtAuthzRule(header, "")
					continue
				}
				resp, err := e.client.Check(ctx, checkRequest("GET", "h", "/", map[string]string{"x-deny": "1"}))
				if err != nil {
					failed.Add(1)
				} else if resp.GetDeniedResponse() == nil {
					wrong.Add(1)
				}
				resp, err = e.client.Check(ctx, checkRequest("GET", "h", "/", map[string]string{"x-fine": "1"}))
				if err != nil {
					failed.Add(1)
				} else if resp.GetOkResponse() == nil {
					wrong.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if failed.Load() != 0 || wrong.Load() != 0 {
		t.Errorf("failed calls %d, wrong answers %d, want none", failed.Load(), wrong.Load())
	}
}
