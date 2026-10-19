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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/server/middleware"
	"goto/pkg/util"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

var (
	stubOnce  sync.Once
	routeOnce sync.Once
)

func stubGlobalFuncs() {
	global.Funcs.IsReadinessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsLivenessProbe = func(*http.Request) bool { return false }
	global.Funcs.IsListenerPresent = func(int) bool { return false }
	global.Funcs.IsListenerOpen = func(int) bool { return false }
	global.Funcs.GetListenerID = func(int) string { return "" }
	global.Funcs.GetListenerLabel = func(*http.Request) string { return "" }
	global.Funcs.GetListenerLabelForPort = func(port int) string { return fmt.Sprintf("listener-%d", port) }
	util.WillTunnel = func(*http.Request, *util.RequestStore) bool { return false }
}

// env has the events enabled, with a stub for the locker that keeps what the locker is given, and all of the package
// state emptied. It puts everything back when the test is done.
type env struct {
	t      *testing.T
	mu     sync.Mutex
	locker []*Event
}

func newEnv(t *testing.T) *env {
	t.Helper()
	stubOnce.Do(stubGlobalFuncs)
	e := &env{t: t}
	oldFlags, oldSelf, oldStore := global.Flags, global.Self, global.Funcs.StoreEventInCurrentLocker
	global.Flags.EnableEvents = true
	global.Flags.PublishEvents = false
	global.Self.Name = "test-peer"
	global.Self.HostLabel = "test-host"
	global.Self.RegistryURL = ""
	global.Funcs.StoreEventInCurrentLocker = func(i interface{}) {
		e.mu.Lock()
		defer e.mu.Unlock()
		if ev, ok := i.(*Event); ok {
			e.locker = append(e.locker, ev)
		}
	}
	e.reset()
	t.Cleanup(func() {
		e.reset()
		global.Flags, global.Self, global.Funcs.StoreEventInCurrentLocker = oldFlags, oldSelf, oldStore
	})
	return e
}

func (e *env) reset() {
	lock.Lock()
	eventsList = []*Event{}
	trafficEventTracker = map[int]map[string]*EventTracker{}
	lock.Unlock()
	for {
		select {
		case <-eventChannel:
		case <-trafficChannel:
		case <-stopSender:
		default:
			return
		}
	}
}

func (e *env) lockerEvents() []*Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]*Event{}, e.locker...)
}

func stored() []*Event {
	lock.RLock()
	defer lock.RUnlock()
	return append([]*Event{}, eventsList...)
}

func titles(events []*Event) []string {
	list := []string{}
	for _, e := range events {
		list = append(list, e.Title)
	}
	return list
}

// queued takes the events that are waiting in the channel of the sender, for tests that run without the sender.
func queued() []*Event {
	list := []*Event{}
	for {
		select {
		case e := <-eventChannel:
			list = append(list, e)
		default:
			return list
		}
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// senderGoroutines counts the goroutines that the sender runs in: the one that stores events and the one that processes
// the traffic reports.
func senderGoroutines() (events, traffic int) {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	stack := string(buf)
	return strings.Count(stack, "pkg/events.eventSender("), strings.Count(stack, "pkg/events.trafficEventsProcessor(")
}

// startSender starts the goroutines of the sender, and stops them when the test is done (whatever the test did to them),
// before the state that they use is put back.
func startSender(t *testing.T) {
	t.Helper()
	StartSender()
	t.Cleanup(func() {
		ev, tr := senderGoroutines()
		for i := 0; i < ev+tr; i++ {
			stopSender <- true
		}
		done := make(chan struct{})
		go func() {
			senders.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("the sender goroutines did not stop")
		}
	})
	waitFor(t, "the sender goroutines to start", func() bool {
		ev, tr := senderGoroutines()
		return ev == 1 && tr == 1
	})
}

func withPort(req *http.Request, port int) context.Context {
	return context.WithValue(req.Context(), util.CurrentPortKey, port)
}

func newRequest(method, target string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, target, body)
	_, req, _ = util.WithRequestStore(req, nil)
	return req
}

// admin sends a request to the admin API of events.
func admin(method, target string) *httptest.ResponseRecorder {
	routeOnce.Do(func() { Middleware.SetRoutes(nil) })
	rr := httptest.NewRecorder()
	middleware.RootRouters["/events"].ServeHTTP(rr, newRequest(method, target, nil))
	return rr
}

func decodeEvents(t *testing.T, rr *httptest.ResponseRecorder) []Event {
	t.Helper()
	var list []Event
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatalf("response %q is not a list of events: %v", rr.Body.String(), err)
	}
	return list
}

