package trigger

import (
  "fmt"
  "goto/pkg/events"
  "goto/pkg/invocation"
  "goto/pkg/metrics"
  "goto/pkg/util"
  "net/http"
  "strings"
  "sync"

  "github.com/gorilla/mux"
)

type TriggerTarget struct {
  Name         string     `json:"name"`
  Method       string     `json:"method"`
  URL          string     `json:"url"`
  Headers      [][]string `json:"headers"`
  Body         string     `json:"body"`
  SendID       bool       `json:"sendID"`
  Enabled      bool       `json:"enabled"`
  TriggerOn    []int      `json:"triggerOn"`
  StartFrom    int        `json:"startFrom"`
  StopAt       int        `json:"stopAt"`
  StatusCount  int        `json:"statusCount"`
  TriggerCount int        `json:"triggerCount"`
  lock         sync.RWMutex
}

type Trigger struct {
  Targets                 map[string]*TriggerTarget         `json:"targets"`
  TargetsByResponseStatus map[int]map[string]*TriggerTarget `json:"targetsByResponseStatus"`
  TriggerResults          map[string]map[int]int            `json:"triggerResults"`
  lock                    sync.RWMutex
}

var (
  Handler      util.ServerHandler  = util.ServerHandler{Name: "trigger", SetRoutes: SetRoutes}
  portTriggers map[string]*Trigger = map[string]*Trigger{}
  triggerLock  sync.RWMutex
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  triggerRouter := util.PathRouter(r, "/triggers")
  util.AddRouteWithPort(triggerRouter, "/add", addTriggerTarget, "POST")
  util.AddRouteWithPort(triggerRouter, "/{target}/remove", removeTriggerTarget, "PUT", "POST")
  util.AddRouteWithPort(triggerRouter, "/{target}/enable", enableTriggerTarget, "PUT", "POST")
  util.AddRouteWithPort(triggerRouter, "/{target}/disable", disableTriggerTarget, "PUT", "POST")
  util.AddRouteWithPort(triggerRouter, "/{targets}/invoke", invokeTriggers, "POST")
  util.AddRouteWithPort(triggerRouter, "/clear", clearTriggers, "POST")
  util.AddRouteWithPort(triggerRouter, "/counts", getTriggerCounts)
  util.AddRouteWithPort(triggerRouter, "", getTriggers)
}

func (t *Trigger) init() {
  t.lock.Lock()
  defer t.lock.Unlock()
  if t.Targets == nil {
    t.Targets = map[string]*TriggerTarget{}
    t.TargetsByResponseStatus = map[int]map[string]*TriggerTarget{}
    t.TriggerResults = map[string]map[int]int{}
  }
}

func (t *Trigger) addTriggerTarget(w http.ResponseWriter, r *http.Request) {
  tt := &TriggerTarget{}
  var err error
  if err = util.ReadJsonPayload(r, tt); err == nil {
    _, err = tt.toInvocationSpec(nil, nil)
  }
  if err == nil {
    t.deleteTriggerTarget(tt.Name)
    t.lock.Lock()
    t.Targets[tt.Name] = tt
    for _, triggerStatus := range tt.TriggerOn {
      if t.TargetsByResponseStatus[triggerStatus] == nil {
        t.TargetsByResponseStatus[triggerStatus] = map[string]*TriggerTarget{}
      }
      t.TargetsByResponseStatus[triggerStatus][tt.Name] = tt
    }
    t.lock.Unlock()
    msg := fmt.Sprintf("Port [%s] Added trigger target: %s", util.GetRequestOrListenerPort(r), tt.Name)
    util.AddLogMessage(msg, r)
    w.WriteHeader(http.StatusOK)
    fmt.Fprintln(w, msg)
    events.SendRequestEventJSON("Trigger Target Added", tt.Name, tt, r)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintf(w, "Invalid trigger target: %s\n", err.Error())
  }
}

func (t *Trigger) getRequestedTriggerTarget(r *http.Request) *TriggerTarget {
  t.lock.RLock()
  defer t.lock.RUnlock()
  if tname, present := util.GetStringParam(r, "target"); present {
    return t.Targets[tname]
  }
  return nil
}

func (t *Trigger) getRequestedTriggerTargets(r *http.Request) map[string]*TriggerTarget {
  t.lock.RLock()
  defer t.lock.RUnlock()
  targets := map[string]*TriggerTarget{}
  if tnamesParam, present := util.GetStringParam(r, "targets"); present {
    tnames := strings.Split(tnamesParam, ",")
    for _, tname := range tnames {
      if target, found := t.Targets[tname]; found {
        targets[target.Name] = target
      }
    }
  } else {
    targets = t.Targets
  }
  return targets
}

