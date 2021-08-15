/**
 * Copyright 2021 uk
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
  . "goto/pkg/events/eventslist"
  "goto/pkg/global"
  "goto/pkg/metrics"
  "goto/pkg/util"
  "log"
  "net/http"
  "strings"
  "sync"
  "time"
)

type InvocationResult struct {
  TargetName          string                   `json:"targetName"`
  TargetID            string                   `json:"targetID"`
  Status              string                   `json:"status"`
  StatusCode          int                      `json:"statusCode"`
  RequestPayloadSize  int                      `json:"requestPayloadSize"`
  ResponsePayloadSize int                      `json:"responsePayloadSize"`
  FirstByteInAt       string                   `json:"firstByteInAt"`
  LastByteInAt        string                   `json:"lastByteInAt"`
  FirstByteOutAt      string                   `json:"firstByteOutAt"`
  LastByteOutAt       string                   `json:"lastByteOutAt"`
  FirstRequestAt      time.Time                `json:"firstRequestAt"`
  LastRequestAt       time.Time                `json:"lastRequestAt"`
  Retries             int                      `json:"retries"`
  URL                 string                   `json:"url"`
  URI                 string                   `json:"uri"`
  RequestID           string                   `json:"requestID"`
  RequestHeaders      map[string]string        `json:"requestHeaders"`
  ResponseHeaders     map[string][]string      `json:"responseHeaders"`
  FailedURLs          map[string]int           `json:"failedURLs"`
  LastRetryReason     string                   `json:"lastRetryReason"`
  ValidAssertionIndex int                      `json:"validAssertionIndex"`
  Errors              []map[string]interface{} `json:"errors"`
  Data                []byte                   `json:"-"`
  TookNanos           int                      `json:"tookNanos"`
  httpResponse        *http.Response
  client              *util.ClientTracker
  tracker             *InvocationTracker
  request             *InvocationRequest
  err                 error
}

type InvocationStatus struct {
  TotalRequests     int    `json:"totalRequests"`
  CompletedRequests int    `json:"completedRequests"`
  SuccessCount      int    `json:"successCount"`
  FailureCount      int    `json:"failureCount"`
  RetriesCount      int    `json:"retriesCount"`
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
    RequestID:       request.requestID,
    TargetName:      request.tracker.Target.Name,
    TargetID:        request.targetID,
    RequestHeaders:  request.headers,
    ResponseHeaders: map[string][]string{},
    FailedURLs:      map[string]int{},
    tracker:         request.tracker,
    client:          request.tracker.client.clientTracker,
    request:         request,
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
    result.readResponsePayload()
    if r != nil {
      result.updateResult(req.url, req.uri, r.Status, r.StatusCode, r.Header)
    }
  } else {
    result.Status = err.Error()
  }
}

func (result *InvocationResult) readResponsePayload() {
  if result.httpResponse != nil && result.httpResponse.Body != nil {
    defer result.httpResponse.Body.Close()
    if result.tracker.Target.TrackPayload {
      data, size, first, last, err := util.ReadAndTrack(result.httpResponse.Body, result.tracker.Target.CollectResponse)
      if err == "" {
        result.ResponsePayloadSize = size
        result.FirstByteInAt = first.UTC().String()
        result.LastByteInAt = last.UTC().String()
        if result.tracker.Target.CollectResponse {
          result.Data = data
        }
      } else {
        result.err = errors.New(err)
        result.Status = err
        result.Errors = append(result.Errors, map[string]interface{}{"errorRead": err})
      }
    } else {
      util.DiscardResponseBody(result.httpResponse)
    }
  }
}

func (result *InvocationResult) updateResult(url, uri, status string, statusCode int, headers map[string][]string) {
  for header, values := range headers {
    result.ResponseHeaders[strings.ToLower(header)] = values
  }
  result.ResponseHeaders["status"] = []string{status}
  result.Status = status
  result.StatusCode = statusCode
  result.URI = uri
  result.URL = url
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
  if result.tracker.Target.RetriableStatusCodes != nil {
    for _, retriableCode := range result.tracker.Target.RetriableStatusCodes {
      if retriableCode == result.httpResponse.StatusCode {
        return true
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
    if result.StatusCode != assert.StatusCode {
      errors["statusCode"] = map[string]interface{}{"expected": assert.StatusCode, "actual": result.StatusCode}
    }
    if assert.PayloadSize > 0 && result.ResponsePayloadSize != assert.PayloadSize {
      errors["payloadLength"] = map[string]interface{}{"expected": assert.PayloadSize, "actual": result.ResponsePayloadSize}
    }
    if len(assert.Payload) > 0 && bytes.Compare(assert.payload, result.Data) != 0 {
      errors["payload"] = map[string]interface{}{"expected": assert.PayloadSize, "actual": result.ResponsePayloadSize}
    }
    if len(assert.headersRegexp) > 0 && !util.ContainsAllHeaders(result.ResponseHeaders, assert.headersRegexp) {
      errors["headers"] = map[string]interface{}{"expected": assert.Headers, "actual": result.ResponseHeaders}
    }
    if assert.Retries > 0 && result.Retries != assert.Retries {
      errors["retries"] = map[string]interface{}{"expected": assert.Retries, "actual": result.Retries}
    }
    if assert.SuccessURL != "" && result.URL != assert.SuccessURL {
      errors["successURL"] = map[string]interface{}{"expected": assert.SuccessURL, "actual": result.URL}
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
  ir.TookNanos = int(end.Sub(start).Nanoseconds())
  ir.LastRequestAt = end
  if ir.FirstRequestAt.IsZero() {
    ir.FirstRequestAt = end
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
  isRepeatStatus := is.lastStatusCode == result.StatusCode
  if !isRepeatStatus && is.lastStatusCount > 1 || is.lastErrorCount > 1 {
    is.reportRepeatedResponse()
    is.lastStatusCode = -1
  }
  if is.lastStatusCode >= 0 && isRepeatStatus {
    is.lastStatusCount++
  } else {
    is.lastStatusCount = 1
    is.lastStatusCode = result.StatusCode
  }
  is.lastError = ""
  is.lastErrorCount = 0
  is.SuccessCount++
  is.lock.Unlock()
  if global.EnableInvocationLogs || !isRepeatStatus {
    if global.EnableInvocationLogs {
      log.Println(util.ToJSON(result))
    }
    is.lock.Lock()
    if !isRepeatStatus {
      events.SendEventJSON(Client_InvocationResponse,
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
