package target

import (
  "fmt"
  "log"
  "net/http"
  "strings"
  "sync"
  "time"

  "goto/pkg/client/results"
  "goto/pkg/constants"
  "goto/pkg/events"
  . "goto/pkg/events/eventslist"
  "goto/pkg/global"
  "goto/pkg/invocation"
  "goto/pkg/util"
)

type Target struct {
  invocation.InvocationSpec
}

type TargetClient struct {
  targets            map[string]*Target
  activeTargetsCount int
  targetsLock        sync.RWMutex
  trackHeaders       []string
  crossTrackHeaders  map[string][]string
  trackTimeBuckets   [][]int
}

var (
  Client                     = NewTargetClient()
  portClientsLock            sync.RWMutex
  InvocationResultsRetention int = 100
)

func NewTargetClient() *TargetClient {
  c := &TargetClient{}
  c.init()
  return c
}

func (pc *TargetClient) init() bool {
  pc.targetsLock.Lock()
  defer pc.targetsLock.Unlock()
  if pc.activeTargetsCount > 0 {
    return false
  }
  pc.targets = map[string]*Target{}
  pc.trackHeaders = []string{}
  pc.crossTrackHeaders = map[string][]string{}
  pc.trackTimeBuckets = [][]int{}
  return true
}

func (pc *TargetClient) AddTarget(t *Target, r ...*http.Request) error {
  invocationSpec := &t.InvocationSpec
  if err := invocation.ValidateSpec(invocationSpec); err == nil {
    pc.targetsLock.Lock()
    pc.targets[t.Name] = t
    pc.targetsLock.Unlock()
    invocation.RemoveHttpClientForTarget(t.Name)
    t.Headers = append(t.Headers, []string{constants.HeaderFromGoto, global.PeerName},
      []string{constants.HeaderFromGotoHost, util.GetHostLabel()})
    if t.AutoInvoke {
      go func() {
        if global.EnableClientLogs {
          log.Printf("Auto-invoking target: %s\n", t.Name)
        }
        if len(r) > 0 {
          invocationSpec = pc.prepareTargetForPeer(invocationSpec, r[0])
        }
        pc.invokeTarget(invocationSpec)
      }()
    }
    return nil
  } else {
    return err
  }
}

func (pc *TargetClient) removeTargets(targets []string) bool {
  pc.targetsLock.Lock()
  defer pc.targetsLock.Unlock()
  if pc.activeTargetsCount > 0 {
    return false
  }
  for _, t := range targets {
    delete(pc.targets, t)
  }
  return true
}

func (pc *TargetClient) prepareTargetForPeer(target *invocation.InvocationSpec, r *http.Request) *invocation.InvocationSpec {
  if target == nil || r == nil {
    return target
  }
  peerName, _ := util.GetFillerUnmarked(target.Name)
  if peerName == "" {
    return target
  }
  peers := global.GetPeers(peerName, r)
  if peers == nil || len(peers) == 0 {
    return target
  }
  if strings.Contains(target.URL, "{") && strings.Contains(target.URL, "}") {
    urlPre := strings.Split(target.URL, "{")[0]
    urlPost := strings.Split(target.URL, "}")[1]
    address := peers[peerName]
    var newTarget = *target
    newTarget.Name = peerName
    newTarget.URL = urlPre + address + urlPost
    target = &newTarget
  }
  return target
}

func (pc *TargetClient) PrepareTarget(name string) *invocation.InvocationSpec {
  var targetToInvoke *invocation.InvocationSpec
  if name != "" {
    pc.targetsLock.RLock()
    target, found := pc.targets[name]
    if !found {
      target, found = pc.targets["{"+name+"}"]
    }
    pc.targetsLock.RUnlock()
    if found {
      targetToInvoke = &target.InvocationSpec
    }
  }
  return targetToInvoke
}

