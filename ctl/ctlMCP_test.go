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
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

const mcpConfig = `
mcp:
  toolSchemas:
  - name: schema1
    inputSchema:
      type: object
      properties:
        text:
          type: string
      required: []
  - name: without-input-schema
  servers:
  - server:
      name: srv1
      port: 9000
      version: "1.0"
      hasTools: true
    tools:
    - schema: schema1
      tool:
        name: tool1
    - tool:
        name: tool2
    - tool:
        name: tool3
        inputSchema:
          type: object
          custom: true
    - schema: no-such-schema
      tool:
        name: tool4
    - nothing: here
    prompts:
    - name: prompt1
    resources:
    - uri: file://resource1
    templates:
    - uriTemplate: "t/{id}"
    payloads:
      server:
        completions:
          list: [a, b]
          delay: 2s
        toolPayloads:
        - name: tool1
          payload:
            k: v
        streamPayloads:
        - name: tool1
          payload:
            count: 3
            delay: 1s
            data:
            - num: 1
            - num: 2
        - name: tool2
          payload:
            count: 2
            data:
            - m: 1
      client:
        sample:
          s: 1
        elicit:
          e: 1
        roots:
        - uri: file:///root
`

func decodeJSON(t *testing.T, body string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("body is not JSON: %v\n%s", err, body)
	}
	return v
}

func TestProcessMCPSendsTheServersAndTheirConfigs(t *testing.T) {
	r := newRemote(t)
	useRemote(t, r.URL)
	config := ParseConfig("mcp.yaml", []byte(mcpConfig))
	if config == nil {
		t.Fatal("config not parsed")
	}
	within(t, "processMCP", func() { processMCP(config) })
	want := []string{
		"POST /mcpapi/servers/add",
		"POST /mcpapi/server/srv1/tools/add",
		"POST /mcpapi/server/srv1/prompts/add",
		"POST /mcpapi/server/srv1/resources/add",
		"POST /mcpapi/server/srv1/templates/add",
		"POST /mcpapi/server/srv1/payload/completion/delay=2s",
		"POST /mcpapi/server/srv1/payload/tools/tool1",
		"POST /mcpapi/server/srv1/payload/tools/tool1/stream/count=3/delay=1s",
		"POST /mcpapi/server/srv1/payload/tools/tool2/stream/count=2",
		"POST /mcpapi/client/srv1/payload/sample",
		"POST /mcpapi/client/srv1/payload/elicit",
		"POST /mcpapi/client/srv1/payload/roots",
	}
	if got := r.paths(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %v\nwant       %v", got, want)
	}
	for _, req := range r.got() {
		if req.ContentType != "application/json" {
			t.Errorf("%s has content type %q", req.Path, req.ContentType)
		}
	}
	servers, _ := decodeJSON(t, r.find("/mcpapi/servers/add")[0].Body).([]any)
	if len(servers) != 1 || servers[0].(map[string]any)["name"] != "srv1" || servers[0].(map[string]any)["port"] != float64(9000) {
		t.Errorf("servers = %v", servers)
	}
	if got := r.find("/mcpapi/server/srv1/payload/completion/delay=2s")[0]; got.RawQuery != "type=ref/prompt" || !reflect.DeepEqual(decodeJSON(t, got.Body), []any{"a", "b"}) {
		t.Errorf("completions = %+v", got)
	}
	if got := r.find("/mcpapi/server/srv1/payload/tools/tool1")[0]; !reflect.DeepEqual(decodeJSON(t, got.Body), map[string]any{"k": "v"}) {
		t.Errorf("tool payload = %s", got.Body)
	}
	if got := r.find("/mcpapi/server/srv1/payload/tools/tool1/stream/count=3/delay=1s")[0]; !reflect.DeepEqual(decodeJSON(t, got.Body), []any{map[string]any{"num": float64(1)}, map[string]any{"num": float64(2)}}) {
		t.Errorf("stream payload = %s", got.Body)
	}
	if got := r.find("/mcpapi/client/srv1/payload/roots")[0]; !reflect.DeepEqual(decodeJSON(t, got.Body), []any{map[string]any{"uri": "file:///root"}}) {
		t.Errorf("roots = %s", got.Body)
	}
	if got := r.find("/mcpapi/client/srv1/payload/sample")[0]; !reflect.DeepEqual(decodeJSON(t, got.Body), map[string]any{"s": float64(1)}) {
		t.Errorf("sample = %s", got.Body)
	}
}

