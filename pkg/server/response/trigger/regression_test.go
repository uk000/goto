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

package trigger

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goto/pkg/util"

	"github.com/gorilla/mux"
)

// noPanic runs f, and reports a panic as a failure of the test, so that a panic does not take the other tests with it.
func noPanic(t *testing.T, what string, f func()) {
	t.Helper()
	defer func() {
		if p := recover(); p != nil {
			t.Errorf("%s panicked: %v", what, p)
		}
	}()
	f()
}

// A URI that is given twice lists the target twice for that URI. Removing the target, which is what replacing it
// does first, used to panic on the second entry, since it removed entries from the slice that it was ranging over.
func TestRegressionTargetWithRepeatedURIIsReplacedAndRemoved(t *testing.T) {
	e := newTestEnv(t)
	name := e.name("dup")
	spec := pipeSpec(name, map[string]any{"triggerURIs": []string{"/dup", "/dup", "/DUP"}})
	e.add(t, spec)
	noPanic(t, "replacing a target that has a repeated URI", func() { e.add(t, spec) })
	v := e.view(t)
	if len(v.Targets) != 1 || len(v.TargetsByURIs["/dup"]) != 1 {
		t.Errorf("triggers = %+v, want the target listed once for its URI", v)
	}
	noPanic(t, "removing a target that has a repeated URI", func() {
		e.mustCall(t, "POST", "/server/response/triggers/"+name+"/remove", "", http.StatusOK)
	})
	if v := e.view(t); len(v.Targets) != 0 || len(v.TargetsByURIs) != 0 {
		t.Errorf("triggers = %+v after removal, want nothing left of the target", v)
	}
}

// The slices of names that the headers of targets are listed in used to be shortened in place without being stored
// back, so that removing a target left the one after it listed twice.
func TestRegressionRemovingTargetKeepsOthersOfSameHeaderListedOnce(t *testing.T) {
	e := newTestEnv(t)
	a, b, c := e.name("a"), e.name("b"), e.name("c")
	for _, name := range []string{a, b, c} {
		e.add(t, pipeSpec(name, map[string]any{"triggerHeaders": map[string]string{"X-Env": "prod"}}))
	}
	e.mustCall(t, "POST", "/server/response/triggers/"+b+"/remove", "", http.StatusOK)
	v := e.view(t)
	if got := v.TargetsByHeaders["X-Env"]["prod"]; !reflect.DeepEqual(got, []string{a, c}) {
		t.Errorf("targets of X-Env=prod = %v, want [%s %s]", got, a, c)
	}
	e.mustCall(t, "POST", "/server/response/triggers/"+c+"/remove", "", http.StatusOK)
	e.mustCall(t, "POST", "/server/response/triggers/"+a+"/remove", "", http.StatusOK)
	if v := e.view(t); len(v.TargetsByHeaders) != 0 {
		t.Errorf("targets by headers = %v after removing every target, want none", v.TargetsByHeaders)
	}
}

// A target that has statuses and headers fires for the statuses that it has, when the headers match too. It used to
// be listed by its headers only, and so it fired for any status.
func TestRegressionTargetWithStatusesAndHeadersNeedsBoth(t *testing.T) {
	e := newTestEnv(t)
	name := e.name("both")
	e.add(t, pipeSpec(name, map[string]any{"triggerStatuses": []int{500}, "triggerHeaders": map[string]string{"X-Env": "prod"}}))
	if _, pipes := e.fired("/anything", 200, "X-Env", "prod"); len(pipes) != 0 {
		t.Errorf("target fired for status 200 = %v, want it to fire for 500 only", pipes)
	}
	if _, pipes := e.fired("/anything", 500, "X-Env", "prod"); !reflect.DeepEqual(pipes, []string{name}) {
		t.Errorf("target fired for status 500 with the header = %v, want %s", pipes, name)
	}
}

