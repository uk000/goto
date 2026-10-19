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
	"fmt"
	. "goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/server/middleware"
	"goto/pkg/transport"
	"goto/pkg/util"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
)

type Event struct {
	Title    string      `json:"title"`
	Summary  string      `json:"summary"`
	Data     interface{} `json:"data"`
	At       time.Time   `json:"at"`
	Peer     string      `json:"peer"`
	PeerHost string      `json:"peerHost"`
}

type EventTracker struct {
	Port              int       `json:"port"`
	URI               string    `json:"uri"`
	StatusCode        int       `json:"statusCode"`
	TrafficDetails    []string  `json:"trafficDetails"`
	StatusRepeatCount int       `json:"statusRepeatCount"`
	FirstEventAt      time.Time `json:"firstEventAt"`
	LastEventAt       time.Time `json:"lastEventAt"`
}

var (
	Middleware          = middleware.NewMiddleware("events", setRoutes, middlewareFunc)
	eventsList          = []*Event{}
	trafficEventTracker = map[int]map[string]*EventTracker{}
	eventChannel        = make(chan *Event, 100)
	trafficChannel      = make(chan []interface{}, 100)
	stopSender          = make(chan bool, 10)
	registryClient      = transport.CreateDefaultHTTPClient(global.Self.ServerPort, "EventsRegistrySender", true, false, false, "", nil)
	lock                sync.RWMutex
	// senders has the goroutines that StartSender starts, for the ones that need to know that those are done.
	senders sync.WaitGroup
)

func setRoutes(r *mux.Router) {
	eventsRouter := middleware.RootPath("/events")
	util.AddRoute(eventsRouter, "/flush", flushEvents, "POST")
	util.AddRoute(eventsRouter, "/clear", clearEvents, "POST")
	util.AddRouteWithMultiQ(eventsRouter, "/search/{text}", searchEvents, [][]string{{}, {"data", "reverse"}}, "GET")
	util.AddRouteWithMultiQ(eventsRouter, "", getEvents, [][]string{{}, {"data", "reverse"}}, "GET")
}

func StartSender() {
	if global.Flags.EnableEvents {
		senders.Add(2)
		go func() {
			defer senders.Done()
			eventSender()
		}()
		go func() {
			defer senders.Done()
			trafficEventsProcessor()
		}()
	}
}

// StopSender flushes the traffic report and stops the two goroutines that StartSender starts, each of which takes one
// stop signal.
func StopSender() {
	if global.Flags.EnableEvents {
		FlushEvents()
		stopSender <- true
		stopSender <- true
	}
}

func newEvent(title, summary string, data interface{}, at time.Time, peer, host string) *Event {
	if summary == "" {
		if text, ok := data.(string); ok {
			n := len(text)
			if n > 20 {
				summary = text[:20] + "..."
			} else {
				summary = text[:n]
			}
		}
	}
	return &Event{Title: title, Summary: summary, Data: data, At: at, Peer: peer, PeerHost: host}
}

func newRequestEvent(title, summary string, data interface{}, at time.Time, r *http.Request) *Event {
	host := ""
	if r != nil {
		host = util.GetCurrentListenerLabel(r)
	} else {
		host = global.Self.HostLabel
	}
	return newEvent(title, summary, data, at, global.Self.Name, host)
}

func newPortEvent(title, summary string, data interface{}, at time.Time, port int) *Event {
	return newEvent(title, summary, data, at, global.Self.Name, global.Self.HostLabel)
}

func SendRequestEvent(title, data string, r *http.Request) time.Time {
	at := time.Now()
	if global.Flags.EnableEvents {
		event := newRequestEvent(title, "", data, at, r)
		eventChannel <- event
	}
	return at
}

func SendEvent(title, data string) time.Time {
	return SendEventForPort(global.Self.ServerPort, title, data)
}

func SendEventForPort(port int, title, data string) time.Time {
	at := time.Now()
	sendEventForPortAt(at, port, title, "", data)
	return at
}

func sendEventForPortAt(at time.Time, port int, title, summary string, data interface{}) {
	if global.Flags.EnableEvents {
		eventChannel <- newPortEvent(title, summary, data, at, port)
	}
}

func SendEventDirect(title, data string) time.Time {
	at := time.Now()
	if global.Flags.EnableEvents {
		event := newPortEvent(title, "", data, at, global.Self.ServerPort)
		storeAndPublishEvent(event)
	}
	return at
}

func SendRequestEventJSON(title, summary string, data interface{}, r *http.Request) time.Time {
	at := time.Now()
	if global.Flags.EnableEvents {
		event := newRequestEvent(title, summary, data, at, r)
		eventChannel <- event
	}
	return at
}

func SendEventJSON(title, summary string, data interface{}) time.Time {
	return SendEventJSONForPort(global.Self.ServerPort, title, summary, data)
}

func SendEventJSONForPort(port int, title, summary string, data interface{}) time.Time {
	at := time.Now()
	if global.Flags.EnableEvents {
		event := newPortEvent(title, summary, data, at, port)
		eventChannel <- event
	}
	return at
}

