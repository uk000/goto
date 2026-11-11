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
	"errors"
	"sync"
	"testing"
	"time"
)

// newStatusTestTracker returns a tracker with just what recording the status of requests needs.
func newStatusTestTracker(name string) *InvocationTracker {
	tracker := &InvocationTracker{
		ID:     1,
		Target: &InvocationSpec{Name: name, URL: "http://127.0.0.1:1/", Replicas: 1, RequestCount: 1},
		Channels: &InvocationChannels{
			StopChannel:   make(chan bool, 20),
			DoneChannel:   make(chan bool, 2),
			ResultChannel: make(chan *InvocationResult, 200),
		},
	}
	tracker.Status = &InvocationStatus{tracker: tracker, lastStatusCode: -1}
	return tracker
}

func newStatusTestResult(tracker *InvocationTracker, statusCode int, err error) *InvocationResult {
	return &InvocationResult{
		Request:  &InvocationResultRequest{ID: "r"},
		Response: &InvocationResultResponse{StatusCode: statusCode},
		tracker:  tracker,
		err:      err,
	}
}

// completesWithin fails the test if f doesn't return in time, e.g. when it's stuck waiting for a lock that it holds itself.
func completesWithin(t *testing.T, timeout time.Duration, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatalf("%s didn't complete in %s: deadlocked", what, timeout)
	}
}

// The status lock isn't reentrant: recording a response whose status code differs from a repeated one used to report the
// repetition through code that takes the lock again, which blocked the invocation, and the requests of the target, forever.
func TestTrackStatusWhenTheStatusCodeChangesAfterARepeat(t *testing.T) {
	tracker := newStatusTestTracker("status-flip")
	status := tracker.Status
	completesWithin(t, 5*time.Second, "recording 200, 200, 500", func() {
		for _, code := range []int{200, 200, 500} {
			status.trackStatus(newStatusTestResult(tracker, code, nil))
		}
	})
	status.lock.RLock()
	defer status.lock.RUnlock()
	if status.SuccessCount != 3 || status.lastStatusCode != 500 || status.lastStatusCount != 1 {
		t.Errorf("status = {success: %d, code: %d, count: %d}, want {3, 500, 1}", status.SuccessCount, status.lastStatusCode, status.lastStatusCount)
	}
}

func TestTrackStatusAfterRepeatedErrors(t *testing.T) {
	tracker := newStatusTestTracker("status-after-errors")
	status := tracker.Status
	completesWithin(t, 5*time.Second, "recording 2 errors and a response", func() {
		for i := 0; i < 2; i++ {
			tracker.processError(newStatusTestResult(tracker, 0, errors.New("connection refused")))
		}
		status.trackStatus(newStatusTestResult(tracker, 200, nil))
	})
	status.lock.RLock()
	defer status.lock.RUnlock()
	if status.FailureCount != 2 || status.SuccessCount != 1 || status.lastError != "" || status.lastErrorCount != 0 || status.lastStatusCode != 200 || status.lastStatusCount != 1 {
		t.Errorf("status = %+v, want 2 failures, 1 success, no pending error and 200 as the last status", status)
	}
}

func TestProcessErrorAfterRepeatedResponses(t *testing.T) {
	tracker := newStatusTestTracker("error-after-repeats")
	status := tracker.Status
	completesWithin(t, 5*time.Second, "recording 3 responses and an error", func() {
		for i := 0; i < 3; i++ {
			tracker.processResponse(newStatusTestResult(tracker, 200, nil))
		}
		tracker.processError(newStatusTestResult(tracker, 0, errors.New("connection reset")))
	})
	status.lock.RLock()
	defer status.lock.RUnlock()
	if status.SuccessCount != 3 || status.FailureCount != 1 || status.lastStatusCount != 0 || status.lastErrorCount != 1 || status.lastError != "connection reset" {
		t.Errorf("status = %+v, want 3 successes and 1 failure, the repeated status reset and the error pending", status)
	}
}

func TestReportRepeatedResponseResetsTheRepeatCounters(t *testing.T) {
	tracker := newStatusTestTracker("report-repeats")
	status := tracker.Status
	status.lastStatusCode, status.lastStatusCount = 200, 4
	status.lastError, status.lastErrorCount = "timeout", 3
	completesWithin(t, 5*time.Second, "reporting the repeats", status.reportRepeatedResponse)
	if status.lastStatusCount != 0 || status.lastStatusCode != -1 || status.lastErrorCount != 0 || status.lastError != "" {
		t.Errorf("status = %+v, want the repeats reported and reset", status)
	}
	// nothing is pending now, so nothing changes and nothing blocks
	completesWithin(t, 5*time.Second, "reporting again", status.reportRepeatedResponse)
	if status.lastStatusCode != -1 {
		t.Errorf("last status code = %d, want -1 still", status.lastStatusCode)
	}
}

func TestSendResultAfterTheChannelsAreClosed(t *testing.T) {
	tracker := newStatusTestTracker("send-after-close")
	tracker.CloseChannels()
	completesWithin(t, 5*time.Second, "sending a result after the channels were closed", func() {
		tracker.SendResult(newStatusTestResult(tracker, 200, nil))
	})
}

func TestStatusIsRecordedConcurrentlyWithoutDeadlock(t *testing.T) {
	const workers, rounds = 8, 200
	tracker := newStatusTestTracker("status-race")
	status := tracker.Status
	start := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			for i := 0; i < rounds; i++ {
				switch (w + i) % 5 {
				case 0:
					tracker.processError(newStatusTestResult(tracker, 0, errors.New("failed")))
				case 1:
					status.reportRepeatedResponse()
				case 2:
					tracker.reportRepeatedResponse()
				default:
					// a few status codes, so that responses repeat and also change
					tracker.processResponse(newStatusTestResult(tracker, 200+(i/3)%3, nil))
				}
			}
		}(w)
	}
	close(start)
	completesWithin(t, 30*time.Second, "recording requests from several goroutines", wg.Wait)
	status.lock.RLock()
	defer status.lock.RUnlock()
	failures, successes := 0, 0
	for w := 0; w < workers; w++ {
		for i := 0; i < rounds; i++ {
			switch (w + i) % 5 {
			case 0:
				failures++
			case 1, 2:
			default:
				successes++
			}
		}
	}
	if status.FailureCount != failures || status.SuccessCount != successes {
		t.Errorf("status = {failures: %d, successes: %d}, want {%d, %d}", status.FailureCount, status.SuccessCount, failures, successes)
	}
}