func TestNewEventSummary(t *testing.T) {
	at := time.Now()
	tests := []struct {
		name    string
		summary string
		data    interface{}
		want    string
	}{
		{"short text is its own summary", "", "short", "short"},
		{"empty text", "", "", ""},
		{"text of 20 characters is not cut", "", "12345678901234567890", "12345678901234567890"},
		{"long text is cut", "", "123456789012345678901", "12345678901234567890..."},
		{"given summary is kept", "mine", "something that is longer than twenty", "mine"},
		{"data that is not text has no summary", "", map[string]int{"a": 1}, ""},
		{"nil data has no summary", "", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEvent("title", tt.summary, tt.data, at, "peer", "host")
			if e.Summary != tt.want {
				t.Errorf("summary = %q, want %q", e.Summary, tt.want)
			}
			if e.Title != "title" || e.Peer != "peer" || e.PeerHost != "host" || !e.At.Equal(at) || !reflect.DeepEqual(e.Data, tt.data) {
				t.Errorf("event = %+v, want the given title, peer, host, time and data", e)
			}
		})
	}
}

func TestEventJSONFields(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	b, err := json.Marshal(newEvent("t", "s", map[string]string{"k": "v"}, at, "p", "h"))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"title", "summary", "data", "at", "peer", "peerHost"} {
		if _, ok := got[key]; !ok {
			t.Errorf("event JSON %s has no %q", b, key)
		}
	}
	if len(got) != 6 {
		t.Errorf("event JSON %s has %d fields, want 6", b, len(got))
	}
}

func TestEventTrackerSummary(t *testing.T) {
	tr := &EventTracker{Port: 8080, URI: "/foo", StatusCode: 503, TrafficDetails: []string{"a", "b"}, StatusRepeatCount: 4}
	want := "Port [8080] URI [/foo] Status [503] Traffic Details [a,b] Repeated x[4]"
	if got := tr.summary(); got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}

func TestEventTrackerCopyIsIndependent(t *testing.T) {
	tr := &EventTracker{Port: 1, URI: "/u", StatusCode: 200, TrafficDetails: []string{"a"}, StatusRepeatCount: 2}
	c := tr.copy()
	tr.StatusRepeatCount++
	tr.TrafficDetails[0] = "changed"
	if c.StatusRepeatCount != 2 || c.TrafficDetails[0] != "a" || c.Port != 1 || c.URI != "/u" || c.StatusCode != 200 {
		t.Errorf("copy = %+v, want the values from before the changes", c)
	}
}

func TestSendEventDirectStoresAndGivesToLocker(t *testing.T) {
	e := newEnv(t)
	before := time.Now()
	at := SendEventDirect("Title", "some data")
	if at.Before(before) || at.After(time.Now()) {
		t.Errorf("returned time %v is not the time of the call", at)
	}
	list := stored()
	if len(list) != 1 {
		t.Fatalf("stored %d events, want 1", len(list))
	}
	ev := list[0]
	if ev.Title != "Title" || ev.Data != "some data" || ev.Summary != "some data" || ev.Peer != "test-peer" ||
		ev.PeerHost != "test-host" || !ev.At.Equal(at) {
		t.Errorf("stored event = %+v", ev)
	}
	if got := e.lockerEvents(); len(got) != 1 || got[0] != ev {
		t.Errorf("locker got %v, want the stored event", got)
	}
	if len(queued()) != 0 {
		t.Error("a direct event also went through the queue")
	}
}

func TestSendEventJSONDirectKeepsDataAndSummary(t *testing.T) {
	newEnv(t)
	data := map[string]int{"a": 1}
	SendEventJSONDirect("JSON", "the summary", data)
	list := stored()
	if len(list) != 1 || list[0].Summary != "the summary" || !reflect.DeepEqual(list[0].Data, data) {
		t.Errorf("stored = %+v, want one event with the data and summary", list)
	}
}

