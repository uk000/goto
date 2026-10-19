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

package rpc

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"goto/pkg/constants"
	"goto/pkg/server/response/payload"

	"github.com/gorilla/mux"
)

// call invokes one of the exported handlers directly, with the route vars that the router would have set.
func (e *testEnv) call(f func(http.ResponseWriter, *http.Request, RPCServiceRegistry), reg RPCServiceRegistry,
	target, body string, vars map[string]string, headers ...string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	f(rr, mux.SetURLVars(e.request("POST", target, body, headers...), vars), reg)
	return rr
}

// lookupPayload finds the payload that the server would respond with for a request of an rpc method.
func lookupPayload(port int, uri string, header http.Header, body string) (*payload.ResponsePayload, bool) {
	var rc io.ReadCloser
	if body != "" {
		rc = io.NopCloser(strings.NewReader(body))
	}
	_, rp, _, found := payload.PayloadManager.GetResponsePayload(port, true, uri, header, nil, rc)
	return rp, found
}

func TestCheckService(t *testing.T) {
	e := newTestEnv(t)
	e.grpc.add("Alpha", "Call")
	e.json.add("Beta", "Do")

	tests := []struct {
		name        string
		reg         RPCServiceRegistry
		vars        map[string]string
		wantOK      bool
		wantCode    int
		wantMsg     string
		wantType    string
		wantMethod  string
		wantService string
	}{
		{name: "no service", reg: e.grpc, vars: map[string]string{}, wantCode: 400, wantMsg: "No service"},
		{name: "empty service", reg: e.grpc, vars: map[string]string{"service": ""}, wantCode: 400, wantMsg: "No service"},
		{name: "unknown service", reg: e.grpc, vars: map[string]string{"service": "Nope"}, wantCode: 400, wantMsg: "Service proto not defined [Nope]"},
		{name: "service of the other registry", reg: e.grpc, vars: map[string]string{"service": "Beta"}, wantCode: 400, wantMsg: "Service proto not defined [Beta]"},
		{name: "unknown method", reg: e.grpc, vars: map[string]string{"service": "Alpha", "method": "Nope"}, wantCode: 400, wantMsg: "Method [Nope] not defined for service [Alpha]"},
		{name: "method of another service", reg: e.json, vars: map[string]string{"service": "Beta", "method": "Call"}, wantCode: 400, wantMsg: "Method [Call] not defined for service [Beta]"},
		{name: "grpc service", reg: e.grpc, vars: map[string]string{"service": "Alpha"}, wantOK: true, wantCode: 200, wantType: "GRPC", wantService: "Alpha"},
		{name: "grpc service and method", reg: e.grpc, vars: map[string]string{"service": "Alpha", "method": "Call"}, wantOK: true, wantCode: 200, wantType: "GRPC", wantService: "Alpha", wantMethod: "Call"},
		{name: "jsonrpc service", reg: e.json, vars: map[string]string{"service": "Beta"}, wantOK: true, wantCode: 200, wantType: "JSONRPC", wantService: "Beta"},
		{name: "jsonrpc service and method", reg: e.json, vars: map[string]string{"service": "Beta", "method": "Do"}, wantOK: true, wantCode: 200, wantType: "JSONRPC", wantService: "Beta", wantMethod: "Do"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := mux.SetURLVars(e.request("GET", "/x", ""), tt.vars)
			service, method, serviceType, msg, ok := CheckService(rr, req, tt.reg)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (msg %q)", ok, tt.wantOK, msg)
			}
			if rr.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", rr.Code, tt.wantCode)
			}
			if msg != tt.wantMsg {
				t.Errorf("msg = %q, want %q", msg, tt.wantMsg)
			}
			if serviceType != tt.wantType {
				t.Errorf("serviceType = %q, want %q", serviceType, tt.wantType)
			}
			if tt.wantService != "" && (service == nil || service.GetName() != tt.wantService) {
				t.Errorf("service = %v, want %s", service, tt.wantService)
			}
			if tt.wantMethod == "" && method != nil {
				t.Errorf("method = %v, want none", method)
			}
			if tt.wantMethod != "" && (method == nil || method.GetName() != tt.wantMethod) {
				t.Errorf("method = %v, want %s", method, tt.wantMethod)
			}
		})
	}
}

