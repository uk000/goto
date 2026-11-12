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
	. "goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/invocation"
	"goto/pkg/util"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// GetLatestJobResults returns the results of the latest run of the named job from the global job manager.
func GetLatestJobResults(name string) []*JobResult {
	return Manager.GetLatestJobResults(name)
}

// state tells whether the run has finished, and whether it was stopped.
func (jr *JobRunContext) state() (finished, stopped bool) {
	jr.lock.RLock()
	defer jr.lock.RUnlock()
	return jr.finished, jr.stopped
}

// getResults is a copy of the results of the run, which doesn't change when more results are added to the run.
func (jr *JobRunContext) getResults() []*JobResult {
	jr.lock.RLock()
	defer jr.lock.RUnlock()
	return append([]*JobResult{}, jr.jobResults...)
}

// getJobResults returns the results of every run of the named job, keyed by run ID.
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

// GetLatestJobResults returns a copy of the results of the run with the highest ID for the named job, or nil if it has none.
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
		return latestJobRun.getResults()
	}
	return nil
}

// getAllJobsResults returns the results of every run of every job, keyed by job name and run ID.
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

// registryClient doesn't wait for a registry that doesn't answer for longer than this
var registryClient = &http.Client{Timeout: 30 * time.Second}

// storeJobResultsInRegistryLocker posts a run's kept results to the registry locker when the locker is enabled and a registry is configured.
func storeJobResultsInRegistryLocker(jobID string, runIndex int, jobResults []*JobResult) {
	if global.Flags.UseLocker && global.Self.RegistryURL != "" {
		key := "job_" + jobID + "_" + strconv.Itoa(runIndex)
		url := global.Self.RegistryURL + "/registry/peers/" + global.Self.Name + "/" + global.Self.Address + "/locker/store/" + key
		if resp, err := registryClient.Post(url, ContentTypeJSON,
			strings.NewReader(util.ToJSONText(jobResults))); err == nil {
			util.CloseResponse(resp)
		}
	}
}

// storeJobResult appends a result to the run, trimming it to the job's keep-results limit (optionally keeping the first), and mirrors the kept results to the registry locker.
func storeJobResult(job *Job, jobRun *JobRunContext, iteration int, data interface{}, last bool) {
	job.lock.RLock()
	jobID := job.Name
	keepResults := job.KeepResults
	keepFirst := job.KeepFirst
	job.lock.RUnlock()
	jobRun.lock.Lock()
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
	kept := append([]*JobResult{}, jobRun.jobResults...)
	runID := jobRun.id
	jobRun.lock.Unlock()
	// the registry is called with no lock held, because the run can't be stopped or reported while it's waited for
	storeJobResultsInRegistryLocker(jobID, runID, kept)
}

// storeHttpResult stores an invocation result as a job result if it belongs to the job's HTTP task target.
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
