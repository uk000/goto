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

package ctl

import (
	"path/filepath"
	"testing"

	mcpserver "goto/pkg/ai/mcp/server"
	"goto/pkg/global"
	"goto/pkg/server/listeners"
)

// Ctl is given the arguments that are left after the flags, and the first one is the command: without any it used to be
// an index out of range.
func TestCtlWithoutArgumentsDoesNotPanic(t *testing.T) {
	dir := isolate(t)
	global.CtlConfig.ContextFile = filepath.Join(dir, "ctx", "ctx.yaml")
	within(t, "Ctl", func() { Ctl(nil) })
	within(t, "Ctl", func() { Ctl([]string{}) })
}

// A context file that has contexts but not the default one, and a context that is not in it, leave the command without
// a context to talk to. That used to be a nil pointer when the first thing was sent.
func TestCtlApplyWithoutAContextDoesNotPanic(t *testing.T) {
	dir := isolate(t)
	r := newRemote(t)
	global.CtlConfig.ContextFile = writeFile(t, dir, "ctx/ctx.yaml", "only:\n  name: only\n  remoteGotoURL: "+r.URL+"\n")
	global.CtlConfig.Context = "unknown"
	config := writeFile(t, dir, "config.yaml", "scripts:\n  config:\n  - name: s\n    content: echo\n")
	within(t, "Ctl apply", func() { Ctl([]string{"apply", "-f", config}) })
	if got := r.got(); len(got) != 0 {
		t.Errorf("requests = %v, want none without a context", got)
	}
}

// A null in the list of scripts to run used to be a nil pointer.
func TestProcessScriptsSkipsANullScriptToRun(t *testing.T) {
	r := newRemote(t)
	useRemote(t, r.URL)
	within(t, "processScripts", func() {
		processScripts(&Scripts{Run: []*ScriptRun{nil, {Name: "valid"}}})
	})
	if paths := r.paths(); len(paths) != 1 || paths[0] != "POST /scripts/valid/run" {
		t.Errorf("requests = %v, want only the script that is valid", paths)
	}
}

// The agents of a server are sent to that server. They used to be added to what was sent to the servers before it, so
// that a server got the agents of all the servers that came ahead of it in the config.
func TestProcessA2ASendsEachServerItsOwnAgents(t *testing.T) {
	r := newRemote(t)
	useRemote(t, r.URL)
	config := ParseConfig("a2a.yaml", []byte(`
a2a:
  servers:
  - port: 9001
    agents:
      a:
        card:
          name: agent-one
  - port: 9002
    agents:
      b:
        card:
          name: agent-two
  - port: 9003
    agents:
      c:
        card:
          name: agent-three
`))
	within(t, "processA2A", func() { processA2A(config) })
	got := r.got()
	if len(got) != 3 {
		t.Fatalf("requests = %+v, want 1 per server", got)
	}
	for i, want := range []struct{ path, agent string }{
		{"/port=9001/a2a/agents/add", "agent-one"},
		{"/port=9002/a2a/agents/add", "agent-two"},
		{"/port=9003/a2a/agents/add", "agent-three"},
	} {
		names := agentNames(t, got[i].Body)
		if got[i].Path != want.path || len(names) != 1 || names[0] != want.agent {
			t.Errorf("request %d = %s with agents %v, want only %s at %s", i, got[i].Path, names, want.agent, want.path)
		}
	}
}

// The key of a CA goes to the API of keys, and not to the one of certs, where it was sent along with the cert.
func TestCACertConfigSendsTheKeyToTheKeyAPI(t *testing.T) {
	r := newRemote(t)
	useRemote(t, r.URL)
	within(t, "load", (&CACertConfig{Name: "myca", Domain: "goto.goto", Cert: "CERT-DATA", Key: "KEY-DATA"}).load)
	got := r.got()
	if len(got) != 2 {
		t.Fatalf("requests = %+v, want 2", got)
	}
	if got[0].Path != "/tls/ca/cert/add/myca/goto.goto" || got[0].Body != "CERT-DATA" {
		t.Errorf("first request = %s with %q, want the cert to the API of certs", got[0].Path, got[0].Body)
	}
	if got[1].Path != "/tls/ca/key/add/myca/goto.goto" || got[1].Body != "KEY-DATA" {
		t.Errorf("second request = %s with %q, want the key to the API of keys", got[1].Path, got[1].Body)
	}
}