// The action is in the last segment of the path. It used to be looked for in the whole URI, so that a trigger that
// has "enable" in its name, or a query that has, could not be disabled.
func TestRegressionTriggerWithEnableInNameCanBeDisabled(t *testing.T) {
	e := newTestEnv(t)
	name := e.name("enable-me")
	e.add(t, pipeSpec(name, nil))
	msg := e.mustCall(t, "POST", "/server/response/triggers/"+name+"/disable", "", http.StatusOK)
	if !strings.Contains(msg, "disabled") || e.view(t).Targets[name].Enabled {
		t.Errorf("after disabling: message %q, enabled %v, want it disabled", msg, e.view(t).Targets[name].Enabled)
	}
	e.mustCall(t, "POST", "/server/response/triggers/"+name+"/enable", "", http.StatusOK)
	e.mustCall(t, "POST", "/server/response/triggers/"+name+"/disable?why=enable", "", http.StatusOK)
	if e.view(t).Targets[name].Enabled {
		t.Errorf("target is enabled after it was disabled by a call that has enable in its query")
	}
}

// Targets that have no name can't be addressed by any of the calls, and URIs that can't be matched would leave a
// target that never fires. Both used to be added.
func TestRegressionAddTriggerRefusesTargetsThatCanNotWork(t *testing.T) {
	e := newTestEnv(t)
	for name, body := range map[string]string{
		"no name":     `{"pipe": true, "enabled": true}`,
		"invalid URI": `{"name":"x","pipe":true,"enabled":true,"triggerURIs":["/a/{b"]}`,
	} {
		rr := e.call("POST", "/server/response/triggers/add", body)
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Invalid trigger") {
			t.Errorf("%s: POST /triggers/add = %d %q, want 400 Invalid trigger", name, rr.Code, rr.Body.String())
		}
	}
	if v := e.view(t); len(v.Targets) != 0 {
		t.Errorf("targets = %v, want none", v.Targets)
	}
}

// Adding a target builds the matcher of its URIs. That used to add a route to the router that serves all requests,
// for every URI, and the routes stay there after the target is removed.
func TestRegressionAddingTargetDoesNotAddRoutesToServerRouter(t *testing.T) {
	e := newTestEnv(t)
	if util.MatchRouter == nil {
		t.Fatal("the router that matches requests is not set up")
	}
	count := func() int {
		n := 0
		util.MatchRouter.Walk(func(*mux.Route, *mux.Router, []*mux.Route) error { n++; return nil })
		return n
	}
	before := count()
	e.add(t, pipeSpec(e.name("routes"), map[string]any{"triggerURIs": []string{"/a", "/b/{id}"}}))
	if after := count(); after != before {
		t.Errorf("routes of the router that matches requests: %d before and %d after adding a target with URIs, want no change", before, after)
	}
}

// Targets are listed with the settings that the API gets them with. Callbacks that pipes register are not one of
// them, and listing a target that has some used to fail, with nothing in the response.
func TestRegressionListingTriggersWithPipeCallbacks(t *testing.T) {
	e := newTestEnv(t)
	name := e.name("listed")
	e.add(t, pipeSpec(name, nil))
	RegisterPipeCallback(name, "source", recordPipe(make(chan pipeCall, 1)))
	body := e.mustCall(t, "GET", "/server/response/triggers", "", http.StatusOK)
	if !json.Valid([]byte(body)) || !strings.Contains(body, name) {
		t.Errorf("GET /triggers = %q, want the JSON of the triggers, with a target that has callbacks", body)
	}
	if strings.Contains(body, "PipeCallbacks") {
		t.Errorf("GET /triggers = %q, want no callbacks in it", body)
	}
}

