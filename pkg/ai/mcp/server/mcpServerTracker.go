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
	return trackPortServerLocked(port, server, m)
}

func trackPortServerLocked[T any](port int, server string, m TrackerByPortServer[T]) map[string]T {
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
	trackPortServerLocked(port, server, m)[key]++
}

func getPortServerCount(port int, server, key string, m TrackerByPortServer[int]) int {
	trackLock.RLock()
	defer trackLock.RUnlock()
	return getPortServerCountLocked(port, server, key, m)
}

func getPortServerCountLocked(port int, server, key string, m TrackerByPortServer[int]) int {
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

// TrackToolCallResult counts the result of a call, and keeps the average duration of the calls of the tool. It is done
// in one go, so the average is of the calls that were counted, whatever other calls finish at the same time.
func TrackToolCallResult(port int, server, tool string, duration time.Duration, success bool) {
	trackLock.Lock()
	defer trackLock.Unlock()
	if success {
		trackPortServerLocked(port, server, ToolCallSuccessByPortServer)[tool]++
	} else {
		trackPortServerLocked(port, server, ToolCallFailureByPortServer)[tool]++
	}
	dmap := trackPortServerLocked(port, server, ToolCallDurationByPortServer)
	total := getPortServerCountLocked(port, server, tool, ToolCallSuccessByPortServer) +
		getPortServerCountLocked(port, server, tool, ToolCallFailureByPortServer)
	dmap[tool] = (dmap[tool]*int64(total-1) + duration.Milliseconds()) / int64(total)
}
