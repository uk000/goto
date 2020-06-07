package target

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"

	"goto/pkg/http/invocation"
	"goto/pkg/http/registry"
	"goto/pkg/http/registry/peer"
	"goto/pkg/http/server/listeners"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

type Target struct {
  invocation.InvocationSpec
}

type TargetResults struct {
  TargetInvocationCounts     map[string]int                       `json:"targetInvocationCounts"`
  CountsByStatus             map[string]int                       `json:"countsByStatus"`
  CountsByStatusCodes        map[int]int                          `json:"countsByStatusCodes"`
  CountsByHeaders            map[string]int                       `json:"countsByHeaders"`
  CountsByHeaderValues       map[string]map[string]int            `json:"countsByHeaderValues"`
  CountsByTargetStatus       map[string]map[string]int            `json:"countsByTargetStatus"`
  CountsByTargetStatusCode   map[string]map[int]int               `json:"countsByTargetStatusCode"`
  CountsByTargetHeaders      map[string]map[string]int            `json:"countsByTargetHeaders"`
  CountsByTargetHeaderValues map[string]map[string]map[string]int `json:"countsByTargetHeaderValues"`
}

type PortClient struct {
  targets            map[string]*Target
  invocationChannels map[int]*invocation.InvocationChannels
  invocationCounter  int
  targetsLock        sync.RWMutex
  blockForResponse   bool
  trackingHeaders    map[string]int
  targetResults      *TargetResults
  resultsLock        sync.RWMutex
}

var (
  Handler         util.ServerHandler = util.ServerHandler{Name: "client", SetRoutes: SetRoutes}
  GetPeer func(name string, r *http.Request) *peer.Peers
  portClients     map[string]*PortClient = map[string]*PortClient{}
  portClientsLock sync.RWMutex
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  targetsRouter := r.PathPrefix("/targets").Subrouter()
  util.AddRoute(targetsRouter, "/add", addTarget, "POST")
  util.AddRoute(targetsRouter, "/{target}/remove", removeTarget, "POST", "PUT")
  util.AddRoute(targetsRouter, "/{targets}/invoke", invokeTargets, "POST")
  util.AddRoute(targetsRouter, "/invoke/all", invokeTargets, "POST")
  util.AddRoute(targetsRouter, "/{targets}/stop", stopTargets, "POST")
  util.AddRoute(targetsRouter, "/stop/all", stopTargets, "POST")
  util.AddRoute(targetsRouter, "/list", getTargets, "GET")
  util.AddRoute(targetsRouter, "", getTargets, "GET")
  util.AddRoute(targetsRouter, "/clear", clearTargets, "POST")

  util.AddRoute(r, "/blocking/set/{flag}", setOrGetBlocking, "POST", "PUT")
  util.AddRoute(r, "/blocking", setOrGetBlocking, "GET")
  util.AddRoute(r, "/track/headers/add/{headers}", addTrackingHeaders, "POST", "PUT")
  util.AddRoute(r, "/track/headers/remove/{header}", removeTrackingHeader, "POST", "PUT")
  util.AddRoute(r, "/track/headers/list", getTrackingHeaders, "GET")
  util.AddRoute(r, "/track/headers/clear", clearTrackingHeaders, "POST")
  util.AddRoute(r, "/track/headers", getTrackingHeaders, "GET")
  util.AddRoute(r, "/results", getResults, "GET")
  util.AddRoute(r, "/results/{targets}/clear", clearResults, "POST")
  util.AddRoute(r, "/results/clear", clearResults, "POST")
}

func (pc *PortClient) init() {
  pc.targetsLock.Lock()
  defer pc.targetsLock.Unlock()
  pc.targets = map[string]*Target{}
  pc.invocationChannels = map[int]*invocation.InvocationChannels{}
  pc.invocationCounter = 0
  pc.blockForResponse = false
  pc.trackingHeaders = map[string]int{}
  pc.targetResults = &TargetResults{}
}

func (pc *PortClient) initTargetResults() {
  pc.resultsLock.Lock()
  defer pc.resultsLock.Unlock()
  if pc.targetResults.TargetInvocationCounts == nil {
    pc.targetResults.TargetInvocationCounts = map[string]int{}
    pc.targetResults.CountsByStatusCodes = map[int]int{}
    pc.targetResults.CountsByStatus = map[string]int{}
    pc.targetResults.CountsByHeaders = map[string]int{}
    pc.targetResults.CountsByHeaderValues = map[string]map[string]int{}
    pc.targetResults.CountsByTargetStatus = map[string]map[string]int{}
    pc.targetResults.CountsByTargetStatusCode = map[string]map[int]int{}
    pc.targetResults.CountsByTargetHeaders = map[string]map[string]int{}
    pc.targetResults.CountsByTargetHeaderValues = map[string]map[string]map[string]int{}
  }
}

