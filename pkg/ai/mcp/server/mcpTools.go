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
	"goto/pkg/constants"
	"goto/pkg/metrics"
	"goto/pkg/server/echo"
	"goto/pkg/transport"
	"goto/pkg/types"
	"goto/pkg/util"
	"log"
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
	Behavior ToolBehavior `json:"behavior,omitempty"`
	Config   ToolConfig   `json:"config"`
	client   transport.ClientTransport
}

type ToolBehavior struct {
	Ping          bool `json:"ping,omitempty"`
	Echo          bool `json:"echo,omitempty"`
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
	run           func(t *ToolCallContext) (*mcp.CallToolResult, error)
}

type ToolConfig struct {
	RemoteTool  *mcpclient.ToolCall     `json:"remote,omitempty"`
	MultiRemote [][]*mcpclient.ToolCall `json:"multiRemote,omitempty"`
	Agent       *a2aclient.AgentCall    `json:"agent,omitempty"`
	Delay       *types.Delay            `json:"delay,omitempty"`
	StreamCount int                     `json:"streamCount,omitempty"`
}

type ToolCallContext struct {
	*MCPTool
	sessionID      string
	listener       string
	rs             *util.RequestStore
	sse            bool
	ctx            context.Context
	requestHeaders http.Header
	req            *gomcp.CallToolRequest
	args           *aicommon.ToolCallArgs
	hops           *util.Hops
	log            []string
}

type ToolState struct {
	RequestHeaders map[string][]string    `json:"requestHeaders,omitempty"`
	Args           *aicommon.ToolCallArgs `json:"args,omitempty"`
	Delay          *types.Delay           `json:"delay,omitempty"`
	ResponseCount  int                    `json:"responseCount,omitempty"`
}

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

func ParseTool(payload []byte) (tool *MCPTool, err error) {
	tool = &MCPTool{}
	if err = json.Unmarshal(payload, tool); err != nil {
		return
	}
	if tool.Tool == nil {
		return nil, fmt.Errorf("Missing tool definition")
	}
	tool.Kind = KindTools
	if tool.Name == "" {
		tool.Name = tool.Tool.Name
	}
	tool.Name = strings.ReplaceAll(tool.Name, "\"", "")
	if err := tool.prepareBehavior(); err != nil {
		return nil, err
	}
	return
}

func (t *MCPTool) prepareBehavior() error {
	if (t.Behavior.Fetch && (t.Config.RemoteTool == nil || t.Config.RemoteTool.URL == "")) ||
		(t.Behavior.Remote && (t.Config.RemoteTool == nil || t.Config.RemoteTool.URL == "")) ||
		(t.Behavior.MultiRemote && len(t.Config.MultiRemote) == 0) ||
		(t.Behavior.Agents && (t.Config.Agent == nil || t.Config.Agent.AgentURL == "")) {
		return errors.New("Incomplete remote configs")
	}
	if t.Behavior.Fetch {
		isTLS := strings.HasPrefix(t.Config.RemoteTool.URL, "https:")
		client := transport.CreateDefaultHTTPClient(t.Name, false, isTLS, t.Config.RemoteTool.Authority, metrics.ConnTracker)
		t.client = client
	}
	if t.Behavior.Echo {
		t.Behavior.run = t.echo
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
		t.Behavior.run = t.sendServerDetails
	} else if t.Behavior.ServerPaths {
		t.Behavior.run = t.sendServerPaths
	} else if t.Behavior.AllServers {
		t.Behavior.run = t.sendAllServers
	} else if t.Behavior.AllComponents {
		t.Behavior.run = t.sendAllComponents
	} else {
		t.Behavior.run = t.stream
	}
	return nil
}