// Entries of the MCP config that are not what they should be are skipped, and the rest of the config is sent. Each of
// them used to be a panic: a stream payload without a payload, a null where a server is, a tool that is not an object,
// and a schema that is not named by text.
func TestProcessMCPSkipsEntriesThatAreNotValid(t *testing.T) {
	r := newRemote(t)
	useRemote(t, r.URL)
	config := ParseConfig("mcp.yaml", []byte(`
mcp:
  toolSchemas:
  - name: 5
    inputSchema:
      type: object
  - name: text-schema
    inputSchema: not-an-object
  - name: good-schema
    inputSchema:
      type: object
  servers:
  - null
  - server: null
    tools:
    - tool:
        name: for-no-server
  - server:
      name: srv1
      port: 9000
    tools:
    - tool: not-an-object
    - schema: 5
      tool:
        name: numeric-schema
    - schema: good-schema
      tool:
        name: good-tool
    payloads:
      server:
        toolPayloads:
        - null
        - name: good-tool
          payload:
            k: v
        streamPayloads:
        - null
        - name: no-payload
        - name: no-data
          payload:
            count: 2
        - name: good-stream
          payload:
            count: 2
            data:
            - num: 1
`))
	if config == nil {
		t.Fatal("config not parsed")
	}
	within(t, "processMCP", func() { processMCP(config) })
	paths := r.paths()
	for _, want := range []string{
		"POST /mcpapi/servers/add",
		"POST /mcpapi/server/srv1/tools/add",
		"POST /mcpapi/server/srv1/payload/tools/good-tool",
		"POST /mcpapi/server/srv1/payload/tools/good-stream/stream/count=2",
	} {
		found := false
		for _, p := range paths {
			found = found || p == want
		}
		if !found {
			t.Errorf("requests = %v, want %s", paths, want)
		}
	}
	for _, p := range paths {
		if p == "POST /mcpapi/server//tools/add" {
			t.Errorf("requests = %v: configs were sent for a server that has none", paths)
		}
	}
	servers, _ := decodeJSON(t, r.find("/mcpapi/servers/add")[0].Body).([]any)
	if len(servers) != 1 {
		t.Errorf("servers = %v, want only the one that is valid", servers)
	}
}

// A null where a proxy is, or a target, used to be a nil pointer.
func TestProcessProxySkipsNullEntries(t *testing.T) {
	r := newRemote(t)
	useRemote(t, r.URL)
	config := ParseConfig("proxy.yaml", []byte(`
proxy:
- null
- http:
    port: 7000
    targets:
      broken: null
      fine:
        enabled: true
`))
	if config == nil {
		t.Fatal("config not parsed")
	}
	within(t, "processProxy", func() { processProxy(config) })
	var targets int
	for _, p := range r.paths() {
		if p == "POST /proxy/http/targets/add" {
			targets++
		}
	}
	if targets != 1 {
		t.Errorf("requests = %v, want the target that is valid to be sent", r.paths())
	}
}

// The command does not start the server, which is what sets the hooks that a TCP listener calls when it is added. That was
// a call of a function that is not there.
func TestProcessListenersOfATCPListenerWithoutTheHooksOfTheServer(t *testing.T) {
	removeListeners(t, 47314)
	old := global.Funcs.GetListenerID
	global.Funcs.GetListenerID = nil
	t.Cleanup(func() { global.Funcs.GetListenerID = old })
	config := ParseConfig("listeners.yaml", []byte("listeners:\n  47314:\n    protocol: tcp\n"))
	within(t, "processListeners", func() { processListeners(config.Listeners) })
	if l := listeners.GetListenerForPort(47314); l == nil || l.Port != 47314 {
		t.Errorf("listener = %+v, want it to have been added", l)
	}
}

// A port that is given without a listener config is a null, which used to be a nil pointer.
func TestProcessListenersSkipsAPortWithoutAConfig(t *testing.T) {
	removeListeners(t, 47315, 47316)
	config := ParseConfig("listeners.yaml", []byte("listeners:\n  47315:\n  47316:\n    protocol: http\n"))
	if config == nil {
		t.Fatal("config not parsed")
	}
	within(t, "processListeners", func() { processListeners(config.Listeners) })
	if listeners.GetListenerForPort(47315) != nil {
		t.Error("a listener was added for a port without a config")
	}
	if listeners.GetListenerForPort(47316) == nil {
		t.Error("the listener that has a config was not added")
	}
}

