package results

import (
	"fmt"
	"goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/http/invocation"
	"goto/pkg/util"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type HeaderCount struct {
  Value         int       `json:"count"`
  FirstResponse time.Time `json:"firstResponse"`
  LastResponse  time.Time `json:"lastResponse"`
}

type HeaderCounts struct {
  Header                    string
  Count                     *HeaderCount                        `json:"count"`
  CountsByValues            map[string]*HeaderCount             `json:"countsByValues"`
  CountsByStatusCodes       map[int]*HeaderCount                `json:"countsByStatusCodes"`
  CountsByValuesStatusCodes map[string]map[int]*HeaderCount     `json:"countsByValuesStatusCodes"`
  CrossHeaders              map[string]*HeaderCounts            `json:"crossHeaders"`
  CrossHeadersByValues      map[string]map[string]*HeaderCounts `json:"crossHeadersByValues"`
  FirstResponse             time.Time                           `json:"firstResponse"`
  LastResponse              time.Time                           `json:"lastResponse"`
}

type TargetResults struct {
  Target              string                   `json:"target"`
  InvocationCounts    int                      `json:"invocationCounts"`
  FirstResponse       time.Time                `json:"firstResponse"`
  LastResponse        time.Time                `json:"lastResponse"`
  CountsByStatus      map[string]int           `json:"countsByStatus"`
  CountsByStatusCodes map[int]int              `json:"countsByStatusCodes"`
  CountsByHeaders     map[string]*HeaderCounts `json:"countsByHeaders"`
  CountsByURIs        map[string]int           `json:"countsByURIs"`
  lock                sync.RWMutex
  crossHeadersMap     map[string]string
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
  CountsByCrossHeaders       map[string]*HeaderCounts             `json:"countsByCrossHeaders"`
  CountsByTargetCrossHeaders map[string]map[string]*HeaderCounts  `json:"countsByTargetCrossHeaders"`
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
  chanSendTargetsToRegistry    chan *TargetResults     = make(chan *TargetResults, 200)
  chanSendInvocationToRegistry chan *InvocationResults = make(chan *InvocationResults, 200)
  chanLockInvocationInRegistry chan uint32             = make(chan uint32, 100)
  stopRegistrySender           chan bool               = make(chan bool, 10)
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
    tr.CountsByHeaders = map[string]*HeaderCounts{}
    //tr.CountsByHeaderValues = map[string]map[string]int{}
    tr.CountsByURIs = map[string]int{}
  }
}

func (tr *TargetResults) Init() {
  tr.init(true)
}