// Every tool has an input schema when it is sent: the one that it names, the default one when it does not name any, and
// its own when it has one. A tool that names a schema that does not exist is not sent: the server refuses a tool that
// has no input schema, and does not add the ones that come after it.
func TestProcessMCPGivesToolsTheirInputSchemas(t *testing.T) {
	r := newRemote(t)
	useRemote(t, r.URL)
	config := ParseConfig("mcp.yaml", []byte(mcpConfig))
	within(t, "processMCP", func() { processMCP(config) })
	tools, _ := decodeJSON(t, r.find("/mcpapi/server/srv1/tools/add")[0].Body).([]any)
	if len(tools) != 4 {
		t.Fatalf("tools = %v, want the 3 that are valid, and the entry that is not a tool", tools)
	}
	schemaOf := func(i int) any {
		tool, _ := tools[i].(map[string]any)["tool"].(map[string]any)
		return tool["inputSchema"]
	}
	if got, _ := schemaOf(0).(map[string]any); got["type"] != "object" || got["properties"] == nil {
		t.Errorf("tool1 schema = %v, want the schema that it names", got)
	}
	if got, _ := schemaOf(1).(map[string]any); got["type"] != "object" || got["required"] == nil {
		t.Errorf("tool2 schema = %v, want the default one", got)
	}
	if got, _ := schemaOf(2).(map[string]any); got["custom"] != true {
		t.Errorf("tool3 schema = %v, want its own", got)
	}
	for _, entry := range tools {
		if tool, _ := entry.(map[string]any)["tool"].(map[string]any); tool != nil && tool["name"] == "tool4" {
			t.Errorf("tool4 was sent, with the schema %v that it names not existing", tool["inputSchema"])
		}
	}
}

func TestProcessToolSchemas(t *testing.T) {
	m := &MCP{ToolSchemas: []map[string]any{
		{"name": "a", "inputSchema": map[string]any{"type": "object"}},
		{"name": "no-schema"},
		{"inputSchema": map[string]any{"type": "object"}},
	}}
	m.ProcessToolSchemas()
	if len(m.toolInputSchemas) != 1 || m.toolInputSchemas["a"]["type"] != "object" {
		t.Errorf("schemas = %v, want only the one that has a name and a schema", m.toolInputSchemas)
	}
	// Processing them again starts over.
	m.ToolSchemas = nil
	m.ProcessToolSchemas()
	if len(m.toolInputSchemas) != 0 {
		t.Errorf("schemas = %v, want none", m.toolInputSchemas)
	}
}

func TestProcessMCPServerSetsTheNameOfTheServer(t *testing.T) {
	m := &MCP{}
	m.ProcessToolSchemas()
	config := ParseConfig("mcp.yaml", []byte(mcpConfig))
	s := config.MCP.Servers[0]
	m.ProcessMCPServer(s)
	if s.name != "srv1" {
		t.Errorf("name = %q", s.name)
	}
	within(t, "ProcessMCPServer", func() {
		m.ProcessMCPServer(nil)
		m.ProcessMCPServer(&MCPServer{})
	})
}

func TestProcessMCPWithNothingSendsNothing(t *testing.T) {
	r := newRemote(t)
	useRemote(t, r.URL)
	within(t, "processMCP", func() {
		processMCP(&GotoConfig{})
		processMCP(&GotoConfig{MCP: &MCP{}})
		processMCP(&GotoConfig{MCP: &MCP{ToolSchemas: []map[string]any{{"name": "x"}}}})
	})
	if got := r.got(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

func TestProcessMCPOfAServerWithoutConfigsSendsOnlyTheServer(t *testing.T) {
	r := newRemote(t)
	useRemote(t, r.URL)
	config := ParseConfig("mcp.yaml", []byte("mcp:\n  servers:\n  - server:\n      name: bare\n      port: 1\n"))
	within(t, "processMCP", func() { processMCP(config) })
	if paths := r.paths(); len(paths) != 1 || paths[0] != "POST /mcpapi/servers/add" {
		t.Errorf("requests = %v", paths)
	}
}

func TestProcessMCPWhenTheRemoteFails(t *testing.T) {
	r := newRemote(t)
	r.setStatus(http.StatusInternalServerError)
	useRemote(t, r.URL)
	config := ParseConfig("mcp.yaml", []byte(mcpConfig))
	within(t, "processMCP", func() { processMCP(config) })
	if got := r.got(); len(got) != 12 {
		t.Errorf("requests = %d, want all 12 to be tried", len(got))
	}
}

func TestProcessMCPWhenTheRemoteIsGone(t *testing.T) {
	r := newRemote(t)
	url := r.URL
	r.Close()
	useRemote(t, url)
	config := ParseConfig("mcp.yaml", []byte(mcpConfig))
	within(t, "processMCP", func() { processMCP(config) })
}

func TestSendMCPConfigsOfNothingDoesNothing(t *testing.T) {
	r := newRemote(t)
	useRemote(t, r.URL)
	m := &MCP{}
	within(t, "sendMCPConfigs", func() {
		m.sendMCPConfigs(nil)
		m.sendMCPConfigs(&MCPServer{name: "empty"})
		m.sendMCPServers(nil)
	})
	if got := r.got(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}
