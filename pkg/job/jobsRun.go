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
	"fmt"
	"goto/pkg/events"
	"goto/pkg/invocation"
	"goto/pkg/util"
	"log"
	"time"

	"github.com/go-co-op/gocron"
)

func RunJobWithInput(job string, input map[string]string, rawInput []byte) int {
	if jobRun := Manager.runJobWithInput(job, input, rawInput); jobRun != nil {
		return jobRun.id
	}
	return 0
}

func WaitForJob(job string, runId int, timeout time.Duration) bool {
	return Manager.waitForJob(job, runId, timeout)
}

func RunJobWithInputAndWait(job string, input map[string]string, rawInput []byte, timeout time.Duration) bool {
	if jobRun := Manager.runJobWithInput(job, input, rawInput); jobRun != nil {
		return Manager.waitForJob(job, jobRun.id, timeout)
	}
	return false
}

func RunJobs(jobs []string) {
	_, jobsToRun := Manager.getJobsToRun(jobs)
	Manager.runJobs(jobsToRun)
}

func StopJob(job string) bool {
	return Manager.stopJob(job)
}

func StopJobs(jobs []string) {
	for _, job := range jobs {
		StopJob(job)
	}
}

func (jm *JobManager) initJobRun(job *Job, jobArgs []string, markers map[string]string, rawInput []byte) *JobRunContext {
	if job == nil || (job.commandTask == nil && job.httpTask == nil) {
		return nil
	}
	jobRun := &JobRunContext{}
	jobRun.jobName = job.Name
	jobRun.jobResults = []*JobResult{}
	jobRun.jobArgs = jobArgs
	jobRun.markers = markers
	jobRun.rawInput = rawInput
	jobRun.stopChannel = make(chan bool, 10)
	jobRun.doneChannel = make(chan bool, 10)
	jobRun.outDoneChannel = make(chan bool, 2)

	job.lock.Lock()
	job.jobRunCounter++
	jobRun.id = job.jobRunCounter
	job.lock.Unlock()

	jm.lock.Lock()
	if jm.jobRuns[job.Name] == nil {
		jm.jobRuns[job.Name] = map[int]*JobRunContext{}
	}
	jm.jobRuns[job.Name][jobRun.id] = jobRun
	jm.lock.Unlock()

	return jobRun
}

func (jm *JobManager) waitForJob(jobName string, runId int, timeout time.Duration) bool {
	var jobRun *JobRunContext
	jm.lock.RLock()
	if jm.jobRuns[jobName] != nil {
		jobRun = jm.jobRuns[jobName][runId]
	}
	jm.lock.RUnlock()
	if jobRun == nil {
		return false
	}
	if finished, stopped := jobRun.state(); !finished && !stopped {
		select {
		case <-jobRun.outDoneChannel:
			return true
		case <-time.After(timeout):
			return false
		}
	}
	time.Sleep(1 * time.Second)
	finished, _ := jobRun.state()
	return finished
}

func (jm *JobManager) runJobWithInput(jobName string, markers map[string]string, rawInput []byte) *JobRunContext {
	jm.lock.RLock()
	job := jm.jobs[jobName]
	jm.lock.RUnlock()
	if job != nil {
		if job.commandTask != nil {
			return jm.runCommandWithInput(job, markers, rawInput)
		} else if job.httpTask != nil {
			return jm.runHttpJobWithInput(job, markers, rawInput)
		}
	}
	return nil
}

func (jm *JobManager) runJob(job *Job, jobArgs []string, markers map[string]string, rawInput []byte) *JobRunContext {
	jobRun := jm.initJobRun(job, jobArgs, markers, rawInput)
	if jobRun != nil {
		jm.executeJobRun(job, jobRun)
	}
	return jobRun
}

func (jm *JobManager) runJobs(jobs []*Job) {
	for _, job := range jobs {
		go jm.RunJob(job)
	}
}

