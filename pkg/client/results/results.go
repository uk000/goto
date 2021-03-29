package results

import (
  "encoding/json"
  "fmt"
  "goto/pkg/constants"
  "goto/pkg/global"
  "goto/pkg/invocation"
  "goto/pkg/util"
  "log"
  "reflect"
  "strings"
  "sync"
  "time"
)

type CountInfo struct {
  Count         int       `json:"count"`
  Retries       int       `json:"retries"`
  FirstResultAt time.Time `json:"firstResultAt,omitempty"`
  LastResultAt  time.Time `json:"lastResultAt,omitempty"`
}

type HeaderCounts struct {
  CountInfo
  Header                    string                              `json:"header"`
  CountsByValues            map[string]*CountInfo               `json:"countsByValues,omitempty"`
  CountsByStatusCodes       map[int]*CountInfo                  `json:"countsByStatusCodes,omitempty"`
  CountsByValuesStatusCodes map[string]map[int]*CountInfo       `json:"countsByValuesStatusCodes,omitempty"`
  CrossHeaders              map[string]*HeaderCounts            `json:"crossHeaders,omitempty"`
  CrossHeadersByValues      map[string]map[string]*HeaderCounts `json:"crossHeadersByValues,omitempty"`
}

type KeyResultCounts struct {
  CountInfo
  ByStatusCodes KeyResult `json:"byStatusCodes,omitempty"`
  ByTimeBuckets KeyResult `json:"byTimeBuckets,omitempty"`
}

type KeyResult map[interface{}]*KeyResultCounts

type TargetResults struct {
  Target                       string                   `json:"target"`
  InvocationCount              int                      `json:"invocationCount"`
  FirstResultAt                time.Time                `json:"firstResultAt,omitempty"`
  LastResultAt                 time.Time                `json:"lastResultAt,omitempty"`
  RetriedInvocationCounts      int                      `json:"retriedInvocationCounts"`
  CountsByHeaders              map[string]*HeaderCounts `json:"countsByHeaders,omitempty"`
  CountsByStatus               map[string]int           `json:"countsByStatus,omitempty"`
  CountsByStatusCodes          KeyResult                `json:"countsByStatusCodes,omitempty"`
  CountsByURIs                 KeyResult                `json:"countsByURIs,omitempty"`
  CountsByRequestPayloadSizes  KeyResult                `json:"countsByRequestPayloadSizes,omitempty"`
  CountsByResponsePayloadSizes KeyResult                `json:"countsByResponsePayloadSizes,omitempty"`
  CountsByRetries              KeyResult                `json:"countsByRetries,omitempty"`
  CountsByRetryReasons         KeyResult                `json:"countsByRetryReasons,omitempty"`
  CountsByErrors               KeyResult                `json:"countsByErrors,omitempty"`
  CountsByTimeBuckets          KeyResult                `json:"countsByTimeBuckets,omitempty"`
  trackingHeaders              []string
  crossTrackingHeaders         map[string][]string
  crossHeadersMap              map[string]string
  trackingTimeBuckets          [][]int
  pendingRegistrySend          bool
  lock                         sync.RWMutex
}

type SummaryCounts struct {
  Count         int
  ByStatusCodes map[interface{}]int
  ByTimeBuckets map[interface{}]int
}

type SummaryResult map[interface{}]*SummaryCounts

type AggregateResults struct {
  CountsByStatusCodes          SummaryResult             `json:"countsByStatusCodes,omitempty"`
  CountsByHeaders              map[string]int            `json:"countsByHeaders,omitempty"`
  CountsByHeaderValues         map[string]map[string]int `json:"countsByHeaderValues,omitempty"`
  CountsByURIs                 SummaryResult             `json:"countsByURIs,omitempty"`
  CountsByRequestPayloadSizes  SummaryResult             `json:"countsByRequestPayloadSizes,omitempty"`
  CountsByResponsePayloadSizes SummaryResult             `json:"countsByResponsePayloadSizes,omitempty"`
  CountsByRetries              SummaryResult             `json:"countsByRetries,omitempty"`
  CountsByRetryReasons         SummaryResult             `json:"countsByRetryReasons,omitempty"`
  CountsByErrors               SummaryResult             `json:"countsByErrors,omitempty"`
  CountsByTimeBuckets          SummaryResult             `json:"countsByTimeBuckets,omitempty"`
}

