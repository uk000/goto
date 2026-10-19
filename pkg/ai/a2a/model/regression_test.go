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

package model

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	goa2a "trpc.group/trpc-go/trpc-a2a-go/protocol"
	trpcserver "trpc.group/trpc-go/trpc-a2a-go/server"
)

// An agent that was added without config (the config is optional) must still take a payload.
func TestRegressionSetPayloadOnAgentWithoutConfig(t *testing.T) {
	agent := &Agent{}
	if err := agent.SetPayload([]byte(`{"text":"hello"}`)); err != nil {
		t.Fatalf("SetPayload failed: %v", err)
	}
	if agent.Config == nil || agent.Config.ResponsePayload == nil || agent.Config.ResponsePayload.Text != "hello" {
		t.Errorf("config = %+v, want the payload to be set", agent.Config)
	}
}

// Serving through an agent that has no handler (not prepared yet) must answer instead of crashing the request.
func TestRegressionServeAgentWithoutHandler(t *testing.T) {
	rr := httptest.NewRecorder()
	(&Agent{}).Serve(rr, httptest.NewRequest("POST", "/agent/x/", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("Serve without handler = %d, want 503", rr.Code)
	}
}

// The payload of the trackers is reported to the admin API. Its counters are atomics, which have no JSON form,
// so they were reported as {}.
func TestRegressionTrackerCountsAreReported(t *testing.T) {
	tr := NewDelegateTracker()
	tr.IncrementCall()
	tr.IncrementResponse()
	tr.IncrementResponse()
	b, err := json.Marshal(tr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"callCount":1`) || !strings.Contains(string(b), `"responseCount":2`) {
		t.Errorf("tracker JSON = %s, want the counts", b)
	}
}

// What the admin API reports for an agent has to be accepted when it is posted back. The handler of the agent is
// runtime state, and was reported as {}, which can't be read into an http.Handler.
func TestRegressionAgentReportCanBePostedBack(t *testing.T) {
	agent := &Agent{Card: nil, Handler: http.NewServeMux()}
	b, err := json.Marshal(agent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &Agent{}); err != nil {
		t.Errorf("unmarshal of the reported agent failed: %v (%s)", err, b)
	}
}

// Finishing a delegate (done by the goroutines that run the delegates) and reporting its tracker (by the API)
// happen at the same time. Run with -race.
func TestRegressionTrackerFinishWhileReported(t *testing.T) {
	tr := NewDelegateTracker()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			tr.Finish()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_, _ = json.Marshal(tr)
		}
	}()
	wg.Wait()
}

// NewTextPart makes a part by value, which is what the agents get to send. Text() has to read both kinds.
func TestRegressionMessageTextOfValuePart(t *testing.T) {
	m := goa2a.NewMessage(goa2a.MessageRoleAgent, []goa2a.Part{goa2a.NewTextPart("by value")})
	if got := (&Message{Message: &m}).Text(); got != "by value" {
		t.Errorf("Text() = %q, want %q", got, "by value")
	}
}

func TestRegressionMessageTextWithoutMessage(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Text() of a message without content panicked: %v", r)
		}
	}()
	if got := (&Message{}).Text(); got != "" {
		t.Errorf("Text() = %q, want empty", got)
	}
}

// The admin API reports an agent while another request replaces its payload. Reporting read the payload without the
// lock that SetPayload writes it with, which the race detector reports.
func TestRegressionAgentReportWhileSettingPayload(t *testing.T) {
	agent := &Agent{ID: "a-1", Card: &trpcserver.AgentCard{Name: "a-1"}, Config: &AgentConfig{}}
	var wg sync.WaitGroup
	var failures atomic.Int32
	const workers, rounds = 4, 200
	for w := 0; w < workers; w++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				if err := agent.SetPayload([]byte(`{"text":"replaced"}`)); err != nil {
					failures.Add(1)
				}
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				if _, err := json.Marshal(agent); err != nil {
					failures.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if n := failures.Load(); n != 0 {
		t.Errorf("%d calls failed, want 0", n)
	}
	b, err := json.Marshal(agent)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"id":"a-1"`, `"name":"a-1"`, `"text":"replaced"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("report = %s, want it to have %s", b, want)
		}
	}
}