// A target that is both invoked and piped is triggered once per response, and it used to be counted for each.
func TestRegressionTargetWithHTTPTargetAndPipeIsCountedOncePerTrigger(t *testing.T) {
	e := newTestEnv(t)
	ts := newTargetServer(t)
	name := e.name("both")
	e.add(t, targetSpec(name, ts.url("/x"), map[string]any{"pipe": true, "triggerStatuses": []int{500}}))
	calls := make(chan pipeCall, 2)
	RegisterPipeCallback(name, "source", recordPipe(calls))
	RunTriggers(e.newRequest("GET", "/traffic", ""), httptest.NewRecorder(), 500)
	ts.waitFor(t, 1)
	select {
	case <-calls:
	case <-time.After(5 * time.Second):
		t.Fatalf("callback was not called")
	}
	eventually(t, "the target to be counted", func() bool { return e.view(t).Targets[name].TriggerCount >= 1 })
	time.Sleep(100 * time.Millisecond)
	if got := e.view(t).Targets[name].TriggerCount; got != 1 {
		t.Errorf("trigger count = %d, want 1 for one trigger", got)
	}
}

// The first requests that arrive together for a port that has no triggers used to each make a Trigger for the port,
// of which only the last stayed, and the targets that had been added to the others were lost.
func TestRegressionConcurrentFirstRequestsShareOneTrigger(t *testing.T) {
	e := newTestEnv(t)
	const requests = 64
	reqs := make([]*http.Request, requests)
	for i := range reqs {
		reqs[i] = e.newRequest("GET", "/server/response/triggers", "")
	}
	got := make([]*Trigger, requests)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range reqs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			got[i] = getPortTrigger(reqs[i])
		}(i)
	}
	close(start)
	wg.Wait()
	for i := range got {
		if got[i] != got[0] {
			t.Fatalf("request %d got another Trigger than request 0, want the port to have one", i)
		}
	}
}

// The counts of a target are kept under the lock of the target. Counts used to be kept under no lock when the target
// was piped, and in the lock of the port when the target was enabled or disabled, so that concurrent responses lost
// counts, and listing the targets while they were counted was a race.
func TestRegressionConcurrentRunTriggersCountExactly(t *testing.T) {
	e := newTestEnv(t)
	name := e.name("count")
	e.add(t, pipeSpec(name, map[string]any{"triggerStatuses": []int{200}}))
	var called atomic.Int32
	RegisterPipeCallback(name, "source", func(string, string, int, *http.Request, int, http.Header) { called.Add(1) })
	const runs, readers = 200, 8
	reqs := make([]*http.Request, runs)
	for i := range reqs {
		reqs[i] = e.newRequest("GET", "/traffic", "")
	}
	reads := make([]*http.Request, readers)
	for i := range reads {
		reads[i] = e.newRequest("GET", "/server/response/triggers", "")
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range reqs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			RunTriggers(reqs[i], httptest.NewRecorder(), 200)
		}(i)
	}
	// The triggers are listed while they run.
	for i := range reads {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			for j := 0; j < 20; j++ {
				api.ServeHTTP(httptest.NewRecorder(), reads[i])
			}
		}(i)
	}
	close(start)
	wg.Wait()
	eventually(t, "the callbacks", func() bool { return int(called.Load()) >= runs })
	eventually(t, "the count of triggers", func() bool { return e.view(t).Targets[name].TriggerCount >= runs })
	time.Sleep(50 * time.Millisecond)
	v := e.view(t).Targets[name]
	if got := int(called.Load()); got != runs || v.MatchCount != runs || v.TriggerCount != runs {
		t.Errorf("callbacks = %d, match count = %d, trigger count = %d, want %d of each", got, v.MatchCount, v.TriggerCount, runs)
	}
}