func TestEventsDisabledDoNothing(t *testing.T) {
	e := newEnv(t)
	global.Flags.EnableEvents = false
	req := newRequest("GET", "/x", nil)
	times := []time.Time{
		SendEvent("a", "b"), SendEventForPort(1, "a", "b"), SendEventDirect("a", "b"),
		SendEventJSON("a", "b", 1), SendEventJSONForPort(1, "a", "b", 1), SendEventJSONDirect("a", "b", 1),
		SendRequestEvent("a", "b", req), SendRequestEventJSON("a", "b", 1, req),
	}
	for i, at := range times {
		if at.IsZero() {
			t.Errorf("call %d returned a zero time", i)
		}
	}
	TrackTrafficEvent(200, req)
	TrackPortTrafficEvent(1, "op", 200)
	FlushEvents()
	ClearEvents()
	StartSender()
	StopSender()
	if n := len(queued()) + len(trafficChannel) + len(stored()) + len(e.lockerEvents()); n != 0 {
		t.Errorf("%d events were queued or stored while events are disabled", n)
	}
	if ev, tr := senderGoroutines(); ev != 0 || tr != 0 {
		t.Errorf("sender goroutines = %d, %d, want none while events are disabled", ev, tr)
	}
}

func TestQueuedEventsDescribeTheirSender(t *testing.T) {
	newEnv(t)
	SendEvent("plain", "d1")
	SendEventForPort(9090, "port", "d2")
	SendEventJSON("json", "sum", map[string]int{"x": 1})
	SendEventJSONForPort(9091, "jsonport", "sum2", 2)
	got := queued()
	if want := []string{"plain", "port", "json", "jsonport"}; !reflect.DeepEqual(titles(got), want) {
		t.Fatalf("queued %v, want %v", titles(got), want)
	}
	for _, ev := range got {
		if ev.Peer != "test-peer" || ev.PeerHost != "test-host" || ev.At.IsZero() {
			t.Errorf("event %+v does not have the peer, host and time of the server", ev)
		}
	}
	if got[2].Summary != "sum" || got[3].Summary != "sum2" || got[0].Summary != "d1" {
		t.Errorf("summaries = %q %q %q", got[0].Summary, got[2].Summary, got[3].Summary)
	}
}

func TestRequestEventsUseListenerOfRequest(t *testing.T) {
	newEnv(t)
	req := httptest.NewRequest("GET", "/x", nil)
	req = req.WithContext(withPort(req, 7777))
	_, req, _ = util.WithRequestStore(req, nil)
	SendRequestEvent("from request", "data", req)
	SendRequestEventJSON("json from request", "s", 5, req)
	SendRequestEvent("without request", "data", nil)
	got := queued()
	if len(got) != 3 {
		t.Fatalf("queued %d events, want 3", len(got))
	}
	rs := util.GetRequestStore(req)
	want := fmt.Sprintf("listener-%d", rs.RequestPortNum)
	if got[0].PeerHost != want || got[1].PeerHost != want {
		t.Errorf("hosts = %q, %q, want %q, the label of the listener of the request", got[0].PeerHost, got[1].PeerHost, want)
	}
	if got[2].PeerHost != "test-host" {
		t.Errorf("host without a request = %q, want the label of the server", got[2].PeerHost)
	}
}

func TestSenderStoresQueuedEvents(t *testing.T) {
	e := newEnv(t)
	startSender(t)
	for i := 0; i < 5; i++ {
		SendEvent(fmt.Sprintf("event %d", i), "d")
	}
	waitFor(t, "5 events to be stored", func() bool { return len(stored()) == 5 })
	waitFor(t, "5 events to be given to the locker", func() bool { return len(e.lockerEvents()) == 5 })
	want := []string{"event 0", "event 1", "event 2", "event 3", "event 4"}
	if got := titles(stored()); !reflect.DeepEqual(got, want) {
		t.Errorf("stored %v, want %v in the order they were sent", got, want)
	}
}

func TestPublishEventsToRegistry(t *testing.T) {
	e := newEnv(t)
	type received struct {
		method, path, contentType string
		event                     Event
	}
	got := make(chan received, 4)
	// The client of the events talks HTTP/2 without TLS, as it does to a registry.
	srv := httptest.NewServer(h2c.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ev Event
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &ev)
		got <- received{r.Method, r.URL.Path, r.Header.Get(constants.HeaderContentType), ev}
	}), &http2.Server{}))
	defer srv.Close()
	global.Flags.PublishEvents = true
	global.Self.RegistryURL = srv.URL
	SendEventJSONDirect("Published", "sum", map[string]int{"n": 3})
	select {
	case r := <-got:
		if r.method != "POST" || r.path != "/registry/peers/test-peer/test-host/events/store" {
			t.Errorf("registry got %s %s", r.method, r.path)
		}
		if r.contentType != constants.ContentTypeJSON {
			t.Errorf("content type = %q, want %q", r.contentType, constants.ContentTypeJSON)
		}
		if r.event.Title != "Published" || r.event.Summary != "sum" || r.event.Peer != "test-peer" || r.event.PeerHost != "test-host" {
			t.Errorf("registry got event %+v", r.event)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the event was not published to the registry")
	}
	if len(stored()) != 1 {
		t.Errorf("stored %d events, want the published event also kept locally", len(stored()))
	}
	if n := len(e.lockerEvents()); n != 0 {
		t.Errorf("locker got %d events, want none when the events are published to the registry", n)
	}
}

