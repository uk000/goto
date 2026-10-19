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

package a2aclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/util"
	"goto/pkg/util/timeline"
	"maps"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"weak"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type A2AResult struct {
	ID                  string
	Server              string
	Agent               string
	AgentCall           *AgentCall
	ServerInfo          *timeline.GotoServerInfo
	Timeline            *timeline.Timeline
	UpstreamStatuses    map[string]any
	LastRequestHeaders  http.Header `json:"-"`
	LastResponseHeaders http.Header `json:"-"`
	LastResponseStatus  int         `json:"-"`
	LastRemoteHeaders   map[string]any
	CallResults         map[string]*A2ACallResult
	RemoteGotos         map[string]bool
	// lock guards the fields that the requests of a call update, as they run concurrently.
	lock sync.Mutex
}

type A2ACallResult struct {
	RequestID       string
	Content         []string                 `json:"Content,omitempty"`
	Data            map[string]any           `json:"Data,omitempty"`
	ClientInfo      *timeline.GotoClientInfo `json:"-"`
	RemoteTimeline  *timeline.Timeline       `json:"RemoteTimeline,omitempty"`
	RemoteResult    any                      `json:"RemoteResult,omitempty"`
	RemoteHeaders   map[string]any           `json:"RemoteHeaders,omitempty"`
	RequestHeaders  http.Header              `json:"RequestHeaders,omitempty"`
	ResponseHeaders http.Header              `json:"ResponseHeaders,omitempty"`
	ResponseStatus  int                      `json:"ResponseStatus,omitempty"`
	parent          *A2AResult
}

func NewA2AResult(server string, ac *AgentCall, t *timeline.Timeline) *A2AResult {
	return &A2AResult{
		ID:               fmt.Sprintf("[%s]@%s", ac.Name, server),
		Server:           server,
		Agent:            ac.Name,
		AgentCall:        ac,
		ServerInfo:       t.Server,
		CallResults:      map[string]*A2ACallResult{},
		UpstreamStatuses: map[string]any{},
		Timeline:         t,
		RemoteGotos:      map[string]bool{},
	}
}

func (r *A2AResult) getOrAddCall(requestID string) *A2ACallResult {
	r.lock.Lock()
	defer r.lock.Unlock()
	return r.getOrAddCallLocked(requestID)
}

func (r *A2AResult) getOrAddCallLocked(requestID string) *A2ACallResult {
	result := r.CallResults[requestID]
	if result == nil {
		result = &A2ACallResult{
			RequestID: requestID,
			Data:      map[string]any{},
			parent:    r,
		}
		r.CallResults[requestID] = result
	}
	return result
}

func (r *A2AResult) addOrUpdateCall(requestID string, result *A2ACallResult, clientInfo *timeline.GotoClientInfo) *A2ACallResult {
	r.lock.Lock()
	defer r.lock.Unlock()
	if result == nil {
		result = r.getOrAddCallLocked(requestID)
	} else {
		existing := r.CallResults[requestID]
		if existing == nil {
			r.CallResults[requestID] = result
		} else {
			existing.merge(result)
			result = existing
		}
	}
	result.ClientInfo = clientInfo
	return result
}

func (r *A2AResult) storeA2ACallResult(requestID string, result *A2ACallResult, clientInfo *timeline.GotoClientInfo) {
	if result != nil {
		result = r.addOrUpdateCall(requestID, result, clientInfo)
	}
}

func (r *A2AResult) storeHeaders(requestID string, requestHeaders, responseHeaders http.Header, status int) {
	r.lock.Lock()
	defer r.lock.Unlock()
	cr := r.getOrAddCallLocked(requestID)
	cr.RequestHeaders = requestHeaders
	cr.ResponseHeaders = responseHeaders
	cr.ResponseStatus = status
	r.LastRequestHeaders = requestHeaders
	r.LastResponseHeaders = responseHeaders
	r.LastResponseStatus = status
	r.UpstreamStatuses[r.Agent] = status
}

