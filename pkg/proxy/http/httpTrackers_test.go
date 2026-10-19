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

package httpproxy

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func TestNewHTTPTrackerStartsEmpty(t *testing.T) {
	tr := NewHTTPTracker()
	if tr.DownstreamRequestCount != 0 || tr.UpstreamRequestCount != 0 || len(tr.TargetTrackers) != 0 {
		t.Errorf("new tracker = %+v", tr)
	}
	if tr.DownstreamRequestCountsByURI == nil || tr.UpstreamRequestCountsByEndpointStatus == nil || tr.QueryValueMatchCounts == nil {
		t.Error("the maps of a new tracker are not initialized")
	}
	if tt := NewHTTPTargetTracker(); tt.HTTPCounts == nil || tt.URIMatchCounts == nil {
		t.Errorf("new target tracker = %+v", tt)
	}
}

func TestTrackerCountsRequestsPerTargetAndEndpoint(t *testing.T) {
	tr := NewHTTPTracker()
	tr.IncrementDownstreamRequestCounts("/a")
	tr.IncrementDownstreamRequestCounts("/a")
	tr.IncrementDownstreamRequestCounts("/b")
	tr.IncrementUpstreamRequestCounts("/up")
	tr.IncrementTargetDownstreamCounts("t1", "/a")
	tr.IncrementTargetDownstreamCounts("t1", "/a")
	tr.IncrementTargetDownstreamCounts("t2", "/b")
	tr.IncrementTargetUpstreamCounts("t1", "ep1", "/up")
	tr.IncrementTargetUpstreamCounts("t1", "ep2", "/up")
	tr.IncrementTargetUpstreamStatusCounts("t1", "ep1", "/up", 200)
	tr.IncrementTargetUpstreamStatusCounts("t1", "ep1", "/up", 503)
	tr.IncrementTargetUpstreamStatusCounts("t2", "ep9", "/other", 200)
	if tr.DownstreamRequestCount != 3 || !reflect.DeepEqual(tr.DownstreamRequestCountsByURI, map[string]int{"/a": 2, "/b": 1}) {
		t.Errorf("downstream = %d %v", tr.DownstreamRequestCount, tr.DownstreamRequestCountsByURI)
	}
	if tr.UpstreamRequestCount != 1 || tr.UpstreamRequestCountsByURI["/up"] != 1 {
		t.Errorf("upstream = %d %v", tr.UpstreamRequestCount, tr.UpstreamRequestCountsByURI)
	}
	if !reflect.DeepEqual(tr.UpstreamRequestCountByStatus, map[string]int{"200": 2, "503": 1}) {
		t.Errorf("upstream by status = %v", tr.UpstreamRequestCountByStatus)
	}
	t1, t2 := tr.GetTargetTracker("t1"), tr.GetTargetTracker("t2")
	if t1 == nil || t2 == nil || tr.GetTargetTracker("nope") != nil {
		t.Fatalf("target trackers = %v, %v", t1, t2)
	}
	if t1.DownstreamRequestCount != 2 || t1.UpstreamRequestCount != 2 || !reflect.DeepEqual(t1.UpstreamRequestCountsByEndpoint, map[string]int{"ep1": 1, "ep2": 1}) {
		t.Errorf("target t1 = %+v", t1.HTTPCounts)
	}
	if !reflect.DeepEqual(t1.UpstreamRequestCountsByEndpointStatus, map[string]map[string]int{"ep1": {"200": 1, "503": 1}}) ||
		!reflect.DeepEqual(t1.UpstreamRequestCountsByURIStatus, map[string]map[string]int{"/up": {"200": 1, "503": 1}}) {
		t.Errorf("target t1 by status = %v, %v", t1.UpstreamRequestCountsByEndpointStatus, t1.UpstreamRequestCountsByURIStatus)
	}
	if t2.DownstreamRequestCount != 1 || t2.UpstreamRequestCountsByEndpointStatus["ep9"]["200"] != 1 {
		t.Errorf("target t2 = %+v", t2.HTTPCounts)
	}
}

func TestTrackerCountsMatches(t *testing.T) {
	tr := NewHTTPTracker()
	tr.IncrementTargetMatchCounts("t", "/uri", "", "", "", "")
	tr.IncrementTargetMatchCounts("t", "/uri", "", "", "", "")
	tr.IncrementTargetMatchCounts("t", "", "x-h", "", "", "")
	tr.IncrementTargetMatchCounts("t", "", "x-h", "v1", "", "")
	tr.IncrementTargetMatchCounts("t", "", "x-h", "v1", "", "")
	tr.IncrementTargetMatchCounts("t", "", "", "", "q", "")
	tr.IncrementTargetMatchCounts("t", "", "", "", "q", "qv")
	tr.IncrementTargetMatchCounts("t", "", "", "", "", "")
	for name, hc := range map[string]*HTTPCounts{"proxy": tr.HTTPCounts, "target": tr.GetTargetTracker("t").HTTPCounts} {
		if hc.URIMatchCounts["/uri"] != 2 || hc.HeaderMatchCounts["x-h"] != 1 || hc.HeaderValueMatchCounts["x-h"]["v1"] != 2 ||
			hc.QueryMatchCounts["q"] != 1 || hc.QueryValueMatchCounts["q"]["qv"] != 1 {
			t.Errorf("%s match counts = %v %v %v %v %v", name, hc.URIMatchCounts, hc.HeaderMatchCounts, hc.HeaderValueMatchCounts, hc.QueryMatchCounts, hc.QueryValueMatchCounts)
		}
	}
}

