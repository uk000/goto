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

package timeline

import (
	"encoding/json"
	"fmt"
	"goto/pkg/global"
	"goto/pkg/types"
	"net/http"
	"reflect"
	"sync"
	"testing"
	"time"
)

func init() {
	global.Funcs.GetListenerLabelForPort = func(int) string { return "listener" }
	// What the timelines say about the instance that they are on is the same wherever the tests run.
	global.Self.HostLabel = "host"
	global.Self.PodName = "pod"
	global.Self.PodIP = "10.0.0.1"
	global.Self.NodeName = "node"
	global.Self.Namespace = "ns"
	global.Self.Cluster = "cluster"
}

func newTestTimeline(stream chan *types.Pair[string, any], update TimelineUpdateNotifierFunc, end TimelineEndNotifierFunc) *Timeline {
	return NewTimeline(8080, "label", map[string]any{"k": "v"}, nil, nil, stream, update, end)
}

func marshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return string(b)
}

func TestIsTimeline(t *testing.T) {
	tests := []struct {
		name string
		data any
		want bool
	}{
		{"timeline", &Timeline{}, true},
		{"map of a timeline", map[string]any{"TYPE": "TIMELINE"}, true},
		{"map of another type", map[string]any{"TYPE": "OTHER"}, false},
		{"map without a type", map[string]any{"Label": "x"}, false},
		{"map with a null type", map[string]any{"TYPE": nil}, false},
		{"map with a type that is not a string", map[string]any{"TYPE": 5}, false},
		{"map with a type that is an object", map[string]any{"TYPE": map[string]any{"a": 1}}, false},
		{"map of the wrong kind", map[string]string{"TYPE": "TIMELINE"}, false},
		{"string", "TIMELINE", false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		if got := IsTimeline(tt.data); got != tt.want {
			t.Errorf("%s: IsTimeline = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestCheckAndGetTimelineOfWhatIsNotATimeline(t *testing.T) {
	for _, data := range []any{map[string]any{"TYPE": 5}, map[string]any{"TYPE": nil}, "x", nil} {
		if got := CheckAndGetTimeline(data); got != nil {
			t.Errorf("CheckAndGetTimeline(%v) = %v, want nil", data, got)
		}
	}
	tl := newTestTimeline(nil, nil, nil)
	if got := CheckAndGetTimeline(tl); got != tl {
		t.Errorf("CheckAndGetTimeline of a timeline = %p, want %p", got, tl)
	}
	loaded := CheckAndGetTimeline(map[string]any{"TYPE": "TIMELINE", "Port": 9090, "Label": "loaded"})
	if loaded == nil || loaded.Port != 9090 || loaded.Label != "loaded" {
		t.Errorf("CheckAndGetTimeline of a map of a timeline = %+v, want the one of the map", loaded)
	}
}

func populatedTimeline() *Timeline {
	tl := newTestTimeline(nil, nil, nil)
	tl.AddEvent("l1", "first")
	client := BuildGotoClientInfo(8080, "caller", "target", "http://example", "server", http.Header{"In": {"i"}}, nil, nil, nil, 2, 3, nil)
	client.StoreHeaders(http.Header{"Out": {"o"}})
	tl.AddEventWithClient("l2", "with client", client)
	tl.AddEventWithRemote("l3", "with remote", "remote text", &GotoServerInfo{ServerInfo: &InstanceInfo{Label: "remote"}}, nil, nil, false)
	tl.AddData("key", map[string]any{"a": 1}, true)
	tl.UpstreamHeaders["up"] = map[string]any{"h": "v"}
	tl.RemoteGotos = map[string]bool{"g": true}
	tl.EndTimeline("end", "done", 200, map[string]any{"up": 1}, nil, true)
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, e := range tl.Events {
		e.At = at
	}
	return tl
}

// The JSON of a timeline is what it has always been, with the timeline written by a marshal that holds its lock.
func TestTimelineJSON(t *testing.T) {
	const want = `{"TYPE":"TIMELINE","Port":8080,"Label":"label","Server":{"ServerInfo":{"Label":"","Host":"host","Listener":"listener","Pod":"pod","PodIP":"10.0.0.1","Node":"node","Namespace":"ns","Cluster":"cluster","Metadata":{"k":"v"}}},"Events":[{"Index":1,"Label":"l1","Text":"first","At":"2026-01-02T03:04:05Z","Client":null,"RemoteServer":null,"RemoteClient":null,"RemoteTimeline":null,"RemoteText":"","RemoteData":null},{"Index":2,"Label":"l2","Text":"with client","At":"2026-01-02T03:04:05Z","Client":{"Label":"caller","Host":"host","Listener":"listener","Pod":"pod","PodIP":"10.0.0.1","Node":"node","Namespace":"ns","Cluster":"cluster","Metadata":null,"RemoteTarget":"target","RemoteURL":"http://example","RemoteLabel":"server","OutboundRequestArgs":null,"OutboundRequestHeaders":{"Out":["o"]},"HeadersConfig":{},"RequestCount":2,"Concurrent":3},"RemoteServer":null,"RemoteClient":null,"RemoteTimeline":null,"RemoteText":"","RemoteData":null},{"Index":3,"Label":"l3","Text":"with remote","At":"2026-01-02T03:04:05Z","Client":null,"RemoteServer":{"ServerInfo":{"Label":"remote","Host":"","Listener":"","Pod":"","PodIP":"","Node":"","Namespace":"","Cluster":"","Metadata":null}},"RemoteClient":null,"RemoteTimeline":null,"RemoteText":"remote text","RemoteData":null},{"Index":4,"Label":"end","Text":"done","At":"2026-01-02T03:04:05Z","Client":null,"RemoteServer":null,"RemoteClient":null,"RemoteTimeline":null,"RemoteText":"","RemoteData":null}],"Data":{"key":{"a":1}},"RemoteCalls":{},"UpstreamHeaders":{"up":{"h":"v"}},"Status":200,"UpstreamStatuses":{"up":1},"Finished":true,"Success":true,"ResultOnly":false,"NoEvents":false,"RemoteGotos":{"g":true}}`
	tl := populatedTimeline()
	if got := marshal(t, tl); got != want {
		t.Errorf("JSON of the timeline =\n%s\nwant\n%s", got, want)
	}
	// The JSON of a timeline loads into the timeline that it was written from.
	loaded, err := LoadTimeline(map[string]any{})
	if err != nil || loaded == nil {
		t.Fatalf("LoadTimeline of nothing = %v, %v", loaded, err)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(want), &data); err != nil {
		t.Fatal(err)
	}
	loaded, err = LoadTimeline(data)
	if err != nil {
		t.Fatal(err)
	}
	if got := marshal(t, loaded); got != want {
		t.Errorf("JSON of the loaded timeline =\n%s\nwant\n%s", got, want)
	}
}

func TestTimelineJSONOfAClientWithoutRemoteInfo(t *testing.T) {
	info := &GotoClientInfo{InstanceInfo: &InstanceInfo{Label: "only-instance"}}
	if got, want := marshal(t, info), `{"Label":"only-instance","Host":"","Listener":"","Pod":"","PodIP":"","Node":"","Namespace":"","Cluster":"","Metadata":null}`; got != want {
		t.Errorf("JSON of a client without remote info = %s, want %s", got, want)
	}
	info.StoreHeaders(http.Header{"A": {"1"}})
	if got, want := marshal(t, info), `{"Label":"only-instance","Host":"","Listener":"","Pod":"","PodIP":"","Node":"","Namespace":"","Cluster":"","Metadata":null,"RemoteTarget":"","RemoteURL":"","RemoteLabel":"","OutboundRequestArgs":null,"OutboundRequestHeaders":{"A":["1"]},"HeadersConfig":null,"RequestCount":0,"Concurrent":0}`; got != want {
		t.Errorf("JSON of a client that stored headers = %s, want %s", got, want)
	}
}

func TestTimelineSnapshot(t *testing.T) {
	tl := populatedTimeline()
	snap := tl.Snapshot()
	before := marshal(t, tl)
	if got := marshal(t, snap); got != before {
		t.Errorf("JSON of the snapshot =\n%s\nwant the one of the timeline\n%s", got, before)
	}
	// What is added to the timeline after the snapshot is not in it, and what is added to the snapshot is not in the timeline.
	tl.AddEvent("later", "after the snapshot")
	tl.AddData("later", 1, false)
	tl.UpstreamHeaders["later"] = 1
	tl.RemoteGotos["later"] = true
	tl.EndTimeline("end", "again", 500, nil, nil, false)
	snap.AddEvent("only", "in the snapshot")
	if got := marshal(t, snap); got == before || len(snap.Events) != 5 || snap.Data["later"] != nil || snap.UpstreamHeaders["later"] != nil || snap.RemoteGotos["later"] || !snap.Success || snap.Status != 200 {
		t.Errorf("snapshot after the timeline went on = %s (success %v, status %d)", got, snap.Success, snap.Status)
	}
	if len(tl.Events) != 6 || tl.Data["only"] != nil || tl.Status != 500 || tl.Success {
		t.Errorf("timeline after its snapshot went on: %d events, status %d, success %v", len(tl.Events), tl.Status, tl.Success)
	}
	if got := tl.NewEvent("x", "y", nil, nil, nil, nil, "", nil).Idx; got != 7 {
		t.Errorf("index of the next event of the timeline = %d, want 7", got)
	}
}

func TestSnapshotOfATimelineWithNothingInIt(t *testing.T) {
	snap := (&Timeline{}).Snapshot()
	if snap.Events != nil || snap.Data != nil || snap.RemoteCalls != nil || snap.UpstreamHeaders != nil || snap.UpstreamStatuses != nil || snap.RemoteGotos != nil {
		t.Errorf("snapshot of an empty timeline = %+v, want what it had: nothing", snap)
	}
}

func TestEndTimeline(t *testing.T) {
	var ended []string
	end := func(key string, data any, success bool) { ended = append(ended, fmt.Sprintf("%s/%v", key, success)) }
	tl := newTestTimeline(nil, func(string, any, bool) error { return nil }, end)
	tl.EndTimeline("l", "finished", 0, map[string]any{"u": 1}, nil, true)
	if !tl.Finished || !tl.Success || tl.Status != 0 || !reflect.DeepEqual(tl.UpstreamStatuses, map[string]any{"u": 1}) {
		t.Errorf("timeline after EndTimeline = finished %v, success %v, status %d, upstream %v", tl.Finished, tl.Success, tl.Status, tl.UpstreamStatuses)
	}
	if len(tl.Events) != 1 || tl.Events[0].Text != "finished" {
		t.Errorf("events after EndTimeline = %v, want one that says finished", tl.Events)
	}
	if !reflect.DeepEqual(ended, []string{"finished/true"}) {
		t.Errorf("end notifications = %v, want the one of the last event, with the success of the timeline", ended)
	}
	tl.EndTimeline("l", "with data", 204, nil, map[string]any{"d": 1}, false)
	if tl.Status != 204 || tl.Success || tl.Data["with data"] == nil {
		t.Errorf("timeline after EndTimeline with data = status %d, success %v, data %v", tl.Status, tl.Success, tl.Data)
	}
}

func TestStreamPreferred(t *testing.T) {
	var updates []string
	update := func(key string, data any, json bool) error { updates = append(updates, key); return nil }
	tl := newTestTimeline(nil, update, nil)
	stream := make(chan *types.Pair[string, any], 10)
	tl.SetStreamPreferred(nil)
	tl.AddEvent("l", "to the notifier")
	tl.SetStreamPreferred(stream)
	tl.AddEvent("l", "to the stream")
	if !reflect.DeepEqual(updates, []string{"ServerInfo", "to the notifier"}) {
		t.Errorf("updates = %v, want the ones before the stream was preferred", updates)
	}
	close(stream)
	var streamed []string
	for p := range stream {
		streamed = append(streamed, p.Left)
	}
	if !reflect.DeepEqual(streamed, []string{"to the stream"}) {
		t.Errorf("streamed = %v, want the event after the stream was preferred", streamed)
	}
}

// The notifications go out in the order that the events are added in.
func TestNotificationsFollowTheOrderOfTheEvents(t *testing.T) {
	const workers, rounds = 8, 30
	stream := make(chan *types.Pair[string, any], workers*rounds+10)
	tl := newTestTimeline(stream, nil, nil)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			for i := 0; i < rounds; i++ {
				tl.AddEvent("l", fmt.Sprintf("w%d-r%d", w, i))
			}
		}(w)
	}
	close(start)
	wg.Wait()
	close(stream)
	var streamed []string
	for p := range stream {
		if p.Left != "ServerInfo" {
			streamed = append(streamed, p.Left)
		}
	}
	if len(tl.Events) != workers*rounds || len(streamed) != workers*rounds {
		t.Fatalf("events = %d, notifications = %d, want %d of each", len(tl.Events), len(streamed), workers*rounds)
	}
	for i, e := range tl.Events {
		if e.Idx != i+1 || e.Text != streamed[i] {
			t.Fatalf("event %d is %d:%q, and its notification is %q, want them to be in the same order", i, e.Idx, e.Text, streamed[i])
		}
	}
}

// What is notified of an update may be reading the timeline that it is notified of, which must not wait for the notifier.
func TestNotifiersMayReadTheTimeline(t *testing.T) {
	var tl *Timeline
	reads := 0
	read := func() {
		if tl != nil {
			if _, err := json.Marshal(tl); err == nil && len(tl.Snapshot().Events) > 0 {
				reads++
			}
		}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		tl = newTestTimeline(nil, func(string, any, bool) error { read(); return nil }, func(string, any, bool) { read() })
		tl.AddEvent("l", "event")
		tl.AddData("k", "v", false)
		tl.EndTimeline("l", "end", 200, nil, nil, true)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the timeline did not finish its updates, a notifier that reads it is waiting for it")
	}
	if reads != 3 {
		t.Errorf("notifiers that read the timeline = %d, want 3: the ones of the event, of the data, and of the end", reads)
	}
}

// Timelines are shared between the goroutines that make a call, the ones that report on it, and the ones that finish it.
func TestConcurrentUpdatesAndReports(t *testing.T) {
	const writers, rounds = 6, 40
	stream := make(chan *types.Pair[string, any], writers*rounds*4+10)
	tl := newTestTimeline(stream, nil, nil)
	client := BuildGotoClientInfo(8080, "caller", "target", "http://example", "server", nil, nil, nil, nil, 1, 1, nil)
	tl.AddEventWithClient("c", "client", client)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			for i := 0; i < rounds; i++ {
				tl.AddEvent("l", fmt.Sprintf("w%d-r%d", w, i))
				tl.AddData(fmt.Sprintf("k%d-%d", w, i), i, false)
				client.StoreHeaders(http.Header{"W": {fmt.Sprint(w)}})
				tl.SetStreamPreferred(stream)
				if i%10 == 9 {
					tl.EndTimeline("l", fmt.Sprintf("end-w%d-r%d", w, i), 200+w, map[string]any{"w": w}, nil, w%2 == 0)
				}
			}
		}(w)
	}
	failures := make([]string, 4)
	for r := range failures {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			<-start
			for i := 0; i < rounds; i++ {
				b, err := json.Marshal(tl)
				if err != nil {
					failures[r] = err.Error()
					return
				}
				var back map[string]any
				if err := json.Unmarshal(b, &back); err != nil || back["TYPE"] != "TIMELINE" {
					failures[r] = fmt.Sprintf("JSON of the timeline is not the one of a timeline: %v", err)
					return
				}
				if snap := tl.Snapshot(); len(snap.Events) == 0 {
					failures[r] = "snapshot without events"
					return
				}
				if _, err := json.Marshal(client); err != nil {
					failures[r] = err.Error()
					return
				}
			}
		}(r)
	}
	close(start)
	wg.Wait()
	for r, f := range failures {
		if f != "" {
			t.Errorf("reporter %d: %s", r, f)
		}
	}
	if want := 1 + writers*rounds + writers*(rounds/10); len(tl.Events) != want {
		t.Errorf("events = %d, want %d", len(tl.Events), want)
	}
	if len(tl.Data) != writers*rounds || !tl.Finished {
		t.Errorf("data = %d entries, finished = %v, want %d entries and finished", len(tl.Data), tl.Finished, writers*rounds)
	}
}