func TestSetServiceResponsePayload(t *testing.T) {
	e := newTestEnv(t)
	s := e.grpc.add("PayloadSvc", "Call")
	m := s.Methods["Call"]
	vars := map[string]string{"service": "PayloadSvc", "method": "Call"}

	rr := e.call(SetServiceResponsePayload, e.grpc, "/grpc/server/PayloadSvc/Call/payload", `{"hello":"world"}`, vars)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "Set GRPC payload for service [PayloadSvc] method [Call]") {
		t.Fatalf("response = %d %q", rr.Code, rr.Body.String())
	}
	got := m.snapshot()
	if got.ResponsePayload != `{"hello":"world"}` {
		t.Errorf("method payload = %q", got.ResponsePayload)
	}
	if got.StreamCount != 0 || got.StreamDelayCount != -1 || got.StreamDelayMin != 0 || got.StreamDelayMax != 0 {
		t.Errorf("method stream config = %+v, want count 0, no delay, delay count -1", got)
	}
	rp, found := lookupPayload(e.port, m.URI, nil, "")
	if !found {
		t.Fatalf("no payload is registered for %s", m.URI)
	}
	if string(rp.Payload) != `{"hello":"world"}` || rp.ContentType != "plain/text" || rp.IsStream {
		t.Errorf("registered payload = %q content-type %q stream %v", rp.Payload, rp.ContentType, rp.IsStream)
	}
	if _, found := lookupPayload(e.port, "/payloadsvc/other", nil, ""); found {
		t.Errorf("the payload of a method must not be served for another URI")
	}
}

func TestSetServiceResponsePayloadUsesContentTypeHeader(t *testing.T) {
	e := newTestEnv(t)
	s := e.json.add("CTSvc", "Call")
	vars := map[string]string{"service": "CTSvc", "method": "Call"}
	e.call(SetServiceResponsePayload, e.json, "/jsonrpc/server/CTSvc/Call/payload", `{"a":1}`, vars,
		constants.HeaderResponseContentType, "application/json")
	rp, found := lookupPayload(e.port, s.Methods["Call"].URI, nil, "")
	if !found || rp.ContentType != "application/json" {
		t.Fatalf("payload = %+v found %v, want content type application/json", rp, found)
	}
}

func TestSetServiceResponsePayloadStream(t *testing.T) {
	e := newTestEnv(t)
	s := e.grpc.add("StreamSvc", "Pump")
	m := s.Methods["Pump"]
	vars := map[string]string{"service": "StreamSvc", "method": "Pump", "count": "3", "delay": "10ms-20ms:2"}

	rr := e.call(SetServiceResponsePayload, e.grpc, "/grpc/server/StreamSvc/Pump/payload/stream/count=3/delay=10ms-20ms:2",
		`[{"n":1},{"n":2},{"n":3}]`, vars)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "Set GRPC payload for service [StreamSvc] method [Pump]") {
		t.Fatalf("response = %d %q", rr.Code, rr.Body.String())
	}
	got := m.snapshot()
	if got.StreamCount != 3 || got.StreamDelayMin != 10*time.Millisecond || got.StreamDelayMax != 20*time.Millisecond || got.StreamDelayCount != 2 {
		t.Errorf("method stream config = %+v, want count 3, delay 10ms-20ms, delay count 2", got)
	}
	rp, found := lookupPayload(e.port, m.URI, nil, "")
	if !found || !rp.IsStream || len(rp.StreamPayload) != 3 {
		t.Fatalf("registered payload = %+v found %v, want a stream of 3 items", rp, found)
	}
}

func TestSetServiceResponsePayloadWithoutBodyIsReportedAndChangesNothing(t *testing.T) {
	e := newTestEnv(t)
	s := e.grpc.add("EmptySvc", "Call")
	m := s.Methods["Call"]
	vars := map[string]string{"service": "EmptySvc", "method": "Call"}

	rr := e.call(SetServiceResponsePayload, e.grpc, "/grpc/server/EmptySvc/Call/payload", "", vars)
	if !strings.Contains(rr.Body.String(), "Failed to set GRPC payload for service [EmptySvc] method [Call]") ||
		!strings.Contains(rr.Body.String(), "No payload") {
		t.Errorf("response = %d %q, want a failure message about the missing payload", rr.Code, rr.Body.String())
	}
	if got := m.snapshot(); got.ResponsePayload != "" || got.StreamDelayCount != 0 {
		t.Errorf("a payload that couldn't be set must not change the method, got %+v", got)
	}
	if _, found := lookupPayload(e.port, m.URI, nil, ""); found {
		t.Errorf("no payload must be registered")
	}
}

