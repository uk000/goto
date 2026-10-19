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
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunJobWithInputAndWait(t *testing.T) {
	setup(t)
	name := jobName(t, "j")
	addJob(t, payload(name, cmdTask("echo", "waited")))
	if !RunJobWithInputAndWait(name, nil, nil, 5*time.Second) {
		t.Error("a job that finished must be reported as finished")
	}
	if got := outputs(name); !equal(got, []string{"waited"}) {
		t.Errorf("outputs = %v", got)
	}
}

func TestWaitForJobOfARunThatIsOngoing(t *testing.T) {
	setup(t)
	name := jobName(t, "j")
	addJob(t, payload(name, shTask("sleep 0.3; echo done")))
	go RunJobWithInput(name, nil, nil)
	waitFor(t, "the run to start", 5*time.Second, func() bool { return latestRun(name) != nil })
	if !WaitForJob(name, 1, 10*time.Second) {
		t.Error("WaitForJob = false for a run that finished in time")
	}
	if got := outputs(name); !equal(got, []string{"done"}) {
		t.Errorf("outputs = %v", got)
	}
}

func TestWaitForJobTimesOut(t *testing.T) {
	setup(t)
	name := jobName(t, "j")
	addJob(t, payload(name, cmdTask("sleep", "30")))
	go RunJobWithInput(name, nil, nil)
	waitFor(t, "the run to start", 5*time.Second, func() bool { return latestRun(name) != nil })
	start := time.Now()
	if WaitForJob(name, 1, 150*time.Millisecond) {
		t.Error("WaitForJob = true for a run that is still going")
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("WaitForJob took %v, want it to give up at its timeout", took)
	}
	StopJob(name)
}

func TestWaitForJobOfAnUnknownRun(t *testing.T) {
	setup(t)
	name := jobName(t, "j")
	addJob(t, payload(name, cmdTask("echo", "x")))
	for _, tc := range []struct {
		job string
		run int
	}{{"no-such-job", 1}, {name, 99}} {
		got := make(chan bool, 1)
		panicked := make(chan any, 1)
		go func() {
			defer func() { panicked <- recover() }()
			got <- WaitForJob(tc.job, tc.run, 100*time.Millisecond)
		}()
		select {
		case p := <-panicked:
			if p != nil {
				t.Errorf("WaitForJob(%s, %d) panicked: %v", tc.job, tc.run, p)
			} else if <-got {
				t.Errorf("WaitForJob(%s, %d) = true for a run that doesn't exist", tc.job, tc.run)
			}
		case <-time.After(5 * time.Second):
			t.Errorf("WaitForJob(%s, %d) did not return", tc.job, tc.run)
		}
	}
}

func TestJobDelaysAreHonored(t *testing.T) {
	setup(t)
	name := jobName(t, "j")
	addJob(t, payload(name, cmdTask("echo", "d")), `"initialDelay":"200ms"`, `"delay":"100ms"`, `"count":3`)
	start := time.Now()
	runSync(t, name, nil, nil)
	if took := time.Since(start); took < 450*time.Millisecond {
		t.Errorf("run took %v, want at least the initial delay and a delay for each of the 3 iterations", took)
	}
	if got := outputs(name); len(got) != 3 {
		t.Errorf("outputs = %v, want 3", got)
	}
}

func TestAutoJobRunsWhenAdded(t *testing.T) {
	setup(t)
	name := jobName(t, "j")
	addJob(t, payload(name, cmdTask("echo", "auto")), `"auto":true`)
	waitFor(t, "the job to run on its own", 5*time.Second, func() bool { return len(outputs(name)) == 1 })
	if runCount(name) != 1 {
		t.Errorf("runs = %d, want 1", runCount(name))
	}
}

func TestCronJobRunsPeriodicallyUntilStopped(t *testing.T) {
	setup(t)
	name := jobName(t, "j")
	addJob(t, payload(name, cmdTask("echo", "tick")), `"cron":"100ms"`)
	waitFor(t, "the cron job to run 3 times", 10*time.Second, func() bool { return runCount(name) >= 3 })
	if !StopJob(name) {
		t.Fatal("StopJob of a cron job = false")
	}
	// a run that was going when the job was stopped may still add itself
	time.Sleep(300 * time.Millisecond)
	stoppedAt := runCount(name)
	time.Sleep(500 * time.Millisecond)
	if got := runCount(name); got != stoppedAt {
		t.Errorf("runs went from %d to %d after the cron job was stopped", stoppedAt, got)
	}
	// a stopped cron job is started again by running it
	RunJobs([]string{name})
	waitFor(t, "the cron job to run again", 10*time.Second, func() bool { return runCount(name) >= stoppedAt+2 })
}