func (t *Trigger) deleteTriggerTarget(targetName string) {
  t.lock.Lock()
  defer t.lock.Unlock()
  delete(t.Targets, targetName)
  for s, targets := range t.TargetsByResponseStatus {
    for name := range targets {
      if name == targetName {
        delete(targets, name)
      }
    }
    if len(targets) == 0 {
      delete(t.TargetsByResponseStatus, s)
    }
  }
}

func (t *Trigger) removeTriggerTarget(w http.ResponseWriter, r *http.Request) {
  if tt := t.getRequestedTriggerTarget(r); tt != nil {
    t.deleteTriggerTarget(tt.Name)
    msg := fmt.Sprintf("Port [%s] Trigger Target Removed: %s", util.GetRequestOrListenerPort(r), tt.Name)
    util.AddLogMessage(msg, r)
    w.WriteHeader(http.StatusOK)
    fmt.Fprintln(w, msg)
    events.SendRequestEvent("Trigger Target Removed", msg, r)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintln(w, "No targets")
  }
}

func (t *Trigger) enableTriggerTarget(w http.ResponseWriter, r *http.Request) {
  if tt := t.getRequestedTriggerTarget(r); tt != nil {
    t.lock.Lock()
    tt.Enabled = true
    t.lock.Unlock()
    msg := fmt.Sprintf("Port [%s] Trigger Target Enabled: %s", util.GetRequestOrListenerPort(r), tt.Name)
    util.AddLogMessage(msg, r)
    w.WriteHeader(http.StatusOK)
    fmt.Fprintln(w, msg)
    events.SendRequestEvent("Trigger Target Enabled", msg, r)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintln(w, "Trigger target not found")
  }
}

func (t *Trigger) disableTriggerTarget(w http.ResponseWriter, r *http.Request) {
  if tt := t.getRequestedTriggerTarget(r); tt != nil {
    t.lock.Lock()
    tt.Enabled = false
    t.lock.Unlock()
    msg := fmt.Sprintf("Port [%s] Trigger Target Disabled: %s", util.GetRequestOrListenerPort(r), tt.Name)
    util.AddLogMessage(msg, r)
    w.WriteHeader(http.StatusOK)
    fmt.Fprintln(w, msg)
    events.SendRequestEvent("Trigger Target Disabled", msg, r)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintln(w, "Trigger target not found")
  }
}

func prepareTargetHeaders(tt *TriggerTarget, r *http.Request, w http.ResponseWriter) [][]string {
  var headers [][]string = [][]string{}
  for _, kv := range tt.Headers {
    if strings.HasPrefix(kv[1], "{") && strings.HasSuffix(kv[1], "}") {
      captureKey := strings.TrimLeft(kv[1], "{")
      captureKey = strings.TrimRight(captureKey, "}")
      if strings.EqualFold(captureKey, "request.uri") {
        kv[1] = r.RequestURI
      } else if strings.EqualFold(captureKey, "request.headers") {
        kv[1] = util.ToJSON(r.Header)
      } else if captureValue := w.Header().Get(captureKey); captureValue != "" {
        kv[1] = captureValue
      }
    }
    headers = append(headers, []string{kv[0], kv[1]})
  }
  return headers
}

func (tt *TriggerTarget) toInvocationSpec(r *http.Request, w http.ResponseWriter) (*invocation.InvocationSpec, error) {
  is := &invocation.InvocationSpec{}
  is.Name = tt.Name
  is.Method = tt.Method
  is.URL = tt.URL
  is.Headers = tt.Headers
  is.Body = tt.Body
  is.SendID = tt.SendID
  is.Replicas = 1
  if r != nil {
    is.Headers = prepareTargetHeaders(tt, r, w)
  }
  return is, invocation.ValidateSpec(is)
}

func (t *Trigger) invokeTargets(targets map[string]*TriggerTarget, w http.ResponseWriter, r *http.Request) []*invocation.InvocationResult {
  responses := []*invocation.InvocationResult{}
  if len(targets) > 0 {
    for _, target := range targets {
      target.lock.Lock()
      target.TriggerCount++
      target.lock.Unlock()
      events.SendRequestEventJSON("Trigger Target Invoked", target.Name, target, r)
      metrics.UpdateTriggerCount(target.Name)
      is, _ := target.toInvocationSpec(r, w)
      tracker := invocation.RegisterInvocation(is)
      results := invocation.StartInvocation(tracker, true)
      responses = append(responses, results...)
    }
    for _, response := range responses {
      if response.StatusCode == 0 {
        response.StatusCode = 503
      }
      t.lock.Lock()
      if t.TriggerResults[response.TargetName] == nil {
        t.TriggerResults[response.TargetName] = map[int]int{}
      }
      t.TriggerResults[response.TargetName][response.StatusCode]++
      t.lock.Unlock()
    }
    return responses
  }
  return nil
}

