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

package results

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"goto/pkg/invocation"
)

// mustNotPanic reports a panic of f as a failure of the test, instead of taking the test binary down.
func mustNotPanic(t *testing.T, what string, f func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s panicked: %v", what, r)
		}
	}()
	f()
}

// The URIs are counted in lower case, but the time buckets of the URIs were looked up with the URI as it was requested,
// so a URI with upper case letters had no counts by time buckets.
func TestRegressionTimeBucketsOfUpperCaseURIs(t *testing.T) {
	tr := NewTargetResults("t1", nil, nil, [][]int{{0, 100}})
	addAll(tr, res{uri: "/Orders/List", status: 200, took: 10 * time.Millisecond},
		res{uri: "/orders/LIST", status: 200, took: 20 * time.Millisecond})
	uri := tr.CountsByURIs["/orders/list"]
	if uri == nil || uri.Count != 2 {
		t.Fatalf("URI counts = %s", toJSON(t, tr.CountsByURIs))
	}
	if b := uri.ByTimeBuckets["[0 100]"]; b == nil || b.Count != 2 || b.ByStatusCodes["200"].Count != 2 {
		t.Errorf("URI counts by time buckets = %s", toJSON(t, uri.ByTimeBuckets))
	}
}

// Results that were saved by a run without time buckets are loaded without the (empty) maps of time buckets, which
// were then written to when the next run tracked time buckets.
func TestRegressionLoadedResultsGetTimeBuckets(t *testing.T) {
	drainRegistryChannels(t)
	before := NewTargetResults("t1", nil, nil, nil)
	addAll(before, res{uri: "/a", status: 200, reqSize: 3, respSize: 4, retries: 1, retryReason: "x", errors: 1})
	content := toJSON(t, map[string]*TargetResults{"": NewTargetResults("", nil, nil, nil), "t1": before})
	if err := LoadTargetsResultsJSON(content); err != nil {
		t.Fatal(err)
	}
	tr, _ := targetsResults.getTargetResults("t1")
	tr.trackingTimeBuckets = [][]int{{0, 100}}
	mustNotPanic(t, "adding a result with time buckets to loaded results", func() {
		addAll(tr, res{uri: "/a", status: 200, reqSize: 3, respSize: 4, retries: 1, retryReason: "x", errors: 1, took: 10 * time.Millisecond})
	})
	if tr.InvocationCount != 2 {
		t.Errorf("InvocationCount = %d, want 2", tr.InvocationCount)
	}
	if b := tr.CountsByURIs["/a"].ByTimeBuckets["[0 100]"]; b == nil || b.Count != 1 {
		t.Errorf("URI counts by time buckets = %s", toJSON(t, tr.CountsByURIs["/a"]))
	}
	if b := tr.CountsByStatusCodes["200"].ByTimeBuckets["[0 100]"]; b == nil || b.Count != 1 {
		t.Errorf("status code counts by time buckets = %s", toJSON(t, tr.CountsByStatusCodes["200"]))
	}
	if b := tr.CountsByErrors["0"].ByTimeBuckets["[0 100]"]; b == nil || b.Count != 1 {
		t.Errorf("error counts by time buckets = %s", toJSON(t, tr.CountsByErrors["0"]))
	}
}

// Invocation results that were loaded had no lock, so the next invocation with an index that was loaded panicked.
func TestRegressionLoadedInvocationResultsCanBeUsed(t *testing.T) {
	drainRegistryChannels(t)
	ir := invocationsResults.getInvocation(7)
	ir.addResult(res{status: 200}.build())
	content := GetInvocationResultsJSON(false)
	ClearResults()
	if err := LoadInvocationResultsJSON(content); err != nil {
		t.Fatal(err)
	}
	mustNotPanic(t, "using loaded invocation results", func() {
		loaded := invocationsResults.getInvocation(7)
		loaded.addResult(res{status: 201}.build())
		loaded.finish()
		if len(loaded.Results) != 2 || !loaded.Finished {
			t.Errorf("loaded invocation results = %+v", loaded)
		}
	})
}

// Loading changes the results, which was done under a read lock.
func TestRegressionLoadResultsWhileReading(t *testing.T) {
	drainRegistryChannels(t)
	tr := NewTargetResults("t1", nil, nil, nil)
	addAll(tr, res{status: 200})
	targets := toJSON(t, map[string]*TargetResults{"": NewTargetResults("", nil, nil, nil), "t1": tr})
	inv := toJSON(t, map[uint32]*InvocationResults{1: {InvocationIndex: 1, Results: []*invocation.InvocationResult{res{}.build()}}})
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				GetTargetsResultsJSON(false)
				GetInvocationResultsJSON(false)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = targetsResults.Results
				targetsResults.lock.RLock()
				_ = len(targetsResults.Results)
				targetsResults.lock.RUnlock()
			}
		}
	}()
	for i := 0; i < 50; i++ {
		if err := LoadTargetsResultsJSON(targets); err != nil {
			t.Error(err)
		}
		if err := LoadInvocationResultsJSON(inv); err != nil {
			t.Error(err)
		}
	}
	close(stop)
	wg.Wait()
}