type ClientAggregateResults struct {
  AggregateResults
  InvocationCount int       `json:"invocationCount,omitempty"`
  FirstResultAt   time.Time `json:"firstResultAt,omitempty"`
  LastResultAt    time.Time `json:"lastResultAt,omitempty"`
}

type ClientTargetsAggregateResults struct {
  AggregateResults
  ResultsByTargets map[string]*ClientAggregateResults `json:"byTargets,omitempty"`
}

type TargetsResults struct {
  Results map[string]*TargetResults `json:"results"`
  lock    sync.RWMutex
}

type InvocationResults struct {
  InvocationIndex uint32                         `json:"invocationIndex"`
  Target          *invocation.InvocationSpec     `json:"target"`
  Status          *invocation.InvocationStatus   `json:"status"`
  Results         []*invocation.InvocationResult `json:"results"`
  Finished        bool                           `json:"finished"`
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

func (kr KeyResult) MarshalJSON() ([]byte, error) {
  data := map[string]interface{}{}
  for k, v := range kr {
    data[fmt.Sprint(k)] = v
  }
  return json.Marshal(data)
}

func (kr *KeyResult) UnmarshalJSON(b []byte) error {
  data := map[string]*KeyResultCounts{}
  if err := json.Unmarshal(b, &data); err != nil {
    return err
  }
  *kr = KeyResult{}
  for k, v := range data {
    if len(v.ByStatusCodes) == 0 {
      v.ByStatusCodes = nil
    }
    if len(v.ByTimeBuckets) == 0 {
      v.ByTimeBuckets = nil
    }
    (*kr)[k] = v
  }
  return nil
}

func (s *SummaryCounts) MarshalJSON() ([]byte, error) {
  if len(s.ByStatusCodes) == 0 && len(s.ByTimeBuckets) == 0 {
    return json.Marshal(s.Count)
  }
  data := map[string]interface{}{}
  data["count"] = s.Count
  if len(s.ByStatusCodes) > 0 {
    byStatusCodes := map[string]interface{}{}
    for k, v := range s.ByStatusCodes {
      byStatusCodes[fmt.Sprint(k)] = v
    }
    data["byStatusCodes"] = byStatusCodes
  }
  if len(s.ByTimeBuckets) > 0 {
    byTimeBuckets := map[string]interface{}{}
    for k, v := range s.ByTimeBuckets {
      byTimeBuckets[fmt.Sprint(k)] = v
    }
    data["byTimeBuckets"] = byTimeBuckets
  }
  return json.Marshal(data)
}

func (s SummaryResult) MarshalJSON() ([]byte, error) {
  data := map[string]interface{}{}
  for k, v := range s {
    data[fmt.Sprint(k)] = v
  }
  return json.Marshal(data)
}

func (tr *TargetResults) init(reset bool) {
  tr.lock.Lock()
  defer tr.lock.Unlock()
  if tr.CountsByStatus == nil || reset {
    tr.InvocationCount = 0
    tr.CountsByStatus = map[string]int{}
    tr.CountsByHeaders = map[string]*HeaderCounts{}
    tr.CountsByStatusCodes = KeyResult{}
    tr.CountsByURIs = KeyResult{}
    tr.CountsByRequestPayloadSizes = KeyResult{}
    tr.CountsByResponsePayloadSizes = KeyResult{}
    tr.CountsByRetries = KeyResult{}
    tr.CountsByRetryReasons = KeyResult{}
    tr.CountsByTimeBuckets = KeyResult{}
    tr.CountsByErrors = KeyResult{}
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

func newKeyResultCounts(withStatusCodes, withTimeBuckets bool) *KeyResultCounts {
  r := &KeyResultCounts{}
  if withStatusCodes {
    r.ByStatusCodes = KeyResult{}
  }
  if withTimeBuckets {
    r.ByTimeBuckets = KeyResult{}
  }
  return r
}

func (c *HeaderCounts) setTimestamps(ts time.Time) {
  if c.FirstResultAt.IsZero() || ts.Before(c.FirstResultAt) {
    c.FirstResultAt = ts
  }
  c.LastResultAt = ts
}

func (c *CountInfo) increment(retries int, ts time.Time) {
  c.Count++
  c.Retries += retries
  if c.FirstResultAt.IsZero() || ts.Before(c.FirstResultAt) {
    c.FirstResultAt = ts
  }
  c.LastResultAt = ts
}

func (c *CountInfo) incrementBy(retries int, by *CountInfo) {
  c.Count += by.Count
  c.Retries += by.Retries
  if c.FirstResultAt.IsZero() || by.FirstResultAt.Before(c.FirstResultAt) {
    c.FirstResultAt = by.FirstResultAt
  }
  if c.LastResultAt.IsZero() || c.LastResultAt.Before(by.LastResultAt) {
    c.LastResultAt = by.LastResultAt
  }
}

func incrementHeaderCount(m map[int]*CountInfo, statusCode int, retries int, ts time.Time) {
  if m[statusCode] == nil {
    m[statusCode] = &CountInfo{}
  }
  m[statusCode].increment(retries, ts)
}

func incrementHeaderCountBy(m map[int]*CountInfo, statusCode int, retries int, by *CountInfo) {
  if m[statusCode] == nil {
    m[statusCode] = &CountInfo{}
  }
  m[statusCode].incrementBy(retries, by)
}

func incrementHeaderValueCount(m map[string]*CountInfo, value string, retries int, ts time.Time) {
  if m[value] == nil {
    m[value] = &CountInfo{}
  }
  m[value].increment(retries, ts)
}

func incrementHeaderValueCountBy(m map[string]*CountInfo, value string, retries int, by *CountInfo) {
  if m[value] == nil {
    m[value] = &CountInfo{}
  }
  m[value].incrementBy(retries, by)
}

func (tr *TargetResults) processCrossHeadersForHeader(header string, values []string, statusCode int, retries int, ts time.Time, allHeaders map[string][]string) {
  if crossHeaders := tr.crossTrackingHeaders[header]; crossHeaders != nil {
    processSubCrossHeadersForHeader(header, values, statusCode, retries, ts, tr.CountsByHeaders[header], crossHeaders, allHeaders)
  }
}

func processSubCrossHeadersForHeader(header string, values []string, statusCode int, retries int, ts time.Time,
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
    crossHeaderCounts.setTimestamps(headerCounts.LastResultAt)
    crossHeaderCounts.increment(retries, ts)
    incrementHeaderCount(crossHeaderCounts.CountsByStatusCodes, statusCode, retries, ts)
    for _, crossValue := range crossValues {
      incrementHeaderValueCount(crossHeaderCounts.CountsByValues, crossValue, retries, ts)
      if crossHeaderCounts.CountsByValuesStatusCodes[crossValue] == nil {
        crossHeaderCounts.CountsByValuesStatusCodes[crossValue] = map[int]*CountInfo{}
      }
      incrementHeaderCount(crossHeaderCounts.CountsByValuesStatusCodes[crossValue], statusCode, retries, ts)
    }
    processSubCrossHeaders := i < len(crossHeaders)-1
    subCrossHeaders := crossHeaders[i+1:]
    if processSubCrossHeaders {
      processSubCrossHeadersForHeader(crossHeader, crossValues, statusCode, retries, ts, crossHeaderCounts, subCrossHeaders, allHeaders)
    }
    for _, value := range values {
      if headerCounts.CrossHeadersByValues[value][crossHeader] == nil {
        headerCounts.CrossHeadersByValues[value][crossHeader] = newHeaderCounts(crossHeader)
      }
      crossHeaderCountsByValue := headerCounts.CrossHeadersByValues[value][crossHeader]
      crossHeaderCountsByValue.setTimestamps(headerCounts.LastResultAt)
      crossHeaderCountsByValue.increment(retries, ts)
      incrementHeaderCount(crossHeaderCountsByValue.CountsByStatusCodes, statusCode, retries, ts)
      for _, crossValue := range crossValues {
        incrementHeaderValueCount(crossHeaderCountsByValue.CountsByValues, crossValue, retries, ts)
        if crossHeaderCountsByValue.CountsByValuesStatusCodes[crossValue] == nil {
          crossHeaderCountsByValue.CountsByValuesStatusCodes[crossValue] = map[int]*CountInfo{}
        }
        incrementHeaderCount(crossHeaderCountsByValue.CountsByValuesStatusCodes[crossValue], statusCode, retries, ts)
      }
      if processSubCrossHeaders {
        processSubCrossHeadersForHeader(crossHeader, crossValues, statusCode, retries, ts, crossHeaderCountsByValue, subCrossHeaders, allHeaders)
      }
    }
  }
}

func (tr *TargetResults) addHeaderResult(header string, values []string, statusCode int, retries int, ts time.Time) {
  if tr.CountsByHeaders[header] == nil {
    tr.CountsByHeaders[header] = newHeaderCounts(header)
  }
  headerCounts := tr.CountsByHeaders[header]
  headerCounts.setTimestamps(ts)
  headerCounts.increment(retries, ts)
  incrementHeaderCount(headerCounts.CountsByStatusCodes, statusCode, retries, ts)
  for _, value := range values {
    incrementHeaderValueCount(headerCounts.CountsByValues, value, retries, ts)
    if headerCounts.CountsByValuesStatusCodes[value] == nil {
      headerCounts.CountsByValuesStatusCodes[value] = map[int]*CountInfo{}
    }
    incrementHeaderCount(headerCounts.CountsByValuesStatusCodes[value], statusCode, retries, ts)
  }
}

func addKeyResultCounts(counts map[interface{}]*KeyResultCounts, key interface{}, statusCode int, retries int, ts time.Time, withStatusCodes, withTimeBuckets bool) {
  if counts[key] == nil {
    counts[key] = newKeyResultCounts(withStatusCodes, withTimeBuckets)
  }
  resultCount := counts[key]
  resultCount.increment(retries, ts)
  if withStatusCodes {
    if resultCount.ByStatusCodes[statusCode] == nil {
      resultCount.ByStatusCodes[statusCode] = &KeyResultCounts{}
    }
    resultCount.ByStatusCodes[statusCode].increment(retries, ts)
  }
}

func (tr *TargetResults) addTimeBucketResult(tb []int, ir *invocation.InvocationResult) {
  bucket := "[]"
  if tb != nil {
    bucket = fmt.Sprint(tb)
  }
  addKeyResultCounts(tr.CountsByTimeBuckets, bucket, ir.StatusCode, ir.Retries, ir.LastRequestAt, true, false)

  if uriCount := tr.CountsByURIs[ir.URI]; uriCount != nil {
    addKeyResultCounts(uriCount.ByTimeBuckets, bucket, ir.StatusCode, ir.Retries, ir.LastRequestAt, true, false)
  }

  if rpc := tr.CountsByRequestPayloadSizes[ir.RequestPayloadSize]; rpc != nil {
    addKeyResultCounts(rpc.ByTimeBuckets, bucket, ir.StatusCode, ir.Retries, ir.LastRequestAt, true, false)
  }

  if rpc := tr.CountsByResponsePayloadSizes[ir.RequestPayloadSize]; rpc != nil {
    addKeyResultCounts(rpc.ByTimeBuckets, bucket, ir.StatusCode, ir.Retries, ir.LastRequestAt, true, false)
  }

  if rc := tr.CountsByRetries[ir.Retries]; rc != nil {
    addKeyResultCounts(rc.ByTimeBuckets, bucket, ir.StatusCode, ir.Retries, ir.LastRequestAt, true, false)
  }

  if rrc := tr.CountsByRetryReasons[ir.LastRetryReason]; rrc != nil {
    addKeyResultCounts(rrc.ByTimeBuckets, bucket, ir.StatusCode, ir.Retries, ir.LastRequestAt, true, false)
  }

  for e := range ir.Errors {
    if ec := tr.CountsByErrors[e]; ec != nil {
      addKeyResultCounts(ec.ByTimeBuckets, bucket, ir.StatusCode, ir.Retries, ir.LastRequestAt, true, false)
    }
  }

  if sc := tr.CountsByStatusCodes[ir.StatusCode]; sc != nil {
    addKeyResultCounts(sc.ByTimeBuckets, bucket, ir.StatusCode, ir.Retries, ir.LastRequestAt, false, false)
  }
}

func (tr *TargetResults) AddResult(ir *invocation.InvocationResult) {
  fmt.Printf("AddResult: Target [%s] Request ID [%s] timestamp [%s]\n", tr.Target, ir.RequestID, ir.LastRequestAt)
  tr.init(false)
  tr.lock.Lock()
  defer tr.lock.Unlock()
  tr.InvocationCount++
  finishedAt := ir.LastRequestAt
  // if finishedAt.IsZero() {
  //   finishedAt = time.Now()
  // }
  if tr.FirstResultAt.IsZero() || finishedAt.Before(tr.FirstResultAt) {
    tr.FirstResultAt = finishedAt
  }
  tr.LastResultAt = finishedAt

  if ir.Retries > 0 {
    tr.RetriedInvocationCounts++
    addKeyResultCounts(tr.CountsByRetries, ir.Retries, ir.StatusCode, ir.Retries, ir.LastRequestAt, true, true)
    if ir.LastRetryReason != "" {
      addKeyResultCounts(tr.CountsByRetryReasons, ir.LastRetryReason, ir.StatusCode, ir.Retries, ir.LastRequestAt, true, true)
    }
  }

  tr.CountsByStatus[ir.Status]++
  addKeyResultCounts(tr.CountsByStatusCodes, ir.StatusCode, ir.StatusCode, ir.Retries, ir.LastRequestAt, false, true)

  uri := strings.ToLower(ir.URI)
  addKeyResultCounts(tr.CountsByURIs, uri, ir.StatusCode, ir.Retries, ir.LastRequestAt, true, true)

  for _, h := range tr.trackingHeaders {
    for rh, values := range ir.Headers {
      if strings.EqualFold(h, rh) {
        tr.addHeaderResult(h, values, ir.StatusCode, ir.Retries, finishedAt)
        tr.processCrossHeadersForHeader(h, values, ir.StatusCode, ir.Retries, ir.LastRequestAt, ir.Headers)
      }
    }
  }
  if ir.RequestPayloadSize > 0 {
    addKeyResultCounts(tr.CountsByRequestPayloadSizes, ir.RequestPayloadSize, ir.StatusCode, ir.Retries, ir.LastRequestAt, true, true)
  }
  if ir.ResponsePayloadSize > 0 {
    addKeyResultCounts(tr.CountsByResponsePayloadSizes, ir.ResponsePayloadSize, ir.StatusCode, ir.Retries, ir.LastRequestAt, true, true)
  }
  for e := range ir.Errors {
    addKeyResultCounts(tr.CountsByErrors, e, ir.StatusCode, ir.Retries, ir.LastRequestAt, true, true)
  }
  if len(tr.trackingTimeBuckets) > 0 {
    addedToTimeBucket := false
    took := ir.TookNanos / 1000000
    for _, tb := range tr.trackingTimeBuckets {
      if took >= tb[0] && took <= tb[1] {
        tr.addTimeBucketResult(tb, ir)
        addedToTimeBucket = true
        break
      }
    }
    if !addedToTimeBucket {
      tr.addTimeBucketResult(nil, ir)
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
    ir.Results = []*invocation.InvocationResult{}
  }
}

func (ir *InvocationResults) addResult(result *invocation.InvocationResult) {
  ir.init(false)
  ir.lock.Lock()
  defer ir.lock.Unlock()
  ir.Results = append(ir.Results, result)
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

func addDeltaCrossHeaders(result, delta map[string]*HeaderCounts, crossTrackingHeaders map[string][]string, crossHeadersMap map[string]string, detailed bool) {
  for ch, chCounts := range delta {
    if result[ch] == nil {
      result[ch] = newHeaderCounts(ch)
    }
    addDeltaHeaderCounts(result[ch], chCounts, crossTrackingHeaders, crossHeadersMap, detailed)
  }
}

func addDeltaCrossHeadersValues(result, delta map[string]map[string]*HeaderCounts, crossTrackingHeaders map[string][]string, crossHeadersMap map[string]string, detailed bool) {
  for value, chCounts := range delta {
    if result[value] == nil {
      result[value] = map[string]*HeaderCounts{}
    }
    for ch, chCounts := range chCounts {
      if result[value][ch] == nil {
        result[value][ch] = newHeaderCounts(ch)
      }
      addDeltaHeaderCounts(result[value][ch], chCounts, crossTrackingHeaders, crossHeadersMap, detailed)
    }
  }
}

func addDeltaHeaderCounts(result, delta *HeaderCounts, crossTrackingHeaders map[string][]string, crossHeadersMap map[string]string, detailed bool) {
  result.setTimestamps(delta.LastResultAt)
  result.incrementBy(0, &delta.CountInfo)
  if result.CountsByValues == nil {
    result.CountsByValues = map[string]*CountInfo{}
  }
  for value, count := range delta.CountsByValues {
    incrementHeaderValueCountBy(result.CountsByValues, value, 0, count)
  }
  if result.CountsByStatusCodes == nil {
    result.CountsByStatusCodes = map[int]*CountInfo{}
  }
  if detailed {
    for statusCode, count := range delta.CountsByStatusCodes {
      incrementHeaderCountBy(result.CountsByStatusCodes, statusCode, 0, count)
    }
    for value, valueCounts := range delta.CountsByValuesStatusCodes {
      if result.CountsByValuesStatusCodes[value] == nil {
        result.CountsByValuesStatusCodes[value] = map[int]*CountInfo{}
      }
      for statusCode, count := range valueCounts {
        incrementHeaderCountBy(result.CountsByValuesStatusCodes[value], statusCode, 0, count)
      }
    }
    if crossTrackingHeaders[result.Header] != nil || crossHeadersMap[result.Header] != "" {
      if delta.CrossHeaders != nil {
        if result.CrossHeaders == nil {
          result.CrossHeaders = map[string]*HeaderCounts{}
        }
        addDeltaCrossHeaders(result.CrossHeaders, delta.CrossHeaders, crossTrackingHeaders, crossHeadersMap, detailed)
      }
      if delta.CrossHeadersByValues != nil {
        if result.CrossHeadersByValues == nil {
          result.CrossHeadersByValues = map[string]map[string]*HeaderCounts{}
        }
        addDeltaCrossHeadersValues(result.CrossHeadersByValues, delta.CrossHeadersByValues, crossTrackingHeaders, crossHeadersMap, detailed)
      }
    }
  }
}

func processDeltaHeaderCounts(result, delta map[string]*HeaderCounts, crossTrackingHeaders map[string][]string, crossHeadersMap map[string]string, detailed bool) {
  for header, counts := range delta {
    if result[header] == nil {
      result[header] = newHeaderCounts(header)
    }
    addDeltaHeaderCounts(result[header], counts, crossTrackingHeaders, crossHeadersMap, detailed)
  }
}

func processDeltaTimeBucketCounts(delta, result *KeyResultCounts, updateStatusCodes bool) {
  for tb, count := range delta.ByTimeBuckets {
    if result.ByTimeBuckets[tb] == nil {
      result.ByTimeBuckets[tb] = newKeyResultCounts(updateStatusCodes, false)
    }
    result.ByTimeBuckets[tb].incrementBy(count.Retries, &count.CountInfo)
  }

}

func processDeltaKeyResultCounts(delta KeyResult, resultPtr *KeyResult, updateStatusCodes, updateTimeBuckets bool) {
  if delta == nil {
    return
  }
  if *resultPtr == nil {
    *resultPtr = KeyResult{}
  }
  result := *resultPtr
  for key, deltaCount := range delta {
    if result[key] == nil {
      result[key] = newKeyResultCounts(updateStatusCodes, updateTimeBuckets)
    }
    resultCount := result[key]
    resultCount.incrementBy(deltaCount.Retries, &deltaCount.CountInfo)
    if updateStatusCodes {
      for sc, count := range deltaCount.ByStatusCodes {
        if resultCount.ByStatusCodes[sc] == nil {
          resultCount.ByStatusCodes[sc] = newKeyResultCounts(false, updateTimeBuckets)
        }
        resultCount.ByStatusCodes[sc].incrementBy(count.Retries, &count.CountInfo)
      }
    }
    if updateTimeBuckets {
      processDeltaTimeBucketCounts(deltaCount, resultCount, updateStatusCodes)
    }
  }
}

func AddDeltaResults(results, delta *TargetResults, detailed bool) {
  fmt.Printf("AddDeltaResults: Target [%s] first response [%s] last response [%s]\n", delta.Target, delta.FirstResultAt.UTC().String(), delta.LastResultAt.UTC().String())
  if results.FirstResultAt.IsZero() || delta.FirstResultAt.Before(results.FirstResultAt) {
    results.FirstResultAt = delta.FirstResultAt
  }
  if results.LastResultAt.IsZero() || results.LastResultAt.Before(delta.LastResultAt) {
    results.LastResultAt = delta.LastResultAt
  }
  results.InvocationCount += delta.InvocationCount
  for k, v := range delta.CountsByStatus {
    results.CountsByStatus[k] += v
  }

  if delta.CountsByHeaders != nil {
    if results.CountsByHeaders == nil {
      results.CountsByHeaders = map[string]*HeaderCounts{}
    }
    processDeltaHeaderCounts(results.CountsByHeaders, delta.CountsByHeaders, results.crossTrackingHeaders, results.crossHeadersMap, detailed)
  }

  processDeltaKeyResultCounts(delta.CountsByStatusCodes, &results.CountsByStatusCodes, false, detailed)
  processDeltaKeyResultCounts(delta.CountsByURIs, &results.CountsByURIs, detailed, detailed)
  processDeltaKeyResultCounts(delta.CountsByRequestPayloadSizes, &results.CountsByRequestPayloadSizes, detailed, detailed)
  processDeltaKeyResultCounts(delta.CountsByResponsePayloadSizes, &results.CountsByResponsePayloadSizes, detailed, detailed)
  processDeltaKeyResultCounts(delta.CountsByRetries, &results.CountsByRetries, detailed, detailed)
  processDeltaKeyResultCounts(delta.CountsByRetryReasons, &results.CountsByRetryReasons, detailed, detailed)
  processDeltaKeyResultCounts(delta.CountsByErrors, &results.CountsByErrors, detailed, detailed)
  processDeltaKeyResultCounts(delta.CountsByTimeBuckets, &results.CountsByTimeBuckets, detailed, false)
}

func incrementKeyResultCounts(result SummaryResult, delta interface{}, detailed bool) {
  v := reflect.ValueOf(delta)
  if v.Kind() != reflect.Map {
    return
  }
  iter := v.MapRange()
  for iter.Next() {
    key := iter.Key().Interface()
    val := iter.Value().Interface().(*KeyResultCounts)
    if result[key] == nil {
      result[key] = &SummaryCounts{}
    }
    counts := result[key]
    counts.Count += val.Count
    if detailed {
      if len(val.ByStatusCodes) > 0 {
        if counts.ByStatusCodes == nil {
          counts.ByStatusCodes = map[interface{}]int{}
        }
        for sc, count := range val.ByStatusCodes {
          counts.ByStatusCodes[sc] += count.Count
        }
      }
      if len(val.ByTimeBuckets) > 0 {
        if counts.ByTimeBuckets == nil {
          counts.ByTimeBuckets = map[interface{}]int{}
        }
        for tb, count := range val.ByTimeBuckets {
          counts.ByTimeBuckets[tb] += count.Count
        }
      }
    }
  }
}

func (sr *AggregateResults) addTargetResult(tr *TargetResults, detailed bool) {
  if tr.CountsByHeaders != nil {
    for h, counts := range tr.CountsByHeaders {
      sr.CountsByHeaders[h] += counts.Count
      if sr.CountsByHeaderValues[h] == nil {
        sr.CountsByHeaderValues[h] = map[string]int{}
      }
      for v, count := range counts.CountsByValues {
        sr.CountsByHeaderValues[h][v] += count.Count
      }
    }
  }
  if tr.CountsByStatusCodes != nil {
    incrementKeyResultCounts(sr.CountsByStatusCodes, tr.CountsByStatusCodes, detailed)
  }
  if tr.CountsByURIs != nil {
    incrementKeyResultCounts(sr.CountsByURIs, tr.CountsByURIs, detailed)
  }
  if tr.CountsByRequestPayloadSizes != nil {
    incrementKeyResultCounts(sr.CountsByRequestPayloadSizes, tr.CountsByRequestPayloadSizes, detailed)
  }
  if tr.CountsByResponsePayloadSizes != nil {
    incrementKeyResultCounts(sr.CountsByResponsePayloadSizes, tr.CountsByResponsePayloadSizes, detailed)
  }
  if tr.CountsByRetries != nil {
    incrementKeyResultCounts(sr.CountsByRetries, tr.CountsByRetries, detailed)
  }
  if tr.CountsByRetryReasons != nil {
    incrementKeyResultCounts(sr.CountsByRetryReasons, tr.CountsByRetryReasons, detailed)
  }
  if tr.CountsByErrors != nil {
    incrementKeyResultCounts(sr.CountsByErrors, tr.CountsByErrors, detailed)
  }
  if tr.CountsByTimeBuckets != nil {
    incrementKeyResultCounts(sr.CountsByTimeBuckets, tr.CountsByTimeBuckets, detailed)
  }
}

func (tsr *ClientAggregateResults) addTargetResult(tr *TargetResults, detailed bool) {
  tsr.InvocationCount += tr.InvocationCount
  if tsr.FirstResultAt.IsZero() || tr.FirstResultAt.Before(tsr.FirstResultAt) {
    tsr.FirstResultAt = tr.FirstResultAt
  }
  if tsr.LastResultAt.IsZero() || tsr.LastResultAt.Before(tr.LastResultAt) {
    tsr.LastResultAt = tr.LastResultAt
  }
  tsr.AggregateResults.addTargetResult(tr, detailed)
}

func (tsr *ClientTargetsAggregateResults) AddTargetResult(tr *TargetResults, detailed bool) {
  fmt.Printf("TargetsSummaryResults.AddTargetResult: Target [%s] first response [%s] last response [%s]\n",
    tr.Target, tr.FirstResultAt.UTC().String(), tr.LastResultAt.UTC().String())
  tsr.AggregateResults.addTargetResult(tr, detailed)
  if tsr.ResultsByTargets[tr.Target] == nil {
    tsr.ResultsByTargets[tr.Target] = &ClientAggregateResults{}
    tsr.ResultsByTargets[tr.Target].Init()
  }
  tsr.ResultsByTargets[tr.Target].addTargetResult(tr, detailed)
}

func (sr *AggregateResults) Init() {
  sr.CountsByStatusCodes = SummaryResult{}
  sr.CountsByHeaders = map[string]int{}
  sr.CountsByHeaderValues = map[string]map[string]int{}
  sr.CountsByURIs = SummaryResult{}
  sr.CountsByRequestPayloadSizes = SummaryResult{}
  sr.CountsByResponsePayloadSizes = SummaryResult{}
  sr.CountsByRetries = SummaryResult{}
  sr.CountsByRetryReasons = SummaryResult{}
  sr.CountsByErrors = SummaryResult{}
  sr.CountsByTimeBuckets = SummaryResult{}
}

func (tsr *ClientTargetsAggregateResults) Init() {
  tsr.AggregateResults.Init()
  tsr.ResultsByTargets = map[string]*ClientAggregateResults{}
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