func newHeaderCounts(header string) *HeaderCounts {
  return &HeaderCounts{
    Header:                    header,
    Count:                     &HeaderCount{},
    CountsByValues:            map[string]*HeaderCount{},
    CountsByStatusCodes:       map[int]*HeaderCount{},
    CountsByValuesStatusCodes: map[string]map[int]*HeaderCount{},
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

func incrementHeaderCount(h *HeaderCount, by ...*HeaderCount) {
  if len(by) > 0 {
    h.Value += by[0].Value
    if h.FirstResponse.IsZero() || h.FirstResponse.After(by[0].FirstResponse) {
      h.FirstResponse = by[0].FirstResponse
    }
    if h.LastResponse.IsZero() || h.LastResponse.Before(by[0].LastResponse) {
      h.LastResponse = by[0].LastResponse
    }
  } else {
    h.Value++
    now := time.Now()
    if h.FirstResponse.IsZero() {
      h.FirstResponse = now
    }
    h.LastResponse = now
  }
}

func incrementHeaderCountForStatus(m map[int]*HeaderCount, statusCode int, by ...*HeaderCount) {
  if m[statusCode] == nil {
    m[statusCode] = &HeaderCount{}
  }
  incrementHeaderCount(m[statusCode], by...)
}

func incrementHeaderCountForValue(m map[string]*HeaderCount, value string, by ...*HeaderCount) {
  if m[value] == nil {
    m[value] = &HeaderCount{}
  }
  incrementHeaderCount(m[value], by...)
}

func (tr *TargetResults) processCrossHeadersForHeader(header string, values []string, statusCode int,
  allHeaders map[string][]string, crossTrackingHeaders map[string][]string) {
  if crossHeaders := crossTrackingHeaders[header]; crossHeaders != nil {
    processSubCrossHeadersForHeader(header, values, statusCode, tr.CountsByHeaders[header], crossHeaders, allHeaders)
  }
}

func processSubCrossHeadersForHeader(header string, values []string, statusCode int,
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
    incrementHeaderCount(crossHeaderCounts.Count)
    incrementHeaderCountForStatus(crossHeaderCounts.CountsByStatusCodes, statusCode)
    for _, crossValue := range crossValues {
      incrementHeaderCountForValue(crossHeaderCounts.CountsByValues, crossValue)
      if crossHeaderCounts.CountsByValuesStatusCodes[crossValue] == nil {
        crossHeaderCounts.CountsByValuesStatusCodes[crossValue] = map[int]*HeaderCount{}
      }
      incrementHeaderCountForStatus(crossHeaderCounts.CountsByValuesStatusCodes[crossValue], statusCode)
    }
    processSubCrossHeaders := i < len(crossHeaders)-1
    subCrossHeaders := crossHeaders[i+1:]
    if processSubCrossHeaders {
      processSubCrossHeadersForHeader(crossHeader, crossValues, statusCode, crossHeaderCounts, subCrossHeaders, allHeaders)
    }
    for _, value := range values {
      if headerCounts.CrossHeadersByValues[value][crossHeader] == nil {
        headerCounts.CrossHeadersByValues[value][crossHeader] = newHeaderCounts(crossHeader)
      }
      crossHeaderCountsByValue := headerCounts.CrossHeadersByValues[value][crossHeader]
      setTimestamps(crossHeaderCountsByValue)
      incrementHeaderCount(crossHeaderCountsByValue.Count)
      incrementHeaderCountForStatus(crossHeaderCountsByValue.CountsByStatusCodes, statusCode)
      for _, crossValue := range crossValues {
        incrementHeaderCountForValue(crossHeaderCountsByValue.CountsByValues, crossValue)
        if crossHeaderCountsByValue.CountsByValuesStatusCodes[crossValue] == nil {
          crossHeaderCountsByValue.CountsByValuesStatusCodes[crossValue] = map[int]*HeaderCount{}
        }
        incrementHeaderCountForStatus(crossHeaderCountsByValue.CountsByValuesStatusCodes[crossValue], statusCode)
      }
      if processSubCrossHeaders {
        processSubCrossHeadersForHeader(crossHeader, crossValues, statusCode, crossHeaderCountsByValue, subCrossHeaders, allHeaders)
      }
    }
  }
}

func (tr *TargetResults) addHeaderResult(header string, values []string, statusCode int) {
  if tr.CountsByHeaders[header] == nil {
    tr.CountsByHeaders[header] = newHeaderCounts(header)
  }
  headerCounts := tr.CountsByHeaders[header]
  setTimestamps(headerCounts)
  incrementHeaderCount(headerCounts.Count)
  incrementHeaderCountForStatus(headerCounts.CountsByStatusCodes, statusCode)
  for _, value := range values {
    incrementHeaderCountForValue(headerCounts.CountsByValues, value)
    if headerCounts.CountsByValuesStatusCodes[value] == nil {
      headerCounts.CountsByValuesStatusCodes[value] = map[int]*HeaderCount{}
    }
    incrementHeaderCountForStatus(headerCounts.CountsByValuesStatusCodes[value], statusCode)
  }
}

func (tr *TargetResults) AddResult(result *invocation.InvocationResult,
  trackingHeaders []string, crossTrackingHeaders map[string][]string) {
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
        tr.addHeaderResult(h, values, result.StatusCode)
        tr.processCrossHeadersForHeader(h, values, result.StatusCode, result.Headers, crossTrackingHeaders)
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

func (tr *TargetsResults) AddResult(result *invocation.InvocationResult,
  trackingHeaders []string, crossTrackingHeaders map[string][]string) {
  targetResults, allResults := tr.getTargetResults(result.TargetName)
  targetResults.AddResult(result, trackingHeaders, crossTrackingHeaders)
  if collectAllTargetsResults {
    allResults.AddResult(result, trackingHeaders, crossTrackingHeaders)
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

func (ir *InvocationResults) addResult(result *invocation.InvocationResult,
  trackingHeaders []string, crossTrackingHeaders map[string][]string) {
  ir.init(false)
  ir.lock.RLock()
  targetResults := ir.Results
  ir.lock.RUnlock()
  targetResults.AddResult(result, trackingHeaders, crossTrackingHeaders)
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
  trackingHeaders []string, crossTrackingHeaders map[string][]string,
  invocationResults *InvocationResults, targetResults *TargetResults, allResults *TargetResults) {
  if result != nil {
    result.Headers = util.ToLowerHeaders(result.Headers)
    if collectInvocationResults {
      invocationResults.addResult(result, trackingHeaders, crossTrackingHeaders)
      chanSendInvocationToRegistry <- invocationResults
    }
    if collectTargetsResults {
      targetResults.AddResult(result, trackingHeaders, crossTrackingHeaders)
      chanSendTargetsToRegistry <- targetResults
    }
    if collectAllTargetsResults {
      allResults.AddResult(result, trackingHeaders, crossTrackingHeaders)
      chanSendTargetsToRegistry <- allResults
    }
  }
}

func channelSink(invocationIndex uint32, resultChannel chan *invocation.InvocationResult, doneChannel chan bool,
  trackingHeaders []string, crossTrackingHeaders map[string][]string,
  invocationResults *InvocationResults, targetResults *TargetResults, allResults *TargetResults) {
  done := false
Results:
  for {
    select {
    case done = <-doneChannel:
      break Results
    case result := <-resultChannel:
      resultSink(invocationIndex, result, trackingHeaders, crossTrackingHeaders, invocationResults, targetResults, allResults)
    }
  }
  if done {
  MoreResults:
    for {
      select {
      case result := <-resultChannel:
        if result != nil {
          resultSink(invocationIndex, result, trackingHeaders, crossTrackingHeaders, invocationResults, targetResults, allResults)
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

func buildCrossHeadersInvertedMap(targetResults *TargetResults, crossTrackingHeaders map[string][]string) {
  targetResults.crossHeadersMap = map[string]string{}
  for header, subheaders := range crossTrackingHeaders {
    for _, subheader := range subheaders {
      targetResults.crossHeadersMap[subheader] = header
    }
  }
}

func ResultChannelSinkFactory(target *invocation.InvocationSpec, trackingHeaders []string, crossTrackingHeaders map[string][]string) invocation.ResultSinkFactory {
  targetResults, allResults := targetsResults.getTargetResults(target.Name)
  targetResults.Target = target.Name
  buildCrossHeadersInvertedMap(targetResults, crossTrackingHeaders)
  return func(tracker *invocation.InvocationTracker) invocation.ResultSink {
    invocationResults := invocationsResults.getInvocation(tracker.ID)
    invocationResults.Target = target
    invocationResults.Status = tracker.Status
    startRegistrySender()
    go channelSink(tracker.ID, tracker.ResultChannel, tracker.DoneChannel, trackingHeaders, crossTrackingHeaders, invocationResults, targetResults, allResults)
    return nil
  }
}

func ResultSinkFactory(target *invocation.InvocationSpec, trackingHeaders []string, crossTrackingHeaders map[string][]string) invocation.ResultSinkFactory {
  targetResults, allResults := targetsResults.getTargetResults(target.Name)
  targetResults.Target = target.Name
  return func(tracker *invocation.InvocationTracker) invocation.ResultSink {
    invocationResults := invocationsResults.getInvocation(tracker.ID)
    invocationResults.Target = target
    invocationResults.Status = tracker.Status
    startRegistrySender()
    return func(result *invocation.InvocationResult) {
      resultSink(tracker.ID, result, trackingHeaders, crossTrackingHeaders, invocationResults, targetResults, allResults)
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
  return util.ToJSON(targetsResults.Results)
}

func GetInvocationResultsJSON() string {
  invocationsResults.lock.RLock()
  defer invocationsResults.lock.RUnlock()
  return util.ToJSON(invocationsResults.Results)
}

func addDeltaCrossHeaders(result, delta map[string]*HeaderCounts) {
  for ch, chCounts := range delta {
    if result[ch] == nil {
      result[ch] = newHeaderCounts(ch)
    }
    addDeltaHeaderCounts(result[ch], chCounts)
  }
}

func addDeltaCrossHeadersValues(result, delta map[string]map[string]*HeaderCounts) {
  for value, chCounts := range delta {
    if result[value] == nil {
      result[value] = map[string]*HeaderCounts{}
    }
    for ch, chCounts := range chCounts {
      if result[value][ch] == nil {
        result[value][ch] = newHeaderCounts(ch)
      }
      addDeltaHeaderCounts(result[value][ch], chCounts)
    }
  }
}

func addDeltaHeaderCounts(result, delta *HeaderCounts) {
  setTimestamps(result)
  incrementHeaderCount(result.Count, delta.Count)
  if result.CountsByValues == nil {
    result.CountsByValues = map[string]*HeaderCount{}
  }
  for value, count := range delta.CountsByValues {
    incrementHeaderCountForValue(result.CountsByValues, value, count)
  }
  if result.CountsByStatusCodes == nil {
    result.CountsByStatusCodes = map[int]*HeaderCount{}
  }
  for statucCode, count := range delta.CountsByStatusCodes {
    incrementHeaderCountForStatus(result.CountsByStatusCodes, statucCode, count)
    for value, count := range delta.CountsByValues {
      if result.CountsByValuesStatusCodes[value] == nil {
        result.CountsByValuesStatusCodes[value] = map[int]*HeaderCount{}
      }
      incrementHeaderCountForStatus(result.CountsByValuesStatusCodes[value], statucCode, count)
    }
  }
  if delta.CrossHeaders != nil {
    if result.CrossHeaders == nil {
      result.CrossHeaders = map[string]*HeaderCounts{}
    }
    addDeltaCrossHeaders(result.CrossHeaders, delta.CrossHeaders)
  }
  if delta.CrossHeadersByValues != nil {
    if result.CrossHeadersByValues == nil {
      result.CrossHeadersByValues = map[string]map[string]*HeaderCounts{}
    }
    addDeltaCrossHeadersValues(result.CrossHeadersByValues, delta.CrossHeadersByValues)
  }
}

func processDeltaHeaderCounts(result, delta map[string]*HeaderCounts) {
  for header, counts := range delta {
    if result[header] == nil {
      result[header] = newHeaderCounts(header)
    }
    addDeltaHeaderCounts(result[header], counts)
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
    processDeltaHeaderCounts(results.CountsByHeaders, delta.CountsByHeaders)
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
    if tsr.CountsByTargetCrossHeaders[tr.Target] == nil {
      tsr.CountsByTargetCrossHeaders[tr.Target] = map[string]*HeaderCounts{}
    }
    processDeltaHeaderCounts(tsr.CountsByCrossHeaders, tr.CountsByHeaders)
    processDeltaHeaderCounts(tsr.CountsByTargetCrossHeaders[tr.Target], tr.CountsByHeaders)
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
  tsr.CountsByCrossHeaders = map[string]*HeaderCounts{}
  tsr.CountsByTargetCrossHeaders = map[string]map[string]*HeaderCounts{}
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

func registrySender(id int) {
  stopSender := false
  for {
  RegistrySend:
    for {
      if len(chanSendTargetsToRegistry) > 50 {
        log.Printf("registrySender[%d]: chanSendTargetsToRegistry length %d\n", id, len(chanSendTargetsToRegistry))
      }
      if len(chanSendInvocationToRegistry) > 50 {
        log.Printf("registrySender[%d]: chanSendInvocationToRegistry length %d\n", id, len(chanSendInvocationToRegistry))
      }
      if len(chanLockInvocationInRegistry) > 50 {
        log.Printf("registrySender[%d]: chanLockInvocationInRegistry length %d\n", id, len(chanLockInvocationInRegistry))
      }
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
    for i := 1; i < 10; i++ {
      go registrySender(i)
    }
  }
}

func StopRegistrySender() {
  for i := 1; i < 10; i++ {
    stopRegistrySender <- true
  }
}

func EnableAllTargetResults(enable bool) {
  collectAllTargetsResults = enable
}

func EnableInvocationResults(enable bool) {
  collectInvocationResults = enable
}
