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

package invocation

import (
	"bytes"
	"errors"
	"fmt"
	"goto/pkg/events"
	"goto/pkg/global"
	"goto/pkg/metrics"
	"goto/pkg/transport"
	"goto/pkg/util"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type InvocationResultResponse struct {
	Status            string      `json:"status"`
	StatusCode        int         `json:"statusCode"`
	Headers           http.Header `json:"headers"`
	PayloadSize       int         `json:"payloadSize"`
	ClientStreamCount int         `json:"clientStreamCount"`
	ServerStreamCount int         `json:"serverStreamCount"`
	Payload           []byte      `json:"-"`
	PayloadText       string      `json:"-"`
	FirstByteInAt     string      `json:"firstByteInAt"`
	LastByteInAt      string      `json:"lastByteInAt"`
}

type InvocationResultRequest struct {
	ID             string            `json:"id"`
	URL            string            `json:"url"`
	URI            string            `json:"uri"`
	Headers        map[string]string `json:"headers"`
	PayloadSize    int               `json:"payloadSize"`
	FirstByteOutAt string            `json:"firstByteOutAt"`
	LastByteOutAt  string            `json:"lastByteOutAt"`
	FirstRequestAt time.Time         `json:"firstRequestAt"`
	LastRequestAt  time.Time         `json:"lastRequestAt"`
}

type InvocationResult struct {
	TargetName          string                    `json:"targetName"`
	TargetID            string                    `json:"targetID"`
	Request             *InvocationResultRequest  `json:"request"`
	Response            *InvocationResultResponse `json:"response"`
	FailedURLs          map[string]int            `json:"failedURLs"`
	Retries             int                       `json:"retries"`
	LastRetryReason     string                    `json:"lastRetryReason"`
	ValidAssertionIndex int                       `json:"validAssertionIndex"`
	Errors              []map[string]interface{}  `json:"errors"`
	TookNanos           time.Duration             `json:"tookNanos"`
	httpResponse        *http.Response
	grpcResponse        interface{}
	grpcStatus          int
	client              transport.TransportClient
	tracker             *InvocationTracker
	request             *InvocationRequest
	err                 error
}

type InvocationStatus struct {
	TotalRequests     int    `json:"totalRequests"`
	AssignedRequests  int    `json:"assignedRequests"`
	CompletedRequests int    `json:"completedRequests"`
	SuccessCount      int    `json:"successCount"`
	FailureCount      int    `json:"failureCount"`
	RetriesCount      int    `json:"retriesCount"`
	ClientStreamCount int    `json:"clientStreamCount"`
	ServerStreamCount int    `json:"serverStreamCount"`
	ABCount           int    `json:"abCount"`
	FirstRequestAt    string `json:"firstRequestAt"`
	LastRequestAt     string `json:"lastRequestAt"`
	StopRequested     bool   `json:"stopRequested"`
	Stopped           bool   `json:"stopped"`
	Closed            bool   `json:"closed"`
	lastStatusCode    int
	lastStatusCount   int
	lastError         string
	lastErrorCount    int
	tracker           *InvocationTracker
	lock              sync.RWMutex
}

func newInvocationResult(request *InvocationRequest) *InvocationResult {
	return &InvocationResult{
		TargetName: request.tracker.Target.Name,
		TargetID:   request.targetID,
		Request: &InvocationResultRequest{
			ID:      request.requestID,
			Headers: request.headers,
		},
		Response:   &InvocationResultResponse{Headers: make(http.Header)},
		FailedURLs: map[string]int{},
		tracker:    request.tracker,
		client:     request.tracker.client.transportClient,
		request:    request,
	}
}

func (t *InvocationTracker) SendResult(r *InvocationResult) {
	if t.Channels != nil {
		t.Channels.Lock.Lock()
		defer t.Channels.Lock.Unlock()
		t.Channels.ResultChannel <- r
	}
}

func (tracker *InvocationTracker) publishResult(result *InvocationResult) {
	if tracker.Channels != nil {
		tracker.Channels.publish(result)
	}
}

func (c *InvocationChannels) publish(result *InvocationResult) {
	c.Lock.Lock()
	defer c.Lock.Unlock()
	if len(c.Sinks) > 0 {
		for _, sink := range c.Sinks {
			sink(result)
		}
	} else if c.ResultChannel != nil {
		if len(c.ResultChannel) > 50 {
			result.tracker.logResultChannelBacklog(result, len(c.ResultChannel))
		}
		c.ResultChannel <- result
	}
}

func (tracker *InvocationTracker) processResponse(result *InvocationResult) {
	if len(tracker.Target.Assertions) > 0 {
		result.validateResponse()
	}
	tracker.Status.trackStatus(result)
}

func (tracker *InvocationTracker) processError(result *InvocationResult) {
	if len(tracker.Target.Assertions) > 0 {
		result.validateResponse()
	}
	tracker.Status.reportRepeatedResponse()
	tracker.Status.trackError(result)
	metrics.UpdateTargetFailureCount(tracker.Target.Name)
}

func (result *InvocationResult) processHTTPResponse(req *InvocationRequest, r *http.Response, err error) {
	result.httpResponse = r
	result.err = err
	if err == nil {
		result.readHTTPResponsePayload()
		if r != nil {
			result.updateResult(req.url, req.uri, r.Status, r.StatusCode, r.Header)
		}
	} else {
		result.Response.Status = err.Error()
	}
}

func (result *InvocationResult) processGRPCResponse(req *InvocationRequest, responseStatus int, responseHeaders map[string][]string,
	responsePayload []string, clientStreamCount, serverStreamCount int, err error) {
	result.err = err
	if err == nil {
		result.Response.PayloadSize = len(responsePayload)
		result.Response.ClientStreamCount = clientStreamCount
		result.Response.ServerStreamCount = serverStreamCount
		result.updateResult(req.url, result.tracker.Target.Method, strconv.Itoa(responseStatus), responseStatus, responseHeaders)
	} else {
		result.Response.Status = err.Error()
	}
}

func (result *InvocationResult) readHTTPResponsePayload() {
	if result.httpResponse != nil && result.httpResponse.Body != nil {
		defer result.httpResponse.Body.Close()
		if result.tracker.Target.TrackPayload || result.tracker.Target.CollectResponse {
			data, size, first, last, err := util.ReadAndTrack(result.httpResponse.Body, result.tracker.Target.CollectResponse)
			if err == "" {
				result.Response.PayloadSize = size
				result.Response.FirstByteInAt = first.UTC().String()
				result.Response.LastByteInAt = last.UTC().String()
				if result.tracker.Target.CollectResponse {
					result.Response.Payload = data
				}
			} else {
				result.err = errors.New(err)
				result.Response.Status = err
				result.Errors = append(result.Errors, map[string]interface{}{"errorRead": err})
			}
		} else {
			util.DiscardResponseBody(result.httpResponse)
		}
	}
}

func (result *InvocationResult) updateResult(url, uri, status string, statusCode int, headers map[string][]string) {
	for header, values := range headers {
		result.Response.Headers[strings.ToLower(header)] = values
	}
	result.Response.Headers["status"] = []string{status}
	result.Response.Status = status
	result.Response.StatusCode = statusCode
	result.Request.URI = uri
	result.Request.URL = url
}

func (result *InvocationResult) retryReason() string {
	if result.err != nil {
		pieces := strings.Split(result.err.Error(), ":")
		return strings.Trim(pieces[len(pieces)-1], " ")
	}
	if result.httpResponse != nil {
		return result.httpResponse.Status
	}
	return ""
}

func (result *InvocationResult) shouldRetry() bool {
	if result.err != nil {
		return true
	}
	if result.httpResponse != nil {
		if result.tracker.Target.RetriableStatusCodes != nil {
			for _, retriableCode := range result.tracker.Target.RetriableStatusCodes {
				if retriableCode == result.httpResponse.StatusCode {
					return true
				}
			}
		}
	}
	return false
}

func (result *InvocationResult) validateResponse() {
	allErrors := []map[string]interface{}{}
	for i, assert := range result.tracker.Target.Assertions {
		if assert == nil {
			continue
		}
		errors := map[string]interface{}{}
		if result.Response.StatusCode != assert.StatusCode {
			errors["statusCode"] = map[string]interface{}{"expected": assert.StatusCode, "actual": result.Response.StatusCode}
		}
		if assert.PayloadSize > 0 && result.Response.PayloadSize != assert.PayloadSize {
			errors["payloadLength"] = map[string]interface{}{"expected": assert.PayloadSize, "actual": result.Response.PayloadSize}
		}
		if len(assert.Payload) > 0 && !bytes.Equal(assert.payload, result.Response.Payload) {
			errors["payload"] = map[string]interface{}{"expected": assert.PayloadSize, "actual": result.Response.PayloadSize}
		}
		if len(assert.headersRegexp) > 0 && !util.ContainsAllHeaders(result.Response.Headers, assert.headersRegexp) {
			errors["headers"] = map[string]interface{}{"expected": assert.Headers, "actual": result.Response.Headers}
		}
		if assert.Retries > 0 && result.Retries != assert.Retries {
			errors["retries"] = map[string]interface{}{"expected": assert.Retries, "actual": result.Retries}
		}
		if assert.SuccessURL != "" && result.Request.URL != assert.SuccessURL {
			errors["successURL"] = map[string]interface{}{"expected": assert.SuccessURL, "actual": result.Request.URL}
		}
		if assert.FailedURL != "" && result.FailedURLs[assert.FailedURL] == 0 {
			errors["failedURL"] = map[string]interface{}{"expected": assert.FailedURL, "actual": result.FailedURLs}
		}
		if len(errors) == 0 {
			result.ValidAssertionIndex = i + 1
			return
		} else {
			errors["assertionIndex"] = i + 1
			allErrors = append(allErrors, errors)
		}
	}
	result.Errors = allErrors
}

func (ir *InvocationResult) trackRequest(start, end time.Time) {
	ir.TookNanos = end.Sub(start)
	ir.Request.LastRequestAt = end
	if ir.Request.FirstRequestAt.IsZero() {
		ir.Request.FirstRequestAt = end
	}
}

func (is *InvocationStatus) trackRequest(at time.Time) {
	when := at.UTC().String()
	is.lock.Lock()
	defer is.lock.Unlock()
	is.TotalRequests++
	is.LastRequestAt = when
	if is.FirstRequestAt == "" {
		is.FirstRequestAt = when
	}
}

func (is *InvocationStatus) incrementRetriesCount() {
	is.lock.Lock()
	defer is.lock.Unlock()
	is.RetriesCount++
}

func (is *InvocationStatus) incrementABCount() {
	is.lock.Lock()
	defer is.lock.Unlock()
	is.ABCount++
}

func (is *InvocationStatus) trackStatus(result *InvocationResult) {
	is.lock.Lock()
	isRepeatStatus := is.lastStatusCode == result.Response.StatusCode
	if !isRepeatStatus && is.lastStatusCount > 1 || is.lastErrorCount > 1 {
		is.reportRepeatedResponse()
		is.lastStatusCode = -1
	}
	if is.lastStatusCode >= 0 && isRepeatStatus {
		is.lastStatusCount++
	} else {
		is.lastStatusCount = 1
		is.lastStatusCode = result.Response.StatusCode
	}
	is.lastError = ""
	is.lastErrorCount = 0
	is.SuccessCount++
	is.ClientStreamCount += result.Response.ClientStreamCount
	is.ServerStreamCount += result.Response.ServerStreamCount
	is.lock.Unlock()
	if global.Flags.EnableInvocationLogs || !isRepeatStatus {
		if global.Flags.EnableInvocationResponseLogs {
			log.Println(util.ToJSONText(result))
		}
		is.lock.Lock()
		if !isRepeatStatus {
			events.SendEventJSON(events.Client_InvocationResponse,
				fmt.Sprintf("%d-%s-%s", is.tracker.ID, result.request.targetID, result.request.requestID), result)
		}
		is.lock.Unlock()
	}
}

func (is *InvocationStatus) trackError(result *InvocationResult) {
	is.lock.Lock()
	defer is.lock.Unlock()
	is.tracker.reportError(result)
	is.lastStatusCode = 0
	is.lastStatusCount = 0
	is.lastError = result.err.Error()
	is.lastErrorCount++
	is.FailureCount++
}

func (is *InvocationStatus) reportRepeatedResponse() {
	is.lock.Lock()
	defer is.lock.Unlock()
	if is.lastStatusCount <= 0 {
		return
	}
	if is.lastStatusCount > 1 {
		is.tracker.reportRepeatedResponse()
		is.lastStatusCount = 0
		is.lastStatusCode = -1
	}
	if is.lastErrorCount > 1 {
		is.tracker.reportRepeatedFailure()
		is.lastErrorCount = 0
		is.lastError = ""
	}
}
