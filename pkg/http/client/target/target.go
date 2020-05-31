package target

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"

	"goto/pkg/http/invocation"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

type TargetResults struct {
  CountsByStatus             map[string]int
  CountsByStatusCodes        map[int]int
  CountsByHeaders            map[string]int
  CountsByHeaderValues       map[string]map[string]int
  CountsByTargetStatus       map[string]map[string]int
  CountsByTargetStatusCode   map[string]map[int]int
  CountsByTargetHeaders      map[string]map[string]int
  CountsByTargetHeaderValues map[string]map[string]map[string]int
}

type PortClient struct {
  targets            map[string]*invocation.InvocationSpec
  invocationChannels map[int]*invocation.InvocationChannels
  invocationCounter  int
  targetsLock        sync.RWMutex
  reportResponse     bool
  trackingHeaders    map[string]int
  targetResults      *TargetResults
  resultsLock        sync.RWMutex
}

var (
  Handler         util.ServerHandler     = util.ServerHandler{Name: "client", SetRoutes: SetRoutes}
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

  util.AddRoute(r, "/reporting/set/{flag}", setOrGetReportingFlag, "POST", "PUT")
  util.AddRoute(r, "/reporting", setOrGetReportingFlag, "GET")
  util.AddRoute(r, "/track/headers/add/{headers}", addTrackingHeaders, "POST", "PUT")
  util.AddRoute(r, "/track/headers/remove/{header}", removeTrackingHeader, "POST", "PUT")
  util.AddRoute(r, "/track/headers/list", getTrackingHeaders, "GET")
  util.AddRoute(r, "/track/headers/clear", clearTrackingHeaders, "POST")
  util.AddRoute(r, "/track/headers", getTrackingHeaders, "GET")
  util.AddRoute(r, "/results", getResults, "GET")
  util.AddRoute(r, "/results/clear", clearResults, "POST")
}

func (pc *PortClient) init() {
  pc.targetsLock.Lock()
  defer pc.targetsLock.Unlock()
  pc.targets = map[string]*invocation.InvocationSpec{}
  pc.invocationChannels = map[int]*invocation.InvocationChannels{}
  pc.invocationCounter = 0
  pc.reportResponse = false
  pc.trackingHeaders = map[string]int{}
  pc.targetResults = &TargetResults{}
}