func TestPublishEventsWithoutRegistryUsesLocker(t *testing.T) {
	e := newEnv(t)
	global.Flags.PublishEvents = true
	global.Self.RegistryURL = ""
	SendEventDirect("Local", "d")
	if n := len(e.lockerEvents()); n != 1 {
		t.Errorf("locker got %d events, want 1 when there is no registry to publish to", n)
	}
}

func TestPublishEventsSurvivesRegistryFailure(t *testing.T) {
	newEnv(t)
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	global.Flags.PublishEvents = true
	global.Self.RegistryURL = url
	SendEventDirect("Unsent", "d")
	if n := len(stored()); n != 1 {
		t.Errorf("stored %d events, want the event kept even if the registry cannot be reached", n)
	}
}

// track runs a report of traffic and returns the titles of the events that it queued.
func track(port int, uri string, status int, details ...string) []string {
	processTrafficEvent([]interface{}{port, uri, status, details})
	return titles(queued())
}

func tracker(port int, uri string) *EventTracker {
	lock.RLock()
	defer lock.RUnlock()
	if trafficEventTracker[port] == nil {
		return nil
	}
	return trafficEventTracker[port][uri]
}

func TestTrafficFirstRequestIsReported(t *testing.T) {
	newEnv(t)
	got := track(8080, "/foo", 200, "d1")
	if !reflect.DeepEqual(got, []string{"URI First Request"}) {
		t.Fatalf("events = %v, want a first request event", got)
	}
	tr := tracker(8080, "/foo")
	if tr == nil || tr.StatusRepeatCount != 1 || tr.StatusCode != 200 || tr.Port != 8080 || tr.URI != "/foo" ||
		!reflect.DeepEqual(tr.TrafficDetails, []string{"d1"}) || tr.FirstEventAt.IsZero() || !tr.FirstEventAt.Equal(tr.LastEventAt) {
		t.Errorf("tracker = %+v", tr)
	}
}

func TestTrafficFirstRequestEventText(t *testing.T) {
	newEnv(t)
	processTrafficEvent([]interface{}{8080, "/foo", 200, []string{"a", "b"}})
	list := queued()
	if len(list) != 1 {
		t.Fatalf("queued %d events, want 1", len(list))
	}
	want := "Port [8080] URI [/foo] First Request with Status [200] Traffic Details [a,b]"
	if list[0].Data != want || list[0].Title != "URI First Request" {
		t.Errorf("event = %q %q, want %q", list[0].Title, list[0].Data, want)
	}
	if tr := tracker(8080, "/foo"); !tr.FirstEventAt.Equal(list[0].At) {
		t.Errorf("tracker first event time %v, want the time of the event %v", tr.FirstEventAt, list[0].At)
	}
}

func TestTrafficRepeatsAreCountedWithoutEvents(t *testing.T) {
	newEnv(t)
	track(8080, "/foo", 200, "d")
	for i := 0; i < 4; i++ {
		if got := track(8080, "/foo", 200, "D"); len(got) != 0 {
			t.Fatalf("repeat %d queued %v, want no event for repeated traffic", i, got)
		}
	}
	tr := tracker(8080, "/foo")
	if tr.StatusRepeatCount != 5 {
		t.Errorf("repeat count = %d, want 5", tr.StatusRepeatCount)
	}
	if !tr.LastEventAt.After(tr.FirstEventAt) && !tr.LastEventAt.Equal(tr.FirstEventAt) {
		t.Errorf("last event %v is before the first %v", tr.LastEventAt, tr.FirstEventAt)
	}
}

func TestTrafficIsTrackedPerPortAndURI(t *testing.T) {
	newEnv(t)
	for _, c := range []struct {
		port int
		uri  string
	}{{1, "/a"}, {1, "/b"}, {2, "/a"}} {
		if got := track(c.port, c.uri, 200); !reflect.DeepEqual(got, []string{"URI First Request"}) {
			t.Errorf("%d %s: events = %v, want a first request each", c.port, c.uri, got)
		}
	}
}

