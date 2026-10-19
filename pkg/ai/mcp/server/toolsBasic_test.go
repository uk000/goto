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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// connectConfigured is connectInMemory for a client that is set up before it connects, like one that has roots.
func connectConfigured(t *testing.T, s *MCPServer, opts *gomcp.ClientOptions, configure func(*gomcp.Client)) *gomcp.ClientSession {
	t.Helper()
	s.SetMCPSessionStore(newRequest("POST", s.URI, s.Port, ""))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	clientTransport, serverTransport := gomcp.NewInMemoryTransports()
	ss, err := s.server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test-client", Version: "1.0"}, opts)
	if configure != nil {
		configure(client)
	}
	cs, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() {
		cs.Close()
		ss.Close()
	})
	return cs
}

// toolJSON is the definition of a tool that has the behavior (and the config, if there is one).
func toolJSON(name, behavior, config string) string {
	if config == "" {
		config = "{}"
	}
	return fmt.Sprintf(`{"tool":{"name":%q,"description":"%s tool","inputSchema":{"type":"object"}},"behavior":%s,"config":%s}`,
		name, name, behavior, config)
}

// newToolServer adds a stateless server that has the tools on a port of its own, and connects a client to it.
func newToolServer(t *testing.T, tools ...string) (*MCPServer, *gomcp.ClientSession) {
	t.Helper()
	port := isolate(t)
	s := addServer(t, port, fmt.Sprintf("tools-%d", port), true)
	if len(tools) > 0 {
		addTools(t, port, s.Name, "["+strings.Join(tools, ",")+"]")
	}
	return s, connectInMemory(t, s, nil)
}

// callToolRaw gives both the result and the error of a call, for the calls that are meant to fail.
func callToolRaw(t *testing.T, cs *gomcp.ClientSession, tool string, args any) (*gomcp.CallToolResult, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return cs.CallTool(ctx, &gomcp.CallToolParams{Name: tool, Arguments: args})
}

func callText(t *testing.T, cs *gomcp.ClientSession, tool string, args any) string {
	t.Helper()
	return resultText(invokeTool(t, cs, tool, args))
}

func compactJSON(t *testing.T, s string) string {
	t.Helper()
	buf := &bytes.Buffer{}
	if err := json.Compact(buf, []byte(strings.TrimSpace(s))); err != nil {
		t.Fatalf("%q is not JSON: %v", s, err)
	}
	return buf.String()
}

func expectContains(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("%q doesn't contain %q", got, want)
		}
	}
}

func TestEchoToolEchoesTheTextAndTheMetadata(t *testing.T) {
	_, cs := newToolServer(t, toolJSON("echo", `{"echo":true}`, ""))
	text := callText(t, cs, "echo", map[string]any{"text": "hello echo", "metadata": map[string]string{"k": "v"}})
	expectContains(t, text, "Echo Server:", "/tools/echo", "Received Input: hello echo", `{"k":"v"}`, "echoing back")
}

func TestEchoToolWithoutInputSaysSo(t *testing.T) {
	_, cs := newToolServer(t, toolJSON("echo", `{"echo":true}`, ""))
	res := invokeTool(t, cs, "echo", map[string]any{})
	if res.IsError {
		t.Fatalf("result is an error: %s", resultText(res))
	}
	// A call that has a metadata that is not empty but no text echoes the metadata only.
	expectContains(t, callText(t, cs, "echo", map[string]any{"metadata": map[string]string{"only": "meta"}}), `{"only":"meta"}`)
}

func TestToolResultsCarryTheTimelineOfTheCall(t *testing.T) {
	s, cs := newToolServer(t, toolJSON("echo", `{"echo":true}`, ""))
	res := invokeTool(t, cs, "echo", map[string]any{"text": "timeline"})
	if res.StructuredContent == nil {
		t.Fatal("the result doesn't have the timeline of the call")
	}
	text := compactJSON(t, toJSON(t, res.StructuredContent))
	expectContains(t, text, `"TYPE":"TIMELINE"`, fmt.Sprintf(`"Port":%d`, s.Port), "/tools/echo")
}

func TestStatusToolReportsTheRequestedStatus(t *testing.T) {
	_, cs := newToolServer(t, toolJSON("status", `{"status":true}`, ""))
	expectContains(t, callText(t, cs, "status", map[string]any{"status": 418}), "Status: Requested [418]")
	expectContains(t, callText(t, cs, "status", map[string]any{}), "Status: Success [200]")
}

func TestPingToolPingsTheClient(t *testing.T) {
	_, cs := newToolServer(t, toolJSON("pinger", `{"ping":true}`, ""))
	expectContains(t, callText(t, cs, "pinger", map[string]any{}), "Ping from Goto MCP successful")
}

func TestTimeToolReturnsTheTimeAndTheArgs(t *testing.T) {
	_, cs := newToolServer(t, toolJSON("clock", `{"time":true}`, ""))
	text := callText(t, cs, "clock", map[string]any{"text": "tick"})
	expectContains(t, text, "Time: 20", `Client Data: {"text":"tick"}`)
}

