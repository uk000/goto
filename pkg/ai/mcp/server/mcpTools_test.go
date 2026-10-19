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
	"context"
	"goto/pkg/util"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
	a2aproto "trpc.group/trpc-go/trpc-a2a-go/protocol"
)

func TestNewMCPToolHasTheDefinitionOfATool(t *testing.T) {
	tool := NewMCPTool("t1", "the first")
	if tool.Tool == nil || tool.Tool.Name != "t1" || tool.Tool.Title != "t1" || tool.Tool.Description != "the first" {
		t.Errorf("tool = %+v", tool.Tool)
	}
	if tool.Tool.InputSchema == nil || tool.Tool.OutputSchema == nil || tool.Tool.Annotations == nil {
		t.Error("the schemas and the annotations of the tool are not set")
	}
}

func TestParseToolReadsAndValidatesATool(t *testing.T) {
	tool, err := ParseTool([]byte(`{"tool":{"name":"\"quoted\"","description":"d","inputSchema":{"type":"object"}},"behavior":{"echo":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	if tool.Kind != KindTools || tool.Name != "quoted" || tool.Tool.Name != "quoted" || !tool.Behavior.Echo {
		t.Errorf("tool = %+v", tool)
	}
	for name, payload := range map[string]string{
		"not JSON":                           `{{`,
		"no definition":                      `{"behavior":{"echo":true}}`,
		"no schema":                          `{"tool":{"name":"x"}}`,
		"schema that isn't there":            `{"tool":{"name":"x"},"schema":"no-such-schema-here"}`,
		"schema that isn't one of an object": `{"tool":{"name":"x","inputSchema":{"type":"string"}}}`,
		"no name":                            `{"tool":{"inputSchema":{"type":"object"}}}`,
		"fetch without a URL":                `{"tool":{"name":"x","inputSchema":{"type":"object"}},"behavior":{"fetch":true}}`,
		"remote without a URL":               `{"tool":{"name":"x","inputSchema":{"type":"object"}},"behavior":{"remote":true},"config":{"remote":{"tool":"t"}}}`,
		"multi remote without any":           `{"tool":{"name":"x","inputSchema":{"type":"object"}},"behavior":{"multiRemote":true}}`,
		"agent without a URL":                `{"tool":{"name":"x","inputSchema":{"type":"object"}},"behavior":{"agents":true},"config":{"agent":{"name":"a"}}}`,
		"proxy without a URL":                `{"tool":{"name":"x","inputSchema":{"type":"object"}},"proxy":true}`,
	} {
		mustNotPanic(t, "parsing a tool with "+name, func() {
			if tool, err := ParseTool([]byte(payload)); err == nil {
				t.Errorf("%s: ParseTool = %+v, want an error", name, tool)
			}
		})
	}
}

func TestParseToolTakesTheSchemaThatIsRegisteredByName(t *testing.T) {
	const name = "tools-test-registered-schema"
	if _, err := AddToolSchemas([]byte(`[{"name":"` + name + `","inputSchema":{"type":"object","properties":{"q":{"type":"string"}}}}]`)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		lock.Lock()
		delete(ToolSchemas, name)
		lock.Unlock()
	})
	tool, err := ParseTool([]byte(`{"tool":{"name":"x"},"schema":"` + name + `","behavior":{"echo":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	// The registered schemas are kept as they were read.
	if got := compactJSON(t, toJSON(t, tool.Tool.InputSchema)); !strings.Contains(got, `"q":{"type":"string"}`) {
		t.Errorf("input schema = %s", got)
	}
}

func TestParseToolsReadsJSONAndYAML(t *testing.T) {
	tools, err := ParseTools([]byte(`[{"tool":{"name":"a","inputSchema":{"type":"object"}},"behavior":{"echo":true}},{"tool":{"name":"b","inputSchema":{"type":"object"}}}]`))
	if err != nil || len(tools) != 2 || tools[0].Name != "a" || tools[1].Name != "b" {
		t.Errorf("JSON: tools = %v, err = %v", tools, err)
	}
	yaml := `
- tool:
    name: yamltool
    inputSchema:
      type: object
  behavior:
    echo: true
`
	tools, err = ParseTools([]byte(yaml))
	if err != nil || len(tools) != 1 {
		t.Fatalf("YAML: tools = %v, err = %v", tools, err)
	}
	if tools[0].Name != "yamltool" || !tools[0].Behavior.Echo {
		t.Errorf("YAML: name = %q, echo = %v", tools[0].Name, tools[0].Behavior.Echo)
	}
	// One that is not valid is none of them.
	if tools, err := ParseTools([]byte(`[{"tool":{"name":"a","inputSchema":{"type":"object"}}},{"tool":{"name":"b"}}]`)); err == nil {
		t.Errorf("tools = %v, want an error for the one without a schema", tools)
	}
}

func TestIsNilSchema(t *testing.T) {
	var nilSchema *jsonschema.Schema
	var nilMap map[string]any
	for name, tt := range map[string]struct {
		schema any
		want   bool
	}{
		"nil":                {nil, true},
		"nil schema pointer": {nilSchema, true},
		"nil map":            {nilMap, true},
		"schema":             {&jsonschema.Schema{Type: "object"}, false},
		"map":                {map[string]any{"type": "object"}, false},
		"string":             {"object", false},
		"number":             {5, false},
	} {
		if got := isNilSchema(tt.schema); got != tt.want {
			t.Errorf("%s: isNilSchema = %v, want %v", name, got, tt.want)
		}
	}
}

func TestValidateObjectSchema(t *testing.T) {
	for name, tt := range map[string]struct {
		schema any
		ok     bool
	}{
		"schema of an object": {&jsonschema.Schema{Type: "object"}, true},
		"schema of a string":  {&jsonschema.Schema{Type: "string"}, false},
		"schema of nothing":   {&jsonschema.Schema{}, false},
		"map of an object":    {map[string]any{"type": "object"}, true},
		"map of an array":     {map[string]any{"type": "array"}, false},
		"string":              {"object", false},
		"number":              {5, false},
		"channel":             {make(chan int), false},
	} {
		mustNotPanic(t, "validating "+name, func() {
			if err := validateObjectSchema(tt.schema); (err == nil) != tt.ok {
				t.Errorf("%s: validateObjectSchema = %v, want ok = %v", name, err, tt.ok)
			}
		})
	}
}

func TestCreateContent(t *testing.T) {
	content, data := createContent("key", "some text")
	if text := content.(*gomcp.TextContent).Text; !strings.Contains(text, "key: some text") || data != nil {
		t.Errorf("string: content = %q, data = %v", text, data)
	}
	content, data = createContent("key", a2aproto.TextPart{Text: "part text"})
	if text := content.(*gomcp.TextContent).Text; text != "part text" || data != nil {
		t.Errorf("text part: content = %q, data = %v", text, data)
	}
	content, data = createContent("key", map[string]any{"a": 1})
	if text := content.(*gomcp.TextContent).Text; !strings.Contains(text, `key: `) || !strings.Contains(text, `"a"`) || data == nil {
		t.Errorf("object: content = %q, data = %v", text, data)
	}
}

// newToolContextOfEcho makes the context of a call of a tool, as the handler of the tool does, without a client.
func newToolContextOfEcho(t *testing.T) *ToolContext {
	t.Helper()
	port := isolate(t)
	s := addServer(t, port, "ctx-a", true)
	addTools(t, port, "ctx-a", "["+echoToolJSON+"]")
	rs := util.GetRequestStore(newRequest("POST", s.URI, port, ""))
	ms := &util.MCPRequestStore{RS: rs, Ctx: context.Background()}
	return NewToolContext(ms, s.GetTool("echo"), &gomcp.CallToolRequest{}, nil, false)
}

func TestPrepareResultGivesTheTimelineToAResultThatHasNoData(t *testing.T) {
	tctx := newToolContextOfEcho(t)
	result := &gomcp.CallToolResult{}
	tctx.Log("something happened")
	tctx.MCPTool.prepareResult(tctx, result)
	if result.StructuredContent != tctx.timeline {
		t.Errorf("structured content = %v, want the timeline of the call", result.StructuredContent)
	}
	// What was logged is not left to be logged again, and is an event of the call.
	if len(tctx.log) != 0 {
		t.Errorf("log = %v, want it flushed", tctx.log)
	}
	found := false
	for _, e := range tctx.timeline.Events {
		found = found || strings.Contains(e.Text, "something happened")
	}
	if !found {
		t.Error("what was logged is not in the events of the timeline")
	}
}

func TestPrepareResultAddsTheDataOfTheResultToTheTimeline(t *testing.T) {
	for name, tt := range map[string]struct {
		content any
		key     string
		want    any
	}{
		"map":             {map[string]any{"k": "v"}, "k", "v"},
		"JSON text":       {`{"j":2}`, "j", float64(2)},
		"anything else":   {[]any{"x", "y"}, "remoteData", []any{"x", "y"}},
		"text that isn't": {`not JSON`, "", nil},
	} {
		tctx := newToolContextOfEcho(t)
		result := &gomcp.CallToolResult{StructuredContent: tt.content}
		mustNotPanic(t, name, func() { tctx.MCPTool.prepareResult(tctx, result) })
		if tt.key == "" {
			if len(tctx.timeline.Data) != 0 {
				t.Errorf("%s: data = %v, want none", name, tctx.timeline.Data)
			}
			continue
		}
		if got := toJSON(t, tctx.timeline.Data[tt.key]); got != toJSON(t, tt.want) {
			t.Errorf("%s: data[%s] = %s, want %s", name, tt.key, got, toJSON(t, tt.want))
		}
	}
}

func TestPrepareResultLeavesATimelineAsItIs(t *testing.T) {
	tctx := newToolContextOfEcho(t)
	result := &gomcp.CallToolResult{StructuredContent: map[string]any{"TYPE": "TIMELINE", "Label": "from upstream"}}
	tctx.MCPTool.prepareResult(tctx, result)
	if len(tctx.timeline.Data) != 0 {
		t.Errorf("the timeline of the upstream was merged: %v", tctx.timeline.Data)
	}
}

func TestToolWithABehaviorIsRunByIt(t *testing.T) {
	port := isolate(t)
	s := addServer(t, port, "behaviors-a", true)
	for name, behavior := range map[string]string{
		"echo": `{"echo":true}`, "status": `{"status":true}`, "ping": `{"ping":true}`, "time": `{"time":true}`,
		"roots": `{"listRoots":true}`, "sample": `{"sample":true}`, "elicit": `{"elicit":true}`,
		"details": `{"serverDetails":true}`, "paths": `{"serverPaths":true}`, "servers": `{"allServers":true}`,
		"components": `{"allComponents":true}`, "add": `{"addTool":true}`, "stream": `{"stream":true}`, "none": `{}`,
	} {
		addTools(t, port, s.Name, "["+toolJSON(name, behavior, "")+"]")
		if tool := s.GetTool(name); tool == nil || tool.Behavior.run == nil {
			t.Errorf("tool %s has nothing to run", name)
		}
	}
}