func TestTrafficStatusChangeAfterRepeatsIsReported(t *testing.T) {
	newEnv(t)
	track(8080, "/foo", 200, "d")
	track(8080, "/foo", 200, "d")
	track(8080, "/foo", 200, "d")
	processTrafficEvent([]interface{}{8080, "/foo", 503, []string{"d"}})
	list := queued()
	if got := titles(list); !reflect.DeepEqual(got, []string{"Repeated URI Status", "URI Status / Details Changed"}) {
		t.Fatalf("events = %v, want the report of the repeats before the change", got)
	}
	if list[0].Summary != "Port [8080] URI [/foo] Status [200] Traffic Details [d] Repeated x[3]" {
		t.Errorf("repeat summary = %q", list[0].Summary)
	}
	if tr, ok := list[0].Data.(*EventTracker); !ok || tr.StatusRepeatCount != 3 || tr.StatusCode != 200 {
		t.Errorf("repeat data = %+v, want the tracker of the old status with 3 repeats", list[0].Data)
	}
	want := "Port [8080] URI [/foo] Old Status [200] New Status [503] Traffic Old Details [d] New Details [d]"
	if list[1].Data != want {
		t.Errorf("change = %q, want %q", list[1].Data, want)
	}
	if tr := tracker(8080, "/foo"); tr.StatusCode != 503 || tr.StatusRepeatCount != 1 {
		t.Errorf("tracker = %+v, want a new one for the new status", tr)
	}
}

func TestTrafficStatusChangeWithoutRepeatsHasNoRepeatReport(t *testing.T) {
	newEnv(t)
	track(8080, "/foo", 200)
	if got := track(8080, "/foo", 500); !reflect.DeepEqual(got, []string{"URI Status / Details Changed"}) {
		t.Errorf("events = %v, want only the change", got)
	}
}

