/**
 * Copyright 2025 uk
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
	registryClient      = transport.CreateDefaultHTTPClient("EventsRegistrySender", true, false, nil)
	lock                sync.RWMutex
)

func setRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
	eventsRouter := r.PathPrefix("/events").Subrouter()
	util.AddRoute(eventsRouter, "/flush", flushEvents, "POST")
	util.AddRoute(eventsRouter, "/clear", clearEvents, "POST")
	util.AddRouteMultiQ(eventsRouter, "/search/{text}", searchEvents, []string{"data", "reverse"}, "GET")
	util.AddRoute(eventsRouter, "/search/{text}", searchEvents, "GET")
	util.AddRouteMultiQ(eventsRouter, "", getEvents, []string{"data", "reverse"}, "GET")
	util.AddRoute(eventsRouter, "", getEvents, "GET")
}

func StartSender() {
	if global.Flags.EnableEvents {
		go eventSender()
		go trafficEventsProcessor()
	}
}

func StopSender() {
	if global.Flags.EnableEvents {
		FlushEvents()
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
	return newEvent(title, summary, data, at, global.Self.Name, global.Funcs.GetHostLabelForPort(port))
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
	if global.Flags.EnableEvents {
		event := newPortEvent(title, "", data, at, port)
		eventChannel <- event
	}
	return at
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
		trafficChannel <- []interface{}{util.GetCurrentPort(r), strings.ToLower(r.URL.Path), statusCode, details}
	}
}

func TrackPortTrafficEvent(port int, operation string, statusCode int, details ...string) {
	if global.Flags.EnableEvents {
		trafficChannel <- []interface{}{port, operation, statusCode, details}
	}
}

func FlushEvents() {
	if global.Flags.EnableEvents {
		lock.RLock()
		trackers := []*EventTracker{}
		for _, tt := range trafficEventTracker {
			for _, t := range tt {
				trackers = append(trackers, t)
			}
		}
		lock.RUnlock()
		SendEventJSONDirect("Flushed Traffic Report", "", trackers)
		lock.Lock()
		trafficEventTracker = map[int]map[string]*EventTracker{}
		lock.Unlock()
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
			log.Printf("trafficEventsProcessor: trafficChannel length %d\n", len(eventChannel))
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

func processTrafficEvent(traffic []interface{}) {
	if len(traffic) < 3 {
		return
	}
	port := traffic[0].(int)
	uri := traffic[1].(string)
	statusCode := traffic[2].(int)
	trafficDetails := traffic[3].([]string)

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
			SendEventJSONForPort(port, "Repeated URI Status", tracker.summary(), tracker)
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
		ts := SendEventForPort(port, title, details)
		tracker = &EventTracker{Port: port, URI: uri, StatusCode: statusCode, TrafficDetails: trafficDetails, StatusRepeatCount: 1, FirstEventAt: ts, LastEventAt: ts}
		portTrafficEventTracker[uri] = tracker
	} else {
		tracker.LastEventAt = time.Now()
		tracker.StatusRepeatCount++
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
		filteredEvents := []*Event{}
		unfilteredEvents := []*Event{}
		pattern := regexp.MustCompile("(?i)" + key)
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