// Targets are added, removed and listed, and responses are matched to them, all together.
func TestRegressionConcurrentChangesListingAndMatching(t *testing.T) {
	e := newTestEnv(t)
	const workers, rounds = 4, 30
	serve := func(req *http.Request) { api.ServeHTTP(httptest.NewRecorder(), req) }
	var adds, removes []*http.Request
	var reads, runs []*http.Request
	for w := 0; w < workers; w++ {
		for i := 0; i < rounds; i++ {
			name := e.name(fmt.Sprintf("w%d-%d", w, i))
			body := fmt.Sprintf(`{"name":%q,"pipe":true,"enabled":true,"triggerStatuses":[500],"triggerHeaders":{"X-Env":"prod"}}`, name)
			adds = append(adds, e.newRequest("POST", "/server/response/triggers/add", body))
			removes = append(removes, e.newRequest("POST", "/server/response/triggers/"+name+"/remove", ""))
			reads = append(reads, e.newRequest("GET", "/server/response/triggers", ""))
			runs = append(runs, e.newRequest("GET", "/traffic", "", "X-Env", "prod"))
		}
	}
	var counts, pipes []*http.Request
	for i := 0; i < rounds; i++ {
		counts = append(counts, e.newRequest("GET", "/server/response/triggers/counts", ""))
		pipes = append(pipes, e.newRequest("GET", "/server/response/triggers/pipes", ""))
	}
	codes := make([]int, len(adds)+len(removes))
	var wg sync.WaitGroup
	start := make(chan struct{})
	for w := 0; w < workers; w++ {
		wg.Add(4)
		go func(w int) {
			defer wg.Done()
			<-start
			for i := w * rounds; i < (w+1)*rounds; i++ {
				rr := httptest.NewRecorder()
				api.ServeHTTP(rr, adds[i])
				codes[i] = rr.Code
				rr = httptest.NewRecorder()
				api.ServeHTTP(rr, removes[i])
				codes[len(adds)+i] = rr.Code
			}
		}(w)
		go func(w int) {
			defer wg.Done()
			<-start
			for i := w * rounds; i < (w+1)*rounds; i++ {
				serve(reads[i])
			}
		}(w)
		go func(w int) {
			defer wg.Done()
			<-start
			for i := w * rounds; i < (w+1)*rounds; i++ {
				RunTriggers(runs[i], httptest.NewRecorder(), 500)
			}
		}(w)
		go func(w int) {
			defer wg.Done()
			<-start
			if w == 0 {
				for i := range counts {
					serve(counts[i])
					serve(pipes[i])
				}
			}
		}(w)
	}
	close(start)
	wg.Wait()
	for i, code := range codes {
		if code != http.StatusOK {
			t.Errorf("call %d = %d, want 200", i, code)
		}
	}
	v := e.view(t)
	if len(v.Targets) != 0 || len(v.TargetsByStatus) != 0 || len(v.TargetsByHeaders) != 0 {
		t.Errorf("triggers = %+v after every target was removed, want none", v)
	}
}

// The targets are invoked after the response is on its way, while the handler goes on with the response. The headers
// that they are given used to be read from the response writer in the background, together with the handler's
// changes of them.
func TestRegressionRunTriggersGivesTargetsACopyOfResponseHeaders(t *testing.T) {
	e := newTestEnv(t)
	ts := newTargetServer(t)
	name := e.name("copy")
	e.add(t, map[string]any{
		"name": name, "enabled": true, "triggerStatuses": []int{500},
		"httpTarget": map[string]any{
			"method": "GET", "url": ts.url("/x"), "headers": map[string]string{"X-From-Response": "{X-Upstream}"},
		},
	})
	rr := httptest.NewRecorder()
	rr.Header().Set("X-Upstream", "u1")
	RunTriggers(e.newRequest("GET", "/traffic", ""), rr, 500)
	// The handler goes on, and changes the headers while the target is invoked.
	for i := 0; ts.count() == 0 && i < 200000; i++ {
		rr.Header().Set("X-Upstream", "changed-"+strconv.Itoa(i))
		rr.Header().Set("X-Other", strconv.Itoa(i))
	}
	hits := ts.waitFor(t, 1)
	if got := hits[0].Header.Get("X-From-Response"); got != "u1" {
		t.Errorf("X-From-Response = %q, want the value that the response header had when the response was handled", got)
	}
}
