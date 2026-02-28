/**
 * Copyright 2025 uk
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

type JSONRPCMessage struct {
	ID      string `json:"id,omitempty"`
	JSONRPC string `json:"jsonrpc,omitempty"`
}

type JSONRPCRequest struct {
	JSONRPCMessage
	Method string         `json:"method"`
	Params map[string]any `json:"params,omitempty"`
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

func NewJSONRPCRequest(id, method string, params map[string]any) *JSONRPCRequest {
	return &JSONRPCRequest{
		JSONRPCMessage: JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      id,
		},
		Method: method,
		Params: params,
	}
}

func NewJSONRPCResponse(id string, result map[string]any) *JSONRPCResponse {
	return &JSONRPCResponse{
		JSONRPCMessage: JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      id,
		},
		Result: result,
	}
}

func NewJSONRPCError(id string, code int, message string, data any) *JSONRPCResponse {
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
