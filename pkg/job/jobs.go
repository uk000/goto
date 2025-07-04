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

package job

import (
	"errors"
	"fmt"
	"goto/pkg/global"
	"goto/pkg/invocation"
	"goto/pkg/util"
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-co-op/gocron"
)

var (
	Manager = newJobManager()
	_       = global.OnShutdown(Manager.StopJobWatch)
)

type Script struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Shell bool   `json:"shell"`
}

type CommandJobTask struct {
	Cmd             string         `json:"cmd"`
	Script          string         `json:"script"`
	Args            []string       `json:"args"`
	OutputMarkers   map[int]string `json:"outputMarkers"`
	OutputSeparator string         `json:"outputSeparator"`
	fillers         map[string]int
}

type HttpJobTask struct {
	invocation.InvocationSpec
	ParseJSON  bool              `json:"parseJSON"`
	Transforms []*util.Transform `json:"transforms"`
}

type JobResult struct {
	ID         string      `json:"id"`
	Finished   bool        `json:"finished"`
	Stopped    bool        `json:"stopped"`
	Last       bool        `json:"last"`
	ResultTime time.Time   `json:"time"`
	Data       interface{} `json:"data"`
}

type JobRunContext struct {
	id             int
	jobName        string
	finished       bool
	stopped        bool
	stopChannel    chan bool
	doneChannel    chan bool
	outDoneChannel chan bool
	resultCount    int
	jobArgs        []string
	jobResults     []*JobResult
	markers        map[string]string
	rawInput       []byte
	lock           sync.RWMutex
}

type JobTrigger struct {
	Name           string `json:"name"`
	ForwardPayload bool   `json:"forwardPayload"`
}

type Job struct {
	Name          string        `json:"name"`
	Task          interface{}   `json:"task"`
	Auto          bool          `json:"auto"`
	Delay         string        `json:"delay"`
	InitialDelay  string        `json:"initialDelay"`
	Count         int           `json:"count"`
	Cron          string        `json:"cron"`
	MaxResults    int           `json:"maxResults"`
	KeepResults   int           `json:"keepResults"`
	KeepFirst     bool          `json:"keepFirst"`
	Timeout       time.Duration `json:"timeout"`
	OutputTrigger *JobTrigger   `json:"outputTrigger"`
	FinishTrigger *JobTrigger   `json:"finishTrigger"`
	delayD        time.Duration
	initialDelayD time.Duration
	httpTask      *HttpJobTask
	commandTask   *CommandJobTask
	jobRunCounter int
	cronScheduler *gocron.Scheduler
	cronJob       *gocron.Job
	lock          sync.RWMutex
}

type JobWatcher func(name string, runId int, results []*JobResult)

type JobManager struct {
	jobs          map[string]*Job
	jobRuns       map[string]map[int]*JobRunContext
	scripts       map[string]*Script
	cronScheduler *gocron.Scheduler
	watchers      map[string]map[string]JobWatcher
	watchChannel  chan *JobRunContext
	stopJobWatch  chan bool
	lock          sync.RWMutex
}

func newJobManager() *JobManager {
	j := &JobManager{}
	j.init()
	go j.WatchJobs()
	return j
}

func (jm *JobManager) init() {
	jm.lock.Lock()
	defer jm.lock.Unlock()
	jm.jobs = map[string]*Job{}
	jm.jobRuns = map[string]map[int]*JobRunContext{}
	jm.scripts = map[string]*Script{}
	jm.cronScheduler = gocron.NewScheduler(time.UTC)
	jm.watchers = map[string]map[string]JobWatcher{}
	jm.watchChannel = make(chan *JobRunContext, 100)
	jm.stopJobWatch = make(chan bool, 2)
}

func (jm *JobManager) StopJobWatch() {
	jm.stopJobWatch <- true
}

func (jm *JobManager) AddJobWatcher(job, name string, watcher JobWatcher) {
	jm.lock.Lock()
	defer jm.lock.Unlock()
	if jm.watchers[job] == nil {
		jm.watchers[job] = map[string]JobWatcher{}
	}
	jm.watchers[job][name] = watcher
}

func (jm *JobManager) RemoveJobWatcher(job, name string) {
	jm.lock.Lock()
	defer jm.lock.Unlock()
	if jm.watchers[job] != nil {
		delete(jm.watchers[job], name)
		if len(jm.watchers[job]) == 0 {
			delete(jm.watchers, job)
		}
	}
}

func (jm *JobManager) ClearJobWatchers(job string) {
	jm.lock.Lock()
	defer jm.lock.Unlock()
	delete(jm.watchers, job)
}

func (jm *JobManager) WatchJobs() {
WatchLoop:
	for {
		select {
		case <-jm.stopJobWatch:
			break WatchLoop
		case jobRun := <-jm.watchChannel:
			go func() {
				jm.lock.RLock()
				var watchers []JobWatcher
				for _, w := range jm.watchers[jobRun.jobName] {
					watchers = append(watchers, w)
				}
				jm.lock.RUnlock()
				for _, w := range watchers {
					w(jobRun.jobName, jobRun.id, jobRun.jobResults)
				}
			}()
		}
	}
}

