package target

import (
	"fmt"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"goto/pkg/global"
	"goto/pkg/http/invocation"
	"goto/pkg/http/registry"
	"goto/pkg/http/server/listeners"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

type Target struct {
  invocation.InvocationSpec
}

type TargetResponseTimes struct {
  FirstResponse time.Time
  LastResponse  time.Time
}

type TargetResults struct {
  TargetInvocationCounts     map[string]int                       `json:"targetInvocationCounts"`
  TargetFirstResponses       map[string]time.Time                 `json:"targetFirstResponses"`
  TargetLastResponses        map[string]time.Time                 `json:"targetLastResponses"`
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
  targets                   map[string]*Target
  activeInvocations         map[int]*invocation.InvocationChannels
  invocationCounter         int
  targetsLock               sync.RWMutex
  blockForResponse          bool
  trackingHeaders           map[string]int
  targetResults             *TargetResults
  targetResultsByInvocation map[int]*TargetResults
  resultsLock               sync.RWMutex
}

var (
  Handler                    util.ServerHandler     = util.ServerHandler{Name: "client", SetRoutes: SetRoutes}
  portClients                map[string]*PortClient = map[string]*PortClient{}
  portClientsLock            sync.RWMutex
  InvocationResultsRetention int = 10
)

func SetRoutes(r *mux.Router, parent *mux.Router, root *mux.Router) {
  targetsRouter := r.PathPrefix("/targets").Subrouter()
  util.AddRoute(targetsRouter, "/add", addTarget, "POST")
  util.AddRoute(targetsRouter, "/{targets}/remove", removeTargets, "POST")
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
  util.AddRoute(r, "/track/headers/clear", clearTrackingHeaders, "POST")
  util.AddRoute(r, "/track/headers/list", getTrackingHeaders, "GET")
  util.AddRoute(r, "/track/headers", getTrackingHeaders, "GET")
  util.AddRoute(r, "/results", getResults, "GET")
  util.AddRoute(r, "/results/invocations", getInvocationResults, "GET")
  util.AddRoute(r, "/results/{targets}/clear", clearResults, "POST")
  util.AddRoute(r, "/results/clear", clearResults, "POST")
}

func (pc *PortClient) init() {
  pc.targetsLock.Lock()
  defer pc.targetsLock.Unlock()
  pc.targets = map[string]*Target{}
  pc.activeInvocations = map[int]*invocation.InvocationChannels{}
  pc.invocationCounter = 0
  pc.blockForResponse = false
  pc.trackingHeaders = map[string]int{}
  pc.targetResults = &TargetResults{}
  pc.targetResultsByInvocation = map[int]*TargetResults{}
}

func initResults(targetResults *TargetResults) {
  targetResults.TargetInvocationCounts = map[string]int{}
  targetResults.TargetFirstResponses = map[string]time.Time{}
  targetResults.TargetLastResponses = map[string]time.Time{}
  targetResults.CountsByStatusCodes = map[int]int{}
  targetResults.CountsByStatus = map[string]int{}
  targetResults.CountsByHeaders = map[string]int{}
  targetResults.CountsByHeaderValues = map[string]map[string]int{}
  targetResults.CountsByTargetStatus = map[string]map[string]int{}
  targetResults.CountsByTargetStatusCode = map[string]map[int]int{}
  targetResults.CountsByTargetHeaders = map[string]map[string]int{}
  targetResults.CountsByTargetHeaderValues = map[string]map[string]map[string]int{}
}

func (pc *PortClient) isTargetResultsInitialized(index ...int) bool {
  if pc.targetResults == nil || pc.targetResults.TargetInvocationCounts == nil {
    return false
  }
  if pc.targetResultsByInvocation == nil || (len(index) > 0 && pc.targetResultsByInvocation[index[0]] == nil) {
    return false
  }
  return true
}

func (pc *PortClient) initTargetResults(index ...int) {
  if ! pc.isTargetResultsInitialized() {
    pc.targetResults = &TargetResults{}
    initResults(pc.targetResults)
  }
  if !pc.isTargetResultsInitialized(index...) {
    if pc.targetResultsByInvocation == nil {
      pc.targetResultsByInvocation = map[int]*TargetResults{}
    }
    pc.targetResultsByInvocation[index[0]] = &TargetResults{}
    initResults(pc.targetResultsByInvocation[index[0]])
  }
}

func (pc *PortClient) AddTarget(t *Target) {
  pc.targetsLock.Lock()
  defer pc.targetsLock.Unlock()
  pc.targets[t.Name] = t
  if t.AutoInvoke {
    go func() {
      log.Printf("Auto-invoking target: %s\n", t.Name)
      pc.invocationCounter++
      ic := invocation.RegisterInvocation(pc.invocationCounter)
      pc.activeInvocations[ic.ID] = ic
      invokeTargetsAndStoreResults(pc, []*invocation.InvocationSpec{&t.InvocationSpec}, ic)
    }()
  }
}

func (pc *PortClient) removeTargets(targets []string) {
  pc.targetsLock.Lock()
  defer pc.targetsLock.Unlock()
  for _, t := range targets {
    delete(pc.targets, t)
  }
}

func prepareTargetsForPeers(targets []*invocation.InvocationSpec, r *http.Request) []*invocation.InvocationSpec {
  targetsToInvoke := []*invocation.InvocationSpec{}
  for _, t := range targets {
    added := false
    if strings.HasPrefix(t.Name, "{") && strings.HasSuffix(t.Name, "}") {
      p := strings.TrimLeft(t.Name, "{")
      p = strings.TrimRight(p, "}")
      if r != nil {
        if peer := registry.GetPeer(p, r); peer != nil {
          if strings.Contains(t.URL, "{") && strings.Contains(t.URL, "}") {
            urlPre := strings.Split(t.URL, "{")[0]
            urlPost := strings.Split(t.URL, "}")[1]
            for address := range peer.Pods {
              var target = *t
              target.Name = peer.Name
              target.URL = urlPre + address + urlPost
              targetsToInvoke = append(targetsToInvoke, &target)
              added = true
            }
          }
        }
      }
    } 
    if !added {
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
      target, found := pc.targets[tname]
      if !found {
        target, found = pc.targets["{"+tname+"}"]
      }
      if found {
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

func (pc *PortClient) getInvocationResults() string {
  pc.resultsLock.RLock()
  defer pc.resultsLock.RUnlock()
  return util.ToJSON(pc.targetResultsByInvocation)
}

func (pc *PortClient) clearResults() {
  pc.targetsLock.Lock()
  pc.invocationCounter = 0
  pc.targetResults = &TargetResults{}
  pc.targetResultsByInvocation = map[int]*TargetResults{}
  pc.initTargetResults()
  pc.targetsLock.Unlock()
}

func storeJobResultsInRegistryLocker(key string, targetResults *TargetResults) {
  if global.UseLocker && global.RegistryURL != "" {
    url := global.RegistryURL + "/registry/peers/" + global.PeerName + "/locker/store/" + key
    if resp, err := http.Post(url, "application/json",
      strings.NewReader(util.ToJSON(targetResults))); err == nil {
      defer resp.Body.Close()
      log.Printf("Stored invocation results under locker key %s for peer [%s] with registry [%s]\n", key, global.PeerName, global.RegistryURL)
    }
  }
}

func (pc *PortClient) storeResults(result *invocation.InvocationResult, targetResults *TargetResults) {
  if targetResults.CountsByTargetStatusCode[result.TargetName] == nil {
    targetResults.CountsByTargetStatus[result.TargetName] = map[string]int{}
    targetResults.CountsByTargetStatusCode[result.TargetName] = map[int]int{}
    targetResults.CountsByTargetHeaders[result.TargetName] = map[string]int{}
    targetResults.CountsByTargetHeaderValues[result.TargetName] = map[string]map[string]int{}
  }
  targetResults.TargetInvocationCounts[result.TargetName]++
  now := time.Now()
  if targetResults.TargetFirstResponses[result.TargetName].IsZero() {
    targetResults.TargetFirstResponses[result.TargetName] = now
  }
  targetResults.TargetLastResponses[result.TargetName] = now
  targetResults.CountsByStatus[result.Status]++
  targetResults.CountsByStatusCodes[result.StatusCode]++
  targetResults.CountsByTargetStatus[result.TargetName][result.Status]++
  targetResults.CountsByTargetStatusCode[result.TargetName][result.StatusCode]++
  trackingHeaders := pc.trackingHeaders
  for h, values := range result.Headers {
    h = strings.ToLower(h)
    if _, present := trackingHeaders[h]; present {
      targetResults.CountsByHeaders[h]++
      targetResults.CountsByTargetHeaders[result.TargetName][h]++
      if targetResults.CountsByHeaderValues[h] == nil {
        targetResults.CountsByHeaderValues[h] = map[string]int{}
      }
      if targetResults.CountsByTargetHeaderValues[result.TargetName][h] == nil {
        targetResults.CountsByTargetHeaderValues[result.TargetName][h] = map[string]int{}
      }
      for _, v := range values {
        targetResults.CountsByHeaderValues[h][v]++
        targetResults.CountsByTargetHeaderValues[result.TargetName][h][v]++
      }
    }
  }
}

func (pc *PortClient) storeTargetResult(invocationIndex int, result *invocation.InvocationResult) {
  pc.targetsLock.Lock()
  defer pc.targetsLock.Unlock()
  pc.initTargetResults(invocationIndex)
  pc.storeResults(result, pc.targetResults)
  for len(pc.targetResultsByInvocation) >= InvocationResultsRetention {
    oldest := math.MaxInt32
    for i := range pc.targetResultsByInvocation {
      if i < oldest {
        oldest = i
      }
    }
    delete(pc.targetResultsByInvocation, oldest)
  }
  if pc.targetResultsByInvocation[invocationIndex] == nil {
    pc.targetResultsByInvocation[invocationIndex] = &TargetResults{}
  }
  pc.storeResults(result, pc.targetResultsByInvocation[invocationIndex])
  storeJobResultsInRegistryLocker("client", pc.targetResults)
  storeJobResultsInRegistryLocker("client_"+strconv.Itoa(invocationIndex), pc.targetResultsByInvocation[invocationIndex])
}

func (pc *PortClient) removeResultsForTargets(targets []string) {
  pc.targetsLock.Lock()
  defer pc.targetsLock.Unlock()
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

func (pc *PortClient) stopTarget(target *Target) {
  for _, c := range pc.activeInvocations {
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

func (pc *PortClient) stopTargets(targetNames []string) bool {
  pc.targetsLock.Lock()
  defer pc.targetsLock.Unlock()
  stopped := false
  if len(targetNames) > 0 {
    for _, tname := range targetNames {
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
  msg := ""
  t := &Target{}
  if err := util.ReadJsonPayload(r, t); err == nil {
    if err := invocation.ValidateSpec(&t.InvocationSpec); err != nil {
      w.WriteHeader(http.StatusBadRequest)
      msg = fmt.Sprintf("Invalid target spec: %s", err.Error())
    } else {
      t.Headers = append(t.Headers, []string{"Goto-Client", listeners.DefaultLabel})
      pc := getPortClient(r)
      pc.AddTarget(t)
      w.WriteHeader(http.StatusOK)
      msg = fmt.Sprintf("Added target: %s", util.ToJSON(t))
    }
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "Failed to parse json"
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func removeTargets(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if targets, present := util.GetListParam(r, "targets"); present {
    getPortClient(r).removeTargets(targets)
    w.WriteHeader(http.StatusOK)
    msg = fmt.Sprintf("Targets Removed: %+v", targets)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "No target given"
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func clearTargets(w http.ResponseWriter, r *http.Request) {
  getPortClient(r).init()
  w.WriteHeader(http.StatusOK)
  msg := "Targets cleared"
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func getTargets(w http.ResponseWriter, r *http.Request) {
  util.WriteJsonPayload(w, getPortClient(r).targets)
  util.AddLogMessage("Reporting targets", r)
}

func setOrGetBlocking(w http.ResponseWriter, r *http.Request) {
  msg := ""
  pc := getPortClient(r)
  if flag, present := util.GetStringParam(r, "flag"); present {
    pc.setReportResponse(util.IsYes(flag))
    w.WriteHeader(http.StatusAccepted)
    if pc.blockForResponse {
      msg = "Invocation will block for results"
    } else {
      msg = "Invocation will not block for results"
    }
  } else {
    w.WriteHeader(http.StatusOK)
    if pc.blockForResponse {
      msg = "Invocation will block for results"
    } else {
      msg = "Invocation will not block for results"
    }
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func addTrackingHeaders(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if h, present := util.GetStringParam(r, "headers"); present {
    getPortClient(r).addTrackingHeaders(h)
    w.WriteHeader(http.StatusAccepted)
    msg = fmt.Sprintf("Header %s will be tracked", h)
  } else {
    w.WriteHeader(http.StatusBadRequest)
    msg = "Invalid header name"
  }
  util.AddLogMessage(msg, r)
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
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func clearTrackingHeaders(w http.ResponseWriter, r *http.Request) {
  getPortClient(r).clearTrackingHeaders()
  w.WriteHeader(http.StatusAccepted)
  msg := "All tracking headers cleared"
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func getTrackingHeaders(w http.ResponseWriter, r *http.Request) {
  w.WriteHeader(http.StatusOK)
  msg := fmt.Sprintf("Tracking headers: %s", strings.Join(getPortClient(r).getTrackingHeaders(), ","))
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}


func getInvocationResults(w http.ResponseWriter, r *http.Request) {
  output := getPortClient(r).getInvocationResults()
  w.WriteHeader(http.StatusAlreadyReported)
  fmt.Fprintln(w, output)
  util.AddLogMessage("Reporting results", r)
}

func getResults(w http.ResponseWriter, r *http.Request) {
  output := getPortClient(r).getResults()
  w.WriteHeader(http.StatusAlreadyReported)
  fmt.Fprintln(w, output)
  util.AddLogMessage("Reporting results", r)
}

func clearResults(w http.ResponseWriter, r *http.Request) {
  msg := ""
  if targets, present := util.GetListParam(r, "targets"); present {
    getPortClient(r).removeResultsForTargets(targets)
    w.WriteHeader(http.StatusOK)
    msg = fmt.Sprintf("Results cleared for targets: %+v", targets)
  } else {
    getPortClient(r).clearResults()
    w.WriteHeader(http.StatusOK)
    msg = "Results cleared"
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func stopTargets(w http.ResponseWriter, r *http.Request) {
  msg := ""
  pc := getPortClient(r)
  targets, _ := util.GetListParam(r, "targets")
  stopped := pc.stopTargets(targets)
  if stopped {
    w.WriteHeader(http.StatusOK)
    msg = fmt.Sprintf("Targets %+v stopped", targets)
  } else {
    w.WriteHeader(http.StatusOK)
    msg = "No targets to stop"
  }
  util.AddLogMessage(msg, r)
  fmt.Fprintln(w, msg)
}

func invokeTargetsAndStoreResults(pc *PortClient, targets []*invocation.InvocationSpec, ic *invocation.InvocationChannels) []*invocation.InvocationResult {
  pc.targetsLock.Lock()
  pc.initTargetResults(ic.ID)
  pc.targetsLock.Unlock()
  results := []*invocation.InvocationResult{}
  c := make(chan bool)
  go func() {
    results = invocation.InvokeTargets(targets, ic, pc.blockForResponse)
    c <- true
  }()
  done := false
  Results:
  for {
    select {
    case done = <-ic.DoneChannel:
      break Results
    case result := <-ic.ResultChannel:
      if result != nil {
        pc.storeTargetResult(ic.ID, result)
      }
    }
  }
  <-c
  if done {
  MoreResults:
    for {
      select {
      case result := <-ic.ResultChannel:
        if result != nil {
          pc.storeTargetResult(ic.ID, result)
        }
      default:
        break MoreResults
      }
    }
  }
  delete(pc.activeInvocations, ic.ID)
  invocation.DeregisterInvocation(ic)
  return results
}

func invokeTargets(w http.ResponseWriter, r *http.Request) {
  pc := getPortClient(r)
  targetsToInvoke := pc.getTargetsToInvoke(r)
  if len(targetsToInvoke) > 0 {
    pc.targetsLock.Lock()
    pc.invocationCounter++
    ic := invocation.RegisterInvocation(pc.invocationCounter)
    pc.activeInvocations[ic.ID] = ic
    pc.targetsLock.Unlock()
    var results []*invocation.InvocationResult
    if pc.blockForResponse {
      results = invokeTargetsAndStoreResults(pc, targetsToInvoke, ic)
      w.WriteHeader(http.StatusAlreadyReported)
      fmt.Fprintln(w, util.ToJSON(results))
      util.AddLogMessage("Targets invoked", r)
    } else {
      go invokeTargetsAndStoreResults(pc, targetsToInvoke, ic)
      w.WriteHeader(http.StatusOK)
      fmt.Fprintln(w, "Targets invoked")
      util.AddLogMessage("Targets invoked", r)
    }
  } else {
    w.WriteHeader(http.StatusOK)
    fmt.Fprintln(w, "No targets to invoke")
    util.AddLogMessage("No targets to invoke", r)
  }
}
