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
	"errors"
	"fmt"
	"goto/pkg/global"
	"goto/pkg/invocation"
	"goto/pkg/util"
	"log"
	"path/filepath"
	"regexp"
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
	scheduleLock  sync.Mutex
	lock          sync.RWMutex
}

// newJobManager creates an initialized job manager and starts its job watcher goroutine.
func newJobManager() *JobManager {
	j := &JobManager{}
	j.init()
	go j.WatchJobs()
	return j
}

// init resets the manager's jobs, runs, scripts and watchers with a fresh cron scheduler, and asynchronously shuts down the previous one.
func (jm *JobManager) init() {
	jm.lock.Lock()
	defer jm.lock.Unlock()
	previous := jm.cronScheduler
	jm.jobs = map[string]*Job{}
	jm.jobRuns = map[string]map[int]*JobRunContext{}
	jm.scripts = map[string]*Script{}
	jm.cronScheduler = gocron.NewScheduler(time.UTC)
	jm.watchers = map[string]map[string]JobWatcher{}
	// The job watcher is already waiting on these channels once the manager was created, and so it keeps them for good.
	if jm.watchChannel == nil {
		jm.watchChannel = make(chan *JobRunContext, 100)
	}
	if jm.stopJobWatch == nil {
		jm.stopJobWatch = make(chan bool, 2)
	}
	if previous != nil {
		// stopping waits for the cron runs that are going, which may need the lock that is held here
		go func() {
			previous.Clear()
			previous.Stop()
		}()
	}
}

// getCronScheduler returns the manager's current cron scheduler.
func (jm *JobManager) getCronScheduler() *gocron.Scheduler {
	jm.lock.RLock()
	defer jm.lock.RUnlock()
	return jm.cronScheduler
}

// StopJobWatch signals the job watcher goroutine to stop.
func (jm *JobManager) StopJobWatch() {
	jm.stopJobWatch <- true
}

// AddJobWatcher registers a named watcher to be called with the results of a job's finished runs.
func (jm *JobManager) AddJobWatcher(job, name string, watcher JobWatcher) {
	jm.lock.Lock()
	defer jm.lock.Unlock()
	if jm.watchers[job] == nil {
		jm.watchers[job] = map[string]JobWatcher{}
	}
	jm.watchers[job][name] = watcher
}

// RemoveJobWatcher removes a named watcher of a job.
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

// ClearJobWatchers removes all watchers of a job.
func (jm *JobManager) ClearJobWatchers(job string) {
	jm.lock.Lock()
	defer jm.lock.Unlock()
	delete(jm.watchers, job)
}

// WatchJobs notifies a job's registered watchers with the run's results as runs complete, until StopJobWatch is called.
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
				if len(watchers) > 0 {
					results := jobRun.getResults()
					for _, w := range watchers {
						w(jobRun.jobName, jobRun.id, results)
					}
				}
			}()
		}
	}
}

// RunJob schedules the job if it has a cron, otherwise runs it right away.
func (jm *JobManager) RunJob(job *Job) {
	if job.Cron != "" {
		if err := jm.schedule(job); err != nil {
			log.Printf("Jobs: Failed to schedule cron job [%s] with error: %s\n", job.Name, err.Error())
		}
	} else {
		jm.runJob(job, nil, nil, nil)
	}
}

// StoreJobScriptOrFile stores the content as a file, registers it as a script, and optionally creates a job that runs it.
func (jm *JobManager) StoreJobScriptOrFile(filePath, fileName string, content []byte, scriptJob bool) (string, string, bool) {
	if fileName == "" {
		fmt.Printf("Jobs: Failed to store job file [%s] without a name\n", filePath)
		return "", "", false
	}
	if path, err := util.StoreFile(filePath, fileName, content); err == nil {
		var extension = filepath.Ext(fileName)
		scriptName := fileName[0 : len(fileName)-len(extension)]
		// the script is run from wherever the file was stored, which is what the path of the file says
		scriptPath := path
		if abs, err := filepath.Abs(path); err == nil {
			scriptPath = abs
		}
		script := &Script{Name: scriptName, Path: scriptPath, Shell: extension == ".sh"}
		jm.lock.Lock()
		jm.scripts[scriptName] = script
		jm.lock.Unlock()
		if scriptJob {
			task := &CommandJobTask{Script: scriptName}
			job := &Job{Name: scriptName, Task: task, commandTask: task, Count: 1, KeepResults: 3}
			jm.AddJob(job)
		}
		return scriptName, path, true
	} else {
		fmt.Printf("Jobs: Failed to store job file [%s] with error: %s\n", filePath, err.Error())
	}
	return "", "", false
}

