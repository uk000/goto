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

package jsonrpc

import (
	"goto/pkg/util"
	"io"
)

const (
	METHOD_INITIALIZE = "initialize"
)

type MCPMethods struct {
	Initialize            bool
	Initialized           bool
	Cancelled             bool
	Progress              bool
	Ping                  bool
	ToolsList             bool
	ToolsListChanged      bool
	ToolsCall             bool
	ResourcesList         bool
	ResourcesRead         bool
	ResourcesUpdated      bool
	ResourcesSubscribe    bool
	ResourcesUnsubscribe  bool
	ResourcesListChanged  bool
	ResourceTemplatesList bool
	PromptsList           bool
	PromptsGet            bool
	PromptsListChanged    bool
	RootsList             bool
	RootsListChanged      bool
	LoggingSetLevel       bool
	LoggingMessage        bool
}

type JSONRPCMessage struct {
	ID      int    `json:"id,omitempty"`
	JSONRPC string `json:"jsonrpc,omitempty"`
}

type JSONRPCRequest struct {
	JSONRPCMessage
	Method    string         `json:"method"`
	Params    map[string]any `json:"params,omitempty"`
	MCPMethod *MCPMethods    `json:"-"`
}

type JSONRPCResponse struct {
	JSONRPCMessage
	Result map[string]any `json:"result,omitempty"`
	Error  *JSONRPCError  `json:"error,omitempty"`
}

type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func NewJSONRPCRequest(id int, method string, params map[string]any) *JSONRPCRequest {
	return &JSONRPCRequest{
		JSONRPCMessage: JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      id,
		},
		Method: method,
		Params: params,
	}
}

func NewJSONRPCResponse(id int, result map[string]any) *JSONRPCResponse {
	return &JSONRPCResponse{
		JSONRPCMessage: JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      id,
		},
		Result: result,
	}
}

func NewJSONRPCError(id int, code int, message string, data any) *JSONRPCResponse {
	return &JSONRPCResponse{
		JSONRPCMessage: JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      id,
		},
		Error: &JSONRPCError{
			Code:    code,
			Message: message,
			Data:    data,
		},
	}
}

func NewJSONRPCNotification(method string, params map[string]any) *JSONRPCRequest {
	return &JSONRPCRequest{
		JSONRPCMessage: JSONRPCMessage{
			JSONRPC: "2.0",
		},
		Method: method,
		Params: params,
	}
}

func ParseJSONRPCRequest(b io.Reader) (*JSONRPCRequest, error) {
	m := &JSONRPCRequest{
		MCPMethod: &MCPMethods{},
	}
	if err := util.ReadJsonPayloadFromBody(b, m); err != nil {
		return nil, err
	}
	switch m.Method {
	case "initialize":
		m.MCPMethod.Initialize = true
	case "initialized":
		m.MCPMethod.Initialized = true
	case "cancelled":
		m.MCPMethod.Cancelled = true
	case "progress":
		m.MCPMethod.Progress = true
	case "ping":
		m.MCPMethod.Ping = true
	case "tools/list":
		m.MCPMethod.ToolsList = true
	case "tools/list_changed":
		m.MCPMethod.ToolsListChanged = true
	case "tools/call":
		m.MCPMethod.ToolsCall = true
	case "resources/list":
		m.MCPMethod.ResourcesList = true
	case "resources/read":
		m.MCPMethod.ResourcesRead = true
	case "resources/updated":
		m.MCPMethod.ResourcesUpdated = true
	case "resources/subscribe":
		m.MCPMethod.ResourcesSubscribe = true
	case "resources/unsubscribe":
		m.MCPMethod.ResourcesUnsubscribe = true
	case "resources/list_changed":
		m.MCPMethod.ResourcesListChanged = true
	case "resources/templates/list":
		m.MCPMethod.ResourceTemplatesList = true
	case "prompts/list":
		m.MCPMethod.PromptsList = true
	case "prompts/get":
		m.MCPMethod.PromptsGet = true
	case "prompts/list_changed":
		m.MCPMethod.PromptsListChanged = true
	case "roots/list":
		m.MCPMethod.RootsList = true
	case "roots/list_changed":
		m.MCPMethod.RootsListChanged = true
	case "logging/setLevel":
		m.MCPMethod.LoggingSetLevel = true
	case "logging/message":
		m.MCPMethod.LoggingMessage = true
	}
	return m, nil
}