func getPortTrigger(r *http.Request) *Trigger {
  triggerLock.RLock()
  listenerPort := util.GetRequestOrListenerPort(r)
  trigger := portTriggers[listenerPort]
  triggerLock.RUnlock()
  if trigger == nil {
    triggerLock.Lock()
    defer triggerLock.Unlock()
    trigger = &Trigger{}
    trigger.init()
    portTriggers[listenerPort] = trigger
  }
  return trigger
}

func addTriggerTarget(w http.ResponseWriter, r *http.Request) {
  getPortTrigger(r).addTriggerTarget(w, r)
}

func removeTriggerTarget(w http.ResponseWriter, r *http.Request) {
  getPortTrigger(r).removeTriggerTarget(w, r)
}

func enableTriggerTarget(w http.ResponseWriter, r *http.Request) {
  getPortTrigger(r).enableTriggerTarget(w, r)
}

func disableTriggerTarget(w http.ResponseWriter, r *http.Request) {
  getPortTrigger(r).disableTriggerTarget(w, r)
}

func clearTriggers(w http.ResponseWriter, r *http.Request) {
  listenerPort := util.GetRequestOrListenerPort(r)
  triggerLock.Lock()
  defer triggerLock.Unlock()
  portTriggers[listenerPort] = &Trigger{}
  portTriggers[listenerPort].init()
  w.WriteHeader(http.StatusOK)
  msg := fmt.Sprintf("Port [%s] Triggers Cleared", util.GetRequestOrListenerPort(r))
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
  events.SendRequestEvent("Triggers Cleared", msg, r)
}

func getTriggerCounts(w http.ResponseWriter, r *http.Request) {
  t := getPortTrigger(r)
  triggerLock.Lock()
  defer triggerLock.Unlock()
  util.AddLogMessage(fmt.Sprintf("Port [%s] Get trigger counts", util.GetRequestOrListenerPort(r)), r)
  util.WriteJsonPayload(w, t.TriggerResults)
}

func getTriggers(w http.ResponseWriter, r *http.Request) {
  t := getPortTrigger(r)
  triggerLock.Lock()
  defer triggerLock.Unlock()
  util.AddLogMessage(fmt.Sprintf("Port [%s] Get triggers", util.GetRequestOrListenerPort(r)), r)
  util.WriteJsonPayload(w, t)
}

func invokeTriggers(w http.ResponseWriter, r *http.Request) {
  t := getPortTrigger(r)
  targets := t.getRequestedTriggerTargets(r)
  if len(targets) > 0 {
    responses := t.invokeTargets(targets, w, r)
    w.WriteHeader(http.StatusOK)
    util.AddLogMessage(fmt.Sprintf("Port [%s] Trigger targets invoked", util.GetRequestOrListenerPort(r)), r)
    fmt.Fprintln(w, util.ToJSON(responses))
  } else {
    w.WriteHeader(http.StatusNotFound)
    util.AddLogMessage("Trigger targets not found", r)
    fmt.Fprintln(w, "Trigger targets not found")
  }
}

func (t *Trigger) getMatchingTargets(r *http.Request, statusCode int) map[string]*TriggerTarget {
  t.lock.RLock()
  defer t.lock.RUnlock()
  targets := map[string]*TriggerTarget{}
  if t.TargetsByResponseStatus[statusCode] != nil {
    for _, tt := range t.TargetsByResponseStatus[statusCode] {
      tt.lock.RLock()
      if tt.StatusCount >= tt.StartFrom && tt.StatusCount <= tt.StopAt {
        targets[tt.Name] = tt
      }
      tt.lock.RUnlock()
    }
  }
  return targets
}

func RunTriggers(r *http.Request, w http.ResponseWriter, statusCode int) {
  if !util.IsAdminRequest(r) && !util.IsMetricsRequest(r) {
    t := getPortTrigger(r)
    for _, tt := range t.TargetsByResponseStatus[statusCode] {
      tt.lock.Lock()
      tt.StatusCount++
      tt.lock.Unlock()
    }
    t.invokeTargets(t.getMatchingTargets(r, statusCode), w, r)
  }
}