// removeJobs removes the named jobs, their cron schedules and their runs.
func (jm *JobManager) removeJobs(jobs []string) {
	jm.lock.Lock()
	defer jm.lock.Unlock()
	for _, j := range jobs {
		if jm.jobs[j] != nil {
			// a job that is removed must not be run again by its schedule
			jm.cronScheduler.RemoveByTag(j)
			delete(jm.jobs, j)
			delete(jm.jobRuns, j)
		}
	}
}

// AddJob adds or replaces a job, returning whether it already existed, and auto-runs or schedules it if it is an auto or cron job.
func (jm *JobManager) AddJob(job *Job) (bool, error) {
	jm.lock.Lock()
	defer jm.lock.Unlock()
	exists := jm.jobs[job.Name] != nil
	if exists {
		// the job that is replaced must not go on running by its schedule
		jm.cronScheduler.RemoveByTag(job.Name)
	}
	jm.jobs[job.Name] = job
	delete(jm.jobRuns, job.Name)
	if job.Cron != "" {
		job.Auto = true
	}
	if job.Auto || job.Cron != "" {
		log.Printf("Jobs: Auto-invoking Job: %s\n", job.Name)
		go jm.RunJob(job)
	}
	return exists, nil
}

// getJobs and getScripts are the jobs and the scripts, which can be reported while they are being changed.
func (jm *JobManager) getJobs() map[string]*Job {
	jm.lock.RLock()
	defer jm.lock.RUnlock()
	jobs := make(map[string]*Job, len(jm.jobs))
	for k, v := range jm.jobs {
		jobs[k] = v
	}
	return jobs
}

// getScript returns the stored script of the name, or nil if there is none.
func (jm *JobManager) getScript(name string) *Script {
	jm.lock.RLock()
	defer jm.lock.RUnlock()
	return jm.scripts[name]
}

// getScripts returns a copy of the stored scripts.
func (jm *JobManager) getScripts() map[string]*Script {
	jm.lock.RLock()
	defer jm.lock.RUnlock()
	scripts := make(map[string]*Script, len(jm.scripts))
	for k, v := range jm.scripts {
		scripts[k] = v
	}
	return scripts
}

// validateTransforms refuses the mappings that have a condition that isn't a regex, which would otherwise make
// initializing them panic, and initializes the mappings, which makes them honor their mode and conditions.
func validateTransforms(transforms []*util.Transform) error {
	for _, t := range transforms {
		if t == nil {
			return errors.New("null transform")
		}
		for _, m := range t.Mappings {
			if m == nil {
				return errors.New("null transform mapping")
			}
			for _, cond := range []string{m.IfContains, m.IfNotContains} {
				if cond != "" {
					if _, err := regexp.Compile("(?i)" + cond); err != nil {
						return fmt.Errorf("invalid transform condition [%s]: %s", cond, err.Error())
					}
				}
			}
		}
	}
	for _, t := range transforms {
		for _, m := range t.Mappings {
			m.Init()
		}
	}
	return nil
}

// ParseJobFromPayload parses a job from JSON, applying defaults and validating its task as either an HTTP task or a command task.
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
					if httpTaskError = validateTransforms(httpTask.Transforms); httpTaskError == nil {
						httpTask.CollectResponse = true
						httpTask.TrackPayload = true
						job.httpTask = &httpTask
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
				if httpTaskError != nil {
					msg += "Jobs: HTTP Task Error: [" + httpTaskError.Error() + "] "
				}
				if cmdTaskError != nil {
					msg += "Jobs: Command Task Error: [" + cmdTaskError.Error() + "] "
				}
				err := errors.New(msg)
				return job, err
			}
		} else {
			return nil, errors.New("Jobs: Invalid Task: no task given")
		}
	} else {
		return nil, fmt.Errorf("Jobs: Failed to parse json with error: %s", err.Error())
	}
}
