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

package a2aserver

import (
	"net/http"
	"strings"
	"testing"
)

func TestEchoUnary(t *testing.T) {
	ts := newTestServer(t)
	name, _ := ts.add("echo-unary", false, nil, nil)
	resp := ts.postMessage("/agent/"+name+"/", "hello", "X-Echo-Probe", "probe-value")
	if resp.Code != http.StatusOK {
		t.Fatalf("call = %d: %s", resp.Code, resp.Body)
	}
	for _, want := range []string{"jsonrpc", "probe-value", "X-Echo-Probe", "/agent/" + name + "/"} {
		if !strings.Contains(resp.Body, want) {
			t.Errorf("echo response has no [%s]: %s", want, resp.Body)
		}
	}
	if got := resp.Header.Get("Goto-A2A-Agent"); got == "" {
		t.Errorf("headers = %v, want the agent header", resp.Header)
	}
}

func TestEchoStream(t *testing.T) {
	ts := newTestServer(t)
	name, _ := ts.add("echo-stream", true, nil, nil)
	r := ts.call(name, "hello from a stream", nil)
	if r.Err != nil {
		t.Fatalf("call failed: %v", r.Err)
	}
	if !r.has("Received Input: hello from a stream") {
		t.Errorf("texts = %v, want the echo of the input", r.Texts)
	}
	if !r.has("Echo Response Sent") {
		t.Errorf("texts = %v, want the end of the session", r.Texts)
	}
}

func TestEchoSeparateCallsAreIndependent(t *testing.T) {
	ts := newTestServer(t)
	name, _ := ts.add("echo-calls", false, nil, nil)
	first := ts.postMessage("/agent/"+name+"/", "one", "X-Call", "first-call")
	second := ts.postMessage("/agent/"+name+"/", "two", "X-Call", "second-call")
	if !strings.Contains(first.Body, "first-call") || strings.Contains(first.Body, "second-call") {
		t.Errorf("first = %s", first.Body)
	}
	if !strings.Contains(second.Body, "second-call") || strings.Contains(second.Body, "first-call") {
		t.Errorf("second = %s", second.Body)
	}
}