func (pc *PortClient) AddTarget(t *Target) {
  pc.targetsLock.Lock()
  defer pc.targetsLock.Unlock()
  pc.targets[t.Name] = t
  if t.AutoInvoke {
    go func() {
      log.Printf("Auto-invoking target: %s\n", t.Name)
      invocationChannels := pc.RegisterInvocation()
      invokeTargetsAndStoreResults(pc, []*invocation.InvocationSpec{&t.InvocationSpec}, invocationChannels)
    }()
  }
}

func (pc *PortClient) removeTarget(name string) bool {
  pc.targetsLock.Lock()
  defer pc.targetsLock.Unlock()
  present := false
  if _, present = pc.targets[name]; present {
    delete(pc.targets, name)
  }
  return present
}

func prepareTargetsForPeers(targets []*invocation.InvocationSpec, r *http.Request) []*invocation.InvocationSpec {
  targetsToInvoke := []*invocation.InvocationSpec{}
  for _, t := range targets {
    targetsForTarget := []*invocation.InvocationSpec{}
    if strings.HasPrefix(t.Name, "{") && strings.HasSuffix(t.Name, "}") {
      p := strings.TrimLeft(t.Name, "{")
      p = strings.TrimRight(p, "}")
      if r != nil {
        if peer := registry.GetPeer(p, r); peer != nil {
          if strings.Contains(t.URL, "{") && strings.Contains(t.URL, "}") {
            urlPre := strings.Split(t.URL, "{")[0]
            urlPost := strings.Split(t.URL, "}")[1]
            for address := range peer.Addresses {
              var target = *t
              target.Name = peer.Name
              target.URL = urlPre + address + urlPost
              targetsForTarget = append(targetsForTarget, &target)
            }
          }
        }
      }
    }
    if len(targetsForTarget) > 0 {
      targetsToInvoke = append(targetsToInvoke, targetsForTarget...)
    } else {
      targetsToInvoke = append(targetsToInvoke, t)
    }
  }
  return targetsToInvoke
}

func (pc *PortClient) PrepareTargetsToInvoke(names []string) []*invocation.InvocationSpec {
  pc.targetsLock.RLock()
  defer pc.targetsLock.RUnlock()
  var targetsToInvoke []*invocation.InvocationSpec
  if len(names) > 0 {
    for _, tname := range names {
      if target, found := pc.targets[tname]; found {
        targetsToInvoke = append(targetsToInvoke, &target.InvocationSpec)
      }
    }
  } else {
    if len(pc.targets) > 0 {
      for _, target := range pc.targets {
        targetsToInvoke = append(targetsToInvoke, &target.InvocationSpec)
      }
    }
  }
  return targetsToInvoke
}

func (pc *PortClient) getTargetsToInvoke(r *http.Request) []*invocation.InvocationSpec {
  var names []string
  if r != nil {
    names, _ = util.GetListParam(r, "targets")
  }
  return prepareTargetsForPeers(pc.PrepareTargetsToInvoke(names), r)
}

func (pc *PortClient) setReportResponse(flag bool) {
  pc.resultsLock.Lock()
  defer pc.resultsLock.Unlock()
  pc.blockForResponse = flag
}

func (pc *PortClient) addTrackingHeaders(headers string) {
  pc.resultsLock.Lock()
  defer pc.resultsLock.Unlock()
  pieces := strings.Split(headers, ",")
  for _, h := range pieces {
    pc.trackingHeaders[strings.ToLower(h)] = 1
  }
}

func (pc *PortClient) removeTrackingHeader(header string) {
  pc.resultsLock.Lock()
  defer pc.resultsLock.Unlock()
  delete(pc.trackingHeaders, strings.ToLower(header))
}

func (pc *PortClient) clearTrackingHeaders() {
  pc.resultsLock.Lock()
  defer pc.resultsLock.Unlock()
  pc.trackingHeaders = map[string]int{}
}

func (pc *PortClient) getTrackingHeaders() []string {
  pc.resultsLock.RLock()
  defer pc.resultsLock.RUnlock()
  headers := []string{}
  for h := range pc.trackingHeaders {
    headers = append(headers, h)
  }
  return headers
}

