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

// TaskState represents the state of a task within the A2A protocol
type TaskState string

const (
	TaskStateSubmitted     TaskState = "submitted"
	TaskStateWorking       TaskState = "working"
	TaskStateInputRequired TaskState = "input-required"
	TaskStateCompleted     TaskState = "completed"
	TaskStateCanceled      TaskState = "canceled"
	TaskStateFailed        TaskState = "failed"
	TaskStateRejected      TaskState = "rejected"
	TaskStateAuthRequired  TaskState = "auth-required"
	TaskStateUnknown       TaskState = "unknown"
)

type Task struct {
	Kind      string      `json:"kind"`
	ID        string      `json:"id"`
	ContextId string      `json:"contextId"`
	Status    *TaskStatus `json:"status"`
	Artifacts []*Artifact `json:"artifacts"`
	History   []*Message  `json:"history"`
	Metadata  AnyMap      `json:"metadata"`
}

type TaskStatus struct {
	State     TaskState `json:"state"`
	Message   *Message  `json:"message"`
	Timestamp string    `json:"timestamp"`
}

// TaskHistory represents the history of a task
type TaskHistory struct {
	// MessageHistory is the list of messages in chronological order
	MessageHistory []Message `json:"messageHistory,omitempty"`
}

// TaskStatusUpdateEvent represents an event for task status updates
type TaskStatusUpdateEvent struct {
	// ID is the ID of the task being updated
	ID string `json:"id"`
	// Status is the new status of the task
	Status TaskStatus `json:"status"`
	// Final indicates if this is the final update for the task
	Final *bool `json:"final,omitempty"`
	// Metadata is optional metadata associated with this update event
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// TaskArtifactUpdateEvent represents an event for task artifact updates
type TaskArtifactUpdateEvent struct {
	// ID is the ID of the task being updated
	ID string `json:"id"`
	// Artifact is the new or updated artifact for the task
	Artifact Artifact `json:"artifact"`
	// Final indicates if this is the final update for the task
	Final *bool `json:"final,omitempty"`
	// Metadata is optional metadata associated with this update event
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}
