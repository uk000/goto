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

package invocation

import (
	"errors"
	. "goto/pkg/constants"
	grpc "goto/pkg/rpc/grpc/client"
	"goto/pkg/types"
	"net/http"
	"testing"
)

// A gRPC request has no HTTP request: it sends the header map as the request headers of the call. Retrying such a request adds
// the retry count header, which used to dereference the missing HTTP request.
func TestAddOrUpdateHeaderOfARequestWithoutAnHTTPRequest(t *testing.T) {
	ir := &InvocationRequest{headers: types.SimpleHTTPHeaders{}}
	ir.addOrUpdateHeader(HeaderGotoRetryCount, "1")
	ir.addOrUpdateHeader(HeaderGotoRetryCount, "2")
	if len(ir.headers) != 1 || ir.headers[HeaderGotoRetryCount] != "2" {
		t.Errorf("headers = %v, want just %s=2", ir.headers, HeaderGotoRetryCount)
	}
}

func TestAddOrUpdateHeaderOfARequestWithAnHTTPRequest(t *testing.T) {
	httpRequest, err := http.NewRequest("GET", "http://127.0.0.1:1/", nil)
	if err != nil {
		t.Fatal(err)
	}
	ir := &InvocationRequest{headers: types.SimpleHTTPHeaders{}, httpRequest: httpRequest}
	ir.addOrUpdateHeader(HeaderGotoRetryCount, "1")
	ir.addOrUpdateHeader(HeaderGotoRetryCount, "2")
	if ir.headers[HeaderGotoRetryCount] != "2" {
		t.Errorf("headers = %v, want %s=2", ir.headers, HeaderGotoRetryCount)
	}
	if got := httpRequest.Header.Values(HeaderGotoRetryCount); len(got) != 1 || got[0] != "2" {
		t.Errorf("headers of the HTTP request = %v, want %s=[2]", httpRequest.Header, HeaderGotoRetryCount)
	}
}

func TestAddOrUpdateRequestIdOfARequestWithoutAnHTTPRequest(t *testing.T) {
	tracker := newStatusTestTracker("grpc-request-id")
	tracker.Target.SendID = true
	ir := &InvocationRequest{requestID: "1", headers: types.SimpleHTTPHeaders{}, tracker: tracker}
	ir.addOrUpdateRequestId()
	if len(ir.headers) != 0 {
		t.Errorf("headers = %v, want none", ir.headers)
	}
}

func newGRPCResultTestRequest(name string) (*InvocationRequest, *InvocationStatus) {
	tracker := newStatusTestTracker(name)
	tracker.Target.Method = "echo"
	result := newStatusTestResult(tracker, 0, nil)
	result.Response.Headers = http.Header{}
	return &InvocationRequest{requestID: "1", url: "localhost:1", headers: types.SimpleHTTPHeaders{}, tracker: tracker, result: result}, tracker.Status
}

// A call can fail without producing any response, and without an error either: e.g. a stream that delivers nothing. Reporting
// the failure used to read the message of that missing error.
func TestProcessGRPCResultWithoutResponsesOrErrors(t *testing.T) {
	for name, result := range map[string]*grpc.GRPCResult{
		"nil result":      nil,
		"empty result":    {},
		"no responses":    {Responses: []*grpc.GRPCResponse{}, Errors: []error{}},
		"only a nil list": {Errors: nil},
	} {
		t.Run(name, func(t *testing.T) {
			ir, _ := newGRPCResultTestRequest("grpc-nothing-" + name)
			ir.processGRPCResult(result)
			if ir.result.err != nil || ir.result.Response.StatusCode != 0 {
				t.Errorf("result = {err: %v, status code: %d}, want no error and no status", ir.result.err, ir.result.Response.StatusCode)
			}
		})
	}
}

func TestProcessGRPCResultWithoutResponsesButWithAnError(t *testing.T) {
	ir, _ := newGRPCResultTestRequest("grpc-error-only")
	ir.processGRPCResult(&grpc.GRPCResult{Errors: []error{errors.New("connection refused")}})
	if ir.result.err != nil {
		t.Errorf("err = %v, want none recorded when there's no response to record it with", ir.result.err)
	}
}

func TestProcessGRPCResultRecordsTheFirstResponseAndError(t *testing.T) {
	ir, _ := newGRPCResultTestRequest("grpc-response")
	ir.processGRPCResult(&grpc.GRPCResult{
		Responses: []*grpc.GRPCResponse{
			{EquivalentHTTPStatusCode: 200, ResponseHeaders: map[string][]string{"x-a": {"1"}}, ResponsePayload: []string{"a", "b"}, ClientStreamCount: 1, ServerStreamCount: 2},
			{EquivalentHTTPStatusCode: 500},
		},
	})
	response := ir.result.Response
	if ir.result.err != nil || response.StatusCode != 200 || response.PayloadSize != 2 || response.ClientStreamCount != 1 || response.ServerStreamCount != 2 {
		t.Errorf("result = {err: %v, response: %+v}, want the first response recorded without an error", ir.result.err, response)
	}
	if response.Headers["x-a"][0] != "1" || ir.result.Request.URI != "echo" {
		t.Errorf("headers = %v, request uri = %q, want x-a=[1] and the method as the uri", response.Headers, ir.result.Request.URI)
	}

	ir, _ = newGRPCResultTestRequest("grpc-response-with-error")
	ir.processGRPCResult(&grpc.GRPCResult{
		Responses: []*grpc.GRPCResponse{{EquivalentHTTPStatusCode: 503}},
		Errors:    []error{errors.New("unavailable"), errors.New("second")},
	})
	if ir.result.err == nil || ir.result.err.Error() != "unavailable" || ir.result.Response.Status != "unavailable" {
		t.Errorf("result = {err: %v, status: %q}, want the first error recorded", ir.result.err, ir.result.Response.Status)
	}
}
