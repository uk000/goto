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

package invocation

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newActiveTestTracker returns a tracker that is registered as an active invocation of the target, the way an invocation
// that is in progress is. The target is long running so that finishing the invocation doesn't need a transport client.
func newActiveTestTracker(t *testing.T, target string) *InvocationTracker {
	t.Helper()
	tracker := newStatusTestTracker(target)
	tracker.ID = atomic.AddUint32(&invocationCounter, 1)
	tracker.Target.LongRunning = true
	tracker.activate()
	t.Cleanup(tracker.deactivate)
	return tracker
}

func TestStopTargetSendsStopRequestsToTheActiveInvocations(t *testing.T) {
	name := "stop-target-sends"
	first, second := newActiveTestTracker(t, name), newActiveTestTracker(t, name)
	other := newActiveTestTracker(t, "stop-target-other")
	// the invocation that is already stopping isn't asked to stop again
	second.Status.StopRequested = true

	completesWithin(t, 5*time.Second, "stopping the target", func() { StopTarget(name) })

	if n := len(first.Channels.StopChannel); n != 1 {
		t.Errorf("stop requests pending for the active invocation = %d, want 1", n)
	}
	if n := len(second.Channels.StopChannel); n != 0 {
		t.Errorf("stop requests pending for the invocation that is stopping = %d, want 0", n)
	}
	if n := len(other.Channels.StopChannel); n != 0 {
		t.Errorf("stop requests pending for the invocation of another target = %d, want 0", n)
	}
}

// An invocation closes its channels before it's removed from the active ones, so stopping a target can run into an invocation
// that has no channels left. Sending to its stop channel used to block, holding the lock of the target's invocations that the
// finishing invocation needs to get removed, or to panic when the channel had just been closed.
func TestStopTargetWhenTheChannelsOfAnInvocationAreClosed(t *testing.T) {
	name := "stop-target-closed"
	tracker := newActiveTestTracker(t, name)
	tracker.CloseChannels()

	completesWithin(t, 5*time.Second, "stopping the target", func() { StopTarget(name) })
	completesWithin(t, 5*time.Second, "finishing the invocation", tracker.deactivate)

	if IsAnyTargetActive([]string{name}) {
		t.Errorf("target %s is still active after its only invocation was deactivated", name)
	}
}

func TestStopTargetWhileTheInvocationsFinish(t *testing.T) {
	const rounds, stoppers = 300, 4
	for i := 0; i < rounds; i++ {
		name := fmt.Sprintf("stop-target-race-%d", i)
		trackers := []*InvocationTracker{newActiveTestTracker(t, name), newActiveTestTracker(t, name)}
		start := make(chan struct{})
		var wg sync.WaitGroup
		for s := 0; s < stoppers; s++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				StopTarget(name)
			}()
		}
		for _, tracker := range trackers {
			wg.Add(1)
			go func(tracker *InvocationTracker) {
				defer wg.Done()
				<-start
				tracker.deactivate()
			}(tracker)
		}
		close(start)
		completesWithin(t, 30*time.Second, "round "+name, wg.Wait)
		if IsAnyTargetActive([]string{name}) {
			t.Fatalf("target %s is still active after all its invocations finished", name)
		}
	}
}

func TestStopRequestOnChannelsThatAreClosed(t *testing.T) {
	tracker := newStatusTestTracker("stop-closed")
	tracker.CloseChannels()
	completesWithin(t, 5*time.Second, "stopping an invocation that has closed its channels", tracker.Stop)
	completesWithin(t, 5*time.Second, "stopping again", tracker.Channels.Stop)
}

// Stopping holds the lock of the channels, which the invocation needs to finish, so it can't wait for room in the stop channel.
func TestStopRequestWhenTheStopChannelIsFull(t *testing.T) {
	tracker := newStatusTestTracker("stop-full")
	tracker.Target.LongRunning = true
	capacity := cap(tracker.Channels.StopChannel)
	completesWithin(t, 5*time.Second, "stopping more times than the stop channel holds", func() {
		for i := 0; i < capacity+5; i++ {
			tracker.Stop()
		}
	})
	if n := len(tracker.Channels.StopChannel); n != capacity {
		t.Errorf("stop requests pending = %d, want %d", n, capacity)
	}
	completesWithin(t, 5*time.Second, "finishing the invocation after the stop requests", tracker.deactivate)
}

func TestStopRequestOnAnInvocationWithoutChannels(t *testing.T) {
	tracker := &InvocationTracker{}
	tracker.Stop()
	tracker.CloseChannels()
}
