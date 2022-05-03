/**
 * Copyright 2022 uk
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
  . "goto/pkg/constants"
  "goto/pkg/global"
  "goto/pkg/invocation"
  "goto/pkg/util"
  "net/http"
  "strconv"
  "strings"
  "time"
)

func GetLatestJobResults(name string) []*JobResult {
  return Manager.GetLatestJobResults(name)
}

func (jm *JobManager) getJobResults(name string) map[int][]interface{} {
  results := map[int][]interface{}{}

  jm.lock.RLock()
  job := jm.jobs[name]
  jobRuns := jm.jobRuns[name]
  jm.lock.RUnlock()

  if job != nil && jobRuns != nil {
    for _, jobRun := range jobRuns {
      jobRun.lock.RLock()
      results[jobRun.id] = []interface{}{}
      for _, r := range jobRun.jobResults {
        results[jobRun.id] = append(results[jobRun.id], r)
      }
      jobRun.lock.RUnlock()
    }
  }
  return results
}

func (jm *JobManager) GetLatestJobResults(name string) []*JobResult {
  var latestJobRun *JobRunContext
  jm.lock.RLock()
  job := jm.jobs[name]
  jobRuns := jm.jobRuns[name]
  if job != nil && jobRuns != nil {
    for id, jobRun := range jobRuns {
      if latestJobRun == nil || id > latestJobRun.id {
        latestJobRun = jobRun
      }
    }
  }
  jm.lock.RUnlock()
  if latestJobRun != nil {
    return latestJobRun.jobResults
  }
  return nil
}

func (jm *JobManager) getAllJobsResults() map[string]map[int][]interface{} {
  results := map[string]map[int][]interface{}{}
  jm.lock.RLock()
  defer jm.lock.RUnlock()
  for _, job := range jm.jobs {
    job.lock.RLock()
    results[job.Name] = map[int][]interface{}{}
    for _, jobRun := range jm.jobRuns[job.Name] {
      jobRun.lock.RLock()
      results[job.Name][jobRun.id] = []interface{}{}
      for _, r := range jobRun.jobResults {
        results[job.Name][jobRun.id] = append(results[job.Name][jobRun.id], r)
      }
      jobRun.lock.RUnlock()
    }
    job.lock.RUnlock()
  }
  return results
}

func storeJobResultsInRegistryLocker(jobID string, runIndex int, jobResults []*JobResult) {
  if global.UseLocker && global.RegistryURL != "" {
    key := "job_" + jobID + "_" + strconv.Itoa(runIndex)
    url := global.RegistryURL + "/registry/peers/" + global.PeerName + "/" + global.PeerAddress + "/locker/store/" + key
    if resp, err := http.Post(url, ContentTypeJSON,
      strings.NewReader(util.ToJSONText(jobResults))); err == nil {
      util.CloseResponse(resp)
    }
  }
}

func storeJobResult(job *Job, jobRun *JobRunContext, iteration int, data interface{}, last bool) {
  job.lock.RLock()
  jobID := job.Name
  keepResults := job.KeepResults
  keepFirst := job.KeepFirst
  job.lock.RUnlock()
  jobRun.lock.Lock()
  defer jobRun.lock.Unlock()
  jobRun.resultCount++
  index := strconv.Itoa(jobRun.id) + "." + strconv.Itoa(iteration) + "." + strconv.Itoa(jobRun.resultCount)
  jobResult := &JobResult{ID: index, Last: last, Stopped: jobRun.stopped,
    Finished: jobRun.finished, ResultTime: time.Now(), Data: data}
  jobRun.jobResults = append(jobRun.jobResults, jobResult)
  if len(jobRun.jobResults) > keepResults {
    if keepFirst {
      jobRun.jobResults = append(jobRun.jobResults[:1], jobRun.jobResults[2:]...)
    } else {
      jobRun.jobResults = jobRun.jobResults[1:]
    }
  }
  storeJobResultsInRegistryLocker(jobID, jobRun.id, jobRun.jobResults)

}

func storeHttpResult(result *invocation.InvocationResult, job *Job, iteration int, jobRun *JobRunContext, last bool) {
  job.lock.RLock()
  httpTask := job.httpTask
  job.lock.RUnlock()
  if httpTask == nil || httpTask.Name != result.TargetName {
    return
  }
  // var payload interface{}
  // if httpTask.ParseJSON {
  //   json := map[string]interface{}{}
  //   if err := util.ReadJson(string(result.Response.Payload), &json); err != nil {
  //     log.Printf("Failed reading response JSON: %s\n", err.Error())
  //     payload = result.Response.Payload
  //   } else {
  //     payload = json
  //   }
  // } else {
  //   payload = result.Response.Payload
  // }
  storeJobResult(job, jobRun, iteration, result, last)
}
