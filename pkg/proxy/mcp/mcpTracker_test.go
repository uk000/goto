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

package mcpproxy

import (
	"encoding/json"
	"reflect"
	"sync"
	"testing"
)

func TestNewMCPProxyTrackerStartsEmpty(t *testing.T) {
	tr := NewMCPProxyTracker()
	if tr.HTTPProxyTracker == nil || tr.ConnCount != 0 || tr.ConnCountByUpstream == nil || tr.RequestCountByUpstream == nil || tr.RequestCountByServer == nil ||
		tr.RequestCountByServerTool == nil || tr.ResponseCountByUpstream == nil || tr.ResponseCountByServer == nil || tr.ResponseCountByServerTool == nil || tr.MessageCountByType == nil {
		t.Errorf("new tracker = %+v", tr)
	}
}

func TestMCPTrackerCountsConnections(t *testing.T) {
	tr := NewMCPProxyTracker()
	tr.IncrementConnCounts("up1")
	tr.IncrementConnCounts("up1")
	tr.IncrementConnCounts("up2")
	tr.IncrementConnCounts("")
	if tr.ConnCount != 4 || !reflect.DeepEqual(tr.ConnCountByUpstream, map[string]int{"up1": 2, "up2": 1}) {
		t.Errorf("connections = %d %v", tr.ConnCount, tr.ConnCountByUpstream)
	}
}

func TestMCPTrackerCountsMatches(t *testing.T) {
	tr := NewMCPProxyTracker()
	tr.AddMatchCounts("up", "srv", "calc", "tools/call", 2, "result", 1)
	tr.AddMatchCounts("up", "srv", "", "tools/list", 1, "", 0)
	tr.AddMatchCounts("", "srv2", "echo", "", 1, "error", 1)
	tr.AddMatchCounts("", "", "orphan", "tools/call", 1, "", 0)
	if !reflect.DeepEqual(tr.RequestCountByUpstream, map[string]int{"up": 3}) || !reflect.DeepEqual(tr.ResponseCountByUpstream, map[string]int{"up": 1}) {
		t.Errorf("by upstream = %v %v", tr.RequestCountByUpstream, tr.ResponseCountByUpstream)
	}
	if !reflect.DeepEqual(tr.RequestCountByServer, map[string]int{"srv": 3, "srv2": 1}) || !reflect.DeepEqual(tr.ResponseCountByServer, map[string]int{"srv": 1, "srv2": 1}) {
		t.Errorf("by server = %v %v", tr.RequestCountByServer, tr.ResponseCountByServer)
	}
	if !reflect.DeepEqual(tr.RequestCountByServerTool, map[string]map[string]int{"srv": {"calc": 2}, "srv2": {"echo": 1}}) ||
		!reflect.DeepEqual(tr.ResponseCountByServerTool, map[string]map[string]int{"srv": {"calc": 1}, "srv2": {"echo": 1}}) {
		t.Errorf("by server and tool = %v %v", tr.RequestCountByServerTool, tr.ResponseCountByServerTool)
	}
	if !reflect.DeepEqual(tr.MessageCountByType, map[string]int{"tools/call": 3, "result": 1, "tools/list": 1, "error": 1}) {
		t.Errorf("by message type = %v", tr.MessageCountByType)
	}
}

func TestMCPTrackerIsReportedAsJSON(t *testing.T) {
	tr := NewMCPProxyTracker()
	tr.IncrementConnCounts("up")
	tr.AddMatchCounts("up", "srv", "calc", "tools/call", 1, "result", 1)
	b, err := json.Marshal(tr)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		ConnCount            int                       `json:"connCount"`
		RequestCountByServer map[string]int            `json:"requestCountByServer"`
		ByServerTool         map[string]map[string]int `json:"requestCountByServerTool"`
		Downstream           *int                      `json:"downstreamRequestCount"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	if got.ConnCount != 1 || got.RequestCountByServer["srv"] != 1 || got.ByServerTool["srv"]["calc"] != 1 || got.Downstream == nil {
		t.Errorf("reported tracker = %s", b)
	}
}

func TestMCPTrackerConcurrentCounting(t *testing.T) {
	tr := NewMCPProxyTracker()
	const workers, rounds = 10, 50
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				tr.IncrementConnCounts("up")
				tr.AddMatchCounts("up", "srv", "calc", "tools/call", 1, "result", 1)
			}
		}()
	}
	wg.Wait()
	total := workers * rounds
	if tr.ConnCount != total || tr.ConnCountByUpstream["up"] != total || tr.RequestCountByServerTool["srv"]["calc"] != total || tr.MessageCountByType["result"] != total {
		t.Errorf("counts = %d %v %v %v", tr.ConnCount, tr.ConnCountByUpstream, tr.RequestCountByServerTool, tr.MessageCountByType)
	}
}