func (pc *PortClient) initTargetResults() {
  pc.resultsLock.Lock()
  defer pc.resultsLock.Unlock()
  if pc.targetResults.CountsByStatusCodes == nil {
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

func (pc *PortClient) addTarget(t *invocation.InvocationSpec) {
  pc.targetsLock.Lock()
  defer pc.targetsLock.Unlock()
  pc.targets[t.Name] = t
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

func (pc *PortClient) getTargetsToInvoke(names string) []*invocation.InvocationSpec {
  pc.targetsLock.RLock()
  defer pc.targetsLock.RUnlock()
  var targetsToInvoke []*invocation.InvocationSpec
  if tnames := strings.Split(names, ","); len(tnames) > 0 && len(tnames[0]) > 0 {
    for _, tname := range tnames {
      if target, found := pc.targets[tname]; found {
        targetsToInvoke = append(targetsToInvoke, target)
      }
    }
  } else {
    if len(pc.targets) > 0 {
      for _, target := range pc.targets {
        targetsToInvoke = append(targetsToInvoke, target)
      }
    }
  }
  return targetsToInvoke
}

func (pc *PortClient) setReportResponse(flag bool) {
  pc.resultsLock.Lock()
  defer pc.resultsLock.Unlock()
  pc.reportResponse = flag
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
  defer pc.resultsLock.Unlock()
  pc.targetResults = &TargetResults{}
}

func (pc *PortClient) addTargetResult(result *invocation.InvocationResult) {
  pc.initTargetResults()
  pc.resultsLock.Lock()
  defer pc.resultsLock.Unlock()
  if pc.targetResults.CountsByTargetStatusCode[result.TargetName] == nil {
    pc.targetResults.CountsByTargetStatus[result.TargetName] = map[string]int{}
    pc.targetResults.CountsByTargetStatusCode[result.TargetName] = map[int]int{}
    pc.targetResults.CountsByTargetHeaders[result.TargetName] = map[string]int{}
    pc.targetResults.CountsByTargetHeaderValues[result.TargetName] = map[string]map[string]int{}
  }
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

func (pc *PortClient) registerInvocation() *invocation.InvocationChannels {
  pc.targetsLock.Lock()
  defer pc.targetsLock.Unlock()
  pc.invocationCounter++
  pc.invocationChannels[pc.invocationCounter] = &invocation.InvocationChannels{}
  pc.invocationChannels[pc.invocationCounter].ID = pc.invocationCounter
  pc.invocationChannels[pc.invocationCounter].StopChannel = make(chan string, 10)
  pc.invocationChannels[pc.invocationCounter].DoneChannel = make(chan bool, 10)
  return pc.invocationChannels[pc.invocationCounter]
}

func (pc *PortClient) deregisterInvocation(i *invocation.InvocationChannels) {
  pc.targetsLock.Lock()
  defer pc.targetsLock.Unlock()
  if pc.invocationChannels[i.ID] != nil {
    close(i.StopChannel)
    delete(pc.invocationChannels, i.ID)
  }
}

func (pc *PortClient) stopTarget(target *invocation.InvocationSpec) {
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

func getPortClient(r *http.Request) *PortClient {
  listenerPort := util.GetListenerPort(r)
  portClientsLock.Lock()
  defer portClientsLock.Unlock()
  pc := portClients[listenerPort]
  if pc == nil {
    pc = &PortClient{}
    pc.init()
    portClients[listenerPort] = pc
  }
  return pc
}

func addTarget(w http.ResponseWriter, r *http.Request) {
  var t invocation.InvocationSpec
  if err := util.ReadJsonPayload(r, &t); err == nil {
    if err := invocation.ValidateSpec(&t); err != nil {
      w.WriteHeader(http.StatusBadRequest)
      fmt.Fprintf(w, "Invalid target spec: %s\n", err.Error())
      log.Println(err)
    } else {
      getPortClient(r).addTarget(&t)
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
}

func getTargets(w http.ResponseWriter, r *http.Request) {
  util.WriteJsonPayload(w, getPortClient(r).targets)
}

func setOrGetReportingFlag(w http.ResponseWriter, r *http.Request) {
  pc := getPortClient(r)
  if flag, present := util.GetStringParam(r, "flag"); present {
    pc.setReportResponse(
      strings.EqualFold(flag, "y") || strings.EqualFold(flag, "yes") ||
        strings.EqualFold(flag, "true") || strings.EqualFold(flag, "1"))
    w.WriteHeader(http.StatusAccepted)
    fmt.Fprintf(w, "Reporting set to: %t\n", pc.reportResponse)
  } else {
    w.WriteHeader(http.StatusOK)
    fmt.Fprintf(w, "Reporting: %t\n", pc.reportResponse)
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
  getPortClient(r).clearResults()
  w.WriteHeader(http.StatusOK)
  fmt.Fprintln(w, "Results cleared")
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

func invokeTargetsAndStoreResults(pc *PortClient, targetsToInvoke []*invocation.InvocationSpec,
  invocationChannels *invocation.InvocationChannels) []*invocation.InvocationResult {
  results := invocation.InvokeTargets(targetsToInvoke, invocationChannels, pc.reportResponse)
  pc.deregisterInvocation(invocationChannels)
  for _, result := range results {
    pc.addTargetResult(result)
  }
  return results
}

func invokeTargets(w http.ResponseWriter, r *http.Request) {
  pc := getPortClient(r)
  targetsToInvoke := pc.getTargetsToInvoke(util.GetStringParamValue(r, "targets"))
  if len(targetsToInvoke) > 0 {
    invocationChannels := pc.registerInvocation()
    var results []*invocation.InvocationResult
    if pc.reportResponse {
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