func (jm *JobManager) RunJob(job *Job) {
	if job.Cron != "" {
		if err := job.schedule(); err != nil {
			log.Printf("Jobs: Failed to schedule cron job: %+v\n", job)
		}
		job.cronScheduler.StartAsync()
	} else {
		jm.runJob(job, nil, nil, nil)
	}
}

func (jm *JobManager) StoreJobScriptOrFile(filePath, fileName string, content []byte, scriptJob bool) (string, string, bool) {
	if path, err := util.StoreFile(filePath, fileName, content); err == nil {
		var extension = filepath.Ext(fileName)
		scriptName := fileName[0 : len(fileName)-len(extension)]
		script := &Script{Name: scriptName, Path: filepath.FromSlash(filePath + "/" + fileName), Shell: extension == ".sh"}
		jm.scripts[scriptName] = script
		if scriptJob {
			task := &CommandJobTask{Script: scriptName}
			job := &Job{Name: scriptName, Task: task, commandTask: task, Count: 1, KeepResults: 3}
			Manager.AddJob(job)
		}
		return scriptName, path, true
	} else {
		fmt.Printf("Jobs: Failed to store job file [%s] with error: %s\n", filePath, err.Error())
	}
	return "", "", false
}

func (jm *JobManager) removeJobs(jobs []string) {
	jm.lock.Lock()
	defer jm.lock.Unlock()
	for _, j := range jobs {
		job := jm.jobs[j]
		if job != nil {
			job.lock.Lock()
			defer job.lock.Unlock()
			delete(jm.jobs, j)
			delete(jm.jobRuns, j)
		}
	}
}

func (jm *JobManager) AddJob(job *Job) (bool, error) {
	jm.lock.Lock()
	defer jm.lock.Unlock()
	exists := jm.jobs[job.Name] != nil
	jm.jobs[job.Name] = job
	delete(jm.jobRuns, job.Name)
	if job.Auto || job.Cron != "" {
		log.Printf("Jobs: Auto-invoking Job: %s\n", job.Name)
		go jm.RunJob(job)
	}
	return exists, nil
}

func ParseJobFromPayload(payload string) (*Job, error) {
	job := &Job{}
	if err := util.ReadJson(payload, job); err == nil {
		if job.delayD, _ = time.ParseDuration(job.Delay); job.delayD == 0 {
			job.delayD = 10 * time.Millisecond
		}
		if job.initialDelayD, _ = time.ParseDuration(job.InitialDelay); job.initialDelayD == 0 {
			job.initialDelayD = 1 * time.Second
		}
		if job.Count == 0 {
			job.Count = 1
		}
		if job.KeepResults <= 0 {
			job.KeepResults = 5
		}
		if job.MaxResults <= 0 {
			job.MaxResults = 20
		}
		if job.Timeout <= 0 {
			job.Timeout = 10 * time.Minute
		}
		if job.Task != nil {
			var httpTask HttpJobTask
			commandTask := CommandJobTask{fillers: map[string]int{}}
			var httpTaskError error
			var cmdTaskError error
			task := util.ToJSONText(job.Task)
			if httpTaskError = util.ReadJson(task, &httpTask); httpTaskError == nil {
				if httpTaskError = invocation.ValidateSpec(&httpTask.InvocationSpec); httpTaskError == nil {
					httpTask.CollectResponse = true
					httpTask.TrackPayload = true
					job.httpTask = &httpTask
					if len(httpTask.Transforms) > 0 {
						httpTask.CollectResponse = true
					}
				}
			}
			if httpTaskError != nil {
				if cmdTaskError = util.ReadJson(task, &commandTask); cmdTaskError == nil {
					if commandTask.Cmd != "" || commandTask.Script != "" {
						for _, arg := range commandTask.Args {
							fillers := util.GetFillers(arg)
							for _, filler := range fillers {
								commandTask.fillers[filler]++
							}
						}
						job.commandTask = &commandTask
					} else {
						cmdTaskError = errors.New("Jobs: A command task must specify a command or reference a script")
					}
				}
			}
			if httpTaskError == nil || cmdTaskError == nil {
				return job, nil
			} else {
				msg := ""
				if cmdTaskError != nil {
					msg += "Jobs: Command Task Error: [" + cmdTaskError.Error() + "] "
				}
				if httpTaskError != nil {
					msg = "Jobs: HTTP Task Error: [" + httpTaskError.Error() + "] "
				}
				err := errors.New(msg)
				return job, err
			}
		} else {
			return nil, fmt.Errorf("Jobs: Invalid Task: %s", err.Error())
		}
	} else {
		return nil, fmt.Errorf("Jobs: Failed to parse json with error: %s", err.Error())
	}
}
