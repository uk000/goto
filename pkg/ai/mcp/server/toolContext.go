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
	"fmt"
	aicommon "goto/pkg/ai/common"
	"goto/pkg/constants"
	"goto/pkg/util"
	"goto/pkg/util/timeline"
	"log"
	"net/http"
	"strings"
	"sync/atomic"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type ToolContext struct {
	*MCPTool
	sessionID      string
	listener       string
	rs             *util.RequestStore
	ms             *util.MCPRequestStore
	sse            bool
	ctx            context.Context
	requestHeaders http.Header
	req            *gomcp.CallToolRequest
	args           *aicommon.ToolCallArgs
	timeline       *timeline.Timeline
	progress       atomic.Int32
	log            []string
}

func NewToolContext(ms *util.MCPRequestStore, t *MCPTool, req *gomcp.CallToolRequest, args *aicommon.ToolCallArgs, isSSE bool) *ToolContext {
	requestHeaders := getRequestHeaders(ms.Ctx, req, ms.RS)
	if args == nil {
		args = aicommon.NewCallArgs()
	}
	if t.Behavior.Remote && args.RemoteArgs == nil {
		args.RemoteArgs = aicommon.NewCallArgs()
	}
	tctx := &ToolContext{
		MCPTool:        t,
		sessionID:      req.Session.ID(),
		listener:       ms.RS.ListenerLabel,
		rs:             ms.RS,
		ms:             ms,
		sse:            isSSE,
		ctx:            ms.Ctx,
		requestHeaders: requestHeaders,
		req:            req,
		args:           args,
		progress:       atomic.Int32{},
	}
	tctx.timeline = timeline.NewTimeline(t.Server.Port, t.Label, map[string]any{
		constants.HeaderGotoMCPServer: t.Server.ID,
		constants.HeaderGotoMCPTool:   t.Name,
	}, args, requestHeaders, nil, tctx.notifyClientWithError, tctx.notifyClient)
	tctx.timeline.ResultOnly = args.ResultOnly
	tctx.timeline.NoEvents = args.NoEvents
	tctx.timeline.AddEvent(t.Label, fmt.Sprintf("%s: Received Tool Call [%s]", t.Server.Name, t.Label))
	return tctx
}

func (tctx *ToolContext) notifyClientWithError(msg string, data any, json bool) error {
	tctx.notifyClient(msg, data, json)
	return nil
}

func (tctx *ToolContext) notifyClient(msg string, data any, json bool) {
	if json {
		// if msg != "" {
		// 	notifyClient(tctx.ctx, msg, tctx.req.Session, nil)
		// }
		msg = util.ToJSONText(data)
		notifyClient(tctx.ctx, tctx.req.Params, msg, tctx.req.Session, gomcp.Meta{"json": true})
	} else {
		if msg != "" && data != nil {
			msg = fmt.Sprintf("%s: %s", msg, util.ToJSONText(data))
		}
		notifyClient(tctx.ctx, tctx.req.Params, msg, tctx.req.Session, nil)
	}
}

func (tctx *ToolContext) AddEvent(msg string) {
	if msg != "" {
		tctx.timeline.AddEvent(tctx.Label, msg)
	}
}

func (tctx *ToolContext) AddRemoteEvent(msg string, remoteText string, remoteData any, json bool) {
	if msg != "" || remoteData != nil {
		tctx.timeline.AddEventWithRemote(tctx.Label, msg, remoteText, nil, nil, remoteData, json)
	}
}

func (tctx *ToolContext) AddData(data any, json bool) {
	if data != nil {
		tctx.timeline.AddData(tctx.Label, data, json)
	}
}

func (tctx *ToolContext) Log(msg string, args ...any) string {
	msg = fmt.Sprintf(msg, args...)
	tctx.log = append(tctx.log, msg)
	return msg
}

func (tctx *ToolContext) Flush(print, clear bool) string {
	msg := strings.Join(tctx.log, " --> ")
	if clear {
		tctx.log = []string{}
	}
	if print {
		log.Println(msg)
	}
	return msg
}

func (tctx *ToolContext) applyDelay() {
	if tctx.args.Delay != nil {
		d := tctx.args.Delay.Compute()
		tctx.Log("Server %s Tool %s: \U0001F634\U0001F4A4 sleeping for [%s]", tctx.Label, tctx.Tool.Name, d)
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
	if err == nil {
		args.ToolDef = map[string]any{}
		err = json.Unmarshal([]byte(raw), &args.ToolDef)
	}
	return
}

func notifyClient(ctx context.Context, params gomcp.Params, msg string, session *gomcp.ServerSession, meta gomcp.Meta) {
	np := &gomcp.ProgressNotificationParams{
		Meta:          meta,
		ProgressToken: params.GetMeta()["progressToken"],
		Message:       msg,
	}
	session.NotifyProgress(ctx, np)
}
