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

package tcpproxy

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"
)

func TestNewTrackers(t *testing.T) {
	pt := NewTCPTracker(8080)
	if pt.ProxyPort != 8080 || pt.ConnCount != 0 || pt.ConnCountsBySNI == nil || pt.RejectCountsBySNI == nil || pt.TargetTrackers == nil {
		t.Errorf("proxy tracker = %+v", pt)
	}
	tt := NewTCPTargetTracker(8080, "web", "1.2.3.4:5")
	if tt.ProxyPort != 8080 || tt.Upstream != "web" || tt.DownAddress != "1.2.3.4:5" || tt.ConnCountsBySNI == nil || tt.TCPSessionTracker == nil {
		t.Errorf("target tracker = %+v", tt)
	}
	st := NewTCPSessionTracker(8080, "web", "1.2.3.4:5")
	if st.ProxyPort != 8080 || st.Upstream != "web" || st.DownAddress != "1.2.3.4:5" || st.DownConnTracker == nil || st.UpConnTracker == nil || len(st.UpConnTracker) != 0 {
		t.Errorf("session tracker = %+v", st)
	}
}

func TestGetOrAddTargetSessionTracker(t *testing.T) {
	pt := NewTCPTracker(80)
	a1 := pt.GetOrAddTargetSessionTracker(80, "web", "c1:1")
	if a1 == nil || a1.Upstream != "web" || a1.DownAddress != "c1:1" || a1.ProxyPort != 80 {
		t.Fatalf("session = %+v", a1)
	}
	if again := pt.GetOrAddTargetSessionTracker(80, "web", "c1:1"); again != a1 {
		t.Error("the same client of the same target must get the same session")
	}
	a2 := pt.GetOrAddTargetSessionTracker(80, "web", "c2:2")
	if a2 == a1 || a2.DownAddress != "c2:2" {
		t.Error("another client must get a session of its own")
	}
	b := pt.GetOrAddTargetSessionTracker(80, "db", "c1:1")
	if b == a1 || b.Upstream != "db" {
		t.Error("another target must get a session of its own")
	}
	if len(pt.TargetTrackers) != 2 || len(pt.TargetTrackers["web"].TCPSessionTracker) != 2 || len(pt.TargetTrackers["db"].TCPSessionTracker) != 1 {
		t.Errorf("trackers = %+v", pt.TargetTrackers)
	}
	if pt.TargetTrackers["web"].DownAddress != "c1:1" {
		t.Errorf("a target tracker keeps the address of the first client, got %q", pt.TargetTrackers["web"].DownAddress)
	}
}

func TestIncrementMatchCounts(t *testing.T) {
	pt := NewTCPTracker(80)
	pt.GetOrAddTargetSessionTracker(80, "web", "c1:1")
	pt.IncrementMatchCounts(80, "web", "c1:1", "")
	pt.IncrementMatchCounts(80, "web", "c2:2", "a.com")
	pt.IncrementMatchCounts(80, "web", "c3:3", "a.com")
	pt.IncrementMatchCounts(80, "web", "c4:4", "b.com")
	if pt.ConnCount != 4 || !equalCounts(pt.ConnCountsBySNI, map[string]int{"a.com": 2, "b.com": 1}) {
		t.Errorf("proxy: conn count %d, by SNI %v", pt.ConnCount, pt.ConnCountsBySNI)
	}
	tt := pt.TargetTrackers["web"]
	if tt.ConnCount != 4 || !equalCounts(tt.ConnCountsBySNI, map[string]int{"a.com": 2, "b.com": 1}) {
		t.Errorf("target: conn count %d, by SNI %v", tt.ConnCount, tt.ConnCountsBySNI)
	}
	// A target that isn't being tracked yet only counts at the proxy level
	pt.IncrementMatchCounts(80, "other", "c5:5", "c.com")
	if pt.ConnCount != 5 || pt.ConnCountsBySNI["c.com"] != 1 || pt.TargetTrackers["other"] != nil {
		t.Errorf("conn count %d, by SNI %v, other target %v", pt.ConnCount, pt.ConnCountsBySNI, pt.TargetTrackers["other"])
	}
}

func TestIncrementRejectCount(t *testing.T) {
	pt := NewTCPTracker(80)
	pt.IncrementRejectCount("x.com")
	pt.IncrementRejectCount("x.com")
	pt.IncrementRejectCount("")
	if pt.ConnCount != 3 || !equalCounts(pt.RejectCountsBySNI, map[string]int{"x.com": 2, "": 1}) {
		t.Errorf("conn count %d, rejects %v", pt.ConnCount, pt.RejectCountsBySNI)
	}
	if len(pt.ConnCountsBySNI) != 0 {
		t.Errorf("rejected connections don't count as matched ones: %v", pt.ConnCountsBySNI)
	}
}

