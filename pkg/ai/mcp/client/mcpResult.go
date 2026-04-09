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

package mcpclient

import (
	"fmt"
	"goto/pkg/util/timeline"
	"net/http"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type MCPResult struct {
	ID                  string
	Server              string
	Tool                string
	ToolCall            *ToolCall
	ServerInfo          *timeline.GotoServerInfo
	Timeline            *timeline.Timeline
	LastRequestHeaders  http.Header `json:"-"`
	LastResponseHeaders http.Header `json:"-"`
	LastResponseStatus  int         `json:"-"`
	LastError           error       `json:"-"`
	CallResults         map[string]*MCPCallResult
}

type MCPCallResult struct {
	RequestID       string
	Content         []gomcp.Content          `json:"Content,omitempty"`
	Data            any                      `json:"Data,omitempty"`
	ClientInfo      *timeline.GotoClientInfo `json:"ClientInfo,omitempty"`
	RemoteTimeline  any                      `json:"RemoteTimeline,omitempty"`
	RequestHeaders  http.Header              `json:"RequestHeaders,omitempty"`
	ResponseHeaders http.Header              `json:"ResponseHeaders,omitempty"`
	ResponseStatus  int                      `json:"ResponseStatus,omitempty"`
}

func NewMCPResult(server string, tc *ToolCall, t *timeline.Timeline) *MCPResult {
	return &MCPResult{
		ID:          fmt.Sprintf("[%s]@%s", tc.Tool, server),
		Server:      server,
		ServerInfo:  t.Server,
		Tool:        tc.Tool,
		ToolCall:    tc,
		CallResults: map[string]*MCPCallResult{},
		Timeline:    t,
	}
}

func (r *MCPResult) getOrAddCall(requestID string) *MCPCallResult {
	if r.CallResults[requestID] == nil {
		r.CallResults[requestID] = &MCPCallResult{
			RequestID: requestID,
		}
	}
	return r.CallResults[requestID]
}

func (r *MCPResult) storeCallResult(requestID string, result *gomcp.CallToolResult, clientInfo *timeline.GotoClientInfo) {
	if result != nil {
		cr := r.getOrAddCall(requestID)
		if !r.ToolCall.ResultOnly {
			cr.Content = result.Content
		}
		cr.ClientInfo = clientInfo
		if m, ok := result.StructuredContent.(map[string]any); ok {
			if m["TYPE"] != nil && m["TYPE"].(string) == timeline.TIMELINE {
				delete(m, "TYPE")
				if r.ToolCall.ResultOnly {
					delete(m, "Events")
				}
				cr.RemoteTimeline = m
			}
		}
		if cr.RemoteTimeline == nil {
			cr.Data = result.StructuredContent
		}
	}
}

func extractDataAndTimeline(sc any) (data map[string]any, tl *timeline.Timeline) {
	if sc != nil {
		if m, ok := sc.(map[string]any); ok {
			data = m
			if m["Timeline"] != nil {
				if t, ok := m["Timeline"].(*timeline.Timeline); ok {
					tl = t
					delete(m, "Timeline")
				}
			}
		}
	}
	return
}

func (r *MCPResult) storeHeaders(requestID string, requestHeaders, responseHeaders http.Header, status int) {
	cr := r.getOrAddCall(requestID)
	cr.RequestHeaders = requestHeaders
	cr.ResponseHeaders = responseHeaders
	cr.ResponseStatus = status
	r.LastRequestHeaders = requestHeaders
	r.LastResponseHeaders = responseHeaders
	r.LastResponseStatus = status
}

func (r *MCPResult) ToMCP() *gomcp.CallToolResult {
	result := &gomcp.CallToolResult{}
	if len(r.CallResults) > 0 {
		r.Timeline.Data["MCPCalls"] = r.buildCallsData()
		if !r.ToolCall.ResultOnly {
			for _, cr := range r.CallResults {
				result.Content = append(result.Content, cr.Content...)
			}
		}
	}
	if r.ToolCall.ResultOnly {
		r.Timeline.Events = nil
	}
	result.StructuredContent = r.Timeline
	return result
}

func (r *MCPResult) ToObject() map[string]any {
	result := map[string]any{}
	if len(r.CallResults) > 0 {
		r.Timeline.Data["MCPCalls"] = r.buildCallsData()
		if !r.ToolCall.ResultOnly {
			content := []string{}
			for _, cr := range r.CallResults {
				for _, c := range cr.Content {
					if text, ok := c.(*gomcp.TextContent); ok {
						content = append(content, text.Text)
					}
				}
			}
			result["Content"] = content
		}
	}
	if r.ToolCall.ResultOnly {
		r.Timeline.Events = nil
	}
	result["Timeline"] = r.Timeline
	return result
}

func (r *MCPResult) buildCallsData() map[string]map[string]any {
	callsData := map[string]map[string]any{}
	callsData[r.ID] = map[string]any{}
	for _, cr := range r.CallResults {
		callsData[r.ID][cr.RequestID] = cr
	}
	return callsData
}
