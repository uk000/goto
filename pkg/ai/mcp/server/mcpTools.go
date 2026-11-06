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
	"encoding/json"
	"errors"
	"fmt"
	"goto/pkg/metrics"
	"goto/pkg/transport"
	"goto/pkg/types"
	"goto/pkg/util"
	"goto/pkg/util/timeline"
	"net/http"
	"reflect"
	"strings"
	"time"

	a2aclient "goto/pkg/ai/a2a/client"
	aicommon "goto/pkg/ai/common"
	mcpclient "goto/pkg/ai/mcp/client"

	a2aproto "trpc.group/trpc-go/trpc-a2a-go/protocol"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type MCPTool struct {
	MCPComponent
	Tool     *gomcp.Tool  `json:"tool"`
	Schema   string       `json:"schema,omitempty"`
	Behavior ToolBehavior `json:"behavior"`
	Config   ToolConfig   `json:"config"`
	client   transport.ClientTransport
}

type ToolBehavior struct {
	Ping          bool `json:"ping,omitempty"`
	Echo          bool `json:"echo,omitempty"`
	Status        bool `json:"status,omitempty"`
	Time          bool `json:"time,omitempty"`
	Stream        bool `json:"stream,omitempty"`
	Elicit        bool `json:"elicit,omitempty"`
	Sample        bool `json:"sample,omitempty"`
	ListRoots     bool `json:"listRoots,omitempty"`
	Fetch         bool `json:"fetch,omitempty"`
	Remote        bool `json:"remote,omitempty"`
	MultiRemote   bool `json:"multiRemote,omitempty"`
	Resumable     bool `json:"resumable,omitempty"`
	Agents        bool `json:"agents,omitempty"`
	ServerDetails bool `json:"serverDetails,omitempty"`
	ServerPaths   bool `json:"serverPaths,omitempty"`
	AllServers    bool `json:"allServers,omitempty"`
	AllComponents bool `json:"allComponents,omitempty"`
	AddTool       bool `json:"addTool,omitempty"`
	run           func(t *ToolContext) (*mcp.CallToolResult, error)
}

type ToolConfig struct {
	RemoteTool  *mcpclient.ToolCall     `json:"remote,omitempty"`
	MultiRemote [][]*mcpclient.ToolCall `json:"multiRemote,omitempty"`
	Agent       *a2aclient.AgentCall    `json:"agent,omitempty"`
	Delay       *types.Delay            `json:"delay,omitempty"`
	StreamCount int                     `json:"streamCount,omitempty"`
}

type ToolState struct {
	RequestHeaders map[string][]string    `json:"requestHeaders,omitempty"`
	Args           *aicommon.ToolCallArgs `json:"args,omitempty"`
	Delay          *types.Delay           `json:"delay,omitempty"`
	ResponseCount  int                    `json:"responseCount,omitempty"`
}

// NewMCPTool creates a tool with the given name and description, and empty schemas.
func NewMCPTool(name, desc string) *MCPTool {
	return &MCPTool{
		Tool: &gomcp.Tool{
			Meta:         map[string]any{},
			Annotations:  &gomcp.ToolAnnotations{},
			Title:        name,
			Name:         name,
			Description:  desc,
			InputSchema:  &jsonschema.Schema{},
			OutputSchema: &jsonschema.Schema{},
		},
	}
}

// ParseTools parses a JSON or YAML list of tools, and validates each of them.
func ParseTools(b []byte) (tools []*MCPTool, err error) {
	tools = []*MCPTool{}
	if err = util.ReadJsonOrYamlPayloadFromBytes(b, &tools); err != nil {
		return
	}
	for _, tool := range tools {
		if err = processTool(tool); err != nil {
			return nil, err
		}
	}
	return tools, nil
}

// ParseTool parses a JSON tool, and validates it.
func ParseTool(payload []byte) (tool *MCPTool, err error) {
	tool = &MCPTool{}
	if err = json.Unmarshal(payload, tool); err != nil {
		return nil, err
	}
	if err = processTool(tool); err != nil {
		return nil, err
	}
	return
}

// isNilSchema tells if the schema is not given, whatever it is made of: a schema that is not a pointer or a map (e.g.
// a string in the payload of a tool) is given, even if it is not a valid one.
func isNilSchema(schema any) bool {
	if schema == nil {
		return true
	}
	switch v := reflect.ValueOf(schema); v.Kind() {
	case reflect.Map, reflect.Pointer, reflect.Slice, reflect.Interface, reflect.Func, reflect.Chan:
		return v.IsNil()
	}
	return false
}