// markRemoteGotos reports the remotes that took part in the responses on the last response headers, marking each of
// them as reached through A2A unless it says how it was reached.
func (r *A2AResult) markRemoteGotos() {
	r.lock.Lock()
	defer r.lock.Unlock()
	if r.LastResponseHeaders == nil {
		// There is no response when the request failed before it got one.
		r.LastResponseHeaders = http.Header{}
	}
	for _, v := range r.LastResponseHeaders[constants.HeaderViaGoto] {
		r.RemoteGotos[v] = true
	}
	viaGotos := []string{}
	for v := range r.RemoteGotos {
		if !strings.Contains(v, "(A2A)") && !strings.Contains(v, "(MCP)") {
			v = v + "(A2A)"
		}
		viaGotos = append(viaGotos, v)
	}
	r.LastResponseHeaders[constants.HeaderViaGoto] = viaGotos
}

func (r *A2AResult) addRemoteGotos(gotos ...string) {
	r.lock.Lock()
	defer r.lock.Unlock()
	for _, g := range gotos {
		r.RemoteGotos[g] = true
	}
}

func (r *A2AResult) addRemoteGotosFromUpstreamHeaders(headers map[string]any) {
	r.lock.Lock()
	defer r.lock.Unlock()
	// The gotos that the remote reported are only relevant once there is a response from the remote.
	if r.LastResponseHeaders != nil {
		for v := range util.GetViaGotosFromUpstreamHeaders(headers) {
			r.RemoteGotos[v] = true
		}
	}
}

func (r *A2AResult) setLastRemoteHeaders(headers map[string]any) {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.LastRemoteHeaders = headers
}

// recordUpstreamStatuses adds the upstream statuses reported by the response to the ones of the call. The request
// store gets a copy, as it is shared by the calls that are made for a request and read by the ones that report it.
func (r *A2AResult) recordUpstreamStatuses(rs *util.RequestStore, upstreamStatuses any) {
	rsLock.Lock()
	defer rsLock.Unlock()
	r.lock.Lock()
	defer r.lock.Unlock()
	statuses := maps.Clone(r.UpstreamStatuses)
	if statuses == nil {
		statuses = map[string]any{}
	}
	statuses["-"] = upstreamStatuses
	r.UpstreamStatuses = statuses
	rs.UpstreamStatuses = maps.Clone(statuses)
}

func (cr *A2ACallResult) merge(other *A2ACallResult) {
	cr.Content = append(cr.Content, other.Content...)
	if other.Data != nil {
		cr.Data = other.Data
	}
	if len(other.RequestHeaders) > 0 {
		cr.RequestHeaders = other.RequestHeaders
	}
	if len(other.ResponseHeaders) > 0 {
		cr.ResponseHeaders = other.ResponseHeaders
	}
	if other.RemoteTimeline != nil {
		cr.RemoteTimeline = other.RemoteTimeline
	}
}

func (cr *A2ACallResult) storeRemoteTimeline(data any) {
	t := timeline.CheckAndGetTimeline(data)
	if t != nil {
		if cr.parent.AgentCall.ResultOnly {
			t.Events = nil
		}
		cr.RemoteTimeline = t
	}
}

// timelineLocks has the lock of each timeline that is shared with the results of calls. The results of the calls that an
// agent makes in parallel are all made with the timeline of the agent, and the timeline has no lock of its own for what
// a result reads and sets. The lock is of the timeline, so that the calls of one agent don't wait for the calls of the
// others, and it is forgotten with the timeline.
var timelineLocks sync.Map

func lockOfTimeline(t *timeline.Timeline) *sync.Mutex {
	key := weak.Make(t)
	if lock, ok := timelineLocks.Load(key); ok {
		return lock.(*sync.Mutex)
	}
	lock, loaded := timelineLocks.LoadOrStore(key, &sync.Mutex{})
	if !loaded {
		runtime.AddCleanup(t, func(key weak.Pointer[timeline.Timeline]) { timelineLocks.Delete(key) }, key)
	}
	return lock.(*sync.Mutex)
}

