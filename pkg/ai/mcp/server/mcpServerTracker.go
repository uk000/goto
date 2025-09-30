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

package mcpserver

import (
	"sync"
	"time"
)

type TrackerByPortServer[T any] map[int]map[string]map[string]T

var (
	InitCallsByPortServer        = TrackerByPortServer[map[string]int]{}
	ToolCallsByPortServer        = TrackerByPortServer[int]{}
	ToolCallsByPortServerSession = TrackerByPortServer[int]{}
	ToolCallSuccessByPortServer  = TrackerByPortServer[int]{}
	ToolCallFailureByPortServer  = TrackerByPortServer[int]{}
	ToolCallDurationByPortServer = TrackerByPortServer[int64]{}
	trackLock                    = sync.RWMutex{}
)

func trackPortServer[T any](port int, server string, m TrackerByPortServer[T]) map[string]T {
	trackLock.Lock()
	defer trackLock.Unlock()
	if m[port] == nil {
		m[port] = map[string]map[string]T{}
	}
	if m[port][server] == nil {
		m[port][server] = map[string]T{}
	}
	return m[port][server]
}

func trackAndIncrementPortServer[T int](port int, server, key string, m TrackerByPortServer[T]) {
	trackLock.Lock()
	defer trackLock.Unlock()
	if m[port] == nil {
		m[port] = map[string]map[string]T{}
	}
	if m[port][server] == nil {
		m[port][server] = map[string]T{}
	}
	m[port][server][key]++
}

func getPortServerCount(port int, server, key string, m TrackerByPortServer[int]) int {
	trackLock.RLock()
	defer trackLock.RUnlock()
	if m[port] != nil && m[port][server] != nil {
		return m[port][server][key]
	}
	return 0
}

func TrackInitCall(port int, server, client, protocol string) {
	trackPortServer(port, server, InitCallsByPortServer)
}

func TrackToolCall(port int, server, sessionID, tool string) {
	trackAndIncrementPortServer(port, server, tool, ToolCallsByPortServer)
	trackAndIncrementPortServer(port, server, sessionID, ToolCallsByPortServerSession)
}

func TrackToolCallResult(port int, server, tool string, duration time.Duration, success bool) {
	if success {
		trackAndIncrementPortServer(port, server, tool, ToolCallSuccessByPortServer)
	} else {
		trackAndIncrementPortServer(port, server, tool, ToolCallFailureByPortServer)
	}
	dmap := trackPortServer(port, server, ToolCallDurationByPortServer)
	successCount := getPortServerCount(port, server, tool, ToolCallSuccessByPortServer)
	failureCount := getPortServerCount(port, server, tool, ToolCallFailureByPortServer)
	total := successCount + failureCount
	trackLock.Lock()
	dmap[tool] = (dmap[tool]*int64(total-1) + duration.Milliseconds()) / int64(total)
	trackLock.Unlock()
}