// validateObjectSchema checks that the schema is the one of an object, which is what the input of a tool is. The SDK
// doesn't take a tool otherwise, it panics.
func validateObjectSchema(schema any) error {
	if s, ok := schema.(*jsonschema.Schema); ok {
		if s.Type != "object" {
			return fmt.Errorf("Tool schema must have type \"object\", got [%s]", s.Type)
		}
		return nil
	}
	b, err := json.Marshal(schema)
	if err != nil {
		return fmt.Errorf("Invalid tool schema: %s", err.Error())
	}
	m := map[string]any{}
	if err := json.Unmarshal(b, &m); err != nil {
		return fmt.Errorf("Tool schema must be an object: %s", err.Error())
	}
	if m["type"] != "object" {
		return fmt.Errorf("Tool schema must have type \"object\", got [%v]", m["type"])
	}
	return nil
}

// processTool validates a parsed tool, resolving its input schema (from the named tool schemas if not given) and name.
func processTool(tool *MCPTool) error {
	if tool.Tool == nil {
		return fmt.Errorf("Missing tool definition")
	}
	if isNilSchema(tool.Tool.InputSchema) {
		if tool.Schema == "" {
			return fmt.Errorf("Missing tool schema")
		}
		lock.RLock()
		ts := ToolSchemas[tool.Schema]
		lock.RUnlock()
		if ts == nil {
			return fmt.Errorf("Undefined tool schema [%s]", tool.Schema)
		}
		tool.Tool.InputSchema = ts
	}
	if err := validateObjectSchema(tool.Tool.InputSchema); err != nil {
		return err
	}
	tool.Kind = KindTools
	if tool.Name == "" {
		tool.Name = tool.Tool.Name
	}
	tool.Name = strings.ReplaceAll(tool.Name, "\"", "")
	if tool.Name == "" {
		return fmt.Errorf("Missing tool name")
	}
	tool.Tool.Name = tool.Name
	if err := tool.validateBehavior(); err != nil {
		return err
	}
	return nil
}

// validateBehavior checks that the behaviors that call a remote have the remote config that they need.
func (t *MCPTool) validateBehavior() error {
	if (t.Behavior.Fetch && (t.Config.RemoteTool == nil || t.Config.RemoteTool.URL == "")) ||
		(t.Behavior.Remote && (t.Config.RemoteTool == nil || t.Config.RemoteTool.URL == "")) ||
		(t.IsProxy && (t.Config.RemoteTool == nil || t.Config.RemoteTool.URL == "")) ||
		(t.Behavior.MultiRemote && len(t.Config.MultiRemote) == 0) ||
		(t.Behavior.Agents && (t.Config.Agent == nil || t.Config.Agent.AgentURL == "")) {
		return errors.New("Incomplete remote configs")
	}
	return nil
}

// prepareBehavior validates the behavior and prepares the remote configs, and picks the function that runs the tool (streaming is the default).
func (t *MCPTool) prepareBehavior() error {
	if err := t.validateBehavior(); err != nil {
		return err
	}
	if t.Config.RemoteTool != nil {
		t.Config.RemoteTool.Prepare()
	}
	if t.Config.Agent != nil {
		t.Config.Agent.Prepare()
	}
	if t.Behavior.Fetch {
		isTLS := strings.HasPrefix(t.Config.RemoteTool.URL, "https:")
		client := transport.CreateDefaultHTTPClient(t.Server.Port, t.Name, false, isTLS, false, t.Config.RemoteTool.Authority, metrics.ConnTracker)
		t.client = client
	}
	if t.Behavior.Echo {
		t.Behavior.run = t.echo
	} else if t.Behavior.Status {
		t.Behavior.run = t.status
	} else if t.Behavior.Ping {
		t.Behavior.run = t.ping
	} else if t.Behavior.Time {
		t.Behavior.run = t.sendTime
	} else if t.Behavior.ListRoots {
		t.Behavior.run = t.listRoots
	} else if t.Behavior.Sample {
		t.Behavior.run = t.sample
	} else if t.Behavior.Elicit {
		t.Behavior.run = t.elicit
	} else if t.Behavior.Fetch {
		t.Behavior.run = t.fetch
	} else if t.Behavior.Remote {
		t.Behavior.run = t.callRemoteTool
	} else if t.Behavior.Agents {
		t.Behavior.run = t.callRemoteAgent
	} else if t.Behavior.ServerDetails {
		t.Behavior.run = t.serverDetails
	} else if t.Behavior.ServerPaths {
		t.Behavior.run = t.serverPaths
	} else if t.Behavior.AllServers {
		t.Behavior.run = t.listServers
	} else if t.Behavior.AllComponents {
		t.Behavior.run = t.listComponents
	} else if t.Behavior.AddTool {
		t.Behavior.run = t.addTool
	} else {
		t.Behavior.run = t.stream
	}
	return nil
}

