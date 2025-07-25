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

// TaskIDParams represents the base parameters for task ID-based operations
type TaskMetadata struct {
	// ID is the unique identifier for the task being initiated or continued
	ID string `json:"id"`
	// Metadata is optional metadata to include with the operation
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// TaskQueryParams represents the parameters for querying task information
type TaskQueryParams struct {
	TaskMetadata
	// HistoryLength is an optional parameter to specify how much history to retrieve
	HistoryLength *int `json:"historyLength,omitempty"`
}

// TaskSendParams represents the parameters for sending a task message
type TaskSendParams struct {
	TaskMetadata
	// SessionID is an optional identifier for the session this task belongs to
	SessionID *string `json:"sessionId,omitempty"`
	// Message is the message content to send to the agent for processing
	Message Message `json:"message"`
	// PushNotification is optional push notification information for receiving notifications
	PushNotification *PushNotificationConfig `json:"pushNotification,omitempty"`
}

type TaskPushNotificationConfig struct {
	ID                     string                 `json:"id"`
	PushNotificationConfig PushNotificationConfig `json:"pushNotificationConfig"`
}

type PushNotificationConfig struct {
	ID             *string                             `json:"id,omitempty"`
	URL            string                              `json:"url"`
	Token          *string                             `json:"token,omitempty"`
	Authentication *PushNotificationAuthenticationInfo `json:"authentication,omitempty"`
}

type PushNotificationAuthenticationInfo struct {
	Schemes     []string `json:"schemes"`
	Credentials *string  `json:"credentials,omitempty"`
}

type TaskRequest struct {
	jsonrpc.JSONRPCRequest
	Params TaskSendParams `json:"params"`
}

type TaskStatusRequest struct {
	jsonrpc.JSONRPCRequest
	Params TaskQueryParams `json:"params"`
}

type CancelTaskRequest struct {
	jsonrpc.JSONRPCRequest
}

type TaskPushNotificationRequest struct {
	jsonrpc.JSONRPCRequest
	Params TaskPushNotificationConfig `json:"params"`
}

type CheckTaskPushNotificationRequest struct {
	jsonrpc.JSONRPCRequest
}

type TaskResubscriptionRequest struct {
	jsonrpc.JSONRPCRequest
	Params TaskQueryParams `json:"params"`
}

type SendTaskStreamingRequest struct {
	jsonrpc.JSONRPCRequest
	Params TaskSendParams `json:"params"`
}

type MessageSendParams struct {
	Message       Message                   `json:"message"`
	Configuration *MessageSendConfiguration `json:"configuration,omitempty"`
	Metadata      AnyMap                    `json:"metadata,omitempty"`
}

type MessageSendConfiguration struct {
	AcceptedOutputModes    []string                `json:"acceptedOutputModes"`
	HistoryLength          *int                    `json:"historyLength,omitempty"`
	PushNotificationConfig *PushNotificationConfig `json:"pushNotificationConfig,omitempty"`
	Blocking               *bool                   `json:"blocking,omitempty"`
}
