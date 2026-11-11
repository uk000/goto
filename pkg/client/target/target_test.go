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

package target

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/invocation"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

// A target pushed by the registry usually has no headers, and AddTarget adds the Goto headers to it.
func TestAddTargetWithoutHeaders(t *testing.T) {
	tc := NewTargetClient(0)
	payload := `{"name":"t1","method":"GET","url":"http://localhost:1/status/200","replicas":1,"requestCount":1,"autoInvoke":false}`
	target := &Target{InvocationSpec: &invocation.InvocationSpec{}}
	if err := util.ReadJson(payload, target); err != nil {
		t.Fatalf("failed to read target: %s", err.Error())
	}
	if target.Headers != nil {
		t.Fatalf("expected the target to have no headers, got %v", target.Headers)
	}
	if err := tc.AddTarget(target); err != nil {
		t.Fatalf("AddTarget failed: %s", err.Error())
	}
	if _, present := target.Headers[constants.HeaderFromGoto]; !present {
		t.Errorf("expected header %s on the target, got %v", constants.HeaderFromGoto, target.Headers)
	}
	if tc.PrepareTarget("t1") == nil {
		t.Errorf("target t1 was not added")
	}
}

func newTestTarget(name, url string) *Target {
	return &Target{InvocationSpec: &invocation.InvocationSpec{Name: name, URL: url, Replicas: 1, RequestCount: 1}}
}

func TestGetTargetsPayload(t *testing.T) {
	tc := NewTargetClient(0)
	for _, name := range []string{"payload1", "payload2"} {
		if err := tc.AddTarget(newTestTarget(name, "http://localhost:1/")); err != nil {
			t.Fatalf("AddTarget(%s) failed: %s", name, err.Error())
		}
	}

	payload, found := tc.getTargetsPayload("")
	var all map[string]*invocation.InvocationSpec
	if err := json.Unmarshal([]byte(payload), &all); !found || err != nil || len(all) != 2 || all["payload1"] == nil || all["payload2"] == nil {
		t.Errorf("all targets = (%q, %v), want both targets as a JSON object (error: %v)", payload, found, err)
	}
	if all["payload1"] != nil {
		if _, present := all["payload1"].Headers[constants.HeaderFromGoto]; !present {
			t.Errorf("headers of the target = %v, want %s among them", all["payload1"].Headers, constants.HeaderFromGoto)
		}
	}

	payload, found = tc.getTargetsPayload("payload2")
	var one invocation.InvocationSpec
	if err := json.Unmarshal([]byte(payload), &one); !found || err != nil || one.Name != "payload2" {
		t.Errorf("target payload2 = (%q, %v), want the target as a JSON object (error: %v)", payload, found, err)
	}

	if payload, found = tc.getTargetsPayload("missing"); found || payload != "" {
		t.Errorf("missing target = (%q, %v), want (\"\", false)", payload, found)
	}
}

func TestGetTrackingConfig(t *testing.T) {
	tc := NewTargetClient(0)
	tc.AddTrackingHeaders("X-A,x-b|x-c")
	if !tc.AddTrackingTimeBuckets("1-100,100-1000") {
		t.Fatal("time buckets were rejected")
	}
	headers, crossHeaders, buckets := tc.getTrackingConfig()
	if !reflect.DeepEqual(headers, []string{"x-a", "x-b", "x-c"}) || !reflect.DeepEqual(crossHeaders, map[string][]string{"x-b": {"x-c"}}) ||
		!reflect.DeepEqual(buckets, [][]int{{1, 100}, {100, 1000}}) || !reflect.DeepEqual(tc.getTrackingTimeBuckets(), buckets) {
		t.Errorf("tracking config = (%v, %v, %v), want the headers, cross headers and time buckets that were set", headers, crossHeaders, buckets)
	}
	tc.clearTrackingHeaders()
	tc.clearTrackingTimeBuckets()
	if headers, crossHeaders, buckets = tc.getTrackingConfig(); len(headers) != 0 || len(crossHeaders) != 0 || len(buckets) != 0 {
		t.Errorf("tracking config after clearing = (%v, %v, %v), want none", headers, crossHeaders, buckets)
	}
}

