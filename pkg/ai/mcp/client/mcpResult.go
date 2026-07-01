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
	"goto/pkg/util"
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
	UpstreamStatuses    map[string]int
	LastRequestHeaders  http.Header `json:"-"`
	LastResponseHeaders http.Header `json:"-"`
	LastResponseStatus  int         `json:"-"`
	LastRemoteHeaders   map[string]any
	LastError           error `json:"-"`
	CallResults         map[string]*MCPCallResult
	RemoteGotos         map[string]bool
}

type MCPCallResult struct {
	RequestID       string
	Content         []gomcp.Content          `json:"Content,omitempty"`
	Data            any                      `json:"Data,omitempty"`
	ClientInfo      *timeline.GotoClientInfo `json:"ClientInfo,omitempty"`
	RemoteTimeline  *timeline.Timeline       `json:"RemoteTimeline,omitempty"`
	RemoteResult    any                      `json:"RemoteResult,omitempty"`
	RemoteHeaders   any                      `json:"RemoteHeaders,omitempty"`
	RequestHeaders  http.Header              `json:"RequestHeaders,omitempty"`
	ResponseHeaders http.Header              `json:"ResponseHeaders,omitempty"`
	ResponseStatus  int                      `json:"ResponseStatus,omitempty"`
	parent          *MCPResult
}

func NewMCPResult(server string, tc *ToolCall, t *timeline.Timeline) *MCPResult {
	return &MCPResult{
		ID:               fmt.Sprintf("[%s]@%s", tc.Tool, server),
		Server:           server,
		ServerInfo:       t.Server,
		Tool:             tc.Tool,
		ToolCall:         tc,
		CallResults:      map[string]*MCPCallResult{},
		Timeline:         t,
		RemoteGotos:      map[string]bool{},
		UpstreamStatuses: map[string]int{},
	}
}

func (r *MCPResult) getOrAddCall(requestID string) *MCPCallResult {
	if r.CallResults[requestID] == nil {
		r.CallResults[requestID] = &MCPCallResult{
			RequestID: requestID,
			parent:    r,
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
		t := timeline.CheckAndGetTimeline(result.StructuredContent)
		if t != nil {
			if r.ToolCall.NoEvents {
				t.Events = nil
			}
			cr.RemoteTimeline = t
			r.LastRemoteHeaders = t.UpstreamHeaders
			if t.UpstreamHeaders != nil {
				viaGotos := util.GetViaGotosFromUpstreamHeaders(t.UpstreamHeaders)
				for v := range viaGotos {
					r.RemoteGotos[v] = true
				}
			}
		} else {
			upresult, upheaders := timeline.CheckAndGetResultOrHeaders(result.StructuredContent)
			if upresult != nil || upheaders != nil {
				cr.RemoteResult = upresult
				cr.RemoteHeaders = upheaders
				r.LastRemoteHeaders = upheaders
				if upheaders != nil {
					viaGotos := util.GetViaGotosFromUpstreamHeaders(upheaders)
					for v := range viaGotos {
						r.RemoteGotos[v] = true
					}
				}
			} else {
				cr.Data = result.StructuredContent
			}
		}
	}
}

func extractDataAndTimeline(v any) (data map[string]any, t *timeline.Timeline) {
	if v != nil {
		if m, ok := v.(map[string]any); ok {
			t = timeline.CheckAndGetTimeline(m)
			if t != nil {
				delete(m, "Timeline")
			}
			data = m
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
	r.UpstreamStatuses[r.Tool] = status
}

func (r *MCPResult) ToMCP() *gomcp.CallToolResult {
	result := &gomcp.CallToolResult{}
	if !r.ToolCall.NoCallDetails {
		if len(r.CallResults) > 0 {
			r.Timeline.Data["MCPCalls"] = r.buildCallsData()
			if !r.ToolCall.ResultOnly {
				for _, cr := range r.CallResults {
					result.Content = append(result.Content, cr.Content...)
				}
			}
		}
	}
	if r.ToolCall.NoEvents {
		r.Timeline.Events = nil
	}
	if r.LastRemoteHeaders != nil {
		for k, v := range r.LastRemoteHeaders {
			r.Timeline.UpstreamHeaders[k] = v
		}
	}
	r.Timeline.UpstreamHeaders[r.Tool] = map[string]any{
		"RequestHeaders":  r.LastRequestHeaders,
		"ResponseHeaders": r.LastResponseHeaders,
	}
	r.Timeline.UpstreamStatuses = r.UpstreamStatuses
	r.Timeline.Status = r.LastResponseStatus
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
	if r.ToolCall.NoEvents {
		r.Timeline.Events = nil
	}
	result["Timeline"] = r.Timeline
	return result
}

func (r *MCPResult) buildCallsData() map[string]map[string]any {
	callsData := map[string]map[string]any{}
	callsData[r.ID] = map[string]any{}
	for _, cr := range r.CallResults {
		if r.ToolCall.ResultOnly {
			cr.Content = nil
			if cr.RemoteTimeline != nil {
				cr.RemoteTimeline.Events = nil
			}
		}
		callsData[r.ID][cr.RequestID] = cr
		r.Timeline.AddRemoteCall(r.ID, cr.RequestID, cr.RemoteTimeline)
	}
	return callsData
}
