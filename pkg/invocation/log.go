/**
 * Copyright 2021 uk
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
  . "goto/pkg/events/eventslist"
  "goto/pkg/global"
  "log"
)

func (tracker *InvocationTracker) logStartInvocation() {
  if global.EnableInvocationLogs {
    log.Printf("[%s]: Invocation[%d]: Started target [%s] with total requests [%d]\n",
      hostLabel, tracker.ID, tracker.Target.Name, (tracker.Target.Replicas * tracker.Target.RequestCount))
  }
}

func (tracker *InvocationTracker) logStoppingWarmup(remaining int) {
  if global.EnableInvocationLogs {
    log.Printf("[%s]: Invocation[%d]: Stopping target [%s] during warmup with remaining [%d]\n",
      hostLabel, tracker.ID, tracker.Target.Name, remaining)
  }
}

func (tracker *InvocationTracker) logStoppingInvocation() {
  if global.EnableInvocationLogs {
    log.Printf("[%s]: Invocation[%d]: Stopping target [%s] with remaining requests [%d]\n",
      hostLabel, tracker.ID, tracker.Target.Name, (tracker.Target.RequestCount*tracker.Target.Replicas)-tracker.Status.CompletedRequests)
  }
}

func (tracker *InvocationTracker) logFinishedInvocation(remaining int) {
  events.SendEventJSON(Client_InvocationFinished, fmt.Sprintf("%d-%s", tracker.ID, tracker.Target.Name),
    map[string]interface{}{"id": tracker.ID, "target": tracker.Target.Name, "status": tracker.Status})
  if global.EnableInvocationLogs {
    log.Printf("[%s]: Invocation[%d]: finished for target [%s] with remaining requests [%d]\n",
      hostLabel, tracker.ID, tracker.Target.Name, remaining)
  }
}

func (tracker *InvocationTracker) logRequestStart(requestID, targetID, url string) {
  if global.EnableInvocationLogs {
    var headersLog interface{} = ""
    if global.LogRequestHeaders {
      headersLog = tracker.Target.Headers
    }
    log.Printf("[%s]: Invocation[%d]: Request[%s]: Invoking targetID [%s], url [%s], method [%s], headers [%+v]\n",
      hostLabel, tracker.ID, requestID, targetID, url, tracker.Target.Method, headersLog)
  }
}

func (tracker *InvocationTracker) logRetryRequired(result *InvocationResult, remaining int) {
  if global.EnableInvocationLogs {
    log.Printf("[%s]: Invocation[%d]: Request[%s]: Target [%s] url [%s] invocation requires retry due to [%s]. Remaining Retries [%d].",
      hostLabel, tracker.ID, result.Request.ID, result.TargetID, result.Request.URL, result.LastRetryReason, remaining)
  }
}

func (tracker *InvocationTracker) logBRequestCreationFailed(result *InvocationResult, bURL string) {
  if global.EnableInvocationLogs {
    log.Printf("[%s]: Invocation[%d]: Request[%s]: Target [%s] failed to create request for fallback url [%s]. Continuing with retry to previous url [%s] \n",
      hostLabel, tracker.ID, result.Request.ID, result.TargetID, bURL, result.Request.URL)
  }
}

func (tracker *InvocationTracker) logConnectionFailed(details string) {
  if global.EnableInvocationLogs {
    log.Printf("[%s]: Invocation[%d]: Target [%s] failed to open connection with error [%s].\n",
      hostLabel, tracker.ID, tracker.Target.Name, details)
  }
}

func (tracker *InvocationTracker) logResultChannelBacklog(result *InvocationResult, size int) {
  if global.EnableInvocationLogs {
    log.Printf("[%s]: Invocation[%d]: Target %s ResultChannel length %d\n",
      hostLabel, tracker.ID, result.Request.ID, size)
  }
}

func (tracker *InvocationTracker) reportRepeatedResponse() {
  tracker.Status.lock.RLock()
  lastStatusCode := tracker.Status.lastStatusCode
  lastStatusCount := tracker.Status.lastStatusCount
  tracker.Status.lock.RUnlock()
  msg := fmt.Sprintf("[%s]: Invocation[%d]: Target [%s], url [%s], burls %+v, Response Status [%d] Repeated x[%d]",
    hostLabel, tracker.ID, tracker.Target.Name, tracker.Target.URL, tracker.Target.BURLS, lastStatusCode, lastStatusCount)
  events.SendEventJSON(Client_InvocationRepeatedResponse, fmt.Sprintf("%d-%s", tracker.ID, tracker.Target.Name), map[string]interface{}{"id": tracker.ID, "details": msg})
  if global.EnableInvocationLogs {
    log.Println(msg)
  }
}

func (tracker *InvocationTracker) reportRepeatedFailure() {
  msg := fmt.Sprintf("[%s]: Invocation[%d]: Target [%s], url [%s], burls %+v, Failiure [%s] Repeated x[%d]",
    hostLabel, tracker.ID, tracker.Target.Name, tracker.Target.URL, tracker.Target.BURLS, tracker.Status.lastError, tracker.Status.lastErrorCount)
  events.SendEventJSON(Client_InvocationRepeatedFailure, fmt.Sprintf("%d-%s", tracker.ID, tracker.Target.Name), map[string]interface{}{"id": tracker.ID, "details": msg})
  if global.EnableInvocationLogs {
    log.Println(msg)
  }
}

func (tracker *InvocationTracker) reportError(result *InvocationResult) {
  msg := fmt.Sprintf("[%s]: Invocation[%d]: Request[%s]: Target %s, url [%s] failed to invoke with error: %s, repeat count: [%d]",
    hostLabel, tracker.ID, result.Request.ID, result.TargetID, result.Request.URL, result.err.Error(), tracker.Status.lastErrorCount)
  if tracker.Status.lastErrorCount == 0 {
    events.SendEventJSON(Client_InvocationFailure,
      fmt.Sprintf("%d-%s-%s", tracker.ID, result.TargetID, result.Request.ID),
      map[string]interface{}{"id": tracker.ID, "details": msg})
  }
  if global.EnableInvocationLogs {
    log.Println(msg)
  }
}
