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

package job

import (
  "goto/pkg/invocation"
  "goto/pkg/util"
  "log"
  "time"
)

func (jm *JobManager) runHttpJobWithInput(job *Job, markers map[string]string, rawInput []byte) *JobRunContext {
  return jm.runJob(job, nil, markers, rawInput)
}

func (jm *JobManager) invokeHttpTarget(job *Job, jobRun *JobRunContext, iteration int, last bool) {
  job.lock.RLock()
  target := &job.httpTask.InvocationSpec
  outputTrigger := job.OutputTrigger
  maxResults := job.MaxResults
  job.lock.RUnlock()
  tracker, err := invocation.RegisterInvocation(target)
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
          job := jm.jobs[outputTrigger.Name]
          jm.lock.RUnlock()
          if job != nil {
            var payload []byte
            if outputTrigger.ForwardPayload {
              payload = result.Response.Payload
              if len(job.httpTask.Transforms) > 0 {
                payload = []byte(util.TransformPayload(string(payload), job.httpTask.Transforms, util.IsYAMLContentType(result.Response.Headers)))
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

Done:
  for {
    select {
    case <-time.After(job.Timeout):
      sendStopSignal()
      break Done
    case <-stopChannel:
      sendStopSignal()
      break Done
    case <-doneChannel:
      break Done
    case result := <-resultChannel:
      if !storeResult(result) {
        sendStopSignal()
      }
    }
  }
  jobRun.lock.Lock()
  jobRun.finished = true
  jobRun.lock.Unlock()
  for result := range resultChannel {
    if !storeResult(result) {
      break
    }
  }
  jobRun.outDoneChannel <- true
}