// The JSON of the results was made while the invocations that are running added to the results, which only hold the
// lock of their target results. This is a crash (concurrent map iteration and map write), not only a data race.
func TestRegressionTargetsResultsJSONWhileResultsAreAdded(t *testing.T) {
	drainRegistryChannels(t)
	tr, _ := targetsResults.getTargetResults("busy")
	tr.trackingHeaders = []string{"h"}
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 150; i++ {
				tr.addResult(res{uri: fmt.Sprintf("/u%d/%d", w, i), status: 200 + i%3, at: at(i),
					headers: map[string][]string{"h": {fmt.Sprint(i)}}}.build())
			}
		}(w)
	}
	for i := 0; i < 15; i++ {
		var decoded map[string]*TargetResults
		if err := json.Unmarshal([]byte(GetTargetsResultsJSON(false)), &decoded); err != nil {
			t.Errorf("invalid JSON: %v", err)
		}
	}
	wg.Wait()
	var decoded map[string]*TargetResults
	if err := json.Unmarshal([]byte(GetTargetsResultsJSON(true)), &decoded); err != nil || decoded["busy"].InvocationCount != 600 {
		t.Errorf("final results = %v, err %v", decoded["busy"], err)
	}
}

// The flags that tell which results to collect were plain bools, which the sinks read while the API changed them.
func TestRegressionCollectFlagsChangedWhileSinking(t *testing.T) {
	drainRegistryChannels(t)
	sink := ResultSinkFactory(&invocation.InvocationSpec{Name: "flags"}, nil, nil, nil)(newTracker(41))
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			sink(res{}.build())
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			EnableAllTargetResults(i%2 == 0)
			EnableInvocationResults(i%2 == 1)
		}
	}()
	wg.Wait()
	if tr := targetsResults.Results["flags"]; tr.InvocationCount != 200 {
		t.Errorf("InvocationCount = %d, want 200", tr.InvocationCount)
	}
}

// The target and the status of an invocation were set without a lock, while the results were being reported.
func TestRegressionSinkFactoryWhileInvocationsAreReported(t *testing.T) {
	drainRegistryChannels(t)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				GetInvocationResultsJSON(false)
			}
		}
	}()
	for i := 0; i < 100; i++ {
		spec := &invocation.InvocationSpec{Name: fmt.Sprintf("reported-%d", i%3)}
		ResultSinkFactory(spec, nil, nil, nil)(newTracker(uint32(i % 5)))
	}
	close(stop)
	wg.Wait()
}

// Two callers that asked for the results of the same invocation at once could be given results of their own, and one
// of them was then lost.
func TestRegressionGetInvocationConcurrentlyReturnsOneResults(t *testing.T) {
	drainRegistryChannels(t)
	const callers = 16
	for round := 0; round < 300; round++ {
		index := uint32(1000 + round)
		got := make([]*InvocationResults, callers)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for c := 0; c < callers; c++ {
			wg.Add(1)
			go func(c int) {
				defer wg.Done()
				<-start
				got[c] = invocationsResults.getInvocation(index)
			}(c)
		}
		close(start)
		wg.Wait()
		for c := 1; c < callers; c++ {
			if got[c] != got[0] {
				t.Fatalf("round %d: callers were given different results for the invocation %d", round, index)
			}
		}
	}
}

// Stopping the senders that were never started left the stop signals in the channel, which stopped the senders that
// were started later, and, as the senders were taken to be running, the results were queued with nobody to take them.
func TestRegressionStopRegistrySenderWithoutSenders(t *testing.T) {
	registrySendLock.Lock()
	prev := sendingToRegistry
	sendingToRegistry = false
	registrySendLock.Unlock()
	t.Cleanup(func() {
		registrySendLock.Lock()
		sendingToRegistry = prev
		registrySendLock.Unlock()
	})
	StopRegistrySender()
	if n := len(stopRegistrySender); n != 0 {
		t.Fatalf("%d stop signals are waiting for senders that don't exist", n)
	}
}