func TestCronJobWithAnExpression(t *testing.T) {
	setup(t)
	name := jobName(t, "j")
	j := parseJob(t, payload(name, cmdTask("echo", "x"), `"cron":"* * * * *"`))
	if err := Manager.schedule(j); err != nil {
		t.Fatalf("a cron expression is a valid schedule: %v", err)
	}
	if j.cronJob == nil {
		t.Errorf("scheduled job = %+v, want a cron job", j)
	}
	Manager.cronScheduler.RemoveByTag(name)
}

func TestInvalidCronScheduleIsReported(t *testing.T) {
	setup(t)
	name := jobName(t, "j")
	j := parseJob(t, payload(name, cmdTask("echo", "x"), `"cron":"not a schedule"`))
	if err := Manager.schedule(j); err == nil {
		t.Error("an invalid schedule must be reported")
	}
	// the manager goes on to schedule the jobs that are valid
	ok := jobName(t, "ok")
	addJob(t, payload(ok, cmdTask("echo", "fine")), `"cron":"100ms"`)
	waitFor(t, "the valid cron job to run", 10*time.Second, func() bool { return runCount(ok) >= 1 })
}

func TestStoppingACronJobLeavesTheOtherCronJobsRunning(t *testing.T) {
	setup(t)
	a, b := jobName(t, "a"), jobName(t, "b")
	addJob(t, payload(a, cmdTask("echo", "a")), `"cron":"100ms"`)
	addJob(t, payload(b, cmdTask("echo", "b")), `"cron":"100ms"`)
	waitFor(t, "both cron jobs to run", 10*time.Second, func() bool { return runCount(a) >= 2 && runCount(b) >= 2 })
	StopJob(a)
	time.Sleep(300 * time.Millisecond)
	before := runCount(b)
	waitFor(t, "the other cron job to keep running", 10*time.Second, func() bool { return runCount(b) >= before+3 })
}

func TestStopJobOfAFinishedRun(t *testing.T) {
	setup(t)
	good, bad := jobName(t, "good"), jobName(t, "bad")
	addJob(t, payload(good, cmdTask("echo", "x")))
	addJob(t, payload(bad, cmdTask("/no/such/command-"+bad)))
	runSync(t, good, nil, nil)
	runSync(t, bad, nil, nil)
	for _, name := range []string{good, bad} {
		res := make(chan any, 1)
		ok := make(chan bool, 1)
		go func() {
			defer func() { res <- recover() }()
			ok <- StopJob(name)
		}()
		select {
		case p := <-res:
			if p != nil {
				t.Errorf("StopJob of %s panicked: %v", name, p)
			} else if !<-ok {
				t.Errorf("StopJob of %s = false for a job that has runs", name)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("StopJob of %s did not return", name)
		}
	}
	// the runs have ended, and a lock of one of them mustn't be held by what stopped the job
	for _, name := range []string{good, bad} {
		jr := latestRun(name)
		if !jr.lock.TryLock() {
			t.Errorf("the lock of the run of %s is still held", name)
		} else {
			jr.lock.Unlock()
		}
	}
}

func TestJobManagerKeepsNotifyingWatchersAfterItIsCleared(t *testing.T) {
	setup(t)
	name := jobName(t, "j")
	addJob(t, payload(name, cmdTask("echo", "x")))
	Manager.init()
	t.Cleanup(func() { Manager.init() })
	addJob(t, payload(name, cmdTask("echo", "x")))
	called := make(chan int, 4)
	Manager.AddJobWatcher(name, "w", func(job string, runId int, results []*JobResult) { called <- runId })
	runSync(t, name, nil, nil)
	select {
	case id := <-called:
		if id != 1 {
			t.Errorf("watcher got run %d, want 1", id)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a watcher added after the jobs were cleared is never called")
	}
}

func TestJobsConcurrently(t *testing.T) {
	setup(t)
	const jobs = 4
	names := make([]string, jobs)
	for i := range names {
		names[i] = jobName(t, "c")
		addJob(t, payload(names[i], cmdTask("echo", "c")), `"count":2`)
	}
	var wg sync.WaitGroup
	for w := 0; w < jobs; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 3; i++ {
				RunJobWithInput(names[w], nil, nil)
				Manager.getJobResults(names[w])
				Manager.getAllJobsResults()
				GetLatestJobResults(names[(w+1)%jobs])
				StopJob(names[(w+2)%jobs])
				Manager.getJobsToRun(nil)
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("concurrent use of the jobs did not finish in time")
	}
	for _, name := range names {
		if runCount(name) != 3 {
			t.Errorf("runs of %s = %d, want 3", name, runCount(name))
		}
		for _, r := range resultsOf(latestRun(name)) {
			if s, _ := r.Data.(string); s != "" && s != "c" {
				t.Errorf("result of %s = %q", name, s)
			}
		}
	}
	_ = strings.Join
}
