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
	"fmt"
	"goto/pkg/util/timeline"
	"net/http"
)

type A2AResult struct {
	ID                  string
	Server              string
	Agent               string
	Timeline            *timeline.Timeline
	LastRequestHeaders  http.Header `json:"-"`
	LastResponseHeaders http.Header `json:"-"`
	CallResults         map[string]*A2ACallResult
}

type A2ACallResult struct {
	RequestID       string
	Content         []string       `json:"Content,omitempty"`
	Data            map[string]any `json:"Data,omitempty"`
	RemoteTimeline  any            `json:"RemoteTimeline,omitempty"`
	RequestHeaders  http.Header    `json:"RequestHeaders,omitempty"`
	ResponseHeaders http.Header    `json:"ResponseHeaders,omitempty"`
}

func NewA2AResult(server, agent string, t *timeline.Timeline) *A2AResult {
	return &A2AResult{
		ID:          fmt.Sprintf("[%s]@%s", agent, server),
		Server:      server,
		Agent:       agent,
		CallResults: map[string]*A2ACallResult{},
		Timeline:    t,
	}
}

func (r *A2AResult) getOrAddCall(requestID string) *A2ACallResult {
	result := r.CallResults[requestID]
	if result == nil {
		result = &A2ACallResult{
			RequestID: requestID,
			Data:      map[string]any{},
		}
		r.CallResults[requestID] = result
	}
	return result
}

func (r *A2AResult) addOrUpdateCall(requestID string, result *A2ACallResult) *A2ACallResult {
	if result == nil {
		result = r.getOrAddCall(requestID)
	} else {
		existing := r.CallResults[requestID]
		if existing == nil {
			r.CallResults[requestID] = result
		} else {
			existing.merge(result)
			result = existing
		}
	}
	return result
}

func (r *A2AResult) storeA2ACallResult(requestID string, result *A2ACallResult) {
	if result != nil {
		result = r.addOrUpdateCall(requestID, result)
	}
}

func (r *A2AResult) storeHeaders(requestID string, requestHeaders, responseHeaders http.Header) {
	cr := r.getOrAddCall(requestID)
	cr.RequestHeaders = requestHeaders
	cr.ResponseHeaders = responseHeaders
	r.LastRequestHeaders = requestHeaders
	r.LastResponseHeaders = responseHeaders
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

func (r *A2AResult) ToObject() map[string]any {
	result := map[string]any{}
	if len(r.CallResults) > 0 {
		r.Timeline.Data["A2ACalls"] = r.buildCallsData()
		content := []string{}
		for _, cr := range r.CallResults {
			content = append(content, cr.Content...)
		}
		result["Content"] = content
	}
	result["Timeline"] = r.Timeline
	return result
}

func (r *A2AResult) buildCallsData() map[string]map[string]any {
	callsData := map[string]map[string]any{}
	callsData[r.ID] = map[string]any{}
	for _, cr := range r.CallResults {
		callsData[r.ID][cr.RequestID] = cr
	}
	return callsData
}