func TestListRootsToolReportsTheRootsOfTheClient(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "roots-a", true)
	addTools(t, port, "roots-a", "["+toolJSON("roots", `{"listRoots":true}`, "")+"]")

	withRoots := connectConfigured(t, s, nil, func(c *gomcp.Client) {
		c.AddRoots(&gomcp.Root{Name: "r1", URI: "file:///r1"}, &gomcp.Root{Name: "r2", URI: "file:///r2"})
	})
	expectContains(t, callText(t, withRoots, "roots", map[string]any{}), "r1 [uri: file:///r1]", "r2 [uri: file:///r2]")

	without := connectInMemory(t, s, nil)
	expectContains(t, callText(t, without, "roots", map[string]any{}), "no roots")
}

func TestToolsThatNeedAClientFailWhenTheCallIsNotFromOne(t *testing.T) {
	port := isolate(t)
	addServer(t, port, "noclient-a", true)
	addTools(t, port, "noclient-a", "["+strings.Join([]string{
		toolJSON("roots", `{"listRoots":true}`, ""),
		toolJSON("pinger", `{"ping":true}`, ""),
		toolJSON("sampler", `{"sample":true}`, ""),
		toolJSON("elicitor", `{"elicit":true}`, ""),
	}, ",")+"]")
	for tool, want := range map[string]string{
		"roots":    "listing roots failed",
		"pinger":   "ping failed",
		"sampler":  "sampling failed",
		"elicitor": "failed to get elicit response",
	} {
		mustNotPanic(t, "calling "+tool+" without a client", func() {
			rr := admin(port, "POST", "/mcpapi/server/noclient-a/tool/"+tool+"/call", "{}")
			expectBodyContains(t, rr.Body.String(), want)
		})
	}
}

func TestToolDelayArgumentDelaysTheCall(t *testing.T) {
	_, cs := newToolServer(t, toolJSON("echo", `{"echo":true}`, ""))
	start := time.Now()
	text := callText(t, cs, "echo", map[string]any{"text": "slow", "delay": "250ms"})
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond {
		t.Errorf("call took %s, want it to be delayed by 250ms", elapsed)
	}
	expectContains(t, text, "sleeping for")
}

func TestServerDetailsToolReportsTheServer(t *testing.T) {
	s, cs := newToolServer(t)
	text := compactJSON(t, callText(t, cs, "ServerDetails", map[string]any{}))
	expectContains(t, text, fmt.Sprintf(`"name":%q`, s.Name), fmt.Sprintf(`"port":%d`, s.Port))
}

func TestListServersToolReportsTheServersOfThePort(t *testing.T) {
	s, cs := newToolServer(t)
	addServer(t, s.Port, "tools-sibling", true)
	text := compactJSON(t, callText(t, cs, "ListServers", map[string]any{}))
	expectContains(t, text, s.Name, "tools-sibling")
}

func TestServerPathsToolReportsTheRoutesOfTheServers(t *testing.T) {
	s, cs := newToolServer(t, toolJSON("echo", `{"echo":true}`, ""))
	text := compactJSON(t, callText(t, cs, "ServerPaths", map[string]any{}))
	expectContains(t, text, s.URI+"/serverdetails", s.URI+"/echo")
}

func TestListComponentsToolReportsTheComponentsOfTheServers(t *testing.T) {
	s, cs := newToolServer(t, toolJSON("echo", `{"echo":true}`, ""))
	mustAdmin(t, s.Port, "POST", "/mcpapi/server/"+s.Name+"/prompts/add", `[{"prompt":{"name":"list-comp-prompt"}}]`)
	text := compactJSON(t, callText(t, cs, "ListComponents", map[string]any{}))
	expectContains(t, text, `"echo"`, `"list-comp-prompt"`, s.Name)
}

func TestAddToolToolAddsAToolToTheServerOnTheFly(t *testing.T) {
	const schema = "tools-test-schema"
	if _, err := AddToolSchemas([]byte(fmt.Sprintf(`[{"name":%q,"inputSchema":{"type":"object"}}]`, schema))); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		lock.Lock()
		delete(ToolSchemas, schema)
		lock.Unlock()
	})
	s, cs := newToolServer(t)
	res := invokeTool(t, cs, "AddTool", map[string]any{
		"name": "dyn", "description": "added on the fly", "schema": schema, "behavior": map[string]any{"echo": true}})
	if res.IsError {
		t.Fatalf("adding the tool failed: %s", resultText(res))
	}
	expectContains(t, resultText(res), "Added Tool [dyn]")
	if s.GetTool("dyn") == nil {
		t.Fatal("the server doesn't have the tool")
	}
	// The tool of the client is told about it, and it works.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	found := false
	for tool, err := range cs.Tools(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		found = found || tool.Name == "dyn"
	}
	if !found {
		t.Error("the added tool is not listed")
	}
	expectContains(t, callText(t, cs, "dyn", map[string]any{"text": "dynamic"}), "Received Input: dynamic")
}

func TestAddToolToolFailsForASchemaThatIsNotDefined(t *testing.T) {
	s, cs := newToolServer(t)
	res := invokeTool(t, cs, "AddTool", map[string]any{"name": "dyn2", "schema": "no-such-schema-anywhere"})
	if !res.IsError {
		t.Errorf("result is not an error: %s", resultText(res))
	}
	expectContains(t, resultText(res), "Failed to add Tool [dyn2]", "Undefined tool schema")
	if s.GetTool("dyn2") != nil {
		t.Error("the tool was added")
	}
}
