package events

import (
  "fmt"
  "goto/pkg/global"
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
  Handler             = util.ServerHandler{Name: "events", SetRoutes: SetRoutes, Middleware: Middleware}
  eventsList          = []*Event{}
  trafficEventTracker = map[int]map[string]*EventTracker{}
  eventChannel        = make(chan *Event, 100)
  trafficChannel      = make(chan []interface{}, 100)
  stopSender          = make(chan bool, 10)
  registryClient      = util.CreateHttpClient()
  lock                sync.RWMutex
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  eventsRouter := r.PathPrefix("/events").Subrouter()
  util.AddRoute(eventsRouter, "/flush", flushEvents, "POST")
  util.AddRoute(eventsRouter, "/clear", clearEvents, "POST")
  util.AddRoute(eventsRouter, "/search/{text}", searchEvents, "GET")
  util.AddRoute(eventsRouter, "/search/{text}/reverse", searchEvents, "GET")
  util.AddRoute(eventsRouter, "/reverse", getEvents, "GET")
  util.AddRoute(eventsRouter, "", getEvents, "GET")
}

func StartSender() {
  if global.EnableEvents {
    go eventSender()
    go trafficEventsProcessor()
  }
}

func StopSender() {
  if global.EnableEvents {
    FlushEvents()
    stopSender <- true
  }
}

func newRequestEvent(title string, data interface{}, at time.Time, r *http.Request) *Event {
  host := ""
  if r != nil {
    host = util.GetCurrentListenerLabel(r)
  } else {
    host = global.HostLabel
  }
  return &Event{Title: title, Data: data, At: at, Peer: global.PeerName, PeerHost: host}
}

func newPortEvent(title string, data interface{}, at time.Time, port int) *Event {
  return &Event{Title: title, Data: data, At: at, Peer: global.PeerName, PeerHost: global.GetHostLabelForPort(port)}
}

func SendRequestEvent(title, data string, r *http.Request) time.Time {
  at := time.Now()
  if global.EnableEvents {
    event := newRequestEvent(title, map[string]string{"details": data}, at, r)
    eventChannel <- event
  }
  return at
}

func SendEvent(title, data string) time.Time {
  return SendEventForPort(global.ServerPort, title, data)
}

func SendEventForPort(port int, title, data string) time.Time {
  at := time.Now()
  if global.EnableEvents {
    event := newPortEvent(title, map[string]string{"details": data}, at, port)
    eventChannel <- event
  }
  return at
}

func SendEventDirect(title, data string) time.Time {
  at := time.Now()
  if global.EnableEvents {
    event := newPortEvent(title, map[string]string{"details": data}, at, global.ServerPort)
    storeAndPublishEvent(event)
  }
  return at
}

func SendRequestEventJSON(title string, data interface{}, r *http.Request) time.Time {
  at := time.Now()
  if global.EnableEvents {
    event := newRequestEvent(title, data, at, r)
    eventChannel <- event
  }
  return at
}

func SendEventJSON(title string, data interface{}) time.Time {
  return SendEventJSONForPort(global.ServerPort, title, data)
}

func SendEventJSONForPort(port int, title string, data interface{}) time.Time {
  at := time.Now()
  if global.EnableEvents {
    event := newPortEvent(title, data, at, port)
    eventChannel <- event
  }
  return at
}

func SendEventJSONDirect(title string, data interface{}) time.Time {
  at := time.Now()
  if global.EnableEvents {
    event := newPortEvent(title, data, at, global.ServerPort)
    storeAndPublishEvent(event)
  }
  return at
}

func TrackTrafficEvent(statusCode int, r *http.Request, details ...string) {
  if global.EnableEvents {
    trafficChannel <- []interface{}{util.GetCurrentPort(r), strings.ToLower(r.URL.Path), statusCode, details}
  }
}

func TrackPortTrafficEvent(port int, operation string, statusCode int, details ...string) {
  if global.EnableEvents {
    trafficChannel <- []interface{}{port, operation, statusCode, details}
  }
}

func FlushEvents() {
  if global.EnableEvents {
    lock.RLock()
    trackers := []*EventTracker{}
    for _, tt := range trafficEventTracker {
      for _, t := range tt {
        trackers = append(trackers, t)
      }
    }
    lock.RUnlock()
    SendEventJSONDirect("Flushed Traffic Report", trackers)
    lock.Lock()
    trafficEventTracker = map[int]map[string]*EventTracker{}
    lock.Unlock()
  }
}

func ClearEvents() {
  if global.EnableEvents {
    lock.Lock()
    eventsList = []*Event{}
    trafficEventTracker = map[int]map[string]*EventTracker{}
    lock.Unlock()
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
      SendEventJSONForPort(port, "Repeated URI Status", map[string]interface{}{"details": tracker.summary(), "data": tracker})
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
  if global.EnableEvents {
    lock.Lock()
    eventsList = append(eventsList, event)
    lock.Unlock()
    if global.PublishEvents && global.RegistryURL != "" {
      url := fmt.Sprintf("%s/registry/peers/%s/%s/events/store", global.RegistryURL, event.Peer, event.PeerHost)
      if resp, err := registryClient.Post(url, "application/json",
        strings.NewReader(util.ToJSON(event))); err == nil {
        util.CloseResponse(resp)
      }
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
  if global.EnableEvents {
    FlushEvents()
    msg = "Events Flushed"
    SendEvent(msg, "")
    fmt.Fprintln(w, util.ToJSON(map[string]interface{}{"flushed": true}))
  } else {
    msg = "Events not enabled"
    fmt.Fprintln(w, util.ToJSON(map[string]interface{}{"flushed": false, "error": msg}))
  }
  util.AddLogMessage(msg, r)
}

func clearEvents(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if global.EnableEvents {
    ClearEvents()
    msg = "Events Cleared"
    SendEvent(msg, "")
    fmt.Fprintln(w, util.ToJSON(map[string]interface{}{"cleared": true}))
  } else {
    msg = "Events not enabled"
    fmt.Fprintln(w, util.ToJSON(map[string]interface{}{"flushed": false, "error": msg}))
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
  lock.RLock()
  defer lock.RUnlock()
  reverse := strings.Contains(r.RequestURI, "reverse")
  SortEvents(eventsList, reverse)
  util.WriteJsonPayload(w, eventsList)
}

func searchEvents(w http.ResponseWriter, r *http.Request) {
  msg := ""
  key := util.GetStringParamValue(r, "text")
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
      data := fmt.Sprint(event.Data)
      if pattern.MatchString(event.Title) || pattern.MatchString(data) ||
        pattern.MatchString(event.PeerHost) || pattern.MatchString(event.At.String()) {
        filteredEvents = append(filteredEvents, event)
      }
    }
    reverse := strings.Contains(r.RequestURI, "reverse")
    SortEvents(filteredEvents, reverse)
    util.WriteJsonPayload(w, filteredEvents)
    msg = fmt.Sprintf("Reported results for key [%s] search", key)
  }
  util.AddLogMessage(msg, r)
}

func Middleware(next http.Handler) http.Handler {
  return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    if next != nil {
      next.ServeHTTP(w, r)
    }
    if !util.IsAdminRequest(r) {
      statusCode, details := util.ReportTrafficEvent(r)
      if details != nil {
        TrackTrafficEvent(statusCode, r, details...)
      } else {
        TrackTrafficEvent(statusCode, r)
      }
    }
  })
}