func (jm *JobManager) executeJobRun(job *Job, jobRun *JobRunContext) {
	job.lock.Lock()
	count := job.Count
	delay := job.delayD
	initialDelay := job.initialDelayD
	finishTrigger := job.FinishTrigger
	if job.commandTask != nil {
		jobRun.lock.Lock()
		if jobRun.jobArgs == nil {
			jobRun.jobArgs = job.commandTask.Args
		}
		jobRun.lock.Unlock()
	}
	job.lock.Unlock()

	log.Printf("Jobs: Job [%s] Run [%d] Started \n", job.Name, jobRun.id)
	events.SendEventJSON(events.Jobs_JobStarted, job.Name, map[string]interface{}{"job": job, "jobRun": jobRun.id})

	time.Sleep(initialDelay)
	for i := 0; i < count; i++ {
		time.Sleep(delay)
		if job.commandTask != nil {
			jm.runCommandJob(job, jobRun, i+1, i == count-1)
		} else if job.httpTask != nil {
			jm.invokeHttpTarget(job, jobRun, i+1, i == count-1)
		}
		finished, stopped := jobRun.state()
		if finished || stopped {
			break
		}
	}
	// The run is over, which the jobs that stop it need to know. The channels of the run are not closed, because the
	// goroutines that read the output of a command may still be sending on them.
	jobRun.lock.Lock()
	jobRun.finished = true
	jobRun.lock.Unlock()
	select {
	case jobRun.outDoneChannel <- true:
	default:
	}
	msg := fmt.Sprintf("Jobs: Job [%s] Run [%d] Finished", job.Name, jobRun.id)
	log.Println(msg)
	events.SendEvent(events.Jobs_JobFinished, msg)

	if finishTrigger != nil {
		var payload []byte
		if finishTrigger.ForwardPayload {
			if latestResults := jobRun.getResults(); len(latestResults) > 0 {
				var data []interface{}
				for _, r := range latestResults {
					// the response of an HTTP call is what is forwarded, and not where the result of the call is kept
					if ir, ok := r.Data.(*invocation.InvocationResult); ok && ir != nil {
						data = append(data, string(ir.Response.Payload))
					} else {
						data = append(data, r.Data)
					}
				}
				payload = []byte(fmt.Sprint(data))
			}
		}
		if job.httpTask != nil && len(job.httpTask.Transforms) > 0 {
			payload = []byte(util.TransformPayload(string(payload), job.httpTask.Transforms, false))
		}
		jm.runJobWithInput(finishTrigger.Name, nil, payload)
	}
	select {
	case jm.watchChannel <- jobRun:
	default:
		log.Printf("Jobs: Job [%s] Run [%d] couldn't be reported to the watchers\n", job.Name, jobRun.id)
	}
}

func (jm *JobManager) stopJob(j string) bool {
	jm.lock.RLock()
	job := jm.jobs[j]
	scheduler := jm.cronScheduler
	var jobRuns []*JobRunContext
	for _, jobRun := range jm.jobRuns[j] {
		jobRuns = append(jobRuns, jobRun)
	}
	jm.lock.RUnlock()
	if job == nil {
		return false
	}
	// Only the schedule of this job is removed. The scheduler is shared by all the cron jobs, and stopping it would wait
	// for the runs that are going, which can't end while this holds the locks that they need.
	scheduler.RemoveByTag(job.Name)
	job.lock.Lock()
	job.cronJob = nil
	job.lock.Unlock()
	if len(jobRuns) == 0 {
		return false
	}
	for _, jobRun := range jobRuns {
		jobRun.lock.Lock()
		if !jobRun.finished && !jobRun.stopped {
			select {
			case jobRun.stopChannel <- true:
			default:
			}
		}
		jobRun.lock.Unlock()
		events.SendEventJSON(events.Jobs_JobStopped, job.Name, job)
	}
	return true
}

func (jm *JobManager) getJobsToRun(names []string) ([]string, []*Job) {
	jm.lock.RLock()
	defer jm.lock.RUnlock()
	var jobsToRun []*Job
	if len(names) > 0 {
		for _, j := range names {
			if job, found := jm.jobs[j]; found {
				jobsToRun = append(jobsToRun, job)
			}
		}
	} else {
		if len(jm.jobs) > 0 {
			for _, job := range jm.jobs {
				jobsToRun = append(jobsToRun, job)
				names = append(names, job.Name)
			}
		}
	}
	return names, jobsToRun
}

func (jm *JobManager) stopJobs(jobs []string) {
	for _, job := range jobs {
		jm.stopJob(job)
	}
}

// schedule makes the manager run the job at the frequency of its cron, and starts the scheduler if it isn't going.
func (jm *JobManager) schedule(job *Job) error {
	// the scheduler builds a job in steps, which isn't safe for jobs to be scheduled at the same time
	jm.scheduleLock.Lock()
	defer jm.scheduleLock.Unlock()
	scheduler := jm.getCronScheduler()
	// a job runs once at a time by its schedule, however many times it is started
	scheduler.RemoveByTag(job.Name)
	var builder *gocron.Scheduler
	if d, err := time.ParseDuration(job.Cron); err == nil {
		builder = scheduler.Every(d)
	} else {
		builder = scheduler.Cron(job.Cron)
	}
	cronJob, err := builder.Tag(job.Name).Do(func() {
		log.Printf("Jobs: Running cron job [%s]\n", job.Name)
		jm.runJob(job, nil, nil, nil)
	})
	if err != nil {
		return err
	}
	job.lock.Lock()
	job.cronJob = cronJob
	job.lock.Unlock()
	scheduler.StartAsync()
	return nil
}