func SendEventJSONDirect(title, summary string, data interface{}) time.Time {
	at := time.Now()
	if global.Flags.EnableEvents {
		event := newPortEvent(title, summary, data, at, global.Self.ServerPort)
		storeAndPublishEvent(event)
	}
	return at
}

func TrackTrafficEvent(statusCode int, r *http.Request, details ...string) {
	if global.Flags.EnableEvents {
		rs := util.GetRequestStore(r)
		trafficChannel <- []interface{}{rs.RequestPortNum, strings.ToLower(rs.RequestURI), statusCode, details}
	}
}

func TrackPortTrafficEvent(port int, operation string, statusCode int, details ...string) {
	if global.Flags.EnableEvents {
		trafficChannel <- []interface{}{port, operation, statusCode, details}
	}
}

func FlushEvents() {
	if global.Flags.EnableEvents {
		// The trackers are copied, and removed in the same step, as the ones that are being counted into must not be
		// reported (and encoded) while they change, and what is counted between a report and a reset must not be lost.
		lock.Lock()
		trackers := []*EventTracker{}
		for _, tt := range trafficEventTracker {
			for _, t := range tt {
				trackers = append(trackers, t.copy())
			}
		}
		trafficEventTracker = map[int]map[string]*EventTracker{}
		lock.Unlock()
		SendEventJSONDirect("Flushed Traffic Report", "", trackers)
	}
}

func ClearEvents() {
	if global.Flags.EnableEvents {
		lock.Lock()
		eventsList = []*Event{}
		trafficEventTracker = map[int]map[string]*EventTracker{}
		lock.Unlock()
		SendEvent("Events Cleared", "")
	}
}

func trafficEventsProcessor() {
TrafficLoop:
	for {
		if len(trafficChannel) > 50 {
			log.Printf("trafficEventsProcessor: trafficChannel length %d\n", len(trafficChannel))
		}
		select {
		case <-stopSender:
			break TrafficLoop
		case traffic := <-trafficChannel:
			processTrafficEvent(traffic)
		}
	}
}

func (t *EventTracker) summary() string {
	return fmt.Sprintf("Port [%d] URI [%s] Status [%d] Traffic Details [%s] Repeated x[%d]",
		t.Port, t.URI, t.StatusCode, strings.Join(t.TrafficDetails, ","), t.StatusRepeatCount)
}

func (t *EventTracker) copy() *EventTracker {
	c := *t
	c.TrafficDetails = append([]string{}, t.TrafficDetails...)
	return &c
}

func processTrafficEvent(traffic []interface{}) {
	if len(traffic) < 4 {
		return
	}
	port, ok1 := traffic[0].(int)
	uri, ok2 := traffic[1].(string)
	statusCode, ok3 := traffic[2].(int)
	trafficDetails, ok4 := traffic[3].([]string)
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return
	}
	// The tracker is changed with the lock held, but the events are queued after it is released: the sender that takes
	// events off the queue needs the lock to store them, so a full queue would not drain.
	var sends []func()
	lock.Lock()
	portTrafficEventTracker := trafficEventTracker[port]
	if portTrafficEventTracker == nil {
		portTrafficEventTracker = map[string]*EventTracker{}
		trafficEventTracker[port] = portTrafficEventTracker
	}
	tracker := portTrafficEventTracker[uri]
	oldStatusCode := -1
	oldDetails := []string{}
	if tracker != nil && (tracker.StatusCode != statusCode ||
		len(tracker.TrafficDetails) != len(trafficDetails) ||
		(len(tracker.TrafficDetails) > 0 && len(trafficDetails) > 0 &&
			!strings.EqualFold(tracker.TrafficDetails[0], trafficDetails[0]))) {
		oldStatusCode = tracker.StatusCode
		oldDetails = tracker.TrafficDetails
		if tracker.StatusRepeatCount > 1 {
			repeated := tracker.copy()
			now := time.Now()
			sends = append(sends, func() {
				sendEventForPortAt(now, port, "Repeated URI Status", repeated.summary(), repeated)
			})
		}
		tracker = nil
	}
	if tracker == nil {
		title := ""
		details := ""
		if oldStatusCode == -1 {
			title = "URI First Request"
			details = fmt.Sprintf("Port [%d] URI [%s] First Request with Status [%d] Traffic Details [%s]", port, uri, statusCode, strings.Join(trafficDetails, ","))
		} else {
			title = "URI Status / Details Changed"
			details = fmt.Sprintf("Port [%d] URI [%s] Old Status [%d] New Status [%d] Traffic Old Details [%s] New Details [%s]",
				port, uri, oldStatusCode, statusCode, strings.Join(oldDetails, ","), strings.Join(trafficDetails, ","))
		}
		ts := time.Now()
		sends = append(sends, func() { sendEventForPortAt(ts, port, title, "", details) })
		tracker = &EventTracker{Port: port, URI: uri, StatusCode: statusCode, TrafficDetails: trafficDetails, StatusRepeatCount: 1, FirstEventAt: ts, LastEventAt: ts}
		portTrafficEventTracker[uri] = tracker
	} else {
		tracker.LastEventAt = time.Now()
		tracker.StatusRepeatCount++
	}
	lock.Unlock()
	for _, send := range sends {
		send()
	}
}