func (pc *TargetClient) getTargetsToInvoke(r *http.Request) []*invocation.InvocationSpec {
  var names []string
  if r != nil {
    names, _ = util.GetListParam(r, "targets")
  }
  var targetsToInvoke []*invocation.InvocationSpec
  if len(names) > 0 {
    for _, name := range names {
      if t := pc.prepareTargetForPeer(pc.PrepareTarget(name), r); t != nil {
        targetsToInvoke = append(targetsToInvoke, t)
      }
    }
  } else {
    for _, target := range pc.targets {
      targetsToInvoke = append(targetsToInvoke, &target.InvocationSpec)
    }
  }
  return targetsToInvoke
}

func (pc *TargetClient) AddTrackingHeaders(headers string) {
  pc.targetsLock.Lock()
  defer pc.targetsLock.Unlock()
  pc.trackHeaders, pc.crossTrackHeaders = util.ParseTrackingHeaders(headers)
}

func (pc *TargetClient) clearTrackingHeaders() {
  pc.targetsLock.Lock()
  pc.trackHeaders = []string{}
  pc.crossTrackHeaders = map[string][]string{}
  pc.targetsLock.Unlock()
}

func (pc *TargetClient) getTrackingHeaders() []string {
  headers := []string{}
  pc.targetsLock.RLock()
  for _, h := range pc.trackHeaders {
    if crossHeaders := pc.crossTrackHeaders[h]; crossHeaders != nil {
      headers = append(headers, strings.Join([]string{h, strings.Join(crossHeaders, "|")}, "|"))
    }
    headers = append(headers, h)
  }
  pc.targetsLock.RUnlock()
  return headers
}

func (pc *TargetClient) AddTrackingTimeBuckets(b string) bool {
  pc.targetsLock.Lock()
  defer pc.targetsLock.Unlock()
  buckets, ok := util.ParseTimeBuckets(b)
  if ok {
    pc.trackTimeBuckets = buckets
  }
  return ok
}

func (pc *TargetClient) clearTrackingTimeBuckets() {
  pc.targetsLock.Lock()
  pc.trackTimeBuckets = [][]int{}
  pc.targetsLock.Unlock()
}

func (pc *TargetClient) stopTargets(targetNames []string) (bool, bool) {
  pc.targetsLock.RLock()
  defer pc.targetsLock.RUnlock()
  stoppingTargets := []string{}
  if len(targetNames) > 0 {
    for _, tname := range targetNames {
      if len(tname) > 0 {
        if target, found := pc.targets[tname]; found {
          go invocation.StopTarget(target.Name)
          stoppingTargets = append(stoppingTargets, target.Name)
        }
      }
    }
  } else {
    if len(pc.targets) > 0 {
      for _, target := range pc.targets {
        go invocation.StopTarget(target.Name)
        stoppingTargets = append(stoppingTargets, target.Name)
      }
    }
  }
  stopped := len(stoppingTargets) == 0
  if !stopped {
    for i := 0; i < 10; i++ {
      if !invocation.IsAnyTargetActive(stoppingTargets) {
        stopped = true
        break
      }
      time.Sleep(time.Second * 1)
    }
  }
  if len(targetNames) == 0 { //Reset active invocations if all targets were stopped
    invocation.ResetActiveInvocations()
  }
  return len(stoppingTargets) > 0, stopped
}