func TestConnTrackerRecords(t *testing.T) {
	var ct ConnTracker
	t0 := time.Unix(1000, 0)
	ct.init(80, "up", "1.2.3.4:5", t0)
	if s := ct.snapshot(); s.ProxyPort != 80 || s.Upstream != "up" || s.RemoteAddress != "1.2.3.4:5" || !s.StartTime.Equal(t0) {
		t.Errorf("after init: %+v", s)
	}
	ct.recordRead(10, t0.Add(1*time.Second))
	ct.recordRead(5, t0.Add(2*time.Second))
	ct.recordWrite(7, t0.Add(3*time.Second))
	ct.recordWrite(8, t0.Add(4*time.Second))
	ct.recordWrite(9, t0.Add(5*time.Second))
	ct.recordDelay()
	ct.recordDrop()
	ct.recordDrop()
	s := ct.snapshot()
	if s.TotalReads != 2 || s.TotalBytesRead != 15 || s.TotalWrites != 3 || s.TotalBytesWritten != 24 || s.DelayCount != 1 || s.DropCount != 2 {
		t.Errorf("counts: %+v", s)
	}
	if !s.FirstByteInAt.Equal(t0.Add(time.Second)) || !s.LastByteInAt.Equal(t0.Add(2*time.Second)) ||
		!s.FirstByteOutAt.Equal(t0.Add(3*time.Second)) || !s.LastByteOutAt.Equal(t0.Add(5*time.Second)) {
		t.Errorf("times: %v %v %v %v", s.FirstByteInAt, s.LastByteInAt, s.FirstByteOutAt, s.LastByteOutAt)
	}
	if s.Closed || s.RemoteClosed || s.ReadError || s.WriteError || s.ConnectFailed || !s.EndTime.IsZero() {
		t.Errorf("flags: %+v", s)
	}
	ct.markRemoteClosed()
	ct.markReadError()
	ct.markWriteError()
	ct.markConnectFailed()
	ct.markClosed(t0.Add(10 * time.Second))
	ct.markClosed(t0.Add(20 * time.Second))
	s = ct.snapshot()
	if !s.Closed || !s.RemoteClosed || !s.ReadError || !s.WriteError || !s.ConnectFailed {
		t.Errorf("flags: %+v", s)
	}
	if !s.EndTime.Equal(t0.Add(10 * time.Second)) {
		t.Errorf("end time = %v, want the time of the first close", s.EndTime)
	}
}

func TestConnTrackerPreRead(t *testing.T) {
	var ct ConnTracker
	at := time.Unix(5, 0)
	ct.recordPreRead(300, at)
	ct.recordRead(20, at.Add(time.Second))
	s := ct.snapshot()
	if s.TotalBytesRead != 320 || s.TotalReads != 2 || !s.FirstByteInAt.Equal(at) || !s.LastByteInAt.Equal(at.Add(time.Second)) {
		t.Errorf("tracker = %+v", s)
	}
}

func TestConnTrackerInitStartsOver(t *testing.T) {
	var ct ConnTracker
	ct.init(1, "a", "x", time.Unix(1, 0))
	ct.recordRead(10, time.Unix(2, 0))
	ct.markClosed(time.Unix(3, 0))
	ct.init(2, "b", "y", time.Unix(4, 0))
	s := ct.snapshot()
	if s.TotalBytesRead != 0 || s.Closed || !s.EndTime.IsZero() || s.ProxyPort != 2 || s.Upstream != "b" {
		t.Errorf("tracker = %+v", s)
	}
}

func TestSessionTrackerBegin(t *testing.T) {
	st := NewTCPSessionTracker(80, "web", "c:1")
	start := time.Unix(100, 0)
	st.begin("c:1", start)
	st.setSNI("a.com")
	first := st.downTracker()
	first.recordRead(10, start)
	up := st.addUpTracker("h:1", "h:1", start)
	up.recordWrite(10, start)
	if st.SNI != "a.com" || len(st.UpConnTracker) != 1 {
		t.Fatalf("session = %+v", st)
	}

	st.begin("c:1", start.Add(time.Hour))
	if st.SNI != "" || len(st.UpConnTracker) != 0 {
		t.Errorf("a new connection starts without SNI or upstream trackers: %+v", st)
	}
	d := st.downTracker().snapshot()
	if d.TotalBytesRead != 0 || !d.StartTime.Equal(start.Add(time.Hour)) || d.RemoteAddress != "c:1" || d.ProxyPort != 80 || d.Upstream != "web" {
		t.Errorf("downstream tracker = %+v", d)
	}
	if st.downTracker() == first {
		t.Error("the downstream tracker of the previous connection must not be reused")
	}
	if got := first.snapshot().TotalBytesRead; got != 10 {
		t.Errorf("what the earlier connection tracked is not to be rewritten, got %d bytes read", got)
	}
}

