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

package mcpserver

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newRemoteMCP serves a server that has an echo and a status tool, over HTTP on a listener of its own, and gives the URL
// of it.
func newRemoteMCP(t *testing.T) (*MCPServer, string) {
	t.Helper()
	port := isolate(t)
	s := addServer(t, port, fmt.Sprintf("remote-%d", port), true)
	addTools(t, port, s.Name, "["+toolJSON("echo", `{"echo":true}`, "")+","+toolJSON("status", `{"status":true}`, "")+"]")
	e := newEndpoint(t, port)
	return s, e.ts.URL + s.URI
}

func remoteTool(tool, url, extra string) string {
	return toolJSON("remote", `{"remote":true}`, fmt.Sprintf(`{"remote":{"tool":%q,"url":%q%s}}`, tool, url, extra))
}

func TestRemoteToolCallsAToolOfAnotherMCPServer(t *testing.T) {
	_, url := newRemoteMCP(t)
	_, cs := newToolServer(t, remoteTool("echo", url, `,"args":{"text":"from the config"}`))
	res := invokeTool(t, cs, "remote", map[string]any{})
	if res.IsError {
		t.Fatalf("result is an error: %s", resultText(res))
	}
	expectContains(t, resultText(res), "Received Input: from the config")
	// What the upstream did is in the timeline of the call, along with what this server did.
	expectContains(t, compactJSON(t, toJSON(t, res.StructuredContent)), `"TYPE":"TIMELINE"`)
}

func TestRemoteToolTakesTheArgsOfTheCall(t *testing.T) {
	_, url := newRemoteMCP(t)
	_, cs := newToolServer(t, remoteTool("echo", url, `,"args":{"text":"from the config"}`))
	res := invokeTool(t, cs, "remote", map[string]any{"remoteArgs": map[string]any{"text": "from the call"}})
	expectContains(t, resultText(res), "Received Input: from the call")
}

func TestRemoteToolTakesTheToolAndTheURLOfTheCall(t *testing.T) {
	_, url := newRemoteMCP(t)
	// The config points to a tool that is not there, and to nowhere, which the call corrects.
	_, cs := newToolServer(t, remoteTool("missing", "http://127.0.0.1:1/nowhere", ""))
	res := invokeTool(t, cs, "remote", map[string]any{"remoteArgs": map[string]any{"tool": "echo", "url": url, "text": "redirected"}})
	if res.IsError {
		t.Fatalf("result is an error: %s", resultText(res))
	}
	expectContains(t, resultText(res), "Received Input: redirected")
}

func TestRemoteToolReportsAnUpstreamThatIsDown(t *testing.T) {
	down := httptest.NewServer(http.NotFoundHandler())
	url := down.URL
	down.Close()
	_, cs := newToolServer(t, remoteTool("echo", url, `,"requestTimeout":"5s"`))
	res, err := callToolRaw(t, cs, "remote", map[string]any{})
	switch {
	case err != nil:
		expectContains(t, err.Error(), "connection refused")
	case res != nil && res.IsError:
		expectContains(t, resultText(res), "Failed to invoke Remote tool [echo]")
	default:
		t.Errorf("a call to a server that is down is a success: %+v", res)
	}
}
