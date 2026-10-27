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

package startup

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"goto/ctl"
	mcpserver "goto/pkg/ai/mcp/server"
)

// mcpPort is a port for the MCP servers of a test, which are removed when the test ends.
func mcpPort(t *testing.T) int {
	t.Helper()
	port := newPort()
	t.Cleanup(func() {
		for name := range mcpserver.GetMCPServerNames()[port] {
			mcpserver.RemoveMCPServer(port, name)
		}
	})
	return port
}

func mcpServerYAML(port int, name string) string {
	return fmt.Sprintf(`
mcp:
  toolSchemas:
  - name: text-schema
    inputSchema:
      type: object
      properties:
        q: {type: string}
  servers:
  - server:
      name: %s
      port: %d
      version: "1.0"
      hasTools: true
      enabled: true
    tools:
    - tool:
        name: echo
        description: echoes
      schema: text-schema
    - tool:
        name: plain
        description: has the default schema
    payloads:
      server:
        toolPayloads:
        - name: echo
          payload: {answer: 42}
`, name, port)
}

func mcpToolNames(port int, name string) []string {
	return mcpserver.GetMCPServerNames()[port][name]
}

func TestLoadMCPNothingToLoad(t *testing.T) {
	resetState(t)
	loadMCP(nil)
	clearMCP(nil)
	logged := captureLog(t)
	loadMCP(&ctl.MCP{Servers: []*ctl.MCPServer{nil, {}}}) // an entry without anything, and one without a server
	clearMCP(&ctl.MCP{Servers: []*ctl.MCPServer{nil, {}}})
	if n := strings.Count(logged.String(), "Skipping MCP server config without a server"); n != 2 {
		t.Errorf("entries without a server reported %d times, want 2: %s", n, logged.String())
	}
}

func TestLoadMCPAddsServersWithTheirTools(t *testing.T) {
	resetState(t)
	port := mcpPort(t)
	logged := captureLog(t)
	loadConfig(parse(t, "mcp.yaml", mcpServerYAML(port, "startup-mcp-tools")))

	if mcpserver.GetMCPServer(port, "startup-mcp-tools") == nil {
		t.Fatalf("the server is not added: %v", mcpserver.GetMCPServerNames()[port])
	}
	tools := mcpToolNames(port, "startup-mcp-tools")
	if !slices.Contains(tools, "echo") || !slices.Contains(tools, "plain") {
		t.Errorf("tools of the server = %v, want echo and plain among them", tools)
	}
	if !strings.Contains(logged.String(), "Set payload for tool [echo]") {
		t.Errorf("the payload of the tool was not set: %s", logged.String())
	}
	if mcpConfigs["mcp.yaml"] == nil {
		t.Error("the config of the file is not kept")
	}
}

func TestLoadMCPDoesNotReportComponentsThatTheServerDoesNotHave(t *testing.T) {
	// A server of tools only has no prompts, resources or templates to add, which is not an error to report.
	resetState(t)
	port := mcpPort(t)
	logged := captureLog(t)
	loadConfig(parse(t, "tools-only.yaml", mcpServerYAML(port, "startup-mcp-toolsonly")))
	if strings.Contains(logged.String(), "[*** ERROR ***]") {
		t.Errorf("errors were reported for a config that is valid:\n%s", logged.String())
	}
}

func TestLoadMCPAddsPromptsResourcesAndTemplates(t *testing.T) {
	resetState(t)
	port := mcpPort(t)
	logged := captureLog(t)
	loadConfig(parse(t, "components.yaml", fmt.Sprintf(`
mcp:
  servers:
  - server:
      name: startup-mcp-components
      port: %d
      version: "1.0"
      hasPrompts: true
      hasResources: true
      enabled: true
    prompts:
    - prompt:
        name: greet
        description: greets
    resources:
    - resource:
        name: readme
        uri: file:///readme
        mimeType: text/plain
    templates:
    - template:
        name: item
        uriTemplate: "file:///items/{id}"
`, port)))
	server := mcpserver.GetMCPServer(port, "startup-mcp-components")
	if server == nil {
		t.Fatalf("the server is not added\nlog:\n%s", logged.String())
	}
	// The names that the server reports are the ones of its tools: the other components are looked for by their kind.
	for kind, want := range map[string]string{
		mcpserver.KindPrompts:   "greet",
		mcpserver.KindResources: "readme",
		mcpserver.KindTemplates: "item",
	} {
		names := mcpComponentNames(server, kind)
		if !slices.Contains(names, want) {
			t.Errorf("%s of the server = %v, want %q among them\nlog:\n%s", kind, names, want, logged.String())
		}
	}
}

func mcpComponentNames(server *mcpserver.MCPServer, kind string) []string {
	names := []string{}
	if components := reflect.ValueOf(server.GetComponents(kind)); components.Kind() == reflect.Map {
		for _, key := range components.MapKeys() {
			names = append(names, key.String())
		}
	}
	sort.Strings(names)
	return names
}

func TestLoadMCPSkipsToolsOfUnknownSchema(t *testing.T) {
	resetState(t)
	port := mcpPort(t)
	loadConfig(parse(t, "badschema.yaml", fmt.Sprintf(`
mcp:
  servers:
  - server: {name: startup-mcp-badschema, port: %d, version: "1", hasTools: true, enabled: true}
    tools:
    - tool: {name: ghost, description: refers to a schema that is not defined}
      schema: nowhere
    - tool: {name: solid, description: has the default}
`, port)))
	tools := mcpToolNames(port, "startup-mcp-badschema")
	if slices.Contains(tools, "solid") == false {
		t.Errorf("tools = %v, want the tool without a schema reference among them", tools)
	}
}

func TestClearMCPRemovesItsServers(t *testing.T) {
	resetState(t)
	port := mcpPort(t)
	loadConfig(parse(t, "gone.yaml", mcpServerYAML(port, "startup-mcp-gone")))
	loadConfig(parse(t, "stays.yaml", mcpServerYAML(port, "startup-mcp-stays")))
	removeConfigs("/configs/gone.yaml")
	if mcpserver.GetMCPServer(port, "startup-mcp-gone") != nil {
		t.Error("the server of the removed file is still there")
	}
	if mcpserver.GetMCPServer(port, "startup-mcp-stays") == nil {
		t.Error("the server of the other file was removed")
	}
	if mcpConfigs["gone.yaml"] != nil || mcpConfigs["stays.yaml"] == nil {
		t.Errorf("configs kept: %v", keys(mcpConfigs))
	}
}

func TestLoadConfigMCPReloadReplacesTheServersOfTheFile(t *testing.T) {
	resetState(t)
	port := mcpPort(t)
	loadConfig(parse(t, "reload.yaml", mcpServerYAML(port, "startup-mcp-before")))
	loadConfig(parse(t, "reload.yaml", mcpServerYAML(port, "startup-mcp-after")))
	if mcpserver.GetMCPServer(port, "startup-mcp-before") != nil {
		t.Error("the server of the file as it was is still there")
	}
	if mcpserver.GetMCPServer(port, "startup-mcp-after") == nil {
		t.Error("the server of the file as it is now is not there")
	}
}
