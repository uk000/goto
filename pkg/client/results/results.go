/**
 * Copyright 2026 uk
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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	CountsByStatusCodes       map[string]*CountInfo               `json:"countsByStatusCodes,omitempty"`
	CountsByValuesStatusCodes map[string]map[string]*CountInfo    `json:"countsByValuesStatusCodes,omitempty"`
	CrossHeaders              map[string]*HeaderCounts            `json:"crossHeaders,omitempty"`
	CrossHeadersByValues      map[string]map[string]*HeaderCounts `json:"crossHeadersByValues,omitempty"`
}

type KeyResultCounts struct {
	CountInfo
	ByStatusCodes KeyResult `json:"byStatusCodes,omitempty"`
	ByTimeBuckets KeyResult `json:"byTimeBuckets,omitempty"`
}

type KeyResult map[string]*KeyResultCounts

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
	lock                         sync.RWMutex
}

type SummaryCounts struct {
	Count             int
	ClientStreamCount int
	ServerStreamCount int
	ByStatusCodes     map[string]int
	ByTimeBuckets     map[string]int
}

type SummaryResult map[string]*SummaryCounts

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
	registryClient               = transport.CreateDefaultHTTPClient(global.Self.ServerPort, "ResultsRegistrySender", true, false, false, "", nil)
	sendingToRegistry            bool
	registrySendLock             sync.Mutex
	// registrySenders is for waiting until the senders have stopped
	registrySenders sync.WaitGroup
	// the flags are read by the sinks of the invocations that are running, while the API changes them
	collectTargetsResults    atomic.Bool
	collectAllTargetsResults atomic.Bool
	collectInvocationResults atomic.Bool
	// registrySendInterval is the pause between the rounds in which a sender collects more results to send together.
	registrySendInterval = time.Second
	_                    = global.OnShutdown(StopRegistrySender)
)

// init turns on collection of the per-target summary results by default.
func init() {
	collectTargetsResults.Store(true)
}

// MarshalJSON serializes the key results as a JSON object with string keys.
func (kr KeyResult) MarshalJSON() ([]byte, error) {
	data := map[string]interface{}{}
	for k, v := range kr {
		data[fmt.Sprint(k)] = v
	}
	return json.Marshal(data)
}

// UnmarshalJSON loads key results from JSON, turning empty status code and time bucket maps into nil.
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

// MarshalJSON emits just the count when there are no status code or time bucket breakdowns, otherwise the full counts object.
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

// MarshalJSON serializes the summary results as a JSON object with string keys.
func (s SummaryResult) MarshalJSON() ([]byte, error) {
	data := map[string]interface{}{}
	for k, v := range s {
		data[fmt.Sprint(k)] = v
	}
	return json.Marshal(data)
}

// unsafeInit allocates the count maps and zeroes the totals when they are unset or reset is true; the caller must hold the lock.
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

// unsafeInitMissing allocates only the count maps that are nil, keeping existing counts (e.g. after loading from JSON).
func (tr *TargetResults) unsafeInitMissing() {
	if tr.CountsByStatus == nil {
		tr.CountsByStatus = map[string]int{}
	}
	if tr.CountsByHeaders == nil {
		tr.CountsByHeaders = map[string]*HeaderCounts{}
	}
	if tr.CountsByStatusCodes == nil {
		tr.CountsByStatusCodes = KeyResult{}
	}
	if tr.CountsByURIs == nil {
		tr.CountsByURIs = KeyResult{}
	}
	if tr.CountsByRequestPayloadSizes == nil {
		tr.CountsByRequestPayloadSizes = KeyResult{}
	}
	if tr.CountsByResponsePayloadSizes == nil {
		tr.CountsByResponsePayloadSizes = KeyResult{}
	}
	if tr.CountsByRetries == nil {
		tr.CountsByRetries = KeyResult{}
	}
	if tr.CountsByRetryReasons == nil {
		tr.CountsByRetryReasons = KeyResult{}
	}
	if tr.CountsByTimeBuckets == nil {
		tr.CountsByTimeBuckets = KeyResult{}
	}
	if tr.CountsByErrors == nil {
		tr.CountsByErrors = KeyResult{}
	}
}

// NewTargetResults creates empty results for a target along with its tracking headers, cross headers and time buckets.
func NewTargetResults(target string, trackingHeaders []string, crossTrackingHeaders map[string][]string, trackingTimeBuckets [][]int) *TargetResults {
	tr := &TargetResults{Target: target}
	tr.unsafeInit(true)
	tr.trackingHeaders = trackingHeaders
	tr.crossTrackingHeaders = crossTrackingHeaders
	tr.crossHeadersMap = util.BuildCrossHeadersMap(crossTrackingHeaders)
	tr.trackingTimeBuckets = trackingTimeBuckets
	return tr
}

// newHeaderCounts creates the counts of a header with all its maps initialized.
func newHeaderCounts(header string) *HeaderCounts {
	return &HeaderCounts{
		Header:                    header,
		CountsByValues:            map[string]*CountInfo{},
		CountsByStatusCodes:       map[string]*CountInfo{},
		CountsByValuesStatusCodes: map[string]map[string]*CountInfo{},
		CrossHeaders:              map[string]*HeaderCounts{},
		CrossHeadersByValues:      map[string]map[string]*HeaderCounts{},
	}
}

// newKeyResultCounts creates key counts, allocating the status code and time bucket maps only when asked.
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

// setTimestamps moves the first result time back to ts if earlier and sets the last result time to ts.
func (c *HeaderCounts) setTimestamps(ts time.Time) {
	if c.FirstResultAt.IsZero() || ts.Before(c.FirstResultAt) {
		c.FirstResultAt = ts
	}
	c.LastResultAt = ts
}

// increment counts one more result with its retries and stream counts, and widens the first/last result timestamps.
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

// incrementBy adds another CountInfo's counts, merging its timestamps while ignoring unset (zero) ones.
func (c *CountInfo) incrementBy(retries int, by *CountInfo) {
	c.Count += by.Count
	c.ClientStreamCount += by.ClientStreamCount
	c.ServerStreamCount += by.ServerStreamCount
	c.Retries += by.Retries
	// the counts that have no timestamps (nothing was counted in them) must not reset the timestamps
	if !by.FirstResultAt.IsZero() && (c.FirstResultAt.IsZero() || by.FirstResultAt.Before(c.FirstResultAt)) {
		c.FirstResultAt = by.FirstResultAt
	}
	if !by.LastResultAt.IsZero() && (c.LastResultAt.IsZero() || c.LastResultAt.Before(by.LastResultAt)) {
		c.LastResultAt = by.LastResultAt
	}
}

// incrementHeaderCount counts a result under the status code in m, creating its entry if missing.
func incrementHeaderCount(m map[string]*CountInfo, statusCode string, retries, csCount, ssCount int, ts time.Time) {
	if m[statusCode] == nil {
		m[statusCode] = &CountInfo{}
	}
	m[statusCode].increment(retries, csCount, ssCount, ts)
}

// incrementHeaderCountBy adds the given counts under the status code in m, creating its entry if missing.
func incrementHeaderCountBy(m map[string]*CountInfo, statusCode string, retries int, by *CountInfo) {
	if m[statusCode] == nil {
		m[statusCode] = &CountInfo{}
	}
	m[statusCode].incrementBy(retries, by)
}

// incrementHeaderValueCount counts a result under the header value in m, creating its entry if missing.
func incrementHeaderValueCount(m map[string]*CountInfo, value string, retries, csCount, ssCount int, ts time.Time) {
	if m[value] == nil {
		m[value] = &CountInfo{}
	}
	m[value].increment(retries, csCount, ssCount, ts)
}

// incrementHeaderValueCountBy adds the given counts under the header value in m, creating its entry if missing.
func incrementHeaderValueCountBy(m map[string]*CountInfo, value string, retries int, by *CountInfo) {
	if m[value] == nil {
		m[value] = &CountInfo{}
	}
	m[value].incrementBy(retries, by)
}

// processCrossHeadersForHeader counts the result under the cross headers configured for the header, if any.
func (tr *TargetResults) processCrossHeadersForHeader(header string, values []string, statusCode string, retries, csCount, ssCount int, ts time.Time, allHeaders map[string][]string) {
	if crossHeaders := tr.crossTrackingHeaders[header]; crossHeaders != nil {
		processSubCrossHeadersForHeader(header, values, statusCode, retries, csCount, ssCount, ts, tr.CountsByHeaders[header], crossHeaders, allHeaders)
	}
}

// processSubCrossHeadersForHeader recursively counts the values of each cross header found in the response, overall and per value of the parent header.
func processSubCrossHeadersForHeader(header string, values []string, statusCode string, retries, csCount, ssCount int, ts time.Time,
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
				crossHeaderCounts.CountsByValuesStatusCodes[crossValue] = map[string]*CountInfo{}
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
					crossHeaderCountsByValue.CountsByValuesStatusCodes[crossValue] = map[string]*CountInfo{}
				}
				incrementHeaderCount(crossHeaderCountsByValue.CountsByValuesStatusCodes[crossValue], statusCode, retries, csCount, ssCount, ts)
			}
			if processSubCrossHeaders {
				processSubCrossHeadersForHeader(crossHeader, crossValues, statusCode, retries, csCount, ssCount, ts, crossHeaderCountsByValue, subCrossHeaders, allHeaders)
			}
		}
	}
}

// addHeaderResult counts a result for a tracked header overall, by status code, by each of its values, and by value and status code.
func (tr *TargetResults) addHeaderResult(header string, values []string, statusCode string, retries, csCount, ssCount int, ts time.Time) {
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
			headerCounts.CountsByValuesStatusCodes[value] = map[string]*CountInfo{}
		}
		incrementHeaderCount(headerCounts.CountsByValuesStatusCodes[value], statusCode, retries, csCount, ssCount, ts)
	}
}

// addKeyResultCounts counts a result under the key, and under its status code too when withStatusCodes is set.
func addKeyResultCounts(counts map[string]*KeyResultCounts, key string, statusCode string, retries, csCount, ssCount int, ts time.Time, withStatusCodes, withTimeBuckets bool) {
	if counts[key] == nil {
		counts[key] = newKeyResultCounts(withStatusCodes, withTimeBuckets)
	}
	resultCount := counts[key]
	resultCount.increment(retries, csCount, ssCount, ts)
	if withStatusCodes {
		if resultCount.ByStatusCodes == nil {
			resultCount.ByStatusCodes = KeyResult{}
		}
		if resultCount.ByStatusCodes[statusCode] == nil {
			resultCount.ByStatusCodes[statusCode] = &KeyResultCounts{}
		}
		resultCount.ByStatusCodes[statusCode].increment(retries, csCount, ssCount, ts)
	}
}

// timeBuckets is the time buckets of the counts, which are missing in the counts that were loaded from JSON, as the
// empty maps are not kept by it.
func (kr *KeyResultCounts) timeBuckets() KeyResult {
	if kr.ByTimeBuckets == nil {
		kr.ByTimeBuckets = KeyResult{}
	}
	return kr.ByTimeBuckets
}

// addTimeBucketResult counts the result under its time bucket (or "[]" if none matched), overall and within the already-tracked URI, size, retry, error and status counts.
func (tr *TargetResults) addTimeBucketResult(tb []int, ir *invocation.InvocationResult) {
	bucket := "[]"
	if tb != nil {
		bucket = fmt.Sprint(tb)
	}
	statusCode := strconv.Itoa(ir.Response.StatusCode)
	addKeyResultCounts(tr.CountsByTimeBuckets, bucket, statusCode, ir.Retries, ir.Response.ClientStreamCount, ir.Response.ServerStreamCount, ir.Request.LastRequestAt, true, false)

	// the URIs are counted in lower case
	if uriCount := tr.CountsByURIs[strings.ToLower(ir.Request.URI)]; uriCount != nil {
		addKeyResultCounts(uriCount.timeBuckets(), bucket, statusCode, ir.Retries, ir.Response.ClientStreamCount, ir.Response.ServerStreamCount, ir.Request.LastRequestAt, true, false)
	}

	if rpc := tr.CountsByRequestPayloadSizes[strconv.Itoa(ir.Request.PayloadSize)]; rpc != nil {
		addKeyResultCounts(rpc.timeBuckets(), bucket, statusCode, ir.Retries, ir.Response.ClientStreamCount, ir.Response.ServerStreamCount, ir.Request.LastRequestAt, true, false)
	}

	if rpc := tr.CountsByResponsePayloadSizes[strconv.Itoa(ir.Response.PayloadSize)]; rpc != nil {
		addKeyResultCounts(rpc.timeBuckets(), bucket, statusCode, ir.Retries, ir.Response.ClientStreamCount, ir.Response.ServerStreamCount, ir.Request.LastRequestAt, true, false)
	}

	if rc := tr.CountsByRetries[strconv.Itoa(ir.Retries)]; rc != nil {
		addKeyResultCounts(rc.timeBuckets(), bucket, statusCode, ir.Retries, ir.Response.ClientStreamCount, ir.Response.ServerStreamCount, ir.Request.LastRequestAt, true, false)
	}

	if rrc := tr.CountsByRetryReasons[ir.LastRetryReason]; rrc != nil {
		addKeyResultCounts(rrc.timeBuckets(), bucket, statusCode, ir.Retries, ir.Response.ClientStreamCount, ir.Response.ServerStreamCount, ir.Request.LastRequestAt, true, false)
	}

	for e := range ir.Errors {
		if ec := tr.CountsByErrors[strconv.Itoa(e)]; ec != nil {
			addKeyResultCounts(ec.timeBuckets(), bucket, statusCode, ir.Retries, ir.Response.ClientStreamCount, ir.Response.ServerStreamCount, ir.Request.LastRequestAt, true, false)
		}
	}

	if sc := tr.CountsByStatusCodes[statusCode]; sc != nil {
		addKeyResultCounts(sc.timeBuckets(), bucket, statusCode, ir.Retries, ir.Response.ClientStreamCount, ir.Response.ServerStreamCount, ir.Request.LastRequestAt, false, false)
	}
}

// addResult records an invocation result into all the target's counters (status, URI, sizes, retries, errors, tracked headers, time buckets) under its lock.
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
	statusCode := strconv.Itoa(ir.Response.StatusCode)
	if ir.Retries > 0 {
		tr.RetriedInvocationCounts++
		addKeyResultCounts(tr.CountsByRetries, strconv.Itoa(ir.Retries), statusCode, ir.Retries, ir.Response.ClientStreamCount, ir.Response.ServerStreamCount, ir.Request.LastRequestAt, true, true)
		if ir.LastRetryReason != "" {
			addKeyResultCounts(tr.CountsByRetryReasons, ir.LastRetryReason, statusCode, ir.Retries, ir.Response.ClientStreamCount, ir.Response.ServerStreamCount, ir.Request.LastRequestAt, true, true)
		}
	}
	tr.ClientStreamCount += ir.Response.ClientStreamCount
	tr.ServerStreamCount += ir.Response.ServerStreamCount
	tr.CountsByStatus[ir.Response.Status]++
	addKeyResultCounts(tr.CountsByStatusCodes, statusCode, statusCode, ir.Retries, ir.Response.ClientStreamCount, ir.Response.ServerStreamCount, ir.Request.LastRequestAt, false, true)

	uri := strings.ToLower(ir.Request.URI)
	addKeyResultCounts(tr.CountsByURIs, uri, statusCode, ir.Retries, ir.Response.ClientStreamCount, ir.Response.ServerStreamCount, ir.Request.LastRequestAt, true, true)

	for _, h := range tr.trackingHeaders {
		for rh, values := range ir.Response.Headers {
			if strings.EqualFold(h, rh) {
				tr.addHeaderResult(h, values, statusCode, ir.Retries, ir.Response.ClientStreamCount, ir.Response.ServerStreamCount, finishedAt)
				tr.processCrossHeadersForHeader(h, values, statusCode, ir.Retries, ir.Response.ClientStreamCount, ir.Response.ServerStreamCount, ir.Request.LastRequestAt, ir.Response.Headers)
			}
		}
	}
	if ir.Request.PayloadSize > 0 {
		addKeyResultCounts(tr.CountsByRequestPayloadSizes, strconv.Itoa(ir.Request.PayloadSize), statusCode, ir.Retries, ir.Response.ClientStreamCount, ir.Response.ServerStreamCount, ir.Request.LastRequestAt, true, true)
	}
	if ir.Response.PayloadSize > 0 {
		addKeyResultCounts(tr.CountsByResponsePayloadSizes, strconv.Itoa(ir.Response.PayloadSize), statusCode, ir.Retries, ir.Response.ClientStreamCount, ir.Response.ServerStreamCount, ir.Request.LastRequestAt, true, true)
	}
	for e := range ir.Errors {
		addKeyResultCounts(tr.CountsByErrors, strconv.Itoa(e), statusCode, ir.Retries, ir.Response.ClientStreamCount, ir.Response.ServerStreamCount, ir.Request.LastRequestAt, true, true)
	}
	if len(tr.trackingTimeBuckets) > 0 {
		addedToTimeBucket := false
		took := int(ir.TookNanos.Nanoseconds()) / 1000000
		for _, tb := range tr.trackingTimeBuckets {
			if took >= tb[0] && (tb[1] == 0 || took <= tb[1]) {
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

// init (re)creates the results map, with the overall results under the empty key, when reset or unset.
func (tr *TargetsResults) init(reset bool) {
	tr.lock.Lock()
	defer tr.lock.Unlock()
	if reset || tr.Results == nil {
		tr.Results = map[string]*TargetResults{}
		tr.Results[""] = &TargetResults{}
		tr.Results[""].unsafeInit(true)
	}
}

// unsafeInitMissing fills in the missing count maps of every target's results; the caller must hold the lock.
func (tr *TargetsResults) unsafeInitMissing() {
	for _, r := range tr.Results {
		r.unsafeInitMissing()
	}
}

// getTargetResults returns the results of the target, created if needed, along with the overall results across all targets.
func (tr *TargetsResults) getTargetResults(target string) (*TargetResults, *TargetResults) {
	tr.init(false)
	tr.lock.Lock()
	if tr.Results[target] == nil {
		tr.Results[target] = &TargetResults{Target: target}
		tr.Results[target].unsafeInit(true)
	}
	targetResults := tr.Results[target]
	allResults := tr.Results[""]
	tr.lock.Unlock()
	return targetResults, allResults
}

// unsafeInit clears the results and finished flag when unset or reset is true; the caller must hold the lock.
func (ir *InvocationResults) unsafeInit(reset bool) {
	if reset || ir.Results == nil {
		ir.Finished = false
		ir.InvocationIndex = 0
		ir.Results = []*invocation.InvocationResult{}
	}
}

// addResult appends an invocation result under the lock.
func (ir *InvocationResults) addResult(result *invocation.InvocationResult) {
	ir.lock.Lock()
	defer ir.lock.Unlock()
	ir.unsafeInit(false)
	ir.Results = append(ir.Results, result)
}

// finish marks the invocation's results as finished.
func (ir *InvocationResults) finish() {
	ir.lock.Lock()
	ir.Finished = true
	ir.lock.Unlock()
}

// init (re)creates the map of invocation results when reset or unset.
func (ir *InvocationsResults) init(reset bool) {
	ir.lock.Lock()
	defer ir.lock.Unlock()
	if reset || ir.Results == nil {
		ir.Results = map[uint32]*InvocationResults{}
	}
}

// getInvocation returns the results of the invocation index, creating them if absent, safely under concurrent callers.
func (ir *InvocationsResults) getInvocation(index uint32) *InvocationResults {
	ir.init(false)
	ir.lock.RLock()
	invocationResults := ir.Results[index]
	ir.lock.RUnlock()
	if invocationResults == nil {
		ir.lock.Lock()
		defer ir.lock.Unlock()
		// the results may have been added by another caller, or replaced, while the lock was not held
		if ir.Results == nil {
			ir.Results = map[uint32]*InvocationResults{}
		}
		invocationResults = ir.Results[index]
		if invocationResults == nil {
			invocationResults = &InvocationResults{lock: &ir.lock}
			invocationResults.unsafeInit(true)
			invocationResults.InvocationIndex = index
			ir.Results[index] = invocationResults
		}
	}
	return invocationResults
}

// setTarget tells which target and invocation the results are for.
func (ir *InvocationResults) setTarget(target *invocation.InvocationSpec, status *invocation.InvocationStatus) {
	ir.lock.Lock()
	ir.Target = target
	ir.Status = status
	ir.lock.Unlock()
}

// resultSink lowercases the response headers and adds the result to whichever of the invocation, target and overall results are enabled, queueing them for the registry.
func resultSink(invocationIndex uint32, result *invocation.InvocationResult,
	invocationResults *InvocationResults, targetResults *TargetResults, allResults *TargetResults) {
	if result != nil {
		result.Response.Headers = util.ToLowerHeaders(result.Response.Headers)
		if collectInvocationResults.Load() {
			invocationResults.addResult(result)
			chanSendInvocationToRegistry <- invocationResults
		}
		if collectTargetsResults.Load() {
			targetResults.addResult(result)
			chanSendTargetsToRegistry <- targetResults
		}
		if collectAllTargetsResults.Load() {
			allResults.addResult(result)
			chanSendTargetsToRegistry <- allResults
		}
	}
}

// channelSink feeds results from the result channel to resultSink until done, drains what is left, then marks the invocation finished.
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

// processTrackingConfig applies the tracking headers, cross headers and time buckets to a target's results and the overall results, taking their locks one at a time.
func processTrackingConfig(targetResults *TargetResults, allResults *TargetResults, trackingHeaders []string,
	crossTrackingHeaders map[string][]string, trackingTimeBuckets [][]int) {
	crossHeadersMap := util.BuildCrossHeadersMap(crossTrackingHeaders)
	// the invocations that are still running add their results while the tracking is changed, and the two results may
	// be one and the same, so the locks are taken one after the other
	targetResults.lock.Lock()
	targetResults.trackingHeaders = trackingHeaders
	targetResults.crossTrackingHeaders = crossTrackingHeaders
	targetResults.crossHeadersMap = crossHeadersMap
	targetResults.trackingTimeBuckets = trackingTimeBuckets
	targetResults.lock.Unlock()
	if allResults != nil && allResults != targetResults {
		allResults.lock.Lock()
		allResults.trackingHeaders = trackingHeaders
		allResults.crossTrackingHeaders = crossTrackingHeaders
		allResults.crossHeadersMap = crossHeadersMap
		allResults.lock.Unlock()
	}
}

// ResultChannelSinkFactory returns an invocation sink factory that processes the invocation's results from its result channel in a goroutine.
func ResultChannelSinkFactory(target *invocation.InvocationSpec, trackingHeaders []string,
	crossTrackingHeaders map[string][]string, trackingTimeBuckets [][]int) invocation.ResultSinkFactory {
	targetResults, allResults := targetsResults.getTargetResults(target.Name)
	processTrackingConfig(targetResults, allResults, trackingHeaders, crossTrackingHeaders, trackingTimeBuckets)
	return func(tracker *invocation.InvocationTracker) invocation.ResultSink {
		invocationResults := invocationsResults.getInvocation(tracker.ID)
		invocationResults.setTarget(target, tracker.Status)
		startRegistrySender()
		tracker.Channels.Lock.RLock()
		go channelSink(tracker.ID, tracker.Channels.ResultChannel, tracker.Channels.DoneChannel, invocationResults, targetResults, allResults)
		tracker.Channels.Lock.RUnlock()
		return nil
	}
}

// ResultSinkFactory returns an invocation sink factory whose sink adds each result directly to the invocation, target and overall results.
func ResultSinkFactory(target *invocation.InvocationSpec, trackingHeaders []string,
	crossTrackingHeaders map[string][]string, trackingTimeBuckets [][]int) invocation.ResultSinkFactory {
	targetResults, allResults := targetsResults.getTargetResults(target.Name)
	processTrackingConfig(targetResults, allResults, trackingHeaders, crossTrackingHeaders, trackingTimeBuckets)
	return func(tracker *invocation.InvocationTracker) invocation.ResultSink {
		invocationResults := invocationsResults.getInvocation(tracker.ID)
		invocationResults.setTarget(target, tracker.Status)
		startRegistrySender()
		return func(result *invocation.InvocationResult) {
			resultSink(tracker.ID, result, invocationResults, targetResults, allResults)
		}
	}
}

// ClearResults resets all invocation and target results.
func ClearResults() {
	invocationsResults.init(true)
	targetsResults.init(true)
}

// GetTargetsResultsJSON returns the per-target results as JSON ("{}" if none), read under each target's lock.
func GetTargetsResultsJSON(pretty bool) string {
	targetsResults.lock.RLock()
	if targetsResults.Results == nil {
		targetsResults.lock.RUnlock()
		return "{}"
	}
	snapshot := make(map[string]*TargetResults, len(targetsResults.Results))
	for target, tr := range targetsResults.Results {
		if tr != nil {
			snapshot[target] = tr
		}
	}
	targetsResults.lock.RUnlock()
	// the sinks of the invocations that are running add to the results of a target while holding only its lock, so the
	// results are read under their locks
	for _, tr := range snapshot {
		tr.lock.RLock()
		defer tr.lock.RUnlock()
	}
	if pretty {
		return util.ToPrettyJSONText(snapshot)
	}
	return util.ToJSONText(snapshot)
}

// LoadTargetsResultsJSON replaces the targets' results with those parsed from JSON, dropping null entries and filling in missing maps.
func LoadTargetsResultsJSON(content string) error {
	targetsResults.lock.Lock()
	defer targetsResults.lock.Unlock()
	err := util.ReadJson(content, &targetsResults.Results)
	for target, tr := range targetsResults.Results {
		if tr == nil {
			delete(targetsResults.Results, target)
		}
	}
	targetsResults.unsafeInitMissing()
	log.Printf("Loaded results of %d targets\n", len(targetsResults.Results))
	return err
}

// GetInvocationResultsJSON returns the per-invocation results as JSON.
func GetInvocationResultsJSON(pretty bool) string {
	invocationsResults.lock.RLock()
	defer invocationsResults.lock.RUnlock()
	if pretty {
		return util.ToPrettyJSONText(invocationsResults.Results)
	}
	return util.ToJSONText(invocationsResults.Results)
}

// LoadInvocationResultsJSON replaces the invocation results with those parsed from JSON, dropping null entries and restoring their shared lock.
func LoadInvocationResultsJSON(content string) error {
	invocationsResults.lock.Lock()
	defer invocationsResults.lock.Unlock()
	err := util.ReadJson(content, &invocationsResults.Results)
	// the lock is not a part of the JSON, and the results are going to be used by the invocations that get the same index
	for index, ir := range invocationsResults.Results {
		if ir == nil {
			delete(invocationsResults.Results, index)
			continue
		}
		ir.lock = &invocationsResults.lock
	}
	return err
}

// addDeltaCrossHeaders merges the delta's cross header counts into result, creating entries as needed.
func addDeltaCrossHeaders(result, delta map[string]*HeaderCounts, crossTrackingHeaders map[string][]string, crossHeadersMap map[string]string, detailed bool) {
	for ch, chCounts := range delta {
		if result[ch] == nil {
			result[ch] = newHeaderCounts(ch)
		}
		addDeltaHeaderCounts(result[ch], chCounts, crossTrackingHeaders, crossHeadersMap, detailed)
	}
}

// addDeltaCrossHeadersValues merges the delta's cross header counts, grouped by parent header value, into result.
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

// addDeltaHeaderCounts merges a delta's header counts into result, adding status code and cross header counts when detailed.
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
		result.CountsByStatusCodes = map[string]*CountInfo{}
	}
	if detailed {
		for statusCode, count := range delta.CountsByStatusCodes {
			incrementHeaderCountBy(result.CountsByStatusCodes, statusCode, 0, count)
		}
		for value, valueCounts := range delta.CountsByValuesStatusCodes {
			if result.CountsByValuesStatusCodes[value] == nil {
				result.CountsByValuesStatusCodes[value] = map[string]*CountInfo{}
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

// processDeltaHeaderCounts merges each header's delta counts into result, creating entries as needed.
func processDeltaHeaderCounts(result, delta map[string]*HeaderCounts, crossTrackingHeaders map[string][]string, crossHeadersMap map[string]string, detailed bool) {
	for header, counts := range delta {
		if result[header] == nil {
			result[header] = newHeaderCounts(header)
		}
		addDeltaHeaderCounts(result[header], counts, crossTrackingHeaders, crossHeadersMap, detailed)
	}
}

// processDeltaTimeBucketCounts merges the delta's per-time-bucket counts into result.
func processDeltaTimeBucketCounts(delta, result *KeyResultCounts, updateStatusCodes bool) {
	for tb, count := range delta.ByTimeBuckets {
		if result.ByTimeBuckets == nil {
			result.ByTimeBuckets = KeyResult{}
		}
		if result.ByTimeBuckets[tb] == nil {
			result.ByTimeBuckets[tb] = newKeyResultCounts(updateStatusCodes, false)
		}
		result.ByTimeBuckets[tb].incrementBy(count.Retries, &count.CountInfo)
	}

}

// processDeltaKeyResultCounts merges delta key results into *resultPtr, with status code and time bucket breakdowns when requested.
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
				if resultCount.ByStatusCodes == nil {
					resultCount.ByStatusCodes = KeyResult{}
				}
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

// AddDeltaResults merges a delta of a target's results (such as from a peer) into results, with detailed breakdowns if asked.
func AddDeltaResults(results, delta *TargetResults, detailed bool) {
	fmt.Printf("AddDeltaResults: Target [%s] first response [%s] last response [%s]\n", delta.Target, delta.FirstResultAt.UTC().String(), delta.LastResultAt.UTC().String())
	results.unsafeInitMissing()
	// a delta without any results has no timestamps, and mustn't reset the ones that the results have
	if !delta.FirstResultAt.IsZero() && (results.FirstResultAt.IsZero() || delta.FirstResultAt.Before(results.FirstResultAt)) {
		results.FirstResultAt = delta.FirstResultAt
	}
	if !delta.LastResultAt.IsZero() && (results.LastResultAt.IsZero() || results.LastResultAt.Before(delta.LastResultAt)) {
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

// incrementKeyResultCounts reflects over a map of KeyResultCounts and adds its totals, plus status code and time bucket totals if detailed, into the summary.
func incrementKeyResultCounts(result SummaryResult, delta interface{}, detailed bool) {
	v := reflect.ValueOf(delta)
	if v.Kind() != reflect.Map {
		return
	}
	iter := v.MapRange()
	for iter.Next() {
		key := iter.Key().String()
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
					counts.ByStatusCodes = map[string]int{}
				}
				for sc, count := range val.ByStatusCodes {
					counts.ByStatusCodes[sc] += count.Count
				}
			}
			if len(val.ByTimeBuckets) > 0 {
				if counts.ByTimeBuckets == nil {
					counts.ByTimeBuckets = map[string]int{}
				}
				for tb, count := range val.ByTimeBuckets {
					counts.ByTimeBuckets[tb] += count.Count
				}
			}
		}
	}
}

// addTargetResult adds a target's header, status, URI, payload size, retry, error and time bucket counts to the aggregate view.
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

// addTargetResult adds a target's invocation count and result time range, then its counts, to the client's aggregate view.
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

// AddTargetResult adds a target's results to both the overall aggregate and the target's own view.
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

// init allocates all the maps of the aggregate view.
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

// NewClientTargetsAggregateResults creates an empty aggregate view across targets, with its per-target views map.
func NewClientTargetsAggregateResults() *ClientTargetsAggregateResultsView {
	ctar := &ClientTargetsAggregateResultsView{}
	ctar.AggregateResultsView.init()
	ctar.ResultsByTargets = map[string]*ClientAggregateResultsView{}
	return ctar
}

// lockInvocationRegistryLocker asks the registry to lock the invocation's key in its locker, when the locker and a registry are in use.
func lockInvocationRegistryLocker(invocationIndex uint32) {
	if global.Flags.UseLocker && global.Self.RegistryURL != "" {
		url := fmt.Sprintf("%s/registry/peers/%s/%s/locker/lock/%s_%d", global.Self.RegistryURL,
			global.Self.Name, global.Self.Address, LockerClientKey, invocationIndex)
		if resp, err := registryClient.HTTP().Post(url, ContentTypeJSON, nil); err == nil {
			util.CloseResponse(resp)
		}
	}
}

// sendResultToRegistry stores the data in the registry's locker under the keys, when the locker and a registry are in use.
func sendResultToRegistry(keys []string, data interface{}) {
	if global.Flags.UseLocker && global.Self.RegistryURL != "" {
		url := fmt.Sprintf("%s/registry/peers/%s/%s/locker/store/%s", global.Self.RegistryURL, global.Self.Name, global.Self.Address, strings.Join(keys, ","))
		if resp, err := registryClient.HTTP().Post(url, ContentTypeJSON,
			strings.NewReader(util.ToJSONText(data))); err == nil {
			util.CloseResponse(resp)
		}
	}
}

// registrySender is a sender goroutine that batches queued target and invocation results, deduplicated by key, and posts them to the registry until stopped.
func registrySender(id int) {
	defer registrySenders.Done()
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
			// the flag is changed here, which is not to be done by senders that share a read lock
			ir.lock.Lock()
			if !ir.pendingRegistrySend {
				hasResultsToSend = true
				ir.pendingRegistrySend = true
				collectedInvocationResults[ir.InvocationIndex] = ir
			}
			ir.lock.Unlock()
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
						ir.lock.Lock()
						if !ir.pendingRegistrySend || collectedInvocationResults[ir.InvocationIndex] != nil {
							ir.pendingRegistrySend = true
							collectedInvocationResults[ir.InvocationIndex] = ir
							hasMoreResults = true
						}
						ir.lock.Unlock()
					default:
						break MoreResults
					}
				}
				if i < SendDelayMin-1 || (hasMoreResults && i < SendDelayMax-1) {
					time.Sleep(registrySendInterval)
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

// startRegistrySender starts the registry sender goroutines, once.
func startRegistrySender() {
	registrySendLock.Lock()
	defer registrySendLock.Unlock()
	if !sendingToRegistry {
		sendingToRegistry = true
		for i := 1; i < SenderCount; i++ {
			registrySenders.Add(1)
			go registrySender(i)
		}
	}
}

// StopRegistrySender signals the running registry senders to stop, and lets them be started again later.
func StopRegistrySender() {
	registrySendLock.Lock()
	defer registrySendLock.Unlock()
	// stop signals that no sender is there to take would stay in the channel, and stop the senders that are started later
	if !sendingToRegistry {
		return
	}
	sendingToRegistry = false
	for i := 1; i < SenderCount; i++ {
		stopRegistrySender <- true
	}
}

// EnableAllTargetResults turns collection of the results across all targets on or off.
func EnableAllTargetResults(enable bool) {
	collectAllTargetsResults.Store(enable)
}

// EnableInvocationResults turns collection of per-invocation results on or off.
func EnableInvocationResults(enable bool) {
	collectInvocationResults.Store(enable)
}
