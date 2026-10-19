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
	"testing"
)

// The tests here are regressions of bugs that were fixed in the MCP proxy. They only use what the proxy had before the
// fixes, so that they can be run against the code before the fixes.

// The decision read the body to the end to look for the tool, and then parsed what was left of it, which is nothing.
func TestRegressionToolCallIsProxied(t *testing.T) {
	e := newEnv(t)
	e.setup("srv", "http://upstream/mcp", "calc", "calc")
	if !WillProxyMCP(e.port, e.request(callBody("calc"))) {
		t.Fatal("a call of a tool that has a proxy target is not proxied")
	}
}

// The decision left the body consumed when it decided not to proxy, so the server that served the request found it empty.
func TestRegressionBodyOfRequestThatIsNotProxiedIsStillReadable(t *testing.T) {
	e := newEnv(t)
	e.setup("srv", "http://upstream/mcp", "calc", "calc")
	for _, body := range []string{listBody, callBody("other")} {
		req := e.request(body)
		if WillProxyMCP(e.port, req) {
			t.Fatalf("%s is proxied", body)
		}
		if got := bodyOf(t, req); got != body {
			t.Errorf("body after the decision = %q, want %q", got, body)
		}
	}
}

// The setup through the admin API looked for the server in a param that no route has, so it failed for every request,
// and answered that it was done anyway.
func TestRegressionAdminSetupConfiguresTheProxy(t *testing.T) {
	e := newEnv(t)
	rr := e.admin("POST", "/proxy/mcp/proxy/calc?endpoint=http://upstream/mcp&to=calc")
	if rr.Code != http.StatusOK {
		t.Fatalf("answered %d %q", rr.Code, rr.Body.String())
	}
	if e.target("calc") == nil {
		t.Fatal("the proxy target was not set up")
	}
}

func TestRegressionRemoveProxyOfServerRemovesItsTargets(t *testing.T) {
	e := newEnv(t)
	e.setup("srv", "http://upstream/mcp", "calc", "calc")
	e.proxy.RemoveProxy("srv")
	if e.targetCount() != 0 {
		t.Errorf("targets = %v after removing the server", e.proxy.Targets)
	}
}