func TestTrackerCountsDrops(t *testing.T) {
	tr := NewHTTPTracker()
	tr.IncrementTargetDropCount("t", "/a", true)
	tr.IncrementTargetDropCount("t", "/a", true)
	tr.IncrementTargetDropCount("t", "/b", false)
	tr.IncrementTargetDropCount("t", "", false)
	tt := tr.GetTargetTracker("t")
	for name, hc := range map[string]*HTTPCounts{"proxy": tr.HTTPCounts, "target": tt.HTTPCounts} {
		if hc.RequestDropCount != 2 || hc.ResponseDropCount != 2 || hc.RequestDropCountsByURI["/a"] != 2 || hc.ResponseDropCountsByURI["/b"] != 1 || len(hc.ResponseDropCountsByURI) != 1 {
			t.Errorf("%s drops = %d %d %v %v", name, hc.RequestDropCount, hc.ResponseDropCount, hc.RequestDropCountsByURI, hc.ResponseDropCountsByURI)
		}
	}
}

func TestTrackerSnapshotIsIndependentOfTheTracker(t *testing.T) {
	tr := NewHTTPTracker()
	tr.IncrementDownstreamRequestCounts("/a")
	tr.IncrementTargetUpstreamStatusCounts("t", "ep", "/a", 200)
	tr.IncrementTargetMatchCounts("t", "/a", "h", "v", "q", "qv")
	snap := tr.snapshot()
	tr.IncrementDownstreamRequestCounts("/a")
	tr.IncrementTargetUpstreamStatusCounts("t", "ep", "/a", 200)
	tr.IncrementTargetMatchCounts("t", "/a", "h", "v", "q", "qv")
	tr.IncrementTargetDownstreamCounts("new", "/n")
	if snap.DownstreamRequestCount != 1 || snap.DownstreamRequestCountsByURI["/a"] != 1 || snap.UpstreamRequestCountByStatus["200"] != 1 {
		t.Errorf("snapshot changed with the tracker: %+v", snap.HTTPCounts)
	}
	st := snap.TargetTrackers["t"]
	if st == nil || st.UpstreamRequestCountsByEndpointStatus["ep"]["200"] != 1 || st.HeaderValueMatchCounts["h"]["v"] != 1 || st.QueryValueMatchCounts["q"]["qv"] != 1 {
		t.Errorf("target snapshot changed with the tracker: %+v", st)
	}
	if snap.TargetTrackers["new"] != nil {
		t.Error("a target tracker that was added after the snapshot is in the snapshot")
	}
	if tr.DownstreamRequestCount != 2 {
		t.Errorf("tracker downstream count = %d, want 2", tr.DownstreamRequestCount)
	}
}

func TestTrackerSnapshotOfNilTrackers(t *testing.T) {
	if (*HTTPProxyTracker)(nil).snapshot() != nil || (*HTTPTargetTracker)(nil).snapshot() != nil {
		t.Error("the snapshot of a nil tracker is not nil")
	}
}

func TestTrackerIsReportedAsJSON(t *testing.T) {
	tr := NewHTTPTracker()
	tr.IncrementDownstreamRequestCounts("/a")
	tr.IncrementTargetUpstreamStatusCounts("t", "ep", "/a", 201)
	b, err := json.Marshal(tr.snapshot())
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Downstream int `json:"downstreamRequestCount"`
		Targets    map[string]struct {
			ByEndpoint map[string]map[string]int `json:"upstreamRequestCountsByEndpointStatus"`
		} `json:"targetTrackers"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	if got.Downstream != 1 || got.Targets["t"].ByEndpoint["ep"]["201"] != 1 {
		t.Errorf("reported tracker = %s", b)
	}
}

func TestTrackerConcurrentCounting(t *testing.T) {
	tr := NewHTTPTracker()
	const workers, rounds = 12, 40
	var nilSnapshots atomic.Int32
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			target := fmt.Sprintf("t%d", w%4)
			for i := 0; i < rounds; i++ {
				tr.IncrementDownstreamRequestCounts("/a")
				tr.IncrementTargetDownstreamCounts(target, "/a")
				tr.IncrementTargetUpstreamCounts(target, "ep", "/a")
				tr.IncrementTargetUpstreamStatusCounts(target, "ep", "/a", 200+i%2)
				tr.IncrementTargetMatchCounts(target, "/a", "h", "v", "", "")
				tr.IncrementTargetDropCount(target, "/a", i%2 == 0)
				// Reporting runs along with the counting.
				if tr.snapshot() == nil {
					nilSnapshots.Add(1)
				}
			}
		}(w)
	}
	wg.Wait()
	total := workers * rounds
	if nilSnapshots.Load() != 0 {
		t.Errorf("%d snapshots were nil", nilSnapshots.Load())
	}
	if tr.DownstreamRequestCount != total || tr.URIMatchCounts["/a"] != total || tr.RequestDropCount+tr.ResponseDropCount != total {
		t.Errorf("counts = %d %d %d", tr.DownstreamRequestCount, tr.URIMatchCounts["/a"], tr.RequestDropCount+tr.ResponseDropCount)
	}
	if tr.UpstreamRequestCountByStatus["200"]+tr.UpstreamRequestCountByStatus["201"] != total {
		t.Errorf("upstream by status = %v", tr.UpstreamRequestCountByStatus)
	}
	perTarget := total / 4
	for i := 0; i < 4; i++ {
		tt := tr.GetTargetTracker(fmt.Sprintf("t%d", i))
		if tt == nil || tt.DownstreamRequestCount != perTarget || tt.UpstreamRequestCountsByEndpoint["ep"] != perTarget {
			t.Errorf("target t%d = %+v", i, tt)
		}
	}
}
