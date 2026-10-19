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

package grpcproxy

import (
	"encoding/json"
	"reflect"
	"sync"
	"testing"
)

func TestNewGRPCProxyTrackerStartsEmpty(t *testing.T) {
	tr := NewGRPCProxyTracker()
	if tr.ConnCount != 0 || tr.ConnCountByUpstream == nil || tr.RequestCountByUpstream == nil || tr.RequestCountByService == nil || tr.RequestCountBySvcMethod == nil ||
		tr.ResponseCountByUpstream == nil || tr.ResponseCountByService == nil || tr.ResponseCountBySvcMethod == nil || tr.MessageCountByType == nil {
		t.Errorf("new tracker = %+v", tr)
	}
}

func TestGRPCTrackerCountsConnections(t *testing.T) {
	tr := NewGRPCProxyTracker()
	tr.IncrementConnCounts("up1")
	tr.IncrementConnCounts("up1")
	tr.IncrementConnCounts("up2")
	tr.IncrementConnCounts("")
	if tr.ConnCount != 4 || !reflect.DeepEqual(tr.ConnCountByUpstream, map[string]int{"up1": 2, "up2": 1}) {
		t.Errorf("connections = %d %v", tr.ConnCount, tr.ConnCountByUpstream)
	}
}

func TestGRPCTrackerCountsMatches(t *testing.T) {
	tr := NewGRPCProxyTracker()
	tr.AddMatchCounts("up", "svc", "Say", "Req", 2, "Res", 1)
	tr.AddMatchCounts("up", "svc", "", "Req", 1, "", 0)
	tr.AddMatchCounts("", "svc2", "Other", "", 1, "Res", 1)
	tr.AddMatchCounts("", "", "orphan", "Req", 1, "", 0)
	if !reflect.DeepEqual(tr.RequestCountByUpstream, map[string]int{"up": 3}) || !reflect.DeepEqual(tr.ResponseCountByUpstream, map[string]int{"up": 1}) {
		t.Errorf("by upstream = %v %v", tr.RequestCountByUpstream, tr.ResponseCountByUpstream)
	}
	if !reflect.DeepEqual(tr.RequestCountByService, map[string]int{"svc": 3, "svc2": 1}) || !reflect.DeepEqual(tr.ResponseCountByService, map[string]int{"svc": 1, "svc2": 1}) {
		t.Errorf("by service = %v %v", tr.RequestCountByService, tr.ResponseCountByService)
	}
	if !reflect.DeepEqual(tr.RequestCountBySvcMethod, map[string]map[string]int{"svc": {"Say": 2}, "svc2": {"Other": 1}}) ||
		!reflect.DeepEqual(tr.ResponseCountBySvcMethod, map[string]map[string]int{"svc": {"Say": 1}, "svc2": {"Other": 1}}) {
		t.Errorf("by service and method = %v %v", tr.RequestCountBySvcMethod, tr.ResponseCountBySvcMethod)
	}
	if !reflect.DeepEqual(tr.MessageCountByType, map[string]int{"Req": 4, "Res": 2}) {
		t.Errorf("by message type = %v", tr.MessageCountByType)
	}
}

func TestGRPCTrackerIsReportedAsJSON(t *testing.T) {
	tr := NewGRPCProxyTracker()
	tr.IncrementConnCounts("up")
	tr.AddMatchCounts("up", "svc", "Say", "Req", 1, "Res", 1)
	b, err := json.Marshal(tr)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		ConnCount int                       `json:"connCount"`
		ByService map[string]int            `json:"requestCountByService"`
		ByMethod  map[string]map[string]int `json:"requestCountByServiceMethod"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	if got.ConnCount != 1 || got.ByService["svc"] != 1 || got.ByMethod["svc"]["Say"] != 1 {
		t.Errorf("reported tracker = %s", b)
	}
}

func TestGRPCTrackerConcurrentCountingAndReporting(t *testing.T) {
	tr := NewGRPCProxyTracker()
	const workers, rounds = 10, 50
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				tr.IncrementConnCounts("up")
				tr.AddMatchCounts("up", "svc", "Say", "Req", 1, "Res", 1)
				json.Marshal(tr)
			}
		}()
	}
	wg.Wait()
	total := workers * rounds
	if tr.ConnCount != total || tr.ConnCountByUpstream["up"] != total || tr.RequestCountBySvcMethod["svc"]["Say"] != total || tr.MessageCountByType["Res"] != total {
		t.Errorf("counts = %d %v %v %v", tr.ConnCount, tr.ConnCountByUpstream, tr.RequestCountBySvcMethod, tr.MessageCountByType)
	}
}
