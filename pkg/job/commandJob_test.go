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
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCommandJobStoresItsOutput(t *testing.T) {
	setup(t)
	name := jobName(t, "j")
	addJob(t, payload(name, shTask("echo first; echo second; echo error-line 1>&2")))
	if id := runSync(t, name, nil, nil); id != 1 {
		t.Fatalf("run id = %d, want 1", id)
	}
	jr := latestRun(name)
	results := resultsOf(jr)
	// the standard output and the standard error are both read, in no particular order, and then comes the final result
	if len(results) != 4 {
		t.Fatalf("results = %d, want 4 (3 lines and the final result)", len(results))
	}
	if got := allOutputs(name); !equal(got, []string{"error-line", "first", "second"}) {
		t.Errorf("outputs = %v", got)
	}
	last := results[len(results)-1]
	if last.Data != "" || !last.Last || !last.Finished || last.Stopped {
		t.Errorf("final result = %+v, want an empty result of a finished run", last)
	}
	for i, r := range results {
		if want := "1.1." + string(rune('1'+i)); r.ID != want {
			t.Errorf("result %d id = %q, want %q", i, r.ID, want)
		}
		if r.ResultTime.IsZero() {
			t.Errorf("result %d has no time", i)
		}
	}
	// a later run has an id of its own, and the results of the latest run are the ones that are reported
	if id := runSync(t, name, nil, nil); id != 2 {
		t.Fatalf("second run id = %d, want 2", id)
	}
	if latest := GetLatestJobResults(name); len(latest) != 4 || !strings.HasPrefix(latest[0].ID, "2.") {
		t.Errorf("latest results = %v", latest)
	}
	if runCount(name) != 2 {
		t.Errorf("runs = %d, want 2", runCount(name))
	}
}

func TestCommandJobCountRepeatsTheCommand(t *testing.T) {
	setup(t)
	name := jobName(t, "j")
	addJob(t, payload(name, cmdTask("echo", "again"), `"count":4`, `"keepResults":20`))
	runSync(t, name, nil, nil)
	results := resultsOf(latestRun(name))
	var lastFlags []bool
	iterations := map[string]int{}
	for _, r := range results {
		parts := strings.Split(r.ID, ".")
		iterations[parts[1]]++
		lastFlags = append(lastFlags, r.Last)
	}
	if len(iterations) != 4 {
		t.Errorf("iterations = %v, want 4 of them", iterations)
	}
	if got := outputs(name); !equal(got, []string{"again", "again", "again", "again"}) {
		t.Errorf("outputs = %v", got)
	}
	for i, r := range results {
		isLastIteration := strings.HasPrefix(r.ID[2:], "4.")
		if r.Last != isLastIteration {
			t.Errorf("result %d (%s) last = %v, want %v", i, r.ID, r.Last, isLastIteration)
		}
	}
}

func TestCommandJobKeepsTheLatestResults(t *testing.T) {
	setup(t)
	name := jobName(t, "j")
	addJob(t, payload(name, shTask("echo 1; echo 2; echo 3")), `"keepResults":2`)
	runSync(t, name, nil, nil)
	results := resultsOf(latestRun(name))
	if len(results) != 2 || results[0].Data != "3" || results[1].Data != "" {
		t.Errorf("results = %+v, want the last output and the final result", results)
	}
}

func TestCommandJobKeepsTheFirstResultOnRequest(t *testing.T) {
	setup(t)
	name := jobName(t, "j")
	addJob(t, payload(name, shTask("echo 1; echo 2; echo 3")), `"keepResults":3`, `"keepFirst":true`)
	runSync(t, name, nil, nil)
	results := resultsOf(latestRun(name))
	if len(results) != 3 || results[0].Data != "1" || results[1].Data != "3" || results[2].Data != "" {
		t.Errorf("results = %+v, want the first output, the last output and the final result", results)
	}
}

func TestCommandJobStopsAtMaxResults(t *testing.T) {
	setup(t)
	name := jobName(t, "j")
	addJob(t, payload(name, shTask("for i in 1 2 3 4 5 6 7 8; do echo $i; done")), `"maxResults":3`, `"keepResults":20`)
	runSync(t, name, nil, nil)
	jr := latestRun(name)
	got := outputs(name)
	if len(got) != 3 || got[0] != "1" || got[1] != "2" || got[2] != "3" {
		t.Errorf("outputs = %v, want the first 3", got)
	}
	jr.lock.RLock()
	stopped := jr.stopped
	jr.lock.RUnlock()
	if !stopped {
		t.Error("a job that went over its max results is stopped")
	}
}