// Each of the entries that are not valid used to be a panic of its own, so each is made to meet the code that it broke.

func TestProcessToolSchemasSkipsSchemasThatAreNotValid(t *testing.T) {
	m := &MCP{ToolSchemas: []map[string]any{
		{"name": 5, "inputSchema": map[string]any{"type": "object"}},
		{"name": "not-an-object", "inputSchema": "text"},
		{"name": "valid", "inputSchema": map[string]any{"type": "object"}},
	}}
	within(t, "ProcessToolSchemas", m.ProcessToolSchemas)
	if len(m.toolInputSchemas) != 1 || m.toolInputSchemas["valid"] == nil {
		t.Errorf("schemas = %v, want only the valid one", m.toolInputSchemas)
	}
}

func TestProcessMCPServerSkipsToolsThatAreNotValid(t *testing.T) {
	m := &MCP{toolInputSchemas: map[string]map[string]any{"known": {"type": "object"}}}
	server := &MCPServer{Server: &mcpserver.MCPServerPayload{Name: "srv"}, Tools: []map[string]any{
		{"tool": "not-an-object"},
		{"schema": 5, "tool": map[string]any{"name": "numeric-schema"}},
		{"schema": "known", "tool": map[string]any{"name": "known-schema"}},
	}}
	within(t, "ProcessMCPServer", func() { m.ProcessMCPServer(server) })
	// The entry that is not a tool is left as it is, and the tool that names a schema that is not text is left out.
	if len(server.Tools) != 2 {
		t.Fatalf("tools = %v, want the entry that is not a tool, and the one that names a known schema", server.Tools)
	}
	for _, entry := range server.Tools {
		if tool, _ := entry["tool"].(map[string]any); tool != nil && tool["name"] == "numeric-schema" {
			t.Error("a tool that names a schema that is not text was kept")
		}
	}
	if known, _ := server.Tools[1]["tool"].(map[string]any); known == nil || known["name"] != "known-schema" || known["inputSchema"] == nil {
		t.Errorf("the tool that names a known schema = %v, want it given the schema", server.Tools[1])
	}
}

// A tool that names a schema that is not defined is left out, and the tools that come after it are not lost with it.
func TestProcessMCPServerLeavesOutToolsOfUnknownSchema(t *testing.T) {
	m := &MCP{toolInputSchemas: map[string]map[string]any{}}
	server := &MCPServer{Server: &mcpserver.MCPServerPayload{Name: "srv"}, Tools: []map[string]any{
		{"tool": map[string]any{"name": "ghost"}, "schema": "nowhere"},
		{"tool": map[string]any{"name": "solid"}},
	}}
	within(t, "ProcessMCPServer", func() { m.ProcessMCPServer(server) })
	if len(server.Tools) != 1 || server.Tools[0]["tool"].(map[string]any)["name"] != "solid" || server.Tools[0]["tool"].(map[string]any)["inputSchema"] == nil {
		t.Errorf("tools = %v, want solid, with the default schema, and without ghost", server.Tools)
	}
}

func TestProcessMCPSkipsServersThatHaveNoServerConfig(t *testing.T) {
	r := newRemote(t)
	useRemote(t, r.URL)
	config := &GotoConfig{MCP: &MCP{Servers: []*MCPServer{nil, {Tools: []map[string]any{{"tool": map[string]any{"name": "t"}}}}}}}
	within(t, "processMCP", func() { processMCP(config) })
	if got := r.got(); len(got) != 0 {
		t.Errorf("requests = %v, want none for servers that have no server config", r.paths())
	}
}

func TestSendMCPConfigsSkipsPayloadsThatAreNotValid(t *testing.T) {
	r := newRemote(t)
	useRemote(t, r.URL)
	m := &MCP{}
	server := &MCPServer{name: "srv", Payloads: &MCPPayloads{Server: &MCPServerPayloads{
		ToolPayloads:   []*MCPToolPayload{nil},
		StreamPayloads: []*MCPStreamPayload{nil, {Name: "no-payload"}, {Name: "no-data", Payload: &StreamPayload{Count: 1}}},
	}}}
	within(t, "sendMCPConfigs", func() { m.sendMCPConfigs(server) })
	if got := r.got(); len(got) != 0 {
		t.Errorf("requests = %v, want none for payloads that are not valid", r.paths())
	}
}
