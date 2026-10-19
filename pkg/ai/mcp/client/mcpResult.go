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
	"encoding/json"
	"fmt"
	"goto/pkg/util"
	"goto/pkg/util/timeline"
	"maps"
	"net/http"
	"sync"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type MCPResult struct {
	ID                  string
	Server              string
	Tool                string
	ToolCall            *ToolCall
	ServerInfo          *timeline.GotoServerInfo
	Timeline            *timeline.Timeline
	UpstreamStatuses    map[string]any
	LastRequestHeaders  http.Header `json:"-"`
	LastResponseHeaders http.Header `json:"-"`
	LastResponseStatus  int         `json:"-"`
	LastRemoteHeaders   map[string]any
	LastError           error `json:"-"`
	CallResults         map[string]*MCPCallResult
	RemoteGotos         map[string]bool
	// lock guards the fields above. Calls of a tool run concurrently, and their responses (and the trailers of those)
	// are stored by the goroutines of the HTTP transport, while the caller waits for the result.
	lock sync.Mutex
}

type MCPCallResult struct {
	RequestID       string
	Content         []gomcp.Content          `json:"Content,omitempty"`
	Data            any                      `json:"Data,omitempty"`
	ClientInfo      *timeline.GotoClientInfo `json:"-"`
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
		UpstreamStatuses: map[string]any{},
	}
}

// getOrAddCall returns the result of the request. The caller holds the lock.
func (r *MCPResult) getOrAddCall(requestID string) *MCPCallResult {
	if r.CallResults == nil {
		r.CallResults = map[string]*MCPCallResult{}
	}
	if r.CallResults[requestID] == nil {
		r.CallResults[requestID] = &MCPCallResult{
			RequestID: requestID,
			parent:    r,
		}
	}
	return r.CallResults[requestID]
}

func (r *MCPResult) storeCallResult(requestID string, result *gomcp.CallToolResult, clientInfo *timeline.GotoClientInfo) {
	if result == nil {
		return
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	if r.RemoteGotos == nil {
		r.RemoteGotos = map[string]bool{}
	}
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
	r.lock.Lock()
	defer r.lock.Unlock()
	cr := r.getOrAddCall(requestID)
	cr.RequestHeaders = requestHeaders
	cr.ResponseHeaders = responseHeaders
	cr.ResponseStatus = status
	r.LastRequestHeaders = requestHeaders
	r.LastResponseHeaders = responseHeaders
	r.LastResponseStatus = status
	// The statuses map may be shared with whoever got it before (the request store of the inbound request), so it's
	// replaced instead of being updated.
	statuses := maps.Clone(r.UpstreamStatuses)
	if statuses == nil {
		statuses = map[string]any{}
	}
	statuses[r.Tool] = status
	r.UpstreamStatuses = statuses
}

// storeResponseDone stores what is known of a response when its body is done: the headers, which by then have the
// trailers, the via-gotos of the remote, and the upstream statuses that the remote reported. The upstream statuses are
// also handed to the request store of the inbound request, if given.
func (r *MCPResult) storeResponseDone(requestID string, headers http.Header, remoteGotos map[string]bool, upstreamStatuses map[string]any, rs *util.RequestStore) {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.LastResponseHeaders = headers
	if cr := r.CallResults[requestID]; cr != nil {
		cr.ResponseHeaders = headers
	}
	r.addRemoteGotos(remoteGotos)
	statuses := maps.Clone(r.UpstreamStatuses)
	if statuses == nil {
		statuses = map[string]any{}
	}
	statuses["-"] = upstreamStatuses
	r.UpstreamStatuses = statuses
	if rs != nil {
		rs.UpstreamStatuses = statuses
	}
}

func (r *MCPResult) addRemoteGotos(gotos map[string]bool) {
	if r.RemoteGotos == nil {
		r.RemoteGotos = map[string]bool{}
	}
	for v := range gotos {
		r.RemoteGotos[v] = true
	}
}

func (r *MCPResult) storeRemoteGotos(gotos map[string]bool) {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.addRemoteGotos(gotos)
}

func (r *MCPResult) setLastError(err error) {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.LastError = err
}

func (r *MCPResult) lastError() error {
	r.lock.Lock()
	defer r.lock.Unlock()
	return r.LastError
}

func (r *MCPResult) clearCallResults() {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.CallResults = nil
}

// MarshalJSON encodes the result while holding its lock, since the responses of calls may still be stored.
func (r *MCPResult) MarshalJSON() ([]byte, error) {
	type plainResult MCPResult
	r.lock.Lock()
	defer r.lock.Unlock()
	return json.Marshal((*plainResult)(r))
}

func (r *MCPResult) ToMCP() *gomcp.CallToolResult {
	r.lock.Lock()
	defer r.lock.Unlock()
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
	r.Timeline.UpstreamHeaders[fmt.Sprintf("%s (MCP)", r.Tool)] = map[string]any{
		"RequestHeaders":  r.LastRequestHeaders,
		"ResponseHeaders": r.LastResponseHeaders,
	}
	r.Timeline.UpstreamStatuses = r.UpstreamStatuses
	r.Timeline.Status = r.LastResponseStatus
	result.StructuredContent = r.Timeline
	return result
}

func (r *MCPResult) ToObject() map[string]any {
	r.lock.Lock()
	defer r.lock.Unlock()
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
