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

package mcpproxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"goto/pkg/server/middleware"
)

// admin sends a request to the admin API, which is on the router of /proxy.
func (e *env) admin(method, target string) *httptest.ResponseRecorder {
	e.t.Helper()
	setupGlobals()
	req := e.request("")
	req.Method = method
	u, err := url.Parse(target)
	if err != nil {
		e.t.Fatal(err)
	}
	req.URL = u
	req.RequestURI = target
	rr := httptest.NewRecorder()
	middleware.RootRouters["/proxy"].ServeHTTP(rr, req)
	return rr
}

func TestAPISetupProxyForAllTools(t *testing.T) {
	e := newEnv(t)
	rr := e.admin("POST", "/proxy/mcp/proxy?endpoint=http://upstream:9000/mcp&server=srv")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Setup MCP proxy") {
		t.Fatalf("answered %d %q", rr.Code, rr.Body.String())
	}
	target := e.target("*")
	if target == nil || target.Name != "srv" || target.Endpoint != "http://upstream:9000/mcp" {
		t.Fatalf("targets = %+v, want one for all tools", e.proxy.Targets)
	}
	if !WillProxyMCP(e.port, e.request(callBody("any-tool"))) {
		t.Error("a call is not proxied after the proxy was set up")
	}
}

func TestAPISetupProxyForTool(t *testing.T) {
	e := newEnv(t)
	rr := e.admin("POST", "/proxy/mcp/proxy/calc?endpoint=http://upstream:9000/mcp&to=remote-calc")
	if rr.Code != http.StatusOK {
		t.Fatalf("answered %d %q", rr.Code, rr.Body.String())
	}
	target := e.proxy.Targets["calc"]
	if target == nil || target.Endpoint != "http://upstream:9000/mcp" || target.Tools["calc"] != "remote-calc" || target.Name == "" {
		t.Fatalf("target = %+v", target)
	}
	if len(e.proxy.Targets) != 1 {
		t.Errorf("targets = %v, want only the one for calc", e.proxy.Targets)
	}
	if WillProxyMCP(e.port, e.request(callBody("other"))) {
		t.Error("a call of another tool is proxied")
	}
	if !WillProxyMCP(e.port, e.request(callBody("calc"))) {
		t.Error("a call of calc is not proxied")
	}
}

func TestAPISetupProxyWithHeaders(t *testing.T) {
	e := newEnv(t)
	rr := e.admin("POST", "/proxy/mcp/proxy/calc?endpoint=http://upstream/mcp&to=calc&server=srv&headers=X-A:1,X-Url:http://h:80,bad,:novalue")
	if rr.Code != http.StatusOK {
		t.Fatalf("answered %d %q", rr.Code, rr.Body.String())
	}
	target := e.proxy.Targets["calc"]
	if target == nil || target.Name != "srv" {
		t.Fatalf("target = %+v", target)
	}
	// A header is its name and its value, which can have colons in it. Entries that are not headers are ignored.
	if want := map[string]string{"X-A": "1", "X-Url": "http://h:80"}; !reflect.DeepEqual(headersOf(target), want) {
		t.Errorf("headers = %v, want %v", headersOf(target), want)
	}
}

func TestAPISetupProxyWithoutEndpointFails(t *testing.T) {
	e := newEnv(t)
	rr := e.admin("POST", "/proxy/mcp/proxy?endpoint=&server=srv")
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Failed to setup MCP proxy") {
		t.Errorf("answered %d %q, want 400 with the failure", rr.Code, rr.Body.String())
	}
	if e.targetCount() > 0 {
		t.Errorf("targets = %v after a failed setup", e.proxy.Targets)
	}
	for _, uri := range []string{"/proxy/mcp/proxy", "/proxy/mcp/proxy/calc"} {
		if rr := e.admin("POST", uri); rr.Code == http.StatusOK {
			t.Errorf("POST %s without an endpoint answered 200", uri)
		}
	}
}

func TestAPISetupProxyRefusesOtherMethods(t *testing.T) {
	e := newEnv(t)
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		if rr := e.admin(method, "/proxy/mcp/proxy?endpoint=http://upstream/mcp"); rr.Code == http.StatusOK {
			t.Errorf("%s answered 200", method)
		}
	}
	if e.targetCount() > 0 {
		t.Error("a refused request set up a target")
	}
}