// The targets are reported, added, removed and cleared by API calls that are served concurrently. Reporting a map that another
// call is updating is a fatal error of the runtime, which takes the whole server down.
func TestTargetsAPIWhileTargetsAreAddedAndCleared(t *testing.T) {
	const workers, rounds = 6, 80
	root := mux.NewRouter()
	SetRoutes(root.PathPrefix("/client").Subrouter())
	// logging the calls needs what the server sets up for the requests that it serves
	logs := global.Flags.EnableClientLogs
	global.Flags.EnableClientLogs = false
	t.Cleanup(func() {
		global.Flags.EnableClientLogs = logs
		Client.init()
	})
	call := func(method, uri, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		root.ServeHTTP(w, httptest.NewRequest(method, uri, strings.NewReader(body)))
		return w
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			for i := 0; i < rounds; i++ {
				switch w % 3 {
				case 0:
					// a few names that are added again and again, so that targets are replaced while they are reported
					body := fmt.Sprintf(`{"name":"api-%d","url":"http://localhost:1/","headers":{"x-a":"1"},"autoInvoke":false}`, i%4)
					if r := call("POST", "/client/targets/add", body); r.Code != http.StatusOK {
						t.Errorf("adding a target = %d %s, want 200", r.Code, r.Body.String())
					}
				case 1:
					if r := call("GET", "/client/targets", ""); r.Code != http.StatusOK || !json.Valid(r.Body.Bytes()) {
						t.Errorf("reporting the targets = %d %q, want 200 and JSON", r.Code, r.Body.String())
					}
					if r := call("GET", "/client/targets/api-1", ""); !json.Valid(r.Body.Bytes()) {
						t.Errorf("reporting a target = %d %q, want JSON", r.Code, r.Body.String())
					}
				default:
					call("POST", "/client/targets/api-2,api-3/remove", "")
					if i%10 == 0 {
						call("POST", "/client/targets/clear", "")
					}
					call("POST", fmt.Sprintf("/client/track/headers/h%d,h2", i%3), "")
					call("POST", "/client/track/time/1-100,100-200", "")
					if r := call("GET", "/client/track/time", ""); !json.Valid(r.Body.Bytes()) {
						t.Errorf("reporting the time buckets = %d %q, want JSON", r.Code, r.Body.String())
					}
					call("GET", "/client/track/headers", "")
				}
			}
		}(w)
	}
	close(start)
	wg.Wait()
}

// Invoking the targets reads the targets and the tracking config, which are changed by the calls that add targets or set what is
// tracked while the invocations are being started.
func TestInvokeTargetsWhileTargetsAndTrackingChange(t *testing.T) {
	const rounds = 15
	tc := NewTargetClient(0)
	for i := 0; i < 3; i++ {
		if err := tc.AddTarget(newTestTarget(fmt.Sprintf("invoke%d", i), "http://127.0.0.1:1/")); err != nil {
			t.Fatalf("AddTarget failed: %s", err.Error())
		}
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	run := func(f func(i int)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < rounds; i++ {
				f(i)
			}
		}()
	}
	// A target that is invoked shares its transport client with its other invocations, and replacing the target closes it, so
	// the target is not invoked by more than one call at a time, and the targets that are added are new ones.
	run(func(int) { tc.InvokeAll() })
	run(func(i int) {
		if err := tc.AddTarget(newTestTarget(fmt.Sprintf("invoke-added-%d", i), "http://127.0.0.1:1/")); err != nil {
			t.Errorf("AddTarget failed: %s", err.Error())
		}
		tc.getTargetsToInvoke(nil)
		tc.PrepareTarget("invoke0")
		tc.getTargetsPayload("")
		tc.getTargetsPayload("invoke1")
	})
	run(func(i int) {
		tc.AddTrackingHeaders(fmt.Sprintf("h%d,h-other", i%3))
		tc.AddTrackingTimeBuckets("1-100,100-200")
		tc.getTrackingHeaders()
		if i%5 == 0 {
			tc.clearTrackingHeaders()
			tc.clearTrackingTimeBuckets()
		}
	})
	close(start)
	wg.Wait()
}

// Waiting for the targets to stop can take seconds, and a call that changes the targets meanwhile must not be held up by it.
func TestStopTargetsDoesNotBlockChangesToTheTargetsWhileWaiting(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	var releaseOnce sync.Once
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(func() { finish(); server.Close() })

	tc := NewTargetClient(0)
	if err := tc.AddTarget(newTestTarget("stop-wait", server.URL)); err != nil {
		t.Fatalf("AddTarget failed: %s", err.Error())
	}
	invoked := make(chan struct{})
	go func() {
		defer close(invoked)
		tc.InvokeAll()
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !invocation.IsAnyTargetActive([]string{"stop-wait"}) {
		if time.Now().After(deadline) {
			t.Fatal("the target didn't start its invocation")
		}
		time.Sleep(10 * time.Millisecond)
	}

	stopResult := make(chan [2]bool, 1)
	go func() {
		stopping, stopped := tc.stopTargets([]string{"stop-wait"})
		stopResult <- [2]bool{stopping, stopped}
	}()
	time.Sleep(200 * time.Millisecond)

	added := make(chan error, 1)
	go func() { added <- tc.AddTarget(newTestTarget("added-while-stopping", "http://127.0.0.1:1/")) }()
	select {
	case err := <-added:
		if err != nil {
			t.Errorf("AddTarget failed: %s", err.Error())
		}
	case <-time.After(3 * time.Second):
		t.Error("adding a target was held up by the call that is waiting for a target to stop")
	}

	finish()
	select {
	case r := <-stopResult:
		if !r[0] || !r[1] {
			t.Errorf("stopTargets = (stopping: %v, stopped: %v), want (true, true) once the request finished", r[0], r[1])
		}
	case <-time.After(15 * time.Second):
		t.Error("stopTargets didn't return")
	}
	<-invoked
}
