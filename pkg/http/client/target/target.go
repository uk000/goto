package target

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"

	"goto/pkg/http/invocation"
	"goto/pkg/http/registry"
	"goto/pkg/util"

	"github.com/gorilla/mux"
)

type Target struct {
  invocation.InvocationSpec
}

type TargetResults struct {
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

func (pc *PortClient) AddTarget(t *Target) {
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

func (pc *PortClient) getTargetsToInvoke(r *http.Request) []*invocation.InvocationSpec {
  names := ""
  if r != nil {
    names = util.GetStringParamValue(r, "targets")
  }
  pc.targetsLock.RLock()
  defer pc.targetsLock.RUnlock()
  var targetsToInvoke []*invocation.InvocationSpec
  if tnames := strings.Split(names, ","); len(tnames) > 0 && len(tnames[0]) > 0 {
    for _, tname := range tnames {
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
  return prepareTargetsForPeers(targetsToInvoke, r)
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
      pc := getPortClient(r)
      pc.AddTarget(t)
      if t.AutoInvoke {
        go func(){
          invocationChannels := pc.registerInvocation()
          invokeTargetsAndStoreResults(pc, []*invocation.InvocationSpec{&t.InvocationSpec}, invocationChannels)
        }()
      }
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
  pc := getPortClient(r)
  pc.resultsLock.Lock()
  defer pc.resultsLock.Unlock()
  // reverting what addTargetResult did
  names := util.GetStringParamValue(r, "targets")
  if tnames := strings.Split(names, ","); len(tnames) > 0 && len(tnames[0]) > 0 {
    for _, tname := range tnames {
      if target, found := pc.targets[tname]; found {
        clearSingleResult(pc, &target.InvocationSpec)
      }
    }
  } else {
    pc.targetResults = &TargetResults{}
  }
  w.WriteHeader(http.StatusOK)
  fmt.Fprintln(w, "Results cleared")
}

func clearSingleResult(pc *PortClient, t *invocation.InvocationSpec) {
  statuses := pc.targetResults.CountsByTargetStatus[t.Name]
  if statuses != nil {
    for k, v := range statuses {
      pc.targetResults.CountsByStatus[k] -= v
    }
    delete(pc.targetResults.CountsByTargetStatus, t.Name)
  }
  codes := pc.targetResults.CountsByTargetStatusCode[t.Name]
  if codes != nil {
    for k, v := range codes {
      pc.targetResults.CountsByStatusCodes[k] -= v
    }
    delete(pc.targetResults.CountsByTargetStatusCode, t.Name)
  }
  headers := pc.targetResults.CountsByTargetHeaders[t.Name]
  if headers != nil {
    for k, v := range headers {
      pc.targetResults.CountsByHeaders[k] -= v
    }
    delete(pc.targetResults.CountsByTargetHeaders, t.Name)
  }
  headerValues := pc.targetResults.CountsByTargetHeaderValues[t.Name]
  if headerValues != nil {
    for h, values := range headerValues {
      if values != nil {
        for k, v := range values {
          pc.targetResults.CountsByHeaderValues[h][k] -= v
        }
      }
    }
    delete(pc.targetResults.CountsByTargetHeaderValues, t.Name)
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

func invokeTargetsAndStoreResults(pc *PortClient, targetsToInvoke []*invocation.InvocationSpec,
  invocationChannels *invocation.InvocationChannels) []*invocation.InvocationResult {
  results := invocation.InvokeTargets(targetsToInvoke, invocationChannels, pc.blockForResponse)
  pc.deregisterInvocation(invocationChannels)
  for _, result := range results {
    pc.addTargetResult(result)
  }
  return results
}

func invokeTargets(w http.ResponseWriter, r *http.Request) {
  pc := getPortClient(r)
  targetsToInvoke := pc.getTargetsToInvoke(r)
  if len(targetsToInvoke) > 0 {
    invocationChannels := pc.registerInvocation()
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

func InvokeTargets(pc *PortClient) {
  targetsToInvoke := pc.getTargetsToInvoke(nil)
  if len(targetsToInvoke) > 0 {
    invocationChannels := pc.registerInvocation()
    invokeTargetsAndStoreResults(pc, targetsToInvoke, invocationChannels)
  }
}