func (t *MCPTool) Handle(ctx context.Context, req *gomcp.CallToolRequest) (result *gomcp.CallToolResult, err error) {
	_, rs := util.GetRequestStoreFromContext(ctx)
	isSSE := false
	if rs != nil {
		if rs.RequestedMCPTool != "" && !strings.EqualFold(rs.RequestedMCPTool, t.Name) && !strings.Contains(t.Name, "toolcall") {
			return nil, fmt.Errorf("URI [%s] doesn't match tool [%s] requested in RPC", rs.RequestedMCPTool, t.Name)
		}
		isSSE = rs.IsSSE
	}
	if !isSSE {
		isSSE = util.IsSSE(ctx)
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
	tctx := &ToolCallContext{
		MCPTool:        t,
		sessionID:      req.Session.ID(),
		listener:       rs.ListenerLabel,
		rs:             rs,
		sse:            isSSE,
		ctx:            ctx,
		requestHeaders: getRequestHeaders(ctx, req, rs),
		req:            req,
		args:           args,
	}
	// rs.ResponseWriter.Header().Add(constants.HeaderGotoMCPServer, t.Server.Name)
	// rs.ResponseWriter.Header().Add(constants.HeaderGotoMCPTool, t.Name)
	tctx.hops = util.NewHops(t.Server.ID, tctx.rs.ListenerLabel, t.Label)
	// t.notifyClient(t.Log("%s: Received request with Args [%+v] Remote Args [%+v] Headers [%+v]", t.Label, t.args, t.remoteArgs, t.requestHeaders), 0)
	tctx.Hop(tctx.Flush(true))
	result, err = t.Behavior.run(tctx)
	if result == nil {
		result = &gomcp.CallToolResult{}
	}
	t.prepareResult(tctx, result)
	return
}

func (t *MCPTool) prepareResult(tctx *ToolCallContext, result *gomcp.CallToolResult) {
	output := map[string]any{}
	if result.StructuredContent != nil {
		m := reflect.ValueOf(result.StructuredContent)
		if m.Kind() == reflect.Map {
			iter := m.MapRange()
			for iter.Next() {
				k := iter.Key().String()
				v := iter.Value().Interface()
				output[k] = v
			}
		} else {
			output["structuredContent"] = result.StructuredContent
		}
	}
	tctx.Hop(tctx.Flush(true))
	tctx.rs.GotoProtocol = "MCP"
	tctx.rs.IsJSONRPC = true
	tctx.rs.RequestPortNum = tctx.MCPTool.Server.Port
	if tctx.rs.RequestHeaders == nil {
		tctx.rs.RequestHeaders = tctx.requestHeaders
	}
	output[constants.HeaderGotoServerInfo] = echo.GetEchoResponseWithAddendum(tctx.rs, map[string]any{"Goto-MCP-Server": t.Server.ID, "Goto-MCP-Tool": t.Name})
	output["hops"] = tctx.hops.Steps
	result.StructuredContent = output
}

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

func (tctx *ToolCallContext) notifyClient(msg string, stats ...float64) {
	var total, progress float64
	if tctx.Response != nil {
		total = float64(tctx.Response.Count())
	}
	if len(stats) > 0 {
		progress = stats[0]
	}
	if len(stats) > 1 {
		total = stats[1]
	}
	params := &gomcp.ProgressNotificationParams{
		ProgressToken: tctx.req.Params.Meta.GetMeta()["progressToken"],
		Total:         total,
		Progress:      progress,
		Message:       msg,
	}
	tctx.req.Session.NotifyProgress(tctx.ctx, params)
}

func (tctx *ToolCallContext) assignClientHops(text string, data map[string]any) map[string]any {
	if data == nil {
		json := util.JSONFromJSONText(text)
		if json != nil {
			data = json.Object()
		}
	}
	if s := data["hops"]; s != nil {
		if steps, ok := s.([]any); ok {
			for _, step := range steps {
				tctx.hops.AddRemote(step)
			}
		}
		delete(data, "hops")
	}
	if len(data) == 0 {
		data = nil
	}
	return data
}

func (tctx *ToolCallContext) Log(msg string, args ...any) string {
	msg = fmt.Sprintf(msg, args...)
	tctx.log = append(tctx.log, msg)
	return msg
}

func (tctx *ToolCallContext) Flush(print bool) string {
	msg := strings.Join(tctx.log, " --> ")
	tctx.log = []string{}
	if print {
		log.Println(msg)
	}
	return msg
}

func (tctx *ToolCallContext) Hop(msg string) {
	if msg != "" {
		tctx.hops.Add(msg)
	}
}

func (tctx *ToolCallContext) applyDelay() {
	if tctx.args.Delay != nil {
		d := tctx.args.Delay.Compute()
		tctx.notifyClient(tctx.Log("Server %s Tool %s: \U0001F634\U0001F4A4 sleeping for [%s]", tctx.Label, tctx.Tool.Name, d), 0)
		tctx.args.Delay.Apply()
	}
}

func getRequestHeaders(ctx context.Context, req *gomcp.CallToolRequest, rs *util.RequestStore) http.Header {
	headers := req.Extra.Header
	if rs != nil {
		if headers != nil {
			rs.RequestHeaders = headers
		} else {
			headers = rs.RequestHeaders
		}
	}
	if headers == nil {
		headers = util.GetRequestHeaders(ctx)
	}
	if headers != nil && rs != nil {
		headers["RequestURI"] = []string{rs.RequestURI}
		headers["RequestHost"] = []string{rs.RequestHost}
	}
	return headers
}

func parseArgs(raw json.RawMessage) (args *aicommon.ToolCallArgs, err error) {
	if len(raw) > 0 {
		args = aicommon.NewCallArgs()
		err = json.Unmarshal([]byte(raw), &args)
	}
	return
}
