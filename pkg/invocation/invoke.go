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
	"fmt"
	. "goto/pkg/constants"
	"goto/pkg/metrics"
	grpc "goto/pkg/rpc/grpc/client"
	"goto/pkg/rpc/grpc/pb"
	"goto/pkg/transport"
	"goto/pkg/util"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"
)

type InvocationRequest struct {
	requestID       string
	targetID        string
	url             string
	uri             string
	host            string
	headers         map[string]string
	httpRequest     *http.Request
	grpcInput       *pb.Input
	grpcStreamInput *pb.StreamConfig
	requestReader   io.ReadCloser
	requestWriter   io.WriteCloser
	client          transport.ClientTransport
	tracker         *InvocationTracker
	result          *InvocationResult
}

func (tracker *InvocationTracker) invokeWithRetries(requestID string, targetID string, urls ...string) *InvocationResult {
	status := tracker.Status
	target := tracker.Target
	request := tracker.newInvocationRequest(requestID, targetID, urls...)
	if request == nil {
		return nil
	}
	result := request.result
	for i := 0; i <= target.Retries; i++ {
		if status.StopRequested || status.Stopped {
			break
		}
		if i > 0 {
			result.Retries++
			time.Sleep(target.retryDelayD)
		}
		if status.StopRequested || status.Stopped {
			break
		}
		request.invoke()
		metrics.UpdateTargetRequestCount(target.Name)
		retry := result.shouldRetry()
		if !retry {
			break
		} else if i < target.Retries {
			result.LastRetryReason = result.retryReason()
			tracker.logRetryRequired(result, target.Retries-i)
			result.FailedURLs[request.url]++
			request.requestID = fmt.Sprintf("%s-%d", requestID, i+2)
			request.addOrUpdateHeader(HeaderGotoRetryCount, strconv.Itoa(i+1))
			if target.Fallback && len(target.BURLS) > i {
				request.url = target.BURLS[i]
				if !tracker.client.prepareRequest(request) {
					tracker.logBRequestCreationFailed(result, target.BURLS[i])
				} else {
					result.Request.URL = request.url
				}
			} else {
				request.addOrUpdateRequestId()
			}
			tracker.Status.incrementRetriesCount()
		}
	}
	if result != nil && !status.StopRequested && !status.Stopped {
		if result.err == nil {
			tracker.processResponse(result)
		} else {
			tracker.processError(result)
		}
	}
	return result
}

func (tracker *InvocationTracker) newInvocationRequest(requestID, targetID string, substituteURL ...string) *InvocationRequest {
	if tracker.client == nil {
		return nil
	}
	url := tracker.prepareRequestURL(requestID, targetID, substituteURL...)
	headers := tracker.prepareRequestHeaders(requestID, targetID, url)
	return tracker.newRequest(requestID, targetID, url, headers)
}

func (tracker *InvocationTracker) newRequest(requestID, targetID, url string, headers map[string]string) *InvocationRequest {
	ir := &InvocationRequest{
		requestID: requestID,
		targetID:  targetID,
		url:       url,
		host:      tracker.Target.Host,
		headers:   headers,
		client:    tracker.client.transportClient,
		tracker:   tracker,
	}
	ir.result = newInvocationResult(ir)
	tracker.client.prepareRequest(ir)
	return ir
}

func (tracker *InvocationTracker) prepareRequestURL(requestID, targetID string, substituteURL ...string) string {
	var url string
	target := tracker.Target
	if target.Random {
		if r := util.Random(len(target.BURLS) + 1); r == 0 {
			url = target.URL
		} else {
			url = target.BURLS[r-1]
		}
	} else if len(substituteURL) > 0 {
		url = substituteURL[0]
	} else {
		url = target.URL
	}
	return url
}

func (ir *InvocationRequest) addOrUpdateHeader(header, value string) {
	ir.headers[header] = value
	ir.httpRequest.Header.Del(header)
	ir.httpRequest.Header.Add(header, value)
}

func (ir *InvocationRequest) addOrUpdateRequestId() {
	if ir.httpRequest == nil {
		return
	}
	if ir.tracker.Target.SendID {
		q := ir.httpRequest.URL.Query()
		q.Del("x-request-id")
		q.Add("x-request-id", ir.requestID)
		ir.httpRequest.URL.RawQuery = q.Encode()
		ir.url = ir.httpRequest.URL.String()
		ir.addOrUpdateHeader(HeaderGotoTargetURL, ir.url)
	}
	ir.addOrUpdateHeader(HeaderGotoRequestID, ir.requestID)
}

