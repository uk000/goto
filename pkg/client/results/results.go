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
  Count         int       `json:"count"`
  Retries       int       `json:"retries"`
  FirstResponse time.Time `json:"firstResponse"`
  LastResponse  time.Time `json:"lastResponse"`
}

type HeaderCounts struct {
  CountInfo
  Header                    string                              `json:"header"`
  CountsByValues            map[string]*CountInfo               `json:"countsByValues"`
  CountsByStatusCodes       map[int]*CountInfo                  `json:"countsByStatusCodes"`
  CountsByValuesStatusCodes map[string]map[int]*CountInfo       `json:"countsByValuesStatusCodes"`
  CrossHeaders              map[string]*HeaderCounts            `json:"crossHeaders"`
  CrossHeadersByValues      map[string]map[string]*HeaderCounts `json:"crossHeadersByValues"`
  FirstResponse             time.Time                           `json:"firstResponse"`
  LastResponse              time.Time                           `json:"lastResponse"`
}

type KeyResultCounts struct {
  CountInfo
  CountsByStatusCodes map[int]*KeyResultCounts    `json:"countsByStatusCodes,omitempty"`
  CountsByTimeBuckets map[string]*KeyResultCounts `json:"countsByTimeBuckets,omitempty"`
}

type TargetResults struct {
  Target                  string                      `json:"target"`
  InvocationCounts        int                         `json:"invocationCounts"`
  FirstResponse           time.Time                   `json:"firstResponse"`
  LastResponse            time.Time                   `json:"lastResponse"`
  RetriedInvocationCounts int                         `json:"retriedInvocationCounts"`
  CountsByStatus          map[string]int              `json:"countsByStatus"`
  CountsByStatusCodes     map[int]*KeyResultCounts    `json:"countsByStatusCodes"`
  CountsByHeaders         map[string]*HeaderCounts    `json:"countsByHeaders"`
  CountsByURIs            map[string]*KeyResultCounts `json:"countsByURIs"`
  CountsByTimeBuckets     map[string]*KeyResultCounts `json:"countsByTimeBuckets"`
  trackingHeaders         []string
  crossTrackingHeaders    map[string][]string
  crossHeadersMap         map[string]string
  trackingTimeBuckets     [][]int
  pendingRegistrySend     bool
  lock                    sync.RWMutex
}

