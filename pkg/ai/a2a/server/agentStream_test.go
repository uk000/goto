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
	"strings"
	"sync"
	"testing"
	"time"

	a2aclient "goto/pkg/ai/a2a/client"
	"goto/pkg/ai/a2a/model"
)

// callObserving calls the agent, and tells what it sends as it is sent.
func callObserving(t *testing.T, agentURL, name, message string, onText func(string)) *reply {
	t.Helper()
	call := &a2aclient.AgentCall{Name: name, CardURL: agentURL, Message: message}
	call.Prepare()
	var mu sync.Mutex
	r := &reply{}
	result, err := a2aclient.CallAgent(ctxWithTimeout(t), 1, call, func(id, out string, data any) {
		if out == "" {
			return
		}
		mu.Lock()
		r.Texts = append(r.Texts, out)
		mu.Unlock()
		onText(out)
	}, nil)
	r.Result, r.Err = result, err
	return r
}

func countResults(r *reply) int {
	n := 0
	for _, text := range r.Texts {
		if strings.Contains(text, "Result# ") {
			n++
		}
	}
	return n
}

func TestStreamWithoutPayload(t *testing.T) {
	ts := newTestServer(t)
	name, _ := ts.add("stream-nopayload", true, &model.AgentBehavior{Stream: true}, nil)
	r := ts.call(name, "go", nil)
	if r.Err != nil || !r.has("No Response Configured") || !r.has("Stream finished") || countResults(r) != 0 {
		t.Errorf("texts = %v, err = %v, want a stream of nothing that finishes", r.Texts, r.Err)
	}
}

func TestStreamCountHintOverridesPayload(t *testing.T) {
	ts := newTestServer(t)
	name, _ := ts.addStreamAgent("stream-count", 5)
	r := ts.call(name, "go 2", nil)
	if r.Err != nil || !r.has("Result# 1 (of 2)") || !r.has("Result# 2 (of 2)") || r.has("Result# 3") {
		t.Errorf("texts = %v, err = %v, want the 2 results that were asked for", r.Texts, r.Err)
	}
}

func TestStreamPayloadCount(t *testing.T) {
	ts := newTestServer(t)
	name, _ := ts.addStreamAgent("stream-all", 4)
	r := ts.call(name, "go", nil)
	if r.Err != nil || countResults(r) != 4 || !r.has("Stream finished") {
		t.Errorf("texts = %v, err = %v, want the 4 results of the payload", r.Texts, r.Err)
	}
}

// runStreamAndStop starts a long stream, asks the agent to stop it after its first result, and returns what the stream
// sent.
func runStreamAndStop(t *testing.T, ts *testServer, name, stopMessage string) *reply {
	t.Helper()
	first := make(chan struct{})
	var once sync.Once
	done := make(chan struct{})
	var streamed *reply
	go func() {
		defer close(done)
		streamed = callObserving(t, ts.agentURL(name), name, "go", func(text string) {
			if strings.Contains(text, "Result# ") {
				once.Do(func() { close(first) })
			}
		})
	}()
	select {
	case <-first:
	case <-time.After(10 * time.Second):
		t.Fatalf("the stream sent no result")
	}
	if r := ts.call(name, stopMessage, nil); r.Err != nil {
		t.Errorf("stop failed: %v", r.Err)
	}
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatalf("the stream did not end")
	}
	return streamed
}

func TestStreamStopMidStream(t *testing.T) {
	ts := newTestServer(t)
	name, _ := ts.addStreamAgent("stream-stop", 60)
	r := runStreamAndStop(t, ts, name, "stop")
	if r.Err != nil {
		t.Errorf("stream failed: %v", r.Err)
	}
	if n := countResults(r); n < 1 || n >= 60 {
		t.Errorf("results = %d, want some of the 60 before the stop", n)
	}
	if !r.has("Stop requested") || !r.has("due to stop requested") {
		t.Errorf("texts = %v, want the stream to say that it was stopped", r.Texts)
	}
}

func TestStreamStopWithError(t *testing.T) {
	ts := newTestServer(t)
	name, _ := ts.addStreamAgent("stream-stop-error", 60)
	r := runStreamAndStop(t, ts, name, "stop with error")
	if n := countResults(r); n < 1 || n >= 60 {
		t.Errorf("results = %d, want some of the 60 before the stop", n)
	}
	if !r.has("Error Requested") {
		t.Errorf("texts = %v, err = %v, want the stream to end with the error that was asked for", r.Texts, r.Err)
	}
	// The next stream is not affected by the stop that ended this one.
	next := ts.call(name, "go 2", nil)
	if next.Err != nil || countResults(next) != 2 {
		t.Errorf("next stream = %v, err = %v, want its 2 results", next.Texts, next.Err)
	}
}
