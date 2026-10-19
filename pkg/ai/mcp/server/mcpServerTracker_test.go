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
	"testing"
	"time"
)

func trackedDuration(port int, server, tool string) int64 {
	trackLock.RLock()
	defer trackLock.RUnlock()
	return ToolCallDurationByPortServer[port][server][tool]
}

func TestTrackToolCallCountsByToolAndBySession(t *testing.T) {
	port := isolate(t)
	TrackToolCall(port, "track-s", "session-1", "echo")
	TrackToolCall(port, "track-s", "session-1", "echo")
	TrackToolCall(port, "track-s", "session-2", "status")
	for key, want := range map[string]int{"echo": 2, "status": 1, "missing": 0} {
		if got := getPortServerCount(port, "track-s", key, ToolCallsByPortServer); got != want {
			t.Errorf("calls of %s = %d, want %d", key, got, want)
		}
	}
	for key, want := range map[string]int{"session-1": 2, "session-2": 1} {
		if got := getPortServerCount(port, "track-s", key, ToolCallsByPortServerSession); got != want {
			t.Errorf("calls of %s = %d, want %d", key, got, want)
		}
	}
	// Another server or another port doesn't have them.
	if getPortServerCount(port, "other-server", "echo", ToolCallsByPortServer) != 0 || getPortServerCount(port+1, "track-s", "echo", ToolCallsByPortServer) != 0 {
		t.Error("calls of the server are counted for another one")
	}
}

func TestTrackToolCallResultCountsAndAveragesTheDuration(t *testing.T) {
	port := isolate(t)
	TrackToolCallResult(port, "track-r", "echo", 100*time.Millisecond, true)
	TrackToolCallResult(port, "track-r", "echo", 300*time.Millisecond, false)
	TrackToolCallResult(port, "track-r", "echo", 200*time.Millisecond, true)
	if got := getPortServerCount(port, "track-r", "echo", ToolCallSuccessByPortServer); got != 2 {
		t.Errorf("successes = %d, want 2", got)
	}
	if got := getPortServerCount(port, "track-r", "echo", ToolCallFailureByPortServer); got != 1 {
		t.Errorf("failures = %d, want 1", got)
	}
	if got := trackedDuration(port, "track-r", "echo"); got != 200 {
		t.Errorf("average duration = %dms, want 200ms", got)
	}
}

func TestTrackInitCallMakesTheServerKnown(t *testing.T) {
	port := isolate(t)
	TrackInitCall(port, "track-i", "client", "2025-06-18")
	trackLock.RLock()
	defer trackLock.RUnlock()
	if InitCallsByPortServer[port] == nil || InitCallsByPortServer[port]["track-i"] == nil {
		t.Errorf("init calls = %v", InitCallsByPortServer[port])
	}
}

func TestTrackingIsSafeForConcurrentCalls(t *testing.T) {
	port := isolate(t)
	const workers, rounds = 16, 50
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				TrackToolCall(port, "track-c", "session", "echo")
				TrackToolCallResult(port, "track-c", "echo", 10*time.Millisecond, i%2 == 0)
				getPortServerCount(port, "track-c", "echo", ToolCallsByPortServer)
				TrackInitCall(port, "track-c", "client", "v")
			}
		}(w)
	}
	waitFor(t, &wg, 30*time.Second)
	const total = workers * rounds
	if got := getPortServerCount(port, "track-c", "echo", ToolCallsByPortServer); got != total {
		t.Errorf("calls = %d, want %d", got, total)
	}
	successes := getPortServerCount(port, "track-c", "echo", ToolCallSuccessByPortServer)
	failures := getPortServerCount(port, "track-c", "echo", ToolCallFailureByPortServer)
	if successes != total/2 || failures != total/2 {
		t.Errorf("successes = %d, failures = %d, want %d of each", successes, failures, total/2)
	}
	if got := trackedDuration(port, "track-c", "echo"); got != 10 {
		t.Errorf("average duration = %dms, want 10ms for calls that all took 10ms", got)
	}
}
