package results

import (
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/http/invocation"
	"goto/pkg/util"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type TargetResults struct {
  Target               string                    `json:"target"`
  InvocationCounts     int                       `json:"invocationCounts"`
  FirstResponse        time.Time                 `json:"firstResponses"`
  LastResponse         time.Time                 `json:"lastResponses"`
  CountsByStatus       map[string]int            `json:"couuntsByStatus"`
  CountsByStatusCodes  map[int]int               `json:"countsByStatusCodes"`
  CountsByHeaders      map[string]int            `json:"countsByHeaders"`
  CountsByHeaderValues map[string]map[string]int `json:"countsByHeaderValues"`
  CountsByURIs         map[string]int            `json:"countsByURIs"`
  lock                 sync.RWMutex
}

type TargetsSummaryResults struct {
  TargetInvocationCounts     map[string]int                       `json:"targetInvocationCounts"`
  TargetFirstResponses       map[string]time.Time                 `json:"targetFirstResponses"`
  TargetLastResponses        map[string]time.Time                 `json:"targetLastResponses"`
  CountsByStatusCodes        map[int]int                          `json:"countsByStatusCodes"`
  CountsByHeaders            map[string]int                       `json:"countsByHeaders"`
  CountsByHeaderValues       map[string]map[string]int            `json:"countsByHeaderValues"`
  CountsByTargetStatusCodes  map[string]map[int]int               `json:"countsByTargetStatusCodes"`
  CountsByTargetHeaders      map[string]map[string]int            `json:"countsByTargetHeaders"`
  CountsByTargetHeaderValues map[string]map[string]map[string]int `json:"countsByTargetHeaderValues"`
}

type TargetsResults struct {
  Results map[string]*TargetResults `json:"results"`
  lock    sync.RWMutex
}

type InvocationResults struct {
  InvocationIndex uint32                       `json:"invocationIndex"`
  Target          *invocation.InvocationSpec   `json:"target"`
  Status          *invocation.InvocationStatus `json:"status"`
  Results         *TargetResults               `json:"results"`
  Finished        bool                         `json:"finished"`
  lock            sync.RWMutex
}

type InvocationsResults struct {
  Results map[uint32]*InvocationResults
  lock    sync.RWMutex
}

var (
  targetsResults               *TargetsResults         = &TargetsResults{}
  invocationsResults           *InvocationsResults     = &InvocationsResults{}
  chanSendTargetsToRegistry    chan *TargetResults     = make(chan *TargetResults, 10)
  chanSendInvocationToRegistry chan *InvocationResults = make(chan *InvocationResults, 10)
  chanLockInvocationInRegistry chan uint32             = make(chan uint32, 10)
  stopRegistrySender           chan bool               = make(chan bool, 1)
  sendingToRegistry            bool
  registryClient               *http.Client
  registrySendLock             sync.Mutex
  collectTargetsResults        bool = true
  collectAllTargetsResults     bool = false
  collectInvocationResults     bool = false
)

func (tr *TargetResults) init(reset bool) {
  tr.lock.Lock()
  defer tr.lock.Unlock()
  if tr.CountsByStatus == nil || reset {
    tr.InvocationCounts = 0
    tr.CountsByStatus = map[string]int{}
    tr.CountsByStatusCodes = map[int]int{}
    tr.CountsByHeaders = map[string]int{}
    tr.CountsByHeaderValues = map[string]map[string]int{}
    tr.CountsByURIs = map[string]int{}
  }
}

func (tr *TargetResults) Init() {
  tr.init(true)
}

func (tr *TargetResults) AddResult(result *invocation.InvocationResult, trackingHeaders []string) {
  tr.init(false)
  tr.lock.Lock()
  defer tr.lock.Unlock()
  tr.InvocationCounts++
  now := time.Now()
  if tr.FirstResponse.IsZero() {
    tr.FirstResponse = now
  }
  tr.LastResponse = now
  tr.CountsByStatus[result.Status]++
  tr.CountsByStatusCodes[result.StatusCode]++

  for _, h := range trackingHeaders {
    for rh, values := range result.Headers {
      if strings.EqualFold(h, rh) {
        tr.CountsByHeaders[h]++
        if tr.CountsByHeaderValues[h] == nil {
          tr.CountsByHeaderValues[h] = map[string]int{}
        }
        for _, v := range values {
          tr.CountsByHeaderValues[h][v]++
        }
      }
    }
  }
  tr.CountsByURIs[strings.ToLower(result.URI)]++
}

func (tr *TargetsResults) init(reset bool) {
  tr.lock.Lock()
  defer tr.lock.Unlock()
  if reset || tr.Results == nil {
    tr.Results = map[string]*TargetResults{}
    tr.Results[""] = &TargetResults{}
    tr.Results[""].init(true)
  }
}

func (tr *TargetsResults) getTargetResults(target string) (*TargetResults, *TargetResults) {
  tr.init(false)
  tr.lock.Lock()
  if tr.Results[target] == nil {
    tr.Results[target] = &TargetResults{}
    tr.Results[target].init(true)
  }
  targetResults := tr.Results[target]
  allResults := tr.Results[""]
  tr.lock.Unlock()
  return targetResults, allResults
}

func (tr *TargetsResults) AddResult(result *invocation.InvocationResult, trackingHeaders []string) {
  targetResults, allResults := tr.getTargetResults(result.TargetName)
  targetResults.AddResult(result, trackingHeaders)
  if collectAllTargetsResults {
    allResults.AddResult(result, trackingHeaders)
  }
}

func (ir *InvocationResults) init(reset bool) {
  ir.lock.Lock()
  defer ir.lock.Unlock()
  if reset || ir.Results == nil {
    ir.Finished = false
    ir.InvocationIndex = 0
    ir.Results = &TargetResults{}
    ir.Results.init(true)
  }
}

func (ir *InvocationResults) addResult(result *invocation.InvocationResult, trackingHeaders []string) {
  ir.init(false)
  ir.lock.RLock()
  targetResults := ir.Results
  ir.lock.RUnlock()
  targetResults.AddResult(result, trackingHeaders)
}

func (ir *InvocationResults) finish() {
  ir.lock.Lock()
  ir.Finished = true
  ir.lock.Unlock()
}

func (ir *InvocationsResults) init(reset bool) {
  ir.lock.Lock()
  defer ir.lock.Unlock()
  if reset || ir.Results == nil {
    ir.Results = map[uint32]*InvocationResults{}
  }
}

func (ir *InvocationsResults) getInvocation(index uint32) *InvocationResults {
  ir.init(false)
  ir.lock.RLock()
  invocationResults := ir.Results[index]
  ir.lock.RUnlock()
  if invocationResults == nil {
    invocationResults = &InvocationResults{}
    invocationResults.init(true)
    invocationResults.InvocationIndex = index
    ir.lock.Lock()
    ir.Results[index] = invocationResults
    ir.lock.Unlock()
  }
  return invocationResults
}

func resultSink(invocationIndex uint32, result *invocation.InvocationResult, trackingHeaders []string,
  invocationResults *InvocationResults, targetResults *TargetResults, allResults *TargetResults) {
  if result != nil {
    if collectInvocationResults {
      invocationResults.addResult(result, trackingHeaders)
      chanSendInvocationToRegistry <- invocationResults
    }
    if collectTargetsResults {
      targetResults.AddResult(result, trackingHeaders)
      chanSendTargetsToRegistry <- targetResults
    }
    if collectAllTargetsResults {
      allResults.AddResult(result, trackingHeaders)
      chanSendTargetsToRegistry <- allResults
    }
  }
}

func channelSink(invocationIndex uint32, resultChannel chan *invocation.InvocationResult, doneChannel chan bool,
  trackingHeaders []string, invocationResults *InvocationResults,
  targetResults *TargetResults, allResults *TargetResults) {
  done := false
Results:
  for {
    select {
    case done = <-doneChannel:
      break Results
    case result := <-resultChannel:
      resultSink(invocationIndex, result, trackingHeaders, invocationResults, targetResults, allResults)
    }
  }
  if done {
  MoreResults:
    for {
      select {
      case result := <-resultChannel:
        if result != nil {
          resultSink(invocationIndex, result, trackingHeaders, invocationResults, targetResults, allResults)
        } else {
          break MoreResults
        }
      default:
        break MoreResults
      }
    }
  }
  invocationResults.finish()
  chanLockInvocationInRegistry <- invocationIndex
}

func ResultChannelSinkFactory(target *invocation.InvocationSpec, trackingHeaders []string) invocation.ResultSinkFactory {
  targetResults, allResults := targetsResults.getTargetResults(target.Name)
  targetResults.Target = target.Name
  return func(tracker *invocation.InvocationTracker) invocation.ResultSink {
    invocationResults := invocationsResults.getInvocation(tracker.ID)
    invocationResults.Target = target
    invocationResults.Status = tracker.Status
    startRegistrySender()
    go channelSink(tracker.ID, tracker.ResultChannel, tracker.DoneChannel, trackingHeaders, invocationResults, targetResults, allResults)
    return nil
  }
}

func ResultSinkFactory(target *invocation.InvocationSpec, trackingHeaders []string) invocation.ResultSinkFactory {
  targetResults, allResults := targetsResults.getTargetResults(target.Name)
  targetResults.Target = target.Name
  return func(tracker *invocation.InvocationTracker) invocation.ResultSink {
    invocationResults := invocationsResults.getInvocation(tracker.ID)
    invocationResults.Target = target
    invocationResults.Status = tracker.Status
    startRegistrySender()
    return func(result *invocation.InvocationResult) {
      resultSink(tracker.ID, result, trackingHeaders, invocationResults, targetResults, allResults)
    }
  }
}

func ClearResults() {
  invocationsResults.init(true)
  targetsResults.init(true)
}

func GetTargetsResultsJSON() string {
  targetsResults.lock.RLock()
  defer targetsResults.lock.RUnlock()
  return util.ToJSON(targetsResults)
}

func GetInvocationResultsJSON() string {
  invocationsResults.lock.RLock()
  defer invocationsResults.lock.RUnlock()
  return util.ToJSON(invocationsResults)
}

func AddDeltaResults(results, delta *TargetResults) {
  if results.FirstResponse.IsZero() || results.FirstResponse.After(delta.FirstResponse) {
    results.FirstResponse = delta.FirstResponse
  }
  if results.LastResponse.IsZero() || results.LastResponse.Before(delta.LastResponse) {
    results.LastResponse = delta.LastResponse
  }
  results.InvocationCounts += delta.InvocationCounts
  for k, v := range delta.CountsByStatus {
    results.CountsByStatus[k] += v
  }
  for k, v := range delta.CountsByStatusCodes {
    results.CountsByStatusCodes[k] += v
  }
  for k, v := range delta.CountsByHeaders {
    results.CountsByHeaders[k] += v
  }
  for h, values := range delta.CountsByHeaderValues {
    if results.CountsByHeaderValues[h] == nil {
      results.CountsByHeaderValues[h] = map[string]int{}
    }
    for hv, v := range values {
      results.CountsByHeaderValues[h][hv] += v
    }
  }
  for k, v := range delta.CountsByURIs {
    results.CountsByURIs[k] += v
  }
}

func (tsr *TargetsSummaryResults) Init() {
  tsr.TargetInvocationCounts = map[string]int{}
  tsr.TargetFirstResponses = map[string]time.Time{}
  tsr.TargetLastResponses = map[string]time.Time{}
  tsr.CountsByStatusCodes = map[int]int{}
  tsr.CountsByHeaders = map[string]int{}
  tsr.CountsByHeaderValues = map[string]map[string]int{}
  tsr.CountsByTargetStatusCodes = map[string]map[int]int{}
  tsr.CountsByTargetHeaders = map[string]map[string]int{}
  tsr.CountsByTargetHeaderValues = map[string]map[string]map[string]int{}
}

func (tsr *TargetsSummaryResults) AddTargetResult(tr *TargetResults) {
  tsr.TargetInvocationCounts[tr.Target] += tr.InvocationCounts
  if tsr.TargetFirstResponses[tr.Target].IsZero() || tsr.TargetFirstResponses[tr.Target].After(tr.FirstResponse) {
    tsr.TargetFirstResponses[tr.Target] = tr.FirstResponse
  }
  if tsr.TargetLastResponses[tr.Target].IsZero() || tsr.TargetLastResponses[tr.Target].Before(tr.LastResponse) {
    tsr.TargetLastResponses[tr.Target] = tr.LastResponse
  }
  if tsr.CountsByTargetStatusCodes[tr.Target] == nil {
    tsr.CountsByTargetStatusCodes[tr.Target] = map[int]int{}
  }
  for k, v := range tr.CountsByStatusCodes {
    tsr.CountsByStatusCodes[k] += v
    tsr.CountsByTargetStatusCodes[tr.Target][k] += v
  }
  if tsr.CountsByTargetHeaders[tr.Target] == nil {
    tsr.CountsByTargetHeaders[tr.Target] = map[string]int{}
  }
  for k, v := range tr.CountsByHeaders {
    tsr.CountsByHeaders[k] += v
    tsr.CountsByTargetHeaders[tr.Target][k] += v
  }
  if tsr.CountsByTargetHeaderValues[tr.Target] == nil {
    tsr.CountsByTargetHeaderValues[tr.Target] = map[string]map[string]int{}
  }
  for h, values := range tr.CountsByHeaderValues {
    if tsr.CountsByHeaderValues[h] == nil {
      tsr.CountsByHeaderValues[h] = map[string]int{}
    }
    if tsr.CountsByTargetHeaderValues[tr.Target][h] == nil {
      tsr.CountsByTargetHeaderValues[tr.Target][h] = map[string]int{}
    }
      for hv, v := range values {
      tsr.CountsByHeaderValues[h][hv] += v
      tsr.CountsByTargetHeaderValues[tr.Target][h][hv] += v
    }
  }
}

func initRegistryHttpClient() {
  tr := &http.Transport{
    MaxIdleConns: 1,
    Proxy:        http.ProxyFromEnvironment,
    DialContext: (&net.Dialer{
      Timeout:   10 * time.Second,
      KeepAlive: time.Minute,
    }).DialContext,
    TLSHandshakeTimeout: 10 * time.Second,
  }
  registryClient = &http.Client{Transport: tr}
}

func lockInvocationRegistryLocker(invocationIndex uint32) {
  if global.UseLocker && global.RegistryURL != "" {
    url := fmt.Sprintf("%s/registry/peers/%s/%s/locker/lock/%s_%d", global.RegistryURL,
      global.PeerName, global.PeerAddress, constants.LockerClientKey, invocationIndex)
    if resp, err := registryClient.Post(url, "application/json", nil); err == nil {
      util.CloseResponse(resp)
    }
  }
}

func storeInvocationResultsInRegistryLocker(keys []string, data interface{}) {
  if global.UseLocker && global.RegistryURL != "" {
    url := fmt.Sprintf("%s/registry/peers/%s/%s/locker/store/%s", global.RegistryURL, global.PeerName, global.PeerAddress, strings.Join(keys, ","))
    if resp, err := registryClient.Post(url, "application/json",
      strings.NewReader(util.ToJSON(data))); err == nil {
      util.CloseResponse(resp)
    }
  }
}

func registrySender() {
  stopSender := false
  for {
  RegistrySend:
    for {
      select {
      case targetResults := <-chanSendTargetsToRegistry:
        targetResults.lock.RLock()
        storeInvocationResultsInRegistryLocker([]string{constants.LockerClientKey, targetResults.Target}, targetResults)
        targetResults.lock.RUnlock()
      case invocationResults := <-chanSendInvocationToRegistry:
        invocationResults.lock.RLock()
        storeInvocationResultsInRegistryLocker([]string{constants.LockerClientKey, constants.LockerInvocationsKey,
          fmt.Sprint(invocationResults.InvocationIndex)}, invocationResults)
        invocationResults.lock.RUnlock()
      case invocationIndex := <-chanLockInvocationInRegistry:
        lockInvocationRegistryLocker(invocationIndex)
      case stopSender = <-stopRegistrySender:
        break RegistrySend
      }
    }
    if stopSender {
      break
    }
  }
}

func startRegistrySender() {
  registrySendLock.Lock()
  defer registrySendLock.Unlock()
  if !sendingToRegistry {
    initRegistryHttpClient()
    sendingToRegistry = true
    go registrySender()
  }
}

func StopRegistrySender() {
  stopRegistrySender <- true
}

func EnableAllTargetResults(enable bool) {
  collectAllTargetsResults = enable
}

func EnableInvocationResults(enable bool) {
  collectInvocationResults = enable
}