func (client *InvocationClient) prepareRequest(ir *InvocationRequest) bool {
	client.lock.Lock()
	defer client.lock.Unlock()
	if client.transportClient != nil {
		if client.transportClient.IsGRPC() {
			ir.uri = client.tracker.Target.Service + "." + client.tracker.Target.Method
		} else if client.transportClient.IsHTTP() {
			var requestReader io.ReadCloser
			var requestWriter io.WriteCloser
			if len(client.tracker.Payloads) > 1 {
				requestReader, requestWriter = io.Pipe()
			} else if len(client.tracker.Payloads) == 1 && len(client.tracker.Payloads[0]) > 0 {
				requestReader = io.NopCloser(bytes.NewReader(client.tracker.Payloads[0]))
				ir.result.Request.PayloadSize = len(client.tracker.Payloads[0])
			}
			if req, err := http.NewRequest(client.tracker.Target.Method, ir.url, requestReader); err == nil {
				ir.httpRequest = req
				ir.addOrUpdateRequestId()
				for h, hv := range ir.headers {
					req.Header.Del(h)
					req.Header.Add(h, hv)
				}
				if ir.host != "" {
					req.Host = ir.host
				}
				if req.Host == "" {
					req.Host = req.URL.Host
				}
				ir.uri = req.URL.Path
				ir.requestReader = requestReader
				ir.requestWriter = requestWriter
			} else {
				ir.result.err = err
				return false
			}
		}
	}
	ir.tracker.logRequestStart(ir.requestID, ir.targetID, ir.url)
	return true
}

func (tracker *InvocationTracker) prepareRequestHeaders(requestID, targetID, url string) map[string]string {
	headers := map[string]string{}
	for _, h := range tracker.Target.Headers {
		if len(h) >= 2 {
			headers[h[0]] = h[1]
		}
	}
	headers[HeaderGotoTargetID] = targetID
	return headers
}

func (ir *InvocationRequest) writeRequestPayload() {
	if ir.requestWriter != nil {
		go func() {
			size, first, last, err := util.WriteAndTrack(ir.requestWriter, ir.tracker.Payloads, ir.tracker.Target.streamDelayD)
			if ir.tracker.Target.TrackPayload {
				if err == nil {
					ir.result.Request.PayloadSize = size
					ir.result.Request.FirstByteOutAt = first.UTC().String()
					ir.result.Request.LastByteOutAt = last.UTC().String()
				} else {
					ir.result.err = err
				}
			}
		}()
	}
}

func (ir *InvocationRequest) invoke() {
	start := time.Now()
	if ir.client.IsGRPC() {
		ir.invokeGRPC()
	} else {
		ir.invokeHTTP()
	}
	end := time.Now()
	ir.result.trackRequest(start, end)
	ir.tracker.Status.trackRequest(end)
}

func (ir *InvocationRequest) invokeHTTP() {
	if ir.client == nil || ir.client.Transport() == nil || ir.client.HTTP() == nil {
		log.Printf("Invocation: [ERROR] HTTP invocation attempted without a client")
		return
	}
	ir.client.SetTLSConfig(tlsConfig(ir.httpRequest.Host, ir.tracker.Target.VerifyTLS))
	ir.writeRequestPayload()
	resp, err := ir.client.HTTP().Do(ir.httpRequest)
	ir.result.processHTTPResponse(ir, resp, err)
}

func (ir *InvocationRequest) invokeGRPC() {
	if ir.client == nil {
		log.Printf("Invocation: [ERROR] GRPC invocation attempted without a client")
		return
	}
	if response, err := ir.client.(*grpc.GRPCClient).Invoke(ir.tracker.Target.Method, ir.headers, ir.tracker.Payloads); err == nil {
		ir.result.processGRPCResponse(ir, response.EquivalentHTTPStatusCode, response.ResponseHeaders,
			response.ResponsePayload, response.ClientStreamCount, response.ServerStreamCount, err)
	} else {
		ir.tracker.logConnectionFailed(err.Error())
	}
}

func (c *InvocationClient) close() {
	c.lock.Lock()
	defer c.lock.Unlock()
	if c.transportClient != nil {
		c.transportClient.Close()
	}
}