func TestSetServiceResponsePayloadRejectsUnknownServiceAndMethod(t *testing.T) {
	e := newTestEnv(t)
	s := e.grpc.add("KnownSvc", "Call")

	rr := e.call(SetServiceResponsePayload, e.grpc, "/x", `{"a":1}`, map[string]string{"service": "Missing", "method": "Call"})
	if rr.Code != 400 || !strings.Contains(rr.Body.String(), "Service proto not defined [Missing]") {
		t.Errorf("unknown service: %d %q", rr.Code, rr.Body.String())
	}
	rr = e.call(SetServiceResponsePayload, e.grpc, "/x", `{"a":1}`, map[string]string{"service": "KnownSvc", "method": "Missing"})
	if rr.Code != 400 || !strings.Contains(rr.Body.String(), "Method [Missing] not defined for service [KnownSvc]") {
		t.Errorf("unknown method: %d %q", rr.Code, rr.Body.String())
	}
	if _, found := lookupPayload(e.port, s.Methods["Call"].URI, nil, ""); found {
		t.Errorf("a rejected request must not register a payload")
	}
}

func TestClearServiceResponsePayload(t *testing.T) {
	e := newTestEnv(t)
	s := e.grpc.add("ClearSvc", "Call")
	vars := map[string]string{"service": "ClearSvc", "method": "Call"}
	e.call(SetServiceResponsePayload, e.grpc, "/grpc/server/ClearSvc/Call/payload", `{"a":1}`, vars)
	if _, found := lookupPayload(e.port, s.Methods["Call"].URI, nil, ""); !found {
		t.Fatalf("payload must be set before it is cleared")
	}
	rr := e.call(ClearServiceResponsePayload, e.grpc, "/grpc/server/ClearSvc/Call/payload/clear", "", vars)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "Cleared GRPC payloads for service [ClearSvc] method [Call]") {
		t.Errorf("response = %d %q", rr.Code, rr.Body.String())
	}
	if _, found := lookupPayload(e.port, s.Methods["Call"].URI, nil, ""); found {
		t.Errorf("payload must be gone after it is cleared")
	}
	rr = e.call(ClearServiceResponsePayload, e.grpc, "/x", "", map[string]string{"service": "ClearSvc", "method": "Missing"})
	if rr.Code != 400 {
		t.Errorf("clearing an unknown method must be rejected, got %d %q", rr.Code, rr.Body.String())
	}
}

func TestSetServicePayloadTransform(t *testing.T) {
	e := newTestEnv(t)
	s := e.json.add("TransformSvc", "Call")
	vars := map[string]string{"service": "TransformSvc", "method": "Call"}
	body := `[{"mappings":[{"source":"a","target":"b"}],"payload":{"x":1}}]`

	rr := e.call(SetServicePayloadTransform, e.json, "/jsonrpc/server/TransformSvc/Call/payload/transform", body, vars)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "Configured Transform paths for JSONRPC service [TransformSvc] method [Call]") {
		t.Fatalf("response = %d %q", rr.Code, rr.Body.String())
	}
	rp, found := lookupPayload(e.port, s.Methods["Call"].URI, nil, "")
	if !found || len(rp.Transforms) != 1 || len(rp.Transforms[0].Mappings) != 1 || rp.Transforms[0].Mappings[0].Source != "a" {
		t.Errorf("registered payload = %+v found %v, want the posted transform", rp, found)
	}
}

func TestSetServicePayloadTransformRejections(t *testing.T) {
	e := newTestEnv(t)
	s := e.json.add("TransformSvc2", "Call")
	vars := map[string]string{"service": "TransformSvc2", "method": "Call"}
	tests := []struct {
		name string
		body string
		vars map[string]string
		want string
	}{
		{name: "null transforms", body: "null", vars: vars, want: "Missing transforms."},
		{name: "not json", body: "{", vars: vars, want: "Failed to read transformations"},
		{name: "empty body", body: "", vars: vars, want: "Failed to read transformations"},
		{name: "unknown service", body: "[]", vars: map[string]string{"service": "Missing", "method": "Call"}, want: "Service proto not defined [Missing]"},
		{name: "unknown method", body: "[]", vars: map[string]string{"service": "TransformSvc2", "method": "Missing"}, want: "Method [Missing] not defined for service [TransformSvc2]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := e.call(SetServicePayloadTransform, e.json, "/x", tt.body, tt.vars)
			if !strings.Contains(rr.Body.String(), tt.want) {
				t.Errorf("response = %d %q, want it to contain %q", rr.Code, rr.Body.String(), tt.want)
			}
			if _, found := lookupPayload(e.port, s.Methods["Call"].URI, nil, ""); found {
				t.Errorf("a rejected transform must not register a payload")
			}
		})
	}
}
