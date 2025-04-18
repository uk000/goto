/**
 * Copyright 2025 uk
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package results

import (
	"encoding/json"
	"fmt"
	. "goto/pkg/constants"
	"goto/pkg/global"
	"goto/pkg/invocation"
	"goto/pkg/transport"
	"goto/pkg/util"
	"log"
	"reflect"
	"strings"
	"sync"
	"time"
)

type CountInfo struct {
	Count             int       `json:"count"`
	ClientStreamCount int       `json:"clientStreamCount"`
	ServerStreamCount int       `json:"serverStreamCount"`
	Retries           int       `json:"retries"`
	FirstResultAt     time.Time `json:"firstResultAt,omitempty"`
	LastResultAt      time.Time `json:"lastResultAt,omitempty"`
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
	ClientStreamCount            int                      `json:"clientStreamCount"`
	ServerStreamCount            int                      `json:"serverStreamCount"`
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
	lock                         *sync.RWMutex
}

type SummaryCounts struct {
	Count             int
	ClientStreamCount int
	ServerStreamCount int
	ByStatusCodes     map[interface{}]int
	ByTimeBuckets     map[interface{}]int
}

type SummaryResult map[interface{}]*SummaryCounts

type AggregateResultsView struct {
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

type ClientAggregateResultsView struct {
	AggregateResultsView
	InvocationCount int       `json:"invocationCount,omitempty"`
	FirstResultAt   time.Time `json:"firstResultAt,omitempty"`
	LastResultAt    time.Time `json:"lastResultAt,omitempty"`
}

type ClientTargetsAggregateResultsView struct {
	AggregateResultsView
	ResultsByTargets map[string]*ClientAggregateResultsView `json:"byTargets,omitempty"`
}

type TargetsResults struct {
	Results map[string]*TargetResults `json:"results"`
	lock    sync.RWMutex
}

type InvocationResults struct {
	InvocationIndex     uint32                         `json:"invocationIndex"`
	Target              *invocation.InvocationSpec     `json:"target"`
	Status              *invocation.InvocationStatus   `json:"status"`
	Results             []*invocation.InvocationResult `json:"results"`
	Finished            bool                           `json:"finished"`
	pendingRegistrySend bool
	lock                *sync.RWMutex
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
	registryClient               = transport.CreateDefaultHTTPClient("ResultsRegistrySender", true, false, nil)
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
	data["clientStreamCount"] = s.ClientStreamCount
	data["serverStreamCount"] = s.ServerStreamCount
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

func (tr *TargetResults) unsafeInit(reset bool) {
	if tr.CountsByStatus == nil || reset {
		tr.InvocationCount = 0
		tr.ClientStreamCount = 0
		tr.ServerStreamCount = 0
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

func NewTargetResults(target string, trackingHeaders []string, crossTrackingHeaders map[string][]string, trackingTimeBuckets [][]int) *TargetResults {
	tr := &TargetResults{Target: target}
	tr.unsafeInit(true)
	tr.trackingHeaders = trackingHeaders
	tr.crossTrackingHeaders = crossTrackingHeaders
	tr.crossHeadersMap = util.BuildCrossHeadersMap(crossTrackingHeaders)
	tr.trackingTimeBuckets = trackingTimeBuckets
	return tr
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

func (c *CountInfo) increment(retries, csCount, ssCount int, ts time.Time) {
	c.Count++
	c.ClientStreamCount += csCount
	c.ServerStreamCount += ssCount
	c.Retries += retries
	if c.FirstResultAt.IsZero() || ts.Before(c.FirstResultAt) {
		c.FirstResultAt = ts
	}
	c.LastResultAt = ts
}

func (c *CountInfo) incrementBy(retries int, by *CountInfo) {
	c.Count += by.Count
	c.ClientStreamCount += by.ClientStreamCount
	c.ServerStreamCount += by.ServerStreamCount
	c.Retries += by.Retries
	if c.FirstResultAt.IsZero() || by.FirstResultAt.Before(c.FirstResultAt) {
		c.FirstResultAt = by.FirstResultAt
	}
	if c.LastResultAt.IsZero() || c.LastResultAt.Before(by.LastResultAt) {
		c.LastResultAt = by.LastResultAt
	}
}

func incrementHeaderCount(m map[int]*CountInfo, statusCode, retries, csCount, ssCount int, ts time.Time) {
	if m[statusCode] == nil {
		m[statusCode] = &CountInfo{}
	}
	m[statusCode].increment(retries, csCount, ssCount, ts)
}

func incrementHeaderCountBy(m map[int]*CountInfo, statusCode, retries int, by *CountInfo) {
	if m[statusCode] == nil {
		m[statusCode] = &CountInfo{}
	}
	m[statusCode].incrementBy(retries, by)
}

func incrementHeaderValueCount(m map[string]*CountInfo, value string, retries, csCount, ssCount int, ts time.Time) {
	if m[value] == nil {
		m[value] = &CountInfo{}
	}
	m[value].increment(retries, csCount, ssCount, ts)
}

func incrementHeaderValueCountBy(m map[string]*CountInfo, value string, retries int, by *CountInfo) {
	if m[value] == nil {
		m[value] = &CountInfo{}
	}
	m[value].incrementBy(retries, by)
}

func (tr *TargetResults) processCrossHeadersForHeader(header string, values []string, statusCode, retries, csCount, ssCount int, ts time.Time, allHeaders map[string][]string) {
	if crossHeaders := tr.crossTrackingHeaders[header]; crossHeaders != nil {
		processSubCrossHeadersForHeader(header, values, statusCode, retries, csCount, ssCount, ts, tr.CountsByHeaders[header], crossHeaders, allHeaders)
	}
}

func processSubCrossHeadersForHeader(header string, values []string, statusCode, retries, csCount, ssCount int, ts time.Time,
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
		crossHeaderCounts.increment(retries, csCount, ssCount, ts)
		incrementHeaderCount(crossHeaderCounts.CountsByStatusCodes, statusCode, retries, csCount, ssCount, ts)
		for _, crossValue := range crossValues {
			incrementHeaderValueCount(crossHeaderCounts.CountsByValues, crossValue, retries, csCount, ssCount, ts)
			if crossHeaderCounts.CountsByValuesStatusCodes[crossValue] == nil {
				crossHeaderCounts.CountsByValuesStatusCodes[crossValue] = map[int]*CountInfo{}
			}
			incrementHeaderCount(crossHeaderCounts.CountsByValuesStatusCodes[crossValue], statusCode, retries, csCount, ssCount, ts)
		}
		processSubCrossHeaders := i < len(crossHeaders)-1
		subCrossHeaders := crossHeaders[i+1:]
		if processSubCrossHeaders {
			processSubCrossHeadersForHeader(crossHeader, crossValues, statusCode, retries, csCount, ssCount, ts, crossHeaderCounts, subCrossHeaders, allHeaders)
		}
		for _, value := range values {
			if headerCounts.CrossHeadersByValues[value][crossHeader] == nil {
				headerCounts.CrossHeadersByValues[value][crossHeader] = newHeaderCounts(crossHeader)
			}
			crossHeaderCountsByValue := headerCounts.CrossHeadersByValues[value][crossHeader]
			crossHeaderCountsByValue.setTimestamps(headerCounts.LastResultAt)
			crossHeaderCountsByValue.increment(retries, csCount, ssCount, ts)
			incrementHeaderCount(crossHeaderCountsByValue.CountsByStatusCodes, statusCode, retries, csCount, ssCount, ts)
			for _, crossValue := range crossValues {
				incrementHeaderValueCount(crossHeaderCountsByValue.CountsByValues, crossValue, retries, csCount, ssCount, ts)
				if crossHeaderCountsByValue.CountsByValuesStatusCodes[crossValue] == nil {
					crossHeaderCountsByValue.CountsByValuesStatusCodes[crossValue] = map[int]*CountInfo{}
				}
				incrementHeaderCount(crossHeaderCountsByValue.CountsByValuesStatusCodes[crossValue], statusCode, retries, csCount, ssCount, ts)
			}
			if processSubCrossHeaders {
				processSubCrossHeadersForHeader(crossHeader, crossValues, statusCode, retries, csCount, ssCount, ts, crossHeaderCountsByValue, subCrossHeaders, allHeaders)
			}
		}
	}
}

func (tr *TargetResults) addHeaderResult(header string, values []string, statusCode, retries, csCount, ssCount int, ts time.Time) {
	if tr.CountsByHeaders[header] == nil {
		tr.CountsByHeaders[header] = newHeaderCounts(header)
	}
	headerCounts := tr.CountsByHeaders[header]
	headerCounts.setTimestamps(ts)
	headerCounts.increment(retries, csCount, ssCount, ts)
	incrementHeaderCount(headerCounts.CountsByStatusCodes, statusCode, retries, csCount, ssCount, ts)
	for _, value := range values {
		incrementHeaderValueCount(headerCounts.CountsByValues, value, retries, csCount, ssCount, ts)
		if headerCounts.CountsByValuesStatusCodes[value] == nil {
			headerCounts.CountsByValuesStatusCodes[value] = map[int]*CountInfo{}
		}
		incrementHeaderCount(headerCounts.CountsByValuesStatusCodes[value], statusCode, retries, csCount, ssCount, ts)
	}
}

func addKeyResultCounts(counts map[interface{}]*KeyResultCounts, key interface{}, statusCode, retries, csCount, ssCount int, ts time.Time, withStatusCodes, withTimeBuckets bool) {
	if counts[key] == nil {
		counts[key] = newKeyResultCounts(withStatusCodes, withTimeBuckets)
	}
	resultCount := counts[key]
	resultCount.increment(retries, csCount, ssCount, ts)
	if withStatusCodes {
		if resultCount.ByStatusCodes[statusCode] == nil {
			resultCount.ByStatusCodes[statusCode] = &KeyResultCounts{}
		}
		resultCount.ByStatusCodes[statusCode].increment(retries, csCount, ssCount, ts)
	}
}

func (tr *TargetResults) addTimeBucketResult(tb []int, ir *invocation.InvocationResult) {
	bucket := "[]"
	if tb != nil {
		bucket = fmt.Sprint(tb)
	}
	addKeyResultCounts(tr.CountsByTimeBuckets, bucket, ir.Response.StatusCode, ir.Retries, ir.Response.ClientStreamCount, ir.Response.ServerStreamCount, ir.Request.LastRequestAt, true, false)

	if uriCount := tr.CountsByURIs[ir.Request.URI]; uriCount != nil {
		addKeyResultCounts(uriCount.ByTimeBuckets, bucket, ir.Response.StatusCode, ir.Retries, ir.Response.ClientStreamCount, ir.Response.ServerStreamCount, ir.Request.LastRequestAt, true, false)
	}

	if rpc := tr.CountsByRequestPayloadSizes[ir.Request.PayloadSize]; rpc != nil {
		addKeyResultCounts(rpc.ByTimeBuckets, bucket, ir.Response.StatusCode, ir.Retries, ir.Response.ClientStreamCount, ir.Response.ServerStreamCount, ir.Request.LastRequestAt, true, false)
	}

	if rpc := tr.CountsByResponsePayloadSizes[ir.Request.PayloadSize]; rpc != nil {
		addKeyResultCounts(rpc.ByTimeBuckets, bucket, ir.Response.StatusCode, ir.Retries, ir.Response.ClientStreamCount, ir.Response.ServerStreamCount, ir.Request.LastRequestAt, true, false)
	}

	if rc := tr.CountsByRetries[ir.Retries]; rc != nil {
		addKeyResultCounts(rc.ByTimeBuckets, bucket, ir.Response.StatusCode, ir.Retries, ir.Response.ClientStreamCount, ir.Response.ServerStreamCount, ir.Request.LastRequestAt, true, false)
	}

	if rrc := tr.CountsByRetryReasons[ir.LastRetryReason]; rrc != nil {
		addKeyResultCounts(rrc.ByTimeBuckets, bucket, ir.Response.StatusCode, ir.Retries, ir.Response.ClientStreamCount, ir.Response.ServerStreamCount, ir.Request.LastRequestAt, true, false)
	}

	for e := range ir.Errors {
		if ec := tr.CountsByErrors[e]; ec != nil {
			addKeyResultCounts(ec.ByTimeBuckets, bucket, ir.Response.StatusCode, ir.Retries, ir.Response.ClientStreamCount, ir.Response.ServerStreamCount, ir.Request.LastRequestAt, true, false)
		}
	}

	if sc := tr.CountsByStatusCodes[ir.Response.StatusCode]; sc != nil {
		addKeyResultCounts(sc.ByTimeBuckets, bucket, ir.Response.StatusCode, ir.Retries, ir.Response.ClientStreamCount, ir.Response.ServerStreamCount, ir.Request.LastRequestAt, false, false)
	}
}

func (tr *TargetResults) addResult(ir *invocation.InvocationResult) {
	tr.lock.Lock()
	defer tr.lock.Unlock()

	tr.unsafeInit(false)
	tr.InvocationCount++
	finishedAt := ir.Request.LastRequestAt
	if tr.FirstResultAt.IsZero() || finishedAt.Before(tr.FirstResultAt) {
		tr.FirstResultAt = finishedAt
	}
	tr.LastResultAt = finishedAt
	if ir.Retries > 0 {
		tr.RetriedInvocationCounts++
		addKeyResultCounts(tr.CountsByRetries, ir.Retries, ir.Response.StatusCode, ir.Retries, ir.Response.ClientStreamCount, ir.Response.ServerStreamCount, ir.Request.LastRequestAt, true, true)
		if ir.LastRetryReason != "" {
			addKeyResultCounts(tr.CountsByRetryReasons, ir.LastRetryReason, ir.Response.StatusCode, ir.Retries, ir.Response.ClientStreamCount, ir.Response.ServerStreamCount, ir.Request.LastRequestAt, true, true)
		}
	}
	tr.ClientStreamCount += ir.Response.ClientStreamCount
	tr.ServerStreamCount += ir.Response.ServerStreamCount
	tr.CountsByStatus[ir.Response.Status]++
	addKeyResultCounts(tr.CountsByStatusCodes, ir.Response.StatusCode, ir.Response.StatusCode, ir.Retries, ir.Response.ClientStreamCount, ir.Response.ServerStreamCount, ir.Request.LastRequestAt, false, true)

	uri := strings.ToLower(ir.Request.URI)
	addKeyResultCounts(tr.CountsByURIs, uri, ir.Response.StatusCode, ir.Retries, ir.Response.ClientStreamCount, ir.Response.ServerStreamCount, ir.Request.LastRequestAt, true, true)

	for _, h := range tr.trackingHeaders {
		for rh, values := range ir.Response.Headers {
			if strings.EqualFold(h, rh) {
				tr.addHeaderResult(h, values, ir.Response.StatusCode, ir.Retries, ir.Response.ClientStreamCount, ir.Response.ServerStreamCount, finishedAt)
				tr.processCrossHeadersForHeader(h, values, ir.Response.StatusCode, ir.Retries, ir.Response.ClientStreamCount, ir.Response.ServerStreamCount, ir.Request.LastRequestAt, ir.Response.Headers)
			}
		}
	}
	if ir.Request.PayloadSize > 0 {
		addKeyResultCounts(tr.CountsByRequestPayloadSizes, ir.Request.PayloadSize, ir.Response.StatusCode, ir.Retries, ir.Response.ClientStreamCount, ir.Response.ServerStreamCount, ir.Request.LastRequestAt, true, true)
	}
	if ir.Response.PayloadSize > 0 {
		addKeyResultCounts(tr.CountsByResponsePayloadSizes, ir.Response.PayloadSize, ir.Response.StatusCode, ir.Retries, ir.Response.ClientStreamCount, ir.Response.ServerStreamCount, ir.Request.LastRequestAt, true, true)
	}
	for e := range ir.Errors {
		addKeyResultCounts(tr.CountsByErrors, e, ir.Response.StatusCode, ir.Retries, ir.Response.ClientStreamCount, ir.Response.ServerStreamCount, ir.Request.LastRequestAt, true, true)
	}
	if len(tr.trackingTimeBuckets) > 0 {
		addedToTimeBucket := false
		took := int(ir.TookNanos.Nanoseconds()) / 1000000
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

func (ir *InvocationResults) unsafeInit(reset bool) {
	if reset || ir.Results == nil {
		ir.Finished = false
		ir.InvocationIndex = 0
		ir.Results = []*invocation.InvocationResult{}
	}
}

func (ir *InvocationResults) addResult(result *invocation.InvocationResult) {
	ir.lock.Lock()
	defer ir.lock.Unlock()
	ir.unsafeInit(false)
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
		invocationResults = &InvocationResults{lock: &ir.lock}
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
		result.Response.Headers = util.ToLowerHeaders(result.Response.Headers)
		if collectInvocationResults {
			invocationResults.addResult(result)
			chanSendInvocationToRegistry <- invocationResults
		}
		if collectTargetsResults {
			targetResults.addResult(result)
			chanSendTargetsToRegistry <- targetResults
		}
		if collectAllTargetsResults {
			allResults.addResult(result)
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
		tracker.Channels.Lock.RLock()
		go channelSink(tracker.ID, tracker.Channels.ResultChannel, tracker.Channels.DoneChannel, invocationResults, targetResults, allResults)
		tracker.Channels.Lock.RUnlock()
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
		return util.ToJSONText(targetsResults.Results)
	}
	return "{}"
}

func GetInvocationResultsJSON() string {
	invocationsResults.lock.RLock()
	defer invocationsResults.lock.RUnlock()
	return util.ToJSONText(invocationsResults.Results)
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
	results.ClientStreamCount += delta.ClientStreamCount
	results.ServerStreamCount += delta.ServerStreamCount
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
		counts.ClientStreamCount += val.ClientStreamCount
		counts.ServerStreamCount += val.ServerStreamCount
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

func (sr *AggregateResultsView) addTargetResult(tr *TargetResults, detailed bool) {
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

func (car *ClientAggregateResultsView) addTargetResult(tr *TargetResults, detailed bool) {
	car.InvocationCount += tr.InvocationCount
	if car.FirstResultAt.IsZero() || tr.FirstResultAt.Before(car.FirstResultAt) {
		car.FirstResultAt = tr.FirstResultAt
	}
	if car.LastResultAt.IsZero() || car.LastResultAt.Before(tr.LastResultAt) {
		car.LastResultAt = tr.LastResultAt
	}
	car.AggregateResultsView.addTargetResult(tr, detailed)
}

func (ctar *ClientTargetsAggregateResultsView) AddTargetResult(tr *TargetResults, detailed bool) {
	fmt.Printf("TargetsSummaryResults.AddTargetResult: Target [%s] first response [%s] last response [%s]\n",
		tr.Target, tr.FirstResultAt.UTC().String(), tr.LastResultAt.UTC().String())
	ctar.AggregateResultsView.addTargetResult(tr, detailed)
	if ctar.ResultsByTargets[tr.Target] == nil {
		ctar.ResultsByTargets[tr.Target] = &ClientAggregateResultsView{}
		ctar.ResultsByTargets[tr.Target].init()
	}
	ctar.ResultsByTargets[tr.Target].addTargetResult(tr, detailed)
}

func (ar *AggregateResultsView) init() {
	ar.CountsByStatusCodes = SummaryResult{}
	ar.CountsByHeaders = map[string]int{}
	ar.CountsByHeaderValues = map[string]map[string]int{}
	ar.CountsByURIs = SummaryResult{}
	ar.CountsByRequestPayloadSizes = SummaryResult{}
	ar.CountsByResponsePayloadSizes = SummaryResult{}
	ar.CountsByRetries = SummaryResult{}
	ar.CountsByRetryReasons = SummaryResult{}
	ar.CountsByErrors = SummaryResult{}
	ar.CountsByTimeBuckets = SummaryResult{}
}

func NewClientTargetsAggregateResults() *ClientTargetsAggregateResultsView {
	ctar := &ClientTargetsAggregateResultsView{}
	ctar.AggregateResultsView.init()
	ctar.ResultsByTargets = map[string]*ClientAggregateResultsView{}
	return ctar
}

func lockInvocationRegistryLocker(invocationIndex uint32) {
	if global.UseLocker && global.RegistryURL != "" {
		url := fmt.Sprintf("%s/registry/peers/%s/%s/locker/lock/%s_%d", global.RegistryURL,
			global.PeerName, global.PeerAddress, LockerClientKey, invocationIndex)
		if resp, err := registryClient.HTTP().Post(url, ContentTypeJSON, nil); err == nil {
			util.CloseResponse(resp)
		}
	}
}

func sendResultToRegistry(keys []string, data interface{}) {
	if global.UseLocker && global.RegistryURL != "" {
		url := fmt.Sprintf("%s/registry/peers/%s/%s/locker/store/%s", global.RegistryURL, global.PeerName, global.PeerAddress, strings.Join(keys, ","))
		if resp, err := registryClient.HTTP().Post(url, ContentTypeJSON,
			strings.NewReader(util.ToJSONText(data))); err == nil {
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
		hasResultsToSend := false
		collectedTargetsResults := map[string]*TargetResults{}
		collectedInvocationResults := map[uint32]*InvocationResults{}
		select {
		case tr := <-chanSendTargetsToRegistry:
			tr.lock.Lock()
			if !tr.pendingRegistrySend {
				hasResultsToSend = true
				tr.pendingRegistrySend = true
				collectedTargetsResults[tr.Target] = tr
			}
			tr.lock.Unlock()
		case ir := <-chanSendInvocationToRegistry:
			ir.lock.RLock()
			if !ir.pendingRegistrySend {
				hasResultsToSend = true
				ir.pendingRegistrySend = true
				collectedInvocationResults[ir.InvocationIndex] = ir
			}
			ir.lock.RUnlock()
		case <-stopRegistrySender:
			break RegistrySend
		}
		if hasResultsToSend {
			hasMoreResults := false
			for i := 0; i < SendDelayMax; i++ {
			MoreResults:
				for {
					select {
					case tr := <-chanSendTargetsToRegistry:
						tr.lock.Lock()
						if !tr.pendingRegistrySend || collectedTargetsResults[tr.Target] != nil {
							tr.pendingRegistrySend = true
							collectedTargetsResults[tr.Target] = tr
							hasMoreResults = true
						}
						tr.lock.Unlock()
					case ir := <-chanSendInvocationToRegistry:
						ir.lock.RLock()
						if !ir.pendingRegistrySend || collectedInvocationResults[ir.InvocationIndex] != nil {
							ir.pendingRegistrySend = true
							collectedInvocationResults[ir.InvocationIndex] = ir
							hasMoreResults = true
						}
						ir.lock.RUnlock()
					default:
						break MoreResults
					}
				}
				if i < SendDelayMin-1 || (hasMoreResults && i < SendDelayMax-1) {
					time.Sleep(time.Second)
				}
			}
			for target, tr := range collectedTargetsResults {
				tr.lock.Lock()
				sendResultToRegistry([]string{LockerClientKey, target}, tr)
				tr.pendingRegistrySend = false
				tr.lock.Unlock()
			}
			for index, ir := range collectedInvocationResults {
				ir.lock.Lock()
				sendResultToRegistry([]string{LockerClientKey, LockerInvocationsKey,
					fmt.Sprint(index)}, ir)
				ir.pendingRegistrySend = false
				ir.lock.Unlock()
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
