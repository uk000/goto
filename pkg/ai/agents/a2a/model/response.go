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

package a2amodel

import "goto/pkg/rpc/jsonrpc"

// ErrorCode represents the error codes used in the A2A protocol
type ErrorCode int

const (
	ErrorCodeParseError                   ErrorCode = -32700
	ErrorCodeInvalidRequest               ErrorCode = -32600
	ErrorCodeMethodNotFound               ErrorCode = -32601
	ErrorCodeInvalidParams                ErrorCode = -32602
	ErrorCodeInternalError                ErrorCode = -32603
	ErrorCodeTaskNotFound                 ErrorCode = -32000
	ErrorCodeTaskNotCancelable            ErrorCode = -32001
	ErrorCodePushNotificationNotSupported ErrorCode = -32002
	ErrorCodeUnsupportedOperation         ErrorCode = -32003
)

// A2AError represents an error in the A2A protocol
type A2AError struct {
	jsonrpc.JSONRPCError
	Code ErrorCode `json:"code"`
}

// SendTaskResponse represents a response to a send task request
type SendTaskResponse struct {
	jsonrpc.JSONRPCResponse
	Result *Task     `json:"result,omitempty"`
	Error  *A2AError `json:"error,omitempty"`
}

// SendTaskStreamingResponse represents a response to a streaming task request
type SendTaskStreamingResponse struct {
	jsonrpc.JSONRPCResponse
	Result any       `json:"result,omitempty"` // Can be TaskStatusUpdateEvent or TaskArtifactUpdateEvent
	Error  *A2AError `json:"error,omitempty"`
}

// GetTaskResponse represents a response to a get task request
type GetTaskResponse struct {
	jsonrpc.JSONRPCResponse
	Result *Task     `json:"result,omitempty"`
	Error  *A2AError `json:"error,omitempty"`
}

// CancelTaskResponse represents a response to a cancel task request
type CancelTaskResponse struct {
	jsonrpc.JSONRPCResponse
	Result *Task     `json:"result,omitempty"`
	Error  *A2AError `json:"error,omitempty"`
}

// GetTaskHistoryResponse represents a response to a get task history request
type GetTaskHistoryResponse struct {
	jsonrpc.JSONRPCResponse
	Result *TaskHistory `json:"result,omitempty"`
	Error  *A2AError    `json:"error,omitempty"`
}

// SetTaskPushNotificationResponse represents a response to a set task push notification request
type SetTaskPushNotificationResponse struct {
	jsonrpc.JSONRPCResponse
	Result *PushNotificationConfig `json:"result,omitempty"`
	Error  *A2AError               `json:"error,omitempty"`
}

// GetTaskPushNotificationResponse represents a response to a get task push notification request
type GetTaskPushNotificationResponse struct {
	jsonrpc.JSONRPCResponse
	Result *PushNotificationConfig `json:"result,omitempty"`
	Error  *A2AError               `json:"error,omitempty"`
}