func (pc *PortClient) getResults() string {
  pc.resultsLock.RLock()
  defer pc.resultsLock.RUnlock()
  return util.ToJSON(pc.targetResults)
}

func (pc *PortClient) clearResults() {
  pc.resultsLock.Lock()
  pc.targetResults = &TargetResults{}
  pc.resultsLock.Unlock()
  pc.initTargetResults()
}

func (pc *PortClient) addTargetResult(result *invocation.InvocationResult) {
  pc.resultsLock.Lock()
  defer pc.resultsLock.Unlock()
  if pc.targetResults.CountsByTargetStatusCode[result.TargetName] == nil {
    pc.targetResults.CountsByTargetStatus[result.TargetName] = map[string]int{}
    pc.targetResults.CountsByTargetStatusCode[result.TargetName] = map[int]int{}
    pc.targetResults.CountsByTargetHeaders[result.TargetName] = map[string]int{}
    pc.targetResults.CountsByTargetHeaderValues[result.TargetName] = map[string]map[string]int{}
  }
  pc.targetResults.TargetInvocationCounts[result.TargetName]++
  pc.targetResults.CountsByStatus[result.Status]++
  pc.targetResults.CountsByStatusCodes[result.StatusCode]++
  pc.targetResults.CountsByTargetStatus[result.TargetName][result.Status]++
  pc.targetResults.CountsByTargetStatusCode[result.TargetName][result.StatusCode]++
  for h, values := range result.Headers {
    h = strings.ToLower(h)
    if _, present := pc.trackingHeaders[h]; present {
      pc.targetResults.CountsByHeaders[h]++
      pc.targetResults.CountsByTargetHeaders[result.TargetName][h]++
      if pc.targetResults.CountsByHeaderValues[h] == nil {
        pc.targetResults.CountsByHeaderValues[h] = map[string]int{}
      }
      if pc.targetResults.CountsByTargetHeaderValues[result.TargetName][h] == nil {
        pc.targetResults.CountsByTargetHeaderValues[result.TargetName][h] = map[string]int{}
      }
      for _, v := range values {
        pc.targetResults.CountsByHeaderValues[h][v]++
        pc.targetResults.CountsByTargetHeaderValues[result.TargetName][h][v]++
      }
    }
  }
}

func (pc *PortClient) removeResultsForTargets(targets []string) {
  pc.resultsLock.Lock()
  defer pc.resultsLock.Unlock()
  for _, target := range targets {
    delete(pc.targetResults.TargetInvocationCounts, target)
    statuses := pc.targetResults.CountsByTargetStatus[target]
    if statuses != nil {
      for k, v := range statuses {
        pc.targetResults.CountsByStatus[k] -= v
        if pc.targetResults.CountsByStatus[k] == 0 {
          delete(pc.targetResults.CountsByStatus, k)
        }
      }
      delete(pc.targetResults.CountsByTargetStatus, target)
    }

    codes := pc.targetResults.CountsByTargetStatusCode[target]
    if codes != nil {
      for k, v := range codes {
        pc.targetResults.CountsByStatusCodes[k] -= v
        if pc.targetResults.CountsByStatusCodes[k] == 0 {
          delete(pc.targetResults.CountsByStatusCodes, k)
        }
      }
      delete(pc.targetResults.CountsByTargetStatusCode, target)
    }

    headers := pc.targetResults.CountsByTargetHeaders[target]
    if headers != nil {
      for k, v := range headers {
        pc.targetResults.CountsByHeaders[k] -= v
        if pc.targetResults.CountsByHeaders[k] == 0 {
          delete(pc.targetResults.CountsByHeaders, k)
        }
      }
      delete(pc.targetResults.CountsByTargetHeaders, target)
    }

    headerValues := pc.targetResults.CountsByTargetHeaderValues[target]
    if headerValues != nil {
      for h, values := range headerValues {
        if values != nil {
          for k, v := range values {
            pc.targetResults.CountsByHeaderValues[h][k] -= v
            if pc.targetResults.CountsByHeaderValues[h][k] == 0 {
              delete(pc.targetResults.CountsByHeaderValues[h], k)
            }
          }
        }
        if len(pc.targetResults.CountsByHeaderValues[h]) == 0 {
          delete(pc.targetResults.CountsByHeaderValues, h)
        }
      }
      delete(pc.targetResults.CountsByTargetHeaderValues, target)
    }
  }
}

