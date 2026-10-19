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

package jsonrpc

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func jsonMap(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal %s: %v", b, err)
	}
	return out
}

func TestNewJSONRPCRequest(t *testing.T) {
	got := jsonMap(t, NewJSONRPCRequest(7, "tools/call", map[string]any{"name": "echo"}))
	want := map[string]any{"jsonrpc": "2.0", "id": float64(7), "method": "tools/call", "params": map[string]any{"name": "echo"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("request = %v, want %v", got, want)
	}
	// params are left out when there are none
	if got := jsonMap(t, NewJSONRPCRequest(1, "ping", nil)); got["params"] != nil || got["method"] != "ping" {
		t.Errorf("request without params = %v", got)
	}
}

func TestNewJSONRPCNotificationHasNoID(t *testing.T) {
	n := NewJSONRPCNotification("initialized", nil)
	if n.ID != 0 || n.JSONRPC != "2.0" || n.Method != "initialized" {
		t.Errorf("notification = %+v", n)
	}
	if got := jsonMap(t, n); got["id"] != nil || got["method"] != "initialized" || got["jsonrpc"] != "2.0" {
		t.Errorf("notification JSON = %v, want no id", got)
	}
}

func TestNewJSONRPCResponse(t *testing.T) {
	got := jsonMap(t, NewJSONRPCResponse(3, map[string]any{"ok": true}))
	want := map[string]any{"jsonrpc": "2.0", "id": float64(3), "result": map[string]any{"ok": true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("response = %v, want %v", got, want)
	}
}

func TestNewJSONRPCError(t *testing.T) {
	r := NewJSONRPCError(4, -32601, "Method not found", map[string]any{"method": "x"})
	if r.Error == nil || r.Error.Code != -32601 || r.Error.Message != "Method not found" || r.Result != nil || r.ID != 4 {
		t.Fatalf("error response = %+v", r)
	}
	got := jsonMap(t, r)
	want := map[string]any{
		"jsonrpc": "2.0", "id": float64(4),
		"error": map[string]any{"code": float64(-32601), "message": "Method not found", "data": map[string]any{"method": "x"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("error JSON = %v, want %v", got, want)
	}
	if got := jsonMap(t, NewJSONRPCError(5, -1, "m", nil)); got["error"].(map[string]any)["data"] != nil {
		t.Errorf("error data must be left out when there is none, got %v", got)
	}
}

// mcpMethodNames are the methods of the protocol that ParseJSONRPCRequest recognizes, with the field of MCPMethods that
// each of them sets.
var mcpMethodNames = map[string]string{
	"initialize":               "Initialize",
	"initialized":              "Initialized",
	"cancelled":                "Cancelled",
	"progress":                 "Progress",
	"ping":                     "Ping",
	"tools/list":               "ToolsList",
	"tools/list_changed":       "ToolsListChanged",
	"tools/call":               "ToolsCall",
	"resources/list":           "ResourcesList",
	"resources/read":           "ResourcesRead",
	"resources/updated":        "ResourcesUpdated",
	"resources/subscribe":      "ResourcesSubscribe",
	"resources/unsubscribe":    "ResourcesUnsubscribe",
	"resources/list_changed":   "ResourcesListChanged",
	"resources/templates/list": "ResourceTemplatesList",
	"prompts/list":             "PromptsList",
	"prompts/get":              "PromptsGet",
	"prompts/list_changed":     "PromptsListChanged",
	"roots/list":               "RootsList",
	"roots/list_changed":       "RootsListChanged",
	"logging/setLevel":         "LoggingSetLevel",
	"logging/message":          "LoggingMessage",
}

func setFlags(m *MCPMethods) []string {
	var set []string
	v := reflect.ValueOf(*m)
	for i := 0; i < v.NumField(); i++ {
		if v.Field(i).Bool() {
			set = append(set, v.Type().Field(i).Name)
		}
	}
	return set
}

func TestParseJSONRPCRequestRecognizesEveryMCPMethod(t *testing.T) {
	if n := reflect.TypeOf(MCPMethods{}).NumField(); n != len(mcpMethodNames) {
		t.Fatalf("MCPMethods has %d flags and the test knows %d methods: keep them in step", n, len(mcpMethodNames))
	}
	for method, flag := range mcpMethodNames {
		t.Run(method, func(t *testing.T) {
			body := `{"jsonrpc":"2.0","id":9,"method":"` + method + `","params":{"a":1}}`
			req, err := ParseJSONRPCRequest(strings.NewReader(body))
			if err != nil {
				t.Fatalf("ParseJSONRPCRequest: %v", err)
			}
			if req.Method != method || req.ID != 9 || req.JSONRPC != "2.0" || req.Params["a"] != float64(1) {
				t.Errorf("request = %+v", req)
			}
			if got := setFlags(req.MCPMethod); !reflect.DeepEqual(got, []string{flag}) {
				t.Errorf("flags set = %v, want only %s", got, flag)
			}
		})
	}
}

func TestParseJSONRPCRequestOfOtherMethods(t *testing.T) {
	for _, method := range []string{"unknown", "", "Tools/List", "tools/list ", "tools/"} {
		req, err := ParseJSONRPCRequest(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"` + method + `"}`))
		if err != nil || req.Method != method || req.MCPMethod == nil || len(setFlags(req.MCPMethod)) != 0 {
			t.Errorf("method %q: request %+v, err %v, want a request that is none of the methods of the protocol", method, req, err)
		}
	}
}

func TestParseJSONRPCRequestOfANotification(t *testing.T) {
	req, err := ParseJSONRPCRequest(strings.NewReader(`{"jsonrpc":"2.0","method":"initialized"}`))
	if err != nil || req.ID != 0 || req.Params != nil || !req.MCPMethod.Initialized {
		t.Errorf("notification = %+v err %v", req, err)
	}
}

func TestParseJSONRPCRequestRefusesWhatIsNotARequest(t *testing.T) {
	tests := map[string]string{
		"not JSON":         `{"jsonrpc":`,
		"empty":            ``,
		"array":            `[1,2]`,
		"params as list":   `{"method":"ping","params":[1]}`,
		"id as text":       `{"method":"ping","id":"abc"}`,
		"method as number": `{"method":5}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			if req, err := ParseJSONRPCRequest(strings.NewReader(body)); err == nil {
				t.Errorf("%q must be refused, got %+v", body, req)
			}
		})
	}
}

func TestRequestRoundTrip(t *testing.T) {
	sent := NewJSONRPCRequest(12, "resources/read", map[string]any{"uri": "file:///x"})
	b, err := json.Marshal(sent)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseJSONRPCRequest(strings.NewReader(string(b)))
	if err != nil || got.ID != 12 || got.Method != "resources/read" || got.Params["uri"] != "file:///x" || !got.MCPMethod.ResourcesRead {
		t.Errorf("parsed %+v err %v from %s", got, err, b)
	}
}
