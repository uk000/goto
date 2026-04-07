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
	timeline       *timeline.Timeline
	progress       atomic.Int32
	log            []string
}

func NewToolCallContext(ctx context.Context, t *MCPTool, req *gomcp.CallToolRequest, args *aicommon.ToolCallArgs, isSSE bool) *ToolCallContext {
	_, rs := util.GetRequestStoreFromContext(ctx)
	requestHeaders := getRequestHeaders(ctx, req, rs)
	tctx := &ToolCallContext{
		MCPTool:        t,
		sessionID:      req.Session.ID(),
		listener:       rs.ListenerLabel,
		rs:             rs,
		sse:            isSSE,
		ctx:            ctx,
		requestHeaders: requestHeaders,
		req:            req,
		args:           args,
		progress:       atomic.Int32{},
	}
	tctx.timeline = timeline.NewTimeline(t.Server.Port, t.Label, map[string]any{
		constants.HeaderGotoMCPServer: t.Server.ID,
		constants.HeaderGotoMCPTool:   t.Name,
	}, args, requestHeaders, nil, tctx.notifyClientWithError, tctx.notifyClient)
	tctx.timeline.StartTimeline(t.Label, fmt.Sprintf("%s: Received Tool Call [%s]", t.Server.Name, t.Label), tctx.timeline.Server)
	return tctx
}

func (tctx *ToolCallContext) notifyClientWithError(msg string, data any, json bool) error {
	tctx.notifyClient(msg, data, json)
	return nil
}

func (tctx *ToolCallContext) notifyClient(msg string, data any, json bool) {
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

func (tctx *ToolCallContext) AddEvent(msg string, remoteData any, json bool) {
	if msg != "" || remoteData != nil {
		tctx.timeline.AddEvent(tctx.Label, msg, nil, remoteData, json)
	}
}

func (tctx *ToolCallContext) AddData(data any, json bool) {
	if data != nil {
		tctx.timeline.AddData(tctx.Label, data, json)
	}
}

func (tctx *ToolCallContext) Log(msg string, args ...any) string {
	msg = fmt.Sprintf(msg, args...)
	tctx.log = append(tctx.log, msg)
	return msg
}

func (tctx *ToolCallContext) Flush(print, clear bool) string {
	msg := strings.Join(tctx.log, " --> ")
	if clear {
		tctx.log = []string{}
	}
	if print {
		log.Println(msg)
	}
	return msg
}

func (tctx *ToolCallContext) applyDelay() {
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