func TestRegressionSendersCanBeRestartedAfterStop(t *testing.T) {
	fr := newFakeRegistry(t)
	useRegistry(t, fr.srv.URL)
	withRegistrySenders(t)
	StopRegistrySender()
	waitForSendersToStop(t)
	registrySendLock.Lock()
	running := sendingToRegistry
	registrySendLock.Unlock()
	if running {
		t.Fatalf("senders are still taken to be running after they were stopped")
	}
	startRegistrySender()
	sink := ResultSinkFactory(&invocation.InvocationSpec{Name: "restarted"}, nil, nil, nil)(newTracker(51))
	sink(res{}.build())
	waitFor(t, "the results to be sent by the restarted senders", func() bool {
		_, ok := fr.find("/locker/store/client,restarted", func(string) bool { return true })
		return ok
	})
}

// The senders marked the invocation results to be sent under a read lock that they share, so they raced to change it.
func TestRegressionSendersMarkInvocationResultsOneAtATime(t *testing.T) {
	fr := newFakeRegistry(t)
	useRegistry(t, fr.srv.URL)
	withRegistrySenders(t)
	EnableInvocationResults(true)
	sink := ResultSinkFactory(&invocation.InvocationSpec{Name: "marked"}, nil, nil, nil)(newTracker(61))
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				sink(res{at: at(i)}.build())
			}
		}()
	}
	wg.Wait()
	waitFor(t, "all of the invocation results to be sent", func() bool {
		_, ok := fr.find("/locker/store/client,invocations,61", func(body string) bool {
			ir := &InvocationResults{}
			return json.Unmarshal([]byte(body), ir) == nil && len(ir.Results) == 400
		})
		return ok
	})
}

// The tracking is changed while the invocations of an earlier call, which are still running, add their results.
func TestRegressionTrackingChangedWhileResultsAreAdded(t *testing.T) {
	tr := NewTargetResults("t1", []string{"h"}, nil, [][]int{{0, 100}})
	all := NewTargetResults("", nil, nil, nil)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			tr.addResult(res{took: time.Millisecond, headers: map[string][]string{"h": {"v"}}}.build())
		}
		close(stop)
	}()
	for i := 0; ; i++ {
		select {
		case <-stop:
			wg.Wait()
			return
		default:
			processTrackingConfig(tr, all, []string{"h"}, map[string][]string{"h": {"g"}}, [][]int{{0, 100}})
		}
	}
}

// Merging a delta that has no results reset the timestamps of the results.
func TestRegressionEmptyDeltaKeepsTimestamps(t *testing.T) {
	merged := NewTargetResults("t1", []string{"h"}, nil, nil)
	addAll(merged, res{at: at(5), headers: map[string][]string{"h": {"v"}}})
	AddDeltaResults(merged, NewTargetResults("t1", nil, nil, nil), true)
	if !merged.FirstResultAt.Equal(at(5)) || !merged.LastResultAt.Equal(at(5)) {
		t.Errorf("first/last = %v/%v, want %v", merged.FirstResultAt, merged.LastResultAt, at(5))
	}
	// the same goes for counts that have no timestamps
	delta := &TargetResults{CountsByStatusCodes: KeyResult{"200": &KeyResultCounts{CountInfo: CountInfo{Count: 1}}}}
	AddDeltaResults(merged, delta, false)
	if c := merged.CountsByStatusCodes["200"]; c.Count != 2 || !c.FirstResultAt.Equal(at(5)) || !c.LastResultAt.Equal(at(5)) {
		t.Errorf("status code counts = %+v", c.CountInfo)
	}
}

func TestRegressionDeltaCanBeAddedToZeroResults(t *testing.T) {
	delta := NewTargetResults("t1", []string{"h"}, nil, [][]int{{0, 100}})
	addAll(delta, res{took: time.Millisecond, headers: map[string][]string{"h": {"v"}}})
	mustNotPanic(t, "adding a delta to results that were not initialised", func() {
		AddDeltaResults(&TargetResults{}, delta, true)
	})
}

// Counts that were loaded have no maps for what was empty when they were saved, which merging wrote to.
func TestRegressionDeltaCanBeAddedToLoadedResults(t *testing.T) {
	saved := NewTargetResults("t1", nil, nil, nil)
	addAll(saved, res{uri: "/a", reqSize: 3})
	loaded := roundTrip(t, saved)
	delta := NewTargetResults("t1", nil, nil, [][]int{{0, 100}})
	addAll(delta, res{uri: "/a", reqSize: 3, took: time.Millisecond})
	mustNotPanic(t, "adding a delta to loaded results", func() {
		AddDeltaResults(loaded, delta, true)
	})
	if loaded.InvocationCount != 2 || loaded.CountsByURIs["/a"].Count != 2 {
		t.Errorf("results = %s", toJSON(t, loaded))
	}
}
