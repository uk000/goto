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

package job

import (
	"goto/pkg/global"
	"goto/pkg/invocation"
	"goto/pkg/util"
	"log"
	"time"
)

// runHttpJobWithInput runs an HTTP job with the given markers and raw input as the request payload.
func (jm *JobManager) runHttpJobWithInput(job *Job, markers map[string]string, rawInput []byte) *JobRunContext {
	return jm.runJob(job, nil, markers, rawInput)
}

// invokeHttpTarget invokes the job's HTTP target, storing its results and triggering the output job, while enforcing the job's timeout, max results and stop signal.
func (jm *JobManager) invokeHttpTarget(job *Job, jobRun *JobRunContext, iteration int, last bool) {
	job.lock.RLock()
	target := &job.httpTask.InvocationSpec
	outputTrigger := job.OutputTrigger
	maxResults := job.MaxResults
	timeout := job.Timeout
	job.lock.RUnlock()
	tracker, err := invocation.RegisterInvocation(global.Self.ServerPort, target)
	if err != nil {
		log.Println(err.Error())
		return
	}
	jobRun.lock.RLock()
	if jobRun.rawInput != nil {
		tracker.Payloads = [][]byte{jobRun.rawInput}
	}
	stopChannel := jobRun.stopChannel
	doneChannel := jobRun.doneChannel
	jobRun.lock.RUnlock()
	tracker.Channels.Lock.RLock()
	resultChannel := tracker.Channels.ResultChannel
	tracker.Channels.Lock.RUnlock()
	resultCount := 0

	go func() {
		invocation.StartInvocation(tracker)
		doneChannel <- true
	}()

	sendStopSignal := func() {
		tracker.Stop()
		jobRun.lock.Lock()
		jobRun.stopped = true
		jobRun.lock.Unlock()
	}

	storeResult := func(result *invocation.InvocationResult) bool {
		if resultCount < maxResults {
			if result != nil {
				resultCount++
				storeHttpResult(result, job, iteration, jobRun, last)
				if outputTrigger != nil {
					jm.lock.RLock()
					triggered := jm.jobs[outputTrigger.Name]
					jm.lock.RUnlock()
					if triggered != nil {
						var payload []byte
						if outputTrigger.ForwardPayload {
							payload = result.Response.Payload
							// the transforms are those of the job that gets the payload, which may not be an HTTP job
							if triggered.httpTask != nil && len(triggered.httpTask.Transforms) > 0 {
								payload = []byte(util.TransformPayload(string(payload), triggered.httpTask.Transforms, util.IsYAMLContentType(result.Response.Headers)))
							}
						}
						go jm.runJobWithInput(outputTrigger.Name, nil, payload)
					}
				}
				return true
			}
		}
		return false
	}

	if timeout <= 0 {
		timeout = 24 * time.Hour
	}
	// the call is given this long to finish, whatever results it produces in the meantime
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	// a channel that is closed is always ready to be read, so it's left alone once there are no more results
	results := resultChannel
Done:
	for {
		select {
		case <-timer.C:
			sendStopSignal()
			break Done
		case <-stopChannel:
			sendStopSignal()
			break Done
		case <-doneChannel:
			break Done
		case result, ok := <-results:
			if !ok {
				results = nil
				continue
			}
			if !storeResult(result) {
				sendStopSignal()
			}
		}
	}
	if last {
		jobRun.lock.Lock()
		jobRun.finished = true
		jobRun.lock.Unlock()
	}
	for result := range resultChannel {
		if !storeResult(result) {
			break
		}
	}
}