func TestCommandJobTimeout(t *testing.T) {
	setup(t)
	name := jobName(t, "j")
	addJob(t, payload(name, cmdTask("sleep", "30")), `"timeout":300000000`)
	start := time.Now()
	runSync(t, name, nil, nil)
	if took := time.Since(start); took > 8*time.Second {
		t.Errorf("run took %v, want it stopped at the timeout", took)
	}
	jr := latestRun(name)
	jr.lock.RLock()
	stopped := jr.stopped
	jr.lock.RUnlock()
	if !stopped {
		t.Error("a job that timed out is stopped")
	}
}

func TestCommandJobStop(t *testing.T) {
	setup(t)
	name := jobName(t, "j")
	addJob(t, payload(name, cmdTask("sleep", "30")))
	ended := make(chan struct{})
	go func() {
		RunJobWithInput(name, nil, nil)
		close(ended)
	}()
	waitFor(t, "the run to start", 5*time.Second, func() bool { return latestRun(name) != nil })
	if !StopJob(name) {
		t.Fatal("StopJob of a running job = false")
	}
	select {
	case <-ended:
	case <-time.After(10 * time.Second):
		t.Fatal("the run did not end after the job was stopped")
	}
	jr := latestRun(name)
	jr.lock.RLock()
	stopped := jr.stopped
	jr.lock.RUnlock()
	if !stopped {
		t.Error("a job that was stopped is marked stopped")
	}
}

func TestCommandJobThatCannotStart(t *testing.T) {
	setup(t)
	name := jobName(t, "j")
	addJob(t, payload(name, cmdTask("/no/such/command-"+name)))
	runSync(t, name, nil, nil)
	results := resultsOf(latestRun(name))
	if len(results) != 1 {
		t.Fatalf("results = %+v, want the one that reports the failure", results)
	}
	if msg, _ := results[0].Data.(string); !strings.Contains(msg, "failed to execute command") || !strings.Contains(msg, name) {
		t.Errorf("result = %q, want the failure to be reported", msg)
	}
}

func TestCommandJobMarkersFillTheArgs(t *testing.T) {
	setup(t)
	name := jobName(t, "j")
	addJob(t, payload(name, cmdTask("echo", "{who}", "says", "{what}", "{unset}")))
	runSync(t, name, map[string]string{"{who}": "alice", "{what}": "hi"}, nil)
	if got := outputs(name); !equal(got, []string{"alice says hi {unset}"}) {
		t.Errorf("outputs = %v, want the markers that were given to be replaced", got)
	}
}

func TestCommandJobOutputTriggersAnotherJobWithMarkers(t *testing.T) {
	setup(t)
	source, target := jobName(t, "source"), jobName(t, "target")
	addJob(t, payload(target, cmdTask("echo", "got:{x}-{y}")))
	addJob(t, payload(source, shTask("echo alpha beta; echo gamma delta"),
		`"outputTrigger":{"name":"`+target+`"}`))
	// the markers are named in the task of the source job
	src := Manager.jobs[source]
	src.commandTask.OutputMarkers = map[int]string{1: "x", 2: "y"}
	runSync(t, source, nil, nil)
	waitFor(t, "the triggered job to run for each line", 5*time.Second, func() bool { return len(allOutputs(target)) == 2 })
	if got := allOutputs(target); !equal(got, []string{"got:alpha-beta", "got:gamma-delta"}) {
		t.Errorf("outputs of the triggered job = %v", got)
	}
}

func TestCommandJobMarkersCarryOverToSuccessors(t *testing.T) {
	setup(t)
	first, second, third := jobName(t, "first"), jobName(t, "second"), jobName(t, "third")
	addJob(t, payload(third, cmdTask("echo", "{x}:{z}")))
	addJob(t, payload(second, cmdTask("echo", "{x}-{y}"), `"outputTrigger":{"name":"`+third+`"}`))
	addJob(t, payload(first, cmdTask("echo", "alpha", "beta"), `"outputTrigger":{"name":"`+second+`"}`))
	Manager.jobs[first].commandTask.OutputMarkers = map[int]string{1: "x", 2: "y"}
	Manager.jobs[second].commandTask.OutputMarkers = map[int]string{1: "z"}
	Manager.jobs[second].commandTask.OutputSeparator = "-"
	runSync(t, first, nil, nil)
	waitFor(t, "the third job", 5*time.Second, func() bool { return len(allOutputs(third)) == 1 })
	if got := allOutputs(second); !equal(got, []string{"alpha-beta"}) {
		t.Errorf("outputs of the second job = %v", got)
	}
	// the third job uses a marker of the first job, and one of the second
	if got := allOutputs(third); !equal(got, []string{"alpha:alpha"}) {
		t.Errorf("outputs of the third job = %v, want the markers of both ancestors", got)
	}
}