func TestTrackerJSON(t *testing.T) {
	pt := NewTCPTracker(80)
	st := pt.GetOrAddTargetSessionTracker(80, "web", "c:1")
	pt.IncrementMatchCounts(80, "web", "c:1", "a.com")
	pt.IncrementRejectCount("b.com")
	st.begin("c:1", time.Unix(1, 0))
	st.setSNI("a.com")
	st.downTracker().recordRead(3, time.Unix(2, 0))
	st.addUpTracker("h:1", "10.0.0.1:1", time.Unix(1, 0)).recordWrite(3, time.Unix(2, 0))

	b, err := json.Marshal(pt)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{"proxyPort": 80.0, "connCount": 2.0} {
		if got[key] != want {
			t.Errorf("%s = %v, want %v", key, got[key], want)
		}
	}
	if got["connCountsBySNI"].(map[string]any)["a.com"] != 1.0 || got["rejectCountsBySNI"].(map[string]any)["b.com"] != 1.0 {
		t.Errorf("counts by SNI: %v %v", got["connCountsBySNI"], got["rejectCountsBySNI"])
	}
	target := got["targetTrackers"].(map[string]any)["web"].(map[string]any)
	for _, key := range []string{"proxyPort", "upstream", "downAddress", "connCount", "connCountsBySNI", "tcpSessions"} {
		if _, ok := target[key]; !ok {
			t.Errorf("target tracker lacks %q: %v", key, target)
		}
	}
	session := target["tcpSessions"].(map[string]any)["c:1"].(map[string]any)
	for _, key := range []string{"proxyPort", "upstream", "downAddress", "sni", "downConn", "upConn"} {
		if _, ok := session[key]; !ok {
			t.Errorf("session tracker lacks %q: %v", key, session)
		}
	}
	if session["sni"] != "a.com" {
		t.Errorf("sni = %v", session["sni"])
	}
	down := session["downConn"].(map[string]any)
	for _, key := range []string{"proxyPort", "upstream", "remoteAddress", "startTime", "endTime", "firstByteInAt", "lastByteInAt",
		"firstByteOutAt", "lastByteOutAt", "totalBytesRead", "totalBytesWritten", "totalReads", "totalWrites", "delayCount",
		"dropCount", "closed", "remoteClosed", "readError", "writeError", "connectFailed"} {
		if _, ok := down[key]; !ok {
			t.Errorf("connection tracker lacks %q: %v", key, down)
		}
	}
	if down["totalBytesRead"] != 3.0 || down["remoteAddress"] != "c:1" {
		t.Errorf("downstream = %v", down)
	}
	ups := session["upConn"].([]any)
	if len(ups) != 1 || ups[0].(map[string]any)["totalBytesWritten"] != 3.0 || ups[0].(map[string]any)["remoteAddress"] != "10.0.0.1:1" {
		t.Errorf("upstream trackers = %v", ups)
	}
}

func TestTrackerJSONOfEmptyProxy(t *testing.T) {
	b, err := json.Marshal(newTCPProxy(9))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Port      int
		Enabled   bool
		Upstreams map[string]any
		Tracker   map[string]any
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Port != 9 || !got.Enabled || got.Upstreams == nil || len(got.Upstreams) != 0 || got.Tracker["proxyPort"] != 9.0 {
		t.Errorf("proxy json = %s", b)
	}
}

func TestTrackersConcurrently(t *testing.T) {
	pt := NewTCPTracker(80)
	const workers, rounds = 8, 100
	var wg sync.WaitGroup
	stop := make(chan struct{})
	var reporter sync.WaitGroup
	reportErr := make(chan error, 1)
	reporter.Add(1)
	go func() {
		defer reporter.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if _, err := json.Marshal(pt); err != nil {
					reportErr <- err
					return
				}
			}
		}
	}()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			target := fmt.Sprintf("t%d", w%2)
			for i := 0; i < rounds; i++ {
				down := fmt.Sprintf("c%d:%d", w, i%5)
				st := pt.GetOrAddTargetSessionTracker(80, target, down)
				pt.IncrementMatchCounts(80, target, down, "a.com")
				pt.IncrementRejectCount("x.com")
				st.begin(down, time.Now())
				d := st.downTracker()
				d.recordRead(1, time.Now())
				d.recordWrite(1, time.Now())
				st.addUpTracker("h:1", "h:1", time.Now()).recordRead(1, time.Now())
			}
		}()
	}
	waitGroup(t, &wg, "workers")
	close(stop)
	waitGroup(t, &reporter, "reporter")
	select {
	case err := <-reportErr:
		t.Errorf("the tracker could not be reported while it was in use: %v", err)
	default:
	}
	// every worker matched and rejected one connection per round
	if want := 2 * workers * rounds; pt.ConnCount != want {
		t.Errorf("conn count = %d, want %d", pt.ConnCount, want)
	}
	if pt.ConnCountsBySNI["a.com"] != workers*rounds || pt.RejectCountsBySNI["x.com"] != workers*rounds {
		t.Errorf("by SNI = %v, rejected by SNI = %v", pt.ConnCountsBySNI, pt.RejectCountsBySNI)
	}
	targets := []string{}
	for name, tt := range pt.TargetTrackers {
		targets = append(targets, name)
		if tt.ConnCount != workers/2*rounds {
			t.Errorf("target %s conn count = %d, want %d", name, tt.ConnCount, workers/2*rounds)
		}
		if len(tt.TCPSessionTracker) != workers/2*5 {
			t.Errorf("target %s sessions = %d, want one per client address (%d)", name, len(tt.TCPSessionTracker), workers/2*5)
		}
	}
	sort.Strings(targets)
	if fmt.Sprint(targets) != "[t0 t1]" {
		t.Errorf("targets = %v", targets)
	}
}