// UpdateTimeline runs what is done to a timeline that is shared with the results of calls, in step with what the results
// do to it.
func UpdateTimeline(t *timeline.Timeline, update func()) {
	if t == nil {
		update()
		return
	}
	lock := lockOfTimeline(t)
	lock.Lock()
	defer lock.Unlock()
	update()
}

// ToObject reports the result with the timeline that it was made with. The timeline is the live one, which goes on to
// change if it is shared. Snapshot is for what is to be handed over to others.
func (r *A2AResult) ToObject() (object map[string]any) {
	UpdateTimeline(r.Timeline, func() { object = r.toObject() })
	return
}

// Snapshot is what ToObject reports, taken as it is now: it has none of the objects that are changed after it is
// taken, so it can be marshaled later, on another goroutine, while the timeline goes on to change.
func (r *A2AResult) Snapshot() (snapshot map[string]any) {
	UpdateTimeline(r.Timeline, func() {
		object := r.toObject()
		snapshot = object
		b, err := json.Marshal(object)
		if err != nil {
			return
		}
		copied := map[string]any{}
		decoder := json.NewDecoder(bytes.NewReader(b))
		decoder.UseNumber()
		if err := decoder.Decode(&copied); err == nil {
			snapshot = copied
		}
	})
	return
}

func (r *A2AResult) toObject() map[string]any {
	result := map[string]any{}
	if len(r.CallResults) > 0 {
		r.Timeline.Data["A2ACalls"] = r.buildCallsData()
		if !r.AgentCall.ResultOnly {
			content := []string{}
			for _, cr := range r.CallResults {
				content = append(content, cr.Content...)
			}
			result["Content"] = content
		}
	}
	if r.AgentCall.ResultOnly {
		r.Timeline.Events = nil
	}
	result["Timeline"] = r.Timeline
	return result
}

func (r *A2AResult) ToMCP(extras map[string]any) (result *gomcp.CallToolResult) {
	UpdateTimeline(r.Timeline, func() { result = r.toMCP(extras) })
	return
}

func (r *A2AResult) toMCP(extras map[string]any) *gomcp.CallToolResult {
	result := &gomcp.CallToolResult{}
	if !r.AgentCall.NoCallDetails {
		if len(r.CallResults) > 0 {
			r.Timeline.Data["A2ACalls"] = r.buildCallsData()
			if !r.AgentCall.ResultOnly {
				for _, cr := range r.CallResults {
					for _, text := range cr.Content {
						result.Content = append(result.Content, &gomcp.TextContent{Text: text})
					}
				}
			}
		}
	}
	if r.AgentCall.NoEvents {
		r.Timeline.Events = nil
	}
	if r.LastRemoteHeaders != nil {
		for k, v := range r.LastRemoteHeaders {
			r.Timeline.UpstreamHeaders[k] = v
		}
	}
	r.Timeline.UpstreamHeaders[fmt.Sprintf("%s (A2A)", r.Agent)] = map[string]any{
		"RequestHeaders":  r.LastRequestHeaders,
		"ResponseHeaders": r.LastResponseHeaders,
	}
	for k, v := range extras {
		r.Timeline.Data[k] = v
	}
	r.Timeline.UpstreamStatuses = r.UpstreamStatuses
	r.Timeline.Status = r.LastResponseStatus
	result.StructuredContent = r.Timeline
	return result
}

func (r *A2AResult) buildCallsData() map[string]map[string]any {
	callsData := map[string]map[string]any{}
	callsData[r.ID] = map[string]any{}
	for _, cr := range r.CallResults {
		if r.AgentCall.ResultOnly {
			cr.Content = nil
			if cr.RemoteTimeline != nil {
				cr.RemoteTimeline.Events = nil

			}
		}
		callsData[r.ID][cr.RequestID] = cr
		if cr.RemoteTimeline != nil {
			r.Timeline.AddRemoteCall(r.ID, cr.RequestID, cr.RemoteTimeline)
		} else {
			r.Timeline.AddRemoteCall(r.ID, cr.RequestID, cr.RemoteResult)
		}
	}
	return callsData
}
