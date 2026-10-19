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

package events

import (
	"net/http"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// StartSender starts two goroutines, one that stores the events and one that processes the traffic reports, and each of
// them stops on one signal. StopSender used to give one signal, which left one of them running.
func TestStopSenderStopsBothGoroutines(t *testing.T) {
	newEnv(t)
	startSender(t)
	StopSender()
	deadline := time.Now().Add(5 * time.Second)
	for {
		ev, tr := senderGoroutines()
		if ev == 0 && tr == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after StopSender, %d goroutines of the event sender and %d of the traffic processor are still running", ev, tr)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A traffic report has the port, the URI, the status and the details. One that came without the details used to panic
// on the missing item.
func TestProcessTrafficEventWithoutDetailsDoesNotPanic(t *testing.T) {
	newEnv(t)
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("processing a report without details panicked: %v", r)
		}
	}()
	processTrafficEvent([]interface{}{8080, "/foo", 200})
	if tracker(8080, "/foo") != nil {
		t.Error("a report without details was tracked")
	}
}

// The trackers of traffic are changed by the goroutine that processes the reports, while flushing takes them for the
// report and starts them over. The flushed reports and what is left to flush have to account for every request, and the
// two must not touch the trackers at the same time (this is meant to be run with the race detector).
func TestTrafficTrackingAndFlushingTogetherCountEveryRequest(t *testing.T) {
	newEnv(t)
	startSender(t)
	const total = 3000
	uris := []string{"/a", "/b", "/c"}
	stop := make(chan struct{})
	flusherDone := make(chan struct{})
	go func() {
		defer close(flusherDone)
		for {
			select {
			case <-stop:
				return
			default:
				FlushEvents()
				runtime.Gosched()
			}
		}
	}()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < total; i++ {
			processTrafficEvent([]interface{}{1, uris[i%len(uris)], 200, []string{"d"}})
		}
	}()
	wg.Wait()
	close(stop)
	<-flusherDone
	FlushEvents()
	counted := 0
	for _, ev := range stored() {
		if ev.Title != "Flushed Traffic Report" {
			continue
		}
		for _, tr := range ev.Data.([]*EventTracker) {
			counted += tr.StatusRepeatCount
		}
	}
	if counted != total {
		t.Errorf("the flushed reports count %d requests, want %d", counted, total)
	}
}

// The key of a search is a regular expression. One that is not valid used to panic the handler.
func TestSearchWithInvalidPatternIsRejected(t *testing.T) {
	newEnv(t)
	seed(&Event{Title: "t", Data: "d", At: at(1)})
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("searching for an invalid pattern panicked: %v", r)
		}
	}()
	rr := admin("GET", "/events/search/%28")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("answered %d %q, want 400 for a pattern that is not valid", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Invalid key") {
		t.Errorf("body = %q, want the reason", rr.Body.String())
	}
}