type TargetsSummaryResults struct {
  TargetInvocationCounts              map[string]int                       `json:"targetInvocationCounts"`
  TargetFirstResponses                map[string]time.Time                 `json:"targetFirstResponses"`
  TargetLastResponses                 map[string]time.Time                 `json:"targetLastResponses"`
  CountsByStatusCodes                 map[int]int                          `json:"countsByStatusCodes"`
  CountsByStatusCodeTimeBuckets       map[int]map[string]int               `json:"countsByStatusCodeTimeBuckets"`
  CountsByHeaders                     map[string]int                       `json:"countsByHeaders"`
  CountsByURIs                        map[string]int                       `json:"countsByURIs"`
  CountsByURIStatusCodes              map[string]map[int]int               `json:"countsByURIStatusCodes"`
  CountsByURITimeBuckets              map[string]map[string]int            `json:"countsByURITimeBuckets"`
  CountsByTimeBuckets                 map[string]int                       `json:"countsByTimeBuckets"`
  CountsByTimeBucketStatusCodes       map[string]map[int]int               `json:"countsByTimeBucketStatusCodes"`
  CountsByHeaderValues                map[string]map[string]int            `json:"countsByHeaderValues"`
  CountsByTargetStatusCodes           map[string]map[int]int               `json:"countsByTargetStatusCodes"`
  CountsByTargetStatusCodeTimeBuckets map[string]map[int]map[string]int    `json:"countsByTargetStatusCodeTimeBuckets"`
  CountsByTargetHeaders               map[string]map[string]int            `json:"countsByTargetHeaders"`
  CountsByTargetHeaderValues          map[string]map[string]map[string]int `json:"countsByTargetHeaderValues"`
  CountsByTargetURIs                  map[string]map[string]int            `json:"countsByTargetURIs"`
  CountsByTargetURIStatusCodes        map[string]map[string]map[int]int    `json:"countsByTargetURIStatusCodes"`
  CountsByTargetURITimeBuckets        map[string]map[string]map[string]int `json:"countsByTargetURITimeBuckets"`
  CountsByTargetTimeBuckets           map[string]map[string]int            `json:"countsByTargetTimeBuckets"`
  CountsByTargetTimeBucketStatusCodes map[string]map[string]map[int]int    `json:"countsByTargetTimeBucketStatusCodes"`
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

func (tr *TargetResults) init(reset bool) {
  tr.lock.Lock()
  defer tr.lock.Unlock()
  if tr.CountsByStatus == nil || reset {
    tr.InvocationCounts = 0
    tr.CountsByStatus = map[string]int{}
    tr.CountsByStatusCodes = map[int]*KeyResultCounts{}
    tr.CountsByHeaders = map[string]*HeaderCounts{}
    tr.CountsByURIs = map[string]*KeyResultCounts{}
    tr.CountsByTimeBuckets = map[string]*KeyResultCounts{}
  }
}

func (tr *TargetResults) Init(trackingHeaders []string, crossTrackingHeaders map[string][]string, trackingTimeBuckets [][]int) {
  tr.init(true)
  tr.trackingHeaders = trackingHeaders
  tr.crossTrackingHeaders = crossTrackingHeaders
  tr.crossHeadersMap = util.BuildCrossHeadersMap(crossTrackingHeaders)
  tr.trackingTimeBuckets = trackingTimeBuckets
}

func newHeaderCounts(header string) *HeaderCounts {
  return &HeaderCounts{
    Header:                    header,
    CountsByValues:            map[string]*CountInfo{},
    CountsByStatusCodes:       map[int]*CountInfo{},
    CountsByValuesStatusCodes: map[string]map[int]*CountInfo{},
    CrossHeaders:              map[string]*HeaderCounts{},
    CrossHeadersByValues:      map[string]map[string]*HeaderCounts{},
  }
}

func newKeyResultCounts() *KeyResultCounts {
  return &KeyResultCounts{
    CountsByStatusCodes: map[int]*KeyResultCounts{},
  }
}

func (c *HeaderCounts) setTimestamps() {
  now := time.Now()
  if c.FirstResponse.IsZero() {
    c.FirstResponse = now
  }
  c.LastResponse = now
}

func (c *CountInfo) increment(retries int, by ...*CountInfo) {
  if len(by) > 0 {
    c.Count += by[0].Count
    c.Retries += by[0].Retries
    if c.FirstResponse.IsZero() || c.FirstResponse.After(by[0].FirstResponse) {
      c.FirstResponse = by[0].FirstResponse
    }
    if c.LastResponse.IsZero() || c.LastResponse.Before(by[0].LastResponse) {
      c.LastResponse = by[0].LastResponse
    }
  } else {
    c.Count++
    c.Retries += retries
    now := time.Now()
    if c.FirstResponse.IsZero() {
      c.FirstResponse = now
    }
    c.LastResponse = now
  }
}

func incrementHeaderCountForStatus(m map[int]*CountInfo, statusCode int, retries int, by ...*CountInfo) {
  if m[statusCode] == nil {
    m[statusCode] = &CountInfo{}
  }
  m[statusCode].increment(retries, by...)
}

func incrementHeaderCountForValue(m map[string]*CountInfo, value string, retries int, by ...*CountInfo) {
  if m[value] == nil {
    m[value] = &CountInfo{}
  }
  m[value].increment(retries, by...)
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
    crossHeaderCounts.setTimestamps()
    crossHeaderCounts.increment(retries)
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
      crossHeaderCountsByValue.setTimestamps()
      crossHeaderCountsByValue.increment(retries)
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
  headerCounts.setTimestamps()
  headerCounts.increment(retries)
  incrementHeaderCountForStatus(headerCounts.CountsByStatusCodes, statusCode, retries)
  for _, value := range values {
    incrementHeaderCountForValue(headerCounts.CountsByValues, value, retries)
    if headerCounts.CountsByValuesStatusCodes[value] == nil {
      headerCounts.CountsByValuesStatusCodes[value] = map[int]*CountInfo{}
    }
    incrementHeaderCountForStatus(headerCounts.CountsByValuesStatusCodes[value], statusCode, retries)
  }
}

func addKeyResultCounts(counts map[string]*KeyResultCounts, key string, statusCode int, retries int) {
  if counts[key] == nil {
    counts[key] = newKeyResultCounts()
  }
  keyCount := counts[key]
  keyCount.increment(retries)
  if keyCount.CountsByStatusCodes[statusCode] == nil {
    keyCount.CountsByStatusCodes[statusCode] = newKeyResultCounts()
  }
  keyCount.CountsByStatusCodes[statusCode].increment(retries)
}

func (tr *TargetResults) addTimeBucketResult(tb []int, uri string, statusCode int, retries int) {
  bucket := "[]"
  if tb != nil {
    bucket = fmt.Sprint(tb)
  }
  addKeyResultCounts(tr.CountsByTimeBuckets, bucket, statusCode, retries)
  if uriCount := tr.CountsByURIs[uri]; uriCount != nil {
    if uriCount.CountsByTimeBuckets == nil {
      uriCount.CountsByTimeBuckets = map[string]*KeyResultCounts{}
    }
    addKeyResultCounts(uriCount.CountsByTimeBuckets, bucket, statusCode, retries)
  }
  if scCount := tr.CountsByStatusCodes[statusCode]; scCount != nil {
    if scCount.CountsByTimeBuckets == nil {
      scCount.CountsByTimeBuckets = map[string]*KeyResultCounts{}
    }
    if scCount.CountsByTimeBuckets[bucket] == nil {
      scCount.CountsByTimeBuckets[bucket] = newKeyResultCounts()
    }
    scCount.CountsByTimeBuckets[bucket].increment(retries)
  }
}

func (tr *TargetResults) addURIResult(uri string, statusCode int, retries int) {
  addKeyResultCounts(tr.CountsByURIs, uri, statusCode, retries)
}

func (tr *TargetResults) AddResult(result *invocation.InvocationResult) {
  tr.init(false)
  tr.lock.Lock()
  defer tr.lock.Unlock()
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
  if tr.CountsByStatusCodes[result.StatusCode] == nil {
    tr.CountsByStatusCodes[result.StatusCode] = newKeyResultCounts()
  }
  tr.CountsByStatusCodes[result.StatusCode].increment(result.Retries)

  uri := strings.ToLower(result.URI)
  tr.addURIResult(uri, result.StatusCode, result.Retries)

  for _, h := range tr.trackingHeaders {
    for rh, values := range result.Headers {
      if strings.EqualFold(h, rh) {
        tr.addHeaderResult(h, values, result.StatusCode, result.Retries)
        tr.processCrossHeadersForHeader(h, values, result.StatusCode, result.Retries, result.Headers)
      }
    }
  }

  if len(tr.trackingTimeBuckets) > 0 {
    addedToTimeBucket := false
    for _, tb := range tr.trackingTimeBuckets {
      if result.TimeTakenMs >= tb[0] && result.TimeTakenMs <= tb[1] {
        tr.addTimeBucketResult(tb, uri, result.StatusCode, result.Retries)
        addedToTimeBucket = true
        break
      }
    }
    if !addedToTimeBucket {
      tr.addTimeBucketResult(nil, uri, result.StatusCode, result.Retries)
    }
  }
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
    tr.Results[target] = &TargetResults{Target: target}
    tr.Results[target].init(true)
  }
  targetResults := tr.Results[target]
  allResults := tr.Results[""]
  tr.lock.Unlock()
  return targetResults, allResults
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

func (ir *InvocationResults) addResult(result *invocation.InvocationResult) {
  ir.init(false)
  ir.lock.RLock()
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
    invocationResults.init(true)
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
      invocationResults.addResult(result)
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

func processTrackingConfig(targetResults *TargetResults, allResults *TargetResults, trackingHeaders []string,
  crossTrackingHeaders map[string][]string, trackingTimeBuckets [][]int) {
  targetResults.trackingHeaders = trackingHeaders
  targetResults.crossTrackingHeaders = crossTrackingHeaders
  targetResults.crossHeadersMap = util.BuildCrossHeadersMap(crossTrackingHeaders)
  targetResults.trackingTimeBuckets = trackingTimeBuckets
  if allResults != nil {
    allResults.trackingHeaders = targetResults.trackingHeaders
    allResults.crossTrackingHeaders = targetResults.crossTrackingHeaders
    allResults.crossHeadersMap = targetResults.crossHeadersMap
  }
}

func ResultChannelSinkFactory(target *invocation.InvocationSpec, trackingHeaders []string,
  crossTrackingHeaders map[string][]string, trackingTimeBuckets [][]int) invocation.ResultSinkFactory {
  targetResults, allResults := targetsResults.getTargetResults(target.Name)
  processTrackingConfig(targetResults, allResults, trackingHeaders, crossTrackingHeaders, trackingTimeBuckets)
  return func(tracker *invocation.InvocationTracker) invocation.ResultSink {
    invocationResults := invocationsResults.getInvocation(tracker.ID)
    invocationResults.Target = target
    invocationResults.Status = tracker.Status
    startRegistrySender()
    go channelSink(tracker.ID, tracker.ResultChannel, tracker.DoneChannel, invocationResults, targetResults, allResults)
    return nil
  }
}

func ResultSinkFactory(target *invocation.InvocationSpec, trackingHeaders []string,
  crossTrackingHeaders map[string][]string, trackingTimeBuckets [][]int) invocation.ResultSinkFactory {
  targetResults, allResults := targetsResults.getTargetResults(target.Name)
  processTrackingConfig(targetResults, allResults, trackingHeaders, crossTrackingHeaders, trackingTimeBuckets)
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
  result.setTimestamps()
  result.increment(0, &delta.CountInfo)
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

func processDeltaTimeBucketCounts(result, delta *KeyResultCounts) {
  for tb, count := range delta.CountsByTimeBuckets {
    if result.CountsByTimeBuckets == nil {
      result.CountsByTimeBuckets = map[string]*KeyResultCounts{}
    }
    if result.CountsByTimeBuckets[tb] == nil {
      result.CountsByTimeBuckets[tb] = newKeyResultCounts()
    }
    result.CountsByTimeBuckets[tb].increment(count.Retries, &count.CountInfo)
  }

}

func processDeltaKeyResultCounts(result, delta map[string]*KeyResultCounts) {
  for key, deltaCount := range delta {
    if result[key] == nil {
      result[key] = newKeyResultCounts()
    }
    resultCount := result[key]
    resultCount.increment(deltaCount.Retries, &deltaCount.CountInfo)
    if resultCount.CountsByStatusCodes == nil {
      resultCount.CountsByStatusCodes = map[int]*KeyResultCounts{}
    }
    for sc, count := range deltaCount.CountsByStatusCodes {
      if resultCount.CountsByStatusCodes[sc] == nil {
        resultCount.CountsByStatusCodes[sc] = newKeyResultCounts()
      }
      resultCount.CountsByStatusCodes[sc].increment(count.Retries, &count.CountInfo)
    }
    processDeltaTimeBucketCounts(resultCount, deltaCount)
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
  if delta.CountsByStatusCodes != nil {
    if results.CountsByStatusCodes == nil {
      results.CountsByStatusCodes = map[int]*KeyResultCounts{}
    }
    for sc, deltaCount := range delta.CountsByStatusCodes {
      if results.CountsByStatusCodes[sc] == nil {
        results.CountsByStatusCodes[sc] = newKeyResultCounts()
      }
      results.CountsByStatusCodes[sc].increment(deltaCount.Retries, &deltaCount.CountInfo)
      processDeltaTimeBucketCounts(results.CountsByStatusCodes[sc], deltaCount)
    }
  }
  if delta.CountsByHeaders != nil {
    if results.CountsByHeaders == nil {
      results.CountsByHeaders = map[string]*HeaderCounts{}
    }
    processDeltaHeaderCounts(results.CountsByHeaders, delta.CountsByHeaders, results.crossTrackingHeaders, results.crossHeadersMap)
  }
  if delta.CountsByURIs != nil {
    if results.CountsByURIs == nil {
      results.CountsByURIs = map[string]*KeyResultCounts{}
    }
    processDeltaKeyResultCounts(results.CountsByURIs, delta.CountsByURIs)
  }
  if delta.CountsByTimeBuckets != nil {
    if results.CountsByTimeBuckets == nil {
      results.CountsByTimeBuckets = map[string]*KeyResultCounts{}
    }
    processDeltaKeyResultCounts(results.CountsByTimeBuckets, delta.CountsByTimeBuckets)
  }
}
func incrementStatusCodeCounts(counts map[int]int, countsByTimeBuckets map[int]map[string]int, delta map[int]*KeyResultCounts) {
  for key, val := range delta {
    counts[key] += val.Count
    if countsByTimeBuckets != nil {
      if countsByTimeBuckets[key] == nil {
        countsByTimeBuckets[key] = map[string]int{}
      }
      for tb, count := range val.CountsByTimeBuckets {
        countsByTimeBuckets[key][tb] += count.Count
      }
    }
  }
}

func incrementKeyResultCounts(counts map[string]int, countsByStatusCodes map[string]map[int]int, countsByTimeBuckets map[string]map[string]int, delta map[string]*KeyResultCounts) {
  for key, val := range delta {
    counts[key] += val.Count
    if countsByStatusCodes != nil {
      if countsByStatusCodes[key] == nil {
        countsByStatusCodes[key] = map[int]int{}
      }
      for sc, count := range val.CountsByStatusCodes {
        countsByStatusCodes[key][sc] += count.Count
      }
    }
    if countsByTimeBuckets != nil {
      if countsByTimeBuckets[key] == nil {
        countsByTimeBuckets[key] = map[string]int{}
      }
      for tb, count := range val.CountsByTimeBuckets {
        countsByTimeBuckets[key][tb] += count.Count
      }
    }
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
  if tsr.CountsByTargetStatusCodeTimeBuckets[tr.Target] == nil {
    tsr.CountsByTargetStatusCodeTimeBuckets[tr.Target] = map[int]map[string]int{}
  }
  if tr.CountsByStatusCodes != nil {
    incrementStatusCodeCounts(tsr.CountsByStatusCodes, tsr.CountsByStatusCodeTimeBuckets, tr.CountsByStatusCodes)
    incrementStatusCodeCounts(tsr.CountsByTargetStatusCodes[tr.Target], tsr.CountsByTargetStatusCodeTimeBuckets[tr.Target], tr.CountsByStatusCodes)
  }
  if tsr.CountsByTargetHeaders[tr.Target] == nil {
    tsr.CountsByTargetHeaders[tr.Target] = map[string]int{}
  }
  if tsr.CountsByTargetHeaderValues[tr.Target] == nil {
    tsr.CountsByTargetHeaderValues[tr.Target] = map[string]map[string]int{}
  }
  if tr.CountsByHeaders != nil {
    for h, counts := range tr.CountsByHeaders {
      tsr.CountsByHeaders[h] += counts.Count
      tsr.CountsByTargetHeaders[tr.Target][h] += counts.Count
      if tsr.CountsByHeaderValues[h] == nil {
        tsr.CountsByHeaderValues[h] = map[string]int{}
      }
      if tsr.CountsByTargetHeaderValues[tr.Target][h] == nil {
        tsr.CountsByTargetHeaderValues[tr.Target][h] = map[string]int{}
      }
      for v, count := range counts.CountsByValues {
        tsr.CountsByHeaderValues[h][v] += count.Count
        tsr.CountsByTargetHeaderValues[tr.Target][h][v] += count.Count
      }
    }
  }
  if tsr.CountsByTargetURIs[tr.Target] == nil {
    tsr.CountsByTargetURIs[tr.Target] = map[string]int{}
  }
  if tsr.CountsByTargetURIStatusCodes[tr.Target] == nil {
    tsr.CountsByTargetURIStatusCodes[tr.Target] = map[string]map[int]int{}
  }
  if tsr.CountsByTargetURITimeBuckets[tr.Target] == nil {
    tsr.CountsByTargetURITimeBuckets[tr.Target] = map[string]map[string]int{}
  }
  if tr.CountsByURIs != nil {
    incrementKeyResultCounts(tsr.CountsByURIs, tsr.CountsByURIStatusCodes, tsr.CountsByURITimeBuckets, tr.CountsByURIs)
    incrementKeyResultCounts(tsr.CountsByTargetURIs[tr.Target], tsr.CountsByTargetURIStatusCodes[tr.Target], tsr.CountsByTargetURITimeBuckets[tr.Target], tr.CountsByURIs)
  }
  if tsr.CountsByTargetTimeBuckets[tr.Target] == nil {
    tsr.CountsByTargetTimeBuckets[tr.Target] = map[string]int{}
  }
  if tsr.CountsByTargetTimeBucketStatusCodes[tr.Target] == nil {
    tsr.CountsByTargetTimeBucketStatusCodes[tr.Target] = map[string]map[int]int{}
  }
  if tr.CountsByTimeBuckets != nil {
    incrementKeyResultCounts(tsr.CountsByTimeBuckets, tsr.CountsByTimeBucketStatusCodes, nil, tr.CountsByTimeBuckets)
    incrementKeyResultCounts(tsr.CountsByTargetTimeBuckets[tr.Target], tsr.CountsByTargetTimeBucketStatusCodes[tr.Target], nil, tr.CountsByTimeBuckets)
  }
}

func (tsr *TargetsSummaryResults) Init() {
  tsr.TargetInvocationCounts = map[string]int{}
  tsr.TargetFirstResponses = map[string]time.Time{}
  tsr.TargetLastResponses = map[string]time.Time{}
  tsr.CountsByStatusCodes = map[int]int{}
  tsr.CountsByStatusCodeTimeBuckets = map[int]map[string]int{}
  tsr.CountsByHeaders = map[string]int{}
  tsr.CountsByURIs = map[string]int{}
  tsr.CountsByURIStatusCodes = map[string]map[int]int{}
  tsr.CountsByURITimeBuckets = map[string]map[string]int{}
  tsr.CountsByTimeBuckets = map[string]int{}
  tsr.CountsByTimeBucketStatusCodes = map[string]map[int]int{}
  tsr.CountsByHeaderValues = map[string]map[string]int{}
  tsr.CountsByTargetStatusCodes = map[string]map[int]int{}
  tsr.CountsByTargetStatusCodeTimeBuckets = map[string]map[int]map[string]int{}
  tsr.CountsByTargetHeaders = map[string]map[string]int{}
  tsr.CountsByTargetHeaderValues = map[string]map[string]map[string]int{}
  tsr.CountsByTargetURIs = map[string]map[string]int{}
  tsr.CountsByTargetURIStatusCodes = map[string]map[string]map[int]int{}
  tsr.CountsByTargetURITimeBuckets = map[string]map[string]map[string]int{}
  tsr.CountsByTargetTimeBuckets = map[string]map[string]int{}
  tsr.CountsByTargetTimeBucketStatusCodes = map[string]map[string]map[int]int{}
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