// Handle serves a call of the tool by parsing the args and running its behavior, and records the forced status and upstream results.
func (t *MCPTool) Handle(ctx context.Context, req *gomcp.CallToolRequest) (result *gomcp.CallToolResult, err error) {
	var header http.Header
	if req.Extra != nil {
		header = req.Extra.Header
	}
	shared, ms := t.Server.callStoreOf(storeKeyOf(header), ctx)
	rs := ms.RS
	isSSE := false
	if rs.RequestedMCPTool != "" && !strings.EqualFold(rs.RequestedMCPTool, t.Name) && !strings.Contains(t.Name, "toolcall") {
		return nil, fmt.Errorf("URI [%s] doesn't match tool [%s] requested in RPC", rs.RequestedMCPTool, t.Name)
	}
	isSSE = rs.IsSSE
	if !isSSE {
		isSSE = util.IsSSE(ms.Ctx)
	}
	var args *aicommon.ToolCallArgs
	if req.Params != nil && req.Params.Arguments != nil {
		if args, err = parseArgs(req.Params.Arguments); err != nil {
			return nil, err
		}
	} else {
		args = aicommon.NewCallArgs()
	}
	args.UpdateDelay(t.Config.Delay)
	tctx := NewToolContext(ms, t, req, args, isSSE)
	// rs.ResponseWriter.Header().Add(constants.HeaderGotoMCPServer, t.Server.Name)
	// rs.ResponseWriter.Header().Add(constants.HeaderGotoMCPTool, t.Name)
	// t.notifyClient(t.Log("%s: Received request with Args [%+v] Remote Args [%+v] Headers [%+v]", t.Label, t.args, t.remoteArgs, t.requestHeaders), 0)
	result, err = t.Behavior.run(tctx)
	if shared != nil && ms.ForcedStatus > 0 {
		setForcedStatus(shared, ms.ForcedStatus)
	}
	if result == nil {
		result = &gomcp.CallToolResult{}
	}
	for v := range tctx.timeline.RemoteGotos {
		rs.ViaGotos = append(rs.ViaGotos, v)
	}
	rs.UpstreamStatuses = tctx.timeline.UpstreamStatuses
	t.prepareResult(tctx, result)
	return
}

// prepareResult flushes the log into the timeline, and merges the structured content of the result with the timeline.
func (t *MCPTool) prepareResult(tctx *ToolContext, result *gomcp.CallToolResult) {
	msg := tctx.Flush(true, true)
	if len(msg) > 0 {
		tctx.AddEvent(msg)
	}
	if result.StructuredContent != nil {
		if !timeline.IsTimeline(result.StructuredContent) {
			m := reflect.ValueOf(result.StructuredContent)
			if m.Kind() == reflect.Map {
				iter := m.MapRange()
				for iter.Next() {
					k := iter.Key().String()
					v := iter.Value().Interface()
					tctx.timeline.Data[k] = v
				}
			} else if m.Kind() == reflect.String {
				json, ok := util.JSONFromJSONText(m.String())
				if ok && !json.IsEmpty() {
					for k, v := range json.Object() {
						tctx.timeline.Data[k] = v
					}
				}
			} else {
				tctx.timeline.Data["remoteData"] = result.StructuredContent
			}
		}
	} else {
		result.StructuredContent = tctx.timeline
	}
}

// createContent makes the text content for a result, along with the data to send for results that are not text.
func createContent(key string, result any) (gomcp.Content, any) {
	content := &gomcp.TextContent{}
	var data any
	if s, ok := result.(string); ok {
		content.Text = fmt.Sprintf("[%s] %s: %s", time.Now().Format(time.RFC3339Nano), key, s)
	} else if t, ok := result.(a2aproto.TextPart); ok {
		content.Text = t.Text
	} else {
		content.Text = fmt.Sprintf("[%s] %s: %s", time.Now().Format(time.RFC3339Nano), key, util.ToJSONText(result))
		data = result
	}
	return content, data
}
