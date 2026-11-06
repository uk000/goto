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
	"goto/pkg/global"
	"log"
)

// logStartInvocation logs the start of an invocation along with the total number of requests (replicas x request count).
func (tracker *InvocationTracker) logStartInvocation() {
	if global.Flags.EnableInvocationLogs {
		log.Printf("[%s]: Invocation[%d]: Started target [%s] with total requests [%d]\n",
			global.Self.Name, tracker.ID, tracker.Target.Name, (tracker.Target.Replicas * tracker.Target.RequestCount))
	}
}

// logStoppingWarmup logs that the target is being stopped during warmup, with the number of warmup requests remaining.
func (tracker *InvocationTracker) logStoppingWarmup(remaining int) {
	if global.Flags.EnableInvocationLogs {
		log.Printf("[%s]: Invocation[%d]: Stopping target [%s] during warmup with remaining [%d]\n",
			global.Self.Name, tracker.ID, tracker.Target.Name, remaining)
	}
}

// logStoppingInvocation logs that the invocation is being stopped, with the number of requests left unsent.
func (tracker *InvocationTracker) logStoppingInvocation() {
	if global.Flags.EnableInvocationLogs {
		log.Printf("[%s]: Invocation[%d]: Stopping target [%s] with remaining requests [%d]\n",
			global.Self.Name, tracker.ID, tracker.Target.Name, (tracker.Target.RequestCount*tracker.Target.Replicas)-tracker.Status.CompletedRequests)
	}
}

// logFinishedInvocation logs that the invocation finished, with the number of requests remaining.
func (tracker *InvocationTracker) logFinishedInvocation(remaining int) {
	if global.Flags.EnableInvocationLogs {
		log.Printf("[%s]: Invocation[%d]: finished for target [%s] with remaining requests [%d]\n",
			global.Self.Name, tracker.ID, tracker.Target.Name, remaining)
	}
}

// logRequestStart logs the request about to be invoked, including its headers only when request header logging is enabled.
func (tracker *InvocationTracker) logRequestStart(requestID, targetID, url string) {
	if global.Flags.EnableInvocationLogs {
		var headersLog interface{} = ""
		if global.Flags.LogRequestHeaders {
			headersLog = tracker.Target.Headers
		}
		log.Printf("[%s]: Invocation[%d]: Request[%s]: Invoking targetID [%s], url [%s], method [%s], headers [%+v]\n",
			global.Self.Name, tracker.ID, requestID, targetID, url, tracker.Target.Method, headersLog)
	}
}

// logRetryRequired logs that a request needs a retry, with the retry reason and the remaining retry count.
func (tracker *InvocationTracker) logRetryRequired(result *InvocationResult, remaining int) {
	if global.Flags.EnableInvocationLogs {
		log.Printf("[%s]: Invocation[%d]: Request[%s]: Target [%s] url [%s] invocation requires retry due to [%s]. Remaining Retries [%d].",
			global.Self.Name, tracker.ID, result.Request.ID, result.TargetID, result.Request.URL, result.LastRetryReason, remaining)
	}
}

// logBRequestCreationFailed logs that the request for a fallback B-URL could not be created and the retry continues on the previous URL.
func (tracker *InvocationTracker) logBRequestCreationFailed(result *InvocationResult, bURL string) {
	if global.Flags.EnableInvocationLogs {
		log.Printf("[%s]: Invocation[%d]: Request[%s]: Target [%s] failed to create request for fallback url [%s]. Continuing with retry to previous url [%s] \n",
			global.Self.Name, tracker.ID, result.Request.ID, result.TargetID, bURL, result.Request.URL)
	}
}

// logConnectionFailed logs that the target failed to open a connection, with the error details.
func (tracker *InvocationTracker) logConnectionFailed(details string) {
	if global.Flags.EnableInvocationLogs {
		log.Printf("[%s]: Invocation[%d]: Target [%s] failed to open connection with error [%s].\n",
			global.Self.Name, tracker.ID, tracker.Target.Name, details)
	}
}

// logResultChannelBacklog logs the current length of the result channel to flag a backlog of unconsumed results.
func (tracker *InvocationTracker) logResultChannelBacklog(result *InvocationResult, size int) {
	if global.Flags.EnableInvocationLogs {
		log.Printf("[%s]: Invocation[%d]: Target %s ResultChannel length %d\n",
			global.Self.Name, tracker.ID, result.Request.ID, size)
	}
}

// reportRepeatedResponse logs the last response status code and the number of consecutive times it repeated.
func (tracker *InvocationTracker) reportRepeatedResponse() {
	tracker.Status.lock.RLock()
	lastStatusCode := tracker.Status.lastStatusCode
	lastStatusCount := tracker.Status.lastStatusCount
	tracker.Status.lock.RUnlock()
	msg := fmt.Sprintf("[%s]: Invocation[%d]: Target [%s], url [%s], burls %+v, Response Status [%d] Repeated x[%d]",
		global.Self.Name, tracker.ID, tracker.Target.Name, tracker.Target.URL, tracker.Target.BURLS, lastStatusCode, lastStatusCount)
	if global.Flags.EnableInvocationLogs {
		log.Println(msg)
	}
}

// reportRepeatedFailure logs the last failure and the number of consecutive times it repeated.
func (tracker *InvocationTracker) reportRepeatedFailure() {
	msg := fmt.Sprintf("[%s]: Invocation[%d]: Target [%s], url [%s], burls %+v, Failiure [%s] Repeated x[%d]",
		global.Self.Name, tracker.ID, tracker.Target.Name, tracker.Target.URL, tracker.Target.BURLS, tracker.Status.lastError, tracker.Status.lastErrorCount)
	if global.Flags.EnableInvocationLogs {
		log.Println(msg)
	}
}

// reportError logs a failed request invocation with its error and the consecutive error repeat count.
func (tracker *InvocationTracker) reportError(result *InvocationResult) {
	msg := fmt.Sprintf("[%s]: Invocation[%d]: Request[%s]: Target %s, url [%s] failed to invoke with error: %s, repeat count: [%d]",
		global.Self.Name, tracker.ID, result.Request.ID, result.TargetID, result.Request.URL, result.err.Error(), tracker.Status.lastErrorCount)
	if global.Flags.EnableInvocationLogs {
		log.Println(msg)
	}
}
