package target

import (
  "fmt"
  "log"
  "net/http"
  "strings"
  "sync"
  "time"

  "goto/pkg/global"
  "goto/pkg/client/results"
  "goto/pkg/invocation"
  "goto/pkg/util"
)

type Target struct {
  invocation.InvocationSpec
}

type PortClient struct {
  targets              map[string]*Target
  activeTargetsCount   int
  targetsLock          sync.RWMutex
  trackingHeaders      []string
  crossTrackingHeaders map[string][]string
}

var (
  portClients                map[string]*PortClient = map[string]*PortClient{}
  portClientsLock            sync.RWMutex
  InvocationResultsRetention int = 100
)

func (pc *PortClient) init() bool {
  pc.targetsLock.Lock()
  defer pc.targetsLock.Unlock()
  if pc.activeTargetsCount > 0 {
    return false
  }
  pc.targets = map[string]*Target{}
  pc.trackingHeaders = []string{}
  pc.crossTrackingHeaders = map[string][]string{}
  return true
}

func (pc *PortClient) AddTarget(t *Target, r ...*http.Request) error {
  invocationSpec := &t.InvocationSpec
  if err := invocation.ValidateSpec(invocationSpec); err == nil {
    pc.targetsLock.Lock()
    pc.targets[t.Name] = t
    pc.targetsLock.Unlock()
    invocation.RemoveHttpClientForTarget(t.Name)
    invocation.PrepareAutoPayload(invocationSpec)
    t.Headers = append(t.Headers, []string{"From-Goto", global.PeerName}, []string{"From-Goto-Host", util.GetHostLabel()})
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

func (pc *PortClient) removeTargets(targets []string) bool {
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

func (pc *PortClient) prepareTargetForPeer(target *invocation.InvocationSpec, r *http.Request) *invocation.InvocationSpec {
  if target != nil {
    peerName := util.GetFillerUnmarked(target.Name)
    if peerName != "" && r != nil {
      if peers := global.GetPeers(peerName, r); peers != nil {
        if strings.Contains(target.URL, "{") && strings.Contains(target.URL, "}") {
          urlPre := strings.Split(target.URL, "{")[0]
          urlPost := strings.Split(target.URL, "}")[1]
          for _, address := range peers {
            var newTarget = *target
            newTarget.Name = peerName
            newTarget.URL = urlPre + address + urlPost
            target = &newTarget
          }
        }
      }
    }
  }
  return target
}

func (pc *PortClient) PrepareTarget(name string) *invocation.InvocationSpec {
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

func (pc *PortClient) getTargetsToInvoke(r *http.Request) []*invocation.InvocationSpec {
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

func (pc *PortClient) AddTrackingHeaders(headers string) {
  pc.targetsLock.Lock()
  defer pc.targetsLock.Unlock()
  pc.trackingHeaders, pc.crossTrackingHeaders = util.ParseTrackingHeaders(headers)
}

func (pc *PortClient) removeTrackingHeader(header string) {
  pc.targetsLock.Lock()
  for i, h := range pc.trackingHeaders {
    if strings.EqualFold(header, h) {
      pc.trackingHeaders = append(pc.trackingHeaders[:i], pc.trackingHeaders[i+1:]...)
    }
    if _, present := pc.crossTrackingHeaders[h]; present {
      delete(pc.crossTrackingHeaders, h)
    }
  }
  pc.targetsLock.Unlock()
}

func (pc *PortClient) clearTrackingHeaders() {
  pc.targetsLock.Lock()
  pc.trackingHeaders = []string{}
  pc.crossTrackingHeaders = map[string][]string{}
  pc.targetsLock.Unlock()
}

func (pc *PortClient) getTrackingHeaders() []string {
  headers := []string{}
  pc.targetsLock.RLock()
  for _, h := range pc.trackingHeaders {
    if crossHeaders := pc.crossTrackingHeaders[h]; crossHeaders != nil {
      headers = append(headers, strings.Join([]string{h, strings.Join(crossHeaders, "|")}, "|"))
    }
    headers = append(headers, h)
  }
  pc.targetsLock.RUnlock()
  return headers
}

func (pc *PortClient) stopTargets(targetNames []string) (bool, bool) {
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

func GetClientForPort(port string) *PortClient {
  portClientsLock.Lock()
  defer portClientsLock.Unlock()
  pc := portClients[port]
  if pc == nil {
    pc = &PortClient{}
    pc.init()
    portClients[port] = pc
  }
  return pc
}

func getPortClient(r *http.Request) *PortClient {
  return GetClientForPort(util.GetListenerPort(r))
}

func addTarget(w http.ResponseWriter, r *http.Request) {
  msg := ""
  t := &Target{}
  if err := util.ReadJsonPayload(r, t); err == nil {
    if err := getPortClient(r).AddTarget(t, r); err != nil {
      w.WriteHeader(http.StatusBadRequest)
      msg = fmt.Sprintf("Invalid target spec: %s", err.Error())
    } else {
      w.WriteHeader(http.StatusAccepted)
      msg = fmt.Sprintf("Added target: %s", util.ToJSON(t))
    }
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "Failed to parse json"
  }
  if global.EnableClientLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func removeTargets(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if targets, present := util.GetListParam(r, "targets"); present {
    if getPortClient(r).removeTargets(targets) {
      w.WriteHeader(http.StatusAccepted)
      msg = fmt.Sprintf("Targets Removed: %+v", targets)
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
  if getPortClient(r).init() {
    w.WriteHeader(http.StatusAccepted)
    msg = "Targets cleared"
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
  util.WriteJsonPayload(w, getPortClient(r).targets)
  if global.EnableClientLogs {
    util.AddLogMessage("Reporting targets", r)
  }
}

func addTrackingHeaders(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if h, present := util.GetStringParam(r, "headers"); present {
    getPortClient(r).AddTrackingHeaders(h)
    w.WriteHeader(http.StatusAccepted)
    msg = fmt.Sprintf("Header %s will be tracked", h)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "Invalid header name"
  }
  if global.EnableClientLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func removeTrackingHeader(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if header, present := util.GetStringParam(r, "header"); present {
    getPortClient(r).removeTrackingHeader(header)
    w.WriteHeader(http.StatusAccepted)
    msg = fmt.Sprintf("Header %s removed from tracking", header)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "Invalid header name"
  }
  fmt.Fprintln(w, msg)
  if global.EnableClientLogs {
    util.AddLogMessage(msg, r)
  }
}

func clearTrackingHeaders(w http.ResponseWriter, r *http.Request) {
  getPortClient(r).clearTrackingHeaders()
  w.WriteHeader(http.StatusAccepted)
  msg := "All tracking headers cleared"
  fmt.Fprintln(w, msg)
  if global.EnableClientLogs {
    util.AddLogMessage(msg, r)
  }
}

func getTrackingHeaders(w http.ResponseWriter, r *http.Request) {
  w.WriteHeader(http.StatusAlreadyReported)
  msg := fmt.Sprintf("Tracking headers: %s", strings.Join(getPortClient(r).getTrackingHeaders(), ","))
  fmt.Fprintln(w, msg)
  if global.EnableClientLogs {
    util.AddLogMessage(msg, r)
  }
}

func addCACert(w http.ResponseWriter, r *http.Request) {
  msg := ""
  data := util.ReadBytes(r.Body)
  if len(data) > 0 {
    invocation.StoreCACert(data)
    msg = "CA Cert stored"
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
  msg := "CA Cert removed"
  fmt.Fprintln(w, msg)
  if global.EnableClientLogs {
    util.AddLogMessage(msg, r)
  }
}

func getInvocationResults(w http.ResponseWriter, r *http.Request) {
  w.WriteHeader(http.StatusAlreadyReported)
  fmt.Fprintln(w, results.GetInvocationResultsJSON())
  if global.EnableClientLogs {
    util.AddLogMessage("Reporting results", r)
  }
}

func getResults(w http.ResponseWriter, r *http.Request) {
  w.WriteHeader(http.StatusAlreadyReported)
  fmt.Fprintln(w, results.GetTargetsResultsJSON())
  if global.EnableClientLogs {
    util.AddLogMessage("Reporting results", r)
  }
}

func getActiveTargets(w http.ResponseWriter, r *http.Request) {
  if global.EnableClientLogs {
    util.AddLogMessage("Reporting active invocations", r)
  }
  result := map[string]interface{}{}
  pc := getPortClient(r)
  pc.targetsLock.RLock()
  result["activeCount"] = pc.activeTargetsCount
  pc.targetsLock.RUnlock()
  result["activeInvocations"] = invocation.GetActiveInvocations()
  util.WriteJsonPayload(w, result)
}

func clearResults(w http.ResponseWriter, r *http.Request) {
  results.ClearResults()
  w.WriteHeader(http.StatusAccepted)
  msg := "Results cleared"
  if global.EnableClientLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func enableAllTargetsResultsCollection(w http.ResponseWriter, r *http.Request) {
  enable := util.GetStringParamValue(r, "enable")
  results.EnableAllTargetResults(util.IsYes(enable))
  w.WriteHeader(http.StatusAccepted)
  msg := "Changed all targets summary results collection"
  if global.EnableClientLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func enableInvocationResultsCollection(w http.ResponseWriter, r *http.Request) {
  results.EnableInvocationResults(util.IsYes(util.GetStringParamValue(r, "enable")))
  w.WriteHeader(http.StatusAccepted)
  msg := "Changed invocation results collection"
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func stopTargets(w http.ResponseWriter, r *http.Request) {
  msg := ""
  pc := getPortClient(r)
  targets, _ := util.GetListParam(r, "targets")
  hasActive, _ := pc.stopTargets(targets)
  if hasActive {
    w.WriteHeader(http.StatusOK)
    msg = fmt.Sprintf("Targets %+v stopped", targets)
  } else {
    w.WriteHeader(http.StatusOK)
    msg = "No targets to stop"
  }
  if global.EnableClientLogs {
    util.AddLogMessage(msg, r)
  }
  fmt.Fprintln(w, msg)
}

func (pc *PortClient) invokeTarget(target *invocation.InvocationSpec) {
  tracker := invocation.RegisterInvocation(target, results.ResultChannelSinkFactory(target, pc.trackingHeaders, pc.crossTrackingHeaders))
  pc.targetsLock.Lock()
  pc.activeTargetsCount++
  pc.targetsLock.Unlock()
  invocation.StartInvocation(tracker)
  pc.targetsLock.Lock()
  pc.activeTargetsCount--
  pc.targetsLock.Unlock()
}

func invokeTargets(w http.ResponseWriter, r *http.Request) {
  pc := getPortClient(r)
  targetsToInvoke := pc.getTargetsToInvoke(r)
  if len(targetsToInvoke) > 0 {
    for _, target := range targetsToInvoke {
      go pc.invokeTarget(target)
    }
    w.WriteHeader(http.StatusAccepted)
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