func storeAndPublishEvent(event *Event) {
	if global.Flags.EnableEvents {
		lock.Lock()
		eventsList = append(eventsList, event)
		lock.Unlock()
		if global.Flags.PublishEvents && global.Self.RegistryURL != "" {
			url := fmt.Sprintf("%s/registry/peers/%s/%s/events/store", global.Self.RegistryURL, event.Peer, event.PeerHost)
			if resp, err := registryClient.HTTP().Post(url, ContentTypeJSON,
				strings.NewReader(util.ToJSONText(event))); err == nil {
				util.CloseResponse(resp)
			}
		} else {
			global.Funcs.StoreEventInCurrentLocker(event)

		}
	}
}

func eventSender() {
SendLoop:
	for {
		if len(eventChannel) > 50 {
			log.Printf("eventSender: eventChannel length %d\n", len(eventChannel))
		}
		select {
		case <-stopSender:
			break SendLoop
		case event := <-eventChannel:
			storeAndPublishEvent(event)
		}
	}
}

func flushEvents(w http.ResponseWriter, r *http.Request) {
	msg := ""
	if global.Flags.EnableEvents {
		FlushEvents()
		msg = "Events Flushed"
		SendEvent(msg, "")
		fmt.Fprintln(w, util.ToJSONText(map[string]interface{}{"flushed": true}))
	} else {
		msg = "Events not enabled"
		fmt.Fprintln(w, util.ToJSONText(map[string]interface{}{"flushed": false, "error": msg}))
	}
	util.AddLogMessage(msg, r)
}

func clearEvents(w http.ResponseWriter, r *http.Request) {
	msg := ""
	if global.Flags.EnableEvents {
		ClearEvents()
		fmt.Fprintln(w, util.ToJSONText(map[string]interface{}{"cleared": true}))
	} else {
		msg = "Events not enabled"
		fmt.Fprintln(w, util.ToJSONText(map[string]interface{}{"flushed": false, "error": msg}))
	}
	util.AddLogMessage(msg, r)
}

func SortEvents(eventsList []*Event, reverse bool) {
	sort.SliceStable(eventsList, func(i, j int) bool {
		if reverse {
			return eventsList[i].At.After(eventsList[j].At)
		} else {
			return eventsList[i].At.Before(eventsList[j].At)
		}
	})
}

func getEvents(w http.ResponseWriter, r *http.Request) {
	reverse := util.GetBoolParamValue(r, "reverse")
	getData := util.GetBoolParamValue(r, "data")
	filteredEvents := []*Event{}
	lock.RLock()
	if getData {
		for _, event := range eventsList {
			filteredEvents = append(filteredEvents, event)
		}
	} else {
		for _, event := range eventsList {
			filteredEvents = append(filteredEvents, newEvent(event.Title, event.Summary, "...", event.At, event.Peer, event.PeerHost))
		}
	}
	lock.RUnlock()
	SortEvents(filteredEvents, reverse)
	util.WriteJsonPayload(w, filteredEvents)
}

func searchEvents(w http.ResponseWriter, r *http.Request) {
	msg := ""
	key := util.GetStringParamValue(r, "text")
	reverse := util.GetBoolParamValue(r, "reverse")
	getData := util.GetBoolParamValue(r, "data")
	if key == "" {
		msg = "Cannot search. No key given."
		fmt.Fprintln(w, msg)
	} else {
		pattern, err := regexp.Compile("(?i)" + key)
		if err != nil {
			msg = fmt.Sprintf("Cannot search. Invalid key [%s]: %s", key, err.Error())
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintln(w, msg)
			util.AddLogMessage(msg, r)
			return
		}
		filteredEvents := []*Event{}
		unfilteredEvents := []*Event{}
		lock.RLock()
		for _, event := range eventsList {
			unfilteredEvents = append(unfilteredEvents, event)
		}
		lock.RUnlock()
		for _, event := range unfilteredEvents {
			data := util.ToJSONText(event)
			if pattern.MatchString(data) {
				if !getData {
					event = newEvent(event.Title, event.Summary, "...", event.At, event.Peer, event.PeerHost)
				}
				filteredEvents = append(filteredEvents, event)
			}
		}
		SortEvents(filteredEvents, reverse)
		util.WriteJsonPayload(w, filteredEvents)
		msg = fmt.Sprintf("Reported results for key [%s] search", key)
	}
	util.AddLogMessage(msg, r)
}

func middlewareFunc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if next != nil {
			next.ServeHTTP(w, r)
		}
		rs := util.GetRequestStore(r)
		if !rs.IsKnownNonTraffic && !rs.IsFilteredRequest {
			statusCode, details := util.ReportTrafficEvent(r)
			if details != nil {
				TrackTrafficEvent(statusCode, r, details...)
			} else {
				TrackTrafficEvent(statusCode, r)
			}
		}
	})
}