func (pc *PortClient) RegisterInvocation() *invocation.InvocationChannels {
  pc.targetsLock.Lock()
  defer pc.targetsLock.Unlock()
  pc.invocationCounter++
  ic := &invocation.InvocationChannels{}
  ic.ID = pc.invocationCounter
  ic.StopChannel = make(chan string, 10)
  ic.DoneChannel = make(chan bool, 10)
  ic.ResultChannel = make(chan *invocation.InvocationResult, 10)
  pc.invocationChannels[pc.invocationCounter] = ic
  return ic
}

func (pc *PortClient) DeregisterInvocation(ic *invocation.InvocationChannels) {
  pc.targetsLock.Lock()
  defer pc.targetsLock.Unlock()
  if pc.invocationChannels[ic.ID] != nil {
    close(ic.StopChannel)
    close(ic.DoneChannel)
    close(ic.ResultChannel)
    delete(pc.invocationChannels, ic.ID)
  }
}

func (pc *PortClient) stopTarget(target *Target) {
  for _, c := range pc.invocationChannels {
    done := false
    select {
    case done = <-c.DoneChannel:
    default:
    }
    if !done {
      c.StopChannel <- target.Name
    }
  }
}

func (pc *PortClient) stopTargets(targetNames string) bool {
  pc.targetsLock.Lock()
  defer pc.targetsLock.Unlock()
  stopped := false
  if tnames := strings.Split(targetNames, ","); len(tnames) > 0 && len(tnames[0]) > 0 {
    for _, tname := range tnames {
      if len(tname) > 0 {
        if target, found := pc.targets[tname]; found {
          pc.stopTarget(target)
          stopped = true
        }
      }
    }
  } else {
    if len(pc.targets) > 0 {
      for _, target := range pc.targets {
        pc.stopTarget(target)
        stopped = true
      }
    }
  }
  return stopped
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
  t := &Target{}
  if err := util.ReadJsonPayload(r, t); err == nil {
    if err := invocation.ValidateSpec(&t.InvocationSpec); err != nil {
      w.WriteHeader(http.StatusBadRequest)
      fmt.Fprintf(w, "Invalid target spec: %s\n", err.Error())
      log.Println(err)
    } else {
      t.Headers = append(t.Headers, []string{"Goto-Client", listeners.DefaultLabel})
      pc := getPortClient(r)
      pc.AddTarget(t)
      log.Printf("Added target: %s\n", util.ToJSON(t))
      w.WriteHeader(http.StatusOK)
      fmt.Fprintf(w, "Added target: %s\n", util.ToJSON(t))
    }
  } else {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintf(w, "Failed to parse json\n")
    log.Println(err)
  }
}

func removeTarget(w http.ResponseWriter, r *http.Request) {
  if tname, present := util.GetStringParam(r, "target"); present {
    if getPortClient(r).removeTarget(tname) {
      w.WriteHeader(http.StatusOK)
      fmt.Fprintf(w, "Target Removed: %s\n", tname)
      log.Printf("Removed target: %s\n", tname)
    } else {
      w.WriteHeader(http.StatusOK)
      fmt.Fprintf(w, "Target not found: %s\n", tname)
    }
  } else {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintln(w, "No target given")
  }
}

func clearTargets(w http.ResponseWriter, r *http.Request) {
  getPortClient(r).init()
  w.WriteHeader(http.StatusOK)
  fmt.Fprintln(w, "Targets cleared")
  log.Println("Targets cleared")
}

func getTargets(w http.ResponseWriter, r *http.Request) {
  util.WriteJsonPayload(w, getPortClient(r).targets)
}

func setOrGetBlocking(w http.ResponseWriter, r *http.Request) {
  pc := getPortClient(r)
  if flag, present := util.GetStringParam(r, "flag"); present {
    pc.setReportResponse(
      strings.EqualFold(flag, "y") || strings.EqualFold(flag, "yes") ||
        strings.EqualFold(flag, "true") || strings.EqualFold(flag, "1"))
    w.WriteHeader(http.StatusAccepted)
    if pc.blockForResponse {
      fmt.Fprintln(w, "Invocation will block for results")
    } else {
      fmt.Fprintln(w, "Invocation will not block for results")
    }
  } else {
    w.WriteHeader(http.StatusOK)
    if pc.blockForResponse {
      fmt.Fprintln(w, "Invocation will block for results")
    } else {
      fmt.Fprintln(w, "Invocation will not block for results")
    }
  }
}