func addTarget(w http.ResponseWriter, r *http.Request) {
  msg := ""
  t := &Target{}
  if err := util.ReadJsonPayload(r, t); err == nil {
    if err := Client.AddTarget(t, r); err != nil {
      w.WriteHeader(http.StatusBadRequest)
      msg = fmt.Sprintf("Invalid target spec: %s", err.Error())
    } else {
      w.WriteHeader(http.StatusOK)
      msg = fmt.Sprintf("Added target: %s", util.ToJSON(t))
      events.SendRequestEventJSON(Client_TargetAdded, t.Name, t, r)
    }
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = fmt.Sprintf("Failed to parse json with error: %s", err.Error())
  }
  if global.EnableClientLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func removeTargets(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if targets, present := util.GetListParam(r, "targets"); present {
    if Client.removeTargets(targets) {
      w.WriteHeader(http.StatusOK)
      msg = fmt.Sprintf("Targets Removed: %+v", targets)
      events.SendRequestEventJSON(Client_TargetsRemoved, util.GetStringParamValue(r, "targets"), targets, r)
    } else {
      w.WriteHeader(http.StatusNotAcceptable)
      msg = fmt.Sprintf("Targets cannot be removed while traffic is running")
    }
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "No target given"
  }
  if global.EnableClientLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func clearTargets(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if Client.init() {
    w.WriteHeader(http.StatusOK)
    msg = "Targets cleared"
    events.SendRequestEvent(Client_TargetsCleared, "", r)
  } else {
    w.WriteHeader(http.StatusNotAcceptable)
    msg = fmt.Sprintf("Targets cannot be cleared while traffic is running")
  }
  if global.EnableClientLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func getTargets(w http.ResponseWriter, r *http.Request) {
  if t, present := util.GetStringParam(r, "target"); present {
    if Client.targets[t] != nil {
      util.WriteJsonPayload(w, Client.targets[t])
    } else {
      util.WriteErrorJson(w, "Target not found: "+t)
    }
  } else {
    util.WriteJsonPayload(w, Client.targets)
  }
  if global.EnableClientLogs {
    util.AddLogMessage("Reporting targets", r)
  }
}

func addTrackingHeaders(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if h, present := util.GetStringParam(r, "headers"); present {
    Client.AddTrackingHeaders(h)
    w.WriteHeader(http.StatusOK)
    msg = fmt.Sprintf("Header %s will be tracked", h)
    events.SendRequestEvent(Client_TrackingHeadersAdded, msg, r)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "Invalid header name"
  }
  if global.EnableClientLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func clearTrackingHeaders(w http.ResponseWriter, r *http.Request) {
  Client.clearTrackingHeaders()
  msg := "All tracking headers cleared"
  events.SendRequestEvent(Client_TrackingHeadersCleared, msg, r)
  fmt.Fprintln(w, msg)
  if global.EnableClientLogs {
    util.AddLogMessage(msg, r)
  }
}

func getTrackingHeaders(w http.ResponseWriter, r *http.Request) {
  msg := fmt.Sprintf("Tracking headers: %s", strings.Join(Client.getTrackingHeaders(), ","))
  fmt.Fprintln(w, msg)
  if global.EnableClientLogs {
    util.AddLogMessage(msg, r)
  }
}

func addTrackingTimeBuckets(w http.ResponseWriter, r *http.Request) {
  msg := ""
  b := util.GetStringParamValue(r, "buckets")
  if !Client.AddTrackingTimeBuckets(b) {
    w.WriteHeader(http.StatusBadRequest)
    msg = "Invalid time bucket"
  } else {
    msg = fmt.Sprintf("Time Buckets [%s] will be tracked", b)
    events.SendRequestEvent(Client_TrackingTimeBucketAdded, msg, r)
  }
  if global.EnableClientLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func clearTrackingTimeBuckets(w http.ResponseWriter, r *http.Request) {
  Client.clearTrackingTimeBuckets()
  msg := "All tracking time buckets cleared"
  events.SendRequestEvent(Client_TrackingTimeBucketsCleared, msg, r)
  fmt.Fprintln(w, msg)
  if global.EnableClientLogs {
    util.AddLogMessage(msg, r)
  }
}

func getTrackingTimeBuckets(w http.ResponseWriter, r *http.Request) {
  util.WriteJsonPayload(w, Client.trackTimeBuckets)
  if global.EnableClientLogs {
    util.AddLogMessage("Tracking TimeBuckets Reported", r)
  }
}

func addCACert(w http.ResponseWriter, r *http.Request) {
  msg := ""
  data := util.ReadBytes(r.Body)
  if len(data) > 0 {
    invocation.StoreCACert(data)
    msg = Client_CACertStored
    events.SendRequestEvent(msg, "", r)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "Invalid header name"
  }
  fmt.Fprintln(w, msg)
  if global.EnableClientLogs {
    util.AddLogMessage(msg, r)
  }
}

func removeCACert(w http.ResponseWriter, r *http.Request) {
  invocation.RemoveCACert()
  msg := Client_CACertRemoved
  events.SendRequestEvent(msg, "", r)
  fmt.Fprintln(w, msg)
  if global.EnableClientLogs {
    util.AddLogMessage(msg, r)
  }
}

func getInvocationResults(w http.ResponseWriter, r *http.Request) {
  util.WriteStringJsonPayload(w, results.GetInvocationResultsJSON())
  if global.EnableClientLogs {
    util.AddLogMessage("Reporting results", r)
  }
}

func getResults(w http.ResponseWriter, r *http.Request) {
  util.WriteStringJsonPayload(w, results.GetTargetsResultsJSON())
  if global.EnableClientLogs {
    util.AddLogMessage("Reporting results", r)
  }
}

func getActiveTargets(w http.ResponseWriter, r *http.Request) {
  if global.EnableClientLogs {
    util.AddLogMessage("Reporting active invocations", r)
  }
  result := map[string]interface{}{}
  pc := Client
  pc.targetsLock.RLock()
  result["activeCount"] = pc.activeTargetsCount
  pc.targetsLock.RUnlock()
  result["activeInvocations"] = invocation.GetActiveInvocations()
  util.WriteJsonPayload(w, result)
}

func clearResults(w http.ResponseWriter, r *http.Request) {
  results.ClearResults()
  w.WriteHeader(http.StatusOK)
  msg := Client_ResultsCleared
  events.SendRequestEvent(msg, "", r)
  if global.EnableClientLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func enableAllTargetsResultsCollection(w http.ResponseWriter, r *http.Request) {
  enable := util.GetStringParamValue(r, "enable")
  results.EnableAllTargetResults(util.IsYes(enable))
  w.WriteHeader(http.StatusOK)
  msg := "Changed all targets summary results collection"
  if global.EnableClientLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func enableInvocationResultsCollection(w http.ResponseWriter, r *http.Request) {
  enable := util.GetBoolParamValue(r, "enable")
  results.EnableInvocationResults(enable)
  msg := ""
  if enable {
    msg = "Will collect invocation results"
  } else {
    msg = "Will not collect invocation results"
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func stopTargets(w http.ResponseWriter, r *http.Request) {
  msg := ""
  pc := Client
  targets, _ := util.GetListParam(r, "targets")
  hasActive, _ := pc.stopTargets(targets)
  if hasActive {
    w.WriteHeader(http.StatusOK)
    msg = fmt.Sprintf("Targets %+v stopped", targets)
    events.SendRequestEvent(Client_TargetsStopped, msg, r)
  } else {
    w.WriteHeader(http.StatusOK)
    msg = "No targets to stop"
  }
  if global.EnableClientLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func (pc *TargetClient) invokeTarget(target *invocation.InvocationSpec) {
  tracker := invocation.RegisterInvocation(target, results.ResultChannelSinkFactory(target, pc.trackHeaders, pc.crossTrackHeaders, pc.trackTimeBuckets))
  pc.targetsLock.Lock()
  pc.activeTargetsCount++
  pc.targetsLock.Unlock()
  events.SendEventJSON(Client_TargetInvoked, target.Name, tracker)
  invocation.StartInvocation(tracker)
  pc.targetsLock.Lock()
  pc.activeTargetsCount--
  pc.targetsLock.Unlock()
}

func invokeTargets(w http.ResponseWriter, r *http.Request) {
  pc := Client
  targetsToInvoke := pc.getTargetsToInvoke(r)
  if len(targetsToInvoke) > 0 {
    for _, target := range targetsToInvoke {
      go pc.invokeTarget(target)
    }
    w.WriteHeader(http.StatusOK)
    fmt.Fprintln(w, "Targets invoked")
    if global.EnableClientLogs {
      util.AddLogMessage("Targets invoked", r)
    }
  } else {
    w.WriteHeader(http.StatusNotAcceptable)
    fmt.Fprintln(w, "No targets to invoke")
    if global.EnableClientLogs {
      util.AddLogMessage("No targets to invoke", r)
    }
  }
}