func TestPrepareCommandMarkers(t *testing.T) {
	jr := &JobRunContext{markers: map[string]string{"{inherited}": "yes"}}
	task := &CommandJobTask{OutputMarkers: map[int]string{1: "a", 3: "c"}, OutputSeparator: ","}
	got := prepareCommandMarkers("one,two,three,four", task, jr)
	want := map[string]string{"{inherited}": "yes", "{a}": "one", "{c}": "three"}
	if len(got) != len(want) {
		t.Fatalf("markers = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("marker %s = %q, want %q", k, got[k], v)
		}
	}
	// the separator is a space unless given
	task = &CommandJobTask{OutputMarkers: map[int]string{2: "second"}}
	if got := prepareCommandMarkers("a b c", task, &JobRunContext{}); got["{second}"] != "b" || len(got) != 1 {
		t.Errorf("markers = %v", got)
	}
	// a job that doesn't name any markers passes none
	if got := prepareCommandMarkers("a b c", &CommandJobTask{}, jr); len(got) != 0 {
		t.Errorf("markers = %v, want none", got)
	}
}

func TestCommandJobFinishTriggersAnotherJob(t *testing.T) {
	setup(t)
	first, second := jobName(t, "first"), jobName(t, "second")
	addJob(t, payload(second, cmdTask("echo", "second ran")))
	addJob(t, payload(first, cmdTask("echo", "first ran"), `"finishTrigger":{"name":"`+second+`"}`))
	runSync(t, first, nil, nil)
	if got := outputs(second); !equal(got, []string{"second ran"}) {
		t.Errorf("outputs of the job that the finish triggered = %v", got)
	}
	if got := outputs(first); !equal(got, []string{"first ran"}) {
		t.Errorf("outputs of the first job = %v", got)
	}
}

// A script job finds its script among the ones that the manager stores, which are added by other calls while jobs are running.
// Reading a map that another call is writing is a fatal error of the runtime, which takes the whole server down.
func TestScriptJobRunsWhileScriptsAreStored(t *testing.T) {
	const runners, writers, rounds = 4, 3, 12
	setup(t)
	dir := t.TempDir()
	script := jobName(t, "script")
	if _, _, ok := Manager.StoreJobScriptOrFile(dir, script+".sh", []byte("echo running $1\n"), true); !ok {
		t.Fatal("StoreJobScriptOrFile of a script failed")
	}
	job := Manager.jobs[script]
	job.initialDelayD, job.delayD, job.MaxResults, job.Timeout = time.Millisecond, time.Millisecond, 10, 10*time.Second
	job.commandTask.Args = []string{"x"}

	start := make(chan struct{})
	var wg sync.WaitGroup
	for r := 0; r < runners; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < rounds; i++ {
				RunJobWithInput(script, nil, nil)
			}
		}()
	}
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			for i := 0; i < rounds*4; i++ {
				if _, _, ok := Manager.StoreJobScriptOrFile(dir, fmt.Sprintf("%s-%d-%d.sh", script, w, i), []byte("echo other\n"), false); !ok {
					t.Errorf("StoreJobScriptOrFile of script %d-%d failed", w, i)
				}
				Manager.getScripts()
			}
		}(w)
	}
	close(start)
	ended := make(chan struct{})
	go func() { wg.Wait(); close(ended) }()
	select {
	case <-ended:
	case <-time.After(60 * time.Second):
		t.Fatal("running the script job while scripts are stored did not end in time")
	}

	got := allOutputs(script)
	if len(got) != runners*rounds {
		t.Fatalf("outputs = %d, want %d: one for each run", len(got), runners*rounds)
	}
	for _, out := range got {
		if out != "running x" {
			t.Fatalf("output = %q, want the output of the script", out)
		}
	}
}