func TestTrafficDetailsChangeIsReported(t *testing.T) {
	tests := []struct {
		name    string
		details []string
		want    []string
	}{
		{"same first detail in other case is the same traffic", []string{"ONE"}, nil},
		{"other first detail", []string{"two"}, []string{"URI Status / Details Changed"}},
		{"other number of details", []string{"two", "extra"}, []string{"URI Status / Details Changed"}},
		{"no details", nil, []string{"URI Status / Details Changed"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newEnv(t)
			track(8080, "/foo", 200, "one")
			got := track(8080, "/foo", 200, tt.details...)
			if len(got) == 0 {
				got = nil
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("events = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProcessTrafficEventIgnoresMalformedReports(t *testing.T) {
	newEnv(t)
	for _, traffic := range [][]interface{}{
		nil,
		{},
		{1, "/u"},
		{8080, "/u", 200},
		{"8080", "/u", 200, []string{}},
		{8080, 5, 200, []string{}},
		{8080, "/u", "200", []string{}},
		{8080, "/u", 200, "details"},
	} {
		processTrafficEvent(traffic)
	}
	if n := len(queued()); n != 0 {
		t.Errorf("malformed reports queued %d events, want none", n)
	}
	if tracker(8080, "/u") != nil {
		t.Error("a malformed report is tracked")
	}
}

func TestTrackTrafficEventNormalizesURI(t *testing.T) {
	newEnv(t)
	req := httptest.NewRequest("GET", "/Foo/BAR?x=1", nil)
	req = req.WithContext(withPort(req, 8080))
	_, req, _ = util.WithRequestStore(req, nil)
	TrackTrafficEvent(201, req, "detail")
	report := <-trafficChannel
	if len(report) != 4 || report[1] != "/foo/bar?x=1" || report[2] != 201 || !reflect.DeepEqual(report[3], []string{"detail"}) {
		t.Errorf("report = %v, want the URI in lower case with the status and details", report)
	}
	if port, ok := report[0].(int); !ok || port != util.GetRequestStore(req).RequestPortNum {
		t.Errorf("report port = %v, want the port of the request", report[0])
	}
}

func TestTrackPortTrafficEventIsProcessed(t *testing.T) {
	newEnv(t)
	startSender(t)
	TrackPortTrafficEvent(4040, "Operation", 200, "x")
	TrackPortTrafficEvent(4040, "Operation", 200, "x")
	waitFor(t, "the traffic to be tracked", func() bool {
		tr := tracker(4040, "Operation")
		return tr != nil && tr.StatusRepeatCount == 2
	})
	waitFor(t, "the first request event", func() bool {
		return reflect.DeepEqual(titles(stored()), []string{"URI First Request"})
	})
}

func TestFlushEventsReportsAndResetsTraffic(t *testing.T) {
	e := newEnv(t)
	track(1, "/a", 200, "x")
	track(1, "/a", 200, "x")
	track(2, "/b", 404)
	FlushEvents()
	list := stored()
	if len(list) != 1 || list[0].Title != "Flushed Traffic Report" {
		t.Fatalf("stored %v, want the flushed report", titles(list))
	}
	trackers, ok := list[0].Data.([]*EventTracker)
	if !ok || len(trackers) != 2 {
		t.Fatalf("report data = %+v, want 2 trackers", list[0].Data)
	}
	counts := map[string]int{}
	for _, tr := range trackers {
		counts[fmt.Sprintf("%d%s", tr.Port, tr.URI)] = tr.StatusRepeatCount
	}
	if !reflect.DeepEqual(counts, map[string]int{"1/a": 2, "2/b": 1}) {
		t.Errorf("reported counts = %v", counts)
	}
	if tracker(1, "/a") != nil || tracker(2, "/b") != nil {
		t.Error("trackers were not reset by the flush")
	}
	if n := len(e.lockerEvents()); n != 1 {
		t.Errorf("locker got %d events, want the flush report", n)
	}
	// What is counted after the flush is not part of the report that was made.
	track(1, "/a", 200, "x")
	if trackers[0].StatusRepeatCount+trackers[1].StatusRepeatCount != 3 {
		t.Errorf("the reported trackers changed after the flush: %+v %+v", trackers[0], trackers[1])
	}
}

func TestFlushEventsWithoutTrafficReportsEmptyList(t *testing.T) {
	newEnv(t)
	FlushEvents()
	list := stored()
	if len(list) != 1 {
		t.Fatalf("stored %d events, want 1", len(list))
	}
	if trackers, ok := list[0].Data.([]*EventTracker); !ok || len(trackers) != 0 {
		t.Errorf("data = %#v, want an empty list of trackers", list[0].Data)
	}
}

func TestClearEventsRemovesEventsAndTraffic(t *testing.T) {
	newEnv(t)
	SendEventDirect("old", "d")
	track(1, "/a", 200)
	ClearEvents()
	if n := len(stored()); n != 0 {
		t.Errorf("%d events left after clearing", n)
	}
	if tracker(1, "/a") != nil {
		t.Error("traffic trackers left after clearing")
	}
	// The clearing leaves a note, which the sender stores.
	if got := titles(queued()); !reflect.DeepEqual(got, []string{"Events Cleared"}) {
		t.Errorf("queued %v, want the note about the clearing", got)
	}
}

func TestSortEvents(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mk := func(title string, sec int) *Event {
		return &Event{Title: title, At: base.Add(time.Duration(sec) * time.Second)}
	}
	list := func() []*Event { return []*Event{mk("b", 2), mk("a", 1), mk("d", 4), mk("c", 3)} }
	asc, desc := list(), list()
	SortEvents(asc, false)
	SortEvents(desc, true)
	if got := titles(asc); !reflect.DeepEqual(got, []string{"a", "b", "c", "d"}) {
		t.Errorf("ascending = %v", got)
	}
	if got := titles(desc); !reflect.DeepEqual(got, []string{"d", "c", "b", "a"}) {
		t.Errorf("descending = %v", got)
	}
	// Events of the same time stay in their order.
	same := []*Event{mk("x", 1), mk("y", 1), mk("z", 1)}
	SortEvents(same, true)
	if got := titles(same); !reflect.DeepEqual(got, []string{"x", "y", "z"}) {
		t.Errorf("same time = %v, want the order kept", got)
	}
	SortEvents(nil, true)
}

func seed(events ...*Event) {
	lock.Lock()
	eventsList = events
	lock.Unlock()
}

func at(sec int) time.Time { return time.Date(2026, 1, 1, 0, 0, sec, 0, time.UTC) }

func TestAPIGetEventsHidesDataByDefault(t *testing.T) {
	newEnv(t)
	seed(
		&Event{Title: "second", Summary: "s2", Data: map[string]string{"k": "v"}, At: at(2), Peer: "p", PeerHost: "h"},
		&Event{Title: "first", Summary: "s1", Data: "text", At: at(1), Peer: "p", PeerHost: "h"},
	)
	rr := admin("GET", "/events")
	if rr.Code != http.StatusOK || rr.Header().Get(constants.HeaderContentType) != constants.ContentTypeJSON {
		t.Fatalf("answered %d with content type %q", rr.Code, rr.Header().Get(constants.HeaderContentType))
	}
	list := decodeEvents(t, rr)
	if len(list) != 2 || list[0].Title != "first" || list[1].Title != "second" {
		t.Fatalf("events = %+v, want 2 in order of time", list)
	}
	for _, ev := range list {
		if ev.Data != "..." || ev.Peer != "p" || ev.PeerHost != "h" {
			t.Errorf("event %+v, want its data hidden and its peer kept", ev)
		}
	}
	if list[0].Summary != "s1" {
		t.Errorf("summary = %q, want it kept", list[0].Summary)
	}
}

func TestAPIGetEventsWithDataAndReverse(t *testing.T) {
	newEnv(t)
	seed(
		&Event{Title: "first", Data: "text", At: at(1)},
		&Event{Title: "second", Data: map[string]string{"k": "v"}, At: at(2)},
	)
	list := decodeEvents(t, admin("GET", "/events?data=true&reverse=true"))
	if len(list) != 2 || list[0].Title != "second" || list[1].Title != "first" {
		t.Fatalf("events = %+v, want the newest first", list)
	}
	if !reflect.DeepEqual(list[0].Data, map[string]interface{}{"k": "v"}) || list[1].Data != "text" {
		t.Errorf("data = %v, %v, want the data of the events", list[0].Data, list[1].Data)
	}
	list = decodeEvents(t, admin("GET", "/events?reverse=true&data=false"))
	if len(list) != 2 || list[0].Title != "second" || list[0].Data != "..." {
		t.Errorf("events = %+v, want the newest first, without data", list)
	}
	list = decodeEvents(t, admin("GET", "/events?data=true&reverse=false"))
	if len(list) != 2 || list[0].Title != "first" || list[1].Data == "..." {
		t.Errorf("events = %+v, want the oldest first, with data", list)
	}
}

func TestAPIGetEventsLeavesStoredDataAlone(t *testing.T) {
	newEnv(t)
	seed(&Event{Title: "t", Data: "secret", At: at(1)})
	admin("GET", "/events")
	if got := stored()[0].Data; got != "secret" {
		t.Errorf("stored data = %v, want it kept when a listing hides it", got)
	}
}

func TestAPIGetEventsWhenEmpty(t *testing.T) {
	newEnv(t)
	rr := admin("GET", "/events")
	if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != "[]" {
		t.Errorf("answered %d %q, want an empty list", rr.Code, rr.Body.String())
	}
}

func TestAPIGetEventsAcceptsTrailingSlash(t *testing.T) {
	newEnv(t)
	seed(&Event{Title: "t", At: at(1)})
	if rr := admin("GET", "/events/"); rr.Code != http.StatusOK || len(decodeEvents(t, rr)) != 1 {
		t.Errorf("answered %d %q", rr.Code, rr.Body.String())
	}
}

func TestAPIMethodsAreChecked(t *testing.T) {
	newEnv(t)
	for _, c := range []struct{ method, target string }{
		{"POST", "/events"}, {"GET", "/events/flush"}, {"GET", "/events/clear"}, {"POST", "/events/search/x"},
	} {
		if rr := admin(c.method, c.target); rr.Code != http.StatusMethodNotAllowed && rr.Code != http.StatusNotFound {
			t.Errorf("%s %s answered %d, want it not to be served", c.method, c.target, rr.Code)
		}
	}
}

func TestAPISearchEvents(t *testing.T) {
	newEnv(t)
	seed(
		&Event{Title: "Target Added", Summary: "x", Data: "alpha", At: at(1)},
		&Event{Title: "Job Started", Summary: "y", Data: map[string]string{"name": "Beta"}, At: at(2)},
		&Event{Title: "Target Invoked", Summary: "z", Data: "gamma", At: at(3)},
	)
	tests := []struct {
		name   string
		target string
		want   []string
		data   bool
	}{
		{"by title, in any case", "/events/search/target", []string{"Target Added", "Target Invoked"}, false},
		{"by data", "/events/search/beta", []string{"Job Started"}, false},
		{"by regular expression", "/events/search/(alpha|gamma)", []string{"Target Added", "Target Invoked"}, false},
		{"reversed", "/events/search/target?reverse=true&data=false", []string{"Target Invoked", "Target Added"}, false},
		{"with data", "/events/search/job?data=true&reverse=false", []string{"Job Started"}, true},
		{"nothing matches", "/events/search/nomatch", []string{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := admin("GET", tt.target)
			if rr.Code != http.StatusOK {
				t.Fatalf("answered %d %q", rr.Code, rr.Body.String())
			}
			list := decodeEvents(t, rr)
			got := []string{}
			for _, ev := range list {
				got = append(got, ev.Title)
				if hidden := ev.Data == "..."; hidden == tt.data {
					t.Errorf("event %q data = %v, want it shown only when the data is asked for", ev.Title, ev.Data)
				}
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("found %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAPIFlushEvents(t *testing.T) {
	newEnv(t)
	track(1, "/a", 200)
	rr := admin("POST", "/events/flush")
	if rr.Code != http.StatusOK {
		t.Fatalf("answered %d", rr.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil || body["flushed"] != true {
		t.Errorf("body = %q, want flushed: true", rr.Body.String())
	}
	if got := titles(stored()); !reflect.DeepEqual(got, []string{"Flushed Traffic Report"}) {
		t.Errorf("stored %v, want the flushed report", got)
	}
	if tracker(1, "/a") != nil {
		t.Error("traffic was not reset")
	}
	if got := titles(queued()); !reflect.DeepEqual(got, []string{"Events Flushed"}) {
		t.Errorf("queued %v, want a note about the flush", got)
	}
}

func TestAPIFlushEventsWhenDisabled(t *testing.T) {
	newEnv(t)
	global.Flags.EnableEvents = false
	rr := admin("POST", "/events/flush")
	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil || body["flushed"] != false || body["error"] != "Events not enabled" {
		t.Errorf("body = %q, want flushed: false with the reason", rr.Body.String())
	}
}

func TestAPIClearEvents(t *testing.T) {
	newEnv(t)
	seed(&Event{Title: "old", At: at(1)})
	rr := admin("POST", "/events/clear")
	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil || body["cleared"] != true {
		t.Errorf("body = %q, want cleared: true", rr.Body.String())
	}
	if n := len(stored()); n != 0 {
		t.Errorf("%d events left after clearing", n)
	}
}

func TestAPIClearEventsWhenDisabled(t *testing.T) {
	newEnv(t)
	global.Flags.EnableEvents = false
	seed(&Event{Title: "kept", At: at(1)})
	rr := admin("POST", "/events/clear")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Events not enabled") {
		t.Errorf("answered %d %q, want the reason", rr.Code, rr.Body.String())
	}
	if n := len(stored()); n != 1 {
		t.Errorf("%d events left, want the one that was there", n)
	}
}

func TestMiddlewareTracksTrafficOfRequests(t *testing.T) {
	newEnv(t)
	startSender(t)
	h := Middleware.MiddlewareHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		util.UpdateTrafficEventStatusCode(r, 418)
		util.UpdateTrafficEventDetails(r, "from handler")
		w.WriteHeader(418)
	}))
	req := httptest.NewRequest("GET", "/Some/Path", nil)
	req = req.WithContext(withPort(req, 8081))
	_, req, rs := util.WithRequestStore(req, nil)
	h.ServeHTTP(httptest.NewRecorder(), req)
	port := rs.RequestPortNum
	waitFor(t, "the traffic to be tracked", func() bool { return tracker(port, "/some/path") != nil })
	tr := tracker(port, "/some/path")
	if tr.StatusCode != 418 || !reflect.DeepEqual(tr.TrafficDetails, []string{"from handler"}) {
		t.Errorf("tracker = %+v, want the status and details that the handler recorded", tr)
	}
}

func TestMiddlewareSkipsNonTraffic(t *testing.T) {
	newEnv(t)
	h := Middleware.MiddlewareHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	for name, set := range map[string]func(*util.RequestStore){
		"known non-traffic": func(rs *util.RequestStore) { rs.IsKnownNonTraffic = true },
		"filtered":          func(rs *util.RequestStore) { rs.IsFilteredRequest = true },
	} {
		req := newRequest("GET", "/x", nil)
		set(util.GetRequestStore(req))
		h.ServeHTTP(httptest.NewRecorder(), req)
		if len(trafficChannel) != 0 {
			t.Errorf("%s request was tracked", name)
			<-trafficChannel
		}
	}
}

func TestMiddlewareWithoutNextHandler(t *testing.T) {
	newEnv(t)
	req := newRequest("GET", "/x", nil)
	Middleware.MiddlewareHandler(nil).ServeHTTP(httptest.NewRecorder(), req)
	if len(trafficChannel) != 1 {
		t.Errorf("tracked %d requests, want 1", len(trafficChannel))
	}
	<-trafficChannel
}

func TestMiddlewareIdentity(t *testing.T) {
	if Middleware == nil || Middleware.Name != "events" || Middleware.SetRoutes == nil || Middleware.MiddlewareHandler == nil {
		t.Errorf("middleware = %+v", Middleware)
	}
}