func addTrackingHeaders(w http.ResponseWriter, r *http.Request) {
  if h, present := util.GetStringParam(r, "headers"); present {
    getPortClient(r).addTrackingHeaders(h)
    w.WriteHeader(http.StatusAccepted)
    fmt.Fprintf(w, "Header %s will be tracked\n", h)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintf(w, "Invalid header name")
  }
}

func removeTrackingHeader(w http.ResponseWriter, r *http.Request) {
  if header, present := util.GetStringParam(r, "header"); present {
    getPortClient(r).removeTrackingHeader(header)
    w.WriteHeader(http.StatusAccepted)
    fmt.Fprintf(w, "Header %s removed from tracking\n", header)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    fmt.Fprintf(w, "Invalid header name")
  }
}

func clearTrackingHeaders(w http.ResponseWriter, r *http.Request) {
  getPortClient(r).clearTrackingHeaders()
  w.WriteHeader(http.StatusAccepted)
  fmt.Fprintln(w, "All tracking headers cleared")
}

func getTrackingHeaders(w http.ResponseWriter, r *http.Request) {
  w.WriteHeader(http.StatusOK)
  fmt.Fprintln(w, strings.Join(getPortClient(r).getTrackingHeaders(), ","))
}

func getResults(w http.ResponseWriter, r *http.Request) {
  output := getPortClient(r).getResults()
  w.WriteHeader(http.StatusAlreadyReported)
  fmt.Fprintln(w, output)
}

func clearResults(w http.ResponseWriter, r *http.Request) {
  if targets, present := util.GetListParam(r, "targets"); present {
    getPortClient(r).removeResultsForTargets(targets)
    w.WriteHeader(http.StatusOK)
    fmt.Fprintf(w, "Results cleared for targets: %+v\n", targets)
  } else {
    getPortClient(r).clearResults()
    w.WriteHeader(http.StatusOK)
    fmt.Fprintln(w, "Results cleared")
  }
}

func stopTargets(w http.ResponseWriter, r *http.Request) {
  pc := getPortClient(r)
  stopped := pc.stopTargets(util.GetStringParamValue(r, "targets"))
  if stopped {
    w.WriteHeader(http.StatusOK)
    fmt.Fprintln(w, "Targets stopped")
  } else {
    w.WriteHeader(http.StatusOK)
    fmt.Fprintln(w, "No targets to stop")
  }
}

func invokeTargetsAndStoreResults(pc *PortClient, targets []*invocation.InvocationSpec, ic *invocation.InvocationChannels) []*invocation.InvocationResult {
  pc.initTargetResults()
  results := []*invocation.InvocationResult{}
  c := make(chan bool)
  go func() {
    results = invocation.InvokeTargets(targets, ic, pc.blockForResponse)
    pc.DeregisterInvocation(ic)
    c <- true
  }()
  Results:
  for {
    select {
    case <-ic.DoneChannel:
      break Results
    case <-ic.StopChannel:
      break Results
    case result := <-ic.ResultChannel:
      if result != nil {
        pc.addTargetResult(result)
      }
    }
  }
  <-c
  return results
}

func invokeTargets(w http.ResponseWriter, r *http.Request) {
  pc := getPortClient(r)
  targetsToInvoke := pc.getTargetsToInvoke(r)
  if len(targetsToInvoke) > 0 {
    invocationChannels := pc.RegisterInvocation()
    var results []*invocation.InvocationResult
    if pc.blockForResponse {
      results = invokeTargetsAndStoreResults(pc, targetsToInvoke, invocationChannels)
      w.WriteHeader(http.StatusAlreadyReported)
      fmt.Fprintln(w, util.ToJSON(results))
    } else {
      go invokeTargetsAndStoreResults(pc, targetsToInvoke, invocationChannels)
      w.WriteHeader(http.StatusOK)
      fmt.Fprintln(w, "Targets invoked")
    }
  } else {
    w.WriteHeader(http.StatusOK)
    fmt.Fprintln(w, "No targets to invoke")
  }
}

func InvokeTargetsByNames(pc *PortClient, targets []string) {
  targetsToInvoke := pc.PrepareTargetsToInvoke(targets)
  if len(targetsToInvoke) > 0 {
    invocationChannels := pc.RegisterInvocation()
    go invokeTargetsAndStoreResults(pc, targetsToInvoke, invocationChannels)
  }
}

func InvokeTargets(pc *PortClient, targets []*invocation.InvocationSpec) {
  if len(targets) > 0 {
    invocationChannels := pc.RegisterInvocation()
    go invokeTargetsAndStoreResults(pc, targets, invocationChannels)
  }
}
