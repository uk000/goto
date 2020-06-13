package trigger

import (
	"fmt"
	"goto/pkg/http/invocation"
	"goto/pkg/util"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/mux"
)

type TriggerTarget struct {
  Name                      string
  Method                    string
  URL                       string
  Headers                   [][]string
  Body                      string
  SendID                    bool
  Enabled                   bool
  TriggerOnResponseStatuses []int
}

type Trigger struct {
  Targets                 map[string]*TriggerTarget
  TargetsByResponseStatus map[int]map[string]*TriggerTarget
  TriggerResults          map[string]map[int]int
  lock                    sync.RWMutex
}

var (
  Handler      util.ServerHandler  = util.ServerHandler{Name: "trigger", SetRoutes: SetRoutes}
  portTriggers map[string]*Trigger = map[string]*Trigger{}
  triggerLock  sync.RWMutex
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  triggerRouter := r.PathPrefix("/trigger").Subrouter()
  util.AddRoute(triggerRouter, "/add", addTriggerTarget, "POST")
  util.AddRoute(triggerRouter, "/{target}/remove", removeTriggerTarget, "PUT", "POST")
  util.AddRoute(triggerRouter, "/{target}/enable", enableTriggerTarget, "PUT", "POST")
  util.AddRoute(triggerRouter, "/{target}/disable", disableTriggerTarget, "PUT", "POST")
  util.AddRoute(triggerRouter, "/{targets}/invoke", invokeTriggers, "POST")
  util.AddRoute(triggerRouter, "/clear", clearTriggers, "POST")
  util.AddRoute(triggerRouter, "/counts", getTriggerCounts)
  util.AddRoute(triggerRouter, "/list", getTriggers)
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
    defer t.lock.Unlock()
    t.Targets[tt.Name] = tt
    for _, triggerStatus := range tt.TriggerOnResponseStatuses {
      if t.TargetsByResponseStatus[triggerStatus] == nil {
        t.TargetsByResponseStatus[triggerStatus] = map[string]*TriggerTarget{}
      }
      t.TargetsByResponseStatus[triggerStatus][tt.Name] = tt
    }
    util.AddLogMessage(fmt.Sprintf("Added trigger target: %+v", tt), r)
    w.WriteHeader(http.StatusAccepted)
    fmt.Fprintf(w, "Added trigger target: %s\n", util.ToJSON(tt))
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
    util.AddLogMessage(fmt.Sprintf("Removed trigger target: %+v", tt), r)
    w.WriteHeader(http.StatusAccepted)
    fmt.Fprintf(w, "Removed trigger target: %s\n", util.ToJSON(tt))
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
    util.AddLogMessage(fmt.Sprintf("Enabled trigger target: %s", tt.Name), r)
    w.WriteHeader(http.StatusAccepted)
    fmt.Fprintf(w, "Enabled trigger target: %s\n", util.ToJSON(tt))
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
    util.AddLogMessage(fmt.Sprintf("Disbled trigger target: %s", tt.Name), r)
    w.WriteHeader(http.StatusAccepted)
    fmt.Fprintf(w, "Disbled trigger target: %s\n", util.ToJSON(tt))
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
  t.lock.Lock()
  defer t.lock.Unlock()
  if len(targets) > 0 {
    invocationSpecs := []*invocation.InvocationSpec{}
    for _, tt := range targets {
      is, _ := tt.toInvocationSpec(r, w)
      invocationSpecs = append(invocationSpecs, is)
    }
    ic := &invocation.InvocationChannels{}
    ic.ID = util.GetListenerPortNum(r)
    responses := invocation.InvokeTargets(invocationSpecs, ic, false)
    for _, response := range responses {
      if response.StatusCode == 0 {
        response.StatusCode = 503
      }
      if t.TriggerResults[response.TargetName] == nil {
        t.TriggerResults[response.TargetName] = map[int]int{}
      }
      t.TriggerResults[response.TargetName][response.StatusCode]++
    }
    return responses
  }
  return nil
}

func getPortTrigger(r *http.Request) *Trigger {
  triggerLock.RLock()
  listenerPort := util.GetListenerPort(r)
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
  listenerPort := util.GetListenerPort(r)
  triggerLock.Lock()
  defer triggerLock.Unlock()
  portTriggers[listenerPort] = &Trigger{}
  portTriggers[listenerPort].init()
  w.WriteHeader(http.StatusAccepted)
  util.AddLogMessage("Triggers cleared", r)
  fmt.Fprintln(w, "Triggers cleared")
}

func getTriggerCounts(w http.ResponseWriter, r *http.Request) {
  t := getPortTrigger(r)
  triggerLock.Lock()
  defer triggerLock.Unlock()
  util.AddLogMessage(fmt.Sprintf("Get trigger counts: %+v", t), r)
  util.WriteJsonPayload(w, t.TriggerResults)
}

func getTriggers(w http.ResponseWriter, r *http.Request) {
  t := getPortTrigger(r)
  triggerLock.Lock()
  defer triggerLock.Unlock()
  util.AddLogMessage(fmt.Sprintf("Get triggers: %+v", t), r)
  util.WriteJsonPayload(w, t)
}

func invokeTriggers(w http.ResponseWriter, r *http.Request) {
  t := getPortTrigger(r)
  targets := t.getRequestedTriggerTargets(r)
  if len(targets) > 0 {
    responses := t.invokeTargets(targets, w, r)
    w.WriteHeader(http.StatusOK)
    util.AddLogMessage("Trigger targets invoked", r)
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
      targets[tt.Name] = tt
    }
  }
  return targets
}

func RunTriggers(r *http.Request, w http.ResponseWriter, statusCode int) {
  if !util.IsAdminRequest(r) {
    t := getPortTrigger(r)
    t.invokeTargets(t.getMatchingTargets(r, statusCode), w, r)
  }
}
