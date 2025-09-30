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

package invocation

import (
	"fmt"
	"goto/pkg/events"
	"sync/atomic"
	"time"
)

type RunnerId uint32

type InvocationIDs struct {
	targetID  string
	requestID string
}

type TargetRunner struct {
	id           RunnerId
	tracker      *InvocationTracker
	requestsChan chan *InvocationIDs
	doneChan     chan RunnerId
	stopChan     chan bool
}

var (
	NextRunnerId atomic.Uint32
)

func StartInvocation(tracker *InvocationTracker, waitForResponse ...bool) []*InvocationResult {
	tracker.activate()
	target := tracker.Target
	time.Sleep(target.initialDelayD)
	events.SendEventJSON(events.Client_InvocationStarted, fmt.Sprintf("%d-%s", tracker.ID, target.Name), target)
	tracker.logStartInvocation()

	doWarmupRounds(tracker)

	var results []*InvocationResult
	if len(waitForResponse) > 0 && waitForResponse[0] {
		tracker.AddSink(func(result *InvocationResult) {
			results = append(results, result)
		})
	}

	go processStopRequest(tracker)

	runners := make(map[RunnerId]*TargetRunner, target.Replicas)
	runnersDoneChan := make(chan RunnerId, target.Replicas)

	for i := 0; i < target.Replicas; i++ {
		id := RunnerId(NextRunnerId.Add(1))
		runners[id] = NewTargetRunner(id, tracker, runnersDoneChan)
		runners[id].sendInvocation(computeInvocationIDs(tracker, id))
		go runners[id].processInvocations()
	}
	for !tracker.Status.Stopped {
		if tracker.Status.StopRequested {
			tracker.Status.Stopped = true
			break
		}
		availableRunner := <-runnersDoneChan
		tracker.Status.CompletedRequests++
		if tracker.Status.AssignedRequests < (target.RequestCount * target.Replicas) {
			runners[availableRunner].sendInvocation(computeInvocationIDs(tracker, availableRunner))
		} else if tracker.Status.CompletedRequests >= (target.RequestCount * target.Replicas) {
			break
		}
	}
	tracker.deactivate()
	return results
}

func computeInvocationIDs(tracker *InvocationTracker, runnerId RunnerId) (targetID string, requestID string) {
	tracker.Status.AssignedRequests++
	if tracker.CustomID > 0 {
		targetID = fmt.Sprintf("%s-%d", tracker.Target.Name, tracker.CustomID)
		requestID = fmt.Sprintf("%s-%d[%d][%d]", tracker.Target.Name, tracker.CustomID, runnerId, tracker.Status.AssignedRequests)
	} else {
		targetID = fmt.Sprintf("%s", tracker.Target.Name)
		requestID = fmt.Sprintf("%s[%d][%d]", tracker.Target.Name, runnerId, tracker.Status.AssignedRequests)
	}
	return
}

func invokeTarget(tracker *InvocationTracker, requestID string, targetID string, publish bool) {
	NewTargetRunner(0, tracker, nil).invoke(&InvocationIDs{requestID: requestID, targetID: targetID}, publish)
}

func NewTargetRunner(id RunnerId, tracker *InvocationTracker, doneChan chan RunnerId) *TargetRunner {
	return &TargetRunner{
		id:           id,
		tracker:      tracker,
		requestsChan: make(chan *InvocationIDs, 10),
		doneChan:     doneChan,
		stopChan:     make(chan bool, 10),
	}
}

func (t *TargetRunner) sendInvocation(requestID, targetID string) {
	t.requestsChan <- &InvocationIDs{requestID: requestID, targetID: targetID}
}

func (t *TargetRunner) processInvocations() {
	for !t.tracker.Status.Stopped && !t.tracker.Status.StopRequested {
		time.Sleep(t.tracker.Target.delayD)
		select {
		case i := <-t.requestsChan:
			t.invoke(i, true)
			if t.doneChan != nil {
				t.doneChan <- t.id
			}
		case <-t.stopChan:
			return
		}
	}
}

func (t *TargetRunner) invoke(i *InvocationIDs, publish bool) {
	target := t.tracker.Target
	if result := t.tracker.invokeWithRetries(i.requestID, i.targetID); result != nil {
		if !t.tracker.Status.StopRequested && !t.tracker.Status.Stopped {
			if publish && target.AB {
				handleABCall(t.tracker, i.requestID, i.targetID, publish)
			}
		}
		if publish && !t.tracker.Status.StopRequested && !t.tracker.Status.Stopped {
			t.tracker.publishResult(result)
		}
	}
}

func doWarmupRounds(tracker *InvocationTracker) {
	for i := 0; i < tracker.Target.WarmupCount; i++ {
		if tracker.Status.StopRequested {
			tracker.logStoppingWarmup(tracker.Target.WarmupCount - i)
			break
		}
		requestId := fmt.Sprintf("%s[Warmup][%d]", tracker.Target.Name, i+1)
		targetID := requestId
		invokeTarget(tracker, requestId, targetID, false)
	}
}

func handleABCall(tracker *InvocationTracker, requestID string, targetID string, publish bool) {
	for i, burl := range tracker.Target.BURLS {
		if tracker.Status.StopRequested || tracker.Status.Stopped {
			break
		}
		bRequestID := fmt.Sprintf("%s-B-%d", requestID, i+1)
		if result := tracker.invokeWithRetries(bRequestID, targetID, burl); result != nil {
			if publish && !tracker.Status.StopRequested && !tracker.Status.Stopped {
				tracker.publishResult(result)
				tracker.Status.incrementABCount()
			}
		}
	}
}
