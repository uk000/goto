package invocation

import (
  "fmt"
  "goto/pkg/events"
  . "goto/pkg/events/eventslist"
  "sync"
  "time"
)

// type InvocationLog struct {
//   Host       string
//   Invocation uint32
//   TargetName string
//   TargetID   string
//   RequestID  string
//   URL        string
//   RequestHeaders      map[string]string
//   ResponseHeaders     map[string][]string
//   Result     *InvocationResult
// }

func StartInvocation(tracker *InvocationTracker, waitForResponse ...bool) []*InvocationResult {
  tracker.activate()
  target := tracker.Target
  completedCount := 0
  time.Sleep(target.initialDelayD)
  events.SendEventJSON(Client_InvocationStarted, fmt.Sprintf("%d-%s", tracker.ID, target.Name), target)
  tracker.logStartInvocation()
  doWarmupRounds(tracker)
  var results []*InvocationResult
  if len(waitForResponse) > 0 && waitForResponse[0] {
    tracker.AddSink(func(result *InvocationResult) {
      results = append(results, result)
    })
  }
  go processStopRequest(tracker)
  for !tracker.Status.Stopped {
    if tracker.Status.StopRequested {
      tracker.Status.Stopped = true
      tracker.deactivate()
      break
    }
    wg := &sync.WaitGroup{}
    for i := 0; i < target.Replicas; i++ {
      requestID := fmt.Sprintf("%s-%d", target.Name, completedCount+i+1)
      targetID := fmt.Sprintf("%s[%d][%d]", target.Name, completedCount+i+1, i+1)
      wg.Add(1)
      go invokeTarget(tracker, requestID, targetID, wg, true)
    }
    wg.Wait()
    delay := 10 * time.Millisecond
    if target.delayD > delay {
      delay = target.delayD
    }
    completedCount += target.Replicas
    tracker.Status.CompletedRequests = completedCount
    if completedCount < (target.RequestCount * target.Replicas) {
      time.Sleep(delay)
    } else {
      break
    }
  }
  tracker.deactivate()
  return results
}

func doWarmupRounds(tracker *InvocationTracker) {
  for i := 0; i < tracker.Target.WarmupCount; i++ {
    if tracker.Status.StopRequested {
      tracker.logStoppingWarmup(tracker.Target.WarmupCount - i)
      break
    }
    requestId := fmt.Sprintf("%s[Warmup][%d]", tracker.Target.Name, i+1)
    targetID := requestId
    invokeTarget(tracker, requestId, targetID, nil, false)
  }
}

func invokeTarget(tracker *InvocationTracker, requestID string, targetID string, wg *sync.WaitGroup, publish bool) {
  target := tracker.Target
  if result := tracker.invokeWithRetries(requestID, targetID); result != nil {
    if !tracker.Status.StopRequested && !tracker.Status.Stopped {
      if publish && target.AB {
        handleABCall(tracker, requestID, targetID, publish)
      }
    }
    if publish && !tracker.Status.StopRequested && !tracker.Status.Stopped {
      tracker.publishResult(result)
    }
  }
  if wg != nil {
    wg.Done()
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
