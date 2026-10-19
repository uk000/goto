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
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goto/pkg/global"
	"goto/pkg/util"
)

var (
	stubOnce sync.Once
	nameSeq  atomic.Int32
	nonWord  = regexp.MustCompile(`[^A-Za-z0-9]+`)
)

func stubGlobalFuncs() {
	global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsListenerPresent = func(int) bool { return false }
	global.Funcs.IsListenerOpen = func(int) bool { return false }
	global.Funcs.GetListenerID = func(int) string { return "" }
	global.Funcs.GetListenerLabel = func(*http.Request) string { return "" }
	global.Funcs.GetListenerLabelForPort = func(int) string { return "" }
	util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
}

// setup makes the job manager, which is shared by the whole package, start the test with no jobs, and stops what the
// test started when it ends.
func setup(t *testing.T) {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
	cleanupManager()
	t.Cleanup(cleanupManager)
}

func runEnded(jr *JobRunContext) bool {
	// a run whose lock is held for good has nothing to wait for
	if !jr.lock.TryRLock() {
		return true
	}
	defer jr.lock.RUnlock()
	return jr.finished || jr.stopped
}

// cleanupManager stops all the jobs, waits for their runs to end, and forgets the jobs, the scripts and the watchers.
// The runs that are left over would otherwise add themselves to the manager after the next test has started.
func cleanupManager() {
	Manager.lock.RLock()
	names := []string{}
	for n := range Manager.jobs {
		names = append(names, n)
	}
	Manager.lock.RUnlock()
	for _, n := range names {
		StopJob(n)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		busy := false
		Manager.lock.RLock()
		for _, runs := range Manager.jobRuns {
			for _, jr := range runs {
				if !runEnded(jr) {
					busy = true
				}
			}
		}
		Manager.lock.RUnlock()
		if !busy {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	Manager.lock.Lock()
	Manager.jobs = map[string]*Job{}
	Manager.jobRuns = map[string]map[int]*JobRunContext{}
	Manager.scripts = map[string]*Script{}
	Manager.watchers = map[string]map[string]JobWatcher{}
	Manager.cronScheduler.Clear()
	Manager.lock.Unlock()
}

// jobName is a job name that no other test uses.
func jobName(t *testing.T, label string) string {
	return fmt.Sprintf("%s_%s_%d", nonWord.ReplaceAllString(t.Name(), "_"), label, nameSeq.Add(1))
}

// payload is the JSON of a job. The initial delay is short, because a job gets a second of it when none is given.
func payload(name, task string, fields ...string) string {
	extra := ""
	for _, f := range fields {
		extra += "," + f
	}
	return fmt.Sprintf(`{"name":%q,"initialDelay":"1ms","delay":"1ms","task":%s%s}`, name, task, extra)
}

func cmdTask(cmd string, args ...string) string {
	if args == nil {
		args = []string{}
	}
	b, _ := json.Marshal(args)
	return fmt.Sprintf(`{"cmd":%q,"args":%s}`, cmd, b)
}

func shTask(script string) string { return cmdTask("sh", "-c", script) }

func parseJob(t *testing.T, p string) *Job {
	t.Helper()
	j, err := ParseJobFromPayload(p)
	if err != nil {
		t.Fatalf("ParseJobFromPayload(%s) = %v", p, err)
	}
	return j
}

// addJob adds a job, with more fields for the JSON of the job when they are given.
func addJob(t *testing.T, p string, fields ...string) *Job {
	t.Helper()
	if len(fields) > 0 {
		p = strings.TrimSuffix(p, "}") + "," + strings.Join(fields, ",") + "}"
	}
	j := parseJob(t, p)
	if _, err := Manager.AddJob(j); err != nil {
		t.Fatalf("AddJob = %v", err)
	}
	return j
}

func waitFor(t *testing.T, what string, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// runSync runs a job and waits for its run to end, which RunJobWithInput does on its own.
func runSync(t *testing.T, name string, input map[string]string, raw []byte) int {
	t.Helper()
	ids := make(chan int, 1)
	go func() { ids <- RunJobWithInput(name, input, raw) }()
	select {
	case id := <-ids:
		return id
	case <-time.After(15 * time.Second):
		t.Fatalf("the run of job %s did not end in time", name)
	}
	return 0
}

func latestRun(name string) *JobRunContext {
	Manager.lock.RLock()
	defer Manager.lock.RUnlock()
	var latest *JobRunContext
	for _, jr := range Manager.jobRuns[name] {
		if latest == nil || jr.id > latest.id {
			latest = jr
		}
	}
	return latest
}

func runCount(name string) int {
	Manager.lock.RLock()
	defer Manager.lock.RUnlock()
	return len(Manager.jobRuns[name])
}

func resultsOf(jr *JobRunContext) []*JobResult {
	jr.lock.RLock()
	defer jr.lock.RUnlock()
	return append([]*JobResult{}, jr.jobResults...)
}

// outputs is the text that a command job produced in its latest run. A command job also adds an empty result at the
// end of its last iteration.
func outputs(name string) []string {
	out := []string{}
	if jr := latestRun(name); jr != nil {
		for _, r := range resultsOf(jr) {
			if s, ok := r.Data.(string); ok && s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

func allOutputs(name string) []string {
	out := []string{}
	Manager.lock.RLock()
	runs := []*JobRunContext{}
	for _, jr := range Manager.jobRuns[name] {
		runs = append(runs, jr)
	}
	Manager.lock.RUnlock()
	for _, jr := range runs {
		for _, r := range resultsOf(jr) {
			if s, ok := r.Data.(string); ok && s != "" {
				out = append(out, s)
			}
		}
	}
	sort.Strings(out)
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestParseJobFromPayloadDefaults(t *testing.T) {
	j, err := ParseJobFromPayload(`{"name":"j","task":{"cmd":"echo","args":["{a}","x","{b}"]}}`)
	if err != nil {
		t.Fatal(err)
	}
	if j.Name != "j" || j.Count != 1 || j.KeepResults != 5 || j.MaxResults != 20 || j.Timeout != 10*time.Minute ||
		j.delayD != 10*time.Millisecond || j.initialDelayD != time.Second {
		t.Errorf("defaults = count %d keep %d max %d timeout %v delay %v initial %v", j.Count, j.KeepResults, j.MaxResults,
			j.Timeout, j.delayD, j.initialDelayD)
	}
	if j.commandTask == nil || j.httpTask != nil || j.commandTask.Cmd != "echo" || len(j.commandTask.Args) != 3 {
		t.Fatalf("task = command %+v http %+v", j.commandTask, j.httpTask)
	}
	if len(j.commandTask.fillers) != 2 || j.commandTask.fillers["{a}"] != 1 || j.commandTask.fillers["{b}"] != 1 {
		t.Errorf("fillers = %v, want {a} and {b}", j.commandTask.fillers)
	}
}

func TestParseJobFromPayloadKeepsWhatIsGiven(t *testing.T) {
	j := parseJob(t, `{"name":"j","delay":"50ms","initialDelay":"5ms","count":3,"keepResults":2,"maxResults":7,"keepFirst":true,
		"timeout":2000000000,"auto":true,"cron":"1h","outputTrigger":{"name":"o","forwardPayload":true},"finishTrigger":{"name":"f"},
		"task":{"script":"s1"}}`)
	if j.delayD != 50*time.Millisecond || j.initialDelayD != 5*time.Millisecond || j.Count != 3 || j.KeepResults != 2 ||
		j.MaxResults != 7 || !j.KeepFirst || j.Timeout != 2*time.Second || !j.Auto || j.Cron != "1h" {
		t.Errorf("job = %+v", j)
	}
	if j.OutputTrigger == nil || j.OutputTrigger.Name != "o" || !j.OutputTrigger.ForwardPayload || j.FinishTrigger == nil || j.FinishTrigger.Name != "f" {
		t.Errorf("triggers = %+v %+v", j.OutputTrigger, j.FinishTrigger)
	}
	if j.commandTask == nil || j.commandTask.Script != "s1" {
		t.Errorf("a task of a script must be a command task: %+v", j.commandTask)
	}
}

func TestParseJobFromPayloadHTTPTask(t *testing.T) {
	j := parseJob(t, `{"name":"j","task":{"name":"t1","url":"http://127.0.0.1:1/x","method":"post","requestCount":3,"parseJSON":true,
		"transforms":[{"payload":{"b":""},"mappings":[{"source":"a","target":"b","mode":"replace"}]}]}}`)
	if j.httpTask == nil || j.commandTask != nil {
		t.Fatalf("task = http %+v command %+v", j.httpTask, j.commandTask)
	}
	task := j.httpTask
	if task.Name != "t1" || task.URL != "http://127.0.0.1:1/x" || task.RequestCount != 3 || !task.ParseJSON {
		t.Errorf("http task = %+v", task.InvocationSpec)
	}
	if !task.CollectResponse || !task.TrackPayload {
		t.Error("a job needs the responses and their payloads to report them")
	}
	if len(task.Transforms) != 1 || len(task.Transforms[0].Mappings) != 1 {
		t.Errorf("transforms = %+v", task.Transforms)
	}
}

func TestParseJobFromPayloadErrors(t *testing.T) {
	for name, tc := range map[string]struct{ payload, want string }{
		"not json":          {`{"name":`, "Failed to parse json"},
		"no task":           {`{"name":"j"}`, "Invalid Task"},
		"empty task":        {`{"name":"j","task":{"foo":1}}`, "A command task must specify"},
		"a task of neither": {`{"name":"j","task":{"url":"http://x","requestCount":-1}}`, "HTTP Task Error"},
	} {
		t.Run(name, func(t *testing.T) {
			var j *Job
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("ParseJobFromPayload panicked: %v", r)
					}
				}()
				j, err = ParseJobFromPayload(tc.payload)
			}()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want one with %q", err, tc.want)
			}
			if j != nil && (j.httpTask != nil || j.commandTask != nil) {
				t.Errorf("a job that is refused must not carry a task: %+v", j)
			}
		})
	}
}

func TestAddJobReplacesAndReports(t *testing.T) {
	setup(t)
	name := jobName(t, "j")
	exists, err := Manager.AddJob(parseJob(t, payload(name, cmdTask("echo", "one"))))
	if err != nil || exists {
		t.Fatalf("AddJob of a new job = %v %v, want not existing", exists, err)
	}
	if runSync(t, name, nil, nil) != 1 {
		t.Fatal("the first run must have id 1")
	}
	if runCount(name) != 1 {
		t.Fatalf("runs = %d, want 1", runCount(name))
	}
	exists, err = Manager.AddJob(parseJob(t, payload(name, cmdTask("echo", "two"))))
	if err != nil || !exists {
		t.Fatalf("AddJob of an existing job = %v %v, want existing", exists, err)
	}
	if runCount(name) != 0 {
		t.Errorf("runs after the job was replaced = %d, want none", runCount(name))
	}
	if id := runSync(t, name, nil, nil); id != 1 {
		t.Errorf("run id of a replaced job = %d, want 1, the runs started over", id)
	}
	if got := outputs(name); !equal(got, []string{"two"}) {
		t.Errorf("outputs = %v, want the output of the replacement", got)
	}
}

func TestRunOfUnknownJob(t *testing.T) {
	setup(t)
	if id := RunJobWithInput("no-such-job", nil, nil); id != 0 {
		t.Errorf("run id of an unknown job = %d, want 0", id)
	}
	if RunJobWithInputAndWait("no-such-job", nil, nil, time.Second) {
		t.Error("waiting for an unknown job must not succeed")
	}
	if StopJob("no-such-job") {
		t.Error("an unknown job can't be stopped")
	}
	if res := GetLatestJobResults("no-such-job"); res != nil {
		t.Errorf("results of an unknown job = %v", res)
	}
	// a job that has no task to run
	name := jobName(t, "notask")
	Manager.AddJob(&Job{Name: name})
	if id := RunJobWithInput(name, nil, nil); id != 0 {
		t.Errorf("run id of a job without a task = %d, want 0", id)
	}
}

func TestGetJobsToRun(t *testing.T) {
	setup(t)
	a, b := jobName(t, "a"), jobName(t, "b")
	addJob(t, payload(a, cmdTask("echo", "a")))
	addJob(t, payload(b, cmdTask("echo", "b")))
	names, jobs := Manager.getJobsToRun([]string{a, "missing"})
	if len(jobs) != 1 || jobs[0].Name != a || len(names) != 2 {
		t.Errorf("named jobs = %v %v, want only %s", names, jobs, a)
	}
	names, jobs = Manager.getJobsToRun(nil)
	sort.Strings(names)
	want := []string{a, b}
	sort.Strings(want)
	if len(jobs) != 2 || !equal(names, want) {
		t.Errorf("all jobs = %v %v", names, jobs)
	}
}

func TestJobWatchers(t *testing.T) {
	setup(t)
	name := jobName(t, "j")
	addJob(t, payload(name, cmdTask("echo", "w")))
	type call struct {
		job   string
		run   int
		count int
	}
	calls := make(chan call, 10)
	Manager.AddJobWatcher(name, "w1", func(job string, runId int, results []*JobResult) {
		calls <- call{job, runId, len(results)}
	})
	other := make(chan call, 10)
	Manager.AddJobWatcher(name, "w2", func(job string, runId int, results []*JobResult) { other <- call{job, runId, len(results)} })
	runSync(t, name, nil, nil)
	for _, ch := range []chan call{calls, other} {
		select {
		case c := <-ch:
			if c.job != name || c.run != 1 || c.count != 2 {
				t.Errorf("watcher got %+v, want job %s run 1 with 2 results", c, name)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("a watcher of the job was not called when its run finished")
		}
	}
	Manager.RemoveJobWatcher(name, "w2")
	runSync(t, name, nil, nil)
	select {
	case c := <-calls:
		if c.run != 2 {
			t.Errorf("watcher got run %d, want 2", c.run)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the watcher that stayed was not called")
	}
	select {
	case c := <-other:
		t.Errorf("a watcher that was removed got %+v", c)
	case <-time.After(100 * time.Millisecond):
	}
	Manager.ClearJobWatchers(name)
	runSync(t, name, nil, nil)
	select {
	case c := <-calls:
		t.Errorf("a watcher that was cleared got %+v", c)
	case <-time.After(200 * time.Millisecond):
	}
	// removing what isn't there is harmless
	Manager.RemoveJobWatcher("unknown", "w")
	Manager.ClearJobWatchers("unknown")
}

func TestStoreJobScriptOrFile(t *testing.T) {
	setup(t)
	dir := t.TempDir()
	name, path, ok := Manager.StoreJobScriptOrFile(dir, "data.txt", []byte("some content"), false)
	if !ok || name != "data" || path != filepath.Join(dir, "data.txt") {
		t.Fatalf("StoreJobScriptOrFile = %q %q %v", name, path, ok)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "some content" {
		t.Errorf("stored file = %q %v", b, err)
	}
	if s := Manager.scripts["data"]; s == nil || s.Shell || s.Path != filepath.Join(dir, "data.txt") {
		t.Errorf("script = %+v", s)
	}
	if Manager.jobs["data"] != nil {
		t.Error("a file that isn't a script job must not get a job")
	}

	script := jobName(t, "script")
	scriptName, _, ok := Manager.StoreJobScriptOrFile(dir, script+".sh", []byte("echo script says $1\n"), true)
	if !ok || scriptName != script {
		t.Fatalf("StoreJobScriptOrFile of a script = %q %v", scriptName, ok)
	}
	if s := Manager.scripts[script]; s == nil || !s.Shell || s.Name != script {
		t.Errorf("script = %+v, want a shell script", s)
	}
	job := Manager.jobs[script]
	if job == nil || job.Count != 1 || job.KeepResults != 3 || job.commandTask == nil || job.commandTask.Script != script {
		t.Fatalf("job of the script = %+v", job)
	}
	job.initialDelayD = time.Millisecond
	job.delayD = time.Millisecond
	job.MaxResults = 10
	job.Timeout = 10 * time.Second
	job.commandTask.Args = []string{"hello"}
	runSync(t, script, nil, nil)
	if got := outputs(script); !equal(got, []string{"script says hello"}) {
		t.Errorf("output of the script job = %v", got)
	}
	// a script that isn't for a shell is run on its own
	plain := jobName(t, "plain")
	if _, _, ok := Manager.StoreJobScriptOrFile(dir, plain+".run", []byte("#!/bin/sh\necho plain $1\n"), true); !ok {
		t.Fatal("StoreJobScriptOrFile of an executable failed")
	}
	pj := Manager.jobs[plain]
	pj.initialDelayD, pj.delayD, pj.MaxResults, pj.Timeout = time.Millisecond, time.Millisecond, 10, 10*time.Second
	pj.commandTask.Args = []string{"direct"}
	runSync(t, plain, nil, nil)
	if got := outputs(plain); !equal(got, []string{"plain direct"}) {
		t.Errorf("output of the executable = %v", got)
	}
}

func TestStoreJobScriptOrFileFailsWhenTheFileCannotBeWritten(t *testing.T) {
	setup(t)
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	// the directory of the file is a file
	if name, path, ok := Manager.StoreJobScriptOrFile(blocker, "f.sh", []byte("echo"), true); ok || name != "" || path != "" {
		t.Errorf("StoreJobScriptOrFile = %q %q %v, want a failure", name, path, ok)
	}
	if len(Manager.scripts) != 0 || len(Manager.jobs) != 0 {
		t.Errorf("a failure must not leave scripts %v or jobs %v", Manager.scripts, Manager.jobs)
	}
}

func TestRunJobsAndStopJobsByName(t *testing.T) {
	setup(t)
	a, b := jobName(t, "a"), jobName(t, "b")
	addJob(t, payload(a, cmdTask("echo", "from-a")))
	addJob(t, payload(b, cmdTask("echo", "from-b")))
	RunJobs([]string{a})
	waitFor(t, "the run of the first job", 5*time.Second, func() bool { return runCount(a) == 1 && len(outputs(a)) == 1 })
	if runCount(b) != 0 {
		t.Errorf("runs of the job that wasn't named = %d", runCount(b))
	}
	RunJobs(nil)
	waitFor(t, "all the jobs to run", 5*time.Second, func() bool { return runCount(a) == 2 && len(outputs(b)) == 1 })
	StopJobs([]string{a, b, "unknown"})
}

func TestInitJobRunNeedsATask(t *testing.T) {
	jm := Manager
	if jm.initJobRun(nil, nil, nil, nil) != nil {
		t.Error("no job, no run")
	}
	if jm.initJobRun(&Job{Name: "x"}, nil, nil, nil) != nil {
		t.Error("a job without a task has no run")
	}
}
