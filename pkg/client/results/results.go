package results

import (
  "fmt"
  "goto/pkg/constants"
  "goto/pkg/global"
  "goto/pkg/invocation"
  "goto/pkg/util"
  "log"
  "strings"
  "sync"
  "time"
)

type CountInfo struct {
  Value         int       `json:"value"`
  Retries       int       `json:"retries"`
  FirstResponse time.Time `json:"firstResponse"`
  LastResponse  time.Time `json:"lastResponse"`
}

type HeaderCounts struct {
  Header                    string                              `json:"header"`
  Count                     *CountInfo                          `json:"count"`
  CountsByValues            map[string]*CountInfo               `json:"countsByValues"`
  CountsByStatusCodes       map[int]*CountInfo                  `json:"countsByStatusCodes"`
  CountsByValuesStatusCodes map[string]map[int]*CountInfo       `json:"countsByValuesStatusCodes"`
  CrossHeaders              map[string]*HeaderCounts            `json:"crossHeaders"`
  CrossHeadersByValues      map[string]map[string]*HeaderCounts `json:"crossHeadersByValues"`
  FirstResponse             time.Time                           `json:"firstResponse"`
  LastResponse              time.Time                           `json:"lastResponse"`
}

type TargetResults struct {
  Target                  string                   `json:"target"`
  InvocationCounts        int                      `json:"invocationCounts"`
  FirstResponse           time.Time                `json:"firstResponse"`
  LastResponse            time.Time                `json:"lastResponse"`
  RetriedInvocationCounts int                      `json:"retriedInvocationCounts"`
  CountsByStatus          map[string]int           `json:"countsByStatus"`
  CountsByStatusCodes     map[int]int              `json:"countsByStatusCodes"`
  CountsByHeaders         map[string]*HeaderCounts `json:"countsByHeaders"`
  CountsByURIs            map[string]int           `json:"countsByURIs"`
  trackingHeaders         []string
  crossTrackingHeaders    map[string][]string
  crossHeadersMap         map[string]string
  pendingRegistrySend     bool
  lock                    *sync.RWMutex
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
  HeaderCounts               map[string]*HeaderCounts             `json:"headerCounts"`
  TargetHeaderCounts         map[string]map[string]*HeaderCounts  `json:"targetHeaderCounts"`
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

const (
  SenderCount  = 10
  SendDelayMin = 3
  SendDelayMax = 5
)

var (
  targetsResults               = &TargetsResults{}
  invocationsResults           = &InvocationsResults{}
  chanSendTargetsToRegistry    = make(chan *TargetResults, 200)
  chanSendInvocationToRegistry = make(chan *InvocationResults, 200)
  stopRegistrySender           = make(chan bool, 10)
  registryClient               = util.CreateHttpClient()
  sendingToRegistry            bool
  registrySendLock             sync.Mutex
  collectTargetsResults        bool = true
  collectAllTargetsResults     bool = false
  collectInvocationResults     bool = false
)

func (tr *TargetResults) unsafeInit(reset bool) {
  if tr.CountsByStatus == nil || reset {
    tr.InvocationCounts = 0
    tr.CountsByStatus = map[string]int{}
    tr.CountsByStatusCodes = map[int]int{}
    tr.CountsByHeaders = map[string]*HeaderCounts{}
    tr.CountsByURIs = map[string]int{}
  }
}

func (tr *TargetResults) Init(trackingHeaders []string, crossTrackingHeaders map[string][]string) {
  tr.unsafeInit(true)
  tr.trackingHeaders = trackingHeaders
  tr.crossTrackingHeaders = crossTrackingHeaders
  tr.crossHeadersMap = util.BuildCrossHeadersMap(crossTrackingHeaders)
}

func newHeaderCounts(header string) *HeaderCounts {
  return &HeaderCounts{
    Header:                    header,
    Count:                     &CountInfo{},
    CountsByValues:            map[string]*CountInfo{},
    CountsByStatusCodes:       map[int]*CountInfo{},
    CountsByValuesStatusCodes: map[string]map[int]*CountInfo{},
    CrossHeaders:              map[string]*HeaderCounts{},
    CrossHeadersByValues:      map[string]map[string]*HeaderCounts{},
  }
}

func setTimestamps(h *HeaderCounts) {
  now := time.Now()
  if h.FirstResponse.IsZero() {
    h.FirstResponse = now
  }
  h.LastResponse = now
}

func incrementHeaderCount(h *CountInfo, retries int, by ...*CountInfo) {
  if len(by) > 0 {
    h.Value += by[0].Value
    h.Retries += by[0].Retries
    if h.FirstResponse.IsZero() || h.FirstResponse.After(by[0].FirstResponse) {
      h.FirstResponse = by[0].FirstResponse
    }
    if h.LastResponse.IsZero() || h.LastResponse.Before(by[0].LastResponse) {
      h.LastResponse = by[0].LastResponse
    }
  } else {
    h.Value++
    h.Retries += retries
    now := time.Now()
    if h.FirstResponse.IsZero() {
      h.FirstResponse = now
    }
    h.LastResponse = now
  }
}

func incrementHeaderCountForStatus(m map[int]*CountInfo, statusCode int, retries int, by ...*CountInfo) {
  if m[statusCode] == nil {
    m[statusCode] = &CountInfo{}
  }
  incrementHeaderCount(m[statusCode], retries, by...)
}

func incrementHeaderCountForValue(m map[string]*CountInfo, value string, retries int, by ...*CountInfo) {
  if m[value] == nil {
    m[value] = &CountInfo{}
  }
  incrementHeaderCount(m[value], retries, by...)
}

func (tr *TargetResults) processCrossHeadersForHeader(header string, values []string, statusCode int, retries int, allHeaders map[string][]string) {
  if crossHeaders := tr.crossTrackingHeaders[header]; crossHeaders != nil {
    processSubCrossHeadersForHeader(header, values, statusCode, retries, tr.CountsByHeaders[header], crossHeaders, allHeaders)
  }
}

func processSubCrossHeadersForHeader(header string, values []string, statusCode int, retries int,
  headerCounts *HeaderCounts, crossHeaders []string, allHeaders map[string][]string) {
  for _, value := range values {
    if headerCounts.CrossHeadersByValues[value] == nil {
      headerCounts.CrossHeadersByValues[value] = map[string]*HeaderCounts{}
    }
  }
  for i, crossHeader := range crossHeaders {
    crossValues := allHeaders[crossHeader]
    if crossValues == nil {
      continue
    }
    if headerCounts.CrossHeaders[crossHeader] == nil {
      headerCounts.CrossHeaders[crossHeader] = newHeaderCounts(crossHeader)
    }
    crossHeaderCounts := headerCounts.CrossHeaders[crossHeader]
    setTimestamps(crossHeaderCounts)
    incrementHeaderCount(crossHeaderCounts.Count, retries)
    incrementHeaderCountForStatus(crossHeaderCounts.CountsByStatusCodes, statusCode, retries)
    for _, crossValue := range crossValues {
      incrementHeaderCountForValue(crossHeaderCounts.CountsByValues, crossValue, retries)
      if crossHeaderCounts.CountsByValuesStatusCodes[crossValue] == nil {
        crossHeaderCounts.CountsByValuesStatusCodes[crossValue] = map[int]*CountInfo{}
      }
      incrementHeaderCountForStatus(crossHeaderCounts.CountsByValuesStatusCodes[crossValue], statusCode, retries)
    }
    processSubCrossHeaders := i < len(crossHeaders)-1
    subCrossHeaders := crossHeaders[i+1:]
    if processSubCrossHeaders {
      processSubCrossHeadersForHeader(crossHeader, crossValues, statusCode, retries, crossHeaderCounts, subCrossHeaders, allHeaders)
    }
    for _, value := range values {
      if headerCounts.CrossHeadersByValues[value][crossHeader] == nil {
        headerCounts.CrossHeadersByValues[value][crossHeader] = newHeaderCounts(crossHeader)
      }
      crossHeaderCountsByValue := headerCounts.CrossHeadersByValues[value][crossHeader]
      setTimestamps(crossHeaderCountsByValue)
      incrementHeaderCount(crossHeaderCountsByValue.Count, retries)
      incrementHeaderCountForStatus(crossHeaderCountsByValue.CountsByStatusCodes, statusCode, retries)
      for _, crossValue := range crossValues {
        incrementHeaderCountForValue(crossHeaderCountsByValue.CountsByValues, crossValue, retries)
        if crossHeaderCountsByValue.CountsByValuesStatusCodes[crossValue] == nil {
          crossHeaderCountsByValue.CountsByValuesStatusCodes[crossValue] = map[int]*CountInfo{}
        }
        incrementHeaderCountForStatus(crossHeaderCountsByValue.CountsByValuesStatusCodes[crossValue], statusCode, retries)
      }
      if processSubCrossHeaders {
        processSubCrossHeadersForHeader(crossHeader, crossValues, statusCode, retries, crossHeaderCountsByValue, subCrossHeaders, allHeaders)
      }
    }
  }
}

func (tr *TargetResults) addHeaderResult(header string, values []string, statusCode int, retries int) {
  if tr.CountsByHeaders[header] == nil {
    tr.CountsByHeaders[header] = newHeaderCounts(header)
  }
  headerCounts := tr.CountsByHeaders[header]
  setTimestamps(headerCounts)
  incrementHeaderCount(headerCounts.Count, retries)
  incrementHeaderCountForStatus(headerCounts.CountsByStatusCodes, statusCode, retries)
  for _, value := range values {
    incrementHeaderCountForValue(headerCounts.CountsByValues, value, retries)
    if headerCounts.CountsByValuesStatusCodes[value] == nil {
      headerCounts.CountsByValuesStatusCodes[value] = map[int]*CountInfo{}
    }
    incrementHeaderCountForStatus(headerCounts.CountsByValuesStatusCodes[value], statusCode, retries)
  }
}

func (tr *TargetResults) AddResult(result *invocation.InvocationResult) {
  tr.lock.Lock()
  defer tr.lock.Unlock()
  tr.unsafeInit(false)
  tr.InvocationCounts++
  now := time.Now()
  if tr.FirstResponse.IsZero() {
    tr.FirstResponse = now
  }
  tr.LastResponse = now
  if result.Retries > 0 {
    tr.RetriedInvocationCounts++
  }
  tr.CountsByStatus[result.Status]++
  tr.CountsByStatusCodes[result.StatusCode]++

  for _, h := range tr.trackingHeaders {
    for rh, values := range result.Headers {
      if strings.EqualFold(h, rh) {
        tr.addHeaderResult(h, values, result.StatusCode, result.Retries)
        tr.processCrossHeadersForHeader(h, values, result.StatusCode, result.Retries, result.Headers)
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
    tr.Results[""] = &TargetResults{lock: &tr.lock}
    tr.Results[""].unsafeInit(true)
  }
}

func (tr *TargetsResults) getTargetResults(target string) (*TargetResults, *TargetResults) {
  tr.init(false)
  tr.lock.Lock()
  if tr.Results[target] == nil {
    tr.Results[target] = &TargetResults{Target: target, lock: &tr.lock}
    tr.Results[target].unsafeInit(true)
  }
  targetResults := tr.Results[target]
  allResults := tr.Results[""]
  tr.lock.Unlock()
  return targetResults, allResults
}

func (tr *TargetsResults) AddResult(result *invocation.InvocationResult,
  trackingHeaders []string, crossTrackingHeaders map[string][]string) {
  targetResults, allResults := tr.getTargetResults(result.TargetName)
  targetResults.AddResult(result)
  if collectAllTargetsResults {
    allResults.AddResult(result)
  }
}

func (ir *InvocationResults) unsafeInit(reset bool) {
  if reset || ir.Results == nil {
    ir.Finished = false
    ir.InvocationIndex = 0
    ir.Results = &TargetResults{}
    ir.Results.unsafeInit(true)
  }
}

func (ir *InvocationResults) addResult(result *invocation.InvocationResult,
  trackingHeaders []string, crossTrackingHeaders map[string][]string) {
  ir.lock.RLock()
  ir.unsafeInit(false)
  targetResults := ir.Results
  ir.lock.RUnlock()
  targetResults.AddResult(result)
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
    invocationResults.unsafeInit(true)
    invocationResults.InvocationIndex = index
    ir.lock.Lock()
    ir.Results[index] = invocationResults
    ir.lock.Unlock()
  }
  return invocationResults
}

func resultSink(invocationIndex uint32, result *invocation.InvocationResult,
  invocationResults *InvocationResults, targetResults *TargetResults, allResults *TargetResults) {
  if result != nil {
    result.Headers = util.ToLowerHeaders(result.Headers)
    if collectInvocationResults {
      invocationResults.addResult(result, targetResults.trackingHeaders, targetResults.crossTrackingHeaders)
      chanSendInvocationToRegistry <- invocationResults
    }
    if collectTargetsResults {
      targetResults.AddResult(result)
      chanSendTargetsToRegistry <- targetResults
    }
    if collectAllTargetsResults {
      allResults.AddResult(result)
      chanSendTargetsToRegistry <- allResults
    }
  }
}

func channelSink(invocationIndex uint32, resultChannel chan *invocation.InvocationResult, doneChannel chan bool,
  invocationResults *InvocationResults, targetResults *TargetResults, allResults *TargetResults) {
  done := false
Results:
  for {
    select {
    case done = <-doneChannel:
      break Results
    case result := <-resultChannel:
      resultSink(invocationIndex, result, invocationResults, targetResults, allResults)
    }
  }
  if done {
  MoreResults:
    for {
      select {
      case result := <-resultChannel:
        if result != nil {
          resultSink(invocationIndex, result, invocationResults, targetResults, allResults)
        } else {
          break MoreResults
        }
      default:
        break MoreResults
      }
    }
  }
  invocationResults.finish()
}

func processTrackingHeaders(targetResults *TargetResults, allResults *TargetResults, trackingHeaders []string, crossTrackingHeaders map[string][]string) {
  targetResults.trackingHeaders = trackingHeaders
  targetResults.crossTrackingHeaders = crossTrackingHeaders
  targetResults.crossHeadersMap = util.BuildCrossHeadersMap(crossTrackingHeaders)
  if allResults != nil {
    allResults.trackingHeaders = targetResults.trackingHeaders
    allResults.crossTrackingHeaders = targetResults.crossTrackingHeaders
    allResults.crossHeadersMap = targetResults.crossHeadersMap
  }
}

func ResultChannelSinkFactory(target *invocation.InvocationSpec, trackingHeaders []string, crossTrackingHeaders map[string][]string) invocation.ResultSinkFactory {
  targetResults, allResults := targetsResults.getTargetResults(target.Name)
  processTrackingHeaders(targetResults, allResults, trackingHeaders, crossTrackingHeaders)
  return func(tracker *invocation.InvocationTracker) invocation.ResultSink {
    invocationResults := invocationsResults.getInvocation(tracker.ID)
    invocationResults.Target = target
    invocationResults.Status = tracker.Status
    startRegistrySender()
    go channelSink(tracker.ID, tracker.ResultChannel, tracker.DoneChannel, invocationResults, targetResults, allResults)
    return nil
  }
}

func ResultSinkFactory(target *invocation.InvocationSpec, trackingHeaders []string, crossTrackingHeaders map[string][]string) invocation.ResultSinkFactory {
  targetResults, allResults := targetsResults.getTargetResults(target.Name)
  processTrackingHeaders(targetResults, allResults, trackingHeaders, crossTrackingHeaders)
  return func(tracker *invocation.InvocationTracker) invocation.ResultSink {
    invocationResults := invocationsResults.getInvocation(tracker.ID)
    invocationResults.Target = target
    invocationResults.Status = tracker.Status
    startRegistrySender()
    return func(result *invocation.InvocationResult) {
      resultSink(tracker.ID, result, invocationResults, targetResults, allResults)
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
  if targetsResults.Results != nil {
    return util.ToJSON(targetsResults.Results)
  }
  return "{}"
}

func GetInvocationResultsJSON() string {
  invocationsResults.lock.RLock()
  defer invocationsResults.lock.RUnlock()
  return util.ToJSON(invocationsResults.Results)
}

func addDeltaCrossHeaders(result, delta map[string]*HeaderCounts, crossTrackingHeaders map[string][]string, crossHeadersMap map[string]string) {
  for ch, chCounts := range delta {
    if result[ch] == nil {
      result[ch] = newHeaderCounts(ch)
    }
    addDeltaHeaderCounts(result[ch], chCounts, crossTrackingHeaders, crossHeadersMap)
  }
}

func addDeltaCrossHeadersValues(result, delta map[string]map[string]*HeaderCounts, crossTrackingHeaders map[string][]string, crossHeadersMap map[string]string) {
  for value, chCounts := range delta {
    if result[value] == nil {
      result[value] = map[string]*HeaderCounts{}
    }
    for ch, chCounts := range chCounts {
      if result[value][ch] == nil {
        result[value][ch] = newHeaderCounts(ch)
      }
      addDeltaHeaderCounts(result[value][ch], chCounts, crossTrackingHeaders, crossHeadersMap)
    }
  }
}

func addDeltaHeaderCounts(result, delta *HeaderCounts, crossTrackingHeaders map[string][]string, crossHeadersMap map[string]string) {
  setTimestamps(result)
  incrementHeaderCount(result.Count, 0, delta.Count)
  if result.CountsByValues == nil {
    result.CountsByValues = map[string]*CountInfo{}
  }
  for value, count := range delta.CountsByValues {
    incrementHeaderCountForValue(result.CountsByValues, value, 0, count)
  }
  if result.CountsByStatusCodes == nil {
    result.CountsByStatusCodes = map[int]*CountInfo{}
  }
  for statusCode, count := range delta.CountsByStatusCodes {
    incrementHeaderCountForStatus(result.CountsByStatusCodes, statusCode, 0, count)
  }
  for value, valueCounts := range delta.CountsByValuesStatusCodes {
    if result.CountsByValuesStatusCodes[value] == nil {
      result.CountsByValuesStatusCodes[value] = map[int]*CountInfo{}
    }
    for statusCode, count := range valueCounts {
      incrementHeaderCountForStatus(result.CountsByValuesStatusCodes[value], statusCode, 0, count)
    }
  }
  if crossTrackingHeaders[result.Header] != nil || crossHeadersMap[result.Header] != "" {
    if delta.CrossHeaders != nil {
      if result.CrossHeaders == nil {
        result.CrossHeaders = map[string]*HeaderCounts{}
      }
      addDeltaCrossHeaders(result.CrossHeaders, delta.CrossHeaders, crossTrackingHeaders, crossHeadersMap)
    }
    if delta.CrossHeadersByValues != nil {
      if result.CrossHeadersByValues == nil {
        result.CrossHeadersByValues = map[string]map[string]*HeaderCounts{}
      }
      addDeltaCrossHeadersValues(result.CrossHeadersByValues, delta.CrossHeadersByValues, crossTrackingHeaders, crossHeadersMap)
    }
  }
}

func processDeltaHeaderCounts(result, delta map[string]*HeaderCounts, crossTrackingHeaders map[string][]string, crossHeadersMap map[string]string) {
  for header, counts := range delta {
    if result[header] == nil {
      result[header] = newHeaderCounts(header)
    }
    addDeltaHeaderCounts(result[header], counts, crossTrackingHeaders, crossHeadersMap)
  }
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
  if delta.CountsByHeaders != nil {
    if results.CountsByHeaders == nil {
      results.CountsByHeaders = map[string]*HeaderCounts{}
    }
    processDeltaHeaderCounts(results.CountsByHeaders, delta.CountsByHeaders, results.crossTrackingHeaders, results.crossHeadersMap)
  }
  for k, v := range delta.CountsByURIs {
    results.CountsByURIs[k] += v
  }
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
  if tsr.CountsByTargetHeaderValues[tr.Target] == nil {
    tsr.CountsByTargetHeaderValues[tr.Target] = map[string]map[string]int{}
  }
  if tr.CountsByHeaders != nil {
    for h, counts := range tr.CountsByHeaders {
      tsr.CountsByHeaders[h] += counts.Count.Value
      tsr.CountsByTargetHeaders[tr.Target][h] += counts.Count.Value
      if tsr.CountsByHeaderValues[h] == nil {
        tsr.CountsByHeaderValues[h] = map[string]int{}
      }
      if tsr.CountsByTargetHeaderValues[tr.Target][h] == nil {
        tsr.CountsByTargetHeaderValues[tr.Target][h] = map[string]int{}
      }
      for v, count := range counts.CountsByValues {
        tsr.CountsByHeaderValues[h][v] += count.Value
        tsr.CountsByTargetHeaderValues[tr.Target][h][v] += count.Value
      }
    }
    if tsr.TargetHeaderCounts[tr.Target] == nil {
      tsr.TargetHeaderCounts[tr.Target] = map[string]*HeaderCounts{}
    }
    processDeltaHeaderCounts(tsr.HeaderCounts, tr.CountsByHeaders, tr.crossTrackingHeaders, tr.crossHeadersMap)
    processDeltaHeaderCounts(tsr.TargetHeaderCounts[tr.Target], tr.CountsByHeaders, tr.crossTrackingHeaders, tr.crossHeadersMap)
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
  tsr.HeaderCounts = map[string]*HeaderCounts{}
  tsr.TargetHeaderCounts = map[string]map[string]*HeaderCounts{}
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

func sendResultToRegistry(keys []string, data interface{}) {
  if global.UseLocker && global.RegistryURL != "" {
    url := fmt.Sprintf("%s/registry/peers/%s/%s/locker/store/%s", global.RegistryURL, global.PeerName, global.PeerAddress, strings.Join(keys, ","))
    if resp, err := registryClient.Post(url, "application/json",
      strings.NewReader(util.ToJSON(data))); err == nil {
      util.CloseResponse(resp)
    }
  }
}

func registrySender(id int) {
RegistrySend:
  for {
    if len(chanSendTargetsToRegistry) > 50 {
      log.Printf("registrySender[%d]: chanSendTargetsToRegistry length %d\n", id, len(chanSendTargetsToRegistry))
    }
    if len(chanSendInvocationToRegistry) > 50 {
      log.Printf("registrySender[%d]: chanSendInvocationToRegistry length %d\n", id, len(chanSendInvocationToRegistry))
    }
    hasTargetsResults := false
    collectedTargetsResults := map[string]*TargetResults{}
    select {
    case targetResult := <-chanSendTargetsToRegistry:
      targetResult.lock.Lock()
      if !targetResult.pendingRegistrySend {
        hasTargetsResults = true
        targetResult.pendingRegistrySend = true
        collectedTargetsResults[targetResult.Target] = targetResult
      }
      targetResult.lock.Unlock()
    case invocationResults := <-chanSendInvocationToRegistry:
      invocationResults.lock.RLock()
      sendResultToRegistry([]string{constants.LockerClientKey, constants.LockerInvocationsKey,
        fmt.Sprint(invocationResults.InvocationIndex)}, invocationResults)
      invocationResults.lock.RUnlock()
    case <-stopRegistrySender:
      break RegistrySend
    }
    if hasTargetsResults {
      hasMoreResults := false
      for i := 0; i < SendDelayMax; i++ {
      MoreResults:
        for {
          select {
          case targetResult := <-chanSendTargetsToRegistry:
            targetResult.lock.Lock()
            if !targetResult.pendingRegistrySend || collectedTargetsResults[targetResult.Target] != nil {
              targetResult.pendingRegistrySend = true
              collectedTargetsResults[targetResult.Target] = targetResult
              hasMoreResults = true
            }
            targetResult.lock.Unlock()
          default:
            break MoreResults
          }
        }
        if i < SendDelayMin-1 || (hasMoreResults && i < SendDelayMax-1) {
          time.Sleep(time.Second)
        }
      }
      for target, targetResult := range collectedTargetsResults {
        targetResult.lock.Lock()
        sendResultToRegistry([]string{constants.LockerClientKey, target}, targetResult)
        targetResult.pendingRegistrySend = false
        targetResult.lock.Unlock()
      }
    }
  }
}

func startRegistrySender() {
  registrySendLock.Lock()
  defer registrySendLock.Unlock()
  if !sendingToRegistry {
    sendingToRegistry = true
    for i := 1; i < SenderCount; i++ {
      go registrySender(i)
    }
  }
}

func StopRegistrySender() {
  for i := 1; i < SenderCount; i++ {
    stopRegistrySender <- true
  }
}

func EnableAllTargetResults(enable bool) {
  collectAllTargetsResults = enable
}

func EnableInvocationResults(enable bool) {
  collectInvocationResults = enable
}
